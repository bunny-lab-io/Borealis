package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"reflect"
)

const k3sJoinTransferMagic = "BOREALIS-NODE-K3S-JOIN-1\n"
const maxK3sJoinMetadata = 512

type k3sJoinTransferMetadata struct {
	Version       int    `json:"version"`
	BindingSHA256 string `json:"binding_sha256"`
	TokenBytes    int    `json:"token_bytes"`
	CABytes       int    `json:"ca_bytes"`
}

func k3sJoinTransferBinding(expected PreparationExpected, source engineidentity.Binding) (string, error) {
	if ValidateK3sJoinBinding(expected, source) != nil {
		return "", ErrK3sJoinMaterial
	}
	raw, err := json.Marshal(struct {
		Domain   string
		Expected PreparationExpected
		Source   engineidentity.Binding
	}{"prejoin-k3s-join-transfer-v1", expected, source})
	if err != nil {
		return "", ErrK3sJoinMaterial
	}
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:]), nil
}

// WriteJoinMaterial emits one bounded private frame inside authenticated SSH.
// It never chooses source, hostname, server endpoint or claim from wire input.
// No generic serializer/exporter can expose this object's private contents.
func (m *K3sJoinMaterial) WriteJoinMaterial(ctx context.Context, expected PreparationExpected, source engineidentity.Binding, out io.Writer, check func(context.Context) error) error {
	if m == nil || m.owned == nil || out == nil || imageBoundary(ctx, check) != nil {
		return ErrK3sJoinMaterial
	}
	d := m.owned
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || !reflect.DeepEqual(expected, d.expected) || source != d.source {
		return ErrK3sJoinMaterial
	}
	binding, err := k3sJoinTransferBinding(expected, source)
	if err != nil {
		return err
	}
	metadata, err := json.Marshal(k3sJoinTransferMetadata{1, binding, len(d.token), len(d.ca)})
	if err != nil || len(metadata) > maxK3sJoinMetadata {
		return ErrK3sJoinMaterial
	}
	var frame bytes.Buffer
	frame.Grow(len(k3sJoinTransferMagic) + 4 + len(metadata) + len(d.token) + len(d.ca) + sha256.Size)
	frame.WriteString(k3sJoinTransferMagic)
	_ = binary.Write(&frame, binary.BigEndian, uint32(len(metadata)))
	frame.Write(metadata)
	frame.Write(d.token)
	frame.Write(d.ca)
	digest := sha256.Sum256(frame.Bytes())
	frame.Write(digest[:])
	raw := frame.Bytes()
	defer clear(raw)
	for len(raw) > 0 {
		if imageBoundary(ctx, check) != nil {
			return ErrK3sJoinMaterial
		}
		n := min(len(raw), 4096)
		written, err := out.Write(raw[:n])
		if err != nil || written != n || imageBoundary(ctx, check) != nil {
			return ErrK3sJoinMaterial
		}
		raw = raw[n:]
	}
	return imageBoundary(ctx, check)
}

// ReceiveK3sJoinMaterial consumes exactly one frame and leaves the next frame
// untouched. Composition must require exact EOF before any native consumer.
// All claim metadata is checked before reading private payload; CA/token proof
// is independent of the sender's frame checksum and private-material digest.
func ReceiveK3sJoinMaterial(ctx context.Context, expected PreparationExpected, source engineidentity.Binding, input io.Reader, check func(context.Context) error) (*K3sJoinMaterial, error) {
	if input == nil || imageBoundary(ctx, check) != nil {
		return nil, ErrK3sJoinMaterial
	}
	binding, err := k3sJoinTransferBinding(expected, source)
	if err != nil {
		return nil, err
	}
	reader := contextReader{ctx, input}
	header := make([]byte, len(k3sJoinTransferMagic)+4)
	if _, err := io.ReadFull(reader, header); err != nil || string(header[:len(k3sJoinTransferMagic)]) != k3sJoinTransferMagic {
		return nil, ErrK3sJoinMaterial
	}
	size := binary.BigEndian.Uint32(header[len(k3sJoinTransferMagic):])
	if size == 0 || size > maxK3sJoinMetadata {
		return nil, ErrK3sJoinMaterial
	}
	raw := make([]byte, size)
	if _, err := io.ReadFull(reader, raw); err != nil {
		return nil, ErrK3sJoinMaterial
	}
	var wire k3sJoinTransferMetadata
	if imageFields(raw, []string{"version", "binding_sha256", "token_bytes", "ca_bytes"}, nil) != nil || imageJSON(raw, &wire) != nil || wire.Version != 1 || wire.BindingSHA256 != binding ||
		wire.TokenBytes < 1 || wire.TokenBytes > MaxK3sJoinTokenBytes || wire.CABytes < 1 || wire.CABytes > MaxK3sJoinCABytes || imageBoundary(ctx, check) != nil {
		return nil, ErrK3sJoinMaterial
	}
	payload := make([]byte, wire.TokenBytes+wire.CABytes)
	defer clear(payload)
	if _, err := io.ReadFull(reader, payload); err != nil || imageBoundary(ctx, check) != nil {
		return nil, ErrK3sJoinMaterial
	}
	h := sha256.New()
	h.Write(header)
	h.Write(raw)
	h.Write(payload)
	var trailer [sha256.Size]byte
	if _, err := io.ReadFull(reader, trailer[:]); err != nil || !bytes.Equal(trailer[:], h.Sum(nil)) || imageBoundary(ctx, check) != nil {
		return nil, ErrK3sJoinMaterial
	}
	material, err := NewK3sJoinMaterial(expected, source, payload[:wire.TokenBytes], payload[wire.TokenBytes:])
	if err != nil {
		return nil, ErrK3sJoinMaterial
	}
	if imageBoundary(ctx, check) != nil {
		material.Close()
		return nil, ErrK3sJoinMaterial
	}
	return material, nil
}
