package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type sshStorageFixture struct {
	mu      sync.Mutex
	a       clusterSSHPreparationAuthority
	objects map[string]map[string]any
	calls   map[string]int
}

func sshStorageObject(version, kind, namespace, name string) map[string]any {
	return map[string]any{"apiVersion": version, "kind": kind, "metadata": map[string]any{"name": name, "namespace": namespace, "uid": newClusterUUID(), "resourceVersion": "1"}}
}

func newSSHStorageFixture(t *testing.T, replacement bool) *sshStorageFixture {
	t.Helper()
	cohort, source, lease, baseline := sshPreparationFixture(t)
	if replacement {
		target := cohort.Targets[1]
		source.Members = append(source.Members, clusterSSHSourceMember{NodeID: newClusterUUID(), NodeUID: newClusterUUID(), Name: target.Report.Hostname, Address: target.Binding.Address, MachineID: target.Report.MachineID, BootID: target.Report.BootID})
		source.ActiveSize, source.DesiredSize, source.Status = 2, 3, "Degraded Quorum"
		cohort.Targets = cohort.Targets[:1]
	}
	f := &sshStorageFixture{a: clusterSSHPreparationAuthority{Cohort: cohort, Source: source, Lease: lease, Baseline: baseline, K3sVersion: "v1.36.3+k3s1"}, objects: map[string]map[string]any{}, calls: map[string]int{}}
	f.objects["/api/v1/namespaces/kube-system"] = map[string]any{"metadata": map[string]any{"uid": source.KubeSystemUID}}
	nodes := []any{}
	for _, member := range source.Members {
		nodes = append(nodes, sshSourceKubernetesFixture(member))
	}
	f.objects["/api/v1/nodes"] = map[string]any{"items": nodes}
	cluster := sshStorageObject("postgresql.cnpg.io/v1", "Cluster", "borealis", "borealis-postgres")
	instances := 1
	if replacement {
		instances = 3
	}
	cluster["spec"] = map[string]any{"instances": instances, "storage": map[string]any{"size": "20Gi", "storageClass": "borealis-longhorn-local", "resizeInUseVolumes": true}}
	cluster["status"] = map[string]any{"readyInstances": len(source.Members), "currentPrimary": "borealis-postgres-1"}
	f.objects[clusterSSHStoragePostgresPath] = cluster
	owner := func() []any {
		return []any{map[string]any{"apiVersion": "postgresql.cnpg.io/v1", "kind": "Cluster", "name": "borealis-postgres", "uid": clusterSSHStorageMap(cluster, "metadata")["uid"], "controller": true}}
	}
	claims, pods := []any{}, []any{}
	addClaim := func(name, class, mode, locality, size, node string, replicas int, active bool) {
		claim := sshStorageObject("v1", "PersistentVolumeClaim", "borealis", name)
		pvName := "pvc-" + newClusterUUID()
		claim["spec"] = map[string]any{"volumeName": pvName, "volumeMode": "Filesystem", "storageClassName": class, "accessModes": []any{mode}, "resources": map[string]any{"requests": map[string]any{"storage": size}}}
		claim["status"] = map[string]any{"phase": "Bound", "capacity": map[string]any{"storage": size}}
		if active {
			clusterSSHStorageMap(claim, "metadata")["ownerReferences"] = owner()
		}
		pv := sshStorageObject("v1", "PersistentVolume", "", pvName)
		pv["spec"] = map[string]any{"volumeMode": "Filesystem", "storageClassName": class, "accessModes": []any{mode}, "capacity": map[string]any{"storage": size},
			"claimRef": map[string]any{"apiVersion": "v1", "kind": "PersistentVolumeClaim", "namespace": "borealis", "name": name, "uid": clusterSSHStorageMap(claim, "metadata")["uid"]},
			"csi":      map[string]any{"driver": clusterLonghornCSIDriver, "fsType": "ext4", "volumeHandle": pvName}}
		pv["status"] = map[string]any{"phase": "Bound"}
		volume := sshStorageObject("longhorn.io/v1beta2", "Volume", "longhorn-system", pvName)
		bytes, _ := clusterSSHStorageBytes(size)
		access := "rwo"
		if mode == "ReadWriteMany" {
			access = "rwx"
		}
		volume["spec"] = map[string]any{"frontend": "blockdev", "dataEngine": "v1", "encrypted": false, "migratable": false, "size": strconv.FormatUint(bytes, 10), "numberOfReplicas": replicas, "accessMode": access, "dataLocality": locality, "nodeID": node}
		state, robustness := "attached", "healthy"
		if node == "" {
			state, robustness = "detached", "unknown"
		}
		volume["status"] = map[string]any{"state": state, "robustness": robustness, "currentNodeID": node, "kubernetesStatus": map[string]any{"namespace": "borealis", "pvcName": name, "pvName": pvName, "pvStatus": "Bound"}}
		f.objects[clusterSSHStoragePVPrefix+pvName], f.objects[clusterSSHStorageVolumePrefix+pvName] = pv, volume
		claims = append(claims, claim)
	}
	artifactReplicas := 1
	if replacement {
		artifactReplicas = 3
	}
	addClaim(clusterSharedArtifactPVCName, "borealis-longhorn", "ReadWriteMany", "disabled", "4Gi", source.Members[0].Name, artifactReplicas, false)
	for i, member := range source.Members {
		name := fmt.Sprintf("borealis-postgres-%d", i+1)
		addClaim(name, "borealis-longhorn-local", "ReadWriteOnce", "strict-local", "20Gi", member.Name, 1, true)
		pod := sshStorageObject("v1", "Pod", "borealis", name)
		clusterSSHStorageMap(pod, "metadata")["ownerReferences"] = owner()
		clusterSSHStorageMap(pod, "metadata")["labels"] = map[string]any{"cnpg.io/cluster": "borealis-postgres"}
		pod["spec"] = map[string]any{"nodeName": member.Name, "volumes": []any{map[string]any{"name": "pgdata", "persistentVolumeClaim": map[string]any{"claimName": name}}, map[string]any{"name": "scratch", "emptyDir": map[string]any{}}}}
		pod["status"] = map[string]any{"phase": "Running", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}}
		pods = append(pods, pod)
	}
	addClaim("postgres-data-postgres-db-0", "borealis-longhorn", "ReadWriteOnce", "disabled", "20Gi", "", 1, false)
	f.objects[clusterSSHStorageClaimsPath] = map[string]any{"apiVersion": "v1", "kind": "PersistentVolumeClaimList", "metadata": map[string]any{"resourceVersion": "100"}, "items": claims}
	f.objects[clusterSSHStoragePodsPath] = map[string]any{"apiVersion": "v1", "kind": "PodList", "metadata": map[string]any{"resourceVersion": "100"}, "items": pods}
	return f
}

func (f *sshStorageFixture) authority(ctx context.Context) (clusterSSHPreparationAuthority, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var value clusterSSHPreparationAuthority
	raw, _ := json.Marshal(f.a)
	_ = json.Unmarshal(raw, &value)
	value.Cohort.ObservedAt++
	return value, ctx.Err()
}
func (f *sshStorageFixture) get(ctx context.Context, path string, out any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[path]++
	object, ok := f.objects[path]
	if !ok {
		return errors.New("private absent object")
	}
	raw, err := json.Marshal(object)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return json.Unmarshal(raw, out)
}
func (f *sshStorageFixture) claim(index int) map[string]any {
	return f.objects[clusterSSHStorageClaimsPath]["items"].([]any)[index].(map[string]any)
}
func (f *sshStorageFixture) pv(index int) map[string]any {
	return f.objects[clusterSSHStoragePVPrefix+clusterSSHStorageText(clusterSSHStorageMap(f.claim(index), "spec"), "volumeName")]
}
func (f *sshStorageFixture) volume(index int) map[string]any {
	return f.objects[clusterSSHStorageVolumePrefix+clusterSSHStorageText(clusterSSHStorageMap(f.claim(index), "spec"), "volumeName")]
}
func (f *sshStorageFixture) pod(index int) map[string]any {
	return f.objects[clusterSSHStoragePodsPath]["items"].([]any)[index].(map[string]any)
}

func TestClusterSSHStorageInventory(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		t.Run(fmt.Sprint(replacement), func(t *testing.T) {
			f := newSSHStorageFixture(t, replacement)
			got, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			if err != nil {
				t.Fatal(err)
			}
			r := got.Requirements
			if r.ArtifactReplicaBytes != 4<<30 || r.PostgresInstanceBytes != 20<<30 || len(r.Volumes) != len(f.a.Source.Members)+2 || r.ConfiguredPostgresInstances != int64(f.objects[clusterSSHStoragePostgresPath]["spec"].(map[string]any)["instances"].(int)) {
				t.Fatal("wrong requirements")
			}
			pg, legacy, artifacts := 0, 0, 0
			for _, v := range r.Volumes {
				switch v.Role {
				case "postgres":
					pg++
					if v.Replicas != 1 || v.Node == "" {
						t.Fatal("missing placement")
					}
				case "other":
					legacy++
					if v.State != "detached" || v.Bytes != 20<<30 {
						t.Fatal("lost retained occupation")
					}
				case "artifacts":
					artifacts++
					if replacement && v.Replicas != 3 {
						t.Fatal("actual policy lost")
					}
				default:
					t.Fatal("unknown role")
				}
			}
			if pg != len(f.a.Source.Members) || legacy != 1 || artifacts != 1 {
				t.Fatal("misclassified claims")
			}
			for path := range f.calls {
				if strings.Contains(path, "storageclasses") {
					t.Fatal("provisioning default substituted")
				}
			}
			before := got
			for _, path := range []string{clusterSSHStorageClaimsPath, clusterSSHStoragePodsPath} {
				items := f.objects[path]["items"].([]any)
				slices.Reverse(items)
				clusterSSHStorageMap(f.objects[path], "metadata")["resourceVersion"] = "101"
			}
			after, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("collection order/clock changed object evidence")
			}
		})
	}
}

func TestClusterSSHStorageRejectsUnsupportedInventory(t *testing.T) {
	cases := map[string]func(*sshStorageFixture){
		"wrong namespace type": func(f *sshStorageFixture) { clusterSSHStorageMap(f.pv(0), "metadata")["namespace"] = nil },
		"wrong empty conditions shape": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.claim(0), "status")["conditions"] = map[string]any{}
		},
		"wrong empty WAL shape": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.objects[clusterSSHStoragePostgresPath], "spec")["walStorage"] = []any{}
		},
		"wrong empty selector shape": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.volume(0), "spec")["nodeSelector"] = map[string]any{}
		},
		"PV node affinity": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.pv(0), "spec")["nodeAffinity"] = map[string]any{"required": map[string]any{}}
		},
		"volume attributes": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.pv(0), "spec")["volumeAttributesClassName"] = "other"
		},
		"artifact foreign owner": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.claim(0), "metadata")["ownerReferences"] = []any{map[string]any{"uid": newClusterUUID()}}
		},

		"claim replacement": func(f *sshStorageFixture) { clusterSSHStorageMap(f.claim(0), "metadata")["uid"] = newClusterUUID() },
		"duplicate claim UID": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.claim(1), "metadata")["uid"] = clusterSSHStorageMap(f.claim(0), "metadata")["uid"]
		},
		"duplicate PV UID": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.pv(1), "metadata")["uid"] = clusterSSHStorageMap(f.pv(0), "metadata")["uid"]
		},
		"duplicate volume UID": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.volume(1), "metadata")["uid"] = clusterSSHStorageMap(f.volume(0), "metadata")["uid"]
		},
		"wrong claim namespace": func(f *sshStorageFixture) {
			clusterSSHStorageMap(clusterSSHStorageMap(f.pv(0), "spec"), "claimRef")["namespace"] = "foreign"
		},
		"wrong CSI handle": func(f *sshStorageFixture) {
			clusterSSHStorageMap(clusterSSHStorageMap(f.pv(0), "spec"), "csi")["volumeHandle"] = "other"
		},
		"wrong CSI driver": func(f *sshStorageFixture) {
			clusterSSHStorageMap(clusterSSHStorageMap(f.pv(0), "spec"), "csi")["driver"] = "other"
		},
		"foreign fs": func(f *sshStorageFixture) {
			clusterSSHStorageMap(clusterSSHStorageMap(f.pv(0), "spec"), "csi")["fsType"] = "xfs"
		},
		"read only CSI": func(f *sshStorageFixture) {
			clusterSSHStorageMap(clusterSSHStorageMap(f.pv(0), "spec"), "csi")["readOnly"] = true
		},
		"block mode":     func(f *sshStorageFixture) { clusterSSHStorageMap(f.claim(0), "spec")["volumeMode"] = "Block" },
		"class mismatch": func(f *sshStorageFixture) { clusterSSHStorageMap(f.pv(0), "spec")["storageClassName"] = "other" },
		"size mismatch": func(f *sshStorageFixture) {
			clusterSSHStorageMap(clusterSSHStorageMap(f.pv(0), "spec"), "capacity")["storage"] = "5Gi"
		},
		"fractional quantity": func(f *sshStorageFixture) {
			clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(f.claim(0), "spec"), "resources"), "requests")["storage"] = "0.5Gi"
		},
		"unfinished resize": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.claim(0), "status")["conditions"] = []any{map[string]any{"type": "Resizing", "status": "True"}}
		},
		"deleting PV": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.pv(0), "metadata")["deletionTimestamp"] = "2026-09-20T00:00:00Z"
		},
		"numeric LH size":  func(f *sshStorageFixture) { clusterSSHStorageMap(f.volume(0), "spec")["size"] = 4 << 30 },
		"quantity LH size": func(f *sshStorageFixture) { clusterSSHStorageMap(f.volume(0), "spec")["size"] = "4Gi" },
		"wrong LH binding": func(f *sshStorageFixture) {
			clusterSSHStorageMap(clusterSSHStorageMap(f.volume(0), "status"), "kubernetesStatus")["pvcName"] = "other"
		},
		"unsupported replicas": func(f *sshStorageFixture) { clusterSSHStorageMap(f.volume(0), "spec")["numberOfReplicas"] = 2 },
		"replica fraction":     func(f *sshStorageFixture) { clusterSSHStorageMap(f.volume(0), "spec")["numberOfReplicas"] = 1.5 },
		"encrypted":            func(f *sshStorageFixture) { clusterSSHStorageMap(f.volume(0), "spec")["encrypted"] = true },
		"disk selector": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.volume(0), "spec")["diskSelector"] = []any{"special"}
		},
		"future engine":              func(f *sshStorageFixture) { clusterSSHStorageMap(f.volume(0), "spec")["dataEngine"] = "v2" },
		"artifact detached":          func(f *sshStorageFixture) { clusterSSHStorageMap(f.volume(0), "status")["state"] = "detached" },
		"PG degraded":                func(f *sshStorageFixture) { clusterSSHStorageMap(f.volume(1), "status")["robustness"] = "degraded" },
		"PG wrong placement":         func(f *sshStorageFixture) { clusterSSHStorageMap(f.volume(1), "status")["currentNodeID"] = "foreign" },
		"PG nonlocal":                func(f *sshStorageFixture) { clusterSSHStorageMap(f.volume(1), "spec")["dataLocality"] = "disabled" },
		"PG replicated strict local": func(f *sshStorageFixture) { clusterSSHStorageMap(f.volume(1), "spec")["numberOfReplicas"] = 3 },
		"PG owner missing":           func(f *sshStorageFixture) { delete(clusterSSHStorageMap(f.claim(1), "metadata"), "ownerReferences") },
		"PG cluster size differs": func(f *sshStorageFixture) {
			clusterSSHStorageMap(clusterSSHStorageMap(f.objects[clusterSSHStoragePostgresPath], "spec"), "storage")["size"] = "21Gi"
		},
		"extra WAL": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.objects[clusterSSHStoragePostgresPath], "spec")["walStorage"] = map[string]any{"size": "1Gi"}
		},
		"extra tablespace": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.objects[clusterSSHStoragePostgresPath], "spec")["tablespaces"] = []any{map[string]any{"name": "extra"}}
		},
		"custom PVC": func(f *sshStorageFixture) {
			clusterSSHStorageMap(clusterSSHStorageMap(f.objects[clusterSSHStoragePostgresPath], "spec"), "storage")["pvcTemplate"] = map[string]any{"metadata": map[string]any{"labels": map[string]any{"custom": "true"}}}
		},
		"unknown storage setting": func(f *sshStorageFixture) {
			clusterSSHStorageMap(clusterSSHStorageMap(f.objects[clusterSSHStoragePostgresPath], "spec"), "storage")["future"] = true
		},
		"wrong configured replicas": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.objects[clusterSSHStoragePostgresPath], "spec")["instances"] = 3
		},
		"wrong primary": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.objects[clusterSSHStoragePostgresPath], "status")["currentPrimary"] = "foreign"
		},
		"wrong ready count": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.objects[clusterSSHStoragePostgresPath], "status")["readyInstances"] = 0
		},
		"pod owner missing": func(f *sshStorageFixture) { delete(clusterSSHStorageMap(f.pod(0), "metadata"), "ownerReferences") },
		"pod wrong owner": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.pod(0), "metadata")["ownerReferences"].([]any)[0].(map[string]any)["uid"] = newClusterUUID()
		},
		"pod wrong label": func(f *sshStorageFixture) {
			clusterSSHStorageMap(clusterSSHStorageMap(f.pod(0), "metadata"), "labels")["cnpg.io/cluster"] = "foreign"
		},
		"pod wrong node": func(f *sshStorageFixture) { clusterSSHStorageMap(f.pod(0), "spec")["nodeName"] = "foreign" },
		"pod pending":    func(f *sshStorageFixture) { clusterSSHStorageMap(f.pod(0), "status")["phase"] = "Pending" },
		"pod not ready": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.pod(0), "status")["conditions"].([]any)[0].(map[string]any)["status"] = "False"
		},
		"pod missing volume": func(f *sshStorageFixture) { clusterSSHStorageMap(f.pod(0), "spec")["volumes"] = []any{} },
		"ephemeral PVC": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.pod(0), "spec")["volumes"].([]any)[0].(map[string]any)["ephemeral"] = map[string]any{}
		},
		"list continuation": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.objects[clusterSSHStorageClaimsPath], "metadata")["continue"] = "opaque"
		},
		"list remaining": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.objects[clusterSSHStoragePodsPath], "metadata")["remainingItemCount"] = 1
		},
		"unbound legacy": func(f *sshStorageFixture) { clusterSSHStorageMap(f.claim(2), "status")["phase"] = "Pending" },
		"missing volume": func(f *sshStorageFixture) {
			delete(f.objects, clusterSSHStorageVolumePrefix+clusterSSHStorageText(clusterSSHStorageMap(f.claim(0), "spec"), "volumeName"))
		},
		"PV path injection": func(f *sshStorageFixture) {
			clusterSSHStorageMap(f.claim(0), "spec")["volumeName"] = "../secrets/private"
		},
		"missing artifact": func(f *sshStorageFixture) {
			f.objects[clusterSSHStorageClaimsPath]["items"] = f.objects[clusterSSHStorageClaimsPath]["items"].([]any)[1:]
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			mutate(f)
			value, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			if err != clusterbootstrap.ErrPreparationConfig || value.Requirements.Volumes != nil {
				t.Fatal("unsupported inventory accepted")
			}
		})
	}
}

func TestClusterSSHStorageParsers(t *testing.T) {
	valid := map[string]uint64{"1": 1, "4Gi": 4 << 30, "20Gi": 20 << 30, "5Ti": 5 << 40, "17Mi": 17 << 20, "2Ki": 2048, "9223372036854775807": math.MaxInt64}
	for input, want := range valid {
		got, ok := clusterSSHStorageBytes(input)
		if !ok || got != want {
			t.Fatalf("valid quantity %s", input)
		}
	}
	for _, input := range []any{nil, 1, true, "", "0", "01", "-1", "1m", "1M", "1G", "1.5Gi", "1e3", "9223372036854775808", "8388608Ti", "9999999999999999999Ti", " 4Gi", "4Gi\n"} {
		if _, ok := clusterSSHStorageBytes(input); ok {
			t.Fatalf("accepted quantity %v", input)
		}
	}
	for _, input := range []string{`null`, `[]`, `{"a":1,"a":2}`, `{"nested":{"a":1,"a":1}}`, `{} {}`, `{"a":1} garbage`, strings.Repeat(`{"a":`, 34) + `1` + strings.Repeat(`}`, 34), "{\"a\":\"\xff\"}"} {
		if _, _, err := clusterSSHStorageObject([]byte(input)); err == nil {
			t.Fatal("ambiguous JSON accepted")
		}
	}
	_, one, err := clusterSSHStorageObject([]byte(`{"b":1,"a":9223372036854775807}`))
	if err != nil {
		t.Fatal(err)
	}
	_, two, err := clusterSSHStorageObject([]byte(`{"a":9223372036854775807,"b":1}`))
	if err != nil || one != two {
		t.Fatal("map order changed receipt")
	}
	_, three, err := clusterSSHStorageObject([]byte(`{"a":9223372036854775806,"b":1}`))
	if err != nil || one == three {
		t.Fatal("large integer rounded")
	}
}
