import os
import time
import json
import os
import base64
import re
import sqlite3
from typing import Any, Dict, List, Optional, Tuple, Callable

"""
Job Scheduler module for Borealis

Responsibilities:
- Maintain an internal clock/loop that evaluates scheduled jobs
- When a job's scheduled time arrives, mark it Running, then immediately Success (placeholder)
- Track simple run history in a dedicated table
- Expose job CRUD endpoints and computed scheduling metadata

This module registers its Flask routes against the provided app and uses
socketio.start_background_task to run the scheduler loop cooperatively
with eventlet.
"""


def _now_ts() -> int:
    return int(time.time())


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


def _decode_script_content(value: Any, encoding_hint: str = "") -> str:
    encoding = (encoding_hint or "").strip().lower()
    if isinstance(value, str):
        if encoding in ("base64", "b64", "base-64"):
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


def _extract_variable_default(var: Dict[str, Any]) -> Any:
    for key in ("value", "default", "defaultValue", "default_value"):
        if key in var:
            val = var.get(key)
            return "" if val is None else val
    return ""


def _prepare_variable_context(doc_variables: List[Dict[str, Any]], overrides: Dict[str, Any]):
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


def _rewrite_powershell_script(content: str, literal_lookup: Dict[str, str]) -> str:
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


def _parse_ts(val: Any) -> Optional[int]:
    """Best effort to parse ISO-ish datetime string or numeric seconds to epoch seconds."""
    if val is None:
        return None
    if isinstance(val, (int, float)):
        return int(val)
    try:
        from datetime import datetime
        s = str(val).strip().replace("Z", "+00:00")
        dt = datetime.fromisoformat(s)
        return int(dt.timestamp())
    except Exception:
        return None


def _parse_expiration(s: Optional[str]) -> Optional[int]:
    """Parse expiration shorthand to seconds.
    Examples: '30m' -> 1800, '1h' -> 3600, '2d' -> 172800.
    Returns None for 'no_expire' or invalid input.
    """
    if not s or s == "no_expire":
        return None
    try:
        s = s.strip().lower()
        unit = s[-1]
        value = int(s[:-1])
        if unit == 'm':
            return value * 60
        if unit == 'h':
            return value * 3600
        if unit == 'd':
            return value * 86400
        # Fallback: treat as minutes if only a number
        return int(s) * 60
    except Exception:
        return None


def _floor_minute(ts: int) -> int:
    ts = int(ts or 0)
    return ts - (ts % 60)


def _now_minute() -> int:
    return _floor_minute(_now_ts())


def _add_months(dt_tuple: Tuple[int, int, int, int, int, int], months: int = 1) -> int:
    """Advance a date by N months and return epoch seconds.
    dt_tuple = (year, month, day, hour, minute, second)
    Handles month-end clamping.
    """
    from calendar import monthrange
    from datetime import datetime

    y, m, d, hh, mm, ss = dt_tuple
    m2 = m + months
    y += (m2 - 1) // 12
    m2 = ((m2 - 1) % 12) + 1
    # Clamp day to last day of new month
    last_day = monthrange(y, m2)[1]
    d = min(d, last_day)
    try:
        return int(datetime(y, m2, d, hh, mm, ss).timestamp())
    except Exception:
        # Fallback to first of month if something odd
        return int(datetime(y, m2, 1, hh, mm, ss).timestamp())


def _add_years(dt_tuple: Tuple[int, int, int, int, int, int], years: int = 1) -> int:
    from datetime import datetime
    y, m, d, hh, mm, ss = dt_tuple
    y += years
    # Handle Feb 29 -> Feb 28 if needed
    try:
        return int(datetime(y, m, d, hh, mm, ss).timestamp())
    except Exception:
        # clamp day to 28
        d2 = 28 if (m == 2 and d > 28) else 1
        return int(datetime(y, m, d2, hh, mm, ss).timestamp())


def _to_dt_tuple(ts: int) -> Tuple[int, int, int, int, int, int]:
    from datetime import datetime
    dt = datetime.utcfromtimestamp(int(ts))
    return (dt.year, dt.month, dt.day, dt.hour, dt.minute, dt.second)


class JobScheduler:
    def __init__(self, app, socketio, db_path: str):
        self.app = app
        self.socketio = socketio
        self.db_path = db_path
        self._running = False
        # Simulated run duration to hold jobs in "Running" before Success
        self.SIMULATED_RUN_SECONDS = int(os.environ.get("BOREALIS_SIM_RUN_SECONDS", "30"))
        # Retention for run history (days)
        self.RETENTION_DAYS = int(os.environ.get("BOREALIS_JOB_HISTORY_DAYS", "30"))
        # Callback to retrieve current set of online hostnames
        self._online_lookup: Optional[Callable[[], List[str]]] = None

        # Ensure run-history table exists
        self._init_tables()

        # Bind routes
        self._register_routes()

    # ---------- Helpers for dispatching scripts ----------
    def _scripts_root(self) -> str:
        import os
        # Unified Assemblies root; script paths should include top-level
        # folder such as "Scripts" or "Ansible Playbooks".
        return os.path.abspath(
            os.path.join(os.path.dirname(__file__), "..", "..", "Assemblies")
        )

    def _is_valid_scripts_relpath(self, rel_path: str) -> bool:
        try:
            p = (rel_path or "").replace("\\", "/").lstrip("/")
            if not p:
                return False
            top = p.split("/", 1)[0]
            return top in ("Scripts",)
        except Exception:
            return False

    def _detect_script_type(self, filename: str) -> str:
        fn_lower = (filename or "").lower()
        if fn_lower.endswith(".json") and os.path.isfile(filename):
            try:
                with open(filename, "r", encoding="utf-8") as fh:
                    data = json.load(fh)
                if isinstance(data, dict):
                    typ = str(data.get("type") or data.get("script_type") or "").strip().lower()
                    if typ in ("powershell", "batch", "bash", "ansible"):
                        return typ
            except Exception:
                pass
            return "powershell"
        if fn_lower.endswith(".yml"):
            return "ansible"
        if fn_lower.endswith(".ps1"):
            return "powershell"
        if fn_lower.endswith(".bat"):
            return "batch"
        if fn_lower.endswith(".sh"):
            return "bash"
        return "unknown"

    def _load_assembly_document(self, abs_path: str, default_type: str) -> Dict[str, Any]:
        base_name = os.path.splitext(os.path.basename(abs_path))[0]
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
        if abs_path.lower().endswith(".json") and os.path.isfile(abs_path):
            try:
                with open(abs_path, "r", encoding="utf-8") as fh:
                    data = json.load(fh)
            except Exception:
                data = {}
            if isinstance(data, dict):
                doc["name"] = str(data.get("name") or doc["name"])
                doc["description"] = str(data.get("description") or "")
                cat = str(data.get("category") or doc["category"]).strip().lower()
                if cat in ("application", "script"):
                    doc["category"] = cat
                typ = str(data.get("type") or data.get("script_type") or default_type).strip().lower()
                if typ in ("powershell", "batch", "bash", "ansible"):
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
                else:
                    if isinstance(content_val, str):
                        doc["script"] = content_val
                encoding_hint = str(data.get("script_encoding") or data.get("scriptEncoding") or "").strip().lower()
                doc["script"] = _decode_script_content(doc.get("script"), encoding_hint)
                if encoding_hint in ("base64", "b64", "base-64"):
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
                for v in vars_in:
                    if not isinstance(v, dict):
                        continue
                    name = str(v.get("name") or v.get("key") or "").strip()
                    if not name:
                        continue
                    vtype = str(v.get("type") or "string").strip().lower()
                    if vtype not in ("string", "number", "boolean", "credential"):
                        vtype = "string"
                    doc["variables"].append({
                        "name": name,
                        "label": str(v.get("label") or ""),
                        "type": vtype,
                        "default": v.get("default", v.get("default_value")),
                        "required": bool(v.get("required")),
                        "description": str(v.get("description") or ""),
                    })
                files_in = data.get("files") if isinstance(data.get("files"), list) else []
                doc["files"] = []
                for f in files_in:
                    if not isinstance(f, dict):
                        continue
                    fname = f.get("file_name") or f.get("name")
                    if not fname or not isinstance(f.get("data"), str):
                        continue
                    try:
                        size_val = int(f.get("size") or 0)
                    except Exception:
                        size_val = 0
                    doc["files"].append({
                        "file_name": str(fname),
                        "size": size_val,
                        "mime_type": str(f.get("mime_type") or f.get("mimeType") or ""),
                        "data": f.get("data"),
                    })
            return doc
        try:
            with open(abs_path, "r", encoding="utf-8", errors="replace") as fh:
                content = fh.read()
        except Exception:
            content = ""
        normalized_script = (content or "").replace("\r\n", "\n")
        doc["script"] = normalized_script
        return doc

    def _ansible_root(self) -> str:
        import os
        return os.path.abspath(
            os.path.join(os.path.dirname(__file__), "..", "..", "Assemblies", "Ansible_Playbooks")
        )

    def _dispatch_ansible(self, hostname: str, rel_path: str, scheduled_job_id: int, scheduled_run_id: int) -> Optional[Dict[str, Any]]:
        try:
            import os, json, uuid
            ans_root = self._ansible_root()
            rel_norm = (rel_path or "").replace("\\", "/").lstrip("/")
            abs_path = os.path.abspath(os.path.join(ans_root, rel_norm))
            if (not abs_path.startswith(ans_root)) or (not os.path.isfile(abs_path)):
                return None
            doc = self._load_assembly_document(abs_path, "ansible")
            content = doc.get("script") or ""
            encoded_content = _encode_script_content(content)
            variables = doc.get("variables") or []
            files = doc.get("files") or []

            # Record in activity_history for UI parity
            now = _now_ts()
            act_id = None
            conn = self._conn()
            cur = conn.cursor()
            try:
                cur.execute(
                    """
                    INSERT INTO activity_history(hostname, script_path, script_name, script_type, ran_at, status, stdout, stderr)
                    VALUES(?,?,?,?,?,?,?,?)
                    """,
                    (
                        str(hostname),
                        rel_norm,
                        doc.get("name") or os.path.basename(abs_path),
                        "ansible",
                        now,
                        "Running",
                        "",
                        "",
                    ),
                )
                act_id = cur.lastrowid
                conn.commit()
            finally:
                conn.close()

            payload = {
                "run_id": uuid.uuid4().hex,
                "target_hostname": str(hostname),
                "playbook_name": doc.get("name") or os.path.basename(abs_path),
                "playbook_content": encoded_content,
                "playbook_encoding": "base64",
                "activity_job_id": act_id,
                "scheduled_job_id": int(scheduled_job_id),
                "scheduled_run_id": int(scheduled_run_id),
                "connection": "winrm",
                "variables": variables,
                "files": files,
            }
            try:
                self.socketio.emit("ansible_playbook_run", payload)
            except Exception:
                pass
            if act_id:
                return {
                    "activity_id": int(act_id),
                    "component_name": doc.get("name") or os.path.basename(abs_path),
                    "component_path": rel_norm,
                    "script_type": "ansible",
                    "component_kind": "ansible",
                }
            return None
        except Exception:
            pass

    def _dispatch_script(self, hostname: str, component: Dict[str, Any], run_mode: str) -> Optional[Dict[str, Any]]:
        """Emit a quick_job_run event to agents for the given script/host.
        Mirrors /api/scripts/quick_run behavior for scheduled jobs.
        """
        try:
            scripts_root = self._scripts_root()
            import os
            rel_path_raw = ""
            if isinstance(component, dict):
                rel_path_raw = str(component.get("path") or component.get("script_path") or "")
            else:
                rel_path_raw = str(component or "")
            path_norm = (rel_path_raw or "").replace("\\", "/").strip()
            if path_norm and not path_norm.startswith("Scripts/"):
                path_norm = f"Scripts/{path_norm}"
            abs_path = os.path.abspath(os.path.join(scripts_root, path_norm))
            if (not abs_path.startswith(scripts_root)) or (not self._is_valid_scripts_relpath(path_norm)) or (not os.path.isfile(abs_path)):
                return None
            doc = self._load_assembly_document(abs_path, "powershell")
            stype = (doc.get("type") or "powershell").lower()
            # For now, only PowerShell is supported by agents for scheduled jobs
            if stype != "powershell":
                return None
            content = doc.get("script") or ""
            doc_variables = doc.get("variables") if isinstance(doc.get("variables"), list) else []

            overrides: Dict[str, Any] = {}
            if isinstance(component, dict):
                if isinstance(component.get("variable_values"), dict):
                    for key, val in component.get("variable_values").items():
                        name = str(key or "").strip()
                        if name:
                            overrides[name] = val
                if isinstance(component.get("variables"), list):
                    for var in component.get("variables"):
                        if not isinstance(var, dict):
                            continue
                        name = str(var.get("name") or "").strip()
                        if not name:
                            continue
                        if "value" in var:
                            overrides[name] = var.get("value")

            env_map, variables, literal_lookup = _prepare_variable_context(doc_variables, overrides)
            content = _rewrite_powershell_script(content, literal_lookup)
            encoded_content = _encode_script_content(content)
            timeout_seconds = 0
            try:
                timeout_seconds = max(0, int(doc.get("timeout_seconds") or 0))
            except Exception:
                timeout_seconds = 0

            # Insert into activity_history for device for parity with Quick Job
            import sqlite3
            now = _now_ts()
            act_id = None
            conn = sqlite3.connect(self.db_path)
            cur = conn.cursor()
            try:
                cur.execute(
                    """
                    INSERT INTO activity_history(hostname, script_path, script_name, script_type, ran_at, status, stdout, stderr)
                    VALUES(?,?,?,?,?,?,?,?)
                    """,
                    (
                        str(hostname),
                        path_norm,
                        doc.get("name") or os.path.basename(abs_path),
                        stype,
                        now,
                        "Running",
                        "",
                        "",
                    ),
                )
                act_id = cur.lastrowid
                conn.commit()
            finally:
                conn.close()

            payload = {
                "job_id": act_id,
                "target_hostname": str(hostname),
                "script_type": stype,
                "script_name": doc.get("name") or os.path.basename(abs_path),
                "script_path": path_norm,
                "script_content": encoded_content,
                "script_encoding": "base64",
                "environment": env_map,
                "variables": variables,
                "timeout_seconds": timeout_seconds,
                "files": doc.get("files") or [],
                "run_mode": (run_mode or "system").strip().lower(),
                "admin_user": "",
                "admin_pass": "",
            }
            try:
                self.socketio.emit("quick_job_run", payload)
            except Exception:
                pass
            if act_id:
                return {
                    "activity_id": int(act_id),
                    "component_name": doc.get("name") or os.path.basename(abs_path),
                    "component_path": path_norm,
                    "script_type": stype,
                    "component_kind": "script",
                }
            return None
        except Exception:
            # Keep scheduler resilient
            pass
        return None

    # ---------- DB helpers ----------
    def _conn(self):
        return sqlite3.connect(self.db_path)

    def _init_tables(self):
        conn = self._conn()
        cur = conn.cursor()
        # Runs table captures each firing
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS scheduled_job_runs (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                job_id INTEGER NOT NULL,
                scheduled_ts INTEGER,
                started_ts INTEGER,
                finished_ts INTEGER,
                status TEXT,
                error TEXT,
                created_at INTEGER,
                updated_at INTEGER,
                FOREIGN KEY(job_id) REFERENCES scheduled_jobs(id) ON DELETE CASCADE
            )
            """
        )
        # Add per-target column if missing
        try:
            cur.execute("PRAGMA table_info(scheduled_job_runs)")
            cols = {row[1] for row in cur.fetchall()}
            if 'target_hostname' not in cols:
                cur.execute("ALTER TABLE scheduled_job_runs ADD COLUMN target_hostname TEXT")
        except Exception:
            pass
        # Helpful index for lookups
        try:
            cur.execute("CREATE INDEX IF NOT EXISTS idx_runs_job_sched_target ON scheduled_job_runs(job_id, scheduled_ts, target_hostname)")
        except Exception:
            pass
        try:
            cur.execute(
                """
                CREATE TABLE IF NOT EXISTS scheduled_job_run_activity (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    run_id INTEGER NOT NULL,
                    activity_id INTEGER NOT NULL,
                    component_kind TEXT,
                    script_type TEXT,
                    component_path TEXT,
                    component_name TEXT,
                    created_at INTEGER,
                    FOREIGN KEY(run_id) REFERENCES scheduled_job_runs(id) ON DELETE CASCADE,
                    FOREIGN KEY(activity_id) REFERENCES activity_history(id) ON DELETE CASCADE
                )
                """
            )
            cur.execute("CREATE INDEX IF NOT EXISTS idx_run_activity_run ON scheduled_job_run_activity(run_id)")
            cur.execute("CREATE UNIQUE INDEX IF NOT EXISTS idx_run_activity_activity ON scheduled_job_run_activity(activity_id)")
        except Exception:
            pass
        conn.commit()
        conn.close()

    # ---------- Scheduling core ----------
    def _get_last_run(self, job_id: int) -> Optional[Dict[str, Any]]:
        conn = self._conn()
        cur = conn.cursor()
        cur.execute(
            "SELECT id, scheduled_ts, started_ts, finished_ts, status FROM scheduled_job_runs WHERE job_id=? ORDER BY COALESCE(started_ts, scheduled_ts, 0) DESC, id DESC LIMIT 1",
            (job_id,)
        )
        row = cur.fetchone()
        conn.close()
        if not row:
            return None
        return {
            "id": row[0],
            "scheduled_ts": row[1],
            "started_ts": row[2],
            "finished_ts": row[3],
            "status": row[4] or "",
        }

    def _compute_next_run(self, schedule_type: str, start_ts: Optional[int], last_run_ts: Optional[int], now_ts: int) -> Optional[int]:
        st = (schedule_type or "immediately").strip().lower()
        start_ts = _floor_minute(start_ts) if start_ts else None
        last_run_ts = _floor_minute(last_run_ts) if last_run_ts else None
        now_ts = _floor_minute(now_ts)
        if st == "immediately":
            # Run once asap if never ran
            return None if last_run_ts else now_ts
        if st == "once":
            if not start_ts:
                return None
            # If never ran and time in future/now
            if not last_run_ts:
                return start_ts
            return None
        if not start_ts:
            return None

        # For recurring types, base off start_ts and last_run_ts
        last = last_run_ts if last_run_ts else None
        # Minute/Hour intervals
        if st in ("every_5_minutes", "every_10_minutes", "every_15_minutes", "every_30_minutes", "every_hour"):
            period_map = {
                "every_5_minutes": 5 * 60,
                "every_10_minutes": 10 * 60,
                "every_15_minutes": 15 * 60,
                "every_30_minutes": 30 * 60,
                "every_hour": 60 * 60,
            }
            period = period_map.get(st)
            candidate = (last + period) if last else start_ts
            while candidate is not None and candidate <= now_ts - 1:
                candidate += period
            return candidate
        if st == "daily":
            period = 86400
            candidate = last + period if last else start_ts
            while candidate is not None and candidate <= now_ts - 1:
                candidate += period
            return candidate
        if st == "weekly":
            period = 7 * 86400
            candidate = last + period if last else start_ts
            while candidate is not None and candidate <= now_ts - 1:
                candidate += period
            return candidate
        if st == "monthly":
            base = _to_dt_tuple(last) if last else _to_dt_tuple(start_ts)
            candidate = _add_months(base, 1 if last else 0)
            while candidate <= now_ts - 1:
                base = _to_dt_tuple(candidate)
                candidate = _add_months(base, 1)
            return candidate
        if st == "yearly":
            base = _to_dt_tuple(last) if last else _to_dt_tuple(start_ts)
            candidate = _add_years(base, 1 if last else 0)
            while candidate <= now_ts - 1:
                base = _to_dt_tuple(candidate)
                candidate = _add_years(base, 1)
            return candidate
        return None

    def _should_expire(self, started_ts: Optional[int], expiration: Optional[str], now_ts: int) -> bool:
        if not started_ts:
            return False
        seconds = _parse_expiration(expiration)
        if not seconds:
            return False
        return (started_ts + seconds) <= now_ts

    def _tick_once(self):
        """Evaluate all enabled scheduled jobs and trigger those due.
        Placeholder execution: mark Running then Success immediately.
        """
        now = _now_ts()
        conn = self._conn()
        cur = conn.cursor()
        # First, mark any stale Running runs that exceeded job expiration as Timed Out
        try:
            cur.execute(
                """
                SELECT r.id, r.started_ts, j.expiration
                FROM scheduled_job_runs r
                JOIN scheduled_jobs j ON j.id = r.job_id
                WHERE r.status = 'Running'
                """
            )
            rows = cur.fetchall()
            for rid, started_ts, expiration in rows:
                if self._should_expire(started_ts, expiration, now):
                    try:
                        c2 = conn.cursor()
                        c2.execute(
                            "UPDATE scheduled_job_runs SET status='Timed Out', updated_at=? WHERE id=?",
                            (now, rid),
                        )
                        conn.commit()
                    except Exception:
                        pass
        except Exception:
            pass

        # Next, finalize any Running runs that have passed the simulated duration
        try:
            cur.execute(
                "SELECT id, started_ts FROM scheduled_job_runs WHERE status='Running'"
            )
            rows = cur.fetchall()
            for rid, started_ts in rows:
                if started_ts and (int(started_ts) + self.SIMULATED_RUN_SECONDS) <= now:
                    try:
                        c2 = conn.cursor()
                        c2.execute(
                            "UPDATE scheduled_job_runs SET finished_ts=?, status='Success', updated_at=? WHERE id=?",
                            (now, now, rid),
                        )
                        conn.commit()
                    except Exception:
                        pass
        except Exception:
            pass

        # Finally, rotate history older than the retention window
        try:
            cutoff = now - (self.RETENTION_DAYS * 86400)
            cur.execute(
                "DELETE FROM scheduled_job_runs WHERE COALESCE(finished_ts, started_ts, scheduled_ts, 0) < ?",
                (cutoff,)
            )
            conn.commit()
        except Exception:
            pass
        try:
            cur.execute(
                "SELECT id, components_json, targets_json, schedule_type, start_ts, expiration, execution_context, created_at FROM scheduled_jobs WHERE enabled=1 ORDER BY id ASC"
            )
            jobs = cur.fetchall()
        except Exception:
            jobs = []
        conn.close()

        # Online hostnames snapshot for this tick
        online = set()
        try:
            if callable(self._online_lookup):
                online = set(self._online_lookup() or [])
        except Exception:
            online = set()

        five_min = 300
        now_min = _now_minute()

        for (job_id, components_json, targets_json, schedule_type, start_ts, expiration, execution_context, created_at) in jobs:
            try:
                # Targets list for this job
                try:
                    targets = json.loads(targets_json or "[]")
                except Exception:
                    targets = []
                targets = [str(t) for t in targets if isinstance(t, (str, int))]
                total_targets = len(targets)

                # Determine scripts to run for this job (first-pass: all 'script' components)
                try:
                    comps = json.loads(components_json or "[]")
                except Exception:
                    comps = []
                script_components = []
                ansible_paths = []
                for c in comps:
                    try:
                        ctype = (c or {}).get("type")
                        if ctype == "script":
                            p = (c.get("path") or c.get("script_path") or "").strip()
                            if p:
                                comp_copy = dict(c)
                                comp_copy["path"] = p
                                script_components.append(comp_copy)
                        elif ctype == "ansible":
                            p = (c.get("path") or "").strip()
                            if p:
                                ansible_paths.append(p)
                    except Exception:
                        continue
                run_mode = (execution_context or "system").strip().lower()

                exp_seconds = _parse_expiration(expiration)

                # Determine current occurrence to work on
                occ = None
                try:
                    conn2 = self._conn()
                    c2 = conn2.cursor()
                    c2.execute("SELECT MAX(scheduled_ts) FROM scheduled_job_runs WHERE job_id=?", (job_id,))
                    occ_from_runs = c2.fetchone()[0]
                    conn2.close()
                except Exception:
                    occ_from_runs = None

                if occ_from_runs:
                    occ = int(occ_from_runs)
                    # Check if occurrence is complete (terminal status per target)
                    try:
                        conn2 = self._conn()
                        c2 = conn2.cursor()
                        c2.execute(
                            "SELECT COUNT(DISTINCT target_hostname) FROM scheduled_job_runs WHERE job_id=? AND scheduled_ts=? AND status IN ('Success','Expired','Timed Out')",
                            (job_id, occ)
                        )
                        done_count = int(c2.fetchone()[0] or 0)
                        conn2.close()
                    except Exception:
                        done_count = 0

                    if total_targets > 0 and done_count >= total_targets:
                        # Move to next occurrence if due in window
                        nxt = self._compute_next_run(schedule_type, start_ts, occ, now_min)
                        if nxt is not None and now_min >= nxt and (now_min - nxt) <= five_min:
                            occ = nxt
                        else:
                            # Nothing to do this tick for this job
                            continue
                    else:
                        # Continue working on this occurrence regardless of the 5-minute window until expiration
                        pass
                else:
                    # No occurrence yet; derive initial occurrence
                    if (schedule_type or '').lower() == 'immediately':
                        occ = _floor_minute(created_at or now_min)
                    else:
                        st_min = _floor_minute(start_ts) if start_ts else None
                        if st_min is None:
                            # Try compute_next_run to derive first planned occurrence
                            occ = self._compute_next_run(schedule_type, start_ts, None, now_min)
                        else:
                            occ = st_min
                    if occ is None:
                        continue
                    # For first occurrence, require it be within the 5-minute window to trigger
                    if not (now_min >= occ and (now_min - occ) <= five_min):
                        continue

                # For each target, if no run exists for this occurrence, either start it (if online) or keep waiting/expire
                for host in targets:
                    # Skip if a run already exists for this job/host/occurrence
                    try:
                        conn2 = self._conn()
                        c2 = conn2.cursor()
                        c2.execute(
                            "SELECT id, status, started_ts FROM scheduled_job_runs WHERE job_id=? AND target_hostname=? AND scheduled_ts=? ORDER BY id DESC LIMIT 1",
                            (job_id, host, occ)
                        )
                        row = c2.fetchone()
                    except Exception:
                        row = None
                    if row:
                        # Existing record - if Running, timeout handled earlier; skip
                        conn2.close()
                        continue

                    # Start if online; otherwise, wait until expiration
                    if host in online:
                        try:
                            ts_now = _now_ts()
                            c2.execute(
                                "INSERT INTO scheduled_job_runs (job_id, target_hostname, scheduled_ts, started_ts, status, created_at, updated_at) VALUES (?,?,?,?,?,?,?)",
                                (job_id, host, occ, ts_now, "Running", ts_now, ts_now),
                            )
                            run_row_id = c2.lastrowid or 0
                            conn2.commit()
                            activity_links: List[Dict[str, Any]] = []
                            # Dispatch all script components for this job to the target host
                            for comp in script_components:
                                try:
                                    link = self._dispatch_script(host, comp, run_mode)
                                    if link and link.get("activity_id"):
                                        activity_links.append({
                                            "run_id": run_row_id,
                                            "activity_id": int(link["activity_id"]),
                                            "component_kind": link.get("component_kind") or "script",
                                            "script_type": link.get("script_type") or "powershell",
                                            "component_path": link.get("component_path") or "",
                                            "component_name": link.get("component_name") or "",
                                        })
                                except Exception:
                                    continue
                            # Dispatch ansible playbooks for this job to the target host
                            for ap in ansible_paths:
                                try:
                                    link = self._dispatch_ansible(host, ap, job_id, run_row_id)
                                    if link and link.get("activity_id"):
                                        activity_links.append({
                                            "run_id": run_row_id,
                                            "activity_id": int(link["activity_id"]),
                                            "component_kind": link.get("component_kind") or "ansible",
                                            "script_type": link.get("script_type") or "ansible",
                                            "component_path": link.get("component_path") or "",
                                            "component_name": link.get("component_name") or "",
                                        })
                                except Exception:
                                    continue
                            if activity_links:
                                try:
                                    for link in activity_links:
                                        c2.execute(
                                            "INSERT OR IGNORE INTO scheduled_job_run_activity(run_id, activity_id, component_kind, script_type, component_path, component_name, created_at) VALUES (?,?,?,?,?,?,?)",
                                            (
                                                int(link["run_id"]),
                                                int(link["activity_id"]),
                                                link.get("component_kind") or "",
                                                link.get("script_type") or "",
                                                link.get("component_path") or "",
                                                link.get("component_name") or "",
                                                ts_now,
                                            ),
                                        )
                                    conn2.commit()
                                except Exception:
                                    pass
                        except Exception:
                            pass
                        finally:
                            conn2.close()
                    else:
                        # If expired window is configured and has passed, mark this host as Expired
                        if exp_seconds is not None and (occ + exp_seconds) <= now:
                            try:
                                ts_now = _now_ts()
                                c2.execute(
                                    "INSERT INTO scheduled_job_runs (job_id, target_hostname, scheduled_ts, finished_ts, status, error, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?)",
                                    (job_id, host, occ, ts_now, "Expired", "Device offline", ts_now, ts_now),
                                )
                                conn2.commit()
                            except Exception:
                                pass
                            finally:
                                conn2.close()
                        else:
                            # keep waiting; no record created yet
                            try:
                                conn2.close()
                            except Exception:
                                pass
            except Exception:
                # Keep loop resilient
                continue

    def start(self):
        if self._running:
            return
        self._running = True

        def _loop():
            # cooperative loop aligned to minutes
            while self._running:
                try:
                    self._tick_once()
                except Exception:
                    pass
                # Sleep until next minute boundary
                delay = 60 - (_now_ts() % 60)
                try:
                    import eventlet
                    eventlet.sleep(delay)
                except Exception:
                    time.sleep(delay)

        # Use SocketIO helper so it integrates with eventlet
        try:
            self.socketio.start_background_task(_loop)
        except Exception:
            # Fallback to thread
            import threading
            threading.Thread(target=_loop, daemon=True).start()

    # ---------- Route registration ----------
    def _register_routes(self):
        app = self.app

        # Utility: job row
        def _job_row_to_dict(r) -> Dict[str, Any]:
            base = {
                "id": r[0],
                "name": r[1],
                "components": json.loads(r[2] or "[]"),
                "targets": json.loads(r[3] or "[]"),
                "schedule_type": r[4] or "immediately",
                "start_ts": r[5],
                "duration_stop_enabled": bool(r[6] or 0),
                "expiration": r[7] or "no_expire",
                "execution_context": r[8] or "system",
                "enabled": bool(r[9] or 0),
                "created_at": r[10] or 0,
                "updated_at": r[11] or 0,
            }
            # Attach computed status summary for latest occurrence
            try:
                conn = self._conn()
                c = conn.cursor()
                c.execute("SELECT MAX(scheduled_ts) FROM scheduled_job_runs WHERE job_id=?", (base["id"],))
                max_occ = c.fetchone()[0]
                summary_status = None
                last_run_ts = None
                result_counts = {
                    "pending": 0,
                    "running": 0,
                    "success": 0,
                    "failed": 0,
                    "expired": 0,
                    "timed_out": 0,
                    "total_targets": len(base.get("targets") or []),
                }
                if max_occ:
                    # Summarize statuses for this occurrence
                    c.execute(
                        "SELECT status, COUNT(*) FROM scheduled_job_runs WHERE job_id=? AND scheduled_ts=? GROUP BY status",
                        (base["id"], max_occ)
                    )
                    counts = {row[0] or "": int(row[1] or 0) for row in c.fetchall()}
                    result_counts["running"] = counts.get("Running", 0)
                    result_counts["success"] = counts.get("Success", 0)
                    result_counts["failed"] = counts.get("Failed", 0)
                    result_counts["expired"] = counts.get("Expired", 0)
                    result_counts["timed_out"] = counts.get("Timed Out", 0)
                    computed = result_counts["running"] + result_counts["success"] + result_counts["failed"] + result_counts["expired"] + result_counts["timed_out"]
                    pend = (result_counts["total_targets"] or 0) - computed
                    result_counts["pending"] = pend if pend > 0 else 0
                    # Priority: Running > Timed Out > Expired > Success
                    if counts.get("Running"):
                        summary_status = "Running"
                    elif counts.get("Timed Out"):
                        summary_status = "Timed Out"
                    elif counts.get("Expired"):
                        summary_status = "Expired"
                    elif counts.get("Success"):
                        summary_status = "Success"
                    last_run_ts = int(max_occ)
                conn.close()
            except Exception:
                summary_status = None
                last_run_ts = None
                result_counts = {
                    "pending": len(base.get("targets") or []),
                    "running": 0,
                    "success": 0,
                    "failed": 0,
                    "expired": 0,
                    "timed_out": 0,
                    "total_targets": len(base.get("targets") or []),
                }
            base["last_run_ts"] = last_run_ts
            base["last_status"] = summary_status or ("Scheduled" if base.get("start_ts") else "")
            base["latest_occurrence"] = last_run_ts
            base["result_counts"] = result_counts
            try:
                base["next_run_ts"] = self._compute_next_run(
                    base["schedule_type"], base.get("start_ts"), base.get("last_run_ts"), _now_ts()
                )
            except Exception:
                base["next_run_ts"] = None
            return base

        @app.route("/api/scheduled_jobs", methods=["GET"])
        def api_scheduled_jobs_list():
            try:
                conn = self._conn()
                cur = conn.cursor()
                cur.execute(
                    """
                    SELECT id, name, components_json, targets_json, schedule_type, start_ts,
                           duration_stop_enabled, expiration, execution_context, enabled, created_at, updated_at
                    FROM scheduled_jobs
                    ORDER BY created_at DESC
                    """
                )
                rows = [ _job_row_to_dict(r) for r in cur.fetchall() ]
                conn.close()
                return json.dumps({"jobs": rows}), 200, {"Content-Type": "application/json"}
            except Exception as e:
                return json.dumps({"error": str(e)}), 500, {"Content-Type": "application/json"}

        @app.route("/api/scheduled_jobs", methods=["POST"])
        def api_scheduled_jobs_create():
            data = self._json_body()
            name = (data.get("name") or "").strip()
            components = data.get("components") or []
            targets = data.get("targets") or []
            schedule_type = (data.get("schedule", {}).get("type") or data.get("schedule_type") or "immediately").strip().lower()
            start = data.get("schedule", {}).get("start") or data.get("start") or None
            start_ts = _parse_ts(start) if start else None
            duration_stop_enabled = int(bool((data.get("duration") or {}).get("stopAfterEnabled") or data.get("duration_stop_enabled")))
            expiration = (data.get("duration") or {}).get("expiration") or data.get("expiration") or "no_expire"
            execution_context = (data.get("execution_context") or "system").strip().lower()
            enabled = int(bool(data.get("enabled", True)))
            if not name or not components or not targets:
                return json.dumps({"error": "name, components, targets required"}), 400, {"Content-Type": "application/json"}
            now = _now_ts()
            try:
                conn = self._conn()
                cur = conn.cursor()
                cur.execute(
                    """
                    INSERT INTO scheduled_jobs
                    (name, components_json, targets_json, schedule_type, start_ts, duration_stop_enabled, expiration, execution_context, enabled, created_at, updated_at)
                    VALUES (?,?,?,?,?,?,?,?,?,?,?)
                    """,
                    (
                        name,
                        json.dumps(components),
                        json.dumps(targets),
                        schedule_type,
                        start_ts,
                        duration_stop_enabled,
                        expiration,
                        execution_context,
                        enabled,
                        now,
                        now,
                    ),
                )
                job_id = cur.lastrowid
                conn.commit()
                cur.execute(
                    """
                    SELECT id, name, components_json, targets_json, schedule_type, start_ts,
                           duration_stop_enabled, expiration, execution_context, enabled, created_at, updated_at
                    FROM scheduled_jobs WHERE id=?
                    """,
                    (job_id,),
                )
                row = cur.fetchone()
                conn.close()
                return json.dumps({"job": _job_row_to_dict(row)}), 200, {"Content-Type": "application/json"}
            except Exception as e:
                return json.dumps({"error": str(e)}), 500, {"Content-Type": "application/json"}

        @app.route("/api/scheduled_jobs/<int:job_id>", methods=["GET"])
        def api_scheduled_jobs_get(job_id: int):
            try:
                conn = self._conn()
                cur = conn.cursor()
                cur.execute(
                    """
                    SELECT id, name, components_json, targets_json, schedule_type, start_ts,
                           duration_stop_enabled, expiration, execution_context, enabled, created_at, updated_at
                    FROM scheduled_jobs WHERE id=?
                    """,
                    (job_id,),
                )
                row = cur.fetchone()
                conn.close()
                if not row:
                    return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                return json.dumps({"job": _job_row_to_dict(row)}), 200, {"Content-Type": "application/json"}
            except Exception as e:
                return json.dumps({"error": str(e)}), 500, {"Content-Type": "application/json"}

        @app.route("/api/scheduled_jobs/<int:job_id>", methods=["PUT"])
        def api_scheduled_jobs_update(job_id: int):
            data = self._json_body()
            fields: Dict[str, Any] = {}
            if "name" in data:
                fields["name"] = (data.get("name") or "").strip()
            if "components" in data:
                fields["components_json"] = json.dumps(data.get("components") or [])
            if "targets" in data:
                fields["targets_json"] = json.dumps(data.get("targets") or [])
            if "schedule" in data or "schedule_type" in data:
                schedule_type = (data.get("schedule", {}).get("type") or data.get("schedule_type") or "immediately").strip().lower()
                fields["schedule_type"] = schedule_type
                start = data.get("schedule", {}).get("start") or data.get("start") or None
                fields["start_ts"] = _parse_ts(start) if start else None
            if "duration" in data or "duration_stop_enabled" in data:
                fields["duration_stop_enabled"] = int(bool((data.get("duration") or {}).get("stopAfterEnabled") or data.get("duration_stop_enabled")))
            if "expiration" in data or (data.get("duration") and "expiration" in data.get("duration")):
                fields["expiration"] = (data.get("duration") or {}).get("expiration") or data.get("expiration") or "no_expire"
            if "execution_context" in data:
                fields["execution_context"] = (data.get("execution_context") or "system").strip().lower()
            if "enabled" in data:
                fields["enabled"] = int(bool(data.get("enabled")))
            if not fields:
                return json.dumps({"error": "no fields to update"}), 400, {"Content-Type": "application/json"}
            try:
                conn = self._conn()
                cur = conn.cursor()
                sets = ", ".join([f"{k}=?" for k in fields.keys()])
                params = list(fields.values()) + [_now_ts(), job_id]
                cur.execute(f"UPDATE scheduled_jobs SET {sets}, updated_at=? WHERE id=?", params)
                if cur.rowcount == 0:
                    conn.close()
                    return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                conn.commit()
                cur.execute(
                    """
                    SELECT id, name, components_json, targets_json, schedule_type, start_ts,
                           duration_stop_enabled, expiration, execution_context, enabled, created_at, updated_at
                    FROM scheduled_jobs WHERE id=?
                    """,
                    (job_id,),
                )
                row = cur.fetchone()
                conn.close()
                return json.dumps({"job": _job_row_to_dict(row)}), 200, {"Content-Type": "application/json"}
            except Exception as e:
                return json.dumps({"error": str(e)}), 500, {"Content-Type": "application/json"}

        @app.route("/api/scheduled_jobs/<int:job_id>/toggle", methods=["POST"])
        def api_scheduled_jobs_toggle(job_id: int):
            data = self._json_body()
            enabled = int(bool(data.get("enabled", True)))
            try:
                conn = self._conn()
                cur = conn.cursor()
                cur.execute("UPDATE scheduled_jobs SET enabled=?, updated_at=? WHERE id=?", (enabled, _now_ts(), job_id))
                if cur.rowcount == 0:
                    conn.close()
                    return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                conn.commit()
                cur.execute(
                    "SELECT id, name, components_json, targets_json, schedule_type, start_ts, duration_stop_enabled, expiration, execution_context, enabled, created_at, updated_at FROM scheduled_jobs WHERE id=?",
                    (job_id,),
                )
                row = cur.fetchone()
                conn.close()
                return json.dumps({"job": _job_row_to_dict(row)}), 200, {"Content-Type": "application/json"}
            except Exception as e:
                return json.dumps({"error": str(e)}), 500, {"Content-Type": "application/json"}

        @app.route("/api/scheduled_jobs/<int:job_id>", methods=["DELETE"])
        def api_scheduled_jobs_delete(job_id: int):
            try:
                conn = self._conn()
                cur = conn.cursor()
                cur.execute("DELETE FROM scheduled_jobs WHERE id=?", (job_id,))
                deleted = cur.rowcount
                conn.commit()
                conn.close()
                if deleted == 0:
                    return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                return json.dumps({"status": "ok"}), 200, {"Content-Type": "application/json"}
            except Exception as e:
                return json.dumps({"error": str(e)}), 500, {"Content-Type": "application/json"}

        @app.route("/api/scheduled_jobs/<int:job_id>/runs", methods=["GET"])
        def api_scheduled_job_runs(job_id: int):
            try:
                from flask import request
                days = request.args.get('days', default=str(self.RETENTION_DAYS))
                try:
                    days = max(1, int(days))
                except Exception:
                    days = self.RETENTION_DAYS
                cutoff = _now_ts() - (days * 86400)

                conn = self._conn()
                cur = conn.cursor()
                cur.execute(
                    """
                    SELECT id, scheduled_ts, started_ts, finished_ts, status, error
                    FROM scheduled_job_runs
                    WHERE job_id=? AND COALESCE(finished_ts, started_ts, scheduled_ts, 0) >= ?
                    ORDER BY COALESCE(started_ts, scheduled_ts, 0) DESC, id DESC
                    LIMIT 500
                    """,
                    (job_id, cutoff)
                )
                rows = cur.fetchall()
                conn.close()
                runs = [
                    {
                        "id": r[0],
                        "scheduled_ts": r[1],
                        "started_ts": r[2],
                        "finished_ts": r[3],
                        "status": r[4] or "",
                        "error": r[5] or "",
                    }
                    for r in rows
                ]
                return json.dumps({"runs": runs}), 200, {"Content-Type": "application/json"}
            except Exception as e:
                return json.dumps({"error": str(e)}), 500, {"Content-Type": "application/json"}

        @app.route("/api/scheduled_jobs/<int:job_id>/devices", methods=["GET"])
        def api_scheduled_job_devices(job_id: int):
            """Return per-target status for the latest occurrence (or specified via ?occurrence=epoch)."""
            try:
                from flask import request
                occurrence = request.args.get('occurrence')
                occ = int(occurrence) if occurrence else None

                conn = self._conn()
                cur = conn.cursor()
                cur.execute(
                    "SELECT targets_json FROM scheduled_jobs WHERE id=?",
                    (job_id,)
                )
                row = cur.fetchone()
                if not row:
                    conn.close()
                    return json.dumps({"error": "not found"}), 404, {"Content-Type": "application/json"}
                try:
                    targets = json.loads(row[0] or "[]")
                except Exception:
                    targets = []
                targets = [str(t) for t in targets if isinstance(t, (str, int))]

                # Determine occurrence if not provided
                if occ is None:
                    cur.execute("SELECT MAX(scheduled_ts) FROM scheduled_job_runs WHERE job_id=?", (job_id,))
                    occ_row = cur.fetchone()
                    occ = int(occ_row[0]) if occ_row and occ_row[0] else None

                # Sites lookup
                site_by_host: Dict[str, str] = {}
                try:
                    cur.execute(
                        "SELECT device_hostname, sites.name FROM device_sites JOIN sites ON sites.id = device_sites.site_id"
                    )
                    for h, n in cur.fetchall():
                        site_by_host[str(h)] = n or ""
                except Exception:
                    pass

                # Status per target for occurrence
                run_by_host: Dict[str, Dict[str, Any]] = {}
                run_ids: List[int] = []
                if occ is not None:
                    try:
                        cur.execute(
                            "SELECT id, target_hostname, status, started_ts, finished_ts FROM scheduled_job_runs WHERE job_id=? AND scheduled_ts=? ORDER BY id DESC",
                            (job_id, occ)
                        )
                        rows = cur.fetchall()
                        for rid, h, st, st_ts, fin_ts in rows:
                            h = str(h)
                            if h not in run_by_host:
                                run_by_host[h] = {
                                    "status": st or "",
                                    "started_ts": st_ts,
                                    "finished_ts": fin_ts,
                                    "run_id": int(rid),
                                }
                                run_ids.append(int(rid))
                    except Exception:
                        pass

                activities_by_run: Dict[int, List[Dict[str, Any]]] = {}
                if run_ids:
                    try:
                        placeholders = ",".join(["?"] * len(run_ids))
                        cur.execute(
                            f"""
                            SELECT
                                s.run_id,
                                s.activity_id,
                                s.component_kind,
                                s.script_type,
                                s.component_path,
                                s.component_name,
                                COALESCE(LENGTH(h.stdout), 0),
                                COALESCE(LENGTH(h.stderr), 0)
                            FROM scheduled_job_run_activity s
                            LEFT JOIN activity_history h ON h.id = s.activity_id
                            WHERE s.run_id IN ({placeholders})
                            """,
                            run_ids,
                        )
                        for rid, act_id, kind, stype, path, name, so_len, se_len in cur.fetchall():
                            rid = int(rid)
                            entry = {
                                "activity_id": int(act_id),
                                "component_kind": kind or "",
                                "script_type": stype or "",
                                "component_path": path or "",
                                "component_name": name or "",
                                "has_stdout": bool(so_len),
                                "has_stderr": bool(se_len),
                            }
                            activities_by_run.setdefault(rid, []).append(entry)
                    except Exception:
                        pass

                conn.close()

                # Online snapshot
                online = set()
                try:
                    if callable(self._online_lookup):
                        online = set(self._online_lookup() or [])
                except Exception:
                    online = set()

                out = []
                for host in targets:
                    rec = run_by_host.get(str(host), {})
                    job_status = rec.get("status") or "Pending"
                    ran_on = rec.get("started_ts") or rec.get("finished_ts")
                    activities = activities_by_run.get(rec.get("run_id", 0) or 0, [])
                    has_stdout = any(a.get("has_stdout") for a in activities)
                    has_stderr = any(a.get("has_stderr") for a in activities)
                    out.append({
                        "hostname": str(host),
                        "online": str(host) in online,
                        "site": site_by_host.get(str(host), ""),
                        "ran_on": ran_on,
                        "job_status": job_status,
                        "has_stdout": has_stdout,
                        "has_stderr": has_stderr,
                        "activities": activities,
                    })

                return json.dumps({"occurrence": occ, "devices": out}), 200, {"Content-Type": "application/json"}
            except Exception as e:
                return json.dumps({"error": str(e)}), 500, {"Content-Type": "application/json"}

        @app.route("/api/scheduled_jobs/<int:job_id>/runs", methods=["DELETE"])
        def api_scheduled_job_runs_clear(job_id: int):
            """Clear all historical runs for a job except the most recent occurrence.

            We keep all rows that belong to the latest occurrence (by scheduled_ts)
            and delete everything older. If there is no occurrence, no-op.
            """
            try:
                conn = self._conn()
                cur = conn.cursor()
                # Determine latest occurrence for this job
                cur.execute(
                    "SELECT MAX(scheduled_ts) FROM scheduled_job_runs WHERE job_id=?",
                    (job_id,)
                )
                row = cur.fetchone()
                latest = int(row[0]) if row and row[0] is not None else None

                if latest is None:
                    # Nothing to clear
                    conn.close()
                    return json.dumps({"status": "ok", "cleared": 0}), 200, {"Content-Type": "application/json"}

                # Delete all runs for older occurrences
                cur.execute(
                    "DELETE FROM scheduled_job_runs WHERE job_id=? AND COALESCE(scheduled_ts, 0) < ?",
                    (job_id, latest),
                )
                cleared = cur.rowcount or 0
                conn.commit()
                conn.close()
                return json.dumps({"status": "ok", "cleared": int(cleared), "kept_occurrence": latest}), 200, {"Content-Type": "application/json"}
            except Exception as e:
                return json.dumps({"error": str(e)}), 500, {"Content-Type": "application/json"}

    # ---------- Request helpers ----------
    def _json_body(self) -> Dict[str, Any]:
        try:
            from flask import request
            return request.get_json(silent=True) or {}
        except Exception:
            return {}


def register(app, socketio, db_path: str) -> JobScheduler:
    """Factory to create and return a JobScheduler instance."""
    return JobScheduler(app, socketio, db_path)


def set_online_lookup(scheduler: JobScheduler, fn: Callable[[], List[str]]):
    scheduler._online_lookup = fn
