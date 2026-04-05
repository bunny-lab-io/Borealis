# ======================================================
# Data\Engine\Unit_Tests\test_websocket_registry.py
# Description: Covers host/service-mode Socket.IO routing for agent sockets.
# ======================================================

from __future__ import annotations

from Data.Engine.services.WebSocket.__init__ import AgentSocketRegistry, OperatorPresenceRegistry


class _FakeSocketIO:
    def __init__(self) -> None:
        self.events = []

    def emit(self, event, payload, to=None):
        self.events.append((event, payload, to))


class _FakeLogger:
    def debug(self, *args, **kwargs):
        return None


def test_agent_socket_registry_routes_by_host_and_service_mode() -> None:
    socketio = _FakeSocketIO()
    registry = AgentSocketRegistry(socketio, _FakeLogger())

    registry.register("LAB-AIO-01_ABCDEF01-0000-0000-0000-000000000001_SYSTEM", "sid-system", service_mode="system")
    registry.register(
        "LAB-AIO-01_ABCDEF01-0000-0000-0000-000000000001_CURRENTUSER",
        "sid-user",
        service_mode="currentuser",
    )

    assert registry.is_host_mode_registered("lab-aio-01", "system") is True
    assert registry.is_host_mode_registered("LAB-AIO-01", "current_user") is True

    assert registry.emit_to_host("LAB-AIO-01", "system", "quick_job_run", {"job_id": 1}) is True
    assert registry.emit_to_host("LAB-AIO-01", "current_user", "quick_job_run", {"job_id": 2}) is True

    assert socketio.events == [
        ("quick_job_run", {"job_id": 1}, "sid-system"),
        ("quick_job_run", {"job_id": 2}, "sid-user"),
    ]


def test_agent_socket_registry_unregister_cleans_host_mode_routes() -> None:
    socketio = _FakeSocketIO()
    registry = AgentSocketRegistry(socketio, _FakeLogger())

    registry.register("LAB-OPERATOR-01_ABCDEF01-0000-0000-0000-000000000001_SYSTEM", "sid-system", service_mode="system")
    assert registry.is_host_mode_registered("LAB-OPERATOR-01", "system") is True

    removed_agent = registry.unregister("sid-system")

    assert removed_agent == "LAB-OPERATOR-01_ABCDEF01-0000-0000-0000-000000000001_SYSTEM"
    assert registry.is_host_mode_registered("LAB-OPERATOR-01", "system") is False
    assert registry.emit_to_host("LAB-OPERATOR-01", "system", "quick_job_run", {"job_id": 3}) is False


def test_operator_presence_registry_tracks_distinct_sessions() -> None:
    registry = OperatorPresenceRegistry()

    first_changed = registry.register_or_update(
        "sid-1",
        username="admin",
        role="Admin",
        current_page="Server Info",
    )
    second_changed = registry.register_or_update(
        "sid-2",
        username="admin",
        role="Admin",
        current_page="Log Management",
    )

    assert first_changed is True
    assert second_changed is True

    sessions = registry.list_sessions()
    assert len(sessions) == 2
    assert {session["sid"] for session in sessions} == {"sid-1", "sid-2"}


def test_operator_presence_registry_ignores_heartbeat_only_updates() -> None:
    registry = OperatorPresenceRegistry()

    assert registry.register_or_update(
        "sid-1",
        username="admin",
        role="Admin",
        current_page="Server Info",
    ) is True
    assert registry.register_or_update(
        "sid-1",
        username="admin",
        role="Admin",
        current_page="Server Info",
    ) is False
    assert registry.register_or_update(
        "sid-1",
        username="admin",
        role="Admin",
        current_page="Log Management",
    ) is True


def test_operator_presence_registry_remove_returns_session_snapshot() -> None:
    registry = OperatorPresenceRegistry()
    registry.register_or_update(
        "sid-1",
        username="admin",
        role="Admin",
        current_page="Server Info",
    )

    removed = registry.remove("sid-1")

    assert removed is not None
    assert removed["username"] == "admin"
    assert registry.list_sessions() == []
