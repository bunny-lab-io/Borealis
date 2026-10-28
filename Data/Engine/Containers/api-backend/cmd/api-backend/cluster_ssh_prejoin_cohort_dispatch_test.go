package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestClusterSSHPrejoinCohortBarrier(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "completed retry", "all completed retry", "first preparation failed", "second preparation failed", "first start sibling drift", "second start sibling drift", "first start authority loss", "second start authority loss", "completed receipt drift", "scope cleanup failed", "scope expected drift", "missing scope", "ignored barrier failure", "start failure", "live sibling custody loss", "first start sibling inputs drift", "second start sibling inputs drift", "sibling inputs drift during proof", "sibling inputs drift after completion", "missing inputs capability"} {
		t.Run(mode, func(t *testing.T) {
			a := sshPrejoinCompletionAuthorityFixture(t, mode == "replacement")
			claims := make([]clusterSSHNetworkTargetClaim, len(a.Cohort.Targets))
			expected := make([]clusterbootstrap.PreparationExpected, len(claims))
			receipts := make([]clusterbootstrap.PrejoinPreparationReceipt, len(claims))
			for i, target := range a.Cohort.Targets {
				claims[i] = clusterSSHNetworkTargetClaim{Lease: a.Lease, Sealed: sealedClusterSSHCredentials{binding: target.Binding}}
				claims[i].Lease.TargetID = target.Binding.TargetID
				var err error
				expected[i], err = buildClusterSSHPreparationExpected(a.Cohort, a.Source, claims[i].Lease, a.Baseline, a.K3sVersion, "10.42.0.0/16", "10.43.0.0/16")
				if err != nil {
					t.Fatal(err)
				}
				receipts[i] = sshPrejoinReceiptIdentityFixture(expected[i], *a.IdentitySource, newClusterUUID(), uint64(42+i))
			}
			configured, completed := make([]bool, len(claims)), make([]bool, len(claims))
			active, opens := make([]int, len(claims)), make([]int, len(claims))
			proofs := make([]int, len(claims))
			starts, drift, inputDrift := 0, -1, -1
			inputCalls := make([]int, len(claims))
			lost, custodyLost := false, false
			if mode == "completed retry" || mode == "all completed retry" {
				completed[0] = true
			}
			if mode == "all completed retry" {
				completed[1] = true
			}
			current := func(ctx context.Context) error {
				if lost || ctx.Err() != nil {
					return clusterbootstrap.ErrSessionAuthority
				}
				return nil
			}
			scope := func(parent context.Context, i int, consume func(context.Context, clusterbootstrap.PreparationExpected, clusterSSHPrejoinNativeTarget) error) error {
				opens[i]++
				if mode == "missing scope" && i == len(claims)-1 {
					return nil
				}
				ctx, cancel := context.WithCancel(parent)
				defer cancel()
				active[i]++
				defer func() { active[i]-- }()
				target := clusterSSHPrejoinNativeTarget{current: func(ctx context.Context) error {
					if active[i] == 0 || custodyLost && i == 0 {
						return clusterbootstrap.ErrSessionAuthority
					}
					return current(ctx)
				}}
				target.inputs = func(ctx context.Context) error {
					inputCalls[i]++
					if active[i] != 1 || inputDrift == i {
						return clusterbootstrap.ErrSessionAuthority
					}
					return current(ctx)
				}
				if mode == "missing inputs capability" && i == 1 {
					target.inputs = nil
				}
				target.run = func(ctx context.Context, preparation bool, proofStep int, beforeStart, check func(context.Context) error, output *clusterbootstrap.PrejoinPreparationReceipt) error {
					beforeInputs := append([]int(nil), inputCalls...)
					if current(ctx) != nil || check == nil || check(ctx) != nil {
						return clusterbootstrap.ErrSessionAuthority
					}
					if !reflect.DeepEqual(beforeInputs, inputCalls) {
						t.Fatal("full input check entered native authority callback")
					}
					if preparation {
						if starts != 0 || beforeStart != nil || proofStep != -1 {
							t.Fatal("preparation invoked after startup")
						}
						if mode == "first preparation failed" && i == 0 || mode == "second preparation failed" && i == 1 {
							return clusterbootstrap.ErrMutationUnknown
						}
						configured[i] = true
						if completed[i] {
							*output = receipts[i]
						}
						return nil
					}
					for j := range claims {
						if !configured[j] || active[j] != 1 {
							t.Fatal("startup/proof lacked configured sibling and live retained scope")
						}
					}
					if proofStep >= 0 {
						if drift == i || proofStep == 7 && completed[i] || proofStep == 10 && !completed[i] {
							return clusterbootstrap.ErrMutationUnknown
						}
						proofs[i]++
						if mode == "sibling inputs drift during proof" && i == 0 && proofs[i] == 2 {
							inputDrift = 1
						}
						if completed[i] {
							*output = receipts[i]
							if mode == "completed receipt drift" {
								output.MemberID++
							}
						}
						return nil
					}
					if beforeStart == nil || completed[i] {
						t.Fatal("missing immediate barrier or completed target replay")
					}
					if mode == "first start sibling drift" || mode == "ignored barrier failure" {
						drift = 1
					}
					if mode == "second start sibling drift" && i == 1 {
						drift = 0
					}
					if mode == "first start authority loss" || mode == "second start authority loss" && i == 1 {
						lost = true
					}
					if mode == "first start sibling inputs drift" {
						inputDrift = 1
					}
					if mode == "second start sibling inputs drift" && i == 1 {
						inputDrift = 0
					}
					before := append([]int(nil), proofs...)
					if err := beforeStart(ctx); err != nil {
						if mode == "ignored barrier failure" {
							*output = receipts[i]
							return nil
						}
						return err
					}
					for j := range claims {
						if proofs[j] != before[j]+1 {
							t.Fatal("cached proof replaced immediate whole-cohort reproof")
						}
					}
					if mode == "live sibling custody loss" {
						custodyLost = true
					}
					if check(ctx) != nil {
						return clusterbootstrap.ErrSessionAuthority
					}
					starts++
					if mode == "start failure" {
						return clusterbootstrap.ErrMutationUnknown
					}
					completed[i] = true
					if mode == "sibling inputs drift after completion" {
						inputDrift = 1
					}
					*output = receipts[i]
					return nil
				}
				e := expected[i]
				if mode == "scope expected drift" && opens[i] > 1 {
					e.Target.Generation++
				}
				err := consume(ctx, e, target)
				if mode == "scope cleanup failed" && starts > 0 {
					return clusterbootstrap.ErrSessionAuthority
				}
				return err
			}
			result, err := dispatchClusterSSHPrejoinCohort(context.Background(), claims, a.Baseline, *a.IdentitySource, scope, current)
			success := mode == "pair" || mode == "replacement" || mode == "completed retry" || mode == "all completed retry"
			if (err == nil) != success {
				t.Fatal("incorrect cohort outcome", err)
			}
			if success {
				if len(result) != len(claims) {
					t.Fatal("missing final cohort")
				}
				for i, value := range result {
					if value.Receipt != receipts[i] || !reflect.DeepEqual(value.Expected, expected[i]) {
						t.Fatal("changed completion binding")
					}
				}
			} else if result != nil {
				t.Fatal("failed cohort returned partial success")
			}
			for _, count := range active {
				if count != 0 {
					t.Fatal("retained scope escaped")
				}
			}
			wantStarts := 0
			switch mode {
			case "pair":
				wantStarts = 2
			case "replacement", "completed retry", "second start sibling drift", "second start authority loss", "completed receipt drift", "scope cleanup failed", "start failure", "second start sibling inputs drift", "sibling inputs drift after completion":
				wantStarts = 1
			}
			if starts != wantStarts {
				t.Fatalf("unexpected membership effects: %d, want %d", starts, wantStarts)
			}
		})
	}
}

func TestClusterSSHPrejoinCohortNestedIdentityScopes(t *testing.T) {
	for _, drift := range []bool{false, true} {
		t.Run(map[bool]string{false: "retained pair", true: "inner custody drift"}[drift], func(t *testing.T) {
			root, consumer, source, proof := sshPrejoinScopeFixture(t)
			proofs := [2]clusterSSHPrejoinSourceCustody{proof, proof}
			calls := [2]int{}
			read := func(i int) func(context.Context) (clusterSSHPrejoinSourceCustody, error) {
				return func(ctx context.Context) (clusterSSHPrejoinSourceCustody, error) {
					calls[i]++
					return proofs[i], ctx.Err()
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var escaped func(context.Context) error
			innerUsed := false
			err := withClusterSSHPrejoinSourceMaterial(ctx, consumer, root, source, read(0),
				func(ctx context.Context, outer *engineidentity.Material, outerCurrent func(context.Context) error) error {
					// Both scopes use the same actual process consumer and directory.
					// LoadFrom reuses LOCK_SH; no new flock or scope-long mutex.
					err := withClusterSSHPrejoinSourceMaterial(ctx, consumer, root, source, read(1),
						func(ctx context.Context, inner *engineidentity.Material, innerCurrent func(context.Context) error) error {
							innerUsed, escaped = true, innerCurrent
							if !outer.Equal(inner) || outerCurrent(ctx) != nil || innerCurrent(ctx) != nil {
								t.Fatal("nested shared consumer lost independent current proof")
							}
							if drift {
								proofs[1].AuthoritySHA256 = strings.Repeat("b", 64)
								if innerCurrent(ctx) == nil {
									t.Fatal("inner source drift passed")
								}
							}
							return nil // Scope must latch ignored sibling failure.
						})
					if err != nil {
						return err
					}
					return outerCurrent(ctx)
				})
			if !innerUsed || (err == nil) == drift || ctx.Err() != nil {
				t.Fatal("nested identity scopes failed or exceeded bounded lifetime", err, ctx.Err())
			}
			before := calls
			if escaped == nil || escaped(context.Background()) == nil || calls != before {
				t.Fatal("closed nested identity scope retained source access")
			}
			if _, err := consumer.LoadFrom(root); err != nil {
				t.Fatal("nested scope closed process-owned shared consumer", err)
			}
		})
	}
}
