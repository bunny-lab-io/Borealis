package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"crypto/cipher"
	"io"
	"net/http"
	"reflect"
	"sync"
	"time"
)

const clusterSSHJoinBrokerPath = "/internal/cluster/ssh-k3s-join"

type clusterSSHJoinBrokerRequest struct {
	Request  clusterSSHSourceBrokerRequest
	Expected clusterbootstrap.PreparationExpected
	Source   engineidentity.Binding
	Network  clusterbootstrap.SourceNetwork
}

func (clusterSSHJoinBrokerRequest) String() string   { return "join broker request [redacted]" }
func (clusterSSHJoinBrokerRequest) GoString() string { return "join broker request [redacted]" }

func (r clusterSSHJoinBrokerRequest) valid(now time.Time) bool {
	public := clusterbootstrap.K3sJoinSourceRequest{Version: 1, ID: r.Request.ID, ExpiresAt: r.Request.ExpiresAt, Expected: r.Expected, Source: r.Source, Network: r.Network}
	return r.Request.validWithin(now, 90) && validClusterSSHPreparationLease(r.Request.Lease) && public.Validate(now) == nil &&
		r.Expected.Source == r.Request.Baseline && r.Expected.Target.ClusterID == r.Request.Binding.ClusterID && r.Expected.Target.OperationID == r.Request.Lease.OperationID &&
		r.Expected.Target.TargetID == r.Request.Lease.TargetID && r.Expected.Target.Generation == r.Request.Lease.Generation &&
		r.Expected.Target.HolderID == r.Request.Lease.Holder && r.Expected.Target.OperationAttempt == r.Request.Lease.OperationAttempt
}

func (r clusterSSHJoinBrokerRequest) authorityMatches(a clusterSSHPreparationAuthority) bool {
	if a.Lease != r.Request.Lease || a.Baseline != r.Request.Baseline || a.IdentitySource == nil || *a.IdentitySource != r.Source || a.Recovery != nil {
		return false
	}
	expected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, a.Lease, a.Baseline, a.K3sVersion, r.Network.PodCIDR, r.Network.ServiceCIDR)
	if err != nil || !reflect.DeepEqual(expected, r.Expected) || r.Source.ClusterID != a.Source.ClusterID {
		return false
	}
	for _, member := range a.Source.Members {
		if member.NodeID == r.Source.SourceID && member.NodeUID == r.Source.SourceUID && member.NodeUID == r.Network.NodeUID && member.Name == r.Network.Hostname &&
			member.MachineID == r.Network.MachineID && member.BootID == r.Network.BootID && r.Network.ManagementLink.MatchesAddress(member.Address) {
			return true
		}
	}
	return false
}

type clusterSSHJoinBrokerResponse struct {
	Version    int
	ID, Status string
	Frame      []byte
}

func (clusterSSHJoinBrokerResponse) String() string   { return "join broker response [redacted]" }
func (clusterSSHJoinBrokerResponse) GoString() string { return "join broker response [redacted]" }

type clusterSSHJoinBroker struct {
	holder                        string
	requestCipher, responseCipher cipher.AEAD
	deliveries                    *clusterSSHJoinSourceDeliveries
	read                          func(context.Context, clusterSSHJoinBrokerRequest) ([]byte, error)
	mu                            sync.Mutex
	seen                          map[string]int64
	active                        int
}

func newClusterSSHJoinBroker(store *postgresOperatorStore, runner *kubernetesClusterStepRunner, holder, secret string) *clusterSSHJoinBroker {
	b := &clusterSSHJoinBroker{holder: holder, deliveries: newClusterSSHJoinSourceDeliveries(secret), seen: map[string]int64{}}
	b.requestCipher, _ = clusterExistingIdentityScopedCipher(secret, "request", "ssh-k3s-join")
	b.responseCipher, _ = clusterExistingIdentityScopedCipher(secret, "response", "ssh-k3s-join")
	b.read = func(ctx context.Context, request clusterSSHJoinBrokerRequest) ([]byte, error) {
		fail := clusterbootstrap.ErrK3sJoinMaterial
		if !clusterControllerEligible() || store == nil || store.db == nil || runner == nil || runner.kube == nil || runner.controllerHolder != holder || request.Request.Lease.ControllerHolder != holder {
			return nil, fail
		}
		authority := func(ctx context.Context) (clusterSSHPreparationAuthority, error) {
			ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			return store.loadClusterSSHPreparationAuthority(ctx, request.Request.Lease, request.Request.Baseline, request.Request.sealed())
		}
		check, err := newClusterSSHJoinAuthorityCheck(ctx, request, authority)
		if err != nil {
			return nil, fail
		}
		material, err := runner.readSSHSourceK3sJoin(ctx, b.deliveries, authority, request.Expected, request.Source, request.Network)
		if err != nil {
			return nil, fail
		}
		defer material.Close()
		storage := make([]byte, clusterbootstrap.MaxK3sJoinTokenBytes+clusterbootstrap.MaxK3sJoinCABytes+1024)
		defer clear(storage)
		out := bytes.NewBuffer(storage[:0])
		if material.WriteJoinMaterial(ctx, request.Expected, request.Source, out, check) != nil || check(ctx) != nil {
			return nil, fail
		}
		return bytes.Clone(out.Bytes()), nil
	}
	return b
}

func newClusterSSHJoinAuthorityCheck(ctx context.Context, request clusterSSHJoinBrokerRequest, authority clusterSSHPreparationAuthorityRead) (func(context.Context) error, error) {
	fail := clusterbootstrap.ErrK3sJoinMaterial
	if authority == nil || ctx.Err() != nil || !request.valid(time.Now()) {
		return nil, fail
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	before, err := authority(ctx)
	if err != nil || !request.authorityMatches(before) {
		return nil, fail
	}
	before.Cohort.ObservedAt = 0
	digest, err := clusterSSHPreparationDigest(before)
	if err != nil {
		return nil, fail
	}
	return func(ctx context.Context) error {
		// Request expiry bounds acquisition transport. The scoped material can
		// outlive that transport only under unchanged live claim/Aegis authority.
		if ctx.Err() != nil {
			return fail
		}
		after, err := authority(ctx)
		if err != nil || !request.authorityMatches(after) || ctx.Err() != nil {
			return fail
		}
		after.Cohort.ObservedAt = 0
		observed, err := clusterSSHPreparationDigest(after)
		if err != nil || observed != digest {
			return fail
		}
		return nil
	}, nil
}

func (b *clusterSSHJoinBroker) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	fail := func(code int) { http.Error(w, "join source unavailable", code) }
	if b == nil || r.Method != http.MethodPost || r.URL.Path != clusterSSHJoinBrokerPath || r.URL.RawQuery != "" || r.URL.ForceQuery ||
		r.Header.Get("Content-Type") != clusterSSHSourceBrokerMedia || r.Header.Get("Content-Encoding") != "" {
		fail(400)
		return
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second))
	wire, err := io.ReadAll(io.LimitReader(r.Body, clusterSSHSourceBrokerLimit+1))
	defer clear(wire)
	var request clusterSSHJoinBrokerRequest
	if err != nil || openClusterSSHVIP(b.requestCipher, clusterSSHJoinBrokerPath, wire, &request, clusterSSHSourceBrokerLimit) != nil || !request.valid(time.Now()) {
		fail(401)
		return
	}
	reply := func(status string, frame []byte) {
		wire, err := sealClusterSSHVIP(b.responseCipher, clusterSSHJoinBrokerPath, clusterSSHJoinBrokerResponse{1, request.Request.ID, status, frame}, clusterSSHSourceBrokerLimit)
		defer clear(wire)
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
	accepted := !replay && b.seen != nil && len(b.seen) < 128 && b.active < 2 && b.read != nil
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
	deadline := time.Unix(request.Request.ExpiresAt, 0)
	// Override only this authenticated new request; old brokers stay at40s.
	_ = http.NewResponseController(w).SetWriteDeadline(deadline.Add(time.Second))
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	defer cancel()
	if ctx.Err() != nil {
		reply("unavailable", nil)
		return
	}
	frame, err := b.read(ctx, request)
	defer clear(frame)
	if err != nil || ctx.Err() != nil || !request.valid(time.Now()) {
		reply("unavailable", nil)
		return
	}
	// Defend the response boundary independently of the concrete reader.
	input := bytes.NewReader(frame)
	check := func(ctx context.Context) error {
		if ctx.Err() != nil || !request.valid(time.Now()) {
			return clusterbootstrap.ErrK3sJoinMaterial
		}
		return nil
	}
	material, err := clusterbootstrap.ReceiveK3sJoinMaterial(ctx, request.Expected, request.Source, input, check)
	if err != nil {
		reply("unavailable", nil)
		return
	}
	material.Close()
	if input.Len() != 0 || check(ctx) != nil {
		reply("unavailable", nil)
		return
	}
	reply("observed", frame)
}
