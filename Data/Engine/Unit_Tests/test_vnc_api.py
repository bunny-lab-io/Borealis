# ======================================================
# Data\Engine\Unit_Tests\test_vnc_api.py
# Description: Validates VNC session bootstrap behavior for same-origin routed access.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

import inspect
from types import SimpleNamespace
from typing import Any

import pytest

from Data.Engine.services.API.devices import vnc as vnc_api
from Data.Engine.services.job_scheduler.queue import WORKER_STATUS_RUNNING, register_worker
from Data.Engine.services.RemoteDesktop import rfb_probe

from .conftest import EngineTestHarness
from .support.engine import db_connection


@pytest.fixture(autouse=True)
def _guacd_ready(monkeypatch, tmp_path):
    monkeypatch.setenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT", str(tmp_path / "auth-tokens"))
    monkeypatch.setenv("BOREALIS_TRAEFIK_DYNAMIC_CONFIG_DIR", str(tmp_path / "dynamic-routes"))
    vnc_api._clear_vnc_auth_rate_limits()
    monkeypatch.setattr(
        vnc_api,
        "guacd_health",
        lambda _context: {
            "enabled": True,
            "available": True,
            "reason": "ready",
            "host": "127.0.0.1",
            "port": 4822,
        },
    )
    monkeypatch.setattr(
        vnc_api,
        "_wait_for_backend_auth_ready",
        lambda *args, **kwargs: rfb_probe.VncAuthProbeResult(True, True, "auth_ok"),
    )

    def _fake_worker_guacamole_session(_app, adapters, worker_route, payload):
        requests = getattr(adapters.context, "worker_guacamole_requests", None)
        if requests is None:
            requests = []
            setattr(adapters.context, "worker_guacamole_requests", requests)
        requests.append({"worker_route": dict(worker_route or {}), "payload": dict(payload or {})})
        return {"status": "ok", "token": "worker-guacamole-token"}

    def _fake_worker_guacamole_disconnect(_app, worker_route, payload):
        disconnects = getattr(_app, "worker_guacamole_disconnects", None)
        if disconnects is None:
            disconnects = []
            setattr(_app, "worker_guacamole_disconnects", disconnects)
        disconnects.append({"worker_route": dict(worker_route or {}), "payload": dict(payload or {})})
        return {"status": "ok", "disconnected": 1}

    monkeypatch.setattr(vnc_api, "_register_worker_guacamole_session", _fake_worker_guacamole_session)
    monkeypatch.setattr(vnc_api, "_disconnect_worker_guacamole_session", _fake_worker_guacamole_disconnect)
    yield
    vnc_api._clear_vnc_auth_rate_limits()


def _client_with_admin_session(harness: EngineTestHarness):
    return _client_with_session(harness, username="admin", role="Admin")


def _client_with_session(harness: EngineTestHarness, *, username: str, role: str = "Admin"):
    _seed_vnc_worker_route(harness)
    client = harness.app.test_client()
    with client.session_transaction() as sess:
        sess["username"] = username
        sess["role"] = role
    return client


def _seed_vnc_worker_route(harness: EngineTestHarness) -> None:
    with db_connection(harness) as conn:
        register_worker(
            conn,
            worker_guid="worker-vnc-route",
            container_name="site-worker-worker-vnc-route",
            site_id=1,
            status=WORKER_STATUS_RUNNING,
            upstream_port=59011,
            route_metadata={
                "remote_ops_socket": {
                    "host": "127.0.0.1",
                    "path": "/socket.io/",
                    "port": 59011,
                    "worker_guid": "worker-vnc-route",
                },
                "remote_desktop_guacamole": {
                    "host": "127.0.0.1",
                    "scheme": "http",
                    "path": "/remote-desktop/vnc/guacamole",
                    "path_prefix": "/remote-desktop/vnc",
                    "port": 61011,
                    "worker_guid": "worker-vnc-route",
                },
            },
        )
        conn.commit()


def _register_agent_credential(
    harness: EngineTestHarness,
    *,
    agent_id: str = "test-device-agent",
    password: str = "bootpass",
    revision: int = 42,
    display_topology: Any = None,
) -> None:
    display = list(display_topology or [])
    if display:
        left = min(int(item.get("left") or 0) for item in display)
        top = min(int(item.get("top") or 0) for item in display)
        right = max(int(item.get("right") or (int(item.get("left") or 0) + int(item.get("width") or 0))) for item in display)
        bottom = max(int(item.get("bottom") or (int(item.get("top") or 0) + int(item.get("height") or 0))) for item in display)
        virtual_bounds = {
            "left": left,
            "top": top,
            "right": right,
            "bottom": bottom,
            "width": max(0, right - left),
            "height": max(0, bottom - top),
        }
    else:
        virtual_bounds = {}

    def _call_agent_event(target_agent_id, event, payload, *, timeout=30.0):
        _ = timeout
        if target_agent_id != agent_id or event != "vnc_credential_request":
            return None
        return {
            "status": "ok",
            "agent_id": agent_id,
            "request_id": payload.get("request_id") if isinstance(payload, dict) else "",
            "controller_password": password,
            "credential_revision": revision,
            "display_topology": display,
            "display_virtual_bounds": virtual_bounds,
            "ready": True,
            "service_state": "RUNNING",
            "listener_state": "listening",
        }

    harness.context.call_agent_event = _call_agent_event


class _FakeTunnelService:
    def __init__(self) -> None:
        self.connect_calls: list[tuple[str, Any, Any]] = []
        self.start_calls: list[tuple[str, bool, str]] = []
        self.transport_marks: list[tuple[str, str]] = []
        self.transport_confirms: list[tuple[str, str]] = []
        self.transport_recovers: list[tuple[str, str, str]] = []

    def session_payload(self, agent_id: str, include_token: bool = False) -> Any:
        _ = agent_id
        _ = include_token
        return None

    def connect(self, *, agent_id: str, operator_id: Any, endpoint_host: Any) -> dict[str, Any]:
        self.connect_calls.append((agent_id, operator_id, endpoint_host))
        return {
            "tunnel_id": "tun-vnc-1",
            "agent_id": agent_id,
            "virtual_ip": "10.255.0.2/32",
            "engine_virtual_ip": "10.255.0.1/32",
        }

    def request_agent_start(
        self,
        agent_id: str,
        *,
        force_restart: bool = False,
        reason: str | None = None,
    ) -> dict[str, Any]:
        self.start_calls.append((agent_id, bool(force_restart), str(reason or "")))
        return {"status": "ok"}

    def mark_transport_required(self, agent_id: str, *, reason: str | None = None) -> bool:
        self.transport_marks.append((agent_id, str(reason or "")))
        return True

    def recover_transport(
        self,
        agent_id: str,
        *,
        trigger: str,
        reason: str | None = None,
    ) -> dict[str, Any]:
        self.transport_recovers.append((agent_id, str(trigger or ""), str(reason or "")))
        return {"status": "ok"}

    def confirm_transport_success(self, agent_id: str, *, reason: str | None = None) -> bool:
        self.transport_confirms.append((agent_id, str(reason or "")))
        return True


class _FakeRegistry:
    def __init__(self) -> None:
        self.created: list[dict[str, Any]] = []
        self.restart_callbacks: list[Any] = []
        self.confirm_callbacks: list[Any] = []

    def create(
        self,
        *,
        agent_id: str,
        host: str,
        port: int,
        operator_id: Any,
        password: str = "",
        session_id: str = "",
        participant_id: str = "",
        role: str = "",
        width: int = 0,
        height: int = 0,
        dpi: int = 0,
        performance_preference: int = 0,
        restart_tunnel: Any = None,
        confirm_transport: Any = None,
        on_open: Any = None,
        on_close: Any = None,
    ):
        self.created.append(
            {
                "agent_id": agent_id,
                "host": host,
                "port": port,
                "password": password,
                "operator_id": operator_id,
                "session_id": session_id,
                "participant_id": participant_id,
                "role": role,
                "width": width,
                "height": height,
                "dpi": dpi,
                "performance_preference": performance_preference,
                "on_open": on_open,
                "on_close": on_close,
            }
        )
        self.restart_callbacks.append(restart_tunnel)
        self.confirm_callbacks.append(confirm_transport)
        return SimpleNamespace(token="session-token")


class _FakeGuacamoleRegistry:
    def __init__(self) -> None:
        self.created: list[dict[str, Any]] = []

    def create(
        self,
        *,
        agent_id: str,
        host: str,
        port: int,
        password: str,
        operator_id: Any,
        session_id: str = "",
        participant_id: str = "",
        role: str = "",
        width: int = 0,
        height: int = 0,
        performance_preference: int = 0,
        restart_tunnel: Any = None,
        confirm_transport: Any = None,
        on_open: Any = None,
        on_close: Any = None,
    ):
        self.created.append(
            {
                "agent_id": agent_id,
                "host": host,
                "port": port,
                "password": password,
                "operator_id": operator_id,
                "session_id": session_id,
                "participant_id": participant_id,
                "role": role,
                "width": width,
                "height": height,
                "performance_preference": performance_preference,
                "on_open": on_open,
                "on_close": on_close,
            }
        )
        _ = restart_tunnel
        _ = confirm_transport
        return SimpleNamespace(token="guacamole-token")


class _FakeProxy:
    def __init__(self) -> None:
        self.disconnect_session_calls: list[tuple[str, str]] = []
        self.disconnect_participant_calls: list[tuple[str, str, str]] = []

    def disconnect_session(self, session_id: str, *, reason: str = "") -> None:
        self.disconnect_session_calls.append((session_id, str(reason or "")))

    def disconnect_participant(self, session_id: str, participant_id: str, *, reason: str = "") -> None:
        self.disconnect_participant_calls.append((session_id, participant_id, str(reason or "")))


class _FakeRfbSocket:
    def __init__(self, payload: bytes) -> None:
        self.payload = bytearray(payload)
        self.sent: list[bytes] = []

    def __enter__(self):
        return self

    def __exit__(self, *_args) -> None:
        return None

    def settimeout(self, _timeout: float) -> None:
        return None

    def recv(self, byte_count: int) -> bytes:
        if not self.payload:
            return b""
        chunk = bytes(self.payload[:byte_count])
        del self.payload[:byte_count]
        return chunk

    def sendall(self, payload: bytes) -> None:
        self.sent.append(bytes(payload))


def test_vnc_rfb_auth_probe_accepts_valid_password(monkeypatch) -> None:
    challenge = b"0123456789abcdef"
    fake_socket = _FakeRfbSocket(
        b"RFB 003.008\n"
        + b"\x01\x02"
        + challenge
        + b"\x00\x00\x00\x00"
        + b"\x04\x00\x03\x00"
        + (b"\x00" * 20)
    )

    monkeypatch.setattr(rfb_probe.socket, "create_connection", lambda *_args, **_kwargs: fake_socket)
    monkeypatch.setattr(rfb_probe, "_vnc_auth_challenge_response", lambda password, challenge: b"x" * 16)

    result = rfb_probe.probe_vnc_auth("10.255.0.2", 5900, "bootpass", 1.0)

    assert result == rfb_probe.VncAuthProbeResult(True, True, "server_init_ok")
    assert fake_socket.sent == [b"RFB 003.008\n", b"\x02", b"x" * 16, b"\x01"]


def test_vnc_rfb_auth_probe_reports_bad_password(monkeypatch) -> None:
    fake_socket = _FakeRfbSocket(
        b"RFB 003.008\n"
        + b"\x01\x02"
        + b"0123456789abcdef"
        + b"\x00\x00\x00\x01"
    )

    monkeypatch.setattr(rfb_probe.socket, "create_connection", lambda *_args, **_kwargs: fake_socket)
    monkeypatch.setattr(rfb_probe, "_vnc_auth_challenge_response", lambda password, challenge: b"x" * 16)

    result = rfb_probe.probe_vnc_auth("10.255.0.2", 5900, "wrongpass", 1.0)

    assert result == rfb_probe.VncAuthProbeResult(True, False, "auth_failed")


def test_vnc_rfb_auth_wait_stops_after_definitive_auth_failure(monkeypatch) -> None:
    calls = 0

    def _auth_failed(*_args, **_kwargs):
        nonlocal calls
        calls += 1
        return rfb_probe.VncAuthProbeResult(True, False, "auth_failed")

    monkeypatch.setattr(rfb_probe, "probe_vnc_auth", _auth_failed)
    monkeypatch.setattr(rfb_probe.time, "sleep", lambda _seconds: None)
    monkeypatch.setenv("BOREALIS_VNC_AUTH_PROBE", "1")

    result = rfb_probe.wait_for_vnc_auth_ready(
        "10.255.0.2",
        5900,
        "wrongpass",
        timeout_seconds=10.0,
        poll_interval_seconds=0.1,
    )

    assert result == rfb_probe.VncAuthProbeResult(True, False, "auth_failed")
    assert calls == 1


def test_vnc_rfb_auth_wait_disabled_by_default(monkeypatch) -> None:
    calls = 0

    def _auth_failed(*_args, **_kwargs):
        nonlocal calls
        calls += 1
        return rfb_probe.VncAuthProbeResult(True, False, "auth_failed")

    monkeypatch.setattr(rfb_probe, "probe_vnc_auth", _auth_failed)
    monkeypatch.delenv("BOREALIS_VNC_AUTH_PROBE", raising=False)

    result = rfb_probe.wait_for_vnc_auth_ready(
        "10.255.0.2",
        5900,
        "wrongpass",
        timeout_seconds=10.0,
        poll_interval_seconds=0.1,
    )

    assert result == rfb_probe.VncAuthProbeResult(False, True, "auth_probe_disabled")
    assert calls == 0


def test_vnc_rfb_auth_wait_retries_transient_connect_failure(monkeypatch) -> None:
    results = [
        rfb_probe.VncAuthProbeResult(True, False, "connection refused"),
        rfb_probe.VncAuthProbeResult(True, True, "server_init_ok"),
    ]

    monkeypatch.setattr(rfb_probe, "probe_vnc_auth", lambda *_args, **_kwargs: results.pop(0))
    monkeypatch.setattr(rfb_probe.time, "sleep", lambda _seconds: None)
    monkeypatch.setenv("BOREALIS_VNC_AUTH_PROBE", "1")

    result = rfb_probe.wait_for_vnc_auth_ready(
        "10.255.0.2",
        5900,
        "bootpass",
        timeout_seconds=10.0,
        poll_interval_seconds=0.1,
    )

    assert result == rfb_probe.VncAuthProbeResult(True, True, "server_init_ok")
    assert results == []


def test_vnc_rfb_auth_response_available_without_des_algorithm() -> None:
    response = rfb_probe._vnc_auth_challenge_response("bootpass", b"0123456789abcdef")

    assert response is not None
    assert len(response) == 16


def test_vnc_establish_returns_same_origin_websocket(engine_harness: EngineTestHarness, monkeypatch) -> None:
    client = _client_with_admin_session(engine_harness)
    fake_tunnel = _FakeTunnelService()
    fake_registry = _FakeRegistry()
    engine_harness.context.public_base_url = "https://borealis.example.com"
    engine_harness.context.public_hostname = "borealis.example.com"
    engine_harness.context.public_wireguard_host = "borealis.example.com"
    _register_agent_credential(
        engine_harness,
        display_topology=[
            {
                "id": "1",
                "display_index": 1,
                "label": "1",
                "device_name": "\\\\.\\DISPLAY1",
                "left": 0,
                "top": 0,
                "right": 1920,
                "bottom": 1080,
                "width": 1920,
                "height": 1080,
                "primary": True,
            },
            {
                "id": "2",
                "display_index": 2,
                "label": "2",
                "device_name": "\\\\.\\DISPLAY2",
                "left": -1024,
                "top": -300,
                "right": 0,
                "bottom": 468,
                "width": 1024,
                "height": 768,
                "primary": False,
            },
        ],
    )

    monkeypatch.setattr(vnc_api, "_get_tunnel_service", lambda _adapters: fake_tunnel)
    monkeypatch.setattr(vnc_api, "_wait_for_backend_ready", lambda *args, **kwargs: True)

    response = client.post(
        "/api/vnc/establish",
        json={"agent_id": "test-device-agent", "remove_wallpaper": True},
    )

    assert response.status_code == 200
    payload = response.get_json()
    assert payload["viewer"] == "guacamole"
    assert payload["token"] == "worker-guacamole-token"
    assert payload["guacamole_ws_path"] == "/_borealis/site-workers/worker-vnc-route/remote-desktop/vnc/guacamole"
    assert payload["guacamole_ws_url"] == "wss://borealis.example.com/_borealis/site-workers/worker-vnc-route/remote-desktop/vnc/guacamole"
    assert "vnc_password" not in payload
    assert "ws_url" not in payload
    worker_request = engine_harness.context.worker_guacamole_requests[0]["payload"]
    assert worker_request["agent_id"] == "test-device-agent"
    assert worker_request["host"] == "10.255.0.2"
    assert worker_request["port"] == 5900
    assert worker_request["password"] == "bootpass"
    assert worker_request["operator_id"] == "admin"
    assert worker_request["role"] == "controller"
    assert worker_request["width"] == 2944
    assert worker_request["height"] == 1380
    assert worker_request["performance_preference"] == 0
    assert payload["remote_ops_session"]["capabilities"] == ["remote_desktop"]
    assert payload["remote_ops_session"]["worker"]["worker_guid"] == "worker-vnc-route"
    assert payload["performance_preference"] == 0
    assert payload["participant_role"] == "controller"
    assert payload["view_only"] is False
    assert payload["session"]["controller_operator_id"] == "admin"
    assert len(payload["display_topology"]) == 2
    assert payload["display_virtual_bounds"] == {
        "left": -1024,
        "top": -300,
        "right": 1920,
        "bottom": 1080,
        "width": 2944,
        "height": 1380,
    }
    assert payload["session"]["display_topology"][0]["label"] == "1"
    assert fake_tunnel.connect_calls == [("test-device-agent", "admin", "borealis.example.com")]
    assert fake_tunnel.transport_marks == []
    assert fake_tunnel.start_calls == []
    assert fake_tunnel.transport_recovers == []
    assert fake_tunnel.transport_confirms == [("test-device-agent", "vnc_backend_ready")]


def test_vnc_establish_defers_after_auth_refresh(engine_harness: EngineTestHarness, monkeypatch) -> None:
    client = _client_with_admin_session(engine_harness)
    fake_tunnel = _FakeTunnelService()
    fake_registry = _FakeRegistry()
    emitted_events: list[tuple[str, str, dict[str, Any]]] = []
    manager = vnc_api.ensure_vnc_collaboration_manager(engine_harness.context, logger=engine_harness.context.logger)
    auth_results = [
        rfb_probe.VncAuthProbeResult(True, False, "auth_failed"),
    ]
    _register_agent_credential(engine_harness)

    def _emit(agent_id, event, payload):
        emitted_events.append((agent_id, event, dict(payload)))
        if event == "vnc_refresh":
            manager.upsert_agent_credential(
                agent_id=agent_id,
                controller_password="freshpw",
                credential_revision=43,
            )
        return True

    engine_harness.context.emit_agent_event = _emit

    monkeypatch.setattr(vnc_api, "_get_tunnel_service", lambda _adapters: fake_tunnel)
    monkeypatch.setattr(vnc_api, "_wait_for_backend_ready", lambda *args, **kwargs: True)
    monkeypatch.setenv("BOREALIS_VNC_AUTH_REFRESH_BACKOFF_SECONDS", "12")
    monkeypatch.setattr(
        vnc_api,
        "_wait_for_backend_auth_ready",
        lambda *args, **kwargs: auth_results.pop(0),
    )
    monkeypatch.setattr(vnc_api.time, "sleep", lambda _seconds: None)

    response = client.post(
        "/api/vnc/establish",
        json={"agent_id": "test-device-agent", "remove_wallpaper": True},
    )

    assert response.status_code == 503
    assert response.get_json() == {
        "error": "vnc_backend_auth_refresh_pending",
        "detail": "auth_failed",
        "retry_after_seconds": 12.0,
    }
    assert auth_results == []
    assert fake_registry.created == []
    assert fake_tunnel.transport_marks == []
    assert fake_tunnel.start_calls == []
    assert emitted_events == []


def test_vnc_establish_caches_auth_refresh_backoff(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    client = _client_with_admin_session(engine_harness)
    fake_tunnel = _FakeTunnelService()
    emitted_events: list[tuple[str, str, dict[str, Any]]] = []
    manager = vnc_api.ensure_vnc_collaboration_manager(engine_harness.context, logger=engine_harness.context.logger)
    auth_probe_calls = 0
    _register_agent_credential(engine_harness)

    def _emit(agent_id, event, payload):
        emitted_events.append((agent_id, event, dict(payload)))
        if event == "vnc_refresh":
            manager.upsert_agent_credential(
                agent_id=agent_id,
                controller_password="freshpw",
                credential_revision=43,
            )
        return True

    def _auth_probe(*_args, **_kwargs):
        nonlocal auth_probe_calls
        auth_probe_calls += 1
        return rfb_probe.VncAuthProbeResult(True, False, "auth_failed")

    engine_harness.context.emit_agent_event = _emit

    monkeypatch.setenv("BOREALIS_VNC_AUTH_REFRESH_BACKOFF_SECONDS", "30")
    monkeypatch.setattr(vnc_api, "_get_tunnel_service", lambda _adapters: fake_tunnel)
    monkeypatch.setattr(vnc_api, "_wait_for_backend_ready", lambda *args, **kwargs: True)
    monkeypatch.setattr(vnc_api, "_wait_for_backend_auth_ready", _auth_probe)
    monkeypatch.setattr(vnc_api.time, "sleep", lambda _seconds: None)

    first = client.post(
        "/api/vnc/establish",
        json={"agent_id": "test-device-agent", "remove_wallpaper": True},
    )
    assert first.status_code == 503
    assert first.get_json()["error"] == "vnc_backend_auth_refresh_pending"

    def _unexpected_probe(*_args, **_kwargs):
        raise AssertionError("cached auth refresh backoff should skip RFB probe")

    monkeypatch.setattr(vnc_api, "_wait_for_backend_auth_ready", _unexpected_probe)

    second = client.post(
        "/api/vnc/establish",
        json={"agent_id": "test-device-agent", "remove_wallpaper": True},
    )

    assert second.status_code == 503
    assert second.get_json()["error"] == "vnc_backend_auth_refresh_pending"
    assert second.get_json()["retry_after_seconds"] <= 30.0
    assert auth_probe_calls == 1
    assert emitted_events == []
    assert fake_tunnel.transport_marks == []
    assert fake_tunnel.start_calls == []


def test_vnc_establish_fails_when_auth_retry_credential_does_not_rotate(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    client = _client_with_admin_session(engine_harness)
    fake_tunnel = _FakeTunnelService()
    emitted_events: list[tuple[str, str, dict[str, Any]]] = []
    _register_agent_credential(engine_harness)
    original_call = engine_harness.context.call_agent_event
    call_count = 0

    def _call_agent_event(agent_id, event, payload, *, timeout=30.0):
        nonlocal call_count
        call_count += 1
        if call_count == 1:
            return original_call(agent_id, event, payload, timeout=timeout)
        return None

    engine_harness.context.call_agent_event = _call_agent_event

    def _emit(agent_id, event, payload):
        emitted_events.append((agent_id, event, dict(payload)))
        return True

    engine_harness.context.emit_agent_event = _emit

    monkeypatch.setattr(vnc_api, "_get_tunnel_service", lambda _adapters: fake_tunnel)
    monkeypatch.setattr(vnc_api, "_wait_for_backend_ready", lambda *args, **kwargs: True)
    monkeypatch.setattr(
        vnc_api,
        "_wait_for_backend_auth_ready",
        lambda *args, **kwargs: rfb_probe.VncAuthProbeResult(True, False, "auth_failed"),
    )
    response = client.post(
        "/api/vnc/establish",
        json={"agent_id": "test-device-agent", "remove_wallpaper": True},
    )

    assert response.status_code == 503
    assert response.get_json() == {
        "error": "vnc_backend_auth_refresh_failed",
        "detail": "live_credential_unavailable",
    }
    assert fake_tunnel.transport_marks == []
    assert fake_tunnel.start_calls == []
    assert emitted_events == []


def test_vnc_establish_stops_when_backend_auth_is_rate_limited(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    client = _client_with_admin_session(engine_harness)
    fake_tunnel = _FakeTunnelService()
    emitted_events: list[tuple[str, str, dict[str, Any]]] = []
    _register_agent_credential(engine_harness)

    engine_harness.context.emit_agent_event = lambda agent_id, event, payload: emitted_events.append(
        (agent_id, event, dict(payload))
    ) or True

    monkeypatch.setenv("BOREALIS_VNC_AUTH_RATE_LIMIT_RETRY_SECONDS", "90")
    monkeypatch.setattr(vnc_api, "_get_tunnel_service", lambda _adapters: fake_tunnel)
    monkeypatch.setattr(vnc_api, "_wait_for_backend_ready", lambda *args, **kwargs: True)
    monkeypatch.setattr(
        vnc_api,
        "_wait_for_backend_auth_ready",
        lambda *args, **kwargs: rfb_probe.VncAuthProbeResult(
            True,
            False,
            "Your connection has been rejected to many attempts.",
        ),
    )

    response = client.post(
        "/api/vnc/establish",
        json={"agent_id": "test-device-agent", "remove_wallpaper": True},
    )

    assert response.status_code == 503
    assert response.get_json() == {
        "error": "vnc_backend_auth_rate_limited",
        "detail": "Your connection has been rejected to many attempts.",
        "retry_after_seconds": 90.0,
    }
    assert emitted_events == []
    assert fake_tunnel.transport_marks == []
    assert fake_tunnel.start_calls == []


def test_vnc_establish_caches_backend_auth_rate_limit(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    client = _client_with_admin_session(engine_harness)
    fake_tunnel = _FakeTunnelService()
    emitted_events: list[tuple[str, str, dict[str, Any]]] = []
    auth_probe_calls = 0
    _register_agent_credential(engine_harness)

    engine_harness.context.emit_agent_event = lambda agent_id, event, payload: emitted_events.append(
        (agent_id, event, dict(payload))
    ) or True

    monkeypatch.setenv("BOREALIS_VNC_AUTH_RATE_LIMIT_RETRY_SECONDS", "90")
    monkeypatch.setattr(vnc_api, "_get_tunnel_service", lambda _adapters: fake_tunnel)
    monkeypatch.setattr(vnc_api, "_wait_for_backend_ready", lambda *args, **kwargs: True)

    def _rate_limited_probe(*_args, **_kwargs):
        nonlocal auth_probe_calls
        auth_probe_calls += 1
        return rfb_probe.VncAuthProbeResult(
            True,
            False,
            "Your connection has been rejected to many attempts.",
        )

    monkeypatch.setattr(vnc_api, "_wait_for_backend_auth_ready", _rate_limited_probe)

    first = client.post(
        "/api/vnc/establish",
        json={"agent_id": "test-device-agent", "remove_wallpaper": True},
    )
    assert first.status_code == 503
    assert first.get_json()["error"] == "vnc_backend_auth_rate_limited"

    def _unexpected_probe(*_args, **_kwargs):
        raise AssertionError("cached auth lockout should skip RFB probe")

    monkeypatch.setattr(vnc_api, "_wait_for_backend_auth_ready", _unexpected_probe)

    second = client.post(
        "/api/vnc/establish",
        json={"agent_id": "test-device-agent", "remove_wallpaper": True},
    )

    assert second.status_code == 503
    assert second.get_json()["error"] == "vnc_backend_auth_rate_limited"
    assert second.get_json()["retry_after_seconds"] <= 90.0
    assert auth_probe_calls == 1
    assert emitted_events == []
    assert fake_tunnel.transport_marks == []
    assert fake_tunnel.start_calls == []


def test_vnc_establish_rejects_unknown_viewer(engine_harness: EngineTestHarness) -> None:
    client = _client_with_admin_session(engine_harness)
    _register_agent_credential(engine_harness)

    response = client.post(
        "/api/vnc/establish",
        json={"agent_id": "test-device-agent", "viewer": "rdp"},
    )

    assert response.status_code == 400
    assert response.get_json()["error"] == "invalid_viewer"


def test_vnc_establish_guacamole_returns_server_side_token_without_password(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    client = _client_with_admin_session(engine_harness)
    fake_tunnel = _FakeTunnelService()
    fake_registry = _FakeGuacamoleRegistry()
    engine_harness.context.public_base_url = "https://borealis.example.com"
    _register_agent_credential(
        engine_harness,
        password="secretpw",
        display_topology=[
            {
                "id": "1",
                "display_index": 1,
                "label": "1",
                "left": 0,
                "top": 0,
                "right": 1920,
                "bottom": 1080,
                "width": 1920,
                "height": 1080,
                "primary": True,
            }
        ],
    )

    monkeypatch.setattr(vnc_api, "_get_tunnel_service", lambda _adapters: fake_tunnel)
    monkeypatch.setattr(vnc_api, "_wait_for_backend_ready", lambda *args, **kwargs: True)
    monkeypatch.setattr(
        vnc_api,
        "guacd_health",
        lambda _context: {
            "enabled": True,
            "available": True,
            "reason": "ready",
            "host": "127.0.0.1",
            "port": 4822,
        },
    )

    response = client.post(
        "/api/vnc/establish",
        json={
            "agent_id": "test-device-agent",
            "viewer": "guacamole",
            "performance_preference": 2,
        },
    )

    assert response.status_code == 200
    payload = response.get_json()
    assert payload["viewer"] == "guacamole"
    assert payload["token"] == "worker-guacamole-token"
    assert payload["guacamole_ws_path"] == "/_borealis/site-workers/worker-vnc-route/remote-desktop/vnc/guacamole"
    assert payload["guacamole_ws_url"] == "wss://borealis.example.com/_borealis/site-workers/worker-vnc-route/remote-desktop/vnc/guacamole"
    assert "vnc_password" not in payload
    worker_request = engine_harness.context.worker_guacamole_requests[0]["payload"]
    assert worker_request["password"] == "secretpw"
    assert worker_request["host"] == "10.255.0.2"
    assert worker_request["width"] == 1920
    assert worker_request["height"] == 1080
    assert worker_request["performance_preference"] == 2
    assert payload["performance_preference"] == 2


def test_vnc_establish_guacamole_unavailable_returns_503(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    client = _client_with_admin_session(engine_harness)
    fake_tunnel = _FakeTunnelService()
    _register_agent_credential(engine_harness)

    monkeypatch.setattr(vnc_api, "_get_tunnel_service", lambda _adapters: fake_tunnel)
    monkeypatch.setattr(vnc_api, "_wait_for_backend_ready", lambda *args, **kwargs: True)
    monkeypatch.setattr(
        vnc_api,
        "guacd_health",
        lambda _context: {
            "enabled": True,
            "available": False,
            "reason": "connection refused",
            "host": "127.0.0.1",
            "port": 4822,
        },
    )

    response = client.post(
        "/api/vnc/establish",
        json={"agent_id": "test-device-agent", "viewer": "guacamole"},
    )

    assert response.status_code == 503
    payload = response.get_json()
    assert payload["error"] == "guacamole_unavailable"


def test_vnc_viewers_reports_guacamole_health(engine_harness: EngineTestHarness, monkeypatch) -> None:
    client = _client_with_admin_session(engine_harness)
    monkeypatch.setattr(
        vnc_api,
        "guacd_health",
        lambda _context: {
            "enabled": True,
            "available": True,
            "reason": "ready",
            "host": "127.0.0.1",
            "port": 4822,
        },
    )

    response = client.get("/api/vnc/viewers")

    assert response.status_code == 200
    payload = response.get_json()
    viewers = {viewer["id"]: viewer for viewer in payload["viewers"]}
    assert payload["default_viewer"] == "guacamole"
    assert set(viewers) == {"guacamole"}
    assert viewers["guacamole"]["available"] is True
    assert payload["guacamole"]["ws_path"] == "/remote-desktop/vnc/guacamole"


def test_vnc_establish_uses_longer_initial_wait_and_shorter_retry_wait(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    client = _client_with_admin_session(engine_harness)
    fake_tunnel = _FakeTunnelService()
    wait_calls: list[float] = []
    _register_agent_credential(engine_harness)
    engine_harness.context.agent_socket_registry = None
    engine_harness.context.emit_agent_event = lambda agent_id, event, payload: True

    monkeypatch.setattr(vnc_api, "_get_tunnel_service", lambda _adapters: fake_tunnel)

    def _fake_wait(*_args, **kwargs):
        wait_calls.append(float(kwargs["timeout_seconds"]))
        return len(wait_calls) > 2

    monkeypatch.setattr(vnc_api, "_wait_for_backend_ready", _fake_wait)
    monkeypatch.setattr(vnc_api.time, "sleep", lambda _seconds: None)

    response = client.post("/api/vnc/establish", json={"agent_id": "test-device-agent"})

    assert response.status_code == 200
    assert wait_calls == [0.75, 12.0, 8.0]
    assert fake_tunnel.transport_marks == [("test-device-agent", "vnc_bootstrap"), ("test-device-agent", "vnc_connect_retry")]
    assert fake_tunnel.start_calls == [
        ("test-device-agent", False, "vnc_bootstrap"),
        ("test-device-agent", True, "vnc_connect_retry"),
    ]
    assert fake_tunnel.transport_recovers == [
        ("test-device-agent", "vnc_connect", "vnc_connect_retry")
    ]


def test_vnc_establish_uses_shorter_waits_for_cached_online_agents(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    client = _client_with_admin_session(engine_harness)
    fake_tunnel = _FakeTunnelService()
    wait_calls: list[float] = []
    _register_agent_credential(engine_harness)
    engine_harness.context.agent_socket_registry = SimpleNamespace(
        is_registered=lambda agent_id: True
    )
    engine_harness.context.emit_agent_event = lambda agent_id, event, payload: True

    monkeypatch.setenv("BOREALIS_VNC_SOCKET_READY_WAIT_SECONDS", "2.5")
    monkeypatch.setenv("BOREALIS_VNC_SOCKET_RETRY_READY_WAIT_SECONDS", "2.0")
    monkeypatch.setenv("BOREALIS_VNC_SOCKET_READY_POLL_INTERVAL_SECONDS", "0.2")
    monkeypatch.setenv("BOREALIS_VNC_SOCKET_POST_BOOTSTRAP_GRACE_SECONDS", "0")
    monkeypatch.setenv("BOREALIS_VNC_SOCKET_SOFT_RETRY_WAIT_SECONDS", "0")
    monkeypatch.setattr(vnc_api, "_get_tunnel_service", lambda _adapters: fake_tunnel)

    def _fake_wait(*_args, **kwargs):
        wait_calls.append(float(kwargs["timeout_seconds"]))
        return len(wait_calls) > 2

    monkeypatch.setattr(vnc_api, "_wait_for_backend_ready", _fake_wait)
    monkeypatch.setattr(vnc_api.time, "sleep", lambda _seconds: None)

    response = client.post("/api/vnc/establish", json={"agent_id": "test-device-agent"})

    assert response.status_code == 200
    assert wait_calls == [0.75, 2.5, 2.0]
    assert fake_tunnel.transport_marks == [("test-device-agent", "vnc_bootstrap"), ("test-device-agent", "vnc_connect_retry")]
    assert fake_tunnel.start_calls == [
        ("test-device-agent", False, "vnc_bootstrap"),
        ("test-device-agent", True, "vnc_connect_retry"),
    ]


def test_vnc_establish_prewarm_cached_online_tunnel_before_fast_probe(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    client = _client_with_admin_session(engine_harness)
    fake_tunnel = _FakeTunnelService()
    wait_calls: list[float] = []
    emitted_events: list[tuple[str, str, dict[str, Any]]] = []
    _register_agent_credential(engine_harness)
    engine_harness.context.agent_socket_registry = SimpleNamespace(
        is_registered=lambda agent_id: True
    )
    engine_harness.context.emit_agent_event = (
        lambda agent_id, event, payload: emitted_events.append((agent_id, event, dict(payload))) or True
    )
    fake_tunnel.session_payload = lambda agent_id, include_token=False: {
        "tunnel_id": "tun-vnc-1",
        "agent_id": agent_id,
        "virtual_ip": "10.255.0.2/32",
        "engine_virtual_ip": "10.255.0.1/32",
    }

    monkeypatch.setattr(vnc_api, "_get_tunnel_service", lambda _adapters: fake_tunnel)

    def _fake_wait(*_args, **kwargs):
        wait_calls.append(float(kwargs["timeout_seconds"]))
        return True

    monkeypatch.setattr(vnc_api, "_wait_for_backend_ready", _fake_wait)

    response = client.post("/api/vnc/establish", json={"agent_id": "test-device-agent"})

    assert response.status_code == 200
    assert wait_calls == [2.0]
    assert fake_tunnel.connect_calls == []
    assert fake_tunnel.transport_marks == [("test-device-agent", "vnc_backend_prewarm")]
    assert fake_tunnel.start_calls == [("test-device-agent", False, "vnc_backend_prewarm")]
    assert fake_tunnel.transport_recovers == []
    assert emitted_events == []


def test_vnc_establish_uses_post_bootstrap_grace_before_retry_for_cached_online_agents(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    client = _client_with_admin_session(engine_harness)
    fake_tunnel = _FakeTunnelService()
    wait_calls: list[float] = []
    _register_agent_credential(engine_harness)
    engine_harness.context.agent_socket_registry = SimpleNamespace(
        is_registered=lambda agent_id: True
    )
    engine_harness.context.emit_agent_event = lambda agent_id, event, payload: True

    monkeypatch.setenv("BOREALIS_VNC_SOCKET_READY_WAIT_SECONDS", "2.5")
    monkeypatch.setenv("BOREALIS_VNC_SOCKET_RETRY_READY_WAIT_SECONDS", "2.0")
    monkeypatch.setenv("BOREALIS_VNC_SOCKET_READY_POLL_INTERVAL_SECONDS", "0.2")
    monkeypatch.setenv("BOREALIS_VNC_SOCKET_POST_BOOTSTRAP_GRACE_SECONDS", "1.5")
    monkeypatch.setenv("BOREALIS_VNC_SOCKET_POST_BOOTSTRAP_GRACE_POLL_INTERVAL_SECONDS", "0.1")
    monkeypatch.setenv("BOREALIS_VNC_SOCKET_SOFT_RETRY_WAIT_SECONDS", "0")
    monkeypatch.setattr(vnc_api, "_get_tunnel_service", lambda _adapters: fake_tunnel)

    def _fake_wait(*_args, **kwargs):
        wait_calls.append(float(kwargs["timeout_seconds"]))
        return len(wait_calls) > 2

    monkeypatch.setattr(vnc_api, "_wait_for_backend_ready", _fake_wait)
    monkeypatch.setattr(vnc_api.time, "sleep", lambda _seconds: None)

    response = client.post("/api/vnc/establish", json={"agent_id": "test-device-agent"})

    assert response.status_code == 200
    assert wait_calls == [0.75, 2.5, 1.5]
    assert fake_tunnel.transport_marks == [("test-device-agent", "vnc_bootstrap")]
    assert fake_tunnel.start_calls == [
        ("test-device-agent", False, "vnc_bootstrap"),
    ]
    assert fake_tunnel.transport_recovers == []


def test_vnc_establish_uses_soft_retry_before_transport_recovery_for_cached_online_agents(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    client = _client_with_admin_session(engine_harness)
    fake_tunnel = _FakeTunnelService()
    wait_calls: list[float] = []
    _register_agent_credential(engine_harness)
    engine_harness.context.agent_socket_registry = SimpleNamespace(
        is_registered=lambda agent_id: True
    )
    engine_harness.context.emit_agent_event = lambda agent_id, event, payload: True

    monkeypatch.setenv("BOREALIS_VNC_SOCKET_READY_WAIT_SECONDS", "2.5")
    monkeypatch.setenv("BOREALIS_VNC_SOCKET_RETRY_READY_WAIT_SECONDS", "2.0")
    monkeypatch.setenv("BOREALIS_VNC_SOCKET_READY_POLL_INTERVAL_SECONDS", "0.2")
    monkeypatch.setenv("BOREALIS_VNC_SOCKET_POST_BOOTSTRAP_GRACE_SECONDS", "1.0")
    monkeypatch.setenv("BOREALIS_VNC_SOCKET_POST_BOOTSTRAP_GRACE_POLL_INTERVAL_SECONDS", "0.1")
    monkeypatch.setenv("BOREALIS_VNC_SOCKET_SOFT_RETRY_WAIT_SECONDS", "1.5")
    monkeypatch.setenv("BOREALIS_VNC_SOCKET_SOFT_RETRY_POLL_INTERVAL_SECONDS", "0.1")
    monkeypatch.setattr(vnc_api, "_get_tunnel_service", lambda _adapters: fake_tunnel)

    def _fake_wait(*_args, **kwargs):
        wait_calls.append(float(kwargs["timeout_seconds"]))
        return len(wait_calls) > 3

    monkeypatch.setattr(vnc_api, "_wait_for_backend_ready", _fake_wait)
    monkeypatch.setattr(vnc_api.time, "sleep", lambda _seconds: None)

    response = client.post("/api/vnc/establish", json={"agent_id": "test-device-agent"})

    assert response.status_code == 200
    assert wait_calls == [0.75, 2.5, 1.0, 1.5]
    assert fake_tunnel.transport_marks == [
        ("test-device-agent", "vnc_bootstrap"),
        ("test-device-agent", "vnc_connect_retry_soft"),
    ]
    assert fake_tunnel.start_calls == [
        ("test-device-agent", False, "vnc_bootstrap"),
        ("test-device-agent", False, "vnc_connect_retry_soft"),
    ]
    assert fake_tunnel.transport_recovers == []


def test_vnc_establish_uses_shorter_waits_for_recently_healthy_reconnects(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    client = _client_with_admin_session(engine_harness)
    fake_tunnel = _FakeTunnelService()
    wait_calls: list[float] = []
    _register_agent_credential(engine_harness)
    manager = vnc_api.ensure_vnc_collaboration_manager(engine_harness.context, logger=engine_harness.context.logger)
    manager.upsert_agent_credential(
        agent_id="test-device-agent",
        controller_password="bootpass",
        credential_revision=42,
    )
    session, _participant, _created = manager.ensure_session(
        agent_id="test-device-agent",
        operator_id="admin",
        controller_password="bootpass",
        credential_revision=42,
        remove_wallpaper=True,
    )
    manager.record_backend_ready(
        session.session_id,
        tunnel_id="tun-vnc-1",
        allowed_ips="10.255.0.1/32",
        engine_virtual_ip="10.255.0.1/32",
    )
    engine_harness.context.agent_socket_registry = SimpleNamespace(
        is_registered=lambda agent_id: True
    )
    engine_harness.context.emit_agent_event = lambda agent_id, event, payload: True

    monkeypatch.setenv("BOREALIS_VNC_WARM_READY_WAIT_SECONDS", "2.5")
    monkeypatch.setenv("BOREALIS_VNC_WARM_RETRY_READY_WAIT_SECONDS", "2.0")
    monkeypatch.setenv("BOREALIS_VNC_WARM_READY_POLL_INTERVAL_SECONDS", "0.2")
    monkeypatch.setenv("BOREALIS_VNC_WARM_POST_BOOTSTRAP_GRACE_SECONDS", "0")
    monkeypatch.setenv("BOREALIS_VNC_WARM_SOFT_RETRY_WAIT_SECONDS", "0")
    monkeypatch.setattr(vnc_api, "_get_tunnel_service", lambda _adapters: fake_tunnel)

    def _fake_wait(*_args, **kwargs):
        wait_calls.append(float(kwargs["timeout_seconds"]))
        return len(wait_calls) > 2

    monkeypatch.setattr(vnc_api, "_wait_for_backend_ready", _fake_wait)
    monkeypatch.setattr(vnc_api.time, "sleep", lambda _seconds: None)

    response = client.post("/api/vnc/establish", json={"agent_id": "test-device-agent"})

    assert response.status_code == 200
    assert wait_calls == [0.75, 2.5, 2.0]
    assert fake_tunnel.transport_marks == [("test-device-agent", "vnc_bootstrap"), ("test-device-agent", "vnc_connect_retry")]
    assert fake_tunnel.start_calls == [
        ("test-device-agent", False, "vnc_bootstrap"),
        ("test-device-agent", True, "vnc_connect_retry"),
    ]


def test_vnc_establish_defaults_without_display_topology(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    client = _client_with_admin_session(engine_harness)
    fake_tunnel = _FakeTunnelService()
    _register_agent_credential(engine_harness)

    monkeypatch.setattr(vnc_api, "_get_tunnel_service", lambda _adapters: fake_tunnel)
    monkeypatch.setattr(vnc_api, "_wait_for_backend_ready", lambda *args, **kwargs: True)

    response = client.post("/api/vnc/establish", json={"agent_id": "test-device-agent"})

    assert response.status_code == 200
    payload = response.get_json()
    assert payload["display_topology"] == []
    assert payload["display_virtual_bounds"] == {}


def test_vnc_establish_skips_bootstrap_settle_when_fast_probe_succeeds(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    admin_client = _client_with_session(engine_harness, username="admin")
    peer_client = _client_with_session(engine_harness, username="alice")
    fake_tunnel = _FakeTunnelService()
    sleep_calls: list[float] = []
    _register_agent_credential(engine_harness)

    monkeypatch.setattr(vnc_api, "_get_tunnel_service", lambda _adapters: fake_tunnel)
    monkeypatch.setattr(vnc_api, "_wait_for_backend_ready", lambda *args, **kwargs: True)

    def _record_vnc_sleep(seconds: float) -> None:
        for frame in inspect.stack()[1:6]:
            if frame.filename.replace("\\", "/").endswith("/services/API/devices/vnc.py"):
                sleep_calls.append(float(seconds))
                break

    monkeypatch.setattr(vnc_api.time, "sleep", _record_vnc_sleep)

    controller_response = admin_client.post("/api/vnc/establish", json={"agent_id": "test-device-agent"})
    peer_response = peer_client.post("/api/vnc/establish", json={"agent_id": "test-device-agent"})

    assert controller_response.status_code == 200
    assert peer_response.status_code == 200
    assert peer_response.get_json()["participant_role"] == "controller"
    assert sleep_calls == []


def test_vnc_handoff_updates_session_owner_without_forcing_reconnect(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    admin_client = _client_with_session(engine_harness, username="admin")
    peer_client = _client_with_session(engine_harness, username="alice")
    fake_tunnel = _FakeTunnelService()
    fake_proxy = _FakeProxy()
    emitted_events: list[tuple[str, str, dict[str, Any]]] = []
    _register_agent_credential(engine_harness)

    engine_harness.context.vnc_proxy = fake_proxy
    engine_harness.context.emit_agent_event = (
        lambda agent_id, event, payload: emitted_events.append((agent_id, event, dict(payload))) or True
    )

    monkeypatch.setattr(vnc_api, "_get_tunnel_service", lambda _adapters: fake_tunnel)
    monkeypatch.setattr(vnc_api, "_wait_for_backend_ready", lambda *args, **kwargs: True)

    controller_response = admin_client.post("/api/vnc/establish", json={"agent_id": "test-device-agent"})
    controller_payload = controller_response.get_json()
    peer_response = peer_client.post("/api/vnc/establish", json={"agent_id": "test-device-agent"})
    peer_payload = peer_response.get_json()
    baseline_event_count = len(emitted_events)

    assert controller_response.status_code == 200
    assert peer_response.status_code == 200
    assert peer_payload["participant_role"] == "controller"

    handoff_response = admin_client.post(
        "/api/vnc/handoff",
        json={
            "session_id": controller_payload["session_id"],
            "target_operator_id": "alice",
        },
    )

    assert handoff_response.status_code == 200
    payload = handoff_response.get_json()
    assert payload["status"] == "ok"
    assert payload["reconnect_required"] is False
    assert payload["participant_role"] == "controller"
    assert payload["session"]["controller_operator_id"] == "alice"
    assert payload["session"]["credential_revision"] == 42
    assert fake_proxy.disconnect_session_calls == []
    assert len(emitted_events) == baseline_event_count


def test_vnc_disconnect_keeps_shared_session_active_for_remaining_participants(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    admin_client = _client_with_session(engine_harness, username="admin")
    peer_client = _client_with_session(engine_harness, username="alice")
    fake_tunnel = _FakeTunnelService()
    fake_proxy = _FakeProxy()
    emitted_events: list[tuple[str, str, dict[str, Any]]] = []
    _register_agent_credential(engine_harness)

    engine_harness.context.vnc_proxy = fake_proxy
    engine_harness.context.emit_agent_event = (
        lambda agent_id, event, payload: emitted_events.append((agent_id, event, dict(payload))) or True
    )

    monkeypatch.setattr(vnc_api, "_get_tunnel_service", lambda _adapters: fake_tunnel)
    monkeypatch.setattr(vnc_api, "_wait_for_backend_ready", lambda *args, **kwargs: True)

    controller_response = admin_client.post("/api/vnc/establish", json={"agent_id": "test-device-agent"})
    controller_payload = controller_response.get_json()
    peer_response = peer_client.post("/api/vnc/establish", json={"agent_id": "test-device-agent"})
    peer_payload = peer_response.get_json()
    baseline_event_count = len(emitted_events)
    assert peer_response.status_code == 200
    assert peer_payload["participant_role"] == "controller"

    disconnect_response = admin_client.post(
        "/api/vnc/disconnect",
        json={"session_id": controller_payload["session_id"]},
    )

    assert disconnect_response.status_code == 200
    disconnect_payload = disconnect_response.get_json()
    assert disconnect_payload["status"] == "left"
    assert disconnect_payload["controller_vacant"] is False
    assert disconnect_payload["reconnect_pending"] is False
    assert disconnect_payload["session"]["state"] == "active"
    assert disconnect_payload["session"]["controller_operator_id"] == "alice"
    assert engine_harness.app.worker_guacamole_disconnects[0]["payload"] == {
        "session_id": controller_payload["session_id"],
        "participant_id": controller_payload["participant_id"],
        "reason": "operator_disconnect",
        "close_session": False,
    }
    assert fake_proxy.disconnect_session_calls == []
    assert len(emitted_events) == baseline_event_count

    sessions_response = peer_client.get(
        f"/api/vnc/sessions?session_id={controller_payload['session_id']}"
    )
    assert sessions_response.status_code == 200
    sessions_payload = sessions_response.get_json()
    assert sessions_payload["count"] == 1
    assert sessions_payload["sessions"][0]["controller_vacant"] is False
    assert sessions_payload["sessions"][0]["current_operator_role"] == "controller"


def test_vnc_disconnect_retains_last_controller_session_for_warm_reconnect(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    admin_client = _client_with_session(engine_harness, username="admin")
    fake_tunnel = _FakeTunnelService()
    fake_proxy = _FakeProxy()
    emitted_events: list[tuple[str, str, dict[str, Any]]] = []
    _register_agent_credential(engine_harness)

    engine_harness.context.vnc_proxy = fake_proxy
    engine_harness.context.emit_agent_event = (
        lambda agent_id, event, payload: emitted_events.append((agent_id, event, dict(payload))) or True
    )

    monkeypatch.setattr(vnc_api, "_get_tunnel_service", lambda _adapters: fake_tunnel)
    monkeypatch.setattr(vnc_api, "_wait_for_backend_ready", lambda *args, **kwargs: True)

    establish_response = admin_client.post("/api/vnc/establish", json={"agent_id": "test-device-agent"})
    assert establish_response.status_code == 200
    establish_payload = establish_response.get_json()

    disconnect_response = admin_client.post(
        "/api/vnc/disconnect",
        json={"session_id": establish_payload["session_id"]},
    )

    assert disconnect_response.status_code == 200
    disconnect_payload = disconnect_response.get_json()
    assert disconnect_payload["status"] == "left"
    assert disconnect_payload["controller_vacant"] is False
    assert disconnect_payload["reconnect_pending"] is True
    assert disconnect_payload["session"]["state"] == "reconnect_pending"
    assert engine_harness.app.worker_guacamole_disconnects[0]["payload"] == {
        "session_id": establish_payload["session_id"],
        "participant_id": establish_payload["participant_id"],
        "reason": "operator_disconnect",
        "close_session": False,
    }
    assert emitted_events[-1][1] == "vnc_stop"
    assert emitted_events[-1][2]["reason"] == "operator_disconnect"

    reconnect_response = admin_client.post("/api/vnc/establish", json={"agent_id": "test-device-agent"})
    assert reconnect_response.status_code == 200
    reconnect_payload = reconnect_response.get_json()
    assert reconnect_payload["session_id"] == establish_payload["session_id"]
    assert reconnect_payload["participant_id"] == establish_payload["participant_id"]
    assert reconnect_payload["participant_role"] == "controller"
    assert reconnect_payload["credential_revision"] == establish_payload["credential_revision"]
    assert reconnect_payload["viewer"] == "guacamole"
    assert "vnc_password" not in reconnect_payload
    assert reconnect_payload["session"]["state"] == "active"


def test_vnc_establish_requires_advertised_agent_credential(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    client = _client_with_admin_session(engine_harness)
    fake_tunnel = _FakeTunnelService()

    monkeypatch.setattr(vnc_api, "_get_tunnel_service", lambda _adapters: fake_tunnel)
    monkeypatch.setattr(vnc_api, "_wait_for_backend_ready", lambda *args, **kwargs: True)

    response = client.post("/api/vnc/establish", json={"agent_id": "test-device-agent"})

    assert response.status_code == 503
    assert response.get_json()["error"] == "vnc_agent_live_credentials_unavailable"


def test_vnc_establish_requests_agent_credential_refresh_when_cache_is_empty(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    client = _client_with_admin_session(engine_harness)
    fake_tunnel = _FakeTunnelService()
    fake_registry = _FakeRegistry()
    emitted_events: list[tuple[str, str, dict[str, Any]]] = []
    _register_agent_credential(
        engine_harness,
        display_topology=[
            {
                "id": "1",
                "display_index": 1,
                "label": "1",
                "left": 0,
                "top": 0,
                "right": 1920,
                "bottom": 1080,
                "width": 1920,
                "height": 1080,
                "primary": True,
            }
        ],
    )
    engine_harness.context.emit_agent_event = (
        lambda agent_id, event, payload: emitted_events.append((agent_id, event, dict(payload))) or True
    )

    monkeypatch.setattr(vnc_api, "_get_tunnel_service", lambda _adapters: fake_tunnel)
    monkeypatch.setattr(vnc_api, "_wait_for_backend_ready", lambda *args, **kwargs: True)

    response = client.post("/api/vnc/establish", json={"agent_id": "test-device-agent"})

    assert response.status_code == 200
    payload = response.get_json()
    assert emitted_events == []
    assert payload["display_topology"][0]["label"] == "1"
    assert engine_harness.context.worker_guacamole_requests[0]["payload"]["agent_id"] == "test-device-agent"


def test_vnc_establish_uses_shorter_waits_after_fresh_credential_refresh(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    client = _client_with_admin_session(engine_harness)
    fake_tunnel = _FakeTunnelService()
    fake_registry = _FakeRegistry()
    emitted_events: list[tuple[str, str, dict[str, Any]]] = []
    wait_calls: list[float] = []
    _register_agent_credential(engine_harness)
    engine_harness.context.emit_agent_event = (
        lambda agent_id, event, payload: emitted_events.append((agent_id, event, dict(payload))) or True
    )
    engine_harness.context.agent_socket_registry = SimpleNamespace(
        is_registered=lambda agent_id: True
    )
    monkeypatch.setenv("BOREALIS_VNC_SOCKET_READY_WAIT_SECONDS", "2.5")
    monkeypatch.setenv("BOREALIS_VNC_SOCKET_RETRY_READY_WAIT_SECONDS", "2.0")
    monkeypatch.setenv("BOREALIS_VNC_SOCKET_READY_POLL_INTERVAL_SECONDS", "0.2")
    monkeypatch.setenv("BOREALIS_VNC_SOCKET_POST_BOOTSTRAP_GRACE_SECONDS", "0")
    monkeypatch.setenv("BOREALIS_VNC_SOCKET_SOFT_RETRY_WAIT_SECONDS", "0")
    monkeypatch.setattr(vnc_api, "_get_tunnel_service", lambda _adapters: fake_tunnel)

    def _fake_wait(*_args, **kwargs):
        wait_calls.append(float(kwargs["timeout_seconds"]))
        return len(wait_calls) > 2

    monkeypatch.setattr(vnc_api, "_wait_for_backend_ready", _fake_wait)
    monkeypatch.setattr(vnc_api.time, "sleep", lambda _seconds: None)

    response = client.post("/api/vnc/establish", json={"agent_id": "test-device-agent"})

    assert response.status_code == 200
    assert all(event != "vnc_refresh" for _agent_id, event, _payload in emitted_events)
    assert wait_calls == [0.75, 2.5, 2.0]


def test_agent_socket_connect_prewarms_vnc_credential_when_cache_is_empty(
    engine_harness: EngineTestHarness,
) -> None:
    engine_harness.context.vpn_tunnel_service = SimpleNamespace(
        session_payload=lambda agent_id, include_token=True: None
    )
    socket_client = engine_harness.context.socketio.test_client(engine_harness.app)

    assert socket_client.is_connected()

    socket_client.emit(
        "connect_agent",
        {"agent_id": "test-device-agent", "hostname": "TEST-DEVICE", "service_mode": "system"},
    )

    received = socket_client.get_received()
    refresh_events = [item for item in received if item["name"] == "vnc_refresh"]
    assert refresh_events == []

    socket_client.disconnect()


def test_vnc_establish_returns_agent_socket_missing_when_backend_needs_bootstrap_but_agent_is_offline(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    client = _client_with_admin_session(engine_harness)
    fake_tunnel = _FakeTunnelService()
    _register_agent_credential(engine_harness)
    engine_harness.context.agent_socket_registry = SimpleNamespace(
        is_registered=lambda agent_id: False
    )

    monkeypatch.setattr(vnc_api, "_get_tunnel_service", lambda _adapters: fake_tunnel)
    monkeypatch.setattr(vnc_api, "_wait_for_backend_ready", lambda *args, **kwargs: False)

    response = client.post("/api/vnc/establish", json={"agent_id": "test-device-agent"})

    assert response.status_code == 409
    assert response.get_json()["error"] == "agent_socket_missing"
    assert fake_tunnel.transport_marks == []
    assert fake_tunnel.start_calls == []


def test_vnc_establish_returns_agent_socket_missing_when_vnc_start_emit_fails(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    client = _client_with_admin_session(engine_harness)
    fake_tunnel = _FakeTunnelService()
    _register_agent_credential(engine_harness)
    engine_harness.context.agent_socket_registry = SimpleNamespace(
        is_registered=lambda agent_id: True
    )
    engine_harness.context.emit_agent_event = lambda agent_id, event, payload: False

    monkeypatch.setattr(vnc_api, "_get_tunnel_service", lambda _adapters: fake_tunnel)

    wait_calls: list[float] = []

    def _fake_wait(*_args, **kwargs):
        wait_calls.append(float(kwargs["timeout_seconds"]))
        return False

    monkeypatch.setattr(vnc_api, "_wait_for_backend_ready", _fake_wait)

    response = client.post("/api/vnc/establish", json={"agent_id": "test-device-agent"})

    assert response.status_code == 409
    assert response.get_json()["error"] == "agent_socket_missing"
    assert wait_calls == [0.75]
    assert fake_tunnel.transport_marks == [("test-device-agent", "vnc_bootstrap")]
    assert fake_tunnel.start_calls == [("test-device-agent", False, "vnc_bootstrap")]
