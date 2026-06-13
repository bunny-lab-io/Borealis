package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSchedulerManagerComputeNextRunMatchesPythonIntervals(t *testing.T) {
	start := int64(1700000042)
	last := int64(1700000105)
	now := int64(1700000400)

	if got := schedulerComputeNextRun("immediately", &start, nil, now); got == nil || *got != schedulerFloorMinute(now) {
		t.Fatalf("immediate first run mismatch: %v", got)
	}
	if got := schedulerComputeNextRun("immediately", &start, &last, now); got != nil {
		t.Fatalf("immediate repeated unexpectedly: %v", *got)
	}
	if got := schedulerComputeNextRun("once", &start, nil, now); got == nil || *got != schedulerFloorMinute(start) {
		t.Fatalf("once run mismatch: %v", got)
	}
	if got := schedulerComputeNextRun("every_15_minutes", &start, &last, now); got == nil || *got != schedulerFloorMinute(last)+15*60 {
		t.Fatalf("interval run mismatch: %v", got)
	}
}

func TestSchedulerManagerOnboardingSiteID(t *testing.T) {
	siteID := schedulerOnboardingSiteID([]any{
		map[string]any{"kind": "device", "hostname": "ignored"},
		map[string]any{"kind": "onboarding_scope", "site_id": float64(42)},
	})
	if siteID != 42 {
		t.Fatalf("expected site id 42, got %d", siteID)
	}
}

func TestSchedulerManagerOnboardingScopeEntries(t *testing.T) {
	entries := schedulerOnboardingScopeEntries([]any{
		map[string]any{"kind": "onboarding_scope", "entries": "10.0.0.10, lab-host\n10.0.0.10"},
		map[string]any{"type": "onboarding_scope", "scope": []any{"win-host:5985", "192.168.10.0/30"}},
		map[string]any{"kind": "onboarding_scope", "targets": []string{"[2001:db8::1]:2222"}},
	})
	expected := []string{"10.0.0.10", "lab-host", "win-host:5985", "192.168.10.0/30", "[2001:db8::1]:2222"}
	if len(entries) != len(expected) {
		t.Fatalf("unexpected entries %#v", entries)
	}
	for i := range expected {
		if entries[i] != expected[i] {
			t.Fatalf("entry %d expected %q got %q", i, expected[i], entries[i])
		}
	}
	host, port := schedulerOnboardingEntryEndpoint("win-host:5985", 22)
	if host != "win-host" || port != 5985 {
		t.Fatalf("unexpected endpoint host=%q port=%d", host, port)
	}
	host, port = schedulerOnboardingEntryEndpoint("[2001:db8::1]:2222", 22)
	if host != "2001:db8::1" || port != 2222 {
		t.Fatalf("unexpected IPv6 endpoint host=%q port=%d", host, port)
	}
}

func TestSchedulerManagerExpirationParser(t *testing.T) {
	cases := map[string]int64{"30m": 1800, "1h": 3600, "2d": 172800, "45": 2700}
	for input, expected := range cases {
		got := schedulerParseExpiration(input)
		if got == nil || *got != expected {
			t.Fatalf("expiration %s expected %d got %v", input, expected, got)
		}
	}
	if got := schedulerParseExpiration("no_expire"); got != nil {
		t.Fatalf("no_expire should be nil, got %d", *got)
	}
}

func TestSchedulerManagerRouteYAMLIncludesRemoteDesktop(t *testing.T) {
	route := schedulerBuildRoute("worker-1", "site-worker-worker-1", 7, 56001, schedulerWorkerRouteMetadata("worker-1", 56001, 61001))
	content := schedulerRouteYAML(route)
	for _, expected := range []string{
		"borealis-site-worker-worker-1-remote-desktop",
		"PathPrefix(`/_borealis/site-workers/worker-1/remote-desktop/vnc`)",
		"\"http://127.0.0.1:61001\"",
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("route yaml missing %q:\n%s", expected, content)
		}
	}
}

func TestSchedulerManagerSiteWorkerImageMatch(t *testing.T) {
	if !schedulerSiteWorkerImageMatches(map[string]any{"configured_image": "site-worker:new"}, "site-worker:new") {
		t.Fatalf("expected configured image to match")
	}
	if schedulerSiteWorkerImageMatches(map[string]any{"configured_image": "site-worker:old", "image": "site-worker:old"}, "site-worker:new") {
		t.Fatalf("stale configured image should not match")
	}
}

func TestSchedulerManagerCallsSiteWorkerHostService(t *testing.T) {
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/remote-ops/host-service/call" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get(internalTokenHeader); got != goInternalToken([]byte("test-secret")) {
			t.Fatalf("unexpected internal token %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["event_name"] != "agent_maintenance_request" {
			t.Fatalf("unexpected body %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"called":   true,
			"response": map[string]any{"status": "ok", "operation_id": "op-1"},
		})
	}))
	defer worker.Close()

	manager := &goSchedulerManager{secret: []byte("test-secret"), httpClient: worker.Client()}
	response, state, err := manager.callSiteWorkerHostService(context.Background(), routeForTestWorker(t, worker.URL), map[string]any{
		"hostname":     "LAB-OPERATOR-01",
		"service_mode": "system",
		"event_name":   "agent_maintenance_request",
		"payload":      map[string]any{"operation_id": "op-1"},
	}, time.Second)
	if err != nil || state != "called" || response["status"] != "ok" {
		t.Fatalf("unexpected call response state=%s response=%#v err=%v", state, response, err)
	}
}

func TestSchedulerManagerAgentMaintenanceErrorText(t *testing.T) {
	got := schedulerAgentMaintenanceError("LAB-01", "no_response", nil)
	if !strings.Contains(got, "did not acknowledge") {
		t.Fatalf("unexpected error text %q", got)
	}
}

func TestSchedulerManagerRunsScheduledWorkflowWorkItem(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/internal/job-scheduler/workflow/start" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get(internalTokenHeader); got != goInternalToken([]byte("test-secret")) {
			t.Fatalf("unexpected internal token %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"run_id": 77})
	}))
	defer server.Close()

	manager := &goSchedulerManager{
		apiBase:    server.URL,
		secret:     []byte("test-secret"),
		httpClient: server.Client(),
	}
	item := schedulerWorkItem{
		Kind:  schedulerKindScheduledWorkflowRun,
		JobID: sql.NullInt64{Int64: 44, Valid: true},
		RunID: sql.NullInt64{Int64: 55, Valid: true},
		Payload: map[string]any{
			"scheduled_ts": int64(1700000000),
			"workflow_component": map[string]any{
				"assembly_guid": "wf-123",
				"name":          "Nightly Workflow",
				"id":            "node-1",
			},
			"workflow_site_scope": map[string]any{"site_id": 7},
		},
	}

	if err := manager.runGlobalWorkItem(context.Background(), item); err != nil {
		t.Fatalf("run global workflow item: %v", err)
	}
	if received["workflow_guid"] != "wf-123" || received["source_type"] != "scheduled_job" {
		t.Fatalf("unexpected workflow payload %#v", received)
	}
	metadata, ok := received["source_metadata"].(map[string]any)
	if !ok || metadata["scheduled_job_id"] == nil || metadata["scheduled_job_run_id"] == nil {
		t.Fatalf("missing source metadata %#v", received)
	}
}

func TestSchedulerExecutionHelpersNormalizeScheduledPayloads(t *testing.T) {
	if got := scheduledAnsibleRelPath(`ops\ping.yml`); got != "Ansible_Playbooks/ops/ping.yml" {
		t.Fatalf("unexpected ansible rel path %q", got)
	}
	if got := scheduledEndpointPort("lab-host.example:5986"); got != 5986 {
		t.Fatalf("unexpected endpoint port %d", got)
	}
	overrides := scheduledComponentVariableOverrides(map[string]any{
		"variable_values": map[string]any{"Token": "explicit"},
		"variables": []any{
			map[string]any{"name": "Token", "value": "ignored"},
			map[string]any{"name": "Region", "value": "us-west"},
		},
	})
	if overrides["Token"] != "explicit" || overrides["Region"] != "us-west" {
		t.Fatalf("unexpected overrides %#v", overrides)
	}
	metadata := scheduledActivityMetadata(12, 34, 56, "script", "Patch", "asm-1")
	if metadata["scheduled_job_id"] != int64(12) || metadata["scheduled_job_run_id"] != int64(34) || metadata["assembly_guid"] != "asm-1" {
		t.Fatalf("unexpected metadata %#v", metadata)
	}
}
