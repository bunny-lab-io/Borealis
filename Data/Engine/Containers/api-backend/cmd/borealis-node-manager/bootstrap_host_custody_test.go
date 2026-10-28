package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func bootstrapHostCustodyFixture(t *testing.T) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	if err := os.MkdirAll(filepath.Join(root.Name(), "opt/Borealis"), 0755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestBootstrapPreparationProgressPaths(t *testing.T) {
	expected := bootstrapHostFilesFixture()
	for _, mode := range []string{"empty", "source", "prepared", "runtime", "foreign K3s", "symlink", "unsafe parent"} {
		t.Run(mode, func(t *testing.T) {
			root := bootstrapHostCustodyFixture(t)
			switch mode {
			case "empty":
				if err := root.Remove("opt/Borealis"); err != nil {
					t.Fatal(err)
				}
			case "prepared":
				if err := installBootstrapPrejoinHostFilesWith(context.Background(), root, expected, func(context.Context) error { return nil }); err != nil {
					t.Fatal(err)
				}
			case "runtime":
				if err := os.MkdirAll(filepath.Join(root.Name(), "var/lib/rancher/k3s"), 0700); err != nil {
					t.Fatal(err)
				}
			case "foreign K3s":
				if err := os.MkdirAll(filepath.Join(root.Name(), "etc/rancher/k3s"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root.Name(), "etc/rancher/k3s/token"), []byte("foreign"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := root.Remove("opt/Borealis"); err != nil {
					t.Fatal(err)
				}
				if err := root.Symlink(t.TempDir(), "opt/Borealis"); err != nil {
					t.Fatal(err)
				}
			case "unsafe parent":
				if err := root.Chmod("opt", 0777); err != nil {
					t.Fatal(err)
				}
			}
			err := verifyBootstrapProgressPaths(root, expected)
			valid := mode == "empty" || mode == "source" || mode == "prepared"
			if (err == nil) != valid {
				t.Fatal("incorrect progress custody", err)
			}
		})
	}
}

func TestBootstrapHostCustodyActualPublication(t *testing.T) {
	root := bootstrapHostCustodyFixture(t)
	expected := bootstrapHostFilesFixture()
	if verifyBootstrapHostPaths(root, expected, "prepare_prejoin_host", true) != nil {
		t.Fatal("clean host rejected")
	}
	checks := 0
	check := func(context.Context) error {
		checks++
		return verifyBootstrapHostPaths(root, expected, "prepare_prejoin_host", false)
	}
	if err := installBootstrapPrejoinHostFilesWith(context.Background(), root, expected, check); err != nil {
		t.Fatal("native writer cannot preserve host custody", err)
	}
	if checks == 0 || verifyBootstrapHostPaths(root, expected, "reconcile_prejoin_host", true) != nil {
		t.Fatal("complete prefix rejected")
	}
	if verifyBootstrapHostPaths(root, expected, "prepare_prejoin_host", true) == nil {
		t.Fatal("fresh request adopted prior host files")
	}
	for _, action := range []string{"stage_source", "reconcile_source", "prepare_runtime_identity", "reconcile_runtime_identity", "prepare_prejoin_identity_root", "reconcile_prejoin_identity_root", "install_prejoin_identity", "reconcile_prejoin_identity"} {
		if verifyBootstrapSourcePaths(root, action, false) == nil {
			t.Fatal("earlier phase accepted host files", action)
		}
	}
}

func TestBootstrapHostCustodyOrderedPrefixes(t *testing.T) {
	expected := bootstrapHostFilesFixture()
	files, _ := bootstrapPrejoinHostFiles(expected)
	for count := 0; count <= len(files); count++ {
		for _, scratch := range []bool{false, true} {
			if count == len(files) && scratch {
				continue
			}
			t.Run(string(rune('0'+count))+map[bool]string{false: "/final", true: "/scratch"}[scratch], func(t *testing.T) {
				root := bootstrapHostCustodyFixture(t)
				for _, file := range files[:count] {
					if bootstrapHostParents(context.Background(), root, filepath.Dir(file.path), func() error { return nil }, true) != nil {
						t.Fatal("parents")
					}
					if err := os.WriteFile(filepath.Join(root.Name(), file.path), file.data, file.mode); err != nil {
						t.Fatal(err)
					}
				}
				if scratch {
					file := files[count]
					if bootstrapHostParents(context.Background(), root, filepath.Dir(file.path), func() error { return nil }, true) != nil {
						t.Fatal("parents")
					}
					if err := os.WriteFile(filepath.Join(root.Name(), filepath.Dir(file.path), file.pending), file.data, file.mode); err != nil {
						t.Fatal(err)
					}
				}
				if verifyBootstrapHostPaths(root, expected, "prepare_prejoin_host", false) != nil {
					t.Fatal("valid ordered prefix rejected")
				}
				if (verifyBootstrapHostPaths(root, expected, "reconcile_prejoin_host", false) == nil) != (count == len(files)) {
					t.Fatal("partial prefix reconciled")
				}
			})
		}
	}
}

func TestBootstrapHostCustodyRejectsForeignState(t *testing.T) {
	for _, mode := range []string{"extra file", "extra directory", "wrong mode", "wrong content", "out of order", "foreign scratch", "partial scratch", "double scratch", "final and scratch", "symlink", "hardlink", "runtime", "missing source", "unknown action", "invalid expected"} {
		t.Run(mode, func(t *testing.T) {
			root := bootstrapHostCustodyFixture(t)
			expected := bootstrapHostFilesFixture()
			files, _ := bootstrapPrejoinHostFiles(expected)
			first := files[0]
			if bootstrapHostParents(context.Background(), root, filepath.Dir(first.path), func() error { return nil }, true) != nil {
				t.Fatal("parents")
			}
			write := func(path string, raw []byte, mode os.FileMode) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root.Name(), path), raw, mode); err != nil {
					t.Fatal(err)
				}
			}
			action := "prepare_prejoin_host"
			scratch := filepath.Join(filepath.Dir(first.path), first.pending)
			switch mode {
			case "extra file":
				write("etc/rancher/k3s/token", []byte("foreign"), 0600)
			case "extra directory":
				if root.Mkdir("etc/rancher/k3s/server", 0700) != nil {
					t.Fatal("mkdir")
				}
			case "wrong mode":
				if root.Chmod("etc/rancher/k3s", 0755) != nil {
					t.Fatal("chmod")
				}
			case "wrong content":
				write(first.path, []byte("foreign"), first.mode)
			case "out of order":
				write(files[1].path, files[1].data, files[1].mode)
			case "foreign scratch":
				write(filepath.Join(filepath.Dir(first.path), ".borealis-host-foreign"), first.data, first.mode)
			case "partial scratch":
				write(scratch, first.data[:5], first.mode)
			case "double scratch":
				write(scratch, first.data, first.mode)
				write(filepath.Join(filepath.Dir(files[1].path), files[1].pending), files[1].data, files[1].mode)
			case "final and scratch":
				write(first.path, first.data, first.mode)
				write(scratch, first.data, first.mode)
			case "symlink":
				if os.Symlink("../registries.yaml", filepath.Join(root.Name(), first.path)) != nil {
					t.Fatal("symlink")
				}
			case "hardlink":
				write(first.path, first.data, first.mode)
				if os.Link(filepath.Join(root.Name(), first.path), filepath.Join(root.Name(), "retained-link")) != nil {
					t.Fatal("link")
				}
			case "runtime":
				if os.MkdirAll(filepath.Join(root.Name(), "var/lib/rancher/k3s"), 0755) != nil {
					t.Fatal("mkdir")
				}
			case "missing source":
				if root.Remove("opt/Borealis") != nil {
					t.Fatal("remove")
				}
			case "unknown action":
				action = "join_cluster"
			case "invalid expected":
				expected.Target.Generation = 0
			}
			if verifyBootstrapHostPaths(root, expected, action, false) == nil {
				t.Fatal("foreign or unsafe host state accepted")
			}
		})
	}
}
