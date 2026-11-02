package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// Exercise the actual operator producer, including its placement expression.
// Only API-assigned identity, scheduling and status are synthetic.
func sshSiteWorkerPodFixture(t *testing.T, node string, site int64) map[string]any {
	t.Helper()
	return sshSiteWorkerPodFixtureWithIdentity(t, node, site, fmt.Sprintf("Site %d", site), fmt.Sprintf("worker-%d", site))
}

func sshSiteWorkerPodFixtureWithIdentity(t *testing.T, node string, site int64, siteName, worker string) map[string]any {
	t.Helper()
	return sshSiteWorkerPodFixtureWithNames(t, node, site, siteName, worker, siteWorkerPodNameForSite(site), siteWorkerServiceNameForSite(site, siteName, worker))
}

func sshSiteWorkerPodFixtureWithNames(t *testing.T, node string, site int64, siteName, worker, podName, serviceName string) map[string]any {
	t.Helper()
	profile, _ := borealisOperatorSiteWorkerResourceProfile("small")
	o := borealisOperator{namespace: "borealis"}
	pod := o.siteWorkerPodManifest(podName, serviceName, "10.43.10.7", site, siteName, siteWorkerSiteSlug(site, siteName), worker, "borealis-engine/site-worker:sha-cccccccccccc", "small", profile, 56001, 61001)
	meta, spec := pod["metadata"].(map[string]any), pod["spec"].(map[string]any)
	meta["uid"], meta["resourceVersion"] = newClusterUUID(), "1"
	spec["nodeName"], spec["schedulerName"] = node, "default-scheduler"
	resources := spec["containers"].([]map[string]any)[0]["resources"]
	pod["status"] = map[string]any{
		"phase": "Running", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}},
		"containerStatuses":     []any{map[string]any{"name": "site-worker", "resources": resources, "ready": true, "started": true, "state": map[string]any{"running": map[string]any{"startedAt": "2026-10-11T00:00:00Z"}}}},
		"initContainerStatuses": []any{map[string]any{"name": "prepare-site-worker-logs", "resources": resources, "state": map[string]any{"terminated": map[string]any{"exitCode": 0, "reason": "Completed"}}}},
	}
	raw, err := json.Marshal(pod)
	var result map[string]any
	if err != nil || json.Unmarshal(raw, &result) != nil {
		t.Fatal("invalid fixture", err)
	}
	return result
}

func sshSiteWorkerResourceValue(node string) clusterSSHSiteWorkerResource {
	return clusterSSHSiteWorkerResource{Name: "site-worker-site-1", UID: newClusterUUID(), Node: node, Worker: "worker-1", SiteID: 1,
		Resources: clusterSSHApplicationResource{Name: "site-worker", CPURequestMilli: 25, CPULimitMilli: 250, MemoryRequestBytes: 96 << 20, MemoryLimitBytes: 256 << 20}}
}

func sshSiteWorkerPopulate(t *testing.T, f *sshStorageFixture) {
	t.Helper()
	for i, member := range f.a.Source.Members {
		f.objects[clusterSSHSiteWorkerPodsPath(member.Name)]["items"] = []any{sshSiteWorkerPodFixture(t, member.Name, int64(i+1))}
	}
}

func sshSiteWorkerPod(f *sshStorageFixture, member int) map[string]any {
	return f.objects[clusterSSHSiteWorkerPodsPath(f.a.Source.Members[member].Name)]["items"].([]any)[0].(map[string]any)
}

func TestClusterSSHSiteWorkerServiceProducerNames(t *testing.T) {
	for _, test := range []struct {
		name, pod, service string
		valid              bool
	}{
		{"legacy", "site-worker-original-site", "site-worker-original-site", true},
		{"current", "site-worker-site-7", "site-worker-site-7", true},
		{"mixed_legacy_current", "site-worker-original-site", "site-worker-site-7", false},
		{"mixed_current_legacy", "site-worker-site-7", "site-worker-original-site", false},
		{"other_site", "site-worker-site-7", "site-worker-site-8", false},
		{"other_legacy_name", "site-worker-site-7", "site-worker-other-site", false},
		{"arbitrary_name", "other-pod", "other-service", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			node := f.a.Source.Members[0].Name
			pod := sshSiteWorkerPodFixtureWithNames(t, node, 7, "Original Site", schedulerK3sSiteWorkerGUID(7), test.pod, test.service)
			f.objects[clusterSSHSiteWorkerPodsPath(node)]["items"] = []any{pod}
			got, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			if test.valid {
				if err != nil || len(got.Requirements.SiteWorkers) != 1 || got.Requirements.SiteWorkers[0].Name != test.pod {
					t.Fatal("exact retained/current producer rejected", err)
				}
			} else if err == nil {
				t.Fatal("foreign Service accepted as producer evidence")
			}
		})
	}
}

func TestClusterSSHSiteWorkerSiteUniqueness(t *testing.T) {
	for _, separateNodes := range []bool{false, true} {
		for _, duplicateSite := range []bool{false, true} {
			t.Run(fmt.Sprintf("separate_nodes=%t/duplicate_site=%t", separateNodes, duplicateSite), func(t *testing.T) {
				f := newSSHStorageFixture(t, separateNodes)
				firstNode, secondNode := f.a.Source.Members[0].Name, f.a.Source.Members[0].Name
				if separateNodes {
					secondNode = f.a.Source.Members[1].Name
				}
				secondSite := int64(2)
				if duplicateSite {
					secondSite = 1
				}
				// A renamed site can leave two individually canonical Pod names.
				// Keep name, UID and worker GUID distinct to isolate SiteID reuse.
				first := sshSiteWorkerPodFixtureWithNames(t, firstNode, 1, "Original site", "worker-first", "site-worker-original-site", "site-worker-original-site")
				second := sshSiteWorkerPodFixtureWithNames(t, secondNode, secondSite, "Renamed site", "worker-second", "site-worker-renamed-site", "site-worker-renamed-site")
				firstPath, secondPath := clusterSSHSiteWorkerPodsPath(firstNode), clusterSSHSiteWorkerPodsPath(secondNode)
				f.objects[firstPath]["items"] = []any{first}
				if separateNodes {
					f.objects[secondPath]["items"] = []any{second}
				} else {
					f.objects[firstPath]["items"] = []any{first, second}
				}
				got, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
				if duplicateSite {
					if err == nil {
						t.Fatal("accepted distinct workers for the same site")
					}
					return
				}
				if err != nil || len(got.Requirements.SiteWorkers) != 2 {
					t.Fatal("rejected distinct-site workers", err)
				}
				cpu, memory, ok := clusterSSHSiteWorkerBudget(got.Requirements.SiteWorkers)
				if !ok || cpu != 50 || memory != 512<<20 {
					t.Fatal("changed distinct-site worker sum", cpu, memory, ok)
				}
			})
		}
	}
}

func TestClusterSSHSiteWorkerResources(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "empty", "empty member", "canonical units", "ordering", "denied", "missing list", "pagination", "too many", "duplicate UID", "duplicate worker", "foreign manager", "foreign namespace", "owned by controller", "name mismatch", "site malformed", "worker malformed", "wrong node", "deleting", "missing affinity", "extra affinity", "extra expression", "inactive placement", "node selector", "spread", "scheduler", "missing main bounds", "missing init bounds", "init drift", "extra container", "restartable init", "pod resources", "overhead", "resize", "resize condition", "runtime drift", "allocation drift", "pending", "unsupported quantity"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, mode == "replacement" || mode == "empty member" || strings.HasPrefix(mode, "duplicate"))
			sshSiteWorkerPopulate(t, f)
			pod := sshSiteWorkerPod(f, 0)
			path := clusterSSHSiteWorkerPodsPath(f.a.Source.Members[0].Name)
			list := f.objects[path]
			meta, spec, status := clusterSSHStorageMap(pod, "metadata"), clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
			labels := clusterSSHStorageMap(meta, "labels")
			main := spec["containers"].([]any)[0].(map[string]any)
			init := spec["initContainers"].([]any)[0].(map[string]any)
			terms := clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(spec, "affinity"), "nodeAffinity"), "requiredDuringSchedulingIgnoredDuringExecution")["nodeSelectorTerms"].([]any)
			expressions := terms[0].(map[string]any)["matchExpressions"].([]any)
			switch mode {
			case "empty":
				list["items"] = []any{}
			case "empty member":
				f.objects[clusterSSHSiteWorkerPodsPath(f.a.Source.Members[1].Name)]["items"] = []any{}
			case "canonical units":
				clusterSSHStorageMap(clusterSSHStorageMap(main, "resources"), "requests")["cpu"] = "0.025"
			case "ordering":
				list["items"] = []any{sshSiteWorkerPodFixture(t, f.a.Source.Members[0].Name, 2), pod}
			case "missing list":
				delete(f.objects, path)
			case "pagination":
				clusterSSHStorageMap(list, "metadata")["continue"] = "more"
			case "too many":
				for len(list["items"].([]any)) <= clusterSSHSiteWorkerLimit {
					list["items"] = append(list["items"].([]any), pod)
				}
			case "duplicate UID":
				clusterSSHStorageMap(sshSiteWorkerPod(f, 1), "metadata")["uid"] = meta["uid"]
			case "duplicate worker":
				clusterSSHStorageMap(clusterSSHStorageMap(sshSiteWorkerPod(f, 1), "metadata"), "labels")["borealis.io/worker-guid"] = labels["borealis.io/worker-guid"]
			case "foreign manager":
				labels["app.kubernetes.io/managed-by"] = "other"
			case "foreign namespace":
				meta["namespace"] = "other"
			case "owned by controller":
				meta["ownerReferences"] = []any{map[string]any{"kind": "ReplicaSet"}}
			case "name mismatch":
				meta["name"] = "site-worker-other"
			case "site malformed":
				labels["borealis.io/site-id"] = "01"
			case "worker malformed":
				labels["borealis.io/worker-guid"] = "../other"
			case "wrong node":
				spec["nodeName"] = "failed-member"
			case "deleting":
				meta["deletionTimestamp"] = "2026-10-11T00:00:00Z"
			case "missing affinity":
				delete(spec, "affinity")
			case "extra affinity":
				clusterSSHStorageMap(spec, "affinity")["podAffinity"] = map[string]any{}
			case "extra expression":
				terms[0].(map[string]any)["matchExpressions"] = append(expressions, expressions[0])
			case "inactive placement":
				expressions[0].(map[string]any)["values"] = []any{"candidate"}
			case "node selector":
				spec["nodeSelector"] = map[string]any{"kubernetes.io/hostname": "other"}
			case "spread":
				spec["topologySpreadConstraints"] = []any{map[string]any{}}
			case "scheduler":
				spec["schedulerName"] = "custom"
			case "missing main bounds":
				delete(main, "resources")
			case "missing init bounds":
				delete(init, "resources")
			case "init drift":
				clusterSSHStorageMap(clusterSSHStorageMap(init, "resources"), "limits")["memory"] = "1Gi"
			case "extra container":
				spec["containers"] = append(spec["containers"].([]any), main)
			case "restartable init":
				init["restartPolicy"] = "Always"
			case "pod resources":
				spec["resources"] = main["resources"]
			case "overhead":
				spec["overhead"] = map[string]any{"memory": "1Gi"}
			case "resize":
				status["resize"] = "InProgress"
			case "resize condition":
				status["conditions"] = append(status["conditions"].([]any), map[string]any{"type": "PodResizePending", "status": "True"})
			case "runtime drift":
				clusterSSHStorageMap(clusterSSHStorageMap(status["containerStatuses"].([]any)[0].(map[string]any), "resources"), "limits")["memory"] = "1Gi"
			case "allocation drift":
				status["containerStatuses"].([]any)[0].(map[string]any)["allocatedResources"] = map[string]any{"cpu": "1", "memory": "96Mi"}
			case "pending":
				status["phase"] = "Pending"
			case "unsupported quantity":
				clusterSSHStorageMap(clusterSSHStorageMap(main, "resources"), "requests")["cpu"] = "0.0001"
			}
			get := func(ctx context.Context, p string, out any) error {
				if mode == "denied" && p == path {
					return fmt.Errorf("denied")
				}
				return f.get(ctx, p, out)
			}
			got, err := observeClusterSSHStorage(context.Background(), f.a.Source, get)
			valid := slices.Contains([]string{"expansion", "replacement", "empty", "empty member", "canonical units", "ordering"}, mode)
			if (err == nil) != valid {
				t.Fatal("unsafe worker observation", err)
			}
			if valid {
				count := 1
				if mode == "replacement" || mode == "ordering" {
					count = 2
				} else if mode == "empty" {
					count = 0
				}
				cpu, memory, ok := clusterSSHNodeWorkloadBudget(got.Requirements)
				if !ok || cpu != 2240+25+155+45+50+120+160+200+240+95+uint64(len(f.a.Source.Members))*100+uint64(count)*25 || memory != (5<<30+1024<<20+128<<20+216<<20+120<<20+112<<20+288<<20+384<<20+480<<20+576<<20+168<<20+128<<20)+uint64(len(f.a.Source.Members))*(128<<20)+uint64(count)*(256<<20) || len(got.Requirements.SiteWorkers) != count {
					t.Fatal("incorrect combined budget", cpu, memory, count, ok)
				}
				for p := range f.calls {
					if clusterSSHSiteWorkerPodsPathValid(p) && !slices.ContainsFunc(f.a.Source.Members, func(m clusterSSHSourceMember) bool { return p == clusterSSHSiteWorkerPodsPath(m.Name) }) {
						t.Fatal("read failed member or candidate", p)
					}
				}
			}
		})
	}
}

func TestClusterSSHSiteWorkerBudgetAndSource(t *testing.T) {
	f := newSSHStorageFixture(t, true)
	sshSiteWorkerPopulate(t, f)
	observed, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"missing", "order", "UID", "worker", "name", "site", "duplicate site", "node", "resource name", "resource request", "CPU overflow", "memory overflow"} {
		t.Run(mode, func(t *testing.T) {
			v := slices.Clone(observed.Requirements.SiteWorkers)
			switch mode {
			case "missing":
				v = nil
			case "order":
				v[0], v[1] = v[1], v[0]
			case "UID":
				v[1].UID = v[0].UID
			case "worker":
				v[1].Worker = v[0].Worker
			case "name":
				v[0].Name = "other"
			case "site":
				v[0].SiteID = -1
			case "duplicate site":
				v[1].SiteID = v[0].SiteID
			case "node":
				v[0].Node = "failed-member"
			case "resource name":
				v[0].Resources.Name = "other"
			case "resource request":
				v[0].Resources.CPURequestMilli = v[0].Resources.CPULimitMilli + 1
			case "CPU overflow":
				v[0].Resources.CPURequestMilli, v[0].Resources.CPULimitMilli = clusterSSHCapacityLimit, clusterSSHCapacityLimit
			case "memory overflow":
				v[0].Resources.MemoryLimitBytes = clusterSSHCapacityLimit
			}
			if clusterSSHSiteWorkersMatchSource(v, f.a.Source) {
				t.Fatal("invalid projection accepted")
			}
		})
	}
}

func TestClusterSSHSiteWorkerAggregateBound(t *testing.T) {
	f := newSSHStorageFixture(t, true)
	for i, member := range f.a.Source.Members {
		items := []any{}
		// Neither individual list exceeds its bound. Combined inventory does.
		for j := 0; j < clusterSSHSiteWorkerLimit/2+i; j++ {
			items = append(items, sshSiteWorkerPodFixture(t, member.Name, int64(1+j+i*clusterSSHSiteWorkerLimit)))
		}
		f.objects[clusterSSHSiteWorkerPodsPath(member.Name)]["items"] = items
	}
	if _, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get); err == nil {
		t.Fatal("accepted oversized aggregate worker inventory")
	}
}

func TestClusterSSHSiteWorkerResourcesBoundToScope(t *testing.T) {
	for _, mode := range []string{"unchanged", "resource", "identity", "receipt", "added", "removed"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, true)
			sshSiteWorkerPopulate(t, f)
			entered := false
			err := withClusterSSHSourceStorage(context.Background(), f.authority, f.get, func(_ context.Context, value clusterSSHStorageRequirements, _ clusterSSHPreparationChecks) error {
				entered = true
				value.SiteWorkers[0].Resources.MemoryLimitBytes = 1 // Consumer cannot change retained values.
				f.mu.Lock()
				defer f.mu.Unlock()
				pod := sshSiteWorkerPod(f, 0)
				list := f.objects[clusterSSHSiteWorkerPodsPath(f.a.Source.Members[0].Name)]
				switch mode {
				case "resource":
					for _, group := range []struct{ part, key string }{{"spec", "containers"}, {"spec", "initContainers"}, {"status", "containerStatuses"}, {"status", "initContainerStatuses"}} {
						c := clusterSSHStorageMap(pod, group.part)[group.key].([]any)[0].(map[string]any)
						clusterSSHStorageMap(clusterSSHStorageMap(c, "resources"), "limits")["cpu"] = "1"
					}
				case "identity":
					clusterSSHStorageMap(pod, "metadata")["uid"] = newClusterUUID()
				case "receipt":
					clusterSSHStorageMap(pod, "metadata")["resourceVersion"] = "2"
				case "added":
					list["items"] = append(list["items"].([]any), sshSiteWorkerPodFixture(t, f.a.Source.Members[0].Name, 3))
				case "removed":
					list["items"] = []any{}
				}
				return nil
			})
			if !entered || (err == nil) != (mode == "unchanged") {
				t.Fatal("unsafe worker scope", entered, err)
			}
			if _, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get); err != nil {
				t.Fatal("changed fixture not independently valid", err)
			}
		})
	}
}
