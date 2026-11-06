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
	wire, wireErr := json.Marshal(snapshot.work)
	credentials := slices.Clone(snapshot.credentials)
	operation := clusterControllerOperation{ID: snapshot.work.Authorization.OperationID, Attempt: snapshot.work.Authorization.Attempt,
		Kind: "ssh_onboarding", State: "waiting", CurrentStep: clusterSSHQualificationStep}
	return func(parent context.Context) error {
		if c == nil || c.store == nil || c.store.db == nil || wireErr != nil || parent.Err() != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		ctx, cancel := context.WithTimeout(parent, 3*time.Second)
		defer cancel()
		current, _, err := c.loadClusterSSHPreparationApproval(ctx, operation)
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
	wire, wireErr := json.Marshal(snapshot.work)
	var work clusterSSHPreparationWork
	decodeErr := json.Unmarshal(wire, &work)
	credentials := slices.Clone(snapshot.credentials)
	plan, planErr := clusterSSHPreparationPlanDigest(work.Authorization)
	valid := wireErr == nil && decodeErr == nil && planErr == nil && len(work.Claims) == 0 && work.Baseline.Validate() == nil &&
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
	return func(parent context.Context, requested string) (string, error) {
		fail := func() (string, error) { failed.Store(true); return "", clusterbootstrap.ErrPreparationConfig }
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
		read := func(i int) (digest string, result error) {
			item, sealed := work.Proof.Cohort.Targets[i], credentials[i]
			if current(ctx) != nil {
				return "", clusterbootstrap.ErrPreparationConfig
			}
			envelope, err := aegis.openClusterSSHCredentials(ctx, sealed, item.Binding)
			if err != nil || envelope.Binding != item.Binding || envelope.validate() != nil || current(ctx) != nil {
				return "", clusterbootstrap.ErrPreparationConfig
			}
			credential, sudo, err := clusterSSHNetworkCredential(envelope)
			envelope = clusterSSHCredentialEnvelope{}
			if err != nil {
				return "", clusterbootstrap.ErrPreparationConfig
			}
			defer credential.Destroy()
			defer clear(sudo)
			key := item.Key
			key.PublicKey = bytes.Clone(key.PublicKey)
			client, err := connect(ctx, clusterremote.Target{Address: item.Binding.Address, Port: item.Binding.Port}, key, credential)
			if client != nil {
				defer func() {
					if client.Close() != nil {
						digest, result = "", clusterbootstrap.ErrPreparationConfig
					}
				}()
			}
			if err != nil || client == nil || current(ctx) != nil {
				return "", clusterbootstrap.ErrPreparationConfig
			}
			var nonce [32]byte
			if _, err := rand.Read(nonce[:]); err != nil {
				return "", clusterbootstrap.ErrPreparationConfig
			}
			request := clusterremote.PreparationProposal{Version: 1, Source: work.Baseline,
				ClusterID: item.Binding.ClusterID, OperationID: work.Authorization.OperationID, OperationAttempt: work.Authorization.Attempt,
				ControllerHolder: work.Authorization.ControllerHolder, PlanSHA256: plan, TargetID: item.Binding.TargetID,
				QualificationGeneration: work.Authorization.Targets[i].QualificationGeneration, Address: item.Binding.Address, Port: item.Binding.Port,
				HostKeyFingerprint: item.Key.Fingerprint, Hostname: item.Report.Hostname, MachineID: item.Report.MachineID,
				BootID: item.Report.BootID, Nonce: hex.EncodeToString(nonce[:])}
			proof, err := client.InspectPreparationProposal(ctx, sudo, request, current)
			if err != nil || proof.Validate(request) != nil || current(ctx) != nil {
				return "", clusterbootstrap.ErrPreparationConfig
			}
			return proof.ObservationSHA256, nil
		}
		var first []string
		for round := 0; round < 2; round++ {
			values := make([]string, 0, len(work.Proof.Cohort.Targets))
			for i := range work.Proof.Cohort.Targets {
				digest, err := read(i)
				if err != nil || !validClusterSSHPreparationDigest(digest) || current(ctx) != nil {
					return fail()
				}
				values = append(values, digest)
			}
			if round == 0 {
				first = values
			} else if !slices.Equal(first, values) {
				return fail()
			}
		}
		digest, err := clusterSSHPreparationDigest(struct {
			Domain, Plan string
			Targets      []string
		}{"preparation-clean-targets-v1", plan, first})
		if err != nil || current(ctx) != nil || (retained != "" && retained != digest) {
			return fail()
		}
		retained = digest
		return digest, nil
	}
}
