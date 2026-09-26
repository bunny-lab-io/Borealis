package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

type sshOnboardingTestBackend struct {
	source      clusterSSHQueueSource
	calls       []string
	sealed      []clusterSSHCredentialEnvelope
	queued      []clusterSSHPlannedTarget
	err         error
	failAt      string
	operationID string
}

func (b *sshOnboardingTestBackend) check(ctx context.Context, name string) error {
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 20*time.Second {
		panic("onboarding request must be bounded")
	}
	b.calls = append(b.calls, name)
	if b.failAt == name {
		return b.err
	}
	return nil
}
func (b *sshOnboardingTestBackend) sshInspectionSource(ctx context.Context) (clusterSSHQueueSource, error) {
	return b.source, b.check(ctx, "source")
}
func (b *sshOnboardingTestBackend) sealClusterSSHCredentials(ctx context.Context, e clusterSSHCredentialEnvelope) (sealedClusterSSHCredentials, error) {
	if err := b.check(ctx, "seal"); err != nil {
		return sealedClusterSSHCredentials{}, err
	}
	if err := e.validate(); err != nil {
		return sealedClusterSSHCredentials{}, err
	}
	b.sealed = append(b.sealed, e)
	return sealedClusterSSHCredentials{binding: e.Binding, generation: "fixture-generation", ciphertext: "aegis:v1:fixture-ciphertext"}, nil
}
func (b *sshOnboardingTestBackend) queueClusterSSHInspections(ctx context.Context, actor string, source clusterSSHQueueSource, targets []clusterSSHPlannedTarget) (map[string]any, error) {
	if err := b.check(ctx, "queue"); err != nil {
		return nil, err
	}
	if b.operationID != "" {
		return nil, errClusterConflict
	}
	if err := validateClusterSSHPlannedTargets(targets, 2); err != nil {
		return nil, err
	}
	b.queued = targets
	b.operationID = targets[0].Binding.OperationID
	return map[string]any{"operation_id": b.operationID, "state": "queued"}, nil
}
func (b *sshOnboardingTestBackend) clusterSSHInspectionProgress(ctx context.Context, id string) (clusterSSHInspectionProgress, error) {
	if err := b.check(ctx, "progress"); err != nil {
		return clusterSSHInspectionProgress{}, err
	}
	if id != b.operationID {
		return clusterSSHInspectionProgress{}, errClusterNotFound
	}
	return clusterSSHInspectionProgress{OperationID: id, State: "queued", Step: "preflight", Attempt: 1}, nil
}

func sshOnboardingTestService(t *testing.T) (*clusterSSHOnboarding, *sshOnboardingTestBackend, string, map[string]any) {
	t.Helper()
	preflight, _, token, one := clusterSSHTestService(t)
	_, _, _, two := clusterSSHTestService(t)
	two["address"] = "192.168.3.250"
	one["sudo_password"] = "  sudo<&>$ preserved  "
	backend := &sshOnboardingTestBackend{source: clusterSSHQueueSource{ClusterID: newClusterUUID(), Enabled: 1, Status: "Healthy", ActiveSize: 1, DesiredSize: 1, Members: 1, HMRState: "inactive", Release: "2026.09.1.2", SHA: strings.Repeat("a", 40), ConfigJSON: `{"k3s_version":"v1.36.3+k3s1"}`}}
	service := &clusterSSHOnboarding{auth: preflight.auth, store: backend, sealer: backend, slots: make(chan struct{}, 1)}
	return service, backend, token, map[string]any{"request_id": newClusterUUID(), "confirmation": "INSPECT ENGINE NODES", "targets": []any{one, two}}
}

func sshOnboardingTestRequest(t *testing.T, service *clusterSSHOnboarding, token, method, path, raw string) *httptest.ResponseRecorder {
	t.Helper()
	request := clusterTestRequest(t, method, path, raw, token)
	response := httptest.NewRecorder()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/server/cluster/onboarding/operations", service.start)
	mux.HandleFunc("GET /api/server/cluster/onboarding/operations/{id}", service.progress)
	mux.ServeHTTP(response, request)
	return response
}

func TestClusterSSHOnboardingRequiresAdminAndRejectsInvalidCohort(t *testing.T) {
	for _, mode := range []string{"unauthenticated", "non Admin", "unapproved second pin", "duplicate address", "duplicate key", "invalid second port", "invalid sudo", "oversize sudo", "invalid username", "mixed credentials", "changed fingerprint", "invalid request ID", "missing confirmation", "too many targets", "null targets", "unknown target field", "unknown field"} {
		t.Run(mode, func(t *testing.T) {
			service, backend, token, body := sshOnboardingTestService(t)
			targets := body["targets"].([]any)
			one, two := targets[0].(map[string]any), targets[1].(map[string]any)
			switch mode {
			case "unauthenticated":
				token = "invalid"
			case "non Admin":
				service.auth, token = clusterTestAuth(t, &clusterTestStore{profile: operatorProfile{Role: "User"}})
			case "unapproved second pin":
				two["host_key_approved"] = false
			case "duplicate address":
				two["address"] = one["address"]
			case "duplicate key":
				for _, key := range []string{"host_key_algorithm", "host_key_fingerprint", "host_key_base64"} {
					two[key] = one[key]
				}
			case "invalid second port":
				two["port"] = 1.5
			case "invalid sudo":
				two["sudo_password"] = "line1\nline2"
			case "oversize sudo":
				two["sudo_password"] = strings.Repeat("s", 4097)
			case "invalid username":
				two["username"] = "user; command"
			case "mixed credentials":
				two["private_key"] = "key"
			case "changed fingerprint":
				two["host_key_fingerprint"] = one["host_key_fingerprint"]
			case "invalid request ID":
				body["request_id"] = "../operations"
			case "missing confirmation":
				delete(body, "confirmation")
			case "too many targets":
				body["targets"] = append(targets, two)
			case "null targets":
				body["targets"] = nil
			case "unknown target field":
				two["command"] = "anything"
			case "unknown field":
				body["run_command"] = "anything"
			}
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "unauthenticated" || mode == "non Admin" {
				raw = []byte("invalid JSON must not be parsed before authorization")
			}
			response := sshOnboardingTestRequest(t, service, token, http.MethodPost, "/api/server/cluster/onboarding/operations", string(raw))
			want := 400
			if mode == "unauthenticated" {
				want = 401
			}
			if mode == "non Admin" {
				want = 403
			}
			if response.Code != want || len(backend.calls) != 0 {
				t.Fatalf("invalid cohort reached source/encryption/queue: status=%d calls=%v", response.Code, backend.calls)
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("credential request became cacheable")
			}
			if strings.Contains(response.Body.String(), one["password"].(string)) {
				t.Fatal("error disclosed submitted secret")
			}
		})
	}
}

func TestClusterSSHOnboardingExactJSONBoundary(t *testing.T) {
	service, backend, token, body := sshOnboardingTestService(t)
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		strings.Replace(string(raw), `"request_id":`, `"request_id":"`+newClusterUUID()+`","request_id":`, 1),
		strings.Replace(string(raw), `"password":`, `"password":"duplicate","password":`, 1),
		strings.Replace(string(raw), `"password":`, `"Password":`, 1),
		string(raw) + ` {}`, string(raw) + string([]byte{0xff}), strings.Repeat(" ", clusterSSHQueueBodyMaxBytes) + string(raw),
	} {
		response := sshOnboardingTestRequest(t, service, token, http.MethodPost, "/api/server/cluster/onboarding/operations", bad)
		if response.Code != 400 || len(backend.calls) != 0 {
			t.Fatal("ambiguous or oversized JSON reached queue work")
		}
	}
	for _, mode := range []string{"query", "content type"} {
		request := clusterTestRequest(t, http.MethodPost, "/api/server/cluster/onboarding/operations", string(raw), token)
		if mode == "query" {
			request.URL.RawQuery = "extra=1"
		} else {
			request.Header.Set("Content-Type", "text/plain")
		}
		response := httptest.NewRecorder()
		service.start(response, request)
		if response.Code != 400 || len(backend.calls) != 0 {
			t.Fatal("invalid request metadata reached queue work")
		}
	}
}

func TestClusterSSHOnboardingSubmissionAndReceiptRecovery(t *testing.T) {
	service, backend, token, body := sshOnboardingTestService(t)
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	response := sshOnboardingTestRequest(t, service, token, http.MethodPost, "/api/server/cluster/onboarding/operations", string(raw))
	if response.Code != 202 || strings.Join(backend.calls, ",") != "source,seal,seal,queue" || len(backend.queued) != 2 {
		t.Fatalf("submission failed: status=%d calls=%v", response.Code, backend.calls)
	}
	id := body["request_id"].(string)
	for i, encrypted := range backend.sealed {
		input := body["targets"].([]any)[i].(map[string]any)
		if encrypted.Binding.ClusterID != backend.source.ClusterID || encrypted.Binding.OperationID != id || !clusterUUIDRE.MatchString(encrypted.Binding.TargetID) || encrypted.Password != input["password"] || encrypted.Binding.Fingerprint != input["host_key_fingerprint"] {
			t.Fatal("submission lost exact secret syntax or current source/target binding")
		}
		if strings.Contains(response.Body.String(), encrypted.Password) {
			t.Fatal("queue receipt disclosed a credential")
		}
	}
	if backend.sealed[0].SudoPassword != body["targets"].([]any)[0].(map[string]any)["sudo_password"] || backend.sealed[0].Binding.TargetID == backend.sealed[1].Binding.TargetID {
		t.Fatal("submission lost sudo syntax or distinct target identities")
	}
	// Caller knows request UUID before submission. A lost HTTP response recovers
	// by GET; repeated POST conflicts and cannot replace stored credentials/work.
	before := len(backend.queued)
	response = sshOnboardingTestRequest(t, service, token, http.MethodGet, "/api/server/cluster/onboarding/operations/"+id, "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), id) || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("lost receipt could not recover operation progress")
	}
	response = sshOnboardingTestRequest(t, service, token, http.MethodPost, "/api/server/cluster/onboarding/operations", string(raw))
	if response.Code != 409 || len(backend.queued) != before {
		t.Fatal("repeated submission rewrote queued operation")
	}
}

func TestClusterSSHOnboardingEncryptedKeyKeepsExactSyntax(t *testing.T) {
	service, backend, token, body := sshOnboardingTestService(t)
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	passphrase := "  <passphrase> $ preserved  "
	block, err := ssh.MarshalPrivateKeyWithPassphrase(private, "fixture", []byte(passphrase))
	if err != nil {
		t.Fatal(err)
	}
	encodedKey := string(pem.EncodeToMemory(block))
	target := body["targets"].([]any)[1].(map[string]any)
	delete(target, "password")
	target["auth_method"], target["private_key"], target["passphrase"] = "private_key", encodedKey, passphrase
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	response := sshOnboardingTestRequest(t, service, token, http.MethodPost, "/api/server/cluster/onboarding/operations", string(raw))
	if response.Code != 202 || len(backend.sealed) != 2 {
		t.Fatalf("encrypted private-key submission failed: %d", response.Code)
	}
	envelope := backend.sealed[1]
	if envelope.Method != "private_key" || envelope.PrivateKey != encodedKey || envelope.Passphrase != passphrase || envelope.Password != "" {
		t.Fatal("private-key credential changed before encryption")
	}
}

func TestClusterSSHOnboardingProgressAndFailuresStayPrivate(t *testing.T) {
	for _, mode := range []string{"unauthenticated progress", "non Admin progress", "bad ID", "query", "body", "missing", "source error", "seal error", "queue error", "wrong batch", "busy"} {
		t.Run(mode, func(t *testing.T) {
			service, backend, token, body := sshOnboardingTestService(t)
			method, path, raw := http.MethodGet, "/api/server/cluster/onboarding/operations/"+body["request_id"].(string), ""
			want := 400
			switch mode {
			case "unauthenticated progress":
				token = "bad"
				want = 401
			case "non Admin progress":
				service.auth, token = clusterTestAuth(t, &clusterTestStore{profile: operatorProfile{Role: "User"}})
				want = 403
			case "bad ID":
				path = "/api/server/cluster/onboarding/operations/not-an-id"
			case "query":
				path += "?password=forbidden"
			case "body":
				raw = "{}"
			case "missing":
				want = 404
			default:
				method, path = http.MethodPost, "/api/server/cluster/onboarding/operations"
				encoded, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				raw = string(encoded)
				backend.err = errors.New("sensitive database/crypto diagnostics")
				want = 503
				if mode == "source error" {
					backend.failAt = "source"
				}
				if mode == "seal error" {
					backend.failAt = "seal"
					want = 409
				}
				if mode == "queue error" {
					backend.failAt = "queue"
				}
				if mode == "wrong batch" {
					backend.source.ActiveSize, backend.source.Members, backend.source.DesiredSize, backend.source.Status = 2, 2, 3, "Degraded Quorum"
					want = 409
				}
				if mode == "busy" {
					service.slots <- struct{}{}
				}
			}
			response := sshOnboardingTestRequest(t, service, token, method, path, raw)
			if response.Code != want || strings.Contains(response.Body.String(), "sensitive") {
				t.Fatalf("wrong private failure boundary: status=%d", response.Code)
			}
			if textInSet(mode, "unauthenticated progress", "non Admin progress", "bad ID", "query", "body", "busy") && len(backend.calls) != 0 {
				t.Fatal("invalid or busy request reached backing store")
			}
			if mode == "wrong batch" && strings.Join(backend.calls, ",") != "source" {
				t.Fatal("invalid source batch reached encryption")
			}
		})
	}
}
