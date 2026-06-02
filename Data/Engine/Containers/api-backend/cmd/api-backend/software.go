package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
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
