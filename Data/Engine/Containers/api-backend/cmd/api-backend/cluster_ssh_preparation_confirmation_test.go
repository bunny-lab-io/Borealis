package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func sshPreparationConfirmationFixture(t *testing.T, a *clusterSSHPreparationAuthorization) clusterSSHPreparationConfirmation {
	t.Helper()
	plan, err := clusterSSHPreparationPlanDigest(*a)
	if err != nil {
		t.Fatal(err)
	}
	raw := marshalClusterJSON(map[string]any{"request_id": newClusterUUID(), "plan_sha256": plan, "confirmation": "PREPARE ENGINE NODES"})
	c, err := newClusterSSHPreparationConfirmation([]byte(raw), "admin", time.Now().Unix(), *a)
	if err != nil {
		t.Fatal(err)
	}
	a.ConfirmationSHA256, err = clusterSSHPreparationDigest(c)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestClusterSSHPreparationConfirmationExactPlan(t *testing.T) {
	cohort, source, _, _ := sshPreparationFixture(t)
	proof := clusterSSHParentProof{Version: 1, Cohort: cohort, Source: source}
	report := newClusterSSHQualificationReport(cohort.Attempt)
	a := sshPreparationAuthorizationFixture(t, proof, clusterSSHQueueSource{}, &report)
	c := sshPreparationConfirmationFixture(t, &a)
	raw, _ := json.Marshal(c)
	if _, err := parseClusterSSHPreparationConfirmation(raw, a); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"operation", "attempt", "controller", "source", "qualification", "target generation", "credential", "target order", "actor", "timestamp", "unknown", "duplicate", "alias"} {
		t.Run(mode, func(t *testing.T) {
			changed := a
			changed.Targets = append([]clusterSSHPreparationAuthorizedTarget(nil), a.Targets...)
			altered := string(raw)
			switch mode {
			case "operation":
				changed.OperationID = newClusterUUID()
			case "attempt":
				changed.Attempt++
			case "controller":
				changed.ControllerHolder += "changed"
			case "source":
				changed.SourceSHA256 = strings.Repeat("f", 64)
			case "qualification":
				changed.QualificationSHA256 = strings.Repeat("f", 64)
			case "target generation":
				changed.Targets[0].QualificationGeneration++
			case "credential":
				changed.Targets[0].CredentialSHA256 = strings.Repeat("f", 64)
			case "target order":
				changed.Targets[0], changed.Targets[1] = changed.Targets[1], changed.Targets[0]
			case "actor":
				altered = strings.Replace(altered, `"actor":"admin"`, `"actor":"other"`, 1)
			case "timestamp":
				altered = strings.Replace(altered, `"confirmed_at":`, `"confirmed_at":0,"old":`, 1)
			case "unknown":
				altered = strings.Replace(altered, `"version":1`, `"version":1,"extra":true`, 1)
			case "duplicate":
				altered = strings.Replace(altered, `"version":1`, `"version":1,"version":1`, 1)
			case "alias":
				altered = strings.Replace(altered, `"version":1`, `"Version":1`, 1)
			}
			if _, err := parseClusterSSHPreparationConfirmation([]byte(altered), changed); err == nil {
				t.Fatal("confirmation accepted changed plan or receipt")
			}
		})
	}
	for _, invalid := range []string{
		`{}`, `null`, `{"request_id":"bad","plan_sha256":"bad","confirmation":"PREPARE ENGINE NODES"}`,
		marshalClusterJSON(map[string]any{"request_id": c.RequestID, "plan_sha256": c.PlanSHA256, "confirmation": "INSPECT ENGINE NODES"}),
		marshalClusterJSON(map[string]any{"request_id": c.RequestID, "plan_sha256": c.PlanSHA256, "confirmation": "PREPARE ENGINE NODES", "actor": "admin"}),
	} {
		if _, err := newClusterSSHPreparationConfirmation([]byte(invalid), "admin", c.ConfirmedAt, a); err == nil {
			t.Fatal("invalid explicit request accepted")
		}
	}
}
