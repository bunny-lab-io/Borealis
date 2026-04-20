from __future__ import annotations

import Data.Agent.session_runtime as session_runtime
from Data.Agent.session_runtime import (
    SESSION_TARGET_ALL,
    _PendingJob,
    SessionHelperBroker,
    _HelperState,
    _HELPER_LAUNCH_GRACE_SECONDS,
    _listener_address,
    build_currentuser_dispatch_fields,
    normalize_session_target,
)


def test_build_currentuser_dispatch_fields_defaults_to_all_active_sessions() -> None:
    payload = build_currentuser_dispatch_fields(run_mode="currentuser")

    assert payload == {
        "target_context": "currentuser",
        "session_target": SESSION_TARGET_ALL,
    }


def test_normalize_session_target_accepts_specific_alias() -> None:
    assert normalize_session_target("specific") == "specific_session"


def test_broker_aggregates_multi_session_results() -> None:
    broker = SessionHelperBroker(
        loop=None,
        log=lambda _message: None,
        emit_quick_job_result=lambda _payload: None,
        http_client_factory=None,
    )
    pending = _PendingJob(
        job_id=41,
        context={"assembly_guid": "asm-1"},
        expected_sessions={
            2: {"username": "alice"},
            7: {"username": "bob"},
        },
        results={
            2: {"job_id": 41, "status": "Success", "stdout": "ok-a", "stderr": "", "context": {}},
            7: {"job_id": 41, "status": "Failed", "stdout": "", "stderr": "boom-b", "context": {}},
        },
    )

    aggregated = broker._aggregate_pending_results(pending)

    assert aggregated["job_id"] == 41
    assert aggregated["status"] == "Failed"
    assert "[Session 2 | alice]" in aggregated["stdout"]
    assert "ok-a" in aggregated["stdout"]
    assert "[Session 7 | bob]" in aggregated["stderr"]
    assert "boom-b" in aggregated["stderr"]
    assert aggregated["context"]["session_target"] == "all_active_sessions"
    assert len(aggregated["context"]["session_results"]) == 2


def test_listener_address_accepts_unique_suffix() -> None:
    address = _listener_address(7, "abc123")

    assert "7" in address
    assert "abc123" in address


def test_currentuser_role_health_reports_registered_when_helper_is_ready() -> None:
    broker = SessionHelperBroker(
        loop=None,
        log=lambda _message: None,
        emit_quick_job_result=lambda _payload: None,
        http_client_factory=None,
    )
    broker.session_inventory_payload = lambda: {  # type: ignore[assignment]
        "sessions": [
            {
                "session_id": 1,
                "username": "BUNNY-LAB\\nicole.rappe",
                "session_name": "console",
                "eligible_for_interactive": True,
                "helper_ready": True,
            },
            {
                "session_id": 2,
                "username": "BUNNY-LAB\\testuser",
                "session_name": "rdp-tcp#5",
                "eligible_for_interactive": True,
                "helper_ready": True,
            }
        ]
    }

    report = broker.currentuser_role_health()

    assert report["status"] == "healthy"
    assert report["details"]["execution_context"] == "CURRENTUSER"
    assert report["details"]["listener_state"] == "Registered"
    assert report["details"]["listener_ready"] is True
    assert report["details"]["loaded_helper_sessions"] == (
        "BUNNY-LAB\\nicole.rappe (console) - Loaded Successfully\n"
        "BUNNY-LAB\\testuser (rdp-tcp#5) - Loaded Successfully"
    )


def test_currentuser_role_health_reports_registering_while_helper_warms_up() -> None:
    broker = SessionHelperBroker(
        loop=None,
        log=lambda _message: None,
        emit_quick_job_result=lambda _payload: None,
        http_client_factory=None,
    )
    broker.session_inventory_payload = lambda: {  # type: ignore[assignment]
        "sessions": [
            {
                "session_id": 1,
                "username": "BUNNY-LAB\\testuser",
                "session_name": "rdp-tcp#5",
                "eligible_for_interactive": True,
                "helper_ready": False,
            }
        ]
    }

    report = broker.currentuser_role_health()

    assert report["status"] == "recovering"
    assert report["details"]["execution_context"] == "CURRENTUSER"
    assert report["details"]["listener_state"] == "Registering"
    assert report["details"]["listener_ready"] is False
    assert report["details"]["loaded_helper_sessions"] == ""
    assert report["details"]["pending_helper_sessions"] == "BUNNY-LAB\\testuser (rdp-tcp#5) - Helper Warming Up"


def test_ensure_helper_logs_listener_create_failure_instead_of_raising(monkeypatch) -> None:
    logged = []
    broker = SessionHelperBroker(
        loop=None,
        log=lambda message: logged.append(str(message)),
        emit_quick_job_result=lambda _payload: None,
        http_client_factory=None,
    )

    def _boom(*_args, **_kwargs):
        raise PermissionError("Access is denied")

    monkeypatch.setattr(session_runtime, "Listener", _boom)

    broker._ensure_helper(
        9,
        {
            "session_id": 9,
            "username": "nicole",
            "state": "active",
            "state_code": "active",
            "eligible_for_interactive": True,
        },
    )

    assert any("listener create failed" in entry for entry in logged)


def test_create_helper_listener_uses_standard_listener_off_windows(monkeypatch) -> None:
    captured = {}
    sentinel = object()

    def _fake_listener(address, family=None, authkey=None):
        captured["args"] = (address, family, authkey)
        return sentinel

    monkeypatch.setattr(session_runtime, "IS_WINDOWS", False)
    monkeypatch.setattr(
        session_runtime,
        "Listener",
        _fake_listener,
    )

    result = session_runtime._create_helper_listener(
        session_id=7,
        address="listener.sock",
        auth_token="abc123",
    )

    assert result is sentinel
    assert captured["args"] == ("listener.sock", session_runtime._connection_family(), b"abc123")


class _FakeThread:
    def __init__(self, *args, alive: bool = True, **_kwargs) -> None:
        self.started = False
        self._alive = alive

    def start(self) -> None:
        self.started = True

    def is_alive(self) -> bool:
        return self._alive


class _FakeListener:
    def __init__(self, *args, **_kwargs) -> None:
        self.closed = False

    def close(self) -> None:
        self.closed = True


def test_ensure_helper_does_not_relaunch_while_launch_in_progress(monkeypatch) -> None:
    broker = SessionHelperBroker(
        loop=None,
        log=lambda _message: None,
        emit_quick_job_result=lambda _payload: None,
        http_client_factory=None,
    )
    existing = _HelperState(
        session_id=9,
        session={"session_id": 9, "username": "nicole"},
        address="existing",
        auth_token="token",
        launched_at=int(session_runtime.time.time()),
        listener_thread=_FakeThread(alive=True),
    )
    broker._helpers[9] = existing

    def _boom(*_args, **_kwargs):
        raise AssertionError("helper launch should not happen while a launch is still in progress")

    monkeypatch.setattr(session_runtime, "Listener", _boom)
    monkeypatch.setattr(broker, "_launch_helper", _boom)

    broker._ensure_helper(
        9,
        {
            "session_id": 9,
            "username": "nicole",
            "state": "active",
            "state_code": "active",
            "eligible_for_interactive": True,
        },
    )

    assert broker._helpers[9] is existing


def test_ensure_helper_restarts_stale_helper_once(monkeypatch) -> None:
    logged = []
    broker = SessionHelperBroker(
        loop=None,
        log=lambda message: logged.append(str(message)),
        emit_quick_job_result=lambda _payload: None,
        http_client_factory=None,
    )
    old_listener = _FakeListener()
    existing = _HelperState(
        session_id=11,
        session={"session_id": 11, "username": "nicole"},
        address="old-address",
        auth_token="old-token",
        listener=old_listener,
        listener_thread=_FakeThread(alive=False),
        launched_at=int(session_runtime.time.time()) - _HELPER_LAUNCH_GRACE_SECONDS - 5,
        helper_pid=0,
    )
    broker._helpers[11] = existing
    created_listeners = []

    def _listener_factory(*_args, **_kwargs):
        listener = _FakeListener()
        created_listeners.append(listener)
        return listener

    monkeypatch.setattr(session_runtime, "Listener", _listener_factory)
    monkeypatch.setattr(session_runtime.threading, "Thread", _FakeThread)
    monkeypatch.setattr(broker, "_launch_helper", lambda _helper: (True, "launched helper pid=222 user=test", 222))

    broker._ensure_helper(
        11,
        {
            "session_id": 11,
            "username": "nicole",
            "state": "active",
            "state_code": "active",
            "eligible_for_interactive": True,
        },
    )

    replacement = broker._helpers[11]
    assert replacement is not existing
    assert replacement.helper_pid == 222
    assert len(created_listeners) == 1
    assert old_listener.closed is True
    assert any("restarting stale helper" in entry for entry in logged)


def test_stop_helper_terminates_unconnected_helper_pid(monkeypatch) -> None:
    broker = SessionHelperBroker(
        loop=None,
        log=lambda _message: None,
        emit_quick_job_result=lambda _payload: None,
        http_client_factory=None,
    )
    helper = _HelperState(
        session_id=13,
        session={"session_id": 13},
        address="stale-address",
        auth_token="token",
        helper_pid=4321,
        listener=_FakeListener(),
    )
    broker._helpers[13] = helper
    killed = []

    monkeypatch.setattr(session_runtime.os, "kill", lambda pid, sig: killed.append((pid, sig)))

    broker._stop_helper(13, reason="helper_restart")

    assert killed == [(4321, session_runtime.signal.SIGTERM)]
