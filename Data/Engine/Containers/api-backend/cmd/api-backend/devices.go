package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type deviceListFilter struct {
	ConnectionType string
	Hostname       string
	OnlyAgents     bool
}

type deviceListStore interface {
	listDevices(ctx context.Context, profile operatorProfile, filter deviceListFilter) ([]map[string]any, error)
}

type agentSocketSnapshot struct {
	Hostnames map[string]struct{}
	AgentIDs  map[string]struct{}
	GUIDs     map[string]struct{}
}

type deviceDetailStore interface {
	getDeviceByGUID(ctx context.Context, profile operatorProfile, guid string) (map[string]any, int, error)
	getDeviceDetails(ctx context.Context, profile operatorProfile, hostname string) (map[string]any, int, error)
}

type deviceMutationStore interface {
	setDeviceDescription(ctx context.Context, profile operatorProfile, hostname string, description string) (map[string]any, int, error)
	setAgentReleaseChannelOverride(ctx context.Context, guid string, channel any, branch any) (map[string]any, int, error)
}

type devicePurgeStore interface {
	purgeDevice(ctx context.Context, profile operatorProfile, guid string) (devicePurgeResult, int, error)
}

type deviceRow struct {
	GUID                        sql.NullString
	Hostname                    sql.NullString
	Description                 sql.NullString
	CreatedAt                   sql.NullInt64
	LastEnrollmentAt            sql.NullInt64
	AgentHash                   sql.NullString
	AgentRoleHealth             sql.NullString
	Memory                      sql.NullString
	Network                     sql.NullString
	Software                    sql.NullString
	Services                    sql.NullString
	Storage                     sql.NullString
	CPU                         sql.NullString
	Sessions                    sql.NullString
	Processes                   sql.NullString
	DeviceType                  sql.NullString
	Domain                      sql.NullString
	ExternalIP                  sql.NullString
	InternalIP                  sql.NullString
	LastReboot                  sql.NullString
	LastSeen                    sql.NullInt64
	CPUPercent                  sql.NullFloat64
	MemoryPercent               sql.NullFloat64
	LastUser                    sql.NullString
	OperatingSystem             sql.NullString
	Uptime                      sql.NullInt64
	AgentID                     sql.NullString
	ConnectionType              sql.NullString
	ConnectionEndpoint          sql.NullString
	AgentReleaseChannelOverride sql.NullString
	AgentReleaseChannel         sql.NullString
	AgentBranch                 sql.NullString
	AgentUpdateChannel          sql.NullString
	AgentUpdateTargetBuildID    sql.NullString
	AgentUpdateState            sql.NullString
	AgentUpdateError            sql.NullString
	AgentUpdateSource           sql.NullString
	SiteID                      sql.NullInt64
	SiteName                    sql.NullString
	SiteDescription             sql.NullString
}

func registerDeviceRoutes(mux *http.ServeMux, auth *authService, runtime devicePurgeRuntime, broadcaster watchdogIncidentBroadcaster) {
	mux.HandleFunc("GET /api/agents", agentListHandler(auth))
	mux.HandleFunc("GET /api/devices", deviceListHandler(auth))
	mux.HandleFunc("GET /api/devices/{device_id}/watchdogs", deviceWatchdogsHandler(auth))
	mux.HandleFunc("POST /api/devices/{device_id}/watchdogs/overrides", deviceWatchdogOverrideHandler(auth, broadcaster))
	mux.HandleFunc("GET /api/devices/{guid}", deviceByGUIDHandler(auth))
	mux.HandleFunc("POST /api/devices/{guid}/quarantine", deviceSecurityStatusHandler(auth, runtime, "quarantined"))
	mux.HandleFunc("POST /api/devices/{guid}/unquarantine", deviceSecurityStatusHandler(auth, runtime, "active"))
	mux.HandleFunc("POST /api/devices/{guid}/revoke", deviceSecurityStatusHandler(auth, runtime, "revoked"))
	mux.HandleFunc("POST /api/devices/{guid}/purge", devicePurgeHandler(auth, runtime))
	mux.HandleFunc("PUT /api/devices/{guid}/agent-release-channel", deviceAgentReleaseChannelHandler(auth))
	mux.HandleFunc("GET /api/device/details/{hostname}", deviceDetailsHandler(auth))
	mux.HandleFunc("POST /api/device/description/{hostname}", deviceDescriptionHandler(auth))
}

func agentListHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
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
		store, ok := auth.store.(deviceListStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "agent_list_unavailable"})
			return
		}

		timeout := auth.timeout
		if timeout <= 0 {
			timeout = defaultAuthTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		devices, err := store.listDevices(ctx, profile, deviceListFilter{OnlyAgents: true})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, buildAgentListPayload(devices, time.Now().Unix()))
	}
}

func deviceListHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
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
		store, ok := auth.store.(deviceListStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "device_list_unavailable"})
			return
		}

		timeout := auth.timeout
		if timeout <= 0 {
			timeout = defaultAuthTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		filter := deviceListFilter{
			ConnectionType: cleanText(r.URL.Query().Get("connection_type")),
			Hostname:       cleanText(r.URL.Query().Get("hostname")),
			OnlyAgents:     parseTruthy(r.URL.Query().Get("only_agents")),
		}
		devices, err := store.listDevices(ctx, profile, filter)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		enrichDeviceAgentSocketState(ctx, auth, devices)
		writeJSON(w, http.StatusOK, map[string]any{"devices": devices})
	}
}

func enrichDeviceAgentSocketState(ctx context.Context, auth *authService, devices []map[string]any) {
	if auth == nil || len(devices) == 0 {
		return
	}
	store, ok := auth.store.(*postgresOperatorStore)
	if !ok || store == nil || store.db == nil {
		return
	}
	siteIDs := make(map[int64]struct{})
	for _, device := range devices {
		siteID := coerceInt64(device["site_id"])
		if siteID > 0 {
			siteIDs[siteID] = struct{}{}
		}
	}
	if len(siteIDs) == 0 {
		return
	}
	conn, err := store.db.Conn(ctx)
	if err != nil {
		return
	}

	routes := make(map[int64]*agentWorkerRoute, len(siteIDs))
	for siteID := range siteIDs {
		route, err := fetchAgentWorkerRoute(ctx, conn, siteID)
		if err != nil || route == nil {
			continue
		}
		routes[siteID] = route
	}
	_ = conn.Close()

	snapshots := make(map[int64]agentSocketSnapshot, len(routes))
	for siteID, route := range routes {
		snapshots[siteID] = fetchWorkerAgentSocketSnapshot(ctx, auth, route)
	}

	for _, device := range devices {
		siteID := coerceInt64(device["site_id"])
		snapshot, ok := snapshots[siteID]
		registered := ok && snapshot.deviceRegistered(device)
		device["agent_socket"] = registered
		if summary, ok := device["summary"].(map[string]any); ok {
			summary["agent_socket"] = registered
		}
		if details, ok := device["details"].(map[string]any); ok {
			if detailSummary, ok := details["summary"].(map[string]any); ok {
				detailSummary["agent_socket"] = registered
			}
		}
	}
}

func fetchWorkerAgentSocketSnapshot(ctx context.Context, auth *authService, route *agentWorkerRoute) agentSocketSnapshot {
	snapshot := agentSocketSnapshot{
		Hostnames: map[string]struct{}{},
		AgentIDs:  map[string]struct{}{},
		GUIDs:     map[string]struct{}{},
	}
	if auth == nil || route == nil {
		return snapshot
	}
	target := workerInternalURL(route, "/agents")
	if target == "" {
		return snapshot
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return snapshot
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set(internalTokenHeader, goInternalToken(auth.verifier.secret))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return snapshot
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return snapshot
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return snapshot
	}
	agents, _ := payload["agents"].(map[string]any)
	for _, raw := range agents {
		agent, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if mode := strings.ToLower(cleanText(agent["service_mode"])); mode != "" && mode != "system" {
			continue
		}
		if hostname := strings.ToLower(cleanText(agent["hostname"])); hostname != "" {
			snapshot.Hostnames[hostname] = struct{}{}
		}
		if agentID := strings.ToLower(cleanText(agent["agent_id"])); agentID != "" {
			snapshot.AgentIDs[agentID] = struct{}{}
		}
		if guid := normalizeGUID(cleanText(agent["guid"])); guid != "" {
			snapshot.GUIDs[guid] = struct{}{}
		}
	}
	return snapshot
}

func (s agentSocketSnapshot) deviceRegistered(device map[string]any) bool {
	if len(s.Hostnames) == 0 && len(s.AgentIDs) == 0 && len(s.GUIDs) == 0 {
		return false
	}
	if hostname := strings.ToLower(cleanText(device["hostname"])); hostname != "" {
		if _, ok := s.Hostnames[hostname]; ok {
			return true
		}
	}
	if agentID := strings.ToLower(cleanText(device["agent_id"])); agentID != "" {
		if _, ok := s.AgentIDs[agentID]; ok {
			return true
		}
	}
	guidCandidates := []any{device["agent_guid"], device["guid"]}
	if summary, ok := device["summary"].(map[string]any); ok {
		guidCandidates = append(guidCandidates, summary["agent_guid"], summary["guid"])
	}
	for _, candidate := range guidCandidates {
		if guid := normalizeGUID(cleanText(candidate)); guid != "" {
			if _, ok := s.GUIDs[guid]; ok {
				return true
			}
		}
	}
	return false
}

func deviceByGUIDHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		guid := normalizeCanonicalGUID(r.PathValue("guid"))
		if guid == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid guid"})
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
		store, ok := auth.store.(deviceDetailStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "device_detail_unavailable"})
			return
		}

		timeout := auth.timeout
		if timeout <= 0 {
			timeout = defaultAuthTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		payload, status, err := store.getDeviceByGUID(ctx, profile, guid)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, status, payload)
	}
}

func deviceDetailsHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		hostname := cleanText(r.PathValue("hostname"))
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
		store, ok := auth.store.(deviceDetailStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "device_detail_unavailable"})
			return
		}

		timeout := auth.timeout
		if timeout <= 0 {
			timeout = defaultAuthTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		payload, status, err := store.getDeviceDetails(ctx, profile, hostname)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, status, payload)
	}
}

func deviceDescriptionHandler(auth *authService) http.HandlerFunc {
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
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
			return
		}
		store, ok := auth.store.(deviceMutationStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "device_mutation_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		payload, status, err := store.setDeviceDescription(ctx, profile, cleanText(r.PathValue("hostname")), cleanText(body["description"]))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, status, payload)
	}
}

func deviceAgentReleaseChannelHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
			return
		}
		store, ok := auth.store.(deviceMutationStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "device_mutation_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		payload, status, err := store.setAgentReleaseChannelOverride(ctx, normalizeCanonicalGUID(r.PathValue("guid")), body["channel"], body["branch"])
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, status, payload)
	}
}

func (s *postgresOperatorStore) listDevices(ctx context.Context, profile operatorProfile, filter deviceListFilter) ([]map[string]any, error) {
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, err
	}
	if allowedSiteIDs != nil && len(allowedSiteIDs) == 0 {
		return []map[string]any{}, nil
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	sqlText := `
		SELECT
			d.guid,
			d.hostname,
			d.description,
			d.created_at,
			d.last_enrollment_at,
			d.agent_hash,
			d.agent_role_health,
			d.memory,
			d.network,
			d.software,
			d.services,
			d.storage,
			d.cpu,
			d.sessions,
			d.processes,
			d.device_type,
			d.domain,
			d.external_ip,
			d.internal_ip,
			d.last_reboot,
			d.last_seen,
			d.cpu_percent,
			d.memory_percent,
			d.last_user,
			d.operating_system,
			d.uptime,
			d.agent_id,
			d.connection_type,
			d.connection_endpoint,
			d.agent_release_channel_override,
			d.agent_release_channel,
			d.agent_branch,
			d.agent_update_channel,
			d.agent_update_target_build_id,
			d.agent_update_state,
			d.agent_update_error,
			d.agent_update_source,
			s.id,
			s.name,
			s.description
		  FROM engine.devices AS d
	 LEFT JOIN engine.device_sites AS ds ON ds.device_hostname = d.hostname
	 LEFT JOIN engine.sites AS s ON s.id = ds.site_id
	`
	clauses := make([]string, 0, 4)
	params := make([]any, 0, 4)
	if filter.ConnectionType != "" {
		params = append(params, filter.ConnectionType)
		clauses = append(clauses, "LOWER(d.connection_type) = LOWER($"+strconv.Itoa(len(params))+")")
	}
	if filter.Hostname != "" {
		params = append(params, strings.ToLower(filter.Hostname))
		clauses = append(clauses, "LOWER(d.hostname) = LOWER($"+strconv.Itoa(len(params))+")")
	}
	if filter.OnlyAgents {
		clauses = append(clauses, "(d.connection_type IS NULL OR TRIM(d.connection_type) = '')")
	}
	if allowedSiteIDs != nil {
		placeholders := make([]string, 0, len(allowedSiteIDs))
		for _, siteID := range allowedSiteIDs {
			params = append(params, siteID)
			placeholders = append(placeholders, "$"+strconv.Itoa(len(params)))
		}
		clauses = append(clauses, "ds.site_id IN ("+strings.Join(placeholders, ",")+")")
	}
	if len(clauses) > 0 {
		sqlText += " WHERE " + strings.Join(clauses, " AND ")
	}

	rows, err := conn.QueryContext(ctx, sqlText, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rawRows := make([]deviceRow, 0)
	for rows.Next() {
		var row deviceRow
		if err := rows.Scan(
			&row.GUID,
			&row.Hostname,
			&row.Description,
			&row.CreatedAt,
			&row.LastEnrollmentAt,
			&row.AgentHash,
			&row.AgentRoleHealth,
			&row.Memory,
			&row.Network,
			&row.Software,
			&row.Services,
			&row.Storage,
			&row.CPU,
			&row.Sessions,
			&row.Processes,
			&row.DeviceType,
			&row.Domain,
			&row.ExternalIP,
			&row.InternalIP,
			&row.LastReboot,
			&row.LastSeen,
			&row.CPUPercent,
			&row.MemoryPercent,
			&row.LastUser,
			&row.OperatingSystem,
			&row.Uptime,
			&row.AgentID,
			&row.ConnectionType,
			&row.ConnectionEndpoint,
			&row.AgentReleaseChannelOverride,
			&row.AgentReleaseChannel,
			&row.AgentBranch,
			&row.AgentUpdateChannel,
			&row.AgentUpdateTargetBuildID,
			&row.AgentUpdateState,
			&row.AgentUpdateError,
			&row.AgentUpdateSource,
			&row.SiteID,
			&row.SiteName,
			&row.SiteDescription,
		); err != nil {
			return nil, err
		}
		rawRows = append(rawRows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	now := time.Now().Unix()
	guidCandidates := make([]string, 0, len(rawRows)*2)
	for _, row := range rawRows {
		rawGUID := nullString(row.GUID)
		if rawGUID == "" {
			continue
		}
		guidCandidates = append(guidCandidates, rawGUID)
		if normalized := normalizeCanonicalGUID(rawGUID); normalized != "" {
			guidCandidates = append(guidCandidates, normalized)
		}
	}
	metadataByGUID, _ := loadFilterMetadataByGUID(ctx, conn, guidCandidates)
	devices := make([]map[string]any, 0, len(rawRows))
	for _, row := range rawRows {
		payload := buildDevicePayload(row, now)
		attachDeviceListMetadataFields(payload, deviceListMetadataPayload(metadataByGUID, row))
		devices = append(devices, payload)
	}
	return devices, nil
}

func deviceListMetadataPayload(metadataByGUID map[string]map[string]string, row deviceRow) map[string]any {
	fields := map[string]any{}
	if len(metadataByGUID) == 0 {
		return fields
	}
	rawGUID := nullString(row.GUID)
	for _, candidate := range []string{
		rawGUID,
		normalizeCanonicalGUID(rawGUID),
		normalizeGUID(rawGUID),
		strings.ToLower(normalizeCanonicalGUID(rawGUID)),
	} {
		values, ok := metadataByGUID[cleanText(candidate)]
		if !ok {
			continue
		}
		for key, value := range values {
			fields[key] = value
		}
	}
	return fields
}

func attachDeviceListMetadataFields(payload map[string]any, metadataFields map[string]any) {
	if metadataFields == nil {
		metadataFields = map[string]any{}
	}
	payload["metadata_fields"] = metadataFields
	if summary, ok := payload["summary"].(map[string]any); ok {
		summary["metadata_fields"] = metadataFields
	}
	if details, ok := payload["details"].(map[string]any); ok {
		details["metadata_fields"] = metadataFields
	}
}

func (s *postgresOperatorStore) setDeviceDescription(ctx context.Context, profile operatorProfile, hostname string, description string) (map[string]any, int, error) {
	if hostname == "" {
		return map[string]any{"error": "not found"}, http.StatusNotFound, nil
	}
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, 0, err
	}
	if allowedSiteIDs != nil && len(allowedSiteIDs) == 0 {
		return map[string]any{"error": "not found"}, http.StatusNotFound, nil
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
	defer rollbackQuietly(tx)

	sqlText := "UPDATE engine.devices SET description = $1 WHERE hostname = $2"
	params := []any{description, hostname}
	if allowedSiteIDs != nil {
		placeholders := make([]string, 0, len(allowedSiteIDs))
		for _, siteID := range allowedSiteIDs {
			params = append(params, siteID)
			placeholders = append(placeholders, "$"+strconv.Itoa(len(params)))
		}
		sqlText += " AND EXISTS (SELECT 1 FROM engine.device_sites ds WHERE ds.device_hostname = engine.devices.hostname AND ds.site_id IN (" + strings.Join(placeholders, ",") + "))"
	}
	result, err := tx.ExecContext(ctx, sqlText, params...)
	if err != nil {
		return nil, 0, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return map[string]any{"error": "not found"}, http.StatusNotFound, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return map[string]any{"status": "ok"}, http.StatusOK, nil
}

func (s *postgresOperatorStore) setAgentReleaseChannelOverride(ctx context.Context, guid string, channel any, branch any) (map[string]any, int, error) {
	normalizedGUID := normalizeCanonicalGUID(guid)
	if normalizedGUID == "" {
		return map[string]any{"error": "invalid_guid"}, http.StatusBadRequest, nil
	}
	rawOverride := strings.ToLower(cleanText(channel))
	cleanedOverride := rawOverride
	switch rawOverride {
	case "release", "releases":
		cleanedOverride = "stable"
	case "source", "branch", "repo", "repository":
		cleanedOverride = "unstable"
	}
	if cleanedOverride != "" && cleanedOverride != "stable" && cleanedOverride != "unstable" {
		return map[string]any{"error": "invalid_channel"}, http.StatusBadRequest, nil
	}
	branchSupplied := branch != nil
	suppliedBranch := ""
	if branchSupplied {
		suppliedBranch = normalizeAgentBranch(branch)
		if suppliedBranch == "" {
			return map[string]any{"error": "invalid_branch"}, http.StatusBadRequest, nil
		}
	}

	targetBranch := ""
	targetBuildID := ""
	targetPublishedAt := ""
	effectiveChannel := ""
	releaseChannel := ""
	if suppliedBranch != "" {
		cleanedOverride = "unstable"
		effectiveChannel = "unstable"
		releaseChannel = "unstable"
		if rawOverride == "source" || rawOverride == "branch" || rawOverride == "repo" || rawOverride == "repository" {
			releaseChannel = "source"
		}
		targetBranch = suppliedBranch
	} else {
		effectiveChannel, targetBuildID, targetPublishedAt = resolveAgentTarget(cleanedOverride)
		settings := collectAgentReleaseChannelSettings()
		if channels, ok := settings["channels"].(map[string]any); ok {
			if target, ok := channels[effectiveChannel].(map[string]any); ok {
				targetBranch = normalizeAgentBranch(target["branch"])
			}
		}
		if strings.EqualFold(effectiveChannel, "unstable") {
			releaseChannel = "unstable"
		} else {
			releaseChannel = "stable"
		}
	}
	var storedOverride any
	if cleanedOverride != "" {
		storedOverride = cleanedOverride
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
	defer rollbackQuietly(tx)
	result, err := tx.ExecContext(
		ctx,
		`
		UPDATE engine.devices
		   SET agent_release_channel_override = $1,
		       agent_release_channel = $2,
		       agent_branch = $3
		 WHERE UPPER(guid) = $4
		`,
		storedOverride,
		releaseChannel,
		targetBranch,
		normalizedGUID,
	)
	if err != nil {
		return nil, 0, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return map[string]any{"error": "not found"}, http.StatusNotFound, nil
	}
	var hostname sql.NullString
	if err := tx.QueryRowContext(ctx, "SELECT hostname FROM engine.devices WHERE UPPER(guid) = $1 LIMIT 1", normalizedGUID).Scan(&hostname); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return map[string]any{
		"status":                          "ok",
		"guid":                            normalizedGUID,
		"hostname":                        nullString(hostname),
		"agent_release_channel_override":  storedOverride,
		"agent_release_channel_effective": effectiveChannel,
		"agent_release_channel":           releaseChannel,
		"agent_branch":                    targetBranch,
		"agent_target_build_id":           targetBuildID,
		"agent_target_published_at":       targetPublishedAt,
	}, http.StatusOK, nil
}

func (s *postgresOperatorStore) getDeviceByGUID(ctx context.Context, profile operatorProfile, guid string) (map[string]any, int, error) {
	row, found, err := s.lookupDeviceDetail(ctx, profile, "LOWER(d.guid) = LOWER($1)", guid)
	if err != nil {
		return nil, 0, err
	}
	if !found {
		return map[string]any{"error": "not found"}, http.StatusNotFound, nil
	}
	return attachAgentVersionStatus(buildDevicePayload(row, time.Now().Unix())), http.StatusOK, nil
}

func (s *postgresOperatorStore) getDeviceDetails(ctx context.Context, profile operatorProfile, hostname string) (map[string]any, int, error) {
	row, found, err := s.lookupDeviceDetail(ctx, profile, "d.hostname = $1", hostname)
	if err != nil {
		return nil, 0, err
	}
	if !found {
		return map[string]any{}, http.StatusOK, nil
	}
	return attachAgentVersionStatus(buildDevicePayload(row, time.Now().Unix())), http.StatusOK, nil
}

func (s *postgresOperatorStore) lookupDeviceDetail(ctx context.Context, profile operatorProfile, predicate string, value string) (deviceRow, bool, error) {
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return deviceRow{}, false, err
	}
	if allowedSiteIDs != nil && len(allowedSiteIDs) == 0 {
		return deviceRow{}, false, nil
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return deviceRow{}, false, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	sqlText := `
		SELECT
			d.guid,
			d.hostname,
			d.description,
			d.created_at,
			d.last_enrollment_at,
			d.agent_hash,
			d.agent_role_health,
			d.memory,
			d.network,
			d.software,
			d.services,
			d.storage,
			d.cpu,
			d.sessions,
			d.processes,
			d.device_type,
			d.domain,
			d.external_ip,
			d.internal_ip,
			d.last_reboot,
			d.last_seen,
			d.cpu_percent,
			d.memory_percent,
			d.last_user,
			d.operating_system,
			d.uptime,
			d.agent_id,
			d.connection_type,
			d.connection_endpoint,
			d.agent_release_channel_override,
			d.agent_release_channel,
			d.agent_branch,
			d.agent_update_channel,
			d.agent_update_target_build_id,
			d.agent_update_state,
			d.agent_update_error,
			d.agent_update_source,
			s.id,
			s.name,
			s.description
		  FROM engine.devices AS d
	 LEFT JOIN engine.device_sites AS ds ON ds.device_hostname = d.hostname
	 LEFT JOIN engine.sites AS s ON s.id = ds.site_id
		 WHERE ` + predicate
	params := []any{value}
	if allowedSiteIDs != nil {
		placeholders := make([]string, 0, len(allowedSiteIDs))
		for _, siteID := range allowedSiteIDs {
			params = append(params, siteID)
			placeholders = append(placeholders, "$"+strconv.Itoa(len(params)))
		}
		sqlText += " AND ds.site_id IN (" + strings.Join(placeholders, ",") + ")"
	}
	sqlText += " LIMIT 1"

	var row deviceRow
	err = conn.QueryRowContext(ctx, sqlText, params...).Scan(
		&row.GUID,
		&row.Hostname,
		&row.Description,
		&row.CreatedAt,
		&row.LastEnrollmentAt,
		&row.AgentHash,
		&row.AgentRoleHealth,
		&row.Memory,
		&row.Network,
		&row.Software,
		&row.Services,
		&row.Storage,
		&row.CPU,
		&row.Sessions,
		&row.Processes,
		&row.DeviceType,
		&row.Domain,
		&row.ExternalIP,
		&row.InternalIP,
		&row.LastReboot,
		&row.LastSeen,
		&row.CPUPercent,
		&row.MemoryPercent,
		&row.LastUser,
		&row.OperatingSystem,
		&row.Uptime,
		&row.AgentID,
		&row.ConnectionType,
		&row.ConnectionEndpoint,
		&row.AgentReleaseChannelOverride,
		&row.AgentReleaseChannel,
		&row.AgentBranch,
		&row.AgentUpdateChannel,
		&row.AgentUpdateTargetBuildID,
		&row.AgentUpdateState,
		&row.AgentUpdateError,
		&row.AgentUpdateSource,
		&row.SiteID,
		&row.SiteName,
		&row.SiteDescription,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return deviceRow{}, false, nil
	}
	if err != nil {
		return deviceRow{}, false, err
	}
	return row, true, nil
}

func buildDevicePayload(row deviceRow, now int64) map[string]any {
	createdAt := nullInt(row.CreatedAt)
	lastEnrollmentAt := nullInt(row.LastEnrollmentAt)
	if lastEnrollmentAt == 0 {
		lastEnrollmentAt = createdAt
	}
	lastSeen := nullInt(row.LastSeen)
	agentHash := nullString(row.AgentHash)
	agentGUID := normalizeGUID(nullString(row.GUID))
	roleHealth := normalizeAgentRoleHealthPayload(row.AgentRoleHealth, now)
	services, servicesReportedAt := normalizeInventoryPayload(row.Services, "services")
	sessions, sessionsReportedAt := normalizeInventoryPayload(row.Sessions, "sessions")
	processes, processesReportedAt := normalizeInventoryPayload(row.Processes, "processes")

	summary := map[string]any{
		"hostname":                       nullString(row.Hostname),
		"description":                    nullString(row.Description),
		"agent_hash":                     agentHash,
		"agent_build_id":                 agentHash,
		"agent_role_health":              roleHealth,
		"agent_guid":                     agentGUID,
		"agent_id":                       nullString(row.AgentID),
		"device_type":                    nullString(row.DeviceType),
		"domain":                         nullString(row.Domain),
		"external_ip":                    nullString(row.ExternalIP),
		"internal_ip":                    nullString(row.InternalIP),
		"last_reboot":                    nullString(row.LastReboot),
		"last_seen":                      lastSeen,
		"cpu_percent":                    nullFloat(row.CPUPercent),
		"memory_percent":                 nullFloat(row.MemoryPercent),
		"last_user":                      nullString(row.LastUser),
		"operating_system":               nullString(row.OperatingSystem),
		"uptime":                         nullInt(row.Uptime),
		"created_at":                     createdAt,
		"last_enrollment_at":             lastEnrollmentAt,
		"connection_type":                nullString(row.ConnectionType),
		"connection_endpoint":            nullString(row.ConnectionEndpoint),
		"agent_release_channel_override": nullString(row.AgentReleaseChannelOverride),
		"agent_release_channel":          nullString(row.AgentReleaseChannel),
		"agent_branch":                   nullString(row.AgentBranch),
		"agent_update_channel":           nullString(row.AgentUpdateChannel),
		"agent_update_target_build_id":   nullString(row.AgentUpdateTargetBuildID),
		"agent_update_state":             nullString(row.AgentUpdateState),
		"agent_update_error":             nullString(row.AgentUpdateError),
		"agent_update_source":            nullString(row.AgentUpdateSource),
	}
	details := map[string]any{
		"summary":   summary,
		"memory":    parseJSONArray(row.Memory),
		"network":   parseJSONArray(row.Network),
		"software":  parseJSONArray(row.Software),
		"services":  services,
		"storage":   parseJSONArray(row.Storage),
		"cpu":       parseJSONObject(row.CPU),
		"sessions":  sessions,
		"processes": processes,
	}

	siteID := any(nil)
	if row.SiteID.Valid {
		siteID = row.SiteID.Int64
	}
	payload := map[string]any{
		"hostname":                       summary["hostname"],
		"description":                    summary["description"],
		"details":                        details,
		"summary":                        summary,
		"created_at":                     createdAt,
		"created_at_iso":                 unixISO(createdAt),
		"last_enrollment_at":             lastEnrollmentAt,
		"last_enrollment_at_iso":         unixISO(lastEnrollmentAt),
		"agent_hash":                     agentHash,
		"agent_build_id":                 agentHash,
		"agent_role_health":              roleHealth,
		"agent_guid":                     agentGUID,
		"guid":                           agentGUID,
		"memory":                         details["memory"],
		"network":                        details["network"],
		"software":                       details["software"],
		"services":                       services,
		"services_reported_at":           servicesReportedAt,
		"storage":                        details["storage"],
		"cpu":                            details["cpu"],
		"sessions":                       sessions,
		"sessions_reported_at":           sessionsReportedAt,
		"processes":                      processes,
		"processes_reported_at":          processesReportedAt,
		"device_type":                    summary["device_type"],
		"domain":                         summary["domain"],
		"external_ip":                    summary["external_ip"],
		"internal_ip":                    summary["internal_ip"],
		"last_reboot":                    summary["last_reboot"],
		"last_seen":                      lastSeen,
		"last_seen_iso":                  unixISO(lastSeen),
		"cpu_percent":                    summary["cpu_percent"],
		"memory_percent":                 summary["memory_percent"],
		"last_user":                      summary["last_user"],
		"operating_system":               summary["operating_system"],
		"uptime":                         summary["uptime"],
		"agent_id":                       summary["agent_id"],
		"connection_type":                summary["connection_type"],
		"connection_endpoint":            summary["connection_endpoint"],
		"agent_release_channel_override": summary["agent_release_channel_override"],
		"agent_release_channel":          summary["agent_release_channel"],
		"agent_branch":                   summary["agent_branch"],
		"agent_update_channel":           summary["agent_update_channel"],
		"agent_update_target_build_id":   summary["agent_update_target_build_id"],
		"agent_update_state":             summary["agent_update_state"],
		"agent_update_error":             summary["agent_update_error"],
		"agent_update_source":            summary["agent_update_source"],
		"site_id":                        siteID,
		"site_name":                      nullString(row.SiteName),
		"site_description":               nullString(row.SiteDescription),
		"status":                         statusFromLastSeen(lastSeen, now),
	}
	return payload
}

func attachAgentVersionStatus(payload map[string]any) map[string]any {
	summary, _ := payload["summary"].(map[string]any)
	channelOverride := cleanText(payload["agent_release_channel_override"])
	if channelOverride == "" {
		channelOverride = cleanText(summary["agent_release_channel_override"])
	}
	effectiveChannel, targetBuildID, targetPublishedAt := resolveAgentTarget(channelOverride)
	installedBuildID := firstText(
		cleanText(payload["agent_build_id"]),
		cleanText(payload["agent_hash"]),
		cleanText(summary["agent_build_id"]),
		cleanText(summary["agent_hash"]),
	)
	status := "Needs Updated"
	if installedBuildID != "" && targetBuildID != "" && strings.EqualFold(installedBuildID, targetBuildID) {
		status = "Up-to-Date"
	}
	releaseChannel := firstText(cleanText(payload["agent_release_channel"]), cleanText(summary["agent_release_channel"]))
	branch := firstText(cleanText(payload["agent_branch"]), cleanText(summary["agent_branch"]))

	payload["agent_version_status"] = status
	payload["agent_target_build_id"] = targetBuildID
	payload["agent_target_published_at"] = targetPublishedAt
	if channelOverride == "" {
		payload["agent_release_channel_override"] = nil
	} else {
		payload["agent_release_channel_override"] = channelOverride
	}
	payload["agent_release_channel"] = releaseChannel
	payload["agent_branch"] = branch
	payload["agent_release_channel_effective"] = effectiveChannel
	if installedBuildID != "" {
		payload["agent_build_id"] = installedBuildID
	}

	applyAgentVersionSummary(summary, status, targetBuildID, targetPublishedAt, payload, installedBuildID)
	if details, ok := payload["details"].(map[string]any); ok {
		if detailSummary, ok := details["summary"].(map[string]any); ok {
			applyAgentVersionSummary(detailSummary, status, targetBuildID, targetPublishedAt, payload, installedBuildID)
		}
	}
	return payload
}

func applyAgentVersionSummary(summary map[string]any, status string, targetBuildID string, targetPublishedAt string, payload map[string]any, installedBuildID string) {
	if summary == nil {
		return
	}
	summary["agent_version_status"] = status
	summary["agent_target_build_id"] = targetBuildID
	summary["agent_target_published_at"] = targetPublishedAt
	summary["agent_release_channel_override"] = payload["agent_release_channel_override"]
	summary["agent_release_channel"] = payload["agent_release_channel"]
	summary["agent_branch"] = payload["agent_branch"]
	if cleanText(payload["agent_release_channel_effective"]) != "" {
		summary["agent_release_channel_effective"] = payload["agent_release_channel_effective"]
	}
	if installedBuildID != "" && cleanText(summary["agent_build_id"]) == "" {
		summary["agent_build_id"] = installedBuildID
	}
}

func resolveAgentTarget(channelOverride string) (string, string, string) {
	settings := collectAgentReleaseChannelSettings()
	effectiveChannel := normalizeAgentReleaseChannel(channelOverride, "")
	if effectiveChannel == "" {
		effectiveChannel = normalizeAgentReleaseChannel(settings["default_channel"], defaultAgentReleaseChannel)
	}
	channels, _ := settings["channels"].(map[string]any)
	target, _ := channels[effectiveChannel].(map[string]any)
	return effectiveChannel, strings.ToLower(cleanText(target["build_id"])), cleanText(target["published_at"])
}

func buildAgentListPayload(devices []map[string]any, now int64) map[string]any {
	grouped := map[string]map[string]map[string]any{}
	for _, record := range devices {
		hostname := cleanText(record["hostname"])
		if hostname == "" {
			hostname = "unknown"
		}
		mode := normalizeServiceMode(record["service_mode"], cleanText(record["agent_id"]))
		agentID := cleanText(record["agent_id"])
		if mode != "currentuser" && strings.HasSuffix(strings.ToLower(agentID), "-script") {
			continue
		}
		lastSeen := coerceInt64(record["last_seen"])
		collectorActive := lastSeen > 0 && now-lastSeen < 130
		status := cleanText(record["status"])
		if status == "" {
			if collectorActive {
				status = "Online"
			} else {
				status = "Offline"
			}
		}
		payload := map[string]any{
			"hostname":            hostname,
			"agent_hostname":      hostname,
			"service_mode":        mode,
			"collector_active":    collectorActive,
			"collector_active_ts": lastSeen,
			"last_seen":           lastSeen,
			"status":              status,
			"agent_id":            agentID,
			"agent_guid":          normalizeGUID(record["agent_guid"]),
			"agent_hash":          cleanText(record["agent_hash"]),
			"connection_type":     cleanText(record["connection_type"]),
			"connection_endpoint": cleanText(record["connection_endpoint"]),
			"device_type":         cleanText(record["device_type"]),
			"domain":              cleanText(record["domain"]),
			"external_ip":         cleanText(record["external_ip"]),
			"internal_ip":         cleanText(record["internal_ip"]),
			"last_reboot":         cleanText(record["last_reboot"]),
			"last_user":           cleanText(record["last_user"]),
			"operating_system":    cleanText(record["operating_system"]),
			"uptime":              coerceInt64(record["uptime"]),
			"site_id":             record["site_id"],
			"site_name":           cleanText(record["site_name"]),
			"site_description":    cleanText(record["site_description"]),
			"helper_contexts":     []any{},
		}
		bucket := grouped[hostname]
		if bucket == nil {
			bucket = map[string]map[string]any{}
			grouped[hostname] = bucket
		}
		existing := bucket[mode]
		if existing == nil || lastSeen >= coerceInt64(existing["last_seen"]) {
			bucket[mode] = payload
		}
	}

	agents := map[string]any{}
	for _, bucket := range grouped {
		for _, payload := range bucket {
			agentKey := cleanText(payload["agent_id"])
			if agentKey == "" {
				agentKey = cleanText(payload["agent_guid"])
			}
			if agentKey == "" {
				agentKey = cleanText(payload["hostname"]) + "|" + cleanText(payload["service_mode"])
			}
			if cleanText(payload["agent_id"]) == "" {
				payload["agent_id"] = agentKey
			}
			agents[agentKey] = payload
		}
	}
	return agents
}

func normalizeServiceMode(value any, agentID string) string {
	text := strings.ToLower(strings.TrimSpace(cleanText(value)))
	if text == "" && agentID != "" {
		lowered := strings.ToLower(agentID)
		if strings.Contains(lowered, "-svc-") || strings.HasSuffix(lowered, "-svc") {
			return "system"
		}
	}
	switch text {
	case "system", "svc", "service", "system_service":
		return "system"
	case "interactive", "currentuser", "user", "current_user":
		return "currentuser"
	default:
		return "currentuser"
	}
}

func normalizeAgentBranch(value any) string {
	text := cleanText(value)
	if text == "" || len(text) > 160 {
		return ""
	}
	for _, ch := range text {
		if ch < 32 || unicode.IsSpace(ch) {
			return ""
		}
		switch {
		case ch >= 'A' && ch <= 'Z':
		case ch >= 'a' && ch <= 'z':
		case ch >= '0' && ch <= '9':
		case ch == '.', ch == '_', ch == '/', ch == '-':
		default:
			return ""
		}
	}
	if strings.HasPrefix(text, "/") || strings.HasPrefix(text, ".") || strings.HasSuffix(text, "/") || strings.HasSuffix(text, ".") {
		return ""
	}
	if strings.Contains(text, "..") || strings.Contains(text, "//") || strings.Contains(text, "@{") || strings.Contains(text, "\\") || strings.Contains(text, ":") {
		return ""
	}
	return text
}

func normalizeCanonicalGUID(value any) string {
	normalized := normalizeGUID(value)
	if len(normalized) != 36 {
		return ""
	}
	for index, ch := range normalized {
		switch index {
		case 8, 13, 18, 23:
			if ch != '-' {
				return ""
			}
		default:
			if !isHexRune(ch) {
				return ""
			}
		}
	}
	return normalized
}

func normalizeInventoryPayload(raw sql.NullString, listKey string) ([]any, int64) {
	value := parseJSON(raw)
	switch typed := value.(type) {
	case []any:
		return typed, 0
	case map[string]any:
		items, _ := typed[listKey].([]any)
		return items, coerceInt64(typed["reported_at"])
	default:
		return []any{}, 0
	}
}

func normalizeAgentRoleHealthPayload(raw sql.NullString, now int64) map[string]any {
	value := parseJSON(raw)
	payload := map[string]any{
		"roles":               []any{},
		"reported_at":         int64(0),
		"supervisor_revision": int64(0),
	}
	var roles []any
	switch typed := value.(type) {
	case []any:
		roles = typed
	case map[string]any:
		if items, ok := typed["roles"].([]any); ok {
			roles = items
		}
		payload["reported_at"] = coerceInt64(typed["reported_at"])
		payload["supervisor_revision"] = coerceInt64(typed["supervisor_revision"])
	default:
		roles = []any{}
	}
	normalizedRoles := make([]any, 0, len(roles))
	for _, item := range roles {
		roleMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		normalizedRoles = append(normalizedRoles, markRoleStale(roleMap, now))
	}
	payload["roles"] = normalizedRoles
	return payload
}

func markRoleStale(role map[string]any, now int64) map[string]any {
	statusCode := strings.ToLower(strings.TrimSpace(cleanText(role["status_code"])))
	if statusCode == "unsupported" || statusCode == "not_applicable" || statusCode == "stale" {
		return role
	}
	checkedAt := coerceInt64(firstNonEmpty(role["last_checked_at"], role["checked_at"]))
	if checkedAt <= 0 || now-checkedAt <= 90 {
		return role
	}
	stale := make(map[string]any, len(role)+4)
	for key, value := range role {
		stale[key] = value
	}
	details := map[string]any{}
	if existing, ok := stale["details"].(map[string]any); ok {
		for key, value := range existing {
			details[key] = value
		}
	}
	age := now - checkedAt
	details["stale_age_seconds"] = strconv.FormatInt(age, 10)
	details["previous_status_code"] = firstText(statusCode, "unknown")
	stale["details"] = details
	stale["status_code"] = "stale"
	stale["status"] = "Stale"
	stale["observed_state"] = "stale"
	stale["last_error"] = "Role health stale for " + strconv.FormatInt(age, 10) + " seconds."
	if cleanText(stale["detail"]) == "" {
		stale["detail"] = stale["last_error"]
	}
	return stale
}

func parseJSON(raw sql.NullString) any {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return nil
	}
	var value any
	if err := json.Unmarshal([]byte(raw.String), &value); err != nil {
		return nil
	}
	return value
}

func parseJSONArray(raw sql.NullString) []any {
	if items, ok := parseJSON(raw).([]any); ok {
		return items
	}
	return []any{}
}

func parseJSONObject(raw sql.NullString) map[string]any {
	if item, ok := parseJSON(raw).(map[string]any); ok {
		return item
	}
	return map[string]any{}
}

func parseTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func nullString(value sql.NullString) string {
	if value.Valid {
		return cleanText(value.String)
	}
	return ""
}

func nullInt(value sql.NullInt64) int64 {
	if value.Valid {
		return value.Int64
	}
	return 0
}

func nullFloat(value sql.NullFloat64) any {
	if value.Valid {
		return value.Float64
	}
	return nil
}

func unixISO(epoch int64) string {
	if epoch <= 0 {
		return ""
	}
	return time.Unix(epoch, 0).UTC().Format("2006-01-02T15:04:05+00:00")
}

func statusFromLastSeen(lastSeen int64, now int64) string {
	if lastSeen > 0 && now-lastSeen <= 300 {
		return "Online"
	}
	return "Offline"
}

func coerceInt64(value any) int64 {
	switch typed := value.(type) {
	case nil:
		return 0
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err == nil {
			return int64(parsed)
		}
	}
	return 0
}

func firstNonEmpty(values ...any) any {
	for _, value := range values {
		if cleanText(value) != "" {
			return value
		}
	}
	return nil
}

func firstText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
