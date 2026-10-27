package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"

	"borealis/api-backend/internal/clusterbootstrap"
)

type bootstrapDockerRepositoryPrefix struct {
	host                      bootstrapDockerHost
	files, installed, pending []bootstrapHostFile
}

// Classify only exact ordered repository effects. A complete retained key is
// required; no download or write is allowed while deciding whether to resume.
func observeBootstrapDockerRepositoryPrefix(ctx context.Context, root *os.Root, expected clusterbootstrap.PreparationExpected,
	check func(context.Context) error, read func(context.Context, string) ([]byte, error)) (bootstrapDockerRepositoryPrefix, error) {
	invalid := bootstrapDockerRepositoryPrefix{}
	if root == nil || expected.Validate() != nil || check == nil || read == nil || ctx.Err() != nil || check(ctx) != nil {
		return invalid, errBootstrapSource
	}
	platform, err := observeBootstrapDockerPlatform(root)
	if err != nil {
		return invalid, errBootstrapSource
	}
	keyPath := "etc/apt/keyrings/docker.asc"
	if bootstrapDockerPathAbsent(root, keyPath) {
		// Discover one candidate only; its bytes AND source-bound name must
		// match below. Unknown, incomplete or multiple scratch is never adopted.
		if bootstrapHostParents(ctx, root, "etc/apt/keyrings", func() error { return check(ctx) }, false) != nil {
			return invalid, errBootstrapSource
		}
		dir, err := root.OpenFile("etc/apt/keyrings", os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return invalid, errBootstrapSource
		}
		entries, readErr := dir.ReadDir(129)
		closeErr := dir.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) || closeErr != nil || len(entries) > 128 {
			return invalid, errBootstrapSource
		}
		keyPath = ""
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".borealis-docker-") {
				if keyPath != "" {
					return invalid, errBootstrapSource
				}
				keyPath = filepath.Join("etc/apt/keyrings", entry.Name())
			}
		}
		if keyPath == "" {
			return invalid, errBootstrapSource
		}
	}
	key, _, err := readBootstrapDockerFile(root, keyPath, bootstrapDockerKeyLimit)
	if err != nil {
		return invalid, errBootstrapSource
	}
	files, err := bootstrapDockerRepositoryFiles(expected, platform.suite, key)
	if err != nil {
		return invalid, errBootstrapSource
	}
	result := bootstrapDockerRepositoryPrefix{files: files}
	unpublished := false
	for _, file := range files {
		pending := file
		pending.path = filepath.Join(filepath.Dir(file.path), file.pending)
		if !bootstrapDockerPathAbsent(root, file.path) {
			if unpublished || !bootstrapDockerPathAbsent(root, pending.path) || proveBootstrapHostFile(root, file) != nil {
				return invalid, errBootstrapSource
			}
			result.installed = append(result.installed, file)
			continue
		}
		if !bootstrapDockerPathAbsent(root, pending.path) {
			if unpublished || proveBootstrapHostFile(root, pending) != nil {
				return invalid, errBootstrapSource
			}
			result.pending = append(result.pending, pending)
		}
		unpublished = true
	}
	if len(result.installed)+len(result.pending) == 0 || proveBootstrapDockerScratch(ctx, root, check, result.pending) != nil {
		return invalid, errBootstrapSource
	}
	result.host, err = observeBootstrapDockerHostPrefix(ctx, root, result.installed, result.pending, check, read)
	if err != nil || result.host.suite != platform.suite || result.host.os != platform.os || result.host.osLink != platform.osLink || validateBootstrapHostBase(result.host.probes["packages"]) != nil || ctx.Err() != nil || check(ctx) != nil {
		return invalid, errBootstrapSource
	}
	return result, nil
}

// Preserve existing file identities, promote complete owned scratch without
// overwrite, and publish only the absent suffix. No network/package effects.
func completeBootstrapDockerRepositoryWith(ctx context.Context, root *os.Root, expected clusterbootstrap.PreparationExpected,
	check func(context.Context) error, read func(context.Context, string) ([]byte, error)) error {
	before, err := observeBootstrapDockerRepositoryPrefix(ctx, root, expected, check, read)
	if err != nil {
		return errBootstrapSource
	}
	current := check
	boundary := func() error {
		if ctx.Err() != nil || current(ctx) != nil {
			return errBootstrapSource
		}
		after, err := observeBootstrapDockerRepositoryPrefix(ctx, root, expected, current, read)
		if err != nil || !reflect.DeepEqual(before.host, after.host) {
			return errBootstrapSource
		}
		return nil
	}
	// Pin every existing final file across every subsequent effect. Pending
	// identity is held by the no-replace promoter during its transition.
	if len(before.installed) > 0 {
		current, err = pinBootstrapPublishedHostFiles(ctx, root, before.installed, check)
		if err != nil {
			return errBootstrapSource
		}
	}
	var published []bootstrapHostFile
	for _, file := range before.files {
		if boundary() != nil {
			return errBootstrapSource
		}
		if bootstrapDockerPathAbsent(root, file.path) {
			// Fresh repository setup creates both parent directories before
			// either publication. Missing/replaced parents reject recovery.
			if bootstrapDockerPathAbsent(root, filepath.Dir(file.path)) {
				return errBootstrapSource
			}
			if bootstrapDockerPathAbsent(root, filepath.Join(filepath.Dir(file.path), file.pending)) {
				err = publishBootstrapHostFile(root, file, boundary)
			} else {
				err = promoteBootstrapRetainedHostFile(root, file, boundary)
			}
			if err != nil || boundary() != nil {
				return errBootstrapSource
			}
		}
		published = append(published, file)
		next, err := pinBootstrapPublishedHostFiles(ctx, root, published, check)
		if err != nil || boundary() != nil {
			return errBootstrapSource
		}
		current = next
	}
	if boundary() != nil {
		return errBootstrapSource
	}
	return proveBootstrapDockerRepositoryWith(ctx, root, expected, current)
}
