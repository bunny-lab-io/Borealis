package clusterremote

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Only this compile-time verb differs from joined recovery. No request supplies
// shell text, executable path, unit properties or a command to execute.
func preparationCommand(size int64, digest, nonce string) string {
	return bootstrapCommandWithRoot(size, digest, nonce, strings.Replace(bootstrapRootScript, "bootstrap-session --nonce", "bootstrap-source-session --nonce", 1))
}

// DeliverPreparation publishes source/configuration only. Controller must hold
// exact preparation authority and all native qualification scopes in check.
// Failed/unknown delivery must never be retried as a new write; use separate
// proof-only ReconcilePreparation after proving the old executor stopped.
func (client *Client) DeliverPreparation(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	sudoPassword []byte, check func(context.Context) error) error {
	return client.preparation(ctx, inputs, expected, "stage_source", sudoPassword, check)
}

func (client *Client) ReconcilePreparation(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	sudoPassword []byte, check func(context.Context) error) error {
	return client.preparation(ctx, inputs, expected, "reconcile_source", sudoPassword, check)
}

// ReconcilePreparationAbsent independently proves no atomic source publication.
// It never retries the write or removes interrupted private scratch.
func (client *Client) ReconcilePreparationAbsent(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	sudoPassword []byte, check func(context.Context) error) error {
	return client.preparation(ctx, inputs, expected, "reconcile_source_absent", sudoPassword, check)
}

func (client *Client) PrepareRuntimeIdentity(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected, sudoPassword []byte, check func(context.Context) error) error {
	return client.preparation(ctx, inputs, expected, "prepare_runtime_identity", sudoPassword, check)
}

// CompleteRuntimeIdentity is fixed group-only forward recovery. Native journal
// requires a newer claim and durably retains the old producer before useradd.
func (client *Client) CompleteRuntimeIdentity(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected, sudoPassword []byte, check func(context.Context) error) error {
	return client.preparation(ctx, inputs, expected, "complete_runtime_identity", sudoPassword, check)
}

func (client *Client) ReconcileRuntimeIdentity(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected, sudoPassword []byte, check func(context.Context) error) error {
	return client.preparation(ctx, inputs, expected, "reconcile_runtime_identity", sudoPassword, check)
}

func (client *Client) preparation(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	action string, sudoPassword []byte, check func(context.Context) error) error {
	return client.preparationWithIdentity(ctx, inputs, expected, action, nil, nil, sudoPassword, check)
}

func (client *Client) PreparePrejoinIdentityRoot(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	source engineidentity.Binding, material *engineidentity.Material, sudoPassword []byte, check func(context.Context) error) error {
	return client.preparationWithIdentity(ctx, inputs, expected, "prepare_prejoin_identity_root", &source, material, sudoPassword, check)
}

func (client *Client) ReconcilePrejoinIdentityRoot(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	source engineidentity.Binding, material *engineidentity.Material, sudoPassword []byte, check func(context.Context) error) error {
	return client.preparationWithIdentity(ctx, inputs, expected, "reconcile_prejoin_identity_root", &source, material, sudoPassword, check)
}

func (client *Client) InstallPrejoinIdentity(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	source engineidentity.Binding, material *engineidentity.Material, sudoPassword []byte, check func(context.Context) error) error {
	return client.preparationWithIdentity(ctx, inputs, expected, "install_prejoin_identity", &source, material, sudoPassword, check)
}

func (client *Client) ReconcilePrejoinIdentity(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	source engineidentity.Binding, material *engineidentity.Material, sudoPassword []byte, check func(context.Context) error) error {
	return client.preparationWithIdentity(ctx, inputs, expected, "reconcile_prejoin_identity", &source, material, sudoPassword, check)
}

// Host effects retain the same original-source material and both target fences.
// Reconciliation is a distinct supervised proof session, never a write retry.
func (client *Client) PreparePrejoinHost(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	source engineidentity.Binding, material *engineidentity.Material, sudoPassword []byte, check func(context.Context) error) error {
	return client.preparationWithIdentity(ctx, inputs, expected, "prepare_prejoin_host", &source, material, sudoPassword, check)
}

func (client *Client) CompletePrejoinFirewall(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	source engineidentity.Binding, material *engineidentity.Material, sudoPassword []byte, check func(context.Context) error) error {
	return client.preparationWithIdentity(ctx, inputs, expected, "complete_prejoin_firewall", &source, material, sudoPassword, check)
}

func (client *Client) CompletePrejoinHost(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	source engineidentity.Binding, material *engineidentity.Material, sudoPassword []byte, check func(context.Context) error) error {
	return client.preparationWithIdentity(ctx, inputs, expected, "complete_prejoin_host", &source, material, sudoPassword, check)
}

func (client *Client) ReconcilePrejoinHost(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	source engineidentity.Binding, material *engineidentity.Material, sudoPassword []byte, check func(context.Context) error) error {
	return client.preparationWithIdentity(ctx, inputs, expected, "reconcile_prejoin_host", &source, material, sudoPassword, check)
}

func (client *Client) preparationWithIdentity(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	action string, source *engineidentity.Binding, material *engineidentity.Material, sudoPassword []byte, check func(context.Context) error) error {
	return client.preparationWithResult(ctx, inputs, expected, action, source, material, sudoPassword, check, nil)
}

func (client *Client) InspectPreparationProgress(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	sudoPassword []byte, check func(context.Context) error) (clusterbootstrap.PreparationProgress, error) {
	var progress clusterbootstrap.PreparationProgress
	if err := client.preparationWithResult(ctx, inputs, expected, "inspect_preparation_progress", nil, nil, sudoPassword, check, &progress); err != nil {
		return clusterbootstrap.PreparationProgress{}, err
	}
	return progress, nil
}

func (client *Client) preparationWithResult(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	action string, source *engineidentity.Binding, material *engineidentity.Material, sudoPassword []byte, check func(context.Context) error, progress *clusterbootstrap.PreparationProgress) error {
	binding := expected.Target
	if client == nil || inputs == nil || expected.Validate() != nil || check == nil || ValidateSudoPassword(sudoPassword) != nil ||
		(source == nil) != (material == nil) ||
		binding.Address != client.target.Address || binding.Port != client.target.Port || binding.HostKeyAlgorithm != client.approved.Algorithm || binding.HostKeyFingerprint != client.approved.Fingerprint {
		return ErrBootstrapVerification
	}
	// Archive production and challenge handling run concurrently. Serialize
	// caller authority callbacks without detached renewals.
	var authorityMu sync.Mutex
	serialized := func(ctx context.Context) error {
		authorityMu.Lock()
		defer authorityMu.Unlock()
		return refreshBootstrapAuthority(ctx, check)
	}
	bundle, err := inputs.SourceBundle(ctx, expected, serialized)
	if err != nil {
		return ErrBootstrapVerification
	}
	bundleSource, digest, size, err := bundle.ManagerProof()
	if err != nil || bundleSource != expected.Source {
		return ErrBootstrapVerification
	}
	return client.transferPreparationWithResult(ctx, bundle.ManagerPath(), digest, size, expected, action, source, sudoPassword, serialized,
		func(ctx context.Context, output io.Writer) error {
			if source != nil {
				return inputs.WritePrejoinPreparation(ctx, expected, *source, material, output, serialized)
			}
			return inputs.WritePreparation(ctx, expected, output, serialized)
		}, progress)
}

// Package-private executable seam supports pinned loopback SSH fixtures. Public
// entrypoints always require a freshly verified PreparationInputs bundle.
func (client *Client) transferPreparation(parent context.Context, managerPath, digest string, size int64, expected clusterbootstrap.PreparationExpected,
	action string, sudoPassword []byte, check func(context.Context) error, produce func(context.Context, io.Writer) error) error {
	return client.transferPreparationWithIdentity(parent, managerPath, digest, size, expected, action, nil, sudoPassword, check, produce)
}

func (client *Client) transferPreparationWithIdentity(parent context.Context, managerPath, digest string, size int64, expected clusterbootstrap.PreparationExpected,
	action string, source *engineidentity.Binding, sudoPassword []byte, check func(context.Context) error, produce func(context.Context, io.Writer) error) error {
	return client.transferPreparationWithResult(parent, managerPath, digest, size, expected, action, source, sudoPassword, check, produce, nil)
}

func (client *Client) transferPreparationWithResult(parent context.Context, managerPath, digest string, size int64, expected clusterbootstrap.PreparationExpected,
	action string, source *engineidentity.Binding, sudoPassword []byte, check func(context.Context) error, produce func(context.Context, io.Writer) error, progress *clusterbootstrap.PreparationProgress) error {
	if (action == "inspect_preparation_progress") != (progress != nil) {
		return ErrBootstrapVerification
	}
	ctx, cancel := context.WithTimeout(parent, clusterbootstrap.SessionLifetime)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { client.Close() })
	defer stop()
	if refreshBootstrapAuthority(ctx, check) != nil {
		return ErrBootstrapVerification
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return ErrBootstrapVerification
	}
	nonce := hex.EncodeToString(random[:])
	request, err := clusterbootstrap.NewPreparationSessionRequest(expected, digest, nonce, action)
	if source != nil {
		request, err = clusterbootstrap.NewPrejoinPreparationSessionRequest(expected, digest, nonce, action, *source)
	}
	if err != nil {
		return ErrBootstrapVerification
	}
	manager, err := os.Open(managerPath)
	if err != nil {
		return ErrBootstrapVerification
	}
	defer manager.Close()
	info, err := manager.Stat()
	if err != nil || !info.Mode().IsRegular() || size < 1 || size > clusterbootstrap.MaxBundleBytes || info.Size() != size {
		return ErrBootstrapVerification
	}
	session, err := client.ssh.NewSession()
	if err != nil {
		return ErrBootstrapVerification
	}
	defer session.Close()
	diagnostics := boundedOutput{close: func() { client.Close() }}
	session.Stderr = &diagnostics
	stdin, err := session.StdinPipe()
	if err != nil {
		return ErrBootstrapVerification
	}
	defer stdin.Close()
	stdout, err := session.StdoutPipe()
	if err != nil || session.Start(preparationCommand(size, digest, nonce)) != nil {
		return ErrBootstrapVerification
	}
	buffer := make([]byte, 128<<10)
	remaining, checked := size, time.Now()
	for remaining > 0 {
		if time.Since(checked) >= clusterbootstrap.SessionChallengeEvery {
			if refreshBootstrapAuthority(ctx, check) != nil {
				return ErrBootstrapVerification
			}
			checked = time.Now()
		}
		n, readErr := manager.Read(buffer[:min(int64(len(buffer)), remaining)])
		if n == 0 || readErr != nil || ctx.Err() != nil {
			return ErrBootstrapVerification
		}
		if _, err := io.Copy(stdin, bytes.NewReader(buffer[:n])); err != nil {
			return ErrBootstrapVerification
		}
		remaining -= int64(n)
	}
	secret := append(bytes.Clone(sudoPassword), '\n')
	defer clear(secret)
	if _, err := io.Copy(stdin, bytes.NewReader(secret)); err != nil {
		return ErrBootstrapVerification
	}
	// Closing the session releases a blocked stdout reader on cancellation.
	input := preparationSSHOutput{Reader: stdout, close: func() error { return session.Close() }}
	output := preparationSSHInput{Writer: stdin, close: func() error { return client.Close() }}
	var cursor clusterbootstrap.PreparationProgress
	if progress != nil {
		cursor, err = clusterbootstrap.ExchangePreparationProgressSession(ctx, input, output, request, produce, check)
	} else {
		err = clusterbootstrap.ExchangePreparationSession(ctx, input, output, request, produce, check)
	}
	if err != nil {
		return ErrBootstrapVerification
	}
	_ = stdin.Close()
	if session.Wait() != nil || diagnostics.overflow || refreshBootstrapAuthority(ctx, check) != nil {
		return ErrBootstrapVerification
	}
	if progress != nil {
		*progress = cursor
	}
	return nil
}

type preparationSSHOutput struct {
	io.Reader
	close func() error
}

func (r preparationSSHOutput) Close() error { return r.close() }

type preparationSSHInput struct {
	io.Writer
	close func() error
}

func (r preparationSSHInput) Close() error { return r.close() }
