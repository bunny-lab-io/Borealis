package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"context"
	"io"
)

func (p *PreparationInputs) WritePrejoinK3sJoinPreparation(ctx context.Context, expected PreparationExpected, source engineidentity.Binding,
	identity *engineidentity.Material, artifacts *PreparationArtifacts, postgresReference string, join *K3sJoinMaterial, out io.Writer, check func(context.Context) error) error {
	if join.Verify(ctx, expected, source, check) != nil || out == nil {
		return ErrK3sJoinMaterial
	}
	if p.WritePrejoinArtifactPreparation(ctx, expected, source, identity, artifacts, postgresReference, out, check) != nil || join.WriteJoinMaterial(ctx, expected, source, out, check) != nil {
		return ErrK3sJoinMaterial
	}
	return imageBoundary(ctx, check)
}

func ConsumePrejoinK3sJoinPreparation(ctx context.Context, parent string, expected PreparationExpected, manager string, source engineidentity.Binding,
	postgresReference string, input io.Reader, check func(context.Context) error, consume func(context.Context, *PreparationInputs, *engineidentity.Material, *PreparationArtifacts, *K3sJoinMaterial) error) error {
	return consumePrejoinK3sJoinPreparation(ctx, parent, expected, manager, source, postgresReference, input, check, consume,
		func(ctx context.Context, input io.Reader) (*PreparationArtifacts, error) {
			return ReceivePreparationArtifacts(ctx, parent, expected.Source, expected.K3sVersion, postgresReference, input, check)
		})
}

// Native consumer cannot observe even authenticated source/identity/artifacts
// until the independently bound join frame and exact EOF both succeed. Every
// owned private buffer and incoming archive closes before this scope returns.
func consumePrejoinK3sJoinPreparation(ctx context.Context, parent string, expected PreparationExpected, manager string, source engineidentity.Binding,
	postgresReference string, input io.Reader, check func(context.Context) error, consume func(context.Context, *PreparationInputs, *engineidentity.Material, *PreparationArtifacts, *K3sJoinMaterial) error,
	receive func(context.Context, io.Reader) (*PreparationArtifacts, error)) error {
	if consume == nil || ValidateK3sJoinBinding(expected, source) != nil {
		return ErrK3sJoinMaterial
	}
	return consumePrejoinArtifactPreparationFrames(ctx, parent, expected, manager, source, postgresReference, input, check,
		func(ctx context.Context, inputs *PreparationInputs, identity *engineidentity.Material, artifacts *PreparationArtifacts) error {
			join, err := ReceiveK3sJoinMaterial(ctx, expected, source, input, check)
			if err != nil {
				return ErrK3sJoinMaterial
			}
			defer join.Close()
			var trailing [1]byte
			if n, err := input.Read(trailing[:]); n != 0 || err != io.EOF || join.Verify(ctx, expected, source, check) != nil {
				return ErrK3sJoinMaterial
			}
			if consume(ctx, inputs, identity, artifacts, join) != nil || join.Verify(ctx, expected, source, check) != nil {
				return ErrK3sJoinMaterial
			}
			return nil
		}, receive)
}
