package clusterbootstrap

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func k3sArchiveFixture(t *testing.T, mode string) ([]byte, K3sInputPins) {
	t.Helper()
	raw := imageFixture(t, mode, "api-backend")
	tr := tar.NewReader(bytes.NewReader(raw))
	blobs := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		blobs[h.Name], err = io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
	}
	var index struct {
		Manifests []imageDescriptor `json:"manifests"`
	}
	_ = json.Unmarshal(blobs["index.json"], &index)
	var manifest struct {
		Config imageDescriptor   `json:"config"`
		Layers []imageDescriptor `json:"layers"`
	}
	_ = json.Unmarshal(blobs["blobs/sha256/"+strings.TrimPrefix(index.Manifests[0].Digest, "sha256:")], &manifest)
	reference := "rancher/fixture:v1"
	layers := []string{}
	for _, d := range manifest.Layers {
		layers = append(layers, "blobs/sha256/"+strings.TrimPrefix(d.Digest, "sha256:"))
	}
	blobs["manifest.json"], _ = json.Marshal([]any{map[string]any{"Config": "blobs/sha256/" + strings.TrimPrefix(manifest.Config.Digest, "sha256:"), "RepoTags": []string{reference}, "Layers": layers}})
	var output bytes.Buffer
	tw := tar.NewWriter(&output)
	for name, data := range blobs {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write(data)
	}
	_ = tw.Close()
	raw = output.Bytes()
	h := sha256.Sum256(raw)
	pins := K3sInputPins{Archive: K3sAssetPin{Size: int64(len(raw)), SHA256: hex.EncodeToString(h[:])}, Images: []string{reference}}
	return raw, pins
}

func TestK3sArchivePinnedContentAndMeasuredLayers(t *testing.T) {
	for _, mode := range []string{"gzip", "relative prefix", "relative duplicate", "unsafe layer", "wrong diff", "architecture", "blob digest", "gzip concatenation", "cancelled", "pin changed", "missing image"} {
		t.Run(mode, func(t *testing.T) {
			raw, pins := k3sArchiveFixture(t, mode)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			if mode == "pin changed" {
				pins.Archive.SHA256 = strings.Repeat("a", 64)
			}
			if mode == "missing image" {
				pins.Images = append(pins.Images, "rancher/other:v1")
			}
			proof, err := inspectK3sArchive(ctx, bytes.NewReader(raw), int64(len(raw)), pins)
			good := mode == "gzip" || mode == "relative prefix"
			if (err == nil) != good {
				t.Fatalf("accepted=%v error=%v", err == nil, err)
			}
			if good && (proof.ContentEntries != 3 || proof.ContentBytes < 1 || proof.ExpandedBytes < 1024 || proof.ExpandedEntries != 2 || len(proof.Images) != 1) {
				t.Fatal("measured contents incomplete")
			}
			if _, err := InspectK3sArchive(ctx, bytes.NewReader(raw), int64(len(raw))); err == nil {
				t.Fatal("fixture bypassed production pin")
			}
		})
	}
}

func TestK3sInventoryIdentityAndBounds(t *testing.T) {
	p := K3sPins()
	e := Expected{Repository: "bunny-lab-io/Borealis", Release: "2026.09.999-rc.1", SourceSHA: imageTestSHA, AllowQualification: true}
	base := k3sInventoryWire{Version: 2, Repository: e.Repository, Release: e.Release, SourceSHA: e.SourceSHA, Platform: "linux-amd64", K3sVersion: p.Version, Binary: p.Binary, Payload: p.Payload, Installer: p.Installer,
		Archive: K3sArchiveProof{ArchiveSHA256: p.Archive.SHA256, ArchiveBytes: p.Archive.Size, ContentBytes: 1024, ContentEntries: 4, ExpandedBytes: 4096, ExpandedEntries: 2, Images: p.Images}}
	for _, mode := range []string{"valid", "source", "binary", "archive", "entries", "expansion", "missing image", "unknown", "alias", "duplicate", "baseline", "old inventory", "payload", "installer"} {
		t.Run(mode, func(t *testing.T) {
			w := base
			version := p.Version
			switch mode {
			case "source":
				w.SourceSHA = strings.Repeat("b", 40)
			case "binary":
				w.Binary.SHA256 = strings.Repeat("c", 64)
			case "archive":
				w.Archive.ArchiveBytes++
			case "entries":
				w.Archive.ExpandedEntries = 0
			case "expansion":
				w.Archive.ExpandedBytes = 65 << 30
			case "missing image":
				w.Archive.Images = p.Images[:7]
			case "old inventory":
				w.Version = 1
			case "installer":
				w.Installer.SHA256 = strings.Repeat("f", 64)
			case "payload":
				w.Payload.TarBytes--
			case "baseline":
				version = "v1.36.4+k3s1"
			}
			raw, _ := json.Marshal(w)
			switch mode {
			case "unknown":
				raw = append([]byte(`{"other":1,`), raw[1:]...)
			case "duplicate":
				raw = append([]byte(`{"version":1,`), raw[1:]...)
			case "alias":
				raw = bytes.Replace(raw, []byte(`"expanded_bytes"`), []byte(`"Expanded_bytes"`), 1)
			}
			inventory, err := ParseK3sInventory(raw, e, version)
			if (err == nil) != (mode == "valid") {
				t.Fatalf("unexpected inventory error: %v", err)
			}
			if err != nil {
				return
			}
			proof := inventory.Archive()
			if inventory.MatchesArchive(proof) != nil {
				t.Fatal("own proof rejected")
			}
			proof.Images[0] = "changed"
			if inventory.MatchesArchive(proof) == nil || inventory.Archive().Images[0] == "changed" {
				t.Fatal("aliased or changed image proof")
			}
		})
	}
}
