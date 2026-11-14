package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestBootstrapHostPackageExecutionBoundaries(t *testing.T) {
	for _, mode := range []string{"valid", "preflight", "already installed", "simulation failure", "unlocked solver", "drift before install", "install failure", "wrong result", "cancel after simulation", "authority after install"} {
		t.Run(mode, func(t *testing.T) {
			plan, before := bootstrapHostPackagePlanFixture(t, bootstrapHostInstallBase, true)
			preflight := mode == "preflight"
			if preflight {
				plan, _ = newBootstrapHostPackagePreflightPlan(plan.directory, before)
			}
			after := bootstrapHostPackageResultFixture(plan, before)
			if mode == "already installed" {
				before = after
				plan, _ = newBootstrapHostPackagePlan(plan.directory, bootstrapHostInstallBase, before)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var calls []string
			reads, installs := 0, 0
			lost := false
			check := func(context.Context) error {
				if lost {
					return errors.New("lost")
				}
				return nil
			}
			read := func(context.Context) ([]byte, error) {
				calls = append(calls, "read")
				reads++
				if reads == 3 {
					if mode == "wrong result" {
						return before, nil
					}
					return after, nil
				}
				if reads == 2 && mode == "drift before install" {
					return bytes.ReplaceAll(before, []byte("2.8.3"), []byte("2.8.4")), nil
				}
				return before, nil
			}
			run := func(_ context.Context, spec bootstrapHostCommandSpec, simulate bool) ([]byte, error) {
				if !slices.Contains(spec.args, "--no-download") || !slices.Contains(spec.args, "APT::Keep-Fds::=3") || !slices.Contains(spec.args, "APT::Keep-Fds::=4") || slices.Contains(spec.args, "--simulate") != simulate {
					t.Fatal("unfenced or online command selected")
				}
				if simulate {
					calls = append(calls, "simulate")
					if mode == "simulation failure" {
						return nil, errors.New("solver")
					}
					if mode == "cancel after simulation" {
						cancel()
					}
					raw := bootstrapHostPackageSimulationFixture(plan)
					if mode == "unlocked solver" {
						raw = append(raw, []byte("Remv apt [2.8.3]\n")...)
					}
					return raw, nil
				}
				calls = append(calls, "install")
				installs++
				if mode == "install failure" {
					return nil, errors.New("uncertain effect")
				}
				lost = mode == "authority after install"
				return nil, nil
			}
			err := prepareBootstrapHostPackagesWith(ctx, plan.directory, bootstrapHostInstallBase, preflight, check, read, run)
			valid := mode == "valid" || preflight || mode == "already installed"
			if (err == nil) != valid || installs > 1 {
				t.Fatal("wrong result or replay", calls, err)
			}
			wantInstall := mode == "valid" || mode == "install failure" || mode == "wrong result" || mode == "authority after install"
			if (installs == 1) != wantInstall {
				t.Fatal("failed preflight reached mutation", calls)
			}
			if mode == "valid" && !reflect.DeepEqual(calls, []string{"read", "simulate", "read", "install", "read"}) {
				t.Fatal("installed snapshot not checked around effect", calls)
			}
		})
	}
}

func TestBootstrapHostPackageCacheOwnsMetadata(t *testing.T) {
	for _, mode := range []string{"valid", "callback failure", "cancel", "foreign lock", "partial retained", "lock replaced"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			directory := t.TempDir()
			archive := filepath.Join(directory, "owned.deb")
			if os.WriteFile(archive, []byte("inert"), 0600) != nil {
				t.Fatal("fixture")
			}
			if mode == "foreign lock" {
				if os.WriteFile(filepath.Join(directory, "lock"), []byte("foreign"), 0600) != nil {
					t.Fatal("fixture")
				}
			}
			called := false
			err := withBootstrapAPTCacheMetadata(ctx, directory, func(ctx context.Context) error { return ctx.Err() }, func(ctx context.Context, got string) error {
				called = true
				if got != directory {
					t.Fatal("directory substituted")
				}
				switch mode {
				case "callback failure":
					return errors.New("failure")
				case "cancel":
					cancel()
				case "partial retained":
					return os.WriteFile(filepath.Join(got, "partial", "unknown"), []byte("keep"), 0600)
				case "lock replaced":
					if os.Rename(filepath.Join(got, "lock"), filepath.Join(got, "old-lock")) != nil {
						t.Fatal("fixture")
					}
					return os.WriteFile(filepath.Join(got, "lock"), []byte("keep"), 0600)
				}
				return nil
			})
			if (err == nil) != (mode == "valid") || called != (mode != "foreign lock") {
				t.Fatal("wrong cache ownership outcome", err)
			}
			if raw, err := os.ReadFile(archive); err != nil || string(raw) != "inert" {
				t.Fatal("package input changed")
			}
			if mode == "partial retained" {
				if raw, err := os.ReadFile(filepath.Join(directory, "partial", "unknown")); err != nil || string(raw) != "keep" {
					t.Fatal("unknown cache child removed")
				}
			} else if mode == "lock replaced" || mode == "foreign lock" {
				if raw, err := os.ReadFile(filepath.Join(directory, "lock")); err != nil || len(raw) == 0 {
					t.Fatal("foreign cache lock removed")
				}
			} else if entries, err := os.ReadDir(directory); err != nil || len(entries) != 1 {
				t.Fatal("owned cache metadata escaped", err)
			}
		})
	}
}
