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
	updateState := watchdogUpdateState(configPath, cfg, now)
	if updateState.Active && !updateState.Expired {
		logWatchdog(configPath, "watchdog", "check_liveness", "skipped", "update_active", nil)
		return nil
	}
	if updateState.Active && updateState.Expired {
		if handled, recoverErr := recoverExpiredUpdateOperation(configPath, cfg, now); handled {
			if recoverErr != nil {
				logWatchdog(configPath, "watchdog", "update_recovery", "failed", updateState.Reason, recoverErr)
				return recoverErr
			}
			logWatchdog(configPath, "watchdog", "update_recovery", "started", updateState.Reason, nil)
			return nil
		}
	}

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
		UpdateActive:           updateState.Active,
		UpdateExpired:          updateState.Expired,
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

type watchdogUpdateActivity struct {
	Active  bool
	Expired bool
	Reason  string
}

func watchdogUpdateState(configPath string, cfg agentconfig.AgentConfig, now time.Time) watchdogUpdateActivity {
	if updateOperationActive(cfg.Agent.Update) {
		deadline := cfg.Agent.Update.DeadlineAt
		if deadline <= 0 {
			base := cfg.Agent.Update.UpdatedAt
			if base <= 0 {
				base = cfg.Agent.Update.StartedAt
			}
			deadline = base + int64(15*time.Minute/time.Second)
		}
		expired := deadline > 0 && now.Unix() > deadline
		return watchdogUpdateActivity{
			Active:  true,
			Expired: expired,
			Reason:  fmt.Sprintf("operation_status=%s", cfg.Agent.Update.Status),
		}
	}
	if updateFilesActive(configPath, now) {
		return watchdogUpdateActivity{Active: true, Expired: false, Reason: "update_files_active"}
	}
	return watchdogUpdateActivity{}
}

func updateOperationActive(update agentconfig.AgentUpdateSection) bool {
	switch strings.ToLower(strings.TrimSpace(update.Status)) {
	case "requested", "config_written", "updater_started", "running", "staging", "restarting", "verifying", "rollback_requested", "rollback_started", "factory_reset_requested", "factory_reset_started":
		return strings.TrimSpace(update.OperationID) != ""
	default:
		return false
	}
}

func recoverExpiredUpdateOperation(configPath string, cfg agentconfig.AgentConfig, now time.Time) (bool, error) {
	update := cfg.Agent.Update
	status := strings.ToLower(strings.TrimSpace(update.Status))
	previousChannel := agentconfig.NormalizeReleaseChannel(update.PreviousChannel)
	previousBranch := agentconfig.NormalizeBranch(update.PreviousBranch)
	if previousChannel == "" {
		previousChannel = agentconfig.ReleaseChannelStable
	}
	if previousChannel == agentconfig.ReleaseChannelStable {
		previousBranch = agentconfig.DefaultBranch
	}
	targetChannel := agentconfig.NormalizeReleaseChannel(update.TargetChannel)
	targetBranch := agentconfig.NormalizeBranch(update.TargetBranch)
	if targetChannel == agentconfig.ReleaseChannelStable {
		targetBranch = agentconfig.DefaultBranch
	}

	if !strings.HasPrefix(status, "rollback_") && !strings.HasPrefix(status, "factory_reset_") && (previousChannel != targetChannel || !strings.EqualFold(previousBranch, targetBranch)) {
		if err := requestWatchdogUpdateTarget(configPath, "rollback_started", previousChannel, previousBranch, now); err != nil {
			return true, err
		}
		return true, startLocalUpdater(configPath)
	}
	if strings.HasPrefix(status, "rollback_") || (previousChannel == targetChannel && strings.EqualFold(previousBranch, targetBranch)) {
		if err := requestWatchdogUpdateTarget(configPath, "factory_reset_started", agentconfig.ReleaseChannelStable, agentconfig.DefaultBranch, now); err != nil {
			return true, err
		}
		return true, startLocalUpdater(configPath)
	}
	return false, nil
}

func requestWatchdogUpdateTarget(configPath string, status string, channel string, branch string, now time.Time) error {
	return agentconfig.UpdateWithWriter(configPath, "watchdog:update_recovery", func(cfg *agentconfig.AgentConfig) {
		targetChannel := agentconfig.NormalizeReleaseChannel(channel)
		targetBranch := agentconfig.NormalizeBranch(branch)
		if targetChannel == agentconfig.ReleaseChannelStable {
			targetBranch = agentconfig.DefaultBranch
		}
		cfg.Agent.ReleaseChannel = targetChannel
		cfg.Agent.Branch = targetBranch
		cfg.Agent.Update.Status = strings.ToLower(strings.TrimSpace(status))
		cfg.Agent.Update.UpdatedAt = now.Unix()
		cfg.Agent.Update.DeadlineAt = now.Add(15 * time.Minute).Unix()
		cfg.Agent.Update.TargetChannel = targetChannel
		cfg.Agent.Update.TargetBranch = targetBranch
		cfg.Agent.Update.LastError = ""
		cfg.Agent.Update.RecoveryAttempts++
	})
}

func updateFilesActive(configPath string, now time.Time) bool {
	root := filepath.Dir(configPath)
	for _, candidate := range []string{
		filepath.Join(root, "Agent.exe.update"),
		filepath.Join(root, "Agent.exe.update.tmp"),
		filepath.Join(root, "Agent.exe.tmp"),
		filepath.Join(root, "Temp", "Updater"),
	} {
		if info, err := os.Stat(candidate); err == nil {
			if now.Sub(info.ModTime()) > 15*time.Minute {
				continue
			}
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
