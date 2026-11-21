package main

import (
	"context"
	"os"
)

func proveBootstrapRuntimeGroupOnly(ctx context.Context) error {
	state, err := observeBootstrapRuntimeAccount(ctx)
	if err != nil || state != (bootstrapRuntimeAccount{Group: true}) {
		return errBootstrapRuntimeAccount
	}
	return nil
}

// Forward recovery has exactly one native operation: create the missing fixed
// user. It never calls groupadd or modifies an existing account. Caller must
// already have durably transferred the supervised journal intent/history.
func completeBootstrapRuntimeIdentity(ctx context.Context, lock *os.File, check func(context.Context) error) error {
	if os.Geteuid() != 0 {
		return errBootstrapRuntimeAccount
	}
	return completeBootstrapRuntimeIdentityWith(ctx, lock, check, observeBootstrapRuntimeAccount, createBootstrapRuntimeAccount)
}

func completeBootstrapRuntimeIdentityWith(ctx context.Context, lock *os.File, check func(context.Context) error,
	observe func(context.Context) (bootstrapRuntimeAccount, error), create func(context.Context, *os.File, bool) error) error {
	if lock == nil || check == nil || observe == nil || create == nil {
		return errBootstrapRuntimeAccount
	}
	prove := func(want bootstrapRuntimeAccount) error {
		if ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
			return errBootstrapRuntimeAccount
		}
		state, err := observe(ctx)
		if err != nil || state != want || ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
			return errBootstrapRuntimeAccount
		}
		return nil
	}
	partial := bootstrapRuntimeAccount{Group: true}
	if prove(partial) != nil || prove(partial) != nil {
		return errBootstrapRuntimeAccount
	}
	if create(ctx, lock, true) != nil {
		return errBootstrapRuntimeAccount
	}
	complete := bootstrapRuntimeAccount{User: true, Group: true, Shell: "/usr/sbin/nologin"}
	if prove(complete) != nil || prove(complete) != nil {
		return errBootstrapRuntimeAccount
	}
	return nil
}
