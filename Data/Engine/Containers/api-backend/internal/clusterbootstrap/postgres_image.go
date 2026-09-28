package clusterbootstrap

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"slices"
	"strings"
	"time"
)

const postgresArchiveName = "borealis-node-postgresql-linux-amd64.oci.tar"

// PostgreSQL is not a compiled static image. Its exact root digest must come
// from current source authority; this function does not establish that authority.
func InspectPostgresImageArchive(ctx context.Context, source io.ReaderAt, size int64, reference string) (ExternalImageProof, error) {
	if !ValidPostgresImageReference(reference) {
		return ExternalImageProof{}, ErrImageArchive
	}
	return inspectDependencyImageArchive(ctx, source, size, reference, strings.TrimPrefix(reference, PostgresImageRepository+"@"), nil)
}

// One inert archive, with the same guarded transfer/cleanup as static images.
// No filesystem path, admission grant or import capability escapes.
type PostgresImage struct {
	set   *ExternalImageSet
	proof ExternalImageProof
}

func (*PostgresImage) String() string               { return "staged PostgreSQL image [private]" }
func (*PostgresImage) GoString() string             { return "staged PostgreSQL image [private]" }
func (*PostgresImage) MarshalJSON() ([]byte, error) { return nil, ErrImageArchive }
func (v *PostgresImage) Proof() ExternalImageProof {
	if v == nil {
		return ExternalImageProof{}
	}
	p := v.proof
	p.Layers = slices.Clone(p.Layers)
	return p
}
func (v *PostgresImage) Close() error {
	if v == nil {
		return nil
	}
	return v.set.Close()
}
func (v *PostgresImage) WriteArchive(ctx context.Context, out io.Writer, check func(context.Context) error) error {
	if v == nil {
		return ErrImageArchive
	}
	return v.set.WriteArchive(ctx, v.proof.Reference, out, check)
}

// AcquirePostgresImage uses anonymous digest-only GHCR reads. Caller owns a
// joined lease heartbeat and checks source/cohort/claim at acquisition boundaries.
// Failed acquisition removes only its new scratch; no image is imported/executed.
func AcquirePostgresImage(ctx context.Context, parent, reference string, check func(context.Context) error) (*PostgresImage, error) {
	if !ValidPostgresImageReference(reference) || imageBoundary(ctx, check) != nil {
		return nil, ErrImageArchive
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	registry := newPostgresRegistry()
	defer registry.close()
	return acquirePostgresImage(ctx, parent, reference, check, registry.fetch)
}

type postgresImageFetch func(context.Context, string, int64, bool, io.Writer) error

func acquirePostgresImage(ctx context.Context, parent, reference string, check func(context.Context) error, fetch postgresImageFetch) (_ *PostgresImage, result error) {
	if !ValidPostgresImageReference(reference) || fetch == nil || imageBoundary(ctx, check) != nil {
		return nil, ErrImageArchive
	}
	directory, err := os.MkdirTemp(parent, "borealis-node-postgresql-")
	if err != nil {
		return nil, ErrImageArchive
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		_ = os.RemoveAll(directory)
		return nil, ErrImageArchive
	}
	set := &ExternalImageSet{path: directory, root: root, names: map[string]string{reference: postgresArchiveName}}
	defer func() {
		if result != nil {
			_ = set.Close()
		}
	}()
	blobs := map[string]int64{}
	var total int64
	// Metadata and layer downloads share exact digest/length verification. No
	// fetch URL or metadata-supplied URL can influence the fixed registry client.
	download := func(d imageDescriptor, manifest bool) ([]byte, error) {
		if imageBoundary(ctx, check) != nil {
			return nil, ErrSessionAuthority
		}
		if n, exists := blobs[d.Digest]; exists {
			if n != d.Size {
				return nil, ErrImageArchive
			}
			return nil, nil
		}
		name := strings.TrimPrefix(d.Digest, "sha256:")
		f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			return nil, ErrImageArchive
		}
		defer f.Close()
		if fetch(ctx, d.Digest, d.Size, manifest, f) != nil {
			return nil, ErrImageArchive
		}
		st, err := f.Stat()
		if err != nil || st.Size() < 1 || st.Size() > MaxImageArchiveBytes {
			return nil, ErrImageArchive
		}
		total += st.Size() + 512 + 511
		if total+10240 > MaxImageArchiveBytes || f.Sync() != nil || imageBoundary(ctx, check) != nil {
			return nil, ErrImageArchive
		}
		blobs[d.Digest] = st.Size()
		if !manifest {
			return nil, nil
		}
		if st.Size() > 1<<20 {
			return nil, ErrImageArchive
		}
		raw, err := io.ReadAll(io.NewSectionReader(f, 0, st.Size()))
		if err != nil {
			return nil, ErrImageArchive
		}
		return raw, nil
	}
	digest := strings.TrimPrefix(reference, PostgresImageRepository+"@")
	raw, err := download(imageDescriptor{Digest: digest, Size: -1}, true)
	if err != nil {
		return nil, err
	}
	var document struct {
		SchemaVersion int               `json:"schemaVersion"`
		MediaType     string            `json:"mediaType"`
		Manifests     []json.RawMessage `json:"manifests"`
	}
	if imageJSON(raw, &document) != nil || document.SchemaVersion != 2 {
		return nil, ErrImageArchive
	}
	rootDesc := imageDescriptor{MediaType: document.MediaType, Digest: digest, Size: int64(len(raw))}
	manifestRaw := raw
	if slices.Contains([]string{ociIndexType, dockerIndexType}, document.MediaType) {
		if len(document.Manifests) < 1 || len(document.Manifests) > 64 {
			return nil, ErrImageArchive
		}
		var selected imageDescriptor
		matches := 0
		for _, raw := range document.Manifests {
			var d imageDescriptor
			if imageJSON(raw, &d) != nil || d.Platform == nil || !validImageDigest(d.Digest) || d.Size < 1 || d.Size > 1<<20 || !slices.Contains([]string{ociManifestType, dockerManifestType}, d.MediaType) {
				return nil, ErrImageArchive
			}
			if d.Platform.OS == "linux" && d.Platform.Architecture == "amd64" {
				if !postgresImageDescriptor(raw, &d, []string{ociManifestType, dockerManifestType}, 1<<20) {
					return nil, ErrImageArchive
				}
				selected = d
				matches++
			}
		}
		if matches != 1 {
			return nil, ErrImageArchive
		}
		manifestRaw, err = download(selected, true)
		if err != nil {
			return nil, err
		}
	} else if !slices.Contains([]string{ociManifestType, dockerManifestType}, document.MediaType) {
		return nil, ErrImageArchive
	}
	var manifest struct {
		SchemaVersion int               `json:"schemaVersion"`
		MediaType     string            `json:"mediaType"`
		Config        json.RawMessage   `json:"config"`
		Layers        []json.RawMessage `json:"layers"`
	}
	if imageFields(manifestRaw, []string{"schemaVersion", "mediaType", "config", "layers"}, []string{"annotations"}) != nil || imageJSON(manifestRaw, &manifest) != nil || manifest.SchemaVersion != 2 || !slices.Contains([]string{ociManifestType, dockerManifestType}, manifest.MediaType) || len(manifest.Layers) < 1 || len(manifest.Layers) > 128 {
		return nil, ErrImageArchive
	}
	var config imageDescriptor
	if !postgresImageDescriptor(manifest.Config, &config, []string{ociConfigType, dockerConfigType}, 1<<20) {
		return nil, ErrImageArchive
	}
	if _, err = download(config, false); err != nil {
		return nil, err
	}
	var compressed int64
	for _, raw := range manifest.Layers {
		var layer imageDescriptor
		if !postgresImageDescriptor(raw, &layer, []string{ociLayerType, ociLayerType + "+gzip", dockerLayerType}, MaxImageArchiveBytes) {
			return nil, ErrImageArchive
		}
		compressed += layer.Size
		if compressed > MaxImageArchiveBytes {
			return nil, ErrImageArchive
		}
		if _, err = download(layer, false); err != nil {
			return nil, err
		}
	}
	rootWire := map[string]any{"mediaType": rootDesc.MediaType, "digest": rootDesc.Digest, "size": rootDesc.Size, "annotations": map[string]string{"org.opencontainers.image.ref.name": reference, "io.containerd.image.name": reference}}
	index, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": ociIndexType, "manifests": []any{rootWire}})
	metadata := map[string][]byte{"oci-layout": []byte(`{"imageLayoutVersion":"1.0.0"}`), "index.json": index}
	archive, err := root.OpenFile(postgresArchiveName, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, ErrImageArchive
	}
	defer archive.Close()
	tw := tar.NewWriter(archive)
	names := []string{"oci-layout", "index.json"}
	for digest := range blobs {
		names = append(names, "blobs/sha256/"+strings.TrimPrefix(digest, "sha256:"))
	}
	slices.Sort(names)
	for _, name := range names {
		var input io.Reader
		var size int64
		var f *os.File
		if raw, ok := metadata[name]; ok {
			input = bytes.NewReader(raw)
			size = int64(len(raw))
		} else {
			suffix := strings.TrimPrefix(name, "blobs/sha256/")
			f, err = root.Open(suffix)
			if err != nil {
				return nil, ErrImageArchive
			}
			input = f
			size = blobs["sha256:"+suffix]
		}
		err = tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: size, Typeflag: tar.TypeReg})
		var n int64
		if err == nil {
			n, err = io.Copy(tw, io.LimitReader(contextReader{ctx, input}, size+1))
		}
		if f != nil {
			_ = f.Close()
		}
		if err != nil || n != size {
			return nil, ErrImageArchive
		}
	}
	if tw.Close() != nil || archive.Sync() != nil {
		return nil, ErrImageArchive
	}
	st, err := archive.Stat()
	if err != nil {
		return nil, ErrImageArchive
	}
	proof, err := InspectPostgresImageArchive(ctx, archive, st.Size(), reference)
	if err != nil || imageBoundary(ctx, check) != nil {
		return nil, ErrImageArchive
	}
	for digest := range blobs {
		if root.Remove(strings.TrimPrefix(digest, "sha256:")) != nil {
			return nil, ErrImageArchive
		}
	}
	if imageBoundary(ctx, check) != nil {
		return nil, ErrSessionAuthority
	}
	wire := externalInventoryWire{Images: []ExternalImageProof{proof}}
	encoded, _ := json.Marshal(wire)
	set.inventory = &ExternalImageInventory{wire: wire, raw: encoded}
	return &PostgresImage{set: set, proof: proof}, nil
}

func postgresImageDescriptor(raw []byte, d *imageDescriptor, types []string, limit int64) bool {
	if imageFields(raw, []string{"mediaType", "digest", "size"}, []string{"annotations", "platform"}) != nil || imageJSON(raw, d) != nil || !validImageDigest(d.Digest) || d.Size < 1 || d.Size > limit || !slices.Contains(types, d.MediaType) {
		return false
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if platform, ok := fields["platform"]; ok {
		return imageFields(platform, []string{"os", "architecture"}, nil) == nil && d.Platform != nil && d.Platform.OS == "linux" && d.Platform.Architecture == "amd64"
	}
	return true
}
