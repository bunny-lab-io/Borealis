package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type patchInventoryStore interface {
	listPatchAudit(ctx context.Context, profile operatorProfile) ([]map[string]any, error)
	listDevicePatches(ctx context.Context, profile operatorProfile, hostname string) ([]map[string]any, string, int, error)
}

type patchInventoryRow struct {
	ID              sql.NullInt64
	DeviceGUID      sql.NullString
	PatchKey        sql.NullString
	KB              sql.NullString
	Title           sql.NullString
	State           sql.NullString
	Source          sql.NullString
	Classification  sql.NullString
	Severity        sql.NullString
	InstalledOn     sql.NullInt64
	PublishedAt     sql.NullInt64
	CapturedAt      sql.NullInt64
	MetadataJSON    sql.NullString
	GUID            sql.NullString
	Hostname        sql.NullString
	AgentID         sql.NullString
	OperatingSystem sql.NullString
	SiteID          sql.NullInt64
	SiteName        sql.NullString
}

func registerPatchRoutes(mux *http.ServeMux, auth *authService, fallback http.Handler) {
	mux.HandleFunc("GET /api/patches/audit", patchAuditHandler(auth))
	mux.HandleFunc("GET /api/device/patches/{hostname}", devicePatchesHandler(auth))
	mux.HandleFunc("POST /api/device/patches/{hostname}/refresh", patchRefreshHandler(auth))
	mux.HandleFunc("/api/patches/", patchSubtreeHandler(fallback))
}

func patchAuditHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, failure := requireUser(r.Context(), auth, r)
		if failure != nil {
			failure.write(w)
			return
		}
		store, ok := auth.store.(patchInventoryStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "patch_audit_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		profile, err := auth.store.lookupOperator(ctx, identity.Username, identity.Role)
		if err != nil {
			if errors.Is(err, errOperatorNotFound) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "operator_lookup_failed", "message": err.Error()})
			return
		}
		rows, err := store.listPatchAudit(ctx, profile)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "patch_audit_failed", "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"patches": rows, "count": len(rows)})
	}
}

func devicePatchesHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		profile, err := auth.currentProfile(r.Context(), r)
		if err != nil {
			if isUnauthorizedAuthError(err) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
			return
		}
		store, ok := auth.store.(patchInventoryStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "patch_inventory_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		rows, hostname, status, err := store.listDevicePatches(ctx, profile, r.PathValue("hostname"))
		if err != nil {
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":   "ok",
			"hostname": hostname,
			"patches":  rows,
			"count":    len(rows),
		})
	}
}

func patchRefreshHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		profile, err := auth.currentProfile(r.Context(), r)
		if err != nil {
			if isUnauthorizedAuthError(err) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
			return
		}
		store, ok := auth.store.(deviceProcessStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "patch_refresh_unavailable"})
			return
		}
		hostname := r.PathValue("hostname")
		requestedAt := time.Now().Unix()
		deadline := time.Now().Add(8 * time.Second)
		var lastSnapshot deviceProcessContext
		for {
			ctx, cancel := requestTimeout(r.Context(), auth)
			snapshot, status, err := store.loadDeviceProcessContext(ctx, profile, hostname)
			cancel()
			if err != nil {
				writeJSON(w, status, map[string]any{"error": err.Error()})
				return
			}
			lastSnapshot = snapshot
			if snapshot.Route != nil {
				eventPayload := map[string]any{
					"hostname":     snapshot.Hostname,
					"agent_id":     snapshot.AgentID,
					"requested_at": requestedAt,
					"requested_by": firstText(cleanText(profile.Username), "unknown"),
					"reason":       "operator_query_patch_inventory",
				}
				result, _, workerErr := emitWorkerHostServiceEvent(r.Context(), auth, snapshot.Route, map[string]any{
					"hostname":            snapshot.Hostname,
					"service_mode":        "system",
					"event_name":          "patch_inventory_refresh_request",
					"payload":             eventPayload,
					"allow_pending":       true,
					"pending_ttl_seconds": int64(180),
				}, 6*time.Second)
				if workerErr == nil && (boolFromAny(result["emitted"]) || boolFromAny(result["queued"])) {
					writeJSON(w, http.StatusOK, map[string]any{
						"status":       "queued",
						"hostname":     snapshot.Hostname,
						"agent_id":     snapshot.AgentID,
						"requested_at": requestedAt,
					})
					return
				}
			}
			if time.Now().After(deadline) {
				break
			}
			select {
			case <-r.Context().Done():
				writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "request_canceled"})
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":        "agent_unavailable",
			"message":      "The agent SYSTEM socket is not available to query patch inventory right now.",
			"hostname":     firstText(lastSnapshot.Hostname, hostname),
			"agent_id":     lastSnapshot.AgentID,
			"requested_at": requestedAt,
		})
	}
}

func patchSubtreeHandler(fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if fallback == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
			return
		}
		fallback.ServeHTTP(w, r)
	}
}

func (s *postgresOperatorStore) listPatchAudit(ctx context.Context, profile operatorProfile) ([]map[string]any, error) {
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, err
	}
	if allowedSiteIDs != nil && len(allowedSiteIDs) == 0 {
		return []map[string]any{}, nil
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	query := `
		SELECT
			dpi.id,
			dpi.device_guid,
			dpi.patch_key,
			dpi.kb,
			dpi.title,
			dpi.state,
			dpi.source,
			dpi.classification,
			dpi.severity,
			dpi.installed_on,
			dpi.published_at,
			dpi.captured_at,
			dpi.metadata_json,
			d.guid,
			d.hostname,
			d.agent_id,
			d.operating_system,
			ds.site_id,
			s.name
		  FROM engine.device_patch_inventory AS dpi
		  JOIN engine.devices AS d
		    ON d.guid = dpi.device_guid
	 LEFT JOIN engine.device_sites AS ds
		    ON ds.device_hostname = d.hostname
	 LEFT JOIN engine.sites AS s
		    ON s.id = ds.site_id
		 WHERE TRIM(COALESCE(dpi.title, dpi.kb, '')) <> ''
	`
	args := []any{}
	if allowedSiteIDs != nil {
		placeholders := make([]string, 0, len(allowedSiteIDs))
		for idx, siteID := range allowedSiteIDs {
			placeholders = append(placeholders, "$"+strconv.Itoa(idx+1))
			args = append(args, siteID)
		}
		query += " AND ds.site_id IN (" + strings.Join(placeholders, ",") + ")"
	}
	query += " ORDER BY LOWER(COALESCE(dpi.kb, '')), LOWER(dpi.title), LOWER(d.hostname)"

	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := []map[string]any{}
	for rows.Next() {
		row, err := scanPatchInventoryRow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, patchInventoryPayload(row))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *postgresOperatorStore) listDevicePatches(ctx context.Context, profile operatorProfile, hostname string) ([]map[string]any, string, int, error) {
	hostname = cleanText(hostname)
	if hostname == "" {
		return nil, "", http.StatusNotFound, errors.New("not found")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, "", http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	var device struct {
		GUID            sql.NullString
		Hostname        sql.NullString
		AgentID         sql.NullString
		OperatingSystem sql.NullString
		SiteID          sql.NullInt64
		SiteName        sql.NullString
	}
	err = conn.QueryRowContext(ctx, `
		SELECT d.guid, d.hostname, d.agent_id, d.operating_system, ds.site_id, s.name
		  FROM engine.devices AS d
	 LEFT JOIN engine.device_sites AS ds ON ds.device_hostname = d.hostname
	 LEFT JOIN engine.sites AS s ON s.id = ds.site_id
		 WHERE LOWER(d.hostname) = LOWER($1)
	  ORDER BY COALESCE(d.last_seen, 0) DESC
		 LIMIT 1
	`, hostname).Scan(&device.GUID, &device.Hostname, &device.AgentID, &device.OperatingSystem, &device.SiteID, &device.SiteName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", http.StatusNotFound, errors.New("not found")
	}
	if err != nil {
		return nil, "", http.StatusInternalServerError, err
	}
	if device.SiteID.Valid {
		allowed, err := profileCanAccessSite(ctx, conn, profile, device.SiteID.Int64)
		if err != nil {
			return nil, "", http.StatusInternalServerError, err
		}
		if !allowed {
			return nil, "", http.StatusNotFound, errors.New("not found")
		}
	} else if !strings.EqualFold(strings.TrimSpace(profile.Role), "admin") {
		return nil, "", http.StatusNotFound, errors.New("not found")
	}

	rows, err := conn.QueryContext(ctx, `
		SELECT
			dpi.id,
			dpi.device_guid,
			dpi.patch_key,
			dpi.kb,
			dpi.title,
			dpi.state,
			dpi.source,
			dpi.classification,
			dpi.severity,
			dpi.installed_on,
			dpi.published_at,
			dpi.captured_at,
			dpi.metadata_json,
			$2::text AS guid,
			$3::text AS hostname,
			$4::text AS agent_id,
			$5::text AS operating_system,
			$6::bigint AS site_id,
			$7::text AS site_name
		  FROM engine.device_patch_inventory AS dpi
		 WHERE UPPER(dpi.device_guid)=UPPER($1)
	  ORDER BY LOWER(COALESCE(dpi.kb, '')), LOWER(dpi.title), LOWER(dpi.state)
	`,
		normalizeCanonicalGUID(device.GUID.String),
		nullString(device.GUID),
		nullString(device.Hostname),
		nullString(device.AgentID),
		nullString(device.OperatingSystem),
		nullableInt(device.SiteID),
		nullString(device.SiteName),
	)
	if err != nil {
		return nil, "", http.StatusInternalServerError, err
	}
	defer rows.Close()

	result := []map[string]any{}
	for rows.Next() {
		row, err := scanPatchInventoryRow(rows)
		if err != nil {
			return nil, "", http.StatusInternalServerError, err
		}
		result = append(result, patchInventoryPayload(row))
	}
	if err := rows.Err(); err != nil {
		return nil, "", http.StatusInternalServerError, err
	}
	return result, nullString(device.Hostname), http.StatusOK, nil
}

type patchRowScanner interface {
	Scan(dest ...any) error
}

func scanPatchInventoryRow(scanner patchRowScanner) (patchInventoryRow, error) {
	var row patchInventoryRow
	err := scanner.Scan(
		&row.ID,
		&row.DeviceGUID,
		&row.PatchKey,
		&row.KB,
		&row.Title,
		&row.State,
		&row.Source,
		&row.Classification,
		&row.Severity,
		&row.InstalledOn,
		&row.PublishedAt,
		&row.CapturedAt,
		&row.MetadataJSON,
		&row.GUID,
		&row.Hostname,
		&row.AgentID,
		&row.OperatingSystem,
		&row.SiteID,
		&row.SiteName,
	)
	return row, err
}

func patchInventoryPayload(row patchInventoryRow) map[string]any {
	metadata := agentDetailsMapFromAny(parseJSON(row.MetadataJSON))
	payload := map[string]any{
		"id":               nullInt(row.ID),
		"inventory_id":     nullInt(row.ID),
		"device_guid":      cleanText(nullString(row.DeviceGUID)),
		"patch_key":        cleanText(nullString(row.PatchKey)),
		"kb":               cleanText(nullString(row.KB)),
		"title":            cleanText(nullString(row.Title)),
		"state":            normalizePatchState(nullString(row.State)),
		"source":           normalizePatchSource(nullString(row.Source)),
		"classification":   cleanText(nullString(row.Classification)),
		"severity":         cleanText(nullString(row.Severity)),
		"installed_on":     nullableInt(row.InstalledOn),
		"published_at":     nullableInt(row.PublishedAt),
		"captured_at":      nullableInt(row.CapturedAt),
		"metadata":         metadata,
		"hostname":         cleanText(nullString(row.Hostname)),
		"agent_id":         cleanText(nullString(row.AgentID)),
		"operating_system": cleanText(nullString(row.OperatingSystem)),
		"platform":         normalizeSoftwarePlatform(nullString(row.OperatingSystem)),
		"site_id":          nil,
		"site_name":        cleanText(nullString(row.SiteName)),
	}
	if row.SiteID.Valid {
		payload["site_id"] = row.SiteID.Int64
	}
	for _, key := range []string{"is_downloaded", "is_mandatory", "requires_reboot", "update_id", "revision_number"} {
		if value, ok := metadata[key]; ok {
			payload[key] = value
		}
	}
	return payload
}

var _ patchInventoryStore = (*postgresOperatorStore)(nil)
