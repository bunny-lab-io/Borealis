package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"context"
)

// InstallPrejoinIdentity installs shared identity after verified source staging
// and before host preparation. It requires absent target Node UID and fresh
// fixed identity files, never a fabricated UID or a joined-recovery request.
// Source provenance, clean-host ownership, stopped legacy processes and fixed
// mount/authority evidence remain mandatory caller checks. Both journals retain
// their original lifecycle binding; success leaves the startup marker in place.
// No production bootstrap-session action invokes this library yet.
func (j *MutationJournal) InstallPrejoinIdentity(ctx context.Context, install engineidentity.Installation, material *engineidentity.Material) (string, error) {
	return j.installIdentity(ctx, install, material, true)
}

// ReconcilePrejoinIdentity records only an uncertain committed identity effect
// after proving the prior executor stopped, originally absent file inventory,
// exact inner journal, files and retained marker. It never repeats installation.
func (j *MutationJournal) ReconcilePrejoinIdentity(ctx context.Context, install engineidentity.Installation, material *engineidentity.Material) (string, error) {
	return j.reconcileIdentity(ctx, install, material, true)
}

// ReleasePrejoinIdentity needs a distinct executor, completed inherited host
// preparation and no uncertain ledger steps. Caller must additionally prove
// actual prepared configuration/CNPG/Aegis bindings and stopped consumers; ledger
// state alone is not live proof. It removes only the committed identity marker,
// never starts consumers, joins membership or rewrites the journal's empty UID.
func (j *MutationJournal) ReleasePrejoinIdentity(ctx context.Context, install engineidentity.Installation, material *engineidentity.Material) (string, error) {
	return j.releaseIdentity(ctx, install, material, true)
}
