//go:build windows

package main

import (
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

func TestBootstrapCLIRejectsInsecureTLSFlag(t *testing.T) {
	for _, arg := range []string{"--insecure", "--insecure-tls-skip-verify"} {
		_, err := parseCLI([]string{arg})
		if err == nil || !strings.Contains(err.Error(), "unsupported Agent.exe argument") {
			t.Fatalf("expected %s to be rejected, got %v", arg, err)
		}
	}
}
