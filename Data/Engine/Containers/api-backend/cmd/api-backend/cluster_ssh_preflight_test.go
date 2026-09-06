package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"borealis/api-backend/internal/clusterremote"
	"golang.org/x/crypto/ssh"
)

type clusterSSHTestInspector struct {
	key   clusterremote.HostKey
	calls int
	err   error
}

func (remote *clusterSSHTestInspector) ProbeHostKey(ctx context.Context, target clusterremote.Target) (clusterremote.HostKey, error) {
	remote.calls++
	return remote.key, remote.err
}

func (remote *clusterSSHTestInspector) Inspect(ctx context.Context, target clusterremote.Target, key clusterremote.HostKey, credential *clusterremote.Credential) (clusterremote.HostFacts, error) {
	remote.calls++
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 30*time.Second {
		panic("inspection requires a bounded context")
	}
	return clusterremote.HostFacts{Kernel: "Linux", Architecture: "x86_64", UID: 1000, Hostname: "joining-engine"}, remote.err
}

func clusterSSHTestService(t *testing.T) (*clusterSSHPreflight, *clusterSSHTestInspector, string, map[string]any) {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	remote := &clusterSSHTestInspector{key: clusterremote.HostKey{Algorithm: key.Type(), Fingerprint: ssh.FingerprintSHA256(key), PublicKey: key.Marshal()}}
	auth, token := clusterTestAuth(t, &clusterTestStore{profile: operatorProfile{Role: "Admin"}})
	service := &clusterSSHPreflight{auth: auth, remote: remote, slots: make(chan struct{}, 1)}
	body := map[string]any{"address": "192.168.3.251", "port": 22, "host_key_algorithm": key.Type(), "host_key_fingerprint": ssh.FingerprintSHA256(key), "host_key_base64": base64.StdEncoding.EncodeToString(key.Marshal()), "host_key_approved": true, "auth_method": "password", "username": "nicole", "password": "  punctuation<&>$ preserved  "}
	return service, remote, token, body
}

func clusterSSHTestCall(t *testing.T, service *clusterSSHPreflight, token string, inspect bool, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := clusterTestRequest(t, http.MethodPost, "/api/server/cluster/onboarding/inspect", string(raw), token)
	recorder := httptest.NewRecorder()
	service.handler(inspect).ServeHTTP(recorder, request)
	return recorder
}

func TestClusterSSHPreflightRequiresAdminBeforeReadingOrDialing(t *testing.T) {
	for _, role := range []string{"", "User"} {
		t.Run(role, func(t *testing.T) {
			service, remote, token, body := clusterSSHTestService(t)
			if role == "" {
				token = "invalid"
			} else {
				service.auth, token = clusterTestAuth(t, &clusterTestStore{profile: operatorProfile{Role: role}})
			}
			for _, inspect := range []bool{false, true} {
				response := clusterSSHTestCall(t, service, token, inspect, body)
				if response.Code != 401 && response.Code != 403 {
					t.Fatalf("unexpected status %d", response.Code)
				}
			}
			if remote.calls != 0 {
				t.Fatal("unauthorized network work")
			}
		})
	}
}

func TestClusterSSHPreflightDiscoveryContainsNoCredentials(t *testing.T) {
	service, remote, token, body := clusterSSHTestService(t)
	response := clusterSSHTestCall(t, service, token, false, map[string]any{"address": body["address"], "port": body["port"]})
	if response.Code != 200 || remote.calls != 1 || !strings.Contains(response.Body.String(), remote.key.Fingerprint) {
		t.Fatalf("discovery failed: %d", response.Code)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("discovery cacheable")
	}
	response = clusterSSHTestCall(t, service, token, false, body)
	if response.Code != 400 || remote.calls != 1 {
		t.Fatal("discovery accepted credential fields")
	}
}

func TestClusterSSHPreflightValidatesBeforeNetwork(t *testing.T) {
	tests := map[string]func(map[string]any){
		"public address":    func(b map[string]any) { b["address"] = "8.8.8.8" },
		"loopback":          func(b map[string]any) { b["address"] = "127.0.0.1" },
		"hostname":          func(b map[string]any) { b["address"] = "engine.example" },
		"port text":         func(b map[string]any) { b["port"] = "22" },
		"fraction port":     func(b map[string]any) { b["port"] = 22.5 },
		"port bound":        func(b map[string]any) { b["port"] = 65536 },
		"approval absent":   func(b map[string]any) { delete(b, "host_key_approved") },
		"approval false":    func(b map[string]any) { b["host_key_approved"] = false },
		"approval string":   func(b map[string]any) { b["host_key_approved"] = "true" },
		"wrong fingerprint": func(b map[string]any) { b["host_key_fingerprint"] = "SHA256:wrong" },
		"wrong algorithm":   func(b map[string]any) { b["host_key_algorithm"] = "ssh-rsa" },
		"bad public key":    func(b map[string]any) { b["host_key_base64"] = "invalid" },
		"empty username":    func(b map[string]any) { b["username"] = "" },
		"shell username":    func(b map[string]any) { b["username"] = "root; id" },
		"empty password":    func(b map[string]any) { b["password"] = "" },
		"null password":     func(b map[string]any) { b["password"] = nil },
		"NUL password":      func(b map[string]any) { b["password"] = "x\x00y" },
		"long password":     func(b map[string]any) { b["password"] = strings.Repeat("x", 4097) },
		"mixed auth":        func(b map[string]any) { b["private_key"] = "private" },
		"unknown method":    func(b map[string]any) { b["auth_method"] = "agent" },
		"bad key": func(b map[string]any) {
			delete(b, "password")
			b["auth_method"] = "private_key"
			b["private_key"] = "invalid"
		},
		"unknown field": func(b map[string]any) { b["command"] = "id" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			service, remote, token, body := clusterSSHTestService(t)
			mutate(body)
			response := clusterSSHTestCall(t, service, token, true, body)
			if response.Code != 400 || remote.calls != 0 {
				t.Fatalf("status %d, calls %d", response.Code, remote.calls)
			}
			if strings.Contains(response.Body.String(), "punctuation") {
				t.Fatal("response exposed password")
			}
		})
	}
}

func TestClusterSSHPreflightRejectsAmbiguousJSONAndQuery(t *testing.T) {
	for _, raw := range []string{
		`{"address":"192.168.3.251","port":22,"port":23}`,
		`{"Address":"192.168.3.251","port":22}`,
		`{"address":"192.168.3.251","port":22} {}`,
		`null`, `[]`, `{`, strings.Repeat(" ", clusterSSHBodyMaxBytes+1),
	} {
		service, remote, token, _ := clusterSSHTestService(t)
		request := clusterTestRequest(t, "POST", "/api/server/cluster/onboarding/host-key", raw, token)
		response := httptest.NewRecorder()
		service.handler(false).ServeHTTP(response, request)
		if response.Code != 400 || remote.calls != 0 {
			t.Fatal("ambiguous JSON accepted")
		}
	}
	for _, variation := range []string{"query", "content-type"} {
		service, remote, token, _ := clusterSSHTestService(t)
		request := clusterTestRequest(t, "POST", "/api/server/cluster/onboarding/host-key", `{"address":"192.168.3.251","port":22}`, token)
		if variation == "query" {
			request.URL.RawQuery = "password=forbidden"
		} else {
			request.Header.Set("Content-Type", "text/plain")
		}
		response := httptest.NewRecorder()
		service.handler(false).ServeHTTP(response, request)
		if response.Code != 400 || remote.calls != 0 {
			t.Fatal("invalid request accepted")
		}
	}
}

func TestClusterSSHPreflightInspectionAndSafeFailures(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{nil, 200, "joining-engine"},
		{clusterremote.ErrHostKeyChanged, 409, "ssh_host_key_changed"},
		{clusterremote.ErrCancelled, 504, "ssh_preflight_timeout"},
		{errors.New("remote banner includes private diagnostic"), 502, "ssh_preflight_failed"},
	} {
		service, remote, token, body := clusterSSHTestService(t)
		remote.err = tc.err
		response := clusterSSHTestCall(t, service, token, true, body)
		if response.Code != tc.status || !strings.Contains(response.Body.String(), tc.code) || remote.calls != 1 {
			t.Fatalf("status %d", response.Code)
		}
		for _, secret := range []string{"punctuation", "private diagnostic"} {
			if strings.Contains(response.Body.String(), secret) {
				t.Fatal("response exposed secret")
			}
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("inspection cacheable")
		}
	}
}

func TestClusterSSHPreflightSaturatedWorkerDoesNotDial(t *testing.T) {
	service, remote, token, body := clusterSSHTestService(t)
	service.slots <- struct{}{}
	response := clusterSSHTestCall(t, service, token, true, body)
	if response.Code != 503 || remote.calls != 0 {
		t.Fatal("saturated worker dialed")
	}
}

func TestClusterSSHPreflightRoutesRegistered(t *testing.T) {
	service, _, _, _ := clusterSSHTestService(t)
	mux := http.NewServeMux()
	registerServerClusterRoutes(mux, service.auth)
	for _, path := range []string{"host-key", "inspect"} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest("POST", "/api/server/cluster/onboarding/"+path, nil))
		if response.Code != 401 {
			t.Fatalf("route %s: %d", path, response.Code)
		}
	}
}
