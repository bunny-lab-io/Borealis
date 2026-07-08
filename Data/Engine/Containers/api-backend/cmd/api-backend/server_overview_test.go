package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
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
