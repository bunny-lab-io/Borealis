package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClusterAutomaticRecoveryApplicationPostgresAuthority(t *testing.T) {
	for _, mode := range []string{"partial health", "probe unhealthy", "native forbidden", "Pod drift", "holder before", "holder after", "expiry before", "expiry after", "cluster after", "drain after", "node after", "logical cluster mismatch", "runner mismatch", "target mismatch", "cancelled during HTTP"} {
		t.Run(mode, func(t *testing.T) {
			f := newAutomaticRecoveryPostgresFixture(t)
			r := f.reserve(t)
			b, objects := automaticRecoveryApplicationFixture()
			originalName := b.Node.NodeName
			b.Node.ClusterID, b.Node.NodeID, b.Node.NodeName, b.Node.NodeUID, b.Node.Controller = r.ClusterID, r.NodeID, r.NodeName, r.NodeUID, r.Controller
			// Rebind only fixture values to this SQL reservation's original node.
			var rebind func(any)
			rebind = func(value any) {
				switch value := value.(type) {
				case map[string]any:
					for key, child := range value {
						if text, ok := child.(string); ok && text == originalName {
							value[key] = b.Node.NodeName
						} else {
							rebind(child)
						}
					}
				case []any:
					for _, child := range value {
						rebind(child)
					}
				}
			}
			for _, object := range objects {
				rebind(object)
			}
			node := objects["/api/v1/nodes/"+originalName]
			delete(objects, "/api/v1/nodes/"+originalName)
			objects["/api/v1/nodes/"+b.Node.NodeName] = node
			clusterSSHStorageMap(node, "metadata")["uid"] = b.Node.NodeUID
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			var gets, probes atomic.Int32
			checkReleased := func() {
				if f.c.controller.store.db.Stats().InUse != 0 {
					t.Error("SQL connection retained during application observation HTTP")
				}
			}
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
					query, args = `UPDATE engine.cluster_nodes SET application_state='drained',drain_reason='operator' WHERE id=$1`, []any{r.NodeID}
				case "node after":
					query, args = `UPDATE engine.cluster_nodes SET node_name='replaced' WHERE id=$1`, []any{r.NodeID}
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
			if mode == "cluster after" {
				t.Cleanup(func() { f.exec(t, `UPDATE engine.cluster_state SET cluster_id=$1 WHERE id=1`, r.ClusterID) })
			}
			native := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				gets.Add(1)
				checkReleased()
				if request.Method != http.MethodGet {
					t.Error("native observation attempted mutation")
				}
				if mode == "native forbidden" {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				object, exists := objects[request.URL.Path]
				if !exists {
					t.Error("unbound Kubernetes request", request.URL.Path)
					http.NotFound(w, request)
					return
				}
				_ = json.NewEncoder(w).Encode(object)
			}))
			defer native.Close()
			application := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				call := probes.Add(1)
				checkReleased()
				if request.Method != http.MethodGet || request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
					t.Error("application observation carried mutation or credentials")
				}
				if call == 1 {
					if strings.HasSuffix(mode, " after") {
						changeAuthority()
					}
					if mode == "Pod drift" {
						clusterSSHStorageMap(objects["/api/v1/namespaces/borealis/pods/"+b.API.Pod], "metadata")["resourceVersion"] = "2"
					}
					if mode == "cancelled during HTTP" {
						cancel()
					}
				}
				if mode == "probe unhealthy" {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "phase": map[string]string{"/startup": "started", "/live": "live", "/ready": "ready"}[request.URL.Path]})
			}))
			defer application.Close()
			// Route independently bound private addresses only into this local fixture.
			transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				if address != net.JoinHostPort(b.PodIP, "5000") && address != net.JoinHostPort(b.ServiceIP, "5000") {
					t.Error("probe escaped bound application addresses", address)
				}
				return (&net.Dialer{}).DialContext(ctx, network, application.Listener.Addr().String())
			}}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			probe := func(ctx context.Context, address string, port int, path string) error {
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > clusterControllerLeaseAcquireTimeout {
					t.Error("application observation lost original five-second scope")
				}
				return recoveryApplicationHTTP(ctx, client, address, port, path)
			}
			runner := &kubernetesClusterStepRunner{controllerHolder: r.Controller, kube: &kubernetesAPIClient{baseURL: native.URL, httpClient: native.Client()}}
			f.c.controller.runner = runner
			if mode == "logical cluster mismatch" {
				b.Node.ClusterID = newClusterUUID()
			}
			if mode == "runner mismatch" {
				runner.controllerHolder = "replacement"
			}
			target := clusterControllerNode{ID: r.NodeID, Name: r.NodeName, ApplicationState: "active"}
			if mode == "target mismatch" {
				target.ID = newClusterUUID()
			}
			observer := f.c.applicationHealthObserverWithProbe(b, probe)
			if !textInSet(mode, "partial health", "probe unhealthy", "native forbidden", "Pod drift") {
				health, err := observer(ctx, target)
				if err == nil || health != (clusterAutomaticRecoveryHealth{}) {
					t.Fatal("changed authority retained application health", health, err)
				}
				if !strings.HasSuffix(mode, " after") && mode != "cancelled during HTTP" && (gets.Load() != 0 || probes.Load() != 0) {
					t.Fatal("unowned application observation reached HTTP")
				}
				return
			}
			seed, err := f.c.Observe(f.ctx, r.OperationID, f.observer(t, true))
			if err != nil || seed.HealthySince == 0 {
				t.Fatal("healthy fixture interval missing", seed, err)
			}
			next, err := f.c.Observe(f.ctx, r.OperationID, observer)
			if err != nil || next.Phase != "recovering" || next.HealthySince != 0 || next.RecoveredAt != 0 || next.RecoveryDeadline != r.RecoveryDeadline || next.NodeUID != r.NodeUID {
				t.Fatal("partial or unknown application proof advanced recovery", next, err)
			}
			if mode == "partial health" && (gets.Load() != 26 || probes.Load() != 4 || next.Reason != "required_health_unavailable") {
				t.Fatal("complete application observation missing or treated as full health", gets.Load(), probes.Load(), next.Reason)
			}
			if mode != "partial health" && next.Reason != "observation_unknown" {
				t.Fatal("failed application observation did not remain unknown", next.Reason)
			}
			f.assertInert(t, r.OperationID)
		})
	}
}
