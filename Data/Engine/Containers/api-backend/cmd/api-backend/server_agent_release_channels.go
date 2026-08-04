package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultAgentReleaseChannel = "stable"
	defaultAgentReleaseRepo    = "bunny-lab-io/Borealis"
	defaultAgentReleaseBranch  = "main"
	agentReleaseSourceEngine   = "engine"
	agentReleaseSourceGitHub   = "github"
)

type githubTokenStateStore interface {
	githubTokenState(ctx context.Context) map[string]any
}

func registerAgentReleaseChannelRoutes(mux *http.ServeMux, auth *authService, fallback http.Handler) {
	mux.HandleFunc("/api/server/agent-release-channels", agentReleaseChannelsHandler(auth, fallback))
	mux.HandleFunc("/api/server/agent-release-channels/refresh", agentReleaseChannelsRefreshHandler(auth))
}

func agentReleaseChannelsHandler(auth *authService, fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodPut {
			writeMethodNotAllowed(w, "GET, PUT")
			return
		}
		if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}

		timeout := auth.timeout
		if timeout <= 0 {
			timeout = defaultAuthTimeout
		}
		if r.Method == http.MethodPut && timeout < agentReleaseRefreshTimeout {
			timeout = agentReleaseRefreshTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		if r.Method == http.MethodPut {
			body, err := readJSONMap(r)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json", "message": "Request body must be valid JSON."})
				return
			}
			settings := collectAgentReleaseChannelSettings()
			settings["default_channel"] = defaultAgentReleaseChannel
			if value, ok := body["binary_source"]; ok {
				settings["binary_source"] = normalizeAgentReleaseBinarySource(value, agentReleaseSourceGitHub)
			}
			if value, ok := body["source"]; ok {
				settings["binary_source"] = normalizeAgentReleaseBinarySource(value, agentReleaseSourceGitHub)
			}
			if value, ok := body["github_fallback_enabled"]; ok {
				settings["github_fallback_enabled"] = boolFromAny(value)
			}
			if value, ok := body["repo"]; ok {
				github, _ := settings["github"].(map[string]any)
				if github == nil {
					github = map[string]any{}
				}
				github["repo"] = normalizeAgentReleaseRepo(value, defaultAgentReleaseRepo)
				settings["github"] = github
			}
			if rawGithub, ok := body["github"].(map[string]any); ok {
				github, _ := settings["github"].(map[string]any)
				if github == nil {
					github = map[string]any{}
				}
				if value, exists := rawGithub["repo"]; exists {
					github["repo"] = normalizeAgentReleaseRepo(value, defaultAgentReleaseRepo)
				}
				settings["github"] = github
			}
			if err := saveAgentReleaseChannelSettings(settings); err != nil {
				payload := agentReleaseChannelsResponsePayload(ctx, auth)
				writeJSON(w, http.StatusInternalServerError, map[string]any{
					"error":    "release_channels_save_failed",
					"message":  err.Error(),
					"settings": payload,
				})
				return
			}
			payload := refreshAgentReleaseChannels(ctx, true)
			addAgentReleaseRuntimeFields(ctx, auth, payload)
			writeJSON(w, http.StatusOK, payload)
			return
		}
		writeJSON(w, http.StatusOK, agentReleaseChannelsResponsePayload(ctx, auth))
	}
}

func agentReleaseChannelsRefreshHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), agentReleaseRefreshTimeout)
		defer cancel()
		payload := refreshAgentReleaseChannels(ctx, true)
		addAgentReleaseRuntimeFields(ctx, auth, payload)
		writeJSON(w, http.StatusOK, payload)
	}
}

func agentReleaseChannelsResponsePayload(ctx context.Context, auth *authService) map[string]any {
	payload := collectAgentReleaseChannelSettings()
	addAgentReleaseRuntimeFields(ctx, auth, payload)
	return payload
}

func addAgentReleaseRuntimeFields(ctx context.Context, auth *authService, payload map[string]any) {
	if payload == nil {
		return
	}
	if auth != nil {
		if store, ok := auth.store.(githubTokenStateStore); ok {
			payload["github_token"] = store.githubTokenState(ctx)
		} else {
			payload["github_token"] = defaultGithubTokenState()
		}
	} else {
		payload["github_token"] = defaultGithubTokenState()
	}
	payload["settings_path"] = agentReleaseChannelsPath()
	if _, ok := payload["last_persist_error"]; !ok {
		payload["last_persist_error"] = agentReleaseLastPersistError()
	}
}

func collectAgentReleaseChannelSettings() map[string]any {
	defaults := defaultAgentReleaseChannelSettings(time.Now().Unix())
	loaded := loadJSONSettingsFile(agentReleaseChannelsPath())
	if len(loaded) == 0 {
		return defaults
	}

	merged := deepCopyMap(defaults)
	for key, value := range loaded {
		if key == "channels" {
			continue
		}
		merged[key] = value
	}
	merged["default_channel"] = defaultAgentReleaseChannel

	github, _ := merged["github"].(map[string]any)
	if github == nil {
		github = map[string]any{}
	}
	github["repo"] = normalizeAgentReleaseRepo(github["repo"], defaultAgentReleaseRepo)
	github["default_branch"] = firstText(cleanText(github["default_branch"]), defaultAgentReleaseBranch)
	merged["github"] = github
	merged["binary_source"] = normalizeAgentReleaseBinarySource(merged["binary_source"], agentReleaseSourceGitHub)
	merged["github_fallback_enabled"] = boolFromAny(merged["github_fallback_enabled"])

	loadedChannels, _ := loaded["channels"].(map[string]any)
	channels, _ := merged["channels"].(map[string]any)
	if channels == nil {
		channels = map[string]any{}
	}
	for _, channel := range []string{"stable", "unstable"} {
		if existing, ok := loadedChannels[channel].(map[string]any); ok {
			target, _ := channels[channel].(map[string]any)
			if target == nil {
				target = map[string]any{}
			}
			for key, value := range existing {
				target[key] = value
			}
			channels[channel] = target
		}
	}
	merged["channels"] = channels
	return merged
}

func defaultAgentReleaseChannelSettings(now int64) map[string]any {
	repo := normalizeAgentReleaseRepo(os.Getenv("BOREALIS_UPDATE_REPO"), defaultAgentReleaseRepo)
	branch := firstText(cleanText(os.Getenv("BOREALIS_UPDATE_BRANCH")), defaultAgentReleaseBranch)
	return map[string]any{
		"version":                 int64(1),
		"default_channel":         defaultAgentReleaseChannel,
		"binary_source":           agentReleaseSourceGitHub,
		"github_fallback_enabled": false,
		"github": map[string]any{
			"repo":           repo,
			"default_branch": branch,
		},
		"channels": map[string]any{
			"stable": map[string]any{
				"channel":         "stable",
				"build_id":        "",
				"artifact_id":     "",
				"artifact_sha256": "",
				"artifact_size":   int64(0),
				"artifact_path":   "",
				"download_url":    "",
				"fallback_url":    "",
				"version_label":   "",
				"release_tag":     "",
				"release_name":    "",
				"published_at":    "",
				"branch":          defaultAgentReleaseBranch,
				"promoted_at":     int64(0),
				"refreshed_at":    int64(0),
				"last_error":      "",
			},
			"unstable": map[string]any{
				"channel":         "unstable",
				"build_id":        "",
				"artifact_id":     "",
				"artifact_sha256": "",
				"artifact_size":   int64(0),
				"artifact_path":   "",
				"download_url":    "",
				"fallback_url":    "",
				"version_label":   branch,
				"branch":          branch,
				"promoted_at":     int64(0),
				"refreshed_at":    int64(0),
				"last_error":      "",
			},
		},
		"last_refresh_started_at":   int64(0),
		"last_refresh_completed_at": int64(0),
		"last_refresh_error":        "",
		"created_at":                now,
		"updated_at":                now,
	}
}

func agentReleaseChannelsPath() string {
	if override := strings.TrimSpace(os.Getenv("BOREALIS_AGENT_RELEASE_CHANNELS_PATH")); override != "" {
		return expandHomePath(override)
	}
	root := strings.TrimSpace(os.Getenv("BOREALIS_PROJECT_ROOT"))
	if root == "" {
		root = "/opt/Borealis"
	}
	return filepath.Join(expandHomePath(root), "Engine", "Services", "api-backend", "config", "agent_release_channels.json")
}

func (s *postgresOperatorStore) githubTokenState(ctx context.Context) map[string]any {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return defaultGithubTokenState()
	}
	defer conn.Close()

	var token sql.NullString
	var resetRequired sql.NullInt64
	var resetAt sql.NullInt64
	err = conn.QueryRowContext(ctx, `
		SELECT token, reset_required, reset_at
		  FROM engine.github_token
		 LIMIT 1
	`).Scan(&token, &resetRequired, &resetAt)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultGithubTokenState()
	}
	if err != nil {
		return defaultGithubTokenState()
	}
	return map[string]any{
		"has_token":      strings.TrimSpace(nullString(token)) != "",
		"reset_required": nullInt(resetRequired) != 0,
		"reset_at":       nullInt(resetAt),
	}
}

func defaultGithubTokenState() map[string]any {
	return map[string]any{
		"has_token":      false,
		"reset_required": false,
		"reset_at":       int64(0),
	}
}

func normalizeAgentReleaseChannel(value any, fallback string) string {
	text := strings.ToLower(strings.TrimSpace(cleanText(value)))
	switch text {
	case "stable", "unstable":
		return text
	default:
		return fallback
	}
}

func normalizeAgentReleaseBinarySource(value any, fallback string) string {
	text := strings.ToLower(strings.TrimSpace(cleanText(value)))
	switch text {
	case "engine", "engine-compiled", "engine_compiled", "engine-cache", "engine_cached":
		return agentReleaseSourceEngine
	case "github", "git", "repository", "repo", "release", "releases":
		return agentReleaseSourceGitHub
	default:
		return fallback
	}
}

func normalizeAgentReleaseRepo(value any, fallback string) string {
	text := strings.TrimSpace(cleanText(value))
	if strings.Contains(text, "/") {
		return text
	}
	return fallback
}

func deepCopyMap(source map[string]any) map[string]any {
	copied := make(map[string]any, len(source))
	for key, value := range source {
		copied[key] = deepCopyValue(value)
	}
	return copied
}

func deepCopyValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return deepCopyMap(typed)
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, deepCopyValue(item))
		}
		return out
	default:
		return typed
	}
}
