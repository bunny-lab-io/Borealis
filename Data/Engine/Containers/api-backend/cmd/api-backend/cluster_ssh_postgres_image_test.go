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
		if err != nil || got.Requirements.PostgresImage != (clusterSSHPostgresImage{Configured: sshPostgresConfiguredFixture, Resolved: sshPostgresResolvedFixture, Bootstrap: sshPostgresBootstrapFixture()}) {
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

func sshPostgresBootstrapFixture() clusterSSHSourceExternalImage {
	for _, p := range clusterbootstrap.ExternalImagePins() {
		if strings.HasPrefix(p.Reference, clusterSSHCNPGRepository+":") {
			return clusterSSHSourceExternalImage{Configured: p.Reference, Resolved: clusterSSHCNPGRepository + "@" + p.IndexDigest}
		}
	}
	panic("CNPG pin missing")
}

func TestClusterSSHStoragePostgresBootstrap(t *testing.T) {
	for _, mode := range []string{"valid", "platform digest", "digest spec", "missing init", "extra init", "sidecar init", "missing status", "duplicate status", "wrong name", "status mismatch", "foreign digest", "mutable runtime", "unreviewed configured", "running", "failed", "missing exit", "mixed members", "final drift"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, true)
			pod := f.pod(0)
			spec, status := clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
			init := spec["initContainers"].([]any)[0].(map[string]any)
			runtime := status["initContainerStatuses"].([]any)[0].(map[string]any)
			switch mode {
			case "platform digest":
				for _, p := range clusterbootstrap.ExternalImagePins() {
					if p.Reference == init["image"] {
						for i := range f.a.Source.Members {
							clusterSSHStorageMap(f.pod(i), "status")["initContainerStatuses"].([]any)[0].(map[string]any)["imageID"] = clusterSSHCNPGRepository + "@" + p.ManifestDigest
						}
					}
				}
			case "digest spec":
				for i := range f.a.Source.Members {
					s, st := clusterSSHStorageMap(f.pod(i), "spec"), clusterSSHStorageMap(f.pod(i), "status")
					c, r := s["initContainers"].([]any)[0].(map[string]any), st["initContainerStatuses"].([]any)[0].(map[string]any)
					c["image"], r["image"] = r["imageID"], r["imageID"]
				}
			case "missing init":
				delete(spec, "initContainers")
			case "extra init":
				spec["initContainers"] = append(spec["initContainers"].([]any), init)
			case "sidecar init":
				init["restartPolicy"] = "Always"
			case "missing status":
				delete(status, "initContainerStatuses")
			case "duplicate status":
				status["initContainerStatuses"] = append(status["initContainerStatuses"].([]any), runtime)
			case "wrong name":
				init["name"] = "other"
			case "status mismatch":
				runtime["image"] = "other"
			case "foreign digest":
				runtime["imageID"] = "foreign.invalid/operator@sha256:" + strings.Repeat("a", 64)
			case "mutable runtime":
				runtime["imageID"] = runtime["image"]
			case "unreviewed configured":
				init["image"] = clusterSSHCNPGRepository + ":1.29.0"
				runtime["image"] = init["image"]
			case "running":
				runtime["state"] = map[string]any{"running": map[string]any{"startedAt": "now"}}
			case "failed":
				clusterSSHStorageMap(clusterSSHStorageMap(runtime, "state"), "terminated")["exitCode"] = 1
			case "missing exit":
				delete(clusterSSHStorageMap(clusterSSHStorageMap(runtime, "state"), "terminated"), "exitCode")
			case "mixed members":
				for _, p := range clusterbootstrap.ExternalImagePins() {
					if p.Reference == init["image"] {
						runtime["imageID"] = clusterSSHCNPGRepository + "@" + p.ManifestDigest
					}
				}
			}
			if mode == "final drift" {
				entered := false
				err := withClusterSSHSourceStorage(context.Background(), f.authority, f.get, func(context.Context, clusterSSHStorageRequirements, clusterSSHPreparationChecks) error {
					entered = true
					f.mu.Lock()
					defer f.mu.Unlock()
					runtime["imageID"] = clusterSSHCNPGRepository + "@sha256:" + strings.Repeat("b", 64)
					return nil
				})
				if err == nil || !entered {
					t.Fatal("init drift survived source lifetime")
				}
				return
			}
			_, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			valid := mode == "valid" || mode == "platform digest" || mode == "digest spec"
			if (err == nil) != valid {
				t.Fatalf("bootstrap evidence: %v", err)
			}
		})
	}
}
