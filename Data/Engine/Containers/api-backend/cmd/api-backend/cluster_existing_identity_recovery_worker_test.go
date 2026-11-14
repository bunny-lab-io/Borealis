package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func existingIdentityRecoveryClaimFixture(t *testing.T) (clusterExistingIdentityRecoveryClaim, clusterExistingIdentitySourceProcess) {
	i, process := existingIdentityInspectionClaimFixture(t)
	result := existingIdentityInspectionResultFixture(i, process)
	l := i.Lease
	l.Holder, l.Generation, l.OperationStep, l.Step = newClusterUUID(), i.Lease.Generation+1, clusterExistingIdentityRecover, clusterExistingIdentityRecover
	c := clusterExistingIdentityRecoveryClaim{Inspection: i, Result: result, ObservedAt: i.Confirmation.ConfirmedAt, Lease: l, receipt: marshalClusterJSON(result)}
	if !c.valid() {
		t.Fatal("invalid recovery fixture")
	}
	return c, process
}

type existingIdentityRecoveryStoreFake struct {
	renew                         func() error
	complete                      func() error
	checks, completions, failures atomic.Int32
}

func (s *existingIdentityRecoveryStoreFake) loadClusterExistingIdentityRecovery(context.Context, clusterExistingIdentityRecoveryClaim) (string, error) {
	return strings.Repeat("a", 64), nil
}
func (s *existingIdentityRecoveryStoreFake) renewClusterExistingIdentityRecovery(context.Context, clusterExistingIdentityRecoveryClaim) error {
	s.checks.Add(1)
	if s.renew != nil {
		return s.renew()
	}
	return nil
}
func (s *existingIdentityRecoveryStoreFake) completeClusterExistingIdentityRecovery(context.Context, clusterExistingIdentityRecoveryClaim) error {
	s.completions.Add(1)
	if s.complete != nil {
		return s.complete()
	}
	return nil
}
func (s *existingIdentityRecoveryStoreFake) requireClusterExistingIdentityRecovery(context.Context, clusterExistingIdentityRecoveryClaim) error {
	s.failures.Add(1)
	return nil
}

func TestClusterExistingIdentityRecoveryWorker(t *testing.T) {
	for _, mode := range []string{"success", "ambiguous delivery", "delivery connection", "release connection", "release proof", "source drift", "wrong root", "closed source", "Aegis locked", "download error", "manager substitution", "cleanup error", "settlement error", "cancel during delivery"} {
		t.Run(mode, func(t *testing.T) {
			c, process := existingIdentityRecoveryClaimFixture(t)
			root := t.TempDir()
			material := sshIdentitySourceMaterial(t, root)
			consumer, err := engineidentity.AcquireConsumer(root)
			if err != nil {
				t.Fatal(err)
			}
			defer consumer.Close()
			store := &existingIdentityRecoveryStoreFake{}
			aegis := &clusterSSHIdentityRecoveryWorkerAegisFake{envelope: clusterSSHCredentialEnvelope{Version: 1, Binding: c.Inspection.Sealed.binding, Username: "operator", Method: "password", Password: " private<&>$ ", SudoPassword: " sudo<&>$ "}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var sourceReads, downloads, cleanups, connections, deliveries, releases int
			var clients []*clusterSSHIdentityDispatchClient
			w := &clusterExistingIdentityRecoveryWorker{store: store, aegis: aegis, consumer: consumer, root: root, heartbeat: time.Hour,
				custody: func(ctx context.Context, got clusterExistingIdentityRecoveryClaim, source clusterExistingIdentitySourceProcess, authority func(context.Context) (string, error)) (clusterExistingIdentitySourceCustody, error) {
					sourceReads++
					if got.Lease != c.Lease || source != process {
						t.Fatal("unbound source custody")
					}
					if _, err := authority(ctx); err != nil {
						return c.Result.Source, err
					}
					proof := c.Result.Source
					if mode == "source drift" && sourceReads > 1 {
						proof.ContainerID = "containerd://" + strings.Repeat("f", 64)
					}
					return proof, nil
				},
				download: func(context.Context, clusterbootstrap.Expected, string) (*clusterbootstrap.Bundle, error) {
					downloads++
					if mode == "download error" {
						return &clusterbootstrap.Bundle{}, errors.New("private download error")
					}
					return &clusterbootstrap.Bundle{}, nil
				},
				proof: func(*clusterbootstrap.Bundle) (clusterbootstrap.Expected, string, error) {
					digest := c.Result.Session.ManagerSHA256
					if mode == "manager substitution" {
						digest = strings.Repeat("f", 64)
					}
					return c.Inspection.Plan.Baseline, digest, nil
				},
				cleanup: func(*clusterbootstrap.Bundle) error {
					cleanups++
					if mode == "cleanup error" {
						return errors.New("private cleanup error")
					}
					return nil
				},
				connect: func(_ context.Context, target clusterremote.Target, key clusterremote.HostKey, credential *clusterremote.Credential) (clusterSSHIdentityRecoveryClient, error) {
					connections++
					if target.Address != c.Inspection.Confirmation.Binding.Address || key.Fingerprint != c.Inspection.Confirmation.Binding.Fingerprint || credential == nil {
						t.Fatal("wrong SSH binding")
					}
					if mode == "delivery connection" && connections == 1 || mode == "release connection" && connections == 2 {
						return nil, errors.New("private transport error")
					}
					client := &clusterSSHIdentityDispatchClient{}
					clients = append(clients, client)
					if connections == 1 {
						client.deliver = func(_ context.Context, got *engineidentity.Material) error {
							deliveries++
							if !got.Equal(material) {
								t.Fatal("wrong identity transferred")
							}
							if mode == "cancel during delivery" {
								cancel()
							}
							if mode == "ambiguous delivery" {
								return errors.New("private lost acknowledgement")
							}
							return nil
						}
					} else {
						client.release = func(_ context.Context, got *engineidentity.Material) error {
							releases++
							if !got.Equal(material) {
								t.Fatal("release changed identity")
							}
							if mode == "release proof" {
								return errors.New("private target proof")
							}
							return nil
						}
					}
					return client, nil
				},
			}
			if mode == "Aegis locked" {
				aegis.verify = func(string) error { return errClusterConflict }
			}
			if mode == "wrong root" {
				w.root = t.TempDir()
			}
			if mode == "closed source" {
				consumer.Close()
			}
			if mode == "settlement error" {
				store.complete = func() error { return errClusterConflict }
			}
			err = w.recover(ctx, c, process)
			success := mode == "success" || mode == "ambiguous delivery" || mode == "delivery connection"
			if success {
				if err != nil || store.completions.Load() != 1 || store.failures.Load() != 0 || connections != 2 || releases != 1 || deliveries > 1 {
					t.Fatal("recovery failed exact delivery/release sequence", err)
				}
			} else if err != errClusterConflict || store.failures.Load() != 1 || (mode != "settlement error" && store.completions.Load() != 0) {
				t.Fatal("failed recovery falsely settled", err)
			}
			if downloads > 0 && cleanups == 0 {
				t.Fatal("private scratch escaped")
			}
			for _, client := range clients {
				if !client.closed.Load() {
					t.Fatal("SSH connection escaped")
				}
			}
			if (mode == "wrong root" || mode == "closed source" || mode == "source drift") && connections > 0 {
				t.Fatal("invalid source opened SSH")
			}
		})
	}
}

func TestClusterExistingIdentityRecoveryWorkerAcquisitionCancellation(t *testing.T) {
	for _, mode := range []string{"parent", "authority"} {
		t.Run(mode, func(t *testing.T) {
			c, process := existingIdentityRecoveryClaimFixture(t)
			root := t.TempDir()
			sshIdentitySourceMaterial(t, root)
			consumer, err := engineidentity.AcquireConsumer(root)
			if err != nil {
				t.Fatal(err)
			}
			defer consumer.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var acquiring, cleaned atomic.Bool
			store := &existingIdentityRecoveryStoreFake{}
			store.renew = func() error {
				if acquiring.Load() {
					if mode == "parent" {
						cancel()
					}
					return errClusterConflict
				}
				return nil
			}
			w := &clusterExistingIdentityRecoveryWorker{store: store,
				aegis: &clusterSSHIdentityRecoveryWorkerAegisFake{}, consumer: consumer, root: root, heartbeat: time.Millisecond,
				custody: func(context.Context, clusterExistingIdentityRecoveryClaim, clusterExistingIdentitySourceProcess, func(context.Context) (string, error)) (clusterExistingIdentitySourceCustody, error) {
					return c.Result.Source, nil
				},
				download: func(ctx context.Context, _ clusterbootstrap.Expected, _ string) (*clusterbootstrap.Bundle, error) {
					acquiring.Store(true)
					<-ctx.Done()
					return &clusterbootstrap.Bundle{}, ctx.Err()
				},
				proof: func(*clusterbootstrap.Bundle) (clusterbootstrap.Expected, string, error) {
					t.Error("cancelled acquisition reached proof")
					return clusterbootstrap.Expected{}, "", errClusterConflict
				},
				cleanup: func(*clusterbootstrap.Bundle) error { cleaned.Store(true); return nil },
				connect: func(context.Context, clusterremote.Target, clusterremote.HostKey, *clusterremote.Credential) (clusterSSHIdentityRecoveryClient, error) {
					t.Error("cancelled acquisition opened SSH")
					return nil, errClusterConflict
				},
			}
			if w.recover(ctx, c, process) != errClusterConflict || !cleaned.Load() || store.completions.Load() != 0 || store.failures.Load() != 1 {
				t.Fatal("cancelled acquisition escaped cleanup or falsely completed")
			}
			checks := store.checks.Load()
			time.Sleep(5 * time.Millisecond)
			if checks < 2 || store.checks.Load() != checks {
				t.Fatal("heartbeat did not renew and join")
			}
		})
	}
}
