package main

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func sshPreparationRecoveryRecordFixture(t *testing.T) (clusterSSHPreparationWork, clusterSSHPreparationRecoveryRecord, string) {
	t.Helper()
	cohort, source, _, _ := sshPreparationFixture(t)
	proof := clusterSSHParentProof{Version: 1, Cohort: cohort, Source: source}
	queue := clusterSSHQueueSource{ClusterID: source.ClusterID, ActiveOperationID: cohort.OperationID}
	report := newClusterSSHQualificationReport(cohort.Attempt)
	for i := 0; i < len(report.Checks)-1; i++ {
		report.Checks[i].State = "passed"
	}
	a := sshPreparationAuthorizationFixture(t, proof, queue, &report)
	confirmation := sshPreparationConfirmationFixture(t, &a, source)
	plan, _ := clusterSSHPreparationPlanDigest(a)
	var receipts [6]string
	for i := range receipts {
		receipts[i] = strings.Repeat("a", 64)
	}
	original := marshalClusterJSON(map[string]any{"ssh_preparation_authorization": a, "ssh_qualification": report,
		"ssh_preparation_confirmation": confirmation, "ssh_preparation_readiness": clusterSSHPreparationReadiness(plan, receipts),
		"retained_exact_number": json.Number("18446744073709551615")})
	base, _, err := clusterSSHPreparationRecoveryPayload(original)
	if err != nil {
		t.Fatal(err)
	}
	work := clusterSSHPreparationWork{Authorization: a, Proof: proof, Source: queue, Payload: original}
	record := clusterSSHPreparationRecoveryRecord{Version: 1, ID: newClusterUUID(), OperationID: a.OperationID, Attempt: a.Attempt,
		ControllerHolder: a.ControllerHolder, PlanSHA256: plan, PayloadSHA256: clusterSSHPreparationRecoveryPayloadDigest(base)}
	holder := newClusterUUID()
	for _, target := range a.Targets {
		record.Targets = append(record.Targets, clusterSSHPreparationRecoveryTarget{TargetID: target.TargetID, PreviousHolder: holder,
			PreviousGeneration: target.QualificationGeneration + 1, Generation: target.QualificationGeneration + 2, PreviousExpiresAt: 123})
	}
	return work, record, original
}

func sshRecoveryPayload(t *testing.T, original string, record clusterSSHPreparationRecoveryRecord) string {
	t.Helper()
	var value map[string]json.RawMessage
	if json.Unmarshal([]byte(original), &value) != nil {
		t.Fatal("invalid fixture")
	}
	value[clusterSSHPreparationRecoveryField], _ = json.Marshal(record)
	return marshalClusterJSON(value)
}

func TestClusterSSHPreparationRecoveryExactLineage(t *testing.T) {
	work, record, original := sshPreparationRecoveryRecordFixture(t)
	raw := sshRecoveryPayload(t, original, record)
	got, err := parseClusterSSHPreparationRecovery(raw, work)
	if err != nil || !reflect.DeepEqual(got, record) {
		t.Fatal("exact lineage rejected", err)
	}
	base, _, err := clusterSSHPreparationRecoveryPayload(raw)
	originalBase, _, originalErr := clusterSSHPreparationRecoveryPayload(original)
	if err != nil || originalErr != nil || string(base) != string(originalBase) || !strings.Contains(string(base), "18446744073709551615") {
		t.Fatal("original authorization or exact number changed")
	}
	if _, _, err := parseClusterSSHPreparationAuthorization(original, work.Proof, work.Source); err != nil {
		t.Fatal("initial authorization fixture invalid", err)
	}
	if _, _, err := parseClusterSSHPreparationAuthorization(raw, work.Proof, work.Source); err == nil {
		t.Fatal("recovery promoted to initial preparation authority")
	}
	for _, mode := range []string{"version", "id", "operation", "attempt", "controller", "plan", "payload", "target", "order", "holder", "split holder", "generation", "next generation", "expiry", "missing sibling", "extra sibling"} {
		t.Run(mode, func(t *testing.T) {
			changed := record
			changed.Targets = slices.Clone(record.Targets)
			switch mode {
			case "version":
				changed.Version++
			case "id":
				changed.ID = "00000000-0000-0000-0000-000000000000"
			case "operation":
				changed.OperationID = newClusterUUID()
			case "attempt":
				changed.Attempt++
			case "controller":
				changed.ControllerHolder += "other"
			case "plan":
				changed.PlanSHA256 = strings.Repeat("b", 64)
			case "payload":
				changed.PayloadSHA256 = strings.Repeat("b", 64)
			case "target":
				changed.Targets[0].TargetID = newClusterUUID()
			case "order":
				slices.Reverse(changed.Targets)
			case "holder":
				changed.Targets[0].PreviousHolder = "invalid"
			case "split holder":
				changed.Targets[1].PreviousHolder = newClusterUUID()
			case "generation":
				changed.Targets[0].PreviousGeneration++
			case "next generation":
				changed.Targets[0].Generation++
			case "expiry":
				changed.Targets[0].PreviousExpiresAt = 0
			case "missing sibling":
				changed.Targets = changed.Targets[:1]
			case "extra sibling":
				changed.Targets = append(changed.Targets, changed.Targets[0])
			}
			if _, err := parseClusterSSHPreparationRecovery(sshRecoveryPayload(t, original, changed), work); err == nil {
				t.Fatal("changed lineage accepted")
			}
		})
	}
	work.Authorization.Targets = slices.Clone(work.Authorization.Targets)
	work.Authorization.Targets[0].QualificationGeneration = 1<<53 - 2
	record.Targets[0].PreviousGeneration, record.Targets[0].Generation = 1<<53-1, 1<<53
	record.PlanSHA256, _ = clusterSSHPreparationPlanDigest(work.Authorization)
	if record.valid(work, base) {
		t.Fatal("generation outside native exact-integer bound accepted")
	}
}

func TestClusterSSHPreparationRecoveryStrictPayload(t *testing.T) {
	work, record, original := sshPreparationRecoveryRecordFixture(t)
	raw := sshRecoveryPayload(t, original, record)
	recordRaw := marshalClusterJSON(record)
	for _, mode := range []string{"unknown", "duplicate", "alias", "nested duplicate", "null", "fraction", "outer duplicate", "outer alias", "original duplicate", "original changed", "trailing", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			changed := raw
			switch mode {
			case "unknown":
				changed = strings.Replace(raw, recordRaw, strings.Replace(recordRaw, `"version":1`, `"version":1,"extra":true`, 1), 1)
			case "duplicate":
				changed = strings.Replace(raw, recordRaw, strings.Replace(recordRaw, `"version":1`, `"version":1,"version":1`, 1), 1)
			case "alias":
				changed = strings.Replace(raw, recordRaw, strings.Replace(recordRaw, `"version":1`, `"Version":1`, 1), 1)
			case "nested duplicate":
				changed = strings.Replace(raw, `"previous_expires_at":123`, `"previous_expires_at":123,"previous_expires_at":123`, 1)
			case "null":
				changed = strings.Replace(raw, recordRaw, "null", 1)
			case "fraction":
				changed = strings.Replace(raw, recordRaw, strings.Replace(recordRaw, `"version":1`, `"version":1.0`, 1), 1)
			case "outer duplicate":
				changed = strings.TrimSuffix(raw, "}") + `,"ssh_preparation_recovery":{}}`
			case "outer alias":
				changed = strings.Replace(raw, `"ssh_preparation_recovery":`, `"SSH_PREPARATION_RECOVERY":`, 1)
			case "original duplicate":
				changed = strings.TrimSuffix(raw, "}") + `,"retained_exact_number":18446744073709551615}`
			case "original changed":
				changed = strings.Replace(raw, "18446744073709551615", "18446744073709551614", 1)
			case "trailing":
				changed += " {}"
			case "oversize":
				changed += strings.Repeat(" ", 256<<10)
			}
			if _, err := parseClusterSSHPreparationRecovery(changed, work); err == nil {
				t.Fatal("invalid private payload accepted")
			}
		})
	}
}
