package clusterbootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
)

func sourcePublicationAbsenceDigest(input string) string {
	sum := sha256.Sum256([]byte("fresh-source-publication-absence-v1\x00" + input))
	return hex.EncodeToString(sum[:])
}

// ReconcileSourceAbsent proves only that atomic source publication did not
// occur. It never removes private scratch or repairs a destination. Previous
// producers must be stopped; the existing journal permits a later write only
// under a strictly newer worker generation, retaining this absence in history.
func (p *PreparationInputs) ReconcileSourceAbsent(ctx context.Context, j *MutationJournal) (string, error) {
	if os.Geteuid() != 0 {
		return "", ErrMutationJournal
	}
	return p.reconcileSourceAbsent(ctx, j, "/opt")
}

func (p *PreparationInputs) reconcileSourceAbsent(ctx context.Context, j *MutationJournal, parent string) (string, error) {
	if j == nil {
		return "", ErrMutationJournal
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	input, err := p.sourcePublicationInput(j)
	if err != nil {
		return "", err
	}
	if p.Verify(ctx, p.configuration.wire.Expected, j.authority) != nil {
		return "", ErrSessionAuthority
	}
	root, err := imageInstallRoot(parent)
	if err != nil {
		return "", ErrPreparationConfig
	}
	defer root.Close()
	identity, err := root.Stat(".")
	if err != nil {
		return "", ErrPreparationConfig
	}
	prove := func(ctx context.Context) (string, error) {
		for range 2 {
			if p.Verify(ctx, p.configuration.wire.Expected, j.authority) != nil || j.priorExecutorsQuiescent(ctx) != nil {
				return "", ErrSessionAuthority
			}
			current, err := imageInstallRoot(parent)
			if err != nil {
				return "", ErrPreparationConfig
			}
			info, statErr := current.Stat(".")
			_, absentErr := current.Lstat("Borealis")
			closeErr := current.Close()
			if statErr != nil || closeErr != nil || !os.SameFile(identity, info) || !errors.Is(absentErr, os.ErrNotExist) || j.priorExecutorsQuiescent(ctx) != nil || j.authority(ctx) != nil {
				return "", ErrMutationUnknown
			}
		}
		return sourcePublicationAbsenceDigest(input), nil
	}
	result, err := j.reconcileLocked(ctx, "stage_source", input, prove, "not_applied")
	if err != nil {
		return "", err
	}
	// No acknowledgement if authority or destination changed during persistence.
	if _, err := prove(ctx); err != nil {
		return "", ErrMutationUnknown
	}
	return result, nil
}
