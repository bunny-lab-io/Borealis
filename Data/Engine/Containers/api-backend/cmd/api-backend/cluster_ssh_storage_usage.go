package main

import (
	"crypto/sha256"
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"borealis/api-backend/internal/clusterbootstrap"
)

// Private evidence for explaining a resourceVersion change between adjacent
// reads. Only the stable receipt crosses the controller/worker boundary.
type clusterSSHStorageUsage struct {
	revision    string
	statusWrite string
	counters    map[string]int64
}

func (s clusterSSHStorageObservation) sameInputs(prior clusterSSHStorageObservation) bool {
	if !maps.Equal(s.receipts, prior.receipts) || !reflect.DeepEqual(s.Requirements, prior.Requirements) || len(s.usage) != len(prior.usage) {
		return false
	}
	for path, now := range s.usage {
		last, ok := prior.usage[path]
		if !ok || ((now.revision != last.revision || now.statusWrite != last.statusWrite) && maps.Equal(now.counters, last.counters)) {
			return false
		}
	}
	return true
}

// Keep all status, spec, ownership and metadata fields except the recognized
// live counters and their resourceVersion. Leave the original object intact
// for the normal validators, including the check for a valid resourceVersion.
func clusterSSHLonghornUsageReceipt(path string, object map[string]any, exact [32]byte) ([32]byte, *clusterSSHStorageUsage, error) {
	var kind, name string
	switch {
	case strings.HasPrefix(path, clusterSSHStorageVolumePrefix):
		kind, name = "Volume", strings.TrimPrefix(path, clusterSSHStorageVolumePrefix)
	case strings.HasPrefix(path, clusterSSHInstanceNodePrefix):
		kind, name = "Node", strings.TrimPrefix(path, clusterSSHInstanceNodePrefix)
	default:
		return exact, nil, nil
	}
	id, ok := clusterSSHStorageMetadata(object, "longhorn.io/v1beta2", kind, "longhorn-system")
	if !ok || id.Name != name {
		return exact, nil, clusterbootstrap.ErrPreparationConfig
	}
	status := maps.Clone(clusterSSHStorageMap(object, "status"))
	sample := clusterSSHStorageUsage{revision: id.Revision, counters: map[string]int64{}}
	if kind == "Volume" {
		raw, exists := status["actualSize"]
		if !exists {
			return exact, nil, nil
		}
		n, ok := clusterSSHStorageInteger(raw)
		if !ok {
			return exact, nil, clusterbootstrap.ErrPreparationConfig
		}
		sample.counters["actualSize"] = n
		status["actualSize"] = 0
	} else {
		disks := maps.Clone(clusterSSHStorageMap(status, "diskStatus"))
		for key, raw := range disks {
			disk, _ := raw.(map[string]any)
			if _, exists := disk["storageAvailable"]; !exists {
				continue
			}
			available, ok := clusterSSHStorageInteger(disk["storageAvailable"])
			maximum, valid := clusterSSHStorageInteger(disk["storageMaximum"])
			if !ok || !valid || available > maximum {
				return exact, nil, clusterbootstrap.ErrPreparationConfig
			}
			sample.counters[key] = available
			disk = maps.Clone(disk)
			disk["storageAvailable"] = 0
			if conditions, ok := disk["conditions"].([]any); ok {
				conditions = slices.Clone(conditions)
				for i, row := range conditions {
					c, _ := row.(map[string]any)
					if c["type"] != "Schedulable" || c["status"] != "False" || c["reason"] != "DiskPressure" {
						continue
					}
					msg, _ := c["message"].(string)
					// The pinned Longhorn reason embeds this disk's current byte
					// count. Keep the minimum, percentage and every other word.
					token := "CurrentAvailable = " + strconv.FormatInt(available, 10) + " (StorageAvailable - Required)"
					if strings.Count(msg, token) == 1 {
						c = maps.Clone(c)
						c["message"] = strings.Replace(msg, token, "CurrentAvailable = <measured> (StorageAvailable - Required)", 1)
						conditions[i] = c
					}
				}
				disk["conditions"] = conditions
			}
			disks[key] = disk
		}
		if len(sample.counters) == 0 {
			return exact, nil, nil
		}
		status["diskStatus"] = disks
	}
	projected := maps.Clone(object)
	meta := maps.Clone(clusterSSHStorageMap(object, "metadata"))
	delete(meta, "resourceVersion")
	// Kubernetes timestamps the manager's status update alongside its new revision.
	// Bind the manager and its field ownership, allowing only that write time to move.
	fields, _ := meta["managedFields"].([]any)
	for i, item := range fields {
		entry, _ := item.(map[string]any)
		if entry["manager"] != "longhorn-manager" || entry["operation"] != "Update" || entry["subresource"] != "status" || entry["apiVersion"] != "longhorn.io/v1beta2" || entry["fieldsType"] != "FieldsV1" {
			continue
		}
		stamp, exists := entry["time"]
		if !exists {
			continue
		}
		timestamp, _ := stamp.(string)
		if _, err := time.Parse(time.RFC3339Nano, timestamp); err != nil || sample.statusWrite != "" {
			return exact, nil, clusterbootstrap.ErrPreparationConfig
		}
		sample.statusWrite = timestamp
		fields = slices.Clone(fields)
		entry = maps.Clone(entry)
		entry["time"] = "<status update>"
		fields[i] = entry
		meta["managedFields"] = fields
	}
	projected["metadata"], projected["status"] = meta, status
	raw, err := json.Marshal(projected)
	if err != nil {
		return exact, nil, err
	}
	return sha256.Sum256(raw), &sample, nil
}
