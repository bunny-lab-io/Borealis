package main

import "testing"

func TestDeviceFilterSiteModeMatchesSpecificAndExclusions(t *testing.T) {
	specific := map[string]any{
		"site_mode": filterSiteModeSpecific,
		"site_ids":  []any{int64(1), int64(2)},
	}
	if !deviceMatchesSiteMode(specific, map[string]any{"site_id": int64(1)}) {
		t.Fatalf("specific site filter did not match included site")
	}
	if deviceMatchesSiteMode(specific, map[string]any{"site_id": int64(3)}) {
		t.Fatalf("specific site filter matched excluded site")
	}
	if deviceMatchesSiteMode(specific, map[string]any{"site_id": nil}) {
		t.Fatalf("specific site filter matched unassigned device")
	}

	exclusions := map[string]any{
		"site_mode": filterSiteModeExclusions,
		"site_ids":  []any{int64(1), int64(2)},
	}
	if deviceMatchesSiteMode(exclusions, map[string]any{"site_id": int64(1)}) {
		t.Fatalf("global exclusion filter matched excluded site")
	}
	if !deviceMatchesSiteMode(exclusions, map[string]any{"site_id": int64(3)}) {
		t.Fatalf("global exclusion filter did not match non-excluded site")
	}
	if !deviceMatchesSiteMode(exclusions, map[string]any{"site_id": nil}) {
		t.Fatalf("global exclusion filter did not match unassigned device")
	}
}

func TestMatchFilterDevicesAdvancedCriteriaTextSoftwareMetadata(t *testing.T) {
	filter := map[string]any{
		"name":      "LAB filter",
		"site_mode": filterSiteModeGlobal,
		"advanced_criteria": map[string]any{
			"groups": []any{
				map[string]any{
					"conditions": []any{
						map[string]any{"field": "hostname", "operator": "begins_with", "value": "LAB"},
						map[string]any{"field": "installed_software", "operator": "contains", "value": "7-Zip", "version_operator": "newer_than", "version_value": "20.0"},
						map[string]any{"field": "metadata_field", "metadata_field_number": 1, "operator": "equals", "value": "Production"},
					},
				},
			},
		},
	}
	devices := []map[string]any{
		{
			"hostname": "LAB-OPERATOR-01",
			"site_id":  int64(1),
			"software_records": []map[string]any{
				{"name": "7-Zip", "version": "24.09", "source": "local_installed"},
			},
			"metadata_fields": map[string]any{"field_001": "Production"},
		},
		{
			"hostname": "LAB-OLD-01",
			"site_id":  int64(1),
			"software_records": []map[string]any{
				{"name": "7-Zip", "version": "19.0", "source": "local_installed"},
			},
			"metadata_fields": map[string]any{"field_001": "Production"},
		},
		{
			"hostname": "DEV-OPERATOR-01",
			"site_id":  int64(1),
			"software_records": []map[string]any{
				{"name": "7-Zip", "version": "24.09", "source": "local_installed"},
			},
			"metadata_fields": map[string]any{"field_001": "Production"},
		},
	}

	matches := matchFilterDevices(filter, devices)
	if len(matches) != 1 {
		t.Fatalf("expected one matching device, got %d", len(matches))
	}
	if got := cleanText(matches[0]["hostname"]); got != "LAB-OPERATOR-01" {
		t.Fatalf("unexpected matched host %q", got)
	}
}

func TestNormalizeFilterRecordMergesBasicCriteria(t *testing.T) {
	filter := normalizeFilterRecord(map[string]any{
		"name": "Basic filter",
		"basic_criteria": map[string]any{
			"criteria": []any{
				map[string]any{"field": "hostname", "operator": "contains", "value": "DOCS"},
				map[string]any{"field": "operating_system", "operator": "contains", "value": "Windows"},
			},
		},
	}, nil, "operator")

	matches := matchFilterDevices(filter, []map[string]any{
		{"hostname": "LAB-DOCS-01", "operating_system": "Windows 11"},
		{"hostname": "LAB-DOCS-02", "operating_system": "Ubuntu"},
	})
	if len(matches) != 1 {
		t.Fatalf("expected one basic-criteria match, got %d", len(matches))
	}
	if got := cleanText(matches[0]["hostname"]); got != "LAB-DOCS-01" {
		t.Fatalf("unexpected basic-criteria matched host %q", got)
	}
}

func TestValidateFilterRecordRejectsInvalidCriteria(t *testing.T) {
	filter := normalizeFilterRecord(map[string]any{
		"name": "Invalid filter",
		"advanced_criteria": map[string]any{
			"groups": []any{
				map[string]any{
					"conditions": []any{
						map[string]any{"field": "hostname", "operator": "greater_than", "value": "LAB"},
						map[string]any{"field": "hostname", "operator": "contains", "value": "[", "use_regex": true},
					},
				},
			},
		},
	}, nil, "operator")

	errors := validateFilterRecord(filter)
	if len(errors) != 2 {
		t.Fatalf("expected two validation errors, got %d: %v", len(errors), errors)
	}
}

func TestFilterUsageTargetsFitScopeUsesAllowedSiteIDs(t *testing.T) {
	fits, err := filterUsageTargetsFitScope(nil, nil, []any{
		map[string]any{"kind": "filter", "filter_id": 42, "allowed_site_ids": []any{int64(2), int64(3)}},
	}, []int64{1, 2, 3})
	if err != nil {
		t.Fatalf("unexpected scope error: %v", err)
	}
	if !fits {
		t.Fatalf("expected target to fit assigned site scope")
	}

	fits, err = filterUsageTargetsFitScope(nil, nil, []any{
		map[string]any{"kind": "filter", "filter_id": 42, "allowed_site_ids": []any{int64(2), int64(4)}},
	}, []int64{1, 2, 3})
	if err != nil {
		t.Fatalf("unexpected scope error: %v", err)
	}
	if fits {
		t.Fatalf("expected target outside assigned site scope")
	}
}
