//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bunny-lab-io/borealis/go-agent/internal/auth"
	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
	"github.com/bunny-lab-io/borealis/go-agent/internal/logutil"
	agentruntime "github.com/bunny-lab-io/borealis/go-agent/internal/runtime"
)

type updateManifest struct {
	TargetBuildID  string `json:"target_build_id"`
	ArtifactSHA256 string `json:"artifact_sha256"`
	DownloadPath   string `json:"download_path"`
}

func runAgentUpdateCheck(cfg BootstrapConfig, logger *BootstrapLogger) (resultErr error) {
	startedAt := time.Now()
	configPath := filepath.Join(cfg.InstallDir, agentconfig.FileName)
	reporter := newUpdateProgressReporter(configPath, logger)
	if updateOperationIsActive(reporter.operation()) {
		markConfigUpdateOperation(configPath, "running", "")
		reporter.emit("requesting_agent_update", "", "success", "Agent Received Request", "Agent persisted update operation before acknowledgement.", "")
		reporter.emit("resolving_engine_artifact", "", "running", "Resolving Engine Artifact", "Requesting current authenticated Agent artifact manifest.", "")
	}
	defer func() {
		if resultErr != nil && updateOperationIsActive(reporter.operation()) {
			reporter.emit("update_completed", "", "failed", "Agent Update Failed", resultErr.Error(), "failed")
		}
	}()
	_ = logutil.RotateAndPrune(
		filepath.Join(cfg.InstallDir, "Logs", "Agent", "updater.log"),
		logutil.RetentionDaysFromConfig(configPath),
		startedAt,
	)
	defer cleanupAgentTemp(cfg, logger)
	cleanupStaleAgentUpdateBinary(cfg, logger)
	serverURL := strings.TrimRight(strings.TrimSpace(cfg.ServerURL), "/")
	if serverURL == "" {
		return fmt.Errorf("server URL missing")
	}
	token := readConfigAccessToken(cfg)
	if token == "" {
		token = strings.TrimSpace(readFirstLine(filepath.Join(agentSettingsDir(cfg.InstallDir), "access.jwt")))
	}
	if token == "" {
		return fmt.Errorf("access token missing")
	}
	engineHTTPClient, err := bootstrapEngineHTTPClient(cfg)
	if err != nil {
		return err
	}
	installed := readConfigInstalledBuildID(cfg)
	logger.Tracef("Agent update check start: source=engine installed_build_id=%s", installed)
	return runEngineManifestUpdateCheck(cfg, logger, reporter, installed, startedAt, serverURL, token, engineHTTPClient)
}

func runEngineManifestUpdateCheck(cfg BootstrapConfig, logger *BootstrapLogger, reporter *updateProgressReporter, installed string, startedAt time.Time, serverURL string, token string, engineHTTPClient *http.Client) error {
	configPath := filepath.Join(cfg.InstallDir, agentconfig.FileName)
	manifest, err := fetchUpdateManifest(engineHTTPClient, serverURL, token, installed)
	if err != nil {
		return err
	}
	target := strings.TrimSpace(strings.ToLower(manifest.TargetBuildID))
	logger.Tracef("Agent update manifest: source=engine target_build_id=%s installed_build_id=%s artifact_sha256_present=%t download_path_present=%t", target, installed, strings.TrimSpace(manifest.ArtifactSHA256) != "", strings.TrimSpace(manifest.DownloadPath) != "")
	if target == "" {
		return fmt.Errorf("update manifest missing target_build_id")
	}
	if installed != "" && strings.EqualFold(installed, target) {
		logger.Infof("Agent update check: up to date (%s).", target)
		operation := reporter.operation()
		if !updateOperationIsActive(operation) || operation.Source == updateSourceHourly {
			logger.Tracef("Agent update check complete: up_to_date non_disruptive=true duration=%s", time.Since(startedAt).Round(time.Millisecond))
			return nil
		}
		reporter.setBuilds(target, installed, installed)
		reporter.emit("resolving_engine_artifact", "", "success", "Current Binary Retained", "Installed Agent binary already matches Engine artifact.", "")
		reporter.emit("downloading_agent_artifact", "resolving_engine_artifact", "skipped", "Skipped When Current", "Binary download not required for install-equivalent repair.", "")
		reporter.emit("verifying_agent_artifact", "downloading_agent_artifact", "skipped", "Skipped When Current", "Current installed binary retained.", "")
		identityBefore := agentUpdateIdentityFingerprint(configPath)
		reporter.emit("protecting_agent_identity_trust", "verifying_agent_artifact", "running", "Protecting Agent Identity/Trust", "Capturing non-secret identity and trust fingerprint.", "")
		reporter.emit("quiescing_managed_components", "", "running", "Quiescing Managed Components", "Stopping Borealis-managed services before same-build reconciliation.", "")
		if err := quiesceBorealisUpdateComponents(cfg, logger, reporter); err != nil {
			return err
		}
		reporter.emit("quiescing_managed_components", "", "success", "Managed Components Stopped", "Borealis-managed services stopped or reached bounded recovery handling.", "")
		reporter.emit("staging_agent_binary", "", "skipped", "Skipped When Current", "Installed Agent binary already matches target build.", "")
		reporter.emit("reconciling_agent_host", "", "running", "Reconciling Agent Host", "Repairing dependencies, scheduled tasks, services, and runtime configuration.", "")
		if err := reconcileAndStartAgentAfterUpdate(cfg, logger); err != nil {
			reporter.emit("reconciling_agent_host", "", "failed", "Agent Host Reconciliation Failed", err.Error(), "")
			return err
		}
		identityAfter := agentUpdateIdentityFingerprint(configPath)
		if identityBefore == "" || identityBefore != identityAfter {
			return fmt.Errorf("Agent identity/trust verification failed after same-build reconciliation")
		}
		reporter.emit("protecting_agent_identity_trust", "verifying_agent_artifact", "success", "Identity/Trust Preserved", "Non-secret identity and trust fingerprint remained unchanged.", "")
		reporter.emit("reconciling_agent_host", "", "success", "Agent Host Reconciled", "Dependencies, tasks, and services reconciled.", "")
		markConfigUpdateOperation(configPath, "awaiting_health", "")
		reporter.emit("starting_agent_runtime", "", "success", "Borealis Agent Service Started", "Agent service start command completed.", "")
		reporter.emit("waiting_agent_reconnection", "", "running", "Waiting for Agent Reconnection", "Waiting for matching heartbeat and required role health.", "")
		logger.Tracef("Agent operator repair complete: same_build duration=%s", time.Since(startedAt).Round(time.Millisecond))
		return nil
	}
	if !updateOperationIsActive(reporter.operation()) {
		reporter.ensureHourlyOperation(target, installed)
		reporter.emit("requesting_agent_update", "", "success", "Hourly Update Checker", "New Engine artifact detected; durable update operation created.", "")
	}
	reporter.setBuilds(target, installed, "")
	reporter.emit("resolving_engine_artifact", "", "success", "Update Available", "Engine artifact differs from installed Agent build.", "")
	downloadURL := strings.TrimSpace(manifest.DownloadPath)
	if downloadURL == "" {
		return fmt.Errorf("update manifest did not provide Engine artifact path")
	}
	if strings.HasPrefix(downloadURL, "/") {
		downloadURL = serverURL + downloadURL
	}
	archivePath := filepath.Join(updateTempDir(cfg), "agent-update.zip")
	markConfigUpdateOperation(configPath, "staging", "")
	reporter.emit("downloading_agent_artifact", "resolving_engine_artifact", "running", "Downloading Agent Artifact", "Downloading authenticated Engine artifact.", "")
	logger.Tracef("Agent update artifact download start: source=engine url=%s archive=%s", downloadURL, archivePath)
	if err := downloadUpdateArtifact(engineHTTPClient, downloadURL, token, archivePath); err != nil {
		reporter.emit("downloading_agent_artifact", "resolving_engine_artifact", "failed", "Download Failed", err.Error(), "")
		return err
	}
	reporter.emit("downloading_agent_artifact", "resolving_engine_artifact", "success", "Download Complete", "Agent artifact downloaded from Engine.", "")
	reporter.emit("verifying_agent_artifact", "downloading_agent_artifact", "running", "Verifying Agent Artifact", "Validating artifact checksum and configuration compatibility.", "")
	if manifest.ArtifactSHA256 != "" {
		logger.Tracef("Agent update checksum verification start: archive=%s", archivePath)
		actual, err := sha256File(archivePath)
		if err != nil {
			return err
		}
		if !strings.EqualFold(actual, manifest.ArtifactSHA256) {
			return fmt.Errorf("update checksum mismatch expected=%s actual=%s", manifest.ArtifactSHA256, actual)
		}
		logger.Tracef("Agent update checksum verified: sha256=%s", actual)
	}
	extractRoot := filepath.Join(updateTempDir(cfg), "extract")
	_ = os.RemoveAll(extractRoot)
	if err := unzipFileLogged(archivePath, extractRoot, logger); err != nil {
		return err
	}
	sourceRoot := extractRoot
	logger.Tracef("Agent update source resolved: %s", sourceRoot)
	if err := validateAgentUpdateSource(cfg, sourceRoot, logger); err != nil {
		reporter.emit("verifying_agent_artifact", "downloading_agent_artifact", "failed", "Verification Failed", err.Error(), "")
		return err
	}
	reporter.emit("verifying_agent_artifact", "downloading_agent_artifact", "success", "Verification Complete", "Artifact checksum and Agent configuration validation passed.", "")
	identityBefore := agentUpdateIdentityFingerprint(configPath)
	reporter.emit("protecting_agent_identity_trust", "verifying_agent_artifact", "running", "Protecting Agent Identity/Trust", "Capturing non-secret identity and trust fingerprint before replacement.", "")
	reporter.emit("quiescing_managed_components", "", "running", "Quiescing Managed Components", "Stopping Borealis-managed services before replacement.", "")
	if err := quiesceBorealisUpdateComponents(cfg, logger, reporter); err != nil {
		return err
	}
	reporter.emit("quiescing_managed_components", "", "success", "Managed Components Stopped", "Borealis-managed components reached stopped state or scoped recovery handling.", "")
	reporter.emit("staging_agent_binary", "", "running", "Staging Agent Binary", "Replacing installed runtime with verified candidate.", "")
	deferred, err := stageAgentUpdateBinary(cfg, sourceRoot, target, logger)
	if err != nil {
		reporter.emit("staging_agent_binary", "", "failed", "Agent Binary Failed to Stage", err.Error(), "")
		if recoveryErr := startAgentRuntime(cfg, logger); recoveryErr != nil {
			return fmt.Errorf("stage Agent update: %w; restart existing Agent after staging failure: %v", err, recoveryErr)
		}
		return err
	}
	if deferred {
		markConfigUpdateOperation(configPath, "restarting", "")
		reporter.emit("staging_agent_binary", "", "recovering", "Waiting for Agent.exe Handles", "Deferred scoped self-replacement scheduled.", "")
		logger.Infof("Agent update staged (%s); deferred replacement will finalize after Agent.exe exits.", target)
		logger.Tracef("Agent update check complete: deferred duration=%s", time.Since(startedAt).Round(time.Millisecond))
		return nil
	}
	reporter.emit("staging_agent_binary", "", "success", "Agent Binary Staged", "Verified Agent binary replaced atomically.", "")
	reporter.emit("reconciling_agent_host", "", "running", "Reconciling Agent Host", "Repairing dependencies, scheduled tasks, services, and runtime configuration.", "")
	if err := reconcileAndStartAgentAfterUpdate(cfg, logger); err != nil {
		reporter.emit("reconciling_agent_host", "", "failed", "Agent Host Reconciliation Failed", err.Error(), "")
		return err
	}
	writeInstalledBuildID(cfg, target)
	if identityBefore == "" || identityBefore != agentUpdateIdentityFingerprint(configPath) {
		return fmt.Errorf("Agent identity/trust verification failed after binary replacement")
	}
	reporter.setBuilds(target, installed, target)
	reporter.emit("protecting_agent_identity_trust", "verifying_agent_artifact", "success", "Identity/Trust Preserved", "Non-secret identity and trust fingerprint remained unchanged.", "")
	reporter.emit("reconciling_agent_host", "", "success", "Agent Host Reconciled", "Dependencies, tasks, and services reconciled.", "")
	markConfigUpdateOperation(configPath, "awaiting_health", "")
	reporter.emit("starting_agent_runtime", "", "success", "Borealis Agent Service Started", "Agent service start command completed.", "")
	reporter.emit("waiting_agent_reconnection", "", "running", "Waiting for Agent Reconnection", "Waiting for matching heartbeat and required role health.", "")
	logger.Infof("Agent update applied (%s).", target)
	logger.Tracef("Agent update check complete: applied duration=%s", time.Since(startedAt).Round(time.Millisecond))
	return nil
}

func quiesceBorealisUpdateComponents(cfg BootstrapConfig, logger *BootstrapLogger, reporter *updateProgressReporter) error {
	logger.Tracef("Update component quiesce starting before runtime replacement.")
	for _, taskName := range []string{legacyAgentTaskName, agentWatchdogTaskName} {
		stopScheduledTask(taskName, logger)
	}
	services := []struct {
		name  string
		phase string
		label string
	}{
		{agentruntime.WindowsServiceName, "stopping_borealis_agent_service", "Borealis Agent Service"},
		{ultraVNCServiceName, "stopping_ultravnc_service", "UltraVNC Service"},
		{wireGuardManagerServiceName, "stopping_wireguard_service", "WireGuard Manager Service"},
		{"BorealisWireGuardTunnel", "stopping_wireguard_service", "Borealis WireGuard Tunnel"},
		{"WireGuardTunnel$wireguard", "stopping_wireguard_service", "WireGuard Tunnel"},
		{"WireGuardTunnel$Borealis", "stopping_wireguard_service", "Borealis WireGuard Tunnel"},
		{"WireGuardTunnel$borealis-wg", "stopping_wireguard_service", "Borealis WireGuard Tunnel"},
	}
	var stopErrors []error
	for _, service := range services {
		reporter.emit(service.phase, "quiescing_managed_components", "running", "Stopping "+service.label, "Requesting graceful service stop.", "")
		if err := stopBorealisOwnedServiceForUpdate(service.name, cfg, 30*time.Second, logger); err != nil {
			reporter.emit(service.phase, "quiescing_managed_components", "failed", service.label+" Failed to Stop", err.Error(), "")
			stopErrors = append(stopErrors, err)
			continue
		}
		reporter.emit(service.phase, "quiescing_managed_components", "success", service.label+" Stopped", "Service stopped or was not installed.", "")
	}
	reporter.emit("evaluating_rdp_service", "quiescing_managed_components", "success", "RDP Service Healthy - Restart Not Required", "Native Windows TermService is OS-owned and was not restarted during update.", "")
	logger.Tracef("Update component quiesce complete using exact managed task/service ownership.")
	return errors.Join(stopErrors...)
}

func bootstrapEngineHTTPClient(cfg BootstrapConfig) (*http.Client, error) {
	agentCfg, err := agentconfig.Load(agentConfigPath(cfg.InstallDir))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.TrustedEngineCAPEM) != "" {
		agentCfg.Trust.EngineCAPEM = agentconfig.NormalizeEngineCAPEM(cfg.TrustedEngineCAPEM)
	}
	return auth.HTTPClientForConfig(agentCfg, 180*time.Second)
}

func fetchUpdateManifest(httpClient *http.Client, serverURL string, token string, installedBuildID string) (updateManifest, error) {
	params := url.Values{}
	if installedBuildID != "" {
		params.Set("installed_build_id", installedBuildID)
	}
	suffix := ""
	if encoded := params.Encode(); encoded != "" {
		suffix = "?" + encoded
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+"/api/agent/update/manifest"+suffix, nil)
	if err != nil {
		return updateManifest{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return updateManifest{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return updateManifest{}, fmt.Errorf("update manifest HTTP %d", resp.StatusCode)
	}
	var manifest updateManifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return updateManifest{}, err
	}
	return manifest, nil
}

func downloadUpdateArtifact(httpClient *http.Client, rawURL string, token string, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("update artifact HTTP %d", resp.StatusCode)
	}
	return copyReaderToFile(resp.Body, destination)
}

func writeInstalledBuildID(cfg BootstrapConfig, value string) {
	_ = writeConfigInstalledBuildID(cfg, value)
}

func updateTempDir(cfg BootstrapConfig) string {
	return filepath.Join(cfg.InstallDir, "Temp", "Updater")
}

func cleanupAgentTemp(cfg BootstrapConfig, logger *BootstrapLogger) {
	updaterRoot := updateTempDir(cfg)
	if filepath.Base(updaterRoot) == "Updater" {
		if err := removePathWithRetries(updaterRoot, 2, 250*time.Millisecond, nil); err != nil && logger != nil {
			logger.Tracef("Updater workspace cleanup deferred/partial: %v", err)
		}
	} else if logger != nil {
		logger.Tracef("Skipping unexpected updater cleanup path: %s", updaterRoot)
	}
	_ = os.Remove(filepath.Join(cfg.InstallDir, "Updater", "update_status.json"))
	_ = os.Remove(filepath.Join(cfg.InstallDir, "Updater"))

	legacyUpdateRoot := filepath.Join(cfg.InstallDir, "Agent")
	if filepath.Base(legacyUpdateRoot) == "Agent" {
		if err := removePathWithRetries(legacyUpdateRoot, 2, 250*time.Millisecond, nil); err != nil && logger != nil {
			logger.Tracef("Legacy Agent update workspace cleanup deferred/partial: %v", err)
		}
	} else if logger != nil {
		logger.Tracef("Skipping unexpected legacy Agent cleanup path: %s", legacyUpdateRoot)
	}

	tempRoot := filepath.Join(cfg.InstallDir, "Temp")
	if filepath.Base(tempRoot) != "Temp" {
		if logger != nil {
			logger.Tracef("Skipping unexpected Temp cleanup path: %s", tempRoot)
		}
		return
	}
	scheduleAgentTempCleanup(tempRoot, logger)
}

func scheduleAgentTempCleanup(tempRoot string, logger *BootstrapLogger) {
	if filepath.Base(tempRoot) != "Temp" {
		if logger != nil {
			logger.Tracef("Skipping unexpected deferred Temp cleanup path: %s", tempRoot)
		}
		return
	}
	logPath := filepath.Join(filepath.Dir(tempRoot), "Logs", "Agent", "updater.log")
	command := fmt.Sprintf(
		`$ErrorActionPreference='Continue'; $tempRoot=%s; $logPath=%s; New-Item -ItemType Directory -Force -Path (Split-Path -Parent $logPath) | Out-Null; function Write-CleanupLog([string]$m){ Add-Content -LiteralPath $logPath -Value ("[{0}] {1}" -f (Get-Date).ToString("s"), $m) }; Start-Sleep -Seconds 5; for ($attempt=1; $attempt -le 30; $attempt++) { if (!(Test-Path -LiteralPath $tempRoot)) { Write-CleanupLog "Temp cleanup complete: $tempRoot"; exit 0 }; try { Remove-Item -LiteralPath $tempRoot -Recurse -Force -ErrorAction Stop; Write-CleanupLog "Temp cleanup complete: $tempRoot"; exit 0 } catch { Write-CleanupLog ("Temp cleanup attempt $attempt failed: " + $_.Exception.Message); Start-Sleep -Seconds 2 } }; Write-CleanupLog "Temp cleanup failed after all attempts: $tempRoot"; exit 1`,
		powershellSingleQuoted(tempRoot),
		powershellSingleQuoted(logPath),
	)
	cmd := exec.Command(powershellPath(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", command)
	if err := cmd.Start(); err != nil {
		if logger != nil {
			logger.Tracef("Deferred Temp cleanup launch failed: %v", err)
		}
		return
	}
	if err := cmd.Process.Release(); err != nil && logger != nil {
		logger.Tracef("Deferred Temp cleanup release failed: %v", err)
		return
	}
	if logger != nil {
		logger.Tracef("Deferred Temp cleanup scheduled: %s", tempRoot)
	}
}

func powershellSingleQuoted(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
