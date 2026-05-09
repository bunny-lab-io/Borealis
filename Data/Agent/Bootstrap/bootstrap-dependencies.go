//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
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
		{"AutoHotKey", ensureAutoHotKey},
		{"Git CLI", ensureGitCLI},
		{"UltraVNC Server", ensureUltraVNCPayload},
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

func ensureAutoHotKey(cfg BootstrapConfig, logger *BootstrapLogger) error {
	installDir := filepath.Join(cfg.InstallDir, "Dependencies", "AutoHotKey")
	exePath := filepath.Join(installDir, "AutoHotkey64.exe")
	if fileExists(exePath) {
		logger.Tracef("AutoHotKey already installed at %s", exePath)
		return nil
	}
	zipPath := filepath.Join(cfg.InstallDir, "Dependencies", "AutoHotkey_2.0.19.zip")
	logger.Tracef("AutoHotKey download/stage start: zip=%s install_dir=%s", zipPath, installDir)
	if err := downloadFileLogged(context.Background(), "https://github.com/AutoHotkey/AutoHotkey/releases/download/v2.0.19/AutoHotkey_2.0.19.zip", zipPath, 180*time.Second, logger); err != nil {
		return err
	}
	_ = os.RemoveAll(installDir)
	if err := unzipFileLogged(zipPath, installDir, logger); err != nil {
		return err
	}
	_ = os.Remove(zipPath)
	if !fileExists(exePath) {
		return fmt.Errorf("AutoHotkey64.exe not found after extraction")
	}
	logger.Infof("AutoHotKey installed.")
	return nil
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
	logger.Infof("WireGuard manager service installed.")
	return nil
}
