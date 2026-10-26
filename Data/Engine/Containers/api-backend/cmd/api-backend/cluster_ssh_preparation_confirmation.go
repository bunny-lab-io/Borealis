package main

import (
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Operator confirmation binds one exact proposed cohort. The authenticated
// caller supplies actor and database time; neither comes from request JSON.
// This contract does not register a public route or establish readiness.
type clusterSSHPreparationConfirmation struct {
	Version     int    `json:"version"`
	RequestID   string `json:"request_id"`
	Actor       string `json:"actor"`
	OperationID string `json:"operation_id"`
	Attempt     int64  `json:"attempt"`
	PlanSHA256  string `json:"plan_sha256"`
	ConfirmedAt int64  `json:"confirmed_at"`
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

func (c clusterSSHPreparationConfirmation) valid(a clusterSSHPreparationAuthorization) bool {
	digest, err := clusterSSHPreparationPlanDigest(a)
	if err != nil || c.Version != 1 || !clusterUUIDRE.MatchString(c.RequestID) || c.OperationID != a.OperationID ||
		c.Attempt != a.Attempt || c.PlanSHA256 != digest || c.ConfirmedAt < 1 || !utf8.ValidString(c.Actor) ||
		len(c.Actor) == 0 || len(c.Actor) > 128 || strings.TrimSpace(c.Actor) != c.Actor {
		return false
	}
	for _, r := range c.Actor {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func newClusterSSHPreparationConfirmation(raw []byte, actor string, now int64, a clusterSSHPreparationAuthorization) (clusterSSHPreparationConfirmation, error) {
	fail := func() (clusterSSHPreparationConfirmation, error) {
		return clusterSSHPreparationConfirmation{}, errClusterConflict
	}
	if len(raw) == 0 || len(raw) > 4096 {
		return fail()
	}
	object, err := decodeClusterSSHObject(raw, map[string]bool{"request_id": true, "plan_sha256": true, "confirmation": true})
	if err != nil || len(object) != 3 {
		return fail()
	}
	request, err := clusterSSHString(object, "request_id", 36, false)
	if err != nil {
		return fail()
	}
	plan, err := clusterSSHString(object, "plan_sha256", 64, false)
	if err != nil {
		return fail()
	}
	phrase, err := clusterSSHString(object, "confirmation", 32, false)
	if err != nil || phrase != "PREPARE ENGINE NODES" {
		return fail()
	}
	c := clusterSSHPreparationConfirmation{Version: 1, RequestID: request, Actor: actor, OperationID: a.OperationID,
		Attempt: a.Attempt, PlanSHA256: plan, ConfirmedAt: now}
	if !c.valid(a) {
		return fail()
	}
	return c, nil
}

func parseClusterSSHPreparationConfirmation(raw []byte, a clusterSSHPreparationAuthorization) (clusterSSHPreparationConfirmation, error) {
	var c clusterSSHPreparationConfirmation
	if len(raw) == 0 || len(raw) > 4096 || json.Unmarshal(raw, &c) != nil || !c.valid(a) {
		return c, errClusterConflict
	}
	canonical, err := json.Marshal(c)
	digest, digestErr := clusterSSHPreparationDigest(c)
	if err != nil || digestErr != nil || !sameClusterSSHStoredJSON(raw, canonical, 0) || digest != a.ConfirmationSHA256 {
		return clusterSSHPreparationConfirmation{}, errClusterConflict
	}
	return c, nil
}
