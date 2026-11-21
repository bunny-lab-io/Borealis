package main

import (
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
	revision string
	writers  map[string]*clusterSSHStorageUpdate
}

// Associate each observation with its writer. A kubelet heartbeat cannot
// explain a concurrent write attributed to the etcd supervisor.
type clusterSSHStorageUpdate struct {
	writeTime      time.Time
	clocks         map[string]time.Time
	counters       map[string]int64
	packageMessage string
}

func (s *clusterSSHStorageUsage) writer(name string) *clusterSSHStorageUpdate {
	if s.writers[name] == nil {
		s.writers[name] = &clusterSSHStorageUpdate{}
	}
	return s.writers[name]
}

func (s clusterSSHStorageUsage) consistentWith(prior clusterSSHStorageUsage) bool {
	if len(s.writers) != len(prior.writers) {
		return false
	}
	changed := false
	for name, current := range s.writers {
		last := prior.writers[name]
		if last == nil || current.writeTime.Before(last.writeTime) {
			return false
		}
		for key, clock := range last.clocks {
			if current.clocks[key].Before(clock) {
				return false
			}
		}
		different := !maps.Equal(current.counters, last.counters) || !maps.Equal(current.clocks, last.clocks) || current.packageMessage != last.packageMessage
		if !different && !current.writeTime.Equal(last.writeTime) {
			return false
		}
		changed = changed || different
	}
	return changed || s.revision == prior.revision
}

func (s clusterSSHStorageObservation) sameInputs(prior clusterSSHStorageObservation) bool {
	if !maps.Equal(s.receipts, prior.receipts) || !reflect.DeepEqual(s.Requirements, prior.Requirements) || len(s.usage) != len(prior.usage) {
		return false
	}
	for path, now := range s.usage {
		last, ok := prior.usage[path]
		if !ok || !now.consistentWith(last) {
			return false
		}
	}
	return true
}

// Hash the stable inputs and retain recognized telemetry for the next recheck.
// Downstream validators still receive the original object.
func clusterSSHStorageTelemetryReceipt(path string, object map[string]any, exact [32]byte) ([32]byte, *clusterSSHStorageUsage, error) {
	var kind, name string
	switch {
	case strings.HasPrefix(path, "/api/v1/nodes/"):
		return clusterSSHKubeNodeReceipt(strings.TrimPrefix(path, "/api/v1/nodes/"), object, exact)
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
	sample := clusterSSHStorageUsage{revision: id.Revision, writers: map[string]*clusterSSHStorageUpdate{}}
	update := sample.writer("longhorn-manager/status")
	update.counters = map[string]int64{}
	if kind == "Volume" {
		raw, exists := status["actualSize"]
		if !exists {
			return exact, nil, nil
		}
		n, ok := clusterSSHStorageInteger(raw)
		if !ok {
			return exact, nil, clusterbootstrap.ErrPreparationConfig
		}
		update.counters["actualSize"] = n
		status["actualSize"] = 0
	} else {
		if err := clusterSSHRequiredPackageOrder(status, update); err != nil {
			return exact, nil, err
		}
		disks := maps.Clone(clusterSSHStorageMap(status, "diskStatus"))
		for key, raw := range disks {
			disk, _ := raw.(map[string]any)
			disk = maps.Clone(disk)
			if disk["healthDataLastCollectedAt"] != nil && disk["healthDataLastCollectedAt"] != "" {
				if err := update.projectClock(disk, "healthDataLastCollectedAt", key); err != nil {
					return exact, nil, err
				}
				disks[key] = disk
			}
			if _, exists := disk["storageAvailable"]; !exists {
				continue
			}
			available, ok := clusterSSHStorageInteger(disk["storageAvailable"])
			maximum, valid := clusterSSHStorageInteger(disk["storageMaximum"])
			if !ok || !valid || available > maximum {
				return exact, nil, clusterbootstrap.ErrPreparationConfig
			}
			update.counters[key] = available
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
		if len(update.counters) == 0 && len(update.clocks) == 0 && update.packageMessage == "" {
			return exact, nil, nil
		}
		if disks != nil {
			status["diskStatus"] = disks
		}
	}
	return clusterSSHProjectStorageMetadata(object, status, clusterSSHStorageMap(object, "metadata"), sample, exact)
}
