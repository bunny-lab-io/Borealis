package clusterbootstrap

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"io"
	"slices"
	"strings"
)

const (
	K3sInventoryName     = "borealis-node-k3s-linux-amd64.json"
	K3sBinaryName        = "borealis-node-k3s-linux-amd64"
	K3sArchiveName       = "borealis-node-k3s-images-linux-amd64.tar"
	MaxK3sInventoryBytes = 16 << 10
)

//go:embed k3s_inputs.lock.json
var k3sInputsLock []byte

type K3sAssetPin struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type K3sInputPins struct {
	Version string      `json:"version"`
	Binary  K3sAssetPin `json:"binary"`
	Archive K3sAssetPin `json:"archive"`
	Images  []string    `json:"images"`
}

// Pins are compiled from reviewed source, never taken from a remote inventory.
// Return a fresh value so callers cannot change the process-wide trust anchor.
func K3sPins() K3sInputPins {
	var p K3sInputPins
	_ = json.Unmarshal(k3sInputsLock, &p)
	return p
}

// Known K3s archive demand only: embedded binary extraction, runtime growth,
// OS packages and external operators still require independent accounting.
type K3sArchiveProof struct {
	ArchiveSHA256   string   `json:"archive_sha256"`
	ArchiveBytes    int64    `json:"archive_bytes"`
	ContentBytes    int64    `json:"content_bytes"`
	ContentEntries  int64    `json:"content_entries"`
	ExpandedBytes   int64    `json:"expanded_bytes"`
	ExpandedEntries int64    `json:"expanded_entries"`
	Images          []string `json:"images"`
}

// InspectK3sArchive accepts only the exact source-pinned upstream archive. It
// does not import or extract it. All OCI blobs are hashed; Docker compatibility
// entries identify the Linux/AMD64 configs/layers whose actual expansion is
// measured. The immutable whole-archive digest also authenticates OCI indexes
// and platform selection. This is deliberately not a general OCI importer.
func InspectK3sArchive(ctx context.Context, source io.ReaderAt, size int64) (K3sArchiveProof, error) {
	return inspectK3sArchive(ctx, source, size, K3sPins())
}

func inspectK3sArchive(ctx context.Context, source io.ReaderAt, size int64, pins K3sInputPins) (K3sArchiveProof, error) {
	fail := func() (K3sArchiveProof, error) { return K3sArchiveProof{}, ErrImageArchive }
	if source == nil || ctx.Err() != nil || size != pins.Archive.Size || size < 1 || size > MaxImageArchiveBytes || len(pins.Images) < 1 || len(pins.Images) > 32 {
		return fail()
	}
	h := sha256.New()
	counter := &imageReadCounter{r: io.TeeReader(io.NewSectionReader(source, 0, size), h), ctx: ctx, limit: size}
	tr := tar.NewReader(counter)
	blobs := map[string]imageBlob{}
	seen := map[string]bool{}
	proof := K3sArchiveProof{ArchiveBytes: size}
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil || len(seen) >= 4096 {
			return fail()
		}
		name := strings.TrimSuffix(header.Name, "/")
		if seen[name] || header.Size < 0 || header.Size > size || len(header.PAXRecords) != 0 {
			return fail()
		}
		seen[name] = true
		if header.Typeflag == tar.TypeDir {
			if header.Size != 0 || name != "blobs" && name != "blobs/sha256" {
				return fail()
			}
			continue
		}
		isBlob := strings.HasPrefix(name, "blobs/sha256/") && digestPattern.MatchString(strings.TrimPrefix(name, "blobs/sha256/"))
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA || !isBlob && name != "manifest.json" && name != "index.json" && name != "oci-layout" {
			return fail()
		}
		blobs[name] = imageBlob{counter.n, header.Size}
		blobHash := sha256.New()
		if _, err := io.Copy(blobHash, tr); err != nil {
			return fail()
		}
		if isBlob {
			if hex.EncodeToString(blobHash.Sum(nil)) != strings.TrimPrefix(name, "blobs/sha256/") {
				return fail()
			}
			proof.ContentBytes += header.Size
			proof.ContentEntries++
		}
	}
	padding := make([]byte, 32<<10)
	for {
		n, err := counter.Read(padding)
		for _, b := range padding[:n] {
			if b != 0 {
				return fail()
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fail()
		}
	}
	proof.ArchiveSHA256 = hex.EncodeToString(h.Sum(nil))
	if counter.n != size || proof.ArchiveSHA256 != pins.Archive.SHA256 {
		return fail()
	}
	read := func(name string) ([]byte, error) {
		b, ok := blobs[name]
		if !ok || b.size > 1<<20 {
			return nil, ErrImageArchive
		}
		return io.ReadAll(&imageReadCounter{r: io.NewSectionReader(source, b.offset, b.size), ctx: ctx, limit: b.size})
	}
	raw, err := read("manifest.json")
	var manifests []struct {
		Config   string   `json:"Config"`
		RepoTags []string `json:"RepoTags"`
		Layers   []string `json:"Layers"`
	}
	if err != nil || imageJSON(raw, &manifests) != nil || len(manifests) != len(pins.Images) {
		return fail()
	}
	for _, manifest := range manifests {
		if len(manifest.RepoTags) != 1 || len(manifest.Layers) < 1 || len(manifest.Layers) > 128 || !slices.Contains(pins.Images, manifest.RepoTags[0]) || slices.Contains(proof.Images, manifest.RepoTags[0]) {
			return fail()
		}
		proof.Images = append(proof.Images, manifest.RepoTags[0])
		configRaw, err := read(manifest.Config)
		var config struct {
			OS           string `json:"os"`
			Architecture string `json:"architecture"`
			RootFS       struct {
				Type    string   `json:"type"`
				DiffIDs []string `json:"diff_ids"`
			} `json:"rootfs"`
		}
		if err != nil || imageJSON(configRaw, &config) != nil || config.OS != "linux" || config.Architecture != "amd64" || config.RootFS.Type != "layers" || len(config.RootFS.DiffIDs) != len(manifest.Layers) {
			return fail()
		}
		for i, name := range manifest.Layers {
			blob, ok := blobs[name]
			if !ok || !strings.HasPrefix(name, "blobs/sha256/") {
				return fail()
			}
			d := imageDescriptor{MediaType: ociLayerType + "+gzip", Digest: "sha256:" + strings.TrimPrefix(name, "blobs/sha256/"), Size: blob.size}
			layer, err := inspectImageLayer(ctx, io.NewSectionReader(source, blob.offset, blob.size), d, config.RootFS.DiffIDs[i])
			if err != nil {
				return fail()
			}
			// Retain duplicate layer demand: no sharing credit across images.
			proof.ExpandedBytes += layer.TarBytes
			proof.ExpandedEntries += layer.Entries
			if proof.ExpandedBytes > 64<<30 || proof.ExpandedEntries > 8*MaxImageEntries {
				return fail()
			}
		}
	}
	slices.Sort(proof.Images)
	if ctx.Err() != nil {
		return fail()
	}
	return proof, nil
}

type k3sInventoryWire struct {
	Version    int             `json:"version"`
	Repository string          `json:"repository"`
	Release    string          `json:"release"`
	SourceSHA  string          `json:"source_sha"`
	Platform   string          `json:"platform"`
	K3sVersion string          `json:"k3s_version"`
	Binary     K3sAssetPin     `json:"binary"`
	Archive    K3sArchiveProof `json:"archive"`
}

type K3sInventory struct {
	wire k3sInventoryWire
	raw  []byte
}

func ParseK3sInventory(raw []byte, expected Expected, version string) (*K3sInventory, error) {
	p := K3sPins()
	var w k3sInventoryWire
	if expected.Validate() != nil || len(raw) > MaxK3sInventoryBytes || version != p.Version || imageFields(raw, []string{"version", "repository", "release", "source_sha", "platform", "k3s_version", "binary", "archive"}, nil) != nil || imageJSON(raw, &w) != nil {
		return nil, ErrImageArchive
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if imageFields(fields["binary"], []string{"name", "size", "sha256"}, nil) != nil || imageFields(fields["archive"], []string{"archive_sha256", "archive_bytes", "content_bytes", "content_entries", "expanded_bytes", "expanded_entries", "images"}, nil) != nil {
		return nil, ErrImageArchive
	}
	a := w.Archive
	if w.Version != 1 || w.Repository != expected.Repository || w.Release != expected.Release || w.SourceSHA != expected.SourceSHA || w.Platform != "linux-amd64" || w.K3sVersion != version || w.Binary != p.Binary || a.ArchiveSHA256 != p.Archive.SHA256 || a.ArchiveBytes != p.Archive.Size || a.ContentBytes < 1 || a.ContentBytes > a.ArchiveBytes || a.ContentEntries < 1 || a.ContentEntries > 4096 || a.ExpandedBytes < 1024 || a.ExpandedBytes > 64<<30 || a.ExpandedEntries < 1 || a.ExpandedEntries > 8*MaxImageEntries || !slices.Equal(a.Images, p.Images) {
		return nil, ErrImageArchive
	}
	return &K3sInventory{wire: w, raw: bytes.Clone(raw)}, nil
}

func (v *K3sInventory) Export() ([]byte, error) {
	if v == nil {
		return nil, ErrImageArchive
	}
	return bytes.Clone(v.raw), nil
}

func (v *K3sInventory) Archive() K3sArchiveProof {
	if v == nil {
		return K3sArchiveProof{}
	}
	a := v.wire.Archive
	a.Images = slices.Clone(a.Images)
	return a
}

func (v *K3sInventory) MatchesArchive(proof K3sArchiveProof) error {
	if v == nil {
		return ErrImageArchive
	}
	a, _ := json.Marshal(v.wire.Archive)
	b, _ := json.Marshal(proof)
	if !bytes.Equal(a, b) {
		return ErrImageArchive
	}
	return nil
}
