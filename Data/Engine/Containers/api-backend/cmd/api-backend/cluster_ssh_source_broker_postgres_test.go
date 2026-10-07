package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestClusterSSHSourceBrokerPostgresAuthorityAndPrivateTransfer(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "qualification", "absent CNPG config", "controller changed", "worker expired", "credential removed", "Aegis locked", "Secret UID changed", "Secret revision changed", "excluded Secret data changed", "storage revision changed", "storage placement changed during Job", "bootstrap image changed during Job", "cert-manager solver changed during Job", "Longhorn CSI changed during Job", "CNPG configuration changed during Job", "Longhorn replica images changed during Job", "Longhorn volume images changed during Job", "Longhorn manager runtime changed during Job", "Longhorn share runtime changed during Job", "Longhorn manager Pod replaced during Job", "kube-vip runtime changed during Job", "kube-vip Pod replaced during Job", "kube-vip image changed during Job", "kube-vip interface mismatch", "snapshot image changed during Job", "upgrade kubectl changed during Job", "Longhorn driver runtime changed during Job", "Longhorn driver init changed during Job", "Longhorn driver owner changed during Job", "Longhorn provisioner runtime changed during Job", "Longhorn provisioner owner changed during Job", "Longhorn resizer runtime changed during Job", "Longhorn resizer owner changed during Job", "Longhorn snapshotter runtime changed during Job", "Longhorn snapshotter owner changed during Job", "Longhorn csi-attacher startup changed during Job", "Longhorn csi-provisioner startup changed during Job", "Longhorn csi-resizer startup changed during Job", "Longhorn csi-snapshotter startup changed during Job", "Longhorn attacher runtime changed during Job", "Longhorn attacher owner changed during Job", "Longhorn UI runtime changed during Job", "Longhorn UI owner changed during Job", "Longhorn UI changed during Job", "Longhorn manager setting changed during Job"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHPreparationAuthorityFixtureForTopology(t, mode == "replacement")
			if mode == "qualification" {
				f.exec(t, `UPDATE engine.cluster_operations SET state='waiting',current_step='qualify_ssh_targets' WHERE id=$1`, f.op.ID)
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET state='queued',current_step='inspection_complete',lease_holder='',lease_expires_at=0,lease_generation=inspected_generation WHERE operation_id=$1`, f.op.ID)
				work, err := f.c.store.claimClusterSSHQualification(f.ctx, f.op.ID, newClusterUUID())
				if err != nil {
					t.Fatal(err)
				}
				f.lease, f.sealed = work.Claims[0].Lease, work.Claims[0].Sealed
			}
			f.c.store.db.SetMaxOpenConns(1)
			current, err := f.read(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			storage := newSSHStorageFixtureForAuthority(t, current)
			if mode == "kube-vip interface mismatch" {
				sshKubeVIPEnv(storage, "vip_interface")["value"] = "ens19"
			}
			before := f.events(t)
			var jobs, secretReads atomic.Int64
			var changed atomic.Bool
			var mu sync.Mutex
			var job map[string]any
			podUID := newClusterUUID()
			kubernetes := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				// The joined authority heartbeat may independently borrow this one
				// connection. A bounded acquisition proves the request released its
				// connection before HTTP, without mistaking a short heartbeat for a leak.
				probeCtx, stop := context.WithTimeout(r.Context(), time.Second)
				connection, probeErr := f.c.store.db.Conn(probeCtx)
				if connection != nil {
					if connection.Close() != nil {
						t.Error("pool probe release")
					}
				}
				stop()
				if probeErr != nil && r.Context().Err() == nil {
					t.Error("database connection unavailable during Kubernetes request")
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				if r.Method == "GET" {
					if mode == "absent CNPG config" && clusterSSHCNPGOptionalConfigPath(r.URL.Path) {
						kind := "configmaps"
						if r.URL.Path == clusterSSHCNPGSecretPath {
							kind = "secrets"
						}
						w.WriteHeader(http.StatusNotFound)
						_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "Status", "status": "Failure", "reason": "NotFound", "code": 404, "details": map[string]any{"kind": kind, "name": "cnpg-controller-manager-config"}})
						return
					}
					if object, ok := storage.objects[r.URL.RequestURI()]; ok && r.URL.Path != "/api/v1/nodes" && r.URL.Path != "/api/v1/namespaces/kube-system" {
						if mode == "storage revision changed" && changed.Load() {
							clusterSSHStorageMap(storage.volume(0), "metadata")["resourceVersion"] = "2"
						}
						_ = json.NewEncoder(w).Encode(object)
						return
					}
					switch r.URL.Path {
					case "/api/v1/nodes":
						items := make([]any, 0, len(current.Source.Members))
						for _, member := range current.Source.Members {
							items = append(items, sshSourceKubernetesFixture(member))
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
						return
					case "/api/v1/namespaces/kube-system":
						_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"uid": current.Source.KubeSystemUID}})
						return
					case clusterSSHRuntimeSecretPath:
						secretReads.Add(1)
						value := sshPreparationSecretFixture()
						if changed.Load() {
							switch mode {
							case "Secret UID changed":
								value["metadata"].(map[string]any)["uid"] = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
							case "Secret revision changed":
								value["metadata"].(map[string]any)["resourceVersion"] = "changed-revision"
							case "excluded Secret data changed":
								value["data"].(map[string]string)["BOREALIS_REPO_ROOT"] = base64.StdEncoding.EncodeToString([]byte("/changed"))
							}
						}
						_ = json.NewEncoder(w).Encode(value)
						return
					}
				}
				if r.Method == "POST" && r.URL.Path == "/apis/batch/v1/namespaces/borealis/jobs" {
					jobs.Add(1)
					job = nil
					if json.NewDecoder(r.Body).Decode(&job) != nil {
						t.Error("Job decode")
						w.WriteHeader(500)
						return
					}
					job["metadata"].(map[string]any)["uid"] = newClusterUUID()
					job["status"] = map[string]any{"succeeded": 1, "conditions": []any{map[string]any{"type": "Complete", "status": "True"}}}
					switch mode {
					case "CNPG configuration changed during Job":
						clusterSSHStorageMap(storage.objects[clusterSSHCNPGSecretPath], "metadata")["resourceVersion"] = "2"
					case "Longhorn replica images changed during Job":
						r := sshStorageReplica(storage, 0, 0)
						image := "docker.io/longhornio/longhorn-engine@" + sshLonghornDriverPin("longhorn-engine").ManifestDigest
						clusterSSHStorageMap(r, "spec")["image"] = image
						clusterSSHStorageMap(r, "status")["currentImage"] = image
					case "Longhorn volume images changed during Job":
						image := "docker.io/longhornio/longhorn-engine@" + sshLonghornDriverPin("longhorn-engine").ManifestDigest
						clusterSSHStorageMap(storage.volume(0), "spec")["image"] = image
						clusterSSHStorageMap(storage.volume(0), "status")["currentImage"] = image
					case "Longhorn manager runtime changed during Job":
						sshLonghornManagerRuntime(storage, 0)["imageID"] = "docker.io/longhornio/longhorn-manager@" + sshLonghornDriverPin("longhorn-manager").IndexDigest
					case "Longhorn share runtime changed during Job":
						sshLonghornManagerShareRuntime(storage, 0)["imageID"] = "docker.io/longhornio/longhorn-share-manager@" + sshLonghornDriverPin("longhorn-share-manager").IndexDigest
					case "Longhorn manager Pod replaced during Job":
						clusterSSHStorageMap(sshLonghornManagerPod(storage, 0), "metadata")["uid"] = newClusterUUID()
					case "kube-vip runtime changed during Job":
						sshKubeVIPRuntime(storage, 0)["imageID"] = sshKubeVIPPin().Reference
					case "kube-vip Pod replaced during Job":
						clusterSSHStorageMap(sshKubeVIPPod(storage, 0), "metadata")["uid"] = newClusterUUID()
					case "kube-vip image changed during Job":
						sshKubeVIPContainer(storage)["image"] = clusterSSHKubeVIPRepository + "@" + sshKubeVIPPin().ManifestDigest
					case "snapshot image changed during Job":
						sshSnapshotControllerContainer(storage)["image"] = clusterSSHSnapshotControllerRepository + "@" + sshSnapshotControllerPin().ManifestDigest
					case "upgrade kubectl changed during Job":
						clusterSSHStorageMap(storage.objects[clusterSSHSystemUpgradeConfigPath], "data")["SYSTEM_UPGRADE_JOB_KUBECTL_IMAGE"] = "docker.io/rancher/kubectl@" + sshSystemUpgradePin("kubectl").ManifestDigest
					case "Longhorn driver runtime changed during Job":
						sshLonghornDriverRuntime(storage, 0)["imageID"] = "docker.io/longhornio/longhorn-manager@" + sshLonghornDriverPin("longhorn-manager").IndexDigest
					case "Longhorn driver init changed during Job":
						sshLonghornDriverInitRuntime(storage, 0)["imageID"] = "docker.io/longhornio/longhorn-manager@" + sshLonghornDriverPin("longhorn-manager").IndexDigest
					case "Longhorn driver owner changed during Job":
						clusterSSHStorageMap(storage.objects[clusterSSHLonghornDriverReplicaSetPrefix+"longhorn-driver-deployer-abcdef"], "metadata")["annotations"] = map[string]any{"changed": "receipt"}
					case "Longhorn provisioner runtime changed during Job":
						sshLonghornCSIRuntime(storage, clusterSSHLonghornProvisioner, 0)["imageID"] = clusterSSHLonghornProvisioner.repository() + "@" + sshLonghornDriverPin("csi-provisioner").IndexDigest
					case "Longhorn provisioner owner changed during Job":
						clusterSSHStorageMap(storage.objects[clusterSSHLonghornAttacherReplicaSetPrefix+"csi-provisioner-abcdef"], "metadata")["annotations"] = map[string]any{"changed": "receipt"}
					case "Longhorn resizer runtime changed during Job":
						sshLonghornCSIRuntime(storage, clusterSSHLonghornResizer, 0)["imageID"] = clusterSSHLonghornResizer.repository() + "@" + sshLonghornDriverPin("csi-resizer").IndexDigest
					case "Longhorn resizer owner changed during Job":
						clusterSSHStorageMap(storage.objects[clusterSSHLonghornAttacherReplicaSetPrefix+"csi-resizer-abcdef"], "metadata")["annotations"] = map[string]any{"changed": "receipt"}
					case "Longhorn snapshotter runtime changed during Job":
						sshLonghornCSIRuntime(storage, clusterSSHLonghornSnapshotter, 0)["imageID"] = clusterSSHLonghornSnapshotter.repository() + "@" + sshLonghornDriverPin("csi-snapshotter").IndexDigest
					case "Longhorn snapshotter owner changed during Job":
						clusterSSHStorageMap(storage.objects[clusterSSHLonghornAttacherReplicaSetPrefix+"csi-snapshotter-abcdef"], "metadata")["annotations"] = map[string]any{"changed": "receipt"}
					case "Longhorn csi-attacher startup changed during Job":
						sshLonghornCSIStartupObjects(storage, "csi-attacher")[2]["args"].([]any)[0] = "--v=3"
					case "Longhorn csi-provisioner startup changed during Job":
						sshLonghornCSIStartupObjects(storage, "csi-provisioner")[2]["args"].([]any)[0] = "--v=3"
					case "Longhorn csi-resizer startup changed during Job":
						sshLonghornCSIStartupObjects(storage, "csi-resizer")[2]["args"].([]any)[0] = "--v=3"
					case "Longhorn csi-snapshotter startup changed during Job":
						sshLonghornCSIStartupObjects(storage, "csi-snapshotter")[2]["args"].([]any)[0] = "--v=3"
					case "Longhorn attacher runtime changed during Job":
						sshLonghornAttacherRuntime(storage, 0)["imageID"] = "docker.io/longhornio/csi-attacher@" + sshLonghornDriverPin("csi-attacher").IndexDigest
					case "Longhorn attacher owner changed during Job":
						clusterSSHStorageMap(storage.objects[clusterSSHLonghornAttacherReplicaSetPrefix+"csi-attacher-abcdef"], "metadata")["annotations"] = map[string]any{"changed": "receipt"}
					case "Longhorn UI runtime changed during Job":
						sshLonghornUIRuntime(storage, 0)["imageID"] = "docker.io/longhornio/longhorn-ui@" + sshLonghornDriverPin("longhorn-ui").IndexDigest
					case "Longhorn UI owner changed during Job":
						clusterSSHStorageMap(storage.objects[clusterSSHLonghornUIReplicaSetPrefix+"longhorn-ui-abcdef"], "metadata")["annotations"] = map[string]any{"changed": "receipt"}
					case "Longhorn UI changed during Job":
						sshLonghornUIContainer(storage)["image"] = "docker.io/longhornio/longhorn-ui@" + sshLonghornDriverPin("longhorn-ui").ManifestDigest
					case "Longhorn manager setting changed during Job":
						sshLonghornManagerSetImage(storage, "support-bundle-kit", "docker.io/longhornio/support-bundle-kit@"+sshLonghornDriverPin("support-bundle-kit").ManifestDigest)
					case "Longhorn CSI changed during Job":
						sshLonghornDriverContainer(storage)["env"].([]any)[3].(map[string]any)["value"] = "docker.io/longhornio/csi-attacher@" + sshLonghornDriverPin("csi-attacher").ManifestDigest
					case "cert-manager solver changed during Job":
						sshCertManagerContainer(storage, "cert-manager")["args"].([]any)[3] = "--acme-http01-solver-image=quay.io/jetstack/cert-manager-acmesolver@" + sshCertManagerPin("acmesolver").ManifestDigest
					case "bootstrap image changed during Job":
						runtime := clusterSSHStorageMap(storage.pod(0), "status")["initContainerStatuses"].([]any)[0].(map[string]any)
						runtime["imageID"] = clusterSSHCNPGRepository + "@sha256:" + strings.Repeat("c", 64)
					case "storage placement changed during Job":
						clusterSSHStorageMap(storage.volume(1), "status")["currentNodeID"] = "foreign"
					case "controller changed":
						f.exec(t, `UPDATE engine.cluster_application_leases SET holder='changed-controller' WHERE name=$1`, clusterControllerLeaseName)
					case "worker expired":
						f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=0 WHERE id=$1`, f.lease.TargetID)
					case "credential removed":
						f.exec(t, `DELETE FROM engine.cluster_onboarding_credentials WHERE target_id=$1`, f.lease.TargetID)
					case "Aegis locked":
						f.aegis.mu.Lock()
						clear(f.aegis.key)
						f.aegis.key = nil
						f.aegis.mu.Unlock()
					}
					w.WriteHeader(201)
					_ = json.NewEncoder(w).Encode(job)
					return
				}
				if r.Method == "GET" && job != nil {
					if strings.HasPrefix(r.URL.Path, "/apis/batch/") {
						_ = json.NewEncoder(w).Encode(job)
						return
					}
					if r.URL.Path == "/api/v1/namespaces/borealis/pods" {
						node := job["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["nodeName"]
						for _, member := range current.Source.Members {
							if member.Name != node {
								continue
							}
							network := sshSourceNetworkFixture(member, current.K3sVersion)
							_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{sourceActionPod(t, job, network, podUID)}})
							return
						}
					}
				}
				t.Error("unexpected Kubernetes request")
				w.WriteHeader(500)
			}))
			defer kubernetes.Close()
			runner := &kubernetesClusterStepRunner{controllerHolder: f.lease.ControllerHolder, namespace: "borealis", actionImage: "registry.example/api@sha256:" + strings.Repeat("a", 64),
				jobPollInterval: time.Millisecond, kube: &kubernetesAPIClient{baseURL: kubernetes.URL, token: "private-fixture", httpClient: kubernetes.Client()}}
			controller := &clusterController{store: f.c.store, runner: runner, holder: f.c.holder}
			t.Setenv("BOREALIS_OPERATOR_SECRET", sshBrokerTestSecret)
			server := httptest.NewServer(controller.healthServer().Handler)
			defer server.Close()
			client := sshBrokerClient(t, server.URL)
			client.httpClient.Timeout = 10 * time.Second
			read := client.preparationRead(f.read, f.lease, f.baseline, f.sealed)
			expected, settings, err := read(f.ctx)
			good := mode == "absent CNPG config" || mode == "expansion" || mode == "replacement" || mode == "qualification" || strings.Contains(mode, "Secret") || mode == "storage revision changed"
			if good {
				if err != nil || expected.Target.TargetID != f.lease.TargetID || !reflect.DeepEqual(settings, sshPreparationRuntimeFixture()) {
					t.Fatal("valid private broker source rejected")
				}
				changed.Store(true)
				_, next, err := read(f.ctx)
				if strings.Contains(mode, "Secret") || mode == "storage revision changed" {
					if err != clusterbootstrap.ErrPreparationConfig || next != nil {
						t.Fatal("source observation drift accepted")
					}
				} else if err != nil || !reflect.DeepEqual(next, settings) || jobs.Load() != int64(4*len(current.Source.Members)) {
					t.Fatal("fresh source Job not observed per member/read")
				}
			} else if err != clusterbootstrap.ErrPreparationConfig || settings != nil {
				t.Fatal("stale authority returned private settings")
			}
			if jobs.Load() == 0 {
				t.Fatal("actual controller source transport was not exercised")
			}
			if textInSet(mode, "controller changed", "worker expired", "credential removed") && secretReads.Load() != 0 {
				t.Fatal("private Secret read after persisted authority loss")
			}
			// Storage drift is found by the final inventory after valid source
			// reads. Its error must discard that response, not pretend DB ownership
			// was lost before the private Secret acquisition.
			if textInSet(mode, "storage placement changed during Job", "bootstrap image changed during Job", "cert-manager solver changed during Job", "Longhorn CSI changed during Job", "CNPG configuration changed during Job", "Longhorn replica images changed during Job", "Longhorn volume images changed during Job", "Longhorn manager runtime changed during Job", "Longhorn share runtime changed during Job", "Longhorn manager Pod replaced during Job", "kube-vip runtime changed during Job", "kube-vip Pod replaced during Job", "kube-vip image changed during Job", "kube-vip interface mismatch", "snapshot image changed during Job", "upgrade kubectl changed during Job", "Longhorn driver runtime changed during Job", "Longhorn driver init changed during Job", "Longhorn driver owner changed during Job", "Longhorn provisioner runtime changed during Job", "Longhorn provisioner owner changed during Job", "Longhorn resizer runtime changed during Job", "Longhorn resizer owner changed during Job", "Longhorn snapshotter runtime changed during Job", "Longhorn snapshotter owner changed during Job", "Longhorn csi-attacher startup changed during Job", "Longhorn csi-provisioner startup changed during Job", "Longhorn csi-resizer startup changed during Job", "Longhorn csi-snapshotter startup changed during Job", "Longhorn attacher runtime changed during Job", "Longhorn attacher owner changed during Job", "Longhorn UI runtime changed during Job", "Longhorn UI owner changed during Job", "Longhorn UI changed during Job", "Longhorn manager setting changed during Job") && secretReads.Load() != 2 {
				t.Fatal("storage drift did not bracket complete source acquisition")
			}
			if f.c.store.db.Stats().InUse != 0 || f.events(t) != before {
				t.Fatal("broker retained DB connection or published event")
			}
		})
	}
}
