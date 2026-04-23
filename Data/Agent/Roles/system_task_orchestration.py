from __future__ import annotations

import asyncio
import time
from typing import Any, Awaitable, Callable, Dict, Optional


KNOWN_LANES = (
    "software_management",
    "scheduled_job_system",
    "service_management",
    "agent_update",
    "file_management",
)


def normalize_lane(value: Any) -> str:
    text = str(value or "").strip().lower().replace("-", "_")
    return text if text in KNOWN_LANES else ""


def resolve_system_lane(payload: Any, *, event_name: str = "quick_job_run") -> str:
    if event_name == "service_control_action":
        return "service_management"
    if event_name == "agent_update_request":
        return "agent_update"
    if event_name == "software_inventory_refresh_request":
        return "software_management"

    if not isinstance(payload, dict):
        return ""
    context = payload.get("context") if isinstance(payload.get("context"), dict) else {}
    explicit_lane = normalize_lane(context.get("queue_lane"))
    if explicit_lane:
        return explicit_lane

    assembly_source = str(context.get("assembly_source") or "").strip().lower()
    if assembly_source == "device_software_uninstall":
        return "software_management"
    if context.get("scheduled_job_id") or context.get("scheduled_job_run_id"):
        return "scheduled_job_system"

    script_path = str(payload.get("script_path") or "").replace("\\", "/").strip().lower()
    if script_path.endswith("scripts/internal/software_uninstall.ps1"):
        return "software_management"
    return ""


class LaneCoordinator:
    def __init__(
        self,
        *,
        emit_progress: Optional[Callable[[Dict[str, Any]], Awaitable[None]]] = None,
        log: Optional[Callable[[str], None]] = None,
    ) -> None:
        self._emit_progress = emit_progress
        self._log = log
        self._lane_locks: Dict[str, asyncio.Lock] = {lane: asyncio.Lock() for lane in KNOWN_LANES}
        self._lane_waiters: Dict[str, int] = {lane: 0 for lane in KNOWN_LANES}
        self._lane_active_jobs: Dict[str, Optional[str]] = {lane: None for lane in KNOWN_LANES}
        self._last_activity_at = 0

    @property
    def last_activity_at(self) -> int:
        return int(self._last_activity_at or 0)

    def snapshot(self) -> Dict[str, str]:
        queued_counts = {
            lane: str(max(0, int(self._lane_waiters.get(lane) or 0)))
            for lane in KNOWN_LANES
        }
        active_counts = {
            lane: "1" if self._lane_active_jobs.get(lane) else "0"
            for lane in KNOWN_LANES
        }
        return {
            "queued_lanes": ", ".join(f"{lane}:{queued_counts[lane]}" for lane in KNOWN_LANES),
            "active_lanes": ", ".join(f"{lane}:{active_counts[lane]}" for lane in KNOWN_LANES),
            "last_lane_activity_at": str(self.last_activity_at),
        }

    async def emit_progress(
        self,
        *,
        job_id: Any,
        status: str,
        queue_lane: str = "",
        activity_kind: str = "",
        metadata: Optional[Dict[str, Any]] = None,
        context: Optional[Dict[str, Any]] = None,
        stdout: str = "",
        stderr: str = "",
        append_output: bool = False,
    ) -> None:
        if not callable(self._emit_progress):
            return
        try:
            payload: Dict[str, Any] = {
                "job_id": job_id,
                "status": str(status or "").strip() or "Running",
            }
            normalized_lane = normalize_lane(queue_lane)
            if normalized_lane:
                payload["queue_lane"] = normalized_lane
            if activity_kind:
                payload["activity_kind"] = str(activity_kind).strip()
            if isinstance(metadata, dict) and metadata:
                payload["metadata"] = dict(metadata)
            if isinstance(context, dict) and context:
                payload["context"] = dict(context)
            if stdout:
                payload["stdout"] = stdout
            if stderr:
                payload["stderr"] = stderr
            if append_output:
                payload["append_output"] = True
            await self._emit_progress(payload)
        except Exception as exc:
            if callable(self._log):
                self._log(f"quick_job_progress emit failed: {exc}")

    async def run(
        self,
        *,
        lane: str,
        job_id: Any,
        work: Callable[[], Awaitable[Any]],
        on_start: Optional[Callable[[], Awaitable[None]]] = None,
        on_queued: Optional[Callable[[bool], Awaitable[None]]] = None,
        on_finish: Optional[Callable[[], Awaitable[None]]] = None,
    ) -> Any:
        normalized_lane = normalize_lane(lane)
        if not normalized_lane:
            if callable(on_start):
                await on_start()
            try:
                return await work()
            finally:
                if callable(on_finish):
                    await on_finish()

        lock = self._lane_locks[normalized_lane]
        queued = bool(lock.locked() or (self._lane_waiters.get(normalized_lane) or 0) > 0)
        self._lane_waiters[normalized_lane] = int(self._lane_waiters.get(normalized_lane) or 0) + 1
        if callable(on_queued):
            await on_queued(queued)

        try:
            async with lock:
                self._lane_waiters[normalized_lane] = max(
                    0,
                    int(self._lane_waiters.get(normalized_lane) or 0) - 1,
                )
                self._lane_active_jobs[normalized_lane] = str(job_id or "")
                self._last_activity_at = int(time.time())
                if callable(on_start):
                    await on_start()
                try:
                    return await work()
                finally:
                    self._last_activity_at = int(time.time())
                    self._lane_active_jobs[normalized_lane] = None
                    if callable(on_finish):
                        await on_finish()
        except Exception:
            self._lane_active_jobs[normalized_lane] = None
            self._last_activity_at = int(time.time())
            raise
