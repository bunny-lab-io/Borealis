package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestClusterSSHPreparationTargetCPUPostgresApproval(t *testing.T) {
	for _, mode := range []string{"fresh", "missing projection", "changed CPU", "changed clean digest", "changed target", "missing capacity", "changed capacity", "changed demand receipt"} {
		t.Run(mode, func(t *testing.T) {
			f, snapshot, confirmation, proof := newSSHPreparationApprovalFixture(t, false)
			switch mode {
			case "missing projection":
				proof.targetCPU = nil
			case "changed CPU":
				proof.targetCPU[0].CPU.MemoryKiB++
			case "changed clean digest":
				proof.receipts[1] = strings.Repeat("f", 64)
			case "changed target":
				proof.targetCPU[0].TargetID = newClusterUUID()
			case "missing capacity":
				proof.capacity = nil
			case "changed capacity":
				proof.capacity.Targets[0].Capacity.LonghornCPU.OverrideMilli++
			case "changed demand receipt":
				proof.capacity.CensusSHA256 = strings.Repeat("f", 64)
			}
			err := f.c.commitClusterSSHPreparationApproval(f.ctx, snapshot, confirmation, proof)
			if (err == nil) != (mode == "fresh") {
				t.Fatal("unexpected atomic CPU approval", err)
			}
			var payload, state string
			if f.c.store.db.QueryRowContext(f.ctx, `SELECT payload_json,state FROM engine.cluster_operations WHERE id=$1`, f.op.ID).Scan(&payload, &state) != nil {
				t.Fatal("read approval")
			}
			if f.c.store.db.Stats().InUse != 0 {
				t.Fatal("approval projection retained connection")
			}
			if mode != "fresh" {
				if state != "waiting" || strings.Contains(payload, "ssh_preparation_readiness") || strings.Contains(payload, "ssh_preparation_authorization") {
					t.Fatal("partial CPU approval committed")
				}
				return
			}
			var raw json.RawMessage
			if clusterBootstrapObject([]byte(payload), map[string]any{"ssh_preparation_readiness": &raw}) != nil {
				t.Fatal("readiness missing")
			}
			var readiness clusterSSHPreparationReadinessReceipt
			if json.Unmarshal(raw, &readiness) != nil || readiness.Version != 3 || len(readiness.Targets) != 2 {
				t.Fatal("typed CPU not committed atomically")
			}
			f.op.State, f.op.CurrentStep = "running", clusterSSHPreparationOperationStep
			work, err := f.c.claimClusterSSHPreparation(f.ctx, f.op, newClusterUUID())
			if err != nil || len(work.Claims) != 2 {
				t.Fatal("CPU-bound approval did not feed normal claim", err)
			}
			for i, claim := range work.Claims {
				authority, err := newClusterSSHPreparationAuthorityRead(f.c.store, f.aegis, claim.Lease, work.Baseline, claim.Sealed)(f.ctx)
				if err != nil {
					t.Fatal("claim authority", err)
				}
				expected, err := buildClusterSSHPreparationExpected(authority.Cohort, authority.Source, authority.Lease, authority.Baseline, authority.K3sVersion, "10.42.0.0/16", "10.43.0.0/16", authority.payload)
				if err != nil || expected.TargetCPU == nil || *expected.TargetCPU != readiness.Targets[i].CPU {
					t.Fatal("first-mutation CPU binding lost", err)
				}
			}
		})
	}
}
