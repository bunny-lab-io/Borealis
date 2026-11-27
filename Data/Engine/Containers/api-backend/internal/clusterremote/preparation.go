package clusterremote

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"golang.org/x/crypto/ssh"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Preparation owns its bulk payload beneath the root wrapper's one private
// directory on the qualified /opt filesystem. The wrapper survives helper
// failure and removes only that directory, including incomplete input scratch.
// The unprivileged upload stays on /var/tmp. No request chooses either path.
const preparationRootStage = `test -d /opt && test ! -L /opt
test "$(stat -c '%u' -- /opt)" = 0
opt_mode=$(stat -c '%a' -- /opt)
test "$((0$opt_mode & 0022))" = 0
root_stage=$(mktemp -d /opt/borealis-bootstrap-root.XXXXXXXXXX)`

func preparationRootScript(artifacts bool) string {
	script := strings.Replace(bootstrapRootScript, "root_stage=$(mktemp -d /var/tmp/borealis-bootstrap-root.XXXXXXXXXX)", preparationRootStage, 1)
	verb := "bootstrap-source-session"
	if artifacts {
		verb = "bootstrap-artifact-session"
	}
	// Both channels run on the same host-key-pinned SSH connection. The
	// root wrapper owns FIFO creation and cleanup, including helper failure.
	script = strings.Replace(script, `chmod 0500 "$root_stage/manager"`, `chmod 0500 "$root_stage/manager"
data_fifo="/run/borealis-ssh-input-$3"
/usr/bin/mkfifo -m 0600 -- "$data_fifo"
trap 'rm -f -- "$data_fifo"; rm -rf -- "$root_stage"' EXIT HUP INT TERM`, 1)
	return strings.Replace(script, "bootstrap-session --nonce", verb+" --nonce", 1)
}

// Fixed preparation verbs retain the shared copy/hash/cleanup and sudo protocol.
func preparationCommand(size int64, digest, nonce string) string {
	return bootstrapCommandWithRoot(size, digest, nonce, preparationRootScript(false))
}

// Fixed artifact command extends only its outer containment budget. The root
// helper still uses the same private upload, digest pin, sudo and cleanup path.
func artifactPreparationCommand(size int64, digest, nonce string) string {
	command := bootstrapCommandWithRoot(size, digest, nonce, preparationRootScript(true))
	return strings.ReplaceAll(command, "--kill-after=2s 6m", "--kill-after=2s 32m")
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

func (client *Client) ResumeFreshPrejoinHost(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	source engineidentity.Binding, material *engineidentity.Material, sudoPassword []byte, check func(context.Context) error) error {
	return client.preparationWithIdentity(ctx, inputs, expected, "resume_fresh_prejoin_host", &source, material, sudoPassword, check)
}

func (client *Client) ResumeRepositoryPrejoinHost(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	source engineidentity.Binding, material *engineidentity.Material, sudoPassword []byte, check func(context.Context) error) error {
	return client.preparationWithIdentity(ctx, inputs, expected, "resume_repository_prejoin_host", &source, material, sudoPassword, check)
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
	return client.preparationWithArtifactsResult(ctx, inputs, expected, action, source, material, nil, "", sudoPassword, check, progress)
}

func (client *Client) PublishPrejoinArtifacts(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	source engineidentity.Binding, material *engineidentity.Material, artifacts *clusterbootstrap.PreparationArtifacts, postgresReference string, sudoPassword []byte, check func(context.Context) error) error {
	return client.preparationWithArtifactsResult(ctx, inputs, expected, "publish_prejoin_artifacts", &source, material, artifacts, postgresReference, sudoPassword, check, nil)
}

func (client *Client) ReconcilePrejoinArtifacts(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	source engineidentity.Binding, material *engineidentity.Material, artifacts *clusterbootstrap.PreparationArtifacts, postgresReference string, sudoPassword []byte, check func(context.Context) error) error {
	return client.preparationWithArtifactsResult(ctx, inputs, expected, "reconcile_prejoin_artifacts", &source, material, artifacts, postgresReference, sudoPassword, check, nil)
}

func (client *Client) InstallPrejoinK3s(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	source engineidentity.Binding, material *engineidentity.Material, artifacts *clusterbootstrap.PreparationArtifacts, postgresReference string, sudoPassword []byte, check func(context.Context) error) error {
	return client.preparationWithArtifactsResult(ctx, inputs, expected, "install_prejoin_k3s", &source, material, artifacts, postgresReference, sudoPassword, check, nil)
}

func (client *Client) ReconcilePrejoinK3s(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	source engineidentity.Binding, material *engineidentity.Material, artifacts *clusterbootstrap.PreparationArtifacts, postgresReference string, sudoPassword []byte, check func(context.Context) error) error {
	return client.preparationWithArtifactsResult(ctx, inputs, expected, "reconcile_prejoin_k3s", &source, material, artifacts, postgresReference, sudoPassword, check, nil)
}

func artifactPreparationAction(action string) bool {
	return action == "publish_prejoin_artifacts" || action == "reconcile_prejoin_artifacts" || action == "install_prejoin_k3s" || action == "reconcile_prejoin_k3s" || action == "configure_prejoin_k3s" || action == "reconcile_prejoin_k3s_configuration" || action == "start_prepared_k3s" || action == "reconcile_prepared_k3s_start" || action == "verify_prepared_images" || action == "reconcile_prepared_images" || action == "activate_prejoin_identity" || action == "reconcile_prejoin_identity_activation"
}

func (client *Client) preparationWithArtifactsResult(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	action string, source *engineidentity.Binding, material *engineidentity.Material, artifacts *clusterbootstrap.PreparationArtifacts, postgresReference string,
	sudoPassword []byte, check func(context.Context) error, progress *clusterbootstrap.PreparationProgress) error {
	return client.preparationWithK3sJoinResult(ctx, inputs, expected, action, source, material, artifacts, postgresReference, nil, sudoPassword, check, progress)
}

func (client *Client) preparationWithK3sJoinResult(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	action string, source *engineidentity.Binding, material *engineidentity.Material, artifacts *clusterbootstrap.PreparationArtifacts, postgresReference string,
	join *clusterbootstrap.K3sJoinMaterial, sudoPassword []byte, check func(context.Context) error, progress *clusterbootstrap.PreparationProgress) error {
	return client.preparationWithCompletion(ctx, inputs, expected, action, source, material, artifacts, postgresReference, join, sudoPassword, check, progress, nil)
}

// The recovery worker embeds its matched helper digest at build time. Only
// that build uses the helper staged alongside the worker by the recovery job.
var pairExecutorSHA256 string

func (client *Client) preparationWithCompletion(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	action string, source *engineidentity.Binding, material *engineidentity.Material, artifacts *clusterbootstrap.PreparationArtifacts, postgresReference string,
	join *clusterbootstrap.K3sJoinMaterial, sudoPassword []byte, check func(context.Context) error, progress *clusterbootstrap.PreparationProgress, completion *clusterbootstrap.PrejoinPreparationReceipt) error {
	configuration := action == "configure_prejoin_k3s" || action == "reconcile_prejoin_k3s_configuration" || action == "start_prepared_k3s" || action == "reconcile_prepared_k3s_start" || action == "verify_prepared_images" || action == "reconcile_prepared_images" || action == "activate_prejoin_identity" || action == "reconcile_prejoin_identity_activation"
	if configuration != (join != nil) {
		return ErrBootstrapVerification
	}
	if artifactPreparationAction(action) {
		if artifacts == nil || source == nil || material == nil || !clusterbootstrap.ValidPostgresImageReference(postgresReference) {
			return ErrBootstrapVerification
		}
	} else if artifacts != nil || postgresReference != "" {
		return ErrBootstrapVerification
	}
	binding := expected.Target
	if client == nil || inputs == nil || expected.Validate() != nil || check == nil || ValidateSudoPassword(sudoPassword) != nil ||
		(source == nil) != (material == nil) ||
		binding.Address != client.target.Address || binding.Port != client.target.Port || binding.HostKeyAlgorithm != client.approved.Algorithm || binding.HostKeyFingerprint != client.approved.Fingerprint {
		return ErrBootstrapVerification
	}
	serialized := preparationAuthority(check, progress != nil)
	bundle, err := inputs.SourceBundle(ctx, expected, serialized)
	if err != nil {
		if progress != nil {
			log.Print("native progress inspection failed at source bundle verification")
		}
		return ErrBootstrapVerification
	}
	bundleSource, digest, size, err := bundle.ManagerProof()
	if err != nil || bundleSource != expected.Source {
		return ErrBootstrapVerification
	}
	managerPath := bundle.ManagerPath()
	if clusterbootstrap.IsPairExecutor() {
		catalog, err := clusterbootstrap.CatalogManagerForExecution(expected, pairExecutorSHA256)
		if err != nil || catalog != digest || pairExecutorSHA256 == digest {
			return ErrBootstrapVerification
		}
		exe, err := os.Executable()
		if err != nil {
			return ErrBootstrapVerification
		}
		managerPath = filepath.Join(filepath.Dir(exe), "pair-node-manager")
		info, err := os.Stat(managerPath)
		if err != nil || !info.Mode().IsRegular() {
			return ErrBootstrapVerification
		}
		digest, size = pairExecutorSHA256, info.Size()
	}
	return client.transferPreparationCompletion(ctx, managerPath, digest, size, expected, action, source, postgresReference, sudoPassword, serialized,
		func(ctx context.Context, output io.Writer) error {
			if clusterbootstrap.HostPackagePreparationAction(action) {
				return inputs.WritePrejoinHostPreparation(ctx, expected, *source, material, output, serialized)
			}
			if join != nil {
				return inputs.WritePrejoinK3sJoinPreparation(ctx, expected, *source, material, artifacts, postgresReference, join, output, serialized)
			}
			if action == "install_prejoin_k3s" || action == "reconcile_prejoin_k3s" {
				return inputs.WritePrejoinPublishedPreparation(ctx, expected, *source, material, artifacts, postgresReference, output, serialized)
			}
			if artifacts != nil {
				return inputs.WritePrejoinArtifactPreparation(ctx, expected, *source, material, artifacts, postgresReference, output, serialized)
			}
			if source != nil {
				return inputs.WritePrejoinPreparation(ctx, expected, *source, material, output, serialized)
			}
			return inputs.WritePreparation(ctx, expected, output, serialized)
		}, progress, completion)
}

// Archive production and the native challenge share one live authority reader.
// A cancelled waiter must leave immediately. Every admitted caller performs a
// new proof under its own deadline, including the caller following a slow read.
func preparationAuthority(check func(context.Context) error, inspecting bool) func(context.Context) error {
	slot := make(chan struct{}, 1)
	return func(ctx context.Context) error {
		start := time.Now()
		wait, cancelWait := context.WithTimeout(ctx, clusterbootstrap.PreparationProofTimeout)
		defer cancelWait()
		select {
		case slot <- struct{}{}:
			defer func() { <-slot }()
		case <-wait.Done():
			if inspecting {
				log.Printf("native progress authority failed: phase=wait_for_reader wait=%s", time.Since(start).Round(time.Millisecond))
			}
			return ErrBootstrapVerification
		}
		if wait.Err() != nil {
			return ErrBootstrapVerification
		}
		began := time.Now()
		err := refreshBootstrapAuthority(ctx, check)
		if inspecting && err != nil {
			log.Printf("native progress authority failed: phase=verify wait=%s proof=%s", began.Sub(start).Round(time.Millisecond), time.Since(began).Round(time.Millisecond))
		}
		return err
	}
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
	return client.transferPreparationArtifactResult(parent, managerPath, digest, size, expected, action, source, "", sudoPassword, check, produce, progress)
}

func (client *Client) transferPreparationArtifactResult(parent context.Context, managerPath, digest string, size int64, expected clusterbootstrap.PreparationExpected,
	action string, source *engineidentity.Binding, postgresReference string, sudoPassword []byte, check func(context.Context) error, produce func(context.Context, io.Writer) error, progress *clusterbootstrap.PreparationProgress) error {
	return client.transferPreparationCompletion(parent, managerPath, digest, size, expected, action, source, postgresReference, sudoPassword, check, produce, progress, nil)
}

type preparationObserverKey struct{}

// WithPreparationObserver receives the same sanitized stage line as the local
// worker log. The callback must return without waiting for database or network IO.
func WithPreparationObserver(ctx context.Context, observe func(string)) context.Context {
	return context.WithValue(ctx, preparationObserverKey{}, observe)
}

// Never carry arbitrary remote output, error text, paths or command lines into
// operation evidence. A signal alone cannot establish a watchdog termination.
func preparationFailureReason(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, clusterbootstrap.ErrSessionAuthority):
		return "authority_rejected"
	case errors.Is(err, clusterbootstrap.ErrSessionProtocol):
		return "invalid_session_protocol"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "unexpected_end_of_stream"
	case errors.Is(err, os.ErrNotExist):
		return "file_missing"
	case errors.Is(err, os.ErrPermission):
		return "permission_denied"
	case errors.Is(err, syscall.ENOSPC):
		return "storage_full"
	default:
		return "underlying_error_withheld"
	}
}

func preparationStderrClass(output *boundedOutput) string {
	output.mu.Lock()
	defer output.mu.Unlock()
	switch {
	case output.overflow:
		return "output_limit"
	case output.buffer.Len() == 0:
		return "empty"
	case bytes.Contains(output.buffer.Bytes(), []byte("sudo: a password is required")):
		return "sudo_password_required"
	case bytes.Contains(output.buffer.Bytes(), []byte("Failed to start transient service unit")):
		return "transient_unit_start_failed"
	default:
		return "other_output_redacted"
	}
}

func (client *Client) transferPreparationCompletion(parent context.Context, managerPath, digest string, size int64, expected clusterbootstrap.PreparationExpected,
	action string, source *engineidentity.Binding, postgresReference string, sudoPassword []byte, check func(context.Context) error, produce func(context.Context, io.Writer) error, progress *clusterbootstrap.PreparationProgress, completion *clusterbootstrap.PrejoinPreparationReceipt) (resultErr error) {
	stage := "validate_action"
	if progress != nil {
		started := time.Now()
		defer func() {
			if resultErr != nil && stage != "exchange" {
				log.Printf("native progress inspection transport failed: phase=%s elapsed=%s", stage, time.Since(started).Round(time.Millisecond))
			}
		}()
	}
	if completion != nil && action != "reconcile_prejoin_identity_activation" {
		return ErrBootstrapVerification
	}
	if (action == "inspect_preparation_progress") != (progress != nil) {
		return ErrBootstrapVerification
	}
	lifetime := clusterbootstrap.SessionLifetime
	if artifactPreparationAction(action) || clusterbootstrap.HostPackagePreparationAction(action) {
		lifetime = clusterbootstrap.ArtifactPreparationLifetime
	}
	ctx, cancel := context.WithTimeout(parent, lifetime)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { client.Close() })
	defer stop()
	stage = "initial_authority"
	if refreshBootstrapAuthority(ctx, check) != nil {
		return ErrBootstrapVerification
	}
	stage = "session_request"
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return ErrBootstrapVerification
	}
	nonce := hex.EncodeToString(random[:])
	request, err := clusterbootstrap.NewPreparationSessionRequest(expected, digest, nonce, action)
	if source != nil {
		request, err = clusterbootstrap.NewPrejoinPreparationSessionRequest(expected, digest, nonce, action, *source)
	}
	if artifactPreparationAction(action) && source != nil {
		request, err = clusterbootstrap.NewArtifactPreparationSessionRequest(expected, digest, nonce, action, *source, postgresReference)
	} else if postgresReference != "" {
		return ErrBootstrapVerification
	}
	if err != nil {
		return ErrBootstrapVerification
	}
	// The request constructor validated the identifiers and action. Only fixed
	// stage names, statuses and elapsed time cross the diagnostic boundary.
	started := time.Now()
	stageStart := started
	last := stage
	exitCode := -1
	reason := "check_rejected"
	var diagnostics boundedOutput
	journal := "unverified"
	expectedCondition := map[string]string{
		"session_request": "validated_binding_and_action", "manager_file": "verified_helper_size_and_file", "ssh_session": "pinned_session_and_pipes",
		"wrapper_start": "supervised_command_accepted", "manager_upload": "complete_helper_transfer_under_authority", "sudo_input": "complete_protected_input",
		"exchange": "verified_protocol_result", "wrapper_exit": "exit_zero_and_bounded_stderr", "final_authority": "current_execution_authority",
	}
	emit := func(status string) {
		stderrClass := "not_sampled"
		if status == "failed" || stage == "wrapper_exit" && status == "completed" {
			stderrClass = preparationStderrClass(&diagnostics)
		}
		line := fmt.Sprintf("SSH native preparation operation=%s target=%s attempt=%d generation=%d action=%s phase=%s status=%s last_completed=%s phase_ms=%d elapsed_ms=%d reason=%s exit_code=%d stderr=%s expected=%s journal_checkpoint=%s",
			expected.Target.OperationID, expected.Target.TargetID, expected.Target.OperationAttempt, expected.Target.Generation, request.Action, stage, status, last, time.Since(stageStart).Milliseconds(), time.Since(started).Milliseconds(), reason, exitCode, stderrClass, expectedCondition[stage], journal)
		log.Print(line)
		if observe, ok := parent.Value(preparationObserverKey{}).(func(string)); ok && observe != nil {
			observe(line)
		}
	}
	advance := func(next string) {
		reason = "passed"
		last = stage
		emit("completed")
		stage = next
		stageStart = time.Now()
		reason = "pending"
		emit("entered")
		reason = "check_rejected"
	}
	defer func() {
		if resultErr == nil {
			last = stage
			reason = "passed"
			emit("completed")
			return
		}
		if ctx.Err() == context.DeadlineExceeded {
			reason = "deadline_exceeded"
		} else if ctx.Err() == context.Canceled {
			reason = "canceled"
		}
		emit("failed")
	}()
	advance("manager_file")
	manager, err := os.Open(managerPath)
	if err != nil {
		reason = preparationFailureReason(err)
		return ErrBootstrapVerification
	}
	defer manager.Close()
	info, err := manager.Stat()
	if err != nil || !info.Mode().IsRegular() || size < 1 || size > clusterbootstrap.MaxBundleBytes || info.Size() != size {
		return ErrBootstrapVerification
	}
	advance("ssh_session")
	session, err := client.ssh.NewSession()
	if err != nil {
		reason = preparationFailureReason(err)
		return ErrBootstrapVerification
	}
	defer session.Close()
	diagnostics.close = func() { client.Close() }
	session.Stderr = &diagnostics
	stdin, err := session.StdinPipe()
	if err != nil {
		reason = preparationFailureReason(err)
		return ErrBootstrapVerification
	}
	stdout, err := session.StdoutPipe()
	command := preparationCommand(size, digest, nonce)
	if artifactPreparationAction(action) || clusterbootstrap.HostPackagePreparationAction(action) {
		command = artifactPreparationCommand(size, digest, nonce)
	}
	advance("wrapper_start")
	if err != nil {
		reason = preparationFailureReason(err)
		return ErrBootstrapVerification
	}
	if err := session.Start(command); err != nil {
		reason = preparationFailureReason(err)
		return ErrBootstrapVerification
	}
	advance("manager_upload")
	buffer := make([]byte, 128<<10)
	remaining, checked := size, time.Now()
	for remaining > 0 {
		if time.Since(checked) >= clusterbootstrap.SessionChallengeEvery {
			if refreshBootstrapAuthority(ctx, check) != nil {
				reason = "authority_rejected"
				return ErrBootstrapVerification
			}
			checked = time.Now()
		}
		n, readErr := manager.Read(buffer[:min(int64(len(buffer)), remaining)])
		if n == 0 || readErr != nil || ctx.Err() != nil {
			return ErrBootstrapVerification
		}
		if _, err := io.Copy(stdin, bytes.NewReader(buffer[:n])); err != nil {
			reason = preparationFailureReason(err)
			return ErrBootstrapVerification
		}
		remaining -= int64(n)
	}
	advance("sudo_input")
	secret := append(bytes.Clone(sudoPassword), '\n')
	defer clear(secret)
	if _, err := io.Copy(stdin, bytes.NewReader(secret)); err != nil {
		reason = preparationFailureReason(err)
		return ErrBootstrapVerification
	}
	// Closing the session releases a blocked stdout reader on cancellation.
	input := preparationSSHOutput{Reader: stdout, close: func() error { return session.Close() }}
	output := preparationSSHInput{WriteCloser: stdin, close: func() error { return client.Close() }}
	advance("exchange")
	var receipt clusterbootstrap.PrejoinPreparationReceipt
	var cursor clusterbootstrap.PreparationProgress
	upload := func(ctx context.Context) (clusterbootstrap.PreparationStreamReceipt, error) {
		return client.uploadPreparation(ctx, nonce, sudoPassword, produce)
	}
	var p *clusterbootstrap.PreparationProgress
	var r *clusterbootstrap.PrejoinPreparationReceipt
	if progress != nil {
		p = &cursor
	}
	if completion != nil {
		r = &receipt
	}
	err = clusterbootstrap.ExchangeSSHPreparation(ctx, input, output, request, upload, check, p, r)
	if err != nil {
		reason = preparationFailureReason(err)
		return ErrBootstrapVerification
	}
	advance("wrapper_exit")
	if err := session.Wait(); err != nil {
		var exit *ssh.ExitError
		if errors.As(err, &exit) {
			exitCode = exit.ExitStatus()
			reason = "remote_process_exit"
		} else {
			reason = "wrapper_exit_unconfirmed"
		}
		return ErrBootstrapVerification
	}
	exitCode = 0
	if diagnostics.overflow {
		reason = "stderr_limit_exceeded"
		return ErrBootstrapVerification
	}
	advance("final_authority")
	if refreshBootstrapAuthority(ctx, check) != nil {
		reason = "authority_rejected"
		return ErrBootstrapVerification
	}
	if progress != nil {
		journal = cursor.Step
		*progress = cursor
	}
	if completion != nil {
		*completion = receipt
	}
	return nil
}

type preparationSSHOutput struct {
	io.Reader
	close func() error
}

func (r preparationSSHOutput) Close() error { return r.close() }

type preparationSSHInput struct {
	io.WriteCloser
	close func() error
}

func (r preparationSSHInput) Close() error      { return r.close() }
func (r preparationSSHInput) CloseWrite() error { return r.WriteCloser.Close() }

// Root and passwordless sudo leave the password line on stdin. Password sudo
// consumes it. A fixed nonce marker unambiguously separates login from data.
// dd cannot create a path if the owning wrapper has already cleaned up.
func (client *Client) uploadPreparation(ctx context.Context, nonce string, password []byte, produce func(context.Context, io.Writer) error) (clusterbootstrap.PreparationStreamReceipt, error) {
	var empty clusterbootstrap.PreparationStreamReceipt
	if _, err := clusterbootstrap.SessionPreamble(nonce); err != nil {
		return empty, err
	}
	s, err := client.ssh.NewSession()
	if err != nil {
		return empty, err
	}
	defer s.Close()
	stop := context.AfterFunc(ctx, func() { client.Close() })
	defer stop()
	in, err := s.StdinPipe()
	if err != nil {
		return empty, err
	}
	defer in.Close()
	if err := s.Start(preparationUploadCommand(nonce)); err != nil {
		return empty, err
	}
	prefix := append(bytes.Clone(password), '\n')
	prefix = append(prefix, nonce...)
	prefix = append(prefix, '\n')
	_, err = in.Write(prefix)
	clear(prefix)
	if err != nil {
		return empty, err
	}
	result, err := clusterbootstrap.StreamPreparation(ctx, in, produce)
	if err != nil || in.Close() != nil || s.Wait() != nil || ctx.Err() != nil {
		return empty, ErrBootstrapVerification
	}
	return result, nil
}

func preparationUploadCommand(nonce string) string {
	const copyScript = `set -eu
IFS= read -r line
if [ "$line" != "$1" ]; then IFS= read -r line; fi
test "$line" = "$1"
unset line
pipe=/run/borealis-ssh-input-$1
test -p "$pipe" && test ! -L "$pipe" && test "$(/usr/bin/stat -c %u:%a -- "$pipe")" = 0:600
exec /usr/bin/dd "of=$pipe" bs=128K conv=nocreat,notrunc status=none`
	copyCommand := "/usr/bin/timeout --kill-after=2s 32m /bin/sh -c " + shellConstant(copyScript) + " copy " + shellConstant(nonce)
	return `if [ "$(id -u)" = 0 ]; then exec ` + copyCommand + `; else exec sudo -k -S -p '' -- ` + copyCommand + `; fi`
}
