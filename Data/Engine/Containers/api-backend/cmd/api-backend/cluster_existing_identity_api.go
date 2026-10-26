package main

import (
	"context"
	"mime"
	"net/http"
	"time"
	"unicode/utf8"
)

type clusterExistingIdentityPlanInput struct {
	RequestID     string `json:"request_id"`
	MaintenanceID string `json:"maintenance_operation_id"`
	SourceID      string `json:"source_node_id"`
	Confirmation  string `json:"confirmation"`
}

func parseClusterExistingIdentityPlanInput(raw []byte) (clusterExistingIdentityPlanInput, error) {
	var p clusterExistingIdentityPlanInput
	if len(raw) == 0 || len(raw) > 4096 || !utf8.Valid(raw) {
		return p, errClusterConflict
	}
	object, err := decodeClusterSSHObject(raw, map[string]bool{"request_id": true, "maintenance_operation_id": true, "source_node_id": true, "confirmation": true})
	if err != nil || len(object) != 4 {
		return p, errClusterConflict
	}
	for key, out := range map[string]*string{"request_id": &p.RequestID, "maintenance_operation_id": &p.MaintenanceID, "source_node_id": &p.SourceID} {
		*out, err = clusterSSHString(object, key, 36, false)
		if err != nil || !clusterUUIDRE.MatchString(*out) || *out == "00000000-0000-0000-0000-000000000000" {
			return p, errClusterConflict
		}
	}
	p.Confirmation, err = clusterSSHString(object, "confirmation", 32, false)
	if err != nil || p.Confirmation != "PLAN IDENTITY RECOVERY" {
		return p, errClusterConflict
	}
	return p, nil
}

type clusterExistingIdentityPlanStore interface {
	clusterExistingIdentityController(context.Context) (string, error)
	clusterExistingIdentityPlan(context.Context, string, string) (clusterExistingIdentityPlan, string, error)
}

type clusterExistingIdentityAPI struct {
	auth    *authService
	store   clusterExistingIdentityPlanStore
	reserve func(context.Context, clusterExistingIdentityBrokerRequest) (clusterExistingIdentityPlan, error)
	slots   chan struct{}
}

func registerClusterExistingIdentityRoutes(mux *http.ServeMux, auth *authService) {
	s := &clusterExistingIdentityAPI{auth: auth, slots: make(chan struct{}, 2)}
	if auth != nil {
		s.store, _ = auth.store.(clusterExistingIdentityPlanStore)
	}
	if client, err := newClusterExistingIdentityBrokerClient(); err == nil {
		s.reserve = client.reserve
	}
	mux.HandleFunc("POST /api/server/cluster/nodes/{id}/identity-recovery/plan", s.plan)
	mux.HandleFunc("GET /api/server/cluster/nodes/{id}/identity-recovery/operations/{operation}", s.read)
}

func writeClusterExistingIdentityPlan(w http.ResponseWriter, status int, p clusterExistingIdentityPlan, state string) {
	if !p.valid() {
		writeClusterSSHOnboardingError(w, errClusterConflict)
		return
	}
	digest, err := clusterSSHPreparationDigest(p)
	if err != nil {
		writeClusterSSHOnboardingError(w, errClusterConflict)
		return
	}
	members := make([]map[string]string, 0, len(p.Members))
	for _, m := range p.Members {
		members = append(members, map[string]string{"node_id": m.NodeID, "node_uid": m.NodeUID, "name": m.Name, "address": m.Address})
	}
	writeJSON(w, status, map[string]any{"operation_id": p.OperationID, "source_node_id": p.SourceID, "target_node_id": p.TargetID,
		"maintenance_operation_id": p.MaintenanceID, "plan_sha256": digest, "observed_at": p.ObservedAt, "state": state,
		"baseline_release": p.Baseline.Release, "source_sha": p.Baseline.SourceSHA, "members": members, "recovery_authorized": false})
}

func (s *clusterExistingIdentityAPI) plan(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	actor, failure := requireAdmin(r.Context(), s.auth, r)
	if failure != nil {
		failure.write(w)
		return
	}
	bad := func() {
		writePublicValidationErrors(w, []publicValidationError{{Field: "body", Message: "canonical target UUID, exact bounded JSON plan fields and no query required"}})
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if !clusterUUIDRE.MatchString(r.PathValue("id")) || r.URL.RawQuery != "" || r.URL.ForceQuery || err != nil || media != "application/json" || r.Header.Get("Content-Encoding") != "" {
		bad()
		return
	}
	raw, err := readLimitedRequestBody(r, 4096)
	if err != nil {
		bad()
		return
	}
	defer clear(raw)
	input, err := parseClusterExistingIdentityPlanInput(raw)
	if err != nil || input.SourceID == r.PathValue("id") {
		bad()
		return
	}
	if s.store == nil || s.reserve == nil || s.slots == nil {
		writeClusterSSHOnboardingError(w, errClusterUnavailable)
		return
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		writeClusterSSHOnboardingError(w, errClusterUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	owner, err := s.store.clusterExistingIdentityController(ctx)
	if err != nil {
		writeClusterSSHOnboardingError(w, err)
		return
	}
	request := clusterExistingIdentityBrokerRequest{Version: 1, ID: newClusterUUID(), ExpiresAt: time.Now().Unix() + 20, Controller: owner,
		Input: input, TargetID: r.PathValue("id"), Actor: actor.Username}
	p, err := s.reserve(ctx, request)
	if err != nil {
		writeClusterSSHOnboardingError(w, err)
		return
	}
	if !request.matches(p) {
		writeClusterSSHOnboardingError(w, errClusterConflict)
		return
	}
	writeClusterExistingIdentityPlan(w, http.StatusAccepted, p, "waiting")
}

func (s *clusterExistingIdentityAPI) read(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if _, failure := requireAdmin(r.Context(), s.auth, r); failure != nil {
		failure.write(w)
		return
	}
	if !clusterUUIDRE.MatchString(r.PathValue("id")) || !clusterUUIDRE.MatchString(r.PathValue("operation")) || r.URL.RawQuery != "" || r.URL.ForceQuery || r.ContentLength != 0 {
		writePublicValidationErrors(w, []publicValidationError{{Field: "id", Message: "canonical target and operation UUIDs, no query/body required"}})
		return
	}
	if s.store == nil {
		writeClusterSSHOnboardingError(w, errClusterUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	p, state, err := s.store.clusterExistingIdentityPlan(ctx, r.PathValue("id"), r.PathValue("operation"))
	if err != nil {
		writeClusterSSHOnboardingError(w, err)
		return
	}
	if p.TargetID != r.PathValue("id") || p.OperationID != r.PathValue("operation") {
		writeClusterSSHOnboardingError(w, errClusterConflict)
		return
	}
	writeClusterExistingIdentityPlan(w, http.StatusOK, p, state)
}
