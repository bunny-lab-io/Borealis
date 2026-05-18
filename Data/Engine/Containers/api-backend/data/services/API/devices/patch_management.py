# ======================================================
# Data\Engine\services\API\devices\patch_management.py
# Description: Windows patch-management policy, catalog, device state, and operator action endpoints.
#
# API Endpoints (if applicable):
# - POST /api/agent/patch-management/policy (Device Authenticated) - Return effective Windows patch policy for an agent.
# - POST /api/agent/patch-management/report (Device Authenticated) - Ingest Windows patch scan/install state from an agent.
# - GET /api/patch-management/catalog (Token Authenticated) - Return RBAC-scoped fleet patch catalog summary.
# - GET/POST /api/patch-management/policies (Token Authenticated) - List or create patch policies.
# - POST /api/patch-management/catalog/hold (Token Authenticated) - Hold a patch globally or for a policy.
# - POST /api/patch-management/catalog/release (Token Authenticated) - Release matching active patch holds.
# - GET /api/patch-management/devices (Token Authenticated) - Return RBAC-scoped device patch compliance rows.
# - GET /api/patch-management/history (Token Authenticated) - Return recent patch action history.
# - GET /api/device/patches/<hostname> (Token Authenticated) - Return patch state for one device.
# - POST /api/device/patches/<hostname>/action (Token Authenticated) - Dispatch scan/install/reboot/defer action to agent.
# ======================================================

"""Windows patch-management APIs for Borealis."""
from __future__ import annotations

import json
import time
import uuid
from typing import Any, Dict, Iterable, List, Optional, Tuple

from flask import Blueprint, g, jsonify, request

from ....auth.device_auth import require_device_auth
from ...auth import UserSiteAccessManager
from .software_uninstall import normalize_text
from .tunnel import _current_user, _require_login, _resolve_requested_agent_id

if False:  # pragma: no cover - type checker hint
    from .. import EngineServiceAdapters


PATCH_CLASS_DEFAULTS = {
    "security": True,
    "critical": True,
    "cumulative": True,
    "definition": True,
    "driver": True,
    "feature": True,
    "optional": True,
    "service_pack": True,
    "update_rollup": True,
    "updates": True,
}

DEFAULT_REBOOT_POLICY = {
    "mode": "maintenance_window",
    "maintenance_window_start": "22:00",
    "maintenance_window_end": "05:00",
    "deferral_deadline_hours": 72,
    "user_prompt": True,
}


def _now_ts() -> int:
    return int(time.time())


def _json_loads(value: Any, default: Any) -> Any:
    if isinstance(value, (dict, list)):
        return value
    if value in (None, ""):
        return json.loads(json.dumps(default))
    try:
        parsed = json.loads(str(value))
    except Exception:
        return json.loads(json.dumps(default))
    if isinstance(default, dict) and isinstance(parsed, dict):
        return parsed
    if isinstance(default, list) and isinstance(parsed, list):
        return parsed
    return json.loads(json.dumps(default))


def _json_dumps(value: Any) -> str:
    return json.dumps(value if value is not None else {}, sort_keys=True, separators=(",", ":"))


def _bool(value: Any, default: bool = False) -> bool:
    if isinstance(value, bool):
        return value
    if isinstance(value, (int, float)):
        return bool(value)
    text = str(value or "").strip().lower()
    if text in {"1", "true", "yes", "on"}:
        return True
    if text in {"0", "false", "no", "off"}:
        return False
    return default


def _int(value: Any, default: int = 0) -> int:
    try:
        return int(float(value))
    except Exception:
        return default


def _list_text(value: Any) -> List[str]:
    if isinstance(value, str):
        raw = [part for part in value.split(",")]
    elif isinstance(value, Iterable) and not isinstance(value, (dict, bytes, bytearray)):
        raw = list(value)
    else:
        raw = []
    out: List[str] = []
    seen = set()
    for item in raw:
        text = normalize_text(item)
        key = text.lower()
        if text and key not in seen:
            seen.add(key)
            out.append(text)
    return out


def _normalize_class_toggles(value: Any) -> Dict[str, bool]:
    raw = _json_loads(value, PATCH_CLASS_DEFAULTS)
    out = dict(PATCH_CLASS_DEFAULTS)
    if isinstance(raw, dict):
        for key in out:
            if key in raw:
                out[key] = _bool(raw.get(key), out[key])
    return out


def _normalize_reboot_policy(value: Any) -> Dict[str, Any]:
    raw = _json_loads(value, DEFAULT_REBOOT_POLICY)
    out = dict(DEFAULT_REBOOT_POLICY)
    if isinstance(raw, dict):
        out.update({key: raw.get(key) for key in out if raw.get(key) not in (None, "")})
    out["user_prompt"] = _bool(out.get("user_prompt"), True)
    out["deferral_deadline_hours"] = max(0, _int(out.get("deferral_deadline_hours"), 72))
    return out


def _policy_payload(row: Any, *, effective_reason: str = "", holds: Optional[List[Dict[str, Any]]] = None) -> Dict[str, Any]:
    if not row:
        return {
            "policy_id": "default",
            "policy_name": "Borealis Default",
            "version": "1",
            "enabled": True,
            "class_toggles": dict(PATCH_CLASS_DEFAULTS),
            "reboot": dict(DEFAULT_REBOOT_POLICY),
            "holds": holds or [],
            "effective_reason": effective_reason or "fallback_default",
        }
    return {
        "policy_id": str(row[0]),
        "policy_name": normalize_text(row[1]) or "Borealis Default",
        "version": str(row[7] or row[6] or row[0]),
        "enabled": bool(row[3]),
        "class_toggles": _normalize_class_toggles(row[4]),
        "reboot": _normalize_reboot_policy(row[5]),
        "holds": holds or [],
        "effective_reason": effective_reason,
    }


def _load_device_by_guid(conn, guid: str) -> Optional[Dict[str, Any]]:
    cur = conn.cursor()
    cur.execute(
        """
        SELECT d.guid, d.hostname, d.agent_id, d.operating_system, ds.site_id
          FROM devices AS d
     LEFT JOIN device_sites AS ds
            ON ds.device_hostname = d.hostname
         WHERE UPPER(d.guid) = UPPER(?)
         LIMIT 1
        """,
        (guid,),
    )
    row = cur.fetchone()
    if not row:
        return None
    return {
        "guid": normalize_text(row[0]),
        "hostname": normalize_text(row[1]),
        "agent_id": normalize_text(row[2]),
        "operating_system": normalize_text(row[3]),
        "site_id": row[4],
    }


def _active_holds(conn, policy_id: Optional[int] = None) -> List[Dict[str, Any]]:
    params: List[Any] = []
    policy_clause = ""
    if policy_id:
        policy_clause = " AND (scope = 'global' OR (scope = 'policy' AND policy_id = ?))"
        params.append(policy_id)
    cur = conn.cursor()
    cur.execute(
        f"""
        SELECT scope, policy_id, update_id, revision_number, kb, title, reason
          FROM patch_holds
         WHERE released_at IS NULL
               {policy_clause}
      ORDER BY created_at DESC
        """,
        params,
    )
    holds = []
    for row in cur.fetchall():
        holds.append(
            {
                "scope": normalize_text(row[0]),
                "policy_id": str(row[1] or ""),
                "update_id": normalize_text(row[2]),
                "revision_number": row[3],
                "kb": normalize_text(row[4]),
                "title": normalize_text(row[5]),
                "reason": normalize_text(row[6]),
            }
        )
    return holds


def _resolve_effective_policy(conn, device: Optional[Dict[str, Any]]) -> Dict[str, Any]:
    cur = conn.cursor()
    candidates: List[Tuple[str, Tuple[Any, ...]]] = []
    if device and device.get("guid"):
        candidates.append(("device", (device.get("guid"),)))
    if device and device.get("site_id") is not None:
        candidates.append(("site", (device.get("site_id"),)))
    candidates.append(("global", ()))
    for scope, params in candidates:
        if scope == "device":
            cur.execute(
                """
                SELECT p.id, p.name, p.description, p.enabled, p.class_toggles_json, p.reboot_policy_json, p.created_at, p.updated_at
                  FROM patch_policy_bindings AS b
                  JOIN patch_policies AS p ON p.id = b.policy_id
                 WHERE b.scope_type = 'device' AND UPPER(COALESCE(b.device_guid, '')) = UPPER(?)
              ORDER BY b.updated_at DESC
                 LIMIT 1
                """,
                params,
            )
        elif scope == "site":
            cur.execute(
                """
                SELECT p.id, p.name, p.description, p.enabled, p.class_toggles_json, p.reboot_policy_json, p.created_at, p.updated_at
                  FROM patch_policy_bindings AS b
                  JOIN patch_policies AS p ON p.id = b.policy_id
                 WHERE b.scope_type = 'site' AND b.site_id = ?
              ORDER BY b.updated_at DESC
                 LIMIT 1
                """,
                params,
            )
        else:
            cur.execute(
                """
                SELECT p.id, p.name, p.description, p.enabled, p.class_toggles_json, p.reboot_policy_json, p.created_at, p.updated_at
                  FROM patch_policy_bindings AS b
                  JOIN patch_policies AS p ON p.id = b.policy_id
                 WHERE b.scope_type = 'global'
              ORDER BY b.updated_at DESC
                 LIMIT 1
                """
            )
        row = cur.fetchone()
        if row:
            policy_id = _int(row[0])
            return _policy_payload(row, effective_reason=scope, holds=_active_holds(conn, policy_id))
    return _policy_payload(None, holds=_active_holds(conn))


def _site_clause_for_user(site_access: UserSiteAccessManager, user: Dict[str, Any], alias: str = "ds") -> Tuple[str, List[Any]]:
    allowed_site_ids = site_access.site_ids_for_user(user)
    if allowed_site_ids is None:
        return "", []
    if not allowed_site_ids:
        return " AND 1 = 0", []
    ordered = sorted(allowed_site_ids)
    return f" AND {alias}.site_id IN ({','.join('?' for _ in ordered)})", ordered


def _update_key(update: Dict[str, Any]) -> Tuple[str, int]:
    return normalize_text(update.get("update_id")).lower(), _int(update.get("revision_number") or update.get("revision"))


def _is_pending_install(update: Dict[str, Any]) -> bool:
    return _bool(update.get("is_downloaded") or update.get("downloaded")) and not _bool(
        update.get("is_installed") or update.get("installed")
    )


def _state_from_update(update: Dict[str, Any]) -> str:
    if _bool(update.get("is_installed") or update.get("installed")):
        return "installed"
    if _bool(update.get("held")):
        return "held"
    if _bool(update.get("is_hidden") or update.get("hidden")):
        return "hidden"
    result = normalize_text(update.get("result_code")).lower()
    if result in {"failed", "install_failed", "download_failed"}:
        return "failed"
    if _is_pending_install(update):
        return "pending_install"
    if _bool(update.get("approved")):
        return "missing"
    return "available"


def register_patch_management(app, adapters: "EngineServiceAdapters") -> None:
    blueprint = Blueprint("patch_management", __name__)
    logger = adapters.context.logger.getChild("patch_management.api")
    site_access = UserSiteAccessManager(adapters.db_conn_factory, logger=logger)
    auth_manager = adapters.device_auth_manager

    def _notify(change: str, payload: Optional[Dict[str, Any]] = None) -> None:
        socketio = getattr(adapters.context, "socketio", None)
        if socketio is None:
            return
        try:
            socketio.emit("patch_management_changed", {"change": change, **(payload or {})})
        except Exception:
            logger.debug("patch_management_changed emit failed", exc_info=True)

    def _load_device_record(hostname: str) -> Optional[Dict[str, Any]]:
        conn = adapters.db_conn_factory()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT d.guid, d.hostname, d.agent_id, d.operating_system, ds.site_id
                  FROM devices AS d
             LEFT JOIN device_sites AS ds
                    ON ds.device_hostname = d.hostname
                 WHERE LOWER(d.hostname) = LOWER(?)
              ORDER BY d.last_seen DESC
                 LIMIT 1
                """,
                (hostname,),
            )
            row = cur.fetchone()
            if not row:
                return None
            return {
                "guid": normalize_text(row[0]),
                "hostname": normalize_text(row[1]),
                "agent_id": normalize_text(row[2]),
                "operating_system": normalize_text(row[3]),
                "site_id": row[4],
            }
        finally:
            conn.close()

    def _dispatch_patch_request(record: Dict[str, Any], action: str, body: Dict[str, Any], operator_id: str) -> bool:
        agent_id = _resolve_requested_agent_id(adapters, record.get("agent_id"))
        payload = {
            "request_id": str(uuid.uuid4()),
            "hostname": record.get("hostname"),
            "target_hostname": record.get("hostname"),
            "agent_id": agent_id,
            "action": action,
            "requested_by": operator_id,
            "requested_at": _now_ts(),
            "update_ids": _list_text(body.get("update_ids")),
            "kb_article_ids": _list_text(body.get("kb_article_ids") or body.get("kbs")),
            "delay_seconds": _int(body.get("delay_seconds"), 60),
        }
        emitted = False
        emitter = getattr(adapters.context, "emit_host_service_event", None)
        if callable(emitter):
            try:
                emitted = bool(emitter(record.get("hostname"), "system", "patch_management_request", payload))
            except Exception:
                emitted = False
        if not emitted:
            agent_emitter = getattr(adapters.context, "emit_agent_event", None)
            if callable(agent_emitter) and agent_id:
                try:
                    emitted = bool(agent_emitter(agent_id, "patch_management_request", payload))
                except Exception:
                    emitted = False
        return emitted

    @blueprint.route("/api/agent/patch-management/policy", methods=["POST"])
    @require_device_auth(auth_manager)
    def agent_patch_policy():
        ctx = getattr(g, "device_auth", None)
        if ctx is None:
            return jsonify({"error": "auth_context_missing"}), 500
        conn = adapters.db_conn_factory()
        try:
            device = _load_device_by_guid(conn, ctx.guid)
            policy = _resolve_effective_policy(conn, device)
            return jsonify(policy), 200
        finally:
            conn.close()

    @blueprint.route("/api/agent/patch-management/report", methods=["POST"])
    @require_device_auth(auth_manager)
    def agent_patch_report():
        ctx = getattr(g, "device_auth", None)
        if ctx is None:
            return jsonify({"error": "auth_context_missing"}), 500
        payload = request.get_json(silent=True) or {}
        now = _now_ts()
        updates = payload.get("updates") if isinstance(payload.get("updates"), list) else []
        install = payload.get("install") if isinstance(payload.get("install"), dict) else {}
        install_results = install.get("results") if isinstance(install.get("results"), list) else []
        result_by_key = {_update_key(item): item for item in install_results if isinstance(item, dict)}
        conn = adapters.db_conn_factory()
        try:
            cur = conn.cursor()
            device = _load_device_by_guid(conn, ctx.guid)
            hostname = normalize_text(payload.get("hostname")) or (device or {}).get("hostname") or ""
            policy_id = normalize_text(payload.get("policy_id"))
            policy_version = normalize_text(payload.get("policy_version"))
            for item in updates:
                if not isinstance(item, dict):
                    continue
                update_id = normalize_text(item.get("update_id"))
                if not update_id:
                    continue
                revision = _int(item.get("revision_number") or item.get("revision"))
                overlay = result_by_key.get((update_id.lower(), revision), {})
                merged = {**item, **(overlay if isinstance(overlay, dict) else {})}
                kb_articles = _list_text(merged.get("kb_article_ids"))
                classifications = _list_text(merged.get("classifications"))
                categories = _list_text(merged.get("categories"))
                cur.execute(
                    """
                    INSERT INTO patch_catalog(update_id, revision_number, title, kb_articles_json, classifications_json, categories_json, msrc_severity, update_type, size_bytes, support_url, first_seen_at, last_seen_at)
                    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                    ON CONFLICT(update_id, revision_number) DO UPDATE SET
                        title=excluded.title,
                        kb_articles_json=excluded.kb_articles_json,
                        classifications_json=excluded.classifications_json,
                        categories_json=excluded.categories_json,
                        msrc_severity=excluded.msrc_severity,
                        update_type=excluded.update_type,
                        size_bytes=excluded.size_bytes,
                        support_url=excluded.support_url,
                        last_seen_at=excluded.last_seen_at
                    """,
                    (
                        update_id,
                        revision,
                        normalize_text(merged.get("title")) or update_id,
                        _json_dumps(kb_articles),
                        _json_dumps(classifications),
                        _json_dumps(categories),
                        normalize_text(merged.get("msrc_severity")),
                        normalize_text(merged.get("update_type")),
                        _int(merged.get("size_bytes")),
                        normalize_text(merged.get("support_url")),
                        now,
                        now,
                    ),
                )
                state = _state_from_update(merged)
                installed = _bool(merged.get("is_installed") or merged.get("installed")) or normalize_text(merged.get("result_code")).lower() == "success"
                if installed:
                    state = "installed"
                cur.execute(
                    """
                    INSERT INTO device_patch_state(device_guid, hostname, update_id, revision_number, status, approved, held, hold_reason, installed, downloaded, hidden, reboot_required, result_code, hresult, policy_id, policy_version, last_seen_at, installed_at, metadata_json)
                    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                    ON CONFLICT(device_guid, update_id, revision_number) DO UPDATE SET
                        hostname=excluded.hostname,
                        status=excluded.status,
                        approved=excluded.approved,
                        held=excluded.held,
                        hold_reason=excluded.hold_reason,
                        installed=excluded.installed,
                        downloaded=excluded.downloaded,
                        hidden=excluded.hidden,
                        reboot_required=excluded.reboot_required,
                        result_code=excluded.result_code,
                        hresult=excluded.hresult,
                        policy_id=excluded.policy_id,
                        policy_version=excluded.policy_version,
                        last_seen_at=excluded.last_seen_at,
                        installed_at=COALESCE(excluded.installed_at, device_patch_state.installed_at),
                        metadata_json=excluded.metadata_json
                    """,
                    (
                        ctx.guid,
                        hostname,
                        update_id,
                        revision,
                        state,
                        1 if _bool(merged.get("approved")) else 0,
                        1 if _bool(merged.get("held")) else 0,
                        normalize_text(merged.get("hold_reason")),
                        1 if installed else 0,
                        1 if _bool(merged.get("is_downloaded") or merged.get("downloaded")) else 0,
                        1 if _bool(merged.get("is_hidden") or merged.get("hidden")) else 0,
                        1 if _bool(merged.get("requires_reboot") or merged.get("reboot_required")) else 0,
                        normalize_text(merged.get("result_code")),
                        normalize_text(merged.get("hresult")),
                        policy_id,
                        policy_version,
                        now,
                        now if installed else None,
                        _json_dumps(merged),
                    ),
                )
            if install or payload.get("error"):
                cur.execute(
                    """
                    INSERT INTO patch_action_history(device_guid, hostname, action, status, requested_by, requested_at, started_at, finished_at, detail, metadata_json)
                    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                    """,
                    (
                        ctx.guid,
                        hostname,
                        "install" if install else "scan",
                        "failed" if payload.get("error") else normalize_text(install.get("result_code")) or "reported",
                        "agent",
                        now,
                        _int(install.get("started_at") or payload.get("scan_started_at")),
                        _int(install.get("finished_at") or payload.get("scan_completed_at")),
                        normalize_text(payload.get("error")),
                        _json_dumps(payload),
                    ),
                )
            conn.commit()
        except Exception as exc:
            try:
                conn.rollback()
            except Exception:
                pass
            logger.debug("patch report ingest failed", exc_info=True)
            return jsonify({"error": "persist_failed", "message": str(exc)}), 500
        finally:
            conn.close()
        _notify("report", {"hostname": normalize_text(payload.get("hostname"))})
        return jsonify({"status": "ok", "count": len(updates)}), 200

    @blueprint.route("/api/patch-management/catalog", methods=["GET"])
    def patch_catalog():
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        user = _current_user(app) or {}
        site_clause, params = _site_clause_for_user(site_access, user)
        conn = adapters.db_conn_factory()
        try:
            cur = conn.cursor()
            cur.execute(
                f"""
                SELECT c.update_id, c.revision_number, c.title, c.kb_articles_json, c.classifications_json, c.categories_json,
                       c.msrc_severity, c.update_type, c.size_bytes, c.support_url, c.first_seen_at, c.last_seen_at,
                       COUNT(DISTINCT s.device_guid) AS affected_devices,
                       SUM(CASE WHEN s.status IN ('missing', 'pending_install') THEN 1 ELSE 0 END) AS missing_count,
                       SUM(CASE WHEN s.status = 'installed' THEN 1 ELSE 0 END) AS installed_count,
                       SUM(CASE WHEN s.status = 'failed' THEN 1 ELSE 0 END) AS failed_count,
                       SUM(CASE WHEN s.reboot_required = 1 THEN 1 ELSE 0 END) AS pending_reboot_count,
                       MAX(s.last_seen_at) AS last_state_at
                  FROM patch_catalog AS c
             LEFT JOIN device_patch_state AS s
                    ON s.update_id = c.update_id AND s.revision_number = c.revision_number
             LEFT JOIN devices AS d
                    ON d.guid = s.device_guid
             LEFT JOIN device_sites AS ds
                    ON ds.device_hostname = d.hostname
                 WHERE 1=1 {site_clause}
              GROUP BY c.update_id, c.revision_number
              ORDER BY affected_devices DESC, c.last_seen_at DESC
                """,
                params,
            )
            rows = cur.fetchall()
            holds = _active_holds(conn)
        finally:
            conn.close()
        hold_keys = {(h.get("update_id", "").lower(), _int(h.get("revision_number"))) for h in holds if h.get("update_id")}
        data = []
        for row in rows:
            held = (normalize_text(row[0]).lower(), _int(row[1])) in hold_keys
            data.append(
                {
                    "update_id": row[0],
                    "revision_number": row[1],
                    "title": row[2],
                    "kb_article_ids": _json_loads(row[3], []),
                    "classifications": _json_loads(row[4], []),
                    "categories": _json_loads(row[5], []),
                    "msrc_severity": row[6] or "",
                    "update_type": row[7] or "",
                    "size_bytes": row[8] or 0,
                    "support_url": row[9] or "",
                    "first_seen_at": row[10] or 0,
                    "last_seen_at": row[11] or 0,
                    "affected_devices": row[12] or 0,
                    "missing_count": row[13] or 0,
                    "installed_count": row[14] or 0,
                    "failed_count": row[15] or 0,
                    "pending_reboot_count": row[16] or 0,
                    "last_state_at": row[17] or 0,
                    "held": held,
                }
            )
        return jsonify({"updates": data, "holds": holds}), 200

    @blueprint.route("/api/patch-management/policies", methods=["GET", "POST"])
    def patch_policies():
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        user = _current_user(app) or {}
        now = _now_ts()
        conn = adapters.db_conn_factory()
        try:
            cur = conn.cursor()
            if request.method == "POST":
                body = request.get_json(silent=True) or {}
                name = normalize_text(body.get("name"))
                if not name:
                    return jsonify({"error": "name_required"}), 400
                cur.execute(
                    """
                    INSERT INTO patch_policies(name, description, enabled, class_toggles_json, reboot_policy_json, created_at, updated_at)
                    VALUES (?, ?, ?, ?, ?, ?, ?)
                    """,
                    (
                        name,
                        normalize_text(body.get("description")),
                        1 if _bool(body.get("enabled"), True) else 0,
                        _json_dumps(_normalize_class_toggles(body.get("class_toggles"))),
                        _json_dumps(_normalize_reboot_policy(body.get("reboot"))),
                        now,
                        now,
                    ),
                )
                policy_id = int(cur.lastrowid)
                scope_type = normalize_text(body.get("scope_type")) or "global"
                if scope_type in {"global", "site", "device"}:
                    cur.execute(
                        """
                        INSERT INTO patch_policy_bindings(policy_id, scope_type, site_id, device_guid, created_at, updated_at)
                        VALUES (?, ?, ?, ?, ?, ?)
                        """,
                        (policy_id, scope_type, body.get("site_id"), normalize_text(body.get("device_guid")), now, now),
                    )
                conn.commit()
                _notify("policy_created", {"policy_id": policy_id})
            cur.execute(
                """
                SELECT p.id, p.name, p.description, p.enabled, p.class_toggles_json, p.reboot_policy_json, p.created_at, p.updated_at,
                       b.scope_type, b.site_id, b.device_guid
                  FROM patch_policies AS p
             LEFT JOIN patch_policy_bindings AS b ON b.policy_id = p.id
              ORDER BY p.id ASC
                """
            )
            policies = []
            for row in cur.fetchall():
                item = _policy_payload(row)
                item["description"] = normalize_text(row[2])
                item["scope_type"] = normalize_text(row[8]) or "unbound"
                item["site_id"] = row[9]
                item["device_guid"] = normalize_text(row[10])
                policies.append(item)
            return jsonify({"policies": policies, "class_defaults": PATCH_CLASS_DEFAULTS}), 200
        finally:
            conn.close()

    @blueprint.route("/api/patch-management/catalog/hold", methods=["POST"])
    def patch_hold():
        return _write_hold(release=False)

    @blueprint.route("/api/patch-management/catalog/release", methods=["POST"])
    def patch_release():
        return _write_hold(release=True)

    def _write_hold(*, release: bool):
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        user = _current_user(app) or {}
        body = request.get_json(silent=True) or {}
        update_id = normalize_text(body.get("update_id"))
        kb = normalize_text(body.get("kb") or body.get("kb_article"))
        if not update_id and not kb:
            return jsonify({"error": "patch_identifier_required"}), 400
        scope = normalize_text(body.get("scope")) or "global"
        if scope not in {"global", "policy"}:
            scope = "global"
        now = _now_ts()
        conn = adapters.db_conn_factory()
        try:
            cur = conn.cursor()
            if release:
                params: List[Any] = [now, normalize_text(user.get("username"))]
                clauses = ["released_at IS NULL"]
                if update_id:
                    clauses.append("LOWER(update_id) = LOWER(?)")
                    params.append(update_id)
                if kb:
                    clauses.append("LOWER(kb) = LOWER(?)")
                    params.append(kb)
                cur.execute(
                    f"UPDATE patch_holds SET released_at = ?, released_by = ? WHERE {' AND '.join(clauses)}",
                    params,
                )
                changed = cur.rowcount
            else:
                cur.execute(
                    """
                    INSERT INTO patch_holds(scope, policy_id, update_id, revision_number, kb, title, reason, created_by, created_at)
                    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
                    """,
                    (
                        scope,
                        body.get("policy_id") if scope == "policy" else None,
                        update_id,
                        _int(body.get("revision_number")),
                        kb,
                        normalize_text(body.get("title")),
                        normalize_text(body.get("reason")) or "Held by operator.",
                        normalize_text(user.get("username")),
                        now,
                    ),
                )
                changed = 1
            conn.commit()
        finally:
            conn.close()
        _notify("hold_released" if release else "hold_created", {"update_id": update_id, "kb": kb})
        return jsonify({"status": "ok", "changed": changed}), 200

    @blueprint.route("/api/patch-management/devices", methods=["GET"])
    def patch_devices():
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        user = _current_user(app) or {}
        site_clause, params = _site_clause_for_user(site_access, user)
        query_params = ["%windows%", *params]
        conn = adapters.db_conn_factory()
        try:
            cur = conn.cursor()
            cur.execute(
                f"""
                SELECT d.guid, d.hostname, s.name AS site_name, d.operating_system, d.last_seen,
                       COUNT(ps.id) AS total_updates,
                       SUM(CASE WHEN ps.status IN ('missing', 'pending_install') THEN 1 ELSE 0 END) AS missing_count,
                       SUM(CASE WHEN ps.status = 'installed' THEN 1 ELSE 0 END) AS installed_count,
                       SUM(CASE WHEN ps.status = 'failed' THEN 1 ELSE 0 END) AS failed_count,
                       SUM(CASE WHEN ps.reboot_required = 1 THEN 1 ELSE 0 END) AS pending_reboot_count,
                       MAX(ps.last_seen_at) AS last_scan_at
                  FROM devices AS d
             LEFT JOIN device_sites AS ds ON ds.device_hostname = d.hostname
             LEFT JOIN sites AS s ON s.id = ds.site_id
             LEFT JOIN device_patch_state AS ps ON ps.device_guid = d.guid
                 WHERE LOWER(COALESCE(d.operating_system, '')) LIKE ? {site_clause}
              GROUP BY d.guid, d.hostname, s.name, d.operating_system, d.last_seen
              ORDER BY missing_count DESC, d.hostname ASC
                """,
                query_params,
            )
            devices = [
                {
                    "device_guid": row[0],
                    "hostname": row[1],
                    "site_name": row[2] or "",
                    "operating_system": row[3] or "",
                    "last_seen": row[4] or 0,
                    "total_updates": row[5] or 0,
                    "missing_count": row[6] or 0,
                    "installed_count": row[7] or 0,
                    "failed_count": row[8] or 0,
                    "pending_reboot_count": row[9] or 0,
                    "last_scan_at": row[10] or 0,
                }
                for row in cur.fetchall()
            ]
        finally:
            conn.close()
        return jsonify({"devices": devices}), 200

    @blueprint.route("/api/patch-management/history", methods=["GET"])
    def patch_history():
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        user = _current_user(app) or {}
        site_clause, params = _site_clause_for_user(site_access, user)
        conn = adapters.db_conn_factory()
        try:
            cur = conn.cursor()
            cur.execute(
                f"""
                SELECT h.id, h.hostname, h.device_guid, h.action, h.status, h.requested_by, h.requested_at, h.started_at, h.finished_at, h.detail
                  FROM patch_action_history AS h
             LEFT JOIN devices AS d ON d.guid = h.device_guid OR LOWER(d.hostname) = LOWER(h.hostname)
             LEFT JOIN device_sites AS ds ON ds.device_hostname = d.hostname
                 WHERE 1=1 {site_clause}
              ORDER BY h.requested_at DESC
                 LIMIT 300
                """,
                params,
            )
            rows = [
                {
                    "id": row[0],
                    "hostname": row[1] or "",
                    "device_guid": row[2] or "",
                    "action": row[3] or "",
                    "status": row[4] or "",
                    "requested_by": row[5] or "",
                    "requested_at": row[6] or 0,
                    "started_at": row[7] or 0,
                    "finished_at": row[8] or 0,
                    "detail": row[9] or "",
                }
                for row in cur.fetchall()
            ]
        finally:
            conn.close()
        return jsonify({"history": rows}), 200

    @blueprint.route("/api/device/patches/<hostname>", methods=["GET"])
    def device_patches(hostname: str):
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        user = _current_user(app) or {}
        if not site_access.user_can_access_hostname(user, hostname):
            return jsonify({"error": "not found"}), 404
        record = _load_device_record(hostname)
        if record is None:
            return jsonify({"error": "not found"}), 404
        conn = adapters.db_conn_factory()
        try:
            cur = conn.cursor()
            policy = _resolve_effective_policy(conn, record)
            cur.execute(
                """
                SELECT ps.update_id, ps.revision_number, c.title, c.kb_articles_json, c.classifications_json, ps.status,
                       ps.approved, ps.held, ps.hold_reason, ps.reboot_required, ps.result_code, ps.hresult, ps.last_seen_at
                  FROM device_patch_state AS ps
             LEFT JOIN patch_catalog AS c ON c.update_id = ps.update_id AND c.revision_number = ps.revision_number
                 WHERE ps.device_guid = ?
              ORDER BY ps.status DESC, c.title ASC
                """,
                (record.get("guid"),),
            )
            updates = [
                {
                    "update_id": row[0],
                    "revision_number": row[1],
                    "title": row[2] or row[0],
                    "kb_article_ids": _json_loads(row[3], []),
                    "classifications": _json_loads(row[4], []),
                    "status": row[5],
                    "approved": bool(row[6]),
                    "held": bool(row[7]),
                    "hold_reason": row[8] or "",
                    "reboot_required": bool(row[9]),
                    "result_code": row[10] or "",
                    "hresult": row[11] or "",
                    "last_seen_at": row[12] or 0,
                }
                for row in cur.fetchall()
            ]
        finally:
            conn.close()
        return jsonify({"device": record, "policy": policy, "updates": updates}), 200

    @blueprint.route("/api/device/patches/<hostname>/action", methods=["POST"])
    def device_patch_action(hostname: str):
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        user = _current_user(app) or {}
        operator_id = normalize_text(user.get("username")) or "unknown"
        if not site_access.user_can_access_hostname(user, hostname):
            return jsonify({"error": "not found"}), 404
        record = _load_device_record(hostname)
        if record is None:
            return jsonify({"error": "not found"}), 404
        body = request.get_json(silent=True) or {}
        action = normalize_text(body.get("action")).lower()
        if action not in {"scan", "install", "policy_refresh", "reboot", "defer"}:
            return jsonify({"error": "invalid_action"}), 400
        now = _now_ts()
        if action == "defer":
            conn = adapters.db_conn_factory()
            try:
                cur = conn.cursor()
                cur.execute(
                    """
                    INSERT INTO patch_reboot_deferrals(device_guid, hostname, defer_until, deadline_ts, requested_by, created_at, updated_at)
                    VALUES (?, ?, ?, ?, ?, ?, ?)
                    """,
                    (record.get("guid"), record.get("hostname"), _int(body.get("defer_until")), _int(body.get("deadline_ts")), operator_id, now, now),
                )
                conn.commit()
            finally:
                conn.close()
            _notify("reboot_deferred", {"hostname": record.get("hostname")})
            return jsonify({"status": "ok", "action": "defer"}), 200
        emitted = _dispatch_patch_request(record, action, body, operator_id)
        conn = adapters.db_conn_factory()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                INSERT INTO patch_action_history(device_guid, hostname, action, status, requested_by, requested_at, detail, metadata_json)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?)
                """,
                (
                    record.get("guid"),
                    record.get("hostname"),
                    action,
                    "dispatched" if emitted else "agent_unavailable",
                    operator_id,
                    now,
                    "" if emitted else "Agent socket unavailable.",
                    _json_dumps(body),
                ),
            )
            conn.commit()
        finally:
            conn.close()
        if not emitted:
            return jsonify({"error": "agent_unavailable"}), 409
        _notify("action_dispatched", {"hostname": record.get("hostname"), "action": action})
        return jsonify({"status": "ok", "action": action}), 200

    app.register_blueprint(blueprint)
