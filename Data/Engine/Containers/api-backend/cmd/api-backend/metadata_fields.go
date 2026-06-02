package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	metadataFieldCount      = 500
	metadataValueMaxLength  = 1024
	metadataFieldDefinition = "Field "
)

type metadataDefinitionStore interface {
	listMetadataDefinitions(ctx context.Context) ([]map[string]any, error)
}

type deviceMetadataStore interface {
	deviceMetadataFields(ctx context.Context, profile operatorProfile, deviceID string) (map[string]any, int, error)
}

type metadataDeviceRecord struct {
	GUID     sql.NullString
	Hostname sql.NullString
	SiteID   sql.NullInt64
	SiteName sql.NullString
}

type deviceMetadataValueRow struct {
	FieldNumber sql.NullInt64
	FieldKey    sql.NullString
	Value       sql.NullString
	ModifiedAt  sql.NullInt64
	Source      sql.NullString
	Actor       sql.NullString
	CreatedAt   sql.NullInt64
	UpdatedAt   sql.NullInt64
}

func registerMetadataRoutes(mux *http.ServeMux, auth *authService, fallback http.Handler) {
	mux.HandleFunc("GET /api/metadata_fields", metadataFieldsHandler(auth))
	mux.HandleFunc("/api/devices/", deviceMetadataFieldsHandler(auth, fallback))
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

	return listMetadataDefinitionsFromConn(ctx, conn)
}

func listMetadataDefinitionsFromConn(ctx context.Context, conn *sql.Conn) ([]map[string]any, error) {
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

func deviceMetadataFieldsHandler(auth *authService, fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deviceID, ok := parseDeviceMetadataFieldsPath(r.URL.Path)
		if !ok {
			proxyFallbackOrMethodNotAllowed(w, r, fallback, http.MethodGet)
			return
		}
		if r.Method != http.MethodGet {
			proxyFallbackOrMethodNotAllowed(w, r, fallback, http.MethodGet)
			return
		}
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
		store, ok := auth.store.(deviceMetadataStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "device_metadata_fields_unavailable"})
			return
		}

		timeout := auth.timeout
		if timeout <= 0 {
			timeout = defaultAuthTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		payload, status, err := store.deviceMetadataFields(ctx, profile, deviceID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, status, payload)
	}
}

func parseDeviceMetadataFieldsPath(path string) (string, bool) {
	rest := strings.TrimPrefix(path, "/api/devices/")
	if rest == path {
		return "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || parts[1] != "metadata_fields" {
		return "", false
	}
	deviceID, err := url.PathUnescape(parts[0])
	if err != nil {
		deviceID = parts[0]
	}
	deviceID = strings.TrimSpace(deviceID)
	return deviceID, deviceID != ""
}

func (s *postgresOperatorStore) deviceMetadataFields(ctx context.Context, profile operatorProfile, deviceID string) (map[string]any, int, error) {
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, 0, err
	}
	if allowedSiteIDs != nil && len(allowedSiteIDs) == 0 {
		return deviceMetadataNotFoundPayload(), http.StatusNotFound, nil
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, 0, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	device, ok, err := resolveMetadataDevice(ctx, conn, deviceID)
	if err != nil {
		return nil, 0, err
	}
	if !ok || !metadataDeviceVisible(device.SiteID, allowedSiteIDs) {
		return deviceMetadataNotFoundPayload(), http.StatusNotFound, nil
	}

	fields, err := listMetadataDefinitionsFromConn(ctx, conn)
	if err != nil {
		return nil, 0, err
	}
	values, err := metadataDeviceValues(ctx, conn, nullString(device.GUID))
	if err != nil {
		return nil, 0, err
	}
	rows := make([]map[string]any, 0, len(fields))
	for _, definition := range fields {
		fieldNumber := int(coerceInt64(definition["field_number"]))
		value := values[fieldNumber]
		decoded := decodeMetadataValue(nullString(value.Value))
		row := make(map[string]any, len(definition)+5)
		for key, item := range definition {
			row[key] = item
		}
		row["value"] = decoded
		row["modified_at"] = nullInt(value.ModifiedAt)
		row["source"] = nullString(value.Source)
		row["actor"] = nullString(value.Actor)
		row["has_value"] = decoded != ""
		rows = append(rows, row)
	}

	return map[string]any{
		"device": map[string]any{
			"guid":      nullString(device.GUID),
			"hostname":  nullString(device.Hostname),
			"site_id":   nullInt(device.SiteID),
			"site_name": nullString(device.SiteName),
		},
		"fields":      rows,
		"count":       len(rows),
		"value_limit": metadataValueMaxLength,
	}, http.StatusOK, nil
}

func resolveMetadataDevice(ctx context.Context, conn *sql.Conn, deviceID string) (metadataDeviceRecord, bool, error) {
	var device metadataDeviceRecord
	err := conn.QueryRowContext(
		ctx,
		`
		SELECT d.guid, d.hostname, ds.site_id, s.name
		  FROM engine.devices AS d
	 LEFT JOIN engine.device_sites AS ds ON ds.device_hostname = d.hostname
	 LEFT JOIN engine.sites AS s ON s.id = ds.site_id
		 WHERE LOWER(d.guid) = LOWER($1)
		    OR d.hostname = $1
		    OR d.agent_id = $1
		 LIMIT 1
		`,
		deviceID,
	).Scan(&device.GUID, &device.Hostname, &device.SiteID, &device.SiteName)
	if errors.Is(err, sql.ErrNoRows) {
		return metadataDeviceRecord{}, false, nil
	}
	if err != nil {
		return metadataDeviceRecord{}, false, err
	}
	return device, true, nil
}

func metadataDeviceValues(ctx context.Context, conn *sql.Conn, deviceGUID string) (map[int]deviceMetadataValueRow, error) {
	if strings.TrimSpace(deviceGUID) == "" {
		return map[int]deviceMetadataValueRow{}, nil
	}
	rows, err := conn.QueryContext(
		ctx,
		`
		SELECT field_number, field_key, value, modified_at, source, actor, created_at, updated_at
		  FROM engine.device_metadata_fields
		 WHERE device_guid = $1
		   AND field_number BETWEEN 1 AND $2
		`,
		deviceGUID,
		metadataFieldCount,
	)
	if err != nil {
		return map[int]deviceMetadataValueRow{}, nil
	}
	defer rows.Close()

	values := map[int]deviceMetadataValueRow{}
	for rows.Next() {
		var row deviceMetadataValueRow
		if err := rows.Scan(
			&row.FieldNumber,
			&row.FieldKey,
			&row.Value,
			&row.ModifiedAt,
			&row.Source,
			&row.Actor,
			&row.CreatedAt,
			&row.UpdatedAt,
		); err != nil {
			return nil, err
		}
		fieldNumber := int(nullInt(row.FieldNumber))
		if fieldNumber < 1 || fieldNumber > metadataFieldCount {
			continue
		}
		values[fieldNumber] = row
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func metadataDeviceVisible(siteID sql.NullInt64, allowedSiteIDs []int64) bool {
	if allowedSiteIDs == nil {
		return true
	}
	if !siteID.Valid {
		return false
	}
	for _, allowed := range allowedSiteIDs {
		if allowed == siteID.Int64 {
			return true
		}
	}
	return false
}

func deviceMetadataNotFoundPayload() map[string]any {
	return map[string]any{
		"error":   "not_found",
		"message": "Device not found.",
	}
}

func decodeMetadataValue(value string) string {
	if value == "" {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return normalizeMetadataValue(value)
	}
	return normalizeMetadataValue(string(decoded))
}

func normalizeMetadataValue(value string) string {
	if len(value) > metadataValueMaxLength {
		return value[:metadataValueMaxLength]
	}
	return value
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
