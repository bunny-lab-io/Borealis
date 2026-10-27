package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bootstrapJoinFilesFixture(t *testing.T) (*os.Root, clusterbootstrap.PreparationExpected, engineidentity.Binding, *clusterbootstrap.K3sJoinMaterial) {
	t.Helper()
	sourceRoot, token, source := sourceJoinFilesFixture(t)
	ca, err := sourceRoot.ReadFile("var/lib/rancher/k3s/server/tls/server-ca.crt")
	if err != nil {
		t.Fatal(err)
	}
	expected := bootstrapHostFilesFixture()
	material, err := clusterbootstrap.NewK3sJoinMaterial(expected, source, bytes.TrimSuffix(token, []byte{'\n'}), ca)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { material.Close() })
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	if installBootstrapPrejoinHostFilesWith(context.Background(), root, expected, func(context.Context) error { return nil }) != nil {
		t.Fatal("baseline fixture")
	}
	return root, expected, source, material
}

func TestBootstrapK3sJoinFilesPublicationAndScope(t *testing.T) {
	root, expected, source, material := bootstrapJoinFilesFixture(t)
	check := func(context.Context) error { return nil }
	var borrowed []byte
	err := withBootstrapK3sJoinFiles(context.Background(), expected, source, material, check, func(plan bootstrapK3sJoinFiles) error {
		borrowed = plan.files[0].data
		for _, format := range []string{"%v", "%+v", "%#v"} {
			if got := fmt.Sprintf(format, plan); strings.Contains(got, "fixture secret") || !strings.Contains(got, "redacted") {
				t.Fatal("private plan formatting leaked")
			}
		}
		if _, err := json.Marshal(plan); err == nil {
			t.Fatal("private plan serialized")
		}
		if verifyBootstrapK3sJoinObservedPrefix(root, expected, true) != nil || verifyBootstrapK3sJoinFilePrefix(root, expected, plan, false, true) != nil || installBootstrapK3sJoinFiles(context.Background(), root, expected, plan, check) != nil || verifyBootstrapK3sJoinFilePrefix(root, expected, plan, true, false) != nil {
			t.Fatal("fixed publication failed")
		}
		if verifyBootstrapK3sJoinObservedPrefix(root, expected, false) != nil || verifyBootstrapK3sJoinObservedPrefix(root, expected, true) == nil {
			t.Fatal("cursor/fresh isolation lost")
		}
		info, _ := root.Lstat(plan.files[0].path)
		if installBootstrapK3sJoinFiles(context.Background(), root, expected, plan, check) == nil {
			t.Fatal("write replay accepted")
		}
		after, _ := root.Lstat(plan.files[0].path)
		if !os.SameFile(info, after) {
			t.Fatal("existing token replaced")
		}
		if proveBootstrapPrejoinHostFilesWith(context.Background(), root, expected, check) == nil {
			t.Fatal("old host proof accepted later join state")
		}
		return nil
	})
	if err != nil || !bytes.Equal(borrowed, make([]byte, len(borrowed))) {
		t.Fatal("private scope retained bytes", err)
	}
	configPath := filepath.Join(root.Name(), strings.TrimPrefix(clusterbootstrap.PrejoinK3sJoinConfigPath, "/"))
	if persistJoinedK3sRuntimeLabels(configPath, "active") != nil {
		t.Fatal("existing later activation parser rejected fixed join config")
	}
	config, _ := os.ReadFile(configPath)
	if !strings.Contains(string(config), "borealis.io/application-state=active") {
		t.Fatal("activation compatibility lost")
	}
}

func TestBootstrapK3sJoinFilesRejectUnknownPrefixes(t *testing.T) {
	for _, mode := range []string{"token only", "token scratch", "config scratch", "config only", "truncated scratch", "foreign file", "changed baseline", "wrong mode", "symlink", "hardlink", "cancel after token", "changed plan"} {
		t.Run(mode, func(t *testing.T) {
			root, expected, source, material := bootstrapJoinFilesFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			check := func(context.Context) error { return nil }
			err := withBootstrapK3sJoinFiles(ctx, expected, source, material, check, func(plan bootstrapK3sJoinFiles) error {
				write := func(file bootstrapHostFile, path string) {
					t.Helper()
					if root.WriteFile(path, file.data, file.mode) != nil {
						t.Fatal("fixture write")
					}
				}
				token, config := plan.files[0], plan.files[1]
				switch mode {
				case "token only":
					write(token, token.path)
				case "token scratch":
					write(token, filepath.Join(filepath.Dir(token.path), token.pending))
				case "config scratch":
					write(token, token.path)
					write(config, filepath.Join(filepath.Dir(config.path), config.pending))
				case "config only":
					write(config, config.path)
				case "truncated scratch":
					token.data = token.data[:1]
					write(token, filepath.Join(filepath.Dir(token.path), token.pending))
				case "foreign file":
					write(token, "etc/rancher/k3s/foreign.token")
				case "changed baseline":
					if root.WriteFile("etc/rancher/k3s/config.yaml.d/10-borealis.yaml", []byte("changed"), 0644) != nil {
						t.Fatal("baseline mutation")
					}
				case "wrong mode":
					token.mode = 0644
					write(token, token.path)
				case "symlink":
					write(token, "elsewhere")
					if root.Symlink("../../../elsewhere", token.path) != nil {
						t.Fatal("symlink fixture")
					}
				case "hardlink":
					write(token, token.path)
					if root.Link(token.path, "other-token") != nil {
						t.Fatal("hardlink fixture")
					}
				case "changed plan":
					plan.files[0].data[0] = 'X'
					return nil
				case "cancel after token":
					boundary := func(context.Context) error {
						if _, err := root.Lstat(token.path); err == nil {
							cancel()
						}
						return nil
					}
					if installBootstrapK3sJoinFiles(ctx, root, expected, plan, boundary) == nil {
						t.Fatal("cancelled publication accepted")
					}
					if _, err := root.Lstat(token.path); err != nil {
						t.Fatal("published token silently removed")
					}
					if _, err := root.Lstat(config.path); !os.IsNotExist(err) {
						t.Fatal("configuration published after cancellation")
					}
					return nil
				}
				prefix := mode == "token only" || mode == "token scratch" || mode == "config scratch"
				// Pre-stream observation bounds metadata, not private token authenticity.
				if (verifyBootstrapK3sJoinObservedPrefix(root, expected, false) == nil) != (prefix || mode == "truncated scratch") {
					t.Fatal("wrong cursor observation")
				}
				if (verifyBootstrapK3sJoinFilePrefix(root, expected, plan, false, false) == nil) != prefix {
					t.Fatal("wrong partial proof")
				}
				if verifyBootstrapK3sJoinFilePrefix(root, expected, plan, true, false) == nil || installBootstrapK3sJoinFiles(ctx, root, expected, plan, check) == nil {
					t.Fatal("partial/foreign state adopted")
				}
				return nil
			})
			if (err != nil) != (mode == "cancel after token" || mode == "changed plan") {
				t.Fatal("wrong scoped outcome", err)
			}
		})
	}
}
