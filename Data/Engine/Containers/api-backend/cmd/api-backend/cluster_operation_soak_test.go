package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestClusterOperationSoakTimingAndInterruption(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		t.Run(map[bool]string{false: "elapsed", true: "interrupted"}[interrupted], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			start := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
			elapsed, observed := make(chan time.Time, 1), make(chan time.Time, 1)
			var events []string
			var recorded clusterOperationSoak
			runner := &kubernetesClusterStepRunner{controllerHolder: "soak-controller"}
			runner.recordOperationSoak = func(callCtx context.Context, run clusterOperationSoak, event string) error {
				if _, ok := callCtx.Deadline(); !ok {
					t.Error("unbounded event callback")
				}
				if recorded.RunID != "" && recorded != run {
					t.Error("timer identity changed")
				}
				recorded = run
				events = append(events, event)
				switch event {
				case "operation_soak_started":
					observed <- start
				case "operation_soak_observed":
					elapsed <- start.Add(30 * time.Second)
					if interrupted {
						cancel()
					}
				}
				return nil
			}
			ctx = runner.soakContext(ctx, clusterControllerOperation{ID: newClusterUUID(), Attempt: 2}, clusterControllerStep{Name: "node:test:minimum_ready_soak"})
			err := waitClusterOperationSoak(ctx, "ready", 30*time.Second, func() time.Time { return start }, elapsed, observed)
			last := "operation_soak_elapsed"
			if interrupted {
				last = "operation_soak_interrupted"
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(events, []string{"operation_soak_started", "operation_soak_observed", last}) {
				t.Fatal(events)
			}
			if recorded.StartedAtMS != start.UnixMilli() || recorded.DeadlineAtMS != start.Add(30*time.Second).UnixMilli() || recorded.Attempt != 2 || !clusterUUIDRE.MatchString(recorded.RunID) {
				t.Fatal(recorded)
			}
		})
	}
}

func TestClusterOperationSoakPersistenceDoesNotRestartTimer(t *testing.T) {
	var runs []string
	for range 2 {
		elapsed := make(chan time.Time, 1)
		runner := &kubernetesClusterStepRunner{recordOperationSoak: func(_ context.Context, run clusterOperationSoak, event string) error {
			if event == "operation_soak_started" {
				runs = append(runs, run.RunID)
				elapsed <- time.Now()
			}
			return errors.New("event store unavailable")
		}}
		ctx := runner.soakContext(context.Background(), clusterControllerOperation{ID: newClusterUUID(), Attempt: 1}, clusterControllerStep{Name: "minimum_ready_soak"})
		if err := waitClusterOperationSoak(ctx, "ready", 30*time.Second, time.Now, elapsed, make(chan time.Time)); err != nil {
			t.Fatal(err)
		}
	}
	if len(runs) != 2 || runs[0] == runs[1] {
		t.Fatal("restart reused timer identity", runs)
	}
}

func TestClusterOperationSoakRunnerPreservesHealthChecks(t *testing.T) {
	for _, failAt := range []int64{0, 1, 2} {
		t.Run(map[int64]string{0: "healthy", 1: "before_wait", 2: "after_wait"}[failAt], func(t *testing.T) {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				ready := requests.Add(1) != failAt
				_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"endpoints": []any{map[string]any{"nodeName": "engine-1", "conditions": map[string]any{"ready": ready}}}}}})
			}))
			defer server.Close()
			node := clusterControllerNode{ID: newClusterUUID(), Name: "engine-1"}
			op := clusterControllerOperation{ID: newClusterUUID(), Kind: "node_maintenance", Attempt: 2, CurrentStep: "node:" + node.ID + ":minimum_ready_soak"}
			var events []string
			runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, token: "test", httpClient: server.Client()}, namespace: "borealis", soak: time.Nanosecond,
				recordOperationSoak: func(_ context.Context, run clusterOperationSoak, event string) error {
					if run.OperationID != op.ID || run.Step != op.CurrentStep || run.Attempt != op.Attempt {
						t.Error("runner lost parent binding", run)
					}
					events = append(events, event)
					return errors.New("observation storage unavailable")
				}}
			err := runner.Run(context.Background(), op, clusterControllerStep{Name: op.CurrentStep, NodeID: node.ID}, []clusterControllerNode{node})
			if (err != nil) != (failAt != 0) {
				t.Fatal("timing replaced health result", err)
			}
			if failAt == 1 {
				if requests.Load() != 1 || len(events) != 0 {
					t.Fatal("timer started before health proof", requests.Load(), events)
				}
			} else if requests.Load() != 2 || !reflect.DeepEqual(events, []string{"operation_soak_started", "operation_soak_elapsed"}) {
				t.Fatal(requests.Load(), events)
			}
		})
	}
}
