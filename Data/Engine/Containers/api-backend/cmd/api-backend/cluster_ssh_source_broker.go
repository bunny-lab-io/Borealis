package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	clusterSSHSourceBrokerPath  = "/internal/cluster/ssh-preparation-source"
	clusterSSHSourceBrokerHost  = "borealis-cluster-source.borealis.svc"
	clusterSSHSourceBrokerLimit = 384 << 10
	clusterSSHSourceBrokerMedia = "application/octet-stream"
)

// These wire types are used only inside authenticated encryption. Neither the
// sealed credential nor source settings belong in generic JSON responses/logs.
type clusterSSHSourceBrokerRequest struct {
	Version    int                         `json:"version"`
	ID         string                      `json:"id"`
	ExpiresAt  int64                       `json:"expires_at"`
	Lease      clusterSSHTargetLease       `json:"lease"`
	Baseline   clusterbootstrap.Expected   `json:"baseline"`
	Binding    clusterSSHCredentialBinding `json:"binding"`
	Generation string                      `json:"aegis_generation"`
	Ciphertext string                      `json:"credential_ciphertext"`
}

func (clusterSSHSourceBrokerRequest) String() string   { return "source broker request [redacted]" }
func (clusterSSHSourceBrokerRequest) GoString() string { return "source broker request [redacted]" }

func (r clusterSSHSourceBrokerRequest) sealed() sealedClusterSSHCredentials {
	return sealedClusterSSHCredentials{binding: r.Binding, generation: r.Generation, ciphertext: r.Ciphertext}
}

func (r clusterSSHSourceBrokerRequest) valid(now time.Time) bool {
	return r.Version == 1 && clusterUUIDRE.MatchString(r.ID) && r.ID != "00000000-0000-0000-0000-000000000000" && r.ExpiresAt > now.Unix() && r.ExpiresAt <= now.Unix()+40 &&
		validClusterSSHPreparationLease(r.Lease) && r.Baseline.Validate() == nil && r.Baseline.Repository == clusterGitHubRepo() &&
		r.Binding.valid() && r.Binding.OperationID == r.Lease.OperationID && r.Binding.TargetID == r.Lease.TargetID &&
		r.Generation != "" && len(r.Generation) <= 16<<10 && strings.HasPrefix(r.Ciphertext, aegisEnvelopePrefix) && len(r.Ciphertext) <= 256<<10
}

type clusterSSHSourceBrokerResponse struct {
	Version  int                            `json:"version"`
	ID       string                         `json:"id"`
	Status   string                         `json:"status"`
	Snapshot *clusterSSHPreparationSnapshot `json:"snapshot"`
}

func (clusterSSHSourceBrokerResponse) String() string   { return "source broker response [redacted]" }
func (clusterSSHSourceBrokerResponse) GoString() string { return "source broker response [redacted]" }

// Separate HMAC contexts derive independent AES-256 keys from the existing
// high-entropy operator secret. Go supplies random GCM nonces. Fixed path is
// authenticated associated data; request UUID binds each response to one call.
func clusterSSHSourceBrokerCipher(secret, direction string) (cipher.AEAD, error) {
	if len(secret) < 32 || len(secret) > 4096 || (direction != "request" && direction != "response") {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("borealis/cluster/ssh-preparation-source/v1/" + direction))
	key := mac.Sum(nil)
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	return cipher.NewGCMWithRandomNonce(block)
}

func sealClusterSSHSourceBroker(aead cipher.AEAD, value any) ([]byte, error) {
	if aead == nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	raw, err := json.Marshal(value)
	defer clear(raw)
	if err != nil || len(raw)+aead.Overhead() > clusterSSHSourceBrokerLimit {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	return aead.Seal(nil, nil, raw, []byte(clusterSSHSourceBrokerPath)), nil
}

func openClusterSSHSourceBroker(aead cipher.AEAD, wire []byte, out any) error {
	if aead == nil || len(wire) > clusterSSHSourceBrokerLimit {
		return clusterbootstrap.ErrPreparationConfig
	}
	raw, err := aead.Open(nil, nil, wire, []byte(clusterSSHSourceBrokerPath))
	defer clear(raw)
	if err != nil || !utf8.Valid(raw) || json.Unmarshal(raw, out) != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	canonical, err := json.Marshal(out)
	defer clear(canonical)
	if err != nil || !sameClusterSSHStoredJSON(raw, canonical, 0) {
		return clusterbootstrap.ErrPreparationConfig
	}
	return nil
}

type clusterSSHSourceBroker struct {
	holder                        string
	requestCipher, responseCipher cipher.AEAD
	read                          func(context.Context, clusterSSHSourceBrokerRequest) (clusterSSHPreparationSnapshot, error)
	mu                            sync.Mutex
	seen                          map[string]int64
	active                        int
}

func newClusterSSHSourceBroker(store *postgresOperatorStore, runner *kubernetesClusterStepRunner, holder, secret string) *clusterSSHSourceBroker {
	b := &clusterSSHSourceBroker{holder: holder, seen: make(map[string]int64)}
	b.requestCipher, _ = clusterSSHSourceBrokerCipher(secret, "request")
	b.responseCipher, _ = clusterSSHSourceBrokerCipher(secret, "response")
	b.read = func(ctx context.Context, request clusterSSHSourceBrokerRequest) (clusterSSHPreparationSnapshot, error) {
		if !clusterControllerEligible() || runner == nil || runner.kube == nil || runner.controllerHolder != holder || request.Lease.ControllerHolder != holder {
			return clusterSSHPreparationSnapshot{}, clusterbootstrap.ErrPreparationConfig
		}
		// The controller checks persisted Aegis generation/ciphertext only. Actual
		// unlocked-key checks remain on the worker on both sides of this transfer.
		authority := func(ctx context.Context) (clusterSSHPreparationAuthority, error) {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			return store.loadClusterSSHPreparationAuthority(ctx, request.Lease, request.Baseline, request.sealed())
		}
		return newClusterSSHPreparationSnapshotRead(authority, runner.kube.getClusterSSHPreparationJSON, runner.newSSHSourceNetworkRead(authority))(ctx)
	}
	return b
}

func (b *clusterSSHSourceBroker) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	fail := func(status int) { http.Error(w, "source unavailable", status) }
	if r.Method != http.MethodPost || r.URL.Path != clusterSSHSourceBrokerPath || r.URL.RawQuery != "" || r.URL.ForceQuery ||
		r.Header.Get("Content-Type") != clusterSSHSourceBrokerMedia || r.Header.Get("Content-Encoding") != "" {
		fail(http.StatusBadRequest)
		return
	}
	// Bound unauthenticated upload time before any body read, including callers
	// with no Content-Length. The server never logs body or decoder diagnostics.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second))
	wire, err := io.ReadAll(io.LimitReader(r.Body, clusterSSHSourceBrokerLimit+1))
	var request clusterSSHSourceBrokerRequest
	if err != nil || b == nil || openClusterSSHSourceBroker(b.requestCipher, wire, &request) != nil || !request.valid(time.Now()) {
		fail(http.StatusUnauthorized)
		return
	}
	reply := func(status string, snapshot *clusterSSHPreparationSnapshot) {
		wire, err := sealClusterSSHSourceBroker(b.responseCipher, clusterSSHSourceBrokerResponse{Version: 1, ID: request.ID, Status: status, Snapshot: snapshot})
		if err != nil {
			fail(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", clusterSSHSourceBrokerMedia)
		_, _ = w.Write(wire)
	}
	if request.Lease.ControllerHolder != b.holder {
		reply("not_owner", nil)
		return
	}
	// Replay memory is bounded and fails closed when full. Replays are never
	// a reason to renew authority or adopt an earlier source-action Job.
	b.mu.Lock()
	now := time.Now().Unix()
	for id, expires := range b.seen {
		if expires <= now {
			delete(b.seen, id)
		}
	}
	_, replay := b.seen[request.ID]
	accepted := !replay && len(b.seen) < 128 && b.active < 2 && b.read != nil
	if accepted {
		b.seen[request.ID] = request.ExpiresAt
		b.active++
	}
	b.mu.Unlock()
	if !accepted {
		reply("unavailable", nil)
		return
	}
	defer func() { b.mu.Lock(); b.active--; b.mu.Unlock() }()
	ctx, cancel := context.WithDeadline(r.Context(), time.Unix(request.ExpiresAt, 0))
	defer cancel()
	ctx, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	value, err := b.read(ctx, request)
	if err != nil || ctx.Err() != nil {
		reply("unavailable", nil)
		return
	}
	reply("ok", &value)
}
