package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type agentMetadataFieldStore interface {
	agentMetadataField(ctx context.Context, guid string, fieldNumber int) (map[string]any, int, error)
}

func registerAgentReadRoutes(mux *http.ServeMux, auth *authService, signer *agentJWTSigner, dpop *dpopVerifier) {
	mux.HandleFunc("GET /api/agent/metadata/{field_number}", agentMetadataFieldHandler(auth, signer, dpop))
	mux.HandleFunc("GET /api/agent/software-management/overrides", agentSoftwareManagementOverridesHandler(auth, signer, dpop))
}

func agentMetadataFieldHandler(auth *authService, signer *agentJWTSigner, dpop *dpopVerifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fieldNumber, err := strconv.Atoi(strings.TrimSpace(r.PathValue("field_number")))
		if err != nil || fieldNumber < 1 || fieldNumber > metadataFieldCount {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error":   "invalid_field",
				"message": "Field number must be between 1 and 500.",
			})
			return
		}
		deviceCtx, failure := authenticateDeviceBearer(r.Context(), r, auth, signer, dpop)
		if failure != nil {
			failure.write(w)
			return
		}
		store, ok := auth.store.(agentMetadataFieldStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "agent_metadata_unavailable"})
			return
		}
		timeout := auth.timeout
		if timeout <= 0 {
			timeout = defaultAuthTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		payload, status, err := store.agentMetadataField(ctx, deviceCtx.GUID, fieldNumber)
		if err != nil {
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, status, payload)
	}
}

func agentSoftwareManagementOverridesHandler(auth *authService, signer *agentJWTSigner, dpop *dpopVerifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, failure := authenticateDeviceBearer(r.Context(), r, auth, signer, dpop); failure != nil {
			failure.write(w)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"windows_icon_overrides": loadAgentSoftwareIconOverrides(),
		})
	}
}

func (s *postgresOperatorStore) agentMetadataField(ctx context.Context, guid string, fieldNumber int) (map[string]any, int, error) {
	guid = normalizeCanonicalGUID(guid)
	if guid == "" {
		return nil, http.StatusNotFound, errors.New("device_not_registered")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	defer conn.Close()
	var storedGUID string
	err = conn.QueryRowContext(
		ctx,
		`SELECT guid FROM engine.devices WHERE UPPER(guid)=UPPER($1) LIMIT 1`,
		guid,
	).Scan(&storedGUID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, http.StatusNotFound, errors.New("device_not_registered")
	}
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	values, err := metadataDeviceValues(ctx, conn, storedGUID)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	record := values[fieldNumber]
	decoded := decodeMetadataValue(nullString(record.Value))
	return map[string]any{
		"field": map[string]any{
			"field_number": fieldNumber,
			"field_key":    metadataFieldKey(fieldNumber),
			"label":        metadataFieldLabel(fieldNumber),
			"value":        decoded,
			"modified_at":  nullInt(record.ModifiedAt),
			"source":       nullString(record.Source),
			"actor":        nullString(record.Actor),
			"has_value":    decoded != "",
		},
	}, http.StatusOK, nil
}

func loadAgentSoftwareIconOverrides() []map[string]any {
	path := agentSoftwareIconOverridesPath()
	if strings.TrimSpace(path) == "" {
		return []map[string]any{}
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return []map[string]any{}
	}
	var payload map[string]any
	if err := json.Unmarshal(content, &payload); err != nil {
		return []map[string]any{}
	}
	rows, ok := payload["windows_icon_overrides"].([]any)
	if !ok {
		return []map[string]any{}
	}
	normalized := make([]map[string]any, 0, len(rows))
	for _, item := range rows {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		cleaned := map[string]any{}
		for key, value := range row {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			cleaned[key] = value
		}
		normalized = append(normalized, cleaned)
	}
	return normalized
}

func agentSoftwareIconOverridesPath() string {
	if value := strings.TrimSpace(os.Getenv("BOREALIS_SOFTWARE_ICON_OVERRIDES_PATH")); value != "" {
		return value
	}
	candidates := []string{
		"/opt/Borealis/Data/Engine/services/API/devices/software_icons_overrides.json",
	}
	projectRoot := strings.TrimSpace(os.Getenv("BOREALIS_PROJECT_ROOT"))
	if projectRoot == "" {
		projectRoot = "/opt/Borealis"
	}
	candidates = append(candidates,
		filepath.Join(projectRoot, "Data", "Engine", "services", "API", "devices", "software_icons_overrides.json"),
		filepath.Join(projectRoot, "Data", "Engine", "Containers", "api-backend", "data", "services", "API", "devices", "software_icons_overrides.json"),
	)
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return candidates[0]
}
