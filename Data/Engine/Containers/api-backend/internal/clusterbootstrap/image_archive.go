package clusterbootstrap

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	ImageInventoryName           = "borealis-node-images-linux-amd64.json"
	MaxImageInventoryBytes       = 512 << 10
	MaxImageArchiveBytes   int64 = 2<<30 - 1
	MaxImageExpandedBytes  int64 = 32 << 30
	MaxImageEntries        int64 = 1000000
	ociManifestType              = "application/vnd.oci.image.manifest.v1+json"
	ociConfigType                = "application/vnd.oci.image.config.v1+json"
	ociLayerType                 = "application/vnd.oci.image.layer.v1.tar"
)

var ErrImageArchive = errors.New("immutable image evidence invalid or changed; private diagnostics withheld")

var imageRoles = []string{"api-backend", "borealis-operator", "job-scheduler", "postgres-db", "remote-desktop-guacd", "site-worker", "traefik-edge", "webui-frontend", "wireguard-tunnel"}

func ImageRoles() []string { return slices.Clone(imageRoles) }
func ImageAssetName(role string) string {
	return "borealis-node-image-" + role + "-linux-amd64.oci.tar"
}
func ImageReference(role, sourceSHA string) string {
	return "docker.io/borealis-engine/" + role + ":release-" + sourceSHA
}

// ImageArchiveProof is measured from all archive bytes. It is public metadata,
// not publication authority, permission to import, or total host disk demand.
type ImageArchiveProof struct {
	Role           string            `json:"role"`
	Image          string            `json:"image"`
	ArchiveSHA256  string            `json:"archive_sha256"`
	ArchiveBytes   int64             `json:"archive_bytes"`
	ManifestDigest string            `json:"manifest_digest"`
	ConfigDigest   string            `json:"config_digest"`
	Layers         []ImageLayerProof `json:"layers"`
}

type ImageLayerProof struct {
	Digest    string `json:"digest"`
	DiffID    string `json:"diff_id"`
	BlobBytes int64  `json:"blob_bytes"`
	TarBytes  int64  `json:"tar_bytes"`
	FileBytes int64  `json:"file_bytes"`
	Entries   int64  `json:"entries"`
}

type imageDescriptor struct {
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Size        int64             `json:"size"`
	Annotations map[string]string `json:"annotations"`
	Platform    *struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
	} `json:"platform"`
}

// Reject ambiguous JSON before standard struct decoding (which accepts case
// aliases and duplicates). OCI config contains extensible nested dictionaries.
func imageJSON(raw []byte, out any) error {
	if len(raw) == 0 || len(raw) > 1<<20 || !utf8.Valid(raw) {
		return ErrImageArchive
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 32 {
			return ErrImageArchive
		}
		t, err := d.Token()
		if err != nil {
			return ErrImageArchive
		}
		switch t {
		case json.Delim('{'):
			seen := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				s, ok := k.(string)
				if e != nil || !ok || seen[strings.ToLower(s)] {
					return ErrImageArchive
				}
				seen[strings.ToLower(s)] = true
				if walk(depth+1) != nil {
					return ErrImageArchive
				}
			}
			if t, e := d.Token(); e != nil || t != json.Delim('}') {
				return ErrImageArchive
			}
		case json.Delim('['):
			for d.More() {
				if walk(depth+1) != nil {
					return ErrImageArchive
				}
			}
			if t, e := d.Token(); e != nil || t != json.Delim(']') {
				return ErrImageArchive
			}
		}
		return nil
	}
	if walk(0) != nil || d.Decode(new(any)) != io.EOF || json.Unmarshal(raw, out) != nil {
		return ErrImageArchive
	}
	return nil
}

func imageFields(raw []byte, required, optional []string) error {
	var fields map[string]json.RawMessage
	if imageJSON(raw, &fields) != nil || fields == nil {
		return ErrImageArchive
	}
	for _, k := range required {
		if len(fields[k]) == 0 || bytes.Equal(fields[k], []byte("null")) {
			return ErrImageArchive
		}
	}
	for k := range fields {
		if !slices.Contains(required, k) && !slices.Contains(optional, k) {
			return ErrImageArchive
		}
	}
	return nil
}

type imageReadCounter struct {
	r     io.Reader
	n     int64
	ctx   context.Context
	limit int64
}

func (r *imageReadCounter) Read(p []byte) (int, error) {
	if r.ctx.Err() != nil {
		return 0, r.ctx.Err()
	}
	if r.n > r.limit {
		return 0, ErrImageArchive
	}
	if int64(len(p)) > r.limit-r.n+1 {
		p = p[:r.limit-r.n+1]
	}
	n, e := r.r.Read(p)
	r.n += int64(n)
	if r.n > r.limit {
		return n, ErrImageArchive
	}
	return n, e
}

type imageBlob struct{ offset, size int64 }

// InspectImageArchive scans an OCI tar without extraction or execution. The
// caller supplies a privately held file and independently expected source/role.
// Only single Linux/AMD64 images and gzip/uncompressed regular OCI layers qualify.
func InspectImageArchive(ctx context.Context, source io.ReaderAt, size int64, role, sourceSHA string) (ImageArchiveProof, error) {
	fail := func() (ImageArchiveProof, error) { return ImageArchiveProof{}, ErrImageArchive }
	if ctx.Err() != nil || source == nil || size < 1 || size > MaxImageArchiveBytes || !slices.Contains(imageRoles, role) || !shaPattern.MatchString(sourceSHA) || sourceSHA == strings.Repeat("0", 40) {
		return fail()
	}
	h := sha256.New()
	counter := &imageReadCounter{r: io.TeeReader(io.NewSectionReader(source, 0, size), h), ctx: ctx, limit: size}
	tr := tar.NewReader(counter)
	blobs := map[string]imageBlob{}
	seen := map[string]bool{}
	for count := 0; ; count++ {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil || count > 260 {
			return fail()
		}
		name := strings.TrimSuffix(header.Name, "/")
		if seen[name] || header.Size < 0 || header.Size > size || len(header.PAXRecords) != 0 {
			return fail()
		}
		seen[name] = true
		if header.Typeflag == tar.TypeDir {
			if name != "blobs" && name != "blobs/sha256" {
				return fail()
			}
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return fail()
		}
		if name != "oci-layout" && name != "index.json" && !(strings.HasPrefix(name, "blobs/sha256/") && digestPattern.MatchString(strings.TrimPrefix(name, "blobs/sha256/"))) {
			return fail()
		}
		blobs[name] = imageBlob{counter.n, header.Size}
		if _, err := io.Copy(io.Discard, tr); err != nil {
			return fail()
		}
	}
	// tar.Reader stops at its end markers. Retain and authenticate all padding;
	// reject hidden second archives or nonzero suffixes.
	padding := make([]byte, 32<<10)
	for {
		n, e := counter.Read(padding)
		for _, b := range padding[:n] {
			if b != 0 {
				return fail()
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return fail()
		}
	}
	if counter.n != size {
		return fail()
	}
	read := func(name string) ([]byte, error) {
		b, ok := blobs[name]
		if !ok || b.size > 1<<20 {
			return nil, ErrImageArchive
		}
		return io.ReadAll(&imageReadCounter{r: io.NewSectionReader(source, b.offset, b.size), ctx: ctx, limit: b.size})
	}
	layout, err := read("oci-layout")
	var layoutValue map[string]string
	if err != nil || imageJSON(layout, &layoutValue) != nil || len(layoutValue) != 1 || layoutValue["imageLayoutVersion"] != "1.0.0" {
		return fail()
	}
	indexRaw, err := read("index.json")
	if err != nil || imageFields(indexRaw, []string{"schemaVersion", "manifests"}, []string{"mediaType", "annotations"}) != nil {
		return fail()
	}
	var index struct {
		SchemaVersion int               `json:"schemaVersion"`
		MediaType     string            `json:"mediaType"`
		Manifests     []json.RawMessage `json:"manifests"`
	}
	if imageJSON(indexRaw, &index) != nil || index.SchemaVersion != 2 || len(index.Manifests) != 1 || index.MediaType != "" && index.MediaType != "application/vnd.oci.image.index.v1+json" {
		return fail()
	}
	visited := map[string]bool{"oci-layout": true, "index.json": true}
	descriptor := func(raw json.RawMessage, media string) (imageDescriptor, error) {
		var d imageDescriptor
		if imageFields(raw, []string{"mediaType", "digest", "size"}, []string{"annotations", "platform"}) != nil || imageJSON(raw, &d) != nil || d.MediaType != media || !strings.HasPrefix(d.Digest, "sha256:") || !digestPattern.MatchString(strings.TrimPrefix(d.Digest, "sha256:")) || d.Size < 1 {
			return d, ErrImageArchive
		}
		if d.Platform != nil && (d.Platform.OS != "linux" || d.Platform.Architecture != "amd64") {
			return d, ErrImageArchive
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		if platform, exists := fields["platform"]; exists && imageFields(platform, []string{"os", "architecture"}, nil) != nil {
			return d, ErrImageArchive
		}
		name := "blobs/sha256/" + strings.TrimPrefix(d.Digest, "sha256:")
		b, ok := blobs[name]
		if !ok || b.size != d.Size {
			return d, ErrImageArchive
		}
		if !visited[name] {
			hash := sha256.New()
			if _, err := io.Copy(hash, &imageReadCounter{r: io.NewSectionReader(source, b.offset, b.size), ctx: ctx, limit: b.size}); err != nil || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != d.Digest {
				return d, ErrImageArchive
			}
			visited[name] = true
		}
		return d, nil
	}
	manifestDesc, err := descriptor(index.Manifests[0], ociManifestType)
	if err != nil {
		return fail()
	}
	image := ImageReference(role, sourceSHA)
	if manifestDesc.Annotations["org.opencontainers.image.ref.name"] != image || manifestDesc.Annotations["io.containerd.image.name"] != "" && manifestDesc.Annotations["io.containerd.image.name"] != image {
		return fail()
	}
	manifestRaw, err := read("blobs/sha256/" + strings.TrimPrefix(manifestDesc.Digest, "sha256:"))
	if err != nil || imageFields(manifestRaw, []string{"schemaVersion", "mediaType", "config", "layers"}, []string{"annotations"}) != nil {
		return fail()
	}
	var manifest struct {
		SchemaVersion int               `json:"schemaVersion"`
		MediaType     string            `json:"mediaType"`
		Config        json.RawMessage   `json:"config"`
		Layers        []json.RawMessage `json:"layers"`
	}
	if imageJSON(manifestRaw, &manifest) != nil || manifest.SchemaVersion != 2 || manifest.MediaType != ociManifestType || len(manifest.Layers) < 1 || len(manifest.Layers) > 128 {
		return fail()
	}
	configDesc, err := descriptor(manifest.Config, ociConfigType)
	if err != nil {
		return fail()
	}
	configRaw, err := read("blobs/sha256/" + strings.TrimPrefix(configDesc.Digest, "sha256:"))
	var configFields map[string]json.RawMessage
	if imageJSON(configRaw, &configFields) != nil || configFields["architecture"] == nil || configFields["os"] == nil || configFields["config"] == nil || configFields["rootfs"] == nil || imageFields(configFields["rootfs"], []string{"type", "diff_ids"}, nil) != nil {
		return fail()
	}
	var runtimeFields map[string]json.RawMessage
	if imageJSON(configFields["config"], &runtimeFields) != nil || runtimeFields["Labels"] == nil {
		return fail()
	}
	var config struct {
		Architecture string `json:"architecture"`
		OS           string `json:"os"`
		Config       struct {
			Labels map[string]string `json:"Labels"`
		} `json:"config"`
		RootFS struct {
			Type    string   `json:"type"`
			DiffIDs []string `json:"diff_ids"`
		} `json:"rootfs"`
	}
	if err != nil || imageJSON(configRaw, &config) != nil || config.Architecture != "amd64" || config.OS != "linux" || config.RootFS.Type != "layers" || len(config.RootFS.DiffIDs) != len(manifest.Layers) || config.Config.Labels["org.opencontainers.image.revision"] != sourceSHA || config.Config.Labels["io.borealis.service"] != role {
		return fail()
	}
	proof := ImageArchiveProof{Role: role, Image: image, ArchiveSHA256: hex.EncodeToString(h.Sum(nil)), ArchiveBytes: size, ManifestDigest: manifestDesc.Digest, ConfigDigest: configDesc.Digest, Layers: []ImageLayerProof{}}
	var expanded, entries int64
	for i, raw := range manifest.Layers {
		var peek imageDescriptor
		if imageJSON(raw, &peek) != nil || peek.MediaType != ociLayerType && peek.MediaType != ociLayerType+"+gzip" {
			return fail()
		}
		d, err := descriptor(raw, peek.MediaType)
		if err != nil {
			return fail()
		}
		b := blobs["blobs/sha256/"+strings.TrimPrefix(d.Digest, "sha256:")]
		layer, err := inspectImageLayer(ctx, io.NewSectionReader(source, b.offset, b.size), d, config.RootFS.DiffIDs[i])
		if err != nil {
			return fail()
		}
		expanded += layer.TarBytes
		entries += layer.Entries
		if expanded > MaxImageExpandedBytes || entries > MaxImageEntries {
			return fail()
		}
		proof.Layers = append(proof.Layers, layer)
	}
	if len(visited) != len(blobs) || ctx.Err() != nil {
		return fail()
	}
	return proof, nil
}

func inspectImageLayer(ctx context.Context, source io.Reader, d imageDescriptor, diffID string) (ImageLayerProof, error) {
	fail := func() (ImageLayerProof, error) { return ImageLayerProof{}, ErrImageArchive }
	input := bufio.NewReader(source)
	var unpacked io.Reader = input
	if d.MediaType == ociLayerType+"+gzip" {
		g, err := gzip.NewReader(input)
		if err != nil {
			return fail()
		}
		defer g.Close()
		g.Multistream(false)
		unpacked = g
	}
	h := sha256.New()
	counter := &imageReadCounter{r: io.TeeReader(unpacked, h), ctx: ctx, limit: MaxImageExpandedBytes}
	tr := tar.NewReader(counter)
	result := ImageLayerProof{Digest: d.Digest, DiffID: diffID, BlobBytes: d.Size}
	seen := map[string]bool{}
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fail()
		}
		name := strings.TrimSuffix(header.Name, "/")
		if name == "." && header.Typeflag == tar.TypeDir {
			continue
		}
		if name == "" || len(name) > 4096 || strings.HasPrefix(name, "/") || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") || seen[name] || header.Size < 0 || header.Size > MaxImageExpandedBytes {
			return fail()
		}
		seen[name] = true
		for key := range header.PAXRecords {
			if strings.Contains(strings.ToLower(key), "sparse") {
				return fail()
			}
		}
		switch header.Typeflag {
		case tar.TypeReg, tar.TypeRegA:
			result.FileBytes += header.Size
		case tar.TypeDir, tar.TypeSymlink, tar.TypeLink:
			if header.Size != 0 {
				return fail()
			}
		default:
			return fail()
		}
		// Links stay inside the container archive; never follow or extract them.
		result.Entries++
		if result.Entries > MaxImageEntries || result.FileBytes > MaxImageExpandedBytes {
			return fail()
		}
		if _, err := io.Copy(io.Discard, tr); err != nil {
			return fail()
		}
	}
	padding := make([]byte, 32<<10)
	for {
		n, e := counter.Read(padding)
		for _, b := range padding[:n] {
			if b != 0 {
				return fail()
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return fail()
		}
	}
	if _, err := input.Peek(1); err != io.EOF || "sha256:"+hex.EncodeToString(h.Sum(nil)) != diffID {
		return fail()
	}
	result.TarBytes = counter.n
	return result, nil
}
