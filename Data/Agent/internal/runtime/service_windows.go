//go:build windows

package agentruntime

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	WindowsServiceName        = "BorealisAgent"
	WindowsServiceDisplayName = "Borealis Agent"
	WindowsUpdaterTaskName    = "Borealis Agent (AutoUpdater)"
	WindowsWatchdogTaskName   = "Borealis Agent (Watchdog)"
	windowsInstallPath        = `C:\Borealis\Agent.exe`
)

func PrepareServiceExecutable(exePath string) (string, error) {
	if samePath(exePath, windowsInstallPath) {
		return windowsInstallPath, nil
	}
	if err := os.MkdirAll(filepath.Dir(windowsInstallPath), 0o755); err != nil {
		return "", err
	}
	if err := copyFile(exePath, windowsInstallPath); err != nil {
		return "", err
	}
	return windowsInstallPath, nil
}

func InstallService(exePath string) error {
	configPath := ConfigPathForExecutable(exePath)
	command := fmt.Sprintf(`"%s" --service --config-path "%s"`, exePath, configPath)
	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect service manager: %w", err)
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(WindowsServiceName)
	if err != nil {
		service, err = manager.CreateService(WindowsServiceName, exePath, mgr.Config{
			StartType:        mgr.StartAutomatic,
			ErrorControl:     mgr.ErrorNormal,
			DisplayName:      WindowsServiceDisplayName,
			Description:      "Borealis Agent runtime",
			DelayedAutoStart: true,
		}, "--service", "--config-path", configPath)
		if err != nil {
			return fmt.Errorf("create service: %w", err)
		}
	} else {
		current, configErr := service.Config()
		if configErr != nil {
			_ = service.Close()
			return fmt.Errorf("query service config: %w", configErr)
		}
		current.StartType = mgr.StartAutomatic
		current.ErrorControl = mgr.ErrorNormal
		current.BinaryPathName = command
		current.DisplayName = WindowsServiceDisplayName
		current.Description = "Borealis Agent runtime"
		current.DelayedAutoStart = true
		if err := service.UpdateConfig(current); err != nil {
			_ = service.Close()
			return fmt.Errorf("update service: %w", err)
		}
	}
	defer service.Close()
	if err := configureServiceRecovery(service); err != nil {
		return err
	}
	if status, err := service.Query(); err == nil && status.State == svc.Running {
		return nil
	}
	if err := service.Start(); err != nil && !strings.Contains(strings.ToLower(err.Error()), "already running") {
		return fmt.Errorf("start service: %w", err)
	}
	return nil
}

func UninstallService() error {
	manager, err := mgr.Connect()
	if err == nil {
		if service, openErr := manager.OpenService(WindowsServiceName); openErr == nil {
			_, _ = service.Control(svc.Stop)
			_ = service.Delete()
			_ = service.Close()
		}
		_ = manager.Disconnect()
	}
	deleteTask(WindowsUpdaterTaskName)
	deleteTask(WindowsWatchdogTaskName)
	return nil
}

func configureServiceRecovery(service *mgr.Service) error {
	actions := []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 15 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
	}
	if err := service.SetRecoveryActions(actions, 86400); err != nil {
		return fmt.Errorf("set service recovery actions: %w", err)
	}
	if err := service.SetRecoveryActionsOnNonCrashFailures(true); err != nil {
		return fmt.Errorf("set service non-crash recovery: %w", err)
	}
	return nil
}

func serviceExecutablePathFromConfig(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "Agent.exe")
}

func deleteTask(name string) {
	_ = exec.Command("schtasks.exe", "/End", "/TN", name).Run()
	_ = exec.Command("schtasks.exe", "/Delete", "/TN", name, "/F").Run()
}

func copyFile(source string, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	return os.Chmod(destination, 0o755)
}

func samePath(left string, right string) bool {
	leftAbs, err := filepath.Abs(left)
	if err == nil {
		left = leftAbs
	}
	rightAbs, err := filepath.Abs(right)
	if err == nil {
		right = rightAbs
	}
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}
