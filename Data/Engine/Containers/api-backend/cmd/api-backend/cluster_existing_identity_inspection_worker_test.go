package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func existingIdentityInspectionClaimFixture(t *testing.T) (clusterExistingIdentityInspectionClaim, clusterExistingIdentitySourceProcess) {
	t.Helper()
	p, process, _ := existingIdentitySourceFixture(t)
	_, remote, _, _ := clusterSSHTestService(t)
	binding := clusterSSHCredentialBinding{ClusterID: p.ClusterID, OperationID: p.OperationID, TargetID: newClusterUUID(), Address: p.Members[2].Address, Port: 22, Fingerprint: remote.key.Fingerprint}
	sealed := sealedClusterSSHCredentials{binding: binding, generation: "generation", ciphertext: aegisEnvelopePrefix + "synthetic"}
	planDigest, _ := clusterSSHPreparationDigest(p)
	credentialDigest, _ := clusterSSHPreparationDigest([]string{sealed.generation, sealed.ciphertext})
	r := clusterExistingIdentityConfirmation{Version: 1, RequestID: binding.TargetID, PlanSHA256: planDigest, Actor: "admin", ConfirmedAt: p.ObservedAt,
		Binding: binding, HostKeyAlgorithm: remote.key.Algorithm, HostKeyBase64: base64.StdEncoding.EncodeToString(remote.key.PublicKey), CredentialSHA256: credentialDigest}
	c := clusterExistingIdentityInspectionClaim{Plan: p, Confirmation: r, Sealed: sealed,
		Lease: clusterSSHTargetLease{OperationID: p.OperationID, TargetID: binding.TargetID, Holder: newClusterUUID(), Generation: 1, ControllerHolder: p.Controller,
			OperationAttempt: 1, OperationKind: clusterExistingIdentityKind, OperationStep: clusterExistingIdentityConfirmed, Step: clusterExistingIdentityInspect},
		payload: marshalClusterJSON(clusterExistingIdentityConfirmedPayload{Version: 2, Plan: p, Confirmation: r})}
	if !c.valid() {
		t.Fatal("invalid inspection fixture")
	}
	return c, process
}

type existingIdentityInspectionStoreFake struct {
	renew               func() error
	complete            func(clusterExistingIdentityInspectionResult) error
	checks, completions atomic.Int32
}

func (s *existingIdentityInspectionStoreFake) loadClusterExistingIdentityInspection(context.Context, clusterExistingIdentityInspectionClaim) (string, error) {
	return strings.Repeat("a", 64), nil
}
func (s *existingIdentityInspectionStoreFake) renewClusterExistingIdentityInspection(context.Context, clusterExistingIdentityInspectionClaim) error {
	s.checks.Add(1)
	if s.renew != nil {
		return s.renew()
	}
	return nil
}
func (s *existingIdentityInspectionStoreFake) completeClusterExistingIdentityInspection(_ context.Context, _ clusterExistingIdentityInspectionClaim, r clusterExistingIdentityInspectionResult) error {
	s.completions.Add(1)
	if s.complete != nil {
		return s.complete(r)
	}
	return nil
}

type existingIdentityInspectionClientFake struct {
	verify func(context.Context, clusterbootstrap.SessionBinding, clusterbootstrap.IdentityRecoveryRequest, []byte, func(context.Context) error, func(context.Context) error) (clusterbootstrap.SessionMessage, error)
	closed bool
}

func (c *existingIdentityInspectionClientFake) VerifyIdentityRecovery(ctx context.Context, _ *clusterbootstrap.Bundle, b clusterbootstrap.SessionBinding, r clusterbootstrap.IdentityRecoveryRequest, sudo []byte, authority, source func(context.Context) error) (clusterbootstrap.SessionMessage, error) {
	return c.verify(ctx, b, r, sudo, authority, source)
}
func (c *existingIdentityInspectionClientFake) Close() error { c.closed = true; return nil }

func TestClusterExistingIdentityInspectionWorker(t *testing.T) {
	for _, mode := range []string{"success", "source drift", "source error", "Aegis locked", "download error", "bundle substitution", "connect error", "verify error", "result substitution", "cleanup error", "settlement error", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			c, process := existingIdentityInspectionClaimFixture(t)
			result := existingIdentityInspectionResultFixture(c, process)
			store := &existingIdentityInspectionStoreFake{}
			aegis := &clusterSSHIdentityRecoveryWorkerAegisFake{envelope: clusterSSHCredentialEnvelope{Version: 1, Binding: c.Sealed.binding, Username: "operator", Method: "password", Password: " private<&>$ ", SudoPassword: " sudo<&>$ "}}
			client := &existingIdentityInspectionClientFake{}
			var checks, downloads, connects, cleanups int
			w := &clusterExistingIdentityInspectionWorker{store: store, aegis: aegis, heartbeat: time.Hour,
				custody: func(ctx context.Context, got clusterExistingIdentityInspectionClaim, source clusterExistingIdentitySourceProcess, authority func(context.Context) (string, error)) (clusterExistingIdentitySourceCustody, error) {
					checks++
					if got.Lease != c.Lease || source != process {
						t.Fatal("substituted custody input")
					}
					if _, err := authority(ctx); err != nil {
						return result.Source, err
					}
					p := result.Source
					if mode == "source error" {
						return p, errors.New("private native error")
					}
					if mode == "source drift" && checks > 1 {
						p.ContainerID = "containerd://" + strings.Repeat("f", 64)
					}
					return p, nil
				},
				download: func(context.Context, clusterbootstrap.Expected, string) (*clusterbootstrap.Bundle, error) {
					downloads++
					if mode == "download error" {
						return &clusterbootstrap.Bundle{}, errors.New("private download error")
					}
					return &clusterbootstrap.Bundle{}, nil
				},
				proof: func(*clusterbootstrap.Bundle) (clusterbootstrap.Expected, string, error) {
					e := c.Plan.Baseline
					if mode == "bundle substitution" {
						e.SourceSHA = strings.Repeat("e", 40)
					}
					return e, result.Session.ManagerSHA256, nil
				},
				cleanup: func(*clusterbootstrap.Bundle) error {
					cleanups++
					if mode == "cleanup error" {
						return errors.New("private cleanup error")
					}
					return nil
				},
				connect: func(_ context.Context, target clusterremote.Target, key clusterremote.HostKey, credential *clusterremote.Credential) (clusterExistingIdentityInspectionClient, error) {
					connects++
					if target.Address != c.Confirmation.Binding.Address || key.Fingerprint != c.Confirmation.Binding.Fingerprint || credential == nil {
						t.Fatal("unbound transport")
					}
					if mode == "connect error" {
						return client, errors.New("private connection error")
					}
					return client, nil
				},
			}
			client.verify = func(ctx context.Context, b clusterbootstrap.SessionBinding, r clusterbootstrap.IdentityRecoveryRequest, sudo []byte, authority, source func(context.Context) error) (clusterbootstrap.SessionMessage, error) {
				if b != *result.Session.Binding || r != *result.Session.IdentityRecovery || string(sudo) != " sudo<&>$ " || authority(ctx) != nil || source(ctx) != nil {
					t.Fatal("lost supervised inspection contract")
				}
				if mode == "verify error" {
					return result.Session, errors.New("private verify error")
				}
				m := result.Session
				if mode == "result substitution" {
					m.ManagerSHA256 = strings.Repeat("f", 64)
				}
				return m, nil
			}
			if mode == "Aegis locked" {
				aegis.verify = func(string) error { return errClusterConflict }
			}
			if mode == "settlement error" {
				store.complete = func(clusterExistingIdentityInspectionResult) error { return errClusterConflict }
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			err := w.inspect(ctx, c, process)
			if mode == "success" {
				if err != nil || store.completions.Load() != 1 || !client.closed || cleanups != 1 {
					t.Fatal("inspection did not settle", err)
				}
			} else if err != errClusterConflict || (mode != "settlement error" && store.completions.Load() != 0) {
				t.Fatal("failed inspection settled or leaked private error", err)
			}
			if downloads > 0 && cleanups == 0 {
				t.Fatal("scratch escaped")
			}
			if connects > 0 && !client.closed {
				t.Fatal("SSH escaped")
			}
		})
	}
}

func TestClusterExistingIdentityInspectionDownloadCancellationJoins(t *testing.T) {
	c, process := existingIdentityInspectionClaimFixture(t)
	result := existingIdentityInspectionResultFixture(c, process)
	store := &existingIdentityInspectionStoreFake{}
	store.renew = func() error {
		if store.checks.Load() > 1 {
			return errClusterConflict
		}
		return nil
	}
	aegis := &clusterSSHIdentityRecoveryWorkerAegisFake{}
	done := false
	w := &clusterExistingIdentityInspectionWorker{store: store, aegis: aegis, heartbeat: time.Millisecond,
		custody: func(context.Context, clusterExistingIdentityInspectionClaim, clusterExistingIdentitySourceProcess, func(context.Context) (string, error)) (clusterExistingIdentitySourceCustody, error) {
			return result.Source, nil
		},
		download: func(ctx context.Context, _ clusterbootstrap.Expected, _ string) (*clusterbootstrap.Bundle, error) {
			<-ctx.Done()
			done = true
			return nil, ctx.Err()
		},
		proof: func(*clusterbootstrap.Bundle) (clusterbootstrap.Expected, string, error) {
			t.Fatal("cancelled download reached proof")
			return c.Plan.Baseline, "", nil
		},
		cleanup: func(*clusterbootstrap.Bundle) error { return nil },
		connect: func(context.Context, clusterremote.Target, clusterremote.HostKey, *clusterremote.Credential) (clusterExistingIdentityInspectionClient, error) {
			t.Fatal("cancelled download reached SSH")
			return nil, nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := w.inspect(ctx, c, process); err != errClusterConflict || !done || store.completions.Load() != 0 {
		t.Fatal("heartbeat cancellation not joined", err)
	}
	checks := store.checks.Load()
	time.Sleep(5 * time.Millisecond)
	if store.checks.Load() != checks {
		t.Fatal("heartbeat escaped scope")
	}
}
