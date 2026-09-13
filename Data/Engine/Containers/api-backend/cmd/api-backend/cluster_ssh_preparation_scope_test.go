package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestClusterSSHPreparationScopeCancelsAndJoinsWork(t *testing.T) {
	for _, mode := range []string{"acquisition", "consumption", "parent cancellation"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var lost atomic.Bool
			var checks atomic.Int64
			check := func(context.Context) error {
				checks.Add(1)
				if lost.Load() {
					return errors.New("private credential detail")
				}
				return nil
			}
			started := make(chan context.Context, 1)
			cleanup := make(chan struct{})
			release := make(chan struct{})
			done := make(chan error, 1)
			go func() {
				done <- runClusterSSHPreparationScope(ctx, time.Millisecond, check, func(workCtx context.Context) error {
					if mode == "consumption" && check(workCtx) != nil {
						return clusterbootstrap.ErrSessionAuthority
					}
					started <- workCtx
					<-workCtx.Done()
					close(cleanup)
					<-release // Model synchronous joining/cleanup before consumer returns.
					return nil
				})
			}()
			workCtx := <-started
			if mode == "parent cancellation" {
				cancel()
			} else {
				lost.Store(true)
			}
			select {
			case <-cleanup:
			case <-time.After(time.Second):
				close(release)
				t.Fatal("ownership loss did not cancel blocked work")
			}
			select {
			case <-done:
				close(release)
				t.Fatal("scope returned before cleanup joined")
			default:
			}
			close(release)
			select {
			case err := <-done:
				if err != clusterbootstrap.ErrSessionAuthority {
					t.Fatal("lost authority returned success/private error")
				}
			case <-time.After(time.Second):
				t.Fatal("scope did not join")
			}
			if workCtx.Err() == nil || checks.Load() < 1 {
				t.Fatal("scope retained usable context")
			}
		})
	}
}

func TestClusterSSHPreparationScopeRejectsBeforeWorkAndAtCompletion(t *testing.T) {
	for _, mode := range []string{"canceled", "initial rejection", "final rejection", "work error", "success"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			calls, work := 0, 0
			var used context.Context
			err := runClusterSSHPreparationScope(ctx, time.Hour, func(context.Context) error {
				calls++
				if mode == "initial rejection" || (mode == "final rejection" && calls > 1) {
					return errors.New("private state")
				}
				return nil
			}, func(ctx context.Context) error {
				work++
				used = ctx
				if mode == "work error" {
					return errors.New("private URL")
				}
				return nil
			})
			if mode == "success" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err != clusterbootstrap.ErrSessionAuthority && err != clusterbootstrap.ErrPreparationConfig {
				t.Fatal("failure escaped boundary")
			}
			if (mode == "initial rejection" || mode == "canceled") && work != 0 {
				t.Fatal("work preceded authority")
			}
			if used != nil && used.Err() == nil {
				t.Fatal("successful scope context remained live after return")
			}
		})
	}
}

func TestClusterSSHPreparationLeaseCheckFreezesAuthorityAcrossRenewal(t *testing.T) {
	for _, mode := range []string{"clock only", "changed after renewal", "changed next check", "renew failure", "read failure", "locked after renewal"} {
		t.Run(mode, func(t *testing.T) {
			_, current, _ := sshBrokerFixture(t)
			reads, renewals := 0, 0
			check := newClusterSSHPreparationLeaseCheck(func(ctx context.Context) (clusterSSHPreparationAuthority, error) {
				reads++
				if _, ok := ctx.Deadline(); !ok {
					t.Error("authority check unbounded")
				}
				if mode == "read failure" || (mode == "locked after renewal" && reads > 1) {
					return clusterSSHPreparationAuthority{}, errors.New("private")
				}
				current.Cohort.ObservedAt++
				if (mode == "changed after renewal" && reads == 2) || (mode == "changed next check" && reads == 3) {
					current.Cohort.Targets[0].Report.BootID = newClusterUUID()
				}
				return current, nil
			}, func(context.Context) error {
				renewals++
				if mode == "renew failure" {
					return errors.New("private renewal")
				}
				return nil
			})
			first := check(context.Background())
			if mode == "clock only" || mode == "changed next check" {
				if first != nil {
					t.Fatal("initial valid check failed")
				}
				second := check(context.Background())
				if mode == "clock only" && second != nil {
					t.Fatal("advancing observation time changed authority")
				}
				if mode == "changed next check" && (second != clusterbootstrap.ErrSessionAuthority || renewals != 1) {
					t.Fatal("changed aliased cohort renewed")
				}
			} else if first != clusterbootstrap.ErrSessionAuthority {
				t.Fatal("unsafe renewal accepted")
			}
			if mode == "read failure" && renewals != 0 {
				t.Fatal("renewal preceded full authority")
			}
		})
	}
}

func TestClusterSSHPreparationLeaseCheckBoundsConcurrentWait(t *testing.T) {
	_, current, _ := sshBrokerFixture(t)
	entered := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	check := newClusterSSHPreparationLeaseCheck(func(ctx context.Context) (clusterSSHPreparationAuthority, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return current, ctx.Err()
	}, func(context.Context) error { t.Error("renewed unavailable authority"); return nil })
	done := make(chan error, 1)
	go func() { done <- check(ctx) }()
	<-entered
	short, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	if err := check(short); err != clusterbootstrap.ErrSessionAuthority {
		t.Fatal("concurrent checker ignored deadline")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("checker did not join cancellation")
	}
}

func TestClusterSSHPreparationBoundaryCannotOutliveScope(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	var calls atomic.Int64
	boundary := clusterSSHPreparationBoundary(ctx, func(context.Context) error { calls.Add(1); return nil }, func(ctx context.Context) error { close(started); <-ctx.Done(); return ctx.Err() })
	done := make(chan error, 1)
	go func() { done <- boundary(context.Background()) }()
	<-started
	cancel()
	select {
	case err := <-done:
		if err != clusterbootstrap.ErrSessionAuthority {
			t.Fatal("canceled export accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("background caller detached from scope")
	}
	if boundary(context.Background()) != clusterbootstrap.ErrSessionAuthority || calls.Load() != 1 {
		t.Fatal("closed scope performed later work")
	}
}
