package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
			baseURL := "http://" + r.Host
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":        "approved",
				"guid":          "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
				"access_token":  "access",
				"refresh_token": "refresh",
				"expires_in":    900,
				"signing_key":   signingPub,
				"remote_ops_route": map[string]any{
					"available":         true,
					"site_id":           1,
					"worker_guid":       "worker-agent-route",
					"route_generation":  4,
					"route_path_prefix": "/_borealis/site-workers/worker-agent-route",
					"base_url":          baseURL + "/_borealis/site-workers/worker-agent-route/",
					"socket_url":        baseURL + "/_borealis/site-workers/worker-agent-route/socket.io/",
				},
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
	if !loaded.RemoteOps.Available || loaded.RemoteOps.BaseURL != server.URL+"/_borealis/site-workers/worker-agent-route" {
		t.Fatalf("remote ops route not saved: %#v", loaded.RemoteOps)
	}
	if client.RemoteOpsBaseURL() != server.URL+"/_borealis/site-workers/worker-agent-route" {
		t.Fatalf("remote ops base url mismatch: %q", client.RemoteOpsBaseURL())
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
		baseURL := "http://" + r.Host
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "new",
			"expires_in":   900,
			"remote_ops_route": map[string]any{
				"available":         true,
				"site_id":           1,
				"worker_guid":       "worker-refresh-route",
				"route_generation":  7,
				"route_path_prefix": "/_borealis/site-workers/worker-refresh-route",
				"base_url":          baseURL + "/_borealis/site-workers/worker-refresh-route",
				"socket_url":        baseURL + "/_borealis/site-workers/worker-refresh-route/socket.io/",
			},
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
	if !loaded.RemoteOps.Available || loaded.RemoteOps.WorkerGUID != "worker-refresh-route" || loaded.RemoteOps.RouteGeneration != 7 {
		t.Fatalf("remote ops route not saved from refresh: %#v", loaded.RemoteOps)
	}
}

func TestRefreshTokenClearsUnavailableRemoteOpsRoute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/token/refresh" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "new",
			"expires_in":   900,
			"remote_ops_route": map[string]any{
				"available": false,
				"site_id":   1,
				"reason":    "site_worker_unavailable",
			},
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
	cfg.RemoteOps.Available = true
	cfg.RemoteOps.BaseURL = server.URL + "/_borealis/site-workers/stale"
	cfg.RemoteOps.WorkerGUID = "stale"
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
	if loaded.RemoteOps.Available || loaded.RemoteOps.BaseURL != "" || loaded.RemoteOps.Reason != "site_worker_unavailable" {
		t.Fatalf("remote ops route not cleared: %#v", loaded.RemoteOps)
	}
	if client.RemoteOpsBaseURL() != "" {
		t.Fatalf("legacy api-backend fallback used: %q", client.RemoteOpsBaseURL())
	}
}

func TestLegacyRemoteOpsRootForcesRefresh(t *testing.T) {
	var refreshSeen bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/token/refresh" {
			http.NotFound(w, r)
			return
		}
		refreshSeen = true
		baseURL := "http://" + r.Host
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "new",
			"expires_in":   900,
			"remote_ops_route": map[string]any{
				"available":         true,
				"site_id":           1,
				"worker_guid":       "worker-current-route",
				"route_generation":  8,
				"route_path_prefix": "/_borealis/site-workers/worker-current-route",
				"base_url":          baseURL + "/_borealis/site-workers/worker-current-route",
				"socket_url":        baseURL + "/_borealis/site-workers/worker-current-route/socket.io/",
			},
		})
	}))
	defer server.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, agentconfig.FileName)
	cfg := agentconfig.Default()
	cfg.ServerURL = server.URL
	cfg.Agent.GUID = "GUID"
	cfg.Tokens.AccessToken = "old"
	cfg.Tokens.AccessExpiresAt = time.Now().Add(time.Hour).Unix()
	cfg.Tokens.RefreshToken = "refresh"
	cfg.RemoteOps.Available = true
	cfg.RemoteOps.BaseURL = server.URL
	cfg.RemoteOps.UpdatedAt = time.Now().Unix()
	if err := agentconfig.Save(path, &cfg); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(path, &cfg, "system", WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}

	if !client.RemoteOpsRouteNeedsRefresh(time.Minute) {
		t.Fatal("legacy api-backend root route did not require refresh")
	}
	if client.RemoteOpsBaseURL() != "" {
		t.Fatalf("legacy api-backend fallback used: %q", client.RemoteOpsBaseURL())
	}
	if err := client.RefreshRemoteOpsRoute(context.Background()); err != nil {
		t.Fatalf("RefreshRemoteOpsRoute failed: %v", err)
	}
	if !refreshSeen {
		t.Fatal("refresh endpoint was not called")
	}
	loaded, _ := agentconfig.Load(path)
	if !loaded.RemoteOps.Available || loaded.RemoteOps.WorkerGUID != "worker-current-route" {
		t.Fatalf("remote ops route not refreshed: %#v", loaded.RemoteOps)
	}
	if client.RemoteOpsBaseURL() != server.URL+"/_borealis/site-workers/worker-current-route" {
		t.Fatalf("remote ops base url mismatch: %q", client.RemoteOpsBaseURL())
	}
}

func TestRemoteOpsRouteNeedsRefreshHonorsAge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, agentconfig.FileName)
	cfg := agentconfig.Default()
	cfg.ServerURL = "https://borealis.example.test"
	cfg.RemoteOps.Available = true
	cfg.RemoteOps.WorkerGUID = "worker-current-route"
	cfg.RemoteOps.RoutePathPrefix = "/_borealis/site-workers/worker-current-route"
	cfg.RemoteOps.BaseURL = "https://borealis.example.test/_borealis/site-workers/worker-current-route"
	cfg.RemoteOps.SocketURL = cfg.RemoteOps.BaseURL + "/socket.io"
	cfg.RemoteOps.UpdatedAt = time.Now().Unix()
	if err := agentconfig.Save(path, &cfg); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(path, &cfg, "system")
	if err != nil {
		t.Fatal(err)
	}
	if client.RemoteOpsRouteNeedsRefresh(time.Minute) {
		t.Fatal("fresh site-worker route required refresh")
	}
	client.mu.Lock()
	client.cfg.RemoteOps.UpdatedAt = time.Now().Add(-2 * time.Minute).Unix()
	client.mu.Unlock()
	if !client.RemoteOpsRouteNeedsRefresh(time.Minute) {
		t.Fatal("stale site-worker route did not require refresh")
	}
}

func TestHTTPClientUsesTrustedEngineCA(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ok" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer access" {
			t.Fatalf("missing authorization")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	}))
	defer server.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, agentconfig.FileName)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	cfg := agentconfig.Default()
	cfg.ServerURL = server.URL
	cfg.Trust.EngineCAPEM = string(certPEM)
	cfg.Agent.GUID = "GUID"
	cfg.Tokens.AccessToken = "access"
	cfg.Tokens.AccessExpiresAt = time.Now().Add(time.Hour).Unix()
	cfg.Tokens.RefreshToken = "refresh"
	if err := agentconfig.Save(path, &cfg); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(path, &cfg, "system")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]string
	if _, err := client.GetJSON(context.Background(), "/ok", &payload); err != nil {
		t.Fatalf("trusted CA request failed: %v", err)
	}
	if payload["status"] != "ok" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestTransientRefreshFailureKeepsRefreshToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/token/refresh" {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "temporary outage", http.StatusInternalServerError)
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
	loaded, _ := agentconfig.Load(path)
	if loaded.Tokens.RefreshToken != "refresh" {
		t.Fatalf("transient refresh failure cleared refresh token: %#v", loaded.Tokens)
	}
}

func TestPermanentRefreshFailureClearsRefreshToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/token/refresh" {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "invalid_refresh", http.StatusUnauthorized)
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
	loaded, _ := agentconfig.Load(path)
	if loaded.Tokens.RefreshToken != "" || loaded.Tokens.AccessToken != "" {
		t.Fatalf("permanent refresh failure did not clear tokens: %#v", loaded.Tokens)
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
