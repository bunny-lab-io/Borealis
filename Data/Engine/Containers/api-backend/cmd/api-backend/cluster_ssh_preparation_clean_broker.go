package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"crypto/cipher"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	clusterSSHPreparationCleanBrokerPath    = "/internal/cluster/ssh-preparation-clean-targets"
	clusterSSHPreparationCleanBrokerScope   = "ssh-preparation-clean-targets"
	clusterSSHPreparationCleanBrokerLimit   = 8 << 10
	clusterSSHPreparationCleanBrokerVersion = 3
)

// Private read-only contract. The source broker runs in the keyless controller
// and requires a claimed target; it cannot execute an original waiting proposal.
// This fixed API route uses the existing operator-secret AEAD helpers, distinct
// direction/scope keys and path AAD. Traefik forwards /api and /socket.io only.
// Authenticated callers supply no target, command, URL, key or credential. SQL
// must still prove the exact original owner, waiting plan and explicit intent.
// Replay starts fresh native reads within the same expiry; it grants no claim.
// Version 3 carries typed native effective-CPU evidence with the clean-host
// observation. Older brokers cannot relabel digest-only receipts as CPU proof.
type clusterSSHPreparationCleanBrokerRequest struct {
	Version          int      `json:"version"`
	ID               string   `json:"id"`
	ExpiresAt        int64    `json:"expires_at"`
	OperationID      string   `json:"operation_id"`
	Attempt          int64    `json:"attempt"`
	ControllerHolder string   `json:"controller_holder"`
	PlanSHA256       string   `json:"plan_sha256"`
	ProposalSHA256   string   `json:"proposal_sha256"`
	FilesystemPaths  []string `json:"filesystem_paths,omitempty"`
}

func (clusterSSHPreparationCleanBrokerRequest) String() string {
	return "clean-target request [redacted]"
}
func (clusterSSHPreparationCleanBrokerRequest) GoString() string {
	return "clean-target request [redacted]"
}

func (r clusterSSHPreparationCleanBrokerRequest) valid(now time.Time) bool {
	return r.Version == clusterSSHPreparationCleanBrokerVersion && clusterUUIDRE.MatchString(r.ID) && r.ID != "00000000-0000-0000-0000-000000000000" &&
		clusterUUIDRE.MatchString(r.OperationID) && r.OperationID != "00000000-0000-0000-0000-000000000000" && r.Attempt > 0 &&
		r.ControllerHolder != "" && len(r.ControllerHolder) <= 1024 && utf8.ValidString(r.ControllerHolder) &&
		strings.IndexFunc(r.ControllerHolder, unicode.IsControl) < 0 && r.ExpiresAt > now.Unix() && r.ExpiresAt <= now.Unix()+30 &&
		validClusterSSHPreparationDigest(r.PlanSHA256) && validClusterSSHPreparationDigest(r.ProposalSHA256) && validClusterSSHPreparationFilesystemPaths(r.FilesystemPaths)
}

func clusterSSHPreparationCleanBrokerBinding(snapshot clusterSSHPreparationApprovalSnapshot) (string, string, error) {
	w := snapshot.work
	plan, err := clusterSSHPreparationPlanDigest(w.Authorization)
	var intent json.RawMessage
	if err != nil || len(w.Claims) != 0 || clusterBootstrapObject([]byte(w.Payload), map[string]any{"ssh_preparation_request": &intent}) != nil {
		return "", "", clusterbootstrap.ErrPreparationConfig
	}
	if _, err := parseClusterSSHPreparationRequest(intent, w.Authorization, w.Proof.Source); err != nil {
		return "", "", clusterbootstrap.ErrPreparationConfig
	}
	proposal, err := clusterSSHPreparationDigest(w)
	return plan, proposal, err
}

type clusterSSHPreparationCleanBrokerResponse struct {
	Version       int                                  `json:"version"`
	ID            string                               `json:"id"`
	RequestSHA256 string                               `json:"request_sha256"`
	Digest        string                               `json:"digest"`
	Targets       []clusterSSHPreparationTargetCPU     `json:"targets"`
	Filesystems   []clusterSSHTargetFilesystemEvidence `json:"filesystems,omitempty"`
}

type clusterSSHPreparationCleanBroker struct {
	store                         *postgresOperatorStore
	aegis                         clusterSSHWorkerAegis
	connect                       clusterSSHPreparationProposalConnect
	requestCipher, responseCipher cipher.AEAD
	slots                         chan struct{}
}

func newClusterSSHPreparationCleanBroker(store *postgresOperatorStore, aegis clusterSSHWorkerAegis, secret string) *clusterSSHPreparationCleanBroker {
	b := &clusterSSHPreparationCleanBroker{store: store, aegis: aegis, connect: connectClusterSSHPreparationProposal, slots: make(chan struct{}, 2)}
	b.requestCipher, _ = clusterExistingIdentityScopedCipher(secret, "request", clusterSSHPreparationCleanBrokerScope)
	b.responseCipher, _ = clusterExistingIdentityScopedCipher(secret, "response", clusterSSHPreparationCleanBrokerScope)
	return b
}

func registerClusterSSHPreparationCleanBrokerRoute(mux *http.ServeMux, auth *authService) *clusterSSHPreparationCleanBroker {
	var store *postgresOperatorStore
	var aegis clusterSSHWorkerAegis
	if auth != nil {
		store, _ = auth.store.(*postgresOperatorStore)
		aegis, _ = auth.aegis.(clusterSSHWorkerAegis)
	}
	b := newClusterSSHPreparationCleanBroker(store, aegis, strings.TrimSpace(os.Getenv("BOREALIS_OPERATOR_SECRET")))
	mux.HandleFunc("POST "+clusterSSHPreparationCleanBrokerPath, b.handle)
	return b
}

func (b *clusterSSHPreparationCleanBroker) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	fail := func(code int) { http.Error(w, "clean-target observation unavailable", code) }
	if b == nil || r.Method != http.MethodPost || r.URL.Path != clusterSSHPreparationCleanBrokerPath || r.URL.RawQuery != "" || r.URL.ForceQuery ||
		r.Header.Get("Content-Type") != clusterSSHSourceBrokerMedia || r.Header.Get("Content-Encoding") != "" {
		fail(http.StatusBadRequest)
		return
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second))
	wire, err := io.ReadAll(io.LimitReader(r.Body, clusterSSHPreparationCleanBrokerLimit+1))
	closeErr := r.Body.Close()
	var request clusterSSHPreparationCleanBrokerRequest
	if err != nil || closeErr != nil || r.Context().Err() != nil || openClusterSSHVIP(b.requestCipher, clusterSSHPreparationCleanBrokerPath, wire, &request, clusterSSHPreparationCleanBrokerLimit) != nil || !request.valid(time.Now()) {
		fail(http.StatusUnauthorized)
		return
	}
	select {
	case b.slots <- struct{}{}:
		defer func() { <-b.slots }()
	default:
		fail(http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithDeadline(r.Context(), time.Unix(request.ExpiresAt, 0))
	defer cancel()
	operation := clusterControllerOperation{ID: request.OperationID, Attempt: request.Attempt, Kind: "ssh_onboarding", State: "waiting", CurrentStep: clusterSSHQualificationStep}
	snapshot, _, err := b.store.readClusterSSHPreparationProposal(ctx, operation, request.ControllerHolder)
	plan, proposal, bindErr := clusterSSHPreparationCleanBrokerBinding(snapshot)
	if err != nil || bindErr != nil || plan != request.PlanSHA256 || proposal != request.ProposalSHA256 || ctx.Err() != nil {
		fail(http.StatusServiceUnavailable)
		return
	}
	check := b.store.preparationCleanTargetsProposalCheck(snapshot, request.ControllerHolder)
	read := newClusterSSHPreparationTargetsFilesystemRead(snapshot, check, b.aegis, b.connect, request.FilesystemPaths)
	observed, err := read(ctx, plan)
	if err != nil || observed.validate(snapshot) != nil || !validClusterSSHPreparationFilesystems(snapshot, request.FilesystemPaths, observed.Filesystems) || check(ctx) != nil || ctx.Err() != nil {
		fail(http.StatusServiceUnavailable)
		return
	}
	requestDigest, err := clusterSSHPreparationDigest(request)
	response := clusterSSHPreparationCleanBrokerResponse{Version: clusterSSHPreparationCleanBrokerVersion, ID: request.ID, RequestSHA256: requestDigest, Digest: observed.Digest, Targets: observed.Targets, Filesystems: observed.Filesystems}
	wire, sealErr := sealClusterSSHVIP(b.responseCipher, clusterSSHPreparationCleanBrokerPath, response, clusterSSHPreparationCleanBrokerLimit)
	if err != nil || sealErr != nil || ctx.Err() != nil {
		fail(http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", clusterSSHSourceBrokerMedia)
	_, _ = w.Write(wire)
}
