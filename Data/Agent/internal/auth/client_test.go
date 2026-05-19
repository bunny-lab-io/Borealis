package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
)

func TestEnrollmentHandshake(t *testing.T) {
	_, signingPub, _ := fakeSigningKey(t)
	var requestSeen bool
	var pollSeen bool
	serverNonce := []byte("server-nonce")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/enroll/request":
			requestSeen = true
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["enrollment_code"] != "CODE" {
				t.Fatalf("bad enrollment code: %v", payload["enrollment_code"])
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":             "pending",
				"approval_reference": "ref",
				"server_nonce":       base64.StdEncoding.EncodeToString(serverNonce),
				"poll_after_ms":      1,
				"signing_key":        signingPub,
			})
		case "/api/agent/enroll/poll":
			pollSeen = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":        "approved",
				"guid":          "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
				"access_token":  "access",
				"refresh_token": "refresh",
				"expires_in":    900,
				"signing_key":   signingPub,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, agentconfig.FileName)
	cfg := agentconfig.Default()
	cfg.ServerURL = server.URL
	cfg.EnrollmentCode = "CODE"
	if err := agentconfig.Save(path, &cfg); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(path, &cfg, "system", WithHTTPClient(server.Client()), WithHostname("host"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.EnsureAuthenticated(ctx); err != nil {
		t.Fatalf("EnsureAuthenticated failed: %v", err)
	}
	if !requestSeen || !pollSeen {
		t.Fatalf("expected request and poll")
	}
	loaded, err := agentconfig.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Agent.GUID != "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE" {
		t.Fatalf("guid mismatch: %q", loaded.Agent.GUID)
	}
	if loaded.Tokens.RefreshToken != "refresh" {
		t.Fatalf("refresh token not saved")
	}
	if loaded.Trust.ServerSigningKeySPKIB64 != signingPub {
		t.Fatalf("signing key not saved")
	}
}

func TestRefreshToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/token/refresh" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer old" {
			t.Fatalf("missing authorization")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "new",
			"expires_in":   900,
		})
	}))
	defer server.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, agentconfig.FileName)
	cfg := agentconfig.Default()
	cfg.ServerURL = server.URL
	cfg.Agent.GUID = "GUID"
	cfg.Tokens.AccessToken = "old"
	cfg.Tokens.AccessExpiresAt = time.Now().Add(-time.Minute).Unix()
	cfg.Tokens.RefreshToken = "refresh"
	if err := agentconfig.Save(path, &cfg); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(path, &cfg, "system", WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	if err := client.EnsureAuthenticated(context.Background()); err != nil {
		t.Fatalf("EnsureAuthenticated failed: %v", err)
	}
	loaded, _ := agentconfig.Load(path)
	if loaded.Tokens.AccessToken != "new" {
		t.Fatalf("access token = %q", loaded.Tokens.AccessToken)
	}
}

func TestRefreshTransientFailureKeepsRefreshToken(t *testing.T) {
	var enrollSeen bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/token/refresh":
			http.Error(w, `{"error":"temporary"}`, http.StatusBadGateway)
		case "/api/agent/enroll/request", "/api/agent/enroll/poll":
			enrollSeen = true
			http.Error(w, `{"error":"unexpected_enrollment"}`, http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, agentconfig.FileName)
	cfg := agentconfig.Default()
	cfg.ServerURL = server.URL
	cfg.EnrollmentCode = "STALE-CODE"
	cfg.Agent.GUID = "GUID"
	cfg.Tokens.AccessToken = "old"
	cfg.Tokens.AccessExpiresAt = time.Now().Add(-time.Minute).Unix()
	cfg.Tokens.RefreshToken = "refresh"
	if err := agentconfig.Save(path, &cfg); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(path, &cfg, "system", WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	if err := client.EnsureAuthenticated(context.Background()); err == nil {
		t.Fatal("expected refresh failure")
	}
	if enrollSeen {
		t.Fatal("transient refresh failure entered enrollment")
	}
	loaded, err := agentconfig.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Tokens.RefreshToken != "refresh" {
		t.Fatalf("refresh token cleared on transient failure: %q", loaded.Tokens.RefreshToken)
	}
	if loaded.Tokens.AccessToken != "old" {
		t.Fatalf("access token cleared on transient failure: %q", loaded.Tokens.AccessToken)
	}
	if loaded.EnrollmentCode != "" {
		t.Fatalf("stale enrollment_code not cleared: %q", loaded.EnrollmentCode)
	}
}

func TestRefreshInvalidTokenClearsCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/token/refresh" {
			http.NotFound(w, r)
			return
		}
		http.Error(w, `{"error":"invalid_refresh_token"}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, agentconfig.FileName)
	cfg := agentconfig.Default()
	cfg.ServerURL = server.URL
	cfg.Agent.GUID = "GUID"
	cfg.Tokens.AccessToken = "old"
	cfg.Tokens.AccessExpiresAt = time.Now().Add(-time.Minute).Unix()
	cfg.Tokens.RefreshToken = "refresh"
	if err := agentconfig.Save(path, &cfg); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(path, &cfg, "system", WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	if err := client.EnsureAuthenticated(context.Background()); err == nil {
		t.Fatal("expected refresh failure")
	}
	loaded, err := agentconfig.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Tokens.RefreshToken != "" || loaded.Tokens.AccessToken != "" || loaded.Tokens.AccessExpiresAt != 0 {
		t.Fatalf("credentials not cleared after invalid refresh: %#v", loaded.Tokens)
	}
}

func TestEnsureAuthenticatedSerializesEnrollment(t *testing.T) {
	_, signingPub, _ := fakeSigningKey(t)
	serverNonce := []byte("server-nonce")
	var mu sync.Mutex
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/enroll/request":
			mu.Lock()
			requestCount++
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":             "pending",
				"approval_reference": "ref",
				"server_nonce":       base64.StdEncoding.EncodeToString(serverNonce),
				"poll_after_ms":      1,
				"signing_key":        signingPub,
			})
		case "/api/agent/enroll/poll":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":        "approved",
				"guid":          "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
				"access_token":  "access",
				"refresh_token": "refresh",
				"expires_in":    900,
				"signing_key":   signingPub,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, agentconfig.FileName)
	cfg := agentconfig.Default()
	cfg.ServerURL = server.URL
	cfg.EnrollmentCode = "CODE"
	if err := agentconfig.Save(path, &cfg); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(path, &cfg, "system", WithHTTPClient(server.Client()), WithHostname("host"))
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			errs <- client.EnsureAuthenticated(ctx)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("EnsureAuthenticated failed: %v", err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if requestCount != 1 {
		t.Fatalf("enrollment request count = %d, want 1", requestCount)
	}
}

func fakeSigningKey(t *testing.T) (ed25519.PrivateKey, string, []byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return priv, base64.StdEncoding.EncodeToString(der), der
}
