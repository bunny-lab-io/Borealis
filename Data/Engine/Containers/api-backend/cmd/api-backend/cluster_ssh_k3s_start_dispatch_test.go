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
	start func(context.Context, bool, *clusterbootstrap.K3sJoinMaterial, func(context.Context) error) error
}

func (c *sshStartupTestClient) StartPreparedK3s(ctx context.Context, _ *clusterbootstrap.PreparationInputs, _ clusterbootstrap.PreparationExpected, _ engineidentity.Binding, _ *engineidentity.Material, _ *clusterbootstrap.PreparationArtifacts, _ string, join *clusterbootstrap.K3sJoinMaterial, _ []byte, check func(context.Context) error) error {
	return c.start(ctx, false, join, check)
}
func (c *sshStartupTestClient) ReconcilePreparedK3sStart(ctx context.Context, _ *clusterbootstrap.PreparationInputs, _ clusterbootstrap.PreparationExpected, _ engineidentity.Binding, _ *engineidentity.Material, _ *clusterbootstrap.PreparationArtifacts, _ string, join *clusterbootstrap.K3sJoinMaterial, _ []byte, check func(context.Context) error) error {
	return c.start(ctx, true, join, check)
}

func TestClusterSSHK3sStartupDispatch(t *testing.T) {
	_, request, frame := sshJoinSourceFixture(t)
	defer clear(frame)
	expected, source := request.Expected, request.Source
	keys := sshIdentitySourceMaterial(t, t.TempDir())
	for _, mode := range []string{"complete", "lost acknowledgement", "resume startup", "unknown proof", "authority loss", "cleanup loss", "missing capability", "old prefix", "acquisition failure"} {
		t.Run(mode, func(t *testing.T) {
			cursor := "configure_k3s"
			if mode == "resume startup" || mode == "old prefix" {
				cursor = "start_k3s"
			}
			var calls []string
			var borrowed []*clusterbootstrap.K3sJoinMaterial
			opened, closed, acquired := 0, 0, 0
			lost := false
			started := cursor == "start_k3s"
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
						if mode == "cleanup loss" && last == "start" {
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
				return &sshStartupTestClient{configuration, func(ctx context.Context, proof bool, join *clusterbootstrap.K3sJoinMaterial, current func(context.Context) error) error {
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
				}}, nil
			}
			acquire := func(ctx context.Context, binding engineidentity.Binding, consume clusterSSHJoinMaterialConsumer) error {
				acquired++
				if binding != source {
					t.Fatal("wrong original source")
				}
				if mode == "acquisition failure" && acquired == 2 {
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
			err := dispatchClusterSSHPrejoinStartup(context.Background(), connect, &clusterbootstrap.PreparationInputs{}, &clusterbootstrap.PreparationArtifacts{}, clusterbootstrap.PostgresImageRepository+"@sha256:"+strings.Repeat("a", 64), expected, source, keys, []byte("fixture"), check, acquire, mode != "old prefix")
			success := mode == "complete" || mode == "lost acknowledgement" || mode == "resume startup"
			if (err == nil) != success || opened != closed {
				t.Fatal("wrong startup result/cleanup", err, opened, closed)
			}
			want := []string{"configuration proof", "start", "startup proof"}
			switch mode {
			case "resume startup":
				want = []string{"startup proof"}
			case "old prefix":
				want = nil
			case "authority loss", "cleanup loss":
				want = want[:2]
			case "acquisition failure":
				want = []string{"configuration proof", "startup proof"}
			case "missing capability":
				want = want[:1]
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatal("startup write replay or missing independent proof", calls, want)
			}
			if mode == "complete" && acquired != 3 || mode == "resume startup" && acquired != 1 {
				t.Fatal("private material reused across sessions")
			}
			for _, join := range borrowed {
				if join.Verify(context.Background(), expected, source, func(context.Context) error { return nil }) == nil {
					t.Fatal("private material escaped scope")
				}
			}
		})
	}
	if clusterSSHStartupDispatchBudget(true, true, true) != 250*time.Minute || clusterSSHStartupDispatchBudget(true, true, false) != 190*time.Minute || clusterSSHStartupDispatchBudget(true, false, false) != 130*time.Minute || clusterSSHStartupDispatchBudget(false, false, false) != 10*time.Minute {
		t.Fatal("startup/legacy budgets diverged")
	}
}
