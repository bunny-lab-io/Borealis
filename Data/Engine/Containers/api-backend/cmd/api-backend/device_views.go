package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
)

type deviceViewStore interface {
	listDeviceViews(ctx context.Context) ([]map[string]any, error)
	getDeviceView(ctx context.Context, viewID int64) (map[string]any, bool, error)
	createDeviceView(ctx context.Context, request deviceViewMutation) (map[string]any, int, error)
	updateDeviceView(ctx context.Context, viewID int64, request deviceViewMutation) (map[string]any, int, error)
	deleteDeviceView(ctx context.Context, viewID int64) (map[string]any, int, error)
}

type deviceViewMutation struct {
	Name       *string
	Columns    []string
	ColumnsSet bool
	Filters    map[string]any
	FiltersSet bool
}

type deviceViewRow struct {
	ID          sql.NullInt64
	Name        sql.NullString
	ColumnsJSON sql.NullString
	FiltersJSON sql.NullString
	CreatedAt   sql.NullInt64
	UpdatedAt   sql.NullInt64
}

func registerDeviceViewRoutes(mux *http.ServeMux, auth *authService) {
	mux.HandleFunc("/api/device_list_views", deviceViewListHandler(auth))
	mux.HandleFunc("/api/device_list_views/", deviceViewGetHandler(auth))
}

func deviceViewListHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			writeMethodNotAllowed(w, strings.Join([]string{http.MethodGet, http.MethodPost}, ", "))
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
		if r.Method == http.MethodPost {
			mutation, status, payload := parseCreateDeviceViewMutation(r)
			if status != 0 {
				writeJSON(w, status, payload)
				return
			}
			view, status, err := store.createDeviceView(ctx, mutation)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, status, view)
			return
		}

		views, err := store.listDeviceViews(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"views": views})
	}
}

func deviceViewGetHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodPut && r.Method != http.MethodDelete {
			writeMethodNotAllowed(w, strings.Join([]string{http.MethodGet, http.MethodPut, http.MethodDelete}, ", "))
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
		if r.Method == http.MethodPut {
			mutation, status, payload := parseUpdateDeviceViewMutation(r)
			if status != 0 {
				writeJSON(w, status, payload)
				return
			}
			view, status, err := store.updateDeviceView(ctx, viewID, mutation)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, status, view)
			return
		}
		if r.Method == http.MethodDelete {
			payload, status, err := store.deleteDeviceView(ctx, viewID)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, status, payload)
			return
		}

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

func (s *postgresOperatorStore) createDeviceView(ctx context.Context, request deviceViewMutation) (map[string]any, int, error) {
	now := time.Now().Unix()
	columnsJSON, filtersJSON, err := encodeDeviceViewJSON(request.Columns, request.Filters)
	if err != nil {
		return nil, 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, errors.Join(errOperatorStoreDown, err)
	}
	defer rollbackQuietly(tx)

	var viewID int64
	err = tx.QueryRowContext(
		ctx,
		`
		INSERT INTO engine.device_list_views(name, columns_json, filters_json, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
		`,
		*request.Name,
		columnsJSON,
		filtersJSON,
		now,
		now,
	).Scan(&viewID)
	if err != nil {
		if isPostgresUniqueViolation(err) {
			return map[string]any{"error": "name already exists"}, http.StatusConflict, nil
		}
		return nil, 0, err
	}
	view, found, err := deviceViewFromTx(ctx, tx, viewID)
	if err != nil {
		return nil, 0, err
	}
	if !found {
		return map[string]any{"error": "creation_failed"}, http.StatusInternalServerError, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return view, http.StatusCreated, nil
}

func (s *postgresOperatorStore) updateDeviceView(ctx context.Context, viewID int64, request deviceViewMutation) (map[string]any, int, error) {
	fields := []string{}
	params := []any{}
	if request.Name != nil {
		params = append(params, *request.Name)
		fields = append(fields, "name = $"+strconv.Itoa(len(params)))
	}
	if request.ColumnsSet {
		columnsJSON, err := marshalJSON(request.Columns)
		if err != nil {
			return nil, 0, err
		}
		params = append(params, columnsJSON)
		fields = append(fields, "columns_json = $"+strconv.Itoa(len(params)))
	}
	if request.FiltersSet {
		filtersJSON, err := marshalJSON(request.Filters)
		if err != nil {
			return nil, 0, err
		}
		params = append(params, filtersJSON)
		fields = append(fields, "filters_json = $"+strconv.Itoa(len(params)))
	}
	params = append(params, time.Now().Unix())
	fields = append(fields, "updated_at = $"+strconv.Itoa(len(params)))
	params = append(params, viewID)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, errors.Join(errOperatorStoreDown, err)
	}
	defer rollbackQuietly(tx)

	result, err := tx.ExecContext(ctx, "UPDATE engine.device_list_views SET "+strings.Join(fields, ", ")+" WHERE id = $"+strconv.Itoa(len(params)), params...)
	if err != nil {
		if isPostgresUniqueViolation(err) {
			return map[string]any{"error": "name already exists"}, http.StatusConflict, nil
		}
		return nil, 0, err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return map[string]any{"error": "not found"}, http.StatusNotFound, nil
	}
	view, found, err := deviceViewFromTx(ctx, tx, viewID)
	if err != nil {
		return nil, 0, err
	}
	if !found {
		return map[string]any{"error": "not found"}, http.StatusNotFound, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return view, http.StatusOK, nil
}

func (s *postgresOperatorStore) deleteDeviceView(ctx context.Context, viewID int64) (map[string]any, int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, errors.Join(errOperatorStoreDown, err)
	}
	defer rollbackQuietly(tx)

	result, err := tx.ExecContext(ctx, "DELETE FROM engine.device_list_views WHERE id = $1", viewID)
	if err != nil {
		return nil, 0, err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return map[string]any{"error": "not found"}, http.StatusNotFound, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return map[string]any{"status": "ok"}, http.StatusOK, nil
}

func deviceViewFromTx(ctx context.Context, tx *sql.Tx, viewID int64) (map[string]any, bool, error) {
	var row deviceViewRow
	err := tx.QueryRowContext(
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

func parseCreateDeviceViewMutation(r *http.Request) (deviceViewMutation, int, map[string]any) {
	body, err := readDeviceViewBody(r)
	if err != nil {
		return deviceViewMutation{}, http.StatusBadRequest, map[string]any{"error": "invalid json"}
	}
	name := strings.TrimSpace(cleanText(body["name"]))
	if name == "" {
		return deviceViewMutation{}, http.StatusBadRequest, map[string]any{"error": "name is required"}
	}
	if strings.EqualFold(name, "default view") {
		return deviceViewMutation{}, http.StatusBadRequest, map[string]any{"error": "reserved name"}
	}
	columns, ok := stringSliceFromAny(body["columns"])
	if !ok {
		return deviceViewMutation{}, http.StatusBadRequest, map[string]any{"error": "columns must be a list of strings"}
	}
	filters, ok := mapFromAny(body["filters"])
	if !ok {
		return deviceViewMutation{}, http.StatusBadRequest, map[string]any{"error": "filters must be an object"}
	}
	return deviceViewMutation{Name: &name, Columns: columns, ColumnsSet: true, Filters: filters, FiltersSet: true}, 0, nil
}

func parseUpdateDeviceViewMutation(r *http.Request) (deviceViewMutation, int, map[string]any) {
	body, err := readDeviceViewBody(r)
	if err != nil {
		return deviceViewMutation{}, http.StatusBadRequest, map[string]any{"error": "invalid json"}
	}
	mutation := deviceViewMutation{}
	if value, ok := body["name"]; ok {
		name := strings.TrimSpace(cleanText(value))
		if name == "" {
			return deviceViewMutation{}, http.StatusBadRequest, map[string]any{"error": "name cannot be empty"}
		}
		if strings.EqualFold(name, "default view") {
			return deviceViewMutation{}, http.StatusBadRequest, map[string]any{"error": "reserved name"}
		}
		mutation.Name = &name
	}
	if value, ok := body["columns"]; ok {
		columns, valid := stringSliceFromAny(value)
		if !valid {
			return deviceViewMutation{}, http.StatusBadRequest, map[string]any{"error": "columns must be a list of strings"}
		}
		mutation.Columns = columns
		mutation.ColumnsSet = true
	}
	if value, ok := body["filters"]; ok {
		filters, valid := mapFromAny(value)
		if !valid {
			return deviceViewMutation{}, http.StatusBadRequest, map[string]any{"error": "filters must be an object"}
		}
		mutation.Filters = filters
		mutation.FiltersSet = true
	}
	return mutation, 0, nil
}

func readDeviceViewBody(r *http.Request) (map[string]any, error) {
	if r.Body == nil {
		return map[string]any{}, nil
	}
	defer r.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		if errors.Is(err, io.EOF) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	if body == nil {
		body = map[string]any{}
	}
	return body, nil
}

func stringSliceFromAny(value any) ([]string, bool) {
	if value == nil {
		return []string{}, true
	}
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, false
		}
		out = append(out, text)
	}
	return out, true
}

func mapFromAny(value any) (map[string]any, bool) {
	if value == nil {
		return map[string]any{}, true
	}
	typed, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	return typed, true
}

func encodeDeviceViewJSON(columns []string, filters map[string]any) (string, string, error) {
	columnsJSON, err := marshalJSON(columns)
	if err != nil {
		return "", "", err
	}
	filtersJSON, err := marshalJSON(filters)
	if err != nil {
		return "", "", err
	}
	return columnsJSON, filtersJSON, nil
}

func marshalJSON(value any) (string, error) {
	content, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

func isPostgresUniqueViolation(err error) bool {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" {
		return true
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "duplicate key") || strings.Contains(text, "unique constraint")
}

func rollbackQuietly(tx *sql.Tx) {
	if tx != nil {
		_ = tx.Rollback()
	}
}
