package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func memberRemovalFixture() (memberRemovalIdentity, map[string]any) {
	identity := memberRemovalIdentity{OperationID: "11111111-1111-4111-8111-111111111111", NodeID: "22222222-2222-4222-8222-222222222222", NodeName: "engine-2", NodeUID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", EtcdMemberName: "engine-2-id"}
	return identity, map[string]any{"operation_id": identity.OperationID, "node_id": identity.NodeID, "node_name": identity.NodeName, "node_uid": identity.NodeUID, "etcd_member_name": identity.EtcdMemberName}
}

func removalIdentityJSON(identity memberRemovalIdentity) string {
	raw, _ := json.Marshal(map[string]any{"metadata": map[string]any{"name": identity.NodeName, "uid": identity.NodeUID, "annotations": map[string]any{"etcd.k3s.cattle.io/node-name": identity.EtcdMemberName}}})
	return string(raw)
}

func TestMemberRemovalRejectsMissingAndMalformedIdentityBeforeHostWork(t *testing.T) {
	for _, field := range []string{"operation_id", "node_id", "node_name", "node_uid", "etcd_member_name"} {
		for _, invalid := range []any{nil, "", "wrong value\n", strings.Repeat("a", 129), 123} {
			_, params := memberRemovalFixture()
			params[field] = invalid
			m := &manager{nodeName: "engine-2"}
			if _, err := m.execute(context.Background(), actionRequest{Verb: "PrepareMemberRemoval", Params: params}); err == nil {
				t.Fatalf("accepted invalid %s=%v", field, invalid)
			}
		}
	}
}

func TestMemberRemovalClientRejectsLegacyOrMismatchedAcknowledgement(t *testing.T) {
	identity, _ := memberRemovalFixture()
	for _, changed := range []string{"", "operation_id", "node_id", "node_name", "node_uid", "etcd_member_name", "service_disabled", "fence_marker", "fence_drop_in", "k3s_state", "armed_at", "legacy"} {
		_, result := memberRemovalFixture()
		result["service_disabled"], result["k3s_state"] = true, "active"
		result["fence_marker"], result["fence_drop_in"] = memberFencePath, memberFenceDropIn
		result["armed_at"] = "2026-09-05T12:00:00Z"
		if changed == "legacy" {
			delete(result, "operation_id")
			delete(result, "node_uid")
		} else if changed != "" {
			result[changed] = "mismatch"
		}
		raw, _ := json.Marshal(map[string]any{"ok": true, "verb": "PrepareMemberRemoval", "result": result})
		err := validateRemovalAcknowledgement(raw, identity)
		if (err == nil) != (changed == "") {
			t.Fatalf("acknowledgement variant %q err=%v", changed, err)
		}
	}
}

func TestMemberRemovalFenceChecksIdentityAndPersistsAcknowledgement(t *testing.T) {
	identity, params := memberRemovalFixture()
	parsed, err := removalIdentity(params, identity.NodeName)
	if err != nil || parsed != identity {
		t.Fatalf("valid contract rejected: %#v %v", parsed, err)
	}
	root := t.TempDir()
	markerPath, dropInPath := filepath.Join(root, "borealis", "fence.json"), filepath.Join(root, "k3s.service.d", "fence.conf")
	commands := []string{}
	command := func(_ context.Context, _, executable string, args ...string) (string, error) {
		invocation := executable + " " + strings.Join(args, " ")
		commands = append(commands, invocation)
		switch invocation {
		case "k3s kubectl get node engine-2 -o json":
			return removalIdentityJSON(identity), nil
		case "systemctl show --property=LoadState --value k3s.service":
			return "loaded", nil
		case "systemctl disable k3s.service":
			var recorded memberRemovalIdentity
			raw, err := os.ReadFile(markerPath)
			if err != nil || json.Unmarshal(raw, &recorded) != nil || recorded != identity {
				t.Fatal("service fence preceded persistent identity marker")
			}
			return "", nil
		case "systemctl daemon-reload":
			return "", nil
		case "systemctl show --property=Restart --value k3s.service":
			return "no", nil
		case "systemctl is-active k3s.service":
			return "active", nil
		}
		t.Errorf("unexpected host command: %s", invocation)
		return "", errors.New("unexpected command")
	}
	for attempt := 0; attempt < 2; attempt++ {
		result, err := prepareMemberRemovalFence(context.Background(), identity, "planned pair removal", command, markerPath, dropInPath)
		if err != nil || result["service_disabled"] != true || result["node_uid"] != identity.NodeUID || result["operation_id"] != identity.OperationID {
			t.Fatalf("fence/replay acknowledgement failed: %#v %v", result, err)
		}
	}
	if commands[0] != "k3s kubectl get node engine-2 -o json" || len(commands) != 12 {
		t.Fatalf("unsafe command sequence: %v", commands)
	}
	for path, mode := range map[string]os.FileMode{markerPath: 0o600, dropInPath: 0o644} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("persistent fence mode: %s %v", path, err)
		}
	}
	changed := identity
	changed.OperationID = "33333333-3333-4333-8333-333333333333"
	before := len(commands)
	if _, err := prepareMemberRemovalFence(context.Background(), changed, "stale replay", command, markerPath, dropInPath); err == nil || len(commands) != before+1 {
		t.Fatalf("different operation replaced existing fence: err=%v commands=%v", err, commands[before:])
	}
}

func TestMemberRemovalRefusesReusedHostnameAndChangedEtcdIdentity(t *testing.T) {
	for _, field := range []string{"uid", "etcd", "missing", "invalid_json"} {
		t.Run(field, func(t *testing.T) {
			identity, _ := memberRemovalFixture()
			observed := identity
			if field == "uid" {
				observed.NodeUID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
			}
			if field == "etcd" {
				observed.EtcdMemberName = "engine-2-rejoined"
			}
			root := t.TempDir()
			calls := 0
			command := func(_ context.Context, _, _ string, _ ...string) (string, error) {
				calls++
				if field == "missing" {
					return "", errors.New("Node not found")
				}
				if field == "invalid_json" {
					return "invalid", nil
				}
				return removalIdentityJSON(observed), nil
			}
			if _, err := prepareMemberRemovalFence(context.Background(), identity, "test", command, filepath.Join(root, "fence.json"), filepath.Join(root, "fence.conf")); err == nil || calls != 1 {
				t.Fatalf("reused/missing identity reached service mutation: %v calls=%d", err, calls)
			}
			entries, _ := os.ReadDir(root)
			if len(entries) != 0 {
				t.Fatal("identity mismatch wrote fence files")
			}
		})
	}
}
