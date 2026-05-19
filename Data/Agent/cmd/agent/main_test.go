package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
	agentruntime "github.com/bunny-lab-io/borealis/go-agent/internal/runtime"
)

func TestPersistInstallConfigRewritesAgentJSONWithFlatReleaseChannel(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, agentconfig.FileName)
	raw := `{
  "schema_version": 1,
  "server_url": "https://old.example",
  "release_channel": "source",
  "agent": {
    "branch": "old-branch"
  },
  "identity": {},
  "tokens": {},
  "trust": {}
}`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := agentconfig.Load(configPath); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("strict load should reject flat release_channel, got %v", err)
	}

	err := persistInstallConfig(agentruntime.Options{
		ConfigPath:     configPath,
		ServerURL:      "https://borealis.example.com/",
		EnrollmentCode: "CODE",
		RepoRef:        "feature/linux-install",
	})
	if err != nil {
		t.Fatalf("persistInstallConfig failed: %v", err)
	}
	loaded, err := agentconfig.Load(configPath)
	if err != nil {
		t.Fatalf("strict load after rewrite failed: %v", err)
	}
	if loaded.ServerURL != "https://borealis.example.com" {
		t.Fatalf("server_url = %q", loaded.ServerURL)
	}
	if loaded.EnrollmentCode != "CODE" {
		t.Fatalf("enrollment_code = %q", loaded.EnrollmentCode)
	}
	if loaded.Agent.Branch != "feature/linux-install" {
		t.Fatalf("branch = %q", loaded.Agent.Branch)
	}
	if loaded.Agent.ReleaseChannel != agentconfig.ReleaseChannelSource {
		t.Fatalf("release_channel = %q", loaded.Agent.ReleaseChannel)
	}
	rewritten, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rewritten), "\n  \"release_channel\"") {
		t.Fatalf("flat release_channel survived rewrite: %s", string(rewritten))
	}
}
