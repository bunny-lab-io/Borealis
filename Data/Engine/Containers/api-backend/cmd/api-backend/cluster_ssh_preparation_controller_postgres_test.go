package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClusterSSHPreparationControllerPostgresNativeSettlement(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "joined proof lost"} {
		t.Run(mode, func(t *testing.T) {
			f, snapshot, confirmation, _ := newSSHPreparationApprovalFixture(t, mode == "replacement")
			sourceNode := ""
			for _, member := range snapshot.work.Proof.Source.Members {
				if member.NodeID == confirmation.IdentitySource.SourceID {
					sourceNode = member.Name
				}
			}
			err := f.c.runSSHPreparationParentWithGates(f.ctx, f.op, f.observe, func(ctx context.Context, _ clusterSSHPreparationApprovalSnapshot, consume func(context.Context, clusterSSHPreparationGates) (clusterSSHPreparationReadinessReceipt, error)) error {
				_, err := consume(ctx, sshPreparationCPUGatesFixture(t, snapshot))
				return err
			}, sshPreparationCPUReadFixture(t, snapshot))
			if err != nil {
				t.Fatal("controller approval", err)
			}
			broker := newClusterSSHSourceBroker(f.c.store, nil, f.c.holder, sshBrokerTestSecret)
			uids := map[string]string{}
			broker.readJoined = func(ctx context.Context, r clusterSSHSourceBrokerRequest) (clusterSSHJoinedTargets, error) {
				if mode == "joined proof lost" {
					return clusterSSHJoinedTargets{}, errClusterConflict
				}
				read := func(ctx context.Context) (clusterSSHPreparationAuthority, error) {
					return f.c.store.loadClusterSSHPreparationAuthority(ctx, r.Lease, r.Baseline, r.sealed())
				}
				a, err := read(ctx)
				if err != nil {
					return clusterSSHJoinedTargets{}, err
				}
				return readClusterSSHJoinedTargets(ctx, read, sshPrejoinCompletionNodes(a, uids))
			}
			server := httptest.NewServer(http.HandlerFunc(broker.handle))
			defer server.Close()
			client := sshBrokerClient(t, server.URL)
			worker := newClusterSSHNativePrejoinPreparationWorkerRead(f.c.store, f.aegis, t.TempDir(), clusterExistingIdentitySourceProcess{}, client.joinedTargetsRead)
			// Only native effects are simulated. Normal controller approval, API
			// claim, renewal, authenticated controller proof and v2 SQL settlement
			// all run unchanged against the disposable database.
			worker.cohort = func(ctx context.Context, claims []clusterSSHNetworkTargetClaim, baseline clusterbootstrap.Expected, confirmation string, source engineidentity.Binding, check func(context.Context) error) ([]clusterSSHPrejoinDispatchResult, error) {
				var values []clusterSSHPrejoinDispatchResult
				for i, claim := range claims {
					if check(ctx) != nil || f.c.store.db.Stats().InUse != 0 {
						return nil, errClusterConflict
					}
					a, err := newClusterSSHPreparationAuthorityRead(f.c.store, f.aegis, claim.Lease, baseline, claim.Sealed)(ctx)
					if err != nil {
						return nil, err
					}
					expected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, claim.Lease, baseline, a.K3sVersion, "10.42.0.0/16", "10.43.0.0/16", a.payload)
					if err != nil {
						return nil, err
					}
					receipt := sshPrejoinReceiptIdentityFixture(expected, source, newClusterUUID(), uint64(42+i))
					uids[claim.Lease.TargetID] = receipt.Node.NodeUID
					values = append(values, clusterSSHPrejoinDispatchResult{Expected: expected, Receipt: receipt})
				}
				return values, nil
			}
			runtime := clusterSSHPreparationRuntime{ready: func() bool { return true }, pending: func(ctx context.Context) (clusterSSHPendingPreparation, error) {
				return f.c.store.pendingClusterSSHPreparation(ctx, sourceNode)
			}, claim: f.c.store.claimClusterSSHPreparation, execute: worker.prepare}
			executor := clusterSSHInspectionRuntime{store: f.c.store, aegis: f.aegis, worker: newClusterSSHInspectionWorker(f.c.store, f.aegis), preparation: &runtime}
			err = executor.runOnce(f.ctx)
			if (err == nil) != (mode != "joined proof lost") {
				t.Fatal("normal executor settlement", err)
			}
			var payload string
			if f.c.store.db.QueryRowContext(f.ctx, `SELECT payload_json FROM engine.cluster_operations WHERE id=$1`, f.op.ID).Scan(&payload) != nil {
				t.Fatal("payload")
			}
			var raw json.RawMessage
			if clusterBootstrapObject([]byte(payload), map[string]any{"ssh_preparation": &raw}) != nil {
				t.Fatal("receipt")
			}
			if mode == "joined proof lost" {
				if len(raw) != 0 {
					t.Fatal("failed proof settled cohort")
				}
			} else {
				proof, _, err := parseClusterSSHPreparationInspection([]byte(payload), clusterSSHTargetLease{OperationID: f.op.ID, OperationAttempt: f.op.Attempt, ControllerHolder: f.c.holder}, snapshot.work.Baseline)
				if err != nil {
					t.Fatal("settled inspection", err)
				}
				receipt, err := parseClusterSSHPreparationReceiptVersion(payload, proof, 2)
				if err != nil || len(receipt.Targets) != len(snapshot.credentials) {
					t.Fatal("missing complete v2 receipt", err)
				}
			}
			pending, err := f.c.store.pendingClusterSSHPreparation(f.ctx, sourceNode)
			if err != nil || pending.Operation.ID != "" {
				t.Fatal("settled or uncertain work redispatched", err)
			}
		})
	}
}

func TestClusterSSHPreparationControllerPostgresWaiting(t *testing.T) {
	for _, mode := range []string{"missing gates", "missing confirmation", "expired inspection", "changed confirmation"} {
		t.Run(mode, func(t *testing.T) {
			f, snapshot, _, _ := newSSHPreparationApprovalFixture(t, false)
			if mode == "missing confirmation" || mode == "changed confirmation" {
				var payload map[string]json.RawMessage
				if json.Unmarshal([]byte(snapshot.work.Payload), &payload) != nil {
					t.Fatal("payload")
				}
				if mode == "missing confirmation" {
					delete(payload, "ssh_preparation_request")
				} else {
					payload["ssh_preparation_request"] = json.RawMessage(`{}`)
				}
				raw, _ := json.Marshal(payload)
				f.exec(t, `UPDATE engine.cluster_operations SET payload_json=$2 WHERE id=$1`, f.op.ID, string(raw))
			}
			if mode == "expired inspection" {
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET inspected_at=1 WHERE operation_id=$1`, f.op.ID)
			}
			before := f.events(t)
			op, ok, err := f.c.claimOperation(f.ctx)
			if err != nil || !ok || op.State != "waiting" || op.CurrentStep != clusterSSHQualificationStep {
				t.Fatal("waiting proposal not selected intact", err)
			}
			for pass := 0; pass < 2; pass++ {
				if err := f.c.runSSHPreparationParent(f.ctx, op, f.observe); err != nil {
					t.Fatal("waiting reconcile", err)
				}
			}
			if f.events(t) != before+1 {
				t.Fatal("unchanged blocker emitted repeated events")
			}
			var state, step, reason, payload string
			if f.c.store.db.QueryRowContext(f.ctx, `SELECT state,current_step,error_text,payload_json FROM engine.cluster_operations WHERE id=$1`, f.op.ID).Scan(&state, &step, &reason, &payload) != nil {
				t.Fatal("read state")
			}
			if state != "waiting" || step != clusterSSHQualificationStep || reason == "" || strings.Contains(payload, "ssh_preparation_authorization") {
				t.Fatal("blocked preparation advanced", state, step, reason)
			}
			if mode == "missing gates" && (!strings.Contains(reason, "clean_targets") || !strings.Contains(reason, "persistent_storage") || strings.Contains(reason, "complete_capacity")) {
				t.Fatal("missing actionable native blockers", reason)
			}
			var claimed int
			if f.c.store.db.QueryRowContext(f.ctx, `SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=$1 AND (state<>'queued' OR current_step<>'qualification_complete' OR lease_holder<>'' OR lease_expires_at<>0)`, f.op.ID).Scan(&claimed) != nil || claimed != 0 {
				t.Fatal("waiting path claimed target")
			}
		})
	}
}

func TestClusterSSHPreparationControllerPostgresApprovalDispatchFence(t *testing.T) {
	t.Run("release phase binding", testPreparationReleasePhaseBinding)
	t.Run("composed expansion", func(t *testing.T) { testSSHPreparationComposedDispatch(t, false) })
	t.Run("composed storage shortage", func(t *testing.T) { testSSHPreparationComposedDispatch(t, true) })
	for _, mode := range []string{"pair", "replacement", "cleanup failure", "owner changed", "cancelled cleanup"} {
		t.Run(mode, func(t *testing.T) {
			f, snapshot, confirmation, _ := newSSHPreparationApprovalFixture(t, mode == "replacement")
			running := f.op
			running.State, running.CurrentStep = "running", clusterSSHPreparationOperationStep
			sourceNode := ""
			for _, member := range snapshot.work.Proof.Source.Members {
				if member.NodeID == confirmation.IdentitySource.SourceID {
					sourceNode = member.Name
				}
			}
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			scope := func(ctx context.Context, snapshot clusterSSHPreparationApprovalSnapshot, consume func(context.Context, clusterSSHPreparationGates) (clusterSSHPreparationReadinessReceipt, error)) error {
				if f.c.store.db.Stats().InUse != 0 {
					t.Fatal("SQL held across native scope")
				}
				gates := sshPreparationCPUGatesFixture(t, snapshot)
				artifact := gates[0]
				waiting := f.c.preparationCleanTargetsProposalCheck(snapshot)
				gates[0] = func(ctx context.Context, plan string) (string, error) {
					if err := waiting(ctx); err != nil {
						return "", err
					}
					return artifact(ctx, plan)
				}
				receipt, err := consume(ctx, gates)
				if err != nil || receipt.Version != 4 {
					t.Fatal("approval did not consume gates", err)
				}
				if waiting(ctx) == nil {
					t.Fatal("waiting proposal still readable after approval commit")
				}
				terminal, err := f.c.clusterSSHPreparationArtifactTerminal(snapshot, receipt, receipt.Checks[0].SHA256)
				if err != nil || terminal(ctx) != nil {
					t.Fatal("terminal reader did not recognize the exact committed payload")
				}
				// A competing API claimant sees the commit before cleanup joins.
				claimed := make(chan error, 1)
				go func() {
					_, err := f.c.store.claimClusterSSHPreparation(ctx, running, f.c.holder, newClusterUUID())
					claimed <- err
				}()
				if <-claimed == nil {
					t.Fatal("claim crossed live artifact cleanup")
				}
				pending, err := f.c.store.pendingClusterSSHPreparation(ctx, sourceNode)
				if err != nil || pending.Operation.ID != "" {
					t.Fatal("pending exposed unclosed scope", err)
				}
				if mode == "cleanup failure" {
					return errClusterConflict
				}
				if mode == "owner changed" {
					f.exec(t, `UPDATE engine.cluster_application_leases SET holder='replacement-controller' WHERE name=$1`, clusterControllerLeaseName)
				}
				if mode == "cancelled cleanup" {
					cancel()
				}
				return nil
			}
			err := f.c.runSSHPreparationParentWithGates(ctx, f.op, f.observe, scope, sshPreparationCPUReadFixture(t, snapshot))
			success := mode == "pair" || mode == "replacement"
			if (err == nil) != success {
				t.Fatal("cleanup outcome", err)
			}
			var reason string
			if f.c.store.db.QueryRowContext(f.ctx, `SELECT COALESCE(error_text,'') FROM engine.cluster_operations WHERE id=$1`, f.op.ID).Scan(&reason) != nil {
				t.Fatal("cleanup state")
			}
			if success && reason != "" || !success && reason != clusterSSHPreparationCleanupPending {
				t.Fatal("cleanup fence changed", reason)
			}
			if _, ok, err := f.c.claimOperation(f.ctx); mode != "owner changed" && (err != nil || ok) {
				t.Fatal("generic controller reclaimed native preparation", err)
			}
			if !success {
				// A restarted controller cannot infer cleanup success or auto-release.
				if mode != "owner changed" {
					_ = f.c.runSSHPreparationParent(f.ctx, running, f.observe)
				}
				if _, err := f.c.store.claimClusterSSHPreparation(f.ctx, running, f.c.holder, newClusterUUID()); err == nil {
					t.Fatal("uncertain cleanup dispatched after restart")
				}
				return
			}
			pending, err := f.c.store.pendingClusterSSHPreparation(f.ctx, sourceNode)
			if err != nil || pending.Operation.ID != running.ID {
				t.Fatal("approved work not delegated", err)
			}
			wrong, err := f.c.store.pendingClusterSSHPreparation(f.ctx, "unselected-api-node")
			if err != nil || wrong.Operation.ID != "" {
				t.Fatal("unselected source claimed work", err)
			}
			work, err := f.c.store.claimClusterSSHPreparation(f.ctx, pending.Operation, pending.Controller, newClusterUUID())
			if err != nil || len(work.Claims) != len(snapshot.credentials) || work.IdentitySource != confirmation.IdentitySource {
				t.Fatal("exact cohort not claimed", err)
			}
			if _, err := f.c.store.claimClusterSSHPreparation(f.ctx, pending.Operation, pending.Controller, newClusterUUID()); err == nil {
				t.Fatal("uncertain work reclaimed")
			}
		})
	}
}

func TestClusterSSHPreparationControllerPostgresLegacyRouting(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(map[int]string{1: "legacy", 2: "native"}[version], func(t *testing.T) {
			f, _ := newSSHPreparationClaimFixture(t, false)
			f.op.Payload["ssh_preparation"] = map[string]any{"version": version}
			f.exec(t, `UPDATE engine.cluster_operations SET payload_json=$2 WHERE id=$1`, f.op.ID, marshalClusterJSON(f.op.Payload))
			op, ok, err := f.c.claimOperation(f.ctx)
			if err != nil || !ok {
				t.Fatal("preparation routing", err)
			}
			if version == 1 && f.c.runSSHPreparationParent(f.ctx, op, f.observe) == nil {
				t.Fatal("partial legacy receipt bypassed existing recovery validation")
			}
			if version == 2 {
				if err := f.c.runSSHPreparationParent(f.ctx, op, f.observe); err != nil {
					t.Fatal("native receipt failure not recorded", err)
				}
				if _, selected, err := f.c.claimOperation(f.ctx); err != nil || selected {
					t.Fatal("partial native receipt escaped admission fence", err)
				}
			}
		})
	}
}

func TestClusterSSHPreparationControllerPostgresNormalLoop(t *testing.T) {
	f, _, _, _ := newSSHPreparationApprovalFixture(t, false)
	f.c.maxIdleConnections = 2
	if err := f.c.runOnce(f.ctx); err != nil {
		t.Fatal("normal controller loop", err)
	}
	var state, step, reason string
	if f.c.store.db.QueryRowContext(f.ctx, `SELECT state,current_step,COALESCE(error_text,'') FROM engine.cluster_operations WHERE id=$1`, f.op.ID).Scan(&state, &step, &reason) != nil {
		t.Fatal("waiting state")
	}
	if state != "waiting" || step != clusterSSHQualificationStep || !strings.Contains(reason, "source observation is unavailable") {
		t.Fatal("normal controller did not preserve and diagnose waiting proposal", state, step, reason)
	}
}

// Exercise both approval rounds, terminal artifact ownership and worker dispatch
// against the same confirmed SQL proposal. SSH uses pinned fixture servers.
// Kubernetes objects, source-network Job output and artifact bytes are fixtures.
func testSSHPreparationComposedDispatch(t *testing.T, shortage bool) {
	f, snapshot, confirmation, _ := newSSHPreparationApprovalFixture(t, false)
	// The shared SQL seed has its own 15-second setup deadline. Run the parent
	// under the test lifetime so its production proof deadline governs admission.
	f.ctx = t.Context()
	inputs := newSSHWaitingCapacityFixtureForSnapshot(t, snapshot, "override")
	var envelopes []clusterSSHCredentialEnvelope
	for _, sealed := range snapshot.credentials {
		e, err := f.aegis.openClusterSSHCredentials(f.ctx, sealed, sealed.binding)
		if err != nil {
			t.Fatal(err)
		}
		envelopes = append(envelopes, e)
	}
	connect := func(paths []string) clusterSSHPreparationProposalConnect {
		return sshPreparationFilesystemConnect(t, snapshot, f.signers, envelopes, func(index int, raw []byte) []byte {
			if len(paths) == 0 {
				return raw
			}
			var wire struct {
				Version   int                              `json:"version"`
				MachineID string                           `json:"machine_id"`
				BootID    string                           `json:"boot_id"`
				Evidence  clusterremote.FilesystemEvidence `json:"evidence"`
			}
			if err := json.Unmarshal(raw, &wire); err != nil {
				t.Fatal(err)
			}
			wire.Version = 6
			e := &wire.Evidence
			e.Filesystems[0].TotalBytes, e.Filesystems[0].AvailableBytes = 120<<30, 90<<30
			if shortage && index == 1 {
				e.Filesystems[0].AvailableBytes = 1 << 20
			}
			e.PackageCopies = &clusterremote.PackageCopyDemand{Bytes: 2000000, Entries: 2, ProfileSHA256: strings.Repeat("d", 64)}
			root := e.Paths[0]
			root.Ancestor = "/"
			e.Paths = nil
			for _, path := range paths {
				root.Path = path
				e.Paths = append(e.Paths, root)
			}
			encoded, err := json.Marshal(wire)
			if err != nil {
				t.Fatal(err)
			}
			return encoded
		}, nil)
	}
	bytes := &sshApprovalArtifactBytes{digest: strings.Repeat("e", 64)}
	bytes.onDemand = func(ctx context.Context, check func(context.Context) error) ([]clusterSSHFilesystemDemand, error) {
		return []clusterSSHFilesystemDemand{{Path: "/opt/Borealis", Bytes: 8 << 30}, {Path: "/var", Bytes: 600 << 20}}, check(ctx)
	}
	deps := sshApprovalArtifactDependencies(bytes)
	check := f.c.preparationCleanTargetsProposalCheck(snapshot)
	deps.proposal = check
	deps.source = func(ctx context.Context, check func(context.Context) error, projection *clusterSSHPreparationArtifactSource) (clusterSSHPreparationArtifactSource, error) {
		return readClusterSSHPreparationArtifactSource(ctx, snapshot.work.Proof.Source, inputs.get, check, projection)
	}
	deps.terminal = func(r clusterSSHPreparationReadinessReceipt, artifact string) (func(context.Context) error, error) {
		return f.c.clusterSSHPreparationArtifactTerminal(snapshot, r, artifact)
	}
	var projection clusterSSHPreparationTargetsRead
	scope := func(ctx context.Context, current clusterSSHPreparationApprovalSnapshot, consume func(context.Context, clusterSSHPreparationGates) (clusterSSHPreparationReadinessReceipt, error)) error {
		native := newClusterSSHPreparationTargetsRead(current, check, f.aegis, connect(nil))
		clean, targets := clusterSSHPreparationTargetCPUReaderPair(current, native)
		filesystems := func(paths []string) clusterSSHPreparationTargetsRead {
			return newClusterSSHPreparationTargetsFilesystemRead(current, check, f.aegis, connect(paths), paths)
		}
		config, profile := clusterSSHPreparationConfigurationProjectionReader(current.work, check, inputs.get, func(ctx context.Context, m clusterSSHSourceMember) (clusterbootstrap.SourceNetwork, error) {
			return sshSourceNetworkFixture(m, clusterbootstrap.K3sPins().Version), ctx.Err()
		})
		capacity, approved, source := newClusterSSHPreparationCapacityStorageRead(current, check, inputs.get, profile, targets, func(ctx context.Context, window int64) ([]int64, error) {
			return readClusterSSHWorkerCensus(ctx, f.c.store.db, window)
		})
		projection = approved
		return runClusterSSHPreparationArtifacts(ctx, inputs.plan, deps,
			clusterSSHPreparationArtifactConsumerForGates(current, check, clean, capacity, config, source, filesystems, consume))
	}
	err := f.c.runSSHPreparationParentWithGates(f.ctx, f.op, inputs.get, scope, func(ctx context.Context, plan string) (clusterSSHPreparationCleanObservation, error) {
		return projection(ctx, plan)
	})
	if shortage {
		var state, reason string
		if err != nil {
			t.Fatal(err)
		}
		if err := f.c.store.db.QueryRowContext(f.ctx, `SELECT state,error_text FROM engine.cluster_operations WHERE id=$1`, f.op.ID).Scan(&state, &reason); err != nil {
			t.Fatal(err)
		}
		pending, err := f.c.store.pendingClusterSSHPreparation(f.ctx, snapshot.work.Proof.Source.Members[0].Name)
		if state != "waiting" || !strings.Contains(reason, "persistent_storage:") || err != nil || pending.Operation.ID != "" || bytes.closes.Load() != 1 {
			t.Fatalf("unsafe or undiagnosed shortage: state=%s reason=%s pending=%s err=%v", state, reason, pending.Operation.ID, err)
		}
		return
	}
	if err != nil || bytes.closes.Load() != 1 || bytes.proves.Load() != 4 {
		t.Fatalf("approval scope: err=%v closes=%d proofs=%d", err, bytes.closes.Load(), bytes.proves.Load())
	}
	pending, err := f.c.store.pendingClusterSSHPreparation(f.ctx, snapshot.work.Proof.Source.Members[0].Name)
	if err != nil || pending.Operation.ID != f.op.ID {
		t.Fatalf("missing dispatch: %v", err)
	}
	work, err := f.c.store.claimClusterSSHPreparation(f.ctx, pending.Operation, pending.Controller, newClusterUUID())
	if err != nil || len(work.Claims) != 2 || work.IdentitySource != confirmation.IdentitySource {
		t.Fatalf("paired claim: %v", err)
	}
}
