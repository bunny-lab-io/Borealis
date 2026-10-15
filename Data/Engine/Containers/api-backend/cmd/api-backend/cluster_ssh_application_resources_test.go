package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func sshApplicationResourcesFixture(f *sshStorageFixture) {
	for _, name := range clusterSSHApplicationDeployments {
		v := sshStorageObject("apps/v1", "Deployment", "borealis", name)
		v["spec"] = map[string]any{"replicas": 0, "template": map[string]any{"spec": map[string]any{"containers": []any{
			map[string]any{"name": name, "resources": map[string]any{
				"requests": map[string]any{"cpu": "100m", "memory": "64Mi"},
				"limits":   map[string]any{"cpu": "4", "memory": "128Mi"},
			}},
		}}}}
		f.objects[clusterSSHApplicationDeploymentPrefix+name] = v
	}
}

func sshApplicationSpec(f *sshStorageFixture) map[string]any {
	return clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(f.objects[clusterSSHApplicationDeploymentPrefix+"api-backend"], "spec"), "template"), "spec")
}

func sshApplicationResourceValues(t *testing.T) []clusterSSHApplicationResource {
	t.Helper()
	f := &sshStorageFixture{objects: map[string]map[string]any{}}
	sshApplicationResourcesFixture(f)
	values, err := observeClusterSSHApplicationResources(func(path string) (map[string]any, error) { return f.objects[path], nil })
	if err != nil {
		t.Fatal(err)
	}
	return values
}

func TestClusterSSHApplicationResources(t *testing.T) {
	for _, mode := range []string{"valid", "missing template", "wrong identity", "duplicate uid", "missing requests", "missing limit", "request exceeds cap", "CPU precision", "CPU zero", "memory overflow", "unsupported resource", "extra container", "init", "pod resources", "overhead", "runtime class", "claims"} {
		t.Run(mode, func(t *testing.T) {
			f := &sshStorageFixture{objects: map[string]map[string]any{}}
			sshApplicationResourcesFixture(f)
			spec := sshApplicationSpec(f)
			c := spec["containers"].([]any)[0].(map[string]any)
			r := clusterSSHStorageMap(c, "resources")
			switch mode {
			case "missing template":
				delete(f.objects, clusterSSHApplicationDeploymentPrefix+"webui-frontend")
			case "wrong identity":
				clusterSSHStorageMap(f.objects[clusterSSHApplicationDeploymentPrefix+"api-backend"], "metadata")["namespace"] = "other"
			case "duplicate uid":
				clusterSSHStorageMap(f.objects[clusterSSHApplicationDeploymentPrefix+"webui-frontend"], "metadata")["uid"] = clusterSSHStorageMap(f.objects[clusterSSHApplicationDeploymentPrefix+"api-backend"], "metadata")["uid"]
			case "missing requests":
				delete(r, "requests")
			case "missing limit":
				delete(clusterSSHStorageMap(r, "limits"), "cpu")
			case "request exceeds cap":
				clusterSSHStorageMap(r, "requests")["memory"] = "129Mi"
			case "CPU precision":
				clusterSSHStorageMap(r, "requests")["cpu"] = "0.0001"
			case "CPU zero":
				clusterSSHStorageMap(r, "limits")["cpu"] = "0"
			case "memory overflow":
				clusterSSHStorageMap(r, "limits")["memory"] = "9007199254740992"
			case "unsupported resource":
				clusterSSHStorageMap(r, "requests")["nvidia.com/gpu"] = "1"
			case "extra container":
				spec["containers"] = append(spec["containers"].([]any), c)
			case "init":
				spec["initContainers"] = []any{c}
			case "pod resources":
				spec["resources"] = r
			case "overhead":
				spec["overhead"] = map[string]any{"cpu": "1"}
			case "runtime class":
				spec["runtimeClassName"] = "other"
			case "claims":
				spec["resourceClaims"] = []any{map[string]any{"name": "gpu"}}
			}
			values, err := observeClusterSSHApplicationResources(func(path string) (map[string]any, error) {
				if !clusterSSHApplicationDeploymentPathValid(path) {
					t.Fatal("unscoped template read", path)
				}
				return f.objects[path], nil
			})
			if mode != "valid" {
				if err == nil {
					t.Fatal("unsupported demand accepted")
				}
				return
			}
			cpu, memory, ok := clusterSSHApplicationBudget(values)
			if err != nil || !ok || cpu != 800 || memory != 1<<30 {
				t.Fatal("wrong one-per-node subtotal", cpu, memory, err)
			}
			for _, bad := range []string{"", "other", "api-backend/status", "api-backend?watch=true", "../api-backend", "api-backend-candidate"} {
				if clusterSSHApplicationDeploymentPathValid(clusterSSHApplicationDeploymentPrefix + bad) {
					t.Fatal("unscoped template allowed")
				}
			}
		})
	}
}

func TestClusterSSHApplicationResourceQuantitiesAndOverflow(t *testing.T) {
	for value, want := range map[string]uint64{"1": 1000, "0.001": 1, "0.250": 250, "1.234": 1234, "100m": 100, "999999999": 999999999000} {
		if got, ok := clusterSSHWorkloadMilliCPU(value); !ok || got != want {
			t.Fatal(value, got)
		}
	}
	for _, value := range []any{nil, 1, "", "0", "0m", "-1", "01", "1e3", "0.1m", "1.0001", "1 ", "1M", "1000000000"} {
		if _, ok := clusterSSHWorkloadMilliCPU(value); ok {
			t.Fatal("invalid CPU accepted", value)
		}
	}
	for value, want := range map[string]uint64{"1": 1, "1Ki": 1024, "1Mi": 1 << 20, "1Gi": 1 << 30, "1Ti": 1 << 40, "1k": 1000, "1M": 1000000, "1G": 1000000000, "1T": 1000000000000} {
		if got, ok := clusterSSHWorkloadMemory(value); !ok || got != want {
			t.Fatal(value, got)
		}
	}
	for _, value := range []any{nil, 1, "", "0", "1m", "1K", "1g", "1.5Gi", "1e9", "-1", "9007199254740992", "9999999999999999T"} {
		if _, ok := clusterSSHWorkloadMemory(value); ok {
			t.Fatal("invalid memory accepted", value)
		}
	}
	values := sshApplicationResourceValues(t)
	for _, mode := range []string{"missing", "extra", "order", "cpu overflow", "memory overflow", "invalid cap"} {
		v := slices.Clone(values)
		switch mode {
		case "missing":
			v = v[:len(v)-1]
		case "extra":
			v = append(v, v[0])
		case "order":
			v[0], v[1] = v[1], v[0]
		case "cpu overflow":
			v[0].CPURequestMilli, v[0].CPULimitMilli = clusterSSHCapacityLimit, clusterSSHCapacityLimit
		case "memory overflow":
			v[0].MemoryLimitBytes = clusterSSHCapacityLimit
		case "invalid cap":
			v[0].CPULimitMilli = 1
		}
		if _, _, ok := clusterSSHApplicationBudget(v); ok {
			t.Fatal("invalid subtotal", mode)
		}
	}
}

func TestClusterSSHApplicationRendererPlacementParity(t *testing.T) {
	// Parse only literal inventory; never import/run reconciliation or kubectl.
	out, err := exec.Command("python3", "-c", `import ast,json,pathlib
p=pathlib.Path('../../../../../../Data/Engine/K3s/cluster/reconcile-node-workloads.py')
t=ast.parse(p.read_text())
v=[ast.literal_eval(n.value) for n in t.body if isinstance(n,ast.Assign) and any(isinstance(x,ast.Name) and x.id=='DEPLOYMENT_SERVICES' for x in n.targets)]
assert len(v)==1
f=next(n for n in t.body if isinstance(n,ast.FunctionDef) and n.name=='reconcile_one')
r=[n.value for n in f.body if isinstance(n,ast.Assign) and any(isinstance(x,ast.Subscript) and isinstance(x.value,ast.Name) and x.value.id=='spec' and isinstance(x.slice,ast.Constant) and x.slice.value=='replicas' for x in n.targets)]
assert len(r)==1 and isinstance(r[0],ast.IfExp)
assert ast.unparse(r[0].test)=='base in HOST_PORT_DEPLOYMENTS and candidate'
assert ast.literal_eval(r[0].body)==0 and ast.literal_eval(r[0].orelse)==1
print(json.dumps(sorted(v[0])))`).Output()
	var names []string
	if err != nil || json.Unmarshal(out, &names) != nil || !slices.Equal(names, clusterSSHApplicationDeployments) {
		t.Fatal("renderer deployment inventory drift", err, string(out))
	}
}

func TestClusterSSHTargetApplicationSizing(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "webhook missing", "webhook before", "webhook after", "webhook CPU fit", "webhook memory fit", "webhook CPU cap overcommit", "cert-manager missing", "cert-manager before", "cert-manager after", "cert-manager CPU fit", "cert-manager memory fit", "cert-manager CPU cap overcommit", "snapshot missing", "snapshot before", "snapshot after", "snapshot CPU fit", "snapshot memory fit", "snapshot CPU cap overcommit", "upgrade missing", "upgrade before", "upgrade after", "upgrade CPU fit", "upgrade memory fit", "upgrade CPU cap overcommit", "operator missing", "operator before", "operator after", "operator CPU fit", "operator memory fit", "operator CPU cap overcommit", "census missing", "census before", "census after", "recovery CPU fit", "recovery memory fit", "policy before", "policy after", "sibling CPU", "sibling memory", "workers missing", "workers before", "workers after", "worker CPU fit", "worker memory fit", "worker CPU cap overcommit", "missing", "drift before", "drift after", "postgres missing", "postgres before", "postgres after", "receipt drift", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHNetworkTargetsFixture(t, mode == "replacement", "peer")
			values := sshApplicationResourceValues(t)
			for i := range values {
				values[i].CPURequestMilli = 1750
				values[i].MemoryLimitBytes = 7 << 29
			}
			operator := sshCNPGResourceValue(t)
			upgrade := sshSystemUpgradeResourceValue(t)
			webhook := sshCertManagerResourceValue(t, "cert-manager-webhook")
			values[0].CPURequestMilli -= 40
			values[0].MemoryLimitBytes -= 96 << 20
			certManager := sshCertManagerResourceValue(t, "cert-manager")
			values[0].CPURequestMilli -= 100
			values[0].MemoryLimitBytes -= 128 << 20
			snapshotResources := sshSnapshotResourceValue(t)
			values[0].CPURequestMilli -= 40
			values[0].MemoryLimitBytes -= 256 << 20
			values[0].CPURequestMilli -= 100
			values[0].MemoryLimitBytes -= 128 << 20
			values[0].CPURequestMilli -= 200
			values[0].MemoryLimitBytes -= 400 << 20
			workers := []clusterSSHSiteWorkerResource{sshSiteWorkerResourceValue(f.a.Source.Members[0].Name)}
			values[0].CPURequestMilli -= workers[0].Resources.CPURequestMilli
			values[0].MemoryLimitBytes -= workers[0].Resources.MemoryLimitBytes
			f.hostWire = func(i int, wire string) string {
				if i == 1 && mode == "sibling CPU" {
					return strings.Replace(wire, "cpu_count=16", "cpu_count=15", 1)
				}
				if i == 1 && mode == "sibling memory" {
					return strings.Replace(wire, "memory_kib=33554432", "memory_kib=33554431", 1)
				}
				return wire
			}
			postgres := sshPostgresResourceValue(t)
			postgres.CPURequestMilli = 2000
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			settings := sshPreparationRuntimeFixture()
			settings["BOREALIS_POSTGRES_DB_MEMORY_LIMIT"] = "4g"
			reads, consumed := 0, false
			source := func(context.Context) (clusterSSHPreparationSnapshot, error) {
				reads++
				if mode == "webhook missing" {
					webhook = clusterSSHCertManagerResources{}
				}
				if (mode == "webhook before" && reads == 2) || (mode == "webhook after" && consumed) {
					webhook.Envelope.CPULimitMilli++
				}
				if mode == "webhook CPU fit" {
					webhook.Envelope.CPURequestMilli = 21
				}
				if mode == "webhook memory fit" {
					webhook.Envelope.MemoryLimitBytes++
				}
				if mode == "webhook CPU cap overcommit" {
					webhook.Envelope.CPULimitMilli = 64000
				}
				if mode == "cert-manager missing" {
					certManager = clusterSSHCertManagerResources{}
				}
				if (mode == "cert-manager before" && reads == 2) || (mode == "cert-manager after" && consumed) {
					certManager.Envelope.CPULimitMilli++
				}
				if mode == "cert-manager CPU fit" {
					certManager.Envelope.CPURequestMilli = 51
				}
				if mode == "cert-manager memory fit" {
					certManager.Envelope.MemoryLimitBytes++
				}
				if mode == "cert-manager CPU cap overcommit" {
					certManager.Envelope.CPULimitMilli = 64000
				}
				if mode == "snapshot missing" {
					snapshotResources = clusterSSHSnapshotResources{}
				}
				if (mode == "snapshot before" && reads == 2) || (mode == "snapshot after" && consumed) {
					snapshotResources.Envelope.CPULimitMilli++
				}
				if mode == "snapshot CPU fit" {
					snapshotResources.Envelope.CPURequestMilli = 21
				}
				if mode == "snapshot memory fit" {
					snapshotResources.Envelope.MemoryLimitBytes++
				}
				if mode == "snapshot CPU cap overcommit" {
					snapshotResources.Envelope.CPULimitMilli = 64000
				}
				if mode == "upgrade missing" {
					upgrade = clusterSSHSystemUpgradeResources{}
				}
				if (mode == "upgrade before" && reads == 2) || (mode == "upgrade after" && consumed) {
					upgrade.Envelope.CPULimitMilli++
				}
				if mode == "upgrade CPU fit" {
					upgrade.Envelope.CPURequestMilli = 101
				}
				if mode == "upgrade memory fit" {
					upgrade.Envelope.MemoryLimitBytes++
				}
				if mode == "upgrade CPU cap overcommit" {
					upgrade.Envelope.CPULimitMilli = 64000
				}
				if mode == "operator missing" {
					operator = clusterSSHCNPGOperatorResources{}
				}
				if (mode == "operator before" && reads == 2) || (mode == "operator after" && consumed) {
					operator.Envelope.CPULimitMilli++
				}
				if mode == "operator CPU fit" {
					operator.Envelope.CPURequestMilli = 101
					operator.Envelope.CPULimitMilli = 101
				}
				if mode == "operator CPU cap overcommit" {
					operator.Envelope.CPULimitMilli = 64000
				}
				if mode == "operator memory fit" {
					operator.Envelope.MemoryLimitBytes = (200 << 20) + 1
				}

				e, err := buildClusterSSHPreparationExpected(f.a.Cohort, f.a.Source, f.a.Lease, f.a.Baseline, f.a.K3sVersion, f.sources[0].PodCIDR, f.sources[0].ServiceCIDR)
				if (mode == "drift before" && reads == 2) || (mode == "drift after" && consumed) {
					values[0].CPULimitMilli = 5000
				}
				if (mode == "postgres before" && reads == 2) || (mode == "postgres after" && consumed) {
					postgres.CPULimitMilli++
				}
				if mode == "postgres missing" {
					postgres = clusterSSHApplicationResource{}
				}
				if mode == "workers missing" {
					workers = nil
				}
				if (mode == "workers before" && reads == 2) || (mode == "workers after" && consumed) {
					workers[0].Resources.CPULimitMilli++
				}
				if mode == "worker CPU cap overcommit" {
					workers[0].Resources.CPULimitMilli = 64000
				}
				if mode == "worker CPU fit" {
					workers[0].Resources.CPURequestMilli = 26
				}
				if mode == "worker memory fit" {
					workers[0].Resources.MemoryLimitBytes = (256 << 20) + 1
				}
				observation := strings.Repeat("a", 64)
				if mode == "receipt drift" && reads == 2 {
					observation = strings.Repeat("b", 64)
				}
				if mode == "missing" {
					values = nil
				}
				sites := []int64{}
				policy := clusterSSHWorkerPolicy{Profile: "standard", OnlineWindow: 300}
				if mode == "census missing" {
					sites = nil
				}
				if (mode == "census before" && reads == 2) || (mode == "census after" && consumed) {
					sites = []int64{2}
				}
				if (mode == "policy before" && reads == 2) || (mode == "policy after" && consumed) {
					policy.Profile = "large"
				}
				if mode == "recovery CPU fit" || mode == "recovery memory fit" {
					sites = []int64{2}
					if mode == "recovery CPU fit" {
						values[0].MemoryLimitBytes -= 512 << 20
					} else {
						values[0].CPURequestMilli -= 50
					}
				}
				return clusterSSHPreparationSnapshot{DesiredWorkerSites: sites, Expected: e, Settings: settings, Storage: clusterSSHStorageSnapshot{Observation: observation, Requirements: clusterSSHStorageRequirements{CertManagerWebhookResources: webhook, CertManagerResources: certManager, SnapshotResources: snapshotResources, SystemUpgradeResources: upgrade, CNPGOperatorResources: operator, WorkerPolicy: policy, SiteWorkers: workers, Applications: values, PostgresResources: postgres}}}, err
			}
			err := runClusterSSHNetworkTargets(ctx, f.a.Baseline, f.claims, f.deps, func(ctx context.Context, readers clusterSSHNetworkTargetReaders) error {
				return withClusterSSHTargetApplicationSizing(ctx, readers, source, func(context.Context, []clusterSSHTargetHostEvidence) error {
					consumed = true
					if mode == "cancel" {
						cancel()
					}
					return nil
				})
			})
			ok := mode == "webhook CPU cap overcommit" || mode == "cert-manager CPU cap overcommit" || mode == "snapshot CPU cap overcommit" || mode == "upgrade CPU cap overcommit" || mode == "expansion" || mode == "replacement" || mode == "worker CPU cap overcommit" || mode == "operator CPU cap overcommit"
			if (err == nil) != ok || consumed != (ok || mode == "webhook after" || mode == "cert-manager after" || mode == "snapshot after" || mode == "upgrade after" || mode == "operator after" || mode == "census after" || mode == "policy after" || mode == "drift after" || mode == "postgres after" || mode == "workers after" || mode == "cancel") {
				t.Fatal("unsafe application sizing boundary", err, consumed)
			}
			if ok && reads < 5 {
				t.Fatal("source not rechecked")
			}
			f.assertClosed(t)
		})
	}
}
