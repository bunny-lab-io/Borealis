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

func TestPatchPolicyEffectiveRulesInheritParentApprovalWithSource(t *testing.T) {
	global := patchPolicyRow{
		ID:         sql.NullInt64{Int64: 11, Valid: true},
		Name:       sql.NullString{String: "Global Server Policy", Valid: true},
		PolicyType: sql.NullString{String: patchPolicyTypeGlobal, Valid: true},
		RoleScope:  sql.NullString{String: patchPolicyRoleServer, Valid: true},
		Rules: []patchPolicyRule{{
			RuleType:   patchPolicyRuleApprove,
			MatchType:  "classification",
			MatchValue: "Security Updates",
		}},
	}
	site := patchPolicyRow{
		ID:         sql.NullInt64{Int64: 22, Valid: true},
		Name:       sql.NullString{String: "Bunny Lab Servers", Valid: true},
		PolicyType: sql.NullString{String: patchPolicyTypeSite, Valid: true},
		RoleScope:  sql.NullString{String: patchPolicyRoleServer, Valid: true},
	}
	decision := patchPolicyDecisionWithSource(
		patchPolicyEffectiveRules([]patchPolicyRow{global, site}),
		map[string]any{"classification": "Security Updates", "severity": "Unspecified"},
	)
	if decision.Decision != patchPolicyRuleApprove {
		t.Fatalf("expected inherited global approval, got %#v", decision)
	}
	if decision.PolicyID != 11 || decision.PolicyType != patchPolicyTypeGlobal || decision.PolicyName != "Global Server Policy" {
		t.Fatalf("expected global policy source, got %#v", decision)
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
	if !patchPolicyRoleMatches(patchPolicyRoleServer, "Windows Server") {
		t.Fatalf("server device_type should match server policy")
	}
	if !patchPolicyRoleMatches(patchPolicyRoleWorkstation, "Laptop") {
		t.Fatalf("non-empty non-server device_type should match workstation policy")
	}
	if !patchPolicyRoleMatches(patchPolicyRoleServer, "Domain Controller", "Microsoft Windows Server 2022") {
		t.Fatalf("Windows Server operating_system should match server policy without creating a new device type")
	}
	if patchPolicyRoleMatches(patchPolicyRoleWorkstation, "Domain Controller", "Microsoft Windows Server 2022") {
		t.Fatalf("Windows Server operating_system should not match workstation policy")
	}
}

func TestPatchPolicyPendingBreakdownPayloadOrdersLabelsAndTotals(t *testing.T) {
	counts := map[string]int{
		patchPolicyTypeDeviceFilter: 10,
		patchPolicyTypeGlobal:       2,
		patchPolicyTypeSite:         15,
	}
	if got := patchPolicyPendingTotal(counts); got != 27 {
		t.Fatalf("pending total=%d want 27", got)
	}
	payload := patchPolicyPendingBreakdownPayload(counts)
	if len(payload) != 3 {
		t.Fatalf("expected three payload entries, got %#v", payload)
	}
	expected := []struct {
		policyType string
		label      string
		count      int
	}{
		{patchPolicyTypeGlobal, "Global", 2},
		{patchPolicyTypeSite, "Site-Level Override", 15},
		{patchPolicyTypeDeviceFilter, "Device Filter", 10},
	}
	for idx, want := range expected {
		if payload[idx]["policy_type"] != want.policyType || payload[idx]["label"] != want.label || payload[idx]["count"] != want.count {
			t.Fatalf("payload[%d]=%#v want type=%q label=%q count=%d", idx, payload[idx], want.policyType, want.label, want.count)
		}
	}
}

func TestPatchPolicyPendingBreakdownCountsEffectivePolicyOnly(t *testing.T) {
	index := patchPolicyPendingInventoryIndex{}
	patchPolicyAddPendingBreakdownCount(&index, patchPolicyInventoryAssignment{
		EffectivePolicyID:   22,
		EffectivePolicyType: patchPolicyTypeSite,
		HierarchyPolicyIDs:  []int64{11, 22},
	}, patchPolicyPendingInventoryRow{SourcePolicyType: patchPolicyTypeGlobal}, "11111111-1111-1111-1111-111111111111")
	patchPolicyAddPendingBreakdownCount(&index, patchPolicyInventoryAssignment{
		EffectivePolicyID:   22,
		EffectivePolicyType: patchPolicyTypeSite,
		HierarchyPolicyIDs:  []int64{11, 22},
	}, patchPolicyPendingInventoryRow{SourcePolicyType: patchPolicyTypeSite}, "11111111-1111-1111-1111-111111111111")

	if got := index.BreakdownByPolicyID[11][patchPolicyTypeSite]; got != 0 {
		t.Fatalf("parent policy received child pending count=%d want 0", got)
	}
	if got := index.BreakdownByPolicyID[22][patchPolicyTypeGlobal]; got != 1 {
		t.Fatalf("effective policy global-source count=%d want 1", got)
	}
	if got := index.BreakdownByPolicyID[22][patchPolicyTypeSite]; got != 1 {
		t.Fatalf("effective policy site-source count=%d want 1", got)
	}
	if got := index.DeviceCountByPolicyID[22]; got != 1 {
		t.Fatalf("effective policy pending device count=%d want 1", got)
	}
}

func TestPatchPolicyScheduledTargetReadsNestedTargetJSON(t *testing.T) {
	filterTarget := patchPolicyScheduledTarget(map[string]any{
		"target_type": "filter",
		"target":      map[string]any{"filter_id": float64(42), "name": "Domain Controllers"},
	})
	if filterTarget["kind"] != "filter" || coerceInt64(filterTarget["filter_id"]) != 42 {
		t.Fatalf("nested filter target=%#v want filter_id=42", filterTarget)
	}

	deviceTarget := patchPolicyScheduledTarget(map[string]any{
		"target": map[string]any{"hostname": "DC-01", "site_id": float64(7), "site_name": "DEEPLAB"},
	})
	if deviceTarget["kind"] != "device" || cleanText(deviceTarget["hostname"]) != "DC-01" || coerceInt64(deviceTarget["site_id"]) != 7 {
		t.Fatalf("nested device target=%#v want DC-01 site 7", deviceTarget)
	}
}

func TestPatchPolicyDeviceFilterTargetSitesUseEligibleDevices(t *testing.T) {
	row := patchPolicyRow{PolicyType: sql.NullString{String: patchPolicyTypeDeviceFilter, Valid: true}}
	sites := patchPolicyTargetSitesForRow(row, patchPolicyDeviceResolution{Eligible: []patchPolicyDevice{
		{Hostname: "DC-01", SiteID: 7, SiteName: "DEEPLAB", DeviceType: "Domain Controller", OperatingSystem: "Microsoft Windows Server 2022"},
		{Hostname: "DC-02", SiteID: 8, SiteName: "Bunny Lab", DeviceType: "Domain Controller", OperatingSystem: "Microsoft Windows Server 2022"},
		{Hostname: "DC-03", SiteID: 7, SiteName: "DEEPLAB", DeviceType: "Domain Controller", OperatingSystem: "Microsoft Windows Server 2022"},
	}})
	if len(sites) != 2 {
		t.Fatalf("expected two target sites, got %#v", sites)
	}
	if sites[0]["name"] != "Bunny Lab" || sites[1]["name"] != "DEEPLAB" {
		t.Fatalf("unexpected target site order/content: %#v", sites)
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
