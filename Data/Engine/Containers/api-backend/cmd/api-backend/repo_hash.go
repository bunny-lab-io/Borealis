package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultRepoHashRepo       = "bunny-lab-io/Borealis"
	defaultRepoHashBranch     = "main"
	defaultRepoHashTTLSeconds = 60
	minRepoHashTTLSeconds     = 30
	maxRepoHashTTLSeconds     = 3600
)

var repoHashCacheMu sync.Mutex
var repoHashFetchHead = fetchGitHubRepoHead

type repoHashCacheFile struct {
	Version int                            `json:"version"`
	Entries map[string]repoHashCacheEntry `json:"entries"`
}

type repoHashCacheEntry struct {
	SHA string  `json:"sha"`
	TS  float64 `json:"ts"`
}

func registerRepoHashRoutes(mux *http.ServeMux, auth *authService, signer *agentJWTSigner, dpop *dpopVerifier) {
	mux.HandleFunc("GET /api/repo/current_hash", repoHashHandler(auth, signer, dpop))
}

func repoHashHandler(auth *authService, signer *agentJWTSigner, dpop *dpopVerifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !repoHashAuthorized(r.Context(), r, auth, signer, dpop) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		payload, status := currentRepoHashPayload(r)
		writeJSON(w, status, payload)
	}
}

func repoHashAuthorized(ctx context.Context, r *http.Request, auth *authService, signer *agentJWTSigner, dpop *dpopVerifier) bool {
	if _, err := auth.currentProfile(ctx, r); err == nil {
		return true
	}
	if _, failure := authenticateDeviceBearer(ctx, r, auth, signer, dpop); failure == nil {
		return true
	}
	return false
}

func currentRepoHashPayload(r *http.Request) (map[string]any, int) {
	query := r.URL.Query()
	repo := firstText(strings.TrimSpace(query.Get("repo")), envDefault("BOREALIS_REPO", defaultRepoHashRepo))
	branch := firstText(strings.TrimSpace(query.Get("branch")), envDefault("BOREALIS_REPO_BRANCH", defaultRepoHashBranch))
	if !strings.Contains(repo, "/") {
		return map[string]any{"error": "repo must be in the form owner/name"}, http.StatusBadRequest
	}
	ttl := normalizeRepoHashTTL(query.Get("ttl"))
	forceRefresh := parseRepoHashRefresh(query.Get("refresh"))
	cachePath := repoHashCachePath()
	cacheKey := repo + ":" + branch
	now := time.Now()

	repoHashCacheMu.Lock()
	cache := loadRepoHashCache(cachePath)
	cached := cache.Entries[cacheKey]
	cachedSHA := strings.TrimSpace(cached.SHA)
	cachedAge := now.Sub(time.Unix(int64(cached.TS), 0)).Seconds()
	if cachedSHA != "" && !forceRefresh && cachedAge >= 0 && cachedAge < float64(ttl) {
		repoHashCacheMu.Unlock()
		return buildRepoHashPayload(repo, branch, cachedSHA, true, cachedAge, "cache", ""), http.StatusOK
	}
	repoHashCacheMu.Unlock()

	sha, errText := repoHashFetchHead(repo, branch, strings.TrimSpace(r.Header.Get("X-GitHub-Token")))
	if sha != "" {
		repoHashCacheMu.Lock()
		cache = loadRepoHashCache(cachePath)
		cache.Entries[cacheKey] = repoHashCacheEntry{SHA: sha, TS: float64(now.UnixNano()) / float64(time.Second)}
		saveRepoHashCache(cachePath, cache)
		repoHashCacheMu.Unlock()
		return buildRepoHashPayload(repo, branch, sha, false, 0, "github", ""), http.StatusOK
	}
	if cachedSHA != "" {
		status := http.StatusOK
		return buildRepoHashPayload(repo, branch, cachedSHA, true, cachedAge, "cache-stale", firstText(errText, "using cached value")), status
	}
	return buildRepoHashPayload(repo, branch, "", false, 0, "github", firstText(errText, "unable to resolve repository head")), http.StatusServiceUnavailable
}

func normalizeRepoHashTTL(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		value = strings.TrimSpace(os.Getenv("BOREALIS_REPO_HASH_REFRESH"))
	}
	ttl := parseIntDefault(value, defaultRepoHashTTLSeconds)
	if ttl < minRepoHashTTLSeconds {
		return minRepoHashTTLSeconds
	}
	if ttl > maxRepoHashTTLSeconds {
		return maxRepoHashTTLSeconds
	}
	return ttl
}

func parseRepoHashRefresh(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "force", "refresh":
		return true
	default:
		return false
	}
}

func repoHashCachePath() string {
	if path := strings.TrimSpace(os.Getenv("BOREALIS_REPO_HASH_CACHE_FILE")); path != "" {
		return path
	}
	if path := strings.TrimSpace(os.Getenv("BOREALIS_REPO_HASH_CACHE_PATH")); path != "" {
		return path
	}
	return "/opt/Borealis/Engine/Services/api-backend/logs/cache/repo_hash_cache.json"
}

func loadRepoHashCache(path string) repoHashCacheFile {
	cache := repoHashCacheFile{Version: 1, Entries: map[string]repoHashCacheEntry{}}
	data, err := os.ReadFile(path)
	if err != nil {
		return cache
	}
	if err := json.Unmarshal(data, &cache); err != nil {
		return repoHashCacheFile{Version: 1, Entries: map[string]repoHashCacheEntry{}}
	}
	if cache.Entries == nil {
		cache.Entries = map[string]repoHashCacheEntry{}
	}
	if cache.Version == 0 {
		cache.Version = 1
	}
	return cache
}

func saveRepoHashCache(path string, cache repoHashCacheFile) {
	if cache.Entries == nil {
		cache.Entries = map[string]repoHashCacheEntry{}
	}
	cache.Version = 1
	data, err := json.Marshal(cache)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmpPath, path)
}

func buildRepoHashPayload(repo string, branch string, sha string, cached bool, ageSeconds float64, source string, errText string) map[string]any {
	var shaValue any
	if strings.TrimSpace(sha) != "" {
		shaValue = strings.TrimSpace(sha)
	}
	var ageValue any
	if cached {
		ageValue = ageSeconds
	}
	payload := map[string]any{
		"repo":        repo,
		"branch":      branch,
		"sha":         shaValue,
		"cached":      cached,
		"age_seconds": ageValue,
		"source":      source,
	}
	if errText != "" {
		payload["error"] = errText
	}
	return payload
}

func fetchGitHubRepoHead(repo string, branch string, token string) (string, string) {
	requestURL := "https://api.github.com/repos/" + repo + "/commits/" + url.PathEscape(branch)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return "", fmt.Sprintf("GitHub REST API repo head lookup unexpected error: %v", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Borealis-Engine")
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Sprintf("GitHub REST API repo head lookup raised: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Sprintf("GitHub REST API repo head lookup failed: HTTP %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Sprintf("GitHub REST API repo head decode error: %v", err)
	}
	sha := strings.TrimSpace(cleanText(body["sha"]))
	if sha == "" {
		commit, _ := body["commit"].(map[string]any)
		sha = strings.TrimSpace(cleanText(commit["sha"]))
	}
	if sha == "" {
		return "", "GitHub REST API repo head missing commit SHA"
	}
	return sha, ""
}
