package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	softwareIconResourcePattern      = regexp.MustCompile(`(?i)^\s*"?(?P<path>.+?\.(?:exe|dll|icl|cpl|ocx|scr))"?\s*(?:,\s*(?P<index>-?\d+))?\s*$`)
	softwareICOResourcePattern       = regexp.MustCompile(`(?i)^\s*"?.+?\.ico"?\s*$`)
	softwareStoreGUIDNamePattern     = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	softwareQuotedCommandPattern     = regexp.MustCompile(`^\s*"(?P<exe>[^"]+)"\s*(?P<args>.*)$`)
	softwareCommandWithExtPattern    = regexp.MustCompile(`(?i)^\s*(?P<exe>(?:(?:[A-Za-z]:|\\\\[^\\/\s]+\\[^\\/\s]+)[^\r\n"]*?\.(?:exe|com|cmd|bat|msi|ps1)|[^\\/\s"']+\.(?:exe|com|cmd|bat|msi|ps1)))\s*(?P<args>.*)$`)
	softwareQuietArgPattern          = regexp.MustCompile(`(?i)^(/quiet|/qn|/qb!?|/passive|/s|/silent|/verysilent|--silent|--quiet|/suppressmsgboxes)$`)
	softwareOverrideWriteLock        sync.Mutex
	softwareSlugSeparatorPattern     = regexp.MustCompile(`[^a-z0-9]+`)
	softwareWindowsProductCodeStrict = regexp.MustCompile(`(?i)^\{[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\}$`)
)

type softwareOverrideStore interface {
	loadDeviceSoftwareContext(ctx context.Context, profile operatorProfile, hostname string) (softwareOverrideContext, int, error)
}

type softwareOverrideContext struct {
	Hostname        string
	AgentID         string
	OperatingSystem string
	SiteID          *int64
	Software        []map[string]any
	Route           *agentWorkerRoute
}

type resolvedSoftwareEntry struct {
	Context softwareOverrideContext
	Entry   map[string]any
}

func deviceSoftwareOverrideHandler(auth *authService, action string) http.HandlerFunc {
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
		body, err := readJSONMap(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json", "message": "Request body must be valid JSON."})
			return
		}
		store, ok := auth.store.(softwareOverrideStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "software_override_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		snapshot, status, err := store.loadDeviceSoftwareContext(ctx, profile, r.PathValue("hostname"))
		cancel()
		if err != nil {
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		entry, code, payload := resolveSoftwareEntryFromBody(snapshot.Software, body)
		if code != 0 {
			writeJSON(w, code, payload)
			return
		}
		rule, err := upsertSoftwareActionRule(action, entry, body)
		if err != nil {
			if errors.Is(err, errSoftwareRuleNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]any{
					"error":   "uninstall_block_not_found",
					"message": "No matching uninstall block rule was found for this software row.",
				})
				return
			}
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": invalidSoftwareActionError(action), "message": err.Error()})
			return
		}
		payload = map[string]any{
			"status":   "ok",
			"hostname": firstText(snapshot.Hostname, r.PathValue("hostname")),
			"rule":     rule,
		}
		if action == "uninstall-unblock" {
			payload = map[string]any{
				"status":           "ok",
				"hostname":         firstText(snapshot.Hostname, r.PathValue("hostname")),
				"removed_rule_ids": rule["removed_rule_ids"],
			}
		}
		if action == "icon-override" {
			emitted, refreshPayload := requestSoftwareInventoryRefresh(r.Context(), auth, store, profile, snapshot, "operator_icon_override:"+cleanText(rule["rule_id"]))
			payload["refresh_requested"] = emitted
			for key, value := range refreshPayload {
				payload[key] = value
			}
		}
		writeJSON(w, http.StatusOK, payload)
	}
}

func bulkSoftwareActionHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		action := strings.ToLower(cleanText(r.PathValue("action")))
		if action != "icon-override" && action != "uninstall-override" && action != "uninstall-block" && action != "uninstall-unblock" {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "unsupported_action"})
			return
		}
		profile, err := auth.currentProfile(r.Context(), r)
		if err != nil {
			if isUnauthorizedAuthError(err) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
			return
		}
		body, err := readJSONMap(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json", "message": "Request body must be valid JSON."})
			return
		}
		store, ok := auth.store.(softwareOverrideStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "software_override_unavailable"})
			return
		}
		resolved, code, payload := resolveBulkSoftwareEntries(r.Context(), auth, store, profile, body)
		if code != 0 {
			writeJSON(w, code, payload)
			return
		}

		persisted := make([]map[string]any, 0, len(resolved))
		errorsPayload := []map[string]any{}
		iconRefreshCandidates := map[string]softwareOverrideContext{}
		for _, item := range resolved {
			rule, err := upsertSoftwareActionRule(action, item.Entry, body)
			if err != nil {
				if errors.Is(err, errSoftwareRuleNotFound) {
					errorsPayload = append(errorsPayload, map[string]any{
						"hostname": cleanText(item.Context.Hostname),
						"software": cleanText(item.Entry["name"]),
						"error":    "rule_not_found",
						"message":  err.Error(),
					})
					continue
				}
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_software_action", "message": err.Error()})
				return
			}
			persisted = append(persisted, map[string]any{
				"hostname": cleanText(item.Context.Hostname),
				"software": map[string]any{
					"name":    cleanText(item.Entry["name"]),
					"version": cleanText(item.Entry["version"]),
					"source":  normalizeSoftwareSource(item.Entry["source"]),
				},
				"rule": rule,
			})
			if action == "icon-override" {
				key := strings.ToLower(cleanText(item.Context.Hostname))
				if key != "" {
					if existing, ok := iconRefreshCandidates[key]; !ok || (existing.Route == nil && item.Context.Route != nil) {
						iconRefreshCandidates[key] = item.Context
					}
				}
			}
		}
		if len(persisted) == 0 && len(errorsPayload) > 0 {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "software_action_failed", "errors": errorsPayload})
			return
		}
		response := map[string]any{
			"status": "ok",
			"action": action,
			"rules":  persisted,
			"errors": errorsPayload,
			"count":  len(persisted),
		}
		if action == "icon-override" && len(iconRefreshCandidates) > 0 {
			refreshSnapshot := firstSoftwareRefreshCandidate(iconRefreshCandidates)
			emitted, refreshPayload := requestSoftwareInventoryRefresh(r.Context(), auth, store, profile, refreshSnapshot, "operator_icon_override_bulk")
			refresh := map[string]any{
				"hostname":          cleanText(refreshSnapshot.Hostname),
				"refresh_requested": emitted,
			}
			for key, value := range refreshPayload {
				refresh[key] = value
			}
			response["refresh"] = refresh
		}
		writeJSON(w, http.StatusOK, response)
	}
}

func (s *postgresOperatorStore) loadDeviceSoftwareContext(ctx context.Context, profile operatorProfile, hostname string) (softwareOverrideContext, int, error) {
	hostname = cleanText(hostname)
	if hostname == "" {
		return softwareOverrideContext{}, http.StatusNotFound, errors.New("not found")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return softwareOverrideContext{}, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	var row struct {
		Hostname        sql.NullString
		AgentID         sql.NullString
		Software        sql.NullString
		OperatingSystem sql.NullString
		SiteID          sql.NullInt64
	}
	err = conn.QueryRowContext(ctx, `
		SELECT d.hostname, d.agent_id, d.software, d.operating_system, ds.site_id
		  FROM engine.devices AS d
	 LEFT JOIN engine.device_sites AS ds ON ds.device_hostname = d.hostname
		 WHERE LOWER(d.hostname) = LOWER($1)
	  ORDER BY COALESCE(d.last_seen, 0) DESC
		 LIMIT 1
	`, hostname).Scan(&row.Hostname, &row.AgentID, &row.Software, &row.OperatingSystem, &row.SiteID)
	if errors.Is(err, sql.ErrNoRows) {
		return softwareOverrideContext{}, http.StatusNotFound, errors.New("not found")
	}
	if err != nil {
		return softwareOverrideContext{}, http.StatusInternalServerError, err
	}
	if row.SiteID.Valid {
		allowed, err := profileCanAccessSite(ctx, conn, profile, row.SiteID.Int64)
		if err != nil {
			return softwareOverrideContext{}, http.StatusInternalServerError, err
		}
		if !allowed {
			return softwareOverrideContext{}, http.StatusNotFound, errors.New("not found")
		}
	} else if !strings.EqualFold(strings.TrimSpace(profile.Role), "admin") {
		return softwareOverrideContext{}, http.StatusNotFound, errors.New("not found")
	}

	snapshot := softwareOverrideContext{
		Hostname:        nullString(row.Hostname),
		AgentID:         nullString(row.AgentID),
		OperatingSystem: nullString(row.OperatingSystem),
		Software:        normalizeSoftwareInventory(parseJSONArray(row.Software)),
	}
	if row.SiteID.Valid && row.SiteID.Int64 > 0 {
		siteID := row.SiteID.Int64
		snapshot.SiteID = &siteID
		route, err := fetchAgentWorkerRoute(ctx, conn, siteID)
		if err != nil {
			return softwareOverrideContext{}, http.StatusInternalServerError, err
		}
		snapshot.Route = route
	}
	return snapshot, http.StatusOK, nil
}

func resolveSoftwareEntryFromBody(software []map[string]any, body map[string]any) (map[string]any, int, map[string]any) {
	name := cleanText(body["name"])
	version := cleanText(body["version"])
	requestedSource := cleanText(body["source"])
	source := normalizeSoftwareSource(requestedSource)
	if name == "" {
		return nil, http.StatusBadRequest, map[string]any{"error": "software_name_required"}
	}
	if requestedSource == "" {
		return nil, http.StatusBadRequest, map[string]any{"error": "software_source_required"}
	}
	entry := findSoftwareEntry(software, name, version, source)
	if entry == nil {
		return nil, http.StatusNotFound, map[string]any{"error": "software_not_found"}
	}
	return entry, 0, nil
}

func resolveBulkSoftwareEntries(ctx context.Context, auth *authService, store softwareOverrideStore, profile operatorProfile, body map[string]any) ([]resolvedSoftwareEntry, int, map[string]any) {
	rawEntries := firstNonEmpty(body["entries"], body["software"])
	entries := []any{}
	switch typed := rawEntries.(type) {
	case []any:
		entries = typed
	case []map[string]any:
		for _, item := range typed {
			entries = append(entries, item)
		}
	case map[string]any:
		entries = []any{typed}
	}
	if len(entries) == 0 {
		return nil, http.StatusBadRequest, map[string]any{"error": "software_entries_required"}
	}
	seen := map[string]struct{}{}
	resolved := []resolvedSoftwareEntry{}
	for _, raw := range entries {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		hostname := cleanText(item["hostname"])
		name := cleanText(item["name"])
		version := cleanText(item["version"])
		requestedSource := cleanText(item["source"])
		source := normalizeSoftwareSource(requestedSource)
		if hostname == "" || name == "" || requestedSource == "" {
			continue
		}
		key := strings.ToLower(hostname) + "\x00" + strings.ToLower(name) + "\x00" + strings.ToLower(version) + "\x00" + source
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		callCtx, cancel := requestTimeout(ctx, auth)
		snapshot, status, err := store.loadDeviceSoftwareContext(callCtx, profile, hostname)
		cancel()
		if err != nil || status >= 400 {
			continue
		}
		entry := findSoftwareEntry(snapshot.Software, name, version, source)
		if entry == nil {
			continue
		}
		resolved = append(resolved, resolvedSoftwareEntry{Context: snapshot, Entry: entry})
	}
	if len(resolved) == 0 {
		return nil, http.StatusNotFound, map[string]any{"error": "software_not_found"}
	}
	return resolved, 0, nil
}

func upsertSoftwareActionRule(action string, softwareEntry map[string]any, body map[string]any) (map[string]any, error) {
	switch action {
	case "icon-override":
		clearIcon := parseTruthy(cleanText(firstNonEmpty(body["clear_icon"], body["remove_icon"])))
		displayIcon := ""
		if !clearIcon {
			displayIcon = canonicalizeSoftwareIconOverrideResource(firstNonEmpty(body["display_icon"], body["icon_location"]))
		}
		if !clearIcon && displayIcon == "" {
			return nil, errors.New("Choose or enter a valid EXE, DLL, ICO, or icon resource path.")
		}
		rule := map[string]any{
			"rule_id": buildSoftwareRuleID("icon_override", softwareEntry, false),
			"name":    cleanText(softwareEntry["name"]),
		}
		if clearIcon {
			rule["clear_icon"] = true
		} else {
			rule["display_icon"] = displayIcon
		}
		return upsertSoftwareIconOverride(rule)
	case "uninstall-override":
		applicationPath := cleanText(firstNonEmpty(body["application_path"], body["file_path"]))
		if applicationPath == "" {
			return nil, errors.New("application_path is required")
		}
		metadata := softwareMetadata(softwareEntry)
		rule := buildSoftwareRuleMatchBase("uninstall_override", softwareEntry)
		rule["strategy"] = "direct_command"
		rule["quiet_uninstall_string"] = buildWindowsCommand(applicationPath, cleanText(body["arguments"]), nil)
		rule["uninstall_string"] = cleanText(metadata["uninstall_string"])
		rule["summary"] = "Operator-defined global uninstall override."
		return upsertUninstallOverride(rule)
	case "uninstall-block":
		reason := cleanText(body["reason"])
		if reason == "" {
			return nil, errors.New("reason is required")
		}
		metadata := softwareMetadata(softwareEntry)
		parsedQuiet := splitWindowsCommandLine(firstText(cleanText(metadata["quiet_uninstall_string"]), cleanText(metadata["uninstall_string"])))
		executableName := strings.ToLower(cleanText(parsedQuiet["executable_name"]))
		quietArgs := extractQuietArgumentTokens(cleanText(parsedQuiet["arguments"]))
		rule := buildSoftwareRuleMatchBase("uninstall_block", softwareEntry)
		rule["reason"] = reason
		if executableName != "" {
			rule["exe_names"] = []any{executableName}
		}
		if len(quietArgs) > 0 {
			values := make([]any, 0, len(quietArgs))
			for _, item := range quietArgs {
				values = append(values, item)
			}
			rule["quiet_args_any"] = values
		}
		return upsertUninstallBlocklistRule(rule)
	case "uninstall-unblock":
		matches := findMatchingUninstallBlocklistRules(softwareEntry)
		removed := []any{}
		for _, rule := range matches {
			ruleID := cleanText(rule["rule_id"])
			if ruleID != "" && removeUninstallBlocklistRule(ruleID) {
				removed = append(removed, ruleID)
			}
		}
		if len(removed) == 0 {
			return nil, errSoftwareRuleNotFound
		}
		return map[string]any{"removed_rule_ids": removed}, nil
	default:
		return nil, errors.New("unsupported action")
	}
}

var errSoftwareRuleNotFound = errors.New("No matching uninstall block rule was found for this software row.")

func invalidSoftwareActionError(action string) string {
	switch action {
	case "icon-override":
		return "invalid_icon_override"
	case "uninstall-override":
		return "invalid_uninstall_override"
	case "uninstall-block":
		return "invalid_uninstall_block"
	default:
		return "invalid_software_action"
	}
}

func requestSoftwareInventoryRefresh(ctx context.Context, auth *authService, store softwareOverrideStore, profile operatorProfile, snapshot softwareOverrideContext, reason string) (bool, map[string]any) {
	requestedAt := time.Now().Unix()
	payload := map[string]any{
		"hostname":     snapshot.Hostname,
		"agent_id":     snapshot.AgentID,
		"requested_at": requestedAt,
	}
	deadline := time.Now().Add(8 * time.Second)
	current := snapshot
	for {
		if current.Route != nil {
			eventPayload := map[string]any{
				"hostname":     current.Hostname,
				"agent_id":     current.AgentID,
				"requested_at": requestedAt,
				"requested_by": firstText(cleanText(profile.Username), "unknown"),
				"reason":       cleanText(reason),
			}
			result, _, err := emitWorkerHostServiceEvent(ctx, auth, current.Route, map[string]any{
				"hostname":            current.Hostname,
				"service_mode":        "system",
				"event_name":          "software_inventory_refresh_request",
				"payload":             eventPayload,
				"allow_pending":       true,
				"pending_ttl_seconds": int64(180),
			}, 6*time.Second)
			if err == nil && (boolFromAny(result["emitted"]) || boolFromAny(result["queued"])) {
				payload["hostname"] = current.Hostname
				payload["agent_id"] = current.AgentID
				return true, payload
			}
		}
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return false, payload
		case <-time.After(500 * time.Millisecond):
		}
		callCtx, cancel := requestTimeout(ctx, auth)
		next, status, err := store.loadDeviceSoftwareContext(callCtx, profile, current.Hostname)
		cancel()
		if err == nil && status < 400 {
			current = next
		}
	}
	return false, payload
}

func firstSoftwareRefreshCandidate(candidates map[string]softwareOverrideContext) softwareOverrideContext {
	keys := make([]string, 0, len(candidates))
	for key := range candidates {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if candidates[key].Route != nil {
			return candidates[key]
		}
	}
	return candidates[keys[0]]
}

func normalizeSoftwareInventory(raw []any) []map[string]any {
	seen := map[string]struct{}{}
	rows := []map[string]any{}
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := cleanText(entry["name"])
		if name == "" {
			continue
		}
		version := cleanText(entry["version"])
		source := normalizeSoftwareSource(entry["source"])
		if source == "windows_store" && softwareStoreGUIDNamePattern.MatchString(name) {
			continue
		}
		key := strings.ToLower(name) + "\x00" + strings.ToLower(version) + "\x00" + source
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		rows = append(rows, map[string]any{
			"name":     name,
			"version":  version,
			"source":   source,
			"metadata": softwareMetadata(entry),
		})
	}
	sort.SliceStable(rows, func(left, right int) bool {
		leftKey := strings.ToLower(cleanText(rows[left]["name"])) + "\x00" + strings.ToLower(cleanText(rows[left]["source"])) + "\x00" + strings.ToLower(cleanText(rows[left]["version"]))
		rightKey := strings.ToLower(cleanText(rows[right]["name"])) + "\x00" + strings.ToLower(cleanText(rows[right]["source"])) + "\x00" + strings.ToLower(cleanText(rows[right]["version"]))
		return leftKey < rightKey
	})
	return rows
}

func findSoftwareEntry(rows []map[string]any, name string, version string, source string) map[string]any {
	normalizedName := strings.ToLower(cleanText(name))
	normalizedVersion := cleanText(version)
	normalizedSource := normalizeSoftwareSource(source)
	if normalizedName == "" {
		return nil
	}
	var exact map[string]any
	fallbacks := []map[string]any{}
	for _, row := range rows {
		if strings.ToLower(cleanText(row["name"])) != normalizedName || normalizeSoftwareSource(row["source"]) != normalizedSource {
			continue
		}
		fallbacks = append(fallbacks, row)
		if normalizedVersion != "" && cleanText(row["version"]) == normalizedVersion {
			exact = row
			break
		}
	}
	if exact != nil {
		return exact
	}
	if len(fallbacks) == 1 {
		return fallbacks[0]
	}
	return nil
}

func softwareMetadata(entry any) map[string]any {
	row, ok := entry.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	metadata := map[string]any{}
	if raw, ok := row["metadata"].(map[string]any); ok {
		for key, value := range raw {
			if cleanText(key) != "" && metadataValuePresent(value) {
				metadata[cleanText(key)] = value
			}
		}
	}
	skip := map[string]struct{}{
		"name": {}, "version": {}, "source": {}, "metadata": {}, "uninstall": {},
		"distribution_platform": {}, "distribution_app_id": {},
	}
	for key, value := range row {
		key = cleanText(key)
		if key == "" {
			continue
		}
		if _, ok := skip[key]; ok || !metadataValuePresent(value) {
			continue
		}
		if !metadataValuePresent(metadata[key]) {
			metadata[key] = value
		}
	}
	return metadata
}

func buildSoftwareRuleID(prefix string, softwareEntry map[string]any, includeVersion bool) string {
	tokens := []string{slugifySoftwareToken(prefix), slugifySoftwareToken(firstText(cleanText(softwareEntry["name"]), "software"))}
	if includeVersion {
		if version := slugifySoftwareToken(softwareEntry["version"]); version != "" {
			tokens = append(tokens, version)
		}
	}
	clean := []string{}
	for _, token := range tokens {
		if token != "" {
			clean = append(clean, token)
		}
	}
	return strings.Join(clean, "_")
}

func buildSoftwareRuleMatchBase(prefix string, softwareEntry map[string]any) map[string]any {
	metadata := softwareMetadata(softwareEntry)
	rule := map[string]any{
		"rule_id": buildSoftwareRuleID(prefix, softwareEntry, true),
		"source":  normalizeSoftwareSource(softwareEntry["source"]),
		"name":    cleanText(softwareEntry["name"]),
	}
	if version := cleanText(softwareEntry["version"]); version != "" {
		rule["version"] = version
	}
	if publisher := cleanText(metadata["publisher"]); publisher != "" {
		rule["publisher_contains_any"] = []any{publisher}
	}
	if productCode := cleanText(metadata["product_code"]); productCode != "" {
		rule["product_code"] = productCode
	}
	if installLocation := trimWindowsPath(metadata["install_location"]); installLocation != "" {
		rule["install_location_contains_any"] = []any{installLocation}
	}
	return rule
}

func slugifySoftwareToken(value any) string {
	text := strings.ToLower(cleanText(value))
	text = softwareSlugSeparatorPattern.ReplaceAllString(text, "_")
	return strings.Trim(text, "_")
}

func canonicalizeSoftwareIconOverrideResource(value any) string {
	text := cleanText(value)
	if text == "" {
		return ""
	}
	lowered := strings.ToLower(text)
	if strings.HasSuffix(lowered, ".ico") || softwareICOResourcePattern.MatchString(text) {
		return strings.Trim(strings.TrimSpace(text), `"`)
	}
	match := softwareIconResourcePattern.FindStringSubmatch(text)
	if match == nil {
		return ""
	}
	path := strings.Trim(cleanText(match[1]), `"`)
	if path == "" {
		return ""
	}
	index := int64(0)
	if len(match) > 2 && cleanText(match[2]) != "" {
		index = coerceInt64(match[2])
	}
	return fmt.Sprintf("%s,%d", path, index)
}

func upsertSoftwareIconOverride(rule map[string]any) (map[string]any, error) {
	ruleID := cleanText(rule["rule_id"])
	name := cleanText(rule["name"])
	if ruleID == "" {
		return nil, errors.New("rule_id is required")
	}
	if name == "" {
		return nil, errors.New("name is required")
	}
	clearIcon := parseTruthy(cleanText(firstNonEmpty(rule["clear_icon"], rule["remove_icon"])))
	displayIcon := ""
	if !clearIcon {
		displayIcon = canonicalizeSoftwareIconOverrideResource(firstNonEmpty(rule["display_icon"], rule["icon_location"]))
	}
	if !clearIcon && displayIcon == "" {
		return nil, errors.New("display_icon must be a valid EXE, DLL, ICO, or icon resource path")
	}
	normalized := map[string]any{"rule_id": ruleID, "name": name}
	if clearIcon {
		normalized["clear_icon"] = true
	} else {
		normalized["display_icon"] = displayIcon
	}
	softwareOverrideWriteLock.Lock()
	defer softwareOverrideWriteLock.Unlock()

	payload := loadSoftwareRuleFile(agentSoftwareIconOverridesPath(), "windows_icon_overrides")
	rows := upsertRuleByIDOrName(payload["windows_icon_overrides"], normalized, ruleID, name)
	if err := writeSoftwareRuleFile(agentSoftwareIconOverridesPath(), map[string]any{"windows_icon_overrides": rows}); err != nil {
		return nil, err
	}
	return normalized, nil
}

func upsertUninstallOverride(rule map[string]any) (map[string]any, error) {
	ruleID := cleanText(rule["rule_id"])
	if ruleID == "" {
		return nil, errors.New("rule_id is required")
	}
	strategy := strings.ToLower(firstText(cleanText(rule["strategy"]), "direct_command"))
	normalized := map[string]any{"rule_id": ruleID, "strategy": strategy}
	switch strategy {
	case "direct_command":
		quiet := canonicalizeWindowsCommand(rule["quiet_uninstall_string"], nil)
		if quiet == "" {
			return nil, errors.New("quiet_uninstall_string is required for direct_command uninstall overrides")
		}
		normalized["quiet_uninstall_string"] = quiet
	case "msi_product_code":
		productCode := strings.ToUpper(cleanText(rule["product_code"]))
		if !softwareWindowsProductCodeStrict.MatchString(productCode) {
			return nil, errors.New("product_code must be a valid Windows MSI product code")
		}
		normalized["product_code"] = productCode
	case "windows_store":
		packageFamily := cleanText(rule["package_family_name"])
		if packageFamily == "" {
			return nil, errors.New("package_family_name is required for windows_store uninstall overrides")
		}
		normalized["package_family_name"] = packageFamily
	default:
		return nil, fmt.Errorf("Unsupported uninstall override strategy '%s'", strategy)
	}
	for _, key := range []string{"source", "name", "version", "product_code", "package_family_name", "summary"} {
		if value := cleanText(rule[key]); value != "" {
			if _, ok := normalized[key]; !ok {
				normalized[key] = value
			}
		}
	}
	if value := cleanText(rule["uninstall_string"]); value != "" {
		normalized["uninstall_string"] = value
	}
	for _, key := range []string{"publisher_contains_any", "name_contains_any", "install_location_contains_any", "exe_names", "uninstall_contains_any"} {
		if values := cleanStringList(rule[key]); len(values) > 0 {
			normalized[key] = values
		}
	}
	softwareOverrideWriteLock.Lock()
	defer softwareOverrideWriteLock.Unlock()
	path := softwareUninstallOverridesPath()
	payload := loadSoftwareRuleFile(path, "windows_uninstall_overrides")
	rows := upsertRuleByID(payload["windows_uninstall_overrides"], normalized, ruleID)
	if err := writeSoftwareRuleFile(path, map[string]any{"windows_uninstall_overrides": rows}); err != nil {
		return nil, err
	}
	return normalized, nil
}

func upsertUninstallBlocklistRule(rule map[string]any) (map[string]any, error) {
	ruleID := cleanText(rule["rule_id"])
	reason := cleanText(rule["reason"])
	if ruleID == "" {
		return nil, errors.New("rule_id is required")
	}
	if reason == "" {
		return nil, errors.New("reason is required")
	}
	normalized := map[string]any{"rule_id": ruleID, "reason": reason}
	for _, key := range []string{"source", "name", "version", "product_code"} {
		if value := cleanText(rule[key]); value != "" {
			normalized[key] = value
		}
	}
	for _, key := range []string{"publisher_contains_any", "name_contains_any", "install_location_contains_any", "exe_names", "quiet_args_any", "uninstall_contains_any"} {
		if values := cleanStringList(rule[key]); len(values) > 0 {
			normalized[key] = values
		}
	}
	softwareOverrideWriteLock.Lock()
	defer softwareOverrideWriteLock.Unlock()
	path := softwareUninstallBlocklistPath()
	payload := loadSoftwareRuleFile(path, "windows_quiet_uninstall_blocklist")
	rows := upsertRuleByID(payload["windows_quiet_uninstall_blocklist"], normalized, ruleID)
	if err := writeSoftwareRuleFile(path, map[string]any{"windows_quiet_uninstall_blocklist": rows}); err != nil {
		return nil, err
	}
	return normalized, nil
}

func removeUninstallBlocklistRule(ruleID string) bool {
	ruleID = cleanText(ruleID)
	if ruleID == "" {
		return false
	}
	softwareOverrideWriteLock.Lock()
	defer softwareOverrideWriteLock.Unlock()
	path := softwareUninstallBlocklistPath()
	payload := loadSoftwareRuleFile(path, "windows_quiet_uninstall_blocklist")
	rows := ruleRows(payload["windows_quiet_uninstall_blocklist"])
	next := []map[string]any{}
	for _, row := range rows {
		if cleanText(row["rule_id"]) != ruleID {
			next = append(next, row)
		}
	}
	if len(next) == len(rows) {
		return false
	}
	_ = writeSoftwareRuleFile(path, map[string]any{"windows_quiet_uninstall_blocklist": next})
	return true
}

func findMatchingUninstallBlocklistRules(entry map[string]any) []map[string]any {
	metadata := softwareMetadata(entry)
	quiet := cleanText(metadata["quiet_uninstall_string"])
	uninstall := cleanText(metadata["uninstall_string"])
	parsed := splitWindowsCommandLine(firstText(quiet, uninstall))
	executableName := strings.ToLower(cleanText(parsed["executable_name"]))
	arguments := cleanText(parsed["arguments"])
	uninstallText := firstText(quiet, uninstall)
	payload := loadSoftwareRuleFile(softwareUninstallBlocklistPath(), "windows_quiet_uninstall_blocklist")
	matches := []map[string]any{}
	for _, rule := range ruleRows(payload["windows_quiet_uninstall_blocklist"]) {
		if softwareMatchesRule(entry, rule, executableName, uninstallText, arguments) {
			matches = append(matches, rule)
		}
	}
	return matches
}

func softwareMatchesRule(entry map[string]any, rule map[string]any, executableName string, uninstallText string, quietArguments string) bool {
	metadata := softwareMetadata(entry)
	source := normalizeSoftwareSource(entry["source"])
	name := cleanText(entry["name"])
	version := cleanText(entry["version"])
	publisher := cleanText(metadata["publisher"])
	installLocation := trimWindowsPath(metadata["install_location"])
	productCode := cleanText(metadata["product_code"])
	exeName := strings.ToLower(cleanText(executableName))
	uninstallLower := strings.ToLower(cleanText(uninstallText))
	expectedSource := normalizeSoftwareSource(rule["source"])
	if cleanText(rule["source"]) != "" && expectedSource != source {
		return false
	}
	if exact := cleanText(rule["name"]); exact != "" && !strings.EqualFold(exact, name) {
		return false
	}
	if exact := cleanText(rule["version"]); exact != "" && !strings.EqualFold(exact, version) {
		return false
	}
	if exact := cleanText(rule["product_code"]); exact != "" && !strings.EqualFold(exact, productCode) {
		return false
	}
	if values := lowerStringList(rule["publisher_contains_any"]); len(values) > 0 && !containsAny(strings.ToLower(publisher), values) {
		return false
	}
	if values := lowerStringList(rule["name_contains_any"]); len(values) > 0 && !containsAny(strings.ToLower(name), values) {
		return false
	}
	if values := lowerStringList(rule["install_location_contains_any"]); len(values) > 0 && !containsAny(strings.ToLower(installLocation), values) {
		return false
	}
	if values := lowerStringList(rule["exe_names"]); len(values) > 0 && !stringSliceContains(values, exeName) {
		return false
	}
	if values := lowerStringList(rule["uninstall_contains_any"]); len(values) > 0 && !containsAny(uninstallLower, values) {
		return false
	}
	if values := cleanStringList(rule["quiet_args_any"]); len(values) > 0 {
		matched := false
		for _, value := range values {
			if optionPresent(quietArguments, cleanText(value)) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func loadSoftwareRuleFile(path string, key string) map[string]any {
	content, err := os.ReadFile(path)
	if err != nil {
		return map[string]any{key: []map[string]any{}}
	}
	var payload map[string]any
	if err := json.Unmarshal(content, &payload); err != nil {
		return map[string]any{key: []map[string]any{}}
	}
	if _, ok := payload[key]; !ok {
		payload[key] = []map[string]any{}
	}
	return payload
}

func writeSoftwareRuleFile(path string, payload map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, content, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func softwareUninstallOverridesPath() string {
	if value := strings.TrimSpace(os.Getenv("BOREALIS_SOFTWARE_UNINSTALL_OVERRIDES_PATH")); value != "" {
		return value
	}
	return filepath.Join(projectRootPath(), "Data", "Engine", "Containers", "api-backend", "data", "services", "API", "devices", "software_uninstall_overrides.json")
}

func softwareUninstallBlocklistPath() string {
	if value := strings.TrimSpace(os.Getenv("BOREALIS_SOFTWARE_UNINSTALL_BLOCKLIST_PATH")); value != "" {
		return value
	}
	return filepath.Join(projectRootPath(), "Data", "Engine", "Containers", "api-backend", "data", "services", "API", "devices", "software_uninstall_blocklist.json")
}

func projectRootPath() string {
	root := strings.TrimSpace(os.Getenv("BOREALIS_PROJECT_ROOT"))
	if root == "" {
		root = "/opt/Borealis"
	}
	return root
}

func ruleRows(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		return append([]map[string]any(nil), typed...)
	case []any:
		rows := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			row, ok := item.(map[string]any)
			if ok {
				rows = append(rows, row)
			}
		}
		return rows
	default:
		return []map[string]any{}
	}
}

func upsertRuleByID(value any, normalized map[string]any, ruleID string) []map[string]any {
	rows := ruleRows(value)
	next := []map[string]any{}
	replaced := false
	for _, existing := range rows {
		if cleanText(existing["rule_id"]) == ruleID {
			next = append(next, normalized)
			replaced = true
		} else {
			next = append(next, existing)
		}
	}
	if !replaced {
		next = append(next, normalized)
	}
	return next
}

func upsertRuleByIDOrName(value any, normalized map[string]any, ruleID string, name string) []map[string]any {
	rows := ruleRows(value)
	next := []map[string]any{}
	replaced := false
	lowerName := strings.ToLower(name)
	for _, existing := range rows {
		existingName := strings.ToLower(cleanText(existing["name"]))
		if cleanText(existing["rule_id"]) == ruleID || (existingName != "" && existingName == lowerName) {
			next = append(next, normalized)
			replaced = true
		} else {
			next = append(next, existing)
		}
	}
	if !replaced {
		next = append(next, normalized)
	}
	return next
}

func buildWindowsCommand(filePath string, arguments string, extraArgs []string) string {
	parts := []string{quoteWindowsToken(filePath)}
	if cleanText(arguments) != "" {
		parts = append(parts, cleanText(arguments))
	}
	for _, arg := range extraArgs {
		if cleanText(arg) != "" {
			parts = append(parts, quoteWindowsToken(arg))
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func quoteWindowsToken(value string) string {
	text := cleanText(value)
	if text == "" {
		return ""
	}
	if strings.HasPrefix(text, `"`) && strings.HasSuffix(text, `"`) {
		return text
	}
	if strings.ContainsAny(text, " \t\r\n") {
		return `"` + text + `"`
	}
	return text
}

func canonicalizeWindowsCommand(commandLine any, extraArgs []string) string {
	parsed := splitWindowsCommandLine(commandLine)
	if len(parsed) == 0 {
		return cleanText(commandLine)
	}
	return buildWindowsCommand(parsed["file_path"], parsed["arguments"], extraArgs)
}

func splitWindowsCommandLine(commandLine any) map[string]string {
	command := cleanText(commandLine)
	if command == "" {
		return map[string]string{}
	}
	if match := softwareQuotedCommandPattern.FindStringSubmatch(command); match != nil {
		filePath := cleanText(match[1])
		return map[string]string{"file_path": filePath, "arguments": cleanText(match[2]), "executable_name": windowsBase(filePath)}
	}
	if match := softwareCommandWithExtPattern.FindStringSubmatch(command); match != nil {
		filePath := cleanText(match[1])
		return map[string]string{"file_path": filePath, "arguments": cleanText(match[2]), "executable_name": windowsBase(filePath)}
	}
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return map[string]string{}
	}
	arguments := ""
	if len(parts) > 1 {
		arguments = strings.TrimSpace(strings.TrimPrefix(command, parts[0]))
	}
	return map[string]string{"file_path": parts[0], "arguments": arguments, "executable_name": windowsBase(parts[0])}
}

func extractQuietArgumentTokens(argumentsText string) []string {
	tokens := []string{}
	for _, raw := range strings.Fields(cleanText(argumentsText)) {
		token := cleanText(raw)
		if token != "" && softwareQuietArgPattern.MatchString(token) {
			tokens = append(tokens, strings.ToLower(token))
		}
	}
	return tokens
}

func optionPresent(argumentsText string, option string) bool {
	option = cleanText(option)
	if option == "" {
		return false
	}
	for _, token := range strings.Fields(cleanText(argumentsText)) {
		if strings.EqualFold(token, option) {
			return true
		}
	}
	return false
}

func trimWindowsPath(value any) string {
	return strings.TrimRight(cleanText(value), `\/`)
}

func windowsBase(path string) string {
	text := strings.TrimRight(cleanText(path), `\/`)
	text = strings.ReplaceAll(text, "/", `\`)
	parts := strings.Split(text, `\`)
	if len(parts) == 0 {
		return text
	}
	return parts[len(parts)-1]
}

func cleanStringList(value any) []any {
	rows := []any{}
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if text := cleanText(item); text != "" {
				rows = append(rows, text)
			}
		}
	case []string:
		for _, item := range typed {
			if text := cleanText(item); text != "" {
				rows = append(rows, text)
			}
		}
	}
	return rows
}

func lowerStringList(value any) []string {
	raw := cleanStringList(value)
	rows := make([]string, 0, len(raw))
	for _, item := range raw {
		rows = append(rows, strings.ToLower(cleanText(item)))
	}
	return rows
}

func containsAny(value string, candidates []string) bool {
	for _, candidate := range candidates {
		if candidate != "" && strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func stringSliceContains(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

var _ softwareOverrideStore = (*postgresOperatorStore)(nil)
