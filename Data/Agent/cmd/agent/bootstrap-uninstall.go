//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func uninstallBorealis(cfg BootstrapConfig, logger *BootstrapLogger) error {
	startedAt := time.Now()
	logger.Infof("Uninstall requested.")
	finalLog := prepareUninstallLog()
	appendUninstallLog(finalLog, "Agent.exe uninstall requested.")
	logger.Tracef("Uninstall sequence start: install_dir=%s final_log=%s", cfg.InstallDir, finalLog)

	removeBorealisScheduledTasks(logger)
	uninstallWireGuardManagerService(logger)
	removeBorealisOwnedServices(logger)
	stopBorealisDependencyProcesses(cfg, logger)
	stopBorealisProcesses(cfg, logger)
	uninstallWireGuardManagerService(logger)
	removeBorealisOwnedServices(logger)
	removeDependencyInstallRoots(logger)

	exe, _ := os.Executable()
	logger.Tracef("Uninstall executing from: %s", exe)
	if strings.HasPrefix(strings.ToLower(filepath.Clean(exe)), strings.ToLower(filepath.Clean(cfg.InstallDir))) {
		if err := launchDeferredUninstallCleanup(cfg, finalLog, logger); err != nil {
			return err
		}
		logger.Infof("Deferred uninstall cleanup launched. Final log: %s", finalLog)
		logger.Tracef("Uninstall sequence deferred duration=%s.", time.Since(startedAt).Round(time.Millisecond))
		return nil
	}

	if err := removeInstallDirWithRetries(cfg.InstallDir, finalLog, logger); err != nil {
		if err := launchDeferredUninstallCleanup(cfg, finalLog, logger); err != nil {
			return err
		}
		logger.Warnf("Install directory still busy; deferred cleanup launched. Final log: %s", finalLog)
	}
	logger.Tracef("Uninstall sequence complete duration=%s.", time.Since(startedAt).Round(time.Millisecond))
	return nil
}

func prepareUninstallLog() string {
	finalLogDir := filepath.Join(os.Getenv("ProgramData"), "Borealis", "Logs")
	if finalLogDir == "" || finalLogDir == "Borealis\\Logs" {
		finalLogDir = filepath.Join(os.Getenv("SystemDrive")+`\`, "ProgramData", "Borealis", "Logs")
	}
	_ = os.MkdirAll(finalLogDir, 0755)
	return filepath.Join(finalLogDir, "bootstrap-uninstall.log")
}

func appendUninstallLog(path string, message string) {
	line := fmt.Sprintf("[%s] %s\r\n", time.Now().Format(time.RFC3339), message)
	handle, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	_, _ = handle.WriteString(line)
	_ = handle.Close()
}

func removeBorealisOwnedServices(logger *BootstrapLogger) {
	logger.Tracef("Removing Borealis-owned services.")
	uninstallWireGuardTunnel("wireguard", logger)
	uninstallWireGuardTunnel("Borealis", logger)
	uninstallWireGuardTunnel("borealis-wg", logger)
	services := removableServiceNames(logger)
	logger.Tracef("Borealis-owned service candidates: %v", services)
	for _, service := range services {
		stopServiceAndWait(service, 30*time.Second, logger)
		deleteServiceAndWait(service, 30*time.Second, logger)
	}
}

type windowsServiceInfo struct {
	Name        string `json:"Name"`
	DisplayName string `json:"DisplayName"`
	PathName    string `json:"PathName"`
	State       string `json:"State"`
}

func removableServiceNames(logger *BootstrapLogger) []string {
	names := map[string]bool{
		"Borealis Agent - Bootstrapper": true,
		"Borealis Agent - UltraVNC":     true,
		"Borealis Agent - WireGuard":    true,
		"BorealisOnboarding":            true,
		"BorealisAgentBootstrap":        true,
		"BorealisAgentBootstrapper":     true,
		"BorealisAgentUltraVNC":         true,
		"BorealisWireGuardTunnel":       true,
		"uvnc_service":                  true,
		"uvnc_service_64":               true,
		"UltraVNC":                      true,
		"WinVNC":                        true,
		"WireGuardManager":              true,
		"WireGuardTunnel$wireguard":     true,
		"WireGuardTunnel$Borealis":      true,
		"WireGuardTunnel$borealis-wg":   true,
	}
	for _, service := range queryServiceInfos(logger) {
		blob := strings.ToLower(strings.Join([]string{service.Name, service.DisplayName, service.PathName}, " "))
		if strings.Contains(blob, "borealis") ||
			strings.Contains(blob, "ultravnc") ||
			strings.Contains(blob, "uvnc") ||
			strings.Contains(blob, "wireguard") {
			name := strings.TrimSpace(service.Name)
			if name != "" {
				names[name] = true
			}
		}
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func queryServiceInfos(logger *BootstrapLogger) []windowsServiceInfo {
	services, err := queryServiceInfosPowerShell()
	if err == nil {
		return services
	}
	if logger != nil {
		logger.Tracef("PowerShell service inventory failed: %v", err)
	}
	return queryServiceInfosSC()
}

func queryServiceInfosPowerShell() ([]windowsServiceInfo, error) {
	script := `$ErrorActionPreference='Stop'; Get-CimInstance Win32_Service | Select-Object Name,DisplayName,PathName,State | ConvertTo-Json -Compress -Depth 3`
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, powershellPath(), "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	raw := strings.TrimSpace(string(output))
	if raw == "" || raw == "null" {
		return nil, nil
	}
	var services []windowsServiceInfo
	if err := json.Unmarshal([]byte(raw), &services); err != nil {
		var service windowsServiceInfo
		if singleErr := json.Unmarshal([]byte(raw), &service); singleErr != nil {
			return nil, err
		}
		services = []windowsServiceInfo{service}
	}
	return services, nil
}

func queryServiceInfosSC() []windowsServiceInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "sc.exe", "queryex", "type=", "service", "state=", "all").CombinedOutput()
	if err != nil {
		return nil
	}
	names := []windowsServiceInfo{}
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToUpper(line), "SERVICE_NAME:") {
			continue
		}
		name := strings.TrimSpace(strings.TrimPrefix(line, "SERVICE_NAME:"))
		if name != "" {
			names = append(names, windowsServiceInfo{Name: name})
		}
	}
	return names
}

func uninstallWireGuardTunnel(tunnelName string, logger *BootstrapLogger) {
	logger.Tracef("WireGuard tunnel uninstall requested: %s", tunnelName)
	for _, candidate := range wireGuardExeCandidates() {
		if candidate != "wireguard.exe" && !fileExists(candidate) {
			continue
		}
		logger.Tracef("WireGuard tunnel uninstall command: executable=%s tunnel=%s", candidate, tunnelName)
		_, _ = runCommandTimeout(logger, 30*time.Second, candidate, "/uninstalltunnelservice", tunnelName)
		return
	}
	logger.Tracef("WireGuard executable missing; tunnel uninstall skipped: %s", tunnelName)
}

func uninstallWireGuardManagerService(logger *BootstrapLogger) {
	logger.Tracef("WireGuard manager service uninstall requested.")
	for _, candidate := range wireGuardExeCandidates() {
		if candidate != "wireguard.exe" && !fileExists(candidate) {
			continue
		}
		logger.Tracef("WireGuard manager uninstall command: executable=%s", candidate)
		_, _ = runCommandTimeout(logger, 30*time.Second, candidate, "/uninstallmanagerservice")
		return
	}
	logger.Tracef("WireGuard executable missing; manager uninstall skipped.")
}

func wireGuardExeCandidates() []string {
	programFiles := os.Getenv("ProgramFiles")
	programFilesX86 := os.Getenv("ProgramFiles(x86)")
	candidates := []string{
		filepath.Join(programFiles, "WireGuard", "wireguard.exe"),
		filepath.Join(programFilesX86, "WireGuard", "wireguard.exe"),
		"wireguard.exe",
	}
	seen := map[string]bool{}
	result := []string{}
	for _, candidate := range candidates {
		cleaned := strings.TrimSpace(candidate)
		if cleaned == "" || seen[strings.ToLower(cleaned)] {
			continue
		}
		seen[strings.ToLower(cleaned)] = true
		result = append(result, cleaned)
	}
	return result
}

func stopServiceAndWait(name string, timeout time.Duration, logger *BootstrapLogger) {
	state, exists := queryServiceState(name)
	if !exists || strings.EqualFold(state, "STOPPED") {
		logger.Tracef("Service stop skipped: name=%s exists=%t state=%s", name, exists, state)
		return
	}
	logger.Tracef("Service stop requested: name=%s state=%s", name, state)
	_, _ = runCommandTimeout(logger, 20*time.Second, "sc.exe", "stop", name)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state, exists := queryServiceState(name)
		if !exists || strings.EqualFold(state, "STOPPED") {
			logger.Tracef("Service stopped: name=%s exists=%t state=%s", name, exists, state)
			return
		}
		time.Sleep(1 * time.Second)
	}
	logger.Warnf("Service did not stop before timeout: %s", name)
}

func deleteServiceAndWait(name string, timeout time.Duration, logger *BootstrapLogger) {
	if _, exists := queryServiceState(name); !exists {
		logger.Tracef("Service delete skipped; missing: name=%s", name)
		return
	}
	logger.Tracef("Service delete requested: name=%s", name)
	_, _ = runCommandTimeout(logger, 20*time.Second, "sc.exe", "delete", name)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, exists := queryServiceState(name)
		if !exists {
			logger.Tracef("Service deleted: name=%s", name)
			return
		}
		time.Sleep(1 * time.Second)
	}
	logger.Warnf("Service did not delete before timeout: %s", name)
}

func queryServiceState(name string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "sc.exe", "query", name).CombinedOutput()
	text := string(output)
	if err != nil && (strings.Contains(text, "1060") || strings.Contains(strings.ToLower(text), "does not exist")) {
		return "", false
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToUpper(line), "STATE") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 4 {
			return strings.ToUpper(strings.TrimSpace(fields[3])), true
		}
	}
	return "", err == nil
}

func removeBorealisScheduledTasks(logger *BootstrapLogger) {
	logger.Tracef("Removing Borealis scheduled tasks.")
	taskNames := map[string]bool{
		agentTaskName:        true,
		agentUpdaterTaskName: true,
	}
	for _, taskName := range queryBorealisScheduledTasks(logger) {
		if strings.TrimSpace(taskName) != "" {
			taskNames[taskName] = true
		}
	}
	names := make([]string, 0, len(taskNames))
	for name := range taskNames {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		stopScheduledTask(name, logger)
		deleteScheduledTask(name, logger)
	}
}

func queryBorealisScheduledTasks(logger *BootstrapLogger) []string {
	script := `$ErrorActionPreference='Stop'; Get-ScheduledTask | Where-Object { $_.TaskName -like '*Borealis*' -or $_.TaskPath -like '*Borealis*' } | ForEach-Object { ($_.TaskPath.TrimEnd('\') + '\' + $_.TaskName).TrimStart('\') }`
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, powershellPath(), "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil || ctx.Err() != nil {
		if logger != nil {
			logger.Tracef("Scheduled task inventory failed: err=%v output=%s", err, strings.TrimSpace(string(output)))
		}
		return nil
	}
	names := []string{}
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			names = append(names, line)
		}
	}
	return names
}

func stopBorealisDependencyProcesses(cfg BootstrapConfig, logger *BootstrapLogger) {
	logger.Tracef("Stopping Borealis dependency processes.")
	for _, name := range []string{"winvnc.exe", "winvnc64.exe", "uvnc_service.exe", "wireguard.exe"} {
		err := eachProcess(func(pid uint32, exe string, commandLine string) {
			lower := strings.ToLower(commandLine + " " + exe)
			if !strings.Contains(lower, "borealis") &&
				!strings.Contains(lower, "ultravnc") &&
				!strings.Contains(lower, "uvnc") &&
				!strings.Contains(lower, "wireguard") {
				return
			}
			if int(pid) == os.Getpid() {
				return
			}
			logger.Tracef("Dependency process matched for uninstall: pid=%d exe=%s command_line=%s", pid, exe, commandLine)
			killProcessTree(int(pid))
		}, name)
		if err != nil {
			logger.Tracef("Dependency process scan failed: image=%s error=%v", name, err)
		}
	}
}

func removeDependencyInstallRoots(logger *BootstrapLogger) {
	for _, root := range dependencyInstallRoots() {
		if !dirExists(root) {
			continue
		}
		logger.Tracef("Removing dependency install root: %s", root)
		clearInstallDirAttributes(root, logger)
		if err := removePathWithRetries(root, 5, 2*time.Second, logger); err != nil {
			logger.Warnf("Dependency install root removal failed: path=%s error=%v", root, err)
		}
	}
}

func dependencyInstallRoots() []string {
	candidates := []string{}
	for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
		base = strings.TrimSpace(base)
		if base == "" {
			continue
		}
		candidates = append(
			candidates,
			filepath.Join(base, "WireGuard"),
			filepath.Join(base, "UltraVNC"),
			filepath.Join(base, "uvnc bvba", "UltraVNC"),
		)
	}
	seen := map[string]bool{}
	roots := []string{}
	for _, candidate := range candidates {
		cleaned := strings.TrimSpace(filepath.Clean(candidate))
		lower := strings.ToLower(cleaned)
		if cleaned == "." || cleaned == `\` || cleaned == `/` || seen[lower] {
			continue
		}
		seen[lower] = true
		roots = append(roots, cleaned)
	}
	return roots
}

func clearInstallDirAttributes(path string, logger *BootstrapLogger) {
	if !dirExists(path) {
		return
	}
	logger.Tracef("Clearing install directory attributes before removal: %s", path)
	_, _ = runCommandTimeout(logger, 60*time.Second, "attrib.exe", "-R", "-S", "-H", filepath.Join(path, "*"), "/S", "/D")
}

func removeInstallDirWithRetries(path string, finalLog string, logger *BootstrapLogger) error {
	var lastErr error
	for attempt := 1; attempt <= 15; attempt++ {
		if !dirExists(path) {
			logger.Tracef("Install directory already removed: %s", path)
			return nil
		}
		if attempt == 1 || attempt%3 == 0 {
			clearInstallDirAttributes(path, logger)
		}
		logger.Tracef("Install directory removal attempt %d: %s", attempt, path)
		if err := os.RemoveAll(path); err != nil {
			lastErr = err
			appendUninstallLog(finalLog, fmt.Sprintf("Remove attempt %d failed: %v", attempt, err))
			logger.Warnf("Install directory removal attempt %d failed: %v", attempt, err)
			time.Sleep(2 * time.Second)
			continue
		}
		if !dirExists(path) {
			return nil
		}
		lastErr = fmt.Errorf("install directory still exists after remove attempt %d", attempt)
		time.Sleep(2 * time.Second)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("install directory still exists: %s", path)
	}
	return lastErr
}

func launchDeferredUninstallCleanup(cfg BootstrapConfig, finalLog string, logger *BootstrapLogger) error {
	cleanupPath := filepath.Join(filepath.Dir(finalLog), "finish-uninstall.ps1")
	script := buildDeferredUninstallScript(cfg.InstallDir, finalLog)
	if err := os.WriteFile(cleanupPath, []byte(script), 0644); err != nil {
		return err
	}
	cmd := exec.Command(powershellPath(), "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", cleanupPath)
	cmd.Dir = filepath.Dir(finalLog)
	if err := cmd.Start(); err != nil {
		return err
	}
	logger.Infof("Deferred cleanup pid=%d", cmd.Process.Pid)
	return nil
}

func buildDeferredUninstallScript(installDir string, finalLog string) string {
	install := escapePowerShellSingleQuotedString(installDir)
	logPath := escapePowerShellSingleQuotedString(finalLog)
	return fmt.Sprintf(`$ErrorActionPreference = 'SilentlyContinue'
$InstallDir = '%s'
$LogFile = '%s'
function Write-UninstallLog([string]$Message) {
  $stamp = Get-Date -Format o
  Add-Content -LiteralPath $LogFile -Value "[$stamp] $Message"
}
function Remove-BorealisTasks {
  schtasks.exe /End /TN "Borealis Agent" *> $null
  schtasks.exe /End /TN "Borealis Agent (AutoUpdater)" *> $null
  schtasks.exe /Delete /TN "Borealis Agent" /F *> $null
  schtasks.exe /Delete /TN "Borealis Agent (AutoUpdater)" /F *> $null
  Get-ScheduledTask | Where-Object { $_.TaskName -like '*Borealis*' -or $_.TaskPath -like '*Borealis*' } | ForEach-Object {
    $fullName = ($_.TaskPath.TrimEnd('\') + '\' + $_.TaskName).TrimStart('\')
    schtasks.exe /End /TN $fullName *> $null
    schtasks.exe /Delete /TN $fullName /F *> $null
  }
}
function Remove-BorealisServices {
  foreach ($wg in @(
    "$env:ProgramFiles\WireGuard\wireguard.exe",
    "${env:ProgramFiles(x86)}\WireGuard\wireguard.exe"
  )) {
    if (Test-Path -LiteralPath $wg) {
      & $wg /uninstalltunnelservice wireguard *> $null
      & $wg /uninstalltunnelservice Borealis *> $null
      & $wg /uninstalltunnelservice borealis-wg *> $null
      & $wg /uninstallmanagerservice *> $null
    }
  }
  $static = @(
    'BorealisOnboarding',
    'BorealisAgentBootstrap',
    'BorealisAgentBootstrapper',
    'BorealisAgentUltraVNC',
    'BorealisWireGuardTunnel',
    'WireGuardManager',
    'WireGuardTunnel$wireguard',
    'WireGuardTunnel$Borealis',
    'WireGuardTunnel$borealis-wg',
    'uvnc_service',
    'uvnc_service_64',
    'UltraVNC',
    'WinVNC'
  )
  foreach ($name in $static) {
    sc.exe stop $name *> $null
    sc.exe delete $name *> $null
  }
  Get-CimInstance Win32_Service | Where-Object {
    $blob = "$($_.Name) $($_.DisplayName) $($_.PathName)".ToLowerInvariant()
    $blob.Contains('borealis') -or $blob.Contains('ultravnc') -or $blob.Contains('uvnc') -or $blob.Contains('wireguard')
  } | ForEach-Object {
    sc.exe stop $_.Name *> $null
    sc.exe delete $_.Name *> $null
  }
}
function Stop-BorealisProcesses {
  Get-CimInstance Win32_Process | Where-Object {
    $name = [string]$_.Name
    $blob = "$($_.ExecutablePath) $($_.CommandLine)".ToLowerInvariant()
    (($blob.Contains($InstallDir.ToLowerInvariant()) -or $blob.Contains('borealis')) -and $_.ProcessId -ne $PID) -or
    ($name -in @('winvnc.exe','winvnc64.exe','uvnc_service.exe','wireguard.exe'))
  } | ForEach-Object {
    taskkill.exe /PID $_.ProcessId /T /F *> $null
  }
}
function Remove-DependencyRoots {
  $roots = @()
  foreach ($base in @($env:ProgramFiles, ${env:ProgramFiles(x86)})) {
    if ([string]::IsNullOrWhiteSpace($base)) {
      continue
    }
    $roots += Join-Path $base 'WireGuard'
    $roots += Join-Path $base 'UltraVNC'
    $roots += Join-Path (Join-Path $base 'uvnc bvba') 'UltraVNC'
  }
  foreach ($root in $roots) {
    if (-not $root -or -not (Test-Path -LiteralPath $root)) {
      continue
    }
    attrib.exe -R -S -H (Join-Path $root '*') /S /D *> $null
    Remove-Item -LiteralPath $root -Recurse -Force *> $null
  }
}
Write-UninstallLog 'Deferred Borealis cleanup started.'
Start-Sleep -Seconds 3
for ($attempt = 1; $attempt -le 12; $attempt++) {
  Remove-BorealisTasks
  Remove-BorealisServices
  Stop-BorealisProcesses
  Remove-DependencyRoots
  if (Test-Path -LiteralPath $InstallDir) {
    attrib.exe -R -S -H (Join-Path $InstallDir '*') /S /D *> $null
    Remove-Item -LiteralPath $InstallDir -Recurse -Force *> $null
  }
  if (-not (Test-Path -LiteralPath $InstallDir)) {
    Write-UninstallLog "Removed $InstallDir."
    Remove-Item -LiteralPath $PSCommandPath -Force *> $null
    exit 0
  }
  Start-Sleep -Seconds 3
}
Write-UninstallLog "Failed to remove $InstallDir."
exit 1
`, install, logPath)
}

func escapePowerShellSingleQuotedString(value string) string {
	escaped := strings.ReplaceAll(value, `'`, `''`)
	return escaped
}
