package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
)

func validClusterSSHRegisteredTargets(authority clusterSSHPreparationAuthority, nodes map[string]string, joining bool) bool {
	if !joining {
		return nodes == nil
	}
	if len(nodes) > len(authority.Cohort.Targets) {
		return false
	}
	seen := map[string]bool{}
	for _, member := range authority.Source.Members {
		seen[member.NodeUID] = true
	}
	for id, uid := range nodes {
		if !clusterUUIDRE.MatchString(uid) || uid == "00000000-0000-0000-0000-000000000000" || seen[uid] {
			return false
		}
		found := false
		for _, target := range authority.Cohort.Targets {
			if target.Binding.TargetID == id {
				found = true
				break
			}
		}
		if !found {
			return false
		}
		seen[uid] = true
	}
	return true
}

// A worker pins registered UIDs across fresh encrypted controller observations.
// Growth is allowed; disappearance, replacement or authority/source drift latch
// failure for the entire private identity scope, including ignored failures.
func newClusterSSHJoiningCustodyRead(read func(context.Context) (clusterSSHJoiningSourceCustody, error)) func(context.Context) (clusterSSHPrejoinSourceCustody, error) {
	gate := make(chan struct{}, 1)
	var original *clusterSSHPrejoinSourceCustody
	nodes := map[string]string{}
	failed := false
	return func(ctx context.Context) (clusterSSHPrejoinSourceCustody, error) {
		invalid := clusterSSHPrejoinSourceCustody{}
		if read == nil || ctx.Err() != nil {
			return invalid, clusterbootstrap.ErrSessionAuthority
		}
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		case <-ctx.Done():
			return invalid, clusterbootstrap.ErrSessionAuthority
		}
		fail := func() (clusterSSHPrejoinSourceCustody, error) {
			failed = true
			return invalid, clusterbootstrap.ErrSessionAuthority
		}
		if failed || ctx.Err() != nil {
			return fail()
		}
		value, err := read(ctx)
		if err != nil || ctx.Err() != nil || !value.Original.valid(value.Original.Source) || original != nil && value.Original != *original || len(value.Registered) > 2 {
			return fail()
		}
		for id, uid := range nodes {
			if value.Registered[id] != uid {
				return fail()
			}
		}
		owned := map[string]string{}
		seen := map[string]bool{}
		for id, uid := range value.Registered {
			if !clusterUUIDRE.MatchString(id) || !clusterUUIDRE.MatchString(uid) || id == "00000000-0000-0000-0000-000000000000" || uid == "00000000-0000-0000-0000-000000000000" || seen[uid] {
				return fail()
			}
			seen[uid], owned[id] = true, uid
		}
		copy := value.Original
		original, nodes = &copy, owned
		return copy, nil
	}
}
