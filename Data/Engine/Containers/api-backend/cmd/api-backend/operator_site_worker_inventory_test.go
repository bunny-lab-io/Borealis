package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func operatorInventoryFixture(t *testing.T, legacy bool) (map[string]any, map[string]any) {
	t.Helper()
	o := &borealisOperator{namespace: "borealis"}
	name := siteWorkerPodNameForSite(7)
	if legacy {
		name = "site-worker-bunny-lab"
	}
	guid := schedulerK3sSiteWorkerGUID(7)
	profile, _ := borealisOperatorSiteWorkerResourceProfile("standard")
	pod := o.siteWorkerPodManifest(name, name, "10.43.0.7", 7, "Bunny Lab", "bunny-lab", guid, "borealis-engine/site-worker:sha-cccccccccccc", "standard", profile, 56001, 61001)
	service := o.siteWorkerServiceManifest(name, 7, "Bunny Lab", "bunny-lab", guid, 56001, 61001)
	pod, service = operatorInventoryClone(t, pod), operatorInventoryClone(t, service)
	for _, object := range []map[string]any{pod, service} {
		meta := clusterSSHStorageMap(object, "metadata")
		meta["uid"], meta["resourceVersion"] = newClusterUUID(), "17"
	}
	clusterSSHStorageMap(pod, "spec")["nodeName"] = "engine-original"
	clusterSSHStorageMap(service, "spec")["clusterIP"] = "10.43.0.7"
	return pod, service
}

func operatorInventoryClone(t *testing.T, object map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(object)
	var decoded operatorSiteWorkerInventoryJSONObject
	decodeErr := decoded.decode(bytes.NewReader(raw))
	if err != nil || decodeErr != nil {
		t.Fatal("invalid fixture", err)
	}
	return map[string]any(decoded.exact)
}

func operatorInventoryList(resource string, items []any) map[string]any {
	kind := "PodList"
	if resource == "services" {
		kind = "ServiceList"
	}
	return map[string]any{"apiVersion": "v1", "kind": kind, "metadata": map[string]any{"resourceVersion": "19", "remainingItemCount": 0}, "items": items}
}

func operatorInventoryResponse(request *http.Request, items []any) map[string]any {
	resource := "pods"
	if strings.HasSuffix(request.URL.Path, "/services") {
		resource = "services"
	}
	return operatorInventoryList(resource, items)
}

func TestOperatorSiteWorkerInventoryProducerViews(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, state := range []string{"resident", "candidate", "completed"} {
			t.Run(fmt.Sprintf("legacy-%t/%s", legacy, state), func(t *testing.T) {
				pod, service := operatorInventoryFixture(t, legacy)
				if state != "resident" {
					pod = sshWorkerRotationCandidate(t, pod)
					meta := clusterSSHStorageMap(pod, "metadata")
					meta["uid"], meta["resourceVersion"] = newClusterUUID(), "18"
					clusterSSHStorageMap(pod, "spec")["nodeName"] = "engine-original"
					if state == "completed" {
						delete(clusterSSHStorageMap(meta, "labels"), "borealis.io/redeploy-agent-candidate")
					}
				}
				reads := map[string]int{}
				var readsMu sync.Mutex
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet {
						t.Errorf("inventory mutated Kubernetes: %s", r.Method)
					}
					readsMu.Lock()
					reads[r.URL.Path]++
					readsMu.Unlock()
					var items []any
					switch r.URL.Path {
					case "/api/v1/namespaces/borealis/pods":
						items = []any{pod}
					case "/api/v1/namespaces/borealis/services":
						items = []any{service}
					case "/apis/metrics.k8s.io/v1beta1/namespaces/borealis/pods":
						items = []any{}
					default:
						t.Errorf("unexpected inventory read: %s", r.URL.Path)
					}
					writeJSON(w, http.StatusOK, operatorInventoryResponse(r, items))
				}))
				defer server.Close()
				o := borealisOperatorTestClient(server)
				workers, err := o.listSiteWorkers(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				readsMu.Lock()
				clear(reads)
				readsMu.Unlock()
				result, status, err := o.executeCommand(context.Background(), "ListSiteWorkers", nil)
				if err != nil || status != http.StatusOK || !reflect.DeepEqual(workers, result["workers"]) {
					t.Fatal("private projection changed worker view", status, err)
				}
				readsMu.Lock()
				defer readsMu.Unlock()
				if len(reads) != 3 {
					t.Fatal("unexpected inventory read set", reads)
				}
				for path, count := range reads {
					if count != 1 {
						t.Fatal("views used different native observations", path, count)
					}
				}
				inventory, ok := result["source_inventory"].(*operatorSiteWorkerInventory)
				if !ok || inventory.Version != 1 || len(inventory.Pods) != 1 || len(inventory.Services) != 1 {
					t.Fatal("missing complete private inventory")
				}
				p, s := inventory.Pods[0], inventory.Services[0]
				if p.Metadata.Name != clusterSSHStorageMap(pod, "metadata")["name"] || p.Metadata.UID != clusterSSHStorageMap(pod, "metadata")["uid"] ||
					p.Metadata.ResourceVersion != clusterSSHStorageMap(pod, "metadata")["resourceVersion"] || p.NodeName == nil || *p.NodeName != "engine-original" ||
					s.Metadata.UID != clusterSSHStorageMap(service, "metadata")["uid"] || s.ClusterIP == nil || *s.ClusterIP != "10.43.0.7" || len(s.Ports) != 2 ||
					s.Selector["borealis.io/worker-guid"] != schedulerK3sSiteWorkerGUID(7) || p.Metadata.Annotations["borealis.io/service-name"] != s.Metadata.Name {
					t.Fatal("native identity or stable Service selection lost")
				}
				_, candidate := p.Metadata.Labels["borealis.io/redeploy-agent-candidate"]
				if candidate != (state == "candidate") || (state != "resident" && p.Metadata.Name == s.Metadata.Name) {
					t.Fatal("projection inferred rotation completion or renamed stable Service")
				}
				for i, object := range []map[string]any{pod, service} {
					raw, _ := json.Marshal(object)
					digest := sha256.Sum256(raw)
					if []string{p.SHA256, s.SHA256}[i] != hex.EncodeToString(digest[:]) {
						t.Fatal("projection lost full-object receipt")
					}
				}
			})
		}
	}
}

func TestOperatorSiteWorkerInventoryEmptyAndMissingEnvelope(t *testing.T) {
	for _, envelope := range []bool{false, true} {
		t.Run(fmt.Sprint(envelope), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				response := map[string]any{"items": []any{}}
				if envelope {
					response = operatorInventoryResponse(r, []any{})
				}
				writeJSON(w, http.StatusOK, response)
			}))
			defer server.Close()
			result, status, err := borealisOperatorTestClient(server).executeCommand(context.Background(), "ListSiteWorkers", nil)
			if err != nil || status != http.StatusOK {
				t.Fatal("empty legacy view failed", err)
			}
			inventory, ok := result["source_inventory"].(*operatorSiteWorkerInventory)
			if !ok || (inventory != nil) != envelope {
				t.Fatal("missing envelope became valid empty proof")
			}
			if envelope && (inventory.Version != 1 || inventory.Pods == nil || inventory.Services == nil || len(inventory.Pods) != 0 || len(inventory.Services) != 0) {
				t.Fatal("complete empty list lost version or independent arrays")
			}
		})
	}
}

func TestOperatorSiteWorkerInventoryUnavailablePreservesWorkers(t *testing.T) {
	for _, field := range []string{"uid", "resourceVersion", "selector"} {
		t.Run(field, func(t *testing.T) {
			pod, service := operatorInventoryFixture(t, false)
			if field == "selector" {
				clusterSSHStorageMap(service, "spec")["selector"] = []any{}
			} else {
				delete(clusterSSHStorageMap(pod, "metadata"), field)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				items := []any{}
				switch r.URL.Path {
				case "/api/v1/namespaces/borealis/pods":
					items = []any{pod}
				case "/api/v1/namespaces/borealis/services":
					items = []any{service}
				}
				writeJSON(w, http.StatusOK, operatorInventoryResponse(r, items))
			}))
			defer server.Close()
			o := borealisOperatorTestClient(server)
			legacy, err := o.listSiteWorkers(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			result, status, err := o.executeCommand(context.Background(), "ListSiteWorkers", nil)
			if err != nil || status != http.StatusOK || !reflect.DeepEqual(legacy, result["workers"]) {
				t.Fatal("unavailable proof changed legacy workers", status, err)
			}
			raw, _ := json.Marshal(result)
			var wire map[string]any
			if json.Unmarshal(raw, &wire) != nil {
				t.Fatal("invalid response")
			}
			inventory, present := wire["source_inventory"]
			if !present || inventory != nil {
				t.Fatal("unavailable proof became absent field or valid empty inventory")
			}
			legacyRaw, _ := json.Marshal(legacy)
			var legacyWire []any
			if json.Unmarshal(legacyRaw, &legacyWire) != nil {
				t.Fatal("legacy view invalid")
			}
			if !reflect.DeepEqual(borealisOperatorPublicSiteWorkers(legacyWire), borealisOperatorPublicSiteWorkers(wire["workers"])) {
				t.Fatal("unavailable proof changed public worker status")
			}
		})
	}
}

func TestOperatorSiteWorkerInventoryExactNumericReceipts(t *testing.T) {
	for _, pair := range [][2]string{{"9007199254740992", "9007199254740993"}, {"1.0000000000000000001", "1.0000000000000000002"}, {"1e3", "1000"}} {
		t.Run(pair[0]+"/"+pair[1], func(t *testing.T) {
			pod, service := operatorInventoryFixture(t, false)
			bodies := [2]map[string][]byte{}
			var observed atomic.Int32
			for i, number := range pair {
				bodies[i] = map[string][]byte{}
				for kind, object := range map[string]map[string]any{"pods": pod, "services": service} {
					copy := operatorInventoryClone(t, object)
					clusterSSHStorageMap(copy, "spec")["ignored-future-field"] = json.Number(number)
					raw, err := json.Marshal(operatorInventoryList(kind, []any{copy}))
					if err != nil {
						t.Fatal(err)
					}
					bodies[i][kind] = raw
				}
			}
			// Prove this pair is invisible to the old numeric representation.
			var old, next any
			if json.Unmarshal([]byte(pair[0]), &old) != nil || json.Unmarshal([]byte(pair[1]), &next) != nil || old != next {
				t.Fatal("numeric control does not exercise float64 loss")
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for _, kind := range []string{"pods", "services"} {
					if r.URL.Path == "/api/v1/namespaces/borealis/"+kind {
						_, _ = w.Write(bodies[observed.Load()][kind])
						return
					}
				}
				writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
			}))
			defer server.Close()
			o := borealisOperatorTestClient(server)
			first, status, err := o.executeCommand(context.Background(), "ListSiteWorkers", nil)
			if err != nil || status != http.StatusOK {
				t.Fatal("first private read failed", err)
			}
			observed.Store(1)
			second, status, err := o.executeCommand(context.Background(), "ListSiteWorkers", nil)
			if err != nil || status != http.StatusOK || !reflect.DeepEqual(first["workers"], second["workers"]) {
				t.Fatal("exact receipt changed legacy workers", err)
			}
			a, okA := first["source_inventory"].(*operatorSiteWorkerInventory)
			b, okB := second["source_inventory"].(*operatorSiteWorkerInventory)
			if !okA || !okB || a == nil || b == nil || a.Pods[0].SHA256 == b.Pods[0].SHA256 || a.Services[0].SHA256 == b.Services[0].SHA256 {
				t.Fatal("number spelling or precision drift lost from full-object receipt")
			}
		})
	}
}

func TestOperatorSiteWorkerInventoryCompleteArrays(t *testing.T) {
	for _, mode := range []string{"empty", "orphan Service", "orphan Pod", "duplicate site", "duplicate GUID", "duplicate UID", "partial labels", "terminating", "candidate false", "candidate empty", "pending", "owned"} {
		t.Run(mode, func(t *testing.T) {
			pod, service := operatorInventoryFixture(t, false)
			pods, services := []map[string]any{pod}, []map[string]any{service}
			wantPods, wantServices := 1, 1
			switch mode {
			case "empty":
				pods, services, wantPods, wantServices = []map[string]any{}, []map[string]any{}, 0, 0
			case "orphan Service":
				pods, wantPods = []map[string]any{}, 0
			case "orphan Pod":
				services, wantServices = []map[string]any{}, 0
			case "duplicate site", "duplicate GUID", "duplicate UID":
				for i, object := range []map[string]any{pod, service} {
					copy := operatorInventoryClone(t, object)
					meta := clusterSSHStorageMap(copy, "metadata")
					meta["name"] = "site-worker-second"
					if mode != "duplicate UID" {
						meta["uid"] = newClusterUUID()
					}
					if mode == "duplicate site" {
						clusterSSHStorageMap(meta, "labels")["borealis.io/worker-guid"] = newClusterUUID()
					}
					if mode == "duplicate GUID" {
						clusterSSHStorageMap(meta, "labels")["borealis.io/site-id"] = "8"
					}
					if i == 0 {
						pods, wantPods = append(pods, copy), 2
					} else {
						services, wantServices = append(services, copy), 2
					}
				}
			case "partial labels":
				clusterSSHStorageMap(pod, "metadata")["name"] = "uncertain-worker"
				clusterSSHStorageMap(pod, "metadata")["labels"] = map[string]any{"app.kubernetes.io/component": " Site-Worker "}
			case "terminating":
				clusterSSHStorageMap(pod, "metadata")["deletionTimestamp"] = "2026-11-09T22:00:00Z"
				clusterSSHStorageMap(service, "metadata")["deletionTimestamp"] = "2026-11-09T22:00:01Z"
			case "candidate false", "candidate empty":
				value := "false"
				if mode == "candidate empty" {
					value = ""
				}
				clusterSSHStorageMap(clusterSSHStorageMap(pod, "metadata"), "labels")["borealis.io/redeploy-agent-candidate"] = value
			case "pending":
				delete(clusterSSHStorageMap(pod, "spec"), "nodeName")
				delete(clusterSSHStorageMap(service, "spec"), "clusterIP")
			case "owned":
				clusterSSHStorageMap(pod, "metadata")["ownerReferences"] = []any{map[string]any{"uid": newClusterUUID()}}
			}
			inventory, err := projectOperatorSiteWorkerInventory(pods, services, "borealis")
			if err != nil || inventory.Pods == nil || inventory.Services == nil || len(inventory.Pods) != wantPods || len(inventory.Services) != wantServices {
				t.Fatal("independent inventory lost objects or empty-list identity", err)
			}
			if mode == "terminating" && (inventory.Pods[0].Metadata.DeletionTimestamp == nil || inventory.Services[0].Metadata.DeletionTimestamp == nil) {
				t.Fatal("terminating objects appeared settled")
			}
			if strings.HasPrefix(mode, "candidate ") {
				if _, present := inventory.Pods[0].Metadata.Labels["borealis.io/redeploy-agent-candidate"]; !present {
					t.Fatal("marker presence lost")
				}
			}
			if mode == "pending" && (inventory.Pods[0].NodeName != nil || inventory.Services[0].ClusterIP != nil) {
				t.Fatal("missing assignment became supplied evidence")
			}
			if mode == "owned" && !inventory.Pods[0].Metadata.HasOwnerReferences {
				t.Fatal("owner references lost")
			}
		})
	}
}

func TestOperatorSiteWorkerInventoryStrictFields(t *testing.T) {
	mutations := map[string]func(map[string]any, map[string]any){
		"Pod kind":        func(p, s map[string]any) { p["kind"] = "Service" },
		"Service version": func(p, s map[string]any) { s["apiVersion"] = "v2" },
		"namespace":       func(p, s map[string]any) { clusterSSHStorageMap(p, "metadata")["namespace"] = "foreign" },
		"UID missing":     func(p, s map[string]any) { delete(clusterSSHStorageMap(p, "metadata"), "uid") },
		"UID zero": func(p, s map[string]any) {
			clusterSSHStorageMap(s, "metadata")["uid"] = "00000000-0000-0000-0000-000000000000"
		},
		"revision missing": func(p, s map[string]any) { delete(clusterSSHStorageMap(s, "metadata"), "resourceVersion") },
		"revision control": func(p, s map[string]any) { clusterSSHStorageMap(s, "metadata")["resourceVersion"] = "1\n" },
		"revision oversized": func(p, s map[string]any) {
			clusterSSHStorageMap(s, "metadata")["resourceVersion"] = strings.Repeat("1", 129)
		},
		"label type": func(p, s map[string]any) {
			clusterSSHStorageMap(clusterSSHStorageMap(p, "metadata"), "labels")["borealis.io/site-id"] = float64(7)
		},
		"annotation type": func(p, s map[string]any) {
			clusterSSHStorageMap(clusterSSHStorageMap(p, "metadata"), "annotations")["borealis.io/service-name"] = true
		},
		"node empty":        func(p, s map[string]any) { clusterSSHStorageMap(p, "spec")["nodeName"] = "" },
		"node numeric":      func(p, s map[string]any) { clusterSSHStorageMap(p, "spec")["nodeName"] = float64(1) },
		"deleted empty":     func(p, s map[string]any) { clusterSSHStorageMap(p, "metadata")["deletionTimestamp"] = "" },
		"deleted malformed": func(p, s map[string]any) { clusterSSHStorageMap(s, "metadata")["deletionTimestamp"] = "tomorrow" },
		"owner shape":       func(p, s map[string]any) { clusterSSHStorageMap(p, "metadata")["ownerReferences"] = []any{nil} },
		"spec absent":       func(p, s map[string]any) { delete(s, "spec") },
		"Service type":      func(p, s map[string]any) { clusterSSHStorageMap(s, "spec")["type"] = "ExternalName" },
		"IP empty":          func(p, s map[string]any) { clusterSSHStorageMap(s, "spec")["clusterIP"] = "" },
		"IP malformed":      func(p, s map[string]any) { clusterSSHStorageMap(s, "spec")["clusterIP"] = "None" },
		"selector type":     func(p, s map[string]any) { clusterSSHStorageMap(s, "spec")["selector"] = []any{} },
		"selector value": func(p, s map[string]any) {
			clusterSSHStorageMap(clusterSSHStorageMap(s, "spec"), "selector")["borealis.io/site-id"] = float64(7)
		},
		"ports missing": func(p, s map[string]any) { delete(clusterSSHStorageMap(s, "spec"), "ports") },
		"port type":     func(p, s map[string]any) { clusterSSHStorageMap(s, "spec")["ports"] = []any{true} },
		"port fractional": func(p, s map[string]any) {
			clusterSSHStorageMap(s, "spec")["ports"].([]any)[0].(map[string]any)["port"] = 1.5
		},
		"port oversized": func(p, s map[string]any) {
			clusterSSHStorageMap(s, "spec")["ports"].([]any)[0].(map[string]any)["port"] = float64(65536)
		},
		"target numeric": func(p, s map[string]any) {
			clusterSSHStorageMap(s, "spec")["ports"].([]any)[0].(map[string]any)["targetPort"] = float64(56001)
		},
		"protocol": func(p, s map[string]any) {
			clusterSSHStorageMap(s, "spec")["ports"].([]any)[0].(map[string]any)["protocol"] = "UDP"
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			pod, service := operatorInventoryFixture(t, false)
			mutate(pod, service)
			if got, err := projectOperatorSiteWorkerInventory([]map[string]any{pod}, []map[string]any{service}, "borealis"); err == nil || got != nil {
				t.Fatal("malformed inventory projected as usable evidence")
			}
		})
	}
}

func TestOperatorSiteWorkerInventoryDriftAndIsolation(t *testing.T) {
	pod, service := operatorInventoryFixture(t, false)
	clusterSSHStorageMap(pod, "spec")["private-test-env"] = "PRIVATE-ENV-MUST-NOT-EXPORT"
	clusterSSHStorageMap(clusterSSHStorageMap(pod, "metadata"), "annotations")["private-test-annotation"] = "PRIVATE-ANNOTATION-MUST-NOT-EXPORT"
	before, err := projectOperatorSiteWorkerInventory([]map[string]any{pod}, []map[string]any{service}, "borealis")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(before)
	if strings.Contains(string(raw), "MUST-NOT-EXPORT") || strings.Contains(string(raw), "private-test") {
		t.Fatal("private inventory exported raw fields")
	}
	clusterSSHStorageMap(pod, "spec")["private-test-env"] = "PRIVATE-CHANGED"
	clusterSSHStorageMap(service, "spec")["sessionAffinity"] = "ClientIP"
	after, err := projectOperatorSiteWorkerInventory([]map[string]any{pod}, []map[string]any{service}, "borealis")
	if err != nil || before.Pods[0].SHA256 == after.Pods[0].SHA256 || before.Services[0].SHA256 == after.Services[0].SHA256 {
		t.Fatal("unprojected source drift escaped receipt", err)
	}
	after.Pods[0].SHA256, after.Services[0].SHA256 = before.Pods[0].SHA256, before.Services[0].SHA256
	if !reflect.DeepEqual(before, after) {
		t.Fatal("private unrelated fields changed typed projection")
	}
	before.Pods[0].Metadata.Labels["borealis.io/site-id"] = "99"
	before.Pods[0].Metadata.Annotations["borealis.io/service-name"] = "changed"
	before.Services[0].Selector["borealis.io/site-id"] = "99"
	*before.Pods[0].NodeName = "changed"
	if clusterSSHStorageMap(clusterSSHStorageMap(pod, "metadata"), "labels")["borealis.io/site-id"] != "7" || clusterSSHStorageMap(clusterSSHStorageMap(service, "spec"), "selector")["borealis.io/site-id"] != "7" || clusterSSHStorageMap(pod, "spec")["nodeName"] != "engine-original" {
		t.Fatal("projection aliases native objects")
	}
}

func TestOperatorSiteWorkerInventoryRejectsIncompleteReads(t *testing.T) {
	for _, resource := range []string{"pods", "services"} {
		for _, body := range []string{`{}`, `{"items":null}`, `{"items":[null]}`, `{"items":[],"metadata":{"continue":"next"}}`, `{"items":[],"metadata":{"remainingItemCount":1}}`, `{"items":[],"items":[]}`, `{"items":[]} {}`} {
			t.Run(resource+"/"+body, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/v1/namespaces/borealis/"+resource {
						_, _ = w.Write([]byte(body))
						return
					}
					writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
				}))
				defer server.Close()
				got, status, err := borealisOperatorTestClient(server).executeCommand(context.Background(), "ListSiteWorkers", nil)
				if err == nil || status != http.StatusBadGateway || got != nil {
					t.Fatal("incomplete native read produced private inventory")
				}
			})
		}
	}
}

func TestOperatorSiteWorkerInventoryBounds(t *testing.T) {
	pod, service := operatorInventoryFixture(t, false)
	for _, kind := range []string{"pods", "services"} {
		t.Run(kind, func(t *testing.T) {
			pods, services := []map[string]any{}, []map[string]any{}
			for i := 0; i <= clusterSSHSiteWorkerLimit; i++ {
				object := pod
				if kind == "services" {
					object = service
				}
				copy := operatorInventoryClone(t, object)
				clusterSSHStorageMap(copy, "metadata")["name"] = fmt.Sprintf("site-worker-site-%d", i+1)
				if kind == "pods" {
					pods = append(pods, copy)
				} else {
					services = append(services, copy)
				}
			}
			if got, err := projectOperatorSiteWorkerInventory(pods, services, "borealis"); err == nil || got != nil {
				t.Fatal("oversized collection accepted")
			}
			if kind == "pods" {
				pods = pods[:clusterSSHSiteWorkerLimit]
			} else {
				services = services[:clusterSSHSiteWorkerLimit]
			}
			if _, err := projectOperatorSiteWorkerInventory(pods, services, "borealis"); err != nil {
				t.Fatal("exact collection boundary rejected", err)
			}
		})
	}
	if got, err := projectOperatorSiteWorkerInventory(nil, []map[string]any{}, "borealis"); err == nil || got != nil {
		t.Fatal("absent list interpreted as empty inventory")
	}
	if got, err := projectOperatorSiteWorkerInventory([]map[string]any{pod, pod}, []map[string]any{}, "borealis"); err == nil || got != nil {
		t.Fatal("duplicate object name accepted")
	}
	result := map[string]any{"workers": []any{}, "padding": ""}
	raw, _ := json.Marshal(map[string]any{"ok": true, "verb": "ListSiteWorkers", "result": result})
	result["padding"] = strings.Repeat("x", operatorSiteWorkerInventoryMaxBytes-len(raw)-1)
	if !operatorSiteWorkerInventoryResponseBounded(result) {
		t.Fatal("exact response boundary rejected")
	}
	result["padding"] = result["padding"].(string) + "x"
	if operatorSiteWorkerInventoryResponseBounded(result) {
		t.Fatal("oversized response accepted")
	}
}

func TestOperatorSiteWorkerInventoryNearBoundPreservesLegacyResponse(t *testing.T) {
	for _, room := range []int{128, 0} {
		t.Run(fmt.Sprint(room), func(t *testing.T) {
			pod, service := operatorInventoryFixture(t, false)
			annotations := clusterSSHStorageMap(clusterSSHStorageMap(service, "metadata"), "annotations")
			annotations["legacy-large-annotation"] = ""
			var mu sync.Mutex
			kubeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				items := []any{}
				switch r.URL.Path {
				case "/api/v1/namespaces/borealis/pods":
					items = []any{pod}
				case "/api/v1/namespaces/borealis/services":
					items = []any{service}
				}
				response := operatorInventoryResponse(r, items)
				raw, err := json.Marshal(response)
				if err != nil || len(raw)+1 > operatorSiteWorkerInventoryMaxBytes {
					t.Error("boundary fixture exceeds original native read bound", len(raw), err)
				}
				writeJSON(w, http.StatusOK, response)
			}))
			defer kubeServer.Close()
			o := borealisOperatorTestClient(kubeServer)
			legacy, err := o.listSiteWorkers(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			legacyEnvelope := httptest.NewRecorder()
			writeJSON(legacyEnvelope, http.StatusOK, map[string]any{"ok": true, "verb": "ListSiteWorkers", "result": map[string]any{"workers": legacy}})
			mu.Lock()
			annotations["legacy-large-annotation"] = strings.Repeat("x", operatorSiteWorkerInventoryMaxBytes-legacyEnvelope.Body.Len()-room)
			mu.Unlock()
			legacy, err = o.listSiteWorkers(context.Background())
			if err != nil {
				t.Fatal("large native inventory not legacy-readable", err)
			}
			legacyEnvelope = httptest.NewRecorder()
			writeJSON(legacyEnvelope, http.StatusOK, map[string]any{"ok": true, "verb": "ListSiteWorkers", "result": map[string]any{"workers": legacy}})
			if legacyEnvelope.Body.Len() != operatorSiteWorkerInventoryMaxBytes-room {
				t.Fatal("boundary fixture does not reach intended legacy response size")
			}
			o.secret = []byte("private-test-operator-boundary-secret")
			operatorServer := httptest.NewServer(o.handler())
			defer operatorServer.Close()
			client := &borealisOperatorClient{baseURL: operatorServer.URL, token: goBorealisOperatorToken(o.secret), httpClient: operatorServer.Client()}
			response, err := client.command(context.Background(), "ListSiteWorkers", nil)
			if err != nil {
				t.Fatal("new proof broke previously readable worker result", err)
			}
			result := response["result"].(map[string]any)
			expected, _ := json.Marshal(legacy)
			actual, _ := json.Marshal(result["workers"])
			if !bytes.Equal(actual, expected) {
				t.Fatal("boundary handling truncated or altered workers")
			}
			proof, present := result["source_inventory"]
			if proof != nil || present != (room > 0) {
				t.Fatal("oversized proof failed to remain explicitly unavailable", present)
			}
		})
	}
}

func TestOperatorSiteWorkerInventoryOwnedCleanup(t *testing.T) {
	for _, resource := range []string{"pods", "services"} {
		for _, mode := range []string{"success", "close error", "blocked close cancel", "child timeout", "client timeout", "redirect", "unfollowed redirect"} {
			t.Run(resource+"/"+mode, func(t *testing.T) {
				pod, service := operatorInventoryFixture(t, false)
				object := pod
				if resource == "services" {
					object = service
				}
				raw, err := json.Marshal(operatorInventoryList(resource, []any{object}))
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				closing, release := make(chan struct{}), make(chan struct{})
				var unblock sync.Once
				defer unblock.Do(func() { close(release) })
				var fired atomic.Bool
				var active, requests atomic.Int64
				transport := &cohortMembershipTransport{roundTrip: func(request *http.Request) (*http.Response, error) {
					requests.Add(1)
					active.Add(1)
					response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Request: request}
					if (mode == "redirect" || mode == "unfollowed redirect") && strings.HasSuffix(request.URL.Path, "/"+resource) && fired.CompareAndSwap(false, true) {
						response.StatusCode = http.StatusFound
						response.Header.Set("Location", request.URL.String()+"-redirected")
					}
					response.Body = &cohortMembershipBody{ReadCloser: io.NopCloser(bytes.NewReader(raw)), close: func() error {
						defer active.Add(-1)
						if mode != "success" && fired.CompareAndSwap(false, true) {
							switch mode {
							case "close error":
								return errors.New("synthetic inventory close failure")
							case "blocked close cancel":
								close(closing)
								<-release
							case "child timeout", "client timeout":
								deadline, ok := request.Context().Deadline()
								if !ok || time.Until(deadline) > 10*time.Second {
									t.Error("native request lost original child deadline")
								}
								if mode == "client timeout" && time.Until(deadline) > time.Second {
									t.Error("copied client lost shorter borrowed timeout")
								}
								<-request.Context().Done()
							}
						}
						return nil
					}}
					return response, nil
				}}
				client := &http.Client{Transport: transport, Timeout: 20 * time.Second}
				if mode == "client timeout" {
					client.Timeout = 40 * time.Millisecond
				}
				if mode == "unfollowed redirect" {
					client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
				}
				timeout := client.Timeout
				redirect := reflect.ValueOf(client.CheckRedirect).Pointer()
				kube := &kubernetesAPIClient{baseURL: "http://inventory.invalid", httpClient: client}
				o := &borealisOperator{namespace: "borealis", kube: kube}
				var legacy, exact []map[string]any
				if mode == "blocked close cancel" {
					done := make(chan struct{})
					go func() { legacy, exact, err = o.siteWorkerObjectsWithInventory(ctx, resource); close(done) }()
					defer func() {
						cancel()
						unblock.Do(func() { close(release) })
						select {
						case <-done:
						case <-time.After(5 * time.Second):
							t.Error("inventory reader survived test cleanup")
						}
					}()
					select {
					case <-closing:
					case <-done:
						t.Fatal("reader escaped owned Close barrier")
					case <-ctx.Done():
						t.Fatal("reader never reached Close barrier")
					}
					cancel()
					select {
					case <-done:
						t.Fatal("cancellation escaped blocked owned Close")
					case <-time.After(25 * time.Millisecond):
					}
					unblock.Do(func() { close(release) })
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Fatal("reader did not join released Close")
					}
				} else {
					legacy, exact, err = o.siteWorkerObjectsWithInventory(ctx, resource)
				}
				if err != nil || len(legacy) != 1 {
					t.Fatal("private cleanup changed successfully decoded legacy view", err)
				}
				if (exact != nil) != (mode == "success") {
					t.Fatal("unconfirmed response cleanup or redirected read exported private evidence")
				}
				if mode != "success" && !fired.Load() {
					t.Fatal("fixture missed intended request boundary")
				}
				if (mode == "child timeout" || mode == "client timeout") && ctx.Err() != nil {
					t.Fatal("fixture expired outer scope instead of child request")
				}
				wantRequests := int64(1)
				if mode == "redirect" {
					wantRequests = 2
				}
				if requests.Load() != wantRequests || active.Load() != 0 {
					t.Fatal("response accounting leaked or added reads", requests.Load(), active.Load())
				}
				// A failed request's private tracker must not poison borrowed
				// transport, future sibling reads or ordinary worker consumers.
				_, sibling, err := o.siteWorkerObjectsWithInventory(context.Background(), resource)
				if err != nil || len(sibling) != 1 {
					t.Fatal("failed private proof poisoned sibling read", err)
				}
				if o.kube != kube || kube.httpClient != client || client.Transport != transport || client.Timeout != timeout || reflect.ValueOf(client.CheckRedirect).Pointer() != redirect || transport.idleCalls.Load() != 0 || active.Load() != 0 {
					t.Fatal("private proof changed shared client ownership")
				}
			})
		}
	}
}
