package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClusterSSHIdentityRecoveryRuntimeClaimsOneReservedTarget(t *testing.T) {
	claim, _, consumer := sshIdentityRecoveryWorkerFixture(t)
	defer consumer.Close()
	for _, mode := range []string{"ready", "locked", "empty", "claim lost", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			var claims, executions atomic.Int32
			runtime := &clusterSSHIdentityRecoveryRuntime{
				ready: func() bool { return mode != "locked" },
				pending: func(context.Context) ([]clusterSSHPendingIdentityRecovery, error) {
					if mode == "empty" {
						return nil, nil
					}
					return []clusterSSHPendingIdentityRecovery{{OperationID: claim.Lease.OperationID, TargetID: claim.Lease.TargetID},
						{OperationID: newClusterUUID(), TargetID: newClusterUUID()}}, nil
				},
				claim: func(_ context.Context, operationID, targetID, holder string) (clusterSSHIdentityRecoveryClaim, error) {
					claims.Add(1)
					if operationID != claim.Lease.OperationID || targetID != claim.Lease.TargetID || !clusterUUIDRE.MatchString(holder) {
						t.Fatal("runtime changed pending identity or generated invalid holder")
					}
					if mode == "claim lost" {
						return clusterSSHIdentityRecoveryClaim{}, errClusterConflict
					}
					return claim, nil
				},
				execute: func(_ context.Context, got clusterSSHIdentityRecoveryClaim) error {
					executions.Add(1)
					if !got.valid() || got.Lease != claim.Lease {
						t.Fatal("runtime execution lost exact claim")
					}
					return nil
				},
			}
			ctx, cancel := context.WithCancel(context.Background())
			if mode == "cancelled" {
				cancel()
			} else {
				defer cancel()
			}
			if err := runtime.runOnce(ctx); err != nil {
				t.Fatal(err)
			}
			wantClaims, wantExecutions := int32(1), int32(1)
			if mode == "locked" || mode == "empty" || mode == "cancelled" {
				wantClaims, wantExecutions = 0, 0
			} else if mode == "claim lost" {
				wantExecutions = 0
			}
			if claims.Load() != wantClaims || executions.Load() != wantExecutions {
				t.Fatalf("unexpected runtime work: claims=%d executions=%d", claims.Load(), executions.Load())
			}
		})
	}
}

func TestClusterSSHIdentityRecoveryBundleAcquisitionKeepsAuthority(t *testing.T) {
	for _, mode := range []string{"success", "download", "heartbeat", "authority", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			claim, current, consumer := sshIdentityRecoveryWorkerFixture(t)
			defer consumer.Close()
			store := &clusterSSHIdentityRecoveryWorkerStoreFake{}
			if mode == "heartbeat" {
				store.renew = func(clusterSSHIdentityRecoveryClaim) error {
					if store.calls.Load() > 1 {
						return errors.New("private renewal failure")
					}
					return nil
				}
			}
			aegis := &clusterSSHIdentityRecoveryWorkerAegisFake{}
			var authorityCalls atomic.Int32
			authority := func(context.Context) (clusterSSHPreparationAuthority, error) {
				call := authorityCalls.Add(1)
				if mode == "authority" && call > 1 {
					return clusterSSHPreparationAuthority{}, errors.New("private authority failure")
				}
				value := current
				value.Cohort.ObservedAt += int64(call)
				return value, nil
			}
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			download := func(ctx context.Context, expected clusterbootstrap.Expected, scratch string) (*clusterbootstrap.Bundle, error) {
				if expected != claim.Baseline || scratch != "/private-scratch" {
					t.Fatal("download lost baseline or scratch parent")
				}
				if mode == "download" {
					return nil, errors.New("private download failure")
				}
				if mode == "cancelled" {
					cancel()
					<-ctx.Done()
					return nil, ctx.Err()
				}
				if mode == "heartbeat" {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				time.Sleep(8 * time.Millisecond)
				return &clusterbootstrap.Bundle{}, nil
			}
			bundle, err := acquireClusterSSHIdentityRecoveryBundleWithInterval(parent, store, aegis, claim, authority,
				"/private-scratch", download, 2*time.Millisecond)
			if mode == "success" {
				if err != nil || bundle == nil || store.calls.Load() < 2 || authorityCalls.Load() < 2 {
					t.Fatalf("guarded acquisition failed: %v renew=%d authority=%d", err, store.calls.Load(), authorityCalls.Load())
				}
			} else if !errors.Is(err, errClusterSSHIdentityRecoveryWorker) || bundle != nil || strings.Contains(err.Error(), "private") {
				t.Fatalf("unsafe acquisition result: bundle=%v err=%v", bundle, err)
			}
		})
	}
}

func TestClusterSSHIdentityRecoveryNodeBrokerKeepsKubernetesPrivate(t *testing.T) {
	claim, current, consumer := sshIdentityRecoveryWorkerFixture(t)
	defer consumer.Close()
	b := newClusterSSHSourceBroker(nil, nil, claim.Lease.ControllerHolder, sshBrokerTestSecret)
	var reads atomic.Int32
	b.readRecovery = func(_ context.Context, request clusterSSHSourceBrokerRequest) error {
		reads.Add(1)
		if request.Lease != claim.Lease || request.Baseline != claim.Baseline || request.Binding != claim.Sealed.binding ||
			request.Generation != claim.Sealed.generation || request.Ciphertext != claim.Sealed.ciphertext {
			t.Fatal("broker lost recovery authority binding")
		}
		return nil
	}
	server := httptest.NewServer(http.HandlerFunc(b.handle))
	defer server.Close()
	client := sshBrokerClient(t, server.URL)
	var authorityCalls atomic.Int32
	authority := func(context.Context) (clusterSSHPreparationAuthority, error) {
		value := current
		value.Cohort.ObservedAt += int64(authorityCalls.Add(1))
		return value, nil
	}
	if err := client.recoveryNodeRead(authority, claim)(context.Background(), claim); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 1 || authorityCalls.Load() != 2 {
		t.Fatalf("incomplete broker proof: reads=%d authority=%d", reads.Load(), authorityCalls.Load())
	}
}
