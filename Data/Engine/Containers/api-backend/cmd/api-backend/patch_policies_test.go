package main

import (
	"database/sql"
	"testing"
)

func TestPatchPolicyRoleScopeOverlap(t *testing.T) {
	cases := []struct {
		left  string
		right string
		want  bool
	}{
		{patchPolicyRoleBoth, patchPolicyRoleServer, true},
		{patchPolicyRoleBoth, patchPolicyRoleWorkstation, true},
		{patchPolicyRoleServer, patchPolicyRoleServer, true},
		{patchPolicyRoleServer, patchPolicyRoleWorkstation, false},
		{patchPolicyRoleWorkstation, patchPolicyRoleServer, false},
	}
	for _, tc := range cases {
		if got := patchPolicyRoleScopesOverlap(tc.left, tc.right); got != tc.want {
			t.Fatalf("overlap(%q,%q)=%v want %v", tc.left, tc.right, got, tc.want)
		}
	}
}

func TestPatchPolicyDecisionRequiresOverrideForParentBlock(t *testing.T) {
	patch := map[string]any{"classification": "Drivers", "kb": "KB5000001", "state": "pending"}
	rules := []patchPolicyRule{
		{RuleType: patchPolicyRuleBlock, MatchType: "classification", MatchValue: "Drivers"},
		{RuleType: patchPolicyRuleApprove, MatchType: "kb", MatchValue: "KB5000001"},
	}
	if got := patchPolicyDecision(rules, patch); got != patchPolicyRuleBlock {
		t.Fatalf("expected parent block to win without override, got %q", got)
	}
	rules[1].OverrideParentBlock = true
	if got := patchPolicyDecision(rules, patch); got != patchPolicyRuleApprove {
		t.Fatalf("expected explicit override approval, got %q", got)
	}
}

func TestPatchPolicyDeferralUsesPublishedThenFirstSeenFallback(t *testing.T) {
	now := int64(1783000000)
	published := patchInventoryRow{
		PublishedAt: sql.NullInt64{Int64: now - 13*86400, Valid: true},
		CapturedAt:  sql.NullInt64{Int64: now - 30*86400, Valid: true},
	}
	if patchPolicyDeferralSatisfied(published, 14, now) {
		t.Fatalf("published_at should drive deferral when present")
	}
	fallback := patchInventoryRow{
		CapturedAt: sql.NullInt64{Int64: now - 15*86400, Valid: true},
	}
	if !patchPolicyDeferralSatisfied(fallback, 14, now) {
		t.Fatalf("captured/first-seen fallback should satisfy deferral")
	}
}

func TestPatchPolicySaveBodyRequiresSitesForSitePolicy(t *testing.T) {
	_, errText := normalizePatchPolicySaveBody(map[string]any{
		"name":        "Servers",
		"policy_type": "site",
	}, patchPolicyRow{}, 1783000000, "operator")
	if errText == "" {
		t.Fatalf("expected site policy without sites to fail validation")
	}
	values, errText := normalizePatchPolicySaveBody(map[string]any{
		"name":        "Servers",
		"policy_type": "site",
		"site_ids":    []any{float64(4)},
		"role_scope":  "Server",
	}, patchPolicyRow{}, 1783000000, "operator")
	if errText != "" {
		t.Fatalf("unexpected validation error: %s", errText)
	}
	if values.RoleScope != patchPolicyRoleServer || len(values.SiteIDs) != 1 || values.SiteIDs[0] != 4 {
		t.Fatalf("unexpected normalized values %#v", values)
	}
}
