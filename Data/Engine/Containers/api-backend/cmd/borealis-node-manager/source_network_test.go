package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSourceNetworkActionRejectsParametersBeforeHostRead(t *testing.T) {
	result, err := (&manager{}).execute(context.Background(), actionRequest{Verb: "InspectSourceNetwork", Params: map[string]any{"path": "/private"}})
	if err != clusterbootstrap.ErrPreparationConfig || result != nil || nodeManagerActionTimeout("InspectSourceNetwork") != 15*time.Second {
		t.Fatal("source action boundary failed")
	}
}

func TestSourceNetworkObserverRechecksRunningSupervisorAndHost(t *testing.T) {
	config, err := os.ReadFile("../../internal/clusterbootstrap/testdata/source-supervisor.json")
	if err != nil {
		t.Fatal(err)
	}
	node, err := os.ReadFile("../../internal/clusterbootstrap/testdata/source-node.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"success", "boot drift", "machine drift", "network drift", "stale Node version", "upgrade during read", "not ready", "wrong Node", "transport", "cancel", "path injection"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			identities, configs, versions, reads := 0, 0, 0, 0
			identity := func() (string, string, error) {
				identities++
				machine, boot := strings.Repeat("b", 32), "22222222-2222-4222-8222-222222222222"
				if identities > 1 {
					if mode == "boot drift" {
						boot = "22222222-2222-4222-8222-222222222223"
					}
					if mode == "machine drift" {
						machine = strings.Repeat("c", 32)
					}
				}
				return machine, boot, nil
			}
			get := func(ctx context.Context, path string) ([]byte, error) {
				reads++
				if mode == "transport" {
					return nil, errors.New("synthetic-private-response")
				}
				if mode == "cancel" {
					cancel()
				}
				switch path {
				case "/v1-k3s/config":
					configs++
					if mode == "network drift" && configs > 1 {
						return []byte(strings.ReplaceAll(string(config), "10.42.0.0", "10.44.0.0")), nil
					}
					return config, nil
				case "/api/v1/nodes/engine-01":
					if mode == "not ready" {
						return []byte(strings.ReplaceAll(string(node), `"True"`, `"False"`)), nil
					}
					if mode == "wrong Node" {
						return []byte(strings.ReplaceAll(string(node), "engine-01", "engine-02")), nil
					}
					return node, nil
				case "/version":
					versions++
					if mode == "stale Node version" || (mode == "upgrade during read" && versions > 1) {
						return []byte(`{"gitVersion":"v1.36.4+k3s1"}`), nil
					}
					return []byte(`{"gitVersion":"v1.36.3+k3s1"}`), nil
				default:
					t.Fatal("unexpected source path")
					return nil, errors.New("unexpected path")
				}
			}
			name := "engine-01"
			if mode == "path injection" {
				name = "../secrets"
			}
			observed, err := observeSourceNetwork(ctx, name, identity, get)
			if mode == "success" {
				if err != nil || observed.Validate() != nil || observed.PodCIDR != "10.42.0.0/16" || identities != 2 || configs != 2 || versions != 2 {
					t.Fatalf("incomplete observation: %v", err)
				}
			} else {
				if !errors.Is(err, clusterbootstrap.ErrPreparationConfig) || observed != (clusterbootstrap.SourceNetwork{}) {
					t.Fatal("unsafe observation accepted")
				}
				if mode == "path injection" && reads != 0 {
					t.Fatal("invalid name reached network")
				}
			}
		})
	}
}

func TestSourceNetworkHTTPSRejectsRedirectsUntrustedTLSAndOversize(t *testing.T) {
	var redirected atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			_, _ = io.WriteString(w, `{"ok":true}`)
		case "/redirect":
			http.Redirect(w, r, "/private", http.StatusFound)
		case "/private":
			redirected.Add(1)
		case "/large":
			_, _ = io.WriteString(w, strings.Repeat("s", (128<<10)+1))
		default:
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, "synthetic-private-response")
		}
	}))
	defer server.Close()
	// Build trust explicitly; default transport must not accept this test CA.
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	if raw, err := sourceNetworkHTTPGet(context.Background(), client, server.URL, "/ok"); err != nil || string(raw) != `{"ok":true}` {
		t.Fatal("trusted HTTPS read failed")
	}
	for _, path := range []string{"/redirect", "/large", "/forbidden"} {
		if raw, err := sourceNetworkHTTPGet(context.Background(), client, server.URL, path); err != clusterbootstrap.ErrPreparationConfig || raw != nil {
			t.Fatal("private HTTP boundary failed")
		}
	}
	if redirected.Load() != 0 {
		t.Fatal("redirect followed")
	}
	if _, err := sourceNetworkHTTPGet(context.Background(), &http.Client{}, server.URL, "/ok"); err == nil {
		t.Fatal("untrusted TLS accepted")
	}
	if _, err := sourceNetworkHTTPGet(context.Background(), client, "http://127.0.0.1", "/ok"); err == nil {
		t.Fatal("plaintext accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sourceNetworkHTTPGet(ctx, client, server.URL, "/ok"); err == nil {
		t.Fatal("cancelled request accepted")
	}
}
