package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"strings"
	"testing"
)

func TestClusterSSHPrejoinCompletionPostgres(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "missing Node", "changed Node", "authority lost", "legacy relabel"} {
		t.Run(mode, func(t *testing.T) {
			f, legacy := newSSHPreparationCompletionFixture(t, mode == "replacement")
			var targets []clusterSSHPrejoinCompletion
			uids := map[string]string{}
			for i, value := range legacy {
				a := value.Authority
				expected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, a.Lease, a.Baseline, a.K3sVersion, "10.42.0.0/16", "10.43.0.0/16")
				if err != nil {
					t.Fatal(err)
				}
				receipt := sshPrejoinReceiptIdentityFixture(expected, *a.IdentitySource, newClusterUUID(), uint64(i+42))
				targets = append(targets, clusterSSHPrejoinCompletion{PayloadSHA256: a.PayloadSHA256, Claim: clusterSSHNetworkTargetClaim{Lease: a.Lease, Sealed: value.Sealed}, Expected: expected, Receipt: receipt})
				uids[a.Lease.TargetID] = receipt.Node.NodeUID
			}
			if mode == "missing Node" {
				delete(uids, targets[0].Claim.Lease.TargetID)
			}
			if mode == "changed Node" {
				uids[targets[0].Claim.Lease.TargetID] = newClusterUUID()
			}
			if mode == "legacy relabel" {
				targets[0].Receipt.Steps[10].ResultSHA256 = strings.Repeat("f", 64)
			}
			reader := sshPrejoinCompletionNodes(legacy[0].Authority, uids)
			getJSON := func(ctx context.Context, path string, out any) error {
				if f.c.store.db.Stats().InUse != 0 {
					t.Fatal("SQL connection retained during independent Node proof")
				}
				if mode == "authority lost" {
					f.exec(t, `UPDATE engine.cluster_application_leases SET expires_at=0 WHERE name=$1`, clusterControllerLeaseName)
				}
				return reader(ctx, path, out)
			}
			before := f.events(t)
			err := f.c.store.completeClusterSSHPrejoinPreparation(f.ctx, f.aegis, targets, getJSON)
			success := mode == "pair" || mode == "replacement"
			if (err == nil) != success {
				t.Fatal("incorrect prejoin settlement", err)
			}
			if !success {
				if f.events(t) != before {
					t.Fatal("failed proof committed event")
				}
				return
			}
			if f.events(t) != before+1 {
				t.Fatal("missing single cohort event")
			}
			var payload string
			if f.c.store.db.QueryRowContext(f.ctx, `SELECT payload_json FROM engine.cluster_operations WHERE id=$1`, f.op.ID).Scan(&payload) != nil {
				t.Fatal("missing stored receipt")
			}
			proof, _, err := parseClusterSSHPreparationInspection([]byte(payload), legacy[0].Authority.Lease, f.baseline)
			if err != nil {
				t.Fatal(err)
			}
			receipt, err := parseClusterSSHPreparationReceiptVersion(payload, proof, 2)
			if err != nil || len(receipt.Targets) != len(targets) {
				t.Fatal("incomplete version2 receipt", err)
			}
			if _, err := f.c.loadClusterSSHIdentityRecoveryReservation(f.ctx, f.op); err == nil {
				t.Fatal("activated identity dispatched legacy repair")
			}
			for i, target := range targets {
				var state, step, holder string
				if f.c.store.db.QueryRowContext(f.ctx, `SELECT state,current_step,lease_holder FROM engine.cluster_onboarding_targets WHERE id=$1`, target.Claim.Lease.TargetID).Scan(&state, &step, &holder) != nil || state != "prepared" || step != clusterSSHPreparationCompleteStep || holder != "" {
					t.Fatal("cohort did not settle atomically")
				}
				if receipt.Targets[i].Prejoin.Receipt != target.Receipt || receipt.Targets[i].JoinClusterSHA256 != "" {
					t.Fatal("native proof changed or legacy semantics used")
				}
			}
		})
	}
}

func TestClusterSSHPrejoinWorkerPostgres(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "dispatch failure", "cleanup failure", "ignored boundary failure", "source drift", "sibling drift"} {
		t.Run(mode, func(t *testing.T) {
			f, work := newSSHPreparationWorkerFixture(t, mode == "replacement")
			uids := map[string]string{}
			var authority clusterSSHPreparationAuthority
			calls := 0
			var escaped func(context.Context) error
			dispatch := func(ctx context.Context, claim clusterSSHNetworkTargetClaim, baseline clusterbootstrap.Expected, confirmation string, source engineidentity.Binding, check func(context.Context) error) (clusterSSHPrejoinDispatchResult, error) {
				if claim != work.Claims[calls] || source != work.IdentitySource || baseline != work.Baseline || confirmation != work.Authorization.ConfirmationSHA256 || f.c.store.db.Stats().InUse != 0 {
					t.Fatal("prejoin dispatch lost exact private binding")
				}
				calls++
				escaped = check
				a, err := newClusterSSHPreparationAuthorityRead(f.c.store, f.aegis, claim.Lease, baseline, claim.Sealed)(ctx)
				if err != nil || check(ctx) != nil {
					return clusterSSHPrejoinDispatchResult{}, errClusterConflict
				}
				authority = a
				expected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, a.Lease, baseline, a.K3sVersion, "10.42.0.0/16", "10.43.0.0/16")
				if err != nil {
					return clusterSSHPrejoinDispatchResult{}, err
				}
				receipt := sshPrejoinReceiptIdentityFixture(expected, source, newClusterUUID(), uint64(41+calls))
				uids[claim.Lease.TargetID] = receipt.Node.NodeUID
				value := clusterSSHPrejoinDispatchResult{Expected: expected, Receipt: receipt}
				if calls == len(work.Claims) {
					switch mode {
					case "dispatch failure", "cleanup failure":
						return value, errClusterConflict
					case "ignored boundary failure":
						failed, cancel := context.WithCancel(ctx)
						cancel()
						_ = check(failed)
					case "source drift":
						value.Receipt.Source.SourceUID = newClusterUUID()
					}
				}
				return value, nil
			}
			reads := 0
			getJSON := func(ctx context.Context, path string, out any) error {
				if escaped == nil || escaped(context.Background()) == nil {
					t.Fatal("settlement ran before dispatch scope cleanup")
				}
				if f.c.store.db.Stats().InUse != 0 {
					t.Fatal("database retained across Node proof")
				}
				if path == "/api/v1/nodes" {
					reads++
					if mode == "sibling drift" && reads >= 7 {
						uids[work.Claims[0].Lease.TargetID] = newClusterUUID()
					}
				}
				return sshPrejoinCompletionNodes(authority, uids)(ctx, path, out)
			}
			worker := newClusterSSHPrejoinPreparationWorker(f.c.store, f.aegis, dispatch, getJSON)
			before := f.events(t)
			err := worker.prepare(f.ctx, work)
			success := mode == "pair" || mode == "replacement"
			if (err == nil) != success {
				t.Fatal("incorrect prejoin worker outcome", err)
			}
			if success {
				if calls != len(work.Claims) || f.events(t) != before+1 {
					t.Fatal("cohort incomplete")
				}
			} else if f.events(t) != before {
				t.Fatal("failed worker settled")
			}
			if _, err := f.c.loadClusterSSHIdentityRecoveryReservation(f.ctx, f.op); err == nil {
				t.Fatal("new worker entered legacy repair")
			}
		})
	}
}
