package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestClusterSSHPreparationNativeGatesIncompleteEvidence(t *testing.T) {
	for _, clean := range []bool{false, true} {
		t.Run(map[bool]string{false: "executor unavailable", true: "executor available"}[clean], func(t *testing.T) {
			want := []string{"complete_capacity", "persistent_storage"}
			if !clean {
				want = append([]string{"clean_targets"}, want...)
				want = append(want, "supervised_recovery")
			}
			blocked := clusterSSHPreparationNativeBlockers(clean)
			var got []string
			for _, item := range blocked {
				got = append(got, item.Gate)
				if item.Reason == "" || !errors.Is(item, errClusterConflict) {
					t.Fatal("missing actionable conflict", item)
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("wrong native gaps: got %v want %v", got, want)
			}
			blocked[0].Reason = "caller substitution"
			if clusterSSHPreparationNativeBlockers(clean)[0].Reason == blocked[0].Reason {
				t.Fatal("caller changed future blocker inventory")
			}
			joined := clusterSSHPreparationNativeBlockedError(clean)
			if !errors.Is(joined, errClusterConflict) {
				t.Fatal("native gaps lost conflict classification")
			}
			for _, code := range want {
				if !strings.Contains(joined.Error(), code+": ") {
					t.Fatal("joined result hid missing gate", code)
				}
			}
		})
	}
}

func TestClusterSSHPreparationNativeGatesJoinEscapedSupervision(t *testing.T) {
	snapshot, _ := sshPreparationCleanTargetsFixture(t, false)
	plan, _ := clusterSSHPreparationPlanDigest(snapshot.work.Authorization)
	started, cancelled, release, joined := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	gates := sshPreparationGatesFixture()
	gates[5] = newClusterSSHPreparationSupervisedRecoveryRead(snapshot, gates[0], func(ctx context.Context, _ string) (string, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-release // Model a target transport whose Close must finish before exit.
		close(joined)
		return "", ctx.Err()
	})
	var escaped func(context.Context, string) (string, error)
	done := make(chan error, 1)
	readerDone := make(chan error, 1)
	go func() {
		_, err := consumeClusterSSHPreparationNativeGates(context.Background(), gates,
			func(ctx context.Context, scoped clusterSSHPreparationGates) (clusterSSHPreparationReadinessReceipt, error) {
				escaped = scoped[5]
				go func() { _, err := scoped[5](context.Background(), plan); readerDone <- err }()
				<-started
				return clusterSSHPreparationReadinessReceipt{}, nil
			})
		done <- err
	}()
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("scope exit did not cancel escaped target reader")
	}
	select {
	case <-done:
		t.Fatal("artifact ownership ended before target cleanup")
	case <-time.After(20 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	select {
	case err := <-done:
		if !errors.Is(err, errClusterConflict) {
			t.Fatal("escaped supervision reported success", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("native callback cleanup did not join")
	}
	<-joined
	if err := <-readerDone; err == nil {
		t.Fatal("cancelled target proof survived scope exit")
	}
	if digest, err := escaped(context.Background(), plan); digest != "" || err == nil {
		t.Fatal("closed native gate reached target reader")
	}
}

func TestClusterSSHPreparationNativeGatesRetainReceiptAndFailures(t *testing.T) {
	for _, ignored := range []bool{false, true} {
		gates := sshPreparationGatesFixture()
		plan := strings.Repeat("a", 64)
		if ignored {
			gates[3] = func(context.Context, string) (string, error) {
				return "", clusterSSHPreparationGateBlocked{"persistent_storage", "Missing native filesystem evidence."}
			}
		}
		var want clusterSSHPreparationReadinessReceipt
		got, err := consumeClusterSSHPreparationNativeGates(context.Background(), gates,
			func(ctx context.Context, scoped clusterSSHPreparationGates) (clusterSSHPreparationReadinessReceipt, error) {
				var receipts [6]string
				for i, gate := range scoped {
					receipts[i], _ = gate(ctx, plan)
				}
				want = clusterSSHPreparationReadiness(plan, receipts)
				return want, nil
			})
		if (err != nil) != ignored || !reflect.DeepEqual(got, want) {
			t.Fatal("native scope changed exact receipt or lost ignored failure", err)
		}
	}
}

func TestClusterSSHPreparationNativeGatesPreserveNativeReaders(t *testing.T) {
	snapshot, _ := sshPreparationCleanTargetsFixture(t, false)
	plan, _ := clusterSSHPreparationPlanDigest(snapshot.work.Authorization)
	var calls [6]int
	reader := func(index int) func(context.Context, string) (string, error) {
		return func(ctx context.Context, requested string) (string, error) {
			if ctx.Err() != nil || requested != plan {
				t.Fatal("native reader lost original context or plan")
			}
			calls[index]++
			return strings.Repeat(string("abcdef"[index]), 64), nil
		}
	}
	gates := clusterSSHPreparationNativeGates(snapshot, reader(0), reader(1), reader(4))
	for i, gate := range gates {
		if gate == nil {
			t.Fatal("gate omitted instead of explicit blocker", i)
		}
		digest, err := gate(context.Background(), plan)
		if i == 0 || i == 1 || i == 4 {
			if err != nil || digest != strings.Repeat(string("abcdef"[i]), 64) || calls[i] != 1 {
				t.Fatal("native callback replaced", i)
			}
			continue
		}
		if i == 5 {
			if err != nil || !validClusterSSHPreparationDigest(digest) || calls[0] != 2 || calls[1] != 2 {
				t.Fatal("supervision omitted fresh immutable/native prerequisites", err)
			}
			continue
		}
		var blocked clusterSSHPreparationGateBlocked
		if digest != "" || !errors.As(err, &blocked) || blocked.Gate != clusterSSHPreparationGateCodes[i] {
			t.Fatal("incomplete evidence returned receipt or wrong gate", i, err)
		}
	}
	consumed := false
	err := withClusterSSHPreparationApprovalProof(context.Background(), plan, gates, func(context.Context) error { return nil },
		func(context.Context, clusterSSHPreparationApprovalProof) error { consumed = true; return nil })
	var blocked clusterSSHPreparationGateBlocked
	if consumed || !errors.As(err, &blocked) || blocked.Gate != "complete_capacity" {
		t.Fatal("partial native set reached approval", err)
	}
	missing := clusterSSHPreparationNativeGates(snapshot, reader(0), nil, reader(4))
	if digest, err := missing[1](context.Background(), plan); digest != "" || !errors.As(err, &blocked) || blocked.Gate != "clean_targets" {
		t.Fatal("missing executor gained clean-target proof", err)
	}
}

func TestClusterSSHPreparationNativeGatesPreciseErrors(t *testing.T) {
	for _, mode := range []string{"missing", "private failure", "native blocker", "recheck blocker"} {
		t.Run(mode, func(t *testing.T) {
			gates := sshPreparationGatesFixture()
			plan := strings.Repeat("a", 64)
			calls := 0
			gates[3] = func(context.Context, string) (string, error) {
				calls++
				if mode == "recheck blocker" && calls == 1 {
					return strings.Repeat("d", 64), nil
				}
				if mode == "private failure" {
					return "", errors.New("private settings and transport detail")
				}
				return "", clusterSSHPreparationGateBlocked{"persistent_storage", "Fresh filesystem evidence is unavailable."}
			}
			if mode == "missing" {
				gates[3] = nil
			}
			consumed := false
			err := withClusterSSHPreparationApprovalProof(context.Background(), plan, gates, func(context.Context) error { return nil },
				func(ctx context.Context, proof clusterSSHPreparationApprovalProof) error {
					consumed = true
					return proof.recheck(ctx)
				})
			var blocked clusterSSHPreparationGateBlocked
			if !errors.Is(err, errClusterConflict) || !errors.As(err, &blocked) || blocked.Gate != "persistent_storage" || strings.Contains(err.Error(), "private settings") {
				t.Fatal("gate failure lost safe precise diagnosis", err)
			}
			if consumed != (mode == "recheck blocker") {
				t.Fatal("failed initial proof reached consumer")
			}
			if (mode == "native blocker" || mode == "recheck blocker") && blocked.Reason != "Fresh filesystem evidence is unavailable." {
				t.Fatal("native actionable reason discarded")
			}
		})
	}
}
