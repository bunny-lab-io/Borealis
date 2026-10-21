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

// Qualification of declared application demand only. This does not supply
// infrastructure/worker/reserve allowances or authorize a hardware minimum.
// Run real profile selection, quantity conversion, manifest and CNPG rendering
// with synthetic private configuration; never read a deployed Engine or K3s.
func TestClusterSSHSmallBusinessApplicationBudget(t *testing.T) {
	const root = "../../../../../.."
	want := []clusterSSHApplicationResource{
		{"api-backend", 75, 2000, 192 << 20, 1024 << 20},
		{"borealis-cluster-controller", 50, 500, 64 << 20, 256 << 20},
		{"borealis-operator", 25, 250, 48 << 20, 160 << 20},
		{"job-scheduler", 50, 1500, 128 << 20, 512 << 20},
		{"remote-desktop-guacd", 25, 1000, 64 << 20, 512 << 20},
		{"traefik-edge", 25, 1500, 96 << 20, 512 << 20},
		{"webui-frontend", 50, 1000, 128 << 20, 512 << 20},
		{"wireguard-tunnel", 25, 1000, 48 << 20, 256 << 20},
	}
	wantPostgres := clusterSSHApplicationResource{"postgres-db", 100, 2000, 256 << 20, 6144 << 20}
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
				postgres, valid := clusterSSHWorkloadResources("postgres-db", raw)
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
