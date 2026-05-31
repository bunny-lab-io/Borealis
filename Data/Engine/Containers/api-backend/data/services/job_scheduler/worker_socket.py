"""Site-worker Socket.IO runtime for Agent remote-operation channels."""

from __future__ import annotations

import importlib
import importlib.util
import logging
import os
import socket
import threading
import time
from contextlib import closing
from typing import Any, Callable, Dict, Mapping, Optional

import jwt
import requests
from flask import Flask, jsonify, request
from flask_socketio import SocketIO

from Data.Engine.auth import device_purge_state, jwt_service as jwt_service_module
from Data.Engine.auth.guid_utils import normalize_guid
from Data.Engine.db import dbapi as sqlite3
from Data.Engine.services.WebSocket.vpn_shell import (
    _CONNECT_TIMEOUT_SECONDS,
    _CONNECT_WAIT_WINDOW_SECONDS,
    _REEMIT_START_AFTER_SECONDS,
    _RETRY_DELAY_SECONDS,
    ShellSession,
    _configure_tcp_socket,
    _cooperative_sleep,
)
from Data.Engine.services.activity_history import (
    get_activity_history_row,
    normalize_activity_status,
    status_is_terminal,
    update_activity_history_row,
)
from Data.Engine.services.remote_ops.agent_socket_registry import (
    AgentSocketRegistry,
    infer_guid_from_agent_id,
    infer_hostname_from_agent_id,
    normalize_helper_contexts,
)
from Data.Engine.services.remote_ops.sessions import RemoteOpSessionError, verify_remote_op_session
from Data.Engine.services.RemoteDesktop.guacamole_proxy import (
    DEFAULT_GUACD_HOST,
    DEFAULT_GUACD_PORT,
    GUACAMOLE_WS_PATH,
    GuacamoleSessionRegistry,
    normalize_guacamole_performance_preference,
)
from Data.Engine.services.RemoteDesktop.vnc_proxy import VncProxyServer
from Data.Engine.services.job_scheduler.security import INTERNAL_TOKEN_HEADER, internal_token, validate_internal_token


def _now_ts() -> int:
    return int(time.time())


def _resolve_socketio_async_mode() -> str:
    requested = str(os.environ.get("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE") or "eventlet").strip().lower() or "eventlet"
    if requested != "eventlet":
        return requested
    try:
        importlib.util.find_spec("engineio.async_drivers.eventlet")
        importlib.import_module("engineio.async_drivers.eventlet")
        import eventlet  # type: ignore

        eventlet.monkey_patch(thread=False)
        return "eventlet"
    except Exception:
        return "threading"


def _remote_addr() -> str:
    forwarded = (request.headers.get("X-Forwarded-For") or "").strip()
    if forwarded:
        return forwarded.split(",")[0].strip()
    return (request.remote_addr or "").strip()


def _assert_port_available(host: str, port: int) -> None:
    with closing(socket.socket(socket.AF_INET, socket.SOCK_STREAM)) as sock:
        sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        sock.bind((host, int(port)))


class SocketAgentAuthError(Exception):
    def __init__(self, code: str, *, status_code: int = 401) -> None:
        super().__init__(code)
        self.code = code
        self.status_code = status_code


def _bearer_token() -> str:
    auth_header = request.headers.get("Authorization", "")
    if not auth_header.startswith("Bearer "):
        return ""
    return auth_header[len("Bearer ") :].strip()


def _authenticate_socket_agent(
    *,
    db_conn_factory: Callable[[], sqlite3.Connection],
    jwt_service: Any,
    worker_site_id: int,
    agent_id: str,
) -> Dict[str, Any]:
    token = _bearer_token()
    if not token:
        raise SocketAgentAuthError("missing_authorization")
    try:
        claims = jwt_service.decode(token)
    except jwt.ExpiredSignatureError:
        raise SocketAgentAuthError("token_expired")
    except Exception:
        raise SocketAgentAuthError("invalid_token")

    guid = normalize_guid(str(claims.get("guid") or "").strip())
    fingerprint = str(claims.get("ssl_key_fingerprint") or "").lower().strip()
    try:
        token_version = int(claims.get("token_version") or 0)
    except Exception:
        token_version = 0
    if not guid or not fingerprint or token_version <= 0:
        raise SocketAgentAuthError("invalid_claims")

    agent_guid = normalize_guid(infer_guid_from_agent_id(agent_id))
    if agent_guid and agent_guid != guid:
        raise SocketAgentAuthError("agent_id_guid_mismatch", status_code=403)

    with closing(db_conn_factory()) as conn:
        cur = conn.cursor()
        required_token_version = device_purge_state.get_required_token_version(cur, guid)
        if required_token_version is not None and token_version < required_token_version:
            raise SocketAgentAuthError("device_purged")
        cur.execute(
            """
            SELECT d.guid, d.ssl_key_fingerprint, d.token_version, d.status, d.hostname, ds.site_id
              FROM devices AS d
         LEFT JOIN device_sites AS ds ON ds.device_hostname = d.hostname
             WHERE UPPER(d.guid) = ?
            """,
            (guid,),
        )
        rows = cur.fetchall()
    row = None
    for candidate in rows or []:
        if normalize_guid(candidate[0]) == guid:
            row = candidate
            break
    if row is None and rows:
        row = rows[0]
    if row is None:
        raise SocketAgentAuthError("device_not_found", status_code=404)
    stored_guid, stored_fingerprint, stored_version, status, hostname, site_id = row
    if normalize_guid(stored_guid) != guid:
        raise SocketAgentAuthError("device_mismatch")
    if str(stored_fingerprint or "").lower().strip() != fingerprint:
        raise SocketAgentAuthError("fingerprint_mismatch", status_code=403)
    if int(stored_version or 0) != token_version:
        raise SocketAgentAuthError("token_version_mismatch", status_code=403)
    if str(status or "active").strip().lower() in {"revoked", "decommissioned"}:
        raise SocketAgentAuthError("device_revoked", status_code=403)
    try:
        device_site_id = int(site_id) if site_id is not None else 0
    except Exception:
        device_site_id = 0
    if device_site_id <= 0:
        raise SocketAgentAuthError("device_site_unassigned", status_code=403)
    if int(worker_site_id or 0) != device_site_id:
        raise SocketAgentAuthError("device_site_mismatch", status_code=403)
    return {
        "guid": guid,
        "hostname": str(hostname or ""),
        "site_id": device_site_id,
        "claims": claims,
    }


class SiteWorkerSocketRuntime:
    def __init__(
        self,
        *,
        worker_guid: str,
        site_id: int,
        host: str,
        port: int,
        guacamole_host: str = "127.0.0.1",
        guacamole_port: int = 0,
        internal_secret: str = "",
        internal_api_base_url: str = "",
        db_conn_factory: Callable[[], sqlite3.Connection],
        logger: logging.Logger,
        service_log: Callable[[str, str, Optional[str]], None],
    ) -> None:
        self.worker_guid = str(worker_guid or "")
        self.site_id = int(site_id or 0)
        self.host = str(host or "127.0.0.1")
        self.port = int(port or 0)
        self.guacamole_host = str(guacamole_host or self.host or "127.0.0.1")
        self.guacamole_port = int(guacamole_port or 0)
        self.internal_secret = str(internal_secret or os.environ.get("BOREALIS_ENGINE_SECRET") or "")
        self.internal_api_base_url = str(
            internal_api_base_url or os.environ.get("BOREALIS_INTERNAL_API_BASE_URL") or "http://127.0.0.1:5000"
        ).rstrip("/")
        self.db_conn_factory = db_conn_factory
        self.logger = logger
        self.service_log = service_log
        self.app = Flask(f"borealis_site_worker_{self.worker_guid}")
        self.app.secret_key = str(os.environ.get("BOREALIS_ENGINE_SECRET") or "site-worker")
        self.socketio = SocketIO(
            self.app,
            cors_allowed_origins="*",
            async_mode=_resolve_socketio_async_mode(),
            engineio_options={
                "max_http_buffer_size": 100_000_000,
                "max_websocket_message_size": 100_000_000,
            },
        )
        self.registry = AgentSocketRegistry(self.socketio, logger.getChild("agent_registry"))
        self.jwt_service = jwt_service_module.load_service()
        try:
            guacamole_ttl_seconds = int(os.environ.get("BOREALIS_VNC_SESSION_TTL_SECONDS") or "120")
        except Exception:
            guacamole_ttl_seconds = 120
        self._guacamole_registry = GuacamoleSessionRegistry(
            ttl_seconds=guacamole_ttl_seconds,
            logger=logger.getChild("guacamole_registry"),
        )
        self._guacamole_proxy: Optional[VncProxyServer] = None
        self._guacamole_lock = threading.RLock()
        self._shell_sessions: Dict[str, ShellSession] = {}
        self._shell_sessions_by_agent: Dict[str, ShellSession] = {}
        self._shell_lock = threading.RLock()
        self._thread: Optional[threading.Thread] = None
        self._register_routes()
        self._register_socket_handlers()
        self._register_shell_handlers()
        self._register_remote_desktop_routes()
        self._register_task_event_handlers()

    def _log(self, message: str, *, level: str = "INFO") -> None:
        try:
            self.service_log("site_worker_remote_ops", message, scope=self.worker_guid, level=level)
        except Exception:
            self.logger.debug("site-worker remote ops service log failed", exc_info=True)

    def _register_routes(self) -> None:
        @self.app.route("/health", methods=["GET"])
        def _health():
            return jsonify({"status": "ok", "worker_guid": self.worker_guid, "site_id": self.site_id})

        @self.app.route("/agents", methods=["GET"])
        def _agents():
            return jsonify({"agents": self.registry.snapshot(), "worker_guid": self.worker_guid, "site_id": self.site_id})

    def _require_internal_request(self) -> Optional[tuple[Dict[str, Any], int]]:
        if validate_internal_token(self.internal_secret, request.headers.get(INTERNAL_TOKEN_HEADER)):
            return None
        self._log(
            "remote_desktop_internal_rejected remote={0}".format(_remote_addr() or "-"),
            level="WARNING",
        )
        return {"error": "unauthorized"}, 401

    def _ensure_guacamole_proxy(self) -> bool:
        if self.guacamole_port <= 0:
            return False
        with self._guacamole_lock:
            if self._guacamole_proxy is None:
                try:
                    guacd_port = int(os.environ.get("BOREALIS_GUACD_PORT") or DEFAULT_GUACD_PORT)
                except Exception:
                    guacd_port = DEFAULT_GUACD_PORT
                self._guacamole_proxy = VncProxyServer(
                    host=self.guacamole_host,
                    port=self.guacamole_port,
                    guacamole_registry=self._guacamole_registry,
                    logger=self.logger.getChild("guacamole_proxy"),
                    guacamole_path=GUACAMOLE_WS_PATH,
                    guacd_host=str(os.environ.get("BOREALIS_GUACD_HOST") or DEFAULT_GUACD_HOST),
                    guacd_port=guacd_port,
                    ssl_context=None,
                )
            return bool(self._guacamole_proxy.ensure_started())

    def _notify_vnc_session_event(
        self,
        *,
        event: str,
        agent_id: str,
        session_id: str,
        participant_id: str,
        reason: str = "",
    ) -> None:
        if not self.internal_secret or not self.internal_api_base_url:
            return
        try:
            response = requests.post(
                f"{self.internal_api_base_url}/api/internal/vnc/session-event",
                headers={INTERNAL_TOKEN_HEADER: internal_token(self.internal_secret)},
                json={
                    "event": str(event or "").strip(),
                    "agent_id": str(agent_id or "").strip(),
                    "session_id": str(session_id or "").strip(),
                    "participant_id": str(participant_id or "").strip(),
                    "reason": str(reason or "").strip(),
                    "worker_guid": self.worker_guid,
                    "site_id": self.site_id,
                },
                timeout=5.0,
            )
            if response.status_code >= 400:
                self._log(
                    "remote_desktop_session_event_failed event={0} session_id={1} status={2}".format(
                        event or "-",
                        session_id or "-",
                        response.status_code,
                    ),
                    level="WARNING",
                )
        except Exception as exc:
            self._log(
                "remote_desktop_session_event_failed event={0} session_id={1} error={2}".format(
                    event or "-",
                    session_id or "-",
                    str(exc)[:160],
                ),
                level="WARNING",
            )

    def _register_remote_desktop_routes(self) -> None:
        @self.app.route("/remote-desktop/vnc/session", methods=["POST"])
        def _remote_desktop_session():
            requirement = self._require_internal_request()
            if requirement:
                payload, status = requirement
                return jsonify(payload), status
            if not self._ensure_guacamole_proxy():
                return jsonify({"error": "guacamole_proxy_unavailable"}), 503

            data = request.get_json(silent=True) or {}
            if not isinstance(data, Mapping):
                data = {}
            token = str(data.get("operation_token") or data.get("remote_op_token") or "").strip()
            try:
                claims = verify_remote_op_session(
                    self.jwt_service,
                    token,
                    required_capability="remote_desktop",
                    worker_guid=self.worker_guid,
                    site_id=self.site_id,
                )
            except RemoteOpSessionError as exc:
                self._log(
                    "remote_desktop_token_rejected reason={0} remote={1}".format(
                        exc.code,
                        _remote_addr() or "-",
                    ),
                    level="WARNING",
                )
                return jsonify({"error": exc.code}), 403

            agent_id = str(data.get("agent_id") or claims.get("agent_id") or "").strip()
            if not agent_id:
                return jsonify({"error": "agent_id_required"}), 400
            if agent_id != str(claims.get("agent_id") or "").strip():
                return jsonify({"error": "device_mismatch"}), 403

            host = str(data.get("host") or data.get("virtual_ip") or "").split("/")[0].strip()
            try:
                port = int(data.get("port") or 5900)
            except Exception:
                port = 5900
            password = str(data.get("password") or "").strip()[:8]
            session_id = str(data.get("session_id") or "").strip()
            participant_id = str(data.get("participant_id") or "").strip()
            operator_id = str(data.get("operator_id") or "").strip()
            role = str(data.get("role") or "").strip()
            if not host or port <= 0 or not password or not session_id or not participant_id:
                return jsonify({"error": "invalid_session_payload"}), 400
            try:
                width = int(data.get("width") or 1024)
            except Exception:
                width = 1024
            try:
                height = int(data.get("height") or 768)
            except Exception:
                height = 768
            try:
                dpi = int(data.get("dpi") or 96)
            except Exception:
                dpi = 96
            performance_preference = normalize_guacamole_performance_preference(data.get("performance_preference"))

            guacamole_session = self._guacamole_registry.create(
                agent_id=agent_id,
                host=host,
                port=port,
                password=password,
                operator_id=operator_id,
                session_id=session_id,
                participant_id=participant_id,
                role=role,
                width=width,
                height=height,
                dpi=dpi,
                performance_preference=performance_preference,
                confirm_transport=lambda reason: self._notify_vnc_session_event(
                    event="transport_confirm",
                    agent_id=agent_id,
                    session_id=session_id,
                    participant_id=participant_id,
                    reason=reason,
                ),
                on_open=lambda: self._notify_vnc_session_event(
                    event="open",
                    agent_id=agent_id,
                    session_id=session_id,
                    participant_id=participant_id,
                ),
                on_close=lambda reason: self._notify_vnc_session_event(
                    event="close",
                    agent_id=agent_id,
                    session_id=session_id,
                    participant_id=participant_id,
                    reason=reason,
                ),
            )
            self._log(
                "remote_desktop_session_registered agent_id={0} session_id={1} participant_id={2} host={3} port={4}".format(
                    agent_id,
                    session_id,
                    participant_id,
                    host,
                    port,
                )
            )
            return jsonify({"status": "ok", "token": guacamole_session.token}), 200

        @self.app.route("/remote-desktop/vnc/disconnect", methods=["POST"])
        def _remote_desktop_disconnect():
            requirement = self._require_internal_request()
            if requirement:
                payload, status = requirement
                return jsonify(payload), status
            data = request.get_json(silent=True) or {}
            if not isinstance(data, Mapping):
                data = {}
            session_id = str(data.get("session_id") or "").strip()
            participant_id = str(data.get("participant_id") or "").strip()
            reason = str(data.get("reason") or "session_closed").strip()
            close_session = bool(data.get("close_session"))
            if not session_id:
                return jsonify({"error": "session_id_required"}), 400
            proxy = self._guacamole_proxy
            disconnected = 0
            if proxy is not None:
                if close_session:
                    disconnected = proxy.disconnect_session(session_id, reason=reason)
                elif participant_id:
                    disconnected = proxy.disconnect_participant(session_id, participant_id, reason=reason)
            return jsonify({"status": "ok", "disconnected": disconnected}), 200

    def _register_socket_handlers(self) -> None:
        @self.socketio.on("connect_agent")
        def _connect_agent(data: Any) -> Dict[str, Any]:
            agent_id = ""
            service_mode = ""
            hostname = ""
            helper_contexts = ()
            if isinstance(data, dict):
                agent_id = str(data.get("agent_id") or "").strip()
                service_mode = str(data.get("service_mode") or "").strip().lower()
                hostname = str(data.get("hostname") or "").strip()
                capabilities = data.get("capabilities") if isinstance(data.get("capabilities"), Mapping) else {}
                helper_contexts = normalize_helper_contexts(data.get("helper_contexts") or capabilities.get("helper_contexts"))
            elif isinstance(data, str):
                agent_id = data.strip()
            if not agent_id:
                self._log(
                    "agent_socket_missing sid={0} remote={1}".format(request.sid, _remote_addr() or "-"),
                    level="WARNING",
                )
                return {"error": "agent_id_required"}
            try:
                auth_context = _authenticate_socket_agent(
                    db_conn_factory=self.db_conn_factory,
                    jwt_service=self.jwt_service,
                    worker_site_id=self.site_id,
                    agent_id=agent_id,
                )
            except SocketAgentAuthError as exc:
                self._log(
                    "agent_socket_rejected agent_id={0} sid={1} reason={2} remote={3}".format(
                        agent_id,
                        request.sid,
                        exc.code,
                        _remote_addr() or "-",
                    ),
                    level="WARNING",
                )
                return {"error": exc.code, "status_code": exc.status_code}

            inferred_hostname = hostname or str(auth_context.get("hostname") or "") or infer_hostname_from_agent_id(agent_id)
            self.registry.register(
                agent_id,
                request.sid,
                service_mode=service_mode,
                hostname=inferred_hostname,
                helper_contexts=helper_contexts,
                guid=str(auth_context.get("guid") or ""),
            )
            self.logger.info(
                "Site-worker Agent socket registered worker_guid=%s site_id=%s agent_id=%s hostname=%s service_mode=%s helper_contexts=%s sid=%s",
                self.worker_guid,
                self.site_id,
                agent_id,
                inferred_hostname,
                service_mode,
                ",".join(helper_contexts) if helper_contexts else "-",
                request.sid,
            )
            self._log(
                "agent_socket_register agent_id={0} hostname={1} service_mode={2} helper_contexts={3} sid={4} remote={5}".format(
                    agent_id,
                    inferred_hostname or "-",
                    service_mode or "-",
                    ",".join(helper_contexts) if helper_contexts else "-",
                    request.sid,
                    _remote_addr() or "-",
                )
            )
            return {"status": "ok", "worker_guid": self.worker_guid, "site_id": self.site_id}

        @self.socketio.on("disconnect")
        def _disconnect() -> None:
            agent_id = self.registry.unregister(request.sid)
            shell_closed = self._close_shell_session(request.sid, reason="operator_socket_disconnect")
            if agent_id:
                self.logger.info(
                    "Site-worker Agent socket disconnected worker_guid=%s site_id=%s agent_id=%s sid=%s",
                    self.worker_guid,
                    self.site_id,
                    agent_id,
                    request.sid,
                )
                self._log("agent_socket_disconnect agent_id={0} sid={1}".format(agent_id, request.sid))
            elif shell_closed:
                self._log("vpn_shell_client_disconnect sid={0} remote={1}".format(request.sid, _remote_addr() or "-"))

    def _remote_op_token_from_payload(self, data: Any) -> str:
        if isinstance(data, Mapping):
            for key in ("operation_token", "remote_op_token", "token"):
                token = str(data.get(key) or "").strip()
                if token:
                    return token
        auth_header = request.headers.get("Authorization", "")
        if auth_header.startswith("Bearer "):
            return auth_header[len("Bearer ") :].strip()
        return ""

    def _verify_shell_operation(self, data: Any) -> Dict[str, Any]:
        token = self._remote_op_token_from_payload(data)
        try:
            return verify_remote_op_session(
                self.jwt_service,
                token,
                required_capability="remote_shell",
                worker_guid=self.worker_guid,
                site_id=self.site_id,
            )
        except RemoteOpSessionError as exc:
            self._log(
                "vpn_shell_token_rejected sid={0} reason={1} remote={2}".format(
                    request.sid,
                    exc.code,
                    _remote_addr() or "-",
                ),
                level="WARNING",
            )
            raise

    def _lookup_shell_virtual_ip(self, agent_id: str) -> str:
        clean_agent_id = str(agent_id or "").strip()
        if not clean_agent_id:
            return ""
        try:
            with closing(self.db_conn_factory()) as conn:
                cur = conn.cursor()
                cur.execute(
                    "SELECT virtual_ip FROM device_vpn_ip_leases WHERE agent_id=?",
                    (clean_agent_id,),
                )
                row = cur.fetchone()
        except Exception:
            self.logger.debug("site-worker shell VPN lease lookup failed agent_id=%s", clean_agent_id, exc_info=True)
            return ""
        if not row:
            return ""
        return str(row[0] or "").split("/")[0].strip()

    def _shell_port(self, data: Any) -> int:
        candidates = []
        if isinstance(data, Mapping):
            candidates.append(data.get("shell_port"))
            tunnel = data.get("tunnel") if isinstance(data.get("tunnel"), Mapping) else {}
            candidates.append(tunnel.get("shell_port"))
        candidates.append(os.environ.get("BOREALIS_WIREGUARD_SHELL_PORT"))
        for candidate in candidates:
            try:
                port = int(str(candidate or "").strip())
            except Exception:
                port = 0
            if port > 0:
                return port
        return 47002

    def _tunnel_payload(self, data: Any, *, agent_id: str, virtual_ip: str) -> Dict[str, Any]:
        if not isinstance(data, Mapping) or not isinstance(data.get("tunnel"), Mapping):
            return {}
        payload = dict(data.get("tunnel") or {})
        for key in ("remote_ops_session", "operation_token", "remote_op_token", "shell_port", "status", "agent_socket"):
            payload.pop(key, None)
        if str(payload.get("agent_id") or "").strip() != agent_id:
            return {}
        payload_virtual_ip = str(payload.get("virtual_ip") or "").split("/")[0].strip()
        if payload_virtual_ip and payload_virtual_ip != virtual_ip:
            return {}
        return payload

    def _remove_shell_session(self, sid: str, session: ShellSession) -> None:
        with self._shell_lock:
            if self._shell_sessions.get(sid) is session:
                self._shell_sessions.pop(sid, None)
            if self._shell_sessions_by_agent.get(session.agent_id) is session:
                self._shell_sessions_by_agent.pop(session.agent_id, None)

    def _close_shell_session(self, sid: str, *, reason: str = "close_request") -> bool:
        with self._shell_lock:
            session = self._shell_sessions.pop(str(sid or ""), None)
            if session is not None and self._shell_sessions_by_agent.get(session.agent_id) is session:
                self._shell_sessions_by_agent.pop(session.agent_id, None)
        if session is None:
            return False
        session.close_with_reason(reason)
        return True

    def _close_agent_shell_session(self, agent_id: str, *, reason: str = "superseded_agent_session") -> bool:
        with self._shell_lock:
            session = self._shell_sessions_by_agent.pop(str(agent_id or ""), None)
            if session is not None and self._shell_sessions.get(session.sid) is session:
                self._shell_sessions.pop(session.sid, None)
        if session is None:
            return False
        session.close_with_reason(reason)
        return True

    def _register_shell_handlers(self) -> None:
        @self.socketio.on("vpn_shell_open")
        def _vpn_shell_open(data: Any) -> Dict[str, Any]:
            if not isinstance(data, Mapping):
                data = {}
            try:
                claims = self._verify_shell_operation(data)
            except RemoteOpSessionError as exc:
                return {"error": exc.code}
            agent_id = str(data.get("agent_id") or claims.get("agent_id") or "").strip()
            if not agent_id:
                return {"error": "agent_id_required"}
            if agent_id != str(claims.get("agent_id") or "").strip():
                return {"error": "device_mismatch"}
            virtual_ip = self._lookup_shell_virtual_ip(agent_id)
            if not virtual_ip:
                self._log(
                    "vpn_shell_open_failed agent_id={0} sid={1} reason=vpn_lease_missing".format(
                        agent_id,
                        request.sid,
                    ),
                    level="WARNING",
                )
                return {"error": "tunnel_down"}
            port = self._shell_port(data)
            tunnel_payload = self._tunnel_payload(data, agent_id=agent_id, virtual_ip=virtual_ip)
            self._close_shell_session(request.sid, reason="superseded_open")
            self._close_agent_shell_session(agent_id, reason="superseded_agent_session")
            session = self._open_shell_session(request.sid, agent_id, virtual_ip, port, tunnel_payload)
            if session is None:
                return {"error": "shell_connect_failed"}
            return {"status": "ok", "session_id": getattr(session, "session_id", "")}

        @self.socketio.on("vpn_shell_send")
        def _vpn_shell_send(data: Any) -> Dict[str, Any]:
            payload = data.get("data") if isinstance(data, Mapping) else data
            if payload is None:
                return {"error": "payload_required"}
            with self._shell_lock:
                session = self._shell_sessions.get(request.sid)
            if session is None or not session.is_active():
                self._close_shell_session(request.sid, reason="inactive_send")
                return {"error": "shell_session_missing"}
            session.send(str(payload))
            return {"status": "ok"}

        @self.socketio.on("vpn_shell_close")
        def _vpn_shell_close(_data: Any = None) -> Dict[str, Any]:
            self._close_shell_session(request.sid, reason="close_request")
            return {"status": "ok"}

    def _emit_shell_agent_start(self, agent_id: str, tunnel_payload: Mapping[str, Any], *, trigger_after: float) -> None:
        if not tunnel_payload:
            return
        emitted = self.registry.emit(agent_id, "vpn_tunnel_start", dict(tunnel_payload))
        self._log(
            "vpn_shell_agent_start_emit agent_id={0} sid={1} trigger_elapsed={2} emitted={3}".format(
                agent_id,
                request.sid,
                int(trigger_after),
                str(bool(emitted)).lower(),
            ),
            level="INFO" if emitted else "WARNING",
        )

    def _open_shell_session(
        self,
        sid: str,
        agent_id: str,
        virtual_ip: str,
        port: int,
        tunnel_payload: Mapping[str, Any],
    ) -> Optional[ShellSession]:
        deadline = time.monotonic() + _CONNECT_WAIT_WINDOW_SECONDS
        reemit_index = 0
        attempts = 0
        last_error: Optional[Exception] = None
        started_at = time.monotonic()
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                break
            elapsed = max(0.0, time.monotonic() - started_at)
            while reemit_index < len(_REEMIT_START_AFTER_SECONDS):
                trigger_after = _REEMIT_START_AFTER_SECONDS[reemit_index]
                if elapsed + 0.001 < trigger_after:
                    break
                self._emit_shell_agent_start(agent_id, tunnel_payload, trigger_after=trigger_after)
                reemit_index += 1
            attempts += 1
            try:
                tcp = socket.create_connection((virtual_ip, int(port)), timeout=min(_CONNECT_TIMEOUT_SECONDS, max(0.5, remaining)))
                _configure_tcp_socket(tcp)
            except Exception as exc:
                last_error = exc
                remaining = deadline - time.monotonic()
                if remaining > 0:
                    _cooperative_sleep(min(_RETRY_DELAY_SECONDS, remaining))
                continue
            session = ShellSession(
                sid=sid,
                agent_id=agent_id,
                socketio=self.socketio,
                tcp=tcp,
                service_log=self.service_log,
                on_closed=lambda closed_sid, closed_session: self._remove_shell_session(closed_sid, closed_session),
            )
            try:
                session.tcp.settimeout(15)
            except Exception:
                pass
            with self._shell_lock:
                self._shell_sessions[sid] = session
                self._shell_sessions_by_agent[agent_id] = session
            self._log(
                "vpn_shell_open_success agent_id={0} sid={1} host={2} port={3} attempts={4}".format(
                    agent_id,
                    sid,
                    virtual_ip,
                    port,
                    attempts,
                )
            )
            session.start_reader()
            if session.wait_for_ready(timeout=min(2.0, max(0.5, deadline - time.monotonic()))):
                return session
            self._remove_shell_session(sid, session)
            session.close_with_reason("ready_probe_failed")
            last_error = RuntimeError("shell_ready_probe_failed")
        self._log(
            "vpn_shell_open_failed agent_id={0} sid={1} host={2} port={3} attempts={4} error={5}".format(
                agent_id,
                sid,
                virtual_ip,
                port,
                attempts,
                str(last_error) if last_error else "-",
            ),
            level="WARNING",
        )
        return None

    def _resolve_scheduled_run_context(
        self,
        cursor: Any,
        *,
        activity_id: int,
        context_info: Optional[Dict[str, Any]],
    ) -> tuple[Optional[int], Optional[int]]:
        try:
            cursor.execute(
                "SELECT run_id FROM scheduled_job_run_activity WHERE activity_id=?",
                (int(activity_id),),
            )
            link = cursor.fetchone()
        except sqlite3.Error:
            link = None
        run_id: Optional[int] = None
        scheduled_ts_ctx: Optional[int] = None
        if link:
            try:
                run_id = int(link[0])
            except Exception:
                run_id = None
        if run_id is None and context_info:
            ctx_run = context_info.get("scheduled_job_run_id") or context_info.get("run_id")
            try:
                if ctx_run is not None:
                    run_id = int(ctx_run)
            except (TypeError, ValueError):
                run_id = None
            try:
                if context_info.get("scheduled_ts") is not None:
                    scheduled_ts_ctx = int(context_info.get("scheduled_ts"))
            except (TypeError, ValueError):
                scheduled_ts_ctx = None
        return run_id, scheduled_ts_ctx

    def _update_scheduled_run_state(
        self,
        cursor: Any,
        *,
        run_id: Optional[int],
        scheduled_ts_ctx: Optional[int],
        status: str,
        activity_id: int,
        context_info: Optional[Dict[str, Any]] = None,
    ) -> None:
        if run_id is None:
            if context_info:
                self._log(
                    f"scheduled_run_update_skipped activity_id={activity_id} status={status} context={context_info}",
                    level="WARNING",
                )
            return
        ts_now = _now_ts()
        normalized_status = normalize_activity_status(status, default="Failed")
        lowered = normalized_status.lower()
        try:
            if lowered == "running":
                cursor.execute(
                    """
                    UPDATE scheduled_job_runs
                       SET status='Running',
                           started_ts=COALESCE(started_ts, ?),
                           updated_at=?
                     WHERE id=?
                    """,
                    (ts_now, ts_now, int(run_id)),
                )
            elif lowered == "queued":
                cursor.execute(
                    "UPDATE scheduled_job_runs SET updated_at=? WHERE id=?",
                    (ts_now, int(run_id)),
                )
            else:
                cursor.execute(
                    """
                    UPDATE scheduled_job_runs
                       SET status=?,
                           finished_ts=COALESCE(finished_ts, ?),
                           updated_at=?
                     WHERE id=?
                    """,
                    (normalized_status, ts_now, ts_now, int(run_id)),
                )
            if scheduled_ts_ctx is not None:
                cursor.execute(
                    "UPDATE scheduled_job_runs SET scheduled_ts=COALESCE(scheduled_ts, ?) WHERE id=?",
                    (int(scheduled_ts_ctx), int(run_id)),
                )
        except Exception:
            self.logger.debug(
                "site-worker task event run update failed activity_id=%s run_id=%s",
                activity_id,
                run_id,
                exc_info=True,
            )

    def _register_task_event_handlers(self) -> None:
        @self.socketio.on("quick_job_progress")
        def _handle_quick_job_progress(data: Any) -> None:
            if not isinstance(data, dict):
                return
            try:
                activity_id = int(data.get("job_id"))
            except (TypeError, ValueError):
                return
            normalized_status = normalize_activity_status(data.get("status"), default="Running")
            ctx_payload = data.get("context")
            context_info: Optional[Dict[str, Any]] = ctx_payload if isinstance(ctx_payload, dict) else None
            metadata = data.get("metadata") if isinstance(data.get("metadata"), dict) else None
            conn: Optional[sqlite3.Connection] = None
            cursor = None
            try:
                conn = self.db_conn_factory()
                cursor = conn.cursor()
                existing_row = get_activity_history_row(conn, activity_id)
                existing_metadata = existing_row.get("metadata") if isinstance(existing_row, dict) else {}
                merged_metadata = dict(existing_metadata) if isinstance(existing_metadata, dict) else {}
                if metadata:
                    merged_metadata.update(metadata)
                if isinstance(context_info, dict):
                    for key in ("assembly_source", "assembly_guid", "scheduled_job_id", "scheduled_job_run_id", "scheduled_ts"):
                        if key in context_info and context_info.get(key) not in (None, ""):
                            merged_metadata.setdefault(key, context_info.get(key))
                ts_now = _now_ts()
                update_kwargs: Dict[str, Any] = {
                    "status": normalized_status,
                    "updated_at": ts_now,
                }
                if data.get("stdout") is not None:
                    update_kwargs["stdout"] = data.get("stdout")
                if data.get("stderr") is not None:
                    update_kwargs["stderr"] = data.get("stderr")
                if bool(data.get("append_output")):
                    update_kwargs["append_output"] = True
                queue_lane = str(data.get("queue_lane") or "")
                activity_kind = str(data.get("activity_kind") or "")
                if queue_lane:
                    update_kwargs["queue_lane"] = queue_lane
                if activity_kind:
                    update_kwargs["activity_kind"] = activity_kind
                if merged_metadata:
                    update_kwargs["metadata"] = merged_metadata
                if normalized_status.lower() == "running":
                    update_kwargs["started_at"] = ts_now
                if status_is_terminal(normalized_status):
                    update_kwargs["finished_at"] = ts_now
                update_activity_history_row(conn, activity_id, **update_kwargs)
                run_id, scheduled_ts_ctx = self._resolve_scheduled_run_context(
                    cursor,
                    activity_id=activity_id,
                    context_info=context_info,
                )
                self._update_scheduled_run_state(
                    cursor,
                    run_id=run_id,
                    scheduled_ts_ctx=scheduled_ts_ctx,
                    status=normalized_status,
                    activity_id=activity_id,
                    context_info=context_info,
                )
                conn.commit()
            except Exception:
                self.logger.warning("site-worker quick_job_progress handler failed activity_id=%s", activity_id, exc_info=True)
            finally:
                if cursor is not None:
                    try:
                        cursor.close()
                    except Exception:
                        pass
                if conn is not None:
                    try:
                        conn.close()
                    except Exception:
                        pass

        @self.socketio.on("quick_job_result")
        def _handle_quick_job_result(data: Any) -> None:
            if not isinstance(data, dict):
                return
            try:
                activity_id = int(data.get("job_id"))
            except (TypeError, ValueError):
                return
            status = str(data.get("status") or "").strip() or "Failed"
            stdout = str(data.get("stdout") or "")
            stderr = str(data.get("stderr") or "")
            ctx_payload = data.get("context")
            context_info: Optional[Dict[str, Any]] = ctx_payload if isinstance(ctx_payload, dict) else None
            conn: Optional[sqlite3.Connection] = None
            cursor = None
            try:
                conn = self.db_conn_factory()
                cursor = conn.cursor()
                existing_row = get_activity_history_row(conn, activity_id)
                existing_metadata = existing_row.get("metadata") if isinstance(existing_row, dict) else {}
                merged_metadata = dict(existing_metadata) if isinstance(existing_metadata, dict) else {}
                if isinstance(context_info, dict):
                    for key in ("assembly_source", "assembly_guid", "scheduled_job_id", "scheduled_job_run_id", "scheduled_ts"):
                        if key in context_info and context_info.get(key) not in (None, ""):
                            merged_metadata.setdefault(key, context_info.get(key))
                result_ts = _now_ts()
                update_kwargs: Dict[str, Any] = {
                    "status": status,
                    "stdout": stdout,
                    "stderr": stderr,
                    "updated_at": result_ts,
                    "finished_at": result_ts,
                }
                if merged_metadata:
                    update_kwargs["metadata"] = merged_metadata
                update_activity_history_row(conn, activity_id, **update_kwargs)
                run_id, scheduled_ts_ctx = self._resolve_scheduled_run_context(
                    cursor,
                    activity_id=activity_id,
                    context_info=context_info,
                )
                self._update_scheduled_run_state(
                    cursor,
                    run_id=run_id,
                    scheduled_ts_ctx=scheduled_ts_ctx,
                    status=status,
                    activity_id=activity_id,
                    context_info=context_info,
                )
                conn.commit()
                self._log(f"quick_job_result_processed activity_id={activity_id} status={status}")
            except Exception:
                self.logger.warning("site-worker quick_job_result handler failed activity_id=%s", activity_id, exc_info=True)
            finally:
                if cursor is not None:
                    try:
                        cursor.close()
                    except Exception:
                        pass
                if conn is not None:
                    try:
                        conn.close()
                    except Exception:
                        pass

    def start(self) -> None:
        if self._thread and self._thread.is_alive():
            return
        if self.port <= 0:
            raise RuntimeError("site-worker remote ops port is required")
        _assert_port_available(self.host, self.port)
        self._thread = threading.Thread(target=self._run, name=f"site-worker-socket-{self.worker_guid}", daemon=True)
        self._thread.start()
        self._log(
            "socket_runtime_start worker_guid={0} site_id={1} host={2} port={3} async_mode={4}".format(
                self.worker_guid,
                self.site_id,
                self.host,
                self.port,
                getattr(self.socketio, "async_mode", "-"),
            )
        )

    def _run(self) -> None:
        run_kwargs: Dict[str, Any] = {"host": self.host, "port": self.port}
        if getattr(self.socketio, "async_mode", "") == "threading":
            run_kwargs["allow_unsafe_werkzeug"] = True
        try:
            self.socketio.run(self.app, **run_kwargs)
        except BaseException:
            self.logger.exception("site-worker Socket.IO runtime crashed")
            raise

    def emit_host_service_event(self, hostname: str, service_mode: str, event_name: str, payload: Any) -> bool:
        return self.registry.emit_to_host(hostname, service_mode, event_name, payload)

    def call_host_service_event(
        self,
        hostname: str,
        service_mode: str,
        event_name: str,
        payload: Any,
        *,
        timeout: float = 30.0,
    ) -> Any:
        return self.registry.call_to_host(hostname, service_mode, event_name, payload, timeout=timeout)

    def has_host_service_socket(self, hostname: str, service_mode: str) -> bool:
        return self.registry.is_host_mode_registered(hostname, service_mode)

    def has_registered_agents(self) -> bool:
        return bool(self.registry.snapshot())


__all__ = ["SiteWorkerSocketRuntime", "SocketAgentAuthError"]
