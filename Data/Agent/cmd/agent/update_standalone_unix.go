//go:build !windows

package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	"github.com/bunny-lab-io/borealis/go-agent/internal/auth"
	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
	agentruntime "github.com/bunny-lab-io/borealis/go-agent/internal/runtime"
)

type linuxUpdateManifest struct {
	TargetBuildID    string `json:"target_build_id"`
	ArtifactSHA256   string `json:"artifact_sha256"`
	FallbackURL      string `json:"fallback_url"`
	DownloadPath     string `json:"download_path"`
	EffectiveChannel string `json:"effective_channel"`
	TargetChannel    string `json:"target_channel"`
	ArtifactFormat   string `json:"artifact_format"`
}

func runStandaloneUpdateCheck(options agentruntime.Options) error {
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
		cfg.ServerURL = agentconfig.NormalizeServerURL(options.ServerURL)
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
	installed := strings.TrimSpace(readFirstLine(filepath.Join(updaterDir(configPath), "installed_build_id.txt")))
	if installed == "" {
		installed = strings.TrimSpace(strings.ToLower(options.BuildID))
	}
	manifest, err := fetchLinuxUpdateManifest(ctx, client, installed)
	if err != nil {
		_ = writeLinuxUpdateStatus(configPath, map[string]any{
			"state":           "failed",
			"last_error":      err.Error(),
			"last_checked_at": time.Now().Unix(),
			"last_source":     "linux_updater",
		})
		return err
	}
	target := strings.TrimSpace(strings.ToLower(manifest.TargetBuildID))
	if target == "" {
		return fmt.Errorf("update manifest missing target_build_id")
	}
	if installed != "" && strings.EqualFold(installed, target) {
		return writeLinuxUpdateStatus(configPath, map[string]any{
			"state":              "up_to_date",
			"target_build_id":    target,
			"installed_build_id": installed,
			"effective_channel":  manifest.EffectiveChannel,
			"target_channel":     manifest.TargetChannel,
			"last_checked_at":    time.Now().Unix(),
			"last_source":        "linux_updater",
			"update_available":   false,
		})
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
		downloadURL = strings.TrimRight(client.BaseURL(), "/") + downloadURL
	}
	archivePath := filepath.Join(updaterDir(configPath), "agent-update.zip")
	if err := downloadLinuxUpdateArtifact(ctx, client, downloadURL, authed, archivePath); err != nil {
		return err
	}
	if strings.TrimSpace(manifest.ArtifactSHA256) != "" {
		actual, err := sha256File(archivePath)
		if err != nil {
			return err
		}
		if !strings.EqualFold(actual, manifest.ArtifactSHA256) {
			return fmt.Errorf("update checksum mismatch expected=%s actual=%s", manifest.ArtifactSHA256, actual)
		}
	}
	if err := stageLinuxAgentUpdate(configPath, archivePath); err != nil {
		return err
	}
	_ = writeFirstLine(filepath.Join(updaterDir(configPath), "installed_build_id.txt"), target)
	_ = writeLinuxUpdateStatus(configPath, map[string]any{
		"state":              "applied",
		"target_build_id":    target,
		"installed_build_id": target,
		"effective_channel":  manifest.EffectiveChannel,
		"target_channel":     manifest.TargetChannel,
		"last_checked_at":    time.Now().Unix(),
		"last_source":        "linux_updater",
		"update_available":   false,
	})
	_ = exec.Command("systemctl", "restart", "borealis-agent.service").Run()
	return nil
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
	resp, err := http.DefaultClient.Do(req)
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

func downloadLinuxUpdateArtifact(ctx context.Context, client *auth.Client, rawURL string, authed bool, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	if authed {
		for key, value := range client.AuthHeaders() {
			req.Header.Set(key, value)
		}
	}
	resp, err := http.DefaultClient.Do(req)
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

func stageLinuxAgentUpdate(configPath string, archivePath string) error {
	binary, err := linuxAgentBinaryFromArchive(archivePath)
	if err != nil {
		return err
	}
	destination := filepath.Join(filepath.Dir(configPath), "Agent")
	pending := destination + ".update"
	if err := os.WriteFile(pending, binary, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(pending, 0o700); err != nil {
		_ = os.Remove(pending)
		return err
	}
	return os.Rename(pending, destination)
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

func readFirstLine(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	line := strings.SplitN(string(data), "\n", 2)[0]
	return strings.TrimSpace(line)
}

func writeFirstLine(path string, value string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.TrimSpace(strings.ToLower(value))), 0o644)
}

func writeLinuxUpdateStatus(configPath string, values map[string]any) error {
	path := filepath.Join(updaterDir(configPath), "update_status.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	current := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &current)
	}
	for key, value := range values {
		current[key] = value
	}
	data, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
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
