package logutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRotateAndPruneKeepsOnlyConfiguredRetention(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.log")
	old := time.Date(2026, 5, 17, 12, 0, 0, 0, time.Local)
	now := time.Date(2026, 5, 19, 9, 0, 0, 0, time.Local)
	if err := os.WriteFile(path, []byte("old active\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".2026-05-16", []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path+".2026-05-16", old.AddDate(0, 0, -1), old.AddDate(0, 0, -1)); err != nil {
		t.Fatal(err)
	}
	if err := RotateAndPrune(path, 1, now); err != nil {
		t.Fatalf("RotateAndPrune failed: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("active log should rotate away before new write, stat err=%v", err)
	}
	if _, err := os.Stat(path + ".2026-05-17"); !os.IsNotExist(err) {
		t.Fatalf("previous-day rotated log should be pruned with retention=1, stat err=%v", err)
	}
	if _, err := os.Stat(path + ".2026-05-16"); !os.IsNotExist(err) {
		t.Fatalf("stale rotated log should be pruned, stat err=%v", err)
	}
}

func TestRotatingWriterRollsOnDayBoundary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.log")
	if err := os.WriteFile(path, []byte("yesterday\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	yesterday := time.Now().AddDate(0, 0, -1)
	if err := os.Chtimes(path, yesterday, yesterday); err != nil {
		t.Fatal(err)
	}
	writer, err := NewRotatingWriter(path, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.Write([]byte("today\n")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "today") {
		t.Fatalf("active log missing current write: %s", string(raw))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var rotated bool
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "agent.log.") {
			rotated = true
			break
		}
	}
	if !rotated {
		t.Fatalf("expected rotated log in %s", dir)
	}
}
