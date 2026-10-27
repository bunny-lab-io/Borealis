package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
)

// Startup stays separate from import verification, identity activation and
// whole-cohort admission. Both exclusions and the identity marker remain held.
type PreparedK3sStartActions struct {
	Configured func(context.Context) error
	CheckFresh func(context.Context) error
	Start      func(context.Context, *os.File, *os.File) error
	Prove      func(context.Context) (K3sStartedObservation, error)
}

type K3sStartedObservation struct {
	Node       K3sJoinedNode
	Membership K3sMembership
}

type prejoinK3sStartPhase struct {
	material *K3sJoinMaterial
	actions  PreparedK3sStartActions
}

func (p *PreparationInputs) StartPreparedK3s(ctx context.Context, j *MutationJournal, install engineidentity.Installation, identity *engineidentity.Material,
	artifacts *PreparationArtifacts, postgresReference string, proveHost func(context.Context) error, material *K3sJoinMaterial, actions PreparedK3sStartActions) (string, error) {
	if os.Geteuid() != 0 || install.UID != PreparationRuntimeID || install.GID != PreparationRuntimeID {
		return "", ErrMutationJournal
	}
	return p.prejoinArtifactPhaseTransactionWithStart(ctx, j, install, identity, artifacts, postgresReference, proveHost, "/opt", "/", false, nil, nil, &prejoinK3sStartPhase{material, actions})
}

func (p *PreparationInputs) ReconcilePreparedK3sStart(ctx context.Context, j *MutationJournal, install engineidentity.Installation, identity *engineidentity.Material,
	artifacts *PreparationArtifacts, postgresReference string, proveHost func(context.Context) error, material *K3sJoinMaterial, actions PreparedK3sStartActions) (string, error) {
	if os.Geteuid() != 0 || install.UID != PreparationRuntimeID || install.GID != PreparationRuntimeID {
		return "", ErrMutationJournal
	}
	return p.prejoinArtifactPhaseTransactionWithStart(ctx, j, install, identity, artifacts, postgresReference, proveHost, "/opt", "/", true, nil, nil, &prejoinK3sStartPhase{material, actions})
}

// Receipt binds this target's actual Node/member identities. Other approved
// peers may subsequently join; their changing registration cannot rewrite this
// target's receipt. Full membership/import/health remain separate admission gates.
func (o K3sStartedObservation) result(expected PreparationExpected, input string) (string, error) {
	if expected.Validate() != nil || !digestPattern.MatchString(input) || !o.Node.Ready || !nonzeroPreparationUUID(o.Node.NodeUID) ||
		o.Node.Hostname != expected.Target.Hostname || o.Node.MachineID != expected.Target.MachineID || o.Node.BootID != expected.TargetBootID ||
		o.Node.Address != expected.Target.Address || o.Node.K3sVersion != expected.K3sVersion || expected.K3sVersion != K3sPins().Version ||
		!o.Membership.Voting || o.Membership.LocalID == 0 || len(o.Membership.Members) < 2 || len(o.Membership.Members) > 3 {
		return "", ErrMutationUnknown
	}
	// Revalidate typed observer output before hashing, including duplicate IDs,
	// foreign addresses and learner state. Native reader also validates exact
	// peer/client URLs from pinned /db/info before producing this projection.
	members := []map[string]any{}
	localName := ""
	for _, m := range o.Membership.Members {
		member := map[string]any{"ID": m.ID, "name": m.Name, "peerURLs": []string{"https://" + m.Address + ":2380"}, "isLearner": m.Learner}
		if m.Name != "" {
			member["clientURLs"] = []string{"https://" + m.Address + ":2379"}
		}
		if m.Address == expected.Target.Address {
			localName = m.Name
		}
		members = append(members, member)
	}
	raw, err := json.Marshal(map[string]any{"members": members})
	if err != nil {
		return "", ErrMutationUnknown
	}
	checked, err := ParseK3sMembership(raw, expected, localName)
	if err != nil || !checked.Voting || checked.LocalID != o.Membership.LocalID {
		return "", ErrMutationUnknown
	}
	raw, err = json.Marshal(struct {
		Domain, Input string
		Node          K3sJoinedNode
		MemberID      uint64
		MemberName    string
	}{"prepared-k3s-started-v1", input, o.Node, checked.LocalID, localName})
	if err != nil {
		return "", ErrMutationUnknown
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
