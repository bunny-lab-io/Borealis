package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type sshPreparationAPITestStore struct {
	snapshot clusterSSHPreparationApprovalSnapshot
	actor    string
	calls    int
	err      error
}

func (s *sshPreparationAPITestStore) clusterSSHPreparationProposal(ctx context.Context, id string) (clusterSSHPreparationApprovalSnapshot, int64, error) {
	s.calls++
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 5*time.Second {
		panic("unbounded plan read")
	}
	return s.snapshot, time.Now().Unix(), s.err
}

func (s *sshPreparationAPITestStore) requestClusterSSHPreparation(ctx context.Context, id, actor string, raw []byte) (clusterSSHPreparationConfirmation, error) {
	s.calls++
	s.actor = actor
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 10*time.Second {
		panic("unbounded confirmation write")
	}
	if s.err != nil {
		return clusterSSHPreparationConfirmation{}, s.err
	}
	return newClusterSSHPreparationConfirmation(raw, actor, time.Now().Unix(), s.snapshot.work.Authorization, s.snapshot.work.Proof.Source)
}

func sshPreparationAPIService(t *testing.T) (*clusterSSHPreparationAPI, *sshPreparationAPITestStore, string, string) {
	t.Helper()
	auth, token := clusterTestAuth(t, &clusterTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}})
	cohort, source, _, baseline := sshPreparationFixture(t)
	proof := clusterSSHParentProof{Version: 1, Cohort: cohort, Source: source}
	report := newClusterSSHQualificationReport(cohort.Attempt)
	authorization := sshPreparationAuthorizationFixture(t, proof, clusterSSHQueueSource{}, &report)
	backend := &sshPreparationAPITestStore{snapshot: clusterSSHPreparationApprovalSnapshot{work: clusterSSHPreparationWork{
		Proof: proof, Baseline: baseline, Authorization: authorization, Payload: `{"private":"do-not-disclose"}`},
		credentials: []sealedClusterSSHCredentials{{ciphertext: "do-not-disclose-credential", generation: "private-generation"}}}}
	plan, _ := clusterSSHPreparationPlanDigest(authorization)
	raw := marshalClusterJSON(map[string]any{"request_id": newClusterUUID(), "plan_sha256": plan, "confirmation": "PREPARE ENGINE NODES", "source_id": source.Members[0].NodeID})
	return &clusterSSHPreparationAPI{auth: auth, store: backend, slots: make(chan struct{}, 1)}, backend, token, raw
}

func sshPreparationAPIRequest(t *testing.T, s *clusterSSHPreparationAPI, token, method, path, raw string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/server/cluster/onboarding/operations/{id}/preparation-plan", s.plan)
	mux.HandleFunc("POST /api/server/cluster/onboarding/operations/{id}/prepare", s.request)
	r := clusterTestRequest(t, method, path, raw, token)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestClusterSSHPreparationAPIAdminAndValidation(t *testing.T) {
	for _, mode := range []string{"success", "unauthenticated", "non admin", "bad id", "query", "unknown", "alias", "duplicate", "timestamp", "actor", "bad UUID", "bad digest", "missing source", "bad source", "zero source", "source UID injection", "wrong phrase", "oversize", "invalid UTF-8", "backend error", "busy"} {
		t.Run(mode, func(t *testing.T) {
			s, backend, token, raw := sshPreparationAPIService(t)
			path := "/api/server/cluster/onboarding/operations/" + backend.snapshot.work.Authorization.OperationID + "/prepare"
			want := 400
			switch mode {
			case "success":
				want = 202
			case "unauthenticated":
				token, raw, want = "invalid", "invalid body", 401
			case "non admin":
				s.auth, token = clusterTestAuth(t, &clusterTestStore{profile: operatorProfile{Role: "User"}})
				raw, want = "invalid body", 403
			case "bad id":
				path = "/api/server/cluster/onboarding/operations/not-a-uuid/prepare"
			case "query":
				path += "?force=true"
			case "unknown", "timestamp", "actor":
				raw = strings.Replace(raw, "{", `{"`+mode+`":"not allowed",`, 1)
			case "alias":
				raw = strings.Replace(raw, `"request_id"`, `"Request_ID"`, 1)
			case "duplicate":
				raw = strings.Replace(raw, "{", `{"request_id":"`+newClusterUUID()+`",`, 1)
			case "bad UUID", "bad digest":
				var body map[string]any
				_ = json.Unmarshal([]byte(raw), &body)
				key := "request_id"
				if mode == "bad digest" {
					key = "plan_sha256"
				}
				body[key] = "invalid"
				raw = marshalClusterJSON(body)
			case "wrong phrase":
				raw = strings.Replace(raw, "PREPARE ENGINE NODES", "INSPECT ENGINE NODES", 1)
			case "missing source", "bad source", "zero source", "source UID injection":
				var body map[string]any
				_ = json.Unmarshal([]byte(raw), &body)
				switch mode {
				case "missing source":
					delete(body, "source_id")
				case "bad source":
					body["source_id"] = "invalid"
				case "zero source":
					body["source_id"] = "00000000-0000-0000-0000-000000000000"
				case "source UID injection":
					body["source_uid"] = newClusterUUID()
				}
				raw = marshalClusterJSON(body)
			case "oversize":
				raw += strings.Repeat(" ", 4097)
			case "invalid UTF-8":
				raw = strings.Replace(raw, "PREPARE ENGINE NODES", "PREPARE ENGINE NODES\xff", 1)
			case "backend error":
				backend.err, want = errors.New("private-database-error"), 503
			case "busy":
				s.slots <- struct{}{}
				want = 503
			}
			w := sshPreparationAPIRequest(t, s, token, http.MethodPost, path, raw)
			if w.Code != want || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "private") {
				t.Fatal("unsafe or incorrect confirmation response", w.Code, w.Body.String())
			}
			if mode != "success" && mode != "backend error" && backend.calls != 0 {
				t.Fatal("invalid request reached domain work")
			}
			if mode == "success" && (backend.actor != "operator" || !strings.Contains(w.Body.String(), `"preparation_authorized":false`)) {
				t.Fatal("actor was not server-derived or intent granted authority")
			}
		})
	}
}

func TestClusterSSHPreparationAPIPlanProjection(t *testing.T) {
	for _, mode := range []string{"success", "confirmed", "unauthenticated", "non admin", "query", "body", "stale confirmation"} {
		t.Run(mode, func(t *testing.T) {
			s, backend, token, raw := sshPreparationAPIService(t)
			path := "/api/server/cluster/onboarding/operations/" + backend.snapshot.work.Authorization.OperationID + "/preparation-plan"
			body, want := "", 200
			switch mode {
			case "confirmed", "stale confirmation":
				c, err := newClusterSSHPreparationConfirmation([]byte(raw), "operator", time.Now().Unix(), backend.snapshot.work.Authorization, backend.snapshot.work.Proof.Source)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "stale confirmation" {
					c.ConfirmedAt = 1
					want = 409
				}
				backend.snapshot.work.Payload = marshalClusterJSON(map[string]any{"ssh_preparation_request": c})
			case "unauthenticated":
				token, want = "invalid", 401
			case "non admin":
				s.auth, token = clusterTestAuth(t, &clusterTestStore{profile: operatorProfile{Role: "User"}})
				want = 403
			case "query":
				path, want = path+"?force=true", 400
			case "body":
				body, want = "{}", 400
			}
			w := sshPreparationAPIRequest(t, s, token, http.MethodGet, path, body)
			if w.Code != want || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "do-not-disclose") ||
				strings.Contains(w.Body.String(), "credential_sha256") || strings.Contains(w.Body.String(), "controller_holder") {
				t.Fatal("unsafe plan projection", w.Code, w.Body.String())
			}
			if want == 200 && (!strings.Contains(w.Body.String(), `"preparation_authorized":false`) || !strings.Contains(w.Body.String(), `"targets"`)) {
				t.Fatal("plan projection lost readiness boundary")
			}
		})
	}
}
