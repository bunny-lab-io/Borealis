//go:build windows

package main

import (
	"context"
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

	stopScheduledTask(agentTaskName, logger)
	stopScheduledTask(agentUpdaterTaskName, logger)
	deleteScheduledTask(agentTaskName, logger)
	deleteScheduledTask(agentUpdaterTaskName, logger)
	removeBorealisOwnedServices(logger)
	stopBorealisProcesses(cfg, logger)
	removeBorealisOwnedServices(logger)

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
	uninstallWireGuardTunnel("Borealis", logger)
	uninstallWireGuardTunnel("borealis-wg", logger)
	services := borealisOwnedServiceNames(logger)
	logger.Tracef("Borealis-owned service candidates: %v", services)
	for _, service := range services {
		stopServiceAndWait(service, 30*time.Second, logger)
		deleteServiceAndWait(service, 30*time.Second, logger)
	}
}

func borealisOwnedServiceNames(logger *BootstrapLogger) []string {
	names := map[string]bool{
		"BorealisOnboarding":          true,
		"BorealisAgentBootstrap":      true,
		"BorealisWireGuardTunnel":     true,
		"WireGuardTunnel$Borealis":    true,
		"WireGuardTunnel$borealis-wg": true,
		"uvnc_service":                true,
		"uvnc_service_64":             true,
		"UltraVNC":                    true,
		"WinVNC":                      true,
	}
	for _, service := range queryServiceNames() {
		normalized := strings.ToLower(service)
		if strings.Contains(normalized, "borealis") || strings.Contains(normalized, "wireguardtunnel$borealis") || strings.Contains(normalized, "uvnc") {
			names[service] = true
		}
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func queryServiceNames() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "sc.exe", "queryex", "type=", "service", "state=", "all").CombinedOutput()
	if err != nil {
		return nil
	}
	names := []string{}
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToUpper(line), "SERVICE_NAME:") {
			continue
		}
		name := strings.TrimSpace(strings.TrimPrefix(line, "SERVICE_NAME:"))
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

func uninstallWireGuardTunnel(tunnelName string, logger *BootstrapLogger) {
	logger.Tracef("WireGuard tunnel uninstall requested: %s", tunnelName)
	for _, candidate := range []string{
		filepath.Join(os.Getenv("ProgramFiles"), "WireGuard", "wireguard.exe"),
		"wireguard.exe",
	} {
		if candidate != "wireguard.exe" && !fileExists(candidate) {
			continue
		}
		logger.Tracef("WireGuard tunnel uninstall command: executable=%s tunnel=%s", candidate, tunnelName)
		_, _ = runCommandTimeout(logger, 30*time.Second, candidate, "/uninstalltunnelservice", tunnelName)
		return
	}
	logger.Tracef("WireGuard executable missing; tunnel uninstall skipped: %s", tunnelName)
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

func removeInstallDirWithRetries(path string, finalLog string, logger *BootstrapLogger) error {
	var lastErr error
	for attempt := 1; attempt <= 10; attempt++ {
		if !dirExists(path) {
			logger.Tracef("Install directory already removed: %s", path)
			return nil
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
	cleanupPath := filepath.Join(filepath.Dir(finalLog), "finish-uninstall.cmd")
	script := buildDeferredUninstallScript(cfg.InstallDir, finalLog)
	if err := os.WriteFile(cleanupPath, []byte(script), 0644); err != nil {
		return err
	}
	cmd := exec.Command("cmd.exe", "/C", cleanupPath)
	cmd.Dir = filepath.Dir(finalLog)
	if err := cmd.Start(); err != nil {
		return err
	}
	logger.Infof("Deferred cleanup pid=%d", cmd.Process.Pid)
	return nil
}

func buildDeferredUninstallScript(installDir string, finalLog string) string {
	install := escapeCmdSetValue(installDir)
	logPath := escapeCmdSetValue(finalLog)
	return fmt.Sprintf(`@echo off
setlocal EnableExtensions
set "INSTALL_DIR=%s"
set "LOG_FILE=%s"
echo [%%DATE%% %%TIME%%] Deferred Borealis cleanup started.>>"%%LOG_FILE%%"
cd /d %%SystemRoot%%
timeout /t 3 /nobreak >nul
for /l %%%%I in (1,1,6) do (
  schtasks.exe /End /TN "Borealis Agent" >nul 2>&1
  schtasks.exe /End /TN "Borealis Agent (AutoUpdater)" >nul 2>&1
  schtasks.exe /Delete /TN "Borealis Agent" /F >nul 2>&1
  schtasks.exe /Delete /TN "Borealis Agent (AutoUpdater)" /F >nul 2>&1
  for %%%%S in ("BorealisOnboarding" "BorealisAgentBootstrap" "BorealisWireGuardTunnel" "WireGuardTunnel$Borealis" "WireGuardTunnel$borealis-wg" "uvnc_service" "uvnc_service_64" "UltraVNC" "WinVNC") do (
    sc.exe stop %%%%~S >nul 2>&1
    sc.exe delete %%%%~S >nul 2>&1
  )
  wmic.exe process where "ExecutablePath like '%%INSTALL_DIR%%\\%%%%' or CommandLine like '%%%%%%INSTALL_DIR%%%%%%'" call terminate >nul 2>&1
  timeout /t 2 /nobreak >nul
  rmdir /s /q "%%INSTALL_DIR%%" >nul 2>&1
  if not exist "%%INSTALL_DIR%%" (
    echo [%%DATE%% %%TIME%%] Removed %%INSTALL_DIR%%.>>"%%LOG_FILE%%"
    del "%%~f0" >nul 2>&1
    exit /b 0
  )
)
echo [%%DATE%% %%TIME%%] Failed to remove %%INSTALL_DIR%%.>>"%%LOG_FILE%%"
exit /b 1
`, install, logPath)
}

func escapeCmdSetValue(value string) string {
	escaped := strings.ReplaceAll(value, `"`, `""`)
	escaped = strings.ReplaceAll(escaped, `%`, `%%`)
	return escaped
}
