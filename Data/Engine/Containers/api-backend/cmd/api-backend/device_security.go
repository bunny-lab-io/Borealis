package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
)

type deviceSecurityStore interface {
	setDeviceSecurityStatus(ctx context.Context, profile operatorProfile, guid string, status string, reason string) (deviceSecurityResult, int, error)
}

type deviceSecurityResult struct {
	Payload map[string]any
	AgentID string
}

func deviceSecurityStatusHandler(auth *authService, runtime devicePurgeRuntime, targetStatus string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, failure := requireAdmin(r.Context(), auth, r)
		if failure != nil {
			failure.write(w)
			return
		}
		guid := normalizeCanonicalGUID(r.PathValue("guid"))
		if guid == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_guid"})
			return
		}
		store, ok := auth.store.(deviceSecurityStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "device_security_unavailable"})
			return
		}
		reason := "operator_requested"
		if r.Body != nil {
			body, err := readJSONMap(r)
			if err != nil {
				invalidJSONOrValidation(w, err)
				return
			}
			reason = firstText(cleanText(body["reason"]), reason)
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		profile := operatorProfile{Username: identity.Username, Role: identity.Role}
		result, status, err := store.setDeviceSecurityStatus(ctx, profile, guid, targetStatus, reason)
		if err != nil {
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		payload := copyMap(result.Payload)
		if status == http.StatusOK && targetStatus != "active" {
			payload["runtime_cleanup"] = runtime.cleanupWithReason(r.Context(), result.AgentID, "device_"+targetStatus)
		}
		writeJSON(w, status, payload)
	}
}

func (s *postgresOperatorStore) setDeviceSecurityStatus(ctx context.Context, profile operatorProfile, guid string, status string, reason string) (deviceSecurityResult, int, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	if !stringInSet(status, "active", "quarantined", "revoked") {
		return deviceSecurityResult{Payload: map[string]any{"error": "invalid_status"}}, http.StatusBadRequest, nil
	}
	guid = normalizeCanonicalGUID(guid)
	if guid == "" {
		return deviceSecurityResult{Payload: map[string]any{"error": "invalid_guid"}}, http.StatusBadRequest, nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return deviceSecurityResult{}, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return deviceSecurityResult{}, http.StatusInternalServerError, err
	}
	defer rollbackQuietly(tx)

	var hostname, agentID, previousStatus sql.NullString
	var tokenVersion sql.NullInt64
	err = tx.QueryRowContext(ctx, `
		SELECT hostname, agent_id, COALESCE(status, 'active'), COALESCE(token_version, 1)
		  FROM engine.devices
		 WHERE UPPER(guid)=UPPER($1)
		 FOR UPDATE
	`, guid).Scan(&hostname, &agentID, &previousStatus, &tokenVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return deviceSecurityResult{Payload: map[string]any{"error": "not_found"}}, http.StatusNotFound, nil
	}
	if err != nil {
		return deviceSecurityResult{}, http.StatusInternalServerError, err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	nextVersion := tokenVersion.Int64 + 1
	if nextVersion <= 1 {
		nextVersion = 2
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE engine.devices
		   SET status=$1, token_version=$2
		 WHERE UPPER(guid)=UPPER($3)
	`, status, nextVersion, guid)
	if err != nil {
		return deviceSecurityResult{}, http.StatusInternalServerError, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return deviceSecurityResult{Payload: map[string]any{"error": "not_found"}}, http.StatusNotFound, nil
	}

	revokedRefreshTokens := int64(0)
	if status == "revoked" {
		refreshResult, err := tx.ExecContext(ctx, `
			UPDATE engine.refresh_tokens
			   SET revoked_at=$1
			 WHERE guid=$2
			   AND revoked_at IS NULL
		`, now, guid)
		if err != nil {
			return deviceSecurityResult{}, http.StatusInternalServerError, err
		}
		revokedRefreshTokens, _ = refreshResult.RowsAffected()
	}
	if err := tx.Commit(); err != nil {
		return deviceSecurityResult{}, http.StatusInternalServerError, err
	}

	payload := map[string]any{
		"status":                 status,
		"guid":                   guid,
		"hostname":               cleanText(hostname.String),
		"agent_id":               cleanText(agentID.String),
		"previous_status":        firstText(cleanText(previousStatus.String), "active"),
		"token_version":          nextVersion,
		"refresh_tokens_revoked": revokedRefreshTokens,
		"reason":                 firstText(cleanText(reason), "operator_requested"),
	}
	_ = profile
	return deviceSecurityResult{Payload: payload, AgentID: cleanText(agentID.String)}, http.StatusOK, nil
}
