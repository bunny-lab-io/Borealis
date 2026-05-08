//go:build windows

package main

import (
	"context"
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

func acquireBootstrapMutex() (func(), bool, error) {
	name, err := windows.UTF16PtrFromString(`Global\BorealisAgentBootstrap`)
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
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if text != "" {
		logger.Infof("%s", text)
	}
	if ctx.Err() != nil {
		return text, ctx.Err()
	}
	if err != nil {
		return text, fmt.Errorf("%s %s failed: %w", name, strings.Join(args, " "), err)
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
	processNames := []string{"python.exe", "pythonw.exe", "powershell.exe"}
	for _, name := range processNames {
		_ = eachProcess(func(pid uint32, exe string, commandLine string) {
			lowerCmd := strings.ToLower(commandLine + " " + exe)
			if !strings.Contains(lowerCmd, strings.ToLower(installRoot)) {
				return
			}
			if int(pid) == os.Getpid() {
				return
			}
			logger.Marker("__BOREALIS_ONBOARDING_STALE_PROCESS_KILLED__=" + strconv.Itoa(int(pid)))
			killProcessTree(int(pid))
		}, name)
	}
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
	for _, path := range []string{
		cfg.InstallDir,
		filepath.Join(cfg.InstallDir, "Agent", "Logs"),
		filepath.Join(cfg.InstallDir, "Temp", "Onboarding"),
		filepath.Join(cfg.InstallDir, "Dependencies"),
	} {
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
	if samePath(exe, destination) {
		return nil
	}
	if err := copyFile(exe, destination); err != nil {
		return err
	}
	logger.Infof("Agent.exe staged at %s", destination)
	return nil
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
