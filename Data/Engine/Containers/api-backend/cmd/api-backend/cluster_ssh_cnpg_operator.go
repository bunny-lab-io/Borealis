package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"errors"
	"io"
	"strings"
)

const (
	clusterSSHCNPGOperatorPath  = "/apis/apps/v1/namespaces/cnpg-system/deployments/cnpg-controller-manager"
	clusterSSHCNPGConfigMapPath = "/api/v1/namespaces/cnpg-system/configmaps/cnpg-controller-manager-config"
	clusterSSHCNPGSecretPath    = "/api/v1/namespaces/cnpg-system/secrets/cnpg-controller-manager-config"
)

var errClusterSSHCNPGConfigAbsent = errors.New("optional CNPG configuration absent")

func clusterSSHCNPGOptionalConfigPath(path string) bool {
	return path == clusterSSHCNPGConfigMapPath || path == clusterSSHCNPGSecretPath
}

// Only an authenticated fixed-path GET's bounded Kubernetes 404 Status proves
// absence. Denied reads, proxy pages and malformed/mismatched statuses do not.
func clusterSSHCNPGNotFound(path string, body io.Reader) error {
	raw, err := io.ReadAll(io.LimitReader(body, 4097))
	if err != nil || len(raw) > 4096 || !clusterSSHCNPGOptionalConfigPath(path) {
		return clusterbootstrap.ErrPreparationConfig
	}
	object, _, err := clusterSSHStorageObject(raw)
	details := clusterSSHStorageMap(object, "details")
	code, ok := clusterSSHStorageInteger(object["code"])
	kind := "configmaps"
	if path == clusterSSHCNPGSecretPath {
		kind = "secrets"
	}
	if err != nil || !ok || code != 404 || object["apiVersion"] != "v1" || object["kind"] != "Status" || object["status"] != "Failure" || object["reason"] != "NotFound" || details["name"] != "cnpg-controller-manager-config" || details["kind"] != kind || !clusterSSHStorageEmptyText(details["group"]) {
		return clusterbootstrap.ErrPreparationConfig
	}
	return errClusterSSHCNPGConfigAbsent
}

func clusterSSHCNPGOperatorImageValid(image string, bootstrap clusterSSHSourceExternalImage) bool {
	return len(image) <= 256 && bootstrap.valid(clusterSSHCNPGRepository) &&
		(clusterSSHSourceExternalImage{Configured: image, Resolved: bootstrap.Resolved}).valid(clusterSSHCNPGRepository)
}

// Desired operator inputs remain tied to completed bootstrap identity. Running
// operator image/ownership is observed separately; TLS/loaded state is not proved.
func observeClusterSSHCNPGOperatorImage(read func(string) (map[string]any, error), bootstrap clusterSSHSourceExternalImage) (string, error) {
	fail := func() (string, error) { return "", clusterbootstrap.ErrPreparationConfig }
	object, err := read(clusterSSHCNPGOperatorPath)
	metadata, ok := clusterSSHStorageMetadata(object, "apps/v1", "Deployment", "cnpg-system")
	if err != nil || !ok || metadata.Name != "cnpg-controller-manager" {
		return fail()
	}
	image, err := clusterSSHCNPGOperatorTemplateImage(object, bootstrap)
	if err != nil {
		return fail()
	}
	for _, path := range []string{clusterSSHCNPGConfigMapPath, clusterSSHCNPGSecretPath} {
		config, err := read(path)
		if err != nil {
			return fail()
		}
		if config == nil {
			continue
		} // Transport-proven absent, never a failed read.
		kind := "ConfigMap"
		if path == clusterSSHCNPGSecretPath {
			kind = "Secret"
		}
		id, ok := clusterSSHStorageMetadata(config, "v1", kind, "cnpg-system")
		// Repository baseline supplies no overrides. Unknown configuration blocks;
		// never decode, export or log Secret contents, including rejected values.
		if !ok || id.Name != "cnpg-controller-manager-config" || !clusterSSHStorageEmptyMap(config["data"]) ||
			!clusterSSHStorageEmptyMap(config["binaryData"]) || !clusterSSHStorageEmptyMap(config["stringData"]) {
			return fail()
		}
		if kind == "Secret" && !(config["type"] == "Opaque" || clusterSSHStorageEmptyText(config["type"])) {
			return fail()
		}
	}
	return image, nil
}

func clusterSSHCNPGOperatorArguments(raw any) bool {
	args, ok := raw.([]any)
	if !ok || len(args) < 6 || len(args) > 10 || args[0] != "controller" {
		return false
	}
	wanted := map[string]string{"--leader-elect": "true", "--max-concurrent-reconciles": "10", "--config-map-name": "cnpg-controller-manager-config", "--secret-name": "cnpg-controller-manager-config", "--webhook-port": "9443"}
	for i := 1; i < len(args); i++ {
		arg, ok := args[i].(string)
		if !ok {
			return false
		}
		flag, value, equals := strings.Cut(arg, "=")
		if !equals {
			if flag == "--leader-elect" {
				value = "true"
			} else {
				i++
				if i >= len(args) {
					return false
				}
				value, ok = args[i].(string)
				if !ok {
					return false
				}
			}
		}
		expected, known := wanted[flag]
		if !known || value != expected {
			return false
		}
		delete(wanted, flag)
	}
	return len(wanted) == 0
}

func clusterSSHCNPGOperatorEnvironment(raw any, image string) bool {
	env, ok := raw.([]any)
	wanted := map[string]string{"OPERATOR_IMAGE_NAME": image, "MONITORING_QUERIES_CONFIGMAP": "cnpg-default-monitoring", "OPERATOR_NAMESPACE": ""}
	if !ok || len(env) != len(wanted) {
		return false
	}
	for _, rawEntry := range env {
		entry, ok := rawEntry.(map[string]any)
		name := clusterSSHStorageText(entry, "name")
		value, known := wanted[name]
		if !ok || !known || len(entry) != 2 {
			return false
		}
		if name == "OPERATOR_NAMESPACE" {
			from := clusterSSHStorageMap(entry, "valueFrom")
			field := clusterSSHStorageMap(from, "fieldRef")
			if len(from) != 1 || field["fieldPath"] != "metadata.namespace" || !(len(field) == 1 || len(field) == 2 && field["apiVersion"] == "v1") {
				return false
			}
		} else if entry["value"] != value {
			return false
		}
		delete(wanted, name)
	}
	return len(wanted) == 0
}

// Bound mount shape, not certificate contents or scratch growth. Those remain
// separate runtime/readiness obligations; no webhook credential is read here.
func clusterSSHCNPGOperatorMounts(rawMounts, rawVolumes any) bool {
	mounts, mOK := rawMounts.([]any)
	volumes, vOK := rawVolumes.([]any)
	wanted := map[string]string{"scratch-data": "/controller", "webhook-certificates": "/run/secrets/cnpg.io/webhook"}
	if !mOK || !vOK || len(mounts) != 2 || len(volumes) != 2 {
		return false
	}
	seen := map[string]bool{}
	for _, rawVolume := range volumes {
		volume, ok := rawVolume.(map[string]any)
		name := clusterSSHStorageText(volume, "name")
		_, known := wanted[name]
		if !ok || !known || seen[name] || len(volume) != 2 {
			return false
		}
		seen[name] = true
		if name == "scratch-data" {
			data, ok := volume["emptyDir"].(map[string]any)
			if !ok || len(data) != 0 {
				return false
			}
		} else {
			secret := clusterSSHStorageMap(volume, "secret")
			mode, ok := clusterSSHStorageInteger(secret["defaultMode"])
			if len(secret) != 3 || !ok || mode != 420 || secret["secretName"] != "cnpg-webhook-cert" || secret["optional"] != true {
				return false
			}
		}
	}
	for _, rawMount := range mounts {
		mount, ok := rawMount.(map[string]any)
		name := clusterSSHStorageText(mount, "name")
		path, known := wanted[name]
		if !ok || !known || mount["mountPath"] != path {
			return false
		}
		for key, value := range mount {
			switch key {
			case "name", "mountPath":
			case "readOnly":
				if value != false {
					return false
				}
			case "mountPropagation":
				if value != "None" {
					return false
				}
			default:
				return false
			}
		}
		delete(wanted, name)
	}
	return len(wanted) == 0
}
