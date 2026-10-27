package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestBootstrapHostFilesParentPreparation(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	files, err := bootstrapPrejoinHostFiles(bootstrapHostFilesFixture())
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		for _, create := range []bool{false, true, true} {
			if err := bootstrapHostParents(context.Background(), root, filepath.Dir(file.path), func() error { return nil }, create); err != nil {
				t.Fatalf("parent preparation %s create=%v: %v", file.path, create, err)
			}
		}
		if err := publishBootstrapHostFile(root, file, func() error { return nil }); err != nil {
			t.Fatalf("publication %s: %v", file.path, err)
		}
	}
}

func TestBootstrapHostFilesFreshPublication(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	expected := bootstrapHostFilesFixture()
	checks := 0
	check := func(context.Context) error { checks++; return nil }
	if err := installBootstrapPrejoinHostFilesWith(context.Background(), root, expected, check); err != nil {
		t.Fatal(err)
	}
	if err := proveBootstrapPrejoinHostFilesWith(context.Background(), root, expected, check); err != nil || checks < 20 {
		t.Fatal("actual native file proof missing", err, checks)
	}
	if err := installBootstrapPrejoinHostFilesWith(context.Background(), root, expected, check); err == nil {
		t.Fatal("fresh write replayed/adopted installed files")
	}
	if err := proveBootstrapPrejoinHostFilesWith(context.Background(), root, expected, check); err != nil {
		t.Fatal("rejected replay changed files", err)
	}
	files, _ := bootstrapPrejoinHostFiles(expected)
	for _, file := range files {
		entries, err := os.ReadDir(filepath.Join(root.Name(), filepath.Dir(file.path)))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if len(entry.Name()) >= len(".borealis-host-") && entry.Name()[:len(".borealis-host-")] == ".borealis-host-" {
				t.Fatal("owned staging file leaked")
			}
		}
	}
}

func TestBootstrapHostFilesRejectsForeignPathsBeforeEffects(t *testing.T) {
	for _, mode := range []string{"existing file", "existing link", "existing scratch", "unsafe parent", "linked parent", "non-directory parent", "lost authority", "cancel", "nil root", "nil check"} {
		t.Run(mode, func(t *testing.T) {
			base := t.TempDir()
			root, err := os.OpenRoot(base)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			check := func(context.Context) error { return nil }
			// Last destination rejects before earlier files or parents are made.
			last := filepath.Join(base, "etc/systemd/system/borealis-k3s-api-firewall.service")
			files, _ := bootstrapPrejoinHostFiles(bootstrapHostFilesFixture())
			pending := filepath.Join(base, filepath.Dir(files[0].path), files[0].pending)
			switch mode {
			case "existing scratch":
				if err := os.MkdirAll(filepath.Dir(pending), 0700); err != nil {
					t.Fatal(err)
				}
				err = os.WriteFile(pending, []byte("preserve unknown scratch"), 0600)
			case "existing file", "existing link":
				if err := os.MkdirAll(filepath.Dir(last), 0755); err != nil {
					t.Fatal(err)
				}
				if mode == "existing file" {
					err = os.WriteFile(last, []byte("preserve operator file"), 0600)
				} else {
					err = os.Symlink("/no/such/file", last)
				}
			case "unsafe parent":
				err = os.Mkdir(filepath.Join(base, "etc"), 0755)
				if err == nil {
					err = os.Chmod(filepath.Join(base, "etc"), 0777)
				}
			case "linked parent":
				err = os.Symlink(t.TempDir(), filepath.Join(base, "etc"))
			case "non-directory parent":
				err = os.WriteFile(filepath.Join(base, "etc"), []byte("preserve"), 0600)
			case "lost authority":
				check = func(context.Context) error { return errors.New("private diagnostic") }
			case "cancel":
				cancel()
			case "nil check":
				check = nil
			}
			if err != nil {
				t.Fatal(err)
			}
			input := root
			if mode == "nil root" {
				input = nil
			}
			if err := installBootstrapPrejoinHostFilesWith(ctx, input, bootstrapHostFilesFixture(), check); err != errBootstrapSource {
				t.Fatal("foreign path accepted or private error exposed", err)
			}
			if _, err := root.Lstat("usr"); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("preflight rejection created host files", err)
			}
			if mode == "existing file" {
				got, err := os.ReadFile(last)
				if err != nil || string(got) != "preserve operator file" {
					t.Fatal("operator file overwritten", err)
				}
			}
			if mode == "existing scratch" {
				got, err := os.ReadFile(pending)
				if err != nil || string(got) != "preserve unknown scratch" {
					t.Fatal("foreign scratch removed or overwritten", err)
				}
			}
		})
	}
}

func TestBootstrapHostFilesPartialEffectsRetained(t *testing.T) {
	for _, mode := range []string{"authority", "cancel", "late collision"} {
		t.Run(mode, func(t *testing.T) {
			root, err := os.OpenRoot(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			expected := bootstrapHostFilesFixture()
			files, _ := bootstrapPrejoinHostFiles(expected)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			changed := false
			check := func(context.Context) error {
				if _, err := root.Lstat(files[0].path); err != nil {
					return nil
				}
				if mode == "authority" {
					return errors.New("authority lost after first publication")
				}
				if mode == "cancel" {
					cancel()
					return nil
				}
				if !changed {
					changed = true
					return os.WriteFile(filepath.Join(root.Name(), files[1].path), []byte("foreign later target"), 0600)
				}
				return nil
			}
			if err := installBootstrapPrejoinHostFilesWith(ctx, root, expected, check); err != errBootstrapSource {
				t.Fatal("partial effect acknowledged", err)
			}
			got, err := os.ReadFile(filepath.Join(root.Name(), files[0].path))
			if err != nil || !bytes.Equal(got, files[0].data) {
				t.Fatal("published effect rolled back or lost", err)
			}
			if mode == "late collision" {
				got, err := os.ReadFile(filepath.Join(root.Name(), files[1].path))
				if err != nil || string(got) != "foreign later target" {
					t.Fatal("atomic no-replace violated", err)
				}
			}
			if err := proveBootstrapPrejoinHostFilesWith(context.Background(), root, expected, func(context.Context) error { return nil }); err == nil {
				t.Fatal("partial files became prepared host proof")
			}
		})
	}
}
