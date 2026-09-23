package main

import (
	"borealis/api-backend/internal/clusterremote"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func sshFilesystemFixtureWire(item clusterSSHInspectedTarget) []byte {
	storage := clusterremote.FilesystemEvidence{MountNamespace: 21, Receipt: strings.Repeat("a", 64),
		Paths: []clusterremote.FilesystemPath{
			{Path: "/opt/Borealis", Ancestor: "/opt", Inode: 123, MountID: 29, MountRoot: "/", MountPoint: "/", Filesystem: "0000000000000001"},
			{Path: "/var/lib/longhorn", Ancestor: "/var/lib", Inode: 124, MountID: 29, MountRoot: "/", MountPoint: "/", Filesystem: "0000000000000001"},
		},
		Filesystems: []clusterremote.FilesystemCapacity{{ID: "0000000000000001", Device: "8:1", Type: "ext4", TotalBytes: 1000000, AvailableBytes: 700000}},
	}
	wire, _ := json.Marshal(struct {
		Version   int                              `json:"version"`
		MachineID string                           `json:"machine_id"`
		BootID    string                           `json:"boot_id"`
		Evidence  clusterremote.FilesystemEvidence `json:"evidence"`
	}{1, item.Report.MachineID, item.Report.BootID, storage})
	return wire
}

func TestClusterSSHFilesystemOriginalNativeCohort(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "mount drift", "ignored failure", "final drift", "lower final free", "free varies", "consumer copy", "input copy", "key copy", "cached", "zero", "substituted", "missing target", "wrong target", "unsorted paths", "source lost", "background loss", "consumer error", "late read"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHNetworkTargetsFixture(t, mode == "replacement", "filesystem")
			selection := make([]clusterSSHTargetFilesystemSelection, len(f.claims))
			for i, claim := range f.claims {
				selection[i] = clusterSSHTargetFilesystemSelection{TargetID: claim.Lease.TargetID, Paths: []string{"/opt/Borealis", "/var/lib/longhorn"}}
			}
			if mode == "missing target" {
				selection = selection[:1]
			}
			if mode == "wrong target" {
				selection[1].TargetID = selection[0].TargetID
			}
			if mode == "unsorted paths" {
				selection[1].Paths[0], selection[1].Paths[1] = selection[1].Paths[1], selection[1].Paths[0]
			}
			consumed := false
			f.filesystemWire = func(i int, wire []byte) []byte {
				if i != 0 {
					return wire
				}
				if (mode == "mount drift" || mode == "ignored failure") && f.opens[i].Load() > 1 || mode == "final drift" && consumed {
					return bytes.ReplaceAll(wire, []byte(`"mount_id":29`), []byte(`"mount_id":30`))
				}
				if mode == "lower final free" && consumed {
					return bytes.Replace(wire, []byte(`"available_bytes":700000`), []byte(`"available_bytes":1`), 1)
				}
				if mode == "free varies" && f.opens[i].Load() == 2 {
					return bytes.Replace(wire, []byte(`"available_bytes":700000`), []byte(`"available_bytes":600000`), 1)
				}
				return wire
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var retained clusterSSHNetworkTargetReaders
			err := runClusterSSHNetworkTargets(ctx, f.a.Baseline, f.claims, f.deps, func(ctx context.Context, readers clusterSSHNetworkTargetReaders) error {
				retained = readers
				read := readers.Filesystem
				cached := map[string]clusterremote.TargetFilesystem{}
				readers.Filesystem = func(ctx context.Context, item clusterSSHInspectedTarget, request clusterremote.FilesystemRequest) (clusterremote.TargetFilesystem, error) {
					if mode == "zero" {
						return clusterremote.TargetFilesystem{}, nil
					}
					if mode == "substituted" {
						item = f.a.Cohort.Targets[1]
						request.MachineID, request.BootID = item.Report.MachineID, item.Report.BootID
					}
					if value, ok := cached[item.Binding.TargetID]; mode == "cached" && ok {
						return value, nil
					}
					value, err := read(ctx, item, request)
					cached[item.Binding.TargetID] = value
					if mode == "input copy" {
						request.Paths[0] = "/other"
						selection[0].Paths[0] = "/other"
					}
					if mode == "key copy" {
						item.Key.PublicKey[0] ^= 1
					}
					return value, err
				}
				caller := ctx
				if mode == "background loss" {
					caller = context.Background()
				}
				err := withClusterSSHTargetFilesystemEvidence(caller, readers, selection, func(ctx context.Context, evidence []clusterSSHTargetFilesystemEvidence) error {
					consumed = true
					if len(evidence) != len(f.claims) {
						t.Fatal("partial cohort")
					}
					for i, item := range evidence {
						if item.TargetID != f.claims[i].Lease.TargetID || len(item.Storage.Paths) != 2 || len(item.Storage.Filesystems) != 1 {
							t.Fatal("wrong binding/shared filesystem")
						}
					}
					if mode == "free varies" && evidence[0].Storage.Filesystems[0].AvailableBytes != 600000 {
						t.Fatal("not conservative")
					}
					switch mode {
					case "source lost":
						f.lost.Store(2)
					case "background loss":
						cancel()
						select {
						case <-ctx.Done():
						case <-time.After(time.Second):
							t.Fatal("scope escaped cancellation")
						}
					case "consumer error":
						return errors.New("private error")
					case "consumer copy":
						evidence[0].TargetID = "other"
						evidence[0].Storage.Paths[0].Path = "/other"
						evidence[0].Storage.Filesystems[0].AvailableBytes = 0
					}
					return nil
				})
				if mode == "ignored failure" {
					return nil
				}
				return err
			})
			good := mode == "expansion" || mode == "replacement" || mode == "free varies" || mode == "consumer copy" || mode == "input copy" || mode == "key copy" || mode == "late read"
			if (err == nil) != good {
				t.Fatal("unsafe filesystem scope", err)
			}
			if good && !consumed {
				t.Fatal("missing consumer")
			}
			if mode == "late read" {
				before := f.opens[0].Load()
				item := f.a.Cohort.Targets[0]
				_, err := retained.Filesystem(context.Background(), item, clusterremote.FilesystemRequest{MachineID: item.Report.MachineID, BootID: item.Report.BootID, Paths: selection[0].Paths})
				if err == nil || f.opens[0].Load() != before {
					t.Fatal("closed scope opened credential")
				}
			}
			if mode == "missing target" || mode == "wrong target" || mode == "unsorted paths" {
				if f.opens[0].Load() != 0 || f.opens[1].Load() != 0 {
					t.Fatal("invalid selection opened credential")
				}
			}
			f.assertClosed(t)
		})
	}
}
