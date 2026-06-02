package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

type deviceSearchMatch struct {
	AgentGUID       string `json:"agent_guid"`
	AgentID         string `json:"agent_id"`
	Hostname        string `json:"hostname"`
	ConnectionType  string `json:"connection_type"`
	SiteID          any    `json:"site_id"`
	SiteName        string `json:"site_name"`
	SiteDescription string `json:"site_description"`
}

type deviceSearchStore interface {
	searchDevicesByHostname(ctx context.Context, profile operatorProfile, query string) ([]deviceSearchMatch, error)
}

func registerDeviceSearchRoutes(mux *http.ServeMux, auth *authService) {
	mux.HandleFunc("/api/devices/search", deviceSearchHandler(auth))
}

func deviceSearchHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		query := cleanText(r.URL.Query().Get("hostname"))
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
		if len(query) < 3 {
			writeJSON(w, http.StatusOK, map[string]any{
				"devices": []deviceSearchMatch{},
				"query":   query,
				"count":   0,
			})
			return
		}

		store, ok := auth.store.(deviceSearchStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "device_search_unavailable"})
			return
		}
		timeout := auth.timeout
		if timeout <= 0 {
			timeout = defaultAuthTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		matches, err := store.searchDevicesByHostname(ctx, profile, query)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"devices": matches,
			"query":   query,
			"count":   len(matches),
		})
	}
}

func (s *postgresOperatorStore) searchDevicesByHostname(ctx context.Context, profile operatorProfile, query string) ([]deviceSearchMatch, error) {
	query = cleanText(query)
	if len(query) < 3 {
		return []deviceSearchMatch{}, nil
	}

	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, err
	}
	if allowedSiteIDs != nil && len(allowedSiteIDs) == 0 {
		return []deviceSearchMatch{}, nil
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	sqlText := `
		SELECT d.guid, d.hostname, d.agent_id, d.connection_type, s.id, s.name, s.description
		  FROM engine.devices AS d
	 LEFT JOIN engine.device_sites AS ds ON ds.device_hostname = d.hostname
	 LEFT JOIN engine.sites AS s ON s.id = ds.site_id
		 WHERE LOWER(d.hostname) LIKE $1 ESCAPE '\'
	`
	params := []any{"%" + escapeLike(strings.ToLower(query)) + "%"}
	if allowedSiteIDs != nil {
		placeholders := make([]string, 0, len(allowedSiteIDs))
		for idx, siteID := range allowedSiteIDs {
			placeholders = append(placeholders, "$"+strconv.Itoa(idx+2))
			params = append(params, siteID)
		}
		sqlText += " AND ds.site_id IN (" + strings.Join(placeholders, ",") + ")"
	}

	rows, err := conn.QueryContext(ctx, sqlText, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	matches := make([]deviceSearchMatch, 0)
	seen := map[string]struct{}{}
	for rows.Next() {
		var rawGUID, hostname, agentID, connectionType, siteName, siteDescription sql.NullString
		var siteID sql.NullInt64
		if err := rows.Scan(&rawGUID, &hostname, &agentID, &connectionType, &siteID, &siteName, &siteDescription); err != nil {
			return nil, err
		}
		hostnameValue := cleanText(hostname.String)
		if hostnameValue == "" {
			continue
		}
		siteValue := any(nil)
		if siteID.Valid {
			siteValue = siteID.Int64
		}
		match := deviceSearchMatch{
			AgentGUID:       normalizeGUID(rawGUID.String),
			AgentID:         cleanText(agentID.String),
			Hostname:        hostnameValue,
			ConnectionType:  cleanText(connectionType.String),
			SiteID:          siteValue,
			SiteName:        cleanText(siteName.String),
			SiteDescription: cleanText(siteDescription.String),
		}
		key := strings.Join([]string{
			strings.ToLower(match.AgentGUID),
			strings.ToLower(match.Hostname),
			strconv.FormatInt(siteID.Int64, 10),
			strings.ToLower(match.AgentID),
		}, "\x00")
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		matches = append(matches, match)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortDeviceSearchMatches(matches, query)
	return matches, nil
}

func (s *postgresOperatorStore) siteIDsForProfile(ctx context.Context, profile operatorProfile) ([]int64, error) {
	if strings.EqualFold(strings.TrimSpace(profile.Role), "admin") {
		return nil, nil
	}
	if profile.ID <= 0 {
		return []int64{}, nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		ctx,
		`
		SELECT site_id
		  FROM engine.user_site_assignments
		 WHERE user_id = $1
		 ORDER BY site_id
		`,
		profile.ID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	siteIDs := make([]int64, 0)
	for rows.Next() {
		var siteID sql.NullInt64
		if err := rows.Scan(&siteID); err != nil {
			return nil, err
		}
		if siteID.Valid {
			siteIDs = append(siteIDs, siteID.Int64)
		}
	}
	return siteIDs, rows.Err()
}

func sortDeviceSearchMatches(matches []deviceSearchMatch, query string) {
	queryLC := strings.ToLower(query)
	sort.SliceStable(matches, func(left, right int) bool {
		a := matches[left]
		b := matches[right]
		aHost := strings.ToLower(a.Hostname)
		bHost := strings.ToLower(b.Hostname)
		if aHost == queryLC && bHost != queryLC {
			return true
		}
		if aHost != queryLC && bHost == queryLC {
			return false
		}
		aPrefix := strings.HasPrefix(aHost, queryLC)
		bPrefix := strings.HasPrefix(bHost, queryLC)
		if aPrefix != bPrefix {
			return aPrefix
		}
		if aHost != bHost {
			return aHost < bHost
		}
		return strings.ToLower(a.SiteName) < strings.ToLower(b.SiteName)
	})
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	value = strings.ReplaceAll(value, `_`, `\_`)
	return value
}

func cleanText(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func normalizeGUID(value any) string {
	candidate := strings.ToUpper(strings.Trim(strings.TrimSpace(fmt.Sprint(value)), "{}"))
	if candidate == "" || candidate == "<NIL>" {
		return ""
	}
	cleaned := make([]rune, 0, len(candidate))
	hexOnly := make([]rune, 0, 32)
	for _, ch := range candidate {
		if ch == '-' {
			cleaned = append(cleaned, ch)
			continue
		}
		if isHexRune(ch) {
			cleaned = append(cleaned, ch)
			hexOnly = append(hexOnly, ch)
		}
	}
	if len(hexOnly) == 32 {
		hex := string(hexOnly)
		return strings.Join([]string{
			hex[0:8],
			hex[8:12],
			hex[12:16],
			hex[16:20],
			hex[20:32],
		}, "-")
	}
	if len(cleaned) > 0 {
		return string(cleaned)
	}
	return candidate
}

func isHexRune(ch rune) bool {
	return (ch >= '0' && ch <= '9') || (ch >= 'A' && ch <= 'F')
}
