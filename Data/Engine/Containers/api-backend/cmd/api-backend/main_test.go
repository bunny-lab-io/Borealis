package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const testAuthToken = "eyJ1Ijoib3BlcmF0b3IiLCJyIjoiQWRtaW4iLCJ0cyI6MTcwMDAwMDAwMH0.ZVPxAA.T_nkD4f7np9iU74bxSttSuR_MoY"
const testCompressedAuthToken = ".eJyrVipVslJKySxKTS7JL6rUKy1OLVLSUSoCCoZCmCXFSlaG5gZQUAsAhqAOag.ZVPxAA.-Zu3AisDtRhgTd33co1kzyxIQqw"

type fakeOperatorStore struct {
	profiles map[string]operatorProfile
	err      error
}

func (s fakeOperatorStore) lookupOperator(_ context.Context, username string, fallbackRole string) (operatorProfile, error) {
	if s.err != nil {
		return operatorProfile{}, s.err
	}
	profile, ok := s.profiles[strings.ToLower(username)]
	if !ok {
		return operatorProfile{}, errOperatorNotFound
	}
	if profile.Role == "" {
		profile.Role = fallbackRole
	}
	return profile, nil
}

func testAuthService(profile operatorProfile) *authService {
	if profile.Username == "" {
		profile.Username = "operator"
	}
	if profile.DisplayName == "" {
		profile.DisplayName = profile.Username
	}
	if profile.Role == "" {
		profile.Role = "Admin"
	}
	if profile.AuthSource == "" {
		profile.AuthSource = "local"
	}
	return &authService{
		verifier: &tokenVerifier{
			secret: []byte("test-secret"),
			maxAge: time.Hour,
			now:    func() time.Time { return time.Unix(1700000010, 0) },
		},
		store: fakeOperatorStore{
			profiles: map[string]operatorProfile{
				strings.ToLower(profile.Username): profile,
			},
		},
		timeout: time.Second,
	}
}

func TestSetEnvReplacesExistingValue(t *testing.T) {
	env := setEnv([]string{"PATH=/bin", "BOREALIS_ENGINE_PORT=5000"}, "BOREALIS_ENGINE_PORT", "5001")
	got := strings.Join(env, "\n")
	if strings.Count(got, "BOREALIS_ENGINE_PORT=") != 1 {
		t.Fatalf("expected one BOREALIS_ENGINE_PORT entry, got %q", got)
	}
	if !strings.Contains(got, "BOREALIS_ENGINE_PORT=5001") {
		t.Fatalf("expected replaced port, got %q", got)
	}
}

func TestEnvDurationSecondsRejectsInvalidValues(t *testing.T) {
	t.Setenv("BOREALIS_TEST_DURATION", "-1")
	if got := envDurationSeconds("BOREALIS_TEST_DURATION", 3*time.Second); got != 3*time.Second {
		t.Fatalf("expected fallback for negative duration, got %s", got)
	}
	t.Setenv("BOREALIS_TEST_DURATION", "1.5")
	if got := envDurationSeconds("BOREALIS_TEST_DURATION", 3*time.Second); got != 1500*time.Millisecond {
		t.Fatalf("expected parsed duration, got %s", got)
	}
}

func TestHealthHandlerReportsHealthyLegacyBackend(t *testing.T) {
	legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Fatalf("unexpected legacy path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer legacy.Close()

	legacyURL, err := url.Parse(legacy.URL)
	if err != nil {
		t.Fatal(err)
	}
	cfg := gatewayConfig{LegacyURL: legacyURL, HealthTimeout: time.Second}
	state := &legacyState{}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	healthHandler(cfg, state).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !state.snapshot().Healthy {
		t.Fatalf("expected state marked healthy")
	}
}

func TestHealthHandlerReportsUnhealthyLegacyBackend(t *testing.T) {
	legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer legacy.Close()

	legacyURL, err := url.Parse(legacy.URL)
	if err != nil {
		t.Fatal(err)
	}
	cfg := gatewayConfig{LegacyURL: legacyURL, HealthTimeout: time.Second}
	state := &legacyState{}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	healthHandler(cfg, state).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if state.snapshot().Healthy {
		t.Fatalf("expected state marked unhealthy")
	}
}

func TestTokenVerifierAcceptsItsDangerousFixture(t *testing.T) {
	verifier := &tokenVerifier{
		secret: []byte("test-secret"),
		maxAge: time.Hour,
		now:    func() time.Time { return time.Unix(1700000010, 0) },
	}

	identity, err := verifier.verify(testAuthToken)
	if err != nil {
		t.Fatalf("expected valid token, got %v", err)
	}
	if identity.Username != "operator" || identity.Role != "Admin" {
		t.Fatalf("unexpected identity %+v", identity)
	}

	identity, err = verifier.verify(testCompressedAuthToken)
	if err != nil {
		t.Fatalf("expected compressed token valid, got %v", err)
	}
	if identity.Username != "directory.user" || identity.Role != "User" {
		t.Fatalf("unexpected compressed identity %+v", identity)
	}
}

func TestAuthMeHandlerReturnsOperatorProfile(t *testing.T) {
	auth := testAuthService(operatorProfile{
		Username:            "operator",
		DisplayName:         "Operator",
		Role:                "Admin",
		MFAEnabled:          true,
		PasskeyCount:        2,
		AuthSource:          "local",
		DirectoryProviderID: 0,
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	authMeHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["username"] != "operator" || payload["role"] != "Admin" || payload["passkey_count"].(float64) != 2 {
		t.Fatalf("unexpected /api/auth/me payload %+v", payload)
	}
}

func TestServerTimeHandlerRequiresAuthentication(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/server/time", nil)
	serverTimeHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "Authentication required") {
		t.Fatalf("expected normalized auth message, got %s", recorder.Body.String())
	}
}

func TestServerTimezonesHandlerRequiresAdmin(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "User"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/server/timezones", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	serverTimezonesHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "Administrator permissions") {
		t.Fatalf("expected admin auth message, got %s", recorder.Body.String())
	}
}

func TestServerTimeHandlerReturnsNativePayload(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/server/time", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	serverTimeHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"epoch", "iso", "utc", "timezone", "timezone_id", "display"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("expected payload key %q in %+v", key, payload)
		}
	}
}

func TestSerializeServerTimeMatchesPythonShape(t *testing.T) {
	location := time.FixedZone("MST", -7*60*60)
	nowLocal := time.Date(2026, 6, 2, 13, 4, 5, 123456789, location)
	nowUTC := nowLocal.UTC()

	payload := serializeServerTime(nowLocal, nowUTC, "America/Denver")

	if got := payload["iso"]; got != "2026-06-02T13:04:05.123456-07:00" {
		t.Fatalf("unexpected local iso %q", got)
	}
	if got := payload["utc"]; got != "2026-06-02T20:04:05.123456+00:00" {
		t.Fatalf("unexpected utc iso %q", got)
	}
	if got := payload["display"]; got != "2026-06-02 13:04:05 MST" {
		t.Fatalf("unexpected display %q", got)
	}
	if got := payload["timezone_id"]; got != "America/Denver" {
		t.Fatalf("unexpected timezone id %q", got)
	}
}

func TestCurrentTimezoneIDPrefersEngineHostTimezone(t *testing.T) {
	t.Setenv("BOREALIS_ENGINE_HOST_TIMEZONE", "America/Denver")
	t.Setenv("TZ", "Etc/UTC")

	if got := currentTimezoneID(); got != "America/Denver" {
		t.Fatalf("expected engine host timezone, got %q", got)
	}
}
