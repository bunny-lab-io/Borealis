package agentruntime

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
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
