package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func bootstrapHostFileProofFixture(t *testing.T) (*os.Root, []bootstrapHostFile) {
	t.Helper()
	files, err := bootstrapPrejoinHostFiles(bootstrapHostFilesFixture())
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	for _, file := range files {
		path := filepath.Join(base, file.path)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, file.data, file.mode); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"etc/rancher/k3s", "etc/rancher/k3s/config.yaml.d"} {
		if err := os.Chmod(filepath.Join(base, path), 0700); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return root, files
}

func TestBootstrapHostFilesActualNativeProof(t *testing.T) {
	for _, mode := range []string{"valid", "missing", "bytes", "mode", "hardlink", "symlink", "directory", "unsafe ancestor", "linked ancestor", "extra config", "extra directory", "wide private directory", "authority", "cancel", "last read drift", "ancestor replacement", "nil check", "nil root"} {
		t.Run(mode, func(t *testing.T) {
			root, files := bootstrapHostFileProofFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			path := filepath.Join(root.Name(), files[0].path)
			var err error
			switch mode {
			case "missing", "symlink", "directory":
				err = os.Remove(path)
				if err == nil && mode == "symlink" {
					err = os.Symlink(filepath.Join(root.Name(), files[1].path), path)
				}
				if err == nil && mode == "directory" {
					err = os.Mkdir(path, 0755)
				}
			case "bytes":
				err = os.WriteFile(path, []byte("changed"), 0644)
			case "mode":
				err = os.Chmod(path, 0666)
			case "hardlink":
				err = os.Link(path, filepath.Join(root.Name(), "other"))
			case "unsafe ancestor":
				err = os.Chmod(filepath.Join(root.Name(), "etc"), 0777)
			case "linked ancestor":
				original := filepath.Join(root.Name(), "etc/rancher")
				err = os.Rename(original, original+"-other")
				if err == nil {
					err = os.Symlink("rancher-other", original)
				}
			case "extra config":
				err = os.WriteFile(filepath.Join(root.Name(), "etc/rancher/k3s/config.yaml.d/20-other.yaml"), []byte("changed"), 0644)
			case "extra directory":
				err = os.Mkdir(filepath.Join(root.Name(), "etc/rancher/k3s/unknown"), 0700)
			case "wide private directory":
				err = os.Chmod(filepath.Join(root.Name(), "etc/rancher/k3s"), 0755)
			case "cancel":
				cancel()
			}
			if err != nil {
				t.Fatal(err)
			}
			checks := 0
			check := func(context.Context) error {
				checks++
				if mode == "authority" {
					return errors.New("private authority diagnostic")
				}
				if mode == "last read drift" && checks == 7 {
					return os.WriteFile(path, []byte("changed between passes"), 0644)
				}
				if mode == "ancestor replacement" && checks == 7 {
					parent := filepath.Join(root.Name(), "etc/rancher/k3s/config.yaml.d")
					if err := os.Rename(parent, parent+"-old"); err != nil {
						return err
					}
					if err := os.Mkdir(parent, 0700); err != nil {
						return err
					}
					return os.WriteFile(path, files[0].data, files[0].mode)
				}
				return nil
			}
			if mode == "nil check" {
				check = nil
			}
			if mode == "nil root" {
				root = nil
			}
			err = proveBootstrapPrejoinHostFilesWith(ctx, root, bootstrapHostFilesFixture(), check)
			if (err == nil) != (mode == "valid") || err != nil && err != errBootstrapSource {
				t.Fatal("invalid actual host files acknowledged", err)
			}
			if mode == "valid" && checks != 11 {
				t.Fatal("missing current authority brackets", checks)
			}
		})
	}
}
