package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
	agentruntime "github.com/bunny-lab-io/borealis/go-agent/internal/runtime"
)

func TestPersistInstallConfigRewritesAgentJSONWithFlatReleaseChannel(t *testing.T) {
	originalResolver := resolveInstallRepoRefBuildIDFunc
	t.Cleanup(func() { resolveInstallRepoRefBuildIDFunc = originalResolver })
	resolveInstallRepoRefBuildIDFunc = func(ctx context.Context, repoRef string) (string, error) {
		return "ABCDEF123456", nil
	}
	dir := t.TempDir()
	configPath := filepath.Join(dir, agentconfig.FileName)
	raw := `{
  "schema_version": 1,
  "server_url": "https://old.example",
  "release_channel": "unstable",
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
	if loaded.Agent.ReleaseChannel != agentconfig.ReleaseChannelUnstable {
		t.Fatalf("release_channel = %q", loaded.Agent.ReleaseChannel)
	}
	if loaded.Agent.InstalledBuildID != "abcdef123456" {
		t.Fatalf("installed_build_id = %q", loaded.Agent.InstalledBuildID)
	}
	rewritten, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rewritten), "\n  \"release_channel\"") {
		t.Fatalf("flat release_channel survived rewrite: %s", string(rewritten))
	}
}

func TestValidateAgentConfigAcceptsFutureFields(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, agentconfig.FileName)
	raw := `{
  "schema_version": 1,
  "server_url": "https://borealis.example.com",
  "agent": {
    "branch": "feature/test",
    "future_liveness_gate": true
  },
  "identity": {},
  "tokens": {},
  "trust": {},
  "future_top_level": {"enabled": true}
}`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateAgentConfig(configPath); err != nil {
		t.Fatalf("validateAgentConfig failed: %v", err)
	}
}

func TestValidateAgentConfigRejectsFutureSchema(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, agentconfig.FileName)
	raw := `{
  "schema_version": 999,
  "server_url": "https://borealis.example.com",
  "agent": {},
  "identity": {},
  "tokens": {},
  "trust": {}
}`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateAgentConfig(configPath); err == nil || !strings.Contains(err.Error(), "unsupported schema_version") {
		t.Fatalf("expected unsupported schema error, got %v", err)
	}
}

func TestFreshDeployInstallDetectionSkipsUpdaterRepair(t *testing.T) {
	if isFreshDeployInstall(agentruntime.Options{ConfigPath: filepath.Join(t.TempDir(), agentconfig.FileName)}) {
		t.Fatalf("config-path only install-service should not be treated as fresh deploy")
	}
	if !isFreshDeployInstall(agentruntime.Options{ServerURL: "https://borealis.example.com"}) {
		t.Fatalf("server-url install-service should be treated as fresh deploy")
	}
	if !isFreshDeployInstall(agentruntime.Options{EnrollmentCode: "CODE"}) {
		t.Fatalf("enrollment-code install-service should be treated as fresh deploy")
	}
}

func TestInstallServiceIsInferredFromEnrollmentInputs(t *testing.T) {
	if shouldRunInstallService(false, agentruntime.Options{}) {
		t.Fatalf("empty options should not infer install-service")
	}
	if shouldRunInstallService(false, agentruntime.Options{RepoRef: "feature/test"}) {
		t.Fatalf("repo-ref alone should not infer install-service")
	}
	if !shouldRunInstallService(false, agentruntime.Options{ServerURL: "https://borealis.example.com", EnrollmentCode: "CODE"}) {
		t.Fatalf("server-url and enrollment-code should infer install-service")
	}
	if !shouldRunInstallService(true, agentruntime.Options{ConfigPath: filepath.Join(t.TempDir(), agentconfig.FileName)}) {
		t.Fatalf("explicit install-service should stay supported")
	}
}

func TestFreshDeployInstallRequiresServerURLAndEnrollmentCode(t *testing.T) {
	if err := validateFreshDeployInstall(agentruntime.Options{ServerURL: "https://borealis.example.com"}); err == nil {
		t.Fatalf("fresh deploy with missing enrollment code should fail")
	}
	if err := validateFreshDeployInstall(agentruntime.Options{EnrollmentCode: "CODE"}); err == nil {
		t.Fatalf("fresh deploy with missing server URL should fail")
	}
	if err := validateFreshDeployInstall(agentruntime.Options{ServerURL: "https://borealis.example.com", EnrollmentCode: "CODE"}); err != nil {
		t.Fatalf("fresh deploy with server URL and enrollment code failed: %v", err)
	}
}

func TestMetadataSetCLIQueuesCliSource(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, agentconfig.FileName)

	if code := runMetadataCommand(configPath, []string{"set", "1", "asset-tag-123"}); code != 0 {
		t.Fatalf("runMetadataCommand exit = %d", code)
	}

	fields, err := agentconfig.LoadQueuedMetadataFields(configPath)
	if err != nil {
		t.Fatal(err)
	}
	field, ok := fields["field_001"]
	if !ok {
		t.Fatalf("field_001 missing: %#v", fields)
	}
	if field.Source != "cli" {
		t.Fatalf("source = %q", field.Source)
	}
	if agentconfig.DecodeMetadataFieldValue(field.Value) != "asset-tag-123" {
		t.Fatalf("value = %q", agentconfig.DecodeMetadataFieldValue(field.Value))
	}
}
