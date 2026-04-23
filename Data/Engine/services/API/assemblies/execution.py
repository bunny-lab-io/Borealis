# ======================================================
# Data\Engine\services\API\assemblies\execution.py
# Description: Quick job dispatch and activity history endpoints for script assemblies.
#
# API Endpoints (if applicable):
# - POST /api/scripts/quick_run (Token Authenticated) - Queues a script assembly for execution on agents.
# - GET/DELETE /api/device/activity/<hostname> (Token Authenticated) - Retrieves or clears device activity history.
# - GET /api/device/activity/job/<int:job_id> (Token Authenticated) - Retrieves a specific activity record.
# ======================================================

"""Assembly execution helpers for the Borealis Engine runtime."""
from __future__ import annotations

import base64
import json
import os
import re
import time
from typing import TYPE_CHECKING, Any, Dict, List, Optional

from flask import Blueprint, jsonify, request

if TYPE_CHECKING:  # pragma: no cover - typing aide
    from flask import Flask

    from .. import EngineServiceAdapters

from ...assemblies.service import AssemblyRuntimeService
from ...auth import RequestAuthContext, UserSiteAccessManager
from ...activity_history import (
    get_activity_history_row,
    insert_activity_history_row,
    list_activity_history_rows,
    update_activity_history_row,
)
from ..devices.session_dispatch import build_currentuser_dispatch_fields

def _normalize_script_relpath(rel_path: Any) -> Optional[str]:
    """Return a canonical Scripts-relative path or ``None`` when invalid."""

    if not isinstance(rel_path, str):
        return None

    raw = rel_path.replace("\\", "/").strip()
    if not raw:
        return None

    segments: List[str] = []
    for part in raw.split("/"):
        candidate = part.strip()
        if not candidate or candidate == ".":
            continue
        if candidate == "..":
            return None
        segments.append(candidate)

    if not segments:
        return None

    first = segments[0]
    if first.lower() != "scripts":
        segments.insert(0, "Scripts")
    else:
        segments[0] = "Scripts"

    return "/".join(segments)


def _decode_base64_text(value: Any) -> Optional[str]:
    if not isinstance(value, str):
        return None
    stripped = value.strip()
    if not stripped:
        return ""
    try:
        cleaned = re.sub(r"\s+", "", stripped)
    except Exception:
        cleaned = stripped
    try:
        decoded = base64.b64decode(cleaned, validate=True)
    except Exception:
        return None
    try:
        return decoded.decode("utf-8")
    except Exception:
        return decoded.decode("utf-8", errors="replace")


def _decode_script_content(value: Any, encoding_hint: str = "") -> str:
    encoding = (encoding_hint or "").strip().lower()
    if isinstance(value, str):
        if encoding in {"base64", "b64", "base-64"}:
            decoded = _decode_base64_text(value)
            if decoded is not None:
                return decoded.replace("\r\n", "\n")
        decoded = _decode_base64_text(value)
        if decoded is not None:
            return decoded.replace("\r\n", "\n")
        return value.replace("\r\n", "\n")
    return ""


def _canonical_env_key(name: Any) -> str:
    try:
        return re.sub(r"[^A-Za-z0-9_]", "_", str(name or "").strip()).upper()
    except Exception:
        return ""


def _env_string(value: Any) -> str:
    if isinstance(value, bool):
        return "True" if value else "False"
    if value is None:
        return ""
    return str(value)


def _powershell_literal(value: Any, var_type: str) -> str:
    typ = str(var_type or "string").lower()
    if typ == "boolean":
        if isinstance(value, bool):
            truthy = value
        elif value is None:
            truthy = False
        elif isinstance(value, (int, float)):
            truthy = value != 0
        else:
            s = str(value).strip().lower()
            if s in {"true", "1", "yes", "y", "on"}:
                truthy = True
            elif s in {"false", "0", "no", "n", "off", ""}:
                truthy = False
            else:
                truthy = bool(s)
        return "$true" if truthy else "$false"
    if typ == "number":
        if value is None or value == "":
            return "0"
        return str(value)
    s = "" if value is None else str(value)
    return "'" + s.replace("'", "''") + "'"


def _expand_env_aliases(env_map: Dict[str, str], variables: List[Dict[str, Any]]) -> Dict[str, str]:
    expanded: Dict[str, str] = dict(env_map or {})
    if not isinstance(variables, list):
        return expanded
    for var in variables:
        if not isinstance(var, dict):
            continue
        name = str(var.get("name") or "").strip()
        if not name:
            continue
        canonical = _canonical_env_key(name)
        if not canonical or canonical not in expanded:
            continue
        value = expanded[canonical]
        alias = re.sub(r"[^A-Za-z0-9_]", "_", name)
        if alias and alias not in expanded:
            expanded[alias] = value
        if alias != name and re.match(r"^[A-Za-z_][A-Za-z0-9_]*$", name) and name not in expanded:
            expanded[name] = value
    return expanded


def _extract_variable_default(var: Dict[str, Any]) -> Any:
    for key in ("value", "default", "defaultValue", "default_value"):
        if key in var:
            val = var.get(key)
            return "" if val is None else val
    return ""


def prepare_variable_context(doc_variables: List[Dict[str, Any]], overrides: Dict[str, Any]):
    env_map: Dict[str, str] = {}
    variables: List[Dict[str, Any]] = []
    literal_lookup: Dict[str, str] = {}
    doc_names: Dict[str, bool] = {}

    overrides = overrides or {}

    if not isinstance(doc_variables, list):
        doc_variables = []

    for var in doc_variables:
        if not isinstance(var, dict):
            continue
        name = str(var.get("name") or "").strip()
        if not name:
            continue
        doc_names[name] = True
        canonical = _canonical_env_key(name)
        var_type = str(var.get("type") or "string").lower()
        default_val = _extract_variable_default(var)
        final_val = overrides[name] if name in overrides else default_val
        if canonical:
            env_map[canonical] = _env_string(final_val)
            literal_lookup[canonical] = _powershell_literal(final_val, var_type)
        if name in overrides:
            new_var = dict(var)
            new_var["value"] = overrides[name]
            variables.append(new_var)
        else:
            variables.append(var)

    for name, val in overrides.items():
        if name in doc_names:
            continue
        canonical = _canonical_env_key(name)
        if canonical:
            env_map[canonical] = _env_string(val)
            literal_lookup[canonical] = _powershell_literal(val, "string")
        variables.append({"name": name, "value": val, "type": "string"})

    env_map = _expand_env_aliases(env_map, variables)
    return env_map, variables, literal_lookup


_ENV_VAR_PATTERN = re.compile(r"(?i)\$env:(\{)?([A-Za-z0-9_\-]+)(?(1)\})")


def rewrite_powershell_script(content: str, literal_lookup: Dict[str, str]) -> str:
    if not content or not literal_lookup:
        return content

    def _replace(match: Any) -> str:
        name = match.group(2)
        canonical = _canonical_env_key(name)
        if not canonical:
            return match.group(0)
        literal = literal_lookup.get(canonical)
        if literal is None:
            return match.group(0)
        return literal

    return _ENV_VAR_PATTERN.sub(_replace, content)


_SUPPORTED_AGENT_SCRIPT_TYPES = {"powershell", "batch", "bash"}


def _normalize_agent_script_type(value: Any) -> str:
    normalized = str(value or "powershell").strip().lower()
    return normalized or "powershell"


def _rewrite_script_for_dispatch(content: str, script_type: str, literal_lookup: Dict[str, str]) -> str:
    normalized_content = (content or "").replace("\r\n", "\n")
    if script_type == "powershell":
        return rewrite_powershell_script(normalized_content, literal_lookup)
    return normalized_content


def _normalize_target_service_mode(value: Any) -> str:
    normalized = str(value or "").strip().lower()
    if normalized == "current_user":
        return "currentuser"
    if normalized in {"system", "currentuser"}:
        return normalized
    return ""


def _load_assembly_document(
    source_identifier: str,
    default_type: str,
    payload: Optional[Dict[str, Any]] = None,
) -> Dict[str, Any]:
    abs_path_str = os.fspath(source_identifier)
    base_name = os.path.splitext(os.path.basename(abs_path_str))[0]
    doc: Dict[str, Any] = {
        "name": base_name,
        "description": "",
        "category": "application" if default_type == "ansible" else "script",
        "type": default_type,
        "script": "",
        "variables": [],
        "files": [],
        "timeout_seconds": 3600,
    }
    data: Dict[str, Any] = {}
    if isinstance(payload, dict):
        data = payload
    elif abs_path_str.lower().endswith(".json") and os.path.isfile(abs_path_str):
        try:
            with open(abs_path_str, "r", encoding="utf-8") as fh:
                data = json.load(fh)
        except Exception:
            data = {}
    if isinstance(data, dict) and data:
        doc["name"] = str(data.get("name") or doc["name"])
        doc["description"] = str(data.get("description") or "")
        cat = str(data.get("category") or doc["category"]).strip().lower()
        if cat in {"application", "script"}:
            doc["category"] = cat
        typ = str(data.get("type") or data.get("script_type") or default_type).strip().lower()
        if typ in {"powershell", "batch", "bash", "ansible"}:
            doc["type"] = typ
        script_val = data.get("script")
        content_val = data.get("content")
        script_lines = data.get("script_lines")
        if isinstance(script_lines, list):
            try:
                doc["script"] = "\n".join(str(line) for line in script_lines)
            except Exception:
                doc["script"] = ""
        elif isinstance(script_val, str):
            doc["script"] = script_val
        elif isinstance(content_val, str):
            doc["script"] = content_val
        encoding_hint = str(data.get("script_encoding") or data.get("scriptEncoding") or "").strip().lower()
        doc["script"] = _decode_script_content(doc.get("script"), encoding_hint)
        if encoding_hint in {"base64", "b64", "base-64"}:
            doc["script_encoding"] = "base64"
        else:
            probe_source = ""
            if isinstance(script_val, str) and script_val:
                probe_source = script_val
            elif isinstance(content_val, str) and content_val:
                probe_source = content_val
            decoded_probe = _decode_base64_text(probe_source) if probe_source else None
            if decoded_probe is not None:
                doc["script_encoding"] = "base64"
                doc["script"] = decoded_probe.replace("\r\n", "\n")
            else:
                doc["script_encoding"] = "plain"
        try:
            timeout_raw = data.get("timeout_seconds", data.get("timeout"))
            if timeout_raw is None:
                doc["timeout_seconds"] = 3600
            else:
                doc["timeout_seconds"] = max(0, int(timeout_raw))
        except Exception:
            doc["timeout_seconds"] = 3600
        vars_in = data.get("variables") if isinstance(data.get("variables"), list) else []
        doc["variables"] = []
        for item in vars_in:
            if not isinstance(item, dict):
                continue
            name = str(item.get("name") or item.get("key") or "").strip()
            if not name:
                continue
            vtype = str(item.get("type") or "string").strip().lower()
            if vtype not in {"string", "number", "boolean", "credential"}:
                vtype = "string"
            doc["variables"].append(
                {
                    "name": name,
                    "label": str(item.get("label") or ""),
                    "type": vtype,
                    "default": item.get("default", item.get("default_value")),
                    "required": bool(item.get("required")),
                    "description": str(item.get("description") or ""),
                }
            )
        files_in = data.get("files") if isinstance(data.get("files"), list) else []
        doc["files"] = []
        for file_item in files_in:
            if not isinstance(file_item, dict):
                continue
            fname = file_item.get("file_name") or file_item.get("name")
            if not fname or not isinstance(file_item.get("data"), str):
                continue
            try:
                size_val = int(file_item.get("size") or 0)
            except Exception:
                size_val = 0
            doc["files"].append(
                {
                    "file_name": str(fname),
                    "size": size_val,
                    "mime_type": str(file_item.get("mime_type") or file_item.get("mimeType") or ""),
                    "data": file_item.get("data"),
                }
            )
        return doc
    if os.path.isfile(abs_path_str):
        try:
            with open(abs_path_str, "r", encoding="utf-8", errors="replace") as fh:
                content = fh.read()
        except Exception:
            content = ""
        doc["script"] = (content or "").replace("\r\n", "\n")
    else:
        doc["script"] = ""
    return doc


def _normalize_hostnames(value: Any) -> List[str]:
    if not isinstance(value, list):
        return []
    hosts: List[str] = []
    for item in value:
        name = str(item or "").strip()
        if name:
            hosts.append(name)
    return hosts


def dispatch_inline_quick_job(
    adapters: "EngineServiceAdapters",
    *,
    hostnames: List[str],
    doc: Dict[str, Any],
    script_path: str,
    requested_by: str,
    variable_overrides: Optional[Dict[str, Any]] = None,
    run_mode: str = "system",
    session_target: Any = None,
    target_session_id: Any = None,
    admin_user: str = "",
    admin_pass: str = "",
    assembly_source: str = "runtime",
    assembly_guid: Optional[str] = None,
    queue_lane: str = "",
    activity_kind: str = "",
    activity_metadata: Optional[Dict[str, Any]] = None,
) -> Dict[str, Any]:
    normalized_hosts = [str(host or "").strip() for host in (hostnames or []) if str(host or "").strip()]
    if not normalized_hosts:
        raise ValueError("Missing hostnames[]")

    script_type = _normalize_agent_script_type(doc.get("type"))
    if script_type not in _SUPPORTED_AGENT_SCRIPT_TYPES:
        raise ValueError(
            f"Unsupported script type '{script_type}'. "
            "Agent quick jobs currently support PowerShell, Batch, and Bash."
        )

    content = doc.get("script") or ""
    doc_variables = doc.get("variables") if isinstance(doc.get("variables"), list) else []
    env_map, variables, literal_lookup = prepare_variable_context(doc_variables, variable_overrides or {})
    normalized_script = _rewrite_script_for_dispatch(content, script_type, literal_lookup)
    script_bytes = normalized_script.encode("utf-8")
    encoded_content = (
        base64.b64encode(script_bytes).decode("ascii")
        if script_bytes or normalized_script == ""
        else ""
    )

    signature_b64 = ""
    signing_key_b64 = ""
    script_signer = adapters.script_signer
    if script_signer is not None:
        try:
            signature_raw = script_signer.sign(script_bytes)
            signature_b64 = base64.b64encode(signature_raw).decode("ascii")
            signing_key_b64 = script_signer.public_base64_spki()
        except Exception:
            signature_b64 = ""
            signing_key_b64 = ""

    try:
        timeout_seconds = max(0, int(doc.get("timeout_seconds") or 0))
    except Exception:
        timeout_seconds = 0

    script_path_normalized = (
        _normalize_script_relpath(script_path)
        or str(script_path or "").replace("\\", "/").strip()
        or "Scripts/Internal/Inline.ps1"
    )
    friendly_name = (doc.get("name") or "").strip()
    if not friendly_name:
        friendly_name = os.path.basename(script_path_normalized)

    socketio = getattr(adapters.context, "socketio", None)
    if socketio is None:
        raise RuntimeError("Realtime transport unavailable; cannot dispatch quick job.")

    emit_host_service_event = getattr(adapters.context, "emit_host_service_event", None)
    target_service_mode = _normalize_target_service_mode(run_mode)
    service_log = getattr(adapters, "service_log", None)
    now = int(time.time())
    results: List[Dict[str, Any]] = []
    resolved_queue_lane = str(queue_lane or "").strip().lower().replace("-", "_")
    if not resolved_queue_lane and str(assembly_source or "").strip().lower() == "device_software_uninstall":
        resolved_queue_lane = "software_management"
    resolved_activity_kind = str(activity_kind or "").strip().lower().replace("-", "_")
    if not resolved_activity_kind and str(assembly_source or "").strip().lower() == "device_software_uninstall":
        resolved_activity_kind = "software_uninstall"
    base_activity_metadata: Dict[str, Any] = {
        "assembly_source": str(assembly_source or "").strip(),
        "requested_by": str(requested_by or "").strip(),
    }
    if assembly_guid:
        base_activity_metadata["assembly_guid"] = str(assembly_guid).strip().lower()
    if isinstance(activity_metadata, dict):
        for raw_key, raw_value in activity_metadata.items():
            key = str(raw_key or "").strip()
            if key:
                base_activity_metadata[key] = raw_value
    initial_status = "Queued" if resolved_queue_lane else "Running"

    def _write_service_log(message: str, *, level: str = "INFO") -> None:
        if not callable(service_log):
            return
        try:
            service_log("assemblies", message, level=level)
        except Exception:
            pass

    def _mark_activity_failed(job_id: int, failure_text: str) -> None:
        conn = None
        try:
            conn = adapters.db_conn_factory()
            failure_ts = _now_ts()
            update_activity_history_row(
                conn,
                int(job_id),
                status="Failed",
                stderr=failure_text,
                updated_at=failure_ts,
                finished_at=failure_ts,
            )
            conn.commit()
        except Exception:
            if conn is not None:
                conn.rollback()
            raise
        finally:
            if conn is not None:
                conn.close()

    activity_rows: List[tuple[str, int]] = []
    conn = None
    try:
        conn = adapters.db_conn_factory()
        for host in normalized_hosts:
            job_id = insert_activity_history_row(
                conn,
                hostname=host,
                script_path=script_path_normalized,
                script_name=friendly_name,
                script_type=script_type,
                ran_at=now,
                status=initial_status,
                stdout="",
                stderr="",
                queue_lane=resolved_queue_lane,
                activity_kind=resolved_activity_kind,
                metadata=base_activity_metadata,
                updated_at=now,
            )
            activity_rows.append((host, int(job_id or 0)))
        conn.commit()
    except Exception:
        if conn is not None:
            conn.rollback()
        raise
    finally:
        if conn is not None:
            conn.close()

    for host, job_id in activity_rows:
        payload = {
            "job_id": job_id,
            "target_hostname": host,
            "script_type": script_type,
            "script_name": friendly_name,
            "script_path": script_path_normalized,
            "script_content": encoded_content,
            "script_encoding": "base64",
            "environment": env_map,
            "variables": variables,
            "timeout_seconds": timeout_seconds,
            "files": doc.get("files") if isinstance(doc.get("files"), list) else [],
            "run_mode": run_mode,
            "admin_user": admin_user,
            "admin_pass": admin_pass,
        }
        payload.update(
            build_currentuser_dispatch_fields(
                run_mode=run_mode,
                session_target=session_target,
                target_session_id=target_session_id,
            )
        )
        if signature_b64:
            payload["signature"] = signature_b64
            payload["sig_alg"] = "ed25519"
        if signing_key_b64:
            payload["signing_key"] = signing_key_b64
        context_block = payload.setdefault("context", {})
        context_block["assembly_source"] = assembly_source
        if assembly_guid:
            context_block["assembly_guid"] = assembly_guid
        if resolved_queue_lane:
            context_block["queue_lane"] = resolved_queue_lane
        if resolved_activity_kind:
            context_block["activity_kind"] = resolved_activity_kind
        if base_activity_metadata:
            context_block["activity_metadata"] = dict(base_activity_metadata)

        emitted = False
        delivery_mode = "broadcast"
        if target_service_mode and callable(emit_host_service_event):
            emitted = bool(
                emit_host_service_event(
                    host,
                    target_service_mode,
                    "quick_job_run",
                    payload,
                )
            )
            delivery_mode = f"targeted:{target_service_mode}"
        else:
            socketio.emit("quick_job_run", payload)
            emitted = True

        if not emitted and target_service_mode and callable(emit_host_service_event):
            failure_text = (
                f"No {target_service_mode} agent socket is registered for host {host}; "
                "unable to dispatch quick job."
            )
            _mark_activity_failed(job_id, failure_text)
            try:
                socketio.emit(
                    "device_activity_changed",
                    {
                        "hostname": host,
                        "activity_id": job_id,
                        "change": "updated",
                        "source": "quick_job",
                    },
                )
            except Exception:
                pass
            results.append(
                {
                    "hostname": host,
                    "job_id": job_id,
                    "status": "Failed",
                    "error": failure_text,
                }
            )
            _write_service_log(
                (
                    f"quick job dispatch failed hostname={host} path={script_path_normalized} "
                    f"run_mode={run_mode} source={assembly_source} requested_by={requested_by} "
                    f"delivery={delivery_mode} error={failure_text}"
                ),
                level="ERROR",
            )
            continue

        try:
            socketio.emit(
                "device_activity_changed",
                {
                    "hostname": host,
                    "activity_id": job_id,
                    "change": "created",
                    "source": "quick_job",
                },
            )
        except Exception:
            pass

        results.append({"hostname": host, "job_id": job_id, "status": initial_status})
        _write_service_log(
            (
                f"quick job queued hostname={host} path={script_path_normalized} "
                f"run_mode={run_mode} source={assembly_source} requested_by={requested_by} "
                f"delivery={delivery_mode} script_type={script_type}"
            ),
        )

    return {
        "results": results,
        "script_name": friendly_name,
        "script_path": script_path_normalized,
        "script_type": script_type,
    }


def register_execution(app: "Flask", adapters: "EngineServiceAdapters") -> None:
    """Register quick execution endpoints for assemblies."""

    blueprint = Blueprint("assemblies_execution", __name__)
    assembly_cache = adapters.context.assembly_cache
    if assembly_cache is None:
        raise RuntimeError("Assembly cache is not initialised; ensure Engine bootstrap executed.")
    assembly_runtime = AssemblyRuntimeService(assembly_cache, logger=adapters.context.logger)
    auth = RequestAuthContext(
        app=app,
        dev_mode_manager=adapters.dev_mode_manager,
        config=adapters.config,
        logger=adapters.context.logger,
        db_conn_factory=adapters.db_conn_factory,
        aegis_cipher_service=adapters.aegis_cipher_service,
    )
    site_access = UserSiteAccessManager(adapters.db_conn_factory, logger=adapters.context.logger)

    @blueprint.route("/api/scripts/quick_run", methods=["POST"])
    def scripts_quick_run():
        user, error = auth.require_user()
        if error:
            return jsonify(error[0]), error[1]
        data = request.get_json(silent=True) or {}
        assembly_guid_input = str(data.get("assembly_guid") or "").strip().lower()
        rel_path_input = data.get("script_path")
        rel_path_normalized = _normalize_script_relpath(rel_path_input)
        hostnames = _normalize_hostnames(data.get("hostnames"))
        run_mode = (data.get("run_mode") or "system").strip().lower()
        session_target = data.get("session_target")
        target_session_id = data.get("target_session_id")
        admin_user = str(data.get("admin_user") or "").strip()
        admin_pass = str(data.get("admin_pass") or "").strip()

        if not hostnames:
            return jsonify({"error": "Missing hostnames[]"}), 400
        if not rel_path_normalized and not assembly_guid_input:
            return jsonify({"error": "Missing script_path or assembly_guid"}), 400
        inaccessible_hosts = [
            host
            for host in hostnames
            if not site_access.user_can_access_hostname(user, host)
        ]
        if inaccessible_hosts:
            return jsonify(
                {
                    "error": "out_of_scope_hostnames",
                    "message": "One or more selected devices is outside your assigned sites.",
                    "hostnames": inaccessible_hosts,
                }
            ), 403

        rel_path_canonical = rel_path_normalized or ""
        username = (user.get("username") if isinstance(user, dict) else None) or "unknown"

        assembly_source = "runtime"
        assembly_guid: Optional[str] = None
        abs_path_str = rel_path_canonical
        doc: Optional[Dict[str, Any]] = None
        record: Optional[Dict[str, Any]] = None
        if assembly_guid_input:
            try:
                record = assembly_runtime.resolve_document_by_guid(assembly_guid_input)
            except Exception:
                record = None
        if record is None and rel_path_canonical:
            try:
                record = assembly_runtime.resolve_document_by_source_path(rel_path_canonical)
            except Exception:
                record = None
        if record:
            payload_doc = record.get("payload_json")
            if not isinstance(payload_doc, dict):
                raw_payload = record.get("payload")
                if isinstance(raw_payload, str):
                    try:
                        payload_doc = json.loads(raw_payload)
                    except Exception:
                        payload_doc = None
            if isinstance(payload_doc, dict):
                assembly_guid = str(record.get("assembly_guid") or "").strip().lower() or None
                source_identifier = (
                    rel_path_canonical
                    or str(record.get("virtual_path") or "").strip()
                    or (assembly_guid or "Scripts/Assembly")
                )
                doc = _load_assembly_document(source_identifier, "powershell", payload=payload_doc)
                if doc:
                    if not doc.get("name"):
                        doc["name"] = record.get("display_name") or doc.get("name")
                    if not rel_path_canonical:
                        rel_path_canonical = str(record.get("virtual_path") or "").strip() or source_identifier
        if doc is None:
            return jsonify({"error": "Script not found"}), 404
        if not doc:
            return jsonify({"error": "Script not found"}), 404

        script_type = _normalize_agent_script_type(doc.get("type"))
        if script_type not in _SUPPORTED_AGENT_SCRIPT_TYPES:
            return jsonify(
                {
                    "error": (
                        f"Unsupported script type '{script_type}'. "
                        "Agent quick jobs currently support PowerShell, Batch, and Bash."
                    )
                }
            ), 400

        overrides_raw = data.get("variable_values")
        overrides: Dict[str, Any] = {}
        if isinstance(overrides_raw, dict):
            for key, val in overrides_raw.items():
                name = str(key or "").strip()
                if not name:
                    continue
                overrides[name] = val

        try:
            dispatch = dispatch_inline_quick_job(
                adapters,
                hostnames=hostnames,
                doc=doc,
                script_path=rel_path_canonical,
                requested_by=username,
                variable_overrides=overrides,
                run_mode=run_mode,
                session_target=session_target,
                target_session_id=target_session_id,
                admin_user=admin_user,
                admin_pass=admin_pass,
                assembly_source=assembly_source,
                assembly_guid=assembly_guid,
            )
        except ValueError as exc:
            return jsonify({"error": str(exc)}), 400
        except RuntimeError as exc:
            return jsonify({"error": str(exc)}), 500
        except Exception as exc:
            return jsonify({"error": str(exc)}), 500

        return jsonify({"results": dispatch["results"]})

    @blueprint.route("/api/device/activity/<hostname>", methods=["GET", "DELETE"])
    def device_activity(hostname: str):
        user, error = auth.require_user()
        if error:
            return jsonify(error[0]), error[1]
        if not site_access.user_can_access_hostname(user, hostname):
            return jsonify({"error": "Not found"}), 404
        conn = None
        try:
            conn = adapters.db_conn_factory()
            cur = conn.cursor()
            if request.method == "DELETE":
                cur.execute("DELETE FROM activity_history WHERE hostname = ?", (hostname,))
                conn.commit()
                return jsonify({"status": "ok"})
            history = list_activity_history_rows(conn, hostname)
            return jsonify({"history": history})
        except Exception as exc:
            return jsonify({"error": str(exc)}), 500
        finally:
            if conn is not None:
                conn.close()

    @blueprint.route("/api/device/activity/job/<int:job_id>", methods=["GET"])
    def device_activity_job(job_id: int):
        user, error = auth.require_user()
        if error:
            return jsonify(error[0]), error[1]
        conn = None
        try:
            conn = adapters.db_conn_factory()
            row = get_activity_history_row(conn, job_id)
            if not row:
                return jsonify({"error": "Not found"}), 404
            hostname = str(row.get("hostname") or "")
            if not site_access.user_can_access_hostname(user, hostname):
                return jsonify({"error": "Not found"}), 404
            return jsonify(row)
        except Exception as exc:
            return jsonify({"error": str(exc)}), 500
        finally:
            if conn is not None:
                conn.close()

    app.register_blueprint(blueprint)
