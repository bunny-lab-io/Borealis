package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"
)

const (
	agentStatusDuplicateSuppressSeconds = 30 * time.Second
	agentStatusEmitMinInterval          = 10 * time.Second
	agentStatusCacheMaxEntries          = 2048
	agentDetailsDuplicateWindow         = 30 * time.Second
	agentDetailsCacheMaxEntries         = 2048
)

type agentIngestStore interface {
	updateAgentHeartbeat(ctx context.Context, deviceCtx deviceBearerAuthContext, payload map[string]any) (map[string]any, int, error)
	updateAgentStatus(ctx context.Context, deviceCtx deviceBearerAuthContext, payload map[string]any) (agentStatusUpdateResult, int, error)
	updateAgentDetails(ctx context.Context, deviceCtx deviceBearerAuthContext, payload map[string]any) (agentDetailsUpdateResult, int, error)
}

type agentStatusBroadcaster interface {
	broadcastAgentStatus(ctx context.Context, payload map[string]any) error
	broadcastDeviceEvent(ctx context.Context, eventName string, payload map[string]any) error
}

type agentStatusUpdateResult struct {
	Payload         map[string]any
	EmitPayload     map[string]any
	Signature       string
	CacheKey        string
	SiteID          *int64
	SiteName        string
	ShouldBroadcast bool
}

type agentDetailsUpdateResult struct {
	Payload           map[string]any
	Hostname          string
	ServicesChanged   bool
	SoftwareChanged   bool
	RememberDuplicate bool
}

type agentStatusCacheEntry struct {
	Signature  string
	SeenAt     time.Time
	LastEmitAt time.Time
	SiteID     *int64
	SiteName   string
}

type agentStatusCache struct {
	mu      sync.Mutex
	entries map[string]agentStatusCacheEntry
}

type agentDetailsCacheEntry struct {
	Hash   string
	SeenAt time.Time
}

type agentDetailsCache struct {
	mu      sync.Mutex
	entries map[string]agentDetailsCacheEntry
}

var globalAgentStatusCache = &agentStatusCache{entries: map[string]agentStatusCacheEntry{}}
var globalAgentDetailsCache = &agentDetailsCache{entries: map[string]agentDetailsCacheEntry{}}

func registerAgentIngestRoutes(mux *http.ServeMux, auth *authService, signer *agentJWTSigner, dpop *dpopVerifier, broadcaster agentStatusBroadcaster) {
	mux.HandleFunc("POST /api/agent/heartbeat", agentHeartbeatHandler(auth, signer, dpop))
	mux.HandleFunc("POST /api/agent/status", agentStatusHandler(auth, signer, dpop, broadcaster, globalAgentStatusCache))
	mux.HandleFunc("POST /api/agent/details", agentDetailsHandler(auth, signer, dpop, broadcaster, globalAgentDetailsCache))
}

func agentHeartbeatHandler(auth *authService, signer *agentJWTSigner, dpop *dpopVerifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		deviceCtx, failure := authenticateDeviceBearer(r.Context(), r, auth, signer, dpop)
		if failure != nil {
			failure.write(w)
			return
		}
		payload, err := readAgentJSONMap(w, r, 8<<20)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
			return
		}
		store, ok := auth.store.(agentIngestStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "agent_ingest_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		response, status, err := store.updateAgentHeartbeat(ctx, deviceCtx, payload)
		if err != nil {
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, status, response)
	}
}

func agentStatusHandler(auth *authService, signer *agentJWTSigner, dpop *dpopVerifier, broadcaster agentStatusBroadcaster, cache *agentStatusCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		deviceCtx, failure := authenticateDeviceBearer(r.Context(), r, auth, signer, dpop)
		if failure != nil {
			failure.write(w)
			return
		}
		payload, err := readAgentJSONMap(w, r, 1<<20)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
			return
		}
		guidLookup := firstText(normalizeCanonicalGUID(deviceCtx.GUID), cleanText(deviceCtx.GUID))
		serviceMode := normalizeAgentServiceMode(firstNonEmpty(payload["service_mode"], claimString(deviceCtx.Claims, "service_mode"), "system"))
		cacheKey := agentStatusCacheKey(guidLookup, serviceMode)
		signature := normalizedAgentStatusSignature(payload)
		if cached := cache.cached(cacheKey, signature, time.Now()); cached != nil {
			writeJSON(w, http.StatusOK, cached)
			return
		}

		store, ok := auth.store.(agentIngestStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "agent_ingest_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		result, status, err := store.updateAgentStatus(ctx, deviceCtx, payload)
		if err != nil {
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		emitAllowed := cache.remember(result.CacheKey, result.Signature, time.Now(), result.SiteID, result.SiteName)
		if broadcaster != nil && result.ShouldBroadcast && emitAllowed {
			if err := broadcaster.broadcastAgentStatus(r.Context(), result.EmitPayload); err != nil {
				// Status ingest remains authoritative even when operator fanout is unavailable.
				logDebug("agents", "agent_status_changed broadcast failed: "+err.Error())
			}
		}
		writeJSON(w, status, result.Payload)
	}
}

func readAgentJSONMap(w http.ResponseWriter, r *http.Request, limit int64) (map[string]any, error) {
	var body map[string]any
	if r.Body != nil {
		err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(&body)
		if err != nil && err.Error() != "EOF" {
			return nil, err
		}
	}
	if body == nil {
		body = map[string]any{}
	}
	return body, nil
}

func readAgentJSONMapWithHash(w http.ResponseWriter, r *http.Request, limit int64) (map[string]any, string, error) {
	bodyBytes, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		return nil, "", err
	}
	bodyHash := ""
	if len(bodyBytes) > 0 {
		bodyHash = fmt.Sprintf("%x", sha256.Sum256(bodyBytes))
	}
	if len(strings.TrimSpace(string(bodyBytes))) == 0 {
		return map[string]any{}, bodyHash, nil
	}
	var body map[string]any
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		return nil, bodyHash, err
	}
	if body == nil {
		body = map[string]any{}
	}
	return body, bodyHash, nil
}

func agentDetailsHandler(auth *authService, signer *agentJWTSigner, dpop *dpopVerifier, broadcaster agentStatusBroadcaster, cache *agentDetailsCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		deviceCtx, failure := authenticateDeviceBearer(r.Context(), r, auth, signer, dpop)
		if failure != nil {
			failure.write(w)
			return
		}
		payload, payloadHash, err := readAgentJSONMapWithHash(w, r, 24<<20)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
			return
		}
		cacheKey := agentDetailsCacheKey(deviceCtx, payload)
		if cache.cached(cacheKey, payloadHash, time.Now()) {
			writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "coalesced": true})
			return
		}
		store, ok := auth.store.(agentIngestStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "agent_ingest_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		result, status, err := store.updateAgentDetails(ctx, deviceCtx, payload)
		if err != nil {
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		if result.RememberDuplicate {
			cache.remember(cacheKey, payloadHash, time.Now())
		}
		if broadcaster != nil && result.Hostname != "" {
			if result.ServicesChanged {
				if err := broadcaster.broadcastDeviceEvent(r.Context(), "device_services_changed", map[string]any{"hostname": result.Hostname, "change": "updated"}); err != nil {
					logDebug("agents", "device_services_changed broadcast failed: "+err.Error())
				}
			}
			if result.SoftwareChanged {
				if err := broadcaster.broadcastDeviceEvent(r.Context(), "device_inventory_changed", map[string]any{"hostname": result.Hostname, "change": "software_updated"}); err != nil {
					logDebug("agents", "device_inventory_changed broadcast failed: "+err.Error())
				}
			}
		}
		writeJSON(w, status, result.Payload)
	}
}

func (c *agentStatusCache) cached(key string, signature string, now time.Time) map[string]any {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]agentStatusCacheEntry{}
	}
	entry, ok := c.entries[key]
	if !ok || entry.Signature != signature || now.Sub(entry.SeenAt) > agentStatusDuplicateSuppressSeconds {
		return nil
	}
	entry.SeenAt = now
	c.entries[key] = entry
	return map[string]any{
		"status":        "ok",
		"poll_after_ms": int64(30000),
		"site_id":       nullableInt64(entry.SiteID),
		"site_name":     entry.SiteName,
		"coalesced":     true,
	}
}

func (c *agentStatusCache) remember(key string, signature string, now time.Time, siteID *int64, siteName string) bool {
	if c == nil {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[key]
	emitAllowed := now.Sub(entry.LastEmitAt) >= agentStatusEmitMinInterval
	if emitAllowed {
		entry.LastEmitAt = now
	}
	entry.Signature = signature
	entry.SeenAt = now
	entry.SiteID = siteID
	entry.SiteName = siteName
	c.entries[key] = entry
	if len(c.entries) > agentStatusCacheMaxEntries {
		type staleEntry struct {
			Key    string
			SeenAt time.Time
		}
		stale := make([]staleEntry, 0, len(c.entries))
		for key, entry := range c.entries {
			stale = append(stale, staleEntry{Key: key, SeenAt: entry.SeenAt})
		}
		sort.Slice(stale, func(i, j int) bool { return stale[i].SeenAt.Before(stale[j].SeenAt) })
		for _, item := range stale[:maxInt(1, len(c.entries)-agentStatusCacheMaxEntries)] {
			delete(c.entries, item.Key)
		}
	}
	return emitAllowed
}

func (c *agentDetailsCache) cached(key string, hash string, now time.Time) bool {
	if c == nil || key == "" || hash == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]agentDetailsCacheEntry{}
	}
	entry, ok := c.entries[key]
	if !ok || entry.Hash != hash || now.Sub(entry.SeenAt) > agentDetailsDuplicateWindow {
		return false
	}
	entry.SeenAt = now
	c.entries[key] = entry
	return true
}

func (c *agentDetailsCache) remember(key string, hash string, now time.Time) {
	if c == nil || key == "" || hash == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]agentDetailsCacheEntry{}
	}
	c.entries[key] = agentDetailsCacheEntry{Hash: hash, SeenAt: now}
	if len(c.entries) > agentDetailsCacheMaxEntries {
		type staleEntry struct {
			Key    string
			SeenAt time.Time
		}
		stale := make([]staleEntry, 0, len(c.entries))
		for key, entry := range c.entries {
			stale = append(stale, staleEntry{Key: key, SeenAt: entry.SeenAt})
		}
		sort.Slice(stale, func(i, j int) bool { return stale[i].SeenAt.Before(stale[j].SeenAt) })
		for _, item := range stale[:maxInt(1, len(c.entries)-agentDetailsCacheMaxEntries)] {
			delete(c.entries, item.Key)
		}
	}
}

func agentDetailsCacheKey(deviceCtx deviceBearerAuthContext, payload map[string]any) string {
	details, _ := payload["details"].(map[string]any)
	summary, _ := details["summary"].(map[string]any)
	hostname := firstText(cleanText(payload["hostname"]), cleanText(summary["hostname"]))
	serviceMode := normalizeAgentServiceMode(firstNonEmpty(payload["service_mode"], summary["service_mode"], claimString(deviceCtx.Claims, "service_mode"), "system"))
	return firstText(normalizeCanonicalGUID(deviceCtx.GUID), cleanText(deviceCtx.GUID)) + "|" + strings.ToLower(hostname) + "|" + serviceMode
}

func (s *postgresOperatorStore) updateAgentHeartbeat(ctx context.Context, deviceCtx deviceBearerAuthContext, payload map[string]any) (map[string]any, int, error) {
	now := time.Now().Unix()
	authGUID := normalizeCanonicalGUID(deviceCtx.GUID)
	if authGUID == "" {
		return nil, http.StatusNotFound, errors.New("device_not_registered")
	}
	updates := map[string]any{"last_seen": now}
	hostname := cleanText(payload["hostname"])
	if hostname != "" {
		updates["hostname"] = hostname
	}
	if inventory, ok := payload["inventory"].(map[string]any); ok {
		for _, key := range []string{"memory", "network", "software", "storage", "cpu"} {
			if value, ok := inventory[key]; ok && value != nil {
				if encoded, ok := jsonString(value); ok {
					updates[key] = encoded
				}
			}
		}
	}
	metrics, _ := payload["metrics"].(map[string]any)
	for target, source := range map[string]string{
		"last_user":        "last_user",
		"domain":           "domain",
		"operating_system": "operating_system",
		"last_reboot":      "last_reboot",
	} {
		if text := cleanText(metrics[source]); text != "" {
			updates[target] = text
		}
	}
	if value, ok := int64Value(metrics["uptime"]); ok {
		updates["uptime"] = value
	}
	if value, ok := float64Value(metrics["cpu_percent"]); ok {
		updates["cpu_percent"] = value
	}
	if value, ok := float64Value(metrics["memory_percent"]); ok {
		updates["memory_percent"] = value
	}
	for _, field := range []string{"external_ip", "internal_ip", "device_type"} {
		if text := cleanText(payload[field]); text != "" {
			updates[field] = text
		}
	}
	agentBuildID := firstText(cleanText(payload["agent_build_id"]), cleanText(payload["installed_build_id"]), cleanText(payload["agent_hash"]))
	if agentBuildID != "" {
		updates["agent_hash"] = agentBuildID
	}
	updateStatus, _ := payload["agent_update_status"].(map[string]any)
	agentReleaseChannel := cleanText(payload["agent_release_channel"])
	agentBranch := cleanText(payload["agent_branch"])
	if agentReleaseChannel != "" {
		updates["agent_release_channel"] = agentReleaseChannel
	}
	if agentBranch != "" {
		updates["agent_branch"] = agentBranch
	}
	if len(updateStatus) > 0 {
		updates["agent_update_channel"] = cleanText(firstNonEmpty(updateStatus["target_channel"], updateStatus["effective_channel"]))
		updates["agent_update_target_build_id"] = cleanText(updateStatus["target_build_id"])
		updates["agent_update_state"] = cleanText(updateStatus["state"])
		updates["agent_update_error"] = cleanText(updateStatus["last_error"])
		updates["agent_update_source"] = cleanText(updateStatus["last_source"])
	}
	incomingRoleHealth, hasRoleHealth := payload["agent_role_health"]
	incomingMetadataFields, hasMetadataFields := payload["metadata_fields"]
	incomingServiceMode := normalizeAgentServiceMode(firstNonEmpty(payload["service_mode"], metrics["service_mode"], claimString(deviceCtx.Claims, "service_mode"), "system"))

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	targetGUID, existingRoleHealth, rowHostname, found, err := selectAgentDeviceForGUID(ctx, tx, authGUID)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if !found {
		return nil, http.StatusNotFound, errors.New("device_not_registered")
	}
	if hostname != "" && !strings.EqualFold(rowHostname, hostname) {
		conflictGUID, conflict, err := lookupDeviceGUIDForHostname(ctx, tx, hostname)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		if conflict && !strings.EqualFold(conflictGUID, targetGUID) {
			delete(updates, "hostname")
		}
	}
	if hasRoleHealth {
		updates["agent_role_health"] = serializeAgentRoleHealth(mergeAgentRoleHealth(existingRoleHealth, incomingRoleHealth, incomingServiceMode))
	}
	updated, err := updateAgentDeviceColumns(ctx, tx, targetGUID, updates)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if updated == 0 {
		return nil, http.StatusNotFound, errors.New("device_not_registered")
	}
	metadataSync := map[string]any{"updates": map[string]any{}, "acks": []string{}}
	if hasMetadataFields {
		metadataSync, err = processAgentMetadataSyncTx(ctx, tx, targetGUID, incomingMetadataFields, now)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
	}
	siteID, siteName, err := lookupAgentSiteTx(ctx, tx, targetGUID)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if err := tx.Commit(); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	committed = true

	if len(updateStatus) > 0 {
		reconcileHostname := firstText(cleanText(updates["hostname"]), hostname, rowHostname)
		_ = s.reconcileAgentMaintenanceHeartbeat(ctx, reconcileHostname, updateStatus, firstText(cleanText(updateStatus["target_channel"]), cleanText(updateStatus["effective_channel"]), agentReleaseChannel), firstText(cleanText(updateStatus["target_branch"]), agentBranch), agentBuildID)
	}

	return map[string]any{
		"status":              "ok",
		"poll_after_ms":       int64(15000),
		"site_id":             nullableInt64(siteID),
		"site_name":           siteName,
		"metadata_fields":     metadataSync["updates"],
		"metadata_field_acks": metadataSync["acks"],
	}, http.StatusOK, nil
}

func (s *postgresOperatorStore) updateAgentStatus(ctx context.Context, deviceCtx deviceBearerAuthContext, payload map[string]any) (agentStatusUpdateResult, int, error) {
	now := time.Now().Unix()
	authGUID := normalizeCanonicalGUID(deviceCtx.GUID)
	if authGUID == "" {
		return agentStatusUpdateResult{}, http.StatusNotFound, errors.New("device_not_registered")
	}
	serviceMode := normalizeAgentServiceMode(firstNonEmpty(payload["service_mode"], claimString(deviceCtx.Claims, "service_mode"), "system"))
	phase := cleanText(payload["phase"])
	message := cleanText(payload["message"])
	statusCode := normalizeAgentStartupStatus(payload["status"])
	hostname := cleanText(payload["hostname"])
	milestones := anySlice(payload["milestones"])
	lastError := payload["last_error"]
	switch lastError.(type) {
	case map[string]any, []any, string:
	default:
		lastError = nil
	}
	bootID := cleanText(payload["boot_id"])
	details := map[string]any{
		"boot_id":         bootID,
		"phase":           phase,
		"message":         message,
		"milestones_json": mustJSONString(milestones),
		"last_error_json": "",
	}
	if lastError != nil {
		details["last_error_json"] = mustJSONString(lastError)
	}
	role := map[string]any{
		"role_id":         "system:system_heartbeat",
		"role_name":       "system_heartbeat",
		"role_label":      "Startup Timeline",
		"context":         "system",
		"status_code":     statusCode,
		"status":          statusCode,
		"detail":          firstText(message, phase, "Startup status updated."),
		"details":         details,
		"last_checked_at": now,
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return agentStatusUpdateResult{}, http.StatusInternalServerError, err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return agentStatusUpdateResult{}, http.StatusInternalServerError, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	targetGUID, existingRoleHealth, existingHostname, found, err := selectAgentDeviceForGUID(ctx, tx, authGUID)
	if err != nil {
		return agentStatusUpdateResult{}, http.StatusInternalServerError, err
	}
	if !found {
		return agentStatusUpdateResult{}, http.StatusNotFound, errors.New("device_not_registered")
	}
	merged := serializeAgentRoleHealth(upsertSingleAgentRoleHealth(existingRoleHealth, role))
	result, err := tx.ExecContext(ctx, `UPDATE engine.devices SET last_seen=$1, agent_role_health=$2 WHERE guid=$3`, now, merged, targetGUID)
	if err != nil {
		return agentStatusUpdateResult{}, http.StatusInternalServerError, err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return agentStatusUpdateResult{}, http.StatusNotFound, errors.New("device_not_registered")
	}
	siteID, siteName, err := lookupAgentSiteTx(ctx, tx, targetGUID)
	if err != nil {
		return agentStatusUpdateResult{}, http.StatusInternalServerError, err
	}
	if err := tx.Commit(); err != nil {
		return agentStatusUpdateResult{}, http.StatusInternalServerError, err
	}
	committed = true

	emittedHostname := firstText(existingHostname, hostname)
	response := map[string]any{
		"status":        "ok",
		"poll_after_ms": int64(15000),
		"site_id":       nullableInt64(siteID),
		"site_name":     siteName,
	}
	emitPayload := map[string]any{
		"hostname":   emittedHostname,
		"guid":       authGUID,
		"phase":      phase,
		"status":     statusCode,
		"changed_at": now,
	}
	signature := normalizedAgentStatusSignature(payload)
	return agentStatusUpdateResult{
		Payload:         response,
		EmitPayload:     emitPayload,
		Signature:       signature,
		CacheKey:        agentStatusCacheKey(authGUID, serviceMode),
		SiteID:          siteID,
		SiteName:        siteName,
		ShouldBroadcast: true,
	}, http.StatusOK, nil
}

func selectAgentDeviceForGUID(ctx context.Context, tx *sql.Tx, guid string) (string, string, string, bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT guid, COALESCE(agent_role_health, ''), COALESCE(hostname, '') FROM engine.devices WHERE UPPER(guid)=UPPER($1)`, guid)
	if err != nil {
		return "", "", "", false, err
	}
	defer rows.Close()
	for rows.Next() {
		var rowGUID, roleHealth, hostname string
		if err := rows.Scan(&rowGUID, &roleHealth, &hostname); err != nil {
			return "", "", "", false, err
		}
		if normalizeCanonicalGUID(rowGUID) == guid {
			return rowGUID, roleHealth, hostname, true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return "", "", "", false, err
	}
	return "", "", "", false, nil
}

func lookupDeviceGUIDForHostname(ctx context.Context, tx *sql.Tx, hostname string) (string, bool, error) {
	var guid sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT guid FROM engine.devices WHERE hostname=$1 LIMIT 1`, hostname).Scan(&guid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return nullString(guid), true, nil
}

func updateAgentDeviceColumns(ctx context.Context, tx *sql.Tx, guid string, updates map[string]any) (int64, error) {
	if len(updates) == 0 {
		return 0, nil
	}
	keys := make([]string, 0, len(updates))
	for key := range updates {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	assignments := make([]string, 0, len(keys))
	args := make([]any, 0, len(keys)+1)
	for index, key := range keys {
		assignments = append(assignments, fmt.Sprintf("%s=$%d", key, index+1))
		args = append(args, updates[key])
	}
	args = append(args, guid)
	result, err := tx.ExecContext(ctx, `UPDATE engine.devices SET `+strings.Join(assignments, ", ")+fmt.Sprintf(" WHERE guid=$%d", len(args)), args...)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && string(pqErr.Code) == "23505" {
			return 0, errors.New("device_update_conflict")
		}
		return 0, err
	}
	return result.RowsAffected()
}

func lookupAgentSiteTx(ctx context.Context, tx *sql.Tx, guid string) (*int64, string, error) {
	var siteID sql.NullInt64
	var siteName sql.NullString
	err := tx.QueryRowContext(ctx, `
		SELECT ds.site_id, s.name
		  FROM engine.devices AS d
	 LEFT JOIN engine.device_sites AS ds ON ds.device_hostname=d.hostname
	 LEFT JOIN engine.sites AS s ON s.id=ds.site_id
		 WHERE UPPER(d.guid)=UPPER($1)
		 LIMIT 1
	`, guid).Scan(&siteID, &siteName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	return nullableInt64Ptr(siteID), nullString(siteName), nil
}

func processAgentMetadataSyncTx(ctx context.Context, tx *sql.Tx, guid string, raw any, now int64) (map[string]any, error) {
	incoming := normalizeAgentMetadataPayload(raw, now)
	if guid == "" || len(incoming) == 0 {
		return map[string]any{"updates": map[string]any{}, "acks": []string{}}, nil
	}
	existing, err := metadataDeviceValuesTx(ctx, tx, guid)
	if err != nil {
		return nil, err
	}
	for fieldNumber, record := range incoming {
		current, ok := existing[fieldNumber]
		if !ok || record.ModifiedAt > nullInt(current.ModifiedAt) {
			if err := upsertAgentMetadataValueTx(ctx, tx, guid, record, now); err != nil {
				return nil, err
			}
		}
	}
	latest, err := metadataDeviceValuesTx(ctx, tx, guid)
	if err != nil {
		return nil, err
	}
	acks := make([]string, 0, len(incoming))
	for fieldNumber, record := range incoming {
		current, ok := latest[fieldNumber]
		if ok && nullInt(current.ModifiedAt) >= record.ModifiedAt {
			acks = append(acks, metadataFieldKey(fieldNumber))
		}
	}
	sort.Strings(acks)
	return map[string]any{"updates": map[string]any{}, "acks": acks}, nil
}

type agentMetadataRecord struct {
	FieldNumber int
	FieldKey    string
	Value       string
	ModifiedAt  int64
	Source      string
	Actor       string
}

func normalizeAgentMetadataPayload(raw any, now int64) map[int]agentMetadataRecord {
	source, ok := raw.(map[string]any)
	if !ok {
		return map[int]agentMetadataRecord{}
	}
	out := map[int]agentMetadataRecord{}
	for rawKey, rawValue := range source {
		fieldNumber := normalizeMetadataFieldNumber(rawKey)
		value := rawValue
		modifiedAt := now
		sourceText := "agent"
		actor := ""
		if row, ok := rawValue.(map[string]any); ok {
			if fieldNumber == 0 {
				fieldNumber = normalizeMetadataFieldNumber(firstNonEmpty(row["field_number"], row["fieldNumber"], row["number"], row["field_key"], row["fieldKey"]))
			}
			value = row["value"]
			if parsed, ok := int64Value(firstNonEmpty(row["modified_at"], row["modifiedAt"], row["modified"])); ok && parsed > 0 {
				modifiedAt = parsed
			}
			sourceText = firstText(cleanText(row["source"]), "agent")
			actor = cleanText(firstNonEmpty(row["actor"], row["modified_by"], row["modifiedBy"]))
		}
		if fieldNumber < 1 || fieldNumber > metadataFieldCount {
			continue
		}
		if modifiedAt > now+300 {
			modifiedAt = now
		}
		out[fieldNumber] = agentMetadataRecord{
			FieldNumber: fieldNumber,
			FieldKey:    metadataFieldKey(fieldNumber),
			Value:       encodeMetadataValue(decodeMetadataValue(cleanText(value))),
			ModifiedAt:  modifiedAt,
			Source:      truncateMetadataText(firstText(sourceText, "agent"), 64),
			Actor:       truncateMetadataText(actor, 255),
		}
	}
	return out
}

func normalizeMetadataFieldNumber(value any) int {
	text := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
	if text == "" || text == "<nil>" {
		return 0
	}
	text = strings.TrimPrefix(text, "metadata")
	text = strings.TrimPrefix(text, "field")
	text = strings.Trim(text, " _-")
	if strings.HasPrefix(text, "_") {
		text = strings.Trim(text, " _-")
	}
	if strings.HasPrefix(text, "field_") {
		text = strings.TrimPrefix(text, "field_")
	}
	parts := strings.FieldsFunc(text, func(r rune) bool { return r == '_' || r == '-' || r == ' ' })
	if len(parts) > 0 {
		text = parts[len(parts)-1]
	}
	parsed, err := strconv.Atoi(text)
	if err != nil || parsed < 1 || parsed > metadataFieldCount {
		return 0
	}
	return parsed
}

func metadataDeviceValuesTx(ctx context.Context, tx *sql.Tx, deviceGUID string) (map[int]deviceMetadataValueRow, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT field_number, field_key, value, modified_at, source, actor, created_at, updated_at
		  FROM engine.device_metadata_fields
		 WHERE device_guid=$1
		   AND field_number BETWEEN 1 AND $2
	`, deviceGUID, metadataFieldCount)
	if err != nil {
		return map[int]deviceMetadataValueRow{}, nil
	}
	defer rows.Close()
	values := map[int]deviceMetadataValueRow{}
	for rows.Next() {
		var row deviceMetadataValueRow
		if err := rows.Scan(&row.FieldNumber, &row.FieldKey, &row.Value, &row.ModifiedAt, &row.Source, &row.Actor, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, err
		}
		fieldNumber := int(nullInt(row.FieldNumber))
		if fieldNumber >= 1 && fieldNumber <= metadataFieldCount {
			values[fieldNumber] = row
		}
	}
	return values, rows.Err()
}

func upsertAgentMetadataValueTx(ctx context.Context, tx *sql.Tx, guid string, record agentMetadataRecord, now int64) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO engine.device_metadata_fields(
		    device_guid, field_number, field_key, value, modified_at, source, actor, created_at, updated_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8)
		ON CONFLICT(device_guid, field_number) DO UPDATE SET
		    field_key=EXCLUDED.field_key,
		    value=EXCLUDED.value,
		    modified_at=EXCLUDED.modified_at,
		    source=EXCLUDED.source,
		    actor=EXCLUDED.actor,
		    updated_at=EXCLUDED.updated_at
	`, guid, record.FieldNumber, record.FieldKey, record.Value, record.ModifiedAt, record.Source, record.Actor, now)
	return err
}

func (s *postgresOperatorStore) reconcileAgentMaintenanceHeartbeat(ctx context.Context, hostname string, updateStatus map[string]any, releaseChannel string, branch string, installedBuildID string) error {
	operationID := cleanText(updateStatus["operation_id"])
	if operationID == "" || hostname == "" {
		return nil
	}
	rawState := strings.ToLower(cleanText(firstNonEmpty(updateStatus["state"], updateStatus["status"])))
	if rawState == "" {
		return nil
	}
	runStatus := "Running"
	activityStatus := "Running"
	var finished any
	stderr := ""
	now := time.Now().Unix()
	if stringSetContains(map[string]bool{"success": true, "completed": true, "complete": true, "up_to_date": true, "applied": true}, rawState) {
		runStatus = "Success"
		activityStatus = "Success"
		finished = now
	} else if stringSetContains(map[string]bool{"failed": true, "error": true}, rawState) {
		runStatus = "Failed"
		activityStatus = "Failed"
		finished = now
		stderr = firstText(cleanText(updateStatus["last_error"]), "Agent update operation failed.")
	}
	stdout := fmt.Sprintf("Agent reported operation_id=%s state=%s release_channel=%s branch=%s installed_build_id=%s\n", operationID, rawState, firstText(releaseChannel, "-"), firstText(branch, "-"), firstText(installedBuildID, "-"))

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
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
	rows, err := tx.QueryContext(ctx, `
		SELECT r.id, h.id
		  FROM engine.scheduled_job_runs r
		  JOIN engine.scheduled_job_run_activity s ON s.run_id=r.id
		  JOIN engine.activity_history h ON h.id=s.activity_id
		  JOIN engine.scheduled_jobs j ON j.id=r.job_id
		 WHERE LOWER(COALESCE(h.hostname, ''))=LOWER($1)
		   AND (COALESCE(h.metadata_json, '') LIKE $2 OR COALESCE(h.stdout, '') LIKE $2)
		   AND COALESCE(j.job_kind, '')='agent_maintenance'
		   AND LOWER(COALESCE(r.status, '')) NOT IN ('success', 'failed', 'skipped')
	`, hostname, "%"+operationID+"%")
	if err != nil {
		return err
	}
	defer rows.Close()
	type pair struct{ runID, activityID int64 }
	pairs := []pair{}
	for rows.Next() {
		var item pair
		if err := rows.Scan(&item.runID, &item.activityID); err != nil {
			return err
		}
		pairs = append(pairs, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range pairs {
		if _, err := tx.ExecContext(ctx, `
			UPDATE engine.scheduled_job_runs
			   SET status=$1, updated_at=$2, finished_ts=COALESCE($3, finished_ts), error=$4
			 WHERE id=$5
		`, runStatus, now, finished, truncateMetadataText(stderr, 512), item.runID); err != nil {
			return err
		}
		resolutionStatus := "eligible"
		if runStatus == "Failed" {
			resolutionStatus = "unresolved"
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE engine.scheduled_job_run_targets
			   SET resolution_status=$1, resolution_reason=$2
			 WHERE run_id=$3
		`, resolutionStatus, truncateMetadataText(stderr, 512), item.runID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE engine.activity_history
			   SET status=$1,
			       stdout=COALESCE(stdout, '') || $2,
			       stderr=COALESCE(stderr, '') || $3,
			       updated_at=$4,
			       finished_at=COALESCE($5, finished_at)
			 WHERE id=$6
		`, activityStatus, stdout, map[bool]string{true: stderr + "\n", false: ""}[stderr != ""], now, finished, item.activityID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func normalizeAgentServiceMode(value any) string {
	text := strings.ToLower(strings.ReplaceAll(cleanText(value), "-", "_"))
	switch text {
	case "", "system", "svc", "service", "system_service":
		return "system"
	case "interactive", "currentuser", "current_user", "user":
		return "currentuser"
	default:
		return text
	}
}

func normalizeAgentStartupStatus(value any) string {
	text := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(cleanText(value), " ", "_"), "-", "_"))
	aliases := map[string]string{
		"ok":        "healthy",
		"online":    "healthy",
		"complete":  "healthy",
		"completed": "healthy",
		"ready":     "healthy",
		"starting":  "recovering",
		"active":    "recovering",
		"pending":   "recovering",
		"failed":    "unhealthy",
		"error":     "unhealthy",
	}
	if alias := aliases[text]; alias != "" {
		text = alias
	}
	switch text {
	case "healthy", "recovering", "unhealthy", "pending", "unknown":
		return text
	default:
		return "recovering"
	}
}

func normalizeAgentRoleStatus(value any) string {
	text := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(cleanText(value), " ", "_"), "-", "_"))
	aliases := map[string]string{
		"ok":            "healthy",
		"up":            "healthy",
		"ready":         "healthy",
		"running":       "healthy",
		"online":        "healthy",
		"warning":       "recovering",
		"degraded":      "recovering",
		"healing":       "recovering",
		"starting":      "recovering",
		"bootstrapping": "pending",
		"initializing":  "pending",
		"idle":          "pending",
		"down":          "unhealthy",
		"failed":        "unhealthy",
		"error":         "unhealthy",
		"broken":        "unhealthy",
		"stale":         "stale",
	}
	if alias := aliases[text]; alias != "" {
		text = alias
	}
	if agentRoleStatusLabel(text) != "" {
		return text
	}
	return "unknown"
}

func agentRoleStatusLabel(code string) string {
	switch code {
	case "healthy":
		return "Healthy"
	case "recovering":
		return "Recovering"
	case "unhealthy":
		return "Unhealthy"
	case "pending":
		return "Pending"
	case "loaded":
		return "Loaded"
	case "unsupported":
		return "Unsupported"
	case "not_applicable":
		return "Not Applicable"
	case "stale":
		return "Stale"
	case "unknown":
		return "Unknown"
	default:
		return ""
	}
}

func normalizeAgentRoleName(value any) string {
	text := cleanText(value)
	if text == "" {
		return ""
	}
	switch strings.ToLower(text) {
	case "script_exec_system":
		return "context_system"
	case "script_exec_currentuser":
		return "context_currentuser"
	case "device_audit":
		return "device_auditor"
	case "service_control":
		return "service_management"
	case "remoteshell":
		return "remote_shell"
	case "wireguardtunnel":
		return "wireguard"
	case "macro":
		return "macros"
	case "screenshot":
		return "node_screenshot"
	default:
		return text
	}
}

func normalizeAgentRoleHealth(raw any, defaultContext string) map[string]any {
	candidate := raw
	if text := cleanText(candidate); text != "" {
		if _, ok := candidate.(string); ok {
			var decoded any
			if err := json.Unmarshal([]byte(text), &decoded); err == nil {
				candidate = decoded
			} else {
				candidate = nil
			}
		}
	}
	payload := map[string]any{"roles": []map[string]any{}, "reported_at": int64(0), "supervisor_revision": int64(0)}
	var roles []any
	switch typed := candidate.(type) {
	case []any:
		roles = typed
	case []map[string]any:
		for _, item := range typed {
			roles = append(roles, item)
		}
	case map[string]any:
		roles = anySlice(typed["roles"])
		payload["reported_at"] = coerceInt64(typed["reported_at"])
		payload["supervisor_revision"] = coerceInt64(typed["supervisor_revision"])
	}
	reportedAt := coerceInt64(payload["reported_at"])
	normalizedContext := normalizeAgentServiceMode(defaultContext)
	outRoles := make([]map[string]any, 0, len(roles))
	for _, item := range roles {
		roleMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		roleName := normalizeAgentRoleName(firstNonEmpty(roleMap["role_name"], roleMap["role"], roleMap["name"], roleMap["role_label"], roleMap["label"]))
		if roleName == "" {
			continue
		}
		contextValue := normalizeAgentServiceMode(firstNonEmpty(roleMap["context"], normalizedContext))
		statusCode := normalizeAgentRoleStatus(firstNonEmpty(roleMap["status_code"], roleMap["status"]))
		lastChecked := coerceInt64(firstNonEmpty(roleMap["last_checked_at"], roleMap["checked_at"], reportedAt))
		details := cleanAgentDetailsMap(firstNonEmpty(roleMap["details"], roleMap["metadata"], roleMap["info"]))
		role := map[string]any{
			"role_id":         contextValue + ":" + roleName,
			"role_name":       roleName,
			"role_label":      firstText(cleanText(firstNonEmpty(roleMap["role_label"], roleMap["label"])), humanizeAgentRoleName(roleName)),
			"context":         contextValue,
			"status_code":     statusCode,
			"status":          agentRoleStatusLabel(statusCode),
			"detail":          cleanText(firstNonEmpty(roleMap["detail"], roleMap["message"])),
			"details":         details,
			"last_checked_at": lastChecked,
		}
		for _, key := range []string{"desired_state", "observed_state", "last_error"} {
			if text := firstText(cleanText(roleMap[key]), cleanText(details[key])); text != "" {
				role[key] = text
			}
		}
		for _, key := range []string{"last_success_at", "recovery_attempts"} {
			if value := coerceInt64(roleMap[key]); value > 0 {
				role[key] = value
			}
		}
		outRoles = append(outRoles, role)
	}
	deduped := map[string]map[string]any{}
	for _, role := range outRoles {
		deduped[cleanText(role["role_id"])] = role
	}
	outRoles = outRoles[:0]
	for _, role := range deduped {
		outRoles = append(outRoles, role)
	}
	sort.Slice(outRoles, func(i, j int) bool {
		left := strings.ToLower(cleanText(outRoles[i]["role_label"])) + "|" + strings.ToLower(cleanText(outRoles[i]["context"]))
		right := strings.ToLower(cleanText(outRoles[j]["role_label"])) + "|" + strings.ToLower(cleanText(outRoles[j]["context"]))
		return left < right
	})
	if reportedAt == 0 {
		for _, role := range outRoles {
			if checked := coerceInt64(role["last_checked_at"]); checked > reportedAt {
				reportedAt = checked
			}
		}
	}
	payload["roles"] = outRoles
	payload["reported_at"] = reportedAt
	return payload
}

func mergeAgentRoleHealth(existingRaw any, incomingRaw any, incomingContext string) map[string]any {
	existing := normalizeAgentRoleHealth(existingRaw, "")
	incoming := normalizeAgentRoleHealth(incomingRaw, incomingContext)
	replaceContexts := map[string]bool{}
	for _, role := range mapSlice(incoming["roles"]) {
		contextValue := normalizeAgentServiceMode(role["context"])
		if contextValue != "unknown" {
			replaceContexts[contextValue] = true
		}
	}
	if contextValue := normalizeAgentServiceMode(incomingContext); contextValue != "" && contextValue != "unknown" {
		replaceContexts[contextValue] = true
	}
	merged := map[string]map[string]any{}
	for _, role := range mapSlice(existing["roles"]) {
		if len(replaceContexts) == 0 || !replaceContexts[normalizeAgentServiceMode(role["context"])] {
			merged[cleanText(role["role_id"])] = role
		}
	}
	for _, role := range mapSlice(incoming["roles"]) {
		merged[cleanText(role["role_id"])] = role
	}
	return buildAgentRoleHealthPayload(merged, coerceInt64(existing["reported_at"]), coerceInt64(incoming["reported_at"]), maxInt64(coerceInt64(existing["supervisor_revision"]), coerceInt64(incoming["supervisor_revision"])))
}

func upsertSingleAgentRoleHealth(existingRaw any, role map[string]any) map[string]any {
	existing := normalizeAgentRoleHealth(existingRaw, "")
	merged := map[string]map[string]any{}
	roleID := cleanText(role["role_id"])
	for _, item := range mapSlice(existing["roles"]) {
		if cleanText(item["role_id"]) != roleID {
			merged[cleanText(item["role_id"])] = item
		}
	}
	merged[roleID] = role
	return buildAgentRoleHealthPayload(merged, coerceInt64(existing["reported_at"]), coerceInt64(role["last_checked_at"]), coerceInt64(existing["supervisor_revision"]))
}

func buildAgentRoleHealthPayload(rolesByID map[string]map[string]any, reportedA int64, reportedB int64, supervisorRevision int64) map[string]any {
	roles := make([]map[string]any, 0, len(rolesByID))
	reportedAt := maxInt64(reportedA, reportedB)
	for id, role := range rolesByID {
		if id == "" {
			continue
		}
		roles = append(roles, role)
		if checked := coerceInt64(role["last_checked_at"]); checked > reportedAt {
			reportedAt = checked
		}
	}
	sort.Slice(roles, func(i, j int) bool {
		left := strings.ToLower(cleanText(roles[i]["role_label"])) + "|" + strings.ToLower(cleanText(roles[i]["context"]))
		right := strings.ToLower(cleanText(roles[j]["role_label"])) + "|" + strings.ToLower(cleanText(roles[j]["context"]))
		return left < right
	})
	return map[string]any{"roles": roles, "reported_at": reportedAt, "supervisor_revision": supervisorRevision}
}

func serializeAgentRoleHealth(payload any) string {
	normalized := normalizeAgentRoleHealth(payload, "")
	encoded, _ := json.Marshal(normalized)
	return string(encoded)
}

func cleanAgentDetailsMap(value any) map[string]any {
	typed, ok := value.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	cleaned := map[string]any{}
	for key, item := range typed {
		key = cleanText(key)
		if key != "" {
			cleaned[key] = cleanText(item)
		}
	}
	return cleaned
}

func humanizeAgentRoleName(value string) string {
	text := strings.ReplaceAll(value, "_", " ")
	parts := strings.Fields(text)
	for index, part := range parts {
		if strings.ToUpper(part) == part {
			continue
		}
		parts[index] = strings.ToUpper(part[:1]) + part[1:]
	}
	if len(parts) == 0 {
		return "Unknown Role"
	}
	return strings.Join(parts, " ")
}

func agentStatusCacheKey(guid string, serviceMode string) string {
	return firstText(normalizeCanonicalGUID(guid), cleanText(guid)) + "|" + normalizeAgentServiceMode(serviceMode)
}

func agentStatusSignature(payload map[string]any) string {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Sprint(payload)
	}
	return string(encoded)
}

func normalizedAgentStatusSignature(payload map[string]any) string {
	lastError := payload["last_error"]
	switch lastError.(type) {
	case map[string]any, []any, string:
	default:
		lastError = nil
	}
	return agentStatusSignature(map[string]any{
		"boot_id":    cleanText(payload["boot_id"]),
		"phase":      cleanText(payload["phase"]),
		"message":    cleanText(payload["message"]),
		"status":     normalizeAgentStartupStatus(payload["status"]),
		"milestones": anySlice(payload["milestones"]),
		"last_error": lastError,
	})
}

func jsonString(value any) (string, bool) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

func mustJSONString(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func anySlice(value any) []any {
	switch typed := value.(type) {
	case []any:
		return typed
	case []map[string]any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, item)
		}
		return out
	default:
		return []any{}
	}
}

func mapSlice(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		return typed
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if row, ok := item.(map[string]any); ok {
				out = append(out, row)
			}
		}
		return out
	default:
		return []map[string]any{}
	}
}

func nullableInt64Ptr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	parsed := value.Int64
	return &parsed
}

func stringSetContains(values map[string]bool, candidate string) bool {
	return values[candidate]
}

func logDebug(scope string, message string) {
	fmt.Printf("[%s] %s\n", scope, message)
}
