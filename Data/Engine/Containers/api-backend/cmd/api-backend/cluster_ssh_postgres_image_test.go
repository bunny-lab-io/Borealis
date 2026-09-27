package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"strings"
	"testing"
)

const sshPostgresConfiguredFixture = clusterSSHPostgresRepository + ":18.4-system-trixie"
const sshPostgresResolvedFixture = clusterSSHPostgresRepository + "@sha256:42708a75345b7a48fdd9257b071830783a97fd228529196b6313187a7198e185"

func sshPostgresContainer(pod map[string]any, status bool) map[string]any {
	if status {
		return clusterSSHStorageMap(pod, "status")["containerStatuses"].([]any)[0].(map[string]any)
	}
	return clusterSSHStorageMap(pod, "spec")["containers"].([]any)[0].(map[string]any)
}

func TestClusterSSHStoragePostgresImage(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		f := newSSHStorageFixture(t, replacement)
		got, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
		if err != nil || got.Requirements.PostgresImage != (clusterSSHPostgresImage{sshPostgresConfiguredFixture, sshPostgresResolvedFixture}) {
			t.Fatal("actual database image missing", err)
		}
		// Explicitly pinned CNPG specs remain supported without tag lookup.
		clusterSSHStorageMap(f.objects[clusterSSHStoragePostgresPath], "spec")["imageName"] = sshPostgresResolvedFixture
		for i := range f.a.Source.Members {
			sshPostgresContainer(f.pod(i), false)["image"] = sshPostgresResolvedFixture
			sshPostgresContainer(f.pod(i), true)["image"] = sshPostgresResolvedFixture
		}
		if _, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get); err != nil {
			t.Fatal("digest-pinned database rejected", err)
		}
	}
}

func TestClusterSSHStoragePostgresImageRejectsIncompleteOrMixedEvidence(t *testing.T) {
	cases := map[string]func(*sshStorageFixture){
		"missing cluster image": func(f *sshStorageFixture) {
			delete(clusterSSHStorageMap(f.objects[clusterSSHStoragePostgresPath], "spec"), "imageName")
		},
		"catalog": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.objects[clusterSSHStoragePostgresPath], "spec")["imageCatalogRef"] = map[string]any{"name": "custom"}
		},
		"missing container": func(f *sshStorageFixture) { delete(clusterSSHStorageMap(f.pod(0), "spec"), "containers") },
		"sidecar": func(f *sshStorageFixture) {
			s := clusterSSHStorageMap(f.pod(0), "spec")
			s["containers"] = append(s["containers"].([]any), map[string]any{"name": "sidecar", "image": "other"})
		},
		"ephemeral": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.pod(0), "spec")["ephemeralContainers"] = []any{map[string]any{"name": "debug"}}
		},
		"wrong container": func(f *sshStorageFixture) { sshPostgresContainer(f.pod(0), false)["name"] = "other" },
		"spec drift": func(f *sshStorageFixture) {
			sshPostgresContainer(f.pod(0), false)["image"] = clusterSSHPostgresRepository + ":17"
		},
		"status drift": func(f *sshStorageFixture) {
			sshPostgresContainer(f.pod(0), true)["image"] = clusterSSHPostgresRepository + ":17"
		},
		"not ready":   func(f *sshStorageFixture) { sshPostgresContainer(f.pod(0), true)["ready"] = false },
		"not started": func(f *sshStorageFixture) { delete(sshPostgresContainer(f.pod(0), true), "started") },
		"terminated": func(f *sshStorageFixture) {
			sshPostgresContainer(f.pod(0), true)["state"] = map[string]any{"terminated": map[string]any{"exitCode": 0}}
		},
		"missing digest": func(f *sshStorageFixture) { delete(sshPostgresContainer(f.pod(0), true), "imageID") },
		"mutable identity": func(f *sshStorageFixture) {
			sshPostgresContainer(f.pod(0), true)["imageID"] = sshPostgresConfiguredFixture
		},
		"foreign repository": func(f *sshStorageFixture) {
			sshPostgresContainer(f.pod(0), true)["imageID"] = "private.invalid/postgres@sha256:" + strings.Repeat("a", 64)
		},
		"mixed member digests": func(f *sshStorageFixture) {
			sshPostgresContainer(f.pod(1), true)["imageID"] = clusterSSHPostgresRepository + "@sha256:" + strings.Repeat("a", 64)
		},
		"duplicate status": func(f *sshStorageFixture) {
			s := clusterSSHStorageMap(f.pod(0), "status")
			s["containerStatuses"] = append(s["containerStatuses"].([]any), sshPostgresContainer(f.pod(0), true))
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			f := newSSHStorageFixture(t, true)
			change(f)
			got, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			if err != clusterbootstrap.ErrPreparationConfig || got.Requirements.PostgresImage != (clusterSSHPostgresImage{}) {
				t.Fatal("invalid or partial runtime identity escaped")
			}
		})
	}
}

func TestClusterSSHStoragePostgresImageBoundToScope(t *testing.T) {
	f := newSSHStorageFixture(t, false)
	entered := false
	err := withClusterSSHSourceStorage(context.Background(), f.authority, f.get, func(context.Context, clusterSSHStorageRequirements, clusterSSHPreparationChecks) error {
		entered = true
		f.mu.Lock()
		defer f.mu.Unlock()
		sshPostgresContainer(f.pod(0), true)["imageID"] = clusterSSHPostgresRepository + "@sha256:" + strings.Repeat("b", 64)
		return nil
	})
	if err == nil || !entered {
		t.Fatal("final reread accepted replaced database image", err)
	}
}
