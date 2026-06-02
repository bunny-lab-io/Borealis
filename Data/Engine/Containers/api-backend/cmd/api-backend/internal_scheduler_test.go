package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func testInternalSchedulerAuth() *authService {
	return &authService{
		verifier: &tokenVerifier{
			secret: []byte("test-secret"),
			maxAge: time.Hour,
			now:    func() time.Time { return time.Unix(1700000010, 0) },
		},
		timeout: time.Second,
	}
}

func TestInternalSchedulerPublicBaseURLRequiresInternalToken(t *testing.T) {
	mux := http.NewServeMux()
	registerInternalSchedulerRoutes(mux, testInternalSchedulerAuth(), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/internal/job-scheduler/public-base-url", nil)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestInternalSchedulerPublicBaseURLReturnsConfiguredURL(t *testing.T) {
	t.Setenv("BOREALIS_PUBLIC_BASE_URL", "https://borealis.example.test/")
	auth := testInternalSchedulerAuth()
	mux := http.NewServeMux()
	registerInternalSchedulerRoutes(mux, auth, http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/internal/job-scheduler/public-base-url", nil)
	request.Header.Set(internalTokenHeader, goInternalToken(auth.verifier.secret))
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["public_base_url"] != "https://borealis.example.test" {
		t.Fatalf("unexpected payload %+v", payload)
	}
}
