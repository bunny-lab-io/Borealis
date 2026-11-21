package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"crypto/tls"
	"net/http"
	"os"
)

func observeBootstrapK3sStartup(ctx context.Context, expected clusterbootstrap.PreparationExpected, source engineidentity.Binding, join *clusterbootstrap.K3sJoinMaterial, check func(context.Context) error) (clusterbootstrap.K3sStartedObservation, error) {
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		return clusterbootstrap.K3sStartedObservation{}, errBootstrapSource
	}
	root, err := os.OpenRoot("/")
	if err != nil {
		return clusterbootstrap.K3sStartedObservation{}, errBootstrapSource
	}
	defer root.Close()
	return observeBootstrapK3sStartupAt(ctx, root, expected, source, join, bootstrapJoinedHostCheck(expected, check), newBootstrapJoinedNodeClient)
}

func observeBootstrapK3sStartupAt(ctx context.Context, root *os.Root, expected clusterbootstrap.PreparationExpected, source engineidentity.Binding, join *clusterbootstrap.K3sJoinMaterial,
	check func(context.Context) error, clientFor func(*tls.Config) *http.Client) (clusterbootstrap.K3sStartedObservation, error) {
	var result clusterbootstrap.K3sStartedObservation
	err := withBootstrapJoinedK3sClientAt(ctx, root, expected, source, join, check, clientFor, true, func(ctx context.Context, refresh func(context.Context) error, get func(context.Context, string) ([]byte, error), name string) error {
		read := func() (clusterbootstrap.K3sMembership, error) {
			raw, err := get(ctx, "/db/info")
			defer clear(raw)
			if err != nil {
				return clusterbootstrap.K3sMembership{}, errBootstrapSource
			}
			return clusterbootstrap.ParseK3sMembership(raw, expected, name)
		}
		before, err := read()
		if err != nil {
			return errBootstrapSource
		}
		node, err := observeBootstrapJoinedNodeWith(ctx, expected, refresh, get)
		if err != nil {
			return errBootstrapSource
		}
		after, err := read()
		if err != nil || before.LocalID != after.LocalID {
			return errBootstrapSource
		}
		// Approved peers may register/promote while startup converges. Existing
		// member identity cannot disappear/change; a voter cannot become learner.
		for _, prior := range before.Members {
			found := false
			for _, current := range after.Members {
				if prior.ID != current.ID {
					continue
				}
				if prior.Address != current.Address || prior.Name != "" && prior.Name != current.Name || !prior.Learner && current.Learner {
					return errBootstrapSource
				}
				found = true
			}
			if !found {
				return errBootstrapSource
			}
		}
		result = clusterbootstrap.K3sStartedObservation{Node: node, Membership: after}
		return nil
	})
	if err != nil {
		return clusterbootstrap.K3sStartedObservation{}, errBootstrapSource
	}
	return result, nil
}
