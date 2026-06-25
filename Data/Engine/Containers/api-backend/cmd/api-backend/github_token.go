package main

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type githubTokenManagementStore interface {
	loadGithubToken(ctx context.Context, secret authSecretService) (githubTokenRecord, error)
	storeGithubToken(ctx context.Context, secret authSecretService, token string) error
}

type githubTokenRecord struct {
	Token         string
	ResetRequired bool
	ResetAt       int64
}

type githubTokenVerification struct {
	Valid     bool
	Message   string
	Status    string
	RateLimit any
	Error     any
}

var githubTokenVerifier = verifyGitHubToken

func githubTokenHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleGithubTokenGet(w, r, auth)
		case http.MethodPost:
			handleGithubTokenPost(w, r, auth)
		default:
			writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
		}
	}
}

func handleGithubTokenGet(w http.ResponseWriter, r *http.Request, auth *authService) {
	if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
		failure.write(w)
		return
	}
	if auth.aegis == nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "aegis_unavailable"})
		return
	}
	store, ok := auth.store.(githubTokenManagementStore)
	if !ok {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "github_token_unavailable"})
		return
	}
	ctx, cancel := requestTimeout(r.Context(), auth)
	defer cancel()

	aegisStatus, err := auth.aegis.status(ctx)
	if err != nil {
		writeJSON(w, protectedSecretErrorStatus(err), protectedSecretErrorBody(err))
		return
	}
	configured := boolFromPayload(aegisStatus["configured"])
	locked := boolFromPayload(aegisStatus["locked"])
	if configured && locked {
		writeJSON(w, http.StatusOK, lockedGithubTokenPayload(configured, locked))
		return
	}

	record, err := store.loadGithubToken(ctx, auth.aegis)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	verification := githubTokenVerifier(record.Token)
	writeJSON(w, http.StatusOK, githubTokenResponse(record.Token, verification, configured, locked, record.ResetRequired, record.ResetAt))
}

func handleGithubTokenPost(w http.ResponseWriter, r *http.Request, auth *authService) {
	if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
		failure.write(w)
		return
	}
	if body, status, blocked := protectedSecretMutationBlock(r.Context(), auth); blocked {
		writeJSON(w, status, body)
		return
	}
	store, ok := auth.store.(githubTokenManagementStore)
	if !ok {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "github_token_unavailable"})
		return
	}
	payload, err := readCredentialJSONMap(w, r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
		return
	}
	token := strings.TrimSpace(secretPlainText(payload["token"]))
	ctx, cancel := requestTimeout(r.Context(), auth)
	defer cancel()
	if err := store.storeGithubToken(ctx, auth.aegis, token); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	verification := githubTokenVerifier(token)
	if token != "" {
		refreshDefaultRepoHashWithToken(token)
	}
	writeJSON(w, http.StatusOK, githubTokenResponse(token, verification, true, false, false, 0))
}

func (s *postgresOperatorStore) loadGithubToken(ctx context.Context, secret authSecretService) (githubTokenRecord, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return githubTokenRecord{}, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	var token sql.NullString
	var resetRequired sql.NullInt64
	var resetAt sql.NullInt64
	err = conn.QueryRowContext(ctx, `
		SELECT token, reset_required, reset_at
		  FROM engine.github_token
		 LIMIT 1
	`).Scan(&token, &resetRequired, &resetAt)
	if errors.Is(err, sql.ErrNoRows) {
		return githubTokenRecord{}, nil
	}
	if err != nil {
		return githubTokenRecord{}, err
	}
	record := githubTokenRecord{
		ResetRequired: nullInt(resetRequired) != 0,
		ResetAt:       nullInt(resetAt),
	}
	if strings.TrimSpace(nullString(token)) != "" {
		if secret == nil {
			return record, errAegisLocked
		}
		plain, err := secret.decryptSecretText(ctx, token.String)
		if err != nil {
			return record, err
		}
		record.Token = strings.TrimSpace(plain)
	}
	return record, nil
}

func (s *postgresOperatorStore) storeGithubToken(ctx context.Context, secret authSecretService, token string) error {
	var encrypted string
	var err error
	if token != "" {
		if secret == nil {
			return errAegisLocked
		}
		encrypted, err = secret.encryptSecretText(ctx, token)
		if err != nil {
			return err
		}
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `DELETE FROM engine.github_token`); err != nil {
		return err
	}
	if encrypted != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO engine.github_token(token, reset_required, reset_at) VALUES ($1, 0, NULL)`, encrypted); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func githubTokenResponse(token string, verification githubTokenVerification, configured bool, locked bool, resetRequired bool, resetAt int64) map[string]any {
	message := verification.Message
	status := verification.Status
	if resetRequired && token == "" {
		message = "Aegis Cipher reset removed the stored GitHub token. Please re-enter the Personal Access Token."
		status = "reset_required"
	} else {
		if message == "" {
			if token == "" {
				message = "API Token Not Configured"
			} else {
				message = "API Token Invalid"
			}
		}
		if status == "" {
			if token == "" {
				status = "missing"
			} else {
				status = "unknown"
			}
		}
	}
	return map[string]any{
		"token":          token,
		"has_token":      token != "",
		"valid":          verification.Valid,
		"message":        message,
		"status":         status,
		"rate_limit":     verification.RateLimit,
		"error":          verification.Error,
		"checked_at":     time.Now().Unix(),
		"configured":     configured,
		"locked":         locked,
		"reset_required": resetRequired,
		"reset_at":       resetAt,
	}
}

func lockedGithubTokenPayload(configured bool, locked bool) map[string]any {
	return map[string]any{
		"token":          "",
		"has_token":      false,
		"valid":          false,
		"message":        "Aegis Cipher is locked. Enter it from Access Management > Credentials to manage the GitHub token.",
		"status":         "locked",
		"rate_limit":     nil,
		"error":          nil,
		"checked_at":     time.Now().Unix(),
		"configured":     configured,
		"locked":         locked,
		"reset_required": false,
		"reset_at":       int64(0),
	}
}

func verifyGitHubToken(token string) githubTokenVerification {
	if strings.TrimSpace(token) == "" {
		return githubTokenVerification{
			Valid:     false,
			Message:   "API Token Not Configured",
			Status:    "missing",
			RateLimit: nil,
		}
	}
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/repos/"+defaultRepoHashRepo+"/branches/"+defaultRepoHashBranch, nil)
	if err != nil {
		return githubTokenVerification{Valid: false, Message: "API Token validation error: " + err.Error(), Status: "error", Error: err.Error()}
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Borealis-Engine")
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return githubTokenVerification{Valid: false, Message: "API Token validation error: " + err.Error(), Status: "error", RateLimit: nil, Error: err.Error()}
	}
	defer resp.Body.Close()
	limitValue := githubRateLimitValue(resp.Header.Get("X-RateLimit-Limit"))
	if resp.StatusCode == http.StatusOK {
		if limit, ok := limitValue.(int); ok && limit >= 5000 {
			return githubTokenVerification{Valid: true, Message: "API Authentication Successful", Status: "ok", RateLimit: limit}
		}
		return githubTokenVerification{
			Valid:     false,
			Message:   "API Token Invalid",
			Status:    "insufficient",
			RateLimit: limitValue,
			Error:     "Authenticated request did not elevate GitHub rate limits",
		}
	}
	snippet := githubResponseSnippet(resp.Body)
	if resp.StatusCode == http.StatusUnauthorized {
		return githubTokenVerification{
			Valid:     false,
			Message:   "API Token Invalid",
			Status:    "invalid",
			RateLimit: limitValue,
			Error:     snippet,
		}
	}
	return githubTokenVerification{
		Valid:     false,
		Message:   "GitHub API error (HTTP " + strconv.Itoa(resp.StatusCode) + ")",
		Status:    "error",
		RateLimit: limitValue,
		Error:     snippet,
	}
}

func githubRateLimitValue(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return nil
	}
	return parsed
}

func githubResponseSnippet(body io.Reader) string {
	data, _ := io.ReadAll(io.LimitReader(body, 200))
	return string(data)
}

func refreshDefaultRepoHashWithToken(token string) {
	repo := envDefault("BOREALIS_REPO", defaultRepoHashRepo)
	branch := envDefault("BOREALIS_REPO_BRANCH", defaultRepoHashBranch)
	if !strings.Contains(repo, "/") {
		return
	}
	sha, _ := repoHashFetchHead(repo, branch, token)
	if strings.TrimSpace(sha) == "" {
		return
	}
	cachePath := repoHashCachePath()
	cacheKey := repo + ":" + branch
	now := time.Now()
	repoHashCacheMu.Lock()
	defer repoHashCacheMu.Unlock()
	cache := loadRepoHashCache(cachePath)
	cache.Entries[cacheKey] = repoHashCacheEntry{SHA: sha, TS: float64(now.UnixNano()) / float64(time.Second)}
	saveRepoHashCache(cachePath, cache)
}
