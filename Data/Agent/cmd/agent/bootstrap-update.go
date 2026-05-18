//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
)

type updateManifest struct {
	TargetBuildID    string `json:"target_build_id"`
	ArtifactSHA256   string `json:"artifact_sha256"`
	FallbackURL      string `json:"fallback_url"`
	DownloadPath     string `json:"download_path"`
	EffectiveChannel string `json:"effective_channel"`
	TargetChannel    string `json:"target_channel"`
	Branch           string `json:"branch"`
}

func runAgentUpdateCheck(cfg BootstrapConfig, logger *BootstrapLogger) error {
	startedAt := time.Now()
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
	installed := readConfigInstalledBuildID(cfg)
	logger.Tracef("Agent update check start: release_channel=%s repo_ref=%s installed_build_id=%s", cfg.ReleaseChannel, cfg.RepoRef, installed)
	if shouldUseRepoRefUpdate(cfg) {
		return runRepoRefUpdateCheck(cfg, logger, installed, startedAt)
	}
	manifest, err := fetchUpdateManifest(serverURL, token, installed)
	if err != nil {
		return err
	}
	target := strings.TrimSpace(strings.ToLower(manifest.TargetBuildID))
	logger.Tracef("Agent update manifest: target_build_id=%s installed_build_id=%s artifact_sha256_present=%t fallback_url_present=%t download_path_present=%t effective_channel=%s target_channel=%s branch=%s", target, installed, strings.TrimSpace(manifest.ArtifactSHA256) != "", strings.TrimSpace(manifest.FallbackURL) != "", strings.TrimSpace(manifest.DownloadPath) != "", manifest.EffectiveChannel, manifest.TargetChannel, manifest.Branch)
	if target == "" {
		return fmt.Errorf("update manifest missing target_build_id")
	}
	writeConfigReleaseTarget(cfg, releaseChannelForUpdateManifest(manifest.EffectiveChannel, manifest.TargetChannel), manifest.Branch)
	if installed != "" && strings.EqualFold(installed, target) {
		logger.Infof("Agent update check: up to date (%s).", target)
		if err := ensureAgentUpdaterTask(cfg, logger); err != nil {
			logger.Warnf("AutoUpdater task reconciliation skipped: %v", err)
		}
		logger.Tracef("Agent update check complete: up_to_date duration=%s", time.Since(startedAt).Round(time.Millisecond))
		return nil
	}
	downloadURL := strings.TrimSpace(manifest.DownloadPath)
	authed := true
	if downloadURL == "" {
		downloadURL = strings.TrimSpace(manifest.FallbackURL)
		authed = false
	}
	if downloadURL == "" {
		return fmt.Errorf("update manifest did not provide artifact URL")
	}
	if strings.HasPrefix(downloadURL, "/") {
		downloadURL = serverURL + downloadURL
	}
	archivePath := filepath.Join(updateTempDir(cfg), "agent-update.zip")
	logger.Tracef("Agent update artifact download start: url=%s authed=%t archive=%s", downloadURL, authed, archivePath)
	if err := downloadUpdateArtifact(downloadURL, token, authed, archivePath); err != nil {
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
	sourceRoot := resolveSourceRoot(extractRoot)
	logger.Tracef("Agent update source resolved: %s", sourceRoot)
	stopScheduledTask(agentTaskName, logger)
	stopBorealisProcesses(cfg, logger)
	deferred, err := stageAgentUpdateBinary(cfg, sourceRoot, target, logger)
	if err != nil {
		return err
	}
	if deferred {
		logger.Infof("Agent update staged (%s); deferred replacement will finalize after Agent.exe exits.", target)
		logger.Tracef("Agent update check complete: deferred duration=%s", time.Since(startedAt).Round(time.Millisecond))
		return nil
	}
	reconcileUltraVNCServiceAfterRuntimeStage(cfg, logger)
	if err := ensureAgentTasks(cfg, logger); err != nil {
		return err
	}
	writeInstalledBuildID(cfg, target)
	logger.Infof("Agent update applied (%s).", target)
	logger.Tracef("Agent update check complete: applied duration=%s", time.Since(startedAt).Round(time.Millisecond))
	return nil
}

func releaseChannelForUpdateManifest(effective string, target string) string {
	channel := strings.ToLower(strings.TrimSpace(effective))
	if channel == "" {
		channel = strings.ToLower(strings.TrimSpace(target))
	}
	switch channel {
	case "unstable", "source", "branch":
		return agentconfig.ReleaseChannelSource
	default:
		return agentconfig.ReleaseChannelStable
	}
}

func shouldUseRepoRefUpdate(cfg BootstrapConfig) bool {
	return agentconfig.UsesSourceReleaseChannel(cfg.ReleaseChannel)
}

func runRepoRefUpdateCheck(cfg BootstrapConfig, logger *BootstrapLogger, installed string, startedAt time.Time) error {
	ref := agentconfig.NormalizeBranch(cfg.RepoRef)
	effectiveRef, target, fellBack, err := resolveRepoRefUpdateTarget(ref, func(candidate string) (string, error) {
		return resolveGithubRefSHA(cfg.RepoURL, candidate)
	})
	if err != nil {
		return fmt.Errorf("resolve repo_ref %q update target; refusing manifest fallback: %w", ref, err)
	}
	ref = effectiveRef
	target = strings.TrimSpace(strings.ToLower(target))
	logger.Tracef("Agent repo_ref update target: repo_ref=%s target_build_id=%s installed_build_id=%s", ref, target, installed)
	if target == "" {
		return fmt.Errorf("repo_ref %q resolved empty target build id", ref)
	}
	if fellBack {
		logger.Warnf("Agent repo_ref %q no longer exists; falling back to repo_ref %q.", cfg.RepoRef, effectiveRef)
		cfg.RepoRef = effectiveRef
		writeConfigReleaseTarget(cfg, agentconfig.ReleaseChannelSource, effectiveRef)
	}
	if installed != "" && strings.EqualFold(installed, target) {
		logger.Infof("Agent repo_ref update check: up to date (%s).", target)
		if fellBack {
			logger.Tracef("Restarting Agent after repo_ref fallback so runtime reloads config.json.")
			stopScheduledTask(agentTaskName, logger)
			if err := ensureAgentTasks(cfg, logger); err != nil {
				return fmt.Errorf("restart Agent after repo_ref fallback: %w", err)
			}
		} else {
			if err := ensureAgentUpdaterTask(cfg, logger); err != nil {
				logger.Warnf("AutoUpdater task reconciliation skipped: %v", err)
			}
		}
		logger.Tracef("Agent update check complete: repo_ref_up_to_date duration=%s", time.Since(startedAt).Round(time.Millisecond))
		return nil
	}
	sourceRoot, err := downloadRepoRefUpdateSource(cfg, logger, ref)
	if err != nil {
		return err
	}
	stopScheduledTask(agentTaskName, logger)
	stopBorealisProcesses(cfg, logger)
	deferred, err := stageAgentUpdateBinary(cfg, sourceRoot, target, logger)
	if err != nil {
		return err
	}
	if deferred {
		logger.Infof("Agent repo_ref update staged (%s @ %s); deferred replacement will finalize after Agent.exe exits.", ref, target)
		logger.Tracef("Agent update check complete: repo_ref_deferred duration=%s", time.Since(startedAt).Round(time.Millisecond))
		return nil
	}
	reconcileUltraVNCServiceAfterRuntimeStage(cfg, logger)
	if err := ensureAgentTasks(cfg, logger); err != nil {
		return err
	}
	writeInstalledBuildID(cfg, target)
	logger.Infof("Agent repo_ref update applied (%s @ %s).", ref, target)
	logger.Tracef("Agent update check complete: repo_ref_applied duration=%s", time.Since(startedAt).Round(time.Millisecond))
	return nil
}

func downloadRepoRefUpdateSource(cfg BootstrapConfig, logger *BootstrapLogger, ref string) (string, error) {
	archiveURL, err := githubArchiveURL(cfg.RepoURL, ref)
	if err != nil {
		return "", err
	}
	archivePath := filepath.Join(updateTempDir(cfg), "repo-ref-update.zip")
	logger.Tracef("Agent repo_ref update archive download start: repo_ref=%s url=%s archive=%s", ref, archiveURL, archivePath)
	if err := downloadFileLogged(context.Background(), archiveURL, archivePath, 180*time.Second, logger); err != nil {
		return "", fmt.Errorf("download repo_ref %q update archive; refusing manifest fallback: %w", ref, err)
	}
	extractRoot := filepath.Join(updateTempDir(cfg), "repo-ref-extract")
	_ = os.RemoveAll(extractRoot)
	if err := unzipFileLogged(archivePath, extractRoot, logger); err != nil {
		return "", err
	}
	sourceRoot := resolveSourceRoot(extractRoot)
	if !sourceRootHasGoAgent(sourceRoot) {
		return "", fmt.Errorf("repo_ref %q update archive missing Go Agent source under Data\\Agent", ref)
	}
	logger.Tracef("Agent repo_ref update source resolved: %s", sourceRoot)
	return sourceRoot, nil
}

func resolveGithubRefSHA(repoURL string, ref string) (string, error) {
	owner, repo, err := githubRepoParts(repoURL)
	if err != nil {
		return "", err
	}
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/commits/%s", owner, repo, url.PathEscape(ref))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", &githubRefHTTPError{Ref: ref, StatusCode: resp.StatusCode, Body: string(body)}
	}
	var payload struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	sha := strings.TrimSpace(payload.SHA)
	if sha == "" {
		return "", fmt.Errorf("GitHub commit API returned empty sha")
	}
	return sha, nil
}

func fetchUpdateManifest(serverURL string, token string, installedBuildID string) (updateManifest, error) {
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
	resp, err := http.DefaultClient.Do(req)
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

func downloadUpdateArtifact(rawURL string, token string, authed bool, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	if authed {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
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
