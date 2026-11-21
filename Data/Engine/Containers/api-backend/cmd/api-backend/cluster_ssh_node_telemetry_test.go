package main

import (
	"context"
	"strings"
	"testing"
)

func TestClusterSSHStorageNodeTelemetry(t *testing.T) {
	for _, tc := range []struct {
		name string
		pass bool
	}{
		{"heartbeat", true}, {"voter heartbeat", true}, {"package order", true},
		{"disk sample time", true}, {"snapshot time", true}, {"unexplained revision", false},
		{"readiness", false}, {"transition", false}, {"older heartbeat", false},
		{"CPU", false}, {"package removed", false}, {"package check failed", false},
		{"writer ownership", false}, {"other writer time", false}, {"node identity", false},
		{"role label", false}, {"disk health", false}, {"snapshot configuration", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const before = "2026-11-21T16:21:14Z"
			const after = "2026-11-21T16:26:21Z"
			f := newSSHStorageFixture(t, false)
			name := f.a.Source.Members[0].Name
			kube := f.objects["/api/v1/nodes/"+name]
			meta := clusterSSHStorageMap(kube, "metadata")
			status := clusterSSHStorageMap(kube, "status")
			condition := map[string]any{"type": "Ready", "status": "True", "lastHeartbeatTime": before, "lastTransitionTime": before}
			voter := map[string]any{"type": "EtcdIsVoter", "status": "True", "lastHeartbeatTime": before}
			status["conditions"] = []any{condition, voter}
			writer := func(manager, subresource string) map[string]any {
				return map[string]any{"manager": manager, "operation": "Update", "subresource": subresource, "apiVersion": "v1", "fieldsType": "FieldsV1", "fieldsV1": map[string]any{}, "time": before}
			}
			kubelet, supervisor := writer("k3s", "status"), writer("k3s-supervisor@"+name, "status")
			snap := writer("k3s-supervisor@"+name, "")
			meta["managedFields"] = []any{kubelet, supervisor, snap}
			annotation := map[string]any{"etcd.k3s.cattle.io/local-snapshots-timestamp": before}
			meta["annotations"] = annotation
			lh := f.objects[clusterSSHInstanceNodePrefix+name]
			pkgs := map[string]any{"type": "RequiredPackages", "status": "True", "reason": "", "message": "All required packages [cryptsetup dmsetup nfs-common open-iscsi] are installed"}
			disk := map[string]any{"storageAvailable": int64(8000), "storageMaximum": int64(10000), "healthDataLastCollectedAt": before}
			lh["status"] = map[string]any{"conditions": []any{pkgs}, "diskStatus": map[string]any{"default": disk}}
			a, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			if err != nil {
				t.Fatal("initial observation", err)
			}
			switch tc.name {
			case "package order", "package removed", "package check failed", "disk sample time", "disk health":
				clusterSSHStorageMap(lh, "metadata")["resourceVersion"] = "200"
				if tc.name == "disk sample time" {
					disk["healthDataLastCollectedAt"] = after
				} else {
					pkgs["message"] = "All required packages [nfs-common open-iscsi cryptsetup dmsetup] are installed"
				}
				if tc.name == "disk health" {
					disk["conditions"] = []any{map[string]any{"type": "Ready", "status": "False"}}
				}
				if tc.name == "package removed" {
					pkgs["message"] = strings.Replace(pkgs["message"].(string), " cryptsetup", "", 1)
				}
				if tc.name == "package check failed" {
					pkgs["status"] = "False"
				}
			default:
				meta["resourceVersion"] = "201"
				meta["managedFields"] = []any{supervisor, snap, kubelet}
				switch tc.name {
				case "voter heartbeat":
					voter["lastHeartbeatTime"], supervisor["time"] = after, after
				case "snapshot time":
					annotation["etcd.k3s.cattle.io/local-snapshots-timestamp"], snap["time"] = after, after
				case "unexplained revision":
				default:
					condition["lastHeartbeatTime"], kubelet["time"] = after, after
				}
				switch tc.name {
				case "older heartbeat":
					condition["lastHeartbeatTime"] = "2026-11-21T15:00:00Z"
				case "readiness":
					condition["status"] = "False"
				case "transition":
					condition["lastTransitionTime"] = after
				case "CPU":
					clusterSSHStorageMap(status, "allocatable")["cpu"] = "4"
				case "node identity":
					meta["uid"] = newClusterUUID()
				case "role label":
					meta["labels"] = map[string]any{"borealis.io/application-state": "drained"}
				case "snapshot configuration":
					annotation["etcd.k3s.cattle.io/snapshot-policy"] = "changed"
				case "writer ownership":
					kubelet["fieldsV1"] = map[string]any{"f:spec": map[string]any{}}
				case "other writer time":
					supervisor["time"] = after
				}
			}
			b, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
			if pass := err == nil && b.sameInputs(a); pass != tc.pass {
				t.Fatalf("same inputs %v, want %v (read error %v)", pass, tc.pass, err)
			}
			if tc.pass && a.Requirements.observation != b.Requirements.observation {
				t.Fatal("worker receipt changed")
			}
		})
	}
}
