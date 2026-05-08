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
	AgentPyExists  bool
	TaskExists     bool
	TaskState      string
	TaskRunning    bool
	TaskStarted    bool
	EngineValid    bool
	Hostname       string
	ValidationText string
}

func assessInstallHealth(cfg BootstrapConfig, logger *BootstrapLogger) InstallHealth {
	health := InstallHealth{Hostname: currentHostname()}
	health.Exists = dirExists(cfg.InstallDir)
	health.AgentPyExists = fileExists(filepath.Join(cfg.InstallDir, "Agent", "Borealis", "agent.py"))
	health.Exists = health.Exists || health.AgentPyExists || existingAgentTokens(cfg)

	task := queryScheduledTask(agentTaskName)
	health.TaskExists = task.Exists
	health.TaskState = task.State
	health.TaskRunning = taskScheduledStateIsRunning(task.State)
	if task.Exists {
		logger.Marker("__BOREALIS_AGENT_TASK_STATE__=" + task.State)
	}
	if task.Exists && !health.TaskRunning {
		logger.Marker("__BOREALIS_AGENT_TASK_START_ATTEMPTED__=1")
		if err := startScheduledTask(agentTaskName, logger); err == nil {
			health.TaskStarted = true
			time.Sleep(3 * time.Second)
			task = queryScheduledTask(agentTaskName)
			health.TaskState = task.State
			health.TaskRunning = taskScheduledStateIsRunning(task.State)
		} else {
			logger.Warnf("Existing Agent task start failed: %v", err)
		}
	}
	valid, detail := validateExistingAgentWithEngine(cfg)
	health.EngineValid = valid
	health.ValidationText = detail
	if detail != "" {
		logger.Infof("%s", detail)
	}
	return health
}

func decideBootstrapAction(cfg BootstrapConfig, health InstallHealth, logger *BootstrapLogger) bootstrapAction {
	if health.Exists {
		logger.Marker("__BOREALIS_ONBOARDING_EXISTING_AGENT_DETECTED__=1")
		writeTimeline(cfg, "running", "Existing Agent Detected", "Existing Borealis Agent installation detected.", 1)
	}
	if health.AgentPyExists && health.TaskExists && health.TaskRunning && health.EngineValid {
		return actionAlreadyHealthy
	}
	if health.AgentPyExists && health.TaskExists && health.TaskStarted && health.EngineValid {
		return actionRepairOnly
	}
	if len(missingBootstrapInputs(cfg)) > 0 {
		return actionMissingInput
	}
	return actionDeploy
}

func existingAgentTokens(cfg BootstrapConfig) bool {
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
	tokenBytes, err := os.ReadFile(filepath.Join(agentSettingsDir(cfg.InstallDir), "access.jwt"))
	if err != nil {
		return false, "Existing Agent access token missing."
	}
	token := strings.TrimSpace(string(tokenBytes))
	if token == "" {
		return false, "Existing Agent access token empty."
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
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
		return true, "Existing Borealis Agent token accepted by Engine."
	}
	return false, fmt.Sprintf("Engine rejected existing Borealis Agent token with HTTP %d.", resp.StatusCode)
}
