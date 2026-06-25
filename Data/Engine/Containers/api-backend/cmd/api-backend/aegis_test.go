package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeAegisStatusProvider struct {
	payload map[string]any
	err     error
}

func (p *fakeAegisStatusProvider) aegisStatus(_ context.Context) (map[string]any, error) {
	if p.err != nil {
		return nil, p.err
	}
	return copyMap(p.payload), nil
}

func TestAegisStatusHandlerAddsUserRole(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/aegis/status", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)

	aegisStatusHandler(auth, &fakeAegisStatusProvider{payload: map[string]any{
		"configured":   true,
		"locked":       false,
		"unlock_scope": "engine_global",
		"secret_scope": []any{"credentials", "github_token", "operator_auth"},
		"updated_at":   int64(1234),
	}}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"user_role":"Admin"`) || !strings.Contains(recorder.Body.String(), `"locked":false`) {
		t.Fatalf("unexpected payload %s", recorder.Body.String())
	}
}

func TestAegisStatusHandlerRequiresAuth(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/aegis/status", nil)

	aegisStatusHandler(auth, &fakeAegisStatusProvider{}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAegisStatusHandlerBlocksLockedState(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/aegis/status", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)

	aegisStatusHandler(auth, &fakeAegisStatusProvider{payload: map[string]any{
		"configured": true,
		"locked":     true,
	}}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAegisStatusHandlerProviderFailure(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/aegis/status", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)

	aegisStatusHandler(auth, &fakeAegisStatusProvider{err: errors.New("offline")}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), "aegis_status_unavailable") {
		t.Fatalf("expected status failure, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}
