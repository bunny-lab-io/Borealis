package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func membershipQuorumTLSFixture(t *testing.T, root *os.Root) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	raw, err := x509.CreateCertificate(rand.Reader, ca, ca, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw})
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	makeCert := func(client bool) ([]byte, []byte, tls.Certificate) {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		cert := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
		if client {
			cert.Subject = pkix.Name{CommonName: "etcd-client"}
			cert.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
		}
		raw, err := x509.CreateCertificate(rand.Reader, cert, ca, pub, key)
		if err != nil {
			t.Fatal(err)
		}
		private, err := x509.MarshalPKCS8PrivateKey(priv)
		if err != nil {
			t.Fatal(err)
		}
		certPEM, keyPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})
		pair, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			t.Fatal(err)
		}
		return certPEM, keyPEM, pair
	}
	cert, keyBytes, _ := makeCert(true)
	// Pinned K3s writes client leaf followed by CA chain.
	cert = append(cert, caPEM...)
	_, _, server := makeCert(false)
	prefix := "var/lib/rancher/k3s/server/tls/etcd/"
	if err := root.MkdirAll(strings.TrimSuffix(prefix, "/"), 0700); err != nil {
		t.Fatal("parents", err)
	}
	for name, raw := range map[string][]byte{"server-ca.crt": caPEM, "client.crt": cert, "client.key": keyBytes} {
		mode := os.FileMode(0644)
		if name == "client.key" {
			mode = 0600
		}
		if root.WriteFile(prefix+name, raw, mode) != nil {
			t.Fatal("TLS fixture write")
		}
	}
	return server, pool
}

func TestBootstrapMembershipQuorumParser(t *testing.T) {
	good := `{"header":{"cluster_id":"439067974868603833","member_id":"18446744073709551615","revision":"40533202","raft_term":"95"}}`
	for _, mode := range []string{"valid", "one key", "zero count", "wrong member", "zero cluster", "number ID", "overflow ID", "negative revision", "zero revision", "overflow revision", "zero term", "missing header", "aliased header", "duplicate header", "aliased member", "unknown header", "unknown field", "values", "more", "two keys", "count number", "trailing", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			raw := good
			switch mode {
			case "one key":
				raw = strings.TrimSuffix(good, "}") + `,"count":"1"}`
			case "zero count":
				raw = strings.TrimSuffix(good, "}") + `,"count":"0"}`
			case "wrong member":
				raw = strings.Replace(raw, "18446744073709551615", "18446744073709551614", 1)
			case "zero cluster":
				raw = strings.Replace(raw, "439067974868603833", "0", 1)
			case "number ID":
				raw = strings.Replace(raw, `"18446744073709551615"`, `18446744073709551615`, 1)
			case "overflow ID":
				raw = strings.Replace(raw, "18446744073709551615", "18446744073709551616", 1)
			case "negative revision":
				raw = strings.Replace(raw, "40533202", "-1", 1)
			case "zero revision":
				raw = strings.Replace(raw, "40533202", "0", 1)
			case "overflow revision":
				raw = strings.Replace(raw, "40533202", "9223372036854775808", 1)
			case "zero term":
				raw = strings.Replace(raw, `"95"`, `"0"`, 1)
			case "missing header":
				raw = `{}`
			case "aliased header":
				raw = strings.Replace(raw, "header", "Header", 1)
			case "duplicate header":
				raw = strings.TrimSuffix(raw, "}") + `,"header":{}}`
			case "aliased member":
				raw = strings.Replace(raw, "member_id", "Member_id", 1)
			case "unknown header":
				raw = strings.Replace(raw, `"raft_term"`, `"other"`, 1)
			case "unknown field":
				raw = strings.TrimSuffix(raw, "}") + `,"other":true}`
			case "values":
				raw = strings.TrimSuffix(raw, "}") + `,"kvs":[]}`
			case "more":
				raw = strings.TrimSuffix(raw, "}") + `,"more":false}`
			case "two keys":
				raw = strings.TrimSuffix(raw, "}") + `,"count":"2"}`
			case "count number":
				raw = strings.TrimSuffix(raw, "}") + `,"count":0}`
			case "trailing":
				raw += "{}"
			case "oversize":
				raw += strings.Repeat(" ", 4096)
			}
			got, err := parseBootstrapMembershipQuorum([]byte(raw), math.MaxUint64)
			if mode == "valid" || mode == "one key" || mode == "zero count" {
				if err != nil || got.identity.MemberID != math.MaxUint64 || got.identity.ClusterID != 439067974868603833 {
					t.Fatal(got, err)
				}
			} else if err == nil {
				t.Fatal("malformed proof accepted")
			}
		})
	}
}

func TestBootstrapMembershipQuorumNative(t *testing.T) {
	for _, mode := range []string{"valid source", "valid target", "missing gateway listener", "foreign gateway listener", "gateway drift", "wrong member", "cluster drift", "revision backwards", "term backwards", "gateway redirect", "gateway error", "key mode", "certificate mode", "certificate writable", "key replaced", "key late drift", "old capability", "listener close failed", "close failed", "client expiry", "cancel close"} {
		t.Run(mode, func(t *testing.T) {
			index := 0
			if mode == "valid target" {
				index = 1
			}
			request := membershipObserverRequestFixture(t, true, index)
			request.RequireListenerProof = true
			request.RequiredListenerProofVersion = clusterbootstrap.MembershipQuorumProofVersion
			f := newMembershipProcFixture(t, os.Getpid())
			request.Network.Kubelet = f.want
			request.Network.ManagementLink.NetworkNamespace = f.want.NetworkNamespace
			tcp := membershipTCP4Header + membershipTCPRow("0100007F", 6443, 123) + membershipTCPRow("0100007F", 2379, 456)
			if mode != "missing gateway listener" {
				tcp += membershipTCPRow("0100007F", 2382, 789)
			}
			f.write(t, f.pid+"/net/tcp", tcp)
			f.link(t, f.pid+"/fd/6", "socket:[456]")
			if mode != "foreign gateway listener" {
				f.link(t, f.pid+"/fd/7", "socket:[789]")
			}
			root, _, _, serverCert, roots := bootstrapJoinedTLSFixture(t, request.Prepared[0].Expected, request.Network.Hostname)
			root.MkdirAll("var/lib/rancher/k3s/server/db/etcd", 0700)
			root.WriteFile("var/lib/rancher/k3s/server/db/etcd/name", []byte(request.Network.Hostname+"-12345678"), 0600)
			gatewayCert, gatewayRoots := membershipQuorumTLSFixture(t, root)
			keyPath := "var/lib/rancher/k3s/server/tls/etcd/client.key"
			if mode == "key mode" {
				root.Chmod(keyPath, 0644)
			}
			if mode == "certificate mode" {
				root.Chmod("var/lib/rancher/k3s/server/tls/etcd/client.crt", 0600)
			}
			if mode == "certificate writable" {
				root.Chmod("var/lib/rancher/k3s/server/tls/etcd/server-ca.crt", 0664)
			}
			node, members := membershipNativeDocuments(request)
			localID := uint64(0)
			for _, raw := range members["members"].([]any) {
				m := raw.(map[string]any)
				if m["name"] == request.Network.Hostname+"-12345678" {
					localID = m["ID"].(uint64)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var reads, gateways, gatewayCloses atomic.Int32
			api := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads.Add(1)
				var body any
				switch r.URL.Path {
				case "/version":
					body = map[string]any{"gitVersion": request.Network.K3sVersion}
				case "/db/info":
					body = members
				case "/api/v1/nodes/" + request.Network.Hostname:
					body = node
				default:
					t.Error("unexpected API path")
					w.WriteHeader(400)
					return
				}
				json.NewEncoder(w).Encode(body)
			}))
			api.TLS = &tls.Config{Certificates: []tls.Certificate{serverCert}, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12}
			api.StartTLS()
			defer api.Close()
			gateway := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				round := gateways.Add(1)
				body, _ := io.ReadAll(r.Body)
				if r.Method != "POST" || r.Host != "127.0.0.1:2382" || r.URL.Path != "/v3/kv/range" || string(body) != bootstrapMembershipQuorumBody || len(r.TLS.PeerCertificates) < 1 || r.TLS.PeerCertificates[0].Subject.CommonName != "etcd-client" {
					t.Error("fixed quorum request escaped")
				}
				if mode == "gateway redirect" {
					http.Redirect(w, r, "https://127.0.0.1:6443/other", 302)
					return
				}
				if mode == "gateway error" {
					w.WriteHeader(503)
					return
				}
				member, cluster, revision, term := localID, uint64(100), uint64(round+100), uint64(2)
				if mode == "wrong member" {
					member--
				}
				if mode == "cluster drift" && round == 2 {
					cluster++
				}
				if mode == "revision backwards" && round == 2 {
					revision = 1
				}
				if mode == "term backwards" && round == 2 {
					term = 1
				}
				if mode == "gateway drift" {
					f.link(t, f.pid+"/fd/7", "socket:[790]")
				}
				if mode == "key replaced" {
					raw, _ := root.ReadFile(keyPath)
					root.Remove(keyPath)
					root.WriteFile(keyPath, raw, 0600)
				}
				fmt.Fprintf(w, `{"header":{"cluster_id":"%d","member_id":"%d","revision":"%d","raft_term":"%d"}}`, cluster, member, revision, term)
			}))
			gateway.TLS = &tls.Config{Certificates: []tls.Certificate{gatewayCert}, ClientCAs: gatewayRoots, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12}
			gateway.StartTLS()
			defer gateway.Close()
			factory := func(config *tls.Config) *http.Client {
				client := newBootstrapJoinedNodeClient(config)
				client.Transport.(*http.Transport).DialContext = func(ctx context.Context, kind, address string) (net.Conn, error) {
					destination := api.Listener.Addr().String()
					if address == "127.0.0.1:2382" {
						destination = gateway.Listener.Addr().String()
					} else if address != "127.0.0.1:6443" {
						t.Error("foreign endpoint")
					}
					return (&net.Dialer{}).DialContext(ctx, kind, destination)
				}
				if mode == "close failed" || mode == "client expiry" || mode == "cancel close" {
					transport := client.Transport
					client.Transport = &sourceNetworkCleanupTransport{roundTrip: func(req *http.Request) (*http.Response, error) {
						response, err := transport.RoundTrip(req)
						if req.URL.Port() == "2382" && response != nil && response.Body != nil {
							response.Body = &sourceNetworkCleanupBody{ReadCloser: response.Body, close: func() error {
								gatewayCloses.Add(1)
								if mode == "close failed" {
									return errors.New("close")
								}
								if mode == "cancel close" {
									cancel()
									return nil
								}
								<-req.Context().Done()
								return nil
							}}
						}
						return response, err
					}}
					if leaf, err := x509.ParseCertificate(config.Certificates[0].Certificate[0]); mode == "client expiry" && err == nil && leaf.Subject.CommonName == "etcd-client" {
						client.Timeout = 150 * time.Millisecond
					}
				}
				return client
			}
			listener := func(ctx context.Context, want clusterbootstrap.SourceKubelet, version uint8) (*bootstrapMembershipListenerScope, error) {
				if mode == "old capability" {
					version = clusterbootstrap.MembershipLocalEtcdProofVersion
				}
				scope, err := captureBootstrapMembershipListenersAt(ctx, f.proc, want, func(context.Context) ([]byte, error) { return f.service, nil }, version)
				if err == nil && mode == "listener close failed" {
					close := scope.close
					scope.close = func() error { _ = close(); return errors.New("owned listener close") }
				}
				return scope, err
			}
			source := func(context.Context) (clusterbootstrap.SourceNetwork, error) {
				if mode == "key late drift" && gateways.Load() == 2 {
					root.Chmod(keyPath, 0644)
				}
				return request.Network, nil
			}
			got, err := observeBootstrapMembershipAt(ctx, root, request, func(ctx context.Context) error { return ctx.Err() }, source, factory, listener)
			if strings.HasPrefix(mode, "valid ") {
				if err != nil || !got.Valid(clusterbootstrap.MembershipObservationRequest(request)) || got.Quorum == nil || got.Quorum.MemberID != localID || got.Quorum.ClusterID != 100 || gateways.Load() != 2 || reads.Load() != 6 {
					t.Fatal("complete native quorum proof", err, gateways.Load(), reads.Load())
				}
			} else if err == nil || !reflect.DeepEqual(got, bootstrapMembershipObservation{}) {
				t.Fatal("invalid quorum evidence returned", err)
			}
			if (mode == "close failed" || mode == "client expiry" || mode == "cancel close") && gatewayCloses.Load() != 1 {
				t.Fatal("new fixed POST did not join cleanup")
			}
			if mode == "client expiry" && ctx.Err() != nil {
				t.Fatal("fixture expired outer scope")
			}
		})
	}
}
