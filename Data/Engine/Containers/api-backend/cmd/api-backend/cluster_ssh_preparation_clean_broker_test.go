package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const sshPreparationCleanBrokerTestSecret = "synthetic-clean-broker-secret-32-bytes"

func sshPreparationCleanBrokerSnapshot(t *testing.T) clusterSSHPreparationApprovalSnapshot {
	t.Helper()
	snapshot, _ := sshPreparationCleanTargetsFixture(t, false)
	plan, err := clusterSSHPreparationPlanDigest(snapshot.work.Authorization)
	if err != nil {
		t.Fatal(err)
	}
	source := snapshot.work.Proof.Source
	body := marshalClusterJSON(map[string]any{"request_id": newClusterUUID(), "plan_sha256": plan, "confirmation": "PREPARE ENGINE NODES", "source_id": source.Members[0].NodeID})
	intent, err := newClusterSSHPreparationConfirmation([]byte(body), "admin", time.Now().Unix(), snapshot.work.Authorization, source)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.work.Payload = marshalClusterJSON(map[string]any{"ssh_preparation_request": intent})
	return snapshot
}

func sshPreparationCleanBrokerTestClient(t *testing.T, transport http.RoundTripper) *clusterSSHPreparationCleanBrokerClient {
	t.Helper()
	t.Setenv("BOREALIS_OPERATOR_SECRET", sshPreparationCleanBrokerTestSecret)
	c, err := newClusterSSHPreparationCleanBrokerClientFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	c.endpoint = func(context.Context) (string, error) { return "http://127.0.0.1:1", nil }
	if transport != nil {
		c.httpClient.Transport = transport
	}
	return c
}

func TestClusterSSHPreparationCleanBrokerRequestBoundary(t *testing.T) {
	snapshot := sshPreparationCleanBrokerSnapshot(t)
	plan, proposal, err := clusterSSHPreparationCleanBrokerBinding(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"unauthenticated", "wrong key", "wrong path AAD", "extra field", "duplicate", "alias", "expired", "future expiry", "wrong version", "old v1 request", "zero operation", "zero request", "attempt", "holder control", "holder oversize", "zero plan", "filesystem relative", "filesystem duplicate", "filesystem unsorted", "query", "content encoding", "oversize", "authenticated unavailable"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("BOREALIS_OPERATOR_SECRET", sshPreparationCleanBrokerTestSecret)
			mux := http.NewServeMux()
			registerClusterSSHOnboardingRoutes(mux, nil)
			broker := newClusterSSHPreparationCleanBroker(nil, nil, sshPreparationCleanBrokerTestSecret)
			request := clusterSSHPreparationCleanBrokerRequest{Version: clusterSSHPreparationCleanBrokerVersion, ID: newClusterUUID(), ExpiresAt: time.Now().Unix() + 25,
				OperationID: snapshot.work.Authorization.OperationID, Attempt: snapshot.work.Authorization.Attempt,
				ControllerHolder: snapshot.work.Authorization.ControllerHolder, PlanSHA256: plan, ProposalSHA256: proposal}
			switch mode {
			case "expired":
				request.ExpiresAt = time.Now().Unix()
			case "future expiry":
				request.ExpiresAt += 60
			case "wrong version":
				request.Version++
			case "old v1 request":
				request.Version = 1
			case "zero operation":
				request.OperationID = "00000000-0000-0000-0000-000000000000"
			case "zero request":
				request.ID = "00000000-0000-0000-0000-000000000000"
			case "attempt":
				request.Attempt = 0
			case "holder control":
				request.ControllerHolder = "holder\n"
			case "holder oversize":
				request.ControllerHolder = strings.Repeat("h", 1025)
			case "zero plan":
				request.PlanSHA256 = strings.Repeat("0", 64)
			case "filesystem relative":
				request.FilesystemPaths = []string{"relative/path"}
			case "filesystem duplicate":
				request.FilesystemPaths = []string{"/opt/Borealis", "/opt/Borealis"}
			case "filesystem unsorted":
				request.FilesystemPaths = []string{"/var/lib/longhorn", "/opt/Borealis"}
			}
			raw, _ := json.Marshal(request)
			switch mode {
			case "extra field":
				raw = append(raw[:len(raw)-1], []byte(`,"host":"192.168.1.1"}`)...)
			case "duplicate":
				raw = append(raw[:len(raw)-1], []byte(`,"version":2}`)...)
			case "alias":
				raw = bytes.Replace(raw, []byte(`"version"`), []byte(`"Version"`), 1)
			}
			cipher := broker.requestCipher
			if mode == "wrong key" {
				cipher, _ = clusterExistingIdentityScopedCipher(strings.Repeat("x", 32), "request", clusterSSHPreparationCleanBrokerScope)
			}
			path := clusterSSHPreparationCleanBrokerPath
			if mode == "wrong path AAD" {
				path = clusterSSHSourceBrokerPath
			}
			wire := cipher.Seal(nil, nil, raw, []byte(path))
			if mode == "unauthenticated" {
				wire = raw
			}
			if mode == "oversize" {
				wire = bytes.Repeat([]byte("x"), clusterSSHPreparationCleanBrokerLimit+1)
			}
			url := clusterSSHPreparationCleanBrokerPath
			if mode == "query" {
				url += "?"
			}
			r := httptest.NewRequest(http.MethodPost, url, bytes.NewReader(wire))
			r.Header.Set("Content-Type", clusterSSHSourceBrokerMedia)
			if mode == "content encoding" {
				r.Header.Set("Content-Encoding", "gzip")
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			want := http.StatusUnauthorized
			if mode == "query" || mode == "content encoding" {
				want = http.StatusBadRequest
			}
			if mode == "authenticated unavailable" {
				want = http.StatusServiceUnavailable
			}
			if w.Code != want {
				t.Fatalf("status %d, want %d", w.Code, want)
			}
			if strings.Contains(w.Body.String(), plan) || strings.Contains(w.Body.String(), request.ControllerHolder) {
				t.Fatal("private input leaked")
			}
		})
	}
}

type sshPreparationCleanBrokerBody struct {
	io.Reader
	close func() error
}

func (b *sshPreparationCleanBrokerBody) Close() error { return b.close() }

func TestClusterSSHPreparationCleanBrokerClientBinding(t *testing.T) {
	for _, mode := range []string{"success", "wrong plan", "missing intent", "wrong request", "wrong ID", "wrong digest", "wrong version", "old v1 response", "redirect", "encoding", "close error", "changed repeat", "cancel during close", "request expiry during close", "child expiry during close"} {
		t.Run(mode, func(t *testing.T) {
			snapshot := sshPreparationCleanBrokerSnapshot(t)
			plan, _, _ := clusterSSHPreparationCleanBrokerBinding(snapshot)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "request expiry during close" {
				var stop context.CancelFunc
				ctx, stop = context.WithDeadline(ctx, time.Unix(time.Now().Unix()+2, 0))
				defer stop()
			}
			var calls, closes, checks atomic.Int64
			closed, release := make(chan struct{}, 1), make(chan struct{})
			var c *clusterSSHPreparationCleanBrokerClient
			transport := bootstrapRoundTripper(func(r *http.Request) (*http.Response, error) {
				calls.Add(1)
				wire, err := io.ReadAll(r.Body)
				if err != nil {
					return nil, err
				}
				var request clusterSSHPreparationCleanBrokerRequest
				if openClusterSSHVIP(c.requestCipher, clusterSSHPreparationCleanBrokerPath, wire, &request, clusterSSHPreparationCleanBrokerLimit) != nil {
					return nil, errClusterConflict
				}
				if r.GetBody != nil || !request.valid(time.Now()) || r.URL.Path != clusterSSHPreparationCleanBrokerPath {
					return nil, errClusterConflict
				}
				digest, _ := clusterSSHPreparationDigest(request)
				value := clusterSSHPreparationCleanBrokerResponse{Version: clusterSSHPreparationCleanBrokerVersion, ID: request.ID, RequestSHA256: digest}
				value.Targets = sshPreparationTargetCPUFixture(snapshot)
				value.Digest, _ = clusterSSHPreparationTargetCPUDigest(snapshot, value.Targets)
				switch mode {
				case "wrong request":
					value.RequestSHA256 = strings.Repeat("f", 64)
				case "wrong ID":
					value.ID = newClusterUUID()
				case "wrong digest":
					value.Digest = strings.Repeat("0", 64)
				case "wrong version":
					value.Version++
				case "old v1 response":
					value.Version = 1
				case "changed repeat":
					if calls.Load() > 1 {
						value.Digest = strings.Repeat("b", 64)
					}
				}
				raw, err := sealClusterSSHVIP(c.responseCipher, clusterSSHPreparationCleanBrokerPath, value, clusterSSHPreparationCleanBrokerLimit)
				if err != nil {
					return nil, err
				}
				body := &sshPreparationCleanBrokerBody{Reader: bytes.NewReader(raw), close: func() error {
					closes.Add(1)
					if strings.Contains(mode, "during close") {
						closed <- struct{}{}
						if mode == "cancel during close" {
							<-release
						} else {
							<-r.Context().Done()
						}
					}
					if mode == "close error" {
						return errors.New("synthetic close failure")
					}
					return nil
				}}
				response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {clusterSSHSourceBrokerMedia}}, Body: body, Request: r}
				if mode == "redirect" {
					response.StatusCode = http.StatusTemporaryRedirect
					response.Header.Set("Location", "http://127.0.0.1:2/escaped")
				}
				if mode == "encoding" {
					response.Header.Set("Content-Encoding", "gzip")
				}
				return response, nil
			})
			c = sshPreparationCleanBrokerTestClient(t, transport)
			if mode == "child expiry during close" {
				c.httpClient.Timeout = 25 * time.Millisecond
			}
			check := func(ctx context.Context) error { checks.Add(1); return ctx.Err() }
			if mode == "missing intent" {
				snapshot.work.Payload = `{}`
			}
			read := c.preparationRead(snapshot, check)
			if mode == "wrong plan" {
				plan = strings.Repeat("e", 64)
			}
			type result struct {
				value string
				err   error
			}
			done := make(chan result, 1)
			go func() { value, err := read(ctx, plan); done <- result{value, err} }()
			if mode == "cancel during close" {
				select {
				case <-closed:
				case <-time.After(5 * time.Second):
					t.Fatal("body Close not reached")
				}
				cancel()
				select {
				case <-done:
					t.Fatal("returned before joined Close")
				case <-time.After(20 * time.Millisecond):
				}
				if checks.Load() != 1 {
					t.Fatal("SQL recheck preceded joined Close")
				}
				close(release)
			}
			var got result
			select {
			case got = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("reader did not finish")
			}
			positive := mode == "success" || mode == "changed repeat"
			if (got.err == nil) != positive || got.err != nil && got.value != "" {
				t.Fatal("unexpected read outcome", got.err)
			}
			before := calls.Load()
			again, err := read(context.Background(), plan)
			if mode == "success" {
				if err != nil || again != got.value || calls.Load() != before+1 {
					t.Fatal("no fresh repeated request", err)
				}
			} else if err == nil || again != "" || mode != "changed repeat" && calls.Load() != before {
				t.Fatal("failed reader revived", err)
			}
			if calls.Load() != closes.Load() {
				t.Fatal("unclosed response")
			}
		})
	}
}
