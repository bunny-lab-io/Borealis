package patchmanagement

import (
	"runtime"
	"strings"
	"testing"
)

func TestParseWindowsPatchInventoryPendingWUA(t *testing.T) {
	raw := `[
		{
			"kb":"5000001",
			"title":"2026-07 Cumulative Update for Windows (KB5000001)",
			"state":"not_installed",
			"source":"windows_update",
			"classification":"Security Updates",
			"severity":"Critical",
			"published_at":1783000000,
			"captured_at":1783000100,
			"metadata":{
				"is_downloaded":true,
				"is_mandatory":false,
				"requires_reboot":true,
				"update_id":"11111111-1111-1111-1111-111111111111",
				"revision_number":4
			}
		}
	]`
	rows, err := parseWindowsPatchInventory(raw)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one row, got %d", len(rows))
	}
	row := rows[0]
	if row.KB != "KB5000001" || row.State != "pending" || row.Source != "wua_pending" {
		t.Fatalf("unexpected normalized row: %#v", row)
	}
	if row.PatchKey != "kb:KB5000001:state:pending" {
		t.Fatalf("unexpected patch key %q", row.PatchKey)
	}
	if row.Metadata["is_downloaded"] != true || row.Metadata["requires_reboot"] != true {
		t.Fatalf("pending metadata not preserved: %#v", row.Metadata)
	}
}

func TestParseWindowsPatchInventoryInstalledHotFix(t *testing.T) {
	raw := `{"hotfix_id":"KB6000001","title":"KB6000001 Security Update","state":"installed","source":"quickfixengineering","installed_on":1783000200,"captured_at":1783000300}`
	rows, err := parseWindowsPatchInventory(raw)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one row, got %d", len(rows))
	}
	row := rows[0]
	if row.KB != "KB6000001" || row.State != "installed" || row.Source != "quick_fix_engineering" {
		t.Fatalf("unexpected installed row: %#v", row)
	}
	if row.InstalledOn != 1783000200 {
		t.Fatalf("installed_on not preserved: %d", row.InstalledOn)
	}
}

func TestParseWindowsPatchInventoryDedupeInstalledHistory(t *testing.T) {
	raw := `[
		{"kb":"KB7000001","title":"Security Update for Windows (KB7000001)","state":"installed","source":"wua_history","installed_on":1783000200,"captured_at":1783000300,"metadata":{"update_id":"22222222-2222-2222-2222-222222222222"}},
		{"kb":"7000001","title":"KB7000001 Security Update","state":"installed","source":"quick_fix_engineering","installed_on":1783000400,"captured_at":1783000500}
	]`
	rows, err := parseWindowsPatchInventory(raw)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected duplicate KB rows to merge, got %d: %#v", len(rows), rows)
	}
	row := rows[0]
	if row.Source != "quick_fix_engineering" {
		t.Fatalf("expected quick fix source to win installed duplicate, got %q", row.Source)
	}
	sources, _ := row.Metadata["sources"].([]string)
	if len(sources) != 2 {
		t.Fatalf("expected merged source list, got %#v", row.Metadata)
	}
}

func TestParseWindowsPatchInventoryNoKBFallsBackToUpdateIdentity(t *testing.T) {
	raw := `[
		{"title":"Driver update without KB","state":"pending","source":"wua_pending","metadata":{"update_id":"33333333-3333-3333-3333-333333333333","revision_number":9}},
		{"title":"Driver update without KB","state":"pending","source":"wua_pending","metadata":{"update_id":"33333333-3333-3333-3333-333333333333","revision_number":9}}
	]`
	rows, err := parseWindowsPatchInventory(raw)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected update identity fallback to dedupe, got %d", len(rows))
	}
	if !strings.HasPrefix(rows[0].PatchKey, "update:33333333-3333-3333-3333-333333333333:9:state:pending") {
		t.Fatalf("unexpected fallback key %q", rows[0].PatchKey)
	}
}

func TestPatchManagementUnsupportedHealth(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unsupported health only applies to non-Windows runtimes")
	}
	manager := New(nil, "linux-host", "system")
	health := manager.Health()
	if health.Status != "unsupported" || health.StatusCode != "unsupported" {
		t.Fatalf("expected unsupported health on non-Windows test runtime, got %#v", health)
	}
}
