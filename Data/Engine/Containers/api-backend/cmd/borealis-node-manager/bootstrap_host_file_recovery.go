package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"borealis/api-backend/internal/clusterbootstrap"
	"golang.org/x/sys/unix"
)

// Only the journal's configured-host continuation calls this. Existing files
// must form the exact ordered prefix; retain their inodes and publish only the
// missing suffix. Complete retained scratch can be promoted without rewriting.
func completeBootstrapHostFilesWith(ctx context.Context, root *os.Root, expected clusterbootstrap.PreparationExpected, check func(context.Context) error) error {
	files, err := bootstrapPrejoinHostFiles(expected)
	if err != nil || root == nil || check == nil {
		return errBootstrapSource
	}
	current := check
	boundary := func() error {
		if ctx.Err() != nil || current(ctx) != nil || ctx.Err() != nil || proveBootstrapHostFilePrefix(root, expected, false) != nil {
			return errBootstrapSource
		}
		return nil
	}
	if boundary() != nil {
		return errBootstrapSource
	}
	var published []bootstrapHostFile
	for _, file := range files {
		if boundary() != nil {
			return errBootstrapSource
		}
		if bootstrapDockerPathAbsent(root, file.path) {
			if bootstrapHostParents(ctx, root, filepath.Dir(file.path), boundary, true) != nil {
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
		} else if proveBootstrapHostFile(root, file) != nil {
			return errBootstrapSource
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
	return proveBootstrapPrejoinHostFilesWith(ctx, root, expected, current)
}

func promoteBootstrapRetainedHostFile(root *os.Root, file bootstrapHostFile, check func() error) (result error) {
	parentPath := filepath.Dir(file.path)
	pending := file
	pending.path = filepath.Join(parentPath, file.pending)
	if check == nil || check() != nil || proveBootstrapHostFile(root, pending) != nil || !bootstrapDockerPathAbsent(root, file.path) {
		return errBootstrapSource
	}
	parent, err := root.OpenRoot(parentPath)
	if err != nil {
		return errBootstrapSource
	}
	defer func() { result = errors.Join(result, parent.Close()) }()
	directory, err := parent.OpenFile(".", os.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		return errBootstrapSource
	}
	defer func() { result = errors.Join(result, directory.Close()) }()
	held, err := directory.Stat()
	if err != nil {
		return errBootstrapSource
	}
	scratch, err := parent.OpenFile(file.pending, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return errBootstrapSource
	}
	defer func() { result = errors.Join(result, scratch.Close()) }()
	pinned, err := scratch.Stat()
	if err != nil {
		return errBootstrapSource
	}
	if check() != nil || proveBootstrapHostFile(root, pending) != nil {
		return errBootstrapSource
	}
	current, pathErr := root.Lstat(parentPath)
	entry, entryErr := parent.Lstat(file.pending)
	if pathErr != nil || entryErr != nil || !os.SameFile(held, current) || !os.SameFile(pinned, entry) || scratch.Sync() != nil {
		return errBootstrapSource
	}
	if unix.Renameat2(int(directory.Fd()), file.pending, int(directory.Fd()), filepath.Base(file.path), unix.RENAME_NOREPLACE) != nil || directory.Sync() != nil || check() != nil || proveBootstrapHostFile(root, file) != nil {
		return errBootstrapSource
	}
	return nil
}
