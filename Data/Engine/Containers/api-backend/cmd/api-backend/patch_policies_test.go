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
		"role_scope":  "Server",
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

func TestPatchPolicyHostnameExclusionRequiresSite(t *testing.T) {
	_, errText := normalizePatchPolicySaveBody(map[string]any{
		"name":        "Scoped Device Policy",
		"policy_type": "device_filter",
		"role_scope":  "Workstation",
		"targets":     []any{map[string]any{"target_type": "filter", "filter_id": float64(8)}},
		"exclusions":  []any{map[string]any{"exclusion_type": "frozen", "target_type": "device", "hostname": "DUPLICATE-HOST"}},
	}, patchPolicyRow{}, 1783000000, "operator")
	if errText != "Device hostname exclusions require a site." {
		t.Fatalf("expected site validation error, got %q", errText)
	}

	values, errText := normalizePatchPolicySaveBody(map[string]any{
		"name":        "Scoped Device Policy",
		"policy_type": "device_filter",
		"role_scope":  "Workstation",
		"targets":     []any{map[string]any{"target_type": "filter", "filter_id": float64(8)}},
		"exclusions":  []any{map[string]any{"exclusion_type": "frozen", "target_type": "device", "hostname": "DUPLICATE-HOST", "site_id": float64(7)}},
	}, patchPolicyRow{}, 1783000000, "operator")
	if errText != "" {
		t.Fatalf("unexpected validation error: %s", errText)
	}
	if len(values.Exclusions) != 1 || values.Exclusions[0].SiteID != 7 {
		t.Fatalf("expected site-scoped exclusion, got %#v", values.Exclusions)
	}
}

func TestPatchPolicySaveBodyRejectsBothRoleScope(t *testing.T) {
	_, errText := normalizePatchPolicySaveBody(map[string]any{
		"name":        "Mixed Role Policy",
		"policy_type": "site",
		"role_scope":  "Both",
		"site_ids":    []any{float64(4)},
	}, patchPolicyRow{}, 1783000000, "operator")
	if errText != "Patch policies require Server or Workstation role scope." {
		t.Fatalf("expected role validation error, got %q", errText)
	}
}

func TestPatchPolicyRoleMatchingSkipsUntypedDevices(t *testing.T) {
	if patchPolicyRoleMatches(patchPolicyRoleServer, "") {
		t.Fatalf("untyped devices should not match server policy")
	}
	if patchPolicyRoleMatches(patchPolicyRoleWorkstation, "") {
		t.Fatalf("untyped devices should not match workstation policy")
	}
	if !patchPolicyRoleMatches(patchPolicyRoleServer, "Domain Controller Server") {
		t.Fatalf("server device_type should match server policy")
	}
	if !patchPolicyRoleMatches(patchPolicyRoleWorkstation, "Laptop") {
		t.Fatalf("non-empty non-server device_type should match workstation policy")
	}
}

func TestPatchPolicyHostnameExclusionKeysAreSiteAware(t *testing.T) {
	siteSevenKeys := patchPolicyCoverageKeys(nil, []patchPolicyExclusionRef{{
		ExclusionType: patchPolicyExclusionFrozen,
		TargetType:    "device",
		Hostname:      "DUPLICATE-HOST",
		SiteID:        7,
	}})
	if key, ok := patchPolicyTargetOverlapsKeys(siteSevenKeys, "device", "", "duplicate-host", 0, 7); !ok || key != "device-host:7:duplicate-host" {
		t.Fatalf("expected same-site hostname overlap, got key=%q ok=%v", key, ok)
	}
	if key, ok := patchPolicyTargetOverlapsKeys(siteSevenKeys, "device", "", "duplicate-host", 0, 8); ok {
		t.Fatalf("different site hostname should not overlap, got key=%q", key)
	}
	if key, ok := patchPolicyTargetOverlapsKeys(siteSevenKeys, "device", "", "duplicate-host", 0, 0); !ok || key != "device-host:7:duplicate-host" {
		t.Fatalf("global hostname should overlap site-specific coverage, got key=%q ok=%v", key, ok)
	}

	globalKeys := patchPolicyCoverageKeys(nil, []patchPolicyExclusionRef{{
		ExclusionType: patchPolicyExclusionFrozen,
		TargetType:    "device",
		Hostname:      "DUPLICATE-HOST",
	}})
	if key, ok := patchPolicyTargetOverlapsKeys(globalKeys, "device", "", "duplicate-host", 0, 7); !ok || key != "device-host:*:duplicate-host" {
		t.Fatalf("site-specific hostname should overlap global coverage, got key=%q ok=%v", key, ok)
	}
}
