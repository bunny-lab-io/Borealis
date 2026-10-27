package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootstrapDockerRepositoryIndependentProof(t *testing.T) {
	for _, mode := range []string{"valid", "installed docker", "key", "source", "suite", "duplicate source", "scratch", "mode", "link", "missing", "cancel", "late cancel", "authority", "drift"} {
		t.Run(mode, func(t *testing.T) {
			root, read := bootstrapDockerFixture(t)
			expected, key := bootstrapHostFilesFixture(), bootstrapDockerKeyFixture(t)
			check := func(context.Context) error { return nil }
			if installBootstrapDockerRepositoryWith(context.Background(), root, expected, check, read, func(context.Context) ([]byte, error) { return key, nil }) != nil {
				t.Fatal("fixture publication failed")
			}
			files, _ := bootstrapDockerRepositoryFiles(expected, "noble", key)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "installed docker":
				bootstrapDockerWrite(t, root, "usr/bin/docker", "installed package binary")
			case "key":
				bootstrapDockerWrite(t, root, files[0].path, "untrusted")
			case "source":
				bootstrapDockerWrite(t, root, files[1].path, strings.Replace(string(files[1].data), "stable", "test", 1))
			case "suite":
				bootstrapDockerWrite(t, root, "etc/os-release", "ID=ubuntu\nVERSION_ID=24.10\nVERSION_CODENAME=oracular\n")
			case "duplicate source":
				bootstrapDockerWrite(t, root, "etc/apt/sources.list.d/duplicate.list", "deb https://download.docker.com/linux/ubuntu noble stable\n")
			case "scratch":
				bootstrapDockerWrite(t, root, filepath.Join(filepath.Dir(files[1].path), files[1].pending), string(files[1].data))
			case "mode":
				if root.Chmod(files[0].path, 0600) != nil {
					t.Fatal("chmod")
				}
			case "link":
				if root.Remove(files[1].path) != nil || os.Symlink("ubuntu.sources", filepath.Join(root.Name(), files[1].path)) != nil {
					t.Fatal("symlink")
				}
			case "missing":
				if root.Remove(files[0].path) != nil {
					t.Fatal("remove")
				}
			case "cancel":
				cancel()
			case "authority":
				check = func(context.Context) error { return errors.New("private") }
			case "late cancel":
				calls := 0
				check = func(context.Context) error {
					calls++
					if calls == 8 {
						cancel()
					}
					return nil
				}
			case "drift":
				calls := 0
				check = func(context.Context) error {
					calls++
					if calls == 5 {
						bootstrapDockerWrite(t, root, "etc/apt/sources.list.d/ubuntu.sources", "Types: deb\nURIs: http://other.invalid/ubuntu\n")
					}
					return nil
				}
			}
			err := proveBootstrapDockerRepositoryWith(ctx, root, expected, check)
			if (err == nil) != (mode == "valid" || mode == "installed docker") || err != nil && err != errBootstrapSource {
				t.Fatal("incomplete repository accepted or diagnostic leak", err)
			}
		})
	}
}
