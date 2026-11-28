package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"context"
	"io"
)

// WritePrejoinArtifactPreparation sends one fixed source/configuration/identity
// prefix and the complete dependency suffix. Source-selected PostgreSQL identity
// is supplied independently of the archive stream. No mutation follows until the
// receiver authenticates every family and exact EOF under current authority.
func (p *PreparationInputs) WritePrejoinArtifactPreparation(ctx context.Context, expected PreparationExpected, source engineidentity.Binding,
	material *engineidentity.Material, artifacts *PreparationArtifacts, postgresReference string, out io.Writer, check func(context.Context) error) error {
	if artifacts == nil || out == nil {
		return ErrPreparationConfig
	}
	artifacts.mu.Lock()
	valid := artifacts.expected == expected.Source && artifacts.k3sVersion == expected.K3sVersion && artifacts.postgresReference == postgresReference && artifacts.verifyBindings(ctx, check) == nil
	artifacts.mu.Unlock()
	if !valid || p.WritePrejoinPreparation(ctx, expected, source, material, out, check) != nil || artifacts.WriteArtifacts(ctx, out, check) != nil {
		return ErrPreparationConfig
	}
	return imageBoundary(ctx, check)
}

// Install and join actions reference the existing journaled image publication.
func (p *PreparationInputs) WritePrejoinPublishedPreparation(ctx context.Context, expected PreparationExpected, source engineidentity.Binding,
	identity *engineidentity.Material, artifacts *PreparationArtifacts, postgresReference string, out io.Writer, check func(context.Context) error) error {
	if artifacts == nil || artifacts.expected != expected.Source || artifacts.k3sVersion != expected.K3sVersion || artifacts.postgresReference != postgresReference {
		return ErrPreparationConfig
	}
	if err := p.WritePrejoinPreparation(ctx, expected, source, identity, out, check); err != nil {
		return err
	}
	return artifacts.writePublishedInventories(ctx, out, check)
}

func ConsumePrejoinPublishedPreparation(ctx context.Context, parent string, expected PreparationExpected, manager string, source engineidentity.Binding,
	postgresReference string, input io.Reader, check func(context.Context) error, consume func(context.Context, *PreparationInputs, *engineidentity.Material, *PreparationArtifacts) error) error {
	return consumePrejoinArtifactPreparation(ctx, parent, expected, manager, source, postgresReference, input, check, consume, func(ctx context.Context, in io.Reader) (*PreparationArtifacts, error) {
		return readPublishedInventories(ctx, expected.Source, expected.K3sVersion, postgresReference, in, check, K3sPins(), ExternalImagePins())
	})
}
func ConsumePrejoinArtifactPreparation(ctx context.Context, parent string, expected PreparationExpected, manager string, source engineidentity.Binding,
	postgresReference string, input io.Reader, check func(context.Context) error, consume func(context.Context, *PreparationInputs, *engineidentity.Material, *PreparationArtifacts) error) error {
	return consumePrejoinArtifactPreparation(ctx, parent, expected, manager, source, postgresReference, input, check, consume,
		func(ctx context.Context, input io.Reader) (*PreparationArtifacts, error) {
			return ReceivePreparationArtifacts(ctx, parent, expected.Source, expected.K3sVersion, postgresReference, input, check)
		})
}

func consumePrejoinArtifactPreparation(ctx context.Context, parent string, expected PreparationExpected, manager string, source engineidentity.Binding,
	postgresReference string, input io.Reader, check func(context.Context) error, consume func(context.Context, *PreparationInputs, *engineidentity.Material, *PreparationArtifacts) error,
	receive func(context.Context, io.Reader) (*PreparationArtifacts, error)) error {
	if consume == nil {
		return ErrPreparationConfig
	}
	return consumePrejoinArtifactPreparationFrames(ctx, parent, expected, manager, source, postgresReference, input, check,
		func(ctx context.Context, inputs *PreparationInputs, material *engineidentity.Material, artifacts *PreparationArtifacts) error {
			var trailing [1]byte
			if n, err := input.Read(trailing[:]); n != 0 || err != io.EOF || imageBoundary(ctx, check) != nil {
				return ErrPreparationConfig
			}
			return consume(ctx, inputs, material, artifacts)
		}, receive)
}

func consumePrejoinArtifactPreparationFrames(ctx context.Context, parent string, expected PreparationExpected, manager string, source engineidentity.Binding,
	postgresReference string, input io.Reader, check func(context.Context) error, consume func(context.Context, *PreparationInputs, *engineidentity.Material, *PreparationArtifacts) error,
	receive func(context.Context, io.Reader) (*PreparationArtifacts, error)) error {
	if consume == nil || receive == nil || !ValidPostgresImageReference(postgresReference) {
		return ErrPreparationConfig
	}
	return consumePrejoinPreparationFrames(ctx, parent, expected, manager, source, input, check,
		func(ctx context.Context, inputs *PreparationInputs, material *engineidentity.Material) (result error) {
			artifacts, err := receive(ctx, input)
			if err != nil {
				return ErrPreparationConfig
			}
			defer func() {
				if artifacts.Close() != nil {
					result = ErrPreparationConfig
				}
			}()
			if artifacts == nil || artifacts.expected != expected.Source || artifacts.k3sVersion != expected.K3sVersion || artifacts.postgresReference != postgresReference {
				return ErrPreparationConfig
			}
			if consume(ctx, inputs, material, artifacts) != nil || imageBoundary(ctx, check) != nil {
				return ErrPreparationConfig
			}
			return nil
		})
}
