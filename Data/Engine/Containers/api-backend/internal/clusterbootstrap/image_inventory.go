package clusterbootstrap

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
)

type imageInventoryWire struct {
	Version    int                 `json:"version"`
	Repository string              `json:"repository"`
	Release    string              `json:"release"`
	SourceSHA  string              `json:"source_sha"`
	Platform   string              `json:"platform"`
	Images     []ImageArchiveProof `json:"images"`
}

// ImageInventory binds measured application images to an independently resolved
// immutable release. It excludes OS/K3s/external images and runtime growth;
// callers cannot interpret it as a complete installation-capacity certificate.
type ImageInventory struct {
	wire imageInventoryWire
	raw  []byte
}

func ParseImageInventory(raw []byte, expected Expected) (*ImageInventory, error) {
	if expected.Validate() != nil || len(raw) == 0 || len(raw) > MaxImageInventoryBytes || imageFields(raw, []string{"version", "repository", "release", "source_sha", "platform", "images"}, nil) != nil {
		return nil, ErrImageArchive
	}
	var v imageInventoryWire
	if imageJSON(raw, &v) != nil || v.Version != 1 || v.Repository != expected.Repository || v.Release != expected.Release || v.SourceSHA != expected.SourceSHA || v.Platform != "linux-amd64" || len(v.Images) != len(imageRoles) {
		return nil, ErrImageArchive
	}
	var outer struct {
		Images []json.RawMessage `json:"images"`
	}
	_ = json.Unmarshal(raw, &outer)
	for i, proof := range v.Images {
		if imageFields(outer.Images[i], []string{"role", "image", "archive_sha256", "archive_bytes", "manifest_digest", "config_digest", "layers"}, nil) != nil || proof.Role != imageRoles[i] || proof.Image != ImageReference(proof.Role, expected.SourceSHA) || !digestPattern.MatchString(proof.ArchiveSHA256) || proof.ArchiveBytes < 1 || proof.ArchiveBytes > MaxImageArchiveBytes || len(proof.Layers) < 1 || len(proof.Layers) > 128 {
			return nil, ErrImageArchive
		}
		for _, digest := range []string{proof.ManifestDigest, proof.ConfigDigest} {
			if !validImageDigest(digest) {
				return nil, ErrImageArchive
			}
		}
		var object struct {
			Layers []json.RawMessage `json:"layers"`
		}
		_ = json.Unmarshal(outer.Images[i], &object)
		var expanded, entries, blobs int64
		for j, layer := range proof.Layers {
			if imageFields(object.Layers[j], []string{"digest", "diff_id", "blob_bytes", "tar_bytes", "file_bytes", "entries"}, nil) != nil || !validImageDigest(layer.Digest) || !validImageDigest(layer.DiffID) || layer.BlobBytes < 1 || layer.BlobBytes > MaxImageArchiveBytes || layer.TarBytes < 1024 || layer.TarBytes > MaxImageExpandedBytes || layer.FileBytes < 0 || layer.FileBytes > layer.TarBytes || layer.Entries < 0 || layer.Entries > MaxImageEntries {
				return nil, ErrImageArchive
			}
			expanded += layer.TarBytes
			entries += layer.Entries
			blobs += layer.BlobBytes
			if expanded > MaxImageExpandedBytes || entries > MaxImageEntries || blobs > proof.ArchiveBytes {
				return nil, ErrImageArchive
			}
		}
	}
	return &ImageInventory{wire: v, raw: bytes.Clone(raw)}, nil
}

func validImageDigest(value string) bool {
	return strings.HasPrefix(value, "sha256:") && digestPattern.MatchString(strings.TrimPrefix(value, "sha256:"))
}
func (v *ImageInventory) Images() []ImageArchiveProof {
	if v == nil {
		return nil
	}
	r := slices.Clone(v.wire.Images)
	for i := range r {
		r[i].Layers = slices.Clone(r[i].Layers)
	}
	return r
}
func (v *ImageInventory) Export() ([]byte, error) {
	if v == nil || len(v.raw) == 0 {
		return nil, ErrImageArchive
	}
	return bytes.Clone(v.raw), nil
}

// MatchesArchive compares every measured byte/count/digest after download. A
// previous proof or caller-supplied statistics cannot replace this inspection.
func (v *ImageInventory) MatchesArchive(proof ImageArchiveProof) error {
	if v == nil || len(v.raw) == 0 {
		return ErrImageArchive
	}
	for _, expected := range v.wire.Images {
		if expected.Role == proof.Role {
			a, _ := json.Marshal(expected)
			b, _ := json.Marshal(proof)
			if bytes.Equal(a, b) {
				return nil
			}
			break
		}
	}
	return ErrImageArchive
}
