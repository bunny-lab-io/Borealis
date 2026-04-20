from __future__ import annotations

from Data.Engine.services.API.devices.session_inventory import normalize_device_sessions
from Data.Engine.services.API.devices.session_dispatch import build_currentuser_dispatch_fields


def test_normalize_device_sessions_preserves_helper_metadata() -> None:
    payload = normalize_device_sessions(
        {
            "reported_at": 1700,
            "sessions": [
                {
                    "session_id": 4,
                    "username": "alice",
                    "state_code": "active",
                    "helper_ready": True,
                    "helper_pid": 1201,
                    "helper_last_seen_at": 1699,
                }
            ],
        }
    )

    assert payload["reported_at"] == 1700
    assert payload["sessions"][0]["eligible_for_interactive"] is True
    assert payload["sessions"][0]["helper_ready"] is True
    assert payload["sessions"][0]["helper_pid"] == 1201
    assert payload["sessions"][0]["helper_last_seen_at"] == 1699


def test_build_currentuser_dispatch_fields_for_specific_session() -> None:
    payload = build_currentuser_dispatch_fields(
        run_mode="currentuser",
        session_target="specific_session",
        target_session_id="9",
    )

    assert payload == {
        "target_context": "currentuser",
        "session_target": "specific_session",
        "target_session_id": 9,
    }
