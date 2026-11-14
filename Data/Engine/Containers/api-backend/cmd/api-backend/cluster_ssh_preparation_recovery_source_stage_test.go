package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"context"
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"
)

type sourceStageTestClient struct {
	inspect   func() (clusterbootstrap.PreparationProgress, error)
	reconcile func() error
	absent    func() error
	close     func() error
}

func (c *sourceStageTestClient) InspectPreparationProgress(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, []byte, func(context.Context) error) (clusterbootstrap.PreparationProgress, error) {
	return c.inspect()
}
func (c *sourceStageTestClient) ReconcilePreparation(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, []byte, func(context.Context) error) error {
	return c.reconcile()
}
func (c *sourceStageTestClient) ReconcilePreparationAbsent(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, []byte, func(context.Context) error) error {
	return c.absent()
}
func (c *sourceStageTestClient) Close() error { return c.close() }

func TestClusterSSHPreparationRecoverySourceStageResolution(t *testing.T) {
	for _, mode := range []string{"applied", "absent", "retained absence", "empty", "later phase", "invalid cursor", "inspect error", "absence error", "absence lost ACK", "false absence cursor", "dial error", "owned dial error", "inspect close", "reconcile close", "absent close", "input drift", "authority lost", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var calls []string
			cursor := "stage_source"
			switch mode {
			case "retained absence":
				cursor = "source_absent"
			case "empty":
				cursor = "empty"
			case "later phase":
				cursor = "prepare_runtime_identity"
			case "invalid cursor":
				cursor = "completed"
			}
			denied, drifted := false, false
			target := clusterSSHPreparationRecoveryTargetInputs{
				current: func(ctx context.Context) error {
					if denied {
						return errClusterConflict
					}
					return ctx.Err()
				}, inputCheck: func(ctx context.Context) error {
					if drifted {
						return errClusterConflict
					}
					return ctx.Err()
				},
			}
			opened, closed := 0, 0
			connect := func(context.Context, clusterremote.Target, clusterremote.HostKey, *clusterremote.Credential) (clusterSSHPreparationRecoverySourceStageClient, error) {
				if mode == "dial error" {
					return nil, errors.New("synthetic dial")
				}
				opened++
				sessionActions := 0
				last := ""
				record := func(action string) {
					if sessionActions != 0 {
						t.Error("native actions shared executor")
					}
					sessionActions++
					last = action
					calls = append(calls, action)
				}
				client := &sourceStageTestClient{
					inspect: func() (clusterbootstrap.PreparationProgress, error) {
						record("inspect")
						if mode == "inspect error" {
							return clusterbootstrap.PreparationProgress{Step: "stage_source"}, errors.New("synthetic reply")
						}
						return clusterbootstrap.PreparationProgress{Step: cursor}, nil
					},
					reconcile: func() error {
						record("reconcile")
						switch mode {
						case "applied":
							return nil
						case "input drift":
							drifted = true
						case "authority lost":
							denied = true
						case "cancel":
							cancel()
						}
						return errors.New("native applied proof unavailable")
					},
					absent: func() error {
						record("absent")
						if mode == "absence error" {
							return errors.New("native absence unavailable")
						}
						if mode != "false absence cursor" {
							cursor = "source_absent"
						}
						if mode == "absence lost ACK" {
							return errors.New("synthetic lost reply")
						}
						return nil
					},
					close: func() error {
						closed++
						if mode == last+" close" {
							return errors.New("synthetic cleanup")
						}
						return nil
					},
				}
				if mode == "owned dial error" {
					return client, errors.New("synthetic dial")
				}
				return client, nil
			}
			resolution, err := reconcileClusterSSHPreparationSourceStage(ctx, target, connect)
			valid := mode == "applied" || mode == "absent" || mode == "retained absence"
			if (err == nil) != valid || !valid && resolution != "" || opened != closed {
				t.Fatalf("resolution/cleanup: %s %v opened=%d closed=%d", resolution, err, opened, closed)
			}
			switch mode {
			case "applied":
				if resolution != "applied" || !slices.Equal(calls, []string{"inspect", "reconcile"}) {
					t.Fatal(calls, resolution)
				}
			case "absent":
				if resolution != "not_applied" || !slices.Equal(calls, []string{"inspect", "reconcile", "absent", "inspect"}) {
					t.Fatal(calls, resolution)
				}
			case "retained absence":
				if resolution != "not_applied" || !slices.Equal(calls, []string{"inspect", "absent", "inspect"}) {
					t.Fatal(calls, resolution)
				}
			case "reconcile close", "input drift", "authority lost", "cancel", "inspect close", "inspect error", "owned dial error":
				if slices.Contains(calls, "absent") {
					t.Fatal("boundary loss became absence", calls)
				}
			}
		})
	}
}

func TestClusterSSHPreparationRecoverySourceStageInterface(t *testing.T) {
	contract := reflect.TypeOf((*clusterSSHPreparationRecoverySourceStageClient)(nil)).Elem()
	var names []string
	for i := 0; i < contract.NumMethod(); i++ {
		names = append(names, contract.Method(i).Name)
	}
	if !slices.Equal(names, []string{"Close", "InspectPreparationProgress", "ReconcilePreparation", "ReconcilePreparationAbsent"}) {
		t.Fatal("source reconciliation gained effect capability", names)
	}
	if clusterSSHSourceStageCursor(clusterbootstrap.PreparationProgress{Step: "empty"}) || clusterSSHSourceStageCursor(clusterbootstrap.PreparationProgress{Step: "prepare_host"}) {
		t.Fatal("unsupported source prefix")
	}
}

func TestClusterSSHPreparationRecoverySourceStageJoinsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, closing, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	target := clusterSSHPreparationRecoveryTargetInputs{current: func(ctx context.Context) error { return ctx.Err() }, inputCheck: func(ctx context.Context) error { return ctx.Err() }}
	client := &sourceStageTestClient{
		inspect: func() (clusterbootstrap.PreparationProgress, error) {
			close(started)
			<-ctx.Done()
			return clusterbootstrap.PreparationProgress{Step: "stage_source"}, nil
		},
		reconcile: func() error { t.Error("canceled inspection reconciled"); return nil },
		absent:    func() error { t.Error("canceled inspection became absence"); return nil },
		close:     func() error { close(closing); <-release; return nil },
	}
	done := make(chan error, 1)
	go func() {
		result := errors.New("callback exited before returning")
		defer func() { done <- result }()
		resolution, err := reconcileClusterSSHPreparationSourceStage(ctx, target, func(context.Context, clusterremote.Target, clusterremote.HostKey, *clusterremote.Credential) (clusterSSHPreparationRecoverySourceStageClient, error) {
			return client, nil
		})
		if resolution != "" {
			result = errors.New("canceled result escaped")
			return
		}
		result = err
	}()
	<-started
	cancel()
	<-closing
	select {
	case <-done:
		t.Fatal("returned before cleanup joined")
	default:
	}
	once.Do(func() { close(release) })
	if err := <-done; err == nil {
		t.Fatal("canceled source proof accepted")
	}
}

func TestClusterSSHPreparationRecoverySourceStageIgnoredAuthorityLoss(t *testing.T) {
	baseline, manifest, members := recoveryProgressArchiveFixture(t)
	archive := recoveryProgressPack(t, members)
	value, _, _, _, _ := recoverySourceFixture(t, false)
	value.Baseline = baseline
	expected, err := buildClusterSSHPreparationRecoveryExpected(value, 0, clusterbootstrap.K3sPins().Version, "10.42.0.0/16", "10.43.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	item := value.Cohort.Targets[0]
	sealed := sealedClusterSSHCredentials{binding: item.Binding, generation: "synthetic", ciphertext: aegisEnvelopePrefix + "synthetic"}
	denied := false
	aegis := sshPreparationProposalTestAegis{verify: func(ctx context.Context, _ string) error {
		if denied {
			return errClusterConflict
		}
		return ctx.Err()
	}, open: func(_ context.Context, _ sealedClusterSSHCredentials, b clusterSSHCredentialBinding) (clusterSSHCredentialEnvelope, error) {
		return clusterSSHCredentialEnvelope{Version: 1, Binding: b, Method: "password", Username: "root", Password: "synthetic"}, nil
	}}
	source := func(ctx context.Context) (clusterSSHPreparationRecoverySource, error) {
		return clusterSSHPreparationRecoverySource{Expected: []clusterbootstrap.PreparationExpected{expected, expected}, Settings: sshPreparationRuntimeFixture()}, ctx.Err()
	}
	err = withClusterSSHPreparationRecoveryTargetInputs(context.Background(), value, 0, sealed, aegis, t.TempDir(), source,
		func(ctx context.Context) error { return ctx.Err() }, func(ctx context.Context, e clusterbootstrap.Expected, parent string) (*clusterbootstrap.Bundle, error) {
			return recoveryProgressStageFixture(t, ctx, e, manifest, archive, parent)
		}, nil, func(target clusterSSHPreparationRecoveryTargetInputs) error {
			denied = true
			if target.current(context.Background()) == nil {
				t.Fatal("lost Aegis accepted")
			}
			denied = false
			if target.current(context.Background()) == nil {
				t.Fatal("ignored authority failure recovered")
			}
			return nil
		})
	if err == nil {
		t.Fatal("ignored failed authority produced evidence")
	}
}
