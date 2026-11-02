package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func recoveryProgressClaimFixture(t *testing.T, replacement bool, baseline clusterbootstrap.Expected) (sshPreparationAuthorityFixture, clusterSSHPreparationRecoveryClaim, map[string]any) {
	t.Helper()
	f, authorization := newSSHPreparationClaimFixture(t, replacement)
	f.baseline = baseline
	f.op.Payload["baseline_sha"] = baseline.SourceSHA
	f.exec(t, `UPDATE engine.cluster_state SET baseline_sha=$1 WHERE id=1`, baseline.SourceSHA)
	raw := marshalClusterJSON(f.op.Payload)
	proof, _, err := parseClusterSSHPreparationInspection([]byte(raw), f.lease, baseline)
	if err != nil {
		t.Fatal("source fixture proof", err)
	}
	source, err := readClusterSSHQueueSource(f.ctx, f.c.store.db)
	if err != nil {
		t.Fatal(err)
	}
	authorization.SourceSHA256, _ = clusterSSHPreparationDigest(source)
	authorization.InspectionSHA256, _ = clusterSSHPreparationDigest(proof)
	f.op.Payload["ssh_preparation_confirmation"] = sshPreparationConfirmationFixture(t, &authorization, proof.Source)
	f.op.Payload["ssh_preparation_authorization"] = authorization
	plan, _ := clusterSSHPreparationPlanDigest(authorization)
	secret := sshPreparationConfigurationSecret()
	secretRaw, _ := json.Marshal(secret)
	originalSecret, err := clusterbootstrap.ParsePreparationRuntimeSecret(secretRaw)
	if err != nil {
		t.Fatal(err)
	}
	var sources []clusterbootstrap.SourceNetwork
	for _, member := range proof.Source.Members {
		sources = append(sources, sshSourceNetworkFixture(member, clusterbootstrap.K3sPins().Version))
	}
	receipt, err := clusterSSHPreparationConfigurationReceipt(baseline, proof, plan, clusterbootstrap.K3sPins().Version, originalSecret, sources)
	if err != nil {
		t.Fatal(err)
	}
	var receipts [6]string
	for i := range receipts {
		receipts[i] = strings.Repeat("a", 64)
	}
	receipts[4] = receipt
	f.op.Payload["ssh_preparation_readiness"] = clusterSSHPreparationReadiness(plan, receipts)
	f.exec(t, `UPDATE engine.cluster_operations SET payload_json=$2 WHERE id=$1`, f.op.ID, marshalClusterJSON(f.op.Payload))
	if _, err := f.c.claimClusterSSHPreparation(f.ctx, f.op, newClusterUUID()); err != nil {
		t.Fatal("initial actual claim", err)
	}
	f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE operation_id=$1`, f.op.ID)
	record, err := f.c.reserveClusterSSHPreparationRecovery(f.ctx, f.op)
	if err != nil {
		t.Fatal("recovery reserve", err)
	}
	f.op.CurrentStep = clusterSSHPreparationRecoveryOperationStep
	claim, err := f.c.claimClusterSSHPreparationRecovery(f.ctx, f.op, record.ID, newClusterUUID())
	if err != nil {
		t.Fatal("issued recovery", err)
	}
	return f, claim, secret
}

func recoveryProgressSQLPoolAvailable(ctx context.Context, f sshPreparationAuthorityFixture) error {
	bound, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	connection, err := f.c.store.db.Conn(bound)
	if err != nil {
		return errors.New("native IO could not acquire released SQL connection")
	}
	return connection.Close()
}

// Admission setup keeps its original 15-second context. Transport exercises
// verify real source/helper bytes across multiple sessions, so each gets a
// separate bounded context without extending any stored lease or credential.
func recoveryTransportSQLExercise(t *testing.T, f sshPreparationAuthorityFixture) sshPreparationAuthorityFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(f.ctx), 90*time.Second)
	t.Cleanup(cancel)
	f.ctx = ctx
	return f
}

func recoveryProgressSQLSource(t *testing.T, f sshPreparationAuthorityFixture, secret map[string]any) func(clusterSSHPreparationRecoveryAuthority, string, func(context.Context) error) clusterSSHPreparationRecoverySourceRead {
	t.Helper()

	return func(value clusterSSHPreparationRecoveryAuthority, payload string, check func(context.Context) error) clusterSSHPreparationRecoverySourceRead {
		get := func(ctx context.Context, path string, out any) error {
			if err := recoveryProgressSQLPoolAvailable(ctx, f); err != nil {
				return err
			}
			var object any
			switch path {
			case clusterSSHRuntimeSecretPath:
				object = secret
			case "/api/v1/namespaces/kube-system":
				object = map[string]any{"metadata": map[string]any{"uid": value.Source.KubeSystemUID}}
			case "/api/v1/nodes":
				var items []any
				for _, member := range value.Source.Members {
					items = append(items, sshSourceKubernetesFixture(member))
				}
				object = map[string]any{"items": items}
			default:
				return errClusterConflict
			}
			raw, _ := json.Marshal(object)
			return json.Unmarshal(raw, out)
		}
		network := func(ctx context.Context, member clusterSSHSourceMember) (clusterbootstrap.SourceNetwork, error) {
			if err := recoveryProgressSQLPoolAvailable(ctx, f); err != nil {
				return clusterbootstrap.SourceNetwork{}, err
			}
			return sshSourceNetworkFixture(member, clusterbootstrap.K3sPins().Version), ctx.Err()
		}
		return newClusterSSHPreparationRecoverySourceRead(value, payload, check, get, network)
	}
}

func TestClusterSSHPreparationRecoveryProgressPostgresCustody(t *testing.T) {
	baseline, manifest, members := recoveryProgressArchiveFixture(t)
	archive := recoveryProgressPack(t, members)
	for _, mode := range []string{"pair", "replacement", "second target failure", "SSH cleanup", "input cleanup", "final Secret changed", "credential changed", "controller changed", "source changed", "expired claim", "unissued claim", "initial claim"} {
		t.Run(mode, func(t *testing.T) {
			f, claim, secret := recoveryProgressClaimFixture(t, mode == "replacement", baseline)
			f = recoveryTransportSQLExercise(t, f)
			f.c.store.db.SetMaxOpenConns(1)
			parent := t.TempDir()
			inspected, closed, cleaned := 0, 0, 0
			var escaped func(context.Context) error
			deps := clusterSSHPreparationRecoveryProgressDependencies{source: recoveryProgressSQLSource(t, f, secret), download: func(ctx context.Context, e clusterbootstrap.Expected, s string) (*clusterbootstrap.Bundle, error) {
				if err := recoveryProgressSQLPoolAvailable(ctx, f); err != nil {
					return nil, err
				}
				return recoveryProgressStageFixture(t, ctx, e, manifest, archive, s)
			}}
			deps.closeInputs = func(p *clusterbootstrap.PreparationInputs) error {
				cleaned++
				err := p.Close()
				if mode == "input cleanup" && cleaned == len(claim.Claims) {
					return errors.New("synthetic cleanup")
				}
				return err
			}
			deps.connect = func(ctx context.Context, target clusterremote.Target, key clusterremote.HostKey, credential *clusterremote.Credential) (clusterSSHPreparationRecoveryProgressClient, error) {
				if err := recoveryProgressSQLPoolAvailable(ctx, f); err != nil {
					return nil, err
				}
				if credential == nil {
					t.Fatal("missing real decrypted credential")
				}
				return &recoveryProgressClient{close: func() error {
					closed++
					if closed == len(claim.Claims) {
						switch mode {
						case "SSH cleanup":
							return errors.New("synthetic close")
						case "final Secret changed":
							secret["metadata"].(map[string]any)["resourceVersion"] = "changed"
						}
					}
					return nil
				}, inspect: func(ctx context.Context, p *clusterbootstrap.PreparationInputs, e clusterbootstrap.PreparationExpected, _ []byte, check func(context.Context) error) (clusterbootstrap.PreparationProgress, error) {
					if err := recoveryProgressSQLPoolAvailable(ctx, f); err != nil {
						return clusterbootstrap.PreparationProgress{}, err
					}
					inspected++
					escaped = check
					if e.Target.Generation != claim.Claims[inspected-1].Lease.Generation || e.Target.HolderID != claim.Claims[inspected-1].Lease.Holder {
						t.Fatal("issued Q+2 lineage changed")
					}
					if _, err := p.Configuration(ctx, e, check); err != nil {
						t.Fatal("current real configuration", err)
					}
					if _, err := p.SourceBundle(ctx, e, check); err != nil {
						t.Fatal("current real helper", err)
					}
					if inspected == len(claim.Claims) {
						switch mode {
						case "second target failure":
							return clusterbootstrap.PreparationProgress{Step: "stage_source"}, errors.New("synthetic EOF/exit")
						case "credential changed":
							f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, claim.Claims[0].Lease.TargetID)
						case "controller changed":
							f.exec(t, `UPDATE engine.cluster_application_leases SET holder='changed' WHERE name=$1`, clusterControllerLeaseName)
						case "source changed":
							f.exec(t, `UPDATE engine.cluster_state SET baseline_sha=$1 WHERE id=1`, strings.Repeat("c", 40))
						}
					}
					return clusterbootstrap.PreparationProgress{Step: "stage_source"}, nil
				}}, nil
			}
			switch mode {
			case "expired claim":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE operation_id=$1`, f.op.ID)
			case "unissued claim":
				claim.custody = nil
			case "initial claim":
				claim.Claims[0].Lease.OperationStep = clusterSSHPreparationOperationStep
				claim.Claims[0].Lease.Step = "stage_source"
			}
			events := f.events(t)
			got, err := f.c.observeClusterSSHPreparationRecoveryProgressWith(f.ctx, f.aegis, claim, parent, deps)
			// Assertions inspect the joined outcome even if the exercise expired.
			assertCtx, stopAssertions := context.WithTimeout(context.WithoutCancel(f.ctx), 5*time.Second)
			defer stopAssertions()
			f.ctx = assertCtx
			valid := mode == "pair" || mode == "replacement"
			if (err == nil) != valid || (!valid && len(got) != 0) {
				t.Fatal("uncertain/partial cursor escaped", mode, got, err)
			}
			if valid && (len(got) != len(claim.Claims) || inspected != len(got) || closed != len(got) || cleaned != len(got)) {
				t.Fatal("incomplete cohort lifecycle")
			}
			if escaped != nil && escaped(context.Background()) == nil {
				t.Fatal("authority escaped closed proof scope")
			}
			if entries, _ := os.ReadDir(parent); len(entries) != 0 {
				t.Fatal("owned scratch leak")
			}
			if events != f.events(t) || f.c.store.db.Stats().InUse != 0 {
				t.Fatal("observation emitted transition/retained connection")
			}
			var state, step string
			if f.c.store.db.QueryRowContext(f.ctx, `SELECT state,current_step FROM engine.cluster_operations WHERE id=$1`, f.op.ID).Scan(&state, &step) != nil || state != "running" || step != clusterSSHPreparationRecoveryOperationStep {
				t.Fatal("progress settled inert recovery")
			}
			if _, err := f.c.observeClusterSSHPreparationRecoveryProgressWith(f.ctx, f.aegis, claim, parent, deps); err == nil {
				t.Fatal("cursor reused issued claim")
			}
		})
	}
}

func TestClusterSSHPreparationRecoveryProgressPostgresJoinsCancellation(t *testing.T) {
	baseline, manifest, members := recoveryProgressArchiveFixture(t)
	archive := recoveryProgressPack(t, members)
	f, claim, secret := recoveryProgressClaimFixture(t, false, baseline)
	f.c.store.db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
	defer cancel()
	started, closing, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	done := make(chan error, 1)
	var escaped func(context.Context) error
	parent := t.TempDir()
	deps := clusterSSHPreparationRecoveryProgressDependencies{source: recoveryProgressSQLSource(t, f, secret), download: func(ctx context.Context, e clusterbootstrap.Expected, s string) (*clusterbootstrap.Bundle, error) {
		if err := recoveryProgressSQLPoolAvailable(ctx, f); err != nil {
			return nil, err
		}
		return recoveryProgressStageFixture(t, ctx, e, manifest, archive, s)
	}, connect: func(context.Context, clusterremote.Target, clusterremote.HostKey, *clusterremote.Credential) (clusterSSHPreparationRecoveryProgressClient, error) {
		return &recoveryProgressClient{close: func() error { close(closing); <-release; return nil }, inspect: func(ctx context.Context, _ *clusterbootstrap.PreparationInputs, _ clusterbootstrap.PreparationExpected, _ []byte, check func(context.Context) error) (clusterbootstrap.PreparationProgress, error) {
			if err := recoveryProgressSQLPoolAvailable(ctx, f); err != nil {
				return clusterbootstrap.PreparationProgress{}, err
			}
			escaped = check
			close(started)
			<-ctx.Done()
			_ = check(context.Background())
			return clusterbootstrap.PreparationProgress{Step: "empty"}, nil
		}}, nil
	}}
	go func() {
		result := errors.New("progress callback exited before returning")
		defer func() { done <- result }()
		got, err := f.c.observeClusterSSHPreparationRecoveryProgressWith(ctx, f.aegis, claim, parent, deps)
		if len(got) != 0 {
			result = errors.New("canceled cursor exposed")
			return
		}
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
		t.Fatal("returned before owned cleanup")
	default:
	}
	once.Do(func() { close(release) })
	err := <-done
	joined = true
	if err == nil || escaped(context.Background()) == nil || f.c.store.db.Stats().InUse != 0 {
		t.Fatal("cancellation custody failed", err)
	}
	if entries, _ := os.ReadDir(parent); len(entries) != 0 {
		t.Fatal("canceled scratch leak")
	}
}
