//go:build windows

package agentruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
	"github.com/bunny-lab-io/borealis/go-agent/internal/logutil"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func RunWatchdogCheck(configPath string) error {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		var err error
		configPath, err = agentconfig.PathFromBinary()
		if err != nil {
			return err
		}
	}
	now := time.Now()
	_ = agentconfig.UpdateWithWriter(configPath, "watchdog:check", func(cfg *agentconfig.AgentConfig) {
		cfg.Agent.Liveness.LastWatchdogCheckAt = now.Unix()
	})
	cfg, _ := agentconfig.Load(configPath)

	manager, err := mgr.Connect()
	if err != nil {
		logWatchdog(configPath, "watchdog", "connect_scm", "failed", "service_manager", err)
		return err
	}
	defer manager.Disconnect()

	service, err := manager.OpenService(WindowsServiceName)
	if err != nil {
		decision := decideWatchdogRecovery(watchdogDecisionInput{ServiceExists: false, Now: now})
		logWatchdog(configPath, "watchdog", decision.Action, decision.Outcome, decision.Reason, err)
		if installErr := InstallService(serviceExecutablePathFromConfig(configPath)); installErr != nil {
			logWatchdog(configPath, "watchdog", "repair_service", "failed", "install_service", installErr)
			return installErr
		}
		recordWatchdogRecovery(configPath, decision.Action, decision.Reason)
		logWatchdog(configPath, "watchdog", decision.Action, "success", decision.Reason, nil)
		return nil
	}
	defer service.Close()

	status, err := service.Query()
	if err != nil {
		logWatchdog(configPath, "watchdog", "query_service", "failed", "query", err)
		return err
	}
	decision := decideWatchdogRecovery(watchdogDecisionInput{
		ServiceExists:          true,
		ServiceRunning:         status.State == svc.Running,
		ServicePID:             status.ProcessId,
		LivenessPID:            cfg.Agent.Liveness.PID,
		LastLocalTickAt:        cfg.Agent.Liveness.LastLocalTickAt,
		LastHeartbeatAttemptAt: cfg.Agent.Liveness.LastHeartbeatAttemptAt,
		LastHeartbeatSuccessAt: cfg.Agent.Liveness.LastHeartbeatSuccessAt,
		LastHeartbeatError:     cfg.Agent.Liveness.LastHeartbeatError,
		LastSocketState:        cfg.Agent.Liveness.LastSocketState,
		LastSocketStateAt:      cfg.Agent.Liveness.LastSocketStateAt,
		Now:                    now,
		UpdateActive:           updateActive(configPath),
	})
	if decision.Action == "start_service" {
		if err := service.Start(); err != nil && !strings.Contains(strings.ToLower(err.Error()), "already running") {
			logWatchdog(configPath, "watchdog", "start_service", "failed", fmt.Sprintf("state=%v", status.State), err)
			return err
		}
		recordWatchdogRecovery(configPath, decision.Action, fmt.Sprintf("state=%v", status.State))
		logWatchdog(configPath, "watchdog", decision.Action, "success", fmt.Sprintf("state=%v", status.State), nil)
		return nil
	}
	if decision.Action == "check_liveness" {
		if decision.Outcome != "healthy" {
			logWatchdog(configPath, "watchdog", decision.Action, decision.Outcome, decision.Reason, nil)
		}
		return nil
	}
	if decision.Action == "restart_service" && decision.Outcome == "skipped" {
		logWatchdog(configPath, "watchdog", decision.Action, decision.Outcome, decision.Reason, nil)
		return nil
	}
	if err := restartStaleService(service, status.ProcessId); err != nil {
		logWatchdog(configPath, "watchdog", decision.Action, "failed", decision.Reason, err)
		return err
	}
	recordWatchdogRecovery(configPath, decision.Action, decision.Reason)
	logWatchdog(configPath, "watchdog", decision.Action, "success", decision.Reason, nil)
	return nil
}

func restartStaleService(service *mgr.Service, pid uint32) error {
	_, _ = service.Control(svc.Stop)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		status, err := service.Query()
		if err == nil && status.State == svc.Stopped {
			return service.Start()
		}
		time.Sleep(time.Second)
	}
	if pid > 0 {
		if proc, err := os.FindProcess(int(pid)); err == nil {
			_ = proc.Kill()
		}
	}
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status, err := service.Query()
		if err == nil && status.State == svc.Stopped {
			break
		}
		time.Sleep(time.Second)
	}
	return service.Start()
}

func updateActive(configPath string) bool {
	root := filepath.Dir(configPath)
	for _, candidate := range []string{
		filepath.Join(root, "Agent.exe.update"),
		filepath.Join(root, "Agent.exe.update.tmp"),
		filepath.Join(root, "Agent.exe.tmp"),
		filepath.Join(root, "Temp", "Updater"),
	} {
		if _, err := os.Stat(candidate); err == nil {
			return true
		}
	}
	return false
}

func recordWatchdogRecovery(configPath string, action string, reason string) {
	_ = agentconfig.UpdateWithWriter(configPath, "watchdog:recovery", func(cfg *agentconfig.AgentConfig) {
		cfg.Agent.Liveness.LastRecoveryAction = strings.TrimSpace(action + ":" + reason)
		cfg.Agent.Liveness.LastRecoveryAt = time.Now().Unix()
	})
}

func logWatchdog(configPath string, component string, action string, outcome string, reason string, err error) {
	errorText := ""
	if err != nil {
		errorText = err.Error()
	}
	logutil.Append(
		filepath.Join(filepath.Dir(configPath), "Logs", "Agent", "role_recovery.log"),
		logutil.RetentionDaysFromConfig(configPath),
		"[%s] [role-recovery] component=%s role_id=watchdog action=%s outcome=%s reason=%s error=%s",
		time.Now().Format("2006-01-02T15:04:05"),
		component,
		action,
		outcome,
		strings.ReplaceAll(strings.TrimSpace(reason), "\n", " "),
		strings.ReplaceAll(strings.TrimSpace(errorText), "\n", " "),
	)
}
