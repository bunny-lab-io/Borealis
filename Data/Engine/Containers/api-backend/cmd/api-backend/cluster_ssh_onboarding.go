package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"time"
	"unicode/utf8"

	"borealis/api-backend/internal/clusterremote"
)

const clusterSSHQueueBodyMaxBytes = 2 * clusterSSHBodyMaxBytes

type clusterSSHOnboardingStore interface {
	sshInspectionSource(context.Context) (clusterSSHQueueSource, error)
	queueClusterSSHInspections(context.Context, string, clusterSSHQueueSource, []clusterSSHPlannedTarget) (map[string]any, error)
	clusterSSHInspectionProgress(context.Context, string) (clusterSSHInspectionProgress, error)
}

func (s *postgresOperatorStore) sshInspectionSource(ctx context.Context) (clusterSSHQueueSource, error) {
	return readClusterSSHQueueSource(ctx, s.db)
}

type clusterSSHCredentialSealer interface {
	sealClusterSSHCredentials(context.Context, clusterSSHCredentialEnvelope) (sealedClusterSSHCredentials, error)
}

type clusterSSHOnboarding struct {
	auth   *authService
	store  clusterSSHOnboardingStore
	sealer clusterSSHCredentialSealer
	slots  chan struct{}
}

func registerClusterSSHOnboardingRoutes(mux *http.ServeMux, auth *authService) {
	service := &clusterSSHOnboarding{auth: auth, slots: make(chan struct{}, 2)}
	if auth != nil {
		service.store, _ = auth.store.(clusterSSHOnboardingStore)
		service.sealer, _ = auth.aegis.(clusterSSHCredentialSealer)
	}
	mux.HandleFunc("POST /api/server/cluster/onboarding/operations", service.start)
	mux.HandleFunc("GET /api/server/cluster/onboarding/operations/{id}", service.progress)
}

type clusterSSHSubmissionTarget struct {
	target   clusterremote.Target
	key      clusterremote.HostKey
	envelope clusterSSHCredentialEnvelope
}

func clearClusterSSHFields(body map[string]json.RawMessage) {
	for _, value := range body {
		clear(value)
	}
}

func readClusterSSHSubmission(r *http.Request) (string, []clusterSSHSubmissionTarget, error) {
	fail := func() (string, []clusterSSHSubmissionTarget, error) {
		return "", nil, errors.New("invalid SSH inspection request")
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || r.URL.RawQuery != "" {
		return fail()
	}
	raw, err := readLimitedRequestBody(r, clusterSSHQueueBodyMaxBytes)
	if err != nil || !utf8.Valid(raw) {
		return fail()
	}
	defer clear(raw)
	body, err := decodeClusterSSHObject(raw, map[string]bool{"request_id": true, "targets": true, "confirmation": true})
	if err != nil {
		return fail()
	}
	defer clearClusterSSHFields(body)
	id, err := clusterSSHString(body, "request_id", 36, false)
	if err != nil || !clusterUUIDRE.MatchString(id) {
		return fail()
	}
	confirmation, err := clusterSSHString(body, "confirmation", 32, false)
	if err != nil || confirmation != "INSPECT ENGINE NODES" {
		return fail()
	}
	var encoded []json.RawMessage
	if json.Unmarshal(body["targets"], &encoded) != nil || len(encoded) < 1 || len(encoded) > 2 {
		return fail()
	}
	defer func() {
		for _, value := range encoded {
			clear(value)
		}
	}()
	allowed := map[string]bool{}
	for _, name := range []string{"address", "port", "host_key_algorithm", "host_key_fingerprint", "host_key_base64", "host_key_approved", "username", "auth_method", "password", "private_key", "passphrase", "sudo_password"} {
		allowed[name] = true
	}
	var targets []clusterSSHSubmissionTarget
	addresses, fingerprints := map[string]bool{}, map[string]bool{}
	for _, raw := range encoded {
		if len(raw) > clusterSSHBodyMaxBytes {
			return fail()
		}
		fields, err := decodeClusterSSHObject(raw, allowed)
		if err != nil {
			return fail()
		}
		defer clearClusterSSHFields(fields)
		target, err := clusterSSHTarget(fields)
		if err != nil || addresses[target.Address] {
			return fail()
		}
		key, err := clusterSSHApproval(fields)
		if err != nil || fingerprints[key.Fingerprint] {
			return fail()
		}
		credential, err := clusterSSHCredential(fields)
		if err != nil {
			return fail()
		}
		credential.Destroy()
		envelope := clusterSSHCredentialEnvelope{Version: 1}
		// Reuse exact validated field bytes; no display-text sanitization of secrets.
		envelope.Username, _ = clusterSSHString(fields, "username", 64, false)
		envelope.Method, _ = clusterSSHString(fields, "auth_method", 11, false)
		envelope.Password, _ = clusterSSHString(fields, "password", clusterremote.MaxPasswordBytes, true)
		envelope.PrivateKey, _ = clusterSSHString(fields, "private_key", clusterremote.MaxPrivateKeyBytes, true)
		envelope.Passphrase, _ = clusterSSHString(fields, "passphrase", clusterremote.MaxPasswordBytes, true)
		envelope.SudoPassword, err = clusterSSHString(fields, "sudo_password", clusterremote.MaxPasswordBytes, true)
		if err != nil || clusterremote.ValidateSudoPassword([]byte(envelope.SudoPassword)) != nil {
			return fail()
		}
		addresses[target.Address], fingerprints[key.Fingerprint] = true, true
		targets = append(targets, clusterSSHSubmissionTarget{target: target, key: key, envelope: envelope})
	}
	return id, targets, nil
}

func (service *clusterSSHOnboarding) start(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	identity, failure := requireAdmin(r.Context(), service.auth, r)
	if failure != nil {
		failure.write(w)
		return
	}
	id, submitted, err := readClusterSSHSubmission(r)
	if err != nil {
		writePublicValidationErrors(w, []publicValidationError{{Field: "body", Message: err.Error()}})
		return
	}
	defer func() {
		for i := range submitted {
			submitted[i].envelope = clusterSSHCredentialEnvelope{}
		}
	}()
	if service.store == nil || service.sealer == nil {
		writeClusterSSHOnboardingError(w, errClusterUnavailable)
		return
	}
	select {
	case service.slots <- struct{}{}:
		defer func() { <-service.slots }()
	default:
		writeClusterSSHOnboardingError(w, errClusterUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	source, err := service.store.sshInspectionSource(ctx)
	if err != nil {
		writeClusterSSHOnboardingError(w, err)
		return
	}
	expected, err := source.validate()
	if err != nil || expected != len(submitted) {
		writeClusterSSHOnboardingError(w, errClusterConflict)
		return
	}
	// All request validation and the source read finish before encryption. The
	// store repeats source/Aegis authority under its queue transaction afterwards.
	var targets []clusterSSHPlannedTarget
	for _, target := range submitted {
		envelope := target.envelope
		envelope.Binding = clusterSSHCredentialBinding{ClusterID: source.ClusterID, OperationID: id, TargetID: newClusterUUID(), Address: target.target.Address, Port: target.target.Port, Fingerprint: target.key.Fingerprint}
		sealed, err := service.sealer.sealClusterSSHCredentials(ctx, envelope)
		if err != nil {
			writeClusterSSHOnboardingError(w, errClusterSSHCredentials)
			return
		}
		targets = append(targets, clusterSSHPlannedTarget{Binding: envelope.Binding, Key: target.key, KeyBase64: base64.StdEncoding.EncodeToString(target.key.PublicKey), Sealed: sealed})
	}
	result, err := service.store.queueClusterSSHInspections(ctx, identity.Username, source, targets)
	if err != nil {
		writeClusterSSHOnboardingError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (service *clusterSSHOnboarding) progress(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if _, failure := requireAdmin(r.Context(), service.auth, r); failure != nil {
		failure.write(w)
		return
	}
	id := r.PathValue("id")
	if !clusterUUIDRE.MatchString(id) || r.URL.RawQuery != "" || r.ContentLength != 0 {
		writePublicValidationErrors(w, []publicValidationError{{Field: "id", Message: "canonical operation UUID and no query/body required"}})
		return
	}
	if service.store == nil {
		writeClusterSSHOnboardingError(w, errClusterUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	result, err := service.store.clusterSSHInspectionProgress(ctx, id)
	if err != nil {
		writeClusterSSHOnboardingError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"operation_id": result.OperationID, "state": result.State,
		"current_step": result.Step, "attempt": result.Attempt, "targets": result.Targets})
}

func writeClusterSSHOnboardingError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, "ssh_onboarding_unavailable"
	switch {
	case errors.Is(err, errClusterNotFound):
		status, code = http.StatusNotFound, "ssh_onboarding_not_found"
	case errors.Is(err, errClusterConflict):
		status, code = http.StatusConflict, "ssh_onboarding_conflict"
	case errors.Is(err, errClusterSSHCredentials):
		status, code = http.StatusConflict, "ssh_credentials_unavailable"
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		status, code = http.StatusGatewayTimeout, "ssh_onboarding_timeout"
	}
	// Store, crypto and transport diagnostics never enter public error messages.
	writeJSON(w, status, map[string]any{"error": code})
}
