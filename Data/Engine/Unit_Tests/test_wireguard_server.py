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
        acl_allowlist_ports=(47002, 5900),
        log_path=logs_dir / "wireguard.log",
    )


@pytest.mark.skipif(os.name == "nt", reason="Linux permission checks do not apply on Windows.")
def test_start_listener_secures_linux_runtime_files(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(wireguard_server.engine_config, "PROJECT_ROOT", tmp_path)
    manager = WireGuardServerManager(_build_config(tmp_path))
    monkeypatch.setattr(manager, "_linux_bring_down", lambda _config_path: None)
    monkeypatch.setattr(manager, "_linux_bring_up", lambda _config_path: None)
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
