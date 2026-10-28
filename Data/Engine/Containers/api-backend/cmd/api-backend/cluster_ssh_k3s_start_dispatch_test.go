package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

type sshStartupTestClient struct {
	*sshConfigurationTestClient
	start    func(context.Context, bool, *clusterbootstrap.K3sJoinMaterial, func(context.Context) error) error
	images   func(context.Context, bool, *clusterbootstrap.K3sJoinMaterial, func(context.Context) error) error
	activate func(context.Context, bool, engineidentity.Binding, *engineidentity.Material, *clusterbootstrap.K3sJoinMaterial, func(context.Context) error) error
}

func (c *sshStartupTestClient) StartPreparedK3s(ctx context.Context, _ *clusterbootstrap.PreparationInputs, _ clusterbootstrap.PreparationExpected, _ engineidentity.Binding, _ *engineidentity.Material, _ *clusterbootstrap.PreparationArtifacts, _ string, join *clusterbootstrap.K3sJoinMaterial, _ []byte, check func(context.Context) error) error {
	return c.start(ctx, false, join, check)
}
func (c *sshStartupTestClient) ReconcilePreparedK3sStart(ctx context.Context, _ *clusterbootstrap.PreparationInputs, _ clusterbootstrap.PreparationExpected, _ engineidentity.Binding, _ *engineidentity.Material, _ *clusterbootstrap.PreparationArtifacts, _ string, join *clusterbootstrap.K3sJoinMaterial, _ []byte, check func(context.Context) error) error {
	return c.start(ctx, true, join, check)
}
func (c *sshStartupTestClient) VerifyPreparedImages(ctx context.Context, _ *clusterbootstrap.PreparationInputs, _ clusterbootstrap.PreparationExpected, _ engineidentity.Binding, _ *engineidentity.Material, _ *clusterbootstrap.PreparationArtifacts, _ string, join *clusterbootstrap.K3sJoinMaterial, _ []byte, check func(context.Context) error) error {
	return c.images(ctx, false, join, check)
}
func (c *sshStartupTestClient) ReconcilePreparedImages(ctx context.Context, _ *clusterbootstrap.PreparationInputs, _ clusterbootstrap.PreparationExpected, _ engineidentity.Binding, _ *engineidentity.Material, _ *clusterbootstrap.PreparationArtifacts, _ string, join *clusterbootstrap.K3sJoinMaterial, _ []byte, check func(context.Context) error) error {
	return c.images(ctx, true, join, check)
}

func (c *sshStartupTestClient) ActivatePrejoinIdentity(ctx context.Context, _ *clusterbootstrap.PreparationInputs, _ clusterbootstrap.PreparationExpected, source engineidentity.Binding, identity *engineidentity.Material, _ *clusterbootstrap.PreparationArtifacts, _ string, join *clusterbootstrap.K3sJoinMaterial, _ []byte, check func(context.Context) error) error {
	return c.activate(ctx, false, source, identity, join, check)
}
func (c *sshStartupTestClient) ReconcilePrejoinIdentityActivation(ctx context.Context, _ *clusterbootstrap.PreparationInputs, _ clusterbootstrap.PreparationExpected, source engineidentity.Binding, identity *engineidentity.Material, _ *clusterbootstrap.PreparationArtifacts, _ string, join *clusterbootstrap.K3sJoinMaterial, _ []byte, check func(context.Context) error) error {
	return c.activate(ctx, true, source, identity, join, check)
}

func TestClusterSSHK3sStartupDispatch(t *testing.T) {
	_, request, frame := sshJoinSourceFixture(t)
	defer clear(frame)
	expected, source := request.Expected, request.Source
	keys := sshIdentitySourceMaterial(t, t.TempDir())
	for _, mode := range []string{"complete", "lost acknowledgement", "resume startup", "resume images", "unknown proof", "unknown image proof", "authority loss", "cleanup loss", "missing capability", "old prefix", "acquisition failure", "resume activation", "old activation prefix", "activation lost acknowledgement", "activation absent", "unknown activation proof", "activation authority loss", "activation cleanup loss", "activation acquisition failure"} {
		t.Run(mode, func(t *testing.T) {
			cursor := "configure_k3s"
			if mode == "resume startup" || mode == "old prefix" {
				cursor = "start_k3s"
			}
			if mode == "resume images" {
				cursor = "verify_images"
			}
			if mode == "resume activation" || mode == "old activation prefix" {
				cursor = "activate_identity"
			}
			var calls []string
			var borrowed []*clusterbootstrap.K3sJoinMaterial
			opened, closed, acquired := 0, 0, 0
			lost := false
			activated := cursor == "activate_identity"
			started := cursor == "start_k3s" || cursor == "verify_images" || activated
			verified := cursor == "verify_images" || activated
			check := func(context.Context) error {
				if lost {
					return clusterbootstrap.ErrSessionAuthority
				}
				return nil
			}
			connect := func(context.Context) (clusterSSHPrejoinClient, error) {
				if opened != closed {
					t.Fatal("next executor opened before previous connection joined")
				}
				opened++
				last := ""
				base := &sshPrejoinDispatchTestClient{
					inspect: func(context.Context) (clusterbootstrap.PreparationProgress, error) {
						return clusterbootstrap.PreparationProgress{Step: cursor}, nil
					},
					close: func() error {
						closed++
						if mode == "cleanup loss" && last == "start" || mode == "activation cleanup loss" && last == "activation" {
							return clusterbootstrap.ErrSessionAuthority
						}
						return nil
					},
					run: func(context.Context, string, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, engineidentity.Binding, *engineidentity.Material, []byte, func(context.Context) error) error {
						t.Fatal("earlier completed phase replayed")
						return nil
					},
				}
				configuration := &sshConfigurationTestClient{base, func(ctx context.Context, proof bool, join *clusterbootstrap.K3sJoinMaterial, current func(context.Context) error) error {
					if !proof || join.Verify(ctx, expected, source, current) != nil {
						t.Fatal("configuration replay/binding lost")
					}
					calls = append(calls, "configuration proof")
					return nil
				}}
				if mode == "missing capability" {
					return configuration, nil
				}
				start := func(ctx context.Context, proof bool, join *clusterbootstrap.K3sJoinMaterial, current func(context.Context) error) error {
					last = "start"
					if proof {
						last = "startup proof"
					} else {
						started = true
					}
					calls = append(calls, last)
					if join.Verify(ctx, expected, source, current) != nil {
						t.Fatal("startup private binding lost")
					}
					if proof && !started {
						return clusterbootstrap.ErrMutationUnknown
					}
					if mode == "authority loss" && !proof {
						lost = true
					}
					if !proof && mode == "lost acknowledgement" || proof && mode == "unknown proof" {
						return clusterbootstrap.ErrMutationUnknown
					}
					return nil
				}
				images := func(ctx context.Context, proof bool, join *clusterbootstrap.K3sJoinMaterial, current func(context.Context) error) error {
					last = "images"
					if proof {
						last = "image proof"
					} else {
						verified = true
					}
					calls = append(calls, last)
					if join.Verify(ctx, expected, source, current) != nil || proof && !verified {
						return clusterbootstrap.ErrMutationUnknown
					}
					if proof && mode == "unknown image proof" {
						return clusterbootstrap.ErrMutationUnknown
					}
					return nil
				}
				activate := func(ctx context.Context, proof bool, identitySource engineidentity.Binding, identity *engineidentity.Material, join *clusterbootstrap.K3sJoinMaterial, current func(context.Context) error) error {
					if last != "" {
						t.Fatal("activation reused an executor connection")
					}
					last = "activation"
					if proof {
						last = "activation proof"
					} else if mode != "activation absent" {
						activated = true
					}
					calls = append(calls, last)
					if !verified || identitySource != source || identity == nil || identity.Digest() != keys.Digest() || join.Verify(ctx, expected, source, current) != nil {
						t.Fatal("activation lost prefix, shared identity or private join binding")
					}
					if mode == "activation authority loss" && !proof {
						lost = true
					}
					if !proof && (mode == "activation lost acknowledgement" || mode == "activation absent") || proof && (mode == "unknown activation proof" || !activated) {
						return clusterbootstrap.ErrMutationUnknown
					}
					return nil
				}
				return &sshStartupTestClient{configuration, start, images, activate}, nil
			}
			acquire := func(ctx context.Context, binding engineidentity.Binding, consume clusterSSHJoinMaterialConsumer) error {
				acquired++
				if binding != source {
					t.Fatal("wrong original source")
				}
				if mode == "acquisition failure" && acquired == 2 || mode == "activation acquisition failure" && acquired == 6 {
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
			err := dispatchClusterSSHPrejoinStartup(context.Background(), connect, &clusterbootstrap.PreparationInputs{}, &clusterbootstrap.PreparationArtifacts{}, clusterbootstrap.PostgresImageRepository+"@sha256:"+strings.Repeat("a", 64), expected, source, keys, []byte("fixture"), check, acquire, mode != "old prefix" && mode != "old activation prefix")
			success := mode == "complete" || mode == "lost acknowledgement" || mode == "resume startup" || mode == "resume images" || mode == "resume activation" || mode == "activation lost acknowledgement"
			if (err == nil) != success || opened != closed {
				t.Fatal("wrong startup result/cleanup", err, opened, closed)
			}
			want := []string{"configuration proof", "start", "startup proof", "images", "image proof", "activation", "activation proof"}
			switch mode {
			case "resume startup":
				want = []string{"startup proof", "images", "image proof", "activation", "activation proof"}
			case "resume images":
				want = []string{"image proof", "activation", "activation proof"}
			case "old prefix", "old activation prefix":
				want = nil
			case "resume activation":
				want = []string{"activation proof"}
			case "activation authority loss", "activation cleanup loss":
				want = want[:6]
			case "activation acquisition failure":
				want = append(want[:5], "activation proof")
			case "authority loss", "cleanup loss":
				want = want[:2]
			case "unknown proof":
				want = want[:3]
			case "unknown image proof":
				want = want[:5]
			case "acquisition failure":
				want = []string{"configuration proof", "startup proof"}
			case "missing capability":
				want = want[:1]
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatal("startup write replay or missing independent proof", calls, want)
			}
			if mode == "complete" && acquired != 7 || mode == "resume startup" && acquired != 5 || mode == "resume images" && acquired != 3 || mode == "resume activation" && acquired != 1 {
				t.Fatal("private material reused across sessions")
			}
			for _, join := range borrowed {
				if join.Verify(context.Background(), expected, source, func(context.Context) error { return nil }) == nil {
					t.Fatal("private material escaped scope")
				}
			}
		})
	}
	if clusterSSHStartupDispatchBudget(true, true, true) != 370*time.Minute || clusterSSHStartupDispatchBudget(true, true, false) != 190*time.Minute || clusterSSHStartupDispatchBudget(true, false, false) != 130*time.Minute || clusterSSHStartupDispatchBudget(false, false, false) != 10*time.Minute {
		t.Fatal("startup/legacy budgets diverged")
	}
}
