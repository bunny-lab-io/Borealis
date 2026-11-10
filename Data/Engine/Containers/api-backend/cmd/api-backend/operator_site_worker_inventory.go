package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// Match the existing private operator JSON transport bound. The per-kind count
// reuses the source collection bound; neither number is a capacity allowance.
const operatorSiteWorkerInventoryMaxBytes = 4 << 20

var errOperatorSiteWorkerInventory = errors.New("site-worker source inventory unavailable")

type operatorSiteWorkerInventoryJSONObject struct {
	legacy operatorSiteWorkerJSONObject
	exact  operatorSiteWorkerJSONObject
}

// Read once, retain the existing decoded worker view, and independently decode
// exact JSON numbers for the private full-object receipts. Never hash values
// rounded through the legacy float64 representation.
func (value *operatorSiteWorkerInventoryJSONObject) decode(reader io.Reader) error {
	raw, err := io.ReadAll(io.LimitReader(reader, operatorSiteWorkerInventoryMaxBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > operatorSiteWorkerInventoryMaxBytes || !utf8.Valid(raw) {
		return errOperatorSiteWorkerInventory
	}
	legacy, err := decodeOperatorSiteWorkerObject(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoded, err := clusterSSHPrivateJSONValue(decoder, 0)
	exact, ok := decoded.(map[string]any)
	if err != nil || !ok || exact == nil {
		return errOperatorSiteWorkerInventory
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errOperatorSiteWorkerInventory
	}
	value.legacy, value.exact = legacy, exact
	return nil
}

func (o *borealisOperator) siteWorkerObjectsWithInventory(ctx context.Context, resource string) ([]map[string]any, []map[string]any, error) {
	if o == nil || o.kube == nil || o.kube.httpClient == nil {
		return nil, nil, errOperatorSiteWorkerInventory
	}
	path := o.kubePath("core", resource, "")
	transport := o.kube.httpClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	observed := &operatorSiteWorkerInventoryHTTP{transport: transport, url: strings.TrimRight(o.kube.baseURL, "/") + path}
	cleanup := &clusterSSHMembershipHTTP{transport: clusterSSHPreparationConfigurationHTTP{observed}}
	client, kube := *o.kube.httpClient, *o.kube
	client.Transport, kube.httpClient = cleanup, &client
	var payload operatorSiteWorkerInventoryJSONObject
	if err := kube.getJSON(ctx, path, &payload); err != nil {
		return nil, nil, err
	}
	legacy, err := operatorSiteWorkerObjects(payload.legacy)
	if err != nil {
		return nil, nil, err
	}
	// Invalid stronger evidence is null, never a complete empty list. Existing
	// incomplete/malformed legacy reads still fail the entire command above.
	exact, _ := operatorSiteWorkerObjects(payload.exact)
	wantKind := map[string]string{"pods": "PodList", "services": "ServiceList"}[resource]
	metadata, metadataOK := payload.exact["metadata"].(map[string]any)
	if ctx.Err() != nil || !cleanup.clean() || observed.failed.Load() || wantKind == "" || payload.exact["apiVersion"] != "v1" || payload.exact["kind"] != wantKind || !metadataOK || metadata == nil {
		exact = nil
	}
	return legacy, exact, nil
}

// Observe the existing request behavior without changing borrowed redirect
// policy or transport ownership. Any redirected/unexpected exchange withholds
// private proof while preserving a successfully decoded legacy worker view.
type operatorSiteWorkerInventoryHTTP struct {
	transport http.RoundTripper
	url       string
	requests  atomic.Int64
	failed    atomic.Bool
}

func (t *operatorSiteWorkerInventoryHTTP) RoundTrip(request *http.Request) (*http.Response, error) {
	if t.requests.Add(1) != 1 || request.Method != http.MethodGet || request.URL.String() != t.url {
		t.failed.Store(true)
	}
	response, err := t.transport.RoundTrip(request)
	if err != nil || response == nil || response.StatusCode != http.StatusOK {
		t.failed.Store(true)
	}
	return response, err
}

// Version 1 is a read projection, not completed-rotation custody. Independent
// arrays retain orphan Services and duplicate site/GUID identities. A future
// consumer must join native Pod/endpoint and route evidence under source fences;
// no existing source observer consumes this projection or accepts more names.
type operatorSiteWorkerInventory struct {
	Version  int                                  `json:"version"`
	Pods     []operatorSiteWorkerInventoryPod     `json:"pods"`
	Services []operatorSiteWorkerInventoryService `json:"services"`
}

type operatorSiteWorkerInventoryMetadata struct {
	Name               string            `json:"name"`
	Namespace          string            `json:"namespace"`
	UID                string            `json:"uid"`
	ResourceVersion    string            `json:"resource_version"`
	DeletionTimestamp  *string           `json:"deletion_timestamp"`
	HasOwnerReferences bool              `json:"has_owner_references"`
	Labels             map[string]string `json:"labels"`
	Annotations        map[string]string `json:"annotations"`
}

type operatorSiteWorkerInventoryPod struct {
	Metadata operatorSiteWorkerInventoryMetadata `json:"metadata"`
	NodeName *string                             `json:"node_name"`
	SHA256   string                              `json:"object_sha256"`
}

type operatorSiteWorkerInventoryService struct {
	Metadata  operatorSiteWorkerInventoryMetadata `json:"metadata"`
	Type      string                              `json:"type"`
	ClusterIP *string                             `json:"cluster_ip"`
	Selector  map[string]string                   `json:"selector"`
	Ports     []operatorSiteWorkerInventoryPort   `json:"ports"`
	SHA256    string                              `json:"object_sha256"`
}

type operatorSiteWorkerInventoryPort struct {
	Name       string `json:"name"`
	Port       int64  `json:"port"`
	TargetPort string `json:"target_port"`
	Protocol   string `json:"protocol"`
}

var operatorSiteWorkerInventoryLabels = []string{
	"app.kubernetes.io/name", "app.kubernetes.io/part-of", "app.kubernetes.io/managed-by", "app.kubernetes.io/component",
	"borealis.io/workload", "borealis.io/worker-guid", "borealis.io/site-id", "borealis.io/resource-profile",
	"borealis.io/redeploy-agent-candidate", "borealis.io/redeploy-agent-revision",
}

var operatorSiteWorkerInventoryAnnotations = []string{
	"borealis.io/site-name", "borealis.io/service-name", "borealis.io/redeploy-agent-source-pod", "borealis.io/image-ref",
}

func projectOperatorSiteWorkerInventory(pods, services []map[string]any, namespace string) (*operatorSiteWorkerInventory, error) {
	fail := func() (*operatorSiteWorkerInventory, error) { return nil, errOperatorSiteWorkerInventory }
	if pods == nil || services == nil || namespace != "borealis" {
		return fail()
	}
	result := &operatorSiteWorkerInventory{Version: 1, Pods: []operatorSiteWorkerInventoryPod{}, Services: []operatorSiteWorkerInventoryService{}}
	for kind, objects := range [][]map[string]any{pods, services} {
		names := map[string]bool{}
		for _, object := range objects {
			metadata, ok := object["metadata"].(map[string]any)
			if !ok || metadata == nil {
				return fail()
			}
			name, ok := metadata["name"].(string)
			labels, labelsOK := operatorSiteWorkerInventoryStrings(metadata["labels"])
			if !ok || name == "" || names[name] || !labelsOK {
				return fail()
			}
			names[name] = true
			// Include uncertain/partially labelled worker objects instead of
			// dropping them from an apparently complete inventory.
			workerLabel := func(key string) bool { return strings.EqualFold(strings.TrimSpace(labels[key]), "site-worker") }
			if !workerLabel("app.kubernetes.io/name") && !workerLabel("app.kubernetes.io/component") &&
				!workerLabel("borealis.io/workload") && !strings.HasPrefix(strings.TrimSpace(name), siteWorkerNamePrefix) {
				continue
			}
			wantKind := []string{"Pod", "Service"}[kind]
			meta, valid := operatorSiteWorkerInventoryIdentity(object, namespace, wantKind)
			spec, specOK := object["spec"].(map[string]any)
			raw, err := json.Marshal(object)
			if !valid || !specOK || spec == nil || err != nil || len(raw) > operatorSiteWorkerInventoryMaxBytes || !operatorSiteWorkerInventoryExactValues(object) {
				return fail()
			}
			digest := sha256.Sum256(raw)
			hash := hex.EncodeToString(digest[:])
			if kind == 0 {
				node, ok := operatorSiteWorkerInventoryOptionalText(spec, "nodeName")
				if !ok || (node != nil && !schedulerPlacementName(*node)) || len(result.Pods) >= clusterSSHSiteWorkerLimit {
					return fail()
				}
				result.Pods = append(result.Pods, operatorSiteWorkerInventoryPod{Metadata: meta, NodeName: node, SHA256: hash})
				continue
			}
			service := operatorSiteWorkerInventoryService{Metadata: meta, SHA256: hash, Ports: []operatorSiteWorkerInventoryPort{}}
			service.Type, ok = spec["type"].(string)
			service.ClusterIP, valid = operatorSiteWorkerInventoryOptionalText(spec, "clusterIP")
			selector, selectorOK := operatorSiteWorkerInventoryStrings(spec["selector"])
			ports, portsOK := spec["ports"].([]any)
			if !ok || service.Type != "ClusterIP" || !valid || !selectorOK || !portsOK || len(ports) > clusterSSHSiteWorkerLimit || len(result.Services) >= clusterSSHSiteWorkerLimit {
				return fail()
			}
			if service.ClusterIP != nil {
				ip, err := netip.ParseAddr(*service.ClusterIP)
				if err != nil || ip.String() != *service.ClusterIP || ip.IsUnspecified() || ip.IsMulticast() {
					return fail()
				}
			}
			service.Selector = selector
			for _, rawPort := range ports {
				port, ok := rawPort.(map[string]any)
				number, numberOK := clusterSSHStorageInteger(port["port"])
				name, nameOK := port["name"].(string)
				target, targetOK := port["targetPort"].(string)
				protocol, protocolOK := port["protocol"].(string)
				if !ok || !numberOK || number < 1 || number > 65535 || !nameOK || !borealisOperatorKubernetesObjectNameAllowed(name) ||
					!targetOK || !borealisOperatorKubernetesObjectNameAllowed(target) || !protocolOK || protocol != "TCP" {
					return fail()
				}
				service.Ports = append(service.Ports, operatorSiteWorkerInventoryPort{Name: name, Port: number, TargetPort: target, Protocol: protocol})
			}
			result.Services = append(result.Services, service)
		}
	}
	slices.SortFunc(result.Pods, func(a, b operatorSiteWorkerInventoryPod) int {
		return strings.Compare(a.Metadata.Name, b.Metadata.Name)
	})
	slices.SortFunc(result.Services, func(a, b operatorSiteWorkerInventoryService) int {
		return strings.Compare(a.Metadata.Name, b.Metadata.Name)
	})
	if raw, err := json.Marshal(result); err != nil || len(raw) > operatorSiteWorkerInventoryMaxBytes {
		return fail()
	}
	return result, nil
}

func operatorSiteWorkerInventoryIdentity(object map[string]any, namespace, kind string) (operatorSiteWorkerInventoryMetadata, bool) {
	meta, _ := object["metadata"].(map[string]any)
	var value operatorSiteWorkerInventoryMetadata
	value.Name, _ = meta["name"].(string)
	value.Namespace, _ = meta["namespace"].(string)
	value.UID, _ = meta["uid"].(string)
	value.ResourceVersion, _ = meta["resourceVersion"].(string)
	labels, labelsOK := operatorSiteWorkerInventoryStrings(meta["labels"])
	annotations, annotationsOK := operatorSiteWorkerInventoryStrings(meta["annotations"])
	deleted, deletionOK := operatorSiteWorkerInventoryOptionalText(meta, "deletionTimestamp")
	if object["apiVersion"] != "v1" || object["kind"] != kind || value.Namespace != namespace ||
		!borealisOperatorKubernetesObjectNameAllowed(value.Name) || !vipScopeUUID(value.UID) ||
		len(value.ResourceVersion) == 0 || len(value.ResourceVersion) > 128 || !labelsOK || !annotationsOK || !deletionOK {
		return value, false
	}
	for _, c := range value.ResourceVersion {
		if c < 33 || c > 126 {
			return value, false
		}
	}
	if deleted != nil {
		if _, err := time.Parse(time.RFC3339Nano, *deleted); err != nil {
			return value, false
		}
	}
	value.DeletionTimestamp = deleted
	if raw, exists := meta["ownerReferences"]; exists && raw != nil {
		refs, ok := raw.([]any)
		if !ok || len(refs) > clusterSSHSiteWorkerLimit {
			return value, false
		}
		for _, ref := range refs {
			if object, ok := ref.(map[string]any); !ok || object == nil {
				return value, false
			}
		}
		value.HasOwnerReferences = len(refs) != 0
	}
	value.Labels, value.Annotations = map[string]string{}, map[string]string{}
	for _, key := range operatorSiteWorkerInventoryLabels {
		if text, exists := labels[key]; exists {
			value.Labels[key] = text
		}
	}
	for _, key := range operatorSiteWorkerInventoryAnnotations {
		if text, exists := annotations[key]; exists {
			value.Annotations[key] = text
		}
	}
	return value, true
}

// Missing and JSON null optional scalar fields remain explicit nulls. Empty,
// malformed and oversized supplied values never become apparently absent.
func operatorSiteWorkerInventoryOptionalText(object map[string]any, key string) (*string, bool) {
	raw, exists := object[key]
	if !exists || raw == nil {
		return nil, true
	}
	value, ok := raw.(string)
	if !ok || value == "" || len(value) > operatorSiteWorkerInventoryMaxBytes || !utf8.ValidString(value) {
		return nil, false
	}
	return &value, true
}

func operatorSiteWorkerInventoryStrings(raw any) (map[string]string, bool) {
	result := map[string]string{}
	if raw == nil {
		return result, true
	}
	values, ok := raw.(map[string]any)
	if !ok || len(values) > clusterSSHSiteWorkerLimit {
		return nil, false
	}
	for key, raw := range values {
		value, ok := raw.(string)
		if !ok || key == "" || !utf8.ValidString(key) || !utf8.ValidString(value) || len(key) > operatorSiteWorkerInventoryMaxBytes || len(value) > operatorSiteWorkerInventoryMaxBytes {
			return nil, false
		}
		result[key] = value
	}
	return result, true
}

func operatorSiteWorkerInventoryResponseBounded(result map[string]any) bool {
	raw, err := json.Marshal(map[string]any{"ok": true, "verb": "ListSiteWorkers", "result": result})
	return err == nil && len(raw)+1 <= operatorSiteWorkerInventoryMaxBytes // writeJSON uses Encoder.Encode, including its newline.
}

// Only the independent exact-number decode can supply receipts. Guard pure
// callers too: a float64 must never silently enter the full-object digest.
func operatorSiteWorkerInventoryExactValues(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for _, item := range typed {
			if !operatorSiteWorkerInventoryExactValues(item) {
				return false
			}
		}
	case []any:
		for _, item := range typed {
			if !operatorSiteWorkerInventoryExactValues(item) {
				return false
			}
		}
	case nil, string, bool, json.Number:
	default:
		return false
	}
	return true
}
