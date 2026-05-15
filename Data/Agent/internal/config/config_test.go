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
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"\"runtime\"", "\"feature_flags\"", "\"last_saved_at\"", "\"extra\""} {
		if strings.Contains(string(raw), removed) {
			t.Fatalf("config contains removed field %s: %s", removed, string(raw))
		}
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
