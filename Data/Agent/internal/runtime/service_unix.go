//go:build !windows

package agentruntime

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

const linuxInstallPath = "/opt/Borealis/Agent/Agent"

func PrepareServiceExecutable(exePath string) (string, error) {
	destination := linuxInstallPath
	if samePath(exePath, destination) {
		_ = os.Chmod(destination, 0o700)
		return destination, nil
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return "", err
	}
	if err := copyFile(exePath, destination, 0o700); err != nil {
		return "", err
	}
	return destination, nil
}

func InstallService(exePath string) error {
	configPath := ConfigPathForExecutable(exePath)
	unit := fmt.Sprintf(`[Unit]
Description=Borealis Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=%s
ExecStart=%s --service --config-path %s
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`, filepath.Dir(exePath), shellQuote(exePath), shellQuote(configPath))
	path := "/etc/systemd/system/borealis-agent.service"
	if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
		return err
	}
	updaterUnit := fmt.Sprintf(`[Unit]
Description=Borealis Agent AutoUpdater
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
WorkingDirectory=%s
ExecStart=%s --update-check --config-path %s
`, filepath.Dir(exePath), shellQuote(exePath), shellQuote(configPath))
	if err := os.WriteFile("/etc/systemd/system/borealis-agent-updater.service", []byte(updaterUnit), 0o644); err != nil {
		return err
	}
	updaterTimer := `[Unit]
Description=Borealis Agent AutoUpdater Timer

[Timer]
OnBootSec=5min
OnUnitActiveSec=1h
Persistent=true
Unit=borealis-agent-updater.service

[Install]
WantedBy=timers.target
`
	if err := os.WriteFile("/etc/systemd/system/borealis-agent-updater.timer", []byte(updaterTimer), 0o644); err != nil {
		return err
	}
	_ = exec.Command("systemctl", "daemon-reload").Run()
	_ = exec.Command("systemctl", "enable", "borealis-agent.service").Run()
	_ = exec.Command("systemctl", "enable", "--now", "borealis-agent-updater.timer").Run()
	return exec.Command("systemctl", "restart", "borealis-agent.service").Run()
}

func UninstallService() error {
	_ = exec.Command("systemctl", "stop", "borealis-agent.service").Run()
	_ = exec.Command("systemctl", "stop", "borealis-agent-updater.timer").Run()
	_ = exec.Command("systemctl", "stop", "borealis-agent-updater.service").Run()
	_ = exec.Command("systemctl", "disable", "borealis-agent.service").Run()
	_ = exec.Command("systemctl", "disable", "borealis-agent-updater.timer").Run()
	_ = os.Remove("/etc/systemd/system/borealis-agent.service")
	_ = os.Remove("/etc/systemd/system/borealis-agent-updater.service")
	_ = os.Remove("/etc/systemd/system/borealis-agent-updater.timer")
	return exec.Command("systemctl", "daemon-reload").Run()
}

func copyFile(source string, destination string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	temp := destination + ".tmp"
	out, err := os.OpenFile(temp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(temp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(temp)
		return closeErr
	}
	if err := os.Chmod(temp, mode); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return os.Rename(temp, destination)
}

func samePath(left string, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr == nil {
		left = leftAbs
	}
	if rightErr == nil {
		right = rightAbs
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func shellQuote(value string) string {
	escaped := "'"
	for _, r := range value {
		if r == '\'' {
			escaped += "'\\''"
			continue
		}
		escaped += string(r)
	}
	return escaped + "'"
}
