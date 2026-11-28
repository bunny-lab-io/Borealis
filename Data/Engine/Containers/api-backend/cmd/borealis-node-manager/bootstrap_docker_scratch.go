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
)

// Fresh continuation must not consume base-package effects while an interrupted
// repository publication remains. Refuse every retained private scratch name;
// only a separately proved repository continuation may adopt exact full bytes.
func proveBootstrapDockerScratchAbsent(ctx context.Context, root *os.Root, check func(context.Context) error) error {
	return proveBootstrapDockerScratch(ctx, root, check, nil)
}

func proveBootstrapDockerScratch(ctx context.Context, root *os.Root, check func(context.Context) error, allowed []bootstrapHostFile) error {
	if root == nil || check == nil {
		return errBootstrapSource
	}
	for _, path := range []string{"etc/apt/keyrings", "etc/apt/sources.list.d"} {
		if ctx.Err() != nil || check(ctx) != nil {
			return errBootstrapSource
		}
		if bootstrapDockerPathAbsent(root, path) {
			continue
		}
		files := []bootstrapHostFile{{path: filepath.Join(path, "observation")}}
		before, err := bootstrapHostFileAncestors(root, files)
		if err != nil {
			return errBootstrapSource
		}
		dir, err := root.OpenFile(path, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return errBootstrapSource
		}
		entries, readErr := dir.ReadDir(129)
		closeErr := dir.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) || closeErr != nil || len(entries) > 128 {
			return errBootstrapSource
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".borealis-docker-") {
				found := false
				for _, file := range allowed {
					if file.path == filepath.Join(path, entry.Name()) && proveBootstrapHostFile(root, file) == nil {
						found = true
					}
				}
				if !found {
					return errBootstrapSource
				}
			}
		}
		after, err := bootstrapHostFileAncestors(root, files)
		if err != nil || !reflect.DeepEqual(before, after) || ctx.Err() != nil || check(ctx) != nil {
			return errBootstrapSource
		}
	}
	if ctx.Err() != nil || check(ctx) != nil {
		return errBootstrapSource
	}
	return nil
}
