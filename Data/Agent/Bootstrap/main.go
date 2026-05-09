//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows/svc"
)

func main() {
	os.Exit(runMain())
}

func runMain() int {
	cli, err := parseCLI(os.Args[1:])
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%v\n", err)
		return 2
	}

	isService, err := svc.IsWindowsService()
	if err == nil && isService {
		return runBootstrapWindowsService(cli)
	}
	return runBootstrapConsole(cli)
}

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
	return runBootstrap(cfg, logger)
}

func runBootstrap(cfg BootstrapConfig, logger *BootstrapLogger) int {
	logger.Marker("__BOREALIS_AGENT_EXE_STARTED__=1")
	logger.Marker("__BOREALIS_ONBOARDING_HOSTNAME__=" + currentHostname())
	writeTimeline(cfg, "running", "Running Agent Bootstrap", "Agent.exe started.", 1)
	writeState(cfg, "running", 1, "Agent.exe started.")

	if cfg.Uninstall {
		if err := uninstallBorealis(cfg, logger); err != nil {
			logger.Errorf("Uninstall failed: %v", err)
			return 1
		}
		return 0
	}

	release, acquired, err := acquireBootstrapMutex()
	if err != nil {
		logger.Errorf("Bootstrap mutex failed: %v", err)
		writeState(cfg, "failed", 1, err.Error())
		return 1
	}
	if !acquired {
		logger.Marker("__BOREALIS_ONBOARDING_ALREADY_RUNNING__=1")
		writeTimeline(cfg, "skipped", "Agent Bootstrap Mutex", "Another Borealis Agent bootstrap is already running.", 73)
		writeState(cfg, "already_pending", 73, "Another Borealis Agent bootstrap is already running.")
		return 73
	}
	defer release()

	if err := ensureBootstrapDirs(cfg); err != nil {
		logger.Errorf("Directory preparation failed: %v", err)
		writeState(cfg, "failed", 1, err.Error())
		return 1
	}

	health := assessInstallHealth(cfg, logger)
	if health.Hostname != "" {
		logger.Marker("__BOREALIS_ONBOARDING_HOSTNAME__=" + health.Hostname)
	}
	action := decideBootstrapAction(cfg, health, logger)
	if action == actionMissingInput {
		missing := missingBootstrapInputs(cfg)
		detail := "Missing required bootstrap input: " + strings.Join(missing, ", ")
		if cfg.Interactive {
			promptForMissingInputs(&cfg, logger)
			if len(missingBootstrapInputs(cfg)) == 0 {
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
		logger.Marker("__BOREALIS_ONBOARDING_ALREADY_ENROLLED__=1")
		writeTimeline(cfg, "skipped", "Already Enrolled and Active", "Existing Borealis Agent is already enrolled and active.", 73)
		writeState(cfg, "already_enrolled", 73, "Existing Borealis Agent is already enrolled and active.")
		if err := runAgentUpdateCheck(cfg, logger); err != nil {
			logger.Warnf("Update check skipped or failed: %v", err)
		}
		logger.Marker("__BOREALIS_WINDOWS_ONBOARDING_EXIT_CODE__=73")
		return 73
	case actionRepairOnly:
		writeTimeline(cfg, "running", "Successfully Repaired Agent", "Existing Borealis Agent task was repaired.", 0)
		writeState(cfg, "already_enrolled", 73, "Existing Borealis Agent was repaired and is active.")
		if err := runAgentUpdateCheck(cfg, logger); err != nil {
			logger.Warnf("Update check skipped or failed: %v", err)
		}
		logger.Marker("__BOREALIS_ONBOARDING_AGENT_REPAIRED__=1")
		logger.Marker("__BOREALIS_ONBOARDING_ALREADY_ENROLLED__=1")
		logger.Marker("__BOREALIS_WINDOWS_ONBOARDING_EXIT_CODE__=73")
		return 73
	case actionDeploy:
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
			return 1
		}
		logger.Marker("__BOREALIS_WINDOWS_ONBOARDING_EXIT_CODE__=0")
		return 0
	default:
		err := errors.New("unsupported bootstrap action")
		logger.Errorf("%v", err)
		writeState(cfg, "failed", 1, err.Error())
		return 1
	}
}

func installOrRedeployAgent(cfg BootstrapConfig, logger *BootstrapLogger) error {
	writeTimeline(cfg, "running", "Running Agent Bootstrap", "Preparing Borealis Agent install.", 1)
	if err := copySelfToInstallRoot(cfg, logger); err != nil {
		return err
	}
	stopBorealisProcesses(cfg, logger)
	sourceRoot, cleanup, err := preparePayloadSource(cfg, logger)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return err
	}
	if err := ensureAgentDependencies(cfg, logger); err != nil {
		return err
	}
	writeTimeline(cfg, "running", "Configuring Agent Runtime", "Creating Python environment and staging Agent files.", 1)
	if err := setupPythonEnvironment(cfg, sourceRoot, logger); err != nil {
		return err
	}
	if err := stageAgentRuntime(cfg, sourceRoot, logger); err != nil {
		return err
	}
	if err := writeAgentSettings(cfg, logger); err != nil {
		return err
	}
	if err := ensureAgentTasks(cfg, logger); err != nil {
		return err
	}
	writeState(cfg, "pending_approval", 0, "Agent.exe completed; device approval pending operator action.")
	writeTimeline(cfg, "running", "Agent Ready and Awaiting Approval", "Agent.exe completed; device approval pending operator action.", 0)
	return nil
}
