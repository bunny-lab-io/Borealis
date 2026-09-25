package main

import (
	"borealis/api-backend/internal/clusterremote"
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func sshCapacityEvidence() clusterremote.FilesystemEvidence {
	return clusterremote.FilesystemEvidence{
		Paths:       []clusterremote.FilesystemPath{{Path: "/opt/Borealis", Ancestor: "/opt", Filesystem: "root"}, {Path: "/var/lib/longhorn", Ancestor: "/var/lib", Filesystem: "root"}},
		Filesystems: []clusterremote.FilesystemCapacity{{ID: "root", Type: "ext4", TotalBytes: 120 << 30, AvailableBytes: 90 << 30, AllocationUnit: 4096, TotalInodes: 1000000, AvailableInodes: 800000}},
	}
}

func TestClusterSSHStorageCapacitySharedFilesystem(t *testing.T) {
	f := newSSHStorageFixture(t, false)
	storage := sshStorageSnapshotFixture(t, f.a).Requirements
	demands := []clusterSSHFilesystemDemand{{Path: "/opt/Borealis", Bytes: 8 << 30}}
	for _, mode := range []string{"shared", "split", "insufficient", "strict boundary", "overprovision not physical credit", "existing storage", "missing path", "duplicate filesystem", "overflow", "extra filesystem", "reserve rounding"} {
		t.Run(mode, func(t *testing.T) {
			r := storage
			e := sshCapacityEvidence()
			d := append([]clusterSSHFilesystemDemand(nil), demands...)
			switch mode {
			case "split":
				e.Paths[0].Filesystem = "install"
				e.Filesystems = append(e.Filesystems, clusterremote.FilesystemCapacity{ID: "install", Type: "xfs", TotalBytes: 20 << 30, AvailableBytes: 9 << 30, AllocationUnit: 4096, TotalInodes: 1000000, AvailableInodes: 800000})
			case "insufficient":
				e.Filesystems[0].AvailableBytes = 80 << 30
			case "strict boundary":
				e.Filesystems[0].AvailableBytes = 82 << 30
			case "overprovision not physical credit":
				r.Policy.OverProvisioningPercent = 1000
				e.Filesystems[0].AvailableBytes = 70 << 30
			case "existing storage":
				e.Paths[1].Ancestor = e.Paths[1].Path
			case "missing path":
				e.Paths = e.Paths[:1]
			case "duplicate filesystem":
				e.Filesystems = append(e.Filesystems, e.Filesystems[0])
			case "overflow":
				d[0].Bytes = clusterSSHCapacityLimit
			case "extra filesystem":
				e.Filesystems = append(e.Filesystems, clusterremote.FilesystemCapacity{ID: "unused", Type: "ext4", TotalBytes: 1, AvailableBytes: 1, AllocationUnit: 4096, TotalInodes: 1000000, AvailableInodes: 800000})
			case "reserve rounding":
				e.Filesystems[0].TotalBytes++
			}
			before, _ := json.Marshal(e)
			budgets, err := calculateClusterSSHStorageCapacity(r, e, d)
			bad := mode == "existing storage" || mode == "missing path" || mode == "duplicate filesystem" || mode == "overflow" || mode == "extra filesystem"
			if (err != nil) != bad {
				t.Fatal("invalid capacity decision", err)
			}
			if bad {
				return
			}
			wantFits := mode == "shared" || mode == "split" || mode == "reserve rounding"
			if budgets[0].Fits != wantFits {
				t.Fatal("wrong fit", budgets)
			}
			if budgets[0].ReplicaBytes != 24<<30 || budgets[0].RebuildBytes != 20<<30 {
				t.Fatal("missing full replicas or rebuild headroom")
			}
			if mode == "split" {
				if len(budgets) != 2 || budgets[0].OtherBytes != 0 || budgets[1].OtherBytes != 8<<30 || !budgets[1].Fits {
					t.Fatal("separate mount misbudgeted")
				}
			} else if len(budgets) != 1 || budgets[0].OtherBytes != 8<<30 {
				t.Fatal("shared filesystem counted twice")
			}
			if mode == "reserve rounding" && budgets[0].MinimumFreeBytes != (30<<30)+1 {
				t.Fatal("headroom rounded down")
			}
			after, _ := json.Marshal(e)
			if string(before) != string(after) {
				t.Fatal("mutated evidence")
			}
		})
	}
}

func TestClusterSSHStorageCapacityAllocationAndInodes(t *testing.T) {
	f := newSSHStorageFixture(t, false)
	r := sshStorageSnapshotFixture(t, f.a).Requirements
	for _, mode := range []string{"shared", "large blocks", "inode equality", "inode exhausted", "entry overflow", "invalid unit", "invalid inode count"} {
		t.Run(mode, func(t *testing.T) {
			e := sshCapacityEvidence()
			e.Paths = append(e.Paths, clusterremote.FilesystemPath{Path: "/var/lib/rancher/k3s", Ancestor: "/var/lib", Filesystem: "root"})
			e.Filesystems[0].AvailableInodes = 11
			demands := []clusterSSHFilesystemDemand{{Path: "/opt/Borealis", Bytes: 100, Entries: 6}, {Path: "/var/lib/rancher/k3s", Bytes: 100, Entries: 4}}
			switch mode {
			case "large blocks":
				e.Filesystems[0].AllocationUnit = 65536
			case "inode equality":
				e.Filesystems[0].AvailableInodes = 10
			case "inode exhausted":
				e.Filesystems[0].AvailableInodes = 0
			case "entry overflow":
				demands[0].Entries = clusterSSHCapacityLimit
			case "invalid unit":
				e.Filesystems[0].AllocationUnit = 1000
			case "invalid inode count":
				e.Filesystems[0].TotalInodes = 1 << 53
			}
			budgets, err := calculateClusterSSHStorageCapacity(r, e, demands)
			good := mode == "shared" || mode == "large blocks"
			if (err == nil) != good {
				t.Fatalf("allocation/inode decision: %v", err)
			}
			if good && (len(budgets) != 1 || budgets[0].OtherBytes != 200+10*e.Filesystems[0].AllocationUnit || !budgets[0].Fits) {
				t.Fatal("shared allocation demand lost or double counted")
			}
		})
	}
}

func TestClusterSSHStorageCapacityNativeCohort(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "low space", "policy drift", "source lost", "consumer error", "consumer copy", "demand copy", "historical filesystem", "existing directory", "current-only wire", "downgraded reader", "persistent final drift"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHNetworkTargetsFixture(t, mode == "replacement", "filesystem")
			f.filesystemWire = func(i int, raw []byte) []byte {
				var wire struct {
					Version   int                              `json:"version"`
					MachineID string                           `json:"machine_id"`
					BootID    string                           `json:"boot_id"`
					Evidence  clusterremote.FilesystemEvidence `json:"evidence"`
				}
				_ = json.Unmarshal(raw, &wire)
				wire.Version = 4
				if mode == "current-only wire" || mode == "downgraded reader" {
					wire.Version = 3
				}
				if mode == "persistent final drift" && f.opens[i].Load() > 2 {
					wire.Evidence.Receipt = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
				}
				wire.Evidence.Filesystems[0].TotalBytes = 120 << 30
				wire.Evidence.Filesystems[0].AvailableBytes = 90 << 30
				if mode == "low space" && i == 0 {
					wire.Evidence.Filesystems[0].AvailableBytes = 80 << 30
				}
				if mode == "existing directory" {
					wire.Evidence.Paths[1].Ancestor = wire.Evidence.Paths[1].Path
				}
				out, _ := json.Marshal(wire)
				return out
			}
			expected, err := buildClusterSSHPreparationExpected(f.a.Cohort, f.a.Source, f.a.Lease, f.a.Baseline, f.a.K3sVersion, "10.42.0.0/16", "10.43.0.0/16")
			if err != nil {
				t.Fatal(err)
			}
			snapshot := clusterSSHPreparationSnapshot{Expected: expected, Storage: sshStorageSnapshotFixture(t, f.a)}
			demands := []clusterSSHFilesystemDemand{{Path: "/opt/Borealis", Bytes: 8 << 30}}
			reads, consumed := 0, false
			source := func(ctx context.Context) (clusterSSHPreparationSnapshot, error) {
				reads++
				value := snapshot
				value.Storage.Requirements.Policy.Classes = append([]clusterSSHStorageClass(nil), snapshot.Storage.Requirements.Policy.Classes...)
				if mode == "policy drift" && consumed {
					value.Storage.Requirements.Policy.MinimalAvailablePercent++
				}
				return value, ctx.Err()
			}
			err = runClusterSSHNetworkTargets(context.Background(), f.a.Baseline, f.claims, f.deps, func(ctx context.Context, readers clusterSSHNetworkTargetReaders) error {
				original := readers.Filesystem
				cached := map[string]clusterremote.TargetFilesystem{}
				readers.Filesystem = func(ctx context.Context, item clusterSSHInspectedTarget, r clusterremote.FilesystemRequest) (clusterremote.TargetFilesystem, error) {
					if !r.RequirePersistent {
						t.Fatal("capacity accepted current-only request")
					}
					if mode == "downgraded reader" {
						r.RequirePersistent = false
					}
					if mode == "historical filesystem" {
						if value, ok := cached[item.Binding.TargetID]; ok {
							return value, nil
						}
					}
					v, err := original(ctx, item, r)
					cached[item.Binding.TargetID] = v
					if mode == "demand copy" {
						demands[0].Bytes = 1
					}
					return v, err
				}
				return withClusterSSHTargetStorageCapacity(ctx, readers, source, demands, func(ctx context.Context, out []clusterSSHTargetStorageCapacity) error {
					consumed = true
					if len(out) != len(f.claims) {
						t.Fatal("partial cohort")
					}
					for i, v := range out {
						want := !(mode == "low space" && i == 0)
						if v.TargetID != f.claims[i].Lease.TargetID || v.Fits != want || len(v.Budgets) != 1 || v.Budgets[0].OtherBytes != 8<<30 {
							t.Fatal("wrong native capacity plan")
						}
					}
					switch mode {
					case "source lost":
						f.lost.Store(2)
					case "consumer error":
						return errors.New("private error")
					case "consumer copy":
						out[0].Budgets[0].OtherBytes = 1
					}
					return nil
				})
			})
			good := mode == "expansion" || mode == "replacement" || mode == "low space" || mode == "consumer copy" || mode == "demand copy"
			if (err == nil) != good {
				t.Fatal("unsafe capacity scope", err)
			}
			if good && (!consumed || reads < 3) {
				t.Fatal("missing source/consumer checks")
			}
			f.assertClosed(t)
		})
	}
}
