package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func sshStorageReplicaObject(f *sshStorageFixture, volumeIndex, replicaIndex int) map[string]any {
	v := f.volume(volumeIndex)
	vm := clusterSSHStorageMap(v, "metadata")
	name := clusterSSHStorageText(vm, "name")
	r := sshStorageObject("longhorn.io/v1beta2", "Replica", "longhorn-system", fmt.Sprintf("%s-r-%d", name, replicaIndex))
	m := clusterSSHStorageMap(r, "metadata")
	m["labels"] = map[string]any{"longhornvolume": name}
	// Upstream GetOwnerReferencesForVolume does not set a controller bit.
	m["ownerReferences"] = []any{map[string]any{"apiVersion": "longhorn.io/v1beta2", "kind": "Volume", "name": name, "uid": vm["uid"]}}
	image := sshLonghornDriverPin("longhorn-engine").Reference
	r["spec"] = map[string]any{"volumeName": name, "dataEngine": "v1", "image": image, "active": true}
	state, current := "running", image
	if clusterSSHStorageMap(v, "status")["state"] == "detached" {
		state, current = "stopped", ""
	}
	r["status"] = map[string]any{"currentState": state, "currentImage": current}
	return r
}
func sshStorageReplicaFixture(f *sshStorageFixture) {
	for i := range f.objects[clusterSSHStorageClaimsPath]["items"].([]any) {
		v := f.volume(i)
		n := clusterSSHStorageMap(v, "spec")["numberOfReplicas"].(int)
		items := []any{}
		for j := 0; j < n; j++ {
			r := sshStorageReplicaObject(f, i, j)
			if j >= len(f.a.Source.Members) {
				clusterSSHStorageMap(r, "spec")["nodeID"] = "failed-member"
				clusterSSHStorageMap(r, "spec")["failedAt"] = "2026-10-03T00:00:00Z"
				clusterSSHStorageMap(r, "status")["currentState"] = "unknown"
			}
			items = append(items, r)
		}
		name := clusterSSHStorageText(clusterSSHStorageMap(v, "metadata"), "name")
		f.objects[clusterSSHStorageReplicasPath(name)] = map[string]any{"apiVersion": "longhorn.io/v1beta2", "kind": "ReplicaList", "metadata": map[string]any{"resourceVersion": "1"}, "items": items}
	}
}
func sshStorageReplicaList(f *sshStorageFixture, volume int) map[string]any {
	return f.objects[clusterSSHStorageReplicasPath(clusterSSHStorageText(clusterSSHStorageMap(f.volume(volume), "metadata"), "name"))]
}
func sshStorageReplica(f *sshStorageFixture, volume, replica int) map[string]any {
	return sshStorageReplicaList(f, volume)["items"].([]any)[replica].(map[string]any)
}

func TestClusterSSHStorageReplicaImages(t *testing.T) {
	goodModes := []string{"expansion", "replacement", "stopped absent image", "error empty image", "unknown empty image", "starting empty image", "stopping empty image", "index", "platform", "retained inactive", "detached empty list", "reordered", "inventory bound"}
	modes := append(slices.Clone(goodModes), "running missing image", "missing desired", "unreviewed inactive", "wrong role", "image mismatch", "missing state", "bad state", "null current", "object current", "oversized image", "wrong namespace", "wrong api", "wrong kind", "missing UID", "missing revision", "deleting", "wrong label", "wrong volume", "wrong engine", "missing owner", "duplicate owner", "foreign owner UID", "foreign owner name", "foreign owner kind", "foreign owner api", "malformed owner flag", "missing list", "wrong list version", "wrong list kind", "continuation", "remaining", "duplicate name", "duplicate UID", "empty attached list", "per-list limit", "aggregate limit", "cross-volume cloned UID")
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, true)
			if mode == "expansion" {
				f = newSSHStorageFixture(t, false)
			}
			list := sshStorageReplicaList(f, 0)
			r := sshStorageReplica(f, 0, 0)
			m, spec, status := clusterSSHStorageMap(r, "metadata"), clusterSSHStorageMap(r, "spec"), clusterSSHStorageMap(r, "status")
			owner := m["ownerReferences"].([]any)[0].(map[string]any)
			switch mode {
			case "stopped absent image":
				status["currentState"] = "stopped"
				delete(status, "currentImage")
			case "error empty image", "unknown empty image", "starting empty image", "stopping empty image":
				status["currentState"], status["currentImage"] = strings.Split(mode, " ")[0], ""
			case "index", "platform":
				digest := sshLonghornDriverPin("longhorn-engine").IndexDigest
				if mode == "platform" {
					digest = sshLonghornDriverPin("longhorn-engine").ManifestDigest
				}
				spec["image"], status["currentImage"] = "docker.io/longhornio/longhorn-engine@"+digest, "docker.io/longhornio/longhorn-engine@"+digest
			case "retained inactive", "unreviewed inactive":
				spec["active"] = false
				status["currentState"], status["currentImage"] = "stopped", ""
				if mode == "unreviewed inactive" {
					spec["image"] = "docker.io/longhornio/longhorn-engine:v1.11.0"
				}
			case "detached empty list":
				sshStorageReplicaList(f, len(f.a.Source.Members)+1)["items"] = []any{}
			case "reordered":
				slices.Reverse(list["items"].([]any))
			case "running missing image":
				delete(status, "currentImage")
			case "missing desired":
				delete(spec, "image")
			case "wrong role":
				spec["image"], status["currentImage"] = sshLonghornDriverPin("longhorn-manager").Reference, sshLonghornDriverPin("longhorn-manager").Reference
			case "image mismatch":
				status["currentImage"] = "docker.io/longhornio/longhorn-engine@" + sshLonghornDriverPin("longhorn-engine").ManifestDigest
			case "missing state":
				delete(status, "currentState")
			case "bad state":
				status["currentState"] = "Ready"
			case "null current":
				status["currentImage"] = nil
			case "object current":
				status["currentImage"] = map[string]any{}
			case "oversized image":
				spec["image"] = strings.Repeat("a", 257)
			case "wrong namespace":
				m["namespace"] = "other"
			case "wrong api":
				r["apiVersion"] = "v1"
			case "wrong kind":
				r["kind"] = "Volume"
			case "missing UID":
				delete(m, "uid")
			case "missing revision":
				delete(m, "resourceVersion")
			case "deleting":
				m["deletionTimestamp"] = "2026-10-03T00:00:00Z"
			case "wrong label":
				clusterSSHStorageMap(m, "labels")["longhornvolume"] = "foreign"
			case "wrong volume":
				spec["volumeName"] = "foreign"
			case "wrong engine":
				spec["dataEngine"] = "v2"
			case "missing owner":
				delete(m, "ownerReferences")
			case "duplicate owner":
				m["ownerReferences"] = []any{owner, owner}
			case "foreign owner UID":
				owner["uid"] = newClusterUUID()
			case "foreign owner name":
				owner["name"] = "foreign"
			case "foreign owner kind":
				owner["kind"] = "Engine"
			case "foreign owner api":
				owner["apiVersion"] = "v1"
			case "malformed owner flag":
				owner["controller"] = "true"
			case "missing list":
				delete(f.objects, clusterSSHStorageReplicasPath(owner["name"].(string)))
			case "wrong list version":
				list["apiVersion"] = "v1"
			case "wrong list kind":
				list["kind"] = "List"
			case "continuation":
				clusterSSHStorageMap(list, "metadata")["continue"] = "more"
			case "remaining":
				clusterSSHStorageMap(list, "metadata")["remainingItemCount"] = 1
			case "duplicate name":
				clusterSSHStorageMap(sshStorageReplica(f, 0, 1), "metadata")["name"] = m["name"]
			case "duplicate UID":
				clusterSSHStorageMap(sshStorageReplica(f, 0, 1), "metadata")["uid"] = m["uid"]
			case "cross-volume cloned UID":
				clusterSSHStorageMap(sshStorageReplica(f, 1, 0), "metadata")["uid"] = m["uid"]
			case "empty attached list":
				list["items"] = []any{}
			case "per-list limit", "aggregate limit", "inventory bound":
				n := clusterSSHStorageReplicaLimit
				if mode == "inventory bound" {
					n -= len(f.a.Source.Members) + 1
				}
				if mode == "per-list limit" {
					n++
				}
				items := []any{}
				for j := 0; j < n; j++ {
					items = append(items, sshStorageReplicaObject(f, 0, j))
				}
				list["items"] = items
			}
			value, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			if !slices.Contains(goodModes, mode) {
				if err != clusterbootstrap.ErrPreparationConfig || value.Requirements.observation != "" {
					t.Fatalf("unproved replica escaped: %v", err)
				}
				return
			}
			if err != nil || !clusterSSHStorageReplicaImagesValid(value.Requirements.ReplicaImages, value.Requirements.Volumes) {
				t.Fatalf("valid image evidence rejected: %v", err)
			}
			count := 0
			for i := range f.objects[clusterSSHStorageClaimsPath]["items"].([]any) {
				count += len(sshStorageReplicaList(f, i)["items"].([]any))
			}
			if len(value.Requirements.ReplicaImages) != count {
				t.Fatal("stopped, failed or inactive replica silently dropped")
			}
			for path := range f.calls {
				if strings.Contains(path, "failed-member") {
					t.Fatal("replacement required failed-node live access")
				}
			}
		})
	}
}

func TestClusterSSHStorageReplicaScope(t *testing.T) {
	for _, mode := range []string{"denied", "lost authority", "final image drift", "retained drift", "UID drift", "collection revision", "list order", "caller alias"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, true)
			list := sshStorageReplicaList(f, 0)
			r := sshStorageReplica(f, 0, 0)
			path := clusterSSHStorageReplicasPath(clusterSSHStorageText(clusterSSHStorageMap(f.volume(0), "metadata"), "name"))
			get := func(ctx context.Context, p string, out any) error {
				if p == path && mode == "denied" {
					return errors.New("private replica denial")
				}
				err := f.get(ctx, p, out)
				if p == path && mode == "lost authority" {
					f.mu.Lock()
					f.a.Source.Members[0].NodeUID = newClusterUUID()
					f.mu.Unlock()
				}
				return err
			}
			consumed := false
			err := withClusterSSHSourceStorage(context.Background(), f.authority, get, func(ctx context.Context, v clusterSSHStorageRequirements, c clusterSSHPreparationChecks) error {
				consumed = true
				f.mu.Lock()
				defer f.mu.Unlock()
				switch mode {
				case "final image drift":
					image := "docker.io/longhornio/longhorn-engine@" + sshLonghornDriverPin("longhorn-engine").ManifestDigest
					clusterSSHStorageMap(r, "spec")["image"], clusterSSHStorageMap(r, "status")["currentImage"] = image, image
				case "retained drift":
					clusterSSHStorageMap(sshStorageReplica(f, 3, 0), "metadata")["resourceVersion"] = "2"
				case "UID drift":
					clusterSSHStorageMap(r, "metadata")["uid"] = newClusterUUID()
				case "collection revision":
					clusterSSHStorageMap(list, "metadata")["resourceVersion"] = "999"
				case "list order":
					slices.Reverse(list["items"].([]any))
				case "caller alias":
					v.ReplicaImages[0].Image = "private caller mutation"
				}
				return nil
			})
			good := textInSet(mode, "collection revision", "list order", "caller alias")
			if (err == nil) != good || consumed == textInSet(mode, "denied", "lost authority") {
				t.Fatalf("scope result: consumed=%t err=%v", consumed, err)
			}
		})
	}
}

func TestClusterSSHStorageReplicaProjection(t *testing.T) {
	f := newSSHStorageFixture(t, true)
	value := sshStorageSnapshotFixture(t, f.a)
	for _, mode := range []string{"missing", "missing running image", "wrong owner", "unknown image", "duplicate UID", "duplicate name", "reordered", "missing attached volume", "unknown state"} {
		t.Run(mode, func(t *testing.T) {
			next := value
			next.Requirements.ReplicaImages = slices.Clone(value.Requirements.ReplicaImages)
			r := &next.Requirements.ReplicaImages[0]
			switch mode {
			case "missing":
				next.Requirements.ReplicaImages = nil
			case "missing running image":
				r.State = "running"
				r.CurrentImage = ""
			case "wrong owner":
				r.VolumeUID = newClusterUUID()
			case "unknown image":
				r.Image = "docker.io/longhornio/longhorn-engine:other"
				r.CurrentImage = r.Image
			case "duplicate UID":
				next.Requirements.ReplicaImages[1].UID = r.UID
			case "duplicate name":
				next.Requirements.ReplicaImages[1].Name = r.Name
			case "reordered":
				slices.Reverse(next.Requirements.ReplicaImages)
			case "missing attached volume":
				volume := next.Requirements.Volumes[0].PV
				next.Requirements.ReplicaImages = slices.DeleteFunc(next.Requirements.ReplicaImages, func(r clusterSSHStorageReplicaImage) bool { return r.Volume == volume })
			case "unknown state":
				r.State = "healthy"
			}
			if validClusterSSHStorageSnapshot(next, f.a.Source) {
				t.Fatal("unproved worker replica accepted")
			}
		})
	}
	raw, _ := json.Marshal(value)
	var decoded clusterSSHStorageSnapshot
	if json.Unmarshal(raw, &decoded) != nil || !validClusterSSHStorageSnapshot(decoded, f.a.Source) {
		t.Fatal("replica projection roundtrip failed")
	}
}
