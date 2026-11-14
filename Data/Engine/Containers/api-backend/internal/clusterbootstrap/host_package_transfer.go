package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"io"
)

// Only these existing journal actions can install packages. Proof-only host
// reconciliation remains a separate input contract and never receives a writer.
func HostPackagePreparationAction(action string) bool {
	return action == "prepare_prejoin_host" || action == "resume_fresh_prejoin_host" || action == "resume_repository_prejoin_host"
}

// BindHostPackages transfers ownership only on success. It connects the existing
// source/configuration owner to release-bound inert bytes, without granting any
// readiness, target compatibility or mutation authority.
func (p *PreparationInputs) BindHostPackages(ctx context.Context, expected PreparationExpected, packages *HostPackageSet, check func(context.Context) error) error {
	if packages == nil || p.Verify(ctx, expected, check) != nil || p.hostPackages != nil {
		return ErrPreparationConfig
	}
	packages.mu.Lock()
	defer packages.mu.Unlock()
	if packages.failed || packages.root == nil || packages.file == nil || packages.inventory == nil ||
		packages.inventory.wire.Repository != expected.Source.Repository || packages.inventory.wire.Release != expected.Source.Release ||
		packages.inventory.wire.SourceSHA != expected.Source.SourceSHA || imageBoundary(ctx, check) != nil {
		return ErrPreparationConfig
	}
	p.hostPackages = packages
	return nil
}

// Consumer stays inside the existing input lifetime. A caller cannot substitute
// a second package set or retain mutation authority after its callback returns.
func (p *PreparationInputs) WithHostPackages(ctx context.Context, expected PreparationExpected, check func(context.Context) error,
	consume func(context.Context, *HostPackageSet) error) error {
	if consume == nil || p.Verify(ctx, expected, check) != nil || p.hostPackages == nil ||
		consume(ctx, p.hostPackages) != nil || imageBoundary(ctx, check) != nil {
		return ErrPreparationConfig
	}
	return nil
}

func (s *HostPackageSet) writePackages(ctx context.Context, output io.Writer, check func(context.Context) error) error {
	if s == nil || output == nil {
		return ErrImageArchive
	}
	s.mu.Lock()
	var raw []byte
	var err error
	if s.failed || s.inventory == nil {
		err = ErrImageArchive
	} else {
		raw, err = s.inventory.Export()
	}
	s.mu.Unlock()
	if err != nil || len(raw) == 0 || len(raw) > MaxImageInventoryBytes || imageBoundary(ctx, check) != nil {
		return ErrImageArchive
	}
	var header bytes.Buffer
	_ = binary.Write(&header, binary.BigEndian, uint32(len(raw)))
	header.Write(raw)
	if _, err := io.Copy(output, &header); err != nil || s.WriteArchive(ctx, output, check) != nil {
		return ErrImageArchive
	}
	digest := sha256.Sum256(raw)
	if _, err := io.Copy(output, bytes.NewReader(digest[:])); err != nil {
		return ErrImageArchive
	}
	return imageBoundary(ctx, check)
}

func receiveHostPackages(ctx context.Context, parent string, expected Expected, input io.Reader, check func(context.Context) error,
	parse func([]byte, Expected) (*HostPackageInventory, error)) (*HostPackageSet, error) {
	if input == nil || parse == nil || imageBoundary(ctx, check) != nil {
		return nil, ErrImageArchive
	}
	raw, err := readPreparationTransferField(contextReader{ctx, input}, MaxImageInventoryBytes)
	if err != nil {
		return nil, ErrImageArchive
	}
	inventory, err := parse(raw, expected)
	if err != nil || inventory == nil {
		return nil, ErrImageArchive
	}
	set, err := StageHostPackages(ctx, parent, expected, inventory, func(context.Context) (io.ReadCloser, error) {
		// Shared session owns the reader. This field is bounded; closing it must
		// not close the channel before its trailer and exact EOF are checked.
		return io.NopCloser(io.LimitReader(input, inventory.Archive().Size)), nil
	}, check)
	if err != nil {
		return nil, err
	}
	var trailer [sha256.Size]byte
	if _, err := io.ReadFull(contextReader{ctx, input}, trailer[:]); err != nil || trailer != sha256.Sum256(raw) || imageBoundary(ctx, check) != nil {
		_ = set.Close()
		return nil, ErrImageArchive
	}
	return set, nil
}

// Reuse the source/configuration/identity prefix and supervised bulk session.
// Host packages form one fixed suffix; no new journal stage or receipt exists.
func (p *PreparationInputs) WritePrejoinHostPreparation(ctx context.Context, expected PreparationExpected, source engineidentity.Binding,
	material *engineidentity.Material, output io.Writer, check func(context.Context) error) error {
	return p.WithHostPackages(ctx, expected, check, func(ctx context.Context, packages *HostPackageSet) error {
		if p.WritePrejoinPreparation(ctx, expected, source, material, output, check) != nil {
			return ErrPreparationConfig
		}
		return packages.writePackages(ctx, output, check)
	})
}

func ConsumePrejoinHostPreparation(ctx context.Context, parent string, expected PreparationExpected, manager string, source engineidentity.Binding,
	input io.Reader, check func(context.Context) error, consume func(context.Context, *PreparationInputs, *engineidentity.Material) error) error {
	return consumePrejoinHostPreparation(ctx, parent, expected, manager, source, input, check, consume, ParseHostPackageInventory)
}

func consumePrejoinHostPreparation(ctx context.Context, parent string, expected PreparationExpected, manager string, source engineidentity.Binding,
	input io.Reader, check func(context.Context) error, consume func(context.Context, *PreparationInputs, *engineidentity.Material) error,
	parse func([]byte, Expected) (*HostPackageInventory, error)) error {
	if consume == nil {
		return ErrPreparationConfig
	}
	return consumePrejoinPreparationFrames(ctx, parent, expected, manager, source, input, check,
		func(ctx context.Context, inputs *PreparationInputs, material *engineidentity.Material) error {
			packages, err := receiveHostPackages(ctx, parent, expected.Source, input, check, parse)
			if err != nil {
				return err
			}
			if err = inputs.BindHostPackages(ctx, expected, packages, check); err != nil {
				_ = packages.Close()
				return err
			}
			var trailing [1]byte
			if n, err := input.Read(trailing[:]); n != 0 || err != io.EOF || imageBoundary(ctx, check) != nil {
				return ErrPreparationConfig
			}
			return consume(ctx, inputs, material)
		})
}
