package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestClusterSSHTargetSizingOriginalNativeCohort(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "smaller than source", "consumer copy", "CPU low", "sibling RAM low", "cap too large", "cap missing", "source error", "source binding", "source drift early", "source drift before consume", "source drift after consume", "source drift final", "source alias", "claim lost", "host drift", "cancel source", "cancel consumer", "consumer error", "ignored failure", "nil source", "nil consumer", "partial cohort"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHNetworkTargetsFixture(t, mode == "replacement", "peer")
			settings := sshPreparationRuntimeFixture()
			settings["BOREALIS_CLUSTER_SIZING_RANK"] = "1"
			settings["BOREALIS_CLUSTER_SIZING_MEMORY_MIB"] = "16384"
			settings["BOREALIS_POSTGRES_DB_MEMORY_LIMIT"] = "4g"
			if mode == "smaller than source" {
				settings["BOREALIS_CLUSTER_SIZING_MEMORY_MIB"] = "131072"
			}
			if mode == "cap too large" {
				settings["BOREALIS_POSTGRES_DB_MEMORY_LIMIT"] = "33g"
			}
			if mode == "cap missing" {
				delete(settings, "BOREALIS_POSTGRES_DB_MEMORY_LIMIT")
			}
			if mode == "partial cohort" {
				f.claims = f.claims[:1]
			}
			// Authority's historical capacity must not replace native readings.
			for i := range f.a.Cohort.Targets {
				f.a.Cohort.Targets[i].Report.CPUCount = 32
			}
			consumed := false
			f.hostWire = func(i int, wire string) string {
				if mode == "CPU low" && i == 0 {
					return strings.Replace(wire, "cpu_count=16", "cpu_count=7", 1)
				}
				if mode == "sibling RAM low" && i == 1 {
					return strings.Replace(wire, "memory_kib=33554432", "memory_kib=16777215", 1)
				}
				if mode == "host drift" && consumed && i == 1 {
					return strings.Replace(wire, "cpu_count=16", "cpu_count=24", 1)
				}
				return wire
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reads := 0
			var source clusterSSHPreparationRead = func(ctx context.Context) (clusterbootstrap.PreparationExpected, map[string]string, error) {
				reads++
				e, err := buildClusterSSHPreparationExpected(f.a.Cohort, f.a.Source, f.a.Lease, f.a.Baseline, f.a.K3sVersion, f.sources[0].PodCIDR, f.sources[0].ServiceCIDR)
				if err != nil {
					return e, nil, err
				}
				if mode == "source binding" {
					e.Target.HolderID = newClusterUUID()
				}
				if mode == "source error" {
					return e, nil, errors.New("private source error")
				}
				if (mode == "source drift early" || mode == "ignored failure" || mode == "source alias") && reads == 2 ||
					mode == "source drift before consume" && reads == 3 || mode == "source drift after consume" && consumed ||
					mode == "source drift final" && f.opens[1].Load() == 3 {
					// Mutate the very map returned by the first callback. A reader
					// retaining this alias instead of owned bytes would miss drift.
					settings["BOREALIS_POSTGRES_DB_MEMORY_LIMIT"] = "5g"
				}
				if mode == "cancel source" {
					cancel()
					select {
					case <-ctx.Done():
					case <-time.After(time.Second):
						t.Fatal("source escaped original scope cancellation")
					}
				}
				return e, settings, nil
			}
			if mode == "nil source" {
				source = nil
			}
			consume := func(ctx context.Context, targets []clusterSSHTargetHostEvidence) error {
				consumed = true
				if len(targets) != len(f.claims) {
					t.Fatal("incomplete capacity decision")
				}
				for i, target := range targets {
					if target.TargetID != f.claims[i].Lease.TargetID || target.Host.CPUCount != 16 || target.Host.MemoryKiB != 33554432 {
						t.Fatal("historical or reordered capacity decision")
					}
				}
				switch mode {
				case "consumer copy":
					targets[0].TargetID, targets[0].Host.CPUCount = "changed", 1
				case "claim lost":
					f.lost.Store(2)
				case "cancel consumer":
					cancel()
					select {
					case <-ctx.Done():
					case <-time.After(time.Second):
						t.Fatal("consumer escaped original scope cancellation")
					}
				case "consumer error":
					return errors.New("private consumer error")
				}
				return nil
			}
			if mode == "nil consumer" {
				consume = nil
			}
			err := runClusterSSHNetworkTargets(ctx, f.a.Baseline, f.claims, f.deps, func(ctx context.Context, readers clusterSSHNetworkTargetReaders) error {
				caller := ctx
				if mode == "cancel source" || mode == "cancel consumer" {
					caller = context.Background()
				}
				err := withClusterSSHTargetSizing(caller, readers, source, consume)
				if mode == "ignored failure" {
					return nil
				}
				return err
			})
			wantSuccess := mode == "expansion" || mode == "replacement" || mode == "smaller than source" || mode == "consumer copy"
			if (err == nil) != wantSuccess {
				t.Fatal("unsafe sizing decision", err)
			}
			afterConsumer := wantSuccess || mode == "source drift after consume" || mode == "source drift final" || mode == "claim lost" || mode == "host drift" || mode == "cancel consumer" || mode == "consumer error"
			if consumed != afterConsumer {
				t.Fatal("wrong consumer boundary")
			}
			if wantSuccess && reads < 5 {
				t.Fatal("source not reobserved around host acquisition and consumer")
			}
			f.assertClosed(t)
		})
	}
}

func TestClusterSSHTargetSizingRequiresActiveScope(t *testing.T) {
	called := false
	consume := func(context.Context, []clusterSSHTargetHostEvidence) error { called = true; return nil }
	if withClusterSSHTargetSizing(context.Background(), clusterSSHNetworkTargetReaders{}, nil, consume) == nil || called {
		t.Fatal("sizing outside original claim scope")
	}
	f := newSSHNetworkTargetsFixture(t, false, "peer")
	var escaped clusterSSHNetworkTargetReaders
	if err := runClusterSSHNetworkTargets(context.Background(), f.a.Baseline, f.claims, f.deps, func(_ context.Context, readers clusterSSHNetworkTargetReaders) error {
		escaped = readers
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	source := func(context.Context) (clusterbootstrap.PreparationExpected, map[string]string, error) {
		called = true
		return clusterbootstrap.PreparationExpected{}, nil, nil
	}
	if withClusterSSHTargetSizing(context.Background(), escaped, source, consume) == nil || called {
		t.Fatal("source work escaped completed original scope")
	}
}
