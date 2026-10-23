package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"encoding/json"
	"strings"
	"testing"
)

func TestClusterSSHIdentityRecoveryPayloadBindsExactNodes(t *testing.T) {
	cohort, source, _, _ := sshPreparationFixture(t)
	proof := clusterSSHParentProof{Version: 1, Cohort: cohort, Source: source}
	target := cohort.Targets[0]
	recovery := clusterbootstrap.IdentityRecoveryRequest{SourceID: source.Members[0].NodeID, SourceNodeUID: source.Members[0].NodeUID, TargetNodeUID: newClusterUUID()}
	raw, _ := json.Marshal(map[string]any{"ssh_identity_recovery": recovery})
	got, binding, err := parseClusterSSHIdentityRecovery(string(raw), proof, target)
	if err != nil || got != recovery || binding.ClusterID != source.ClusterID || binding.SourceID != recovery.SourceID || binding.SourceUID != recovery.SourceNodeUID {
		t.Fatalf("recovery payload lost identity binding: %#v %#v %v", got, binding, err)
	}

	for _, mode := range []string{"missing", "unknown", "duplicate", "wrong source", "wrong source UID", "same target UID", "bad target UID"} {
		t.Run(mode, func(t *testing.T) {
			value := recovery
			var payload string
			switch mode {
			case "missing":
				payload = `{}`
			case "unknown":
				payload = `{"ssh_identity_recovery":{"source_node_id":"` + value.SourceID + `","source_node_uid":"` + value.SourceNodeUID + `","target_node_uid":"` + value.TargetNodeUID + `","extra":true}}`
			case "duplicate":
				payload = `{"ssh_identity_recovery":{"source_node_id":"` + value.SourceID + `","source_node_id":"` + value.SourceID + `","source_node_uid":"` + value.SourceNodeUID + `","target_node_uid":"` + value.TargetNodeUID + `"}}`
			default:
				switch mode {
				case "wrong source":
					value.SourceID = newClusterUUID()
				case "wrong source UID":
					value.SourceNodeUID = newClusterUUID()
				case "same target UID":
					value.TargetNodeUID = value.SourceNodeUID
				case "bad target UID":
					value.TargetNodeUID = strings.Repeat("f", 36)
				}
				raw, _ := json.Marshal(map[string]any{"ssh_identity_recovery": value})
				payload = string(raw)
			}
			if _, _, err := parseClusterSSHIdentityRecovery(payload, proof, target); err != errClusterConflict {
				t.Fatalf("unsafe identity payload accepted: %v", err)
			}
		})
	}
}
