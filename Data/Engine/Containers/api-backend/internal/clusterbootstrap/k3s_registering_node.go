package clusterbootstrap

import (
	"encoding/json"
	"strings"
)

// ParseK3sRegisteringNode observes a confirmed target while kubelet publishes
// its first status. It returns ONLY a Node UID, never readiness or membership
// completion. Present identity fields must match; missing status may converge.
// Startup still needs independent durable phase authority. Final admission uses
// ParseK3sJoinedNode and matching native/controller proofs, not this projection.
func ParseK3sRegisteringNode(raw []byte, expected K3sJoinedNodeIdentity) (string, error) {
	fail := func() (string, error) { return "", ErrPreparationConfig }
	object, err := sourceJSONObject(raw, 128<<10)
	if err != nil {
		return fail()
	}
	metadata, err := sourceJSONObject(object["metadata"], 128<<10)
	if err != nil {
		return fail()
	}
	// Build the complete expected projection and let the strict final parser
	// validate expected identity, metadata, labels and every present status field.
	// Synthetic missing fields are only for validation; never return this Node.
	info := map[string]any{"machineID": expected.MachineID, "bootID": expected.BootID,
		"kubeletVersion": expected.K3sVersion, "architecture": "amd64", "operatingSystem": "linux"}
	status := map[string]any{"nodeInfo": info,
		"addresses":  []any{map[string]any{"type": "InternalIP", "address": expected.Address}},
		"conditions": []any{map[string]any{"type": "Ready", "status": "Unknown"}}}
	if value, ok := object["status"]; ok {
		observed, err := sourceJSONObject(value, 128<<10)
		if err != nil {
			return fail()
		}
		for key := range observed {
			// Standard JSON's case-insensitive matching must not hide a field.
			for _, field := range []string{"nodeInfo", "addresses", "conditions"} {
				if strings.EqualFold(field, key) && field != key {
					return fail()
				}
			}
			if key != "nodeInfo" && key != "addresses" && key != "conditions" {
				continue
			}
			if key == "nodeInfo" {
				fields, err := sourceJSONObject(observed[key], 128<<10)
				if err != nil {
					return fail()
				}
				for field, value := range fields {
					var text string
					if json.Unmarshal(value, &text) != nil {
						return fail()
					}
					if want, constrained := info[field]; constrained {
						if text != "" && text != want {
							return fail()
						}
					} else {
						info[field] = text
					}
				}
			} else {
				var entries []json.RawMessage
				if json.Unmarshal(observed[key], &entries) != nil {
					return fail()
				}
				wanted := "InternalIP"
				fallback := map[string]string{"type": wanted, "address": expected.Address}
				if key == "conditions" {
					wanted = "Ready"
					fallback = map[string]string{"type": wanted, "status": "Unknown"}
				}
				present := false
				for _, entry := range entries {
					fields, err := sourceJSONObject(entry, 128<<10)
					var kind string
					if err != nil || json.Unmarshal(fields["type"], &kind) != nil {
						return fail()
					}
					present = present || kind == wanted
				}
				if !present {
					raw, _ := json.Marshal(fallback)
					entries = append(entries, raw)
				}
				status[key] = entries
			}
		}
	}
	projected, err := json.Marshal(map[string]any{"apiVersion": object["apiVersion"], "kind": object["kind"], "metadata": metadata, "status": status})
	if err != nil {
		return fail()
	}
	node, err := ParseK3sJoinedNodeIdentity(projected, expected)
	if err != nil {
		return fail()
	}
	return node.NodeUID, nil
}
