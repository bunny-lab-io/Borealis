package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

type recoveryProgressClient struct {
	inspect func(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, []byte, func(context.Context) error) (clusterbootstrap.PreparationProgress, error)
	close   func() error
}

func (c *recoveryProgressClient) InspectPreparationProgress(ctx context.Context, p *clusterbootstrap.PreparationInputs, e clusterbootstrap.PreparationExpected, sudo []byte, check func(context.Context) error) (clusterbootstrap.PreparationProgress, error) {
	return c.inspect(ctx, p, e, sudo, check)
}
func (c *recoveryProgressClient) Close() error { return c.close() }

func TestClusterSSHPreparationRecoveryProgressTargetCustody(t *testing.T) {
	baseline, manifest, members := recoveryProgressArchiveFixture(t)
	archive := recoveryProgressPack(t, members)
	for _, mode := range []string{"success", "password", "download error", "owned download error", "nil download", "helper drift", "archive drift", "changed runtime", "substituted target", "connect error", "owned connect error", "inspect error", "invalid cursor", "authority after cursor", "SSH close", "input close", "bad envelope", "late source drift"} {
		t.Run(mode, func(t *testing.T) {
			value, _, _, _, _ := recoverySourceFixture(t, false)
			value.Baseline = baseline
			expected, err := buildClusterSSHPreparationRecoveryExpected(value, 0, clusterbootstrap.K3sPins().Version, "10.42.0.0/16", "10.43.0.0/16")
			if err != nil {
				t.Fatal(err)
			}
			settings := sshPreparationRuntimeFixture()
			item := value.Cohort.Targets[0]
			sealed := sealedClusterSSHCredentials{binding: item.Binding, generation: "synthetic", ciphertext: aegisEnvelopePrefix + "synthetic"}
			denied := false
			check := func(ctx context.Context) error {
				if denied {
					return errClusterConflict
				}
				return ctx.Err()
			}
			source := func(ctx context.Context) (clusterSSHPreparationRecoverySource, error) {
				values := []clusterbootstrap.PreparationExpected{expected, expected}
				if mode == "substituted target" {
					values[0].Target.TargetID = newClusterUUID()
				}
				copied := map[string]string{}
				for k, v := range settings {
					copied[k] = v
				}
				return clusterSSHPreparationRecoverySource{Expected: values, Settings: copied}, ctx.Err()
			}
			aegis := sshPreparationProposalTestAegis{verify: func(ctx context.Context, _ string) error { return ctx.Err() }, open: func(ctx context.Context, _ sealedClusterSSHCredentials, binding clusterSSHCredentialBinding) (clusterSSHCredentialEnvelope, error) {
				envelope := clusterSSHCredentialEnvelope{Version: 1, Binding: binding, Method: "password", Username: "root", Password: "synthetic-password", SudoPassword: "synthetic-sudo"}
				if mode == "bad envelope" {
					envelope.Binding.TargetID = newClusterUUID()
				}
				return envelope, nil
			}}
			parent := t.TempDir()
			closed, inspected, cleaned := 0, 0, 0
			var staged *clusterbootstrap.Bundle
			deps := clusterSSHPreparationRecoveryProgressDependencies{}
			deps.download = func(ctx context.Context, e clusterbootstrap.Expected, scratch string) (*clusterbootstrap.Bundle, error) {
				if e != baseline {
					t.Fatal("source changed")
				}
				if mode == "download error" {
					return nil, errors.New("private download")
				}
				if mode == "nil download" {
					return nil, nil
				}
				b, err := recoveryProgressStageFixture(t, ctx, e, manifest, archive, scratch)
				staged = b
				if err == nil && mode == "helper drift" {
					err = os.WriteFile(b.ManagerPath(), []byte("changed"), 0700)
				}
				if err == nil && mode == "archive drift" {
					err = os.WriteFile(b.ArchivePath(), []byte("changed"), 0600)
				}
				if err == nil && mode == "owned download error" {
					err = errors.New("private download")
				}
				return b, err
			}
			deps.closeInputs = func(p *clusterbootstrap.PreparationInputs) error {
				cleaned++
				err := p.Close()
				if mode == "input close" {
					return errors.New("private cleanup")
				}
				return err
			}
			deps.connect = func(ctx context.Context, target clusterremote.Target, key clusterremote.HostKey, credential *clusterremote.Credential) (clusterSSHPreparationRecoveryProgressClient, error) {
				if target.Address != item.Binding.Address || target.Port != item.Binding.Port || !reflect.DeepEqual(key, item.Key) || credential == nil {
					t.Fatal("original pin/credential lost")
				}
				if mode == "connect error" {
					return nil, errors.New("private dial")
				}
				client := &recoveryProgressClient{close: func() error {
					closed++
					if mode == "SSH close" {
						return errors.New("private close")
					}
					return nil
				}, inspect: func(ctx context.Context, p *clusterbootstrap.PreparationInputs, e clusterbootstrap.PreparationExpected, sudo []byte, current func(context.Context) error) (clusterbootstrap.PreparationProgress, error) {
					inspected++
					if !reflect.DeepEqual(e, expected) || string(sudo) != "synthetic-sudo" || current(ctx) != nil {
						t.Fatal("request changed")
					}
					if _, err := p.Configuration(ctx, e, current); err != nil {
						t.Fatal("unverified configuration", err)
					}
					if _, err := p.SourceBundle(ctx, e, current); err != nil {
						t.Fatal("unverified bundle", err)
					}
					if mode == "inspect error" {
						return clusterbootstrap.PreparationProgress{Step: "stage_source"}, errors.New("private exit")
					}
					if mode == "invalid cursor" {
						return clusterbootstrap.PreparationProgress{Step: "completed"}, nil
					}
					if mode == "authority after cursor" {
						denied = true
					}
					if mode == "late source drift" {
						settings["BOREALIS_OPERATOR_SECRET"] = "changed"
					}
					return clusterbootstrap.PreparationProgress{Step: "stage_source"}, nil
				}}
				if mode == "changed runtime" {
					settings["BOREALIS_OPERATOR_SECRET"] = "changed"
				}
				if mode == "owned connect error" {
					return client, errors.New("private dial")
				}
				return client, nil
			}
			result, err := inspectClusterSSHPreparationRecoveryTarget(context.Background(), value, 0, sealed, aegis, parent, source, check, deps)
			valid := mode == "success" || mode == "password"
			if (err == nil) != valid || (!valid && result.Step != "") {
				t.Fatal("uncertain progress escaped", mode, result, err)
			}
			if valid && (result.Step != "stage_source" || closed != 1 || cleaned != 1 || inspected != 1) {
				t.Fatal("missing joined lifecycle")
			}
			entries, _ := os.ReadDir(parent)
			if len(entries) != 0 {
				t.Fatal("owned scratch retained")
			}
			if staged != nil {
				if _, err := os.Stat(filepath.Dir(staged.ArchivePath())); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("owned bundle not closed", err)
				}
			}
		})
	}
}

func TestClusterSSHPreparationRecoveryProgressJoinsCanceledClient(t *testing.T) {
	baseline, manifest, members := recoveryProgressArchiveFixture(t)
	archive := recoveryProgressPack(t, members)
	value, _, _, _, _ := recoverySourceFixture(t, false)
	value.Baseline = baseline
	expected, _ := buildClusterSSHPreparationRecoveryExpected(value, 0, clusterbootstrap.K3sPins().Version, "10.42.0.0/16", "10.43.0.0/16")
	item := value.Cohort.Targets[0]
	sealed := sealedClusterSSHCredentials{binding: item.Binding, generation: "synthetic", ciphertext: aegisEnvelopePrefix + "synthetic"}
	aegis := sshPreparationProposalTestAegis{verify: func(ctx context.Context, _ string) error { return ctx.Err() }, open: func(ctx context.Context, _ sealedClusterSSHCredentials, b clusterSSHCredentialBinding) (clusterSSHCredentialEnvelope, error) {
		return clusterSSHCredentialEnvelope{Version: 1, Binding: b, Method: "password", Username: "root", Password: "synthetic"}, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	started, closing, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	done := make(chan error, 1)
	parent := t.TempDir()
	deps := clusterSSHPreparationRecoveryProgressDependencies{download: func(ctx context.Context, e clusterbootstrap.Expected, s string) (*clusterbootstrap.Bundle, error) {
		return recoveryProgressStageFixture(t, ctx, e, manifest, archive, s)
	}, connect: func(context.Context, clusterremote.Target, clusterremote.HostKey, *clusterremote.Credential) (clusterSSHPreparationRecoveryProgressClient, error) {
		return &recoveryProgressClient{close: func() error { close(closing); <-release; return nil }, inspect: func(ctx context.Context, _ *clusterbootstrap.PreparationInputs, _ clusterbootstrap.PreparationExpected, _ []byte, _ func(context.Context) error) (clusterbootstrap.PreparationProgress, error) {
			close(started)
			<-ctx.Done()
			return clusterbootstrap.PreparationProgress{Step: "empty"}, nil
		}}, nil
	}}
	source := func(ctx context.Context) (clusterSSHPreparationRecoverySource, error) {
		return clusterSSHPreparationRecoverySource{Expected: []clusterbootstrap.PreparationExpected{expected, expected}, Settings: sshPreparationRuntimeFixture()}, ctx.Err()
	}
	go func() {
		result := errors.New("progress callback exited before returning")
		defer func() { done <- result }()
		_, err := inspectClusterSSHPreparationRecoveryTarget(ctx, value, 0, sealed, aegis, parent, source, func(ctx context.Context) error { return ctx.Err() }, deps)
		result = err
	}()
	joined := false
	defer func() {
		cancel()
		once.Do(func() { close(release) })
		if !joined {
			<-done
		}
	}()
	select {
	case <-started:
	case err := <-done:
		joined = true
		t.Fatal("entry", err)
	case <-ctx.Done():
		t.Fatal("entry timeout")
	}
	cancel()
	<-closing
	select {
	case <-done:
		joined = true
		t.Fatal("returned before SSH cleanup joined")
	default:
	}
	once.Do(func() { close(release) })
	err := <-done
	joined = true
	if err == nil {
		t.Fatal("cancellation became cursor")
	}
	entries, _ := os.ReadDir(parent)
	if len(entries) != 0 {
		t.Fatal("canceled scratch not closed")
	}
}
