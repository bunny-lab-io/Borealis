package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"math"
	"slices"
)

// This wire projection carries no lease or transferable qualification grant.
// The opaque receipt binds the complete Kubernetes observations held only by
// the controller; workers still independently check authority and reread.
type clusterSSHStorageSnapshot struct {
	Requirements clusterSSHStorageRequirements `json:"requirements"`
	Observation  string                        `json:"observation_sha256"`
}

func (r *kubernetesClusterStepRunner) readSSHStoragePreparationSnapshot(ctx context.Context, authority clusterSSHPreparationAuthorityRead) (clusterSSHPreparationSnapshot, error) {
	var value clusterSSHPreparationSnapshot
	err := r.withSSHSourceStorage(ctx, authority, func(ctx context.Context, storage clusterSSHStorageRequirements, checks clusterSSHPreparationChecks) error {
		// Bind source Jobs and private configuration to the storage scope's original
		// authority at each existing source boundary, without retaining a DB handle.
		boundAuthority := func(ctx context.Context) (clusterSSHPreparationAuthority, error) {
			if checks.Authority(ctx) != nil {
				return clusterSSHPreparationAuthority{}, clusterbootstrap.ErrPreparationConfig
			}
			current, err := authority(ctx)
			if err != nil || checks.Authority(ctx) != nil {
				return clusterSSHPreparationAuthority{}, clusterbootstrap.ErrPreparationConfig
			}
			return current, nil
		}
		var err error
		value, err = newClusterSSHPreparationSnapshotRead(boundAuthority, r.kube.getClusterSSHPreparationJSON, r.newSSHSourceNetworkRead(boundAuthority))(ctx)
		if err != nil || checks.Authority(ctx) != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		observation := storage.observation
		storage.observation = ""
		storage.Volumes = slices.Clone(storage.Volumes)
		value.Storage = clusterSSHStorageSnapshot{Requirements: storage, Observation: observation}
		return nil
	})
	if err != nil || ctx.Err() != nil {
		return clusterSSHPreparationSnapshot{}, clusterbootstrap.ErrPreparationConfig
	}
	return value, nil
}

// Validate only independently supplied source identity and bounded public
// fields. Actual Kubernetes binding remains controller-owned; a well-shaped
// digest alone cannot create provenance, prove free space or select a target.
func validClusterSSHStorageSnapshot(value clusterSSHStorageSnapshot, source clusterSSHSourceCohort) bool {
	r := value.Requirements
	validBytes := func(n uint64) bool { return n > 0 && n <= math.MaxInt64 }
	if !clusterSSHSourceObservationRE.MatchString(value.Observation) || r.observation != "" ||
		!validBytes(r.ArtifactReplicaBytes) || !validBytes(r.PostgresInstanceBytes) || len(source.Members) < 1 || len(source.Members) > 2 ||
		len(r.Volumes) < len(source.Members)+1 || len(r.Volumes) > 16 {
		return false
	}
	instances := int64(1)
	if len(source.Members) == 2 {
		instances = 3
	}
	if r.ConfiguredPostgresInstances != instances {
		return false
	}
	nodes := map[string]bool{}
	for _, member := range source.Members {
		if nodes[member.Name] || !clusterSSHStorageName(member.Name) {
			return false
		}
		nodes[member.Name] = true
	}
	seenPV, seenClaimUID, seenPVUID, seenVolumeUID := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	occupied := map[string]bool{}
	artifact, pg := 0, 0
	pgClass := ""
	previous := ""
	for _, v := range r.Volumes {
		if !clusterSSHStorageName(v.Claim) || v.Claim <= previous || !clusterSSHStorageName(v.PV) || !clusterSSHStorageName(v.StorageClass) ||
			!validBytes(v.Bytes) || (v.Replicas != 1 && v.Replicas != 3) || !textInSet(v.DataLocality, "disabled", "strict-local") ||
			(v.DataLocality == "strict-local" && v.Replicas != 1) || !textInSet(v.State, "attached", "detached") || !textInSet(v.Robustness, "healthy", "degraded", "unknown") {
			return false
		}
		for _, id := range []string{v.ClaimUID, v.PVUID, v.VolumeUID} {
			if !clusterUUIDRE.MatchString(id) || id == "00000000-0000-0000-0000-000000000000" {
				return false
			}
		}
		if seenPV[v.PV] || seenClaimUID[v.ClaimUID] || seenPVUID[v.PVUID] || seenVolumeUID[v.VolumeUID] {
			return false
		}
		previous = v.Claim
		seenPV[v.PV], seenClaimUID[v.ClaimUID], seenPVUID[v.PVUID], seenVolumeUID[v.VolumeUID] = true, true, true, true
		switch v.Role {
		case "artifacts":
			if v.Claim != clusterSharedArtifactPVCName || v.Bytes != r.ArtifactReplicaBytes || v.Node != "" || v.DataLocality != "disabled" || v.Replicas < int64(len(nodes)) || v.State != "attached" || !textInSet(v.Robustness, "healthy", "degraded") {
				return false
			}
			artifact++
		case "postgres":
			if !clusterSSHStorageInstanceRE.MatchString(v.Claim) || v.Bytes != r.PostgresInstanceBytes || !nodes[v.Node] || occupied[v.Node] || v.DataLocality != "strict-local" || v.Replicas != 1 || v.State != "attached" || v.Robustness != "healthy" {
				return false
			}
			if pgClass != "" && pgClass != v.StorageClass {
				return false
			}
			pgClass = v.StorageClass
			occupied[v.Node] = true
			pg++
		case "other":
			if v.Claim == clusterSharedArtifactPVCName || v.Node != "" {
				return false
			}
		default:
			return false
		}
	}
	return artifact == 1 && pg == len(nodes)
}
