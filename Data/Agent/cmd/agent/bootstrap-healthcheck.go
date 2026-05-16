//go:build windows

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type bootstrapAction string

const (
	actionAlreadyHealthy bootstrapAction = "already_healthy"
	actionRepairOnly     bootstrapAction = "repair_only"
	actionDeploy         bootstrapAction = "deploy"
	actionMissingInput   bootstrapAction = "missing_input"
)

type InstallHealth struct {
	Exists         bool
	AgentExeExists bool
	TaskExists     bool
	TaskState      string
	TaskRunning    bool
	TaskStarted    bool
	EngineValid    bool
	Hostname       string
	ValidationText string
}

func assessInstallHealth(cfg BootstrapConfig, logger *BootstrapLogger) InstallHealth {
	startedAt := time.Now()
	logger.Tracef("Assessing existing Agent install health.")
	writeTimeline(cfg, "running", "Inspecting Existing Agent", "Checking local Borealis Agent footprint, scheduled tasks, and Engine token validity.", 1)
	health := InstallHealth{Hostname: currentHostname()}
	health.Exists = dirExists(cfg.InstallDir)
	health.AgentExeExists = fileExists(filepath.Join(cfg.InstallDir, "Agent.exe"))
	tokenFootprint := existingAgentTokens(cfg)
	health.Exists = health.Exists || health.AgentExeExists || tokenFootprint
	logger.Tracef("Health filesystem: install_dir_exists=%t agent_exe_exists=%t token_footprint=%t", dirExists(cfg.InstallDir), health.AgentExeExists, tokenFootprint)

	task := queryScheduledTask(agentTaskName)
	health.TaskExists = task.Exists
	health.TaskState = task.State
	health.TaskRunning = taskScheduledStateIsRunning(task.State)
	logger.Tracef("Health scheduled task: name=%s exists=%t state=%s running=%t error=%s", agentTaskName, task.Exists, task.State, health.TaskRunning, task.Error)
	if task.Exists {
		logger.Marker("__BOREALIS_AGENT_TASK_STATE__=" + task.State)
	}
	if task.Exists && !health.TaskRunning {
		logger.Tracef("Existing Agent task present but not running; start attempt beginning.")
		logger.Marker("__BOREALIS_AGENT_TASK_START_ATTEMPTED__=1")
		writeTimeline(cfg, "running", "Repairing Existing Agent Task", "Existing Borealis Agent task is present but not running; attempting task start.", 1)
		if err := startScheduledTask(agentTaskName, logger); err == nil {
			health.TaskStarted = true
			time.Sleep(3 * time.Second)
			task = queryScheduledTask(agentTaskName)
			health.TaskState = task.State
			health.TaskRunning = taskScheduledStateIsRunning(task.State)
			logger.Tracef("Existing Agent task start result: state=%s running=%t", health.TaskState, health.TaskRunning)
		} else {
			logger.Warnf("Existing Agent task start failed: %v", err)
		}
	}
	logger.Tracef("Validating existing Agent token against Engine.")
	valid, detail := validateExistingAgentWithEngine(cfg)
	health.EngineValid = valid
	health.ValidationText = detail
	if detail != "" {
		logger.Infof("%s", detail)
		eventStatus := "completed"
		writeTimeline(cfg, eventStatus, "Validating Existing Agent Token", detail, 0)
	}
	logger.Tracef("Health assessment complete duration=%s exists=%t agent_exe=%t task_exists=%t task_running=%t task_started=%t engine_valid=%t", time.Since(startedAt).Round(time.Millisecond), health.Exists, health.AgentExeExists, health.TaskExists, health.TaskRunning, health.TaskStarted, health.EngineValid)
	return health
}

func decideBootstrapAction(cfg BootstrapConfig, health InstallHealth, logger *BootstrapLogger) bootstrapAction {
	logger.Tracef("Decision inputs: missing_inputs=%v agent_exe=%t task_exists=%t task_running=%t task_started=%t engine_valid=%t", missingBootstrapInputs(cfg), health.AgentExeExists, health.TaskExists, health.TaskRunning, health.TaskStarted, health.EngineValid)
	if health.Exists {
		logger.Marker("__BOREALIS_ONBOARDING_EXISTING_AGENT_DETECTED__=1")
		writeTimeline(cfg, "running", "Existing Agent Detected", "Existing Borealis Agent installation detected.", 1)
	}
	if health.AgentExeExists && health.TaskExists && health.TaskRunning && health.EngineValid {
		return actionAlreadyHealthy
	}
	if health.AgentExeExists && health.TaskExists && health.TaskStarted && health.EngineValid {
		return actionRepairOnly
	}
	if len(missingBootstrapInputs(cfg)) > 0 {
		return actionMissingInput
	}
	return actionDeploy
}

func existingAgentTokens(cfg BootstrapConfig) bool {
	if readConfigAccessToken(cfg) != "" {
		return true
	}
	for _, path := range []string{
		filepath.Join(agentSettingsDir(cfg.InstallDir), "access.jwt"),
		filepath.Join(agentSettingsDir(cfg.InstallDir), "refresh.token"),
		filepath.Join(agentSettingsDir(cfg.InstallDir), "Agent_GUID.txt"),
	} {
		if fileExists(path) {
			return true
		}
	}
	return false
}

func validateExistingAgentWithEngine(cfg BootstrapConfig) (bool, string) {
	serverURL := strings.TrimRight(strings.TrimSpace(cfg.ServerURL), "/")
	if serverURL == "" {
		return false, "Server URL unavailable for existing Agent validation."
	}
	token := readConfigAccessToken(cfg)
	if token == "" {
		tokenBytes, err := os.ReadFile(filepath.Join(agentSettingsDir(cfg.InstallDir), "access.jwt"))
		if err != nil {
			return false, "Existing Agent access token missing."
		}
		token = strings.TrimSpace(string(tokenBytes))
	}
	if token == "" {
		return false, "Existing Agent access token missing."
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	startedAt := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+"/api/repo/current_hash?ttl=300", nil)
	if err != nil {
		return false, fmt.Sprintf("Existing Agent validation request failed: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, fmt.Sprintf("Engine did not answer existing Agent validation: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, fmt.Sprintf("Existing Borealis Agent token accepted by Engine in %s.", time.Since(startedAt).Round(time.Millisecond))
	}
	return false, fmt.Sprintf("Engine rejected existing Borealis Agent token with HTTP %d after %s.", resp.StatusCode, time.Since(startedAt).Round(time.Millisecond))
}
