package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCollectOverviewPublicEdgePayloadReadsTraefikACMECertificate(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "Engine", "Services", "traefik-edge", "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	acmePath := filepath.Join(stateDir, "acme.json")
	settingsPath := filepath.Join(stateDir, "Settings.json")
	certPEM := testOverviewCertificatePEM(t, "borealis.example.test", time.Now().Add(90*24*time.Hour))
	acmePayload := map[string]any{
		"letsencrypt": map[string]any{
			"Certificates": []any{
				map[string]any{
					"Store":       "default",
					"certificate": base64.StdEncoding.EncodeToString(certPEM),
					"domain": map[string]any{
						"main": "borealis.example.test",
						"sans": []any{"www.borealis.example.test"},
					},
				},
			},
		},
	}
	acmeJSON, err := json.Marshal(acmePayload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(acmePath, acmeJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"acme_email":"ops@example.test"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("BOREALIS_PROJECT_ROOT", root)
	t.Setenv("BOREALIS_TRAEFIK_SETTINGS_PATH", settingsPath)
	t.Setenv("BOREALIS_PUBLIC_HOSTNAME", "borealis.example.test")
	t.Setenv("BOREALIS_PUBLIC_BASE_URL", "https://borealis.example.test")
	t.Setenv("BOREALIS_PUBLIC_EDGE_ENABLED", "1")

	payload := collectOverviewPublicEdgePayload()
	if got := payload["acme_email"]; got != "ops@example.test" {
		t.Fatalf("expected acme email, got %#v", got)
	}
	certificates, ok := payload["certificates"].([]any)
	if !ok || len(certificates) != 1 {
		t.Fatalf("expected one certificate row, got %#v", payload["certificates"])
	}
	row := certificates[0].(map[string]any)
	if got := row["name"]; got != "borealis.example.test" {
		t.Fatalf("expected certificate name, got %#v", got)
	}
	if got := row["severity"]; got != "healthy" {
		t.Fatalf("expected healthy certificate, got %#v", got)
	}
	if got := row["expires_at"]; cleanText(got) == "" {
		t.Fatalf("expected certificate expiry, got %#v", row)
	}
	if got := row["sha256_fingerprint"]; cleanText(got) == "" {
		t.Fatalf("expected certificate fingerprint, got %#v", row)
	}
}

func TestCollectOverviewHostPayloadReportsWebUITrafficOwner(t *testing.T) {
	t.Setenv("BOREALIS_WEBUI_MODE", "prod")
	t.Setenv("BOREALIS_WEBUI_TRAFFIC_OWNER", "k3s")
	t.Setenv("BOREALIS_WEBUI_UPSTREAM_HOST", "10.43.82.247")
	t.Setenv("BOREALIS_WEBUI_UPSTREAM_PORT", "8000")

	payload := collectOverviewHostPayload()
	if got := payload["webui_mode"]; got != "production" {
		t.Fatalf("webui mode = %#v", got)
	}
	if got := payload["webui_traffic_owner"]; got != "k3s" {
		t.Fatalf("webui traffic owner = %#v", got)
	}
	upstream, ok := payload["webui_upstream"].(map[string]any)
	if !ok {
		t.Fatalf("webui upstream missing: %#v", payload["webui_upstream"])
	}
	if got := upstream["display"]; got != "10.43.82.247:8000" {
		t.Fatalf("webui upstream display = %#v", got)
	}
}

func TestCollectOverviewServiceRowsOmitsRetiredComposeWebUI(t *testing.T) {
	t.Setenv("BOREALIS_ENGINE_CONTAINERIZED", "1")
	t.Setenv("BOREALIS_WEBUI_TRAFFIC_OWNER", "k3s")
	t.Setenv("BOREALIS_WEBUI_FRONTEND_IMAGE", "borealis-engine/webui-frontend:sha-test")

	rows := collectOverviewServiceRows(context.Background(), nil)
	for _, row := range rows {
		if row["key"] == "webui-frontend" && row["runtime"] == "compose" {
			t.Fatalf("retired Compose webui-frontend row should be omitted: %#v", row)
		}
	}
}

func TestCollectOverviewServiceRowsIncludesK3sScheduler(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req borealisOperatorCommandRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Verb != "GetWorkloadStatus" {
			t.Fatalf("unexpected operator verb %q", req.Verb)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":   true,
			"verb": req.Verb,
			"result": map[string]any{
				"kind":               "Deployment",
				"name":               "job-scheduler",
				"service_key":        "job-scheduler",
				"replicas":           1,
				"ready_replicas":     1,
				"available_replicas": 1,
				"desired_ready":      true,
			},
		})
	}))
	defer server.Close()

	t.Setenv("BOREALIS_ENGINE_CONTAINERIZED", "1")
	t.Setenv("BOREALIS_JOB_SCHEDULER_RUNTIME_OWNER", "k3s")
	t.Setenv("BOREALIS_OPERATOR_BASE_URL", server.URL)
	t.Setenv("BOREALIS_OPERATOR_SECRET", "test-secret")

	rows := collectOverviewServiceRows(context.Background(), nil)
	for _, row := range rows {
		if row["key"] == "job-scheduler" {
			if row["runtime"] != "k3s" || row["status"] != "healthy" {
				t.Fatalf("unexpected K3s scheduler row: %#v", row)
			}
			return
		}
	}
	t.Fatalf("K3s scheduler row missing: %#v", rows)
}

func TestCollectOverviewServiceRowsUsesK3sRowsForRetiredWorkloads(t *testing.T) {
	expected := map[string]string{
		"api-backend":          "Deployment",
		"job-scheduler":        "Deployment",
		"postgres-db":          "StatefulSet",
		"remote-desktop-guacd": "Deployment",
		"webui-frontend":       "Deployment",
		"wireguard-tunnel":     "Deployment",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req borealisOperatorCommandRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Verb != "GetWorkloadStatus" {
			t.Fatalf("unexpected operator verb %q", req.Verb)
		}
		serviceKey := cleanText(req.Params["service_key"])
		kind, ok := expected[serviceKey]
		if !ok {
			t.Fatalf("unexpected service_key %q", serviceKey)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":   true,
			"verb": req.Verb,
			"result": map[string]any{
				"kind":               kind,
				"name":               serviceKey,
				"service_key":        serviceKey,
				"replicas":           1,
				"ready_replicas":     1,
				"available_replicas": 1,
				"desired_ready":      true,
			},
		})
	}))
	defer server.Close()

	t.Setenv("BOREALIS_ENGINE_CONTAINERIZED", "1")
	t.Setenv("BOREALIS_API_BACKEND_RUNTIME_OWNER", "k3s")
	t.Setenv("BOREALIS_JOB_SCHEDULER_RUNTIME_OWNER", "k3s")
	t.Setenv("BOREALIS_POSTGRES_RUNTIME_OWNER", "k3s")
	t.Setenv("BOREALIS_REMOTE_DESKTOP_GUACD_RUNTIME_OWNER", "k3s")
	t.Setenv("BOREALIS_WEBUI_RUNTIME_OWNER", "k3s")
	t.Setenv("BOREALIS_WIREGUARD_TUNNEL_RUNTIME_OWNER", "k3s")
	t.Setenv("BOREALIS_OPERATOR_BASE_URL", server.URL)
	t.Setenv("BOREALIS_OPERATOR_SECRET", "test-secret")

	rows := collectOverviewServiceRows(context.Background(), nil)
	seen := map[string]map[string]any{}
	for _, row := range rows {
		seen[cleanText(row["key"])] = row
	}
	for serviceKey, kind := range expected {
		row := seen[serviceKey]
		if row == nil {
			t.Fatalf("K3s row missing for %s: %#v", serviceKey, rows)
		}
		if row["runtime"] != "k3s" || row["compose_service"] != nil || row["status"] != "healthy" {
			t.Fatalf("unexpected K3s row for %s: %#v", serviceKey, row)
		}
		if row["kubernetes_kind"] != kind {
			t.Fatalf("kind for %s = %#v", serviceKey, row["kubernetes_kind"])
		}
	}
	for _, serviceKey := range []string{"docker-proxy", "site-worker-orchestrator", "traefik-edge"} {
		row := seen[serviceKey]
		if row == nil || row["runtime"] != "compose" {
			t.Fatalf("remaining bridge Compose row missing for %s: %#v", serviceKey, rows)
		}
	}
}

type overviewServiceSnapshotStoreStub struct {
	snapshots map[string]overviewServiceSnapshot
	err       error
}

func (s overviewServiceSnapshotStoreStub) lookupOperator(context.Context, string, string) (operatorProfile, error) {
	return operatorProfile{}, nil
}

func (s overviewServiceSnapshotStoreStub) overviewServiceSnapshots(context.Context) (map[string]overviewServiceSnapshot, error) {
	return s.snapshots, s.err
}

func TestCollectOverviewServiceRowsUsesSchedulerSnapshotsForComposeBridge(t *testing.T) {
	t.Setenv("BOREALIS_ENGINE_CONTAINERIZED", "1")
	now := time.Now().Unix()
	store := overviewServiceSnapshotStoreStub{snapshots: map[string]overviewServiceSnapshot{
		"docker-proxy": {
			updatedAt: now,
			payload: map[string]any{
				"Name":    "borealis-engine-docker-proxy",
				"Service": "docker-proxy",
				"State":   "running",
				"Health":  "healthy",
				"Status":  "Up 10 minutes (healthy)",
				"Image":   "ghcr.io/tecnativa/docker-socket-proxy:v0.4.2",
			},
		},
		"site-worker-orchestrator": {
			updatedAt: now,
			payload: map[string]any{
				"Name":    "borealis-engine-site-worker-orchestrator",
				"Service": "site-worker-orchestrator",
				"State":   "running",
				"Health":  "healthy",
				"Status":  "Up 10 minutes (healthy)",
				"Image":   "borealis-engine/api-backend:sha-test",
			},
		},
		"traefik-edge": {
			updatedAt: now,
			payload: map[string]any{
				"Name":    "borealis-engine-traefik-edge",
				"Service": "traefik-edge",
				"State":   "running",
				"Health":  "healthy",
				"Status":  "Up 10 minutes (healthy)",
				"Image":   "traefik:v3.4.3",
			},
		},
	}}

	rows := collectOverviewServiceRows(context.Background(), store)
	seen := map[string]map[string]any{}
	for _, row := range rows {
		seen[cleanText(row["key"])] = row
	}
	for _, serviceKey := range []string{"docker-proxy", "site-worker-orchestrator", "traefik-edge"} {
		row := seen[serviceKey]
		if row == nil {
			t.Fatalf("compose bridge row missing for %s: %#v", serviceKey, rows)
		}
		if row["snapshot_source"] != "job-scheduler" || row["runtime"] != "compose" || row["status"] != "healthy" {
			t.Fatalf("expected scheduler snapshot row for %s, got %#v", serviceKey, row)
		}
	}
}

func TestCollectOverviewPublicEdgePayloadReadsInternalLocalCA(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "Engine", "Services", "traefik-edge", "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(stateDir, "local-ca.crt")
	leafPath := filepath.Join(stateDir, "leaf.crt")
	caPEM := testOverviewCertificatePEM(t, "Borealis Local Engine CA", time.Now().Add(365*24*time.Hour))
	leafPEM := testOverviewCertificatePEM(t, "borealis.internal.example", time.Now().Add(90*24*time.Hour))
	if err := os.WriteFile(caPath, caPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leafPath, leafPEM, 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("BOREALIS_PROJECT_ROOT", root)
	t.Setenv("BOREALIS_ENGINE_DEPLOYMENT_PROFILE", "internal-only")
	t.Setenv("BOREALIS_LOCAL_CA_ENABLED", "1")
	t.Setenv("BOREALIS_LOCAL_CA_CERT_PATH", caPath)
	t.Setenv("BOREALIS_LOCAL_TLS_CERT_PATH", leafPath)
	t.Setenv("BOREALIS_PUBLIC_HOSTNAME", "borealis.internal.example")
	t.Setenv("BOREALIS_PUBLIC_HOSTNAME_ALIASES", "engine.internal.example,borealis.internal.example")
	t.Setenv("BOREALIS_PUBLIC_BASE_URL", "https://borealis.internal.example")
	t.Setenv("BOREALIS_ENGINE_IP_FALLBACK", "192.168.3.251")

	payload := collectOverviewPublicEdgePayload()
	if got := payload["deployment_profile"]; got != "internal-only" {
		t.Fatalf("deployment profile = %#v", got)
	}
	if got := payload["certificate_mode"]; got != "local_ca" {
		t.Fatalf("certificate mode = %#v", got)
	}
	if got := payload["server_ip_fallback"]; got != "192.168.3.251" {
		t.Fatalf("server IP fallback = %#v", got)
	}
	certificates, ok := payload["certificates"].([]any)
	if !ok || len(certificates) != 1 {
		t.Fatalf("expected one local certificate row, got %#v", payload["certificates"])
	}
	row := certificates[0].(map[string]any)
	if got := row["source"]; got != "traefik_local_ca" {
		t.Fatalf("expected local certificate source, got %#v", got)
	}
	localCA := payload["local_ca"].(map[string]any)
	if localCA["pem_b64"] != base64.StdEncoding.EncodeToString(caPEM) {
		t.Fatalf("local CA pem_b64 missing: %#v", localCA)
	}
	if got := localCA["installable"]; got != true {
		t.Fatalf("local CA should be installable, got %#v", got)
	}
}

func TestOverviewEngineNetworkModeMapsPublicAndLocal(t *testing.T) {
	t.Setenv("BOREALIS_ENGINE_NETWORK_MODE", "local")
	if got := overviewEngineNetworkMode(); got != "local" {
		t.Fatalf("network mode = %#v", got)
	}
	if got := overviewEngineNetworkModeLabel(); got != "Local" {
		t.Fatalf("network mode label = %#v", got)
	}
	if got := overviewEngineDeploymentProfile(); got != "internal-only" {
		t.Fatalf("deployment profile = %#v", got)
	}

	t.Setenv("BOREALIS_ENGINE_NETWORK_MODE", "public")
	if got := overviewEngineNetworkMode(); got != "public" {
		t.Fatalf("network mode = %#v", got)
	}
	if got := overviewEngineNetworkModeLabel(); got != "Public" {
		t.Fatalf("network mode label = %#v", got)
	}
	if got := overviewEngineDeploymentProfile(); got != "externally-accessible" {
		t.Fatalf("deployment profile = %#v", got)
	}
}

func testOverviewCertificatePEM(t *testing.T, commonName string, notAfter time.Time) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject: pkix.Name{
			CommonName: commonName,
		},
		NotBefore: time.Now().Add(-1 * time.Hour),
		NotAfter:  notAfter,
		DNSNames:  []string{commonName},
		KeyUsage:  x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
		},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
