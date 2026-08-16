package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeRDPCredentialStore struct {
	profile    operatorProfile
	credential map[string]any
	found      bool
	err        error
}

func (s *fakeRDPCredentialStore) lookupOperator(_ context.Context, username string, fallbackRole string) (operatorProfile, error) {
	profile := s.profile
	if profile.Username == "" {
		profile.Username = username
	}
	if profile.Role == "" {
		profile.Role = fallbackRole
	}
	return profile, nil
}

func (s *fakeRDPCredentialStore) loadDecryptedSchedulerCredential(_ context.Context, _ authSecretService, _ int64) (map[string]any, bool, error) {
	return copyMap(s.credential), s.found, s.err
}

func TestValidateRDPCredentialSplitsDomainUsernameAndPreservesPassword(t *testing.T) {
	credential, err := validateRDPCredential(`LAB\nicole`, " password with spaces ", "LAB")
	if err != nil {
		t.Fatalf("validateRDPCredential returned error: %v", err)
	}
	if credential.Domain != "LAB" || credential.Username != "nicole" {
		t.Fatalf("unexpected identity %#v", credential)
	}
	if credential.Password != " password with spaces " {
		t.Fatalf("password changed during validation")
	}
}

func TestValidateRDPCredentialRejectsConflictingDomain(t *testing.T) {
	_, err := validateRDPCredential(`LAB\nicole`, "secret", "OTHER")
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("expected domain conflict, got %v", err)
	}
}

func TestValidateRDPCredentialEnforcesFieldLimits(t *testing.T) {
	_, err := validateRDPCredential(strings.Repeat("u", maxRDPUsernameLength+1), "secret", "")
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected username length error, got %v", err)
	}
	_, err = validateRDPCredential("nicole", strings.Repeat("p", maxRDPPasswordLength+1), "")
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected password length error, got %v", err)
	}
}

func TestValidateRDPRequestCredentialInputRejectsInvalidShapes(t *testing.T) {
	for _, body := range []map[string]any{
		{"credential_id": float64(42.5)},
		{"rdp_username": float64(42), "rdp_password": "secret"},
		{"rdp_username": "nicole", "rdp_password": "line\nbreak"},
	} {
		if err := validateRDPRequestCredentialInput(body); err == nil {
			t.Fatalf("expected invalid request body rejection for %#v", body)
		}
	}
}

func TestValidateRDPDisplayInputAcceptsViewportAndRejectsInvalidShapes(t *testing.T) {
	if err := validateRDPDisplayInput(map[string]any{"width": float64(2298), "height": float64(1214), "dpi": float64(96)}); err != nil {
		t.Fatalf("expected viewport dimensions to pass validation: %v", err)
	}
	for _, body := range []map[string]any{
		{"width": float64(319)},
		{"height": float64(8193)},
		{"dpi": float64(96.5)},
		{"width": "wide"},
	} {
		if err := validateRDPDisplayInput(body); err == nil {
			t.Fatalf("expected invalid display body rejection for %#v", body)
		}
	}
}

func TestRDPDisplayDimensionUsesViewportAndSafeFallback(t *testing.T) {
	if got := rdpDisplayDimension(float64(2298), defaultRDPDisplayWidth, minRDPDisplayWidth, maxRDPDisplayDimension); got != 2298 {
		t.Fatalf("expected viewport width 2298, got %d", got)
	}
	if got := rdpDisplayDimension(float64(120), defaultRDPDisplayWidth, minRDPDisplayWidth, maxRDPDisplayDimension); got != defaultRDPDisplayWidth {
		t.Fatalf("expected invalid viewport width fallback %d, got %d", defaultRDPDisplayWidth, got)
	}
}

func TestResolveRDPCredentialAcceptsGlobalWindowsCredential(t *testing.T) {
	store := &fakeRDPCredentialStore{
		found: true,
		credential: map[string]any{
			"id":              int64(42),
			"name":            "LAB Domain Admin",
			"site_id":         nil,
			"credential_type": "domain",
			"connection_type": "windows",
			"username":        `LAB\nicole`,
			"password":        "secret",
		},
	}
	runtime := &rdpRuntime{auth: &authService{store: store, aegis: &authLoginTestAegis{unlockedCipher: "ready"}}}
	siteID := int64(7)
	credential, status, payloadErr := runtime.resolveCredential(context.Background(), remoteOpsSessionDevice{SiteID: &siteID}, map[string]any{"credential_id": float64(42)})
	if payloadErr != nil || status != http.StatusOK {
		t.Fatalf("unexpected resolve result status=%d error=%#v", status, payloadErr)
	}
	if credential.CredentialID != 42 || credential.Username != "nicole" || credential.Domain != "LAB" || credential.Password != "secret" {
		t.Fatalf("unexpected credential %#v", credential)
	}
}

func TestResolveRDPCredentialRejectsDifferentSite(t *testing.T) {
	store := &fakeRDPCredentialStore{
		found: true,
		credential: map[string]any{
			"id":              int64(42),
			"site_id":         int64(9),
			"credential_type": "machine",
			"connection_type": "winrm",
			"username":        "Administrator",
			"password":        "secret",
		},
	}
	runtime := &rdpRuntime{auth: &authService{store: store, aegis: &authLoginTestAegis{unlockedCipher: "ready"}}}
	siteID := int64(7)
	_, status, payloadErr := runtime.resolveCredential(context.Background(), remoteOpsSessionDevice{SiteID: &siteID}, map[string]any{"credential_id": float64(42)})
	if status != http.StatusForbidden || cleanText(payloadErr["error"]) != "credential_site_mismatch" {
		t.Fatalf("unexpected resolve result status=%d error=%#v", status, payloadErr)
	}
}

func TestRequestRDPStartReadyUsesRDPAgentEvent(t *testing.T) {
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode worker request: %v", err)
		}
		if body["event_name"] != "rdp_start" || body["hostname"] != "LAB-AIO-01" {
			t.Fatalf("unexpected worker body %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"called":   true,
			"response": map[string]any{"status": "ok", "ready": true},
		})
	}))
	defer worker.Close()

	response, status, workerErr := requestRDPStartReady(
		context.Background(),
		vncTestAuth(&fakeVNCStore{}),
		routeForTestWorker(t, worker.URL),
		"LAB-AIO-01",
		"system",
		map[string]any{"agent_id": "LAB-AIO-01_SYSTEM", "rdp_port": 3389},
		1,
	)
	if workerErr != nil || status != http.StatusOK || response["ready"] != true {
		t.Fatalf("unexpected response status=%d response=%#v error=%#v", status, response, workerErr)
	}
}
