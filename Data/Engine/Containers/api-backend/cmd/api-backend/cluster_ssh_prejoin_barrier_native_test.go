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
	for _, tc := range []struct {
		name, cursor    string
		phase           clusterSSHInstallPhase
		success, starts bool
	}{
		{"configured", "configure_k3s", prepareBeforeJoin, true, false},
		{"joined", "configure_k3s", joinDrainedServer, true, true},
		{"lost join acknowledgement", "start_k3s", joinDrainedServer, true, false},
		{"unknown configured proof", "configure_k3s", joinDrainedServer, false, false},
		{"join before configured", "prepare_host", joinDrainedServer, false, false},
		{"finish before membership", "configure_k3s", finishJoinedServer, false, false},
		{"finish after membership", "start_k3s", finishJoinedServer, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
						return clusterbootstrap.PreparationProgress{Step: tc.cursor}, nil
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
						if tc.name == "unknown configured proof" {
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
			var receipt clusterbootstrap.PrejoinPreparationReceipt
			err := dispatchClusterSSHPrejoinPhase(context.WithValue(context.Background(), clusterSSHPairManifestKey{}, map[string]string{}), connect, &clusterbootstrap.PreparationInputs{}, &clusterbootstrap.PreparationArtifacts{},
				clusterbootstrap.PostgresImageRepository+"@sha256:"+strings.Repeat("a", 64), expected, source, keys, nil, check, acquire, true, &receipt, tc.phase)
			if (err == nil) != tc.success || opened != closed || cursors != 1 || slices.Contains(actions, "start") != tc.starts {
				t.Fatal("incorrect phase or repeated join", err, actions)
			}
			if tc.name == "joined" && slices.Contains(actions, "start proof") {
				t.Fatal("repeated successful journaled start", actions)
			}
			if tc.name == "lost join acknowledgement" && !slices.Equal(actions, []string{"start proof"}) {
				t.Fatal("lost acknowledgement requires actual membership proof", actions)
			}
			if tc.phase == joinDrainedServer && (slices.Contains(actions, "images") || slices.Contains(actions, "activation")) {
				t.Fatal("join waited for application work", actions)
			}
		})
	}
}
