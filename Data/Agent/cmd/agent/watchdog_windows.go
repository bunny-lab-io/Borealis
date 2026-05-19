//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
	"github.com/bunny-lab-io/borealis/go-agent/internal/localui"
	agentruntime "github.com/bunny-lab-io/borealis/go-agent/internal/runtime"
)

const (
	watchdogHeartbeatStaleAfter = 15 * time.Minute
	watchdogRestartCooldown     = 30 * time.Minute
)

type watchdogState struct {
	LastRestartAt int64  `json:"last_restart_at"`
	RestartCount  int    `json:"restart_count"`
	LastReason    string `json:"last_reason"`
}

type watchdogHeartbeatMarker struct {
	Hostname  string `json:"hostname"`
	Timestamp int64  `json:"timestamp"`
}

func runAgentWatchdog(options agentruntime.Options) error {
	cfg := watchdogConfig(options)
	logger, closeLog, err := openWatchdogLogger(cfg)
	if err != nil {
		return err
	}
	defer closeLog()

	logger.Tracef("Agent watchdog check start: install_dir=%s config_path=%s", cfg.InstallDir, cfg.ConfigPath)
	if !fileExists(filepath.Join(cfg.InstallDir, "Agent.exe")) {
		return fmt.Errorf("Agent.exe not found at %s", cfg.InstallDir)
	}

	if err := ensureMaintenanceTasksPresent(cfg, logger); err != nil {
		logger.Warnf("Watchdog maintenance task reconciliation failed: %v", err)
	}

	task := queryScheduledTask(agentTaskName)
	if !task.Exists {
		logger.Warnf("Borealis Agent task missing; recreating task.")
		if err := ensureAgentTaskDefinition(cfg, logger); err != nil {
			return err
		}
		if err := startScheduledTask(agentTaskName, logger); err != nil {
			return err
		}
		recordWatchdogRestart(cfg, "task_missing", logger)
		return nil
	}
	if !taskScheduledStateIsRunning(task.State) {
		logger.Warnf("Borealis Agent task not running; start requested. state=%s", task.State)
		if err := startScheduledTask(agentTaskName, logger); err != nil {
			return err
		}
		recordWatchdogRestart(cfg, "task_not_running", logger)
		return nil
	}

	if agentUpdateBusy(cfg, logger) {
		logger.Tracef("Agent watchdog skipped restart: update in progress.")
		return nil
	}

	lastHeartbeat, heartbeatSource := lastSuccessfulHeartbeatAt(cfg)
	if lastHeartbeat.IsZero() {
		graceStart := watchdogGraceStart(cfg)
		if !graceStart.IsZero() && time.Since(graceStart) < watchdogHeartbeatStaleAfter {
			logger.Tracef("Agent watchdog grace active: heartbeat missing source=%s grace_age=%s", heartbeatSource, time.Since(graceStart).Round(time.Second))
			return nil
		}
		return restartAgentFromWatchdog(cfg, "heartbeat_missing", logger)
	}
	age := time.Since(lastHeartbeat)
	if age <= watchdogHeartbeatStaleAfter {
		logger.Tracef("Agent watchdog healthy: last_heartbeat_age=%s source=%s", age.Round(time.Second), heartbeatSource)
		return nil
	}
	return restartAgentFromWatchdog(cfg, fmt.Sprintf("heartbeat_stale_%s", age.Round(time.Second)), logger)
}

func watchdogConfig(options agentruntime.Options) BootstrapConfig {
	cfg := defaultBootstrapConfig()
	cfg.ConfigPath = agentConfigPath(cfg.InstallDir)
	cfg.Verbose = options.Verbose
	if strings.TrimSpace(options.ConfigPath) != "" {
		cfg.ConfigPath = strings.TrimSpace(options.ConfigPath)
		cfg.InstallDir = filepath.Dir(cfg.ConfigPath)
	}
	if current, err := agentconfig.Load(cfg.ConfigPath); err == nil {
		cfg.ServerURL = current.ServerURL
		cfg.ReleaseChannel = agentconfig.NormalizeReleaseChannel(current.Agent.ReleaseChannel)
		cfg.RepoRef = agentconfig.NormalizeBranch(current.Agent.Branch)
	}
	if strings.TrimSpace(options.ServerURL) != "" {
		cfg.ServerURL = options.ServerURL
	}
	if strings.TrimSpace(options.RepoRef) != "" {
		cfg.RepoRef = agentconfig.NormalizeBranch(options.RepoRef)
	}
	if strings.TrimSpace(options.ReleaseChannel) != "" {
		cfg.ReleaseChannel = agentconfig.NormalizeReleaseChannel(options.ReleaseChannel)
	}
	return cfg
}

func openWatchdogLogger(cfg BootstrapConfig) (*BootstrapLogger, func(), error) {
	logPath := filepath.Join(cfg.InstallDir, "Logs", "Agent", "watchdog.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, nil, err
	}
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, err
	}
	logger := &BootstrapLogger{
		primaryFile: file,
		lastPath:    logPath,
		redacts: []string{
			cfg.SiteEnrollmentCode,
			cfg.LegacyEnrollment,
			cfg.ServerURL,
		},
	}
	return logger, func() { _ = file.Close() }, nil
}

func ensureMaintenanceTasksPresent(cfg BootstrapConfig, logger *BootstrapLogger) error {
	if !queryScheduledTask(agentUpdaterTaskName).Exists {
		if err := ensureAgentUpdaterTask(cfg, logger); err != nil {
			return err
		}
	}
	if !queryScheduledTask(agentWatchdogTaskName).Exists {
		if err := ensureAgentWatchdogTask(cfg, logger); err != nil {
			return err
		}
	}
	return nil
}

func ensureAgentTaskDefinition(cfg BootstrapConfig, logger *BootstrapLogger) error {
	agentExe := filepath.Join(cfg.InstallDir, "Agent.exe")
	if !fileExists(agentExe) {
		return fmt.Errorf("Agent.exe not found at %s", agentExe)
	}
	taskAction := fmt.Sprintf(`"%s" --system-service`, agentExe)
	return createOrReplaceTask(agentTaskName, taskAction, "ONSTART", logger)
}

func agentUpdateBusy(cfg BootstrapConfig, logger *BootstrapLogger) bool {
	if task := queryScheduledTask(agentUpdaterTaskName); taskScheduledStateIsRunning(task.State) {
		logger.Tracef("AutoUpdater task running; watchdog will not restart Agent.")
		return true
	}
	for _, path := range []string{
		filepath.Join(cfg.InstallDir, "Agent.exe.update"),
		updateTempDir(cfg),
	} {
		info, err := os.Stat(path)
		if err == nil && time.Since(info.ModTime()) < 30*time.Minute {
			logger.Tracef("Recent update artifact present; watchdog will not restart Agent. path=%s age=%s", path, time.Since(info.ModTime()).Round(time.Second))
			return true
		}
	}
	return false
}

func lastSuccessfulHeartbeatAt(cfg BootstrapConfig) (time.Time, string) {
	markerPath := filepath.Join(cfg.InstallDir, "Logs", "Agent", "heartbeat-success.json")
	if data, err := os.ReadFile(markerPath); err == nil {
		var marker watchdogHeartbeatMarker
		if json.Unmarshal(data, &marker) == nil && marker.Timestamp > 0 {
			return time.Unix(marker.Timestamp, 0), markerPath
		}
	}
	if snapshot, err := localui.ReadStatusSnapshot(""); err == nil && snapshot.LastHeartbeatAt > 0 {
		return time.Unix(snapshot.LastHeartbeatAt, 0), localui.StatusPath("")
	}
	return time.Time{}, "missing"
}

func watchdogGraceStart(cfg BootstrapConfig) time.Time {
	candidates := []string{
		filepath.Join(cfg.InstallDir, "Logs", "Agent", "heartbeat-success.json"),
		localui.StatusPath(""),
		cfg.ConfigPath,
	}
	var newest time.Time
	for _, path := range candidates {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return newest
}

func restartAgentFromWatchdog(cfg BootstrapConfig, reason string, logger *BootstrapLogger) error {
	state := readWatchdogState(cfg)
	if state.LastRestartAt > 0 {
		lastRestart := time.Unix(state.LastRestartAt, 0)
		if time.Since(lastRestart) < watchdogRestartCooldown {
			logger.Warnf("Agent watchdog restart suppressed by cooldown: reason=%s last_restart_age=%s", reason, time.Since(lastRestart).Round(time.Second))
			return nil
		}
	}
	logger.Warnf("Agent watchdog restarting Agent: reason=%s", reason)
	stopScheduledTask(agentTaskName, logger)
	stopBorealisProcesses(cfg, logger)
	if err := startScheduledTask(agentTaskName, logger); err != nil {
		return err
	}
	recordWatchdogRestart(cfg, reason, logger)
	return nil
}

func watchdogStatePath(cfg BootstrapConfig) string {
	return filepath.Join(cfg.InstallDir, "Logs", "Agent", "watchdog-state.json")
}

func readWatchdogState(cfg BootstrapConfig) watchdogState {
	data, err := os.ReadFile(watchdogStatePath(cfg))
	if err != nil {
		return watchdogState{}
	}
	var state watchdogState
	if json.Unmarshal(data, &state) != nil {
		return watchdogState{}
	}
	return state
}

func recordWatchdogRestart(cfg BootstrapConfig, reason string, logger *BootstrapLogger) {
	state := readWatchdogState(cfg)
	state.LastRestartAt = time.Now().Unix()
	state.RestartCount++
	state.LastReason = reason
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		logger.Warnf("Watchdog state encode failed: %v", err)
		return
	}
	payload = append(payload, '\n')
	path := watchdogStatePath(cfg)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		logger.Warnf("Watchdog state directory failed: %v", err)
		return
	}
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		logger.Warnf("Watchdog state write failed: %v", err)
	}
}
