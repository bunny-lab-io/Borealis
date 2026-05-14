//go:build !windows

package agentruntime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func InstallService(exePath string) error {
	unit := fmt.Sprintf(`[Unit]
Description=Borealis Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=%s
ExecStart=%s --system-service
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`, filepath.Dir(exePath), exePath)
	path := "/etc/systemd/system/borealis-agent.service"
	if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
		return err
	}
	_ = exec.Command("systemctl", "daemon-reload").Run()
	_ = exec.Command("systemctl", "enable", "borealis-agent.service").Run()
	return exec.Command("systemctl", "restart", "borealis-agent.service").Run()
}

func UninstallService() error {
	_ = exec.Command("systemctl", "stop", "borealis-agent.service").Run()
	_ = exec.Command("systemctl", "disable", "borealis-agent.service").Run()
	_ = os.Remove("/etc/systemd/system/borealis-agent.service")
	return exec.Command("systemctl", "daemon-reload").Run()
}
