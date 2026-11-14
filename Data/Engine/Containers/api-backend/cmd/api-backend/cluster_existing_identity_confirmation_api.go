package main

import (
	"borealis/api-backend/internal/clusterremote"
	"context"
	"encoding/base64"
	"mime"
	"net/http"
	"time"
	"unicode/utf8"
)

type clusterExistingIdentityConfirmationStore interface {
	confirmClusterExistingIdentity(context.Context, string, clusterExistingIdentityPlan, string, clusterSSHPlannedTarget) (clusterExistingIdentityConfirmation, error)
}

func parseClusterExistingIdentityConfirmationInput(raw []byte) (string, string, clusterSSHSubmissionTarget, error) {
	fail := func() (string, string, clusterSSHSubmissionTarget, error) {
		return "", "", clusterSSHSubmissionTarget{}, errClusterConflict
	}
	if len(raw) == 0 || len(raw) > clusterSSHQueueBodyMaxBytes || !utf8.Valid(raw) {
		return fail()
	}
	body, err := decodeClusterSSHObject(raw, map[string]bool{"request_id": true, "plan_sha256": true, "confirmation": true, "target": true})
	if err != nil || len(body) != 4 {
		return fail()
	}
	defer clearClusterSSHFields(body)
	id, err := clusterSSHString(body, "request_id", 36, false)
	if err != nil || !clusterUUIDRE.MatchString(id) || id == "00000000-0000-0000-0000-000000000000" {
		return fail()
	}
	digest, err := clusterSSHString(body, "plan_sha256", 64, false)
	if err != nil || !validClusterSSHPreparationDigest(digest) {
		return fail()
	}
	phrase, err := clusterSSHString(body, "confirmation", 32, false)
	if err != nil || phrase != "RECOVER ENGINE IDENTITY" || len(body["target"]) > clusterSSHBodyMaxBytes {
		return fail()
	}
	allowed := map[string]bool{}
	for _, key := range []string{"address", "port", "host_key_algorithm", "host_key_fingerprint", "host_key_base64", "host_key_approved", "username", "auth_method", "password", "private_key", "passphrase", "sudo_password"} {
		allowed[key] = true
	}
	fields, err := decodeClusterSSHObject(body["target"], allowed)
	if err != nil {
		return fail()
	}
	defer clearClusterSSHFields(fields)
	target, err := clusterSSHTarget(fields)
	if err != nil {
		return fail()
	}
	key, err := clusterSSHApproval(fields)
	if err != nil {
		return fail()
	}
	credential, err := clusterSSHCredential(fields)
	if err != nil {
		return fail()
	}
	credential.Destroy()
	envelope := clusterSSHCredentialEnvelope{Version: 1}
	envelope.Username, _ = clusterSSHString(fields, "username", 64, false)
	envelope.Method, _ = clusterSSHString(fields, "auth_method", 11, false)
	envelope.Password, _ = clusterSSHString(fields, "password", clusterremote.MaxPasswordBytes, true)
	envelope.PrivateKey, _ = clusterSSHString(fields, "private_key", clusterremote.MaxPrivateKeyBytes, true)
	envelope.Passphrase, _ = clusterSSHString(fields, "passphrase", clusterremote.MaxPasswordBytes, true)
	envelope.SudoPassword, err = clusterSSHString(fields, "sudo_password", clusterremote.MaxPasswordBytes, true)
	if err != nil || clusterremote.ValidateSudoPassword([]byte(envelope.SudoPassword)) != nil {
		return fail()
	}
	return id, digest, clusterSSHSubmissionTarget{target: target, key: key, envelope: envelope}, nil
}

func (s *clusterExistingIdentityAPI) confirm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	actor, failure := requireAdmin(r.Context(), s.auth, r)
	if failure != nil {
		failure.write(w)
		return
	}
	bad := func() {
		writePublicValidationErrors(w, []publicValidationError{{Field: "body", Message: "exact plan confirmation and approved SSH credential fields required"}})
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if !clusterUUIDRE.MatchString(r.PathValue("id")) || !clusterUUIDRE.MatchString(r.PathValue("operation")) || r.URL.RawQuery != "" || r.URL.ForceQuery ||
		err != nil || media != "application/json" || r.Header.Get("Content-Encoding") != "" {
		bad()
		return
	}
	raw, err := readLimitedRequestBody(r, clusterSSHQueueBodyMaxBytes)
	if err != nil {
		bad()
		return
	}
	defer clear(raw)
	request, digest, submitted, err := parseClusterExistingIdentityConfirmationInput(raw)
	if err != nil {
		bad()
		return
	}
	defer func() { submitted.envelope = clusterSSHCredentialEnvelope{} }()
	backend, ok := s.store.(clusterExistingIdentityConfirmationStore)
	if !ok || s.sealer == nil || s.slots == nil {
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
	readCtx, readCancel := context.WithTimeout(ctx, 5*time.Second)
	plan, state, err := s.store.clusterExistingIdentityPlan(readCtx, r.PathValue("id"), r.PathValue("operation"))
	readCancel()
	if err != nil {
		writeClusterSSHOnboardingError(w, err)
		return
	}
	expected, digestErr := clusterSSHPreparationDigest(plan)
	address := ""
	for _, m := range plan.Members {
		if m.NodeID == plan.TargetID {
			address = m.Address
		}
	}
	if !plan.valid() || state != "waiting" || digestErr != nil || expected != digest || plan.TargetID != r.PathValue("id") || plan.OperationID != r.PathValue("operation") ||
		request == plan.OperationID || request == plan.SourceID || request == plan.TargetID || address != submitted.target.Address {
		writeClusterSSHOnboardingError(w, errClusterConflict)
		return
	}
	// Source/plan SQL returned its connection before Aegis handles plaintext.
	submitted.envelope.Binding = clusterSSHCredentialBinding{ClusterID: plan.ClusterID, OperationID: plan.OperationID, TargetID: request, Address: address, Port: submitted.target.Port, Fingerprint: submitted.key.Fingerprint}
	sealed, err := s.sealer.sealClusterSSHCredentials(ctx, submitted.envelope)
	if err != nil {
		writeClusterSSHOnboardingError(w, errClusterSSHCredentials)
		return
	}
	target := clusterSSHPlannedTarget{Binding: submitted.envelope.Binding, Key: submitted.key, KeyBase64: base64.StdEncoding.EncodeToString(submitted.key.PublicKey), Sealed: sealed}
	receipt, err := backend.confirmClusterExistingIdentity(ctx, actor.Username, plan, digest, target)
	if err != nil {
		writeClusterSSHOnboardingError(w, err)
		return
	}
	if !receipt.valid(plan) || receipt.RequestID != request || receipt.Binding != target.Binding || receipt.Actor != actor.Username {
		writeClusterSSHOnboardingError(w, errClusterConflict)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"operation_id": plan.OperationID, "request_id": request, "plan_sha256": digest, "confirmed_at": receipt.ConfirmedAt,
		"state": "awaiting_identity_observation", "recovery_authorized": false})
}
