package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const watchdogEvaluationLoopSeconds = 30

type watchdogRuntime struct {
	auth     *authService
	store    *postgresOperatorStore
	realtime *operatorRealtimeHub
}

func startGoWatchdogRuntime(ctx context.Context, auth *authService, realtime *operatorRealtimeHub) {
	if auth == nil {
		return
	}
	store, ok := auth.store.(*postgresOperatorStore)
	if !ok || store == nil {
		logDebug("watchdogs", "Go watchdog runtime skipped: postgres store unavailable")
		return
	}
	runtime := &watchdogRuntime{auth: auth, store: store, realtime: realtime}
	if purged, err := store.purgeResolvedOfflineWatchdogIncidents(ctx); err != nil {
		logDebug("watchdogs", "startup offline incident purge failed: "+err.Error())
	} else if purged > 0 {
		logDebug("watchdogs", fmt.Sprintf("purged %d resolved offline watchdog incidents during startup", purged))
	}
	go runtime.loop(ctx)
	logDebug("watchdogs", "Go watchdog evaluator loop started")
}

func (r *watchdogRuntime) loop(ctx context.Context) {
	ticker := time.NewTicker(watchdogEvaluationLoopSeconds * time.Second)
	defer ticker.Stop()
	for {
		if err := r.evaluateDueWatchdogs(ctx); err != nil {
			logDebug("watchdogs", "watchdog evaluator tick failed: "+err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *watchdogRuntime) evaluateDueWatchdogs(parent context.Context) error {
	ctx, cancel := watchdogRuntimeContext(parent, r.auth)
	defer cancel()
	profile := watchdogRuntimeProfile()
	records, err := r.store.listWatchdogs(ctx, profile, watchdogListFilter{ArchivedSet: true, Archived: false})
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	due := make([]map[string]any, 0, len(records))
	for _, record := range records {
		if !boolDefault(record["enabled"], true) {
			continue
		}
		interval := maxInt(int(coerceInt64(record["evaluation_interval_seconds"])), watchdogDefaultEvalSeconds)
		if interval < watchdogEvaluationLoopSeconds {
			interval = watchdogEvaluationLoopSeconds
		}
		lastEvaluated := coerceInt64(record["last_evaluated_at"])
		if lastEvaluated > 0 && now-lastEvaluated < int64(interval) {
			continue
		}
		due = append(due, record)
	}
	if len(due) == 0 {
		return nil
	}
	devices, err := r.store.listDevices(ctx, profile, deviceListFilter{})
	if err != nil {
		return err
	}
	for _, record := range due {
		if err := r.evaluateWatchdog(ctx, record, devices); err != nil {
			logDebug("watchdogs", fmt.Sprintf("watchdog %d evaluation failed: %v", coerceInt64(record["id"]), err))
		}
	}
	return nil
}

func (r *watchdogRuntime) evaluateWatchdogByID(parent context.Context, watchdogID int64) error {
	if watchdogID <= 0 {
		return nil
	}
	ctx, cancel := watchdogRuntimeContext(parent, r.auth)
	defer cancel()
	profile := watchdogRuntimeProfile()
	record, found, err := r.store.getWatchdog(ctx, profile, watchdogID)
	if err != nil || !found {
		return err
	}
	devices, err := r.store.listDevices(ctx, profile, deviceListFilter{})
	if err != nil {
		return err
	}
	return r.evaluateWatchdog(ctx, record, devices)
}

func (r *watchdogRuntime) evaluateWatchdog(ctx context.Context, record map[string]any, devices []map[string]any) error {
	watchdogID := coerceInt64(record["id"])
	if watchdogID <= 0 {
		return nil
	}
	resolvedDevices, err := r.store.resolveWatchdogPreviewTargets(ctx, watchdogRuntimeProfile(), record, devices)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	if boolDefault(record["archived"], false) || !boolDefault(record["enabled"], true) {
		return r.deactivateWatchdog(ctx, record, len(resolvedDevices), now)
	}
	states, err := r.store.loadWatchdogRuntimeStates(ctx, watchdogID)
	if err != nil {
		return err
	}
	overrides, err := r.store.loadWatchdogRuntimeOverrides(ctx, watchdogID, now)
	if err != nil {
		return err
	}
	incidents, err := r.store.loadWatchdogRuntimeActiveIncidents(ctx, watchdogID)
	if err != nil {
		return err
	}
	touched := map[string]struct{}{}
	for _, device := range resolvedDevices {
		hostname := cleanText(device["hostname"])
		hostKey := strings.ToLower(hostname)
		if hostname == "" || hostKey == "" {
			continue
		}
		touched[hostKey] = struct{}{}
		priorState := states[hostKey]
		override := overrides[hostKey]
		incident := incidents[hostKey]
		if override != nil && watchdogRuntimeValidOverrideState(cleanText(override["state"])) {
			if err := r.applyWatchdogOverride(ctx, record, device, priorState, override, incident, now); err != nil {
				return err
			}
			r.broadcast(hostname, watchdogID)
			continue
		}
		evaluation := evaluateWatchdogPreviewDevice(record, device, priorState)
		updated, err := r.applyWatchdogEvaluation(ctx, record, device, priorState, incident, evaluation, now)
		if err != nil {
			return err
		}
		if updated {
			r.broadcast(hostname, watchdogID)
		}
	}
	for hostKey, priorState := range states {
		if _, ok := touched[hostKey]; ok {
			continue
		}
		if err := r.markWatchdogTargetRemoved(ctx, watchdogID, priorState, now); err != nil {
			return err
		}
		if hostname := cleanText(priorState["hostname"]); hostname != "" {
			r.broadcast(hostname, watchdogID)
		}
	}
	if err := r.store.updateWatchdogEvaluationMetadata(ctx, watchdogID, now, len(resolvedDevices)); err != nil {
		return err
	}
	if len(resolvedDevices) == 0 {
		r.broadcast("", watchdogID)
	}
	return nil
}

func (r *watchdogRuntime) deactivateWatchdog(ctx context.Context, record map[string]any, targetDeviceCount int, now int64) error {
	watchdogID := coerceInt64(record["id"])
	states, err := r.store.loadWatchdogRuntimeStates(ctx, watchdogID)
	if err != nil {
		return err
	}
	incidents, err := r.store.loadWatchdogRuntimeActiveIncidents(ctx, watchdogID)
	if err != nil {
		return err
	}
	reason := "disabled"
	if boolDefault(record["archived"], false) {
		reason = "archived"
	}
	for _, incident := range incidents {
		if err := r.store.resolveWatchdogIncident(ctx, coerceInt64(incident["id"]), reason, now); err != nil {
			return err
		}
	}
	for _, state := range states {
		next := watchdogRuntimeStateUpdate{
			WatchdogID:        watchdogID,
			DeviceGUID:        cleanText(state["device_guid"]),
			Hostname:          cleanText(state["hostname"]),
			SiteID:            int64PtrFromAny(state["site_id"]),
			State:             reason,
			LastEvaluatedAt:   now,
			LastSample:        map[string]any{},
			CurrentIncidentID: nil,
			LastActionAt:      int64PtrFromAny(state["last_action_at"]),
		}
		if err := r.store.upsertWatchdogRuntimeState(ctx, next); err != nil {
			return err
		}
		if next.Hostname != "" {
			r.broadcast(next.Hostname, watchdogID)
		}
	}
	if err := r.store.updateWatchdogEvaluationMetadata(ctx, watchdogID, now, targetDeviceCount); err != nil {
		return err
	}
	if len(states) == 0 {
		r.broadcast("", watchdogID)
	}
	return nil
}

func (r *watchdogRuntime) applyWatchdogOverride(ctx context.Context, record map[string]any, device map[string]any, priorState map[string]any, override map[string]any, incident map[string]any, now int64) error {
	watchdogID := coerceInt64(record["id"])
	state := strings.ToLower(cleanText(override["state"]))
	reason := firstText(cleanText(override["reason"]), state, "suppressed")
	var incidentID *int64
	if incident != nil && coerceInt64(incident["id"]) > 0 {
		id := coerceInt64(incident["id"])
		incidentID = &id
		if err := r.store.setWatchdogIncidentState(ctx, id, "suppressed", reason, now); err != nil {
			return err
		}
	}
	return r.store.upsertWatchdogRuntimeState(ctx, watchdogRuntimeStateUpdate{
		WatchdogID:        watchdogID,
		DeviceGUID:        normalizeCanonicalGUID(firstPresentAny(device["guid"], device["agent_guid"], device["device_guid"])),
		Hostname:          cleanText(device["hostname"]),
		SiteID:            int64PtrFromAny(device["site_id"]),
		State:             state,
		LastEvaluatedAt:   now,
		LastSample:        map[string]any{"override": override},
		CurrentIncidentID: incidentID,
		LastActionAt:      int64PtrFromAny(priorState["last_action_at"]),
	})
}

func (r *watchdogRuntime) applyWatchdogEvaluation(ctx context.Context, record map[string]any, device map[string]any, priorState map[string]any, incident map[string]any, evaluation map[string]any, now int64) (bool, error) {
	watchdogID := coerceInt64(record["id"])
	matched := boolDefault(evaluation["matched"], false)
	staleState := strings.EqualFold(cleanText(evaluation["state"]), "stale_data")
	consecutive := coerceInt64(priorState["consecutive_matches"])
	firstMatchedAt := int64PtrFromAny(priorState["first_matched_at"])
	clearStartedAt := int64PtrFromAny(priorState["clear_started_at"])
	lastActionAt := int64PtrFromAny(priorState["last_action_at"])
	var currentIncidentID *int64
	stateName := firstText(strings.ToLower(cleanText(evaluation["state"])), "normal")
	var lastMatchedAt *int64
	if matched {
		consecutive++
		if firstMatchedAt == nil {
			firstMatchedAt = &now
		}
		clearStartedAt = nil
		if consecutive >= int64(maxInt(int(coerceInt64(record["min_consecutive_matches"])), watchdogDefaultMinMatches)) {
			persistedIncident, err := r.store.createOrUpdateWatchdogIncident(ctx, record, device, incident, evaluation, nil, now)
			if err != nil {
				return false, err
			}
			incident = persistedIncident
			id := coerceInt64(persistedIncident["id"])
			currentIncidentID = &id
			incidentState := strings.ToLower(firstText(cleanText(persistedIncident["state"]), "open"))
			if incidentState == "open" && watchdogRuntimeShouldRunActions(record, lastActionAt, now) {
				actionSummary := r.runWatchdogActions(ctx, record, device, evaluation, mergeMap(persistedIncident, map[string]any{"watchdog_id": watchdogID}))
				if len(actionSummary) > 0 {
					if err := r.store.updateWatchdogIncidentActionSummary(ctx, id, actionSummary, now); err != nil {
						return false, err
					}
				}
				lastActionAt = &now
			}
			stateName = "triggered"
			if incidentState == "suppressed" {
				stateName = "suppressed"
			}
			lastMatchedAt = &now
		} else {
			stateName = "pending"
		}
	} else {
		consecutive = 0
		firstMatchedAt = nil
		lastMatchedAt = nil
		stateName = firstText(strings.ToLower(cleanText(evaluation["state"])), "normal")
		if incident != nil && coerceInt64(incident["id"]) > 0 {
			incidentID := coerceInt64(incident["id"])
			autoResolveAfter := int64(maxInt(int(coerceInt64(record["auto_resolve_after_seconds"])), 0))
			reason := "cleared"
			if staleState {
				reason = "telemetry_stale"
			}
			if staleState {
				if clearStartedAt == nil {
					clearStartedAt = &now
				}
				currentIncidentID = &incidentID
			} else if watchdogRuntimeShouldPurgeClearedIncident(record, staleState, reason) {
				if err := r.store.deleteWatchdogIncident(ctx, incidentID); err != nil {
					return false, err
				}
				clearStartedAt = nil
			} else {
				if clearStartedAt == nil {
					clearStartedAt = &now
				}
				currentIncidentID = &incidentID
				if autoResolveAfter <= 0 || now-*clearStartedAt >= autoResolveAfter {
					if err := r.store.resolveWatchdogIncident(ctx, incidentID, reason, now); err != nil {
						return false, err
					}
					currentIncidentID = nil
					clearStartedAt = nil
				}
			}
		} else {
			clearStartedAt = nil
			currentIncidentID = nil
		}
	}
	err := r.store.upsertWatchdogRuntimeState(ctx, watchdogRuntimeStateUpdate{
		WatchdogID:        watchdogID,
		DeviceGUID:        normalizeCanonicalGUID(firstPresentAny(device["guid"], device["agent_guid"], device["device_guid"])),
		Hostname:          cleanText(device["hostname"]),
		SiteID:            int64PtrFromAny(device["site_id"]),
		State:             stateName,
		Consecutive:       consecutive,
		FirstMatchedAt:    firstMatchedAt,
		ClearStartedAt:    clearStartedAt,
		LastEvaluatedAt:   now,
		LastMatchedAt:     lastMatchedAt,
		LastSample:        asStringAnyMap(evaluation["sample"]),
		CurrentIncidentID: currentIncidentID,
		LastActionAt:      lastActionAt,
	})
	return true, err
}

func (r *watchdogRuntime) markWatchdogTargetRemoved(ctx context.Context, watchdogID int64, priorState map[string]any, now int64) error {
	if incidentID := coerceInt64(priorState["current_incident_id"]); incidentID > 0 {
		if err := r.store.resolveWatchdogIncident(ctx, incidentID, "target_removed", now); err != nil {
			return err
		}
	}
	return r.store.upsertWatchdogRuntimeState(ctx, watchdogRuntimeStateUpdate{
		WatchdogID:        watchdogID,
		DeviceGUID:        cleanText(priorState["device_guid"]),
		Hostname:          cleanText(priorState["hostname"]),
		SiteID:            int64PtrFromAny(priorState["site_id"]),
		State:             "normal",
		LastEvaluatedAt:   now,
		LastSample:        map[string]any{},
		CurrentIncidentID: nil,
		LastActionAt:      int64PtrFromAny(priorState["last_action_at"]),
	})
}

func (r *watchdogRuntime) runWatchdogActions(ctx context.Context, record map[string]any, device map[string]any, evaluation map[string]any, incident map[string]any) []map[string]any {
	actionPayload := asStringAnyMap(record["actions"])
	results := []map[string]any{}
	for _, rawAction := range anySlice(actionPayload["actions"]) {
		action := asStringAnyMap(rawAction)
		if len(action) == 0 || !boolDefault(action["enabled"], true) {
			continue
		}
		actionType := strings.ToLower(cleanText(action["type"]))
		switch actionType {
		case "notification":
			title := firstText(cleanText(action["title"]), fmt.Sprintf("%s triggered", cleanText(record["name"])))
			message := firstText(cleanText(evaluation["message"]), fmt.Sprintf("%s triggered a watchdog incident.", cleanText(device["hostname"])))
			if r.realtime != nil {
				_ = r.realtime.emit("borealis_notification", map[string]any{
					"title":   title,
					"message": message,
					"variant": firstText(strings.ToLower(cleanText(action["variant"])), "warning"),
					"source":  "watchdog",
				})
			}
			results = append(results, map[string]any{"type": actionType, "status": "sent", "message": title})
		case "do_nothing":
			results = append(results, map[string]any{"type": actionType, "status": "noop", "message": "Incident recorded without notification or remediation."})
		case "service_control":
			results = append(results, r.dispatchWatchdogServiceControl(ctx, device, action))
		case "assembly":
			results = append(results, r.dispatchWatchdogAssembly(ctx, device, action, incident))
		}
	}
	return results
}

func (r *watchdogRuntime) dispatchWatchdogServiceControl(ctx context.Context, device map[string]any, action map[string]any) map[string]any {
	hostname := cleanText(device["hostname"])
	serviceName := cleanSingleLine(cleanText(action["service_name"]))
	serviceAction := normalizeServiceActionValue(action["action"])
	if hostname == "" || serviceName == "" || serviceAction == "" {
		return map[string]any{"type": "service_control", "status": "failed", "message": "Service control action is incomplete."}
	}
	snapshot, status, err := r.store.loadDeviceServices(ctx, watchdogRuntimeProfile(), hostname)
	if err != nil || status != http.StatusOK {
		return map[string]any{"type": "service_control", "status": "failed", "message": firstText(errorString(err), "Device services are unavailable.")}
	}
	if snapshot.Route == nil {
		return map[string]any{"type": "service_control", "status": "failed", "message": "No agent socket is connected for service remediation."}
	}
	requestedAt := time.Now().Unix()
	updatedServices, reportedAt, found := markServiceControlPending(snapshot.Services, serviceName, serviceAction, requestedAt, "watchdog", snapshot.Reported)
	if !found {
		return map[string]any{"type": "service_control", "status": "failed", "message": fmt.Sprintf("Service %s was not found.", serviceName)}
	}
	eventPayload := map[string]any{
		"hostname":      snapshot.Hostname,
		"agent_id":      snapshot.AgentID,
		"service_name":  serviceName,
		"action":        serviceAction,
		"requested_at":  requestedAt,
		"requested_by":  "watchdog",
		"service_label": serviceActionLabel(serviceAction),
	}
	result, _, workerErr := emitWorkerHostServiceEvent(ctx, r.auth, snapshot.Route, map[string]any{
		"hostname":     snapshot.Hostname,
		"service_mode": "system",
		"event_name":   "service_control_action",
		"payload":      eventPayload,
	}, 6*time.Second)
	if workerErr != nil || !boolFromAny(result["emitted"]) {
		return map[string]any{"type": "service_control", "status": "failed", "message": "No agent socket is connected for service remediation."}
	}
	if err := r.store.persistDeviceServices(ctx, snapshot.Hostname, updatedServices, reportedAt); err != nil {
		return map[string]any{"type": "service_control", "status": "failed", "message": "Service remediation was queued but service inventory update failed."}
	}
	if r.realtime != nil {
		_ = r.realtime.emit("device_services_changed", map[string]any{"hostname": snapshot.Hostname, "change": "updated", "source": "watchdog"})
	}
	return map[string]any{"type": "service_control", "status": "queued", "message": fmt.Sprintf("%s queued for %s.", serviceActionLabel(serviceAction), serviceName)}
}

func (r *watchdogRuntime) dispatchWatchdogAssembly(ctx context.Context, device map[string]any, action map[string]any, incident map[string]any) map[string]any {
	assemblyGUID := assemblyCoerceGUID(action["assembly_guid"])
	if assemblyGUID == "" {
		return map[string]any{"type": "assembly", "status": "failed", "message": "Assembly could not be resolved."}
	}
	item, found, err := r.store.getAssembly(ctx, assemblyGUID, true)
	if err != nil {
		return map[string]any{"type": "assembly", "status": "failed", "message": err.Error()}
	}
	if !found {
		return map[string]any{"type": "assembly", "status": "failed", "message": "Assembly could not be resolved."}
	}
	assemblyType := strings.ToLower(firstText(cleanText(item["assembly_type"]), cleanText(item["assembly_subtype"]), "script"))
	if assemblyType == "workflow" {
		return r.dispatchWatchdogWorkflowAssembly(ctx, device, action, item, incident)
	}
	if assemblyType == "ansible" {
		return r.dispatchWatchdogAnsibleAssembly(ctx, device, action, item)
	}
	return r.dispatchWatchdogScriptAssembly(ctx, device, action, item)
}

func (r *watchdogRuntime) dispatchWatchdogScriptAssembly(ctx context.Context, device map[string]any, action map[string]any, item map[string]any) map[string]any {
	hostname := cleanText(device["hostname"])
	snapshot, status, err := r.store.loadDeviceProcessContext(ctx, watchdogRuntimeProfile(), hostname)
	if err != nil || status != http.StatusOK {
		return map[string]any{"type": "assembly", "assembly_type": "script", "status": "failed", "message": firstText(errorString(err), "Device route unavailable.")}
	}
	payload := quickRunPayloadMap(item)
	if payload == nil {
		return map[string]any{"type": "assembly", "assembly_type": "script", "status": "failed", "message": "Assembly payload could not be loaded."}
	}
	scriptPath := firstText(quickRunItemPath(item), "Scripts/Watchdog")
	doc := quickRunLoadAssemblyDocument(scriptPath, "powershell", payload)
	if !quickRunSupportedScriptType(quickRunNormalizeScriptType(doc["type"])) {
		return map[string]any{"type": "assembly", "assembly_type": "script", "status": "failed", "message": "Unsupported quick-run script type."}
	}
	results, code, response := dispatchQuickRun(ctx, r.auth, r.realtime, r.store, []quickRunTarget{{Hostname: firstText(snapshot.Hostname, hostname), Context: snapshot}}, doc, scriptPath, "watchdog", quickRunVariableOverrides(action["variable_values"]), map[string]any{
		"run_mode":        action["run_mode"],
		"assembly_source": "watchdog",
		"assembly_guid":   assemblyCoerceGUID(firstNonEmptyAny(item["assembly_guid"], action["assembly_guid"])),
	})
	if code != 0 {
		return map[string]any{"type": "assembly", "assembly_type": "script", "status": "failed", "message": firstText(cleanText(response["message"]), cleanText(response["error"]), "Script assembly dispatch failed.")}
	}
	if len(results) == 0 {
		return map[string]any{"type": "assembly", "assembly_type": "script", "status": "failed", "message": "Script assembly dispatch returned no result."}
	}
	result := results[0]
	statusText := "queued"
	if strings.EqualFold(cleanText(result["status"]), "failed") {
		statusText = "failed"
	}
	return map[string]any{"type": "assembly", "assembly_type": "script", "status": statusText, "activity_id": result["job_id"], "message": firstText(cleanText(result["error"]), "Script assembly queued successfully.")}
}

func (r *watchdogRuntime) dispatchWatchdogWorkflowAssembly(ctx context.Context, device map[string]any, action map[string]any, item map[string]any, incident map[string]any) map[string]any {
	workflowGUID := assemblyCoerceGUID(firstNonEmptyAny(item["assembly_guid"], action["assembly_guid"]))
	result, _, err := r.store.startWorkflowRun(ctx, workflowStartRequest{
		WorkflowGUID: workflowGUID,
		SourceType:   "manual",
		SourceMetadata: map[string]any{
			"trigger_source":  "watchdog",
			"incident_id":     incident["id"],
			"watchdog_id":     incident["watchdog_id"],
			"hostname":        cleanText(device["hostname"]),
			"device_guid":     normalizeCanonicalGUID(firstPresentAny(device["guid"], device["agent_guid"], device["device_guid"])),
			"variable_values": asStringAnyMap(action["variable_values"]),
		},
		CreatedBy:     "watchdog",
		ExecuteAsync:  true,
		RunnerProfile: watchdogRuntimeProfile(),
		Auth:          r.auth,
	})
	if err != nil {
		return map[string]any{"type": "assembly", "assembly_type": "workflow", "status": "failed", "message": err.Error()}
	}
	if result.ShouldExecute {
		go executeWorkflowRunBackground(r.auth, result.RunID, watchdogRuntimeProfile())
	}
	return map[string]any{"type": "assembly", "assembly_type": "workflow", "status": "queued", "run_id": result.RunID, "message": "Workflow queued successfully."}
}

func (r *watchdogRuntime) dispatchWatchdogAnsibleAssembly(ctx context.Context, device map[string]any, action map[string]any, item map[string]any) map[string]any {
	executionContext := strings.ToLower(firstText(cleanText(action["execution_context"]), "local"))
	if executionContext != "local" {
		return map[string]any{"type": "assembly", "assembly_type": "ansible", "status": "failed", "message": "Watchdog Ansible remediation currently supports local execution only."}
	}
	payload := quickRunPayloadMap(item)
	if payload == nil {
		return map[string]any{"type": "assembly", "assembly_type": "ansible", "status": "failed", "message": "Playbook payload could not be loaded."}
	}
	playbookPath := firstText(quickRunItemPath(item), "Ansible_Playbooks/Watchdog")
	doc := quickRunLoadAssemblyDocument(playbookPath, "ansible", payload)
	route, err := r.store.watchdogAnsibleRoute(ctx, int64PtrFromAny(device["site_id"]))
	if err != nil {
		return map[string]any{"type": "assembly", "assembly_type": "ansible", "status": "failed", "message": err.Error()}
	}
	queueRun := map[string]any{
		"hostname":          "engine",
		"playbook_rel_path": playbookPath,
		"playbook_name":     firstText(cleanText(doc["name"]), cleanText(item["display_name"]), "Watchdog Playbook"),
		"playbook_content":  strings.ReplaceAll(cleanText(doc["script"]), "\r\n", "\n"),
		"variable_values":   asStringAnyMap(action["variable_values"]),
		"payload_files":     quickRunFiles(doc["files"]),
		"target_specifications": []any{map[string]any{
			"hostname":           "engine",
			"inventory_hostname": "engine",
			"site_group":         "site_local",
			"site_id":            coerceInt64(device["site_id"]),
			"host_vars":          map[string]any{"ansible_connection": "local"},
		}},
		"runtime_files": []any{},
		"source":        "watchdog",
		"connection":    "local",
	}
	response, errPayload := postWatchdogWorkerJSON(ctx, r.auth, route, "/automation/ansible/run", map[string]any{"queue_run": queueRun}, 10*time.Second)
	if errPayload != nil {
		return map[string]any{"type": "assembly", "assembly_type": "ansible", "status": "failed", "message": firstText(cleanText(errPayload["message"]), cleanText(errPayload["error"]), "Ansible playbook dispatch failed.")}
	}
	runID := cleanText(response["run_id"])
	if runID == "" {
		return map[string]any{"type": "assembly", "assembly_type": "ansible", "status": "failed", "message": "Site-worker did not return an Ansible run id."}
	}
	return map[string]any{"type": "assembly", "assembly_type": "ansible", "status": "queued", "run_id": runID, "message": "Ansible playbook queued successfully."}
}

func (r *watchdogRuntime) broadcast(hostname string, watchdogID int64) {
	broadcastWatchdogRefresh(context.Background(), r.realtime, hostname, watchdogID)
}

func runtimeRealtimeFromBroadcaster(broadcaster watchdogIncidentBroadcaster) *operatorRealtimeHub {
	if realtime, ok := broadcaster.(*operatorRealtimeHub); ok {
		return realtime
	}
	return nil
}

type watchdogRuntimeStateUpdate struct {
	WatchdogID        int64
	DeviceGUID        string
	Hostname          string
	SiteID            *int64
	State             string
	Consecutive       int64
	FirstMatchedAt    *int64
	ClearStartedAt    *int64
	LastEvaluatedAt   int64
	LastMatchedAt     *int64
	LastSample        map[string]any
	CurrentIncidentID *int64
	LastActionAt      *int64
}

func (s *postgresOperatorStore) loadWatchdogRuntimeStates(ctx context.Context, watchdogID int64) (map[string]map[string]any, error) {
	result := map[string]map[string]any{}
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
	for rows.Next() {
		var row watchdogDeviceStateRow
		if err := rows.Scan(&row.WatchdogID, &row.DeviceGUID, &row.Hostname, &row.SiteID, &row.State, &row.Consecutive, &row.FirstMatchedAt, &row.ClearStartedAt, &row.LastEvaluatedAt, &row.LastMatchedAt, &row.LastSampleJSON, &row.CurrentIncidentID, &row.LastActionAt, &row.UpdatedAt); err != nil {
			return nil, err
		}
		payload := watchdogDeviceStatePayload(row)
		if hostname := strings.ToLower(cleanText(payload["hostname"])); hostname != "" {
			result[hostname] = payload
		}
	}
	return result, rows.Err()
}

func (s *postgresOperatorStore) loadWatchdogRuntimeOverrides(ctx context.Context, watchdogID int64, now int64) (map[string]map[string]any, error) {
	result := map[string]map[string]any{}
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
	`, watchdogID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var row watchdogDeviceOverrideRow
		if err := rows.Scan(&row.WatchdogID, &row.ID, &row.DeviceGUID, &row.Hostname, &row.SiteID, &row.State, &row.Reason, &row.CreatedBy, &row.CreatedAt, &row.ExpiresAt, &row.UpdatedAt); err != nil {
			return nil, err
		}
		payload := watchdogDeviceOverridePayload(row)
		if hostname := strings.ToLower(cleanText(payload["hostname"])); hostname != "" {
			if _, exists := result[hostname]; !exists {
				result[hostname] = payload
			}
		}
	}
	return result, rows.Err()
}

func (s *postgresOperatorStore) loadWatchdogRuntimeActiveIncidents(ctx context.Context, watchdogID int64) (map[string]map[string]any, error) {
	result := map[string]map[string]any{}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, `
		SELECT watchdog_id, id, device_guid, hostname, site_id, severity, state, title, message,
		       sample_json, rule_summary_json, action_summary_json, opened_at, updated_at,
		       resolved_at, resolution_reason, acknowledged_at, acknowledged_by, trigger_count
		  FROM engine.watchdog_incidents
		 WHERE watchdog_id=$1
		   AND state IN ('open', 'suppressed')
	  ORDER BY updated_at DESC, id DESC
	`, watchdogID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var row watchdogCompactIncidentRow
		if err := rows.Scan(&row.WatchdogID, &row.ID, &row.DeviceGUID, &row.Hostname, &row.SiteID, &row.Severity, &row.State, &row.Title, &row.Message, &row.SampleJSON, &row.RuleSummaryJSON, &row.ActionSummaryJSON, &row.OpenedAt, &row.UpdatedAt, &row.ResolvedAt, &row.ResolutionReason, &row.AcknowledgedAt, &row.AcknowledgedBy, &row.TriggerCount); err != nil {
			return nil, err
		}
		payload := watchdogCompactIncidentPayload(row)
		if hostname := strings.ToLower(cleanText(payload["hostname"])); hostname != "" {
			if _, exists := result[hostname]; !exists {
				result[hostname] = payload
			}
		}
	}
	return result, rows.Err()
}

func (s *postgresOperatorStore) createOrUpdateWatchdogIncident(ctx context.Context, watchdog map[string]any, device map[string]any, current map[string]any, evaluation map[string]any, actionSummary []map[string]any, now int64) (map[string]any, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer rollbackQuietly(tx)
	payload, err := createOrUpdateWatchdogIncidentTx(ctx, tx, watchdog, device, current, evaluation, actionSummary, now)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return payload, nil
}

func createOrUpdateWatchdogIncidentTx(ctx context.Context, tx *sql.Tx, watchdog map[string]any, device map[string]any, current map[string]any, evaluation map[string]any, actionSummary []map[string]any, now int64) (map[string]any, error) {
	hostname := cleanText(device["hostname"])
	title := fmt.Sprintf("%s on %s", cleanText(watchdog["name"]), hostname)
	message := firstText(cleanText(evaluation["message"]), title)
	sampleJSON := mustJSONString(asStringAnyMap(evaluation["sample"]))
	rulesJSON := mustJSONString(anySlice(evaluation["rule_results"]))
	actionsJSON := mustJSONString(actionSummary)
	if current != nil && coerceInt64(current["id"]) > 0 {
		incidentID := coerceInt64(current["id"])
		if _, err := tx.ExecContext(ctx, `
			UPDATE engine.watchdog_incidents
			   SET message=$1,
			       sample_json=$2,
			       rule_summary_json=$3,
			       action_summary_json=$4,
			       updated_at=$5,
			       trigger_count=COALESCE(trigger_count, 0) + 1
			 WHERE id=$6
		`, message, sampleJSON, rulesJSON, actionsJSON, now, incidentID); err != nil {
			return nil, err
		}
		payload := copyMap(current)
		payload["message"] = message
		payload["sample"] = asStringAnyMap(evaluation["sample"])
		payload["rule_summary"] = anySlice(evaluation["rule_results"])
		payload["action_summary"] = actionSummary
		payload["updated_at"] = now
		payload["trigger_count"] = coerceInt64(current["trigger_count"]) + 1
		return payload, nil
	}
	var incidentID int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO engine.watchdog_incidents (
			watchdog_id, device_guid, hostname, site_id, severity, state, title, message,
			sample_json, rule_summary_json, action_summary_json, opened_at, updated_at, trigger_count
		) VALUES ($1,$2,$3,$4,$5,'open',$6,$7,$8,$9,$10,$11,$12,1)
		RETURNING id
	`, coerceInt64(watchdog["id"]), normalizeCanonicalGUID(firstPresentAny(device["guid"], device["agent_guid"], device["device_guid"])), hostname, watchdogNullableInt64(device["site_id"]), normalizeWatchdogSeverity(cleanText(watchdog["severity"])), title, message, sampleJSON, rulesJSON, actionsJSON, now, now).Scan(&incidentID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"id":             incidentID,
		"watchdog_id":    coerceInt64(watchdog["id"]),
		"device_guid":    normalizeCanonicalGUID(firstPresentAny(device["guid"], device["agent_guid"], device["device_guid"])),
		"hostname":       hostname,
		"site_id":        watchdogNullableInt64(device["site_id"]),
		"severity":       normalizeWatchdogSeverity(cleanText(watchdog["severity"])),
		"state":          "open",
		"title":          title,
		"message":        message,
		"sample":         asStringAnyMap(evaluation["sample"]),
		"rule_summary":   anySlice(evaluation["rule_results"]),
		"action_summary": actionSummary,
		"opened_at":      now,
		"updated_at":     now,
		"trigger_count":  int64(1),
	}, nil
}

func (s *postgresOperatorStore) updateWatchdogIncidentActionSummary(ctx context.Context, incidentID int64, actionSummary []map[string]any, now int64) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	_, err = conn.ExecContext(ctx, `
		UPDATE engine.watchdog_incidents
		   SET action_summary_json=$1,
		       updated_at=$2
		 WHERE id=$3
	`, mustJSONString(actionSummary), now, incidentID)
	return err
}

func (s *postgresOperatorStore) upsertWatchdogRuntimeState(ctx context.Context, update watchdogRuntimeStateUpdate) error {
	if update.WatchdogID <= 0 || cleanText(update.Hostname) == "" {
		return nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	_, err = conn.ExecContext(ctx, `
		INSERT INTO engine.watchdog_device_state (
			watchdog_id, device_guid, hostname, site_id, state, consecutive_matches,
			first_matched_at, clear_started_at, last_evaluated_at, last_matched_at,
			last_sample_json, current_incident_id, last_action_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT(watchdog_id, hostname) DO UPDATE SET
			device_guid=EXCLUDED.device_guid,
			site_id=EXCLUDED.site_id,
			state=EXCLUDED.state,
			consecutive_matches=EXCLUDED.consecutive_matches,
			first_matched_at=EXCLUDED.first_matched_at,
			clear_started_at=EXCLUDED.clear_started_at,
			last_evaluated_at=EXCLUDED.last_evaluated_at,
			last_matched_at=EXCLUDED.last_matched_at,
			last_sample_json=EXCLUDED.last_sample_json,
			current_incident_id=EXCLUDED.current_incident_id,
			last_action_at=EXCLUDED.last_action_at,
			updated_at=EXCLUDED.updated_at
	`, update.WatchdogID, update.DeviceGUID, update.Hostname, update.SiteID, firstText(update.State, "normal"), update.Consecutive, update.FirstMatchedAt, update.ClearStartedAt, update.LastEvaluatedAt, update.LastMatchedAt, mustJSONString(update.LastSample), update.CurrentIncidentID, update.LastActionAt, update.LastEvaluatedAt)
	return err
}

func (s *postgresOperatorStore) updateWatchdogEvaluationMetadata(ctx context.Context, watchdogID int64, lastEvaluatedAt int64, targetDeviceCount int) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	_, err = conn.ExecContext(ctx, `
		UPDATE engine.watchdogs
		   SET last_evaluated_at=$1,
		       target_device_count=$2
		 WHERE id=$3
	`, lastEvaluatedAt, maxInt(targetDeviceCount, 0), watchdogID)
	return err
}

func (s *postgresOperatorStore) setWatchdogIncidentState(ctx context.Context, incidentID int64, state string, reason string, now int64) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	_, err = conn.ExecContext(ctx, `
		UPDATE engine.watchdog_incidents
		   SET state=$1,
		       resolved_at=NULL,
		       resolution_reason=$2,
		       updated_at=$3
		 WHERE id=$4
	`, firstText(strings.ToLower(cleanText(state)), "open"), cleanSingleLine(reason), now, incidentID)
	return err
}

func (s *postgresOperatorStore) resolveWatchdogIncident(ctx context.Context, incidentID int64, reason string, now int64) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	_, err = conn.ExecContext(ctx, `
		UPDATE engine.watchdog_incidents
		   SET state='resolved',
		       resolved_at=$1,
		       resolution_reason=$2,
		       updated_at=$1
		 WHERE id=$3
	`, now, cleanSingleLine(reason), incidentID)
	return err
}

func (s *postgresOperatorStore) deleteWatchdogIncident(ctx context.Context, incidentID int64) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "UPDATE engine.watchdog_device_state SET current_incident_id=NULL WHERE current_incident_id=$1", incidentID); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, "DELETE FROM engine.watchdog_incidents WHERE id=$1", incidentID)
	return err
}

func (s *postgresOperatorStore) purgeResolvedOfflineWatchdogIncidents(ctx context.Context) (int, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return 0, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, `
		SELECT i.id, w.criteria_json
		  FROM engine.watchdog_incidents AS i
		  JOIN engine.watchdogs AS w ON w.id = i.watchdog_id
		 WHERE i.state='resolved'
	`)
	if err != nil {
		return 0, err
	}
	ids := []int64{}
	for rows.Next() {
		var id sql.NullInt64
		var criteriaJSON sql.NullString
		if err := rows.Scan(&id, &criteriaJSON); err != nil {
			_ = rows.Close()
			return 0, err
		}
		if id.Valid && watchdogRuntimeRulesAreOfflineOnly(asStringAnyMap(parseJSON(criteriaJSON))) {
			ids = append(ids, id.Int64)
		}
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer rollbackQuietly(tx)
	query, params := inClauseQuery("UPDATE engine.watchdog_device_state SET current_incident_id=NULL WHERE current_incident_id IN (%s)", ids)
	if _, err := tx.ExecContext(ctx, query, params...); err != nil {
		return 0, err
	}
	query, params = inClauseQuery("DELETE FROM engine.watchdog_incidents WHERE id IN (%s)", ids)
	if _, err := tx.ExecContext(ctx, query, params...); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(ids), nil
}

func (s *postgresOperatorStore) watchdogAnsibleRoute(ctx context.Context, siteID *int64) (*agentWorkerRoute, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	if siteID != nil && *siteID > 0 {
		route, err := fetchAgentWorkerRoute(ctx, conn, *siteID)
		if err != nil || route != nil {
			return route, err
		}
		return nil, fmt.Errorf("No active site-worker route is available for site %d.", *siteID)
	}
	var route agentWorkerRoute
	err = conn.QueryRowContext(ctx, `
		SELECT worker_guid, site_id, route_path_prefix, upstream_scheme, upstream_host, upstream_port, generation
		  FROM engine.job_scheduler_worker_routes
		 WHERE status='active'
	  ORDER BY updated_at DESC, generation DESC
		 LIMIT 1
	`).Scan(&route.WorkerGUID, &route.SiteID, &route.RoutePathPrefix, &route.UpstreamScheme, &route.UpstreamHost, &route.UpstreamPort, &route.Generation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("No active site-worker route is available for Engine-side Ansible execution.")
	}
	if err != nil {
		return nil, err
	}
	return &route, nil
}

func postWatchdogWorkerJSON(ctx context.Context, auth *authService, route *agentWorkerRoute, path string, body map[string]any, timeout time.Duration) (map[string]any, map[string]any) {
	if auth == nil || route == nil {
		return nil, map[string]any{"error": "site_worker_unavailable"}
	}
	target := workerInternalURL(route, path)
	if target == "" {
		return nil, map[string]any{"error": "site_worker_unavailable"}
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, map[string]any{"error": "invalid_request", "message": err.Error()}
	}
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return nil, map[string]any{"error": "worker_request_failed", "message": err.Error()}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(internalTokenHeader, goInternalToken(auth.verifier.secret))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, map[string]any{"error": "site_worker_unavailable", "message": err.Error()}
	}
	defer resp.Body.Close()
	var response map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&response); err != nil {
		return nil, map[string]any{"error": "invalid_worker_response", "message": err.Error()}
	}
	if resp.StatusCode >= 400 {
		if cleanText(response["error"]) == "" {
			response["error"] = "site_worker_error"
		}
		return nil, response
	}
	return response, nil
}

func watchdogRuntimeContext(parent context.Context, auth *authService) (context.Context, context.CancelFunc) {
	timeout := 2 * time.Minute
	if auth != nil && auth.timeout > 0 {
		timeout = maxDuration(auth.timeout*4, timeout)
	}
	return context.WithTimeout(parent, timeout)
}

func watchdogRuntimeProfile() operatorProfile {
	return operatorProfile{Username: "watchdog", Role: "Admin"}
}

func watchdogRuntimeValidOverrideState(value string) bool {
	switch strings.ToLower(cleanText(value)) {
	case "suppressed", "disabled":
		return true
	default:
		return false
	}
}

func watchdogRuntimeShouldRunActions(record map[string]any, lastActionAt *int64, now int64) bool {
	cooldown := int64(watchdogDefaultCooldownSeconds)
	if parsed, ok := int64Value(record["cooldown_seconds"]); ok {
		cooldown = parsed
	}
	return lastActionAt == nil || cooldown <= 0 || now-*lastActionAt >= cooldown
}

func watchdogRuntimeShouldPurgeClearedIncident(record map[string]any, stale bool, reason string) bool {
	return !stale && strings.EqualFold(cleanText(reason), "cleared") && watchdogRuntimeRulesAreOfflineOnly(asStringAnyMap(record["criteria"]))
}

func watchdogRuntimeRulesAreOfflineOnly(criteria map[string]any) bool {
	rules := anySlice(criteria["rules"])
	if len(rules) == 0 {
		return false
	}
	for _, raw := range rules {
		if !strings.EqualFold(cleanText(asStringAnyMap(raw)["type"]), "device_offline") {
			return false
		}
	}
	return true
}

func int64PtrFromAny(value any) *int64 {
	if parsed, ok := int64Value(value); ok {
		return &parsed
	}
	return nil
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func maxDuration(left time.Duration, right time.Duration) time.Duration {
	if left > right {
		return left
	}
	return right
}
