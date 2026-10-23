package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

var identityConsumerTestTarget = identityConsumerTarget{"engine-2", "11111111-1111-4111-8111-111111111111"}

func identityConsumerTestNode() map[string]any {
	return map[string]any{"metadata": map[string]any{
		"name": identityConsumerTestTarget.NodeName, "uid": identityConsumerTestTarget.NodeUID,
		"labels": map[string]any{"borealis.io/engine-node": "true", "borealis.io/application-state": "drained"},
	}}
}

func identityConsumerTestPod(role, hostPath string) map[string]any {
	spec := map[string]any{"nodeName": identityConsumerTestTarget.NodeName}
	if hostPath != "" {
		spec["volumes"] = []any{map[string]any{"hostPath": map[string]any{"path": hostPath}}}
	}
	return map[string]any{
		"metadata": map[string]any{"name": "fixture-pod", "namespace": "borealis", "uid": "pod-1", "labels": map[string]any{"app.kubernetes.io/name": role}},
		"spec":     spec,
	}
}

func TestIdentityConsumersObservation(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(map[string]any, map[string]any)
		want error
	}{
		{"empty", nil, nil},
		{"resident", func(_ map[string]any, list map[string]any) {
			list["items"] = []any{identityConsumerTestPod("borealis-operator", "/var/lib/rancher/k3s")}
		}, nil},
		{"distinct path prefix", func(_ map[string]any, list map[string]any) {
			list["items"] = []any{identityConsumerTestPod("unrelated", "/opt/Borealis/Engine/Services/api-backend/secrets-other")}
		}, nil},
		{"relative host path", func(_ map[string]any, list map[string]any) {
			list["items"] = []any{identityConsumerTestPod("unrelated", "secrets")}
		}, errIdentityConsumerObservation},
		{"unclean host path", func(_ map[string]any, list map[string]any) {
			list["items"] = []any{identityConsumerTestPod("unrelated", "/opt/Borealis/../Borealis")}
		}, errIdentityConsumerObservation},
		{"active API", func(_ map[string]any, list map[string]any) {
			list["items"] = []any{identityConsumerTestPod("api-backend", "")}
		}, errIdentityConsumersPresent},
		{"candidate API", func(_ map[string]any, list map[string]any) {
			list["items"] = []any{identityConsumerTestPod("api-backend-candidate", "")}
		}, errIdentityConsumersPresent},
		{"scheduler", func(_ map[string]any, list map[string]any) {
			list["items"] = []any{identityConsumerTestPod("job-scheduler-candidate", "")}
		}, errIdentityConsumersPresent},
		{"terminating worker", func(_ map[string]any, list map[string]any) {
			pod := identityConsumerTestPod("site-worker", "")
			pod["metadata"].(map[string]any)["deletionTimestamp"] = "2026-10-21T20:00:00Z"
			pod["status"] = map[string]any{"phase": "Succeeded", "conditions": []any{map[string]any{"type": "Ready", "status": "False"}}}
			list["items"] = []any{pod}
		}, errIdentityConsumersPresent},
		{"missing items", func(_ map[string]any, list map[string]any) { delete(list, "items") }, errIdentityConsumerObservation},
		{"null items", func(_ map[string]any, list map[string]any) { list["items"] = nil }, errIdentityConsumerObservation},
		{"partial list", func(_ map[string]any, list map[string]any) {
			list["metadata"] = map[string]any{"continue": "next-page"}
		}, errIdentityConsumerObservation},
		{"wrong UID", func(node map[string]any, _ map[string]any) { node["metadata"].(map[string]any)["uid"] = "replaced" }, errIdentityConsumerObservation},
		{"wrong name", func(node map[string]any, _ map[string]any) { node["metadata"].(map[string]any)["name"] = "engine-1" }, errIdentityConsumerObservation},
		{"undrained", func(node map[string]any, _ map[string]any) {
			node["metadata"].(map[string]any)["labels"].(map[string]any)["borealis.io/application-state"] = "active"
		}, errIdentityConsumerObservation},
		{"deleting node", func(node map[string]any, _ map[string]any) {
			node["metadata"].(map[string]any)["deletionTimestamp"] = "now"
		}, errIdentityConsumerObservation},
		{"missing pod spec", func(_ map[string]any, list map[string]any) {
			pod := identityConsumerTestPod("borealis-operator", "")
			delete(pod, "spec")
			list["items"] = []any{pod}
		}, errIdentityConsumerObservation},
		{"foreign pod", func(_ map[string]any, list map[string]any) {
			pod := identityConsumerTestPod("borealis-operator", "")
			pod["spec"].(map[string]any)["nodeName"] = "engine-1"
			list["items"] = []any{pod}
		}, errIdentityConsumerObservation},
		{"duplicate pod", func(_ map[string]any, list map[string]any) {
			pod := identityConsumerTestPod("borealis-operator", "")
			list["items"] = []any{pod, pod}
		}, errIdentityConsumerObservation},
	} {
		t.Run(test.name, func(t *testing.T) {
			node, list := identityConsumerTestNode(), map[string]any{"items": []any{}}
			if test.edit != nil {
				test.edit(node, list)
			}
			calls := 0
			read := func(ctx context.Context, args ...string) ([]byte, error) {
				calls++
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 15*time.Second {
					t.Fatal("observation lacks bounded deadline")
				}
				want := []string{"kubectl", "--request-timeout=5s", "get", "node", "engine-2", "-o", "json"}
				value := node
				if calls == 2 {
					want = []string{"kubectl", "--request-timeout=5s", "get", "pods", "--all-namespaces", "--field-selector", "spec.nodeName=engine-2", "--chunk-size=0", "-o", "json"}
					value = list
				}
				if !reflect.DeepEqual(args, want) {
					t.Fatalf("unexpected/mutating command: %v", args)
				}
				return json.Marshal(value)
			}
			got, err := observeIdentityConsumers(context.Background(), identityConsumerTestTarget, read)
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v; want %v", err, test.want)
			}
			if err == nil {
				if calls != 3 || !reflect.DeepEqual(got, map[string]any{"node_name": "engine-2", "node_uid": identityConsumerTestTarget.NodeUID, "pod_consumers_absent": true}) {
					t.Fatalf("missing target recheck or unsafe acknowledgement: %v calls=%d", got, calls)
				}
			} else if got != nil {
				t.Fatal("failed observation returned evidence")
			}
		})
	}
}

func TestIdentityConsumersHostPaths(t *testing.T) {
	const root = "/opt/Borealis/Engine/Services/api-backend/secrets"
	for _, path := range []string{"/", "/opt", "/opt/Borealis/Engine", root, root + "/engine_secret.txt", root + "/Auth_Tokens"} {
		t.Run(path, func(t *testing.T) {
			calls := 0
			_, err := observeIdentityConsumers(context.Background(), identityConsumerTestTarget, func(context.Context, ...string) ([]byte, error) {
				calls++
				if calls == 1 {
					return json.Marshal(identityConsumerTestNode())
				}
				pod := identityConsumerTestPod("unlabelled-helper", path)
				pod["metadata"].(map[string]any)["namespace"] = "other-namespace"
				return json.Marshal(map[string]any{"items": []any{pod}})
			})
			if !errors.Is(err, errIdentityConsumersPresent) {
				t.Fatalf("identity mount %s accepted: %v", path, err)
			}
		})
	}
}

func TestIdentityConsumersRejectInvalidAndChangingEvidence(t *testing.T) {
	for _, failAt := range []int{1, 2, 3} {
		for _, fault := range []string{"read error", "invalid JSON", "oversized", "cancel", "target replaced"} {
			t.Run(fault+string(rune('0'+failAt)), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				calls := 0
				got, err := observeIdentityConsumers(ctx, identityConsumerTestTarget, func(context.Context, ...string) ([]byte, error) {
					calls++
					if calls == failAt {
						switch fault {
						case "read error":
							return nil, errors.New("private diagnostic must not escape")
						case "invalid JSON":
							return []byte("private invalid JSON"), nil
						case "oversized":
							return []byte(strings.Repeat(" ", identityConsumerObservationLimit+1)), nil
						case "cancel":
							cancel()
						case "target replaced":
							return []byte(`{"metadata":{"uid":"replacement"},"items":[{}]}`), nil
						}
					}
					if calls == 2 {
						return []byte(`{"items":[]}`), nil
					}
					return json.Marshal(identityConsumerTestNode())
				})
				if err != errIdentityConsumerObservation || got != nil || calls != failAt {
					t.Fatalf("failed evidence accepted/leaked/retried: result=%v err=%v calls=%d", got, err, calls)
				}
			})
		}
	}
}

func TestIdentityConsumersInputAndAcknowledgement(t *testing.T) {
	valid := map[string]any{"node_name": "engine-2", "node_uid": identityConsumerTestTarget.NodeUID}
	for _, name := range []string{"", strings.Repeat("a", 64), "Engine-2", "engine-2;id", " engine-2"} {
		if _, err := parseIdentityConsumerTarget(map[string]any{"node_name": name, "node_uid": identityConsumerTestTarget.NodeUID}, name); err == nil {
			t.Fatalf("invalid identifier accepted: %q", name)
		}
	}
	for _, bad := range []map[string]any{nil, {}, {"node_name": "engine-2"}, {"node_name": "engine-1", "node_uid": identityConsumerTestTarget.NodeUID}, {"node_name": "engine-2", "node_uid": 123}, {"node_name": "engine-2", "node_uid": "00000000-0000-0000-0000-000000000000"}, {"node_name": "engine-2", "node_uid": identityConsumerTestTarget.NodeUID, "path": "/tmp"}} {
		m := manager{nodeName: "engine-2"}
		if _, err := m.execute(context.Background(), actionRequest{Verb: "InspectIdentityConsumers", Params: bad}); err != errIdentityConsumerObservation {
			t.Fatalf("invalid input reached command or missing action dispatch: %v", err)
		}
	}
	if got, err := parseIdentityConsumerTarget(valid, "engine-2"); err != nil || got != identityConsumerTestTarget {
		t.Fatal("valid target rejected")
	}
	if nodeManagerActionTimeout("InspectIdentityConsumers") != 15*time.Second {
		t.Fatal("unbounded action")
	}
	good := `{"ok":true,"verb":"InspectIdentityConsumers","result":{"node_name":"engine-2","node_uid":"11111111-1111-4111-8111-111111111111","pod_consumers_absent":true}}`
	if validateIdentityConsumerAcknowledgement([]byte(good), identityConsumerTestTarget) != nil {
		t.Fatal("valid acknowledgement rejected")
	}
	for _, bad := range []string{`{"ok":true}`, `{}`, strings.Replace(good, "engine-2", "engine-1", 1), strings.Replace(good, "11111111", "22222222", 1), strings.Replace(good, "Consumers", "Health", 1), strings.ReplaceAll(good, "true", "false")} {
		if validateIdentityConsumerAcknowledgement([]byte(bad), identityConsumerTestTarget) == nil {
			t.Fatal("legacy/unbound/failed acknowledgement accepted")
		}
	}
}

func TestIdentityConsumersOutputIsBounded(t *testing.T) {
	output := &identityConsumerOutput{}
	_, err := io.Copy(output, strings.NewReader(strings.Repeat("x", identityConsumerObservationLimit+1)))
	if !errors.Is(err, errIdentityConsumerObservation) || output.buffer.Len() > identityConsumerObservationLimit {
		t.Fatal("command output bypassed bound")
	}
}
