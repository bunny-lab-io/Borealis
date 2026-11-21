package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestClusterSSHPreparationRuntimeDelegation(t *testing.T) {
	for _, mode := range []string{"approved", "locked", "missing", "claim failed", "cancelled", "dispatch uncertain"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			pending := clusterSSHPendingPreparation{Operation: clusterControllerOperation{ID: newClusterUUID(), Kind: "ssh_onboarding", State: "running", CurrentStep: clusterSSHPreparationOperationStep, Attempt: 2}, Controller: "original-controller"}
			claims, calls := 0, 0
			r := clusterSSHPreparationRuntime{
				ready: func() bool { return mode != "locked" },
				pending: func(context.Context) (clusterSSHPendingPreparation, error) {
					if mode == "missing" {
						return clusterSSHPendingPreparation{}, nil
					}
					return pending, nil
				},
				claim: func(ctx context.Context, op clusterControllerOperation, controller, holder string) (clusterSSHPreparationWork, error) {
					claims++
					if op.ID != pending.Operation.ID || controller != pending.Controller || !clusterUUIDRE.MatchString(holder) {
						t.Fatal("delegation identity changed")
					}
					if mode == "claim failed" {
						return clusterSSHPreparationWork{}, errClusterConflict
					}
					return clusterSSHPreparationWork{Payload: "exact approved work"}, nil
				},
				execute: func(ctx context.Context, work clusterSSHPreparationWork) error {
					calls++
					if work.Payload != "exact approved work" {
						t.Fatal("worker input changed")
					}
					if mode == "dispatch uncertain" {
						return errClusterConflict
					}
					return nil
				},
			}
			err := r.runOnce(ctx)
			want := mode == "approved" || mode == "dispatch uncertain"
			if (calls == 1) != want || claims > 1 || calls > 1 {
				t.Fatal("unexpected claim/dispatch", claims, calls)
			}
			if (err != nil) != (mode == "claim failed" || mode == "dispatch uncertain") {
				t.Fatal("outcome", err)
			}
		})
	}
}

func TestClusterSSHPreparationBlockedDiagnostics(t *testing.T) {
	private := errors.New("private-ssh-diagnostic")
	err := errors.Join(private, clusterSSHPreparationGateBlocked{Gate: "complete_capacity", Reason: "private-host-detail"}, clusterSSHPreparationGateBlocked{Gate: "persistent_storage", Reason: "private-disk-detail"})
	reasons := clusterSSHPreparationBlockedReasons(err)
	if len(reasons) != 2 || strings.Contains(strings.Join(reasons, ";"), "private") {
		t.Fatal("diagnostics leaked or lost", reasons)
	}
	if len(clusterSSHPreparationBlockedReasons(private)) != 0 {
		t.Fatal("private failure promoted into gate reason")
	}
}
