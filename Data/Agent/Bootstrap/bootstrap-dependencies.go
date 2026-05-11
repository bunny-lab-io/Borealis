//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	ultraVNCServiceName         = "BorealisAgentUltraVNC"
	ultraVNCServiceDisplayName  = "Borealis Agent - UltraVNC"
	wireGuardManagerServiceName = "WireGuardManager"
	wireGuardManagerDisplayName = "Borealis Agent - WireGuard Manager"
)

func ensureAgentDependencies(cfg BootstrapConfig, logger *BootstrapLogger) error {
	startedAt := time.Now()
	logger.Tracef("Dependency coordinator start.")
	writeTimeline(cfg, "running", dependencyTaskName("Python"), "Installing Python runtime.", 1)
	if err := ensurePythonRuntime(cfg, logger); err != nil {
		return err
	}
	writeTimeline(cfg, "completed", dependencyTaskName("Python"), "Python dependency ready.", 0)
	optionalSteps := []struct {
		name string
		fn   func(BootstrapConfig, *BootstrapLogger) error
	}{
		{"Git CLI", ensureGitCLI},
		{"UltraVNC Server", ensureUltraVNCServer},
		{"WireGuard VPN Adapter", ensureWireGuardInstaller},
	}
	for _, step := range optionalSteps {
		stepStartedAt := time.Now()
		task := dependencyTaskName(step.name)
		logger.Tracef("Dependency step start: %s", step.name)
		writeTimeline(cfg, "running", task, "Installing "+step.name+".", 1)
		if err := step.fn(cfg, logger); err != nil {
			logger.Warnf("%s dependency deferred: %v", step.name, err)
			writeTimeline(cfg, "completed", task, step.name+" dependency deferred: "+err.Error(), 0)
			logger.Tracef("Dependency step deferred: %s duration=%s error=%v", step.name, time.Since(stepStartedAt).Round(time.Millisecond), err)
			continue
		}
		writeTimeline(cfg, "completed", task, step.name+" dependency ready.", 0)
		logger.Tracef("Dependency step complete: %s duration=%s", step.name, time.Since(stepStartedAt).Round(time.Millisecond))
	}
	logger.Tracef("Dependency coordinator complete duration=%s.", time.Since(startedAt).Round(time.Millisecond))
	return nil
}

func dependencyTaskName(name string) string {
	return "Installing Agent Dependencies: " + name
}

func ensureGitCLI(cfg BootstrapConfig, logger *BootstrapLogger) error {
	gitExe := filepath.Join(cfg.InstallDir, "Dependencies", "git", "cmd", "git.exe")
	if fileExists(gitExe) {
		logger.Tracef("Git CLI already installed at %s", gitExe)
		return nil
	}
	zipPath := filepath.Join(cfg.InstallDir, "Dependencies", "MinGit-2.47.1-64-bit.zip")
	logger.Tracef("Git CLI download/stage start: zip=%s", zipPath)
	if err := downloadFileLogged(context.Background(), "https://github.com/git-for-windows/git/releases/download/v2.47.1.windows.1/MinGit-2.47.1-64-bit.zip", zipPath, 240*time.Second, logger); err != nil {
		return err
	}
	installDir := filepath.Join(cfg.InstallDir, "Dependencies", "git")
	_ = os.RemoveAll(installDir)
	if err := unzipFileLogged(zipPath, installDir, logger); err != nil {
		return err
	}
	_ = os.Remove(zipPath)
	if !fileExists(gitExe) {
		return fmt.Errorf("git.exe not found after extraction")
	}
	logger.Infof("Git CLI installed.")
	return nil
}

func ensureUltraVNCPayload(cfg BootstrapConfig, logger *BootstrapLogger) error {
	root := filepath.Join(cfg.InstallDir, "Dependencies", "UltraVNC_Server")
	if fileExists(filepath.Join(root, "payload", "x64", "winvnc.exe")) || fileExists(filepath.Join(root, "payload", "x86", "winvnc.exe")) {
		logger.Tracef("UltraVNC payload already present under %s", root)
		return nil
	}
	zipPath := filepath.Join(root, "UltraVNC_1640.zip")
	logger.Tracef("UltraVNC payload download/stage start: zip=%s", zipPath)
	if err := downloadFileLogged(context.Background(), "https://uvnc.eu/download/1640/UltraVNC_1640.zip", zipPath, 240*time.Second, logger); err != nil {
		return err
	}
	payloadRoot := filepath.Join(root, "payload")
	_ = os.RemoveAll(payloadRoot)
	if err := unzipFileLogged(zipPath, payloadRoot, logger); err != nil {
		return err
	}
	logger.Infof("UltraVNC payload staged.")
	return nil
}

func ensureUltraVNCServer(cfg BootstrapConfig, logger *BootstrapLogger) error {
	if err := ensureUltraVNCPayload(cfg, logger); err != nil {
		return err
	}
	exePath, err := resolveUltraVNCBootstrapExe(cfg)
	if err != nil {
		return err
	}
	configPath, err := ensureUltraVNCBootstrapConfig(cfg, logger)
	if err != nil {
		return err
	}
	return ensureUltraVNCServiceRegistration(exePath, configPath, logger)
}

func resolveUltraVNCBootstrapExe(cfg BootstrapConfig) (string, error) {
	candidates := []string{
		filepath.Join(cfg.InstallDir, "Agent", "Borealis", "Tools", "UltraVNC", "Server", "winvnc.exe"),
		filepath.Join(cfg.InstallDir, "Agent", "Borealis", "Tools", "UltraVNC", "Server", "winvnc64.exe"),
		filepath.Join(cfg.InstallDir, "Dependencies", "UltraVNC_Server", "payload", "x64", "winvnc.exe"),
		filepath.Join(cfg.InstallDir, "Dependencies", "UltraVNC_Server", "payload", "x64", "winvnc64.exe"),
		filepath.Join(cfg.InstallDir, "Dependencies", "UltraVNC_Server", "payload", "x86", "winvnc.exe"),
	}
	for _, candidate := range candidates {
		if fileExists(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("UltraVNC winvnc.exe not found after payload staging")
}

func ensureUltraVNCBootstrapConfig(cfg BootstrapConfig, logger *BootstrapLogger) (string, error) {
	configDir := filepath.Join(cfg.InstallDir, "Agent", "Borealis", "Settings", "UltraVNC")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", err
	}
	configPath := filepath.Join(configDir, "ultravnc.ini")
	if fileExists(configPath) {
		logger.Tracef("UltraVNC config already present at %s", configPath)
		return configPath, nil
	}
	content := "[UltraVNC]\n" +
		"UseRegistry=0\n" +
		"AuthRequired=1\n" +
		"MSLogonRequired=0\n" +
		"NewMSLogon=0\n" +
		"PortNumber=5900\n" +
		"AutoPortSelect=0\n" +
		"SocketConnect=1\n" +
		"AllowLoopback=1\n" +
		"LoopbackOnly=0\n" +
		"HTTPConnect=0\n" +
		"AllowShutdown=1\n" +
		"DisableTrayIcon=1\n" +
		"EnableFileTransfer=0\n" +
		"RemoveWallpaper=1\n"
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		return "", err
	}
	logger.Tracef("UltraVNC bootstrap config written at %s", configPath)
	return configPath, nil
}

func ensureUltraVNCServiceRegistration(exePath string, configPath string, logger *BootstrapLogger) error {
	removeLegacyUltraVNCServices(logger)
	desiredBinPath := fmt.Sprintf(`"%s" -service -config "%s"`, exePath, configPath)
	if _, err := runCommandTimeout(logger, 20*time.Second, "sc.exe", "query", ultraVNCServiceName); err != nil {
		if _, createErr := runCommandTimeout(
			logger,
			30*time.Second,
			"sc.exe",
			"create",
			ultraVNCServiceName,
			"binPath=",
			desiredBinPath,
			"start=",
			"demand",
			"type=",
			"own",
			"DisplayName=",
			ultraVNCServiceDisplayName,
		); createErr != nil {
			return createErr
		}
		logger.Infof("UltraVNC service registered.")
	}
	_, err := runCommandTimeout(
		logger,
		30*time.Second,
		"sc.exe",
		"config",
		ultraVNCServiceName,
		"binPath=",
		desiredBinPath,
		"start=",
		"demand",
		"DisplayName=",
		ultraVNCServiceDisplayName,
	)
	if err != nil {
		return err
	}
	logger.Tracef("UltraVNC service registration verified: service=%s bin_path=%s", ultraVNCServiceName, desiredBinPath)
	return nil
}

func removeLegacyUltraVNCServices(logger *BootstrapLogger) {
	for _, service := range []string{"uvnc_service", "uvnc_service_64", "UltraVNC", "WinVNC"} {
		if service == ultraVNCServiceName {
			continue
		}
		binPath := queryServiceBinPath(service, logger)
		if binPath == "" || !strings.Contains(strings.ToLower(binPath), `\borealis\`) {
			continue
		}
		logger.Tracef("Removing legacy Borealis UltraVNC service: name=%s bin_path=%s", service, binPath)
		stopServiceAndWait(service, 30*time.Second, logger)
		deleteServiceAndWait(service, 30*time.Second, logger)
	}
}

func queryServiceBinPath(service string, logger *BootstrapLogger) string {
	output, err := runCommandTimeout(logger, 20*time.Second, "sc.exe", "qc", service)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(strings.ToUpper(line), "BINARY_PATH_NAME") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(parts[1])
		}
	}
	return ""
}

func ensureWireGuardInstaller(cfg BootstrapConfig, logger *BootstrapLogger) error {
	root := filepath.Join(cfg.InstallDir, "Dependencies", "VPN_Tunnel_Adapter")
	installer := filepath.Join(root, "wireguard-installer.exe")
	if !fileExists(installer) {
		logger.Tracef("WireGuard installer missing; downloading to %s", installer)
		if err := downloadFileLogged(context.Background(), "https://download.wireguard.com/windows-client/wireguard-installer.exe", installer, 240*time.Second, logger); err != nil {
			return err
		}
	} else {
		logger.Tracef("WireGuard installer already present at %s", installer)
	}
	logger.Tracef("WireGuard manager service install command starting.")
	_, err := runCommandTimeout(logger, 180*time.Second, installer, "/installmanagerservice")
	if err != nil {
		return err
	}
	ensureWireGuardManagerServiceDisplayName(logger)
	logger.Infof("WireGuard manager service installed.")
	return nil
}

func ensureWireGuardManagerServiceDisplayName(logger *BootstrapLogger) {
	_, err := runCommandTimeout(
		logger,
		30*time.Second,
		"sc.exe",
		"config",
		wireGuardManagerServiceName,
		"DisplayName=",
		wireGuardManagerDisplayName,
	)
	if err != nil {
		logger.Tracef("WireGuard manager display-name update skipped: %v", err)
	}
}
