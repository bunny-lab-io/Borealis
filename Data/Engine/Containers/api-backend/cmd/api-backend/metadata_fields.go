package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
)

const (
	metadataFieldCount      = 500
	metadataValueMaxLength  = 1024
	metadataFieldDefinition = "Field "
)

type metadataDefinitionStore interface {
	listMetadataDefinitions(ctx context.Context) ([]map[string]any, error)
}

func registerMetadataRoutes(mux *http.ServeMux, auth *authService) {
	mux.HandleFunc("GET /api/metadata_fields", metadataFieldsHandler(auth))
}

func metadataFieldsHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		if _, failure := requireUser(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		store, ok := auth.store.(metadataDefinitionStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "metadata_fields_unavailable"})
			return
		}

		timeout := auth.timeout
		if timeout <= 0 {
			timeout = defaultAuthTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		fields, err := store.listMetadataDefinitions(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"fields":      fields,
			"count":       len(fields),
			"value_limit": metadataValueMaxLength,
		})
	}
}

func (s *postgresOperatorStore) listMetadataDefinitions(ctx context.Context) ([]map[string]any, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		ctx,
		`
		SELECT field_number, description, updated_at, updated_by
		  FROM engine.metadata_field_definitions
		 WHERE field_number BETWEEN 1 AND $1
		`,
		metadataFieldCount,
	)
	if err != nil {
		return buildMetadataDefinitions(map[int]metadataDefinitionRow{}), nil
	}
	defer rows.Close()

	descriptions := map[int]metadataDefinitionRow{}
	for rows.Next() {
		var row metadataDefinitionRow
		if err := rows.Scan(&row.FieldNumber, &row.Description, &row.UpdatedAt, &row.UpdatedBy); err != nil {
			return nil, err
		}
		fieldNumber := int(nullInt(row.FieldNumber))
		if fieldNumber < 1 || fieldNumber > metadataFieldCount {
			continue
		}
		descriptions[fieldNumber] = row
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return buildMetadataDefinitions(descriptions), nil
}

type metadataDefinitionRow struct {
	FieldNumber sql.NullInt64
	Description sql.NullString
	UpdatedAt   sql.NullInt64
	UpdatedBy   sql.NullString
}

func buildMetadataDefinitions(descriptions map[int]metadataDefinitionRow) []map[string]any {
	fields := make([]map[string]any, 0, metadataFieldCount)
	for fieldNumber := 1; fieldNumber <= metadataFieldCount; fieldNumber++ {
		defaultLabel := metadataFieldLabel(fieldNumber)
		definition := descriptions[fieldNumber]
		description := nullString(definition.Description)
		label := description
		if label == "" {
			label = defaultLabel
		}
		fields = append(fields, map[string]any{
			"field_number":  fieldNumber,
			"field_key":     metadataFieldKey(fieldNumber),
			"default_label": defaultLabel,
			"label":         label,
			"description":   description,
			"updated_at":    nullInt(definition.UpdatedAt),
			"updated_by":    nullString(definition.UpdatedBy),
			"value_limit":   metadataValueMaxLength,
		})
	}
	return fields
}

func metadataFieldKey(fieldNumber int) string {
	return "field_" + leftPad3(fieldNumber)
}

func metadataFieldLabel(fieldNumber int) string {
	return metadataFieldDefinition + leftPad3(fieldNumber)
}

func leftPad3(value int) string {
	text := strconv.Itoa(value)
	for len(text) < 3 {
		text = "0" + text
	}
	return text
}
