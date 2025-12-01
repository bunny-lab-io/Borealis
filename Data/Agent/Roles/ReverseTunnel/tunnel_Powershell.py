"""PowerShell channel implementation for reverse tunnel (Agent side)."""
from __future__ import annotations

import asyncio
import os
import sys
from typing import Any, Dict, Optional

# Message types mirrored from the tunnel framing (kept local to avoid import cycles).
MSG_DATA = 0x05
MSG_WINDOW_UPDATE = 0x06
MSG_CONTROL = 0x09
MSG_CLOSE = 0x08

# Close codes (mirrored from engine framing)
CLOSE_OK = 0
CLOSE_PROTOCOL_ERROR = 3
CLOSE_AGENT_SHUTDOWN = 6


class PowershellChannel:
    def __init__(self, role, tunnel, channel_id: int, metadata: Optional[Dict[str, Any]]):
        self.role = role
        self.tunnel = tunnel
        self.channel_id = channel_id
        self.metadata = metadata or {}
        self.loop = getattr(role, "loop", None) or asyncio.get_event_loop()
        self._closed = False
        self._reader_task = None
        self._writer_task = None
        self._stdin_queue: asyncio.Queue = asyncio.Queue()
        self._pty = None
        self._exit_code: Optional[int] = None
        self._frame_cls = getattr(role, "_frame_cls", None)

    # ------------------------------------------------------------------ Helpers
    def _make_frame(self, msg_type: int, payload: bytes = b"", *, flags: int = 0):
        frame_cls = self._frame_cls
        if frame_cls is None:
            return None
        try:
            return frame_cls(msg_type=msg_type, channel_id=self.channel_id, payload=payload or b"", flags=flags)
        except Exception:
            return None

    async def _send_frame(self, frame) -> None:
        if frame is None:
            return
        await self.role._send_frame(self.tunnel, frame)

    async def _send_close(self, code: int, reason: str) -> None:
        try:
            close_frame = getattr(self.role, "close_frame")
            if callable(close_frame):
                await self._send_frame(close_frame(self.channel_id, code, reason))
                return
        except Exception:
            pass
        frame = self._make_frame(
            MSG_CLOSE,
            payload=f'{{"code":{code},"reason":"{reason}"}}'.encode("utf-8"),
        )
        await self._send_frame(frame)

    def _powershell_path(self) -> str:
        preferred = self.metadata.get("shell") if isinstance(self.metadata, dict) else None
        if isinstance(preferred, str) and preferred.strip():
            return preferred.strip()
        # Default to Windows PowerShell; fallback to pwsh if provided later.
        return "powershell.exe"

    def _initial_size(self) -> tuple:
        cols = int(self.metadata.get("cols") or self.metadata.get("columns") or 120) if isinstance(self.metadata, dict) else 120
        rows = int(self.metadata.get("rows") or 32) if isinstance(self.metadata, dict) else 32
        cols = max(20, min(cols, 300))
        rows = max(10, min(rows, 200))
        return cols, rows

    # ------------------------------------------------------------------ Lifecycle
    async def start(self) -> None:
        if sys.platform.lower().startswith("win") is False:
            await self._send_close(CLOSE_PROTOCOL_ERROR, "windows_only")
            return
        try:
            import pywinpty  # type: ignore
        except Exception as exc:  # pragma: no cover - dependency guard
            self.role._log(f"reverse_tunnel ps channel missing pywinpty: {exc}", error=True)
            await self._send_close(CLOSE_PROTOCOL_ERROR, "pywinpty_missing")
            return

        shell = self._powershell_path()
        cols, rows = self._initial_size()
        try:
            self._pty = pywinpty.Process(
                spawn_cmd=shell,
                dimensions=(cols, rows),
            )
        except Exception as exc:
            self.role._log(f"reverse_tunnel ps channel failed to spawn {shell}: {exc}", error=True)
            await self._send_close(CLOSE_PROTOCOL_ERROR, "spawn_failed")
            return

        self._reader_task = self.loop.create_task(self._pump_stdout())
        self._writer_task = self.loop.create_task(self._pump_stdin())
        self.role._log(f"reverse_tunnel ps channel started shell={shell} cols={cols} rows={rows}")

    async def on_frame(self, frame) -> None:
        if self._closed:
            return
        if frame.msg_type == MSG_DATA:
            if frame.payload:
                try:
                    self._stdin_queue.put_nowait(frame.payload)
                except Exception:
                    await self._stdin_queue.put(frame.payload)
        elif frame.msg_type == MSG_CONTROL:
            await self._handle_control(frame.payload)
        elif frame.msg_type == MSG_CLOSE:
            await self.stop(code=CLOSE_AGENT_SHUTDOWN, reason="operator_close")
        elif frame.msg_type == MSG_WINDOW_UPDATE:
            # Reserved for back-pressure; ignore for now.
            return

    async def _handle_control(self, payload: bytes) -> None:
        try:
            import json

            data = json.loads(payload.decode("utf-8"))
        except Exception:
            return
        cols = data.get("cols") or data.get("columns")
        rows = data.get("rows")
        if cols is None and rows is None:
            return
        try:
            cols_int = int(cols) if cols is not None else None
            rows_int = int(rows) if rows is not None else None
        except Exception:
            return
        await self._resize(cols_int, rows_int)

    async def _resize(self, cols: Optional[int], rows: Optional[int]) -> None:
        if self._pty is None:
            return
        try:
            cur_cols, cur_rows = self._initial_size()
            if cols is None:
                cols = cur_cols
            if rows is None:
                rows = cur_rows
            cols = max(20, min(int(cols), 300))
            rows = max(10, min(int(rows), 200))
            self._pty.set_size(cols, rows)
            self.role._log(f"reverse_tunnel ps channel resized cols={cols} rows={rows}")
        except Exception:
            self.role._log("reverse_tunnel ps channel resize failed", error=True)

    async def _pump_stdout(self) -> None:
        loop = asyncio.get_event_loop()
        try:
            while not self._closed and self._pty:
                chunk = await loop.run_in_executor(None, self._pty.read, 4096)
                if chunk is None:
                    break
                if isinstance(chunk, str):
                    data = chunk.encode("utf-8", errors="replace")
                else:
                    data = bytes(chunk)
                if not data:
                    break
                frame = self._make_frame(MSG_DATA, payload=data)
                await self._send_frame(frame)
        except asyncio.CancelledError:
            pass
        except Exception:
            self.role._log("reverse_tunnel ps stdout pump error", error=True)
        finally:
            await self.stop(reason="stdout_closed")

    async def _pump_stdin(self) -> None:
        loop = asyncio.get_event_loop()
        try:
            while not self._closed and self._pty:
                try:
                    data = await self._stdin_queue.get()
                except asyncio.CancelledError:
                    break
                if data is None:
                    break
                if isinstance(data, (bytes, bytearray)):
                    text = data.decode("utf-8", errors="replace")
                else:
                    text = str(data)
                try:
                    await loop.run_in_executor(None, self._pty.write, text)
                except Exception:
                    break
        except asyncio.CancelledError:
            pass
        except Exception:
            self.role._log("reverse_tunnel ps stdin pump error", error=True)
        finally:
            await self.stop(reason="stdin_closed")

    async def stop(self, code: int = CLOSE_OK, reason: str = "") -> None:
        if self._closed:
            return
        self._closed = True
        if self._pty is not None:
            try:
                self._pty.terminate()
            except Exception:
                pass
        current = asyncio.current_task()
        if self._reader_task and self._reader_task is not current:
            try:
                self._reader_task.cancel()
            except Exception:
                pass
        if self._writer_task and self._writer_task is not current:
            try:
                self._writer_task.cancel()
            except Exception:
                pass
        await self._send_close(code, reason or "powershell_exit")
        self.role._log(f"reverse_tunnel ps channel stopped channel={self.channel_id} reason={reason or 'exit'}")
