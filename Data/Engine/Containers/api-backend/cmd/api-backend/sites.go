package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type siteListStore interface {
	listSites(ctx context.Context, profile operatorProfile) ([]map[string]any, error)
	siteDeviceMap(ctx context.Context, profile operatorProfile, hostnames []string) (map[string]map[string]any, error)
}

type siteMutationStore interface {
	createSite(ctx context.Context, name string, description string) (map[string]any, int, error)
	deleteSites(ctx context.Context, ids []int64) (map[string]any, int, error)
	assignDevicesToSite(ctx context.Context, siteID int64, hostnames []string) (map[string]any, int, error)
	renameSite(ctx context.Context, siteID int64, newName string) (map[string]any, int, error)
	updateSiteAutoApproval(ctx context.Context, siteID int64, autoApproveUntil *int64) (map[string]any, int, error)
}

type siteRow struct {
	ID               sql.NullInt64
	Name             sql.NullString
	Description      sql.NullString
	CreatedAt        sql.NullInt64
	DeviceCount      sql.NullInt64
	EnrollmentCode   sql.NullString
	AutoApproveUntil sql.NullInt64
}

func registerSiteRoutes(mux *http.ServeMux, auth *authService, fallback http.Handler) {
	mux.HandleFunc("/api/sites", siteListHandler(auth, fallback))
	mux.HandleFunc("/api/sites/device_map", siteDeviceMapHandler(auth, fallback))
	mux.HandleFunc("POST /api/sites/delete", siteDeleteHandler(auth))
	mux.HandleFunc("POST /api/sites/assign", siteAssignHandler(auth))
	mux.HandleFunc("POST /api/sites/rename", siteRenameHandler(auth))
	mux.HandleFunc("POST /api/sites/{site_id}/auto-approval", siteAutoApprovalHandler(auth))
}

func siteListHandler(auth *authService, fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleSiteList(w, r, auth)
		case http.MethodPost:
			handleSiteCreate(w, r, auth)
		default:
			proxyFallbackOrMethodNotAllowed(w, r, fallback, http.MethodGet+", "+http.MethodPost)
		}
	}
}

func handleSiteList(w http.ResponseWriter, r *http.Request, auth *authService) {
	profile, err := auth.currentProfile(r.Context(), r)
	if err != nil {
		if isUnauthorizedAuthError(err) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error":  "auth_unavailable",
			"detail": err.Error(),
		})
		return
	}
	store, ok := auth.store.(siteListStore)
	if !ok {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "site_list_unavailable"})
		return
	}

	timeout := auth.timeout
	if timeout <= 0 {
		timeout = defaultAuthTimeout
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	sites, err := store.listSites(ctx, profile)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	payload := siteInstallMetadata(r)
	payload["sites"] = sites
	writeJSON(w, http.StatusOK, payload)
}

func handleSiteCreate(w http.ResponseWriter, r *http.Request, auth *authService) {
	if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
		failure.write(w)
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, http.ErrBodyReadAfterClose) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
		return
	}
	name := cleanText(body["name"])
	description := cleanText(body["description"])
	store, ok := auth.store.(siteMutationStore)
	if !ok {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "site_mutation_unavailable"})
		return
	}
	ctx, cancel := requestTimeout(r.Context(), auth)
	defer cancel()
	payload, status, err := store.createSite(ctx, name, description)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, status, payload)
}

func (s *postgresOperatorStore) listSites(ctx context.Context, profile operatorProfile) ([]map[string]any, error) {
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

	sqlText := `
		SELECT s.id,
			   s.name,
			   s.description,
			   s.created_at,
			   COALESCE(ds.cnt, 0) AS device_count,
			   s.enrollment_code,
			   s.auto_approve_until
		  FROM engine.sites AS s
	 LEFT JOIN (
			   SELECT site_id, COUNT(*) AS cnt
				 FROM engine.device_sites
			 GROUP BY site_id
		   ) AS ds ON ds.site_id = s.id
	`
	params := make([]any, 0, len(allowedSiteIDs))
	if allowedSiteIDs != nil {
		placeholders := make([]string, 0, len(allowedSiteIDs))
		for _, siteID := range allowedSiteIDs {
			params = append(params, siteID)
			placeholders = append(placeholders, "$"+strconv.Itoa(len(params)))
		}
		sqlText += " WHERE s.id IN (" + strings.Join(placeholders, ",") + ")"
	}
	sqlText += " ORDER BY LOWER(s.name) ASC"

	rows, err := conn.QueryContext(ctx, sqlText, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rawRows := make([]siteRow, 0)
	for rows.Next() {
		var row siteRow
		if err := rows.Scan(
			&row.ID,
			&row.Name,
			&row.Description,
			&row.CreatedAt,
			&row.DeviceCount,
			&row.EnrollmentCode,
			&row.AutoApproveUntil,
		); err != nil {
			return nil, err
		}
		rawRows = append(rawRows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	now := time.Now().Unix()
	sites := make([]map[string]any, 0, len(rawRows))
	for _, row := range rawRows {
		autoApproveUntil := nullInt(row.AutoApproveUntil)
		sites = append(sites, map[string]any{
			"id":                   nullInt(row.ID),
			"name":                 nullString(row.Name),
			"description":          nullString(row.Description),
			"created_at":           nullInt(row.CreatedAt),
			"device_count":         nullInt(row.DeviceCount),
			"enrollment_code":      nullString(row.EnrollmentCode),
			"auto_approve_until":   autoApproveUntil,
			"auto_approval_active": autoApproveUntil > now,
		})
	}
	return sites, nil
}

func siteDeleteHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
			return
		}
		rawIDs, ok := body["ids"].([]any)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "ids must be a list"})
			return
		}
		ids := make([]int64, 0, len(rawIDs))
		for _, rawID := range rawIDs {
			siteID, ok := parseInt64Value(rawID)
			if !ok {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
				return
			}
			ids = append(ids, siteID)
		}
		store, ok := auth.store.(siteMutationStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "site_mutation_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		payload, status, err := store.deleteSites(ctx, ids)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, status, payload)
	}
}

func siteAssignHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
			return
		}
		siteID, ok := parseInt64Value(body["site_id"])
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid site_id"})
			return
		}
		rawHostnames, ok := body["hostnames"].([]any)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "hostnames must be a list of strings"})
			return
		}
		hostnames := make([]string, 0, len(rawHostnames))
		for _, rawHostname := range rawHostnames {
			hostname := cleanText(rawHostname)
			if hostname == "" {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "hostnames must be a list of strings"})
				return
			}
			hostnames = append(hostnames, hostname)
		}
		store, ok := auth.store.(siteMutationStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "site_mutation_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		payload, status, err := store.assignDevicesToSite(ctx, siteID, hostnames)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, status, payload)
	}
}

func siteRenameHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
			return
		}
		siteID, ok := parseInt64Value(body["id"])
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
			return
		}
		newName := cleanText(body["new_name"])
		store, ok := auth.store.(siteMutationStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "site_mutation_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		payload, status, err := store.renameSite(ctx, siteID, newName)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, status, payload)
	}
}

func siteAutoApprovalHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		siteID, ok := parseInt64Value(r.PathValue("site_id"))
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
			return
		}
		var until *int64
		if raw, exists := body["auto_approve_until"]; exists && raw != nil && cleanText(raw) != "" && cleanText(raw) != "0" {
			parsed, ok := parseInt64Value(raw)
			if !ok {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid auto_approve_until"})
				return
			}
			if parsed <= time.Now().Unix() {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "auto_approve_until must be in the future"})
				return
			}
			until = &parsed
		}
		store, ok := auth.store.(siteMutationStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "site_mutation_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		payload, status, err := store.updateSiteAutoApproval(ctx, siteID, until)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, status, payload)
	}
}

func (s *postgresOperatorStore) createSite(ctx context.Context, name string, description string) (map[string]any, int, error) {
	if name == "" {
		return map[string]any{"error": "name is required"}, http.StatusBadRequest, nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, 0, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer rollbackQuietly(tx)

	now := time.Now().Unix()
	var siteID int64
	var codeValue string
	for attempt := 0; attempt < 12; attempt++ {
		codeValue = generateEnrollmentCode()
		err = tx.QueryRowContext(
			ctx,
			`INSERT INTO engine.sites(name, description, created_at, enrollment_code)
			 VALUES ($1, $2, $3, $4)
			 RETURNING id`,
			name,
			description,
			now,
			codeValue,
		).Scan(&siteID)
		if err == nil {
			break
		}
		if isPostgresUniqueViolation(err) {
			if strings.Contains(strings.ToLower(err.Error()), "sites_name") || strings.Contains(strings.ToLower(err.Error()), "sites.name") || strings.Contains(strings.ToLower(err.Error()), "name") {
				return map[string]any{"error": "name already exists"}, http.StatusConflict, nil
			}
			continue
		}
		return nil, 0, err
	}
	if siteID <= 0 {
		return map[string]any{"error": "unable_to_generate_enrollment_code"}, http.StatusInternalServerError, nil
	}
	row, found, err := fetchSiteRowTx(ctx, tx, siteID)
	if err != nil {
		return nil, 0, err
	}
	if !found {
		return map[string]any{"error": "creation_failed"}, http.StatusInternalServerError, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return siteRowPayload(row), http.StatusCreated, nil
}

func (s *postgresOperatorStore) deleteSites(ctx context.Context, ids []int64) (map[string]any, int, error) {
	if len(ids) == 0 {
		return map[string]any{"status": "ok", "deleted": int64(0)}, http.StatusOK, nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, 0, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer rollbackQuietly(tx)
	placeholders := postgresPlaceholders(1, len(ids))
	params := intsToAny(ids)
	if _, err := tx.ExecContext(ctx, "DELETE FROM engine.user_site_assignments WHERE site_id IN ("+placeholders+")", params...); err != nil {
		return nil, 0, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM engine.device_sites WHERE site_id IN ("+placeholders+")", params...); err != nil {
		return nil, 0, err
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM engine.sites WHERE id IN ("+placeholders+")", params...)
	if err != nil {
		return nil, 0, err
	}
	deleted, _ := result.RowsAffected()
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return map[string]any{"status": "ok", "deleted": deleted}, http.StatusOK, nil
}

func (s *postgresOperatorStore) assignDevicesToSite(ctx context.Context, siteID int64, hostnames []string) (map[string]any, int, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, 0, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer rollbackQuietly(tx)
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT 1 FROM engine.sites WHERE id = $1", siteID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return map[string]any{"error": "site not found"}, http.StatusNotFound, nil
	} else if err != nil {
		return nil, 0, err
	}
	now := time.Now().Unix()
	for _, hostname := range hostnames {
		hostname = cleanText(hostname)
		if hostname == "" {
			continue
		}
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO engine.device_sites(device_hostname, site_id, assigned_at)
			 VALUES ($1, $2, $3)
			 ON CONFLICT(device_hostname)
			 DO UPDATE SET site_id=excluded.site_id, assigned_at=excluded.assigned_at`,
			hostname,
			siteID,
			now,
		); err != nil {
			return nil, 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return map[string]any{"status": "ok"}, http.StatusOK, nil
}

func (s *postgresOperatorStore) renameSite(ctx context.Context, siteID int64, newName string) (map[string]any, int, error) {
	if newName == "" {
		return map[string]any{"error": "new_name is required"}, http.StatusBadRequest, nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, 0, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer rollbackQuietly(tx)
	result, err := tx.ExecContext(ctx, "UPDATE engine.sites SET name = $1 WHERE id = $2", newName, siteID)
	if err != nil {
		if isPostgresUniqueViolation(err) {
			return map[string]any{"error": "name already exists"}, http.StatusConflict, nil
		}
		return nil, 0, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return map[string]any{"error": "site not found"}, http.StatusNotFound, nil
	}
	row, found, err := fetchSiteRowTx(ctx, tx, siteID)
	if err != nil {
		return nil, 0, err
	}
	if !found {
		return map[string]any{"error": "site not found"}, http.StatusNotFound, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return siteRowPayload(row), http.StatusOK, nil
}

func (s *postgresOperatorStore) updateSiteAutoApproval(ctx context.Context, siteID int64, autoApproveUntil *int64) (map[string]any, int, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, 0, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer rollbackQuietly(tx)
	var value any
	if autoApproveUntil != nil {
		value = *autoApproveUntil
	}
	result, err := tx.ExecContext(ctx, "UPDATE engine.sites SET auto_approve_until = $1 WHERE id = $2", value, siteID)
	if err != nil {
		return nil, 0, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return map[string]any{"error": "site not found"}, http.StatusNotFound, nil
	}
	row, found, err := fetchSiteRowTx(ctx, tx, siteID)
	if err != nil {
		return nil, 0, err
	}
	if !found {
		return map[string]any{"error": "site not found"}, http.StatusNotFound, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return siteRowPayload(row), http.StatusOK, nil
}

func fetchSiteRowTx(ctx context.Context, tx *sql.Tx, siteID int64) (siteRow, bool, error) {
	var row siteRow
	err := tx.QueryRowContext(
		ctx,
		`
		SELECT s.id,
			   s.name,
			   s.description,
			   s.created_at,
			   COALESCE(ds.cnt, 0) AS device_count,
			   s.enrollment_code,
			   s.auto_approve_until
		  FROM engine.sites AS s
	 LEFT JOIN (
			   SELECT site_id, COUNT(*) AS cnt
				 FROM engine.device_sites
			 GROUP BY site_id
		   ) AS ds ON ds.site_id = s.id
		 WHERE s.id = $1
		`,
		siteID,
	).Scan(
		&row.ID,
		&row.Name,
		&row.Description,
		&row.CreatedAt,
		&row.DeviceCount,
		&row.EnrollmentCode,
		&row.AutoApproveUntil,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return siteRow{}, false, nil
	}
	if err != nil {
		return siteRow{}, false, err
	}
	return row, true, nil
}

func siteRowPayload(row siteRow) map[string]any {
	autoApproveUntil := nullInt(row.AutoApproveUntil)
	return map[string]any{
		"id":                   nullInt(row.ID),
		"name":                 nullString(row.Name),
		"description":          nullString(row.Description),
		"created_at":           nullInt(row.CreatedAt),
		"device_count":         nullInt(row.DeviceCount),
		"enrollment_code":      nullString(row.EnrollmentCode),
		"auto_approve_until":   autoApproveUntil,
		"auto_approval_active": autoApproveUntil > time.Now().Unix(),
	}
}

func siteDeviceMapHandler(auth *authService, fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			proxyFallbackOrMethodNotAllowed(w, r, fallback, http.MethodGet)
			return
		}
		profile, err := auth.currentProfile(r.Context(), r)
		if err != nil {
			if isUnauthorizedAuthError(err) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error":  "auth_unavailable",
				"detail": err.Error(),
			})
			return
		}
		store, ok := auth.store.(siteListStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "site_device_map_unavailable"})
			return
		}

		timeout := auth.timeout
		if timeout <= 0 {
			timeout = defaultAuthTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		mapping, err := store.siteDeviceMap(ctx, profile, parseHostnameCSV(r.URL.Query().Get("hostnames")))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"mapping": mapping})
	}
}

func (s *postgresOperatorStore) siteDeviceMap(ctx context.Context, profile operatorProfile, hostnames []string) (map[string]map[string]any, error) {
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, err
	}
	if allowedSiteIDs != nil && len(allowedSiteIDs) == 0 {
		return map[string]map[string]any{}, nil
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	sqlText := `
		SELECT ds.device_hostname, s.id, s.name
		  FROM engine.device_sites AS ds
		  JOIN engine.sites AS s ON s.id = ds.site_id
		 WHERE 1 = 1
	`
	params := make([]any, 0, len(hostnames)+len(allowedSiteIDs))
	if len(hostnames) > 0 {
		placeholders := make([]string, 0, len(hostnames))
		for _, hostname := range hostnames {
			params = append(params, hostname)
			placeholders = append(placeholders, "$"+strconv.Itoa(len(params)))
		}
		sqlText += " AND ds.device_hostname IN (" + strings.Join(placeholders, ",") + ")"
	}
	if allowedSiteIDs != nil {
		placeholders := make([]string, 0, len(allowedSiteIDs))
		for _, siteID := range allowedSiteIDs {
			params = append(params, siteID)
			placeholders = append(placeholders, "$"+strconv.Itoa(len(params)))
		}
		sqlText += " AND ds.site_id IN (" + strings.Join(placeholders, ",") + ")"
	}

	rows, err := conn.QueryContext(ctx, sqlText, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type rowData struct {
		hostname string
		siteID   int64
		siteName string
	}
	rawRows := make([]rowData, 0)
	for rows.Next() {
		var hostname, siteName sql.NullString
		var siteID sql.NullInt64
		if err := rows.Scan(&hostname, &siteID, &siteName); err != nil {
			return nil, err
		}
		if !hostname.Valid || !siteID.Valid {
			continue
		}
		rawRows = append(rawRows, rowData{
			hostname: hostname.String,
			siteID:   siteID.Int64,
			siteName: nullString(siteName),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	mapping := make(map[string]map[string]any, len(rawRows))
	for _, row := range rawRows {
		mapping[row.hostname] = map[string]any{
			"site_id":   row.siteID,
			"site_name": row.siteName,
		}
	}
	return mapping, nil
}

func siteInstallMetadata(r *http.Request) map[string]any {
	baseURL := strings.TrimRight(firstText(
		os.Getenv("BOREALIS_PUBLIC_BASE_URL"),
		os.Getenv("BOREALIS_AGENT_PUBLIC_BASE_URL"),
		os.Getenv("BOREALIS_SERVER_URL"),
	), "/")
	hostname := firstText(
		os.Getenv("BOREALIS_PUBLIC_HOSTNAME"),
		os.Getenv("BOREALIS_PUBLIC_WIREGUARD_HOST"),
	)
	if hostname == "" && baseURL != "" {
		if parsed, err := url.Parse(baseURL); err == nil {
			hostname = parsed.Hostname()
		}
	}
	if hostname == "" && r != nil {
		host := strings.TrimSpace(r.Host)
		if strings.Contains(host, ":") {
			if parsed, err := url.Parse("//" + host); err == nil {
				host = parsed.Hostname()
			}
		}
		hostname = host
	}
	if baseURL == "" && hostname != "" {
		scheme := "https"
		if r != nil && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "http") {
			scheme = "http"
		}
		port := strings.TrimSpace(os.Getenv("BOREALIS_PUBLIC_HTTPS_PORT"))
		netloc := hostname
		if port != "" && port != "0" && port != "443" {
			netloc = hostname + ":" + port
		}
		baseURL = scheme + "://" + netloc
	}
	return map[string]any{
		"public_base_url": strings.TrimRight(baseURL, "/"),
		"public_hostname": hostname,
	}
}

func parseHostnameCSV(value string) []string {
	seen := map[string]struct{}{}
	hostnames := make([]string, 0)
	for _, part := range strings.Split(value, ",") {
		hostname := strings.TrimSpace(part)
		if hostname == "" {
			continue
		}
		if _, ok := seen[hostname]; ok {
			continue
		}
		seen[hostname] = struct{}{}
		hostnames = append(hostnames, hostname)
	}
	return hostnames
}

func requestTimeout(ctx context.Context, auth *authService) (context.Context, context.CancelFunc) {
	timeout := defaultAuthTimeout
	if auth != nil && auth.timeout > 0 {
		timeout = auth.timeout
	}
	return context.WithTimeout(ctx, timeout)
}

func parseInt64Value(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		if typed == float64(int64(typed)) {
			return int64(typed), true
		}
		return 0, false
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func generateEnrollmentCode() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return strings.ToUpper(hex.EncodeToString([]byte(strconv.FormatInt(time.Now().UnixNano(), 16))))[:32]
	}
	hexText := strings.ToUpper(hex.EncodeToString(raw))
	parts := make([]string, 0, 8)
	for index := 0; index < len(hexText); index += 4 {
		parts = append(parts, hexText[index:index+4])
	}
	return strings.Join(parts, "-")
}
