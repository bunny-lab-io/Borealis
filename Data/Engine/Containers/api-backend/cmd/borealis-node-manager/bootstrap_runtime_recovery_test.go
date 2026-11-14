package main

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestBootstrapRuntimeGroupOnlyCompletion(t *testing.T) {
	for _, mode := range []string{"valid", "missing group", "existing user", "unknown account", "group drift", "authority", "late authority", "cancelled", "create failed", "missing user after create", "changed shell", "late drift"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			lock, err := os.CreateTemp(t.TempDir(), "lock")
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			state := bootstrapRuntimeAccount{Group: true}
			if mode == "missing group" {
				state.Group = false
			}
			if mode == "existing user" {
				state.User = true
				state.Shell = "/usr/sbin/nologin"
			}
			if mode == "cancelled" {
				cancel()
			}
			reads, writes, checks := 0, 0, 0
			err = completeBootstrapRuntimeIdentityWith(ctx, lock, func(context.Context) error {
				checks++
				if mode == "authority" || mode == "late authority" && writes > 0 {
					return errors.New("lost authority")
				}
				return nil
			}, func(context.Context) (bootstrapRuntimeAccount, error) {
				reads++
				if mode == "unknown account" {
					return bootstrapRuntimeAccount{}, errors.New("unknown NSS")
				}
				if mode == "group drift" && reads == 2 {
					state.Group = false
				}
				if mode == "late drift" && reads == 4 {
					state.Group = false
				}
				return state, nil
			}, func(_ context.Context, got *os.File, user bool) error {
				writes++
				if got != lock || !user || !state.Group || state.User {
					t.Fatal("recovery tried group creation or existing-account mutation")
				}
				if mode == "create failed" {
					return errors.New("useradd failed")
				}
				state.User = true
				state.Shell = "/usr/sbin/nologin"
				if mode == "missing user after create" {
					state.User = false
				}
				if mode == "changed shell" {
					state.Shell = "/bin/false"
				}
				return nil
			})
			if (err == nil) != (mode == "valid") {
				t.Fatal("unexpected completion", err)
			}
			if writes > 1 {
				t.Fatal("user creation replayed")
			}
			if mode == "valid" && (writes != 1 || reads != 4 || checks != 8) {
				t.Fatal("incomplete custody proof", writes, reads, checks)
			}
			if (mode == "missing group" || mode == "existing user" || mode == "unknown account" || mode == "group drift" || mode == "authority" || mode == "cancelled") && writes != 0 {
				t.Fatal("invalid partial state reached write")
			}
		})
	}
}
