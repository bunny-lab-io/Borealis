package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
)

const (
	adminSelectionMessage  = "An administrator was selected, admins inherantly have access to all managed sites.  Please unselect the admin and try again."
	mixedAssignmentWarning = "The users selected for site assignment are members of different sites.  Changes made here will overwrite existing site assignments for the selected users."
)

type userSiteAssignmentStore interface {
	loadUserSiteAssignmentSelection(ctx context.Context, usernames []string) (map[string]any, int, error)
	assignUserSites(ctx context.Context, usernames []string, siteIDs []int64) (map[string]any, int, error)
}

type userSiteAssignmentUser struct {
	ID          sql.NullInt64
	Username    sql.NullString
	DisplayName sql.NullString
	Role        sql.NullString
}

type userSiteAssignmentSite struct {
	ID             sql.NullInt64
	Name           sql.NullString
	Description    sql.NullString
	CreatedAt      sql.NullInt64
	DeviceCount    sql.NullInt64
	EnrollmentCode sql.NullString
}

func registerUserSiteAssignmentRoutes(mux *http.ServeMux, auth *authService, _ http.Handler) {
	mux.HandleFunc("/api/user_site_assignments/", userSiteAssignmentHandler(auth))
}

func userSiteAssignmentHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		route := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/user_site_assignments/"), "/")
		if route != "selection" && route != "assign" {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
			return
		}
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		store, ok := auth.store.(userSiteAssignmentStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "user_site_assignments_unavailable"})
			return
		}
		var body map[string]any
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			body = map[string]any{}
		}
		usernames := normalizeAssignmentUsernames(body["usernames"])
		siteIDs := normalizeAssignmentSiteIDs(body["site_ids"])
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()

		var payload map[string]any
		var status int
		var err error
		if route == "selection" {
			payload, status, err = store.loadUserSiteAssignmentSelection(ctx, usernames)
		} else {
			payload, status, err = store.assignUserSites(ctx, usernames, siteIDs)
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, status, payload)
	}
}

func normalizeAssignmentUsernames(raw any) []string {
	values, ok := raw.([]any)
	if !ok {
		return []string{}
	}
	usernames := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		username := cleanText(value)
		lowered := strings.ToLower(username)
		if username == "" || seen[lowered] {
			continue
		}
		seen[lowered] = true
		usernames = append(usernames, username)
	}
	return usernames
}

func normalizeAssignmentSiteIDs(raw any) []int64 {
	values, ok := raw.([]any)
	if !ok {
		return []int64{}
	}
	siteIDs := make([]int64, 0, len(values))
	seen := map[int64]bool{}
	for _, value := range values {
		siteID := coerceInt64(value)
		if siteID == 0 || seen[siteID] {
			continue
		}
		seen[siteID] = true
		siteIDs = append(siteIDs, siteID)
	}
	return siteIDs
}

func (s *postgresOperatorStore) loadUserSiteAssignmentSelection(ctx context.Context, usernames []string) (map[string]any, int, error) {
	if len(usernames) == 0 {
		return map[string]any{"error": "No users were selected."}, http.StatusBadRequest, nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, 0, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	users, err := loadAssignmentUsers(ctx, conn, usernames)
	if err != nil {
		return nil, 0, err
	}
	if len(users) != len(usernames) {
		return map[string]any{"error": "One or more selected users no longer exists."}, http.StatusNotFound, nil
	}
	if adminSelected(users) {
		return map[string]any{"error": "admin_selected", "message": adminSelectionMessage}, http.StatusBadRequest, nil
	}
	assignments, err := loadAssignmentsForUsers(ctx, conn, users)
	if err != nil {
		return nil, 0, err
	}
	signatures := map[string][]int64{}
	for _, user := range users {
		username := nullString(user.Username)
		siteIDs := make([]int64, 0)
		for _, site := range assignments[username] {
			siteIDs = append(siteIDs, coerceInt64(site["id"]))
		}
		sort.Slice(siteIDs, func(i, j int) bool { return siteIDs[i] < siteIDs[j] })
		signatures[siteIDSignature(siteIDs)] = siteIDs
	}
	hasMixed := len(signatures) > 1
	selectedSiteIDs := []int64{}
	if !hasMixed {
		for _, ids := range signatures {
			selectedSiteIDs = ids
			break
		}
	}
	sites, err := loadAssignmentSites(ctx, conn)
	if err != nil {
		return nil, 0, err
	}
	warning := ""
	if hasMixed {
		warning = mixedAssignmentWarning
	}
	return map[string]any{
		"users":                 assignmentUsersPayload(users),
		"sites":                 sites,
		"existing_assignments":  assignments,
		"selected_site_ids":     selectedSiteIDs,
		"has_mixed_assignments": hasMixed,
		"warning":               warning,
	}, http.StatusOK, nil
}

func (s *postgresOperatorStore) assignUserSites(ctx context.Context, usernames []string, siteIDs []int64) (map[string]any, int, error) {
	if len(usernames) == 0 {
		return map[string]any{"error": "No users were selected."}, http.StatusBadRequest, nil
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
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	users, err := loadAssignmentUsersTx(ctx, tx, usernames)
	if err != nil {
		return nil, 0, err
	}
	if len(users) != len(usernames) {
		return map[string]any{"error": "One or more selected users no longer exists."}, http.StatusNotFound, nil
	}
	if adminSelected(users) {
		return map[string]any{"error": "admin_selected", "message": adminSelectionMessage}, http.StatusBadRequest, nil
	}
	if len(siteIDs) > 0 {
		missing, err := missingAssignmentSites(ctx, tx, siteIDs)
		if err != nil {
			return nil, 0, err
		}
		if len(missing) > 0 {
			return map[string]any{"error": "One or more selected sites no longer exists: " + int64SliceDisplay(missing)}, http.StatusNotFound, nil
		}
	}
	userIDs := make([]int64, 0, len(users))
	for _, user := range users {
		userIDs = append(userIDs, nullInt(user.ID))
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM engine.user_site_assignments WHERE user_id = ANY($1)", pq.Array(userIDs)); err != nil {
		return nil, 0, err
	}
	now := time.Now().Unix()
	for _, userID := range userIDs {
		for _, siteID := range siteIDs {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO engine.user_site_assignments(user_id, site_id, assigned_at)
				VALUES ($1, $2, $3)
				ON CONFLICT(user_id, site_id) DO UPDATE SET assigned_at = EXCLUDED.assigned_at
			`, userID, siteID, now); err != nil {
				return nil, 0, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	committed = true
	return map[string]any{
		"status":              "ok",
		"assigned_user_count": len(users),
		"assigned_site_ids":   siteIDs,
	}, http.StatusOK, nil
}

func loadAssignmentUsers(ctx context.Context, conn *sql.Conn, usernames []string) ([]userSiteAssignmentUser, error) {
	query, params := assignmentUsersQuery(usernames)
	rows, err := conn.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAssignmentUsers(rows, usernames)
}

func loadAssignmentUsersTx(ctx context.Context, tx *sql.Tx, usernames []string) ([]userSiteAssignmentUser, error) {
	query, params := assignmentUsersQuery(usernames)
	rows, err := tx.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAssignmentUsers(rows, usernames)
}

func assignmentUsersQuery(usernames []string) (string, []any) {
	params := make([]any, 0, len(usernames))
	placeholders := make([]string, 0, len(usernames))
	for _, username := range usernames {
		params = append(params, strings.ToLower(username))
		placeholders = append(placeholders, "$"+strconv.Itoa(len(params)))
	}
	return `
		SELECT id, username, display_name, role
		  FROM engine.users
		 WHERE LOWER(username) IN (` + strings.Join(placeholders, ",") + `)
	`, params
}

func scanAssignmentUsers(rows *sql.Rows, requested []string) ([]userSiteAssignmentUser, error) {
	lookup := map[string]userSiteAssignmentUser{}
	for rows.Next() {
		var user userSiteAssignmentUser
		if err := rows.Scan(&user.ID, &user.Username, &user.DisplayName, &user.Role); err != nil {
			return nil, err
		}
		lookup[strings.ToLower(nullString(user.Username))] = user
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	users := make([]userSiteAssignmentUser, 0, len(requested))
	for _, username := range requested {
		if user, ok := lookup[strings.ToLower(username)]; ok {
			users = append(users, user)
		}
	}
	return users, nil
}

func adminSelected(users []userSiteAssignmentUser) bool {
	for _, user := range users {
		if strings.EqualFold(strings.TrimSpace(nullString(user.Role)), "admin") {
			return true
		}
	}
	return false
}

func loadAssignmentsForUsers(ctx context.Context, conn *sql.Conn, users []userSiteAssignmentUser) (map[string][]map[string]any, error) {
	assignments := map[string][]map[string]any{}
	usernamesByID := map[int64]string{}
	userIDs := make([]int64, 0, len(users))
	for _, user := range users {
		userID := nullInt(user.ID)
		username := nullString(user.Username)
		userIDs = append(userIDs, userID)
		usernamesByID[userID] = username
		assignments[username] = []map[string]any{}
	}
	rows, err := conn.QueryContext(ctx, `
		SELECT usa.user_id, s.id, s.name
		  FROM engine.user_site_assignments AS usa
		  JOIN engine.sites AS s ON s.id = usa.site_id
		 WHERE usa.user_id = ANY($1)
	  ORDER BY LOWER(s.name) ASC
	`, pq.Array(userIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var userID sql.NullInt64
		var siteID sql.NullInt64
		var siteName sql.NullString
		if err := rows.Scan(&userID, &siteID, &siteName); err != nil {
			return nil, err
		}
		username := usernamesByID[nullInt(userID)]
		if username == "" {
			continue
		}
		assignments[username] = append(assignments[username], map[string]any{
			"id":   nullInt(siteID),
			"name": nullString(siteName),
		})
	}
	return assignments, rows.Err()
}

func loadAssignmentSites(ctx context.Context, conn *sql.Conn) ([]map[string]any, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT s.id, s.name, s.description, s.created_at, COALESCE(ds.cnt, 0), s.enrollment_code
		  FROM engine.sites AS s
	 LEFT JOIN (
			SELECT site_id, COUNT(*) AS cnt
			  FROM engine.device_sites
		  GROUP BY site_id
		) AS ds ON ds.site_id = s.id
	  ORDER BY LOWER(s.name) ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sites := make([]map[string]any, 0)
	for rows.Next() {
		var site userSiteAssignmentSite
		if err := rows.Scan(&site.ID, &site.Name, &site.Description, &site.CreatedAt, &site.DeviceCount, &site.EnrollmentCode); err != nil {
			return nil, err
		}
		sites = append(sites, map[string]any{
			"id":              nullInt(site.ID),
			"name":            nullString(site.Name),
			"description":     nullString(site.Description),
			"created_at":      nullInt(site.CreatedAt),
			"device_count":    nullInt(site.DeviceCount),
			"enrollment_code": nullString(site.EnrollmentCode),
		})
	}
	return sites, rows.Err()
}

func missingAssignmentSites(ctx context.Context, tx *sql.Tx, siteIDs []int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id FROM engine.sites WHERE id = ANY($1)", pq.Array(siteIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	existing := map[int64]bool{}
	for rows.Next() {
		var siteID sql.NullInt64
		if err := rows.Scan(&siteID); err != nil {
			return nil, err
		}
		existing[nullInt(siteID)] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	missing := make([]int64, 0)
	for _, siteID := range siteIDs {
		if !existing[siteID] {
			missing = append(missing, siteID)
		}
	}
	return missing, nil
}

func assignmentUsersPayload(users []userSiteAssignmentUser) []map[string]any {
	payload := make([]map[string]any, 0, len(users))
	for _, user := range users {
		username := nullString(user.Username)
		displayName := nullString(user.DisplayName)
		if displayName == "" {
			displayName = username
		}
		role := nullString(user.Role)
		if role == "" {
			role = defaultUserRole
		}
		payload = append(payload, map[string]any{
			"id":           nullInt(user.ID),
			"username":     username,
			"display_name": displayName,
			"role":         role,
		})
	}
	return payload
}

func siteIDSignature(siteIDs []int64) string {
	parts := make([]string, 0, len(siteIDs))
	for _, siteID := range siteIDs {
		parts = append(parts, strconv.FormatInt(siteID, 10))
	}
	return strings.Join(parts, ",")
}

func int64SliceDisplay(values []int64) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.FormatInt(value, 10))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
