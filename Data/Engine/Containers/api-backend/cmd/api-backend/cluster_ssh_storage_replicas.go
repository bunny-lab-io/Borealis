package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"slices"
	"strings"
)

const clusterSSHStorageReplicasPrefix = "/apis/longhorn.io/v1beta2/namespaces/longhorn-system/replicas?labelSelector=longhornvolume%3D"
const clusterSSHStorageReplicasSuffix = "&limit=64"
const clusterSSHStorageReplicaLimit = 64 // Bounded evidence inventory, never a capacity/readiness minimum.

func clusterSSHStorageReplicasPath(volume string) string {
	return clusterSSHStorageReplicasPrefix + volume + clusterSSHStorageReplicasSuffix
}
func clusterSSHStorageReplicasPathValid(path string) bool {
	volume, ok := strings.CutPrefix(path, clusterSSHStorageReplicasPrefix)
	if !ok {
		return false
	}
	volume, ok = strings.CutSuffix(volume, clusterSSHStorageReplicasSuffix)
	return ok && clusterSSHStorageName(volume)
}

// No replica is filtered by node, health or active state. Stopped, failed and
// retained replicas contribute image evidence, never reclaimed capacity.
type clusterSSHStorageReplicaImage struct {
	Name         string `json:"name"`
	UID          string `json:"uid"`
	Volume       string `json:"volume"`
	VolumeUID    string `json:"volume_uid"`
	Image        string `json:"image"`
	CurrentImage string `json:"current_image"`
	State        string `json:"state"`
}

func (r clusterSSHStorageReplicaImage) valid() bool {
	return clusterSSHStorageName(r.Name) && clusterUUIDRE.MatchString(r.UID) && r.UID != "00000000-0000-0000-0000-000000000000" &&
		clusterSSHLonghornImageValid(r.Image, "longhorn-engine") &&
		textInSet(r.State, "running", "stopped", "error", "unknown", "starting", "stopping") &&
		(r.CurrentImage == r.Image || (r.CurrentImage == "" && r.State != "running"))
}

func clusterSSHStorageReplicaImagesValid(replicas []clusterSSHStorageReplicaImage, volumes []clusterSSHStorageVolume) bool {
	if replicas == nil || len(replicas) > clusterSSHStorageReplicaLimit {
		return false
	}
	owners, counts := map[string]string{}, map[string]int{}
	for _, v := range volumes {
		owners[v.PV] = v.VolumeUID
	}
	names, ids := map[string]bool{}, map[string]bool{}
	previous := ""
	for _, r := range replicas {
		key := r.Volume + "\x00" + r.Name
		if !r.valid() || r.VolumeUID == "" || owners[r.Volume] != r.VolumeUID || key <= previous || names[r.Name] || ids[r.UID] {
			return false
		}
		names[r.Name], ids[r.UID], previous = true, true, key
		counts[r.Volume]++
	}
	for _, v := range volumes {
		// A complete empty list is meaningful for detached retained inventory,
		// but cannot substantiate an attached volume. Do not require desired
		// replica count or failed-member health before replacement.
		if v.State == "attached" && counts[v.PV] == 0 {
			return false
		}
	}
	return true
}

func observeClusterSSHStorageReplicaImages(readList func(string, string, string, int) ([]map[string]any, error), volumes []clusterSSHStorageVolume) ([]clusterSSHStorageReplicaImage, error) {
	fail := func() ([]clusterSSHStorageReplicaImage, error) { return nil, clusterbootstrap.ErrPreparationConfig }
	if readList == nil || len(volumes) < 1 || len(volumes) > 16 {
		return fail()
	}
	result := []clusterSSHStorageReplicaImage{}
	for _, v := range volumes {
		objects, err := readList(clusterSSHStorageReplicasPath(v.PV), "Replica", "longhorn-system", clusterSSHStorageReplicaLimit)
		if err != nil || len(result)+len(objects) > clusterSSHStorageReplicaLimit {
			return fail()
		}
		for _, object := range objects {
			id, ok := clusterSSHStorageMetadata(object, "longhorn.io/v1beta2", "Replica", "longhorn-system")
			metadata, spec, status := clusterSSHStorageMap(object, "metadata"), clusterSSHStorageMap(object, "spec"), clusterSSHStorageMap(object, "status")
			if !ok || clusterSSHStorageMap(metadata, "labels")["longhornvolume"] != v.PV || spec["volumeName"] != v.PV || spec["dataEngine"] != "v1" {
				return fail()
			}
			owners, ok := metadata["ownerReferences"].([]any)
			if !ok || len(owners) != 1 {
				return fail()
			}
			owner, ok := owners[0].(map[string]any)
			if !ok || owner["apiVersion"] != "longhorn.io/v1beta2" || owner["kind"] != "Volume" || owner["name"] != v.PV || owner["uid"] != v.VolumeUID {
				return fail()
			}
			// Longhorn's Volume owner reference has no controller bit. Shape-check
			// any supplied bit without treating its absence as foreign ownership.
			if flag, exists := owner["controller"]; exists {
				if _, ok := flag.(bool); !ok {
					return fail()
				}
			}
			if current, exists := status["currentImage"]; exists {
				if _, ok := current.(string); !ok {
					return fail()
				}
			}
			r := clusterSSHStorageReplicaImage{Name: id.Name, UID: id.UID, Volume: v.PV, VolumeUID: v.VolumeUID,
				Image: clusterSSHStorageText(spec, "image"), CurrentImage: clusterSSHStorageText(status, "currentImage"), State: clusterSSHStorageText(status, "currentState")}
			if !r.valid() {
				return fail()
			}
			result = append(result, r)
		}
	}
	slices.SortFunc(result, func(a, b clusterSSHStorageReplicaImage) int {
		return strings.Compare(a.Volume+"\x00"+a.Name, b.Volume+"\x00"+b.Name)
	})
	if !clusterSSHStorageReplicaImagesValid(result, volumes) {
		return fail()
	}
	return result, nil
}
