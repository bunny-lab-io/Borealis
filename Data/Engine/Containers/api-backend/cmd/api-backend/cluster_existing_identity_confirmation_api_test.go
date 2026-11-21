package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type existingIdentityConfirmationAPIStore struct {
	existingIdentityAPIStore
	confirmed  int
	state      string
	actor      string
	confirmErr error
}

func (s *existingIdentityConfirmationAPIStore) clusterExistingIdentityPlan(ctx context.Context, target, id string) (clusterExistingIdentityPlan, string, error) {
	p, state, err := s.existingIdentityAPIStore.clusterExistingIdentityPlan(ctx, target, id)
	if s.state != "" {
		state = s.state
	}
	return p, state, err
}
func (s *existingIdentityConfirmationAPIStore) confirmClusterExistingIdentity(ctx context.Context, actor string, p clusterExistingIdentityPlan, digest string, target clusterSSHPlannedTarget) (clusterExistingIdentityConfirmation, error) {
	s.confirmed++
	s.actor = actor
	if d, ok := ctx.Deadline(); !ok || time.Until(d) > 20*time.Second {
		panic("unbounded confirmation")
	}
	if s.confirmErr != nil {
		return clusterExistingIdentityConfirmation{}, s.confirmErr
	}
	hash, err := clusterSSHPreparationDigest([]string{target.Sealed.generation, target.Sealed.ciphertext})
	return clusterExistingIdentityConfirmation{Version: 1, RequestID: target.Binding.TargetID, PlanSHA256: digest, Actor: actor, ConfirmedAt: time.Now().Unix(), Binding: target.Binding,
		HostKeyAlgorithm: target.Key.Algorithm, HostKeyBase64: target.KeyBase64, CredentialSHA256: hash}, err
}

func TestClusterExistingIdentityAPIConfirmation(t *testing.T) {
	for _, mode := range []string{"success", "unauthenticated", "nonadmin", "query", "encoding", "bad path", "unknown", "alias", "duplicate", "actor", "wrong phrase", "bad UUID", "bad digest", "UTF8", "oversize", "unapproved pin", "wrong pin", "unknown target", "duplicate target field", "mixed credentials", "bad username", "bad sudo", "oversize sudo", "wrong target address", "changed plan digest", "wrong operation", "already confirmed", "cancelled", "busy", "seal error", "store error"} {
		t.Run(mode, func(t *testing.T) {
			p := existingIdentityPlanFixture(t)
			digest, _ := clusterSSHPreparationDigest(p)
			_, _, _, target := clusterSSHTestService(t)
			for _, m := range p.Members {
				if m.NodeID == p.TargetID {
					target["address"] = m.Address
				}
			}
			target["sudo_password"] = "  synthetic sudo<&>$ preserved  "
			body := map[string]any{"request_id": newClusterUUID(), "plan_sha256": digest, "confirmation": "RECOVER ENGINE IDENTITY", "target": target}
			auth, token := clusterTestAuth(t, &clusterTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}})
			backend := &existingIdentityConfirmationAPIStore{existingIdentityAPIStore: existingIdentityAPIStore{plan: p}}
			sealer := &sshOnboardingTestBackend{}
			s := &clusterExistingIdentityAPI{auth: auth, store: backend, sealer: sealer, slots: make(chan struct{}, 1)}
			path := "/api/server/cluster/nodes/" + p.TargetID + "/identity-recovery/operations/" + p.OperationID + "/confirm"
			want := 400
			switch mode {
			case "success":
				want = 202
			case "unauthenticated":
				token, want = "invalid", 401
			case "nonadmin":
				s.auth, token = clusterTestAuth(t, &clusterTestStore{profile: operatorProfile{Role: "User"}})
				want = 403
			case "query":
				path += "?"
			case "bad path":
				path = strings.Replace(path, p.OperationID, "bad", 1)
			case "unknown", "actor":
				body[mode] = "forged"
			case "wrong phrase":
				body["confirmation"] = "PREPARE ENGINE NODES"
			case "bad UUID":
				body["request_id"] = "bad"
			case "bad digest":
				body["plan_sha256"] = "bad"
			case "unapproved pin":
				target["host_key_approved"] = false
			case "wrong pin":
				target["host_key_fingerprint"] = "SHA256:" + strings.Repeat("a", 43)
			case "unknown target":
				target["command"] = "not allowed"
			case "mixed credentials":
				target["private_key"] = "forbidden"
			case "bad username":
				target["username"] = "user; command"
			case "bad sudo":
				target["sudo_password"] = "line1\nline2"
			case "oversize sudo":
				target["sudo_password"] = strings.Repeat("s", 4097)
			case "wrong target address":
				target["address"] = "192.168.90.99"
				want = 409
			case "changed plan digest":
				body["plan_sha256"] = strings.Repeat("f", 64)
				want = 409
			case "wrong operation":
				backend.plan.OperationID = newClusterUUID()
				want = 409
			case "already confirmed":
				backend.state = "awaiting_identity_observation"
				want = 409
			case "cancelled":
				backend.state = "cancelled"
				want = 409
			case "busy":
				s.slots <- struct{}{}
				want = 503
			case "seal error":
				sealer.failAt = "seal"
				sealer.err = errors.New("private-seal-error")
				want = 409
			case "store error":
				backend.confirmErr = errors.New("private-database-error")
				want = 503
			}
			raw := marshalClusterJSON(body)
			switch mode {
			case "unauthenticated", "nonadmin":
				raw = "invalid"
			case "alias":
				raw = strings.Replace(raw, `"request_id"`, `"Request_ID"`, 1)
			case "duplicate":
				raw = strings.Replace(raw, "{", `{"request_id":"`+newClusterUUID()+`",`, 1)
			case "duplicate target field":
				raw = strings.Replace(raw, `"target":{`, `"target":{"username":"operator",`, 1)
			case "UTF8":
				raw = strings.Replace(raw, "RECOVER", "\xffRECOVER", 1)
			case "oversize":
				raw += strings.Repeat(" ", clusterSSHQueueBodyMaxBytes+1)
			}
			r := clusterTestRequest(t, http.MethodPost, path, raw, token)
			if mode == "encoding" {
				r.Header.Set("Content-Encoding", "gzip")
			}
			mux := http.NewServeMux()
			mux.HandleFunc("POST /api/server/cluster/nodes/{id}/identity-recovery/operations/{operation}/confirm", s.confirm)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != want || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "sudo") || strings.Contains(w.Body.String(), "ciphertext") {
				t.Fatal("unsafe confirmation response", w.Code, w.Body.String())
			}
			if want == 400 || want == 401 || want == 403 || mode == "busy" {
				if backend.calls != 0 || backend.confirmed != 0 || len(sealer.calls) != 0 {
					t.Fatal("invalid input reached domain")
				}
			}
			if mode == "success" {
				if backend.confirmed != 1 || backend.actor != "operator" || len(sealer.sealed) != 1 || sealer.sealed[0].Password != target["password"] || sealer.sealed[0].SudoPassword != target["sudo_password"] || !strings.Contains(w.Body.String(), `"recovery_authorized":false`) {
					t.Fatal("confirmation lost credential syntax or authority boundary")
				}
			}
		})
	}
}

func TestClusterExistingIdentityConfirmationCanonical(t *testing.T) {
	p := existingIdentityPlanFixture(t)
	digest, _ := clusterSSHPreparationDigest(p)
	_, remote, _, _ := clusterSSHTestService(t)
	address := ""
	for _, m := range p.Members {
		if m.NodeID == p.TargetID {
			address = m.Address
		}
	}
	request := newClusterUUID()
	for _, mode := range []string{"valid", "unknown", "alias", "duplicate", "wrong digest", "wrong target", "wrong actor", "future confirmation", "old confirmation", "extra nested field"} {
		t.Run(mode, func(t *testing.T) {
			c := clusterExistingIdentityConfirmation{Version: 1, RequestID: request, PlanSHA256: digest, Actor: "admin", ConfirmedAt: time.Now().Unix(), Binding: clusterSSHCredentialBinding{ClusterID: p.ClusterID, OperationID: p.OperationID, TargetID: request, Address: address, Port: 22, Fingerprint: remote.key.Fingerprint},
				HostKeyAlgorithm: remote.key.Algorithm, HostKeyBase64: base64.StdEncoding.EncodeToString(remote.key.PublicKey), CredentialSHA256: strings.Repeat("e", 64)}
			switch mode {
			case "wrong digest":
				c.PlanSHA256 = strings.Repeat("f", 64)
			case "wrong target":
				c.Binding.Address = "192.168.90.99"
			case "wrong actor":
				c.Actor = "admin\n"
			case "future confirmation":
				c.ConfirmedAt = p.ObservedAt + 300
			case "old confirmation":
				c.ConfirmedAt = p.ObservedAt - 1
			}
			raw := marshalClusterJSON(clusterExistingIdentityConfirmedPayload{Version: 2, Plan: p, Confirmation: c})
			switch mode {
			case "unknown":
				raw = strings.Replace(raw, "{", `{"extra":true,`, 1)
			case "alias":
				raw = strings.Replace(raw, `"Version"`, `"version"`, 1)
			case "duplicate":
				raw = strings.Replace(raw, "{", `{"Version":2,`, 1)
			case "extra nested field":
				var body map[string]any
				_ = json.Unmarshal([]byte(raw), &body)
				body["Confirmation"].(map[string]any)["extra"] = true
				raw = marshalClusterJSON(body)
			}
			_, _, err := parseClusterExistingIdentityPayload([]byte(raw))
			if (err == nil) != (mode == "valid") {
				t.Fatal("canonical confirmation", mode, err)
			}
		})
	}
}
