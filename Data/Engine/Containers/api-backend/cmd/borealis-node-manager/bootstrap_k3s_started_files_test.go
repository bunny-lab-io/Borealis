package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBootstrapStartedK3sConfiguration(t *testing.T) {
	for _, mode := range []string{"absent", "generated", "symlink", "hardlink", "public", "empty", "oversized", "foreign config", "changed token", "changed baseline", "changed join", "scratch"} {
		t.Run(mode, func(t *testing.T) {
			root, expected, source, material := bootstrapJoinFilesFixture(t)
			check := func(context.Context) error { return nil }
			ctx := context.Background()
			err := withBootstrapK3sJoinFiles(ctx, expected, source, material, check, func(plan bootstrapK3sJoinFiles) error {
				if installBootstrapK3sJoinFiles(ctx, root, expected, plan, check) != nil {
					t.Fatal("fixture publication")
				}
				path := filepath.Join(root.Name(), "etc/rancher/k3s/k3s.yaml")
				write := func(path string, raw []byte) {
					t.Helper()
					if err := os.WriteFile(path, raw, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if mode != "absent" {
					write(path, []byte("private generated kubeconfig fixture"))
				}
				switch mode {
				case "symlink":
					if err := os.Rename(path, path+"-target"); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(path+"-target", path); err != nil {
						t.Fatal(err)
					}
				case "hardlink":
					if err := os.Link(path, filepath.Join(root.Name(), "linked-config")); err != nil {
						t.Fatal(err)
					}
				case "public":
					if err := os.Chmod(path, 0644); err != nil {
						t.Fatal(err)
					}
				case "empty":
					write(path, nil)
				case "oversized":
					write(path, make([]byte, (64<<10)+1))
				case "foreign config":
					write(filepath.Join(root.Name(), "etc/rancher/k3s/config.yaml"), []byte("foreign"))
				case "changed token":
					write(filepath.Join(root.Name(), plan.files[0].path), []byte("foreign"))
				case "changed baseline":
					write(filepath.Join(root.Name(), "etc/rancher/k3s/config.yaml.d/10-borealis.yaml"), []byte("foreign"))
				case "changed join":
					write(filepath.Join(root.Name(), plan.files[1].path), []byte("foreign"))
				case "scratch":
					write(filepath.Join(root.Name(), filepath.Dir(plan.files[0].path), plan.files[0].pending), plan.files[0].data)
				}
				got := verifyBootstrapK3sJoinFilePhase(root, expected, plan, true, false, true)
				if mode == "absent" || mode == "generated" || mode == "empty" {
					if got != nil {
						t.Fatal("startup configuration rejected", got)
					}
				} else if got == nil {
					t.Fatal("unsafe configuration admitted")
				}
				if (verifyBootstrapK3sGeneratedConfig(root, true) == nil) != (mode == "generated" || mode == "foreign config" || mode == "changed token" || mode == "changed baseline" || mode == "changed join" || mode == "scratch") {
					t.Fatal("generated output completion proof admitted missing, empty or unsafe file")
				}
				if mode == "generated" && verifyBootstrapK3sJoinFilePrefix(root, expected, plan, true, false) == nil {
					t.Fatal("old phase accepted runtime-generated file")
				}
				if verifyBootstrapK3sJoinFilePhase(root, expected, plan, false, false, true) == nil || verifyBootstrapK3sJoinFilePhase(root, expected, plan, false, true, true) == nil {
					t.Fatal("startup admitted incomplete configuration")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
