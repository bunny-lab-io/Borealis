package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestClusterSSHArtifactDispatch(t *testing.T) {
	a, source, _ := sshPrejoinSourceFixture(t, false)
	expected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, a.Lease, a.Baseline, a.K3sVersion, "10.42.0.0/16", "10.43.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	keys := sshIdentitySourceMaterial(t, t.TempDir())
	// Transport seam only. Production native sender rejects these inert objects.
	inputs := &clusterbootstrap.PreparationInputs{}
	for _, mode := range []string{"complete", "lost acknowledgement", "incomplete publication", "resume host", "resume artifacts", "prefix rejects artifacts", "authority loss", "cleanup loss", "resume k3s", "prefix rejects k3s", "k3s lost acknowledgement", "incomplete k3s"} {
		t.Run(mode, func(t *testing.T) {
			artifacts := &clusterbootstrap.PreparationArtifacts{}
			pg := clusterbootstrap.PostgresImageRepository + "@sha256:" + strings.Repeat("a", 64)
			step := "empty"
			if mode == "resume host" {
				step = "prepare_host"
			}
			if mode == "resume artifacts" || mode == "prefix rejects artifacts" {
				step = "stage_images"
			}
			if mode == "resume k3s" || mode == "prefix rejects k3s" {
				step = "install_k3s"
			}
			if mode == "prefix rejects artifacts" || mode == "prefix rejects k3s" {
				artifacts = nil
				pg = ""
			}
			var calls []string
			opened, closed := 0, 0
			lost := false
			check := func(context.Context) error {
				if lost {
					return clusterbootstrap.ErrSessionAuthority
				}
				return nil
			}
			connect := func(context.Context) (clusterSSHPrejoinClient, error) {
				opened++
				last := ""
				return &sshPrejoinDispatchTestClient{
					inspect: func(context.Context) (clusterbootstrap.PreparationProgress, error) {
						return clusterbootstrap.PreparationProgress{Step: step}, nil
					},
					close: func() error {
						closed++
						if mode == "cleanup loss" && last == "artifacts" {
							return clusterbootstrap.ErrSessionAuthority
						}
						return nil
					},
					run: func(ctx context.Context, action string, p *clusterbootstrap.PreparationInputs, e clusterbootstrap.PreparationExpected, s engineidentity.Binding, k *engineidentity.Material, sudo []byte, current func(context.Context) error) error {
						last = action
						calls = append(calls, action)
						if p != inputs || !reflect.DeepEqual(e, expected) || string(sudo) != "private fixture" || current(ctx) != nil {
							t.Fatal("binding drift")
						}
						if (strings.HasPrefix(action, "artifacts") || strings.HasPrefix(action, "k3s")) && (s != source.Binding || !k.Equal(keys)) {
							t.Fatal("artifact source identity lost")
						}
						if action == "artifacts" {
							if mode == "authority loss" {
								lost = true
								return clusterbootstrap.ErrSessionAuthority
							}
							if mode == "lost acknowledgement" || mode == "incomplete publication" {
								return clusterbootstrap.ErrMutationUnknown
							}
						}
						if action == "artifacts proof" && mode == "incomplete publication" {
							return clusterbootstrap.ErrMutationUnknown
						}
						if action == "k3s" && (mode == "k3s lost acknowledgement" || mode == "incomplete k3s") || action == "k3s proof" && mode == "incomplete k3s" {
							return clusterbootstrap.ErrMutationUnknown
						}
						return nil
					},
				}, nil
			}
			err := dispatchClusterSSHPrejoinArtifacts(context.Background(), connect, inputs, artifacts, pg, expected, source.Binding, keys, []byte("private fixture"), check)
			success := mode == "complete" || mode == "lost acknowledgement" || mode == "resume host" || mode == "resume artifacts" || mode == "resume k3s" || mode == "k3s lost acknowledgement"
			if (err == nil) != success || opened != closed {
				t.Fatal("wrong outcome/cleanup", err, opened, closed)
			}
			want := []string{"source", "source proof", "account", "account proof", "directory", "directory proof", "keys", "keys proof", "host", "host proof", "artifacts", "artifacts proof"}
			switch mode {
			case "resume host":
				want = []string{"host proof", "artifacts", "artifacts proof"}
			case "resume artifacts":
				want = []string{"artifacts proof"}
			case "prefix rejects artifacts", "prefix rejects k3s":
				want = nil
			case "authority loss", "cleanup loss":
				want = want[:len(want)-1]
			}
			if success || mode == "incomplete k3s" {
				if mode == "resume k3s" {
					want = []string{"k3s proof"}
				} else {
					want = append(want, "k3s", "k3s proof")
				}
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatal("write replayed or proof skipped", calls, want)
			}
		})
	}
}
