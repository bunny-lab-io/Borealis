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
	writeTimeline(cfg, "running", dependencyTaskName("Python"), "Installing Python runtime.", 1)
	if err := ensurePythonRuntime(cfg, logger); err != nil {
		return err
	}
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
		task := dependencyTaskName(step.name)
		writeTimeline(cfg, "running", task, "Installing "+step.name+".", 1)
		if err := step.fn(cfg, logger); err != nil {
			logger.Warnf("%s dependency deferred: %v", step.name, err)
			writeTimeline(cfg, "completed", task, step.name+" dependency deferred: "+err.Error(), 0)
		}
	}
	return nil
}

func dependencyTaskName(name string) string {
	return "Installing Agent Dependencies: " + name
}

func ensureAutoHotKey(cfg BootstrapConfig, logger *BootstrapLogger) error {
	installDir := filepath.Join(cfg.InstallDir, "Dependencies", "AutoHotKey")
	exePath := filepath.Join(installDir, "AutoHotkey64.exe")
	if fileExists(exePath) {
		return nil
	}
	zipPath := filepath.Join(cfg.InstallDir, "Dependencies", "AutoHotkey_2.0.19.zip")
	if err := downloadFile(context.Background(), "https://github.com/AutoHotkey/AutoHotkey/releases/download/v2.0.19/AutoHotkey_2.0.19.zip", zipPath, 180*time.Second); err != nil {
		return err
	}
	_ = os.RemoveAll(installDir)
	if err := unzipFile(zipPath, installDir); err != nil {
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
		return nil
	}
	zipPath := filepath.Join(cfg.InstallDir, "Dependencies", "MinGit-2.47.1-64-bit.zip")
	if err := downloadFile(context.Background(), "https://github.com/git-for-windows/git/releases/download/v2.47.1.windows.1/MinGit-2.47.1-64-bit.zip", zipPath, 240*time.Second); err != nil {
		return err
	}
	installDir := filepath.Join(cfg.InstallDir, "Dependencies", "git")
	_ = os.RemoveAll(installDir)
	if err := unzipFile(zipPath, installDir); err != nil {
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
		return nil
	}
	zipPath := filepath.Join(root, "UltraVNC_1640.zip")
	if err := downloadFile(context.Background(), "https://uvnc.eu/download/1640/UltraVNC_1640.zip", zipPath, 240*time.Second); err != nil {
		return err
	}
	payloadRoot := filepath.Join(root, "payload")
	_ = os.RemoveAll(payloadRoot)
	if err := unzipFile(zipPath, payloadRoot); err != nil {
		return err
	}
	logger.Infof("UltraVNC payload staged.")
	return nil
}

func ensureWireGuardInstaller(cfg BootstrapConfig, logger *BootstrapLogger) error {
	root := filepath.Join(cfg.InstallDir, "Dependencies", "VPN_Tunnel_Adapter")
	installer := filepath.Join(root, "wireguard-installer.exe")
	if !fileExists(installer) {
		if err := downloadFile(context.Background(), "https://download.wireguard.com/windows-client/wireguard-installer.exe", installer, 240*time.Second); err != nil {
			return err
		}
	}
	_, err := runCommandTimeout(logger, 180*time.Second, installer, "/installmanagerservice")
	if err != nil {
		return err
	}
	logger.Infof("WireGuard manager service installed.")
	return nil
}
