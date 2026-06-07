package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	windowsSoftwareUninstallPath = "Scripts/Internal/Software_Uninstall.ps1"
	softwareUninstallQueueLane   = "software_management"
	softwareUninstallActivity    = "software_uninstall"
)

type softwareUninstallStore interface {
	softwareOverrideStore
	insertSoftwareUninstallActivity(ctx context.Context, hostname string, scriptName string, metadata map[string]any) (int64, error)
	markSoftwareUninstallActivityFailed(ctx context.Context, activityID int64, failureText string) error
}

type softwareUninstallQueueResult struct {
	Hostname      string
	AgentID       string
	JobID         int64
	ScriptName    string
	Software      map[string]any
	UninstallPlan map[string]any
}

func deviceSoftwareUninstallHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		profile, err := auth.currentProfile(r.Context(), r)
		if err != nil {
			if isUnauthorizedAuthError(err) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
			return
		}
		body, err := readJSONMap(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json", "message": "Request body must be valid JSON."})
			return
		}
		store, ok := auth.store.(softwareUninstallStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "software_uninstall_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		snapshot, status, err := store.loadDeviceSoftwareContext(ctx, profile, r.PathValue("hostname"))
		cancel()
		if err != nil {
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		entry, code, payload := resolveSoftwareEntryFromBody(snapshot.Software, body)
		if code != 0 {
			writeJSON(w, code, payload)
			return
		}
		result, code, payload := queueSoftwareUninstall(r.Context(), auth, store, profile, snapshot, entry, "device_software_uninstall")
		if code != 0 {
			writeJSON(w, code, payload)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":      "queued",
			"hostname":    result.Hostname,
			"agent_id":    result.AgentID,
			"job_id":      result.JobID,
			"script_name": result.ScriptName,
			"software":    result.Software,
			"uninstall":   result.UninstallPlan,
		})
	}
}

func bulkSoftwareUninstallHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		profile, err := auth.currentProfile(r.Context(), r)
		if err != nil {
			if isUnauthorizedAuthError(err) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
			return
		}
		body, err := readJSONMap(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json", "message": "Request body must be valid JSON."})
			return
		}
		store, ok := auth.store.(softwareUninstallStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "software_uninstall_unavailable"})
			return
		}
		resolved, code, payload := resolveBulkSoftwareEntries(r.Context(), auth, store, profile, body)
		if code != 0 {
			writeJSON(w, code, payload)
			return
		}
		queued := []map[string]any{}
		errorsPayload := []map[string]any{}
		for _, item := range resolved {
			result, code, payload := queueSoftwareUninstall(r.Context(), auth, store, profile, item.Context, item.Entry, "software_audit_uninstall")
			if code != 0 {
				errorsPayload = append(errorsPayload, map[string]any{
					"hostname": cleanText(item.Context.Hostname),
					"software": cleanText(item.Entry["name"]),
					"error":    firstText(cleanText(payload["error"]), "software_uninstall_failed"),
					"message":  cleanText(payload["message"]),
					"result":   payload["result"],
				})
				continue
			}
			queued = append(queued, map[string]any{
				"hostname":  result.Hostname,
				"agent_id":  result.AgentID,
				"job_id":    result.JobID,
				"software":  result.Software,
				"uninstall": result.UninstallPlan,
			})
		}
		if len(queued) == 0 && len(errorsPayload) > 0 {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "software_uninstall_failed", "queued": queued, "errors": errorsPayload})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "queued", "queued": queued, "errors": errorsPayload, "count": len(queued)})
	}
}

func queueSoftwareUninstall(ctx context.Context, auth *authService, store softwareUninstallStore, profile operatorProfile, snapshot softwareOverrideContext, entry map[string]any, assemblySource string) (softwareUninstallQueueResult, int, map[string]any) {
	hostname := cleanText(snapshot.Hostname)
	if hostname == "" {
		return softwareUninstallQueueResult{}, http.StatusNotFound, map[string]any{"error": "not found"}
	}
	if !strings.Contains(strings.ToLower(cleanText(snapshot.OperatingSystem)), "windows") {
		return softwareUninstallQueueResult{}, http.StatusBadRequest, map[string]any{
			"error":   "unsupported_platform",
			"message": "Software uninstall is currently supported for Windows devices only.",
		}
	}
	name := cleanText(entry["name"])
	version := cleanText(entry["version"])
	source := normalizeSoftwareSource(entry["source"])
	metadata := softwareMetadata(entry)
	plan := softwareUninstallCapability(name, version, source, metadata, snapshot.OperatingSystem)
	if !boolFromAny(plan["supported"]) {
		return softwareUninstallQueueResult{}, http.StatusBadRequest, map[string]any{
			"error":   "software_uninstall_unsupported",
			"message": firstText(cleanText(plan["reason"]), "Borealis could not find a supported silent uninstall path for that software row."),
		}
	}
	if snapshot.Route == nil {
		return softwareUninstallQueueResult{}, http.StatusConflict, map[string]any{
			"error":   "agent_unavailable",
			"message": "The agent SYSTEM socket is not available to queue the uninstall request.",
		}
	}
	statusCtx, statusCancel := context.WithTimeout(ctx, 5*time.Second)
	registered := workerHostServiceRegistered(statusCtx, auth, snapshot.Route, hostname, "system")
	statusCancel()
	if !registered {
		return softwareUninstallQueueResult{}, http.StatusConflict, map[string]any{
			"error":   "agent_unavailable",
			"message": "The agent SYSTEM socket is not available to queue the uninstall request.",
		}
	}
	scriptName := "Uninstall - " + firstText(name, "Software")
	uninstallPayload := softwareUninstallPlanPayload(plan)
	commandPreview := softwareUninstallCommandPreview(plan)
	activityMetadata := map[string]any{
		"assembly_source":    cleanText(assemblySource),
		"requested_by":       firstText(cleanText(profile.Username), "unknown"),
		"software_name":      name,
		"software_version":   version,
		"software_source":    source,
		"uninstall_strategy": cleanText(plan["strategy"]),
		"uninstall_summary":  cleanText(plan["summary"]),
		"uninstall_rule_id":  cleanText(plan["rule_id"]),
		"command_preview":    commandPreview,
	}
	insertCtx, insertCancel := requestTimeout(ctx, auth)
	activityID, err := store.insertSoftwareUninstallActivity(insertCtx, hostname, scriptName, activityMetadata)
	insertCancel()
	if err != nil {
		return softwareUninstallQueueResult{}, http.StatusInternalServerError, map[string]any{"error": "dispatch_failed", "message": err.Error()}
	}
	quickPayload, err := softwareUninstallQuickJobPayload(activityID, hostname, scriptName, entry, plan, activityMetadata, assemblySource)
	if err != nil {
		_ = markSoftwareUninstallDispatchFailed(ctx, auth, store, activityID, err.Error())
		return softwareUninstallQueueResult{}, http.StatusInternalServerError, map[string]any{"error": "dispatch_failed", "message": err.Error()}
	}
	result, _, workerErr := emitWorkerHostServiceEvent(ctx, auth, snapshot.Route, map[string]any{
		"hostname":     hostname,
		"service_mode": "system",
		"event_name":   "quick_job_run",
		"payload":      quickPayload,
	}, 6*time.Second)
	if workerErr != nil || (!boolFromAny(result["emitted"]) && !boolFromAny(result["queued"])) {
		failure := "No system agent socket is registered for host " + hostname + "; unable to dispatch quick job."
		if workerErr != nil {
			failure = firstText(cleanText(workerErr["message"]), cleanText(workerErr["error"]), failure)
		}
		_ = markSoftwareUninstallDispatchFailed(ctx, auth, store, activityID, failure)
		return softwareUninstallQueueResult{}, http.StatusConflict, map[string]any{
			"error":   "dispatch_failed",
			"message": failure,
			"result":  result,
		}
	}
	return softwareUninstallQueueResult{
		Hostname:   hostname,
		AgentID:    cleanText(snapshot.AgentID),
		JobID:      activityID,
		ScriptName: scriptName,
		Software: map[string]any{
			"name":    name,
			"version": version,
			"source":  source,
		},
		UninstallPlan: uninstallPayload,
	}, 0, nil
}

func markSoftwareUninstallDispatchFailed(ctx context.Context, auth *authService, store softwareUninstallStore, activityID int64, failure string) error {
	updateCtx, cancel := requestTimeout(ctx, auth)
	defer cancel()
	return store.markSoftwareUninstallActivityFailed(updateCtx, activityID, failure)
}

func softwareUninstallQuickJobPayload(activityID int64, hostname string, scriptName string, entry map[string]any, plan map[string]any, activityMetadata map[string]any, assemblySource string) (map[string]any, error) {
	signer, err := loadOrCreateScriptSigner()
	if err != nil {
		return nil, err
	}
	if signer == nil || len(signer.privateKey) == 0 {
		return nil, errors.New("script signer unavailable")
	}
	scriptBytes := []byte(windowsSoftwareUninstallScript)
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(signer.privateKey, scriptBytes))
	signingKey := scriptSigningKeyB64(signer)
	metadata := softwareMetadata(entry)
	environment := map[string]any{
		"SOFTWARE_NAME":          cleanText(entry["name"]),
		"SOFTWARE_VERSION":       cleanText(entry["version"]),
		"SOFTWARE_SOURCE":        normalizeSoftwareSource(entry["source"]),
		"PACKAGE_FAMILY_NAME":    firstText(cleanText(plan["package_family_name"]), cleanText(metadata["package_family_name"])),
		"QUIET_UNINSTALL_STRING": cleanText(plan["quiet_uninstall_string"]),
		"UNINSTALL_STRING":       firstText(cleanText(plan["uninstall_string"]), cleanText(metadata["uninstall_string"])),
		"PRODUCT_CODE":           cleanText(plan["product_code"]),
	}
	return map[string]any{
		"job_id":          activityID,
		"target_hostname": cleanText(hostname),
		"script_type":     "powershell",
		"script_name":     scriptName,
		"script_path":     windowsSoftwareUninstallPath,
		"script_content":  base64.StdEncoding.EncodeToString(scriptBytes),
		"script_encoding": "base64",
		"environment":     environment,
		"variables":       []map[string]any{},
		"timeout_seconds": 1800,
		"files":           []any{},
		"run_mode":        "system",
		"admin_user":      "",
		"admin_pass":      "",
		"signature":       signature,
		"sig_alg":         "ed25519",
		"signing_key":     signingKey,
		"context": map[string]any{
			"assembly_source":   cleanText(assemblySource),
			"queue_lane":        softwareUninstallQueueLane,
			"activity_kind":     softwareUninstallActivity,
			"activity_metadata": activityMetadata,
		},
	}, nil
}

func softwareUninstallPlanPayload(plan map[string]any) map[string]any {
	return map[string]any{
		"strategy":        cleanText(plan["strategy"]),
		"summary":         cleanText(plan["summary"]),
		"rule_id":         cleanText(plan["rule_id"]),
		"command_preview": softwareUninstallCommandPreview(plan),
	}
}

func softwareUninstallCommandPreview(plan map[string]any) string {
	if quiet := cleanText(plan["quiet_uninstall_string"]); quiet != "" {
		return quiet
	}
	if uninstall := cleanText(plan["uninstall_string"]); uninstall != "" {
		return uninstall
	}
	if productCode := cleanText(plan["product_code"]); productCode != "" {
		return fmt.Sprintf("msiexec.exe /x %s /qn /norestart", productCode)
	}
	if packageFamily := cleanText(plan["package_family_name"]); packageFamily != "" {
		return fmt.Sprintf("Remove-AppxPackage -AllUsers (%s)", packageFamily)
	}
	return ""
}

func (s *postgresOperatorStore) insertSoftwareUninstallActivity(ctx context.Context, hostname string, scriptName string, metadata map[string]any) (int64, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return 0, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return 0, err
	}
	now := time.Now().Unix()
	var activityID int64
	err = conn.QueryRowContext(ctx, `
		INSERT INTO engine.activity_history(
			hostname, script_path, script_name, script_type, ran_at, status,
			stdout, stderr, queue_lane, activity_kind, metadata_json,
			started_at, updated_at, finished_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING id
	`, hostname, windowsSoftwareUninstallPath, scriptName, "powershell", now, "Queued", "", "", softwareUninstallQueueLane, softwareUninstallActivity, string(metadataJSON), nil, now, nil).Scan(&activityID)
	return activityID, err
}

func (s *postgresOperatorStore) markSoftwareUninstallActivityFailed(ctx context.Context, activityID int64, failureText string) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	now := time.Now().Unix()
	_, err = conn.ExecContext(ctx, `
		UPDATE engine.activity_history
		   SET status=$1,
		       stderr=$2,
		       updated_at=$3,
		       finished_at=$4
		 WHERE id=$5
	`, "Failed", failureText, now, now, activityID)
	return err
}

const windowsSoftwareUninstallScript = `$ErrorActionPreference = 'Stop'

$softwareName = [string]$env:SOFTWARE_NAME
$softwareVersion = [string]$env:SOFTWARE_VERSION
$softwareSource = ([string]$env:SOFTWARE_SOURCE).Trim().ToLowerInvariant()
$packageFamilyName = [string]$env:PACKAGE_FAMILY_NAME
$quietUninstallString = [string]$env:QUIET_UNINSTALL_STRING
$uninstallString = [string]$env:UNINSTALL_STRING
$productCode = [string]$env:PRODUCT_CODE
$script:UninstallLog = New-Object System.Collections.Generic.List[string]

function Add-UninstallLog([string]$Message) {
  if ($Message) {
    [void]$script:UninstallLog.Add($Message)
  }
}

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
  Add-UninstallLog ("Invoking {0} {1}" -f $parsed.FilePath, (($argumentList -join ' ').Trim()))
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
    Add-UninstallLog ("Removed Appx package {0}" -f $pkg.PackageFullName)
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
    Add-UninstallLog ("Removed provisioned package {0}" -f $pkg.PackageName)
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
    Add-UninstallLog ("Using MSI product code {0}" -f $resolvedProductCode)
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
    Add-UninstallLog "Applying Inno Setup silent flags."
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
    Add-UninstallLog "Applying Squirrel-style silent flags."
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
$rawExitCode = if ($softwareSource -eq 'windows_store') {
  Invoke-WindowsStoreUninstall
} else {
  Invoke-LocalInstalledUninstall
}
$exitCodeCandidates = @(
  $rawExitCode | Where-Object {
    $_ -is [int] -or
    $_ -is [long] -or
    (('' + $_).Trim() -match '^-?\d+$')
  }
)
if (-not $exitCodeCandidates.Count) {
  throw ("Silent uninstall did not return a numeric exit code. Output: {0}" -f (($rawExitCode | ForEach-Object { '' + $_ }) -join '; '))
}
$exitCode = [int](('' + $exitCodeCandidates[-1]).Trim())
foreach ($line in $script:UninstallLog) {
  Write-Output $line
}

if ($exitCode -in @(0, 1605, 1614, 3010)) {
  if ($exitCode -eq 3010) {
    Write-Output "Silent uninstall finished and requested a reboot."
  } else {
    Write-Output "Silent uninstall finished successfully."
  }
  exit 0
}

throw ("Silent uninstall exited with code {0}." -f $exitCode)`
