package clusterbootstrap

import (
	"encoding/json"
	"testing"
)

func TestK3sRegisteringNode(t *testing.T) {
	for _, mode := range []string{"complete", "missing status", "empty status", "empty info", "empty identity", "pending", "wrong boot", "wrong address", "active", "missing label", "deleted", "alias info", "alias machine", "duplicate ready", "null status"} {
		t.Run(mode, func(t *testing.T) {
			expected, raw := joinedNodeFixture(t)
			identity := K3sJoinedNodeIdentity{expected.Target.Hostname, expected.Target.MachineID, expected.TargetBootID, expected.Target.Address, expected.K3sVersion}
			var node map[string]any
			if json.Unmarshal(raw, &node) != nil {
				t.Fatal("fixture")
			}
			meta := node["metadata"].(map[string]any)
			status := node["status"].(map[string]any)
			info := status["nodeInfo"].(map[string]any)
			switch mode {
			case "missing status":
				delete(node, "status")
			case "empty status":
				node["status"] = map[string]any{}
			case "empty info":
				status["nodeInfo"] = map[string]any{}
			case "empty identity":
				info["bootID"] = ""
			case "pending":
				status["conditions"].([]any)[0].(map[string]any)["status"] = "False"
			case "wrong boot":
				info["bootID"] = "88888888-8888-4888-8888-888888888888"
			case "wrong address":
				status["addresses"].([]any)[1].(map[string]any)["address"] = "192.168.50.99"
			case "active":
				meta["labels"].(map[string]any)["borealis.io/application-state"] = "active"
			case "missing label":
				delete(meta["labels"].(map[string]any), "borealis.io/edge-eligible")
			case "deleted":
				meta["deletionTimestamp"] = "2026-10-27T00:00:00Z"
			case "alias info":
				delete(status, "nodeInfo")
				status["NODEINFO"] = info
			case "alias machine":
				delete(info, "machineID")
				info["MACHINEID"] = expected.Target.MachineID
			case "duplicate ready":
				status["conditions"] = append(status["conditions"].([]any), status["conditions"].([]any)[0])
			case "null status":
				node["status"] = nil
			}
			raw, _ = json.Marshal(node)
			uid, err := ParseK3sRegisteringNode(raw, identity)
			valid := mode == "complete" || mode == "missing status" || mode == "empty status" || mode == "empty info" || mode == "empty identity" || mode == "pending"
			if (err == nil) != valid || valid && uid != meta["uid"] || !valid && uid != "" {
				t.Fatal("unsafe registration projection", err)
			}
			if valid && mode != "complete" && mode != "pending" {
				if _, err := ParseK3sJoinedNode(raw, expected); err == nil {
					t.Fatal("registration mistaken for final evidence")
				}
			}
		})
	}
}
