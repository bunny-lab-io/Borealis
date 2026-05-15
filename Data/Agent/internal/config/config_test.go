package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSaveLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	cfg := Default()
	cfg.ServerURL = "https://borealis.example.com/"
	cfg.EnrollmentCode = "CODE"
	cfg.Agent.GUID = "guid"
	cfg.Agent.Branch = "feature/test"
	cfg.Identity.PublicKeySPKIB64 = "pub"

	if err := Save(path, &cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.SchemaVersion != SchemaVersion {
		t.Fatalf("schema mismatch: %d", loaded.SchemaVersion)
	}
	if loaded.ServerURL != "https://borealis.example.com" {
		t.Fatalf("server url not normalized: %q", loaded.ServerURL)
	}
	if loaded.Agent.Branch != "feature/test" {
		t.Fatalf("branch mismatch: %q", loaded.Agent.Branch)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, unexpected := range []string{"\"runtime\"", "\"feature_flags\"", "\"last_saved_at\"", "\"extra\""} {
		if strings.Contains(string(raw), unexpected) {
			t.Fatalf("config contains unexpected field %s: %s", unexpected, string(raw))
		}
	}
}

func TestDefaultBranch(t *testing.T) {
	cfg := Default()
	cfg.ApplyDefaults()
	if cfg.Agent.Branch != DefaultBranch {
		t.Fatalf("default branch = %q, want %q", cfg.Agent.Branch, DefaultBranch)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	raw := `{
  "schema_version": 1,
  "server_url": "https://borealis.example.com",
  "agent": {},
  "identity": {},
  "tokens": {},
  "trust": {},
  "runtime": {"feature_flags": {"system_scripts": true}}
}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestUnixPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions test")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	cfg := Default()
	if err := Save(path, &cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("file mode = %o, want 600", got)
	}
	parent, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := parent.Mode().Perm(); got != 0o700 {
		t.Fatalf("dir mode = %o, want 700", got)
	}
}
