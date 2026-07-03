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

const patchInstallProgressRoutePath = "/api/agent/patches/install-progress"

type patchInstallProgressStore interface {
	recordPatchInstallProgress(ctx context.Context, deviceCtx deviceBearerAuthContext, payload map[string]any) (map[string]any, int, error)
}

func registerAgentPatchProgressRoutes(mux *http.ServeMux, auth *authService, signer *agentJWTSigner, dpop *dpopVerifier, realtime *operatorRealtimeHub) {
	mux.HandleFunc("POST "+patchInstallProgressRoutePath, agentPatchInstallProgressHandler(auth, signer, dpop, realtime))
}

func agentPatchInstallProgressHandler(auth *authService, signer *agentJWTSigner, dpop *dpopVerifier, realtime *operatorRealtimeHub) http.HandlerFunc {
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
		store, ok := auth.store.(patchInstallProgressStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "patch_progress_unavailable"})
			return
		}
		body, err := readJSONMap(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
			return
		}
		timeout := auth.timeout
		if timeout <= 0 {
			timeout = defaultAuthTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		progress, status, err := store.recordPatchInstallProgress(ctx, deviceCtx, body)
		if err != nil {
			code := statusOrDefault(status, http.StatusInternalServerError)
			writeJSON(w, code, map[string]any{"error": err.Error()})
			return
		}
		if realtime != nil {
			_ = realtime.emit("scheduled_job_patch_progress", map[string]any{
				"scheduled_job_id":     progress["scheduled_job_id"],
				"scheduled_job_run_id": progress["scheduled_job_run_id"],
				"hostname":             progress["hostname"],
				"phase":                progress["phase"],
				"percent":              progress["percent"],
				"progress":             progress,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "patch_progress": progress})
	}
}

func (s *postgresOperatorStore) recordPatchInstallProgress(ctx context.Context, deviceCtx deviceBearerAuthContext, payload map[string]any) (map[string]any, int, error) {
	jobID := coerceInt64(firstNonNil(firstPatchProgressValue(payload, "scheduled_job_id", "job_id"), payload["jobID"]))
	runID := coerceInt64(firstNonNil(firstPatchProgressValue(payload, "scheduled_job_run_id", "run_id"), payload["runID"]))
	if jobID <= 0 || runID <= 0 {
		return nil, http.StatusBadRequest, errors.New("scheduled_job_context_required")
	}
	progress := normalizePatchInstallProgressPayload(payload)
	if progress == nil {
		return nil, http.StatusBadRequest, errors.New("patch_progress_required")
	}
	progress["scheduled_job_id"] = jobID
	progress["scheduled_job_run_id"] = runID

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	var foundJobID, activityID sql.NullInt64
	var targetGUID, targetHostname, authHostname sql.NullString
	err = conn.QueryRowContext(ctx, `
		SELECT r.job_id,
		       COALESCE(t.device_guid, ''),
		       COALESCE(t.hostname, r.target_hostname, ''),
		       COALESCE(d.hostname, ''),
		       COALESCE(a.activity_id, 0)
		  FROM engine.scheduled_job_runs AS r
	 LEFT JOIN engine.scheduled_job_run_targets AS t ON t.run_id=r.id
	 LEFT JOIN engine.devices AS d ON UPPER(d.guid)=UPPER($3)
	 LEFT JOIN engine.scheduled_job_run_activity AS a ON a.run_id=r.id AND a.component_kind=$4
		 WHERE r.id=$1 AND r.job_id=$2
	  ORDER BY t.id ASC
		 LIMIT 1
	`, runID, jobID, deviceCtx.GUID, patchInstallComponentKind).Scan(&foundJobID, &targetGUID, &targetHostname, &authHostname, &activityID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, http.StatusNotFound, errors.New("scheduled_patch_run_not_found")
	}
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if nullInt(activityID) <= 0 {
		return nil, http.StatusConflict, errors.New("scheduled_patch_activity_not_ready")
	}
	if guid := normalizeCanonicalGUID(nullString(targetGUID)); guid != "" && guid != deviceCtx.GUID {
		return nil, http.StatusForbidden, errors.New("scheduled_patch_run_device_mismatch")
	}
	if normalizeCanonicalGUID(nullString(targetGUID)) == "" {
		targetHost := strings.ToLower(nullString(targetHostname))
		authHost := strings.ToLower(nullString(authHostname))
		if targetHost != "" && authHost != "" && targetHost != authHost {
			return nil, http.StatusForbidden, errors.New("scheduled_patch_run_device_mismatch")
		}
	}
	progress["hostname"] = firstText(cleanText(progress["hostname"]), nullString(targetHostname), nullString(authHostname))

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	defer rollbackQuietly(tx)
	var metadataJSON sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT metadata_json FROM engine.activity_history WHERE id=$1 FOR UPDATE`, nullInt(activityID)).Scan(&metadataJSON); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	metadata := agentDetailsMapFromAny(parseJSON(metadataJSON))
	metadata["patch_progress"] = progress
	now := time.Now().Unix()
	if _, err := tx.ExecContext(ctx, `
		UPDATE engine.activity_history
		   SET metadata_json=$1,
		       updated_at=$2
		 WHERE id=$3
	`, mustJSONString(metadata), now, nullInt(activityID)); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if err := tx.Commit(); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return progress, http.StatusOK, nil
}

func normalizePatchInstallProgressPayload(payload map[string]any) map[string]any {
	phase := normalizePatchInstallProgressPhase(firstPatchProgressValue(payload, "phase", "status_phase"))
	if phase == "" {
		return nil
	}
	capturedAt := coerceInt64(payload["captured_at"])
	if capturedAt <= 0 {
		capturedAt = time.Now().Unix()
	}
	progress := map[string]any{
		"phase":       phase,
		"percent":     clampInt64(coerceInt64(payload["percent"]), 0, 100),
		"captured_at": capturedAt,
	}
	for _, key := range []string{"request_id", "hostname", "agent_id", "patch_key", "kb", "title", "update_id", "message", "stdout", "stderr"} {
		if value := cleanText(payload[key]); value != "" {
			progress[key] = value
		}
	}
	if _, exists := payload["revision_number"]; exists {
		progress["revision_number"] = coerceInt64(payload["revision_number"])
	}
	if _, exists := payload["current_update_index"]; exists {
		progress["current_update_index"] = coerceInt64(payload["current_update_index"])
	}
	if _, exists := payload["current_update_percent"]; exists {
		progress["current_update_percent"] = clampInt64(coerceInt64(payload["current_update_percent"]), 0, 100)
	}
	progress["display_label"] = patchInstallProgressDisplayLabel(progress)
	return progress
}

func normalizePatchInstallProgressPhase(value any) string {
	switch strings.ToLower(cleanText(value)) {
	case "download", "downloading":
		return "download"
	case "install", "installing":
		return "install"
	case "prepare", "preparing", "search", "searching":
		return "prepare"
	case "finalize", "finalizing":
		return "finalize"
	default:
		return ""
	}
}

func patchInstallProgressDisplayLabel(progress map[string]any) string {
	percent := clampInt64(coerceInt64(progress["percent"]), 0, 100)
	switch normalizePatchInstallProgressPhase(progress["phase"]) {
	case "download":
		return "Downloading " + strconvFormatInt(percent) + "%"
	case "install":
		return "Installing " + strconvFormatInt(percent) + "%"
	case "prepare":
		return "Preparing"
	case "finalize":
		return "Finalizing"
	default:
		return ""
	}
}

func patchProgressFromActivities(activities []map[string]any) map[string]any {
	var selected map[string]any
	for _, activity := range activities {
		progress := agentDetailsMapFromAny(activity["patch_progress"])
		if len(progress) == 0 {
			metadata := agentDetailsMapFromAny(activity["metadata"])
			progress = agentDetailsMapFromAny(metadata["patch_progress"])
		}
		if len(progress) == 0 {
			continue
		}
		if selected == nil || coerceInt64(progress["captured_at"]) >= coerceInt64(selected["captured_at"]) {
			selected = progress
		}
	}
	if selected == nil {
		return nil
	}
	if cleanText(selected["display_label"]) == "" {
		selected["display_label"] = patchInstallProgressDisplayLabel(selected)
	}
	return selected
}

func patchProgressTooltip(progress map[string]any) string {
	if len(progress) == 0 {
		return ""
	}
	parts := []string{}
	if label := cleanText(progress["display_label"]); label != "" {
		parts = append(parts, label)
	}
	if kb := cleanText(progress["kb"]); kb != "" {
		parts = append(parts, kb)
	}
	if title := cleanText(progress["title"]); title != "" {
		parts = append(parts, title)
	}
	if message := cleanText(progress["message"]); message != "" {
		parts = append(parts, message)
	}
	if capturedAt := coerceInt64(progress["captured_at"]); capturedAt > 0 {
		parts = append(parts, "Updated "+time.Unix(capturedAt, 0).Local().Format("2006-01-02 15:04:05"))
	}
	return strings.Join(parts, "\n")
}

func clampInt64(value int64, minValue int64, maxValue int64) int64 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func strconvFormatInt(value int64) string {
	return strconv.FormatInt(value, 10)
}

func firstPatchProgressValue(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if values == nil {
			return nil
		}
		if value, ok := values[key]; ok && value != nil {
			return value
		}
	}
	return nil
}
