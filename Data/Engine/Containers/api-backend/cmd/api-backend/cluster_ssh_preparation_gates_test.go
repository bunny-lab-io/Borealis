package main

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func sshPreparationGatesFixture() clusterSSHPreparationGates {
	var gates clusterSSHPreparationGates
	for i := range gates {
		digest := strings.Repeat(string("abcdef"[i]), 64)
		gates[i] = func(context.Context, string) (string, error) { return digest, nil }
	}
	return gates
}

func TestClusterSSHPreparationGatesRequireLiveCompleteProof(t *testing.T) {
	for _, mode := range []string{"success", "missing", "failed", "invalid", "source changed", "cancelled", "ignored failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			plan := strings.Repeat("a", 64)
			gates := sshPreparationGatesFixture()
			reads, sourceReads, consumed := 0, 0, false
			gates[2] = func(ctx context.Context, got string) (string, error) {
				reads++
				if got != plan {
					t.Error("wrong plan")
				}
				if ctx.Done() == nil {
					t.Error("unbounded observation")
				}
				if mode == "failed" {
					return "", errClusterConflict
				}
				if mode == "invalid" {
					return strings.Repeat("0", 64), nil
				}
				return strings.Repeat("c", 64), nil
			}
			if mode == "missing" {
				gates[5] = nil
			}
			var escaped clusterSSHPreparationApprovalProof
			err := withClusterSSHPreparationApprovalProof(ctx, plan, gates, func(context.Context) error {
				sourceReads++
				if (mode == "source changed" || mode == "ignored failure") && sourceReads == 2 {
					return errClusterConflict
				}
				return nil
			}, func(ctx context.Context, proof clusterSSHPreparationApprovalProof) error {
				consumed = true
				escaped = proof
				if !proof.valid(plan) {
					t.Error("invalid live proof")
				}
				if mode == "cancelled" {
					cancel()
				}
				err := proof.recheck(ctx)
				if mode == "ignored failure" {
					return nil
				}
				return err
			})
			if (err == nil) != (mode == "success") {
				t.Fatal("wrong gate outcome", err)
			}
			if mode == "success" && (!consumed || reads != 1 || sourceReads != 2) {
				t.Fatal("expected one gate run and two source checks")
			}
			if escaped.recheck != nil {
				before := reads
				if escaped.valid(plan) || escaped.recheck(context.Background()) == nil || reads != before {
					t.Fatal("proof escaped closed scope")
				}
			}
		})
	}
}

// Virtual time exercises release-sized I/O without slowing the regression suite.
func TestClusterSSHPreparationApprovalBudgets(t *testing.T) {
	for _, mode := range []string{"slow archive", "bad archive", "native expiry", "commit expiry", "parent expiry", "ignored recheck"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				parent, cancel := context.WithCancel(context.Background())
				defer cancel()
				if mode == "parent expiry" {
					var stop context.CancelFunc
					parent, stop = context.WithTimeout(parent, 20*time.Second)
					defer stop()
				}
				plan := strings.Repeat("a", 64)
				gates := sshPreparationGatesFixture()
				archives := 0
				gates[0] = func(ctx context.Context, _ string) (string, error) {
					archives++
					time.Sleep(40 * time.Second)
					if mode == "bad archive" {
						return "", errClusterConflict
					}
					return plan, ctx.Err()
				}
				gates[1] = func(ctx context.Context, _ string) (string, error) {
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) > clusterSSHPreparationFreshness {
						t.Fatal("native observation escaped freshness window")
					}
					if mode == "native expiry" {
						<-ctx.Done()
					}
					return plan, ctx.Err()
				}
				err := withClusterSSHPreparationApprovalProof(parent, plan, gates, func(ctx context.Context) error { return ctx.Err() },
					func(ctx context.Context, proof clusterSSHPreparationApprovalProof) error {
						if mode == "ignored recheck" {
							return nil
						}
						if err := proof.recheck(ctx); err != nil {
							return err
						}
						if mode == "commit expiry" {
							time.Sleep(clusterSSHPreparationFreshness + time.Second)
							if proof.valid(plan) {
								t.Fatal("expired approval still valid")
							}
						}
						return nil
					})
				if (err == nil) != (mode == "slow archive") || archives != 1 {
					t.Fatalf("archives=%d error=%v", archives, err)
				}
			})
		})
	}
}

func TestClusterSSHPreparationGatesReceiptRejectsSubstitution(t *testing.T) {
	cohort, source, _, _ := sshPreparationFixture(t)
	report := newClusterSSHQualificationReport(cohort.Attempt)
	a := sshPreparationAuthorizationFixture(t, clusterSSHParentProof{Version: 1, Cohort: cohort, Source: source}, clusterSSHQueueSource{}, &report)
	plan, _ := clusterSSHPreparationPlanDigest(a)
	var receipts [6]string
	for i := range receipts {
		receipts[i] = strings.Repeat("a", 64)
	}
	raw := marshalClusterJSON(clusterSSHPreparationReadiness(plan, receipts))
	if parseClusterSSHPreparationReadiness([]byte(raw), a) != nil {
		t.Fatal("valid receipt")
	}
	for _, invalid := range []string{`{}`, strings.Replace(raw, `"version":1`, `"version":1,"version":1`, 1), strings.Replace(raw, `"code":"clean_targets"`, `"code":"target_ready"`, 1), strings.Replace(raw, plan, strings.Repeat("f", 64), 1)} {
		if parseClusterSSHPreparationReadiness([]byte(invalid), a) == nil {
			t.Fatal("changed receipt accepted")
		}
	}
}
