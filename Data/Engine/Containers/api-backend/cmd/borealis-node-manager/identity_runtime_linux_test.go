//go:build linux

package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func identityRuntimeRequestFixture() clusterbootstrap.SessionRequest {
	return clusterbootstrap.SessionRequest{
		Protocol: 1, Action: "verify", Nonce: strings.Repeat("c", 64), Repository: "bunny-lab-io/Borealis",
		Release: "2026.10.999-rc.1", SourceSHA: strings.Repeat("a", 40), ManagerSHA256: strings.Repeat("b", 64), AllowQualification: true,
		Binding: clusterbootstrap.SessionBinding{
			ClusterID: "11111111-1111-4111-8111-111111111111", OperationID: "22222222-2222-4222-8222-222222222222",
			TargetID: "33333333-3333-4333-8333-333333333333", HolderID: "44444444-4444-4444-8444-444444444444",
			Generation: 7, OperationAttempt: 2, Address: "192.168.3.251", Port: 22, Hostname: "engine-02",
			MachineID: strings.Repeat("1", 32), HostKeyAlgorithm: "ssh-ed25519",
			HostKeyFingerprint: "SHA256:" + base64.RawStdEncoding.EncodeToString(make([]byte, 32)),
		},
	}
}

func identityRuntimeImageFixture(request clusterbootstrap.SessionRequest, role string) map[string]any {
	digest := strings.Repeat(string('a'+rune(len(role)%6)), 64)
	config := strings.Repeat(string('1'+rune(len(role)%6)), 64)
	return map[string]any{
		"image":             "docker.io/borealis-engine/" + role + "@sha256:" + digest,
		"archive_reference": "docker.io/borealis-engine/" + role + ":release-" + request.SourceSHA,
		"hash":              digest, "mode": "prod", "manifest_digest": "sha256:" + digest, "config_digest": "sha256:" + config,
	}
}

func identityRuntimeManifestFixture(request clusterbootstrap.SessionRequest) []byte {
	services := map[string]any{}
	for _, role := range []string{"api-backend", "borealis-operator", "job-scheduler", "postgres-db", "remote-desktop-guacd", "site-worker", "traefik-edge", "webui-frontend", "wireguard-tunnel"} {
		services[role] = identityRuntimeImageFixture(request, role)
	}
	raw, _ := json.Marshal(map[string]any{"schema_version": 1, "project": "borealis-engine", "mode": "prod",
		"source_sha": request.SourceSHA, "release": request.Release, "services": services})
	return raw
}

func identityRuntimeDeploymentFixture(request clusterbootstrap.SessionRequest, root, role, name string, images map[string]string) map[string]any {
	container := map[string]any{"name": role, "image": images[role],
		"volumeMounts": []any{map[string]any{"name": "api-secrets", "mountPath": root, "readOnly": role == "job-scheduler"}}}
	if role == "job-scheduler" {
		container["env"] = []any{map[string]any{"name": "BOREALIS_SITE_WORKER_IMAGE", "value": images["site-worker"]}}
	}
	return map[string]any{
		"metadata": map[string]any{
			"labels":      map[string]any{"app.kubernetes.io/name": name, "borealis.io/engine-node": request.Binding.Hostname, "borealis.io/node-workload": "true"},
			"annotations": map[string]any{"borealis.io/revision": request.SourceSHA},
		},
		"spec": map[string]any{"replicas": 0, "template": map[string]any{
			"metadata": map[string]any{"annotations": map[string]any{"borealis.io/revision": request.SourceSHA}},
			"spec": map[string]any{
				"containers": []any{container},
				"volumes":    []any{map[string]any{"name": "api-secrets", "hostPath": map[string]any{"path": root}}},
			},
		}},
	}
}

func identityRuntimeWorkloadsFixture(request clusterbootstrap.SessionRequest, root string, images map[string]string) []byte {
	items := []any{
		identityRuntimeDeploymentFixture(request, root, "api-backend", "api-backend", images),
		identityRuntimeDeploymentFixture(request, root, "job-scheduler", "job-scheduler", images),
	}
	raw, _ := json.Marshal(map[string]any{"items": items})
	return raw
}

func identityRuntimeFixture(t *testing.T) (clusterbootstrap.SessionRequest, []byte, []byte, identityRuntimeMount) {
	t.Helper()
	request := identityRuntimeRequestFixture()
	manifest := identityRuntimeManifestFixture(request)
	images, _, err := identityRuntimeImages(manifest, request)
	if err != nil {
		t.Fatal(err)
	}
	workloads := identityRuntimeWorkloadsFixture(request, identityRuntimeRoot, images)
	mount := identityRuntimeMount{Device: 1, Inode: 2, Mount: 3, Mode: 0o40750, UID: actionRuntimeID, GID: actionRuntimeID}
	return request, manifest, workloads, mount
}

func TestIdentityRuntimeProofRequiresStableReleaseWorkloadsAndQuiescence(t *testing.T) {
	for _, mode := range []string{"valid", "manager", "manifest", "workloads", "mount", "consumer", "manager drift", "manifest drift", "workload drift", "mount drift", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			request, manifest, workloads, mount := identityRuntimeFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			managerCalls, manifestCalls, workloadCalls, mountCalls := 0, 0, 0, 0
			reader := identityRuntimeReader{
				manager: func(context.Context) (string, error) {
					managerCalls++
					if mode == "manager" || mode == "manager drift" && managerCalls > 1 {
						return strings.Repeat("d", 64), nil
					}
					return request.ManagerSHA256, nil
				},
				manifest: func() ([]byte, error) {
					manifestCalls++
					if mode == "manifest" {
						return []byte(`{}`), nil
					}
					if mode == "manifest drift" && manifestCalls > 1 {
						return append(append([]byte{}, manifest...), '\n'), nil
					}
					return manifest, nil
				},
				deployments: func(context.Context, string) ([]byte, error) {
					workloadCalls++
					if mode == "workloads" {
						return []byte(`{"items":[]}`), nil
					}
					if mode == "workload drift" && workloadCalls > 1 {
						return append(append([]byte{}, workloads...), '\n'), nil
					}
					return workloads, nil
				},
				mount: func(string, int, int) (identityRuntimeMount, error) {
					mountCalls++
					if mode == "mount" {
						return identityRuntimeMount{}, errIdentityRuntimeProof
					}
					if mode == "mount drift" && mountCalls > 1 {
						changed := mount
						changed.Inode++
						return changed, nil
					}
					return mount, nil
				},
				quiesced: func(context.Context, string, identityRuntimeMount) error {
					if mode == "consumer" {
						return errIdentityRuntimeProof
					}
					return nil
				},
			}
			if mode == "canceled" {
				cancel()
			}
			err := reader.check(ctx, request, identityRuntimeRoot, actionRuntimeID, actionRuntimeID)
			if (err == nil) != (mode == "valid" || mode == "workload drift") {
				t.Fatalf("runtime proof boundary failed: %v", err)
			}
			// Whitespace-only Kubernetes JSON changes do not change selected
			// immutable workload evidence; semantic projection remains stable.
			if mode == "valid" && (managerCalls != 2 || manifestCalls != 2 || workloadCalls != 2 || mountCalls != 2) {
				t.Fatal("runtime evidence was not re-read")
			}
		})
	}
}

func TestIdentityRuntimeImagesRejectChangedReleaseEvidence(t *testing.T) {
	request := identityRuntimeRequestFixture()
	for _, mode := range []string{"valid", "source", "release", "count", "service set", "image", "unused image", "archive", "manifest digest", "config digest", "mode"} {
		t.Run(mode, func(t *testing.T) {
			var payload map[string]any
			_ = json.Unmarshal(identityRuntimeManifestFixture(request), &payload)
			services := payload["services"].(map[string]any)
			api := services["api-backend"].(map[string]any)
			switch mode {
			case "source":
				payload["source_sha"] = strings.Repeat("d", 40)
			case "release":
				payload["release"] = "2026.10.998-rc.1"
			case "count":
				delete(services, "postgres-db")
			case "service set":
				services["foreign-service"] = services["postgres-db"]
				delete(services, "postgres-db")
			case "image":
				api["image"] = "docker.io/borealis-engine/api-backend:latest"
			case "unused image":
				services["postgres-db"].(map[string]any)["image"] = "docker.io/borealis-engine/postgres-db:latest"
			case "archive":
				api["archive_reference"] = "docker.io/borealis-engine/api-backend:latest"
			case "manifest digest":
				api["manifest_digest"] = "sha256:" + strings.Repeat("z", 64)
			case "config digest":
				api["config_digest"] = "sha256:" + strings.Repeat("z", 64)
			case "mode":
				api["mode"] = "dev"
			}
			raw, _ := json.Marshal(payload)
			_, _, err := identityRuntimeImages(raw, request)
			if (err == nil) != (mode == "valid") {
				t.Fatalf("image evidence boundary failed: %v", err)
			}
		})
	}
}

func TestIdentityRuntimeWorkloadsRejectUnsafeConsumerRestart(t *testing.T) {
	request := identityRuntimeRequestFixture()
	images, _, _ := identityRuntimeImages(identityRuntimeManifestFixture(request), request)
	for _, mode := range []string{"valid", "replicas", "revision", "template revision", "node", "image", "worker image", "mount", "writable scheduler mount", "unknown consumer", "missing api", "candidate", "duplicate candidate"} {
		t.Run(mode, func(t *testing.T) {
			var payload map[string]any
			_ = json.Unmarshal(identityRuntimeWorkloadsFixture(request, identityRuntimeRoot, images), &payload)
			items := payload["items"].([]any)
			api := items[0].(map[string]any)
			scheduler := items[1].(map[string]any)
			apiSpec := api["spec"].(map[string]any)
			apiTemplate := apiSpec["template"].(map[string]any)
			apiPod := apiTemplate["spec"].(map[string]any)
			switch mode {
			case "replicas":
				apiSpec["replicas"] = 1
			case "revision":
				api["metadata"].(map[string]any)["annotations"].(map[string]any)["borealis.io/revision"] = strings.Repeat("d", 40)
			case "template revision":
				apiTemplate["metadata"].(map[string]any)["annotations"].(map[string]any)["borealis.io/revision"] = strings.Repeat("d", 40)
			case "node":
				api["metadata"].(map[string]any)["labels"].(map[string]any)["borealis.io/engine-node"] = "engine-03"
			case "image":
				apiPod["containers"].([]any)[0].(map[string]any)["image"] = "docker.io/borealis-engine/api-backend:latest"
			case "worker image":
				scheduler["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)["env"] = []any{map[string]any{"name": "BOREALIS_SITE_WORKER_IMAGE", "value": "latest"}}
			case "mount":
				apiPod["containers"].([]any)[0].(map[string]any)["volumeMounts"] = []any{}
			case "writable scheduler mount":
				scheduler["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)["volumeMounts"].([]any)[0].(map[string]any)["readOnly"] = false
			case "unknown consumer":
				other := identityRuntimeDeploymentFixture(request, identityRuntimeRoot, "api-backend", "foreign", images)
				items = append(items, other)
				payload["items"] = items
			case "missing api":
				payload["items"] = items[1:]
			case "candidate":
				items = append(items, identityRuntimeDeploymentFixture(request, identityRuntimeRoot, "api-backend", "api-backend-candidate", images))
				payload["items"] = items
			case "duplicate candidate":
				candidate := identityRuntimeDeploymentFixture(request, identityRuntimeRoot, "api-backend", "api-backend-candidate", images)
				payload["items"] = append(items, candidate, candidate)
			}
			raw, _ := json.Marshal(payload)
			_, err := identityRuntimeWorkloads(raw, request, identityRuntimeRoot, images)
			if (err == nil) != (mode == "valid" || mode == "candidate") {
				t.Fatalf("workload evidence boundary failed: %v", err)
			}
		})
	}
}

func TestIdentityRuntimeMountAndProcessProof(t *testing.T) {
	root := filepath.Join(t.TempDir(), "secrets")
	if err := os.Mkdir(root, 0o750); err != nil {
		t.Fatal(err)
	}
	mount, err := captureIdentityRuntimeMount(root, os.Geteuid(), os.Getegid())
	if err != nil || mount.Device == 0 || mount.Inode == 0 || mount.Mount == 0 {
		t.Fatal("identity mount not captured", err)
	}
	link := root + "-link"
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if _, err := captureIdentityRuntimeMount(link, os.Geteuid(), os.Getegid()); err == nil {
		t.Fatal("symlinked identity mount accepted")
	}

	proc := t.TempDir()
	write := func(name, value string) {
		t.Helper()
		path := filepath.Join(proc, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("1/ns/mnt", "host namespace")
	write("2/ns/mnt", "container namespace")
	write("2/cmdline", "sleep\x0010\x00")
	if err := os.Symlink("/usr/bin/true", filepath.Join(proc, "2", "exe")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/", filepath.Join(proc, "2", "root")); err != nil {
		t.Fatal(err)
	}
	if err := identityRuntimeProcessesAbsent(context.Background(), proc, root, mount); !errors.Is(err, errIdentityRuntimeProof) {
		t.Fatal("container identity mount accepted", err)
	}
	if err := os.Remove(filepath.Join(proc, "2", "root")); err != nil {
		t.Fatal(err)
	}
	isolated := t.TempDir()
	if err := os.Symlink(isolated, filepath.Join(proc, "2", "root")); err != nil {
		t.Fatal(err)
	}
	if err := identityRuntimeProcessesAbsent(context.Background(), proc, root, mount); err != nil {
		t.Fatal("unrelated container mount rejected", err)
	}
	if err := os.Remove(filepath.Join(proc, "2", "ns", "mnt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(proc, "1", "ns", "mnt"), filepath.Join(proc, "2", "ns", "mnt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(proc, "2", "exe")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/usr/local/bin/borealis-api-backend-go", filepath.Join(proc, "2", "exe")); err != nil {
		t.Fatal(err)
	}
	if err := identityRuntimeProcessesAbsent(context.Background(), proc, root, mount); !errors.Is(err, errIdentityRuntimeProof) {
		t.Fatal("known host consumer accepted", err)
	}
}
