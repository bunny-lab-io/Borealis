//go:build !windows

package agentruntime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
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

	state, err := queryLinuxAgentService()
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

	decision := decideWatchdogRecovery(watchdogDecisionInput{
		ServiceExists:          true,
		ServiceRunning:         state.Running,
		ServicePID:             state.PID,
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
	switch decision.Action {
	case "start_service":
		if err := systemctl("start", linuxServiceName); err != nil {
			logWatchdog(configPath, "watchdog", "start_service", "failed", state.ActiveState, err)
			return err
		}
		recordWatchdogRecovery(configPath, decision.Action, state.ActiveState)
		logWatchdog(configPath, "watchdog", decision.Action, "success", state.ActiveState, nil)
		return nil
	case "check_liveness":
		if decision.Outcome != "healthy" {
			logWatchdog(configPath, "watchdog", decision.Action, decision.Outcome, decision.Reason, nil)
		}
		return nil
	case "restart_service":
		if decision.Outcome == "skipped" {
			logWatchdog(configPath, "watchdog", decision.Action, decision.Outcome, decision.Reason, nil)
			return nil
		}
		if err := restartLinuxAgentService(state.PID); err != nil {
			logWatchdog(configPath, "watchdog", decision.Action, "failed", decision.Reason, err)
			return err
		}
		recordWatchdogRecovery(configPath, decision.Action, decision.Reason)
		logWatchdog(configPath, "watchdog", decision.Action, "success", decision.Reason, nil)
		return nil
	default:
		return nil
	}
}

type linuxServiceState struct {
	Exists      bool
	Running     bool
	PID         uint32
	ActiveState string
}

func queryLinuxAgentService() (linuxServiceState, error) {
	output, err := exec.Command("systemctl", "show", linuxServiceName, "--property=ActiveState", "--property=MainPID", "--no-page").CombinedOutput()
	if err != nil {
		return linuxServiceState{}, fmt.Errorf("query systemd service: %w: %s", err, strings.TrimSpace(string(output)))
	}
	state := linuxServiceState{Exists: true}
	for _, line := range strings.Split(string(output), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "ActiveState":
			state.ActiveState = strings.TrimSpace(value)
			state.Running = state.ActiveState == "active"
		case "MainPID":
			pid, _ := strconv.ParseUint(strings.TrimSpace(value), 10, 32)
			state.PID = uint32(pid)
		}
	}
	if state.ActiveState == "" {
		return state, fmt.Errorf("systemd service state missing")
	}
	return state, nil
}

func restartLinuxAgentService(pid uint32) error {
	_ = systemctl("stop", linuxServiceName)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		state, err := queryLinuxAgentService()
		if err == nil && !state.Running {
			return systemctl("start", linuxServiceName)
		}
		time.Sleep(time.Second)
	}
	if pid > 0 {
		if proc, err := os.FindProcess(int(pid)); err == nil {
			_ = proc.Kill()
		}
	}
	time.Sleep(2 * time.Second)
	return systemctl("start", linuxServiceName)
}

func systemctl(args ...string) error {
	output, err := exec.Command("systemctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func serviceExecutablePathFromConfig(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "Agent")
}
