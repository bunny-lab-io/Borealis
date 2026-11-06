package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func sshJoinSourceFixture(t *testing.T) (clusterSSHPreparationAuthority, clusterbootstrap.K3sJoinSourceRequest, []byte) {
	t.Helper()
	cohort, source, lease, baseline := sshPreparationFixture(t)
	member := source.Members[0]
	binding := engineidentity.Binding{ClusterID: source.ClusterID, SourceID: member.NodeID, SourceUID: member.NodeUID}
	authority := clusterSSHPreparationAuthority{Cohort: cohort, Source: source, Lease: lease, Baseline: baseline, K3sVersion: clusterbootstrap.K3sPins().Version, IdentitySource: &binding}
	network := sshSourceNetworkFixture(member, authority.K3sVersion)
	expected, err := buildClusterSSHPreparationExpected(cohort, source, lease, baseline, authority.K3sVersion, network.PodCIDR, network.ServiceCIDR)
	if err != nil {
		t.Fatal(err)
	}
	r := clusterbootstrap.K3sJoinSourceRequest{Version: 1, ID: newClusterUUID(), ExpiresAt: time.Now().Unix() + 80, Expected: expected, Source: binding, Network: network}
	if err := r.Validate(time.Now()); err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, public, private)
	if err != nil {
		t.Fatal(err)
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	digest := sha256.Sum256(ca)
	material, err := clusterbootstrap.NewK3sJoinMaterial(expected, binding, []byte("K10"+hex.EncodeToString(digest[:])+"::server:private fixture token"), ca)
	if err != nil {
		t.Fatal(err)
	}
	defer material.Close()
	var frame bytes.Buffer
	if err := material.WriteJoinMaterial(context.Background(), expected, binding, &frame, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	return authority, r, frame.Bytes()
}

func sendJoinCallback(b *clusterSSHJoinSourceDeliveries, id string, wire []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", clusterbootstrap.K3sJoinSourcePath, bytes.NewReader(wire))
	r.Header.Set("Content-Type", "application/octet-stream")
	r.Header.Set(clusterbootstrap.K3sJoinSourceRequestHeader, id)
	w := httptest.NewRecorder()
	b.handle(w, r)
	return w
}

func TestClusterSSHJoinDeliveryBoundedScopeAndNativeReceipt(t *testing.T) {
	_, request, frame := sshJoinSourceFixture(t)
	secret := strings.Repeat("s", 32)
	b := newClusterSSHJoinSourceDeliveries(secret)
	closePending, err := b.register(request)
	if err != nil {
		t.Fatal(err)
	}
	defer closePending()
	if closeAgain, err := b.register(request); err == nil || closeAgain != nil {
		t.Fatal("duplicate registration")
	}
	second := request
	second.ID = newClusterUUID()
	closeSecond, err := b.register(second)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSecond()
	third := request
	third.ID = newClusterUUID()
	if _, err := b.register(third); err == nil {
		t.Fatal("unbounded pending reads")
	}
	job, pod := newClusterUUID(), newClusterUUID()
	wire, receipt, err := clusterbootstrap.SealK3sJoinSourceDelivery([]byte(secret), request, job, pod, frame, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if w := sendJoinCallback(b, third.ID, wire); w.Code != 404 {
		t.Fatal("unregistered callback accepted")
	}
	if w := sendJoinCallback(b, request.ID, wire); w.Code != 204 {
		t.Fatal("callback failed", w.Code)
	}
	if w := sendJoinCallback(b, request.ID, wire); w.Code != 204 {
		t.Fatal("identical acknowledgement retry failed")
	}
	other, _, _ := clusterbootstrap.SealK3sJoinSourceDelivery([]byte(secret), request, job, pod, frame, time.Now())
	if w := sendJoinCallback(b, request.ID, other); w.Code != 409 {
		t.Fatal("second delivery replaced first")
	}
	check := func(context.Context) error { return nil }
	bad := receipt
	bad.CiphertextSHA256 = strings.Repeat("0", 64)
	if m, err := b.finish(context.Background(), request, bad, check); err == nil || m != nil {
		t.Fatal("HTTP alone granted native authority")
	}
	owned := b.pending[request.ID].wire
	m, err := b.finish(context.Background(), request, receipt, check)
	if err != nil {
		t.Fatal(err)
	}
	m.Close()
	if !bytes.Equal(owned, make([]byte, len(owned))) {
		t.Fatal("consumed ciphertext retained")
	}
	if m, err := b.finish(context.Background(), request, receipt, check); err == nil || m != nil {
		t.Fatal("receipt reused")
	}
	if w := sendJoinCallback(b, request.ID, wire); w.Code != 409 {
		t.Fatal("consumed delivery revived")
	}
	closePending()
	closePending()
	if w := sendJoinCallback(b, request.ID, wire); w.Code != 404 {
		t.Fatal("closed delivery revived")
	}
	if len(b.pending) != 1 {
		t.Fatal("scope cleanup lost other request")
	}
}

func TestClusterSSHJoinDeliveryHTTPBoundary(t *testing.T) {
	_, request, frame := sshJoinSourceFixture(t)
	secret := strings.Repeat("s", 32)
	for _, mode := range []string{"wrong key", "tamper", "expired", "query", "force query", "encoding", "media", "oversize", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			b := newClusterSSHJoinSourceDeliveries(secret)
			closePending, err := b.register(request)
			if err != nil {
				t.Fatal(err)
			}
			defer closePending()
			key := secret
			if mode == "wrong key" {
				key = strings.Repeat("x", 32)
			}
			wire, _, err := clusterbootstrap.SealK3sJoinSourceDelivery([]byte(key), request, newClusterUUID(), newClusterUUID(), frame, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if mode == "tamper" {
				wire[len(wire)-1]++
			}
			if mode == "oversize" {
				wire = make([]byte, clusterbootstrap.MaxK3sJoinSourceDeliveryBytes+1)
			}
			if mode == "expired" {
				b.pending[request.ID].request.ExpiresAt = time.Now().Unix() - 1
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := httptest.NewRequest("POST", clusterbootstrap.K3sJoinSourcePath, bytes.NewReader(wire)).WithContext(ctx)
			r.Header.Set("Content-Type", "application/octet-stream")
			r.Header.Set(clusterbootstrap.K3sJoinSourceRequestHeader, request.ID)
			if mode == "query" {
				r.URL.RawQuery = "source=other"
			}
			if mode == "force query" {
				r.URL.ForceQuery = true
			}
			if mode == "encoding" {
				r.Header.Set("Content-Encoding", "gzip")
			}
			if mode == "media" {
				r.Header.Set("Content-Type", "application/json")
			}
			if mode == "cancel" {
				cancel()
			}
			w := httptest.NewRecorder()
			b.handle(w, r)
			if w.Code < 400 || len(b.pending[request.ID].wire) != 0 || w.Body.String() != "source join delivery unavailable\n" {
				t.Fatal("invalid callback retained private data", w.Code)
			}
		})
	}
}

func sshJoinSourcePod(t *testing.T, job map[string]any, podUID, message string) map[string]any {
	t.Helper()
	metadata := sourceActionMap(job, "metadata")
	return map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"namespace": "borealis", "uid": podUID,
		"labels":          map[string]any{"batch.kubernetes.io/controller-uid": metadata["uid"]},
		"ownerReferences": []any{map[string]any{"apiVersion": "batch/v1", "kind": "Job", "name": metadata["name"], "uid": metadata["uid"], "controller": true}}},
		"spec": sourceActionCopy(t, sourceActionMap(sourceActionMap(sourceActionMap(job, "spec"), "template"), "spec")),
		"status": map[string]any{"phase": "Succeeded", "containerStatuses": []any{map[string]any{"name": "action", "restartCount": 0, "lastState": map[string]any{},
			"state": map[string]any{"terminated": map[string]any{"exitCode": 0, "reason": "Completed", "message": message}}}}}}
}

func TestClusterSSHJoinSourceActualJobProofAndAuthority(t *testing.T) {
	for _, mode := range []string{"complete", "lost authority", "wrong controller", "foreign source", "different expected", "lost POST", "job replaced", "job failed", "two pods", "wrong owner", "privileged", "secret reference changed", "no callback", "forged callback", "invalid private frame", "private receipt", "cancel", "incomplete"} {
		t.Run(mode, func(t *testing.T) {
			current, request, frame := sshJoinSourceFixture(t)
			secret := strings.Repeat("s", 32)
			b := newClusterSSHJoinSourceDeliveries(secret)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var posted atomic.Bool
			authority := func(context.Context) (clusterSSHPreparationAuthority, error) {
				if mode == "lost authority" && posted.Load() {
					return clusterSSHPreparationAuthority{}, errors.New("private authority failure")
				}
				return current, nil
			}
			var job map[string]any
			podUID, message := newClusterUUID(), ""
			var requests atomic.Int64
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer synthetic-private" {
					t.Error("Kubernetes authentication missing")
				}
				if r.Method == "POST" {
					requests.Add(1)
					if json.NewDecoder(r.Body).Decode(&job) != nil {
						t.Error("invalid manifest")
						w.WriteHeader(500)
						return
					}
					container := anySlice(sourceActionMap(sourceActionMap(sourceActionMap(job, "spec"), "template"), "spec")["containers"])[0].(map[string]any)
					args := clusterStringSlice(container["args"])
					var err error
					if len(args) != 1 {
						t.Error("private/public request shape")
						w.WriteHeader(500)
						return
					}
					request, err = clusterbootstrap.ParseK3sJoinSourceRequest([]byte(args[0]), time.Now())
					if err != nil || bytes.Contains([]byte(args[0]), []byte("private fixture token")) {
						t.Error("invalid public Job args")
						w.WriteHeader(500)
						return
					}
					uid := newClusterUUID()
					sourceActionMap(job, "metadata")["uid"] = uid
					job["status"] = map[string]any{"succeeded": 1, "conditions": []any{map[string]any{"type": "Complete", "status": "True"}}}
					if mode == "complete" {
						expected, _ := clusterSSHJoinSourceJobManifest(sourceActionMap(job, "metadata")["name"].(string), "registry.example/api@sha256:"+strings.Repeat("a", 64), request)
						if !validClusterSSHSourceJob(job, expected, uid) {
							t.Error("fixture Job fails independent contract")
						}
					}
					if mode == "invalid private frame" {
						frame = []byte("invalid private frame")
					}
					wire, receipt, err := clusterbootstrap.SealK3sJoinSourceDelivery([]byte(secret), request, uid, podUID, frame, time.Now())
					if err != nil {
						t.Error(err)
						w.WriteHeader(500)
						return
					}
					public, _ := json.Marshal(receipt)
					message = string(public)
					if mode == "forged callback" {
						wire, _, _ = clusterbootstrap.SealK3sJoinSourceDelivery([]byte(secret), request, uid, podUID, frame, time.Now())
					}
					if mode != "no callback" && sendJoinCallback(b, request.ID, wire).Code != 204 {
						t.Error("callback rejected")
					}
					posted.Store(true)
					if mode == "cancel" {
						cancel()
					}
					if mode == "lost POST" {
						w.WriteHeader(500)
						return
					}
					w.WriteHeader(201)
					json.NewEncoder(w).Encode(job)
					return
				}
				if r.Method != "GET" || job == nil {
					t.Error("unexpected lookup")
					w.WriteHeader(500)
					return
				}
				if strings.HasPrefix(r.URL.Path, "/apis/batch/") {
					out := sourceActionCopy(t, job)
					if mode == "job replaced" {
						sourceActionMap(out, "metadata")["uid"] = newClusterUUID()
					}
					if mode == "job failed" {
						sourceActionMap(out, "status")["failed"] = 1
					}
					json.NewEncoder(w).Encode(out)
					return
				}
				pod := sshJoinSourcePod(t, job, podUID, message)
				if mode == "complete" {
					if _, complete, err := clusterSSHSourcePodMessage(pod, job, request.ID, sourceActionMap(job, "metadata")["uid"].(string)); err != nil || !complete {
						t.Error("fixture Pod fails independent contract", err)
					}
				}
				if mode == "wrong owner" {
					anySlice(sourceActionMap(pod, "metadata")["ownerReferences"])[0].(map[string]any)["uid"] = newClusterUUID()
				}
				container := anySlice(sourceActionMap(pod, "spec")["containers"])[0].(map[string]any)
				if mode == "privileged" {
					sourceActionMap(container, "securityContext")["privileged"] = true
				}
				if mode == "secret reference changed" {
					env := anySlice(container["env"])[2].(map[string]any)
					sourceActionMap(sourceActionMap(env, "valueFrom"), "secretKeyRef")["name"] = "other"
				}
				if mode == "private receipt" {
					terminated := sourceActionMap(sourceActionMap(anySlice(sourceActionMap(pod, "status")["containerStatuses"])[0].(map[string]any), "state"), "terminated")
					terminated["message"] = "private fixture token"
				}
				if mode == "incomplete" {
					sourceActionMap(pod, "status")["phase"] = "Running"
					cancel()
				}
				items := []any{pod}
				if mode == "two pods" {
					items = append(items, pod)
				}
				json.NewEncoder(w).Encode(map[string]any{"items": items})
			}))
			defer server.Close()
			runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, token: "synthetic-private", httpClient: server.Client()}, namespace: "borealis", controllerHolder: current.Lease.ControllerHolder, actionImage: "registry.example/api@sha256:" + strings.Repeat("a", 64), jobPollInterval: time.Millisecond}
			if mode == "wrong controller" {
				runner.controllerHolder = "other"
			}
			if mode == "foreign source" {
				request.Source.SourceID = newClusterUUID()
			}
			if mode == "different expected" {
				request.Expected.Target.Generation++
			}
			m, err := runner.readSSHSourceK3sJoin(ctx, b, authority, request.Expected, request.Source, request.Network)
			if mode == "complete" {
				if err != nil || m == nil {
					t.Fatal("complete source rejected", err)
				}
				m.Close()
			} else if err != clusterbootstrap.ErrK3sJoinMaterial || m != nil {
				t.Fatal("unproven source accepted", err)
			}
			b.mu.Lock()
			pending := len(b.pending)
			b.mu.Unlock()
			if pending != 0 || requests.Load() > 1 {
				t.Fatal("pending material leaked or Job replayed")
			}
		})
	}
}
