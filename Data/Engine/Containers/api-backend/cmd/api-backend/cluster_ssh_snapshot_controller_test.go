package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"errors"
	"strings"
	"testing"
)

func sshSnapshotControllerPin() clusterbootstrap.ExternalImagePin {
	for _, pin := range clusterbootstrap.ExternalImagePins() {
		if strings.HasPrefix(pin.Reference, "registry.k8s.io/sig-storage/snapshot-controller@") {
			return pin
		}
	}
	panic("missing snapshot fixture pin")
}

// Mirrors Data/Engine/K3s/cluster/snapshot-controller.yaml's executable inputs.
func sshSnapshotControllerFixture() map[string]any {
	object := sshStorageObject("apps/v1", "Deployment", "kube-system", "snapshot-controller")
	object["spec"] = map[string]any{"template": map[string]any{"metadata": map[string]any{"labels": map[string]any{"app.kubernetes.io/name": "snapshot-controller"}}, "spec": map[string]any{
		"serviceAccountName": "snapshot-controller",
		"containers": []any{map[string]any{"name": "snapshot-controller", "image": sshSnapshotControllerPin().Reference,
			"args": []any{"--v=2", "--leader-election=true", "--leader-election-namespace=kube-system", "--http-endpoint=:8080"}}},
	}}}
	return object
}

func sshSnapshotControllerSpec(f *sshStorageFixture) map[string]any {
	return clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(f.objects[clusterSSHSnapshotControllerPath], "spec"), "template"), "spec")
}

func sshSnapshotControllerContainer(f *sshStorageFixture) map[string]any {
	return sshSnapshotControllerSpec(f)["containers"].([]any)[0].(map[string]any)
}

func TestClusterSSHSnapshotControllerSourceConfiguration(t *testing.T) {
	for _, mode := range []string{"reviewed index", "platform digest", "split flags", "mixed flags", "reordered flags", "split boolean", "bare boolean",
		"missing deployment", "wrong kind", "wrong namespace", "wrong name", "missing UID", "missing revision", "deleting", "wrong container", "sidecar", "init", "ephemeral",
		"tag", "unreviewed digest", "wrong role", "image type", "command", "missing args", "args type", "argument type", "duplicate flag", "unknown flag", "missing value", "value type", "wrong namespace flag", "disabled election", "endpoint override", "expanded argument", "repeated flag",
		"env", "envFrom", "mount", "device", "unused volume", "working directory", "lifecycle", "restart policy"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			spec, container := sshSnapshotControllerSpec(f), sshSnapshotControllerContainer(f)
			metadata := clusterSSHStorageMap(f.objects[clusterSSHSnapshotControllerPath], "metadata")
			args := container["args"].([]any)
			valid := false
			switch mode {
			case "reviewed index":
				valid = true
			case "platform digest":
				valid = true
				container["image"] = clusterSSHSnapshotControllerRepository + "@" + sshSnapshotControllerPin().ManifestDigest
			case "split flags":
				valid = true
				container["args"] = []any{"--v", "2", "--leader-election=true", "--leader-election-namespace", "kube-system", "--http-endpoint", ":8080"}
			case "mixed flags":
				valid = true
				container["args"] = []any{"--v=2", "--leader-election=true", "--leader-election-namespace=kube-system", "--http-endpoint", ":8080"}
			case "split boolean":
				container["args"] = []any{"--v=2", "--leader-election", "true", "--leader-election-namespace=kube-system", "--http-endpoint=:8080"}
			case "bare boolean":
				args[1] = "--leader-election"
			case "reordered flags":
				valid = true
				args[0], args[3] = args[3], args[0]
			case "missing deployment":
				delete(f.objects, clusterSSHSnapshotControllerPath)
			case "wrong kind":
				f.objects[clusterSSHSnapshotControllerPath]["kind"] = "DaemonSet"
			case "wrong namespace":
				metadata["namespace"] = "other"
			case "wrong name":
				metadata["name"] = "other"
			case "missing UID":
				delete(metadata, "uid")
			case "missing revision":
				delete(metadata, "resourceVersion")
			case "deleting":
				metadata["deletionTimestamp"] = "2026-10-01T00:00:00Z"
			case "wrong container":
				container["name"] = "other"
			case "sidecar":
				spec["containers"] = []any{container, container}
			case "init":
				spec["initContainers"] = []any{container}
			case "ephemeral":
				spec["ephemeralContainers"] = []any{container}
			case "tag":
				container["image"] = clusterSSHSnapshotControllerRepository + ":latest"
			case "unreviewed digest":
				container["image"] = clusterSSHSnapshotControllerRepository + "@sha256:" + strings.Repeat("a", 64)
			case "wrong role":
				container["image"] = sshSystemUpgradePin("kubectl").Reference
			case "image type":
				container["image"] = map[string]any{}
			case "command":
				container["command"] = []any{"other"}
			case "missing args":
				delete(container, "args")
			case "args type":
				container["args"] = "other"
			case "argument type":
				args[0] = map[string]any{}
			case "duplicate flag":
				args[3] = args[0]
			case "unknown flag":
				args[0] = "--config=/other"
			case "missing value":
				args[3] = "--http-endpoint"
			case "value type":
				container["args"] = append(args[:3], "--http-endpoint", 8000)
			case "wrong namespace flag":
				args[2] = "--leader-election-namespace=other"
			case "disabled election":
				args[1] = "--leader-election=false"
			case "endpoint override":
				args[3] = "--http-endpoint=:9090"
			case "expanded argument":
				args[2] = "--leader-election-namespace=$(NAMESPACE)"
			case "repeated flag":
				container["args"] = append(args, args[0])
			case "env":
				container["env"] = []any{map[string]any{"name": "CONFIG", "value": "other"}}
			case "envFrom":
				container["envFrom"] = []any{map[string]any{"configMapRef": map[string]any{"name": "other"}}}
			case "mount":
				container["volumeMounts"] = []any{map[string]any{"name": "other", "mountPath": "/config"}}
			case "device":
				container["volumeDevices"] = []any{map[string]any{"name": "other", "devicePath": "/config"}}
			case "unused volume":
				spec["volumes"] = []any{map[string]any{"name": "other", "emptyDir": map[string]any{}}}
			case "working directory":
				container["workingDir"] = "/other"
			case "lifecycle":
				container["lifecycle"] = map[string]any{"postStart": map[string]any{"exec": map[string]any{"command": []any{"other"}}}}
			case "restart policy":
				container["restartPolicy"] = "Always"
			}
			if valid {
				sshSnapshotRuntimeFixture(f)
			}
			value, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			if valid {
				if err != nil || value.Requirements.SnapshotControllerImage != container["image"] {
					t.Fatalf("reviewed configuration rejected: %v", err)
				}
			} else if err != clusterbootstrap.ErrPreparationConfig || value.Requirements.observation != "" {
				t.Fatalf("unproved input escaped: %v", err)
			}
		})
	}
}

func TestClusterSSHSnapshotControllerDeniedRead(t *testing.T) {
	f := newSSHStorageFixture(t, false)
	get := func(ctx context.Context, path string, out any) error {
		if path == clusterSSHSnapshotControllerPath {
			return errors.New("private authorization failure")
		}
		return f.get(ctx, path, out)
	}
	value, err := observeClusterSSHStorage(context.Background(), f.a.Source, get)
	if err != clusterbootstrap.ErrPreparationConfig || value.Requirements.observation != "" {
		t.Fatalf("denied read escaped: %v", err)
	}
	consumed := false
	err = withClusterSSHSourceStorage(context.Background(), f.authority, get, func(context.Context, clusterSSHStorageRequirements, clusterSSHPreparationChecks) error {
		consumed = true
		return nil
	})
	if err != clusterbootstrap.ErrSessionAuthority || consumed {
		t.Fatalf("denied source reached consumer: %v", err)
	}
}
