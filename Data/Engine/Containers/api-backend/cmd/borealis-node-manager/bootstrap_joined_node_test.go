package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"
)

func bootstrapJoinedNodeDocument(e clusterbootstrap.PreparationExpected) map[string]any {
	return map[string]any{"apiVersion": "v1", "kind": "Node", "metadata": map[string]any{"name": e.Target.Hostname, "uid": "99999999-9999-4999-8999-999999999999", "labels": map[string]any{
		"borealis.io/engine-node": "true", "borealis.io/application-state": "drained", "borealis.io/control-plane-eligible": "false", "borealis.io/edge-eligible": "false", "borealis.io/scheduler-eligible": "false", "borealis.io/postgres-primary-eligible": "false"}},
		"status": map[string]any{"nodeInfo": map[string]any{"machineID": e.Target.MachineID, "bootID": e.TargetBootID, "kubeletVersion": e.K3sVersion, "architecture": "amd64", "operatingSystem": "linux"}, "addresses": []any{map[string]any{"type": "InternalIP", "address": e.Target.Address}}, "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}}}
}

func bootstrapJoinedTLSFixture(t *testing.T, e clusterbootstrap.PreparationExpected, clientName string) (*os.Root, engineidentity.Binding, *clusterbootstrap.K3sJoinMaterial, tls.Certificate, *x509.CertPool) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	cert := func(client bool) ([]byte, []byte, tls.Certificate) {
		t.Helper()
		pub, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		template := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
		if client {
			template.SerialNumber = big.NewInt(3)
			template.Subject = pkix.Name{CommonName: "system:node:" + clientName, Organization: []string{"system:nodes"}}
			template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
			template.IPAddresses = nil
		}
		raw, err := x509.CreateCertificate(rand.Reader, template, ca, pub, key)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := x509.MarshalPKCS8PrivateKey(private)
		if err != nil {
			t.Fatal(err)
		}
		certPEM, keyPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})
		pair, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			t.Fatal(err)
		}
		return certPEM, keyPEM, pair
	}
	certPEM, keyPEM, _ := cert(true)
	_, _, server := cert(false)
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	if root.MkdirAll("var/lib/rancher/k3s/agent", 0700) != nil {
		t.Fatal("fixture parents")
	}
	for name, raw := range map[string][]byte{"server-ca.crt": caPEM, "client-kubelet.crt": certPEM, "client-kubelet.key": keyPEM} {
		if root.WriteFile("var/lib/rancher/k3s/agent/"+name, raw, 0600) != nil {
			t.Fatal("fixture write")
		}
	}
	source := engineidentity.Binding{ClusterID: e.Target.ClusterID, SourceID: "77777777-7777-4777-8777-777777777777", SourceUID: "88888888-8888-4888-8888-888888888888"}
	hash := sha256.Sum256(caPEM)
	join, err := clusterbootstrap.NewK3sJoinMaterial(e, source, []byte("K10"+hex.EncodeToString(hash[:])+"::server:private native fixture"), caPEM)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { join.Close() })
	return root, source, join, server, pool
}

func TestBootstrapJoinedNodePrivateTLSObservation(t *testing.T) {
	for _, mode := range []string{"valid", "nil client", "pending", "readiness transition", "wrong client", "wrong mode", "linked key", "unsafe parent", "CA changed", "key changed during read", "Node UID changed", "version changed", "redirect", "authority lost", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			expected := bootstrapHostFilesFixture()
			clientName := expected.Target.Hostname
			if mode == "wrong client" {
				clientName = "foreign"
			}
			root, source, join, serverCert, roots := bootstrapJoinedTLSFixture(t, expected, clientName)
			keyPath := "var/lib/rancher/k3s/agent/client-kubelet.key"
			switch mode {
			case "wrong mode":
				if root.Chmod(keyPath, 0644) != nil {
					t.Fatal("mode")
				}
			case "linked key":
				if root.Link(keyPath, "alias") != nil {
					t.Fatal("link")
				}
			case "unsafe parent":
				if root.Chmod("var/lib/rancher/k3s/agent", 0777) != nil {
					t.Fatal("parent")
				}
			case "CA changed":
				if root.WriteFile("var/lib/rancher/k3s/agent/server-ca.crt", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCert.Certificate[0]}), 0600) != nil {
					t.Fatal("CA")
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var mu sync.Mutex
			var paths []string
			nodes := 0
			lost := false
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				paths = append(paths, r.URL.Path)
				if r.Method != "GET" || r.Host != "127.0.0.1:6443" || r.Header.Get("Authorization") != "" || len(r.TLS.PeerCertificates) != 1 || r.TLS.PeerCertificates[0].Subject.CommonName != "system:node:"+expected.Target.Hostname {
					t.Error("unbound credential/endpoint")
				}
				if mode == "redirect" {
					http.Redirect(w, r, "https://foreign.invalid/", http.StatusFound)
					return
				}
				if mode == "key changed during read" {
					if root.WriteFile(keyPath, []byte("changed"), 0600) != nil {
						t.Error("key mutation")
					}
				}
				if mode == "authority lost" {
					lost = true
				}
				if mode == "cancelled" {
					cancel()
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/version":
					version := expected.K3sVersion
					if mode == "version changed" {
						version = "v1.36.4+k3s1"
					}
					json.NewEncoder(w).Encode(map[string]any{"gitVersion": version})
				case "/api/v1/nodes/" + expected.Target.Hostname:
					nodes++
					node := bootstrapJoinedNodeDocument(expected)
					if mode == "Node UID changed" && nodes == 2 {
						node["metadata"].(map[string]any)["uid"] = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
					}
					if mode == "pending" || mode == "readiness transition" && nodes == 1 {
						node["status"].(map[string]any)["conditions"] = []any{map[string]any{"type": "Ready", "status": "False"}}
					}
					json.NewEncoder(w).Encode(node)
				default:
					t.Error("unapproved path")
					http.NotFound(w, r)
				}
			}))
			server.TLS = &tls.Config{Certificates: []tls.Certificate{serverCert}, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12}
			server.StartTLS()
			defer server.Close()
			var retained *tls.Config
			factory := func(config *tls.Config) *http.Client {
				retained = config
				if mode == "nil client" {
					return nil
				}
				client := newBootstrapJoinedNodeClient(config)
				transport := client.Transport.(*http.Transport)
				if transport.Proxy != nil || !transport.DisableKeepAlives || !transport.DisableCompression {
					t.Fatal("ambient HTTP behavior")
				}
				transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
					if address != "127.0.0.1:6443" {
						t.Error("foreign dial")
					}
					return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
				}
				return client
			}
			check := func(ctx context.Context) error {
				mu.Lock()
				defer mu.Unlock()
				if ctx.Err() != nil || lost {
					return errBootstrapSource
				}
				return nil
			}
			got, err := observeBootstrapJoinedK3sNodeAt(ctx, root, expected, source, join, check, factory)
			valid := mode == "valid" || mode == "pending" || mode == "readiness transition"
			if (err == nil) != valid {
				t.Fatal("wrong native result", err)
			}
			if !valid && got != (clusterbootstrap.K3sJoinedNode{}) {
				t.Fatal("partial evidence escaped")
			}
			if retained != nil && (retained.Certificates != nil || retained.RootCAs != nil) {
				t.Fatal("private TLS scope retained identity")
			}
			mu.Lock()
			defer mu.Unlock()
			if valid && (!reflect.DeepEqual(paths, []string{"/version", "/api/v1/nodes/" + expected.Target.Hostname, "/api/v1/nodes/" + expected.Target.Hostname, "/version"}) || got.Ready == (mode == "pending")) {
				t.Fatal("missing independent reads/readiness", paths)
			}
			if mode == "wrong client" || mode == "wrong mode" || mode == "linked key" || mode == "unsafe parent" || mode == "CA changed" {
				if len(paths) != 0 {
					t.Fatal("invalid private identity reached network")
				}
			}
		})
	}
}
