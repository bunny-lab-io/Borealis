from __future__ import annotations

from typing import Any, Dict


SESSION_TARGET_ALL = "all_active_sessions"
SESSION_TARGET_SPECIFIC = "specific_session"


def _clean_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def _coerce_int(value: Any, default: int = 0) -> int:
    try:
        if value in (None, ""):
            raise ValueError
        return int(float(value))
    except Exception:
        return default


def normalize_session_target(value: Any, *, default: str = SESSION_TARGET_ALL) -> str:
    text = _clean_text(value).lower().replace("-", "_")
    if text in {"specific", "specific_session", "single", "session"}:
        return SESSION_TARGET_SPECIFIC
    if text in {"all", "all_active_sessions", "all_sessions", "fanout"}:
        return SESSION_TARGET_ALL
    return SESSION_TARGET_SPECIFIC if default == SESSION_TARGET_SPECIFIC else SESSION_TARGET_ALL


def normalize_target_session_id(value: Any) -> int:
    parsed = _coerce_int(value, 0)
    return parsed if parsed > 0 else 0


def build_currentuser_dispatch_fields(
    *,
    run_mode: Any,
    session_target: Any = None,
    target_session_id: Any = None,
    default_session_target: str = SESSION_TARGET_ALL,
) -> Dict[str, Any]:
    normalized_run_mode = _clean_text(run_mode).lower() or "system"
    payload = {"target_context": normalized_run_mode}
    if normalized_run_mode != "currentuser":
        return payload
    normalized_target = normalize_session_target(session_target, default=default_session_target)
    payload["session_target"] = normalized_target
    session_id = normalize_target_session_id(target_session_id)
    if normalized_target == SESSION_TARGET_SPECIFIC and session_id > 0:
        payload["target_session_id"] = session_id
    return payload
