package rdp

import (
	"context"
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
