package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
)

type softwareIconAsset struct {
	Hash     string
	MIMEType string
	Bytes    []byte
}

type softwareIconStore interface {
	loadSoftwareIconAsset(ctx context.Context, iconHash string) (softwareIconAsset, bool, error)
}

func registerSoftwareRoutes(mux *http.ServeMux, auth *authService, fallback http.Handler) {
	mux.HandleFunc("GET /api/device/software/icon/{icon_hash}", softwareIconHandler(auth))
	mux.HandleFunc("POST /api/device/software/{hostname}/refresh", softwareRefreshHandler(auth))
	mux.HandleFunc("POST /api/device/software/{hostname}/icon-override", deviceSoftwareOverrideHandler(auth, "icon-override"))
	mux.HandleFunc("POST /api/device/software/{hostname}/uninstall-override", deviceSoftwareOverrideHandler(auth, "uninstall-override"))
	mux.HandleFunc("POST /api/device/software/{hostname}/uninstall-block", deviceSoftwareOverrideHandler(auth, "uninstall-block"))
	mux.HandleFunc("POST /api/device/software/{hostname}/uninstall-unblock", deviceSoftwareOverrideHandler(auth, "uninstall-unblock"))
	mux.HandleFunc("GET /api/device/services/{hostname}", deviceServicesHandler(auth))
	mux.HandleFunc("POST /api/device/services/{hostname}/action", deviceServiceActionHandler(auth))
	mux.HandleFunc("GET /api/software/audit", softwareAuditHandler(auth))
	mux.HandleFunc("POST /api/software/action/{action}", bulkSoftwareActionHandler(auth))
	mux.HandleFunc("/api/software/", softwareSubtreeHandler(fallback))
}

func softwareIconHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, failure := requireUser(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		iconHash := normalizeSoftwareIconHash(r.PathValue("icon_hash"))
		if iconHash == "" {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}
		store, ok := auth.store.(softwareIconStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "software_icon_unavailable"})
			return
		}
		timeout := auth.timeout
		if timeout <= 0 {
			timeout = defaultAuthTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		asset, found, err := store.loadSoftwareIconAsset(ctx, iconHash)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		if !found || len(asset.Bytes) == 0 {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}
		mimeType := strings.TrimSpace(asset.MIMEType)
		if mimeType == "" {
			mimeType = "image/png"
		}
		w.Header().Set("Content-Type", mimeType)
		w.Header().Set("Cache-Control", "private, max-age=86400")
		w.Header().Set("ETag", iconHash)
		w.Header().Set("Content-Disposition", "inline; filename=\""+iconHash+".png\"")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(asset.Bytes)
	}
}

func softwareSubtreeHandler(fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if fallback == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
			return
		}
		fallback.ServeHTTP(w, r)
	}
}

func softwareRefreshHandler(auth *authService) http.HandlerFunc {
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
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "software_refresh_unavailable"})
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
					"reason":       "operator_query_software_updates",
				}
				result, workerStatus, workerErr := emitWorkerHostServiceEvent(r.Context(), auth, snapshot.Route, map[string]any{
					"hostname":            snapshot.Hostname,
					"service_mode":        "system",
					"event_name":          "software_inventory_refresh_request",
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
				_ = workerStatus
				_ = workerErr
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
			"message":      "The agent SYSTEM socket is not available to query software changes right now.",
			"hostname":     firstText(lastSnapshot.Hostname, hostname),
			"agent_id":     lastSnapshot.AgentID,
			"requested_at": requestedAt,
		})
	}
}

func (s *postgresOperatorStore) loadSoftwareIconAsset(ctx context.Context, iconHash string) (softwareIconAsset, bool, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return softwareIconAsset{}, false, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	var asset softwareIconAsset
	err = conn.QueryRowContext(ctx, `
		SELECT icon_hash, mime_type, icon_bytes
		  FROM engine.software_icon_assets
		 WHERE icon_hash = $1
		 LIMIT 1
	`, iconHash).Scan(&asset.Hash, &asset.MIMEType, &asset.Bytes)
	if errors.Is(err, sql.ErrNoRows) {
		return softwareIconAsset{}, false, nil
	}
	if err != nil {
		return softwareIconAsset{}, false, err
	}
	asset.Hash = normalizeSoftwareIconHash(asset.Hash)
	if asset.MIMEType == "" {
		asset.MIMEType = "image/png"
	}
	return asset, true, nil
}

func normalizeSoftwareIconHash(value string) string {
	text := strings.ToLower(strings.TrimSpace(value))
	if len(text) != 64 {
		return ""
	}
	for _, char := range text {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return ""
		}
	}
	return text
}
