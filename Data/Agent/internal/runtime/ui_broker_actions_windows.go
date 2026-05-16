//go:build windows

package agentruntime

import (
	"fmt"
	"os/exec"
	"syscall"
)

func restartLocalAgent() error {
	command := `timeout /t 1 /nobreak >nul & schtasks.exe /End /TN "Borealis Agent" >nul 2>nul & timeout /t 2 /nobreak >nul & schtasks.exe /Run /TN "Borealis Agent" >nul 2>nul`
	cmd := exec.Command("cmd.exe", "/C", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x00000008}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start restart broker command: %w", err)
	}
	return nil
}
