package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func sshPreparationProofPayload(t *testing.T, proof clusterSSHParentProof, baseline clusterbootstrap.Expected) []byte {
	t.Helper()
	raw, err := json.Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	payload, err := json.Marshal(map[string]any{"inspection_only": true, "baseline_release": baseline.Release, "baseline_sha": baseline.SourceSHA, "source_k3s_version": "v1.36.3+k3s1", "target_count": len(proof.Cohort.Targets),
		"ssh_inspection": map[string]any{"sha256": hex.EncodeToString(hash[:]), "proof": proof, "qualification_required": true}})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestClusterSSHPreparationAuthorityProofKeepsOriginalGeneration(t *testing.T) {
	cohort, source, lease, baseline := sshPreparationFixture(t)
	proof := clusterSSHParentProof{Version: 1, Cohort: cohort, Source: source}
	raw := sshPreparationProofPayload(t, proof, baseline)
	for _, mode := range []string{"typed order", "map order", "exact large generation"} {
		t.Run(mode, func(t *testing.T) {
			input := raw
			if mode == "map order" {
				var object any
				_ = json.Unmarshal(raw, &object)
				input, _ = json.Marshal(object)
			}
			if mode == "exact large generation" {
				copy := proof
				copy.Cohort.Targets = append([]clusterSSHInspectedTarget(nil), proof.Cohort.Targets...)
				copy.Cohort.Targets[0].Generation = 9007199254740993
				input = sshPreparationProofPayload(t, copy, baseline)
			}
			parsed, version, err := parseClusterSSHPreparationInspection(input, lease, baseline)
			if err != nil || version != "v1.36.3+k3s1" || parsed.Cohort.Targets[0].Generation == lease.Generation {
				t.Fatalf("inspection evidence changed or rejected: %v", err)
			}
			if mode == "exact large generation" && parsed.Cohort.Targets[0].Generation != 9007199254740993 {
				t.Fatal("generation rounded")
			}
		})
	}
}

func TestClusterSSHPreparationAuthorityProofRejectsAmbiguityAndRebinding(t *testing.T) {
	cohort, source, lease, baseline := sshPreparationFixture(t)
	raw := sshPreparationProofPayload(t, clusterSSHParentProof{Version: 1, Cohort: cohort, Source: source}, baseline)
	for _, mode := range []string{"wrong holder", "wrong attempt", "wrong operation", "wrong baseline", "hash", "missing wrapper", "not historical proof", "target count", "unknown proof", "case alias", "duplicate proof key", "trailing", "oversize", "invalid UTF8"} {
		t.Run(mode, func(t *testing.T) {
			input := append([]byte{}, raw...)
			currentLease, currentBaseline := lease, baseline
			switch mode {
			case "wrong holder":
				currentLease.ControllerHolder = "other-" + lease.ControllerHolder
			case "wrong attempt":
				currentLease.OperationAttempt++
			case "wrong operation":
				currentLease.OperationID = newClusterUUID()
			case "wrong baseline":
				currentBaseline.SourceSHA = strings.Repeat("b", 40)
			case "hash":
				input = bytes.Replace(input, []byte(`"sha256":"`), []byte(`"sha256":"f`), 1)
			case "missing wrapper":
				input = bytes.Replace(input, []byte(`"ssh_inspection":`), []byte(`"other":`), 1)
			case "not historical proof":
				input = bytes.Replace(input, []byte(`"qualification_required":true`), []byte(`"qualification_required":false`), 1)
			case "target count":
				input = bytes.Replace(input, []byte(`"target_count":2`), []byte(`"target_count":1`), 1)
			case "unknown proof":
				input = bytes.Replace(input, []byte(`"version":1`), []byte(`"version":1,"unexpected":true`), 1)
			case "case alias":
				input = bytes.Replace(input, []byte(`"ObservedAt":`), []byte(`"observedat":`), 1)
			case "duplicate proof key":
				input = bytes.Replace(input, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1)
			case "trailing":
				input = append(input, []byte("{}")...)
			case "oversize":
				input = append([]byte(strings.Repeat(" ", 256<<10)), input...)
			case "invalid UTF8":
				input = append([]byte{0xff}, input...)
			}
			if _, _, err := parseClusterSSHPreparationInspection(input, currentLease, currentBaseline); err != clusterbootstrap.ErrPreparationConfig {
				t.Fatal("unsafe persisted proof accepted")
			}
		})
	}
}
