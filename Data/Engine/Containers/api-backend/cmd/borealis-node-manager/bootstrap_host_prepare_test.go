package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBootstrapNativeHostOrderingAndFailure(t *testing.T) {
	want := []string{"preflight", "command1", "command2", "base", "repository", "command1", "command3", "files",
		"command4", "multipath", "command7", "command8", "command9", "firewall", "command10", "prove"}
	for failAt := -1; failAt < len(want); failAt++ {
		for _, failure := range []string{"error", "cancel", "authority"} {
			t.Run(fmt.Sprintf("%d/%s", failAt, failure), func(t *testing.T) {
				host, identity := bootstrapHostCommandLocks(t)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var got []string
				lost := false
				check := func(context.Context) error {
					if lost {
						return errors.New("private authority")
					}
					return nil
				}
				call := func(name string) func(context.Context) error {
					return func(context.Context) error {
						got = append(got, name)
						if len(got)-1 == failAt {
							switch failure {
							case "error":
								return errors.New("private command diagnostics")
							case "cancel":
								cancel()
							case "authority":
								lost = true
							}
						}
						return nil
					}
				}
				o := bootstrapNativeHostOperations{preflight: call("preflight"), base: call("base"), repository: call("repository"),
					files: call("files"), multipath: call("multipath"), firewall: call("firewall"), prove: call("prove"),
					command: func(ctx context.Context, step bootstrapHostCommand, h, i *os.File) error {
						if h != host || i != identity {
							t.Fatal("borrowed locks replaced")
						}
						return call(fmt.Sprintf("command%d", step))(ctx)
					},
				}
				err := prepareBootstrapNativeHostWith(ctx, bootstrapHostFilesFixture(), host, identity, check, o)
				prefix := want
				if failAt >= 0 {
					prefix = want[:failAt+1]
				}
				if !reflect.DeepEqual(got, prefix) || (err == nil) != (failAt < 0) || err != nil && err != errBootstrapSource {
					t.Fatal("wrong order, replay, missing proof or leaked diagnostics", got, err)
				}
				if _, err := host.Stat(); err != nil {
					t.Fatal("borrowed host lock closed")
				}
				if _, err := identity.Stat(); err != nil {
					t.Fatal("borrowed identity lock closed")
				}
			})
		}
	}
}

func TestBootstrapNativeHostRejectsBeforeEffects(t *testing.T) {
	for _, mode := range []string{"expected", "host", "identity", "closed", "check", "lost", "cancel", "preflight", "base", "repository", "files", "multipath", "firewall", "prove", "command"} {
		t.Run(mode, func(t *testing.T) {
			host, identity := bootstrapHostCommandLocks(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			call := func(context.Context) error { calls++; return nil }
			check := func(context.Context) error { return nil }
			o := bootstrapNativeHostOperations{call, call, call, call, call, call, call,
				func(context.Context, bootstrapHostCommand, *os.File, *os.File) error { calls++; return nil }}
			expected := bootstrapHostFilesFixture()
			switch mode {
			case "expected":
				expected.Target.Generation = 0
			case "host":
				host = identity
			case "identity":
				identity = host
			case "closed":
				host.Close()
			case "check":
				check = nil
			case "lost":
				check = func(context.Context) error { return errors.New("lost") }
			case "cancel":
				cancel()
			case "preflight":
				o.preflight = nil
			case "base":
				o.base = nil
			case "repository":
				o.repository = nil
			case "files":
				o.files = nil
			case "multipath":
				o.multipath = nil
			case "firewall":
				o.firewall = nil
			case "prove":
				o.prove = nil
			case "command":
				o.command = nil
			}
			if prepareBootstrapNativeHostWith(ctx, expected, host, identity, check, o) != errBootstrapSource || calls != 0 {
				t.Fatal("invalid request reached effects")
			}
		})
	}
}

func TestBootstrapNativeHostFilePreflight(t *testing.T) {
	for _, mode := range []string{"clean", "final", "scratch", "ancestor", "cancel", "authority"} {
		t.Run(mode, func(t *testing.T) {
			root, _ := bootstrapDockerFixture(t)
			expected := bootstrapHostFilesFixture()
			files, _ := bootstrapPrejoinHostFiles(expected)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			check := func(context.Context) error { return nil }
			switch mode {
			case "final":
				bootstrapDockerWrite(t, root, files[3].path, string(files[3].data))
			case "scratch":
				bootstrapDockerWrite(t, root, filepath.Join(filepath.Dir(files[3].path), files[3].pending), "")
			case "ancestor":
				if err := os.Symlink("apt", filepath.Join(root.Name(), "etc/rancher")); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				cancel()
			case "authority":
				check = func(context.Context) error { return errors.New("lost") }
			}
			if (preflightBootstrapHostFiles(ctx, root, expected, check) == nil) != (mode == "clean") {
				t.Fatal("unsafe file preflight accepted")
			}
			if _, err := root.Lstat(files[0].path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("read-only preflight wrote files")
			}
		})
	}
}

func TestBootstrapNativeHostBaseInventory(t *testing.T) {
	root, read := bootstrapDockerFixture(t)
	_ = root
	base, _ := read(context.Background(), "packages")
	raw := string(base)
	for _, name := range bootstrapHostPackages[:7] {
		raw += name + "\tamd64\tinstall ok installed\t1.2\n"
	}
	for _, mode := range []string{"valid", "missing", "foreign", "residual", "pending", "docker", "duplicate"} {
		value := raw
		switch mode {
		case "missing":
			value = strings.Replace(value, "curl\tamd64\tinstall ok installed\t1.2\n", "", 1)
		case "foreign":
			value = strings.Replace(value, "curl\tamd64", "curl\tarm64", 1)
		case "residual":
			value = strings.Replace(value, "curl\tamd64\tinstall ok installed", "curl\tamd64\tdeinstall ok config-files", 1)
		case "pending":
			value = strings.Replace(value, "install ok installed", "install ok unpacked", 1)
		case "docker":
			value += "docker-ce\tamd64\tinstall ok installed\t1.2\n"
		case "duplicate":
			value += "curl\tamd64\tinstall ok installed\t1.2\n"
		}
		if (validateBootstrapHostBase(value) == nil) != (mode == "valid") {
			t.Fatal(mode)
		}
	}
}

func TestBootstrapRepositoryHostOrderingAndFailure(t *testing.T) {
	want := []string{"preflight", "firewall", "repository", "command1", "command3", "files",
		"command4", "multipath", "command7", "command8", "command9", "firewall", "command10", "prove"}
	for failAt := -1; failAt < len(want); failAt++ {
		for _, failure := range []string{"error", "cancel", "authority"} {
			t.Run(fmt.Sprintf("%d/%s", failAt, failure), func(t *testing.T) {
				host, identity := bootstrapHostCommandLocks(t)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var got []string
				lost := false
				check := func(context.Context) error {
					if lost {
						return errors.New("private authority")
					}
					return nil
				}
				call := func(name string) func(context.Context) error {
					return func(context.Context) error {
						got = append(got, name)
						if len(got)-1 == failAt {
							switch failure {
							case "error":
								return errors.New("private command diagnostics")
							case "cancel":
								cancel()
							case "authority":
								lost = true
							}
						}
						return nil
					}
				}
				o := bootstrapNativeHostOperations{preflight: call("preflight"), base: call("base"), repository: call("repository"),
					files: call("files"), multipath: call("multipath"), firewall: call("firewall"), prove: call("prove"),
					command: func(ctx context.Context, step bootstrapHostCommand, h, i *os.File) error {
						if h != host || i != identity {
							t.Fatal("borrowed locks replaced")
						}
						return call(fmt.Sprintf("command%d", step))(ctx)
					},
				}
				err := prepareBootstrapNativeHostPhaseWith(ctx, bootstrapHostFilesFixture(), host, identity, check, true, o)
				prefix := want
				if failAt >= 0 {
					prefix = want[:failAt+1]
				}
				if !reflect.DeepEqual(got, prefix) || (err == nil) != (failAt < 0) || err != nil && err != errBootstrapSource {
					t.Fatal("wrong order, replay, missing proof or leaked diagnostics", got, err)
				}
				if _, err := host.Stat(); err != nil {
					t.Fatal("borrowed host lock closed")
				}
				if _, err := identity.Stat(); err != nil {
					t.Fatal("borrowed identity lock closed")
				}
			})
		}
	}
}
