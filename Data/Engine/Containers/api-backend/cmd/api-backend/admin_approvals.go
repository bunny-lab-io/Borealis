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

type adminApprovalStore interface {
	listEnrollmentCodes(ctx context.Context) ([]map[string]any, error)
	listDeviceApprovals(ctx context.Context, profile operatorProfile, statusFilter string) ([]map[string]any, error)
	setDeviceApprovalStatus(ctx context.Context, profile operatorProfile, approvalID string, status string, guid string, resolution string) (map[string]any, int, error)
}

type approvalRow struct {
	ID                 sql.NullString
	ApprovalReference  sql.NullString
	GUID               sql.NullString
	HostnameClaimed    sql.NullString
	FingerprintClaimed sql.NullString
	EnrollmentCode     sql.NullString
	SiteID             sql.NullInt64
	Status             sql.NullString
	ClientNonce        sql.NullString
	ServerNonce        sql.NullString
	CreatedAt          sql.NullString
	UpdatedAt          sql.NullString
	ApprovedByUserID   sql.NullString
	ApprovedByUsername sql.NullString
	SiteName           sql.NullString
	OnboardingJobID    sql.NullInt64
	OnboardingRunID    sql.NullInt64
	OnboardingTarget   sql.NullString
}

type hostnameConflict struct {
	GUID        string
	Fingerprint string
	SiteID      any
	SiteName    string
}

func registerAdminDeviceRoutes(mux *http.ServeMux, auth *authService) {
	mux.HandleFunc("/api/admin/enrollment-codes", adminEnrollmentCodesHandler(auth))
	mux.HandleFunc("DELETE /api/admin/enrollment-codes/{code_id}", adminEnrollmentCodeDeleteHandler(auth))
	mux.HandleFunc("/api/admin/device-approvals", adminDeviceApprovalsHandler(auth))
	mux.HandleFunc("POST /api/admin/device-approvals/{approval_id}/approve", adminDeviceApprovalApproveHandler(auth))
	mux.HandleFunc("POST /api/admin/device-approvals/{approval_id}/deny", adminDeviceApprovalDenyHandler(auth))
}

func adminEnrollmentCodesHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
				failure.write(w)
				return
			}
			store, ok := auth.store.(adminApprovalStore)
			if !ok {
				writeJSON(w, http.StatusBadGateway, map[string]any{"error": "admin_device_unavailable"})
				return
			}
			ctx, cancel := requestTimeout(r.Context(), auth)
			defer cancel()
			codes, err := store.listEnrollmentCodes(ctx)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"codes": codes})
		case http.MethodPost:
			if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
				failure.write(w)
				return
			}
			writeJSON(w, http.StatusGone, map[string]any{"error": "legacy_endpoint_removed_use_sites_api"})
		default:
			writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
		}
	}
}

func adminEnrollmentCodeDeleteHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		writeJSON(w, http.StatusGone, map[string]any{"error": "legacy_endpoint_removed_use_sites_api"})
	}
}

func adminDeviceApprovalsHandler(auth *authService) http.HandlerFunc {
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
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
			return
		}
		store, ok := auth.store.(adminApprovalStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "admin_device_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		approvals, err := store.listDeviceApprovals(ctx, profile, r.URL.Query().Get("status"))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"approvals": approvals})
	}
}

func adminDeviceApprovalApproveHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		handleApprovalStatusMutation(w, r, auth, "approved")
	}
}

func adminDeviceApprovalDenyHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		handleApprovalStatusMutation(w, r, auth, "denied")
	}
}

func handleApprovalStatusMutation(w http.ResponseWriter, r *http.Request, auth *authService, status string) {
	profile, err := auth.currentProfile(r.Context(), r)
	if err != nil {
		if isUnauthorizedAuthError(err) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
		return
	}
	var body map[string]any
	if status == "approved" {
		var err error
		body, err = readJSONMap(r)
		if err != nil {
			invalidJSONOrValidation(w, err)
			return
		}
	}
	guid := cleanText(body["guid"])
	resolution := cleanText(body["conflict_resolution"])
	store, ok := auth.store.(adminApprovalStore)
	if !ok {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "admin_device_unavailable"})
		return
	}
	ctx, cancel := requestTimeout(r.Context(), auth)
	defer cancel()
	payload, responseStatus, err := store.setDeviceApprovalStatus(ctx, profile, r.PathValue("approval_id"), status, guid, resolution)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, responseStatus, payload)
}

func (s *postgresOperatorStore) listEnrollmentCodes(ctx context.Context) ([]map[string]any, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(
		ctx,
		`
		SELECT id, name, enrollment_code, created_at
		  FROM engine.sites
		 WHERE COALESCE(TRIM(enrollment_code), '') != ''
		 ORDER BY LOWER(name) ASC
		`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	codes := make([]map[string]any, 0)
	for rows.Next() {
		var id, createdAt sql.NullInt64
		var name, code sql.NullString
		if err := rows.Scan(&id, &name, &code, &createdAt); err != nil {
			return nil, err
		}
		codes = append(codes, map[string]any{
			"id":         "site:" + strconv.FormatInt(nullInt(id), 10),
			"site_id":    nullInt(id),
			"site_name":  nullString(name),
			"code":       nullString(code),
			"created_at": nullInt(createdAt),
		})
	}
	return codes, rows.Err()
}

func (s *postgresOperatorStore) listDeviceApprovals(ctx context.Context, profile operatorProfile, statusFilter string) ([]map[string]any, error) {
	statusNorm := strings.ToLower(strings.TrimSpace(statusFilter))
	if statusNorm == "wrong_code" {
		if !strings.EqualFold(profile.Role, "admin") {
			return []map[string]any{}, nil
		}
		return s.listWrongCodeAttempts(ctx, 300)
	}
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
		SELECT
			da.id,
			da.approval_reference,
			da.guid,
			da.hostname_claimed,
			da.ssl_key_fingerprint_claimed,
			da.enrollment_code,
			da.site_id,
			da.status,
			da.client_nonce,
			da.server_nonce,
			da.created_at,
			da.updated_at,
			da.approved_by_user_id,
			u.username AS approved_by_username,
			s.name AS site_name,
			da.onboarding_job_id,
			da.onboarding_run_id,
			da.onboarding_target
		  FROM engine.device_approvals AS da
	 LEFT JOIN engine.users AS u
			ON (
				CAST(da.approved_by_user_id AS TEXT) = CAST(u.id AS TEXT)
				OR LOWER(da.approved_by_user_id) = LOWER(u.username)
			)
	 LEFT JOIN engine.sites AS s ON s.id = da.site_id
	`
	params := make([]any, 0)
	clauses := make([]string, 0)
	if statusNorm != "" && statusNorm != "all" {
		params = append(params, statusNorm)
		clauses = append(clauses, "LOWER(da.status) = $"+strconv.Itoa(len(params)))
	}
	if allowedSiteIDs != nil {
		placeholders := make([]string, 0, len(allowedSiteIDs))
		for _, siteID := range allowedSiteIDs {
			params = append(params, siteID)
			placeholders = append(placeholders, "$"+strconv.Itoa(len(params)))
		}
		clauses = append(clauses, "da.site_id IN ("+strings.Join(placeholders, ",")+")")
	}
	if len(clauses) > 0 {
		sqlText += " WHERE " + strings.Join(clauses, " AND ")
	}
	sqlText += " ORDER BY da.created_at ASC"

	rows, err := conn.QueryContext(ctx, sqlText, params...)
	if err != nil {
		return nil, err
	}
	rawRows := make([]approvalRow, 0)
	for rows.Next() {
		var row approvalRow
		if err := rows.Scan(
			&row.ID,
			&row.ApprovalReference,
			&row.GUID,
			&row.HostnameClaimed,
			&row.FingerprintClaimed,
			&row.EnrollmentCode,
			&row.SiteID,
			&row.Status,
			&row.ClientNonce,
			&row.ServerNonce,
			&row.CreatedAt,
			&row.UpdatedAt,
			&row.ApprovedByUserID,
			&row.ApprovedByUsername,
			&row.SiteName,
			&row.OnboardingJobID,
			&row.OnboardingRunID,
			&row.OnboardingTarget,
		); err != nil {
			_ = rows.Close()
			return nil, err
		}
		rawRows = append(rawRows, row)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	approvals := make([]map[string]any, 0, len(rawRows))
	for _, row := range rawRows {
		hostname := nullString(row.HostnameClaimed)
		guid := nullString(row.GUID)
		claimedFP := strings.ToLower(nullString(row.FingerprintClaimed))
		conflictRaw, err := lookupHostnameConflict(ctx, conn, hostname, guid)
		if err != nil {
			return nil, err
		}
		var conflict any
		fingerprintMatch := false
		requiresPrompt := false
		var alternate any
		if conflictRaw != nil {
			fingerprintMatch = conflictRaw.Fingerprint != "" && claimedFP != "" && strings.EqualFold(conflictRaw.Fingerprint, claimedFP)
			requiresPrompt = !fingerprintMatch
			conflict = map[string]any{
				"guid":                firstAny(conflictRaw.GUID, nil),
				"ssl_key_fingerprint": firstAny(conflictRaw.Fingerprint, nil),
				"site_id":             conflictRaw.SiteID,
				"site_name":           conflictRaw.SiteName,
				"fingerprint_match":   fingerprintMatch,
				"requires_prompt":     requiresPrompt,
			}
			if requiresPrompt {
				alternate, err = suggestAlternateHostname(ctx, conn, hostname, guid)
				if err != nil {
					return nil, err
				}
			}
		}
		approvals = append(approvals, map[string]any{
			"id":                          nullString(row.ID),
			"approval_reference":          nullString(row.ApprovalReference),
			"guid":                        guid,
			"hostname_claimed":            hostname,
			"ssl_key_fingerprint_claimed": nullString(row.FingerprintClaimed),
			"enrollment_code":             nullString(row.EnrollmentCode),
			"site_id":                     nullableInt(row.SiteID),
			"status":                      nullString(row.Status),
			"client_nonce":                nullString(row.ClientNonce),
			"server_nonce":                nullString(row.ServerNonce),
			"created_at":                  nullString(row.CreatedAt),
			"updated_at":                  nullString(row.UpdatedAt),
			"approved_by_user_id":         nullString(row.ApprovedByUserID),
			"hostname_conflict":           conflict,
			"alternate_hostname":          alternate,
			"conflict_requires_prompt":    requiresPrompt,
			"fingerprint_match":           fingerprintMatch,
			"approved_by_username":        nullString(row.ApprovedByUsername),
			"site_name":                   nullString(row.SiteName),
			"onboarding_job_id":           nullableInt(row.OnboardingJobID),
			"onboarding_run_id":           nullableInt(row.OnboardingRunID),
			"onboarding_target":           nullString(row.OnboardingTarget),
		})
	}
	return approvals, nil
}

func (s *postgresOperatorStore) listWrongCodeAttempts(ctx context.Context, windowSeconds int) ([]map[string]any, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	if windowSeconds < 60 {
		windowSeconds = 60
	}
	cutoff := time.Now().UTC().Add(-time.Duration(windowSeconds) * time.Second).Format(time.RFC3339)
	rows, err := conn.QueryContext(
		ctx,
		`
		SELECT id,
			   hostname_claimed,
			   ssl_key_fingerprint_claimed,
			   enrollment_code_mask,
			   remote_addr,
			   first_seen_at,
			   last_seen_at,
			   attempt_count,
			   last_error
		  FROM engine.enrollment_code_failures
		 WHERE last_seen_at >= $1
		 ORDER BY last_seen_at DESC
		`,
		cutoff,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	approvals := make([]map[string]any, 0)
	for rows.Next() {
		var id, hostname, fingerprint, codeMask, remoteAddr, firstSeen, lastSeen, lastError sql.NullString
		var attemptCount sql.NullInt64
		if err := rows.Scan(&id, &hostname, &fingerprint, &codeMask, &remoteAddr, &firstSeen, &lastSeen, &attemptCount, &lastError); err != nil {
			return nil, err
		}
		approvals = append(approvals, map[string]any{
			"id":                          nullString(id),
			"approval_reference":          nil,
			"guid":                        nil,
			"hostname_claimed":            nullString(hostname),
			"ssl_key_fingerprint_claimed": nullString(fingerprint),
			"enrollment_code":             nullString(codeMask),
			"site_id":                     nil,
			"status":                      "wrong_code",
			"client_nonce":                nil,
			"server_nonce":                nil,
			"created_at":                  nullString(firstSeen),
			"updated_at":                  nullString(lastSeen),
			"approved_by_user_id":         nil,
			"hostname_conflict":           nil,
			"alternate_hostname":          nil,
			"conflict_requires_prompt":    false,
			"fingerprint_match":           false,
			"approved_by_username":        nil,
			"site_name":                   nil,
			"remote_addr":                 nullString(remoteAddr),
			"first_seen_at":               nullString(firstSeen),
			"last_seen_at":                nullString(lastSeen),
			"wrong_code_attempt_count":    nullInt(attemptCount),
			"last_error":                  nullString(lastError),
		})
	}
	return approvals, rows.Err()
}

func (s *postgresOperatorStore) setDeviceApprovalStatus(ctx context.Context, profile operatorProfile, approvalID string, status string, guid string, resolution string) (map[string]any, int, error) {
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, 0, err
	}
	if allowedSiteIDs != nil && len(allowedSiteIDs) == 0 {
		return map[string]any{"error": "not found"}, http.StatusNotFound, nil
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

	sqlText := `
		SELECT status, guid, hostname_claimed, ssl_key_fingerprint_claimed, site_id
		  FROM engine.device_approvals
		 WHERE id = $1
	`
	params := []any{approvalID}
	if allowedSiteIDs != nil {
		placeholders := make([]string, 0, len(allowedSiteIDs))
		for _, siteID := range allowedSiteIDs {
			params = append(params, siteID)
			placeholders = append(placeholders, "$"+strconv.Itoa(len(params)))
		}
		sqlText += " AND site_id IN (" + strings.Join(placeholders, ",") + ")"
	}
	var existingStatus, storedGUID, hostname, fingerprint sql.NullString
	var siteID sql.NullInt64
	err = tx.QueryRowContext(ctx, sqlText, params...).Scan(&existingStatus, &storedGUID, &hostname, &fingerprint, &siteID)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]any{"error": "not found"}, http.StatusNotFound, nil
	}
	if err != nil {
		return nil, 0, err
	}
	if !strings.EqualFold(nullString(existingStatus), "pending") {
		return map[string]any{"error": "approval_not_pending"}, http.StatusConflict, nil
	}
	guidEffective := normalizeCanonicalGUID(guid)
	if guidEffective == "" {
		guidEffective = normalizeCanonicalGUID(nullString(storedGUID))
	}
	resolutionEffective := strings.ToLower(strings.TrimSpace(resolution))
	if status == "approved" {
		conflict, err := lookupHostnameConflictTx(ctx, tx, nullString(hostname), guidEffective)
		if err != nil {
			return nil, 0, err
		}
		if conflict != nil {
			conflictFP := strings.ToLower(strings.TrimSpace(conflict.Fingerprint))
			claimedFP := strings.ToLower(nullString(fingerprint))
			fingerprintMatch := conflictFP != "" && claimedFP != "" && conflictFP == claimedFP
			if fingerprintMatch {
				if conflict.GUID != "" {
					guidEffective = conflict.GUID
				}
				if resolutionEffective == "" {
					resolutionEffective = "auto_merge_fingerprint"
				}
			} else if resolutionEffective == "overwrite" {
				if conflict.GUID != "" {
					guidEffective = conflict.GUID
				}
			} else if resolutionEffective == "coexist" {
				// New enrollment can coexist with existing hostname after later Agent-side hostname change.
			} else {
				return map[string]any{"error": "conflict_resolution_required", "hostname": nullString(hostname)}, http.StatusConflict, nil
			}
		}
	}
	approvedBy, err := lookupApproverID(ctx, tx, profile.Username)
	if err != nil {
		return nil, 0, err
	}
	if approvedBy == "" {
		approvedBy = firstText(profile.Username, "system")
	}
	if guidEffective == "" {
		guidEffective = normalizeCanonicalGUID(nullString(storedGUID))
	}
	_, err = tx.ExecContext(
		ctx,
		`
		UPDATE engine.device_approvals
		   SET status = $1,
			   guid = $2,
			   approved_by_user_id = $3,
			   updated_at = $4
		 WHERE id = $5
		`,
		status,
		nullableStringValue(guidEffective),
		approvedBy,
		time.Now().UTC().Format(time.RFC3339),
		approvalID,
	)
	if err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	payload := map[string]any{"status": status}
	if resolutionEffective != "" {
		payload["conflict_resolution"] = resolutionEffective
	}
	return payload, http.StatusOK, nil
}

func lookupHostnameConflict(ctx context.Context, conn *sql.Conn, hostname string, pendingGUID string) (*hostnameConflict, error) {
	var guid, fp, siteName sql.NullString
	var siteID sql.NullInt64
	err := conn.QueryRowContext(
		ctx,
		`
		SELECT d.guid, d.ssl_key_fingerprint, ds.site_id, s.name
		  FROM engine.devices d
	 LEFT JOIN engine.device_sites ds ON ds.device_hostname = d.hostname
	 LEFT JOIN engine.sites s ON s.id = ds.site_id
		 WHERE d.hostname = $1
		`,
		hostname,
	).Scan(&guid, &fp, &siteID, &siteName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	existingGUID := normalizeCanonicalGUID(nullString(guid))
	if existingGUID != "" && normalizeCanonicalGUID(pendingGUID) == existingGUID {
		return nil, nil
	}
	return &hostnameConflict{
		GUID:        existingGUID,
		Fingerprint: strings.ToLower(nullString(fp)),
		SiteID:      nullableInt(siteID),
		SiteName:    nullString(siteName),
	}, nil
}

func lookupHostnameConflictTx(ctx context.Context, tx *sql.Tx, hostname string, pendingGUID string) (*hostnameConflict, error) {
	var guid, fp, siteName sql.NullString
	var siteID sql.NullInt64
	err := tx.QueryRowContext(
		ctx,
		`
		SELECT d.guid, d.ssl_key_fingerprint, ds.site_id, s.name
		  FROM engine.devices d
	 LEFT JOIN engine.device_sites ds ON ds.device_hostname = d.hostname
	 LEFT JOIN engine.sites s ON s.id = ds.site_id
		 WHERE d.hostname = $1
		`,
		hostname,
	).Scan(&guid, &fp, &siteID, &siteName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	existingGUID := normalizeCanonicalGUID(nullString(guid))
	if existingGUID != "" && normalizeCanonicalGUID(pendingGUID) == existingGUID {
		return nil, nil
	}
	return &hostnameConflict{
		GUID:        existingGUID,
		Fingerprint: strings.ToLower(nullString(fp)),
		SiteID:      nullableInt(siteID),
		SiteName:    nullString(siteName),
	}, nil
}

func suggestAlternateHostname(ctx context.Context, conn *sql.Conn, hostname string, pendingGUID string) (any, error) {
	base := cleanText(hostname)
	if base == "" {
		return nil, nil
	}
	if len(base) > 253 {
		base = base[:253]
	}
	pending := normalizeCanonicalGUID(pendingGUID)
	candidate := base
	for suffix := 1; suffix <= 50; suffix++ {
		var existing sql.NullString
		err := conn.QueryRowContext(ctx, "SELECT guid FROM engine.devices WHERE hostname = $1", candidate).Scan(&existing)
		if errors.Is(err, sql.ErrNoRows) {
			return candidate, nil
		}
		if err != nil {
			return nil, err
		}
		if pending != "" && normalizeCanonicalGUID(nullString(existing)) == pending {
			return candidate, nil
		}
		candidate = base + "-" + strconv.Itoa(suffix)
	}
	if pending != "" {
		return pending, nil
	}
	return candidate, nil
}

func lookupApproverID(ctx context.Context, tx *sql.Tx, username string) (string, error) {
	if strings.TrimSpace(username) == "" {
		return "", nil
	}
	var id sql.NullString
	err := tx.QueryRowContext(ctx, "SELECT CAST(id AS TEXT) FROM engine.users WHERE LOWER(username) = LOWER($1)", username).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return nullString(id), nil
}

func nullableStringValue(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func firstAny(value any, fallback any) any {
	if cleanText(value) == "" {
		return fallback
	}
	return value
}
