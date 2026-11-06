package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestClusterAutomaticRecoveryPostgresNativeNodeAuthority(t *testing.T) {
	for _, mode := range []string{"partial health", "holder before", "holder after", "expiry before", "expiry after", "cluster after", "drain after", "node after", "logical cluster mismatch", "runner mismatch", "target mismatch", "first close after", "last close after", "cancel close after"} {
		t.Run(mode, func(t *testing.T) {
			f := newAutomaticRecoveryPostgresFixture(t)
			r := f.reserve(t)
			b, namespace, node := automaticRecoveryNativeFixture()
			b.ClusterID, b.NodeID, b.NodeName, b.NodeUID, b.Controller = r.ClusterID, r.NodeID, r.NodeName, r.NodeUID, r.Controller
			clusterSSHStorageMap(node, "metadata")["name"], clusterSSHStorageMap(node, "metadata")["uid"] = b.NodeName, b.NodeUID
			var calls atomic.Int32
			changeAuthority := func() {
				var query string
				var args []any
				switch mode {
				case "holder before", "holder after":
					query, args = `UPDATE engine.cluster_application_leases SET holder='replacement' WHERE name=$1`, []any{clusterControllerLeaseName}
				case "expiry before", "expiry after":
					query, args = `UPDATE engine.cluster_application_leases SET expires_at=1 WHERE name=$1`, []any{clusterControllerLeaseName}
				case "cluster after":
					query, args = `UPDATE engine.cluster_state SET cluster_id=$1 WHERE id=1`, []any{newClusterUUID()}
				case "drain after":
					query, args = `UPDATE engine.cluster_nodes SET application_state='drained',drain_reason='operator' WHERE id=$1`, []any{b.NodeID}
				case "node after":
					query, args = `UPDATE engine.cluster_nodes SET node_name='replaced' WHERE id=$1`, []any{b.NodeID}
				}
				if query != "" {
					if _, err := f.c.controller.store.db.ExecContext(f.ctx, query, args...); err != nil {
						t.Error(err)
					}
				}
			}
			if strings.HasSuffix(mode, " before") {
				changeAuthority()
			}
			// Restore the logical row for the fixture's exact-identity cleanup.
			if mode == "cluster after" {
				t.Cleanup(func() { f.exec(t, `UPDATE engine.cluster_state SET cluster_id=$1 WHERE id=1`, r.ClusterID) })
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				call := calls.Add(1)
				if f.c.controller.store.db.Stats().InUse != 0 {
					t.Error("database connection retained during native health GET")
				}
				if request.Method != http.MethodGet {
					t.Error("native observation attempted mutation")
				}
				if call == 4 && strings.HasSuffix(mode, " after") {
					changeAuthority()
				}
				object := namespace
				if request.URL.Path == "/api/v1/nodes/"+b.NodeName {
					object = node
				} else if request.URL.Path != "/api/v1/namespaces/kube-system" {
					t.Error("unexpected native endpoint", request.URL.Path)
				}
				_ = json.NewEncoder(w).Encode(object)
			}))
			defer server.Close()
			client := server.Client()
			base := client.Transport
			var active, closes atomic.Int64
			closed, release := make(chan struct{}), make(chan struct{})
			var unblock sync.Once
			defer unblock.Do(func() { close(release) })
			transport := &cohortMembershipTransport{roundTrip: func(request *http.Request) (*http.Response, error) {
				response, err := base.RoundTrip(request)
				if response != nil && response.Body != nil {
					active.Add(1)
					response.Body = &cohortMembershipBody{response.Body, func() error {
						defer active.Add(-1)
						n := closes.Add(1)
						if f.c.controller.store.db.Stats().InUse != 0 {
							t.Error("database connection retained during native health cleanup")
						}
						if mode == "cancel close after" && n == 4 {
							close(closed)
							<-release
						}
						if mode == "first close after" && n == 1 || mode == "last close after" && n == 4 {
							return errors.New("synthetic private native response cleanup failure")
						}
						return nil
					}}
				}
				return response, err
			}}
			client.Transport = transport
			runner := &kubernetesClusterStepRunner{controllerHolder: r.Controller, kube: &kubernetesAPIClient{baseURL: server.URL, httpClient: client}}
			f.c.controller.runner = runner
			defer func() {
				if active.Load() != 0 || transport.idleCalls.Load() != 0 || client.Transport != transport || runner.kube.httpClient != client {
					t.Error("native health escaped cleanup or changed shared transport")
				}
			}()
			if mode == "logical cluster mismatch" {
				b.ClusterID = newClusterUUID()
			}
			if mode == "runner mismatch" {
				runner.controllerHolder = "other"
			}
			target := clusterControllerNode{ID: r.NodeID, Name: r.NodeName, ApplicationState: "active"}
			if mode == "target mismatch" {
				target.ID = newClusterUUID()
			}
			observer := f.c.nodeHealthObserver(b)
			if mode == "cancel close after" {
				ctx, cancel := context.WithCancel(f.ctx)
				defer cancel()
				type result struct {
					health clusterAutomaticRecoveryHealth
					err    error
				}
				done := make(chan result, 1)
				go func() { health, err := observer(ctx, target); done <- result{health, err} }()
				joined := false
				defer func() {
					cancel()
					unblock.Do(func() { close(release) })
					if !joined {
						select {
						case <-done:
						case <-time.After(5 * time.Second):
							t.Error("native health did not join released cleanup")
						}
					}
				}()
				select {
				case <-closed:
				case got := <-done:
					joined = true
					t.Fatal("native health returned before cleanup gate", got)
				case <-ctx.Done():
					t.Fatal("native health never reached cleanup gate")
				}
				cancel()
				select {
				case got := <-done:
					joined = true
					t.Fatal("native health escaped cancellation before cleanup joined", got)
				case <-time.After(25 * time.Millisecond):
				}
				unblock.Do(func() { close(release) })
				select {
				case got := <-done:
					joined = true
					if got.err == nil || got.health != (clusterAutomaticRecoveryHealth{}) || calls.Load() != 4 || closes.Load() != 4 {
						t.Fatal("cancelled cleanup retained health", got)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("native health did not join cleanup")
				}
				return
			}
			if mode != "partial health" {
				health, err := observer(f.ctx, target)
				if err == nil || health != (clusterAutomaticRecoveryHealth{}) {
					t.Fatal("changed authority retained health", health, err)
				}
				if !strings.HasSuffix(mode, " after") && calls.Load() != 0 {
					t.Fatal("unowned native health reached Kubernetes")
				}
				return
			}
			seed, err := f.c.Observe(f.ctx, r.OperationID, f.observer(t, true))
			if err != nil || seed.HealthySince == 0 {
				t.Fatal("healthy fixture interval missing", seed, err)
			}
			next, err := f.c.Observe(f.ctx, r.OperationID, observer)
			if err != nil || next.Phase != "recovering" || next.HealthySince != 0 || next.RecoveredAt != 0 ||
				next.RecoveryDeadline != r.RecoveryDeadline || next.NodeUID != r.NodeUID || calls.Load() != 4 {
				t.Fatal("partial native proof advanced automatic recovery", next, err)
			}
			f.assertInert(t, r.OperationID)
		})
	}
}
