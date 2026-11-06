package clusterbootstrap

import (
	"context"
	"errors"
	"sync"
)

// PreparationArtifacts owns all immutable runtime inputs, but grants no host
// mutation or readiness. Complete-cohort source/capacity/lease checks remain the
// caller's responsibility. Partial cohorts or missing dependency sets reject.
type PreparationArtifacts struct {
	mu                            sync.Mutex
	expected                      Expected
	k3sVersion, postgresReference string
	application                   *ImageSet
	k3s                           *K3sSet
	external                      *ExternalImageSet
	postgres                      *PostgresImage
}

func (*PreparationArtifacts) String() string               { return "complete preparation artifacts [private]" }
func (*PreparationArtifacts) GoString() string             { return "complete preparation artifacts [private]" }
func (*PreparationArtifacts) MarshalJSON() ([]byte, error) { return nil, ErrImageArchive }

// Ownership transfers only on success. A failed constructor leaves all cleanup
// with the caller. Bindings are checked under the component locks; complete byte
// authentication is repeated by each guarded writer before/during transport.
func NewPreparationArtifacts(ctx context.Context, expected Expected, k3sVersion, postgresReference string,
	application *ImageSet, k3s *K3sSet, external *ExternalImageSet, postgres *PostgresImage,
	check func(context.Context) error) (*PreparationArtifacts, error) {
	a := &PreparationArtifacts{expected: expected, k3sVersion: k3sVersion, postgresReference: postgresReference,
		application: application, k3s: k3s, external: external, postgres: postgres}
	if err := a.verifyBindings(ctx, check); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *PreparationArtifacts) verifyBindings(ctx context.Context, check func(context.Context) error) error {
	if a == nil || a.expected.Validate() != nil || a.k3sVersion != K3sPins().Version || !ValidPostgresImageReference(a.postgresReference) ||
		a.application == nil || a.k3s == nil || a.external == nil || a.postgres == nil || a.postgres.set == nil || a.external == a.postgres.set || imageBoundary(ctx, check) != nil {
		return ErrImageArchive
	}
	a.application.mu.Lock()
	defer a.application.mu.Unlock()
	a.k3s.mu.Lock()
	defer a.k3s.mu.Unlock()
	a.external.mu.Lock()
	defer a.external.mu.Unlock()
	a.postgres.set.mu.Lock()
	defer a.postgres.set.mu.Unlock()
	if a.application.root == nil || a.application.inventory == nil || a.k3s.root == nil || a.k3s.inventory == nil ||
		a.external.root == nil || a.external.inventory == nil || a.postgres.set.root == nil || a.postgres.set.inventory == nil {
		return ErrImageArchive
	}
	app, k3s, external := a.application.inventory, a.k3s.inventory, a.external.inventory
	for _, identity := range [][3]string{{app.wire.Repository, app.wire.Release, app.wire.SourceSHA}, {k3s.wire.Repository, k3s.wire.Release, k3s.wire.SourceSHA}, {external.wire.Repository, external.wire.Release, external.wire.SourceSHA}} {
		if identity != [3]string{a.expected.Repository, a.expected.Release, a.expected.SourceSHA} {
			return ErrImageArchive
		}
	}
	if len(app.raw) == 0 || len(k3s.raw) == 0 || len(external.raw) == 0 || len(app.wire.Images) != len(ImageRoles()) ||
		len(external.wire.Images) != len(ExternalImagePins()) || k3s.wire.K3sVersion != a.k3sVersion ||
		a.postgres.proof.Reference != a.postgresReference || len(a.postgres.set.inventory.wire.Images) != 1 ||
		a.postgres.set.inventory.MatchesArchive(a.postgres.proof) != nil {
		return ErrImageArchive
	}
	return imageBoundary(ctx, check)
}

func (a *PreparationArtifacts) Close() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var errs []error
	if a.application != nil {
		errs = append(errs, a.application.Close())
	}
	if a.k3s != nil {
		errs = append(errs, a.k3s.Close())
	}
	if a.external != nil {
		errs = append(errs, a.external.Close())
	}
	if a.postgres != nil {
		errs = append(errs, a.postgres.Close())
	}
	a.application, a.k3s, a.external, a.postgres = nil, nil, nil, nil
	return errors.Join(errs...)
}
