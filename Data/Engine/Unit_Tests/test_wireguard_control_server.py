from __future__ import annotations

import importlib.util
from pathlib import Path

import pytest


def _load_control_server():
    path = Path(__file__).resolve().parents[1] / "Containers" / "wireguard-tunnel" / "control_server.py"
    spec = importlib.util.spec_from_file_location("wireguard_control_server_test_module", path)
    assert spec is not None
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


def test_wireguard_control_server_allows_only_expected_runtime_commands(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("BOREALIS_WIREGUARD_ENGINE_VIRTUAL_IP", "10.255.0.1/32")
    monkeypatch.setenv("BOREALIS_WIREGUARD_PEER_NETWORK", "10.255.0.0/16")
    module = _load_control_server()
    service_root = tmp_path / "Engine" / "Services" / "wireguard-tunnel"
    (service_root / "config").mkdir(parents=True)
    (service_root / "secrets").mkdir(parents=True)
    config_path = service_root / "config" / "borealis-wg.conf"
    key_path = service_root / "secrets" / "server_private.key"
    config_path.write_text("[Interface]\n", encoding="utf-8")
    key_path.write_text("private\n", encoding="utf-8")
    module.SERVICE_ROOT = service_root

    allowed = [
        ["wg", "show", "borealis-wg"],
        ["wg", "show", "borealis-wg", "peers"],
        ["wg", "show", "borealis-wg", "latest-handshakes"],
        ["wg", "set", "borealis-wg", "listen-port", "30000", "private-key", str(key_path)],
        ["wg", "set", "borealis-wg", "peer", "AbCdEfGhIjKlMnOpQrStUvWxYz1234567890+/ABC=", "allowed-ips", "10.255.0.2/32"],
        ["wg", "set", "borealis-wg", "peer", "AbCdEfGhIjKlMnOpQrStUvWxYz1234567890+/ABC=", "remove"],
        ["wg-quick", "up", str(config_path)],
        ["ip", "address", "replace", "10.255.0.1/32", "dev", "borealis-wg"],
        ["ip", "route", "replace", "10.255.0.0/16", "dev", "borealis-wg"],
        ["ip", "link", "set", "up", "dev", "borealis-wg"],
        ["ip", "link", "show", "dev", "borealis-wg"],
        ["iptables", "-A", "BOREALIS-WG-INPUT", "-s", "10.255.0.0/16", "-m", "comment", "--comment", "borealis deny agent host ingress", "-j", "DROP"],
        ["iptables", "-I", "INPUT", "1", "-i", "borealis-wg", "-j", "BOREALIS-WG-INPUT"],
    ]

    for command in allowed:
        module._validate_command(command)


@pytest.mark.parametrize(
    "command",
    [
        ["firewall-cmd", "--add-port=30000/udp"],
        ["iptables", "-A", "BOREALIS-WG-INPUT", "-j", "ACCEPT"],
        ["wg", "set", "borealis-wg", "peer", "AbCdEfGhIjKlMnOpQrStUvWxYz1234567890+/ABC=", "allowed-ips", "10.255.0.0/16"],
        ["wg", "set", "borealis-wg", "peer", "AbCdEfGhIjKlMnOpQrStUvWxYz1234567890+/ABC=", "allowed-ips", "10.255.0.1/32"],
        ["wg-quick", "up", "/tmp/attacker.conf"],
        ["ip", "route", "add", "0.0.0.0/0", "dev", "borealis-wg"],
        ["ip", "route", "replace", "0.0.0.0/0", "dev", "borealis-wg"],
        ["iptables", "-A", "BOREALIS-WG-INPUT", "-s", "0.0.0.0/0", "-m", "comment", "--comment", "borealis deny agent host ingress", "-j", "DROP"],
    ],
)
def test_wireguard_control_server_rejects_broad_privileged_commands(command: list[str], tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("BOREALIS_WIREGUARD_ENGINE_VIRTUAL_IP", "10.255.0.1/32")
    monkeypatch.setenv("BOREALIS_WIREGUARD_PEER_NETWORK", "10.255.0.0/16")
    module = _load_control_server()
    module.SERVICE_ROOT = tmp_path / "Engine" / "Services" / "wireguard-tunnel"

    with pytest.raises(ValueError):
        module._validate_command(command)
