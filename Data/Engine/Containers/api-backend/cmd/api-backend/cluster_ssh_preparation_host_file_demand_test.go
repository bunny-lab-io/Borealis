package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"reflect"
	"testing"
)

func TestClusterSSHPreparationHostFileDemands(t *testing.T) {
	_, _, snapshot := sshBrokerFixture(t)
	expected := snapshot.Expected
	got, err := clusterSSHPreparationHostFileDemands(expected, "noble")
	if err != nil {
		t.Fatal(err)
	}
	shared, err := clusterbootstrap.PreparationHostFileDemands(expected, "noble")
	if err != nil || len(got) != len(shared) {
		t.Fatal("shared native demand missing", err)
	}
	metadata := 0
	for i, row := range shared {
		if got[i] != (clusterSSHFilesystemDemand{Path: row.Path, Bytes: row.Bytes, Entries: row.Entries}) {
			t.Fatal("adapter changed native directory demand")
		}
		if row.Bytes == 0 {
			metadata++
		}
	}
	if metadata == 0 {
		t.Fatal("created directory entries omitted")
	}
	got[0].Entries = 999
	again, err := clusterSSHPreparationHostFileDemands(expected, "noble")
	if err != nil || reflect.DeepEqual(got, again) {
		t.Fatal("adapter aliases previous result", err)
	}
	if got, err := clusterSSHPreparationHostFileDemands(clusterbootstrap.PreparationExpected{}, "noble"); err == nil || got != nil {
		t.Fatal("absent binding produced demand")
	}
	if got, err := clusterSSHPreparationHostFileDemands(expected, "noble\nforeign"); err == nil || got != nil {
		t.Fatal("invalid suite produced demand")
	}
}

// Feed native generated files through the same merger, path projection and
// filesystem arithmetic used by preparation; directory-only rows consume real
// inodes/allocation without inventing content or a second rename generation.
func TestClusterSSHPreparationHostFilesStorageComposition(t *testing.T) {
	_, _, snapshot := sshBrokerFixture(t)
	generated, err := clusterSSHPreparationHostFileDemands(snapshot.Expected, "noble")
	if err != nil {
		t.Fatal(err)
	}
	var bytes, entries uint64
	for _, d := range generated {
		bytes += d.Bytes
		entries += d.Entries
	}
	merged, err := mergeClusterSSHFilesystemDemands(generated,
		[]clusterSSHFilesystemDemand{{Path: "/opt/Borealis", Bytes: 100, Entries: 2}, {Path: "/var/lib/rancher/k3s", Bytes: 200, Entries: 3}, {Path: "/usr/local/bin", Bytes: 300, Entries: 1}, {Path: "/var/tmp", Bytes: 400, Entries: 2}})
	if err != nil {
		t.Fatal("native demand cannot reach storage reader", err)
	}
	f := newSSHStorageFixture(t, false)
	requirements := sshStorageSnapshotFixture(t, f.a).Requirements
	paths, ok := clusterSSHStorageDemandPaths(requirements.Policy, merged)
	if !ok || len(paths) <= 8 {
		t.Fatal("generated filesystem projection missing")
	}
	request := clusterremote.FilesystemRequest{MachineID: "11111111111111111111111111111111", BootID: "11111111-1111-4111-8111-111111111111", Paths: paths, RequirePersistent: true}
	if request.Validate() != nil {
		t.Fatal("native request rejects actual plan")
	}
	evidence := sshCapacityEvidence()
	evidence.Paths = nil
	for _, p := range paths {
		evidence.Paths = append(evidence.Paths, clusterremote.FilesystemPath{Path: p, Ancestor: "/", Filesystem: "root"})
	}
	budgets, err := calculateClusterSSHStorageCapacity(requirements, evidence, merged)
	if err != nil || len(budgets) != 1 || budgets[0].OtherBytes != bytes+1000+(entries+8)*4096 || !budgets[0].Fits {
		t.Fatal("generated files or parents lost in shared filesystem charge", err, budgets)
	}
	evidence.Filesystems[0].AvailableInodes = entries + 8
	if _, err := calculateClusterSSHStorageCapacity(requirements, evidence, merged); err == nil {
		t.Fatal("metadata exhausted available inodes but accepted")
	}
	for _, bad := range []clusterSSHFilesystemDemand{{Path: "/etc"}, {Path: "/etc", Entries: clusterSSHCapacityLimit + 1}, {Path: "/etc", Bytes: clusterSSHCapacityLimit + 1}} {
		if _, err := mergeClusterSSHFilesystemDemands([]clusterSSHFilesystemDemand{bad}); err == nil {
			t.Fatal("empty/overflow demand accepted")
		}
	}
}
