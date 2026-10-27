package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bootstrapK3sFilesFixture(t *testing.T) (*os.Root, []bootstrapHostFile) {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	files, err := bootstrapPrejoinK3sFiles(bootstrapHostFilesFixture())
	if err != nil {
		t.Fatal(err)
	}
	if err := bootstrapHostParents(context.Background(), root, "etc/systemd/system", func() error { return nil }, true); err != nil {
		t.Fatal(err)
	}
	return root, files
}

func TestBootstrapK3sFilesNativePublication(t *testing.T) {
	root, files := bootstrapK3sFilesFixture(t)
	expected := bootstrapHostFilesFixture()
	checks := 0
	check := func(context.Context) error { checks++; return nil }
	if err := installBootstrapPrejoinK3sFilesWith(context.Background(), root, expected, check); err != nil {
		t.Fatal(err)
	}
	if checks < 10 || verifyBootstrapK3sFilePrefix(root, expected, true, false) != nil {
		t.Fatal("missing full proof")
	}
	before, _ := root.Lstat(files[1].path)
	if installBootstrapPrejoinK3sFilesWith(context.Background(), root, expected, check) == nil {
		t.Fatal("replayed fresh publication")
	}
	after, _ := root.Lstat(files[1].path)
	if !os.SameFile(before, after) {
		t.Fatal("replaced existing service")
	}
	for _, path := range []string{"etc/systemd/system/multi-user.target.wants", "var/lib/rancher/k3s/server"} {
		if _, err := root.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("enabled/started runtime")
		}
	}
	for _, forbidden := range []string{"/etc/default", "/etc/sysconfig", "K3S_TOKEN", "curl", "wget", "ExecStartPost", "--cluster-init"} {
		if strings.Contains(string(files[1].data), forbidden) {
			t.Fatal("ambient/unbounded service contract", forbidden)
		}
	}
	t.Setenv("K3S_URL", "https://foreign.invalid")
	expected.Target.Generation++
	expected.Target.OperationAttempt++
	again, err := bootstrapPrejoinK3sFiles(expected)
	if err != nil || again[1].pending != files[1].pending || string(again[1].data) != string(files[1].data) {
		t.Fatal("takeover/environment changed fixed plan", err)
	}
	expected.Source.SourceSHA = strings.Repeat("f", 40)
	foreign, err := bootstrapPrejoinK3sFiles(expected)
	if err != nil || foreign[1].pending == files[1].pending {
		t.Fatal("source not bound", err)
	}
}

func TestBootstrapK3sFilesRejectForeignAndInterruptedState(t *testing.T) {
	for _, mode := range []string{"unit only", "partial scratch", "unknown scratch", "drop-in", "symlink", "hardlink", "wrong mode", "changed bytes", "lost authority", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			root, files := bootstrapK3sFilesFixture(t)
			expected := bootstrapHostFilesFixture()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			check := func(context.Context) error { return nil }
			put := func(path string, data []byte, mode os.FileMode) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root.Name(), path), data, mode); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "unit only":
				put(files[1].path, files[1].data, files[1].mode)
			case "partial scratch":
				put(filepath.Join(filepath.Dir(files[1].path), files[1].pending), files[1].data[:9], 0600)
			case "unknown scratch":
				put("etc/systemd/system/.borealis-k3s-foreign", nil, 0600)
			case "drop-in":
				if err := root.Mkdir("etc/systemd/system/k3s.service.d", 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := root.Symlink("/dev/null", files[0].path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				put(files[0].path, nil, 0600)
				if err := root.Link(files[0].path, "etc/systemd/system/foreign"); err != nil {
					t.Fatal(err)
				}
			case "wrong mode":
				put(files[0].path, nil, 0644)
			case "changed bytes":
				put(files[0].path, []byte("K3S_URL=foreign\n"), 0600)
			case "lost authority":
				check = func(context.Context) error { return errBootstrapSource }
			case "cancel":
				cancel()
			}
			if installBootstrapPrejoinK3sFilesWith(ctx, root, expected, check) == nil {
				t.Fatal("unsafe state admitted")
			}
			if mode != "lost authority" && mode != "cancel" && verifyBootstrapK3sFilePrefix(root, expected, false, false) == nil {
				t.Fatal("unsafe prefix recognized")
			}
		})
	}
}

func TestBootstrapK3sFilesExactPendingPrefix(t *testing.T) {
	root, files := bootstrapK3sFilesFixture(t)
	expected := bootstrapHostFilesFixture()
	if err := publishBootstrapHostFile(root, files[0], func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	pending := filepath.Join(filepath.Dir(files[1].path), files[1].pending)
	if err := os.WriteFile(filepath.Join(root.Name(), pending), files[1].data, files[1].mode); err != nil {
		t.Fatal(err)
	}
	if verifyBootstrapK3sFilePrefix(root, expected, false, false) != nil {
		t.Fatal("exact interrupted prefix rejected")
	}
	if verifyBootstrapK3sFilePrefix(root, expected, true, false) == nil {
		t.Fatal("pending mistaken for completed service")
	}
	if installBootstrapPrejoinK3sFilesWith(context.Background(), root, expected, func(context.Context) error { return nil }) == nil {
		t.Fatal("interrupted publication replayed")
	}
	if _, err := root.Lstat(pending); err != nil {
		t.Fatal("retained pending removed", err)
	}
}
