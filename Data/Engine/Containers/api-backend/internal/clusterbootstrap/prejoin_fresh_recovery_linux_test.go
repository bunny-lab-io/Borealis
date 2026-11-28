package clusterbootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPrejoinFreshHostRecovery(t *testing.T) {
	for _, mode := range []string{"success", "lost acknowledgement", "interrupted again", "source drift", "key drift"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			p, j, path, parent, install, material := prejoinHostFixture(t)
			configured, complete := false, false
			prove := func(context.Context) error {
				if !complete {
					return ErrMutationUnknown
				}
				return nil
			}
			_, err := p.preparePrejoinHost(ctx, j, install, material, PrejoinHostActions{
				CheckFresh: func(context.Context) error { return nil },
				Prepare: func(context.Context, *os.File, *os.File) error {
					configured = true
					return errors.New("packages configured, files incomplete")
				}, Prove: prove,
			}, parent, false)
			if err != ErrMutationUnknown {
				t.Fatal(err)
			}
			old := readMutationFixture(t, path).Steps["prepare_host"]
			j = reopenSourcePublication(t, p, j, path)
			calls := 0
			actions := PrejoinHostActions{
				CheckFresh: func(context.Context) error {
					requirePrejoinHostLock(t, j.lock, j.lock.Name())
					identityConsumerBlocked(t, install.Root)
					if !configured || complete {
						return ErrMutationUnknown
					}
					return nil
				},
				Prepare: func(_ context.Context, host, identity *os.File) error {
					calls++
					state := readMutationFixture(t, path)
					if state.Version != 4 || len(state.History) != calls || state.History[0].Record.Executor != old.Executor || state.History[0].Record.Binding != old.Binding || state.History[0].Record.ResultSHA256 != prejoinHostFreshDigest(old.InputSHA256) || state.Steps["prepare_host"].Executor != j.executor || state.Steps["prepare_host"].State != "intent" {
						t.Fatal("host effects preceded durable producer transfer")
					}
					requirePrejoinHostLock(t, host, j.lock.Name())
					requirePrejoinHostLock(t, identity, install.Root)
					identityConsumerBlocked(t, install.Root)
					if mode == "interrupted again" && calls == 1 {
						return errors.New("interrupted")
					}
					complete = true
					if mode == "lost acknowledgement" {
						return errors.New("complete, ack lost")
					}
					return nil
				}, Prove: prove,
			}
			if mode == "source drift" {
				if err := os.WriteFile(filepath.Join(parent, "Borealis", "Engine.sh"), []byte("foreign"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "key drift" {
				for name := range material.Files() {
					if err := os.WriteFile(filepath.Join(install.Root, name), []byte("foreign"), 0600); err != nil {
						t.Fatal(err)
					}
					break
				}
			}
			_, err = p.resumeFreshPrejoinHost(ctx, j, install, material, actions, parent)
			if (err == nil) != (mode == "success") {
				t.Fatal("incorrect continuation", err)
			}
			if mode == "source drift" || mode == "key drift" {
				if calls != 0 || readMutationFixture(t, path).Version != 2 {
					t.Fatal("drift consumed continuation")
				}
				return
			}
			if _, err := p.resumeFreshPrejoinHost(ctx, j, install, material, actions, parent); err == nil || calls != 1 {
				t.Fatal("same producer replayed", err, calls)
			}
			j = reopenSourcePublication(t, p, j, path)
			if mode == "interrupted again" {
				if _, err := p.resumeFreshPrejoinHost(ctx, j, install, material, actions, parent); err != nil || calls != 2 {
					t.Fatal("second claim failed", err, calls)
				}
				j = reopenSourcePublication(t, p, j, path)
			}
			if _, err := p.preparePrejoinHost(ctx, j, install, material, PrejoinHostActions{Prove: prove}, parent, true); err != nil {
				t.Fatal("independent complete proof", err)
			}
			state := readMutationFixture(t, path)
			if state.Steps["prepare_host"].State != "applied" || state.History[0].Record.State != "continued" {
				t.Fatal("lost history")
			}
			if progress, err := p.InspectPreparationProgress(ctx, j); err != nil || progress.Step != "prepare_host" {
				t.Fatal("resume lost continuation", progress, err)
			}
			if install.ProvePrejoinFencedCommitted(material) != nil {
				t.Fatal("identity fence changed")
			}
		})
	}
}
