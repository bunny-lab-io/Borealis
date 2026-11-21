package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"time"

	"borealis/api-backend/internal/clusterbootstrap"
)

// Project only known collection clocks. Preserve the presence of each field,
// its writer's ownership, condition transitions and all non-clock data.
func (u *clusterSSHStorageUpdate) projectClock(object map[string]any, field, key string) error {
	raw, ok := object[field].(string)
	t, err := time.Parse(time.RFC3339Nano, raw)
	if !ok || err != nil || t.IsZero() {
		return clusterbootstrap.ErrPreparationConfig
	}
	if u.clocks == nil {
		u.clocks = map[string]time.Time{}
	}
	if _, duplicate := u.clocks[key]; duplicate {
		return clusterbootstrap.ErrPreparationConfig
	}
	u.clocks[key] = t.UTC()
	object[field] = "<collection time>"
	return nil
}

// Longhorn emits this success message by iterating the package set. Unknown
// messages, package membership, the condition status and reason stay exact.
func clusterSSHRequiredPackageOrder(status map[string]any, update *clusterSSHStorageUpdate) error {
	conditions, ok := status["conditions"].([]any)
	if !ok {
		return nil
	}
	const prefix = "All required packages ["
	const suffix = "] are installed"
	for i, item := range conditions {
		c, _ := item.(map[string]any)
		if c["type"] != "RequiredPackages" || c["status"] != "True" {
			continue
		}
		message, _ := c["message"].(string)
		if !strings.HasPrefix(message, prefix) || !strings.HasSuffix(message, suffix) {
			continue
		}
		packages := strings.Split(strings.TrimSuffix(strings.TrimPrefix(message, prefix), suffix), " ")
		for _, pkg := range packages {
			if !clusterSSHStorageName(pkg) {
				return nil
			}
		}
		slices.Sort(packages)
		if update.packageMessage != "" || len(slices.Compact(packages)) != len(packages) {
			return clusterbootstrap.ErrPreparationConfig
		}
		update.packageMessage = message
		c = maps.Clone(c)
		c["message"] = prefix + strings.Join(packages, " ") + suffix
		conditions = slices.Clone(conditions)
		conditions[i] = c
		status["conditions"] = conditions
	}
	return nil
}

func clusterSSHKubeNodeReceipt(name string, object map[string]any, exact [32]byte) ([32]byte, *clusterSSHStorageUsage, error) {
	id, ok := clusterSSHStorageMetadata(object, "v1", "Node", "")
	if !ok || id.Name != name {
		return exact, nil, clusterbootstrap.ErrPreparationConfig
	}
	sample := clusterSSHStorageUsage{revision: id.Revision, writers: map[string]*clusterSSHStorageUpdate{}}
	status := maps.Clone(clusterSSHStorageMap(object, "status"))
	meta := maps.Clone(clusterSSHStorageMap(object, "metadata"))
	supervisor := "k3s-supervisor@" + name
	conditions, _ := status["conditions"].([]any)
	if conditions != nil {
		conditions = slices.Clone(conditions)
		for i, item := range conditions {
			c, _ := item.(map[string]any)
			kind, _ := c["type"].(string)
			writer := "k3s/status"
			switch kind {
			case "Ready", "MemoryPressure", "DiskPressure", "PIDPressure":
			case "EtcdIsVoter":
				writer = supervisor + "/status"
			default:
				continue
			}
			if c["lastHeartbeatTime"] == nil || c["lastHeartbeatTime"] == "" {
				continue
			}
			c = maps.Clone(c)
			if err := sample.writer(writer).projectClock(c, "lastHeartbeatTime", kind); err != nil {
				return exact, nil, err
			}
			conditions[i] = c
		}
		status["conditions"] = conditions
	}
	const stamp = "etcd.k3s.cattle.io/local-snapshots-timestamp"
	annotations := clusterSSHStorageMap(meta, "annotations")
	if annotations[stamp] != nil && annotations[stamp] != "" {
		annotations = maps.Clone(annotations)
		if err := sample.writer(supervisor+"/").projectClock(annotations, stamp, stamp); err != nil {
			return exact, nil, err
		}
		meta["annotations"] = annotations
	}
	if len(sample.writers) == 0 {
		return exact, nil, nil
	}
	return clusterSSHProjectStorageMetadata(object, status, meta, sample, exact)
}

func clusterSSHProjectStorageMetadata(object, status, metadata map[string]any, sample clusterSSHStorageUsage, exact [32]byte) ([32]byte, *clusterSSHStorageUsage, error) {
	meta := maps.Clone(metadata)
	delete(meta, "resourceVersion")
	// Kubernetes may reorder field-manager records when it timestamps an update.
	// Preserve the complete multiset, including unmanaged entries and fieldsV1.
	if fields, ok := meta["managedFields"].([]any); ok {
		records := make([]json.RawMessage, 0, len(fields))
		for _, item := range fields {
			entry, _ := item.(map[string]any)
			manager, _ := entry["manager"].(string)
			subresource, _ := entry["subresource"].(string)
			u := sample.writers[manager+"/"+subresource]
			if u != nil && entry["operation"] == "Update" && entry["apiVersion"] == object["apiVersion"] && entry["fieldsType"] == "FieldsV1" {
				if stamp, exists := entry["time"]; exists {
					text, _ := stamp.(string)
					t, err := time.Parse(time.RFC3339Nano, text)
					if err != nil || t.IsZero() || !u.writeTime.IsZero() {
						return exact, nil, clusterbootstrap.ErrPreparationConfig
					}
					u.writeTime = t.UTC()
					entry = maps.Clone(entry)
					entry["time"] = "<observed write>"
					item = entry
				}
			}
			raw, err := json.Marshal(item)
			if err != nil {
				return exact, nil, err
			}
			records = append(records, raw)
		}
		slices.SortFunc(records, func(a, b json.RawMessage) int { return bytes.Compare(a, b) })
		meta["managedFields"] = records
	}
	projected := maps.Clone(object)
	projected["metadata"] = meta
	if status != nil {
		projected["status"] = status
	}
	raw, err := json.Marshal(projected)
	if err != nil {
		return exact, nil, err
	}
	return sha256.Sum256(raw), &sample, nil
}
