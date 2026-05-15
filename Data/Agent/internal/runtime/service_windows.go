//go:build windows

package agentruntime

import (
	"fmt"
	"os/exec"
)

func PrepareServiceExecutable(exePath string) (string, error) {
	return exePath, nil
}

func InstallService(exePath string) error {
	command := fmt.Sprintf(`"%s" --system-service`, exePath)
	deleteTask("Borealis Agent")
	args := []string{"/Create", "/TN", "Borealis Agent", "/TR", command, "/SC", "ONSTART", "/RU", "SYSTEM", "/RL", "HIGHEST", "/F"}
	if output, err := exec.Command("schtasks.exe", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("create task failed: %w: %s", err, string(output))
	}
	return exec.Command("schtasks.exe", "/Run", "/TN", "Borealis Agent").Run()
}

func UninstallService() error {
	deleteTask("Borealis Agent")
	deleteTask("Borealis Agent (AutoUpdater)")
	return nil
}

func deleteTask(name string) {
	_ = exec.Command("schtasks.exe", "/End", "/TN", name).Run()
	_ = exec.Command("schtasks.exe", "/Delete", "/TN", name, "/F").Run()
}
