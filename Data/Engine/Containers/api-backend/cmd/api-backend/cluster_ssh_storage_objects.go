package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	clusterSSHStorageClaimsPath   = "/api/v1/namespaces/borealis/persistentvolumeclaims"
	clusterSSHStoragePodsPath     = "/api/v1/namespaces/borealis/pods?labelSelector=cnpg.io%2Fcluster%3Dborealis-postgres"
	clusterSSHStoragePostgresPath = "/apis/postgresql.cnpg.io/v1/namespaces/borealis/clusters/borealis-postgres"
	clusterSSHStorageVolumePrefix = "/apis/longhorn.io/v1beta2/namespaces/longhorn-system/volumes/"
	clusterSSHStoragePVPrefix     = "/api/v1/persistentvolumes/"
)

var clusterSSHStorageNameRE = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$`)
var clusterSSHStorageSizeRE = regexp.MustCompile(`^([1-9][0-9]{0,18})(Ki|Mi|Gi|Ti)?$`)
var clusterSSHStorageInstanceRE = regexp.MustCompile(`^borealis-postgres-[1-9][0-9]{0,8}$`)

func clusterSSHStorageName(value string) bool {
	if len(value) < 1 || len(value) > 253 {
		return false
	}
	for _, part := range strings.Split(value, ".") {
		if len(part) > 63 || !clusterSSHStorageNameRE.MatchString(part) {
			return false
		}
	}
	return true
}

// Exact map lookup preserves number types and distinguishes absent/wrong-shaped objects.
func clusterSSHStorageMap(object map[string]any, key string) map[string]any {
	value, _ := object[key].(map[string]any)
	return value
}

func clusterSSHStorageText(m map[string]any, key string) string {
	value, _ := m[key].(string)
	return value
}

// Storage accepts a conservative integer Kubernetes quantity subset. In
// particular, bare bytes are allowed and lowercase m is never interpreted as Mi.
func clusterSSHStorageBytes(value any) (uint64, bool) {
	s, ok := value.(string)
	parts := clusterSSHStorageSizeRE.FindStringSubmatch(s)
	if !ok || parts == nil {
		return 0, false
	}
	n, err := strconv.ParseUint(parts[1], 10, 64)
	factor := map[string]uint64{"": 1, "Ki": 1 << 10, "Mi": 1 << 20, "Gi": 1 << 30, "Ti": 1 << 40}[parts[2]]
	if err != nil || n > math.MaxInt64/factor {
		return 0, false
	}
	return n * factor, true
}

func clusterSSHStorageInteger(value any) (int64, bool) {
	n, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	i, err := strconv.ParseInt(n.String(), 10, 64)
	return i, err == nil && i >= 0 && strconv.FormatInt(i, 10) == n.String()
}

// Raw Kubernetes objects stay private. Decode maps with exact key lookup and
// numbers, then reuse recursive canonical comparison to reject duplicates,
// trailing JSON and excessive nesting before policy reads any field.
func clusterSSHStorageObject(raw []byte) (map[string]any, [32]byte, error) {
	var object map[string]any
	fail := func() (map[string]any, [32]byte, error) {
		return nil, [32]byte{}, clusterbootstrap.ErrPreparationConfig
	}
	if len(raw) == 0 || len(raw) > 2<<20 || !utf8.Valid(raw) {
		return fail()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&object) != nil || object == nil {
		return fail()
	}
	canonical, err := json.Marshal(object)
	if err != nil || !sameClusterSSHStoredJSON(raw, canonical, 0) {
		return fail()
	}
	return object, sha256.Sum256(canonical), nil
}

type clusterSSHStorageIdentity struct{ Name, UID, Revision string }

func clusterSSHStorageMetadata(object map[string]any, version, kind, namespace string) (clusterSSHStorageIdentity, bool) {
	m := clusterSSHStorageMap(object, "metadata")
	id := clusterSSHStorageIdentity{clusterSSHStorageText(m, "name"), clusterSSHStorageText(m, "uid"), clusterSSHStorageText(m, "resourceVersion")}
	if clusterSSHStorageText(object, "apiVersion") != version || clusterSSHStorageText(object, "kind") != kind ||
		clusterSSHStorageText(m, "namespace") != namespace || !clusterSSHStorageName(id.Name) || !clusterUUIDRE.MatchString(id.UID) ||
		id.UID == "00000000-0000-0000-0000-000000000000" || len(id.Revision) < 1 || len(id.Revision) > 128 || m["deletionTimestamp"] != nil {
		return id, false
	}
	if ns, exists := m["namespace"]; exists && ns != namespace {
		return id, false
	}
	for _, c := range id.Revision {
		if c < 33 || c > 126 {
			return id, false
		}
	}
	return id, true
}

func clusterSSHStorageList(object map[string]any, kind string, limit int) ([]map[string]any, bool) {
	items, ok := object["items"].([]any)
	meta := clusterSSHStorageMap(object, "metadata")
	if !ok || len(items) > limit || clusterSSHStorageText(object, "apiVersion") != "v1" || clusterSSHStorageText(object, "kind") != kind+"List" || meta == nil {
		return nil, false
	}
	if value, exists := meta["continue"]; exists && value != "" {
		return nil, false
	}
	if value, exists := meta["remainingItemCount"]; exists {
		n, ok := clusterSSHStorageInteger(value)
		if !ok || n != 0 {
			return nil, false
		}
	}
	result := make([]map[string]any, 0, len(items))
	names, uids := map[string]bool{}, map[string]bool{}
	for _, item := range items {
		x, ok := item.(map[string]any)
		id, valid := clusterSSHStorageMetadata(x, "v1", kind, "borealis")
		if !ok || !valid || names[id.Name] || uids[id.UID] {
			return nil, false
		}
		names[id.Name], uids[id.UID] = true, true
		result = append(result, x)
	}
	return result, true
}

func clusterSSHStorageStrings(value any, wanted ...string) bool {
	items, ok := value.([]any)
	if !ok || len(items) != len(wanted) {
		return false
	}
	for i, item := range items {
		if item != wanted[i] {
			return false
		}
	}
	return true
}

func clusterSSHStorageEmptyList(value any) bool {
	items, ok := value.([]any)
	return value == nil || (ok && len(items) == 0)
}

func clusterSSHStorageEmptyMap(value any) bool {
	object, ok := value.(map[string]any)
	return value == nil || (ok && len(object) == 0)
}

func clusterSSHStorageEmptyText(value any) bool {
	return value == nil || value == ""
}

func clusterSSHStorageOwner(object map[string]any, owner clusterSSHStorageIdentity) bool {
	refs, ok := clusterSSHStorageMap(object, "metadata")["ownerReferences"].([]any)
	if !ok || len(refs) != 1 {
		return false
	}
	r, ok := refs[0].(map[string]any)
	return ok && r["apiVersion"] == "postgresql.cnpg.io/v1" && r["kind"] == "Cluster" && r["name"] == owner.Name && r["uid"] == owner.UID && r["controller"] == true
}

// All fields are public scalar observations, not a storage reservation or
// provisioning grant. Other claims remain occupied inventory, never free space.
type clusterSSHStorageVolume struct {
	Claim, ClaimUID, PV, PVUID, VolumeUID, StorageClass, Role, Node string
	Bytes                                                           uint64
	Replicas                                                        int64
	DataLocality, State, Robustness                                 string
}

type clusterSSHStorageRequirements struct {
	ArtifactReplicaBytes, PostgresInstanceBytes uint64
	ConfiguredPostgresInstances                 int64
	Volumes                                     []clusterSSHStorageVolume
}

func clusterSSHStorageBoundVolume(claim, pv, volume map[string]any) (clusterSSHStorageVolume, error) {
	fail := func() (clusterSSHStorageVolume, error) {
		return clusterSSHStorageVolume{}, clusterbootstrap.ErrPreparationConfig
	}
	c, cOK := clusterSSHStorageMetadata(claim, "v1", "PersistentVolumeClaim", "borealis")
	p, pOK := clusterSSHStorageMetadata(pv, "v1", "PersistentVolume", "")
	v, vOK := clusterSSHStorageMetadata(volume, "longhorn.io/v1beta2", "Volume", "longhorn-system")
	cs, ps, vs := clusterSSHStorageMap(claim, "spec"), clusterSSHStorageMap(pv, "spec"), clusterSSHStorageMap(volume, "spec")
	ct, pt, vt := clusterSSHStorageMap(claim, "status"), clusterSSHStorageMap(pv, "status"), clusterSSHStorageMap(volume, "status")
	ref, csi, ks := clusterSSHStorageMap(ps, "claimRef"), clusterSSHStorageMap(ps, "csi"), clusterSSHStorageMap(vt, "kubernetesStatus")
	class := clusterSSHStorageText(cs, "storageClassName")
	if !cOK || !pOK || !vOK || cs["volumeName"] != p.Name || p.Name != v.Name || !clusterSSHStorageName(class) || ps["storageClassName"] != class ||
		ct["phase"] != "Bound" || pt["phase"] != "Bound" || cs["volumeMode"] != "Filesystem" || ps["volumeMode"] != "Filesystem" ||
		ref["apiVersion"] != "v1" || ref["kind"] != "PersistentVolumeClaim" || ref["namespace"] != "borealis" || ref["name"] != c.Name || ref["uid"] != c.UID ||
		csi["driver"] != clusterLonghornCSIDriver || csi["volumeHandle"] != p.Name || csi["fsType"] != "ext4" ||
		ks["namespace"] != "borealis" || ks["pvcName"] != c.Name || ks["pvName"] != p.Name || ks["pvStatus"] != "Bound" ||
		!clusterSSHStorageEmptyList(ct["conditions"]) || !clusterSSHStorageEmptyText(cs["volumeAttributesClassName"]) || !clusterSSHStorageEmptyText(ps["volumeAttributesClassName"]) || !clusterSSHStorageEmptyMap(ps["nodeAffinity"]) ||
		vs["frontend"] != "blockdev" || vs["dataEngine"] != "v1" || vs["encrypted"] != false || vs["migratable"] != false ||
		!clusterSSHStorageEmptyList(vs["diskSelector"]) || !clusterSSHStorageEmptyList(vs["nodeSelector"]) {
		return fail()
	}
	if x, exists := csi["readOnly"]; exists && x != false {
		return fail()
	}
	requested, ok := clusterSSHStorageBytes(clusterSSHStorageMap(clusterSSHStorageMap(cs, "resources"), "requests")["storage"])
	capacity, capOK := clusterSSHStorageBytes(clusterSSHStorageMap(ct, "capacity")["storage"])
	pvBytes, pvOK := clusterSSHStorageBytes(clusterSSHStorageMap(ps, "capacity")["storage"])
	volumeBytes, volOK := clusterSSHStorageBytes(vs["size"])
	replicas, repOK := clusterSSHStorageInteger(vs["numberOfReplicas"])
	if !ok || !capOK || !pvOK || !volOK || clusterSSHStorageText(vs, "size") != strconv.FormatUint(volumeBytes, 10) || requested != capacity || capacity != pvBytes || pvBytes != volumeBytes ||
		!repOK || (replicas != 1 && replicas != 3) {
		return fail()
	}
	mode, locality := clusterSSHStorageText(vs, "accessMode"), clusterSSHStorageText(vs, "dataLocality")
	access := "ReadWriteOnce"
	if mode == "rwx" {
		access = "ReadWriteMany"
	} else if mode != "rwo" {
		return fail()
	}
	if !clusterSSHStorageStrings(cs["accessModes"], access) || !clusterSSHStorageStrings(ps["accessModes"], access) ||
		(locality != "disabled" && locality != "strict-local") || (locality == "strict-local" && (replicas != 1 || mode != "rwo")) {
		return fail()
	}
	state, robustness := clusterSSHStorageText(vt, "state"), clusterSSHStorageText(vt, "robustness")
	if !textInSet(state, "attached", "detached") || !textInSet(robustness, "healthy", "degraded", "unknown") {
		return fail()
	}
	return clusterSSHStorageVolume{Claim: c.Name, ClaimUID: c.UID, PV: p.Name, PVUID: p.UID, VolumeUID: v.UID, StorageClass: class, Role: "other", Bytes: capacity, Replicas: replicas, DataLocality: locality, State: state, Robustness: robustness}, nil
}
