package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	metadataFieldCount      = 500
	metadataValueMaxLength  = 1024
	metadataFieldDefinition = "Field "
)

const reservedMetadataFieldTooltip = "Reserved Borealis Metadata Field - Create a scheduled job using the hyperlinked assembly to collect data for this field."
const reservedMetadataPlaceholderTooltip = "Reserved Borealis Metadata Field - Reserved for future Borealis use."

type reservedMetadataField struct {
	Description  string
	AssemblyName string
	AssemblyGUID string
	AssemblyType string
}

var reservedMetadataFields = map[int]reservedMetadataField{
	1: {
		Description:  "Server Roles",
		AssemblyName: "Detect Server Roles [WIN]",
		AssemblyGUID: "628f6686-c7c4-477d-bf9a-13c73d8246ba",
		AssemblyType: "script",
	},
	2: {
		Description:  "Bitlocker Drive Encryption",
		AssemblyName: "Audit Bitlocker / TPM Status [WIN]",
		AssemblyGUID: "c4f97974-1d9c-4e89-8257-8a139637e51f",
		AssemblyType: "script",
	},
	3:  {Description: "Reserved"},
	4:  {Description: "Reserved"},
	5:  {Description: "Reserved"},
	6:  {Description: "Reserved"},
	7:  {Description: "Reserved"},
	8:  {Description: "Reserved"},
	9:  {Description: "Reserved"},
	10: {Description: "Reserved"},
}

type metadataDefinitionStore interface {
	listMetadataDefinitions(ctx context.Context) ([]map[string]any, error)
	updateMetadataDefinition(ctx context.Context, fieldNumber int, description string, actor string) (map[string]any, int, error)
}

type deviceMetadataStore interface {
	deviceMetadataFields(ctx context.Context, profile operatorProfile, deviceID string) (map[string]any, int, error)
	updateDeviceMetadataField(ctx context.Context, profile operatorProfile, deviceID string, fieldNumber int, value string) (map[string]any, int, error)
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

func registerMetadataRoutes(mux *http.ServeMux, auth *authService) {
	mux.HandleFunc("GET /api/metadata_fields", metadataFieldsHandler(auth))
	mux.HandleFunc("PUT /api/metadata_fields/{field_number}", metadataFieldDefinitionHandler(auth))
	mux.HandleFunc("GET /api/devices/{device_id}/metadata_fields", deviceMetadataFieldsHandler(auth))
	mux.HandleFunc("PUT /api/devices/{device_id}/metadata_fields/{field_number}", deviceMetadataFieldsHandler(auth))
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

func metadataFieldDefinitionHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fieldNumber, ok := parseMetadataFieldDefinitionPath(r.URL.Path)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_field", "message": "Field number must be between 1 and 500."})
			return
		}
		if r.Method != http.MethodPut {
			writeMethodNotAllowed(w, http.MethodPut)
			return
		}
		identity, failure := requireAdmin(r.Context(), auth, r)
		if failure != nil {
			failure.write(w)
			return
		}
		if _, reserved := reservedMetadataFieldDefinition(fieldNumber); reserved {
			writeJSON(w, http.StatusConflict, reservedMetadataFieldUpdatePayload(fieldNumber))
			return
		}
		store, ok := auth.store.(metadataDefinitionStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "metadata_fields_unavailable"})
			return
		}

		body, err := readJSONMap(r)
		if err != nil {
			invalidJSONOrValidation(w, err)
			return
		}
		description := cleanText(body["description"])

		timeout := auth.timeout
		if timeout <= 0 {
			timeout = defaultAuthTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		payload, status, err := store.updateMetadataDefinition(ctx, fieldNumber, description, firstText(identity.Username, "Unknown"))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, status, payload)
	}
}

func parseMetadataFieldDefinitionPath(path string) (int, bool) {
	rest := strings.Trim(strings.TrimPrefix(path, "/api/metadata_fields/"), "/")
	if rest == "" || strings.Contains(rest, "/") {
		return 0, false
	}
	fieldNumber, err := strconv.Atoi(rest)
	if err != nil || fieldNumber < 1 || fieldNumber > metadataFieldCount {
		return 0, false
	}
	return fieldNumber, true
}

func (s *postgresOperatorStore) updateMetadataDefinition(ctx context.Context, fieldNumber int, description string, actor string) (map[string]any, int, error) {
	if fieldNumber < 1 || fieldNumber > metadataFieldCount {
		return map[string]any{"error": "invalid_field", "message": "Field number must be between 1 and 500."}, http.StatusBadRequest, nil
	}
	if _, reserved := reservedMetadataFieldDefinition(fieldNumber); reserved {
		return reservedMetadataFieldUpdatePayload(fieldNumber), http.StatusConflict, nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, 0, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	now := time.Now().Unix()
	cleanDescription := normalizeMetadataDescription(description)
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO engine.metadata_field_definitions(field_number, description, updated_at, updated_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT(field_number) DO UPDATE SET
		    description = EXCLUDED.description,
		    updated_at = EXCLUDED.updated_at,
		    updated_by = EXCLUDED.updated_by
	`, fieldNumber, cleanDescription, now, truncateMetadataText(actor, 255)); err != nil {
		return nil, 0, err
	}
	return map[string]any{"field": metadataDefinitionPayload(fieldNumber, cleanDescription, now, truncateMetadataText(actor, 255))}, http.StatusOK, nil
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

func deviceMetadataFieldsHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		parsed, ok := parseDeviceMetadataFieldsPath(r.URL.Path)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}
		if r.Method == http.MethodGet && parsed.hasField {
			writeMethodNotAllowed(w, http.MethodPut)
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

		var payload map[string]any
		var status int
		if r.Method == http.MethodGet && !parsed.hasField {
			payload, status, err = store.deviceMetadataFields(ctx, profile, parsed.deviceID)
		} else if r.Method == http.MethodPut && parsed.hasField {
			body, err := readJSONMap(r)
			if err != nil {
				invalidJSONOrValidation(w, err)
				return
			}
			payload, status, err = store.updateDeviceMetadataField(ctx, profile, parsed.deviceID, parsed.fieldNumber, normalizeMetadataValue(cleanText(body["value"])))
		} else {
			writeMethodNotAllowed(w, "GET, PUT")
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, status, payload)
	}
}

type parsedDeviceMetadataPath struct {
	deviceID    string
	fieldNumber int
	hasField    bool
}

func parseDeviceMetadataFieldsPath(path string) (parsedDeviceMetadataPath, bool) {
	rest := strings.TrimPrefix(path, "/api/devices/")
	if rest == path {
		return parsedDeviceMetadataPath{}, false
	}
	parts := strings.Split(rest, "/")
	if (len(parts) != 2 && len(parts) != 3) || strings.TrimSpace(parts[0]) == "" || parts[1] != "metadata_fields" {
		return parsedDeviceMetadataPath{}, false
	}
	deviceID, err := url.PathUnescape(parts[0])
	if err != nil {
		deviceID = parts[0]
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return parsedDeviceMetadataPath{}, false
	}
	parsed := parsedDeviceMetadataPath{deviceID: deviceID}
	if len(parts) == 3 {
		fieldNumber, err := strconv.Atoi(strings.TrimSpace(parts[2]))
		if err != nil || fieldNumber < 1 || fieldNumber > metadataFieldCount {
			return parsedDeviceMetadataPath{}, false
		}
		parsed.fieldNumber = fieldNumber
		parsed.hasField = true
	}
	return parsed, true
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

func (s *postgresOperatorStore) updateDeviceMetadataField(ctx context.Context, profile operatorProfile, deviceID string, fieldNumber int, value string) (map[string]any, int, error) {
	if fieldNumber < 1 || fieldNumber > metadataFieldCount {
		return map[string]any{"error": "invalid_field", "message": "Field number must be between 1 and 500."}, http.StatusBadRequest, nil
	}
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

	device, ok, err := resolveMetadataDeviceTx(ctx, tx, deviceID)
	if err != nil {
		return nil, 0, err
	}
	if !ok || !metadataDeviceVisible(device.SiteID, allowedSiteIDs) {
		return deviceMetadataNotFoundPayload(), http.StatusNotFound, nil
	}

	now := time.Now().Unix()
	actor := firstText(profile.Username, "Unknown")
	encodedValue := encodeMetadataValue(value)
	fieldKey := metadataFieldKey(fieldNumber)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO engine.device_metadata_fields(
		    device_guid, field_number, field_key, value, modified_at, source, actor, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, 'engine', $6, $7, $7)
		ON CONFLICT(device_guid, field_number) DO UPDATE SET
		    field_key = EXCLUDED.field_key,
		    value = EXCLUDED.value,
		    modified_at = EXCLUDED.modified_at,
		    source = EXCLUDED.source,
		    actor = EXCLUDED.actor,
		    updated_at = EXCLUDED.updated_at
	`, nullString(device.GUID), fieldNumber, fieldKey, encodedValue, now, truncateMetadataText(actor, 255), now); err != nil {
		return nil, 0, err
	}

	metadata, _ := json.Marshal(map[string]any{
		"device_guid":  nullString(device.GUID),
		"field_number": fieldNumber,
		"field_key":    fieldKey,
		"source":       "engine",
		"actor":        actor,
		"cleared":      value == "",
	})
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO engine.activity_history(
		    hostname, script_path, script_name, script_type, ran_at, status, stdout, stderr,
		    queue_lane, activity_kind, metadata_json, started_at, updated_at, finished_at
		)
		VALUES ($1, 'Internal/Metadata_Fields', $2, 'metadata_fields', $3, 'Success', $4, '',
		        'metadata_fields', 'metadata_field_update', $5, $3, $3, $3)
	`, nullString(device.Hostname), metadataFieldLabel(fieldNumber)+" Metadata Update", now, metadataFieldLabel(fieldNumber)+" "+map[bool]string{true: "cleared", false: "updated"}[value == ""]+".", string(metadata)); err != nil {
		return nil, 0, err
	}

	definition, err := metadataDefinitionFromTx(ctx, tx, fieldNumber)
	if err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	committed = true

	row := map[string]any{}
	for key, item := range definition {
		row[key] = item
	}
	row["field_number"] = fieldNumber
	row["field_key"] = fieldKey
	row["value"] = value
	row["modified_at"] = now
	row["source"] = "engine"
	row["actor"] = actor
	row["created_at"] = now
	row["updated_at"] = now
	row["label"] = firstText(cleanText(row["description"]), cleanText(row["default_label"]), metadataFieldLabel(fieldNumber))
	row["has_value"] = value != ""
	return map[string]any{"field": row}, http.StatusOK, nil
}

func resolveMetadataDeviceTx(ctx context.Context, tx *sql.Tx, deviceID string) (metadataDeviceRecord, bool, error) {
	var device metadataDeviceRecord
	err := tx.QueryRowContext(
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

func metadataDefinitionFromTx(ctx context.Context, tx *sql.Tx, fieldNumber int) (map[string]any, error) {
	var row metadataDefinitionRow
	err := tx.QueryRowContext(ctx, `
		SELECT field_number, description, updated_at, updated_by
		  FROM engine.metadata_field_definitions
		 WHERE field_number = $1
	`, fieldNumber).Scan(&row.FieldNumber, &row.Description, &row.UpdatedAt, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return metadataDefinitionPayload(fieldNumber, "", 0, ""), nil
	}
	if err != nil {
		return nil, err
	}
	return metadataDefinitionPayload(fieldNumber, nullString(row.Description), nullInt(row.UpdatedAt), nullString(row.UpdatedBy)), nil
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

func normalizeMetadataDescription(value string) string {
	text := strings.TrimSpace(normalizeMetadataValue(value))
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		cleaned := strings.TrimSpace(line)
		if cleaned != "" {
			parts = append(parts, cleaned)
		}
	}
	return truncateMetadataText(strings.Join(parts, " "), metadataValueMaxLength)
}

func encodeMetadataValue(value string) string {
	text := normalizeMetadataValue(value)
	if text == "" {
		return ""
	}
	return base64.StdEncoding.EncodeToString([]byte(text))
}

func truncateMetadataText(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit]
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
		definition := descriptions[fieldNumber]
		fields = append(fields, metadataDefinitionPayload(fieldNumber, nullString(definition.Description), nullInt(definition.UpdatedAt), nullString(definition.UpdatedBy)))
	}
	return fields
}

func metadataDefinitionPayload(fieldNumber int, description string, updatedAt int64, updatedBy string) map[string]any {
	defaultLabel := metadataFieldLabel(fieldNumber)
	cleanDescription := strings.TrimSpace(description)
	if reserved, ok := reservedMetadataFieldDefinition(fieldNumber); ok {
		payload := map[string]any{
			"field_number":     fieldNumber,
			"field_key":        metadataFieldKey(fieldNumber),
			"default_label":    defaultLabel,
			"label":            reserved.Description,
			"description":      reserved.Description,
			"updated_at":       int64(0),
			"updated_by":       "Borealis",
			"value_limit":      metadataValueMaxLength,
			"reserved":         true,
			"reserved_tooltip": reservedMetadataTooltip(reserved),
		}
		if strings.TrimSpace(reserved.AssemblyGUID) != "" {
			payload["linked_assembly"] = map[string]any{
				"guid": reserved.AssemblyGUID,
				"name": reserved.AssemblyName,
				"type": reserved.AssemblyType,
				"path": reservedMetadataAssemblyPath(reserved),
			}
			payload["linked_assembly_guid"] = reserved.AssemblyGUID
			payload["linked_assembly_name"] = reserved.AssemblyName
			payload["linked_assembly_type"] = reserved.AssemblyType
			payload["linked_assembly_path"] = reservedMetadataAssemblyPath(reserved)
		}
		return payload
	}
	label := cleanDescription
	if label == "" {
		label = defaultLabel
	}
	return map[string]any{
		"field_number":  fieldNumber,
		"field_key":     metadataFieldKey(fieldNumber),
		"default_label": defaultLabel,
		"label":         label,
		"description":   cleanDescription,
		"updated_at":    updatedAt,
		"updated_by":    updatedBy,
		"value_limit":   metadataValueMaxLength,
		"reserved":      false,
	}
}

func reservedMetadataFieldDefinition(fieldNumber int) (reservedMetadataField, bool) {
	reserved, ok := reservedMetadataFields[fieldNumber]
	return reserved, ok
}

func reservedMetadataTooltip(reserved reservedMetadataField) string {
	if strings.TrimSpace(reserved.AssemblyGUID) == "" {
		return reservedMetadataPlaceholderTooltip
	}
	return reservedMetadataFieldTooltip
}

func reservedMetadataAssemblyPath(reserved reservedMetadataField) string {
	if strings.TrimSpace(reserved.AssemblyGUID) == "" {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(reserved.AssemblyType)) {
	case "ansible_playbook":
		return "/assemblies/ansible_playbooks/" + url.PathEscape(reserved.AssemblyGUID)
	case "workflow":
		return "/assemblies/workflows/" + url.PathEscape(reserved.AssemblyGUID)
	default:
		return "/assemblies/scripts/" + url.PathEscape(reserved.AssemblyGUID)
	}
}

func reservedMetadataFieldUpdatePayload(fieldNumber int) map[string]any {
	reserved, _ := reservedMetadataFieldDefinition(fieldNumber)
	return map[string]any{
		"error":   "reserved_metadata_field",
		"message": reservedMetadataTooltip(reserved),
		"field":   metadataDefinitionPayload(fieldNumber, reserved.Description, 0, "Borealis"),
	}
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
