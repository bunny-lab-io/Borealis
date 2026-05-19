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
	cfg.Agent.ReleaseChannel = "Source"
	cfg.Agent.Branch = "feature/test"
	cfg.Agent.InstalledBuildID = "ABCDEF"
	cfg.DependencyVersions = &DependencyVersionsSection{WireGuard: "1.1\r\n", UltraVNC: "1.8.2.1"}
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
	if loaded.Agent.ReleaseChannel != ReleaseChannelSource {
		t.Fatalf("release channel mismatch: %q", loaded.Agent.ReleaseChannel)
	}
	if loaded.Agent.InstalledBuildID != "abcdef" {
		t.Fatalf("installed build id mismatch: %q", loaded.Agent.InstalledBuildID)
	}
	if loaded.Agent.LogRetentionDays != DefaultLogRetentionDays {
		t.Fatalf("log retention default = %d, want %d", loaded.Agent.LogRetentionDays, DefaultLogRetentionDays)
	}
	if loaded.DependencyVersions == nil {
		t.Fatal("dependency_versions missing")
	}
	if loaded.DependencyVersions.WireGuard != "1.1" {
		t.Fatalf("wireguard dependency version mismatch: %q", loaded.DependencyVersions.WireGuard)
	}
	if loaded.DependencyVersions.UltraVNC != "1.8.2.1" {
		t.Fatalf("ultravnc dependency version mismatch: %q", loaded.DependencyVersions.UltraVNC)
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

func TestFileNameIsAgentJSONOnly(t *testing.T) {
	if FileName != "agent.json" {
		t.Fatalf("FileName = %q, want agent.json", FileName)
	}
}

func TestLoadDoesNotFallbackToConfigJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"schema_version":1,"server_url":"https://old.example","agent":{},"identity":{},"tokens":{},"trust":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.ServerURL != "" {
		t.Fatalf("loaded server_url from config.json fallback: %q", loaded.ServerURL)
	}
}

func TestLivenessUpdatePersistsAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	cfg := Default()
	if err := Save(path, &cfg); err != nil {
		t.Fatal(err)
	}
	if err := Update(path, func(cfg *AgentConfig) {
		cfg.Agent.Liveness.PID = 123
		cfg.Agent.Liveness.BootID = "boot-1"
		cfg.Agent.Liveness.LastLocalTickAt = 456
	}); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Agent.Liveness.PID != 123 || loaded.Agent.Liveness.BootID != "boot-1" || loaded.Agent.Liveness.LastLocalTickAt != 456 {
		t.Fatalf("liveness not persisted: %#v", loaded.Agent.Liveness)
	}
}

func TestDefaultBranch(t *testing.T) {
	cfg := Default()
	cfg.ApplyDefaults()
	if cfg.Agent.Branch != DefaultBranch {
		t.Fatalf("default branch = %q, want %q", cfg.Agent.Branch, DefaultBranch)
	}
	if cfg.Agent.ReleaseChannel != ReleaseChannelStable {
		t.Fatalf("default release channel = %q, want %q", cfg.Agent.ReleaseChannel, ReleaseChannelStable)
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
