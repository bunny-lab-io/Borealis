//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
)

func TestBootstrapCLIAcceptsServerIPFallback(t *testing.T) {
	opts, err := parseCLI([]string{"--server-ip-fallback", "192.168.3.251"})
	if err != nil {
		t.Fatalf("parseCLI returned error: %v", err)
	}
	if opts.ServerIPFallback != "192.168.3.251" {
		t.Fatalf("unexpected server IP fallback: %q", opts.ServerIPFallback)
	}
}

func TestBootstrapCLIAcceptsEnrollmentCodeAlias(t *testing.T) {
	opts, err := parseCLI([]string{"--enrollment-code", "CODE-123"})
	if err != nil {
		t.Fatalf("parseCLI returned error: %v", err)
	}
	if opts.SiteEnrollmentCode != "CODE-123" {
		t.Fatalf("site enrollment code = %q", opts.SiteEnrollmentCode)
	}
}

func TestNormalizeBootstrapServerIPFallbackRejectsInvalidValue(t *testing.T) {
	cfg := BootstrapConfig{ServerIPFallback: "https://192.168.3.251"}
	if err := normalizeBootstrapServerIPFallback(&cfg); err == nil || !strings.Contains(err.Error(), "server_ip_fallback") {
		t.Fatalf("expected server_ip_fallback rejection, got %v", err)
	}
}

func TestWriteGoAgentConfigPersistsServerIPFallback(t *testing.T) {
	cfg := BootstrapConfig{
		InstallDir:         t.TempDir(),
		ServerURL:          "https://internal-borealis.example.test",
		ServerIPFallback:   "192.168.3.251",
		SiteEnrollmentCode: "AAAA-BBBB",
		RepoRef:            "issue/internal-only-engine-deployment-profile",
		ReleaseChannel:     agentconfig.ReleaseChannelUnstable,
		TrustedEngineCAPEM: "",
		TrustedEngineCAB64: "",
		TimeoutSeconds:     defaultTimeoutSeconds,
	}
	if err := writeGoAgentConfig(cfg, nil); err != nil {
		t.Fatalf("writeGoAgentConfig returned error: %v", err)
	}
	loaded, err := agentconfig.Load(agentConfigPath(cfg.InstallDir))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if loaded.ServerIPFallback != "192.168.3.251" {
		t.Fatalf("server_ip_fallback = %q", loaded.ServerIPFallback)
	}
}

func TestWriteGoAgentConfigPreservesExistingIdentityAndTrust(t *testing.T) {
	installDir := t.TempDir()
	cfg := agentconfig.Default()
	cfg.ServerURL = "https://old.example.com"
	cfg.EnrollmentCode = "OLD-CODE"
	cfg.Agent.GUID = "device-guid"
	cfg.Agent.AgentID = "agent-id"
	cfg.Identity.PrivateKeyPKCS8B64 = "private-key"
	cfg.Identity.PublicKeySPKIB64 = "public-key"
	cfg.Tokens.AccessToken = "access-token"
	cfg.Tokens.RefreshToken = "refresh-token"
	cfg.Trust.ServerSigningKeySPKIB64 = "server-signing-key"
	if err := agentconfig.Save(agentConfigPath(installDir), &cfg); err != nil {
		t.Fatal(err)
	}

	if err := writeGoAgentConfig(BootstrapConfig{
		InstallDir:         installDir,
		ServerURL:          "https://borealis.example.com",
		SiteEnrollmentCode: "NEW-CODE",
		RepoRef:            "main",
		ReleaseChannel:     agentconfig.ReleaseChannelStable,
		TimeoutSeconds:     defaultTimeoutSeconds,
	}, nil); err != nil {
		t.Fatalf("writeGoAgentConfig returned error: %v", err)
	}

	loaded, err := agentconfig.Load(agentConfigPath(installDir))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ServerURL != "https://borealis.example.com" || loaded.EnrollmentCode != "NEW-CODE" {
		t.Fatalf("install inputs not updated: server=%q enrollment=%q", loaded.ServerURL, loaded.EnrollmentCode)
	}
	if loaded.Agent.GUID != "device-guid" || loaded.Agent.AgentID != "agent-id" {
		t.Fatalf("agent identity changed: guid=%q id=%q", loaded.Agent.GUID, loaded.Agent.AgentID)
	}
	if loaded.Identity.PrivateKeyPKCS8B64 != "private-key" || loaded.Identity.PublicKeySPKIB64 != "public-key" {
		t.Fatalf("device keypair changed: %#v", loaded.Identity)
	}
	if loaded.Tokens.AccessToken != "access-token" || loaded.Tokens.RefreshToken != "refresh-token" {
		t.Fatalf("tokens changed: %#v", loaded.Tokens)
	}
	if loaded.Trust.ServerSigningKeySPKIB64 != "server-signing-key" {
		t.Fatalf("server signing trust changed: %q", loaded.Trust.ServerSigningKeySPKIB64)
	}
}

func TestBootstrapDeployIntentIgnoresDefaultRepoRef(t *testing.T) {
	cfg := defaultBootstrapConfig()
	normalizeBootstrapConfig(&cfg)
	if cfg.DeployIntent {
		t.Fatalf("default bootstrap config should not imply deploy intent")
	}
	if shouldValidateFreshBootstrap(cfg) {
		t.Fatalf("default repo_ref should not require fresh install validation")
	}
}

func TestBootstrapDeployIntentFromConfigFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, bootstrapConfigFileName)
	if err := os.WriteFile(configPath, []byte(`{"server_url":"https://borealis.example.com","site_enrollment_code":"CODE"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOREALIS_AGENT_BOOTSTRAP_CONFIG", configPath)

	cfg, err := loadBootstrapConfig(cliOptions{}, false)
	if err != nil {
		t.Fatalf("loadBootstrapConfig returned error: %v", err)
	}
	if !cfg.DeployIntent {
		t.Fatalf("bootstrap config file should imply deploy intent")
	}
	if !shouldValidateFreshBootstrap(cfg) {
		t.Fatalf("deploy intent should require validation")
	}
}

func TestBootstrapCLIRejectsInsecureTLSFlag(t *testing.T) {
	for _, arg := range []string{"--insecure", "--insecure-tls-skip-verify"} {
		_, err := parseCLI([]string{arg})
		if err == nil || !strings.Contains(err.Error(), "unsupported Agent.exe argument") {
			t.Fatalf("expected %s to be rejected, got %v", arg, err)
		}
	}
}
