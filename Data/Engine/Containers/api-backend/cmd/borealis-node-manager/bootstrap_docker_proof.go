package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"os"
	"path/filepath"
	"reflect"
)

// Read-only and independent of download/installation success. Unlike fresh
// preflight, this remains valid after Docker packages and services exist.
func proveBootstrapDockerRepository(ctx context.Context, expected clusterbootstrap.PreparationExpected, check func(context.Context) error) error {
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		return errBootstrapSource
	}
	root, err := os.OpenRoot("/")
	if err != nil {
		return errBootstrapSource
	}
	defer root.Close()
	return proveBootstrapDockerRepositoryWith(ctx, root, expected, check)
}

func proveBootstrapDockerRepositoryWith(ctx context.Context, root *os.Root, expected clusterbootstrap.PreparationExpected, check func(context.Context) error) error {
	if expected.Validate() != nil || root == nil || check == nil {
		return errBootstrapSource
	}
	var before bootstrapDockerHost
	var keyBefore, sourceBefore bootstrapDockerFileState
	for pass := 0; pass < 2; pass++ {
		if ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
			return errBootstrapSource
		}
		current, err := observeBootstrapDockerPlatform(root)
		if err != nil {
			return errBootstrapSource
		}
		key, keyState, err := readBootstrapDockerFile(root, "etc/apt/keyrings/docker.asc", bootstrapDockerKeyLimit)
		if err != nil || !validBootstrapDockerKey(key) {
			return errBootstrapSource
		}
		files, err := bootstrapDockerRepositoryFiles(expected, current.suite, key)
		if err != nil {
			return errBootstrapSource
		}
		for _, file := range files {
			if ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil || proveBootstrapHostFile(root, file) != nil ||
				!bootstrapDockerPathAbsent(root, filepath.Join(filepath.Dir(file.path), file.pending)) {
				return errBootstrapSource
			}
		}
		_, sourceState, err := readBootstrapDockerFile(root, files[1].path, 16384)
		if err != nil {
			return errBootstrapSource
		}
		current.sources, err = bootstrapDockerSources(root, true)
		if err != nil {
			return errBootstrapSource
		}
		current.parents, err = bootstrapDockerParents(root, current.osLink != "")
		if err != nil || ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
			return errBootstrapSource
		}
		if pass == 0 {
			before, keyBefore, sourceBefore = current, keyState, sourceState
		} else if !reflect.DeepEqual(before, current) || keyBefore != keyState || sourceBefore != sourceState {
			return errBootstrapSource
		}
	}
	return nil
}

func observeBootstrapDockerPlatform(root *os.Root) (bootstrapDockerHost, error) {
	current := bootstrapDockerHost{}
	path := "etc/os-release"
	info, err := root.Lstat(path)
	if err != nil {
		return current, errBootstrapSource
	}
	if info.Mode()&os.ModeSymlink != 0 {
		current.osLink, err = root.Readlink(path)
		if err != nil || current.osLink != "../usr/lib/os-release" {
			return current, errBootstrapSource
		}
		path = "usr/lib/os-release"
	}
	raw, state, err := readBootstrapDockerFile(root, path, 16384)
	if err != nil {
		return current, errBootstrapSource
	}
	current.os = state
	current.suite, err = bootstrapDockerRelease(raw)
	return current, err
}
