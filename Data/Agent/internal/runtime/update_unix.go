//go:build !windows

package agentruntime

import (
	"os"
	"os/exec"
	"path/filepath"
)

func startLocalUpdater(configPath string) error {
	if _, err := exec.LookPath("systemctl"); err == nil {
		if runErr := exec.Command("systemctl", "start", linuxUpdaterServiceName).Run(); runErr == nil {
			return nil
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"--update-check"}
	if configPath != "" {
		args = append(args, "--config-path", filepath.Clean(configPath))
	}
	cmd := exec.Command(exe, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
