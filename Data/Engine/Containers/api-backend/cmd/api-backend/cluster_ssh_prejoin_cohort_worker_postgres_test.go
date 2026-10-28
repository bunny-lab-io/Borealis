package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"reflect"
	"testing"
)

func TestClusterSSHPrejoinCohortWorkerPostgres(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "missing sibling", "reordered receipts", "dispatch failure", "ignored authority failure", "wrong generation"} {
		t.Run(mode, func(t *testing.T) {
			f, work := newSSHPreparationWorkerFixture(t, mode == "replacement")
			var authority clusterSSHPreparationAuthority
			uids := map[string]string{}
			var escaped func(context.Context) error
			dispatch := func(ctx context.Context, claims []clusterSSHNetworkTargetClaim, baseline clusterbootstrap.Expected, confirmation string,
				source engineidentity.Binding, check func(context.Context) error) ([]clusterSSHPrejoinDispatchResult, error) {
				if !reflect.DeepEqual(claims, work.Claims) || baseline != work.Baseline || source != work.IdentitySource || confirmation != work.Authorization.ConfirmationSHA256 || f.c.store.db.Stats().InUse != 0 {
					t.Fatal("cohort dispatch lost exact work or retained SQL connection")
				}
				escaped = check
				var values []clusterSSHPrejoinDispatchResult
				for i, claim := range claims {
					a, err := newClusterSSHPreparationAuthorityRead(f.c.store, f.aegis, claim.Lease, baseline, claim.Sealed)(ctx)
					if err != nil || check(ctx) != nil {
						return nil, errClusterConflict
					}
					authority = a
					expected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, a.Lease, baseline, a.K3sVersion, "10.42.0.0/16", "10.43.0.0/16")
					if err != nil {
						return nil, err
					}
					receipt := sshPrejoinReceiptIdentityFixture(expected, source, newClusterUUID(), uint64(42+i))
					uids[claim.Lease.TargetID] = receipt.Node.NodeUID
					values = append(values, clusterSSHPrejoinDispatchResult{Expected: expected, Receipt: receipt})
				}
				switch mode {
				case "missing sibling":
					values = values[:1]
				case "reordered receipts":
					values[0], values[1] = values[1], values[0]
				case "dispatch failure":
					return values, errClusterConflict
				case "ignored authority failure":
					failed, cancel := context.WithCancel(ctx)
					cancel()
					_ = check(failed)
				case "wrong generation":
					values[0].Expected.Target.Generation++
				}
				return values, nil
			}
			getJSON := func(ctx context.Context, path string, out any) error {
				if escaped == nil || escaped(context.Background()) == nil || f.c.store.db.Stats().InUse != 0 {
					t.Fatal("settlement preceded scope cleanup or retained SQL connection")
				}
				return sshPrejoinCompletionNodes(authority, uids)(ctx, path, out)
			}
			worker := newClusterSSHPrejoinCohortPreparationWorker(f.c.store, f.aegis, dispatch, getJSON)
			before := f.events(t)
			err := worker.prepare(f.ctx, work)
			success := mode == "pair" || mode == "replacement"
			if (err == nil) != success || success && f.events(t) != before+1 || !success && f.events(t) != before {
				t.Fatal("incorrect whole-cohort settlement", err)
			}
			if _, err := f.c.loadClusterSSHIdentityRecoveryReservation(f.ctx, f.op); err == nil {
				t.Fatal("cohort barrier entered legacy joined recovery")
			}
		})
	}
}
