from __future__ import annotations

import logging
import time
from dataclasses import dataclass
from typing import Any, Dict, Mapping, Optional, Tuple

from Data.Engine.repositories.sqlite import SQLiteDeviceRepository

__all__ = ["AgentRealtimeService", "AgentRecord"]


@dataclass(slots=True)
class AgentRecord:
    """In-memory representation of a connected agent."""

    agent_id: str
    hostname: str = "unknown"
    agent_operating_system: str = "-"
    last_seen: int = 0
    status: str = "orphaned"
    service_mode: str = "currentuser"
    is_script_agent: bool = False
    collector_active_ts: Optional[float] = None


class AgentRealtimeService:
    """Track realtime agent presence and provide persistence hooks."""

    def __init__(
        self,
        *,
        device_repository: SQLiteDeviceRepository,
        logger: Optional[logging.Logger] = None,
    ) -> None:
        self._device_repository = device_repository
        self._log = logger or logging.getLogger("borealis.engine.services.realtime.agents")
        self._agents: Dict[str, AgentRecord] = {}
        self._configs: Dict[str, Dict[str, Any]] = {}
        self._screenshots: Dict[str, Dict[str, Any]] = {}
        self._task_screenshots: Dict[Tuple[str, str], Dict[str, Any]] = {}

    # ------------------------------------------------------------------
    # Agent presence management
    # ------------------------------------------------------------------
    def register_connection(self, agent_id: str, service_mode: Optional[str]) -> AgentRecord:
        record = self._agents.get(agent_id) or AgentRecord(agent_id=agent_id)
        mode = self.normalize_service_mode(service_mode, agent_id)
        now = int(time.time())

        record.service_mode = mode
        record.is_script_agent = self._is_script_agent(agent_id)
        record.last_seen = now
        record.status = "provisioned" if agent_id in self._configs else "orphaned"
        self._agents[agent_id] = record

        self._persist_activity(
            hostname=record.hostname,
            last_seen=record.last_seen,
            agent_id=agent_id,
            operating_system=record.agent_operating_system,
        )
        return record

    def heartbeat(self, payload: Mapping[str, Any]) -> Optional[AgentRecord]:
        if not payload:
            return None

        agent_id = payload.get("agent_id")
        if not agent_id:
            return None

        hostname = payload.get("hostname") or ""
        mode = self.normalize_service_mode(payload.get("service_mode"), agent_id)
        is_script_agent = self._is_script_agent(agent_id)
        last_seen = self._coerce_int(payload.get("last_seen"), default=int(time.time()))
        operating_system = (payload.get("agent_operating_system") or "-").strip() or "-"

        if hostname:
            self._reconcile_hostname_collisions(
                hostname=hostname,
                agent_id=agent_id,
                incoming_mode=mode,
                is_script_agent=is_script_agent,
                last_seen=last_seen,
            )

        record = self._agents.get(agent_id) or AgentRecord(agent_id=agent_id)
        if hostname:
            record.hostname = hostname
        record.agent_operating_system = operating_system
        record.last_seen = last_seen
        record.service_mode = mode
        record.is_script_agent = is_script_agent
        record.status = "provisioned" if agent_id in self._configs else record.status or "orphaned"
        self._agents[agent_id] = record

        self._persist_activity(
            hostname=record.hostname or hostname,
            last_seen=record.last_seen,
            agent_id=agent_id,
            operating_system=record.agent_operating_system,
        )
        return record

    def collector_status(self, payload: Mapping[str, Any]) -> None:
        if not payload:
            return

        agent_id = payload.get("agent_id")
        if not agent_id:
            return

        hostname = payload.get("hostname") or ""
        mode = self.normalize_service_mode(payload.get("service_mode"), agent_id)
        active = bool(payload.get("active"))
        last_user = (payload.get("last_user") or "").strip()

        record = self._agents.get(agent_id) or AgentRecord(agent_id=agent_id)
        if hostname:
            record.hostname = hostname
        if mode:
            record.service_mode = mode
        record.is_script_agent = self._is_script_agent(agent_id) or record.is_script_agent
        if active:
            record.collector_active_ts = time.time()
        self._agents[agent_id] = record

        if (
            last_user
            and hostname
            and self._is_valid_interactive_user(last_user)
            and not self._is_system_service_agent(agent_id, record.is_script_agent)
        ):
            self._persist_activity(
                hostname=hostname,
                last_seen=int(time.time()),
                agent_id=agent_id,
                operating_system=record.agent_operating_system,
                last_user=last_user,
            )

    # ------------------------------------------------------------------
    # Configuration management
    # ------------------------------------------------------------------
    def set_agent_config(self, agent_id: str, config: Mapping[str, Any]) -> None:
        self._configs[agent_id] = dict(config)
        record = self._agents.get(agent_id)
        if record:
            record.status = "provisioned"

    def get_agent_config(self, agent_id: str) -> Optional[Dict[str, Any]]:
        config = self._configs.get(agent_id)
        if config is None:
            return None
        return dict(config)

    # ------------------------------------------------------------------
    # Screenshot caches
    # ------------------------------------------------------------------
    def store_agent_screenshot(self, agent_id: str, image_base64: str) -> None:
        self._screenshots[agent_id] = {
            "image_base64": image_base64,
            "timestamp": time.time(),
        }

    def store_task_screenshot(self, agent_id: str, node_id: str, image_base64: str) -> None:
        self._task_screenshots[(agent_id, node_id)] = {
            "image_base64": image_base64,
            "timestamp": time.time(),
        }

    # ------------------------------------------------------------------
    # Helpers
    # ------------------------------------------------------------------
    @staticmethod
    def normalize_service_mode(value: Optional[str], agent_id: Optional[str] = None) -> str:
        text = ""
        try:
            if isinstance(value, str):
                text = value.strip().lower()
        except Exception:
            text = ""

        if not text and agent_id:
            try:
                lowered = agent_id.lower()
                if "-svc-" in lowered or lowered.endswith("-svc"):
                    return "system"
            except Exception:
                pass

        if text in {"system", "svc", "service", "system_service"}:
            return "system"
        if text in {"interactive", "currentuser", "user", "current_user"}:
            return "currentuser"
        return "currentuser"

    @staticmethod
    def _coerce_int(value: Any, *, default: int = 0) -> int:
        try:
            return int(value)
        except Exception:
            return default

    @staticmethod
    def _is_script_agent(agent_id: Optional[str]) -> bool:
        try:
            return bool(isinstance(agent_id, str) and agent_id.lower().endswith("-script"))
        except Exception:
            return False

    @staticmethod
    def _is_valid_interactive_user(candidate: Optional[str]) -> bool:
        if not candidate:
            return False
        try:
            text = str(candidate).strip()
        except Exception:
            return False
        if not text:
            return False
        upper = text.upper()
        if text.endswith("$"):
            return False
        if "NT AUTHORITY\\" in upper or "NT SERVICE\\" in upper:
            return False
        if upper.endswith("\\SYSTEM"):
            return False
        if upper.endswith("\\LOCAL SERVICE"):
            return False
        if upper.endswith("\\NETWORK SERVICE"):
            return False
        if upper == "ANONYMOUS LOGON":
            return False
        return True

    @staticmethod
    def _is_system_service_agent(agent_id: str, is_script_agent: bool) -> bool:
        try:
            lowered = agent_id.lower()
        except Exception:
            lowered = ""
        if is_script_agent:
            return False
        return "-svc-" in lowered or lowered.endswith("-svc")

    def _reconcile_hostname_collisions(
        self,
        *,
        hostname: str,
        agent_id: str,
        incoming_mode: str,
        is_script_agent: bool,
        last_seen: int,
    ) -> None:
        transferred_config = False
        for existing_id, info in list(self._agents.items()):
            if existing_id == agent_id:
                continue
            if info.hostname != hostname:
                continue
            existing_mode = self.normalize_service_mode(info.service_mode, existing_id)
            if existing_mode != incoming_mode:
                continue
            if is_script_agent and not info.is_script_agent:
                self._persist_activity(
                    hostname=hostname,
                    last_seen=last_seen,
                    agent_id=existing_id,
                    operating_system=info.agent_operating_system,
                )
                return
            if not transferred_config and existing_id in self._configs and agent_id not in self._configs:
                self._configs[agent_id] = dict(self._configs[existing_id])
                transferred_config = True
            self._agents.pop(existing_id, None)
            if existing_id != agent_id:
                self._configs.pop(existing_id, None)

    def _persist_activity(
        self,
        *,
        hostname: Optional[str],
        last_seen: Optional[int],
        agent_id: Optional[str],
        operating_system: Optional[str],
        last_user: Optional[str] = None,
    ) -> None:
        if not hostname:
            return
        try:
            self._device_repository.update_device_summary(
                hostname=hostname,
                last_seen=last_seen,
                agent_id=agent_id,
                operating_system=operating_system,
                last_user=last_user,
            )
        except Exception as exc:  # pragma: no cover - defensive logging
            self._log.debug("failed to persist device activity for %s: %s", hostname, exc)
