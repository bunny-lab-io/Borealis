package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"crypto/cipher"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const clusterSSHPrejoinCustodyPath = "/internal/cluster/ssh-prejoin-custody"

// Encrypted credential authority is carried only inside the bounded envelope.
// This read-only protocol proves a caller-selected original source. It does
// not select one durably or authorize dispatch/identity mutation by itself.
type clusterSSHPrejoinCustodyRequest struct {
	Request clusterSSHSourceBrokerRequest
	Source  clusterSSHPrejoinSource
}

func (clusterSSHPrejoinCustodyRequest) String() string   { return "prejoin custody request [redacted]" }
func (clusterSSHPrejoinCustodyRequest) GoString() string { return "prejoin custody request [redacted]" }

func (r clusterSSHPrejoinCustodyRequest) valid(now time.Time) bool {
	for _, id := range []string{r.Request.Lease.OperationID, r.Request.Lease.TargetID, r.Request.Lease.Holder} {
		if id == "00000000-0000-0000-0000-000000000000" {
			return false
		}
	}
	return r.Request.valid(now) && r.Request.ExpiresAt <= now.Unix()+25 && validClusterSSHPreparationLease(r.Request.Lease) &&
		r.Request.Lease.Generation < 1<<53 && r.Request.Lease.OperationAttempt < 1<<53 &&
		r.Source.validIdentity() && r.Source.Binding.ClusterID == r.Request.Binding.ClusterID && r.Source.Binding.SourceID != r.Request.Lease.TargetID
}

type clusterSSHPrejoinCustodyResponse struct {
	Version    int
	ID, Status string
	Proof      *clusterSSHPrejoinSourceCustody
}

type clusterSSHPrejoinCustodyBroker struct {
	holder                        string
	requestCipher, responseCipher cipher.AEAD
	read                          func(context.Context, clusterSSHPrejoinCustodyRequest) (clusterSSHPrejoinSourceCustody, error)
	mu                            sync.Mutex
	seen                          map[string]int64
	active                        int
}

func newClusterSSHPrejoinCustodyBroker(store *postgresOperatorStore, runner *kubernetesClusterStepRunner, holder, secret string) *clusterSSHPrejoinCustodyBroker {
	b := &clusterSSHPrejoinCustodyBroker{holder: holder, seen: map[string]int64{}}
	b.requestCipher, _ = clusterExistingIdentityScopedCipher(secret, "request", "ssh-prejoin-custody")
	b.responseCipher, _ = clusterExistingIdentityScopedCipher(secret, "response", "ssh-prejoin-custody")
	b.read = func(ctx context.Context, r clusterSSHPrejoinCustodyRequest) (clusterSSHPrejoinSourceCustody, error) {
		if !clusterControllerEligible() || store == nil || store.db == nil || runner == nil || runner.kube == nil || runner.controllerHolder != holder ||
			r.Request.Lease.ControllerHolder != holder || !r.valid(time.Now()) {
			return clusterSSHPrejoinSourceCustody{}, clusterbootstrap.ErrSessionAuthority
		}
		read := func(ctx context.Context) (clusterSSHPreparationAuthority, error) {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			// Existing SQL-only reader checks exact persisted credential/lease,
			// then releases its connection before native Pod/Node observation.
			return store.loadClusterSSHPreparationAuthority(ctx, r.Request.Lease, r.Request.Baseline, r.Request.sealed())
		}
		return readClusterSSHPrejoinSourceCustody(ctx, r.Source, read, runner.kube.getJSON)
	}
	return b
}

func (b *clusterSSHPrejoinCustodyBroker) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	fail := func(code int) { http.Error(w, "prejoin custody unavailable", code) }
	if r.Method != http.MethodPost || r.URL.Path != clusterSSHPrejoinCustodyPath || r.URL.RawQuery != "" || r.URL.ForceQuery ||
		r.Header.Get("Content-Type") != clusterSSHSourceBrokerMedia || r.Header.Get("Content-Encoding") != "" {
		fail(400)
		return
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second))
	wire, err := io.ReadAll(io.LimitReader(r.Body, clusterSSHSourceBrokerLimit+1))
	defer clear(wire)
	var request clusterSSHPrejoinCustodyRequest
	// Reuse canonical bounded AEAD codec, with independent derivation, path
	// AAD and request/response keys. No VIP or existing-identity message works.
	if err != nil || b == nil || openClusterSSHVIP(b.requestCipher, clusterSSHPrejoinCustodyPath, wire, &request, clusterSSHSourceBrokerLimit) != nil || !request.valid(time.Now()) {
		fail(401)
		return
	}
	reply := func(status string, proof *clusterSSHPrejoinSourceCustody) {
		wire, err := sealClusterSSHVIP(b.responseCipher, clusterSSHPrejoinCustodyPath,
			clusterSSHPrejoinCustodyResponse{Version: 1, ID: request.Request.ID, Status: status, Proof: proof}, clusterSSHSourceBrokerLimit)
		if err != nil {
			fail(503)
			return
		}
		w.Header().Set("Content-Type", clusterSSHSourceBrokerMedia)
		_, _ = w.Write(wire)
	}
	if request.Request.Lease.ControllerHolder != b.holder {
		reply("not_owner", nil)
		return
	}
	b.mu.Lock()
	for id, expiry := range b.seen {
		if expiry <= time.Now().Unix() {
			delete(b.seen, id)
		}
	}
	_, replay := b.seen[request.Request.ID]
	accepted := b.seen != nil && !replay && len(b.seen) < 128 && b.active < 2 && b.read != nil
	if accepted {
		b.seen[request.Request.ID] = request.Request.ExpiresAt
		b.active++
	}
	b.mu.Unlock()
	if !accepted {
		reply("unavailable", nil)
		return
	}
	defer func() { b.mu.Lock(); b.active--; b.mu.Unlock() }()
	ctx, cancel := context.WithDeadline(r.Context(), time.Unix(request.Request.ExpiresAt, 0))
	defer cancel()
	ctx, stop := context.WithTimeout(ctx, 15*time.Second)
	defer stop()
	if ctx.Err() != nil {
		reply("unavailable", nil)
		return
	}
	proof, err := b.read(ctx, request)
	if err != nil || ctx.Err() != nil || !proof.valid(request.Source) {
		reply("unavailable", nil)
		return
	}
	reply("observed", &proof)
}

type clusterSSHPrejoinCustodyClient struct{ clusterSSHSourceBrokerClient }

func newClusterSSHPrejoinCustodyClient() (*clusterSSHPrejoinCustodyClient, error) {
	transport, err := newClusterSSHSourceBrokerClientFromEnv()
	if err != nil {
		return nil, clusterbootstrap.ErrSessionAuthority
	}
	secret := strings.TrimSpace(os.Getenv("BOREALIS_OPERATOR_SECRET"))
	transport.requestCipher, err = clusterExistingIdentityScopedCipher(secret, "request", "ssh-prejoin-custody")
	if err != nil {
		return nil, clusterbootstrap.ErrSessionAuthority
	}
	transport.responseCipher, err = clusterExistingIdentityScopedCipher(secret, "response", "ssh-prejoin-custody")
	if err != nil {
		return nil, clusterbootstrap.ErrSessionAuthority
	}
	return &clusterSSHPrejoinCustodyClient{*transport}, nil
}

// The supplied authority reader must include the worker's current unlocked
// Aegis check. Broker authority alone cannot keep a locked worker alive.
func (c *clusterSSHPrejoinCustodyClient) observe(parent context.Context, source clusterSSHPrejoinSource, lease clusterSSHTargetLease,
	baseline clusterbootstrap.Expected, sealed sealedClusterSSHCredentials, authority clusterSSHPreparationAuthorityRead) (clusterSSHPrejoinSourceCustody, error) {
	fail := func() (clusterSSHPrejoinSourceCustody, error) {
		return clusterSSHPrejoinSourceCustody{}, clusterbootstrap.ErrSessionAuthority
	}
	if c == nil || c.httpClient == nil || c.peers == nil || authority == nil || parent.Err() != nil {
		return fail()
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	before, err := authority(ctx)
	if err != nil || !source.valid(before) || before.Lease != lease || before.Baseline != baseline {
		return fail()
	}
	before.Cohort.ObservedAt = 0
	digest, err := clusterSSHPreparationDigest(before)
	if err != nil {
		return fail()
	}
	request := clusterSSHPrejoinCustodyRequest{Source: source, Request: clusterSSHSourceBrokerRequest{Version: 1, ID: newClusterUUID(), ExpiresAt: time.Now().Unix() + 20,
		Lease: lease, Baseline: baseline, Binding: sealed.binding, Generation: sealed.generation, Ciphertext: sealed.ciphertext}}
	if !request.valid(time.Now()) {
		return fail()
	}
	peers, err := c.peers(ctx)
	if err != nil || len(peers) == 0 || len(peers) > 3 {
		return fail()
	}
	wire, err := sealClusterSSHVIP(c.requestCipher, clusterSSHPrejoinCustodyPath, request, clusterSSHSourceBrokerLimit)
	if err != nil {
		return fail()
	}
	defer clear(wire)
	client := *c.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for _, peer := range peers {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, peer+clusterSSHPrejoinCustodyPath, bytes.NewReader(wire))
		if err != nil {
			return fail()
		}
		req.Header.Set("Content-Type", clusterSSHSourceBrokerMedia)
		req.GetBody = nil
		res, err := client.Do(req)
		if err != nil {
			return fail()
		}
		raw, readErr := io.ReadAll(io.LimitReader(res.Body, clusterSSHSourceBrokerLimit+1))
		closeErr := res.Body.Close()
		var response clusterSSHPrejoinCustodyResponse
		if readErr != nil || closeErr != nil || res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != clusterSSHSourceBrokerMedia || res.Header.Get("Content-Encoding") != "" ||
			openClusterSSHVIP(c.responseCipher, clusterSSHPrejoinCustodyPath, raw, &response, clusterSSHSourceBrokerLimit) != nil || response.Version != 1 || response.ID != request.Request.ID || ctx.Err() != nil {
			return fail()
		}
		if response.Status == "not_owner" && response.Proof == nil {
			continue
		}
		if response.Status != "observed" || response.Proof == nil || !response.Proof.valid(source) || response.Proof.AuthoritySHA256 != digest {
			return fail()
		}
		after, err := authority(ctx)
		if err != nil || !source.valid(after) || ctx.Err() != nil {
			return fail()
		}
		after.Cohort.ObservedAt = 0
		afterDigest, err := clusterSSHPreparationDigest(after)
		if err != nil || afterDigest != digest {
			return fail()
		}
		return *response.Proof, nil
	}
	return fail()
}
