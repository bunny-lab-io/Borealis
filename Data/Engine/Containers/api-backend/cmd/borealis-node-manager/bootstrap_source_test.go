package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootstrapSourceNativeProofBindings(t *testing.T) {
	r := identityRuntimeRequestFixture()
	expected := clusterbootstrap.PreparationExpected{
		Source: clusterbootstrap.Expected{Repository: r.Repository, Release: r.Release, SourceSHA: r.SourceSHA, AllowQualification: true}, Target: r.Binding,
		CohortSHA256: strings.Repeat("c", 64), TargetBootID: "55555555-5555-4555-8555-555555555555", KubeSystemUID: "66666666-6666-4666-8666-666666666666",
		ControlPlaneVIP: "192.168.3.248", EdgeVIP: "192.168.3.249", ManagementCIDR: "192.168.3.0/24", K3sVersion: "v1.36.3+k3s1", PodCIDR: "10.42.0.0/16", ServiceCIDR: "10.43.0.0/16",
		PeerAddresses: []string{"192.168.3.250", "192.168.3.251", "192.168.3.252"},
	}
	request, err := clusterbootstrap.NewPreparationSessionRequest(expected, r.ManagerSHA256, r.Nonce, "stage_source")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"valid", "invalid request", "missing observer", "host", "host drift", "boot", "boot drift", "paths", "paths drift", "unit", "cancel", "cancel during unit"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			want := request
			if mode == "invalid request" {
				want.Action = "join_cluster"
			}
			hosts, boots, paths, units := 0, 0, 0, 0
			observers := bootstrapSourceObservers{
				host: func(_ context.Context, got clusterbootstrap.SessionRequest) error {
					hosts++
					if got != r {
						t.Fatal("helper or host request substituted")
					}
					if mode == "host" || mode == "host drift" && hosts == 2 {
						return errors.New("private host error")
					}
					return nil
				},
				boot: func() (string, error) {
					boots++
					if mode == "boot" || mode == "boot drift" && boots == 2 {
						return "other\n", nil
					}
					return expected.TargetBootID + "\n", nil
				},
				paths: func(action string, destination bool) error {
					paths++
					if action != "stage_source" || !destination {
						t.Fatal("path scope substituted")
					}
					if mode == "paths" || mode == "paths drift" && paths == 2 {
						return errors.New("private path error")
					}
					return nil
				},
				unit: func(_ context.Context, unit string) error {
					units++
					if unit != []string{"k3s.service", "k3s-agent.service", "borealis-node-manager.service"}[units-1] {
						t.Fatal("unit substituted")
					}
					if mode == "unit" {
						return errors.New("private unit error")
					}
					if mode == "cancel during unit" {
						cancel()
					}
					return nil
				},
			}
			if mode == "missing observer" {
				observers.boot = nil
			}
			err := verifyBootstrapSourceWith(ctx, want, true, observers)
			if (err == nil) != (mode == "valid") {
				t.Fatal("unexpected native proof", err)
			}
			if mode == "valid" && (hosts != 2 || boots != 2 || paths != 2 || units != 3) {
				t.Fatal("missing repeated proof")
			}
			if (mode == "invalid request" || mode == "missing observer" || mode == "cancel") && hosts != 0 {
				t.Fatal("host accessed without valid request")
			}
		})
	}
}

func TestBootstrapSourcePaths(t *testing.T) {
	for _, mode := range []string{"fresh", "reconcile", "prepare_runtime_identity", "reconcile_runtime_identity", "prepare_prejoin_identity_root", "reconcile_prejoin_identity_root", "install_prejoin_identity", "reconcile_prejoin_identity", "missing account source", "occupied", "symlink", "unsafe ancestor", "file ancestor", "runtime", "missing source", "unknown action"} {
		t.Run(mode, func(t *testing.T) {
			base := t.TempDir()
			if err := os.Mkdir(filepath.Join(base, "opt"), 0755); err != nil {
				t.Fatal(err)
			}
			action := "stage_source"
			switch mode {
			case "reconcile", "occupied", "prepare_runtime_identity", "reconcile_runtime_identity", "prepare_prejoin_identity_root", "reconcile_prejoin_identity_root", "install_prejoin_identity", "reconcile_prejoin_identity":
				if err := os.Mkdir(filepath.Join(base, "opt", "Borealis"), 0755); err != nil {
					t.Fatal(err)
				}
				if mode == "reconcile" {
					action = "reconcile_source"
				}
				if strings.HasSuffix(mode, "runtime_identity") || strings.Contains(mode, "prejoin") {
					action = mode
				}
			case "symlink":
				if err := os.Symlink(t.TempDir(), filepath.Join(base, "opt", "Borealis")); err != nil {
					t.Fatal(err)
				}
			case "unsafe ancestor":
				if err := os.Chmod(filepath.Join(base, "opt"), 0777); err != nil {
					t.Fatal(err)
				}
			case "file ancestor":
				if err := os.WriteFile(filepath.Join(base, "etc"), []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			case "runtime":
				if err := os.MkdirAll(filepath.Join(base, "var", "lib", "rancher", "k3s"), 0755); err != nil {
					t.Fatal(err)
				}
			case "missing source":
				action = "reconcile_source"
			case "missing account source":
				action = "prepare_runtime_identity"
			case "unknown action":
				action = "join_cluster"
			}
			root, err := os.OpenRoot(base)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			err = verifyBootstrapSourcePaths(root, action, true)
			if (err == nil) != (mode == "fresh" || mode == "reconcile" || strings.HasSuffix(mode, "runtime_identity") || strings.Contains(mode, "prejoin")) {
				t.Fatal("unsafe path evidence accepted", err)
			}
			if mode == "occupied" && verifyBootstrapSourcePaths(root, action, false) != nil {
				t.Fatal("post-publication authority incorrectly requires absent source")
			}
		})
	}
}

func TestBootstrapSourceUnits(t *testing.T) {
	valid := "LoadState=not-found\nActiveState=inactive\nSubState=dead\nMainPID=0\nControlPID=0\nNeedDaemonReload=no\nJob=\n"
	if validateBootstrapAbsentUnit(valid) != nil {
		t.Fatal("absent unit rejected")
	}
	for _, raw := range []string{
		strings.Replace(valid, "not-found", "loaded", 1), strings.Replace(valid, "inactive", "active", 1),
		strings.Replace(valid, "dead", "running", 1), strings.Replace(valid, "MainPID=0", "MainPID=1", 1),
		strings.Replace(valid, "ControlPID=0", "ControlPID=2", 1), strings.Replace(valid, "NeedDaemonReload=no", "NeedDaemonReload=yes", 1),
		strings.Replace(valid, "Job=", "Job=3", 1), valid + "Job=\n", strings.TrimSuffix(valid, "Job=\n"), valid + "Unexpected=1\n", valid[:len(valid)-1],
	} {
		if validateBootstrapAbsentUnit(raw) == nil {
			t.Fatal("unknown or active unit accepted")
		}
	}
}
