package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Synthetic metadata only: archive verification has separate native fixtures.
func addExternalReleaseFixture(t *testing.T, f *bootstrapReleaseFixture) uint64 {
	t.Helper()
	var proofs []clusterbootstrap.ExternalImageProof
	var budget uint64
	for i, pin := range clusterbootstrap.ExternalImagePins() {
		p := clusterbootstrap.ExternalImageProof{Reference: pin.Reference, IndexDigest: pin.IndexDigest, ManifestDigest: pin.ManifestDigest, ConfigDigest: pin.ConfigDigest, ArchiveSHA256: strings.Repeat("a", 64), ContentBytes: pin.IndexBytes + pin.ManifestBytes + pin.ConfigBytes + pin.LayerBlobBytes, ContentEntries: int64(3 + pin.LayerCount)}
		remaining := pin.LayerBlobBytes
		for j := 0; j < pin.LayerCount; j++ {
			n := remaining / int64(pin.LayerCount-j)
			remaining -= n
			digest := bootstrapDigest(fmt.Sprint(i, "/", j))
			p.Layers = append(p.Layers, clusterbootstrap.ImageLayerProof{Digest: digest, DiffID: digest, BlobBytes: n, TarBytes: 10240, FileBytes: 4, Entries: 2})
		}
		p.ArchiveBytes = p.ContentBytes + 10240
		// Shared filesystem: three archive copies, unique content, expanded
		// layers, then 4KiB allocation per retained/incoming/expanded entry.
		budget += uint64(3*p.ArchiveBytes + p.ContentBytes + int64(pin.LayerCount)*10240 + 4096*(3+p.ContentEntries+int64(pin.LayerCount)*2))
		proofs = append(proofs, p)
		name := clusterbootstrap.ExternalImageAssetName(p.Reference)
		a := clusterBootstrapAsset{ID: int64(201 + i), Name: name, State: "uploaded", Size: p.ArchiveBytes, Digest: "sha256:" + p.ArchiveSHA256}
		a.URL = fmt.Sprintf("%s/repos/%s/releases/assets/%d", clusterGitHubAPIBase(), f.expected.Repository, a.ID)
		a.BrowserDownloadURL = clusterbootstrap.AssetURL(f.expected, name)
		f.release.Assets = append(f.release.Assets, a)
	}
	raw, _ := json.Marshal(map[string]any{"version": 1, "repository": f.expected.Repository, "release": f.expected.Release, "source_sha": f.expected.SourceSHA, "platform": "linux-amd64", "images": proofs})
	a := clusterBootstrapAsset{ID: 200, Name: clusterbootstrap.ExternalInventoryName, State: "uploaded", Size: int64(len(raw)), Digest: bootstrapDigest(string(raw))}
	a.URL = fmt.Sprintf("%s/repos/%s/releases/assets/200", clusterGitHubAPIBase(), f.expected.Repository)
	a.BrowserDownloadURL = clusterbootstrap.AssetURL(f.expected, a.Name)
	f.release.Assets = append(f.release.Assets, a)
	if f.extraBodies == nil {
		f.extraBodies = map[string]string{}
	}
	f.extraBodies[fmt.Sprintf("/repos/%s/releases/assets/200", f.expected.Repository)] = string(raw)
	return budget
}

func TestClusterSSHExternalPublicationAndDemand(t *testing.T) {
	for _, mode := range []string{"valid", "missing", "digest", "inventory tamper", "source", "mutable", "asset changed", "inventory changed", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			f := newBootstrapReleaseFixture(t)
			expectedBudget := addExternalReleaseFixture(t, f)
			switch mode {
			case "missing":
				f.release.Assets = f.release.Assets[:len(f.release.Assets)-1]
			case "digest":
				f.release.Assets[2].Digest = "sha256:" + strings.Repeat("b", 64)
			case "inventory tamper":
				for k := range f.extraBodies {
					f.extraBodies[k] += " "
				}
			case "source":
				f.sha = strings.Repeat("b", 40)
			case "mutable":
				no := false
				f.release.Immutable = &no
			}
			ctx, cancel := context.WithCancel(bootstrapContext())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			v, err := resolveClusterSSHExternalInventory(ctx, f.expected)
			valid := mode == "valid" || mode == "asset changed" || mode == "inventory changed"
			if (err == nil) != valid {
				t.Fatalf("resolution: %v", err)
			}
			if !valid {
				return
			}
			demands, err := v.demands()
			if err != nil || len(demands) != 2 {
				t.Fatal("missing external demands")
			}
			var budget uint64
			for _, d := range demands {
				budget += d.Bytes + 4096*d.Entries
			}
			if budget != expectedBudget || demands[0].Path != "/opt/Borealis" || demands[1].Path != "/var/lib/rancher/k3s" {
				t.Fatal("incomplete external footprint")
			}
			if mode == "asset changed" {
				f.release.Assets[2].Size--
			}
			if mode == "inventory changed" {
				for k := range f.extraBodies {
					f.extraBodies[k] += " "
				}
			}
			if (v.refresh(ctx) == nil) != (mode == "valid") {
				t.Fatal("publication drift survived")
			}
			if f.archiveReads != 0 {
				t.Fatal("metadata resolution read archive")
			}
		})
	}
}
