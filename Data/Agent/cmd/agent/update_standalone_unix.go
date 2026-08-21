//go:build !windows

package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bunny-lab-io/borealis/go-agent/internal/auth"
	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
	agentruntime "github.com/bunny-lab-io/borealis/go-agent/internal/runtime"
)

type linuxUpdateManifest struct {
	TargetBuildID  string `json:"target_build_id"`
	ArtifactSHA256 string `json:"artifact_sha256"`
	DownloadPath   string `json:"download_path"`
	ArtifactFormat string `json:"artifact_format"`
}

var (
	stageLinuxAgentUpdateForRequest   = stageLinuxAgentUpdate
	installLinuxAgentServiceForUpdate = agentruntime.InstallService
)

func runStandaloneUpdateCheck(options agentruntime.Options) (resultErr error) {
	configPath := strings.TrimSpace(options.ConfigPath)
	if configPath == "" {
		resolved, err := agentconfig.PathFromBinary()
		if err != nil {
			return err
		}
		configPath = resolved
	}
	cfg, err := agentconfig.LoadOrCreate(configPath)
	if err != nil {
		return err
	}
	if strings.TrimSpace(options.ServerURL) != "" {
		if err := agentconfig.ValidateServerURLForEnrollment(options.ServerURL); err != nil {
			return err
		}
		cfg.ServerURL = agentconfig.NormalizeServerURL(options.ServerURL)
	}
	if strings.TrimSpace(options.ServerIPFallback) != "" {
		if err := agentconfig.ValidateServerIPFallback(options.ServerIPFallback); err != nil {
			return err
		}
		cfg.ServerIPFallback = agentconfig.NormalizeServerIPFallback(options.ServerIPFallback)
	}
	if strings.TrimSpace(options.TrustedEngineCAB64) != "" {
		pemText, err := agentconfig.DecodeEngineCAB64(options.TrustedEngineCAB64)
		if err != nil {
			return err
		}
		cfg.Trust.EngineCAPEM = pemText
	}
	if strings.TrimSpace(options.TrustedEngineCAPEM) != "" {
		cfg.Trust.EngineCAPEM = agentconfig.NormalizeEngineCAPEM(options.TrustedEngineCAPEM)
	}
	if err := agentconfig.Save(configPath, &cfg); err != nil {
		return err
	}
	client, err := auth.NewClient(configPath, &cfg, "system")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := client.EnsureAuthenticated(ctx); err != nil {
		return err
	}
	installed := agentconfig.NormalizeBuildID(cfg.Agent.InstalledBuildID)
	if installed == "" {
		installed = agentconfig.NormalizeBuildID(options.BuildID)
	}
	reporter := newUpdateProgressReporter(configPath, nil)
	if updateOperationIsActive(reporter.operation()) {
		reporter.emit("requesting_agent_update", "", "success", "Agent Received Request", "Agent persisted update operation before acknowledgement.", "")
		reporter.emit("resolving_engine_artifact", "", "running", "Resolving Engine Artifact", "Requesting current authenticated Agent artifact manifest.", "")
	}
	defer func() {
		if resultErr != nil && updateOperationIsActive(reporter.operation()) {
			reporter.emit("update_completed", "", "failed", "Agent Update Failed", resultErr.Error(), "failed")
		}
	}()
	return runLinuxManifestUpdateCheck(ctx, client, configPath, &cfg, reporter, installed)
}

func runLinuxManifestUpdateCheck(ctx context.Context, client *auth.Client, configPath string, cfg *agentconfig.AgentConfig, reporter *updateProgressReporter, installed string) error {
	manifest, err := fetchLinuxUpdateManifest(ctx, client, installed)
	if err != nil {
		removeLinuxUpdateStatus(configPath)
		return err
	}
	target := strings.TrimSpace(strings.ToLower(manifest.TargetBuildID))
	if target == "" {
		return fmt.Errorf("update manifest missing target_build_id")
	}
	if installed != "" && strings.EqualFold(installed, target) {
		removeLinuxUpdateStatus(configPath)
		operation := reporter.operation()
		if !updateOperationIsActive(operation) || operation.Source == updateSourceHourly {
			return nil
		}
		reporter.setBuilds(target, installed, installed)
		reporter.emit("resolving_engine_artifact", "", "success", "Current Binary Retained", "Installed Agent binary already matches Engine artifact.", "")
		reporter.emit("downloading_agent_artifact", "resolving_engine_artifact", "skipped", "Skipped When Current", "Binary download not required for install-equivalent repair.", "")
		reporter.emit("verifying_agent_artifact", "downloading_agent_artifact", "skipped", "Skipped When Current", "Current installed binary retained.", "")
		identityBefore := agentUpdateIdentityFingerprint(configPath)
		reporter.emit("protecting_agent_identity_trust", "verifying_agent_artifact", "running", "Protecting Agent Identity/Trust", "Capturing non-secret identity and trust fingerprint.", "")
		reporter.emit("quiescing_managed_components", "", "running", "Quiescing Managed Components", "Stopping Borealis-managed systemd services and WireGuard interface.", "")
		if err := quiesceLinuxUpdateComponents(configPath, reporter); err != nil {
			return restoreLinuxRuntimeAfterUpdateFailure(configPath, reporter, err)
		}
		reporter.emit("quiescing_managed_components", "", "success", "Managed Components Stopped", "Borealis-managed Linux services stopped.", "")
		reporter.emit("staging_agent_binary", "", "skipped", "Skipped When Current", "Installed Agent binary already matches target build.", "")
		reporter.emit("reconciling_agent_host", "", "running", "Reconciling Agent Host", "Rewriting managed systemd units, timers, and service state.", "")
		if err := installLinuxAgentServiceForUpdate(filepath.Join(filepath.Dir(configPath), "Agent")); err != nil {
			return restoreLinuxRuntimeAfterUpdateFailure(configPath, reporter, err)
		}
		if identityBefore == "" || identityBefore != agentUpdateIdentityFingerprint(configPath) {
			return fmt.Errorf("Agent identity/trust verification failed after Linux same-build reconciliation")
		}
		reporter.emit("protecting_agent_identity_trust", "verifying_agent_artifact", "success", "Identity/Trust Preserved", "Non-secret identity and trust fingerprint remained unchanged.", "")
		reporter.emit("reconciling_agent_host", "", "success", "Agent Host Reconciled", "Managed systemd units, timers, and service state reconciled.", "")
		markConfigUpdateOperation(configPath, "awaiting_health", "")
		reporter.emit("starting_agent_runtime", "", "success", "Borealis Agent Service Started", "Agent systemd service restart completed.", "")
		reporter.emit("waiting_agent_reconnection", "", "running", "Waiting for Agent Reconnection", "Waiting for matching heartbeat and required role health.", "")
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
		downloadURL = strings.TrimRight(client.BaseURL(), "/") + downloadURL
	}
	archivePath := filepath.Join(updaterDir(configPath), "agent-update.zip")
	reporter.emit("downloading_agent_artifact", "resolving_engine_artifact", "running", "Downloading Agent Artifact", "Downloading authenticated Engine artifact.", "")
	if err := downloadLinuxUpdateArtifact(ctx, client, downloadURL, archivePath); err != nil {
		reporter.emit("downloading_agent_artifact", "resolving_engine_artifact", "failed", "Download Failed", err.Error(), "")
		return err
	}
	reporter.emit("downloading_agent_artifact", "resolving_engine_artifact", "success", "Download Complete", "Agent artifact downloaded from Engine.", "")
	reporter.emit("verifying_agent_artifact", "downloading_agent_artifact", "running", "Verifying Agent Artifact", "Validating artifact checksum and configuration compatibility.", "")
	if strings.TrimSpace(manifest.ArtifactSHA256) != "" {
		actual, err := sha256File(archivePath)
		if err != nil {
			return err
		}
		if !strings.EqualFold(actual, manifest.ArtifactSHA256) {
			return fmt.Errorf("update checksum mismatch expected=%s actual=%s", manifest.ArtifactSHA256, actual)
		}
	}
	identityBefore := agentUpdateIdentityFingerprint(configPath)
	reporter.emit("verifying_agent_artifact", "downloading_agent_artifact", "success", "Verification Complete", "Artifact checksum verification passed.", "")
	reporter.emit("protecting_agent_identity_trust", "verifying_agent_artifact", "running", "Protecting Agent Identity/Trust", "Capturing non-secret identity and trust fingerprint before replacement.", "")
	reporter.emit("quiescing_managed_components", "", "running", "Quiescing Managed Components", "Stopping Borealis-managed systemd services and WireGuard interface.", "")
	if err := quiesceLinuxUpdateComponents(configPath, reporter); err != nil {
		return restoreLinuxRuntimeAfterUpdateFailure(configPath, reporter, err)
	}
	reporter.emit("quiescing_managed_components", "", "success", "Managed Components Stopped", "Borealis-managed Linux services stopped.", "")
	reporter.emit("staging_agent_binary", "", "running", "Staging Agent Binary", "Replacing installed runtime with verified candidate.", "")
	backupPath, err := stageLinuxAgentUpdateWithRecovery(configPath, archivePath, reporter)
	if err != nil {
		return err
	}
	reporter.emit("staging_agent_binary", "", "success", "Agent Binary Staged", "Verified Agent binary replaced atomically.", "")
	reporter.emit("reconciling_agent_host", "", "running", "Reconciling Agent Host", "Rewriting managed systemd units, timers, and service state.", "")
	destination := filepath.Join(filepath.Dir(configPath), "Agent")
	if err := installLinuxAgentServiceForUpdate(destination); err != nil {
		if restoreErr := restoreLinuxAgentUpdate(destination, backupPath); restoreErr != nil {
			return restoreLinuxRuntimeAfterUpdateFailure(
				configPath,
				reporter,
				errors.Join(fmt.Errorf("start updated Linux Agent: %w", err), fmt.Errorf("restore previous runtime: %w", restoreErr)),
			)
		}
		return restoreLinuxRuntimeAfterUpdateFailure(configPath, reporter, fmt.Errorf("start updated Linux Agent: %w; previous runtime restored", err))
	}
	_ = os.Remove(backupPath)
	_ = writeLinuxInstalledBuildID(configPath, cfg, target)
	if identityBefore == "" || identityBefore != agentUpdateIdentityFingerprint(configPath) {
		return fmt.Errorf("Agent identity/trust verification failed after Linux binary replacement")
	}
	reporter.setBuilds(target, installed, target)
	reporter.emit("protecting_agent_identity_trust", "verifying_agent_artifact", "success", "Identity/Trust Preserved", "Non-secret identity and trust fingerprint remained unchanged.", "")
	reporter.emit("reconciling_agent_host", "", "success", "Agent Host Reconciled", "Managed systemd units, timers, and service state reconciled.", "")
	markConfigUpdateOperation(configPath, "awaiting_health", "")
	reporter.emit("starting_agent_runtime", "", "success", "Borealis Agent Service Started", "Agent systemd service restart completed.", "")
	reporter.emit("waiting_agent_reconnection", "", "running", "Waiting for Agent Reconnection", "Waiting for matching heartbeat and required role health.", "")
	return nil
}

func stageLinuxAgentUpdateWithRecovery(configPath string, archivePath string, reporter *updateProgressReporter) (string, error) {
	backupPath, err := stageLinuxAgentUpdateForRequest(configPath, archivePath)
	if err == nil {
		return backupPath, nil
	}
	if reporter != nil {
		reporter.emit("staging_agent_binary", "", "failed", "Agent Binary Failed to Stage", err.Error(), "")
	}
	return "", restoreLinuxRuntimeAfterUpdateFailure(configPath, reporter, err)
}

func restoreLinuxRuntimeAfterUpdateFailure(configPath string, reporter *updateProgressReporter, updateErr error) error {
	destination := filepath.Join(filepath.Dir(configPath), "Agent")
	if reporter != nil {
		reporter.emit("starting_agent_runtime", "", "running", "Restoring Borealis Agent Service", "Restarting previous Agent service and watchdog timers after update failure.", "")
	}
	if err := installLinuxAgentServiceForUpdate(destination); err != nil {
		if reporter != nil {
			reporter.emit("starting_agent_runtime", "", "failed", "Borealis Agent Service Failed to Restore", err.Error(), "")
		}
		return errors.Join(updateErr, fmt.Errorf("restore Linux Agent runtime after update failure: %w", err))
	}
	if reporter != nil {
		reporter.emit("starting_agent_runtime", "", "success", "Borealis Agent Service Restored", "Previous Agent service and watchdog timers restarted after update failure.", "")
	}
	return updateErr
}

func fetchLinuxUpdateManifest(ctx context.Context, client *auth.Client, installedBuildID string) (linuxUpdateManifest, error) {
	params := url.Values{}
	if installedBuildID != "" {
		params.Set("installed_build_id", installedBuildID)
	}
	suffix := ""
	if encoded := params.Encode(); encoded != "" {
		suffix = "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(client.BaseURL(), "/")+"/api/agent/update/manifest"+suffix, nil)
	if err != nil {
		return linuxUpdateManifest{}, err
	}
	for key, value := range client.AuthHeaders() {
		req.Header.Set(key, value)
	}
	resp, err := client.HTTPClient().Do(req)
	if err != nil {
		return linuxUpdateManifest{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return linuxUpdateManifest{}, fmt.Errorf("update manifest HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var manifest linuxUpdateManifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return linuxUpdateManifest{}, err
	}
	return manifest, nil
}

func downloadLinuxUpdateArtifact(ctx context.Context, client *auth.Client, rawURL string, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	for key, value := range client.AuthHeaders() {
		req.Header.Set(key, value)
	}
	resp, err := client.HTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("update artifact HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	temp := destination + ".download"
	out, err := os.OpenFile(temp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, resp.Body)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(temp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(temp)
		return closeErr
	}
	return os.Rename(temp, destination)
}

func stageLinuxAgentUpdate(configPath string, archivePath string) (string, error) {
	binary, err := linuxAgentBinaryFromArchive(archivePath)
	if err != nil {
		return "", err
	}
	return stageLinuxAgentBinary(configPath, binary)
}

func stageLinuxAgentBinary(configPath string, binary []byte) (string, error) {
	destination := filepath.Join(filepath.Dir(configPath), "Agent")
	pending := destination + ".update"
	if err := os.WriteFile(pending, binary, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(pending, 0o700); err != nil {
		_ = os.Remove(pending)
		return "", err
	}
	if err := validateLinuxAgentUpdateCandidate(configPath, pending); err != nil {
		_ = os.Remove(pending)
		return "", err
	}
	backup := destination + ".previous"
	_ = os.Remove(backup)
	if err := copyLinuxAgentFile(destination, backup); err != nil {
		_ = os.Remove(pending)
		return "", err
	}
	if err := os.Rename(pending, destination); err != nil {
		_ = os.Remove(pending)
		_ = os.Remove(backup)
		return "", err
	}
	return backup, nil
}

func quiesceLinuxUpdateComponents(configPath string, reporter *updateProgressReporter) error {
	var stopErrors []error
	for _, unit := range []struct {
		name  string
		phase string
		label string
	}{
		{"borealis-agent-watchdog.timer", "stopping_borealis_agent_service", "Borealis Watchdog Timer"},
		{"borealis-agent-watchdog.service", "stopping_borealis_agent_service", "Borealis Watchdog Service"},
		{"borealis-agent.service", "stopping_borealis_agent_service", "Borealis Agent Service"},
	} {
		reporter.emit(unit.phase, "quiescing_managed_components", "running", "Stopping "+unit.label, "Requesting bounded systemd stop.", "")
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
		output, err := exec.CommandContext(stopCtx, "systemctl", "stop", unit.name).CombinedOutput()
		stopContextErr := stopCtx.Err()
		stopCancel()
		if stopContextErr != nil {
			err = stopContextErr
		}
		if err != nil && !strings.Contains(strings.ToLower(string(output)), "not loaded") {
			stopErr := fmt.Errorf("stop %s: %w: %s", unit.name, err, strings.TrimSpace(string(output)))
			reporter.emit(unit.phase, "quiescing_managed_components", "failed", unit.label+" Failed to Stop", stopErr.Error(), "")
			stopErrors = append(stopErrors, stopErr)
			continue
		}
		reporter.emit(unit.phase, "quiescing_managed_components", "success", unit.label+" Stopped", "Managed systemd unit stopped or was not installed.", "")
	}
	wireguardConfig := filepath.Join(filepath.Dir(configPath), "wireguard.conf")
	reporter.emit("stopping_wireguard_service", "quiescing_managed_components", "running", "Stopping WireGuard Service", "Stopping Borealis-managed WireGuard interface.", "")
	wireGuardCtx, wireGuardCancel := context.WithTimeout(context.Background(), 30*time.Second)
	output, err := exec.CommandContext(wireGuardCtx, "wg-quick", "down", wireguardConfig).CombinedOutput()
	wireGuardContextErr := wireGuardCtx.Err()
	wireGuardCancel()
	if wireGuardContextErr != nil {
		err = wireGuardContextErr
	}
	if err != nil {
		text := strings.ToLower(string(output))
		if !strings.Contains(text, "not a wireguard interface") && !strings.Contains(text, "does not exist") && !errors.Is(err, exec.ErrNotFound) {
			stopErr := fmt.Errorf("stop WireGuard interface: %w: %s", err, strings.TrimSpace(string(output)))
			reporter.emit("stopping_wireguard_service", "quiescing_managed_components", "failed", "WireGuard Service Failed to Stop", stopErr.Error(), "")
			stopErrors = append(stopErrors, stopErr)
		}
	}
	reporter.emit("stopping_wireguard_service", "quiescing_managed_components", "success", "WireGuard Service Stopped", "WireGuard interface stopped or was not active.", "")
	return errors.Join(stopErrors...)
}

func copyLinuxAgentFile(source string, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(destination)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(destination)
		return closeErr
	}
	return os.Chmod(destination, 0o700)
}

func restoreLinuxAgentUpdate(destination string, backup string) error {
	if strings.TrimSpace(backup) == "" {
		return fmt.Errorf("previous Agent runtime backup missing")
	}
	if err := os.Rename(backup, destination); err != nil {
		return err
	}
	return os.Chmod(destination, 0o700)
}

func validateLinuxAgentUpdateCandidate(configPath string, candidate string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, candidate, "--validate-config", "--config-path", configPath)
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("linux Agent candidate config validation timed out")
	}
	if err != nil {
		return fmt.Errorf("linux Agent candidate rejected current agent.json: %w output=%s", err, text)
	}
	return nil
}

func linuxAgentBinaryFromArchive(archivePath string) ([]byte, error) {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, err
	}
	defer archive.Close()
	for _, file := range archive.File {
		name := strings.Trim(strings.ReplaceAll(file.Name, "\\", "/"), "/")
		if name == "Data/Agent/dist/linux-amd64/Agent" || strings.HasSuffix(name, "/Data/Agent/dist/linux-amd64/Agent") {
			handle, err := file.Open()
			if err != nil {
				return nil, err
			}
			data, readErr := io.ReadAll(handle)
			closeErr := handle.Close()
			if readErr != nil {
				return nil, readErr
			}
			if closeErr != nil {
				return nil, closeErr
			}
			if len(data) == 0 {
				return nil, fmt.Errorf("linux Agent binary in update artifact is empty")
			}
			return data, nil
		}
	}
	return nil, fmt.Errorf("update artifact missing Data/Agent/dist/linux-amd64/Agent")
}

func updaterDir(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "Updater")
}

func writeLinuxInstalledBuildID(configPath string, cfg *agentconfig.AgentConfig, value string) error {
	removeLinuxUpdateStatus(configPath)
	buildID := agentconfig.NormalizeBuildID(value)
	if buildID == "" || strings.EqualFold(buildID, "dev") {
		return nil
	}
	loaded, err := agentconfig.LoadOrCreate(configPath)
	if err != nil {
		return err
	}
	cfg = &loaded
	cfg.Agent.InstalledBuildID = buildID
	if err := agentconfig.Save(configPath, cfg); err != nil {
		return err
	}
	return nil
}

func removeLinuxUpdateStatus(configPath string) {
	_ = os.Remove(filepath.Join(updaterDir(configPath), "update_status.json"))
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
