package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClusterSSHVIPBrokerPostgresLiveAuthorityAndFreshJobs(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "replacement heartbeat join", "controller changed", "worker expired", "ciphertext changed", "credential removed", "Aegis locked", "locked after finish"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHPreparationAuthorityFixtureForTopology(t, strings.HasPrefix(mode, "replacement"))
			f.c.store.db.SetMaxOpenConns(1)
			a, err := f.read(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			_, _, lease := sshVIPFixture(t, false)
			lease.Holder = a.Source.Members[len(a.Source.Members)-1].Name
			var sources []clusterbootstrap.SourceNetwork
			for _, m := range a.Source.Members {
				sources = append(sources, sshSourceNetworkFixture(m, a.K3sVersion))
			}
			probe := func(ctx context.Context) {
				// One-connection pool must remain available inside real Kubernetes I/O.
				// A concurrent heartbeat may briefly borrow it; a retained transaction
				// would deadlock this query and fail its independent bound.
				ctx, cancel := context.WithTimeout(ctx, time.Second)
				defer cancel()
				var n int
				if err := f.c.store.db.QueryRowContext(ctx, "SELECT 1").Scan(&n); err != nil || n != 1 {
					t.Error("connection retained across Kubernetes I/O", err)
				}
			}
			kube, posts := sshVIPKubernetesFixture(t, a, sources, lease, probe)
			runner := &kubernetesClusterStepRunner{kube: kube, namespace: "borealis", controllerHolder: f.lease.ControllerHolder, actionImage: "registry.example/api@sha256:" + strings.Repeat("a", 64), jobPollInterval: time.Millisecond}
			controller := &clusterController{store: f.c.store, runner: runner, holder: f.c.holder}
			t.Setenv("BOREALIS_OPERATOR_SECRET", sshBrokerTestSecret)
			server := httptest.NewServer(controller.healthServer().Handler)
			defer server.Close()
			client := sshVIPBrokerClient(t, server.URL)
			heartbeatEntered := make(chan struct{})
			if mode == "replacement heartbeat join" {
				// Hold an actual worker heartbeat across the final synchronous
				// check. Normal shutdown must join it without canceling its HTTP
				// request and therefore invalidating the controller scope.
				transport := http.DefaultTransport.(*http.Transport).Clone()
				defer transport.CloseIdleConnections()
				var controls atomic.Int64
				finalChecked := make(chan struct{})
				client.httpClient.Transport = bootstrapRoundTripper(func(r *http.Request) (*http.Response, error) {
					var control clusterSSHVIPControl
					if r.URL.Path == clusterSSHVIPCheckPath {
						raw, err := io.ReadAll(r.Body)
						r.Body.Close()
						if err != nil || openClusterSSHVIP(client.checkRequest, clusterSSHVIPCheckPath, raw, &control, clusterSSHVIPFrameLimit) != nil {
							return nil, clusterbootstrap.ErrPreparationConfig
						}
						r.Body = io.NopCloser(bytes.NewReader(raw))
					}
					var n int64
					if control.Step == "authority" {
						n = controls.Add(1)
					}
					if n == 2 {
						close(heartbeatEntered)
						select {
						case <-finalChecked:
						case <-r.Context().Done():
							return nil, r.Context().Err()
						}
						timer := time.NewTimer(30 * time.Millisecond)
						defer timer.Stop()
						select {
						case <-timer.C:
						case <-r.Context().Done():
							return nil, r.Context().Err()
						}
					}
					response, err := transport.RoundTrip(r)
					if n == 3 {
						close(finalChecked)
					}
					return response, err
				})
			}
			events := f.events(t)
			consumed := false
			consume := func(ctx context.Context, owner clusterSSHVIPOwner, checks clusterSSHPreparationChecks) error {
				consumed = true
				if posts.Load() != int64(2*len(sources)) || !validClusterSSHVIPOwner(owner, a, sources) {
					t.Error("missing fresh source ownership")
				}
				if mode == "replacement heartbeat join" {
					select {
					case <-heartbeatEntered:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				switch mode {
				case "controller changed":
					f.exec(t, `UPDATE engine.cluster_application_leases SET holder='other-controller' WHERE name=$1`, clusterControllerLeaseName)
				case "worker expired":
					f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=0 WHERE id=$1`, f.lease.TargetID)
				case "ciphertext changed":
					f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, f.lease.TargetID)
				case "credential removed":
					f.exec(t, `DELETE FROM engine.cluster_onboarding_credentials WHERE target_id=$1`, f.lease.TargetID)
				case "Aegis locked":
					f.aegis.mu.Lock()
					clear(f.aegis.key)
					f.aegis.key = nil
					f.aegis.mu.Unlock()
				}
				return checks.Inputs(ctx)
			}
			authority := f.read
			if mode == "locked after finish" {
				// Only worker memory key changes after the controller's last source round.
				authority = func(ctx context.Context) (clusterSSHPreparationAuthority, error) {
					if posts.Load() >= int64(4*len(sources)) {
						f.aegis.mu.Lock()
						clear(f.aegis.key)
						f.aegis.key = nil
						f.aegis.mu.Unlock()
					}
					return f.read(ctx)
				}
			}
			err = client.withOwner(f.ctx, authority, f.lease, f.baseline, f.sealed, sources, consume)
			good := mode == "expansion" || strings.HasPrefix(mode, "replacement")
			if (err == nil) != good || !consumed {
				t.Fatal("live broker authority outcome", err, consumed)
			}
			rounds := 4
			if mode == "replacement heartbeat join" {
				rounds = 3 // No explicit Inputs call inside this consumer.
			}
			if good && posts.Load() != int64(rounds*len(sources)) {
				t.Fatal("missing final source Job round", posts.Load())
			}
			server.Close()
			if f.c.store.db.Stats().InUse != 0 || f.events(t) != events {
				t.Fatal("retained connection or published event")
			}
		})
	}
}
