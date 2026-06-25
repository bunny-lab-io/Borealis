package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const agentUpdateArtifactFormat = "borealis-go-agent-v1"

var requiredGoAgentArtifacts = map[string]any{
	"windows-amd64": "Data/Agent/dist/windows-amd64/Agent.exe",
	"linux-amd64":   "Data/Agent/dist/linux-amd64/Agent",
}

type agentUpdateManifestStore interface {
	agentUpdateManifest(ctx context.Context, guid, hostname, installedBuildID string) (map[string]any, int, error)
}

type agentUpdateDeviceIdentity struct {
	GUID                   string
	Hostname               string
	AgentHash              string
	AgentID                string
	ReleaseChannelOverride string
}

func registerAgentUpdateRoutes(mux *http.ServeMux, auth *authService, signer *agentJWTSigner, dpop *dpopVerifier) {
	mux.HandleFunc("GET /api/agent/update/manifest", agentUpdateManifestHandler(auth, signer, dpop))
	mux.HandleFunc("GET /api/agent/update/download/{artifact_id}", agentUpdateDownloadHandler(auth, signer, dpop))
}

func agentUpdateManifestHandler(auth *authService, signer *agentJWTSigner, dpop *dpopVerifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deviceCtx, failure := authenticateDeviceBearer(r.Context(), r, auth, signer, dpop)
		if failure != nil {
			failure.write(w)
			return
		}
		store, ok := auth.store.(agentUpdateManifestStore)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "release_channels_unavailable"})
			return
		}
		timeout := auth.timeout
		if timeout <= 0 {
			timeout = defaultAuthTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		query := r.URL.Query()
		installedBuildID := firstText(
			cleanText(query.Get("installed_build_id")),
			cleanText(query.Get("current_build_id")),
			cleanText(query.Get("agent_build_id")),
		)
		payload, status, err := store.agentUpdateManifest(ctx, deviceCtx.GUID, query.Get("hostname"), installedBuildID)
		if err != nil {
			writeJSON(w, status, map[string]any{
				"error":   "manifest_unavailable",
				"message": err.Error(),
			})
			return
		}
		writeJSON(w, status, payload)
	}
}

func agentUpdateDownloadHandler(auth *authService, signer *agentJWTSigner, dpop *dpopVerifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, failure := authenticateDeviceBearer(r.Context(), r, auth, signer, dpop); failure != nil {
			failure.write(w)
			return
		}
		artifactID := cleanText(r.PathValue("artifact_id"))
		artifactPath := agentUpdateArtifactPath(artifactID)
		if artifactPath == "" {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "artifact_not_found"})
			return
		}
		info, err := os.Stat(artifactPath)
		if err != nil || info.IsDir() {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "artifact_not_found"})
			return
		}
		filename := filepath.Base(artifactPath)
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
		http.ServeFile(w, r, artifactPath)
	}
}

func (s *postgresOperatorStore) agentUpdateManifest(ctx context.Context, guid, hostname, installedBuildID string) (map[string]any, int, error) {
	identity, err := s.agentUpdateDeviceIdentity(ctx, guid, hostname)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, http.StatusServiceUnavailable, errors.New("device not found")
		}
		return nil, http.StatusServiceUnavailable, err
	}
	settings := collectAgentReleaseChannelSettings()
	effectiveChannel := resolveEffectiveAgentReleaseChannel(settings, identity.ReleaseChannelOverride)
	target := agentReleaseChannelTarget(settings, effectiveChannel)
	targetBuildID := strings.ToLower(cleanText(target["build_id"]))
	if targetBuildID == "" {
		return nil, http.StatusServiceUnavailable, errors.New("channel target unavailable")
	}
	currentBuildID := strings.ToLower(firstText(cleanText(installedBuildID), cleanText(identity.AgentHash)))
	artifactID := cleanText(target["artifact_id"])
	return map[string]any{
		"status":             "ok",
		"hostname":           firstText(identity.Hostname, hostname),
		"guid":               firstText(identity.GUID, guid),
		"effective_channel":  effectiveChannel,
		"target_channel":     effectiveChannel,
		"target_build_id":    targetBuildID,
		"update_available":   currentBuildID == "" || currentBuildID != targetBuildID,
		"artifact_id":        artifactID,
		"artifact_sha256":    cleanText(target["artifact_sha256"]),
		"artifact_size":      coerceInt64(target["artifact_size"]),
		"artifact_format":    firstText(cleanText(target["artifact_format"]), agentUpdateArtifactFormat),
		"platform_artifacts": agentReleasePlatformArtifacts(target["platform_artifacts"]),
		"download_path":      fmt.Sprintf("/api/agent/update/download/%s", artifactID),
		"fallback_url":       cleanText(target["fallback_url"]),
		"release_tag":        cleanText(target["release_tag"]),
		"release_name":       cleanText(target["release_name"]),
		"version_label":      cleanText(target["version_label"]),
		"published_at":       cleanText(target["published_at"]),
		"branch":             cleanText(target["branch"]),
		"repo":               agentReleaseSettingsRepo(settings),
		"promoted_at":        coerceInt64(target["promoted_at"]),
	}, http.StatusOK, nil
}

func (s *postgresOperatorStore) agentUpdateDeviceIdentity(ctx context.Context, guid, hostname string) (agentUpdateDeviceIdentity, error) {
	normalizedGUID := normalizeCanonicalGUID(guid)
	normalizedHost := strings.ToLower(cleanText(hostname))
	if normalizedGUID == "" && normalizedHost == "" {
		return agentUpdateDeviceIdentity{}, sql.ErrNoRows
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return agentUpdateDeviceIdentity{}, err
	}
	defer conn.Close()

	var rowGUID, rowHostname, rowAgentHash, rowAgentID, rowOverride sql.NullString
	if normalizedGUID != "" {
		err = conn.QueryRowContext(
			ctx,
			`
			SELECT guid, hostname, agent_hash, agent_id, agent_release_channel_override
			  FROM engine.devices
			 WHERE UPPER(guid)=UPPER($1)
			 LIMIT 1
			`,
			normalizedGUID,
		).Scan(&rowGUID, &rowHostname, &rowAgentHash, &rowAgentID, &rowOverride)
	} else {
		err = conn.QueryRowContext(
			ctx,
			`
			SELECT guid, hostname, agent_hash, agent_id, agent_release_channel_override
			  FROM engine.devices
			 WHERE LOWER(hostname)=LOWER($1)
			 ORDER BY last_seen DESC
			 LIMIT 1
			`,
			normalizedHost,
		).Scan(&rowGUID, &rowHostname, &rowAgentHash, &rowAgentID, &rowOverride)
	}
	if err != nil {
		return agentUpdateDeviceIdentity{}, err
	}
	var row agentUpdateDeviceIdentity
	row.GUID = nullString(rowGUID)
	row.Hostname = nullString(rowHostname)
	row.AgentHash = nullString(rowAgentHash)
	row.AgentID = nullString(rowAgentID)
	row.ReleaseChannelOverride = nullString(rowOverride)
	row.GUID = normalizeCanonicalGUID(row.GUID)
	row.Hostname = cleanText(row.Hostname)
	row.AgentHash = strings.ToLower(cleanText(row.AgentHash))
	row.AgentID = cleanText(row.AgentID)
	row.ReleaseChannelOverride = normalizeAgentReleaseChannel(row.ReleaseChannelOverride, "")
	return row, nil
}

func resolveEffectiveAgentReleaseChannel(settings map[string]any, override string) string {
	if normalized := normalizeAgentReleaseChannel(override, ""); normalized != "" {
		return normalized
	}
	return normalizeAgentReleaseChannel(settings["default_channel"], defaultAgentReleaseChannel)
}

func agentReleaseChannelTarget(settings map[string]any, channel string) map[string]any {
	normalized := normalizeAgentReleaseChannel(channel, defaultAgentReleaseChannel)
	channels, _ := settings["channels"].(map[string]any)
	if channels == nil {
		return map[string]any{"channel": normalized}
	}
	target, _ := channels[normalized].(map[string]any)
	if target == nil {
		return map[string]any{"channel": normalized}
	}
	copied := deepCopyMap(target)
	copied["channel"] = normalized
	return copied
}

func agentReleasePlatformArtifacts(value any) map[string]any {
	if artifacts, ok := value.(map[string]any); ok && len(artifacts) > 0 {
		return deepCopyMap(artifacts)
	}
	return deepCopyMap(requiredGoAgentArtifacts)
}

func agentReleaseSettingsRepo(settings map[string]any) string {
	github, _ := settings["github"].(map[string]any)
	if github == nil {
		return ""
	}
	return cleanText(github["repo"])
}

func agentUpdateCacheRoot() string {
	root := strings.TrimSpace(os.Getenv("BOREALIS_PROJECT_ROOT"))
	if root == "" {
		root = "/opt/Borealis"
	}
	return filepath.Join(expandHomePath(root), "Engine", "Services", "api-backend", "cache", "AgentUpdates")
}

func agentUpdateArtifactPath(artifactID string) string {
	artifactID = cleanText(artifactID)
	if artifactID == "" || strings.ContainsAny(artifactID, `/\`) {
		return ""
	}
	return filepath.Join(agentUpdateCacheRoot(), artifactID+".zip")
}
