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
        self.returncode = 0
        self._poll_values = [None, 0]

    def poll(self):
        if self._poll_values:
            return self._poll_values.pop(0)
        return 0


def test_read_windows_pipe_chunk_uses_peeked_available_bytes(monkeypatch) -> None:
    requested: list[tuple[int, int]] = []

    monkeypatch.setattr(remote_shell_role, "_get_windows_pipe_handle", lambda _stdout: 55)
    monkeypatch.setattr(remote_shell_role, "_peek_windows_pipe_available", lambda _handle: 13)
    monkeypatch.setattr(
        remote_shell_role,
        "_read_windows_pipe_handle",
        lambda handle, size: (requested.append((handle, size)) or (b"x" * size)),
    )

    chunk = remote_shell_role._read_windows_pipe_chunk(_FakeStdout(), max_bytes=4096)

    assert chunk == (b"x" * 13)
    assert requested == [(55, 13)]


def test_read_windows_pipe_chunk_returns_none_when_no_bytes_available(monkeypatch) -> None:
    monkeypatch.setattr(remote_shell_role, "_get_windows_pipe_handle", lambda _stdout: 55)
    monkeypatch.setattr(remote_shell_role, "_peek_windows_pipe_available", lambda _handle: 0)
    monkeypatch.setattr(
        remote_shell_role,
        "_read_windows_pipe_handle",
        lambda _handle, _size: (_ for _ in ()).throw(AssertionError("unexpected read")),
    )

    chunk = remote_shell_role._read_windows_pipe_chunk(_FakeStdout(), max_bytes=4096)

    assert chunk is None


def test_powershell_reader_forwards_peeked_pipe_chunks(monkeypatch) -> None:
    session = remote_shell_role.ShellSession(
        conn=_FakeConn(),
        address=("10.255.0.1", 47002),
        shell_kind="powershell",
        shell_bin="powershell.exe",
    )
    session.proc = _FakeProc()
    forwarded: list[bytes] = []
    read_chunks = iter([None, b"nt authority\\system\r\n", b"PS C:\\>", b""])
    sleep_calls: list[float] = []

    monkeypatch.setattr(remote_shell_role, "_write_log", lambda _message: None)
    monkeypatch.setattr(session, "_send_stdout", lambda chunk: forwarded.append(chunk))
    monkeypatch.setattr(remote_shell_role, "_WINDOWS_PIPE_SUPPORT", True)
    monkeypatch.setattr(remote_shell_role, "_read_windows_pipe_chunk", lambda _stdout, _size=4096: next(read_chunks))
    monkeypatch.setattr(remote_shell_role.time, "sleep", lambda seconds: sleep_calls.append(seconds))

    session._reader_loop_powershell()

    assert forwarded == [b"nt authority\\system\r\n", b"PS C:\\>"]
    assert sleep_calls == [0.05]


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
