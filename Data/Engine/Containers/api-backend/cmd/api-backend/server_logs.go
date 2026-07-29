package main

import (
	"context"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	defaultLogRetentionDays          = 30
	defaultLogRetentionSweepInterval = 6 * time.Hour
)

var rotatedLogFilePattern = regexp.MustCompile(`(?i)\.log\.\d{4}-\d{2}-\d{2}$`)

func registerServerLogRoutes(mux *http.ServeMux, auth *authService, _ http.Handler) {
	mux.HandleFunc("/api/server/logs", serverLogsHandler(auth))
	mux.HandleFunc("/api/server/logs/", serverLogEntriesHandler(auth))
}

func serverLogsHandler(auth *authService) http.HandlerFunc {
	return retiredServerLogsHandler(auth)
}

func serverLogEntriesHandler(auth *authService) http.HandlerFunc {
	return retiredServerLogsHandler(auth)
}

func retiredServerLogsHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		writeJSON(w, http.StatusGone, map[string]any{
			"error":   "server_logs_retired",
			"message": "WebUI log access is retired. Use Engine Log Access CLI documentation for K3s pod logs and Borealis file logs.",
		})
	}
}

func startServerLogRetentionRuntime(ctx context.Context) {
	runServerLogRetentionSweep()
	go func() {
		ticker := time.NewTicker(defaultLogRetentionSweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runServerLogRetentionSweep()
			}
		}
	}()
}

func runServerLogRetentionSweep() []map[string]any {
	roots := resolveServerLogRetentionRoots()
	retentionDays := engineFileLogRetentionDays()
	deletions := []map[string]any{}
	for _, root := range roots {
		deleted, err := applyLogRetention(root, retentionDays)
		if err != nil {
			log.Printf("server_log_retention_failed root=%s retention_days=%d error=%v", root, retentionDays, err)
			continue
		}
		if len(deleted) == 0 {
			continue
		}
		log.Printf("server_log_retention_applied root=%s retention_days=%d deleted=%d", root, retentionDays, len(deleted))
		for _, name := range deleted {
			deletions = append(deletions, map[string]any{"root": root, "file": name})
		}
	}
	return deletions
}

func engineFileLogRetentionDays() int {
	return envInt("BOREALIS_ENGINE_FILE_LOG_RETENTION_DAYS", defaultLogRetentionDays, 1, 3650)
}

func resolveLogRoot() string {
	if value := cleanText(os.Getenv("BOREALIS_GO_API_LOG_ROOT")); value != "" {
		return filepath.Clean(value)
	}
	if value := cleanText(os.Getenv("BOREALIS_LOG_ROOT")); value != "" {
		return filepath.Clean(value)
	}
	if logFile := cleanText(os.Getenv("BOREALIS_LOG_FILE")); logFile != "" {
		return filepath.Dir(filepath.Clean(logFile))
	}
	return "/opt/Borealis/Engine/Services/api-backend/logs"
}

func resolveServerLogRetentionRoots() []string {
	if configured := cleanText(os.Getenv("BOREALIS_ENGINE_FILE_LOG_RETENTION_ROOTS")); configured != "" {
		return cleanExistingLogRoots(splitConfiguredLogRoots(configured))
	}

	apiLogRoot := resolveLogRoot()
	candidates := []string{
		apiLogRoot,
		cleanText(os.Getenv("BOREALIS_TRAEFIK_LOG_ROOT")),
		cleanText(os.Getenv("BOREALIS_WIREGUARD_TUNNEL_LOG_ROOT")),
	}
	if engineRoot := engineRuntimeRootFromAPILogRoot(apiLogRoot); engineRoot != "" {
		candidates = append(candidates,
			filepath.Join(engineRoot, "Services", "traefik-edge", "logs"),
			filepath.Join(engineRoot, "Services", "wireguard-tunnel", "logs"),
		)
	}
	return cleanExistingLogRoots(candidates)
}

func splitConfiguredLogRoots(raw string) []string {
	separator := string(filepath.ListSeparator)
	normalized := strings.ReplaceAll(raw, "\n", separator)
	normalized = strings.ReplaceAll(normalized, ",", separator)
	return filepath.SplitList(normalized)
}

func cleanExistingLogRoots(candidates []string) []string {
	roots := []string{}
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		root := cleanText(candidate)
		if root == "" {
			continue
		}
		root = filepath.Clean(root)
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			continue
		}
		if abs, err := filepath.Abs(root); err == nil {
			root = abs
		}
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		roots = append(roots, root)
	}
	return roots
}

func engineRuntimeRootFromAPILogRoot(apiLogRoot string) string {
	cleaned := filepath.ToSlash(filepath.Clean(apiLogRoot))
	suffix := "/Services/api-backend/logs"
	if !strings.HasSuffix(cleaned, suffix) {
		return ""
	}
	root := strings.TrimSuffix(cleaned, suffix)
	if root == "" {
		return ""
	}
	return filepath.Clean(filepath.FromSlash(root))
}

func applyLogRetention(root string, defaultDays int) ([]string, error) {
	if defaultDays <= 0 {
		return nil, nil
	}
	now := time.Now().UTC()
	cutoff := now.Add(-time.Duration(defaultDays) * 24 * time.Hour)
	deleted := []string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		name = filepath.ToSlash(name)
		if !rotatedLogFilePattern.MatchString(name) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		if !info.ModTime().UTC().Before(cutoff) {
			return nil
		}
		if err := os.Remove(path); err == nil {
			deleted = append(deleted, name)
		}
		return nil
	})
	if err != nil {
		return deleted, err
	}
	sort.Strings(deleted)
	return deleted, nil
}

func asMap(value any) map[string]any {
	typed, _ := value.(map[string]any)
	if typed == nil {
		return map[string]any{}
	}
	return typed
}
