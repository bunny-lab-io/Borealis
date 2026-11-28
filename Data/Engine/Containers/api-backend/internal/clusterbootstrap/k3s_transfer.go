package clusterbootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"io"
)

const k3sTransferMagic = "BOREALIS-NODE-K3S-INPUTS-1\n"

// WriteInputs sends inventory, three fixed-order exact-length assets and the
// inventory digest. Paths and commands never arrive from the sender. Successful
// transfer grants no installation, consumer startup or membership authority.
func (s *K3sSet) WriteInputs(ctx context.Context, output io.Writer, check func(context.Context) error) error {
	if s == nil || output == nil {
		return ErrImageArchive
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root == nil || s.inventory == nil || imageBoundary(ctx, check) != nil {
		return ErrSessionAuthority
	}
	raw, err := s.inventory.Export()
	if err != nil || len(raw) == 0 || len(raw) > MaxK3sInventoryBytes {
		return ErrImageArchive
	}
	var header bytes.Buffer
	header.WriteString(k3sTransferMagic)
	_ = binary.Write(&header, binary.BigEndian, uint32(len(raw)))
	header.Write(raw)
	if _, err := io.Copy(output, &header); err != nil {
		return ErrImageArchive
	}
	for _, name := range []string{K3sBinaryName, K3sInstallerName, K3sArchiveName} {
		if err := s.writeAsset(ctx, name, output, check); err != nil {
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

// ReceiveK3sInputs rebinds inventory to independently selected release/version,
// reserves and verifies all assets, and consumes exactly one frame. Failed or
// interrupted transfer removes only its new scratch and yields no usable set.
func ReceiveK3sInputs(ctx context.Context, parent string, expected Expected, version string, input io.Reader, check func(context.Context) error) (*K3sSet, error) {
	return receiveK3sInputs(ctx, parent, expected, version, input, check, K3sPins())
}

func receiveK3sInputs(ctx context.Context, parent string, expected Expected, version string, input io.Reader,
	check func(context.Context) error, pins K3sInputPins) (*K3sSet, error) {
	if input == nil || expected.Validate() != nil || imageBoundary(ctx, check) != nil {
		return nil, ErrImageArchive
	}
	reader := contextReader{ctx, input}
	prefix := make([]byte, len(k3sTransferMagic)+4)
	if _, err := io.ReadFull(reader, prefix); err != nil || string(prefix[:len(k3sTransferMagic)]) != k3sTransferMagic {
		return nil, ErrImageArchive
	}
	size := binary.BigEndian.Uint32(prefix[len(k3sTransferMagic):])
	if size == 0 || size > MaxK3sInventoryBytes {
		return nil, ErrImageArchive
	}
	raw := make([]byte, int(size))
	if _, err := io.ReadFull(reader, raw); err != nil {
		return nil, ErrImageArchive
	}
	inventory, err := parseK3sInventory(raw, expected, version, pins)
	if err != nil {
		return nil, err
	}
	sizes := map[string]int64{K3sBinaryName: pins.Binary.Size, K3sInstallerName: pins.Installer.Size, K3sArchiveName: pins.Archive.Size}
	set, err := stageK3sInputs(ctx, parent, inventory, func(_ context.Context, name string) (io.ReadCloser, error) {
		return io.NopCloser(io.LimitReader(reader, sizes[name])), nil
	}, check, pins, reserveImageFile)
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
