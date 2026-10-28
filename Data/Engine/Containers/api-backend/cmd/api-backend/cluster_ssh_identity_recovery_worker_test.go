package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

type clusterSSHIdentityRecoveryWorkerStoreFake struct {
	renew     func(clusterSSHIdentityRecoveryClaim) error
	complete  func(clusterSSHIdentityRecoveryClaim) error
	require   func(clusterSSHIdentityRecoveryClaim) error
	calls     atomic.Int32
	completed atomic.Int32
	required  atomic.Int32
}

func (s *clusterSSHIdentityRecoveryWorkerStoreFake) renewClusterSSHIdentityRecoveryTarget(_ context.Context, claim clusterSSHIdentityRecoveryClaim) error {
	s.calls.Add(1)
	if s.renew != nil {
		return s.renew(claim)
	}
	return nil
}

func (s *clusterSSHIdentityRecoveryWorkerStoreFake) completeClusterSSHIdentityRecovery(_ context.Context, claim clusterSSHIdentityRecoveryClaim) error {
	s.completed.Add(1)
	if s.complete != nil {
		return s.complete(claim)
	}
	return nil
}

func (s *clusterSSHIdentityRecoveryWorkerStoreFake) requireClusterSSHIdentityRecovery(_ context.Context, claim clusterSSHIdentityRecoveryClaim) error {
	s.required.Add(1)
	if s.require != nil {
		return s.require(claim)
	}
	return nil
}

type clusterSSHIdentityRecoveryWorkerAegisFake struct {
	envelope clusterSSHCredentialEnvelope
	verify   func(string) error
	open     func(sealedClusterSSHCredentials, clusterSSHCredentialBinding) error
	checks   atomic.Int32
}

func (a *clusterSSHIdentityRecoveryWorkerAegisFake) verifyClusterSSHGeneration(_ context.Context, generation string) error {
	a.checks.Add(1)
	if a.verify != nil {
		return a.verify(generation)
	}
	return nil
}

func (a *clusterSSHIdentityRecoveryWorkerAegisFake) openClusterSSHCredentials(_ context.Context, sealed sealedClusterSSHCredentials, binding clusterSSHCredentialBinding) (clusterSSHCredentialEnvelope, error) {
	if a.open != nil {
		if err := a.open(sealed, binding); err != nil {
			return clusterSSHCredentialEnvelope{}, err
		}
	}
	return a.envelope, nil
}

func sshIdentityRecoveryWorkerFixture(t *testing.T) (clusterSSHIdentityRecoveryClaim, clusterSSHPreparationAuthority, *engineidentity.Consumer) {
	t.Helper()
	cohort, source, lease, baseline := sshPreparationFixture(t)
	target := cohort.Targets[0]
	lease.OperationStep = clusterSSHIdentityRecoveryOperationStep
	lease.Step = clusterSSHIdentityRecoveryTargetStep
	lease.Generation = target.Generation + 1
	recovery := clusterbootstrap.IdentityRecoveryRequest{SourceID: source.Members[0].NodeID, SourceNodeUID: source.Members[0].NodeUID, TargetNodeUID: newClusterUUID()}
	var payload map[string]any
	if json.Unmarshal(sshPreparationProofPayload(t, clusterSSHParentProof{Version: 1, Cohort: cohort, Source: source}, baseline), &payload) != nil {
		t.Fatal("parse worker payload")
	}
	confirmation := strings.Repeat("a", 64)
	receipt := clusterSSHPreparationReceipt{Version: 1, Attempt: lease.OperationAttempt, ConfirmationSHA256: confirmation}
	for _, prepared := range cohort.Targets {
		preparationLease := lease
		preparationLease.TargetID = prepared.Binding.TargetID
		preparationLease.Generation = prepared.Generation + 1
		preparationLease.OperationStep = clusterSSHPreparationOperationStep
		preparationLease.Step = "stage_source"
		receipt.Targets = append(receipt.Targets, clusterSSHPreparationResultFixture(preparationLease, confirmation))
	}
	payload["ssh_preparation"] = receipt
	payload["ssh_identity_recovery"] = recovery
	claim := clusterSSHIdentityRecoveryClaim{Lease: lease, Key: target.Key, Recovery: recovery, Inspected: target, Cohort: cohort, Topology: source, payload: marshalClusterJSON(payload),
		Source: engineidentity.Binding{ClusterID: source.ClusterID, SourceID: recovery.SourceID, SourceUID: recovery.SourceNodeUID}, Baseline: baseline,
		Sealed: sealedClusterSSHCredentials{binding: target.Binding, generation: "generation-1", ciphertext: aegisEnvelopePrefix + "fixture"}}
	claim.Binding = clusterbootstrap.SessionBinding{ClusterID: target.Binding.ClusterID, OperationID: lease.OperationID, TargetID: lease.TargetID,
		HolderID: lease.Holder, Generation: lease.Generation, OperationAttempt: lease.OperationAttempt, Address: target.Binding.Address, Port: target.Binding.Port,
		Hostname: target.Report.Hostname, MachineID: target.Report.MachineID, HostKeyAlgorithm: target.Key.Algorithm, HostKeyFingerprint: target.Key.Fingerprint}
	if !claim.valid() {
		t.Fatal("invalid worker claim fixture")
	}
	root := t.TempDir()
	sshIdentitySourceMaterial(t, root)
	consumer, err := engineidentity.AcquireConsumer(root)
	if err != nil {
		t.Fatal(err)
	}
	recoveryAuthority := &clusterSSHIdentityRecoveryAuthorityBinding{Recovery: recovery, Source: claim.Source, Target: target}
	return claim, clusterSSHPreparationAuthority{Cohort: cohort, Source: source, Lease: lease, Baseline: baseline, K3sVersion: "v1.36.3+k3s1", Recovery: recoveryAuthority}, consumer
}

func TestClusterSSHIdentityRecoveryWorkerComposesClaimAndSafeRelease(t *testing.T) {
	for _, mode := range []string{"delivered", "ambiguous delivery"} {
		t.Run(mode, func(t *testing.T) {
			claim, current, consumer := sshIdentityRecoveryWorkerFixture(t)
			defer consumer.Close()
			store := &clusterSSHIdentityRecoveryWorkerStoreFake{renew: func(got clusterSSHIdentityRecoveryClaim) error {
				if !got.valid() || got.Lease != claim.Lease || got.Recovery != claim.Recovery {
					t.Fatal("renewed changed recovery claim")
				}
				return nil
			}}
			aegis := &clusterSSHIdentityRecoveryWorkerAegisFake{envelope: clusterSSHCredentialEnvelope{Version: 1, Binding: claim.Sealed.binding,
				Username: "nicole", Method: "password", Password: "credential-secret", SudoPassword: "sudo-secret"}}
			var cleanup atomic.Int32
			worker := &clusterSSHIdentityRecoveryWorker{store: store, aegis: aegis, consumer: consumer,
				bundleClose: func(*clusterbootstrap.Bundle) error { cleanup.Add(1); return nil },
				bundleProof: func(*clusterbootstrap.Bundle) (clusterbootstrap.Expected, error) { return claim.Baseline, nil }}
			var opened, delivered, released, authorityCalls, nodeCalls atomic.Int32
			worker.connect = func(_ context.Context, target clusterremote.Target, key clusterremote.HostKey, credential *clusterremote.Credential) (clusterSSHIdentityRecoveryClient, error) {
				if target.Address != claim.Binding.Address || target.Port != claim.Binding.Port || key.Fingerprint != claim.Key.Fingerprint || credential == nil {
					t.Fatal("connection lost target, key or credential binding")
				}
				call := opened.Add(1)
				client := &clusterSSHIdentityDispatchClient{}
				if call == 1 {
					client.deliver = func(context.Context, *engineidentity.Material) error {
						delivered.Add(1)
						if mode == "ambiguous delivery" {
							return errors.New("private lost acknowledgement")
						}
						return nil
					}
				} else {
					client.release = func(context.Context, *engineidentity.Material) error { released.Add(1); return nil }
				}
				return client, nil
			}
			authority := func(context.Context) (clusterSSHPreparationAuthority, error) {
				call := authorityCalls.Add(1)
				value := current
				value.Cohort.ObservedAt += int64(call)
				return value, nil
			}
			node := func(_ context.Context, got clusterSSHIdentityRecoveryClaim) error {
				nodeCalls.Add(1)
				if got.Recovery != claim.Recovery || got.Binding != claim.Binding {
					t.Fatal("target Node proof received changed claim")
				}
				return nil
			}
			if err := worker.recover(context.Background(), claim, &clusterbootstrap.Bundle{}, authority, node); err != nil {
				t.Fatalf("recovery worker failed: %v", err)
			}
			if opened.Load() != 2 || delivered.Load() != 1 || released.Load() != 1 || authorityCalls.Load() < 6 || nodeCalls.Load() < 3 || store.calls.Load() < 6 || aegis.checks.Load() < 12 || store.completed.Load() != 1 || store.required.Load() != 0 || cleanup.Load() != 1 {
				t.Fatalf("incomplete recovery composition: open=%d deliver=%d release=%d authority=%d node=%d renew=%d aegis=%d complete=%d required=%d",
					opened.Load(), delivered.Load(), released.Load(), authorityCalls.Load(), nodeCalls.Load(), store.calls.Load(), aegis.checks.Load(), store.completed.Load(), store.required.Load())
			}
		})
	}
}

func TestClusterSSHIdentityRecoveryWorkerFailsClosed(t *testing.T) {
	for _, mode := range []string{"bundle", "cleanup", "Aegis", "renew", "authority", "source topology", "target Node", "credential", "connect", "release", "completion"} {
		t.Run(mode, func(t *testing.T) {
			claim, current, consumer := sshIdentityRecoveryWorkerFixture(t)
			defer consumer.Close()
			store := &clusterSSHIdentityRecoveryWorkerStoreFake{}
			if mode == "renew" {
				store.renew = func(clusterSSHIdentityRecoveryClaim) error { return errors.New("private renewal") }
			}
			if mode == "completion" {
				store.complete = func(clusterSSHIdentityRecoveryClaim) error { return errors.New("private completion") }
			}
			aegis := &clusterSSHIdentityRecoveryWorkerAegisFake{envelope: clusterSSHCredentialEnvelope{Version: 1, Binding: claim.Sealed.binding,
				Username: "nicole", Method: "password", Password: "credential-secret", SudoPassword: "sudo-secret"}}
			if mode == "Aegis" {
				aegis.verify = func(string) error { return errors.New("private Aegis") }
			}
			if mode == "credential" {
				aegis.open = func(sealedClusterSSHCredentials, clusterSSHCredentialBinding) error {
					return errors.New("private credential")
				}
			}
			var cleanup atomic.Int32
			worker := &clusterSSHIdentityRecoveryWorker{store: store, aegis: aegis, consumer: consumer,
				bundleClose: func(*clusterbootstrap.Bundle) error {
					cleanup.Add(1)
					if mode == "cleanup" {
						return errors.New("private cleanup failure")
					}
					return nil
				},
				bundleProof: func(*clusterbootstrap.Bundle) (clusterbootstrap.Expected, error) {
					if mode == "bundle" {
						return clusterbootstrap.Expected{}, errors.New("private bundle")
					}
					return claim.Baseline, nil
				}}
			var opened atomic.Int32
			worker.connect = func(context.Context, clusterremote.Target, clusterremote.HostKey, *clusterremote.Credential) (clusterSSHIdentityRecoveryClient, error) {
				call := opened.Add(1)
				if mode == "connect" {
					return nil, errors.New("private connection")
				}
				client := &clusterSSHIdentityDispatchClient{}
				if call == 1 {
					client.deliver = func(context.Context, *engineidentity.Material) error { return nil }
				} else {
					client.release = func(context.Context, *engineidentity.Material) error {
						if mode == "release" {
							return errors.New("private release")
						}
						return nil
					}
				}
				return client, nil
			}
			authority := func(context.Context) (clusterSSHPreparationAuthority, error) {
				value := current
				if mode == "authority" {
					value.Lease.Generation++
				}
				if mode == "source topology" {
					value.Source.Members = append([]clusterSSHSourceMember(nil), value.Source.Members...)
					value.Source.Members[0].BootID = newClusterUUID()
				}
				return value, nil
			}
			node := func(context.Context, clusterSSHIdentityRecoveryClaim) error {
				if mode == "target Node" {
					return errors.New("private target Node")
				}
				return nil
			}
			err := worker.recover(context.Background(), claim, &clusterbootstrap.Bundle{}, authority, node)
			if !errors.Is(err, errClusterSSHIdentityRecoveryWorker) || strings.Contains(err.Error(), "private") {
				t.Fatalf("unsafe recovery failure: %v", err)
			}
			if store.required.Load() != 1 || (mode == "completion" && store.completed.Load() != 1) {
				t.Fatalf("failure outcome not persisted: complete=%d required=%d", store.completed.Load(), store.required.Load())
			}
			if cleanup.Load() != 1 && mode != "cleanup" || mode == "cleanup" && cleanup.Load() != 2 {
				t.Fatalf("private bundle cleanup not completed exactly: %d", cleanup.Load())
			}
		})
	}
}
