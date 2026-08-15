package agentruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
	"github.com/bunny-lab-io/borealis/go-agent/internal/logutil"
)

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
	if !strings.HasPrefix(status, "factory_reset_") {
		if err := requestWatchdogUpdateRetry(configPath, "factory_reset_started", now); err != nil {
			return true, err
		}
		return true, startLocalUpdater(configPath)
	}
	return false, nil
}

func requestWatchdogUpdateRetry(configPath string, status string, now time.Time) error {
	return agentconfig.UpdateWithWriter(configPath, "watchdog:update_recovery", func(cfg *agentconfig.AgentConfig) {
		cfg.Agent.Update.Status = strings.ToLower(strings.TrimSpace(status))
		cfg.Agent.Update.UpdatedAt = now.Unix()
		cfg.Agent.Update.DeadlineAt = now.Add(15 * time.Minute).Unix()
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
		filepath.Join(root, "Agent.update"),
		filepath.Join(root, "Agent.update.tmp"),
		filepath.Join(root, "Temp", "Updater"),
		filepath.Join(root, "Updater"),
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
