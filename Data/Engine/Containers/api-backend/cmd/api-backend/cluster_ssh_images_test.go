package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func imageReleaseFixture(t *testing.T, expected ...clusterbootstrap.Expected) *bootstrapReleaseFixture {
	t.Helper()
	f := newBootstrapReleaseFixture(t)
	if len(expected) > 0 {
		f.expected = expected[0]
		f.sha = f.expected.SourceSHA
		f.release.TagName = f.expected.Release
		qualification := strings.Contains(f.expected.Release, "-rc.")
		f.release.Prerelease = &qualification
	}
	var images []clusterbootstrap.ImageArchiveProof
	for _, role := range clusterbootstrap.ImageRoles() {
		images = append(images, clusterbootstrap.ImageArchiveProof{Role: role, Image: clusterbootstrap.ImageReference(role, f.expected.SourceSHA), ArchiveSHA256: strings.Repeat("a", 64), ArchiveBytes: 8192, ManifestDigest: "sha256:" + strings.Repeat("b", 64), ConfigDigest: "sha256:" + strings.Repeat("c", 64), Layers: []clusterbootstrap.ImageLayerProof{{Digest: "sha256:" + strings.Repeat("d", 64), DiffID: "sha256:" + strings.Repeat("e", 64), BlobBytes: 1024, TarBytes: 4096, FileBytes: 2048, Entries: 2}}})
	}
	raw, _ := json.Marshal(map[string]any{"version": 1, "repository": f.expected.Repository, "release": f.expected.Release, "source_sha": f.expected.SourceSHA, "platform": "linux-amd64", "images": images})
	f.manifest = string(raw)
	a := f.release.Assets[0]
	a.Name = clusterbootstrap.ImageInventoryName
	a.Size = int64(len(raw))
	a.Digest = bootstrapDigest(f.manifest)
	a.BrowserDownloadURL = clusterbootstrap.AssetURL(f.expected, a.Name)
	f.release.Assets = []clusterBootstrapAsset{a}
	for i, image := range images {
		a := clusterBootstrapAsset{ID: int64(i + 2), Name: clusterbootstrap.ImageAssetName(image.Role), State: "uploaded", Size: image.ArchiveBytes, Digest: "sha256:" + image.ArchiveSHA256}
		a.URL = fmt.Sprintf("%s/repos/%s/releases/assets/%d", clusterGitHubAPIBase(), f.expected.Repository, a.ID)
		a.BrowserDownloadURL = clusterbootstrap.AssetURL(f.expected, a.Name)
		f.release.Assets = append(f.release.Assets, a)
	}
	return f
}

func TestClusterSSHImageCapacityNativeAuthority(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "image drift", "source drift", "sibling lost", "inode exhausted"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHNetworkTargetsFixture(t, mode == "replacement", "filesystem")
			release := imageReleaseFixture(t, f.a.Baseline)
			f.filesystemWire = func(_ int, raw []byte) []byte {
				var w struct {
					Version   int                              `json:"version"`
					MachineID string                           `json:"machine_id"`
					BootID    string                           `json:"boot_id"`
					Evidence  clusterremote.FilesystemEvidence `json:"evidence"`
				}
				_ = json.Unmarshal(raw, &w)
				w.Version = 4
				w.Evidence.Filesystems[0].TotalBytes = 120 << 30
				w.Evidence.Filesystems[0].AvailableBytes = 90 << 30
				if mode == "inode exhausted" {
					w.Evidence.Filesystems[0].AvailableInodes = 1
				}
				p := w.Evidence.Paths[1]
				p.Path = "/var/lib/rancher/k3s"
				w.Evidence.Paths = append(w.Evidence.Paths, p)
				out, _ := json.Marshal(w)
				return out
			}
			expected, err := buildClusterSSHPreparationExpected(f.a.Cohort, f.a.Source, f.a.Lease, f.a.Baseline, f.a.K3sVersion, "10.42.0.0/16", "10.43.0.0/16")
			if err != nil {
				t.Fatal(err)
			}
			snapshot := clusterSSHPreparationSnapshot{Expected: expected, Storage: sshStorageSnapshotFixture(t, f.a)}
			consumed := false
			source := func(ctx context.Context) (clusterSSHPreparationSnapshot, error) {
				v := snapshot
				if mode == "source drift" && consumed {
					v.Expected.Source.SourceSHA = strings.Repeat("d", 40)
				}
				return v, ctx.Err()
			}
			err = runClusterSSHNetworkTargets(bootstrapContext(), f.a.Baseline, f.claims, f.deps, func(ctx context.Context, readers clusterSSHNetworkTargetReaders) error {
				return withClusterSSHImageStorageCapacity(ctx, readers, source, func(_ context.Context, out []clusterSSHTargetStorageCapacity) error {
					consumed = true
					if len(out) != len(f.claims) {
						t.Fatal("partial image-capacity cohort")
					}
					for _, v := range out {
						if !v.Fits || len(v.Budgets) != 1 || v.Budgets[0].OtherBytes != 2*clusterbootstrap.MaxExpandedBytes+clusterbootstrap.MaxBundleBytes+9*(3*8192+1024+4096)+4096*(2*clusterbootstrap.MaxEntries+10+9*7) {
							t.Fatal("image bytes missing from shared filesystem budget")
						}
					}
					if mode == "image drift" {
						release.release.Assets = release.release.Assets[:9]
					}
					if mode == "sibling lost" {
						f.lost.Store(2)
					}
					return nil
				})
			})
			if (err == nil) != (mode == "expansion" || mode == "replacement") || consumed != (mode != "inode exhausted") {
				t.Fatalf("image-capacity authority: consumed=%v error=%v", consumed, err)
			}
			f.assertClosed(t)
		})
	}
}

func TestClusterSSHImageInventoryPublicationAndDemand(t *testing.T) {
	for _, mode := range []string{"valid", "missing image", "changed digest", "wrong source", "changed manifest", "mutable", "asset ID drift", "missing after read"} {
		t.Run(mode, func(t *testing.T) {
			f := imageReleaseFixture(t)
			switch mode {
			case "missing image":
				f.release.Assets = f.release.Assets[:9]
			case "changed digest":
				f.release.Assets[2].Digest = "sha256:" + strings.Repeat("f", 64)
			case "wrong source":
				f.sha = strings.Repeat("b", 40)
			case "changed manifest":
				f.manifestReply = f.manifest + " "
			case "mutable":
				no := false
				f.release.Immutable = &no
			}
			v, err := resolveClusterSSHImageInventory(bootstrapContext(), f.expected)
			valid := mode == "valid" || mode == "asset ID drift" || mode == "missing after read"
			if (err == nil) != valid {
				t.Fatalf("unexpected inventory result %v", err)
			}
			if f.archiveReads != 0 {
				t.Fatal("metadata resolution downloaded image data")
			}
			if !valid {
				return
			}
			d, err := v.demands()
			if err != nil || len(d) != 2 || d[0].Path != "/opt/Borealis" || d[0].Bytes != 2*clusterbootstrap.MaxExpandedBytes+clusterbootstrap.MaxBundleBytes+9*8192 || d[1].Path != "/var/lib/rancher/k3s" || d[1].Bytes != 9*(2*8192+1024+4096) {
				t.Fatal("incomplete or discounted image demand")
			}
			if mode == "asset ID drift" {
				f.release.Assets[2].ID = 100
				f.release.Assets[2].URL = fmt.Sprintf("%s/repos/%s/releases/assets/100", clusterGitHubAPIBase(), f.expected.Repository)
			}
			if mode == "missing after read" {
				f.release.Assets = f.release.Assets[:9]
			}
			if (v.refresh(bootstrapContext()) == nil) != (mode == "valid") {
				t.Fatal("changed publication survived refresh")
			}
		})
	}
}
