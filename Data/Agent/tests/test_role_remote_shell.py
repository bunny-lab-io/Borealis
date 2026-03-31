from __future__ import annotations

import Data.Agent.Roles.role_RemoteShell as remote_shell_role


class _FakeConn:
    def __init__(self) -> None:
        self.closed = False

    def close(self) -> None:
        self.closed = True


class _FakeStdout:
    @staticmethod
    def fileno() -> int:
        return 123


class _FakeProc:
    def __init__(self) -> None:
        self.stdout = _FakeStdout()
        self._poll_values = [None, None, 0]

    def poll(self):
        if self._poll_values:
            return self._poll_values.pop(0)
        return 0


def test_powershell_reader_forwards_raw_pipe_chunks(monkeypatch) -> None:
    session = remote_shell_role.ShellSession(
        conn=_FakeConn(),
        address=("10.255.0.1", 47002),
        shell_kind="powershell",
        shell_bin="powershell.exe",
    )
    session.proc = _FakeProc()
    forwarded: list[bytes] = []
    read_chunks = iter([b"nt authority\\system\r\n", b"PS C:\\>", b""])

    monkeypatch.setattr(remote_shell_role, "_write_log", lambda _message: None)
    monkeypatch.setattr(session, "_send_stdout", lambda chunk: forwarded.append(chunk))
    monkeypatch.setattr(remote_shell_role.os, "read", lambda _fd, _size: next(read_chunks))

    session._reader_loop_powershell()

    assert forwarded == [b"nt authority\\system\r\n", b"PS C:\\>"]


def test_control_messages_capture_engine_session_id() -> None:
    sent: list[bytes] = []

    class _Conn(_FakeConn):
        def sendall(self, data: bytes) -> None:
            sent.append(data)

    session = remote_shell_role.ShellSession(
        conn=_Conn(),
        address=("10.255.0.1", 47002),
        shell_kind="powershell",
        shell_bin="powershell.exe",
    )

    handled = session._handle_control_message(
        {
            "type": "ping",
            "ping_id": "ping-1",
            "sent_at_ms": 1000,
            "session_id": "engine-session-1",
        }
    )

    assert handled is True
    assert session.engine_session_id == "engine-session-1"
    assert sent and b'"session_id": "engine-session-1"' in sent[0]
