package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestAgentReleaseChannelsUpdateRefreshesArtifacts(t *testing.T) {
	tmpRoot := t.TempDir()
	settingsPath := filepath.Join(tmpRoot, "Engine", "Services", "api-backend", "config", "agent_release_channels.json")
	t.Setenv("BOREALIS_PROJECT_ROOT", tmpRoot)
	t.Setenv("BOREALIS_AGENT_RELEASE_CHANNELS_PATH", settingsPath)

	sourceZip := agentReleaseTestSourceZip(t)
	stableSHA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	unstableSHA := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo":
			writeTestJSON(t, w, map[string]any{"default_branch": "main"})
		case "/repos/owner/repo/releases/latest":
			writeTestJSON(t, w, map[string]any{
				"tag_name":     "v1.0.0",
				"name":         "v1.0.0",
				"published_at": "2026-06-01T00:00:00Z",
				"zipball_url":  server.URL + "/stable.zip",
			})
		case "/repos/owner/repo/commits/v1.0.0":
			writeTestJSON(t, w, map[string]any{
				"sha": stableSHA,
				"commit": map[string]any{
					"author": map[string]any{"date": "2026-06-01T00:00:00Z"},
				},
			})
		case "/repos/owner/repo/commits/main":
			writeTestJSON(t, w, map[string]any{
				"sha": unstableSHA,
				"commit": map[string]any{
					"author": map[string]any{"date": "2026-06-02T00:00:00Z"},
				},
			})
		case "/stable.zip", "/repos/owner/repo/zipball/" + unstableSHA:
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(sourceZip)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("BOREALIS_GITHUB_API_BASE_URL", server.URL)

	auth, _ := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	mux := http.NewServeMux()
	registerAgentReleaseChannelRoutes(mux, auth, http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/server/agent-release-channels", bytes.NewBufferString(`{"default_channel":"unstable","repo":"owner/repo"}`))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	request.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["default_channel"] != "unstable" {
		t.Fatalf("expected unstable default, got %#v", payload["default_channel"])
	}
	if payload["last_refresh_error"] != "" {
		t.Fatalf("expected no refresh error, got %#v", payload["last_refresh_error"])
	}
	github := payload["github"].(map[string]any)
	if github["repo"] != "owner/repo" || github["default_branch"] != "main" {
		t.Fatalf("unexpected github settings %+v", github)
	}
	channels := payload["channels"].(map[string]any)
	stable := channels["stable"].(map[string]any)
	artifactPath := cleanText(stable["artifact_path"])
	if artifactPath == "" {
		t.Fatalf("expected stable artifact path")
	}
	if _, err := os.Stat(artifactPath); err != nil {
		t.Fatalf("expected artifact at %s: %v", artifactPath, err)
	}
	if err := validateAgentReleaseArtifact(artifactPath); err != nil {
		t.Fatalf("expected valid artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(agentUpdateCacheRoot(), cleanText(stable["artifact_id"])+".json")); err != nil {
		t.Fatalf("expected cache manifest: %v", err)
	}

	refreshRecorder := httptest.NewRecorder()
	refreshRequest := httptest.NewRequest(http.MethodPost, "/api/server/agent-release-channels/refresh", nil)
	refreshRequest.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(refreshRecorder, refreshRequest)
	if refreshRecorder.Code != http.StatusOK {
		t.Fatalf("expected refresh 200, got %d body=%s", refreshRecorder.Code, refreshRecorder.Body.String())
	}
}

func agentReleaseTestSourceZip(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, body := range map[string][]byte{
		"repo/Data/Agent/dist/windows-amd64/Agent.exe": []byte("windows-agent"),
		"repo/Data/Agent/dist/linux-amd64/Agent":       []byte("linux-agent"),
	} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func writeTestJSON(t *testing.T, w http.ResponseWriter, payload map[string]any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatal(err)
	}
}
