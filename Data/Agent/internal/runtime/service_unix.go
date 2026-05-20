//go:build !windows

package agentruntime

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	linuxInstallPath         = "/opt/Borealis/Agent/Agent"
	linuxServiceName         = "borealis-agent.service"
	linuxUpdaterServiceName  = "borealis-agent-updater.service"
	linuxUpdaterTimerName    = "borealis-agent-updater.timer"
	linuxWatchdogServiceName = "borealis-agent-watchdog.service"
	linuxWatchdogTimerName   = "borealis-agent-watchdog.timer"
)

func ResetInstallForFreshDeploy(exePath string) error {
	root := filepath.Clean("/opt/Borealis")
	if isPathInside(filepath.Clean(exePath), root) {
		return nil
	}
	_ = exec.Command("systemctl", "stop", linuxServiceName).Run()
	_ = exec.Command("systemctl", "stop", linuxUpdaterTimerName).Run()
	_ = exec.Command("systemctl", "stop", linuxUpdaterServiceName).Run()
	_ = exec.Command("systemctl", "stop", linuxWatchdogTimerName).Run()
	_ = exec.Command("systemctl", "stop", linuxWatchdogServiceName).Run()
	_ = exec.Command("systemctl", "disable", linuxServiceName).Run()
	_ = exec.Command("systemctl", "disable", linuxUpdaterTimerName).Run()
	_ = exec.Command("systemctl", "disable", linuxWatchdogTimerName).Run()
	_ = os.Remove("/etc/systemd/system/" + linuxServiceName)
	_ = os.Remove("/etc/systemd/system/" + linuxUpdaterServiceName)
	_ = os.Remove("/etc/systemd/system/" + linuxUpdaterTimerName)
	_ = os.Remove("/etc/systemd/system/" + linuxWatchdogServiceName)
	_ = os.Remove("/etc/systemd/system/" + linuxWatchdogTimerName)
	_ = exec.Command("systemctl", "daemon-reload").Run()
	return os.RemoveAll(root)
}

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
	path := "/etc/systemd/system/" + linuxServiceName
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
	if err := os.WriteFile("/etc/systemd/system/"+linuxUpdaterServiceName, []byte(updaterUnit), 0o644); err != nil {
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
	if err := os.WriteFile("/etc/systemd/system/"+linuxUpdaterTimerName, []byte(updaterTimer), 0o644); err != nil {
		return err
	}
	watchdogUnit := fmt.Sprintf(`[Unit]
Description=Borealis Agent Watchdog
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
WorkingDirectory=%s
ExecStart=%s --watchdog-check --config-path %s
`, filepath.Dir(exePath), shellQuote(exePath), shellQuote(configPath))
	if err := os.WriteFile("/etc/systemd/system/"+linuxWatchdogServiceName, []byte(watchdogUnit), 0o644); err != nil {
		return err
	}
	watchdogTimer := `[Unit]
Description=Borealis Agent Watchdog Timer

[Timer]
OnBootSec=1min
OnUnitActiveSec=1min
AccuracySec=5s
Persistent=true
Unit=borealis-agent-watchdog.service

[Install]
WantedBy=timers.target
`
	if err := os.WriteFile("/etc/systemd/system/"+linuxWatchdogTimerName, []byte(watchdogTimer), 0o644); err != nil {
		return err
	}
	_ = exec.Command("systemctl", "daemon-reload").Run()
	_ = exec.Command("systemctl", "enable", linuxServiceName).Run()
	_ = exec.Command("systemctl", "enable", "--now", linuxUpdaterTimerName).Run()
	_ = exec.Command("systemctl", "enable", "--now", linuxWatchdogTimerName).Run()
	return exec.Command("systemctl", "restart", linuxServiceName).Run()
}

func UninstallService() error {
	_ = exec.Command("systemctl", "stop", linuxServiceName).Run()
	_ = exec.Command("systemctl", "stop", linuxUpdaterTimerName).Run()
	_ = exec.Command("systemctl", "stop", linuxUpdaterServiceName).Run()
	_ = exec.Command("systemctl", "stop", linuxWatchdogTimerName).Run()
	_ = exec.Command("systemctl", "stop", linuxWatchdogServiceName).Run()
	_ = exec.Command("systemctl", "disable", linuxServiceName).Run()
	_ = exec.Command("systemctl", "disable", linuxUpdaterTimerName).Run()
	_ = exec.Command("systemctl", "disable", linuxWatchdogTimerName).Run()
	_ = os.Remove("/etc/systemd/system/" + linuxServiceName)
	_ = os.Remove("/etc/systemd/system/" + linuxUpdaterServiceName)
	_ = os.Remove("/etc/systemd/system/" + linuxUpdaterTimerName)
	_ = os.Remove("/etc/systemd/system/" + linuxWatchdogServiceName)
	_ = os.Remove("/etc/systemd/system/" + linuxWatchdogTimerName)
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

func isPathInside(path string, root string) bool {
	pathAbs, pathErr := filepath.Abs(path)
	rootAbs, rootErr := filepath.Abs(root)
	if pathErr == nil {
		path = pathAbs
	}
	if rootErr == nil {
		root = rootAbs
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
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
