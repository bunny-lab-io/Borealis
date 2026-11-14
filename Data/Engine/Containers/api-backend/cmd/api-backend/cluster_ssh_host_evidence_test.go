package main

import (
	"borealis/api-backend/internal/clusterremote"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestClusterSSHHostEvidenceOriginalNativeCohort(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "CPU drift", "ignored failure", "background loss", "free drift", "free rises", "source lost", "cancel", "consumer error", "consumer copy", "key alias", "cached", "zero", "substituted"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHNetworkTargetsFixture(t, mode == "replacement", "peer")
			// Fresh native fixtures report 16 CPUs; historical inspection is
			// deliberately different. Never substitute the historical value.
			for i := range f.a.Cohort.Targets {
				f.a.Cohort.Targets[i].Report.CPUCount = 8
			}
			consumed := false
			f.hostWire = func(i int, wire string) string {
				if i != 0 {
					return wire
				}
				if (mode == "CPU drift" || mode == "ignored failure") && f.opens[i].Load() > 1 {
					return strings.Replace(wire, "cpu_count=16", "cpu_count=24", 1)
				}
				if mode == "free drift" && consumed {
					return strings.Replace(wire, "disk_free_kib=314572800", "disk_free_kib=1", 1)
				}
				if mode == "free rises" {
					if consumed {
						return strings.Replace(wire, "disk_free_kib=314572800", "disk_free_kib=400000000", 1)
					}
					if f.opens[i].Load() > 1 {
						return strings.Replace(wire, "disk_free_kib=314572800", "disk_free_kib=300000000", 1)
					}
				}
				return wire
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			err := runClusterSSHNetworkTargets(ctx, f.a.Baseline, f.claims, f.deps, func(ctx context.Context, readers clusterSSHNetworkTargetReaders) error {
				peer := readers.Peer
				cached := map[string]clusterremote.TargetManagementPeer{}
				readers.Peer = func(ctx context.Context, item clusterSSHInspectedTarget, peers []string) (clusterremote.TargetManagementPeer, error) {
					if mode == "zero" {
						return clusterremote.TargetManagementPeer{}, nil
					}
					if mode == "substituted" {
						item = f.a.Cohort.Targets[1]
						peers = clusterSSHNetworkTargetPeers(f.a, item.Binding.Address)
					}
					if value, ok := cached[item.Binding.TargetID]; mode == "cached" && ok {
						return value, nil
					}
					value, err := peer(ctx, item, peers)
					if mode == "key alias" {
						item.Key.PublicKey[0] ^= 1
						peers[0] = "192.168.90.247"
					}
					cached[item.Binding.TargetID] = value
					return value, err
				}
				caller := ctx
				if mode == "background loss" {
					caller = context.Background()
				}
				evidenceErr := withClusterSSHTargetHostEvidence(caller, readers, func(ctx context.Context, evidence []clusterSSHTargetHostEvidence) error {
					consumed = true
					if len(evidence) != len(f.claims) {
						t.Fatal("partial cohort")
					}
					for i, value := range evidence {
						if value.TargetID != f.claims[i].Lease.TargetID || value.Host.Hostname != f.a.Cohort.Targets[i].Report.Hostname || value.Host.CPUCount != 16 || value.Host.MemoryKiB != 33554432 {
							t.Fatal("historical, foreign or unordered hardware")
						}
					}
					if mode == "free rises" && evidence[0].Host.DiskFreeKiB != 300000000 {
						t.Fatal("not conservative across complete rounds")
					}
					switch mode {
					case "source lost":
						f.lost.Store(2)
					case "cancel":
						cancel()
					case "background loss":
						cancel()
						select {
						case <-ctx.Done():
						case <-time.After(time.Second):
							t.Fatal("consumer escaped original scope cancellation")
						}
					case "consumer error":
						return errors.New("private consumer error")
					case "consumer copy":
						evidence[0].Host.CPUCount = 1
						evidence[0].TargetID = "changed"
					}
					return nil
				})
				if mode == "ignored failure" {
					return nil
				}
				return evidenceErr
			})
			wantSuccess := mode == "expansion" || mode == "replacement" || mode == "consumer copy" || mode == "free rises" || mode == "key alias"
			if (err == nil) != wantSuccess {
				t.Fatal("unsafe host evidence scope", err)
			}
			beforeConsumer := mode == "CPU drift" || mode == "ignored failure" || mode == "cached" || mode == "zero" || mode == "substituted"
			if beforeConsumer && consumed || wantSuccess && !consumed {
				t.Fatal("consumer boundary")
			}
			f.assertClosed(t)
		})
	}
}
