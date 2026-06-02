package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

type deviceViewStore interface {
	listDeviceViews(ctx context.Context) ([]map[string]any, error)
	getDeviceView(ctx context.Context, viewID int64) (map[string]any, bool, error)
}

type deviceViewRow struct {
	ID          sql.NullInt64
	Name        sql.NullString
	ColumnsJSON sql.NullString
	FiltersJSON sql.NullString
	CreatedAt   sql.NullInt64
	UpdatedAt   sql.NullInt64
}

func registerDeviceViewRoutes(mux *http.ServeMux, auth *authService, fallback http.Handler) {
	mux.HandleFunc("/api/device_list_views", deviceViewListHandler(auth, fallback))
	mux.HandleFunc("/api/device_list_views/", deviceViewGetHandler(auth, fallback))
}

func deviceViewListHandler(auth *authService, fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			proxyFallbackOrMethodNotAllowed(w, r, fallback, http.MethodGet)
			return
		}
		if _, failure := requireUser(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		store, ok := auth.store.(deviceViewStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "device_views_unavailable"})
			return
		}
		timeout := auth.timeout
		if timeout <= 0 {
			timeout = defaultAuthTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		views, err := store.listDeviceViews(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"views": views})
	}
}

func deviceViewGetHandler(auth *authService, fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			proxyFallbackOrMethodNotAllowed(w, r, fallback, http.MethodGet)
			return
		}
		if _, failure := requireUser(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		viewID, err := parseDeviceViewID(r.URL.Path)
		if err != nil || viewID <= 0 {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}
		store, ok := auth.store.(deviceViewStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "device_views_unavailable"})
			return
		}
		timeout := auth.timeout
		if timeout <= 0 {
			timeout = defaultAuthTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		view, found, err := store.getDeviceView(ctx, viewID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		if !found {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusOK, view)
	}
}

func (s *postgresOperatorStore) listDeviceViews(ctx context.Context) ([]map[string]any, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		ctx,
		`
		SELECT id, name, columns_json, filters_json, created_at, updated_at
		  FROM engine.device_list_views
	  ORDER BY LOWER(name) ASC, id ASC
		`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rawRows := make([]deviceViewRow, 0)
	for rows.Next() {
		var row deviceViewRow
		if err := rows.Scan(&row.ID, &row.Name, &row.ColumnsJSON, &row.FiltersJSON, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, err
		}
		rawRows = append(rawRows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	views := make([]map[string]any, 0, len(rawRows))
	for _, row := range rawRows {
		views = append(views, buildDeviceViewPayload(row))
	}
	return views, nil
}

func (s *postgresOperatorStore) getDeviceView(ctx context.Context, viewID int64) (map[string]any, bool, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, false, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	var row deviceViewRow
	err = conn.QueryRowContext(
		ctx,
		`
		SELECT id, name, columns_json, filters_json, created_at, updated_at
		  FROM engine.device_list_views
		 WHERE id = $1
		`,
		viewID,
	).Scan(&row.ID, &row.Name, &row.ColumnsJSON, &row.FiltersJSON, &row.CreatedAt, &row.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return buildDeviceViewPayload(row), true, nil
}

func buildDeviceViewPayload(row deviceViewRow) map[string]any {
	return map[string]any{
		"id":         nullInt(row.ID),
		"name":       nullString(row.Name),
		"columns":    parseJSONArray(row.ColumnsJSON),
		"filters":    parseJSONObject(row.FiltersJSON),
		"created_at": nullInt(row.CreatedAt),
		"updated_at": nullInt(row.UpdatedAt),
	}
}

func parseDeviceViewID(path string) (int64, error) {
	raw := strings.TrimPrefix(path, "/api/device_list_views/")
	if raw == "" || strings.Contains(raw, "/") {
		return 0, strconv.ErrSyntax
	}
	return strconv.ParseInt(raw, 10, 64)
}
