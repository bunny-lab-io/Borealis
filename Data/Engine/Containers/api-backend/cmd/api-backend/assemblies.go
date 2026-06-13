package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	assemblyDomainOfficial   = "official"
	assemblyDomainCommunity  = "community"
	assemblyDomainUser       = "user"
	assemblyDocumentMaxBytes = int64(950_000_000)
)

type assemblyStore interface {
	listAssemblies(ctx context.Context, filter assemblyListFilter) ([]map[string]any, []map[string]any, error)
	getAssembly(ctx context.Context, assemblyGUID string, includePayload bool) (map[string]any, bool, error)
	createAssembly(ctx context.Context, payload map[string]any) (map[string]any, int, error)
	updateAssembly(ctx context.Context, assemblyGUID string, payload map[string]any) (map[string]any, int, error)
	deleteAssembly(ctx context.Context, assemblyGUID string) (map[string]any, int, error)
	cloneAssembly(ctx context.Context, assemblyGUID string, payload map[string]any) (map[string]any, int, error)
	importAssembly(ctx context.Context, payload map[string]any) (map[string]any, int, error)
}

type assemblyCatalogStore interface {
	assemblyStore
	listOfficialCatalogState(ctx context.Context) (map[string]officialCatalogState, error)
	upsertOfficialCatalogState(ctx context.Context, state officialCatalogState) error
	deleteOfficialCatalogState(ctx context.Context, assemblyGUID string) error
}

type assemblyListFilter struct {
	Domain       string
	AssemblyType string
}

type assemblyDBRow struct {
	Domain           string
	AssemblyGUID     string
	DisplayName      string
	Summary          string
	AssemblyType     string
	AssemblySubtype  string
	PayloadJSON      string
	SourceRepo       string
	SourcePath       string
	SourceVersion    string
	ContentHash      string
	PayloadSizeBytes int64
	CreatedAt        string
	UpdatedAt        string
}

type goDevModeManager struct {
	mu      sync.Mutex
	entries map[string]time.Time
	ttl     time.Duration
	now     func() time.Time
}

func newGoDevModeManager() *goDevModeManager {
	ttl := 15 * time.Minute
	if raw := strings.TrimSpace(osEnv("BOREALIS_DEV_MODE_TTL_SECONDS")); raw != "" {
		if parsed, err := time.ParseDuration(raw + "s"); err == nil && parsed > 0 {
			ttl = parsed
		}
	}
	if ttl < time.Minute {
		ttl = time.Minute
	}
	return &goDevModeManager{
		entries: map[string]time.Time{},
		ttl:     ttl,
		now:     time.Now,
	}
}

func (m *goDevModeManager) key(profile operatorProfile, r *http.Request) string {
	if m == nil {
		return ""
	}
	token, err := extractAuthToken(r)
	if err != nil || strings.TrimSpace(token) == "" {
		return ""
	}
	username := strings.ToLower(strings.TrimSpace(profile.Username))
	if username == "" {
		return ""
	}
	return username + "\x00" + token
}

func (m *goDevModeManager) enable(profile operatorProfile, r *http.Request) bool {
	key := m.key(profile, r)
	if key == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked()
	m.entries[key] = m.now().Add(m.ttl)
	return true
}

func (m *goDevModeManager) disable(profile operatorProfile, r *http.Request) bool {
	key := m.key(profile, r)
	if key == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, key)
	return false
}

func (m *goDevModeManager) enabled(profile operatorProfile, r *http.Request) bool {
	key := m.key(profile, r)
	if key == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked()
	expiresAt, ok := m.entries[key]
	return ok && m.now().Before(expiresAt)
}

func (m *goDevModeManager) pruneLocked() {
	now := m.now()
	for key, expiresAt := range m.entries {
		if !now.Before(expiresAt) {
			delete(m.entries, key)
		}
	}
}

func registerAssemblyRoutes(mux *http.ServeMux, auth *authService, fallback http.Handler, legacyURL *url.URL) {
	mux.HandleFunc("/api/assemblies", assembliesRootHandler(auth, fallback, legacyURL))
	mux.HandleFunc("/api/assemblies/", assembliesSubtreeHandler(auth, fallback, legacyURL))
}

func assembliesRootHandler(auth *authService, fallback http.Handler, legacyURL *url.URL) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimRight(r.URL.Path, "/") != "/api/assemblies" {
			fallback.ServeHTTP(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			if assemblyTruthy(r.URL.Query().Get("refresh_catalog")) || assemblyTruthy(r.URL.Query().Get("force_catalog_refresh")) {
				assemblyCatalogRefresh(w, r, auth, legacyURL)
				return
			}
			assemblyList(w, r, auth)
		case http.MethodPost:
			assemblyCreate(w, r, auth, legacyURL)
		default:
			proxyFallbackOrMethodNotAllowed(w, r, fallback, strings.Join([]string{http.MethodGet, http.MethodPost}, ", "))
		}
	}
}

func assembliesSubtreeHandler(auth *authService, fallback http.Handler, legacyURL *url.URL) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		parts := assemblyPathParts(r.URL.Path)
		if len(parts) == 2 && parts[0] == "dev-mode" && parts[1] == "switch" && r.Method == http.MethodPost {
			assemblyDevModeSwitch(w, r, auth)
			return
		}
		if len(parts) == 2 && parts[0] == "dev-mode" && parts[1] == "write" && r.Method == http.MethodPost {
			assemblyDevModeWrite(w, r, auth, legacyURL)
			return
		}
		if len(parts) == 1 && parts[0] == "import" && r.Method == http.MethodPost {
			assemblyImport(w, r, auth, legacyURL)
			return
		}
		if len(parts) == 2 && parts[0] == "official" && parts[1] == "update-all" {
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, http.MethodPost)
				return
			}
			assemblyCatalogUpdateAll(w, r, auth, legacyURL)
			return
		}
		if len(parts) == 2 && parts[1] == "official-update" {
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, http.MethodPost)
				return
			}
			assemblyCatalogUpdateOne(w, r, auth, legacyURL, parts[0])
			return
		}
		if len(parts) == 2 && parts[1] == "export" && r.Method == http.MethodGet {
			assemblyExport(w, r, auth, parts[0])
			return
		}
		if len(parts) == 2 && parts[1] == "clone" && r.Method == http.MethodPost {
			assemblyClone(w, r, auth, legacyURL, parts[0])
			return
		}
		if len(parts) == 1 && parts[0] != "" {
			switch r.Method {
			case http.MethodGet:
				assemblyDetail(w, r, auth, parts[0])
			case http.MethodPut:
				assemblyUpdate(w, r, auth, legacyURL, parts[0])
			case http.MethodDelete:
				assemblyDelete(w, r, auth, legacyURL, parts[0])
			default:
				proxyFallbackOrMethodNotAllowed(w, r, fallback, strings.Join([]string{http.MethodGet, http.MethodPut, http.MethodDelete}, ", "))
			}
			return
		}
		fallback.ServeHTTP(w, r)
	}
}

func assemblyList(w http.ResponseWriter, r *http.Request, auth *authService) {
	_, store, ok := assemblyRequestContext(w, r, auth)
	if !ok {
		return
	}
	filter := assemblyListFilter{
		Domain:       assemblyNormalizeDomain(r.URL.Query().Get("domain")),
		AssemblyType: strings.ToLower(strings.TrimSpace(r.URL.Query().Get("type"))),
	}
	ctx, cancel := requestTimeout(r.Context(), auth)
	defer cancel()
	items, queue, err := store.listAssemblies(ctx, filter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"queue": queue,
		"official_catalog": map[string]any{
			"repo_url":           "https://github.com/bunny-lab-io/Aurora",
			"source":             "bundled",
			"available":          false,
			"update_count":       0,
			"new_assembly_count": 0,
			"actionable_count":   0,
		},
	})
}

func assemblyDetail(w http.ResponseWriter, r *http.Request, auth *authService, assemblyGUID string) {
	_, store, ok := assemblyRequestContext(w, r, auth)
	if !ok {
		return
	}
	ctx, cancel := requestTimeout(r.Context(), auth)
	defer cancel()
	item, found, err := store.getAssembly(ctx, assemblyGUID, true)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func assemblyCreate(w http.ResponseWriter, r *http.Request, auth *authService, legacyURL *url.URL) {
	profile, store, ok := assemblyRequestContext(w, r, auth)
	if !ok {
		return
	}
	payload, ok := readAssemblyJSON(w, r)
	if !ok {
		return
	}
	domain := assemblyNormalizeDomain(payload["domain"])
	if domain == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid domain"})
		return
	}
	if !assemblyMutationAllowed(w, r, auth, profile, domain) {
		return
	}
	ctx, cancel := requestTimeout(r.Context(), auth)
	defer cancel()
	item, status, err := store.createAssembly(ctx, payload)
	if err != nil {
		writeJSON(w, statusOrDefault(status, http.StatusBadRequest), map[string]any{"error": err.Error()})
		return
	}
	_ = notifyLegacyAssemblyCache(ctx, auth, legacyURL, "reload")
	writeJSON(w, statusOrDefault(status, http.StatusCreated), item)
}

func assemblyUpdate(w http.ResponseWriter, r *http.Request, auth *authService, legacyURL *url.URL, assemblyGUID string) {
	profile, store, ok := assemblyRequestContext(w, r, auth)
	if !ok {
		return
	}
	ctx, cancel := requestTimeout(r.Context(), auth)
	defer cancel()
	existing, found, err := store.getAssembly(ctx, assemblyGUID, false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	domain := assemblyNormalizeDomain(existing["source"])
	if !assemblyMutationAllowed(w, r, auth, profile, domain) {
		return
	}
	payload, ok := readAssemblyJSON(w, r)
	if !ok {
		return
	}
	item, status, err := store.updateAssembly(ctx, assemblyGUID, payload)
	if err != nil {
		writeJSON(w, statusOrDefault(status, http.StatusBadRequest), map[string]any{"error": err.Error()})
		return
	}
	_ = notifyLegacyAssemblyCache(ctx, auth, legacyURL, "reload")
	writeJSON(w, statusOrDefault(status, http.StatusOK), item)
}

func assemblyDelete(w http.ResponseWriter, r *http.Request, auth *authService, legacyURL *url.URL, assemblyGUID string) {
	profile, store, ok := assemblyRequestContext(w, r, auth)
	if !ok {
		return
	}
	ctx, cancel := requestTimeout(r.Context(), auth)
	defer cancel()
	existing, found, err := store.getAssembly(ctx, assemblyGUID, false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	domain := assemblyNormalizeDomain(existing["source"])
	if !assemblyMutationAllowed(w, r, auth, profile, domain) {
		return
	}
	payload, status, err := store.deleteAssembly(ctx, assemblyGUID)
	if err != nil {
		writeJSON(w, statusOrDefault(status, http.StatusBadRequest), map[string]any{"error": err.Error()})
		return
	}
	_ = notifyLegacyAssemblyCache(ctx, auth, legacyURL, "reload")
	writeJSON(w, statusOrDefault(status, http.StatusAccepted), payload)
}

func assemblyClone(w http.ResponseWriter, r *http.Request, auth *authService, legacyURL *url.URL, assemblyGUID string) {
	profile, store, ok := assemblyRequestContext(w, r, auth)
	if !ok {
		return
	}
	payload, ok := readAssemblyJSON(w, r)
	if !ok {
		return
	}
	domain := assemblyNormalizeDomain(payload["target_domain"])
	if domain == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid target domain"})
		return
	}
	if !assemblyMutationAllowed(w, r, auth, profile, domain) {
		return
	}
	ctx, cancel := requestTimeout(r.Context(), auth)
	defer cancel()
	item, status, err := store.cloneAssembly(ctx, assemblyGUID, payload)
	if err != nil {
		writeJSON(w, statusOrDefault(status, http.StatusBadRequest), map[string]any{"error": err.Error()})
		return
	}
	_ = notifyLegacyAssemblyCache(ctx, auth, legacyURL, "reload")
	writeJSON(w, statusOrDefault(status, http.StatusCreated), item)
}

func assemblyImport(w http.ResponseWriter, r *http.Request, auth *authService, legacyURL *url.URL) {
	profile, store, ok := assemblyRequestContext(w, r, auth)
	if !ok {
		return
	}
	payload, ok := readAssemblyJSON(w, r)
	if !ok {
		return
	}
	domain := assemblyNormalizeDomain(payload["domain"])
	if domain == "" {
		domain = assemblyDomainUser
		payload["domain"] = domain
	}
	if !assemblyMutationAllowed(w, r, auth, profile, domain) {
		return
	}
	ctx, cancel := requestTimeout(r.Context(), auth)
	defer cancel()
	item, status, err := store.importAssembly(ctx, payload)
	if err != nil {
		writeJSON(w, statusOrDefault(status, http.StatusBadRequest), map[string]any{"error": err.Error()})
		return
	}
	_ = notifyLegacyAssemblyCache(ctx, auth, legacyURL, "reload")
	writeJSON(w, statusOrDefault(status, http.StatusCreated), item)
}

func assemblyExport(w http.ResponseWriter, r *http.Request, auth *authService, assemblyGUID string) {
	_, store, ok := assemblyRequestContext(w, r, auth)
	if !ok {
		return
	}
	ctx, cancel := requestTimeout(r.Context(), auth)
	defer cancel()
	item, found, err := store.getAssembly(ctx, assemblyGUID, true)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	exported := assemblyExportDocument(item)
	writeJSON(w, http.StatusOK, exported)
}

func assemblyDevModeSwitch(w http.ResponseWriter, r *http.Request, auth *authService) {
	profile, failure := requireAdmin(r.Context(), auth, r)
	if failure != nil {
		failure.write(w)
		return
	}
	fullProfile, err := auth.currentProfile(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
		return
	}
	body, ok := readAssemblyJSON(w, r)
	if !ok {
		return
	}
	enabled := assemblyTruthy(body["enabled"])
	if auth.devMode == nil {
		auth.devMode = newGoDevModeManager()
	}
	if enabled {
		enabled = auth.devMode.enable(fullProfile, r)
	} else {
		auth.devMode.disable(fullProfile, r)
	}
	_ = profile
	writeJSON(w, http.StatusOK, map[string]any{"dev_mode": enabled})
}

func assemblyDevModeWrite(w http.ResponseWriter, r *http.Request, auth *authService, legacyURL *url.URL) {
	profile, failure := requireAdmin(r.Context(), auth, r)
	if failure != nil {
		failure.write(w)
		return
	}
	fullProfile, err := auth.currentProfile(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
		return
	}
	if auth.devMode == nil || !auth.devMode.enabled(fullProfile, r) {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error":   "dev_mode_required",
			"message": "Enable Dev Mode from the Assemblies admin controls to continue.",
		})
		return
	}
	ctx, cancel := requestTimeout(r.Context(), auth)
	defer cancel()
	_ = notifyLegacyAssemblyCache(ctx, auth, legacyURL, "flush")
	_ = profile
	writeJSON(w, http.StatusOK, map[string]any{"status": "flushed"})
}

func assemblyRequestContext(w http.ResponseWriter, r *http.Request, auth *authService) (operatorProfile, assemblyStore, bool) {
	profile, err := auth.currentProfile(r.Context(), r)
	if err != nil {
		if isUnauthorizedAuthError(err) {
			unauthorizedAuthFailure().write(w)
			return operatorProfile{}, nil, false
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
		return operatorProfile{}, nil, false
	}
	store, ok := auth.store.(assemblyStore)
	if !ok {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "assemblies_unavailable"})
		return operatorProfile{}, nil, false
	}
	return profile, store, true
}

func assemblyMutationAllowed(w http.ResponseWriter, r *http.Request, auth *authService, profile operatorProfile, domain string) bool {
	if domain == assemblyDomainUser {
		return true
	}
	if !strings.EqualFold(strings.TrimSpace(profile.Role), "admin") {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error":   "forbidden",
			"message": "Administrator permissions are required for this action.",
		})
		return false
	}
	if auth.devMode == nil || !auth.devMode.enabled(profile, r) {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error":   "dev_mode_required",
			"message": "Enable Dev Mode from the Assemblies admin controls to continue.",
		})
		return false
	}
	return true
}

func readAssemblyJSON(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	var body map[string]any
	reader := http.MaxBytesReader(w, r.Body, assemblyDocumentMaxBytes)
	err := json.NewDecoder(reader).Decode(&body)
	if err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return nil, false
	}
	if body == nil {
		body = map[string]any{}
	}
	return body, true
}

func assemblyPathParts(pathText string) []string {
	trimmed := strings.Trim(strings.TrimPrefix(pathText, "/api/assemblies/"), "/")
	if trimmed == "" {
		return nil
	}
	rawParts := strings.Split(trimmed, "/")
	parts := make([]string, 0, len(rawParts))
	for _, raw := range rawParts {
		if raw == "" {
			continue
		}
		decoded, err := url.PathUnescape(raw)
		if err != nil {
			decoded = raw
		}
		parts = append(parts, decoded)
	}
	return parts
}

func notifyLegacyAssemblyCache(ctx context.Context, auth *authService, legacyURL *url.URL, action string) error {
	if auth == nil || auth.verifier == nil || legacyURL == nil {
		return nil
	}
	path := "/api/internal/assemblies/cache/reload"
	if action == "flush" {
		path = "/api/internal/assemblies/cache/flush"
	}
	target := *legacyURL
	target.Path = path
	target.RawQuery = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set(internalTokenHeader, goInternalToken(auth.verifier.secret))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("legacy assembly cache %s failed: %s", action, resp.Status)
	}
	return nil
}

func (s *postgresOperatorStore) listAssemblies(ctx context.Context, filter assemblyListFilter) ([]map[string]any, []map[string]any, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close()
	rows := []assemblyDBRow{}
	for _, domain := range assemblyDomains() {
		if filter.Domain != "" && filter.Domain != domain {
			continue
		}
		domainRows, err := queryAssemblyRows(ctx, conn, domain, filter.AssemblyType)
		if err != nil {
			return nil, nil, err
		}
		rows = append(rows, domainRows...)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return strings.ToLower(rows[i].DisplayName) < strings.ToLower(rows[j].DisplayName)
	})
	items := make([]map[string]any, 0, len(rows))
	queue := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, serializeAssemblyRow(row, false))
		queue = append(queue, assemblyQueueEntry(row))
	}
	return items, queue, nil
}

func (s *postgresOperatorStore) getAssembly(ctx context.Context, assemblyGUID string, includePayload bool) (map[string]any, bool, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, false, err
	}
	defer conn.Close()
	row, found, err := findAssemblyRow(ctx, conn, assemblyGUID)
	if err != nil || !found {
		return nil, found, err
	}
	return serializeAssemblyRow(row, includePayload), true, nil
}

func (s *postgresOperatorStore) createAssembly(ctx context.Context, payload map[string]any) (map[string]any, int, error) {
	domain := assemblyNormalizeDomain(payload["domain"])
	if domain == "" {
		return nil, http.StatusBadRequest, fmt.Errorf("invalid domain")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	defer conn.Close()
	guid := assemblyCoerceGUID(payload["assembly_guid"])
	if guid != "" {
		if _, found, err := findAssemblyRow(ctx, conn, guid); err != nil {
			return nil, http.StatusInternalServerError, err
		} else if found {
			return nil, http.StatusBadRequest, fmt.Errorf("Assembly '%s' already exists", guid)
		}
	}
	row, err := buildAssemblyRow(payload, nil, domain)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	if err := upsertAssemblyRow(ctx, conn, row); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return serializeAssemblyRow(row, true), http.StatusCreated, nil
}

func (s *postgresOperatorStore) updateAssembly(ctx context.Context, assemblyGUID string, payload map[string]any) (map[string]any, int, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	defer conn.Close()
	existing, found, err := findAssemblyRow(ctx, conn, assemblyGUID)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if !found {
		return nil, http.StatusNotFound, fmt.Errorf("not found")
	}
	row, err := buildAssemblyRow(payload, &existing, existing.Domain)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	if err := upsertAssemblyRow(ctx, conn, row); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return serializeAssemblyRow(row, true), http.StatusOK, nil
}

func (s *postgresOperatorStore) deleteAssembly(ctx context.Context, assemblyGUID string) (map[string]any, int, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	defer conn.Close()
	existing, found, err := findAssemblyRow(ctx, conn, assemblyGUID)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if !found {
		return nil, http.StatusNotFound, fmt.Errorf("not found")
	}
	if err := deleteAssemblyRow(ctx, conn, existing.Domain, existing.AssemblyGUID); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return map[string]any{"status": "queued"}, http.StatusAccepted, nil
}

func (s *postgresOperatorStore) cloneAssembly(ctx context.Context, assemblyGUID string, payload map[string]any) (map[string]any, int, error) {
	targetDomain := assemblyNormalizeDomain(payload["target_domain"])
	if targetDomain == "" {
		return nil, http.StatusBadRequest, fmt.Errorf("invalid target domain")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	defer conn.Close()
	source, found, err := findAssemblyRow(ctx, conn, assemblyGUID)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if !found {
		return nil, http.StatusBadRequest, fmt.Errorf("Assembly '%s' not found", assemblyGUID)
	}
	cloneGUID := assemblyCoerceGUID(payload["new_assembly_guid"])
	if cloneGUID == "" {
		cloneGUID, err = newAssemblyGUID()
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
	}
	if _, found, err := findAssemblyRow(ctx, conn, cloneGUID); err != nil {
		return nil, http.StatusInternalServerError, err
	} else if found {
		return nil, http.StatusBadRequest, fmt.Errorf("Assembly '%s' already exists; provide a unique identifier.", cloneGUID)
	}
	now := assemblyNowString()
	row := assemblyDBRow{
		Domain:           targetDomain,
		AssemblyGUID:     cloneGUID,
		DisplayName:      source.DisplayName,
		Summary:          source.Summary,
		AssemblyType:     source.AssemblyType,
		AssemblySubtype:  source.AssemblySubtype,
		PayloadJSON:      source.PayloadJSON,
		PayloadSizeBytes: int64(len([]byte(source.PayloadJSON))),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	row.ContentHash = assemblyContentHash(row)
	if err := upsertAssemblyRow(ctx, conn, row); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return serializeAssemblyRow(row, true), http.StatusCreated, nil
}

func (s *postgresOperatorStore) importAssembly(ctx context.Context, payload map[string]any) (map[string]any, int, error) {
	domain := assemblyNormalizeDomain(payload["domain"])
	if domain == "" {
		domain = assemblyDomainUser
	}
	importPayload, err := prepareAssemblyImportPayload(payload, domain)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	guid := assemblyCoerceGUID(importPayload["assembly_guid"])
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	defer conn.Close()
	if guid != "" {
		if existing, found, err := findAssemblyRow(ctx, conn, guid); err != nil {
			return nil, http.StatusInternalServerError, err
		} else if found {
			row, err := buildAssemblyRow(importPayload, &existing, existing.Domain)
			if err != nil {
				return nil, http.StatusBadRequest, err
			}
			if err := upsertAssemblyRow(ctx, conn, row); err != nil {
				return nil, http.StatusInternalServerError, err
			}
			item := serializeAssemblyRow(row, true)
			item["queue"] = []map[string]any{assemblyQueueEntry(row)}
			return item, http.StatusCreated, nil
		}
	}
	row, err := buildAssemblyRow(importPayload, nil, domain)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	if err := upsertAssemblyRow(ctx, conn, row); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	item := serializeAssemblyRow(row, true)
	item["queue"] = []map[string]any{assemblyQueueEntry(row)}
	return item, http.StatusCreated, nil
}

func queryAssemblyRows(ctx context.Context, conn *sql.Conn, domain string, assemblyType string) ([]assemblyDBRow, error) {
	query := fmt.Sprintf(`
		SELECT assembly_guid, display_name, summary, assembly_type, assembly_subtype, payload_json,
		       source_repo, source_path, source_version, content_hash, payload_size_bytes, created_at, updated_at
		FROM %s`, assemblyQualifiedTable(domain))
	args := []any{}
	if strings.TrimSpace(assemblyType) != "" {
		query += " WHERE LOWER(assembly_type)=LOWER($1)"
		args = append(args, assemblyType)
	}
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []assemblyDBRow{}
	for rows.Next() {
		row := assemblyDBRow{Domain: domain}
		var summary, subtype, repo, sourcePath, sourceVersion, contentHash sql.NullString
		if err := rows.Scan(
			&row.AssemblyGUID,
			&row.DisplayName,
			&summary,
			&row.AssemblyType,
			&subtype,
			&row.PayloadJSON,
			&repo,
			&sourcePath,
			&sourceVersion,
			&contentHash,
			&row.PayloadSizeBytes,
			&row.CreatedAt,
			&row.UpdatedAt,
		); err != nil {
			return nil, err
		}
		row.Summary = summary.String
		row.AssemblySubtype = subtype.String
		row.SourceRepo = repo.String
		row.SourcePath = sourcePath.String
		row.SourceVersion = sourceVersion.String
		row.ContentHash = contentHash.String
		normalizeAssemblyRow(&row)
		result = append(result, row)
	}
	return result, rows.Err()
}

func findAssemblyRow(ctx context.Context, conn *sql.Conn, assemblyGUID string) (assemblyDBRow, bool, error) {
	guid := assemblyCoerceGUID(assemblyGUID)
	if guid == "" {
		return assemblyDBRow{}, false, nil
	}
	for _, domain := range assemblyDomains() {
		query := fmt.Sprintf(`
			SELECT assembly_guid, display_name, summary, assembly_type, assembly_subtype, payload_json,
			       source_repo, source_path, source_version, content_hash, payload_size_bytes, created_at, updated_at
			FROM %s
			WHERE assembly_guid=$1
			LIMIT 1`, assemblyQualifiedTable(domain))
		row := assemblyDBRow{Domain: domain}
		var summary, subtype, repo, sourcePath, sourceVersion, contentHash sql.NullString
		err := conn.QueryRowContext(ctx, query, guid).Scan(
			&row.AssemblyGUID,
			&row.DisplayName,
			&summary,
			&row.AssemblyType,
			&subtype,
			&row.PayloadJSON,
			&repo,
			&sourcePath,
			&sourceVersion,
			&contentHash,
			&row.PayloadSizeBytes,
			&row.CreatedAt,
			&row.UpdatedAt,
		)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return assemblyDBRow{}, false, err
		}
		row.Summary = summary.String
		row.AssemblySubtype = subtype.String
		row.SourceRepo = repo.String
		row.SourcePath = sourcePath.String
		row.SourceVersion = sourceVersion.String
		row.ContentHash = contentHash.String
		normalizeAssemblyRow(&row)
		return row, true, nil
	}
	return assemblyDBRow{}, false, nil
}

func upsertAssemblyRow(ctx context.Context, conn *sql.Conn, row assemblyDBRow) error {
	query := fmt.Sprintf(`
		INSERT INTO %s (
			assembly_guid, display_name, summary, assembly_type, assembly_subtype, payload_json,
			source_repo, source_path, source_version, content_hash, payload_size_bytes, created_at, updated_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT(assembly_guid) DO UPDATE SET
			display_name=EXCLUDED.display_name,
			summary=EXCLUDED.summary,
			assembly_type=EXCLUDED.assembly_type,
			assembly_subtype=EXCLUDED.assembly_subtype,
			payload_json=EXCLUDED.payload_json,
			source_repo=EXCLUDED.source_repo,
			source_path=EXCLUDED.source_path,
			source_version=EXCLUDED.source_version,
			content_hash=EXCLUDED.content_hash,
			payload_size_bytes=EXCLUDED.payload_size_bytes,
			updated_at=EXCLUDED.updated_at`, assemblyQualifiedTable(row.Domain))
	_, err := conn.ExecContext(
		ctx,
		query,
		row.AssemblyGUID,
		row.DisplayName,
		nullableString(row.Summary),
		row.AssemblyType,
		nullableString(row.AssemblySubtype),
		row.PayloadJSON,
		nullableString(row.SourceRepo),
		nullableString(row.SourcePath),
		nullableString(row.SourceVersion),
		nullableString(row.ContentHash),
		row.PayloadSizeBytes,
		row.CreatedAt,
		row.UpdatedAt,
	)
	return err
}

func deleteAssemblyRow(ctx context.Context, conn *sql.Conn, domain string, assemblyGUID string) error {
	query := fmt.Sprintf("DELETE FROM %s WHERE assembly_guid=$1", assemblyQualifiedTable(domain))
	_, err := conn.ExecContext(ctx, query, assemblyCoerceGUID(assemblyGUID))
	return err
}

func buildAssemblyRow(payload map[string]any, existing *assemblyDBRow, domain string) (assemblyDBRow, error) {
	now := assemblyNowString()
	guid := assemblyCoerceGUID(payload["assembly_guid"])
	if guid == "" && existing != nil {
		guid = existing.AssemblyGUID
	}
	if guid == "" {
		generated, err := newAssemblyGUID()
		if err != nil {
			return assemblyDBRow{}, err
		}
		guid = generated
	}
	assemblyType := assemblyNormalizeType(firstNonEmptyAny(payload["assembly_type"], existingAssemblyValue(existing, "assembly_type"), "script"))
	displayName := cleanText(payload["display_name"])
	if displayName == "" && existing != nil {
		displayName = existing.DisplayName
	}
	if displayName == "" {
		displayName = guid
	}
	summary, summaryProvided := optionalAssemblyText(payload["summary"])
	payloadContent, payloadProvided := payload["payload"]
	if payloadMap, ok := payloadContent.(map[string]any); ok {
		payloadMap = copyMap(payloadMap)
		payloadMap["assembly_guid"] = guid
		payloadContent = payloadMap
	}
	if !summaryProvided {
		if payloadSummary := assemblyPayloadSummary(payloadContent); payloadSummary != "" {
			summary = payloadSummary
		} else if existing != nil {
			summary = existing.Summary
		}
	}
	subtype := strings.ToLower(cleanText(firstNonEmptyAny(payload["assembly_subtype"], existingAssemblyValue(existing, "assembly_subtype"), assemblyDefaultSubtype(assemblyType))))
	sourceRepo := assemblyOptionalUpdate(payload, "source_repo", existing)
	sourcePath := assemblyOptionalUpdate(payload, "source_path", existing)
	sourceVersion := assemblyOptionalUpdate(payload, "source_version", existing)
	payloadText := ""
	if payloadProvided {
		serialized, err := assemblySerializePayload(payloadContent)
		if err != nil {
			return assemblyDBRow{}, err
		}
		payloadText = serialized
	} else if existing != nil {
		payloadText = existing.PayloadJSON
	} else {
		return assemblyDBRow{}, fmt.Errorf("payload content required for new assemblies")
	}
	createdAt := now
	if existing != nil && existing.CreatedAt != "" {
		createdAt = existing.CreatedAt
	}
	row := assemblyDBRow{
		Domain:           domain,
		AssemblyGUID:     guid,
		DisplayName:      displayName,
		Summary:          summary,
		AssemblyType:     assemblyType,
		AssemblySubtype:  subtype,
		PayloadJSON:      payloadText,
		SourceRepo:       sourceRepo,
		SourcePath:       sourcePath,
		SourceVersion:    sourceVersion,
		PayloadSizeBytes: int64(len([]byte(payloadText))),
		CreatedAt:        createdAt,
		UpdatedAt:        now,
	}
	row.ContentHash = assemblyContentHash(row)
	return row, nil
}

func prepareAssemblyImportPayload(payload map[string]any, domain string) (map[string]any, error) {
	document, ok := payload["document"]
	if !ok || document == nil {
		document = payload["payload"]
	}
	if document == nil {
		return nil, fmt.Errorf("missing document")
	}
	documentMap, err := assemblyDocumentMap(document)
	if err != nil {
		return nil, err
	}
	payloadDoc, err := assemblyPayloadDocument(documentMap)
	if err != nil {
		return nil, err
	}
	assemblyType := assemblyInferType(documentMap, payloadDoc)
	if assemblyType == "" {
		return nil, fmt.Errorf("Unable to determine assembly type from JSON document.")
	}
	guid := assemblyCoerceGUID(firstNonEmptyAny(payload["assembly_guid"], documentMap["assembly_guid"], payloadDoc["assembly_guid"]))
	if guid != "" {
		payloadDoc = copyMap(payloadDoc)
		payloadDoc["assembly_guid"] = guid
	}
	displayName := cleanText(firstNonEmptyAny(
		documentMap["display_name"],
		documentMap["name"],
		documentMap["tab_name"],
		payloadDoc["display_name"],
		payloadDoc["name"],
		payloadDoc["tab_name"],
		"Imported Assembly",
	))
	summary := assemblyPayloadSummary(payloadDoc)
	if summary == "" {
		summary = cleanText(firstNonEmptyAny(documentMap["description"], documentMap["summary"]))
	}
	subtype := strings.ToLower(cleanText(firstNonEmptyAny(documentMap["assembly_subtype"], documentMap["type"], payloadDoc["assembly_subtype"], payloadDoc["type"], assemblyDefaultSubtype(assemblyType))))
	return map[string]any{
		"assembly_guid":    guid,
		"domain":           domain,
		"assembly_type":    assemblyType,
		"display_name":     displayName,
		"summary":          summary,
		"assembly_subtype": subtype,
		"source_repo":      cleanText(firstNonEmptyAny(documentMap["source_repo"], payloadDoc["source_repo"])),
		"source_path":      cleanText(firstNonEmptyAny(documentMap["source_path"], payloadDoc["source_path"])),
		"source_version":   cleanText(firstNonEmptyAny(documentMap["source_version"], payloadDoc["source_version"])),
		"payload":          payloadDoc,
	}, nil
}

func serializeAssemblyRow(row assemblyDBRow, includePayload bool) map[string]any {
	normalizeAssemblyRow(&row)
	summary := assemblyCanonicalSummary(row.Summary, row.PayloadJSON)
	contentHash := row.ContentHash
	if contentHash == "" {
		contentHash = assemblyContentHash(row)
	}
	virtualPath := row.SourcePath
	if strings.TrimSpace(virtualPath) == "" {
		virtualPath = assemblyFallbackSourcePath(row)
	}
	item := map[string]any{
		"assembly_guid":    row.AssemblyGUID,
		"name":             row.DisplayName,
		"description":      summary,
		"display_name":     row.DisplayName,
		"summary":          summary,
		"assembly_type":    row.AssemblyType,
		"assembly_subtype": row.AssemblySubtype,
		"source":           row.Domain,
		"is_dirty":         false,
		"dirty_since":      nil,
		"last_persisted":   row.UpdatedAt,
		"payload_guid":     row.AssemblyGUID,
		"source_repo":      nilString(row.SourceRepo),
		"source_path":      nilString(row.SourcePath),
		"source_version":   nilString(row.SourceVersion),
		"virtual_path":     virtualPath,
		"path":             virtualPath,
		"assembly_id":      row.AssemblyGUID,
		"created_at":       row.CreatedAt,
		"updated_at":       row.UpdatedAt,
		"content_hash":     contentHash,
	}
	if includePayload {
		item["payload"] = row.PayloadJSON
		var payloadJSON any
		if err := json.Unmarshal([]byte(row.PayloadJSON), &payloadJSON); err != nil {
			payloadJSON = nil
		}
		item["payload_json"] = payloadJSON
	}
	return item
}

func assemblyExportDocument(item map[string]any) map[string]any {
	payloadText := cleanText(item["payload"])
	var payloadBody any
	if err := json.Unmarshal([]byte(payloadText), &payloadBody); err != nil {
		payloadBody = payloadText
	}
	if payloadMap, ok := payloadBody.(map[string]any); ok {
		if cleanText(item["assembly_type"]) == "workflow" {
			workflowDocument := copyMap(payloadMap)
			if cleanText(workflowDocument["tab_name"]) == "" && cleanText(item["display_name"]) != "" {
				workflowDocument["tab_name"] = cleanText(item["display_name"])
			}
			for _, field := range []string{"assembly_guid", "assembly_type", "assembly_subtype", "display_name", "summary", "domain", "payload_guid", "source_repo", "source_path", "source_version", "content_hash", "created_at", "updated_at", "is_dirty", "dirty_since", "last_persisted", "version", "category", "sites", "name", "description", "type", "workflow", "workflow_encoding", "workflowEncoding"} {
				delete(workflowDocument, field)
			}
			encoded, _ := json.Marshal(workflowDocument)
			return map[string]any{
				"assembly_guid": cleanText(item["assembly_guid"]),
				"name":          firstNonEmptyAny(item["display_name"], workflowDocument["tab_name"], "Workflow"),
				"description":   cleanText(item["summary"]),
				"type":          "workflow",
				"workflow":      base64.StdEncoding.EncodeToString(encoded),
			}
		}
		document := copyMap(payloadMap)
		document["assembly_guid"] = cleanText(item["assembly_guid"])
		if cleanText(firstNonEmptyAny(document["name"], document["tab_name"])) == "" && cleanText(item["display_name"]) != "" {
			document["name"] = cleanText(item["display_name"])
		}
		if cleanText(document["description"]) == "" && cleanText(item["summary"]) != "" {
			document["description"] = cleanText(item["summary"])
		}
		if cleanText(document["type"]) == "" && cleanText(item["assembly_subtype"]) != "" {
			document["type"] = cleanText(item["assembly_subtype"])
		}
		for _, field := range []string{"assembly_type", "assembly_subtype", "display_name", "summary", "domain", "payload_guid", "source_repo", "source_path", "source_version", "content_hash", "created_at", "updated_at", "is_dirty", "dirty_since", "last_persisted", "version", "category", "sites", "script_encoding", "scriptEncoding"} {
			delete(document, field)
		}
		return document
	}
	return map[string]any{
		"assembly_guid": cleanText(item["assembly_guid"]),
		"name":          firstNonEmptyAny(item["display_name"], "Assembly"),
		"description":   cleanText(item["summary"]),
		"type":          firstNonEmptyAny(item["assembly_subtype"], "powershell"),
		"content":       payloadBody,
	}
}

func assemblyQueueEntry(row assemblyDBRow) map[string]any {
	return map[string]any{
		"assembly_guid":  row.AssemblyGUID,
		"domain":         row.Domain,
		"is_dirty":       "false",
		"dirty_since":    "",
		"last_persisted": row.UpdatedAt,
	}
}

func assemblyDomains() []string {
	return []string{assemblyDomainOfficial, assemblyDomainCommunity, assemblyDomainUser}
}

func assemblyQualifiedTable(domain string) string {
	switch domain {
	case assemblyDomainOfficial:
		return "assemblies.official_assemblies"
	case assemblyDomainCommunity:
		return "assemblies.community_assemblies"
	default:
		return "assemblies.user_created_assemblies"
	}
}

func assemblyNormalizeDomain(value any) string {
	switch strings.ToLower(strings.TrimSpace(fmt.Sprint(value))) {
	case assemblyDomainOfficial:
		return assemblyDomainOfficial
	case assemblyDomainCommunity:
		return assemblyDomainCommunity
	case assemblyDomainUser:
		return assemblyDomainUser
	default:
		return ""
	}
}

func assemblyNormalizeType(value any) string {
	switch strings.ToLower(strings.TrimSpace(fmt.Sprint(value))) {
	case "workflow":
		return "workflow"
	case "ansible":
		return "ansible"
	default:
		return "script"
	}
}

func assemblyDefaultSubtype(assemblyType string) string {
	switch assemblyNormalizeType(assemblyType) {
	case "workflow":
		return "workflow"
	case "ansible":
		return "ansible"
	default:
		return "powershell"
	}
}

func normalizeAssemblyRow(row *assemblyDBRow) {
	row.AssemblyGUID = assemblyCoerceGUID(row.AssemblyGUID)
	row.Domain = assemblyNormalizeDomain(row.Domain)
	if row.Domain == "" {
		row.Domain = assemblyDomainUser
	}
	row.AssemblyType = assemblyNormalizeType(row.AssemblyType)
	if strings.TrimSpace(row.AssemblySubtype) == "" {
		row.AssemblySubtype = assemblyDefaultSubtype(row.AssemblyType)
	}
	if row.PayloadJSON == "" {
		row.PayloadJSON = "{}"
	}
	if row.PayloadSizeBytes <= 0 {
		row.PayloadSizeBytes = int64(len([]byte(row.PayloadJSON)))
	}
}

func assemblyCoerceGUID(value any) string {
	text := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
	if text == "" || text == "<nil>" {
		return ""
	}
	return text
}

func newAssemblyGUID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func assemblyNowString() string {
	return time.Now().UTC().Truncate(time.Second).Format("2006-01-02T15:04:05")
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nilString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func optionalAssemblyText(value any) (string, bool) {
	if value == nil {
		return "", false
	}
	return cleanText(value), true
}

func assemblyOptionalUpdate(payload map[string]any, key string, existing *assemblyDBRow) string {
	if value, ok := payload[key]; ok {
		return cleanText(value)
	}
	if existing == nil {
		return ""
	}
	switch key {
	case "source_repo":
		return existing.SourceRepo
	case "source_path":
		return existing.SourcePath
	case "source_version":
		return existing.SourceVersion
	default:
		return ""
	}
}

func existingAssemblyValue(row *assemblyDBRow, key string) any {
	if row == nil {
		return ""
	}
	switch key {
	case "assembly_type":
		return row.AssemblyType
	case "assembly_subtype":
		return row.AssemblySubtype
	default:
		return ""
	}
}

func assemblySerializePayload(value any) (string, error) {
	switch typed := value.(type) {
	case map[string]any, []any:
		encoded, err := json.MarshalIndent(typed, "", "  ")
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	case string:
		return typed, nil
	default:
		return "", fmt.Errorf("payload must be JSON object, array, or string")
	}
}

func assemblyDocumentMap(document any) (map[string]any, error) {
	switch typed := document.(type) {
	case map[string]any:
		return copyMap(typed), nil
	case string:
		var decoded map[string]any
		if err := json.Unmarshal([]byte(typed), &decoded); err != nil {
			return nil, fmt.Errorf("Import document is not valid JSON.")
		}
		if decoded == nil {
			return nil, fmt.Errorf("Import document must decode to a JSON object.")
		}
		return decoded, nil
	default:
		return nil, fmt.Errorf("Import document must be a JSON object or string.")
	}
}

func assemblyPayloadDocument(document map[string]any) (map[string]any, error) {
	if payload, ok := document["payload"].(map[string]any); ok {
		return copyMap(payload), nil
	}
	if workflowRaw, ok := document["workflow"]; ok && workflowRaw != nil {
		return assemblyWorkflowPayload(document, workflowRaw)
	}
	return copyMap(document), nil
}

func assemblyWorkflowPayload(document map[string]any, workflowRaw any) (map[string]any, error) {
	var workflow map[string]any
	switch typed := workflowRaw.(type) {
	case map[string]any:
		workflow = copyMap(typed)
	case string:
		workflowText := strings.TrimSpace(typed)
		if decoded, err := base64.StdEncoding.DecodeString(workflowText); err == nil {
			workflowText = string(decoded)
		}
		if err := json.Unmarshal([]byte(workflowText), &workflow); err != nil {
			return nil, fmt.Errorf("Workflow document did not decode to valid JSON.")
		}
	default:
		return nil, fmt.Errorf("Workflow document must be a JSON object or encoded string.")
	}
	if workflow == nil {
		return nil, fmt.Errorf("Workflow document must decode to a JSON object.")
	}
	if guid := assemblyCoerceGUID(firstNonEmptyAny(document["assembly_guid"], workflow["assembly_guid"])); guid != "" {
		workflow["assembly_guid"] = guid
	}
	if tabName := cleanText(firstNonEmptyAny(workflow["tab_name"], document["tab_name"], document["name"], document["display_name"])); tabName != "" {
		workflow["tab_name"] = tabName
	}
	return workflow, nil
}

func assemblyInferType(document map[string]any, payload map[string]any) string {
	if value := assemblyNormalizeType(firstNonEmptyAny(document["assembly_type"], document["kind"], payload["assembly_type"], payload["kind"])); value != "script" {
		return value
	}
	typeHint := strings.ToLower(cleanText(firstNonEmptyAny(document["type"], document["assembly_subtype"], payload["type"], payload["assembly_subtype"])))
	if strings.Contains(typeHint, "workflow") {
		return "workflow"
	}
	if strings.Contains(typeHint, "ansible") || strings.Contains(typeHint, "playbook") {
		return "ansible"
	}
	if _, ok := document["workflow"]; ok {
		return "workflow"
	}
	return "script"
}

func assemblyPayloadSummary(payload any) string {
	if payloadMap, ok := payload.(map[string]any); ok {
		return cleanText(firstNonEmptyAny(payloadMap["description"], payloadMap["summary"]))
	}
	return ""
}

func assemblyCanonicalSummary(summary string, payloadText string) string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadText), &payload); err == nil {
		if payloadSummary := assemblyPayloadSummary(payload); payloadSummary != "" {
			return payloadSummary
		}
	}
	return strings.TrimSpace(summary)
}

func assemblyFallbackSourcePath(row assemblyDBRow) string {
	prefix := "Scripts"
	if row.AssemblyType == "workflow" {
		prefix = "Workflows"
	} else if row.AssemblyType == "ansible" {
		prefix = "Ansible_Playbooks"
	}
	name := row.DisplayName
	if strings.TrimSpace(name) == "" {
		name = cleanText(firstNonEmpty(row.Summary, row.AssemblyGUID, "Assembly"))
	}
	return prefix + "/" + assemblySanitizePathName(name)
}

func assemblySanitizePathName(value string) string {
	var builder strings.Builder
	for _, ch := range strings.TrimSpace(value) {
		if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '.' || ch == '_' || ch == '-' {
			builder.WriteRune(ch)
		} else {
			builder.WriteByte('_')
		}
	}
	text := strings.TrimSpace(builder.String())
	if text == "" {
		return "Assembly"
	}
	return text
}

func assemblyContentHash(row assemblyDBRow) string {
	var payload any
	if err := json.Unmarshal([]byte(row.PayloadJSON), &payload); err != nil {
		payload = row.PayloadJSON
	}
	fields := map[string]any{
		"assembly_guid":    assemblyCoerceGUID(row.AssemblyGUID),
		"display_name":     row.DisplayName,
		"summary":          row.Summary,
		"assembly_type":    row.AssemblyType,
		"assembly_subtype": firstNonEmpty(row.AssemblySubtype, assemblyDefaultSubtype(row.AssemblyType)),
		"payload":          payload,
	}
	encoded, _ := json.Marshal(fields)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func assemblyTruthy(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes", "on", "refresh", "force":
			return true
		}
	}
	return false
}

func firstNonEmptyAny(values ...any) any {
	for _, value := range values {
		text := cleanText(value)
		if text != "" {
			return value
		}
	}
	return ""
}

func osEnv(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}
