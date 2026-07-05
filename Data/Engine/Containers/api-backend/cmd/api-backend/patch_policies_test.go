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

func TestPatchPolicyDecisionMatchesTitleContains(t *testing.T) {
	rules := []patchPolicyRule{
		{RuleType: patchPolicyRuleApprove, MatchType: patchPolicyMatchTitleContains, MatchValue: "Security Intelligence Update"},
		{RuleType: patchPolicyRuleBlock, MatchType: patchPolicyMatchTitleContains, MatchValue: "Preview"},
	}
	defenderPatch := map[string]any{"title": "Security Intelligence Update for Microsoft Defender Antivirus - KB2267602", "state": "pending"}
	if got := patchPolicyDecision(rules, defenderPatch); got != patchPolicyRuleApprove {
		t.Fatalf("expected title_contains defender approval, got %q", got)
	}
	previewPatch := map[string]any{"title": "2022-02 Cumulative Update Preview for .NET Framework", "state": "pending"}
	if got := patchPolicyDecision(rules, previewPatch); got != patchPolicyRuleBlock {
		t.Fatalf("expected title_contains preview block, got %q", got)
	}
	if got := normalizePatchPolicyMatchType("Title Contains"); got != patchPolicyMatchTitleContains {
		t.Fatalf("normalized title contains=%q want %q", got, patchPolicyMatchTitleContains)
	}
}

func TestPatchPolicyLinkedPoliciesIncludeTitleApprovesAndBlocks(t *testing.T) {
	rules := []patchPolicyRule{
		{PolicyID: 11, PolicyName: "Global Workstation Policy", PolicyType: patchPolicyTypeGlobal, RuleType: patchPolicyRuleApprove, MatchType: patchPolicyMatchTitleContains, MatchValue: "Security Intelligence Update"},
		{PolicyID: 11, PolicyName: "Global Workstation Policy", PolicyType: patchPolicyTypeGlobal, RuleType: patchPolicyRuleBlock, MatchType: patchPolicyMatchTitleContains, MatchValue: "Preview"},
		{PolicyID: 22, PolicyName: "Site Workstation Policy", PolicyType: patchPolicyTypeSite, RuleType: patchPolicyRuleApprove, MatchType: "classification", MatchValue: "Drivers"},
	}

	defender := patchPolicyLinkedPoliciesForPatch(rules, map[string]any{"title": "Security Intelligence Update for Microsoft Defender Antivirus - KB2267602"})
	if len(defender) != 1 {
		t.Fatalf("expected one defender linked policy, got %#v", defender)
	}
	if defender[0].PolicyID != 11 || defender[0].PolicyType != patchPolicyTypeGlobal || defender[0].PolicyName != "Global Workstation Policy" {
		t.Fatalf("unexpected defender linked policy %#v", defender[0])
	}
	if len(defender[0].RuleTypes) != 1 || defender[0].RuleTypes[0] != patchPolicyRuleApprove {
		t.Fatalf("expected defender approve rule source, got %#v", defender[0].RuleTypes)
	}

	preview := patchPolicyLinkedPoliciesForPatch(rules, map[string]any{"title": "2026-07 Cumulative Update Preview for Windows 11"})
	if len(preview) != 1 {
		t.Fatalf("expected one preview linked policy, got %#v", preview)
	}
	if preview[0].PolicyID != 11 || len(preview[0].RuleTypes) != 1 || preview[0].RuleTypes[0] != patchPolicyRuleBlock {
		t.Fatalf("expected preview block source from global policy, got %#v", preview[0])
	}
}

func TestPatchPolicyLinkedPoliciesDedupesSamePolicyMatches(t *testing.T) {
	rules := []patchPolicyRule{
		{PolicyID: 11, PolicyName: "Global Workstation Policy", PolicyType: patchPolicyTypeGlobal, RuleType: patchPolicyRuleApprove, MatchType: patchPolicyMatchTitleContains, MatchValue: "Security Intelligence Update"},
		{PolicyID: 11, PolicyName: "Global Workstation Policy", PolicyType: patchPolicyTypeGlobal, RuleType: patchPolicyRuleBlock, MatchType: patchPolicyMatchTitleContains, MatchValue: "Preview"},
	}
	linked := patchPolicyLinkedPoliciesForPatch(rules, map[string]any{"title": "Security Intelligence Update Preview for Microsoft Defender Antivirus"})
	if len(linked) != 1 {
		t.Fatalf("expected same policy to collapse into one linked policy, got %#v", linked)
	}
	if len(linked[0].RuleTypes) != 2 || linked[0].RuleTypes[0] != patchPolicyRuleApprove || linked[0].RuleTypes[1] != patchPolicyRuleBlock {
		t.Fatalf("expected approve and block rule types, got %#v", linked[0].RuleTypes)
	}
}

func TestAttachPatchPolicyInventoryPayloadIncludesLinkedPoliciesWhenNotInstallCandidate(t *testing.T) {
	payload := map[string]any{"inventory_id": int64(42), "state": "pending"}
	index := patchPolicyPendingInventoryIndex{RowsByInventoryID: map[int64]patchPolicyPendingInventoryRow{
		42: {
			patchPolicyInventoryAssignment: patchPolicyInventoryAssignment{
				EffectivePolicyID:    22,
				EffectivePolicyName:  "Site Workstation Policy",
				EffectivePolicyType:  patchPolicyTypeSite,
				EffectiveRoleScope:   patchPolicyRoleWorkstation,
				HierarchyPolicyIDs:   []int64{11, 22},
				HierarchyPolicyNames: []string{"Global Workstation Policy", "Site Workstation Policy"},
			},
			InstallCandidate: false,
			SkipReason:       "not_approved",
			LinkedPolicies: []patchPolicyLinkedPolicy{{
				PolicyID:    11,
				PolicyName:  "Global Workstation Policy",
				PolicyType:  patchPolicyTypeGlobal,
				RuleTypes:   []string{patchPolicyRuleBlock},
				MatchTypes:  []string{patchPolicyMatchTitleContains},
				MatchValues: []string{"Preview"},
			}},
		},
	}}

	attachPatchPolicyInventoryPayload(payload, index)
	if payload["patch_policy_install_candidate"] != false || payload["patch_policy_skip_reason"] != "not_approved" {
		t.Fatalf("unexpected install candidate metadata %#v", payload)
	}
	linked, ok := payload["patch_policy_linked_policies"].([]map[string]any)
	if !ok || len(linked) != 1 {
		t.Fatalf("expected one linked policy payload, got %#v", payload["patch_policy_linked_policies"])
	}
	if linked[0]["policy_name"] != "Global Workstation Policy" || linked[0]["policy_type"] != patchPolicyTypeGlobal {
		t.Fatalf("unexpected linked policy payload %#v", linked[0])
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
	deviceCounts := map[string]int{
		patchPolicyTypeDeviceFilter: 4,
		patchPolicyTypeGlobal:       1,
		patchPolicyTypeSite:         3,
	}
	if got := patchPolicyPendingTotal(counts); got != 27 {
		t.Fatalf("pending total=%d want 27", got)
	}
	payload := patchPolicyPendingBreakdownPayload(counts, deviceCounts)
	if len(payload) != 3 {
		t.Fatalf("expected three payload entries, got %#v", payload)
	}
	expected := []struct {
		policyType string
		label      string
		count      int
		devices    int
	}{
		{patchPolicyTypeGlobal, "Global", 2, 1},
		{patchPolicyTypeSite, "Site", 15, 3},
		{patchPolicyTypeDeviceFilter, "Device Filter", 10, 4},
	}
	for idx, want := range expected {
		if payload[idx]["policy_type"] != want.policyType || payload[idx]["label"] != want.label || payload[idx]["count"] != want.count || payload[idx]["device_count"] != want.devices {
			t.Fatalf("payload[%d]=%#v want type=%q label=%q count=%d devices=%d", idx, payload[idx], want.policyType, want.label, want.count, want.devices)
		}
	}
}

func TestPatchPolicyPendingBreakdownCountsEffectiveAndSourcePolicy(t *testing.T) {
	index := patchPolicyPendingInventoryIndex{}
	patchPolicyAddPendingBreakdownCount(&index, patchPolicyInventoryAssignment{
		EffectivePolicyID:   22,
		EffectivePolicyType: patchPolicyTypeSite,
		HierarchyPolicyIDs:  []int64{11, 22},
	}, patchPolicyPendingInventoryRow{SourcePolicyID: 11, SourcePolicyType: patchPolicyTypeGlobal}, "11111111-1111-1111-1111-111111111111", "kb:KB5000001")
	patchPolicyAddPendingBreakdownCount(&index, patchPolicyInventoryAssignment{
		EffectivePolicyID:   22,
		EffectivePolicyType: patchPolicyTypeSite,
		HierarchyPolicyIDs:  []int64{11, 22},
	}, patchPolicyPendingInventoryRow{SourcePolicyID: 11, SourcePolicyType: patchPolicyTypeGlobal}, "22222222-2222-2222-2222-222222222222", "kb:KB5000001")
	patchPolicyAddPendingBreakdownCount(&index, patchPolicyInventoryAssignment{
		EffectivePolicyID:   22,
		EffectivePolicyType: patchPolicyTypeSite,
		HierarchyPolicyIDs:  []int64{11, 22},
	}, patchPolicyPendingInventoryRow{SourcePolicyID: 22, SourcePolicyType: patchPolicyTypeSite}, "11111111-1111-1111-1111-111111111111", "kb:KB5000002")

	if got := index.BreakdownByPolicyID[11][patchPolicyTypeSite]; got != 0 {
		t.Fatalf("parent policy received child pending count=%d want 0", got)
	}
	if got := index.BreakdownByPolicyID[11][patchPolicyTypeGlobal]; got != 1 {
		t.Fatalf("source policy global-source count=%d want 1", got)
	}
	if got := index.BreakdownByPolicyID[22][patchPolicyTypeGlobal]; got != 1 {
		t.Fatalf("effective policy global-source count=%d want 1", got)
	}
	if got := index.BreakdownByPolicyID[22][patchPolicyTypeSite]; got != 1 {
		t.Fatalf("effective policy site-source count=%d want 1", got)
	}
	if got := index.DeviceCountByPolicyID[22]; got != 2 {
		t.Fatalf("effective policy pending device count=%d want 2", got)
	}
	if got := index.DeviceCountByPolicyIDAndType[22][patchPolicyTypeGlobal]; got != 2 {
		t.Fatalf("effective policy global-source pending device count=%d want 2", got)
	}
	if got := index.DeviceCountByPolicyIDAndType[22][patchPolicyTypeSite]; got != 1 {
		t.Fatalf("effective policy site-source pending device count=%d want 1", got)
	}
	if got := index.DeviceCountByPolicyIDAndType[11][patchPolicyTypeGlobal]; got != 2 {
		t.Fatalf("source policy global-source pending device count=%d want 2", got)
	}
}

func TestPatchPolicyPendingBreakdownPropagatesSourceToEffectiveHierarchy(t *testing.T) {
	index := patchPolicyPendingInventoryIndex{}
	assignment := patchPolicyInventoryAssignment{
		EffectivePolicyID:   33,
		EffectivePolicyType: patchPolicyTypeDeviceFilter,
		HierarchyPolicyIDs:  []int64{11, 22, 33},
	}
	patchPolicyAddPendingBreakdownCount(&index, assignment, patchPolicyPendingInventoryRow{SourcePolicyID: 11, SourcePolicyType: patchPolicyTypeGlobal}, "11111111-1111-1111-1111-111111111111", "kb:KB5000001")
	patchPolicyAddPendingBreakdownCount(&index, assignment, patchPolicyPendingInventoryRow{SourcePolicyID: 11, SourcePolicyType: patchPolicyTypeGlobal}, "22222222-2222-2222-2222-222222222222", "kb:KB5000001")
	patchPolicyAddPendingBreakdownCount(&index, assignment, patchPolicyPendingInventoryRow{SourcePolicyID: 22, SourcePolicyType: patchPolicyTypeSite}, "22222222-2222-2222-2222-222222222222", "kb:KB5000002")

	if got := index.BreakdownByPolicyID[11][patchPolicyTypeGlobal]; got != 1 {
		t.Fatalf("global policy unique global updates=%d want 1", got)
	}
	if got := index.BreakdownByPolicyID[22][patchPolicyTypeGlobal]; got != 1 {
		t.Fatalf("site policy inherited global updates=%d want 1", got)
	}
	if got := index.BreakdownByPolicyID[33][patchPolicyTypeGlobal]; got != 1 {
		t.Fatalf("filter policy inherited global updates=%d want 1", got)
	}
	if got := index.BreakdownByPolicyID[11][patchPolicyTypeSite]; got != 0 {
		t.Fatalf("global policy child site updates=%d want 0", got)
	}
	if got := index.BreakdownByPolicyID[22][patchPolicyTypeSite]; got != 1 {
		t.Fatalf("site policy own updates=%d want 1", got)
	}
	if got := index.BreakdownByPolicyID[33][patchPolicyTypeSite]; got != 1 {
		t.Fatalf("filter policy inherited site updates=%d want 1", got)
	}
	if got := index.DeviceCountByPolicyIDAndType[22][patchPolicyTypeGlobal]; got != 2 {
		t.Fatalf("site policy inherited global devices=%d want 2", got)
	}
	if got := index.DeviceCountByPolicyIDAndType[33][patchPolicyTypeSite]; got != 1 {
		t.Fatalf("filter policy inherited site devices=%d want 1", got)
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
