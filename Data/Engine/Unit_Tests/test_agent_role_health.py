from Data.Engine.services.API.devices.agent_role_health import merge_agent_role_health


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
