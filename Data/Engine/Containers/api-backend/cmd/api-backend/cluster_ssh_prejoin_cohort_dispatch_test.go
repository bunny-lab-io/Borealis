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
	for _, mode := range []string{"pair", "replacement", "second prepare fails", "second join fails", "ownership lost", "invalid receipt", "cleanup fails"} {
		t.Run(mode, func(t *testing.T) {
			a := sshPrejoinCompletionAuthorityFixture(t, mode == "replacement")
			claims := make([]clusterSSHNetworkTargetClaim, len(a.Cohort.Targets))
			for i, target := range a.Cohort.Targets {
				claims[i] = clusterSSHNetworkTargetClaim{Lease: a.Lease, Sealed: sealedClusterSSHCredentials{binding: target.Binding}}
				claims[i].Lease.TargetID = target.Binding.TargetID
			}
			var phases []clusterSSHInstallPhase
			opens, closes := 0, 0
			canceled := false
			current := func(ctx context.Context) error {
				if canceled {
					return clusterbootstrap.ErrSessionAuthority
				}
				return ctx.Err()
			}
			scope := func(ctx context.Context, i int, consume func(context.Context, clusterbootstrap.PreparationExpected, clusterSSHPrejoinNativeTarget) error) error {
				opens++
				defer func() { closes++ }()
				expected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, claims[i].Lease, a.Baseline, a.K3sVersion, "10.42.0.0/16", "10.43.0.0/16")
				if err != nil {
					t.Fatal(err)
				}
				run := func(ctx context.Context, phase clusterSSHInstallPhase, receipt *clusterbootstrap.PrejoinPreparationReceipt) error {
					if opens != len(claims) || closes != 0 {
						t.Fatal("input scope escaped")
					}
					phases = append(phases, phase)
					if i == 1 && (mode == "second prepare fails" && phase == prepareBeforeJoin || mode == "second join fails" && phase == joinDrainedServer) {
						return clusterbootstrap.ErrMutationUnknown
					}
					if phase == joinDrainedServer && mode == "ownership lost" {
						canceled = true
					}
					if phase == finishJoinedServer && mode != "invalid receipt" {
						*receipt = sshPrejoinReceiptIdentityFixture(expected, *a.IdentitySource, newClusterUUID(), uint64(i+42))
					}
					return nil
				}
				err = consume(ctx, expected, clusterSSHPrejoinNativeTarget{current: current, run: run})
				if mode == "cleanup fails" {
					return clusterbootstrap.ErrMutationUnknown
				}
				return err
			}
			result, err := dispatchClusterSSHPrejoinCohort(context.Background(), claims, a.Baseline, *a.IdentitySource, scope, current)
			success := mode == "pair" || mode == "replacement"
			if (err == nil) != success || success != (result != nil) || opens != len(claims) || closes != opens {
				t.Fatal("pair outcome", err, phases)
			}
			want := []clusterSSHInstallPhase{prepareBeforeJoin, prepareBeforeJoin, joinDrainedServer, joinDrainedServer, finishJoinedServer, finishJoinedServer}
			switch mode {
			case "replacement":
				want = []clusterSSHInstallPhase{prepareBeforeJoin, joinDrainedServer, finishJoinedServer}
			case "second prepare fails":
				want = want[:2]
			case "ownership lost":
				want = want[:3]
			case "second join fails":
				want = want[:4]
			case "invalid receipt":
				want = want[:5]
			}
			if !reflect.DeepEqual(phases, want) {
				t.Fatal("phase order", phases)
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
