package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"errors"
	"os"
	"path/filepath"
)

const bootstrapPrejoinEngine = "/opt/Borealis/Engine"
const bootstrapPrejoinSecrets = bootstrapPrejoinEngine + "/Services/api-backend/secrets"

func bootstrapPrejoinInstallation(ctx context.Context, request clusterbootstrap.PreparationSessionRequest, authority func(context.Context) error) (engineidentity.Installation, error) {
	invalid := engineidentity.Installation{}
	if _, err := request.Expected(); err != nil || request.IdentitySource == nil || authority == nil {
		return invalid, errBootstrapSource
	}
	quiesced, err := bootstrapPrejoinQuiescence(ctx, request)
	if err != nil {
		return invalid, err
	}
	return engineidentity.Installation{
		Root: bootstrapPrejoinSecrets, Journal: filepath.Join(bootstrapMutationRoot, "identity-attempt"),
		Binding: *request.IdentitySource,
		Target:  engineidentity.TargetBinding{ProvisioningID: request.Session.Binding.TargetID, SSHFingerprint: request.Session.Binding.HostKeyFingerprint},
		UID:     actionRuntimeID, GID: actionRuntimeID,
		Check: func() error {
			if authority(ctx) != nil || proveBootstrapRuntimeAccount(ctx) != nil || ctx.Err() != nil {
				return errBootstrapSource
			}
			return nil
		}, Quiesced: quiesced,
	}, nil
}

// Fresh targets have no workload inventory to substitute for actual custody.
// Pin the existing Engine ancestor and the newly published identity mount;
// scan known host consumers, mount namespaces and all runtime UID credentials.
// Quiescence does not depend on the controller lease, so rollback can still
// prove stopped consumers after authority loss. It never stops a process.
func bootstrapPrejoinQuiescence(ctx context.Context, request clusterbootstrap.PreparationSessionRequest) (func() error, error) {
	if _, err := request.Expected(); err != nil || request.IdentitySource == nil {
		return nil, errBootstrapSource
	}
	return newBootstrapPrejoinQuiescence(ctx, request.Action == "prepare_prejoin_identity_root", bootstrapPrejoinObservers{
		boundary: func() error {
			if verifyBootstrapSource(ctx, request, false) != nil || proveBootstrapRuntimeAccount(ctx) != nil {
				return errBootstrapSource
			}
			return nil
		},
		anchor: func() (identityRuntimeMount, error) { return captureIdentityRuntimeMount(bootstrapPrejoinEngine, 0, 0) },
		identity: func() (identityRuntimeMount, bool, error) {
			if _, err := os.Lstat(bootstrapPrejoinSecrets); errors.Is(err, os.ErrNotExist) {
				return identityRuntimeMount{}, false, nil
			} else if err != nil {
				return identityRuntimeMount{}, false, errBootstrapSource
			}
			mount, err := captureIdentityRuntimeMount(bootstrapPrejoinSecrets, actionRuntimeID, actionRuntimeID)
			return mount, true, err
		},
		processes: func(path string, mount identityRuntimeMount) error {
			return identityRuntimeProcessesAbsent(ctx, "/proc", path, mount)
		},
	})
}

type bootstrapPrejoinObservers struct {
	boundary  func() error
	anchor    func() (identityRuntimeMount, error)
	identity  func() (identityRuntimeMount, bool, error)
	processes func(string, identityRuntimeMount) error
}

func newBootstrapPrejoinQuiescence(ctx context.Context, create bool, o bootstrapPrejoinObservers) (func() error, error) {
	if o.boundary == nil || o.anchor == nil || o.identity == nil || o.processes == nil || ctx.Err() != nil || o.boundary() != nil {
		return nil, errBootstrapSource
	}
	anchor, err := o.anchor()
	if err != nil || anchor.Device == 0 || anchor.Inode == 0 || anchor.Mount == 0 {
		return nil, errBootstrapSource
	}
	identity, present, err := o.identity()
	if err != nil || present == create {
		return nil, errBootstrapSource
	}
	check := func() error {
		if ctx.Err() != nil || o.boundary() != nil {
			return errBootstrapSource
		}
		before, err := o.anchor()
		if err != nil || before != anchor {
			return errBootstrapSource
		}
		current, exists, err := o.identity()
		if err != nil || present && (!exists || current != identity) || !exists && !create {
			return errBootstrapSource
		}
		if exists {
			if current.Device == 0 || current.Inode == 0 || current.Mount == 0 {
				return errBootstrapSource
			}
			identity, present = current, true
		}
		if o.processes(bootstrapPrejoinEngine, anchor) != nil || exists && o.processes(bootstrapPrejoinSecrets, current) != nil {
			return errBootstrapSource
		}
		after, err := o.anchor()
		if err != nil || after != anchor {
			return errBootstrapSource
		}
		last, lastExists, err := o.identity()
		if err != nil || lastExists != exists || last != current || ctx.Err() != nil || o.boundary() != nil {
			return errBootstrapSource
		}
		return nil
	}
	if check() != nil {
		return nil, errBootstrapSource
	}
	return check, nil
}
