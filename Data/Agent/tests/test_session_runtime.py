from __future__ import annotations

from Data.Agent.session_runtime import (
    SESSION_TARGET_ALL,
    _PendingJob,
    SessionHelperBroker,
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
