package clusterbootstrap

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestPreparationScratchMeasuredFromOwnedBundle(t *testing.T) {
	e, manifest, members := archiveFixture(t)
	b, err := stageFixture(t, context.Background(), e, manifest, pack(t, members), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	expected, runtime := preparationFixture()
	expected.Source = e
	config, err := NewPreparationConfiguration(expected, runtime)
	if err != nil {
		t.Fatal(err)
	}
	check := func(context.Context) error { return nil }
	inputs, err := BindPreparationInputs(context.Background(), b, config, check)
	if err != nil {
		t.Fatal(err)
	}
	var actual PreparationScratchDemand
	if err := filepath.WalkDir(b.root, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		actual.Entries++
		if info.Mode().IsRegular() {
			actual.Bytes += uint64(info.Size())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	measured, err := inputs.StorageDemand(context.Background(), expected, check)
	if err != nil || measured != actual || measured.Bytes == 0 {
		t.Fatal("owned bundle demand differs from actual scratch", err)
	}
	calls := 0
	if _, err := inputs.StorageDemand(context.Background(), expected, func(context.Context) error {
		calls++
		if calls > 2 {
			return ErrSessionAuthority
		}
		return nil
	}); err == nil {
		t.Fatal("final authority loss accepted")
	}
	if err := os.WriteFile(b.ManagerPath(), []byte("changed"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := inputs.StorageDemand(context.Background(), expected, check); err == nil {
		t.Fatal("changed bundle supplied demand")
	}
	if err := inputs.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := inputs.StorageDemand(context.Background(), expected, check); err == nil {
		t.Fatal("closed bundle supplied demand")
	}
}
