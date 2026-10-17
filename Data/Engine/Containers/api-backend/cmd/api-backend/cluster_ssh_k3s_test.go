package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func addK3sReleaseFixture(t *testing.T, f *bootstrapReleaseFixture) {
	t.Helper()
	p := clusterbootstrap.K3sPins()
	proof := clusterbootstrap.K3sArchiveProof{ArchiveSHA256: p.Archive.SHA256, ArchiveBytes: p.Archive.Size, ContentBytes: 1024, ContentEntries: 4, ExpandedBytes: 4096, ExpandedEntries: 2, Images: p.Images}
	raw, _ := json.Marshal(map[string]any{"version": 2, "repository": f.expected.Repository, "release": f.expected.Release, "source_sha": f.expected.SourceSHA, "platform": "linux-amd64", "k3s_version": p.Version, "binary": p.Binary, "payload": p.Payload, "installer": p.Installer, "archive": proof})
	if f.extraBodies == nil {
		f.extraBodies = map[string]string{}
	}
	for i, name := range []string{clusterbootstrap.K3sInventoryName, clusterbootstrap.K3sBinaryName, clusterbootstrap.K3sArchiveName, clusterbootstrap.K3sInstallerName} {
		a := clusterBootstrapAsset{ID: int64(100 + i), Name: name, State: "uploaded"}
		a.URL = fmt.Sprintf("%s/repos/%s/releases/assets/%d", clusterGitHubAPIBase(), f.expected.Repository, a.ID)
		a.BrowserDownloadURL = clusterbootstrap.AssetURL(f.expected, name)
		switch i {
		case 0:
			a.Size, a.Digest = int64(len(raw)), bootstrapDigest(string(raw))
			f.extraBodies[fmt.Sprintf("/repos/%s/releases/assets/100", f.expected.Repository)] = string(raw)
		case 1:
			a.Size, a.Digest = p.Binary.Size, "sha256:"+p.Binary.SHA256
		case 3:
			a.Size, a.Digest = p.Installer.Size, "sha256:"+p.Installer.SHA256
		case 2:
			a.Size, a.Digest = p.Archive.Size, "sha256:"+p.Archive.SHA256
		}
		f.release.Assets = append(f.release.Assets, a)
	}
}

func TestClusterSSHK3sPublicationAndCapacity(t *testing.T) {
	for _, mode := range []string{"valid", "missing archive", "digest", "baseline", "inventory tamper", "publication changed"} {
		t.Run(mode, func(t *testing.T) {
			f := newBootstrapReleaseFixture(t)
			addK3sReleaseFixture(t, f)
			p := clusterbootstrap.K3sPins()
			version := p.Version
			switch mode {
			case "missing archive":
				f.release.Assets = f.release.Assets[:len(f.release.Assets)-1]
			case "digest":
				f.release.Assets[len(f.release.Assets)-1].Digest = "sha256:" + strings.Repeat("a", 64)
			case "baseline":
				version = "v1.36.4+k3s1"
			case "inventory tamper":
				for k := range f.extraBodies {
					f.extraBodies[k] += " "
				}
			}
			v, err := resolveClusterSSHK3sInventory(bootstrapContext(), f.expected, version)
			valid := mode == "valid" || mode == "publication changed"
			if (err == nil) != valid {
				t.Fatalf("unexpected resolution: %v", err)
			}
			if !valid {
				return
			}
			d, err := v.demands()
			if err != nil || len(d) != 3 || d[1].Path != "/usr/local/bin" || d[1].Bytes != uint64(2*p.Binary.Size) || d[2].Bytes != uint64(2*p.Archive.Size+1024+4096+2*p.Payload.TarBytes) || d[2].Entries != uint64(8+2*(p.Payload.Entries+p.Payload.CNILinks+7)) {
				t.Fatal("incomplete K3s demand")
			}
			merged, err := mergeClusterSSHFilesystemDemands(d, d)
			if err != nil || len(merged) != 3 || merged[0].Bytes != 2*d[0].Bytes || merged[0].Entries != 8 {
				t.Fatal("shared paths not summed")
			}
			if mode == "publication changed" {
				f.release.Assets[len(f.release.Assets)-1].Size--
			}
			if (v.refresh(bootstrapContext()) == nil) != (mode == "valid") {
				t.Fatal("changed publication passed")
			}
			if f.archiveReads != 0 {
				t.Fatal("resolution downloaded binary/image bytes")
			}
		})
	}
}
