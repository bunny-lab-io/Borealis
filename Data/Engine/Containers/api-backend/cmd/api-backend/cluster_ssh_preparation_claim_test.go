package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func sshPreparationAuthorizationFixture(t *testing.T, proof clusterSSHParentProof, source clusterSSHQueueSource, report *clusterSSHQualificationReport) clusterSSHPreparationAuthorization {
	t.Helper()
	digest := func(value any) string {
		got, err := clusterSSHPreparationDigest(value)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	a := clusterSSHPreparationAuthorization{Version: 1, OperationID: proof.Cohort.OperationID, Attempt: proof.Cohort.Attempt,
		ControllerHolder: proof.Cohort.ControllerHolder, InspectionSHA256: digest(proof), QualificationSHA256: digest(report),
		SourceSHA256: digest(source), ConfirmationSHA256: strings.Repeat("a", 64)}
	for i, target := range proof.Cohort.Targets {
		a.Targets = append(a.Targets, clusterSSHPreparationAuthorizedTarget{TargetID: target.Binding.TargetID,
			QualificationGeneration: target.Generation + int64(i) + 1, CredentialSHA256: strings.Repeat("b", 64)})
	}
	return a
}

func TestClusterSSHPreparationAuthorizationBindsExactReservation(t *testing.T) {
	cohort, source, _, _ := sshPreparationFixture(t)
	proof := clusterSSHParentProof{Version: 1, Cohort: cohort, Source: source}
	queue := clusterSSHQueueSource{ClusterID: source.ClusterID, ActiveOperationID: cohort.OperationID}
	report := newClusterSSHQualificationReport(cohort.Attempt)
	for i := 0; i < len(report.Checks)-1; i++ {
		report.Checks[i].State = "passed"
	}
	authorization := sshPreparationAuthorizationFixture(t, proof, queue, &report)
	payload := map[string]any{"ssh_preparation_authorization": authorization, "ssh_qualification": report}
	raw := marshalClusterJSON(payload)
	if _, _, err := parseClusterSSHPreparationAuthorization(raw, proof, queue); err != nil {
		t.Fatal("valid reservation rejected", err)
	}
	for _, mode := range []string{"missing", "unknown", "duplicate", "alias", "attempt", "controller", "confirmation", "inspection", "qualification", "source", "generation", "overflow", "order", "completed", "recovery", "blocked"} {
		t.Run(mode, func(t *testing.T) {
			var changed map[string]json.RawMessage
			if json.Unmarshal([]byte(raw), &changed) != nil {
				t.Fatal("decode fixture")
			}
			a := authorization
			a.Targets = append([]clusterSSHPreparationAuthorizedTarget(nil), authorization.Targets...)
			switch mode {
			case "attempt":
				a.Attempt++
			case "controller":
				a.ControllerHolder += "changed"
			case "confirmation":
				a.ConfirmationSHA256 = strings.Repeat("0", 64)
			case "inspection":
				a.InspectionSHA256 = strings.Repeat("b", 64)
			case "qualification":
				a.QualificationSHA256 = strings.Repeat("b", 64)
			case "source":
				a.SourceSHA256 = strings.Repeat("b", 64)
			case "generation":
				a.Targets[0].QualificationGeneration = cohort.Targets[0].Generation
			case "overflow":
				a.Targets[0].QualificationGeneration = 1<<63 - 1
			case "order":
				a.Targets[0], a.Targets[1] = a.Targets[1], a.Targets[0]
			case "completed":
				changed["ssh_preparation"] = json.RawMessage(`{}`)
			case "recovery":
				changed["ssh_identity_recovery"] = json.RawMessage(`{}`)
			case "blocked":
				blocked := newClusterSSHQualificationReport(cohort.Attempt)
				a.QualificationSHA256, _ = clusterSSHPreparationDigest(blocked)
				changed["ssh_qualification"], _ = json.Marshal(blocked)
			}
			value, _ := json.Marshal(a)
			switch mode {
			case "unknown":
				value = []byte(strings.Replace(string(value), `"version":1`, `"version":1,"extra":true`, 1))
			case "duplicate":
				value = []byte(strings.Replace(string(value), `"version":1`, `"version":1,"version":1`, 1))
			case "alias":
				value = []byte(strings.Replace(string(value), `"version":1`, `"Version":1`, 1))
			}
			changed["ssh_preparation_authorization"] = value
			if mode == "missing" {
				delete(changed, "ssh_preparation_authorization")
			}
			if _, _, err := parseClusterSSHPreparationAuthorization(marshalClusterJSON(changed), proof, queue); err == nil {
				t.Fatal("invalid reservation accepted")
			}
		})
	}
}
