package rdp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeAuthClient struct {
	agentID  string
	response map[string]any
	path     string
}

func (f *fakeAuthClient) PostJSON(_ context.Context, path string, _ any, response any) (any, error) {
	f.path = path
	if target, ok := response.(*map[string]any); ok {
		*target = f.response
	}
	return nil, nil
}

func (f *fakeAuthClient) AgentID() string {
	return f.agentID
}

func TestBuildFirewallCommandScopesOnlyBorealisRule(t *testing.T) {
	command := buildFirewallCommand("10.255.0.1/32", "10.255.0.42/32", 3389)
	for _, expected := range []string{
		"Borealis - RDP - WireGuard",
		"Borealis managed RDP; port=3389; local=10.255.0.42/32; remote=10.255.0.1/32",
		"Get-NetFirewallPortFilter",
		"Get-NetFirewallAddressFilter",
		"if (-not $valid)",
		"-LocalPort 3389",
		"-LocalAddress '10.255.0.42/32'",
		"-RemoteAddress '10.255.0.1/32'",
	} {
		if !strings.Contains(command, expected) {
			t.Fatalf("firewall command missing %q: %s", expected, command)
		}
	}
	for _, forbidden := range []string{"Set-NetFirewallProfile", "Remote Desktop Users", "Enable-NetFirewallRule -DisplayGroup"} {
		if strings.Contains(command, forbidden) {
			t.Fatalf("firewall command contains broad mutation %q: %s", forbidden, command)
		}
	}
}

func TestStartRequestUsesCachedReadyFastPath(t *testing.T) {
	runnerCalls := 0
	manager := &Manager{
		authClient:        &fakeAuthClient{agentID: "system:agent-1"},
		supported:         true,
		port:              3389,
		engineAddress:     "10.255.0.1/32",
		localAddress:      "10.255.0.42/32",
		lastReady:         true,
		lastReadyAt:       time.Now().Unix(),
		lastServiceState:  "RUNNING",
		lastListenerState: "listening",
		listenerProbe:     func(int) bool { return true },
		runner: func(context.Context, time.Duration, string, ...string) (commandResult, error) {
			runnerCalls++
			return commandResult{}, errors.New("ready fast path should not run lifecycle commands")
		},
	}

	raw, err := manager.HandleStart(context.Background(), map[string]any{
		"agent_id":        "system:agent-1",
		"reason":          "rdp_establish",
		"rdp_port":        3389,
		"allowed_ips":     "10.255.0.1/32",
		"virtual_ip":      "10.255.0.42/32",
		"timeout_seconds": 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := raw.(map[string]any)
	if payload["ready"] != true || runnerCalls != 0 {
		t.Fatalf("unexpected fast-path response=%#v runner_calls=%d", payload, runnerCalls)
	}
}

func TestReadinessRequestContextCapsAtSixtySeconds(t *testing.T) {
	ctx, cancel := readinessRequestContext(context.Background(), 120)
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected readiness deadline")
	}
	remaining := time.Until(deadline)
	if remaining < 59*time.Second || remaining > maxReadinessTimeout {
		t.Fatalf("unexpected readiness deadline %s", remaining)
	}
}

func TestForegroundEnsureStopsWaitingWhenRequestExpires(t *testing.T) {
	manager := &Manager{supported: true}
	manager.ensureMu.Lock()
	defer manager.ensureMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	err := manager.ensureReady(ctx, "rdp_establish")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline while waiting for lifecycle lock, got %v", err)
	}
}

func TestRoutineEnsureCoalescesWhenLifecycleBusy(t *testing.T) {
	runnerCalls := 0
	manager := &Manager{
		supported: true,
		runner: func(context.Context, time.Duration, string, ...string) (commandResult, error) {
			runnerCalls++
			return commandResult{}, nil
		},
	}
	manager.ensureMu.Lock()
	defer manager.ensureMu.Unlock()

	if err := manager.ensureReady(context.Background(), "always_on_check"); err != nil {
		t.Fatal(err)
	}
	if runnerCalls != 0 {
		t.Fatalf("coalesced routine ensure ran %d lifecycle commands", runnerCalls)
	}
}

func TestRoutineEnsureSkipsHeavyAuditUntilDue(t *testing.T) {
	runnerCalls := 0
	manager := &Manager{
		supported:       true,
		lastReconcileAt: time.Now().Unix(),
		runner: func(context.Context, time.Duration, string, ...string) (commandResult, error) {
			runnerCalls++
			return commandResult{}, nil
		},
	}

	if err := manager.ensureReady(context.Background(), "always_on_check"); err != nil {
		t.Fatal(err)
	}
	if runnerCalls != 0 {
		t.Fatalf("recent routine audit ran %d lifecycle commands", runnerCalls)
	}
}

func TestEnsureFirewallPreservesCommandFailureOutput(t *testing.T) {
	manager := &Manager{
		runner: func(context.Context, time.Duration, string, ...string) (commandResult, error) {
			return commandResult{Stderr: "Access is denied.", ExitCode: 1}, errors.New("exit status 1")
		},
	}
	err := manager.ensureFirewall(context.Background(), "10.255.0.1/32", "10.255.0.42/32", 3389)
	if err == nil || !strings.Contains(err.Error(), "Access is denied") {
		t.Fatalf("expected PowerShell output in error, got %v", err)
	}
}

func TestBuildFirewallCommandRejectsNonIPv4Scope(t *testing.T) {
	if command := buildFirewallCommand("Any", "10.255.0.42", 3389); command != "" {
		t.Fatalf("expected invalid remote scope rejection, got %s", command)
	}
	if command := buildFirewallCommand("10.255.0.1", "0.0.0.0", 3389); command != "" {
		t.Fatalf("expected unspecified local scope rejection, got %s", command)
	}
}

func TestEnsureWindowsRDPDoesNotMutateGroupsOrPolicy(t *testing.T) {
	manager := &Manager{supported: true}
	command := ""
	manager.runner = func(_ context.Context, _ time.Duration, _ string, args ...string) (commandResult, error) {
		command = strings.Join(args, " ")
		return commandResult{}, nil
	}
	if err := manager.ensureWindowsRDP(context.Background()); err != nil {
		t.Fatalf("ensureWindowsRDP returned error: %v", err)
	}
	for _, expected := range []string{"fDenyTSConnections", "TermService", "StartupType Automatic"} {
		if !strings.Contains(command, expected) {
			t.Fatalf("RDP enable command missing %q: %s", expected, command)
		}
	}
	for _, forbidden := range []string{"Add-LocalGroupMember", "Remote Desktop Users", "GroupPolicy", "Set-GPRegistryValue"} {
		if strings.Contains(command, forbidden) {
			t.Fatalf("RDP enable command contains forbidden mutation %q: %s", forbidden, command)
		}
	}
}

func TestEnsureFromEngineUsesWireGuardAddresses(t *testing.T) {
	authClient := &fakeAuthClient{
		agentID: "system:agent-1",
		response: map[string]any{
			"allowed_ips": "10.255.0.1/32",
			"virtual_ip":  "10.255.0.42/32",
			"rdp_port":    3389,
		},
	}
	manager := &Manager{
		authClient: authClient,
		supported:  true,
		platform:   "windows",
		port:       defaultRDPPort,
	}
	manager.runner = func(_ context.Context, _ time.Duration, name string, args ...string) (commandResult, error) {
		if name == "sc.exe" {
			return commandResult{Stdout: "STATE              : 4  RUNNING"}, nil
		}
		return commandResult{}, nil
	}
	manager.listenerProbe = func(int) bool { return true }

	if err := manager.ensureFromEngine(context.Background(), "test"); err != nil {
		t.Fatalf("ensureFromEngine returned error: %v", err)
	}
	if authClient.path != "/api/agent/rdp/ensure" {
		t.Fatalf("path = %q", authClient.path)
	}
	if manager.engineAddress != "10.255.0.1/32" || manager.localAddress != "10.255.0.42/32" {
		t.Fatalf("unexpected scopes engine=%q local=%q", manager.engineAddress, manager.localAddress)
	}
}
