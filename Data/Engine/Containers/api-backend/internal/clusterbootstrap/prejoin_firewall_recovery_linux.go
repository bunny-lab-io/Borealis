package clusterbootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"

	"borealis/api-backend/internal/engineidentity"
)

func prejoinHostFirewallDigest(input string) string {
	sum := sha256.Sum256([]byte("prejoin-host-interrupted-firewall-v1\x00" + input))
	return hex.EncodeToString(sum[:])
}

// All non-firewall host effects must already be proven complete. The only
// allowed write completes the exact owned firewall script under both fences.
type PrejoinFirewallRecoveryActions struct {
	ProvePartial func(context.Context) error
	Complete     func(context.Context, *os.File, *os.File) error
	Prove        func(context.Context) error
}

func (p *PreparationInputs) CompletePrejoinFirewall(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, actions PrejoinFirewallRecoveryActions) (string, error) {
	if os.Geteuid() != 0 || install.UID != PreparationRuntimeID || install.GID != PreparationRuntimeID {
		return "", ErrMutationJournal
	}
	return p.completePrejoinFirewall(ctx, j, install, material, actions, "/opt")
}

func (p *PreparationInputs) completePrejoinFirewall(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, actions PrejoinFirewallRecoveryActions, parent string) (string, error) {
	if actions.ProvePartial == nil || actions.Complete == nil || actions.Prove == nil {
		return "", ErrMutationJournal
	}
	return p.prejoinHostTransaction(ctx, j, install, material, PrejoinHostActions{Prepare: actions.Complete, Prove: actions.Prove}, parent, false, actions.ProvePartial, true)
}

func (j *MutationJournal) continuePrejoinFirewallLocked(ctx context.Context, input string, partial func(context.Context) error, complete func(context.Context, *os.File) (string, error)) (string, error) {
	return j.continuePrejoinHostClassLocked(ctx, input, prejoinHostFirewallDigest(input), partial, complete)
}
