package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type sshApprovalArtifactBytes struct {
	proves, closes atomic.Int64
	digest         string
	onProve        func(context.Context) error
	closeErr       error
	onClose        func() error
}

func (v *sshApprovalArtifactBytes) prove(ctx context.Context, check func(context.Context) error) (string, error) {
	v.proves.Add(1)
	if check(ctx) != nil {
		return "", errClusterConflict
	}
	if v.onProve != nil {
		if err := v.onProve(ctx); err != nil {
			return "", err
		}
	}
	if check(ctx) != nil || ctx.Err() != nil {
		return "", errClusterConflict
	}
	return v.digest, nil
}
func (v *sshApprovalArtifactBytes) Close() error {
	v.closes.Add(1)
	if v.onClose != nil {
		return errors.Join(v.closeErr, v.onClose())
	}
	return v.closeErr
}

func sshApprovalArtifactDependencies(v *sshApprovalArtifactBytes) clusterSSHPreparationArtifactDependencies {
	check := func(ctx context.Context) error { return ctx.Err() }
	return clusterSSHPreparationArtifactDependencies{
		proposal: check,
		source: func(ctx context.Context, check func(context.Context) error) (clusterSSHPreparationArtifactSource, error) {
			return clusterSSHPreparationArtifactSource{strings.Repeat("b", 64), clusterbootstrap.PostgresImageRepository + "@sha256:" + strings.Repeat("c", 64)}, check(ctx)
		},
		publication: func(ctx context.Context, check func(context.Context) error) (string, error) {
			return strings.Repeat("d", 64), check(ctx)
		},
		acquire: func(ctx context.Context, _ string, check func(context.Context) error) (clusterSSHPreparationArtifactBytes, error) {
			return v, check(ctx)
		},
		terminal: func(clusterSSHPreparationReadinessReceipt, string) (func(context.Context) error, error) {
			return check, nil
		},
	}
}

func TestClusterSSHPreparationArtifactsScope(t *testing.T) {
	for _, mode := range []string{"valid", "copied capability", "cancelled", "proposal lost", "tampered bytes", "ignored gate failure", "consume failure", "cleanup failure", "acquisition failure", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "timeout" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 5*time.Millisecond)
				defer stop()
			}
			v := &sshApprovalArtifactBytes{digest: strings.Repeat("e", 64)}
			d := sshApprovalArtifactDependencies(v)
			var lost atomic.Bool
			d.proposal = func(ctx context.Context) error {
				if lost.Load() {
					return errClusterConflict
				}
				return ctx.Err()
			}
			d.terminal = func(clusterSSHPreparationReadinessReceipt, string) (func(context.Context) error, error) {
				return d.proposal, nil
			}
			if mode == "acquisition failure" {
				d.acquire = func(context.Context, string, func(context.Context) error) (clusterSSHPreparationArtifactBytes, error) {
					return v, errClusterConflict
				}
			}
			if mode == "timeout" {
				d.acquire = func(ctx context.Context, _ string, _ func(context.Context) error) (clusterSSHPreparationArtifactBytes, error) {
					<-ctx.Done()
					return v, ctx.Err()
				}
			}
			plan := strings.Repeat("a", 64)
			var escaped clusterSSHPreparationArtifactGate
			consumed := false
			err := runClusterSSHPreparationArtifacts(ctx, plan, d, func(ctx context.Context, gate clusterSSHPreparationArtifactGate) (clusterSSHPreparationReadinessReceipt, error) {
				consumed = true
				escaped = gate
				proof, err := gate(ctx, plan)
				if err != nil || !validClusterSSHPreparationDigest(proof) {
					t.Fatal("initial gate", err)
				}
				if mode == "copied capability" {
					copied := gate
					again, err := copied(context.Background(), plan)
					if err != nil || again != proof {
						t.Fatal("copied live capability", err)
					}
				}
				switch mode {
				case "cancelled":
					cancel()
				case "proposal lost":
					lost.Store(true)
				case "tampered bytes":
					v.digest = strings.Repeat("f", 64)
				case "ignored gate failure":
					_, _ = gate(ctx, strings.Repeat("f", 64))
				case "consume failure":
					return clusterSSHPreparationReadinessReceipt{}, errClusterConflict
				case "cleanup failure":
					v.closeErr = errClusterConflict
				}
				return clusterSSHPreparationReadinessReceipt{}, nil
			})
			valid := mode == "valid" || mode == "copied capability"
			if (err == nil) != valid || v.closes.Load() != 1 {
				t.Fatal(mode, err, "closes", v.closes.Load())
			}
			if (mode == "acquisition failure" || mode == "timeout") && consumed {
				t.Fatal("failed acquisition exposed gate")
			}
			if escaped != nil {
				before := v.proves.Load()
				if _, err := escaped(context.Background(), plan); err == nil || v.proves.Load() != before {
					t.Fatal("escaped capability used artifacts")
				}
			}
		})
	}
}

// Acquisition retains its existing artifact ceiling and live proposal checks;
// only owned-byte approval starts the unchanged short proof lifetime.
func TestClusterSSHPreparationArtifactsSeparateAcquisitionLifetime(t *testing.T) {
	v := &sshApprovalArtifactBytes{digest: strings.Repeat("e", 64)}
	d := sshApprovalArtifactDependencies(v)
	d.acquire = func(ctx context.Context, _ string, check func(context.Context) error) (clusterSSHPreparationArtifactBytes, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) <= 30*time.Second {
			t.Fatal("bulk acquisition consumed approval proof lifetime")
		}
		return v, check(ctx)
	}
	err := runClusterSSHPreparationArtifacts(context.Background(), strings.Repeat("a", 64), d, func(ctx context.Context, gate clusterSSHPreparationArtifactGate) (clusterSSHPreparationReadinessReceipt, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 30*time.Second {
			t.Fatal("approval proof lifetime changed")
		}
		_, err := gate(ctx, strings.Repeat("a", 64))
		return clusterSSHPreparationReadinessReceipt{}, err
	})
	if err != nil || v.closes.Load() != 1 {
		t.Fatal("separate acquisition lifetime", err)
	}
}

func TestClusterSSHPreparationArtifactsCleanupKeepsProofDeadline(t *testing.T) {
	v := &sshApprovalArtifactBytes{digest: strings.Repeat("e", 64)}
	var proof context.Context
	v.onProve = func(ctx context.Context) error { proof = ctx; return ctx.Err() }
	v.onClose = func() error {
		if proof == nil || proof.Err() != nil {
			t.Error("proof context ended before cleanup")
			return errClusterConflict
		}
		return nil
	}
	err := runClusterSSHPreparationArtifacts(context.Background(), strings.Repeat("a", 64), sshApprovalArtifactDependencies(v), func(context.Context, clusterSSHPreparationArtifactGate) (clusterSSHPreparationReadinessReceipt, error) {
		return clusterSSHPreparationReadinessReceipt{}, nil
	})
	if err != nil || v.closes.Load() != 1 || proof == nil || proof.Err() == nil {
		t.Fatal("cleanup proof lifetime", err)
	}
}

func TestClusterSSHPreparationArtifactsAcquiredPublication(t *testing.T) {
	for _, family := range []string{"bootstrap", "application", "external", "K3s"} {
		t.Run(family, func(t *testing.T) {
			f := preparationPublicationFixture(t)
			ctx := bootstrapContext()
			_, bootstrap, err := readClusterSSHBootstrapPublication(ctx, f.expected)
			if err != nil {
				t.Fatal(err)
			}
			application, err := resolveClusterSSHImageInventory(ctx, f.expected)
			if err != nil {
				t.Fatal(err)
			}
			external, err := resolveClusterSSHExternalInventory(ctx, f.expected)
			if err != nil {
				t.Fatal(err)
			}
			k3s, err := resolveClusterSSHK3sInventory(ctx, f.expected, clusterbootstrap.K3sPins().Version)
			if err != nil {
				t.Fatal(err)
			}
			groups := map[string]map[string]clusterBootstrapAsset{"bootstrap": bootstrap, "application": application.assets, "external": external.assets, "K3s": k3s.assets}
			check, err := clusterSSHPreparationArtifactPublication(f.expected, bootstrap, application.assets, external.assets, k3s.assets)
			if err != nil || check(ctx) != nil {
				t.Fatal("acquired publication", err)
			}
			var chosen clusterBootstrapAsset
			for _, asset := range groups[family] {
				chosen = asset
				break
			}
			// Caller aliases cannot retarget the retained acquired generation.
			changed := chosen
			changed.Digest = "sha256:" + strings.Repeat("f", 64)
			groups[family][chosen.Name] = changed
			if check(ctx) != nil {
				t.Fatal("caller map changed retained publication")
			}
			for i := range f.release.Assets {
				if f.release.Assets[i].Name == chosen.Name {
					f.release.Assets[i] = changed
				}
			}
			if check(ctx) == nil {
				t.Fatal("published generation no longer matches acquired bytes")
			}
		})
	}
}

func TestClusterSSHPreparationArtifactsDriftAndJoinedCancellation(t *testing.T) {
	for _, mode := range []string{"source", "publication", "pending reader"} {
		t.Run(mode, func(t *testing.T) {
			v := &sshApprovalArtifactBytes{digest: strings.Repeat("e", 64)}
			d := sshApprovalArtifactDependencies(v)
			var drift atomic.Bool
			source, publication := d.source, d.publication
			d.source = func(ctx context.Context, check func(context.Context) error) (clusterSSHPreparationArtifactSource, error) {
				value, err := source(ctx, check)
				if mode == "source" && drift.Load() {
					value.Observation = strings.Repeat("f", 64)
				}
				return value, err
			}
			d.publication = func(ctx context.Context, check func(context.Context) error) (string, error) {
				value, err := publication(ctx, check)
				if mode == "publication" && drift.Load() {
					value = strings.Repeat("f", 64)
				}
				return value, err
			}
			started, done := make(chan struct{}), make(chan struct{})
			plan := strings.Repeat("a", 64)
			err := runClusterSSHPreparationArtifacts(context.Background(), plan, d, func(ctx context.Context, gate clusterSSHPreparationArtifactGate) (clusterSSHPreparationReadinessReceipt, error) {
				if mode != "pending reader" {
					drift.Store(true)
					return clusterSSHPreparationReadinessReceipt{}, nil
				}
				v.onProve = func(ctx context.Context) error { close(started); <-ctx.Done(); return ctx.Err() }
				go func() { defer close(done); _, _ = gate(context.Background(), plan) }()
				<-started
				return clusterSSHPreparationReadinessReceipt{}, nil
			})
			if err == nil || v.closes.Load() != 1 {
				t.Fatal("drift/reader escaped cleanup", err)
			}
			if mode == "pending reader" {
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("reader not joined")
				}
			}
		})
	}
}

func TestClusterSSHPreparationArtifactsNativeSource(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		t.Run(map[bool]string{false: "pair", true: "replacement"}[replacement], func(t *testing.T) {
			f := newSSHStorageFixture(t, replacement)
			check := func(ctx context.Context) error { return ctx.Err() }
			first, err := readClusterSSHPreparationArtifactSource(context.Background(), f.a.Source, f.get, check)
			if err != nil || !validClusterSSHPreparationDigest(first.Observation) || !clusterbootstrap.ValidPostgresImageReference(first.Postgres) {
				t.Fatal("native source", err)
			}
			second, err := readClusterSSHPreparationArtifactSource(context.Background(), f.a.Source, f.get, check)
			if err != nil || first != second {
				t.Fatal("stable source changed", err)
			}
			f.objects[clusterSSHStoragePostgresPath]["metadata"].(map[string]any)["resourceVersion"] = "changed"
			changed, err := readClusterSSHPreparationArtifactSource(context.Background(), f.a.Source, f.get, check)
			if err != nil || changed == first {
				t.Fatal("source revision not bound", err)
			}
			if _, err := readClusterSSHPreparationArtifactSource(context.Background(), f.a.Source, f.get, func(context.Context) error { return errClusterConflict }); err == nil {
				t.Fatal("lost authority accepted")
			}
		})
	}
}

func TestClusterSSHPreparationArtifactsPostgresTerminal(t *testing.T) {
	for _, mode := range []string{"waiting", "exact commit", "different payload", "wrong phase", "wrong attempt", "lease lost", "expired lease", "claimed target", "changed source", "changed credential", "different readiness", "failed consume", "failed consume after commit", "cleanup failure"} {
		t.Run(mode, func(t *testing.T) {
			f, snapshot, confirmation, _ := newSSHPreparationApprovalFixture(t, false)
			f.c.store.db.SetMaxOpenConns(1)
			plan, err := clusterSSHPreparationPlanDigest(snapshot.work.Authorization)
			if err != nil {
				t.Fatal(err)
			}
			v := &sshApprovalArtifactBytes{digest: strings.Repeat("e", 64)}
			d := sshApprovalArtifactDependencies(v)
			d.proposal = func(ctx context.Context) error {
				if f.c.store.db.Stats().InUse != 0 {
					t.Error("artifact observation retained SQL connection")
				}
				current, _, err := f.c.loadClusterSSHPreparationApproval(ctx, f.op)
				if err != nil || !reflect.DeepEqual(current, snapshot) {
					return errClusterConflict
				}
				return ctx.Err()
			}
			d.terminal = func(receipt clusterSSHPreparationReadinessReceipt, artifact string) (func(context.Context) error, error) {
				if receipt.Version == 0 {
					return d.proposal, nil
				}
				return f.c.clusterSSHPreparationArtifactTerminal(snapshot, receipt, artifact)
			}
			var approvedPayload string
			err = runClusterSSHPreparationArtifacts(f.ctx, plan, d, func(ctx context.Context, gate clusterSSHPreparationArtifactGate) (clusterSSHPreparationReadinessReceipt, error) {
				artifact, err := gate(ctx, plan)
				if err != nil {
					t.Fatal("gate", err)
				}
				if mode == "waiting" {
					return clusterSSHPreparationReadinessReceipt{}, nil
				}
				if mode == "failed consume" {
					return clusterSSHPreparationReadinessReceipt{}, errors.New("consumer failed")
				}
				var receipts [6]string
				for i := range receipts {
					receipts[i] = strings.Repeat("a", 64)
				}
				receipts[0] = artifact
				proof := clusterSSHPreparationApprovalProof{receipts: receipts, plan: plan, started: time.Now(), lifetime: ctx, recheck: func(ctx context.Context) error {
					actual, err := gate(ctx, plan)
					if err != nil || actual != artifact {
						return errClusterConflict
					}
					return nil
				}}
				if err := f.c.commitClusterSSHPreparationApproval(ctx, snapshot, confirmation, proof); err != nil {
					t.Fatal("exact approval", err)
				}
				if err := f.c.store.db.QueryRowContext(ctx, `SELECT payload_json FROM engine.cluster_operations WHERE id=$1`, f.op.ID).Scan(&approvedPayload); err != nil {
					t.Fatal(err)
				}
				if mode == "failed consume after commit" {
					return clusterSSHPreparationReadinessReceipt{}, errors.New("consumer failed after commit")
				}
				switch mode {
				case "different payload":
					f.exec(t, `UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=$1`, f.op.ID)
				case "wrong phase":
					f.exec(t, `UPDATE engine.cluster_operations SET current_step='other_phase' WHERE id=$1`, f.op.ID)
				case "wrong attempt":
					f.exec(t, `UPDATE engine.cluster_operations SET attempt=attempt+1 WHERE id=$1`, f.op.ID)
				case "lease lost":
					f.exec(t, `UPDATE engine.cluster_application_leases SET holder='other-controller' WHERE name=$1`, clusterControllerLeaseName)
				case "expired lease":
					f.exec(t, `UPDATE engine.cluster_application_leases SET expires_at=0 WHERE name=$1`, clusterControllerLeaseName)
				case "claimed target":
					f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_holder='other-worker' WHERE operation_id=$1`, f.op.ID)
				case "changed source":
					f.exec(t, `UPDATE engine.cluster_state SET desired_size=desired_size+2 WHERE id=1`)
				case "changed credential":
					f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, snapshot.work.Proof.Cohort.Targets[0].Binding.TargetID)
				case "different readiness":
					receipts[1] = strings.Repeat("b", 64)
				case "cleanup failure":
					v.closeErr = errClusterConflict
				}
				return clusterSSHPreparationReadiness(plan, receipts), nil
			})
			if (err == nil) != (mode == "waiting" || mode == "exact commit") || v.closes.Load() != 1 {
				t.Fatal(mode, err, "cleanup", v.closes.Load())
			}
			if f.c.store.db.Stats().InUse != 0 {
				t.Fatal("terminal verifier retained connection")
			}
			var state, payload string
			if err := f.c.store.db.QueryRowContext(f.ctx, `SELECT state,payload_json FROM engine.cluster_operations WHERE id=$1`, f.op.ID).Scan(&state, &payload); err != nil {
				t.Fatal(err)
			}
			waiting := mode == "waiting" || mode == "failed consume"
			if (state == "waiting") != waiting {
				t.Fatal("scope changed durable operation", state)
			}
			if !waiting && mode != "different payload" && payload != approvedPayload {
				t.Fatal("scope changed durable approved payload")
			}
		})
	}
}
