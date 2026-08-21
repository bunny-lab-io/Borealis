package logutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
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

func TestRetentionDaysFromConfigMigratesLegacyDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), agentconfig.FileName)
	if err := os.WriteFile(path, []byte(`{"agent":{"log_retention_days":1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := RetentionDaysFromConfig(path); got != agentconfig.DefaultLogRetentionDays {
		t.Fatalf("retention days = %d, want %d", got, agentconfig.DefaultLogRetentionDays)
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

func TestRotateAndPruneDefaultKeepsSevenCalendarDays(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.log")
	now := time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local)
	activeTime := time.Date(2026, 8, 19, 12, 0, 0, 0, time.Local)
	oldestKeptTime := time.Date(2026, 8, 14, 12, 0, 0, 0, time.Local)
	staleTime := time.Date(2026, 8, 13, 12, 0, 0, 0, time.Local)

	for file, modifiedAt := range map[string]time.Time{
		path:                 activeTime,
		path + ".2026-08-14": oldestKeptTime,
		path + ".2026-08-13": staleTime,
	} {
		if err := os.WriteFile(file, []byte("log\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(file, modifiedAt, modifiedAt); err != nil {
			t.Fatal(err)
		}
	}

	if err := RotateAndPrune(path, agentconfig.DefaultLogRetentionDays, now); err != nil {
		t.Fatalf("RotateAndPrune failed: %v", err)
	}
	for _, kept := range []string{path + ".2026-08-19", path + ".2026-08-14"} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("seven-day retention removed %s: %v", kept, err)
		}
	}
	if _, err := os.Stat(path + ".2026-08-13"); !os.IsNotExist(err) {
		t.Fatalf("log older than seven calendar days was not pruned: %v", err)
	}
}
