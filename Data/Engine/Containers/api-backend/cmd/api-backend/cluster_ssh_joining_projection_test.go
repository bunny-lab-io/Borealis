package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func sshJoiningStorageFixture(t *testing.T, replacement bool) *sshStorageFixture {
	t.Helper()
	a, _, _ := sshPrejoinSourceFixture(t, replacement)
	f := newSSHStorageFixtureForAuthority(t, a)
	list := f.objects["/api/v1/nodes"]
	list["apiVersion"], list["kind"] = "v1", "NodeList"
	for _, value := range list["items"].([]any) {
		node := value.(map[string]any)
		node["apiVersion"], node["kind"] = "v1", "Node"
		node["metadata"].(map[string]any)["labels"] = map[string]any{"borealis.io/engine-node": "true", "borealis.io/application-state": "active"}
	}
	f.objects[clusterSSHRuntimeSecretPath] = sshPreparationSecretFixture()
	return f
}

func TestClusterSSHJoiningProjectionAcrossPreparation(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "appears", "replaced", "disappears", "source inactive", "unknown", "qualification", "zero namespace", "authority drift"} {
		t.Run(mode, func(t *testing.T) {
			f := sshJoiningStorageFixture(t, mode == "replacement")
			items := f.objects["/api/v1/nodes"]["items"].([]any)
			target := f.a.Cohort.Targets[0]
			node := sshJoinedNodeFixture(target, f.a.K3sVersion, newClusterUUID())
			delete(node, "status") // Deliberate initial registration, not ready.
			if mode != "appears" {
				items = append(items, node)
			}
			f.objects["/api/v1/nodes"]["items"] = items
			if mode == "source inactive" {
				items[0].(map[string]any)["metadata"].(map[string]any)["labels"].(map[string]any)["borealis.io/application-state"] = "drained"
			}
			if mode == "unknown" {
				node["metadata"].(map[string]any)["name"] = "foreign"
			}
			if mode == "qualification" {
				f.a.Lease.OperationStep, f.a.Lease.Step = clusterSSHQualificationStep, "qualify"
			}
			if mode == "zero namespace" {
				f.a.Source.KubeSystemUID = "00000000-0000-0000-0000-000000000000"
			}
			get := newClusterSSHJoiningProjection(f.authority, f.get)
			consumed := false
			err := withClusterSSHSourceStorage(context.Background(), f.authority, get, func(ctx context.Context, storage clusterSSHStorageRequirements, checks clusterSSHPreparationChecks) error {
				consumed = true
				f.mu.Lock()
				switch mode {
				case "appears":
					f.objects["/api/v1/nodes"]["items"] = append(items, node)
				case "replaced":
					node["metadata"].(map[string]any)["uid"] = newClusterUUID()
				case "disappears":
					f.objects["/api/v1/nodes"]["items"] = items[:len(items)-1]
				case "authority drift":
					f.a.Lease.Generation++
				}
				f.mu.Unlock()
				read := newClusterSSHPreparationSnapshotRead(f.authority, get, func(ctx context.Context, member clusterSSHSourceMember) (clusterbootstrap.SourceNetwork, error) {
					return sshSourceNetworkFixture(member, f.a.K3sVersion), nil
				})
				value, err := read(ctx)
				if err != nil {
					return err
				}
				if value.Expected.Target.TargetID != target.Binding.TargetID || len(value.Sources) != len(f.a.Source.Members) {
					t.Fatal("wrong source projection")
				}
				return checks.Inputs(ctx)
			})
			valid := mode == "pair" || mode == "replacement" || mode == "appears"
			if (err == nil) != valid || valid && !consumed {
				t.Fatal("wrong integrated joining projection", err, consumed)
			}
			if valid {
				if _, err := observeClusterSSHSourceCohort(context.Background(), f.get, f.a.Source); err == nil {
					t.Fatal("strict entrypoint accepted new Node")
				}
			}
		})
	}
}

func TestClusterSSHJoiningProjectionOwnsAndLatches(t *testing.T) {
	f := sshJoiningStorageFixture(t, false)
	node := sshJoinedNodeFixture(f.a.Cohort.Targets[0], f.a.K3sVersion, newClusterUUID())
	list := f.objects["/api/v1/nodes"]
	list["items"] = append(list["items"].([]any), node)
	projection := newClusterSSHJoiningProjection(f.authority, f.get)
	var first map[string]any
	if projection(context.Background(), "/api/v1/nodes", &first) != nil {
		t.Fatal("initial")
	}
	// Output mutation cannot alter the pinned cohort or prior registrations.
	first["items"] = nil
	uid := node["metadata"].(map[string]any)["uid"]
	node["metadata"].(map[string]any)["uid"] = newClusterUUID()
	if projection(context.Background(), "/api/v1/nodes", &first) == nil {
		t.Fatal("replacement")
	}
	node["metadata"].(map[string]any)["uid"] = uid
	if projection(context.Background(), "/api/v1/nodes", &first) == nil {
		t.Fatal("ignored failure resumed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if projection(ctx, "/api/v1/nodes", &first) == nil {
		t.Fatal("cancelled read")
	}
	// Fresh scope still validates actual source members rather than canned output.
	fresh := newClusterSSHJoiningProjection(f.authority, f.get)
	got, err := observeClusterSSHSourceCohort(context.Background(), fresh, f.a.Source)
	if err != nil || !reflect.DeepEqual(got, f.a.Source) {
		t.Fatal("fresh source", err)
	}
	var raw json.RawMessage
	if fresh(context.Background(), clusterSSHRuntimeSecretPath, &raw) != nil || len(raw) == 0 {
		t.Fatal("fixed non-Node path changed")
	}
}
