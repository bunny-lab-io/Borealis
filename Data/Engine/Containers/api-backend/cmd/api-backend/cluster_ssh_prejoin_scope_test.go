package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"errors"
	"io"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func sshPrejoinScopeFixture(t *testing.T) (string, *engineidentity.Consumer, clusterSSHPrejoinSource, clusterSSHPrejoinSourceCustody) {
	t.Helper()
	a, source, pod := sshPrejoinSourceFixture(t, false)
	p, err := clusterIdentityPodCustody(pod, a.Baseline.SourceSHA, source.Process)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	sshIdentitySourceMaterial(t, root)
	consumer, err := engineidentity.AcquireConsumer(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { consumer.Close() })
	return root, consumer, source, clusterSSHPrejoinSourceCustody{Source: source, Pod: p, AuthoritySHA256: strings.Repeat("a", 64)}
}

func TestClusterSSHPrejoinScopeChallenge(t *testing.T) {
	for _, mode := range []string{"valid", "ignored failure", "proof drift", "key drift", "key drift after proof", "key drift before callback", "final key drift", "cancelled caller", "callback failure"} {
		t.Run(mode, func(t *testing.T) {
			root, consumer, source, proof := sshPrejoinScopeFixture(t)
			calls, used := 0, false
			read := func(ctx context.Context) (clusterSSHPrejoinSourceCustody, error) {
				calls++
				if calls > 2 {
					if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 3*time.Second {
						t.Fatal("challenge read unbounded")
					}
				}
				if mode == "key drift before callback" && calls == 2 || mode == "key drift after proof" && calls == 4 {
					sshIdentitySourceMaterial(t, root)
				}
				if calls > 2 && mode == "ignored failure" {
					return proof, errors.New("private custody diagnostic")
				}
				return proof, nil
			}
			var escaped func(context.Context) error
			var callbackContext context.Context
			err := withClusterSSHPrejoinSourceMaterial(context.Background(), consumer, root, source, read,
				func(ctx context.Context, material *engineidentity.Material, refresh func(context.Context) error) error {
					used, escaped, callbackContext = true, refresh, ctx
					if mode == "proof drift" {
						proof.Pod.ContainerID = "containerd://" + strings.Repeat("f", 64)
					}
					if mode == "key drift" || mode == "final key drift" {
						sshIdentitySourceMaterial(t, root)
					}
					if mode == "final key drift" {
						return nil // Final scope proof cannot trust callback success.
					}
					if mode == "callback failure" {
						return errors.New("private transport diagnostic")
					}
					caller, cancel := context.WithCancel(context.Background())
					defer cancel()
					if mode == "cancelled caller" {
						cancel()
					}
					checkErr := refresh(caller)
					if mode == "valid" {
						if checkErr != nil || ctx.Err() != nil {
							t.Fatal("valid refresh rejected", checkErr)
						}
					} else if checkErr != clusterbootstrap.ErrSessionAuthority || ctx.Err() == nil {
						t.Fatal("failed refresh did not cancel transport", checkErr, ctx.Err())
					}
					return nil // Deliberately ignore failures; lifetime must latch them.
				})
			if (err == nil) != (mode == "valid") || err != nil && err != clusterbootstrap.ErrSessionAuthority {
				t.Fatal("incorrect scope outcome", err)
			}
			if used != (mode != "key drift before callback") {
				t.Fatal("changed keys reached callback")
			}
			if escaped != nil {
				before := calls
				if callbackContext.Err() == nil || escaped(context.Background()) != clusterbootstrap.ErrSessionAuthority || calls != before {
					t.Fatal("escaped capability performed source IO")
				}
			}
		})
	}
}

func TestClusterSSHPrejoinScopeJoinsOutstandingChallenge(t *testing.T) {
	root, consumer, source, proof := sshPrejoinScopeFixture(t)
	started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	read := func(ctx context.Context) (clusterSSHPrejoinSourceCustody, error) {
		if calls.Add(1) == 3 {
			close(started)
			<-ctx.Done()
			close(cancelled)
			<-release
			return proof, ctx.Err()
		}
		return proof, nil
	}
	result, checked := make(chan error, 1), make(chan error, 1)
	go func() {
		result <- withClusterSSHPrejoinSourceMaterial(context.Background(), consumer, root, source, read,
			func(ctx context.Context, material *engineidentity.Material, refresh func(context.Context) error) error {
				go func() { checked <- refresh(context.Background()) }()
				<-started
				return nil
			})
	}()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("scope did not cancel outstanding challenge")
	}
	select {
	case <-result:
		close(release)
		t.Fatal("scope returned before challenge cleanup")
	default:
	}
	close(release)
	if err := <-result; err != clusterbootstrap.ErrSessionAuthority {
		t.Fatal("in-flight challenge acknowledged", err)
	}
	if err := <-checked; err != clusterbootstrap.ErrSessionAuthority {
		t.Fatal("cancelled challenge passed", err)
	}
}

func TestClusterSSHPrejoinScopeSerializesChallenges(t *testing.T) {
	root, consumer, source, proof := sshPrejoinScopeFixture(t)
	var active, calls atomic.Int32
	read := func(context.Context) (clusterSSHPrejoinSourceCustody, error) {
		calls.Add(1)
		if active.Add(1) != 1 {
			t.Error("overlapping source reads")
		}
		defer active.Add(-1)
		runtime.Gosched()
		return proof, nil
	}
	err := withClusterSSHPrejoinSourceMaterial(context.Background(), consumer, root, source, read,
		func(ctx context.Context, material *engineidentity.Material, refresh func(context.Context) error) error {
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if err := refresh(context.Background()); err != nil {
						t.Error("concurrent refresh failed", err)
					}
				}()
			}
			wg.Wait()
			return nil
		})
	if err != nil || calls.Load() != 20 || active.Load() != 0 {
		t.Fatal("serialized source scope failed", err, calls.Load(), active.Load())
	}
}

// Exercise the real challenge protocol, not only direct callback invocation.
// Receiver checks an in-memory test envelope; this does not publish a source
// archive or install keys on a host.
func TestClusterSSHPrejoinScopePreparationChallenges(t *testing.T) {
	for _, revoked := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "revoked during transfer"}[revoked], func(t *testing.T) {
			a, source, pod := sshPrejoinSourceFixture(t, false)
			p, err := clusterIdentityPodCustody(pod, a.Baseline.SourceSHA, source.Process)
			if err != nil {
				t.Fatal(err)
			}
			proof := clusterSSHPrejoinSourceCustody{Source: source, Pod: p, AuthoritySHA256: strings.Repeat("a", 64)}
			expected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, a.Lease, a.Baseline, a.K3sVersion, "10.42.0.0/16", "10.43.0.0/16")
			if err != nil {
				t.Fatal(err)
			}
			request, err := clusterbootstrap.NewPrejoinPreparationSessionRequest(expected, strings.Repeat("b", 64), strings.Repeat("c", 64), "install_prejoin_identity", source.Binding)
			if err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			want := sshIdentitySourceMaterial(t, root)
			consumer, err := engineidentity.AcquireConsumer(root)
			if err != nil {
				t.Fatal(err)
			}
			defer consumer.Close()
			var revokedNow, producerStopped, received atomic.Bool
			var reads atomic.Int32
			read := func(context.Context) (clusterSSHPrejoinSourceCustody, error) {
				reads.Add(1)
				if revokedNow.Load() {
					return proof, errors.New("private source revoked")
				}
				return proof, nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			err = withClusterSSHPrejoinSourceMaterial(ctx, consumer, root, source, read,
				func(ctx context.Context, material *engineidentity.Material, refresh func(context.Context) error) error {
					client, server := net.Pipe()
					defer client.Close()
					defer server.Close()
					serverResult := make(chan error, 1)
					go func() {
						serverResult <- clusterbootstrap.ServePreparationSession(ctx, server, server, request.Session.Nonce,
							func(_ context.Context, got clusterbootstrap.PreparationSessionRequest) error {
								if got.IdentitySource == nil || *got.IdentitySource != source.Binding {
									return clusterbootstrap.ErrSessionProtocol
								}
								return nil
							}, func(_ context.Context, _ clusterbootstrap.PreparationSessionRequest, input io.Reader) error {
								raw, err := io.ReadAll(io.LimitReader(input, engineidentity.MaxEnvelopeBytes+1))
								defer clear(raw)
								if err != nil {
									return err
								}
								got, err := engineidentity.Import(raw, source.Binding)
								if err != nil || !got.Equal(want) {
									return clusterbootstrap.ErrSessionProtocol
								}
								received.Store(true)
								return nil
							}, func(time.Time) error { return nil })
					}()
					exchangeErr := clusterbootstrap.ExchangePreparationSession(ctx, client, client, request,
						func(ctx context.Context, output io.Writer) error {
							defer producerStopped.Store(true)
							if revoked {
								revokedNow.Store(true)
								<-ctx.Done()
								return ctx.Err()
							}
							raw, err := material.Export(source.Binding)
							defer clear(raw)
							if err == nil {
								_, err = output.Write(raw)
							}
							return err
						}, refresh)
					serverErr := <-serverResult
					if exchangeErr != nil || serverErr != nil {
						return clusterbootstrap.ErrSessionAuthority
					}
					return nil
				})
			if (err == nil) == revoked || received.Load() == revoked || !producerStopped.Load() || reads.Load() < 7 {
				t.Fatal("preparation challenge composition failed", err, received.Load(), producerStopped.Load(), reads.Load())
			}
		})
	}
}
