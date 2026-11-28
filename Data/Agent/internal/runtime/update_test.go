package agentruntime

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
)

func TestHandleAgentMaintenanceRequestPersistsOperationBeforeAckAndStartsUpdaterAfterAck(t *testing.T) {
	for _, tc := range []struct {
		status string
		err    error
	}{{status: "updater_started"}, {status: "failed", err: errors.New("fixture updater failed")}} {
		t.Run(tc.status, func(t *testing.T) {
			originalStarter := startLocalUpdaterForRequest
			t.Cleanup(func() { startLocalUpdaterForRequest = originalStarter })
			started := make(chan struct{}, 1)
			release := make(chan struct{})
			var releaseOnce sync.Once
			releaseUpdater := func() { releaseOnce.Do(func() { close(release) }) }
			startLocalUpdaterForRequest = func(configPath string) error {
				started <- struct{}{}
				<-release
				return tc.err
			}

			configPath := filepath.Join(t.TempDir(), agentconfig.FileName)
			cfg := agentconfig.Default()
			cfg.ServerURL = "https://borealis.example.com"
			if err := agentconfig.Save(configPath, &cfg); err != nil {
				t.Fatal(err)
			}

			agent, err := New(Options{ConfigPath: configPath, ServiceMode: "system"}, log.New(io.Discard, "", 0))
			if err != nil {
				t.Fatal(err)
			}
			response, err := agent.handleAgentMaintenanceRequest(context.Background(), map[string]any{
				"operation_id": "op-ack-first",
				"kind":         "update_now",
			})
			if err != nil {
				t.Fatal(err)
			}
			body, afterAck := responseBody(t, response)
			if body["status"] != "ok" || body["operation_id"] != "op-ack-first" {
				t.Fatalf("unexpected response: %#v", body)
			}
			select {
			case <-started:
				t.Fatalf("local updater started before ack callback")
			default:
			}

			loaded, err := agentconfig.Load(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if loaded.Agent.Update.OperationID != "op-ack-first" || loaded.Agent.Update.Status != "requested" {
				t.Fatalf("update operation must be durable before ack callback: %#v", loaded.Agent.Update)
			}
			done := runUpdateAfterAck(t, afterAck, releaseUpdater)
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatalf("local updater was not started after ack callback")
			}
			select {
			case <-done:
				t.Fatal("ack callback returned before updater completed")
			default:
			}
			releaseUpdater()
			waitUpdateAfterAck(t, done)
			loaded, err = agentconfig.Load(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if loaded.Agent.Update.Status != tc.status || loaded.Agent.Update.OperationID != "op-ack-first" {
				t.Fatalf("ack callback returned before persisting updater result: %#v", loaded.Agent.Update)
			}
			if tc.err != nil && (loaded.Agent.Update.LastError != tc.err.Error() || loaded.Agent.Update.CompletedAt == 0) {
				t.Fatalf("failed updater result missing completion or error: %#v", loaded.Agent.Update)
			}
		})
	}
}

func TestHandleUpdateRequestStoresSchedulerCorrelationBeforeAckCallback(t *testing.T) {
	originalStarter := startLocalUpdaterForRequest
	t.Cleanup(func() { startLocalUpdaterForRequest = originalStarter })
	started := make(chan struct{}, 1)
	startLocalUpdaterForRequest = func(configPath string) error {
		started <- struct{}{}
		return nil
	}

	configPath := filepath.Join(t.TempDir(), agentconfig.FileName)
	cfg := agentconfig.Default()
	cfg.ServerURL = "https://borealis.example.com"
	if err := agentconfig.Save(configPath, &cfg); err != nil {
		t.Fatal(err)
	}

	agent, err := New(Options{ConfigPath: configPath, ServiceMode: "system"}, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	response, err := agent.handleUpdateRequest(context.Background(), map[string]any{
		"operation_id":         "op-current-target",
		"scheduled_job_id":     float64(123),
		"scheduled_job_run_id": float64(456),
		"requested_by":         "operator",
		"source":               "operator_initiated",
	})
	if err != nil {
		t.Fatal(err)
	}
	body, afterAck := responseBody(t, response)
	if body["status"] != "ok" || body["operation_id"] != "op-current-target" {
		t.Fatalf("unexpected response: %#v", body)
	}
	loaded, err := agentconfig.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Agent.Update.OperationID != "op-current-target" || loaded.Agent.Update.Status != "requested" || loaded.Agent.Update.ScheduledJobID != 123 || loaded.Agent.Update.ScheduledJobRunID != 456 {
		t.Fatalf("update operation correlation must be written before ack callback: %#v", loaded.Agent.Update)
	}
	select {
	case <-started:
		t.Fatal("local updater started before ack callback")
	default:
	}

	done := runUpdateAfterAck(t, afterAck, func() {})
	waitUpdateAfterAck(t, done)
	loaded, err = agentconfig.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Agent.Update.OperationID != "op-current-target" || loaded.Agent.Update.Status != "updater_started" || loaded.Agent.Update.ScheduledJobID != 123 || loaded.Agent.Update.ScheduledJobRunID != 456 {
		t.Fatalf("ack callback did not persist updater result with scheduler correlation: %#v", loaded.Agent.Update)
	}
	if loaded.Agent.Update.Kind != "update_now" {
		t.Fatalf("update kind = %q", loaded.Agent.Update.Kind)
	}
}

func runUpdateAfterAck(t *testing.T, afterAck, release func()) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	// Registered after TempDir and hook restoration: even a fatal assertion
	// releases the fixture and joins the callback before either cleanup runs.
	t.Cleanup(func() {
		release()
		<-done
	})
	go func() {
		defer close(done)
		afterAck()
	}()
	return done
}

func waitUpdateAfterAck(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ack callback did not complete")
	}
}

func TestHandleUpdateRequestCoalescesDuplicateActiveOperation(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), agentconfig.FileName)
	cfg := agentconfig.Default()
	cfg.ServerURL = "https://borealis.example.com"
	cfg.Agent.Update = agentconfig.AgentUpdateSection{
		OperationID: "existing-operation",
		Status:      "awaiting_health",
		UpdatedAt:   time.Now().Unix(),
		DeadlineAt:  time.Now().Add(time.Minute).Unix(),
	}
	if err := agentconfig.Save(configPath, &cfg); err != nil {
		t.Fatal(err)
	}
	agent, err := New(Options{ConfigPath: configPath, ServiceMode: "system"}, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	response, err := agent.handleUpdateRequest(context.Background(), map[string]any{"operation_id": "new-operation"})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := responseBody(t, response)
	if body["status"] != "active" || body["operation_id"] != "existing-operation" {
		t.Fatalf("duplicate request was not coalesced: %#v", body)
	}
}

type testAfterAckResponse interface {
	AckPayload() any
	AfterAck()
}

func responseBody(t *testing.T, response any) (map[string]any, func()) {
	t.Helper()
	afterAck := func() {}
	if wrapped, ok := response.(testAfterAckResponse); ok {
		afterAck = wrapped.AfterAck
		response = wrapped.AckPayload()
	}
	body, ok := response.(map[string]any)
	if !ok {
		t.Fatalf("response is %T", response)
	}
	return body, afterAck
}

func TestWriteInstalledBuildIDStoresConfigAndRemovesLegacyStatus(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, agentconfig.FileName)
	cfg := agentconfig.Default()
	cfg.ServerURL = "https://borealis.example.com"
	if err := agentconfig.Save(configPath, &cfg); err != nil {
		t.Fatal(err)
	}
	statusPath := filepath.Join(root, "Updater", "update_status.json")
	if err := os.MkdirAll(filepath.Dir(statusPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statusPath, []byte(`{"state":"old"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := writeInstalledBuildID(configPath, "ABC123"); err != nil {
		t.Fatal(err)
	}

	loaded, err := agentconfig.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Agent.InstalledBuildID != "abc123" {
		t.Fatalf("installed_build_id = %q", loaded.Agent.InstalledBuildID)
	}
	if _, err := os.Stat(statusPath); !os.IsNotExist(err) {
		t.Fatalf("legacy update_status.json still exists or unexpected stat error: %v", err)
	}
}

func TestWriteInstalledBuildIDDoesNotAdvanceRequestedOperation(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), agentconfig.FileName)
	cfg := agentconfig.Default()
	cfg.Agent.Update = agentconfig.AgentUpdateSection{OperationID: "op-requested", Status: "requested"}
	if err := agentconfig.Save(configPath, &cfg); err != nil {
		t.Fatal(err)
	}

	if err := writeInstalledBuildID(configPath, "ABC123"); err != nil {
		t.Fatal(err)
	}
	loaded, err := agentconfig.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Agent.Update.Status != "requested" {
		t.Fatalf("startup advanced operation before updater ran: %+v", loaded.Agent.Update)
	}
}
