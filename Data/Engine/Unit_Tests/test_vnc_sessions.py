# ======================================================
# Data\Engine\Unit_Tests\test_vnc_sessions.py
# Description: Validates collaboration-aware VNC session lifecycle behavior.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

import logging

from Data.Engine.services.RemoteDesktop.vnc_sessions import VncCollaborationManager


def _build_manager() -> VncCollaborationManager:
    return VncCollaborationManager(logger=logging.getLogger("test.vnc.sessions"))


def test_collaboration_session_tracks_controller_spectators_and_handoff() -> None:
    manager = _build_manager()

    session, alice, created = manager.ensure_session(
        agent_id="agent-1",
        operator_id="alice",
        remove_wallpaper=True,
    )
    assert created is True
    assert alice.role == "controller"

    _, bob, created = manager.ensure_session(
        agent_id="agent-1",
        operator_id="bob",
        remove_wallpaper=True,
    )
    assert created is False
    assert bob.role == "spectator"

    manager.record_backend_ready(
        session.session_id,
        tunnel_id="tun-1",
        allowed_ips="10.255.0.1/32",
        engine_virtual_ip="10.255.0.1/32",
    )
    snapshot = manager.session_snapshot(session, current_operator_id="alice")
    assert snapshot["controller_operator_id"] == "alice"
    assert snapshot["participant_count"] == 2
    assert snapshot["can_handoff"] is True
    assert snapshot["allowed_ips"] == "10.255.0.1/32"
    assert snapshot["engine_virtual_ip"] == "10.255.0.1/32"

    handoff_result = manager.handoff(
        session_id=session.session_id,
        actor_operator_id="alice",
        target_operator_id="bob",
    )
    handed_session = handoff_result["session"]
    handed_snapshot = manager.session_snapshot(handed_session, current_operator_id="bob")
    assert handed_snapshot["controller_operator_id"] == "bob"
    assert handed_snapshot["current_operator_role"] == "controller"
    assert handed_snapshot["credential_revision"] == 2


def test_controller_leave_marks_session_vacant_and_remaining_spectator_can_claim_control() -> None:
    manager = _build_manager()

    session, _, _ = manager.ensure_session(
        agent_id="agent-2",
        operator_id="alice",
        remove_wallpaper=True,
    )
    manager.ensure_session(
        agent_id="agent-2",
        operator_id="bob",
        remove_wallpaper=True,
    )

    leave_result = manager.leave_or_close(
        session_id=session.session_id,
        operator_id="alice",
        close_session=False,
    )
    assert leave_result["closed"] is False
    assert leave_result["controller_vacant"] is True

    vacant_session = manager.get_session_by_id(session.session_id)
    assert vacant_session is not None
    snapshot = manager.session_snapshot(vacant_session, current_operator_id="bob")
    assert snapshot["controller_vacant"] is True
    assert snapshot["can_claim_control"] is True

    claim_result = manager.handoff(
        session_id=session.session_id,
        actor_operator_id="bob",
        target_operator_id=None,
    )
    claimed_session = claim_result["session"]
    claimed_snapshot = manager.session_snapshot(claimed_session, current_operator_id="bob")
    assert claimed_snapshot["controller_operator_id"] == "bob"
    assert claimed_snapshot["current_operator_role"] == "controller"


def test_last_controller_disconnect_keeps_session_warm_for_reconnect() -> None:
    manager = _build_manager()

    session, alice, _ = manager.ensure_session(
        agent_id="agent-keepalive",
        operator_id="alice",
        remove_wallpaper=True,
    )
    original_password = session.controller_password
    original_revision = session.credential_revision

    leave_result = manager.leave_or_close(
        session_id=session.session_id,
        operator_id="alice",
        close_session=False,
    )
    assert leave_result["closed"] is False
    assert leave_result["controller_vacant"] is False
    assert leave_result["reconnect_pending"] is True

    retained_session = manager.get_session_by_id(session.session_id)
    assert retained_session is not None
    retained_snapshot = manager.session_snapshot(retained_session, current_operator_id="alice")
    assert retained_snapshot["state"] == "reconnect_pending"
    assert retained_snapshot["reconnect_pending"] is True
    assert retained_snapshot["controller_operator_id"] == "alice"
    assert retained_snapshot["participant_count"] == 1
    assert retained_session.controller_password == original_password
    assert retained_session.credential_revision == original_revision

    rejoined_session, rejoined_participant, created = manager.ensure_session(
        agent_id="agent-keepalive",
        operator_id="alice",
        remove_wallpaper=True,
    )
    assert created is False
    assert rejoined_session.session_id == session.session_id
    assert rejoined_participant.participant_id == alice.participant_id
    assert rejoined_participant.role == "controller"
    assert rejoined_session.state == "active"
    assert rejoined_session.controller_password == original_password
    assert rejoined_session.credential_revision == original_revision


def test_close_session_and_revoke_agent_remove_active_collaboration_session() -> None:
    manager = _build_manager()

    session, _, _ = manager.ensure_session(
        agent_id="agent-3",
        operator_id="alice",
        remove_wallpaper=False,
    )
    manager.ensure_session(
        agent_id="agent-3",
        operator_id="bob",
        remove_wallpaper=False,
    )

    close_result = manager.close_session(session_id=session.session_id, reason="admin_close")
    assert close_result["session"].last_error == "admin_close"
    assert manager.get_session_by_id(session.session_id) is None

    session_two, _, _ = manager.ensure_session(
        agent_id="agent-4",
        operator_id="carol",
        remove_wallpaper=True,
    )
    revoke_result = manager.revoke_agent("agent-4", reason="device_purged")
    assert revoke_result is not None
    assert revoke_result["session"].session_id == session_two.session_id
    assert manager.get_session_for_agent("agent-4") is None
