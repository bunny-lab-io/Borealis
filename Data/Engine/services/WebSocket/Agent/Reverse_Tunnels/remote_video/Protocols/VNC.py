"""Placeholder VNC channel server (Engine side)."""
from __future__ import annotations

from collections import deque


class VNCChannelServer:
    protocol_name = "vnc"

    def __init__(self, bridge, service, *, channel_id: int = 1, frame_cls=None, close_frame_fn=None):
        self.bridge = bridge
        self.service = service
        self.channel_id = channel_id
        self.logger = service.logger.getChild(f"vnc.{bridge.lease.tunnel_id}")
        self._open_sent = False
        self._ack_received = False
        self._closed = False
        self._output = deque()
        self._frame_cls = frame_cls
        self._close_frame_fn = close_frame_fn

    def handle_agent_frame(self, frame) -> None:
        try:
            if frame.msg_type == 0x04:  # MSG_CHANNEL_ACK
                self._ack_received = True
        except Exception:
            return

    def open_channel(self, *, cols: int = 120, rows: int = 32) -> None:
        self._open_sent = True
        self.logger.info(
            "vnc channel placeholder open sent tunnel_id=%s channel_id=%s cols=%s rows=%s",
            self.bridge.lease.tunnel_id,
            self.channel_id,
            cols,
            rows,
        )

    def send_input(self, data: str) -> None:
        self.logger.info("vnc placeholder send_input ignored tunnel_id=%s", self.bridge.lease.tunnel_id)

    def send_resize(self, cols: int, rows: int) -> None:
        return

    def close(self, code: int = 6, reason: str = "operator_close") -> None:
        if self._closed:
            return
        self._closed = True
        if callable(self._close_frame_fn):
            try:
                frame = self._close_frame_fn(self.channel_id, code, reason)
                self.bridge.operator_to_agent(frame)
            except Exception:
                pass

    def drain_output(self):
        items = []
        while self._output:
            items.append(self._output.popleft())
        return items

    def status(self):
        return {
            "channel_id": self.channel_id,
            "open_sent": self._open_sent,
            "ack": self._ack_received,
            "closed": self._closed,
            "close_reason": None,
            "close_code": None,
        }


__all__ = ["VNCChannelServer"]
