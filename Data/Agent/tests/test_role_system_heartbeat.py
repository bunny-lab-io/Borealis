from __future__ import annotations

from Data.Agent.Roles.role_system_heartbeat import HeartbeatController, Role


class _FakeHttpClient:
    def __init__(self) -> None:
        self.calls = []

    def post_json(self, path, payload, *, require_auth=False):
        self.calls.append((path, payload, require_auth))
        return {"status": "ok"}


def test_heartbeat_buffers_until_http_client_is_available() -> None:
    controller = HeartbeatController()
    controller.complete("server_config_loaded", "Config loaded.")

    assert controller.flush_now(reason="pre_auth") is False

    client = _FakeHttpClient()
    controller.configure(hooks={"http_client": lambda: client}, service_mode="system")

    assert controller.flush_now(reason="authenticated") is True
    assert len(client.calls) == 1

    path, payload, require_auth = client.calls[0]
    assert path == "/api/agent/status"
    assert require_auth is True
    assert payload["boot_id"] == controller.boot_id
    assert payload["service_mode"] == "system"
    assert any(
        item["key"] == "server_config_loaded" and item["state"] == "complete"
        for item in payload["milestones"]
    )


def test_heartbeat_role_health_registers_system_heartbeat_timeline() -> None:
    controller = HeartbeatController()
    controller.complete("wireguard_online", "WireGuard tunnel is online.")

    report = controller.health_report()

    assert report["role_name"] == "system_heartbeat"
    assert report["role_label"] == "Startup Timeline"
    assert report["context"] == "system"
    assert report["details"]["boot_id"] == controller.boot_id
    assert "wireguard_online" in report["details"]["milestones_json"]


def test_heartbeat_successors_close_active_predecessors() -> None:
    controller = HeartbeatController()

    controller.fail("authenticating", "SSLError: EOF")
    controller.complete("authenticated", "Engine device authentication succeeded.")
    controller.record("socket_connecting", "active", "Socket connect attempt 1.")
    controller.complete("socket_connected", "Socket.IO connection established.")
    controller.record("roles_loading", "active", "Loading SYSTEM roles.")
    controller.complete("roles_ready", "SYSTEM roles loaded.")
    controller.record("wireguard_starting", "active", "Ensuring tunnel.")
    controller.complete("wireguard_online", "WireGuard tunnel is online.")

    states = {item["key"]: item["state"] for item in controller.payload()["milestones"]}
    assert states["authenticating"] == "complete"
    assert states["authenticated"] == "complete"
    assert states["socket_connecting"] == "complete"
    assert states["roles_loading"] == "complete"
    assert states["wireguard_starting"] == "complete"
    assert controller.payload()["status"] == "recovering"
    assert controller.payload()["last_error"] is None


def test_role_wrapper_uses_singleton_controller_hooks() -> None:
    client = _FakeHttpClient()
    ctx = type(
        "Ctx",
        (),
        {
            "hooks": {"http_client": lambda: client},
            "service_mode": "system",
        },
    )()
    role = Role(ctx)

    role.record("inventory_ready", "complete", "Inventory accepted.")

    assert role.flush_now("inventory_ready") is True
    assert client.calls[-1][0] == "/api/agent/status"
    assert any(
        item["key"] == "inventory_ready" and item["state"] == "complete"
        for item in client.calls[-1][1]["milestones"]
    )
