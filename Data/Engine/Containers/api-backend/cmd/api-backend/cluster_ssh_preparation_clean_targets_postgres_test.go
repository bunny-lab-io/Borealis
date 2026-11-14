package main

import (
	"borealis/api-backend/internal/clusterremote"
	"context"
	"strings"
	"testing"
)

func TestClusterSSHPreparationCleanTargetsPostgres(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "controller", "controller expiry", "attempt", "running phase", "active operation", "target generation", "target holder", "target report", "credential", "peer credential expiry", "source release", "source drain", "changed intent", "Aegis locked", "during native controller", "during native credential", "during native attempt", "during close controller"} {
		t.Run(mode, func(t *testing.T) {
			f, snapshot, _, _ := newSSHPreparationApprovalFixture(t, mode == "replacement")
			f.c.store.db.SetMaxOpenConns(1)
			plan, _ := clusterSSHPreparationPlanDigest(snapshot.work.Authorization)
			before := f.events(t)
			mutate := func(name string) {
				switch name {
				case "controller":
					f.exec(t, `UPDATE engine.cluster_application_leases SET holder='foreign-controller' WHERE name=$1`, clusterControllerLeaseName)
				case "controller expiry":
					f.exec(t, `UPDATE engine.cluster_application_leases SET expires_at=1 WHERE name=$1`, clusterControllerLeaseName)
				case "attempt":
					f.exec(t, `UPDATE engine.cluster_operations SET attempt=attempt+1 WHERE id=$1`, f.op.ID)
				case "running phase":
					f.exec(t, `UPDATE engine.cluster_operations SET state='running',current_step='prepare_ssh_targets' WHERE id=$1`, f.op.ID)
				case "active operation":
					f.exec(t, `UPDATE engine.cluster_state SET active_operation_id=NULL WHERE id=1`)
				case "target generation":
					f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_generation=lease_generation+1 WHERE id=$1`, f.targets[0].Binding.TargetID)
				case "target holder":
					f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_holder=$2 WHERE id=$1`, f.targets[0].Binding.TargetID, newClusterUUID())
				case "target report":
					f.exec(t, `UPDATE engine.cluster_onboarding_targets SET inspection_json='{}' WHERE id=$1`, f.targets[0].Binding.TargetID)
				case "credential":
					f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, f.targets[0].Binding.TargetID)
				case "peer credential expiry":
					f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET expires_at=1 WHERE target_id=$1`, f.targets[1].Binding.TargetID)
				case "source release":
					f.exec(t, `UPDATE engine.cluster_state SET baseline_release='2026.09.998-rc.1' WHERE id=1`)
				case "source drain":
					f.exec(t, `UPDATE engine.cluster_nodes SET application_state='draining' WHERE id=$1`, snapshot.work.Proof.Source.Members[0].NodeID)
				case "changed intent":
					f.exec(t, `UPDATE engine.cluster_operations SET payload_json=jsonb_set(payload_json::jsonb,'{ssh_preparation_request,request_id}',to_jsonb($2::text))::text WHERE id=$1`, f.op.ID, newClusterUUID())
				case "Aegis locked":
					f.aegis.mu.Lock()
					clear(f.aegis.key)
					f.aegis.key = nil
					f.aegis.mu.Unlock()
				}
			}
			checkPool := func() {
				t.Helper()
				if f.c.store.db.Stats().InUse != 0 {
					t.Fatal("crypto or SSH retained PostgreSQL connection")
				}
				var one int
				if err := f.c.store.db.QueryRowContext(f.ctx, `SELECT 1`).Scan(&one); err != nil || one != 1 {
					t.Fatal("sole pool connection unavailable", err)
				}
			}
			aegis := sshPreparationProposalTestAegis{
				open: func(ctx context.Context, sealed sealedClusterSSHCredentials, binding clusterSSHCredentialBinding) (clusterSSHCredentialEnvelope, error) {
					checkPool()
					value, err := f.aegis.openClusterSSHCredentials(ctx, sealed, binding)
					checkPool()
					return value, err
				},
				verify: f.aegis.verifyClusterSSHGeneration,
			}
			reads, connects, closes := 0, 0, 0
			connect := func(ctx context.Context, target clusterremote.Target, key clusterremote.HostKey, credential *clusterremote.Credential) (clusterSSHPreparationProposalClient, error) {
				checkPool()
				connects++
				index := (connects - 1) % len(snapshot.credentials)
				expected := snapshot.work.Proof.Cohort.Targets[index]
				if target.Address != expected.Binding.Address || target.Port != expected.Binding.Port || key.Fingerprint != expected.Key.Fingerprint || credential == nil {
					t.Fatal("native connection lost original target")
				}
				return &sshPreparationProposalTestClient{
					inspect: func(ctx context.Context, _ []byte, request clusterremote.PreparationProposal, current func(context.Context) error) (clusterremote.PreparationProposalProof, error) {
						checkPool()
						reads++
						if request.TargetID != expected.Binding.TargetID || request.OperationID != f.op.ID || request.OperationAttempt != f.op.Attempt ||
							request.QualificationGeneration != snapshot.work.Authorization.Targets[index].QualificationGeneration || current(ctx) != nil {
							t.Fatal("native proposal lost reserved generation")
						}
						if strings.HasPrefix(mode, "during native ") {
							mutate(strings.TrimPrefix(mode, "during native "))
						}
						// Return apparently successful native evidence even after losing
						// authority; the adapter must independently reject it.
						return sshPreparationProposalTestProof(request, strings.Repeat("a", 64)), nil
					},
					close: func() error {
						checkPool()
						closes++
						if mode == "during close controller" {
							mutate("controller")
						}
						return nil
					},
				}, nil
			}
			read := newClusterSSHPreparationCleanTargetsRead(snapshot, f.c.preparationCleanTargetsProposalCheck(snapshot), aegis, connect)
			if !strings.HasPrefix(mode, "during ") {
				mutate(mode)
			}
			value, err := read(f.ctx, plan)
			positive := mode == "pair" || mode == "replacement"
			if (err == nil) != positive || err != nil && value != "" {
				t.Fatal("unexpected proposal proof", err)
			}
			if positive {
				if reads != 2*len(snapshot.credentials) || !validClusterSSHPreparationDigest(value) {
					t.Fatal("incomplete native rounds", reads)
				}
				after, _, err := f.c.loadClusterSSHPreparationApproval(f.ctx, f.op)
				if err != nil || marshalClusterJSON(after.work) != marshalClusterJSON(snapshot.work) {
					t.Fatal("reader mutated waiting proposal", err)
				}
				for i, target := range snapshot.work.Proof.Cohort.Targets {
					var state, step, holder string
					var generation, expiry int64
					if err := f.c.store.db.QueryRowContext(f.ctx, `SELECT state,current_step,lease_holder,lease_generation,lease_expires_at FROM engine.cluster_onboarding_targets WHERE id=$1`, target.Binding.TargetID).Scan(&state, &step, &holder, &generation, &expiry); err != nil ||
						state != "queued" || step != "qualification_complete" || holder != "" || expiry != 0 || generation != snapshot.work.Authorization.Targets[i].QualificationGeneration {
						t.Fatal("read manufactured worker claim", err)
					}
				}
			} else if !strings.HasPrefix(mode, "during ") && reads != 0 {
				t.Fatal("stale proposal reached native transport")
			}
			if connects != closes || f.events(t) != before {
				t.Fatal("unclosed client or emitted event", connects, closes)
			}
		})
	}
}
