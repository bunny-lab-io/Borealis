package main

import (
	"context"
	"errors"
	"io"
	"os"
	"sort"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// Read actual sysfs, never modprobe output. No module load, block-device IO,
// multipath flush or daemon stop is permitted in prepared-state observation.
func proveBootstrapPrejoinKernel(ctx context.Context, check func(context.Context) error) error {
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		return errBootstrapSource
	}
	root, err := os.OpenRoot("/sys")
	if err != nil {
		return errBootstrapSource
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return errBootstrapSource
	}
	var fs unix.Statfs_t
	statErr := unix.Fstatfs(int(dir.Fd()), &fs)
	closeErr := dir.Close()
	if statErr != nil || closeErr != nil || fs.Type != unix.SYSFS_MAGIC {
		return errBootstrapSource
	}
	return proveBootstrapPrejoinKernelWith(ctx, root, check)
}

func proveBootstrapPrejoinKernelWith(ctx context.Context, root *os.Root, check func(context.Context) error) error {
	if root == nil || check == nil {
		return errBootstrapSource
	}
	boundary := func() error {
		if ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
			return errBootstrapSource
		}
		return nil
	}
	read := func(path string, limit int64) (string, error) {
		before, err := root.Lstat(path)
		if err != nil || !before.Mode().IsRegular() {
			return "", errBootstrapSource
		}
		file, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return "", errBootstrapSource
		}
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() || !os.SameFile(before, info) {
			file.Close()
			return "", errBootstrapSource
		}
		raw, readErr := io.ReadAll(io.LimitReader(file, limit+1))
		closeErr := file.Close()
		after, pathErr := root.Lstat(path)
		if readErr != nil || closeErr != nil || pathErr != nil || !after.Mode().IsRegular() || !os.SameFile(info, after) || int64(len(raw)) > limit {
			return "", errBootstrapSource
		}
		return string(raw), nil
	}
	previous := ""
	for pass := 0; pass < 2; pass++ {
		if boundary() != nil {
			return errBootstrapSource
		}
		module, err := read("module/iscsi_tcp/initstate", 32)
		if err != nil || module != "live\n" || boundary() != nil {
			return errBootstrapSource
		}
		dir, err := root.OpenFile("class/block", os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return errBootstrapSource
		}
		entries, readErr := dir.ReadDir(4097)
		closeErr := dir.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) || closeErr != nil || len(entries) > 4096 {
			return errBootstrapSource
		}
		inventory := make([]string, 0, len(entries))
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), "dm-") {
				continue
			}
			if boundary() != nil {
				return errBootstrapSource
			}
			// class/block links are kernel-owned sysfs links inside the held
			// sysfs root. OpenRoot confines traversal; final attributes cannot
			// be links. Unknown/inaccessible maps never count as absence.
			uuid, err := read("class/block/"+entry.Name()+"/dm/uuid", 4096)
			if err != nil || !strings.HasSuffix(uuid, "\n") || strings.Count(uuid, "\n") != 1 || strings.HasPrefix(uuid, "mpath-") {
				return errBootstrapSource
			}
			for _, value := range uuid[:len(uuid)-1] {
				if value < ' ' || value > '~' {
					return errBootstrapSource
				}
			}
			inventory = append(inventory, entry.Name()+"="+uuid)
		}
		sort.Strings(inventory)
		current := strings.Join(inventory, "")
		if pass > 0 && current != previous || boundary() != nil {
			return errBootstrapSource
		}
		previous = current
	}
	return nil
}
