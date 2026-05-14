//go:build windows

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
	"path/filepath"
	"strings"
	"time"
)

func preparePayloadSource(cfg BootstrapConfig, logger *BootstrapLogger) (string, func(), error) {
	startedAt := time.Now()
	logger.Tracef("Payload source resolution start: payload_path=%s exists=%t repo_url=%s repo_ref=%s", cfg.PayloadPath, fileExists(cfg.PayloadPath), cfg.RepoURL, cfg.RepoRef)
	if fileExists(cfg.PayloadPath) {
		if err := validateStagedPayloadManifest(cfg, logger); err != nil {
			return "", nil, err
		}
		if cfg.PayloadSHA256 != "" {
			logger.Tracef("Payload checksum verification start: %s", cfg.PayloadPath)
			actual, err := sha256File(cfg.PayloadPath)
			if err != nil {
				return "", nil, err
			}
			if !strings.EqualFold(actual, cfg.PayloadSHA256) {
				return "", nil, fmt.Errorf("agent payload checksum mismatch expected=%s actual=%s", cfg.PayloadSHA256, actual)
			}
			logger.Tracef("Payload checksum verified: sha256=%s", actual)
		}
		extractRoot := filepath.Join(cfg.InstallDir, "Temp", "Payload")
		_ = os.RemoveAll(extractRoot)
		if err := unzipFileLogged(cfg.PayloadPath, extractRoot, logger); err != nil {
			return "", nil, err
		}
		sourceRoot := resolveSourceRoot(extractRoot)
		if !fileExists(filepath.Join(sourceRoot, "Data", "Agent", "agent.py")) {
			return "", nil, fmt.Errorf("agent payload missing Data\\Agent\\agent.py")
		}
		logger.Tracef("Payload source resolved from staged payload: source_root=%s duration=%s", sourceRoot, time.Since(startedAt).Round(time.Millisecond))
		return sourceRoot, func() {}, nil
	}

	if sourceRoot := discoverLocalSourceRoot(); sourceRoot != "" {
		if err := validateLocalSourceRef(sourceRoot, cfg.RepoRef); err != nil {
			return "", nil, err
		}
		logger.Infof("Using local Borealis source at %s", sourceRoot)
		logger.Tracef("Payload source resolved from local source: source_root=%s duration=%s", sourceRoot, time.Since(startedAt).Round(time.Millisecond))
		return sourceRoot, func() {}, nil
	}

	logger.Infof("No staged payload found; downloading Borealis source archive for %s.", cfg.RepoRef)
	archivePath := filepath.Join(cfg.InstallDir, "Temp", "Onboarding", "borealis-source.zip")
	archiveURL, err := githubArchiveURL(cfg.RepoURL, cfg.RepoRef)
	if err != nil {
		return "", nil, err
	}
	logger.Tracef("Payload source archive download start: url=%s destination=%s", archiveURL, archivePath)
	if err := downloadFileLogged(context.Background(), archiveURL, archivePath, 180*time.Second, logger); err != nil {
		return "", nil, fmt.Errorf("failed to download Borealis source archive for repo_ref %q; refusing branch fallback: %w", cfg.RepoRef, err)
	}
	extractRoot := filepath.Join(cfg.InstallDir, "Temp", "Payload")
	_ = os.RemoveAll(extractRoot)
	if err := unzipFileLogged(archivePath, extractRoot, logger); err != nil {
		return "", nil, err
	}
	sourceRoot := resolveSourceRoot(extractRoot)
	if !fileExists(filepath.Join(sourceRoot, "Data", "Agent", "agent.py")) {
		return "", nil, fmt.Errorf("downloaded source archive missing Data\\Agent\\agent.py")
	}
	logger.Tracef("Payload source resolved from archive: source_root=%s duration=%s", sourceRoot, time.Since(startedAt).Round(time.Millisecond))
	return sourceRoot, func() {}, nil
}

func validateStagedPayloadManifest(cfg BootstrapConfig, logger *BootstrapLogger) error {
	manifestPath := strings.TrimSpace(cfg.ManifestPath)
	if manifestPath == "" || !fileExists(manifestPath) {
		return fmt.Errorf("staged agent payload manifest missing for repo_ref %q; refusing unknown branch payload", cfg.RepoRef)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read staged agent payload manifest: %w", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("parse staged agent payload manifest: %w", err)
	}
	manifestRef := strings.TrimSpace(fmt.Sprint(manifest["repo_ref"]))
	if manifestRef == "" || manifestRef == "<nil>" {
		return fmt.Errorf("staged agent payload manifest missing repo_ref; refusing unknown branch payload")
	}
	if !strings.EqualFold(manifestRef, strings.TrimSpace(cfg.RepoRef)) {
		return fmt.Errorf("staged agent payload repo_ref %q does not match requested repo_ref %q; refusing branch fallback", manifestRef, cfg.RepoRef)
	}
	logger.Tracef("Payload manifest verified: repo_ref=%s", manifestRef)
	return nil
}

func validateLocalSourceRef(sourceRoot string, expectedRef string) error {
	expected := strings.TrimSpace(expectedRef)
	if expected == "" {
		return nil
	}
	actual := localGitBranch(sourceRoot)
	if actual == "" {
		return fmt.Errorf("local Borealis source at %s has no verifiable Git branch; refusing branch fallback for repo_ref %q", sourceRoot, expected)
	}
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("local Borealis source branch %q does not match requested repo_ref %q; refusing branch fallback", actual, expected)
	}
	return nil
}

func localGitBranch(sourceRoot string) string {
	headPath := filepath.Join(sourceRoot, ".git", "HEAD")
	data, err := os.ReadFile(headPath)
	if err == nil {
		text := strings.TrimSpace(string(data))
		const prefix = "ref: refs/heads/"
		if strings.HasPrefix(text, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(text, prefix))
		}
		return ""
	}
	return ""
}

func discoverLocalSourceRoot() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	start := filepath.Dir(exe)
	for {
		if fileExists(filepath.Join(start, "Data", "Agent", "agent.py")) {
			return start
		}
		parent := filepath.Dir(start)
		if parent == start {
			break
		}
		start = parent
	}
	return ""
}

func githubArchiveURL(repoURL string, ref string) (string, error) {
	owner, repo, err := githubRepoParts(repoURL)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("https://codeload.github.com/%s/%s/zip/refs/heads/%s", owner, repo, url.PathEscape(ref)), nil
}

func githubRepoParts(repoURL string) (string, string, error) {
	normalized := strings.TrimSuffix(strings.TrimSpace(repoURL), ".git")
	parts := strings.Split(normalized, "github.com/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("source archive fallback only supports GitHub repository URLs")
	}
	repoPath := strings.Trim(parts[1], "/")
	items := strings.Split(repoPath, "/")
	if len(items) != 2 {
		return "", "", fmt.Errorf("invalid GitHub repository URL: %s", repoURL)
	}
	return items[0], items[1], nil
}

func downloadFile(ctx context.Context, rawURL string, destination string, timeout time.Duration) error {
	return downloadFileLogged(ctx, rawURL, destination, timeout, nil)
}

func downloadFileLogged(ctx context.Context, rawURL string, destination string, timeout time.Duration, logger *BootstrapLogger) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	startedAt := time.Now()
	if logger != nil {
		logger.Tracef("Download start: url=%s destination=%s timeout=%s", rawURL, destination, timeout)
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download %s returned HTTP %d", rawURL, resp.StatusCode)
	}
	if logger != nil {
		logger.Tracef("Download response: url=%s status=%d content_length=%d", rawURL, resp.StatusCode, resp.ContentLength)
	}
	tmp := destination + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, resp.Body)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	_ = os.Remove(destination)
	if err := os.Rename(tmp, destination); err != nil {
		return err
	}
	if logger != nil {
		size := int64(-1)
		if info, err := os.Stat(destination); err == nil {
			size = info.Size()
		}
		logger.Tracef("Download complete: destination=%s bytes=%d duration=%s", destination, size, time.Since(startedAt).Round(time.Millisecond))
	}
	return nil
}

func unzipFile(archivePath string, destination string) error {
	return unzipFileLogged(archivePath, destination, nil)
}

func unzipFileLogged(archivePath string, destination string, logger *BootstrapLogger) error {
	startedAt := time.Now()
	if logger != nil {
		logger.Tracef("Unzip start: archive=%s destination=%s", archivePath, destination)
	}
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer reader.Close()
	if err := os.MkdirAll(destination, 0755); err != nil {
		return err
	}
	files := 0
	dirs := 0
	for _, item := range reader.File {
		target := filepath.Join(destination, item.Name)
		cleanDest := filepath.Clean(destination)
		cleanTarget := filepath.Clean(target)
		if !strings.HasPrefix(strings.ToLower(cleanTarget), strings.ToLower(cleanDest)+strings.ToLower(string(os.PathSeparator))) && !strings.EqualFold(cleanTarget, cleanDest) {
			return fmt.Errorf("zip entry escapes destination: %s", item.Name)
		}
		if item.FileInfo().IsDir() {
			dirs++
			if err := os.MkdirAll(target, item.Mode()); err != nil {
				return err
			}
			continue
		}
		files++
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		in, err := item.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, item.Mode())
		if err != nil {
			_ = in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeInErr := in.Close()
		closeOutErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeInErr != nil {
			return closeInErr
		}
		if closeOutErr != nil {
			return closeOutErr
		}
	}
	if logger != nil {
		logger.Tracef("Unzip complete: archive=%s files=%d dirs=%d duration=%s", archivePath, files, dirs, time.Since(startedAt).Round(time.Millisecond))
	}
	return nil
}

func resolveSourceRoot(extractRoot string) string {
	if fileExists(filepath.Join(extractRoot, "Data", "Agent", "agent.py")) {
		return extractRoot
	}
	entries, err := os.ReadDir(extractRoot)
	if err == nil && len(entries) == 1 && entries[0].IsDir() {
		candidate := filepath.Join(extractRoot, entries[0].Name())
		if fileExists(filepath.Join(candidate, "Data", "Agent", "agent.py")) {
			return candidate
		}
	}
	_ = filepath.Walk(extractRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() {
			return nil
		}
		if fileExists(filepath.Join(path, "Data", "Agent", "agent.py")) {
			extractRoot = path
			return filepath.SkipDir
		}
		return nil
	})
	return extractRoot
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func copyFile(source string, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := destination + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	_ = os.Remove(destination)
	if err := os.Rename(tmp, destination); err != nil {
		return err
	}
	return nil
}
