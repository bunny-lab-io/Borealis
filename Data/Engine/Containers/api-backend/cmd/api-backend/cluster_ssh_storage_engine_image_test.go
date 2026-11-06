package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestClusterSSHStorageVolumeEngineImages(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		count := 3
		if replacement {
			count++
		}
		for index := range count {
			for _, mode := range []string{"tag", "index", "platform", "missing desired", "missing current", "empty desired", "empty current", "null desired", "null current", "object desired", "object current", "wrong role", "unreviewed retained", "upgrade", "rollback", "runtime prefix", "whitespace", "oversized"} {
				t.Run(fmt.Sprintf("replacement=%t/volume=%d/%s", replacement, index, mode), func(t *testing.T) {
					f := newSSHStorageFixture(t, replacement)
					volume := f.volume(index)
					spec, status := clusterSSHStorageMap(volume, "spec"), clusterSSHStorageMap(volume, "status")
					pin := sshLonghornDriverPin("longhorn-engine")
					image := pin.Reference
					switch mode {
					case "index":
						image = "docker.io/longhornio/longhorn-engine@" + pin.IndexDigest
					case "platform":
						image = "docker.io/longhornio/longhorn-engine@" + pin.ManifestDigest
					case "wrong role":
						image = sshLonghornDriverPin("longhorn-manager").Reference
					case "unreviewed retained":
						image = "docker.io/longhornio/longhorn-engine:v1.11.0"
					case "runtime prefix":
						image = "docker-pullable://docker.io/longhornio/longhorn-engine@" + pin.ManifestDigest
					case "whitespace":
						image += " "
					case "oversized":
						image = strings.Repeat("a", 257)
					}
					spec["image"], status["currentImage"] = image, image
					switch mode {
					case "missing desired":
						delete(spec, "image")
					case "missing current":
						delete(status, "currentImage")
					case "empty desired":
						spec["image"] = ""
					case "empty current":
						status["currentImage"] = ""
					case "null desired":
						spec["image"] = nil
					case "null current":
						status["currentImage"] = nil
					case "object desired":
						spec["image"] = map[string]any{"image": image}
					case "object current":
						status["currentImage"] = map[string]any{"image": image}
					case "upgrade":
						spec["image"] = "docker.io/longhornio/longhorn-engine@" + pin.ManifestDigest
					case "rollback":
						status["currentImage"] = "docker.io/longhornio/longhorn-engine@" + pin.ManifestDigest
					}
					value, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
					if !textInSet(mode, "tag", "index", "platform") {
						if err != clusterbootstrap.ErrPreparationConfig || value.Requirements.observation != "" {
							t.Fatalf("unproved volume image escaped: %v", err)
						}
						return
					}
					if err != nil {
						t.Fatalf("stable reviewed image rejected: %v", err)
					}
					v := value.Requirements.Volumes[index]
					if v.EngineImage != image || v.CurrentEngineImage != image {
						t.Fatal("volume-specific references replaced by manager default")
					}
					if index == count-1 && (v.State != "detached" || v.Role != "other" || v.Bytes != 20<<30) {
						t.Fatal("retained detached volume lost occupied capacity or became active")
					}
					snapshot := clusterSSHStorageSnapshot{Requirements: value.Requirements, Observation: value.Requirements.observation}
					snapshot.Requirements.observation = ""
					if !validClusterSSHStorageSnapshot(snapshot, f.a.Source) {
						t.Fatal("worker rejected observed references")
					}
				})
			}
		}
	}
}

func TestClusterSSHStorageVolumeEngineImageScope(t *testing.T) {
	for _, mode := range []string{"denied", "lost authority", "initial drift", "final drift", "detached drift"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, true)
			index := 0
			if mode == "detached drift" {
				index = len(f.a.Source.Members) + 1
			}
			volume := f.volume(index)
			path := clusterSSHStorageVolumePrefix + clusterSSHStorageText(clusterSSHStorageMap(volume, "metadata"), "name")
			change := func() {
				image := "docker.io/longhornio/longhorn-engine@" + sshLonghornDriverPin("longhorn-engine").ManifestDigest
				clusterSSHStorageMap(volume, "spec")["image"] = image
				clusterSSHStorageMap(volume, "status")["currentImage"] = image
			}
			reads := 0
			get := func(ctx context.Context, p string, out any) error {
				if p == path && mode == "denied" {
					return errors.New("private storage denial")
				}
				err := f.get(ctx, p, out)
				if p == path {
					reads++
					f.mu.Lock()
					if mode == "initial drift" && reads == 1 {
						change()
					}
					if mode == "lost authority" {
						f.a.Source.Members[0].NodeUID = newClusterUUID()
					}
					f.mu.Unlock()
				}
				return err
			}
			consumed := false
			err := withClusterSSHSourceStorage(context.Background(), f.authority, get, func(context.Context, clusterSSHStorageRequirements, clusterSSHPreparationChecks) error {
				consumed = true
				f.mu.Lock()
				defer f.mu.Unlock()
				change()
				return nil
			})
			if err == nil || consumed != textInSet(mode, "final drift", "detached drift") {
				t.Fatalf("volume image drift/authority escaped: consumed=%t err=%v", consumed, err)
			}
		})
	}
}
