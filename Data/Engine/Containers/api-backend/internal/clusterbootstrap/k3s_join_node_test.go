package clusterbootstrap

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func joinedNodeFixture(t *testing.T) (PreparationExpected, []byte) {
	t.Helper()
	expected, _ := preparationFixture()
	raw, err := json.Marshal(map[string]any{
		"apiVersion": "v1", "kind": "Node",
		"metadata": map[string]any{"name": expected.Target.Hostname, "uid": "99999999-9999-4999-8999-999999999999", "labels": map[string]string{
			"borealis.io/engine-node": "true", "borealis.io/application-state": "drained", "borealis.io/control-plane-eligible": "false", "borealis.io/edge-eligible": "false", "borealis.io/scheduler-eligible": "false", "borealis.io/postgres-primary-eligible": "false"}},
		"status": map[string]any{"nodeInfo": map[string]any{"machineID": expected.Target.MachineID, "bootID": expected.TargetBootID, "kubeletVersion": expected.K3sVersion, "architecture": "amd64", "operatingSystem": "linux"},
			"addresses":  []any{map[string]any{"type": "Hostname", "address": expected.Target.Hostname}, map[string]any{"type": "InternalIP", "address": expected.Target.Address}},
			"conditions": []any{map[string]any{"type": "Ready", "status": "True"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return expected, raw
}

func TestK3sJoinedNodeObservation(t *testing.T) {
	expected, raw := joinedNodeFixture(t)
	observed, err := ParseK3sJoinedNode(raw, expected)
	if err != nil || !observed.Ready || observed.Hostname != expected.Target.Hostname || observed.BootID != expected.TargetBootID || observed.Address != expected.Target.Address {
		t.Fatal("bound Node rejected", err)
	}
	for _, status := range []string{"False", "Unknown"} {
		pending := bytes.Replace(raw, []byte(`"status":"True"`), []byte(`"status":"`+status+`"`), 1)
		got, err := ParseK3sJoinedNode(pending, expected)
		if err != nil || got.Ready || got.NodeUID != observed.NodeUID {
			t.Fatal("joining observation mistaken for readiness", err)
		}
	}
	for _, mode := range []string{"wrong UID", "wrong name", "wrong machine", "wrong boot", "wrong version", "wrong IP", "duplicate IP", "missing readiness", "duplicate readiness", "unknown readiness", "active", "eligible", "missing eligibility", "deleted", "wrong kind", "wrong architecture", "wrong OS", "duplicate JSON", "alias JSON", "alias address", "alias condition", "extra JSON", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			var node map[string]any
			if json.Unmarshal(raw, &node) != nil {
				t.Fatal("fixture")
			}
			metadata := node["metadata"].(map[string]any)
			status := node["status"].(map[string]any)
			info := status["nodeInfo"].(map[string]any)
			labels := metadata["labels"].(map[string]any)
			switch mode {
			case "wrong UID":
				metadata["uid"] = strings.Repeat("0", 36)
			case "wrong name":
				metadata["name"] = "foreign"
			case "wrong machine":
				info["machineID"] = strings.Repeat("e", 32)
			case "wrong boot":
				info["bootID"] = "88888888-8888-4888-8888-888888888888"
			case "wrong version":
				info["kubeletVersion"] = "v1.36.4+k3s1"
			case "wrong IP":
				status["addresses"].([]any)[1].(map[string]any)["address"] = "192.168.50.99"
			case "duplicate IP":
				status["addresses"] = append(status["addresses"].([]any), status["addresses"].([]any)[1])
			case "missing readiness":
				status["conditions"] = []any{}
			case "duplicate readiness":
				status["conditions"] = append(status["conditions"].([]any), status["conditions"].([]any)[0])
			case "unknown readiness":
				status["conditions"].([]any)[0].(map[string]any)["status"] = "true"
			case "active":
				labels["borealis.io/application-state"] = "active"
			case "eligible":
				labels["borealis.io/scheduler-eligible"] = "true"
			case "missing eligibility":
				delete(labels, "borealis.io/edge-eligible")
			case "deleted":
				metadata["deletionTimestamp"] = "2026-10-27T00:00:00Z"
			case "wrong kind":
				node["kind"] = "Pod"
			case "wrong architecture":
				info["architecture"] = "arm64"
			case "wrong OS":
				info["operatingSystem"] = "windows"
			}
			changed, _ := json.Marshal(node)
			switch mode {
			case "duplicate JSON":
				changed = bytes.Replace(changed, []byte(`"kind":"Node"`), []byte(`"kind":"Node","kind":"Node"`), 1)
			case "alias JSON":
				changed = bytes.Replace(changed, []byte(`"machineID"`), []byte(`"MACHINEID"`), 1)
			case "alias address":
				changed = bytes.Replace(changed, []byte(`"address"`), []byte(`"ADDRESS"`), 1)
			case "alias condition":
				changed = bytes.Replace(changed, []byte(`"type":"Ready"`), []byte(`"TYPE":"Ready"`), 1)
			case "extra JSON":
				changed = append(changed, []byte(`{}`)...)
			case "oversized":
				changed = append(changed, bytes.Repeat([]byte(" "), 128<<10)...)
			}
			if got, err := ParseK3sJoinedNode(changed, expected); err == nil || got != (K3sJoinedNode{}) {
				t.Fatal("unsafe joining Node accepted")
			}
		})
	}
}
