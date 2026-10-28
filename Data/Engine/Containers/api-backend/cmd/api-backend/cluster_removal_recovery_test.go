package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func removalRecoveryPair() (clusterControllerOperation, []clusterControllerNode, []clusterRemovalFence) {
	operation, first, firstFence := removalFenceFixture()
	second := clusterControllerNode{ID: "33333333-3333-4333-8333-333333333333", Name: "engine-3", ApplicationState: "drained"}
	secondFence := firstFence
	secondFence.NodeID, secondFence.NodeName = second.ID, second.Name
	secondFence.NodeUID, secondFence.EtcdMemberName = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "engine-3-id"
	setRemovalFenceRecord(operation, firstFence)
	setRemovalFenceRecord(operation, secondFence)
	survivor := clusterControllerNode{ID: "44444444-4444-4444-8444-444444444444", Name: "engine-1", ApplicationState: "active"}
	return operation, []clusterControllerNode{first, second, survivor}, []clusterRemovalFence{firstFence, secondFence}
}

func TestRemovalRecoveryRetainsPairOrderAcrossRestartAndRetry(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		name := "target-first"
		if reverse {
			name = "paired-first"
		}
		t.Run(name, func(t *testing.T) {
			operation, nodes, _ := removalRecoveryPair()
			ids := []any{nodes[0].ID, nodes[1].ID}
			if reverse {
				ids[0], ids[1] = ids[1], ids[0]
			}
			operation.Payload["removal_node_ids"] = ids
			baseline, err := clusterOperationSteps(operation, nodes)
			if err != nil {
				t.Fatal(err)
			}
			var targetOrder []string
			for _, step := range baseline {
				if strings.HasSuffix(step.Name, ":verify_member_removed") {
					targetOrder = append(targetOrder, step.NodeID)
				}
			}
			if !reflect.DeepEqual(targetOrder, []string{ids[0].(string), ids[1].(string)}) {
				t.Fatalf("stored removal order lost: got %v want %v", targetOrder, ids)
			}
			for _, leader := range []int{0, 1, 2} {
				for current := range baseline {
					// Each checkpoint is loaded by a fresh controller after role transfer.
					resumed := operation
					resumed.Payload = parseClusterJSON(marshalClusterJSON(operation.Payload))
					resumed.CurrentStep = baseline[current].Name
					changed := append([]clusterControllerNode(nil), nodes...)
					changed[leader].Roles = map[string]any{"etcd_leader": true, "postgres_primary": true}
					changed[0], changed[1] = changed[1], changed[0]
					steps, err := clusterOperationSteps(resumed, changed)
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(steps, baseline) {
						t.Fatalf("role drift changed plan at %s, leader=%s", resumed.CurrentStep, nodes[leader].Name)
					}
					wantNext := ""
					if current+1 < len(baseline) {
						wantNext = baseline[current+1].Name
					}
					if next, err := nextClusterOperationStep(resumed, steps, current); err != nil || next != wantNext {
						t.Fatalf("checkpoint %s advanced to %q, want %q: %v", resumed.CurrentStep, next, wantNext, err)
					}
					if current > 0 {
						resumed.Attempt++
						resumed.Payload["retry_resume_step"] = resumed.CurrentStep
						resumed.CurrentStep = "preflight"
						if next, err := nextClusterOperationStep(resumed, steps, 0); err != nil || next != baseline[current].Name {
							t.Fatalf("retry skipped retained checkpoint: next=%q err=%v", next, err)
						}
					}
				}
			}
		})
	}
}

func TestRemovalRecoveryRejectsMalformedDurableCohort(t *testing.T) {
	operation, nodes, _ := removalRecoveryPair()
	for name, ids := range map[string]any{
		"null":           nil,
		"empty":          []any{},
		"string":         nodes[0].ID,
		"object":         map[string]any{"node": nodes[0].ID},
		"partial_pair":   []any{nodes[0].ID},
		"extra_target":   []any{nodes[0].ID, nodes[1].ID, nodes[2].ID},
		"duplicate":      []any{nodes[0].ID, nodes[0].ID},
		"missing_member": []any{nodes[0].ID, "55555555-5555-4555-8555-555555555555"},
		"blank":          []any{nodes[0].ID, ""},
		"wrong_type":     []any{nodes[0].ID, map[string]any{"id": nodes[1].ID}},
	} {
		t.Run(name, func(t *testing.T) {
			operation.Payload["removal_node_ids"] = ids
			if _, err := clusterOperationSteps(operation, nodes); err == nil {
				t.Fatal("invalid durable cohort fell back to unrelated target fields")
			}
		})
	}
	delete(operation.Payload, "removal_node_ids")
	nodes[0].Roles = map[string]any{"etcd_leader": true}
	legacy, err := clusterRemovalNodes(operation, nodes)
	if err != nil || len(legacy) != 2 || legacy[0].ID != operation.TargetNodeID || legacy[1].ID != nodes[1].ID {
		t.Fatalf("legacy target fields did not retain stable pair order: nodes=%v err=%v", legacy, err)
	}
}

func TestRemovalRecoveryFinalCohortRejectsPartialPairAndReplacement(t *testing.T) {
	for _, step := range []string{"scale_shared_artifact_membership", "verify_quorum"} {
		for _, failure := range []string{"first_present", "second_present", "replacement", "unacknowledged", "missing_fence", "wrong_operation", "future_attempt", "api_failure"} {
			t.Run(step+"/"+failure, func(t *testing.T) {
				operation, nodes, fences := removalRecoveryPair()
				operation.Attempt = 2
				operation.CurrentStep = step
				switch failure {
				case "unacknowledged":
					fences[1].AcknowledgedAt = 0
					setRemovalFenceRecord(operation, fences[1])
				case "missing_fence":
					records := mapStringAny(operation.Payload["removal_fences"])
					delete(records, nodes[1].ID)
					operation.Payload["removal_fences"] = records
				case "wrong_operation":
					fences[1].OperationID = "55555555-5555-4555-8555-555555555555"
					setRemovalFenceRecord(operation, fences[1])
				case "future_attempt":
					fences[1].ActionAttempt = operation.Attempt + 1
					setRemovalFenceRecord(operation, fences[1])
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
					if request.Method != http.MethodGet || !strings.HasPrefix(request.URL.Path, "/api/v1/nodes/") {
						t.Errorf("partial removal reached final storage/health work: %s %s", request.Method, request.URL.Path)
						http.Error(w, "unexpected final work", http.StatusBadRequest)
						return
					}
					for index, fence := range fences {
						if request.URL.Path != "/api/v1/nodes/"+fence.NodeName {
							continue
						}
						if failure == "api_failure" && index == 1 {
							http.Error(w, "observation unavailable", http.StatusServiceUnavailable)
							return
						}
						if (failure == "first_present" && index == 0) || (failure == "second_present" && index == 1) || (failure == "replacement" && index == 1) {
							observed := removalFenceNode(fence, "Unknown")
							if failure == "replacement" {
								observed["metadata"].(map[string]any)["uid"] = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
							}
							_ = json.NewEncoder(w).Encode(observed)
							return
						}
					}
					http.NotFound(w, request)
				}))
				defer server.Close()
				runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, httpClient: server.Client()}}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := runner.Run(ctx, operation, clusterControllerStep{Name: step}, nodes); err == nil {
					t.Fatal("incomplete or stale cohort reached final removal work")
				}
			})
		}
	}
}

func TestRemovalRecoveryFinalCohortUsesPriorAcknowledgementsAfterRestart(t *testing.T) {
	operation, nodes, _ := removalRecoveryPair()
	operation.Attempt = 3
	operation.CurrentStep = "verify_quorum"
	operation.Payload = parseClusterJSON(marshalClusterJSON(operation.Payload))
	var observed []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Errorf("final proof mutated Kubernetes: %s %s", request.Method, request.URL.Path)
		}
		for _, node := range nodes[:2] {
			if request.URL.Path == "/api/v1/nodes/"+node.Name {
				observed = append(observed, node.ID)
				http.NotFound(w, request)
				return
			}
		}
		if strings.Contains(request.URL.Path, "/endpointslices") {
			if !reflect.DeepEqual(observed, []string{nodes[0].ID, nodes[1].ID}) {
				t.Errorf("completion health preceded whole-pair absence proof: %v", observed)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"endpoints": []any{
				map[string]any{"nodeName": nodes[2].Name, "conditions": map[string]any{"ready": true}},
			}}}})
			return
		}
		t.Errorf("final proof tried to recover or repeat a fence: %s", request.URL.Path)
		http.Error(w, "unexpected request", http.StatusBadRequest)
	}))
	defer server.Close()
	runner := &kubernetesClusterStepRunner{
		kube:      &kubernetesAPIClient{baseURL: server.URL, httpClient: server.Client()},
		namespace: "borealis",
		persistRemovalFence: func(context.Context, clusterControllerOperation, clusterRemovalFence) error {
			t.Error("final proof rewrote durable fence")
			return nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runner.Run(ctx, operation, clusterControllerStep{Name: operation.CurrentStep}, nodes); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(observed, []string{nodes[0].ID, nodes[1].ID}) {
		t.Fatalf("final proof omitted removal target: %v", observed)
	}
}
