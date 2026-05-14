//go:build windows

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

func stageAgentUpdateBinary(cfg BootstrapConfig, sourceRoot string, logger *BootstrapLogger) (bool, error) {
	candidates := []string{
		filepath.Join(sourceRoot, "Agent.exe"),
		filepath.Join(sourceRoot, "Data", "Agent", "Agent.exe"),
		filepath.Join(sourceRoot, "Data", "Agent", "dist", "windows-amd64", "Agent.exe"),
		filepath.Join(sourceRoot, "dist", "windows-amd64", "Agent.exe"),
	}
	for _, candidate := range candidates {
		if !fileExists(candidate) {
			continue
		}
		destination := filepath.Join(cfg.InstallDir, "Agent.exe")
		pending := filepath.Join(cfg.InstallDir, "Agent.exe.update")
		if logger != nil {
			logger.Tracef("Staging Go Agent update binary: source=%s destination=%s", candidate, destination)
		}
		if err := copyFile(candidate, pending); err != nil {
			return false, err
		}
		exe, _ := os.Executable()
		if samePath(exe, destination) {
			if err := scheduleAgentSelfReplacement(pending, destination, logger); err != nil {
				return false, err
			}
			return true, nil
		}
		if err := copyFile(pending, destination); err != nil {
			return false, err
		}
		_ = os.Remove(pending)
		return false, nil
	}
	if sourceRootHasGoAgent(sourceRoot) {
		return false, fmt.Errorf("Go Agent source archive does not include a built Windows Agent.exe; update artifact must include Data\\Agent\\dist\\windows-amd64\\Agent.exe")
	}
	return false, fmt.Errorf("update artifact missing Agent.exe")
}

func scheduleAgentSelfReplacement(pending string, destination string, logger *BootstrapLogger) error {
	if logger != nil {
		logger.Tracef("Scheduling Agent.exe self-replacement: pending=%s destination=%s", pending, destination)
	}
	command := fmt.Sprintf(
		`ping -n 4 127.0.0.1 >NUL & move /Y "%s" "%s" >NUL & schtasks.exe /Run /TN "%s" >NUL 2>&1`,
		pending,
		destination,
		agentTaskName,
	)
	cmd := exec.Command("cmd.exe", "/C", command)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func copyReaderToFile(reader io.Reader, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	out, err := os.Create(destination)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, reader)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
