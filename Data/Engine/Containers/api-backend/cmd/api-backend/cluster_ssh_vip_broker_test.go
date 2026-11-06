package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func sshVIPBrokerClient(t *testing.T, peers ...string) *clusterSSHVIPBrokerClient {
	t.Helper()
	t.Setenv("BOREALIS_OPERATOR_SECRET", sshBrokerTestSecret)
	c, err := newClusterSSHVIPBrokerClientFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	c.peers = func(context.Context) ([]string, error) { return peers, nil }
	return c
}
func sshVIPBrokerServer(t *testing.T, b *clusterSSHVIPBroker) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+clusterSSHVIPStartPath, b.start)
	mux.HandleFunc("POST "+clusterSSHVIPCheckPath, b.check)
	server := httptest.NewUnstartedServer(mux)
	server.Config.ReadHeaderTimeout = 5 * time.Second
	server.Config.ReadTimeout = 5 * time.Second
	server.Config.WriteTimeout = 40 * time.Second
	server.Start()
	t.Cleanup(server.Close)
	return server
}
func sshVIPBrokerRun(a clusterSSHPreparationAuthority, lease clusterbootstrap.VIPLease, changed *atomic.Bool, reads *atomic.Int64) clusterSSHVIPScopeRun {
	var renewals atomic.Int64
	return func(ctx context.Context, v clusterSSHVIPStart, consume func(context.Context, clusterSSHVIPOwner, clusterSSHPreparationChecks) error) error {
		authority := func(context.Context) (clusterSSHPreparationAuthority, error) { return a, nil }
		readLease := func(context.Context) (clusterbootstrap.VIPLease, error) {
			n := renewals.Add(1)
			l := lease
			l.ResourceVersion = fmt.Sprint(n)
			l.RenewTime = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(n+2) * time.Second).Format(time.RFC3339Nano)
			return l, nil
		}
		readVIP := func(ctx context.Context, m clusterSSHSourceMember) (clusterSSHSourceVIPObservation, error) {
			reads.Add(1)
			n := sshSourceNetworkFixture(m, a.K3sVersion)
			if changed.Load() {
				n.ManagementLink.Index++
			}
			return sshVIPObservation(n, a.Source.ControlPlaneVIP, m.Name == lease.Holder), nil
		}
		return withClusterSSHVIPOwner(ctx, authority, v.Sources, readVIP, readLease, consume)
	}
}

func TestClusterSSHVIPBrokerLiveScopeAndFailures(t *testing.T) {
	for _, mode := range []string{"success", "replacement", "follower", "concurrent inputs", "copied inputs", "closed checks", "changed source", "changed final source", "locked worker", "changed authority", "ignored failed check", "canceled check", "consumer error", "canceled consumer", "controller loss", "blocked control", "lost control response", "redirect", "wrong response ID", "wrong response scope", "replayed control", "truncated stream", "early complete"} {
		t.Run(mode, func(t *testing.T) {
			a, sources, lease := sshVIPFixture(t, mode == "replacement")
			r, _, _ := sshBrokerFixture(t)
			r.Lease = a.Lease
			r.Baseline = a.Baseline
			r.Binding = a.Cohort.Targets[0].Binding
			var changed, locked atomic.Bool
			var reads, controls, entered, serverActive atomic.Int64
			b := newClusterSSHVIPBroker(nil, nil, a.Lease.ControllerHolder, sshBrokerTestSecret)
			original := sshVIPBrokerRun(a, lease, &changed, &reads)
			var serverCancel atomic.Value
			b.run = func(ctx context.Context, v clusterSSHVIPStart, consume func(context.Context, clusterSSHVIPOwner, clusterSSHPreparationChecks) error) error {
				serverActive.Add(1)
				defer serverActive.Add(-1)
				ctx, cancel := context.WithCancel(ctx)
				defer cancel()
				serverCancel.Store(cancel)
				return original(ctx, v, consume)
			}
			base := sshVIPBrokerServer(t, b)
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.Proxy = nil
			defer transport.CloseIdleConnections()
			var replay []byte
			var replayMu sync.Mutex
			mux := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == clusterSSHVIPStartPath {
					if mode == "redirect" {
						w.Header().Set("Location", base.URL+clusterSSHVIPStartPath)
						w.WriteHeader(302)
						return
					}
					if mode == "early complete" {
						raw, _ := io.ReadAll(req.Body)
						var start clusterSSHVIPStart
						_ = openClusterSSHVIP(b.startRequest, clusterSSHVIPStartPath, raw, &start, clusterSSHSourceBrokerLimit)
						scope := newClusterUUID()
						n := sources[len(sources)-1]
						m := a.Source.Members[len(sources)-1]
						owner := clusterSSHVIPOwner{Address: a.Source.ControlPlaneVIP, Owner: clusterSSHManagementPeer{ID: m.NodeID, NodeUID: m.NodeUID, Hostname: m.Name, MachineID: m.MachineID, BootID: m.BootID, SSHFingerprint: m.SSHFingerprint, Link: n.ManagementLink}, LeaseUID: lease.UID, AcquireTime: lease.AcquireTime, Transitions: lease.Transitions}
						w.Header().Set("Content-Type", clusterSSHSourceBrokerMedia)
						_ = writeClusterSSHVIPFrame(w, b.startResponse, clusterSSHVIPMessage{Version: 1, ID: start.Request.ID, Scope: scope, Status: "ready", Owner: &owner})
						_ = writeClusterSSHVIPFrame(w, b.startResponse, clusterSSHVIPMessage{Version: 1, ID: start.Request.ID, Scope: scope, Status: "complete"})
						return
					}
					b.start(w, req)
					return
				}
				count := controls.Add(1)
				if mode == "blocked control" && entered.Load() > 0 {
					_, _ = io.Copy(io.Discard, req.Body)
					<-req.Context().Done()
					return
				}
				if mode == "truncated stream" && count == 1 {
					serverCancel.Load().(context.CancelFunc)()
				}
				if mode == "replayed control" {
					replayMu.Lock()
					defer replayMu.Unlock()
					raw, _ := io.ReadAll(req.Body)
					if replay == nil {
						replay = bytes.Clone(raw)
					} else {
						raw = bytes.Clone(replay)
					}
					req.Body = io.NopCloser(bytes.NewReader(raw))
				}
				if slices.Contains([]string{"lost control response", "wrong response ID", "wrong response scope"}, mode) && entered.Load() > 0 {
					rec := httptest.NewRecorder()
					b.check(rec, req)
					if mode == "lost control response" {
						w.WriteHeader(502)
						w.Write([]byte("private diagnostic"))
						return
					}
					var reply clusterSSHVIPMessage
					_ = openClusterSSHVIP(b.checkResponse, clusterSSHVIPCheckPath, rec.Body.Bytes(), &reply, clusterSSHVIPFrameLimit)
					if mode == "wrong response ID" {
						reply.ID = newClusterUUID()
					} else {
						reply.Scope = newClusterUUID()
					}
					raw, _ := sealClusterSSHVIP(b.checkResponse, clusterSSHVIPCheckPath, reply, clusterSSHVIPFrameLimit)
					w.Header().Set("Content-Type", clusterSSHSourceBrokerMedia)
					w.Write(raw)
					return
				}
				b.check(w, req)
			})
			server := httptest.NewServer(mux)
			defer server.Close()
			peers := []string{server.URL}
			if mode == "follower" {
				other := newClusterSSHVIPBroker(nil, nil, "other-holder", sshBrokerTestSecret)
				peers = append([]string{sshVIPBrokerServer(t, other).URL}, peers...)
			}
			c := sshVIPBrokerClient(t, peers...)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			authority := func(context.Context) (clusterSSHPreparationAuthority, error) {
				v := a
				if locked.Load() {
					if mode == "changed authority" {
						v.Lease.Generation++
					} else {
						return v, errors.New("private Aegis")
					}
				}
				return v, nil
			}
			var saved clusterSSHPreparationChecks
			consume := func(ctx context.Context, owner clusterSSHVIPOwner, checks clusterSSHPreparationChecks) error {
				entered.Add(1)
				saved = checks
				if reads.Load() != 2*int64(len(sources)) || !validClusterSSHVIPOwner(owner, a, sources) {
					t.Error("incomplete acquisition")
				}
				switch mode {
				case "copied inputs":
					sources[0].ManagementLink.Index++
				case "changed source", "changed final source":
					changed.Store(true)
				case "locked worker", "changed authority":
					locked.Store(true)
				case "consumer error":
					return errors.New("private consumer")
				case "canceled consumer":
					cancel()
					<-ctx.Done()
					return nil
				case "canceled check":
					bad, end := context.WithCancel(context.Background())
					end()
					_ = checks.Authority(bad)
					return nil
				case "controller loss":
					serverCancel.Load().(context.CancelFunc)()
					<-ctx.Done()
					return nil
				case "ignored failed check":
					locked.Store(true)
					_ = checks.Authority(context.Background())
					locked.Store(false)
					return nil
				case "blocked control":
					<-ctx.Done()
					return nil
				}
				if mode == "changed final source" {
					return nil
				}
				if mode == "concurrent inputs" {
					var wg sync.WaitGroup
					errs := make(chan error, 3)
					for i := 0; i < 3; i++ {
						wg.Add(1)
						go func() { defer wg.Done(); errs <- checks.Inputs(ctx) }()
					}
					wg.Wait()
					close(errs)
					for err := range errs {
						if err != nil {
							return err
						}
					}
				} else if checks.Inputs(ctx) != nil {
					return clusterbootstrap.ErrPreparationConfig
				}
				// Keep scope open across server write/read deadlines and a worker heartbeat.
				if mode == "success" {
					select {
					case <-time.After(1200 * time.Millisecond):
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				return nil
			}
			start := time.Now()
			err := c.withOwner(ctx, authority, a.Lease, a.Baseline, r.sealed(), sources, consume)
			good := slices.Contains([]string{"success", "replacement", "follower", "concurrent inputs", "copied inputs", "closed checks"}, mode)
			if (err == nil) != good {
				t.Fatal("bridge outcome", mode, err)
			}
			if err != nil && err != clusterbootstrap.ErrPreparationConfig {
				t.Fatal("private failure escaped")
			}
			if good && entered.Load() != 1 {
				t.Fatal("consumer skipped")
			}
			if time.Since(start) > 6*time.Second {
				t.Fatal("failed bridge exceeded test bound")
			}
			if mode == "closed checks" && (saved.Authority(context.Background()) == nil || saved.Inputs(context.Background()) == nil) {
				t.Fatal("checks survived scope")
			}
			deadline := time.Now().Add(time.Second)
			for {
				b.mu.Lock()
				active, scopes := b.active, len(b.scopes)
				b.mu.Unlock()
				if active == 0 && scopes == 0 && serverActive.Load() == 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("controller scope leaked")
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
}

func TestClusterSSHVIPBrokerWireAndReplayBounds(t *testing.T) {
	r, a, snapshot := sshBrokerFixture(t)
	b := newClusterSSHVIPBroker(nil, nil, r.Lease.ControllerHolder, sshBrokerTestSecret)
	start := clusterSSHVIPStart{Request: r, Sources: snapshot.Sources}
	wire, err := sealClusterSSHVIP(b.startRequest, clusterSSHVIPStartPath, start, clusterSSHSourceBrokerLimit)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"direction", "path", "tampered", "oversize", "unknown field", "null sources", "duplicate key"} {
		t.Run(mode, func(t *testing.T) {
			raw := bytes.Clone(wire)
			key, path := b.startRequest, clusterSSHVIPStartPath
			switch mode {
			case "direction":
				key = b.checkRequest
			case "path":
				path = clusterSSHVIPCheckPath
			case "tampered":
				raw[len(raw)-1] ^= 1
			case "oversize":
				raw = make([]byte, clusterSSHSourceBrokerLimit+1)
			default:
				plain, _ := json.Marshal(start)
				if mode == "unknown field" {
					plain = append([]byte(`{"private":"secret",`), plain[1:]...)
				}
				if mode == "duplicate key" {
					plain = append([]byte(`{"sources":[],`), plain[1:]...)
				}
				if mode == "null sources" {
					start.Sources = nil
					plain, _ = json.Marshal(start)
				}
				raw = b.startRequest.Seal(nil, nil, plain, []byte(path))
			}
			var decoded clusterSSHVIPStart
			err := openClusterSSHVIP(key, path, raw, &decoded, clusterSSHSourceBrokerLimit)
			if err == nil && validClusterSSHVIPStart(decoded) {
				t.Fatal("unsafe start accepted")
			}
		})
	}
	var calls atomic.Int64
	b.run = func(context.Context, clusterSSHVIPStart, func(context.Context, clusterSSHVIPOwner, clusterSSHPreparationChecks) error) error {
		calls.Add(1)
		return clusterbootstrap.ErrPreparationConfig
	}
	server := sshVIPBrokerServer(t, b)
	for i := 0; i < 2; i++ {
		req, _ := http.NewRequest("POST", server.URL+clusterSSHVIPStartPath, bytes.NewReader(wire))
		req.Header.Set("Content-Type", clusterSSHSourceBrokerMedia)
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	if calls.Load() != 1 {
		t.Fatal("start replay executed work")
	}
	control := clusterSSHVIPControl{Version: 1, ID: newClusterUUID(), Start: r.ID, Scope: newClusterUUID(), Step: "authority"}
	var canceled atomic.Bool
	s := &clusterSSHVIPSession{ctx: context.Background(), cancel: func() { canceled.Store(true) }, seen: map[string]bool{}, changed: make(chan struct{})}
	if !s.enter(control) {
		t.Fatal("valid control")
	}
	s.leave()
	if s.enter(control) || !canceled.Load() {
		t.Fatal("control replay did not fence scope")
	}
	for _, raw := range [][]byte{{0, 0, 0, 0}, {0, 1, 0, 0}, {0, 0, 0, 5, 1, 2}} {
		if _, err := readClusterSSHVIPFrame(bytes.NewReader(raw), b.startResponse); err == nil {
			t.Fatal("bad frame accepted")
		}
	}
	if validClusterSSHVIPOwner(clusterSSHVIPOwner{}, a, snapshot.Sources) {
		t.Fatal("missing owner accepted")
	}
	if strings.Contains(fmt.Sprint(start), r.Ciphertext) {
		t.Fatal("private start formatted")
	}
}

func TestClusterSSHVIPBrokerPinnedPeerComposition(t *testing.T) {
	for _, mode := range []string{"success", "target changed", "target changed during VIP acquisition", "credential lost", "ignored peer failure"} {
		t.Run(mode, func(t *testing.T) {
			a, sources, lease := sshVIPFixture(t, false)
			keys := sshNetworkFixtureKeys(t, &a.Cohort)
			r, _, _ := sshBrokerFixture(t)
			r.Lease = a.Lease
			r.Baseline = a.Baseline
			r.Binding = a.Cohort.Targets[0].Binding
			authority := func(context.Context) (clusterSSHPreparationAuthority, error) { return a, nil }
			var changed, lost atomic.Bool
			var vipReads atomic.Int64
			broker := newClusterSSHVIPBroker(nil, nil, a.Lease.ControllerHolder, sshBrokerTestSecret)
			run := sshVIPBrokerRun(a, lease, &atomic.Bool{}, &vipReads)
			broker.run = func(ctx context.Context, v clusterSSHVIPStart, consume func(context.Context, clusterSSHVIPOwner, clusterSSHPreparationChecks) error) error {
				if mode == "target changed during VIP acquisition" {
					changed.Store(true)
				}
				return run(ctx, v, consume)
			}
			server := sshVIPBrokerServer(t, broker)
			client := sshVIPBrokerClient(t, server.URL)
			source := func(context.Context) (clusterSSHPreparationSnapshot, error) {
				expected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, a.Lease, a.Baseline, a.K3sVersion, sources[0].PodCIDR, sources[0].ServiceCIDR)
				return clusterSSHPreparationSnapshot{Expected: expected, Observation: strings.Repeat("a", 64), Sources: slices.Clone(sources), started: time.Now()}, err
			}
			target := func(ctx context.Context, item clusterSSHInspectedTarget, peers []string) (clusterremote.TargetManagementPeer, error) {
				link := sshSourceNetworkFixture(clusterSSHSourceMember{Address: item.Binding.Address}, a.K3sVersion).ManagementLink
				if changed.Load() {
					link.NetworkNamespace++
				}
				return sshNetworkFixturePeer(t, ctx, item, keys[item.Binding.TargetID], link, peers)
			}
			refresh := func(context.Context) error {
				if lost.Load() {
					return errors.New("private credential")
				}
				return nil
			}
			consume := func(ctx context.Context, peers []clusterSSHManagementPeer, owner clusterSSHVIPOwner, checks clusterSSHPreparationChecks) error {
				if len(peers) != 3 || owner.Owner != peers[0] {
					t.Error("unbound peer/VIP identity")
				}
				if mode == "target changed" || mode == "ignored peer failure" {
					changed.Store(true)
				}
				if mode == "credential lost" {
					lost.Store(true)
				}
				err := checks.Inputs(ctx)
				if mode == "ignored peer failure" {
					changed.Store(false)
					return nil
				}
				return err
			}
			err := client.withNetworkOwner(context.Background(), authority, source, target, refresh, a.Lease, a.Baseline, r.sealed(), consume)
			if (err == nil) != (mode == "success") {
				t.Fatal("peer/VIP composition", err)
			}
		})
	}
}

func TestClusterSSHVIPBrokerAdmissionAndResourceBounds(t *testing.T) {
	for _, mode := range []string{"active limit", "replay limit", "expired", "wrong method", "query", "wrong media", "missing sources"} {
		t.Run(mode, func(t *testing.T) {
			request, _, snapshot := sshBrokerFixture(t)
			b := newClusterSSHVIPBroker(nil, nil, request.Lease.ControllerHolder, sshBrokerTestSecret)
			var calls atomic.Int64
			b.run = func(context.Context, clusterSSHVIPStart, func(context.Context, clusterSSHVIPOwner, clusterSSHPreparationChecks) error) error {
				calls.Add(1)
				return clusterbootstrap.ErrPreparationConfig
			}
			start := clusterSSHVIPStart{Request: request, Sources: snapshot.Sources}
			switch mode {
			case "active limit":
				b.active = 2
			case "replay limit":
				for i := 0; i < 128; i++ {
					b.seen[newClusterUUID()] = time.Now().Unix() + 35
				}
			case "expired":
				start.Request.ExpiresAt = time.Now().Unix() - 1
			case "missing sources":
				start.Sources = nil
			}
			wire, _ := sealClusterSSHVIP(b.startRequest, clusterSSHVIPStartPath, start, clusterSSHSourceBrokerLimit)
			method, path, media := "POST", clusterSSHVIPStartPath, clusterSSHSourceBrokerMedia
			if mode == "wrong method" {
				method = "GET"
			}
			if mode == "query" {
				path += "?extra=true"
			}
			if mode == "wrong media" {
				media = "application/json"
			}
			req := httptest.NewRequest(method, path, bytes.NewReader(wire))
			req.Header.Set("Content-Type", media)
			w := httptest.NewRecorder()
			b.start(w, req)
			if w.Code < 400 || calls.Load() != 0 || strings.Contains(w.Body.String(), request.Ciphertext) {
				t.Fatal("invalid admission reached work")
			}
		})
	}
	for _, mode := range []string{"concurrent", "history", "closing"} {
		t.Run(mode, func(t *testing.T) {
			var canceled atomic.Bool
			s := &clusterSSHVIPSession{ctx: context.Background(), cancel: func() { canceled.Store(true) }, seen: map[string]bool{}, changed: make(chan struct{})}
			if mode == "concurrent" {
				s.active = 4
			}
			if mode == "closing" {
				s.closing = true
			}
			if mode == "history" {
				for i := 0; i < 128; i++ {
					s.seen[newClusterUUID()] = true
				}
			}
			v := clusterSSHVIPControl{Version: 1, ID: newClusterUUID(), Start: newClusterUUID(), Scope: newClusterUUID(), Step: "authority"}
			if s.enter(v) || !canceled.Load() {
				t.Fatal("exhausted scope not fenced")
			}
		})
	}
}
