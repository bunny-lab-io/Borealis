"""Placeholder VNC channel (Agent side)."""
from __future__ import annotations

import asyncio
from typing import Any, Dict, Optional

MSG_DATA = 0x05
MSG_CONTROL = 0x09
MSG_CLOSE = 0x08
CLOSE_PROTOCOL_ERROR = 3
CLOSE_AGENT_SHUTDOWN = 6


class VNCChannel:
    """Stub VNC handler that marks the channel unsupported for now."""

    def __init__(self, role, tunnel, channel_id: int, metadata: Optional[Dict[str, Any]]):
        self.role = role
        self.tunnel = tunnel
        self.channel_id = channel_id
        self.metadata = metadata or {}
        self.loop = getattr(role, "loop", None) or asyncio.get_event_loop()
        self._closed = False

    async def start(self) -> None:
        await self.stop(code=CLOSE_PROTOCOL_ERROR, reason="vnc_unsupported")

    async def on_frame(self, frame) -> None:
        if self._closed:
            return
        if frame.msg_type in (MSG_DATA, MSG_CONTROL):
            await self.stop(code=CLOSE_PROTOCOL_ERROR, reason="vnc_unsupported")
        elif frame.msg_type == MSG_CLOSE:
            await self.stop(code=CLOSE_AGENT_SHUTDOWN, reason="operator_close")

    async def stop(self, code: int = CLOSE_PROTOCOL_ERROR, reason: str = "") -> None:
        if self._closed:
            return
        self._closed = True
        try:
            await self.role._send_frame(self.tunnel, self.role.close_frame(self.channel_id, code, reason or "vnc_closed"))
        except Exception:
            pass
        self.role._log(f"reverse_tunnel vnc channel stopped channel={self.channel_id} reason={reason or 'closed'}")


__all__ = ["VNCChannel"]
