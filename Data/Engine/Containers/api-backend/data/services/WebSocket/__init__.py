# ======================================================
# Data\Engine\services\WebSocket\__init__.py
# Description: Socket.IO handlers for Engine runtime quick job updates and VPN shell bridging.
#
# API Endpoints (if applicable): None
# ======================================================

"""WebSocket service registration for the Borealis Engine runtime."""
from __future__ import annotations

import re
import threading
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Callable, Dict, Optional

from flask import request, session
from flask_socketio import SocketIO

from ...db import dbapi as sqlite3
from ...database import initialise_engine_database
from ...security import signing
from ...server import EngineContext
from ..activity_history import (
    get_activity_history_row,
    normalize_activity_status,
    status_is_terminal,
    update_activity_history_row,
)
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


def _normalize_host_key(value: Any) -> str:
    return str(value or "").strip().lower()


def _normalize_service_mode(value: Any) -> str:
    normalized = str(value or "").strip().lower()
    if normalized in {"system", "sys"}:
        return "system"
    if normalized in {"currentuser", "current_user", "interactive", "user"}:
        return "currentuser"
    return ""


def _normalize_helper_contexts(value: Any) -> tuple[str, ...]:
    contexts = []
    if isinstance(value, (list, tuple, set)):
        candidates = list(value)
    else:
        candidates = [value]
    seen = set()
    for candidate in candidates:
        normalized = _normalize_service_mode(candidate)
        if not normalized or normalized in seen:
            continue
        seen.add(normalized)
        contexts.append(normalized)
    return tuple(contexts)


_AGENT_ID_HOST_PATTERN = re.compile(
    r"^(?P<hostname>.+)_(?P<guid>[0-9A-F-]+)_(?P<context>[A-Z0-9_-]+)$",
    re.IGNORECASE,
)


def _infer_hostname_from_agent_id(agent_id: Any) -> str:
    raw = str(agent_id or "").strip()
    if not raw:
        return ""
    match = _AGENT_ID_HOST_PATTERN.match(raw)
    if match:
        return str(match.group("hostname") or "").strip()
    parts = raw.rsplit("_", 2)
    if len(parts) == 3:
        return str(parts[0] or "").strip()
    return ""


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


@dataclass
class EngineRealtimeAdapters:
    context: EngineContext
    db_conn_factory: Callable[[], sqlite3.Connection] = field(init=False)
    service_log: Callable[[str, str, Optional[str]], None] = field(init=False)

    def __post_init__(self) -> None:
        from ..API import _make_db_conn_factory, _make_service_logger  # Local import to avoid circular import at module load

        initialise_engine_database(self.context.database_url, logger=self.context.logger)
        self.db_conn_factory = _make_db_conn_factory(
            self.context.database_url,
            sslmode=str(self.context.config.get("db_sslmode") or "prefer"),
            pool_size=int(self.context.config.get("db_pool_size") or 10),
            max_overflow=int(self.context.config.get("db_max_overflow") or 20),
            connect_timeout=int(self.context.config.get("db_connect_timeout") or 15),
            idle_in_transaction_timeout_ms=int(self.context.config.get("db_idle_in_transaction_timeout_ms") or 60000),
            logger=self.context.logger,
        )

        log_file = str(
            self.context.config.get("log_file")
            or self.context.config.get("LOG_FILE")
            or ""
        ).strip()
        if log_file:
            base = Path(log_file).resolve().parent
        else:
            base = Path.cwd() / "Engine" / "Services" / "api-backend" / "logs"
        self.service_log = _make_service_logger(base, self.context.logger)


class AgentSocketRegistry:
    def __init__(self, socketio: SocketIO, logger) -> None:
        self.socketio = socketio
        self.logger = logger
        self._sid_by_agent: Dict[str, str] = {}
        self._agent_by_sid: Dict[str, str] = {}
        self._sid_by_host_mode: Dict[tuple[str, str], str] = {}
        self._host_mode_by_sid: Dict[str, tuple[str, str]] = {}
        self._meta_by_sid: Dict[str, Dict[str, Any]] = {}

    def register(
        self,
        agent_id: str,
        sid: str,
        *,
        service_mode: str = "",
        hostname: str = "",
        helper_contexts: Any = None,
    ) -> None:
        if not agent_id or not sid:
            return
        previous = self._sid_by_agent.get(agent_id)
        if previous and previous != sid:
            self._agent_by_sid.pop(previous, None)
            route = self._host_mode_by_sid.pop(previous, None)
            if route and self._sid_by_host_mode.get(route) == previous:
                self._sid_by_host_mode.pop(route, None)
        self._sid_by_agent[agent_id] = sid
        self._agent_by_sid[sid] = agent_id
        host_key = _normalize_host_key(hostname or _infer_hostname_from_agent_id(agent_id))
        mode_key = _normalize_service_mode(service_mode)
        previous_route = self._host_mode_by_sid.pop(sid, None)
        if previous_route and self._sid_by_host_mode.get(previous_route) == sid:
            self._sid_by_host_mode.pop(previous_route, None)
        if host_key and mode_key:
            route = (host_key, mode_key)
            prior_sid = self._sid_by_host_mode.get(route)
            if prior_sid and prior_sid != sid:
                self._host_mode_by_sid.pop(prior_sid, None)
            self._sid_by_host_mode[route] = sid
            self._host_mode_by_sid[sid] = route
        self._meta_by_sid[sid] = {
            "hostname": host_key,
            "service_mode": mode_key,
            "helper_contexts": _normalize_helper_contexts(helper_contexts),
        }

    def snapshot(self) -> Dict[str, Dict[str, Any]]:
        snapshot: Dict[str, Dict[str, Any]] = {}
        for agent_id, sid in self._sid_by_agent.items():
            meta = self._meta_by_sid.get(sid, {})
            snapshot[str(agent_id)] = {
                "agent_id": str(agent_id),
                "sid": str(sid),
                "hostname": str(meta.get("hostname") or ""),
                "service_mode": str(meta.get("service_mode") or ""),
                "helper_contexts": list(_normalize_helper_contexts(meta.get("helper_contexts"))),
            }
        return snapshot

    def unregister(self, sid: str) -> Optional[str]:
        agent_id = self._agent_by_sid.pop(sid, None)
        if agent_id and self._sid_by_agent.get(agent_id) == sid:
            self._sid_by_agent.pop(agent_id, None)
        route = self._host_mode_by_sid.pop(sid, None)
        if route and self._sid_by_host_mode.get(route) == sid:
            self._sid_by_host_mode.pop(route, None)
        self._meta_by_sid.pop(sid, None)
        return agent_id

    def _resolve_sid_for_host_mode(self, hostname: str, service_mode: str) -> str:
        host_key = _normalize_host_key(hostname)
        mode_key = _normalize_service_mode(service_mode)
        if not host_key or not mode_key:
            return ""
        direct_sid = self._sid_by_host_mode.get((host_key, mode_key))
        if direct_sid:
            return direct_sid
        if mode_key == "currentuser":
            system_sid = self._sid_by_host_mode.get((host_key, "system"))
            system_meta = self._meta_by_sid.get(system_sid or "") if system_sid else None
            helper_contexts = _normalize_helper_contexts((system_meta or {}).get("helper_contexts"))
            if system_sid and "currentuser" in helper_contexts:
                return system_sid
        return ""

    def is_registered(self, agent_id: str) -> bool:
        return bool(self._sid_by_agent.get(agent_id))

    def is_host_mode_registered(self, hostname: str, service_mode: str) -> bool:
        return bool(self._resolve_sid_for_host_mode(hostname, service_mode))

    def get_agent_id_for_host_mode(self, hostname: str, service_mode: str) -> str:
        sid = self._resolve_sid_for_host_mode(hostname, service_mode)
        if not sid:
            return ""
        return str(self._agent_by_sid.get(sid) or "")

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

    def call(self, agent_id: str, event: str, payload: Any, *, timeout: float = 30.0) -> Any:
        sid = self._sid_by_agent.get(agent_id)
        if not sid or not hasattr(self.socketio, "call"):
            return None
        try:
            return self.socketio.call(event, payload, to=sid, timeout=timeout)
        except Exception:
            self.logger.debug("Failed to call %s for agent_id=%s", event, agent_id, exc_info=True)
            return None

    def emit_to_host(self, hostname: str, service_mode: str, event: str, payload: Any) -> bool:
        sid = self._resolve_sid_for_host_mode(hostname, service_mode)
        if not sid:
            return False
        try:
            self.socketio.emit(event, payload, to=sid)
            return True
        except Exception:
            self.logger.debug(
                "Failed to emit %s to hostname=%s service_mode=%s",
                event,
                hostname,
                service_mode,
                exc_info=True,
            )
            return False

    def call_to_host(self, hostname: str, service_mode: str, event: str, payload: Any, *, timeout: float = 30.0) -> Any:
        sid = self._resolve_sid_for_host_mode(hostname, service_mode)
        if not sid or not hasattr(self.socketio, "call"):
            return None
        try:
            return self.socketio.call(event, payload, to=sid, timeout=timeout)
        except Exception:
            self.logger.debug(
                "Failed to call %s for hostname=%s service_mode=%s",
                event,
                hostname,
                service_mode,
                exc_info=True,
            )
            return None


class OperatorPresenceRegistry:
    def __init__(self) -> None:
        self._lock = threading.RLock()
        self._records_by_sid: Dict[str, Dict[str, Any]] = {}

    def register_or_update(
        self,
        sid: str,
        *,
        username: str,
        role: str,
        current_page: str = "",
    ) -> bool:
        normalized_sid = str(sid or "").strip()
        normalized_username = str(username or "").strip()
        if not normalized_sid or not normalized_username:
            return False

        normalized_role = str(role or "User").strip() or "User"
        normalized_page = self._normalize_page(current_page)
        now_ts = _now_ts()
        changed = False

        with self._lock:
            existing = self._records_by_sid.get(normalized_sid)
            if existing is None:
                self._records_by_sid[normalized_sid] = {
                    "sid": normalized_sid,
                    "username": normalized_username,
                    "role": normalized_role,
                    "current_page": normalized_page,
                    "connected_at": now_ts,
                    "last_seen_at": now_ts,
                }
                changed = True
            else:
                if (
                    str(existing.get("username") or "") != normalized_username
                    or str(existing.get("role") or "") != normalized_role
                    or str(existing.get("current_page") or "") != normalized_page
                ):
                    changed = True
                existing["username"] = normalized_username
                existing["role"] = normalized_role
                existing["current_page"] = normalized_page
                existing["last_seen_at"] = now_ts
        return changed

    def remove(self, sid: str) -> Optional[Dict[str, Any]]:
        normalized_sid = str(sid or "").strip()
        if not normalized_sid:
            return None
        with self._lock:
            record = self._records_by_sid.pop(normalized_sid, None)
        return dict(record) if isinstance(record, dict) else None

    def list_sessions(self) -> list[Dict[str, Any]]:
        with self._lock:
            records = [dict(record) for record in self._records_by_sid.values()]
        return sorted(
            records,
            key=lambda item: (
                -int(item.get("last_seen_at") or 0),
                -int(item.get("connected_at") or 0),
                str(item.get("username") or "").lower(),
                str(item.get("sid") or ""),
            ),
        )

    def count_sessions(self) -> int:
        with self._lock:
            return len(self._records_by_sid)

    @staticmethod
    def _normalize_page(value: Any) -> str:
        text = str(value or "").strip()
        if not text:
            return ""
        return text[:160]


def register_realtime(socket_server: SocketIO, context: EngineContext) -> None:
    """Register Socket.IO event handlers for the Engine runtime."""

    adapters = EngineRealtimeAdapters(context)
    logger = context.logger.getChild("realtime.quick_jobs")
    agent_logger = context.logger.getChild("realtime.agents")
    operator_logger = context.logger.getChild("realtime.operators")
    shell_bridge = VpnShellBridge(socket_server, context, adapters.service_log)
    agent_registry = AgentSocketRegistry(socket_server, agent_logger)
    operator_registry = OperatorPresenceRegistry()
    setattr(context, "agent_socket_registry", agent_registry)
    setattr(context, "operator_presence_registry", operator_registry)

    def _emit_agent_event(agent_id: str, event: str, payload: Any) -> bool:
        return agent_registry.emit(agent_id, event, payload)

    def _call_agent_event(agent_id: str, event: str, payload: Any, *, timeout: float = 30.0) -> Any:
        return agent_registry.call(agent_id, event, payload, timeout=timeout)

    setattr(context, "legacy_emit_agent_event", _emit_agent_event)
    setattr(context, "legacy_call_agent_event", _call_agent_event)
    setattr(context, "legacy_emit_host_service_event", agent_registry.emit_to_host)
    setattr(context, "legacy_call_host_service_event", agent_registry.call_to_host)
    setattr(context, "legacy_has_host_service_socket", agent_registry.is_host_mode_registered)
    if not callable(getattr(context, "emit_agent_event", None)):
        setattr(context, "emit_agent_event", _emit_agent_event)
    if not callable(getattr(context, "call_agent_event", None)):
        setattr(context, "call_agent_event", _call_agent_event)
    if not callable(getattr(context, "emit_host_service_event", None)):
        setattr(context, "emit_host_service_event", agent_registry.emit_to_host)
    if not callable(getattr(context, "call_host_service_event", None)):
        setattr(context, "call_host_service_event", agent_registry.call_to_host)
    if not callable(getattr(context, "has_host_service_socket", None)):
        setattr(context, "has_host_service_socket", agent_registry.is_host_mode_registered)

    def _prewarm_vnc_credential(agent_id: str) -> None:
        normalized_agent_id = _normalize_text(agent_id)
        if not normalized_agent_id:
            return
        try:
            adapters.service_log(
                "VNC",
                "vnc_socket_prewarm_skip agent_id={0} reason=live_credential_requested_on_establish".format(
                    normalized_agent_id,
                ),
            )
        except Exception:
            agent_logger.debug(
                "Failed to write VNC prewarm skip log for agent_id=%s",
                normalized_agent_id,
                exc_info=True,
            )

    def _emit_operator_presence_changed() -> None:
        try:
            socket_server.emit("server_operator_presence_changed", {"changed_at": _now_ts()})
        except Exception:
            operator_logger.debug("Failed to emit operator presence change event.", exc_info=True)

    def _current_operator_identity() -> Optional[Dict[str, str]]:
        username = str(session.get("username") or "").strip()
        if not username:
            return None
        role = str(session.get("role") or "User").strip() or "User"
        return {"username": username, "role": role}

    def _get_tunnel_service() -> Optional[VpnTunnelService]:
        service = getattr(context, "vpn_tunnel_service", None)
        if service is not None:
            return service
        with _get_tunnel_service_init_lock(context):
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
                            acl_allowlist_ports=tuple(context.wireguard_port_allowlist),
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

    def _resolve_scheduled_run_context(
        cursor: Any,
        *,
        job_id: int,
        context_info: Optional[Dict[str, Any]],
    ) -> tuple[Optional[int], Optional[int]]:
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
        return run_id, scheduled_ts_ctx

    def _update_scheduled_run_state(
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
                adapters.service_log(
                    "scheduled_jobs",
                    f"scheduled run update skipped (no run_id) activity_id={activity_id} status={status} context={context_info}",
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
                    (ts_now, ts_now, run_id),
                )
            elif lowered == "queued":
                cursor.execute(
                    "UPDATE scheduled_job_runs SET updated_at=? WHERE id=?",
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
                    (normalized_status, ts_now, ts_now, run_id),
                )
            if scheduled_ts_ctx is not None:
                cursor.execute(
                    "UPDATE scheduled_job_runs SET scheduled_ts=COALESCE(scheduled_ts, ?) WHERE id=?",
                    (scheduled_ts_ctx, run_id),
                )
            adapters.service_log(
                "scheduled_jobs",
                f"scheduled run update run_id={run_id} activity_id={activity_id} status={normalized_status}",
            )
        except Exception as exc:  # pragma: no cover - defensive guard
            logger.debug(
                "quick_job progress update failed for activity_id=%s run_id=%s: %s",
                activity_id,
                run_id,
                exc,
            )

    @socket_server.on("quick_job_progress")
    def _handle_quick_job_progress(data: Any) -> None:
        if not isinstance(data, dict):
            logger.debug("quick_job_progress payload ignored (non-dict): %r", data)
            return

        job_id_raw = data.get("job_id")
        try:
            job_id = int(job_id_raw)
        except (TypeError, ValueError):
            logger.debug("quick_job_progress missing valid job_id: %r", job_id_raw)
            return

        normalized_status = normalize_activity_status(data.get("status"), default="Running")
        stdout = data.get("stdout")
        stderr = data.get("stderr")
        append_output = bool(data.get("append_output"))
        queue_lane = _normalize_text(data.get("queue_lane"))
        activity_kind = _normalize_text(data.get("activity_kind"))
        metadata = data.get("metadata") if isinstance(data.get("metadata"), dict) else None
        ctx_payload = data.get("context")
        context_info: Optional[Dict[str, Any]] = ctx_payload if isinstance(ctx_payload, dict) else None
        ts_now = _now_ts()

        conn: Optional[sqlite3.Connection] = None
        cursor = None
        broadcast_payload: Optional[Dict[str, Any]] = None

        try:
            conn = adapters.db_conn_factory()
            cursor = conn.cursor()
            existing_row = get_activity_history_row(conn, job_id)
            existing_metadata = existing_row.get("metadata") if isinstance(existing_row, dict) else {}
            merged_metadata = dict(existing_metadata) if isinstance(existing_metadata, dict) else {}
            if metadata:
                merged_metadata.update(metadata)
            if isinstance(context_info, dict):
                for key in ("assembly_source", "assembly_guid", "scheduled_job_id", "scheduled_job_run_id", "scheduled_ts"):
                    if key in context_info and context_info.get(key) not in (None, ""):
                        merged_metadata.setdefault(key, context_info.get(key))
            update_kwargs: Dict[str, Any] = {
                "status": normalized_status,
                "updated_at": ts_now,
            }
            if stdout is not None:
                update_kwargs["stdout"] = stdout
            if stderr is not None:
                update_kwargs["stderr"] = stderr
            if append_output:
                update_kwargs["append_output"] = True
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
            rowcount = update_activity_history_row(conn, job_id, **update_kwargs)
            if rowcount == 0:
                logger.debug("quick_job_progress missing activity_history row for job_id=%s", job_id)

            run_id, scheduled_ts_ctx = _resolve_scheduled_run_context(cursor, job_id=job_id, context_info=context_info)
            _update_scheduled_run_state(
                cursor,
                run_id=run_id,
                scheduled_ts_ctx=scheduled_ts_ctx,
                status=normalized_status,
                activity_id=job_id,
                context_info=context_info,
            )
            conn.commit()

            row = get_activity_history_row(conn, job_id)
            if row and row.get("hostname"):
                broadcast_payload = {
                    "activity_id": int(row["id"]),
                    "hostname": row.get("hostname"),
                    "status": row.get("status") or normalized_status,
                    "queue_lane": row.get("queue_lane") or queue_lane,
                    "activity_kind": row.get("activity_kind") or activity_kind,
                    "change": "updated",
                    "source": "quick_job_progress",
                }
        except Exception as exc:  # pragma: no cover - defensive guard
            logger.warning(
                "quick_job_progress handler error for job_id=%s: %s",
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
                    "Failed to emit device_activity_changed for progress job_id=%s: %s",
                    job_id,
                    exc,
                )

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
            existing_row = get_activity_history_row(conn, job_id)
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
            rowcount = update_activity_history_row(conn, job_id, **update_kwargs)
            if rowcount == 0:
                logger.debug("quick_job_result missing activity_history row for job_id=%s", job_id)
            run_id, scheduled_ts_ctx = _resolve_scheduled_run_context(cursor, job_id=job_id, context_info=context_info)
            _update_scheduled_run_state(
                cursor,
                run_id=run_id,
                scheduled_ts_ctx=scheduled_ts_ctx,
                status=status,
                activity_id=job_id,
                context_info=context_info,
            )
            conn.commit()

            row = get_activity_history_row(conn, job_id)
            if row and row.get("hostname"):
                broadcast_payload = {
                    "activity_id": int(row["id"]),
                    "hostname": row.get("hostname"),
                    "status": row.get("status") or status,
                    "queue_lane": row.get("queue_lane") or "",
                    "activity_kind": row.get("activity_kind") or "",
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
        registry = getattr(context, "agent_socket_registry", None)
        if registry and hasattr(registry, "is_registered"):
            try:
                if not registry.is_registered(agent_id):
                    # Non-blocking: shell may still be reachable when the agent socket registry
                    # is stale/unavailable (for example persistent tunnel already ensured via REST).
                    _shell_log(
                        "vpn_shell_open_warn agent_id={0} sid={1} reason=agent_socket_missing_nonblocking".format(
                            agent_id,
                            request.sid,
                        ),
                        level="WARNING",
                    )
            except Exception:
                agent_logger.debug("agent_socket_registry lookup failed for agent_id=%s", agent_id, exc_info=True)

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
        return {"status": "ok", "session_id": getattr(session, "session_id", "")}

    @socket_server.on("connect_agent")
    def _connect_agent(data: Any) -> Dict[str, Any]:
        agent_id = ""
        service_mode = ""
        hostname = ""
        helper_contexts = ()
        if isinstance(data, dict):
            agent_id = str(data.get("agent_id") or "").strip()
            service_mode = str(data.get("service_mode") or "").strip().lower()
            hostname = str(data.get("hostname") or "").strip()
            helper_contexts = _normalize_helper_contexts(
                data.get("helper_contexts")
                or (data.get("capabilities") or {}).get("helper_contexts")
                if isinstance(data.get("capabilities"), dict)
                else data.get("helper_contexts")
            )
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

        inferred_hostname = hostname or _infer_hostname_from_agent_id(agent_id)
        agent_registry.register(
            agent_id,
            request.sid,
            service_mode=service_mode,
            hostname=inferred_hostname,
            helper_contexts=helper_contexts,
        )
        agent_logger.info(
            "Agent socket registered agent_id=%s hostname=%s service_mode=%s helper_contexts=%s sid=%s",
            agent_id,
            inferred_hostname,
            service_mode,
            ",".join(helper_contexts) if helper_contexts else "-",
            request.sid,
        )
        _tunnel_log(
            "vpn_agent_socket_register agent_id={0} hostname={1} service_mode={2} helper_contexts={3} sid={4} remote={5}".format(
                agent_id,
                inferred_hostname or "-",
                service_mode or "-",
                ",".join(helper_contexts) if helper_contexts else "-",
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

        _prewarm_vnc_credential(agent_id)
        return {"status": "ok"}

    @socket_server.on("operator_presence_sync")
    def _operator_presence_sync(data: Any) -> Dict[str, Any]:
        identity = _current_operator_identity()
        if identity is None:
            return {"error": "unauthorized"}

        current_page = ""
        if isinstance(data, dict):
            current_page = str(data.get("current_page") or "").strip()
        changed = operator_registry.register_or_update(
            request.sid,
            username=identity["username"],
            role=identity["role"],
            current_page=current_page,
        )
        operator_logger.debug(
            "Operator presence synced sid=%s username=%s role=%s page=%s changed=%s",
            request.sid,
            identity["username"],
            identity["role"],
            current_page or "-",
            changed,
        )
        if changed:
            _emit_operator_presence_changed()
        return {"status": "ok"}

    @socket_server.on("operator_presence_clear")
    def _operator_presence_clear(_data: Any = None) -> Dict[str, Any]:
        removed = operator_registry.remove(request.sid)
        if removed:
            operator_logger.debug(
                "Operator presence cleared sid=%s username=%s",
                request.sid,
                removed.get("username") or "-",
            )
            _emit_operator_presence_changed()
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
        removed_operator = operator_registry.remove(request.sid)
        if removed_operator:
            operator_logger.debug(
                "Operator socket disconnected sid=%s username=%s",
                request.sid,
                removed_operator.get("username") or "-",
            )
            _emit_operator_presence_changed()
        had_shell_session = shell_bridge.close(request.sid)
        if agent_id:
            agent_logger.info("Agent socket disconnected agent_id=%s sid=%s", agent_id, request.sid)
            _tunnel_log(
                "vpn_agent_socket_disconnect agent_id={0} sid={1}".format(
                    agent_id,
                    request.sid,
                )
            )
        elif had_shell_session:
            _shell_log(
                "vpn_shell_client_disconnect sid={0} remote={1}".format(
                    request.sid,
                    _remote_addr() or "-",
                ),
                level="WARNING",
            )
