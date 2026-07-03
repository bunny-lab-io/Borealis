package patchmanagement

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
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

func TestPatchInstallRequestNormalizesIdentity(t *testing.T) {
	spec := patchInstallRequest(map[string]any{
		"patch": map[string]any{
			"patch_key": "kb:KB5000001:state:pending",
			"kb":        "5000001",
			"title":     "Security Update",
			"state":     "pending",
			"metadata": map[string]any{
				"update_id":       "11111111-1111-1111-1111-111111111111",
				"revision_number": float64(7),
			},
		},
	})
	if spec == nil {
		t.Fatalf("expected install spec")
	}
	if spec["kb"] != "KB5000001" || spec["update_id"] != "11111111-1111-1111-1111-111111111111" || spec["revision_number"] != int64(7) {
		t.Fatalf("unexpected install spec %#v", spec)
	}
}

func TestParsePatchInstallProgressJSONL(t *testing.T) {
	line := `{"kind":"progress","request_id":"patch-job-1-run-2","phase":"install","percent":42,"current_update_index":0,"current_update_percent":42,"message":"Installing selected update.","captured_at":1783000999}`
	progress := parsePatchInstallProgressLine(line)
	if progress == nil {
		t.Fatalf("expected progress payload")
	}
	if progress["phase"] != "install" || progress["percent"] != int64(42) || progress["captured_at"] != int64(1783000999) {
		t.Fatalf("unexpected progress payload %#v", progress)
	}

	output := strings.Join([]string{
		`{"kind":"progress","phase":"download","percent":100}`,
		`{"kind":"result","ok":true,"status":"completed","result_code":2,"installed_count":1}`,
	}, "\n")
	result := parsePatchInstallResult(output)
	if result == nil || result["ok"] != true || result["result_code"] != float64(2) {
		t.Fatalf("unexpected parsed JSONL result %#v", result)
	}
}

func TestWindowsPatchInstallScriptFallsBackToKBWhenIdentityIsStale(t *testing.T) {
	script := windowsPatchInstallScript(map[string]any{
		"request_id":      "patch-job-243-run-6889",
		"patch_key":       "kb:KB2267602:state:pending",
		"kb":              "KB2267602",
		"title":           "Security Intelligence Update for Microsoft Defender Antivirus - KB2267602",
		"update_id":       "fffd624f-7798-480c-9a2b-a3d947fa358e",
		"revision_number": int64(200),
	})
	if !strings.Contains(script, "function Test-BorealisKBMatch") {
		t.Fatalf("expected explicit KB fallback matcher in installer script")
	}
	if strings.Contains(script, "return $false\n  }\n  $requestedKB") {
		t.Fatalf("installer script still short-circuits on stale WUA identity")
	}
}

func TestPatchInstallProgressPayloadIncludesSchedulerContext(t *testing.T) {
	payload := patchInstallProgressPayload("LAB-OPERATOR-01", map[string]any{
		"request_id":           "patch-job-9-run-12",
		"scheduled_job_id":     int64(9),
		"scheduled_job_run_id": int64(12),
		"kb":                   "KB5000001",
		"title":                "Security Update",
	}, map[string]any{
		"phase":       "download",
		"percent":     int64(37),
		"message":     "Downloading selected update.",
		"captured_at": int64(1783001111),
	})
	if payload["request_id"] != "patch-job-9-run-12" || payload["scheduled_job_id"] != int64(9) || payload["scheduled_job_run_id"] != int64(12) {
		t.Fatalf("scheduler context missing from payload %#v", payload)
	}
	if payload["hostname"] != "LAB-OPERATOR-01" || payload["phase"] != "download" || payload["percent"] != int64(37) {
		t.Fatalf("unexpected progress payload %#v", payload)
	}
}

func TestPatchInstallRequestAcceptedRunsAsync(t *testing.T) {
	manager := New(nil, "LAB-OPERATOR-01", "system")
	manager.supported = true
	manager.unsupportedReason = ""
	manager.publisher = func(context.Context, Snapshot) error { return nil }
	installStarted := make(chan struct{}, 1)
	manager.runner = func(_ context.Context, _ time.Duration, _ string, args ...string) (commandResult, error) {
		return commandResult{Stdout: `[]`}, nil
	}
	manager.installRunner = func(_ context.Context, _ time.Duration, _ map[string]any, _ func(map[string]any)) (patchInstallResult, error) {
		installStarted <- struct{}{}
		return patchInstallResult{Stdout: `{"ok":true,"status":"completed"}`, Parsed: map[string]any{"ok": true, "status": "completed"}}, nil
	}

	response, err := manager.HandleInstallRequest(context.Background(), map[string]any{
		"hostname": "LAB-OPERATOR-01",
		"patch": map[string]any{
			"patch_key": "update:33333333-3333-3333-3333-333333333333:9:state:pending",
			"title":     "Driver update without KB",
			"state":     "pending",
			"metadata": map[string]any{
				"update_id":       "33333333-3333-3333-3333-333333333333",
				"revision_number": 9,
			},
		},
	})
	if err != nil {
		t.Fatalf("install request failed: %v", err)
	}
	payload, _ := response.(map[string]any)
	if payload["status"] != "accepted" || payload["ok"] != true {
		t.Fatalf("unexpected install response %#v", payload)
	}
	select {
	case <-installStarted:
	case <-time.After(2 * time.Second):
		t.Fatalf("install runner was not called")
	}
}

func TestPatchInstallRequestWaitsForCompletion(t *testing.T) {
	manager := New(nil, "LAB-OPERATOR-01", "system")
	manager.supported = true
	manager.unsupportedReason = ""
	manager.publisher = func(context.Context, Snapshot) error { return nil }
	manager.runner = func(_ context.Context, _ time.Duration, _ string, args ...string) (commandResult, error) {
		return commandResult{Stdout: `[]`, ExitCode: 0}, nil
	}
	manager.installRunner = func(_ context.Context, _ time.Duration, _ map[string]any, onProgress func(map[string]any)) (patchInstallResult, error) {
		if onProgress != nil {
			onProgress(map[string]any{"kind": "progress", "phase": "install", "percent": 42, "captured_at": int64(1783000999)})
		}
		return patchInstallResult{
			Stdout:   `{"ok":true,"status":"completed","result_code":2,"reboot_required":false,"installed_count":1}`,
			Stderr:   "",
			ExitCode: 0,
			Parsed:   map[string]any{"ok": true, "status": "completed", "result_code": float64(2), "reboot_required": false, "installed_count": float64(1)},
		}, nil
	}

	response, err := manager.HandleInstallRequest(context.Background(), map[string]any{
		"hostname":            "LAB-OPERATOR-01",
		"wait_for_completion": true,
		"request_id":          "patch-job-1-run-2",
		"patch": map[string]any{
			"patch_key": "kb:KB5000001:state:pending",
			"kb":        "KB5000001",
			"title":     "Security Update",
			"state":     "pending",
		},
	})
	if err != nil {
		t.Fatalf("install request failed: %v", err)
	}
	payload, _ := response.(map[string]any)
	if payload["status"] != "completed" || payload["ok"] != true || payload["request_id"] != "patch-job-1-run-2" {
		t.Fatalf("unexpected install response %#v", payload)
	}
	if payload["result_code"] != float64(2) || payload["installed_count"] != float64(1) {
		t.Fatalf("expected WUA result details in response %#v", payload)
	}
	if manager.Health().Details["install_running"] != "false" {
		t.Fatalf("install running flag was not cleared")
	}
}
