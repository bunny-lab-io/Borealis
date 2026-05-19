from Data.Engine.services.API.devices.agent_role_health import merge_agent_role_health, normalize_agent_role_health


def test_merge_agent_role_health_replaces_all_contexts_present_in_incoming_payload() -> None:
    existing = {
        "reported_at": 100,
        "roles": [
            {
                "role_id": "currentuser:macro",
                "role_name": "macro",
                "role_label": "Macro Automation",
                "context": "currentuser",
                "status": "healthy",
                "last_checked_at": 100,
            },
            {
                "role_id": "currentuser:screenshot",
                "role_name": "screenshot",
                "role_label": "Screenshot Capture",
                "context": "currentuser",
                "status": "healthy",
                "last_checked_at": 100,
            },
            {
                "role_id": "system:script_exec_system",
                "role_name": "script_exec_system",
                "role_label": "Script Execution - SYSTEM",
                "context": "system",
                "status": "healthy",
                "last_checked_at": 100,
            },
        ],
    }
    incoming = {
        "reported_at": 200,
        "roles": [
            {
                "role_id": "system:script_exec_system",
                "role_name": "script_exec_system",
                "role_label": "Script Execution - SYSTEM",
                "context": "system",
                "status": "healthy",
                "last_checked_at": 200,
            },
            {
                "role_id": "currentuser:script_exec_currentuser",
                "role_name": "script_exec_currentuser",
                "role_label": "Script Execution - CURRENTUSER",
                "context": "currentuser",
                "status": "recovering",
                "last_checked_at": 200,
            },
        ],
    }

    merged = merge_agent_role_health(existing, incoming, incoming_context="system")
    role_ids = {str(item.get("role_id") or "") for item in merged.get("roles") or []}

    assert "system:context_system" in role_ids
    assert "currentuser:context_currentuser" in role_ids
    assert "currentuser:macros" not in role_ids
    assert "currentuser:node_screenshot" not in role_ids


def test_merge_agent_role_health_preserves_startup_timeline_across_system_heartbeat() -> None:
    existing = {
        "reported_at": 100,
        "roles": [
            {
                "role_id": "startup:system_heartbeat",
                "role_name": "system_heartbeat",
                "role_label": "Startup Timeline",
                "context": "startup",
                "status": "healthy",
                "last_checked_at": 100,
                "details": {
                    "milestones_json": '[{"key":"steady_state_online","state":"complete"}]',
                },
            },
            {
                "role_id": "system:context_system",
                "role_name": "context_system",
                "role_label": "System Context",
                "context": "system",
                "status": "healthy",
                "last_checked_at": 100,
            },
        ],
    }
    incoming = {
        "reported_at": 200,
        "roles": [
            {
                "role_id": "system:context_system",
                "role_name": "context_system",
                "role_label": "System Context",
                "context": "system",
                "status": "healthy",
                "last_checked_at": 200,
            },
            {
                "role_id": "currentuser:context_currentuser",
                "role_name": "context_currentuser",
                "role_label": "Current User Context",
                "context": "currentuser",
                "status": "healthy",
                "last_checked_at": 200,
            },
        ],
    }

    merged = merge_agent_role_health(existing, incoming, incoming_context="system")
    role_ids = {str(item.get("role_id") or "") for item in merged.get("roles") or []}

    assert "startup:system_heartbeat" in role_ids
    assert "system:context_system" in role_ids
    assert "currentuser:context_currentuser" in role_ids


def test_normalize_agent_role_health_preserves_supervisor_fields() -> None:
    normalized = normalize_agent_role_health(
        {
            "reported_at": 300,
            "supervisor_revision": 7,
            "roles": [
                {
                    "role_name": "vnc",
                    "role_label": "UltraVNC Service",
                    "context": "system",
                    "status_code": "recovering",
                    "desired_state": "running",
                    "observed_state": "recovering",
                    "last_success_at": 250,
                    "last_error": "listener warming",
                    "recovery_attempts": 2,
                    "details": {"runtime": "go", "desired_state": "running"},
                    "last_checked_at": 300,
                }
            ],
        }
    )

    assert normalized["supervisor_revision"] == 7
    role = normalized["roles"][0]
    assert role["desired_state"] == "running"
    assert role["observed_state"] == "recovering"
    assert role["last_success_at"] == 250
    assert role["last_error"] == "listener warming"
    assert role["recovery_attempts"] == 2
