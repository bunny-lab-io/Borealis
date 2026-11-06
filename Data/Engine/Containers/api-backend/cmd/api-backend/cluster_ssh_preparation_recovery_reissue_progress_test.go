package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func reissuedProgressFixture(t *testing.T, replacement bool, rounds int, baseline clusterbootstrap.Expected) (
	clusterSSHPreparationRecoveryReissuedProofAuthority, string, []sealedClusterSSHCredentials, map[string]any,
	func(context.Context, string, any) error, clusterSSHSourceNetworkRead) {
	t.Helper()
	value, _, secret, get, network := reissuedSourceFixture(t, replacement, 0)
	value.Baseline = baseline
	var sealed []sealedClusterSSHCredentials
	for i, item := range value.Cohort.Targets {
		credential := sealedClusterSSHCredentials{binding: item.Binding, generation: "synthetic", ciphertext: aegisEnvelopePrefix + "synthetic"}
		sealed = append(sealed, credential)
		var err error
		value.Authorization.Targets[i].CredentialSHA256, err = clusterSSHPreparationDigest([]string{credential.generation, credential.ciphertext})
		if err != nil {
			t.Fatal(err)
		}
	}
	plan, err := clusterSSHPreparationPlanDigest(value.Authorization)
	if err != nil {
		t.Fatal(err)
	}
	proof := clusterSSHParentProof{Version: 1, Cohort: value.Cohort, Source: value.Source}
	work := clusterSSHPreparationWork{Authorization: value.Authorization, Baseline: baseline, Proof: proof,
		Payload: marshalClusterJSON(map[string]any{"source_k3s_version": clusterbootstrap.K3sPins().Version})}
	approved, err := newClusterSSHPreparationConfigurationRead(work, func(ctx context.Context) error { return ctx.Err() }, get, network)(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	var receipts [6]string
	for i := range receipts {
		receipts[i] = strings.Repeat("a", 64)
	}
	receipts[4] = approved
	payload := marshalClusterJSON(map[string]any{"source_k3s_version": clusterbootstrap.K3sPins().Version,
		"ssh_preparation_readiness": clusterSSHPreparationReadiness(plan, receipts)})
	base, _, err := clusterSSHPreparationRecoveryPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	value.Lineage.Original.PlanSHA256, value.Lineage.Original.PayloadSHA256 = plan, clusterSSHPreparationRecoveryPayloadDigest(base)
	payload = sshRecoveryPayload(t, payload, value.Lineage.Original)
	for range rounds {
		value.Lineage = appendReissueProofFixture(t, value.Lineage, payload)
		entry := &value.Lineage.Requests[len(value.Lineage.Requests)-1]
		for i := range entry.Targets {
			entry.Targets[i].PreviousHolder = value.Leases[i].Holder
			value.Leases[i].Holder, value.Leases[i].Generation = entry.Holder, entry.Targets[i].Generation
			value.ExpiresAt[value.Leases[i].TargetID] = 1000
		}
		payload = reissueProofPayload(t, payload, value.Lineage)
	}
	return value, payload, sealed, secret, get, network
}

func reissuedProgressAegis() sshPreparationProposalTestAegis {
	return sshPreparationProposalTestAegis{verify: func(ctx context.Context, generation string) error {
		if generation != "synthetic" {
			return errClusterConflict
		}
		return ctx.Err()
	}, open: func(ctx context.Context, sealed sealedClusterSSHCredentials, binding clusterSSHCredentialBinding) (clusterSSHCredentialEnvelope, error) {
		if sealed.binding != binding || sealed.generation != "synthetic" || sealed.ciphertext != aegisEnvelopePrefix+"synthetic" {
			return clusterSSHCredentialEnvelope{}, errClusterConflict
		}
		return clusterSSHCredentialEnvelope{Version: 1, Binding: binding, Method: "password", Username: "root", Password: "synthetic-password", SudoPassword: "synthetic-sudo"}, ctx.Err()
	}}
}

func TestClusterSSHPreparationRecoveryReissuedProgressRejectsForeignInputs(t *testing.T) {
	baseline, _ := recoveryProgressManifestFixture()
	for _, mode := range []string{"negative index", "past index", "Q2 lineage", "old generation", "future generation", "holder", "missing lease", "inspection", "raw payload", "credential binding", "credential generation", "credential ciphertext", "substituted Expected", "missing Expected"} {
		t.Run(mode, func(t *testing.T) {
			value, payload, sealed, _, get, network := reissuedProgressFixture(t, false, 2, baseline)
			index := 0
			switch mode {
			case "negative index":
				index = -1
			case "past index":
				index = len(value.Leases)
			case "Q2 lineage":
				value.Lineage.Requests = nil
			case "old generation":
				value.Leases[0].Generation--
			case "future generation":
				value.Leases[0].Generation++
			case "holder":
				value.Leases[0].Holder = newClusterUUID()
			case "missing lease":
				value.Leases = value.Leases[:1]
			case "inspection":
				value.Cohort.Targets[0].Report.BootID = newClusterUUID()
			case "raw payload":
				payload = `{}`
			case "credential binding":
				sealed[0].binding.TargetID = newClusterUUID()
			case "credential generation":
				sealed[0].generation += "changed"
			case "credential ciphertext":
				sealed[0].ciphertext += "changed"
			}
			calls := 0
			check := func(ctx context.Context) error { return ctx.Err() }
			original := newClusterSSHPreparationRecoveryReissuedSourceRead(value, payload, check, get, network)
			source := func(ctx context.Context) (clusterSSHPreparationRecoverySource, error) {
				current, err := original(ctx)
				if err == nil && mode == "substituted Expected" {
					current.Expected[0].Target.Generation++
				}
				if err == nil && mode == "missing Expected" {
					current.Expected = current.Expected[:1]
				}
				return current, err
			}
			aegis := reissuedProgressAegis()
			aegis.open = func(context.Context, sealedClusterSSHCredentials, clusterSSHCredentialBinding) (clusterSSHCredentialEnvelope, error) {
				calls++
				return clusterSSHCredentialEnvelope{}, errClusterConflict
			}
			deps := clusterSSHPreparationRecoveryReissuedProgressDependencies{download: func(context.Context, clusterbootstrap.Expected, string) (*clusterbootstrap.Bundle, error) {
				calls++
				return nil, errClusterConflict
			}, connect: func(context.Context, clusterremote.Target, clusterremote.HostKey, *clusterremote.Credential) (clusterSSHPreparationRecoveryProgressClient, error) {
				calls++
				return nil, errClusterConflict
			}}
			progress, err := inspectClusterSSHPreparationRecoveryReissuedTarget(context.Background(), value, payload, index, sealed[0], aegis, t.TempDir(), source, check, deps)
			if err == nil || progress != (clusterbootstrap.PreparationProgress{}) || calls != 0 {
				t.Fatal("foreign typed inputs reached artifact or credential work")
			}
		})
	}
}

func TestClusterSSHPreparationRecoveryReissuedProgressTargetCustody(t *testing.T) {
	baseline, manifest, members := recoveryProgressArchiveFixture(t)
	archive := recoveryProgressPackWithCompressionLevel(t, members, gzip.NoCompression)
	for _, mode := range []string{"Q3 pair", "Q3 replacement", "Q4 pair", "Q4 replacement", "SSH close", "input close", "late source", "late authority", "owned connect failure"} {
		t.Run(mode, func(t *testing.T) {
			rounds := 1
			if strings.HasPrefix(mode, "Q4") {
				rounds = 2
			}
			value, payload, sealed, secret, get, network := reissuedProgressFixture(t, strings.Contains(mode, "replacement"), rounds, baseline)
			denied := false
			check := func(ctx context.Context) error {
				if denied {
					return errClusterConflict
				}
				return ctx.Err()
			}
			read := newClusterSSHPreparationRecoveryReissuedSourceRead(value, payload, check, get, network)
			index := len(value.Leases) - 1
			item, lease := value.Cohort.Targets[index], value.Leases[index]
			closed, inspected, cleaned := 0, 0, 0
			parent := t.TempDir()
			deps := clusterSSHPreparationRecoveryReissuedProgressDependencies{download: func(ctx context.Context, e clusterbootstrap.Expected, scratch string) (*clusterbootstrap.Bundle, error) {
				return recoveryProgressStageFixture(t, ctx, e, manifest, archive, scratch)
			}, closeInputs: func(inputs *clusterbootstrap.PreparationInputs) error {
				cleaned++
				err := inputs.Close()
				if mode == "input close" {
					return errors.New("private cleanup")
				}
				return err
			}}
			deps.connect = func(ctx context.Context, target clusterremote.Target, key clusterremote.HostKey, credential *clusterremote.Credential) (clusterSSHPreparationRecoveryProgressClient, error) {
				if target.Address != item.Binding.Address || target.Port != item.Binding.Port || !reflect.DeepEqual(key, item.Key) || credential == nil {
					t.Fatal("original target pin or credential lost")
				}
				client := &recoveryProgressClient{close: func() error {
					closed++
					switch mode {
					case "SSH close":
						return errors.New("private close")
					case "late source":
						secret["metadata"].(map[string]any)["resourceVersion"] = "changed"
					case "late authority":
						denied = true
					}
					return nil
				}, inspect: func(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected, sudo []byte, current func(context.Context) error) (clusterbootstrap.PreparationProgress, error) {
					inspected++
					if expected.Target.Generation != lease.Generation || expected.Target.HolderID != lease.Holder || expected.Target.TargetID != lease.TargetID ||
						string(sudo) != "synthetic-sudo" || current(ctx) != nil {
						t.Fatal("reissued tip or credential changed")
					}
					if _, err := inputs.Configuration(ctx, expected, current); err != nil {
						t.Fatal("original configuration unavailable", err)
					}
					if _, err := inputs.SourceBundle(ctx, expected, current); err != nil {
						t.Fatal("original bundle unavailable", err)
					}
					return clusterbootstrap.PreparationProgress{Step: "stage_source"}, nil
				}}
				if mode == "owned connect failure" {
					return client, errors.New("private dial")
				}
				return client, nil
			}
			progress, err := inspectClusterSSHPreparationRecoveryReissuedTarget(context.Background(), value, payload, index, sealed[index], reissuedProgressAegis(), parent, read, check, deps)
			valid := strings.HasPrefix(mode, "Q")
			if (err == nil) != valid || !valid && progress != (clusterbootstrap.PreparationProgress{}) || valid && progress.Step != "stage_source" {
				t.Fatal("uncertain cursor escaped", err)
			}
			wantInspected := 1
			if mode == "owned connect failure" {
				wantInspected = 0
			}
			if closed != 1 || cleaned != 1 || inspected != wantInspected {
				t.Fatal("unjoined target lifecycle", closed, cleaned, inspected)
			}
			if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
				t.Fatal("owned scratch retained", err)
			}
		})
	}
}

func TestClusterSSHPreparationRecoveryReissuedProgressJoinsCancellation(t *testing.T) {
	baseline, manifest, members := recoveryProgressArchiveFixture(t)
	archive := recoveryProgressPackWithCompressionLevel(t, members, gzip.NoCompression)
	value, payload, sealed, _, get, network := reissuedProgressFixture(t, false, 2, baseline)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	check := func(ctx context.Context) error { return ctx.Err() }
	read := newClusterSSHPreparationRecoveryReissuedSourceRead(value, payload, check, get, network)
	started, closing, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	var once sync.Once
	joined := false
	defer func() {
		cancel()
		once.Do(func() { close(release) })
		if !joined {
			<-done
		}
	}()
	parent := t.TempDir()
	deps := clusterSSHPreparationRecoveryReissuedProgressDependencies{download: func(ctx context.Context, e clusterbootstrap.Expected, scratch string) (*clusterbootstrap.Bundle, error) {
		return recoveryProgressStageFixture(t, ctx, e, manifest, archive, scratch)
	}, connect: func(context.Context, clusterremote.Target, clusterremote.HostKey, *clusterremote.Credential) (clusterSSHPreparationRecoveryProgressClient, error) {
		return &recoveryProgressClient{close: func() error { close(closing); <-release; return nil }, inspect: func(ctx context.Context, _ *clusterbootstrap.PreparationInputs, _ clusterbootstrap.PreparationExpected, _ []byte, _ func(context.Context) error) (clusterbootstrap.PreparationProgress, error) {
			close(started)
			<-ctx.Done()
			return clusterbootstrap.PreparationProgress{Step: "empty"}, nil
		}}, nil
	}}
	go func() {
		result := errors.New("progress callback exited before returning")
		defer func() { done <- result }()
		_, err := inspectClusterSSHPreparationRecoveryReissuedTarget(ctx, value, payload, 0, sealed[0], reissuedProgressAegis(), parent, read, check, deps)
		result = err
	}()
	select {
	case <-started:
	case err := <-done:
		joined = true
		t.Fatal("inspection failed before controlled cancellation", err)
	case <-ctx.Done():
		t.Fatal("inspection entry timeout")
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
	if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
		t.Fatal("canceled scratch retained", err)
	}
}

func TestClusterSSHPreparationRecoveryReissuedProgressConsumesFailedHandle(t *testing.T) {
	// Callable composition must burn the shared one-use latch even when a copied
	// handle fails before SQL entry. SQL consumer tests exercise valid entry.
	issued := reissuedProofClaimFixture(t)
	copied := issued
	issued.Claims = append([]clusterSSHPreparationRecoveryTargetClaim(nil), issued.Claims...)
	issued.Claims[0].Lease.Generation++
	var controller *clusterController
	got, err := controller.observeClusterSSHPreparationRecoveryReissuedProgressWith(context.Background(), nil, issued, t.TempDir(), clusterSSHPreparationRecoveryReissuedProgressDependencies{})
	if err == nil || len(got) != 0 || !copied.custody.started.Load() || !copied.custody.failed.Load() {
		t.Fatal("failed copied handle preserved custody")
	}
	if got, err := controller.observeClusterSSHPreparationRecoveryReissuedProgressWith(context.Background(), nil, copied, t.TempDir(), clusterSSHPreparationRecoveryReissuedProgressDependencies{}); err == nil || len(got) != 0 {
		t.Fatal("copied handle resumed failed proof")
	}
}
