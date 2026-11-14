package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestClusterSSHPreparationNativeGatesPostgresBlockedProposal(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		for _, mode := range []string{"missing executor", "bound executor", "payload drift", "credential drift", "cancelled"} {
			t.Run(map[bool]string{false: "expansion", true: "replacement"}[replacement]+"/"+mode, func(t *testing.T) {
				f, snapshot, _, _ := newSSHPreparationApprovalFixture(t, replacement)
				if mode == "payload drift" {
					f.exec(t, `UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=$1`, f.op.ID)
				}
				if mode == "credential drift" {
					f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, f.targets[0].Binding.TargetID)
				}
				before := sshPreparationNativeGateState(t, f)
				ctx, cancel := context.WithCancel(f.ctx)
				defer cancel()
				if mode == "cancelled" {
					cancel()
				}
				var clean func(context.Context, string) (string, error)
				if mode != "missing executor" {
					clean = func(context.Context, string) (string, error) {
						t.Fatal("known incomplete gates reached SSH")
						return "", errClusterConflict
					}
				}
				consumed := false
				err := f.c.withPreparationNativeGates(ctx, snapshot, t.TempDir(), clean, nil, nil,
					func(context.Context, clusterSSHPreparationGates) (clusterSSHPreparationReadinessReceipt, error) {
						consumed = true
						return clusterSSHPreparationReadinessReceipt{}, nil
					})
				if err == nil || consumed || !errors.Is(err, errClusterConflict) || f.c.store.db.Stats().InUse != 0 {
					t.Fatal("incomplete native proof reached approval or retained connection", err)
				}
				var blocked clusterSSHPreparationGateBlocked
				if mode == "missing executor" || mode == "bound executor" {
					if !errors.As(err, &blocked) {
						t.Fatal("current waiting proposal lost readiness diagnosis", err)
					}
					for _, item := range clusterSSHPreparationNativeBlockers(clean != nil, false) {
						if !strings.Contains(err.Error(), item.Error()) {
							t.Fatal("missing exact gate diagnosis", item.Gate)
						}
					}
				} else if errors.As(err, &blocked) {
					t.Fatal("stale proposal reported as current native readiness", err)
				}
				if after := sshPreparationNativeGateState(t, f); before != after {
					t.Fatal("blocked readiness changed durable rows")
				}
			})
		}
	}
}

func sshPreparationNativeGateState(t *testing.T, f sshPreparationAuthorityFixture) string {
	t.Helper()
	var state string
	err := f.c.store.db.QueryRowContext(f.ctx, `SELECT json_build_array(to_jsonb(o),to_jsonb(c),
 (SELECT json_agg(to_jsonb(t) ORDER BY t.id) FROM engine.cluster_onboarding_targets t WHERE t.operation_id=o.id),
 (SELECT json_agg(to_jsonb(p) ORDER BY p.target_id) FROM engine.cluster_onboarding_credentials p JOIN engine.cluster_onboarding_targets t ON t.id=p.target_id WHERE t.operation_id=o.id),
 (SELECT json_agg(to_jsonb(n) ORDER BY n.id) FROM engine.cluster_nodes n),
 (SELECT json_agg(to_jsonb(l) ORDER BY l.name) FROM engine.cluster_application_leases l WHERE l.name=$2),
 (SELECT json_agg(to_jsonb(e) ORDER BY e.id) FROM engine.cluster_operation_events e WHERE e.operation_id=o.id))::text
 FROM engine.cluster_operations o CROSS JOIN engine.cluster_state c WHERE o.id=$1 AND c.id=1`, f.op.ID, clusterControllerLeaseName).Scan(&state)
	if err != nil {
		t.Fatal("read complete readiness state", err)
	}
	return state
}
