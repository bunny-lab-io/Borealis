package main

import (
	"borealis/api-backend/internal/clusterremote"
	"context"
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"
)

func validClusterExistingIdentityActor(actor string) bool {
	return utf8.ValidString(actor) && len(actor) > 0 && len(actor) <= 128 && strings.TrimSpace(actor) == actor && strings.IndexFunc(actor, unicode.IsControl) < 0
}

// Canonical persisted plans are independent of clean-host preparation receipts.
// No private material or caller-selected executable paths belong in this type.
func (p clusterExistingIdentityPlan) valid() bool {
	for _, id := range []string{p.OperationID, p.SourceID, p.TargetID, p.MaintenanceID, p.ClusterID, p.KubeSystemUID} {
		if !clusterUUIDRE.MatchString(id) || id == "00000000-0000-0000-0000-000000000000" {
			return false
		}
	}
	if p.Version != 1 || p.SourceID == p.TargetID || p.MaintenanceAttempt < 1 || p.ObservedAt < 1 || len(p.Controller) == 0 || len(p.Controller) > 1024 ||
		!utf8.ValidString(p.Controller) || p.Baseline.Validate() != nil || p.Baseline.Repository != clusterGitHubRepo() || !validClusterSSHPreparationDigest(p.ConfigSHA256) || !validClusterSSHPreparationDigest(p.SnapshotSHA256) || len(p.Members) != 3 {
		return false
	}
	seen := map[string]bool{}
	previous := ""
	for _, m := range p.Members {
		if !clusterUUIDRE.MatchString(m.NodeID) || !clusterUUIDRE.MatchString(m.NodeUID) || !clusterUUIDRE.MatchString(m.BootID) || !clusterSSHMachineID.MatchString(m.MachineID) ||
			m.NodeID <= previous || len(validateClusterNodeName("node", m.Name)) != 0 || (clusterremote.Target{Address: m.Address, Port: 22}).Validate() != nil {
			return false
		}
		for _, value := range []string{"id:" + m.NodeID, "uid:" + m.NodeUID, "boot:" + m.BootID, "machine:" + m.MachineID, "name:" + m.Name, "address:" + m.Address} {
			if seen[value] || strings.HasSuffix(value, ":00000000-0000-0000-0000-000000000000") || strings.HasSuffix(value, ":00000000000000000000000000000000") {
				return false
			}
			seen[value] = true
		}
		previous = m.NodeID
	}
	return seen["id:"+p.SourceID] && seen["id:"+p.TargetID]
}

func parseClusterExistingIdentityPlan(raw []byte) (clusterExistingIdentityPlan, error) {
	var p clusterExistingIdentityPlan
	if len(raw) == 0 || len(raw) > 64<<10 || !utf8.Valid(raw) || json.Unmarshal(raw, &p) != nil || !p.valid() {
		return clusterExistingIdentityPlan{}, errClusterConflict
	}
	canonical, err := json.Marshal(p)
	if err != nil || !sameClusterSSHStoredJSON(raw, canonical, 0) {
		return clusterExistingIdentityPlan{}, errClusterConflict
	}
	return p, nil
}

func (s *postgresOperatorStore) clusterExistingIdentityController(ctx context.Context) (string, error) {
	var owner string
	if s == nil || s.db == nil || s.db.QueryRowContext(ctx, `SELECT holder FROM engine.cluster_application_leases
 WHERE name=$1 AND expires_at>extract(epoch FROM clock_timestamp())`, clusterControllerLeaseName).Scan(&owner) != nil || owner == "" || len(owner) > 1024 {
		return "", errClusterUnavailable
	}
	return owner, nil
}

// Readback recovers a lost reservation acknowledgement without replaying POST.
// This is historical projection, not renewed permission to execute or confirm.
func (s *postgresOperatorStore) clusterExistingIdentityPlan(ctx context.Context, target, id string) (clusterExistingIdentityPlan, string, error) {
	var payload, state, step string
	if s == nil || s.db == nil || !clusterUUIDRE.MatchString(target) || !clusterUUIDRE.MatchString(id) ||
		s.db.QueryRowContext(ctx, `SELECT payload_json,state,current_step FROM engine.cluster_operations
 WHERE id=$1 AND target_node_id=$2 AND kind=$3 AND octet_length(payload_json)<=65536`, id, target, clusterExistingIdentityKind).Scan(&payload, &state, &step) != nil {
		return clusterExistingIdentityPlan{}, "", errClusterConflict
	}
	p, err := parseClusterExistingIdentityPlan([]byte(payload))
	if err != nil || p.OperationID != id || p.TargetID != target || !((state == "waiting" && step == clusterExistingIdentityReserved) || (state == "cancelled" && step == "cancelled")) {
		return clusterExistingIdentityPlan{}, "", errClusterConflict
	}
	return p, state, nil
}
