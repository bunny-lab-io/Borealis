package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

type clusterSSHPreparationProposalClient interface {
	InspectPreparationProposal(context.Context, []byte, clusterremote.PreparationProposal, func(context.Context) error) (clusterremote.PreparationProposalProof, error)
	Close() error
}

type clusterSSHPreparationProposalConnect func(context.Context, clusterremote.Target, clusterremote.HostKey, *clusterremote.Credential) (clusterSSHPreparationProposalClient, error)

// This callable reader requires an existing Aegis-capable executor. It does not
// propagate keys into the controller, create a worker claim or install polling.
func (c *clusterController) preparationCleanTargetsRead(snapshot clusterSSHPreparationApprovalSnapshot, aegis clusterSSHWorkerAegis) func(context.Context, string) (string, error) {
	return newClusterSSHPreparationCleanTargetsRead(snapshot, c.preparationCleanTargetsProposalCheck(snapshot), aegis, connectClusterSSHPreparationProposal)
}

func connectClusterSSHPreparationProposal(ctx context.Context, target clusterremote.Target, key clusterremote.HostKey, credential *clusterremote.Credential) (clusterSSHPreparationProposalClient, error) {
	client, err := (clusterremote.Transport{}).Connect(ctx, target, key, credential)
	if err != nil {
		// Do not turn a nil *Client into a non-nil interface whose Close panics.
		return nil, err
	}
	return client, nil
}

// Every proposal read releases its SQL connections before hashing, decryption
// or SSH. The eventual approval transaction retains its separate final locks,
// database-clock fences and all six independent phase proofs.
func (c *clusterController) preparationCleanTargetsProposalCheck(snapshot clusterSSHPreparationApprovalSnapshot) func(context.Context) error {
	if c == nil {
		return func(context.Context) error { return clusterbootstrap.ErrPreparationConfig }
	}
	check := c.store.preparationCleanTargetsProposalCheck(snapshot, c.holder)
	return func(ctx context.Context) error {
		if c.holder != snapshot.work.Authorization.ControllerHolder {
			return clusterbootstrap.ErrPreparationConfig
		}
		return check(ctx)
	}
}

// API execution uses the recorded original controller holder, never a synthetic
// controller or a new lease. The controller wrapper keeps its own holder fence.
func (s *postgresOperatorStore) preparationCleanTargetsProposalCheck(snapshot clusterSSHPreparationApprovalSnapshot, holder string) func(context.Context) error {
	wire, wireErr := json.Marshal(snapshot.work)
	credentials := slices.Clone(snapshot.credentials)
	operation := clusterControllerOperation{ID: snapshot.work.Authorization.OperationID, Attempt: snapshot.work.Authorization.Attempt,
		Kind: "ssh_onboarding", State: "waiting", CurrentStep: clusterSSHQualificationStep}
	return func(parent context.Context) error {
		if s == nil || s.db == nil || holder != snapshot.work.Authorization.ControllerHolder || wireErr != nil || parent.Err() != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		ctx, cancel := context.WithTimeout(parent, 3*time.Second)
		defer cancel()
		current, _, err := s.readClusterSSHPreparationProposal(ctx, operation, holder)
		actual, encodeErr := json.Marshal(current.work)
		if err != nil || encodeErr != nil || !bytes.Equal(wire, actual) || !slices.Equal(credentials, current.credentials) || ctx.Err() != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		return nil
	}
}

// The private seam supplies only a fresh original-target read. Complete cohort
// rounds must agree; future calls reconnect and reobserve instead of accepting
// an old receipt. One failure permanently invalidates this reader.
func newClusterSSHPreparationCleanTargetsRead(snapshot clusterSSHPreparationApprovalSnapshot, proposal func(context.Context) error,
	aegis clusterSSHWorkerAegis, connect clusterSSHPreparationProposalConnect) func(context.Context, string) (string, error) {
	read := newClusterSSHPreparationTargetsRead(snapshot, proposal, aegis, connect)
	return func(ctx context.Context, plan string) (string, error) {
		value, err := read(ctx, plan)
		return value.Digest, err
	}
}

func newClusterSSHPreparationTargetsRead(snapshot clusterSSHPreparationApprovalSnapshot, proposal func(context.Context) error,
	aegis clusterSSHWorkerAegis, connect clusterSSHPreparationProposalConnect) clusterSSHPreparationTargetsRead {
	return newClusterSSHPreparationTargetsFilesystemRead(snapshot, proposal, aegis, connect, nil)
}

func newClusterSSHPreparationTargetsFilesystemRead(snapshot clusterSSHPreparationApprovalSnapshot, proposal func(context.Context) error,
	aegis clusterSSHWorkerAegis, connect clusterSSHPreparationProposalConnect, paths []string) clusterSSHPreparationTargetsRead {
	paths = slices.Clone(paths)
	wire, wireErr := json.Marshal(snapshot.work)
	var work clusterSSHPreparationWork
	decodeErr := json.Unmarshal(wire, &work)
	credentials := slices.Clone(snapshot.credentials)
	plan, planErr := clusterSSHPreparationPlanDigest(work.Authorization)
	valid := validClusterSSHPreparationFilesystemPaths(paths) && wireErr == nil && decodeErr == nil && planErr == nil && len(work.Claims) == 0 && work.Baseline.Validate() == nil &&
		validateClusterSSHInspectionCohort(work.Proof.Cohort, work.Proof.Source) == nil && len(credentials) == len(work.Proof.Cohort.Targets) &&
		len(work.Authorization.Targets) == len(credentials) && len(credentials) > 0 && proposal != nil && aegis != nil && connect != nil &&
		work.Authorization.OperationID == work.Proof.Cohort.OperationID && work.Authorization.Attempt == work.Proof.Cohort.Attempt &&
		work.Authorization.ControllerHolder == work.Proof.Cohort.ControllerHolder
	if valid {
		for i, item := range work.Proof.Cohort.Targets {
			reserved, sealed := work.Authorization.Targets[i], credentials[i]
			credentialDigest, err := clusterSSHPreparationDigest([]string{sealed.generation, sealed.ciphertext})
			if reserved.TargetID != item.Binding.TargetID || reserved.QualificationGeneration <= item.Generation || sealed.binding != item.Binding ||
				sealed.generation == "" || len(sealed.generation) > 16<<10 || sealed.generation != credentials[0].generation ||
				!strings.HasPrefix(sealed.ciphertext, aegisEnvelopePrefix) || len(sealed.ciphertext) > 256<<10 ||
				err != nil || credentialDigest != reserved.CredentialSHA256 {
				valid = false
				break
			}
		}
	}
	serial := make(chan struct{}, 1)
	var failed atomic.Bool
	var retained string
	var retainedFilesystems []clusterSSHTargetFilesystemEvidence
	return func(parent context.Context, requested string) (clusterSSHPreparationCleanObservation, error) {
		fail := func() (clusterSSHPreparationCleanObservation, error) {
			failed.Store(true)
			return clusterSSHPreparationCleanObservation{}, clusterbootstrap.ErrPreparationConfig
		}
		if !valid || failed.Load() || parent.Err() != nil || requested != plan {
			return fail()
		}
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		select {
		case serial <- struct{}{}:
			defer func() { <-serial }()
		case <-ctx.Done():
			return fail()
		}
		current := func(ctx context.Context) error {
			if failed.Load() || ctx.Err() != nil || proposal(ctx) != nil || aegis.verifyClusterSSHGeneration(ctx, credentials[0].generation) != nil || ctx.Err() != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			return nil
		}
		if current(ctx) != nil {
			return fail()
		}
		var filesystems []clusterSSHTargetFilesystemEvidence
		read := func(i int) (observed clusterSSHPreparationTargetCPU, result error) {
			item, sealed := work.Proof.Cohort.Targets[i], credentials[i]
			if current(ctx) != nil {
				return clusterSSHPreparationTargetCPU{}, clusterbootstrap.ErrPreparationConfig
			}
			envelope, err := aegis.openClusterSSHCredentials(ctx, sealed, item.Binding)
			if err != nil || envelope.Binding != item.Binding || envelope.validate() != nil || current(ctx) != nil {
				return clusterSSHPreparationTargetCPU{}, clusterbootstrap.ErrPreparationConfig
			}
			credential, sudo, err := clusterSSHNetworkCredential(envelope)
			envelope = clusterSSHCredentialEnvelope{}
			if err != nil {
				return clusterSSHPreparationTargetCPU{}, clusterbootstrap.ErrPreparationConfig
			}
			defer credential.Destroy()
			defer clear(sudo)
			key := item.Key
			key.PublicKey = bytes.Clone(key.PublicKey)
			client, err := connect(ctx, clusterremote.Target{Address: item.Binding.Address, Port: item.Binding.Port}, key, credential)
			if client != nil {
				defer func() {
					if client.Close() != nil {
						observed, result = clusterSSHPreparationTargetCPU{}, clusterbootstrap.ErrPreparationConfig
					}
				}()
			}
			if err != nil || client == nil || current(ctx) != nil {
				return clusterSSHPreparationTargetCPU{}, clusterbootstrap.ErrPreparationConfig
			}
			var nonce [32]byte
			if _, err := rand.Read(nonce[:]); err != nil {
				return clusterSSHPreparationTargetCPU{}, clusterbootstrap.ErrPreparationConfig
			}
			cpuPlan, _ := clusterbootstrap.PreparationTargetCPUPlanDigest(clusterbootstrap.K3sPins().Version)
			request := clusterremote.PreparationProposal{Version: clusterremote.PreparationProposalVersion, Source: work.Baseline, CPUPlanSHA256: cpuPlan,
				ClusterID: item.Binding.ClusterID, OperationID: work.Authorization.OperationID, OperationAttempt: work.Authorization.Attempt,
				ControllerHolder: work.Authorization.ControllerHolder, PlanSHA256: plan, TargetID: item.Binding.TargetID,
				QualificationGeneration: work.Authorization.Targets[i].QualificationGeneration, Address: item.Binding.Address, Port: item.Binding.Port,
				HostKeyFingerprint: item.Key.Fingerprint, Hostname: item.Report.Hostname, MachineID: item.Report.MachineID,
				BootID: item.Report.BootID, Nonce: hex.EncodeToString(nonce[:])}
			proof, err := client.InspectPreparationProposal(ctx, sudo, request, current)
			if err != nil || proof.Validate(request) != nil || current(ctx) != nil {
				return clusterSSHPreparationTargetCPU{}, clusterbootstrap.ErrPreparationConfig
			}
			if paths != nil {
				filesystem, ok := client.(clusterSSHPreparationFilesystemClient)
				if !ok {
					return clusterSSHPreparationTargetCPU{}, clusterbootstrap.ErrPreparationConfig
				}
				selection := clusterremote.FilesystemRequest{MachineID: item.Report.MachineID, BootID: item.Report.BootID, Paths: slices.Clone(paths), RequirePersistent: true}
				target := clusterremote.Target{Address: item.Binding.Address, Port: item.Binding.Port}
				filesystemKey := item.Key
				filesystemKey.PublicKey = bytes.Clone(filesystemKey.PublicKey)
				started := time.Now()
				value, err := filesystem.InspectFilesystem(ctx, sudo, target, filesystemKey, selection, current)
				selection.Paths = slices.Clone(paths)
				storage, evidenceErr := value.Evidence(started, target, item.Key, selection)
				if err != nil || evidenceErr != nil || current(ctx) != nil {
					return clusterSSHPreparationTargetCPU{}, clusterbootstrap.ErrPreparationConfig
				}
				// A fresh clean proposal closes the same pinned SSH acquisition.
				// Filesystem inspection cannot substitute for clean-host evidence.
				if _, err := rand.Read(nonce[:]); err != nil {
					return clusterSSHPreparationTargetCPU{}, clusterbootstrap.ErrPreparationConfig
				}
				request.Nonce = hex.EncodeToString(nonce[:])
				last, err := client.InspectPreparationProposal(ctx, sudo, request, current)
				if err != nil || last.Validate(request) != nil || last.ObservationSHA256 != proof.ObservationSHA256 || last.CPU != proof.CPU || current(ctx) != nil {
					return clusterSSHPreparationTargetCPU{}, clusterbootstrap.ErrPreparationConfig
				}
				filesystems = append(filesystems, clusterSSHTargetFilesystemEvidence{TargetID: item.Binding.TargetID, Storage: storage})
			}
			return clusterSSHPreparationTargetCPU{TargetID: item.Binding.TargetID, ObservationSHA256: proof.ObservationSHA256, CPU: proof.CPU}, nil
		}
		var first []clusterSSHPreparationTargetCPU
		var firstFilesystems []clusterSSHTargetFilesystemEvidence
		for round := 0; round < 2; round++ {
			filesystems = nil
			values := make([]clusterSSHPreparationTargetCPU, 0, len(work.Proof.Cohort.Targets))
			for i := range work.Proof.Cohort.Targets {
				digest, err := read(i)
				if err != nil || !validClusterSSHPreparationDigest(digest.ObservationSHA256) || current(ctx) != nil {
					return fail()
				}
				values = append(values, digest)
			}
			if round == 0 {
				first = values
				firstFilesystems = cloneClusterSSHPreparationFilesystems(filesystems)
			} else if !slices.Equal(first, values) || !mergeClusterSSHFilesystemAvailability(firstFilesystems, filesystems, false) {
				return fail()
			}
		}
		digest, err := clusterSSHPreparationTargetCPUDigest(clusterSSHPreparationApprovalSnapshot{work: work}, first)
		// Waiting storage approval recalculates every required budget on every
		// read. Retain conservative free-counter minima while requiring the same
		// persistent identity; a harmless decrease is not an authority change.
		if err != nil || current(ctx) != nil || (retained != "" && retained != digest) ||
			retainedFilesystems != nil && !mergeClusterSSHFilesystemAvailability(retainedFilesystems, firstFilesystems, false) {
			return fail()
		}
		retained = digest
		if retainedFilesystems == nil {
			retainedFilesystems = firstFilesystems
		}
		return clusterSSHPreparationCleanObservation{Digest: digest, Targets: slices.Clone(first), Filesystems: cloneClusterSSHPreparationFilesystems(retainedFilesystems)}, nil
	}
}
