package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

type fakeOperatorAuthGate struct {
	allowed bool
	err     error
}

func (g fakeOperatorAuthGate) operatorAuthAllowed(_ context.Context) (bool, error) {
	return g.allowed, g.err
}

func TestRequireUserRejectsWhenBootstrapGateBlocksAuth(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})
	auth.bootstrapGate = fakeOperatorAuthGate{allowed: false}
	request := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)

	_, failure := requireUser(context.Background(), auth, request)
	if failure == nil || failure.status != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized failure, got %+v", failure)
	}
}

func TestLegacyBootstrapGateUsesInternalToken(t *testing.T) {
	secret := []byte("test-secret")
	var gotToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/internal/bootstrap/state" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		gotToken = r.Header.Get(internalTokenHeader)
		writeJSON(w, http.StatusOK, map[string]any{"phase": "login_required", "configured": true, "locked": false})
	}))
	defer server.Close()
	baseURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	allowed, err := (&legacyBootstrapGate{baseURL: baseURL, secret: secret, client: server.Client()}).operatorAuthAllowed(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("expected auth allowed")
	}
	if gotToken != goInternalToken(secret) {
		t.Fatalf("unexpected internal token %q", gotToken)
	}
}

func TestLegacyBootstrapGateBlocksNonLoginPhase(t *testing.T) {
	secret := []byte("test-secret")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"phase": "aegis_unlock_required", "configured": true, "locked": true})
	}))
	defer server.Close()
	baseURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	allowed, err := (&legacyBootstrapGate{baseURL: baseURL, secret: secret, client: server.Client()}).operatorAuthAllowed(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("expected auth blocked")
	}
}
