package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"encoding/json"
)

// withClusterSSHSourceIdentityMaterial keeps fixed identity material inside one
// synchronous controller-owned scope. It reads through the API process's
// existing consumer fence and never serializes, persists or returns material.
// A later SSH delivery caller must consume it inside the callback and retain
// all transport, target-quiescence and mutation-journal boundaries separately.
func withClusterSSHSourceIdentityMaterial(parent context.Context, consumer *engineidentity.Consumer,
	expected engineidentity.Binding, authority clusterSSHPreparationAuthorityRead,
	consume func(context.Context, *engineidentity.Material) error) error {
	fail := func() error { return clusterbootstrap.ErrPreparationConfig }
	const zeroUUID = "00000000-0000-0000-0000-000000000000"
	if consumer == nil || authority == nil || consume == nil || parent.Err() != nil ||
		!clusterUUIDRE.MatchString(expected.ClusterID) || !clusterUUIDRE.MatchString(expected.SourceID) ||
		!clusterUUIDRE.MatchString(expected.SourceUID) || expected.ClusterID == zeroUUID || expected.SourceID == zeroUUID || expected.SourceUID == zeroUUID {
		return fail()
	}
	observe := func() ([]byte, error) {
		value, err := authority(parent)
		if err != nil || value.Baseline.Validate() != nil || !validClusterSSHObservationLease(value.Lease) ||
			validateClusterSSHInspectionCohort(value.Cohort, value.Source) != nil || parent.Err() != nil ||
			value.Source.ClusterID != expected.ClusterID {
			return nil, fail()
		}
		found := false
		for _, member := range value.Source.Members {
			if member.NodeID == expected.SourceID && member.NodeUID == expected.SourceUID {
				found = true
				break
			}
		}
		if !found {
			return nil, fail()
		}
		// Observation time may advance across fresh database reads. Every
		// authority, lease, source member and Node identity must remain exact.
		value.Cohort.ObservedAt = 0
		wire, err := json.Marshal(value)
		if err != nil || len(wire) > 128<<10 {
			return nil, fail()
		}
		return wire, nil
	}
	before, err := observe()
	if err != nil {
		return fail()
	}
	material, err := consumer.Load()
	if err != nil {
		return fail()
	}
	after, err := observe()
	if err != nil || !bytes.Equal(before, after) {
		return fail()
	}
	if consume(parent, material) != nil || parent.Err() != nil {
		return fail()
	}
	final, err := observe()
	if err != nil || !bytes.Equal(before, final) || parent.Err() != nil {
		return fail()
	}
	return nil
}

func withClusterSSHProcessIdentityMaterial(parent context.Context, expected engineidentity.Binding,
	authority clusterSSHPreparationAuthorityRead, consume func(context.Context, *engineidentity.Material) error) error {
	return withClusterSSHSourceIdentityMaterial(parent, processIdentityConsumer, expected, authority, consume)
}
