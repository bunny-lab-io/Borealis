package clusterbootstrap

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	return acquirePostgresImageReserved(ctx, parent, reference, check, fetch, reservePostgresFile)
}

// Allocation seam is private; production never substitutes sparse truncation.
func acquirePostgresImageReserved(ctx context.Context, parent, reference string, check func(context.Context) error, fetch postgresImageFetch, reserve func(*os.File, int64) error) (_ *PostgresImage, result error) {
	if !ValidPostgresImageReference(reference) || fetch == nil || reserve == nil || imageBoundary(ctx, check) != nil {
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
	reserved := map[string]int64{}
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
		flags := os.O_CREATE | os.O_EXCL | os.O_RDWR
		if size, exists := reserved[d.Digest]; exists {
			if size != d.Size {
				return nil, ErrImageArchive
			}
			flags = os.O_RDWR
		}
		f, err := root.OpenFile(name, flags, 0600)
		if err != nil {
			return nil, ErrImageArchive
		}
		defer f.Close()
		limit := d.Size
		if limit == -1 {
			limit = 1 << 20
		}
		h := sha256.New()
		writer := &postgresDownloadWriter{out: io.MultiWriter(f, h), limit: limit}
		if fetch(ctx, d.Digest, d.Size, manifest, writer) != nil || writer.n < 1 || (d.Size != -1 && writer.n != d.Size) || "sha256:"+hex.EncodeToString(h.Sum(nil)) != d.Digest {
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
	// Authenticate and bound the complete descriptor set before any bulk IO.
	descriptors := map[string]imageDescriptor{}
	for digest, size := range blobs {
		descriptors[digest] = imageDescriptor{Digest: digest, Size: size}
	}
	add := func(d imageDescriptor) bool {
		if old, ok := descriptors[d.Digest]; ok {
			return old.Size == d.Size
		}
		descriptors[d.Digest] = d
		return true
	}
	if _, collision := descriptors[config.Digest]; collision || !add(config) {
		return nil, ErrImageArchive
	}
	var compressed int64
	layers := make([]imageDescriptor, 0, len(manifest.Layers))
	for _, raw := range manifest.Layers {
		var layer imageDescriptor
		if !postgresImageDescriptor(raw, &layer, []string{ociLayerType, ociLayerType + "+gzip", dockerLayerType}, MaxImageArchiveBytes) {
			return nil, ErrImageArchive
		}
		if _, metadata := blobs[layer.Digest]; metadata || layer.Digest == config.Digest || !add(layer) {
			return nil, ErrImageArchive
		}
		compressed += layer.Size
		if compressed > MaxImageArchiveBytes {
			return nil, ErrImageArchive
		}
		layers = append(layers, layer)
	}
	rootWire := map[string]any{"mediaType": rootDesc.MediaType, "digest": rootDesc.Digest, "size": rootDesc.Size, "annotations": map[string]string{"org.opencontainers.image.ref.name": reference, "io.containerd.image.name": reference}}
	index, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": ociIndexType, "manifests": []any{rootWire}})
	metadata := map[string][]byte{"oci-layout": []byte(`{"imageLayoutVersion":"1.0.0"}`), "index.json": index}
	// Fixed short names and bounded lengths use ordinary USTAR headers. Reserve
	// exact padded archive plus every unique content file on the worker filesystem.
	archiveBytes := int64(1024)
	for _, raw := range metadata {
		archiveBytes += 512 + (int64(len(raw))+511)/512*512
	}
	var contentBytes int64
	namesToReserve := make([]string, 0, len(descriptors))
	for digest, d := range descriptors {
		contentBytes += d.Size
		archiveBytes += 512 + (d.Size+511)/512*512
		namesToReserve = append(namesToReserve, digest)
	}
	if archiveBytes > MaxImageArchiveBytes || contentBytes > MaxImageArchiveBytes {
		return nil, ErrImageArchive
	}
	slices.Sort(namesToReserve)
	for _, digest := range namesToReserve {
		if _, exists := blobs[digest]; exists {
			continue
		}
		if imageBoundary(ctx, check) != nil {
			return nil, ErrSessionAuthority
		}
		d := descriptors[digest]
		f, err := root.OpenFile(strings.TrimPrefix(digest, "sha256:"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			return nil, ErrImageArchive
		}
		err = reserve(f, d.Size)
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			return nil, ErrImageArchive
		}
		reserved[digest] = d.Size
	}
	if imageBoundary(ctx, check) != nil {
		return nil, ErrSessionAuthority
	}
	archive, err := root.OpenFile(postgresArchiveName, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, ErrImageArchive
	}
	defer archive.Close()
	if reserve(archive, archiveBytes) != nil || imageBoundary(ctx, check) != nil {
		return nil, ErrImageArchive
	}
	if _, err = download(config, false); err != nil {
		return nil, err
	}
	for _, layer := range layers {
		if _, err = download(layer, false); err != nil {
			return nil, err
		}
	}
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
	if err != nil || st.Size() != archiveBytes {
		return nil, ErrImageArchive
	}
	position, err := archive.Seek(0, io.SeekCurrent)
	if err != nil || position != archiveBytes {
		return nil, ErrImageArchive
	}
	proof, err := InspectPostgresImageArchive(ctx, archive, st.Size(), reference)
	if err != nil || proof.ContentBytes != contentBytes || proof.ContentEntries != int64(len(descriptors)) || imageBoundary(ctx, check) != nil {
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

// Bound every fetch independently of registry implementation and authenticate
// bytes before parsing metadata or trusting their allocation instructions.
type postgresDownloadWriter struct {
	out      io.Writer
	limit, n int64
}

func (w *postgresDownloadWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.limit-w.n {
		return 0, ErrImageArchive
	}
	n, err := w.out.Write(p)
	w.n += int64(n)
	return n, err
}
