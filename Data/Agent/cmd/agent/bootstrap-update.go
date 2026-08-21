//go:build windows

package main

import (
	"context"
	"encoding/json"
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

func runAgentUpdateCheck(cfg BootstrapConfig, logger *BootstrapLogger) error {
	startedAt := time.Now()
	configPath := filepath.Join(cfg.InstallDir, agentconfig.FileName)
	markConfigUpdateOperation(configPath, "running", "")
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
	return runEngineManifestUpdateCheck(cfg, logger, installed, startedAt, serverURL, token, engineHTTPClient)
}

func runEngineManifestUpdateCheck(cfg BootstrapConfig, logger *BootstrapLogger, installed string, startedAt time.Time, serverURL string, token string, engineHTTPClient *http.Client) error {
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
		if err := ensureAgentTasks(cfg, logger); err != nil {
			logger.Warnf("Agent service/task reconciliation skipped: %v", err)
		}
		logger.Tracef("Agent update check complete: up_to_date duration=%s", time.Since(startedAt).Round(time.Millisecond))
		return nil
	}
	downloadURL := strings.TrimSpace(manifest.DownloadPath)
	if downloadURL == "" {
		return fmt.Errorf("update manifest did not provide Engine artifact path")
	}
	if strings.HasPrefix(downloadURL, "/") {
		downloadURL = serverURL + downloadURL
	}
	archivePath := filepath.Join(updateTempDir(cfg), "agent-update.zip")
	markConfigUpdateOperation(configPath, "staging", "")
	logger.Tracef("Agent update artifact download start: source=engine url=%s archive=%s", downloadURL, archivePath)
	if err := downloadUpdateArtifact(engineHTTPClient, downloadURL, token, archivePath); err != nil {
		return err
	}
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
		return err
	}
	quiesceBorealisUpdateComponents(cfg, logger)
	deferred, err := stageAgentUpdateBinary(cfg, sourceRoot, target, logger)
	if err != nil {
		if recoveryErr := startAgentRuntime(cfg, logger); recoveryErr != nil {
			return fmt.Errorf("stage Agent update: %w; restart existing Agent after staging failure: %v", err, recoveryErr)
		}
		return err
	}
	if deferred {
		markConfigUpdateOperation(configPath, "restarting", "")
		logger.Infof("Agent update staged (%s); deferred replacement will finalize after Agent.exe exits.", target)
		logger.Tracef("Agent update check complete: deferred duration=%s", time.Since(startedAt).Round(time.Millisecond))
		return nil
	}
	if err := reconcileAndStartAgentAfterUpdate(cfg, logger); err != nil {
		return err
	}
	writeInstalledBuildID(cfg, target)
	markConfigUpdateOperation(configPath, "success", "")
	logger.Infof("Agent update applied (%s).", target)
	logger.Tracef("Agent update check complete: applied duration=%s", time.Since(startedAt).Round(time.Millisecond))
	return nil
}

func quiesceBorealisUpdateComponents(cfg BootstrapConfig, logger *BootstrapLogger) {
	logger.Tracef("Update component quiesce starting before runtime replacement.")
	for _, serviceName := range []string{
		agentruntime.WindowsServiceName,
		ultraVNCServiceName,
		wireGuardManagerServiceName,
		"BorealisWireGuardTunnel",
		"WireGuardTunnel$wireguard",
		"WireGuardTunnel$Borealis",
		"WireGuardTunnel$borealis-wg",
	} {
		stopServiceAndWait(serviceName, 30*time.Second, logger)
	}
	stopBorealisProcesses(cfg, logger)
	logger.Tracef("Update component quiesce complete.")
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
