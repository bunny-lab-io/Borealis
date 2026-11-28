package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func checkNativeTransferAuthority(t *testing.T) {
	f, _ := newSSHPreparationClaimFixture(t, false)
	work, err := f.c.store.claimFencedSSHPreparation(f.ctx, f.op, f.c.holder, newClusterUUID())
	if err != nil || len(work.Claims) != 2 {
		t.Fatal("paired claim", err)
	}
	var finished bool
	cohort := func(ctx context.Context, claims []clusterSSHNetworkTargetClaim, baseline clusterbootstrap.Expected, confirmation string, binding engineidentity.Binding, pair func(context.Context) error) ([]clusterSSHPrejoinDispatchResult, error) {
		claim := claims[0]
		read := newClusterSSHPreparationAuthorityRead(f.c.store, f.aegis, claim.Lease, baseline, claim.Sealed)
		a, err := read(ctx)
		if err != nil {
			return nil, err
		}
		_, proc, _ := existingIdentitySourceFixture(t)
		for _, m := range a.Source.Members {
			if m.NodeID == binding.SourceID {
				proc.NodeName = m.Name
			}
		}
		source := clusterSSHPrejoinSource{Binding: binding, Process: proc}
		expected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, claim.Lease, baseline, a.K3sVersion, "10.42.0.0/16", "10.43.0.0/16", a.payload)
		if err != nil {
			return nil, err
		}
		acquire := func(ctx context.Context, consume clusterSSHPrejoinWorkerSourceConsumer) error {
			ok := func(context.Context) error { return nil }
			checks := clusterSSHPreparationChecks{Authority: pair, Inputs: ok, Storage: func(context.Context) (clusterSSHStorageSnapshot, error) { return clusterSSHStorageSnapshot{}, nil }, K3sJoin: func(context.Context, engineidentity.Binding, clusterSSHJoinMaterialConsumer) error { return nil }}
			return consume(ctx, &clusterbootstrap.PreparationInputs{}, &clusterbootstrap.PreparationArtifacts{}, clusterbootstrap.PostgresImageRepository+"@sha256:"+a.PayloadSHA256, sshPrejoinWorkerCapacityFixture(), expected, checks)
		}
		storage := func(context.Context, string, clusterbootstrap.PreparationExpected, clusterSSHPreparedImageCapacity, clusterSSHPreparationChecks) error {
			return nil
		}
		err = withClusterSSHPrejoinWorkerInputs(ctx, claim, baseline, confirmation, source, read, acquire, storage, func(bound context.Context, _ *clusterbootstrap.PreparationInputs, _ *clusterbootstrap.PreparationArtifacts, _ string, _ clusterbootstrap.PreparationExpected, checks clusterSSHPreparationChecks) error {
			start := make(chan struct{})
			done := make(chan error, 2)
			began := time.Now()
			for range 2 {
				go func() {
					<-start
					for range 4 {
						c, cancel := context.WithTimeout(bound, clusterbootstrap.PreparationProofTimeout)
						err := checks.Authority(c)
						cancel()
						if err != nil {
							done <- err
							return
						}
					}
					done <- nil
				}()
			}
			close(start)
			first, second := <-done, <-done
			if first != nil || second != nil {
				t.Errorf("concurrent proof rejected: %v %v", first, second)
				return errClusterConflict
			}
			t.Logf("8 proofs from two competing callers: %s", time.Since(began))
			if err := exerciseNativeAuthorityTransfer(bound, expected, checks.Authority, nil); err != nil {
				t.Errorf("verified transfer: %v", err)
				return err
			}
			revoke := func() {
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_generation=lease_generation+1 WHERE id=$1`, claims[1].Lease.TargetID)
			}
			if err := exerciseNativeAuthorityTransfer(bound, expected, checks.Authority, revoke); !errors.Is(err, clusterbootstrap.ErrSessionAuthority) {
				t.Errorf("revoked transfer: %v", err)
			}
			finished = true
			return errClusterConflict
		}, pair)
		return nil, err
	}
	w := newClusterSSHPrejoinCohortPreparationWorker(f.c.store, f.aegis, cohort, f.observe)
	if w.prepare(f.ctx, work) == nil || !finished {
		t.Fatal("fixture missed native authority or released custody")
	}
}

// Stream over actual OS pipes using the sender/receiver protocol. The producer
// verifies the pair throughout the transfer until a concurrent challenge has
// completed. Only the remote byte consumer and watchdog notification are doubles.
func exerciseNativeAuthorityTransfer(parent context.Context, expected clusterbootstrap.PreparationExpected, check func(context.Context) error, revoke func()) error {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	request, err := clusterbootstrap.NewPreparationSessionRequest(expected, strings.Repeat("b", 64), strings.Repeat("a", 64), "inspect_preparation_progress")
	if err != nil {
		return err
	}
	input, toPeer, err := os.Pipe()
	if err != nil {
		return err
	}
	defer input.Close()
	defer toPeer.Close()
	fromPeer, output, err := os.Pipe()
	if err != nil {
		return err
	}
	defer fromPeer.Close()
	defer output.Close()
	rawIn, rawOut, err := os.Pipe()
	if err != nil {
		return err
	}
	defer rawIn.Close()
	defer rawOut.Close()
	const size = 4 << 20
	done := make(chan error, 1)
	go func() {
		err := clusterbootstrap.ServeSSHPreparation(ctx, input, output, request.Session.Nonce, false,
			func(context.Context, string) (io.ReadCloser, error) { return rawIn, nil },
			func(_ context.Context, r clusterbootstrap.PreparationSessionRequest) error {
				if r.Session.Nonce != request.Session.Nonce {
					return clusterbootstrap.ErrSessionProtocol
				}
				return nil
			},
			func(_ context.Context, _ clusterbootstrap.PreparationSessionRequest, r io.Reader) (clusterbootstrap.PreparationSessionResult, error) {
				n, err := io.Copy(io.Discard, r)
				if err == nil && n != size {
					err = io.ErrUnexpectedEOF
				}
				return clusterbootstrap.PreparationSessionResult{Progress: clusterbootstrap.PreparationProgress{Step: "empty"}}, err
			}, func(time.Time) error { return nil })
		output.Close()
		input.Close()
		done <- err
	}()
	var transferring atomic.Bool
	challenge := make(chan struct{})
	var once sync.Once
	refresh := func(ctx context.Context) error {
		if transferring.Load() {
			once.Do(func() {
				if revoke != nil {
					revoke()
				}
				close(challenge)
			})
		}
		return check(ctx)
	}
	produce := func(ctx context.Context, w io.Writer) error {
		block := bytes.Repeat([]byte{0xab}, size/2)
		if _, err := w.Write(block); err != nil {
			return err
		}
		transferring.Store(true)
		for {
			if err := check(ctx); err != nil {
				return err
			}
			select {
			case <-challenge:
				_, err := w.Write(block)
				return err
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		}
	}
	var progress clusterbootstrap.PreparationProgress
	err = clusterbootstrap.ExchangeSSHPreparation(ctx, fromPeer, toPeer, request, func(ctx context.Context) (clusterbootstrap.PreparationStreamReceipt, error) {
		defer rawOut.Close()
		return clusterbootstrap.StreamPreparation(ctx, rawOut, produce)
	}, refresh, &progress, nil)
	toPeer.Close()
	fromPeer.Close()
	cancel()
	remoteErr := <-done
	if err != nil {
		return err
	}
	if progress.Step != "empty" || remoteErr != nil {
		return clusterbootstrap.ErrSessionProtocol
	}
	return nil
}
