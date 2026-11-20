package clusterbootstrap

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHostPackageStorageDemand(t *testing.T) {
	for _, mode := range []string{"valid", "wrong source", "cancelled", "authority before", "authority after", "closed", "changed bytes", "replaced archive"} {
		t.Run(mode, func(t *testing.T) {
			expected, inventory, archive := hostPackageStageFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			set, err := StageHostPackages(ctx, t.TempDir(), expected, inventory, func(context.Context) (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(archive)), nil
			}, func(ctx context.Context) error { return ctx.Err() })
			if err != nil {
				t.Fatal(err)
			}
			defer set.Close()
			original := expected
			switch mode {
			case "wrong source":
				expected.SourceSHA = strings.Repeat("b", 40)
			case "cancelled":
				cancel()
			case "closed":
				if err := set.Close(); err != nil {
					t.Fatal(err)
				}
			case "changed bytes":
				archive[1024] ^= 1
				if err := os.WriteFile(filepath.Join(set.path, HostPackageArchiveName), archive, 0600); err != nil {
					t.Fatal(err)
				}
			case "replaced archive":
				if err := os.Remove(filepath.Join(set.path, HostPackageArchiveName)); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(set.path, HostPackageArchiveName), archive, 0600); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			check := func(ctx context.Context) error {
				calls++
				if mode == "authority before" || mode == "authority after" && calls == 2 {
					return ErrSessionAuthority
				}
				return ctx.Err()
			}
			got, err := set.StorageDemand(ctx, expected, check)
			if mode == "valid" {
				want := PreparationScratchDemand{Bytes: uint64(len(archive)), Entries: uint64(len(inventory.Packages())) + 5}
				for _, pin := range inventory.Packages() {
					want.Bytes += uint64(pin.Size)
				}
				if err != nil || got != want || calls != 3 {
					t.Fatal("lost tar/extracted/cache charge or charged installed content", got, want, calls, err)
				}
				got.Bytes = 0
				again, err := set.StorageDemand(ctx, expected, check)
				if err != nil || again != want {
					t.Fatal("caller changed owner demand", again, err)
				}
				if err := set.Close(); err != nil {
					t.Fatal(err)
				}
			} else if err == nil || got != (PreparationScratchDemand{}) {
				t.Fatal("invalid demand escaped", got, err)
			}
			if got, err := set.StorageDemand(context.Background(), original, func(context.Context) error { return nil }); err == nil || got != (PreparationScratchDemand{}) {
				t.Fatal("failed/closed owner lent demand", got, err)
			}
		})
	}
	var absent *HostPackageSet
	if got, err := absent.StorageDemand(context.Background(), Expected{}, nil); err == nil || got != (PreparationScratchDemand{}) {
		t.Fatal("absent owner lent demand", got, err)
	}
}

func TestHostPackageStorageDemandJoinsClose(t *testing.T) {
	expected, inventory, archive := hostPackageStageFixture(t)
	set, err := StageHostPackages(context.Background(), t.TempDir(), expected, inventory, func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(archive)), nil
	}, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	readDone, closeDone := make(chan error, 1), make(chan error, 1)
	go func() {
		first := true
		_, err := set.StorageDemand(context.Background(), expected, func(context.Context) error {
			if first {
				first = false
				close(started)
				<-release
			}
			return nil
		})
		readDone <- err
	}()
	<-started
	go func() { closeDone <- set.Close() }()
	select {
	case err := <-closeDone:
		close(release)
		<-readDone
		t.Fatal("Close escaped active demand proof", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-readDone; err != nil {
		t.Fatal(err)
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
}

func TestHostPackageApprovalDigest(t *testing.T) {
	for _, mode := range []string{"valid", "wrong source", "lost authority", "changed after verification", "closed"} {
		t.Run(mode, func(t *testing.T) {
			want, inventory, raw := hostPackageStageFixture(t)
			ctx := t.Context()
			ok := func(ctx context.Context) error { return ctx.Err() }
			set, err := StageHostPackages(ctx, t.TempDir(), want, inventory, func(context.Context) (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(raw)), nil
			}, ok)
			if err != nil {
				t.Fatal(err)
			}
			defer set.Close()
			expected := want
			if mode == "wrong source" {
				expected.SourceSHA = strings.Repeat("b", 40)
			}
			if mode == "closed" {
				if err := set.Close(); err != nil {
					t.Fatal(err)
				}
			}
			checks := 0
			digest, err := set.ApprovalDigest(ctx, expected, func(ctx context.Context) error {
				checks++
				if checks == 2 {
					switch mode {
					case "changed after verification":
						raw[1024] ^= 1
						return os.WriteFile(filepath.Join(set.path, HostPackageArchiveName), raw, 0600)
					case "lost authority":
						return ErrSessionAuthority
					}
				}
				return ctx.Err()
			})
			if mode == "valid" {
				if err != nil || digest != inventory.Archive().SHA256 {
					t.Fatal("wrong approval digest", digest, err)
				}
				return
			}
			if err == nil || digest != "" {
				t.Fatal("invalid package proof accepted", digest, err)
			}
			if _, err := set.ApprovalDigest(ctx, want, ok); err == nil {
				t.Fatal("failed owner reused")
			}
		})
	}
}
