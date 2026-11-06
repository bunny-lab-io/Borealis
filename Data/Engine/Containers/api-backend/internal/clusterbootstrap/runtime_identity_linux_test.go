package clusterbootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeIdentityIntentAndExactReconciliation(t *testing.T) {
	for _, mode := range []string{"success", "unknown effect", "partial effect", "source drift", "authority drift"} {
		t.Run(mode, func(t *testing.T) {
			p, j, journal, parent := sourcePublicationFixture(t)
			ctx := context.Background()
			if _, err := p.publishSource(ctx, j, parent); err != nil {
				t.Fatal(err)
			}
			calls := 0
			ready := false
			actions := RuntimeIdentityActions{
				Prepare: func(_ context.Context, lock *os.File) error {
					calls++
					if lock == nil {
						t.Fatal("missing child lock")
					}
					if readMutationFixture(t, journal).Steps["prepare_runtime_identity"].State != "intent" {
						t.Fatal("account write preceded intent")
					}
					ready = mode != "partial effect"
					if mode == "source drift" {
						if err := os.WriteFile(filepath.Join(parent, "Borealis", "Engine.sh"), []byte("changed"), 0755); err != nil {
							t.Fatal(err)
						}
					}
					if mode == "authority drift" {
						j.check = func(context.Context) error { return ErrSessionAuthority }
					}
					if mode == "unknown effect" {
						return errors.New("private lost command result")
					}
					return nil
				},
				Prove: func(context.Context) error {
					if !ready {
						return errors.New("private incomplete account")
					}
					return nil
				},
			}
			_, err := p.prepareRuntimeIdentity(ctx, j, actions, parent, os.Geteuid(), os.Getegid(), false)
			if (err == nil) != (mode == "success") || calls != 1 {
				t.Fatal("unexpected account outcome", err, calls)
			}
			if _, err := p.prepareRuntimeIdentity(ctx, j, actions, parent, os.Geteuid(), os.Getegid(), false); err == nil || calls != 1 {
				t.Fatal("account write replayed")
			}
			if mode == "success" || mode == "unknown effect" || mode == "partial effect" {
				if _, err := p.prepareRuntimeIdentity(ctx, j, actions, parent, os.Geteuid(), os.Getegid(), true); err != ErrMutationUnknown {
					t.Fatal("producer settled itself", err)
				}
				j = reopenSourcePublication(t, p, j, journal)
				_, err := p.prepareRuntimeIdentity(ctx, j, RuntimeIdentityActions{Prove: actions.Prove}, parent, os.Geteuid(), os.Getegid(), true)
				if (err == nil) != (mode != "partial effect") {
					t.Fatal("incorrect recovery proof", err)
				}
				if calls != 1 {
					t.Fatal("proof invoked writer")
				}
			}
		})
	}
}
