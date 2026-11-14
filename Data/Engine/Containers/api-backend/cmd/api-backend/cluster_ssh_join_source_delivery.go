package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"io"
	"net/http"
	"slices"
	"sync"
	"time"
)

// At most two paired-target reads. Entries live only for their synchronous
// controller acquisition. Callback ciphertext is not evidence of native origin;
// finish requires an independently read immutable Job/Pod receipt.
type clusterSSHJoinSourceDeliveries struct {
	mu      sync.Mutex
	secret  []byte
	pending map[string]*clusterSSHJoinSourcePending
}

type clusterSSHJoinSourcePending struct {
	request  clusterbootstrap.K3sJoinSourceRequest
	wire     []byte
	receipt  clusterbootstrap.K3sJoinSourceReceipt
	consumed bool
}

func (*clusterSSHJoinSourceDeliveries) String() string   { return "source deliveries [redacted]" }
func (*clusterSSHJoinSourceDeliveries) GoString() string { return "source deliveries [redacted]" }
func (clusterSSHJoinSourcePending) String() string       { return "source delivery [redacted]" }
func (clusterSSHJoinSourcePending) GoString() string     { return "source delivery [redacted]" }

func newClusterSSHJoinSourceDeliveries(secret string) *clusterSSHJoinSourceDeliveries {
	b := &clusterSSHJoinSourceDeliveries{pending: map[string]*clusterSSHJoinSourcePending{}}
	if len(secret) >= 32 && len(secret) <= 4096 {
		b.secret = []byte(secret)
	}
	return b
}

func (b *clusterSSHJoinSourceDeliveries) register(request clusterbootstrap.K3sJoinSourceRequest) (func(), error) {
	if b == nil || request.Validate(time.Now()) != nil {
		return nil, clusterbootstrap.ErrK3sJoinMaterial
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.secret) < 32 || len(b.pending) >= 2 || b.pending[request.ID] != nil {
		return nil, clusterbootstrap.ErrK3sJoinMaterial
	}
	request.Expected.PeerAddresses = slices.Clone(request.Expected.PeerAddresses)
	p := &clusterSSHJoinSourcePending{request: request}
	b.pending[request.ID] = p
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.pending[request.ID] == p {
			delete(b.pending, request.ID)
		}
		clear(p.wire)
		p.wire = nil
	}, nil
}

func (b *clusterSSHJoinSourceDeliveries) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	fail := func(code int) { http.Error(w, "source join delivery unavailable", code) }
	if b == nil || r.Method != http.MethodPost || r.URL.Path != clusterbootstrap.K3sJoinSourcePath || r.URL.RawQuery != "" || r.URL.ForceQuery ||
		r.Header.Get("Content-Type") != "application/octet-stream" || r.Header.Get("Content-Encoding") != "" {
		fail(400)
		return
	}
	id := r.Header.Get(clusterbootstrap.K3sJoinSourceRequestHeader)
	if !clusterUUIDRE.MatchString(id) || id == "00000000-0000-0000-0000-000000000000" {
		fail(400)
		return
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second))
	wire, err := io.ReadAll(io.LimitReader(r.Body, clusterbootstrap.MaxK3sJoinSourceDeliveryBytes+1))
	defer clear(wire)
	if err != nil || len(wire) == 0 || len(wire) > clusterbootstrap.MaxK3sJoinSourceDeliveryBytes || r.Context().Err() != nil {
		fail(400)
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.pending[id]
	if p == nil {
		fail(404)
		return
	}
	if p.consumed {
		fail(409)
		return
	}
	frame, receipt, err := clusterbootstrap.OpenK3sJoinSourceDelivery(b.secret, p.request, wire, time.Now())
	defer clear(frame)
	if err != nil {
		fail(409)
		return
	}
	if p.wire != nil {
		if !bytes.Equal(p.wire, wire) || p.receipt != receipt {
			fail(409)
			return
		}
	} else {
		p.wire, p.receipt = bytes.Clone(wire), receipt
	}
	w.WriteHeader(http.StatusNoContent)
}

func (b *clusterSSHJoinSourceDeliveries) finish(ctx context.Context, request clusterbootstrap.K3sJoinSourceRequest,
	observed clusterbootstrap.K3sJoinSourceReceipt, check func(context.Context) error) (*clusterbootstrap.K3sJoinMaterial, error) {
	fail := clusterbootstrap.ErrK3sJoinMaterial
	if b == nil || check == nil || ctx.Err() != nil || check(ctx) != nil {
		return nil, fail
	}
	b.mu.Lock()
	p := b.pending[request.ID]
	if p == nil || p.consumed || p.receipt != observed || observed.Validate(request, observed.JobUID, observed.PodUID) != nil || len(p.wire) == 0 {
		b.mu.Unlock()
		return nil, fail
	}
	frame, receipt, err := clusterbootstrap.OpenK3sJoinSourceDelivery(b.secret, request, p.wire, time.Now())
	p.consumed = true
	clear(p.wire)
	p.wire = nil
	b.mu.Unlock()
	defer clear(frame)
	if err != nil || receipt != observed || ctx.Err() != nil || check(ctx) != nil {
		return nil, fail
	}
	input := bytes.NewReader(frame)
	material, err := clusterbootstrap.ReceiveK3sJoinMaterial(ctx, request.Expected, request.Source, input, check)
	if err != nil {
		return nil, fail
	}
	if input.Len() != 0 || request.Validate(time.Now()) != nil || ctx.Err() != nil || check(ctx) != nil {
		material.Close()
		return nil, fail
	}
	return material, nil
}
