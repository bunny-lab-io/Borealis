//go:build windows

package agentruntime

import (
	"fmt"
	"os/exec"
	"syscall"
)

func restartLocalAgent() error {
	command := `timeout /t 1 /nobreak >nul & sc.exe stop BorealisAgent >nul 2>nul & timeout /t 2 /nobreak >nul & sc.exe start BorealisAgent >nul 2>nul`
	cmd := exec.Command("cmd.exe", "/C", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x00000008}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start restart broker command: %w", err)
	}
	return nil
}
