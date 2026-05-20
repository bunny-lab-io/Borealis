//go:build windows

package agentruntime

import (
	"fmt"
	"os/exec"
)

func startLocalUpdater(configPath string) error {
	output, err := exec.Command("schtasks.exe", "/Run", "/TN", WindowsUpdaterTaskName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("start AutoUpdater task: %w: %s", err, string(output))
	}
	return nil
}
