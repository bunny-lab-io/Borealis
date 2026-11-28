package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func existingIdentityBrokerFixture(t *testing.T) (*clusterExistingIdentityBroker, clusterExistingIdentityBrokerRequest, clusterExistingIdentityPlan) {
	t.Helper()
	p := existingIdentityPlanFixture(t)
	b := newClusterExistingIdentityBroker(&clusterController{holder: p.Controller}, nil, strings.Repeat("s", 64))
	r := clusterExistingIdentityBrokerRequest{Version: 1, ID: newClusterUUID(), ExpiresAt: time.Now().Unix() + 20, Controller: p.Controller, TargetID: p.TargetID, Actor: "admin",
		Input: clusterExistingIdentityPlanInput{RequestID: p.OperationID, MaintenanceID: p.MaintenanceID, SourceID: p.SourceID, Confirmation: "PLAN IDENTITY RECOVERY"}}
	return b, r, p
}

func TestClusterExistingIdentityBrokerBoundary(t *testing.T) {
	for _, mode := range []string{"success", "replay", "wrong owner", "expired", "future", "actor", "target source", "unknown", "alias", "duplicate", "old broker key", "wrong direction", "tampered", "oversize", "query", "encoding", "busy", "full replay memory", "nil replay memory", "reservation error", "substituted plan", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			b, r, p := existingIdentityBrokerFixture(t)
			calls := 0
			b.reserve = func(ctx context.Context, req clusterExistingIdentityBrokerRequest) (clusterExistingIdentityPlan, error) {
				calls++
				if d, ok := ctx.Deadline(); !ok || time.Until(d) > 15*time.Second {
					t.Error("unbounded controller reservation")
				}
				if mode == "reservation error" {
					return p, errors.New("private-db-error")
				}
				if mode == "substituted plan" {
					p.SourceID = p.TargetID
				}
				return p, nil
			}
			wantStatus, wantCode, wantCalls := "reserved", 200, 1
			switch mode {
			case "wrong owner":
				r.Controller = "other"
				wantStatus, wantCalls = "not_owner", 0
			case "expired":
				r.ExpiresAt = time.Now().Unix() - 1
				wantCode, wantCalls = 401, 0
			case "future":
				r.ExpiresAt = time.Now().Unix() + 60
				wantCode, wantCalls = 401, 0
			case "actor":
				r.Actor = "admin\n"
				wantCode, wantCalls = 401, 0
			case "target source":
				r.TargetID = r.Input.SourceID
				wantCode, wantCalls = 401, 0
			case "unknown", "alias", "duplicate", "old broker key", "wrong direction", "tampered", "oversize":
				wantCode, wantCalls = 401, 0
			case "query", "encoding":
				wantCode, wantCalls = 400, 0
			case "busy":
				b.active = 2
				wantStatus, wantCalls = "unavailable", 0
			case "full replay memory":
				for i := 0; i < 128; i++ {
					b.seen[newClusterUUID()] = time.Now().Unix() + 20
				}
				wantStatus, wantCalls = "unavailable", 0
			case "nil replay memory":
				b.seen = nil
				wantStatus, wantCalls = "unavailable", 0
			case "reservation error", "substituted plan", "cancelled":
				wantStatus = "unavailable"
			}
			wire, _ := sealClusterExistingIdentityBroker(b.requestCipher, r)
			switch mode {
			case "unknown", "alias", "duplicate":
				raw := marshalClusterJSON(r)
				if mode == "unknown" {
					raw = strings.Replace(raw, "{", `{"extra":1,`, 1)
				}
				if mode == "duplicate" {
					raw = strings.Replace(raw, "{", `{"version":1,`, 1)
				}
				if mode == "alias" {
					raw = strings.Replace(raw, `"version"`, `"Version"`, 1)
				}
				wire = b.requestCipher.Seal(nil, nil, []byte(raw), []byte(clusterExistingIdentityBrokerPath))
			case "old broker key":
				old, _ := clusterSSHSourceBrokerCipher(strings.Repeat("s", 64), "request")
				wire, _ = sealClusterExistingIdentityBroker(old, r)
			case "wrong direction":
				wire, _ = sealClusterExistingIdentityBroker(b.responseCipher, r)
			case "tampered":
				wire[len(wire)-1] ^= 1
			case "oversize":
				wire = make([]byte, clusterExistingIdentityBrokerLimit+1)
			}
			invoke := func() clusterExistingIdentityBrokerResponse {
				path := clusterExistingIdentityBrokerPath
				if mode == "query" {
					path += "?"
				}
				req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(wire))
				req.Header.Set("Content-Type", clusterSSHSourceBrokerMedia)
				if mode == "encoding" {
					req.Header.Set("Content-Encoding", "gzip")
				}
				if mode == "cancelled" {
					ctx, cancel := context.WithCancel(req.Context())
					cancel()
					req = req.WithContext(ctx)
				}
				w := httptest.NewRecorder()
				b.handle(w, req)
				if w.Code != wantCode || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "private-db-error") {
					t.Fatal("unsafe broker response", w.Code)
				}
				var value clusterExistingIdentityBrokerResponse
				if wantCode == 200 {
					if openClusterExistingIdentityBroker(b.responseCipher, w.Body.Bytes(), &value) != nil || value.ID != r.ID || value.Status != wantStatus {
						t.Fatal("unbound response", value.Status)
					}
				}
				return value
			}
			value := invoke()
			if wantStatus == "reserved" && wantCode == 200 && (value.Plan == nil || !r.matches(*value.Plan)) {
				t.Fatal("missing exact plan")
			}
			if mode == "replay" {
				wantStatus = "unavailable"
				invoke()
			}
			if calls != wantCalls {
				t.Fatal("unexpected dispatch", calls, wantCalls)
			}
		})
	}
}

func TestClusterExistingIdentityBrokerClient(t *testing.T) {
	for _, mode := range []string{"success", "not owner", "unavailable", "redirect", "lost acknowledgement", "wrong id", "wrong plan", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			b, r, p := existingIdentityBrokerFixture(t)
			b.reserve = func(context.Context, clusterExistingIdentityBrokerRequest) (clusterExistingIdentityPlan, error) {
				return p, nil
			}
			var firstCalls, secondCalls atomic.Int32
			second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { secondCalls.Add(1); b.handle(w, req) }))
			defer second.Close()
			first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				firstCalls.Add(1)
				if mode == "redirect" {
					http.Redirect(w, req, second.URL+clusterExistingIdentityBrokerPath, 307)
					return
				}
				if mode == "lost acknowledgement" {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err == nil {
						_ = conn.Close()
					}
					return
				}
				if mode == "success" {
					b.handle(w, req)
					return
				}
				_, _ = io.Copy(io.Discard, req.Body)
				response := clusterExistingIdentityBrokerResponse{Version: 1, ID: r.ID, Status: "unavailable"}
				if mode == "not owner" {
					response.Status = "not_owner"
				}
				if mode == "wrong id" {
					response.ID = newClusterUUID()
					response.Status = "reserved"
					response.Plan = &p
				}
				if mode == "wrong plan" {
					copy := p
					copy.OperationID = newClusterUUID()
					response.Status = "reserved"
					response.Plan = &copy
				}
				wire, _ := sealClusterExistingIdentityBroker(b.responseCipher, response)
				w.Header().Set("Content-Type", clusterSSHSourceBrokerMedia)
				_, _ = w.Write(wire)
			}))
			defer first.Close()
			client := &clusterExistingIdentityBrokerClient{requestCipher: b.requestCipher, responseCipher: b.responseCipher, httpClient: first.Client(), peers: func(context.Context) ([]string, error) { return []string{first.URL, second.URL}, nil }}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			got, err := client.reserve(ctx, r)
			ok := mode == "success" || mode == "not owner"
			if (err == nil) != ok || (ok && !r.matches(got)) {
				t.Fatal("client authority boundary", err)
			}
			wantSecond := int32(0)
			if mode == "not owner" {
				wantSecond = 1
			}
			if secondCalls.Load() != wantSecond {
				t.Fatal("unsafe automatic replay", secondCalls.Load())
			}
			if mode == "cancelled" && firstCalls.Load() != 0 {
				t.Fatal("cancelled dispatch")
			}
		})
	}
}
