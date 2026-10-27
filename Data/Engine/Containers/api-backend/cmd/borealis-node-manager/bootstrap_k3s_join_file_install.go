package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"errors"
	"os"
	"path/filepath"
)

// Filesystem-only effect. Native caller must hold both fences under durable
// configuration intent and separately prove loaded/disabled/inactive K3s.
// This function never starts/enables/reloads a service or releases consumers.
func installBootstrapK3sJoinFiles(ctx context.Context, root *os.Root, expected clusterbootstrap.PreparationExpected, plan bootstrapK3sJoinFiles, check func(context.Context) error) error {
	if root == nil || check == nil || ctx.Err() != nil || check(ctx) != nil || verifyBootstrapK3sJoinFilePrefix(root, expected, plan, false, true) != nil {
		return errBootstrapSource
	}
	before, err := bootstrapHostFileAncestors(root, plan.files)
	if err != nil {
		return errBootstrapSource
	}
	stable := func() error {
		if ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
			return errBootstrapSource
		}
		after, err := bootstrapHostFileAncestors(root, plan.files)
		if err != nil || len(after) != len(before) {
			return errBootstrapSource
		}
		for path, identity := range before {
			if after[path] != identity {
				return errBootstrapSource
			}
		}
		return nil
	}
	for _, file := range plan.files {
		if stable() != nil || verifyBootstrapK3sJoinFilePrefix(root, expected, plan, false, false) != nil || publishBootstrapHostFile(root, file, stable) != nil {
			return errBootstrapSource
		}
	}
	if stable() != nil || verifyBootstrapK3sJoinFilePrefix(root, expected, plan, true, false) != nil || stable() != nil {
		return errBootstrapSource
	}
	return nil
}

// Recognize token then configuration, with at most one exact complete scratch.
// This grants no retry, adoption, promotion or cleanup permission. Incomplete or
// foreign bytes remain unknown, and old host/file proofs still reject this state.
func verifyBootstrapK3sJoinFilePrefix(root *os.Root, expected clusterbootstrap.PreparationExpected, plan bootstrapK3sJoinFiles, complete, fresh bool) error {
	if root == nil || complete && fresh || len(plan.files) != 2 || plan.files[0].path != "etc/rancher/k3s/borealis-server.token" || plan.files[1].path != "etc/rancher/k3s/config.yaml.d/20-borealis-cluster-join.yaml" {
		return errBootstrapSource
	}
	base, err := bootstrapPrejoinHostFiles(expected)
	if err != nil {
		return errBootstrapSource
	}
	if _, err := bootstrapHostFileAncestors(root, append(base, plan.files...)); err != nil {
		return errBootstrapSource
	}
	for _, file := range base {
		if proveBootstrapHostFile(root, file) != nil {
			return errBootstrapSource
		}
	}
	inventory := map[string]map[string]bool{
		"etc/rancher/k3s":               {"config.yaml.d": true, "registries.yaml": false},
		"etc/rancher/k3s/config.yaml.d": {"10-borealis.yaml": false},
	}
	ended, pending := false, false
	for _, file := range plan.files {
		dir := filepath.Dir(file.path)
		_, finalErr := root.Lstat(file.path)
		scratch := file
		scratch.path = filepath.Join(dir, file.pending)
		_, scratchErr := root.Lstat(scratch.path)
		if finalErr == nil {
			if fresh || ended || !errors.Is(scratchErr, os.ErrNotExist) || proveBootstrapHostFile(root, file) != nil {
				return errBootstrapSource
			}
			inventory[dir][filepath.Base(file.path)] = false
			continue
		}
		if !errors.Is(finalErr, os.ErrNotExist) || complete {
			return errBootstrapSource
		}
		if scratchErr == nil {
			if fresh || ended || pending || proveBootstrapHostFile(root, scratch) != nil {
				return errBootstrapSource
			}
			pending = true
			inventory[dir][file.pending] = false
		} else if !errors.Is(scratchErr, os.ErrNotExist) {
			return errBootstrapSource
		}
		ended = true
	}
	for path, entries := range inventory {
		if proveBootstrapHostDirectory(root, path, entries) != nil {
			return errBootstrapSource
		}
	}
	return nil
}
