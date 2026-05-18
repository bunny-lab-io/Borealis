//go:build !windows

package main

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
)

func TestRunLinuxRepoRefUpdateCheckPersistsMainFallbackWhenCurrent(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), agentconfig.FileName)
	cfg := agentconfig.Default()
	cfg.Agent.ReleaseChannel = agentconfig.ReleaseChannelSource
	cfg.Agent.Branch = "feature/deleted"
	cfg.Agent.InstalledBuildID = "main-sha"
	if err := agentconfig.Save(configPath, &cfg); err != nil {
		t.Fatal(err)
	}
	restarted := false
	originalRestart := restartLinuxAgentService
	restartLinuxAgentService = func() error {
		restarted = true
		return nil
	}
	t.Cleanup(func() {
		restartLinuxAgentService = originalRestart
	})
	resolver := func(ref string) (string, error) {
		switch ref {
		case "feature/deleted":
			return "", &githubRefHTTPError{Ref: ref, StatusCode: http.StatusUnprocessableEntity, Body: "No commit found for SHA: feature/deleted"}
		case agentconfig.DefaultBranch:
			return "main-sha", nil
		default:
			t.Fatalf("unexpected ref %q", ref)
			return "", nil
		}
	}

	if err := runLinuxRepoRefUpdateCheckWithResolver(context.Background(), configPath, &cfg, cfg.Agent.Branch, cfg.Agent.InstalledBuildID, resolver); err != nil {
		t.Fatal(err)
	}
	loaded, err := agentconfig.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Agent.ReleaseChannel != agentconfig.ReleaseChannelSource {
		t.Fatalf("release_channel = %q", loaded.Agent.ReleaseChannel)
	}
	if loaded.Agent.Branch != agentconfig.DefaultBranch {
		t.Fatalf("branch = %q", loaded.Agent.Branch)
	}
	if loaded.Agent.InstalledBuildID != "main-sha" {
		t.Fatalf("installed_build_id = %q", loaded.Agent.InstalledBuildID)
	}
	if !restarted {
		t.Fatalf("expected service restart after fallback")
	}
}
