package main

import (
	"borealis/api-backend/internal/engineidentity"
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Operator confirmation binds one exact proposed cohort. The authenticated
// caller supplies actor and database time; neither comes from request JSON.
// The Admin API records this intent; it does not establish readiness.
type clusterSSHPreparationConfirmation struct {
	Version        int                    `json:"version"`
	RequestID      string                 `json:"request_id"`
	Actor          string                 `json:"actor"`
	OperationID    string                 `json:"operation_id"`
	Attempt        int64                  `json:"attempt"`
	PlanSHA256     string                 `json:"plan_sha256"`
	ConfirmedAt    int64                  `json:"confirmed_at"`
	IdentitySource engineidentity.Binding `json:"identity_source"`
}

func validClusterSSHPreparationDigest(value string) bool {
	return clusterSSHPreparationResultDigestRE.MatchString(value) && value != strings.Repeat("0", 64)
}

func clusterSSHPreparationPlanDigest(a clusterSSHPreparationAuthorization) (string, error) {
	if a.Version != 1 || !clusterUUIDRE.MatchString(a.OperationID) || a.Attempt < 1 ||
		a.ControllerHolder == "" || len(a.ControllerHolder) > 1024 || len(a.Targets) < 1 || len(a.Targets) > 2 {
		return "", errClusterConflict
	}
	for _, value := range []string{a.InspectionSHA256, a.QualificationSHA256, a.SourceSHA256} {
		if !validClusterSSHPreparationDigest(value) {
			return "", errClusterConflict
		}
	}
	seen := map[string]bool{}
	for _, target := range a.Targets {
		if !clusterUUIDRE.MatchString(target.TargetID) || seen[target.TargetID] || target.QualificationGeneration < 2 ||
			target.QualificationGeneration == 1<<63-1 || !validClusterSSHPreparationDigest(target.CredentialSHA256) {
			return "", errClusterConflict
		}
		seen[target.TargetID] = true
	}
	// Exclude only the digest of the confirmation which will bind this plan.
	a.ConfirmationSHA256 = ""
	return clusterSSHPreparationDigest(a)
}

func (c clusterSSHPreparationConfirmation) valid(a clusterSSHPreparationAuthorization, source clusterSSHSourceCohort) bool {
	digest, err := clusterSSHPreparationPlanDigest(a)
	selected, sourceErr := clusterSSHPreparationIdentitySource(source, c.IdentitySource.SourceID)
	if err != nil || sourceErr != nil || selected != c.IdentitySource || c.Version != 2 || !clusterUUIDRE.MatchString(c.RequestID) || c.OperationID != a.OperationID ||
		c.Attempt != a.Attempt || c.PlanSHA256 != digest || c.ConfirmedAt < 1 || !utf8.ValidString(c.Actor) ||
		len(c.Actor) == 0 || len(c.Actor) > 128 || strings.TrimSpace(c.Actor) != c.Actor {
		return false
	}
	for _, target := range a.Targets {
		if target.TargetID == selected.SourceID {
			return false
		}
	}
	for _, r := range c.Actor {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func clusterSSHPreparationIdentitySource(source clusterSSHSourceCohort, id string) (engineidentity.Binding, error) {
	invalid := engineidentity.Binding{}
	if len(source.Members) < 1 || len(source.Members) > 2 || !clusterUUIDRE.MatchString(id) || id == "00000000-0000-0000-0000-000000000000" {
		return invalid, errClusterConflict
	}
	var selected engineidentity.Binding
	for _, member := range source.Members {
		if member.NodeID != id {
			continue
		}
		if selected != invalid {
			return invalid, errClusterConflict
		}
		selected = engineidentity.Binding{ClusterID: source.ClusterID, SourceID: member.NodeID, SourceUID: member.NodeUID}
	}
	for _, value := range []string{selected.ClusterID, selected.SourceID, selected.SourceUID} {
		if !clusterUUIDRE.MatchString(value) || value == "00000000-0000-0000-0000-000000000000" {
			return invalid, errClusterConflict
		}
	}
	return selected, nil
}

func newClusterSSHPreparationConfirmation(raw []byte, actor string, now int64, a clusterSSHPreparationAuthorization, source clusterSSHSourceCohort) (clusterSSHPreparationConfirmation, error) {
	request, plan, sourceID, err := parseClusterSSHPreparationRequestBody(raw)
	if err != nil {
		return clusterSSHPreparationConfirmation{}, errClusterConflict
	}
	selected, err := clusterSSHPreparationIdentitySource(source, sourceID)
	if err != nil {
		return clusterSSHPreparationConfirmation{}, errClusterConflict
	}
	c := clusterSSHPreparationConfirmation{Version: 2, RequestID: request, Actor: actor, OperationID: a.OperationID,
		Attempt: a.Attempt, PlanSHA256: plan, ConfirmedAt: now, IdentitySource: selected}
	if !c.valid(a, source) {
		return clusterSSHPreparationConfirmation{}, errClusterConflict
	}
	return c, nil
}

func parseClusterSSHPreparationRequestBody(raw []byte) (string, string, string, error) {
	fail := func() (string, string, string, error) {
		return "", "", "", errClusterConflict
	}
	if len(raw) == 0 || len(raw) > 4096 || !utf8.Valid(raw) {
		return fail()
	}
	object, err := decodeClusterSSHObject(raw, map[string]bool{"request_id": true, "plan_sha256": true, "confirmation": true, "source_id": true})
	if err != nil || len(object) != 4 {
		return fail()
	}
	request, err := clusterSSHString(object, "request_id", 36, false)
	if err != nil || !clusterUUIDRE.MatchString(request) {
		return fail()
	}
	sourceID, err := clusterSSHString(object, "source_id", 36, false)
	if err != nil || !clusterUUIDRE.MatchString(sourceID) || sourceID == "00000000-0000-0000-0000-000000000000" {
		return fail()
	}
	plan, err := clusterSSHString(object, "plan_sha256", 64, false)
	if err != nil || !validClusterSSHPreparationDigest(plan) {
		return fail()
	}
	phrase, err := clusterSSHString(object, "confirmation", 32, false)
	if err != nil || phrase != "PREPARE ENGINE NODES" {
		return fail()
	}
	return request, plan, sourceID, nil
}

func parseClusterSSHPreparationConfirmation(raw []byte, a clusterSSHPreparationAuthorization, source clusterSSHSourceCohort) (clusterSSHPreparationConfirmation, error) {
	c, err := parseClusterSSHPreparationRequest(raw, a, source)
	digest, digestErr := clusterSSHPreparationDigest(c)
	if err != nil || digestErr != nil || digest != a.ConfirmationSHA256 {
		return clusterSSHPreparationConfirmation{}, errClusterConflict
	}
	return c, nil
}

// An authenticated request records intent for this plan, not phase readiness.
// Approval separately binds its digest into durable mutation authorization.
func parseClusterSSHPreparationRequest(raw []byte, a clusterSSHPreparationAuthorization, source clusterSSHSourceCohort) (clusterSSHPreparationConfirmation, error) {
	var c clusterSSHPreparationConfirmation
	if len(raw) == 0 || len(raw) > 4096 || json.Unmarshal(raw, &c) != nil || !c.valid(a, source) {
		return c, errClusterConflict
	}
	canonical, err := json.Marshal(c)
	if err != nil || !sameClusterSSHStoredJSON(raw, canonical, 0) {
		return clusterSSHPreparationConfirmation{}, errClusterConflict
	}
	return c, nil
}

// Completion/recovery must retain the source selected before mutation. A
// preparation receipt alone is not permission to pick another active member.
func clusterSSHPreparationRecordedSource(payload string, proof clusterSSHParentProof, confirmationSHA string) (engineidentity.Binding, error) {
	invalid := engineidentity.Binding{}
	var authorizationRaw, confirmationRaw json.RawMessage
	if len(payload) == 0 || len(payload) > 256<<10 || clusterBootstrapObject([]byte(payload), map[string]any{
		"ssh_preparation_authorization": &authorizationRaw, "ssh_preparation_confirmation": &confirmationRaw,
	}) != nil || len(authorizationRaw) == 0 || len(authorizationRaw) > 8<<10 {
		return invalid, errClusterConflict
	}
	var a clusterSSHPreparationAuthorization
	if json.Unmarshal(authorizationRaw, &a) != nil {
		return invalid, errClusterConflict
	}
	canonical, err := json.Marshal(a)
	inspection, inspectionErr := clusterSSHPreparationDigest(proof)
	if err != nil || inspectionErr != nil || !sameClusterSSHStoredJSON(authorizationRaw, canonical, 0) || a.InspectionSHA256 != inspection ||
		a.OperationID != proof.Cohort.OperationID || a.Attempt != proof.Cohort.Attempt || a.ControllerHolder != proof.Cohort.ControllerHolder || a.ConfirmationSHA256 != confirmationSHA {
		return invalid, errClusterConflict
	}
	c, err := parseClusterSSHPreparationConfirmation(confirmationRaw, a, proof.Source)
	if err != nil {
		return invalid, errClusterConflict
	}
	return c.IdentitySource, nil
}
