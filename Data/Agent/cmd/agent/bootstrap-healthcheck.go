//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bunny-lab-io/borealis/go-agent/internal/auth"
	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
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
	ServiceExists  bool
	ServiceState   string
	ServiceRunning bool
	ServiceStarted bool
	EngineValid    bool
	Hostname       string
	ValidationText string
}

func assessInstallHealth(cfg BootstrapConfig, logger *BootstrapLogger) InstallHealth {
	startedAt := time.Now()
	logger.Tracef("Assessing existing Agent install health.")
	writeTimeline(cfg, "running", "Inspecting Existing Agent", "Checking local Borealis Agent footprint, Windows service, and Engine token validity.", 1)
	health := InstallHealth{Hostname: currentHostname()}
	health.Exists = dirExists(cfg.InstallDir)
	health.AgentExeExists = fileExists(filepath.Join(cfg.InstallDir, "Agent.exe"))
	tokenFootprint := existingAgentTokens(cfg)
	health.Exists = health.Exists || health.AgentExeExists || tokenFootprint
	logger.Tracef("Health filesystem: install_dir_exists=%t agent_exe_exists=%t token_footprint=%t", dirExists(cfg.InstallDir), health.AgentExeExists, tokenFootprint)

	serviceState, serviceExists := queryServiceState("BorealisAgent")
	health.ServiceExists = serviceExists
	health.ServiceState = serviceState
	health.ServiceRunning = strings.EqualFold(serviceState, "RUNNING")
	logger.Tracef("Health Windows service: name=%s exists=%t state=%s running=%t", "BorealisAgent", serviceExists, serviceState, health.ServiceRunning)
	if serviceExists {
		logger.Marker("__BOREALIS_AGENT_SERVICE_STATE__=" + serviceState)
	}
	if serviceExists && !health.ServiceRunning {
		logger.Tracef("Existing Agent service present but not running; start attempt beginning.")
		logger.Marker("__BOREALIS_AGENT_SERVICE_START_ATTEMPTED__=1")
		writeTimeline(cfg, "running", "Repairing Existing Agent Service", "Existing Borealis Agent service is present but not running; attempting service start.", 1)
		if err := startAgentRuntime(cfg, logger); err == nil {
			health.ServiceStarted = true
			time.Sleep(3 * time.Second)
			serviceState, serviceExists = queryServiceState("BorealisAgent")
			health.ServiceState = serviceState
			health.ServiceRunning = serviceExists && strings.EqualFold(serviceState, "RUNNING")
			logger.Tracef("Existing Agent service start result: state=%s running=%t", health.ServiceState, health.ServiceRunning)
		} else {
			logger.Warnf("Existing Agent service start failed: %v", err)
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
	logger.Tracef("Health assessment complete duration=%s exists=%t agent_exe=%t service_exists=%t service_running=%t service_started=%t engine_valid=%t", time.Since(startedAt).Round(time.Millisecond), health.Exists, health.AgentExeExists, health.ServiceExists, health.ServiceRunning, health.ServiceStarted, health.EngineValid)
	return health
}

func decideBootstrapAction(cfg BootstrapConfig, health InstallHealth, logger *BootstrapLogger) bootstrapAction {
	missing := missingBootstrapInputs(cfg)
	if logger != nil {
		logger.Tracef("Decision inputs: missing_inputs=%v deploy_intent=%t agent_exe=%t service_exists=%t service_running=%t service_started=%t engine_valid=%t", missing, cfg.DeployIntent, health.AgentExeExists, health.ServiceExists, health.ServiceRunning, health.ServiceStarted, health.EngineValid)
	}
	if health.Exists && logger != nil {
		logger.Marker("__BOREALIS_ONBOARDING_EXISTING_AGENT_DETECTED__=1")
		writeTimeline(cfg, "running", "Existing Agent Detected", "Existing Borealis Agent installation detected.", 1)
	}
	if cfg.DeployIntent {
		if len(missing) > 0 {
			return actionMissingInput
		}
		if logger != nil {
			logger.Tracef("Explicit deploy input supplied; treating existing Agent as in-place redeploy target.")
		}
		return actionDeploy
	}
	if health.AgentExeExists && health.ServiceExists && health.ServiceRunning && health.EngineValid {
		return actionAlreadyHealthy
	}
	if health.AgentExeExists && health.ServiceExists && health.ServiceStarted && health.EngineValid {
		return actionRepairOnly
	}
	if len(missing) > 0 {
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
	configPath := agentConfigPath(cfg.InstallDir)
	agentCfg, err := agentconfig.Load(configPath)
	if err != nil {
		return false, fmt.Sprintf("Existing Agent config could not be read: %v", err)
	}
	client, err := auth.NewClient(configPath, &agentCfg, "system")
	if err != nil {
		return false, fmt.Sprintf("Existing Agent authentication client failed: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	startedAt := time.Now()
	var response map[string]any
	resp, err := client.GetJSON(ctx, "/api/agent/metadata/1", &response)
	if err != nil {
		return false, fmt.Sprintf("Engine rejected existing Agent authentication: %v", err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, fmt.Sprintf("Existing Borealis Agent token accepted by Engine in %s.", time.Since(startedAt).Round(time.Millisecond))
	}
	return false, fmt.Sprintf("Engine rejected existing Borealis Agent token with HTTP %d after %s.", resp.StatusCode, time.Since(startedAt).Round(time.Millisecond))
}
