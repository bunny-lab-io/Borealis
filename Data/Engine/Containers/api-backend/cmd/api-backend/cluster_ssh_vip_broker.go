package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"crypto/cipher"
	"io"
	"net/http"
	"sync"
	"time"
)

type clusterSSHVIPScopeRun func(context.Context, clusterSSHVIPStart, func(context.Context, clusterSSHVIPOwner, clusterSSHPreparationChecks) error) error

type clusterSSHVIPBroker struct {
	holder                                                   string
	startRequest, startResponse, checkRequest, checkResponse cipher.AEAD
	run                                                      clusterSSHVIPScopeRun
	mu                                                       sync.Mutex
	seen                                                     map[string]int64
	active                                                   int
	scopes                                                   map[string]*clusterSSHVIPSession
}

type clusterSSHVIPSession struct {
	start    string
	ctx      context.Context
	cancel   context.CancelFunc
	checks   clusterSSHPreparationChecks
	finish   chan struct{}
	finishID string
	mu       sync.Mutex
	changed  chan struct{}
	seen     map[string]bool
	closing  bool
	active   int
}

func (s *clusterSSHVIPSession) enter(v clusterSSHVIPControl) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.ctx.Err() != nil || s.seen[v.ID] || len(s.seen) >= 128 || s.active >= 4 {
		s.cancel()
		return false
	}
	s.seen[v.ID] = true
	s.active++
	if v.Step == "finish" {
		s.closing = true
	}
	return true
}
func (s *clusterSSHVIPSession) leave() {
	s.mu.Lock()
	s.active--
	close(s.changed)
	s.changed = make(chan struct{})
	s.mu.Unlock()
}
func (s *clusterSSHVIPSession) wait(ctx context.Context, limit int) error {
	for {
		s.mu.Lock()
		n, changed := s.active, s.changed
		s.mu.Unlock()
		if n <= limit {
			return nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return clusterbootstrap.ErrPreparationConfig
		}
	}
}
func (s *clusterSSHVIPSession) join() {
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
	_ = s.wait(context.Background(), 0) // All admitted callbacks honor the scope context.
}

func newClusterSSHVIPBroker(store *postgresOperatorStore, runner *kubernetesClusterStepRunner, holder, secret string) *clusterSSHVIPBroker {
	b := &clusterSSHVIPBroker{holder: holder, seen: map[string]int64{}, scopes: map[string]*clusterSSHVIPSession{}}
	b.startRequest, _ = clusterSSHVIPCipher(secret, "start-request")
	b.startResponse, _ = clusterSSHVIPCipher(secret, "start-response")
	b.checkRequest, _ = clusterSSHVIPCipher(secret, "check-request")
	b.checkResponse, _ = clusterSSHVIPCipher(secret, "check-response")
	b.run = func(ctx context.Context, v clusterSSHVIPStart, consume func(context.Context, clusterSSHVIPOwner, clusterSSHPreparationChecks) error) error {
		if !clusterControllerEligible() || store == nil || runner == nil || runner.kube == nil || runner.controllerHolder != holder || v.Request.Lease.ControllerHolder != holder {
			return clusterbootstrap.ErrPreparationConfig
		}
		authority := func(ctx context.Context) (clusterSSHPreparationAuthority, error) {
			ctx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			return store.loadClusterSSHPreparationAuthority(ctx, v.Request.Lease, v.Request.Baseline, v.Request.sealed())
		}
		return withClusterSSHVIPOwner(ctx, authority, v.Sources, runner.newSSHSourceVIPRead(authority), runner.kube.readClusterSSHVIPLease, consume)
	}
	return b
}
func vipBrokerBody(w http.ResponseWriter, r *http.Request, path string, limit int) ([]byte, error) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != "POST" || r.URL.Path != path || r.URL.RawQuery != "" || r.URL.ForceQuery || r.Header.Get("Content-Type") != clusterSSHSourceBrokerMedia || r.Header.Get("Content-Encoding") != "" {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(time.Second))
	raw, err := io.ReadAll(io.LimitReader(r.Body, int64(limit+1)))
	if err != nil || len(raw) > limit {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	return raw, nil
}
func (b *clusterSSHVIPBroker) start(w http.ResponseWriter, r *http.Request) {
	raw, err := vipBrokerBody(w, r, clusterSSHVIPStartPath, clusterSSHSourceBrokerLimit)
	var v clusterSSHVIPStart
	if b == nil || err != nil || openClusterSSHVIP(b.startRequest, clusterSSHVIPStartPath, raw, &v, clusterSSHSourceBrokerLimit) != nil || !validClusterSSHVIPStart(v) {
		http.Error(w, "VIP unavailable", 400)
		return
	}
	w.Header().Set("Content-Type", clusterSSHSourceBrokerMedia)
	if v.Request.Lease.ControllerHolder != b.holder {
		_ = writeClusterSSHVIPFrame(w, b.startResponse, clusterSSHVIPMessage{Version: 1, ID: v.Request.ID, Status: "not_owner"})
		return
	}
	b.mu.Lock()
	now := time.Now().Unix()
	for id, expires := range b.seen {
		if expires <= now {
			delete(b.seen, id)
		}
	}
	_, replay := b.seen[v.Request.ID]
	accepted := !replay && len(b.seen) < 128 && b.active < 2 && b.run != nil
	if accepted {
		b.seen[v.Request.ID] = v.Request.ExpiresAt
		b.active++
	}
	b.mu.Unlock()
	if !accepted {
		http.Error(w, "VIP unavailable", 409)
		return
	}
	defer func() { b.mu.Lock(); b.active--; b.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	ctx, stop := context.WithDeadline(ctx, time.Unix(v.Request.ExpiresAt, 0))
	defer stop()
	controller := http.NewResponseController(w)
	// Complete request body was read before response streaming. No hijack or
	// detached session survives this handler. Slow writes fail within one second.
	send := func(message clusterSSHVIPMessage) error {
		if controller.SetWriteDeadline(time.Now().Add(time.Second)) != nil || writeClusterSSHVIPFrame(w, b.startResponse, message) != nil || controller.Flush() != nil {
			cancel()
			return clusterbootstrap.ErrPreparationConfig
		}
		deadline, _ := ctx.Deadline()
		if controller.SetWriteDeadline(deadline) != nil {
			cancel()
			return clusterbootstrap.ErrPreparationConfig
		}
		return nil
	}
	scopeID := newClusterUUID()
	completedID := ""
	err = b.run(ctx, v, func(scope context.Context, owner clusterSSHVIPOwner, checks clusterSSHPreparationChecks) error {
		s := &clusterSSHVIPSession{start: v.Request.ID, ctx: scope, cancel: cancel, checks: checks, finish: make(chan struct{}), changed: make(chan struct{}), seen: map[string]bool{}}
		b.mu.Lock()
		b.scopes[scopeID] = s
		b.mu.Unlock()
		defer func() { s.join(); b.mu.Lock(); delete(b.scopes, scopeID); b.mu.Unlock() }()
		if send(clusterSSHVIPMessage{Version: 1, ID: v.Request.ID, Scope: scopeID, Status: "ready", Owner: &owner}) != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		select {
		case <-scope.Done():
			return clusterbootstrap.ErrPreparationConfig
		case <-s.finish:
			completedID = s.finishID
			return nil
		}
	})
	// Success is emitted only after final full-source reobservation, all control
	// callbacks and the original controller heartbeat have joined.
	if err == nil && ctx.Err() == nil {
		_ = send(clusterSSHVIPMessage{Version: 1, ID: completedID, Scope: scopeID, Status: "complete"})
	}
}
func (b *clusterSSHVIPBroker) check(w http.ResponseWriter, r *http.Request) {
	raw, err := vipBrokerBody(w, r, clusterSSHVIPCheckPath, clusterSSHVIPFrameLimit)
	var v clusterSSHVIPControl
	if b == nil || err != nil || openClusterSSHVIP(b.checkRequest, clusterSSHVIPCheckPath, raw, &v, clusterSSHVIPFrameLimit) != nil || !v.valid() {
		http.Error(w, "VIP unavailable", 400)
		return
	}
	b.mu.Lock()
	s := b.scopes[v.Scope]
	b.mu.Unlock()
	if s == nil || s.start != v.Start || !s.enter(v) {
		http.Error(w, "VIP unavailable", 409)
		return
	}
	defer s.leave()
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	if v.Step == "inputs" {
		cancel()
		ctx, cancel = context.WithTimeout(r.Context(), 30*time.Second)
	}
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	ok := ctx.Err() == nil
	if v.Step == "finish" {
		ok = ok && s.wait(ctx, 1) == nil
	}
	if ok {
		if v.Step == "inputs" {
			ok = s.checks.Inputs(ctx) == nil
		} else {
			ok = s.checks.Authority(ctx) == nil
		}
	}
	if !ok || ctx.Err() != nil || s.ctx.Err() != nil {
		s.cancel()
		http.Error(w, "VIP unavailable", 409)
		return
	}
	if v.Step == "finish" {
		s.finishID = v.ID // Published by closing finish; exactly one finish enters.
		close(s.finish)
	}
	response := clusterSSHVIPMessage{Version: 1, ID: v.ID, Scope: v.Scope, Status: "ok"}
	wire, err := sealClusterSSHVIP(b.checkResponse, clusterSSHVIPCheckPath, response, clusterSSHVIPFrameLimit)
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Second))
	if err != nil {
		s.cancel()
		return
	}
	w.Header().Set("Content-Type", clusterSSHSourceBrokerMedia)
	if n, err := w.Write(wire); err != nil || n != len(wire) {
		s.cancel()
	}
}
