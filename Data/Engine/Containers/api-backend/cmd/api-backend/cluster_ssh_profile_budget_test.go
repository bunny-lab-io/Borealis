package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func sshSmallBusinessSettings() map[string]string {
	settings := sshPreparationRuntimeFixture()
	settings["BOREALIS_CLUSTER_SIZING_RANK"] = "1"
	settings["BOREALIS_CLUSTER_SIZING_MEMORY_MIB"] = "16384"
	settings["BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY"] = "8"
	for service, limits := range map[string][2]string{
		"API_BACKEND": {"1g", "2.00"}, "JOB_SCHEDULER": {"512m", "1.50"},
		"WEBUI_FRONTEND": {"512m", "1.00"}, "TRAEFIK_EDGE": {"512m", "1.50"},
		"POSTGRES_DB": {"6144m", "2.00"}, "REMOTE_DESKTOP_GUACD": {"512m", "1.00"},
		"WIREGUARD_TUNNEL": {"256m", "1.00"},
	} {
		settings["BOREALIS_"+service+"_MEMORY_LIMIT"] = limits[0]
		settings["BOREALIS_"+service+"_CPU_LIMIT"] = limits[1]
	}
	return settings
}

func TestClusterSSHTargetProfileSizing(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "missing cap", "override", "other profile", "template mismatch", "postgres mismatch", "missing infrastructure", "source identity", "settings drift before", "settings drift after", "template drift before", "template drift after", "receipt drift", "unrelated setting drift", "nil source"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHNetworkTargetsFixture(t, mode == "replacement", "peer")
			snapshot := clusterSSHPreparationSnapshot{Settings: sshSmallBusinessSettings(), Storage: sshStorageSnapshotFixture(t, f.a), DesiredWorkerSites: []int64{}}
			snapshot.Storage.Requirements.Applications = clusterSSHSmallBusinessApplications()
			snapshot.Storage.Requirements.PostgresResources = clusterSSHSmallBusinessPostgres()
			if cpu, memory, valid := clusterSSHDesiredWorkerWorkloadBudget(snapshot); !valid {
				t.Fatalf("incomplete baseline workload fixture: %d %d", cpu, memory)
			}
			consumed, reads := false, 0
			var source clusterSSHPreparationSnapshotRead = func(context.Context) (clusterSSHPreparationSnapshot, error) {
				reads++
				e, err := buildClusterSSHPreparationExpected(f.a.Cohort, f.a.Source, f.a.Lease, f.a.Baseline, f.a.K3sVersion, f.sources[0].PodCIDR, f.sources[0].ServiceCIDR)
				snapshot.Expected = e
				switch mode {
				case "missing cap":
					delete(snapshot.Settings, "BOREALIS_API_BACKEND_MEMORY_LIMIT")
				case "override":
					snapshot.Settings["BOREALIS_API_BACKEND_MEMORY_LIMIT"] = "2g"
				case "other profile":
					snapshot.Settings["BOREALIS_CLUSTER_SIZING_RANK"] = "0"
				case "template mismatch":
					snapshot.Storage.Requirements.Applications[0].CPURequestMilli++
				case "postgres mismatch":
					snapshot.Storage.Requirements.PostgresResources.MemoryLimitBytes++
				case "missing infrastructure":
					snapshot.Storage.Requirements.LonghornInstanceManagerResources = clusterSSHInstanceManagerResources{}
				case "source identity":
					snapshot.Expected.Source.SourceSHA = strings.Repeat("e", 40)
				}
				if (mode == "settings drift before" && reads == 2) || (mode == "settings drift after" && consumed) {
					snapshot.Settings["BOREALIS_API_BACKEND_CPU_LIMIT"] = "3.00"
				}
				if (mode == "template drift before" && reads == 2) || (mode == "template drift after" && consumed) {
					snapshot.Storage.Requirements.Applications[0].MemoryLimitBytes++
				}
				if mode == "receipt drift" && reads == 2 {
					snapshot.Storage.Observation = strings.Repeat("e", 64)
				}
				if mode == "unrelated setting drift" && reads == 2 {
					snapshot.Settings["BOREALIS_DB_POOL_SIZE"] = "13"
				}
				return snapshot, err
			}
			if mode == "nil source" {
				source = nil
			}
			err := runClusterSSHNetworkTargets(context.Background(), f.a.Baseline, f.claims, f.deps, func(ctx context.Context, readers clusterSSHNetworkTargetReaders) error {
				return withClusterSSHTargetProfileSizing(ctx, readers, source, func(context.Context, []clusterSSHTargetHostEvidence) error {
					consumed = true
					return nil
				})
			})
			valid := mode == "expansion" || mode == "replacement"
			if (err == nil) != valid || (valid && !consumed) {
				t.Fatal("profile qualification result", consumed, err)
			}
			if !valid && !strings.HasSuffix(mode, "after") && consumed {
				t.Fatal("unqualified budget consumed")
			}
			if strings.HasSuffix(mode, "after") && !consumed {
				t.Fatal("test did not reach post-consumption recheck")
			}
		})
	}
}

// Qualification of declared application demand only. This does not supply
// infrastructure/worker/reserve allowances or authorize a hardware minimum.
// Run real profile selection, quantity conversion, manifest and CNPG rendering
// with synthetic private configuration; never read a deployed Engine or K3s.
func TestClusterSSHSmallBusinessApplicationBudget(t *testing.T) {
	const root = "../../../../../.."
	want := clusterSSHSmallBusinessApplications()
	wantPostgres := clusterSSHSmallBusinessPostgres()
	for _, host := range []struct {
		name, cpu, memory string
		valid             bool
	}{
		{"source profile", "8", "16384", true},
		{"larger joining host", "32", "131072", true},
		{"CPU below profile", "7", "16384", false},
		{"RAM below profile", "8", "16383", false},
	} {
		t.Run(host.name, func(t *testing.T) {
			directory := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			render := exec.CommandContext(ctx, "bash", filepath.Join(root, "Tests/render-k3s-manifests.sh"), directory, "small-business", host.cpu, host.memory)
			// Exclude ambient Borealis overrides and credentials from fixtures.
			render.Env = []string{"PATH=" + os.Getenv("PATH"), "LC_ALL=C", "PYTHONDONTWRITEBYTECODE=1"}
			out, err := render.CombinedOutput()
			if !host.valid {
				if err == nil || ctx.Err() != nil || !strings.Contains(string(out), "Joining host capacity is below the cluster sizing profile.") {
					t.Fatalf("expected inherited profile rejection: %v: %s", err, out)
				}
				return
			}
			if err != nil {
				t.Fatalf("profile manifest rendering: %v: %s", err, out)
			}
			decode := exec.CommandContext(ctx, "python3", "-c", `import json,pathlib,subprocess,sys,yaml
root,directory=map(pathlib.Path,sys.argv[1:])
profile=json.loads((directory/'profile.json').read_text())
assert profile=={'rank':1,'reference_memory_mib':16384,'scheduled_slots':8},profile
apps={}
postgres=[]
paths=sorted(directory.glob('*.yaml'))+[root/'Data/Engine/K3s/cluster/controller.yaml']
for path in paths:
    if path.name=='webui-dev.yaml':
        continue
    for obj in yaml.safe_load_all(path.read_text()):
        if not obj:
            continue
        if obj.get('kind')=='Deployment':
            name=obj['metadata']['name']
            assert name not in apps,name
            apps[name]=obj
        if obj.get('kind')=='StatefulSet':
            assert obj['metadata']['name']=='postgres-db'
            postgres.append(obj)
assert len(postgres)==1
cnpg=[]
for instances in (1,3):
    result=subprocess.run([sys.executable,str(root/'Data/Engine/K3s/cluster/render-postgres.py'),str(root/'Data/Engine/K3s/cluster/postgres.yaml'),str(instances)],input=json.dumps(postgres[0]),text=True,capture_output=True,check=True,timeout=5)
    clusters=[obj for obj in yaml.safe_load_all(result.stdout) if obj and obj.get('kind')=='Cluster']
    assert len(clusters)==1 and clusters[0]['spec']['instances']==instances
    cnpg.append(clusters[0]['spec']['resources'])
print(json.dumps({'applications':apps,'postgres':postgres[0]['spec']['template']['spec']['containers'][0]['resources'],'cnpg':cnpg}))
`, root, directory)
			decode.Env = render.Env
			out, err = decode.Output()
			if err != nil {
				if failure, ok := err.(*exec.ExitError); ok {
					t.Fatalf("decode production manifests: %v: %s", err, failure.Stderr)
				}
				t.Fatal(err)
			}
			var rendered struct {
				Applications map[string]map[string]any `json:"applications"`
				Postgres     map[string]any            `json:"postgres"`
				CNPG         []map[string]any          `json:"cnpg"`
			}
			if err := json.Unmarshal(out, &rendered); err != nil {
				t.Fatal(err)
			}
			if len(rendered.Applications) != len(want) || len(rendered.CNPG) != 2 {
				t.Fatal("production workload inventory changed")
			}
			// Kubernetes assigns these fields. Keep every rendered template field
			// intact while giving the existing observer synthetic API identity.
			for _, object := range rendered.Applications {
				metadata := clusterSSHStorageMap(object, "metadata")
				metadata["uid"], metadata["resourceVersion"] = newClusterUUID(), "1"
			}
			values, err := observeClusterSSHApplicationResources(func(path string) (map[string]any, error) {
				return rendered.Applications[filepath.Base(path)], nil
			})
			if err != nil || !slices.Equal(values, want) {
				t.Fatalf("Small Business application budget drift: %v: got %+v; want %+v", err, values, want)
			}
			for _, raw := range append(rendered.CNPG, rendered.Postgres) {
				postgres, valid := clusterSSHWorkloadResources("postgres", raw)
				if !valid || postgres != wantPostgres {
					t.Fatalf("PostgreSQL/CNPG inherited budget drift: got %+v; want %+v", postgres, wantPostgres)
				}
			}
			cpu, memory, valid := clusterSSHApplicationBudget(values)
			if !valid || cpu != 325 || memory != 3744<<20 {
				t.Fatalf("incorrect per-node application subtotal: %d mCPU, %d bytes", cpu, memory)
			}
			if cpu+wantPostgres.CPURequestMilli != 425 || memory+wantPostgres.MemoryLimitBytes != 9888<<20 {
				t.Fatal("incorrect application plus one PostgreSQL instance subtotal")
			}
		})
	}
}
