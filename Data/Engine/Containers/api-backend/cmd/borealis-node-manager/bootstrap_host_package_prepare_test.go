package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestBootstrapHostPackageApprovedInventoryBeforeEffects(t *testing.T) {
	for _, preflight := range []bool{false, true} {
		for _, mode := range []string{"approved", "missing", "inventory drift", "selection drift", "reordered during simulation"} {
			t.Run(fmt.Sprintf("preflight=%t/%s", preflight, mode), func(t *testing.T) {
				plan, before := bootstrapHostPackagePlanFixture(t, bootstrapHostInstallBase, true)
				if preflight {
					plan, _ = newBootstrapHostPackagePreflightPlan("noble", plan.directory, before)
				}
				selection, err := clusterbootstrap.SelectHostPackages(before)
				if err != nil {
					t.Fatal(err)
				}
				approved := &selection
				switch mode {
				case "missing":
					approved = nil
				case "inventory drift":
					selection.InventorySHA256 = strings.Repeat("b", 64)
				case "selection drift":
					selection.Missing = selection.Missing[1:]
				}
				reads, simulations, installs := 0, 0, 0
				read := func(context.Context) ([]byte, error) {
					reads++
					if reads == 2 && mode == "reordered during simulation" {
						lines := strings.Split(strings.TrimSuffix(string(before), "\n"), "\n")
						slices.Reverse(lines)
						return []byte(strings.Join(lines, "\n") + "\n"), nil
					}
					if reads == 3 {
						return bootstrapHostPackageResultFixture(plan, before), nil
					}
					return before, nil
				}
				run := func(_ context.Context, _ bootstrapHostCommandSpec, simulate bool) ([]byte, error) {
					if simulate {
						simulations++
						return bootstrapHostPackageSimulationFixture(plan), nil
					}
					installs++
					return nil, nil
				}
				err = prepareBootstrapHostPackagesWith(context.Background(), plan.directory, bootstrapHostInstallBase, preflight, "noble",
					func(ctx context.Context) error { return ctx.Err() },
					func(raw []byte) error { return validateBootstrapTargetPackages(approved, raw) }, read, run)
				if (err == nil) != (mode == "approved") || (installs == 1) != (mode == "approved" && !preflight) {
					t.Fatal("unapproved inventory reached installation", err, installs)
				}
				if (simulations == 1) != (mode == "approved" || mode == "reordered during simulation") {
					t.Fatal("initial approval was not checked before simulation")
				}
			})
		}
	}
}

func TestBootstrapHostPackageApprovedFirstEffectAndContinuation(t *testing.T) {
	for _, mode := range []string{"base then Docker", "Docker first", "drift before Docker first"} {
		t.Run(mode, func(t *testing.T) {
			base, current := bootstrapHostPackagePlanFixture(t, bootstrapHostInstallBase, false)
			baseMissing := mode == "base then Docker"
			if !baseMissing {
				current = bootstrapHostPackageResultFixture(base, current)
			}
			approved, err := clusterbootstrap.SelectHostPackages(current)
			if err != nil {
				t.Fatal(err)
			}
			installs := 0
			for _, phase := range []bootstrapHostCommand{0, bootstrapHostInstallBase, bootstrapHostInstallDocker} {
				if phase == bootstrapHostInstallDocker && mode == "drift before Docker first" {
					current = bytes.ReplaceAll(current, []byte("2.8.3"), []byte("2.8.4"))
				}
				var plan *bootstrapHostPackagePlan
				if phase == 0 {
					plan, err = newBootstrapHostPackagePreflightPlan("noble", base.directory, current)
				} else {
					plan, err = newBootstrapHostPackagePlan("noble", base.directory, phase, current)
				}
				if err != nil {
					t.Fatal(err)
				}
				before := bytes.Clone(current)
				approval := bootstrapTargetPackageInventoryCheck(&approved, phase)
				if (approval == nil) != (phase == bootstrapHostInstallDocker && baseMissing) {
					t.Fatal("original inventory checked after owned effects, or first effect unchecked")
				}
				err = prepareBootstrapHostPackagesWith(context.Background(), base.directory, phase, phase == 0, "noble",
					func(ctx context.Context) error { return ctx.Err() }, approval,
					func(context.Context) ([]byte, error) { return current, nil },
					func(_ context.Context, _ bootstrapHostCommandSpec, simulate bool) ([]byte, error) {
						if simulate {
							return bootstrapHostPackageSimulationFixture(plan), nil
						}
						installs++
						current = bootstrapHostPackageResultFixture(plan, before)
						return nil, nil
					})
				if (err != nil) != (phase == bootstrapHostInstallDocker && mode == "drift before Docker first") {
					t.Fatal("wrong first-effect/continuation result", phase, err)
				}
			}
			wantInstalls := 1
			if baseMissing {
				wantInstalls = 2
			} else if mode == "drift before Docker first" {
				wantInstalls = 0
			}
			if installs != wantInstalls {
				t.Fatal("wrong package effect count", installs)
			}
		})
	}
}

func TestBootstrapHostPackageExecutionBoundaries(t *testing.T) {
	for _, mode := range []string{"valid", "preflight", "already installed", "simulation failure", "unlocked solver", "drift before install", "install failure", "wrong result", "cancel after simulation", "authority after install"} {
		t.Run(mode, func(t *testing.T) {
			plan, before := bootstrapHostPackagePlanFixture(t, bootstrapHostInstallBase, true)
			preflight := mode == "preflight"
			if preflight {
				plan, _ = newBootstrapHostPackagePreflightPlan("noble", plan.directory, before)
			}
			after := bootstrapHostPackageResultFixture(plan, before)
			if mode == "already installed" {
				before = after
				plan, _ = newBootstrapHostPackagePlan("noble", plan.directory, bootstrapHostInstallBase, before)
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
			err := prepareBootstrapHostPackagesWith(ctx, plan.directory, bootstrapHostInstallBase, preflight, "noble", check, nil, read, run)
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
