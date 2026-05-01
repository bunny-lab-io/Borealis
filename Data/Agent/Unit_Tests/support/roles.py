"""Role runtime helpers for Agent unit tests."""

from __future__ import annotations

from typing import Any


def install_role_status_hooks(role: Any, events: list | None = None) -> list:
    """Attach in-memory agent status hooks to Role instances built with __new__."""
    if events is None:
        events = []
    role._agent_status_record = lambda key, state, detail: events.append(("record", key, state, detail))
    role._agent_status_complete = lambda key, detail: events.append(("complete", key, detail))
    role._agent_status_failed = lambda key, detail: events.append(("failed", key, detail))
    role._agent_status_flush = lambda *, reason="": events.append(("flush", reason))
    return events
