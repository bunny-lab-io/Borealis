"""Builders for Engine job manifests."""

from __future__ import annotations

import base64
import json
import logging
import os
import re
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Dict, Iterable, List, Mapping, Optional, Tuple

from Data.Engine.repositories.sqlite.job_repository import ScheduledJobRecord

__all__ = [
    "JobComponentManifest",
    "JobManifest",
    "JobFabricator",
]


_ENV_VAR_PATTERN = re.compile(r"(?i)\$env:(\{)?([A-Za-z0-9_\-]+)(?(1)\})")


@dataclass(frozen=True, slots=True)
class JobComponentManifest:
    """Materialized job component ready for execution."""

    name: str
    path: str
    script_type: str
    script_content: str
    encoded_content: str
    environment: Dict[str, str]
    literal_environment: Dict[str, str]
    timeout_seconds: int


@dataclass(frozen=True, slots=True)
class JobManifest:
    job_id: int
    name: str
    occurrence_ts: int
    execution_context: str
    targets: Tuple[str, ...]
    components: Tuple[JobComponentManifest, ...]


class JobFabricator:
    """Convert stored job records into immutable manifests."""

    def __init__(
        self,
        *,
        assemblies_root: Path,
        logger: Optional[logging.Logger] = None,
    ) -> None:
        self._assemblies_root = assemblies_root
        self._log = logger or logging.getLogger("borealis.engine.builders.jobs")

    def build(
        self,
        job: ScheduledJobRecord,
        *,
        occurrence_ts: int,
    ) -> JobManifest:
        components = tuple(self._materialize_component(job, component) for component in job.components)
        targets = tuple(str(t) for t in job.targets)
        return JobManifest(
            job_id=job.id,
            name=job.name,
            occurrence_ts=occurrence_ts,
            execution_context=job.execution_context,
            targets=targets,
            components=tuple(c for c in components if c is not None),
        )

    # ------------------------------------------------------------------
    # Component handling
    # ------------------------------------------------------------------
    def _materialize_component(
        self,
        job: ScheduledJobRecord,
        component: Mapping[str, Any],
    ) -> Optional[JobComponentManifest]:
        if not isinstance(component, Mapping):
            return None

        component_type = str(component.get("type") or "").strip().lower()
        if component_type not in {"script", "ansible"}:
            return None

        path = str(component.get("path") or component.get("script_path") or "").strip()
        if not path:
            return None

        try:
            abs_path = self._resolve_script_path(path)
        except FileNotFoundError:
            self._log.warning(
                "job component path invalid", extra={"job_id": job.id, "path": path}
            )
            return None
        script_type = self._detect_script_type(abs_path, component_type)
        script_content = self._load_script_content(abs_path, component)

        doc_variables: List[Dict[str, Any]] = []
        if isinstance(component.get("variables"), list):
            doc_variables = [v for v in component["variables"] if isinstance(v, dict)]
        overrides = self._collect_overrides(component)
        env_map, _, literal_lookup = _prepare_variable_context(doc_variables, overrides)

        rewritten = _rewrite_powershell_script(script_content, literal_lookup)
        encoded = _encode_script_content(rewritten)

        timeout_seconds = _coerce_int(component.get("timeout_seconds"))
        if not timeout_seconds:
            timeout_seconds = _coerce_int(component.get("timeout"))

        return JobComponentManifest(
            name=self._component_name(abs_path, component),
            path=path,
            script_type=script_type,
            script_content=rewritten,
            encoded_content=encoded,
            environment=env_map,
            literal_environment=literal_lookup,
            timeout_seconds=timeout_seconds,
        )

    def _component_name(self, abs_path: Path, component: Mapping[str, Any]) -> str:
        if isinstance(component.get("name"), str) and component["name"].strip():
            return component["name"].strip()
        return abs_path.stem

    def _resolve_script_path(self, rel_path: str) -> Path:
        candidate = Path(rel_path.replace("\\", "/").lstrip("/"))
        if candidate.parts and candidate.parts[0] != "Scripts":
            candidate = Path("Scripts") / candidate
        abs_path = (self._assemblies_root / candidate).resolve()
        try:
            abs_path.relative_to(self._assemblies_root)
        except ValueError:
            raise FileNotFoundError(rel_path)
        if not abs_path.is_file():
            raise FileNotFoundError(rel_path)
        return abs_path

    def _load_script_content(self, abs_path: Path, component: Mapping[str, Any]) -> str:
        if isinstance(component.get("script"), str) and component["script"].strip():
            return _decode_script_content(component["script"], component.get("encoding") or "")
        try:
            return abs_path.read_text(encoding="utf-8")
        except Exception as exc:
            self._log.warning("unable to read script for job component: path=%s error=%s", abs_path, exc)
            return ""

    def _detect_script_type(self, abs_path: Path, declared: str) -> str:
        lower = declared.lower()
        if lower in {"script", "powershell"}:
            return "powershell"
        suffix = abs_path.suffix.lower()
        if suffix == ".ps1":
            return "powershell"
        if suffix == ".yml":
            return "ansible"
        if suffix == ".json":
            try:
                data = json.loads(abs_path.read_text(encoding="utf-8"))
                if isinstance(data, dict):
                    t = str(data.get("type") or data.get("script_type") or "").strip().lower()
                    if t:
                        return t
            except Exception:
                pass
        return lower or "powershell"

    def _collect_overrides(self, component: Mapping[str, Any]) -> Dict[str, Any]:
        overrides: Dict[str, Any] = {}
        values = component.get("variable_values")
        if isinstance(values, Mapping):
            for key, value in values.items():
                name = str(key or "").strip()
                if name:
                    overrides[name] = value
        vars_inline = component.get("variables")
        if isinstance(vars_inline, Iterable):
            for var in vars_inline:
                if not isinstance(var, Mapping):
                    continue
                name = str(var.get("name") or "").strip()
                if not name:
                    continue
                if "value" in var:
                    overrides[name] = var.get("value")
        return overrides


def _coerce_int(value: Any) -> int:
    try:
        return int(value or 0)
    except Exception:
        return 0


def _env_string(value: Any) -> str:
    if isinstance(value, bool):
        return "True" if value else "False"
    if value is None:
        return ""
    return str(value)


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


def _decode_script_content(value: Any, encoding_hint: Any = "") -> str:
    encoding = str(encoding_hint or "").strip().lower()
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


def _encode_script_content(script_text: Any) -> str:
    if not isinstance(script_text, str):
        if script_text is None:
            script_text = ""
        else:
            script_text = str(script_text)
    normalized = script_text.replace("\r\n", "\n")
    if not normalized:
        return ""
    encoded = base64.b64encode(normalized.encode("utf-8"))
    return encoded.decode("ascii")


def _canonical_env_key(name: Any) -> str:
    try:
        return re.sub(r"[^A-Za-z0-9_]", "_", str(name or "").strip()).upper()
    except Exception:
        return ""


def _expand_env_aliases(env_map: Dict[str, str], variables: Iterable[Mapping[str, Any]]) -> Dict[str, str]:
    expanded = dict(env_map or {})
    for var in variables:
        if not isinstance(var, Mapping):
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


def _extract_variable_default(var: Mapping[str, Any]) -> Any:
    for key in ("value", "default", "defaultValue", "default_value"):
        if key in var:
            val = var.get(key)
            return "" if val is None else val
    return ""


def _prepare_variable_context(
    doc_variables: Iterable[Mapping[str, Any]],
    overrides: Mapping[str, Any],
) -> Tuple[Dict[str, str], List[Dict[str, Any]], Dict[str, str]]:
    env_map: Dict[str, str] = {}
    variables: List[Dict[str, Any]] = []
    literal_lookup: Dict[str, str] = {}
    doc_names: Dict[str, bool] = {}

    overrides = dict(overrides or {})

    for var in doc_variables:
        if not isinstance(var, Mapping):
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
            variables.append(dict(var))

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


def _rewrite_powershell_script(content: str, literal_lookup: Mapping[str, str]) -> str:
    if not content or not literal_lookup:
        return content

    def _replace(match: re.Match[str]) -> str:
        name = match.group(2)
        canonical = _canonical_env_key(name)
        if not canonical:
            return match.group(0)
        literal = literal_lookup.get(canonical)
        if literal is None:
            return match.group(0)
        return literal

    return _ENV_VAR_PATTERN.sub(_replace, content)
