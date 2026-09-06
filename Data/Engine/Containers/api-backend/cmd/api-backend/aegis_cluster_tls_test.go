package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type aegisTestIdentity struct {
	cert, key []byte
	pair      tls.Certificate
}

type aegisTestCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newAegisTestCA(t *testing.T, now time.Time, serial int64) aegisTestCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: fmt.Sprintf("CA-%d", serial)},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(48 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return aegisTestCA{cert, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

func (ca aegisTestCA) identity(t *testing.T, now time.Time, serial int64, duration time.Duration, usages ...x509.ExtKeyUsage) aegisTestIdentity {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(usages) == 0 {
		usages = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial), DNSNames: []string{"api-backend-aegis.borealis.svc"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:   now.Add(-time.Minute), NotAfter: now.Add(duration),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: usages,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	identity := aegisTestIdentity{
		cert: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		key:  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
	}
	identity.pair, err = tls.X509KeyPair(identity.cert, identity.key)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

type aegisTestProjection struct {
	dir        string
	generation int
	clock      atomic.Int64
	loader     *aegisClusterTLSReloader
}

func newAegisTestProjection(t *testing.T, now time.Time, identity aegisTestIdentity, roots []byte) *aegisTestProjection {
	t.Helper()
	p := &aegisTestProjection{dir: t.TempDir()}
	p.clock.Store(now.UnixNano())
	p.loader = &aegisClusterTLSReloader{serverName: "127.0.0.1", now: func() time.Time { return time.Unix(0, p.clock.Load()) }}
	for i, name := range []string{"tls.crt", "tls.key", "ca.crt"} {
		p.loader.paths[i] = filepath.Join(p.dir, name)
		if err := os.Symlink(filepath.Join("..data", name), p.loader.paths[i]); err != nil {
			t.Fatal(err)
		}
	}
	p.update(t, identity.cert, identity.key, roots)
	return p
}

func (p *aegisTestProjection) update(t *testing.T, cert, key, roots []byte) {
	t.Helper()
	p.generation++
	dir := filepath.Join(p.dir, fmt.Sprintf("generation-%d", p.generation))
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"tls.crt": cert, "tls.key": key, "ca.crt": roots} {
		if data != nil {
			if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.Symlink(dir, filepath.Join(p.dir, "..next")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(p.dir, "..next"), filepath.Join(p.dir, "..data")); err != nil {
		t.Fatal(err)
	}
}

func aegisTestTLSServer(t *testing.T, loader *aegisClusterTLSReloader, handler http.Handler) *httptest.Server {
	t.Helper()
	config, err := loader.serverConfig()
	if err != nil {
		t.Fatal(err)
	}
	if handler == nil {
		handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = config
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.Config.SetKeepAlivesEnabled(false)
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

func aegisTestTLSClient(identity aegisTestIdentity, roots []byte, now func() time.Time) *http.Client {
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(roots)
	config := &tls.Config{
		MinVersion: tls.VersionTLS13, RootCAs: pool, Time: now,
		ClientSessionCache: tls.NewLRUClientSessionCache(10),
	}
	if identity.pair.PrivateKey != nil {
		config.Certificates = []tls.Certificate{identity.pair}
	}
	return &http.Client{Timeout: time.Second, Transport: &http.Transport{TLSClientConfig: config, DisableKeepAlives: true}}
}

func assertAegisHandshake(t *testing.T, client *http.Client, server *httptest.Server, serial int64) {
	t.Helper()
	response, err := client.Get(server.URL)
	if serial == 0 {
		if err == nil {
			response.Body.Close()
			t.Fatal("expected TLS handshake rejection")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.TLS.DidResume || response.TLS.Version != tls.VersionTLS13 || response.TLS.PeerCertificates[0].SerialNumber.Int64() != serial {
		t.Fatalf("expected fresh TLS 1.3 handshake with serial %d; got %+v", serial, response.TLS)
	}
}

func TestAegisClusterTLSRenewalPartialProjectionAndExpiry(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	ca := newAegisTestCA(t, now, 1)
	old := ca.identity(t, now, 10, time.Hour)
	renewed := ca.identity(t, now, 11, 4*time.Hour)
	peer := ca.identity(t, now, 12, 24*time.Hour)
	p := newAegisTestProjection(t, now, old, ca.pem)
	server := aegisTestTLSServer(t, p.loader, nil)
	client := aegisTestTLSClient(peer, ca.pem, p.loader.now)
	assertAegisHandshake(t, client, server, 10)
	for _, update := range []struct {
		name             string
		cert, key, roots []byte
	}{
		{"mismatched key", renewed.cert, old.key, ca.pem},
		{"missing key", renewed.cert, nil, ca.pem},
		{"malformed trust", old.cert, old.key, []byte("invalid")},
		{"missing trust", old.cert, old.key, nil},
		{"malformed certificate", []byte("invalid"), renewed.key, ca.pem},
	} {
		t.Run(update.name, func(t *testing.T) {
			p.update(t, update.cert, update.key, update.roots)
			assertAegisHandshake(t, client, server, 10)
			config, err := p.loader.config(false)
			if err != nil || config.Certificates[0].Leaf.SerialNumber.Int64() != 10 {
				t.Fatalf("sender lost valid cached identity: %v", err)
			}
		})
	}
	p.clock.Store(now.Add(2 * time.Hour).UnixNano())
	assertAegisHandshake(t, client, server, 0)
	if _, err := p.loader.config(false); err == nil {
		t.Fatal("sender accepted expired cached identity")
	}
	p.update(t, renewed.cert, renewed.key, ca.pem)
	assertAegisHandshake(t, client, server, 11)
	p.clock.Store(now.Add(5 * time.Hour).UnixNano())
	assertAegisHandshake(t, client, server, 0)
}

func TestAegisClusterTLSCAOverlapAndRetirement(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	oldCA, newCA := newAegisTestCA(t, now, 1), newAegisTestCA(t, now, 2)
	old, renewed := oldCA.identity(t, now, 10, 24*time.Hour), newCA.identity(t, now, 20, 24*time.Hour)
	combined := append(append([]byte(nil), oldCA.pem...), newCA.pem...)
	p := newAegisTestProjection(t, now, old, oldCA.pem)
	server := aegisTestTLSServer(t, p.loader, nil)
	oldClient := aegisTestTLSClient(old, combined, p.loader.now)
	newClient := aegisTestTLSClient(renewed, combined, p.loader.now)
	assertAegisHandshake(t, oldClient, server, 10)
	assertAegisHandshake(t, newClient, server, 0)
	p.update(t, old.cert, old.key, combined)
	assertAegisHandshake(t, oldClient, server, 10)
	assertAegisHandshake(t, newClient, server, 10)
	p.update(t, renewed.cert, renewed.key, combined)
	assertAegisHandshake(t, oldClient, server, 20)
	assertAegisHandshake(t, newClient, server, 20)
	// Retirement applies even while a malformed key requires credential fallback.
	p.update(t, renewed.cert, old.key, newCA.pem)
	assertAegisHandshake(t, oldClient, server, 0)
	assertAegisHandshake(t, newClient, server, 20)
	p.update(t, renewed.cert, renewed.key, []byte("broken update"))
	assertAegisHandshake(t, oldClient, server, 0)
	assertAegisHandshake(t, newClient, server, 20)
	// Removing old trust before rotating an old holder must fail closed.
	lagging := newAegisTestProjection(t, now, old, combined)
	laggingServer := aegisTestTLSServer(t, lagging.loader, nil)
	lagging.update(t, old.cert, nil, newCA.pem)
	assertAegisHandshake(t, oldClient, laggingServer, 0)
	assertAegisHandshake(t, newClient, laggingServer, 0)
	lagging.update(t, renewed.cert, renewed.key, newCA.pem)
	assertAegisHandshake(t, newClient, laggingServer, 20)
}

func TestAegisClusterTLSRequiresPeerIdentityAndBothUsages(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	ca := newAegisTestCA(t, now, 1)
	good := ca.identity(t, now, 10, time.Hour)
	p := newAegisTestProjection(t, now, good, ca.pem)
	server := aegisTestTLSServer(t, p.loader, nil)
	for _, peer := range []aegisTestIdentity{
		{}, ca.identity(t, now, 11, time.Hour, x509.ExtKeyUsageServerAuth),
		ca.identity(t, now.Add(-2*time.Hour), 12, time.Hour),
	} {
		assertAegisHandshake(t, aegisTestTLSClient(peer, ca.pem, p.loader.now), server, 0)
	}
	for _, usage := range []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth} {
		bad := ca.identity(t, now, 13, time.Hour, usage)
		projection := newAegisTestProjection(t, now, bad, ca.pem)
		if _, err := projection.loader.serverConfig(); err == nil {
			t.Fatal("local identity must support both usages")
		}
	}
	wrongName := newAegisTestProjection(t, now, good, ca.pem)
	wrongName.loader.serverName = "wrong-service.borealis.svc"
	if _, err := wrongName.loader.config(false); err == nil {
		t.Fatal("local identity with wrong DNS name was accepted")
	}
}

func TestAegisClusterTLSRejectsMixedGenerationAndConcurrentReload(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	ca := newAegisTestCA(t, now, 1)
	old, renewed := ca.identity(t, now, 10, time.Hour), ca.identity(t, now, 11, time.Hour)
	p := newAegisTestProjection(t, now, old, ca.pem)
	server := aegisTestTLSServer(t, p.loader, nil)
	client := aegisTestTLSClient(old, ca.pem, p.loader.now)
	p.update(t, renewed.cert, renewed.key, ca.pem)
	// Pin only the CA to the previous generation: contents alone are identical.
	if err := os.Remove(p.loader.paths[2]); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(p.dir, "generation-1", "ca.crt"), p.loader.paths[2]); err != nil {
		t.Fatal(err)
	}
	assertAegisHandshake(t, client, server, 10)
	if err := os.Remove(p.loader.paths[2]); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..data", "ca.crt"), p.loader.paths[2]); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 8 {
				response, err := client.Get(server.URL)
				if err != nil {
					t.Error(err)
					return
				}
				response.Body.Close()
			}
		}()
	}
	for range 8 {
		p.update(t, old.cert, old.key, ca.pem)
		p.update(t, renewed.cert, renewed.key, ca.pem)
	}
	wg.Wait()
	assertAegisHandshake(t, client, server, 11)
}

func TestAegisClusterTLSCAExpiryAndMalformedBundle(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	ca := newAegisTestCA(t, now, 1)
	identity := ca.identity(t, now, 10, 72*time.Hour)
	p := newAegisTestProjection(t, now, identity, ca.pem)
	if _, err := p.loader.config(false); err != nil {
		t.Fatal(err)
	}
	p.clock.Store(now.Add(49 * time.Hour).UnixNano())
	if _, err := p.loader.config(false); err == nil {
		t.Fatal("expired CA must invalidate otherwise unexpired cached leaf")
	}
	for _, data := range [][]byte{nil, identity.cert, append(append([]byte(nil), ca.pem...), []byte("garbage")...), identity.key,
		append([]byte("-----BEGIN CERTIFICATE-----\nbroken\n-----END CERTIFICATE-----\n"), ca.pem...)} {
		if _, err := parseAegisTrustBundle(data); err == nil {
			t.Fatal("accepted malformed or non-CA trust bundle")
		}
	}
	p.update(t, identity.cert, make([]byte, aegisClusterMaxPEMBytes+1), ca.pem)
	files, err := readAegisTLSProjection(p.loader.paths)
	if err != nil || files[1].err == nil {
		t.Fatalf("oversized key was not rejected: %v", err)
	}
}
