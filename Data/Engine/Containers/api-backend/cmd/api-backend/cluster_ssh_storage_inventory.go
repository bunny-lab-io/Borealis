package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
)

type clusterSSHStorageObservation struct {
	Requirements clusterSSHStorageRequirements
	receipts     map[string][32]byte
}

// No raw Kubernetes object or diagnostic survives the collection. Receipts
// bind exact objects (including revision/spec/status); list ordering and the
// collection's global resourceVersion are not individual object changes.
func observeClusterSSHStorage(ctx context.Context, source clusterSSHSourceCohort,
	getJSON func(context.Context, string, any) error) (clusterSSHStorageObservation, error) {
	fail := func() (clusterSSHStorageObservation, error) {
		return clusterSSHStorageObservation{}, clusterbootstrap.ErrPreparationConfig
	}
	if getJSON == nil || ctx.Err() != nil || len(source.Members) < 1 || len(source.Members) > 2 {
		return fail()
	}
	result := clusterSSHStorageObservation{receipts: map[string][32]byte{}}
	read := func(path string) (map[string]any, error) {
		var raw json.RawMessage
		if getJSON(ctx, path, &raw) != nil || ctx.Err() != nil {
			return nil, clusterbootstrap.ErrPreparationConfig
		}
		object, digest, err := clusterSSHStorageObject(raw)
		if err != nil {
			return nil, err
		}
		if path != clusterSSHStorageClaimsPath && path != clusterSSHStoragePodsPath {
			result.receipts[path] = digest
		}
		return object, nil
	}
	readList := func(path, kind string, limit int) ([]map[string]any, error) {
		object, err := read(path)
		if err != nil {
			return nil, err
		}
		items, ok := clusterSSHStorageList(object, kind, limit)
		if !ok {
			return nil, clusterbootstrap.ErrPreparationConfig
		}
		slices.SortFunc(items, func(a, b map[string]any) int {
			return strings.Compare(clusterSSHStorageText(clusterSSHStorageMap(a, "metadata"), "name"), clusterSSHStorageText(clusterSSHStorageMap(b, "metadata"), "name"))
		})
		raw, err := json.Marshal(items)
		if err != nil {
			return nil, clusterbootstrap.ErrPreparationConfig
		}
		result.receipts[path] = sha256.Sum256(raw)
		return items, nil
	}
	cluster, err := read(clusterSSHStoragePostgresPath)
	if err != nil {
		return fail()
	}
	owner, ok := clusterSSHStorageMetadata(cluster, "postgresql.cnpg.io/v1", "Cluster", "borealis")
	spec, status := clusterSSHStorageMap(cluster, "spec"), clusterSSHStorageMap(cluster, "status")
	storage := clusterSSHStorageMap(spec, "storage")
	pgBytes, sizeOK := clusterSSHStorageBytes(storage["size"])
	pgClass := clusterSSHStorageText(storage, "storageClass")
	instances, instancesOK := clusterSSHStorageInteger(spec["instances"])
	ready, readyOK := clusterSSHStorageInteger(status["readyInstances"])
	wantInstances := int64(1)
	if len(source.Members) == 2 {
		wantInstances = 3
	}
	if !ok || owner.Name != "borealis-postgres" || !sizeOK || !clusterSSHStorageName(pgClass) || !instancesOK || instances != wantInstances || !readyOK || ready != int64(len(source.Members)) ||
		!clusterSSHStorageEmptyMap(spec["walStorage"]) || !clusterSSHStorageEmptyList(spec["tablespaces"]) || !clusterSSHStorageEmptyMap(spec["ephemeralVolumeSource"]) || !clusterSSHStorageEmptyMap(storage["pvcTemplate"]) {
		return fail()
	}
	for key, value := range storage {
		switch key {
		case "size", "storageClass", "pvcTemplate":
		case "resizeInUseVolumes":
			if _, ok := value.(bool); !ok {
				return fail()
			}
		default:
			return fail()
		}
	}
	pods, err := readList(clusterSSHStoragePodsPath, "Pod", 3)
	if err != nil || len(pods) != len(source.Members) {
		return fail()
	}
	memberNodes := map[string]bool{}
	for _, member := range source.Members {
		if memberNodes[member.Name] || !clusterSSHStorageName(member.Name) {
			return fail()
		}
		memberNodes[member.Name] = true
	}
	activeClaims, occupiedNodes := map[string]string{}, map[string]bool{}
	primaryFound := false
	for _, pod := range pods {
		id, _ := clusterSSHStorageMetadata(pod, "v1", "Pod", "borealis")
		ps, pt := clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
		node := clusterSSHStorageText(ps, "nodeName")
		if !clusterSSHStorageInstanceRE.MatchString(id.Name) || !clusterSSHStorageOwner(pod, owner) || !memberNodes[node] || occupiedNodes[node] || pt["phase"] != "Running" ||
			clusterSSHStorageMap(clusterSSHStorageMap(pod, "metadata"), "labels")["cnpg.io/cluster"] != "borealis-postgres" {
			return fail()
		}
		conditions, ok := pt["conditions"].([]any)
		readyConditions := 0
		if !ok {
			return fail()
		}
		for _, value := range conditions {
			c, ok := value.(map[string]any)
			if !ok {
				return fail()
			}
			if c["type"] == "Ready" {
				if c["status"] != "True" {
					return fail()
				}
				readyConditions++
			}
		}
		if readyConditions != 1 {
			return fail()
		}
		volumes, ok := ps["volumes"].([]any)
		claimCount := 0
		if !ok || len(volumes) > 32 {
			return fail()
		}
		for _, value := range volumes {
			v, ok := value.(map[string]any)
			if !ok {
				return fail()
			}
			if _, exists := v["ephemeral"]; exists {
				return fail()
			}
			if claim, exists := v["persistentVolumeClaim"]; exists {
				c, ok := claim.(map[string]any)
				if !ok || c["claimName"] != id.Name {
					return fail()
				}
				if ro, exists := c["readOnly"]; exists && ro != false {
					return fail()
				}
				claimCount++
			}
		}
		if claimCount != 1 {
			return fail()
		}
		activeClaims[id.Name] = node
		occupiedNodes[node] = true
		primaryFound = primaryFound || status["currentPrimary"] == id.Name
	}
	if !primaryFound {
		return fail()
	}
	claims, err := readList(clusterSSHStorageClaimsPath, "PersistentVolumeClaim", 16)
	if err != nil || len(claims) < len(pods)+1 {
		return fail()
	}
	seenPV, seenPVUID, seenVolumeUID := map[string]bool{}, map[string]bool{}, map[string]bool{}
	artifactFound := false
	activeFound := 0
	for _, claim := range claims {
		cs := clusterSSHStorageMap(claim, "spec")
		id, _ := clusterSSHStorageMetadata(claim, "v1", "PersistentVolumeClaim", "borealis")
		pvName := clusterSSHStorageText(cs, "volumeName")
		if !clusterSSHStorageName(pvName) || seenPV[pvName] {
			return fail()
		}
		seenPV[pvName] = true
		pv, err := read(clusterSSHStoragePVPrefix + pvName)
		if err != nil {
			return fail()
		}
		volume, err := read(clusterSSHStorageVolumePrefix + pvName)
		if err != nil {
			return fail()
		}
		value, err := clusterSSHStorageBoundVolume(claim, pv, volume)
		if err != nil || seenPVUID[value.PVUID] || seenVolumeUID[value.VolumeUID] {
			return fail()
		}
		seenPVUID[value.PVUID], seenVolumeUID[value.VolumeUID] = true, true
		if id.Name == clusterSharedArtifactPVCName {
			if !clusterSSHStorageEmptyList(clusterSSHStorageMap(claim, "metadata")["ownerReferences"]) || !clusterSSHStorageStrings(cs["accessModes"], "ReadWriteMany") || value.DataLocality != "disabled" || value.Replicas < int64(len(source.Members)) || value.State != "attached" || !textInSet(value.Robustness, "healthy", "degraded") {
				return fail()
			}
			artifactFound = true
			value.Role = "artifacts"
			result.Requirements.ArtifactReplicaBytes = value.Bytes
		} else if node, active := activeClaims[id.Name]; active {
			vs, vt := clusterSSHStorageMap(volume, "spec"), clusterSSHStorageMap(volume, "status")
			if !clusterSSHStorageOwner(claim, owner) || value.Bytes != pgBytes || value.StorageClass != pgClass || value.DataLocality != "strict-local" || value.Replicas != 1 ||
				value.State != "attached" || value.Robustness != "healthy" || vs["nodeID"] != node || vt["currentNodeID"] != node {
				return fail()
			}
			value.Role, value.Node = "postgres", node
			activeFound++
		}
		result.Requirements.Volumes = append(result.Requirements.Volumes, value)
	}
	if !artifactFound || activeFound != len(activeClaims) || ctx.Err() != nil {
		return fail()
	}
	result.Requirements.PostgresInstanceBytes = pgBytes
	result.Requirements.ConfiguredPostgresInstances = instances
	return result, nil
}

// Fixed GET-only transport uses existing controller TLS/token and existing
// PVC/PV/Pod/CNPG/Longhorn read permissions. No StorageClass read or RBAC change.
func (c *kubernetesAPIClient) getClusterSSHStorageJSON(ctx context.Context, path string, out any) error {
	valid := path == clusterSSHStorageClaimsPath || path == clusterSSHStoragePodsPath || path == clusterSSHStoragePostgresPath || path == "/api/v1/namespaces/kube-system" || path == "/api/v1/nodes"
	for _, prefix := range []string{clusterSSHStoragePVPrefix, clusterSSHStorageVolumePrefix} {
		if strings.HasPrefix(path, prefix) {
			valid = clusterSSHStorageName(strings.TrimPrefix(path, prefix))
			break
		}
	}
	if !valid {
		return clusterbootstrap.ErrPreparationConfig
	}
	return c.clusterSSHPrivateJSON(ctx, http.MethodGet, path, nil, out)
}
