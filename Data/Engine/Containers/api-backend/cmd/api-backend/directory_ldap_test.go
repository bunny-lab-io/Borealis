package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func TestDirectoryTLSConfigFailsClosedWithoutCustomCA(t *testing.T) {
	provider := directoryProviderConfig{
		Row: directoryProviderRow{
			TLSRequired: sql.NullInt64{Int64: 0, Valid: true},
		},
	}
	target := directoryConnectionTarget{
		Scheme:        "ldaps",
		Host:          "ldap.example.test",
		RequestedHost: "ldap.example.test",
		ServerURL:     "ldaps://ldap.example.test:636",
	}

	config, err := directoryTLSConfig(provider, target)
	if err != nil {
		t.Fatal(err)
	}
	if config == nil {
		t.Fatal("expected LDAPS TLS config")
	}
	if config.InsecureSkipVerify {
		t.Fatal("LDAPS config must not disable certificate verification")
	}
	if config.ServerName != "ldap.example.test" {
		t.Fatalf("unexpected server name %q", config.ServerName)
	}
}

func TestDirectoryTLSConfigPinnedLeafUsesStrictTrustAnchor(t *testing.T) {
	now := time.Now()
	leaf, pemText := testDirectoryLeafCertificate(t, "ldap.example.test", now.Add(-time.Hour), now.Add(time.Hour))
	provider := directoryProviderConfig{
		Row: directoryProviderRow{
			TLSCAPEM: sql.NullString{String: pemText, Valid: true},
		},
	}
	target := directoryConnectionTarget{
		Scheme:        "ldaps",
		Host:          "ldap.example.test",
		RequestedHost: "ldap.example.test",
		ServerURL:     "ldaps://ldap.example.test:636",
	}

	config, err := directoryTLSConfig(provider, target)
	if err != nil {
		t.Fatal(err)
	}
	if config == nil {
		t.Fatal("expected LDAPS TLS config")
	}
	if config.InsecureSkipVerify {
		t.Fatal("pinned leaf config must not disable certificate verification")
	}
	if config.RootCAs == nil {
		t.Fatal("pinned leaf config must install certificate roots")
	}
	opts := x509.VerifyOptions{
		Roots:       config.RootCAs,
		DNSName:     "ldap.example.test",
		CurrentTime: now,
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if _, err := leaf.Verify(opts); err != nil {
		t.Fatalf("expected exact pinned certificate to verify: %v", err)
	}
}

func TestDirectoryTLSConfigPinnedLeafRejectsUnsafePeers(t *testing.T) {
	now := time.Now()
	pinned, _ := testDirectoryLeafCertificate(t, "ldap.example.test", now.Add(-time.Hour), now.Add(time.Hour))
	other, _ := testDirectoryLeafCertificate(t, "ldap.example.test", now.Add(-time.Hour), now.Add(time.Hour))
	wrongHost, _ := testDirectoryLeafCertificate(t, "wrong.example.test", now.Add(-time.Hour), now.Add(time.Hour))
	expired, _ := testDirectoryLeafCertificate(t, "ldap.example.test", now.Add(-2*time.Hour), now.Add(-time.Hour))
	roots := x509.NewCertPool()
	roots.AddCert(pinned)

	opts := x509.VerifyOptions{
		Roots:       roots,
		DNSName:     "ldap.example.test",
		CurrentTime: now,
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if _, err := pinned.Verify(opts); err != nil {
		t.Fatalf("expected pinned certificate to verify: %v", err)
	}
	if _, err := other.Verify(opts); err == nil {
		t.Fatal("expected unpinned replacement certificate to fail")
	}

	wrongHostRoots := x509.NewCertPool()
	wrongHostRoots.AddCert(wrongHost)
	wrongHostOpts := opts
	wrongHostOpts.Roots = wrongHostRoots
	if _, err := wrongHost.Verify(wrongHostOpts); err == nil {
		t.Fatal("expected hostname mismatch to fail")
	}

	expiredRoots := x509.NewCertPool()
	expiredRoots.AddCert(expired)
	expiredOpts := opts
	expiredOpts.Roots = expiredRoots
	if _, err := expired.Verify(expiredOpts); err == nil {
		t.Fatal("expected expired pinned certificate to fail")
	}
}

func testDirectoryLeafCertificate(t *testing.T, dnsName string, notBefore time.Time, notAfter time.Time) (*x509.Certificate, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(notBefore.UnixNano()),
		Subject:      pkix.Name{CommonName: dnsName},
		DNSNames:     []string{dnsName},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	raw, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(raw)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw})
	if pemBytes == nil {
		t.Fatal("failed to encode certificate")
	}
	return cert, string(pemBytes)
}
