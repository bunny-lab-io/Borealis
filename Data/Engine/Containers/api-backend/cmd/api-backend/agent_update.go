package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const agentUpdateArtifactFormat = "borealis-go-agent-v1"
const agentInstallDownloadTokenType = "agent_install_download"
const defaultAgentInstallDownloadTokenTTL = 24 * time.Hour

var requiredGoAgentArtifacts = map[string]any{
	"windows-amd64": "Data/Agent/dist/windows-amd64/Agent.exe",
	"linux-amd64":   "Data/Agent/dist/linux-amd64/Agent",
}

type agentUpdateManifestStore interface {
	agentUpdateManifest(ctx context.Context, guid, hostname, installedBuildID string) (map[string]any, int, error)
}

type agentInstallDownloadStore interface {
	siteEnrollmentCode(ctx context.Context, siteID int64) (string, error)
}

type agentInstallLinkStore interface {
	agentInstallDownloadStore
	ensureAgentInstallLink(ctx context.Context, siteID int64, platform string, artifactID string, expiresAt int64) (agentInstallLinkRecord, error)
	agentInstallLinkForDownload(ctx context.Context, siteID int64, platform string, artifactID string, expiresAt int64) (agentInstallLinkRecord, error)
	recordAgentInstallLinkDownload(ctx context.Context, linkID int64) error
	revokeAgentInstallLink(ctx context.Context, siteID int64, platform string, artifactID string, expiresAt int64) (agentInstallLinkRecord, error)
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
	mux.HandleFunc("GET /api/agent/install/download/{platform}", agentInstallDownloadHandler(auth))
	mux.HandleFunc("GET /api/agent/install/download/{token}/{platform}", agentInstallDownloadHandler(auth))
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

func agentInstallDownloadHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := cleanText(r.PathValue("token"))
		platform := normalizeAgentInstallPlatform(r.PathValue("platform"))
		var artifactID string
		var linkRecord agentInstallLinkRecord
		if token == "" {
			var err error
			linkRecord, err = verifyAgentInstallDownloadQuery(r.Context(), auth, r.URL.Query(), platform)
			if err != nil {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_install_download_signature"})
				return
			}
			artifactID = linkRecord.ArtifactID
		} else {
			payload, err := verifyAgentInstallDownloadToken(auth, token, platform)
			if err != nil {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_install_download_token"})
				return
			}
			siteID, ok := parseInt64Value(payload["site_id"])
			if !ok || siteID <= 0 {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_install_download_token"})
				return
			}
			if store, ok := auth.store.(agentInstallDownloadStore); ok {
				enrollmentCode, err := store.siteEnrollmentCode(r.Context(), siteID)
				if err != nil {
					writeJSON(w, http.StatusNotFound, map[string]any{"error": "site_not_found"})
					return
				}
				if sha256HexText(enrollmentCode) != cleanText(payload["enrollment_code_sha256"]) {
					writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "install_download_token_revoked"})
					return
				}
			}
			artifactID = cleanText(payload["artifact_id"])
		}
		artifactPath := agentUpdateArtifactPath(artifactID)
		if artifactPath == "" {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "artifact_not_found"})
			return
		}
		body, filename, err := platformArtifactBytes(artifactPath, platform)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "artifact_not_found", "message": err.Error()})
			return
		}
		contentType := "application/octet-stream"
		if strings.HasSuffix(strings.ToLower(filename), ".exe") {
			contentType = "application/vnd.microsoft.portable-executable"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
		w.Header().Set("Cache-Control", "no-store")
		written, writeErr := w.Write(body)
		if writeErr == nil && written == len(body) && linkRecord.ID > 0 {
			if store, ok := auth.store.(agentInstallLinkStore); ok {
				_ = store.recordAgentInstallLinkDownload(r.Context(), linkRecord.ID)
			}
		}
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

func (s *postgresOperatorStore) siteEnrollmentCode(ctx context.Context, siteID int64) (string, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	var enrollmentCode sql.NullString
	err = conn.QueryRowContext(ctx, `SELECT enrollment_code FROM engine.sites WHERE id=$1 LIMIT 1`, siteID).Scan(&enrollmentCode)
	if err != nil {
		return "", err
	}
	return nullString(enrollmentCode), nil
}

func resolveEffectiveAgentReleaseChannel(settings map[string]any, override string) string {
	if normalized := normalizeAgentReleaseChannel(override, ""); normalized != "" {
		return normalized
	}
	return defaultAgentReleaseChannel
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
	if normalized == "stable" && cleanText(copied["branch"]) == "" {
		copied["branch"] = defaultAgentReleaseBranch
	}
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

func signAgentInstallDownloadToken(auth *authService, siteID int64, enrollmentCode string, artifactID string, platform string) (string, error) {
	if auth == nil || auth.verifier == nil {
		return "", errInvalidToken
	}
	return auth.verifier.signPayload(map[string]any{
		"typ":                    agentInstallDownloadTokenType,
		"site_id":                siteID,
		"enrollment_code_sha256": sha256HexText(enrollmentCode),
		"artifact_id":            cleanText(artifactID),
		"platform":               cleanText(platform),
	})
}

func verifyAgentInstallDownloadToken(auth *authService, token string, platform string) (map[string]any, error) {
	if auth == nil || auth.verifier == nil {
		return nil, errInvalidToken
	}
	payload, err := auth.verifier.signedPayload(token, agentInstallDownloadTokenTTL())
	if err != nil {
		return nil, err
	}
	if cleanText(payload["typ"]) != agentInstallDownloadTokenType {
		return nil, errInvalidToken
	}
	if normalizeAgentInstallPlatform(cleanText(payload["platform"])) != normalizeAgentInstallPlatform(platform) {
		return nil, errInvalidToken
	}
	if cleanText(payload["artifact_id"]) == "" {
		return nil, errInvalidToken
	}
	return payload, nil
}

func signAgentInstallDownloadPath(auth *authService, enrollmentCode string, link agentInstallLinkRecord) (string, string, error) {
	if auth == nil || auth.verifier == nil {
		return "", "", errInvalidToken
	}
	if link.SiteID <= 0 || link.ArtifactID == "" || link.Platform == "" || link.LinkNonce == "" || link.ExpiresAt <= 0 {
		return "", "", errInvalidToken
	}
	expiresAt := time.Unix(link.ExpiresAt, 0).UTC().Truncate(time.Second)
	expiresText := expiresAt.Format(time.RFC3339)
	signature := auth.verifier.signatureSegment(agentInstallDownloadSignatureInput(link.SiteID, enrollmentCode, link.ArtifactID, link.Platform, expiresText, link.LinkNonce))
	query := "site_id=" + url.QueryEscape(strconv.FormatInt(link.SiteID, 10)) +
		"&artifact=" + url.QueryEscape(cleanText(link.ArtifactID)) +
		"&expires=" + url.QueryEscape(expiresText) +
		"&download_signature=" + url.QueryEscape(signature)
	return "/api/agent/install/download/" + cleanText(link.Platform) + "?" + query, expiresText, nil
}

func verifyAgentInstallDownloadQuery(ctx context.Context, auth *authService, values url.Values, platform string) (agentInstallLinkRecord, error) {
	if auth == nil || auth.verifier == nil {
		return agentInstallLinkRecord{}, errInvalidToken
	}
	siteID, ok := parseInt64Value(values.Get("site_id"))
	if !ok || siteID <= 0 {
		return agentInstallLinkRecord{}, errInvalidToken
	}
	artifactID := cleanText(values.Get("artifact"))
	expiresText := cleanText(values.Get("expires"))
	signature := cleanText(values.Get("download_signature"))
	platform = normalizeAgentInstallPlatform(platform)
	if artifactID == "" || expiresText == "" || signature == "" || platform == "" {
		return agentInstallLinkRecord{}, errInvalidToken
	}
	expiresAt, err := time.Parse(time.RFC3339, expiresText)
	if err != nil {
		return agentInstallLinkRecord{}, errInvalidToken
	}
	now := agentInstallDownloadNow(auth)
	if now.After(expiresAt) {
		return agentInstallLinkRecord{}, errExpiredToken
	}
	store, ok := auth.store.(agentInstallLinkStore)
	if !ok {
		return agentInstallLinkRecord{}, errInvalidToken
	}
	enrollmentCode, err := store.siteEnrollmentCode(ctx, siteID)
	if err != nil {
		return agentInstallLinkRecord{}, errInvalidToken
	}
	link, err := store.agentInstallLinkForDownload(ctx, siteID, platform, artifactID, expiresAt.Unix())
	if err != nil {
		return agentInstallLinkRecord{}, errInvalidToken
	}
	canonicalExpires := expiresAt.UTC().Truncate(time.Second).Format(time.RFC3339)
	if !auth.verifier.verifySignature(agentInstallDownloadSignatureInput(siteID, enrollmentCode, artifactID, platform, canonicalExpires, link.LinkNonce), signature) {
		return agentInstallLinkRecord{}, errInvalidToken
	}
	return link, nil
}

func agentInstallDownloadSignatureInput(siteID int64, enrollmentCode string, artifactID string, platform string, expiresText string, linkNonce string) []byte {
	parts := []string{
		agentInstallDownloadTokenType,
		strconv.FormatInt(siteID, 10),
		cleanText(artifactID),
		normalizeAgentInstallPlatform(platform),
		cleanText(expiresText),
		cleanText(linkNonce),
		sha256HexText(enrollmentCode),
	}
	return []byte(strings.Join(parts, "\n"))
}

func agentInstallDownloadNow(auth *authService) time.Time {
	if auth != nil && auth.verifier != nil && auth.verifier.now != nil {
		return auth.verifier.now()
	}
	return time.Now()
}

func agentInstallDownloadTokenTTL() time.Duration {
	value := strings.TrimSpace(os.Getenv("BOREALIS_AGENT_INSTALL_DOWNLOAD_TOKEN_TTL_SECONDS"))
	seconds := parseIntDefault(value, int(defaultAgentInstallDownloadTokenTTL/time.Second))
	if seconds < 300 {
		seconds = 300
	}
	if seconds > int((7*24*time.Hour)/time.Second) {
		seconds = int((7 * 24 * time.Hour) / time.Second)
	}
	return time.Duration(seconds) * time.Second
}

func platformArtifactBytes(artifactPath string, platform string) ([]byte, string, error) {
	relativePath := cleanText(requiredGoAgentArtifacts[platform])
	if relativePath == "" {
		return nil, "", fmt.Errorf("unsupported platform %s", platform)
	}
	reader, err := zip.OpenReader(artifactPath)
	if err != nil {
		return nil, "", err
	}
	defer reader.Close()
	body, ok := zipMemberBytes(reader.File, relativePath)
	if !ok || len(body) == 0 {
		return nil, "", fmt.Errorf("artifact missing %s", relativePath)
	}
	return body, filepath.Base(relativePath), nil
}

func sha256HexText(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return fmt.Sprintf("%x", sum[:])
}

func currentAgentInstallArtifactTarget() (map[string]any, string, bool) {
	settings := collectAgentReleaseChannelSettings()
	source := normalizeAgentReleaseBinarySource(settings["binary_source"], agentReleaseSourceGitHub)
	target := agentReleaseChannelTarget(settings, defaultAgentReleaseChannel)
	artifactID := cleanText(target["artifact_id"])
	artifactPath := agentUpdateArtifactPath(artifactID)
	cacheReady := source == agentReleaseSourceEngine
	if cacheReady && artifactPath != "" {
		if info, err := os.Stat(artifactPath); err == nil && !info.IsDir() {
			cacheReady = validateAgentReleaseArtifact(artifactPath) == nil
			if cacheReady && coerceInt64(target["compiled_at"]) == 0 {
				target["compiled_at"] = agentReleaseArtifactCompiledAt(artifactPath, info)
			}
		} else {
			cacheReady = false
		}
	}
	if source == agentReleaseSourceEngine {
		if desiredBuildID, err := engineAgentReleaseBuildID(); err == nil {
			target["desired_build_id"] = desiredBuildID
			targetBuildID := strings.ToLower(cleanText(target["build_id"]))
			target["agent_source_stale"] = desiredBuildID != "" && targetBuildID != "" && targetBuildID != strings.ToLower(cleanText(desiredBuildID))
		}
	}
	target["binary_source"] = source
	target["github_fallback_enabled"] = boolFromAny(settings["github_fallback_enabled"])
	target["last_refresh_started_at"] = coerceInt64(settings["last_refresh_started_at"])
	target["last_refresh_completed_at"] = coerceInt64(settings["last_refresh_completed_at"])
	target["last_refresh_error"] = cleanText(settings["last_refresh_error"])
	return target, artifactID, cacheReady
}

func agentInstallArtifactBuildStatus(source string, engineCacheReady bool, linkStoreReady bool, startedAt int64, completedAt int64, refreshError string, targetError string, sourceStale bool) string {
	if normalizeAgentReleaseBinarySource(source, agentReleaseSourceGitHub) != agentReleaseSourceEngine {
		return "external"
	}
	if startedAt > completedAt {
		return "compiling"
	}
	if sourceStale {
		return "stale"
	}
	if !engineCacheReady {
		if cleanText(refreshError) != "" || cleanText(targetError) != "" {
			return "error"
		}
		return "compiling"
	}
	if !linkStoreReady {
		return "link_state_unavailable"
	}
	return "ready"
}

func agentInstallDownloadPayload(auth *authService, baseURL string, enrollmentCode string, link agentInstallLinkRecord) map[string]any {
	path, expiresText, err := signAgentInstallDownloadPath(auth, enrollmentCode, link)
	if err != nil || path == "" {
		return nil
	}
	urlValue := path
	if baseURL != "" {
		urlValue = strings.TrimRight(baseURL, "/") + path
	}
	return map[string]any{
		"path":               path,
		"url":                urlValue,
		"platform":           link.Platform,
		"os":                 agentInstallOSID(link.Platform),
		"artifact":           link.ArtifactID,
		"artifact_id":        link.ArtifactID,
		"issued_at":          link.IssuedAt,
		"expires":            expiresText,
		"expires_at":         link.ExpiresAt,
		"revoked_at":         link.RevokedAt,
		"download_count":     link.DownloadCount,
		"last_downloaded_at": link.LastDownloadedAt,
		"active":             link.RevokedAt == 0 && link.ExpiresAt > agentInstallDownloadNow(auth).Unix(),
	}
}

func attachAgentInstallDownloads(r *http.Request, auth *authService, metadata map[string]any, sites []map[string]any) {
	if metadata == nil || len(sites) == 0 {
		return
	}
	target, artifactID, cacheReady := currentAgentInstallArtifactTarget()
	source := cleanText(target["binary_source"])
	var linkStore agentInstallLinkStore
	linkStoreReady := false
	if auth != nil && auth.store != nil {
		linkStore, linkStoreReady = auth.store.(agentInstallLinkStore)
	}
	engineCacheReady := cacheReady
	linksReady := engineCacheReady && linkStoreReady
	buildStartedAt := coerceInt64(target["last_refresh_started_at"])
	buildCompletedAt := coerceInt64(target["last_refresh_completed_at"])
	buildError := firstText(cleanText(target["last_refresh_error"]), cleanText(target["last_error"]))
	compiledAt := coerceInt64(target["compiled_at"])
	sourceStale := boolFromAny(target["agent_source_stale"])
	metadata["agent_binary_source"] = source
	metadata["agent_install_artifact"] = map[string]any{
		"available":               linksReady,
		"artifact_id":             artifactID,
		"artifact_sha256":         cleanText(target["artifact_sha256"]),
		"artifact_size":           coerceInt64(target["artifact_size"]),
		"binary_source":           source,
		"build_status":            agentInstallArtifactBuildStatus(source, engineCacheReady, linkStoreReady, buildStartedAt, buildCompletedAt, cleanText(target["last_refresh_error"]), cleanText(target["last_error"]), sourceStale),
		"build_started_at":        buildStartedAt,
		"build_completed_at":      buildCompletedAt,
		"build_error":             buildError,
		"compiled_at":             compiledAt,
		"agent_source_stale":      sourceStale,
		"desired_build_id":        cleanText(target["desired_build_id"]),
		"engine_cache_available":  engineCacheReady,
		"promoted_at":             coerceInt64(target["promoted_at"]),
		"refreshed_at":            coerceInt64(target["refreshed_at"]),
		"source":                  cleanText(target["source"]),
		"target_build_id":         cleanText(target["build_id"]),
		"version_label":           cleanText(target["version_label"]),
		"token_ttl_seconds":       int64(agentInstallDownloadTokenTTL() / time.Second),
		"github_fallback_enabled": boolFromAny(target["github_fallback_enabled"]),
		"link_state_available":    linkStoreReady,
	}
	if !linksReady {
		return
	}
	ctx := context.Background()
	if r != nil {
		ctx = r.Context()
	}
	baseURL := strings.TrimRight(cleanText(metadata["public_base_url"]), "/")
	expiresAt := agentInstallDownloadNow(auth).Add(agentInstallDownloadTokenTTL()).UTC().Truncate(time.Second).Unix()
	for _, site := range sites {
		siteID, ok := parseInt64Value(site["id"])
		if !ok || siteID <= 0 {
			continue
		}
		enrollmentCode := cleanText(site["enrollment_code"])
		if enrollmentCode == "" {
			continue
		}
		downloads := map[string]any{}
		for osID, platform := range map[string]string{
			"windows": "windows-amd64",
			"linux":   "linux-amd64",
		} {
			link, err := linkStore.ensureAgentInstallLink(ctx, siteID, platform, artifactID, expiresAt)
			if err != nil {
				continue
			}
			download := agentInstallDownloadPayload(auth, baseURL, enrollmentCode, link)
			if len(download) == 0 {
				continue
			}
			download["compiled_at"] = compiledAt
			download["os"] = osID
			downloads[osID] = download
		}
		if len(downloads) > 0 {
			site["agent_install_downloads"] = downloads
		}
	}
}
