package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"errors"
	"reflect"
	"slices"
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
	onDemand       func(context.Context, func(context.Context) error) ([]clusterSSHFilesystemDemand, error)
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

func (v *sshApprovalArtifactBytes) storageDemand(ctx context.Context, check func(context.Context) error) ([]clusterSSHFilesystemDemand, error) {
	if v.onDemand != nil {
		return v.onDemand(ctx, check)
	}
	return nil, clusterbootstrap.ErrPreparationConfig
}

func TestClusterSSHPreparationArtifactsDemandConsumer(t *testing.T) {
	for _, mode := range []string{"valid", "wrong plan", "failed bytes", "source drift"} {
		t.Run(mode, func(t *testing.T) {
			v := &sshApprovalArtifactBytes{digest: strings.Repeat("e", 64)}
			d := sshApprovalArtifactDependencies(v)
			var drift atomic.Bool
			originalSource := d.source
			d.source = func(ctx context.Context, check func(context.Context) error) (clusterSSHPreparationArtifactSource, error) {
				value, err := originalSource(ctx, check)
				if drift.Load() {
					value.Observation = strings.Repeat("f", 64)
				}
				return value, err
			}
			plan := strings.Repeat("a", 64)
			measured := []clusterSSHFilesystemDemand{{Path: "/opt/Borealis", Bytes: 17, Entries: 2}}
			calls := 0
			v.onDemand = func(ctx context.Context, check func(context.Context) error) ([]clusterSSHFilesystemDemand, error) {
				calls++
				if mode == "failed bytes" {
					return nil, errClusterConflict
				}
				return measured, check(ctx)
			}
			var escaped clusterSSHPreparationArtifactDemandRead
			err := runClusterSSHPreparationArtifacts(context.Background(), plan, d,
				func(ctx context.Context, gate clusterSSHPreparationArtifactGate, read clusterSSHPreparationArtifactDemandRead) (clusterSSHPreparationReadinessReceipt, error) {
					escaped = read
					requested := plan
					if mode == "wrong plan" {
						requested = strings.Repeat("f", 64)
					}
					if mode == "source drift" {
						drift.Store(true)
					}
					value, readErr := read(ctx, requested)
					if mode == "valid" {
						if readErr != nil || !reflect.DeepEqual(value, measured) {
							t.Fatal("owned demand unavailable", value, readErr)
						}
						value[0].Bytes++
						again, againErr := read(ctx, requested)
						if againErr != nil || !reflect.DeepEqual(again, measured) {
							t.Fatal("consumer rewrote retained demand", again, againErr)
						}
					} else if readErr == nil || value != nil {
						t.Fatal("invalid demand read succeeded", value, readErr)
					}
					// Ignoring a failed read cannot make outer approval succeed.
					return clusterSSHPreparationReadinessReceipt{}, nil
				})
			if (err == nil) != (mode == "valid") || v.closes.Load() != 1 {
				t.Fatal("demand lifetime", mode, err, v.closes.Load())
			}
			before := calls
			if value, err := escaped(context.Background(), plan); err == nil || value != nil || calls != before {
				t.Fatal("escaped demand read reused closed ownership", value, err)
			}
		})
	}
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
			err := runClusterSSHPreparationArtifacts(ctx, plan, d, func(ctx context.Context, gate clusterSSHPreparationArtifactGate, _ clusterSSHPreparationArtifactDemandRead) (clusterSSHPreparationReadinessReceipt, error) {
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
	err := runClusterSSHPreparationArtifacts(context.Background(), strings.Repeat("a", 64), d, func(ctx context.Context, gate clusterSSHPreparationArtifactGate, _ clusterSSHPreparationArtifactDemandRead) (clusterSSHPreparationReadinessReceipt, error) {
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
	err := runClusterSSHPreparationArtifacts(context.Background(), strings.Repeat("a", 64), sshApprovalArtifactDependencies(v), func(context.Context, clusterSSHPreparationArtifactGate, clusterSSHPreparationArtifactDemandRead) (clusterSSHPreparationReadinessReceipt, error) {
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

func TestClusterSSHPreparationArtifactsMeasuredDemand(t *testing.T) {
	f := preparationPublicationFixture(t)
	ctx := bootstrapContext()
	application, err := resolveClusterSSHImageInventory(ctx, f.expected)
	if err != nil {
		t.Fatal(err)
	}
	external, err := resolveClusterSSHExternalInventory(ctx, f.expected)
	if err != nil {
		t.Fatal(err)
	}
	version := clusterbootstrap.K3sPins().Version
	k3s, err := resolveClusterSSHK3sInventory(ctx, f.expected, version)
	if err != nil {
		t.Fatal(err)
	}
	pg := clusterbootstrap.PostgresImageDemand{Reference: sshPostgresResolvedFixture, IncomingBytes: 100, IncomingEntries: 1,
		RuntimeBytes: 1000, RuntimeEntries: 10, WorkerPeakBytes: 190, WorkerPeakEntries: 5}
	const managerBytes int64 = 12345
	demand, err := clusterSSHPreparationArtifactDemand(f.expected, version, pg.Reference, application, external, k3s, managerBytes, pg)
	if err != nil {
		t.Fatal(err)
	}
	appDemand, _ := application.demands()
	externalDemand, _ := external.demands()
	k3sDemand, _ := k3s.demands()
	base, err := mergeClusterSSHFilesystemDemands(appDemand, externalDemand, k3sDemand)
	if err != nil || len(demand) != len(base)+1 {
		t.Fatal("artifact families changed", err)
	}
	for i, value := range base {
		want := value
		switch value.Path {
		case "/opt/Borealis":
			want.Bytes += 100 + uint64(managerBytes)
			want.Entries += 4
		case "/var/lib/rancher/k3s":
			want.Bytes += 1000
			want.Entries += 10
		}
		if demand[i] != want {
			t.Fatal("target demand lost existing retained-generation/entry accounting or includes worker scratch", demand[i], want)
		}
	}
	if demand[len(demand)-1] != (clusterSSHFilesystemDemand{Path: "/var/tmp", Bytes: uint64(managerBytes), Entries: 2}) {
		t.Fatal("SSH helper upload/root copy escaped their own filesystem budget", demand)
	}
	larger, err := clusterSSHPreparationArtifactDemand(f.expected, version, pg.Reference, application, external, k3s, managerBytes+1, pg)
	if err != nil || len(larger) != len(demand) {
		t.Fatal("helper measurement unavailable", larger, err)
	}
	for i, want := range demand {
		if want.Path == "/opt/Borealis" || want.Path == "/var/tmp" {
			want.Bytes++
		}
		if larger[i] != want {
			t.Fatal("helper measurement changed source/runtime charge or used a guessed reserve", larger[i], want)
		}
	}
	for _, mode := range []string{"application source", "external source", "K3s source", "K3s version", "PostgreSQL image", "missing measurement", "wrong archive count", "overflow", "missing helper", "negative helper", "oversized helper"} {
		t.Run(mode, func(t *testing.T) {
			app, ext, k, p := application, external, k3s, pg
			manager := managerBytes
			switch mode {
			case "application source":
				app.expected.SourceSHA = strings.Repeat("f", 40)
			case "external source":
				ext.expected.SourceSHA = strings.Repeat("f", 40)
			case "K3s source":
				k.expected.SourceSHA = strings.Repeat("f", 40)
			case "K3s version":
				k.version = "v0.0.0+k3s1"
			case "PostgreSQL image":
				p.Reference = clusterbootstrap.PostgresImageRepository + "@sha256:" + strings.Repeat("f", 64)
			case "missing measurement":
				p.RuntimeEntries = 0
			case "wrong archive count":
				p.IncomingEntries = 2
			case "overflow":
				p.RuntimeBytes = clusterSSHCapacityLimit
			case "missing helper":
				manager = 0
			case "negative helper":
				manager = -1
			case "oversized helper":
				manager = clusterbootstrap.MaxBundleBytes + 1
			}
			if got, err := clusterSSHPreparationArtifactDemand(f.expected, version, pg.Reference, app, ext, k, manager, p); err == nil || len(got) != 0 {
				t.Fatal("invalid measured demand escaped", got, err)
			}
		})
	}
	if f.archiveReads != 0 {
		t.Fatal("demand calculation acquired another archive")
	}
}

func TestClusterSSHPreparationArtifactsDemandOwnedLifetime(t *testing.T) {
	for _, mode := range []string{"valid", "closed", "prior byte proof failure", "failed proof", "bad digest", "cancelled", "lost authority", "terminal authority loss", "empty demand"} {
		t.Run(mode, func(t *testing.T) {
			original := []clusterSSHFilesystemDemand{{Path: "/opt/Borealis", Bytes: 500, Entries: 2}}
			v := &clusterSSHOwnedPreparationArtifacts{demand: slices.Clone(original)}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			proves, checks := 0, 0
			check := func(ctx context.Context) error {
				checks++
				if mode == "lost authority" || mode == "terminal authority loss" && checks == 2 {
					return errClusterConflict
				}
				return ctx.Err()
			}
			proof := func(context.Context, func(context.Context) error) (string, error) {
				proves++
				if mode == "failed proof" {
					return "", errClusterConflict
				}
				if mode == "bad digest" {
					return "incomplete", nil
				}
				return strings.Repeat("a", 64), nil
			}
			switch mode {
			case "prior byte proof failure":
				if _, err := v.prove(ctx, check); err == nil {
					t.Fatal("missing owned bytes accepted")
				}
			case "closed":
				if err := v.Close(); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				cancel()
			case "empty demand":
				v.demand = nil
			}
			got, err := v.storageDemandWith(ctx, check, proof)
			if (err == nil) != (mode == "valid") || err != nil && len(got) != 0 {
				t.Fatal("owned demand result", got, err)
			}
			if mode == "valid" {
				if !slices.Equal(got, original) || proves != 1 || checks != 2 {
					t.Fatal("missing byte proof/authority or wrong demand", got, proves, checks)
				}
				got[0].Bytes, got[0].Path = 1, "/tmp"
				again, err := v.storageDemandWith(ctx, check, proof)
				if err != nil || !slices.Equal(again, original) {
					t.Fatal("caller rewrote retained demand", again, err)
				}
				if err := v.Close(); err != nil {
					t.Fatal(err)
				}
			}
			// A restored callback/context cannot resurrect failed or closed owner.
			before := proves
			if got, err := v.storageDemandWith(context.Background(), func(context.Context) error { return nil }, proof); err == nil || got != nil || proves != before {
				t.Fatal("failed owner lent demand", got, err)
			}
		})
	}
	for _, v := range []*clusterSSHOwnedPreparationArtifacts{nil, {demand: []clusterSSHFilesystemDemand{{Path: "/opt/Borealis", Bytes: 1, Entries: 1}}}} {
		if got, err := v.storageDemand(context.Background(), func(context.Context) error { return nil }); err == nil || got != nil {
			t.Fatal("production reader accepted missing owned artifacts", got, err)
		}
	}
}

func TestClusterSSHPreparationArtifactsDemandJoinsClose(t *testing.T) {
	v := &clusterSSHOwnedPreparationArtifacts{demand: []clusterSSHFilesystemDemand{{Path: "/opt/Borealis", Bytes: 500, Entries: 2}}}
	started, release, readDone, closeDone := make(chan struct{}), make(chan struct{}), make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := v.storageDemandWith(context.Background(), func(context.Context) error { return nil }, func(context.Context, func(context.Context) error) (string, error) {
			close(started)
			<-release
			return strings.Repeat("a", 64), nil
		})
		readDone <- err
	}()
	<-started
	go func() { closeDone <- v.Close() }()
	select {
	case err := <-closeDone:
		close(release)
		<-readDone
		t.Fatal("Close escaped active byte proof", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-readDone; err != nil {
		t.Fatal(err)
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	if got, err := v.storageDemand(context.Background(), func(context.Context) error { return nil }); err == nil || got != nil {
		t.Fatal("closed owner lent demand", got, err)
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
			err := runClusterSSHPreparationArtifacts(context.Background(), plan, d, func(ctx context.Context, gate clusterSSHPreparationArtifactGate, _ clusterSSHPreparationArtifactDemandRead) (clusterSSHPreparationReadinessReceipt, error) {
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
			err = runClusterSSHPreparationArtifacts(f.ctx, plan, d, func(ctx context.Context, gate clusterSSHPreparationArtifactGate, _ clusterSSHPreparationArtifactDemandRead) (clusterSSHPreparationReadinessReceipt, error) {
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
				sshPreparationApprovalCapacityProof(t, snapshot, &proof)
				receipts = proof.receipts
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
				readiness := clusterSSHPreparationReadiness(plan, receipts)
				readiness.Version, readiness.Targets, readiness.Capacity = 3, proof.targetCPU, proof.capacity
				return readiness, nil
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
