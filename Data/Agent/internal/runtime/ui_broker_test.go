package agentruntime

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/bunny-lab-io/borealis/go-agent/internal/localui"
)

func TestUIBrokerCommandsAndAuth(t *testing.T) {
	stateDir := t.TempDir()
	updateCalled := false
	restartCalled := false
	broker, err := startUIBroker(context.Background(), uiBrokerOptions{
		StateDir: stateDir,
		Status: func() localui.StatusSnapshot {
			return localui.StatusSnapshot{
				Hostname:    "LAB-OPERATOR-01",
				ServerURL:   "https://borealis.example.test",
				EngineState: "Online",
				Roles: []localui.RoleHealth{
					{RoleLabel: "SYSTEM Context", StatusCode: "healthy"},
				},
			}
		},
		StartUpdate: func() error {
			updateCalled = true
			return nil
		},
		RestartAgent: func() error {
			restartCalled = true
			return nil
		},
		DiagnosticsText: func() string {
			return "diagnostics"
		},
	})
	if err != nil {
		t.Fatalf("start broker: %v", err)
	}
	defer broker.Close()

	httpClient := &http.Client{Timeout: 5 * time.Second}
	state, err := localui.ReadBrokerState(stateDir)
	if err != nil {
		t.Fatalf("read broker state: %v", err)
	}
	if state.Token == "" || state.URL == "" {
		t.Fatalf("broker state missing URL/token: %+v", state)
	}

	response, err := localui.DoCommandWithState(context.Background(), httpClient, state, localui.CommandRequest{Command: localui.CommandStatusGet})
	if err != nil {
		t.Fatalf("status command failed: %v", err)
	}
	if response.Status != "ok" {
		t.Fatalf("unexpected response: %+v", response)
	}

	if _, err := localui.DoCommandWithState(context.Background(), httpClient, localui.BrokerState{URL: state.URL, Token: "wrong"}, localui.CommandRequest{Command: localui.CommandStatusGet}); err == nil {
		t.Fatalf("unauthorized command unexpectedly succeeded")
	}

	if _, err := localui.DoCommandWithState(context.Background(), httpClient, state, localui.CommandRequest{Command: localui.CommandAgentUpdate}); err != nil {
		t.Fatalf("update command failed: %v", err)
	}
	if !updateCalled {
		t.Fatalf("update command did not call action")
	}

	if _, err := localui.DoCommandWithState(context.Background(), httpClient, state, localui.CommandRequest{Command: localui.CommandAgentRestart}); err != nil {
		t.Fatalf("restart command failed: %v", err)
	}
	if !restartCalled {
		t.Fatalf("restart command did not call action")
	}

	diagnostics, err := localui.DoCommandWithState(context.Background(), httpClient, state, localui.CommandRequest{Command: localui.CommandDiagnosticsCopy})
	if err != nil {
		t.Fatalf("diagnostics command failed: %v", err)
	}
	if diagnostics.Status != "ok" {
		t.Fatalf("unexpected diagnostics response: %+v", diagnostics)
	}
}
