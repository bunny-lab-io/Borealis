package clusterbootstrap

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

const imageTestSHA = "0123456789abcdef0123456789abcdef01234567"

func imageFixture(t *testing.T, mode, role string) []byte {
	t.Helper()
	var layer bytes.Buffer
	tw := tar.NewWriter(&layer)
	name := "etc/example"
	if mode == "unsafe layer" {
		name = "../escape"
	}
	if mode == "relative prefix" || mode == "relative duplicate" {
		name = "./etc/example"
	}
	if mode == "relative traversal" {
		name = "./../escape"
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: 4, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write([]byte("data"))
	if mode == "relative duplicate" {
		_ = tw.WriteHeader(&tar.Header{Name: "etc/example", Typeflag: tar.TypeReg, Mode: 0o644})
	}
	if mode == "special layer" {
		_ = tw.WriteHeader(&tar.Header{Name: "device", Typeflag: tar.TypeChar, Devmajor: 1, Devminor: 3})
	}
	_ = tw.WriteHeader(&tar.Header{Name: "run", Typeflag: tar.TypeSymlink, Linkname: "/run"})
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	digest := func(raw []byte) string { v := sha256.Sum256(raw); return "sha256:" + hex.EncodeToString(v[:]) }
	diff := digest(layer.Bytes())
	layerData := bytes.Clone(layer.Bytes())
	media := ociLayerType
	if mode != "plain" {
		var b bytes.Buffer
		g := gzip.NewWriter(&b)
		_, _ = g.Write(layerData)
		_ = g.Close()
		layerData = b.Bytes()
		media += "+gzip"
		if mode == "gzip concatenation" {
			layerData = append(bytes.Clone(layerData), layerData...)
		}
	}
	if mode == "wrong diff" {
		diff = "sha256:" + strings.Repeat("a", 64)
	}
	encode := func(v any) []byte {
		b, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	arch, sha := "amd64", imageTestSHA
	if mode == "architecture" {
		arch = "arm64"
	}
	if mode == "source" {
		sha = strings.Repeat("a", 40)
	}
	config := encode(map[string]any{"architecture": arch, "os": "linux", "config": map[string]any{"Labels": map[string]string{"org.opencontainers.image.revision": sha, "io.borealis.service": role}}, "rootfs": map[string]any{"type": "layers", "diff_ids": []string{diff}}})
	if mode == "duplicate JSON" {
		config = bytes.Replace(config, []byte(`"architecture":"amd64"`), []byte(`"architecture":"amd64","architecture":"arm64"`), 1)
	}
	if mode == "labels alias" {
		config = bytes.Replace(config, []byte(`"Labels"`), []byte(`"labels"`), 1)
	}
	if mode == "invalid UTF8" {
		config = bytes.Replace(config, []byte(`"os":"linux"`), []byte("\"os\":\"linux\",\"extra\":\"\xff\""), 1)
	}
	if mode == "case alias" {
		config = bytes.Replace(config, []byte(`"architecture"`), []byte(`"Architecture"`), 1)
	}
	blobs := map[string][]byte{}
	desc := func(raw []byte, media string) map[string]any {
		d := digest(raw)
		blobs["blobs/sha256/"+strings.TrimPrefix(d, "sha256:")] = raw
		return map[string]any{"mediaType": media, "size": len(raw), "digest": d}
	}
	manifest := encode(map[string]any{"schemaVersion": 2, "mediaType": ociManifestType, "config": desc(config, ociConfigType), "layers": []any{desc(layerData, media)}})
	md := desc(manifest, ociManifestType)
	ref := ImageReference(role, imageTestSHA)
	if mode == "reference" {
		ref += "-other"
	}
	md["annotations"] = map[string]string{"org.opencontainers.image.ref.name": ref}
	if mode == "platform variant" {
		md["platform"] = map[string]string{"os": "linux", "architecture": "amd64", "variant": "v9"}
	}
	manifests := []any{md}
	if mode == "multiple images" {
		manifests = append(manifests, md)
	}
	blobs["index.json"] = encode(map[string]any{"schemaVersion": 2, "manifests": manifests})
	blobs["oci-layout"] = []byte(`{"imageLayoutVersion":"1.0.0"}`)
	if mode == "blob digest" {
		blobs["blobs/sha256/"+strings.TrimPrefix(digest(layerData), "sha256:")] = bytes.Repeat([]byte("x"), len(layerData))
	}
	if mode == "missing blob" {
		delete(blobs, "blobs/sha256/"+strings.TrimPrefix(digest(layerData), "sha256:"))
	}
	if mode == "extra blob" {
		blobs["blobs/sha256/"+strings.Repeat("f", 64)] = []byte("extra")
	}
	if mode == "foreign outer" {
		blobs["../../escape"] = []byte("foreign")
	}
	var archive bytes.Buffer
	outer := tar.NewWriter(&archive)
	for name, raw := range blobs {
		if err := outer.WriteHeader(&tar.Header{Name: name, Size: int64(len(raw)), Mode: 0o644, Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = outer.Write(raw)
		if mode == "duplicate outer" && name == "index.json" {
			_ = outer.WriteHeader(&tar.Header{Name: name, Size: int64(len(raw)), Mode: 0o644, Typeflag: tar.TypeReg})
			_, _ = outer.Write(raw)
		}
	}
	if err := outer.Close(); err != nil {
		t.Fatal(err)
	}
	if mode == "trailing" {
		archive.WriteString("nonzero hidden data")
	}
	return archive.Bytes()
}

func TestImageArchive(t *testing.T) {
	for _, mode := range []string{"gzip", "plain", "relative prefix", "relative duplicate", "relative traversal", "unsafe layer", "special layer", "gzip concatenation", "wrong diff", "architecture", "source", "labels alias", "invalid UTF8", "platform variant", "duplicate JSON", "case alias", "reference", "multiple images", "blob digest", "missing blob", "extra blob", "foreign outer", "duplicate outer", "trailing", "cancelled", "wrong role", "size", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			raw := imageFixture(t, mode, "api-backend")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			role, size := "api-backend", int64(len(raw))
			if mode == "wrong role" {
				role = "foreign"
			}
			if mode == "size" {
				size++
			}
			if mode == "truncated" {
				raw = raw[:len(raw)/2]
				size = int64(len(raw))
			}
			proof, err := InspectImageArchive(ctx, bytes.NewReader(raw), size, role, imageTestSHA)
			good := mode == "gzip" || mode == "plain" || mode == "relative prefix"
			if (err == nil) != good {
				t.Fatalf("accepted=%v error=%v", err == nil, err)
			}
			if good {
				hash := sha256.Sum256(raw)
				if proof.ArchiveSHA256 != hex.EncodeToString(hash[:]) || proof.ArchiveBytes != size || len(proof.Layers) != 1 || proof.Layers[0].Entries != 2 || proof.Layers[0].FileBytes != 4 || proof.Layers[0].TarBytes < 1024 {
					t.Fatal("incorrect measured proof")
				}
			}
		})
	}
}

type imageCancelReader struct {
	io.ReaderAt
	cancel context.CancelFunc
	reads  int
}

func (r *imageCancelReader) ReadAt(p []byte, o int64) (int, error) {
	r.reads++
	if r.reads == 3 {
		r.cancel()
	}
	return r.ReaderAt.ReadAt(p, o)
}
func TestImageArchiveCancellationDuringRead(t *testing.T) {
	raw := imageFixture(t, "gzip", "api-backend")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &imageCancelReader{ReaderAt: bytes.NewReader(raw), cancel: cancel}
	if _, err := InspectImageArchive(ctx, r, int64(len(raw)), "api-backend", imageTestSHA); err == nil {
		t.Fatal("cancelled scan accepted")
	}
}
