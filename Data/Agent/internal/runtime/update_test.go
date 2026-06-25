package agentruntime

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
)

func TestHandleReleaseChannelChangedStoresUnstableBranch(t *testing.T) {
	originalStarter := startLocalUpdaterForRequest
	t.Cleanup(func() { startLocalUpdaterForRequest = originalStarter })
	startedUpdater := make(chan struct{}, 1)
	startLocalUpdaterForRequest = func(configPath string) error {
		startedUpdater <- struct{}{}
		return nil
	}

	configPath := filepath.Join(t.TempDir(), agentconfig.FileName)
	cfg := agentconfig.Default()
	cfg.ServerURL = "https://borealis.example.com"
	cfg.Agent.ReleaseChannel = agentconfig.ReleaseChannelStable
	cfg.Agent.Branch = agentconfig.DefaultBranch
	if err := agentconfig.Save(configPath, &cfg); err != nil {
		t.Fatal(err)
	}

	agent, err := New(Options{ConfigPath: configPath, ServiceMode: "system"}, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	response, err := agent.handleReleaseChannelChanged(context.Background(), map[string]any{
		"channel": "unstable",
		"branch":  "feature/rewrite-borealis-agent-in-golang",
	})
	if err != nil {
		t.Fatal(err)
	}
	body, afterAck := responseBody(t, response)
	if body["status"] != "ok" {
		t.Fatalf("status = %v", body["status"])
	}
	if body["release_channel"] != agentconfig.ReleaseChannelUnstable {
		t.Fatalf("release_channel = %v", body["release_channel"])
	}
	select {
	case <-startedUpdater:
		t.Fatalf("local updater started before ack callback")
	case <-time.After(50 * time.Millisecond):
	}
	go afterAck()
	select {
	case <-startedUpdater:
	case <-time.After(time.Second):
		t.Fatalf("local updater was not started after ack callback")
	}

	var loaded agentconfig.AgentConfig
	for i := 0; i < 20; i++ {
		loaded, err = agentconfig.Load(configPath)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Agent.Update.OperationID != "" && loaded.Agent.Update.Status == "updater_started" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if loaded.Agent.ReleaseChannel != agentconfig.ReleaseChannelUnstable {
		t.Fatalf("stored release_channel = %q", loaded.Agent.ReleaseChannel)
	}
	if loaded.Agent.Branch != "feature/rewrite-borealis-agent-in-golang" {
		t.Fatalf("stored branch = %q", loaded.Agent.Branch)
	}
	if loaded.Agent.Update.OperationID == "" || loaded.Agent.Update.Status != "updater_started" {
		t.Fatalf("update operation not tracked: %#v", loaded.Agent.Update)
	}
}

func TestHandleReleaseChannelChangedRejectsMissingChannel(t *testing.T) {
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
	response, err := agent.handleReleaseChannelChanged(context.Background(), map[string]any{
		"branch": "feature/test",
	})
	if err != nil {
		t.Fatal(err)
	}
	body := response.(map[string]any)
	if body["status"] != "error" || body["detail"] != "release_channel missing" {
		t.Fatalf("unexpected response: %#v", body)
	}
}

func TestHandleAgentMaintenanceRequestStartsUpdaterAfterAckCallback(t *testing.T) {
	originalStarter := startLocalUpdaterForRequest
	t.Cleanup(func() { startLocalUpdaterForRequest = originalStarter })
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	startLocalUpdaterForRequest = func(configPath string) error {
		started <- struct{}{}
		<-release
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
	response, err := agent.handleAgentMaintenanceRequest(context.Background(), map[string]any{
		"operation_id":    "op-ack-first",
		"release_channel": "unstable",
		"branch":          "feature/rewrite-api-backend-in-golang",
		"kind":            "update_now",
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
	case <-time.After(50 * time.Millisecond):
	}

	loaded, err := agentconfig.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Agent.Update.OperationID != "" {
		t.Fatalf("update operation should not be written before ack callback: %#v", loaded.Agent.Update)
	}
	go afterAck()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatalf("local updater was not started after ack callback")
	}
	close(release)
	for i := 0; i < 20; i++ {
		loaded, err = agentconfig.Load(configPath)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Agent.Update.Status == "updater_started" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("update operation did not reach updater_started: %#v", loaded.Agent.Update)
}

func TestHandleUpdateRequestStoresCurrentTargetAfterAckCallback(t *testing.T) {
	originalStarter := startLocalUpdaterForRequest
	t.Cleanup(func() { startLocalUpdaterForRequest = originalStarter })
	startLocalUpdaterForRequest = func(configPath string) error {
		return nil
	}

	configPath := filepath.Join(t.TempDir(), agentconfig.FileName)
	cfg := agentconfig.Default()
	cfg.ServerURL = "https://borealis.example.com"
	cfg.Agent.ReleaseChannel = agentconfig.ReleaseChannelUnstable
	cfg.Agent.Branch = "feature/rewrite-api-backend-in-golang"
	if err := agentconfig.Save(configPath, &cfg); err != nil {
		t.Fatal(err)
	}

	agent, err := New(Options{ConfigPath: configPath, ServiceMode: "system"}, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	response, err := agent.handleUpdateRequest(context.Background(), map[string]any{
		"operation_id": "op-current-target",
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
	if loaded.Agent.Update.OperationID != "" {
		t.Fatalf("update operation should not be written before ack callback: %#v", loaded.Agent.Update)
	}

	go afterAck()
	for i := 0; i < 20; i++ {
		loaded, err = agentconfig.Load(configPath)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Agent.Update.Status == "updater_started" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if loaded.Agent.Update.OperationID != "op-current-target" {
		t.Fatalf("operation id = %q", loaded.Agent.Update.OperationID)
	}
	if loaded.Agent.Update.TargetChannel != agentconfig.ReleaseChannelUnstable {
		t.Fatalf("target channel = %q", loaded.Agent.Update.TargetChannel)
	}
	if loaded.Agent.Update.TargetBranch != "feature/rewrite-api-backend-in-golang" {
		t.Fatalf("target branch = %q", loaded.Agent.Update.TargetBranch)
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

func TestReleaseChannelFromPayloadAliases(t *testing.T) {
	tests := []struct {
		name    string
		payload map[string]any
		want    string
	}{
		{name: "source", payload: map[string]any{"release_channel": "source"}, want: agentconfig.ReleaseChannelUnstable},
		{name: "unstable", payload: map[string]any{"channel": "unstable"}, want: agentconfig.ReleaseChannelUnstable},
		{name: "branch", payload: map[string]any{"target_channel": "branch"}, want: agentconfig.ReleaseChannelUnstable},
		{name: "stable", payload: map[string]any{"effective_channel": "stable"}, want: agentconfig.ReleaseChannelStable},
		{name: "release", payload: map[string]any{"release_channel": "release"}, want: agentconfig.ReleaseChannelStable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := releaseChannelFromPayload(tt.payload); got != tt.want {
				t.Fatalf("releaseChannelFromPayload = %q, want %q", got, tt.want)
			}
		})
	}
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
