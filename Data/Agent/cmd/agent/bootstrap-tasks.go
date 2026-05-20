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

	agentruntime "github.com/bunny-lab-io/borealis/go-agent/internal/runtime"
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
	if shouldResetForFreshBootstrap(cfg) {
		if err := validateFreshBootstrap(cfg); err != nil {
			return 1
		}
		if err := resetInstallRootForFreshBootstrap(cfg); err != nil {
			return 1
		}
	}
	logger, closeLog, err := openBootstrapLogger(cfg, false)
	if err != nil {
		return 1
	}
	defer closeLog()
	logger.Tracef("Windows service host starting: service_name=%s", cfg.ServiceName)
	handler := &bootstrapService{cfg: cfg, logger: logger}
	if err := svc.Run(cfg.ServiceName, handler); err != nil {
		logger.Errorf("Windows service runner failed: %v", err)
		return 1
	}
	logger.Tracef("Windows service host stopped: exit_code=%d", handler.exitCode)
	return handler.exitCode
}

type bootstrapService struct {
	cfg      BootstrapConfig
	logger   *BootstrapLogger
	exitCode int
}

func (s *bootstrapService) Execute(args []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown
	s.logger.Tracef("Windows service Execute entered: args=%v", args)
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
			s.logger.Tracef("Windows service bootstrap goroutine complete: exit_code=%d", code)
			changes <- svc.Status{State: svc.StopPending}
			return false, uint32(code)
		case req := <-requests:
			s.logger.Tracef("Windows service control request: cmd=%v", req.Cmd)
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
	logger.Tracef("Scheduled task start requested: %s", taskName)
	_, err := runCommandTimeout(logger, 20*time.Second, "schtasks.exe", "/Run", "/TN", taskName)
	if err != nil {
		logger.Tracef("Scheduled task start failed: %s error=%v", taskName, err)
		return err
	}
	info := queryScheduledTask(taskName)
	logger.Tracef("Scheduled task start command completed: %s exists=%t state=%s error=%s", taskName, info.Exists, info.State, info.Error)
	return err
}

func stopScheduledTask(taskName string, logger *BootstrapLogger) {
	logger.Tracef("Scheduled task stop requested: %s", taskName)
	_, _ = runCommandTimeout(logger, 20*time.Second, "schtasks.exe", "/End", "/TN", taskName)
}

func deleteScheduledTask(taskName string, logger *BootstrapLogger) {
	logger.Tracef("Scheduled task delete requested: %s", taskName)
	_, _ = runCommandTimeout(logger, 20*time.Second, "schtasks.exe", "/Delete", "/TN", taskName, "/F")
}

func ensureAgentTasks(cfg BootstrapConfig, logger *BootstrapLogger) error {
	startedAt := time.Now()
	agentExe := filepath.Join(cfg.InstallDir, "Agent.exe")
	logger.Tracef("Ensuring Agent service and scheduled support tasks: agent_exe=%s agent_exe_exists=%t", agentExe, fileExists(agentExe))
	if !fileExists(agentExe) {
		return fmt.Errorf("Agent.exe not found at %s", agentExe)
	}
	deleteScheduledTask(legacyAgentTaskName, logger)
	if err := ensureAgentUpdaterTask(cfg, logger); err != nil {
		return err
	}
	if err := ensureAgentWatchdogTask(cfg, logger); err != nil {
		return err
	}
	if err := agentruntime.InstallService(agentExe); err != nil {
		return err
	}
	logger.Tracef("Agent service and support tasks ensured duration=%s.", time.Since(startedAt).Round(time.Millisecond))
	return nil
}

func ensureAgentUpdaterTask(cfg BootstrapConfig, logger *BootstrapLogger) error {
	agentExe := filepath.Join(cfg.InstallDir, "Agent.exe")
	logger.Tracef("Ensuring Agent AutoUpdater scheduled task: agent_exe=%s agent_exe_exists=%t", agentExe, fileExists(agentExe))
	if !fileExists(agentExe) {
		return fmt.Errorf("Agent.exe not found at %s", agentExe)
	}
	updateAction := fmt.Sprintf(`"%s" --update-check --config-path "%s"`, agentExe, filepath.Join(cfg.InstallDir, "agent.json"))
	return createOrReplaceTask(agentUpdaterTaskName, updateAction, "HOURLY", logger)
}

func ensureAgentWatchdogTask(cfg BootstrapConfig, logger *BootstrapLogger) error {
	agentExe := filepath.Join(cfg.InstallDir, "Agent.exe")
	logger.Tracef("Ensuring Agent Watchdog scheduled task: agent_exe=%s agent_exe_exists=%t", agentExe, fileExists(agentExe))
	if !fileExists(agentExe) {
		return fmt.Errorf("Agent.exe not found at %s", agentExe)
	}
	action := fmt.Sprintf(`"%s" --watchdog-check --config-path "%s"`, agentExe, filepath.Join(cfg.InstallDir, "agent.json"))
	return createOrReplaceTask(agentWatchdogTaskName, action, "MINUTE", logger)
}

func createOrReplaceTask(name string, command string, schedule string, logger *BootstrapLogger) error {
	logger.Tracef("Creating/replacing scheduled task: name=%s schedule=%s command=%s", name, schedule, command)
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
	if schedule == "HOURLY" || schedule == "MINUTE" {
		args = append(args, "/MO", "1")
	}
	_, err := runCommandTimeout(logger, 30*time.Second, "schtasks.exe", args...)
	info := queryScheduledTask(name)
	logger.Tracef("Scheduled task create result: name=%s err=%v exists=%t state=%s query_error=%s", name, err, info.Exists, info.State, info.Error)
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
	logger.Tracef("Starting Agent runtime through Windows service.")
	if err := agentruntime.InstallService(filepath.Join(cfg.InstallDir, "Agent.exe")); err != nil {
		return err
	}
	if !waitForServiceRunning(agentruntime.WindowsServiceName, 30*time.Second) {
		logger.Warnf("Borealis Agent service did not report RUNNING before timeout.")
	} else {
		logger.Tracef("Borealis Agent service reported RUNNING.")
	}
	return nil
}

func waitForServiceRunning(serviceName string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state, exists := queryServiceState(serviceName)
		if exists && strings.EqualFold(state, "RUNNING") {
			return true
		}
		time.Sleep(2 * time.Second)
	}
	return false
}

func createOneShotServiceCommand(cfg BootstrapConfig) string {
	return strconv.Quote(filepath.Join(cfg.InstallDir, "Agent.exe"))
}
