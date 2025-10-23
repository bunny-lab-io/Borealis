"""Device inventory collection helpers for the Borealis agent."""

from __future__ import annotations

import logging
import socket
from typing import Any, Dict, List, Optional

LOGGER_NAME = "borealis.agent.device_inventory"


class DeviceInventoryCollector:
    """Collects device inventory snapshots for upload to the Engine."""

    def __init__(self, config: Optional[object] = None, *, logger: Optional[logging.Logger] = None) -> None:
        from Data.Agent.Roles import role_DeviceAudit as audit_module

        self._module = audit_module
        self._config = config
        self._log = logger or logging.getLogger(LOGGER_NAME)
        self._external_ip_cache: Dict[str, Any] = {"value": None, "timestamp": 0.0}

    def _call_module(self, name: str, *args: Any, **kwargs: Any) -> Any:
        func = getattr(self._module, name, None)
        if not callable(func):
            self._log.debug("device-inventory-missing-helper name=%s", name)
            return None
        try:
            return func(*args, **kwargs)
        except Exception as exc:  # pragma: no cover - best effort logging
            self._log.exception("device-inventory-helper-failed name=%s", name, exc_info=exc)
            return None

    def _config_proxy(self) -> object:
        if self._config is not None:
            return self._config

        class _Proxy:
            data: Dict[str, Any] = {}

            def _write(self) -> None:  # pragma: no cover - compatibility stub
                return None

        return _Proxy()

    def collect_summary(self) -> Dict[str, Any]:
        summary = self._call_module("collect_summary", self._config_proxy())
        if isinstance(summary, dict):
            return summary
        return {}

    def collect_list_field(self, helper_name: str) -> List[Dict[str, Any]]:
        data = self._call_module(helper_name)
        if isinstance(data, list):
            # Filter out obviously invalid entries so we do not break downstream JSON expectations.
            cleaned: List[Dict[str, Any]] = []
            for item in data:
                if isinstance(item, dict):
                    cleaned.append(item)
            return cleaned
        return []

    def collect_cpu(self) -> Dict[str, Any]:
        data = self._call_module("collect_cpu")
        if isinstance(data, dict):
            return data
        return {}

    def collect_details(self) -> Dict[str, Any]:
        details: Dict[str, Any] = {}
        builder = getattr(self._module, "_build_details_fallback", None)
        if callable(builder):
            try:
                raw = builder()
            except Exception as exc:  # pragma: no cover - defensive logging
                self._log.exception("device-inventory-builder-failed", exc_info=exc)
            else:
                if isinstance(raw, dict):
                    details = raw

        summary = details.get("summary") if isinstance(details, dict) else None
        if not isinstance(summary, dict):
            summary = {}
        details["summary"] = summary

        collected_summary = self.collect_summary()
        for key, value in collected_summary.items():
            if key not in summary or summary[key] in (None, ""):
                summary[key] = value

        for field in ("software", "memory", "storage", "network"):
            values = details.get(field)
            if not isinstance(values, list) or not values:
                details[field] = self.collect_list_field(f"collect_{field}")

        cpu_info = details.get("cpu")
        if not isinstance(cpu_info, dict) or not cpu_info:
            cpu_info = summary.get("cpu") if isinstance(summary.get("cpu"), dict) else None
            if not isinstance(cpu_info, dict) or not cpu_info:
                cpu_info = self.collect_cpu()
            if cpu_info:
                details["cpu"] = cpu_info
        else:
            summary.setdefault("cpu", cpu_info)

        # Ensure hostname is present for downstream normalization.
        host = summary.get("hostname")
        if not isinstance(host, str) or not host.strip():
            summary["hostname"] = socket.gethostname()

        try:
            normalized = self._call_module(
                "normalize_inventory_details",
                details,
                external_ip_cache=self._external_ip_cache,
            )
        except Exception as exc:  # pragma: no cover - defensive logging
            self._log.exception("device-inventory-normalize-error", exc_info=exc)
        else:
            if isinstance(normalized, dict):
                details = normalized

        return details

    def build_payload(self, agent_id: Optional[str]) -> Dict[str, Any]:
        details = self.collect_details()
        summary = details.get("summary", {})
        hostname = summary.get("hostname")
        if not isinstance(hostname, str) or not hostname:
            hostname = socket.gethostname()
            summary["hostname"] = hostname

        payload = {
            "agent_id": agent_id or "",
            "hostname": hostname,
            "details": details,
        }
        return payload


__all__ = ["DeviceInventoryCollector"]
