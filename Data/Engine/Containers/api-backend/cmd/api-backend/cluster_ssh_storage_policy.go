package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"encoding/json"
	"path"
	"slices"
	"strconv"
	"strings"
)

const (
	clusterSSHStorageClassPrefix   = "/apis/storage.k8s.io/v1/storageclasses/"
	clusterSSHStorageSettingPrefix = "/apis/longhorn.io/v1beta2/namespaces/longhorn-system/settings/"
)

// Effective provisioning policy is separate from each existing volume's actual
// replica/locality policy. Neither class defaults nor retained claims free space.
type clusterSSHStoragePolicy struct {
	Version                 string                   `json:"version"`
	DefaultPath             string                   `json:"default_path"`
	ReservedPercent         uint64                   `json:"reserved_percent"`
	MinimalAvailablePercent uint64                   `json:"minimal_available_percent"`
	OverProvisioningPercent uint64                   `json:"over_provisioning_percent"`
	DefaultReplicas         int64                    `json:"default_replicas"`
	Classes                 []clusterSSHStorageClass `json:"classes"`
}

type clusterSSHStorageClass struct {
	Name     string `json:"name"`
	UID      string `json:"uid"`
	Replicas int64  `json:"replicas"`
	Locality string `json:"locality"`
	Binding  string `json:"binding"`
	Reclaim  string `json:"reclaim"`
}

var clusterSSHStoragePolicySettings = []string{
	"current-longhorn-version", "default-data-path", "create-default-disk-labeled-nodes",
	"storage-reserved-percentage-for-default-disk", "storage-minimal-available-percentage", "storage-over-provisioning-percentage",
	"default-replica-count", "replica-soft-anti-affinity", "replica-zone-soft-anti-affinity", "replica-disk-soft-anti-affinity",
	"allow-empty-node-selector-volume", "allow-empty-disk-selector-volume", "disable-scheduling-on-cordoned-node", "replica-auto-balance",
}

// Longhorn configuration normally contains a trailing slash. Normalize only
// that spelling at the producer boundary; do not silently clean '..', repeated
// slashes or relative paths supplied by a different policy.
func clusterSSHStoragePath(raw string) (string, bool) {
	if strings.HasSuffix(raw, "/") && raw != "/" {
		raw = strings.TrimSuffix(raw, "/")
	}
	request := clusterremote.FilesystemRequest{MachineID: strings.Repeat("1", 32), BootID: "11111111-1111-4111-8111-111111111111", Paths: []string{raw}}
	return raw, raw != "/" && path.Clean(raw) == raw && request.Validate() == nil
}

func observeClusterSSHStoragePolicy(read func(string) (map[string]any, error), volumes []clusterSSHStorageVolume) (clusterSSHStoragePolicy, error) {
	fail := func() (clusterSSHStoragePolicy, error) {
		return clusterSSHStoragePolicy{}, clusterbootstrap.ErrPreparationConfig
	}
	settings := map[string]string{}
	for _, name := range clusterSSHStoragePolicySettings {
		object, err := read(clusterSSHStorageSettingPrefix + name)
		id, ok := clusterSSHStorageMetadata(object, "longhorn.io/v1beta2", "Setting", "longhorn-system")
		value := clusterSSHStorageText(object, "value")
		if err != nil || !ok || id.Name != name || len(value) == 0 || len(value) > 1024 {
			return fail()
		}
		settings[name] = value
	}
	// These settings define the supported empty-target default disk and one
	// artifact replica per node. Tagged-only provisioning, automatic rebalance and
	// hard zone spreading require a separate placement plan, not optimistic math.
	for name, want := range map[string]string{
		"current-longhorn-version": "v1.12.0", "create-default-disk-labeled-nodes": "false",
		"replica-soft-anti-affinity": "false", "replica-zone-soft-anti-affinity": "true", "replica-disk-soft-anti-affinity": "true",
		"allow-empty-node-selector-volume": "true", "allow-empty-disk-selector-volume": "true",
		"disable-scheduling-on-cordoned-node": "true", "replica-auto-balance": "disabled",
	} {
		if settings[name] != want {
			return fail()
		}
	}
	p := clusterSSHStoragePolicy{Version: settings["current-longhorn-version"]}
	var ok bool
	if p.DefaultPath, ok = clusterSSHStoragePath(settings["default-data-path"]); !ok {
		return fail()
	}
	for name, destination := range map[string]*uint64{
		"storage-reserved-percentage-for-default-disk": &p.ReservedPercent,
		"storage-minimal-available-percentage":         &p.MinimalAvailablePercent,
		"storage-over-provisioning-percentage":         &p.OverProvisioningPercent,
	} {
		n, err := strconv.ParseUint(settings[name], 10, 64)
		if err != nil || strconv.FormatUint(n, 10) != settings[name] || n > 1000 {
			return fail()
		}
		*destination = n
	}
	// v1.12 uses an engine-keyed setting, not a plain integer. Strictly check
	// the complete shape so duplicate/case-aliased keys cannot change semantics.
	var defaults struct {
		V1 string `json:"v1"`
		V2 string `json:"v2"`
	}
	raw := []byte(settings["default-replica-count"])
	if json.Unmarshal(raw, &defaults) != nil {
		return fail()
	}
	canonical, err := json.Marshal(defaults)
	if err != nil || !sameClusterSSHStoredJSON(raw, canonical, 0) || !textInSet(defaults.V1, "1", "2", "3") || !textInSet(defaults.V2, "1", "2", "3") {
		return fail()
	}
	p.DefaultReplicas, _ = strconv.ParseInt(defaults.V1, 10, 64)
	classes := []string{}
	for _, volume := range volumes {
		if !clusterSSHStorageName(volume.StorageClass) {
			return fail()
		}
		classes = append(classes, volume.StorageClass)
	}
	slices.Sort(classes)
	for _, name := range slices.Compact(classes) {
		object, err := read(clusterSSHStorageClassPrefix + name)
		if err != nil {
			return fail()
		}
		class, err := parseClusterSSHStorageClass(object, p)
		if err != nil || class.Name != name {
			return fail()
		}
		p.Classes = append(p.Classes, class)
	}
	if !p.valid(volumes) {
		return fail()
	}
	return p, nil
}

func parseClusterSSHStorageClass(object map[string]any, policy clusterSSHStoragePolicy) (clusterSSHStorageClass, error) {
	fail := func() (clusterSSHStorageClass, error) {
		return clusterSSHStorageClass{}, clusterbootstrap.ErrPreparationConfig
	}
	id, ok := clusterSSHStorageMetadata(object, "storage.k8s.io/v1", "StorageClass", "")
	if !ok || object["provisioner"] != clusterLonghornCSIDriver || !clusterSSHStorageEmptyList(object["mountOptions"]) || !clusterSSHStorageEmptyList(object["allowedTopologies"]) || policy.Version != "v1.12.0" {
		return fail()
	}
	p := clusterSSHStorageMap(object, "parameters")
	if p == nil {
		return fail()
	}
	for key, raw := range p {
		value, ok := raw.(string)
		if !ok || len(value) > 1024 {
			return fail()
		}
		switch key {
		case "numberOfReplicas", "dataLocality":
		case "fsType":
			if value != "ext4" {
				return fail()
			}
		case "dataEngine":
			if value != "v1" {
				return fail()
			}
		case "staleReplicaTimeout":
			n, err := strconv.ParseUint(value, 10, 32)
			if err != nil || n == 0 || strconv.FormatUint(n, 10) != value {
				return fail()
			}
		case "replicaSoftAntiAffinity", "replicaZoneSoftAntiAffinity", "replicaDiskSoftAntiAffinity", "replicaAutoBalance", "unmapMarkSnapChainRemoved":
			if value != "ignored" {
				return fail()
			}
		case "fromBackup", "backingImage", "dataSource", "diskSelector", "nodeSelector":
			if value != "" {
				return fail()
			}
		case "backupTargetName":
			if value != "" && value != "default" {
				return fail()
			}
		case "disableRevisionCounter":
			if !textInSet(value, "true", "false") {
				return fail()
			}
		case "encrypted", "migratable":
			if value != "false" {
				return fail()
			}
		default:
			return fail()
		}
	}
	// Absent fsType/dataEngine are ext4/v1 in the explicitly observed v1.12.0
	// CSI implementation. No absent replica or locality setting is guessed.
	replicas := policy.DefaultReplicas
	if value, exists := p["numberOfReplicas"]; exists {
		if !textInSet(value.(string), "0", "1", "2", "3") {
			return fail()
		}
		if value != "0" {
			replicas, _ = strconv.ParseInt(value.(string), 10, 64)
		}
	}
	locality := clusterSSHStorageText(p, "dataLocality")
	if !textInSet(locality, "disabled", "strict-local") || (locality == "strict-local" && replicas != 1) {
		return fail()
	}
	result := clusterSSHStorageClass{Name: id.Name, UID: id.UID, Replicas: replicas, Locality: locality,
		Binding: clusterSSHStorageText(object, "volumeBindingMode"), Reclaim: clusterSSHStorageText(object, "reclaimPolicy")}
	if !textInSet(result.Binding, "Immediate", "WaitForFirstConsumer") || !textInSet(result.Reclaim, "Delete", "Retain") {
		return fail()
	}
	return result, nil
}

func (p clusterSSHStoragePolicy) valid(volumes []clusterSSHStorageVolume) bool {
	canonical, ok := clusterSSHStoragePath(p.DefaultPath)
	if !ok || canonical != p.DefaultPath || p.Version != "v1.12.0" || p.ReservedPercent >= 100 || p.MinimalAvailablePercent >= 100 || p.OverProvisioningPercent < 1 || p.OverProvisioningPercent > 1000 || p.DefaultReplicas < 1 || p.DefaultReplicas > 3 || len(p.Classes) == 0 || len(p.Classes) > 16 {
		return false
	}
	classes, seenUID := map[string]clusterSSHStorageClass{}, map[string]bool{}
	previous := ""
	for _, c := range p.Classes {
		if !clusterSSHStorageName(c.Name) || c.Name <= previous || !clusterUUIDRE.MatchString(c.UID) || c.UID == "00000000-0000-0000-0000-000000000000" || seenUID[c.UID] || c.Replicas < 1 || c.Replicas > 3 || !textInSet(c.Locality, "disabled", "strict-local") || (c.Locality == "strict-local" && c.Replicas != 1) || !textInSet(c.Binding, "Immediate", "WaitForFirstConsumer") || !textInSet(c.Reclaim, "Delete", "Retain") {
			return false
		}
		classes[c.Name], seenUID[c.UID], previous = c, true, c.Name
	}
	used := map[string]bool{}
	for _, v := range volumes {
		c, exists := classes[v.StorageClass]
		if !exists {
			return false
		}
		used[c.Name] = true
		// Existing artifact replica counts may differ from the class. New PG
		// instances must inherit strict-local singleton, deferred binding/retain.
		if v.Role == "postgres" && (c.Locality != "strict-local" || c.Replicas != 1 || c.Binding != "WaitForFirstConsumer" || c.Reclaim != "Retain") {
			return false
		}
		if v.Role == "artifacts" && c.Locality != "disabled" {
			return false
		}
	}
	return len(used) == len(classes)
}
