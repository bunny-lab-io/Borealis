package clusterbootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
)

const imageTransferMagic = "BOREALIS-NODE-IMAGES-1\n"

// WriteImages sends inventory followed by its nine exact-length archives and an
// inventory-digest trailer. Transport must keep this stream private and cancel
// blocked IO with ctx. This is inert staging data, never a command or permission
// to import. The receiving side must independently bind the expected release.
func (s *ImageSet) WriteImages(ctx context.Context, output io.Writer, check func(context.Context) error) error {
	if s == nil || output == nil {
		return ErrImageArchive
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root == nil || s.inventory == nil || imageBoundary(ctx, check) != nil {
		return ErrSessionAuthority
	}
	raw, err := s.inventory.Export()
	if err != nil {
		return err
	}
	var header bytes.Buffer
	header.WriteString(imageTransferMagic)
	_ = binary.Write(&header, binary.BigEndian, uint32(len(raw)))
	header.Write(raw)
	if _, err := io.Copy(output, &header); err != nil {
		return ErrImageArchive
	}
	for _, proof := range s.inventory.Images() {
		if err := s.writeArchive(ctx, proof.Role, output, check); err != nil {
			return err
		}
	}
	if imageBoundary(ctx, check) != nil {
		return ErrSessionAuthority
	}
	digest := sha256.Sum256(raw)
	if _, err := io.Copy(output, bytes.NewReader(digest[:])); err != nil {
		return ErrImageArchive
	}
	return imageBoundary(ctx, check)
}

// ReceiveImages independently parses and measures the whole stream into new
// private scratch. It consumes exactly one framed set, leaving subsequent
// protocol bytes to its caller. No paths, shell commands or credentials arrive
// from the sender. An interrupted or changed stream yields no usable ImageSet.
func ReceiveImages(ctx context.Context, parent string, expected Expected, input io.Reader, check func(context.Context) error) (*ImageSet, error) {
	if input == nil || expected.Validate() != nil || imageBoundary(ctx, check) != nil {
		return nil, ErrImageArchive
	}
	reader := contextReader{ctx, input}
	prefix := make([]byte, len(imageTransferMagic)+4)
	if _, err := io.ReadFull(reader, prefix); err != nil || string(prefix[:len(imageTransferMagic)]) != imageTransferMagic {
		return nil, ErrImageArchive
	}
	size := binary.BigEndian.Uint32(prefix[len(imageTransferMagic):])
	if size == 0 || size > MaxImageInventoryBytes {
		return nil, ErrImageArchive
	}
	raw := make([]byte, int(size))
	if _, err := io.ReadFull(reader, raw); err != nil {
		return nil, ErrImageArchive
	}
	inventory, err := ParseImageInventory(raw, expected)
	if err != nil {
		return nil, err
	}
	set, err := StageImages(ctx, parent, inventory, func(ctx context.Context, proof ImageArchiveProof) (io.ReadCloser, error) {
		return io.NopCloser(io.LimitReader(reader, proof.ArchiveBytes)), nil
	}, check)
	if err != nil {
		return nil, err
	}
	var trailer [sha256.Size]byte
	digest := sha256.Sum256(raw)
	if _, err := io.ReadFull(reader, trailer[:]); err != nil || trailer != digest || imageBoundary(ctx, check) != nil {
		_ = set.Close()
		return nil, ErrImageArchive
	}
	return set, nil
}

// RuntimeManifest builds existing schema1 node-workload image references from
// verified immutable images. It does not write installed state. Content digests
// are explicit alongside the legacy hash field; the latter is a manifest hash,
// not Engine.sh's local-build cache key. A later local build will recompute its
// own input hash and cannot mistake this for a reusable Docker build.
func (s *ImageSet) RuntimeManifest(ctx context.Context, check func(context.Context) error) ([]byte, error) {
	if s == nil {
		return nil, ErrImageArchive
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root == nil || s.inventory == nil || imageBoundary(ctx, check) != nil {
		return nil, ErrSessionAuthority
	}
	return s.runtimeManifest()
}

func (s *ImageSet) runtimeManifest() ([]byte, error) {
	services := map[string]any{}
	for _, image := range s.inventory.Images() {
		repository, _, _ := strings.Cut(image.Image, ":")
		services[image.Role] = map[string]any{"image": repository + "@" + image.ManifestDigest, "archive_reference": image.Image, "hash": image.ManifestDigest[len("sha256:"):], "mode": "prod", "manifest_digest": image.ManifestDigest, "config_digest": image.ConfigDigest}
	}
	h := sha256.Sum256(s.inventory.raw)
	return json.Marshal(map[string]any{"schema_version": 1, "project": "borealis-engine", "mode": "prod", "source_sha": s.inventory.wire.SourceSHA,
		"release": s.inventory.wire.Release, "image_inventory_sha256": hex.EncodeToString(h[:]), "services": services})
}
