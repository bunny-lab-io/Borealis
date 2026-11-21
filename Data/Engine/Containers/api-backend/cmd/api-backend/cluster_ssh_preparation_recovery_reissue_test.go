package main

import (
	"context"
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func reissueProofPayload(t *testing.T, original string, lineage clusterSSHPreparationRecoveryLineage) string {
	t.Helper()
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(original), &object) != nil {
		t.Fatal("fixture payload")
	}
	wire, err := lineage.canonical()
	if err != nil {
		t.Fatal(err)
	}
	object[clusterSSHPreparationRecoveryField] = wire
	return marshalClusterJSON(object)
}

func appendReissueProofFixture(t *testing.T, lineage clusterSSHPreparationRecoveryLineage, payload string) clusterSSHPreparationRecoveryLineage {
	t.Helper()
	id, holder, targets := lineage.tip()
	if holder == "" {
		holder = newClusterUUID()
	}
	wire, err := lineage.canonical()
	if err != nil {
		t.Fatal(err)
	}
	base, _, err := clusterSSHPreparationRecoveryPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(base, &object) != nil {
		t.Fatal("base")
	}
	object[clusterSSHPreparationRecoveryField] = wire
	priorPayload, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	entry := clusterSSHPreparationRecoveryProofRecord{ID: newClusterUUID(), PreviousID: id, Holder: newClusterUUID(),
		PreviousRecordSHA256: clusterSSHPreparationRecoveryPayloadDigest(wire), PayloadSHA256: clusterSSHPreparationRecoveryPayloadDigest(priorPayload)}
	for _, target := range targets {
		entry.Targets = append(entry.Targets, clusterSSHPreparationRecoveryTarget{TargetID: target.TargetID, PreviousHolder: holder,
			PreviousGeneration: target.Generation, PreviousExpiresAt: 456, Generation: target.Generation + 1})
	}
	lineage.Requests = append(slices.Clone(lineage.Requests), entry)
	return lineage
}

func TestClusterSSHPreparationRecoveryReissueLineage(t *testing.T) {
	work, original, payload := sshPreparationRecoveryRecordFixture(t)
	payload = sshRecoveryPayload(t, payload, original)
	genesis, err := parseClusterSSHPreparationRecoveryLineage(payload, work)
	if err != nil || !reflect.DeepEqual(genesis.Original, original) || len(genesis.Requests) != 0 {
		t.Fatal("genesis", err)
	}
	lineage := genesis
	for range 3 {
		// Raw formatting differs from the reconstructible canonical history.
		var formatted strings.Builder
		formatted.WriteString("  ")
		formatted.WriteString(payload)
		formatted.WriteString("\n")
		payload = formatted.String()
		before := payload
		lineage = appendReissueProofFixture(t, lineage, payload)
		payload = reissueProofPayload(t, payload, lineage)
		got, err := parseClusterSSHPreparationRecoveryLineage(payload, work)
		if err != nil || !reflect.DeepEqual(got, lineage) || !reflect.DeepEqual(got.Original, original) {
			t.Fatal("new proof lineage", err)
		}
		base, _, err := clusterSSHPreparationRecoveryPayload(payload)
		oldBase, _, oldErr := clusterSSHPreparationRecoveryPayload(before)
		if err != nil || oldErr != nil || string(base) != string(oldBase) || !strings.Contains(string(base), "18446744073709551615") {
			t.Fatal("original payload changed")
		}
		if _, err := parseClusterSSHPreparationRecovery(payload, work); err == nil {
			t.Fatal("new proof became initial Q+2 record")
		}
	}
}

func TestClusterSSHPreparationRecoveryReissueRejectsChangedHistory(t *testing.T) {
	work, original, payload := sshPreparationRecoveryRecordFixture(t)
	payload = sshRecoveryPayload(t, payload, original)
	lineage := appendReissueProofFixture(t, clusterSSHPreparationRecoveryLineage{Version: 2, Original: original}, payload)
	payload = reissueProofPayload(t, payload, lineage)
	lineage = appendReissueProofFixture(t, lineage, payload)
	payload = reissueProofPayload(t, payload, lineage)
	for _, mode := range []string{"version", "original holder", "original source", "no requests", "request reuse", "genesis request reuse", "branch", "prior hash", "payload hash", "foreign payload hash", "first payload hash", "zero holder", "root holder", "previous holder", "old holder reuse", "split holder", "target", "order", "missing sibling", "expiry", "generation gap", "overflow", "unknown", "alias", "duplicate", "nested duplicate", "trailing", "changed original bytes", "outer alias"} {
		t.Run(mode, func(t *testing.T) {
			var changed clusterSSHPreparationRecoveryLineage
			wire, _ := json.Marshal(lineage)
			_ = json.Unmarshal(wire, &changed)
			last := &changed.Requests[1]
			switch mode {
			case "version":
				changed.Version++
			case "original holder":
				changed.Original.Targets[0].PreviousHolder = newClusterUUID()
			case "original source":
				changed.Original.PlanSHA256 = strings.Repeat("c", 64)
			case "no requests":
				changed.Requests = nil
			case "request reuse":
				last.ID = changed.Requests[0].ID
			case "genesis request reuse":
				last.ID = original.ID
			case "branch":
				last.PreviousID = original.ID
			case "prior hash":
				last.PreviousRecordSHA256 = strings.Repeat("b", 64)
			case "payload hash":
				last.PayloadSHA256 = strings.Repeat("0", 64)
			case "foreign payload hash":
				last.PayloadSHA256 = strings.Repeat("b", 64)
			case "first payload hash":
				changed.Requests = changed.Requests[:1]
				changed.Requests[0].PayloadSHA256 = strings.Repeat("b", 64)
			case "zero holder":
				last.Holder = "00000000-0000-0000-0000-000000000000"
			case "root holder":
				last.Holder = original.Targets[0].PreviousHolder
			case "previous holder":
				last.Holder = last.Targets[0].PreviousHolder
			case "old holder reuse":
				last.Holder = changed.Requests[0].Targets[0].PreviousHolder
			case "split holder":
				last.Targets[1].PreviousHolder = newClusterUUID()
			case "target":
				last.Targets[0].TargetID = newClusterUUID()
			case "order":
				slices.Reverse(last.Targets)
			case "missing sibling":
				last.Targets = last.Targets[:1]
			case "expiry":
				last.Targets[0].PreviousExpiresAt = 0
			case "generation gap":
				last.Targets[0].Generation++
			case "overflow":
				last.Targets[0].PreviousGeneration, last.Targets[0].Generation = 1<<53-1, 1<<53
			}
			// Encode the actual v2 shape even for empty requests: canonical()
			// intentionally represents a zero-entry in-memory genesis as v1.
			var object map[string]json.RawMessage
			_ = json.Unmarshal([]byte(payload), &object)
			object[clusterSSHPreparationRecoveryField], _ = json.Marshal(changed)
			raw := marshalClusterJSON(object)
			switch mode {
			case "unknown":
				raw = strings.Replace(raw, `"version":2`, `"version":2,"extra":true`, 1)
			case "alias":
				raw = strings.Replace(raw, `"requests":`, `"Requests":`, 1)
			case "duplicate":
				raw = strings.Replace(raw, `"version":2`, `"version":2,"version":2`, 1)
			case "nested duplicate":
				raw = strings.Replace(raw, `"previous_expires_at":456`, `"previous_expires_at":456,"previous_expires_at":456`, 1)
			case "trailing":
				raw += " {}"
			case "changed original bytes":
				raw = strings.Replace(raw, "18446744073709551615", "18446744073709551614", 1)
			case "outer alias":
				raw = strings.Replace(raw, `"ssh_preparation_recovery":`, `"SSH_PREPARATION_RECOVERY":`, 1)
			}
			if _, err := parseClusterSSHPreparationRecoveryLineage(raw, work); err == nil {
				t.Fatal("changed history accepted")
			}
		})
	}
	// Extending history stops at the existing byte bound; no record is pruned.
	for {
		lineage = appendReissueProofFixture(t, lineage, payload)
		payload = reissueProofPayload(t, payload, lineage)
		wire, _ := json.Marshal(lineage)
		_, err := parseClusterSSHPreparationRecoveryLineage(payload, work)
		if len(wire) > 8<<10 {
			if err == nil {
				t.Fatal("oversized history accepted")
			}
			break
		}
		if err != nil {
			t.Fatal("history below existing bound rejected", err)
		}
	}
}

func reissuedProofClaimFixture(t *testing.T) clusterSSHPreparationRecoveryReissuedProofClaim {
	t.Helper()
	work, original, payload := sshPreparationRecoveryRecordFixture(t)
	payload = sshRecoveryPayload(t, payload, original)
	lineage := appendReissueProofFixture(t, clusterSSHPreparationRecoveryLineage{Version: 2, Original: original}, payload)
	payload = reissueProofPayload(t, payload, lineage)
	claim := clusterSSHPreparationRecoveryReissuedProofClaim{Lineage: lineage, Baseline: sshPreparationConfigurationFixture(t, false).Baseline, payload: payload,
		custody: &clusterSSHPreparationRecoveryCustody{assigned: map[string]int64{}}}
	_, holder, targets := lineage.tip()
	for i, target := range targets {
		claim.custody.assigned[target.TargetID] = 1000
		claim.Claims = append(claim.Claims, clusterSSHPreparationRecoveryTargetClaim{Previous: target,
			Sealed: sealedClusterSSHCredentials{binding: work.Proof.Cohort.Targets[i].Binding, generation: "synthetic", ciphertext: aegisEnvelopePrefix + "synthetic"},
			Lease: clusterSSHTargetLease{TargetID: target.TargetID, OperationID: original.OperationID, OperationAttempt: original.Attempt, ControllerHolder: original.ControllerHolder,
				OperationKind: "ssh_onboarding", OperationStep: clusterSSHPreparationRecoveryOperationStep, Step: clusterSSHPreparationRecoveryClaimStep, Holder: holder, Generation: target.Generation}})
	}
	return claim
}

func TestClusterSSHPreparationRecoveryReissueHandle(t *testing.T) {
	claim := reissuedProofClaimFixture(t)
	frozen, expires, err := freezeClusterSSHPreparationRecoveryReissuedProof(claim)
	if err != nil {
		t.Fatal(err)
	}
	frozen.Lineage.Requests[0].Targets[0].PreviousHolder = "changed"
	expires[claim.Claims[0].Lease.TargetID] = 0
	if claim.Lineage.Requests[0].Targets[0].PreviousHolder == "changed" || claim.custody.assigned[claim.Claims[0].Lease.TargetID] == 0 {
		t.Fatal("frozen copy aliased issuer")
	}
	initial := clusterSSHPreparationRecoveryClaim{Reservation: claim.Lineage.Original, Claims: claim.Claims, Baseline: claim.Baseline, payload: claim.payload, custody: claim.custody}
	if _, _, err := freezeClusterSSHPreparationRecoveryClaim(initial); err == nil {
		t.Fatal("reissued claim became Q+2 handle")
	}
	copy := claim
	var controller *clusterController
	if controller.withClusterSSHPreparationRecoveryReissuedProof(context.Background(), nil, claim, nil) == nil || !claim.custody.started.Load() || !claim.custody.failed.Load() {
		t.Fatal("failed entry did not consume handle")
	}
	if controller.withClusterSSHPreparationRecoveryReissuedProof(context.Background(), nil, copy, nil) == nil {
		t.Fatal("copied handle replayed")
	}
	if _, _, err := freezeClusterSSHPreparationRecoveryReissuedProof(clusterSSHPreparationRecoveryReissuedProofClaim{Lineage: claim.Lineage, Claims: claim.Claims, Baseline: claim.Baseline, payload: claim.payload}); err == nil {
		t.Fatal("lost local handle reconstructed")
	}
}

func TestClusterSSHPreparationRecoveryReissueTypedLeaseCustody(t *testing.T) {
	claim := reissuedProofClaimFixture(t)
	expiries := maps.Clone(claim.custody.assigned)
	claim.custody.started.Store(true)
	value := clusterSSHPreparationRecoveryReissuedProofAuthority{Lineage: claim.Lineage, Baseline: claim.Baseline, ExpiresAt: maps.Clone(expiries)}
	reads := 0
	check, snapshot := newClusterSSHPreparationRecoveryTypedLeaseCheck(claim.custody, expiries, func(context.Context) error { return nil },
		func(_ context.Context, expected map[string]int64, renew bool) (clusterSSHPreparationRecoveryReissuedProofAuthority, map[string]int64, error) {
			reads++
			if !maps.Equal(expected, expiries) {
				t.Fatal("expected SQL expiry lost")
			}
			if renew {
				for id := range expiries {
					expiries[id]++
				}
			}
			value.ExpiresAt = maps.Clone(expiries)
			return value, maps.Clone(expiries), nil
		})
	if check(context.Background()) != nil {
		t.Fatal("typed renewal rejected")
	}
	got, err := snapshot()
	if err != nil || !maps.Equal(got.ExpiresAt, expiries) {
		t.Fatal("typed snapshot", err)
	}
	got.Lineage.Requests[0].Targets[0].PreviousHolder = "consumer mutation"
	if check(context.Background()) != nil {
		t.Fatal("detached snapshot altered owner")
	}
	value.Lineage.Requests[0].PayloadSHA256 = strings.Repeat("b", 64)
	if check(context.Background()) == nil || !claim.custody.failed.Load() {
		t.Fatal("changed request lineage accepted")
	}
	before := reads
	if check(context.Background()) == nil || reads != before {
		t.Fatal("failed lineage resumed")
	}
	if _, err := snapshot(); err == nil {
		t.Fatal("failed lineage exported proof")
	}
}
