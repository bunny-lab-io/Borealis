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
import os
import time
from typing import TYPE_CHECKING, Any, Dict, List

from flask import Blueprint, jsonify, request

from ..scheduled_jobs.management import ensure_scheduler, get_scheduler

if TYPE_CHECKING:  # pragma: no cover - typing aide
    from flask import Flask

    from .. import EngineServiceAdapters


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

    ensure_scheduler(app, adapters)
    blueprint = Blueprint("assemblies_execution", __name__)
    service_log = adapters.service_log

    @blueprint.route("/api/scripts/quick_run", methods=["POST"])
    def scripts_quick_run():
        scheduler = get_scheduler(adapters)
        data = request.get_json(silent=True) or {}
        rel_path = (data.get("script_path") or "").strip()
        hostnames = _normalize_hostnames(data.get("hostnames"))
        run_mode = (data.get("run_mode") or "system").strip().lower()

        if not rel_path or not hostnames:
            return jsonify({"error": "Missing script_path or hostnames[]"}), 400

        scripts_root = scheduler._scripts_root()  # type: ignore[attr-defined]
        abs_path = os.path.abspath(os.path.join(scripts_root, rel_path))
        if (
            not abs_path.startswith(scripts_root)
            or not scheduler._is_valid_scripts_relpath(rel_path)  # type: ignore[attr-defined]
            or not os.path.isfile(abs_path)
        ):
            return jsonify({"error": "Script not found"}), 404

        doc = scheduler._load_assembly_document(abs_path, "scripts")  # type: ignore[attr-defined]
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

        env_map, variables, literal_lookup = scheduler._prepare_variable_context(doc_variables, overrides)  # type: ignore[attr-defined]
        content = scheduler._rewrite_powershell_script(content, literal_lookup)  # type: ignore[attr-defined]
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

        friendly_name = (doc.get("name") or "").strip() or os.path.basename(abs_path)
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
                        rel_path.replace(os.sep, "/"),
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
                    "script_path": rel_path.replace(os.sep, "/"),
                    "script_content": encoded_content,
                    "script_encoding": "base64",
                    "environment": env_map,
                    "variables": variables,
                    "timeout_seconds": timeout_seconds,
                    "files": doc.get("files") if isinstance(doc.get("files"), list) else [],
                    "run_mode": run_mode,
                }
                if signature_b64:
                    payload["signature"] = signature_b64
                    payload["sig_alg"] = "ed25519"
                if signing_key_b64:
                    payload["signing_key"] = signing_key_b64

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
                    f"quick job queued hostname={host} path={rel_path} run_mode={run_mode}",
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
        return jsonify({"error": "Ansible quick run is not yet available in the Engine runtime."}), 501

    @blueprint.route("/api/device/activity/<hostname>", methods=["GET", "DELETE"])
    def device_activity(hostname: str):
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
