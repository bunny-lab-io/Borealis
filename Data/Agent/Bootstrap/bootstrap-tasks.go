//go:build windows

package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows/svc"
)

type ScheduledTaskInfo struct {
	Exists bool
	State  string
	Raw    string
	Error  string
}

func runBootstrapWindowsService(cli cliOptions) int {
	cfg, err := loadBootstrapConfig(cli, true)
	if err != nil {
		return 2
	}
	logger, closeLog, err := openBootstrapLogger(cfg, false)
	if err != nil {
		return 1
	}
	defer closeLog()
	handler := &bootstrapService{cfg: cfg, logger: logger}
	if err := svc.Run(cfg.ServiceName, handler); err != nil {
		logger.Errorf("Windows service runner failed: %v", err)
		return 1
	}
	return handler.exitCode
}

type bootstrapService struct {
	cfg      BootstrapConfig
	logger   *BootstrapLogger
	exitCode int
}

func (s *bootstrapService) Execute(args []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}
	done := make(chan int, 1)
	go func() {
		done <- runBootstrap(s.cfg, s.logger)
	}()
	changes <- svc.Status{State: svc.Running, Accepts: accepted}
	for {
		select {
		case code := <-done:
			s.exitCode = code
			changes <- svc.Status{State: svc.StopPending}
			return false, uint32(code)
		case req := <-requests:
			switch req.Cmd {
			case svc.Interrogate:
				changes <- req.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				return false, 1
			}
		}
	}
}

func queryScheduledTask(taskName string) ScheduledTaskInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "schtasks.exe", "/Query", "/TN", taskName, "/FO", "LIST", "/V")
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		return ScheduledTaskInfo{Exists: false, Raw: text, Error: strings.TrimSpace(err.Error() + " " + text)}
	}
	return ScheduledTaskInfo{Exists: true, State: parseScheduledTaskState(text), Raw: text}
}

func parseScheduledTaskState(output string) string {
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(strings.ToLower(parts[0]))
		if key == "status" || key == "scheduled task state" {
			return strings.TrimSpace(parts[1])
		}
	}
	return ""
}

func taskScheduledStateIsRunning(state string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(state)), "running")
}

func startScheduledTask(taskName string, logger *BootstrapLogger) error {
	_, err := runCommandTimeout(logger, 20*time.Second, "schtasks.exe", "/Run", "/TN", taskName)
	return err
}

func stopScheduledTask(taskName string, logger *BootstrapLogger) {
	_, _ = runCommandTimeout(logger, 20*time.Second, "schtasks.exe", "/End", "/TN", taskName)
}

func deleteScheduledTask(taskName string, logger *BootstrapLogger) {
	_, _ = runCommandTimeout(logger, 20*time.Second, "schtasks.exe", "/Delete", "/TN", taskName, "/F")
}

func ensureAgentTasks(cfg BootstrapConfig, logger *BootstrapLogger) error {
	agentExe := filepath.Join(cfg.InstallDir, "Agent.exe")
	launchScript := filepath.Join(cfg.InstallDir, "Agent", "Borealis", "launch_service.ps1")
	if !fileExists(launchScript) {
		return fmt.Errorf("launch_service.ps1 not found at %s", launchScript)
	}
	taskAction := fmt.Sprintf(`%s -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "%s"`, powershellPath(), launchScript)
	if err := createOrReplaceTask(agentTaskName, taskAction, "ONSTART", logger); err != nil {
		return err
	}
	updateAction := fmt.Sprintf(`"%s"`, agentExe)
	if err := createOrReplaceTask(agentUpdaterTaskName, updateAction, "HOURLY", logger); err != nil {
		return err
	}
	return startScheduledTask(agentTaskName, logger)
}

func createOrReplaceTask(name string, command string, schedule string, logger *BootstrapLogger) error {
	deleteScheduledTask(name, logger)
	args := []string{
		"/Create",
		"/TN", name,
		"/TR", command,
		"/SC", schedule,
		"/RU", "SYSTEM",
		"/RL", "HIGHEST",
		"/F",
	}
	if schedule == "HOURLY" {
		args = append(args, "/MO", "1")
	}
	_, err := runCommandTimeout(logger, 30*time.Second, "schtasks.exe", args...)
	return err
}

func waitForTaskRunning(taskName string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		info := queryScheduledTask(taskName)
		if taskScheduledStateIsRunning(info.State) {
			return true
		}
		time.Sleep(2 * time.Second)
	}
	return false
}

func startAgentRuntime(cfg BootstrapConfig, logger *BootstrapLogger) error {
	if err := startScheduledTask(agentTaskName, logger); err != nil {
		return err
	}
	if !waitForTaskRunning(agentTaskName, 30*time.Second) {
		logger.Warnf("Borealis Agent task did not report Running before timeout.")
	}
	return nil
}

func createOneShotServiceCommand(cfg BootstrapConfig) string {
	return strconv.Quote(filepath.Join(cfg.InstallDir, "Agent.exe"))
}
