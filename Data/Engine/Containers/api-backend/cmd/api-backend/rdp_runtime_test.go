package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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
		if body["timeout_seconds"] != float64(1) {
			t.Fatalf("unexpected timeout %#v", body["timeout_seconds"])
		}
		payload := body["payload"].(map[string]any)
		if payload["timeout_seconds"] != float64(1) {
			t.Fatalf("Agent payload missing readiness timeout %#v", payload)
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

func TestRDPSlowDeviceBudgets(t *testing.T) {
	if defaultRDPStartReadyWaitSeconds != 60 {
		t.Fatalf("RDP Agent readiness budget must allow 60 seconds")
	}
	if defaultRDPEstablishDeadlineSeconds != 75 {
		t.Fatalf("RDP establish budget must preserve post-readiness overhead")
	}
}

func TestRDPTransportFailureForcesAgentReconciliationThenRetries(t *testing.T) {
	var recoveryCalls atomic.Int32
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode worker request: %v", err)
		}
		payload := body["payload"].(map[string]any)
		if payload["reason"] != "rdp_transport_recovery" {
			t.Fatalf("unexpected recovery payload %#v", payload)
		}
		recoveryCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"called":   true,
			"response": map[string]any{"status": "ok", "ready": true},
		})
	}))
	defer worker.Close()

	waitCalls := 0
	waiter := func(_ string, _ int, _ float64, _ float64) bool {
		waitCalls++
		return waitCalls == 2
	}
	payloadErr, status := ensureRDPWireGuardTransport(
		context.Background(),
		vncTestAuth(&fakeVNCStore{}),
		routeForTestWorker(t, worker.URL),
		"LAB-DC-01",
		"system",
		map[string]any{"agent_id": "LAB-DC-01_SYSTEM", "reason": "rdp_establish"},
		"10.255.0.100",
		3389,
		waiter,
	)
	if payloadErr != nil || status != 0 || waitCalls != 2 || recoveryCalls.Load() != 1 {
		t.Fatalf("unexpected recovery result status=%d error=%#v wait_calls=%d recovery_calls=%d", status, payloadErr, waitCalls, recoveryCalls.Load())
	}
}

func TestRDPTransportFailureReturnsAfterSingleRecovery(t *testing.T) {
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"called":   true,
			"response": map[string]any{"status": "ok", "ready": true},
		})
	}))
	defer worker.Close()

	waitCalls := 0
	payloadErr, status := ensureRDPWireGuardTransport(
		context.Background(),
		vncTestAuth(&fakeVNCStore{}),
		routeForTestWorker(t, worker.URL),
		"LAB-DC-01",
		"system",
		map[string]any{"agent_id": "LAB-DC-01_SYSTEM"},
		"10.255.0.100",
		3389,
		func(_ string, _ int, _ float64, _ float64) bool { waitCalls++; return false },
	)
	if status != http.StatusServiceUnavailable || cleanText(payloadErr["error"]) != "rdp_backend_not_ready" || waitCalls != 2 {
		t.Fatalf("unexpected failed recovery status=%d error=%#v wait_calls=%d", status, payloadErr, waitCalls)
	}
}

func TestRDPTransportProbeBudgetHonorsEstablishDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	budget := rdpTransportProbeBudget(ctx, 5)
	if budget <= 0 || budget > 0.05 {
		t.Fatalf("unexpected bounded probe budget %f", budget)
	}
	if budget := rdpTransportProbeBudget(context.Background(), 5); budget != 5 {
		t.Fatalf("unexpected unbounded probe budget %f", budget)
	}
}
