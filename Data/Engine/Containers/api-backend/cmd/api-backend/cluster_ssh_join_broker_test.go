package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
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

func sshJoinBrokerFixture(t *testing.T) (*clusterSSHJoinBroker, clusterSSHJoinBrokerRequest, clusterSSHPreparationAuthority, []byte) {
	t.Helper()
	a, public, frame := sshJoinSourceFixture(t)
	r := clusterSSHJoinBrokerRequest{Request: clusterSSHSourceBrokerRequest{Version: 1, ID: newClusterUUID(), ExpiresAt: time.Now().Unix() + 85,
		Lease: a.Lease, Baseline: a.Baseline, Binding: a.Cohort.Targets[0].Binding, Generation: "private-generation", Ciphertext: aegisEnvelopePrefix + "private-envelope"}, Expected: public.Expected, Source: public.Source, Network: public.Network}
	if !r.valid(time.Now()) || !r.authorityMatches(a) || r.Request.valid(time.Now()) {
		t.Fatal("new request boundary changed old transport window")
	}
	b := newClusterSSHJoinBroker(nil, nil, a.Lease.ControllerHolder, sshBrokerTestSecret)
	return b, r, a, frame
}

func TestClusterSSHJoinBrokerBoundary(t *testing.T) {
	for _, mode := range []string{"complete", "replay", "wrong owner", "qualification", "expired", "future", "different claim", "source substitution", "wrong key", "response key", "tamper", "query", "media", "busy", "full cache", "private error", "invalid frame", "trailing frame", "cancel", "nil store"} {
		t.Run(mode, func(t *testing.T) {
			b, request, _, frame := sshJoinBrokerFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			var owned []byte
			if mode != "nil store" {
				b.read = func(ctx context.Context, r clusterSSHJoinBrokerRequest) ([]byte, error) {
					calls++
					if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 90*time.Second {
						t.Fatal("unbounded request")
					}
					owned = bytes.Clone(frame)
					if mode == "private error" {
						return owned, errors.New("private token diagnostic")
					}
					if mode == "invalid frame" {
						owned = []byte("private invalid frame")
					}
					if mode == "trailing frame" {
						owned = append(owned, 'x')
					}
					return owned, nil
				}
			}
			code, status, wantCalls := 200, "observed", 1
			switch mode {
			case "wrong owner":
				request.Request.Lease.ControllerHolder = "other"
				status, wantCalls = "not_owner", 0
			case "qualification":
				request.Request.Lease.OperationStep, request.Request.Lease.Step = clusterSSHQualificationStep, "qualify"
				code, wantCalls = 401, 0
			case "expired":
				request.Request.ExpiresAt = time.Now().Unix()
				code, wantCalls = 401, 0
			case "future":
				request.Request.ExpiresAt = time.Now().Unix() + 91
				code, wantCalls = 401, 0
			case "different claim":
				request.Expected.Target.Generation++
				code, wantCalls = 401, 0
			case "source substitution":
				request.Source.SourceID = newClusterUUID()
				status = "unavailable"
			case "wrong key", "response key", "tamper":
				code, wantCalls = 401, 0
			case "query", "media":
				code, wantCalls = 400, 0
			case "busy":
				b.active = 2
				status, wantCalls = "unavailable", 0
			case "full cache":
				for i := 0; i < 128; i++ {
					b.seen[newClusterUUID()] = time.Now().Unix() + 90
				}
				status, wantCalls = "unavailable", 0
			case "private error", "invalid frame", "trailing frame":
				status = "unavailable"
			case "cancel":
				cancel()
				status, wantCalls = "unavailable", 0
			case "nil store":
				status, wantCalls = "unavailable", 0
			}
			key := b.requestCipher
			if mode == "wrong key" {
				key, _ = clusterExistingIdentityScopedCipher(sshBrokerTestSecret, "request", "ssh-prejoin-custody")
			}
			if mode == "response key" {
				key = b.responseCipher
			}
			wire, err := sealClusterSSHVIP(key, clusterSSHJoinBrokerPath, request, clusterSSHSourceBrokerLimit)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "tamper" {
				wire[len(wire)-1]++
			}
			send := func() *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", clusterSSHJoinBrokerPath, bytes.NewReader(wire)).WithContext(ctx)
				r.Header.Set("Content-Type", clusterSSHSourceBrokerMedia)
				if mode == "query" {
					r.URL.RawQuery = "source=other"
				}
				if mode == "media" {
					r.Header.Set("Content-Type", "text/plain")
				}
				w := httptest.NewRecorder()
				b.handle(w, r)
				return w
			}
			w := send()
			if w.Code != code || calls != wantCalls || bytes.Contains(w.Body.Bytes(), []byte("private")) {
				t.Fatal("wrong broker boundary", w.Code, calls)
			}
			if code == 200 {
				var response clusterSSHJoinBrokerResponse
				if openClusterSSHVIP(b.responseCipher, clusterSSHJoinBrokerPath, w.Body.Bytes(), &response, clusterSSHSourceBrokerLimit) != nil || response.Status != status {
					t.Fatal("response mismatch", response.Status)
				}
				if (len(response.Frame) > 0) != (status == "observed") {
					t.Fatal("private frame on failed response")
				}
				clear(response.Frame)
			}
			if owned != nil && !bytes.Equal(owned, make([]byte, len(owned))) {
				t.Fatal("reader output not cleared")
			}
			if mode == "replay" {
				w = send()
				var response clusterSSHJoinBrokerResponse
				if openClusterSSHVIP(b.responseCipher, clusterSSHJoinBrokerPath, w.Body.Bytes(), &response, clusterSSHSourceBrokerLimit) != nil || response.Status != "unavailable" || calls != 1 {
					t.Fatal("request replayed source Job")
				}
			}
		})
	}
}

func TestClusterSSHJoinControllerRouteRegistration(t *testing.T) {
	t.Setenv("BOREALIS_OPERATOR_SECRET", sshBrokerTestSecret)
	controller := &clusterController{}
	handler := controller.healthServer().Handler
	for _, path := range []string{clusterSSHJoinBrokerPath, clusterbootstrap.K3sJoinSourcePath} {
		r := httptest.NewRequest("POST", path, strings.NewReader("invalid ciphertext"))
		r.Header.Set("Content-Type", clusterSSHSourceBrokerMedia)
		r.Header.Set(clusterbootstrap.K3sJoinSourceRequestHeader, newClusterUUID())
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if path == clusterSSHJoinBrokerPath && w.Code != 401 {
			t.Fatal("join broker route missing", w.Code)
		}
		if path == clusterbootstrap.K3sJoinSourcePath && w.Code != 404 {
			t.Fatal("private callback route missing or accepted without pending request", w.Code)
		}
		if w.Body.String() != "join source unavailable\n" && w.Body.String() != "source join delivery unavailable\n" {
			t.Fatal("registered wrong handler")
		}
	}
}

func TestClusterSSHJoinWorkerScopeAndPreparationCapability(t *testing.T) {
	for _, mode := range []string{"complete", "clock", "not owner", "locked before", "locked after", "authority drift", "source substitution", "preparation lost", "consumer failure", "ignored check failure", "unfinished check", "wrong response id", "redirect", "public peer", "cancel", "closed preparation", "snapshot owned", "duplicate source"} {
		t.Run(mode, func(t *testing.T) {
			b, request, authority, frame := sshJoinBrokerFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls, reads, consumed := 0, 0, 0
			lost := false
			var block atomic.Bool
			started, finished := make(chan struct{}), make(chan struct{})
			b.read = func(context.Context, clusterSSHJoinBrokerRequest) ([]byte, error) { return bytes.Clone(frame), nil }
			readAuthority := func(ctx context.Context) (clusterSSHPreparationAuthority, error) {
				reads++
				if block.Load() {
					close(started)
					<-ctx.Done()
					close(finished)
					return authority, ctx.Err()
				}
				if mode == "locked before" || (mode == "locked after" && calls > 0) || lost {
					return authority, errors.New("private Aegis diagnostic")
				}
				if mode == "authority drift" && calls > 0 {
					authority.PayloadSHA256 = strings.Repeat("d", 64)
				}
				if mode == "clock" {
					authority.Cohort.ObservedAt++
				}
				return authority, nil
			}
			client := &clusterSSHJoinBrokerClient{clusterSSHSourceBrokerClient{requestCipher: b.requestCipher, responseCipher: b.responseCipher,
				peers: func(context.Context) ([]string, error) {
					if mode == "public peer" {
						return []string{"http://8.8.8.8:8090"}, nil
					}
					if mode == "not owner" {
						return []string{"http://10.42.0.3:8090", "http://10.42.0.2:8090"}, nil
					}
					return []string{"http://10.42.0.2:8090"}, nil
				},
				httpClient: &http.Client{Transport: bootstrapRoundTripper(func(r *http.Request) (*http.Response, error) {
					calls++
					w := httptest.NewRecorder()
					if mode == "redirect" {
						w.Header().Set("Location", "http://forbidden.example")
						w.WriteHeader(302)
						return w.Result(), nil
					}
					if mode == "not owner" && calls == 1 {
						follower := newClusterSSHJoinBroker(nil, nil, "other", sshBrokerTestSecret)
						follower.handle(w, r)
						return w.Result(), nil
					}
					if mode == "cancel" {
						cancel()
					}
					if mode == "wrong response id" {
						wire, _ := sealClusterSSHVIP(b.responseCipher, clusterSSHJoinBrokerPath, clusterSSHJoinBrokerResponse{1, newClusterUUID(), "observed", frame}, clusterSSHSourceBrokerLimit)
						w.Header().Set("Content-Type", clusterSSHSourceBrokerMedia)
						w.Write(wire)
						return w.Result(), nil
					}
					b.handle(w, r)
					return w.Result(), nil
				})}}}
			preparation := func(context.Context) error {
				if mode == "preparation lost" && calls > 0 {
					return errors.New("lost")
				}
				return nil
			}
			networks := []clusterbootstrap.SourceNetwork{request.Network}
			if mode == "duplicate source" {
				networks = append(networks, request.Network)
			}
			join := newClusterSSHPreparationJoinRead(ctx, client, request.Expected, networks, request.Request.Lease, request.Request.Baseline, request.Request.sealed(), readAuthority, preparation)
			if mode == "snapshot owned" {
				networks[0].NodeUID = newClusterUUID()
			}
			if mode == "closed preparation" {
				cancel()
			}
			if mode == "source substitution" {
				request.Source.SourceID = newClusterUUID()
			}
			var retained *clusterbootstrap.K3sJoinMaterial
			var retainedCheck func(context.Context) error
			err := join(context.Background(), request.Source, func(ctx context.Context, m *clusterbootstrap.K3sJoinMaterial, check func(context.Context) error) error {
				consumed++
				retained, retainedCheck = m, check
				var token bytes.Buffer
				if m.WriteToken(ctx, request.Expected, request.Source, check, &token) != nil || !strings.HasSuffix(token.String(), "private fixture token") {
					t.Fatal("guarded material missing")
				}
				if mode == "consumer failure" {
					return errors.New("consumer failure")
				}
				if mode == "ignored check failure" {
					lost = true
					_ = check(ctx)
				}
				if mode == "unfinished check" {
					block.Store(true)
					go func() { _ = check(context.Background()) }()
					<-started
				}
				return nil
			})
			success := mode == "complete" || mode == "clock" || mode == "not owner" || mode == "snapshot owned"
			if (err == nil) != success {
				t.Fatal("wrong scoped result", err, calls, reads, consumed)
			}
			if mode == "unfinished check" {
				select {
				case <-finished:
				default:
					t.Fatal("unfinished check not cancelled/joined")
				}
			}
			if retained != nil {
				before := reads
				if retainedCheck(context.Background()) == nil || reads != before {
					t.Fatal("closed check performed IO")
				}
				if retained.WriteToken(context.Background(), request.Expected, request.Source, func(context.Context) error { return nil }, io.Discard) == nil {
					t.Fatal("material escaped scope")
				}
			}
			if (mode == "public peer" || mode == "closed preparation" || mode == "locked before" || mode == "duplicate source" || mode == "source substitution") && calls != 0 {
				t.Fatal("invalid request reached controller")
			}
		})
	}
}
