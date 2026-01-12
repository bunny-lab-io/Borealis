# ======================================================
# Data\Engine\services\WebSocket\__init__.py
# Description: Socket.IO handlers for Engine runtime quick job updates and VPN shell bridging.
#
# API Endpoints (if applicable): None
# ======================================================

"""WebSocket service registration for the Borealis Engine runtime."""
from __future__ import annotations

import sqlite3
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Callable, Dict, Optional

from flask import request
from flask_socketio import SocketIO

from ...database import initialise_engine_database
from ...security import signing
from ...server import EngineContext
from ..VPN import WireGuardServerConfig, WireGuardServerManager, VpnTunnelService
from .vpn_shell import VpnShellBridge


def _now_ts() -> int:
    return int(time.time())


def _normalize_text(value: Any) -> str:
    if value is None:
        return ""
    if isinstance(value, bytes):
        try:
            return value.decode("utf-8")
        except Exception:
            return value.decode("utf-8", errors="replace")
    return str(value)


@dataclass
class EngineRealtimeAdapters:
    context: EngineContext
    db_conn_factory: Callable[[], sqlite3.Connection] = field(init=False)
    service_log: Callable[[str, str, Optional[str]], None] = field(init=False)

    def __post_init__(self) -> None:
        from ..API import _make_db_conn_factory, _make_service_logger  # Local import to avoid circular import at module load

        initialise_engine_database(self.context.database_path, logger=self.context.logger)
        self.db_conn_factory = _make_db_conn_factory(self.context.database_path)

        log_file = str(
            self.context.config.get("log_file")
            or self.context.config.get("LOG_FILE")
            or ""
        ).strip()
        if log_file:
            base = Path(log_file).resolve().parent
        else:
            base = Path(self.context.database_path).resolve().parent
        self.service_log = _make_service_logger(base, self.context.logger)


class AgentSocketRegistry:
    def __init__(self, socketio: SocketIO, logger) -> None:
        self.socketio = socketio
        self.logger = logger
        self._sid_by_agent: Dict[str, str] = {}
        self._agent_by_sid: Dict[str, str] = {}

    def register(self, agent_id: str, sid: str) -> None:
        if not agent_id or not sid:
            return
        previous = self._sid_by_agent.get(agent_id)
        if previous and previous != sid:
            self._agent_by_sid.pop(previous, None)
        self._sid_by_agent[agent_id] = sid
        self._agent_by_sid[sid] = agent_id

    def unregister(self, sid: str) -> Optional[str]:
        agent_id = self._agent_by_sid.pop(sid, None)
        if agent_id and self._sid_by_agent.get(agent_id) == sid:
            self._sid_by_agent.pop(agent_id, None)
        return agent_id

    def emit(self, agent_id: str, event: str, payload: Any) -> bool:
        sid = self._sid_by_agent.get(agent_id)
        if not sid:
            return False
        try:
            self.socketio.emit(event, payload, to=sid)
            return True
        except Exception:
            self.logger.debug("Failed to emit %s to agent_id=%s", event, agent_id, exc_info=True)
            return False


def register_realtime(socket_server: SocketIO, context: EngineContext) -> None:
    """Register Socket.IO event handlers for the Engine runtime."""

    adapters = EngineRealtimeAdapters(context)
    logger = context.logger.getChild("realtime.quick_jobs")
    agent_logger = context.logger.getChild("realtime.agents")
    shell_bridge = VpnShellBridge(socket_server, context, adapters.service_log)
    agent_registry = AgentSocketRegistry(socket_server, agent_logger)

    def _emit_agent_event(agent_id: str, event: str, payload: Any) -> bool:
        return agent_registry.emit(agent_id, event, payload)

    setattr(context, "emit_agent_event", _emit_agent_event)

    def _get_tunnel_service() -> Optional[VpnTunnelService]:
        service = getattr(context, "vpn_tunnel_service", None)
        if service is not None:
            return service
        manager = getattr(context, "wireguard_server_manager", None)
        if manager is None:
            try:
                manager = WireGuardServerManager(
                    WireGuardServerConfig(
                        port=context.wireguard_port,
                        engine_virtual_ip=context.wireguard_engine_virtual_ip,
                        peer_network=context.wireguard_peer_network,
                        private_key_path=Path(context.wireguard_server_private_key_path),
                        public_key_path=Path(context.wireguard_server_public_key_path),
                        acl_allowlist_windows=tuple(context.wireguard_acl_allowlist_windows),
                        log_path=Path(context.vpn_tunnel_log_path),
                    )
                )
                setattr(context, "wireguard_server_manager", manager)
            except Exception:
                context.logger.error("Failed to initialize WireGuard server manager on demand.", exc_info=True)
                return None
        try:
            signer = signing.load_signer()
        except Exception:
            signer = None
        service = VpnTunnelService(
            context=context,
            wireguard_manager=manager,
            db_conn_factory=adapters.db_conn_factory,
            socketio=socket_server,
            service_log=adapters.service_log,
            signer=signer,
        )
        setattr(context, "vpn_tunnel_service", service)
        return service

    def _tunnel_log(message: str, *, level: str = "INFO") -> None:
        try:
            adapters.service_log("VPN_Tunnel/tunnel", message, level=level)
        except Exception:
            agent_logger.debug("vpn_tunnel service log write failed", exc_info=True)

    def _shell_log(message: str, *, level: str = "INFO") -> None:
        try:
            adapters.service_log("VPN_Tunnel/remote_shell", message, level=level)
        except Exception:
            agent_logger.debug("vpn_shell service log write failed", exc_info=True)

    def _remote_addr() -> str:
        forwarded = (request.headers.get("X-Forwarded-For") or "").strip()
        if forwarded:
            return forwarded.split(",")[0].strip()
        return (request.remote_addr or "").strip()

    @socket_server.on("quick_job_result")
    def _handle_quick_job_result(data: Any) -> None:
        if not isinstance(data, dict):
            logger.debug("quick_job_result payload ignored (non-dict): %r", data)
            return

        job_id_raw = data.get("job_id")
        try:
            job_id = int(job_id_raw)
        except (TypeError, ValueError):
            logger.debug("quick_job_result missing valid job_id: %r", job_id_raw)
            return

        status = str(data.get("status") or "").strip() or "Failed"
        stdout = _normalize_text(data.get("stdout"))
        stderr = _normalize_text(data.get("stderr"))

        conn: Optional[sqlite3.Connection] = None
        cursor = None
        broadcast_payload: Optional[Dict[str, Any]] = None

        ctx_payload = data.get("context")
        context_info: Optional[Dict[str, Any]] = ctx_payload if isinstance(ctx_payload, dict) else None

        try:
            conn = adapters.db_conn_factory()
            cursor = conn.cursor()
            cursor.execute(
                "UPDATE activity_history SET status=?, stdout=?, stderr=? WHERE id=?",
                (status, stdout, stderr, job_id),
            )
            if cursor.rowcount == 0:
                logger.debug("quick_job_result missing activity_history row for job_id=%s", job_id)
            conn.commit()

            try:
                cursor.execute(
                    "SELECT run_id FROM scheduled_job_run_activity WHERE activity_id=?",
                    (job_id,),
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

            if run_id is not None:
                ts_now = _now_ts()
                try:
                    if status.lower() == "running":
                        cursor.execute(
                            "UPDATE scheduled_job_runs SET status='Running', updated_at=? WHERE id=?",
                            (ts_now, run_id),
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
                            (status, ts_now, ts_now, run_id),
                        )
                    if scheduled_ts_ctx is not None:
                        cursor.execute(
                            "UPDATE scheduled_job_runs SET scheduled_ts=COALESCE(scheduled_ts, ?) WHERE id=?",
                            (scheduled_ts_ctx, run_id),
                        )
                    conn.commit()
                    adapters.service_log(
                        "scheduled_jobs",
                        f"scheduled run update run_id={run_id} activity_id={job_id} status={status}",
                    )
                except Exception as exc:  # pragma: no cover - defensive guard
                    logger.debug(
                        "quick_job_result failed to update scheduled_job_runs for job_id=%s run_id=%s: %s",
                        job_id,
                        run_id,
                        exc,
                    )
            elif context_info:
                adapters.service_log(
                    "scheduled_jobs",
                    f"scheduled run update skipped (no run_id) activity_id={job_id} status={status} context={context_info}",
                    level="WARNING",
                )

            try:
                cursor.execute(
                    "SELECT id, hostname, status FROM activity_history WHERE id=?",
                    (job_id,),
                )
                row = cursor.fetchone()
            except sqlite3.Error:
                row = None

            if row:
                hostname = (row[1] or "").strip()
                if hostname:
                    broadcast_payload = {
                        "activity_id": int(row[0]),
                        "hostname": hostname,
                        "status": row[2] or status,
                        "change": "updated",
                        "source": "quick_job",
                    }

            adapters.service_log(
                "assemblies",
                f"quick_job_result processed job_id={job_id} status={status}",
            )
        except Exception as exc:  # pragma: no cover - defensive guard
            logger.warning(
                "quick_job_result handler error for job_id=%s: %s",
                job_id,
                exc,
                exc_info=True,
            )
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

        if broadcast_payload:
            try:
                socket_server.emit("device_activity_changed", broadcast_payload)
            except Exception as exc:  # pragma: no cover - defensive guard
                logger.debug(
                    "Failed to emit device_activity_changed for job_id=%s: %s",
                    job_id,
                    exc,
                )

    @socket_server.on("vpn_shell_open")
    def _vpn_shell_open(data: Any) -> Dict[str, Any]:
        agent_id = ""
        if isinstance(data, dict):
            agent_id = str(data.get("agent_id") or "").strip()
        elif isinstance(data, str):
            agent_id = data.strip()
        if not agent_id:
            _shell_log(
                "vpn_shell_open_missing sid={0} remote={1}".format(
                    request.sid,
                    _remote_addr() or "-",
                ),
                level="WARNING",
            )
            return {"error": "agent_id_required"}

        _shell_log(
            "vpn_shell_open_request agent_id={0} sid={1} remote={2}".format(
                agent_id,
                request.sid,
                _remote_addr() or "-",
            )
        )
        service = _get_tunnel_service()
        if service is None:
            _shell_log(
                "vpn_shell_open_failed agent_id={0} sid={1} reason=vpn_service_unavailable".format(
                    agent_id,
                    request.sid,
                ),
                level="WARNING",
            )
            return {"error": "vpn_service_unavailable"}
        if not service.status(agent_id):
            _shell_log(
                "vpn_shell_open_failed agent_id={0} sid={1} reason=tunnel_down".format(
                    agent_id,
                    request.sid,
                ),
                level="WARNING",
            )
            return {"error": "tunnel_down"}

        session = shell_bridge.open_session(request.sid, agent_id)
        if session is None:
            _shell_log(
                "vpn_shell_open_failed agent_id={0} sid={1} reason=shell_connect_failed".format(
                    agent_id,
                    request.sid,
                ),
                level="WARNING",
            )
            return {"error": "shell_connect_failed"}
        service.bump_activity(agent_id)
        _shell_log(
            "vpn_shell_open_success agent_id={0} sid={1}".format(
                agent_id,
                request.sid,
            )
        )
        return {"status": "ok"}

    @socket_server.on("connect_agent")
    def _connect_agent(data: Any) -> Dict[str, Any]:
        agent_id = ""
        service_mode = ""
        if isinstance(data, dict):
            agent_id = str(data.get("agent_id") or "").strip()
            service_mode = str(data.get("service_mode") or "").strip().lower()
        elif isinstance(data, str):
            agent_id = data.strip()
        if not agent_id:
            _tunnel_log(
                "vpn_agent_socket_missing sid={0} remote={1}".format(
                    request.sid,
                    _remote_addr() or "-",
                ),
                level="WARNING",
            )
            return {"error": "agent_id_required"}

        agent_registry.register(agent_id, request.sid)
        agent_logger.info("Agent socket registered agent_id=%s service_mode=%s sid=%s", agent_id, service_mode, request.sid)
        _tunnel_log(
            "vpn_agent_socket_register agent_id={0} service_mode={1} sid={2} remote={3}".format(
                agent_id,
                service_mode or "-",
                request.sid,
                _remote_addr() or "-",
            )
        )

        service = _get_tunnel_service()
        if service:
            payload = service.session_payload(agent_id, include_token=True)
            if payload:
                if agent_registry.emit(agent_id, "vpn_tunnel_start", payload):
                    _tunnel_log(
                        "vpn_agent_socket_emit_start agent_id={0} tunnel_id={1} sid={2}".format(
                            agent_id,
                            payload.get("tunnel_id", "-"),
                            request.sid,
                        )
                    )

        return {"status": "ok"}

    @socket_server.on("vpn_shell_send")
    def _vpn_shell_send(data: Any) -> Dict[str, Any]:
        payload = None
        if isinstance(data, dict):
            payload = data.get("data")
        else:
            payload = data
        if payload is None:
            return {"error": "payload_required"}
        try:
            payload_len = len(str(payload))
        except Exception:
            payload_len = 0
        _shell_log(
            "vpn_shell_send_request sid={0} bytes={1} remote={2}".format(
                request.sid,
                payload_len,
                _remote_addr() or "-",
            )
        )
        shell_bridge.send(request.sid, str(payload))
        return {"status": "ok"}

    @socket_server.on("vpn_shell_close")
    def _vpn_shell_close(data: Any = None) -> Dict[str, Any]:
        _shell_log(
            "vpn_shell_close_request sid={0} remote={1}".format(
                request.sid,
                _remote_addr() or "-",
            )
        )
        shell_bridge.close(request.sid)
        return {"status": "ok"}

    @socket_server.on("disconnect")
    def _ws_disconnect() -> None:
        agent_id = agent_registry.unregister(request.sid)
        if agent_id:
            agent_logger.info("Agent socket disconnected agent_id=%s sid=%s", agent_id, request.sid)
            _tunnel_log(
                "vpn_agent_socket_disconnect agent_id={0} sid={1}".format(
                    agent_id,
                    request.sid,
                )
            )
        else:
            _shell_log(
                "vpn_shell_client_disconnect sid={0} remote={1}".format(
                    request.sid,
                    _remote_addr() or "-",
                ),
                level="WARNING",
            )
        shell_bridge.close(request.sid)
