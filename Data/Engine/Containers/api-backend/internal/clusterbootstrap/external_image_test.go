package clusterbootstrap

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
)

func externalFixture(t *testing.T, mode string) ([]byte, ExternalImagePin) {
	t.Helper()
	blobs := map[string][]byte{}
	tr := tar.NewReader(bytes.NewReader(imageFixture(t, mode, "api-backend")))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		blobs[h.Name], _ = io.ReadAll(tr)
	}
	encode := func(v any) []byte {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	digest := func(b []byte) string { h := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(h[:]) }
	var outer struct {
		Manifests []imageDescriptor `json:"manifests"`
	}
	_ = json.Unmarshal(blobs["index.json"], &outer)
	child := outer.Manifests[0]
	oldName := "blobs/sha256/" + strings.TrimPrefix(child.Digest, "sha256:")
	var manifest struct {
		SchemaVersion int               `json:"schemaVersion"`
		MediaType     string            `json:"mediaType"`
		Config        imageDescriptor   `json:"config"`
		Layers        []imageDescriptor `json:"layers"`
	}
	_ = json.Unmarshal(blobs[oldName], &manifest)
	if mode == "docker" {
		manifest.MediaType = dockerManifestType
		manifest.Config.MediaType = dockerConfigType
		for i := range manifest.Layers {
			manifest.Layers[i].MediaType = dockerLayerType
		}
	}
	mraw := bytes.ReplaceAll(encode(manifest), []byte(`,"platform":null`), nil)
	delete(blobs, oldName)
	child.Digest, child.Size, child.MediaType = digest(mraw), int64(len(mraw)), manifest.MediaType
	blobs["blobs/sha256/"+strings.TrimPrefix(child.Digest, "sha256:")] = mraw
	children := []any{map[string]any{"mediaType": child.MediaType, "digest": child.Digest, "size": child.Size, "platform": map[string]string{"os": "linux", "architecture": "amd64"}},
		map[string]any{"mediaType": child.MediaType, "digest": "sha256:" + strings.Repeat("f", 64), "size": 512, "platform": map[string]string{"os": "linux", "architecture": "arm64", "variant": "v8"}}}
	if mode == "duplicate platform" {
		children = append(children, children[0])
	}
	if mode == "wrong platform" {
		children[0].(map[string]any)["platform"] = map[string]string{"os": "linux", "architecture": "arm64"}
	}
	upstreamType := ociIndexType
	if mode == "docker" {
		upstreamType = dockerIndexType
	}
	upstream := encode(map[string]any{"schemaVersion": 2, "mediaType": upstreamType, "manifests": children})
	pin := ExternalImagePin{Reference: "ghcr.io/kube-vip/kube-vip:test", IndexDigest: digest(upstream), IndexBytes: int64(len(upstream)), ManifestDigest: child.Digest, ManifestBytes: child.Size, ConfigDigest: manifest.Config.Digest, ConfigBytes: manifest.Config.Size, LayerCount: len(manifest.Layers)}
	for _, l := range manifest.Layers {
		pin.LayerBlobBytes += l.Size
	}
	blobs["blobs/sha256/"+strings.TrimPrefix(pin.IndexDigest, "sha256:")] = upstream
	root := map[string]any{"mediaType": upstreamType, "digest": pin.IndexDigest, "size": pin.IndexBytes, "annotations": map[string]string{"org.opencontainers.image.ref.name": pin.Reference, "io.containerd.image.name": pin.Reference}}
	if mode == "wrong alias" {
		root["annotations"] = map[string]string{"org.opencontainers.image.ref.name": "foreign"}
	}
	blobs["index.json"] = encode(map[string]any{"schemaVersion": 2, "mediaType": ociIndexType, "manifests": []any{root}})
	if mode == "extra blob" {
		b := []byte("extra")
		blobs["blobs/sha256/"+strings.TrimPrefix(digest(b), "sha256:")] = b
	}
	if mode == "missing config" {
		delete(blobs, "blobs/sha256/"+strings.TrimPrefix(pin.ConfigDigest, "sha256:"))
	}
	if mode == "tampered layer" {
		n := "blobs/sha256/" + strings.TrimPrefix(manifest.Layers[0].Digest, "sha256:")
		blobs[n] = append(blobs[n], byte(0))
	}
	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	for name, b := range blobs {
		_ = tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(b)), Typeflag: tar.TypeReg, Mode: 0o644})
		_, _ = tw.Write(b)
	}
	_ = tw.Close()
	if mode == "trailing" {
		out.WriteString("hidden")
	}
	return out.Bytes(), pin
}

func TestExternalImageArchiveAuthenticatesPlatformAndMeasuresLayers(t *testing.T) {
	for _, mode := range []string{"valid", "plain", "docker", "relative prefix"} {
		t.Run(mode, func(t *testing.T) {
			raw, pin := externalFixture(t, mode)
			proof, err := inspectExternalImageArchive(context.Background(), bytes.NewReader(raw), int64(len(raw)), pin)
			if err != nil || proof.IndexDigest != pin.IndexDigest || proof.ContentEntries != 4 || len(proof.Layers) != 1 || proof.Layers[0].FileBytes != 4 || proof.Layers[0].Entries != 2 {
				t.Fatal("external archive rejected or measured incorrectly", err, proof)
			}
		})
	}
}

func TestExternalImageArchiveRejectsChangedOrUnsupportedInputs(t *testing.T) {
	for _, mode := range []string{"duplicate platform", "wrong platform", "wrong alias", "extra blob", "missing config", "tampered layer", "trailing", "architecture", "wrong diff", "unsafe layer", "special layer", "relative duplicate", "relative traversal", "gzip concatenation"} {
		t.Run(mode, func(t *testing.T) {
			raw, pin := externalFixture(t, mode)
			proof, err := inspectExternalImageArchive(context.Background(), bytes.NewReader(raw), int64(len(raw)), pin)
			if err != ErrImageArchive || proof.Reference != "" {
				t.Fatal("unsafe proof escaped", err)
			}
		})
	}
	raw, pin := externalFixture(t, "valid")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := inspectExternalImageArchive(ctx, bytes.NewReader(raw), int64(len(raw)), pin); err != ErrImageArchive {
		t.Fatal("cancelled scan accepted")
	}
	if _, err := InspectExternalImageArchive(context.Background(), bytes.NewReader(raw), int64(len(raw)), pin.Reference); err != ErrImageArchive {
		t.Fatal("caller-created pin accepted")
	}
}

func externalInventoryFixture() externalInventoryWire {
	v := externalInventoryWire{Version: 1, Repository: "bunny-lab-io/Borealis", Release: "2026.09.999-rc.1", SourceSHA: imageTestSHA, Platform: "linux-amd64"}
	for i, pin := range ExternalImagePins() {
		p := ExternalImageProof{Reference: pin.Reference, IndexDigest: pin.IndexDigest, ManifestDigest: pin.ManifestDigest, ConfigDigest: pin.ConfigDigest, ArchiveSHA256: strings.Repeat("a", 64), ContentBytes: pin.IndexBytes + pin.ManifestBytes + pin.ConfigBytes + pin.LayerBlobBytes, ContentEntries: int64(3 + pin.LayerCount)}
		remaining := pin.LayerBlobBytes
		for j := 0; j < pin.LayerCount; j++ {
			n := remaining / int64(pin.LayerCount-j)
			remaining -= n
			h := sha256.Sum256([]byte(fmt.Sprint(i, "/", j)))
			d := "sha256:" + hex.EncodeToString(h[:])
			p.Layers = append(p.Layers, ImageLayerProof{Digest: d, DiffID: d, BlobBytes: n, TarBytes: 10240, FileBytes: 4, Entries: 2})
		}
		p.ArchiveBytes = p.ContentBytes + 10240
		v.Images = append(v.Images, p)
	}
	return v
}

func TestExternalImageInventoryBindsCompleteReviewedSetAndCopies(t *testing.T) {
	v := externalInventoryFixture()
	expected := Expected{Repository: v.Repository, Release: v.Release, SourceSHA: v.SourceSHA, AllowQualification: true}
	raw, _ := json.Marshal(v)
	parsed, err := ParseExternalImageInventory(raw, expected)
	if err != nil {
		t.Fatal(err)
	}
	copy := parsed.Images()
	copy[0].Layers[0].Entries++
	if parsed.MatchesArchive(copy[0]) != ErrImageArchive || parsed.MatchesArchive(v.Images[0]) != nil {
		t.Fatal("caller changed retained proof")
	}
	exported, _ := parsed.Export()
	exported[0] = 'x'
	again, _ := parsed.Export()
	if !bytes.Equal(raw, again) {
		t.Fatal("export aliases input")
	}
	for _, mode := range []string{"missing", "order", "digest", "counts", "bytes", "layer", "unknown", "null"} {
		t.Run(mode, func(t *testing.T) {
			next := externalInventoryFixture()
			switch mode {
			case "missing":
				next.Images = next.Images[:len(next.Images)-1]
			case "order":
				slices.Reverse(next.Images)
			case "digest":
				next.Images[0].IndexDigest = "sha256:" + strings.Repeat("b", 64)
			case "counts":
				next.Images[0].ContentEntries++
			case "bytes":
				next.Images[0].ContentBytes--
			case "layer":
				next.Images[0].Layers[0].BlobBytes++
			}
			b, _ := json.Marshal(next)
			if mode == "unknown" {
				b = bytes.Replace(b, []byte(`"version":1`), []byte(`"version":1,"extra":true`), 1)
			}
			if mode == "null" {
				b = bytes.Replace(b, []byte(`"content_bytes":`), []byte(`"content_bytes":null,"unused":`), 1)
			}
			if _, err := ParseExternalImageInventory(b, expected); err != ErrImageArchive {
				t.Fatal("invalid inventory accepted")
			}
		})
	}
	pins := ExternalImagePins()
	names := map[string]bool{}
	for _, p := range pins {
		n := ExternalImageAssetName(p.Reference)
		if n == "" || names[n] {
			t.Fatal("ambiguous asset name")
		}
		names[n] = true
	}
	pins[0].Reference = "caller"
	if ExternalImagePins()[0].Reference == "caller" {
		t.Fatal("pin alias")
	}
}
