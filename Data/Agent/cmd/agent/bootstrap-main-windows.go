//go:build windows

package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

func runBootstrapConsole(cli cliOptions) int {
	cfg, err := loadBootstrapConfig(cli, false)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%v\n", err)
		return 2
	}
	logger, closeLog, err := openBootstrapLogger(cfg, true)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	defer closeLog()
	exitCode := runBootstrap(cfg, logger)
	pauseInteractiveConsole(cfg)
	return exitCode
}

func pauseInteractiveConsole(cfg BootstrapConfig) {
	if !cfg.Interactive || cfg.NonInteractive || cfg.Uninstall {
		return
	}
	_, _ = fmt.Fprint(os.Stdout, "\nAgent bootstrap complete. Press Enter to exit...")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

func runBootstrap(cfg BootstrapConfig, logger *BootstrapLogger) int {
	startedAt := time.Now()
	logger.Stepf("Running Agent Bootstrap.")
	logger.Marker("__BOREALIS_AGENT_EXE_STARTED__=1")
	logger.Marker("__BOREALIS_ONBOARDING_HOSTNAME__=" + currentHostname())
	logBootstrapConfigSummary(cfg, logger)
	writeTimeline(cfg, "running", "Running Agent Bootstrap", "Agent.exe started.", 1)
	writeState(cfg, "running", 1, "Agent.exe started.")

	if cfg.Uninstall {
		logger.Stepf("Uninstalling Borealis Agent.")
		logger.Tracef("Bootstrap action requested: uninstall")
		if err := uninstallBorealis(cfg, logger); err != nil {
			logger.Errorf("Uninstall failed: %v", err)
			return 1
		}
		logger.Tracef("Bootstrap completed action=uninstall duration=%s exit_code=0", time.Since(startedAt).Round(time.Millisecond))
		return 0
	}

	logger.Stepf("Preparing Bootstrap Runtime.")
	logger.Tracef("Acquiring bootstrap mutex.")
	release, acquired, err := acquireBootstrapMutex()
	if err != nil {
		logger.Errorf("Bootstrap mutex failed: %v", err)
		writeState(cfg, "failed", 1, err.Error())
		return 1
	}
	if !acquired {
		logger.Tracef("Bootstrap mutex already held by another process.")
		logger.Marker("__BOREALIS_ONBOARDING_ALREADY_RUNNING__=1")
		writeTimeline(cfg, "skipped", "Agent Bootstrap Mutex", "Another Borealis Agent bootstrap is already running.", 73)
		writeState(cfg, "already_pending", 73, "Another Borealis Agent bootstrap is already running.")
		return 73
	}
	logger.Tracef("Bootstrap mutex acquired.")
	defer release()

	logger.Tracef("Ensuring bootstrap directories.")
	if err := ensureBootstrapDirs(cfg); err != nil {
		logger.Errorf("Directory preparation failed: %v", err)
		writeState(cfg, "failed", 1, err.Error())
		return 1
	}
	logger.Tracef("Bootstrap directories ready.")

	logger.Stepf("Checking Existing Agent Installation.")
	health := assessInstallHealth(cfg, logger)
	if health.Hostname != "" {
		logger.Marker("__BOREALIS_ONBOARDING_HOSTNAME__=" + health.Hostname)
	}
	action := decideBootstrapAction(cfg, health, logger)
	logger.Tracef("Bootstrap decision: action=%s health_exists=%t agent_exe=%t task_exists=%t task_state=%s task_running=%t task_started=%t engine_valid=%t validation=%s", action, health.Exists, health.AgentExeExists, health.TaskExists, health.TaskState, health.TaskRunning, health.TaskStarted, health.EngineValid, health.ValidationText)
	if action == actionMissingInput {
		missing := missingBootstrapInputs(cfg)
		detail := "Missing required bootstrap input: " + strings.Join(missing, ", ")
		logger.Tracef("Bootstrap missing input: %s", strings.Join(missing, ", "))
		if cfg.Interactive {
			logger.Tracef("Interactive console detected; prompting for missing bootstrap input.")
			promptForMissingInputs(&cfg, logger)
			if len(missingBootstrapInputs(cfg)) == 0 {
				logger.Tracef("Interactive bootstrap input collected.")
				action = actionDeploy
			} else {
				logger.Errorf(detail)
				writeState(cfg, "failed", 1, detail)
				return 1
			}
		} else {
			logger.Errorf(detail)
			writeState(cfg, "failed", 1, detail)
			writeTimeline(cfg, "failed", "Bootstrap Input Required", detail, 1)
			return 1
		}
	}

	switch action {
	case actionAlreadyHealthy:
		logger.Stepf("Existing Agent Healthy; Checking For Updates.")
		logger.Tracef("Existing Agent healthy. Running update check then exiting skipped.")
		logger.Marker("__BOREALIS_ONBOARDING_ALREADY_ENROLLED__=1")
		writeTimeline(cfg, "skipped", "Already Enrolled and Active", "Existing Borealis Agent is already enrolled and active.", 73)
		writeState(cfg, "already_enrolled", 73, "Existing Borealis Agent is already enrolled and active.")
		if err := runAgentUpdateCheck(cfg, logger); err != nil {
			logger.Warnf("Update check skipped or failed: %v", err)
		}
		logger.Marker("__BOREALIS_WINDOWS_ONBOARDING_EXIT_CODE__=73")
		logger.Tracef("Bootstrap completed action=%s duration=%s exit_code=73", action, time.Since(startedAt).Round(time.Millisecond))
		return 73
	case actionRepairOnly:
		logger.Stepf("Repairing Existing Agent Scheduled Task.")
		logger.Tracef("Existing Agent repaired by starting scheduled task. Running update check then exiting skipped.")
		writeTimeline(cfg, "running", "Successfully Repaired Agent", "Existing Borealis Agent task was repaired.", 0)
		writeState(cfg, "already_enrolled", 73, "Existing Borealis Agent was repaired and is active.")
		if err := runAgentUpdateCheck(cfg, logger); err != nil {
			logger.Warnf("Update check skipped or failed: %v", err)
		}
		logger.Marker("__BOREALIS_ONBOARDING_AGENT_REPAIRED__=1")
		logger.Marker("__BOREALIS_ONBOARDING_ALREADY_ENROLLED__=1")
		logger.Marker("__BOREALIS_WINDOWS_ONBOARDING_EXIT_CODE__=73")
		logger.Tracef("Bootstrap completed action=%s duration=%s exit_code=73", action, time.Since(startedAt).Round(time.Millisecond))
		return 73
	case actionDeploy:
		logger.Stepf("Installing Or Repairing Borealis Agent.")
		logger.Tracef("Deploy/redeploy path starting.")
		if health.Exists {
			logger.Marker("__BOREALIS_ONBOARDING_EXISTING_AGENT_DETECTED__=1")
			logger.Marker("__BOREALIS_ONBOARDING_REDEPLOY_REQUIRED__=1")
			writeTimeline(cfg, "running", "Unable to Repair Agent > Re-Deploying", "Existing install could not be validated; re-deploying.", 1)
		}
		if err := installOrRedeployAgent(cfg, logger); err != nil {
			logger.Errorf("Agent bootstrap failed: %v", err)
			writeState(cfg, "failed", 1, err.Error())
			writeTimeline(cfg, "failed", "Running Agent Bootstrap", err.Error(), 1)
			logger.Marker("__BOREALIS_WINDOWS_ONBOARDING_EXIT_CODE__=1")
			logger.Tracef("Bootstrap completed action=%s duration=%s exit_code=1", action, time.Since(startedAt).Round(time.Millisecond))
			return 1
		}
		logger.Marker("__BOREALIS_WINDOWS_ONBOARDING_EXIT_CODE__=0")
		logger.Tracef("Bootstrap completed action=%s duration=%s exit_code=0", action, time.Since(startedAt).Round(time.Millisecond))
		return 0
	default:
		err := errors.New("unsupported bootstrap action")
		logger.Errorf("%v", err)
		writeState(cfg, "failed", 1, err.Error())
		return 1
	}
}

func installOrRedeployAgent(cfg BootstrapConfig, logger *BootstrapLogger) error {
	startedAt := time.Now()
	logger.Tracef("Install/redeploy sequence start.")
	logger.Stepf("Cleaning Up Existing Agent Processes.")
	logger.Tracef("Stopping stale Borealis-owned processes before staging runtime.")
	stopBorealisProcesses(cfg, logger)
	if err := copySelfToInstallRoot(cfg, logger); err != nil {
		return err
	}
	logger.Stepf("(Re)Installing Agent Support Dependencies.")
	if err := ensureAgentDependencies(cfg, logger); err != nil {
		return err
	}
	logger.Tracef("Agent support dependencies ready.")
	writeTimeline(cfg, "running", "Configuring Agent Runtime", "Writing Go Agent configuration.", 1)
	logger.Stepf("Reconciling Remote Access Services.")
	reconcileUltraVNCServiceAfterRuntimeStage(cfg, logger)
	logger.Stepf("Writing Agent Configuration.")
	if err := writeGoAgentConfig(cfg, logger); err != nil {
		return err
	}
	logger.Tracef("Agent config.json ready.")
	if err := applyInstallTreeACL(cfg, logger); err != nil {
		return err
	}
	logger.Stepf("Creating Scheduled Tasks And Starting Agent.")
	if err := ensureAgentTasks(cfg, logger); err != nil {
		return err
	}
	logger.Tracef("Agent scheduled tasks ready.")
	writeState(cfg, "pending_approval", 0, "Agent.exe completed; device approval pending operator action.")
	writeTimeline(cfg, "running", "Agent Ready and Awaiting Approval", "Agent.exe completed; device approval pending operator action.", 0)
	logger.Stepf("Agent Ready And Awaiting Approval.")
	logger.Tracef("Install/redeploy sequence complete duration=%s.", time.Since(startedAt).Round(time.Millisecond))
	return nil
}
