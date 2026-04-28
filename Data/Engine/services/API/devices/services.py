# ======================================================
# Data\Engine\services\API\devices\services.py
# Description: Device service inventory and operator-triggered control endpoints.
#
# API Endpoints (if applicable):
# - GET /api/device/software/icon/<icon_hash> (Token Authenticated) - Serves a cached installed-software icon asset by hash.
# - GET /api/device/services/<hostname> (Token Authenticated) - Returns cached service inventory for an in-scope device.
# - POST /api/device/services/<hostname>/action (Token Authenticated) - Start, stop, or restart a named service on an in-scope device.
# - POST /api/device/software/<hostname>/refresh (Token Authenticated) - Requests an immediate software inventory refresh over the device SYSTEM socket.
# - POST /api/device/software/<hostname>/icon-override (Token Authenticated) - Persist a hotloaded global software icon override and request a software refresh.
# - POST /api/device/software/<hostname>/uninstall-override (Token Authenticated) - Persist a hotloaded global software uninstall override.
# - POST /api/device/software/<hostname>/uninstall-block (Token Authenticated) - Persist a hotloaded global uninstall blocklist rule.
# - POST /api/device/software/<hostname>/uninstall-unblock (Token Authenticated) - Remove matching global uninstall blocklist rules for a software row.
# - POST /api/device/software/<hostname>/uninstall (Token Authenticated) - Queues a silent uninstall quick job for a supported software row on an in-scope Windows device.
# - GET /api/software/audit (Token Authenticated) - Returns RBAC-scoped installed software across visible devices.
# - POST /api/software/action/<action> (Token Authenticated) - Applies global software management actions across selected software rows.
# - POST /api/software/uninstall (Token Authenticated) - Queues uninstall quick jobs across selected software rows.
# - POST /api/device/update-agent/<hostname> (Token Authenticated) - Ask an in-scope device to start its local AutoUpdater task immediately.
# ======================================================

"""Device service inventory and operator-triggered control endpoints for the Borealis Engine."""
from __future__ import annotations

from io import BytesIO
import json
import random
import re
import textwrap
import time
from typing import Any, Dict, List, Optional, Tuple

from flask import Blueprint, jsonify, request, send_file

from ...auth import UserSiteAccessManager
from ..assemblies.execution import dispatch_inline_quick_job
from .software_icons import (
    canonicalize_software_icon_override_resource,
    load_software_icon_asset,
    normalize_software_icon_hash,
    upsert_software_icon_override,
)
from .software_uninstall import (
    build_windows_command,
    extract_quiet_argument_tokens,
    find_software_entry,
    find_matching_uninstall_blocklist_rules,
    normalize_text,
    normalize_software_source,
    resolve_software_uninstall_capability,
    software_metadata,
    split_windows_command_line,
    upsert_uninstall_blocklist_rule,
    upsert_uninstall_override,
    remove_uninstall_blocklist_rule,
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
_SOFTWARE_AUDIT_METADATA_KEYS = {
    "display_icon",
    "display_icon_override",
    "display_icon_override_cleared",
    "display_icon_override_rule_id",
    "distribution_app_id",
    "distribution_platform",
    "icon_hash",
    "icon_location",
    "install_location",
    "non_removable",
    "original_display_icon",
    "package_family_name",
    "product_code",
    "publisher",
    "quiet_uninstall_string",
    "uninstall_string",
}
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


def _software_uninstall_command_preview(uninstall_plan: Dict[str, Any]) -> str:
    quiet_command = normalize_text(uninstall_plan.get("quiet_uninstall_string"))
    if quiet_command:
        return quiet_command
    uninstall_command = normalize_text(uninstall_plan.get("uninstall_string"))
    if uninstall_command:
        return uninstall_command
    product_code = normalize_text(uninstall_plan.get("product_code"))
    if product_code:
        return f"msiexec.exe /x {product_code} /qn /norestart"
    package_family_name = normalize_text(uninstall_plan.get("package_family_name"))
    if package_family_name:
        return f"Remove-AppxPackage -AllUsers ({package_family_name})"
    return ""


def _trim_windows_path(value: Any) -> str:
    return normalize_text(value).rstrip("\\/")


def _slugify_software_token(value: Any) -> str:
    return re.sub(r"[^a-z0-9]+", "_", normalize_text(value).lower()).strip("_")


def _coerce_bool_flag(value: Any) -> bool:
    if isinstance(value, bool):
        return value
    return normalize_text(value).lower() in {"1", "true", "yes", "on"}


def _build_software_rule_id(prefix: str, software_entry: Dict[str, Any], *, include_version: bool = True) -> str:
    tokens = [
        _slugify_software_token(prefix),
        _slugify_software_token(software_entry.get("name") or "software"),
    ]
    version_token = _slugify_software_token(software_entry.get("version")) if include_version else ""
    if include_version and version_token:
        tokens.append(version_token)
    return "_".join(token for token in tokens if token)


def _build_software_rule_match_base(software_entry: Dict[str, Any]) -> Dict[str, Any]:
    metadata = software_metadata(software_entry)
    payload: Dict[str, Any] = {
        "source": normalize_software_source(software_entry.get("source")),
        "name": normalize_text(software_entry.get("name")),
    }
    version = normalize_text(software_entry.get("version"))
    if version:
        payload["version"] = version
    publisher = normalize_text(metadata.get("publisher"))
    if publisher:
        payload["publisher_contains_any"] = [publisher]
    product_code = normalize_text(metadata.get("product_code"))
    if product_code:
        payload["product_code"] = product_code
    install_location = _trim_windows_path(metadata.get("install_location"))
    if install_location:
        payload["install_location_contains_any"] = [install_location]
    return payload


def _build_software_icon_rule_match_base(software_entry: Dict[str, Any]) -> Dict[str, Any]:
    return {
        "name": normalize_text((software_entry or {}).get("name")),
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

    def _resolve_software_request_context(hostname: str) -> Tuple[Optional[Dict[str, Any]], Optional[Dict[str, Any]], str, Optional[Tuple[Dict[str, Any], int]]]:
        user = _current_user(app) or {}
        operator_id = normalize_text(user.get("username")) or "unknown"
        if not site_access.user_can_access_hostname(user, hostname):
            return None, None, operator_id, ({"error": "not found"}, 404)

        record = _load_device_record(hostname)
        if record is None:
            return None, None, operator_id, ({"error": "not found"}, 404)

        body = request.get_json(silent=True) or {}
        software_name = normalize_text(body.get("name"))
        software_version = normalize_text(body.get("version"))
        requested_source = normalize_text(body.get("source"))
        software_source = normalize_software_source(requested_source)
        if not software_name:
            return None, None, operator_id, ({"error": "software_name_required"}, 400)
        if not requested_source:
            return None, None, operator_id, ({"error": "software_source_required"}, 400)

        software_entry = find_software_entry(
            record.get("software"),
            name=software_name,
            version=software_version,
            source=software_source,
        )
        if software_entry is None:
            return None, None, operator_id, ({"error": "software_not_found"}, 404)

        return record, software_entry, operator_id, None

    def _request_software_inventory_refresh(
        *,
        hostname: str,
        record: Dict[str, Any],
        operator_id: str,
        reason: str,
    ) -> Tuple[bool, Dict[str, Any]]:
        requested_at = int(time.time())
        agent_id = _resolve_requested_agent_id(adapters, record.get("agent_id"))
        emit_host_service_event = getattr(adapters.context, "emit_host_service_event", None)
        emit_agent_event = getattr(adapters.context, "emit_agent_event", None)
        event_payload = {
            "hostname": record.get("hostname") or hostname,
            "agent_id": agent_id,
            "requested_at": requested_at,
            "requested_by": operator_id,
            "reason": normalize_text(reason),
        }

        emitted = False
        if callable(emit_host_service_event):
            try:
                emitted = bool(
                    emit_host_service_event(
                        record.get("hostname") or hostname,
                        "system",
                        "software_inventory_refresh_request",
                        event_payload,
                    )
                )
            except Exception:
                emitted = False
        if not emitted and callable(emit_agent_event) and agent_id:
            try:
                emitted = bool(emit_agent_event(agent_id, "software_inventory_refresh_request", event_payload))
            except Exception:
                emitted = False

        log_payload = {
            "hostname": record.get("hostname") or hostname,
            "agent_id": agent_id,
            "requested_at": requested_at,
        }
        if emitted:
            _agent_update_log_event(
                "device_software_refresh_request hostname={hostname} agent_id={agent_id} operator={operator} remote={remote} reason={reason}".format(
                    hostname=record.get("hostname") or hostname,
                    agent_id=agent_id or "-",
                    operator=operator_id or "-",
                    remote=_request_remote() or "-",
                    reason=normalize_text(reason) or "-",
                )
            )
        else:
            _agent_update_log_event(
                "device_software_refresh_unavailable hostname={hostname} agent_id={agent_id} operator={operator} remote={remote} reason={reason}".format(
                    hostname=record.get("hostname") or hostname,
                    agent_id=agent_id or "-",
                    operator=operator_id or "-",
                    remote=_request_remote() or "-",
                    reason=normalize_text(reason) or "-",
                ),
                level="WARNING",
            )
        return emitted, log_payload

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

    def _normalize_software_platform(operating_system: Any) -> str:
        value = normalize_text(operating_system).lower()
        if "windows" in value or value.startswith("win"):
            return "windows"
        if "mac" in value or "darwin" in value or "os x" in value:
            return "macos"
        if value:
            return "linux"
        return "unknown"

    def _metadata_from_json(value: Any) -> Dict[str, Any]:
        if isinstance(value, dict):
            return value
        if not value:
            return {}
        try:
            parsed = json.loads(value)
        except Exception:
            return {}
        return parsed if isinstance(parsed, dict) else {}

    def _software_entry_from_inventory_row(row: Dict[str, Any], *, include_metadata: bool = True) -> Dict[str, Any]:
        metadata = _metadata_from_json(row.get("metadata_json")) if include_metadata else {}
        return {
            "name": normalize_text(row.get("name")),
            "version": normalize_text(row.get("version")),
            "source": normalize_software_source(row.get("source")),
            "metadata": metadata,
        }

    def _software_audit_metadata(metadata: Dict[str, Any]) -> Dict[str, Any]:
        if not isinstance(metadata, dict):
            return {}
        return {
            key: value
            for key, value in metadata.items()
            if key in _SOFTWARE_AUDIT_METADATA_KEYS and value not in (None, "", [], {})
        }

    def _load_software_audit_rows(user: Dict[str, Any]) -> List[Dict[str, Any]]:
        allowed_site_ids = site_access.site_ids_for_user(user)
        if allowed_site_ids is not None and not allowed_site_ids:
            return []

        params: List[Any] = []
        site_clause = ""
        if allowed_site_ids is not None:
            placeholders = ",".join("?" for _ in sorted(allowed_site_ids))
            site_clause = f" AND ds.site_id IN ({placeholders})"
            params.extend(sorted(allowed_site_ids))

        conn = None
        try:
            conn = adapters.db_conn_factory()
            cur = conn.cursor()
            cur.execute(
                f"""
                SELECT
                    dsi.id,
                    dsi.device_guid,
                    dsi.name,
                    dsi.version,
                    dsi.source,
                    dsi.captured_at,
                    CASE
                        WHEN LOWER(COALESCE(d.operating_system, '')) LIKE '%%windows%%'
                          OR LOWER(COALESCE(d.operating_system, '')) LIKE 'win%%'
                        THEN dsi.metadata_json
                        ELSE NULL
                    END AS metadata_json,
                    d.guid,
                    d.hostname,
                    d.agent_id,
                    d.operating_system,
                    ds.site_id,
                    s.name
                  FROM device_software_inventory AS dsi
                  JOIN devices AS d
                    ON d.guid = dsi.device_guid
             LEFT JOIN device_sites AS ds
                    ON ds.device_hostname = d.hostname
             LEFT JOIN sites AS s
                    ON s.id = ds.site_id
                 WHERE TRIM(COALESCE(dsi.name, '')) <> ''
                       {site_clause}
                """,
                params,
            )
            rows = cur.fetchall()
        finally:
            if conn is not None:
                try:
                    conn.close()
                except Exception:
                    pass

        audit_rows: List[Dict[str, Any]] = []
        for row in rows:
            row_map = {
                "inventory_id": row[0],
                "device_guid": row[1],
                "name": row[2],
                "version": row[3],
                "source": row[4],
                "captured_at": row[5],
                "metadata_json": row[6],
                "guid": row[7],
                "hostname": row[8],
                "agent_id": row[9],
                "operating_system": row[10],
                "site_id": row[11],
                "site_name": row[12],
            }
            platform = _normalize_software_platform(row_map.get("operating_system"))
            entry = _software_entry_from_inventory_row(row_map, include_metadata=platform == "windows")
            uninstall_plan = (
                resolve_software_uninstall_capability(entry, row_map.get("operating_system"))
                if platform == "windows"
                else {
                    "supported": False,
                    "reason": "Software uninstall is currently supported for Windows devices only.",
                    "summary": "",
                }
            )
            response_metadata = _software_audit_metadata(entry.get("metadata") or {})
            distribution_platform = normalize_text(entry.get("distribution_platform") or response_metadata.get("distribution_platform"))
            distribution_app_id = normalize_text(entry.get("distribution_app_id") or response_metadata.get("distribution_app_id"))
            audit_rows.append(
                {
                    "id": row_map["inventory_id"],
                    "inventory_id": row_map["inventory_id"],
                    "name": normalize_text(entry.get("name")),
                    "version": normalize_text(entry.get("version")),
                    "source": normalize_software_source(entry.get("source")),
                    "metadata": response_metadata,
                    "captured_at": row_map["captured_at"] or 0,
                    "device_guid": normalize_text(row_map.get("device_guid")),
                    "hostname": normalize_text(row_map.get("hostname")),
                    "agent_id": normalize_text(row_map.get("agent_id")),
                    "operating_system": normalize_text(row_map.get("operating_system")),
                    "platform": platform,
                    "site_id": row_map.get("site_id"),
                    "site_name": normalize_text(row_map.get("site_name")),
                    "uninstall": uninstall_plan,
                    **({"distribution_platform": distribution_platform} if distribution_platform else {}),
                    **({"distribution_app_id": distribution_app_id} if distribution_app_id else {}),
                }
            )
        return audit_rows

    def _resolve_bulk_software_entries(
        body: Dict[str, Any],
        user: Dict[str, Any],
    ) -> Tuple[List[Tuple[Dict[str, Any], Dict[str, Any]]], Optional[Tuple[Dict[str, Any], int]]]:
        entries = body.get("entries") or body.get("software") or []
        if isinstance(entries, dict):
            entries = [entries]
        if not isinstance(entries, list) or not entries:
            return [], ({"error": "software_entries_required"}, 400)

        resolved: List[Tuple[Dict[str, Any], Dict[str, Any]]] = []
        seen: set[Tuple[str, str, str, str]] = set()
        for item in entries:
            if not isinstance(item, dict):
                continue
            hostname = normalize_text(item.get("hostname"))
            software_name = normalize_text(item.get("name"))
            software_version = normalize_text(item.get("version"))
            requested_source = normalize_text(item.get("source"))
            software_source = normalize_software_source(requested_source)
            if not hostname or not software_name or not requested_source:
                continue
            dedupe_key = (hostname.lower(), software_name.lower(), software_version.lower(), software_source)
            if dedupe_key in seen:
                continue
            seen.add(dedupe_key)
            if not site_access.user_can_access_hostname(user, hostname):
                continue
            record = _load_device_record(hostname)
            if record is None:
                continue
            software_entry = find_software_entry(
                record.get("software"),
                name=software_name,
                version=software_version,
                source=software_source,
            )
            if software_entry is None:
                continue
            resolved.append((record, software_entry))

        if not resolved:
            return [], ({"error": "software_not_found"}, 404)
        return resolved, None

    def _upsert_bulk_software_action_rule(
        *,
        action: str,
        software_entry: Dict[str, Any],
        body: Dict[str, Any],
    ) -> Dict[str, Any]:
        if action == "icon-override":
            clear_icon = _coerce_bool_flag(body.get("clear_icon") or body.get("remove_icon"))
            display_icon = ""
            if not clear_icon:
                display_icon = canonicalize_software_icon_override_resource(
                    body.get("display_icon") or body.get("icon_location")
                )
            if not clear_icon and not display_icon:
                raise ValueError("Choose or enter a valid EXE, DLL, ICO, or icon resource path.")
            rule = {
                "rule_id": _build_software_rule_id("icon_override", software_entry or {}, include_version=False),
                **_build_software_icon_rule_match_base(software_entry or {}),
            }
            if clear_icon:
                rule["clear_icon"] = True
            else:
                rule["display_icon"] = display_icon
            return upsert_software_icon_override(rule)

        if action == "uninstall-override":
            application_path = normalize_text(body.get("application_path") or body.get("file_path"))
            if not application_path:
                raise ValueError("application_path is required")
            metadata = software_metadata(software_entry or {})
            rule = {
                "rule_id": _build_software_rule_id("uninstall_override", software_entry or {}),
                **_build_software_rule_match_base(software_entry or {}),
                "strategy": "direct_command",
                "quiet_uninstall_string": build_windows_command(application_path, normalize_text(body.get("arguments"))),
                "uninstall_string": normalize_text(metadata.get("uninstall_string")),
                "summary": "Operator-defined global uninstall override.",
            }
            return upsert_uninstall_override(rule)

        if action == "uninstall-block":
            reason = normalize_text(body.get("reason"))
            if not reason:
                raise ValueError("reason is required")
            metadata = software_metadata(software_entry or {})
            parsed_quiet = split_windows_command_line(metadata.get("quiet_uninstall_string") or metadata.get("uninstall_string"))
            executable_name = normalize_text((parsed_quiet or {}).get("executable_name")).lower()
            quiet_args = extract_quiet_argument_tokens((parsed_quiet or {}).get("arguments"))
            rule = {
                "rule_id": _build_software_rule_id("uninstall_block", software_entry or {}),
                **_build_software_rule_match_base(software_entry or {}),
                "reason": reason,
            }
            if executable_name:
                rule["exe_names"] = [executable_name]
            if quiet_args:
                rule["quiet_args_any"] = quiet_args
            return upsert_uninstall_blocklist_rule(rule)

        if action == "uninstall-unblock":
            removed_rule_ids: List[str] = []
            for rule in find_matching_uninstall_blocklist_rules(software_entry or {}):
                rule_id = normalize_text(rule.get("rule_id"))
                if rule_id and remove_uninstall_blocklist_rule(rule_id):
                    removed_rule_ids.append(rule_id)
            if not removed_rule_ids:
                raise LookupError("No matching uninstall block rule was found for this software row.")
            return {"removed_rule_ids": removed_rule_ids}

        raise ValueError("unsupported action")

    @blueprint.route("/api/device/software/icon/<icon_hash>", methods=["GET"])
    def get_software_icon(icon_hash: str):
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        normalized_hash = normalize_software_icon_hash(icon_hash)
        if not normalized_hash:
            return jsonify({"error": "not found"}), 404

        asset = load_software_icon_asset(adapters.db_conn_factory, normalized_hash)
        if asset is None or not asset.get("icon_bytes"):
            return jsonify({"error": "not found"}), 404

        response = send_file(
            BytesIO(asset["icon_bytes"]),
            mimetype=asset.get("mime_type") or "image/png",
            download_name=f"{normalized_hash}.png",
            max_age=86400,
            etag=normalized_hash,
        )
        response.headers["Cache-Control"] = "private, max-age=86400"
        return response

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

    @blueprint.route("/api/device/software/<hostname>/refresh", methods=["POST"])
    def refresh_device_software(hostname: str):
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

        emitted, refresh_payload = _request_software_inventory_refresh(
            hostname=hostname,
            record=record,
            operator_id=operator_id,
            reason="operator_query_software_updates",
        )
        if not emitted:
            return (
                jsonify(
                    {
                        "error": "agent_unavailable",
                        "message": "The agent SYSTEM socket is not available to query software changes right now.",
                        **refresh_payload,
                    }
                ),
                409,
            )

        return jsonify({"status": "queued", **refresh_payload}), 200

    @blueprint.route("/api/device/software/<hostname>/icon-override", methods=["POST"])
    def create_device_software_icon_override(hostname: str):
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        record, software_entry, operator_id, error_response = _resolve_software_request_context(hostname)
        if error_response is not None:
            payload, status = error_response
            return jsonify(payload), status

        body = request.get_json(silent=True) or {}
        clear_icon = _coerce_bool_flag(body.get("clear_icon") or body.get("remove_icon"))
        display_icon = ""
        if not clear_icon:
            display_icon = canonicalize_software_icon_override_resource(
                body.get("display_icon") or body.get("icon_location")
            )
        if not clear_icon and not display_icon:
            return (
                jsonify(
                    {
                        "error": "display_icon_required",
                        "message": "Choose or enter a valid EXE, DLL, ICO, or icon resource path.",
                    }
                ),
                400,
            )

        rule = {
            "rule_id": _build_software_rule_id("icon_override", software_entry or {}, include_version=False),
            **_build_software_icon_rule_match_base(software_entry or {}),
        }
        if clear_icon:
            rule["clear_icon"] = True
        else:
            rule["display_icon"] = display_icon
        try:
            persisted_rule = upsert_software_icon_override(rule)
        except ValueError as exc:
            return jsonify({"error": "invalid_icon_override", "message": str(exc)}), 400
        except Exception as exc:
            logger.debug("Failed to persist software icon override", exc_info=True)
            return jsonify({"error": "icon_override_persist_failed", "message": str(exc)}), 500

        emitted, refresh_payload = _request_software_inventory_refresh(
            hostname=hostname,
            record=record or {},
            operator_id=operator_id,
            reason=f"operator_icon_override:{persisted_rule.get('rule_id')}",
        )
        return jsonify(
            {
                "status": "ok",
                "hostname": (record or {}).get("hostname") or hostname,
                "rule": persisted_rule,
                "refresh_requested": emitted,
                **refresh_payload,
            }
        ), 200

    @blueprint.route("/api/device/software/<hostname>/uninstall-override", methods=["POST"])
    def create_device_software_uninstall_override(hostname: str):
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        _record, software_entry, _operator_id, error_response = _resolve_software_request_context(hostname)
        if error_response is not None:
            payload, status = error_response
            return jsonify(payload), status

        body = request.get_json(silent=True) or {}
        application_path = normalize_text(body.get("application_path") or body.get("file_path"))
        arguments = normalize_text(body.get("arguments"))
        if not application_path:
            return jsonify({"error": "application_path_required"}), 400

        metadata = software_metadata(software_entry or {})
        rule = {
            "rule_id": _build_software_rule_id("uninstall_override", software_entry or {}),
            **_build_software_rule_match_base(software_entry or {}),
            "strategy": "direct_command",
            "quiet_uninstall_string": build_windows_command(application_path, arguments),
            "uninstall_string": normalize_text(metadata.get("uninstall_string")),
            "summary": "Operator-defined global uninstall override.",
        }
        try:
            persisted_rule = upsert_uninstall_override(rule)
        except ValueError as exc:
            return jsonify({"error": "invalid_uninstall_override", "message": str(exc)}), 400
        except Exception as exc:
            logger.debug("Failed to persist uninstall override", exc_info=True)
            return jsonify({"error": "uninstall_override_persist_failed", "message": str(exc)}), 500

        return jsonify({"status": "ok", "hostname": hostname, "rule": persisted_rule}), 200

    @blueprint.route("/api/device/software/<hostname>/uninstall-block", methods=["POST"])
    def create_device_software_uninstall_block(hostname: str):
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        _record, software_entry, _operator_id, error_response = _resolve_software_request_context(hostname)
        if error_response is not None:
            payload, status = error_response
            return jsonify(payload), status

        body = request.get_json(silent=True) or {}
        reason = normalize_text(body.get("reason"))
        if not reason:
            return jsonify({"error": "reason_required"}), 400

        metadata = software_metadata(software_entry or {})
        parsed_quiet = split_windows_command_line(metadata.get("quiet_uninstall_string") or metadata.get("uninstall_string"))
        executable_name = normalize_text((parsed_quiet or {}).get("executable_name")).lower()
        quiet_args = extract_quiet_argument_tokens((parsed_quiet or {}).get("arguments"))
        rule = {
            "rule_id": _build_software_rule_id("uninstall_block", software_entry or {}),
            **_build_software_rule_match_base(software_entry or {}),
            "reason": reason,
        }
        if executable_name:
            rule["exe_names"] = [executable_name]
        if quiet_args:
            rule["quiet_args_any"] = quiet_args

        try:
            persisted_rule = upsert_uninstall_blocklist_rule(rule)
        except ValueError as exc:
            return jsonify({"error": "invalid_uninstall_block", "message": str(exc)}), 400
        except Exception as exc:
            logger.debug("Failed to persist uninstall block", exc_info=True)
            return jsonify({"error": "uninstall_block_persist_failed", "message": str(exc)}), 500

        return jsonify({"status": "ok", "hostname": hostname, "rule": persisted_rule}), 200

    @blueprint.route("/api/device/software/<hostname>/uninstall-unblock", methods=["POST"])
    def remove_device_software_uninstall_block(hostname: str):
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        _record, software_entry, _operator_id, error_response = _resolve_software_request_context(hostname)
        if error_response is not None:
            payload, status = error_response
            return jsonify(payload), status

        matching_rules = find_matching_uninstall_blocklist_rules(software_entry or {})
        removed_rule_ids: list[str] = []
        for rule in matching_rules:
            rule_id = normalize_text(rule.get("rule_id"))
            if not rule_id:
                continue
            if remove_uninstall_blocklist_rule(rule_id):
                removed_rule_ids.append(rule_id)

        if not removed_rule_ids:
            return (
                jsonify(
                    {
                        "error": "uninstall_block_not_found",
                        "message": "No matching uninstall block rule was found for this software row.",
                    }
                ),
                404,
            )

        return jsonify(
            {
                "status": "ok",
                "hostname": hostname,
                "removed_rule_ids": removed_rule_ids,
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
        uninstall_command_preview = _software_uninstall_command_preview(uninstall_plan)
        try:
            dispatch = dispatch_inline_quick_job(
                adapters,
                hostnames=[record.get("hostname") or hostname],
                doc=uninstall_doc,
                script_path=_WINDOWS_SOFTWARE_UNINSTALL_PATH,
                requested_by=operator_id,
                run_mode="system",
                assembly_source="device_software_uninstall",
                queue_lane="software_management",
                activity_kind="software_uninstall",
                activity_metadata={
                    "software_name": normalize_text(software_entry.get("name")),
                    "software_version": normalize_text(software_entry.get("version")),
                    "software_source": normalize_software_source(software_entry.get("source")),
                    "uninstall_strategy": normalize_text(uninstall_plan.get("strategy")),
                    "uninstall_summary": normalize_text(uninstall_plan.get("summary")),
                    "uninstall_rule_id": normalize_text(uninstall_plan.get("rule_id")),
                    "command_preview": uninstall_command_preview,
                },
            )
        except ValueError as exc:
            return jsonify({"error": "dispatch_invalid", "message": str(exc)}), 400
        except RuntimeError as exc:
            return jsonify({"error": "dispatch_unavailable", "message": str(exc)}), 500
        except Exception as exc:
            logger.debug("Failed to queue software uninstall", exc_info=True)
            return jsonify({"error": "dispatch_failed", "message": str(exc)}), 500

        result = dispatch["results"][0] if dispatch.get("results") else {}
        if str(result.get("status") or "").strip().lower() not in {"queued", "running"}:
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
                        "command_preview": uninstall_command_preview,
                    },
                }
            ),
            200,
        )

    @blueprint.route("/api/software/audit", methods=["GET"])
    def list_software_audit():
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        user = _current_user(app) or {}
        try:
            rows = _load_software_audit_rows(user)
        except Exception as exc:
            logger.debug("Failed to list software audit rows", exc_info=True)
            return jsonify({"error": "software_audit_failed", "message": str(exc)}), 500
        return jsonify({"software": rows, "count": len(rows)}), 200

    @blueprint.route("/api/software/action/<action>", methods=["POST"])
    def bulk_software_action(action: str):
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        normalized_action = normalize_text(action).lower()
        if normalized_action not in {"icon-override", "uninstall-override", "uninstall-block", "uninstall-unblock"}:
            return jsonify({"error": "unsupported_action"}), 404

        user = _current_user(app) or {}
        operator_id = normalize_text(user.get("username")) or "unknown"
        body = request.get_json(silent=True) or {}
        resolved_entries, error_response = _resolve_bulk_software_entries(body, user)
        if error_response is not None:
            payload, status = error_response
            return jsonify(payload), status

        persisted_rules: List[Dict[str, Any]] = []
        errors: List[Dict[str, Any]] = []
        icon_refresh_candidates: Dict[str, Dict[str, Any]] = {}
        for record, software_entry in resolved_entries:
            try:
                rule = _upsert_bulk_software_action_rule(
                    action=normalized_action,
                    software_entry=software_entry,
                    body=body,
                )
                persisted_rules.append(
                    {
                        "hostname": normalize_text(record.get("hostname")),
                        "software": {
                            "name": normalize_text(software_entry.get("name")),
                            "version": normalize_text(software_entry.get("version")),
                            "source": normalize_software_source(software_entry.get("source")),
                        },
                        "rule": rule,
                    }
                )
                if normalized_action == "icon-override":
                    hostname_value = normalize_text(record.get("hostname"))
                    if hostname_value and hostname_value.lower() not in icon_refresh_candidates:
                        icon_refresh_candidates[hostname_value.lower()] = record
            except LookupError as exc:
                errors.append(
                    {
                        "hostname": normalize_text(record.get("hostname")),
                        "software": normalize_text(software_entry.get("name")),
                        "error": "rule_not_found",
                        "message": str(exc),
                    }
                )
            except ValueError as exc:
                return jsonify({"error": "invalid_software_action", "message": str(exc)}), 400
            except Exception as exc:
                logger.debug("Failed bulk software action", exc_info=True)
                errors.append(
                    {
                        "hostname": normalize_text(record.get("hostname")),
                        "software": normalize_text(software_entry.get("name")),
                        "error": "action_failed",
                        "message": str(exc),
                    }
                )

        if not persisted_rules and errors:
            return jsonify({"error": "software_action_failed", "errors": errors}), 500
        refresh_result: Dict[str, Any] = {}
        if normalized_action == "icon-override" and icon_refresh_candidates:
            candidate_records = list(icon_refresh_candidates.values())
            online_records = [
                record
                for record in candidate_records
                if _agent_socket_available(
                    normalize_text(record.get("hostname")),
                    _resolve_requested_agent_id(adapters, record.get("agent_id")),
                )
            ]
            refresh_record = random.choice(online_records or candidate_records)
            refresh_hostname = normalize_text(refresh_record.get("hostname"))
            emitted, refresh_payload = _request_software_inventory_refresh(
                hostname=refresh_hostname,
                record=refresh_record,
                operator_id=operator_id,
                reason="operator_icon_override_bulk",
            )
            refresh_result = {
                "hostname": refresh_hostname,
                "refresh_requested": emitted,
                **refresh_payload,
            }
        return (
            jsonify(
                {
                    "status": "ok",
                    "action": normalized_action,
                    "rules": persisted_rules,
                    "errors": errors,
                    "count": len(persisted_rules),
                    **({"refresh": refresh_result} if refresh_result else {}),
                }
            ),
            200,
        )

    @blueprint.route("/api/software/uninstall", methods=["POST"])
    def bulk_uninstall_software():
        requirement = _require_login(app)
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        user = _current_user(app) or {}
        operator_id = normalize_text(user.get("username")) or "unknown"
        body = request.get_json(silent=True) or {}
        resolved_entries, error_response = _resolve_bulk_software_entries(body, user)
        if error_response is not None:
            payload, status = error_response
            return jsonify(payload), status

        queued: List[Dict[str, Any]] = []
        errors: List[Dict[str, Any]] = []
        for record, software_entry in resolved_entries:
            hostname_value = normalize_text(record.get("hostname"))
            operating_system = normalize_text(record.get("operating_system"))
            if "windows" not in operating_system.lower():
                errors.append(
                    {
                        "hostname": hostname_value,
                        "software": normalize_text(software_entry.get("name")),
                        "error": "unsupported_platform",
                        "message": "Software uninstall is currently supported for Windows devices only.",
                    }
                )
                continue

            uninstall_plan = resolve_software_uninstall_capability(software_entry, operating_system)
            if not uninstall_plan.get("supported"):
                errors.append(
                    {
                        "hostname": hostname_value,
                        "software": normalize_text(software_entry.get("name")),
                        "error": "software_uninstall_unsupported",
                        "message": normalize_text(uninstall_plan.get("reason"))
                        or "Borealis could not find a supported silent uninstall path for that software row.",
                    }
                )
                continue

            agent_id = _resolve_requested_agent_id(adapters, record.get("agent_id"))
            if not _agent_socket_available(hostname_value, agent_id):
                errors.append(
                    {
                        "hostname": hostname_value,
                        "software": normalize_text(software_entry.get("name")),
                        "error": "agent_unavailable",
                        "message": "The agent SYSTEM socket is not available to queue the uninstall request.",
                    }
                )
                continue

            uninstall_doc = _build_windows_uninstall_doc(software_entry, uninstall_plan)
            uninstall_command_preview = _software_uninstall_command_preview(uninstall_plan)
            try:
                dispatch = dispatch_inline_quick_job(
                    adapters,
                    hostnames=[hostname_value],
                    doc=uninstall_doc,
                    script_path=_WINDOWS_SOFTWARE_UNINSTALL_PATH,
                    requested_by=operator_id,
                    run_mode="system",
                    assembly_source="software_audit_uninstall",
                    queue_lane="software_management",
                    activity_kind="software_uninstall",
                    activity_metadata={
                        "software_name": normalize_text(software_entry.get("name")),
                        "software_version": normalize_text(software_entry.get("version")),
                        "software_source": normalize_software_source(software_entry.get("source")),
                        "uninstall_strategy": normalize_text(uninstall_plan.get("strategy")),
                        "uninstall_summary": normalize_text(uninstall_plan.get("summary")),
                        "uninstall_rule_id": normalize_text(uninstall_plan.get("rule_id")),
                        "command_preview": uninstall_command_preview,
                    },
                )
            except Exception as exc:
                logger.debug("Failed to queue bulk software uninstall", exc_info=True)
                errors.append(
                    {
                        "hostname": hostname_value,
                        "software": normalize_text(software_entry.get("name")),
                        "error": "dispatch_failed",
                        "message": str(exc),
                    }
                )
                continue

            result = dispatch["results"][0] if dispatch.get("results") else {}
            if str(result.get("status") or "").strip().lower() not in {"queued", "running"}:
                errors.append(
                    {
                        "hostname": hostname_value,
                        "software": normalize_text(software_entry.get("name")),
                        "error": "dispatch_failed",
                        "message": normalize_text(result.get("error")) or "The uninstall request could not be queued.",
                        "result": result,
                    }
                )
                continue

            queued.append(
                {
                    "hostname": hostname_value,
                    "agent_id": agent_id,
                    "job_id": result.get("job_id"),
                    "software": {
                        "name": normalize_text(software_entry.get("name")),
                        "version": normalize_text(software_entry.get("version")),
                        "source": normalize_software_source(software_entry.get("source")),
                    },
                    "uninstall": {
                        "strategy": normalize_text(uninstall_plan.get("strategy")),
                        "summary": normalize_text(uninstall_plan.get("summary")),
                        "rule_id": normalize_text(uninstall_plan.get("rule_id")),
                        "command_preview": uninstall_command_preview,
                    },
                }
            )

        if not queued and errors:
            return jsonify({"error": "software_uninstall_failed", "queued": queued, "errors": errors}), 409
        return jsonify({"status": "queued", "queued": queued, "errors": errors, "count": len(queued)}), 200

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
