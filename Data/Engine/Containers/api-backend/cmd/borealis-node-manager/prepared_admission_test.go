package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func nativePreparedAdmissionFixture(t *testing.T) clusterbootstrap.PreparedAdmissionRequest {
	t.Helper()
	r := membershipObservationFixture(t, true)
	return clusterbootstrap.PreparedAdmissionRequest{Version: 1, Action: "PromoteCandidate", StartBefore: time.Now().Add(time.Minute).Unix(), Expected: r.Prepared[0].Expected, Receipt: r.Prepared[0].Receipt}
}

func TestPreparedAdmissionStartDeadline(t *testing.T) {
	now := time.Unix(2000000000, 0)
	r := nativePreparedAdmissionFixture(t)
	for _, value := range []int64{now.Unix(), now.Unix() - 1, now.Add(5*time.Minute + time.Second).Unix()} {
		r.StartBefore = value
		if preparedAdmissionStartValid(now, r) {
			t.Fatal("invalid start deadline accepted", value)
		}
	}
	for _, value := range []int64{now.Add(time.Second).Unix(), now.Add(5 * time.Minute).Unix()} {
		r.StartBefore = value
		if !preparedAdmissionStartValid(now, r) {
			t.Fatal("bounded start deadline rejected", value)
		}
	}
}

func TestPreparedAdmissionReconciliationWaitsForJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mutation.lock")
	producer, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	observer, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	if err := syscall.Flock(int(producer.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	busy := make(chan struct{}, 4)
	done := make(chan error, 1)
	go func() {
		done <- reconcilePreparedAdmissionStopped(ctx, time.Now().Add(time.Minute).Unix(), func() error {
			if err := syscall.Flock(int(observer.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
				if !errors.Is(err, syscall.EWOULDBLOCK) {
					return err
				}
				select {
				case busy <- struct{}{}:
				default:
				}
				return clusterbootstrap.ErrMutationBusy
			}
			return syscall.Flock(int(observer.Fd()), syscall.LOCK_UN)
		})
	}()
	for attempt := 0; attempt < 2; attempt++ {
		select {
		case <-busy:
		case err := <-done:
			t.Fatal("reconciliation quit while producer still held journal", err)
		case <-ctx.Done():
			t.Fatal("reconciliation did not retry busy journal")
		}
	}
	if err := syscall.Flock(int(producer.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("released journal failed reconciliation", err)
		}
	case <-ctx.Done():
		t.Fatal("released journal was not observed")
	}
}

func TestPreparedAdmissionReconciliationStops(t *testing.T) {
	for _, mode := range []string{"cancel before", "expired before", "cancel busy", "expire busy", "other failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			before := time.Now().Add(time.Minute).Unix()
			if mode == "cancel before" {
				cancel()
			}
			if mode == "expired before" {
				before = time.Now().Unix()
			}
			if mode == "expire busy" {
				before = time.Now().Unix() + 1
			}
			calls := 0
			failure := errors.New("journal changed")
			err := reconcilePreparedAdmissionStopped(ctx, before, func() error {
				calls++
				if mode == "other failure" {
					return failure
				}
				if mode == "cancel busy" {
					cancel()
				}
				return clusterbootstrap.ErrMutationBusy
			})
			if err == nil || mode == "other failure" && !errors.Is(err, failure) {
				t.Fatal("invalid reconciliation result", err)
			}
			if (mode == "cancel before" || mode == "expired before") && calls != 0 {
				t.Fatal("observation started after expiry/cancellation")
			}
			if (mode == "cancel busy" || mode == "other failure") && calls != 1 {
				t.Fatal("observation retried permanent failure", calls)
			}
		})
	}
}

func TestPreparedAdmissionNative(t *testing.T) {
	for _, mode := range []string{"valid", "active", "absent HMR", "HMR true", "HMR invalid", "native listener", "wrong host", "wrong identity", "identity marker", "wrong UID", "wrong boot", "wrong machine", "deleted", "mixed roles", "wrong member", "wrong certificate", "listener failure", "listener close", "HTTP close", "HTTP expiry", "cancel before", "cancel action", "late UID", "late identity", "late certificate", "command boundary", "swallowed command failure", "action failure", "caller alias"} {
		t.Run(mode, func(t *testing.T) {
			r := nativePreparedAdmissionFixture(t)
			clientName := r.Expected.Target.Hostname
			if mode == "wrong certificate" {
				clientName = "foreign"
			}
			root, _, _, cert, roots := bootstrapJoinedTLSFixture(t, r.Expected, clientName)
			if root.MkdirAll("var/lib/rancher/k3s/server/db/etcd", 0700) != nil || root.WriteFile("var/lib/rancher/k3s/server/db/etcd/name", []byte(r.Receipt.MemberName), 0600) != nil {
				t.Fatal("member fixture")
			}
			identity := t.TempDir()
			material := nodeManagerIdentityMaterial(t)
			for name, raw := range material.Files() {
				path := filepath.Join(identity, name)
				if os.MkdirAll(filepath.Dir(path), 0700) != nil || os.WriteFile(path, raw, 0600) != nil {
					t.Fatal("identity fixture")
				}
			}
			r.Receipt.Steps[3].ResultSHA256 = material.Digest()
			if mode == "wrong identity" {
				r.Receipt.Steps[3].ResultSHA256 = strings.Repeat("b", 64)
			}
			if mode == "identity marker" {
				_ = os.WriteFile(filepath.Join(identity, engineidentity.RecoveryMarkerName), []byte("{}"), 0600)
			}
			if mode == "wrong member" {
				_ = root.WriteFile("var/lib/rancher/k3s/server/db/etcd/name", []byte(r.Expected.Target.Hostname+"-87654321"), 0600)
			}
			node := bootstrapJoinedNodeDocument(r.Expected)
			meta := node["metadata"].(map[string]any)
			labels := meta["labels"].(map[string]any)
			labels["borealis.io/hmr-target"] = "false"
			switch mode {
			case "absent HMR":
				delete(labels, "borealis.io/hmr-target")
			case "HMR true":
				labels["borealis.io/hmr-target"] = "true"
			case "HMR invalid":
				labels["borealis.io/hmr-target"] = "invalid"
			}
			meta["uid"] = r.Receipt.Node.NodeUID
			if mode == "active" {
				labels["borealis.io/application-state"] = "active"
				for _, role := range []string{"control-plane", "edge", "scheduler", "postgres-primary"} {
					labels["borealis.io/"+role+"-eligible"] = "true"
				}
			}
			if mode == "mixed roles" {
				labels["borealis.io/edge-eligible"] = "true"
			}
			if mode == "wrong UID" {
				meta["uid"] = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
			}
			if mode == "deleted" {
				meta["deletionTimestamp"] = "2026-11-10T00:00:00Z"
			}
			info := node["status"].(map[string]any)["nodeInfo"].(map[string]any)
			if mode == "wrong machine" {
				info["machineID"] = strings.Repeat("e", 32)
			}
			if mode == "wrong boot" {
				info["bootID"] = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
			}
			var drift atomic.Bool
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path != "/api/v1/nodes/"+r.Expected.Target.Hostname {
					t.Error("unexpected endpoint")
					w.WriteHeader(404)
					return
				}
				raw, _ := json.Marshal(node)
				if drift.Load() && (mode == "late UID" || mode == "command boundary" || mode == "swallowed command failure") {
					raw = bytes.Replace(raw, []byte(r.Receipt.Node.NodeUID), []byte("eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"), 1)
				}
				_, _ = w.Write(raw)
			}))
			server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12}
			server.StartTLS()
			defer server.Close()
			clientFor := func(config *tls.Config) *http.Client {
				transport := &http.Transport{TLSClientConfig: config, DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
				}}
				var rt http.RoundTripper = transport
				if mode == "HTTP close" || mode == "HTTP expiry" {
					rt = preparedAdmissionRoundTrip(func(req *http.Request) (*http.Response, error) {
						response, err := transport.RoundTrip(req)
						if response != nil {
							response.Body = &preparedAdmissionBody{ReadCloser: response.Body, close: func() error {
								if mode == "HTTP expiry" {
									<-req.Context().Done()
									return nil
								}
								return errors.New("close failed")
							}}
						}
						return response, err
					})
				}
				client := &http.Client{Transport: rt, Timeout: time.Second}
				if mode == "HTTP expiry" {
					client.Timeout = 30 * time.Millisecond
				}
				return client
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel before" {
				cancel()
			}
			listenerFor := func(context.Context) (*bootstrapMembershipListenerScope, error) {
				return &bootstrapMembershipListenerScope{proofVersion: 1, check: func(context.Context) error {
					if mode == "listener failure" {
						return errBootstrapSource
					}
					return nil
				}, close: func() error {
					if mode == "listener close" {
						return errBootstrapSource
					}
					return nil
				}}, nil
			}
			if mode == "native listener" {
				f := newMembershipProcFixture(t, os.Getpid())
				listenerFor = func(ctx context.Context) (*bootstrapMembershipListenerScope, error) {
					return captureBootstrapMembershipListenerAt(ctx, f.proc, f.want, func(context.Context) ([]byte, error) { return f.service, nil })
				}
			}
			effect := filepath.Join(t.TempDir(), "effect")
			calls := 0
			var escaped context.Context
			err := withPreparedAdmissionNative(ctx, root, identity, r, func(context.Context) error {
				if mode == "wrong host" {
					return errBootstrapSource
				}
				return nil
			}, listenerFor, clientFor, func(actionCtx context.Context) error {
				calls++
				escaped = actionCtx
				f, err := os.Open(identity)
				if err != nil {
					t.Fatal(err)
				}
				if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
					t.Fatal("identity lock not held")
				}
				_ = f.Close()
				switch mode {
				case "cancel action":
					cancel()
				case "late UID", "command boundary", "swallowed command failure":
					drift.Store(true)
				case "late identity":
					_ = os.WriteFile(filepath.Join(identity, engineidentity.EngineSecretPath), []byte(strings.Repeat("x", 48)), 0600)
				case "late certificate":
					_ = root.WriteFile("var/lib/rancher/k3s/agent/server-ca.crt", []byte("changed"), 0600)
				case "action failure":
					return errors.New("failed action")
				case "caller alias":
					r.Expected.PeerAddresses[0] = "192.168.3.99"
				}
				if mode == "command boundary" || mode == "swallowed command failure" {
					_, err := run(actionCtx, "", "/usr/bin/touch", effect)
					if mode == "swallowed command failure" {
						return nil
					}
					return err
				}
				if mode == "valid" || mode == "active" || mode == "absent HMR" || mode == "native listener" || mode == "caller alias" {
					_, err := run(actionCtx, "", "/usr/bin/touch", effect)
					return err
				}
				return nil
			})
			want := mode == "valid" || mode == "active" || mode == "absent HMR" || mode == "native listener" || mode == "caller alias"
			if (err == nil) != want {
				t.Fatal("native guard", err)
			}
			_, statErr := os.Stat(effect)
			if (statErr == nil) != want {
				t.Fatal("unexpected action effect", statErr)
			}
			if escaped != nil {
				if _, err := run(escaped, "", "/usr/bin/touch", effect+"-escaped"); err == nil {
					t.Fatal("escaped guard accepted")
				}
			}
			if calls > 0 && !want && !strings.HasPrefix(mode, "late ") && mode != "listener close" && mode != "cancel action" && mode != "command boundary" && mode != "swallowed command failure" && mode != "action failure" {
				t.Fatal("action reached through initial failed guard")
			}
		})
	}
}

type preparedAdmissionRoundTrip func(*http.Request) (*http.Response, error)

func (f preparedAdmissionRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type preparedAdmissionBody struct {
	io.ReadCloser
	close func() error
}

func (b *preparedAdmissionBody) Close() error { return errors.Join(b.ReadCloser.Close(), b.close()) }

func TestPreparedAdmissionClientAndRoute(t *testing.T) {
	r := nativePreparedAdmissionFixture(t)
	payload, _ := clusterbootstrap.EncodePreparedAdmissionRequest(r)
	for _, mode := range []string{"valid", "old manager", "wrong request", "wrong verb", "extra field", "HTTP error", "redirect", "close failed", "child expiry", "cancel close", "route wrong host", "route alias", "route extra params", "wrong token"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			client := &http.Client{Transport: preparedAdmissionRoundTrip(func(req *http.Request) (*http.Response, error) {
				calls++
				body, _ := io.ReadAll(req.Body)
				if got, err := clusterbootstrap.ParsePreparedAdmissionActionRequest(body); err != nil || got.SHA256() != r.SHA256() {
					t.Fatal("client request")
				}
				ack := map[string]any{"ok": true, "verb": clusterbootstrap.PreparedAdmissionVerb, "result": map[string]any{"version": 1, "request_sha256": r.SHA256()}}
				status := 200
				switch mode {
				case "old manager", "HTTP error":
					status = 422
				case "wrong request":
					ack["result"].(map[string]any)["request_sha256"] = strings.Repeat("a", 64)
				case "wrong verb":
					ack["verb"] = "PromoteCandidate"
				case "extra field":
					ack["result"].(map[string]any)["ignored"] = true
				case "redirect":
					status = 307
				}
				raw, _ := json.Marshal(ack)
				if strings.HasPrefix(mode, "route ") || mode == "wrong token" {
					if mode == "route alias" {
						body = bytes.Replace(body, []byte(`"verb":`), []byte(`"Verb":`), 1)
					}
					if mode == "route extra params" {
						body = bytes.Replace(body, []byte(`"params":{`), []byte(`"params":{"target_sha":"extra",`), 1)
					}
					manager := &manager{nodeName: "foreign", secret: []byte(strings.Repeat("s", 32))}
					recorder := httptest.NewRecorder()
					native := httptest.NewRequest("POST", "/v1/action", bytes.NewReader(body))
					native.Header = req.Header.Clone()
					if mode == "wrong token" {
						native.Header.Set(managerTokenHeader, strings.Repeat("x", 32))
					}
					manager.authorized(manager.handleAction)(recorder, native)
					raw = recorder.Body.Bytes()
					status = recorder.Code
					want := 422
					if mode == "route alias" || mode == "route extra params" {
						want = 400
					}
					if mode == "wrong token" {
						want = 401
					}
					if status != want {
						t.Fatalf("route HTTP %d wanted %d", status, want)
					}
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Location": []string{"http://foreign/action"}}, Body: &preparedAdmissionBody{ReadCloser: io.NopCloser(bytes.NewReader(raw)), close: func() error {
					if mode == "close failed" {
						return errors.New("close")
					}
					if mode == "child expiry" {
						<-req.Context().Done()
					}
					if mode == "cancel close" {
						cancel()
					}
					return nil
				}}}, nil
			})}
			if mode == "child expiry" {
				client.Timeout = 10 * time.Millisecond
			}
			got, err := readPreparedAdmissionAcknowledgement(ctx, client, strings.Repeat("s", 32), payload)
			if (err == nil) != (mode == "valid") || err != nil && got != nil || calls != 1 {
				t.Fatal("ack boundary", err, calls)
			}
		})
	}
}

func TestPreparedAdmissionMembershipPhase(t *testing.T) {
	r := membershipObserverRequestFixture(t, true, 1)
	r.RequireListenerProof, r.RequiredListenerProofVersion, r.PreparedAdmission = true, 3, true
	node, members := membershipNativeDocuments(r)
	rawMembers, _ := json.Marshal(members)
	want := r.Nodes[1]
	parsed, err := clusterbootstrap.ParseK3sMembershipIdentity(rawMembers, clusterbootstrap.K3sJoinedNodeIdentity{Hostname: want.Node.Hostname, MachineID: want.Node.MachineID, BootID: want.Node.BootID, Address: want.Node.Address, K3sVersion: want.Node.K3sVersion}, r.Prepared[0].Expected.PeerAddresses, want.MemberName)
	if err != nil {
		t.Fatal(err)
	}
	labels := node["metadata"].(map[string]any)["labels"].(map[string]any)
	labels["borealis.io/application-state"], labels["borealis.io/hmr-target"] = "active", "false"
	for _, role := range []string{"control-plane", "edge", "scheduler", "postgres-primary"} {
		labels["borealis.io/"+role+"-eligible"] = "true"
	}
	raw, _ := json.Marshal(node)
	if got, err := observeBootstrapMembershipLocalNode(raw, r, parsed, want.MemberName); err != nil || got != want.Node {
		t.Fatal("active prepared observer rejected", err)
	}
	r.PreparedAdmission = false
	if _, err := observeBootstrapMembershipLocalNode(raw, r, parsed, want.MemberName); err == nil {
		t.Fatal("default drained observer relaxed")
	}
	r.PreparedAdmission = true
	labels["borealis.io/edge-eligible"] = "false"
	raw, _ = json.Marshal(node)
	if _, err := observeBootstrapMembershipLocalNode(raw, r, parsed, want.MemberName); err == nil {
		t.Fatal("mixed role phase accepted")
	}
}
