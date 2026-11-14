package main

import (
	"bytes"
	"context"
	"os"
	"reflect"
)

// Retain successful publication identity across later package/service commands.
// Caller must prove exact complete files through fresh no-replace publication
// or the journal-bound configured-host continuation. Partial/foreign files are
// never adopted; restart/reconciliation still requires independent native proof.
func pinBootstrapPublishedHostFiles(ctx context.Context, root *os.Root, files []bootstrapHostFile, check func(context.Context) error) (func(context.Context) error, error) {
	if root == nil || len(files) == 0 || check == nil {
		return nil, errBootstrapSource
	}
	owned := append([]bootstrapHostFile(nil), files...)
	for i := range owned {
		owned[i].data = bytes.Clone(owned[i].data)
	}
	type snapshot struct {
		parents map[string]bootstrapHostDirectoryIdentity
		files   map[string]bootstrapDockerFileState
	}
	observe := func(ctx context.Context) (snapshot, error) {
		invalid := snapshot{}
		if ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
			return invalid, errBootstrapSource
		}
		parents, err := bootstrapHostFileAncestors(root, owned)
		if err != nil {
			return invalid, errBootstrapSource
		}
		states := map[string]bootstrapDockerFileState{}
		for _, file := range owned {
			if proveBootstrapHostFile(root, file) != nil {
				return invalid, errBootstrapSource
			}
			raw, state, err := readBootstrapDockerFile(root, file.path, int64(len(file.data)))
			if err != nil || !bytes.Equal(raw, file.data) || state.mode != file.mode || ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
				return invalid, errBootstrapSource
			}
			states[file.path] = state
		}
		return snapshot{parents, states}, nil
	}
	before, err := observe(ctx)
	if err != nil {
		return nil, errBootstrapSource
	}
	pinned := func(ctx context.Context) error {
		after, err := observe(ctx)
		if err != nil || !reflect.DeepEqual(before, after) {
			return errBootstrapSource
		}
		return nil
	}
	if pinned(ctx) != nil {
		return nil, errBootstrapSource
	}
	return pinned, nil
}
