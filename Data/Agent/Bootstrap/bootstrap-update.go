//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type updateManifest struct {
	TargetBuildID    string `json:"target_build_id"`
	ArtifactSHA256   string `json:"artifact_sha256"`
	FallbackURL      string `json:"fallback_url"`
	DownloadPath     string `json:"download_path"`
	EffectiveChannel string `json:"effective_channel"`
	TargetChannel    string `json:"target_channel"`
}

func runAgentUpdateCheck(cfg BootstrapConfig, logger *BootstrapLogger) error {
	serverURL := strings.TrimRight(strings.TrimSpace(cfg.ServerURL), "/")
	if serverURL == "" {
		return fmt.Errorf("server URL missing")
	}
	token := strings.TrimSpace(readFirstLine(filepath.Join(agentSettingsDir(cfg.InstallDir), "access.jwt")))
	if token == "" {
		return fmt.Errorf("access token missing")
	}
	installed := strings.TrimSpace(readFirstLine(filepath.Join(agentSettingsDir(cfg.InstallDir), "Updater", "installed_build_id.txt")))
	manifest, err := fetchUpdateManifest(serverURL, token, installed)
	if err != nil {
		return err
	}
	target := strings.TrimSpace(strings.ToLower(manifest.TargetBuildID))
	if target == "" {
		return fmt.Errorf("update manifest missing target_build_id")
	}
	if installed != "" && strings.EqualFold(installed, target) {
		logger.Infof("Agent update check: up to date (%s).", target)
		writeUpdateStatus(cfg, map[string]any{
			"state":            "up_to_date",
			"target_build_id":  target,
			"last_checked_at":  time.Now().Unix(),
			"update_available": false,
		})
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
	archivePath := filepath.Join(agentSettingsDir(cfg.InstallDir), "Updater", "agent-update.zip")
	if err := downloadUpdateArtifact(downloadURL, token, authed, archivePath); err != nil {
		return err
	}
	if manifest.ArtifactSHA256 != "" {
		actual, err := sha256File(archivePath)
		if err != nil {
			return err
		}
		if !strings.EqualFold(actual, manifest.ArtifactSHA256) {
			return fmt.Errorf("update checksum mismatch expected=%s actual=%s", manifest.ArtifactSHA256, actual)
		}
	}
	extractRoot := filepath.Join(agentSettingsDir(cfg.InstallDir), "Updater", "extract")
	_ = os.RemoveAll(extractRoot)
	if err := unzipFile(archivePath, extractRoot); err != nil {
		return err
	}
	sourceRoot := resolveSourceRoot(extractRoot)
	stopScheduledTask(agentTaskName, logger)
	stopBorealisProcesses(cfg, logger)
	if err := stageAgentRuntime(cfg, sourceRoot, logger); err != nil {
		return err
	}
	if err := setupPythonEnvironment(cfg, sourceRoot, logger); err != nil {
		return err
	}
	if err := ensureAgentTasks(cfg, logger); err != nil {
		return err
	}
	writeInstalledBuildID(cfg, target)
	writeUpdateStatus(cfg, map[string]any{
		"state":            "applied",
		"target_build_id":  target,
		"last_checked_at":  time.Now().Unix(),
		"update_available": false,
	})
	logger.Infof("Agent update applied (%s).", target)
	return nil
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
	path := filepath.Join(agentSettingsDir(cfg.InstallDir), "Updater", "installed_build_id.txt")
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	_ = os.WriteFile(path, []byte(strings.TrimSpace(strings.ToLower(value))), 0644)
}

func writeUpdateStatus(cfg BootstrapConfig, values map[string]any) {
	path := filepath.Join(agentSettingsDir(cfg.InstallDir), "Updater", "update_status.json")
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	current := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &current)
	}
	for key, value := range values {
		current[key] = value
	}
	data, err := json.MarshalIndent(current, "", "  ")
	if err == nil {
		_ = os.WriteFile(path, data, 0644)
	}
}
