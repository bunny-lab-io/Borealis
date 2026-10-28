package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func sshPrejoinBrokerFixture(t *testing.T) (*clusterSSHPrejoinCustodyBroker, clusterSSHPrejoinCustodyRequest, clusterSSHPreparationAuthority, clusterSSHPrejoinSourceCustody) {
	t.Helper()
	a, source, pod := sshPrejoinSourceFixture(t, false)
	r := clusterSSHPrejoinCustodyRequest{Source: source, Request: clusterSSHSourceBrokerRequest{Version: 1, ID: newClusterUUID(), ExpiresAt: time.Now().Unix() + 20,
		Lease: a.Lease, Baseline: a.Baseline, Binding: a.Cohort.Targets[0].Binding, Generation: "private-generation", Ciphertext: aegisEnvelopePrefix + "private-ciphertext"}}
	p, err := clusterIdentityPodCustody(pod, a.Baseline.SourceSHA, source.Process)
	if err != nil {
		t.Fatal(err)
	}
	frozen := a
	frozen.Cohort.ObservedAt = 0
	digest, _ := clusterSSHPreparationDigest(frozen)
	proof := clusterSSHPrejoinSourceCustody{Source: source, Pod: p, AuthoritySHA256: digest}
	b := newClusterSSHPrejoinCustodyBroker(nil, nil, a.Lease.ControllerHolder, sshBrokerTestSecret)
	return b, r, a, proof
}

func TestClusterSSHPrejoinCustodyBrokerBoundary(t *testing.T) {
	for _, mode := range []string{"success", "replay", "wrong owner", "qualification", "zero holder", "large generation", "expired", "future", "wrong scope", "response key", "wrong path AAD", "tampered", "unknown", "oversized", "query", "busy", "full replay cache", "source substitution", "malformed proof", "private error", "cancelled", "nil store"} {
		t.Run(mode, func(t *testing.T) {
			b, r, _, proof := sshPrejoinBrokerFixture(t)
			calls := 0
			if mode != "nil store" {
				b.read = func(ctx context.Context, got clusterSSHPrejoinCustodyRequest) (clusterSSHPrejoinSourceCustody, error) {
					calls++
					if d, ok := ctx.Deadline(); !ok || time.Until(d) > 15*time.Second {
						t.Fatal("unbounded observation")
					}
					if got != r {
						t.Fatal("request substituted")
					}
					if mode == "source substitution" {
						proof.Source.Binding.SourceUID = newClusterUUID()
					}
					if mode == "malformed proof" {
						proof.Pod.Image = "mutable:latest"
					}
					if mode == "private error" {
						return proof, errors.New("private-key-diagnostic")
					}
					return proof, nil
				}
			}
			code, status, wantCalls := 200, "observed", 1
			switch mode {
			case "wrong owner":
				r.Request.Lease.ControllerHolder = "other"
				status, wantCalls = "not_owner", 0
			case "qualification":
				r.Request.Lease.OperationStep, r.Request.Lease.Step = clusterSSHQualificationStep, "qualify"
				code, wantCalls = 401, 0
			case "zero holder":
				r.Request.Lease.Holder = "00000000-0000-0000-0000-000000000000"
				code, wantCalls = 401, 0
			case "large generation":
				r.Request.Lease.Generation = 1 << 53
				code, wantCalls = 401, 0
			case "expired":
				r.Request.ExpiresAt = time.Now().Unix() - 1
				code, wantCalls = 401, 0
			case "future":
				r.Request.ExpiresAt = time.Now().Unix() + 40
				code, wantCalls = 401, 0
			case "wrong scope", "response key", "wrong path AAD", "tampered", "unknown", "oversized":
				code, wantCalls = 401, 0
			case "query":
				code, wantCalls = 400, 0
			case "busy":
				b.active = 2
				status, wantCalls = "unavailable", 0
			case "full replay cache":
				for i := 0; i < 128; i++ {
					b.seen[newClusterUUID()] = time.Now().Unix() + 20
				}
				status, wantCalls = "unavailable", 0
			case "source substitution", "malformed proof", "private error":
				status = "unavailable"
			case "cancelled", "nil store":
				status, wantCalls = "unavailable", 0
			}
			key, path := b.requestCipher, clusterSSHPrejoinCustodyPath
			if mode == "wrong scope" {
				key, _ = clusterExistingIdentityScopedCipher(sshBrokerTestSecret, "request", "existing-identity-custody")
			}
			if mode == "response key" {
				key = b.responseCipher
			}
			if mode == "wrong path AAD" {
				path = clusterExistingIdentityCustodyPath
			}
			wire, err := sealClusterSSHVIP(key, path, r, clusterSSHSourceBrokerLimit)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "unknown" {
				wire, _ = sealClusterSSHVIP(key, path, map[string]any{"Request": r.Request, "Source": r.Source, "unknown": true}, clusterSSHSourceBrokerLimit)
			}
			if mode == "tampered" {
				wire[len(wire)-1] ^= 1
			}
			if mode == "oversized" {
				wire = make([]byte, clusterSSHSourceBrokerLimit+1)
			}
			requestPath := clusterSSHPrejoinCustodyPath
			if mode == "query" {
				requestPath += "?"
			}
			invoke := func() *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodPost, requestPath, bytes.NewReader(wire))
				req.Header.Set("Content-Type", clusterSSHSourceBrokerMedia)
				if mode == "cancelled" {
					ctx, cancel := context.WithCancel(req.Context())
					cancel()
					req = req.WithContext(ctx)
				}
				w := httptest.NewRecorder()
				b.handle(w, req)
				return w
			}
			w := invoke()
			if w.Code != code || calls != wantCalls || strings.Contains(w.Body.String(), "private-") {
				t.Fatal("unsafe response", w.Code, calls)
			}
			if code == 200 {
				var result clusterSSHPrejoinCustodyResponse
				if openClusterSSHVIP(b.responseCipher, clusterSSHPrejoinCustodyPath, w.Body.Bytes(), &result, clusterSSHSourceBrokerLimit) != nil || result.Status != status || result.ID != r.Request.ID {
					t.Fatal("wrong response")
				}
			}
			if mode == "replay" {
				w = invoke()
				var result clusterSSHPrejoinCustodyResponse
				if openClusterSSHVIP(b.responseCipher, clusterSSHPrejoinCustodyPath, w.Body.Bytes(), &result, clusterSSHSourceBrokerLimit) != nil || result.Status != "unavailable" || calls != 1 {
					t.Fatal("replayed request")
				}
			}
			if strings.Contains(fmt.Sprintf("%v %#v", r, r), "private-") {
				t.Fatal("request formatting exposed credential")
			}
		})
	}
}

func TestClusterSSHPrejoinCustodyClient(t *testing.T) {
	for _, mode := range []string{"success", "wrong proof", "wrong authority", "authority drift", "alias drift", "locked before", "locked after", "clock", "redirect", "private error", "wrong ID", "legacy", "response replay", "wrong direction", "not owner", "malformed not owner", "transport error"} {
		t.Run(mode, func(t *testing.T) {
			b, r, a, proof := sshPrejoinBrokerFixture(t)
			b.read = func(context.Context, clusterSSHPrejoinCustodyRequest) (clusterSSHPrejoinSourceCustody, error) {
				if mode == "private error" {
					return proof, errors.New("private-controller-diagnostic")
				}
				return proof, nil
			}
			calls := 0
			var retained []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls++
				if mode == "alias drift" {
					a.Source.Members[0].BootID = newClusterUUID()
				}
				if mode == "redirect" {
					http.Redirect(w, req, "/other", 307)
					return
				}
				if mode == "transport error" {
					w.WriteHeader(503)
					return
				}
				recorder := httptest.NewRecorder()
				b.handle(recorder, req)
				raw := recorder.Body.Bytes()
				var response clusterSSHPrejoinCustodyResponse
				if openClusterSSHVIP(b.responseCipher, clusterSSHPrejoinCustodyPath, raw, &response, clusterSSHSourceBrokerLimit) != nil {
					t.Error("server response failed")
					return
				}
				switch mode {
				case "wrong proof":
					response.Proof.Source.Binding.SourceUID = newClusterUUID()
				case "wrong authority":
					response.Proof.AuthoritySHA256 = strings.Repeat("b", 64)
				case "wrong ID":
					response.ID = newClusterUUID()
				case "legacy":
					response.Version = 0
				case "malformed not owner":
					response.Status = "not_owner"
				}
				key := b.responseCipher
				if mode == "wrong direction" {
					key = b.requestCipher
				}
				raw, _ = sealClusterSSHVIP(key, clusterSSHPrejoinCustodyPath, response, clusterSSHSourceBrokerLimit)
				if mode == "response replay" {
					if retained == nil {
						retained = raw
					} else {
						raw = retained
					}
				}
				w.Header().Set("Content-Type", clusterSSHSourceBrokerMedia)
				_, _ = w.Write(raw)
			}))
			defer server.Close()
			// Follower has distinct replay memory and never observes current work.
			follower := newClusterSSHPrejoinCustodyBroker(nil, nil, "not-original", sshBrokerTestSecret)
			followerServer := httptest.NewServer(http.HandlerFunc(follower.handle))
			defer followerServer.Close()
			client := &clusterSSHPrejoinCustodyClient{clusterSSHSourceBrokerClient{requestCipher: b.requestCipher, responseCipher: b.responseCipher, httpClient: server.Client(), peers: func(context.Context) ([]string, error) {
				if mode == "not owner" {
					return []string{followerServer.URL, server.URL}, nil
				}
				return []string{server.URL}, nil
			}}}
			reads := 0
			authority := func(context.Context) (clusterSSHPreparationAuthority, error) {
				reads++
				if mode == "locked before" || mode == "locked after" && reads == 2 {
					return a, errors.New("private-aegis-error")
				}
				if mode == "authority drift" && reads == 2 {
					a.PayloadSHA256 = strings.Repeat("b", 64)
				}
				if mode == "clock" {
					a.Cohort.ObservedAt++
				}
				return a, nil
			}
			got, err := client.observe(context.Background(), r.Source, r.Request.Lease, r.Request.Baseline, r.Request.sealed(), authority)
			if mode == "success" || mode == "clock" || mode == "not owner" || mode == "response replay" {
				if err != nil || got != proof || reads != 2 {
					t.Fatal("valid custody rejected", err, reads)
				}
				if mode == "response replay" {
					if _, err := client.observe(context.Background(), r.Source, r.Request.Lease, r.Request.Baseline, r.Request.sealed(), authority); err != clusterbootstrap.ErrSessionAuthority {
						t.Fatal("replayed response accepted", err)
					}
				}
			} else if err != clusterbootstrap.ErrSessionAuthority {
				t.Fatal("unsafe response accepted", err)
			}
			wantCalls := 1
			if mode == "locked before" {
				wantCalls = 0
			}
			if mode == "response replay" {
				wantCalls = 2
			}
			if calls != wantCalls {
				t.Fatal("POST retried unexpectedly", calls)
			}
		})
	}
}
