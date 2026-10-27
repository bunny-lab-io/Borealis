package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Filesystem-only adapter. Caller owns durable install intent and both fences.
// No installer, binary, service command, enablement or image import runs here.
func installBootstrapPrejoinK3sFilesWith(ctx context.Context, root *os.Root, expected clusterbootstrap.PreparationExpected, check func(context.Context) error) error {
	files, err := bootstrapPrejoinK3sFiles(expected)
	if err != nil || root == nil || check == nil {
		return errBootstrapSource
	}
	boundary := func() error {
		if ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
			return errBootstrapSource
		}
		return nil
	}
	if boundary() != nil || verifyBootstrapK3sFilePrefix(root, expected, false, true) != nil {
		return errBootstrapSource
	}
	ancestors, err := bootstrapHostFileAncestors(root, files)
	if err != nil {
		return errBootstrapSource
	}
	stable := func() error {
		if boundary() != nil {
			return errBootstrapSource
		}
		current, err := bootstrapHostFileAncestors(root, files)
		if err != nil || len(current) != len(ancestors) {
			return errBootstrapSource
		}
		for path, original := range ancestors {
			if current[path] != original {
				return errBootstrapSource
			}
		}
		return nil
	}
	for _, file := range files {
		if stable() != nil || verifyBootstrapK3sFilePrefix(root, expected, false, false) != nil ||
			publishBootstrapHostFile(root, file, stable) != nil {
			return errBootstrapSource
		}
	}
	if stable() != nil || verifyBootstrapK3sFilePrefix(root, expected, true, false) != nil || stable() != nil {
		return errBootstrapSource
	}
	return nil
}

// Recognize only ordered final files and at most one exact complete pending
// file. Recognition grants no replay/promotion authority. Truncated/foreign
// scratch, drop-ins, symlinks and a unit without its environment remain fenced.
func verifyBootstrapK3sFilePrefix(root *os.Root, expected clusterbootstrap.PreparationExpected, complete, fresh bool) error {
	files, err := bootstrapPrejoinK3sFiles(expected)
	if err != nil || root == nil || complete && fresh {
		return errBootstrapSource
	}
	if _, err := bootstrapHostFileAncestors(root, files); err != nil {
		return errBootstrapSource
	}
	for _, path := range []string{"etc/systemd/system/k3s.service.d", "etc/systemd/system/k3s-agent.service", "etc/systemd/system/k3s-agent.service.d"} {
		if _, err := root.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return errBootstrapSource
		}
	}
	entries, err := bootstrapArtifactDirectory(root, "etc/systemd/system", 4097)
	if err != nil {
		return errBootstrapSource
	}
	wantPending := map[string]bool{}
	ended, pending := false, false
	for _, file := range files {
		wantPending[file.pending] = true
		_, finalErr := root.Lstat(file.path)
		scratch := file
		scratch.path = filepath.Join(filepath.Dir(file.path), file.pending)
		_, pendingErr := root.Lstat(scratch.path)
		if finalErr == nil {
			if fresh || ended || !errors.Is(pendingErr, os.ErrNotExist) || proveBootstrapHostFile(root, file) != nil {
				return errBootstrapSource
			}
			continue
		}
		if !errors.Is(finalErr, os.ErrNotExist) || complete {
			return errBootstrapSource
		}
		if pendingErr == nil {
			if fresh || ended || pending || proveBootstrapHostFile(root, scratch) != nil {
				return errBootstrapSource
			}
			pending = true
		} else if !errors.Is(pendingErr, os.ErrNotExist) {
			return errBootstrapSource
		}
		ended = true
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".borealis-k3s-") && !wantPending[entry.Name()] {
			return errBootstrapSource
		}
	}
	return nil
}
