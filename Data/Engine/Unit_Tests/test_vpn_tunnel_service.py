# ======================================================
# Data\Engine\Unit_Tests\test_vpn_tunnel_service.py
# Description: Focused WireGuard tunnel service regressions around default SSH exposure and per-session allowed-port expansion.
# ======================================================

from __future__ import annotations

import logging
import time
from types import SimpleNamespace
from typing import Any

from Data.Engine.services.VPN.vpn_tunnel_service import VpnTunnelService


class _DummySocketIO:
    def __init__(self) -> None:
        self.emits: list[tuple[str, Any, str]] = []

    def start_background_task(self, target, *args, **kwargs):
        return None

    def emit(self, event: str, payload: Any, namespace: str = "/") -> None:
        self.emits.append((event, payload, namespace))


class _FakeWireGuardManager:
    def __init__(self) -> None:
        self.server_public_key = "server-public-key"
        self.logger = logging.getLogger("borealis.test.vpn.service")
        self.current_peers: dict[str, dict[str, Any]] = {}
        self.apply_calls = 0
        self.reconcile_calls = 0
        self.removed_rules: list[list[str]] = []

    def require_orchestration_token(self, token: Any) -> Any:
        return token

    def build_peer_profile(self, agent_id: str, virtual_ip: str, allowed_ports: Any = None) -> dict[str, Any]:
        return {
            "agent_id": agent_id,
            "virtual_ip": virtual_ip,
            "allowed_ips": [virtual_ip],
            "allowed_ports": tuple(allowed_ports or ()),
        }

    def apply_firewall_rules(self, peer: Any) -> list[str]:
        self.apply_calls += 1
        return [f"rule-{peer.get('agent_id', 'agent')}"]

    def remove_firewall_rules(self, rule_names: Any) -> None:
        self.removed_rules.append(list(rule_names))

    def reconcile_peers(self, peers: Any) -> None:
        self.reconcile_calls += 1
        self.current_peers = {
            str(peer.get("agent_id") or ""): dict(peer)
            for peer in (peers or [])
            if str(peer.get("agent_id") or "").strip()
        }

    def upsert_peer(self, peer: Any) -> None:
        self.current_peers[str(peer.get("agent_id") or "")] = dict(peer)

    def remove_peer(self, _peer: Any) -> None:
        return None

    def ensure_listener(self) -> None:
        return None

    def cleanup_listener(self) -> list[str]:
        return []

    def listener_status(self) -> dict[str, Any]:
        return {
            "healthy": True,
            "reason": "listener_running",
            "service_state": "RUNNING",
            "peer_count": len(self.current_peers),
        }

    def check_listener_health(self) -> dict[str, Any]:
        return self.listener_status()

    def check_peer_health(self, _public_key: str) -> dict[str, Any]:
        return {
            "healthy": True,
            "reason": "listener_running",
            "service_state": "RUNNING",
            "peer_present": True,
            "last_handshake_at": time.time(),
            "last_handshake_at_iso": "2026-04-27T04:20:00+00:00",
            "handshake_age_seconds": 0,
        }


def _build_service() -> tuple[VpnTunnelService, _FakeWireGuardManager, _DummySocketIO]:
    socketio = _DummySocketIO()
    wg = _FakeWireGuardManager()
    context = SimpleNamespace(
        logger=logging.getLogger("borealis.test.vpn.service"),
        wireguard_port=30000,
        wireguard_engine_virtual_ip="10.255.0.1/24",
        wireguard_peer_network="10.255.0.0/24",
        wireguard_port_allowlist=(47002, 5900, 22),
    )
    service = VpnTunnelService(
        context=context,
        wireguard_manager=wg,
        db_conn_factory=None,
        socketio=socketio,
        service_log=lambda *_args, **_kwargs: None,
        signer=None,
    )
    return service, wg, socketio


def test_request_agent_start_expands_allowed_ports_for_nondefault_ansible_transport() -> None:
    service, wg, socketio = _build_service()
    initial = service.connect(agent_id="agent-1", operator_id=None, endpoint_host="engine.local")

    payload = service.request_agent_start(
        "agent-1",
        reason="shared_ansible_prepare",
        required_ports=[2222],
    )

    assert initial["allowed_ports"] == [47002, 5900, 22]
    assert payload is not None
    assert payload["allowed_ports"] == [47002, 5900, 22, 2222]
    assert wg.apply_calls == 2
    assert wg.removed_rules == [["rule-agent-1"]]
    assert socketio.emits[-1][0] == "vpn_tunnel_start"
    assert socketio.emits[-1][1]["allowed_ports"] == [47002, 5900, 22, 2222]


def test_wait_for_sessions_ready_requires_agent_ready_callback() -> None:
    service, _wg, _socketio = _build_service()
    session = service.connect(agent_id="agent-1", operator_id=None, endpoint_host="engine.local")

    before_ready = service.wait_for_sessions_ready(
        ["agent-1"],
        required_ports=[22],
        timeout_seconds=0,
    )

    assert before_ready["agent-1"]["dispatch_ready"] is False
    assert before_ready["agent-1"]["dispatch_ready_reason"] == "agent_ready_missing"

    ready_payload = service.record_agent_ready(
        "agent-1",
        tunnel_id=str(session["tunnel_id"]),
        allowed_ports=[22],
        reason="unit_test",
        service_state="RUNNING",
        virtual_ip=str(session["virtual_ip"]),
    )
    after_ready = service.wait_for_sessions_ready(
        ["agent-1"],
        required_ports=[22],
        timeout_seconds=0,
    )

    assert ready_payload is not None
    assert ready_payload["dispatch_ready"] is True
    assert after_ready["agent-1"]["dispatch_ready"] is True
    assert after_ready["agent-1"]["agent_ready"] is True


def test_dispatch_ready_rejects_probe_grace_without_transport_confirmation() -> None:
    service, wg, _socketio = _build_service()
    session = service.connect(agent_id="agent-1", operator_id=None, endpoint_host="engine.local")
    wg.check_peer_health = lambda _public_key: {
        "healthy": True,
        "reason": "listener_running",
        "service_state": "RUNNING",
        "peer_present": True,
        "last_handshake_at": None,
        "last_handshake_at_iso": "",
        "handshake_age_seconds": None,
    }
    service.record_agent_ready(
        "agent-1",
        tunnel_id=str(session["tunnel_id"]),
        allowed_ports=[22],
        reason="unit_test",
        service_state="RUNNING",
        virtual_ip=str(session["virtual_ip"]),
    )

    after_probe = service.wait_for_sessions_ready(
        ["agent-1"],
        required_ports=[22],
        timeout_seconds=0,
    )

    assert after_probe["agent-1"]["dispatch_ready"] is False
    assert after_probe["agent-1"]["dispatch_ready_reason"] == "transport_probe_pending"
