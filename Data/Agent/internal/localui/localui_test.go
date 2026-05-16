package localui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStatusSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	err := WriteStatusSnapshot(dir, StatusSnapshot{
		Hostname:    "LAB",
		ServerURL:   "https://borealis.example.test",
		EngineState: "Online",
		Roles: []RoleHealth{
			{RoleLabel: "SYSTEM Context", StatusCode: "healthy"},
		},
	})
	if err != nil {
		t.Fatalf("write status snapshot: %v", err)
	}
	snapshot, err := ReadStatusSnapshot(dir)
	if err != nil {
		t.Fatalf("read status snapshot: %v", err)
	}
	if snapshot.Hostname != "LAB" || len(snapshot.Roles) != 1 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if !strings.HasSuffix(StatusPath(dir), filepath.Join(dir, StatusFile)) {
		t.Fatalf("status path did not use override dir: %s", StatusPath(dir))
	}
}

func TestCommandRequestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	id, err := WriteCommandRequest(dir, CommandRequest{Command: CommandAgentUpdate})
	if err != nil {
		t.Fatalf("write command request: %v", err)
	}
	if id == "" {
		t.Fatalf("command id empty")
	}
	requests, err := ReadCommandRequests(context.Background(), dir, time.Minute)
	if err != nil {
		t.Fatalf("read command requests: %v", err)
	}
	if len(requests) != 1 || requests[0].Command != CommandAgentUpdate {
		t.Fatalf("unexpected requests: %+v", requests)
	}
	requests, err = ReadCommandRequests(context.Background(), dir, time.Minute)
	if err != nil {
		t.Fatalf("read command requests after consume: %v", err)
	}
	if len(requests) != 0 {
		t.Fatalf("command request was not consumed: %+v", requests)
	}
}

func TestCommandRequestRejectsUnsupportedCommand(t *testing.T) {
	dir := t.TempDir()
	if _, err := WriteCommandRequest(dir, CommandRequest{Command: "config.dump"}); err == nil {
		t.Fatalf("unsupported command was accepted")
	}
}
