# ======================================================
# Data\Engine\services\API\assemblies\execution.py
# Description: Shared script assembly execution normalization helpers retained for Python runtimes.
#
# API Endpoints (if applicable):
# - None. Go api-backend owns public quick-run dispatch.
# ======================================================

"""Assembly execution helpers for the Borealis Engine runtime."""
from __future__ import annotations

import base64
import json
import os
import re
from typing import Any, Dict, List, Optional


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
