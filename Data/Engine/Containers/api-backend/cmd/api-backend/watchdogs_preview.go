package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const watchdogPreviewTelemetryStaleSeconds = 900

var watchdogPreviewValidRuleTypes = map[string]struct{}{
	"device_offline":               {},
	"storage_usage_percent":        {},
	"service_state":                {},
	"agent_role_health":            {},
	"software_presence_or_version": {},
	"agent_version_status":         {},
	"cpu_usage_percent":            {},
	"memory_usage_percent":         {},
	"uptime_above_seconds":         {},
	"reboot_detected":              {},
	"service_pending_timeout":      {},
	"user_session_match":           {},
	"process_presence":             {},
	"session_state":                {},
	"network_interface_change":     {},
	"drive_presence_change":        {},
}

func (s *postgresOperatorStore) previewWatchdog(ctx context.Context, profile operatorProfile, body map[string]any) (map[string]any, []string, error) {
	record := normalizeWatchdogPreviewRecord(body, firstText(cleanText(profile.Username), "Unknown"))
	validationErrors, err := s.validateWatchdogPreviewRecord(ctx, profile, record)
	if err != nil || len(validationErrors) > 0 {
		return nil, validationErrors, err
	}

	devices, err := s.listDevices(ctx, profile, deviceListFilter{})
	if err != nil {
		return nil, nil, err
	}
	for _, device := range devices {
		attachAgentVersionStatus(device)
	}
	targets, err := s.resolveWatchdogPreviewTargets(ctx, profile, record, devices)
	if err != nil {
		return nil, nil, err
	}

	var overrides map[string]map[string]any
	var currentState map[string]map[string]any
	if watchdogID := watchdogPreviewRecordID(record); watchdogID > 0 {
		overrides, err = s.loadWatchdogPreviewOverrides(ctx, watchdogID)
		if err != nil {
			return nil, nil, err
		}
		currentState, err = s.loadWatchdogPreviewStates(ctx, watchdogID)
		if err != nil {
			return nil, nil, err
		}
	} else {
		overrides = map[string]map[string]any{}
		currentState = map[string]map[string]any{}
	}

	results := make([]map[string]any, 0, len(targets))
	var matchedCount int64
	for _, device := range targets {
		hostname := cleanText(device["hostname"])
		key := strings.ToLower(hostname)
		override := overrides[key]
		evaluation := evaluateWatchdogPreviewDevice(record, device, currentState[key])
		if override != nil {
			evaluation["state"] = firstText(strings.ToLower(cleanText(override["state"])), "suppressed")
			evaluation["message"] = firstText(cleanText(override["reason"]), "Watchdog is overridden for this device.")
			evaluation["matched"] = false
		}
		if boolDefault(evaluation["matched"], false) {
			matchedCount++
		}
		result := map[string]any{
			"device_guid":  normalizeCanonicalGUID(firstPresentAny(device["guid"], device["agent_guid"], device["device_guid"])),
			"hostname":     hostname,
			"site_id":      watchdogPreviewNullableInt64(device["site_id"]),
			"site_name":    cleanText(device["site_name"]),
			"status":       firstText(cleanText(device["status"]), "Offline"),
			"state":        firstText(strings.ToLower(cleanText(evaluation["state"])), "normal"),
			"matched":      boolDefault(evaluation["matched"], false),
			"message":      cleanText(evaluation["message"]),
			"sample":       asStringAnyMap(evaluation["sample"]),
			"rule_results": anySlice(evaluation["rule_results"]),
			"override":     nullableMap(override),
		}
		results = append(results, result)
	}
	return map[string]any{
		"devices":       results,
		"device_count":  int64(len(results)),
		"matched_count": matchedCount,
	}, nil, nil
}

func normalizeWatchdogPreviewRecord(payload map[string]any, username string) map[string]any {
	criteriaPayload := asStringAnyMap(payload["criteria"])
	rulesInput := criteriaPayload["rules"]
	if rulesInput == nil {
		rulesInput = payload["rules"]
	}
	rules := make([]any, 0, len(anySlice(rulesInput)))
	for index, raw := range anySlice(rulesInput) {
		rule := normalizeWatchdogPreviewRule(index, asStringAnyMap(raw))
		if rule != nil {
			rules = append(rules, rule)
		}
		if len(rules) >= 24 {
			break
		}
	}
	criteria := map[string]any{
		"match_mode": normalizeWatchdogMatchMode(cleanText(firstNonEmpty(criteriaPayload["match_mode"], payload["match_mode"]))),
		"rules":      rules,
	}
	recordID := watchdogPreviewOptionalInt(firstNonEmpty(payload["id"], payload["watchdog_id"]))
	name := cleanSingleLine(cleanText(payload["name"]))
	if name == "" {
		name = "Unnamed Watchdog"
	}
	return map[string]any{
		"id":                          recordID,
		"name":                        name,
		"description":                 cleanSingleLine(cleanText(payload["description"])),
		"archived":                    boolDefault(payload["archived"], false),
		"enabled":                     boolDefault(payload["enabled"], true),
		"severity":                    normalizeWatchdogSeverity(cleanText(payload["severity"])),
		"site_mode":                   normalizeWatchdogSiteMode(cleanText(payload["site_mode"])),
		"site_ids":                    watchdogPreviewSiteIDs(firstNonEmpty(payload["site_ids"], payload["sites"], payload["site_scope_values"])),
		"criteria":                    criteria,
		"match_mode":                  criteria["match_mode"],
		"targets":                     normalizeScheduledTargetsForSave(payload["targets"]),
		"evaluation_interval_seconds": maxInt(fallbackInt(payload["evaluation_interval_seconds"], watchdogDefaultEvalSeconds), 30),
		"cooldown_seconds":            maxInt(int(coerceInt64(payload["cooldown_seconds"])), 0),
		"auto_resolve_after_seconds":  maxInt(int(coerceInt64(payload["auto_resolve_after_seconds"])), 0),
		"min_consecutive_matches":     maxInt(fallbackInt(payload["min_consecutive_matches"], watchdogDefaultMinMatches), 1),
		"boot_grace_seconds":          maxInt(int(coerceInt64(payload["boot_grace_seconds"])), 0),
		"last_edited_by":              firstText(username, "Unknown"),
	}
}

func normalizeWatchdogPreviewRule(index int, raw map[string]any) map[string]any {
	ruleType := strings.ToLower(cleanText(raw["type"]))
	if _, ok := watchdogPreviewValidRuleTypes[ruleType]; !ok {
		return nil
	}
	base := map[string]any{
		"id":   firstText(cleanText(raw["id"]), fmt.Sprintf("rule-%d", index+1)),
		"type": ruleType,
	}
	switch ruleType {
	case "device_offline":
		base["offline_after_seconds"] = maxInt(fallbackInt(raw["offline_after_seconds"], watchdogDefaultOfflineSeconds), 60)
	case "storage_usage_percent":
		threshold := watchdogPreviewFloat(raw["threshold"], 90)
		base["threshold"] = clampFloat(threshold, 1, 100)
		base["drive"] = cleanText(raw["drive"])
		base["drive_mode"] = watchdogPreviewStorageDriveMode(raw["drive_mode"], raw["drive"])
	case "service_state":
		serviceName := cleanSingleLine(firstText(cleanText(raw["service_name"]), cleanText(raw["name"])))
		if serviceName == "" {
			return nil
		}
		expected := strings.ToLower(cleanText(firstNonEmpty(raw["expected_status"], "running")))
		if expected != "running" && expected != "stopped" {
			expected = "running"
		}
		base["service_name"] = serviceName
		base["expected_status"] = expected
	case "agent_role_health":
		base["role_name"] = cleanSingleLine(firstText(cleanText(raw["role_name"]), cleanText(raw["role"])))
		base["trigger_statuses"] = watchdogPreviewChoiceList(firstNonEmpty(raw["trigger_statuses"], raw["statuses"], []any{"unhealthy"}), map[string]struct{}{"healthy": {}, "recovering": {}, "unhealthy": {}, "pending": {}, "unsupported": {}, "unknown": {}}, []string{"unhealthy"})
		base["min_duration_seconds"] = maxInt(int(coerceInt64(raw["min_duration_seconds"])), 0)
	case "cpu_usage_percent", "memory_usage_percent":
		base["threshold"] = clampFloat(watchdogPreviewFloat(raw["threshold"], watchdogDefaultResourceThreshold), 1, 100)
		base["duration_seconds"] = maxInt(fallbackInt(raw["duration_seconds"], watchdogDefaultResourceDuration), 0)
	case "uptime_above_seconds":
		base["threshold_seconds"] = maxInt(fallbackInt(raw["threshold_seconds"], watchdogDefaultUptimeSeconds), 60)
	case "reboot_detected":
	case "service_pending_timeout":
		action := strings.ToLower(cleanText(firstNonEmpty(raw["pending_action"], raw["action_filter"])))
		if action != "start" && action != "stop" && action != "restart" {
			action = ""
		}
		base["service_name"] = cleanSingleLine(firstText(cleanText(raw["service_name"]), cleanText(raw["name"])))
		base["pending_action"] = action
		base["timeout_seconds"] = maxInt(fallbackInt(raw["timeout_seconds"], watchdogDefaultServicePending), 0)
	case "user_session_match":
		patterns := watchdogPreviewStringList(firstNonEmpty(raw["patterns"], raw["user_patterns"]))
		if len(patterns) == 0 {
			return nil
		}
		mode := strings.ToLower(firstText(cleanText(firstNonEmpty(raw["match_mode"], raw["mode"])), "blocklist"))
		if mode != "allowlist" && mode != "blocklist" {
			mode = "blocklist"
		}
		patternMode := strings.ToLower(firstText(cleanText(raw["pattern_mode"]), "normalized"))
		if patternMode != "normalized" && patternMode != "wildcard" && patternMode != "regex" {
			patternMode = "normalized"
		}
		base["match_mode"] = mode
		base["pattern_mode"] = patternMode
		base["patterns"] = patterns
	case "process_presence":
		processName := cleanSingleLine(firstText(cleanText(raw["process_name"]), cleanText(raw["name"])))
		if processName == "" {
			return nil
		}
		expectation := strings.ToLower(firstText(cleanText(firstNonEmpty(raw["expectation"], raw["presence"])), "present"))
		if expectation != "present" && expectation != "missing" {
			expectation = "present"
		}
		base["process_name"] = processName
		base["expectation"] = expectation
	case "session_state":
		mode := strings.ToLower(firstText(cleanText(firstNonEmpty(raw["session_mode"], raw["mode"])), "current"))
		if mode != "current" && mode != "transition" {
			mode = "current"
		}
		base["session_mode"] = mode
		base["rdp_only"] = boolDefault(raw["rdp_only"], false)
		base["states"] = watchdogPreviewChoiceList(firstNonEmpty(raw["states"], raw["trigger_states"], []any{"active"}), map[string]struct{}{"active": {}, "locked": {}, "disconnected": {}, "idle": {}, "unknown": {}}, []string{"active"})
		base["events"] = watchdogPreviewChoiceList(firstNonEmpty(raw["events"], raw["trigger_events"], []any{"started"}), map[string]struct{}{"started": {}, "ended": {}, "locked": {}, "unlocked": {}, "rdp_started": {}, "rdp_ended": {}}, []string{"started"})
	case "network_interface_change":
		base["change_types"] = watchdogPreviewChoiceList(firstNonEmpty(raw["change_types"], []any{"added", "removed", "mac_changed"}), map[string]struct{}{"added": {}, "removed": {}, "mac_changed": {}}, []string{"added", "removed", "mac_changed"})
	case "drive_presence_change":
		scope := strings.ToLower(firstText(cleanText(raw["storage_scope"]), "all"))
		if scope != "all" && scope != "fixed" && scope != "removable" {
			scope = "all"
		}
		mode := strings.ToLower(firstText(cleanText(raw["watch_mode"]), "any"))
		if mode != "any" && mode != "specific" {
			mode = "any"
		}
		base["storage_scope"] = scope
		base["watch_mode"] = mode
		base["change_types"] = watchdogPreviewChoiceList(firstNonEmpty(raw["change_types"], []any{"added", "removed"}), map[string]struct{}{"added": {}, "removed": {}}, []string{"added", "removed"})
		base["drive_list"] = watchdogPreviewStringList(firstNonEmpty(raw["drive_list"], raw["drives"]))
	case "software_presence_or_version":
		softwareName := cleanSingleLine(firstText(cleanText(raw["software_name"]), cleanText(raw["name"])))
		if softwareName == "" {
			return nil
		}
		operator := strings.ToLower(cleanText(raw["version_operator"]))
		if operator != "matches" && operator != "older_than" && operator != "newer_than" {
			operator = ""
		}
		base["software_name"] = softwareName
		base["software_source"] = strings.ToLower(firstText(cleanText(raw["software_source"]), cleanText(raw["source"])))
		base["require_present"] = boolDefault(raw["require_present"], true)
		base["version_operator"] = operator
		base["version_value"] = cleanSingleLine(firstText(cleanText(raw["version_value"]), cleanText(raw["version"])))
	case "agent_version_status":
		expected := firstText(cleanText(raw["expected_status"]), "Up-to-Date")
		if expected != "Up-to-Date" && expected != "Needs Updated" {
			expected = "Up-to-Date"
		}
		base["expected_status"] = expected
	}
	return base
}

func (s *postgresOperatorStore) validateWatchdogPreviewRecord(ctx context.Context, profile operatorProfile, record map[string]any) ([]string, error) {
	errorsOut := []string{}
	rules := anySlice(asStringAnyMap(record["criteria"])["rules"])
	if len(rules) == 0 {
		errorsOut = append(errorsOut, "At least one watchdog rule is required.")
	}
	for _, rawRule := range rules {
		rule := asStringAnyMap(rawRule)
		switch strings.ToLower(cleanText(rule["type"])) {
		case "storage_usage_percent":
			if watchdogPreviewStorageDriveMode(rule["drive_mode"], rule["drive"]) == "specific" && cleanText(rule["drive"]) == "" {
				errorsOut = append(errorsOut, "Storage usage rules using Specific Drive must include a drive letter or mount path.")
			}
		case "drive_presence_change":
			if strings.EqualFold(cleanText(firstNonEmpty(rule["watch_mode"], "any")), "specific") && len(watchdogPreviewStringList(rule["drive_list"])) == 0 {
				errorsOut = append(errorsOut, "Drive presence rules using Specific Drives must include at least one expected drive.")
			}
		}
	}
	targets := anySlice(record["targets"])
	if len(targets) == 0 {
		errorsOut = append(errorsOut, "At least one target device or filter is required.")
	}

	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, err
	}
	if allowedSiteIDs == nil {
		return errorsOut, nil
	}
	if len(allowedSiteIDs) == 0 {
		errorsOut = append(errorsOut, "You do not have any assigned sites available for watchdog targeting.")
		return errorsOut, nil
	}
	allowed := int64Set(allowedSiteIDs)
	for _, siteID := range coerceInt64Slice(record["site_ids"]) {
		if _, ok := allowed[siteID]; !ok {
			errorsOut = append(errorsOut, "One or more selected scope sites is outside your assigned site scope.")
			break
		}
	}
	filterIDs := watchdogPreviewFilterIDs(targets)
	if len(filterIDs) > 0 {
		filterRecords, err := s.loadDeviceFilters(ctx, filterIDs, false)
		if err != nil {
			return nil, err
		}
		allSiteIDs, err := s.allSiteIDs(ctx)
		if err != nil {
			return nil, err
		}
		for _, filterID := range filterIDs {
			filterRecord := filterRecords[filterID]
			visible := false
			if filterRecord != nil {
				visible, err = s.filterRecordVisibleToProfileWithAll(ctx, profile, filterRecord, allSiteIDs)
				if err != nil {
					return nil, err
				}
			}
			if !visible {
				errorsOut = append(errorsOut, fmt.Sprintf("Filter %d is outside your assigned site scope.", filterID))
			}
		}
	}
	return errorsOut, nil
}

func (s *postgresOperatorStore) resolveWatchdogPreviewTargets(ctx context.Context, profile operatorProfile, record map[string]any, devices []map[string]any) ([]map[string]any, error) {
	allSiteIDs, err := s.allSiteIDs(ctx)
	if err != nil {
		return nil, err
	}
	allSiteSet := int64Set(allSiteIDs)
	effectiveSites := effectiveWatchdogSiteIDs(record, allSiteSet)
	scopedDevices := make([]map[string]any, 0, len(devices))
	for _, device := range devices {
		siteID := int64FromAny(device["site_id"])
		if siteID == nil {
			continue
		}
		if _, ok := effectiveSites[*siteID]; ok {
			scopedDevices = append(scopedDevices, device)
		}
	}

	targets := anySlice(record["targets"])
	for _, rawTarget := range targets {
		target := asStringAnyMap(rawTarget)
		kind := strings.ToLower(firstText(cleanText(target["kind"]), cleanText(target["type"])))
		if kind == "all_devices" || boolDefault(target["all_devices"], false) {
			return sortWatchdogPreviewDevices(scopedDevices), nil
		}
	}

	filterIDs := watchdogPreviewFilterIDs(targets)
	filterRecords := map[int64]map[string]any{}
	if len(filterIDs) > 0 {
		filterRecords, err = s.loadDeviceFilters(ctx, filterIDs, false)
		if err != nil {
			return nil, err
		}
	}
	matches := map[string]map[string]any{}
	for _, rawTarget := range targets {
		target := asStringAnyMap(rawTarget)
		if len(target) > 0 && watchdogTargetFilterID(target) > 0 {
			continue
		}
		targetGUID := normalizeCanonicalGUID(firstPresentAny(target["device_guid"], target["guid"]))
		targetHostname := strings.ToLower(cleanText(target["hostname"]))
		if len(target) == 0 {
			targetHostname = strings.ToLower(cleanText(rawTarget))
		}
		for _, device := range scopedDevices {
			deviceGUID := normalizeCanonicalGUID(firstPresentAny(device["guid"], device["agent_guid"], device["device_guid"]))
			deviceHostname := strings.ToLower(cleanText(device["hostname"]))
			if targetGUID != "" && deviceGUID != "" && strings.EqualFold(targetGUID, deviceGUID) {
				matches[firstText(deviceHostname, deviceGUID)] = device
				continue
			}
			if targetHostname != "" && targetHostname == deviceHostname {
				matches[deviceHostname] = device
			}
		}
	}
	for _, filterID := range filterIDs {
		filterRecord := filterRecords[filterID]
		if filterRecord == nil {
			continue
		}
		visible, err := s.filterRecordVisibleToProfileWithAll(ctx, profile, filterRecord, allSiteIDs)
		if err != nil {
			return nil, err
		}
		if !visible {
			continue
		}
		for _, device := range matchFilterDevices(filterRecord, scopedDevices) {
			hostname := strings.ToLower(cleanText(device["hostname"]))
			if hostname != "" {
				matches[hostname] = device
			}
		}
	}
	resolved := make([]map[string]any, 0, len(matches))
	for _, device := range matches {
		resolved = append(resolved, device)
	}
	return sortWatchdogPreviewDevices(resolved), nil
}

func evaluateWatchdogPreviewDevice(record map[string]any, device map[string]any, priorState map[string]any) map[string]any {
	now := time.Now().Unix()
	uptime := coerceInt64(device["uptime"])
	bootGrace := coerceInt64(record["boot_grace_seconds"])
	if uptime > 0 && uptime < bootGrace {
		return map[string]any{
			"matched": false,
			"state":   "pending",
			"message": "Boot grace period is still active.",
			"sample": map[string]any{
				"uptime_seconds":     uptime,
				"boot_grace_seconds": bootGrace,
			},
			"rule_results": []any{},
		}
	}
	criteria := asStringAnyMap(record["criteria"])
	rules := anySlice(criteria["rules"])
	ruleResults := make([]any, 0, len(rules))
	for _, rawRule := range rules {
		rule := asStringAnyMap(rawRule)
		if len(rule) == 0 {
			continue
		}
		ruleResults = append(ruleResults, evaluateWatchdogPreviewRule(rule, device, now, watchdogPreviewPriorRuleResult(priorState, rule["id"])))
	}
	if len(ruleResults) == 0 {
		return map[string]any{
			"matched":      false,
			"state":        "normal",
			"message":      "No rules configured.",
			"sample":       map[string]any{},
			"rule_results": []any{},
		}
	}
	matchMode := normalizeWatchdogMatchMode(firstText(cleanText(criteria["match_mode"]), cleanText(record["match_mode"])))
	staleResults := []map[string]any{}
	matchedResults := []map[string]any{}
	typedResults := make([]map[string]any, 0, len(ruleResults))
	for _, raw := range ruleResults {
		result := asStringAnyMap(raw)
		typedResults = append(typedResults, result)
		if boolDefault(result["stale"], false) {
			staleResults = append(staleResults, result)
		}
		if boolDefault(result["matched"], false) {
			matchedResults = append(matchedResults, result)
		}
	}
	matched := false
	state := "normal"
	if matchMode == "all" {
		if len(staleResults) > 0 {
			state = "stale_data"
		} else {
			matched = len(matchedResults) == len(typedResults)
			if matched {
				state = "triggered"
			}
		}
	} else {
		matched = len(matchedResults) > 0
		if matched {
			state = "triggered"
		} else if len(staleResults) > 0 {
			state = "stale_data"
		}
	}
	primary := matchedResults
	if len(primary) == 0 {
		primary = staleResults
	}
	if len(primary) == 0 {
		primary = typedResults
	}
	messages := []string{}
	for _, result := range primary {
		if summary := cleanText(result["summary"]); summary != "" {
			messages = append(messages, summary)
		}
	}
	sampleResults := make([]any, 0, len(typedResults))
	for _, result := range typedResults {
		sampleResults = append(sampleResults, map[string]any{
			"rule_id": result["rule_id"],
			"type":    result["type"],
			"matched": boolDefault(result["matched"], false),
			"stale":   boolDefault(result["stale"], false),
			"summary": result["summary"],
			"sample":  asStringAnyMap(result["sample"]),
		})
	}
	sample := map[string]any{
		"match_mode": matchMode,
		"results":    sampleResults,
	}
	return map[string]any{
		"matched":      matched,
		"state":        state,
		"message":      strings.Join(messages, "; "),
		"sample":       sample,
		"rule_results": sampleResults,
	}
}

func evaluateWatchdogPreviewRule(rule map[string]any, device map[string]any, now int64, priorResult map[string]any) map[string]any {
	ruleType := strings.ToLower(cleanText(rule["type"]))
	switch ruleType {
	case "device_offline":
		offlineAfter := int64(fallbackInt(rule["offline_after_seconds"], watchdogDefaultOfflineSeconds))
		lastSeen := coerceInt64(device["last_seen"])
		age := offlineAfter
		if lastSeen > 0 {
			age = watchdogPreviewMaxInt64(0, now-lastSeen)
		}
		matched := lastSeen <= 0 || age >= offlineAfter
		summary := fmt.Sprintf("Device heartbeat age is %d seconds", age)
		if matched {
			summary = fmt.Sprintf("Device has not checked in for %d minute(s)", maxInt(int(age/60), 1))
		}
		return watchdogPreviewRuleResult(rule, matched, false, summary, map[string]any{"last_seen": lastSeen, "age_seconds": age, "offline_after_seconds": offlineAfter})
	case "storage_usage_percent":
		return watchdogPreviewEvaluateStorage(rule, device)
	case "service_state":
		payload := watchdogPreviewInventoryPayload(device, "services")
		if watchdogPreviewPayloadStale(payload, now) {
			return watchdogPreviewRuleResult(rule, false, true, "Service inventory is stale", map[string]any{})
		}
		serviceName := strings.ToLower(cleanText(rule["service_name"]))
		expected := strings.ToLower(firstText(cleanText(rule["expected_status"]), "running"))
		for _, entry := range watchdogPreviewMapEntries(payload["services"]) {
			if strings.ToLower(cleanText(entry["name"])) != serviceName {
				continue
			}
			actual := strings.ToLower(firstText(cleanText(entry["status_code"]), "unknown"))
			return watchdogPreviewRuleResult(rule, actual != expected, false, fmt.Sprintf("Service %s is %s (expected %s)", entry["name"], actual, expected), map[string]any{
				"service_name":    entry["name"],
				"actual_status":   actual,
				"expected_status": expected,
				"pending_action":  cleanText(entry["pending_action"]),
			})
		}
		return watchdogPreviewRuleResult(rule, true, false, fmt.Sprintf("Service %s is not present in the cached inventory", cleanText(rule["service_name"])), map[string]any{"service_name": cleanText(rule["service_name"]), "actual_status": "missing", "expected_status": expected})
	case "agent_role_health":
		return watchdogPreviewEvaluateRoleHealth(rule, device, now, priorResult)
	case "cpu_usage_percent":
		return watchdogPreviewEvaluateResource(rule, device, now, priorResult, "CPU", "cpu_percent")
	case "memory_usage_percent":
		return watchdogPreviewEvaluateResource(rule, device, now, priorResult, "Memory", "memory_percent")
	case "uptime_above_seconds":
		uptime := coerceInt64(device["uptime"])
		if uptime <= 0 {
			return watchdogPreviewRuleResult(rule, false, true, "Uptime telemetry is unavailable", map[string]any{})
		}
		threshold := int64(maxInt(fallbackInt(rule["threshold_seconds"], watchdogDefaultUptimeSeconds), 60))
		return watchdogPreviewRuleResult(rule, uptime >= threshold, false, fmt.Sprintf("Device uptime is %d second(s) (threshold %d)", uptime, threshold), map[string]any{"uptime_seconds": uptime, "threshold_seconds": threshold, "last_reboot": cleanText(device["last_reboot"])})
	case "reboot_detected":
		return watchdogPreviewEvaluateReboot(rule, device, priorResult)
	case "service_pending_timeout":
		return watchdogPreviewEvaluateServicePending(rule, device, now)
	case "user_session_match":
		return watchdogPreviewEvaluateUserSessions(rule, device, now)
	case "process_presence":
		return watchdogPreviewEvaluateProcess(rule, device, now)
	case "session_state":
		return watchdogPreviewEvaluateSessionState(rule, device, now, priorResult)
	case "network_interface_change":
		return watchdogPreviewEvaluateNetworkChange(rule, device, priorResult)
	case "drive_presence_change":
		return watchdogPreviewEvaluateDrivePresence(rule, device, priorResult)
	case "software_presence_or_version":
		return watchdogPreviewEvaluateSoftware(rule, device)
	case "agent_version_status":
		current := firstText(cleanText(device["agent_version_status"]), "Needs Updated")
		expected := firstText(cleanText(rule["expected_status"]), "Up-to-Date")
		return watchdogPreviewRuleResult(rule, current != expected, false, fmt.Sprintf("Agent version status is %s", current), map[string]any{"current_status": current, "expected_status": expected, "agent_hash": cleanText(device["agent_hash"])})
	default:
		return watchdogPreviewRuleResult(rule, false, true, "Unsupported rule", map[string]any{})
	}
}

func watchdogPreviewEvaluateStorage(rule map[string]any, device map[string]any) map[string]any {
	rows := watchdogPreviewStorageEntries(device["storage"])
	if len(rows) == 0 {
		return watchdogPreviewRuleResult(rule, false, true, "Storage inventory is not available", map[string]any{})
	}
	threshold := watchdogPreviewFloat(rule["threshold"], 90)
	driveMode := watchdogPreviewStorageDriveMode(rule["drive_mode"], rule["drive"])
	selectedDrive := cleanText(rule["drive"])
	selectedKey := watchdogPreviewStorageDriveKey(selectedDrive)
	evaluated := []map[string]any{}
	for _, row := range rows {
		if _, ok := float64Value(row["usage_percent"]); !ok {
			continue
		}
		if driveMode == "specific" && watchdogPreviewStorageDriveKey(row["drive"]) != selectedKey {
			continue
		}
		evaluated = append(evaluated, row)
	}
	if driveMode == "specific" && len(evaluated) == 0 {
		return watchdogPreviewRuleResult(rule, false, false, fmt.Sprintf("Drive %s is not present in storage inventory", firstText(selectedDrive, "target drive")), map[string]any{"drive_scope": "specific", "drive": selectedDrive, "present": false, "threshold": roundFloat(threshold, 2)})
	}
	if len(evaluated) == 0 {
		return watchdogPreviewRuleResult(rule, false, true, "Storage usage data is incomplete", map[string]any{})
	}
	sort.Slice(evaluated, func(i, j int) bool {
		left, _ := float64Value(evaluated[i]["usage_percent"])
		right, _ := float64Value(evaluated[j]["usage_percent"])
		return left > right
	})
	chosen := evaluated[0]
	usage, _ := float64Value(chosen["usage_percent"])
	matchedRows := []map[string]any{}
	for _, row := range evaluated {
		value, _ := float64Value(row["usage_percent"])
		if value >= threshold {
			matchedRows = append(matchedRows, row)
		}
	}
	if driveMode == "specific" {
		return watchdogPreviewRuleResult(rule, usage >= threshold, false, fmt.Sprintf("%s usage is %.1f%% (threshold %.1f%%)", chosen["drive"], usage, threshold), map[string]any{"drive_scope": "specific", "drive": chosen["drive"], "usage_percent": roundFloat(usage, 2), "threshold": roundFloat(threshold, 2), "present": true})
	}
	summary := fmt.Sprintf("Highest drive usage is %s at %.1f%% (threshold %.1f%%)", chosen["drive"], usage, threshold)
	if len(matchedRows) == 1 {
		value, _ := float64Value(matchedRows[0]["usage_percent"])
		summary = fmt.Sprintf("%s usage is %.1f%% (threshold %.1f%%)", matchedRows[0]["drive"], value, threshold)
	} else if len(matchedRows) > 1 {
		summary = fmt.Sprintf("%d drives are at or above %.1f%% usage", len(matchedRows), threshold)
	}
	matchedDriveSamples := make([]any, 0, len(matchedRows))
	for _, row := range matchedRows {
		value, _ := float64Value(row["usage_percent"])
		matchedDriveSamples = append(matchedDriveSamples, map[string]any{"drive": row["drive"], "usage_percent": roundFloat(value, 2)})
	}
	return watchdogPreviewRuleResult(rule, len(matchedRows) > 0, false, summary, map[string]any{"drive_scope": "all", "threshold": roundFloat(threshold, 2), "highest_drive": chosen["drive"], "highest_usage_percent": roundFloat(usage, 2), "matched_drives": matchedDriveSamples})
}

func watchdogPreviewEvaluateRoleHealth(rule map[string]any, device map[string]any, now int64, priorResult map[string]any) map[string]any {
	payload := asStringAnyMap(device["agent_role_health"])
	if watchdogPreviewPayloadStale(payload, now) {
		return watchdogPreviewRuleResult(rule, false, true, "Agent role health telemetry is stale", map[string]any{})
	}
	roleName := watchdogPreviewRoleMatchName(rule["role_name"])
	triggerStatuses := stringSetFromAny(rule["trigger_statuses"])
	if len(triggerStatuses) == 0 {
		triggerStatuses["unhealthy"] = struct{}{}
	}
	var matchedRole map[string]any
	var fallbackRole map[string]any
	for _, entry := range watchdogPreviewMapEntries(payload["roles"]) {
		candidateName := watchdogPreviewRoleMatchName(entry["role_name"])
		candidateLabel := strings.ToLower(cleanText(entry["role_label"]))
		if roleName != "" && roleName != candidateName && roleName != candidateLabel {
			continue
		}
		if fallbackRole == nil {
			fallbackRole = entry
		}
		if _, ok := triggerStatuses[strings.ToLower(cleanText(entry["status_code"]))]; ok {
			matchedRole = entry
			break
		}
	}
	if matchedRole == nil {
		matchedRole = fallbackRole
	}
	minDuration := int64(maxInt(int(coerceInt64(rule["min_duration_seconds"])), 0))
	priorSample := asStringAnyMap(priorResult["sample"])
	trackedRole := firstText(cleanText(rule["role_name"]), "role")
	statusCode := "missing"
	active := true
	detail := ""
	if matchedRole != nil {
		trackedRole = firstText(cleanText(firstNonEmpty(matchedRole["role_name"], matchedRole["role_label"])), "role")
		statusCode = firstText(strings.ToLower(cleanText(matchedRole["status_code"])), "unknown")
		_, active = triggerStatuses[statusCode]
		detail = cleanText(matchedRole["detail"])
	}
	var activeStarted any
	var activeDuration int64
	if active {
		previousStarted := coerceInt64(priorSample["active_started_at"])
		previousActive := boolDefault(priorSample["active"], false)
		previousRole := strings.ToLower(cleanText(priorSample["tracked_role_name"]))
		if previousActive && previousStarted > 0 && previousRole == strings.ToLower(trackedRole) {
			activeStarted = previousStarted
		} else {
			activeStarted = now
		}
		activeDuration = watchdogPreviewMaxInt64(0, now-coerceInt64(activeStarted))
	}
	matched := active && (minDuration <= 0 || activeDuration >= minDuration)
	if matchedRole == nil {
		summary := fmt.Sprintf("Role %s is not reporting health telemetry", firstText(cleanText(rule["role_name"]), "target role"))
		return watchdogPreviewRuleResult(rule, matched, false, summary, map[string]any{"role_name": cleanText(rule["role_name"]), "tracked_role_name": trackedRole, "status_code": "missing", "trigger_statuses": sortedStringSet(triggerStatuses), "active": active, "active_started_at": activeStarted, "active_duration_seconds": activeDuration, "min_duration_seconds": minDuration})
	}
	summary := fmt.Sprintf("%s health is %s", firstText(cleanText(firstNonEmpty(matchedRole["role_label"], matchedRole["role_name"])), trackedRole), statusCode)
	if minDuration > 0 && active && !matched {
		summary = fmt.Sprintf("%s health is %s for %d second(s)", firstText(cleanText(firstNonEmpty(matchedRole["role_label"], matchedRole["role_name"])), trackedRole), statusCode, activeDuration)
	}
	return watchdogPreviewRuleResult(rule, matched, false, summary, map[string]any{"role_name": matchedRole["role_name"], "role_label": matchedRole["role_label"], "tracked_role_name": trackedRole, "status_code": statusCode, "trigger_statuses": sortedStringSet(triggerStatuses), "detail": detail, "active": active, "active_started_at": activeStarted, "active_duration_seconds": activeDuration, "min_duration_seconds": minDuration})
}

func watchdogPreviewEvaluateResource(rule map[string]any, device map[string]any, now int64, priorResult map[string]any, label string, field string) map[string]any {
	value, ok := float64Value(device[field])
	if !ok || watchdogPreviewHeartbeatStale(device, now) {
		return watchdogPreviewRuleResult(rule, false, true, label+" telemetry is stale", map[string]any{})
	}
	threshold := watchdogPreviewFloat(rule["threshold"], watchdogDefaultResourceThreshold)
	duration := int64(maxInt(fallbackInt(rule["duration_seconds"], watchdogDefaultResourceDuration), 0))
	priorSample := asStringAnyMap(priorResult["sample"])
	overThreshold := value >= threshold
	var started any
	var elapsed int64
	if overThreshold {
		previousStarted := coerceInt64(priorSample["threshold_started_at"])
		if boolDefault(priorSample["over_threshold"], false) && previousStarted > 0 {
			started = previousStarted
		} else {
			started = now
		}
		elapsed = watchdogPreviewMaxInt64(0, now-coerceInt64(started))
	}
	matched := overThreshold && (duration <= 0 || elapsed >= duration)
	summary := fmt.Sprintf("%s usage is %.1f%% (threshold %.1f%%)", label, value, threshold)
	if overThreshold && !matched {
		summary = fmt.Sprintf("%s usage is %.1f%% and has been above %.1f%% for %d second(s)", label, value, threshold, elapsed)
	}
	return watchdogPreviewRuleResult(rule, matched, false, summary, map[string]any{"usage_percent": roundFloat(value, 2), "threshold": roundFloat(threshold, 2), "duration_seconds": duration, "over_threshold": overThreshold, "threshold_started_at": started, "elapsed_seconds": elapsed})
}

func watchdogPreviewEvaluateReboot(rule map[string]any, device map[string]any, priorResult map[string]any) map[string]any {
	uptime := coerceInt64(device["uptime"])
	if uptime <= 0 {
		return watchdogPreviewRuleResult(rule, false, true, "Uptime telemetry is unavailable", map[string]any{})
	}
	priorSample := asStringAnyMap(priorResult["sample"])
	previous := coerceInt64(priorSample["current_uptime_seconds"])
	baseline := boolDefault(priorSample["baseline_established"], false) || previous > 0
	if !baseline {
		return watchdogPreviewRuleResult(rule, false, false, "Waiting for reboot baseline snapshot", map[string]any{"baseline_established": true, "previous_uptime_seconds": int64(0), "current_uptime_seconds": uptime, "last_reboot": cleanText(device["last_reboot"])})
	}
	matched := uptime < previous
	summary := fmt.Sprintf("Device uptime increased from %d to %d second(s)", previous, uptime)
	if matched {
		summary = fmt.Sprintf("Device reboot detected: uptime moved from %d to %d second(s)", previous, uptime)
	}
	return watchdogPreviewRuleResult(rule, matched, false, summary, map[string]any{"baseline_established": true, "previous_uptime_seconds": previous, "current_uptime_seconds": uptime, "last_reboot": cleanText(device["last_reboot"])})
}

func watchdogPreviewEvaluateServicePending(rule map[string]any, device map[string]any, now int64) map[string]any {
	payload := watchdogPreviewInventoryPayload(device, "services")
	if watchdogPreviewPayloadStale(payload, now) {
		return watchdogPreviewRuleResult(rule, false, true, "Service inventory is stale", map[string]any{})
	}
	serviceName := strings.ToLower(cleanText(rule["service_name"]))
	pendingAction := strings.ToLower(cleanText(rule["pending_action"]))
	timeout := int64(maxInt(fallbackInt(rule["timeout_seconds"], watchdogDefaultServicePending), 0))
	pendingRows := []any{}
	timedOutRows := []any{}
	var youngest map[string]any
	for _, entry := range watchdogPreviewMapEntries(payload["services"]) {
		entryName := cleanText(entry["name"])
		entryAction := strings.ToLower(cleanText(entry["pending_action"]))
		if entryAction == "" || serviceName != "" && strings.ToLower(entryName) != serviceName || pendingAction != "" && entryAction != pendingAction {
			continue
		}
		requestedAt := watchdogPreviewMaxInt64(coerceInt64(entry["pending_requested_at"]), coerceInt64(entry["captured_at"]))
		age := int64(0)
		if requestedAt > 0 {
			age = watchdogPreviewMaxInt64(0, now-requestedAt)
		}
		row := map[string]any{"service_name": entryName, "pending_action": entryAction, "requested_at": requestedAt, "age_seconds": age, "desired_status": strings.ToLower(cleanText(entry["desired_status"])), "status_code": strings.ToLower(cleanText(entry["status_code"]))}
		pendingRows = append(pendingRows, row)
		if age >= timeout {
			timedOutRows = append(timedOutRows, row)
		}
		if youngest == nil || age > coerceInt64(youngest["age_seconds"]) {
			youngest = row
		}
	}
	if len(pendingRows) == 0 {
		summary := "No matching pending service actions were found"
		if serviceName != "" {
			summary = fmt.Sprintf("Service %s has no pending action", cleanText(rule["service_name"]))
		}
		return watchdogPreviewRuleResult(rule, false, false, summary, map[string]any{"service_name": cleanText(rule["service_name"]), "pending_action": pendingAction, "timeout_seconds": timeout, "pending_services": []any{}})
	}
	summary := fmt.Sprintf("%s pending %s for %d second(s)", youngest["service_name"], youngest["pending_action"], coerceInt64(youngest["age_seconds"]))
	if len(timedOutRows) == 1 {
		row := asStringAnyMap(timedOutRows[0])
		summary = fmt.Sprintf("%s has been pending %s for %d second(s)", row["service_name"], row["pending_action"], coerceInt64(row["age_seconds"]))
	} else if len(timedOutRows) > 1 {
		summary = fmt.Sprintf("%d service action(s) exceeded the pending timeout", len(timedOutRows))
	}
	return watchdogPreviewRuleResult(rule, len(timedOutRows) > 0, false, summary, map[string]any{"service_name": cleanText(rule["service_name"]), "pending_action": pendingAction, "timeout_seconds": timeout, "pending_services": pendingRows, "timed_out_services": timedOutRows})
}

func watchdogPreviewEvaluateUserSessions(rule map[string]any, device map[string]any, now int64) map[string]any {
	payload := watchdogPreviewInventoryPayload(device, "sessions")
	if watchdogPreviewPayloadStale(payload, now) {
		return watchdogPreviewRuleResult(rule, false, true, "Session inventory is stale", map[string]any{})
	}
	matchMode := strings.ToLower(firstText(cleanText(rule["match_mode"]), "blocklist"))
	patternMode := strings.ToLower(firstText(cleanText(rule["pattern_mode"]), "normalized"))
	patterns := watchdogPreviewStringsFromAny(rule["patterns"])
	deviceDomain := cleanText(device["domain"])
	deduped := map[string]map[string]any{}
	for _, entry := range watchdogPreviewMapEntries(payload["sessions"]) {
		username := cleanText(entry["username"])
		if username == "" {
			continue
		}
		key := strings.ToLower(username)
		if _, exists := deduped[key]; exists {
			continue
		}
		aliases := watchdogPreviewUsernameAliases(username, deviceDomain)
		matchedPatterns := []string{}
		for _, pattern := range patterns {
			if watchdogPreviewUserMatchesPattern(pattern, aliases, patternMode, deviceDomain) {
				matchedPatterns = append(matchedPatterns, pattern)
			}
		}
		sessionLabels := []string{}
		for _, candidate := range watchdogPreviewMapEntries(payload["sessions"]) {
			if strings.EqualFold(cleanText(candidate["username"]), username) {
				sessionLabels = append(sessionLabels, watchdogPreviewDescribeSession(candidate))
			}
		}
		deduped[key] = map[string]any{"username": username, "aliases": sortedStringSet(stringStructSet(aliases)), "matched_patterns": matchedPatterns, "sessions": sessionLabels}
	}
	userRows := make([]any, 0, len(deduped))
	for _, row := range deduped {
		userRows = append(userRows, row)
	}
	violating := []any{}
	for _, raw := range userRows {
		row := asStringAnyMap(raw)
		matchedPatterns := watchdogPreviewStringsFromAny(row["matched_patterns"])
		if matchMode == "allowlist" && len(matchedPatterns) == 0 || matchMode != "allowlist" && len(matchedPatterns) > 0 {
			violating = append(violating, row)
		}
	}
	matched := len(violating) > 0
	summary := "No logged-in users match the blocklist"
	if matchMode == "allowlist" {
		summary = "All logged-in users match the allowlist"
	}
	if len(userRows) == 0 {
		summary = "No active user sessions found"
	} else if matched {
		names := []string{}
		for _, raw := range violating {
			names = append(names, firstText(cleanText(asStringAnyMap(raw)["username"]), "Unknown"))
		}
		if matchMode == "allowlist" {
			summary = "Non-allowlisted users detected: " + strings.Join(names, ", ")
		} else {
			summary = "Blocked users detected: " + strings.Join(names, ", ")
		}
	}
	return watchdogPreviewRuleResult(rule, matched, false, summary, map[string]any{"match_mode": matchMode, "pattern_mode": patternMode, "patterns": patterns, "users": userRows, "violating_users": violating})
}

func watchdogPreviewEvaluateProcess(rule map[string]any, device map[string]any, now int64) map[string]any {
	payload := watchdogPreviewInventoryPayload(device, "processes")
	if watchdogPreviewPayloadStale(payload, now) {
		return watchdogPreviewRuleResult(rule, false, true, "Process inventory is stale", map[string]any{})
	}
	processName := cleanText(rule["process_name"])
	expectation := strings.ToLower(firstText(cleanText(rule["expectation"]), "present"))
	requestedKey := watchdogPreviewProcessKey(processName)
	var match map[string]any
	for _, entry := range watchdogPreviewMapEntries(payload["processes"]) {
		if watchdogPreviewProcessKey(entry["name"]) == requestedKey {
			match = entry
			break
		}
	}
	present := match != nil
	matched := present
	if expectation == "missing" {
		matched = !present
	}
	count := int64(0)
	if match != nil {
		count = coerceInt64(match["count"])
	}
	summary := fmt.Sprintf("Process %s is not running", processName)
	if present {
		summary = fmt.Sprintf("Process %s is running (%d instance(s))", processName, count)
	}
	return watchdogPreviewRuleResult(rule, matched, false, summary, map[string]any{"process_name": processName, "expectation": expectation, "present": present, "count": count})
}

func watchdogPreviewEvaluateSessionState(rule map[string]any, device map[string]any, now int64, priorResult map[string]any) map[string]any {
	payload := watchdogPreviewInventoryPayload(device, "sessions")
	if watchdogPreviewPayloadStale(payload, now) {
		return watchdogPreviewRuleResult(rule, false, true, "Session inventory is stale", map[string]any{})
	}
	sessionMode := strings.ToLower(firstText(cleanText(rule["session_mode"]), "current"))
	rdpOnly := boolDefault(rule["rdp_only"], false)
	current := watchdogPreviewFilteredSessions(payload["sessions"], rdpOnly)
	if sessionMode == "current" {
		states := stringSetFromAny(rule["states"])
		matching := []any{}
		for _, entry := range current {
			if _, ok := states[strings.ToLower(cleanText(asStringAnyMap(entry)["state_code"]))]; ok {
				matching = append(matching, entry)
			}
		}
		summary := "No matching sessions found"
		if len(current) == 0 {
			summary = "No sessions found"
		} else if len(matching) > 0 {
			summary = fmt.Sprintf("%d matching session(s) detected", len(matching))
		}
		return watchdogPreviewRuleResult(rule, len(matching) > 0, false, summary, map[string]any{"session_mode": sessionMode, "rdp_only": rdpOnly, "states": sortedStringSet(states), "sessions": current, "matching_sessions": matching})
	}
	priorSample := asStringAnyMap(priorResult["sample"])
	priorSessions := watchdogPreviewFilteredSessions(priorSample["sessions"], rdpOnly)
	baseline := boolDefault(priorSample["baseline_established"], false) || len(priorSessions) > 0
	if !baseline {
		return watchdogPreviewRuleResult(rule, false, false, "Waiting for session baseline snapshot", map[string]any{"session_mode": sessionMode, "rdp_only": rdpOnly, "baseline_established": true, "sessions": current, "detected_events": []any{}})
	}
	events := stringSetFromAny(rule["events"])
	priorLookup := map[string]map[string]any{}
	for _, raw := range priorSessions {
		entry := asStringAnyMap(raw)
		priorLookup[watchdogPreviewSessionIdentity(entry)] = entry
	}
	currentLookup := map[string]map[string]any{}
	for _, raw := range current {
		entry := asStringAnyMap(raw)
		currentLookup[watchdogPreviewSessionIdentity(entry)] = entry
	}
	detected := []any{}
	for identity, currentEntry := range currentLookup {
		previous := priorLookup[identity]
		label := watchdogPreviewDescribeSession(currentEntry)
		if previous == nil {
			detected = append(detected, mergeMap(map[string]any{"event": "started", "session": label}, currentEntry))
			if boolDefault(currentEntry["is_rdp"], false) {
				detected = append(detected, mergeMap(map[string]any{"event": "rdp_started", "session": label}, currentEntry))
			}
			continue
		}
		prevState := strings.ToLower(cleanText(previous["state_code"]))
		currState := strings.ToLower(cleanText(currentEntry["state_code"]))
		if currState == "locked" && prevState != "locked" {
			detected = append(detected, mergeMap(map[string]any{"event": "locked", "session": label}, currentEntry))
		}
		if prevState == "locked" && currState == "active" {
			detected = append(detected, mergeMap(map[string]any{"event": "unlocked", "session": label}, currentEntry))
		}
	}
	for identity, previous := range priorLookup {
		if _, ok := currentLookup[identity]; ok {
			continue
		}
		label := watchdogPreviewDescribeSession(previous)
		detected = append(detected, mergeMap(map[string]any{"event": "ended", "session": label}, previous))
		if boolDefault(previous["is_rdp"], false) {
			detected = append(detected, mergeMap(map[string]any{"event": "rdp_ended", "session": label}, previous))
		}
	}
	matching := []any{}
	for _, raw := range detected {
		if _, ok := events[strings.ToLower(cleanText(asStringAnyMap(raw)["event"]))]; ok {
			matching = append(matching, raw)
		}
	}
	summary := "No matching session transitions detected"
	if len(matching) > 0 {
		summary = fmt.Sprintf("%d session transition(s) detected", len(matching))
	}
	return watchdogPreviewRuleResult(rule, len(matching) > 0, false, summary, map[string]any{"session_mode": sessionMode, "rdp_only": rdpOnly, "baseline_established": true, "events": sortedStringSet(events), "sessions": current, "detected_events": matching})
}

func watchdogPreviewEvaluateNetworkChange(rule map[string]any, device map[string]any, priorResult map[string]any) map[string]any {
	current := watchdogPreviewNetworkEntries(device["network"])
	priorSample := asStringAnyMap(priorResult["sample"])
	prior := watchdogPreviewNetworkEntries(firstNonEmpty(priorSample["interfaces"], priorSample["network"]))
	baseline := boolDefault(priorSample["baseline_established"], false) || len(prior) > 0
	if !baseline {
		return watchdogPreviewRuleResult(rule, false, false, "Waiting for network interface baseline snapshot", map[string]any{"baseline_established": true, "interfaces": current, "detected_changes": []any{}})
	}
	changeTypes := stringSetFromAny(rule["change_types"])
	previousByKey := map[string]map[string]any{}
	currentByKey := map[string]map[string]any{}
	for _, entry := range prior {
		previousByKey[cleanText(entry["adapter_key"])] = entry
	}
	for _, entry := range current {
		currentByKey[cleanText(entry["adapter_key"])] = entry
	}
	changes := []any{}
	for key, currentEntry := range currentByKey {
		previous := previousByKey[key]
		if previous == nil {
			if _, ok := changeTypes["added"]; ok {
				changes = append(changes, mergeMap(map[string]any{"change_type": "added"}, currentEntry))
			}
			continue
		}
		if _, ok := changeTypes["mac_changed"]; ok {
			currentMAC := strings.ToLower(cleanText(currentEntry["mac"]))
			previousMAC := strings.ToLower(cleanText(previous["mac"]))
			if currentMAC != "" && previousMAC != "" && currentMAC != previousMAC {
				changes = append(changes, map[string]any{"change_type": "mac_changed", "adapter": currentEntry["adapter"], "adapter_key": currentEntry["adapter_key"], "previous_mac": previousMAC, "current_mac": currentMAC, "ips": currentEntry["ips"]})
			}
		}
	}
	if _, ok := changeTypes["removed"]; ok {
		for key, previous := range previousByKey {
			if currentByKey[key] == nil {
				changes = append(changes, mergeMap(map[string]any{"change_type": "removed"}, previous))
			}
		}
	}
	summary := "No matching network interface changes detected"
	if len(changes) > 0 {
		summary = fmt.Sprintf("%d network interface change(s) detected", len(changes))
	}
	return watchdogPreviewRuleResult(rule, len(changes) > 0, false, summary, map[string]any{"baseline_established": true, "change_types": sortedStringSet(changeTypes), "interfaces": current, "detected_changes": changes})
}

func watchdogPreviewEvaluateDrivePresence(rule map[string]any, device map[string]any, priorResult map[string]any) map[string]any {
	scope := strings.ToLower(firstText(cleanText(rule["storage_scope"]), "all"))
	watchMode := strings.ToLower(firstText(cleanText(rule["watch_mode"]), "any"))
	changeTypes := stringSetFromAny(rule["change_types"])
	scopedCurrent := watchdogPreviewScopedStorageEntries(device["storage"], scope)
	currentByKey := map[string]map[string]any{}
	for _, entry := range scopedCurrent {
		currentByKey[cleanText(entry["drive_key"])] = entry
	}
	if watchMode == "specific" {
		driveList := watchdogPreviewStringList(rule["drive_list"])
		expected := map[string]string{}
		for _, drive := range driveList {
			if key := watchdogPreviewStorageDriveKey(drive); key != "" {
				expected[key] = drive
			}
		}
		changes := []any{}
		if _, ok := changeTypes["removed"]; ok {
			for key, original := range expected {
				if currentByKey[key] == nil {
					changes = append(changes, map[string]any{"change_type": "removed", "drive": original, "drive_key": key})
				}
			}
		}
		if _, ok := changeTypes["added"]; ok {
			for key, entry := range currentByKey {
				if _, expected := expected[key]; !expected {
					changes = append(changes, mergeMap(map[string]any{"change_type": "added"}, entry))
				}
			}
		}
		summary := "Drive topology matches the expected set"
		if len(changes) > 0 {
			summary = fmt.Sprintf("%d drive presence change(s) detected", len(changes))
		}
		return watchdogPreviewRuleResult(rule, len(changes) > 0, false, summary, map[string]any{"storage_scope": scope, "watch_mode": watchMode, "drive_list": driveList, "current_drives": scopedCurrent, "detected_changes": changes})
	}
	priorSample := asStringAnyMap(priorResult["sample"])
	prior := watchdogPreviewScopedStorageEntries(priorSample["drives"], scope)
	baseline := boolDefault(priorSample["baseline_established"], false) || len(prior) > 0
	if !baseline {
		return watchdogPreviewRuleResult(rule, false, false, "Waiting for drive baseline snapshot", map[string]any{"baseline_established": true, "storage_scope": scope, "watch_mode": watchMode, "drives": scopedCurrent, "detected_changes": []any{}})
	}
	priorByKey := map[string]map[string]any{}
	for _, entry := range prior {
		priorByKey[cleanText(entry["drive_key"])] = entry
	}
	changes := []any{}
	if _, ok := changeTypes["added"]; ok {
		for key, entry := range currentByKey {
			if priorByKey[key] == nil {
				changes = append(changes, mergeMap(map[string]any{"change_type": "added"}, entry))
			}
		}
	}
	if _, ok := changeTypes["removed"]; ok {
		for key, entry := range priorByKey {
			if currentByKey[key] == nil {
				changes = append(changes, mergeMap(map[string]any{"change_type": "removed"}, entry))
			}
		}
	}
	summary := "No matching drive changes detected"
	if len(changes) > 0 {
		summary = fmt.Sprintf("%d drive change(s) detected", len(changes))
	}
	return watchdogPreviewRuleResult(rule, len(changes) > 0, false, summary, map[string]any{"baseline_established": true, "storage_scope": scope, "watch_mode": watchMode, "drives": scopedCurrent, "detected_changes": changes})
}

func watchdogPreviewEvaluateSoftware(rule map[string]any, device map[string]any) map[string]any {
	name := strings.ToLower(cleanText(rule["software_name"]))
	source := strings.ToLower(cleanText(rule["software_source"]))
	requirePresent := boolDefault(rule["require_present"], true)
	operator := strings.ToLower(cleanText(rule["version_operator"]))
	versionValue := cleanText(rule["version_value"])
	candidates := []map[string]any{}
	for _, entry := range watchdogPreviewMapEntries(device["software"]) {
		entryName := strings.ToLower(cleanText(entry["name"]))
		entrySource := strings.ToLower(cleanText(entry["source"]))
		if !strings.Contains(entryName, name) {
			continue
		}
		if source != "" && source != entrySource {
			continue
		}
		candidates = append(candidates, entry)
	}
	if len(candidates) == 0 {
		return watchdogPreviewRuleResult(rule, requirePresent, false, fmt.Sprintf("%s is not installed", cleanText(rule["software_name"])), map[string]any{"software_name": cleanText(rule["software_name"]), "present": false})
	}
	sample := candidates[0]
	versionText := cleanText(sample["version"])
	versionMatched := false
	if operator != "" && versionValue != "" {
		comparison, ok := watchdogPreviewCompareVersion(versionText, versionValue)
		switch {
		case operator == "matches":
			versionMatched = versionText == versionValue
		case ok && operator == "older_than":
			versionMatched = comparison < 0
		case ok && operator == "newer_than":
			versionMatched = comparison > 0
		}
	}
	matched := false
	if operator != "" && versionValue != "" {
		matched = versionMatched
	}
	summary := fmt.Sprintf("%s version %s", sample["name"], firstText(versionText, "unknown"))
	if operator != "" && versionValue != "" {
		summary = fmt.Sprintf("%s (%s %s)", summary, strings.ReplaceAll(operator, "_", " "), versionValue)
	}
	return watchdogPreviewRuleResult(rule, matched, false, summary, map[string]any{"software_name": sample["name"], "present": true, "version": versionText, "version_operator": operator, "version_value": versionValue})
}

func (s *postgresOperatorStore) loadWatchdogPreviewOverrides(ctx context.Context, watchdogID int64) (map[string]map[string]any, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, `
		SELECT watchdog_id, id, device_guid, hostname, site_id, state, reason, created_by, created_at, expires_at, updated_at
		  FROM engine.watchdog_device_overrides
		 WHERE watchdog_id=$1
		   AND (expires_at IS NULL OR expires_at = 0 OR expires_at > $2)
	  ORDER BY updated_at DESC, id DESC
	`, watchdogID, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]map[string]any{}
	for rows.Next() {
		var row watchdogDeviceOverrideRow
		if err := rows.Scan(&row.WatchdogID, &row.ID, &row.DeviceGUID, &row.Hostname, &row.SiteID, &row.State, &row.Reason, &row.CreatedBy, &row.CreatedAt, &row.ExpiresAt, &row.UpdatedAt); err != nil {
			return nil, err
		}
		hostname := strings.ToLower(cleanText(nullString(row.Hostname)))
		if hostname != "" {
			if _, exists := result[hostname]; !exists {
				result[hostname] = watchdogDeviceOverridePayload(row)
			}
		}
	}
	return result, rows.Err()
}

func (s *postgresOperatorStore) loadWatchdogPreviewStates(ctx context.Context, watchdogID int64) (map[string]map[string]any, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, `
		SELECT watchdog_id, device_guid, hostname, site_id, state, consecutive_matches,
		       first_matched_at, clear_started_at, last_evaluated_at, last_matched_at,
		       last_sample_json, current_incident_id, last_action_at, updated_at
		  FROM engine.watchdog_device_state
		 WHERE watchdog_id=$1
	  ORDER BY updated_at DESC, id DESC
	`, watchdogID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]map[string]any{}
	for rows.Next() {
		var row watchdogDeviceStateRow
		if err := rows.Scan(&row.WatchdogID, &row.DeviceGUID, &row.Hostname, &row.SiteID, &row.State, &row.Consecutive, &row.FirstMatchedAt, &row.ClearStartedAt, &row.LastEvaluatedAt, &row.LastMatchedAt, &row.LastSampleJSON, &row.CurrentIncidentID, &row.LastActionAt, &row.UpdatedAt); err != nil {
			return nil, err
		}
		hostname := strings.ToLower(cleanText(nullString(row.Hostname)))
		if hostname != "" {
			if _, exists := result[hostname]; !exists {
				result[hostname] = watchdogDeviceStatePayload(row)
			}
		}
	}
	return result, rows.Err()
}

func watchdogPreviewRuleResult(rule map[string]any, matched bool, stale bool, summary string, sample map[string]any) map[string]any {
	return map[string]any{
		"rule_id": rule["id"],
		"type":    strings.ToLower(cleanText(rule["type"])),
		"matched": matched,
		"stale":   stale,
		"summary": summary,
		"sample":  sample,
	}
}

func watchdogPreviewInventoryPayload(device map[string]any, listKey string) map[string]any {
	payload := asStringAnyMap(device[listKey+"_payload"])
	if len(payload) > 0 {
		return payload
	}
	return map[string]any{
		listKey:       anySlice(device[listKey]),
		"reported_at": coerceInt64(device[listKey+"_reported_at"]),
	}
}

func watchdogPreviewPayloadStale(payload map[string]any, now int64) bool {
	reportedAt := coerceInt64(payload["reported_at"])
	return reportedAt <= 0 || now-reportedAt > watchdogPreviewTelemetryStaleSeconds
}

func watchdogPreviewHeartbeatStale(device map[string]any, now int64) bool {
	lastSeen := coerceInt64(device["last_seen"])
	return lastSeen <= 0 || now-lastSeen > watchdogPreviewTelemetryStaleSeconds
}

func watchdogPreviewPriorRuleResult(priorState map[string]any, ruleID any) map[string]any {
	lastSample := asStringAnyMap(priorState["last_sample"])
	for _, raw := range anySlice(lastSample["results"]) {
		result := asStringAnyMap(raw)
		if cleanText(result["rule_id"]) == cleanText(ruleID) {
			return result
		}
	}
	return map[string]any{}
}

func watchdogPreviewStorageEntries(raw any) []map[string]any {
	entries := watchdogPreviewMapEntries(raw)
	rows := make([]map[string]any, 0, len(entries))
	for index, item := range entries {
		drive := firstText(cleanText(firstNonEmpty(item["drive"], item["label"], item["mount"])), fmt.Sprintf("Drive %d", index+1))
		total, totalOK := float64Value(item["total"])
		used, usedOK := float64Value(item["used"])
		free, freeOK := float64Value(item["free"])
		usage, usageOK := float64Value(item["usage"])
		if usageOK && usage <= 1 {
			usage *= 100
		}
		if !usageOK && totalOK && usedOK && total > 0 {
			usage = clampFloat((used/total)*100, 0, 100)
			usageOK = true
		}
		if !usageOK && totalOK && freeOK && total > 0 {
			usage = clampFloat(((total-free)/total)*100, 0, 100)
			usageOK = true
		}
		row := map[string]any{
			"id":        fmt.Sprintf("%s-%d", firstText(cleanText(firstNonEmpty(item["drive"], item["label"], item["mount"])), "drive"), index),
			"drive":     drive,
			"total":     nil,
			"used":      nil,
			"free":      nil,
			"disk_type": firstText(cleanText(firstNonEmpty(item["disk_type"], item["type"])), "Fixed Disk"),
		}
		if totalOK {
			row["total"] = total
		}
		if usedOK {
			row["used"] = used
		}
		if freeOK {
			row["free"] = free
		}
		if usageOK {
			row["usage_percent"] = usage
		}
		rows = append(rows, row)
	}
	return rows
}

func watchdogPreviewNetworkEntries(raw any) []map[string]any {
	entries := watchdogPreviewMapEntries(raw)
	rows := make([]map[string]any, 0, len(entries))
	for index, item := range entries {
		adapter := firstText(cleanText(firstNonEmpty(item["adapter"], item["name"], item["interface"])), fmt.Sprintf("Adapter %d", index+1))
		ips := []string{}
		for _, ip := range anySlice(item["ips"]) {
			if text := cleanText(ip); text != "" {
				ips = append(ips, text)
			}
		}
		rows = append(rows, map[string]any{
			"id":          fmt.Sprintf("%s-%d", strings.ToLower(adapter), index),
			"adapter":     adapter,
			"adapter_key": strings.ToLower(adapter),
			"mac":         strings.ToLower(cleanText(item["mac"])),
			"ips":         ips,
		})
	}
	return rows
}

func watchdogPreviewScopedStorageEntries(raw any, scope string) []map[string]any {
	rows := []map[string]any{}
	for _, entry := range watchdogPreviewStorageEntries(raw) {
		diskType := strings.ToLower(cleanText(entry["disk_type"]))
		removable := strings.Contains(diskType, "removable") || strings.Contains(diskType, "usb")
		if scope == "removable" && !removable || scope == "fixed" && removable {
			continue
		}
		driveKey := watchdogPreviewStorageDriveKey(entry["drive"])
		if driveKey == "" {
			continue
		}
		next := copyMap(entry)
		next["drive_key"] = driveKey
		rows = append(rows, next)
	}
	return rows
}

func watchdogPreviewMapEntries(raw any) []map[string]any {
	switch typed := raw.(type) {
	case []map[string]any:
		return typed
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if row, ok := item.(map[string]any); ok {
				out = append(out, row)
			}
		}
		return out
	default:
		return []map[string]any{}
	}
}

func watchdogPreviewFilterIDs(targets []any) []int64 {
	ids := []int64{}
	seen := map[int64]struct{}{}
	for _, raw := range targets {
		if id := watchdogTargetFilterID(asStringAnyMap(raw)); id > 0 {
			if _, exists := seen[id]; !exists {
				seen[id] = struct{}{}
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func sortWatchdogPreviewDevices(devices []map[string]any) []map[string]any {
	out := append([]map[string]any(nil), devices...)
	sort.Slice(out, func(i, j int) bool {
		left := strings.ToLower(cleanText(out[i]["site_name"])) + "\x00" + strings.ToLower(cleanText(out[i]["hostname"]))
		right := strings.ToLower(cleanText(out[j]["site_name"])) + "\x00" + strings.ToLower(cleanText(out[j]["hostname"]))
		return left < right
	})
	return out
}

func watchdogPreviewSiteIDs(raw any) []int64 {
	ids := []int64{}
	seen := map[int64]struct{}{}
	if typed, ok := raw.([]int64); ok {
		for _, parsed := range typed {
			if parsed <= 0 {
				continue
			}
			if _, exists := seen[parsed]; exists {
				continue
			}
			seen[parsed] = struct{}{}
			ids = append(ids, parsed)
		}
		return ids
	}
	for _, item := range anySlice(raw) {
		value := item
		if typed, ok := item.(map[string]any); ok {
			value = firstNonEmpty(typed["site_id"], typed["id"], typed["value"])
		}
		parsed := coerceInt64(value)
		if parsed <= 0 {
			continue
		}
		if _, exists := seen[parsed]; exists {
			continue
		}
		seen[parsed] = struct{}{}
		ids = append(ids, parsed)
	}
	return ids
}

func watchdogPreviewOptionalInt(value any) any {
	parsed := coerceInt64(value)
	if parsed <= 0 {
		return nil
	}
	return parsed
}

func watchdogPreviewRecordID(record map[string]any) int64 {
	return coerceInt64(record["id"])
}

func watchdogPreviewNullableInt64(value any) any {
	if parsed, ok := int64Value(value); ok {
		return parsed
	}
	return nil
}

func watchdogPreviewFloat(value any, fallback int64) float64 {
	if parsed, ok := float64Value(value); ok {
		return parsed
	}
	return float64(fallback)
}

func clampFloat(value float64, minimum float64, maximum float64) float64 {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func roundFloat(value float64, places int) float64 {
	scale := 1.0
	for i := 0; i < places; i++ {
		scale *= 10
	}
	if value >= 0 {
		return float64(int64(value*scale+0.5)) / scale
	}
	return float64(int64(value*scale-0.5)) / scale
}

func watchdogPreviewMaxInt64(values ...int64) int64 {
	if len(values) == 0 {
		return 0
	}
	result := values[0]
	for _, value := range values[1:] {
		if value > result {
			result = value
		}
	}
	return result
}

func watchdogPreviewStringList(raw any) []string {
	values := []any{}
	switch typed := raw.(type) {
	case []any:
		values = typed
	case []string:
		for _, item := range typed {
			values = append(values, item)
		}
	case nil:
	default:
		for _, part := range strings.FieldsFunc(cleanText(typed), func(r rune) bool { return r == ',' || r == '\r' || r == '\n' }) {
			values = append(values, part)
		}
	}
	result := []string{}
	seen := map[string]struct{}{}
	for _, value := range values {
		text := cleanSingleLine(cleanText(value))
		key := strings.ToLower(text)
		if text == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, text)
	}
	return result
}

func watchdogPreviewChoiceList(raw any, valid map[string]struct{}, defaults []string) []string {
	values := watchdogPreviewStringList(raw)
	result := []string{}
	seen := map[string]struct{}{}
	for _, value := range values {
		normalized := strings.ToLower(value)
		if _, ok := valid[normalized]; !ok {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	if len(result) > 0 {
		return result
	}
	for _, value := range defaults {
		if _, ok := valid[value]; ok {
			result = append(result, value)
		}
	}
	return result
}

func watchdogPreviewStringsFromAny(raw any) []string {
	if typed, ok := raw.([]string); ok {
		return append([]string(nil), typed...)
	}
	return watchdogPreviewStringList(raw)
}

func stringSetFromAny(raw any) map[string]struct{} {
	result := map[string]struct{}{}
	for _, item := range watchdogPreviewStringsFromAny(raw) {
		if item = strings.ToLower(cleanText(item)); item != "" {
			result[item] = struct{}{}
		}
	}
	return result
}

func sortedStringSet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func stringStructSet(values []string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func watchdogPreviewStorageDriveMode(value any, drive any) string {
	mode := strings.ToLower(cleanText(value))
	if mode == "all" || mode == "specific" {
		return mode
	}
	if cleanText(drive) != "" {
		return "specific"
	}
	return "all"
}

func watchdogPreviewStorageDriveKey(value any) string {
	text := strings.ToLower(cleanText(value))
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "\\", "/")
	if text == "/" {
		return text
	}
	text = strings.TrimRight(text, "/")
	if len(text) >= 2 && text[1] == ':' && text[0] >= 'a' && text[0] <= 'z' {
		return text[:1]
	}
	if len(text) == 1 && text[0] >= 'a' && text[0] <= 'z' {
		return text
	}
	return text
}

func watchdogPreviewRoleMatchName(value any) string {
	text := strings.ToLower(cleanText(value))
	switch text {
	case "script_exec_system":
		return "context_system"
	case "script_exec_currentuser":
		return "context_currentuser"
	case "device_audit":
		return "device_auditor"
	case "service_control":
		return "service_management"
	case "remoteshell":
		return "remote_shell"
	case "wireguardtunnel":
		return "wireguard"
	case "macro":
		return "macros"
	case "screenshot":
		return "node_screenshot"
	default:
		return text
	}
}

func watchdogPreviewProcessKey(value any) string {
	text := strings.ToLower(cleanText(value))
	return strings.TrimSuffix(text, ".exe")
}

func watchdogPreviewUsernameAliases(rawUser any, deviceDomain string) []string {
	text := strings.ToLower(cleanText(rawUser))
	if text == "" || text == "no users logged in" {
		return []string{}
	}
	aliases := map[string]struct{}{text: {}}
	domainText := strings.ToLower(cleanText(deviceDomain))
	domainShort := domainText
	if index := strings.Index(domainShort, "."); index >= 0 {
		domainShort = domainShort[:index]
	}
	userOnly := text
	observedDomain := ""
	if strings.Contains(text, "\\") {
		parts := strings.SplitN(text, "\\", 2)
		observedDomain = parts[0]
		userOnly = parts[1]
	} else if strings.Contains(text, "@") {
		parts := strings.SplitN(text, "@", 2)
		userOnly = parts[0]
		observedDomain = parts[1]
	}
	if userOnly != "" {
		aliases[userOnly] = struct{}{}
		if observedDomain != "" {
			observedShort := strings.SplitN(observedDomain, ".", 2)[0]
			aliases[observedShort+"\\"+userOnly] = struct{}{}
			aliases[userOnly+"@"+observedDomain] = struct{}{}
		}
		if domainShort != "" {
			aliases[domainShort+"\\"+userOnly] = struct{}{}
		}
		if domainText != "" {
			aliases[userOnly+"@"+domainText] = struct{}{}
		}
	}
	return sortedStringSet(aliases)
}

func watchdogPreviewUserMatchesPattern(pattern string, aliases []string, patternMode string, deviceDomain string) bool {
	pattern = cleanText(pattern)
	if pattern == "" || len(aliases) == 0 {
		return false
	}
	switch patternMode {
	case "regex":
		expr, err := regexp.Compile("(?i)" + pattern)
		if err != nil {
			return false
		}
		for _, alias := range aliases {
			if expr.MatchString(alias) {
				return true
			}
		}
		return false
	case "wildcard":
		expr := "^" + strings.ReplaceAll(strings.ReplaceAll(regexp.QuoteMeta(strings.ToLower(pattern)), "\\*", ".*"), "\\?", ".") + "$"
		compiled, err := regexp.Compile(expr)
		if err != nil {
			return false
		}
		for _, alias := range aliases {
			if compiled.MatchString(alias) {
				return true
			}
		}
		return false
	default:
		patternAliases := stringStructSet(watchdogPreviewUsernameAliases(pattern, deviceDomain))
		for _, alias := range aliases {
			if _, ok := patternAliases[alias]; ok {
				return true
			}
		}
		return false
	}
}

func watchdogPreviewDescribeSession(entry map[string]any) string {
	username := firstText(cleanText(entry["username"]), "Unknown User")
	sessionName := cleanText(entry["session_name"])
	if sessionName != "" {
		return fmt.Sprintf("%s (%s)", username, sessionName)
	}
	return username
}

func watchdogPreviewSessionIdentity(entry map[string]any) string {
	sessionID := coerceInt64(entry["session_id"])
	if sessionID > 0 {
		return fmt.Sprintf("id:%d", sessionID)
	}
	return strings.ToLower(fmt.Sprintf("%s|%s|%s", cleanText(entry["username"]), cleanText(entry["session_name"]), cleanText(entry["protocol"])))
}

func watchdogPreviewFilteredSessions(raw any, rdpOnly bool) []any {
	out := []any{}
	for _, entry := range watchdogPreviewMapEntries(raw) {
		if rdpOnly && !boolDefault(entry["is_rdp"], false) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func watchdogPreviewCompareVersion(left string, right string) (int, bool) {
	left = cleanText(left)
	right = cleanText(right)
	if left == "" || right == "" {
		return 0, false
	}
	leftParts := watchdogPreviewVersionParts(left)
	rightParts := watchdogPreviewVersionParts(right)
	maxLen := len(leftParts)
	if len(rightParts) > maxLen {
		maxLen = len(rightParts)
	}
	for i := 0; i < maxLen; i++ {
		leftValue := "0"
		rightValue := "0"
		if i < len(leftParts) {
			leftValue = leftParts[i]
		}
		if i < len(rightParts) {
			rightValue = rightParts[i]
		}
		leftInt, leftErr := strconv.Atoi(leftValue)
		rightInt, rightErr := strconv.Atoi(rightValue)
		if leftErr == nil && rightErr == nil {
			if leftInt < rightInt {
				return -1, true
			}
			if leftInt > rightInt {
				return 1, true
			}
			continue
		}
		if leftValue < rightValue {
			return -1, true
		}
		if leftValue > rightValue {
			return 1, true
		}
	}
	return 0, true
}

func watchdogPreviewVersionParts(value string) []string {
	parts := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return r == '.' || r == '-' || r == '_' || r == '+'
	})
	out := []string{}
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func mergeMap(left map[string]any, right map[string]any) map[string]any {
	result := copyMap(left)
	for key, value := range right {
		result[key] = value
	}
	return result
}
