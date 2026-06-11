package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
)

const maxSoftwareIconBytes = 512 * 1024

var (
	windowsStoreGUIDNamePattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	softwareIconHashPattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	allowedSoftwareIconMIMEs    = map[string]bool{
		"image/png":     true,
		"image/jpeg":    true,
		"image/webp":    true,
		"image/x-icon":  true,
		"image/svg+xml": true,
	}
)

type agentDetailsExisting struct {
	Row         deviceRow
	Fingerprint sql.NullString
}

func (s *postgresOperatorStore) updateAgentDetails(ctx context.Context, deviceCtx deviceBearerAuthContext, payload map[string]any) (agentDetailsUpdateResult, int, error) {
	authGUID := normalizeCanonicalGUID(deviceCtx.GUID)
	if authGUID == "" {
		return agentDetailsUpdateResult{}, http.StatusNotFound, errors.New("device_not_registered")
	}
	details, ok := cloneMap(payload["details"])
	if !ok {
		return agentDetailsUpdateResult{}, http.StatusBadRequest, errors.New("invalid payload")
	}
	summary := ensureMap(details, "summary")
	if rawSoftware, exists := details["software"]; exists {
		details["software"] = normalizeAgentSoftwareInventory(rawSoftware)
	}
	var incomingServices map[string]any
	if rawServices, exists := details["services"]; exists {
		incomingServices = normalizeDeviceServicesAny(rawServices, 0)
		details["services"] = mapSliceToAny(incomingServices["services"])
	}
	if rawSessions, exists := details["sessions"]; exists {
		sessions := normalizeDeviceSessionsAny(rawSessions, 0)
		details["sessions"] = mapSliceToAny(sessions["sessions"])
	}
	if rawProcesses, exists := details["processes"]; exists {
		processes := normalizeDeviceProcessesAny(rawProcesses, 0)
		details["processes"] = mapSliceToAny(processes["processes"])
	}
	iconAssets := normalizeSoftwareIconAssets(details["software_icon_payloads"])
	delete(details, "software_icon_payloads")

	hostname := firstText(cleanText(payload["hostname"]), cleanText(summary["hostname"]))
	if hostname == "" {
		return agentDetailsUpdateResult{}, http.StatusBadRequest, errors.New("invalid payload")
	}
	serviceMode := normalizeAgentServiceMode(firstNonEmpty(payload["service_mode"], summary["service_mode"], claimString(deviceCtx.Claims, "service_mode"), "system"))
	agentID := cleanText(payload["agent_id"])
	agentHash := firstText(
		cleanText(payload["agent_build_id"]),
		cleanText(payload["installed_build_id"]),
		cleanText(payload["agent_hash"]),
		cleanText(summary["agent_build_id"]),
		cleanText(summary["installed_build_id"]),
		cleanText(summary["agent_hash"]),
	)
	incomingRoleHealth := firstNonNil(payload["agent_role_health"], payload["role_health"], details["agent_role_health"], summary["agent_role_health"])
	updateStatus := agentDetailsMapFromAny(firstNonNil(payload["agent_update_status"], summary["agent_update_status"]))
	releaseChannel := cleanText(firstNonEmpty(payload["agent_release_channel"], summary["agent_release_channel"]))
	agentBranch := normalizeAgentBranch(firstNonEmpty(payload["agent_branch"], summary["agent_branch"]))

	existing, found, err := s.loadAgentDetailsExisting(ctx, authGUID, hostname)
	if err != nil {
		return agentDetailsUpdateResult{}, http.StatusInternalServerError, err
	}
	if !found {
		return agentDetailsUpdateResult{}, http.StatusNotFound, errors.New("device_not_registered")
	}
	existingGUID := normalizeCanonicalGUID(existing.Row.GUID.String)
	if existingGUID != "" && existingGUID != authGUID {
		return agentDetailsUpdateResult{}, http.StatusForbidden, errors.New("guid_mismatch")
	}
	storedFingerprint := strings.ToLower(strings.TrimSpace(nullString(existing.Fingerprint)))
	if storedFingerprint != "" && deviceCtx.Fingerprint != "" && storedFingerprint != strings.ToLower(strings.TrimSpace(deviceCtx.Fingerprint)) {
		return agentDetailsUpdateResult{}, http.StatusForbidden, errors.New("fingerprint_mismatch")
	}
	if storedHostname := nullString(existing.Row.Hostname); storedHostname != "" {
		hostname = storedHostname
	}

	if agentID != "" && cleanText(summary["agent_id"]) == "" {
		summary["agent_id"] = agentID
	}
	if hostname != "" && cleanText(summary["hostname"]) == "" {
		summary["hostname"] = hostname
	}
	if agentHash != "" {
		summary["agent_hash"] = agentHash
		summary["agent_build_id"] = agentHash
	}
	if releaseChannel != "" {
		summary["agent_release_channel"] = releaseChannel
	}
	if agentBranch != "" {
		summary["agent_branch"] = agentBranch
	}
	applyAgentUpdateStatusSummary(summary, updateStatus)
	effectiveGUID := firstText(authGUID, existingGUID)
	if effectiveGUID != "" {
		summary["agent_guid"] = effectiveGUID
	}
	if deviceCtx.Fingerprint != "" {
		if cleanText(summary["ssl_key_fingerprint"]) == "" {
			summary["ssl_key_fingerprint"] = deviceCtx.Fingerprint
		}
	}

	now := time.Now().Unix()
	previous := buildDevicePayload(existing.Row, now)
	prevDetails, _ := cloneMap(previous["details"])
	prevSummary := agentDetailsMapFromAny(prevDetails["summary"])
	if isEmptyValue(summary["last_seen"]) && !isEmptyValue(prevSummary["last_seen"]) {
		summary["last_seen"] = prevSummary["last_seen"]
	}
	if isEmptyValue(summary["last_user"]) && !isEmptyValue(prevSummary["last_user"]) {
		summary["last_user"] = prevSummary["last_user"]
	}

	merged := deepMergePreserve(prevDetails, details)
	if incomingServices != nil {
		mergedServices := mergeDeviceServicesForDetails(existing.Row.Services, incomingServices)
		merged["services"] = mapSliceToAny(mergedServices["services"])
	} else {
		existingServices, _ := normalizeInventoryPayload(existing.Row.Services, "services")
		merged["services"] = existingServices
	}
	mergedSummary := ensureMap(merged, "summary")
	if hostname != "" && cleanText(mergedSummary["hostname"]) == "" {
		mergedSummary["hostname"] = hostname
	}
	if agentID != "" && cleanText(mergedSummary["agent_id"]) == "" {
		mergedSummary["agent_id"] = agentID
	}
	if agentHash != "" && isEmptyValue(mergedSummary["agent_hash"]) {
		mergedSummary["agent_hash"] = agentHash
	}
	if agentHash != "" && isEmptyValue(mergedSummary["agent_build_id"]) {
		mergedSummary["agent_build_id"] = agentHash
	}
	if effectiveGUID != "" {
		mergedSummary["agent_guid"] = effectiveGUID
	}
	if deviceCtx.Fingerprint != "" && cleanText(mergedSummary["ssl_key_fingerprint"]) == "" {
		mergedSummary["ssl_key_fingerprint"] = deviceCtx.Fingerprint
	}
	if description := nullString(existing.Row.Description); description != "" && isEmptyValue(mergedSummary["description"]) {
		mergedSummary["description"] = description
	}
	if existingHash := nullString(existing.Row.AgentHash); existingHash != "" {
		if isEmptyValue(mergedSummary["agent_hash"]) {
			mergedSummary["agent_hash"] = existingHash
		}
		if isEmptyValue(mergedSummary["agent_build_id"]) {
			mergedSummary["agent_build_id"] = existingHash
		}
	}
	createdAt := nullInt(existing.Row.CreatedAt)
	if createdAt <= 0 {
		createdAt = now
	}
	if cleanText(mergedSummary["created"]) == "" {
		mergedSummary["created"] = time.Unix(createdAt, 0).UTC().Format("2006-01-02 15:04:05")
	}
	if isEmptyValue(mergedSummary["created_at"]) {
		mergedSummary["created_at"] = createdAt
	}

	mergedRoleHealth := normalizeAgentRoleHealth(existing.Row.AgentRoleHealth.String, "")
	if incomingRoleHealth != nil {
		mergedRoleHealth = mergeAgentRoleHealth(existing.Row.AgentRoleHealth.String, incomingRoleHealth, serviceMode)
	}
	columns := extractAgentDetailDeviceColumns(merged)
	existingServices, _ := normalizeDeviceServicePayload(existing.Row.Services)
	servicesChanged := canonicalJSON(mapSliceToAny(existingServices)) != canonicalJSON(columns["services_payload"])
	softwareChanged := canonicalJSON(normalizeAgentSoftwareInventory(parseJSON(existing.Row.Software))) != canonicalJSON(normalizeAgentSoftwareInventory(merged["software"]))

	if err := s.writeAgentDetails(ctx, agentDetailsWriteInput{
		Hostname:        hostname,
		Description:     nullString(existing.Row.Description),
		CreatedAt:       createdAt,
		AgentHash:       firstText(agentHash, nullString(existing.Row.AgentHash)),
		AgentRoleHealth: serializeAgentRoleHealth(mergedRoleHealth),
		GUID:            effectiveGUID,
		Fingerprint:     deviceCtx.Fingerprint,
		Columns:         columns,
		Software:        normalizeAgentSoftwareInventory(merged["software"]),
		IconAssets:      iconAssets,
	}); err != nil {
		return agentDetailsUpdateResult{}, http.StatusInternalServerError, err
	}

	if len(updateStatus) > 0 {
		_ = s.reconcileAgentMaintenanceHeartbeat(ctx, hostname, updateStatus, firstText(cleanText(updateStatus["target_channel"]), cleanText(updateStatus["effective_channel"]), releaseChannel), firstText(cleanText(updateStatus["target_branch"]), agentBranch), firstText(agentHash, cleanText(mergedSummary["agent_build_id"]), cleanText(mergedSummary["agent_hash"])))
	}
	return agentDetailsUpdateResult{
		Payload:           map[string]any{"status": "ok"},
		Hostname:          hostname,
		ServicesChanged:   servicesChanged,
		SoftwareChanged:   softwareChanged,
		RememberDuplicate: true,
	}, http.StatusOK, nil
}

type agentDetailsWriteInput struct {
	Hostname        string
	Description     string
	CreatedAt       int64
	AgentHash       string
	AgentRoleHealth string
	GUID            string
	Fingerprint     string
	Columns         map[string]any
	Software        []map[string]any
	IconAssets      []softwareIconAsset
}

func (s *postgresOperatorStore) loadAgentDetailsExisting(ctx context.Context, authGUID string, hostname string) (agentDetailsExisting, bool, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return agentDetailsExisting{}, false, err
	}
	defer conn.Close()
	if hostname != "" {
		record, found, err := queryAgentDetailsExisting(ctx, conn, "d.hostname = $1", hostname)
		if err != nil || found {
			return record, found, err
		}
	}
	if authGUID != "" {
		return queryAgentDetailsExisting(ctx, conn, "UPPER(d.guid) = UPPER($1)", authGUID)
	}
	return agentDetailsExisting{}, false, nil
}

func queryAgentDetailsExisting(ctx context.Context, conn *sql.Conn, predicate string, arg any) (agentDetailsExisting, bool, error) {
	sqlText := `
		SELECT
			d.guid, d.hostname, d.description, d.created_at, d.last_enrollment_at,
			d.agent_hash, d.agent_role_health, d.memory, d.network, d.software,
			d.services, d.storage, d.cpu, d.sessions, d.processes, d.device_type,
			d.domain, d.external_ip, d.internal_ip, d.last_reboot, d.last_seen,
			d.cpu_percent, d.memory_percent, d.last_user, d.operating_system,
			d.uptime, d.agent_id, d.connection_type, d.connection_endpoint,
			d.agent_release_channel_override, d.agent_release_channel, d.agent_branch,
			d.agent_update_channel, d.agent_update_target_build_id, d.agent_update_state,
			d.agent_update_error, d.agent_update_source,
			NULL::BIGINT AS site_id, NULL::TEXT AS site_name, NULL::TEXT AS site_description,
			d.ssl_key_fingerprint
		  FROM engine.devices AS d
		 WHERE ` + predicate + `
		 LIMIT 1`
	var item agentDetailsExisting
	err := conn.QueryRowContext(ctx, sqlText, arg).Scan(
		&item.Row.GUID, &item.Row.Hostname, &item.Row.Description, &item.Row.CreatedAt, &item.Row.LastEnrollmentAt,
		&item.Row.AgentHash, &item.Row.AgentRoleHealth, &item.Row.Memory, &item.Row.Network, &item.Row.Software,
		&item.Row.Services, &item.Row.Storage, &item.Row.CPU, &item.Row.Sessions, &item.Row.Processes, &item.Row.DeviceType,
		&item.Row.Domain, &item.Row.ExternalIP, &item.Row.InternalIP, &item.Row.LastReboot, &item.Row.LastSeen,
		&item.Row.CPUPercent, &item.Row.MemoryPercent, &item.Row.LastUser, &item.Row.OperatingSystem,
		&item.Row.Uptime, &item.Row.AgentID, &item.Row.ConnectionType, &item.Row.ConnectionEndpoint,
		&item.Row.AgentReleaseChannelOverride, &item.Row.AgentReleaseChannel, &item.Row.AgentBranch,
		&item.Row.AgentUpdateChannel, &item.Row.AgentUpdateTargetBuildID, &item.Row.AgentUpdateState,
		&item.Row.AgentUpdateError, &item.Row.AgentUpdateSource, &item.Row.SiteID, &item.Row.SiteName, &item.Row.SiteDescription,
		&item.Fingerprint,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return agentDetailsExisting{}, false, nil
	}
	if err != nil {
		return agentDetailsExisting{}, false, err
	}
	return item, true, nil
}

func (s *postgresOperatorStore) writeAgentDetails(ctx context.Context, input agentDetailsWriteInput) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	params := []any{
		input.Hostname, input.Description, input.CreatedAt, nullIfEmpty(input.AgentHash), nullIfEmpty(input.AgentRoleHealth), nullIfEmpty(input.GUID),
		input.Columns["memory"], input.Columns["network"], input.Columns["software"], input.Columns["services"], input.Columns["storage"], input.Columns["cpu"],
		input.Columns["sessions"], input.Columns["processes"], input.Columns["device_type"], input.Columns["domain"], input.Columns["external_ip"], input.Columns["internal_ip"],
		input.Columns["last_reboot"], input.Columns["last_seen"], input.Columns["cpu_percent"], input.Columns["memory_percent"], input.Columns["last_user"],
		input.Columns["operating_system"], input.Columns["uptime"], input.Columns["agent_id"], input.Columns["connection_type"], input.Columns["connection_endpoint"],
		input.Columns["agent_release_channel"], input.Columns["agent_branch"], input.Columns["agent_update_channel"], input.Columns["agent_update_target_build_id"],
		input.Columns["agent_update_state"], input.Columns["agent_update_error"], input.Columns["agent_update_source"],
	}
	_, err = tx.ExecContext(ctx, agentDetailsUpsertSQL(), params...)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && string(pqErr.Code) == "23505" {
			return errors.New("device_update_conflict")
		}
		return err
	}
	if input.GUID != "" {
		if err := syncAgentDetailsSoftwareInventory(ctx, tx, input.GUID, input.Software); err != nil {
			return err
		}
	}
	if len(input.IconAssets) > 0 {
		if err := upsertSoftwareIconAssetsTx(ctx, tx, input.IconAssets); err != nil {
			return err
		}
	}
	if input.GUID != "" && input.Fingerprint != "" {
		nowISO := time.Now().UTC().Format(time.RFC3339)
		if _, err := tx.ExecContext(ctx, `UPDATE engine.devices SET ssl_key_fingerprint=$1, key_added_at=COALESCE(key_added_at, $2) WHERE UPPER(guid)=UPPER($3)`, strings.ToLower(input.Fingerprint), nowISO, input.GUID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO engine.device_keys(id, guid, ssl_key_fingerprint, added_at)
			VALUES($1,$2,$3,$4)
			ON CONFLICT(guid, ssl_key_fingerprint) DO NOTHING
		`, randomHexID(), input.GUID, strings.ToLower(input.Fingerprint), nowISO); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func agentDetailsUpsertSQL() string {
	return `
		INSERT INTO engine.devices(
			hostname, description, created_at, agent_hash, agent_role_health, guid,
			memory, network, software, services, storage, cpu, sessions, processes,
			device_type, domain, external_ip, internal_ip, last_reboot, last_seen,
			cpu_percent, memory_percent, last_user, operating_system, uptime, agent_id,
			connection_type, connection_endpoint, agent_release_channel, agent_branch,
			agent_update_channel, agent_update_target_build_id, agent_update_state,
			agent_update_error, agent_update_source
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,
			$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35
		)
		ON CONFLICT(hostname) DO UPDATE SET
			description=EXCLUDED.description,
			created_at=COALESCE(engine.devices.created_at, EXCLUDED.created_at),
			agent_hash=COALESCE(NULLIF(EXCLUDED.agent_hash, ''), engine.devices.agent_hash),
			agent_role_health=COALESCE(NULLIF(EXCLUDED.agent_role_health, ''), engine.devices.agent_role_health),
			guid=COALESCE(NULLIF(EXCLUDED.guid, ''), engine.devices.guid),
			memory=EXCLUDED.memory,
			network=EXCLUDED.network,
			software=EXCLUDED.software,
			services=EXCLUDED.services,
			storage=EXCLUDED.storage,
			cpu=EXCLUDED.cpu,
			sessions=EXCLUDED.sessions,
			processes=EXCLUDED.processes,
			device_type=COALESCE(NULLIF(EXCLUDED.device_type, ''), engine.devices.device_type),
			domain=COALESCE(NULLIF(EXCLUDED.domain, ''), engine.devices.domain),
			external_ip=COALESCE(NULLIF(EXCLUDED.external_ip, ''), engine.devices.external_ip),
			internal_ip=COALESCE(NULLIF(EXCLUDED.internal_ip, ''), engine.devices.internal_ip),
			last_reboot=COALESCE(NULLIF(EXCLUDED.last_reboot, ''), engine.devices.last_reboot),
			last_seen=COALESCE(NULLIF(EXCLUDED.last_seen, 0), engine.devices.last_seen),
			cpu_percent=COALESCE(EXCLUDED.cpu_percent, engine.devices.cpu_percent),
			memory_percent=COALESCE(EXCLUDED.memory_percent, engine.devices.memory_percent),
			last_user=COALESCE(NULLIF(EXCLUDED.last_user, ''), engine.devices.last_user),
			operating_system=COALESCE(NULLIF(EXCLUDED.operating_system, ''), engine.devices.operating_system),
			uptime=COALESCE(NULLIF(EXCLUDED.uptime, 0), engine.devices.uptime),
			agent_id=COALESCE(NULLIF(EXCLUDED.agent_id, ''), engine.devices.agent_id),
			connection_type=COALESCE(NULLIF(EXCLUDED.connection_type, ''), engine.devices.connection_type),
			connection_endpoint=COALESCE(NULLIF(EXCLUDED.connection_endpoint, ''), engine.devices.connection_endpoint),
			agent_release_channel=COALESCE(NULLIF(EXCLUDED.agent_release_channel, ''), engine.devices.agent_release_channel),
			agent_branch=COALESCE(NULLIF(EXCLUDED.agent_branch, ''), engine.devices.agent_branch),
			agent_update_channel=COALESCE(NULLIF(EXCLUDED.agent_update_channel, ''), engine.devices.agent_update_channel),
			agent_update_target_build_id=COALESCE(NULLIF(EXCLUDED.agent_update_target_build_id, ''), engine.devices.agent_update_target_build_id),
			agent_update_state=COALESCE(NULLIF(EXCLUDED.agent_update_state, ''), engine.devices.agent_update_state),
			agent_update_error=COALESCE(NULLIF(EXCLUDED.agent_update_error, ''), engine.devices.agent_update_error),
			agent_update_source=COALESCE(NULLIF(EXCLUDED.agent_update_source, ''), engine.devices.agent_update_source)`
}

func extractAgentDetailDeviceColumns(details map[string]any) map[string]any {
	summary := agentDetailsMapFromAny(details["summary"])
	out := map[string]any{}
	for _, key := range []string{"memory", "network", "software", "services", "storage", "sessions", "processes"} {
		out[key] = mustJSONString(anySlice(details[key]))
		out[key+"_payload"] = anySlice(details[key])
	}
	cpuValue := firstNonNil(summary["cpu"], details["cpu"])
	out["cpu"] = mustJSONString(agentDetailsMapFromAny(cpuValue))
	out["device_type"] = cleanText(firstNonEmpty(summary["device_type"], summary["type"]))
	out["domain"] = cleanText(summary["domain"])
	out["external_ip"] = cleanText(firstNonEmpty(summary["external_ip"], summary["public_ip"]))
	out["internal_ip"] = cleanText(firstNonEmpty(summary["internal_ip"], summary["private_ip"]))
	out["last_reboot"] = cleanText(firstNonEmpty(summary["last_reboot"], summary["last_boot"]))
	out["last_seen"] = nullableInt64Value(coerceInt64(summary["last_seen"]))
	out["cpu_percent"] = nullableFloatValue(summary["cpu_percent"])
	out["memory_percent"] = nullableFloatValue(summary["memory_percent"])
	out["last_user"] = cleanText(firstNonEmpty(summary["last_user"], summary["last_user_name"], summary["username"]))
	out["operating_system"] = cleanText(firstNonEmpty(summary["operating_system"], summary["agent_operating_system"], summary["os"]))
	out["uptime"] = nullableInt64Value(coerceInt64(firstNonEmpty(summary["uptime_sec"], summary["uptime_seconds"], summary["uptime"])))
	out["agent_id"] = cleanText(summary["agent_id"])
	out["connection_type"] = cleanText(firstNonEmpty(summary["connection_type"], summary["remote_type"]))
	out["connection_endpoint"] = cleanText(firstNonEmpty(summary["connection_endpoint"], summary["connection_address"], summary["address"], summary["external_ip"], summary["internal_ip"]))
	out["agent_release_channel"] = cleanText(firstNonEmpty(summary["agent_release_channel"], summary["release_channel"]))
	out["agent_branch"] = normalizeAgentBranch(firstNonEmpty(summary["agent_branch"], summary["branch"], summary["repo_ref"], summary["repo_branch"]))
	out["agent_update_channel"] = cleanText(firstNonEmpty(summary["agent_update_channel"], summary["target_channel"], summary["agent_release_channel_effective"]))
	out["agent_update_target_build_id"] = cleanText(firstNonEmpty(summary["agent_update_target_build_id"], summary["target_build_id"], summary["agent_target_build_id"]))
	out["agent_update_state"] = cleanText(firstNonEmpty(summary["agent_update_state"], summary["update_state"]))
	out["agent_update_error"] = cleanText(firstNonEmpty(summary["agent_update_error"], summary["last_update_error"]))
	out["agent_update_source"] = cleanText(firstNonEmpty(summary["agent_update_source"], summary["update_source"]))
	return out
}

func normalizeAgentSoftwareInventory(raw any) []map[string]any {
	entries := anySlice(raw)
	seen := map[string]bool{}
	out := []map[string]any{}
	for _, item := range entries {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := cleanText(entry["name"])
		if name == "" {
			continue
		}
		source := normalizeSoftwareSource(entry["source"])
		if source == "windows_store" && windowsStoreGUIDNamePattern.MatchString(name) {
			continue
		}
		version := cleanText(entry["version"])
		key := strings.ToLower(name) + "\x00" + strings.ToLower(version) + "\x00" + source
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, map[string]any{
			"name":     name,
			"version":  version,
			"source":   source,
			"metadata": softwareMetadata(entry),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := strings.ToLower(cleanText(out[i]["name"])) + "|" + strings.ToLower(cleanText(out[i]["source"])) + "|" + strings.ToLower(cleanText(out[i]["version"]))
		right := strings.ToLower(cleanText(out[j]["name"])) + "|" + strings.ToLower(cleanText(out[j]["source"])) + "|" + strings.ToLower(cleanText(out[j]["version"]))
		return left < right
	})
	return out
}

func normalizeDeviceServicesAny(raw any, defaultCapturedAt int64) map[string]any {
	payload := normalizeServicePayloadShape(sql.NullString{String: jsonText(raw), Valid: true})
	if _, ok := raw.(map[string]any); ok {
		payload = normalizeServicePayloadMap(raw)
	}
	reportedAt := coerceInt64(firstNonEmpty(payload["reported_at"], defaultCapturedAt))
	items := anySlice(payload["services"])
	byID := map[string]map[string]any{}
	for _, item := range items {
		entry := normalizeServiceEntry(item, reportedAt)
		if entry == nil {
			continue
		}
		byID[cleanText(entry["service_id"])] = entry
		if captured := coerceInt64(entry["captured_at"]); captured > reportedAt {
			reportedAt = captured
		}
	}
	services := make([]map[string]any, 0, len(byID))
	for _, entry := range byID {
		services = append(services, entry)
	}
	sort.SliceStable(services, func(i, j int) bool {
		left := strings.ToLower(firstText(cleanText(services[i]["display_name"]), cleanText(services[i]["name"]))) + "|" + strings.ToLower(cleanText(services[i]["name"])) + "|" + strings.ToLower(cleanText(services[i]["description"]))
		right := strings.ToLower(firstText(cleanText(services[j]["display_name"]), cleanText(services[j]["name"]))) + "|" + strings.ToLower(cleanText(services[j]["name"])) + "|" + strings.ToLower(cleanText(services[j]["description"]))
		return left < right
	})
	return map[string]any{"services": services, "reported_at": reportedAt}
}

func normalizeServicePayloadMap(raw any) map[string]any {
	switch typed := raw.(type) {
	case map[string]any:
		return map[string]any{"services": anySlice(typed["services"]), "reported_at": coerceInt64(typed["reported_at"])}
	default:
		return map[string]any{"services": anySlice(raw), "reported_at": int64(0)}
	}
}

func mergeDeviceServicesForDetails(existingRaw sql.NullString, incomingPayload map[string]any) map[string]any {
	existingServices, _ := normalizeDeviceServicePayload(existingRaw)
	incomingServices := mapSlice(incomingPayload["services"])
	existingByID := map[string]map[string]any{}
	for _, entry := range existingServices {
		existingByID[cleanText(entry["service_id"])] = entry
	}
	merged := make([]map[string]any, 0, len(incomingServices))
	reportedAt := coerceInt64(incomingPayload["reported_at"])
	for _, incoming := range incomingServices {
		entry := copyMap(incoming)
		existing := existingByID[cleanText(entry["service_id"])]
		pendingAction := normalizeServiceActionValue(firstNonEmpty(entry["pending_action"], existing["pending_action"]))
		desiredStatus := normalizeServiceStatusCode(firstNonEmpty(entry["desired_status"], desiredServiceStatus(pendingAction), existing["desired_status"]))
		requestedAt := maxInt64(coerceInt64(existing["pending_requested_at"]), coerceInt64(entry["pending_requested_at"]))
		requestedBy := firstText(cleanText(entry["pending_requested_by"]), cleanText(existing["pending_requested_by"]))
		displayName := firstText(cleanText(entry["display_name"]), cleanText(existing["display_name"]))
		if displayName != "" {
			entry["display_name"] = displayName
		}
		if pendingAction != "" && desiredStatus != "" && !(coerceInt64(entry["captured_at"]) >= requestedAt && normalizeServiceStatusCode(entry["status_code"]) == desiredStatus) {
			entry["pending_action"] = pendingAction
			entry["desired_status"] = desiredStatus
			entry["pending_requested_at"] = requestedAt
			entry["pending_requested_by"] = requestedBy
		}
		if captured := coerceInt64(entry["captured_at"]); captured > reportedAt {
			reportedAt = captured
		}
		merged = append(merged, entry)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		left := strings.ToLower(firstText(cleanText(merged[i]["display_name"]), cleanText(merged[i]["name"]))) + "|" + strings.ToLower(cleanText(merged[i]["name"])) + "|" + strings.ToLower(cleanText(merged[i]["description"]))
		right := strings.ToLower(firstText(cleanText(merged[j]["display_name"]), cleanText(merged[j]["name"]))) + "|" + strings.ToLower(cleanText(merged[j]["name"])) + "|" + strings.ToLower(cleanText(merged[j]["description"]))
		return left < right
	})
	return map[string]any{"services": merged, "reported_at": reportedAt}
}

func normalizeDeviceProcessesAny(raw any, defaultReportedAt int64) map[string]any {
	payload := normalizeNamedListPayload(raw, "processes", defaultReportedAt)
	byName := map[string]map[string]any{}
	for _, item := range anySlice(payload["processes"]) {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := firstText(cleanText(entry["name"]), cleanText(entry["process_name"]), cleanText(entry["image_name"]))
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		count := maxInt64(1, coerceInt64(firstNonEmpty(entry["count"], 1)))
		if existing := byName[key]; existing != nil {
			if count > coerceInt64(existing["count"]) {
				existing["count"] = count
			}
			continue
		}
		byName[key] = map[string]any{"name": name, "count": count}
	}
	processes := make([]map[string]any, 0, len(byName))
	for _, entry := range byName {
		processes = append(processes, entry)
	}
	sort.SliceStable(processes, func(i, j int) bool {
		return strings.ToLower(cleanText(processes[i]["name"])) < strings.ToLower(cleanText(processes[j]["name"]))
	})
	return map[string]any{"processes": processes, "reported_at": coerceInt64(payload["reported_at"])}
}

func normalizeDeviceSessionsAny(raw any, defaultReportedAt int64) map[string]any {
	payload := normalizeNamedListPayload(raw, "sessions", defaultReportedAt)
	seen := map[string]map[string]any{}
	for _, item := range anySlice(payload["sessions"]) {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		username := cleanText(entry["username"])
		sessionName := cleanText(firstNonEmpty(entry["session_name"], entry["name"]))
		sessionID := coerceInt64(entry["session_id"])
		if username == "" && sessionName == "" && sessionID <= 0 {
			continue
		}
		stateCode := normalizeSessionState(firstNonEmpty(entry["state_code"], entry["state"]))
		key := fmt.Sprintf("%d:%s:%s", sessionID, strings.ToLower(username), strings.ToLower(sessionName))
		seen[key] = map[string]any{
			"session_id":               sessionID,
			"username":                 username,
			"session_name":             sessionName,
			"state_code":               stateCode,
			"state":                    sessionStateLabel(stateCode),
			"protocol":                 cleanText(entry["protocol"]),
			"is_rdp":                   boolFromAny(entry["is_rdp"]),
			"eligible_for_interactive": boolFromAny(firstNonEmpty(entry["eligible_for_interactive"], stateCode == "active" || stateCode == "locked")),
			"helper_ready":             boolFromAny(entry["helper_ready"]),
			"helper_pid":               coerceInt64(entry["helper_pid"]),
			"helper_last_seen_at":      coerceInt64(entry["helper_last_seen_at"]),
		}
	}
	sessions := make([]map[string]any, 0, len(seen))
	for _, entry := range seen {
		sessions = append(sessions, entry)
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		left := strings.ToLower(cleanText(sessions[i]["username"])) + fmt.Sprintf("|%012d|", coerceInt64(sessions[i]["session_id"])) + strings.ToLower(cleanText(sessions[i]["session_name"]))
		right := strings.ToLower(cleanText(sessions[j]["username"])) + fmt.Sprintf("|%012d|", coerceInt64(sessions[j]["session_id"])) + strings.ToLower(cleanText(sessions[j]["session_name"]))
		return left < right
	})
	return map[string]any{"sessions": sessions, "reported_at": coerceInt64(payload["reported_at"])}
}

func normalizeNamedListPayload(raw any, key string, defaultReportedAt int64) map[string]any {
	if row, ok := raw.(map[string]any); ok {
		return map[string]any{key: anySlice(row[key]), "reported_at": firstNonEmpty(row["reported_at"], defaultReportedAt)}
	}
	if text := cleanText(raw); text != "" {
		var decoded any
		if err := json.Unmarshal([]byte(text), &decoded); err == nil {
			return normalizeNamedListPayload(decoded, key, defaultReportedAt)
		}
	}
	return map[string]any{key: anySlice(raw), "reported_at": defaultReportedAt}
}

func normalizeSessionState(value any) string {
	text := strings.ToLower(strings.ReplaceAll(cleanText(value), " ", "_"))
	switch text {
	case "active", "connected":
		return "active"
	case "locked", "lock":
		return "locked"
	case "disconnected", "disc":
		return "disconnected"
	case "idle":
		return "idle"
	default:
		return "unknown"
	}
}

func sessionStateLabel(code string) string {
	switch normalizeSessionState(code) {
	case "active":
		return "Active"
	case "locked":
		return "Locked"
	case "disconnected":
		return "Disconnected"
	case "idle":
		return "Idle"
	default:
		return "Unknown"
	}
}

func normalizeSoftwareIconAssets(raw any) []softwareIconAsset {
	items := anySlice(raw)
	byHash := map[string]softwareIconAsset{}
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		data := firstText(cleanText(entry["data_base64"]), cleanText(entry["icon_data_base64"]), cleanText(entry["data"]))
		if data == "" {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(data)
		if err != nil || len(decoded) == 0 || len(decoded) > maxSoftwareIconBytes {
			continue
		}
		sum := sha256.Sum256(decoded)
		hash := hex.EncodeToString(sum[:])
		mimeType := strings.ToLower(firstText(cleanText(entry["mime_type"]), "image/png"))
		if !allowedSoftwareIconMIMEs[mimeType] {
			mimeType = "image/png"
		}
		byHash[hash] = softwareIconAsset{Hash: hash, MIMEType: mimeType, Bytes: decoded}
	}
	assets := make([]softwareIconAsset, 0, len(byHash))
	for _, asset := range byHash {
		assets = append(assets, asset)
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].Hash < assets[j].Hash })
	return assets
}

func syncAgentDetailsSoftwareInventory(ctx context.Context, tx *sql.Tx, guid string, rows []map[string]any) error {
	guid = normalizeCanonicalGUID(guid)
	if guid == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM engine.device_software_inventory WHERE device_guid=$1`, guid); err != nil {
		return err
	}
	now := time.Now().Unix()
	for _, row := range rows {
		name := cleanText(row["name"])
		if name == "" {
			continue
		}
		metadata := mustJSONString(agentDetailsMapFromAny(row["metadata"]))
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO engine.device_software_inventory(device_guid, name, name_normalized, version, source, captured_at, metadata_json)
			VALUES($1,$2,$3,$4,$5,$6,$7)
		`, guid, name, strings.ToLower(name), cleanText(row["version"]), normalizeSoftwareSource(row["source"]), now, metadata); err != nil {
			return err
		}
	}
	return nil
}

func upsertSoftwareIconAssetsTx(ctx context.Context, tx *sql.Tx, assets []softwareIconAsset) error {
	now := time.Now().Unix()
	for _, asset := range assets {
		if !softwareIconHashPattern.MatchString(asset.Hash) || len(asset.Bytes) == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO engine.software_icon_assets(icon_hash, mime_type, icon_bytes, byte_size, created_at, updated_at)
			VALUES($1,$2,$3,$4,$5,$5)
			ON CONFLICT(icon_hash) DO UPDATE SET
				mime_type=EXCLUDED.mime_type,
				icon_bytes=EXCLUDED.icon_bytes,
				byte_size=EXCLUDED.byte_size,
				updated_at=EXCLUDED.updated_at
		`, asset.Hash, asset.MIMEType, asset.Bytes, len(asset.Bytes), now); err != nil {
			return err
		}
	}
	return nil
}

func applyAgentUpdateStatusSummary(summary map[string]any, updateStatus map[string]any) {
	if len(updateStatus) == 0 {
		return
	}
	if value := cleanText(firstNonEmpty(updateStatus["target_channel"], updateStatus["effective_channel"])); value != "" {
		summary["agent_update_channel"] = value
	}
	if value := cleanText(updateStatus["target_build_id"]); value != "" {
		summary["agent_update_target_build_id"] = value
	}
	if value := cleanText(updateStatus["state"]); value != "" {
		summary["agent_update_state"] = value
	}
	if value := cleanText(updateStatus["last_error"]); value != "" {
		summary["agent_update_error"] = value
	}
	if value := cleanText(updateStatus["last_source"]); value != "" {
		summary["agent_update_source"] = value
	}
}

func deepMergePreserve(base map[string]any, incoming map[string]any) map[string]any {
	merged := deepCopyMap(base)
	for key, value := range incoming {
		if incomingMap, ok := value.(map[string]any); ok {
			if baseMap, ok := merged[key].(map[string]any); ok {
				merged[key] = deepMergePreserve(baseMap, incomingMap)
				continue
			}
		}
		if !isEmptyValue(value) {
			merged[key] = value
		}
	}
	return merged
}

func cloneMap(value any) (map[string]any, bool) {
	item, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	return deepCopyMap(item), true
}

func ensureMap(parent map[string]any, key string) map[string]any {
	if child, ok := parent[key].(map[string]any); ok {
		return child
	}
	child := map[string]any{}
	parent[key] = child
	return child
}

func agentDetailsMapFromAny(value any) map[string]any {
	if row, ok := value.(map[string]any); ok {
		return row
	}
	if text := cleanText(value); text != "" {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(text), &decoded); err == nil {
			return decoded
		}
	}
	return map[string]any{}
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func isEmptyValue(value any) bool {
	if value == nil {
		return true
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) == ""
	case int:
		return typed == 0
	case int64:
		return typed == 0
	case float64:
		return typed == 0
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	default:
		return false
	}
}

func nullableInt64Value(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func nullableFloatValue(value any) any {
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return nil
		}
		var parsed float64
		if _, err := fmt.Sscan(text, &parsed); err == nil {
			return parsed
		}
	}
	return nil
}

func mapSliceToAny(value any) []any {
	switch typed := value.(type) {
	case []map[string]any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, item)
		}
		return out
	case []any:
		return typed
	default:
		return []any{}
	}
}

func jsonText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return mustJSONString(value)
}

func canonicalJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	var buffer bytes.Buffer
	if err := json.Compact(&buffer, encoded); err != nil {
		return string(encoded)
	}
	return buffer.String()
}

func randomHexID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw)
}
