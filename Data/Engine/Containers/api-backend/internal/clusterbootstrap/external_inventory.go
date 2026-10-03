package clusterbootstrap

import (
	"bytes"
	"encoding/json"
	"slices"
)

type externalInventoryWire struct {
	Version    int                  `json:"version"`
	Repository string               `json:"repository"`
	Release    string               `json:"release"`
	SourceSHA  string               `json:"source_sha"`
	Platform   string               `json:"platform"`
	Images     []ExternalImageProof `json:"images"`
}

// Static external images only. Dynamic PostgreSQL, OS/runtime growth, source
// image authority and placement remain separate requirements for readiness.
type ExternalImageInventory struct {
	wire externalInventoryWire
	raw  []byte
}

func ParseExternalImageInventory(raw []byte, expected Expected) (*ExternalImageInventory, error) {
	if expected.Validate() != nil || len(raw) == 0 || len(raw) > MaxImageInventoryBytes || imageFields(raw, []string{"version", "repository", "release", "source_sha", "platform", "images"}, nil) != nil {
		return nil, ErrImageArchive
	}
	var wire externalInventoryWire
	pins := ExternalImagePins()
	if imageJSON(raw, &wire) != nil || wire.Version != 1 || wire.Repository != expected.Repository || wire.Release != expected.Release || wire.SourceSHA != expected.SourceSHA || wire.Platform != "linux-amd64" || len(wire.Images) != len(pins) || len(pins) != 23 {
		return nil, ErrImageArchive
	}
	var outer struct {
		Images []json.RawMessage `json:"images"`
	}
	_ = json.Unmarshal(raw, &outer)
	for i, proof := range wire.Images {
		pin := pins[i]
		if imageFields(outer.Images[i], []string{"reference", "archive_sha256", "archive_bytes", "index_digest", "manifest_digest", "config_digest", "content_bytes", "content_entries", "layers"}, nil) != nil ||
			proof.Reference != pin.Reference || proof.IndexDigest != pin.IndexDigest || proof.ManifestDigest != pin.ManifestDigest || proof.ConfigDigest != pin.ConfigDigest ||
			!digestPattern.MatchString(proof.ArchiveSHA256) || proof.ArchiveBytes < 1 || proof.ArchiveBytes > MaxImageArchiveBytes || len(proof.Layers) != pin.LayerCount {
			return nil, ErrImageArchive
		}
		var fields struct {
			Layers []json.RawMessage `json:"layers"`
		}
		_ = json.Unmarshal(outer.Images[i], &fields)
		content := map[string]int64{pin.IndexDigest: pin.IndexBytes, pin.ManifestDigest: pin.ManifestBytes, pin.ConfigDigest: pin.ConfigBytes}
		layers := map[string]ImageLayerProof{}
		var expanded, entries, compressed int64
		for j, layer := range proof.Layers {
			if imageFields(fields.Layers[j], []string{"digest", "diff_id", "blob_bytes", "tar_bytes", "file_bytes", "entries"}, nil) != nil ||
				!validImageDigest(layer.Digest) || !validImageDigest(layer.DiffID) || layer.BlobBytes < 1 || layer.BlobBytes > MaxImageArchiveBytes || layer.TarBytes < 1024 || layer.TarBytes > MaxImageExpandedBytes || layer.FileBytes < 0 || layer.FileBytes > layer.TarBytes || layer.Entries < 0 || layer.Entries > MaxImageEntries {
				return nil, ErrImageArchive
			}
			if prior, ok := layers[layer.Digest]; ok {
				if prior != layer {
					return nil, ErrImageArchive
				}
			} else if _, exists := content[layer.Digest]; exists {
				return nil, ErrImageArchive
			}
			layers[layer.Digest], content[layer.Digest] = layer, layer.BlobBytes
			expanded, entries, compressed = expanded+layer.TarBytes, entries+layer.Entries, compressed+layer.BlobBytes
			if expanded > MaxImageExpandedBytes || entries > MaxImageEntries || compressed > MaxImageArchiveBytes {
				return nil, ErrImageArchive
			}
		}
		var contentBytes int64
		for _, size := range content {
			contentBytes += size
		}
		if compressed != pin.LayerBlobBytes || proof.ContentBytes != contentBytes || proof.ContentEntries != int64(len(content)) || contentBytes > proof.ArchiveBytes {
			return nil, ErrImageArchive
		}
	}
	return &ExternalImageInventory{wire: wire, raw: bytes.Clone(raw)}, nil
}

func (v *ExternalImageInventory) Images() []ExternalImageProof {
	if v == nil {
		return nil
	}
	out := slices.Clone(v.wire.Images)
	for i := range out {
		out[i].Layers = slices.Clone(out[i].Layers)
	}
	return out
}

func (v *ExternalImageInventory) Export() ([]byte, error) {
	if v == nil || len(v.raw) == 0 {
		return nil, ErrImageArchive
	}
	return bytes.Clone(v.raw), nil
}

func (v *ExternalImageInventory) MatchesArchive(proof ExternalImageProof) error {
	if v == nil || len(v.raw) == 0 {
		return ErrImageArchive
	}
	for _, expected := range v.wire.Images {
		if expected.Reference == proof.Reference {
			a, _ := json.Marshal(expected)
			b, _ := json.Marshal(proof)
			if bytes.Equal(a, b) {
				return nil
			}
		}
	}
	return ErrImageArchive
}
