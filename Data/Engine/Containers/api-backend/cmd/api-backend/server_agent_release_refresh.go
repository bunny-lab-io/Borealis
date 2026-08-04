package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	agentReleaseGitHubDefaultBaseURL = "https://api.github.com"
	agentReleaseRefreshTimeout       = 10 * time.Minute
)

var (
	agentReleaseArtifactIDPattern  = regexp.MustCompile(`[^a-z0-9._-]+`)
	agentReleaseRefreshMu          sync.Mutex
	agentReleasePersistMu          sync.Mutex
	agentReleasePersistError       string
	agentReleaseHTTPClient         = &http.Client{Timeout: 120 * time.Second}
	agentReleaseBuildAgentBinaries = buildEngineAgentBinaries
)

func refreshAgentReleaseChannels(ctx context.Context, force bool) map[string]any {
	agentReleaseRefreshMu.Lock()
	defer agentReleaseRefreshMu.Unlock()

	snapshot := collectAgentReleaseChannelSettings()
	now := time.Now().Unix()
	github, _ := snapshot["github"].(map[string]any)
	if github == nil {
		github = map[string]any{}
	}
	repo := normalizeAgentReleaseRepo(github["repo"], defaultAgentReleaseRepo)
	github["repo"] = repo
	snapshot["github"] = github
	source := normalizeAgentReleaseBinarySource(snapshot["binary_source"], agentReleaseSourceGitHub)
	snapshot["binary_source"] = source
	fallbackEnabled := boolFromAny(snapshot["github_fallback_enabled"])
	snapshot["github_fallback_enabled"] = fallbackEnabled
	snapshot["last_refresh_started_at"] = now
	snapshot["last_refresh_error"] = ""

	priorChannels, _ := snapshot["channels"].(map[string]any)
	priorChannels = deepCopyMap(priorChannels)

	if source == agentReleaseSourceEngine {
		enginePayload, engineOK := refreshAgentReleaseChannelsFromEngine(ctx, snapshot, repo, priorChannels, force)
		if engineOK || !fallbackEnabled {
			return persistAgentReleaseRefreshPayload(enginePayload)
		}
		fallbackReason := cleanText(enginePayload["last_refresh_error"])
		log.Printf("Agent release Engine build failed; GitHub fallback enabled: %s", firstText(fallbackReason, "unknown error"))
		snapshot = enginePayload
		snapshot["last_refresh_started_at"] = now
		return refreshAgentReleaseChannelsFromGitHub(ctx, snapshot, repo, priorChannels, force, fallbackReason)
	}
	return refreshAgentReleaseChannelsFromGitHub(ctx, snapshot, repo, priorChannels, force, "")
}

func refreshAgentReleaseChannelsFromGitHub(ctx context.Context, snapshot map[string]any, repo string, priorChannels map[string]any, force bool, fallbackReason string) map[string]any {
	github, _ := snapshot["github"].(map[string]any)
	if github == nil {
		github = map[string]any{}
	}
	repo = normalizeAgentReleaseRepo(repo, defaultAgentReleaseRepo)
	github["repo"] = repo
	snapshot["github"] = github

	repoPayload, err := agentReleaseGitHubJSON(ctx, "/repos/"+strings.Trim(repo, "/"))
	if err != nil {
		snapshot["last_refresh_error"] = err.Error()
		snapshot["last_refresh_completed_at"] = time.Now().Unix()
		return persistAgentReleaseRefreshPayload(snapshot)
	}
	defaultBranch := firstText(cleanText(repoPayload["default_branch"]), cleanText(github["default_branch"]), defaultAgentReleaseBranch)
	github["default_branch"] = defaultBranch
	snapshot["github"] = github

	stableCandidate, stableErr := stableAgentReleaseCandidate(ctx, repo)
	unstableCandidate, unstableErr := unstableAgentReleaseCandidate(ctx, repo, defaultBranch)
	refreshErrors := make([]string, 0, 2)

	channels, _ := snapshot["channels"].(map[string]any)
	if channels == nil {
		channels = map[string]any{}
	}
	if stableErr != nil {
		refreshErrors = append(refreshErrors, "stable: "+stableErr.Error())
		channels["stable"] = agentReleaseChannelErrorTarget(repo, map[string]any{
			"channel": "stable",
			"source":  agentReleaseSourceGitHub,
		}, mapValue(priorChannels, "stable"), stableErr)
	} else if target, err := ensureCachedAgentReleaseArtifact(ctx, repo, stableCandidate, force); err != nil {
		refreshErrors = append(refreshErrors, "stable: "+err.Error())
		channels["stable"] = agentReleaseChannelErrorTarget(repo, stableCandidate, mapValue(priorChannels, "stable"), err)
	} else {
		if fallbackReason != "" {
			target["source"] = "github_fallback"
			target["fallback_reason"] = fallbackReason
		}
		channels["stable"] = target
	}

	if unstableErr != nil {
		refreshErrors = append(refreshErrors, "unstable: "+unstableErr.Error())
		channels["unstable"] = agentReleaseChannelErrorTarget(repo, map[string]any{
			"channel":       "unstable",
			"source":        agentReleaseSourceGitHub,
			"version_label": defaultBranch,
			"branch":        defaultBranch,
		}, mapValue(priorChannels, "unstable"), unstableErr)
	} else if target, err := ensureCachedAgentReleaseArtifact(ctx, repo, unstableCandidate, force); err != nil {
		refreshErrors = append(refreshErrors, "unstable: "+err.Error())
		channels["unstable"] = agentReleaseChannelErrorTarget(repo, unstableCandidate, mapValue(priorChannels, "unstable"), err)
	} else {
		if fallbackReason != "" {
			target["source"] = "github_fallback"
			target["fallback_reason"] = fallbackReason
		}
		channels["unstable"] = target
	}

	snapshot["channels"] = channels
	snapshot["last_refresh_completed_at"] = time.Now().Unix()
	snapshot["last_refresh_error"] = strings.Join(refreshErrors, "; ")
	if fallbackReason != "" {
		fallbackMessage := "Engine source failed; GitHub fallback used: " + fallbackReason
		if cleanText(snapshot["last_refresh_error"]) != "" {
			snapshot["last_refresh_error"] = fallbackMessage + "; GitHub refresh warnings: " + cleanText(snapshot["last_refresh_error"])
		} else {
			snapshot["last_refresh_error"] = fallbackMessage
		}
	}
	return persistAgentReleaseRefreshPayload(snapshot)
}

func refreshAgentReleaseChannelsFromEngine(ctx context.Context, snapshot map[string]any, repo string, priorChannels map[string]any, force bool) (map[string]any, bool) {
	channels, _ := snapshot["channels"].(map[string]any)
	if channels == nil {
		channels = map[string]any{}
	}
	refreshErrors := make([]string, 0, 2)
	allOK := true
	for _, channel := range []string{"stable", "unstable"} {
		candidate, err := engineAgentReleaseCandidate(channel, repo)
		if err == nil {
			var target map[string]any
			target, err = ensureEngineCompiledAgentReleaseArtifact(ctx, repo, candidate, force)
			if err == nil {
				channels[channel] = target
				continue
			}
		}
		allOK = false
		refreshErrors = append(refreshErrors, channel+": "+err.Error())
		channels[channel] = agentReleaseChannelErrorTarget(repo, map[string]any{
			"channel":       channel,
			"source":        agentReleaseSourceEngine,
			"version_label": engineAgentReleaseVersionLabel(channel),
			"branch":        engineAgentReleaseBranch(channel),
		}, mapValue(priorChannels, channel), err)
	}
	snapshot["channels"] = channels
	snapshot["last_refresh_completed_at"] = time.Now().Unix()
	snapshot["last_refresh_error"] = strings.Join(refreshErrors, "; ")
	return snapshot, allOK
}

func persistAgentReleaseRefreshPayload(snapshot map[string]any) map[string]any {
	if err := saveAgentReleaseChannelSettings(snapshot); err != nil {
		snapshot["last_persist_error"] = err.Error()
		return snapshot
	}
	return collectAgentReleaseChannelSettings()
}

func stableAgentReleaseCandidate(ctx context.Context, repo string) (map[string]any, error) {
	payload, err := agentReleaseGitHubJSON(ctx, "/repos/"+strings.Trim(repo, "/")+"/releases/latest")
	if err != nil {
		return nil, err
	}
	tagName := cleanText(payload["tag_name"])
	if tagName == "" {
		return nil, fmt.Errorf("GitHub latest release payload missing tag_name")
	}
	commit, err := agentReleaseCommitMetadata(ctx, repo, tagName)
	if err != nil {
		return nil, err
	}
	downloadURL := firstText(cleanText(payload["zipball_url"]), agentReleaseGitHubURL("/repos/"+strings.Trim(repo, "/")+"/zipball/"+url.PathEscape(tagName)))
	return map[string]any{
		"channel":       "stable",
		"source":        agentReleaseSourceGitHub,
		"build_id":      cleanText(commit["sha"]),
		"download_url":  downloadURL,
		"fallback_url":  "https://github.com/" + strings.Trim(repo, "/") + "/archive/refs/tags/" + tagName + ".zip",
		"version_label": tagName,
		"release_tag":   tagName,
		"release_name":  firstText(cleanText(payload["name"]), tagName),
		"published_at":  cleanText(payload["published_at"]),
		"branch":        defaultAgentReleaseBranch,
	}, nil
}

func unstableAgentReleaseCandidate(ctx context.Context, repo string, defaultBranch string) (map[string]any, error) {
	branch := firstText(cleanText(defaultBranch), defaultAgentReleaseBranch)
	commit, err := agentReleaseCommitMetadata(ctx, repo, branch)
	if err != nil {
		return nil, err
	}
	buildID := strings.ToLower(cleanText(commit["sha"]))
	return map[string]any{
		"channel":       "unstable",
		"source":        agentReleaseSourceGitHub,
		"build_id":      buildID,
		"download_url":  agentReleaseGitHubURL("/repos/" + strings.Trim(repo, "/") + "/zipball/" + url.PathEscape(buildID)),
		"fallback_url":  "https://github.com/" + strings.Trim(repo, "/") + "/archive/" + buildID + ".zip",
		"version_label": branch,
		"branch":        branch,
		"published_at":  cleanText(commit["published_at"]),
	}, nil
}

func agentReleaseCommitMetadata(ctx context.Context, repo string, refName string) (map[string]any, error) {
	payload, err := agentReleaseGitHubJSON(ctx, "/repos/"+strings.Trim(repo, "/")+"/commits/"+url.PathEscape(refName))
	if err != nil {
		return nil, err
	}
	sha := strings.ToLower(cleanText(payload["sha"]))
	if sha == "" {
		return nil, fmt.Errorf("GitHub commit lookup missing sha for ref=%s", refName)
	}
	commitPayload, _ := payload["commit"].(map[string]any)
	authorPayload, _ := commitPayload["author"].(map[string]any)
	committerPayload, _ := commitPayload["committer"].(map[string]any)
	return map[string]any{
		"sha":          sha,
		"published_at": firstText(cleanText(authorPayload["date"]), cleanText(committerPayload["date"])),
	}, nil
}

func ensureCachedAgentReleaseArtifact(ctx context.Context, repo string, candidate map[string]any, force bool) (map[string]any, error) {
	channel := normalizeAgentReleaseChannel(candidate["channel"], defaultAgentReleaseChannel)
	buildID := strings.ToLower(cleanText(candidate["build_id"]))
	if buildID == "" {
		return nil, fmt.Errorf("%s target missing build id", channel)
	}
	artifactID := agentReleaseArtifactID(channel, buildID)
	artifactPath := agentUpdateArtifactPath(artifactID)
	if artifactPath == "" {
		return nil, fmt.Errorf("%s target artifact path invalid", channel)
	}

	var artifactSHA string
	var artifactSize int64
	if info, err := os.Stat(artifactPath); err == nil && !info.IsDir() && !force {
		if err := validateAgentReleaseArtifact(artifactPath); err != nil {
			return nil, err
		}
		artifactSHA = sha256File(artifactPath)
		artifactSize = info.Size()
	} else {
		body, err := agentReleaseDownload(ctx, cleanText(candidate["download_url"]))
		if err != nil {
			return nil, err
		}
		if err := packageAgentReleaseArtifact(body, artifactPath, map[string]any{
			"channel":       channel,
			"repo":          repo,
			"build_id":      buildID,
			"download_url":  cleanText(candidate["download_url"]),
			"fallback_url":  cleanText(candidate["fallback_url"]),
			"version_label": cleanText(candidate["version_label"]),
			"release_tag":   cleanText(candidate["release_tag"]),
			"release_name":  cleanText(candidate["release_name"]),
			"published_at":  cleanText(candidate["published_at"]),
			"branch":        cleanText(candidate["branch"]),
		}); err != nil {
			return nil, err
		}
		info, err := os.Stat(artifactPath)
		if err != nil {
			return nil, err
		}
		artifactSHA = sha256File(artifactPath)
		artifactSize = info.Size()
	}

	manifest := map[string]any{
		"channel":            channel,
		"repo":               repo,
		"source":             firstText(cleanText(candidate["source"]), agentReleaseSourceGitHub),
		"artifact_format":    agentUpdateArtifactFormat,
		"platform_artifacts": deepCopyMap(requiredGoAgentArtifacts),
		"build_id":           buildID,
		"artifact_id":        artifactID,
		"artifact_path":      artifactPath,
		"artifact_sha256":    artifactSHA,
		"artifact_size":      artifactSize,
		"download_url":       cleanText(candidate["download_url"]),
		"fallback_url":       cleanText(candidate["fallback_url"]),
		"version_label":      cleanText(candidate["version_label"]),
		"release_tag":        cleanText(candidate["release_tag"]),
		"release_name":       cleanText(candidate["release_name"]),
		"published_at":       cleanText(candidate["published_at"]),
		"branch":             cleanText(candidate["branch"]),
		"promoted_at":        time.Now().Unix(),
		"refreshed_at":       time.Now().Unix(),
		"last_error":         "",
	}
	if err := writeAgentReleaseJSON(filepath.Join(agentUpdateCacheRoot(), artifactID+".json"), manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

func engineAgentReleaseCandidate(channel string, repo string) (map[string]any, error) {
	normalizedChannel := normalizeAgentReleaseChannel(channel, defaultAgentReleaseChannel)
	buildID, err := engineAgentReleaseBuildID()
	if err != nil {
		return nil, err
	}
	branch := engineAgentReleaseBranch(normalizedChannel)
	versionLabel := engineAgentReleaseVersionLabel(normalizedChannel)
	return map[string]any{
		"channel":       normalizedChannel,
		"source":        agentReleaseSourceEngine,
		"repo":          repo,
		"build_id":      buildID,
		"version_label": versionLabel,
		"branch":        branch,
		"published_at":  time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func ensureEngineCompiledAgentReleaseArtifact(ctx context.Context, repo string, candidate map[string]any, force bool) (map[string]any, error) {
	channel := normalizeAgentReleaseChannel(candidate["channel"], defaultAgentReleaseChannel)
	buildID := strings.ToLower(cleanText(candidate["build_id"]))
	if buildID == "" {
		return nil, fmt.Errorf("%s Engine target missing build id", channel)
	}
	artifactID := agentReleaseArtifactID("engine-"+channel, buildID)
	artifactPath := agentUpdateArtifactPath(artifactID)
	if artifactPath == "" {
		return nil, fmt.Errorf("%s Engine target artifact path invalid", channel)
	}

	var artifactSHA string
	var artifactSize int64
	var compiledAt int64
	if info, err := os.Stat(artifactPath); err == nil && !info.IsDir() && !force {
		if err := validateAgentReleaseArtifact(artifactPath); err != nil {
			return nil, err
		}
		artifactSHA = sha256File(artifactPath)
		artifactSize = info.Size()
		compiledAt = firstPositiveInt64(coerceInt64(candidate["compiled_at"]), agentReleaseArtifactCompiledAt(artifactPath, info))
	} else {
		outputRoot := filepath.Join(agentUpdateCacheRoot(), "Builds", artifactID, "dist")
		if err := agentReleaseBuildAgentBinaries(ctx, buildID, outputRoot); err != nil {
			return nil, err
		}
		compiledAt = time.Now().Unix()
		if err := packageCompiledAgentReleaseArtifact(outputRoot, artifactPath, map[string]any{
			"channel":       channel,
			"repo":          repo,
			"source":        agentReleaseSourceEngine,
			"build_id":      buildID,
			"version_label": cleanText(candidate["version_label"]),
			"branch":        cleanText(candidate["branch"]),
			"published_at":  cleanText(candidate["published_at"]),
			"compiled_at":   compiledAt,
		}); err != nil {
			return nil, err
		}
		info, err := os.Stat(artifactPath)
		if err != nil {
			return nil, err
		}
		artifactSHA = sha256File(artifactPath)
		artifactSize = info.Size()
	}

	manifest := map[string]any{
		"channel":            channel,
		"repo":               repo,
		"source":             agentReleaseSourceEngine,
		"artifact_format":    agentUpdateArtifactFormat,
		"platform_artifacts": deepCopyMap(requiredGoAgentArtifacts),
		"build_id":           buildID,
		"artifact_id":        artifactID,
		"artifact_path":      artifactPath,
		"artifact_sha256":    artifactSHA,
		"artifact_size":      artifactSize,
		"download_url":       "",
		"fallback_url":       "",
		"version_label":      cleanText(candidate["version_label"]),
		"release_tag":        "",
		"release_name":       "Engine-Compiled Agent",
		"published_at":       cleanText(candidate["published_at"]),
		"branch":             cleanText(candidate["branch"]),
		"compiled_at":        compiledAt,
		"promoted_at":        time.Now().Unix(),
		"refreshed_at":       time.Now().Unix(),
		"last_error":         "",
	}
	if err := writeAgentReleaseJSON(filepath.Join(agentUpdateCacheRoot(), artifactID+".json"), manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

func buildEngineAgentBinaries(ctx context.Context, buildID string, outputRoot string) error {
	sourceAgentDir := filepath.Join(agentReleaseProjectRoot(), "Data", "Agent")
	if info, err := os.Stat(filepath.Join(sourceAgentDir, "build-agent.sh")); err != nil || info.IsDir() {
		return fmt.Errorf("Agent build script unavailable under %s", sourceAgentDir)
	}
	artifactWorkRoot := filepath.Join(agentUpdateCacheRoot(), "BuildSource", "engine-"+agentReleaseArtifactID("source", buildID))
	stagedAgentDir := filepath.Join(artifactWorkRoot, "Data", "Agent")
	if err := os.RemoveAll(artifactWorkRoot); err != nil {
		return err
	}
	if err := copyAgentSourceTree(sourceAgentDir, stagedAgentDir); err != nil {
		return err
	}
	if err := os.RemoveAll(outputRoot); err != nil {
		return err
	}
	if err := os.MkdirAll(outputRoot, 0o755); err != nil {
		return err
	}
	buildLogPath := filepath.Join(filepath.Dir(outputRoot), "build.log")
	cmd := exec.CommandContext(ctx, filepath.Join(stagedAgentDir, "build-agent.sh"))
	cmd.Dir = stagedAgentDir
	goInstallRoot := filepath.Join(agentUpdateCacheRoot(), "Go", "go1.22.12")
	cmd.Env = append(os.Environ(),
		"BOREALIS_AGENT_VERSION="+buildID,
		"BOREALIS_GO_AGENT_OUTPUT_ROOT="+outputRoot,
		"BOREALIS_GO_INSTALL_ROOT="+goInstallRoot,
	)
	output, err := cmd.CombinedOutput()
	_ = os.MkdirAll(filepath.Dir(buildLogPath), 0o755)
	_ = os.WriteFile(buildLogPath, output, 0o600)
	_ = os.RemoveAll(artifactWorkRoot)
	if err != nil {
		return fmt.Errorf("Engine Agent build failed: %w: %s", err, truncateLogSnippet(string(output), 4096))
	}
	return nil
}

func packageCompiledAgentReleaseArtifact(outputRoot string, destinationPath string, manifest map[string]any) error {
	binaries := map[string][]byte{}
	missing := make([]string, 0)
	for platform, rawPath := range requiredGoAgentArtifacts {
		outputPath := filepath.Join(outputRoot, strings.TrimPrefix(cleanText(rawPath), "Data/Agent/dist/"))
		body, err := os.ReadFile(outputPath)
		if err != nil || len(body) == 0 {
			missing = append(missing, outputPath)
			continue
		}
		binaries[platform] = body
	}
	if len(missing) > 0 {
		return fmt.Errorf("compiled Agent artifact missing binaries: %s", strings.Join(missing, ", "))
	}
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		return err
	}
	tempPath := destinationPath + ".tmp"
	payload := deepCopyMap(manifest)
	payload["artifact_format"] = agentUpdateArtifactFormat
	payload["platform_artifacts"] = deepCopyMap(requiredGoAgentArtifacts)

	var bundle bytes.Buffer
	writer := zip.NewWriter(&bundle)
	manifestBody, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		_ = writer.Close()
		return err
	}
	if err := writeZipEntry(writer, "manifest.json", append(manifestBody, '\n')); err != nil {
		_ = writer.Close()
		return err
	}
	for platform, rawPath := range requiredGoAgentArtifacts {
		if err := writeZipEntry(writer, cleanText(rawPath), binaries[platform]); err != nil {
			_ = writer.Close()
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return err
	}
	if err := os.WriteFile(tempPath, bundle.Bytes(), 0o600); err != nil {
		return err
	}
	if err := validateAgentReleaseArtifact(tempPath); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return os.Rename(tempPath, destinationPath)
}

func copyAgentSourceTree(sourceDir string, destinationDir string) error {
	return filepath.WalkDir(sourceDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return os.MkdirAll(destinationDir, 0o755)
		}
		normalizedRelative := strings.Trim(filepath.ToSlash(relative), "/")
		if normalizedRelative == "dist" || strings.HasPrefix(normalizedRelative, "dist/") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if normalizedRelative == "cmd/agent/agent_windows.syso" {
			return nil
		}
		targetPath := filepath.Join(destinationDir, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(targetPath, info.Mode().Perm())
		}
		return copyFile(path, targetPath, info.Mode().Perm())
	})
}

func copyFile(sourcePath string, destinationPath string, mode fs.FileMode) error {
	input, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		return err
	}
	tempPath := destinationPath + ".tmp"
	output, err := os.OpenFile(tempPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		_ = os.Remove(tempPath)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tempPath)
		return closeErr
	}
	return os.Rename(tempPath, destinationPath)
}

func engineAgentReleaseBuildID() (string, error) {
	if digest, err := sourceTreeDigest(filepath.Join(agentReleaseProjectRoot(), "Data", "Agent")); err == nil {
		text := strings.ToLower(cleanText(digest))
		if text != "" {
			return text, nil
		}
	}
	for _, value := range []string{
		os.Getenv("BOREALIS_ENGINE_SOURCE_BUILD_ID"),
		version,
		gitCommandOutput("rev-parse", "HEAD"),
	} {
		text := strings.ToLower(cleanText(value))
		if text != "" && text != "dev" {
			return text, nil
		}
	}
	return "", fmt.Errorf("Agent source build id unavailable")
}

func engineAgentReleaseBranch(channel string) string {
	for _, value := range []string{
		os.Getenv("BOREALIS_ENGINE_SOURCE_BRANCH"),
		gitCommandOutput("branch", "--show-current"),
	} {
		text := cleanText(value)
		if text != "" {
			return text
		}
	}
	if normalizeAgentReleaseChannel(channel, defaultAgentReleaseChannel) == "stable" {
		return defaultAgentReleaseBranch
	}
	return defaultAgentReleaseBranch
}

func engineAgentReleaseVersionLabel(channel string) string {
	branch := engineAgentReleaseBranch(channel)
	if branch == "" {
		branch = defaultAgentReleaseBranch
	}
	return "engine:" + branch
}

func gitCommandOutput(args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", agentReleaseProjectRoot()}, args...)...)
	body, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(body))
}

func sourceTreeDigest(root string) (string, error) {
	hash := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		normalizedRelative := strings.Trim(filepath.ToSlash(relative), "/")
		if normalizedRelative == "" || normalizedRelative == "." {
			return nil
		}
		if normalizedRelative == "dist" || strings.HasPrefix(normalizedRelative, "dist/") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if normalizedRelative == "cmd/agent/agent_windows.syso" {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, _ = hash.Write([]byte(normalizedRelative))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(body)
		_, _ = hash.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func agentReleaseProjectRoot() string {
	root := strings.TrimSpace(os.Getenv("BOREALIS_PROJECT_ROOT"))
	if root == "" {
		root = "/opt/Borealis"
	}
	return expandHomePath(root)
}

func truncateLogSnippet(value string, limit int) string {
	text := strings.TrimSpace(value)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return text[:limit] + "..."
}

func agentReleaseChannelErrorTarget(repo string, candidate map[string]any, priorTarget map[string]any, failure error) map[string]any {
	var base map[string]any
	if prior, err := validatedPriorAgentReleaseTarget(priorTarget); err == nil && prior != nil {
		base = prior
	} else {
		base = map[string]any{
			"channel":         normalizeAgentReleaseChannel(candidate["channel"], defaultAgentReleaseChannel),
			"repo":            repo,
			"build_id":        "",
			"artifact_id":     "",
			"artifact_path":   "",
			"artifact_sha256": "",
			"artifact_size":   int64(0),
			"promoted_at":     int64(0),
		}
	}
	base["channel"] = normalizeAgentReleaseChannel(candidate["channel"], defaultAgentReleaseChannel)
	base["repo"] = repo
	base["source"] = firstText(cleanText(candidate["source"]), cleanText(base["source"]))
	base["download_url"] = cleanText(candidate["download_url"])
	base["fallback_url"] = cleanText(candidate["fallback_url"])
	base["version_label"] = cleanText(candidate["version_label"])
	base["release_tag"] = cleanText(candidate["release_tag"])
	base["release_name"] = cleanText(candidate["release_name"])
	base["published_at"] = cleanText(candidate["published_at"])
	base["branch"] = firstText(cleanText(candidate["branch"]), stableAgentReleaseBranch(base["channel"]))
	base["refreshed_at"] = time.Now().Unix()
	base["last_error"] = failure.Error()
	return base
}

func stableAgentReleaseBranch(channel any) string {
	if normalizeAgentReleaseChannel(channel, defaultAgentReleaseChannel) == "stable" {
		return defaultAgentReleaseBranch
	}
	return ""
}

func validatedPriorAgentReleaseTarget(target map[string]any) (map[string]any, error) {
	if target == nil {
		return nil, fmt.Errorf("prior target unavailable")
	}
	artifactPath := cleanText(target["artifact_path"])
	buildID := strings.ToLower(cleanText(target["build_id"]))
	if artifactPath == "" || buildID == "" {
		return nil, fmt.Errorf("prior target incomplete")
	}
	info, err := os.Stat(artifactPath)
	if err != nil || info.IsDir() {
		return nil, fmt.Errorf("prior target artifact unavailable")
	}
	if err := validateAgentReleaseArtifact(artifactPath); err != nil {
		return nil, err
	}
	return deepCopyMap(target), nil
}

func packageAgentReleaseArtifact(source []byte, destinationPath string, manifest map[string]any) error {
	sourceReader, err := zip.NewReader(bytes.NewReader(source), int64(len(source)))
	if err != nil {
		return fmt.Errorf("downloaded artifact is not a valid zip: %w", err)
	}
	binaries := map[string][]byte{}
	missing := make([]string, 0)
	for platform, rawPath := range requiredGoAgentArtifacts {
		relativePath := cleanText(rawPath)
		body, ok := zipMemberBytes(sourceReader.File, relativePath)
		if !ok || len(body) == 0 {
			missing = append(missing, relativePath)
			continue
		}
		binaries[platform] = body
	}
	if len(missing) > 0 {
		return fmt.Errorf("downloaded artifact missing prebuilt Go Agent binaries: %s", strings.Join(missing, ", "))
	}

	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		return err
	}
	tempPath := destinationPath + ".tmp"
	payload := deepCopyMap(manifest)
	payload["artifact_format"] = agentUpdateArtifactFormat
	payload["platform_artifacts"] = deepCopyMap(requiredGoAgentArtifacts)

	var bundle bytes.Buffer
	writer := zip.NewWriter(&bundle)
	manifestBody, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		_ = writer.Close()
		return err
	}
	if err := writeZipEntry(writer, "manifest.json", append(manifestBody, '\n')); err != nil {
		_ = writer.Close()
		return err
	}
	for platform, rawPath := range requiredGoAgentArtifacts {
		if err := writeZipEntry(writer, cleanText(rawPath), binaries[platform]); err != nil {
			_ = writer.Close()
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return err
	}
	if err := os.WriteFile(tempPath, bundle.Bytes(), 0o600); err != nil {
		return err
	}
	if err := validateAgentReleaseArtifact(tempPath); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return os.Rename(tempPath, destinationPath)
}

func validateAgentReleaseArtifact(path string) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("cached artifact is not a valid zip: %w", err)
	}
	defer reader.Close()
	missing := make([]string, 0)
	for _, rawPath := range requiredGoAgentArtifacts {
		relativePath := cleanText(rawPath)
		body, ok := zipMemberBytes(reader.File, relativePath)
		if !ok || len(body) == 0 {
			missing = append(missing, relativePath)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("artifact missing prebuilt Go Agent binaries: %s", strings.Join(missing, ", "))
	}
	if manifestText, ok := zipMemberText(reader.File, "manifest.json"); ok && strings.TrimSpace(manifestText) != "" {
		var manifest map[string]any
		if err := json.Unmarshal([]byte(manifestText), &manifest); err != nil {
			return fmt.Errorf("artifact manifest is invalid JSON: %w", err)
		}
		if cleanText(manifest["artifact_format"]) != agentUpdateArtifactFormat {
			return fmt.Errorf("artifact manifest has unsupported format")
		}
	}
	return nil
}

func agentReleaseArtifactCompiledAt(path string, info os.FileInfo) int64 {
	reader, err := zip.OpenReader(path)
	if err == nil {
		defer reader.Close()
		if manifestText, ok := zipMemberText(reader.File, "manifest.json"); ok && strings.TrimSpace(manifestText) != "" {
			var manifest map[string]any
			if json.Unmarshal([]byte(manifestText), &manifest) == nil {
				if compiledAt := firstPositiveInt64(coerceInt64(manifest["compiled_at"]), coerceInt64(manifest["build_completed_at"])); compiledAt > 0 {
					return compiledAt
				}
			}
		}
	}
	if info != nil && !info.ModTime().IsZero() {
		return info.ModTime().Unix()
	}
	return 0
}

func zipMemberText(files []*zip.File, suffix string) (string, bool) {
	body, ok := zipMemberBytes(files, suffix)
	if !ok {
		return "", false
	}
	return string(body), true
}

func zipMemberBytes(files []*zip.File, suffix string) ([]byte, bool) {
	normalizedSuffix := strings.Trim(strings.ReplaceAll(suffix, "\\", "/"), "/")
	for _, file := range files {
		normalizedName := strings.Trim(strings.ReplaceAll(file.Name, "\\", "/"), "/")
		if normalizedName == "" {
			continue
		}
		if normalizedName == normalizedSuffix || strings.HasSuffix(normalizedName, "/"+normalizedSuffix) {
			handle, err := file.Open()
			if err != nil {
				return nil, false
			}
			defer handle.Close()
			body, err := io.ReadAll(handle)
			return body, err == nil
		}
	}
	return nil, false
}

func writeZipEntry(writer *zip.Writer, name string, body []byte) error {
	entry, err := writer.Create(strings.Trim(strings.ReplaceAll(name, "\\", "/"), "/"))
	if err != nil {
		return err
	}
	_, err = entry.Write(body)
	return err
}

func agentReleaseGitHubJSON(ctx context.Context, path string) (map[string]any, error) {
	requestURL := agentReleaseGitHubURL(path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Borealis-Engine")
	resp, err := agentReleaseHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return nil, fmt.Errorf("GitHub request failed status=%d url=%s detail=%s", resp.StatusCode, requestURL, firstText(cleanText(string(snippet)), "-"))
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("GitHub request returned invalid JSON url=%s", requestURL)
	}
	if payload == nil {
		return nil, fmt.Errorf("GitHub request returned invalid JSON url=%s", requestURL)
	}
	return payload, nil
}

func agentReleaseDownload(ctx context.Context, rawURL string) ([]byte, error) {
	requestURL := resolveAgentReleaseURL(rawURL)
	if requestURL == "" {
		return nil, fmt.Errorf("artifact download URL unavailable")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Borealis-Engine")
	resp, err := agentReleaseHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return nil, fmt.Errorf("artifact download failed status=%d url=%s detail=%s", resp.StatusCode, requestURL, firstText(cleanText(string(snippet)), "-"))
	}
	return io.ReadAll(resp.Body)
}

func agentReleaseGitHubURL(path string) string {
	return strings.TrimRight(agentReleaseGitHubBaseURL(), "/") + "/" + strings.TrimLeft(path, "/")
}

func resolveAgentReleaseURL(rawURL string) string {
	text := cleanText(rawURL)
	if text == "" {
		return ""
	}
	if strings.HasPrefix(text, "http://") || strings.HasPrefix(text, "https://") {
		return text
	}
	if strings.HasPrefix(text, "/") {
		return agentReleaseGitHubURL(text)
	}
	return agentReleaseGitHubURL("/" + text)
}

func agentReleaseGitHubBaseURL() string {
	return firstText(strings.TrimSpace(os.Getenv("BOREALIS_GITHUB_API_BASE_URL")), agentReleaseGitHubDefaultBaseURL)
}

func saveAgentReleaseChannelSettings(payload map[string]any) error {
	copied := deepCopyMap(payload)
	now := time.Now().Unix()
	if coerceInt64(copied["created_at"]) == 0 {
		copied["created_at"] = now
	}
	copied["updated_at"] = now
	if err := writeAgentReleaseJSON(agentReleaseChannelsPath(), copied); err != nil {
		setAgentReleaseLastPersistError(err.Error())
		return err
	}
	setAgentReleaseLastPersistError("")
	return nil
}

func writeAgentReleaseJSON(path string, payload map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, append(content, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func sha256File(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	hash := sha256.New()
	_, _ = io.Copy(hash, file)
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func agentReleaseArtifactID(channel string, buildID string) string {
	cleanedChannel := strings.Trim(agentReleaseArtifactIDPattern.ReplaceAllString(strings.ToLower(cleanText(channel)), "-"), "-")
	if cleanedChannel == "" {
		cleanedChannel = "channel"
	}
	cleanedBuild := strings.Trim(agentReleaseArtifactIDPattern.ReplaceAllString(strings.ToLower(cleanText(buildID)), "-"), "-")
	if cleanedBuild == "" {
		cleanedBuild = "build"
	}
	if len(cleanedBuild) > 20 {
		cleanedBuild = cleanedBuild[:20]
	}
	return cleanedChannel + "-" + cleanedBuild
}

func mapValue(input map[string]any, key string) map[string]any {
	if input == nil {
		return nil
	}
	value, _ := input[key].(map[string]any)
	return value
}

func setAgentReleaseLastPersistError(value string) {
	agentReleasePersistMu.Lock()
	defer agentReleasePersistMu.Unlock()
	agentReleasePersistError = value
}

func agentReleaseLastPersistError() string {
	agentReleasePersistMu.Lock()
	defer agentReleasePersistMu.Unlock()
	return agentReleasePersistError
}
