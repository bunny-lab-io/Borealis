package main

import (
	"context"
	"strconv"
	"testing"
)

func TestClusterSSHStorageUsageRechecks(t *testing.T) {
	const before = int64(22754099200)
	const after = int64(22858956800)
	message := func(n int64) string {
		return "Actual space usage condition failed: CurrentAvailable = " + strconv.FormatInt(n, 10) + " (StorageAvailable - Required) is less than or equal to MinimalAvailable = 31555993600 (25% of Storage Max)."
	}
	for _, tc := range []struct {
		name string
		pass bool
	}{
		{"node and volume telemetry", true}, {"unexplained revision", false}, {"volume revision alone", false}, {"status write alone", false}, {"changed field ownership", false}, {"other manager timestamp", false}, {"disk identity", false}, {"disk reserve", false}, {"disk health", false}, {"unknown disk message", false}, {"volume ownership", false}, {"volume attachment", false}, {"replica count", false}, {"volume robustness", false}, {"invalid available", false}, {"invalid actual size", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSSHStorageFixture(t, false)
			node := f.objects[clusterSSHInstanceNodePrefix+f.a.Source.Members[0].Name]
			cond := map[string]any{"type": "Schedulable", "status": "False", "reason": "DiskPressure", "message": message(before)}
			disk := map[string]any{"diskUUID": "disk-A", "storageAvailable": before, "storageMaximum": int64(126223974400), "conditions": []any{cond}}
			node["status"] = map[string]any{"diskStatus": map[string]any{"original": disk}}
			vol := f.volume(0)
			status := clusterSSHStorageMap(vol, "status")
			status["actualSize"] = 104857600
			writer := map[string]any{"manager": "longhorn-manager", "subresource": "status", "operation": "Update", "apiVersion": "longhorn.io/v1beta2", "fieldsType": "FieldsV1", "time": "2026-11-20T00:00:00Z", "fieldsV1": map[string]any{"f:status": map[string]any{"f:actualSize": map[string]any{}}}}
			other := map[string]any{"manager": "longhorn-manager", "operation": "Update", "time": "2026-11-20T00:00:00Z"}
			clusterSSHStorageMap(vol, "metadata")["managedFields"] = []any{other, writer}
			originalRevision := clusterSSHStorageMap(vol, "metadata")["resourceVersion"]
			rounds := 0
			read := func(ctx context.Context, path string, out any) error {
				if path == clusterSSHStoragePostgresPath {
					rounds++
					if rounds == 2 {
						f.mu.Lock()
						clusterSSHStorageMap(node, "metadata")["resourceVersion"] = "2"
						clusterSSHStorageMap(vol, "metadata")["resourceVersion"] = "2"
						disk["storageAvailable"], cond["message"], status["actualSize"] = after, message(after), 104861696
						writer["time"] = "2026-11-20T00:00:08Z"
						switch tc.name {
						case "status write alone":
							clusterSSHStorageMap(vol, "metadata")["resourceVersion"] = originalRevision
							status["actualSize"] = 104857600
						case "changed field ownership":
							writer["fieldsV1"] = map[string]any{}
						case "other manager timestamp":
							other["time"] = writer["time"]
						case "unexplained revision":
							disk["storageAvailable"], cond["message"] = before, message(before)
						case "volume revision alone":
							status["actualSize"] = 104857600
						case "disk identity":
							disk["diskUUID"] = "disk-B"
						case "disk reserve":
							disk["storageMaximum"] = int64(120000000000)
						case "disk health":
							cond["status"] = "True"
						case "unknown disk message":
							cond["message"] = message(after) + " other condition"
						case "volume ownership":
							clusterSSHStorageMap(vol, "metadata")["uid"] = newClusterUUID()
						case "volume attachment":
							status["currentNodeID"] = "other-node"
						case "replica count":
							clusterSSHStorageMap(vol, "spec")["numberOfReplicas"] = 3
						case "volume robustness":
							status["robustness"] = "faulted"
						case "invalid available":
							disk["storageAvailable"] = -1
						case "invalid actual size":
							status["actualSize"] = "invalid"
						}
						f.mu.Unlock()
					}
				}
				return f.get(ctx, path, out)
			}
			consumed := false
			err := withClusterSSHSourceStorage(context.Background(), f.authority, read, func(_ context.Context, _ clusterSSHStorageRequirements, _ clusterSSHPreparationChecks) error {
				consumed = true
				return nil
			})
			if consumed != tc.pass || (err == nil) != tc.pass {
				t.Fatalf("consumed=%t err=%v", consumed, err)
			}
		})
	}
}
