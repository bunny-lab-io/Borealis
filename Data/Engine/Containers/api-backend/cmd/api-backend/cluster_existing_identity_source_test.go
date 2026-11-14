package main

import (
	"borealis/api-backend/internal/engineidentity"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func existingIdentitySourceFixture(t *testing.T) (clusterExistingIdentityPlan, clusterExistingIdentitySourceProcess, map[string]any) {
	t.Helper()
	p := existingIdentityPlanFixture(t)
	process := clusterExistingIdentitySourceProcess{PodName: "api-backend-source-abcde", PodUID: newClusterUUID(), NodeName: p.Members[0].Name}
	env := []any{map[string]any{"name": "BOREALIS_API_BACKGROUND_LOOPS", "value": "1"}}
	for name, field := range map[string]string{"BOREALIS_ENGINE_NODE_NAME": "spec.nodeName", "BOREALIS_ENGINE_POD_NAME": "metadata.name", "BOREALIS_ENGINE_POD_UID": "metadata.uid"} {
		env = append(env, map[string]any{"name": name, "valueFrom": map[string]any{"fieldRef": map[string]any{"apiVersion": "v1", "fieldPath": field}}})
	}
	image := "ghcr.io/bunny-lab-io/borealis/api-backend@sha256:" + strings.Repeat("a", 64)
	pod := map[string]any{
		"metadata": map[string]any{"name": process.PodName, "uid": process.PodUID, "namespace": "borealis",
			"labels":      map[string]any{"app.kubernetes.io/name": "api-backend", "borealis.io/engine-node": process.NodeName, "borealis.io/update-candidate": "false", "borealis.io/traffic-state": "active"},
			"annotations": map[string]any{"borealis.io/revision": p.Baseline.SourceSHA}},
		"spec": map[string]any{"nodeName": process.NodeName,
			"containers": []any{map[string]any{"name": "api-backend", "image": image, "env": env,
				"volumeMounts": []any{map[string]any{"name": "api-secrets", "mountPath": clusterExistingIdentitySourceRoot}}}},
			"volumes": []any{map[string]any{"name": "api-secrets", "hostPath": map[string]any{"path": clusterExistingIdentitySourceRoot, "type": "Directory"}}}},
		"status": map[string]any{"phase": "Running", "containerStatuses": []any{map[string]any{"name": "api-backend", "ready": true, "started": true, "imageID": image,
			"containerID": "containerd://" + strings.Repeat("b", 64), "state": map[string]any{"running": map[string]any{"startedAt": "2026-10-25T00:00:00Z"}}}}},
	}
	return p, process, pod
}

func TestClusterExistingIdentitySourcePod(t *testing.T) {
	for _, mode := range []string{"valid", "pod uid", "node", "deleted", "candidate", "release", "unready", "restart", "image", "imageID", "sidecar", "init malformed", "ephemeral", "literal identity", "duplicate env", "no loops", "subpath", "wrong host", "overlay", "alias overlay", "duplicate volume"} {
		t.Run(mode, func(t *testing.T) {
			p, process, pod := existingIdentitySourceFixture(t)
			meta := clusterSSHStorageMap(pod, "metadata")
			spec, status := clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
			container := anySlice(spec["containers"])[0].(map[string]any)
			runtime := anySlice(status["containerStatuses"])[0].(map[string]any)
			switch mode {
			case "pod uid":
				meta["uid"] = newClusterUUID()
			case "node":
				spec["nodeName"] = p.Members[2].Name
			case "deleted":
				meta["deletionTimestamp"] = "2026-10-25T01:00:00Z"
			case "candidate":
				clusterSSHStorageMap(meta, "labels")["borealis.io/update-candidate"] = "true"
			case "release":
				clusterSSHStorageMap(meta, "annotations")["borealis.io/revision"] = strings.Repeat("b", 40)
			case "unready":
				runtime["ready"] = false
			case "restart":
				runtime["state"] = map[string]any{"waiting": map[string]any{"reason": "ContainerCreating"}}
			case "image":
				container["image"] = "api:latest"
			case "imageID":
				runtime["imageID"] = "sha256:" + strings.Repeat("c", 64)
			case "sidecar":
				spec["containers"] = append(anySlice(spec["containers"]), map[string]any{"name": "sidecar"})
			case "init malformed":
				spec["initContainers"] = "unknown"
			case "ephemeral":
				spec["ephemeralContainers"] = []any{map[string]any{"name": "debug"}}
			case "literal identity":
				container["env"] = append(anySlice(container["env"])[:1], map[string]any{"name": "BOREALIS_ENGINE_POD_UID", "value": process.PodUID})
			case "duplicate env":
				container["env"] = append(anySlice(container["env"]), anySlice(container["env"])[1])
			case "no loops":
				anySlice(container["env"])[0].(map[string]any)["value"] = "0"
			case "subpath":
				anySlice(container["volumeMounts"])[0].(map[string]any)["subPath"] = "other"
			case "wrong host":
				clusterSSHStorageMap(anySlice(spec["volumes"])[0].(map[string]any), "hostPath")["path"] = "/other"
			case "overlay", "alias overlay":
				path := clusterExistingIdentitySourceRoot + "/engine_secret.txt"
				if mode == "alias overlay" {
					path = clusterExistingIdentitySourceRoot + "/./engine_secret.txt"
				}
				container["volumeMounts"] = append(anySlice(container["volumeMounts"]), map[string]any{"name": "overlay", "mountPath": path})
			case "duplicate volume":
				spec["volumes"] = append(anySlice(spec["volumes"]), anySlice(spec["volumes"])[0])
			}
			proof, err := clusterExistingIdentityPodCustody(pod, p, process)
			if mode == "valid" {
				if err != nil || !proof.valid(p) || proof.Process != process {
					t.Fatal("valid source rejected", err)
				}
			} else if err == nil {
				t.Fatal("invalid source accepted")
			}
		})
	}
}

func TestClusterExistingIdentitySourceObservation(t *testing.T) {
	for _, mode := range []string{"valid", "namespace drift", "pod drift", "node drift", "private error", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			p, process, pod := existingIdentitySourceFixture(t)
			var nodes []any
			for _, m := range p.Members {
				n := sshSourceKubernetesFixture(m)
				state := "active"
				if m.NodeID == p.TargetID {
					state = "drained"
				}
				clusterSSHStorageMap(n, "metadata")["labels"] = map[string]any{"borealis.io/engine-node": "true", "borealis.io/application-state": state}
				nodes = append(nodes, n)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls, pods := 0, 0
			read := func(ctx context.Context, path string, out any) error {
				calls++
				if d, ok := ctx.Deadline(); !ok || time.Until(d) > 15*time.Second {
					t.Fatal("unbounded observation")
				}
				var value any
				switch path {
				case "/api/v1/namespaces/kube-system":
					uid := p.KubeSystemUID
					if mode == "namespace drift" && calls > 5 {
						uid = newClusterUUID()
					}
					value = map[string]any{"metadata": map[string]any{"uid": uid}}
				case "/api/v1/nodes":
					if mode == "node drift" && calls > 5 {
						clusterSSHStorageMap(nodes[0].(map[string]any), "metadata")["uid"] = newClusterUUID()
					}
					value = map[string]any{"items": nodes}
				case "/api/v1/namespaces/borealis/pods/" + process.PodName:
					pods++
					if mode == "pod drift" && pods == 2 {
						anySlice(clusterSSHStorageMap(pod, "status")["containerStatuses"])[0].(map[string]any)["containerID"] = "containerd://" + strings.Repeat("c", 64)
					}
					value = pod
				default:
					t.Fatal("unexpected Kubernetes request", path)
				}
				if mode == "private error" {
					return errors.New("private diagnostic")
				}
				if mode == "cancelled" {
					cancel()
				}
				raw, _ := json.Marshal(value)
				return json.Unmarshal(raw, out)
			}
			proof, err := readClusterExistingIdentitySourceCustody(ctx, read, p, process)
			if mode == "valid" {
				if err != nil || !proof.valid(p) || calls != 8 {
					t.Fatal("custody observation failed", err, calls)
				}
			} else if err != errClusterConflict {
				t.Fatal("drift or private diagnostic escaped", err)
			}
		})
	}
}

func TestClusterExistingIdentitySourceMaterial(t *testing.T) {
	for _, mode := range []string{"valid", "other root", "closed", "authority drift", "custody drift", "empty custody", "callback error", "cancelled", "final drift"} {
		t.Run(mode, func(t *testing.T) {
			p, process, pod := existingIdentitySourceFixture(t)
			proof, _ := clusterExistingIdentityPodCustody(pod, p, process)
			root := t.TempDir()
			want := sshIdentitySourceMaterial(t, root)
			consumer, err := engineidentity.AcquireConsumer(root)
			if err != nil {
				t.Fatal(err)
			}
			defer consumer.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls, reads, used := 0, 0, false
			authority := func(context.Context) (string, error) {
				calls++
				if mode == "authority drift" && calls == 2 || mode == "final drift" && calls == 6 {
					return strings.Repeat("b", 64), nil
				}
				return strings.Repeat("a", 64), nil
			}
			custody := func(context.Context) (clusterExistingIdentitySourceCustody, error) {
				reads++
				if mode == "custody drift" && reads == 2 {
					proof.ContainerID = "containerd://" + strings.Repeat("d", 64)
				}
				if mode == "empty custody" {
					proof.Image = ""
				}
				return proof, nil
			}
			if mode == "other root" {
				root = t.TempDir()
			}
			if mode == "closed" {
				consumer.Close()
			}
			if mode == "cancelled" {
				cancel()
			}
			err = withClusterExistingIdentitySourceMaterial(ctx, consumer, root, p, process, authority, custody, func(_ context.Context, got *engineidentity.Material) error {
				used = true
				if !got.Equal(want) {
					t.Fatal("wrong source material")
				}
				if mode == "callback error" {
					return errors.New("private SSH failure")
				}
				return nil
			})
			if mode == "valid" {
				if err != nil || !used || calls != 6 || reads != 3 {
					t.Fatal("source scope failed", err, calls, reads)
				}
			} else {
				if err != errClusterConflict {
					t.Fatal("invalid source accepted", err)
				}
				if mode != "callback error" && mode != "final drift" && used {
					t.Fatal("invalid source reached consumer")
				}
			}
		})
	}
}

func TestClusterExistingIdentityLocalSource(t *testing.T) {
	p, process, _ := existingIdentitySourceFixture(t)
	t.Setenv("BOREALIS_ENGINE_POD_NAME", process.PodName)
	t.Setenv("BOREALIS_ENGINE_POD_UID", process.PodUID)
	t.Setenv("BOREALIS_ENGINE_NODE_NAME", process.NodeName)
	t.Setenv("BOREALIS_API_BACKGROUND_LOOPS", "1")
	if got, err := clusterExistingIdentityLocalSource(p); err != nil || got != process {
		t.Fatal("local source lost", err)
	}
	for _, field := range []string{"BOREALIS_ENGINE_POD_NAME", "BOREALIS_ENGINE_POD_UID", "BOREALIS_ENGINE_NODE_NAME", "BOREALIS_API_BACKGROUND_LOOPS"} {
		t.Run(field, func(t *testing.T) {
			t.Setenv(field, "")
			if _, err := clusterExistingIdentityLocalSource(p); err == nil {
				t.Fatal("missing source identity accepted")
			}
		})
	}
}
