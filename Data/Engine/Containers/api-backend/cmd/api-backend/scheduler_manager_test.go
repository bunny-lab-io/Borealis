package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
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

func TestScheduledParseTSUsesEngineTimezoneForWallClockInput(t *testing.T) {
	t.Setenv("BOREALIS_ENGINE_HOST_TIMEZONE", "America/Denver")
	loc, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatal(err)
	}

	got := scheduledParseTS("2026-06-25T00:00")
	want := time.Date(2026, time.June, 25, 0, 0, 0, 0, loc).Unix()
	if !got.Valid || got.Int64 != want {
		t.Fatalf("expected Denver wall clock epoch %d, got %#v", want, got)
	}

	absolute := scheduledParseTS("2026-06-25T00:00:00Z")
	wantUTC := time.Date(2026, time.June, 25, 0, 0, 0, 0, time.UTC).Unix()
	if !absolute.Valid || absolute.Int64 != wantUTC {
		t.Fatalf("expected RFC3339 UTC epoch %d, got %#v", wantUTC, absolute)
	}
}

func TestSchedulerManagerCalendarNextRunPreservesLocalMidnightAcrossDST(t *testing.T) {
	t.Setenv("BOREALIS_ENGINE_HOST_TIMEZONE", "America/Denver")
	loc, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, time.March, 8, 0, 0, 0, 0, loc).Unix()
	last := time.Date(2026, time.March, 8, 0, 0, 0, 0, loc).Unix()
	now := time.Date(2026, time.March, 9, 0, 0, 0, 0, loc).Unix()

	got := schedulerComputeNextRun("daily", &start, &last, now)
	want := time.Date(2026, time.March, 9, 0, 0, 0, 0, loc).Unix()
	if got == nil || *got != want {
		t.Fatalf("expected next Denver midnight %d, got %v", want, got)
	}
}

func TestSchedulerManagerMonthlyNextRunPreservesLocalWallClockAcrossDST(t *testing.T) {
	t.Setenv("BOREALIS_ENGINE_HOST_TIMEZONE", "America/Denver")
	loc, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, time.October, 25, 0, 0, 0, 0, loc).Unix()
	last := time.Date(2026, time.October, 25, 0, 0, 0, 0, loc).Unix()
	now := time.Date(2026, time.November, 25, 0, 0, 0, 0, loc).Unix()

	got := schedulerComputeNextRun("monthly", &start, &last, now)
	want := time.Date(2026, time.November, 25, 0, 0, 0, 0, loc).Unix()
	if got == nil || *got != want {
		t.Fatalf("expected next Denver monthly wall clock %d, got %v", want, got)
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

func TestSchedulerRunTimeoutFallsBackToOrphanTimeout(t *testing.T) {
	orphan := int64(3600)
	if got := scheduledRunTimeoutSeconds("no_expire", "", orphan); got == nil || *got != orphan {
		t.Fatalf("expected orphan fallback %d, got %v", orphan, got)
	}
	if got := scheduledRunTimeoutSeconds("1h", "", orphan); got == nil || *got != 3600 {
		t.Fatalf("expected explicit expiration, got %v", got)
	}
	if got := scheduledRunTimeoutSeconds("no_expire", "workflow", orphan); got != nil {
		t.Fatalf("workflow runs should not use script orphan timeout, got %v", got)
	}
	if got := scheduledRunTimeoutSeconds("no_expire", "", 0); got != nil {
		t.Fatalf("disabled orphan timeout should stay disabled, got %v", got)
	}
}

func TestDurationForOperator(t *testing.T) {
	cases := map[int64]string{45: "45s", 1800: "30m", 7200: "2h", 172800: "2d"}
	for seconds, expected := range cases {
		if got := durationForOperator(seconds); got != expected {
			t.Fatalf("duration %d expected %q got %q", seconds, expected, got)
		}
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

func TestSchedulerSiteWorkerSocketIOAsyncModeDefaultsToEventlet(t *testing.T) {
	t.Setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "")
	if got := schedulerSiteWorkerSocketIOAsyncMode(); got != "eventlet" {
		t.Fatalf("expected eventlet default, got %q", got)
	}
	t.Setenv("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "threading")
	if got := schedulerSiteWorkerSocketIOAsyncMode(); got != "threading" {
		t.Fatalf("expected explicit threading, got %q", got)
	}
}

func TestSchedulerManagerServiceActionUsesSchedulerImageForHelper(t *testing.T) {
	tmp := t.TempDir()
	capturePath := filepath.Join(tmp, "docker-args.txt")
	dockerPath := filepath.Join(tmp, "docker")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shellQuote(capturePath) + "\nprintf 'helper-id\\n'\n"
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOREALIS_DOCKER_BIN", dockerPath)
	t.Setenv("BOREALIS_API_BACKEND_IMAGE", "borealis-engine/api-backend:test")
	t.Setenv("BOREALIS_JOB_SCHEDULER_IMAGE", "borealis-engine/job-scheduler:test")

	manager := &goSchedulerManager{projectRoot: "/opt/Borealis"}
	err := manager.runServiceAction(context.Background(), map[string]any{
		"service_key": "webui-frontend",
		"action":      map[string]any{"action": "restart"},
	})
	if err != nil {
		t.Fatalf("run service action: %v", err)
	}
	raw, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(raw)), "\n")
	joined := "\n" + strings.Join(args, "\n") + "\n"
	for _, expected := range []string{
		"\nrun\n",
		"\n--entrypoint\n",
		"\n/bin/bash\n",
		"\nborealis-engine/job-scheduler:test\n",
		"\n-lc\n",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("helper docker args missing %q:\n%s", expected, joined)
		}
	}
	if strings.Contains(joined, "borealis-engine/api-backend:test") {
		t.Fatalf("helper should not use api-backend image:\n%s", joined)
	}
	if !strings.Contains(joined, "Engine.sh") || !strings.Contains(joined, "--service") || !strings.Contains(joined, "webui-frontend") || !strings.Contains(joined, "restart") {
		t.Fatalf("helper command missing Engine.sh service action:\n%s", joined)
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

func testOpenSSHPrivateKey(t *testing.T) string {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{42}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(privateKey, "")
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(block))
}

func TestNormalizeSSHPrivateKeyMaterialRepairsEscapedLineEndings(t *testing.T) {
	raw := "-----BEGIN OPENSSH PRIVATE KEY-----\\r\\nabc\\n-----END OPENSSH PRIVATE KEY-----"
	got := normalizeSSHPrivateKeyMaterial(raw)
	if strings.Contains(got, `\n`) || strings.Contains(got, "\r") {
		t.Fatalf("private key line endings not normalized: %q", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Fatalf("expected final newline")
	}
}

func TestScheduledSSHPrivateKeyContentNormalizesAndParses(t *testing.T) {
	key := testOpenSSHPrivateKey(t)
	escaped := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(key), "\n", `\n`), "\r", "")
	got, err := scheduledSSHPrivateKeyContent(map[string]any{
		"private_key": escaped,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got == "" || strings.Contains(got, `\n`) || !strings.HasSuffix(got, "\n") {
		t.Fatalf("unexpected normalized key content")
	}
}

func TestScheduledSSHPrivateKeyContentFallsBackToPasswordOnInvalidKey(t *testing.T) {
	got, err := scheduledSSHPrivateKeyContent(map[string]any{
		"private_key": "not a private key",
		"password":    "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("invalid key with password should use password-only auth")
	}
}

func TestScheduledSSHPrivateKeyContentRejectsInvalidKeyWithoutPassword(t *testing.T) {
	_, err := scheduledSSHPrivateKeyContent(map[string]any{
		"private_key": "not a private key",
	})
	if err == nil || !strings.Contains(err.Error(), "could not be parsed") {
		t.Fatalf("expected invalid private key error, got %v", err)
	}
}

func TestAnsibleSSHPrivateKeyContentUsesContextInPassphraseError(t *testing.T) {
	_, err := ansibleSSHPrivateKeyContent(map[string]any{
		"private_key":            testOpenSSHPrivateKey(t),
		"private_key_passphrase": "phrase",
	}, "workflow Ansible runs")
	if err == nil || !strings.Contains(err.Error(), "workflow Ansible runs") {
		t.Fatalf("expected workflow context in passphrase error, got %v", err)
	}
}

func TestApplyScheduledSSHCredentialHostVarsUsesDocumentedAuthFlags(t *testing.T) {
	hostVars := map[string]any{}
	applyScheduledSSHCredentialHostVars(hostVars, map[string]any{
		"username":        "ops",
		"password":        "secret",
		"become_method":   "sudo",
		"become_username": "root",
		"become_password": "become-secret",
	}, scheduledSSHPrivateKeyPath)

	if hostVars["ansible_user"] != "ops" || hostVars["ansible_password"] != "secret" || hostVars["ansible_ssh_password_mechanism"] != "sshpass" {
		t.Fatalf("missing scheduled ssh auth vars %#v", hostVars)
	}
	if hostVars["ansible_ssh_private_key_file"] != scheduledSSHPrivateKeyPath || !strings.Contains(cleanText(hostVars["ansible_ssh_extra_args"]), "PreferredAuthentications") {
		t.Fatalf("missing scheduled private-key vars %#v", hostVars)
	}
	if hostVars["ansible_become"] != true || hostVars["ansible_become_method"] != "sudo" || hostVars["ansible_become_user"] != "root" || hostVars["ansible_become_password"] != "become-secret" {
		t.Fatalf("missing scheduled become vars %#v", hostVars)
	}
}
