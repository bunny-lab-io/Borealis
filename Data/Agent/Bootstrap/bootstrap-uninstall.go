//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func uninstallBorealis(cfg BootstrapConfig, logger *BootstrapLogger) error {
	logger.Infof("Uninstall requested.")
	stopScheduledTask(agentTaskName, logger)
	stopScheduledTask(agentUpdaterTaskName, logger)
	deleteScheduledTask(agentTaskName, logger)
	deleteScheduledTask(agentUpdaterTaskName, logger)
	stopBorealisProcesses(cfg, logger)
	removeBorealisOwnedServices(logger)
	finalLogDir := filepath.Join(os.Getenv("ProgramData"), "Borealis", "Logs")
	if finalLogDir == "" || finalLogDir == "Borealis\\Logs" {
		finalLogDir = filepath.Join(os.Getenv("SystemDrive")+`\`, "ProgramData", "Borealis", "Logs")
	}
	_ = os.MkdirAll(finalLogDir, 0755)
	finalLog := filepath.Join(finalLogDir, "bootstrap-uninstall.log")
	_ = os.WriteFile(finalLog, []byte(time.Now().Format(time.RFC3339)+" Agent.exe uninstall requested.\r\n"), 0644)

	exe, _ := os.Executable()
	if strings.HasPrefix(strings.ToLower(filepath.Clean(exe)), strings.ToLower(filepath.Clean(cfg.InstallDir))) {
		cmd := exec.Command("cmd.exe", "/C", fmt.Sprintf("ping 127.0.0.1 -n 3 > nul & rmdir /s /q %s", strconvQuoteForCmd(cfg.InstallDir)))
		_ = cmd.Start()
		return nil
	}
	return os.RemoveAll(cfg.InstallDir)
}

func removeBorealisOwnedServices(logger *BootstrapLogger) {
	for _, service := range []string{
		"BorealisOnboarding",
		"BorealisAgentBootstrap",
		"BorealisWireGuardTunnel",
		"WireGuardTunnel$Borealis",
		"WireGuardTunnel$borealis-wg",
	} {
		_, _ = runCommandTimeout(logger, 20*time.Second, "sc.exe", "stop", service)
		_, _ = runCommandTimeout(logger, 20*time.Second, "sc.exe", "delete", service)
	}
}

func strconvQuoteForCmd(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}
