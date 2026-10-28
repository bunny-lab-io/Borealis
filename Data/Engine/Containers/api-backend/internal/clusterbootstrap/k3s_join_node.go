package clusterbootstrap

import (
	"encoding/json"
	"net/netip"
	"strings"
)

// Public identity observation, not membership, image-import or traffic approval.
// Native target and original controller must independently observe the same UID.
// Ready may be false while a durably authorized join is in progress; completion
// must additionally require Ready plus runtime/etcd/import/identity proofs.
type K3sJoinedNode struct {
	NodeUID    string `json:"node_uid"`
	Hostname   string `json:"hostname"`
	MachineID  string `json:"machine_id"`
	BootID     string `json:"boot_id"`
	Address    string `json:"address"`
	K3sVersion string `json:"k3s_version"`
	Ready      bool   `json:"ready"`
}

// Identity-only expectations come from confirmed inspection, not a target lease.
type K3sJoinedNodeIdentity struct{ Hostname, MachineID, BootID, Address, K3sVersion string }

func ParseK3sJoinedNode(raw []byte, expected PreparationExpected) (K3sJoinedNode, error) {
	if expected.Validate() != nil {
		return K3sJoinedNode{}, ErrPreparationConfig
	}
	return ParseK3sJoinedNodeIdentity(raw, K3sJoinedNodeIdentity{expected.Target.Hostname, expected.Target.MachineID, expected.TargetBootID, expected.Target.Address, expected.K3sVersion})
}

// Identity-only variant lets original controller derive expectations directly
// from confirmed inspection; it must not invent another target's lease claim.
func ParseK3sJoinedNodeIdentity(raw []byte, expected K3sJoinedNodeIdentity) (K3sJoinedNode, error) {
	fail := func() (K3sJoinedNode, error) { return K3sJoinedNode{}, ErrPreparationConfig }
	address, addressErr := netip.ParseAddr(expected.Address)
	if addressErr != nil || !address.Is4() || !address.IsPrivate() || address.String() != expected.Address || !sessionHostname.MatchString(expected.Hostname) || !sessionMachineID.MatchString(expected.MachineID) || expected.MachineID == strings.Repeat("0", 32) || !nonzeroPreparationUUID(expected.BootID) || expected.K3sVersion != K3sPins().Version {
		return fail()
	}
	object, err := sourceJSONObject(raw, 128<<10)
	if err != nil {
		return fail()
	}
	require := func(raw []byte, keys ...string) (map[string]json.RawMessage, bool) {
		fields, err := sourceJSONObject(raw, 128<<10)
		if err != nil {
			return nil, false
		}
		for _, key := range keys {
			if _, ok := fields[key]; !ok {
				return nil, false
			}
		}
		return fields, true
	}
	metadata, metadataOK := require(object["metadata"], "name", "uid", "labels")
	status, statusOK := require(object["status"], "nodeInfo", "addresses", "conditions")
	if !metadataOK || !statusOK {
		return fail()
	}
	if _, ok := require(metadata["labels"]); !ok {
		return fail()
	}
	if _, ok := require(status["nodeInfo"], "machineID", "bootID", "kubeletVersion", "architecture", "operatingSystem"); !ok {
		return fail()
	}
	// Exact spelling also applies to array elements: encoding/json alone would
	// accept TYPE/STATUS aliases and silently project them into trusted fields.
	for field, keys := range map[string][]string{"addresses": {"type", "address"}, "conditions": {"type", "status"}} {
		var items []json.RawMessage
		if json.Unmarshal(status[field], &items) != nil || len(items) == 0 {
			return fail()
		}
		for _, item := range items {
			if _, ok := require(item, keys...); !ok {
				return fail()
			}
		}
	}
	var node struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Metadata   struct {
			Name              string            `json:"name"`
			UID               string            `json:"uid"`
			DeletionTimestamp json.RawMessage   `json:"deletionTimestamp"`
			Labels            map[string]string `json:"labels"`
		} `json:"metadata"`
		Status struct {
			NodeInfo struct {
				MachineID       string `json:"machineID"`
				BootID          string `json:"bootID"`
				KubeletVersion  string `json:"kubeletVersion"`
				Architecture    string `json:"architecture"`
				OperatingSystem string `json:"operatingSystem"`
			} `json:"nodeInfo"`
			Addresses []struct {
				Type    string `json:"type"`
				Address string `json:"address"`
			} `json:"addresses"`
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
		} `json:"status"`
	}
	// Exact top-level field spelling, in addition to duplicate/alias exclusion.
	for _, key := range []string{"apiVersion", "kind", "metadata", "status"} {
		if _, ok := object[key]; !ok {
			return fail()
		}
	}
	if json.Unmarshal(raw, &node) != nil || node.APIVersion != "v1" || node.Kind != "Node" || !nonzeroPreparationUUID(node.Metadata.UID) || node.Metadata.Name != expected.Hostname ||
		len(node.Metadata.DeletionTimestamp) != 0 && string(node.Metadata.DeletionTimestamp) != "null" || node.Status.NodeInfo.MachineID != expected.MachineID || node.Status.NodeInfo.BootID != expected.BootID ||
		node.Status.NodeInfo.KubeletVersion != expected.K3sVersion || node.Status.NodeInfo.Architecture != "amd64" || node.Status.NodeInfo.OperatingSystem != "linux" {
		return fail()
	}
	for key, want := range map[string]string{"borealis.io/engine-node": "true", "borealis.io/application-state": "drained", "borealis.io/control-plane-eligible": "false", "borealis.io/edge-eligible": "false", "borealis.io/scheduler-eligible": "false", "borealis.io/postgres-primary-eligible": "false"} {
		if node.Metadata.Labels[key] != want {
			return fail()
		}
	}
	internal, ready, addresses, readiness := "", "", 0, 0
	for _, item := range node.Status.Addresses {
		if item.Type == "InternalIP" {
			addresses++
			internal = item.Address
		}
	}
	for _, item := range node.Status.Conditions {
		if item.Type == "Ready" {
			readiness++
			ready = item.Status
		}
	}
	if addresses != 1 || internal != expected.Address || readiness != 1 || ready != "True" && ready != "False" && ready != "Unknown" {
		return fail()
	}
	return K3sJoinedNode{node.Metadata.UID, node.Metadata.Name, node.Status.NodeInfo.MachineID, node.Status.NodeInfo.BootID, internal, node.Status.NodeInfo.KubeletVersion, ready == "True"}, nil
}
