package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func sshPreparationConfirmationFixture(t *testing.T, a *clusterSSHPreparationAuthorization, source clusterSSHSourceCohort) clusterSSHPreparationConfirmation {
	t.Helper()
	plan, err := clusterSSHPreparationPlanDigest(*a)
	if err != nil {
		t.Fatal(err)
	}
	raw := marshalClusterJSON(map[string]any{"request_id": newClusterUUID(), "plan_sha256": plan, "confirmation": "PREPARE ENGINE NODES", "source_id": source.Members[len(source.Members)-1].NodeID})
	c, err := newClusterSSHPreparationConfirmation([]byte(raw), "admin", time.Now().Unix(), *a, source)
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
	c := sshPreparationConfirmationFixture(t, &a, source)
	raw, _ := json.Marshal(c)
	if _, err := parseClusterSSHPreparationConfirmation(raw, a, source); err != nil {
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
				altered = strings.Replace(altered, `"version":2`, `"version":2,"extra":true`, 1)
			case "duplicate":
				altered = strings.Replace(altered, `"version":2`, `"version":2,"version":2`, 1)
			case "alias":
				altered = strings.Replace(altered, `"version":2`, `"Version":2`, 1)
			}
			if _, err := parseClusterSSHPreparationConfirmation([]byte(altered), changed, source); err == nil {
				t.Fatal("confirmation accepted changed plan or receipt")
			}
		})
	}
	for _, invalid := range []string{
		`{}`, `null`, `{"request_id":"bad","plan_sha256":"bad","confirmation":"PREPARE ENGINE NODES"}`,
		marshalClusterJSON(map[string]any{"request_id": c.RequestID, "plan_sha256": c.PlanSHA256, "confirmation": "INSPECT ENGINE NODES"}),
		marshalClusterJSON(map[string]any{"request_id": c.RequestID, "plan_sha256": c.PlanSHA256, "confirmation": "PREPARE ENGINE NODES", "actor": "admin"}),
	} {
		if _, err := newClusterSSHPreparationConfirmation([]byte(invalid), "admin", c.ConfirmedAt, a, source); err == nil {
			t.Fatal("invalid explicit request accepted")
		}
	}
}

func TestClusterSSHPreparationConfirmationExplicitSource(t *testing.T) {
	authority, _, _ := sshPrejoinSourceFixture(t, true)
	proof := clusterSSHParentProof{Version: 1, Cohort: authority.Cohort, Source: authority.Source}
	report := newClusterSSHQualificationReport(proof.Cohort.Attempt)
	a := sshPreparationAuthorizationFixture(t, proof, clusterSSHQueueSource{}, &report)
	plan, _ := clusterSSHPreparationPlanDigest(a)
	for _, mode := range []string{"first survivor", "second survivor", "unknown", "target", "source UID replaced", "legacy", "missing selection", "receipt source changed", "receipt digest changed"} {
		t.Run(mode, func(t *testing.T) {
			id := proof.Source.Members[1].NodeID
			if mode == "first survivor" {
				id = proof.Source.Members[0].NodeID
			}
			if mode == "unknown" {
				id = newClusterUUID()
			}
			if mode == "target" {
				id = a.Targets[0].TargetID
			}
			body := []byte(marshalClusterJSON(map[string]any{"request_id": newClusterUUID(), "plan_sha256": plan, "source_id": id, "confirmation": "PREPARE ENGINE NODES"}))
			c, err := newClusterSSHPreparationConfirmation(body, "admin", time.Now().Unix(), a, proof.Source)
			if mode == "unknown" || mode == "target" {
				if err == nil {
					t.Fatal("non-source accepted")
				}
				return
			}
			if err != nil || c.IdentitySource.SourceID != id {
				t.Fatal("explicit selection lost", err)
			}
			bound := a
			bound.ConfirmationSHA256, _ = clusterSSHPreparationDigest(c)
			confirmed := bound.ConfirmationSHA256
			changed := proof
			changed.Source.Members = append([]clusterSSHSourceMember(nil), proof.Source.Members...)
			switch mode {
			case "source UID replaced":
				changed.Source.Members[1].NodeUID = newClusterUUID()
			case "legacy":
				c.Version = 1
			case "missing selection":
				c.IdentitySource.SourceID = ""
			case "receipt source changed":
				c.IdentitySource.SourceID = proof.Source.Members[0].NodeID
				c.IdentitySource.SourceUID = proof.Source.Members[0].NodeUID
			case "receipt digest changed":
				bound.ConfirmationSHA256 = strings.Repeat("b", 64)
			}
			payload := marshalClusterJSON(map[string]any{"ssh_preparation_authorization": bound, "ssh_preparation_confirmation": c})
			selected, err := clusterSSHPreparationRecordedSource(payload, changed, confirmed)
			if mode == "first survivor" || mode == "second survivor" {
				if err != nil || selected != c.IdentitySource {
					t.Fatal("recorded selection lost", err)
				}
			} else if err == nil {
				t.Fatal("changed recorded selection accepted")
			}
		})
	}
}
