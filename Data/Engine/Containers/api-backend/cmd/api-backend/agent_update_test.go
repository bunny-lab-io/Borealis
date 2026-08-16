package main

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func configureTestAgentArtifact(t *testing.T, root, artifactID string, compiledAt int64) string {
	t.Helper()
	t.Setenv("BOREALIS_PROJECT_ROOT", root)
	settingsPath := filepath.Join(root, "Engine", "Services", "api-backend", "config", "agent_artifact.json")
	t.Setenv("BOREALIS_AGENT_ARTIFACT_PATH", settingsPath)
	artifactPath := agentUpdateArtifactPath(artifactID)
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o700); err != nil {
		t.Fatal(err)
	}
	handle, err := os.Create(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(handle)
	entries := map[string][]byte{
		"manifest.json":                           []byte(fmt.Sprintf(`{"artifact_format":%q,"compiled_at":%d}`, agentUpdateArtifactFormat, compiledAt)),
		"Data/Agent/dist/linux-amd64/Agent":       []byte("linux-agent"),
		"Data/Agent/dist/windows-amd64/Agent.exe": []byte("windows-agent"),
	}
	for name, body := range entries {
		writer, createErr := archive.Create(name)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, writeErr := writer.Write(body); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{
		"version": 1,
		"source":  agentArtifactSourceEngine,
		"artifact": map[string]any{
			"source":             agentArtifactSourceEngine,
			"build_id":           "build",
			"artifact_id":        artifactID,
			"artifact_path":      artifactPath,
			"artifact_sha256":    sha256File(artifactPath),
			"artifact_size":      info.Size(),
			"artifact_format":    agentUpdateArtifactFormat,
			"platform_artifacts": requiredGoAgentArtifacts,
			"compiled_at":        compiledAt,
		},
	}
	body, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return artifactPath
}

type agentUpdateTestStore struct {
	deviceAuthRecord deviceBearerAuthRecord
	deviceAuthFound  bool
	requiredVersion  *int
	siteEnrollment   string
	linkNow          int64
	nextLinkID       int64
	links            map[int64]agentInstallLinkRecord
	activeLinks      map[string]int64
	manifestGUID     string
	manifestHostname string
	manifestBuildID  string
	manifestPayload  map[string]any
	manifestStatus   int
	manifestErr      error
}

func (s *agentUpdateTestStore) lookupOperator(_ context.Context, username string, fallbackRole string) (operatorProfile, error) {
	return operatorProfile{Username: username, Role: fallbackRole}, nil
}

func (s *agentUpdateTestStore) requiredDeviceTokenVersion(_ context.Context, _ string) (*int, error) {
	return s.requiredVersion, nil
}

func (s *agentUpdateTestStore) deviceBearerAuthRecord(_ context.Context, _ string) (deviceBearerAuthRecord, bool, error) {
	if !s.deviceAuthFound {
		return deviceBearerAuthRecord{}, false, nil
	}
	return s.deviceAuthRecord, true, nil
}

func (s *agentUpdateTestStore) agentUpdateManifest(_ context.Context, guid, hostname, installedBuildID string) (map[string]any, int, error) {
	s.manifestGUID = guid
	s.manifestHostname = hostname
	s.manifestBuildID = installedBuildID
	if s.manifestErr != nil {
		status := s.manifestStatus
		if status == 0 {
			status = http.StatusServiceUnavailable
		}
		return nil, status, s.manifestErr
	}
	status := s.manifestStatus
	if status == 0 {
		status = http.StatusOK
	}
	return copyMap(s.manifestPayload), status, nil
}

func (s *agentUpdateTestStore) siteEnrollmentCode(_ context.Context, _ int64) (string, error) {
	if s.siteEnrollment == "" {
		return "", sql.ErrNoRows
	}
	return s.siteEnrollment, nil
}

func (s *agentUpdateTestStore) ensureAgentInstallLink(_ context.Context, siteID int64, platform string, artifactID string, expiresAt int64) (agentInstallLinkRecord, error) {
	s.ensureLinkMaps()
	platform = normalizeAgentInstallPlatform(platform)
	key := s.linkKey(siteID, platform)
	now := s.now()
	if id := s.activeLinks[key]; id > 0 {
		link := s.links[id]
		if link.ArtifactID == artifactID && link.ExpiresAt > now && link.RevokedAt == 0 {
			return link, nil
		}
		link.RevokedAt = now
		s.links[id] = link
		delete(s.activeLinks, key)
	}
	return s.insertLink(siteID, platform, artifactID, expiresAt), nil
}

func (s *agentUpdateTestStore) agentInstallLinkForDownload(_ context.Context, siteID int64, platform string, artifactID string, expiresAt int64) (agentInstallLinkRecord, error) {
	s.ensureLinkMaps()
	platform = normalizeAgentInstallPlatform(platform)
	id := s.activeLinks[s.linkKey(siteID, platform)]
	if id <= 0 {
		return agentInstallLinkRecord{}, errInvalidToken
	}
	link := s.links[id]
	if link.ArtifactID != artifactID || link.ExpiresAt != expiresAt || link.RevokedAt != 0 || link.ExpiresAt <= s.now() {
		return agentInstallLinkRecord{}, errInvalidToken
	}
	return link, nil
}

func (s *agentUpdateTestStore) recordAgentInstallLinkDownload(_ context.Context, linkID int64) error {
	s.ensureLinkMaps()
	link := s.links[linkID]
	if link.ID <= 0 || link.RevokedAt != 0 {
		return nil
	}
	link.DownloadCount++
	link.LastDownloadedAt = s.now()
	s.links[linkID] = link
	return nil
}

func (s *agentUpdateTestStore) revokeAgentInstallLink(_ context.Context, siteID int64, platform string, artifactID string, expiresAt int64) (agentInstallLinkRecord, error) {
	s.ensureLinkMaps()
	platform = normalizeAgentInstallPlatform(platform)
	key := s.linkKey(siteID, platform)
	if id := s.activeLinks[key]; id > 0 {
		link := s.links[id]
		link.RevokedAt = s.now()
		s.links[id] = link
		delete(s.activeLinks, key)
	}
	return s.insertLink(siteID, platform, artifactID, expiresAt), nil
}

func (s *agentUpdateTestStore) ensureLinkMaps() {
	if s.links == nil {
		s.links = map[int64]agentInstallLinkRecord{}
	}
	if s.activeLinks == nil {
		s.activeLinks = map[string]int64{}
	}
	if s.nextLinkID == 0 {
		s.nextLinkID = 1
	}
}

func (s *agentUpdateTestStore) insertLink(siteID int64, platform string, artifactID string, expiresAt int64) agentInstallLinkRecord {
	s.ensureLinkMaps()
	id := s.nextLinkID
	s.nextLinkID++
	link := agentInstallLinkRecord{
		ID:         id,
		SiteID:     siteID,
		Platform:   normalizeAgentInstallPlatform(platform),
		ArtifactID: cleanText(artifactID),
		LinkNonce:  "nonce-" + strconv.FormatInt(id, 10),
		IssuedAt:   s.now(),
		ExpiresAt:  expiresAt,
	}
	s.links[id] = link
	s.activeLinks[s.linkKey(siteID, link.Platform)] = id
	return link
}

func (s *agentUpdateTestStore) linkKey(siteID int64, platform string) string {
	return strconv.FormatInt(siteID, 10) + ":" + normalizeAgentInstallPlatform(platform)
}

func (s *agentUpdateTestStore) now() int64 {
	if s.linkNow > 0 {
		return s.linkNow
	}
	return 1700000000
}

func TestAgentUpdateManifestHandlerReturnsAuthenticatedManifest(t *testing.T) {
	guid := "2540DA38-E2B1-45B9-9113-BF7CF0E1778A"
	signer := testAgentJWTSigner(t)
	signer.now = func() time.Time { return time.Unix(1700000000, 0) }
	token, err := signer.issueAccessToken(guid, "fingerprint", 4, agentAccessTokenTTL)
	if err != nil {
		t.Fatal(err)
	}
	store := &agentUpdateTestStore{
		deviceAuthFound: true,
		deviceAuthRecord: deviceBearerAuthRecord{
			GUID:         guid,
			Fingerprint:  "fingerprint",
			TokenVersion: 4,
			Status:       "active",
		},
		manifestPayload: map[string]any{
			"status":           "ok",
			"guid":             guid,
			"hostname":         "LAB-OPERATOR-01",
			"target_build_id":  "build-next",
			"update_available": true,
			"artifact_id":      "unstable-build-next",
			"download_path":    "/api/agent/update/download/unstable-build-next",
		},
	}
	auth := &authService{store: store, timeout: time.Second}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/agent/update/manifest?hostname=LAB-OPERATOR-01&current_build_id=build-old&installed_build_id=build-current", nil)
	request.Header.Set("Authorization", "Bearer "+token)

	agentUpdateManifestHandler(auth, signer, &dpopVerifier{seenJTI: map[string]time.Time{}}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.manifestGUID != guid || store.manifestHostname != "LAB-OPERATOR-01" || store.manifestBuildID != "build-current" {
		t.Fatalf("unexpected manifest request guid=%s hostname=%s build=%s", store.manifestGUID, store.manifestHostname, store.manifestBuildID)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["target_build_id"] != "build-next" || payload["download_path"] != "/api/agent/update/download/unstable-build-next" {
		t.Fatalf("unexpected manifest payload %+v", payload)
	}
}

func TestAgentUpdateDownloadHandlerServesCachedArtifact(t *testing.T) {
	guid := "2540DA38-E2B1-45B9-9113-BF7CF0E1778A"
	signer := testAgentJWTSigner(t)
	signer.now = func() time.Time { return time.Unix(1700000000, 0) }
	token, err := signer.issueAccessToken(guid, "fingerprint", 4, agentAccessTokenTTL)
	if err != nil {
		t.Fatal(err)
	}
	store := &agentUpdateTestStore{
		deviceAuthFound: true,
		deviceAuthRecord: deviceBearerAuthRecord{
			GUID:         guid,
			Fingerprint:  "fingerprint",
			TokenVersion: 4,
			Status:       "active",
		},
	}
	root := t.TempDir()
	t.Setenv("BOREALIS_PROJECT_ROOT", root)
	artifactDir := filepath.Join(root, "Engine", "Services", "api-backend", "cache", "AgentUpdates")
	if err := os.MkdirAll(artifactDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifactDir, "stable-build.zip"), []byte("zip-payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	auth := &authService{store: store, timeout: time.Second}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/agent/update/download/stable-build", nil)
	request.SetPathValue("artifact_id", "stable-build")
	request.Header.Set("Authorization", "Bearer "+token)

	agentUpdateDownloadHandler(auth, signer, &dpopVerifier{seenJTI: map[string]time.Time{}}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("unexpected content type %s", recorder.Header().Get("Content-Type"))
	}
	if recorder.Body.String() != "zip-payload" {
		t.Fatalf("unexpected artifact body %q", recorder.Body.String())
	}
}

func TestAgentInstallDownloadHandlerServesSignedPlatformArtifact(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BOREALIS_AGENT_INSTALL_DOWNLOAD_TOKEN_TTL_SECONDS", "3600")
	artifactID := "engine-build"
	configureTestAgentArtifact(t, root, artifactID, 1700000300)
	store := &agentUpdateTestStore{siteEnrollment: "CODE-1234", linkNow: 1700000000}
	auth := &authService{
		verifier: &tokenVerifier{
			secret: []byte("test-secret"),
			maxAge: time.Hour,
			now:    func() time.Time { return time.Unix(1700000000, 0) },
		},
		store:   store,
		timeout: time.Second,
	}
	link, err := store.ensureAgentInstallLink(context.Background(), 7, "linux-amd64", artifactID, 1700003600)
	if err != nil {
		t.Fatal(err)
	}
	path, _, err := signAgentInstallDownloadPath(auth, "CODE-1234", link)
	if err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.SetPathValue("platform", "linux-amd64")
	agentInstallDownloadHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Body.String() != "linux-agent" {
		t.Fatalf("unexpected body %q", recorder.Body.String())
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("expected no-store, got %q", got)
	}
	updated := store.links[link.ID]
	if updated.DownloadCount != 1 || updated.LastDownloadedAt != 1700000000 {
		t.Fatalf("expected successful download counter update, got %+v", updated)
	}

	tampered := httptest.NewRecorder()
	tamperedRequest := httptest.NewRequest(http.MethodGet, strings.Replace(path, "artifact="+artifactID, "artifact=other-build", 1), nil)
	tamperedRequest.SetPathValue("platform", "linux-amd64")
	agentInstallDownloadHandler(auth).ServeHTTP(tampered, tamperedRequest)
	if tampered.Code != http.StatusUnauthorized {
		t.Fatalf("expected tampered query to fail, got %d body=%s", tampered.Code, tampered.Body.String())
	}
	if store.links[link.ID].DownloadCount != 1 {
		t.Fatalf("tampered request should not increment counter, got %+v", store.links[link.ID])
	}
}

func TestAttachAgentInstallDownloadsAddsSignedSiteURLs(t *testing.T) {
	root := t.TempDir()
	artifactID := "engine-build"
	configureTestAgentArtifact(t, root, artifactID, 1700000300)
	store := &agentUpdateTestStore{siteEnrollment: "CODE-1234", linkNow: 1700000000}
	auth := &authService{
		verifier: &tokenVerifier{
			secret: []byte("test-secret"),
			now:    func() time.Time { return time.Unix(1700000000, 0) },
		},
		store: store,
	}
	metadata := map[string]any{"public_base_url": "https://borealis.example.com"}
	sites := []map[string]any{{"id": int64(7), "enrollment_code": "CODE-1234"}}

	attachAgentInstallDownloads(nil, auth, metadata, sites)

	if metadata["agent_binary_source"] != agentArtifactSourceEngine {
		t.Fatalf("expected engine source metadata, got %#v", metadata["agent_binary_source"])
	}
	artifact := metadata["agent_install_artifact"].(map[string]any)
	if artifact["available"] != true {
		t.Fatalf("expected available artifact metadata, got %+v", artifact)
	}
	if artifact["engine_cache_available"] != true || artifact["link_state_available"] != true || artifact["build_status"] != "ready" {
		t.Fatalf("expected ready engine install artifact metadata, got %+v", artifact)
	}
	if artifact["compiled_at"] != int64(1700000300) {
		t.Fatalf("expected compile timestamp from artifact metadata, got %+v", artifact)
	}
	downloads := sites[0]["agent_install_downloads"].(map[string]any)
	linux := downloads["linux"].(map[string]any)
	if linux["compiled_at"] != int64(1700000300) {
		t.Fatalf("expected link compile timestamp from artifact metadata, got %+v", linux)
	}
	path := cleanText(linux["path"])
	if !strings.HasPrefix(path, "/api/agent/install/download/linux-amd64?") {
		t.Fatalf("unexpected linux download path %q", path)
	}
	if got := cleanText(linux["url"]); got != "https://borealis.example.com"+path {
		t.Fatalf("unexpected linux download url %q", got)
	}
	if !strings.Contains(path, "site_id=7") || !strings.Contains(path, "artifact="+artifactID) || !strings.Contains(path, "expires=") || !strings.Contains(path, "download_signature=") {
		t.Fatalf("expected readable signed query, got %q", path)
	}
	parsedRequest := httptest.NewRequest(http.MethodGet, path, nil)
	verifiedLink, err := verifyAgentInstallDownloadQuery(context.Background(), auth, parsedRequest.URL.Query(), "linux-amd64")
	if err != nil {
		t.Fatalf("expected signed query to verify: %v", err)
	}
	if verifiedLink.ArtifactID != artifactID {
		t.Fatalf("expected artifact id %s, got %s", artifactID, verifiedLink.ArtifactID)
	}
	firstLinkID := verifiedLink.ID
	attachAgentInstallDownloads(nil, auth, metadata, sites)
	parsedAgain := httptest.NewRequest(http.MethodGet, cleanText(sites[0]["agent_install_downloads"].(map[string]any)["linux"].(map[string]any)["path"]), nil)
	reusedLink, err := verifyAgentInstallDownloadQuery(context.Background(), auth, parsedAgain.URL.Query(), "linux-amd64")
	if err != nil {
		t.Fatalf("expected reused link to verify: %v", err)
	}
	if reusedLink.ID != firstLinkID {
		t.Fatalf("expected active link reuse id=%d, got %d", firstLinkID, reusedLink.ID)
	}
}

func TestSiteAgentInstallLinkRevokeReplacesOnlyRequestedPlatform(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BOREALIS_AGENT_INSTALL_DOWNLOAD_TOKEN_TTL_SECONDS", "3600")
	artifactID := "engine-build"
	configureTestAgentArtifact(t, root, artifactID, 1700000300)
	store := &agentUpdateTestStore{siteEnrollment: "CODE-1234", linkNow: 1700000000}
	auth := &authService{
		verifier: &tokenVerifier{
			secret: []byte("test-secret"),
			maxAge: time.Hour,
			now:    func() time.Time { return time.Unix(1700000010, 0) },
		},
		store:   store,
		timeout: time.Second,
	}
	oldLinux, err := store.ensureAgentInstallLink(context.Background(), 7, "linux-amd64", artifactID, 1700003600)
	if err != nil {
		t.Fatal(err)
	}
	oldWindows, err := store.ensureAgentInstallLink(context.Background(), 7, "windows-amd64", artifactID, 1700003600)
	if err != nil {
		t.Fatal(err)
	}
	oldLinuxPath, _, err := signAgentInstallDownloadPath(auth, "CODE-1234", oldLinux)
	if err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/sites/7/agent-install-links/linux-amd64/revoke", nil)
	request.SetPathValue("site_id", "7")
	request.SetPathValue("platform", "linux-amd64")
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	siteAgentInstallLinkRevokeHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	download, ok := payload["download"].(map[string]any)
	if !ok {
		t.Fatalf("expected replacement download payload, got %+v", payload)
	}
	if payload["platform"] != "linux-amd64" || payload["os"] != "linux" {
		t.Fatalf("unexpected platform payload %+v", payload)
	}
	newPath := cleanText(download["path"])
	if newPath == "" || newPath == oldLinuxPath {
		t.Fatalf("expected new replacement path, old=%q new=%q", oldLinuxPath, newPath)
	}
	if download["download_count"] != float64(0) && download["download_count"] != int64(0) {
		t.Fatalf("expected new counter at zero, got %+v", download)
	}
	if store.links[oldLinux.ID].RevokedAt == 0 {
		t.Fatalf("expected old linux link revoked: %+v", store.links[oldLinux.ID])
	}
	if store.links[oldWindows.ID].RevokedAt != 0 || store.activeLinks[store.linkKey(7, "windows-amd64")] != oldWindows.ID {
		t.Fatalf("windows link should remain active: %+v", store.links[oldWindows.ID])
	}

	oldDownload := httptest.NewRecorder()
	oldRequest := httptest.NewRequest(http.MethodGet, oldLinuxPath, nil)
	oldRequest.SetPathValue("platform", "linux-amd64")
	agentInstallDownloadHandler(auth).ServeHTTP(oldDownload, oldRequest)
	if oldDownload.Code != http.StatusUnauthorized {
		t.Fatalf("expected old revoked URL to fail, got %d body=%s", oldDownload.Code, oldDownload.Body.String())
	}
}
