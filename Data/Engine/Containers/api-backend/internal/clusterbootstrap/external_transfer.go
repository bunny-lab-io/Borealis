package clusterbootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"io"
)

const externalTransferMagic = "BOREALIS-NODE-EXTERNAL-IMAGES-1\n"

// WriteImages sends the complete reviewed static dependency set. The inventory
// fixes order/lengths; final digest authenticates framing, never import readiness.
func (s *ExternalImageSet) WriteImages(ctx context.Context, output io.Writer, check func(context.Context) error) error {
	if s == nil || output == nil {
		return ErrImageArchive
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root == nil || s.inventory == nil || imageBoundary(ctx, check) != nil {
		return ErrSessionAuthority
	}
	// Dynamic PostgreSQL uses a separate source-bound single-image contract.
	if len(s.inventory.wire.Images) != len(ExternalImagePins()) {
		return ErrImageArchive
	}
	raw, err := s.inventory.Export()
	if err != nil || len(raw) == 0 || len(raw) > MaxImageInventoryBytes {
		return ErrImageArchive
	}
	var header bytes.Buffer
	header.WriteString(externalTransferMagic)
	_ = binary.Write(&header, binary.BigEndian, uint32(len(raw)))
	header.Write(raw)
	if _, err := io.Copy(output, &header); err != nil {
		return ErrImageArchive
	}
	for _, proof := range s.inventory.Images() {
		if err := s.writeArchive(ctx, proof.Reference, output, check); err != nil {
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

// ReceiveExternalImages uses receiver-compiled pins and independent release
// expectation. Every archive is reserved, hashed and structurally remeasured;
// unknown/partial sets never escape. Exactly one frame is consumed.
func ReceiveExternalImages(ctx context.Context, parent string, expected Expected, input io.Reader, check func(context.Context) error) (*ExternalImageSet, error) {
	return receiveExternalImages(ctx, parent, expected, input, check, ExternalImagePins())
}

func receiveExternalImages(ctx context.Context, parent string, expected Expected, input io.Reader,
	check func(context.Context) error, pins []ExternalImagePin) (*ExternalImageSet, error) {
	if input == nil || expected.Validate() != nil || imageBoundary(ctx, check) != nil {
		return nil, ErrImageArchive
	}
	reader := contextReader{ctx, input}
	prefix := make([]byte, len(externalTransferMagic)+4)
	if _, err := io.ReadFull(reader, prefix); err != nil || string(prefix[:len(externalTransferMagic)]) != externalTransferMagic {
		return nil, ErrImageArchive
	}
	size := binary.BigEndian.Uint32(prefix[len(externalTransferMagic):])
	if size == 0 || size > MaxImageInventoryBytes {
		return nil, ErrImageArchive
	}
	raw := make([]byte, int(size))
	if _, err := io.ReadFull(reader, raw); err != nil {
		return nil, ErrImageArchive
	}
	inventory, err := parseExternalImageInventory(raw, expected, pins)
	if err != nil {
		return nil, err
	}
	inspect := func(ctx context.Context, input io.ReaderAt, size int64, reference string) (ExternalImageProof, error) {
		for _, pin := range pins {
			if pin.Reference == reference {
				return inspectExternalImageArchive(ctx, input, size, pin)
			}
		}
		return ExternalImageProof{}, ErrImageArchive
	}
	set, err := stageExternalImages(ctx, parent, inventory, func(_ context.Context, proof ExternalImageProof) (io.ReadCloser, error) {
		return io.NopCloser(io.LimitReader(reader, proof.ArchiveBytes)), nil
	}, check, inspect)
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
