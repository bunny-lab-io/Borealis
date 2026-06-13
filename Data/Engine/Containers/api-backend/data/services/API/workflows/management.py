# ======================================================
# Data\Engine\services\API\workflows\management.py
# Description: Retained WorkflowRuntimeService bootstrap helpers for Python
#              watchdog/scheduler consumers during Go cutover.
#
# API Endpoints (if applicable):
# - None. Go api-backend owns public workflow routes and internal workflow start bridge.
# ======================================================

"""Workflow runtime bootstrap helpers for retained Python consumers."""

from __future__ import annotations

import json
import os
import time
from typing import TYPE_CHECKING, Any, Dict, List, Mapping, Optional
import urllib.error
import urllib.request
from urllib.parse import urlsplit

from ...aegis_cipher import AegisDataCorruptionError, AegisLockedError, AegisSecretResetRequiredError
from ...ansible.worker_dispatch import WorkerAnsibleDispatcher
from ...assemblies.service import AssemblyRuntimeService
from ...auth.secrets import require_app_secret
from ...job_scheduler.security import INTERNAL_TOKEN_HEADER, internal_token
from ...workflows import WorkflowRuntimeService

if TYPE_CHECKING:  # pragma: no cover - typing aide
    from flask import Flask

    from .. import EngineServiceAdapters


_DEFAULT_SHARED_ANSIBLE_VPN_PREP_WAIT_SECONDS = 10.0
_DEFAULT_SHARED_ANSIBLE_VPN_PREP_POLL_INTERVAL_SECONDS = 0.5


def _public_vpn_endpoint_host() -> str:
    for env_name in ("BOREALIS_AGENT_PUBLIC_BASE_URL", "BOREALIS_PUBLIC_BASE_URL"):
        raw_value = str(os.getenv(env_name) or "").strip()
        if not raw_value:
            continue
        try:
            parsed = urlsplit(raw_value if "://" in raw_value else f"//{raw_value}")
            if parsed.hostname:
                return str(parsed.hostname).strip()
        except Exception:
            continue
    return ""


def _internal_api_base() -> str:
    configured = str(os.getenv("BOREALIS_INTERNAL_API_BASE_URL") or "").strip()
    if configured:
        return configured.rstrip("/")
    host = str(os.getenv("BOREALIS_GO_API_HOST") or "127.0.0.1").strip() or "127.0.0.1"
    port = str(os.getenv("BOREALIS_GO_API_PORT") or "5000").strip() or "5000"
    return f"http://{host}:{port}"


def _internal_api_json(app: "Flask", path: str, *, payload: Optional[dict] = None, timeout: float = 10.0) -> dict:
    secret = require_app_secret(app)
    body = None
    method = "GET"
    headers = {
        "Accept": "application/json",
        INTERNAL_TOKEN_HEADER: internal_token(secret),
    }
    if payload is not None:
        method = "POST"
        body = json.dumps(payload).encode("utf-8")
        headers["Content-Type"] = "application/json"
    request_obj = urllib.request.Request(
        f"{_internal_api_base()}{path}",
        data=body,
        headers=headers,
        method=method,
    )
    try:
        with urllib.request.urlopen(request_obj, timeout=timeout) as response:
            decoded = json.loads(response.read().decode("utf-8") or "{}")
            return decoded if isinstance(decoded, dict) else {}
    except (urllib.error.URLError, urllib.error.HTTPError, TimeoutError, json.JSONDecodeError):
        return {}


def _internal_api_json_strict(app: "Flask", path: str, *, timeout: float = 10.0) -> dict:
    secret = require_app_secret(app)
    request_obj = urllib.request.Request(
        f"{_internal_api_base()}{path}",
        headers={
            "Accept": "application/json",
            INTERNAL_TOKEN_HEADER: internal_token(secret),
        },
        method="GET",
    )
    try:
        with urllib.request.urlopen(request_obj, timeout=timeout) as response:
            decoded = json.loads(response.read().decode("utf-8") or "{}")
            return decoded if isinstance(decoded, dict) else {}
    except urllib.error.HTTPError as exc:
        try:
            payload = json.loads(exc.read().decode("utf-8") or "{}")
        except Exception:
            payload = {}
        error_code = str(payload.get("error") or "").strip()
        message = str(payload.get("message") or error_code or exc).strip()
        if exc.code == 423 and error_code == "credential_reset_required":
            raise AegisSecretResetRequiredError(message)
        if exc.code == 423 and error_code == "aegis_locked":
            raise AegisLockedError(message)
        if error_code == "corrupt_secret_store":
            raise AegisDataCorruptionError(message)
        raise RuntimeError(message)
    except (urllib.error.URLError, TimeoutError, json.JSONDecodeError) as exc:
        raise RuntimeError(str(exc)) from exc


def ensure_workflow_runtime(app: "Flask", adapters: "EngineServiceAdapters") -> WorkflowRuntimeService:
    runtime = getattr(adapters.context, "workflow_runtime", None)
    if runtime is not None:
        return runtime

    cache = adapters.context.assembly_cache
    if cache is None:
        raise RuntimeError("Assembly cache is required to initialise the workflow runtime.")

    assembly_runtime = AssemblyRuntimeService(cache, logger=adapters.context.logger)
    ansible_runner = WorkerAnsibleDispatcher(
        app=app,
        adapters=adapters,
        logger=adapters.context.logger.getChild("workflows.ansible.worker_dispatch"),
    )
    runtime = WorkflowRuntimeService(
        db_conn_factory=adapters.db_conn_factory,
        assembly_runtime=assembly_runtime,
        script_signer=adapters.script_signer,
        socketio=getattr(adapters.context, "socketio", None),
        service_log=adapters.service_log,
        logger=adapters.context.logger.getChild("workflows.runtime"),
        ansible_runner=ansible_runner,
    )

    emit_host_service_event = getattr(adapters.context, "emit_host_service_event", None)
    if callable(emit_host_service_event):
        runtime.set_emit_host_service_event(emit_host_service_event)

    def _online_hostnames_snapshot() -> List[str]:
        threshold = int(time.time()) - 300
        conn = None
        try:
            conn = adapters.db_conn_factory()
            cur = conn.cursor()
            cur.execute(
                "SELECT hostname FROM devices WHERE last_seen IS NOT NULL AND last_seen >= ?",
                (threshold,),
            )
            rows = cur.fetchall()
        except Exception:
            rows = []
        finally:
            try:
                if conn is not None:
                    conn.close()
            except Exception:
                pass
        hostnames: List[str] = []
        for row in rows or []:
            try:
                hostname = str(row[0] if isinstance(row, (list, tuple)) else row or "").strip()
            except Exception:
                hostname = ""
            if hostname:
                hostnames.append(hostname)
        return hostnames

    runtime.set_online_lookup(_online_hostnames_snapshot)

    def _active_vpn_session_snapshot():
        payload = _internal_api_json(app, "/api/internal/job-scheduler/vpn-sessions", timeout=10.0)
        sessions = payload.get("sessions") if isinstance(payload, dict) else {}
        if not isinstance(sessions, dict):
            return {}
        return {
            str(agent_id or "").strip(): dict(session)
            for agent_id, session in sessions.items()
            if str(agent_id or "").strip() and isinstance(session, dict)
        }

    runtime.set_vpn_session_lookup(_active_vpn_session_snapshot)

    def _prepare_vpn_session_snapshot(agent_ids: List[str], required_ports: Optional[List[int] | tuple[int, ...]] = None):
        requested_ids = sorted({str(agent_id or "").strip() for agent_id in (agent_ids or []) if str(agent_id or "").strip()})
        snapshot = _active_vpn_session_snapshot()
        endpoint_host = _public_vpn_endpoint_host()
        if not endpoint_host:
            for payload in snapshot.values():
                if not isinstance(payload, dict):
                    continue
                endpoint = str(payload.get("endpoint") or "").strip()
                if not endpoint:
                    continue
                endpoint_host = endpoint.rsplit(":", 1)[0].strip().strip("[]")
                if endpoint_host:
                    break
        payload = _internal_api_json(
            app,
            "/api/internal/job-scheduler/vpn-prepare",
            payload={
                "agent_ids": requested_ids,
                "required_ports": list(required_ports or []),
                "endpoint_host": endpoint_host,
                "reason": "workflow_ansible_prepare",
                "timeout_seconds": _DEFAULT_SHARED_ANSIBLE_VPN_PREP_WAIT_SECONDS,
                "poll_interval_seconds": _DEFAULT_SHARED_ANSIBLE_VPN_PREP_POLL_INTERVAL_SECONDS,
            },
            timeout=_DEFAULT_SHARED_ANSIBLE_VPN_PREP_WAIT_SECONDS + 5.0,
        )
        sessions = payload.get("sessions") if isinstance(payload, dict) else {}
        if isinstance(sessions, dict):
            snapshot = {
                str(agent_id or "").strip(): dict(session)
                for agent_id, session in sessions.items()
                if str(agent_id or "").strip() and isinstance(session, dict)
            }
        for agent_id in requested_ids:
            payload = snapshot.get(agent_id)
            if isinstance(payload, dict):
                payload["_requested_start"] = True
        return snapshot

    runtime.set_vpn_session_prepare(_prepare_vpn_session_snapshot)

    def _load_decrypted_credential(credential_id: int):
        payload = _internal_api_json_strict(
            app,
            f"/api/internal/job-scheduler/credential/{int(credential_id)}",
            timeout=30.0,
        )
        credential = payload.get("credential") if isinstance(payload, Mapping) else None
        return dict(credential) if isinstance(credential, Mapping) else None

    runtime.set_credential_fetcher(_load_decrypted_credential)
    adapters.context.workflow_runtime = runtime
    adapters.service_log("workflows", "workflow runtime initialised", level="INFO")
    return runtime


def register_management(app: "Flask", adapters: "EngineServiceAdapters") -> None:
    """Go api-backend owns public workflow routes and internal start bridge."""

    return None
