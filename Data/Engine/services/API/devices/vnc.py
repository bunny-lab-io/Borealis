# ======================================================
# Data\Engine\services\API\devices\vnc.py
# Description: VNC session bootstrap for noVNC WebSocket tunnels.
#
# API Endpoints (if applicable):
# - POST /api/vnc/establish (Token Authenticated) - Establish or join a collaboration-aware VNC session for noVNC.
# - POST /api/vnc/disconnect (Token Authenticated) - Leave or close a collaboration-aware VNC session.
# - POST /api/vnc/handoff (Token Authenticated) - Reassign session-owner metadata to another session participant.
# - GET /api/vnc/sessions (Token Authenticated) - List active collaboration-aware VNC sessions.
# - POST /api/vnc/session (Token Authenticated) - Legacy alias for establish.
# ======================================================

"""VNC session bootstrap endpoints for the Borealis Engine."""
from __future__ import annotations

import os
import socket
import time
from typing import Any, Dict, Optional, Tuple

from flask import Blueprint, jsonify, request, session
from itsdangerous import BadSignature, SignatureExpired, URLSafeTimedSerializer

from ....public_endpoints import build_websocket_url, public_vnc_path, wireguard_endpoint
from ...auth import UserSiteAccessManager
from ...auth.secrets import require_app_secret
from ...RemoteDesktop.vnc_proxy import ensure_vnc_proxy
from ...RemoteDesktop.vnc_sessions import ensure_vnc_collaboration_manager
from .tunnel import _get_tunnel_service, _resolve_requested_agent_id

if False:  # pragma: no cover - hint for type checkers
    from .. import EngineServiceAdapters


def _current_user(app) -> Optional[Dict[str, str]]:
    username = session.get("username")
    role = session.get("role") or "User"
    if username:
        return {"username": username, "role": role}

    token = None
    auth_header = request.headers.get("Authorization") or ""
    if auth_header.lower().startswith("bearer "):
        token = auth_header.split(" ", 1)[1].strip()
    if not token:
        token = request.cookies.get("borealis_auth")
    if not token:
        return None

    try:
        serializer = URLSafeTimedSerializer(require_app_secret(app), salt="borealis-auth")
        token_ttl = int(os.environ.get("BOREALIS_TOKEN_TTL_SECONDS", 60 * 60 * 24 * 30))
        data = serializer.loads(token, max_age=token_ttl)
        username = data.get("u")
        role = data.get("r") or "User"
        if username:
            return {"username": username, "role": role}
    except (BadSignature, SignatureExpired, Exception):
        return None
    return None


def _require_login(app) -> Optional[Tuple[Dict[str, Any], int]]:
    user = _current_user(app)
    if not user:
        return {"error": "unauthorized"}, 401
    return None


def _normalize_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def _normalize_bool(value: Any, default: bool = False) -> bool:
    if isinstance(value, bool):
        return value
    if isinstance(value, (int, float)):
        return bool(value)
    if isinstance(value, str):
        cleaned = value.strip().lower()
        if cleaned in {"1", "true", "yes", "y", "on"}:
            return True
        if cleaned in {"0", "false", "no", "n", "off"}:
            return False
    return default


def _coerce_timeout(value: Any, default: float) -> float:
    try:
        parsed = float(value)
    except Exception:
        return default
    if parsed <= 0:
        return default
    return parsed


def _coerce_nonnegative_timeout(value: Any, default: float) -> float:
    try:
        parsed = float(value)
    except Exception:
        return default
    if parsed < 0:
        return default
    return parsed


def _probe_tcp_listener(host: str, port: int, timeout_seconds: float) -> bool:
    host_value = _normalize_text(host)
    try:
        port_value = int(port)
    except Exception:
        return False
    if not host_value or port_value < 1 or port_value > 65535:
        return False
    try:
        with socket.create_connection((host_value, port_value), timeout=max(0.1, timeout_seconds)):
            return True
    except Exception:
        return False


def _wait_for_backend_ready(
    host: str,
    port: int,
    *,
    timeout_seconds: float,
    poll_interval_seconds: float,
) -> bool:
    deadline = time.monotonic() + max(0.5, timeout_seconds)
    while time.monotonic() < deadline:
        if _probe_tcp_listener(host, port, min(0.75, timeout_seconds)):
            return True
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            break
        time.sleep(min(max(0.1, poll_interval_seconds), remaining))
    return False


def _context_emit_agent_event(context: Any, agent_id: str, event: str, payload: Dict[str, Any]) -> bool:
    emitter = getattr(context, "emit_agent_event", None)
    if not callable(emitter):
        return False
    try:
        return bool(emitter(agent_id, event, payload))
    except Exception:
        if hasattr(context, "logger"):
            context.logger.debug("Failed to emit agent event %s for %s", event, agent_id, exc_info=True)
        return False


def register_vnc(app, adapters: "EngineServiceAdapters") -> None:
    blueprint = Blueprint("vnc", __name__)
    logger = adapters.context.logger.getChild("vnc.api")
    service_log = adapters.service_log
    site_access = UserSiteAccessManager(adapters.db_conn_factory, logger=logger)

    def _service_log_event(message: str, *, level: str = "INFO") -> None:
        if not callable(service_log):
            return
        try:
            service_log("VNC", message, level=level)
        except Exception:
            logger.debug("vnc service log write failed", exc_info=True)

    def _request_remote() -> str:
        forwarded = (request.headers.get("X-Forwarded-For") or "").strip()
        if forwarded:
            return forwarded.split(",")[0].strip()
        return (request.remote_addr or "").strip()

    def _resolve_allowed_ips(
        *,
        session_payload: Optional[Dict[str, Any]] = None,
        collaboration_session: Optional[Any] = None,
    ) -> str:
        if collaboration_session is not None:
            value = (
                getattr(collaboration_session, "allowed_ips", None)
                or getattr(collaboration_session, "engine_virtual_ip", None)
            )
            normalized = _normalize_text(value)
            if normalized:
                return normalized
        payload = session_payload if isinstance(session_payload, dict) else {}
        value = payload.get("allowed_ips") or payload.get("engine_virtual_ip")
        if isinstance(value, list):
            value = value[0] if value else ""
        return _normalize_text(value)

    def _issue_session(
        agent_id: str,
        operator_id: Optional[str],
        *,
        remove_wallpaper: bool,
    ) -> Tuple[Dict[str, Any], int]:
        manager = ensure_vnc_collaboration_manager(adapters.context, logger=logger)
        agent_credential = manager.get_agent_credential(agent_id)
        if agent_credential is None or not _normalize_text(agent_credential.controller_password):
            return {"error": "vnc_agent_credentials_unavailable"}, 503
        try:
            collaboration_session, participant, _created = manager.ensure_session(
                agent_id=agent_id,
                operator_id=operator_id or "",
                controller_password=agent_credential.controller_password,
                credential_revision=agent_credential.credential_revision,
                remove_wallpaper=remove_wallpaper,
            )
        except ValueError:
            return {"error": "operator_required"}, 401
        _ = remove_wallpaper
        tunnel_service = _get_tunnel_service(adapters)
        session_payload = tunnel_service.session_payload(agent_id, include_token=False)
        if not session_payload:
            try:
                session_payload = tunnel_service.connect(
                    agent_id=agent_id,
                    operator_id=operator_id,
                    endpoint_host=wireguard_endpoint(adapters.context, req=request)[0],
                )
            except Exception:
                return {"error": "tunnel_down"}, 409
        allowed_ips = _resolve_allowed_ips(session_payload=session_payload)
        engine_virtual_ip = _normalize_text(session_payload.get("engine_virtual_ip"))
        vnc_port = int(getattr(adapters.context, "vnc_port", 5900))
        virtual_ip = _normalize_text(session_payload.get("virtual_ip"))
        host = virtual_ip.split("/")[0] if virtual_ip else ""
        if not host:
            return {"error": "virtual_ip_missing"}, 500

        def _restart_tunnel(reason: str) -> None:
            force_restart = str(reason or "").strip().lower() == "vnc_connect_retry"
            tunnel_service.mark_transport_required(agent_id, reason=reason)
            tunnel_service.request_agent_start(
                agent_id,
                force_restart=force_restart,
                reason=reason,
            )
            if force_restart:
                tunnel_service.recover_transport(
                    agent_id,
                    trigger="vnc_connect",
                    reason=reason,
                )

        def _confirm_transport(reason: str) -> None:
            tunnel_service.confirm_transport_success(agent_id, reason=reason)

        def _emit_vnc_start(reason: str) -> None:
            _context_emit_agent_event(
                adapters.context,
                agent_id,
                "vnc_start",
                {
                    "agent_id": agent_id,
                    "session_id": collaboration_session.session_id,
                    "controller_password": collaboration_session.controller_password,
                    "view_only_password": "",
                    "port": int(getattr(adapters.context, "vnc_port", 5900)),
                    "allowed_ips": allowed_ips,
                    "remove_wallpaper": bool(remove_wallpaper),
                    "credential_revision": collaboration_session.credential_revision,
                    "reason": reason,
                },
            )

        fast_ready = _wait_for_backend_ready(
            host,
            vnc_port,
            timeout_seconds=_coerce_timeout(
                os.environ.get("BOREALIS_VNC_FAST_READY_WAIT_SECONDS"),
                0.75,
            ),
            poll_interval_seconds=_coerce_timeout(
                os.environ.get("BOREALIS_VNC_FAST_READY_POLL_INTERVAL_SECONDS"),
                0.15,
            ),
        )
        initial_ready = fast_ready
        if fast_ready:
            _service_log_event(
                "vnc_backend_fast_ready agent_id={0} session_id={1} credential_revision={2}".format(
                    agent_id,
                    collaboration_session.session_id,
                    collaboration_session.credential_revision,
                )
            )
        else:
            _service_log_event(
                "vnc_backend_bootstrap_required agent_id={0} session_id={1} credential_revision={2}".format(
                    agent_id,
                    collaboration_session.session_id,
                    collaboration_session.credential_revision,
                )
            )
            try:
                _restart_tunnel("vnc_bootstrap")
                _emit_vnc_start("vnc_bootstrap")
                settle_seconds = _coerce_nonnegative_timeout(
                    os.environ.get("BOREALIS_VNC_BOOTSTRAP_SETTLE_SECONDS"),
                    0.0,
                )
                if _created and settle_seconds > 0:
                    time.sleep(settle_seconds)
            except Exception:
                logger.debug("Failed to re-emit vpn_tunnel_start before VNC bootstrap", exc_info=True)

            initial_ready = _wait_for_backend_ready(
                host,
                vnc_port,
                timeout_seconds=_coerce_timeout(
                    os.environ.get("BOREALIS_VNC_READY_WAIT_SECONDS"),
                    12.0,
                ),
                poll_interval_seconds=_coerce_timeout(
                    os.environ.get("BOREALIS_VNC_READY_POLL_INTERVAL_SECONDS"),
                    0.35,
                ),
            )
        if not initial_ready:
            try:
                _restart_tunnel("vnc_connect_retry")
                _emit_vnc_start("vnc_connect_retry")
            except Exception:
                logger.debug("Failed to request VNC connect retry agent_id=%s", agent_id, exc_info=True)
            initial_ready = _wait_for_backend_ready(
                host,
                vnc_port,
                timeout_seconds=_coerce_timeout(
                    os.environ.get("BOREALIS_VNC_RETRY_READY_WAIT_SECONDS"),
                    8.0,
                ),
                poll_interval_seconds=_coerce_timeout(
                    os.environ.get("BOREALIS_VNC_READY_POLL_INTERVAL_SECONDS"),
                    0.35,
                ),
            )
        if not initial_ready:
            manager.record_error(collaboration_session.session_id, "backend_not_ready")
            return {"error": "vnc_backend_unavailable"}, 503
        manager.record_backend_ready(
            collaboration_session.session_id,
            tunnel_id=_normalize_text(session_payload.get("tunnel_id")),
            allowed_ips=allowed_ips,
            engine_virtual_ip=engine_virtual_ip,
        )
        try:
            _confirm_transport("vnc_backend_ready")
        except Exception:
            logger.debug("Failed to confirm VNC backend readiness agent_id=%s", agent_id, exc_info=True)

        registry = ensure_vnc_proxy(adapters.context, logger=logger)
        if registry is None:
            return {"error": "vnc_proxy_unavailable"}, 503

        _service_log_event(
            "vnc_establish_request agent_id={0} operator={1} role={2} session_id={3} remote={4}".format(
                agent_id,
                operator_id or "-",
                participant.role,
                collaboration_session.session_id,
                _request_remote() or "-",
            )
        )

        vnc_session = registry.create(
            agent_id=agent_id,
            host=host,
            port=vnc_port,
            operator_id=operator_id,
            session_id=collaboration_session.session_id,
            participant_id=participant.participant_id,
            role=participant.role,
            restart_tunnel=_restart_tunnel,
            confirm_transport=_confirm_transport,
            on_open=lambda: manager.record_proxy_open(
                collaboration_session.session_id,
                participant.participant_id,
            ),
            on_close=lambda reason: manager.record_proxy_close(
                collaboration_session.session_id,
                participant.participant_id,
                reason=reason,
            ),
        )
        ws_path = public_vnc_path(adapters.context)
        ws_url = build_websocket_url(
            adapters.context,
            request,
            ws_path,
            query={"token": vnc_session.token},
        )

        _service_log_event(
            "vnc_session_ready agent_id={0} session_id={1} role={2} credential_revision={3}".format(
                agent_id,
                collaboration_session.session_id,
                participant.role,
                collaboration_session.credential_revision,
            )
        )

        session_snapshot = manager.session_snapshot(
            collaboration_session,
            current_operator_id=operator_id or "",
        )
        vnc_password = collaboration_session.controller_password

        return (
            {
                "ws_url": ws_url,
                "ws_path": ws_path,
                "token": vnc_session.token,
                "session_id": collaboration_session.session_id,
                "participant_id": participant.participant_id,
                "participant_role": participant.role,
                "view_only": False,
                "session_state": collaboration_session.state,
                "controller_operator_id": collaboration_session.controller_operator_id or "",
                "credential_revision": collaboration_session.credential_revision,
                "session": session_snapshot,
                "virtual_ip": host,
                "tunnel_id": session_payload.get("tunnel_id"),
                "engine_virtual_ip": session_payload.get("engine_virtual_ip"),
                "vnc_password": vnc_password,
                "vnc_port": vnc_port,
            },
            200,
        )

    @blueprint.route("/api/vnc/establish", methods=["POST"])
    def vnc_establish():
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        user = _current_user(app) or {}
        operator_id = user.get("username") or None

        body = request.get_json(silent=True) or {}
        requested_agent_id = _normalize_text(body.get("agent_id"))

        if not requested_agent_id:
            return jsonify({"error": "agent_id_required"}), 400
        agent_id = _resolve_requested_agent_id(adapters, requested_agent_id)
        if not site_access.user_can_access_agent_id(user, agent_id):
            return jsonify({"error": "not found"}), 404

        remove_wallpaper = _normalize_bool(body.get("remove_wallpaper"), default=True)

        payload, status = _issue_session(
            agent_id,
            operator_id,
            remove_wallpaper=remove_wallpaper,
        )
        return jsonify(payload), status

    @blueprint.route("/api/vnc/session", methods=["POST"])
    def vnc_session():
        return vnc_establish()

    @blueprint.route("/api/vnc/disconnect", methods=["POST"])
    def vnc_disconnect():
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        user = _current_user(app) or {}
        operator_id = user.get("username") or None

        body = request.get_json(silent=True) or {}
        agent_id = _normalize_text(body.get("agent_id"))
        session_id = _normalize_text(body.get("session_id"))
        reason = _normalize_text(body.get("reason") or "operator_disconnect")
        close_session = _normalize_bool(body.get("close_session"), default=False)

        manager = ensure_vnc_collaboration_manager(adapters.context, logger=logger)
        collaboration_session = manager.get_session_by_id(session_id) if session_id else None
        resolved_agent_id = collaboration_session.agent_id if collaboration_session is not None else ""
        if not resolved_agent_id:
            if not agent_id:
                return jsonify({"error": "agent_id_or_session_id_required"}), 400
            resolved_agent_id = _resolve_requested_agent_id(adapters, agent_id)
            collaboration_session = manager.get_session_for_agent(resolved_agent_id)
        if not site_access.user_can_access_agent_id(user, resolved_agent_id):
            return jsonify({"error": "not found"}), 404
        if collaboration_session is None:
            return jsonify({"error": "session_not_found"}), 404

        current_snapshot = manager.session_snapshot(
            collaboration_session,
            current_operator_id=operator_id or "",
        )
        user_is_admin = str(user.get("role") or "").strip().lower() == "admin"
        if close_session and not (
            user_is_admin
            or (_normalize_text(operator_id) and _normalize_text(operator_id) == _normalize_text(collaboration_session.controller_operator_id))
        ):
            return jsonify({"error": "controller_required"}), 403

        try:
            if close_session and user_is_admin and current_snapshot.get("current_operator_role") != "controller":
                result = manager.close_session(
                    session_id=collaboration_session.session_id,
                    reason=reason or "session_closed",
                )
                result["closed"] = True
                result["controller_vacant"] = False
                result["participant_id"] = ""
            else:
                result = manager.leave_or_close(
                    session_id=collaboration_session.session_id,
                    operator_id=operator_id or "",
                    close_session=close_session,
                )
        except PermissionError:
            return jsonify({"error": "participant_required"}), 403
        except KeyError:
            return jsonify({"error": "session_not_found"}), 404

        proxy = getattr(adapters.context, "vnc_proxy", None)
        if bool(result.get("closed")):
            if proxy is not None and hasattr(proxy, "disconnect_session"):
                try:
                    proxy.disconnect_session(
                        collaboration_session.session_id,
                        reason=reason or "session_closed",
                    )
                except Exception:
                    logger.debug("Failed to disconnect VNC session %s", collaboration_session.session_id, exc_info=True)
            _context_emit_agent_event(
                adapters.context,
                resolved_agent_id,
                "vnc_stop",
                {"agent_id": resolved_agent_id, "reason": reason or "session_closed"},
            )
        else:
            left_participant_id = _normalize_text(result.get("participant_id"))
            if proxy is not None and hasattr(proxy, "disconnect_participant") and left_participant_id:
                try:
                    proxy.disconnect_participant(
                        collaboration_session.session_id,
                        left_participant_id,
                        reason=reason or "participant_left",
                    )
                except Exception:
                    logger.debug(
                        "Failed to disconnect VNC participant %s/%s",
                        collaboration_session.session_id,
                        left_participant_id,
                        exc_info=True,
                    )
            if bool(result.get("reconnect_pending")):
                _context_emit_agent_event(
                    adapters.context,
                    resolved_agent_id,
                    "vnc_stop",
                    {"agent_id": resolved_agent_id, "reason": reason or "operator_disconnect"},
                )
            if bool(result.get("controller_vacant")):
                refreshed_session = manager.get_session_by_id(collaboration_session.session_id)
                if refreshed_session is not None:
                    tunnel_service = _get_tunnel_service(adapters)
                    refreshed_payload = None
                    if hasattr(tunnel_service, "session_payload"):
                        refreshed_payload = tunnel_service.session_payload(
                            resolved_agent_id,
                            include_token=False,
                        )
                    _context_emit_agent_event(
                        adapters.context,
                        resolved_agent_id,
                        "vnc_start",
                        {
                            "agent_id": resolved_agent_id,
                            "session_id": refreshed_session.session_id,
                            "controller_password": refreshed_session.controller_password,
                            "view_only_password": "",
                            "port": int(getattr(adapters.context, "vnc_port", 5900)),
                            "allowed_ips": _resolve_allowed_ips(
                                session_payload=refreshed_payload,
                                collaboration_session=refreshed_session,
                            ),
                            "remove_wallpaper": bool(refreshed_session.remove_wallpaper),
                            "credential_revision": refreshed_session.credential_revision,
                            "reason": "controller_vacated",
                        },
                    )
                    if proxy is not None and hasattr(proxy, "disconnect_session"):
                        try:
                            proxy.disconnect_session(
                                refreshed_session.session_id,
                                reason="controller_reconnect_required",
                            )
                        except Exception:
                            logger.debug(
                                "Failed to force VNC reconnect after controller leave %s",
                                refreshed_session.session_id,
                                exc_info=True,
                            )

        _service_log_event(
            "vnc_disconnect agent_id={0} operator={1} session_id={2} close_session={3} reason={4}".format(
                resolved_agent_id,
                operator_id or "-",
                collaboration_session.session_id,
                str(bool(close_session)).lower(),
                reason or "-",
            )
        )

        response_payload = {
            "status": "closed" if bool(result.get("closed")) else "left",
            "reason": reason,
            "session_id": collaboration_session.session_id,
            "controller_vacant": bool(result.get("controller_vacant")),
            "reconnect_pending": bool(result.get("reconnect_pending")),
        }
        if not bool(result.get("closed")):
            refreshed_session = manager.get_session_by_id(collaboration_session.session_id)
            if refreshed_session is not None:
                response_payload["session"] = manager.session_snapshot(
                    refreshed_session,
                    current_operator_id=operator_id or "",
                )
        return jsonify(response_payload), 200

    @blueprint.route("/api/vnc/handoff", methods=["POST"])
    def vnc_handoff():
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        user = _current_user(app) or {}
        operator_id = user.get("username") or None
        body = request.get_json(silent=True) or {}
        session_id = _normalize_text(body.get("session_id"))
        target_operator_id = _normalize_text(body.get("target_operator_id"))
        if not session_id:
            return jsonify({"error": "session_id_required"}), 400

        manager = ensure_vnc_collaboration_manager(adapters.context, logger=logger)
        collaboration_session = manager.get_session_by_id(session_id)
        if collaboration_session is None:
            return jsonify({"error": "session_not_found"}), 404
        if not site_access.user_can_access_agent_id(user, collaboration_session.agent_id):
            return jsonify({"error": "not found"}), 404

        try:
            handoff_result = manager.handoff(
                session_id=session_id,
                actor_operator_id=operator_id or "",
                target_operator_id=target_operator_id or None,
            )
        except PermissionError:
            return jsonify({"error": "controller_required"}), 403
        except KeyError as exc:
            error_key = str(exc).strip("'")
            return jsonify({"error": error_key or "target_not_found"}), 404
        except ValueError:
            return jsonify({"error": "target_already_controller"}), 409

        refreshed_session = handoff_result["session"]
        tunnel_service = _get_tunnel_service(adapters)
        session_payload = None
        if hasattr(tunnel_service, "session_payload"):
            session_payload = tunnel_service.session_payload(refreshed_session.agent_id, include_token=False)
        allowed_ips = _resolve_allowed_ips(
            session_payload=session_payload if isinstance(session_payload, dict) else None,
            collaboration_session=refreshed_session,
        )

        _service_log_event(
            "vnc_handoff agent_id={0} session_id={1} from={2} to={3}".format(
                refreshed_session.agent_id,
                refreshed_session.session_id,
                operator_id or "-",
                refreshed_session.controller_operator_id or "-",
            )
        )
        return (
            jsonify(
                {
                    "status": "ok",
                    "participant_role": manager.session_snapshot(
                        refreshed_session,
                        current_operator_id=operator_id or "",
                    ).get("current_operator_role")
                    or "",
                    "session": manager.session_snapshot(
                        refreshed_session,
                        current_operator_id=operator_id or "",
                    ),
                    "reconnect_required": False,
                    "allowed_ips": allowed_ips,
                }
            ),
            200,
        )

    @blueprint.route("/api/vnc/sessions", methods=["GET"])
    def vnc_sessions():
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        user = _current_user(app) or {}
        operator_id = user.get("username") or ""
        requested_agent_id = _normalize_text(request.args.get("agent_id"))
        requested_session_id = _normalize_text(request.args.get("session_id"))
        manager = ensure_vnc_collaboration_manager(adapters.context, logger=logger)
        sessions = manager.list_sessions()
        if requested_session_id:
            sessions = [session for session in sessions if session.session_id == requested_session_id]
        if requested_agent_id:
            resolved_agent_id = _resolve_requested_agent_id(adapters, requested_agent_id)
            if not site_access.user_can_access_agent_id(user, resolved_agent_id):
                return jsonify({"error": "not found"}), 404
            sessions = [session for session in sessions if session.agent_id == resolved_agent_id]
        visible_sessions = [
            manager.session_snapshot(session, current_operator_id=operator_id)
            for session in sessions
            if site_access.user_can_access_agent_id(user, session.agent_id)
        ]
        return jsonify({"sessions": visible_sessions, "count": len(visible_sessions)}), 200

    app.register_blueprint(blueprint)
