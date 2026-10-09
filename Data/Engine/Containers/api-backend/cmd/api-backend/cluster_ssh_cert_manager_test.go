package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"strings"
	"testing"
)

func sshCertManagerPin(role string) clusterbootstrap.ExternalImagePin {
	for _, pin := range clusterbootstrap.ExternalImagePins() {
		if strings.HasPrefix(pin.Reference, "quay.io/jetstack/cert-manager-"+role+":") {
			return pin
		}
	}
	panic("missing cert-manager fixture pin")
}

// Mirrors the reviewed cert-manager manifest, including repeated DNS flags.
func sshCertManagerDeploymentFixture(name string) map[string]any {
	role := strings.TrimPrefix(name, "cert-manager-")
	args := []any{"--v=2", "--leader-election-namespace=kube-system"}
	if name == "cert-manager" {
		role = "controller"
		args = append(args, "--cluster-resource-namespace=$(POD_NAMESPACE)", "--acme-http01-solver-image="+sshCertManagerPin("acmesolver").Reference, "--max-concurrent-challenges=60")
	}
	if role == "webhook" {
		args = []any{"--v=2", "--secure-port=10250", "--dynamic-serving-ca-secret-namespace=$(POD_NAMESPACE)", "--dynamic-serving-ca-secret-name=cert-manager-webhook-ca", "--dynamic-serving-dns-names=cert-manager-webhook", "--dynamic-serving-dns-names=cert-manager-webhook.$(POD_NAMESPACE)", "--dynamic-serving-dns-names=cert-manager-webhook.$(POD_NAMESPACE).svc"}
	}
	object := sshStorageObject("apps/v1", "Deployment", "cert-manager", name)
	object["spec"] = map[string]any{"template": map[string]any{"metadata": map[string]any{"labels": map[string]any{"app.kubernetes.io/name": func() string {
		if name == "cert-manager" {
			return name
		}
		return role
	}()}}, "spec": map[string]any{"serviceAccountName": name, "containers": []any{map[string]any{
		"name": "cert-manager-" + role, "image": sshCertManagerPin(role).Reference, "args": args,
		"env": []any{map[string]any{"name": "POD_NAMESPACE", "valueFrom": map[string]any{"fieldRef": map[string]any{"fieldPath": "metadata.namespace"}}}},
	}}}}}
	return object
}

func sshCertManagerSpec(f *sshStorageFixture, name string) map[string]any {
	return clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(f.objects[clusterSSHCertManagerDeploymentPrefix+name], "spec"), "template"), "spec")
}

func sshCertManagerContainer(f *sshStorageFixture, name string) map[string]any {
	return sshCertManagerSpec(f, name)["containers"].([]any)[0].(map[string]any)
}

func TestClusterSSHCertManagerSourceConfiguration(t *testing.T) {
	for _, mode := range []string{"reviewed tags", "index digests", "platform digests", "split solver", "API default fieldRef",
		"missing deployment", "wrong identity", "deleting", "missing UID", "wrong container", "sidecar", "init", "ephemeral", "command", "envFrom", "env", "mount", "devices",
		"missing args", "missing solver", "duplicate solver", "split duplicate solver", "foreign solver", "solver expansion", "unreviewed image", "swapped role", "config flag", "unknown image flag", "bare argument", "missing flag value", "malformed args"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			c := sshCertManagerContainer(f, "cert-manager")
			args := c["args"].([]any)
			valid := false
			switch mode {
			case "reviewed tags":
				valid = true
			case "index digests", "platform digests":
				valid = true
				for _, role := range []string{"controller", "cainjector", "webhook", "acmesolver"} {
					pin := sshCertManagerPin(role)
					digest := pin.IndexDigest
					if mode == "platform digests" {
						digest = pin.ManifestDigest
					}
					ref := "quay.io/jetstack/cert-manager-" + role + "@" + digest
					switch role {
					case "controller":
						c["image"] = ref
					case "acmesolver":
						args[3] = "--acme-http01-solver-image=" + ref
					default:
						sshCertManagerContainer(f, "cert-manager-"+role)["image"] = ref
					}
				}
			case "split solver":
				valid = true
				c["args"] = []any{"--acme-http01-solver-image", sshCertManagerPin("acmesolver").Reference}
			case "API default fieldRef":
				valid = true
				clusterSSHStorageMap(clusterSSHStorageMap(c["env"].([]any)[0].(map[string]any), "valueFrom"), "fieldRef")["apiVersion"] = "v1"
			case "missing deployment":
				delete(f.objects, clusterSSHCertManagerDeploymentPrefix+"cert-manager-webhook")
			case "wrong identity":
				clusterSSHStorageMap(f.objects[clusterSSHCertManagerDeploymentPrefix+"cert-manager"], "metadata")["name"] = "other"
			case "deleting":
				clusterSSHStorageMap(f.objects[clusterSSHCertManagerDeploymentPrefix+"cert-manager"], "metadata")["deletionTimestamp"] = "2026-09-30T00:00:00Z"
			case "missing UID":
				delete(clusterSSHStorageMap(f.objects[clusterSSHCertManagerDeploymentPrefix+"cert-manager"], "metadata"), "uid")
			case "wrong container":
				c["name"] = "other"
			case "sidecar":
				sshCertManagerSpec(f, "cert-manager")["containers"] = []any{c, c}
			case "init":
				sshCertManagerSpec(f, "cert-manager")["initContainers"] = []any{c}
			case "ephemeral":
				sshCertManagerSpec(f, "cert-manager")["ephemeralContainers"] = []any{c}
			case "command":
				c["command"] = []any{"/other"}
			case "envFrom":
				c["envFrom"] = []any{map[string]any{"configMapRef": map[string]any{"name": "other"}}}
			case "env":
				c["env"] = append(c["env"].([]any), map[string]any{"name": "SOLVER_IMAGE", "value": "other"})
			case "mount":
				c["volumeMounts"] = []any{map[string]any{"name": "config", "mountPath": "/config"}}
			case "devices":
				c["volumeDevices"] = []any{map[string]any{"name": "config", "devicePath": "/config"}}
			case "missing args":
				delete(c, "args")
			case "missing solver":
				c["args"] = []any{"--v=2"}
			case "duplicate solver":
				c["args"] = append(args, args[3])
			case "split duplicate solver":
				c["args"] = append(args, "--acme-http01-solver-image", sshCertManagerPin("acmesolver").Reference)
			case "foreign solver":
				args[3] = "--acme-http01-solver-image=example.invalid/solver:v1"
			case "solver expansion":
				args[3] = "--acme-http01-solver-image=$(SOLVER_IMAGE)"
			case "unreviewed image":
				c["image"] = "quay.io/jetstack/cert-manager-controller:other"
			case "swapped role":
				c["image"] = sshCertManagerPin("webhook").Reference
			case "config flag":
				c["args"] = append(args, "--config=/config/controller.yaml")
			case "unknown image flag":
				c["args"] = append(args, "--future-image=other")
			case "bare argument":
				c["args"] = append(args, "other")
			case "missing flag value":
				c["args"] = append(args, "--v")
			case "malformed args":
				c["args"] = []any{1}
			}
			if valid {
				sshCertManagerRuntimeFixture(f)
			}
			result, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			if valid {
				if err != nil || !result.Requirements.CertManagerImages.valid() {
					t.Fatalf("reviewed configuration rejected: %v", err)
				}
			} else if err != clusterbootstrap.ErrPreparationConfig || result.Requirements.observation != "" {
				t.Fatalf("unproved configuration escaped: %v", err)
			}
		})
	}
}
