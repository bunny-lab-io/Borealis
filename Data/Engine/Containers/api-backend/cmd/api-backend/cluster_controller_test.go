package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	admission := map[string]any{"id": firstPending, "invitation_id": invitationID, "cluster_id": clusterID, "token_hash": invitation["token_hash"], "node_name": "engine-2", "hostname": "engine-2", "management_ip": "192.0.2.22", "architecture": "amd64", "os_version": "Ubuntu 24.04"}
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

func TestClusterCustomResourceStatesKeepDesiredAndRuntimeFieldsSeparate(t *testing.T) {
	clusterID := "11111111-1111-4111-8111-111111111111"
	operationID := "22222222-2222-4222-8222-222222222222"
	state := clusterControllerState{
		ClusterID:       clusterID,
		Enabled:         true,
		Status:          "Mixed Version",
		ActiveSize:      1,
		DesiredSize:     3,
		ControlPlaneVIP: "192.0.2.10",
		EdgeVIP:         "192.0.2.11",
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
	if coerceInt64(spec["activeSize"]) != 1 || coerceInt64(spec["desiredSize"]) != 3 {
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
		ManagementIP: "192.0.2.12",
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
			if index+3 >= len(steps) || !strings.HasSuffix(steps[index+1].Name, ":wait_member_fenced") || !strings.HasSuffix(steps[index+2].Name, ":delete_member_node") || !strings.HasSuffix(steps[index+3].Name, ":verify_member_removed") {
				t.Fatalf("unsafe member removal order near %s: %#v", step.Name, steps)
			}
			if index <= previousVerify {
				t.Fatalf("second member began before first verification: %#v", steps)
			}
			previousVerify = index + 3
		}
	}
	if removed != 2 || steps[1].Name != "pre_change_snapshot" || steps[2].Name != "prepare_postgres_removal" {
		t.Fatalf("safe paired removal sequence incomplete: %#v", steps)
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
	if strings.Contains(joined, "enter_drain") || strings.Contains(joined, "prepare_member_removal") || !strings.Contains(joined, "delete_member_node") || !strings.Contains(joined, "scale_postgres_membership") {
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

func TestHMRRequiresActiveTarget(t *testing.T) {
	_, err := clusterOperationSteps(clusterControllerOperation{Kind: "hmr_start", TargetNodeID: "11111111-1111-4111-8111-111111111111"}, nil)
	if err == nil {
		t.Fatal("expected inactive HMR target rejection")
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
	if positions["exit_drain"] >= allPositions["hmr_move_roles_to_standby"] ||
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
	assertStepOrder(t, positions, []string{"prepare_restore", "inspect_health", "minimum_ready_soak", "restore_roles", "inspect_role_health", "minimum_role_soak", "exit_drain"})
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

func TestEdgeRoleTransferWaitsForLeaseAndWireGuardReadiness(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apis/coordination.k8s.io/v1/namespaces/kube-system/leases/borealis-edge-vip":
			_ = json.NewEncoder(w).Encode(map[string]any{"spec": map[string]any{"holderIdentity": "engine-2"}})
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := runner.waitEdgeAndWireGuardOwner(ctx, "engine-2"); err != nil {
		t.Fatalf("edge/WireGuard ownership did not converge: %v", err)
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
