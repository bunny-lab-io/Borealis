package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"io"
)

// WritePrejoinPreparation appends only the original source-bound shared keys
// after the complete authenticated source/configuration transfer. Transport
// still must prove actual source custody and native target state before bytes.
// Private envelope buffers are cleared; no identity data enters scratch/logs.
func (p *PreparationInputs) WritePrejoinPreparation(ctx context.Context, expected PreparationExpected, source engineidentity.Binding,
	material *engineidentity.Material, output io.Writer, check func(context.Context) error) error {
	if output == nil || validatePrejoinSource(expected.Target, source) != nil || imageBoundary(ctx, check) != nil {
		return ErrPreparationConfig
	}
	raw, err := material.Export(source)
	if err != nil {
		return ErrPreparationConfig
	}
	defer clear(raw)
	if len(raw) == 0 || len(raw) > engineidentity.MaxEnvelopeBytes || p.WritePreparation(ctx, expected, output, check) != nil || imageBoundary(ctx, check) != nil {
		return ErrPreparationConfig
	}
	var frame bytes.Buffer
	_ = binary.Write(&frame, binary.BigEndian, uint32(len(raw)))
	frame.Write(raw)
	digest := sha256.Sum256(raw)
	frame.Write(digest[:])
	defer clear(frame.Bytes())
	if _, err := io.Copy(output, &frame); err != nil || imageBoundary(ctx, check) != nil {
		return ErrPreparationConfig
	}
	return nil
}

// ConsumePrejoinPreparation validates source/helper/configuration, original
// identity binding, bounded envelope digest and exact EOF before any mutation.
// The synchronous consumer receives in-memory keys; scratch contains only the
// source bundle. A failed consumer leaves its own durable mutation fences intact.
func ConsumePrejoinPreparation(ctx context.Context, parent string, expected PreparationExpected, manager string, source engineidentity.Binding,
	input io.Reader, check func(context.Context) error, consume func(context.Context, *PreparationInputs, *engineidentity.Material) error) (result error) {
	if consume == nil {
		return ErrPreparationConfig
	}
	return consumePrejoinPreparationFrames(ctx, parent, expected, manager, source, input, check, func(ctx context.Context, inputs *PreparationInputs, material *engineidentity.Material) error {
		var trailing [1]byte
		if n, err := input.Read(trailing[:]); n != 0 || err != io.EOF || imageBoundary(ctx, check) != nil {
			return ErrPreparationConfig
		}
		return consume(ctx, inputs, material)
	})
}

// Private composition seam: caller must consume its fixed suffix and exact EOF
// before invoking any mutation. Ordinary pre-join input remains EOF-strict.
func consumePrejoinPreparationFrames(ctx context.Context, parent string, expected PreparationExpected, manager string, source engineidentity.Binding,
	input io.Reader, check func(context.Context) error, consume func(context.Context, *PreparationInputs, *engineidentity.Material) error) (result error) {
	if !digestPattern.MatchString(manager) || consume == nil || validatePrejoinSource(expected.Target, source) != nil {
		return ErrPreparationConfig
	}
	inputs, err := ReceivePreparation(ctx, parent, expected, input, check)
	if err != nil {
		return ErrPreparationConfig
	}
	defer func() {
		if inputs.Close() != nil {
			result = ErrPreparationConfig
		}
	}()
	_, actual, _, err := inputs.bundle.ManagerProof()
	if err != nil || actual != manager || imageBoundary(ctx, check) != nil {
		return ErrPreparationConfig
	}
	raw, err := readPreparationTransferField(contextReader{ctx, input}, engineidentity.MaxEnvelopeBytes)
	if err != nil {
		return ErrPreparationConfig
	}
	defer clear(raw)
	want := sha256.Sum256(raw)
	var trailer [sha256.Size]byte
	if _, err := io.ReadFull(contextReader{ctx, input}, trailer[:]); err != nil || !bytes.Equal(want[:], trailer[:]) {
		return ErrPreparationConfig
	}
	material, err := engineidentity.Import(raw, source)
	if err != nil {
		return ErrPreparationConfig
	}
	if consume(ctx, inputs, material) != nil || imageBoundary(ctx, check) != nil {
		return ErrPreparationConfig
	}
	return nil
}
