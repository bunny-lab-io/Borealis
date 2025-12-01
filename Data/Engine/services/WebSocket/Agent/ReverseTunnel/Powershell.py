"""Engine-side PowerShell tunnel channel helper."""
from __future__ import annotations

import json
from collections import deque
from typing import Any, Deque, Dict, List, Optional

from ..ReverseTunnel import (
    CLOSE_AGENT_SHUTDOWN,
    CLOSE_OK,
    CLOSE_PROTOCOL_ERROR,
    MSG_CHANNEL_ACK,
    MSG_CHANNEL_OPEN,
    MSG_CLOSE,
    MSG_CONTROL,
    MSG_DATA,
    TunnelFrame,
    close_frame,
)


class PowershellChannelServer:
    """Coordinate PowerShell channel frames over a TunnelBridge."""

    def __init__(self, bridge, service, *, channel_id: int = 1):
        self.bridge = bridge
        self.service = service
        self.channel_id = channel_id
        self.logger = service.logger.getChild(f"ps.{bridge.lease.tunnel_id}")
        self._open_sent = False
        self._ack_received = False
        self._closed = False
        self._output: Deque[str] = deque()
        self._close_reason: Optional[str] = None
        self._close_code: Optional[int] = None

    # ------------------------------------------------------------------ Agent frame handling
    def handle_agent_frame(self, frame: TunnelFrame) -> None:
        if frame.channel_id != self.channel_id:
            return
        if frame.msg_type == MSG_CHANNEL_ACK:
            self._ack_received = True
            self.logger.info("ps channel acked tunnel_id=%s", self.bridge.lease.tunnel_id)
            return
        if frame.msg_type == MSG_DATA:
            try:
                text = frame.payload.decode("utf-8", errors="replace")
            except Exception:
                text = ""
            if text:
                self._append_output(text)
            return
        if frame.msg_type == MSG_CLOSE:
            try:
                payload = json.loads(frame.payload.decode("utf-8"))
            except Exception:
                payload = {}
            self._closed = True
            self._close_code = payload.get("code") if isinstance(payload, dict) else None
            self._close_reason = payload.get("reason") if isinstance(payload, dict) else None
            self.logger.info(
                "ps channel closed tunnel_id=%s code=%s reason=%s",
                self.bridge.lease.tunnel_id,
                self._close_code,
                self._close_reason or "-",
            )

    # ------------------------------------------------------------------ Operator actions
    def open_channel(self, *, cols: int = 120, rows: int = 32) -> None:
        if self._open_sent:
            return
        payload = json.dumps(
            {"protocol": "ps", "metadata": {"cols": cols, "rows": rows}},
            separators=(",", ":"),
        ).encode("utf-8")
        frame = TunnelFrame(msg_type=MSG_CHANNEL_OPEN, channel_id=self.channel_id, payload=payload)
        self.bridge.operator_to_agent(frame)
        self._open_sent = True
        self.logger.info(
            "ps channel open sent tunnel_id=%s channel_id=%s cols=%s rows=%s",
            self.bridge.lease.tunnel_id,
            self.channel_id,
            cols,
            rows,
        )

    def send_input(self, data: str) -> None:
        if self._closed:
            return
        payload = data.encode("utf-8", errors="replace")
        frame = TunnelFrame(msg_type=MSG_DATA, channel_id=self.channel_id, payload=payload)
        self.bridge.operator_to_agent(frame)

    def send_resize(self, cols: int, rows: int) -> None:
        if self._closed:
            return
        payload = json.dumps({"cols": cols, "rows": rows}, separators=(",", ":")).encode("utf-8")
        frame = TunnelFrame(msg_type=MSG_CONTROL, channel_id=self.channel_id, payload=payload)
        self.bridge.operator_to_agent(frame)

    def close(self, code: int = CLOSE_AGENT_SHUTDOWN, reason: str = "operator_close") -> None:
        if self._closed:
            return
        self._closed = True
        self.bridge.operator_to_agent(close_frame(self.channel_id, code, reason))

    # ------------------------------------------------------------------ Output polling
    def drain_output(self) -> List[str]:
        items: List[str] = []
        while self._output:
            items.append(self._output.popleft())
        return items

    def _append_output(self, text: str) -> None:
        self._output.append(text)
        # Cap buffer to avoid unbounded memory growth.
        while len(self._output) > 500:
            self._output.popleft()

    # ------------------------------------------------------------------ Status helpers
    def status(self) -> Dict[str, Any]:
        return {
            "channel_id": self.channel_id,
            "open_sent": self._open_sent,
            "ack": self._ack_received,
            "closed": self._closed,
            "close_reason": self._close_reason,
            "close_code": self._close_code,
        }

