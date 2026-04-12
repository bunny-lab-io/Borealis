# ======================================================
# Data\Engine\Unit_Tests\test_wireguard_server.py
# Description: Validates Linux WireGuard listener file hardening and
#              health checks used by the reverse VPN transport.
# ======================================================

from __future__ import annotations

import os
import stat
from pathlib import Path

import pytest

from Data.Engine.services.VPN import wireguard_server
from Data.Engine.services.VPN.wireguard_server import WireGuardServerConfig, WireGuardServerManager


def _build_config(root: Path) -> WireGuardServerConfig:
    cert_dir = root / "certificates"
    cert_dir.mkdir(parents=True, exist_ok=True)
    logs_dir = root / "logs"
    logs_dir.mkdir(parents=True, exist_ok=True)
    return WireGuardServerConfig(
        port=30000,
        engine_virtual_ip="10.255.0.1/24",
        peer_network="10.255.0.0/24",
        private_key_path=cert_dir / "server.key",
        public_key_path=cert_dir / "server.pub",
        acl_allowlist_ports=(47002, 5900, 22),
        log_path=logs_dir / "wireguard.log",
    )


@pytest.mark.skipif(os.name == "nt", reason="Linux permission checks do not apply on Windows.")
def test_start_listener_secures_linux_runtime_files(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(wireguard_server.engine_config, "PROJECT_ROOT", tmp_path)
    manager = WireGuardServerManager(_build_config(tmp_path))
    monkeypatch.setattr(manager, "_linux_interface_exists", lambda: False)
    monkeypatch.setattr(manager, "_linux_bring_up", lambda _config_path: None)
    monkeypatch.setattr(manager, "_linux_upsert_peer", lambda _peer: None)
    monkeypatch.setattr(manager, "_ensure_linux_peer_route", lambda: None)
    monkeypatch.setattr(manager, "_ensure_linux_listener_rule", lambda: None)

    manager.start_listener(
        [
            {
                "agent_id": "agent-1",
                "allowed_ips": ["10.255.0.2/32"],
                "public_key": "test-public-key",
            }
        ]
    )

    config_path = manager._listener_config_path()
    assert stat.S_IMODE(config_path.stat().st_mode) == 0o600
    assert stat.S_IMODE(manager.config.private_key_path.stat().st_mode) == 0o600
    assert stat.S_IMODE(manager.config.public_key_path.stat().st_mode) == 0o600


def test_check_listener_health_requires_configured_peers(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(wireguard_server.engine_config, "PROJECT_ROOT", tmp_path)
    manager = WireGuardServerManager(_build_config(tmp_path))
    manager._wg = "wg"
    manager._ip = "ip"
    monkeypatch.setattr(manager, "_linux_interface_exists", lambda: True)

    def _run_without_peers(args):
        if args == ["wg", "show", manager._interface_name]:
            return 0, "", ""
        if args == ["wg", "show", manager._interface_name, "peers"]:
            return 0, "", ""
        return 1, "", "unexpected command"

    monkeypatch.setattr(manager, "_run_command", _run_without_peers)
    health = manager.check_listener_health()
    assert health["healthy"] is False
    assert health["reason"] == "no_peers_configured"
    assert health["peer_count"] == 0

    def _run_with_peers(args):
        if args == ["wg", "show", manager._interface_name]:
            return 0, "", ""
        if args == ["wg", "show", manager._interface_name, "peers"]:
            return 0, "peer-public-key\n", ""
        return 1, "", "unexpected command"

    monkeypatch.setattr(manager, "_run_command", _run_with_peers)
    healthy = manager.check_listener_health()
    assert healthy["healthy"] is True
    assert healthy["reason"] == "listener_running"
    assert healthy["peer_count"] == 1


def test_check_listener_health_detects_stale_managed_interfaces(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(wireguard_server.engine_config, "PROJECT_ROOT", tmp_path)
    manager = WireGuardServerManager(_build_config(tmp_path))
    manager._wg = "wg"
    manager._ip = "ip"
    monkeypatch.setattr(manager, "_linux_interface_exists", lambda name=None: True)
    monkeypatch.setattr(manager, "_linux_list_wireguard_interfaces", lambda: ["borealis-wg", "borealis"])

    health = manager.check_listener_health()
    assert health["healthy"] is False
    assert health["reason"] == "stale_interface_present"
    assert health["stale_interfaces"] == "borealis"


def test_start_listener_rejects_duplicate_allowed_ips(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(wireguard_server.engine_config, "PROJECT_ROOT", tmp_path)
    manager = WireGuardServerManager(_build_config(tmp_path))

    with pytest.raises(ValueError, match="already assigned"):
        manager.start_listener(
            [
                {
                    "agent_id": "agent-1",
                    "allowed_ips": ["10.255.0.2/32"],
                    "public_key": "peer-public-key-1",
                },
                {
                    "agent_id": "agent-2",
                    "allowed_ips": ["10.255.0.2/32"],
                    "public_key": "peer-public-key-2",
                },
            ]
        )


@pytest.mark.skipif(os.name == "nt", reason="Linux listener lifecycle checks do not apply on Windows.")
def test_ensure_listener_bootstraps_interface_when_absent(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(wireguard_server.engine_config, "PROJECT_ROOT", tmp_path)
    manager = WireGuardServerManager(_build_config(tmp_path))
    up_calls: list[Path] = []
    apply_runtime_calls: list[str] = []
    route_calls: list[str] = []

    monkeypatch.setattr(manager, "_linux_interface_exists", lambda: False)
    monkeypatch.setattr(manager, "_linux_bring_up", lambda config_path: up_calls.append(config_path))
    monkeypatch.setattr(manager, "_linux_apply_interface_runtime", lambda: apply_runtime_calls.append("apply"))
    monkeypatch.setattr(manager, "_ensure_linux_peer_route", lambda: route_calls.append(str(manager.config.peer_subnet())))
    monkeypatch.setattr(manager, "_ensure_linux_listener_rule", lambda: None)

    manager.ensure_listener()

    assert up_calls == [manager._listener_config_path()]
    assert apply_runtime_calls == []
    assert route_calls == [str(manager.config.peer_subnet())]


@pytest.mark.skipif(os.name == "nt", reason="Linux listener lifecycle checks do not apply on Windows.")
def test_ensure_listener_reapplies_peer_route_when_interface_present(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(wireguard_server.engine_config, "PROJECT_ROOT", tmp_path)
    manager = WireGuardServerManager(_build_config(tmp_path))
    apply_runtime_calls: list[str] = []
    route_calls: list[str] = []

    monkeypatch.setattr(manager, "_linux_interface_exists", lambda: True)
    monkeypatch.setattr(manager, "_linux_apply_interface_runtime", lambda: apply_runtime_calls.append("apply"))
    monkeypatch.setattr(manager, "_ensure_linux_peer_route", lambda: route_calls.append(str(manager.config.peer_subnet())))
    monkeypatch.setattr(manager, "_ensure_linux_listener_rule", lambda: None)

    manager.ensure_listener()

    assert apply_runtime_calls == ["apply"]
    assert route_calls == [str(manager.config.peer_subnet())]


@pytest.mark.skipif(os.name == "nt", reason="Linux listener lifecycle checks do not apply on Windows.")
def test_cleanup_stale_runtime_removes_legacy_interfaces(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(wireguard_server.engine_config, "PROJECT_ROOT", tmp_path)
    manager = WireGuardServerManager(_build_config(tmp_path))
    manager._ip = "ip"

    removed: list[str] = []
    monkeypatch.setattr(manager, "_linux_list_wireguard_interfaces", lambda: ["borealis-wg", "borealis", "wg0"])
    monkeypatch.setattr(manager, "_linux_delete_interface", lambda name=None: removed.append(str(name or "")))

    cleanup = manager.cleanup_stale_runtime()

    assert cleanup == ["borealis"]
    assert removed == ["borealis"]


@pytest.mark.skipif(os.name == "nt", reason="Linux listener lifecycle checks do not apply on Windows.")
def test_linux_bring_up_deletes_stale_interface_before_retry(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(wireguard_server.engine_config, "PROJECT_ROOT", tmp_path)
    manager = WireGuardServerManager(_build_config(tmp_path))
    manager._wg_quick = "wg-quick"
    manager._ip = "ip"

    commands: list[list[str]] = []
    interface_present = {"value": True}

    def _fake_run(args):
        commands.append(list(args))
        if args == ["wg-quick", "up", str(manager._listener_config_path())] and commands.count(list(args)) == 1:
            return 1, "", "already exists"
        if args == ["wg-quick", "down", str(manager._listener_config_path())]:
            return 1, "", "down failed"
        if args == ["ip", "link", "delete", "dev", manager._interface_name]:
            interface_present["value"] = False
            return 0, "", ""
        if args == ["wg-quick", "up", str(manager._listener_config_path())]:
            return 0, "", ""
        return 0, "", ""

    monkeypatch.setattr(manager, "_run_command", _fake_run)
    monkeypatch.setattr(manager, "_linux_interface_exists", lambda name=None: interface_present["value"])

    manager._linux_bring_up(manager._listener_config_path())

    assert commands == [
        ["wg-quick", "up", str(manager._listener_config_path())],
        ["wg-quick", "down", str(manager._listener_config_path())],
        ["ip", "link", "delete", "dev", manager._interface_name],
        ["wg-quick", "up", str(manager._listener_config_path())],
    ]


@pytest.mark.skipif(os.name == "nt", reason="Linux peer mutation checks do not apply on Windows.")
def test_upsert_peer_uses_live_wg_set_when_interface_present(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(wireguard_server.engine_config, "PROJECT_ROOT", tmp_path)
    manager = WireGuardServerManager(_build_config(tmp_path))
    manager._wg = "wg"
    manager._ip = "ip"
    manager._wg_quick = "wg-quick"

    commands: list[list[str]] = []

    def _fake_run(args):
        commands.append(list(args))
        return 0, "", ""

    monkeypatch.setattr(manager, "_run_command", _fake_run)
    monkeypatch.setattr(manager, "_linux_interface_exists", lambda: True)
    monkeypatch.setattr(manager, "_ensure_linux_listener_rule", lambda: None)

    manager.upsert_peer(
        {
            "agent_id": "agent-1",
            "allowed_ips": ["10.255.0.2/32"],
            "public_key": "peer-public-key",
        }
    )

    assert ["wg-quick", "down", str(manager._listener_config_path())] not in commands
    assert ["wg-quick", "up", str(manager._listener_config_path())] not in commands
    assert ["wg", "set", manager._interface_name, "peer", "peer-public-key", "allowed-ips", "10.255.0.2/32"] in commands


@pytest.mark.skipif(os.name == "nt", reason="Linux route mutation checks do not apply on Windows.")
def test_ensure_linux_peer_route_replaces_peer_network_route(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(wireguard_server.engine_config, "PROJECT_ROOT", tmp_path)
    manager = WireGuardServerManager(_build_config(tmp_path))
    manager._ip = "ip"

    commands: list[list[str]] = []

    def _fake_run(args):
        commands.append(list(args))
        return 0, "", ""

    monkeypatch.setattr(manager, "_run_command", _fake_run)

    manager._ensure_linux_peer_route()

    assert commands == [["ip", "route", "replace", "10.255.0.0/24", "dev", manager._interface_name]]
