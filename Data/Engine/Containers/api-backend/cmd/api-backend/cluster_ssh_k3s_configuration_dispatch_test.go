package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
)

type sshConfigurationTestClient struct {
	*sshPrejoinDispatchTestClient
	configure func(context.Context, bool, *clusterbootstrap.K3sJoinMaterial, func(context.Context) error) error
}

func (c *sshConfigurationTestClient) ConfigurePrejoinK3s(ctx context.Context, p *clusterbootstrap.PreparationInputs, e clusterbootstrap.PreparationExpected, s engineidentity.Binding, keys *engineidentity.Material, assets *clusterbootstrap.PreparationArtifacts, pg string, join *clusterbootstrap.K3sJoinMaterial, sudo []byte, check func(context.Context) error) error {
	return c.configure(ctx, false, join, check)
}
func (c *sshConfigurationTestClient) ReconcilePrejoinK3sConfiguration(ctx context.Context, p *clusterbootstrap.PreparationInputs, e clusterbootstrap.PreparationExpected, s engineidentity.Binding, keys *engineidentity.Material, assets *clusterbootstrap.PreparationArtifacts, pg string, join *clusterbootstrap.K3sJoinMaterial, sudo []byte, check func(context.Context) error) error {
	return c.configure(ctx, true, join, check)
}

func TestClusterSSHK3sConfigurationDispatch(t *testing.T) {
	_, request, frame := sshJoinSourceFixture(t)
	defer clear(frame)
	expected, source := request.Expected, request.Source
	keys := sshIdentitySourceMaterial(t, t.TempDir())
	for _, mode := range []string{"complete", "resume installed", "resume configuration", "lost acknowledgement", "partial", "authority loss", "acquisition failure", "missing capability", "old prefix", "cleanup loss"} {
		t.Run(mode, func(t *testing.T) {
			cursor := "empty"
			if mode == "resume installed" {
				cursor = "install_k3s"
			}
			if mode == "resume configuration" || mode == "old prefix" {
				cursor = "configure_k3s"
			}
			var calls []string
			var borrowed []*clusterbootstrap.K3sJoinMaterial
			opened, closed, acquisitions := 0, 0, 0
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
				base := &sshPrejoinDispatchTestClient{
					inspect: func(context.Context) (clusterbootstrap.PreparationProgress, error) {
						return clusterbootstrap.PreparationProgress{Step: cursor}, nil
					},
					close: func() error {
						closed++
						if mode == "cleanup loss" && last == "configure" {
							return clusterbootstrap.ErrSessionAuthority
						}
						return nil
					},
					run: func(ctx context.Context, action string, p *clusterbootstrap.PreparationInputs, e clusterbootstrap.PreparationExpected, s engineidentity.Binding, k *engineidentity.Material, sudo []byte, current func(context.Context) error) error {
						if current(ctx) != nil || acquisitions != 0 {
							t.Fatal("private acquisition preceded installed proof")
						}
						calls = append(calls, action)
						return nil
					},
				}
				if mode == "missing capability" {
					return base, nil
				}
				return &sshConfigurationTestClient{base, func(ctx context.Context, proof bool, join *clusterbootstrap.K3sJoinMaterial, current func(context.Context) error) error {
					action := "configure"
					if proof {
						action = "configuration proof"
					}
					last = action
					calls = append(calls, action)
					if join.Verify(ctx, expected, source, current) != nil {
						t.Fatal("lost private claim/source binding")
					}
					if !proof && mode == "authority loss" {
						lost = true
					}
					if !proof && (mode == "lost acknowledgement" || mode == "partial") || proof && mode == "partial" {
						return clusterbootstrap.ErrMutationUnknown
					}
					return nil
				}}, nil
			}
			acquire := func(ctx context.Context, selected engineidentity.Binding, consume clusterSSHJoinMaterialConsumer) error {
				acquisitions++
				if selected != source {
					t.Fatal("changed source")
				}
				if mode == "acquisition failure" {
					return clusterbootstrap.ErrK3sJoinMaterial
				}
				join, err := clusterbootstrap.ReceiveK3sJoinMaterial(ctx, expected, source, bytes.NewReader(frame), check)
				if err != nil {
					t.Fatal(err)
				}
				borrowed = append(borrowed, join)
				defer join.Close()
				return consume(ctx, join, check)
			}
			if mode == "old prefix" {
				acquire = nil
			}
			err := dispatchClusterSSHPrejoinConfiguration(context.Background(), connect, &clusterbootstrap.PreparationInputs{}, &clusterbootstrap.PreparationArtifacts{}, clusterbootstrap.PostgresImageRepository+"@sha256:"+strings.Repeat("a", 64), expected, source, keys, []byte("fixture"), check, acquire)
			success := mode == "complete" || mode == "resume installed" || mode == "resume configuration" || mode == "lost acknowledgement"
			if (err == nil) != success || opened != closed {
				t.Fatal("wrong result/cleanup", err, opened, closed)
			}
			prefix := []string{"source", "source proof", "account", "account proof", "directory", "directory proof", "keys", "keys proof", "host", "host proof", "artifacts", "artifacts proof", "k3s", "k3s proof"}
			switch mode {
			case "resume installed":
				prefix = []string{"k3s proof"}
			case "resume configuration", "old prefix":
				prefix = nil
			}
			want := append([]string(nil), prefix...)
			switch mode {
			case "old prefix", "missing capability", "acquisition failure":
			case "resume configuration":
				want = append(want, "configuration proof")
			case "authority loss", "cleanup loss":
				want = append(want, "configure")
			default:
				want = append(want, "configure", "configuration proof")
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatal("write replay or skipped proof", calls, want)
			}
			for _, material := range borrowed {
				if material.Verify(context.Background(), expected, source, func(context.Context) error { return nil }) == nil {
					t.Fatal("private scope escaped")
				}
			}
		})
	}
}

func TestClusterSSHK3sConfigurationCapacity(t *testing.T) {
	_, request, frame := sshJoinSourceFixture(t)
	clear(frame)
	demands, err := clusterSSHK3sConfigurationDemands(request.Expected)
	config, configErr := clusterbootstrap.PrejoinK3sJoinConfiguration(request.Expected)
	if err != nil || configErr != nil || len(demands) != 1 || demands[0].Path != "/etc/rancher/k3s" || demands[0].Bytes != uint64(len(config)+clusterbootstrap.MaxK3sJoinTokenBytes+clusterbootstrap.MaxK3sGeneratedKubeconfigBytes) || demands[0].Entries != 3 {
		t.Fatal("configuration not fully reserved", err)
	}
	merged, err := mergeClusterSSHFilesystemDemands(demands, []clusterSSHFilesystemDemand{{Path: "/etc/rancher/k3s", Bytes: 23, Entries: 1}})
	if err != nil || len(merged) != 1 || merged[0].Bytes != demands[0].Bytes+23 || merged[0].Entries != 4 {
		t.Fatal("capacity discounted shared path")
	}
	request.Expected.Target.Hostname = "invalid\nname"
	if _, err := clusterSSHK3sConfigurationDemands(request.Expected); err == nil {
		t.Fatal("invalid target acquired capacity")
	}
}
