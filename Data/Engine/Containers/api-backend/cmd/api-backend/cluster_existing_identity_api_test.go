package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func existingIdentityPlanFixture(t *testing.T) clusterExistingIdentityPlan {
	t.Helper()
	p := clusterExistingIdentityPlan{Version: 1, OperationID: newClusterUUID(), MaintenanceID: newClusterUUID(), MaintenanceAttempt: 1,
		ObservedAt: time.Now().Unix(), Controller: "private-controller", ClusterID: newClusterUUID(), KubeSystemUID: newClusterUUID(),
		Baseline: clusterbootstrap.Expected{Repository: clusterGitHubRepo(), Release: "2026.9.5.1", SourceSHA: strings.Repeat("a", 40)}, ConfigSHA256: strings.Repeat("b", 64), SnapshotSHA256: strings.Repeat("c", 64)}
	for i := 0; i < 3; i++ {
		p.Members = append(p.Members, clusterSSHSourceMember{NodeID: newClusterUUID(), NodeUID: newClusterUUID(), Name: fmt.Sprintf("engine-%d", i+1),
			Address: fmt.Sprintf("192.168.90.%d", i+10), MachineID: fmt.Sprintf("%032x", i+1), BootID: newClusterUUID()})
	}
	slices.SortFunc(p.Members, func(a, b clusterSSHSourceMember) int { return strings.Compare(a.NodeID, b.NodeID) })
	p.SourceID, p.TargetID = p.Members[0].NodeID, p.Members[2].NodeID
	if !p.valid() {
		t.Fatal("invalid plan fixture")
	}
	return p
}

type existingIdentityAPIStore struct {
	plan  clusterExistingIdentityPlan
	calls int
	err   error
}

func (s *existingIdentityAPIStore) clusterExistingIdentityController(ctx context.Context) (string, error) {
	s.calls++
	if d, ok := ctx.Deadline(); !ok || time.Until(d) > 20*time.Second {
		panic("unbounded controller read")
	}
	return s.plan.Controller, s.err
}
func (s *existingIdentityAPIStore) clusterExistingIdentityPlan(ctx context.Context, target, id string) (clusterExistingIdentityPlan, string, error) {
	s.calls++
	if d, ok := ctx.Deadline(); !ok || time.Until(d) > 5*time.Second {
		panic("unbounded plan read")
	}
	return s.plan, "waiting", s.err
}

func TestClusterExistingIdentityAPIPlan(t *testing.T) {
	for _, mode := range []string{"success", "unauthenticated", "nonadmin", "bad path", "query", "empty query", "encoding", "media", "unknown", "alias", "duplicate", "actor", "nil", "UTF8", "oversize", "source target", "bad UUID", "zero UUID", "phrase", "busy", "unavailable", "substitution"} {
		t.Run(mode, func(t *testing.T) {
			p := existingIdentityPlanFixture(t)
			auth, token := clusterTestAuth(t, &clusterTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}})
			backend := &existingIdentityAPIStore{plan: p}
			calls := 0
			s := &clusterExistingIdentityAPI{auth: auth, store: backend, slots: make(chan struct{}, 1), reserve: func(ctx context.Context, req clusterExistingIdentityBrokerRequest) (clusterExistingIdentityPlan, error) {
				calls++
				if !req.valid(time.Now()) || req.Actor != "operator" || !req.matches(p) {
					t.Error("unbound dispatch")
				}
				if mode == "substitution" {
					p.TargetID = p.SourceID
				}
				return p, nil
			}}
			input := clusterExistingIdentityPlanInput{RequestID: p.OperationID, MaintenanceID: p.MaintenanceID, SourceID: p.SourceID, Confirmation: "PLAN IDENTITY RECOVERY"}
			path := "/api/server/cluster/nodes/" + p.TargetID + "/identity-recovery/plan"
			raw := marshalClusterJSON(input)
			want := 400
			switch mode {
			case "success":
				want = 202
			case "unauthenticated":
				token, raw, want = "invalid", "invalid", 401
			case "nonadmin":
				s.auth, token = clusterTestAuth(t, &clusterTestStore{profile: operatorProfile{Role: "User"}})
				raw, want = "invalid", 403
			case "bad path":
				path = strings.Replace(path, p.TargetID, "invalid", 1)
			case "query":
				path += "?force=true"
			case "empty query":
				path += "?"
			case "unknown", "actor":
				raw = strings.Replace(raw, "{", `{"`+mode+`":"forged",`, 1)
			case "alias":
				raw = strings.Replace(raw, "request_id", "Request_ID", 1)
			case "duplicate":
				raw = strings.Replace(raw, "{", `{"request_id":"`+p.OperationID+`",`, 1)
			case "nil":
				raw = "null"
			case "UTF8":
				raw = strings.Replace(raw, "PLAN", "\xffPLAN", 1)
			case "oversize":
				raw += strings.Repeat(" ", 4097)
			case "source target":
				raw = strings.Replace(raw, p.SourceID, p.TargetID, 1)
			case "bad UUID":
				raw = strings.Replace(raw, p.OperationID, "invalid", 1)
			case "zero UUID":
				raw = strings.Replace(raw, p.OperationID, "00000000-0000-0000-0000-000000000000", 1)
			case "phrase":
				raw = strings.Replace(raw, "PLAN IDENTITY RECOVERY", "RECOVER", 1)
			case "busy":
				s.slots <- struct{}{}
				want = 503
			case "unavailable":
				backend.err = errors.New("private database error")
				want = 503
			case "substitution":
				want = 409
			}
			r := clusterTestRequest(t, http.MethodPost, path, raw, token)
			if mode == "encoding" {
				r.Header.Set("Content-Encoding", "gzip")
			}
			if mode == "media" {
				r.Header.Set("Content-Type", "text/plain")
			}
			mux := http.NewServeMux()
			mux.HandleFunc("POST /api/server/cluster/nodes/{id}/identity-recovery/plan", s.plan)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != want || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "private") {
				t.Fatal("unsafe response", w.Code, w.Body.String())
			}
			if want == 400 || want == 401 || want == 403 || mode == "busy" {
				if backend.calls != 0 || calls != 0 {
					t.Fatal("invalid input reached domain")
				}
			}
			if mode == "success" && (calls != 1 || !strings.Contains(w.Body.String(), `"recovery_authorized":false`)) {
				t.Fatal("lost planning boundary")
			}
		})
	}
}

func TestClusterExistingIdentityAPIReadback(t *testing.T) {
	for _, mode := range []string{"success", "unauthenticated", "nonadmin", "target substitution", "operation substitution", "body", "query", "missing"} {
		t.Run(mode, func(t *testing.T) {
			p := existingIdentityPlanFixture(t)
			auth, token := clusterTestAuth(t, &clusterTestStore{profile: operatorProfile{Role: "Admin"}})
			backend := &existingIdentityAPIStore{plan: p}
			s := &clusterExistingIdentityAPI{auth: auth, store: backend}
			path := "/api/server/cluster/nodes/" + p.TargetID + "/identity-recovery/operations/" + p.OperationID
			want, raw := 200, ""
			switch mode {
			case "unauthenticated":
				token, want = "invalid", 401
			case "nonadmin":
				s.auth, token = clusterTestAuth(t, &clusterTestStore{profile: operatorProfile{Role: "User"}})
				want = 403
			case "target substitution":
				backend.plan.TargetID = p.SourceID
				want = 409
			case "operation substitution":
				backend.plan.OperationID = newClusterUUID()
				want = 409
			case "body":
				raw, want = "{}", 400
			case "query":
				path += "?"
				want = 400
			case "missing":
				backend.err = errClusterConflict
				want = 409
			}
			mux := http.NewServeMux()
			mux.HandleFunc("GET /api/server/cluster/nodes/{id}/identity-recovery/operations/{operation}", s.read)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, clusterTestRequest(t, http.MethodGet, path, raw, token))
			if w.Code != want || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "MachineID") || strings.Contains(w.Body.String(), "ConfigSHA256") {
				t.Fatal("unsafe readback", w.Code, w.Body.String())
			}
		})
	}
}

func TestClusterExistingIdentityPlanCanonical(t *testing.T) {
	for _, mode := range []string{"valid", "unknown", "duplicate", "alias", "member extra", "member duplicate", "unsorted", "missing source", "target source", "zero machine", "bad digest", "bad baseline"} {
		t.Run(mode, func(t *testing.T) {
			p := existingIdentityPlanFixture(t)
			switch mode {
			case "member duplicate":
				p.Members[1].NodeUID = p.Members[0].NodeUID
			case "unsorted":
				slices.Reverse(p.Members)
			case "missing source":
				p.SourceID = newClusterUUID()
			case "target source":
				p.TargetID = p.SourceID
			case "zero machine":
				p.Members[0].MachineID = strings.Repeat("0", 32)
			case "bad digest":
				p.ConfigSHA256 = "bad"
			case "bad baseline":
				p.Baseline.SourceSHA = "main"
			}
			raw := marshalClusterJSON(p)
			switch mode {
			case "unknown":
				raw = strings.Replace(raw, "{", `{"extra":true,`, 1)
			case "duplicate":
				raw = strings.Replace(raw, "{", `{"Version":1,`, 1)
			case "alias":
				raw = strings.Replace(raw, `"Version"`, `"version"`, 1)
			case "member extra":
				var body map[string]any
				_ = json.Unmarshal([]byte(raw), &body)
				body["Members"].([]any)[0].(map[string]any)["extra"] = true
				raw = marshalClusterJSON(body)
			}
			_, err := parseClusterExistingIdentityPlan([]byte(raw))
			if (err == nil) != (mode == "valid") {
				t.Fatal("canonical plan boundary", mode, err)
			}
		})
	}
}
