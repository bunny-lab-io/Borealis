package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestClusterSSHPreparedMembershipScopePostgresCustody(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "explicit refresh", "old capability", "native read failure", "payload drift", "credential expiry", "controller expiry", "heartbeat expiry", "source row drift", "target row drift", "ignored authority failure", "expiry during cleanup", "cancel during cleanup", "request expiry during cleanup", "client expiry during cleanup"} {
		t.Run(mode, func(t *testing.T) { exercisePreparedMembershipScopeSQL(t, mode) })
	}
}

func exercisePreparedMembershipScopeSQL(t *testing.T, mode string) {
	t.Helper()
	f, a, targets := newSSHPreparedAuthorityFixture(t, mode == "replacement")
	authority, _, err := f.c.loadClusterSSHPreparedAuthority(f.ctx, f.op)
	if err != nil {
		t.Fatal("original settled authority", err)
	}
	// Independent fixture setup does not spend the consumer's budget or renew
	// stored expiry. The producer still owns one unchanged 60-second ceiling.
	phase, stop := context.WithTimeout(context.WithoutCancel(f.ctx), 60*time.Second)
	defer stop()
	ctx, cancel := context.WithCancel(phase)
	defer cancel()
	f.c.store.db.SetMaxOpenConns(1)
	state, err := preparedCohortSQLState(phase, f)
	if err != nil {
		t.Fatal(err)
	}
	drift := func(query string, args ...any) error {
		result, err := f.c.store.db.ExecContext(phase, query, args...)
		if err != nil {
			t.Error("fixture fault write", err)
			return err
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			t.Error("fixture fault count", count, err)
			return errors.New("fault count")
		}
		state, err = preparedCohortSQLState(phase, f)
		if err != nil {
			t.Error("fixture fault oracle", err)
		}
		return err
	}
	n := newPreparedMembershipScopeNative(t, a, authority, targets, mode)
	n.before = func(ctx context.Context) error { return recoveryProgressSQLPoolAvailable(ctx, f) }
	f.c.runner = n.runner
	childExpiry := mode == "request expiry during cleanup" || mode == "client expiry during cleanup"
	client, transport := n.runner.kube.httpClient, n.runner.kube.httpClient.Transport
	if childExpiry {
		client.Timeout = 7 * time.Second
	}
	if mode == "client expiry during cleanup" {
		client.Timeout = time.Second
	}
	timeout := client.Timeout
	childExpired := false
	closing, release := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	n.closeHook = func(closeCtx context.Context, post int) error {
		// Native IO and response cleanup can borrow the only connection. An
		// independently admitted SQL heartbeat may run, but retains no connection
		// across this boundary; final zero-InUse assertion follows joined guards.
		if err := recoveryProgressSQLPoolAvailable(phase, f); err != nil {
			t.Error("cleanup pool release", err)
			return err
		}
		if post == 12 && childExpiry {
			<-closeCtx.Done()
			childExpired = closeCtx.Err() == context.DeadlineExceeded
			if phase.Err() != nil || ctx.Err() != nil {
				t.Error("fixture expired outer scope instead of child request")
			}
		}
		if post == 12 && (mode == "expiry during cleanup" || mode == "cancel during cleanup") {
			close(closing)
			<-release
		}
		return nil
	}
	var escaped clusterSSHPreparedMembershipChecks
	consumed := false
	done := make(chan error, 1)
	go func() {
		done <- f.c.withClusterSSHPreparedMembership(ctx, f.op, func(consumer context.Context, value clusterSSHPreparedCohortMembershipObservation, checks clusterSSHPreparedMembershipChecks) error {
			consumed = true
			escaped = checks
			if n.posts.Load() != 6 || len(value.Views) != 3 {
				t.Error("redundant initial collection or missing proof")
			}
			if err := recoveryProgressSQLPoolAvailable(consumer, f); err != nil {
				t.Error("consumer pool release", err)
				return err
			}
			n.phase.Store(1)
			switch mode {
			case "payload drift", "ignored authority failure":
				if err := drift(`UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=$1`, f.op.ID); err != nil {
					return err
				}
			case "credential expiry":
				if err := drift(`UPDATE engine.cluster_onboarding_credentials SET expires_at=0 WHERE target_id=$1`, f.targets[0].Binding.TargetID); err != nil {
					return err
				}
			case "controller expiry", "heartbeat expiry":
				if err := drift(`UPDATE engine.cluster_application_leases SET expires_at=0 WHERE name=$1`, clusterControllerLeaseName); err != nil {
					return err
				}
			case "source row drift":
				if err := drift(`UPDATE engine.cluster_nodes SET application_state='drained' WHERE id=$1`, a.Source.Members[0].NodeID); err != nil {
					return err
				}
			case "target row drift":
				if err := drift(`UPDATE engine.cluster_onboarding_targets SET lease_generation=lease_generation+1 WHERE id=$1`, f.targets[0].Binding.TargetID); err != nil {
					return err
				}
			}
			if mode == "heartbeat expiry" {
				// No explicit checker runs: the original SQL heartbeat must revoke
				// a still-active synchronous consumer and join it before return.
				select {
				case <-consumer.Done():
					return nil
				case <-time.After(5 * time.Second):
					t.Error("expired SQL lease did not interrupt consumer")
					return errClusterConflict
				}
			}
			if mode == "ignored authority failure" {
				if checks.Authority(context.Background()) == nil {
					t.Error("SQL drift accepted")
				}
				if err := drift(`UPDATE engine.cluster_operations SET payload_json=$2 WHERE id=$1`, f.op.ID, authority.payload); err != nil {
					return err
				}
				if checks.Authority(context.Background()) == nil {
					t.Error("restored SQL revived failed evidence")
				}
				return nil
			}
			if mode == "explicit refresh" {
				return checks.Inputs(context.Background())
			}
			return nil
		})
	}()
	if mode == "expiry during cleanup" || mode == "cancel during cleanup" {
		select {
		case <-closing:
		case err := <-done:
			t.Fatal("cleanup not reached", err)
		case <-time.After(15 * time.Second):
			t.Fatal("cleanup not reached")
		}
		if mode == "expiry during cleanup" {
			if err := drift(`UPDATE engine.cluster_onboarding_credentials SET expires_at=0 WHERE target_id=$1`, f.targets[0].Binding.TargetID); err != nil {
				t.Fatal(err)
			}
		} else {
			cancel()
		}
		select {
		case err := <-done:
			t.Fatal("scope escaped owned cleanup", err)
		case <-time.After(20 * time.Millisecond):
		}
		unblock.Do(func() { close(release) })
	}
	select {
	case err = <-done:
	case <-time.After(30 * time.Second):
		cancel()
		t.Fatal("scope failed to join")
	}
	success := mode == "pair" || mode == "replacement" || mode == "explicit refresh"
	if !consumed || (err == nil) != success || err != nil && err != errClusterConflict {
		t.Fatal("wrong SQL scope result", err, consumed)
	}
	if success {
		want := int32(12)
		if mode == "explicit refresh" {
			want = 18
		}
		if n.posts.Load() != want {
			t.Fatal("wrong collection count", n.posts.Load(), want)
		}
	}
	posts := n.posts.Load()
	if escaped.Inputs(context.Background()) == nil || escaped.Authority(context.Background()) == nil || posts != n.posts.Load() {
		t.Fatal("escaped SQL scope revived")
	}
	if childExpiry {
		if !childExpired || phase.Err() != nil || ctx.Err() != nil || n.posts.Load() != 12 {
			t.Fatal("child expiry boundary not exercised", childExpired, n.posts.Load())
		}
		if _, _, err := f.c.loadClusterSSHPreparedAuthority(phase, f.op); err != nil {
			t.Fatal("fixture lost original SQL authority instead of child request", err)
		}
		if err := f.c.withClusterSSHPreparedMembership(phase, f.op, func(_ context.Context, value clusterSSHPreparedCohortMembershipObservation, _ clusterSSHPreparedMembershipChecks) error {
			if len(value.Views) != 3 || n.posts.Load() != 18 {
				t.Error("sibling lost fresh initial collection")
			}
			return nil
		}); err != nil || n.posts.Load() != 24 || n.runner.kube.httpClient != client || client.Transport != transport || client.Timeout != timeout {
			t.Fatal("failed cleanup poisoned SQL sibling or shared client", err, n.posts.Load())
		}
		if escaped.Authority(context.Background()) == nil {
			t.Fatal("successful sibling revived escaped failed scope")
		}
	}
	final, finalStop := context.WithTimeout(context.Background(), 5*time.Second)
	defer finalStop()
	after, err := preparedCohortSQLState(final, f)
	if err != nil || after != state {
		t.Fatal("scope mutated retained SQL state", err)
	}
	if f.c.store.db.Stats().InUse != 0 || n.active.Load() != 0 {
		t.Fatal("scope escaped SQL/HTTP cleanup", f.c.store.db.Stats().InUse, n.active.Load())
	}
}
