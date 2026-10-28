package main

import (
	"context"
	"strings"
	"testing"
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
	for _, mode := range []string{"success", "missing", "failed", "changed", "invalid", "source changed", "cancelled", "ignored failure"} {
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
				if mode == "failed" || (mode == "ignored failure" && reads > 1) {
					return "", errClusterConflict
				}
				if mode == "changed" && reads > 1 {
					return strings.Repeat("f", 64), nil
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
				if mode == "source changed" && sourceReads > 2 {
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
			if mode == "success" && (!consumed || reads != 2 || sourceReads != 4) {
				t.Fatal("incomplete proof rounds")
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
