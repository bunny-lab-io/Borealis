package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRepoHashHandlerReturnsCachedHashForOperator(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})
	cachePath := writeRepoHashTestCache(t, "owner/repo", "main", "abc123")
	t.Setenv("BOREALIS_REPO_HASH_CACHE_FILE", cachePath)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/repo/current_hash?repo=owner/repo&branch=main&ttl=3600", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)

	repoHashHandler(auth, testAgentJWTSigner(t), &dpopVerifier{seenJTI: map[string]time.Time{}}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["sha"] != "abc123" || payload["cached"] != true || payload["source"] != "cache" {
		t.Fatalf("unexpected payload %+v", payload)
	}
}

func TestRepoHashHandlerAcceptsAgentBearer(t *testing.T) {
	guid := "2540DA38-E2B1-45B9-9113-BF7CF0E1778A"
	signer := testAgentJWTSigner(t)
	signer.now = func() time.Time { return time.Unix(1700000000, 0) }
	token, err := signer.issueAccessToken(guid, "fingerprint", 4, agentAccessTokenTTL)
	if err != nil {
		t.Fatal(err)
	}
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.deviceAuthFound = true
	store.deviceAuthRecord = deviceBearerAuthRecord{
		GUID:         guid,
		Fingerprint:  "fingerprint",
		TokenVersion: 4,
		Status:       "active",
	}
	cachePath := writeRepoHashTestCache(t, "owner/repo", "main", "def456")
	t.Setenv("BOREALIS_REPO_HASH_CACHE_FILE", cachePath)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/repo/current_hash?repo=owner/repo&branch=main&ttl=3600", nil)
	request.Header.Set("Authorization", "Bearer "+token)

	repoHashHandler(auth, signer, &dpopVerifier{seenJTI: map[string]time.Time{}}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["sha"] != "def456" || payload["cached"] != true {
		t.Fatalf("unexpected payload %+v", payload)
	}
}

func TestRepoHashForceRefreshUsesFetcherAndUpdatesCache(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})
	cachePath := writeRepoHashTestCache(t, "owner/repo", "main", "oldsha")
	t.Setenv("BOREALIS_REPO_HASH_CACHE_FILE", cachePath)

	originalFetch := repoHashFetchHead
	repoHashFetchHead = func(repo string, branch string, token string) (string, string) {
		if repo != "owner/repo" || branch != "main" || token != "secret" {
			t.Fatalf("unexpected fetch repo=%s branch=%s token=%s", repo, branch, token)
		}
		return "newsha", ""
	}
	defer func() { repoHashFetchHead = originalFetch }()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/repo/current_hash?repo=owner/repo&branch=main&refresh=force", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	request.Header.Set("X-GitHub-Token", "secret")

	repoHashHandler(auth, testAgentJWTSigner(t), &dpopVerifier{seenJTI: map[string]time.Time{}}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["sha"] != "newsha" || payload["cached"] != false || payload["source"] != "github" {
		t.Fatalf("unexpected payload %+v", payload)
	}
	cache := loadRepoHashCache(cachePath)
	if got := cache.Entries["owner/repo:main"].SHA; got != "newsha" {
		t.Fatalf("expected updated cache, got %s", got)
	}
}

func writeRepoHashTestCache(t *testing.T, repo string, branch string, sha string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "repo_hash_cache.json")
	cache := repoHashCacheFile{
		Version: 1,
		Entries: map[string]repoHashCacheEntry{
			repo + ":" + branch: {
				SHA: sha,
				TS:  float64(time.Now().UnixNano()) / float64(time.Second),
			},
		},
	}
	data, err := json.Marshal(cache)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
