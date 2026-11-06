package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// Called only inside the fixed native host action, after durable prepare_host
// intent and while both borrowed locks remain held. It does not execute the
// rendered firewall, reload/start services or install packages. Partial effects
// stay for separate proof/recovery; existing destination files are never adopted
// or replaced by this fresh-write path.
func installBootstrapPrejoinHostFiles(ctx context.Context, expected clusterbootstrap.PreparationExpected, check func(context.Context) error) error {
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		return errBootstrapSource
	}
	root, err := os.OpenRoot("/")
	if err != nil {
		return errBootstrapSource
	}
	defer root.Close()
	return installBootstrapPrejoinHostFilesWith(ctx, root, expected, check)
}

func installBootstrapPrejoinHostFilesWith(ctx context.Context, root *os.Root, expected clusterbootstrap.PreparationExpected, check func(context.Context) error) error {
	files, err := bootstrapPrejoinHostFiles(expected)
	if err != nil || root == nil || check == nil {
		return errBootstrapSource
	}
	boundary := func() error {
		if ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
			return errBootstrapSource
		}
		return nil
	}
	if boundary() != nil {
		return errBootstrapSource
	}
	// Preflight every parent and final path before consuming any file effects.
	for _, file := range files {
		if bootstrapHostParents(ctx, root, filepath.Dir(file.path), boundary, false) != nil {
			return errBootstrapSource
		}
		if _, err := root.Lstat(file.path); !errors.Is(err, os.ErrNotExist) {
			return errBootstrapSource
		}
	}
	for _, file := range files {
		if boundary() != nil || bootstrapHostParents(ctx, root, filepath.Dir(file.path), boundary, true) != nil ||
			publishBootstrapHostFile(root, file, boundary) != nil {
			return errBootstrapSource
		}
	}
	return proveBootstrapPrejoinHostFilesWith(ctx, root, expected, check)
}

// Missing parents are allowed during read-only preflight. Existing components
// must be real root-owned non-writable directories; never follow a link. Create
// only fixed missing components, with explicit modes despite helper umask0077.
func bootstrapHostParents(ctx context.Context, root *os.Root, path string, check func() error, create bool) error {
	current := ""
	for _, part := range strings.Split(path, "/") {
		current = filepath.Join(current, part)
		if ctx.Err() != nil || check() != nil {
			return errBootstrapSource
		}
		info, err := root.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if !create {
				return nil
			}
			mode := os.FileMode(0755)
			if current == "etc/rancher/k3s" || current == "etc/rancher/k3s/config.yaml.d" {
				mode = 0700
			}
			if root.Mkdir(current, mode) != nil {
				return errBootstrapSource
			}
			child, openErr := root.OpenFile(current, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
			if openErr != nil {
				return errBootstrapSource
			}
			modeErr, syncErr := child.Chmod(mode), child.Sync()
			closeErr := child.Close()
			if errors.Join(modeErr, syncErr, closeErr) != nil || syncBootstrapHostDirectory(root, filepath.Dir(current)) != nil || check() != nil {
				return errBootstrapSource
			}
			info, err = root.Lstat(current)
		}
		if err != nil || !info.IsDir() || info.Mode().Perm()&0022 != 0 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return errBootstrapSource
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || int(owner.Uid) != os.Geteuid() || int(owner.Gid) != os.Getegid() {
			return errBootstrapSource
		}
	}
	return nil
}

func syncBootstrapHostDirectory(root *os.Root, path string) error {
	directory, err := root.OpenFile(path, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return errBootstrapSource
	}
	err = directory.Sync()
	return errors.Join(err, directory.Close())
}

func publishBootstrapHostFile(root *os.Root, expected bootstrapHostFile, check func() error) (result error) {
	parentPath := filepath.Dir(expected.path)
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
	boundary := func() error {
		current, err := root.Lstat(parentPath)
		if err != nil || !os.SameFile(held, current) || check() != nil {
			return errBootstrapSource
		}
		return nil
	}
	if boundary() != nil {
		return errBootstrapSource
	}
	name := expected.pending
	file, err := parent.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return errBootstrapSource
	}
	defer func() {
		if err := parent.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, errBootstrapSource)
		}
		result = errors.Join(result, directory.Sync())
	}()
	_, writeErr := file.Write(expected.data)
	modeErr, syncErr := file.Chmod(expected.mode), file.Sync()
	closeErr := file.Close()
	if errors.Join(writeErr, modeErr, syncErr, closeErr) != nil || boundary() != nil {
		return errBootstrapSource
	}
	if unix.Renameat2(int(directory.Fd()), name, int(directory.Fd()), filepath.Base(expected.path), unix.RENAME_NOREPLACE) != nil || directory.Sync() != nil || boundary() != nil || proveBootstrapHostFile(root, expected) != nil {
		return errBootstrapSource
	}
	return nil
}
