# ======================================================
# Data\Engine\services\API\workflows\management.py
# Description: Workflow runtime API endpoints for manual launch, read-only run
#              inspection, and webhook management.
#
# API Endpoints (if applicable):
# - POST /api/workflows/run (Token Authenticated) - Save-triggered manual workflow launch.
# - GET /api/workflows/<workflow_guid>/runs (Token Authenticated) - Lists historical runs for a saved workflow.
# - GET /api/workflows/runs/<run_id> (Token Authenticated) - Returns an immutable workflow run snapshot.
# - GET /api/workflows/runs/<run_id>/nodes/<node_id> (Token Authenticated) - Returns one persisted node-run record.
# - POST /api/workflows/runs/<run_id>/resolve (Operator Admin Session) - Manually resolves an active stuck workflow run as Failed or Timed Out.
# ======================================================

"""Workflow runtime API endpoints for the Borealis Engine."""

from __future__ import annotations

import json
import os
import time
from typing import TYPE_CHECKING, Any, Dict, List, Mapping, Optional
import urllib.error
import urllib.request
from urllib.parse import urlsplit

from flask import Blueprint, jsonify, request

from ...aegis_cipher import AegisDataCorruptionError, AegisLockedError, AegisSecretResetRequiredError
from ...ansible.worker_dispatch import WorkerAnsibleDispatcher
from ...assemblies.service import AssemblyRuntimeService
from ...auth import RequestAuthContext, UserSiteAccessManager
from ...auth.secrets import require_app_secret
from ...job_scheduler.security import INTERNAL_TOKEN_HEADER, internal_token
from ...workflows import WorkflowRuntimeService

if TYPE_CHECKING:  # pragma: no cover - typing aide
    from flask import Flask

    from .. import EngineServiceAdapters


_DEFAULT_SHARED_ANSIBLE_VPN_PREP_WAIT_SECONDS = 10.0
_DEFAULT_SHARED_ANSIBLE_VPN_PREP_POLL_INTERVAL_SECONDS = 0.5


def _coerce_positive_int(value: Any, default: int) -> int:
    try:
        parsed = int(value)
        if parsed > 0:
            return parsed
    except Exception:
        pass
    return default


def _coerce_non_negative_float(value: Any, default: float) -> float:
    try:
        parsed = float(value)
        if parsed >= 0:
            return parsed
    except Exception:
        pass
    return default


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
    runtime = ensure_workflow_runtime(app, adapters)
    site_access = UserSiteAccessManager(adapters.db_conn_factory, logger=adapters.context.logger)
    auth = RequestAuthContext(
        app=app,
        dev_mode_manager=adapters.dev_mode_manager,
        config=adapters.config,
        logger=adapters.context.logger,
        db_conn_factory=adapters.db_conn_factory,
        aegis_cipher_service=getattr(adapters, "aegis_cipher_service", None),
    )
    blueprint = Blueprint("workflows", __name__, url_prefix="/api/workflows")

    def _current_user():
        return auth.current_user()

    def _created_by(user: Optional[Dict[str, Any]]) -> str:
        if not user:
            return ""
        username = str(user.get("username") or "").strip()
        role = str(user.get("role") or "").strip()
        return f"{username} ({role})" if username and role else username

    def _load_all_site_ids() -> set[int]:
        conn = adapters.db_conn_factory()
        try:
            cur = conn.cursor()
            cur.execute("SELECT id FROM sites")
            return {int(row[0]) for row in cur.fetchall() if row and row[0] is not None}
        finally:
            conn.close()

    def _workflow_editor_access_summary(workflow_guid: str, user: Optional[Dict[str, Any]]) -> Dict[str, Any]:
        allowed_site_ids = site_access.site_ids_for_user(user)
        if allowed_site_ids is None:
            return {
                "allowed": True,
                "hidden_devices": [],
                "hidden_filters": [],
                "message": "",
            }

        _workflow_export, workflow_payload = runtime.load_workflow_snapshot(workflow_guid)
        nodes = list(workflow_payload.get("nodes") or [])
        all_site_ids = _load_all_site_ids()
        hidden_devices: List[Dict[str, Any]] = []
        hidden_filters: List[Dict[str, Any]] = []
        seen_devices: set[str] = set()
        seen_filters: set[str] = set()

        def _record_hidden_device(
            *,
            node_id: str,
            node_label: str,
            hostname: str,
            site_id: Optional[int],
            site_name: str,
        ) -> None:
            identity = f"{site_id or 'na'}::{hostname.strip().lower()}"
            if not hostname or identity in seen_devices:
                return
            seen_devices.add(identity)
            hidden_devices.append(
                {
                    "node_id": node_id,
                    "node_label": node_label,
                    "hostname": hostname,
                    "site_id": site_id,
                    "site_name": site_name,
                }
            )

        def _record_hidden_filter(
            *,
            node_id: str,
            node_label: str,
            filter_id: Optional[int],
            filter_name: str,
        ) -> None:
            identity = f"{filter_id or 'na'}::{filter_name.strip().lower()}"
            if identity in seen_filters:
                return
            seen_filters.add(identity)
            hidden_filters.append(
                {
                    "node_id": node_id,
                    "node_label": node_label,
                    "filter_id": filter_id,
                    "filter_name": filter_name,
                }
            )

        def _check_filter_target(
            *,
            node_id: str,
            node_label: str,
            filter_id: Optional[int],
            fallback_name: str = "",
        ) -> None:
            if filter_id is None:
                return
            filters = runtime._filter_matcher.load_filters([int(filter_id)], include_archived=True)
            record = filters.get(int(filter_id))
            if not record:
                return
            site_mode = str(record.get("site_mode") or "global").strip().lower()
            configured_site_ids = {int(value) for value in (record.get("site_ids") or []) if value is not None}
            if site_mode == "specific_sites":
                effective_site_ids = configured_site_ids
            elif site_mode == "global_exclusions":
                effective_site_ids = set(all_site_ids).difference(configured_site_ids)
            else:
                effective_site_ids = set(all_site_ids)
            if not effective_site_ids.issubset(set(allowed_site_ids)):
                _record_hidden_filter(
                    node_id=node_id,
                    node_label=node_label,
                    filter_id=int(filter_id),
                    filter_name=str(record.get("name") or fallback_name or f"Filter {filter_id}").strip(),
                )

        def _check_device_target(
            *,
            node_id: str,
            node_label: str,
            target: Mapping[str, Any],
        ) -> None:
            hostname = str(target.get("hostname") or "").strip()
            device_guid = str(target.get("device_guid") or target.get("guid") or "").strip()
            site_id_raw = target.get("site_id")
            try:
                parsed_site_id = int(site_id_raw) if site_id_raw not in (None, "", "null") else None
            except Exception:
                parsed_site_id = None
            site_name = str(target.get("site_name") or target.get("site") or "").strip()
            if parsed_site_id is not None:
                if parsed_site_id in allowed_site_ids:
                    return
            if device_guid and site_access.user_can_access_guid(user, device_guid):
                return
            if hostname and site_access.user_can_access_hostname(user, hostname):
                return
            looked_up_site_id, looked_up_site_name, resolved_hostname = site_access._lookup_device_site(
                hostname=hostname,
                guid=device_guid,
            )
            _record_hidden_device(
                node_id=node_id,
                node_label=node_label,
                hostname=resolved_hostname or hostname or device_guid or "Unknown Device",
                site_id=looked_up_site_id if looked_up_site_id is not None else parsed_site_id,
                site_name=looked_up_site_name or site_name,
            )

        for node in nodes:
            if not isinstance(node, Mapping):
                continue
            node_id = str(node.get("id") or "").strip()
            data = node.get("data") if isinstance(node.get("data"), Mapping) else {}
            node_label = str(data.get("label") or node.get("label") or node.get("type") or "Node").strip()
            node_type = str(node.get("type") or "").strip()

            if node_type == "workflow_agent_filter":
                _check_filter_target(
                    node_id=node_id,
                    node_label=node_label,
                    filter_id=runtime._node_agent_filter_id(node),
                    fallback_name=str(data.get("filter_name") or data.get("selected_filter_name") or node_label).strip(),
                )
                continue

            if node_type == "workflow_agent_array":
                for target in runtime._node_agent_array_entries(node):
                    _check_device_target(node_id=node_id, node_label=node_label, target=target)
                continue

            if node_type == "workflow_execute_assembly":
                for target in runtime._node_target_definition(node):
                    if not isinstance(target, Mapping):
                        hostname = str(target or "").strip()
                        if hostname:
                            _check_device_target(
                                node_id=node_id,
                                node_label=node_label,
                                target={"hostname": hostname},
                            )
                        continue
                    kind = str(target.get("kind") or target.get("type") or "").strip().lower()
                    if kind == "filter" or target.get("filter_id") is not None:
                        filter_id_raw = target.get("filter_id") or target.get("id")
                        try:
                            filter_id = int(filter_id_raw) if filter_id_raw not in (None, "", "null") else None
                        except Exception:
                            filter_id = None
                        _check_filter_target(
                            node_id=node_id,
                            node_label=node_label,
                            filter_id=filter_id,
                            fallback_name=str(target.get("name") or "").strip(),
                        )
                    else:
                        _check_device_target(node_id=node_id, node_label=node_label, target=target)

        hidden_devices.sort(
            key=lambda item: (str(item.get("hostname") or "").lower(), str(item.get("site_name") or "").lower())
        )
        hidden_filters.sort(
            key=lambda item: (str(item.get("filter_name") or "").lower(), int(item.get("filter_id") or 0))
        )
        allowed = not hidden_devices and not hidden_filters
        return {
            "allowed": allowed,
            "hidden_devices": hidden_devices,
            "hidden_filters": hidden_filters,
            "message": "" if allowed else "This workflow references targets outside your assigned sites and cannot be opened.",
        }

    def _webhook_payload(record: Mapping[str, Any]) -> Dict[str, Any]:
        token = str(record.get("opaque_token") or "")
        return {
            **dict(record),
            "webhook_url": request.url_root.rstrip("/") + f"/api/workflows/webhooks/{token}",
            "created": int(record.get("created_at") or 0),
            "creator": str(record.get("creator_username") or "").strip() or "Unknown",
        }

    @blueprint.route("/run", methods=["POST"])
    def run_workflow():
        user, error = auth.require_user()
        if error:
            return jsonify(error[0]), error[1]
        payload = request.get_json(silent=True) or {}
        workflow_guid = str(payload.get("workflow_guid") or payload.get("workflowGuid") or "").strip()
        if not workflow_guid:
            return jsonify({"error": "workflow_guid is required"}), 400
        source_metadata = payload.get("source_metadata") if isinstance(payload.get("source_metadata"), dict) else {}
        source_metadata = dict(source_metadata or {})
        source_metadata.setdefault("workflow_guid", workflow_guid)
        source_metadata.setdefault("created_by", _created_by(user))
        try:
            result = runtime.start_run(
                workflow_guid=workflow_guid,
                source_type="manual",
                source_metadata=source_metadata,
                created_by=_created_by(user),
                execute_async=True,
            )
            return jsonify(result), 200
        except ValueError as exc:
            return jsonify({"error": "validation_failed", "message": str(exc)}), 400
        except Exception as exc:
            return jsonify({"error": str(exc)}), 500

    @blueprint.route("/<workflow_guid>/editor-access", methods=["GET"])
    def workflow_editor_access(workflow_guid: str):
        user, error = auth.require_user()
        if error:
            return jsonify(error[0]), error[1]
        try:
            return jsonify(_workflow_editor_access_summary(workflow_guid, user)), 200
        except ValueError:
            return jsonify({"error": "not found"}), 404
        except Exception as exc:
            return jsonify({"error": str(exc)}), 500

    @blueprint.route("/<workflow_guid>/runs", methods=["GET"])
    def list_workflow_runs(workflow_guid: str):
        _, error = auth.require_user()
        if error:
            return jsonify(error[0]), error[1]
        limit = _coerce_positive_int(request.args.get("limit"), 100)
        try:
            runs = runtime.list_runs(workflow_guid, limit=limit)
            return jsonify({"runs": runs})
        except Exception as exc:
            return jsonify({"error": str(exc)}), 500

    @blueprint.route("/runs/<int:run_id>", methods=["GET"])
    def get_workflow_run(run_id: int):
        _, error = auth.require_user()
        if error:
            return jsonify(error[0]), error[1]
        record = runtime.get_run(run_id)
        if not record:
            return jsonify({"error": "not found"}), 404
        return jsonify(record)

    @blueprint.route("/runs/<int:run_id>/resolve", methods=["POST"])
    def resolve_workflow_run(run_id: int):
        error = auth.require_admin()
        if error:
            return jsonify(error[0]), error[1]
        user = _current_user() or {}
        payload = request.get_json(silent=True) or {}
        requested_status = payload.get("status")
        try:
            result = runtime.resolve_stuck_run(
                run_id,
                status=requested_status,
                actor=_created_by(user),
                recovery_reason="manual_admin_resolve",
            )
        except LookupError:
            return jsonify({"error": "not found"}), 404
        except ValueError as exc:
            return jsonify({"error": "validation_failed", "message": str(exc)}), 400
        except Exception as exc:
            return jsonify({"error": str(exc)}), 500
        if not result.get("resolved"):
            return (
                jsonify(
                    {
                        "error": "run_not_active",
                        "message": "Workflow run is already terminal and does not need manual recovery.",
                        "run": result.get("run"),
                    }
                ),
                409,
            )
        return jsonify(result), 200

    @blueprint.route("/runs/<int:run_id>/nodes/<node_id>", methods=["GET"])
    def get_workflow_node_run(run_id: int, node_id: str):
        _, error = auth.require_user()
        if error:
            return jsonify(error[0]), error[1]
        record = runtime.get_node_run(run_id, node_id)
        if not record:
            return jsonify({"error": "not found"}), 404
        return jsonify(record)

    @blueprint.route("/<workflow_guid>/webhooks", methods=["GET"])
    def list_workflow_webhooks(workflow_guid: str):
        _, error = auth.require_user()
        if error:
            return jsonify(error[0]), error[1]
        try:
            webhooks = [_webhook_payload(record) for record in runtime.list_webhooks(workflow_guid)]
            return jsonify({"webhooks": webhooks})
        except Exception as exc:
            return jsonify({"error": str(exc)}), 500

    @blueprint.route("/<workflow_guid>/webhooks", methods=["POST"])
    def create_workflow_webhook(workflow_guid: str):
        user, error = auth.require_user()
        if error:
            return jsonify(error[0]), error[1]
        try:
            webhook = runtime.create_webhook(workflow_guid, creator=user)
            return jsonify({"webhook": _webhook_payload(webhook)}), 200
        except Exception as exc:
            return jsonify({"error": str(exc)}), 500

    @blueprint.route("/<workflow_guid>/webhooks/<int:webhook_id>", methods=["DELETE"])
    def delete_workflow_webhook(workflow_guid: str, webhook_id: int):
        _, error = auth.require_user()
        if error:
            return jsonify(error[0]), error[1]
        deleted = runtime.delete_webhook(workflow_guid, webhook_id)
        if not deleted:
            return jsonify({"error": "not found"}), 404
        return jsonify({"status": "ok"})

    @blueprint.route("/webhooks/<opaque_token>", methods=["POST"])
    def trigger_workflow_webhook(opaque_token: str):
        webhook = runtime.find_webhook_by_token(opaque_token)
        if not webhook:
            return jsonify({"error": "not found"}), 404
        source_metadata = {
            "workflow_guid": webhook.get("workflow_guid"),
            "webhook_id": webhook.get("id"),
            "created_by": "Webhook",
        }
        try:
            result = runtime.start_run(
                workflow_guid=str(webhook.get("workflow_guid") or ""),
                source_type="webhook",
                source_metadata=source_metadata,
                created_by="Webhook",
                execute_async=True,
            )
        except ValueError as exc:
            return jsonify({"error": "validation_failed", "message": str(exc)}), 400
        except Exception as exc:
            return jsonify({"error": str(exc)}), 500

        conn = adapters.db_conn_factory()
        try:
            cur = conn.cursor()
            cur.execute("UPDATE workflow_webhooks SET last_used_at=? WHERE id=?", (int(time.time()), int(webhook["id"])))
            conn.commit()
        finally:
            conn.close()
        return jsonify(result), 200

    app.register_blueprint(blueprint)
