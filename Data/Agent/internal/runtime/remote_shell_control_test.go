package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bunny-lab-io/borealis/go-agent/internal/auth"
	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
	remoteshell "github.com/bunny-lab-io/borealis/go-agent/internal/roles/remote_shell"
)

func TestHandleRemoteShellRestartTargetsCurrentAgent(t *testing.T) {
	port := reserveTCPPort(t)
	t.Setenv("BOREALIS_WIREGUARD_SHELL_PORT", fmt.Sprintf("%d", port))

	root := t.TempDir()
	configPath := filepath.Join(root, agentconfig.FileName)
	cfg := agentconfig.Default()
	cfg.Agent.GUID = "2540DA38-E2B1-45B9-9113-BF7CF0E1778A"
	cfg.Agent.AgentID = "LAB-OPERATOR-01_2540DA38-E2B1-45B9-9113-BF7CF0E1778A_SYSTEM"
	if err := agentconfig.Save(configPath, &cfg); err != nil {
		t.Fatal(err)
	}
	authClient, err := auth.NewClient(configPath, &cfg, "system", auth.WithHostname("LAB-OPERATOR-01"))
	if err != nil {
		t.Fatal(err)
	}
	shellManager := remoteshell.New("LAB-OPERATOR-01", "system", configPath)
	shellManager.Start(context.Background())
	agent := &Agent{authClient: authClient, remoteShell: shellManager}
	t.Cleanup(func() {
		shellManager.Stop(context.Background())
	})

	mismatch, err := agent.handleRemoteShellRestart(context.Background(), map[string]any{
		"agent_id": "OTHER_SYSTEM",
		"reason":   "remote_shell_backend_unreachable",
	})
	if err != nil {
		t.Fatal(err)
	}
	if mismatch.(map[string]any)["error"] != "not_for_agent" {
		t.Fatalf("expected not_for_agent, got %#v", mismatch)
	}

	response, err := agent.handleRemoteShellRestart(context.Background(), map[string]any{
		"agent_id": cfg.Agent.AgentID,
		"reason":   "remote_shell_backend_unreachable",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.(map[string]any)["status"] != "ok" {
		t.Fatalf("unexpected restart response %#v", response)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if shellManager.Health().StatusCode == "healthy" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("remote shell listener did not become healthy: %#v", shellManager.Health())
}

func reserveTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func TestTrackedRemoteShellRestartUsesOriginalJournalAndIndependentShellReceipt(t *testing.T) {
	c, original, path := workerOperationTestFixture(t)
	original.dropTerminal = true
	original.terminalEmitted = make(chan struct{}, 4)
	shell := workerOperationTestPayload()
	delete(shell, "action")
	shell["type"], shell["session_id"] = "worker_operation_bind", "original-shell"
	completeShell, err := c.bindShell(context.Background(), shell)
	if err != nil {
		t.Fatal(err)
	}
	port := reserveTCPPort(t)
	t.Setenv("BOREALIS_WIREGUARD_SHELL_PORT", fmt.Sprint(port))
	manager := remoteshell.New("fixture", "system", path)
	manager.Start(context.Background())
	t.Cleanup(func() { manager.Stop(context.Background()) })
	a := &Agent{remoteShell: manager}
	payload := map[string]any{"job_id": 18, "reason": "fixture", "context": map[string]any{"worker_operation": workerOperationCorrelation(strings.Repeat("b", 32))}}
	handler := c.async("remote_shell_restart", a.handleRemoteShellRestartWithCompletion)
	ack, err := handler(context.Background(), payload)
	if err != nil || ack.(map[string]any)["status"] != "accepted" {
		t.Fatalf("restart acceptance: %#v/%v", ack, err)
	}
	select {
	case <-original.terminalEmitted:
	case <-time.After(3 * time.Second):
		t.Fatal("restart terminal missing")
	}
	// Join durable callback after server received receipt but lost its ACK.
	c.outbox.delivery.Lock()
	c.outbox.delivery.Unlock()
	if manager.Health().StatusCode != "healthy" {
		t.Fatal("terminal preceded listener installation")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(path), "worker-operations.json"))
	if err != nil {
		t.Fatal(err)
	}
	var journal struct {
		Records []agentconfig.WorkerOperationRecord
	}
	if err := json.Unmarshal(raw, &journal); err != nil || len(journal.Records) != 2 {
		t.Fatalf("journal: %s/%v", raw, err)
	}
	states := map[int64]string{}
	for _, record := range journal.Records {
		states[record.Key.JobID] = record.State
	}
	if states[17] != "granted" || states[18] != "terminal" {
		t.Fatalf("restart released original shell: %v", states)
	}
	original.mu.Lock()
	first := original.receipts[0]
	preflights := original.preflights
	original.mu.Unlock()
	if _, err := handler(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	original.mu.Lock()
	if original.preflights != preflights || len(original.receipts) != 2 || original.receipts[1] != first {
		t.Fatal("duplicate restart acquired grant or changed receipt")
	}
	original.mu.Unlock()
	changed := map[string]any{"job_id": 18, "reason": "changed", "context": payload["context"]}
	if ack, _ := handler(context.Background(), changed); ack.(map[string]any)["status"] != "rejected" {
		t.Fatal("changed request reused restart grant")
	}
	c.close()
	replacementServer := &workerOperationTestServer{grants: original.grants, committed: original.committed}
	replacement := workerOperationTestConnection(t, path, replacementServer, c.outbox)
	replacement.replay(context.Background())
	if len(replacementServer.receipts) != 1 || replacementServer.receipts[0] != first {
		t.Fatal("reconnect failed exact restart replay")
	}
	completeShell(nil)
	original.mu.Lock()
	last := original.receipts[len(original.receipts)-1]
	original.mu.Unlock()
	if !strings.Contains(last, `"job_id":17`) || len(replacementServer.receipts) != 1 {
		t.Fatal("old shell callback changed operation or emitter")
	}
	replacement.replay(context.Background())
	if len(replacementServer.receipts) != 2 || replacementServer.receipts[1] != last {
		t.Fatal("old shell did not replay its own terminal")
	}
	workerOperationTestPending(t, replacement, 0)
}

func TestTrackedRemoteShellRestartRejectsBeforeEffects(t *testing.T) {
	for _, mode := range []string{"missing_role", "wrong_agent"} {
		t.Run(mode, func(t *testing.T) {
			c, server, _ := workerOperationTestFixture(t)
			a := &Agent{}
			if mode == "wrong_agent" {
				cfg := agentconfig.Default()
				cfg.Agent.GUID = "2540DA38-E2B1-45B9-9113-BF7CF0E1778A"
				cfg.Agent.AgentID = "FIXTURE_2540DA38-E2B1-45B9-9113-BF7CF0E1778A_SYSTEM"
				path := filepath.Join(t.TempDir(), "agent.json")
				client, err := auth.NewClient(path, &cfg, "system", auth.WithHostname("FIXTURE"))
				if err != nil {
					t.Fatal(err)
				}
				a.authClient, a.remoteShell = client, remoteshell.New("FIXTURE", "system", path)
			}
			payload := workerOperationTestPayload()
			payload["agent_id"] = "OTHER_SYSTEM"
			ack, err := c.async("remote_shell_restart", a.handleRemoteShellRestartWithCompletion)(context.Background(), payload)
			if err != nil || ack.(map[string]any)["ok"] != false || len(server.receipts) != 1 || !strings.Contains(server.receipts[0], `"status":"Failed"`) {
				t.Fatalf("prework rejection lost exact terminal: %#v/%v/%v", ack, err, server.receipts)
			}
			if a.remoteShell != nil && a.remoteShell.Health().StatusCode != "pending" {
				t.Fatal("wrong Agent started listener")
			}
		})
	}
}
