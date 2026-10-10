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
)

// Executable inputs from checksum-pinned CNPG1.30 manifest; no runtime access.
func sshCNPGOperatorFixture() map[string]any {
	object := sshStorageObject("apps/v1", "Deployment", "cnpg-system", "cnpg-controller-manager")
	var spec map[string]any
	if err := json.Unmarshal([]byte(`{"containers":[{"args":["controller","--leader-elect","--max-concurrent-reconciles=10","--config-map-name=cnpg-controller-manager-config","--secret-name=cnpg-controller-manager-config","--webhook-port=9443"],"command":["/manager"],"env":[{"name":"OPERATOR_IMAGE_NAME","value":"ghcr.io/cloudnative-pg/cloudnative-pg:1.30.0"},{"name":"OPERATOR_NAMESPACE","valueFrom":{"fieldRef":{"fieldPath":"metadata.namespace"}}},{"name":"MONITORING_QUERIES_CONFIGMAP","value":"cnpg-default-monitoring"}],"image":"ghcr.io/cloudnative-pg/cloudnative-pg:1.30.0","imagePullPolicy":"Always","livenessProbe":{"httpGet":{"path":"/readyz","port":9443,"scheme":"HTTPS"}},"name":"manager","ports":[{"containerPort":8080,"name":"metrics","protocol":"TCP"},{"containerPort":9443,"name":"webhook-server","protocol":"TCP"}],"readinessProbe":{"httpGet":{"path":"/readyz","port":9443,"scheme":"HTTPS"}},"resources":{"limits":{"cpu":"100m","memory":"200Mi"},"requests":{"cpu":"100m","memory":"100Mi"}},"securityContext":{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]},"readOnlyRootFilesystem":true,"runAsGroup":10001,"runAsUser":10001,"seccompProfile":{"type":"RuntimeDefault"}},"startupProbe":{"failureThreshold":6,"httpGet":{"path":"/readyz","port":9443,"scheme":"HTTPS"},"periodSeconds":5},"volumeMounts":[{"mountPath":"/controller","name":"scratch-data"},{"mountPath":"/run/secrets/cnpg.io/webhook","name":"webhook-certificates"}]}],"securityContext":{"runAsNonRoot":true,"seccompProfile":{"type":"RuntimeDefault"}},"serviceAccountName":"cnpg-manager","terminationGracePeriodSeconds":10,"volumes":[{"emptyDir":{},"name":"scratch-data"},{"name":"webhook-certificates","secret":{"defaultMode":420,"optional":true,"secretName":"cnpg-webhook-cert"}}]}`), &spec); err != nil {
		panic(err)
	}
	object["spec"] = map[string]any{"template": map[string]any{"metadata": map[string]any{"labels": map[string]any{"app.kubernetes.io/name": "cloudnative-pg"}}, "spec": spec}}
	return object
}
func sshCNPGOperatorSpec(f *sshStorageFixture) map[string]any {
	return clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(f.objects[clusterSSHCNPGOperatorPath], "spec"), "template"), "spec")
}
func sshCNPGOperatorContainer(f *sshStorageFixture) map[string]any {
	return sshCNPGOperatorSpec(f)["containers"].([]any)[0].(map[string]any)
}

func TestClusterSSHCNPGOperatorConfiguration(t *testing.T) {
	for _, mode := range []string{"reviewed tag", "reviewed index", "absent config", "split flags", "explicit boolean", "reordered env", "wrong name", "wrong namespace", "wrong kind", "missing UID", "missing revision", "deleting", "missing deployment", "wrong container", "sidecar", "init", "ephemeral", "image override", "wrong role", "bootstrap mismatch", "command", "unknown flag", "missing flag", "duplicate flag", "disabled election", "split boolean", "config name", "secret name", "namespace env", "duplicate env", "extra env", "indirect env", "envFrom", "lifecycle", "working directory", "device", "restart policy", "extra mount", "subpath", "readonly scratch", "extra volume", "memory scratch", "TLS source", "TLS items", "TLS mode", "config override", "config malformed", "config binary", "secret override", "secret malformed", "secret stringData", "secret type", "config identity", "secret identity"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			spec, c := sshCNPGOperatorSpec(f), sshCNPGOperatorContainer(f)
			meta := clusterSSHStorageMap(f.objects[clusterSSHCNPGOperatorPath], "metadata")
			args := c["args"].([]any)
			env := c["env"].([]any)
			mounts := c["volumeMounts"].([]any)
			volumes := spec["volumes"].([]any)
			valid := false
			switch mode {
			case "reviewed tag", "absent config":
				valid = true
			case "reviewed index":
				valid = true
				c["image"] = sshPostgresBootstrapFixture().Resolved
				env[0].(map[string]any)["value"] = c["image"]
			case "split flags":
				valid = true
				c["args"] = []any{"controller", "--leader-elect", "--max-concurrent-reconciles", "10", "--config-map-name", "cnpg-controller-manager-config", "--secret-name", "cnpg-controller-manager-config", "--webhook-port", "9443"}
			case "explicit boolean":
				valid = true
				args[1] = "--leader-elect=true"
			case "reordered env":
				valid = true
				env[0], env[2] = env[2], env[0]
			case "wrong name":
				meta["name"] = "other"
			case "wrong namespace":
				meta["namespace"] = "other"
			case "wrong kind":
				f.objects[clusterSSHCNPGOperatorPath]["kind"] = "DaemonSet"
			case "missing UID":
				delete(meta, "uid")
			case "missing revision":
				delete(meta, "resourceVersion")
			case "deleting":
				meta["deletionTimestamp"] = "2026-10-01T00:00:00Z"
			case "missing deployment":
				delete(f.objects, clusterSSHCNPGOperatorPath)
			case "wrong container":
				c["name"] = "other"
			case "sidecar":
				spec["containers"] = []any{c, c}
			case "init":
				spec["initContainers"] = []any{c}
			case "ephemeral":
				spec["ephemeralContainers"] = []any{c}
			case "image override":
				c["image"] = clusterSSHCNPGRepository + ":other"
			case "wrong role":
				c["image"] = sshSnapshotControllerPin().Reference
			case "bootstrap mismatch":
				env[0].(map[string]any)["value"] = sshPostgresBootstrapFixture().Resolved
			case "command":
				c["command"] = []any{"/other"}
			case "unknown flag":
				args[2] = "--pprof-server=true"
			case "missing flag":
				c["args"] = args[:5]
			case "duplicate flag":
				args[5] = args[2]
			case "disabled election":
				args[1] = "--leader-elect=false"
			case "split boolean":
				c["args"] = append(args[:2], append([]any{"true"}, args[2:]...)...)
			case "config name":
				args[3] = "--config-map-name=other"
			case "secret name":
				args[4] = "--secret-name=other"
			case "namespace env":
				env[1] = map[string]any{"name": "OPERATOR_NAMESPACE", "value": "cnpg-system"}
			case "duplicate env":
				env[2] = env[0]
			case "extra env":
				c["env"] = append(env, map[string]any{"name": "POSTGRES_IMAGE_NAME", "value": "other"})
			case "indirect env":
				env[0] = map[string]any{"name": "OPERATOR_IMAGE_NAME", "valueFrom": map[string]any{"configMapKeyRef": map[string]any{"name": "other", "key": "image"}}}
			case "envFrom":
				c["envFrom"] = []any{map[string]any{"secretRef": map[string]any{"name": "other"}}}
			case "lifecycle":
				c["lifecycle"] = map[string]any{"postStart": map[string]any{}}
			case "working directory":
				c["workingDir"] = "/other"
			case "device":
				c["volumeDevices"] = []any{map[string]any{}}
			case "restart policy":
				c["restartPolicy"] = "Always"
			case "extra mount":
				c["volumeMounts"] = append(mounts, mounts[0])
			case "subpath":
				mounts[0].(map[string]any)["subPath"] = "other"
			case "readonly scratch":
				mounts[0].(map[string]any)["readOnly"] = true
			case "extra volume":
				spec["volumes"] = append(volumes, volumes[0])
			case "memory scratch":
				volumes[0].(map[string]any)["emptyDir"] = map[string]any{"medium": "Memory"}
			case "TLS source":
				clusterSSHStorageMap(volumes[1].(map[string]any), "secret")["secretName"] = "other"
			case "TLS items":
				clusterSSHStorageMap(volumes[1].(map[string]any), "secret")["items"] = []any{}
			case "TLS mode":
				clusterSSHStorageMap(volumes[1].(map[string]any), "secret")["defaultMode"] = 511
			case "config override":
				f.objects[clusterSSHCNPGConfigMapPath]["data"] = map[string]any{"OPERATOR_IMAGE_NAME": "unreviewed"}
			case "config malformed":
				f.objects[clusterSSHCNPGConfigMapPath]["data"] = []any{}
			case "config binary":
				f.objects[clusterSSHCNPGConfigMapPath]["binaryData"] = map[string]any{"x": "eA=="}
			case "secret override":
				f.objects[clusterSSHCNPGSecretPath]["data"] = map[string]any{"OPERATOR_IMAGE_NAME": "cHJpdmF0ZQ=="}
			case "secret malformed":
				f.objects[clusterSSHCNPGSecretPath]["data"] = "private"
			case "secret stringData":
				f.objects[clusterSSHCNPGSecretPath]["stringData"] = map[string]any{"private": "private"}
			case "secret type":
				f.objects[clusterSSHCNPGSecretPath]["type"] = "kubernetes.io/tls"
			case "config identity":
				clusterSSHStorageMap(f.objects[clusterSSHCNPGConfigMapPath], "metadata")["namespace"] = "other"
			case "secret identity":
				clusterSSHStorageMap(f.objects[clusterSSHCNPGSecretPath], "metadata")["name"] = "other"
			}
			get := func(ctx context.Context, path string, out any) error {
				if mode == "absent config" && clusterSSHCNPGOptionalConfigPath(path) {
					return errClusterSSHCNPGConfigAbsent
				}
				return f.get(ctx, path, out)
			}
			if valid {
				sshCNPGRuntimeFixture(f)
			}
			got, err := observeClusterSSHStorage(context.Background(), f.a.Source, get)
			if valid {
				if err != nil || got.Requirements.CNPGOperatorImage != c["image"] {
					t.Fatalf("reviewed inputs rejected: %v", err)
				}
			} else if err != clusterbootstrap.ErrPreparationConfig || got.Requirements.observation != "" {
				t.Fatalf("unproved inputs consumed: %v", err)
			}
		})
	}
}

func TestClusterSSHCNPGDeniedReads(t *testing.T) {
	for _, path := range []string{clusterSSHCNPGOperatorPath, clusterSSHCNPGConfigMapPath, clusterSSHCNPGSecretPath} {
		t.Run(path, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			get := func(ctx context.Context, p string, out any) error {
				if p == path {
					return errors.New("private denied response")
				}
				return f.get(ctx, p, out)
			}
			entered := false
			err := withClusterSSHSourceStorage(context.Background(), f.authority, get, func(context.Context, clusterSSHStorageRequirements, clusterSSHPreparationChecks) error {
				entered = true
				return nil
			})
			if err != clusterbootstrap.ErrSessionAuthority || entered {
				t.Fatalf("denied read reached consumer: %v", err)
			}
		})
	}
}

func TestClusterSSHCNPGOptionalConfigTransport(t *testing.T) {
	for _, path := range []string{clusterSSHCNPGConfigMapPath, clusterSSHCNPGSecretPath} {
		for _, mode := range []string{"absent", "denied", "server error", "redirect", "wrong name", "wrong kind", "wrong group", "wrong reason", "wrong status", "wrong code", "duplicate", "trailing", "oversize", "proxy", "200 status", "200 null", "required deployment"} {
			t.Run(path+mode, func(t *testing.T) {
				kind := "configmaps"
				if path == clusterSSHCNPGSecretPath {
					kind = "secrets"
				}
				status := map[string]any{"apiVersion": "v1", "kind": "Status", "status": "Failure", "reason": "NotFound", "code": 404, "details": map[string]any{"kind": kind, "name": "cnpg-controller-manager-config"}}
				code := 404
				endpoint := path
				details := status["details"].(map[string]any)
				switch mode {
				case "denied":
					code = 403
				case "server error":
					code = 500
				case "redirect":
					code = 302
				case "wrong name":
					details["name"] = "other"
				case "wrong kind":
					details["kind"] = "deployments"
				case "wrong group":
					details["group"] = "other"
				case "wrong reason":
					status["reason"] = "Forbidden"
				case "wrong status":
					status["status"] = "Success"
				case "wrong code":
					status["code"] = 403
				case "200 status", "200 null":
					code = 200
				case "required deployment":
					endpoint = clusterSSHCNPGOperatorPath
				}
				raw, _ := json.Marshal(status)
				switch mode {
				case "duplicate":
					raw = []byte(strings.Replace(string(raw), `"code":404`, `"code":404,"code":404`, 1))
				case "trailing":
					raw = append(raw, []byte(` {}`)...)
				case "oversize":
					raw = []byte(strings.Repeat(" ", 4097))
				case "proxy":
					raw = []byte("not found")
				case "200 null":
					raw = []byte("null")
				}
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "GET" || r.URL.String() != endpoint || r.Header.Get("Authorization") != "Bearer private-fixture" {
						t.Error("unexpected transport")
					}
					w.WriteHeader(code)
					_, _ = w.Write(raw)
				}))
				defer server.Close()
				client := &kubernetesAPIClient{baseURL: server.URL, httpClient: server.Client(), token: "private-fixture"}
				var out json.RawMessage
				err := client.getClusterSSHStorageJSON(context.Background(), endpoint, &out)
				if mode == "absent" {
					if err != errClusterSSHCNPGConfigAbsent {
						t.Fatalf("authenticated absence rejected: %v", err)
					}
				} else if err == errClusterSSHCNPGConfigAbsent {
					t.Fatal("unproved response became absence")
				}
				if mode == "200 status" || mode == "200 null" {
					f := newSSHStorageFixture(t, false)
					get := func(ctx context.Context, p string, out any) error {
						if p == path {
							return client.getClusterSSHStorageJSON(ctx, p, out)
						}
						return f.get(ctx, p, out)
					}
					if _, err := observeClusterSSHStorage(context.Background(), f.a.Source, get); err == nil {
						t.Fatal("200 non-object accepted as absent")
					}
				} else if mode != "absent" && err != clusterbootstrap.ErrPreparationConfig {
					t.Fatalf("bad response accepted: %v", err)
				}
			})
		}
	}
}

func TestClusterSSHCNPGOptionalConfigPresenceDrift(t *testing.T) {
	for _, path := range []string{clusterSSHCNPGConfigMapPath, clusterSSHCNPGSecretPath} {
		for _, initial := range []bool{false, true} {
			t.Run(path+map[bool]string{false: "created", true: "removed"}[initial], func(t *testing.T) {
				f := newSSHStorageFixture(t, false)
				var present atomic.Bool
				present.Store(initial)
				get := func(ctx context.Context, p string, out any) error {
					if p == path && !present.Load() {
						return errClusterSSHCNPGConfigAbsent
					}
					return f.get(ctx, p, out)
				}
				consumed := false
				err := withClusterSSHSourceStorage(context.Background(), f.authority, get, func(context.Context, clusterSSHStorageRequirements, clusterSSHPreparationChecks) error {
					consumed = true
					present.Store(!initial)
					return nil
				})
				if err != clusterbootstrap.ErrSessionAuthority || !consumed {
					t.Fatalf("presence drift escaped: %v", err)
				}
			})
		}
	}
}

func TestClusterSSHCNPGAbsentScopeAuthority(t *testing.T) {
	for _, lost := range []bool{false, true} {
		f := newSSHStorageFixture(t, false)
		get := func(ctx context.Context, path string, out any) error {
			if clusterSSHCNPGOptionalConfigPath(path) {
				if lost {
					f.mu.Lock()
					f.a.Lease.Generation++
					f.mu.Unlock()
				}
				return errClusterSSHCNPGConfigAbsent
			}
			return f.get(ctx, path, out)
		}
		entered := false
		err := withClusterSSHSourceStorage(context.Background(), f.authority, get, func(context.Context, clusterSSHStorageRequirements, clusterSSHPreparationChecks) error {
			entered = true
			return nil
		})
		if lost {
			if err != clusterbootstrap.ErrSessionAuthority || entered {
				t.Fatal("absence bypassed authority", err)
			}
		} else if err != nil || !entered {
			t.Fatal("stable proven absence rejected", err)
		}
	}
}
