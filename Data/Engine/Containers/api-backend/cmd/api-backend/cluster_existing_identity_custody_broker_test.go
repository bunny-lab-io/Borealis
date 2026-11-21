package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClusterExistingIdentityCustodyBrokerBoundary(t *testing.T) {
	for _, mode := range []string{"success", "replay", "wrong owner", "expired", "future", "wrong phase", "plan key", "response key", "tampered", "unknown", "query", "busy", "source substitution", "private error", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			c, process := existingIdentityInspectionClaimFixture(t)
			proof := existingIdentityInspectionResultFixture(c, process).Source
			b := newClusterExistingIdentityCustodyBroker(&clusterController{holder: c.Plan.Controller}, nil, strings.Repeat("s", 64))
			r := clusterExistingIdentityCustodyRequest{Version: 1, ID: newClusterUUID(), ExpiresAt: time.Now().Unix() + 20, Lease: c.Lease, Process: process}
			calls := 0
			b.read = func(ctx context.Context, request clusterExistingIdentityCustodyRequest) (clusterExistingIdentitySourceCustody, string, error) {
				calls++
				if d, ok := ctx.Deadline(); !ok || time.Until(d) > 15*time.Second {
					t.Fatal("unbounded custody")
				}
				if request.Lease != c.Lease || request.Process != process {
					t.Fatal("wrong custody binding")
				}
				if mode == "private error" {
					return proof, "", errors.New("private credential data")
				}
				if mode == "source substitution" {
					proof.Process.PodUID = newClusterUUID()
				}
				return proof, strings.Repeat("a", 64), nil
			}
			code, status, wantCalls := 200, "observed", 1
			switch mode {
			case "wrong owner":
				r.Lease.ControllerHolder = "other"
				status, wantCalls = "not_owner", 0
			case "expired":
				r.ExpiresAt = time.Now().Unix() - 1
				code, wantCalls = 401, 0
			case "future":
				r.ExpiresAt = time.Now().Unix() + 60
				code, wantCalls = 401, 0
			case "wrong phase":
				r.Lease.Step = "recover_identity"
				code, wantCalls = 401, 0
			case "plan key", "response key", "tampered", "unknown":
				code, wantCalls = 401, 0
			case "query":
				code, wantCalls = 400, 0
			case "busy":
				b.active = 2
				status, wantCalls = "unavailable", 0
			case "source substitution", "private error", "cancelled":
				status = "unavailable"
			}
			key := b.requestCipher
			if mode == "plan key" {
				key, _ = clusterExistingIdentityBrokerCipher(strings.Repeat("s", 64), "request")
			}
			if mode == "response key" {
				key = b.responseCipher
			}
			wire, _ := sealClusterExistingIdentityScopedBroker(key, r, clusterExistingIdentityCustodyPath)
			if mode == "unknown" {
				wire, _ = sealClusterExistingIdentityScopedBroker(key, map[string]any{"Version": 1, "ID": r.ID, "unknown": true}, clusterExistingIdentityCustodyPath)
			}
			if mode == "tampered" {
				wire[len(wire)-1] ^= 1
			}
			path := clusterExistingIdentityCustodyPath
			if mode == "query" {
				path += "?"
			}
			request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(wire))
			request.Header.Set("Content-Type", clusterSSHSourceBrokerMedia)
			if mode == "cancelled" {
				ctx, cancel := context.WithCancel(request.Context())
				cancel()
				request = request.WithContext(ctx)
			}
			w := httptest.NewRecorder()
			b.handle(w, request)
			if w.Code != code || calls != wantCalls || strings.Contains(w.Body.String(), "private") {
				t.Fatal("unsafe broker response", w.Code, calls)
			}
			if code == 200 {
				var response clusterExistingIdentityCustodyResponse
				if openClusterExistingIdentityScopedBroker(b.responseCipher, w.Body.Bytes(), &response, clusterExistingIdentityCustodyPath) != nil || response.Status != status {
					t.Fatal("invalid broker envelope")
				}
			}
			if mode == "replay" {
				request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(wire))
				request.Header.Set("Content-Type", clusterSSHSourceBrokerMedia)
				w = httptest.NewRecorder()
				b.handle(w, request)
				var response clusterExistingIdentityCustodyResponse
				if openClusterExistingIdentityScopedBroker(b.responseCipher, w.Body.Bytes(), &response, clusterExistingIdentityCustodyPath) != nil || response.Status != "unavailable" || calls != 1 {
					t.Fatal("replayed custody request")
				}
			}
		})
	}
}

func TestClusterExistingIdentityCustodyClient(t *testing.T) {
	for _, mode := range []string{"success", "wrong proof", "authority drift", "redirect", "private error"} {
		t.Run(mode, func(t *testing.T) {
			c, process := existingIdentityInspectionClaimFixture(t)
			proof := existingIdentityInspectionResultFixture(c, process).Source
			b := newClusterExistingIdentityCustodyBroker(&clusterController{holder: c.Plan.Controller}, nil, strings.Repeat("s", 64))
			b.read = func(context.Context, clusterExistingIdentityCustodyRequest) (clusterExistingIdentitySourceCustody, string, error) {
				if mode == "wrong proof" {
					proof.Image = "mutable:latest"
				}
				if mode == "private error" {
					return proof, "", errors.New("private")
				}
				return proof, strings.Repeat("a", 64), nil
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if mode == "redirect" {
					http.Redirect(w, r, "/other", 307)
					return
				}
				b.handle(w, r)
			}))
			defer server.Close()
			client := &clusterExistingIdentityCustodyClient{clusterExistingIdentityBrokerClient{requestCipher: b.requestCipher, responseCipher: b.responseCipher, httpClient: server.Client(), peers: func(context.Context) ([]string, error) { return []string{server.URL}, nil }}}
			reads := 0
			got, err := client.observe(context.Background(), c, process, func(context.Context) (string, error) {
				reads++
				if mode == "authority drift" && reads > 1 {
					return strings.Repeat("b", 64), nil
				}
				return strings.Repeat("a", 64), nil
			})
			if mode == "success" {
				if err != nil || got != proof || reads != 2 {
					t.Fatal("custody client failed", err)
				}
			} else if err == nil {
				t.Fatal("unsafe custody response accepted")
			}
			if calls != 1 {
				t.Fatal("POST replayed", calls)
			}
		})
	}
}
