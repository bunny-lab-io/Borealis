# ======================================================
# Data\Engine\services\API\devices\tunnel.py
# Description: WireGuard VPN tunnel API (connect/status).
#
# API Endpoints (if applicable):
# - POST /api/tunnel/connect (Token Authenticated) - Issues VPN session material for an agent.
# - GET /api/tunnel/status (Token Authenticated) - Returns VPN status for an agent.
# - GET /api/tunnel/active (Token Authenticated) - Lists active VPN tunnel sessions.
# ======================================================

"""WireGuard VPN tunnel API (Engine side)."""
from __future__ import annotations

import os
import re
import threading
from pathlib import Path
from typing import Any, Dict, Optional, Tuple

from flask import Blueprint, jsonify, request, session
from itsdangerous import BadSignature, SignatureExpired, URLSafeTimedSerializer

from ....auth.guid_utils import normalize_guid
from ....public_endpoints import wireguard_endpoint
from ...auth import UserSiteAccessManager
from ...auth.secrets import require_app_secret
from ...VPN import WireGuardServerConfig, WireGuardServerManager, VpnTunnelService

if False:  # pragma: no cover - import cycle hint for type checkers
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


def _get_tunnel_service_init_lock(context: Any) -> threading.Lock:
    existing = getattr(context, "_vpn_tunnel_service_init_lock", None)
    if existing is not None:
        return existing
    try:
        from eventlet import semaphore as eventlet_semaphore  # type: ignore

        lock = eventlet_semaphore.Semaphore(1)
    except Exception:
        lock = threading.Lock()
    current = getattr(context, "_vpn_tunnel_service_init_lock", None)
    if current is not None:
        return current
    setattr(context, "_vpn_tunnel_service_init_lock", lock)
    return lock


def _get_tunnel_service(adapters: "EngineServiceAdapters") -> VpnTunnelService:
    service = getattr(adapters.context, "vpn_tunnel_service", None) or getattr(adapters, "_vpn_tunnel_service", None)
    if service is None:
        with _get_tunnel_service_init_lock(adapters.context):
            service = getattr(adapters.context, "vpn_tunnel_service", None) or getattr(adapters, "_vpn_tunnel_service", None)
            if service is not None:
                return service
            manager = getattr(adapters.context, "wireguard_server_manager", None)
            if manager is None:
                try:
                    manager = WireGuardServerManager(
                        WireGuardServerConfig(
                            port=adapters.context.wireguard_port,
                            engine_virtual_ip=adapters.context.wireguard_engine_virtual_ip,
                            peer_network=adapters.context.wireguard_peer_network,
                            private_key_path=Path(adapters.context.wireguard_server_private_key_path),
                            public_key_path=Path(adapters.context.wireguard_server_public_key_path),
                            acl_allowlist_ports=tuple(adapters.context.wireguard_port_allowlist),
                            log_path=Path(adapters.context.vpn_tunnel_log_path),
                        )
                    )
                    adapters.context.wireguard_server_manager = manager
                except Exception as exc:
                    adapters.context.logger.error("Failed to initialize WireGuard server manager on demand.", exc_info=True)
                    raise RuntimeError("wireguard_manager_unavailable") from exc
            service = VpnTunnelService(
                context=adapters.context,
                wireguard_manager=manager,
                db_conn_factory=adapters.db_conn_factory,
                socketio=getattr(adapters.context, "socketio", None),
                service_log=adapters.service_log,
                signer=getattr(adapters, "script_signer", None),
            )
            setattr(adapters, "_vpn_tunnel_service", service)
            setattr(adapters.context, "vpn_tunnel_service", service)
    return service


def _normalize_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def _infer_endpoint_host(adapters: "EngineServiceAdapters", req) -> str:
    host, _port = wireguard_endpoint(adapters.context, req=req)
    return host


def _down_status_payload(agent_id: str, *, agent_socket: bool) -> Dict[str, Any]:
    return {
        "status": "down",
        "agent_id": agent_id,
        "agent_socket": agent_socket,
        "listener_healthy": False,
        "recovery_in_progress": False,
        "last_recovery_attempt_at": None,
        "last_recovery_attempt_at_iso": "",
    }


def _normalize_tunnel_status(payload: Dict[str, Any]) -> str:
    current = _normalize_text(payload.get("status")).lower()
    if current in {"up", "down", "recovering"}:
        return current
    if bool(payload.get("recovery_in_progress")) or payload.get("listener_healthy") is False:
        return "recovering"
    return "up"


def _guid_candidate(value: Any) -> str:
    text = _normalize_text(value)
    if not text:
        return ""
    normalized = normalize_guid(text)
    if not normalized:
        return ""
    return normalized if normalized == text.strip().strip("{}").upper() else ""


_AGENT_ID_HOST_PATTERN = re.compile(
    r"^(?P<hostname>.+)_(?P<guid>[0-9A-F-]+)_(?P<context>[A-Z0-9_-]+)$",
    re.IGNORECASE,
)


def _guid_from_agent_id(value: Any) -> str:
    text = _normalize_text(value)
    if not text:
        return ""
    match = _AGENT_ID_HOST_PATTERN.match(text)
    if match:
        return _guid_candidate(match.group("guid"))
    parts = text.rsplit("_", 2)
    if len(parts) == 3:
        return _guid_candidate(parts[1])
    return ""


def _infer_hostname_from_agent_id(value: Any) -> str:
    text = _normalize_text(value)
    if not text:
        return ""
    match = _AGENT_ID_HOST_PATTERN.match(text)
    if match:
        return _normalize_text(match.group("hostname"))
    parts = text.rsplit("_", 2)
    if len(parts) == 3:
        return _normalize_text(parts[0])
    return ""


def _load_device_agent_binding(
    adapters: "EngineServiceAdapters",
    *,
    guid: Any = None,
    agent_id: Any = None,
) -> Dict[str, str]:
    guid_value = _guid_candidate(guid)
    agent_id_value = _normalize_text(agent_id)
    if not guid_value and not agent_id_value:
        return {"guid": "", "hostname": "", "agent_id": ""}

    conn_factory = getattr(adapters, "db_conn_factory", None)
    if not callable(conn_factory):
        return {"guid": "", "hostname": "", "agent_id": ""}

    conn = None
    try:
        conn = conn_factory()
        cur = conn.cursor()
        if guid_value:
            cur.execute(
                "SELECT guid, hostname, agent_id FROM devices WHERE UPPER(guid) = ? ORDER BY last_seen DESC LIMIT 1",
                (guid_value,),
            )
        else:
            cur.execute(
                "SELECT guid, hostname, agent_id FROM devices WHERE LOWER(agent_id) = LOWER(?) ORDER BY last_seen DESC LIMIT 1",
                (agent_id_value,),
            )
        row = cur.fetchone()
        return {
            "guid": _guid_candidate(row[0] if row else ""),
            "hostname": _normalize_text(row[1] if row else ""),
            "agent_id": _normalize_text(row[2] if row else ""),
        }
    except Exception:
        return {"guid": "", "hostname": "", "agent_id": ""}
    finally:
        if conn is not None:
            try:
                conn.close()
            except Exception:
                pass


def _resolve_requested_agent_id(
    adapters: "EngineServiceAdapters",
    requested_agent_id: Any,
    *,
    expected_guid: Any = None,
) -> str:
    agent_id = _normalize_text(requested_agent_id)
    if not agent_id:
        return ""

    expected_guid_value = _guid_candidate(expected_guid)
    if expected_guid_value and _guid_from_agent_id(agent_id) == expected_guid_value:
        return agent_id

    guid = _guid_candidate(agent_id)
    binding = _load_device_agent_binding(adapters, guid=guid) if guid else _load_device_agent_binding(adapters, agent_id=agent_id)
    resolved = _normalize_text(binding.get("agent_id"))
    return resolved or agent_id


def _host_service_socket_available(
    adapters: "EngineServiceAdapters",
    agent_id: str,
    *,
    service_mode: str = "system",
) -> bool:
    binding = _load_device_agent_binding(adapters, agent_id=agent_id)
    hostname = _normalize_text(binding.get("hostname")) or _infer_hostname_from_agent_id(agent_id)
    if not hostname:
        return False
    checker = getattr(getattr(adapters, "context", None), "has_host_service_socket", None)
    if not callable(checker):
        return False
    try:
        return bool(checker(hostname, service_mode or "system"))
    except Exception:
        return False


def register_tunnel(app, adapters: "EngineServiceAdapters") -> None:
    blueprint = Blueprint("vpn_tunnel", __name__)
    logger = adapters.context.logger.getChild("vpn_tunnel.api")
    service_log = adapters.service_log
    site_access = UserSiteAccessManager(adapters.db_conn_factory, logger=logger)

    def _service_log_event(message: str, *, level: str = "INFO") -> None:
        if not callable(service_log):
            return
        try:
            service_log("VPN_Tunnel/tunnel", message, level=level)
        except Exception:
            logger.debug("vpn_tunnel service log write failed", exc_info=True)

    def _request_remote() -> str:
        forwarded = (request.headers.get("X-Forwarded-For") or "").strip()
        if forwarded:
            return forwarded.split(",")[0].strip()
        return (request.remote_addr or "").strip()

    @blueprint.route("/api/tunnel/connect", methods=["POST"])
    def connect_tunnel():
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

        try:
            tunnel_service = _get_tunnel_service(adapters)
            endpoint_host = _infer_endpoint_host(adapters, request)
            _service_log_event(
                "vpn_api_connect_request requested_agent_id={0} resolved_agent_id={1} operator={2} endpoint_host={3} remote={4}".format(
                    requested_agent_id,
                    agent_id,
                    operator_id or "-",
                    endpoint_host or "-",
                    _request_remote() or "-",
                )
            )
            payload = tunnel_service.connect(
                agent_id=agent_id,
                operator_id=operator_id,
                endpoint_host=endpoint_host,
            )
        except Exception as exc:
            _service_log_event(
                "vpn_api_connect_failed agent_id={0} operator={1} error={2}".format(
                    agent_id,
                    operator_id or "-",
                    str(exc),
                ),
                level="ERROR",
            )
            logger.warning("vpn connect failed for agent_id=%s: %s", agent_id, exc)
            return jsonify({"error": "connect_failed", "detail": str(exc)}), 500

        _service_log_event(
            "vpn_api_connect_response agent_id={0} tunnel_id={1} status=ok".format(
                payload.get("agent_id", agent_id),
                payload.get("tunnel_id", "-"),
            )
        )
        return jsonify(payload), 200

    @blueprint.route("/api/tunnel/status", methods=["GET"])
    def tunnel_status():
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        requested_agent_id = _normalize_text(request.args.get("agent_id") or "")
        if not requested_agent_id:
            return jsonify({"error": "agent_id_required"}), 400
        agent_id = _resolve_requested_agent_id(adapters, requested_agent_id)
        user = _current_user(app) or {}
        if not site_access.user_can_access_agent_id(user, agent_id):
            return jsonify({"error": "not found"}), 404

        tunnel_service = _get_tunnel_service(adapters)
        payload = tunnel_service.status(agent_id)
        agent_socket = _host_service_socket_available(adapters, agent_id)
        bump = _normalize_text(request.args.get("bump") or "")
        _service_log_event(
            "vpn_api_status_request requested_agent_id={0} resolved_agent_id={1} bump={2} remote={3}".format(
                requested_agent_id,
                agent_id,
                "true" if bump else "false",
                _request_remote() or "-",
            )
        )
        if not payload:
            _service_log_event(
                "vpn_api_status_response agent_id={0} status=down".format(agent_id)
            )
            return jsonify(_down_status_payload(agent_id, agent_socket=agent_socket)), 200
        payload["status"] = _normalize_tunnel_status(payload)
        payload["agent_socket"] = agent_socket
        if bump:
            tunnel_service.bump_activity(agent_id)
        _service_log_event(
            "vpn_api_status_response agent_id={0} status={1} tunnel_id={2}".format(
                agent_id,
                payload.get("status", "-"),
                payload.get("tunnel_id", "-"),
            )
        )
        return jsonify(payload), 200

    @blueprint.route("/api/tunnel/connect/status", methods=["GET"])
    def tunnel_connect_status():
        return tunnel_status()

    @blueprint.route("/api/tunnel/active", methods=["GET"])
    def tunnel_active():
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        tunnel_service = _get_tunnel_service(adapters)
        sessions = list(tunnel_service.list_sessions())
        enriched_sessions = []
        for session_payload in sessions:
            payload = dict(session_payload or {})
            agent_id = _normalize_text(payload.get("agent_id"))
            if agent_id and not site_access.user_can_access_agent_id(_current_user(app) or {}, agent_id):
                continue
            payload["agent_socket"] = _host_service_socket_available(adapters, agent_id) if agent_id else False
            payload["status"] = _normalize_tunnel_status(payload)
            enriched_sessions.append(payload)
        sessions = enriched_sessions
        _service_log_event(
            "vpn_api_active_response count={0} remote={1}".format(
                len(sessions),
                _request_remote() or "-",
            )
        )
        return jsonify({"count": len(sessions), "tunnels": sessions}), 200

    app.register_blueprint(blueprint)
