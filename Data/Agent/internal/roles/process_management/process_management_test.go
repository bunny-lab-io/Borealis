package processmanagement

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"testing"
	"time"
)

func TestHandleListReturnsCurrentProcess(t *testing.T) {
	manager := New("test-host")
	response, err := manager.HandleRequest(context.Background(), map[string]any{
		"action":          "list",
		"hostname":        "test-host",
		"max_age_seconds": 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, ok := response.(map[string]any)
	if !ok {
		t.Fatalf("response type = %T", response)
	}
	if payload["ok"] != true {
		t.Fatalf("response = %#v", payload)
	}
	processes, ok := payload["processes"].([]map[string]any)
	if !ok {
		t.Fatalf("processes type = %T", payload["processes"])
	}
	currentPID := os.Getpid()
	for _, process := range processes {
		if asInt(process["pid"]) == currentPID {
			if cleanText(process["name"]) == "" {
				t.Fatalf("current process has empty name: %#v", process)
			}
			return
		}
	}
	t.Fatalf("current pid %d not found in %d processes", currentPID, len(processes))
}

func TestListUsesFreshSnapshotCache(t *testing.T) {
	manager := New("test-host")
	first, err := manager.HandleRequest(context.Background(), map[string]any{"action": "list", "max_age_seconds": 0})
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.HandleRequest(context.Background(), map[string]any{"action": "list", "max_age_seconds": 60})
	if err != nil {
		t.Fatal(err)
	}
	firstPayload := first.(map[string]any)
	secondPayload := second.(map[string]any)
	if firstPayload["reported_at"] != secondPayload["reported_at"] {
		t.Fatalf("cache missed: first=%v second=%v", firstPayload["reported_at"], secondPayload["reported_at"])
	}
}

func TestListRetriesEmptySnapshotBeforeReturning(t *testing.T) {
	manager := New("test-host")
	calls := 0
	manager.collector = func(context.Context, map[string]rateCounter, map[string]rateCounter) (Snapshot, map[string]rateCounter, map[string]rateCounter, error) {
		calls++
		if calls == 1 {
			return Snapshot{
				ReportedAt:        1700000100,
				RefreshIntervalMS: int(refreshIntervalSeconds * 1000),
				Processes:         []Process{},
			}, map[string]rateCounter{}, map[string]rateCounter{}, nil
		}
		return Snapshot{
			ReportedAt:        1700000101,
			RefreshIntervalMS: int(refreshIntervalSeconds * 1000),
			Processes: []Process{
				{ID: "123:1", PID: 123, Name: "ready-process"},
			},
		}, map[string]rateCounter{}, map[string]rateCounter{}, nil
	}

	response, err := manager.HandleRequest(context.Background(), map[string]any{
		"action":          "list",
		"hostname":        "test-host",
		"max_age_seconds": 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := response.(map[string]any)
	processes := payload["processes"].([]map[string]any)
	if calls != 2 {
		t.Fatalf("collector calls = %d, want 2", calls)
	}
	if payload["collection_state"] != "ready" || len(processes) != 1 || asInt(processes[0]["pid"]) != 123 {
		t.Fatalf("response = %#v", payload)
	}
}

func TestTerminateRefusesOwnAgentProcess(t *testing.T) {
	manager := New("test-host")
	response, err := manager.HandleRequest(context.Background(), map[string]any{
		"action": "terminate",
		"pid":    os.Getpid(),
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := response.(map[string]any)
	if payload["ok"] != false || payload["error"] != "protected_process" {
		t.Fatalf("response = %#v", payload)
	}
}

func TestTerminateProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native Windows termination validation runs on Windows hosts")
	}
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep command unavailable")
	}
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	})

	manager := New("test-host")
	response, err := manager.HandleRequest(context.Background(), map[string]any{
		"action": "terminate",
		"pid":    strconv.Itoa(pid),
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := response.(map[string]any)
	if payload["ok"] != true {
		t.Fatalf("response = %#v", payload)
	}
	done := make(chan error, 1)
	go func() {
		_, waitErr := cmd.Process.Wait()
		done <- waitErr
	}()
	select {
	case <-done:
		waited = true
	case <-time.After(5 * time.Second):
		t.Fatalf("pid %d did not exit after terminate", pid)
	}
}

func TestDescendantPIDsReturnsChildrenBeforeParent(t *testing.T) {
	processes := []Process{
		{PID: 10, ParentPID: 1},
		{PID: 11, ParentPID: 10},
		{PID: 12, ParentPID: 11},
		{PID: 13, ParentPID: 10},
	}
	got := descendantPIDs(processes, 10)
	want := []int{12, 11, 13}
	if len(got) != len(want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("got %#v want %#v", got, want)
		}
	}
}

func TestBytesPerSecondHandlesCounterDeltas(t *testing.T) {
	start := time.Unix(100, 0)
	got := bytesPerSecond(
		rateCounter{At: start, Total: 1_000},
		rateCounter{At: start.Add(2 * time.Second), Total: 3_500},
	)
	if got != 1250 {
		t.Fatalf("rate = %v, want 1250", got)
	}
	if bytesPerSecond(rateCounter{At: start, Total: 10}, rateCounter{At: start.Add(time.Second), Total: 5}) != 0 {
		t.Fatalf("counter reset should produce zero")
	}
}
