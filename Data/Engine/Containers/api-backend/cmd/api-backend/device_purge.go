package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
)

type devicePurgeResult struct {
	Payload map[string]any
	AgentID string
}

type devicePurgeRuntime struct {
	vpn *vpnTunnelService
	vnc *vncRuntime
}

type devicePurgeRecord struct {
	GUID         string
	Hostname     string
	AgentID      string
	Fingerprint  string
	TokenVersion int
	SiteID       *int64
}

type devicePurgeCondition struct {
	Clause string
	Args   []any
}

func devicePurgeHandler(auth *authService, runtime devicePurgeRuntime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, failure := requireAdmin(r.Context(), auth, r)
		if failure != nil {
			failure.write(w)
			return
		}
		guid := normalizeCanonicalGUID(r.PathValue("guid"))
		if guid == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid guid"})
			return
		}
		store, ok := auth.store.(devicePurgeStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "device_purge_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()

		profile := operatorProfile{Username: identity.Username, Role: identity.Role}
		result, status, err := store.purgeDevice(ctx, profile, guid)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		payload := copyMap(result.Payload)
		if status == http.StatusOK {
			payload["runtime_cleanup"] = runtime.cleanup(r.Context(), result.AgentID)
		}
		writeJSON(w, status, payload)
	}
}

func (r devicePurgeRuntime) cleanup(ctx context.Context, agentID string) map[string]any {
	return r.cleanupWithReason(ctx, agentID, "device_purged")
}

func (r devicePurgeRuntime) cleanupWithReason(ctx context.Context, agentID string, reason string) map[string]any {
	agentID = cleanText(agentID)
	summary := map[string]any{
		"vpn_disconnected":       false,
		"vnc_sessions_revoked":   int64(0),
		"vnc_connections_closed": int64(0),
	}
	if agentID == "" {
		return summary
	}
	if r.vnc != nil {
		summary["vnc_sessions_revoked"] = int64(r.vnc.revokeAgent(agentID))
	}
	if r.vpn != nil {
		summary["vpn_disconnected"] = r.vpn.disconnect(ctx, agentID, firstText(cleanText(reason), "device_contained"), true)
	}
	return summary
}

func (v *vncRuntime) revokeAgent(agentID string) int {
	if v == nil {
		return 0
	}
	agentID = cleanText(agentID)
	if agentID == "" {
		return 0
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	sessionID := v.byAgent[agentID]
	if sessionID == "" {
		return 0
	}
	if session := v.byID[sessionID]; session != nil {
		delete(v.byID, session.SessionID)
		delete(v.byAgent, session.AgentID)
		return 1
	}
	delete(v.byAgent, agentID)
	return 0
}

func (s *postgresOperatorStore) purgeDevice(ctx context.Context, profile operatorProfile, guid string) (devicePurgeResult, int, error) {
	normalizedGUID := normalizeCanonicalGUID(guid)
	if normalizedGUID == "" {
		return devicePurgeResult{Payload: map[string]any{"error": "invalid guid"}}, http.StatusBadRequest, nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return devicePurgeResult{}, 0, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return devicePurgeResult{}, 0, err
	}
	defer rollbackQuietly(tx)

	if err := ensureDevicePurgeBarrierTable(ctx, tx); err != nil {
		return devicePurgeResult{}, 0, err
	}
	record, found, err := loadDevicePurgeRecord(ctx, tx, normalizedGUID)
	if err != nil {
		return devicePurgeResult{}, 0, err
	}
	if !found {
		return devicePurgeResult{Payload: map[string]any{"error": "not found"}}, http.StatusNotFound, nil
	}
	barrier, err := upsertDevicePurgeBarrier(ctx, tx, record, profile.Username)
	if err != nil {
		return devicePurgeResult{}, 0, err
	}
	scheduledJobs, err := rewriteScheduledJobsForDevicePurge(ctx, tx, record)
	if err != nil {
		return devicePurgeResult{}, 0, err
	}
	deletedRows, err := purgeDeviceRows(ctx, tx, record)
	if err != nil {
		return devicePurgeResult{}, 0, err
	}
	if err := tx.Commit(); err != nil {
		return devicePurgeResult{}, 0, err
	}
	payload := map[string]any{
		"status":                 "purged",
		"device_guid":            record.GUID,
		"hostname":               record.Hostname,
		"required_token_version": barrier["required_token_version"],
		"scheduled_jobs":         scheduledJobs,
		"deleted_rows":           deletedRows,
	}
	return devicePurgeResult{Payload: payload, AgentID: record.AgentID}, http.StatusOK, nil
}

func ensureDevicePurgeBarrierTable(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS engine.device_purge_barriers (
			guid TEXT PRIMARY KEY,
			required_token_version INTEGER NOT NULL,
			purged_at TEXT NOT NULL,
			purged_by TEXT,
			last_hostname TEXT,
			last_agent_id TEXT
		)
	`); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_device_purge_barriers_required_token_version
		    ON engine.device_purge_barriers(required_token_version)
	`)
	return err
}

func loadDevicePurgeRecord(ctx context.Context, tx *sql.Tx, guid string) (devicePurgeRecord, bool, error) {
	var rawGUID, hostname, agentID, fingerprint sql.NullString
	var tokenVersion sql.NullInt64
	var siteID sql.NullInt64
	err := tx.QueryRowContext(ctx, `
		SELECT d.guid,
		       d.hostname,
		       d.agent_id,
		       d.ssl_key_fingerprint,
		       d.token_version,
		       ds.site_id
		  FROM engine.devices AS d
	 LEFT JOIN engine.device_sites AS ds ON ds.device_hostname = d.hostname
		 WHERE UPPER(d.guid) = UPPER($1)
		 LIMIT 1
	`, guid).Scan(&rawGUID, &hostname, &agentID, &fingerprint, &tokenVersion, &siteID)
	if errors.Is(err, sql.ErrNoRows) {
		return devicePurgeRecord{}, false, nil
	}
	if err != nil {
		return devicePurgeRecord{}, false, err
	}
	requiredSiteID := (*int64)(nil)
	if siteID.Valid {
		value := siteID.Int64
		requiredSiteID = &value
	}
	version := 1
	if tokenVersion.Valid && tokenVersion.Int64 > 1 {
		version = int(tokenVersion.Int64)
	}
	record := devicePurgeRecord{
		GUID:         firstText(normalizeCanonicalGUID(nullString(rawGUID)), guid),
		Hostname:     nullString(hostname),
		AgentID:      nullString(agentID),
		Fingerprint:  strings.ToLower(nullString(fingerprint)),
		TokenVersion: version,
		SiteID:       requiredSiteID,
	}
	return record, true, nil
}

func upsertDevicePurgeBarrier(ctx context.Context, tx *sql.Tx, record devicePurgeRecord, purgedBy string) (map[string]any, error) {
	requiredTokenVersion := record.TokenVersion + 1
	now := isoUTC(time.Now())
	var storedRequired sql.NullInt64
	var storedPurgedAt, storedPurgedBy, storedHostname, storedAgentID sql.NullString
	err := tx.QueryRowContext(ctx, `
		INSERT INTO engine.device_purge_barriers (
			guid,
			required_token_version,
			purged_at,
			purged_by,
			last_hostname,
			last_agent_id
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT(guid) DO UPDATE SET
			required_token_version = GREATEST(device_purge_barriers.required_token_version, EXCLUDED.required_token_version),
			purged_at = EXCLUDED.purged_at,
			purged_by = COALESCE(NULLIF(EXCLUDED.purged_by, ''), device_purge_barriers.purged_by),
			last_hostname = COALESCE(NULLIF(EXCLUDED.last_hostname, ''), device_purge_barriers.last_hostname),
			last_agent_id = COALESCE(NULLIF(EXCLUDED.last_agent_id, ''), device_purge_barriers.last_agent_id)
		RETURNING required_token_version, purged_at, purged_by, last_hostname, last_agent_id
	`, record.GUID, requiredTokenVersion, now, cleanText(purgedBy), record.Hostname, record.AgentID).Scan(
		&storedRequired,
		&storedPurgedAt,
		&storedPurgedBy,
		&storedHostname,
		&storedAgentID,
	)
	if err != nil {
		return nil, err
	}
	required := int64(1)
	if storedRequired.Valid && storedRequired.Int64 > 1 {
		required = storedRequired.Int64
	}
	return map[string]any{
		"guid":                   record.GUID,
		"required_token_version": required,
		"purged_at":              nullString(storedPurgedAt),
		"purged_by":              nullString(storedPurgedBy),
		"last_hostname":          nullString(storedHostname),
		"last_agent_id":          nullString(storedAgentID),
	}, nil
}

func rewriteScheduledJobsForDevicePurge(ctx context.Context, tx *sql.Tx, record devicePurgeRecord) (map[string]any, error) {
	exists, err := relationExistsTx(ctx, tx, "engine.scheduled_jobs")
	if err != nil || !exists {
		return map[string]any{"updated": int64(0), "deleted": int64(0), "targets_removed": int64(0)}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, targets_json FROM engine.scheduled_jobs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type scheduledJobTargetRow struct {
		ID      int64
		Targets sql.NullString
	}
	jobs := []scheduledJobTargetRow{}
	for rows.Next() {
		var row scheduledJobTargetRow
		if err := rows.Scan(&row.ID, &row.Targets); err != nil {
			return nil, err
		}
		jobs = append(jobs, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	now := time.Now().Unix()
	summary := map[string]any{"updated": int64(0), "deleted": int64(0), "targets_removed": int64(0)}
	for _, job := range jobs {
		var rawTargets []any
		if err := json.Unmarshal([]byte(nullString(job.Targets)), &rawTargets); err != nil {
			rawTargets = []any{}
		}
		updatedTargets, removedCount := pruneScheduledTargetsForDevice(rawTargets, record.GUID, record.Hostname, record.SiteID)
		if removedCount <= 0 {
			continue
		}
		summary["targets_removed"] = summary["targets_removed"].(int64) + int64(removedCount)
		if len(updatedTargets) == 0 {
			result, err := tx.ExecContext(ctx, `DELETE FROM engine.scheduled_jobs WHERE id = $1`, job.ID)
			if err != nil {
				return nil, err
			}
			affected, _ := result.RowsAffected()
			summary["deleted"] = summary["deleted"].(int64) + affected
			continue
		}
		encodedTargets, err := json.Marshal(updatedTargets)
		if err != nil {
			return nil, err
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE engine.scheduled_jobs
			   SET targets_json = $1,
			       updated_at = $2
			 WHERE id = $3
		`, string(encodedTargets), now, job.ID)
		if err != nil {
			return nil, err
		}
		affected, _ := result.RowsAffected()
		summary["updated"] = summary["updated"].(int64) + affected
	}
	return summary, nil
}

func purgeDeviceRows(ctx context.Context, tx *sql.Tx, record devicePurgeRecord) (map[string]any, error) {
	deleted := map[string]any{}
	activityIDs, err := purgeActivityIDs(ctx, tx, record.Hostname)
	if err != nil {
		return nil, err
	}
	targetRunIDs, err := purgeTargetRunIDs(ctx, tx, record.Hostname)
	if err != nil {
		return nil, err
	}

	deleted["scheduled_job_run_targets"], err = purgeDeleteByInt64List(ctx, tx, "engine.scheduled_job_run_targets", "run_id", targetRunIDs)
	if err != nil {
		return nil, err
	}
	extraTargets, err := purgeDeleteWhere(ctx, tx, "engine.scheduled_job_run_targets", []devicePurgeCondition{
		{Clause: "UPPER(device_guid) = UPPER(?)", Args: nonEmptyArgs(record.GUID)},
		{Clause: "LOWER(hostname) = LOWER(?)", Args: nonEmptyArgs(record.Hostname)},
	})
	if err != nil {
		return nil, err
	}
	deleted["scheduled_job_run_targets"] = deleted["scheduled_job_run_targets"].(int64) + extraTargets

	deleted["scheduled_job_run_activity"], err = purgeDeleteByInt64List(ctx, tx, "engine.scheduled_job_run_activity", "run_id", targetRunIDs)
	if err != nil {
		return nil, err
	}
	extraActivity, err := purgeDeleteByInt64List(ctx, tx, "engine.scheduled_job_run_activity", "activity_id", activityIDs)
	if err != nil {
		return nil, err
	}
	deleted["scheduled_job_run_activity"] = deleted["scheduled_job_run_activity"].(int64) + extraActivity

	deleted["scheduled_job_runs"], err = purgeDeleteByInt64List(ctx, tx, "engine.scheduled_job_runs", "id", targetRunIDs)
	if err != nil {
		return nil, err
	}
	deleted["activity_history"], err = purgeDeleteByInt64List(ctx, tx, "engine.activity_history", "id", activityIDs)
	if err != nil {
		return nil, err
	}
	deleted["ansible_play_recaps"], err = purgeDeleteWhere(ctx, tx, "engine.ansible_play_recaps", []devicePurgeCondition{
		{Clause: "scheduled_run_id = ANY(?)", Args: int64ArrayArg(targetRunIDs)},
		{Clause: "LOWER(hostname) = LOWER(?)", Args: nonEmptyArgs(record.Hostname)},
		{Clause: "agent_id = ?", Args: nonEmptyArgs(record.AgentID)},
	})
	if err != nil {
		return nil, err
	}
	deleted["workflow_child_jobs"], err = purgeDeleteWhere(ctx, tx, "engine.workflow_child_jobs", []devicePurgeCondition{
		{Clause: "LOWER(target_hostname) = LOWER(?)", Args: nonEmptyArgs(record.Hostname)},
	})
	if err != nil {
		return nil, err
	}
	deleted["device_software_inventory"], err = purgeDeleteWhere(ctx, tx, "engine.device_software_inventory", []devicePurgeCondition{
		{Clause: "UPPER(device_guid) = UPPER(?)", Args: nonEmptyArgs(record.GUID)},
	})
	if err != nil {
		return nil, err
	}
	deleted["device_sites"], err = purgeDeleteWhere(ctx, tx, "engine.device_sites", []devicePurgeCondition{
		{Clause: "LOWER(device_hostname) = LOWER(?)", Args: nonEmptyArgs(record.Hostname)},
	})
	if err != nil {
		return nil, err
	}
	deleted["device_approvals"], err = purgeDeleteWhere(ctx, tx, "engine.device_approvals", []devicePurgeCondition{
		{Clause: "UPPER(guid) = UPPER(?)", Args: nonEmptyArgs(record.GUID)},
		{Clause: "LOWER(hostname_claimed) = LOWER(?)", Args: nonEmptyArgs(record.Hostname)},
		{Clause: "LOWER(ssl_key_fingerprint_claimed) = LOWER(?)", Args: nonEmptyArgs(record.Fingerprint)},
	})
	if err != nil {
		return nil, err
	}
	deleted["refresh_tokens"], err = purgeDeleteWhere(ctx, tx, "engine.refresh_tokens", []devicePurgeCondition{
		{Clause: "UPPER(guid) = UPPER(?)", Args: nonEmptyArgs(record.GUID)},
	})
	if err != nil {
		return nil, err
	}
	deleted["device_keys"], err = purgeDeleteWhere(ctx, tx, "engine.device_keys", []devicePurgeCondition{
		{Clause: "UPPER(guid) = UPPER(?)", Args: nonEmptyArgs(record.GUID)},
	})
	if err != nil {
		return nil, err
	}
	deleted["device_vpn_config"], err = purgeDeleteWhere(ctx, tx, "engine.device_vpn_config", []devicePurgeCondition{
		{Clause: "agent_id = ?", Args: nonEmptyArgs(record.AgentID)},
	})
	if err != nil {
		return nil, err
	}
	deleted["device_vpn_ip_leases"], err = purgeDeleteWhere(ctx, tx, "engine.device_vpn_ip_leases", []devicePurgeCondition{
		{Clause: "agent_id = ?", Args: nonEmptyArgs(record.AgentID)},
	})
	if err != nil {
		return nil, err
	}
	deleted["device_vpn_key_leases"], err = purgeDeleteWhere(ctx, tx, "engine.device_vpn_key_leases", []devicePurgeCondition{
		{Clause: "agent_id = ?", Args: nonEmptyArgs(record.AgentID)},
	})
	if err != nil {
		return nil, err
	}
	deleted["agent_service_account"], err = purgeDeleteWhere(ctx, tx, "engine.agent_service_account", []devicePurgeCondition{
		{Clause: "agent_id = ?", Args: nonEmptyArgs(record.AgentID)},
	})
	if err != nil {
		return nil, err
	}
	deleted["devices"], err = purgeDeleteWhere(ctx, tx, "engine.devices", []devicePurgeCondition{
		{Clause: "UPPER(guid) = UPPER(?)", Args: nonEmptyArgs(record.GUID)},
	})
	if err != nil {
		return nil, err
	}
	return deleted, nil
}

func purgeActivityIDs(ctx context.Context, tx *sql.Tx, hostname string) ([]int64, error) {
	if cleanText(hostname) == "" {
		return nil, nil
	}
	return purgeSelectInt64List(ctx, tx, "engine.activity_history", "id", "LOWER(hostname) = LOWER(?)", hostname)
}

func purgeTargetRunIDs(ctx context.Context, tx *sql.Tx, hostname string) ([]int64, error) {
	if cleanText(hostname) == "" {
		return nil, nil
	}
	return purgeSelectInt64List(ctx, tx, "engine.scheduled_job_runs", "id", "LOWER(target_hostname) = LOWER(?)", hostname)
}

func purgeSelectInt64List(ctx context.Context, tx *sql.Tx, table string, column string, clause string, args ...any) ([]int64, error) {
	exists, err := relationExistsTx(ctx, tx, table)
	if err != nil || !exists {
		return nil, err
	}
	where, err := purgeNumberedClause(clause, 1)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT "+column+" FROM "+table+" WHERE "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []int64{}
	for rows.Next() {
		var value sql.NullInt64
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		if value.Valid {
			values = append(values, value.Int64)
		}
	}
	return values, rows.Err()
}

func purgeDeleteByInt64List(ctx context.Context, tx *sql.Tx, table string, column string, values []int64) (int64, error) {
	if len(values) == 0 {
		return 0, nil
	}
	return purgeDeleteWhere(ctx, tx, table, []devicePurgeCondition{
		{Clause: column + " = ANY(?)", Args: int64ArrayArg(values)},
	})
}

func purgeDeleteWhere(ctx context.Context, tx *sql.Tx, table string, conditions []devicePurgeCondition) (int64, error) {
	exists, err := relationExistsTx(ctx, tx, table)
	if err != nil || !exists {
		return 0, err
	}
	clauses := []string{}
	params := []any{}
	nextArg := 1
	for _, condition := range conditions {
		if strings.TrimSpace(condition.Clause) == "" || len(condition.Args) == 0 {
			continue
		}
		clause, err := purgeNumberedClause(condition.Clause, nextArg)
		if err != nil {
			return 0, err
		}
		nextArg += strings.Count(condition.Clause, "?")
		clauses = append(clauses, "("+clause+")")
		params = append(params, condition.Args...)
	}
	if len(clauses) == 0 {
		return 0, nil
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE "+strings.Join(clauses, " OR "), params...)
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return affected, nil
}

func purgeNumberedClause(clause string, start int) (string, error) {
	var builder strings.Builder
	next := start
	for _, ch := range clause {
		if ch == '?' {
			builder.WriteString("$")
			builder.WriteString(strconv.Itoa(next))
			next++
			continue
		}
		builder.WriteRune(ch)
	}
	if next == start {
		return "", errors.New("purge clause missing placeholder")
	}
	return builder.String(), nil
}

func nonEmptyArgs(value string) []any {
	if cleanText(value) == "" {
		return nil
	}
	return []any{value}
}

func int64ArrayArg(values []int64) []any {
	if len(values) == 0 {
		return nil
	}
	return []any{pq.Array(values)}
}

func pruneScheduledTargetsForDevice(entries []any, deviceGUID string, hostname string, siteID *int64) ([]any, int) {
	normalizedGUID := normalizeCanonicalGUID(deviceGUID)
	normalizedHost := strings.ToLower(cleanText(hostname))
	normalizedTargets := normalizeScheduledTargetsForPurge(entries)
	updated := []any{}
	removed := 0
	for _, entry := range normalizedTargets {
		drop := false
		switch typed := entry.(type) {
		case string:
			drop = normalizedHost != "" && strings.EqualFold(strings.TrimSpace(typed), normalizedHost)
		case map[string]any:
			kind := strings.ToLower(firstText(cleanText(typed["kind"]), cleanText(typed["type"])))
			if kind == "all_devices" || boolFromAny(typed["all_devices"]) || kind == "filter" || typed["filter_id"] != nil {
				drop = false
			} else {
				entryGUID := normalizeCanonicalGUID(firstText(cleanText(typed["device_guid"]), cleanText(typed["guid"])))
				entryHost := strings.ToLower(cleanText(typed["hostname"]))
				entrySiteID := normalizePurgeSiteID(typed["site_id"])
				if normalizedGUID != "" && entryGUID != "" && strings.EqualFold(entryGUID, normalizedGUID) {
					drop = true
				} else if normalizedHost != "" && entryHost == normalizedHost {
					drop = siteID == nil || entrySiteID == nil || *entrySiteID == *siteID
				}
			}
		}
		if drop {
			removed++
			continue
		}
		updated = append(updated, entry)
	}
	return updated, removed
}

func normalizeScheduledTargetsForPurge(entries []any) []any {
	normalized := []any{}
	seenFilters := map[int64]struct{}{}
	seenDevices := map[string]struct{}{}
	seenScopes := map[string]struct{}{}
	includeAll := false
	for _, entry := range entries {
		switch typed := entry.(type) {
		case string:
			host := cleanText(typed)
			if host == "" {
				continue
			}
			key := strings.ToLower(host)
			if _, ok := seenDevices[key]; ok {
				continue
			}
			seenDevices[key] = struct{}{}
			normalized = append(normalized, host)
		case map[string]any:
			kind := strings.ToLower(firstText(cleanText(typed["kind"]), cleanText(typed["type"])))
			switch {
			case kind == "onboarding_scope":
				siteID := normalizePurgeSiteID(firstNonEmpty(typed["site_id"], typed["siteId"]))
				if siteID == nil {
					continue
				}
				scopeEntries := normalizePurgeScopeEntries(firstNonEmpty(typed["entries"], typed["scope"], typed["targets"], typed["discovery_scope"]))
				if len(scopeEntries) == 0 {
					continue
				}
				exclusions := normalizePurgeScopeEntries(firstNonEmpty(typed["exclusions"], typed["exclude_entries"], typed["exclusion_scope"], typed["exclusionScope"]))
				key := "onboarding:" + strconv.FormatInt(*siteID, 10) + ":" + strings.Join(lowerTrimList(scopeEntries), "|") + ":" + strings.Join(lowerTrimList(exclusions), "|")
				if _, ok := seenScopes[key]; ok {
					continue
				}
				seenScopes[key] = struct{}{}
				normalized = append(normalized, map[string]any{
					"kind":       "onboarding_scope",
					"site_id":    *siteID,
					"site_name":  firstNonEmpty(typed["site_name"], typed["site"]),
					"entries":    scopeEntries,
					"exclusions": exclusions,
				})
			case kind == "all_devices" || boolFromAny(typed["all_devices"]):
				if includeAll {
					continue
				}
				includeAll = true
				name := firstNonEmpty(typed["name"], "All Devices in Scope")
				normalized = append(normalized, map[string]any{"kind": "all_devices", "name": name})
			case kind == "filter" || typed["filter_id"] != nil:
				filterID := normalizePurgeSiteID(firstNonEmpty(typed["filter_id"], typed["id"]))
				if filterID == nil {
					continue
				}
				if _, ok := seenFilters[*filterID]; ok {
					continue
				}
				seenFilters[*filterID] = struct{}{}
				normalized = append(normalized, map[string]any{"kind": "filter", "filter_id": *filterID, "name": typed["name"]})
			default:
				host := cleanText(typed["hostname"])
				if host == "" {
					continue
				}
				deviceGUID := strings.ToLower(normalizeCanonicalGUID(firstText(cleanText(typed["device_guid"]), cleanText(typed["guid"]))))
				siteID := normalizePurgeSiteID(typed["site_id"])
				key := strings.ToLower(host)
				if deviceGUID != "" {
					key = "guid:" + deviceGUID
				} else if siteID != nil {
					key = "site:" + strconv.FormatInt(*siteID, 10) + ":" + strings.ToLower(host)
				}
				if _, ok := seenDevices[key]; ok {
					continue
				}
				seenDevices[key] = struct{}{}
				target := map[string]any{
					"kind":        "device",
					"device_guid": deviceGUID,
					"hostname":    host,
					"site_id":     nil,
					"site_name":   firstNonEmpty(typed["site_name"], typed["site"]),
				}
				if siteID != nil {
					target["site_id"] = *siteID
				}
				normalized = append(normalized, target)
			}
		}
	}
	return normalized
}

func normalizePurgeSiteID(value any) *int64 {
	text := cleanText(value)
	if text == "" || strings.EqualFold(text, "null") {
		return nil
	}
	parsed, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		if floatValue, ok := value.(float64); ok {
			parsed = int64(floatValue)
		} else {
			return nil
		}
	}
	return &parsed
}

func normalizePurgeScopeEntries(value any) []string {
	switch typed := value.(type) {
	case string:
		text := strings.ReplaceAll(strings.ReplaceAll(typed, "\r\n", "\n"), "\r", "\n")
		if text == "" {
			return nil
		}
		return strings.Split(text, "\n")
	case []any:
		out := []string{}
		for _, item := range typed {
			itemText := cleanText(item)
			if itemText != "" {
				out = append(out, itemText)
			}
		}
		return out
	case []string:
		out := []string{}
		for _, item := range typed {
			itemText := cleanText(item)
			if itemText != "" {
				out = append(out, itemText)
			}
		}
		return out
	default:
		return nil
	}
}

func lowerTrimList(values []string) []string {
	out := []string{}
	for _, value := range values {
		if text := strings.ToLower(cleanText(value)); text != "" {
			out = append(out, text)
		}
	}
	return out
}
