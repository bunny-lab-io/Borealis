package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// Exercise the ordinary ownership/selection/intent path. Only Kubernetes HTTP
// is synthetic: native quorum Jobs and original prepared SQL custody remain the
// production implementations. Unavailable unrelated observers return ordinary
// Kubernetes errors rather than bypassing controller reconciliation methods.
func TestClusterSSHPreparedAdmissionControllerPostgresEntry(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "final native failure", "final cleanup failure"} {
		t.Run(mode, func(t *testing.T) {
			f, source, targets := newSSHPreparedAuthorityFixture(t, mode == "replacement")
			original, _, err := f.c.loadClusterSSHPreparedAuthority(f.ctx, f.op)
			if err != nil {
				t.Fatal(err)
			}
			nativeMode := ""
			if mode == "final native failure" {
				nativeMode = "native read failure"
			}
			n := newPreparedMembershipNativeForProof(t, source, original, targets, nativeMode, clusterbootstrap.MembershipQuorumProofVersion)
			n.before = func(ctx context.Context) error { return recoveryProgressSQLPoolAvailable(ctx, f) }
			n.closeHook = func(ctx context.Context, post int) error {
				if err := recoveryProgressSQLPoolAvailable(ctx, f); err != nil {
					return err
				}
				if mode == "final cleanup failure" && post == 12 {
					return errors.New("synthetic final native response closure failure")
				}
				return nil
			}
			base := n.runner.kube.httpClient.Transport
			collection := "/apis/engine.borealis.io/v1alpha1/namespaces/borealis/borealisclusteroperations"
			resource := collection + "/" + f.op.ID
			var mu sync.Mutex
			var object map[string]any
			var writes, unexpectedEffects atomic.Int32
			n.runner.kube.httpClient.Transport = bootstrapRoundTripper(func(request *http.Request) (*http.Response, error) {
				path := request.URL.Path
				if path == "/api/v1/nodes" || path == "/api/v1/namespaces/kube-system" || path == "/api/v1/namespaces/borealis/pods" || strings.HasPrefix(path, "/apis/batch/v1/namespaces/borealis/jobs") {
					return base.RoundTrip(request)
				}
				mu.Lock()
				defer mu.Unlock()
				code := http.StatusServiceUnavailable
				var result any = map[string]any{"apiVersion": "v1", "kind": "Status", "status": "Failure", "reason": "ServiceUnavailable", "message": "synthetic unrelated observer unavailable", "code": code}
				if path == collection || path == resource || path == resource+"/status" {
					switch request.Method {
					case http.MethodGet:
						if object == nil {
							code = http.StatusNotFound
							result = map[string]any{"apiVersion": "v1", "kind": "Status", "status": "Failure", "reason": "NotFound", "code": code}
						} else {
							code, result = http.StatusOK, object
						}
					case http.MethodPost:
						if path != collection || object != nil {
							return nil, errors.New("unexpected operation create")
						}
						if err := json.NewDecoder(request.Body).Decode(&object); err != nil {
							return nil, err
						}
						_ = request.Body.Close()
						metadata := clusterSSHStorageMap(object, "metadata")
						if metadata["name"] != f.op.ID || object["kind"] != "BorealisClusterOperation" {
							return nil, errors.New("foreign operation resource")
						}
						metadata["uid"], metadata["resourceVersion"], metadata["generation"] = newClusterUUID(), "1", float64(1)
						code, result = http.StatusCreated, object
						writes.Add(1)
					case http.MethodPatch:
						if object == nil {
							return nil, errors.New("operation patch before create")
						}
						var patch map[string]any
						if err := json.NewDecoder(request.Body).Decode(&patch); err != nil {
							return nil, err
						}
						_ = request.Body.Close()
						for key, value := range patch {
							object[key] = value
						}
						code, result = http.StatusOK, object
						writes.Add(1)
					default:
						return nil, errors.New("unexpected operation resource method")
					}
				} else if request.Method != http.MethodGet {
					unexpectedEffects.Add(1)
				}
				raw, err := json.Marshal(result)
				if err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(raw)), ContentLength: int64(len(raw)), Request: request}, nil
			})
			f.c.runner = n.runner
			f.c.maxIdleConnections = 1
			f.c.store.db.SetMaxOpenConns(1)
			if err := f.c.runOnce(f.ctx); err != nil {
				t.Fatal("normal controller admission entry", err)
			}
			var state, step, kind, payload, reason, pointer string
			var attempt int64
			if err := f.c.store.db.QueryRowContext(f.ctx, `SELECT o.state,o.current_step,o.kind,o.attempt,o.payload_json,COALESCE(o.error_text,''),COALESCE(c.active_operation_id,'') FROM engine.cluster_operations o CROSS JOIN engine.cluster_state c WHERE o.id=$1 AND c.id=1`, f.op.ID).Scan(&state, &step, &kind, &attempt, &payload, &reason, &pointer); err != nil {
				t.Fatal(err)
			}
			success := mode == "pair" || mode == "replacement"
			if state != "running" || kind != "ssh_onboarding" || attempt != f.op.Attempt || payload != original.payload || pointer != f.op.ID || writes.Load() < 2 || unexpectedEffects.Load() != 0 || n.active.Load() != 0 || f.c.store.db.Stats().InUse != 0 {
				t.Fatal("normal loop lost authority, intent or cleanup")
			}
			if success {
				if step != "apply_membership" || reason != "" || n.posts.Load() != 12 {
					t.Fatal("normal loop skipped complete native admission proof", step, reason, n.posts.Load())
				}
			} else {
				if step != clusterSSHPreparationOperationStep || reason != clusterSSHPreparedAdmissionBlocked {
					t.Fatal("failed native proof did not retain blocked preparation", step, reason)
				}
				posts, events := n.posts.Load(), f.events(t)
				if err := f.c.runOnce(f.ctx); err != nil {
					t.Fatal("blocked normal loop", err)
				}
				if n.posts.Load() != posts || f.events(t) != events || unexpectedEffects.Load() != 0 {
					t.Fatal("blocked normal loop replayed proof or admission")
				}
			}
			var prepared, claims int
			if err := f.c.store.db.QueryRowContext(f.ctx, `SELECT count(*) FILTER(WHERE state='prepared' AND current_step='preparation_complete'),count(*) FILTER(WHERE lease_holder<>'' OR lease_expires_at<>0) FROM engine.cluster_onboarding_targets WHERE operation_id=$1`, f.op.ID).Scan(&prepared, &claims); err != nil {
				t.Fatal(err)
			}
			if prepared != len(original.receipt.Targets) || claims != 0 {
				t.Fatal("normal admission entry reacquired preparation claims")
			}
		})
	}
}
