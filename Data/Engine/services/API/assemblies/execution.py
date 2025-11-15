# ======================================================
# Data\Engine\services\API\assemblies\execution.py
# Description: Quick job dispatch and activity history endpoints for script and playbook assemblies.
#
# API Endpoints (if applicable):
# - POST /api/scripts/quick_run (Token Authenticated) - Queues a PowerShell assembly for execution on agents.
# - POST /api/ansible/quick_run (Token Authenticated) - (Not Yet Implemented) Placeholder for Ansible assemblies.
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
from pathlib import Path
from typing import TYPE_CHECKING, Any, Dict, List, Optional

from flask import Blueprint, jsonify, request

if TYPE_CHECKING:  # pragma: no cover - typing aide
    from flask import Flask

    from .. import EngineServiceAdapters

from ...assemblies.service import AssemblyRuntimeService
from ...auth import RequestAuthContext


def _assemblies_root() -> Path:
    base = Path(__file__).resolve()
    search_roots = (base, *base.parents)
    for candidate in search_roots:
        engine_dir: Optional[Path]
        if candidate.name.lower() == "engine":
            engine_dir = candidate
        else:
            tentative = candidate / "Engine"
            engine_dir = tentative if tentative.is_dir() else None
        if not engine_dir:
            continue
        assemblies_dir = engine_dir / "Assemblies"
        if assemblies_dir.is_dir():
            return assemblies_dir.resolve()
    raise RuntimeError("Engine assemblies directory not found; expected Engine/Assemblies.")


def _scripts_root() -> Path:
    assemblies_root = _assemblies_root()
    scripts_dir = assemblies_root / "Scripts"
    if not scripts_dir.is_dir():
        raise RuntimeError("Engine scripts directory not found; expected Engine/Assemblies/Scripts.")
    return scripts_dir.resolve()


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
        "metadata": {},
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
        doc["metadata"] = data.get("metadata") if isinstance(data.get("metadata"), dict) else {}
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


def register_execution(app: "Flask", adapters: "EngineServiceAdapters") -> None:
    """Register quick execution endpoints for assemblies."""

    blueprint = Blueprint("assemblies_execution", __name__)
    service_log = adapters.service_log
    assembly_cache = adapters.context.assembly_cache
    if assembly_cache is None:
        raise RuntimeError("Assembly cache is not initialised; ensure Engine bootstrap executed.")
    assembly_runtime = AssemblyRuntimeService(assembly_cache, logger=adapters.context.logger)
    auth = RequestAuthContext(
        app=app,
        dev_mode_manager=adapters.dev_mode_manager,
        config=adapters.config,
        logger=adapters.context.logger,
    )

    @blueprint.route("/api/scripts/quick_run", methods=["POST"])
    def scripts_quick_run():
        user, error = auth.require_user()
        if error:
            return jsonify(error[0]), error[1]
        data = request.get_json(silent=True) or {}
        rel_path_input = data.get("script_path")
        rel_path_normalized = _normalize_script_relpath(rel_path_input)
        hostnames = _normalize_hostnames(data.get("hostnames"))
        run_mode = (data.get("run_mode") or "system").strip().lower()
        admin_user = str(data.get("admin_user") or "").strip()
        admin_pass = str(data.get("admin_pass") or "").strip()

        if not rel_path_normalized or not hostnames:
            return jsonify({"error": "Missing script_path or hostnames[]"}), 400

        rel_path_canonical = rel_path_normalized
        username = (user.get("username") if isinstance(user, dict) else None) or "unknown"

        assembly_source = "runtime"
        assembly_guid: Optional[str] = None
        abs_path_str = rel_path_canonical
        doc: Optional[Dict[str, Any]] = None
        record: Optional[Dict[str, Any]] = None
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
                doc = _load_assembly_document(rel_path_canonical, "powershell", payload=payload_doc)
                if doc:
                    metadata_block = doc.get("metadata") if isinstance(doc.get("metadata"), dict) else {}
                    if isinstance(metadata_block, dict):
                        assembly_guid = metadata_block.get("assembly_guid")
                    if not doc.get("name"):
                        doc["name"] = record.get("display_name") or doc.get("name")
        if doc is None:
            assembly_source = "filesystem"
            try:
                scripts_root = _scripts_root()
                assemblies_root = scripts_root.parent.resolve()
                abs_path = (assemblies_root / rel_path_canonical).resolve()
            except Exception as exc:  # pragma: no cover - defensive guard
                service_log(
                    "assemblies",
                    f"quick job failed to resolve script path={rel_path_input!r} user={username}: {exc}",
                    level="ERROR",
                )
                return jsonify({"error": "Failed to resolve script path"}), 500

            scripts_root_str = str(scripts_root)
            abs_path_str = str(abs_path)
            try:
                within_scripts = os.path.commonpath([scripts_root_str, abs_path_str]) == scripts_root_str
            except ValueError:
                within_scripts = False

            if not within_scripts or not os.path.isfile(abs_path_str):
                service_log(
                    "assemblies",
                    f"quick job requested missing or out-of-scope script input={rel_path_input!r} normalized={rel_path_canonical} user={username}",
                    level="WARNING",
                )
                return jsonify({"error": "Script not found"}), 404

            doc = _load_assembly_document(abs_path_str, "powershell")
        if not doc:
            return jsonify({"error": "Script not found"}), 404

        script_type = (doc.get("type") or "powershell").lower()
        if script_type != "powershell":
            return jsonify({"error": f"Unsupported script type '{script_type}'. Only PowerShell is supported."}), 400

        content = doc.get("script") or ""
        doc_variables = doc.get("variables") if isinstance(doc.get("variables"), list) else []
        overrides_raw = data.get("variable_values")
        overrides: Dict[str, Any] = {}
        if isinstance(overrides_raw, dict):
            for key, val in overrides_raw.items():
                name = str(key or "").strip()
                if not name:
                    continue
                overrides[name] = val

        env_map, variables, literal_lookup = prepare_variable_context(doc_variables, overrides)
        content = rewrite_powershell_script(content, literal_lookup)
        normalized_script = (content or "").replace("\r\n", "\n")
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

        friendly_name = (doc.get("name") or "").strip()
        if not friendly_name:
            friendly_name = os.path.basename(rel_path_canonical)
        now = int(time.time())
        results: List[Dict[str, Any]] = []
        socketio = getattr(adapters.context, "socketio", None)
        if socketio is None:
            return jsonify({"error": "Realtime transport unavailable; cannot dispatch quick job."}), 500

        conn = None
        try:
            conn = adapters.db_conn_factory()
            cur = conn.cursor()
            for host in hostnames:
                cur.execute(
                    """
                    INSERT INTO activity_history(hostname, script_path, script_name, script_type, ran_at, status, stdout, stderr)
                    VALUES(?,?,?,?,?,?,?,?)
                    """,
                    (
                        host,
                        rel_path_canonical.replace(os.sep, "/"),
                        friendly_name,
                        script_type,
                        now,
                        "Running",
                        "",
                        "",
                    ),
                )
                job_id = cur.lastrowid
                conn.commit()

                payload = {
                    "job_id": job_id,
                    "target_hostname": host,
                    "script_type": script_type,
                    "script_name": friendly_name,
                    "script_path": rel_path_canonical.replace(os.sep, "/"),
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
                if signature_b64:
                    payload["signature"] = signature_b64
                    payload["sig_alg"] = "ed25519"
                if signing_key_b64:
                    payload["signing_key"] = signing_key_b64
                context_block = payload.setdefault("context", {})
                context_block["assembly_source"] = assembly_source
                if assembly_guid:
                    context_block["assembly_guid"] = assembly_guid

                socketio.emit("quick_job_run", payload)
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

                results.append({"hostname": host, "job_id": job_id, "status": "Running"})
                service_log(
                    "assemblies",
                    f"quick job queued hostname={host} path={rel_path_canonical} run_mode={run_mode} source={assembly_source} requested_by={username}",
                )
        except Exception as exc:
            if conn is not None:
                conn.rollback()
            return jsonify({"error": str(exc)}), 500
        finally:
            if conn is not None:
                conn.close()

        return jsonify({"results": results})

    @blueprint.route("/api/ansible/quick_run", methods=["POST"])
    def ansible_quick_run():
        _, error = auth.require_user()
        if error:
            return jsonify(error[0]), error[1]
        return jsonify({"error": "Ansible quick run is not yet available in the Engine runtime."}), 501

    @blueprint.route("/api/device/activity/<hostname>", methods=["GET", "DELETE"])
    def device_activity(hostname: str):
        _, error = auth.require_user()
        if error:
            return jsonify(error[0]), error[1]
        conn = None
        try:
            conn = adapters.db_conn_factory()
            cur = conn.cursor()
            if request.method == "DELETE":
                cur.execute("DELETE FROM activity_history WHERE hostname = ?", (hostname,))
                conn.commit()
                return jsonify({"status": "ok"})

            cur.execute(
                """
                SELECT id, script_name, script_path, script_type, ran_at, status, LENGTH(stdout), LENGTH(stderr)
                  FROM activity_history
                 WHERE hostname = ?
                 ORDER BY ran_at DESC, id DESC
                """,
                (hostname,),
            )
            rows = cur.fetchall()
            history = []
            for jid, name, path, stype, ran_at, status, so_len, se_len in rows:
                history.append(
                    {
                        "id": jid,
                        "script_name": name,
                        "script_path": path,
                        "script_type": stype,
                        "ran_at": ran_at,
                        "status": status,
                        "has_stdout": bool(so_len or 0),
                        "has_stderr": bool(se_len or 0),
                    }
                )
            return jsonify({"history": history})
        except Exception as exc:
            return jsonify({"error": str(exc)}), 500
        finally:
            if conn is not None:
                conn.close()

    @blueprint.route("/api/device/activity/job/<int:job_id>", methods=["GET"])
    def device_activity_job(job_id: int):
        _, error = auth.require_user()
        if error:
            return jsonify(error[0]), error[1]
        conn = None
        try:
            conn = adapters.db_conn_factory()
            cur = conn.cursor()
            cur.execute(
                """
                SELECT id, hostname, script_name, script_path, script_type, ran_at, status, stdout, stderr
                  FROM activity_history
                 WHERE id = ?
                """,
                (job_id,),
            )
            row = cur.fetchone()
            if not row:
                return jsonify({"error": "Not found"}), 404
            (jid, hostname, name, path, stype, ran_at, status, stdout, stderr) = row
            return jsonify(
                {
                    "id": jid,
                    "hostname": hostname,
                    "script_name": name,
                    "script_path": path,
                    "script_type": stype,
                    "ran_at": ran_at,
                    "status": status,
                    "stdout": stdout or "",
                    "stderr": stderr or "",
                }
            )
        except Exception as exc:
            return jsonify({"error": str(exc)}), 500
        finally:
            if conn is not None:
                conn.close()

    app.register_blueprint(blueprint)
