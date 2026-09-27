package clusterbootstrap

import (
	"archive/tar"
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
	ExternalInventoryName = "borealis-node-external-images-linux-amd64.json"
	ociIndexType          = "application/vnd.oci.image.index.v1+json"
	dockerIndexType       = "application/vnd.docker.distribution.manifest.list.v2+json"
	dockerManifestType    = "application/vnd.docker.distribution.manifest.v2+json"
	dockerConfigType      = "application/vnd.docker.container.image.v1+json"
	dockerLayerType       = "application/vnd.docker.image.rootfs.diff.tar.gzip"
)

//go:embed external_images.lock.json
var externalImageLock []byte

type ExternalImagePin struct {
	Reference      string `json:"reference"`
	IndexDigest    string `json:"index_digest"`
	IndexBytes     int64  `json:"index_bytes"`
	ManifestDigest string `json:"manifest_digest"`
	ManifestBytes  int64  `json:"manifest_bytes"`
	ConfigDigest   string `json:"config_digest"`
	ConfigBytes    int64  `json:"config_bytes"`
	LayerCount     int    `json:"layer_count"`
	LayerBlobBytes int64  `json:"layer_blob_bytes"`
}

// Reviewed source pins, never a registry response or caller-owned allocation.
func ExternalImagePins() []ExternalImagePin {
	var lock struct {
		Components []struct {
			Images []ExternalImagePin `json:"images"`
		} `json:"components"`
	}
	if json.Unmarshal(externalImageLock, &lock) != nil {
		return nil
	}
	var pins []ExternalImagePin
	for _, component := range lock.Components {
		pins = append(pins, component.Images...)
	}
	slices.SortFunc(pins, func(a, b ExternalImagePin) int { return strings.Compare(a.Reference, b.Reference) })
	return pins
}

func ExternalImageAssetName(reference string) string {
	for _, pin := range ExternalImagePins() {
		if pin.Reference == reference {
			// Repository basenames are unique in the reviewed fixed image set.
			name := strings.Split(strings.Split(reference, "@")[0], ":")[0]
			return "borealis-node-external-" + name[strings.LastIndex(name, "/")+1:] + "-linux-amd64.oci.tar"
		}
	}
	return ""
}

type ExternalImageProof struct {
	Reference      string            `json:"reference"`
	ArchiveSHA256  string            `json:"archive_sha256"`
	ArchiveBytes   int64             `json:"archive_bytes"`
	IndexDigest    string            `json:"index_digest"`
	ManifestDigest string            `json:"manifest_digest"`
	ConfigDigest   string            `json:"config_digest"`
	ContentBytes   int64             `json:"content_bytes"`
	ContentEntries int64             `json:"content_entries"`
	Layers         []ImageLayerProof `json:"layers"`
}

// Authenticate all included bytes without extraction/import/execution. Keep the
// original upstream index, but materialize only the reviewed Linux/AMD64 child.
// Foreign-platform descriptors can remain unmaterialized; foreign included
// blobs cannot. Source PostgreSQL is separately acquired, not a static input.
func InspectExternalImageArchive(ctx context.Context, source io.ReaderAt, size int64, reference string) (ExternalImageProof, error) {
	for _, pin := range ExternalImagePins() {
		if pin.Reference == reference {
			return inspectExternalImageArchive(ctx, source, size, pin)
		}
	}
	return ExternalImageProof{}, ErrImageArchive
}

func inspectExternalImageArchive(ctx context.Context, source io.ReaderAt, size int64, pin ExternalImagePin) (ExternalImageProof, error) {
	fail := func() (ExternalImageProof, error) { return ExternalImageProof{}, ErrImageArchive }
	if ctx.Err() != nil || source == nil || size < 1 || size > MaxImageArchiveBytes {
		return fail()
	}
	h := sha256.New()
	counter := &imageReadCounter{r: io.TeeReader(io.NewSectionReader(source, 0, size), h), ctx: ctx, limit: size}
	tr := tar.NewReader(counter)
	blobs, seen := map[string]imageBlob{}, map[string]bool{}
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil || len(seen) >= 260 {
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
		blob := strings.HasPrefix(name, "blobs/sha256/") && digestPattern.MatchString(strings.TrimPrefix(name, "blobs/sha256/"))
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA || !blob && name != "oci-layout" && name != "index.json" {
			return fail()
		}
		blobs[name] = imageBlob{counter.n, header.Size}
		bh := sha256.New()
		if _, err := io.Copy(bh, tr); err != nil || blob && hex.EncodeToString(bh.Sum(nil)) != strings.TrimPrefix(name, "blobs/sha256/") {
			return fail()
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
	if counter.n != size {
		return fail()
	}
	read := func(name string) ([]byte, error) {
		blob, ok := blobs[name]
		if !ok || blob.size < 1 || blob.size > 1<<20 {
			return nil, ErrImageArchive
		}
		return io.ReadAll(&imageReadCounter{r: io.NewSectionReader(source, blob.offset, blob.size), ctx: ctx, limit: blob.size})
	}
	layout, err := read("oci-layout")
	var lv map[string]string
	if err != nil || imageJSON(layout, &lv) != nil || len(lv) != 1 || lv["imageLayoutVersion"] != "1.0.0" {
		return fail()
	}
	visited := map[string]bool{"oci-layout": true, "index.json": true}
	checkBlob := func(d imageDescriptor) bool {
		name := "blobs/sha256/" + strings.TrimPrefix(d.Digest, "sha256:")
		b, ok := blobs[name]
		if !validImageDigest(d.Digest) || !ok || d.Size < 1 || b.size != d.Size {
			return false
		}
		visited[name] = true
		return true
	}
	parseDescriptor := func(raw json.RawMessage) (imageDescriptor, bool) {
		var d imageDescriptor
		if imageFields(raw, []string{"mediaType", "digest", "size"}, []string{"annotations", "platform"}) != nil || imageJSON(raw, &d) != nil || !validImageDigest(d.Digest) || d.Size < 1 {
			return d, false
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		if platform, ok := fields["platform"]; ok && (imageFields(platform, []string{"os", "architecture"}, nil) != nil || d.Platform == nil || d.Platform.OS != "linux" || d.Platform.Architecture != "amd64") {
			return d, false
		}
		return d, true
	}
	type indexValue struct {
		SchemaVersion int               `json:"schemaVersion"`
		MediaType     string            `json:"mediaType"`
		Manifests     []json.RawMessage `json:"manifests"`
	}
	indexRaw, err := read("index.json")
	var index indexValue
	if err != nil || imageFields(indexRaw, []string{"schemaVersion", "manifests"}, []string{"mediaType", "annotations"}) != nil || imageJSON(indexRaw, &index) != nil || index.SchemaVersion != 2 || index.MediaType != "" && index.MediaType != ociIndexType || len(index.Manifests) != 1 {
		return fail()
	}
	root, ok := parseDescriptor(index.Manifests[0])
	if !ok || !slices.Contains([]string{ociIndexType, dockerIndexType}, root.MediaType) || root.Digest != pin.IndexDigest || root.Size != pin.IndexBytes || !checkBlob(root) || root.Annotations["org.opencontainers.image.ref.name"] != pin.Reference || root.Annotations["io.containerd.image.name"] != "" && root.Annotations["io.containerd.image.name"] != pin.Reference {
		return fail()
	}
	upstreamRaw, err := read("blobs/sha256/" + strings.TrimPrefix(root.Digest, "sha256:"))
	var upstream indexValue
	if err != nil || imageJSON(upstreamRaw, &upstream) != nil || upstream.SchemaVersion != 2 || upstream.MediaType != root.MediaType || len(upstream.Manifests) < 1 || len(upstream.Manifests) > 64 {
		return fail()
	}
	var manifestDesc imageDescriptor
	matches := 0
	for _, raw := range upstream.Manifests {
		var d imageDescriptor
		if imageJSON(raw, &d) != nil || d.Platform == nil || !validImageDigest(d.Digest) || d.Size < 1 || d.Size > 1<<20 || !slices.Contains([]string{ociManifestType, dockerManifestType}, d.MediaType) {
			return fail()
		}
		if d.Platform.OS != "linux" || d.Platform.Architecture != "amd64" {
			continue
		}
		d, ok := parseDescriptor(raw)
		if !ok || !slices.Contains([]string{ociManifestType, dockerManifestType}, d.MediaType) || d.Digest != pin.ManifestDigest || d.Size != pin.ManifestBytes || !checkBlob(d) {
			return fail()
		}
		manifestDesc, matches = d, matches+1
	}
	if matches != 1 {
		return fail()
	}
	manifestRaw, err := read("blobs/sha256/" + strings.TrimPrefix(manifestDesc.Digest, "sha256:"))
	var manifest struct {
		SchemaVersion int               `json:"schemaVersion"`
		MediaType     string            `json:"mediaType"`
		Config        json.RawMessage   `json:"config"`
		Layers        []json.RawMessage `json:"layers"`
	}
	if err != nil || imageFields(manifestRaw, []string{"schemaVersion", "mediaType", "config", "layers"}, []string{"annotations"}) != nil || imageJSON(manifestRaw, &manifest) != nil || manifest.SchemaVersion != 2 || manifest.MediaType != manifestDesc.MediaType || len(manifest.Layers) != pin.LayerCount || pin.LayerCount < 1 || pin.LayerCount > 128 {
		return fail()
	}
	configDesc, ok := parseDescriptor(manifest.Config)
	if !ok || !slices.Contains([]string{ociConfigType, dockerConfigType}, configDesc.MediaType) || configDesc.Digest != pin.ConfigDigest || configDesc.Size != pin.ConfigBytes || !checkBlob(configDesc) {
		return fail()
	}
	configRaw, err := read("blobs/sha256/" + strings.TrimPrefix(configDesc.Digest, "sha256:"))
	var config struct {
		Architecture string `json:"architecture"`
		OS           string `json:"os"`
		RootFS       struct {
			Type    string   `json:"type"`
			DiffIDs []string `json:"diff_ids"`
		} `json:"rootfs"`
	}
	var cf map[string]json.RawMessage
	if err != nil || imageJSON(configRaw, &cf) != nil || imageFields(cf["rootfs"], []string{"type", "diff_ids"}, nil) != nil || imageJSON(configRaw, &config) != nil || config.OS != "linux" || config.Architecture != "amd64" || config.RootFS.Type != "layers" || len(config.RootFS.DiffIDs) != pin.LayerCount {
		return fail()
	}
	proof := ExternalImageProof{Reference: pin.Reference, ArchiveSHA256: hex.EncodeToString(h.Sum(nil)), ArchiveBytes: size, IndexDigest: root.Digest, ManifestDigest: manifestDesc.Digest, ConfigDigest: configDesc.Digest, Layers: []ImageLayerProof{}}
	var expanded, entries, compressed int64
	for i, raw := range manifest.Layers {
		d, ok := parseDescriptor(raw)
		if !ok || !slices.Contains([]string{ociLayerType, ociLayerType + "+gzip", dockerLayerType}, d.MediaType) || !checkBlob(d) || !validImageDigest(config.RootFS.DiffIDs[i]) {
			return fail()
		}
		blob := blobs["blobs/sha256/"+strings.TrimPrefix(d.Digest, "sha256:")]
		if d.MediaType == dockerLayerType {
			d.MediaType = ociLayerType + "+gzip"
		}
		layer, err := inspectImageLayer(ctx, io.NewSectionReader(source, blob.offset, blob.size), d, config.RootFS.DiffIDs[i])
		if err != nil {
			return fail()
		}
		expanded, entries, compressed = expanded+layer.TarBytes, entries+layer.Entries, compressed+layer.BlobBytes
		if expanded > MaxImageExpandedBytes || entries > MaxImageEntries || compressed > MaxImageArchiveBytes {
			return fail()
		}
		proof.Layers = append(proof.Layers, layer)
	}
	if compressed != pin.LayerBlobBytes || len(visited) != len(blobs) || ctx.Err() != nil {
		return fail()
	}
	for name, blob := range blobs {
		if strings.HasPrefix(name, "blobs/") {
			proof.ContentEntries++
			proof.ContentBytes += blob.size
		}
	}
	return proof, nil
}
