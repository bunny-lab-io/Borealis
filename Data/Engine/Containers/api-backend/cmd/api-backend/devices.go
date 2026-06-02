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
)

type deviceListFilter struct {
	ConnectionType string
	Hostname       string
	OnlyAgents     bool
}

type deviceListStore interface {
	listDevices(ctx context.Context, profile operatorProfile, filter deviceListFilter) ([]map[string]any, error)
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

func registerDeviceRoutes(mux *http.ServeMux, auth *authService) {
	mux.HandleFunc("GET /api/agents", agentListHandler(auth))
	mux.HandleFunc("GET /api/devices", deviceListHandler(auth))
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
		writeJSON(w, http.StatusOK, map[string]any{"devices": devices})
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
	devices := make([]map[string]any, 0, len(rawRows))
	for _, row := range rawRows {
		devices = append(devices, buildDevicePayload(row, now))
	}
	return devices, nil
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
