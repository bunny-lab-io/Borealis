package main

import (
	"context"
	"encoding/json"
	"mime"
	"net/http"
	"time"
)

type clusterSSHPreparationRequestStore interface {
	clusterSSHPreparationProposal(context.Context, string) (clusterSSHPreparationApprovalSnapshot, int64, error)
	requestClusterSSHPreparation(context.Context, string, string, []byte) (clusterSSHPreparationConfirmation, error)
}

type clusterSSHPreparationAPI struct {
	auth  *authService
	store clusterSSHPreparationRequestStore
	slots chan struct{}
}

func registerClusterSSHPreparationRoutes(mux *http.ServeMux, auth *authService) {
	service := &clusterSSHPreparationAPI{auth: auth, slots: make(chan struct{}, 2)}
	if auth != nil {
		service.store, _ = auth.store.(clusterSSHPreparationRequestStore)
	}
	mux.HandleFunc("GET /api/server/cluster/onboarding/operations/{id}/preparation-plan", service.plan)
	mux.HandleFunc("POST /api/server/cluster/onboarding/operations/{id}/prepare", service.request)
}

func clusterSSHPreparationRequestPath(r *http.Request) bool {
	return clusterUUIDRE.MatchString(r.PathValue("id")) && r.URL.RawQuery == ""
}

func (s *clusterSSHPreparationAPI) plan(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if _, failure := requireAdmin(r.Context(), s.auth, r); failure != nil {
		failure.write(w)
		return
	}
	if !clusterSSHPreparationRequestPath(r) || r.ContentLength != 0 {
		writePublicValidationErrors(w, []publicValidationError{{Field: "id", Message: "canonical operation UUID and no query/body required"}})
		return
	}
	if s.store == nil {
		writeClusterSSHOnboardingError(w, errClusterUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	snapshot, now, err := s.store.clusterSSHPreparationProposal(ctx, r.PathValue("id"))
	if err != nil {
		writeClusterSSHOnboardingError(w, err)
		return
	}
	plan, err := clusterSSHPreparationPlanDigest(snapshot.work.Authorization)
	if err != nil {
		writeClusterSSHOnboardingError(w, errClusterConflict)
		return
	}
	var raw json.RawMessage
	if clusterBootstrapObject([]byte(snapshot.work.Payload), map[string]any{"ssh_preparation_request": &raw}) != nil {
		writeClusterSSHOnboardingError(w, errClusterConflict)
		return
	}
	var confirmation any
	if len(raw) != 0 {
		stored, err := parseClusterSSHPreparationRequest(raw, snapshot.work.Authorization, snapshot.work.Proof.Source)
		if err != nil || stored.ConfirmedAt > now || now-stored.ConfirmedAt >= clusterSSHInspectionLifetimeSeconds {
			writeClusterSSHOnboardingError(w, errClusterConflict)
			return
		}
		confirmation = map[string]any{"request_id": stored.RequestID, "confirmed_at": stored.ConfirmedAt, "source_id": stored.IdentitySource.SourceID}
	}
	targets := make([]map[string]any, 0, len(snapshot.work.Proof.Cohort.Targets))
	for _, target := range snapshot.work.Proof.Cohort.Targets {
		targets = append(targets, map[string]any{"target_id": target.Binding.TargetID, "address": target.Binding.Address,
			"port": target.Binding.Port, "hostname": target.Report.Hostname, "host_key_fingerprint": target.Binding.Fingerprint})
	}
	// Explicit projection: never serialize snapshot, authorization or credentials.
	sources := make([]map[string]any, 0, len(snapshot.work.Proof.Source.Members))
	for _, member := range snapshot.work.Proof.Source.Members {
		sources = append(sources, map[string]any{"source_id": member.NodeID, "hostname": member.Name})
	}
	writeJSON(w, http.StatusOK, map[string]any{"operation_id": snapshot.work.Authorization.OperationID,
		"attempt": snapshot.work.Authorization.Attempt, "plan_sha256": plan, "baseline_release": snapshot.work.Baseline.Release,
		"source_sha": snapshot.work.Baseline.SourceSHA, "targets": targets, "identity_sources": sources, "confirmation": confirmation,
		"preparation_authorized": false, "state": "awaiting_readiness"})
}

func (s *clusterSSHPreparationAPI) request(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	identity, failure := requireAdmin(r.Context(), s.auth, r)
	if failure != nil {
		failure.write(w)
		return
	}
	media, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if !clusterSSHPreparationRequestPath(r) || mediaErr != nil || media != "application/json" {
		writePublicValidationErrors(w, []publicValidationError{{Field: "body", Message: "canonical operation UUID, JSON and no query required"}})
		return
	}
	raw, err := readLimitedRequestBody(r, 4096)
	if err != nil {
		writePublicValidationErrors(w, []publicValidationError{{Field: "body", Message: "bounded preparation confirmation required"}})
		return
	}
	defer clear(raw)
	if _, _, _, err := parseClusterSSHPreparationRequestBody(raw); err != nil {
		writePublicValidationErrors(w, []publicValidationError{{Field: "body", Message: "exact preparation confirmation fields required"}})
		return
	}
	if s.store == nil || s.slots == nil {
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
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	confirmation, err := s.store.requestClusterSSHPreparation(ctx, r.PathValue("id"), identity.Username, raw)
	if err != nil {
		writeClusterSSHOnboardingError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"operation_id": confirmation.OperationID, "request_id": confirmation.RequestID,
		"plan_sha256": confirmation.PlanSHA256, "confirmed_at": confirmation.ConfirmedAt, "source_id": confirmation.IdentitySource.SourceID,
		"state": "awaiting_readiness", "preparation_authorized": false})
}
