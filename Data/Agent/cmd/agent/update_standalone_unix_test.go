//go:build !windows

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/bunny-lab-io/borealis/go-agent/internal/auth"
	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
)

func TestResolveEngineRepoRefSHAUsesEngineCurrentHashAPI(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/api/repo/current_hash" {
			t.Errorf("path = %q", r.URL.Path)
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
		if got := r.URL.Query().Get("branch"); got != "feature/agent-metadata-fields" {
			t.Errorf("branch query = %q", got)
			http.Error(w, "bad branch", http.StatusBadRequest)
			return
		}
		if got := r.URL.Query().Get("ttl"); got != "300" {
			t.Errorf("ttl query = %q", got)
			http.Error(w, "bad ttl", http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer agent-token" {
			t.Errorf("authorization header = %q", got)
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"sha":"ABCDEF123456"}`)
	}))
	defer server.Close()

	cfg := agentconfig.Default()
	cfg.ServerURL = server.URL
	cfg.Agent.GUID = "GUID-TEST-0001"
	cfg.Tokens.AccessToken = "agent-token"
	cfg.Tokens.AccessExpiresAt = time.Now().Add(time.Hour).Unix()
	cfg.Tokens.RefreshToken = "refresh-token"
	configPath := filepath.Join(t.TempDir(), agentconfig.FileName)
	if err := agentconfig.Save(configPath, &cfg); err != nil {
		t.Fatal(err)
	}

	client, err := auth.NewClient(configPath, &cfg, "system")
	if err != nil {
		t.Fatal(err)
	}

	sha, err := resolveEngineRepoRefSHA(context.Background(), client, "feature/agent-metadata-fields")
	if err != nil {
		t.Fatalf("resolveEngineRepoRefSHA failed: %v", err)
	}
	if sha != "ABCDEF123456" {
		t.Fatalf("sha = %q", sha)
	}
	if requests != 1 {
		t.Fatalf("requests = %d", requests)
	}
}
