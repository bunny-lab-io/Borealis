# ======================================================
# Data\Engine\services\API\devices\services.py
# Description: Device service inventory and operator-triggered control endpoints.
#
# API Endpoints (if applicable):
# - GET /api/device/services/<hostname> (Token Authenticated) - Returns cached service inventory for an in-scope device.
# - POST /api/device/services/<hostname>/action (Token Authenticated) - Start, stop, or restart a named service on an in-scope device.
# - POST /api/device/software/<hostname>/uninstall (Token Authenticated) - Queues a silent uninstall quick job for a supported software row on an in-scope Windows device.
# - POST /api/device/update-agent/<hostname> (Token Authenticated) - Ask an in-scope device to start its local AutoUpdater task immediately.
# ======================================================

"""Device service inventory and operator-triggered control endpoints for the Borealis Engine."""
from __future__ import annotations

import textwrap
import time
from typing import Any, Dict, Optional, Tuple

from flask import Blueprint, jsonify, request

from ...auth import UserSiteAccessManager
from ..assemblies.execution import dispatch_inline_quick_job
from .software_uninstall import (
    find_software_entry,
    normalize_text,
    normalize_software_source,
    resolve_software_uninstall_capability,
    software_metadata,
)
from .service_inventory import (
    action_label,
    mark_service_control_pending,
    normalize_device_services,
    normalize_service_action,
    serialize_device_services,
)
from .tunnel import _current_user, _require_login, _resolve_requested_agent_id

if False:  # pragma: no cover - hint for type checkers
    from .. import EngineServiceAdapters


_WINDOWS_SOFTWARE_UNINSTALL_PATH = "Scripts/Internal/Software_Uninstall.ps1"
_WINDOWS_UNINSTALL_SCRIPT = textwrap.dedent(
    r"""
    $ErrorActionPreference = 'Stop'

    $softwareName = [string]$env:SOFTWARE_NAME
    $softwareVersion = [string]$env:SOFTWARE_VERSION
    $softwareSource = ([string]$env:SOFTWARE_SOURCE).Trim().ToLowerInvariant()
    $packageFamilyName = [string]$env:PACKAGE_FAMILY_NAME
    $quietUninstallString = [string]$env:QUIET_UNINSTALL_STRING
    $uninstallString = [string]$env:UNINSTALL_STRING
    $productCode = [string]$env:PRODUCT_CODE

    function Split-ExecutableAndArguments([string]$CommandLine) {
      $trimmed = ('' + $CommandLine).Trim()
      if (-not $trimmed) {
        return $null
      }
      if ($trimmed -match '^\s*"(?<exe>[^"]+)"\s*(?<args>.*)$') {
        return @{
          FilePath = $Matches['exe']
          Arguments = [string]$Matches['args']
        }
      }
      if ($trimmed -match '^\s*(?<exe>(?:(?:[A-Za-z]:|\\\\[^\\\/]+\\[^\\\/]+)[^\r\n"]*?\.(?:exe|com|cmd|bat|msi|ps1)|[^\\/\s"]+\.(?:exe|com|cmd|bat|msi|ps1)))\s*(?<args>.*)$') {
        return @{
          FilePath = $Matches['exe']
          Arguments = [string]$Matches['args']
        }
      }
      if ($trimmed -match '^\s*(?<exe>\S+)\s*(?<args>.*)$') {
        return @{
          FilePath = $Matches['exe']
          Arguments = [string]$Matches['args']
        }
      }
      return $null
    }

    function Has-QuietSwitch([string]$Arguments) {
      $text = ('' + $Arguments).Trim()
      if (-not $text) {
        return $false
      }
      return [bool]($text -match '(?i)(^|\s)(/quiet|/qn|/qb!?|/passive|/s(\s|$)|/silent|/verysilent|--silent|--quiet|/suppressmsgboxes)(\s|$)')
    }

    function Get-MsiProductCode([string]$CommandLine) {
      $trimmed = ('' + $CommandLine).Trim()
      if (-not $trimmed) {
        return ''
      }
      if ($trimmed -match '(?i)\{[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\}') {
        return $Matches[0].ToUpperInvariant()
      }
      return ''
    }

    function Start-QuietProcess([string]$CommandLine, [string[]]$ExtraArgs = @()) {
      $parsed = Split-ExecutableAndArguments $CommandLine
      if ($null -eq $parsed -or -not $parsed.FilePath) {
        throw "Unable to parse command line: $CommandLine"
      }
      $argumentList = @()
      if ($parsed.Arguments) {
        $argumentList += $parsed.Arguments
      }
      foreach ($arg in ($ExtraArgs | Where-Object { $_ -and ('' + $_).Trim() })) {
        $argumentList += [string]$arg
      }
      Write-Output ("Invoking {0} {1}" -f $parsed.FilePath, (($argumentList -join ' ').Trim()))
      $proc = Start-Process -FilePath $parsed.FilePath -ArgumentList $argumentList -Wait -PassThru -WindowStyle Hidden
      return [int]($proc.ExitCode)
    }

    function Invoke-WindowsStoreUninstall {
      $packages = @()
      if ($packageFamilyName) {
        try {
          $packages = @(Get-AppxPackage -AllUsers -ErrorAction Stop | Where-Object { $_.PackageFamilyName -eq $packageFamilyName })
        } catch {
          $packages = @()
        }
        if (-not $packages.Count) {
          try {
            $packages = @(Get-AppxPackage -ErrorAction SilentlyContinue | Where-Object { $_.PackageFamilyName -eq $packageFamilyName })
          } catch {
            $packages = @()
          }
        }
      }
      if (-not $packages.Count -and $softwareName) {
        try {
          $packages = @(Get-AppxPackage -AllUsers -ErrorAction Stop | Where-Object { $_.Name -eq $softwareName })
        } catch {
          $packages = @()
        }
      }

      $removed = $false
      foreach ($pkg in $packages) {
        if (-not $pkg.PackageFullName) {
          continue
        }
        try {
          Remove-AppxPackage -Package $pkg.PackageFullName -AllUsers -ErrorAction Stop
        } catch {
          Remove-AppxPackage -Package $pkg.PackageFullName -ErrorAction Stop
        }
        $removed = $true
        Write-Output ("Removed Appx package {0}" -f $pkg.PackageFullName)
      }

      $provisioned = @()
      try {
        $provisioned = @(Get-AppxProvisionedPackage -Online -ErrorAction SilentlyContinue | Where-Object {
          ($packageFamilyName -and $_.PackageName -like "*$packageFamilyName*") -or
          ($softwareName -and ($_.DisplayName -eq $softwareName -or $_.PackageName -like "$softwareName*"))
        })
      } catch {
        $provisioned = @()
      }
      foreach ($pkg in $provisioned) {
        if (-not $pkg.PackageName) {
          continue
        }
        Remove-AppxProvisionedPackage -Online -PackageName $pkg.PackageName -ErrorAction SilentlyContinue | Out-Null
        Write-Output ("Removed provisioned package {0}" -f $pkg.PackageName)
        $removed = $true
      }

      if (-not $removed) {
        $lookupLabel = if ($packageFamilyName) { $packageFamilyName } else { $softwareName }
        if (-not $lookupLabel) {
          $lookupLabel = 'the selected package'
        }
        throw ("No installed or provisioned Windows Store package matched {0}." -f $lookupLabel)
      }
      return 0
    }

    function Invoke-LocalInstalledUninstall {
      if ($quietUninstallString) {
        return Start-QuietProcess $quietUninstallString
      }

      $resolvedProductCode = ''
      if ($productCode -match '(?i)^\{[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\}$') {
        $resolvedProductCode = $productCode.ToUpperInvariant()
      }
      if (-not $resolvedProductCode -and $uninstallString) {
        $resolvedProductCode = Get-MsiProductCode $uninstallString
      }
      if ($resolvedProductCode) {
        Write-Output ("Using MSI product code {0}" -f $resolvedProductCode)
        $proc = Start-Process -FilePath 'msiexec.exe' -ArgumentList @('/x', $resolvedProductCode, '/qn', '/norestart') -Wait -PassThru -WindowStyle Hidden
        return [int]($proc.ExitCode)
      }

      if (-not $uninstallString) {
        throw ("No uninstall string is available for {0}." -f $softwareName)
      }

      $parsed = Split-ExecutableAndArguments $uninstallString
      if ($null -eq $parsed -or -not $parsed.FilePath) {
        throw ("Unable to parse uninstall string for {0}." -f $softwareName)
      }
      $existingArgs = [string]$parsed.Arguments
      $exeName = [System.IO.Path]::GetFileName($parsed.FilePath).ToLowerInvariant()

      if (Has-QuietSwitch $existingArgs) {
        return Start-QuietProcess $uninstallString
      }
      if ($exeName -like 'unins*.exe') {
        Write-Output "Applying Inno Setup silent flags."
        return Start-QuietProcess $uninstallString @('/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART')
      }
      if ($exeName -eq 'update.exe') {
        $extraArgs = @()
        if ($existingArgs -notmatch '(?i)(^|\s)--uninstall(\s|$)') {
          $extraArgs += '--uninstall'
        }
        if ($existingArgs -notmatch '(?i)(^|\s)(/s|-s|--silent|--quiet)(\s|$)') {
          $extraArgs += '--silent'
        }
        Write-Output "Applying Squirrel-style silent flags."
        return Start-QuietProcess $uninstallString $extraArgs
      }

      throw (
        "Borealis could not derive a silent uninstall command for {0}. " +
        "QuietUninstallString was missing and the uninstall command is not a recognized MSI, built-in silent pattern, or supported Borealis uninstall rule."
      ) -f $softwareName
    }

    if (-not $softwareName) {
      throw "Missing software name."
    }

    Write-Output ("Starting silent uninstall for {0} {1}" -f $softwareName, $softwareVersion)
    $exitCode = if ($softwareSource -eq 'windows_store') {
      Invoke-WindowsStoreUninstall
    } else {
      Invoke-LocalInstalledUninstall
    }

    if ($exitCode -in @(0, 1605, 1614, 3010)) {
      if ($exitCode -eq 3010) {
        Write-Output "Silent uninstall finished and requested a reboot."
      } else {
        Write-Output "Silent uninstall finished successfully."
      }
      exit 0
    }

    throw ("Silent uninstall exited with code {0}." -f $exitCode)
    """
).strip()


def _build_windows_uninstall_doc(software_entry: Dict[str, Any], uninstall_plan: Dict[str, Any]) -> Dict[str, Any]:
    metadata = software_metadata(software_entry)
    return {
        "name": f"Uninstall - {normalize_text(software_entry.get('name')) or 'Software'}",
        "description": "Operator-triggered silent software uninstall from the Device Summary Installed Software tab.",
        "type": "powershell",
        "script": _WINDOWS_UNINSTALL_SCRIPT,
        "timeout_seconds": 1800,
        "variables": [
            {"name": "SOFTWARE_NAME", "type": "string", "default": normalize_text(software_entry.get("name"))},
            {"name": "SOFTWARE_VERSION", "type": "string", "default": normalize_text(software_entry.get("version"))},
            {"name": "SOFTWARE_SOURCE", "type": "string", "default": normalize_software_source(software_entry.get("source"))},
            {
                "name": "PACKAGE_FAMILY_NAME",
                "type": "string",
                "default": normalize_text(uninstall_plan.get("package_family_name")) or normalize_text(metadata.get("package_family_name")),
            },
            {
                "name": "QUIET_UNINSTALL_STRING",
                "type": "string",
                "default": normalize_text(uninstall_plan.get("quiet_uninstall_string")),
            },
            {
                "name": "UNINSTALL_STRING",
                "type": "string",
                "default": normalize_text(uninstall_plan.get("uninstall_string")) or normalize_text(metadata.get("uninstall_string")),
            },
            {
                "name": "PRODUCT_CODE",
                "type": "string",
                "default": normalize_text(uninstall_plan.get("product_code")),
            },
        ],
        "files": [],
    }


def register_services(app, adapters: "EngineServiceAdapters") -> None:
    blueprint = Blueprint("device_services", __name__)
    logger = adapters.context.logger.getChild("device_services.api")
    service_log = adapters.service_log
    site_access = UserSiteAccessManager(adapters.db_conn_factory, logger=logger)

    def _service_log_event(message: str, *, level: str = "INFO") -> None:
        if not callable(service_log):
            return
        try:
            service_log("device_services", message, level=level)
        except Exception:
            logger.debug("device_services service log write failed", exc_info=True)

    def _agent_update_log_event(message: str, *, level: str = "INFO") -> None:
        if not callable(service_log):
            return
        try:
            service_log("device_actions", message, level=level)
        except Exception:
            logger.debug("device_actions service log write failed", exc_info=True)

    def _request_remote() -> str:
        forwarded = (request.headers.get("X-Forwarded-For") or "").strip()
        if forwarded:
            return forwarded.split(",")[0].strip()
        return (request.remote_addr or "").strip()

    def _notify_clients(hostname: str, change: str) -> None:
        socketio = getattr(adapters.context, "socketio", None)
        normalized_hostname = normalize_text(hostname)
        normalized_change = normalize_text(change)
        if socketio is None or not normalized_hostname or not normalized_change:
            return
        try:
            socketio.emit(
                "device_services_changed",
                {
                    "hostname": normalized_hostname,
                    "change": normalized_change,
                },
            )
        except Exception:
            logger.debug("device_services_changed emit failed hostname=%s", normalized_hostname, exc_info=True)

    def _load_device_record(hostname: str) -> Optional[Dict[str, Any]]:
        conn = None
        try:
            conn = adapters.db_conn_factory()
            cur = conn.cursor()
            cur.execute(
                """
                SELECT hostname, agent_id, services, software, operating_system, last_seen
                  FROM devices
                 WHERE LOWER(hostname) = LOWER(?)
              ORDER BY last_seen DESC
                 LIMIT 1
                """,
                (hostname,),
            )
            row = cur.fetchone()
            if not row:
                return None
            return {
                "hostname": normalize_text(row[0]),
                "agent_id": normalize_text(row[1]),
                "services": row[2],
                "software": row[3],
                "operating_system": normalize_text(row[4]),
                "last_seen": row[5] or 0,
            }
        finally:
            if conn is not None:
                try:
                    conn.close()
                except Exception:
                    pass

    def _agent_socket_available(hostname: str, agent_id: str) -> bool:
        has_host_socket = getattr(adapters.context, "has_host_service_socket", None)
        if callable(has_host_socket):
            try:
                if bool(has_host_socket(hostname, "system")):
                    return True
            except Exception:
                logger.debug("has_host_service_socket failed hostname=%s", hostname, exc_info=True)
        registry = getattr(adapters.context, "agent_socket_registry", None)
        if registry and hasattr(registry, "is_registered") and agent_id:
            try:
                return bool(registry.is_registered(agent_id))
            except Exception:
                logger.debug("agent_socket_registry lookup failed agent_id=%s", agent_id, exc_info=True)
        return False

    @blueprint.route("/api/device/services/<hostname>", methods=["GET"])
    def get_device_services(hostname: str):
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

        services_payload = normalize_device_services(record.get("services"))
        agent_id = _resolve_requested_agent_id(adapters, record.get("agent_id"))
        response = {
            "hostname": record.get("hostname") or hostname,
            "agent_id": agent_id,
            "agent_socket": _agent_socket_available(record.get("hostname") or hostname, agent_id),
            "reported_at": services_payload.get("reported_at") or 0,
            "refresh_interval_seconds": 60,
            "count": len(services_payload.get("services") or []),
            "services": services_payload.get("services") or [],
        }
        return jsonify(response), 200

    @blueprint.route("/api/device/services/<hostname>/action", methods=["POST"])
    def service_action(hostname: str):
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        user = _current_user(app) or {}
        operator_id = normalize_text(user.get("username"))
        if not site_access.user_can_access_hostname(user, hostname):
            return jsonify({"error": "not found"}), 404

        body = request.get_json(silent=True) or {}
        service_name = normalize_text(body.get("service_name") or body.get("name"))
        action = normalize_service_action(body.get("action"))
        if not service_name:
            return jsonify({"error": "service_name_required"}), 400
        if not action:
            return jsonify({"error": "invalid_action"}), 400

        record = _load_device_record(hostname)
        if record is None:
            return jsonify({"error": "not found"}), 404

        requested_at = int(time.time())
        updated_services = mark_service_control_pending(
            record.get("services"),
            service_name,
            action,
            requested_at=requested_at,
            requested_by=operator_id,
        )
        if updated_services is None:
            return jsonify({"error": "service_not_found"}), 404

        agent_id = _resolve_requested_agent_id(adapters, record.get("agent_id"))
        emit_host_service_event = getattr(adapters.context, "emit_host_service_event", None)
        emit_agent_event = getattr(adapters.context, "emit_agent_event", None)
        event_payload = {
            "hostname": record.get("hostname") or hostname,
            "agent_id": agent_id,
            "service_name": service_name,
            "action": action,
            "requested_at": requested_at,
            "requested_by": operator_id,
        }

        emitted = False
        if callable(emit_host_service_event):
            try:
                emitted = bool(
                    emit_host_service_event(
                        record.get("hostname") or hostname,
                        "system",
                        "service_control_action",
                        event_payload,
                    )
                )
            except Exception:
                emitted = False
        if not emitted and callable(emit_agent_event) and agent_id:
            try:
                emitted = bool(emit_agent_event(agent_id, "service_control_action", event_payload))
            except Exception:
                emitted = False
        if not emitted:
            _service_log_event(
                "device_services_action_unavailable hostname={0} agent_id={1} service_name={2} action={3} operator={4} remote={5}".format(
                    record.get("hostname") or hostname,
                    agent_id or "-",
                    service_name,
                    action,
                    operator_id or "-",
                    _request_remote() or "-",
                ),
                level="WARNING",
            )
            return jsonify({"error": "agent_unavailable"}), 409

        conn = None
        try:
            conn = adapters.db_conn_factory()
            cur = conn.cursor()
            cur.execute(
                "UPDATE devices SET services = ? WHERE LOWER(hostname) = LOWER(?)",
                (serialize_device_services(updated_services), hostname),
            )
            conn.commit()
        except Exception as exc:
            if conn is not None:
                try:
                    conn.rollback()
                except Exception:
                    pass
            logger.debug("Failed to persist pending service action", exc_info=True)
            return jsonify({"error": "persist_failed"}), 500
        finally:
            if conn is not None:
                try:
                    conn.close()
                except Exception:
                    pass

        _service_log_event(
            "device_services_action_request hostname={0} agent_id={1} service_name={2} action={3} operator={4} remote={5}".format(
                record.get("hostname") or hostname,
                agent_id or "-",
                service_name,
                action,
                operator_id or "-",
                _request_remote() or "-",
            )
        )
        _notify_clients(record.get("hostname") or hostname, "requested")

        response = normalize_device_services(updated_services)
        return jsonify(
            {
                "status": "ok",
                "hostname": record.get("hostname") or hostname,
                "agent_id": agent_id,
                "service_name": service_name,
                "action": action,
                "action_label": action_label(action),
                "requested_at": requested_at,
                "reported_at": response.get("reported_at") or 0,
                "count": len(response.get("services") or []),
                "services": response.get("services") or [],
            }
        ), 200

    @blueprint.route("/api/device/software/<hostname>/uninstall", methods=["POST"])
    def uninstall_software(hostname: str):
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        user = _current_user(app) or {}
        operator_id = normalize_text(user.get("username")) or "unknown"
        if not site_access.user_can_access_hostname(user, hostname):
            return jsonify({"error": "not found"}), 404

        body = request.get_json(silent=True) or {}
        software_name = normalize_text(body.get("name"))
        software_version = normalize_text(body.get("version"))
        requested_source = normalize_text(body.get("source"))
        software_source = normalize_software_source(requested_source)
        if not software_name:
            return jsonify({"error": "software_name_required"}), 400
        if not requested_source:
            return jsonify({"error": "software_source_required"}), 400

        record = _load_device_record(hostname)
        if record is None:
            return jsonify({"error": "not found"}), 404

        operating_system = normalize_text(record.get("operating_system"))
        if "windows" not in operating_system.lower():
            return (
                jsonify(
                    {
                        "error": "unsupported_platform",
                        "message": "Software uninstall is currently supported for Windows devices only.",
                    }
                ),
                400,
            )

        software_entry = find_software_entry(
            record.get("software"),
            name=software_name,
            version=software_version,
            source=software_source,
        )
        if software_entry is None:
            return jsonify({"error": "software_not_found"}), 404
        uninstall_plan = resolve_software_uninstall_capability(software_entry, operating_system)
        if not uninstall_plan.get("supported"):
            return (
                jsonify(
                    {
                        "error": "software_uninstall_unsupported",
                        "message": normalize_text(uninstall_plan.get("reason"))
                        or "Borealis could not find a supported silent uninstall path for that software row.",
                    }
                ),
                400,
            )

        agent_id = _resolve_requested_agent_id(adapters, record.get("agent_id"))
        if not _agent_socket_available(record.get("hostname") or hostname, agent_id):
            _agent_update_log_event(
                "device_software_uninstall_unavailable hostname={0} agent_id={1} software_name={2} source={3} operator={4} remote={5}".format(
                    record.get("hostname") or hostname,
                    agent_id or "-",
                    software_name,
                    software_source,
                    operator_id or "-",
                    _request_remote() or "-",
                ),
                level="WARNING",
            )
            return (
                jsonify(
                    {
                        "error": "agent_unavailable",
                        "message": "The agent SYSTEM socket is not available to queue the uninstall request.",
                    }
                ),
                409,
            )

        uninstall_doc = _build_windows_uninstall_doc(software_entry, uninstall_plan)
        try:
            dispatch = dispatch_inline_quick_job(
                adapters,
                hostnames=[record.get("hostname") or hostname],
                doc=uninstall_doc,
                script_path=_WINDOWS_SOFTWARE_UNINSTALL_PATH,
                requested_by=operator_id,
                run_mode="system",
                assembly_source="device_software_uninstall",
            )
        except ValueError as exc:
            return jsonify({"error": "dispatch_invalid", "message": str(exc)}), 400
        except RuntimeError as exc:
            return jsonify({"error": "dispatch_unavailable", "message": str(exc)}), 500
        except Exception as exc:
            logger.debug("Failed to queue software uninstall", exc_info=True)
            return jsonify({"error": "dispatch_failed", "message": str(exc)}), 500

        result = dispatch["results"][0] if dispatch.get("results") else {}
        if str(result.get("status") or "").strip().lower() != "running":
            return (
                jsonify(
                    {
                        "error": "dispatch_failed",
                        "message": normalize_text(result.get("error")) or "The uninstall request could not be queued.",
                        "result": result,
                    }
                ),
                409,
            )

        _agent_update_log_event(
            "device_software_uninstall_request hostname={0} agent_id={1} software_name={2} version={3} source={4} operator={5} remote={6}".format(
                record.get("hostname") or hostname,
                agent_id or "-",
                normalize_text(software_entry.get("name")) or software_name,
                normalize_text(software_entry.get("version")) or "-",
                normalize_software_source(software_entry.get("source")) or software_source,
                operator_id or "-",
                _request_remote() or "-",
            )
        )
        return (
            jsonify(
                {
                    "status": "queued",
                    "hostname": record.get("hostname") or hostname,
                    "agent_id": agent_id,
                    "job_id": result.get("job_id"),
                    "script_name": dispatch.get("script_name") or "",
                    "software": {
                        "name": normalize_text(software_entry.get("name")),
                        "version": normalize_text(software_entry.get("version")),
                        "source": normalize_software_source(software_entry.get("source")),
                    },
                    "uninstall": {
                        "strategy": normalize_text(uninstall_plan.get("strategy")),
                        "summary": normalize_text(uninstall_plan.get("summary")),
                        "rule_id": normalize_text(uninstall_plan.get("rule_id")),
                    },
                }
            ),
            200,
        )

    @blueprint.route("/api/device/update-agent/<hostname>", methods=["POST"])
    def update_agent(hostname: str):
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        user = _current_user(app) or {}
        operator_id = normalize_text(user.get("username"))
        if not site_access.user_can_access_hostname(user, hostname):
            return jsonify({"error": "not found"}), 404

        record = _load_device_record(hostname)
        if record is None:
            return jsonify({"error": "not found"}), 404

        requested_at = int(time.time())
        agent_id = _resolve_requested_agent_id(adapters, record.get("agent_id"))
        emit_host_service_event = getattr(adapters.context, "emit_host_service_event", None)
        emit_agent_event = getattr(adapters.context, "emit_agent_event", None)
        event_payload = {
            "hostname": record.get("hostname") or hostname,
            "agent_id": agent_id,
            "requested_at": requested_at,
            "requested_by": operator_id,
        }

        emitted = False
        if callable(emit_host_service_event):
            try:
                emitted = bool(
                    emit_host_service_event(
                        record.get("hostname") or hostname,
                        "system",
                        "agent_update_request",
                        event_payload,
                    )
                )
            except Exception:
                emitted = False
        if not emitted and callable(emit_agent_event) and agent_id:
            try:
                emitted = bool(emit_agent_event(agent_id, "agent_update_request", event_payload))
            except Exception:
                emitted = False

        if not emitted:
            _agent_update_log_event(
                "device_agent_update_unavailable hostname={0} agent_id={1} operator={2} remote={3}".format(
                    record.get("hostname") or hostname,
                    agent_id or "-",
                    operator_id or "-",
                    _request_remote() or "-",
                ),
                level="WARNING",
            )
            return (
                jsonify(
                    {
                        "error": "agent_unavailable",
                        "message": "The agent SYSTEM socket is not available to start the local AutoUpdater task.",
                    }
                ),
                409,
            )

        _agent_update_log_event(
            "device_agent_update_request hostname={0} agent_id={1} operator={2} remote={3}".format(
                record.get("hostname") or hostname,
                agent_id or "-",
                operator_id or "-",
                _request_remote() or "-",
            )
        )
        return (
            jsonify(
                {
                    "status": "queued",
                    "hostname": record.get("hostname") or hostname,
                    "agent_id": agent_id,
                    "requested_at": requested_at,
                }
            ),
            200,
        )

    app.register_blueprint(blueprint)
