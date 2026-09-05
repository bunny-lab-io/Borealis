package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type blockingClusterControllerRunner struct {
	calls   atomic.Int64
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *blockingClusterControllerRunner) Run(ctx context.Context, _ clusterControllerOperation, _ clusterControllerStep, _ []clusterControllerNode) error {
	r.calls.Add(1)
	r.once.Do(func() { close(r.started) })
	select {
	case <-r.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestClusterControllerStepTimeoutCoversLongNodeActions(t *testing.T) {
	tests := []struct {
		step string
		want time.Duration
	}{
		{step: "apply_cluster_foundation", want: 95 * time.Minute},
		{step: "apply_membership", want: 3 * time.Hour},
		{step: "node:id:stage_revision_images", want: 65 * time.Minute},
		{step: "node:id:redeploy_revision", want: 65 * time.Minute},
		{step: "node:id:promote_candidate", want: 65 * time.Minute},
		{step: "node:id:fetch_release", want: 35 * time.Minute},
		{step: "node:id:prepare_restore", want: 15 * time.Minute},
	}
	for _, test := range tests {
		if got := clusterControllerStepTimeout(test.step); got != test.want {
			t.Fatalf("clusterControllerStepTimeout(%q)=%s want %s", test.step, got, test.want)
		}
	}
}

func TestClusterControllerStartupAndLivenessStayLocal(t *testing.T) {
	controller := &clusterController{}
	handler := controller.healthServer().Handler
	for _, path := range []string{"/startup", "/live"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status=%d want %d", path, recorder.Code, http.StatusOK)
		}
	}
}

func TestClusterControllerCandidateCannotAcquireOperationLease(t *testing.T) {
	t.Setenv("BOREALIS_CLUSTER_CONTROLLER_ELIGIBLE", "false")
	if clusterControllerEligible() {
		t.Fatal("isolated cluster-controller candidate remained eligible")
	}
	t.Setenv("BOREALIS_CLUSTER_CONTROLLER_ELIGIBLE", "true")
	if !clusterControllerEligible() {
		t.Fatal("promoted cluster controller did not regain eligibility")
	}
}

func TestClusterDatabaseRuntimeStateTracksConfiguredAndReadyInstances(t *testing.T) {
	state := clusterDatabaseRuntimeState(map[string]any{
		"spec": map[string]any{"instances": int64(3)},
		"status": map[string]any{
			"readyInstances": int64(2),
			"phase":          "Waiting for the instances to become active",
			"currentPrimary": "borealis-postgres-3",
		},
	}, 3)
	if state["configured_instances"] != int64(3) || state["ready_instances"] != int64(2) {
		t.Fatalf("unexpected database counts: %+v", state)
	}
	if state["fully_ready"] != false || state["durability_quorum"] != true {
		t.Fatalf("reduced redundancy semantics incorrect: %+v", state)
	}
	if got := clusterRuntimeDatabaseStatus("Healthy", state); got != "Degraded Database" {
		t.Fatalf("database degradation status=%q", got)
	}
	state["ready_instances"] = int64(3)
	state["fully_ready"] = true
	if got := clusterRuntimeDatabaseStatus("Degraded Database", state); got != "Healthy" {
		t.Fatalf("database recovery status=%q", got)
	}
	if got := clusterRuntimeDatabaseStatus("HMR Non-HA", state); got != "HMR Non-HA" {
		t.Fatalf("database observation overwrote HMR status=%q", got)
	}
}

func TestClusterControllerRejectsFiveNodeAdmissionBeforeMutation(t *testing.T) {
	operation := clusterControllerOperation{Payload: map[string]any{
		"admission_ids":    []any{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"},
		"node_names":       []any{"engine-4", "engine-5"},
		"baseline_release": "2026.08.24",
		"baseline_sha":     strings.Repeat("a", 40),
	}}
	runner := &kubernetesClusterStepRunner{}
	err := runner.admitPendingMembers(context.Background(), operation, make([]clusterControllerNode, 3))
	if err == nil || !strings.Contains(err.Error(), "future roadmap") {
		t.Fatalf("expected five-node controller admission fence, got %v", err)
	}
}

func TestClusterControllerAcceptsSingleDegradedReplacement(t *testing.T) {
	if err := validateCurrentReleaseAdmission(2, 1); err != nil {
		t.Fatalf("single degraded replacement rejected: %v", err)
	}
	if err := validateCurrentReleaseAdmission(1, 2); err != nil {
		t.Fatalf("one-to-three pair rejected: %v", err)
	}
	for _, counts := range [][2]int{{2, 2}, {3, 1}, {3, 2}} {
		if err := validateCurrentReleaseAdmission(counts[0], counts[1]); err == nil {
			t.Fatalf("unsupported admission accepted: active=%d pending=%d", counts[0], counts[1])
		}
	}
}

func TestSharedArtifactStorageExpandsAndVerifiesReplicaPerEngine(t *testing.T) {
	const volumeName = "pvc-60b09c5a-dda0-4bc8-a8db-009254605ffb"
	replicaCount := int64(1)
	patches := 0
	nodes := []clusterControllerNode{{Name: "borealis-engine-01"}, {Name: "borealis-engine-02"}, {Name: "borealis-engine-03"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/namespaces/borealis/persistentvolumeclaims/borealis-agent-artifacts":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"spec":   map[string]any{"volumeName": volumeName, "accessModes": []any{"ReadWriteMany"}},
				"status": map[string]any{"phase": "Bound"},
			})
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/persistentvolumes/"+volumeName:
			_ = json.NewEncoder(w).Encode(map[string]any{"spec": map[string]any{
				"claimRef":    map[string]any{"namespace": "borealis", "name": "borealis-agent-artifacts"},
				"accessModes": []any{"ReadWriteMany"},
				"csi":         map[string]any{"driver": "driver.longhorn.io", "volumeHandle": volumeName},
			}})
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/volumes/"+volumeName):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"spec": map[string]any{"numberOfReplicas": replicaCount, "accessMode": "rwx"},
				"status": map[string]any{
					"robustness":       "healthy",
					"kubernetesStatus": map[string]any{"namespace": "borealis", "pvcName": "borealis-agent-artifacts", "pvName": volumeName},
				},
			})
		case request.Method == http.MethodPatch && strings.HasSuffix(request.URL.Path, "/volumes/"+volumeName):
			patches++
			var patch map[string]any
			if err := json.NewDecoder(request.Body).Decode(&patch); err != nil {
				t.Fatal(err)
			}
			if got := coerceInt64(nestedMap(patch, "spec")["numberOfReplicas"]); got != clusterSharedArtifactReplicaCount {
				t.Fatalf("replica patch=%d", got)
			}
			replicaCount = clusterSharedArtifactReplicaCount
			_ = json.NewEncoder(w).Encode(patch)
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/replicas"):
			if request.URL.Query().Get("labelSelector") != "longhornvolume="+volumeName {
				t.Fatalf("unexpected replica selector %q", request.URL.RawQuery)
			}
			items := make([]any, 0, len(nodes))
			for _, node := range nodes {
				items = append(items, map[string]any{
					"spec":   map[string]any{"nodeID": node.Name, "failedAt": "", "healthyAt": "2026-08-26T15:00:00Z", "desireState": "running", "evictionRequested": false},
					"status": map[string]any{"currentState": "running"},
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
		default:
			t.Fatalf("unexpected Kubernetes request %s %s", request.Method, request.URL.String())
		}
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()}, namespace: "borealis"}
	if err := runner.reconcileSharedArtifactStorage(context.Background(), nodes); err == nil || !strings.Contains(err.Error(), "reconciliation started") {
		t.Fatalf("expected initial expansion, got %v", err)
	}
	if err := runner.reconcileSharedArtifactStorage(context.Background(), nodes); err != nil {
		t.Fatalf("expanded storage did not verify: %v", err)
	}
	if patches != 1 {
		t.Fatalf("replica expansion patches=%d", patches)
	}
}

func TestSharedArtifactStorageRetryProtectsSurvivorThenDownscalesExactly(t *testing.T) {
	const volumeName = "pvc-60b09c5a-dda0-4bc8-a8db-009254605ffb"
	replicaCount := int64(3)
	robustness := "degraded"
	patches := 0
	nodes := []clusterControllerNode{{Name: "borealis-engine-01"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/namespaces/borealis/persistentvolumeclaims/borealis-agent-artifacts":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"spec": map[string]any{"volumeName": volumeName, "accessModes": []any{"ReadWriteMany"}}, "status": map[string]any{"phase": "Bound"},
			})
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/persistentvolumes/"+volumeName:
			_ = json.NewEncoder(w).Encode(map[string]any{"spec": map[string]any{
				"claimRef": map[string]any{"namespace": "borealis", "name": "borealis-agent-artifacts"}, "accessModes": []any{"ReadWriteMany"},
				"csi": map[string]any{"driver": "driver.longhorn.io", "volumeHandle": volumeName},
			}})
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/volumes/"+volumeName):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"spec": map[string]any{"numberOfReplicas": replicaCount, "accessMode": "rwx"},
				"status": map[string]any{"robustness": robustness, "kubernetesStatus": map[string]any{
					"namespace": "borealis", "pvcName": "borealis-agent-artifacts", "pvName": volumeName,
				}},
			})
		case request.Method == http.MethodPatch && strings.HasSuffix(request.URL.Path, "/volumes/"+volumeName):
			patches++
			var patch map[string]any
			if err := json.NewDecoder(request.Body).Decode(&patch); err != nil {
				t.Fatal(err)
			}
			if got := coerceInt64(nestedMap(patch, "spec")["numberOfReplicas"]); got != 1 {
				t.Fatalf("downscale replica patch=%d", got)
			}
			replicaCount = 1
			robustness = "healthy"
			_ = json.NewEncoder(w).Encode(patch)
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/replicas"):
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{
				"spec":   map[string]any{"nodeID": nodes[0].Name, "failedAt": "", "healthyAt": "2026-08-26T15:00:00Z", "desireState": "running", "evictionRequested": false},
				"status": map[string]any{"currentState": "running"},
			}}})
		default:
			t.Fatalf("unexpected Kubernetes request %s %s", request.Method, request.URL.String())
		}
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{
		kube: &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()}, namespace: "borealis", jobPollInterval: time.Millisecond,
	}
	if err := runner.reconcileSharedArtifactStorageState(context.Background(), nodes, 1, false, false); err != nil {
		t.Fatalf("degraded retry rejected healthy survivor replica: %v", err)
	}
	if patches != 0 {
		t.Fatalf("retry safety check changed replica count before member removal")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runner.waitForSharedArtifactStorageState(ctx, nodes, 1, true, true); err != nil {
		t.Fatal(err)
	}
	if patches != 1 || replicaCount != 1 || robustness != "healthy" {
		t.Fatalf("shared artifact downscale incomplete: patches=%d replicas=%d robustness=%s", patches, replicaCount, robustness)
	}
}

func TestSharedArtifactStorageRejectsPVOwnershipMismatch(t *testing.T) {
	const volumeName = "pvc-60b09c5a-dda0-4bc8-a8db-009254605ffb"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests++
		switch request.URL.Path {
		case "/api/v1/namespaces/borealis/persistentvolumeclaims/borealis-agent-artifacts":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"spec": map[string]any{"volumeName": volumeName, "accessModes": []any{"ReadWriteMany"}}, "status": map[string]any{"phase": "Bound"},
			})
		case "/api/v1/persistentvolumes/" + volumeName:
			_ = json.NewEncoder(w).Encode(map[string]any{"spec": map[string]any{
				"claimRef": map[string]any{"namespace": "other", "name": "borealis-agent-artifacts"}, "accessModes": []any{"ReadWriteMany"},
				"csi": map[string]any{"driver": "driver.longhorn.io", "volumeHandle": volumeName},
			}})
		default:
			t.Fatalf("unexpected request after ownership mismatch: %s", request.URL.Path)
		}
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()}, namespace: "borealis"}
	nodes := []clusterControllerNode{{Name: "engine-1"}, {Name: "engine-2"}, {Name: "engine-3"}}
	if err := runner.reconcileSharedArtifactStorage(context.Background(), nodes); err == nil || !strings.Contains(err.Error(), "claim ownership") {
		t.Fatalf("expected PV ownership rejection, got %v", err)
	}
	if requests != 2 {
		t.Fatalf("ownership mismatch made %d Kubernetes requests", requests)
	}
}

func TestPlannedDisruptionRequiresSharedArtifactHAWhileRecoveryBypassesGate(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{"status": map[string]any{"phase": "Pending"}})
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{
		kube:            &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()},
		namespace:       "borealis",
		jobPollInterval: time.Millisecond,
	}
	nodes := []clusterControllerNode{{Name: "engine-1"}, {Name: "engine-2"}, {Name: "engine-3"}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if err := runner.Run(ctx, clusterControllerOperation{Kind: "hmr_start"}, clusterControllerStep{Name: "preflight"}, nodes); err == nil || !strings.Contains(err.Error(), "not failure-safe") {
		t.Fatalf("planned HMR did not enforce storage gate: %v", err)
	}
	requestCount := requests
	if err := runner.Run(context.Background(), clusterControllerOperation{Kind: "hmr_exit"}, clusterControllerStep{Name: "preflight"}, nodes); err != nil {
		t.Fatalf("HMR recovery was blocked by storage gate: %v", err)
	}
	if requests != requestCount {
		t.Fatalf("HMR recovery made %d storage requests", requests-requestCount)
	}
}

func TestCloudNativePGMembershipAllowsTransientReplacementSize(t *testing.T) {
	for _, size := range []int{1, 2, 3} {
		if err := validateCNPGMembershipSize(size); err != nil {
			t.Fatalf("supported CloudNativePG size %d rejected: %v", size, err)
		}
	}
	for _, size := range []int{0, 4, 5} {
		if err := validateCNPGMembershipSize(size); err == nil {
			t.Fatalf("unsupported CloudNativePG size %d accepted", size)
		}
	}
}

func TestClusterControllerLeaseGuardRenewsDuringLongStep(t *testing.T) {
	var renewals atomic.Int64
	guard := startClusterControllerLeaseGuard(context.Background(), 2*time.Millisecond, func(context.Context) (bool, error) {
		renewals.Add(1)
		return true, nil
	})
	defer guard.Close()
	deadline := time.After(time.Second)
	for renewals.Load() < 3 {
		select {
		case <-deadline:
			t.Fatalf("lease heartbeat stopped after %d renewals", renewals.Load())
		case <-time.After(time.Millisecond):
		}
	}
	if err := guard.Err(); err != nil || guard.Context().Err() != nil {
		t.Fatalf("healthy lease heartbeat canceled long step: err=%v context=%v", err, guard.Context().Err())
	}
}

func TestClusterControllerLeaseGuardToleratesTransientRenewalFailure(t *testing.T) {
	var attempts atomic.Int64
	guard := startClusterControllerLeaseGuardWithGrace(context.Background(), time.Millisecond, 20*time.Millisecond, func(context.Context) (bool, error) {
		if attempts.Add(1) <= 3 {
			return false, errors.New("temporary database switchover")
		}
		return true, nil
	})
	defer guard.Close()
	deadline := time.After(time.Second)
	for attempts.Load() < 5 {
		select {
		case <-deadline:
			t.Fatalf("lease heartbeat stopped after %d attempts", attempts.Load())
		case <-time.After(time.Millisecond):
		}
	}
	if err := guard.Err(); err != nil || guard.Context().Err() != nil {
		t.Fatalf("transient renewal failure canceled operation: err=%v context=%v", err, guard.Context().Err())
	}
}

func TestClusterControllerLeaseGuardCancelsStepOnOwnershipLoss(t *testing.T) {
	guard := startClusterControllerLeaseGuard(context.Background(), time.Millisecond, func(context.Context) (bool, error) {
		return false, nil
	})
	defer guard.Close()
	select {
	case <-guard.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("lease ownership loss did not cancel step context")
	}
	if !errors.Is(guard.Err(), errClusterControllerLeaseLost) || !errors.Is(context.Cause(guard.Context()), errClusterControllerLeaseLost) {
		t.Fatalf("unexpected lease loss cause: err=%v cause=%v", guard.Err(), context.Cause(guard.Context()))
	}
}

func TestClusterControllerLeaseGuardBoundsBlockedRenewalAndClose(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	returned := make(chan struct{})
	guard := startClusterControllerLeaseGuardWithGrace(context.Background(), time.Millisecond, 250*time.Millisecond, func(context.Context) (bool, error) {
		close(entered)
		<-release // Deliberately ignore cancellation like the recorded driver failure.
		defer close(returned)
		return true, nil
	})
	defer func() {
		guard.Close()
		close(release)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("renewal did not start before expiry")
	}
	select {
	case <-guard.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("blocked renewal disabled independent expiry watchdog")
	}
	closed := make(chan struct{})
	go func() { guard.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("guard shutdown waited for blocked renewal")
	}
	if !errors.Is(guard.Err(), errClusterControllerLeaseLost) {
		t.Fatalf("expiry cause=%v", guard.Err())
	}
}

func TestPostgresPrimaryTransferRejectsReadyButUnsynchronizedReplica(t *testing.T) {
	patches := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/clusters/borealis-postgres"):
			_ = json.NewEncoder(w).Encode(map[string]any{"status": map[string]any{
				"currentPrimary": "borealis-postgres-1",
				"targetPrimary":  "borealis-postgres-1",
				"phase":          "Cluster in healthy state",
			}})
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/pods"):
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{
				"metadata": map[string]any{"name": "borealis-postgres-2"},
				"status": map[string]any{"conditions": []any{map[string]any{
					"type": "Ready", "status": "True",
				}}},
			}}})
		case request.Method == http.MethodPatch:
			patches++
			t.Fatalf("unsynchronized replica received primary patch")
		default:
			t.Fatalf("unexpected Kubernetes request %s %s", request.Method, request.URL.String())
		}
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{
		kube:                     &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()},
		namespace:                "borealis",
		jobPollInterval:          time.Millisecond,
		postgresReplicationProbe: func(context.Context, string, int64) (bool, error) { return false, nil },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	err := runner.ensurePostgresPrimaryOnNode(ctx, "borealis-engine-02")
	if err == nil || !strings.Contains(err.Error(), "did not become a synchronized streaming quorum candidate") {
		t.Fatalf("expected synchronization rejection, got %v", err)
	}
	if patches != 0 {
		t.Fatalf("unsynchronized replica primary patches=%d", patches)
	}
}

func TestPostgresPrimaryTransferWaitsForSynchronizedCluster(t *testing.T) {
	current := "borealis-postgres-1"
	requested := current
	patches := 0
	transitionReads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/clusters/borealis-postgres"):
			if requested != current && transitionReads > 0 {
				current = requested
			}
			if requested != current {
				transitionReads++
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"spec": map[string]any{"instances": int64(3)},
				"status": map[string]any{
					"currentPrimary": current,
					"targetPrimary":  requested,
					"readyInstances": int64(3),
					"phase":          "Cluster in healthy state",
				},
			})
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/pods"):
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{
				"metadata": map[string]any{"name": "borealis-postgres-2"},
				"status": map[string]any{"conditions": []any{map[string]any{
					"type": "Ready", "status": "True",
				}}},
			}}})
		case request.Method == http.MethodPatch && strings.HasSuffix(request.URL.Path, "/clusters/borealis-postgres/status"):
			patches++
			requested = "borealis-postgres-2"
			_ = json.NewEncoder(w).Encode(map[string]any{"status": map[string]any{"currentPrimary": current, "targetPrimary": requested}})
		default:
			t.Fatalf("unexpected Kubernetes request %s %s", request.Method, request.URL.String())
		}
	}))
	defer server.Close()
	probes := make([]string, 0, 2)
	runner := &kubernetesClusterStepRunner{
		kube:            &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()},
		namespace:       "borealis",
		jobPollInterval: time.Millisecond,
		postgresReplicationProbe: func(_ context.Context, target string, expectedReplicas int64) (bool, error) {
			probes = append(probes, fmt.Sprintf("%s:%d", target, expectedReplicas))
			return true, nil
		},
	}
	if err := runner.ensurePostgresPrimaryOnNode(context.Background(), "borealis-engine-02"); err != nil {
		t.Fatal(err)
	}
	if patches != 1 {
		t.Fatalf("synchronized replica primary patches=%d", patches)
	}
	if transitionReads != 1 {
		t.Fatalf("CloudNativePG transition reads=%d want 1", transitionReads)
	}
	if strings.Join(probes, ",") != "borealis-postgres-2:0,:2" {
		t.Fatalf("unexpected replication probes %v", probes)
	}
}

func TestSafeRemovalMovesPostgresPrimaryToSurvivorBeforeScaleDown(t *testing.T) {
	nodes := []clusterControllerNode{
		{ID: "11111111-1111-4111-8111-111111111111", Name: "borealis-engine-01", ApplicationState: "active"},
		{ID: "22222222-2222-4222-8222-222222222222", Name: "borealis-engine-02", ApplicationState: "active"},
		{ID: "33333333-3333-4333-8333-333333333333", Name: "borealis-engine-03", ApplicationState: "active"},
	}
	operation := clusterControllerOperation{Kind: "node_remove", TargetNodeID: nodes[1].ID, Payload: map[string]any{
		"emergency": false, "removal_node_ids": []any{nodes[1].ID, nodes[2].ID}, "target_size": int64(1),
	}}
	primary := "borealis-postgres-3"
	scaleRequested := false
	actions := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/clusters/borealis-postgres"):
			instances := int64(3)
			if scaleRequested {
				instances = 1
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"spec": map[string]any{"instances": instances},
				"status": map[string]any{
					"currentPrimary": primary,
					"targetPrimary":  primary,
					"instances":      instances,
					"readyInstances": instances,
					"phase":          "Cluster in healthy state",
				},
			})
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/pods") && request.URL.Query().Get("fieldSelector") != "":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{
				"metadata": map[string]any{"name": "borealis-postgres-1"},
				"status":   map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "True"}}},
			}}})
		case request.Method == http.MethodPatch && strings.HasSuffix(request.URL.Path, "/clusters/borealis-postgres/status"):
			if scaleRequested {
				t.Fatal("PostgreSQL switchover occurred after scale-down")
			}
			actions = append(actions, "switchover")
			primary = "borealis-postgres-1"
			_ = json.NewEncoder(w).Encode(map[string]any{"status": map[string]any{"currentPrimary": primary, "targetPrimary": primary}})
		case request.Method == http.MethodPatch && strings.HasSuffix(request.URL.Path, "/clusters/borealis-postgres"):
			if primary != "borealis-postgres-1" {
				t.Fatalf("PostgreSQL scaled before primary reached survivor: %s", primary)
			}
			actions = append(actions, "scale")
			scaleRequested = true
			_ = json.NewEncoder(w).Encode(map[string]any{"spec": map[string]any{"instances": int64(1)}})
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/pods"):
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{
				"metadata": map[string]any{"name": "borealis-postgres-1"},
				"spec":     map[string]any{"nodeName": "borealis-engine-01"},
			}}})
		default:
			t.Fatalf("unexpected Kubernetes request %s %s", request.Method, request.URL.String())
		}
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{
		kube:            &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()},
		namespace:       "borealis",
		jobPollInterval: time.Millisecond,
		postgresReplicationProbe: func(context.Context, string, int64) (bool, error) {
			return true, nil
		},
	}
	if err := runner.preparePostgresRemoval(context.Background(), operation, nodes); err != nil {
		t.Fatal(err)
	}
	if strings.Join(actions, ",") != "switchover,scale" {
		t.Fatalf("unsafe PostgreSQL removal order: %v", actions)
	}
}

func TestWaitNodeNotReadyRetriesTransientKubernetesFailure(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests++
		if requests == 1 {
			http.Error(w, "temporary control-plane handoff", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": map[string]any{"conditions": []any{map[string]any{
			"type": "Ready", "status": "Unknown",
		}}}})
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{
		kube:            &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()},
		jobPollInterval: time.Millisecond,
	}
	if err := runner.waitNodeNotReady(context.Background(), "borealis-engine-02"); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("fence wait requests=%d want 2", requests)
	}
}

func TestPostgresClusterSynchronizationResolvesPrimaryPodFromNode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/clusters/borealis-postgres"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"spec": map[string]any{"instances": int64(3)},
				"status": map[string]any{
					"currentPrimary": "borealis-postgres-1",
					"targetPrimary":  "borealis-postgres-1",
					"readyInstances": int64(3),
					"phase":          "Cluster in healthy state",
				},
			})
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/pods/borealis-postgres-1"):
			_ = json.NewEncoder(w).Encode(map[string]any{"spec": map[string]any{"nodeName": "borealis-engine-01"}})
		default:
			t.Fatalf("unexpected Kubernetes request %s %s", request.Method, request.URL.String())
		}
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{
		kube:      &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()},
		namespace: "borealis",
		postgresReplicationProbe: func(_ context.Context, target string, expectedReplicas int64) (bool, error) {
			if target != "" || expectedReplicas != 2 {
				t.Fatalf("replication probe target=%q replicas=%d", target, expectedReplicas)
			}
			return true, nil
		},
	}
	if err := runner.waitPostgresClusterSynchronizedOnNode(context.Background(), "borealis-engine-01"); err != nil {
		t.Fatal(err)
	}
}

func TestClusterControllerPostgresLeaseSerializesLongStep(t *testing.T) {
	databaseURL := os.Getenv("BOREALIS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("BOREALIS_TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store := &postgresOperatorStore{db: db}
	if err := store.ensureClusterSchema(ctx); err != nil {
		t.Fatal(err)
	}
	operationID := "8ad68810-bced-453f-a1cb-6585ec4cb383"
	clusterID := "5bcc1c7b-81d8-43c4-8faf-2f7b628f1f0d"
	base := time.Now().UTC().Unix()
	cleanup := func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_operation_events WHERE operation_id=$1`, operationID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_operations WHERE id=$1`, operationID)
		_, _ = db.ExecContext(context.Background(), `UPDATE engine.cluster_state SET active_operation_id=NULL WHERE id=1 AND active_operation_id=$1`, operationID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_application_leases WHERE name=$1`, clusterControllerLeaseName)
	}
	cleanup()
	defer cleanup()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO engine.cluster_state(id,cluster_id,enabled,status,active_size,desired_size,hmr_state,active_operation_id,config_json,created_at,updated_at)
		VALUES(1,$1,1,'Healthy',1,1,'inactive',$2,'{}',$3,$3)
		ON CONFLICT(id) DO UPDATE SET status='Healthy',active_operation_id=EXCLUDED.active_operation_id,updated_at=EXCLUDED.updated_at
	`, clusterID, operationID, base); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO engine.cluster_operations(id,kind,state,current_step,requested_by,payload_json,attempt,created_at,updated_at)
		VALUES($1,'membership_admit','queued','preflight','lease-test','{}',1,$2,$2)
	`, operationID, base); err != nil {
		t.Fatal(err)
	}

	firstRunner := &blockingClusterControllerRunner{started: make(chan struct{}), release: make(chan struct{})}
	secondRunner := &blockingClusterControllerRunner{started: make(chan struct{}), release: make(chan struct{})}
	var logicalClock atomic.Int64
	first := &clusterController{
		store: store, runner: firstRunner, holder: "controller-one", leaseRenewInterval: 5 * time.Millisecond,
		now: func() time.Time { return time.Unix(base+logicalClock.Add(1), 0).UTC() },
	}
	second := &clusterController{
		store: store, runner: secondRunner, holder: "controller-two", leaseRenewInterval: 5 * time.Millisecond,
		now: func() time.Time { return time.Unix(base+logicalClock.Load(), 0).UTC() },
	}
	firstDone := make(chan error, 1)
	go func() { firstDone <- first.runOnce(ctx) }()
	select {
	case <-firstRunner.started:
	case <-ctx.Done():
		t.Fatal("first controller did not start operation step")
	}
	for {
		var updatedAt int64
		if err := db.QueryRowContext(ctx, `SELECT updated_at FROM engine.cluster_application_leases WHERE name=$1`, clusterControllerLeaseName).Scan(&updatedAt); err != nil {
			t.Fatal(err)
		}
		if updatedAt > base+1 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("controller lease was not renewed during blocked step")
		case <-time.After(time.Millisecond):
		}
	}
	if err := second.runOnce(ctx); err != nil {
		t.Fatalf("standby controller lease check failed: %v", err)
	}
	if secondRunner.calls.Load() != 0 {
		t.Fatalf("standby controller executed owned operation %d time(s)", secondRunner.calls.Load())
	}
	close(firstRunner.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("lease-owning controller failed long step: %v", err)
	}
	var state, step string
	if err := db.QueryRowContext(ctx, `SELECT state,current_step FROM engine.cluster_operations WHERE id=$1`, operationID).Scan(&state, &step); err != nil {
		t.Fatal(err)
	}
	if state != "running" || step != "apply_membership" || firstRunner.calls.Load() != 1 {
		t.Fatalf("unexpected operation state=%s step=%s first_calls=%d", state, step, firstRunner.calls.Load())
	}
}

func TestClusterMembershipStorePostgresReleaseFences(t *testing.T) {
	databaseURL := os.Getenv("BOREALIS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("BOREALIS_TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store := &postgresOperatorStore{db: db}
	if err := store.ensureClusterSchema(ctx); err != nil {
		t.Fatal(err)
	}
	const (
		clusterID     = "7d8cfa65-2897-4516-92de-9be63ae7815a"
		invitationID  = "52e26bea-39c6-4ff5-91da-d5d457141d79"
		secondInvite  = "fe148f04-44bc-4964-b0f6-a4a3a7ccc958"
		thirdInvite   = "ab3e5345-61fe-434a-9451-e6a2071140c1"
		firstPending  = "cb9f2c44-fc43-472c-ad6b-c39b162614b5"
		secondPending = "2cd2de2e-a79d-4282-bf0f-68b37b544044"
	)
	replacementOperationID := ""
	cleanup := func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_operation_events WHERE admission_id IN ($1,$2)`, firstPending, secondPending)
		if replacementOperationID != "" {
			_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_operation_events WHERE operation_id=$1`, replacementOperationID)
			_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_operations WHERE id=$1`, replacementOperationID)
		}
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_admissions WHERE id IN ($1,$2)`, firstPending, secondPending)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_invitations WHERE id IN ($1,$2,$3)`, invitationID, secondInvite, thirdInvite)
		_, _ = db.ExecContext(context.Background(), `UPDATE engine.cluster_state SET active_size=1,desired_size=1,active_operation_id=NULL WHERE id=1`)
	}
	cleanup()
	defer cleanup()
	now := time.Now().UTC().Unix()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO engine.cluster_state(id,cluster_id,enabled,status,active_size,desired_size,hmr_state,active_operation_id,baseline_release,baseline_sha,config_json,created_at,updated_at)
		VALUES(1,$1,1,'Healthy',3,3,'inactive',NULL,'2026.08.24',$2,'{"k3s_version":"v1.36.3+k3s1"}',$3,$3)
		ON CONFLICT(id) DO UPDATE SET cluster_id=EXCLUDED.cluster_id,enabled=1,status='Healthy',active_size=3,desired_size=3,hmr_state='inactive',active_operation_id=NULL,baseline_release=EXCLUDED.baseline_release,baseline_sha=EXCLUDED.baseline_sha,config_json=EXCLUDED.config_json,updated_at=EXCLUDED.updated_at
	`, clusterID, strings.Repeat("a", 40), now); err != nil {
		t.Fatal(err)
	}
	invitation := map[string]any{"id": invitationID, "cluster_id": clusterID, "node_name": "engine-2", "token_hash": "membership-fence-token-one", "expires_at": now + 600}
	if err := store.createClusterInvitation(ctx, "operator", invitation); !errors.Is(err, errClusterConflict) {
		t.Fatalf("three-node store accepted new invitation: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE engine.cluster_state SET active_size=1,desired_size=3 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := store.createClusterInvitation(ctx, "operator", invitation); err != nil {
		t.Fatalf("one-to-three store rejected invitation: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE engine.cluster_state SET active_size=3,desired_size=3 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	admission := map[string]any{"id": firstPending, "invitation_id": invitationID, "cluster_id": clusterID, "token_hash": invitation["token_hash"], "node_name": "engine-2", "hostname": "engine-2", "management_ip": "10.20.30.22", "architecture": "amd64", "os_version": "Ubuntu 24.04"}
	if _, err := store.consumeClusterInvitation(ctx, admission); !errors.Is(err, errClusterConflict) {
		t.Fatalf("stale invitation bypassed three-node fence: %v", err)
	}
	var consumedAt sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT consumed_at FROM engine.cluster_invitations WHERE id=$1`, invitationID).Scan(&consumedAt); err != nil || consumedAt.Valid {
		t.Fatalf("rejected invitation was consumed: consumed=%v err=%v", consumedAt.Valid, err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO engine.cluster_invitations(id,cluster_id,node_name,token_hash,created_by,expires_at,created_at)
		VALUES($1,$3,'engine-3','membership-fence-token-two','operator',$4,$5),($2,$3,'engine-4','membership-fence-token-three','operator',$4,$5)
	`, secondInvite, thirdInvite, clusterID, now+600, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO engine.cluster_admissions(id,invitation_id,cluster_id,node_name,hostname,management_ip,architecture,os_version,state,created_at,updated_at)
		VALUES($1,$2,$5,'engine-3','engine-3','192.0.2.23','amd64','Ubuntu 24.04','Pending Quorum',$6,$6),($3,$4,$5,'engine-4','engine-4','192.0.2.24','amd64','Ubuntu 24.04','Pending Quorum',$6,$6)
	`, firstPending, secondInvite, secondPending, thirdInvite, clusterID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.approveClusterAdmission(ctx, "operator", firstPending); !errors.Is(err, errClusterConflict) {
		t.Fatalf("stale admissions bypassed three-node fence: %v", err)
	}
	var pendingCount int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM engine.cluster_admissions WHERE id IN ($1,$2) AND state='Pending Quorum'`, firstPending, secondPending).Scan(&pendingCount); err != nil || pendingCount != 2 {
		t.Fatalf("rejected admissions changed state: pending=%d err=%v", pendingCount, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE engine.cluster_state SET status='Degraded Quorum',active_size=2,desired_size=3,active_operation_id=NULL WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	result, err := store.approveClusterAdmission(ctx, "operator", firstPending)
	if err != nil {
		t.Fatalf("degraded-quorum replacement approval failed: %v", err)
	}
	replacementOperationID = cleanText(result["operation_id"])
	approvedIDs, ok := result["admission_ids"].([]string)
	if !ok || len(approvedIDs) != 1 || approvedIDs[0] != firstPending {
		t.Fatalf("replacement approval did not select exactly one node: %+v", result)
	}
	var firstState, secondState string
	if err := db.QueryRowContext(ctx, `SELECT state FROM engine.cluster_admissions WHERE id=$1`, firstPending).Scan(&firstState); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT state FROM engine.cluster_admissions WHERE id=$1`, secondPending).Scan(&secondState); err != nil {
		t.Fatal(err)
	}
	if firstState != "Approved" || secondState != "Pending Quorum" {
		t.Fatalf("replacement approval touched wrong admission set: first=%s second=%s", firstState, secondState)
	}
}

func TestClusterMaintenanceStorePostgresRecoveryGates(t *testing.T) {
	databaseURL := os.Getenv("BOREALIS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("BOREALIS_TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store := &postgresOperatorStore{db: db}
	if err := store.ensureClusterSchema(ctx); err != nil {
		t.Fatal(err)
	}
	const (
		clusterID   = "8f19197a-320f-466e-9ff8-1bebdf1315e7"
		nodeOneID   = "a7b4ef2d-3c70-4e47-a258-e5fa961e2405"
		nodeTwoID   = "ce1cd384-bcb9-47e3-b33a-d7a4d3df641c"
		nodeThreeID = "edfb0f66-a7e4-44a0-8250-90b38d49b97f"
		actor       = "cluster-maintenance-gate-test"
	)
	operationID := ""
	cleanup := func() {
		if operationID != "" {
			_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_operation_events WHERE operation_id=$1`, operationID)
			_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_operations WHERE id=$1`, operationID)
		}
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_audit_events WHERE actor=$1`, actor)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_nodes WHERE id IN ($1,$2,$3)`, nodeOneID, nodeTwoID, nodeThreeID)
		_, _ = db.ExecContext(context.Background(), `UPDATE engine.cluster_state SET status='Healthy',active_size=1,desired_size=1,active_operation_id=NULL,config_json='{}' WHERE id=1`)
	}
	cleanup()
	defer cleanup()
	now := time.Now().UTC().Unix()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO engine.cluster_state(id,cluster_id,enabled,status,active_size,desired_size,hmr_state,active_operation_id,config_json,created_at,updated_at)
		VALUES(1,$1,1,'Healthy',3,3,'inactive',NULL,'{"database_runtime":{"fully_ready":true,"durability_quorum":true}}',$2,$2)
		ON CONFLICT(id) DO UPDATE SET cluster_id=EXCLUDED.cluster_id,enabled=1,status='Healthy',active_size=3,desired_size=3,hmr_state='inactive',hmr_node_id=NULL,active_operation_id=NULL,config_json=EXCLUDED.config_json,updated_at=EXCLUDED.updated_at
	`, clusterID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO engine.cluster_nodes(id,node_name,hostname,management_ip,architecture,os_version,membership_state,application_state,roles_json,probe_health_json,created_at,updated_at)
		VALUES
			($1,'maintenance-engine-1','maintenance-engine-1','192.0.2.31','amd64','Ubuntu 24.04','Active','active','{}','{}',$4,$4),
			($2,'maintenance-engine-2','maintenance-engine-2','192.0.2.32','amd64','Ubuntu 24.04','Active','drained','{}','{}',$4,$4),
			($3,'maintenance-engine-3','maintenance-engine-3','192.0.2.33','amd64','Ubuntu 24.04','Active','active','{}','{}',$4,$4)
	`, nodeOneID, nodeTwoID, nodeThreeID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.createClusterOperation(ctx, actor, clusterMutation{Kind: "node_maintenance", TargetNodeID: nodeOneID, Payload: map[string]any{"action": "enter"}}); !errors.Is(err, errClusterConflict) {
		t.Fatalf("second application drain was accepted: %v", err)
	}
	result, err := store.createClusterOperation(ctx, actor, clusterMutation{Kind: "node_maintenance", TargetNodeID: nodeTwoID, Payload: map[string]any{"action": "exit"}})
	if err != nil {
		t.Fatalf("drained-node recovery was rejected: %v", err)
	}
	operationID = cleanText(result["operation_id"])
	if operationID == "" {
		t.Fatalf("maintenance recovery operation missing: %+v", result)
	}
	if _, err := db.ExecContext(ctx, `UPDATE engine.cluster_state SET active_operation_id=NULL,status='Mixed Version',config_json='{"database_runtime":{"fully_ready":false,"durability_quorum":true}}' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE engine.cluster_nodes SET application_state='active' WHERE id=$1`, nodeTwoID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.createClusterOperation(ctx, actor, clusterMutation{Kind: "node_maintenance", TargetNodeID: nodeOneID, Payload: map[string]any{"action": "enter"}}); !errors.Is(err, errClusterConflict) {
		t.Fatalf("mixed-version status masked database degradation: %v", err)
	}
}

func TestClusterCompletionPostgresPreservesAndClearsRecoveryState(t *testing.T) {
	databaseURL := os.Getenv("BOREALIS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("BOREALIS_TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store := &postgresOperatorStore{db: db}
	if err := store.ensureClusterSchema(ctx); err != nil {
		t.Fatal(err)
	}
	const (
		clusterID       = "650ab7f3-bdf8-4b5e-aecc-cd1feab1b9c9"
		failoverID      = "13e4fa3a-cfd1-4141-aef0-769a7832c124"
		maintenanceID   = "9bf355ab-9665-4052-aa39-e9f46def5d14"
		maintenanceNode = "59ab117e-8e56-4088-b127-cef9ca41191c"
		holder          = "cluster-completion-recovery-test"
	)
	cleanup := func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_operation_events WHERE operation_id IN ($1,$2)`, failoverID, maintenanceID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_operations WHERE id IN ($1,$2)`, failoverID, maintenanceID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_nodes WHERE id=$1`, maintenanceNode)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_application_leases WHERE name=$1 AND holder=$2`, clusterControllerLeaseName, holder)
		_, _ = db.ExecContext(context.Background(), `UPDATE engine.cluster_state SET status='Healthy',active_size=1,desired_size=1,active_operation_id=NULL,config_json='{}' WHERE id=1`)
	}
	cleanup()
	defer cleanup()
	now := time.Now().UTC().Unix()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO engine.cluster_state(id,cluster_id,enabled,status,active_size,desired_size,hmr_state,active_operation_id,config_json,created_at,updated_at)
		VALUES(1,$1,1,'Degraded Quorum',2,3,'inactive',$2,'{"database_runtime":{"fully_ready":true,"durability_quorum":true}}',$3,$3)
		ON CONFLICT(id) DO UPDATE SET cluster_id=EXCLUDED.cluster_id,enabled=1,status='Degraded Quorum',active_size=2,desired_size=3,hmr_state='inactive',hmr_node_id=NULL,active_operation_id=$2,config_json=EXCLUDED.config_json,updated_at=EXCLUDED.updated_at
	`, clusterID, failoverID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO engine.cluster_operations(id,kind,state,current_step,requested_by,payload_json,created_at,updated_at) VALUES($1,'postgres_emergency_failover','running','verify_postgres',$2,'{}',$3,$3)`, failoverID, holder, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO engine.cluster_application_leases(name,holder,expires_at,updated_at) VALUES($1,$2,$3,$4) ON CONFLICT(name) DO UPDATE SET holder=EXCLUDED.holder,expires_at=EXCLUDED.expires_at,updated_at=EXCLUDED.updated_at`, clusterControllerLeaseName, holder, now+60, now); err != nil {
		t.Fatal(err)
	}
	controller := &clusterController{store: store, holder: holder, now: func() time.Time { return time.Unix(now+1, 0).UTC() }}
	if err := controller.completeOperation(ctx, clusterControllerOperation{ID: failoverID, Kind: "postgres_emergency_failover"}, nil); err != nil {
		t.Fatalf("complete degraded failover: %v", err)
	}
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM engine.cluster_state WHERE id=1`).Scan(&status); err != nil || status != "Degraded Quorum" {
		t.Fatalf("two-of-three failover completion status=%q err=%v", status, err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO engine.cluster_nodes(id,node_name,hostname,management_ip,architecture,os_version,membership_state,application_state,roles_json,probe_health_json,created_at,updated_at)
		VALUES($1,'completion-engine-1','completion-engine-1','192.0.2.41','amd64','Ubuntu 24.04','Active','drained','{}','{}',$2,$2)
	`, maintenanceNode, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE engine.cluster_state SET status='Degraded Quorum',active_size=3,desired_size=3,active_operation_id=$1 WHERE id=1`, maintenanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO engine.cluster_operations(id,kind,state,current_step,target_node_id,requested_by,payload_json,created_at,updated_at) VALUES($1,'node_maintenance','running',$2,$3,$4,'{"action":"exit"}',$5,$5)`, maintenanceID, "node:"+maintenanceNode+":exit_drain", maintenanceNode, holder, now); err != nil {
		t.Fatal(err)
	}
	maintenance := clusterControllerOperation{ID: maintenanceID, Kind: "node_maintenance", TargetNodeID: maintenanceNode, Payload: map[string]any{"action": "exit"}}
	if err := controller.completeOperation(ctx, maintenance, nil); err != nil {
		t.Fatalf("complete maintenance recovery: %v", err)
	}
	var applicationState string
	if err := db.QueryRowContext(ctx, `SELECT status FROM engine.cluster_state WHERE id=1`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT application_state FROM engine.cluster_nodes WHERE id=$1`, maintenanceNode).Scan(&applicationState); err != nil {
		t.Fatal(err)
	}
	if status != "Healthy" || applicationState != "active" {
		t.Fatalf("maintenance recovery did not restore durable state: status=%q application=%q", status, applicationState)
	}
}

func TestClusterMembershipAdmissionReactivatesRetainedNodeIdentity(t *testing.T) {
	databaseURL := os.Getenv("BOREALIS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("BOREALIS_TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store := &postgresOperatorStore{db: db}
	if err := store.ensureClusterSchema(ctx); err != nil {
		t.Fatal(err)
	}
	const (
		clusterID         = "bda02778-144e-4943-a614-0358f40842ac"
		operationID       = "0db17acb-28fc-4b10-b029-c8ef61f728b1"
		survivorID        = "69374d32-a9c7-4dba-813c-df3089550119"
		retainedNodeTwoID = "0a01c416-26f5-4734-9a6f-373178a19918"
		retainedNodeTriID = "f7f9f965-342d-4ebc-882f-4af47ea0c643"
		admissionTwoID    = "306a1ca0-bd39-4137-aae0-bac90b51c510"
		admissionThreeID  = "7a322421-41d7-4d27-85f8-a8ad66daf042"
		invitationTwoID   = "c813a16e-d4a3-44df-85cc-8bd002d78af7"
		invitationTriID   = "f0e65df7-4b00-42eb-9511-e29f1df0ef36"
		holder            = "cluster-readmission-identity-test"
		baselineRelease   = "2026.08.27"
		baselineSHA       = "0f829bff08f578328db21d21f3673eb2e1db7993"
	)
	nodeNames := []string{"readmit-engine-02", "readmit-engine-03"}
	cleanup := func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_operation_events WHERE operation_id=$1`, operationID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_operations WHERE id=$1`, operationID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_admissions WHERE id IN ($1,$2)`, admissionTwoID, admissionThreeID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_nodes WHERE id IN ($1,$2,$3,$4,$5) OR node_name IN ($6,$7)`, survivorID, retainedNodeTwoID, retainedNodeTriID, admissionTwoID, admissionThreeID, nodeNames[0], nodeNames[1])
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_application_leases WHERE name=$1 AND holder=$2`, clusterControllerLeaseName, holder)
		_, _ = db.ExecContext(context.Background(), `UPDATE engine.cluster_state SET status='Healthy',active_size=1,desired_size=1,active_operation_id=NULL,config_json='{}' WHERE id=1`)
	}
	cleanup()
	defer cleanup()
	now := time.Now().UTC().Unix()
	createdAt := now - 100
	if _, err := db.ExecContext(ctx, `
		INSERT INTO engine.cluster_state(id,cluster_id,enabled,status,active_size,desired_size,hmr_state,active_operation_id,config_json,created_at,updated_at)
		VALUES(1,$1,1,'Degraded Quorum',1,1,'inactive',$2,'{}',$3,$3)
		ON CONFLICT(id) DO UPDATE SET cluster_id=EXCLUDED.cluster_id,enabled=1,status='Degraded Quorum',active_size=1,desired_size=1,hmr_state='inactive',hmr_node_id=NULL,active_operation_id=EXCLUDED.active_operation_id,config_json='{}',updated_at=EXCLUDED.updated_at
	`, clusterID, operationID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO engine.cluster_nodes(id,node_name,hostname,management_ip,architecture,os_version,membership_state,application_state,release_tag,release_sha,drain_reason,roles_json,probe_health_json,created_at,updated_at)
		VALUES
			($1,'readmit-engine-01','readmit-engine-01','192.0.2.51','amd64','Ubuntu 24.04','Active','active',$4,$5,NULL,'{}','{}',$6,$7),
			($2,$8,'retired-hostname-02','10.20.30.152','amd64','Ubuntu 22.04','Removed','drained','2026.08.1',$9,'safe_pair_removal','{}','{}',$6,$7),
			($3,$10,'retired-hostname-03','10.20.30.153','amd64','Ubuntu 22.04','Removed','drained','2026.08.1',$9,'safe_pair_removal','{}','{}',$6,$7)
	`, survivorID, retainedNodeTwoID, retainedNodeTriID, baselineRelease, baselineSHA, createdAt, now, nodeNames[0], strings.Repeat("b", 40), nodeNames[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO engine.cluster_admissions(id,invitation_id,cluster_id,node_name,hostname,management_ip,architecture,os_version,state,created_at,updated_at)
		VALUES
			($1,$2,$5,$6,'readmit-host-02','192.0.2.52','amd64','Ubuntu 24.04','Approved',$8,$8),
			($3,$4,$5,$7,'readmit-host-03','192.0.2.53','amd64','Ubuntu 24.04','Approved',$8,$8)
	`, admissionTwoID, invitationTwoID, admissionThreeID, invitationTriID, clusterID, nodeNames[0], nodeNames[1], now); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"admission_ids":    []any{admissionTwoID, admissionThreeID},
		"baseline_release": baselineRelease,
		"baseline_sha":     baselineSHA,
		"k3s_version":      "v1.36.3+k3s1",
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO engine.cluster_operations(id,kind,state,current_step,requested_by,payload_json,attempt,created_at,updated_at)
		VALUES($1,'membership_admit','running','verify_quorum',$2,$3,2,$4,$4)
	`, operationID, holder, marshalClusterJSON(payload), now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO engine.cluster_application_leases(name,holder,expires_at,updated_at)
		VALUES($1,$2,$3,$4)
		ON CONFLICT(name) DO UPDATE SET holder=EXCLUDED.holder,expires_at=EXCLUDED.expires_at,updated_at=EXCLUDED.updated_at
	`, clusterControllerLeaseName, holder, now+60, now); err != nil {
		t.Fatal(err)
	}
	controller := &clusterController{store: store, holder: holder, now: func() time.Time { return time.Unix(now+1, 0).UTC() }}
	operation := clusterControllerOperation{ID: operationID, Kind: "membership_admit", Payload: payload, Attempt: 2}
	if _, err := db.ExecContext(ctx, `UPDATE engine.cluster_nodes SET membership_state='Active' WHERE id=$1`, retainedNodeTwoID); err != nil {
		t.Fatal(err)
	}
	if err := controller.completeOperation(ctx, operation, nil); err == nil || !strings.Contains(err.Error(), "conflicts with non-removed durable identity") {
		t.Fatalf("active node-name collision did not fail closed: %v", err)
	}
	var approvedAdmissions int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM engine.cluster_admissions WHERE id IN ($1,$2) AND state='Approved'`, admissionTwoID, admissionThreeID).Scan(&approvedAdmissions); err != nil || approvedAdmissions != 2 {
		t.Fatalf("failed completion did not roll back admission state: approved=%d err=%v", approvedAdmissions, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE engine.cluster_nodes SET membership_state='Removed' WHERE id=$1`, retainedNodeTwoID); err != nil {
		t.Fatal(err)
	}
	if err := controller.completeOperation(ctx, operation, nil); err != nil {
		t.Fatalf("complete retained-node re-admission: %v", err)
	}

	expected := []struct {
		nodeName, retainedID, admissionID, hostname, managementIP string
	}{
		{nodeNames[0], retainedNodeTwoID, admissionTwoID, "readmit-host-02", "192.0.2.52"},
		{nodeNames[1], retainedNodeTriID, admissionThreeID, "readmit-host-03", "192.0.2.53"},
	}
	for _, want := range expected {
		var id, hostname, managementIP, architecture, osVersion, membershipState, applicationState, releaseTag, releaseSHA, drainReason, admissionState string
		var retainedCreatedAt int64
		if err := db.QueryRowContext(ctx, `
			SELECT id,hostname,management_ip,architecture,os_version,membership_state,application_state,COALESCE(release_tag,''),COALESCE(release_sha,''),COALESCE(drain_reason,''),created_at
			FROM engine.cluster_nodes WHERE node_name=$1
		`, want.nodeName).Scan(&id, &hostname, &managementIP, &architecture, &osVersion, &membershipState, &applicationState, &releaseTag, &releaseSHA, &drainReason, &retainedCreatedAt); err != nil {
			t.Fatal(err)
		}
		if id != want.retainedID || retainedCreatedAt != createdAt {
			t.Fatalf("node %s identity changed: id=%s created_at=%d", want.nodeName, id, retainedCreatedAt)
		}
		if hostname != want.hostname || managementIP != want.managementIP || architecture != "amd64" || osVersion != "Ubuntu 24.04" {
			t.Fatalf("node %s mutable identity was not refreshed: host=%s ip=%s architecture=%s os=%s", want.nodeName, hostname, managementIP, architecture, osVersion)
		}
		if membershipState != "Active" || applicationState != "active" || releaseTag != baselineRelease || releaseSHA != baselineSHA || drainReason != "" {
			t.Fatalf("node %s was not fully reactivated: membership=%s application=%s release=%s sha=%s drain=%q", want.nodeName, membershipState, applicationState, releaseTag, releaseSHA, drainReason)
		}
		var replacementRows int64
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM engine.cluster_nodes WHERE id=$1`, want.admissionID).Scan(&replacementRows); err != nil || replacementRows != 0 {
			t.Fatalf("admission UUID replaced retained node identity: rows=%d err=%v", replacementRows, err)
		}
		if err := db.QueryRowContext(ctx, `SELECT state FROM engine.cluster_admissions WHERE id=$1`, want.admissionID).Scan(&admissionState); err != nil || admissionState != "Admitted" {
			t.Fatalf("admission %s state=%q err=%v", want.admissionID, admissionState, err)
		}
	}
	var status, activeOperation, operationState, operationStep string
	var activeSize, desiredSize int64
	if err := db.QueryRowContext(ctx, `SELECT status,active_size,desired_size,COALESCE(active_operation_id,'') FROM engine.cluster_state WHERE id=1`).Scan(&status, &activeSize, &desiredSize, &activeOperation); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT state,current_step FROM engine.cluster_operations WHERE id=$1`, operationID).Scan(&operationState, &operationStep); err != nil {
		t.Fatal(err)
	}
	if status != "Healthy" || activeSize != 3 || desiredSize != 3 || activeOperation != "" || operationState != "succeeded" || operationStep != "complete" {
		t.Fatalf("re-admission did not finalize cluster: status=%s size=%d/%d active_operation=%q operation=%s/%s", status, activeSize, desiredSize, activeOperation, operationState, operationStep)
	}
}

func TestClusterNodeRuntimeStateSeparatesDesiredAndObservedState(t *testing.T) {
	node := clusterControllerNode{
		ID:               "11111111-1111-4111-8111-111111111111",
		Name:             "engine-1",
		ApplicationState: "active",
		ReleaseTag:       "2026.08.24",
		ReleaseSHA:       strings.Repeat("a", 40),
		DrainReason:      "update",
		Roles:            map[string]any{"edge_vip_owner": true},
		ProbeHealth:      map[string]any{"readiness": "passed"},
	}
	kubernetesNode := map[string]any{
		"metadata": map[string]any{"labels": map[string]any{"borealis.io/application-state": "drained"}},
		"status":   map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "False"}}},
	}
	spec, status, err := clusterNodeRuntimeState(node, kubernetesNode)
	if err != nil {
		t.Fatal(err)
	}
	if cleanText(spec["desiredApplicationState"]) != "active" || cleanText(status["observedApplicationState"]) != "drained" || status["nodeReady"] != false {
		t.Fatalf("desired/observed node runtime state collapsed: spec=%#v status=%#v", spec, status)
	}
	if cleanText(spec["desiredSHA"]) != node.ReleaseSHA || cleanText(nestedMap(status, "roles")["edge_vip_owner"]) != "true" {
		t.Fatalf("node runtime release/roles missing: spec=%#v status=%#v", spec, status)
	}
}

func TestUnexpectedK3sDrainFailsClosedOnlyAtSteadyState(t *testing.T) {
	node := clusterControllerNode{ID: "5feda1fc-c15a-4640-b0fb-6670944d3fc6", Name: "borealis-engine-02", ApplicationState: "active"}
	kubernetesNode := map[string]any{"metadata": map[string]any{"labels": map[string]any{"borealis.io/application-state": "drained"}}}
	updated, changed := clusterObservedDrainTransition(node, kubernetesNode, true)
	if !changed || updated.ApplicationState != "drained" || updated.DrainReason != "k3s_restart_label_drift" {
		t.Fatalf("unexpected steady-state drain transition: %#v changed=%v", updated, changed)
	}
	if _, changed := clusterObservedDrainTransition(node, kubernetesNode, false); changed {
		t.Fatal("active operation drain observation changed durable state")
	}
	alreadyDrained := node
	alreadyDrained.ApplicationState = "drained"
	activeKubernetesNode := map[string]any{"metadata": map[string]any{"labels": map[string]any{"borealis.io/application-state": "active"}}}
	if _, changed := clusterObservedDrainTransition(alreadyDrained, activeKubernetesNode, true); changed {
		t.Fatal("observed active label bypassed explicit recovery")
	}
}

func TestClusterCustomResourceStatesKeepDesiredAndRuntimeFieldsSeparate(t *testing.T) {
	clusterID := "11111111-1111-4111-8111-111111111111"
	operationID := "22222222-2222-4222-8222-222222222222"
	state := clusterControllerState{
		ClusterID:       clusterID,
		Enabled:         true,
		Status:          "Mixed Version",
		ActiveSize:      1,
		DesiredSize:     3,
		ControlPlaneVIP: "10.20.30.10",
		EdgeVIP:         "10.20.30.10",
		BaselineRelease: "2026.08.24",
		BaselineSHA:     strings.Repeat("a", 40),
		HMRState:        "inactive",
		ActiveOperation: operationID,
		Config:          map[string]any{"k3s_version": "v1.36.3+k3s1"},
	}
	spec, status, err := clusterResourceState(state)
	if err != nil {
		t.Fatal(err)
	}
	if coerceInt64(spec["activeSize"]) != 1 || coerceInt64(spec["desiredSize"]) != 3 || cleanText(spec["clusterVIP"]) != "10.20.30.10" {
		t.Fatalf("cluster desired sizes missing: %#v", spec)
	}
	replacement := state
	replacement.Status = "Degraded Quorum"
	replacement.ActiveSize = 2
	replacement.DesiredSize = 3
	if replacementSpec, _, err := clusterResourceState(replacement); err != nil || coerceInt64(replacementSpec["activeSize"]) != 2 {
		t.Fatalf("degraded two-of-three replacement state rejected: spec=%#v err=%v", replacementSpec, err)
	}
	replacement.Status = "Healthy"
	if _, _, err := clusterResourceState(replacement); err == nil {
		t.Fatal("healthy two-of-three custom-resource state accepted")
	}
	replacement.Status = "Degraded Quorum"
	replacement.DesiredSize = 2
	if _, _, err := clusterResourceState(replacement); err == nil {
		t.Fatal("steady two-node custom-resource state accepted")
	}
	replacement.ActiveSize = 3
	replacement.DesiredSize = 1
	if _, _, err := clusterResourceState(replacement); err == nil {
		t.Fatal("unsupported three-to-one transitional custom-resource state accepted")
	}
	state.ActiveSize = 3
	state.DesiredSize = 5
	if _, _, err := clusterResourceState(state); err == nil {
		t.Fatal("five-node custom-resource membership remained valid")
	}
	if cleanText(status["phase"]) != "Mixed Version" || cleanText(status["activeOperationID"]) != operationID {
		t.Fatalf("cluster runtime status missing: %#v", status)
	}
	conditions := anySlice(status["conditions"])
	if len(conditions) != 1 || cleanText(nestedMap(map[string]any{"condition": conditions[0]}, "condition")["status"]) != "True" {
		t.Fatalf("serving mixed-version cluster marked unavailable: %#v", status)
	}

	admission := clusterControllerAdmission{
		ID:           "33333333-3333-4333-8333-333333333333",
		NodeName:     "engine-2",
		Hostname:     "engine-2.example.test",
		ManagementIP: "10.20.30.12",
		Architecture: "amd64",
		OSVersion:    "Ubuntu 24.04",
		State:        "Approved",
		ApprovedBy:   "admin",
		ApprovedAt:   1787600000,
	}
	admissionSpec, admissionStatus, err := clusterAdmissionResourceState(admission)
	if err != nil {
		t.Fatal(err)
	}
	if cleanText(admissionSpec["managementIP"]) != admission.ManagementIP || cleanText(admissionStatus["state"]) != "Approved" {
		t.Fatalf("admission desired/runtime state missing: spec=%#v status=%#v", admissionSpec, admissionStatus)
	}

	operation := clusterControllerOperationResource{Operation: clusterControllerOperation{
		ID:            operationID,
		Kind:          "engine_update",
		State:         "running",
		CurrentStep:   "node:" + admission.ID + ":inspect_health",
		TargetNodeID:  admission.ID,
		TargetRelease: "2026.08.24",
		TargetSHA:     strings.Repeat("b", 40),
		Attempt:       2,
	}}
	operationSpec, operationStatus, err := clusterOperationResourceState(operation)
	if err != nil {
		t.Fatal(err)
	}
	if cleanText(operationSpec["step"]) != operation.Operation.CurrentStep || cleanText(operationStatus["state"]) != "running" || coerceInt64(operationStatus["attempt"]) != 2 {
		t.Fatalf("operation desired/runtime state missing: spec=%#v status=%#v", operationSpec, operationStatus)
	}
}

func TestClusterMergePatchRemovesControllerOwnedStaleFields(t *testing.T) {
	patch := clusterMergePatchMap(
		map[string]any{"state": "failed", "error": "old failure", "observedAt": int64(1)},
		map[string]any{"state": "running", "observedAt": int64(2)},
	)
	if cleanText(patch["state"]) != "running" || coerceInt64(patch["observedAt"]) != 2 || patch["error"] != nil {
		t.Fatalf("merge patch did not clear stale controller field: %#v", patch)
	}
}

func TestNodeRuntimeReconcileCreatesStatusAndAvoidsNoopWrites(t *testing.T) {
	var object map[string]any
	creates := 0
	specPatches := 0
	statusPatches := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && object == nil:
			http.Error(w, "missing", http.StatusNotFound)
		case request.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(object)
		case request.Method == http.MethodPost:
			creates++
			if err := json.NewDecoder(request.Body).Decode(&object); err != nil {
				t.Fatal(err)
			}
			_ = json.NewEncoder(w).Encode(object)
		case request.Method == http.MethodPatch && strings.HasSuffix(request.URL.Path, "/status"):
			statusPatches++
			var patch map[string]any
			if err := json.NewDecoder(request.Body).Decode(&patch); err != nil {
				t.Fatal(err)
			}
			object["status"] = patch["status"]
			_ = json.NewEncoder(w).Encode(object)
		case request.Method == http.MethodPatch:
			specPatches++
			var patch map[string]any
			if err := json.NewDecoder(request.Body).Decode(&patch); err != nil {
				t.Fatal(err)
			}
			currentSpec := nestedMap(object, "spec")
			for key, value := range nestedMap(patch, "spec") {
				if value == nil {
					delete(currentSpec, key)
				} else {
					currentSpec[key] = value
				}
			}
			object["spec"] = currentSpec
			_ = json.NewEncoder(w).Encode(object)
		default:
			t.Fatalf("unexpected Kubernetes request %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{
		kube:      &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()},
		namespace: "borealis",
	}
	node := clusterControllerNode{
		ID:               "11111111-1111-4111-8111-111111111111",
		Name:             "engine-1",
		ApplicationState: "active",
		ReleaseTag:       "2026.08.24",
		ReleaseSHA:       strings.Repeat("a", 40),
		Roles:            map[string]any{"scheduler_leader": true},
		ProbeHealth:      map[string]any{"readiness": "passed"},
	}
	kubernetesNode := map[string]any{
		"metadata": map[string]any{"labels": map[string]any{"borealis.io/application-state": "active"}},
		"status":   map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "True"}}},
	}
	if err := runner.reconcileNodeRuntime(context.Background(), node, kubernetesNode, 1787600000); err != nil {
		t.Fatal(err)
	}
	if err := runner.reconcileNodeRuntime(context.Background(), node, kubernetesNode, 1787600060); err != nil {
		t.Fatal(err)
	}
	node.ApplicationState = "drained"
	node.ReleaseTag = ""
	node.ReleaseSHA = ""
	if err := runner.reconcileNodeRuntime(context.Background(), node, kubernetesNode, 1787600120); err != nil {
		t.Fatal(err)
	}
	if creates != 1 || specPatches != 1 || statusPatches != 2 {
		t.Fatalf("node runtime writes create=%d spec=%d status=%d", creates, specPatches, statusPatches)
	}
	if _, present := nestedMap(object, "spec")["desiredRelease"]; present {
		t.Fatalf("stale optional desired release survived merge patch: %#v", object)
	}
	if _, present := nestedMap(object, "spec")["desiredReleaseChannel"]; present {
		t.Fatalf("stale optional desired release channel survived merge patch: %#v", object)
	}
}

func TestWaitJobToleratesTemporaryKubernetesAPIOutage(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests <= 2 {
			http.Error(w, "starting", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": map[string]any{"succeeded": 1}})
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{
		kube:            &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()},
		namespace:       "borealis",
		jobPollInterval: time.Millisecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runner.waitJob(ctx, "cluster-test"); err != nil {
		t.Fatalf("temporary API outage failed Job wait: %v", err)
	}
	if requests != 3 {
		t.Fatalf("unexpected Job poll count %d", requests)
	}
}

func TestTransientKubernetesAPIErrorAcceptsEOF(t *testing.T) {
	err := fmt.Errorf("read Kubernetes API response: %w", io.EOF)
	if !transientKubernetesAPIError(err) {
		t.Fatalf("wrapped EOF was not classified as transient: %v", err)
	}
}

func TestWaitJobRejectsPermanentKubernetesAPIError(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{
		kube:            &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()},
		namespace:       "borealis",
		jobPollInterval: time.Millisecond,
	}
	if err := runner.waitJob(context.Background(), "cluster-test"); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("expected permanent API error, got %v", err)
	}
	if requests != 1 {
		t.Fatalf("permanent API error retried %d times", requests)
	}
	certificateError := &url.Error{Op: http.MethodGet, URL: server.URL, Err: errors.New("x509: certificate signed by unknown authority")}
	if transientKubernetesAPIError(certificateError) {
		t.Fatal("permanent TLS trust error classified as transient")
	}
}

func TestClusterInitJobToleratesBoundedAuthorizationTransition(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests <= 2 {
			http.Error(w, "forbidden during datastore restart", http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": map[string]any{"succeeded": 1}})
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{
		kube:                          &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()},
		namespace:                     "borealis",
		jobPollInterval:               time.Millisecond,
		clusterInitAuthorizationGrace: 50 * time.Millisecond,
	}
	if err := runner.waitClusterInitJob(context.Background(), "cluster-test"); err != nil {
		t.Fatalf("cluster-init authorization transition failed Job wait: %v", err)
	}
	if requests != 3 {
		t.Fatalf("unexpected cluster-init Job poll count %d", requests)
	}
}

func TestClusterInitJobRejectsPersistentAuthorizationFailure(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{
		kube:                          &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()},
		namespace:                     "borealis",
		jobPollInterval:               time.Millisecond,
		clusterInitAuthorizationGrace: 3 * time.Millisecond,
	}
	if err := runner.waitClusterInitJob(context.Background(), "cluster-test"); err == nil || !strings.Contains(err.Error(), "authorization did not recover") {
		t.Fatalf("expected persistent cluster-init authorization failure, got %v", err)
	}
	if requests < 2 {
		t.Fatalf("cluster-init authorization failure was not retried: %d requests", requests)
	}
}

func TestNodeActionJobDoesNotCreateAfterAuthorizationFailure(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected mutating request after authorization failure: %s", r.Method)
		}
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{
		kube:        &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()},
		namespace:   "borealis",
		actionImage: "registry.example/borealis@sha256:" + strings.Repeat("a", 64),
	}
	err := runner.nodeActionJob(
		context.Background(),
		clusterControllerOperation{ID: "11111111-1111-4111-8111-111111111111", Attempt: 1},
		clusterControllerStep{Name: "inspect", NodeID: "22222222-2222-4222-8222-222222222222"},
		clusterControllerNode{ID: "22222222-2222-4222-8222-222222222222", Name: "engine-2"},
		"InspectHealth",
	)
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("expected authorization error, got %v", err)
	}
	if requests != 1 {
		t.Fatalf("authorization failure produced %d Kubernetes requests", requests)
	}
}

func TestClusterUpdateOrdersNonLeadersBeforeLeaders(t *testing.T) {
	nodes := []clusterControllerNode{
		{ID: "11111111-1111-4111-8111-111111111111", Name: "engine-1", Roles: map[string]any{"postgres_primary": true, "edge_vip_owner": true}},
		{ID: "22222222-2222-4222-8222-222222222222", Name: "engine-2", Roles: map[string]any{}},
		{ID: "33333333-3333-4333-8333-333333333333", Name: "engine-3", Roles: map[string]any{"scheduler_leader": true}},
	}
	operation := clusterControllerOperation{Kind: "engine_update", TargetRelease: "2026.08.23", TargetSHA: strings.Repeat("a", 40), Payload: map[string]any{"scope": "all", "compatibility": map[string]any{"database_migration": "expand-contract"}}}
	steps, err := clusterOperationSteps(operation, nodes)
	if err != nil {
		t.Fatalf("build steps: %v", err)
	}
	firstDrain := ""
	lastDrain := ""
	for _, step := range steps {
		if strings.HasSuffix(step.Name, ":enter_drain") {
			if firstDrain == "" {
				firstDrain = step.NodeID
			}
			lastDrain = step.NodeID
		}
	}
	if firstDrain != nodes[1].ID || lastDrain != nodes[0].ID {
		t.Fatalf("unsafe update order first=%s last=%s", firstDrain, lastDrain)
	}
}

func TestClusterUpdateKeepsPinnedNodeOrderAfterRoleMovement(t *testing.T) {
	nodes := []clusterControllerNode{
		{ID: "11111111-1111-4111-8111-111111111111", Name: "engine-1", Roles: map[string]any{"scheduler_leader": true}},
		{ID: "22222222-2222-4222-8222-222222222222", Name: "engine-2", Roles: map[string]any{}},
		{ID: "33333333-3333-4333-8333-333333333333", Name: "engine-3", Roles: map[string]any{}},
	}
	operation := clusterControllerOperation{Payload: map[string]any{
		"scope":           "all",
		"update_node_ids": []any{nodes[1].ID, nodes[2].ID, nodes[0].ID},
	}}
	// Runtime leadership moves while earlier node drains. Persisted order must
	// remain operation order instead of being recomputed from changing roles.
	nodes[1].Roles["scheduler_leader"] = true
	delete(nodes[0].Roles, "scheduler_leader")
	ordered, err := clusterUpdateNodes(operation, nodes)
	if err != nil {
		t.Fatal(err)
	}
	if ordered[0].ID != nodes[1].ID || ordered[1].ID != nodes[2].ID || ordered[2].ID != nodes[0].ID {
		t.Fatalf("pinned update order changed after role movement: %#v", ordered)
	}
}

func TestClusterUpdateStateMachineRestoresEveryNodeBeforeFinalize(t *testing.T) {
	nodes := []clusterControllerNode{
		{ID: "11111111-1111-4111-8111-111111111111", Name: "engine-1", Roles: map[string]any{}},
		{ID: "22222222-2222-4222-8222-222222222222", Name: "engine-2", Roles: map[string]any{}},
		{ID: "33333333-3333-4333-8333-333333333333", Name: "engine-3", Roles: map[string]any{}},
	}
	operation := clusterControllerOperation{Kind: "engine_update", TargetRelease: "2026.08.23", TargetSHA: strings.Repeat("b", 40), Payload: map[string]any{"scope": "all", "compatibility": map[string]any{"database_migration": "expand-contract"}}}
	steps, err := clusterOperationSteps(operation, nodes)
	if err != nil {
		t.Fatalf("build steps: %v", err)
	}
	drained := map[string]bool{}
	restored := map[string]bool{}
	for _, step := range steps {
		if strings.HasSuffix(step.Name, ":enter_drain") {
			if len(drained) != len(restored) {
				t.Fatalf("more than one node drained at step %s", step.Name)
			}
			drained[step.NodeID] = true
		}
		if strings.HasSuffix(step.Name, ":exit_drain") {
			restored[step.NodeID] = true
		}
	}
	if len(drained) != 3 || len(restored) != 3 {
		t.Fatalf("state machine omitted nodes drained=%v restored=%v", drained, restored)
	}
	expandIndex, finalizeIndex, firstStageIndex, firstRedeployIndex, firstCandidateIndex, lastExitIndex := -1, -1, -1, -1, -1, -1
	for index, step := range steps {
		switch {
		case step.Name == "expand_schema":
			expandIndex = index
		case step.Name == "finalize_schema":
			finalizeIndex = index
		case firstStageIndex == -1 && strings.HasSuffix(step.Name, ":stage_revision_images"):
			firstStageIndex = index
		case firstRedeployIndex == -1 && strings.HasSuffix(step.Name, ":redeploy_revision"):
			firstRedeployIndex = index
		case firstCandidateIndex == -1 && strings.HasSuffix(step.Name, ":inspect_candidate_health"):
			firstCandidateIndex = index
		}
		if strings.HasSuffix(step.Name, ":exit_drain") {
			lastExitIndex = index
		}
	}
	if !(firstStageIndex < expandIndex && expandIndex < firstRedeployIndex && firstRedeployIndex < firstCandidateIndex && lastExitIndex < finalizeIndex) {
		t.Fatalf("unsafe expand/finalize order: %#v", steps)
	}
}

func TestClusterUpdateVerifiesIsolatedCandidateBeforePromotion(t *testing.T) {
	node := clusterControllerNode{ID: "11111111-1111-4111-8111-111111111111", Name: "engine-1", Roles: map[string]any{}}
	operation := clusterControllerOperation{Kind: "engine_update", TargetRelease: "2026.08.23", TargetSHA: strings.Repeat("c", 40), Payload: map[string]any{"scope": "all", "compatibility": map[string]any{"database_migration": "expand-contract"}}}
	steps, err := clusterOperationSteps(operation, []clusterControllerNode{node})
	if err != nil {
		t.Fatalf("build steps: %v", err)
	}
	positions := map[string]int{}
	for index, step := range steps {
		positions[strings.TrimPrefix(step.Name, "node:"+node.ID+":")] = index
	}
	ordered := []string{"redeploy_revision", "inspect_candidate_health", "minimum_candidate_soak", "promote_candidate", "inspect_health", "minimum_ready_soak", "restore_roles", "inspect_role_health", "minimum_role_soak", "exit_drain"}
	for index := 1; index < len(ordered); index++ {
		if positions[ordered[index-1]] >= positions[ordered[index]] {
			t.Fatalf("unsafe candidate order: %#v", positions)
		}
	}
}

func TestClusterUpdateWithoutDatabaseMigrationOmitsSchemaPhases(t *testing.T) {
	node := clusterControllerNode{ID: "11111111-1111-4111-8111-111111111111", Name: "engine-1", Roles: map[string]any{}}
	operation := clusterControllerOperation{Kind: "engine_update", TargetRelease: "2026.08.23", TargetSHA: strings.Repeat("d", 40), Payload: map[string]any{"scope": "all", "compatibility": map[string]any{"database_migration": "none"}}}
	steps, err := clusterOperationSteps(operation, []clusterControllerNode{node})
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range steps {
		if step.Name == "expand_schema" || step.Name == "finalize_schema" {
			t.Fatalf("database_migration=none included schema phase: %#v", steps)
		}
	}
}

func TestQualificationUpdateDefersSchemaFinalization(t *testing.T) {
	node := clusterControllerNode{ID: "11111111-1111-4111-8111-111111111111", Name: "engine-1", Roles: map[string]any{}}
	operation := clusterControllerOperation{Kind: "engine_update", TargetRelease: "2026.09.1-rc.2", TargetSHA: strings.Repeat("d", 40), Payload: map[string]any{"scope": "all", "release_channel": "qualification", "compatibility": map[string]any{"database_migration": "expand-contract"}}}
	steps, err := clusterOperationSteps(operation, []clusterControllerNode{node})
	if err != nil {
		t.Fatal(err)
	}
	foundExpand := false
	for _, step := range steps {
		if step.Name == "expand_schema" {
			foundExpand = true
		}
		if step.Name == "finalize_schema" {
			t.Fatalf("qualification update finalized contract schema: %#v", steps)
		}
	}
	if !foundExpand {
		t.Fatalf("qualification update omitted expand schema phase: %#v", steps)
	}
}

func TestCompletedEngineReleaseConfigTracksQualificationAndStablePromotion(t *testing.T) {
	stableSHA := strings.Repeat("a", 40)
	qualificationSHA := strings.Repeat("b", 40)
	qualification := clusterControllerOperation{
		TargetRelease: "2026.09.2-rc.1",
		TargetSHA:     qualificationSHA,
		Payload: map[string]any{
			"source_release": "2026.09.1",
			"source_sha":     stableSHA,
			"compatibility":  map[string]any{"database_migration": "expand-contract"},
		},
	}
	config := completedEngineReleaseConfig(map[string]any{"k3s_version": "v1.36.3+k3s1"}, qualification)
	if config["release_channel"] != "qualification" || config["last_stable_release"] != "2026.09.1" || config["last_stable_sha"] != stableSHA || config["qualification_schema_finalize_pending"] != true {
		t.Fatalf("qualification state was not persisted: %#v", config)
	}
	promotion := clusterControllerOperation{TargetRelease: "2026.09.2", TargetSHA: qualificationSHA, Payload: map[string]any{"compatibility": map[string]any{"database_migration": "expand-contract"}}}
	config = completedEngineReleaseConfig(config, promotion)
	if config["release_channel"] != "stable" || config["last_stable_release"] != "2026.09.2" || config["last_stable_sha"] != qualificationSHA {
		t.Fatalf("stable promotion state was not persisted: %#v", config)
	}
	if _, present := config["qualification_schema_finalize_pending"]; present {
		t.Fatalf("stable promotion retained pending schema finalization: %#v", config)
	}
}

func TestClusterSchemaFinalizeRequiresEveryActiveNodeAtTarget(t *testing.T) {
	target := strings.Repeat("e", 40)
	nodes := []clusterControllerNode{{ReleaseSHA: target}, {ReleaseSHA: strings.Repeat("f", 40)}}
	if clusterAllNodesAtRevision(nodes, target) {
		t.Fatal("mixed-version cluster permitted contract finalization")
	}
	nodes[1].ReleaseSHA = target
	if !clusterAllNodesAtRevision(nodes, target) {
		t.Fatal("uniform target revision blocked contract finalization")
	}
	if clusterAllNodesAtRevision(nil, target) || clusterAllNodesAtRevision(nodes, "not-a-sha") {
		t.Fatal("invalid finalization input accepted")
	}
}

func TestClusterSchemaNodeActionPinsPhaseAndRevision(t *testing.T) {
	revision := strings.Repeat("a", 40)
	operation := clusterControllerOperation{
		TargetSHA: revision,
		Payload:   map[string]any{"schema_phase": "expand"},
	}
	want := []string{"client", "--verb", "RunSchemaPhase", "--schema-phase", "expand", "--target-sha", revision}
	got := clusterNodeActionArgs(operation, "RunSchemaPhase")
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("schema node action args=%q want %q", got, want)
	}
}

func TestClusterActionJobUsesPinnedNodeAndRestrictedContainer(t *testing.T) {
	manifest := clusterActionJobManifest("cluster-action", "borealis", "engine-2", "registry.example/borealis@sha256:"+strings.Repeat("a", 64), []string{"client", "--verb", "InspectHealth"}, "operation", "inspect")
	spec := nestedMap(nestedMap(manifest, "spec"), "template")
	podSpec := nestedMap(spec, "spec")
	if cleanText(podSpec["nodeName"]) != "engine-2" || podSpec["automountServiceAccountToken"] != false {
		t.Fatalf("job not pinned/restricted: %#v", podSpec)
	}
	containers := anySlice(podSpec["containers"])
	security := nestedMap(containers[0].(map[string]any), "securityContext")
	if security["allowPrivilegeEscalation"] != false || security["readOnlyRootFilesystem"] != true {
		t.Fatalf("unsafe action security context: %#v", security)
	}
}

func TestClusterActionJobNormalizesStepLabel(t *testing.T) {
	step := "admit:692f3ce6-038e-43c7-a7f8-9d0d425ce8bf:redeploy:" + strings.Repeat("x", 64)
	manifest := clusterActionJobManifest("cluster-action", "borealis", "engine-2", "registry.example/borealis@sha256:"+strings.Repeat("a", 64), []string{"client", "--verb", "InspectHealth"}, "operation", step)
	template := nestedMap(nestedMap(manifest, "spec"), "template")
	labels := nestedMap(template, "metadata")["labels"].(map[string]string)
	label := labels["borealis.io/operation-step"]
	if len(label) > 63 || !clusterControllerLabelValueRegex.MatchString(label) || strings.Contains(label, ":") {
		t.Fatalf("invalid operation-step label %q", label)
	}
	if label != clusterActionStepLabel(step) {
		t.Fatalf("operation-step label is not stable: %q", label)
	}
}

func TestClusterActionJobIdentityRejectsImmutableMismatch(t *testing.T) {
	expected := clusterActionJobManifest(
		"cluster-action", "borealis", "engine-2",
		"registry.example/borealis@sha256:"+strings.Repeat("a", 64),
		[]string{"client", "--verb", "InspectHealth"}, "operation", "inspect",
	)
	clone := func(t *testing.T) map[string]any {
		t.Helper()
		raw, err := json.Marshal(expected)
		if err != nil {
			t.Fatal(err)
		}
		var output map[string]any
		if err := json.Unmarshal(raw, &output); err != nil {
			t.Fatal(err)
		}
		return output
	}
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "operation label", mutate: func(job map[string]any) {
			nestedMap(job, "metadata")["labels"].(map[string]any)["borealis.io/operation-id"] = "different-operation"
		}},
		{name: "step annotation", mutate: func(job map[string]any) {
			nestedMap(job, "metadata")["annotations"].(map[string]any)["borealis.io/operation-step"] = "different-step"
		}},
		{name: "step label", mutate: func(job map[string]any) {
			template := nestedMap(nestedMap(job, "spec"), "template")
			nestedMap(template, "metadata")["labels"].(map[string]any)["borealis.io/operation-step"] = "different-step"
		}},
		{name: "target node", mutate: func(job map[string]any) {
			spec := job["spec"].(map[string]any)
			template := spec["template"].(map[string]any)
			template["spec"].(map[string]any)["nodeName"] = "engine-3"
		}},
		{name: "image", mutate: func(job map[string]any) {
			template := nestedMap(nestedMap(job, "spec"), "template")
			container := anySlice(nestedMap(template, "spec")["containers"])[0].(map[string]any)
			container["image"] = "registry.example/borealis@sha256:" + strings.Repeat("b", 64)
		}},
		{name: "arguments", mutate: func(job map[string]any) {
			template := nestedMap(nestedMap(job, "spec"), "template")
			container := anySlice(nestedMap(template, "spec")["containers"])[0].(map[string]any)
			container["args"] = []any{"client", "--verb", "EnterApplicationDrain"}
		}},
	}
	matching := clone(t)
	if err := validateClusterActionJobIdentity(matching, expected); err != nil {
		t.Fatalf("matching Kubernetes Job rejected: %v", err)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := clone(t)
			test.mutate(actual)
			if err := validateClusterActionJobIdentity(actual, expected); err == nil {
				t.Fatal("mismatched Kubernetes Job accepted")
			}
		})
	}
}

func TestNodeActionJobResumesMatchingAlreadyExistsCollision(t *testing.T) {
	operation := clusterControllerOperation{ID: "11111111-1111-4111-8111-111111111111", Attempt: 2}
	step := clusterControllerStep{Name: "admit:22222222-2222-4222-8222-222222222222:candidate_health", NodeID: "22222222-2222-4222-8222-222222222222"}
	node := clusterControllerNode{ID: step.NodeID, Name: "engine-2"}
	image := "registry.example/borealis@sha256:" + strings.Repeat("a", 64)
	jobName := clusterActionJobName(operation.ID, "attempt:2:"+step.Name)
	job := clusterActionJobManifest(jobName, "borealis", node.Name, image, clusterNodeActionArgs(operation, "InspectCandidateHealth"), operation.ID, step.Name)
	job["status"] = map[string]any{"succeeded": 1}
	gets := 0
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/jobs/"+jobName):
			gets++
			if gets == 1 {
				http.NotFound(w, request)
				return
			}
			_ = json.NewEncoder(w).Encode(job)
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/jobs"):
			posts++
			http.Error(w, "already exists", http.StatusConflict)
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{
		kube:      &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()},
		namespace: "borealis", actionImage: image, jobPollInterval: time.Millisecond,
	}
	if err := runner.nodeActionJob(context.Background(), operation, step, node, "InspectCandidateHealth"); err != nil {
		t.Fatalf("matching AlreadyExists Job did not resume: %v", err)
	}
	if posts != 1 || gets != 3 {
		t.Fatalf("unexpected collision flow posts=%d gets=%d", posts, gets)
	}
}

func TestNodeActionJobRejectsMismatchedExistingJob(t *testing.T) {
	operation := clusterControllerOperation{ID: "11111111-1111-4111-8111-111111111111", Attempt: 2}
	step := clusterControllerStep{Name: "inspect", NodeID: "22222222-2222-4222-8222-222222222222"}
	node := clusterControllerNode{ID: step.NodeID, Name: "engine-2"}
	image := "registry.example/borealis@sha256:" + strings.Repeat("a", 64)
	jobName := clusterActionJobName(operation.ID, "attempt:2:"+step.Name)
	job := clusterActionJobManifest(jobName, "borealis", node.Name, "registry.example/borealis@sha256:"+strings.Repeat("b", 64), clusterNodeActionArgs(operation, "InspectHealth"), operation.ID, step.Name)
	job["status"] = map[string]any{"succeeded": 1}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(w).Encode(job)
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{
		kube:      &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()},
		namespace: "borealis", actionImage: image, jobPollInterval: time.Millisecond,
	}
	err := runner.nodeActionJob(context.Background(), operation, step, node, "InspectHealth")
	if err == nil || !strings.Contains(err.Error(), "action image mismatch") {
		t.Fatalf("mismatched existing Job did not fail closed: %v", err)
	}
	operation.Kind = "engine_update"
	if err := runner.nodeActionJob(context.Background(), operation, step, node, "InspectHealth"); err != nil {
		t.Fatalf("exact Engine update Job did not survive controller image transition: %v", err)
	}
}

func TestMembershipAdmissionRunsNodeConformanceBeforeRedeploy(t *testing.T) {
	nodeID := "22222222-2222-4222-8222-222222222222"
	k3sVersion := "v1.36.3+k3s1"
	conformance := clusterAdmissionConformanceAction(nodeID, k3sVersion)
	if conformance.verb != "RunK3sProbeConformance" || conformance.targetRelease != k3sVersion {
		t.Fatalf("membership admission conformance action=%#v", conformance)
	}
	actions := clusterAdmissionWorkloadActions(nodeID)
	if len(actions) != 3 {
		t.Fatalf("membership admission workload actions=%d want 3", len(actions))
	}
	wantVerbs := []string{"RedeployRevision", "InspectCandidateHealth", "PromoteCandidate"}
	for index, want := range wantVerbs {
		if actions[index].verb != want {
			t.Fatalf("membership admission action %d=%s want %s", index, actions[index].verb, want)
		}
	}
	if !actions[1].soakAfter || actions[0].soakAfter || actions[2].soakAfter {
		t.Fatalf("membership admission candidate soak is not isolated: %#v", actions)
	}
	for _, action := range actions {
		if action.targetRelease != "" {
			t.Fatalf("membership admission K3s target leaked into workloads: %#v", actions)
		}
	}
}

func TestSafeRemovalFencesAndDeletesPairSequentially(t *testing.T) {
	nodes := []clusterControllerNode{
		{ID: "11111111-1111-4111-8111-111111111111", Name: "engine-1", ApplicationState: "active", Roles: map[string]any{}},
		{ID: "22222222-2222-4222-8222-222222222222", Name: "engine-2", ApplicationState: "active", Roles: map[string]any{}},
		{ID: "33333333-3333-4333-8333-333333333333", Name: "engine-3", ApplicationState: "active", Roles: map[string]any{"etcd_leader": true}},
	}
	operation := clusterControllerOperation{Kind: "node_remove", TargetNodeID: nodes[0].ID, Payload: map[string]any{
		"emergency": false, "removal_node_ids": []any{nodes[0].ID, nodes[1].ID}, "target_size": int64(1),
	}}
	steps, err := clusterOperationSteps(operation, nodes)
	if err != nil {
		t.Fatal(err)
	}
	previousVerify := -1
	removed := 0
	for index, step := range steps {
		if strings.HasSuffix(step.Name, ":prepare_member_removal") {
			removed++
			if index+4 >= len(steps) || !strings.HasSuffix(steps[index+1].Name, ":remove_etcd_membership") || !strings.HasSuffix(steps[index+2].Name, ":wait_member_fenced") || !strings.HasSuffix(steps[index+3].Name, ":delete_member_node") || !strings.HasSuffix(steps[index+4].Name, ":verify_member_removed") {
				t.Fatalf("unsafe member removal order near %s: %#v", step.Name, steps)
			}
			if index <= previousVerify {
				t.Fatalf("second member began before first verification: %#v", steps)
			}
			previousVerify = index + 4
		}
	}
	if removed != 2 || steps[1].Name != "pre_change_snapshot" || steps[2].Name != "prepare_postgres_removal" || steps[len(steps)-2].Name != "scale_shared_artifact_membership" {
		t.Fatalf("safe paired removal sequence incomplete: %#v", steps)
	}
}

func TestManagedEtcdRemovalWaitsForK3sConfirmation(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests++
		switch requests {
		case 1:
			if request.Method != http.MethodGet {
				t.Fatalf("initial member request method=%s", request.Method)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"name": "engine-2", "uid": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "resourceVersion": "10", "annotations": map[string]any{
				clusterK3sEtcdNodeNameAnnotation: "engine-2-id",
			}}})
		case 2:
			if request.Method != http.MethodPatch {
				t.Fatalf("member removal request method=%s", request.Method)
			}
			var patch map[string]any
			if err := json.NewDecoder(request.Body).Decode(&patch); err != nil {
				t.Fatal(err)
			}
			if cleanText(nestedMap(patch, "metadata")["uid"]) != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" || cleanText(nestedMap(patch, "metadata")["resourceVersion"]) != "10" {
				t.Fatalf("removal patch lost identity preconditions: %#v", patch)
			}
			annotations, _ := nestedMap(patch, "metadata")["annotations"].(map[string]any)
			if cleanText(annotations[clusterK3sEtcdRemoveAnnotation]) != "true" {
				t.Fatalf("managed etcd removal annotation missing: %#v", patch)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"annotations": annotations}})
		case 3:
			_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"name": "engine-2", "uid": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "resourceVersion": "10", "annotations": map[string]any{
				clusterK3sEtcdRemovedNameAnnotation: "engine-2-id",
			}}})
		default:
			t.Fatalf("unexpected membership request %d", requests)
		}
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{
		kube:            &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()},
		jobPollInterval: time.Millisecond,
	}
	if err := runner.removeEtcdMembership(context.Background(), "engine-2", clusterRemovalFence{NodeName: "engine-2", NodeUID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", EtcdMemberName: "engine-2-id", AcknowledgedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if requests != 3 {
		t.Fatalf("membership requests=%d want 3", requests)
	}
}

func TestEmergencyRemovalNeverContactsUnreachableTarget(t *testing.T) {
	nodes := []clusterControllerNode{
		{ID: "11111111-1111-4111-8111-111111111111", Name: "engine-1", Roles: map[string]any{}},
		{ID: "22222222-2222-4222-8222-222222222222", Name: "engine-2", Roles: map[string]any{}},
		{ID: "33333333-3333-4333-8333-333333333333", Name: "engine-3", Roles: map[string]any{}},
	}
	operation := clusterControllerOperation{Kind: "node_remove", TargetNodeID: nodes[0].ID, Payload: map[string]any{
		"emergency": true, "removal_node_ids": []any{nodes[0].ID}, "target_size": int64(2), "fencing_confirmation": "TARGET IS POWERED OFF",
	}}
	steps, err := clusterOperationSteps(operation, nodes)
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, step := range steps {
		joined += step.Name + "\n"
	}
	if strings.Contains(joined, "enter_drain") || strings.Contains(joined, "prepare_member_removal") || strings.Contains(joined, "remove_etcd_membership") || !strings.Contains(joined, "delete_member_node") || !strings.Contains(joined, "scale_postgres_membership") {
		t.Fatalf("unsafe emergency sequence: %s", joined)
	}
	if desired, status := completedRemovalClusterState(2, true); desired != 3 || status != "Degraded Quorum" {
		t.Fatalf("emergency removal did not retain three-node desired state: desired=%d status=%s", desired, status)
	}
	if desired, status := completedRemovalClusterState(1, false); desired != 1 || status != "Healthy" {
		t.Fatalf("safe removal completion changed: desired=%d status=%s", desired, status)
	}
}

func TestK3sUpdateOrdersOneServerThroughConformanceBeforeNext(t *testing.T) {
	nodes := []clusterControllerNode{
		{ID: "11111111-1111-4111-8111-111111111111", Name: "engine-1", Roles: map[string]any{"etcd_leader": true}},
		{ID: "22222222-2222-4222-8222-222222222222", Name: "engine-2", Roles: map[string]any{}},
		{ID: "33333333-3333-4333-8333-333333333333", Name: "engine-3", Roles: map[string]any{}},
	}
	operation := clusterControllerOperation{Kind: "k3s_update", TargetRelease: "v1.36.4+k3s1", Payload: map[string]any{"scope": "all", "source_k3s_version": "v1.36.3+k3s1", "upgrade_image": "registry.example/k3s-upgrade@sha256:" + strings.Repeat("a", 64)}}
	steps, err := clusterOperationSteps(operation, nodes)
	if err != nil {
		t.Fatal(err)
	}
	inFlight := false
	completed := 0
	for _, step := range steps {
		switch {
		case strings.HasSuffix(step.Name, ":apply_k3s_upgrade"):
			if inFlight {
				t.Fatalf("second K3s server started before prior restore: %#v", steps)
			}
			inFlight = true
		case strings.HasSuffix(step.Name, ":exit_drain"):
			if !inFlight {
				t.Fatalf("K3s server restored without upgrade: %#v", steps)
			}
			inFlight = false
			completed++
		}
	}
	if inFlight || completed != 3 {
		t.Fatalf("K3s sequence incomplete: %#v", steps)
	}
	joined := ""
	for _, step := range steps {
		joined += step.Name + "\n"
	}
	if !strings.Contains(joined, "post_k3s_conformance") || !strings.Contains(joined, "pre_change_snapshot") {
		t.Fatalf("K3s safety gates missing: %s", joined)
	}
	positions := nodeStepPositions(steps, nodes[1].ID)
	assertStepOrder(t, positions, []string{"post_k3s_conformance", "prepare_restore", "inspect_health", "minimum_ready_soak", "restore_roles", "inspect_role_health", "minimum_role_soak", "exit_drain"})
}

func TestK3sPlanNameIsStableDNSLabel(t *testing.T) {
	name := clusterK3sPlanName("11111111-1111-4111-8111-111111111111", "engine-1")
	if len(name) > 63 || !clusterControllerNodeRegex.MatchString(name) || name != clusterK3sPlanName("11111111-1111-4111-8111-111111111111", "engine-1") {
		t.Fatalf("invalid K3s Plan name %q", name)
	}
}

func TestK3sPlanVersionAcceptsSystemUpgradeNormalization(t *testing.T) {
	uncordons := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/plans/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"status": map[string]any{
				"latestVersion": "v1.36.4-k3s1",
				"conditions":    []any{map[string]any{"type": "Complete", "status": "True"}},
			}})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/nodes/engine-1":
			var patch map[string]any
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
				t.Fatal(err)
			}
			if nestedMap(patch, "spec")["unschedulable"] != false {
				t.Fatalf("K3s Plan completion did not uncordon node: %#v", patch)
			}
			uncordons++
			_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"name": "engine-1"}})
		case r.URL.Path == "/api/v1/nodes/engine-1":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"spec": map[string]any{},
				"status": map[string]any{
					"nodeInfo": map[string]any{"kubeletVersion": "v1.36.4+k3s1"},
					"conditions": []any{
						map[string]any{"type": "Ready", "status": "True"},
						map[string]any{"type": "EtcdIsVoter", "status": "True"},
					},
				},
			})
		default:
			t.Fatalf("unexpected K3s Plan request %s", r.URL.String())
		}
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{
		kube:            &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()},
		jobPollInterval: time.Millisecond,
	}
	if err := runner.waitK3sUpgradePlan(context.Background(), "plan-1", "engine-1", "v1.36.4+k3s1"); err != nil {
		t.Fatal(err)
	}
	if uncordons != 1 {
		t.Fatalf("K3s Plan uncordons=%d want 1", uncordons)
	}
}

func TestK3sPlanWaitToleratesTemporaryKubernetesAPIOutage(t *testing.T) {
	planRequests := 0
	uncordons := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/plans/"):
			planRequests++
			if planRequests == 1 {
				http.Error(w, "control plane restarting", http.StatusServiceUnavailable)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": map[string]any{
				"latestVersion": "v1.36.4+k3s1",
				"conditions":    []any{map[string]any{"type": "Complete", "status": "True"}},
			}})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/nodes/engine-1":
			uncordons++
			_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"name": "engine-1"}})
		case r.URL.Path == "/api/v1/nodes/engine-1":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"spec": map[string]any{},
				"status": map[string]any{
					"nodeInfo": map[string]any{"kubeletVersion": "v1.36.4+k3s1"},
					"conditions": []any{
						map[string]any{"type": "Ready", "status": "True"},
						map[string]any{"type": "EtcdIsVoter", "status": "True"},
					},
				},
			})
		default:
			t.Fatalf("unexpected K3s Plan request %s", r.URL.String())
		}
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{
		kube:            &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()},
		jobPollInterval: time.Millisecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runner.waitK3sUpgradePlan(ctx, "plan-1", "engine-1", "v1.36.4+k3s1"); err != nil {
		t.Fatal(err)
	}
	if planRequests != 2 {
		t.Fatalf("K3s Plan polls=%d want 2", planRequests)
	}
	if uncordons != 1 {
		t.Fatalf("K3s Plan uncordons=%d want 1", uncordons)
	}
}

func TestK3sPlanWaitAllowsRetryableFailedPodCount(t *testing.T) {
	planRequests := 0
	jobRequests := 0
	uncordons := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/plans/"):
			planRequests++
			conditions := []any{}
			if planRequests > 1 {
				conditions = append(conditions, map[string]any{"type": "Complete", "status": "True"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": map[string]any{
				"latestVersion": "v1.36.4+k3s1",
				"conditions":    conditions,
			}})
		case strings.Contains(r.URL.Path, "/jobs"):
			jobRequests++
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{
				"status": map[string]any{"failed": 1},
			}}})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/nodes/engine-1":
			uncordons++
			_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"name": "engine-1"}})
		case r.URL.Path == "/api/v1/nodes/engine-1":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"spec": map[string]any{},
				"status": map[string]any{
					"nodeInfo": map[string]any{"kubeletVersion": "v1.36.4+k3s1"},
					"conditions": []any{
						map[string]any{"type": "Ready", "status": "True"},
						map[string]any{"type": "EtcdIsVoter", "status": "True"},
					},
				},
			})
		default:
			t.Fatalf("unexpected K3s Plan request %s", r.URL.String())
		}
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{
		kube:            &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()},
		jobPollInterval: time.Millisecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runner.waitK3sUpgradePlan(ctx, "plan-1", "engine-1", "v1.36.4+k3s1"); err != nil {
		t.Fatal(err)
	}
	if planRequests != 2 || jobRequests != 1 || uncordons != 1 {
		t.Fatalf("K3s retryable Job polling plans=%d jobs=%d uncordons=%d", planRequests, jobRequests, uncordons)
	}
}

func TestK3sPlanWaitRejectsTerminalFailedJob(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/plans/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"status": map[string]any{
				"latestVersion": "v1.36.4+k3s1",
				"conditions":    []any{},
			}})
		case strings.Contains(r.URL.Path, "/jobs"):
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{
				"status": map[string]any{
					"failed":     1,
					"conditions": []any{map[string]any{"type": "Failed", "status": "True"}},
				},
			}}})
		default:
			t.Fatalf("unexpected K3s Plan request %s", r.URL.String())
		}
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{
		kube:            &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()},
		jobPollInterval: time.Millisecond,
	}
	err := runner.waitK3sUpgradePlan(context.Background(), "plan-1", "engine-1", "v1.36.4+k3s1")
	if err == nil || !strings.Contains(err.Error(), "has failed Job") {
		t.Fatalf("terminal failed K3s Job error=%v", err)
	}
}

func TestApplyK3sUpgradeSkipsPlanWhenNodeAlreadyAtTarget(t *testing.T) {
	cordoned := true
	patches := 0
	nodeReads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/nodes/engine-1" {
			t.Fatalf("already-upgraded node unexpectedly requested %s", r.URL.String())
		}
		if r.Method == http.MethodPatch {
			patches++
			cordoned = false
			_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"name": "engine-1"}})
			return
		}
		nodeReads++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"spec": map[string]any{"unschedulable": cordoned},
			"status": map[string]any{
				"nodeInfo": map[string]any{"kubeletVersion": "v1.36.4+k3s1"},
				"conditions": []any{
					map[string]any{"type": "Ready", "status": "True"},
					map[string]any{"type": "EtcdIsVoter", "status": "True"},
				},
			},
		})
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{
		kube: &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()},
	}
	operation := clusterControllerOperation{
		ID:            "11111111-1111-4111-8111-111111111111",
		Attempt:       3,
		TargetRelease: "v1.36.4+k3s1",
		Payload: map[string]any{
			"upgrade_image": "docker.io/rancher/k3s-upgrade@sha256:" + strings.Repeat("a", 64),
		},
	}
	if err := runner.applyK3sUpgrade(context.Background(), operation, clusterControllerNode{Name: "engine-1"}); err != nil {
		t.Fatal(err)
	}
	if patches != 1 || nodeReads != 2 || cordoned {
		t.Fatalf("already-upgraded node patches=%d reads=%d cordoned=%t", patches, nodeReads, cordoned)
	}
}

func TestCleanupK3sUpgradeOperationPlansDeletesOwnedAttempts(t *testing.T) {
	operationID := "11111111-1111-4111-8111-111111111111"
	deleted := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		collectionPath := "/apis/upgrade.cattle.io/v1/namespaces/system-upgrade/plans"
		if r.Method == http.MethodGet && r.URL.Path == collectionPath {
			if selector := r.URL.Query().Get("labelSelector"); selector != "borealis.io/operation-id="+operationID {
				t.Fatalf("K3s Plan cleanup selector=%q", selector)
			}
			items := []any{}
			for _, name := range []string{"borealis-k3s-111111111111-aaaaaaaa", "borealis-k3s-111111111111-bbbbbbbb"} {
				items = append(items, map[string]any{"metadata": map[string]any{
					"name":   name,
					"labels": map[string]any{"borealis.io/operation-id": operationID},
				}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
			return
		}
		if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, collectionPath+"/") {
			deleted[strings.TrimPrefix(r.URL.Path, collectionPath+"/")] = true
			_ = json.NewEncoder(w).Encode(map[string]any{})
			return
		}
		t.Fatalf("unexpected K3s Plan cleanup request %s %s", r.Method, r.URL.String())
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{
		kube: &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()},
	}
	if err := runner.cleanupK3sUpgradeOperationPlans(context.Background(), operationID); err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 2 || !deleted["borealis-k3s-111111111111-aaaaaaaa"] || !deleted["borealis-k3s-111111111111-bbbbbbbb"] {
		t.Fatalf("K3s Plan cleanup deleted=%v", deleted)
	}
}

func TestClusterRetryUsesFreshNodeActionsAndK3sPlans(t *testing.T) {
	operation := clusterControllerOperation{ID: "11111111-1111-4111-8111-111111111111", Attempt: 1}
	firstJob := clusterActionJobName(operation.ID, "attempt:1:node:engine-1:inspect_health")
	firstPlan := clusterK3sPlanName(clusterOperationAttemptKey(operation), "engine-1")
	operation.Attempt = 2
	secondJob := clusterActionJobName(operation.ID, "attempt:2:node:engine-1:inspect_health")
	secondPlan := clusterK3sPlanName(clusterOperationAttemptKey(operation), "engine-1")
	if firstJob == secondJob || firstPlan == secondPlan {
		t.Fatalf("retry reused action resources job=%q plan=%q", secondJob, secondPlan)
	}
}

func TestClusterRetryResumesFailedCheckpointAfterPreflight(t *testing.T) {
	steps := []clusterControllerStep{
		{Name: "preflight"},
		{Name: "pre_change_snapshot"},
		{Name: "node:engine-1:enter_drain"},
		{Name: "node:engine-1:promote_candidate"},
		{Name: "node:engine-1:exit_drain"},
	}
	operation := clusterControllerOperation{Payload: map[string]any{"retry_resume_step": "node:engine-1:promote_candidate"}}
	next, err := nextClusterOperationStep(operation, steps, 0)
	if err != nil || next != "node:engine-1:promote_candidate" {
		t.Fatalf("retry next step=%q err=%v", next, err)
	}
	operation.Payload = map[string]any{}
	next, err = nextClusterOperationStep(operation, steps, 0)
	if err != nil || next != "pre_change_snapshot" {
		t.Fatalf("normal next step=%q err=%v", next, err)
	}
	operation.Payload["retry_resume_step"] = "node:missing:promote_candidate"
	if _, err := nextClusterOperationStep(operation, steps, 0); err == nil || !strings.Contains(err.Error(), "not valid") {
		t.Fatalf("invalid retry checkpoint did not fail closed: %v", err)
	}
}

func TestClusterOperationRetryPersistsFailedStepCheckpoint(t *testing.T) {
	databaseURL := os.Getenv("BOREALIS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("BOREALIS_TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store := &postgresOperatorStore{db: db}
	if err := store.ensureClusterSchema(ctx); err != nil {
		t.Fatal(err)
	}
	const (
		clusterID   = "9dad8543-68c8-4d08-8c9e-8e87cf472f14"
		operationID = "24f9fb9a-0ae8-46d6-aefd-0cfcb0f1254e"
		failedStep  = "node:11111111-1111-4111-8111-111111111111:promote_candidate"
	)
	cleanup := func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.realtime_outbox WHERE payload_json LIKE '%' || $1 || '%'`, operationID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_audit_events WHERE target_id=$1`, operationID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_operation_events WHERE operation_id=$1`, operationID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_operations WHERE id=$1`, operationID)
		_, _ = db.ExecContext(context.Background(), `UPDATE engine.cluster_state SET active_operation_id=NULL WHERE id=1 AND active_operation_id=$1`, operationID)
	}
	cleanup()
	defer cleanup()
	now := time.Now().UTC().Unix()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO engine.cluster_state(id,cluster_id,enabled,status,active_size,desired_size,hmr_state,active_operation_id,config_json,created_at,updated_at)
		VALUES(1,$1,1,'Degraded Quorum',3,3,'inactive',NULL,'{}',$2,$2)
		ON CONFLICT(id) DO UPDATE SET cluster_id=EXCLUDED.cluster_id,enabled=1,status='Degraded Quorum',active_size=3,desired_size=3,hmr_state='inactive',active_operation_id=NULL,updated_at=EXCLUDED.updated_at
	`, clusterID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO engine.cluster_operations(id,kind,state,current_step,requested_by,payload_json,error_text,attempt,created_at,finished_at,updated_at)
		VALUES($1,'engine_update','failed',$2,'retry-checkpoint-test','{}','first failure',1,$3,$3,$3)
	`, operationID, failedStep, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.retryClusterOperation(ctx, "retry-checkpoint-test", operationID); err != nil {
		t.Fatalf("first retry: %v", err)
	}
	assertRetry := func(wantAttempt int64) {
		t.Helper()
		var state, currentStep, payloadJSON string
		var attempt int64
		if err := db.QueryRowContext(ctx, `SELECT state,current_step,payload_json,attempt FROM engine.cluster_operations WHERE id=$1`, operationID).Scan(&state, &currentStep, &payloadJSON, &attempt); err != nil {
			t.Fatal(err)
		}
		if state != "queued" || currentStep != "preflight" || attempt != wantAttempt || cleanText(parseClusterJSON(payloadJSON)["retry_resume_step"]) != failedStep {
			t.Fatalf("retry state=%s step=%s attempt=%d payload=%s", state, currentStep, attempt, payloadJSON)
		}
	}
	assertRetry(2)
	if _, err := db.ExecContext(ctx, `UPDATE engine.cluster_operations SET state='failed',error_text='preflight failed',finished_at=$1 WHERE id=$2`, now+1, operationID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE engine.cluster_state SET active_operation_id=NULL WHERE id=1 AND active_operation_id=$1`, operationID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.retryClusterOperation(ctx, "retry-checkpoint-test", operationID); err != nil {
		t.Fatalf("preflight retry: %v", err)
	}
	assertRetry(3)
}

func TestClusterControllerClaimPinsOperationActionImage(t *testing.T) {
	databaseURL := os.Getenv("BOREALIS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("BOREALIS_TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store := &postgresOperatorStore{db: db}
	if err := store.ensureClusterSchema(ctx); err != nil {
		t.Fatal(err)
	}
	const (
		clusterID   = "7cf0b650-ac4c-4c15-b01d-c83202684fc3"
		operationID = "2a5989ab-7a6a-4cd0-bf30-e07fb8420ddf"
		holder      = "action-image-pin-test"
		actionImage = "borealis-engine/api-backend:sha-1234567890ab"
	)
	cleanup := func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.realtime_outbox WHERE payload_json LIKE '%' || $1 || '%'`, operationID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_operation_events WHERE operation_id=$1`, operationID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_operations WHERE id=$1`, operationID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.cluster_application_leases WHERE name=$1 AND holder=$2`, clusterControllerLeaseName, holder)
		_, _ = db.ExecContext(context.Background(), `UPDATE engine.cluster_state SET active_operation_id=NULL WHERE id=1 AND active_operation_id=$1`, operationID)
	}
	cleanup()
	defer cleanup()
	now := time.Now().UTC().Unix()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO engine.cluster_state(id,cluster_id,enabled,status,active_size,desired_size,hmr_state,active_operation_id,config_json,created_at,updated_at)
		VALUES(1,$1,1,'Healthy',1,1,'inactive',$2,'{}',$3,$3)
		ON CONFLICT(id) DO UPDATE SET cluster_id=EXCLUDED.cluster_id,enabled=1,status='Healthy',active_size=1,desired_size=1,hmr_state='inactive',active_operation_id=EXCLUDED.active_operation_id,updated_at=EXCLUDED.updated_at
	`, clusterID, operationID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO engine.cluster_operations(id,kind,state,current_step,requested_by,payload_json,attempt,created_at,updated_at)
		VALUES($1,'membership_admit','queued','preflight','action-image-pin-test','{}',1,$2,$2)
	`, operationID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO engine.cluster_application_leases(name,holder,expires_at,updated_at)
		VALUES($1,$2,$3,$4)
		ON CONFLICT(name) DO UPDATE SET holder=EXCLUDED.holder,expires_at=EXCLUDED.expires_at,updated_at=EXCLUDED.updated_at
	`, clusterControllerLeaseName, holder, now+60, now); err != nil {
		t.Fatal(err)
	}
	controller := &clusterController{
		store:  store,
		runner: &kubernetesClusterStepRunner{actionImage: actionImage},
		holder: holder,
		now:    func() time.Time { return time.Unix(now+1, 0).UTC() },
	}
	operation, claimed, err := controller.claimOperation(ctx)
	if err != nil || !claimed {
		t.Fatalf("claim operation: claimed=%t err=%v", claimed, err)
	}
	if cleanText(operation.Payload["action_image"]) != actionImage || clusterOperationActionImage(operation, "borealis-engine/api-backend:sha-abcdef123456") != actionImage {
		t.Fatalf("claimed operation did not use pinned action image: %#v", operation.Payload)
	}
	var payloadJSON string
	if err := db.QueryRowContext(ctx, `SELECT payload_json FROM engine.cluster_operations WHERE id=$1`, operationID).Scan(&payloadJSON); err != nil {
		t.Fatal(err)
	}
	if cleanText(parseClusterJSON(payloadJSON)["action_image"]) != actionImage {
		t.Fatalf("durable operation action image was not pinned: %s", payloadJSON)
	}
}

func TestClusterOperationActionImageFallsBackBeforeClaimPin(t *testing.T) {
	operation := clusterControllerOperation{Payload: map[string]any{}}
	fallback := "borealis-engine/api-backend:sha-abcdef123456"
	if got := clusterOperationActionImage(operation, "  "+fallback+"  "); got != fallback {
		t.Fatalf("fallback action image=%q", got)
	}
}

func TestMemberRemovalHealthRequiresReadyEtcdVoters(t *testing.T) {
	voter := "True"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/nodes/engine-1":
			http.NotFound(w, r)
		case "/api/v1/nodes/engine-2", "/api/v1/nodes/engine-3":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": map[string]any{"conditions": []any{
				map[string]any{"type": "Ready", "status": "True"},
				map[string]any{"type": "EtcdIsVoter", "status": voter},
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()}, namespace: "borealis"}
	nodes := []clusterControllerNode{{ID: "1", Name: "engine-1"}, {ID: "2", Name: "engine-2"}, {ID: "3", Name: "engine-3"}}
	operation := clusterControllerOperation{Payload: map[string]any{"removal_node_ids": []any{"1"}}}
	healthy, err := runner.memberRemovalStateHealthy(context.Background(), operation, "engine-1", nodes)
	if err != nil || !healthy {
		t.Fatalf("expected remaining voter health, healthy=%v err=%v", healthy, err)
	}
	voter = "False"
	healthy, err = runner.memberRemovalStateHealthy(context.Background(), operation, "engine-1", nodes)
	if err != nil || healthy {
		t.Fatalf("expected non-voter rejection, healthy=%v err=%v", healthy, err)
	}
}

func TestMemberRemovalVerificationRetiresResidentWorkloads(t *testing.T) {
	scaled := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/nodes/engine-1":
			http.NotFound(w, r)
		case r.URL.Path == "/api/v1/nodes/engine-2" || r.URL.Path == "/api/v1/nodes/engine-3":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": map[string]any{"conditions": []any{
				map[string]any{"type": "Ready", "status": "True"},
				map[string]any{"type": "EtcdIsVoter", "status": "True"},
			}}})
		case r.Method == http.MethodGet && r.URL.Path == "/apis/apps/v1/namespaces/borealis/deployments":
			selector := r.URL.Query().Get("labelSelector")
			appName := ""
			for _, candidate := range []string{"borealis-operator", "wireguard-tunnel"} {
				if strings.Contains(selector, "app.kubernetes.io/name="+candidate) {
					appName = candidate
					break
				}
			}
			if appName == "" || !strings.Contains(selector, "borealis.io/engine-node=engine-1") {
				t.Fatalf("unexpected removed-node workload selector %q", selector)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{
				map[string]any{"metadata": map[string]any{"name": appName + "-engine-1"}},
			}})
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/apis/apps/v1/namespaces/borealis/deployments/"):
			name := strings.TrimPrefix(r.URL.Path, "/apis/apps/v1/namespaces/borealis/deployments/")
			var patch map[string]any
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
				t.Fatal(err)
			}
			replicas, present := nestedMap(patch, "spec")["replicas"]
			if !present || coerceInt64(replicas) != 0 {
				t.Fatalf("removed-node workload %s was not retired: %#v", name, patch)
			}
			scaled[name]++
			_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"name": name}})
		default:
			t.Fatalf("unexpected member-removal verification request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{
		kube:      &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()},
		namespace: "borealis",
		soak:      time.Millisecond,
	}
	nodes := []clusterControllerNode{{ID: "1", Name: "engine-1"}, {ID: "2", Name: "engine-2"}, {ID: "3", Name: "engine-3"}}
	operation := clusterControllerOperation{Payload: map[string]any{"removal_node_ids": []any{"1"}}}
	if err := runner.verifyMemberRemoved(context.Background(), operation, "engine-1", nodes); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"borealis-operator-engine-1", "wireguard-tunnel-engine-1"} {
		if scaled[name] != 1 {
			t.Fatalf("removed-node workload %s scaled %d times", name, scaled[name])
		}
	}
}

func TestHMRRequiresActiveTarget(t *testing.T) {
	_, err := clusterOperationSteps(clusterControllerOperation{Kind: "hmr_start", TargetNodeID: "11111111-1111-4111-8111-111111111111"}, nil)
	if err == nil {
		t.Fatal("expected inactive HMR target rejection")
	}
}

func TestPostgresEmergencyFailoverDoesNotDependOnNewBackup(t *testing.T) {
	targetID := "11111111-1111-4111-8111-111111111111"
	nodes := []clusterControllerNode{{ID: targetID, Name: "engine-1"}}
	steps, err := clusterOperationSteps(clusterControllerOperation{Kind: "postgres_emergency_failover", TargetNodeID: targetID}, nodes)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 3 || steps[0].Name != "preflight" || steps[1].Name != "postgres_role_change" || steps[2].Name != "verify_postgres" {
		t.Fatalf("emergency failover retained blocking snapshot dependency: %#v", steps)
	}
	switchover, err := clusterOperationSteps(clusterControllerOperation{Kind: "postgres_switchover", TargetNodeID: targetID}, nodes)
	if err != nil {
		t.Fatal(err)
	}
	if len(switchover) != 4 || switchover[1].Name != "pre_change_snapshot" {
		t.Fatalf("planned switchover lost pre-change snapshot: %#v", switchover)
	}
}

func TestTwoNodePreflightAllowsOnlyDegradedRecoveryOperations(t *testing.T) {
	nodes := []clusterControllerNode{
		{ID: "11111111-1111-4111-8111-111111111111", Name: "engine-1"},
		{ID: "22222222-2222-4222-8222-222222222222", Name: "engine-2"},
	}
	runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{}}
	step := clusterControllerStep{Name: "preflight"}
	allowed := []clusterControllerOperation{
		{Kind: "membership_admit"},
		{Kind: "postgres_emergency_failover"},
		{Kind: "node_maintenance", Payload: map[string]any{"action": "exit"}},
	}
	for _, operation := range allowed {
		if err := runner.Run(context.Background(), operation, step, nodes); err != nil {
			t.Fatalf("two-node recovery %s rejected: %v", operation.Kind, err)
		}
	}
	blocked := []clusterControllerOperation{
		{Kind: "membership_scale"},
		{Kind: "postgres_switchover"},
		{Kind: "node_maintenance", Payload: map[string]any{"action": "enter"}},
	}
	for _, operation := range blocked {
		if err := runner.Run(context.Background(), operation, step, nodes); err == nil {
			t.Fatalf("unsupported two-node operation %s accepted", operation.Kind)
		}
	}
}

func TestOperationDrainStepsPersistDurableNodeState(t *testing.T) {
	nodeID := "11111111-1111-4111-8111-111111111111"
	operation := clusterControllerOperation{
		Kind:        "engine_update",
		CurrentStep: "node:" + nodeID + ":enter_drain",
		Payload:     map[string]any{"reason": "rolling release"},
	}
	gotID, state, reason, ok := clusterOperationNodeStateTransition(operation)
	if !ok || gotID != nodeID || state != "drained" || reason != "rolling release" {
		t.Fatalf("enter-drain state transition missing: id=%q state=%q reason=%q ok=%v", gotID, state, reason, ok)
	}
	operation.CurrentStep = "node:" + nodeID + ":exit_drain"
	gotID, state, reason, ok = clusterOperationNodeStateTransition(operation)
	if !ok || gotID != nodeID || state != "active" || reason != "" {
		t.Fatalf("exit-drain state transition missing: id=%q state=%q reason=%q ok=%v", gotID, state, reason, ok)
	}
	operation.CurrentStep = "node:" + nodeID + ":inspect_health"
	if _, _, _, ok := clusterOperationNodeStateTransition(operation); ok {
		t.Fatal("non-drain step changed durable node state")
	}
}

func TestRecoveryCompletionPreservesTopologyAndDatabaseDegradation(t *testing.T) {
	healthyDatabase := map[string]any{"database_runtime": map[string]any{"fully_ready": true, "durability_quorum": true}}
	degradedDatabase := map[string]any{"database_runtime": map[string]any{"fully_ready": false, "durability_quorum": true}}
	emergency := clusterControllerOperation{Kind: "postgres_emergency_failover"}
	if got := completedClusterRecoveryStatus(emergency, "Degraded Quorum", 2, 3, true, healthyDatabase); got != "Degraded Quorum" {
		t.Fatalf("two-of-three failover cleared quorum degradation: %q", got)
	}
	if got := completedClusterRecoveryStatus(emergency, "Degraded Database", 3, 3, true, degradedDatabase); got != "Degraded Database" {
		t.Fatalf("failover hid remaining database degradation: %q", got)
	}
	exit := clusterControllerOperation{Kind: "node_maintenance", Payload: map[string]any{"action": "exit"}}
	if got := completedClusterRecoveryStatus(exit, "Degraded Quorum", 3, 3, true, healthyDatabase); got != "Healthy" {
		t.Fatalf("verified maintenance recovery did not clear degraded state: %q", got)
	}
	if got := completedClusterRecoveryStatus(exit, "Degraded Quorum", 3, 3, false, healthyDatabase); got != "Degraded Quorum" {
		t.Fatalf("maintenance exit cleared state with another drained node: %q", got)
	}
	hmrExit := clusterControllerOperation{Kind: "hmr_exit"}
	if got := completedClusterRecoveryStatus(hmrExit, "HMR Transition", 3, 3, true, degradedDatabase); got != "Degraded Database" {
		t.Fatalf("HMR exit hid database degradation: %q", got)
	}
}

func TestHMRExitRestoresStandbyBeforeClearingDrain(t *testing.T) {
	targetID := "11111111-1111-4111-8111-111111111111"
	standbyID := "22222222-2222-4222-8222-222222222222"
	nodes := []clusterControllerNode{{ID: targetID, Name: "engine-1"}, {ID: standbyID, Name: "engine-2"}}
	steps, err := clusterOperationSteps(clusterControllerOperation{Kind: "hmr_exit", TargetNodeID: targetID}, nodes)
	if err != nil {
		t.Fatal(err)
	}
	positions := nodeStepPositions(steps, standbyID)
	assertStepOrder(t, positions, []string{"prepare_restore", "inspect_health", "minimum_ready_soak", "restore_roles", "inspect_role_health", "minimum_role_soak", "exit_drain"})
	allPositions := map[string]int{}
	for index, step := range steps {
		allPositions[step.Name] = index
	}
	targetPositions := nodeStepPositions(steps, targetID)
	assertStepOrder(t, targetPositions, []string{"enter_drain", "wait_endpoint_withdrawal", "restore_roles", "inspect_role_health", "minimum_role_soak", "exit_drain"})
	if allPositions["hmr_reconcile_vip_placement"] >= positions["prepare_restore"] ||
		positions["exit_drain"] >= allPositions["hmr_move_roles_to_standby"] ||
		allPositions["hmr_move_roles_to_standby"] >= targetPositions["enter_drain"] ||
		targetPositions["wait_endpoint_withdrawal"] >= allPositions["hmr_restore_pinned_release"] ||
		allPositions["hmr_restore_pinned_release"] >= allPositions["hmr_inspect_candidate"] ||
		allPositions["hmr_inspect_candidate"] >= allPositions["hmr_candidate_soak"] ||
		allPositions["hmr_candidate_soak"] >= allPositions["hmr_promote_candidate"] ||
		allPositions["hmr_promote_candidate"] >= allPositions["hmr_verify_production"] ||
		allPositions["hmr_verify_production"] >= targetPositions["restore_roles"] {
		t.Fatalf("unsafe HMR production restore order: %#v", allPositions)
	}
}

func TestHMRPinnedRestoreUsesSavedImmutableRelease(t *testing.T) {
	sha := strings.Repeat("a", 40)
	operation := clusterControllerOperation{Payload: map[string]any{"baseline_release": "2026.08.24", "baseline_sha": sha}}
	restore, err := hmrPinnedRestoreOperation(operation, clusterControllerNode{})
	if err != nil || restore.TargetRelease != "2026.08.24" || restore.TargetSHA != sha {
		t.Fatalf("saved release not restored: %#v err=%v", restore, err)
	}
	if _, err := hmrPinnedRestoreOperation(clusterControllerOperation{}, clusterControllerNode{}); err == nil {
		t.Fatal("missing pinned production release accepted")
	}
	development := clusterControllerOperation{Payload: map[string]any{"baseline_release": "dev-aaaaaaaaaaaa", "baseline_sha": sha}}
	restore, err = hmrPinnedRestoreOperation(development, clusterControllerNode{})
	if err != nil || restore.TargetRelease != "dev-aaaaaaaaaaaa" || restore.TargetSHA != sha {
		t.Fatalf("saved development baseline not restored: %#v err=%v", restore, err)
	}
	development.Payload["baseline_release"] = "dev-bbbbbbbbbbbb"
	if _, err := hmrPinnedRestoreOperation(development, clusterControllerNode{}); err == nil {
		t.Fatal("development baseline mismatched to full SHA accepted")
	}
}

func TestSingleNodeHMRExitFencesHostPortsBeforeCandidate(t *testing.T) {
	targetID := "11111111-1111-4111-8111-111111111111"
	steps, err := clusterOperationSteps(clusterControllerOperation{Kind: "hmr_exit", TargetNodeID: targetID}, []clusterControllerNode{{ID: targetID, Name: "engine-1"}})
	if err != nil {
		t.Fatal(err)
	}
	positions := map[string]int{}
	for index, step := range steps {
		positions[step.Name] = index
	}
	for _, required := range []string{"hmr_fence_target_roles", "node:" + targetID + ":enter_drain", "node:" + targetID + ":wait_endpoint_withdrawal", "hmr_restore_pinned_release"} {
		if _, exists := positions[required]; !exists {
			t.Fatalf("single-node HMR restore step %q missing: %#v", required, positions)
		}
	}
	if positions["hmr_fence_target_roles"] >= positions["node:"+targetID+":enter_drain"] ||
		positions["node:"+targetID+":wait_endpoint_withdrawal"] >= positions["hmr_restore_pinned_release"] {
		t.Fatalf("single-node HMR restore did not fence active host ports: %#v", positions)
	}
}

func TestMaintenanceExitProvesHealthBeforeClearingDrain(t *testing.T) {
	nodeID := "11111111-1111-4111-8111-111111111111"
	nodes := []clusterControllerNode{{ID: nodeID, Name: "engine-1"}}
	operation := clusterControllerOperation{Kind: "node_maintenance", TargetNodeID: nodeID, Payload: map[string]any{"action": "exit"}}
	steps, err := clusterOperationSteps(operation, nodes)
	if err != nil {
		t.Fatal(err)
	}
	positions := nodeStepPositions(steps, nodeID)
	assertStepOrder(t, positions, []string{"reconcile_vip_placement", "prepare_restore", "inspect_health", "minimum_ready_soak", "restore_roles", "inspect_role_health", "minimum_role_soak", "exit_drain"})
}

func TestMaintenanceAndHMRReconcileVIPPlacementBeforeRoleTransfer(t *testing.T) {
	targetID := "11111111-1111-4111-8111-111111111111"
	nodes := []clusterControllerNode{
		{ID: targetID, Name: "engine-1"},
		{ID: "22222222-2222-4222-8222-222222222222", Name: "engine-2"},
		{ID: "33333333-3333-4333-8333-333333333333", Name: "engine-3"},
	}
	maintenance, err := clusterOperationSteps(clusterControllerOperation{Kind: "node_maintenance", TargetNodeID: targetID, Payload: map[string]any{"action": "enter"}}, nodes)
	if err != nil {
		t.Fatal(err)
	}
	maintenancePositions := nodeStepPositions(maintenance, targetID)
	assertStepOrder(t, maintenancePositions, []string{"reconcile_vip_placement", "transfer_roles", "enter_drain"})

	hmr, err := clusterOperationSteps(clusterControllerOperation{Kind: "hmr_start", TargetNodeID: targetID}, nodes)
	if err != nil {
		t.Fatal(err)
	}
	hmrPositions := map[string]int{}
	for index, step := range hmr {
		hmrPositions[step.Name] = index
	}
	if hmrPositions["hmr_reconcile_vip_placement"] >= hmrPositions["hmr_move_roles"] || hmrPositions["hmr_move_roles"] >= hmrPositions["hmr_drain_standby"] {
		t.Fatalf("unsafe HMR VIP placement order: %#v", hmrPositions)
	}
}

func nodeStepPositions(steps []clusterControllerStep, nodeID string) map[string]int {
	positions := map[string]int{}
	prefix := "node:" + nodeID + ":"
	for index, step := range steps {
		if strings.HasPrefix(step.Name, prefix) {
			positions[strings.TrimPrefix(step.Name, prefix)] = index
		}
	}
	return positions
}

func assertStepOrder(t *testing.T, positions map[string]int, ordered []string) {
	t.Helper()
	for index, name := range ordered {
		position, exists := positions[name]
		if !exists {
			t.Fatalf("restore step %q missing: %#v", name, positions)
		}
		if index > 0 && positions[ordered[index-1]] >= position {
			t.Fatalf("unsafe restore order: %#v", positions)
		}
	}
}

func TestHMRNodeHealthRequiresReadyNodeScopedEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/nodes/engine-1":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "True"}}}})
		case "/apis/discovery.k8s.io/v1/namespaces/borealis/endpointslices":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"endpoints": []any{map[string]any{"nodeName": "engine-1", "conditions": map[string]any{"ready": true}}}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()}, namespace: "borealis"}
	ready, err := runner.nodeReady(context.Background(), "engine-1")
	if err != nil || !ready {
		t.Fatalf("expected ready node, ready=%v err=%v", ready, err)
	}
	if err := runner.verifyReadyEndpointsForNode(context.Background(), "engine-1"); err != nil {
		t.Fatalf("expected node-scoped ready endpoint: %v", err)
	}
	if err := runner.verifyReadyEndpointsForNode(context.Background(), "engine-2"); err == nil {
		t.Fatal("expected node-scoped endpoint rejection for lost HMR node")
	}
}

func TestWaitNodeEndpointsWithdrawnIgnoresResidentInfrastructureEndpoints(t *testing.T) {
	trafficReady := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trafficEndpoints := []any{map[string]any{"nodeName": "engine-2", "conditions": map[string]any{"ready": true}}}
		if trafficReady {
			trafficEndpoints = append(trafficEndpoints, map[string]any{"nodeName": "engine-1", "conditions": map[string]any{"ready": true}})
		}
		items := []any{
			map[string]any{
				"metadata":  map[string]any{"labels": map[string]any{"kubernetes.io/service-name": "borealis-operator"}},
				"endpoints": []any{map[string]any{"nodeName": "engine-1", "conditions": map[string]any{"ready": true}}},
			},
			map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"kubernetes.io/service-name": "api-backend-aegis"}},
				"endpoints": []any{map[string]any{
					"nodeName": "engine-1", "conditions": map[string]any{"ready": true},
					"targetRef": map[string]any{"kind": "Pod", "name": "api-backend-engine-1-candidate-7654"},
				}},
			},
			map[string]any{
				"metadata":  map[string]any{"labels": map[string]any{"kubernetes.io/service-name": "borealis-postgres-r"}},
				"endpoints": []any{map[string]any{"nodeName": "engine-1", "conditions": map[string]any{"ready": true}}},
			},
			map[string]any{
				"metadata":  map[string]any{"labels": map[string]any{"kubernetes.io/service-name": "api-backend"}},
				"endpoints": trafficEndpoints,
			},
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()}, namespace: "borealis"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runner.waitNodeEndpointsWithdrawn(ctx, "engine-1"); err != nil {
		t.Fatalf("resident infrastructure or isolated candidate endpoint blocked application drain: %v", err)
	}
	trafficReady = true
	readyCtx, readyCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer readyCancel()
	if err := runner.waitNodeEndpointsWithdrawn(readyCtx, "engine-1"); err == nil {
		t.Fatal("ready drained-traffic endpoint was accepted")
	}
}

func TestVIPRoleTransferWaitsForClusterLeaseAndWireGuardReadiness(t *testing.T) {
	clusterOwner := "engine-1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apis/coordination.k8s.io/v1/namespaces/kube-system/leases/borealis-cluster-vip":
			_ = json.NewEncoder(w).Encode(map[string]any{"spec": map[string]any{"holderIdentity": clusterOwner}})
		case "/apis/apps/v1/namespaces/borealis/deployments":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"metadata": map[string]any{"name": "wireguard-tunnel-engine-2"}}}})
		case "/apis/apps/v1/namespaces/borealis/deployments/wireguard-tunnel-engine-2":
			if r.Method == http.MethodPatch {
				_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"name": "wireguard-tunnel-engine-2"}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"metadata": map[string]any{"generation": int64(2)},
				"spec":     map[string]any{"replicas": int64(1)},
				"status": map[string]any{
					"observedGeneration": int64(2), "availableReplicas": int64(1),
					"readyReplicas": int64(1), "updatedReplicas": int64(1),
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()}, namespace: "borealis"}
	blockedCtx, blockedCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer blockedCancel()
	if err := runner.waitVIPAndWireGuardOwner(blockedCtx, "engine-2"); err == nil {
		t.Fatal("VIP role transfer accepted stale Cluster Virtual IP owner")
	}
	clusterOwner = "engine-2"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := runner.waitVIPAndWireGuardOwner(ctx, "engine-2"); err != nil {
		t.Fatalf("Cluster Virtual IP and WireGuard ownership did not converge: %v", err)
	}
}

func TestHMRExitRoleTransferAwayAcceptsHealthyEligibleOwner(t *testing.T) {
	readyReplicas := int64(1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apis/coordination.k8s.io/v1/namespaces/kube-system/leases/borealis-cluster-vip":
			_ = json.NewEncoder(w).Encode(map[string]any{"spec": map[string]any{"holderIdentity": "engine-3"}})
		case "/apis/apps/v1/namespaces/borealis/deployments":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{
				"metadata": map[string]any{"name": "wireguard-tunnel-engine-3", "generation": int64(2)},
				"spec":     map[string]any{"replicas": int64(1)},
				"status": map[string]any{
					"observedGeneration": int64(2), "availableReplicas": readyReplicas,
					"readyReplicas": readyReplicas, "updatedReplicas": int64(1),
				},
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()}, namespace: "borealis"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := runner.waitVIPAndWireGuardOwnersAwayFrom(ctx, "engine-1"); err != nil {
		t.Fatalf("HMR exit rejected healthy non-target VIP/WireGuard owners: %v", err)
	}
	readyReplicas = 0
	unreadyCtx, unreadyCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer unreadyCancel()
	if err := runner.waitVIPAndWireGuardOwnersAwayFrom(unreadyCtx, "engine-1"); err == nil {
		t.Fatal("HMR exit accepted unready non-target VIP/WireGuard owners")
	}
}

func TestRoleEligibilityDoesNotWaitForLegacyStandbyWireGuardReadiness(t *testing.T) {
	readinessGets := 0
	patchedLabels := map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/nodes/engine-1":
			if r.Method == http.MethodPatch {
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				patchedLabels = nestedMap(nestedMap(payload, "metadata"), "labels")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"name": "engine-1"}})
		case "/apis/apps/v1/namespaces/borealis/deployments":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"metadata": map[string]any{"name": "wireguard-tunnel-engine-1"}}}})
		case "/apis/apps/v1/namespaces/borealis/deployments/wireguard-tunnel-engine-1":
			if r.Method == http.MethodGet {
				readinessGets++
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"name": "wireguard-tunnel-engine-1"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()}, namespace: "borealis"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := runner.setNodeRoleEligibility(ctx, "engine-1", false); err != nil {
		t.Fatalf("legacy standby WireGuard readiness blocked role fencing: %v", err)
	}
	if readinessGets != 0 {
		t.Fatalf("role fencing performed %d readiness GETs before VIP ownership moved", readinessGets)
	}
	if cleanText(patchedLabels["borealis.io/control-plane-eligible"]) != "false" || cleanText(patchedLabels["borealis.io/edge-eligible"]) != "false" {
		t.Fatalf("role fencing did not withdraw both VIP eligibility labels: %#v", patchedLabels)
	}
}

func TestEtcdLeaderOwnerRequiresExactlyOneReportedNode(t *testing.T) {
	ready := []any{map[string]any{"type": "Ready", "status": "True"}}
	notReady := []any{map[string]any{"type": "Ready", "status": "False"}}
	items := []any{map[string]any{"metadata": map[string]any{"name": "engine-2"}, "status": map[string]any{"conditions": ready}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/nodes" || r.URL.Query().Get("labelSelector") != "borealis.io/etcd-leader=true" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()}}
	owner, err := runner.etcdLeaderOwner(context.Background())
	if err != nil || owner != "engine-2" {
		t.Fatalf("etcd leader owner=%q err=%v", owner, err)
	}
	items = append(items, map[string]any{"metadata": map[string]any{"name": "engine-3"}, "status": map[string]any{"conditions": notReady}})
	owner, err = runner.etcdLeaderOwner(context.Background())
	if err != nil || owner != "engine-2" {
		t.Fatalf("stale NotReady etcd leader label was not ignored owner=%q err=%v", owner, err)
	}
	items[1] = map[string]any{"metadata": map[string]any{"name": "engine-3"}, "status": map[string]any{"conditions": ready}}
	if _, err := runner.etcdLeaderOwner(context.Background()); err == nil {
		t.Fatal("multiple ready etcd leader reports accepted")
	}
}
