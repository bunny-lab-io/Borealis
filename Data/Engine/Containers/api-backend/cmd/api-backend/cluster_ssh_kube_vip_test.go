package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"errors"
	"strings"
	"testing"
)

func sshKubeVIPPin() clusterbootstrap.ExternalImagePin {
	for _, pin := range clusterbootstrap.ExternalImagePins() {
		if strings.HasPrefix(pin.Reference, clusterSSHKubeVIPRepository+"@") {
			return pin
		}
	}
	panic("missing kube-vip fixture pin")
}

// Mirrors kube-vip.yaml.in. Dynamic VIP/interface come from observed source.
func sshKubeVIPFixture(source clusterSSHSourceCohort) map[string]any {
	object := sshStorageObject("apps/v1", "DaemonSet", "kube-system", "kube-vip-borealis-cluster")
	env := []any{}
	for name, value := range map[string]string{"vip_arp": "true", "port": "6443", "vip_interface": "ens18", "vip_subnet": "32", "address": source.ControlPlaneVIP, "cp_namespace": "kube-system", "vip_leaderelection": "true", "vip_leasename": "borealis-cluster-vip", "vip_leaseduration": "10", "vip_renewdeadline": "5", "vip_retryperiod": "2", "cp_enable": "true", "svc_enable": "false", "prometheus_server": ":2112"} {
		env = append(env, map[string]any{"name": name, "value": value})
	}
	env = append(env, map[string]any{"name": "vip_nodename", "valueFrom": map[string]any{"fieldRef": map[string]any{"fieldPath": "spec.nodeName"}}})
	object["spec"] = map[string]any{"template": map[string]any{"spec": map[string]any{
		"hostNetwork": true, "serviceAccountName": "kube-vip-borealis", "automountServiceAccountToken": true,
		"containers": []any{map[string]any{"name": "kube-vip", "image": sshKubeVIPPin().Reference, "args": []any{"manager"}, "env": env}},
	}}}
	return object
}
func sshKubeVIPSpec(f *sshStorageFixture) map[string]any {
	return clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(f.objects[clusterSSHKubeVIPPath], "spec"), "template"), "spec")
}
func sshKubeVIPContainer(f *sshStorageFixture) map[string]any {
	return sshKubeVIPSpec(f)["containers"].([]any)[0].(map[string]any)
}
func sshKubeVIPEnv(f *sshStorageFixture, name string) map[string]any {
	for _, raw := range sshKubeVIPContainer(f)["env"].([]any) {
		entry := raw.(map[string]any)
		if entry["name"] == name {
			return entry
		}
	}
	panic("missing fixture environment")
}

func TestClusterSSHKubeVIPSourceConfiguration(t *testing.T) {
	cases := map[string]func(*sshStorageFixture){
		"reviewed index": func(f *sshStorageFixture) {},
		"reviewed platform": func(f *sshStorageFixture) {
			sshKubeVIPContainer(f)["image"] = clusterSSHKubeVIPRepository + "@" + sshKubeVIPPin().ManifestDigest
		},
		"defaulted field API": func(f *sshStorageFixture) {
			clusterSSHStorageMap(clusterSSHStorageMap(sshKubeVIPEnv(f, "vip_nodename"), "valueFrom"), "fieldRef")["apiVersion"] = "v1"
		},
		"missing":    func(f *sshStorageFixture) { delete(f.objects, clusterSSHKubeVIPPath) },
		"wrong kind": func(f *sshStorageFixture) { f.objects[clusterSSHKubeVIPPath]["kind"] = "Deployment" },
		"wrong namespace": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.objects[clusterSSHKubeVIPPath], "metadata")["namespace"] = "other"
		},
		"wrong name": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.objects[clusterSSHKubeVIPPath], "metadata")["name"] = "other"
		},
		"missing UID": func(f *sshStorageFixture) {
			delete(clusterSSHStorageMap(f.objects[clusterSSHKubeVIPPath], "metadata"), "uid")
		},
		"missing revision": func(f *sshStorageFixture) {
			delete(clusterSSHStorageMap(f.objects[clusterSSHKubeVIPPath], "metadata"), "resourceVersion")
		},
		"deleting": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.objects[clusterSSHKubeVIPPath], "metadata")["deletionTimestamp"] = "2026-10-02T00:00:00Z"
		},
		"tag": func(f *sshStorageFixture) { sshKubeVIPContainer(f)["image"] = clusterSSHKubeVIPRepository + ":latest" },
		"unreviewed image": func(f *sshStorageFixture) {
			sshKubeVIPContainer(f)["image"] = clusterSSHKubeVIPRepository + "@sha256:" + strings.Repeat("a", 64)
		},
		"wrong role": func(f *sshStorageFixture) { sshKubeVIPContainer(f)["image"] = sshSnapshotControllerPin().Reference },
		"sidecar": func(f *sshStorageFixture) {
			sshKubeVIPSpec(f)["containers"] = []any{sshKubeVIPContainer(f), sshKubeVIPContainer(f)}
		},
		"init":              func(f *sshStorageFixture) { sshKubeVIPSpec(f)["initContainers"] = []any{sshKubeVIPContainer(f)} },
		"ephemeral":         func(f *sshStorageFixture) { sshKubeVIPSpec(f)["ephemeralContainers"] = []any{sshKubeVIPContainer(f)} },
		"host network":      func(f *sshStorageFixture) { sshKubeVIPSpec(f)["hostNetwork"] = false },
		"service account":   func(f *sshStorageFixture) { sshKubeVIPSpec(f)["serviceAccountName"] = "other" },
		"token":             func(f *sshStorageFixture) { sshKubeVIPSpec(f)["automountServiceAccountToken"] = false },
		"wrong container":   func(f *sshStorageFixture) { sshKubeVIPContainer(f)["name"] = "other" },
		"command":           func(f *sshStorageFixture) { sshKubeVIPContainer(f)["command"] = []any{"other"} },
		"args":              func(f *sshStorageFixture) { sshKubeVIPContainer(f)["args"] = []any{"manager", "--services"} },
		"arg expansion":     func(f *sshStorageFixture) { sshKubeVIPContainer(f)["args"] = []any{"$(COMMAND)"} },
		"args type":         func(f *sshStorageFixture) { sshKubeVIPContainer(f)["args"] = []any{map[string]any{}} },
		"working directory": func(f *sshStorageFixture) { sshKubeVIPContainer(f)["workingDir"] = "/other" },
		"lifecycle": func(f *sshStorageFixture) {
			sshKubeVIPContainer(f)["lifecycle"] = map[string]any{"postStart": map[string]any{}}
		},
		"restartable": func(f *sshStorageFixture) { sshKubeVIPContainer(f)["restartPolicy"] = "Always" },
		"volume":      func(f *sshStorageFixture) { sshKubeVIPSpec(f)["volumes"] = []any{map[string]any{"name": "other"}} },
		"mount": func(f *sshStorageFixture) {
			sshKubeVIPContainer(f)["volumeMounts"] = []any{map[string]any{"name": "other"}}
		},
		"device": func(f *sshStorageFixture) {
			sshKubeVIPContainer(f)["volumeDevices"] = []any{map[string]any{"name": "other"}}
		},
		"envFrom": func(f *sshStorageFixture) {
			sshKubeVIPContainer(f)["envFrom"] = []any{map[string]any{"configMapRef": map[string]any{"name": "other"}}}
		},
		"missing env": func(f *sshStorageFixture) { delete(sshKubeVIPContainer(f), "env") },
		"extra env": func(f *sshStorageFixture) {
			c := sshKubeVIPContainer(f)
			c["env"] = append(c["env"].([]any), map[string]any{"name": "extra", "value": "true"})
		},
		"duplicate env":       func(f *sshStorageFixture) { c := sshKubeVIPContainer(f); e := c["env"].([]any); e[0] = e[1] },
		"wrong VIP":           func(f *sshStorageFixture) { sshKubeVIPEnv(f, "address")["value"] = "192.168.90.99" },
		"edge VIP mismatch":   func(f *sshStorageFixture) { f.a.Source.EdgeVIP = "192.168.90.99" },
		"interface expansion": func(f *sshStorageFixture) { sshKubeVIPEnv(f, "vip_interface")["value"] = "$(INTERFACE)" },
		"interface too long":  func(f *sshStorageFixture) { sshKubeVIPEnv(f, "vip_interface")["value"] = strings.Repeat("a", 16) },
		"interface empty":     func(f *sshStorageFixture) { sshKubeVIPEnv(f, "vip_interface")["value"] = "" },
		"indirect interface": func(f *sshStorageFixture) {
			e := sshKubeVIPEnv(f, "vip_interface")
			delete(e, "value")
			e["valueFrom"] = map[string]any{"fieldRef": map[string]any{"fieldPath": "metadata.name"}}
		},
		"literal node": func(f *sshStorageFixture) {
			e := sshKubeVIPEnv(f, "vip_nodename")
			delete(e, "valueFrom")
			e["value"] = "node"
		},
		"wrong downward field": func(f *sshStorageFixture) {
			clusterSSHStorageMap(clusterSSHStorageMap(sshKubeVIPEnv(f, "vip_nodename"), "valueFrom"), "fieldRef")["fieldPath"] = "metadata.name"
		},
	}
	for _, name := range []string{"vip_arp", "port", "vip_subnet", "cp_namespace", "vip_leaderelection", "vip_leasename", "vip_leaseduration", "vip_renewdeadline", "vip_retryperiod", "cp_enable", "svc_enable", "prometheus_server"} {
		cases["override "+name] = func(f *sshStorageFixture) { sshKubeVIPEnv(f, name)["value"] = "other" }
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			mutate(f)
			v, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			good := name == "reviewed index" || name == "reviewed platform" || name == "defaulted field API"
			if good {
				if err != nil || v.Requirements.KubeVIP.Image != sshKubeVIPContainer(f)["image"] {
					t.Fatalf("reviewed input rejected: %v", err)
				}
			} else if err != clusterbootstrap.ErrPreparationConfig || v.Requirements.observation != "" {
				t.Fatalf("unproved input escaped: %v", err)
			}
		})
	}
}

func TestClusterSSHKubeVIPDeniedRead(t *testing.T) {
	f := newSSHStorageFixture(t, false)
	get := func(ctx context.Context, path string, out any) error {
		if path == clusterSSHKubeVIPPath {
			return errors.New("private denied read")
		}
		return f.get(ctx, path, out)
	}
	consumed := false
	err := withClusterSSHSourceStorage(context.Background(), f.authority, get, func(context.Context, clusterSSHStorageRequirements, clusterSSHPreparationChecks) error {
		consumed = true
		return nil
	})
	if err != clusterbootstrap.ErrSessionAuthority || consumed {
		t.Fatalf("denied input consumed: %v", err)
	}
}
