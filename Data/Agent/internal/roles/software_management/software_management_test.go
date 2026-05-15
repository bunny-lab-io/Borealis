package softwaremanagement

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestParseLinuxPackagesDedupeAndSort(t *testing.T) {
	rows := parseLinuxPackages("zlib\t1.2\nbash\t5.2\nbash\t5.2\n", "dpkg")
	if len(rows) != 2 {
		t.Fatalf("row count = %d, want 2: %#v", len(rows), rows)
	}
	if rows[0].Name != "bash" || rows[0].Version != "5.2" || rows[0].Source != "dpkg" {
		t.Fatalf("first row = %#v", rows[0])
	}
	if rows[1].Name != "zlib" {
		t.Fatalf("second row = %#v", rows[1])
	}
}

func TestParseWindowsSoftwareSingleObject(t *testing.T) {
	raw := `{"name":"Example App","version":"1.0","source":"registry","metadata":{"publisher":"Example Co","estimated_size_kb":1024,"windows_installer":true,"empty":""}}`
	rows, err := parseWindowsSoftware(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("row count = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Name != "Example App" || row.Version != "1.0" || row.Source != "local_installed" {
		t.Fatalf("row = %#v", row)
	}
	if row.Metadata["publisher"] != "Example Co" || row.Metadata["windows_installer"] != true {
		t.Fatalf("metadata = %#v", row.Metadata)
	}
	if _, ok := row.Metadata["empty"]; ok {
		t.Fatalf("empty metadata was retained: %#v", row.Metadata)
	}
}

func TestHandleRefreshRequestRejectsInvalidPayloads(t *testing.T) {
	manager := New(nil, "test-host", "system")
	manager.supported = true
	response, err := manager.HandleRefreshRequest(context.Background(), map[string]any{
		"hostname": "other-host",
		"reason":   "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := response.(map[string]any)
	if payload["ok"] != false || payload["error"] != "not_for_host" {
		t.Fatalf("response = %#v", payload)
	}
	manager.supported = false
	manager.unsupportedReason = "unsupported"
	response, err = manager.HandleRefreshRequest(context.Background(), map[string]any{
		"hostname": "test-host",
		"reason":   "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	payload = response.(map[string]any)
	if payload["ok"] != false || payload["error"] != "unsupported_platform" {
		t.Fatalf("response = %#v", payload)
	}
}

func TestRefreshPublishesLinuxInventory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux inventory command wiring is validated on Linux")
	}
	manager := New(nil, "test-host", "system")
	manager.supported = true
	commands := []string{}
	manager.runner = func(_ context.Context, _ time.Duration, name string, args ...string) (commandResult, error) {
		commands = append(commands, strings.TrimSpace(name+" "+strings.Join(args, " ")))
		return commandResult{Stdout: "bash\t5.2\ncoreutils\t9.1\n", ExitCode: 0}, nil
	}
	published := make(chan []Software, 1)
	manager.publisher = func(_ context.Context, rows []Software) error {
		published <- rows
		return nil
	}
	if err := manager.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case rows := <-published:
		if len(rows) != 2 || rows[0].Name != "bash" {
			t.Fatalf("published rows = %#v", rows)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("software inventory was not published")
	}
	if len(commands) != 1 || !strings.Contains(commands[0], "dpkg-query") && !strings.Contains(commands[0], "rpm") {
		t.Fatalf("commands = %#v", commands)
	}
}
