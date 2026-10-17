package clusterbootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func stagedImagesFixture(t *testing.T) (*ImageSet, Expected) {
	t.Helper()
	ctx := context.Background()
	expected, wire := imageInventoryFixture(t)
	archives := map[string][]byte{}
	for i, proof := range wire.Images {
		raw := imageFixture(t, "gzip", proof.Role)
		archives[proof.Role] = raw
		wire.Images[i], _ = InspectImageArchive(ctx, bytes.NewReader(raw), int64(len(raw)), proof.Role, expected.SourceSHA)
	}
	raw, _ := json.Marshal(wire)
	inventory, err := ParseImageInventory(raw, expected)
	if err != nil {
		t.Fatal(err)
	}
	set, err := StageImages(ctx, t.TempDir(), inventory, func(_ context.Context, proof ImageArchiveProof) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(archives[proof.Role])), nil
	}, mutationAuthority)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close() })
	return set, expected
}

func TestImageJournalPublicationAndRecovery(t *testing.T) {
	for _, mode := range []string{"complete", "missing identity", "collision", "cancel after file", "tampered recovery"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			set, expected := stagedImagesFixture(t)
			images, deploy, ledger := mutationTempDir(t), mutationTempDir(t), mutationTempDir(t)
			owner, _ := mutationFixture()
			var lost bool
			check := func(context.Context) error {
				if lost {
					return ErrSessionAuthority
				}
				if mode == "cancel after file" {
					if _, err := os.Stat(filepath.Join(images, imagePinnedArchive(set.inventory.Images()[0]))); err == nil {
						lost = true
						return ErrSessionAuthority
					}
				}
				return nil
			}
			j, err := openTestMutationJournal(ctx, ledger, owner, expected, check)
			if err != nil {
				t.Fatal(err)
			}
			for _, step := range []string{"stage_source", "install_identity", "prepare_host"} {
				if step == "install_identity" && mode == "missing identity" {
					continue
				}
				if _, err := j.Apply(ctx, step, mutationDigest(step), func(context.Context, *os.File) (string, error) { return mutationDigest("done"), nil }); err != nil {
					t.Fatal(err)
				}
			}
			collision := filepath.Join(images, imagePinnedArchive(set.inventory.Images()[1]))
			if mode == "collision" {
				if err := os.WriteFile(collision, []byte("operator file"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			result, err := set.publishK3sArchives(ctx, j, images, deploy)
			if mode == "missing identity" {
				if err != ErrSessionAuthority {
					t.Fatalf("missing identity accepted: %v", err)
				}
				entries, _ := os.ReadDir(images)
				if len(entries) != 0 {
					t.Fatal("wrote before identity")
				}
				j.Close()
				return
			}
			if mode == "collision" || mode == "cancel after file" {
				if err != ErrMutationUnknown || readMutationFixture(t, ledger).Steps["stage_images"].State != "intent" {
					t.Fatalf("partial publication not retained: %v", err)
				}
				if mode == "collision" {
					raw, _ := os.ReadFile(collision)
					if string(raw) != "operator file" {
						t.Fatal("overwrote existing archive")
					}
				}
			} else {
				if err != nil || result == "" {
					t.Fatal(err)
				}
				if _, err := set.publishK3sArchives(ctx, j, images, deploy); err != ErrMutationApplied {
					t.Fatal("replayed completed install")
				}
			}
			j.Close()
			if mode == "tampered recovery" {
				if err := os.WriteFile(filepath.Join(deploy, "image-manifest.json"), []byte("changed"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			j, err = openTestMutationJournal(ctx, ledger, nextMutationOwner(owner), expected, mutationAuthority)
			if err != nil {
				t.Fatal(err)
			}
			defer j.Close()
			got, err := set.reconcileK3sArchives(ctx, j, images, deploy)
			if mode == "complete" {
				if err != nil || got != result {
					t.Fatalf("read-only recovery failed: %v", err)
				}
			} else if err == nil {
				t.Fatal("partial or changed files reconciled as applied")
			}
		})
	}
}
