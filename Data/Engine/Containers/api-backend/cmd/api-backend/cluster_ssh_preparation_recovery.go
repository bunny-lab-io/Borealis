package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"slices"
	"unicode/utf8"
)

const (
	clusterSSHPreparationRecoveryOperationStep = "recover_ssh_preparation"
	clusterSSHPreparationRecoveryReservedStep  = "preparation_recovery_reserved"
	clusterSSHPreparationRecoveryClaimStep     = "reconcile_preparation"
	clusterSSHPreparationRecoveryField         = "ssh_preparation_recovery"
)

// This lineage reserves reconciliation, not mutation permission. Lease expiry
// cannot prove a stopped producer or an absent effect. A future native consumer
// must independently prove both journal custody and actual executor quiescence.
// Original approval, qualification generations and timestamps stay unchanged.
type clusterSSHPreparationRecoveryRecord struct {
	Version          int                                   `json:"version"`
	ID               string                                `json:"id"`
	OperationID      string                                `json:"operation_id"`
	Attempt          int64                                 `json:"attempt"`
	ControllerHolder string                                `json:"controller_holder"`
	PlanSHA256       string                                `json:"plan_sha256"`
	PayloadSHA256    string                                `json:"payload_sha256"`
	Targets          []clusterSSHPreparationRecoveryTarget `json:"targets"`
}

type clusterSSHPreparationRecoveryTarget struct {
	TargetID           string `json:"target_id"`
	PreviousHolder     string `json:"previous_holder"`
	PreviousGeneration int64  `json:"previous_generation"`
	PreviousExpiresAt  int64  `json:"previous_expires_at"`
	Generation         int64  `json:"generation"`
}

type clusterSSHPreparationRecoverySnapshot struct {
	work        clusterSSHPreparationWork
	record      clusterSSHPreparationRecoveryRecord
	credentials []sealedClusterSSHCredentials
	payload     string
	step        string
}

// Separate types deliberately cannot enter ordinary preparation workers,
// settlement, source brokers or dispatch. No production caller is installed.
type clusterSSHPreparationRecoveryTargetClaim struct {
	Lease    clusterSSHTargetLease
	Previous clusterSSHPreparationRecoveryTarget
	Sealed   sealedClusterSSHCredentials
}
type clusterSSHPreparationRecoveryClaim struct {
	Reservation clusterSSHPreparationRecoveryRecord
	Claims      []clusterSSHPreparationRecoveryTargetClaim
	Baseline    clusterbootstrap.Expected
	payload     string
	custody     *clusterSSHPreparationRecoveryCustody
}

func (clusterSSHPreparationRecoverySnapshot) String() string {
	return "SSH preparation recovery [redacted]"
}
func (clusterSSHPreparationRecoverySnapshot) GoString() string {
	return "SSH preparation recovery [redacted]"
}
func (clusterSSHPreparationRecoveryClaim) String() string {
	return "SSH preparation recovery claim [redacted]"
}
func (clusterSSHPreparationRecoveryClaim) GoString() string {
	return "SSH preparation recovery claim [redacted]"
}

// Canonicalize the complete original object, preserving exact JSON numbers and
// rejecting duplicate/aliased recovery fields. Only this one added field is
// excluded from the historical payload digest; no original field is rewritten.
func clusterSSHPreparationRecoveryPayload(payload string) ([]byte, json.RawMessage, error) {
	if len(payload) == 0 || len(payload) > 256<<10 || !utf8.ValidString(payload) {
		return nil, nil, errClusterConflict
	}
	var recovery json.RawMessage
	if clusterBootstrapObject([]byte(payload), map[string]any{clusterSSHPreparationRecoveryField: &recovery}) != nil {
		return nil, nil, errClusterConflict
	}
	d := json.NewDecoder(bytes.NewBufferString(payload))
	d.UseNumber()
	value, err := clusterSSHPrivateJSONValue(d, 0)
	object, ok := value.(map[string]any)
	if err != nil || !ok {
		return nil, nil, errClusterConflict
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, nil, errClusterConflict
	}
	delete(object, clusterSSHPreparationRecoveryField)
	base, err := json.Marshal(object)
	if err != nil {
		return nil, nil, errClusterConflict
	}
	return base, recovery, nil
}

func clusterSSHPreparationRecoveryPayloadDigest(base []byte) string {
	sum := sha256.Sum256(base)
	return hex.EncodeToString(sum[:])
}

func (r clusterSSHPreparationRecoveryRecord) valid(work clusterSSHPreparationWork, base []byte) bool {
	plan, err := clusterSSHPreparationPlanDigest(work.Authorization)
	if err != nil || r.Version != 1 || !clusterUUIDRE.MatchString(r.ID) || r.ID == "00000000-0000-0000-0000-000000000000" ||
		r.OperationID != work.Authorization.OperationID || r.Attempt != work.Authorization.Attempt || r.ControllerHolder != work.Authorization.ControllerHolder ||
		r.PlanSHA256 != plan || r.PayloadSHA256 != clusterSSHPreparationRecoveryPayloadDigest(base) ||
		len(r.Targets) != len(work.Authorization.Targets) || len(r.Targets) < 1 || len(r.Targets) > 2 {
		return false
	}
	var holder string
	for i, prior := range r.Targets {
		approved := work.Authorization.Targets[i]
		if prior.TargetID != approved.TargetID || !clusterUUIDRE.MatchString(prior.PreviousHolder) || prior.PreviousHolder == "00000000-0000-0000-0000-000000000000" ||
			prior.PreviousGeneration != approved.QualificationGeneration+1 || prior.PreviousGeneration < 1 || prior.PreviousGeneration >= 1<<53-1 ||
			prior.Generation != prior.PreviousGeneration+1 || prior.PreviousExpiresAt <= 0 || i > 0 && prior.PreviousHolder != holder {
			return false
		}
		holder = prior.PreviousHolder
	}
	return true
}

func parseClusterSSHPreparationRecovery(payload string, work clusterSSHPreparationWork) (clusterSSHPreparationRecoveryRecord, error) {
	base, raw, err := clusterSSHPreparationRecoveryPayload(payload)
	var record clusterSSHPreparationRecoveryRecord
	if err != nil || len(raw) == 0 || len(raw) > 8<<10 || json.Unmarshal(raw, &record) != nil {
		return record, errClusterConflict
	}
	canonical, err := json.Marshal(record)
	if err != nil || !sameClusterSSHStoredJSON(raw, canonical, 0) || !record.valid(work, base) {
		return clusterSSHPreparationRecoveryRecord{}, errClusterConflict
	}
	record.Targets = slices.Clone(record.Targets)
	return record, nil
}
