package servicemanagement

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNormalizeServiceStatus(t *testing.T) {
	cases := map[string]string{
		"Running":          "running",
		"Start Pending":    "starting",
		"stop-pending":     "stopping",
		"continue_pending": "starting",
		"dead":             "stopped",
		"failed":           "failed",
		"not-a-state":      "unknown",
	}
	for input, want := range cases {
		if got := normalizeServiceStatus(input); got != want {
			t.Fatalf("normalizeServiceStatus(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseLinuxServices(t *testing.T) {
	output := `
ssh.service loaded active running OpenBSD Secure Shell server
cron.service loaded inactive dead Regular background program processing daemon
broken.service loaded failed failed Broken service
`
	services := parseLinuxServices(output, 123)
	if len(services) != 3 {
		t.Fatalf("service count = %d, want 3: %#v", len(services), services)
	}
	byName := map[string]Service{}
	for _, service := range services {
		byName[service.Name] = service
	}
	if byName["ssh.service"].StatusCode != "running" {
		t.Fatalf("ssh status = %#v", byName["ssh.service"])
	}
	if byName["cron.service"].StatusCode != "stopped" {
		t.Fatalf("cron status = %#v", byName["cron.service"])
	}
	if byName["broken.service"].StatusCode != "failed" {
		t.Fatalf("broken status = %#v", byName["broken.service"])
	}
	if byName["ssh.service"].CapturedAt != 123 {
		t.Fatalf("captured_at = %d, want 123", byName["ssh.service"].CapturedAt)
	}
}

func TestParseWindowsServicesHandlesSingleObject(t *testing.T) {
	services, err := parseWindowsServices(`{"Name":"Spooler","DisplayName":"Print Spooler","Description":"Loads files to memory for printing","State":"Running"}`, 456)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 {
		t.Fatalf("service count = %d, want 1", len(services))
	}
	service := services[0]
	if service.ServiceID != "spooler" || service.StatusCode != "running" || service.Status != "Running" {
		t.Fatalf("service = %#v", service)
	}
	if service.CapturedAt != 456 {
		t.Fatalf("captured_at = %d, want 456", service.CapturedAt)
	}
}

func TestHandleControlActionRejectsInvalidPayloads(t *testing.T) {
	manager := New(nil, "test-host", "system")
	manager.supported = true
	response, err := manager.HandleControlAction(context.Background(), map[string]any{
		"hostname":     "other-host",
		"service_name": "ssh.service",
		"action":       "restart",
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := response.(map[string]any)
	if payload["ok"] != false || payload["error"] != "not_for_host" {
		t.Fatalf("response = %#v", payload)
	}
	response, err = manager.HandleControlAction(context.Background(), map[string]any{
		"hostname":     "test-host",
		"service_name": "ssh.service",
		"action":       "reload",
	})
	if err != nil {
		t.Fatal(err)
	}
	payload = response.(map[string]any)
	if payload["ok"] != false || payload["error"] != "invalid_action" {
		t.Fatalf("response = %#v", payload)
	}
}

func TestRunActionPublishesFreshSnapshot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("systemctl action wiring is validated on Linux")
	}
	manager := New(nil, "test-host", "system")
	manager.supported = true
	commands := []string{}
	manager.runner = func(_ context.Context, _ time.Duration, name string, args ...string) (commandResult, error) {
		commands = append(commands, strings.TrimSpace(name+" "+strings.Join(args, " ")))
		if name == "systemctl" && len(args) > 0 && args[0] == "list-units" {
			return commandResult{Stdout: "ssh.service loaded active running OpenSSH server\n", ExitCode: 0}, nil
		}
		return commandResult{ExitCode: 0}, nil
	}
	published := make(chan []Service, 1)
	manager.publisher = func(_ context.Context, services []Service) error {
		published <- services
		return nil
	}
	manager.runAction(context.Background(), "ssh.service", "restart", "operator")
	select {
	case services := <-published:
		if len(services) != 1 || services[0].Name != "ssh.service" || services[0].StatusCode != "running" {
			t.Fatalf("published services = %#v", services)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("service snapshot was not published")
	}
	if len(commands) < 2 || !strings.Contains(commands[0], "systemctl restart ssh.service") || !strings.Contains(commands[1], "systemctl list-units") {
		t.Fatalf("commands = %#v", commands)
	}
}
