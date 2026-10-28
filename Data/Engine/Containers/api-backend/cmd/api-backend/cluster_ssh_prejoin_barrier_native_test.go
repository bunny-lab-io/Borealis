package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
)

func TestClusterSSHPrejoinNativeBarrierPhases(t *testing.T) {
	_, request, frame := sshJoinSourceFixture(t)
	defer clear(frame)
	expected, source := request.Expected, request.Source
	keys := sshIdentitySourceMaterial(t, t.TempDir())
	for _, mode := range []string{"fresh preparation", "configured retry", "completed retry", "partial startup", "partial verification", "configured proof", "completed proof", "unknown configured proof", "start barrier", "failed barrier", "barrier authority loss", "barrier started drift", "barrier completed drift"} {
		t.Run(mode, func(t *testing.T) {
			cursor, prepareOnly, proofStep := "configure_k3s", true, -1
			switch mode {
			case "fresh preparation":
				cursor = "empty"
			case "completed retry":
				cursor = "activate_identity"
			case "partial startup":
				cursor = "start_k3s"
			case "partial verification":
				cursor = "verify_images"
			case "configured proof", "unknown configured proof":
				prepareOnly, proofStep = false, 7
			case "completed proof":
				prepareOnly, proofStep = false, 10
			case "start barrier", "failed barrier", "barrier authority loss":
				prepareOnly = false
			case "barrier started drift":
				prepareOnly, cursor = false, "start_k3s"
			case "barrier completed drift":
				prepareOnly, cursor = false, "activate_identity"
			}
			var actions []string
			opened, closed, cursors := 0, 0, 0
			lost := false
			check := func(context.Context) error {
				if lost {
					return clusterbootstrap.ErrSessionAuthority
				}
				return nil
			}
			connect := func(context.Context) (clusterSSHPrejoinClient, error) {
				if opened != closed {
					t.Fatal("overlapping target executors")
				}
				opened++
				base := &sshPrejoinDispatchTestClient{
					inspect: func(context.Context) (clusterbootstrap.PreparationProgress, error) {
						cursors++
						return clusterbootstrap.PreparationProgress{Step: cursor}, nil
					},
					close: func() error { closed++; return nil },
					run: func(_ context.Context, name string, _ *clusterbootstrap.PreparationInputs, _ clusterbootstrap.PreparationExpected, _ engineidentity.Binding, _ *engineidentity.Material, _ []byte, _ func(context.Context) error) error {
						actions = append(actions, name)
						return nil
					},
				}
				action := func(name string) func(context.Context, bool, *clusterbootstrap.K3sJoinMaterial, func(context.Context) error) error {
					return func(ctx context.Context, proof bool, join *clusterbootstrap.K3sJoinMaterial, current func(context.Context) error) error {
						if join.Verify(ctx, expected, source, current) != nil {
							return clusterbootstrap.ErrSessionAuthority
						}
						if proof {
							name += " proof"
						}
						actions = append(actions, name)
						if mode == "unknown configured proof" {
							return clusterbootstrap.ErrMutationUnknown
						}
						return nil
					}
				}
				return &sshStartupTestClient{
					sshConfigurationTestClient: &sshConfigurationTestClient{base, action("configuration")},
					start:                      action("start"), images: action("images"),
					activate: func(ctx context.Context, proof bool, _ engineidentity.Binding, _ *engineidentity.Material, join *clusterbootstrap.K3sJoinMaterial, current func(context.Context) error) error {
						return action("activation")(ctx, proof, join, current)
					},
				}, nil
			}
			acquire := func(ctx context.Context, binding engineidentity.Binding, consume clusterSSHJoinMaterialConsumer) error {
				if binding != source {
					t.Fatal("private source changed")
				}
				join, err := clusterbootstrap.ReceiveK3sJoinMaterial(ctx, expected, source, bytes.NewReader(frame), check)
				if err != nil {
					return err
				}
				defer join.Close()
				return consume(ctx, join, check)
			}
			var beforeStart func(context.Context) error
			if strings.Contains(mode, "barrier") {
				beforeStart = func(context.Context) error {
					if opened != closed {
						t.Fatal("barrier ran while target executor/connection remained open")
					}
					actions = append(actions, "barrier")
					if mode == "failed barrier" {
						return clusterbootstrap.ErrSessionAuthority
					}
					lost = mode == "barrier authority loss"
					return nil
				}
			}
			var receipt clusterbootstrap.PrejoinPreparationReceipt
			err := dispatchClusterSSHPrejoinPhase(context.Background(), connect, &clusterbootstrap.PreparationInputs{}, &clusterbootstrap.PreparationArtifacts{},
				clusterbootstrap.PostgresImageRepository+"@sha256:"+strings.Repeat("a", 64), expected, source, keys, nil, check, acquire, true, &receipt, prepareOnly, proofStep, beforeStart)
			success := mode != "partial startup" && mode != "partial verification" && mode != "unknown configured proof" && mode != "failed barrier" && mode != "barrier authority loss" && mode != "barrier started drift" && mode != "barrier completed drift"
			if (err == nil) != success || opened != closed {
				t.Fatal("incorrect phase outcome/cleanup", err, actions)
			}
			if proofStep >= 0 && cursors != 0 {
				t.Fatal("barrier used cursor instead of direct phase proof")
			}
			if strings.HasSuffix(mode, " drift") && len(actions) != 0 {
				t.Fatal("changed target phase authorized later effects", actions)
			}
			if mode == "start barrier" {
				if slices.Index(actions, "barrier")+1 != slices.Index(actions, "start") {
					t.Fatal("native mutation preceded immediate barrier", actions)
				}
			} else if slices.Contains(actions, "start") {
				t.Fatal("preparation/proof/failure started K3s", actions)
			}
			completed := mode == "completed retry" || mode == "completed proof" || mode == "start barrier"
			if completed && receipt.Validate(expected, source) != nil || !completed && receipt != (clusterbootstrap.PrejoinPreparationReceipt{}) {
				t.Fatal("phase fabricated or lost typed final receipt")
			}
		})
	}
}
