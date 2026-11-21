package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type sshWorkerCustodyFixture struct {
	source                 clusterSSHSourceCohort
	pod, service, endpoint map[string]any
	inventory              *operatorSiteWorkerInventory
	routes                 []clusterSSHCurrentWorkerRoute
}

func newSSHWorkerCustodyFixture(t *testing.T, legacy bool, rotations int) *sshWorkerCustodyFixture {
	t.Helper()
	pod, service := operatorInventoryFixture(t, legacy)
	for i := 0; i < rotations; i++ {
		pod = operatorInventoryClone(t, sshWorkerRotationCandidate(t, pod))
		meta := clusterSSHStorageMap(pod, "metadata")
		meta["uid"], meta["resourceVersion"] = newClusterUUID(), "19"
		delete(clusterSSHStorageMap(meta, "labels"), "borealis.io/redeploy-agent-candidate")
		clusterSSHStorageMap(pod, "spec")["nodeName"] = "engine-original"
		clusterSSHStorageMap(clusterSSHStorageMap(service, "spec"), "selector")["borealis.io/redeploy-agent-revision"] = "agent-fixture"
	}
	pod["status"] = map[string]any{"phase": "Running", "podIP": "10.42.0.7", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}}
	f := &sshWorkerCustodyFixture{source: clusterSSHSourceCohort{Members: []clusterSSHSourceMember{{Name: "engine-original"}}}, pod: pod, service: service}
	f.refresh(t)
	meta, serviceMeta := clusterSSHStorageMap(pod, "metadata"), clusterSSHStorageMap(service, "metadata")
	f.endpoint = operatorInventoryClone(t, map[string]any{
		"apiVersion": "discovery.k8s.io/v1", "kind": "EndpointSlice", "addressType": "IPv4",
		"metadata": map[string]any{"name": serviceMeta["name"].(string) + "-abcde", "namespace": "borealis", "uid": newClusterUUID(), "resourceVersion": "27",
			"labels":          map[string]any{"kubernetes.io/service-name": serviceMeta["name"], "endpointslice.kubernetes.io/managed-by": "endpointslice-controller.k8s.io"},
			"ownerReferences": []any{map[string]any{"apiVersion": "v1", "kind": "Service", "name": serviceMeta["name"], "uid": serviceMeta["uid"], "controller": true, "blockOwnerDeletion": true}}},
		"ports": []any{map[string]any{"name": "remote-ops", "port": 56001, "protocol": "TCP"}, map[string]any{"name": "remote-desktop", "port": 61001, "protocol": "TCP"}},
		"endpoints": []any{map[string]any{"addresses": []any{"10.42.0.7"}, "conditions": map[string]any{"ready": true, "serving": true, "terminating": false}, "nodeName": "engine-original",
			"targetRef": map[string]any{"kind": "Pod", "namespace": "borealis", "name": meta["name"], "uid": meta["uid"]}}},
	})
	return f
}

func (f *sshWorkerCustodyFixture) refresh(t *testing.T) {
	t.Helper()
	var err error
	f.inventory, err = projectOperatorSiteWorkerInventory([]map[string]any{f.pod}, []map[string]any{f.service}, "borealis")
	if err != nil {
		t.Fatal(err)
	}
	row := sshCustodyRouteRow(t, f.pod, f.service, `{"worker_admission":{"version":1,"operations":{"old":{"state":"terminal","pod_uid":"old-incarnation"}}}}`)
	route, ok := clusterSSHCurrentWorkerRouteFromRow(row)
	if !ok {
		t.Fatal("actual route producer fixture rejected")
	}
	f.routes = []clusterSSHCurrentWorkerRoute{route}
}

func (f *sshWorkerCustodyFixture) join() ([]clusterSSHSiteWorkerCustody, error) {
	return joinClusterSSHSiteWorkerCustody(f.source, []map[string]any{f.pod}, f.inventory, map[string][]map[string]any{f.inventory.Services[0].Metadata.Name: {f.endpoint}}, f.routes)
}

func TestClusterSSHSiteWorkerCustodyProducerJoin(t *testing.T) {
	t.Setenv("BOREALIS_DEPLOYMENT_PROFILE_RANK", "1")
	t.Setenv("BOREALIS_CLUSTER_SIZING_RANK", "1")
	for _, legacy := range []bool{false, true} {
		for _, rotations := range []int{0, 1, 2} {
			name := "current"
			if legacy {
				name = "legacy"
			}
			t.Run(name+strings.Repeat("-rotated", rotations), func(t *testing.T) {
				f := newSSHWorkerCustodyFixture(t, legacy, rotations)
				got, err := f.join()
				if err != nil || len(got) != 1 || got[0].PodName != f.inventory.Pods[0].Metadata.Name || got[0].ServiceName != f.inventory.Services[0].Metadata.Name || got[0].PodUID != f.routes[0].PodUID {
					t.Fatal("actual current producer join rejected", err)
				}
				original := got[0]
				got[0].PodName = "changed"
				again, err := f.join()
				if err != nil || again[0] != original {
					t.Fatal("joined result aliases input")
				}
				f.endpoint["omitted_fixture_field"] = json.Number("9007199254740993")
				changed, err := f.join()
				if err != nil || changed[0].EndpointSHA256 == original.EndpointSHA256 {
					t.Fatal("full native endpoint drift lost")
				}
			})
		}
	}
	empty := &operatorSiteWorkerInventory{Version: 1, Pods: []operatorSiteWorkerInventoryPod{}, Services: []operatorSiteWorkerInventoryService{}}
	got, err := joinClusterSSHSiteWorkerCustody(clusterSSHSourceCohort{Members: []clusterSSHSourceMember{{Name: "engine-original"}}}, []map[string]any{}, empty, map[string][]map[string]any{}, []clusterSSHCurrentWorkerRoute{})
	if err != nil || got == nil || len(got) != 0 {
		t.Fatal("complete empty inventory rejected", err)
	}
}

func TestClusterSSHSiteWorkerCustodyRejectsIncompleteJoin(t *testing.T) {
	t.Setenv("BOREALIS_DEPLOYMENT_PROFILE_RANK", "1")
	t.Setenv("BOREALIS_CLUSTER_SIZING_RANK", "1")
	for _, mode := range []string{"candidate true", "candidate false", "candidate empty", "service candidate", "duplicate Pod", "duplicate Service", "orphan Service", "orphan Pod", "foreign node", "unassigned", "deleted Pod", "owned Pod", "deleted Service", "owned Service", "wrong selector", "old revision", "wrong service UID", "native Pod replacement", "native omitted drift", "not Running", "not Ready", "wrong Pod IP", "wrong container port", "numeric target", "wrong Service port", "route old UID", "route old name", "route old node", "route old host", "route wrong site", "route wrong worker", "route wrong port", "route synthetic generation", "route synthetic digest", "route missing", "route duplicate", "extra endpoints", "missing endpoints", "endpoint wrong UID", "endpoint wrong name", "endpoint wrong namespace", "endpoint wrong node", "endpoint old Service UID", "endpoint wrong owner", "endpoint duplicate target", "endpoint wrong port", "endpoint duplicate port", "endpoint not ready", "endpoint terminating", "endpoint not serving", "endpoint wrong address", "endpoint foreign Service", "endpoint wrong family", "endpoint deleted", "endpoint rounded"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHWorkerCustodyFixture(t, false, 1)
			pods := []map[string]any{f.pod}
			sets := map[string][]map[string]any{f.inventory.Services[0].Metadata.Name: {f.endpoint}}
			endpoint := f.endpoint["endpoints"].([]any)[0].(map[string]any)
			target, condition := clusterSSHStorageMap(endpoint, "targetRef"), clusterSSHStorageMap(endpoint, "conditions")
			meta := clusterSSHStorageMap(f.pod, "metadata")
			switch mode {
			case "candidate true", "candidate false", "candidate empty":
				value := strings.TrimPrefix(mode, "candidate ")
				if value == "empty" {
					value = ""
				}
				clusterSSHStorageMap(meta, "labels")["borealis.io/redeploy-agent-candidate"] = value
				f.refresh(t)
			case "service candidate":
				clusterSSHStorageMap(clusterSSHStorageMap(f.service, "metadata"), "labels")["borealis.io/redeploy-agent-candidate"] = "false"
				f.refresh(t)
			case "duplicate Pod":
				pods = append(pods, f.pod)
			case "duplicate Service":
				f.inventory.Services = append(f.inventory.Services, f.inventory.Services[0])
			case "orphan Service":
				pods = []map[string]any{}
				f.inventory.Pods = []operatorSiteWorkerInventoryPod{}
			case "orphan Pod":
				f.inventory.Services = []operatorSiteWorkerInventoryService{}
			case "foreign node":
				f.source.Members[0].Name = "other-node"
			case "unassigned":
				f.inventory.Pods[0].NodeName = nil
			case "deleted Pod":
				value := "2026-11-09T00:00:00Z"
				meta["deletionTimestamp"] = value
				f.refresh(t)
			case "owned Pod":
				meta["ownerReferences"] = []any{map[string]any{"kind": "Deployment"}}
				f.refresh(t)
			case "deleted Service":
				value := "2026-11-09T00:00:00Z"
				f.inventory.Services[0].Metadata.DeletionTimestamp = &value
			case "owned Service":
				f.inventory.Services[0].Metadata.HasOwnerReferences = true
			case "wrong selector":
				delete(f.inventory.Services[0].Selector, "borealis.io/worker-guid")
			case "old revision":
				f.inventory.Services[0].Selector["borealis.io/redeploy-agent-revision"] = "old-revision"
			case "wrong service UID":
				f.inventory.Services[0].Metadata.UID = newClusterUUID()
			case "native Pod replacement":
				meta["uid"] = newClusterUUID()
			case "native omitted drift":
				meta["omitted"] = "changed"
			case "not Running":
				clusterSSHStorageMap(f.pod, "status")["phase"] = "Succeeded"
				f.refresh(t)
			case "not Ready":
				clusterSSHStorageMap(f.pod, "status")["conditions"] = []any{}
				f.refresh(t)
			case "wrong Pod IP":
				clusterSSHStorageMap(f.pod, "status")["podIP"] = "10.42.0.8"
				f.refresh(t)
			case "wrong container port":
				clusterSSHStorageMap(f.pod, "spec")["containers"].([]any)[0].(map[string]any)["ports"].([]any)[0].(map[string]any)["containerPort"] = json.Number("56002")
				f.refresh(t)
			case "numeric target":
				f.inventory.Services[0].Ports[0].TargetPort = "56001"
			case "wrong Service port":
				f.inventory.Services[0].Ports[0].Port++
			case "route old UID":
				f.routes[0].PodUID = newClusterUUID()
			case "route old name":
				f.routes[0].PodName += "-old"
			case "route old node":
				f.routes[0].Node = "old-node"
			case "route old host":
				f.routes[0].Host = "10.43.0.8"
			case "route wrong site":
				f.routes[0].SiteID++
			case "route wrong worker":
				f.routes[0].Worker += "x"
			case "route wrong port":
				f.routes[0].DesktopPort++
			case "route synthetic generation":
				f.routes[0].Generation++
			case "route synthetic digest":
				f.routes[0].SHA256 = strings.Repeat("a", 64)
			case "route missing":
				f.routes = []clusterSSHCurrentWorkerRoute{}
			case "route duplicate":
				f.routes = append(f.routes, f.routes[0])
			case "extra endpoints":
				sets["orphan"] = []map[string]any{f.endpoint}
			case "missing endpoints":
				sets = map[string][]map[string]any{}
			case "endpoint wrong UID":
				target["uid"] = newClusterUUID()
			case "endpoint wrong name":
				target["name"] = "other-pod"
			case "endpoint wrong namespace":
				target["namespace"] = "other"
			case "endpoint wrong node":
				endpoint["nodeName"] = "other-node"
			case "endpoint old Service UID":
				clusterSSHStorageMap(f.endpoint, "metadata")["ownerReferences"].([]any)[0].(map[string]any)["uid"] = newClusterUUID()
			case "endpoint wrong owner":
				clusterSSHStorageMap(f.endpoint, "metadata")["ownerReferences"].([]any)[0].(map[string]any)["controller"] = false
			case "endpoint duplicate target":
				f.endpoint["endpoints"] = append(f.endpoint["endpoints"].([]any), endpoint)
			case "endpoint wrong port":
				f.endpoint["ports"].([]any)[0].(map[string]any)["port"] = json.Number("56002")
			case "endpoint duplicate port":
				f.endpoint["ports"].([]any)[1] = f.endpoint["ports"].([]any)[0]
			case "endpoint not ready":
				condition["ready"] = false
			case "endpoint terminating":
				condition["terminating"] = true
			case "endpoint not serving":
				condition["serving"] = false
			case "endpoint wrong address":
				endpoint["addresses"] = []any{"10.42.0.8"}
			case "endpoint foreign Service":
				clusterSSHStorageMap(clusterSSHStorageMap(f.endpoint, "metadata"), "labels")["kubernetes.io/service-name"] = "other-service"
			case "endpoint wrong family":
				f.endpoint["addressType"] = "FQDN"
			case "endpoint deleted":
				clusterSSHStorageMap(f.endpoint, "metadata")["deletionTimestamp"] = "2026-11-09T00:00:00Z"
			case "endpoint rounded":
				f.endpoint["omitted"] = float64(1)
			}
			if got, err := joinClusterSSHSiteWorkerCustody(f.source, pods, f.inventory, sets, f.routes); err == nil || got != nil {
				t.Fatal("incomplete current custody accepted", mode)
			}
		})
	}
}

func TestClusterSSHSiteWorkerCustodyExactNativeReceipt(t *testing.T) {
	t.Setenv("BOREALIS_DEPLOYMENT_PROFILE_RANK", "1")
	t.Setenv("BOREALIS_CLUSTER_SIZING_RANK", "1")
	for _, numbers := range [][2]string{{"9007199254740992", "9007199254740993"}, {"1", "1.00000000000000000001"}, {"1", "1e0"}} {
		t.Run(numbers[1], func(t *testing.T) {
			f := newSSHWorkerCustodyFixture(t, false, 0)
			f.pod["omitted"] = json.Number(numbers[0])
			f.refresh(t)
			if _, err := f.join(); err != nil {
				t.Fatal("identical exact native bytes rejected", err)
			}
			f.pod["omitted"] = json.Number(numbers[1])
			if _, err := f.join(); err == nil {
				t.Fatal("different exact native omitted value accepted")
			}
			// Legacy decode collapses every adversarial pair. It must never be
			// allowed to manufacture equality with exact operator evidence.
			raw, _ := json.Marshal(f.pod)
			var rounded map[string]any
			if err := json.Unmarshal(raw, &rounded); err != nil {
				t.Fatal(err)
			}
			other := operatorInventoryClone(t, f.pod)
			other["omitted"] = json.Number(numbers[0])
			otherRaw, _ := json.Marshal(other)
			var roundedOther map[string]any
			_ = json.Unmarshal(otherRaw, &roundedOther)
			if !reflect.DeepEqual(rounded, roundedOther) {
				t.Fatal("adversarial float64 collapse absent")
			}
			// Isolate the exact-receipt guard from selected port/type validation:
			// keep known fields exact while the omitted value alone is rounded.
			f.pod = other
			f.pod["omitted"] = rounded["omitted"]
			if _, err := f.join(); err == nil {
				t.Fatal("rounded omitted native value supplied exact receipt")
			}
			f.pod = rounded
			if _, err := f.join(); err == nil {
				t.Fatal("rounded native map supplied exact receipt")
			}
		})
	}
}

func TestClusterSSHSiteWorkerCustodyCurrentRouteCrossCheck(t *testing.T) {
	t.Setenv("BOREALIS_DEPLOYMENT_PROFILE_RANK", "1")
	t.Setenv("BOREALIS_CLUSTER_SIZING_RANK", "1")
	for _, mode := range []string{"same Pod new generation", "old Pod UID", "old Pod name", "old node", "other Service IP", "other Service port"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHWorkerCustodyFixture(t, false, 1)
			row := f.routes[0].observation
			var fields map[string]any
			decoder := json.NewDecoder(strings.NewReader(row.Metadata.String))
			decoder.UseNumber()
			if err := decoder.Decode(&fields); err != nil {
				t.Fatal(err)
			}
			placement := clusterSSHStorageMap(fields, schedulerWorkerPlacementKey)
			row.Generation.Int64 = 2
			placement["route_generation"] = json.Number("2")
			switch mode {
			case "old Pod UID":
				placement["pod_uid"] = newClusterUUID()
			case "old Pod name":
				row.WorkerPod.String = "site-worker-older-resident"
				row.RoutePod = row.WorkerPod
				placement["pod_name"] = row.WorkerPod.String
			case "old node":
				placement["node_name"] = "older-node"
			case "other Service IP":
				row.Host.String = "10.43.0.8"
				fields["upstream_host"] = row.Host.String
				clusterSSHStorageMap(fields, "remote_ops_socket")["host"] = row.Host.String
				clusterSSHStorageMap(fields, "remote_desktop_guacamole")["host"] = row.Host.String
			case "other Service port":
				row.Port.Int64++
				clusterSSHStorageMap(fields, "remote_ops_socket")["port"] = json.Number("56002")
			}
			raw, _ := json.Marshal(fields)
			row.Metadata.String = string(raw)
			route, ok := clusterSSHCurrentWorkerRouteFromRow(row)
			if !ok {
				t.Fatal("coherent current SQL fixture invalid")
			}
			f.routes[0] = route
			got, err := f.join()
			if mode == "same Pod new generation" {
				if err != nil || got[0].Generation != 2 {
					t.Fatal("current generation rejected", err)
				}
			} else if err == nil || got != nil {
				t.Fatal("current SQL route does not match native custody", mode)
			}
		})
	}
}
