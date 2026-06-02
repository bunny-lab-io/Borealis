package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultSiteWorkerScheduledConcurrency = 5
	maxSiteWorkerScheduledConcurrency     = 32
	defaultAnsibleRunnerJobLimit          = 5
	defaultAnsibleRunnerGlobalLimit       = 50
)

func registerServerSettingsRoutes(mux *http.ServeMux, auth *authService, fallback http.Handler) {
	mux.HandleFunc("/api/server/site-worker-settings", siteWorkerSettingsHandler(auth))
	mux.HandleFunc("/api/server/ansible-runner-settings", ansibleRunnerSettingsHandler(auth, fallback))
}

func siteWorkerSettingsHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}

		writeJSON(w, http.StatusOK, collectSiteWorkerSettingsPayload())
	}
}

func ansibleRunnerSettingsHandler(auth *authService, fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			proxyFallbackOrMethodNotAllowed(w, r, fallback, http.MethodGet)
			return
		}
		if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}

		writeJSON(w, http.StatusOK, collectAnsibleRunnerSettingsPayload())
	}
}

func collectSiteWorkerSettingsPayload() map[string]any {
	return map[string]any{
		"scheduled_task_concurrency_limit":     loadSiteWorkerScheduledConcurrency(),
		"max_scheduled_task_concurrency_limit": maxSiteWorkerScheduledConcurrency,
		"editable":                             false,
		"managed_by":                           "deployment_profile",
		"deployment_profile":                   deploymentProfilePayload(),
	}
}

func collectAnsibleRunnerSettingsPayload() map[string]any {
	settings := loadAnsibleRunnerSettings()
	return map[string]any{
		"job_concurrency_limit":    coercePositiveInt(settings["job_concurrency_limit"], defaultAnsibleRunnerJobLimit),
		"global_concurrency_limit": coercePositiveInt(settings["global_concurrency_limit"], defaultAnsibleRunnerGlobalLimit),
	}
}

func deploymentProfilePayload() map[string]any {
	name := strings.TrimSpace(os.Getenv("BOREALIS_DEPLOYMENT_PROFILE"))
	if name == "" {
		name = "Unprofiled"
	}
	return map[string]any{
		"name":            name,
		"rank":            safeEnvInt("BOREALIS_DEPLOYMENT_PROFILE_RANK", 0),
		"cpu_rank":        safeEnvInt("BOREALIS_DEPLOYMENT_CPU_RANK", 0),
		"memory_rank":     safeEnvInt("BOREALIS_DEPLOYMENT_MEMORY_RANK", 0),
		"host_vcpu":       safeEnvInt("BOREALIS_DEPLOYMENT_HOST_VCPU", 0),
		"host_memory_mib": safeEnvInt("BOREALIS_DEPLOYMENT_HOST_MEMORY_MIB", 0),
		"host_memory_gib": strings.TrimSpace(os.Getenv("BOREALIS_DEPLOYMENT_HOST_MEMORY_GIB")),
	}
}

func loadAnsibleRunnerSettings() map[string]any {
	defaults := defaultAnsibleRunnerSettings()
	settings := loadJSONSettingsFile(ansibleRunnerSettingsPath())
	if len(settings) == 0 {
		return defaults
	}
	return map[string]any{
		"job_concurrency_limit": coercePositiveInt(
			settings["job_concurrency_limit"],
			coercePositiveInt(defaults["job_concurrency_limit"], defaultAnsibleRunnerJobLimit),
		),
		"global_concurrency_limit": coercePositiveInt(
			settings["global_concurrency_limit"],
			coercePositiveInt(defaults["global_concurrency_limit"], defaultAnsibleRunnerGlobalLimit),
		),
	}
}

func defaultAnsibleRunnerSettings() map[string]any {
	return map[string]any{
		"job_concurrency_limit": coercePositiveInt(
			os.Getenv("BOREALIS_ANSIBLE_RUNNER_JOB_CONCURRENCY_LIMIT"),
			defaultAnsibleRunnerJobLimit,
		),
		"global_concurrency_limit": coercePositiveInt(
			os.Getenv("BOREALIS_ANSIBLE_RUNNER_GLOBAL_CONCURRENCY_LIMIT"),
			defaultAnsibleRunnerGlobalLimit,
		),
	}
}

func ansibleRunnerSettingsPath() string {
	if override := strings.TrimSpace(os.Getenv("BOREALIS_ANSIBLE_RUNNER_SETTINGS_PATH")); override != "" {
		return expandHomePath(override)
	}
	root := strings.TrimSpace(os.Getenv("BOREALIS_PROJECT_ROOT"))
	if root == "" {
		root = "/opt/Borealis"
	}
	return filepath.Join(expandHomePath(root), "Engine", "Services", "api-backend", "config", "ansible_runner_settings.json")
}

func loadSiteWorkerScheduledConcurrency() int {
	if value := strings.TrimSpace(os.Getenv("BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY")); value != "" {
		return coerceSiteWorkerScheduledConcurrency(value, defaultSiteWorkerScheduledConcurrency)
	}

	settings := loadSiteWorkerSettingsFile()
	if value, ok := settings["scheduled_task_concurrency_limit"]; ok {
		return coerceSiteWorkerScheduledConcurrency(value, defaultSiteWorkerScheduledConcurrency)
	}
	return defaultSiteWorkerScheduledConcurrency
}

func loadSiteWorkerSettingsFile() map[string]any {
	return loadJSONSettingsFile(siteWorkerSettingsPath())
}

func loadJSONSettingsFile(path string) map[string]any {
	content, err := os.ReadFile(path)
	if err != nil {
		return map[string]any{}
	}
	var settings map[string]any
	if err := json.Unmarshal(content, &settings); err != nil || settings == nil {
		return map[string]any{}
	}
	return settings
}

func siteWorkerSettingsPath() string {
	if override := strings.TrimSpace(os.Getenv("BOREALIS_SITE_WORKER_SETTINGS_PATH")); override != "" {
		return expandHomePath(override)
	}
	root := strings.TrimSpace(os.Getenv("BOREALIS_PROJECT_ROOT"))
	if root == "" {
		root = "/opt/Borealis"
	}
	return filepath.Join(expandHomePath(root), "Engine", "Services", "api-backend", "config", "site_worker_settings.json")
}

func expandHomePath(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func coerceSiteWorkerScheduledConcurrency(value any, fallback int) int {
	parsed, err := intFromAny(value)
	if err != nil {
		parsed = fallback
	}
	if parsed < 1 {
		return 1
	}
	if parsed > maxSiteWorkerScheduledConcurrency {
		return maxSiteWorkerScheduledConcurrency
	}
	return parsed
}

func coercePositiveInt(value any, fallback int) int {
	parsed, err := intFromAny(value)
	if err != nil {
		parsed = fallback
	}
	if parsed < 1 {
		return 1
	}
	return parsed
}

func safeEnvInt(name string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil {
		return fallback
	}
	return parsed
}

func intFromAny(value any) (int, error) {
	switch typed := value.(type) {
	case int:
		return typed, nil
	case int64:
		return int(typed), nil
	case float64:
		return int(typed), nil
	case json.Number:
		parsed, err := typed.Int64()
		return int(parsed), err
	case string:
		return strconv.Atoi(strings.TrimSpace(typed))
	default:
		return 0, strconv.ErrSyntax
	}
}
