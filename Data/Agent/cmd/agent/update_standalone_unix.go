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
	Branch           string `json:"branch"`
}

var restartLinuxAgentService = func() error {
	return exec.Command("systemctl", "restart", "borealis-agent.service").Run()
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
	if strings.TrimSpace(options.RepoRef) != "" {
		cfg.Agent.Branch = agentconfig.NormalizeBranch(options.RepoRef)
		cfg.Agent.ReleaseChannel = agentconfig.ReleaseChannelForBranch(cfg.Agent.Branch)
	}
	if strings.TrimSpace(options.ReleaseChannel) != "" {
		cfg.Agent.ReleaseChannel = agentconfig.NormalizeReleaseChannel(options.ReleaseChannel)
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
	branch := agentconfig.NormalizeBranch(cfg.Agent.Branch)
	if agentconfig.UsesSourceReleaseChannel(cfg.Agent.ReleaseChannel) {
		return runLinuxRepoRefUpdateCheck(ctx, configPath, &cfg, branch, installed)
	}
	manifest, err := fetchLinuxUpdateManifest(ctx, client, installed)
	if err != nil {
		removeLinuxUpdateStatus(configPath)
		return err
	}
	target := strings.TrimSpace(strings.ToLower(manifest.TargetBuildID))
	if target == "" {
		return fmt.Errorf("update manifest missing target_build_id")
	}
	_ = writeLinuxReleaseTarget(configPath, &cfg, releaseChannelForLinuxUpdateManifest(manifest.EffectiveChannel, manifest.TargetChannel), manifest.Branch)
	if installed != "" && strings.EqualFold(installed, target) {
		removeLinuxUpdateStatus(configPath)
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
	_ = writeLinuxInstalledBuildID(configPath, &cfg, target)
	_ = exec.Command("systemctl", "restart", "borealis-agent.service").Run()
	return nil
}

func releaseChannelForLinuxUpdateManifest(effective string, target string) string {
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

func runLinuxRepoRefUpdateCheck(ctx context.Context, configPath string, cfg *agentconfig.AgentConfig, branch string, installed string) error {
	return runLinuxRepoRefUpdateCheckWithResolver(ctx, configPath, cfg, branch, installed, func(candidate string) (string, error) {
		return resolveGithubRefSHA(ctx, candidate)
	})
}

func runLinuxRepoRefUpdateCheckWithResolver(ctx context.Context, configPath string, cfg *agentconfig.AgentConfig, branch string, installed string, resolve func(string) (string, error)) error {
	branch = agentconfig.NormalizeBranch(branch)
	effectiveBranch, target, fellBack, err := resolveRepoRefUpdateTarget(branch, resolve)
	if err != nil {
		removeLinuxUpdateStatus(configPath)
		return err
	}
	branch = effectiveBranch
	target = strings.TrimSpace(strings.ToLower(target))
	if target == "" {
		return fmt.Errorf("repo_ref %q resolved empty target build id", branch)
	}
	if fellBack {
		_ = writeLinuxReleaseTarget(configPath, cfg, agentconfig.ReleaseChannelSource, branch)
	}
	if installed != "" && strings.EqualFold(installed, target) {
		removeLinuxUpdateStatus(configPath)
		if fellBack {
			return restartLinuxAgentService()
		}
		return nil
	}
	downloadURL := linuxBranchAgentURL(branch)
	binaryPath := filepath.Join(updaterDir(configPath), "Agent.branch")
	if err := downloadRawFile(ctx, downloadURL, binaryPath); err != nil {
		removeLinuxUpdateStatus(configPath)
		return err
	}
	data, err := os.ReadFile(binaryPath)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("repo_ref %q Linux Agent artifact is empty", branch)
	}
	if err := stageLinuxAgentBinary(configPath, data); err != nil {
		return err
	}
	_ = writeLinuxInstalledBuildID(configPath, cfg, target)
	_ = restartLinuxAgentService()
	return nil
}

func resolveGithubRefSHA(ctx context.Context, branch string) (string, error) {
	apiURL := "https://api.github.com/repos/bunny-lab-io/Borealis/commits/" + url.PathEscape(branch)
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
		return "", &githubRefHTTPError{Ref: branch, StatusCode: resp.StatusCode, Body: string(body)}
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

func linuxBranchAgentURL(branch string) string {
	escapedBranch := strings.Trim(strings.ReplaceAll(branch, "\\", "/"), "/")
	rawURL := url.URL{
		Scheme: "https",
		Host:   "raw.githubusercontent.com",
		Path:   "/bunny-lab-io/Borealis/refs/heads/" + escapedBranch + "/Data/Agent/dist/linux-amd64/Agent",
	}
	return rawURL.String()
}

func downloadRawFile(ctx context.Context, rawURL string, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("raw artifact HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
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
	return stageLinuxAgentBinary(configPath, binary)
}

func stageLinuxAgentBinary(configPath string, binary []byte) error {
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

func writeLinuxInstalledBuildID(configPath string, cfg *agentconfig.AgentConfig, value string) error {
	removeLinuxUpdateStatus(configPath)
	buildID := agentconfig.NormalizeBuildID(value)
	if buildID == "" || strings.EqualFold(buildID, "dev") {
		return nil
	}
	if cfg == nil {
		loaded, err := agentconfig.LoadOrCreate(configPath)
		if err != nil {
			return err
		}
		cfg = &loaded
	}
	cfg.Agent.InstalledBuildID = buildID
	if err := agentconfig.Save(configPath, cfg); err != nil {
		return err
	}
	return nil
}

func writeLinuxReleaseTarget(configPath string, cfg *agentconfig.AgentConfig, releaseChannel string, branch string) error {
	if cfg == nil {
		loaded, err := agentconfig.LoadOrCreate(configPath)
		if err != nil {
			return err
		}
		cfg = &loaded
	}
	cfg.Agent.ReleaseChannel = agentconfig.NormalizeReleaseChannel(releaseChannel)
	if strings.TrimSpace(branch) != "" {
		cfg.Agent.Branch = agentconfig.NormalizeBranch(branch)
	}
	return agentconfig.Save(configPath, cfg)
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
