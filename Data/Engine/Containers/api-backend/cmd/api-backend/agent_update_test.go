package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type agentUpdateTestStore struct {
	deviceAuthRecord deviceBearerAuthRecord
	deviceAuthFound  bool
	requiredVersion  *int
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

func TestResolveEffectiveAgentReleaseChannelPinsNoOverrideToStable(t *testing.T) {
	settings := map[string]any{"default_channel": "unstable"}
	if got := resolveEffectiveAgentReleaseChannel(settings, ""); got != "stable" {
		t.Fatalf("expected no override to resolve stable, got %q", got)
	}
	if got := resolveEffectiveAgentReleaseChannel(settings, "unstable"); got != "unstable" {
		t.Fatalf("expected explicit override to remain unstable, got %q", got)
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
