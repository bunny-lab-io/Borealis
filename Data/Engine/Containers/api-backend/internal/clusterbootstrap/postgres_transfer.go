package clusterbootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
)

const postgresTransferMagic = "BOREALIS-NODE-POSTGRES-IMAGE-1\n"
const maxPostgresTransferMetadata = 2048

type postgresTransferMetadata struct {
	Version       int    `json:"version"`
	Reference     string `json:"reference"`
	ArchiveSHA256 string `json:"archive_sha256"`
	ArchiveBytes  int64  `json:"archive_bytes"`
}

// WriteImage sends one inert archive with bounded metadata. Current source
// authority must independently select Reference on both sides of the transport.
func (v *PostgresImage) WriteImage(ctx context.Context, output io.Writer, check func(context.Context) error) error {
	if v == nil || v.set == nil || output == nil {
		return ErrImageArchive
	}
	v.set.mu.Lock()
	defer v.set.mu.Unlock()
	if v.set.root == nil || v.set.inventory == nil || imageBoundary(ctx, check) != nil {
		return ErrSessionAuthority
	}
	p := v.proof
	if !ValidPostgresImageReference(p.Reference) || !digestPattern.MatchString(p.ArchiveSHA256) || p.ArchiveBytes < 1 || p.ArchiveBytes > MaxImageArchiveBytes {
		return ErrImageArchive
	}
	raw, err := json.Marshal(postgresTransferMetadata{1, p.Reference, p.ArchiveSHA256, p.ArchiveBytes})
	if err != nil || len(raw) > maxPostgresTransferMetadata {
		return ErrImageArchive
	}
	var header bytes.Buffer
	header.WriteString(postgresTransferMagic)
	_ = binary.Write(&header, binary.BigEndian, uint32(len(raw)))
	header.Write(raw)
	if _, err := io.Copy(output, &header); err != nil {
		return ErrImageArchive
	}
	if err := v.set.writeArchive(ctx, p.Reference, output, check); err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	if _, err := io.Copy(output, bytes.NewReader(digest[:])); err != nil {
		return ErrImageArchive
	}
	return imageBoundary(ctx, check)
}

// ReceivePostgresImage authenticates selected manifest/platform/layers against
// independently observed source digest, not sender measurement claims. Scratch
// is fully reserved before archive IO. No registry fetch, import or execution.
func ReceivePostgresImage(ctx context.Context, parent, expectedReference string, input io.Reader, check func(context.Context) error) (_ *PostgresImage, result error) {
	if input == nil || !ValidPostgresImageReference(expectedReference) || imageBoundary(ctx, check) != nil {
		return nil, ErrImageArchive
	}
	reader := contextReader{ctx, input}
	prefix := make([]byte, len(postgresTransferMagic)+4)
	if _, err := io.ReadFull(reader, prefix); err != nil || string(prefix[:len(postgresTransferMagic)]) != postgresTransferMagic {
		return nil, ErrImageArchive
	}
	size := binary.BigEndian.Uint32(prefix[len(postgresTransferMagic):])
	if size == 0 || size > maxPostgresTransferMetadata {
		return nil, ErrImageArchive
	}
	raw := make([]byte, int(size))
	if _, err := io.ReadFull(reader, raw); err != nil {
		return nil, ErrImageArchive
	}
	var wire postgresTransferMetadata
	if imageFields(raw, []string{"version", "reference", "archive_sha256", "archive_bytes"}, nil) != nil || imageJSON(raw, &wire) != nil ||
		wire.Version != 1 || wire.Reference != expectedReference || !digestPattern.MatchString(wire.ArchiveSHA256) || wire.ArchiveBytes < 1 || wire.ArchiveBytes > MaxImageArchiveBytes || imageBoundary(ctx, check) != nil {
		return nil, ErrImageArchive
	}
	directory, err := os.MkdirTemp(parent, "borealis-node-postgresql-received-")
	if err != nil {
		return nil, ErrImageArchive
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		_ = os.RemoveAll(directory)
		return nil, ErrImageArchive
	}
	set := &ExternalImageSet{path: directory, root: root, names: map[string]string{expectedReference: postgresArchiveName}}
	defer func() {
		if result != nil {
			_ = set.Close()
		}
	}()
	files, err := reserveStagedImageArchives(ctx, root, map[string]int64{postgresArchiveName: wire.ArchiveBytes}, check, reserveImageFile)
	if err != nil {
		return nil, err
	}
	defer closeStagedImageArchives(files)
	f := files[postgresArchiveName]
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(reader, wire.ArchiveBytes))
	if err != nil || n != wire.ArchiveBytes || hex.EncodeToString(h.Sum(nil)) != wire.ArchiveSHA256 || imageBoundary(ctx, check) != nil {
		return nil, ErrImageArchive
	}
	proof, err := InspectPostgresImageArchive(ctx, f, n, expectedReference)
	if err != nil || proof.ArchiveSHA256 != wire.ArchiveSHA256 || f.Sync() != nil || !stagedImageFileMatches(root, postgresArchiveName, f, n) {
		return nil, ErrImageArchive
	}
	var trailer [sha256.Size]byte
	digest := sha256.Sum256(raw)
	if _, err := io.ReadFull(reader, trailer[:]); err != nil || trailer != digest || imageBoundary(ctx, check) != nil {
		return nil, ErrImageArchive
	}
	if err := f.Close(); err != nil {
		return nil, ErrImageArchive
	}
	delete(files, postgresArchiveName)
	if imageBoundary(ctx, check) != nil {
		return nil, ErrSessionAuthority
	}
	inventory := externalInventoryWire{Images: []ExternalImageProof{proof}}
	encoded, _ := json.Marshal(inventory)
	set.inventory = &ExternalImageInventory{wire: inventory, raw: encoded}
	return &PostgresImage{set: set, proof: proof}, nil
}
