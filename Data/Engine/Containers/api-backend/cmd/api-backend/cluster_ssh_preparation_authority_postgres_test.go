package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

type sshPreparationAuthorityFixture struct {
	sshParentFixture
	lease    clusterSSHTargetLease
	baseline clusterbootstrap.Expected
	sealed   sealedClusterSSHCredentials
}

func newSSHPreparationAuthorityFixture(t *testing.T) sshPreparationAuthorityFixture {
	return newSSHPreparationAuthorityFixtureForTopology(t, false)
}

func newSSHPreparationAuthorityFixtureForTopology(t *testing.T, replacement bool) sshPreparationAuthorityFixture {
	t.Helper()
	f := newSSHParentFixture(t)
	if replacement {
		member := clusterSSHSourceMember{Name: f.reports[1].Hostname, Address: f.targets[1].Binding.Address,
			NodeUID: newClusterUUID(), MachineID: f.reports[1].MachineID, BootID: f.reports[1].BootID}
		t.Cleanup(seedAdmissionPeer(t, f.c.store, f.ctx, member.Name, member.Address))
		f.exec(t, `DELETE FROM engine.cluster_onboarding_targets WHERE id=$1`, f.targets[1].Binding.TargetID)
		f.exec(t, `UPDATE engine.cluster_state SET active_size=2,desired_size=3,status='Degraded Quorum' WHERE id=1`)
		original := f.observe
		f.observe = func(ctx context.Context, path string, out any) error {
			if path != "/api/v1/nodes" {
				return original(ctx, path, out)
			}
			var nodes struct {
				Items []map[string]any `json:"items"`
			}
			if err := original(ctx, path, &nodes); err != nil {
				return err
			}
			nodes.Items = append(nodes.Items, sshSourceKubernetesFixture(member))
			raw, _ := json.Marshal(nodes)
			return json.Unmarshal(raw, out)
		}
		f.targets, f.reports = f.targets[:1], f.reports[:1]
	}
	old := f.complete(t, 0)
	if len(f.targets) > 1 {
		f.complete(t, 1)
	}
	baseline := clusterbootstrap.Expected{Repository: "bunny-lab-io/Borealis", Release: "2026.09.999-rc.1", SourceSHA: strings.Repeat("a", 40), AllowQualification: true}
	f.op.Payload = map[string]any{"inspection_only": true, "baseline_release": baseline.Release, "baseline_sha": baseline.SourceSHA, "source_k3s_version": "v1.36.3+k3s1", "target_count": len(f.targets)}
	f.exec(t, `UPDATE engine.cluster_state SET baseline_release=$1,baseline_sha=$2 WHERE id=1`, baseline.Release, baseline.SourceSHA)
	f.exec(t, `UPDATE engine.cluster_operations SET payload_json=$2 WHERE id=$1`, f.op.ID, marshalClusterJSON(f.op.Payload))
	if err := f.c.runSSHInspectionParent(f.ctx, f.op, f.observe); err != nil {
		t.Fatal(err)
	}
	if !replacement {
		f.assertState(t, "waiting", clusterSSHQualificationStep, true)
	}
	// Reserved preparation phase exists only in this isolated fixture. Production
	// claiming still rejects it; these tests do not implement a transition.
	f.exec(t, `UPDATE engine.cluster_operations SET state='running',current_step='prepare_ssh_targets' WHERE id=$1`, f.op.ID)
	f.exec(t, `UPDATE engine.cluster_onboarding_targets SET current_step='stage_source' WHERE operation_id=$1`, f.op.ID)
	lease := old
	lease.Holder = newClusterUUID()
	lease.Generation++
	lease.Step = "stage_source"
	lease.OperationStep = clusterSSHPreparationOperationStep
	f.exec(t, `UPDATE engine.cluster_onboarding_targets SET state='running',lease_holder=$2,lease_generation=$3,lease_expires_at=$4 WHERE id=$1`, lease.TargetID, lease.Holder, lease.Generation, time.Now().Unix()+300)
	return sshPreparationAuthorityFixture{sshParentFixture: f, lease: lease, baseline: baseline, sealed: f.targets[0].Sealed}
}

func (f sshPreparationAuthorityFixture) read(ctx context.Context) (clusterSSHPreparationAuthority, error) {
	return newClusterSSHPreparationAuthorityRead(f.c.store, f.aegis, f.lease, f.baseline, f.sealed)(ctx)
}

func TestClusterSSHPreparationAuthorityPostgresReadsOutsideConnection(t *testing.T) {
	for _, topology := range []string{"expansion", "replacement"} {
		t.Run(topology, func(t *testing.T) {
			f := newSSHPreparationAuthorityFixtureForTopology(t, topology == "replacement")
			before := f.events(t)
			authority, err := f.read(f.ctx)
			if err != nil || authority.Lease != f.lease || authority.Baseline != f.baseline || authority.Cohort.Targets[0].Generation != f.lease.Generation-1 || authority.Cohort.Targets[0].Attempt != f.lease.OperationAttempt {
				t.Fatalf("authority read: %v", err)
			}
			if f.c.store.db.Stats().InUse != 0 {
				t.Fatal("authority held database connection")
			}
			get := func(ctx context.Context, path string, out any) error {
				if f.c.store.db.Stats().InUse != 0 {
					t.Fatal("source observation held database connection")
				}
				if path != clusterSSHRuntimeSecretPath {
					return f.observe(ctx, path, out)
				}
				raw, _ := json.Marshal(sshPreparationSecretFixture())
				return json.Unmarshal(raw, out)
			}
			network := func(ctx context.Context, member clusterSSHSourceMember) (clusterbootstrap.SourceNetwork, error) {
				if f.c.store.db.Stats().InUse != 0 {
					t.Fatal("network observation held database connection")
				}
				return clusterbootstrap.SourceNetwork{NodeUID: member.NodeUID, Hostname: member.Name, MachineID: member.MachineID, BootID: member.BootID, K3sVersion: "v1.36.3+k3s1", PodCIDR: "10.42.0.0/16", ServiceCIDR: "10.43.0.0/16", ManagementLink: clusterbootstrap.ManagementLink{Interface: "ens18", Index: 2, Address: member.Address + "/24", MAC: "02:00:00:00:00:01", NetworkNamespace: 1234}}, nil
			}
			read := newClusterSSHPreparationSourceRead(newClusterSSHPreparationAuthorityRead(f.c.store, f.aegis, f.lease, f.baseline, f.sealed), get, network)
			expected, settings, err := read(f.ctx)
			if err != nil || expected.Target.Generation != f.lease.Generation || !reflect.DeepEqual(settings, sshPreparationRuntimeFixture()) {
				t.Fatalf("source integration: %v", err)
			}
			if _, _, err := read(f.ctx); err != nil {
				t.Fatal("fresh repeated read failed")
			}
			if f.events(t) != before {
				t.Fatal("read published operation event")
			}
			if len(f.targets) > 1 {
				if _, err := f.c.store.claimClusterSSHTarget(f.ctx, f.op.ID, f.targets[1].Binding.TargetID, newClusterUUID()); err == nil {
					t.Fatal("adapter enabled preparation claiming")
				}
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET state='running',lease_holder=$2,lease_generation=inspected_generation+1,lease_expires_at=$3 WHERE id=$1`, f.targets[1].Binding.TargetID, newClusterUUID(), time.Now().Unix()+60)
				if _, err := f.read(f.ctx); err != nil {
					t.Fatal("concurrently owned second source staging rejected")
				}
			}
			f.aegis.mu.Lock()
			clear(f.aegis.key)
			f.aegis.key = nil
			f.aegis.mu.Unlock()
			if _, err := f.read(f.ctx); err != clusterbootstrap.ErrPreparationConfig {
				t.Fatal("locked Aegis accepted")
			}
		})
	}
}

func TestClusterSSHPreparationAuthorityPostgresRejectsChangedCohort(t *testing.T) {
	for _, mode := range []string{"controller holder", "controller expiry", "operation phase", "operation attempt", "active operation", "target holder", "target generation", "target expiry", "own ciphertext", "other credential missing", "other credential expiry", "other Aegis generation", "other report", "other report generation", "stale report", "other recovery", "other joined", "missing target", "source release", "source K3s", "source VIP", "source drained", "source members", "unknown proof", "proof replaced"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHPreparationAuthorityFixture(t)
			other := f.targets[1].Binding.TargetID
			switch mode {
			case "controller holder":
				f.exec(t, `UPDATE engine.cluster_application_leases SET holder=$2 WHERE name=$1`, clusterControllerLeaseName, "other-"+f.c.holder)
			case "controller expiry":
				f.exec(t, `UPDATE engine.cluster_application_leases SET expires_at=1 WHERE name=$1`, clusterControllerLeaseName)
			case "operation phase":
				f.exec(t, `UPDATE engine.cluster_operations SET current_step='inspect_ssh_targets' WHERE id=$1`, f.op.ID)
			case "operation attempt":
				f.exec(t, `UPDATE engine.cluster_operations SET attempt=attempt+1 WHERE id=$1`, f.op.ID)
			case "active operation":
				f.exec(t, `UPDATE engine.cluster_state SET active_operation_id=NULL WHERE id=1`)
			case "target holder":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_holder=$2 WHERE id=$1`, f.lease.TargetID, newClusterUUID())
			case "target generation":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_generation=lease_generation+1 WHERE id=$1`, f.lease.TargetID)
			case "target expiry":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE id=$1`, f.lease.TargetID)
			case "own ciphertext":
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=$2 WHERE target_id=$1`, f.lease.TargetID, f.targets[1].Sealed.ciphertext)
			case "other credential missing":
				f.exec(t, `DELETE FROM engine.cluster_onboarding_credentials WHERE target_id=$1`, other)
			case "other credential expiry":
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET expires_at=1 WHERE target_id=$1`, other)
			case "other Aegis generation":
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET aegis_generation='synthetic-changed-generation' WHERE target_id=$1`, other)
			case "other report":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET inspection_json='{}' WHERE id=$1`, other)
			case "other report generation":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET inspected_generation=inspected_generation+1 WHERE id=$1`, other)
			case "stale report":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET inspected_at=1 WHERE id=$1`, other)
			case "other recovery":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET state='recovery_required' WHERE id=$1`, other)
			case "other joined":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET state='joined' WHERE id=$1`, other)
			case "missing target":
				f.exec(t, `DELETE FROM engine.cluster_onboarding_targets WHERE id=$1`, other)
			case "source release":
				f.exec(t, `UPDATE engine.cluster_state SET baseline_release='2026.09.998-rc.1' WHERE id=1`)
			case "source K3s":
				f.exec(t, `UPDATE engine.cluster_state SET config_json='{"k3s_version":"v1.36.4+k3s1"}' WHERE id=1`)
			case "source VIP":
				f.exec(t, `UPDATE engine.cluster_state SET edge_vip='192.168.90.249' WHERE id=1`)
			case "source drained":
				f.exec(t, `UPDATE engine.cluster_nodes SET application_state='draining' WHERE membership_state='Active'`)
			case "source members":
				f.exec(t, `UPDATE engine.cluster_state SET active_size=2 WHERE id=1`)
			case "unknown proof":
				f.exec(t, `UPDATE engine.cluster_operations SET payload_json=jsonb_set(payload_json::jsonb,'{ssh_inspection,proof,extra}','true')::text WHERE id=$1`, f.op.ID)
			case "proof replaced":
				f.exec(t, `UPDATE engine.cluster_operations SET payload_json='{}' WHERE id=$1`, f.op.ID)
			}
			before := f.events(t)
			if result, err := f.read(f.ctx); err != clusterbootstrap.ErrPreparationConfig || !reflect.DeepEqual(result, clusterSSHPreparationAuthority{}) {
				t.Fatal("changed authority accepted or private diagnostic returned")
			}
			if f.c.store.db.Stats().InUse != 0 || f.events(t) != before {
				t.Fatal("rejected read retained connection or published event")
			}
		})
	}
}

func TestClusterSSHPreparationAuthorityPostgresRechecksAfterLocks(t *testing.T) {
	for _, mode := range []string{"controller first", "payload changed", "other credential expires", "own lease expires", "cancelled wait"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHPreparationAuthorityFixture(t)
			store := f.c.store
			deadline := time.Now().Unix() + 2
			if mode == "other credential expires" {
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET expires_at=$2 WHERE target_id=$1`, f.targets[1].Binding.TargetID, deadline)
			}
			if mode == "own lease expires" {
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=$2 WHERE id=$1`, f.lease.TargetID, deadline)
			}
			store.db.SetMaxOpenConns(3)
			workerCtx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
			defer cancel()
			blocker, err := store.db.BeginTx(f.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback()
			var present int
			watch := "%engine.cluster_application_leases%"
			switch mode {
			case "payload changed":
				watch = "%engine.cluster_operations%"
				err = blocker.QueryRowContext(workerCtx, `SELECT 1 FROM engine.cluster_operations WHERE id=$1 FOR UPDATE`, f.op.ID).Scan(&present)
			case "other credential expires", "own lease expires":
				watch = "%engine.cluster_nodes%"
				err = blocker.QueryRowContext(workerCtx, `SELECT 1 FROM engine.cluster_nodes WHERE membership_state='Active' FOR UPDATE`).Scan(&present)
			default:
				err = blocker.QueryRowContext(workerCtx, `SELECT 1 FROM engine.cluster_application_leases WHERE name=$1 FOR UPDATE`, clusterControllerLeaseName).Scan(&present)
			}
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			consumed := false
			t.Cleanup(func() {
				blocker.Rollback()
				cancel()
				if !consumed {
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Error("authority reader did not stop")
					}
				}
			})
			go func() { _, err := f.read(workerCtx); done <- err }()
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					consumed = true
					t.Fatal("reader completed before authority lock release")
				case <-workerCtx.Done():
					t.Fatal("reader did not reach expected lock")
				case <-ticker.C:
				}
				var waiting bool
				if err := store.db.QueryRowContext(workerCtx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE $1)`, watch).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
			}
			switch mode {
			case "controller first":
				_, err = blocker.ExecContext(workerCtx, `UPDATE engine.cluster_operations SET state='waiting' WHERE id=$1`, f.op.ID)
			case "payload changed":
				_, err = blocker.ExecContext(workerCtx, `UPDATE engine.cluster_operations SET payload_json=jsonb_set(payload_json::jsonb,'{changed}','true')::text WHERE id=$1`, f.op.ID)
			case "cancelled wait":
				cancel()
			default:
				for {
					var expired bool
					if err := store.db.QueryRowContext(workerCtx, `SELECT extract(epoch FROM clock_timestamp())>=$1`, deadline).Scan(&expired); err != nil {
						t.Fatal(err)
					}
					if expired {
						break
					}
					select {
					case <-ticker.C:
					case <-workerCtx.Done():
						t.Fatal("expiry wait exceeded bound")
					}
				}
			}
			if err != nil {
				t.Fatal("reader lock order blocked controller update")
			}
			if mode != "cancelled wait" {
				if err := blocker.Commit(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				consumed = true
				if err != clusterbootstrap.ErrPreparationConfig {
					t.Fatal("stale authority accepted after lock wait")
				}
			case <-time.After(time.Second):
				t.Fatal("reader did not return after release/cancellation")
			}
			blocker.Rollback()
			// database/sql's cancellation goroutine can mark Tx done before its
			// driver rollback returns the connection. Assert bounded release of
			// the actual pool, not an immediate scheduling-dependent snapshot.
			releasedBy := time.NewTimer(time.Second)
			defer releasedBy.Stop()
			for store.db.Stats().InUse != 0 {
				select {
				case <-ticker.C:
				case <-releasedBy.C:
					t.Fatal("failed authority read retained a connection")
				}
			}
		})
	}
}
