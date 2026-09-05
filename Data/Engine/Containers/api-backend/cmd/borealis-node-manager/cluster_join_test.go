package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const joinTestID = "11111111-1111-4111-8111-111111111111"

func TestClusterJoinRejectsPlaintextAndRedirectDisclosure(t *testing.T) {
	var disclosed atomic.Int32
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { disclosed.Add(1) }))
	defer receiver.Close()
	for _, method := range []string{http.MethodPost, http.MethodGet} {
		t.Run(method, func(t *testing.T) {
			source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, receiver.URL, http.StatusTemporaryRedirect)
			}))
			defer source.Close()
			_, status, err := clusterJoinRequest(context.Background(), source.Client(), method, source.URL, "/api/bootstrap/cluster/join", "sensitive-bundle", []byte(`{"invite_bundle":"sensitive-bundle"}`))
			if err == nil || status != http.StatusTemporaryRedirect || disclosed.Load() != 0 {
				t.Fatalf("redirect disclosed invitation: status=%d err=%v calls=%d", status, err, disclosed.Load())
			}
			if strings.Contains(err.Error(), "sensitive-bundle") {
				t.Fatal("error exposed bundle")
			}
		})
	}
	for _, endpoint := range []string{"http://127.0.0.1", "https://user:password@localhost", "https://localhost/path", "https://localhost?token=secret", "https://localhost/#fragment"} {
		if _, err := submitClusterAdmission(context.Background(), receiver.Client(), endpoint, map[string]any{"invite_bundle": "sensitive-bundle"}); err == nil {
			t.Fatalf("unsafe endpoint accepted: %s", endpoint)
		}
	}
	if disclosed.Load() != 0 {
		t.Fatal("invalid endpoint sent invitation")
	}
}

func TestClusterJoinRetriesLostResponseAndPollsCurrentState(t *testing.T) {
	var posts, polls atomic.Int32
	var firstBody string
	var bodyMu sync.Mutex
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			bodyMu.Lock()
			defer bodyMu.Unlock()
			raw, _ := io.ReadAll(r.Body)
			if posts.Add(1) == 1 {
				firstBody = string(raw)
				// Acceptance committed; response connection disappears before client sees ID.
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				conn.Close()
				return
			}
			if string(raw) != firstBody {
				t.Error("retry changed original join identity")
			}
		} else {
			if r.Header.Get("X-Borealis-Cluster-Invite") != "retained-bundle" {
				t.Error("poll lost invitation binding")
			}
			if polls.Add(1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
		}
		_ = json.NewEncoder(w).Encode(clusterJoinAdmission{ID: joinTestID, State: "Approved", Config: clusterJoinConfig{Server: "https://192.168.90.248:6443", Version: "v1.36.3+k3s1", PeerCIDRs: "192.168.90.10/32,192.168.90.11/32,192.168.90.12/32"}})
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	admission, err := submitClusterAdmission(ctx, server.Client(), server.URL, map[string]any{"invite_bundle": "retained-bundle", "node_name": "engine-02"})
	if err != nil || admission.ID != joinTestID || posts.Load() != 2 {
		t.Fatalf("lost response retry: admission=%+v posts=%d err=%v", admission, posts.Load(), err)
	}
	current, err := waitForClusterAdmission(ctx, server.Client(), server.URL, "retained-bundle", admission.ID)
	if err != nil || current.State != "Approved" || polls.Load() != 2 {
		t.Fatalf("current state without event history: %+v polls=%d err=%v", current, polls.Load(), err)
	}
}

func TestClusterJoinPollRejectsChangedIdentityAndTerminalState(t *testing.T) {
	for _, response := range []clusterJoinAdmission{
		{ID: "22222222-2222-4222-8222-222222222222", State: "Approved"},
		{ID: joinTestID, State: "Cancelled"}, {ID: joinTestID, State: "Expired"}, {ID: joinTestID, State: "Recovery Required"},
	} {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(response) }))
		_, err := waitForClusterAdmission(context.Background(), server.Client(), server.URL, "bundle", joinTestID)
		server.Close()
		if err == nil {
			t.Fatalf("invalid authorization accepted: %+v", response)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := submitClusterAdmission(ctx, http.DefaultClient, "https://127.0.0.1:1", map[string]any{}); err != context.Canceled {
		t.Fatalf("cancel not bounded: %v", err)
	}
}

func TestClusterJoinUsesAuthoritativeConfig(t *testing.T) {
	valid := clusterJoinConfig{Server: "https://192.168.90.248:6443", Version: "v1.36.3+k3s1", PeerCIDRs: "192.168.90.10/32,192.168.90.11/32,192.168.90.12/32"}
	if err := validateClusterJoinConfig(valid, "192.168.90.11", "", "", ""); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"server", "version", "peers", "target", "assert-server", "assert-version", "assert-peers"} {
		config := valid
		target := "192.168.90.11"
		server, version, peers := "", "", ""
		switch field {
		case "server":
			config.Server = "http://192.168.90.248:6443"
		case "version":
			config.Version = "latest"
		case "peers":
			config.PeerCIDRs = "0.0.0.0/0"
		case "target":
			target = "192.168.90.99"
		case "assert-server":
			server = "https://192.168.90.249:6443"
		case "assert-version":
			version = "v1.35.1+k3s1"
		case "assert-peers":
			peers = "192.168.90.11/32"
		}
		if err := validateClusterJoinConfig(config, target, server, version, peers); err == nil {
			t.Errorf("invalid %s accepted", field)
		}
	}
}
