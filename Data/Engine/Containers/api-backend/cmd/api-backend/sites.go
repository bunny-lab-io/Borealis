package main

import (
	"context"
	"database/sql"
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

type siteRow struct {
	ID               sql.NullInt64
	Name             sql.NullString
	Description      sql.NullString
	CreatedAt        sql.NullInt64
	DeviceCount      sql.NullInt64
	EnrollmentCode   sql.NullString
	AutoApproveUntil sql.NullInt64
}

func registerSiteRoutes(mux *http.ServeMux, auth *authService) {
	mux.HandleFunc("GET /api/sites", siteListHandler(auth))
	mux.HandleFunc("GET /api/sites/device_map", siteDeviceMapHandler(auth))
}

func siteListHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
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

func siteDeviceMapHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
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
