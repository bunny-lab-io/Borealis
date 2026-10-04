package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClusterSSHStorageScope(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "initial revision drift", "final revision drift", "final content drift", "CNPG receipt drift", "CNPG config receipt drift", "CNPG secret receipt drift", "CNPG config override", "kube-vip receipt drift", "kube-vip image drift", "kube-vip interface drift", "snapshot receipt drift", "snapshot image drift", "upgrade receipt drift", "upgrade kubectl drift", "UI receipt drift", "UI image drift", "manager receipt drift", "manager Settings drift", "Longhorn receipt drift", "Longhorn CSI drift", "cert-manager receipt drift", "cert-manager solver drift", "source boot drift", "source authority drift", "target authority drift", "lost authority during GET", "ignored input failure", "ignored cancelled authority", "consumer failure"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, mode == "replacement")
			var lost atomic.Bool
			authority := func(ctx context.Context) (clusterSSHPreparationAuthority, error) {
				if lost.Load() {
					return clusterSSHPreparationAuthority{}, errors.New("private authority")
				}
				return f.authority(ctx)
			}
			rounds := 0
			get := func(ctx context.Context, path string, out any) error {
				if path == clusterSSHStoragePostgresPath {
					rounds++
					if (mode == "initial revision drift" && rounds == 2) || (mode == "final revision drift" && rounds == 3) {
						f.mu.Lock()
						clusterSSHStorageMap(f.volume(0), "metadata")["resourceVersion"] = "2"
						f.mu.Unlock()
					}
				}
				err := f.get(ctx, path, out)
				if mode == "lost authority during GET" {
					lost.Store(true)
				}
				return err
			}
			consumed := false
			var saved clusterSSHPreparationChecks
			err := withClusterSSHSourceStorage(context.Background(), authority, get, func(ctx context.Context, value clusterSSHStorageRequirements, checks clusterSSHPreparationChecks) error {
				consumed = true
				saved = checks
				if rounds != 2 || len(value.Volumes) < 3 {
					t.Fatal("consumer reached without two complete observations")
				}
				// Caller aliases cannot rewrite retained comparison receipts or values.
				value.Volumes[0].Bytes = 1
				f.mu.Lock()
				switch mode {
				case "final content drift":
					clusterSSHStorageMap(f.volume(0), "spec")["numberOfReplicas"] = 3
				case "CNPG receipt drift":
					clusterSSHStorageMap(f.objects[clusterSSHCNPGOperatorPath], "metadata")["resourceVersion"] = "2"
				case "CNPG config receipt drift":
					clusterSSHStorageMap(f.objects[clusterSSHCNPGConfigMapPath], "metadata")["resourceVersion"] = "2"
				case "CNPG secret receipt drift":
					clusterSSHStorageMap(f.objects[clusterSSHCNPGSecretPath], "metadata")["resourceVersion"] = "2"
				case "CNPG config override":
					f.objects[clusterSSHCNPGConfigMapPath]["data"] = map[string]any{"OPERATOR_IMAGE_NAME": "other"}
				case "kube-vip receipt drift":
					clusterSSHStorageMap(f.objects[clusterSSHKubeVIPPath], "metadata")["resourceVersion"] = "2"
				case "kube-vip image drift":
					sshKubeVIPContainer(f)["image"] = clusterSSHKubeVIPRepository + "@" + sshKubeVIPPin().ManifestDigest
				case "kube-vip interface drift":
					sshKubeVIPEnv(f, "vip_interface")["value"] = "ens19"
				case "snapshot receipt drift":
					clusterSSHStorageMap(f.objects[clusterSSHSnapshotControllerPath], "metadata")["annotations"] = map[string]any{"changed": "original receipt"}
				case "snapshot image drift":
					sshSnapshotControllerContainer(f)["image"] = clusterSSHSnapshotControllerRepository + "@" + sshSnapshotControllerPin().ManifestDigest
				case "upgrade receipt drift":
					clusterSSHStorageMap(f.objects[clusterSSHSystemUpgradePath], "metadata")["annotations"] = map[string]any{"changed": "frozen original receipt"}
				case "upgrade kubectl drift":
					clusterSSHStorageMap(f.objects[clusterSSHSystemUpgradeConfigPath], "data")["SYSTEM_UPGRADE_JOB_KUBECTL_IMAGE"] = "docker.io/rancher/kubectl@" + sshSystemUpgradePin("kubectl").ManifestDigest
				case "UI receipt drift":
					clusterSSHStorageMap(f.objects[clusterSSHLonghornUIPath], "metadata")["annotations"] = map[string]any{"changed": "frozen original receipt"}
				case "UI image drift":
					sshLonghornUIContainer(f)["image"] = "docker.io/longhornio/longhorn-ui@" + sshLonghornDriverPin("longhorn-ui").ManifestDigest
				case "manager receipt drift":
					clusterSSHStorageMap(f.objects[clusterSSHLonghornManagerPath], "metadata")["annotations"] = map[string]any{"changed": "frozen original receipt"}
				case "manager Settings drift":
					sshLonghornManagerSetImage(f, "support-bundle-kit", "docker.io/longhornio/support-bundle-kit@"+sshLonghornDriverPin("support-bundle-kit").ManifestDigest)
				case "Longhorn receipt drift":
					clusterSSHStorageMap(f.objects[clusterSSHLonghornDriverPath], "metadata")["annotations"] = map[string]any{"changed": "original receipt must stay frozen"}
				case "Longhorn CSI drift":
					sshLonghornDriverContainer(f)["env"].([]any)[3].(map[string]any)["value"] = "docker.io/longhornio/csi-attacher@" + sshLonghornDriverPin("csi-attacher").ManifestDigest
				case "cert-manager receipt drift":
					clusterSSHStorageMap(f.objects[clusterSSHCertManagerDeploymentPrefix+"cert-manager-webhook"], "metadata")["resourceVersion"] = "2"
				case "cert-manager solver drift":
					// Even another reviewed reference cannot replace the original observation.
					sshCertManagerContainer(f, "cert-manager")["args"].([]any)[3] = "--acme-http01-solver-image=quay.io/jetstack/cert-manager-acmesolver@" + sshCertManagerPin("acmesolver").ManifestDigest
				case "source boot drift":
					clusterSSHStorageMap(clusterSSHStorageMap(f.objects["/api/v1/nodes"]["items"].([]any)[0].(map[string]any), "status"), "nodeInfo")["bootID"] = newClusterUUID()
				case "source authority drift":
					f.a.Source.Members[0].NodeUID = newClusterUUID()
				case "target authority drift":
					f.a.Cohort.Targets[0].Key.PublicKey[0] ^= 1
				case "ignored input failure":
					clusterSSHStorageMap(f.volume(0), "metadata")["resourceVersion"] = "2"
				}
				f.mu.Unlock()
				if mode == "ignored input failure" {
					if checks.Inputs(context.Background()) == nil {
						t.Fatal("changed input accepted")
					}
					f.mu.Lock()
					clusterSSHStorageMap(f.volume(0), "metadata")["resourceVersion"] = "1"
					f.mu.Unlock()
				}
				if mode == "ignored cancelled authority" {
					cancelled, cancel := context.WithCancel(context.Background())
					cancel()
					if checks.Authority(cancelled) == nil {
						t.Fatal("cancelled check accepted")
					}
				}
				if mode == "consumer failure" {
					return errors.New("private consumer")
				}
				return nil
			})
			if mode == "expansion" || mode == "replacement" {
				if err != nil || !consumed || rounds != 3 {
					t.Fatalf("scope failed %v rounds %d", err, rounds)
				}
			} else if err == nil {
				t.Fatal("scope accepted drift/failure")
			}
			if mode == "initial revision drift" || mode == "lost authority during GET" {
				if consumed {
					t.Fatal("invalid initial inputs consumed")
				}
			}
			if saved.Inputs != nil {
				before := rounds
				if saved.Inputs(context.Background()) == nil || saved.Authority(context.Background()) == nil || rounds != before {
					t.Fatal("closed scope allowed work")
				}
			}
		})
	}
}

func TestClusterSSHStorageScopeJoinsCancellationAndEscapedChecks(t *testing.T) {
	for _, mode := range []string{"parent cancellation", "escaped read", "escaped authority", "heartbeat loss"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			type readerCallKey struct{}
			var consuming, lost atomic.Bool
			entered, joined, callDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			get := func(ctx context.Context, path string, out any) error {
				if consuming.Load() && mode != "heartbeat loss" && mode != "escaped authority" {
					close(entered)
					<-ctx.Done()
					close(joined)
					return ctx.Err()
				}
				return f.get(ctx, path, out)
			}
			authority := func(ctx context.Context) (clusterSSHPreparationAuthority, error) {
				if mode == "heartbeat loss" && lost.Load() {
					return clusterSSHPreparationAuthority{}, errors.New("private ownership")
				}
				if mode == "escaped authority" && ctx.Value(readerCallKey{}) == true {
					close(entered)
					<-ctx.Done()
					close(joined)
					return clusterSSHPreparationAuthority{}, ctx.Err()
				}
				return f.authority(ctx)
			}
			started := time.Now()
			err := withClusterSSHSourceStorage(ctx, authority, get, func(scope context.Context, _ clusterSSHStorageRequirements, checks clusterSSHPreparationChecks) error {
				consuming.Store(true)
				if mode == "heartbeat loss" {
					lost.Store(true)
					<-scope.Done()
					return nil
				}
				go func() {
					if mode == "escaped authority" {
						callDone <- checks.Authority(context.WithValue(context.Background(), readerCallKey{}, true))
					} else {
						callDone <- checks.Inputs(context.Background())
					}
				}()
				select {
				case <-entered:
				case <-ctx.Done():
					t.Fatal("read not entered")
				}
				if mode == "parent cancellation" {
					cancel()
				}
				// Scope, not consumer, must cancel/join this deliberately escaped call.
				return nil
			})
			if err == nil || time.Since(started) > 4*time.Second {
				t.Fatal("scope failed to stop promptly")
			}
			if mode != "heartbeat loss" {
				select {
				case <-joined:
				default:
					t.Fatal("work outlived scope")
				}
				select {
				case err := <-callDone:
					if err == nil {
						t.Fatal("escaped call succeeded")
					}
				case <-time.After(time.Second):
					t.Fatal("check not joined")
				}
			}
		})
	}
}

func TestClusterSSHStorageScopeRejectsInvalidAuthority(t *testing.T) {
	for _, mode := range []string{"baseline", "lease operation", "lease holder", "lease attempt", "lease target", "lease generation", "cohort", "source"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			switch mode {
			case "baseline":
				f.a.Baseline.SourceSHA = "invalid"
			case "lease operation":
				f.a.Lease.OperationID = newClusterUUID()
			case "lease holder":
				f.a.Lease.ControllerHolder = "other"
			case "lease attempt":
				f.a.Lease.OperationAttempt++
			case "lease target":
				f.a.Lease.TargetID = newClusterUUID()
			case "lease generation":
				f.a.Lease.Generation = f.a.Cohort.Targets[0].Generation
			case "cohort":
				f.a.Cohort.Targets = f.a.Cohort.Targets[:1]
			case "source":
				f.a.Source.Members = nil
			}
			consumed := false
			err := withClusterSSHSourceStorage(context.Background(), f.authority, f.get, func(context.Context, clusterSSHStorageRequirements, clusterSSHPreparationChecks) error {
				consumed = true
				return nil
			})
			if err == nil || consumed || len(f.calls) != 0 {
				t.Fatal("invalid authority reached Kubernetes")
			}
		})
	}
}

func TestClusterSSHStorageControllerTLSBoundary(t *testing.T) {
	f := newSSHStorageFixture(t, true)
	var mode atomic.Value
	mode.Store("success")
	var requests, redirected atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer synthetic-private-token" {
			t.Error("wrong transport contract")
		}
		if r.URL.Path == "/leak" {
			redirected.Add(1)
		}
		switch mode.Load().(string) {
		case "redirect":
			http.Redirect(w, r, "/leak", http.StatusFound)
			return
		case "failure":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("private response"))
			return
		case "large":
			_, _ = w.Write([]byte(strings.Repeat("x", (2<<20)+1)))
			return
		}
		f.mu.Lock()
		object, ok := f.objects[r.URL.RequestURI()]
		f.mu.Unlock()
		if !ok {
			t.Error("unexpected read")
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(object)
	}))
	defer server.Close()
	kube := &kubernetesAPIClient{baseURL: server.URL, token: "synthetic-private-token", httpClient: server.Client()}
	runner := &kubernetesClusterStepRunner{kube: kube, namespace: "borealis"}
	consumed := false
	if err := runner.withSSHSourceStorage(context.Background(), f.authority, func(context.Context, clusterSSHStorageRequirements, clusterSSHPreparationChecks) error {
		consumed = true
		return nil
	}); err != nil || !consumed {
		t.Fatalf("controller TLS composition failed %v", err)
	}
	for _, bad := range []string{"redirect", "failure", "large"} {
		mode.Store(bad)
		var raw json.RawMessage
		if err := kube.getClusterSSHStorageJSON(context.Background(), clusterSSHStorageClaimsPath, &raw); err != clusterbootstrap.ErrPreparationConfig || raw != nil {
			t.Fatal("private failure escaped")
		}
	}
	if redirected.Load() != 0 {
		t.Fatal("redirect followed")
	}
	before := requests.Load()
	for _, path := range []string{clusterSSHLonghornDriverPodsPath("../other"), clusterSSHLonghornDriverPodsPath("node") + "&watch=true", clusterSSHLonghornDriverPodsPrefix + "node&limit=17", clusterSSHLonghornDriverReplicaSetPrefix + "longhorn-driver-deployer-x/status", clusterSSHLonghornDriverReplicaSetPrefix + "longhorn-driver-deployer-x?watch=true", clusterSSHLonghornUIPodsPath("../other"), clusterSSHLonghornUIPodsPath("node") + "&watch=true", clusterSSHLonghornUIPodsPrefix + "node&limit=17", clusterSSHLonghornUIReplicaSetPrefix, clusterSSHLonghornUIReplicaSetPrefix + "longhorn-ui-../other", clusterSSHLonghornUIReplicaSetPrefix + "longhorn-ui-x?watch=true", clusterSSHLonghornUIReplicaSetPrefix + "other", clusterSSHStorageReplicasPath("../other"), clusterSSHStorageReplicasPath("name%2Fother"), clusterSSHStorageReplicasPath("volume") + "&watch=true", clusterSSHStorageReplicasPrefix + "volume", clusterSSHStorageReplicasPrefix + "volume&limit=65", "/apis/longhorn.io/v1beta2/namespaces/longhorn-system/replicas", clusterSSHLonghornManagerPodsPath("../other"), clusterSSHLonghornManagerPodsPath("node%2Fother"), clusterSSHLonghornManagerPodsPath("node") + "&watch=true", clusterSSHLonghornManagerPodsPrefix + "node", "/api/v1/namespaces/longhorn-system/pods", clusterSSHLonghornManagerPodsPrefix + "node&limit=3", clusterSSHKubeVIPPodsPath("../other"), clusterSSHKubeVIPPodsPath("node%2Fother"), clusterSSHKubeVIPPodsPath("node") + "&watch=true", clusterSSHKubeVIPPodsPrefix + "node", "/api/v1/namespaces/kube-system/pods", clusterSSHKubeVIPPodsPrefix + "node&limit=3", clusterSSHKubeVIPPath + "/status", clusterSSHKubeVIPPath + "?watch=true", strings.Replace(clusterSSHKubeVIPPath, "kube-vip-borealis-cluster", "other", 1), "/apis/apps/v1/namespaces/kube-system/daemonsets", clusterSSHCNPGOperatorPath + "/status", clusterSSHCNPGConfigMapPath + "?watch=true", clusterSSHCNPGSecretPath + "/other", "/api/v1/namespaces/cnpg-system/secrets", clusterSSHSnapshotControllerPath + "/status", clusterSSHSnapshotControllerPath + "?watch=true", strings.Replace(clusterSSHSnapshotControllerPath, "snapshot-controller", "other", 1), clusterSSHSystemUpgradePath + "/status", clusterSSHSystemUpgradeConfigPath + "?watch=true", strings.Replace(clusterSSHSystemUpgradeConfigPath, "default-controller-env", "other", 1), "/api/v1/namespaces/system-upgrade/configmaps", clusterSSHLonghornUIPath + "/status", clusterSSHLonghornUIPath + "?watch=true", strings.Replace(clusterSSHLonghornUIPath, "longhorn-ui", "other", 1), clusterSSHLonghornManagerPath + "/status", clusterSSHLonghornManagerPath + "?watch=true", clusterSSHStorageSettingPrefix + "default-instance-manager-image", clusterSSHStorageSettingPrefix + "default-backing-image-manager-image", clusterSSHLonghornDriverPath + "?watch=true", clusterSSHLonghornDriverPath + "/status", strings.Replace(clusterSSHLonghornDriverPath, "longhorn-driver-deployer", "other", 1), clusterSSHCertManagerDeploymentPrefix, clusterSSHCertManagerDeploymentPrefix + "other", clusterSSHCertManagerDeploymentPrefix + "cert-manager?watch=true", clusterSSHCertManagerDeploymentPrefix + "../cert-manager", "/api/v1/secrets", clusterSSHRuntimeSecretPath, "/apis/storage.k8s.io/v1/storageclasses", clusterSSHStoragePVPrefix + "../secrets", clusterSSHStorageVolumePrefix + "volume?other=true", clusterSSHStoragePVPrefix + "%2fsecret", clusterSSHStoragePodsPath + "&limit=1", clusterSSHStoragePVPrefix + strings.Repeat("a", 64)} {
		var raw json.RawMessage
		if kube.getClusterSSHStorageJSON(context.Background(), path, &raw) == nil {
			t.Fatal("unscoped path accepted")
		}
	}
	if requests.Load() != before {
		t.Fatal("invalid path reached transport")
	}
	mode.Store("success")
	var raw json.RawMessage
	kube.baseURL = "http://127.0.0.1"
	if kube.getClusterSSHStorageJSON(context.Background(), clusterSSHStorageClaimsPath, &raw) == nil {
		t.Fatal("plaintext accepted")
	}
	kube.baseURL = server.URL
	kube.httpClient = &http.Client{}
	if kube.getClusterSSHStorageJSON(context.Background(), clusterSSHStorageClaimsPath, &raw) == nil {
		t.Fatal("untrusted TLS accepted")
	}
	kube.httpClient = server.Client()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if kube.getClusterSSHStorageJSON(ctx, clusterSSHStorageClaimsPath, &raw) == nil {
		t.Fatal("cancelled request accepted")
	}
	runner.namespace = "other"
	if runner.withSSHSourceStorage(context.Background(), f.authority, func(context.Context, clusterSSHStorageRequirements, clusterSSHPreparationChecks) error { return nil }) == nil {
		t.Fatal("foreign controller namespace accepted")
	}
}
