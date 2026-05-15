//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

const waitAbandoned = 0x00000080

type windowsProcessInfo struct {
	ProcessID      uint32 `json:"ProcessId"`
	ExecutablePath string `json:"ExecutablePath"`
	CommandLine    string `json:"CommandLine"`
}

func acquireBootstrapMutex() (func(), bool, error) {
	name, err := windows.UTF16PtrFromString(`Global\BorealisAgentBootstrapper`)
	if err != nil {
		return func() {}, false, err
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		return func() {}, false, err
	}
	waitResult, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		_ = windows.CloseHandle(handle)
		return func() {}, false, err
	}
	if waitResult != windows.WAIT_OBJECT_0 && waitResult != waitAbandoned {
		_ = windows.CloseHandle(handle)
		return func() {}, false, nil
	}
	release := func() {
		_ = windows.ReleaseMutex(handle)
		_ = windows.CloseHandle(handle)
	}
	return release, true, nil
}

func runCommand(ctx context.Context, logger *BootstrapLogger, name string, args ...string) (string, error) {
	startedAt := time.Now()
	if logger != nil {
		logger.Tracef("Command start: %s %s timeout_set=%t", name, strings.Join(args, " "), ctx != nil)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if text != "" {
		logger.Infof("%s", text)
	}
	if ctx.Err() != nil {
		if logger != nil {
			logger.Tracef("Command timeout: %s duration=%s error=%v", name, time.Since(startedAt).Round(time.Millisecond), ctx.Err())
		}
		return text, ctx.Err()
	}
	if err != nil {
		if logger != nil {
			logger.Tracef("Command failed: %s duration=%s error=%v", name, time.Since(startedAt).Round(time.Millisecond), err)
		}
		return text, fmt.Errorf("%s %s failed: %w", name, strings.Join(args, " "), err)
	}
	if logger != nil {
		logger.Tracef("Command complete: %s duration=%s output_bytes=%d", name, time.Since(startedAt).Round(time.Millisecond), len(output))
	}
	return text, nil
}

func runCommandTimeout(logger *BootstrapLogger, timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return runCommand(ctx, logger, name, args...)
}

func stopBorealisProcesses(cfg BootstrapConfig, logger *BootstrapLogger) {
	installRoot := strings.ToLower(filepath.Clean(cfg.InstallDir))
	logger.Tracef("Scanning for stale Borealis processes under %s.", cfg.InstallDir)
	killed := 0
	processNames := []string{
		"powershell.exe",
		"Agent.exe",
		"winvnc.exe",
		"winvnc64.exe",
		"wireguard.exe",
	}
	for _, name := range processNames {
		logger.Tracef("Process scan start: image=%s", name)
		err := eachProcess(func(pid uint32, exe string, commandLine string) {
			lowerCmd := strings.ToLower(commandLine + " " + exe)
			if !strings.Contains(lowerCmd, strings.ToLower(installRoot)) {
				return
			}
			if int(pid) == os.Getpid() {
				return
			}
			logger.Tracef("Stale process matched: pid=%d exe=%s command_line=%s", pid, exe, commandLine)
			logger.Marker("__BOREALIS_ONBOARDING_STALE_PROCESS_KILLED__=" + strconv.Itoa(int(pid)))
			killProcessTree(int(pid))
			killed++
		}, name)
		if err != nil {
			logger.Tracef("Process scan failed: image=%s error=%v", name, err)
		}
	}
	logger.Tracef("Stale process scan complete: killed=%d", killed)
}

func killProcessTree(pid int) {
	if pid <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "taskkill.exe", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
}

func eachProcess(callback func(pid uint32, exe string, commandLine string), imageName string) error {
	if err := eachProcessPowerShell(callback, imageName); err == nil {
		return nil
	} else {
		wmicErr := eachProcessWMIC(callback, imageName)
		if wmicErr == nil {
			return nil
		}
		return fmt.Errorf("powershell process query failed: %v; wmic process query failed: %w", err, wmicErr)
	}
}

func eachProcessPowerShell(callback func(pid uint32, exe string, commandLine string), imageName string) error {
	escapedName := strings.ReplaceAll(imageName, "'", "''")
	script := fmt.Sprintf(
		`$ErrorActionPreference='Stop'; $items = @(Get-CimInstance Win32_Process -Filter "Name='%s'" | Select-Object ProcessId,ExecutablePath,CommandLine); if ($items.Count -gt 0) { ConvertTo-Json -InputObject $items -Compress -Depth 3 }`,
		escapedName,
	)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, powershellPath(), "-NoProfile", "-NonInteractive", "-Command", script)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	raw := strings.TrimSpace(string(output))
	if raw == "" || raw == "null" {
		return nil
	}
	var processes []windowsProcessInfo
	if err := json.Unmarshal([]byte(raw), &processes); err != nil {
		var process windowsProcessInfo
		if singleErr := json.Unmarshal([]byte(raw), &process); singleErr != nil {
			return err
		}
		processes = []windowsProcessInfo{process}
	}
	for _, process := range processes {
		if process.ProcessID == 0 {
			continue
		}
		callback(process.ProcessID, process.ExecutablePath, process.CommandLine)
	}
	return nil
}

func eachProcessWMIC(callback func(pid uint32, exe string, commandLine string), imageName string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "wmic.exe", "process", "where", fmt.Sprintf("name='%s'", imageName), "get", "ProcessId,ExecutablePath,CommandLine", "/FORMAT:CSV")
	output, err := cmd.Output()
	if err != nil {
		return err
	}
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(strings.ToLower(line), "node,") {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) < 4 {
			continue
		}
		pidRaw := strings.TrimSpace(parts[len(parts)-1])
		pid64, err := strconv.ParseUint(pidRaw, 10, 32)
		if err != nil {
			continue
		}
		commandLine := strings.Join(parts[1:len(parts)-2], ",")
		exe := parts[len(parts)-2]
		callback(uint32(pid64), exe, commandLine)
	}
	return nil
}

func currentHostname() string {
	candidates := []string{}
	if hostname, err := os.Hostname(); err == nil {
		candidates = append(candidates, hostname)
	}
	candidates = append(candidates, os.Getenv("COMPUTERNAME"))
	for _, candidate := range candidates {
		value := strings.TrimSpace(candidate)
		value = strings.Trim(value, "[]")
		if value == "" {
			continue
		}
		if index := strings.IndexAny(value, "\r\n\t "); index >= 0 {
			value = strings.TrimSpace(value[:index])
		}
		if value != "" {
			return value
		}
	}
	return ""
}

func isInteractiveConsole() bool {
	var mode uint32
	handle := windows.Handle(os.Stdin.Fd())
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return false
	}
	return true
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func ensureBootstrapDirs(cfg BootstrapConfig) error {
	paths := []string{
		cfg.InstallDir,
		filepath.Join(cfg.InstallDir, "Logs"),
		filepath.Join(cfg.InstallDir, "Logs", "Agent"),
		filepath.Join(cfg.InstallDir, "Logs", "UltraVNC"),
		filepath.Join(cfg.InstallDir, "Logs", "WireGuard"),
		filepath.Join(cfg.InstallDir, "Temp", "Onboarding"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(path, 0755); err != nil {
			return err
		}
	}
	return nil
}

func copySelfToInstallRoot(cfg BootstrapConfig, logger *BootstrapLogger) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	destination := filepath.Join(cfg.InstallDir, "Agent.exe")
	logger.Tracef("Agent.exe self-stage check: source=%s destination=%s same_path=%t", exe, destination, samePath(exe, destination))
	if samePath(exe, destination) {
		logger.Tracef("Agent.exe already running from install root.")
		return nil
	}
	if err := copyFile(exe, destination); err != nil {
		return err
	}
	logger.Infof("Agent.exe staged at %s", destination)
	return nil
}

func removePathWithRetries(path string, attempts int, delay time.Duration, logger *BootstrapLogger) error {
	var lastErr error
	if attempts < 1 {
		attempts = 1
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		if !dirExists(path) && !fileExists(path) {
			return nil
		}
		if err := os.RemoveAll(path); err != nil {
			lastErr = err
			if logger != nil {
				logger.Warnf("Remove attempt %d/%d failed for %s: %v", attempt, attempts, path, err)
			}
		} else if !dirExists(path) && !fileExists(path) {
			return nil
		} else {
			lastErr = fmt.Errorf("path still exists after remove attempt %d", attempt)
			if logger != nil {
				logger.Warnf("Remove attempt %d/%d left path behind: %s", attempt, attempts, path)
			}
		}
		if attempt < attempts {
			time.Sleep(delay)
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("path still exists: %s", path)
	}
	return lastErr
}

func powershellPath() string {
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	candidate := filepath.Join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if fileExists(candidate) {
		return candidate
	}
	return "powershell.exe"
}
