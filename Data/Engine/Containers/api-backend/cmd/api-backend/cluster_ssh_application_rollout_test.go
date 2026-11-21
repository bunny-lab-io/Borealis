package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

// Execute the actual reconciler against an in-memory Kubernetes command seam.
// Resources come from the real profile renderers. No subprocess may reach K3s.
func sshApplicationUpdateCopies(t *testing.T, values []clusterSSHApplicationResource) map[string]int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "python3", "-c", `import copy,importlib.util,json,pathlib,sys
sys.dont_write_bytecode=True
path=pathlib.Path('../../../../../../Data/Engine/K3s/cluster/reconcile-node-workloads.py')
spec=importlib.util.spec_from_file_location('reconcile',path)
m=importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)
def forbidden(*args,**kwargs):
    raise AssertionError('external subprocess forbidden')
m.subprocess.run=forbidden
values=json.load(sys.stdin)
assert sorted(m.DEPLOYMENT_SERVICES)==[v['name'] for v in values]
assert m.HOST_PORT_DEPLOYMENTS=={'traefik-edge','wireguard-tunnel'}
node='engine-fixture'
state={}
original={}
maximum={v['name']:1 for v in values}
events=[]
for v in values:
    base=v['name']
    resources={'requests':{'cpu':str(v['cpu_request_milli'])+'m','memory':str(v['memory_request_bytes'])},'limits':{'cpu':str(v['cpu_limit_milli'])+'m','memory':str(v['memory_limit_bytes'])}}
    original[base]=resources
    state['deployment/'+base]={'apiVersion':'apps/v1','kind':'Deployment','metadata':{'name':base,'resourceVersion':'1'},'spec':{'replicas':0,'selector':{'matchLabels':{'app.kubernetes.io/name':base}},'template':{'spec':{'containers':[{'name':base,'resources':resources,'startupProbe':{'exec':{'command':['true']}},'livenessProbe':{'exec':{'command':['true']}}}]}}}}
state['borealiscluster.engine.borealis.io/borealis']={'spec':{'clusterVIP':'192.168.40.10'}}
m.load_json=lambda key:copy.deepcopy(state.get(key))
def kubectl(*args,stdin=None):
    if args[0]=='apply':
        obj=json.loads(stdin)
        base=obj['metadata']['annotations']['borealis.io/cluster-template']
        name=obj['metadata']['name']
        candidate=obj['metadata']['labels'][m.CANDIDATE_LABEL]=='true'
        host=base in m.HOST_PORT_DEPLOYMENTS
        assert obj['spec']['replicas']==(0 if host and candidate else 1)
        assert obj['spec']['strategy']==({'type':'Recreate'} if host else {'type':'RollingUpdate','rollingUpdate':{'maxUnavailable':0,'maxSurge':1}})
        containers=obj['spec']['template']['spec']['containers']
        assert len(containers)==1 and containers[0]['resources']==original[base]
        assert not obj['spec']['template']['spec'].get('initContainers')
        active=state.get('deployment/'+m.deployment_name(base,node))
        staged=state.get('deployment/'+m.candidate_deployment_name(base,node))
        if candidate:
            assert active and active['spec']['replicas']==1
            maximum[base]=max(maximum[base],1+obj['spec']['replicas'])
        elif staged:
            # The existing active Deployment and candidate are both present
            # when promotion applies its new active template. Recreate has no
            # declared surge; ordinary applications retain one old/new pair.
            assert staged['spec']['replicas']==(0 if host else 1)
            surge=0 if host else obj['spec']['strategy']['rollingUpdate']['maxSurge']
            maximum[base]=max(maximum[base],obj['spec']['replicas']+surge+staged['spec']['replicas'])
        state['deployment/'+name]=copy.deepcopy(obj)
        events.append(('apply',name))
        return ''
    assert args[:2]==('-n','borealis'),args
    action=args[2]
    if action=='scale':
        key=args[3]
        state[key]['spec']['replicas']=int(args[4].split('=')[1])
        events.append(('scale',key.split('/')[1]))
        return ''
    if action=='rollout':
        assert args[3]=='status'
        events.append(('ready',args[4].split('/')[1]))
        return ''
    if action=='patch':
        key=args[3]
        patch=json.loads(args[args.index('-p')+1])
        obj=state[key]
        obj['metadata'].setdefault('annotations',{}).update(patch['metadata']['annotations'])
        template=obj['spec']['template']
        template.setdefault('metadata',{}).setdefault('annotations',{}).update(patch['spec']['template']['metadata']['annotations'])
        images={c['name']:c['image'] for c in patch['spec']['template']['spec']['containers']}
        for c in template['spec']['containers']:
            c['image']=images[c['name']]
        return json.dumps(obj)
    if action=='delete':
        key=args[3]
        base=state[key]['metadata']['annotations']['borealis.io/cluster-template']
        active=m.deployment_name(base,node)
        assert ('ready',active) in events
        assert max(i for i,event in enumerate(events) if event==('ready',active))>max(i for i,event in enumerate(events) if event==('apply',active))
        del state[key]
        events.append(('delete',key.split('/')[1]))
        return ''
    raise AssertionError(args)
m.kubectl=kubectl
images={service:'example.invalid/'+service+'@sha256:'+'a'*64 for service in m.DEPLOYMENT_SERVICES.values()}
for base,service in m.DEPLOYMENT_SERVICES.items():
    m.reconcile_one(base,service,node,'a'*40,images,False)
for base,service in m.DEPLOYMENT_SERVICES.items():
    m.reconcile_one(base,service,node,'b'*40,images,True)
# Every non-host-port candidate remains alongside its active workload before
# the first promotion. No test stub silently deletes earlier candidates.
for base in m.DEPLOYMENT_SERVICES:
    candidate=state['deployment/'+m.candidate_deployment_name(base,node)]
    assert candidate['spec']['replicas']==(0 if base in m.HOST_PORT_DEPLOYMENTS else 1)
events.clear()
for base in m.DEPLOYMENT_SERVICES:
    m.promote_one(base,node,'b'*40)
assert all('deployment/'+m.candidate_deployment_name(base,node) not in state for base in m.DEPLOYMENT_SERVICES)
print(json.dumps(maximum))`)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "LC_ALL=C", "PYTHONDONTWRITEBYTECODE=1"}
	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	command.Stdin = strings.NewReader(string(raw))
	out, err := command.CombinedOutput()
	var copies map[string]int
	if err != nil || json.Unmarshal(out, &copies) != nil || len(copies) != len(values) {
		t.Fatalf("actual application update lifecycle drift: %v: %s", err, out)
	}
	return copies
}

func TestClusterSSHApplicationUpdateOverlapProducerParity(t *testing.T) {
	for rank, profile := range []string{"homelab", "small-business", "msp", "enterprise"} {
		t.Run(profile, func(t *testing.T) {
			_, applications, _, _ := sshRenderedProfileContract(t, profile, rank, [4]string{"8192", "16384", "32768", "65536"}[rank])
			copies := sshApplicationUpdateCopies(t, applications)
			var wantCPU, wantMemory uint64
			for _, value := range applications {
				wantCopies := 3
				if value.Name == "traefik-edge" || value.Name == "wireguard-tunnel" {
					wantCopies = 1
				}
				if copies[value.Name] != wantCopies {
					t.Fatal("declared lifecycle copy count changed", value.Name, copies[value.Name])
				}
				wantCPU += uint64(copies[value.Name]-1) * value.CPURequestMilli
				wantMemory += uint64(copies[value.Name]-1) * value.MemoryLimitBytes
			}
			cpu, memory, ok := clusterSSHApplicationUpdateOverlapBudget(applications)
			if !ok || cpu != wantCPU || memory != wantMemory || cpu != 550 || memory != [4]uint64{3392, 5952, 10560, 17216}[rank]<<20 {
				t.Fatal("application candidate/active surge charge differs from producer", cpu, memory, wantCPU, wantMemory)
			}
		})
	}
}

func TestClusterSSHApplicationUpdateOverlapBudget(t *testing.T) {
	for _, mode := range []string{"valid", "missing", "duplicate", "reordered", "invalid host-port", "CPU overlap overflow", "memory overlap overflow", "CPU combined overflow", "memory combined overflow"} {
		t.Run(mode, func(t *testing.T) {
			_, _, snapshot := sshBrokerFixture(t)
			values := snapshot.Storage.Requirements.Applications
			switch mode {
			case "missing":
				values = values[:len(values)-1]
			case "duplicate":
				values[1] = values[0]
			case "reordered":
				values[0], values[1] = values[1], values[0]
			case "invalid host-port":
				values[7].MemoryLimitBytes = 0
			case "CPU overlap overflow", "CPU combined overflow":
				values[0].CPURequestMilli = clusterSSHCapacityLimit/2 + 1
				if mode == "CPU combined overflow" {
					values[0].CPURequestMilli = clusterSSHCapacityLimit / 3
				}
				values[0].CPULimitMilli = values[0].CPURequestMilli
			case "memory overlap overflow", "memory combined overflow":
				values[0].MemoryLimitBytes = clusterSSHCapacityLimit/2 + 1
				if mode == "memory combined overflow" {
					values[0].MemoryLimitBytes = clusterSSHCapacityLimit / 3
				}
			}
			snapshot.Storage.Requirements.Applications = values
			before := slices.Clone(values)
			cpu, memory, ok := clusterSSHApplicationUpdateOverlapBudget(values)
			combined := strings.Contains(mode, "combined overflow")
			if (mode == "valid" || combined) != ok || !slices.Equal(values, before) {
				t.Fatal("invalid or mutable overlap subtotal", cpu, memory, ok)
			}
			if mode == "valid" && (cpu != 1200 || memory != 1536<<20) {
				t.Fatal("wrong declared fixture overlap", cpu, memory)
			}
			if !ok && (cpu != 0 || memory != 0) {
				t.Fatal("partial failed subtotal escaped")
			}
			if combined {
				if _, _, valid := clusterSSHNodeWorkloadBudget(snapshot.Storage.Requirements); !valid {
					t.Fatal("steady fixture must remain valid before added overlap")
				}
				if cpu, memory, valid := clusterSSHDesiredWorkerWorkloadBudget(snapshot); valid || cpu != 0 || memory != 0 {
					t.Fatal("combined update budget overflow escaped", cpu, memory)
				}
			}
		})
	}
}

func TestClusterSSHApplicationUpdateOverlapTargetFit(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "all overlap CPU omitted", "all overlap memory omitted", "one cohort CPU omitted", "one cohort memory omitted", "drift before", "drift after"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHNetworkTargetsFixture(t, mode == "replacement", "peer")
			_, _, snapshot := sshBrokerFixture(t)
			var err error
			snapshot.Expected, err = buildClusterSSHPreparationExpected(f.a.Cohort, f.a.Source, f.a.Lease, f.a.Baseline, f.a.K3sVersion, f.sources[0].PodCIDR, f.sources[0].ServiceCIDR)
			if err != nil {
				t.Fatal(err)
			}
			snapshot.Settings["BOREALIS_POSTGRES_DB_MEMORY_LIMIT"] = "4g"
			cpu, memory, ok := clusterSSHNodeWorkloadBudget(snapshot.Storage.Requirements)
			// Independently retain this fixture's one worker candidate and
			// two copies of six 100m/128Mi application envelopes. Deriving
			// these values through the new calculation would hide its removal.
			cpu += 25 + 1200
			memory += (256 + 1536) << 20
			if !ok || cpu >= 16000 || memory+clusterSSHHostMemoryReserveBytes >= 32<<30 {
				t.Fatal("invalid native fit baseline")
			}
			// Balance only the single-copy host-port envelope. Ordinary
			// resources retain independently known 1200m/1536Mi extra demand.
			app := &snapshot.Storage.Requirements.Applications[7]
			app.CPURequestMilli += 16000 - cpu
			app.CPULimitMilli = app.CPURequestMilli
			app.MemoryLimitBytes += (32 << 30) - clusterSSHHostMemoryReserveBytes - memory
			switch mode {
			case "one cohort CPU omitted":
				app.CPURequestMilli += 600
				app.CPULimitMilli = app.CPURequestMilli
			case "one cohort memory omitted":
				app.MemoryLimitBytes += 768 << 20
			case "all overlap CPU omitted":
				app.CPURequestMilli += 1200
				app.CPULimitMilli = app.CPURequestMilli
			case "all overlap memory omitted":
				app.MemoryLimitBytes += 1536 << 20
			}
			consumed, reads := false, 0
			err = runClusterSSHNetworkTargets(context.Background(), f.a.Baseline, f.claims, f.deps, func(ctx context.Context, readers clusterSSHNetworkTargetReaders) error {
				return withClusterSSHTargetApplicationSizing(ctx, readers, func(context.Context) (clusterSSHPreparationSnapshot, error) {
					reads++
					if mode == "drift before" && reads == 2 || mode == "drift after" && consumed {
						// Smaller cap still fits alone; retained source drift rejects.
						snapshot.Storage.Requirements.Applications[0].MemoryLimitBytes--
					}
					return snapshot, nil
				}, func(context.Context, []clusterSSHTargetHostEvidence) error { consumed = true; return nil })
			})
			valid := mode == "expansion" || mode == "replacement"
			if (err == nil) != valid || consumed != (valid || mode == "drift after") || reads < 2 {
				t.Fatal("application update escaped native fit or drift checks", consumed, reads, err)
			}
			f.assertClosed(t)
		})
	}
}
