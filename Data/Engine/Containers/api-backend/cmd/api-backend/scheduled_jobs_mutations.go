package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const onboardingComponentKind = "device_onboarding"
const patchInstallComponentKind = "patch_install"

type scheduledJobMutationValues struct {
	Name                string
	Components          []any
	Targets             []any
	ScheduleType        string
	StartTS             sql.NullInt64
	DurationStopEnabled bool
	Expiration          string
	ExecutionContext    string
	CredentialID        sql.NullInt64
	UseServiceAccount   bool
	JobKind             string
	Enabled             bool
}

func (s *postgresOperatorStore) createScheduledJob(ctx context.Context, profile operatorProfile, payload map[string]any) (map[string]any, int, error) {
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	values, warning, body, status, err := buildScheduledJobCreateValues(ctx, conn, profile, allowedSiteIDs, payload)
	if err != nil {
		return body, status, err
	}
	now := time.Now().Unix()
	componentsJSON, targetsJSON, err := scheduledJobMutationJSON(values.Components, values.Targets)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	defer rollbackQuietly(tx)

	var row scheduledJobRow
	err = tx.QueryRowContext(ctx, `
		INSERT INTO engine.scheduled_jobs(
			name, components_json, targets_json, schedule_type, start_ts,
			duration_stop_enabled, expiration, execution_context, credential_id,
			use_service_account, job_kind, enabled, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13)
		RETURNING id, name, components_json, targets_json, schedule_type, start_ts,
		          duration_stop_enabled, expiration, execution_context, credential_id,
		          use_service_account, enabled, created_at, updated_at, job_kind
	`,
		values.Name,
		componentsJSON,
		targetsJSON,
		values.ScheduleType,
		sqlNullIntArg(values.StartTS),
		boolIntArg(values.DurationStopEnabled),
		values.Expiration,
		values.ExecutionContext,
		sqlNullIntArg(values.CredentialID),
		boolIntArg(values.UseServiceAccount),
		values.JobKind,
		boolIntArg(values.Enabled),
		now,
	).Scan(&row.ID, &row.Name, &row.ComponentsJSON, &row.TargetsJSON, &row.ScheduleType, &row.StartTS, &row.DurationStopEnabled, &row.Expiration, &row.ExecutionContext, &row.CredentialID, &row.UseServiceAccount, &row.Enabled, &row.CreatedAt, &row.UpdatedAt, &row.JobKind)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if err := tx.Commit(); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	job, err := scheduledJobPayload(ctx, conn, row)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	result := map[string]any{"job": job}
	for key, value := range warning {
		result[key] = value
	}
	return result, http.StatusOK, nil
}

func (s *postgresOperatorStore) updateScheduledJob(ctx context.Context, profile operatorProfile, jobID int64, payload map[string]any) (map[string]any, int, error) {
	if !scheduledJobUpdateHasRecognizedFields(payload) {
		return map[string]any{"error": "no fields to update"}, http.StatusBadRequest, errors.New("no fields to update")
	}
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	current, found, err := loadScheduledJobVisibility(ctx, conn, jobID, allowedSiteIDs)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if !found {
		return map[string]any{"error": "not found"}, http.StatusNotFound, nil
	}
	values, warning, body, status, err := buildScheduledJobUpdateValues(ctx, conn, profile, allowedSiteIDs, payload, current)
	if err != nil {
		return body, status, err
	}
	componentsJSON, targetsJSON, err := scheduledJobMutationJSON(values.Components, values.Targets)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	defer rollbackQuietly(tx)
	result, err := tx.ExecContext(ctx, `
		UPDATE engine.scheduled_jobs
		   SET name=$1,
		       components_json=$2,
		       targets_json=$3,
		       schedule_type=$4,
		       start_ts=$5,
		       duration_stop_enabled=$6,
		       expiration=$7,
		       execution_context=$8,
		       credential_id=$9,
		       use_service_account=$10,
		       enabled=$11,
		       job_kind=$12,
		       updated_at=$13
		 WHERE id=$14
	`,
		values.Name,
		componentsJSON,
		targetsJSON,
		values.ScheduleType,
		sqlNullIntArg(values.StartTS),
		boolIntArg(values.DurationStopEnabled),
		values.Expiration,
		values.ExecutionContext,
		sqlNullIntArg(values.CredentialID),
		boolIntArg(values.UseServiceAccount),
		boolIntArg(values.Enabled),
		values.JobKind,
		time.Now().Unix(),
		jobID,
	)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return map[string]any{"error": "not found"}, http.StatusNotFound, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	rows, err := queryScheduledJobRows(ctx, conn, &jobID)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if len(rows) == 0 {
		return map[string]any{"error": "not found"}, http.StatusNotFound, nil
	}
	job, err := scheduledJobPayload(ctx, conn, rows[0])
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	response := map[string]any{"job": job}
	for key, value := range warning {
		response[key] = value
	}
	return response, http.StatusOK, nil
}

func buildScheduledJobCreateValues(ctx context.Context, conn *sql.Conn, profile operatorProfile, allowedSiteIDs []int64, payload map[string]any) (scheduledJobMutationValues, map[string]string, map[string]any, int, error) {
	jobKind := normalizeScheduledJobKind(cleanText(firstPresentAny(payload["job_kind"], payload["kind"])))
	components := scheduledAnySlice(payload["components"])
	if jobKind == scheduledJobKindOnboarding && len(components) == 0 {
		components = []any{map[string]any{"kind": onboardingComponentKind}}
	}
	targets, scopeErr, err := normalizeAndScopeScheduledTargets(ctx, conn, profile, allowedSiteIDs, firstPresentAny(payload["targets"], []any{}))
	if err != nil {
		return scheduledJobMutationValues{}, nil, nil, http.StatusInternalServerError, err
	}
	if scopeErr != "" {
		return scheduledJobMutationValues{}, nil, map[string]any{"error": "out_of_scope_targets", "message": scopeErr}, http.StatusForbidden, errors.New(scopeErr)
	}
	schedule := scheduledMapAny(payload["schedule"])
	duration := scheduledMapAny(payload["duration"])
	executionContext := strings.ToLower(firstText(cleanText(payload["execution_context"]), "system"))
	if jobKind == scheduledJobKindOnboarding {
		executionContext = "onboarding_local_network"
	}
	if jobKind == scheduledJobKindPatchInstall {
		executionContext = "system"
	}
	credentialID := scheduledOptionalPositiveInt64(payload["credential_id"])
	_, useServiceAccountProvided := payload["use_service_account"]
	useServiceAccount := normalizeScheduledAnsibleTransport(executionContext) == "winrm" && (!useServiceAccountProvided || boolFromAny(payload["use_service_account"]))
	if jobKind == scheduledJobKindOnboarding {
		useServiceAccount = false
	}
	if jobKind == scheduledJobKindPatchInstall {
		useServiceAccount = false
		credentialID = sql.NullInt64{}
	}
	enabled := true
	if raw, ok := payload["enabled"]; ok {
		enabled = boolFromAny(raw)
	}
	values := scheduledJobMutationValues{
		Name:                cleanText(payload["name"]),
		Components:          components,
		Targets:             targets,
		ScheduleType:        strings.ToLower(firstText(cleanText(firstPresentAny(schedule["type"], payload["schedule_type"])), "immediately")),
		StartTS:             scheduledParseTS(firstPresentAny(schedule["start"], payload["start"])),
		DurationStopEnabled: boolFromAny(firstPresentAny(duration["stopAfterEnabled"], payload["duration_stop_enabled"])),
		Expiration:          firstText(cleanText(firstPresentAny(duration["expiration"], payload["expiration"])), "no_expire"),
		ExecutionContext:    executionContext,
		CredentialID:        credentialID,
		UseServiceAccount:   useServiceAccount,
		JobKind:             jobKind,
		Enabled:             enabled,
	}
	return validateScheduledJobMutation(ctx, conn, values)
}

func buildScheduledJobUpdateValues(ctx context.Context, conn *sql.Conn, profile operatorProfile, allowedSiteIDs []int64, payload map[string]any, current scheduledJobRow) (scheduledJobMutationValues, map[string]string, map[string]any, int, error) {
	values := scheduledJobMutationValues{
		Name:                nullString(current.Name),
		Components:          parseJSONArray(current.ComponentsJSON),
		Targets:             parseJSONArray(current.TargetsJSON),
		ScheduleType:        firstText(nullString(current.ScheduleType), "immediately"),
		StartTS:             current.StartTS,
		DurationStopEnabled: boolInt64(current.DurationStopEnabled),
		Expiration:          firstText(nullString(current.Expiration), "no_expire"),
		ExecutionContext:    firstText(nullString(current.ExecutionContext), "system"),
		CredentialID:        current.CredentialID,
		UseServiceAccount:   boolInt64(current.UseServiceAccount),
		JobKind:             normalizeScheduledJobKind(nullString(current.JobKind)),
		Enabled:             boolInt64(current.Enabled),
	}
	if _, ok := payload["name"]; ok {
		values.Name = cleanText(payload["name"])
	}
	if _, ok := payload["components"]; ok {
		values.Components = scheduledAnySlice(payload["components"])
	}
	if _, ok := payload["targets"]; ok {
		targets, scopeErr, err := normalizeAndScopeScheduledTargets(ctx, conn, profile, allowedSiteIDs, payload["targets"])
		if err != nil {
			return scheduledJobMutationValues{}, nil, nil, http.StatusInternalServerError, err
		}
		if scopeErr != "" {
			return scheduledJobMutationValues{}, nil, map[string]any{"error": "out_of_scope_targets", "message": scopeErr}, http.StatusForbidden, errors.New(scopeErr)
		}
		values.Targets = targets
	}
	if _, ok := payload["schedule"]; ok {
		schedule := scheduledMapAny(payload["schedule"])
		values.ScheduleType = strings.ToLower(firstText(cleanText(firstPresentAny(schedule["type"], payload["schedule_type"])), "immediately"))
		values.StartTS = scheduledParseTS(firstPresentAny(schedule["start"], payload["start"]))
	} else if _, ok := payload["schedule_type"]; ok {
		values.ScheduleType = strings.ToLower(firstText(cleanText(payload["schedule_type"]), "immediately"))
		values.StartTS = scheduledParseTS(payload["start"])
	}
	if _, ok := payload["duration"]; ok {
		duration := scheduledMapAny(payload["duration"])
		values.DurationStopEnabled = boolFromAny(firstPresentAny(duration["stopAfterEnabled"], payload["duration_stop_enabled"]))
		values.Expiration = firstText(cleanText(firstPresentAny(duration["expiration"], payload["expiration"])), "no_expire")
	} else {
		if _, ok := payload["duration_stop_enabled"]; ok {
			values.DurationStopEnabled = boolFromAny(payload["duration_stop_enabled"])
		}
		if _, ok := payload["expiration"]; ok {
			values.Expiration = firstText(cleanText(payload["expiration"]), "no_expire")
		}
	}
	if _, ok := payload["execution_context"]; ok {
		values.ExecutionContext = strings.ToLower(firstText(cleanText(payload["execution_context"]), "system"))
		if normalizeScheduledAnsibleTransport(values.ExecutionContext) != "winrm" {
			values.UseServiceAccount = false
		}
	}
	if _, ok := payload["credential_id"]; ok {
		values.CredentialID = scheduledOptionalPositiveInt64(payload["credential_id"])
	}
	if _, ok := payload["use_service_account"]; ok {
		values.UseServiceAccount = boolFromAny(payload["use_service_account"])
	}
	if _, ok := payload["enabled"]; ok {
		values.Enabled = boolFromAny(payload["enabled"])
	}
	if _, ok := payload["job_kind"]; ok {
		values.JobKind = normalizeScheduledJobKind(cleanText(payload["job_kind"]))
	} else if _, ok := payload["kind"]; ok {
		values.JobKind = normalizeScheduledJobKind(cleanText(payload["kind"]))
	}
	if values.JobKind == scheduledJobKindOnboarding {
		values.ExecutionContext = "onboarding_local_network"
		values.UseServiceAccount = false
		if len(values.Components) == 0 {
			values.Components = []any{map[string]any{"kind": onboardingComponentKind}}
		}
	}
	if values.JobKind == scheduledJobKindPatchInstall {
		values.ExecutionContext = "system"
		values.UseServiceAccount = false
		values.CredentialID = sql.NullInt64{}
	}
	return validateScheduledJobMutation(ctx, conn, values)
}

func validateScheduledJobMutation(ctx context.Context, conn *sql.Conn, values scheduledJobMutationValues) (scheduledJobMutationValues, map[string]string, map[string]any, int, error) {
	if values.Name == "" || len(values.Components) == 0 {
		return values, nil, map[string]any{"error": "name and components are required"}, http.StatusBadRequest, errors.New("name and components are required")
	}
	workflowErr := ""
	isWorkflowJob := false
	if values.JobKind != scheduledJobKindOnboarding {
		workflowErr = validateScheduledWorkflowJobConfiguration(values.Components, values.Targets, values.ExecutionContext, values.CredentialID, values.UseServiceAccount)
		isWorkflowJob = workflowErr == "" && len(scheduledWorkflowComponents(values.Components)) > 0
	}
	if !isWorkflowJob && len(values.Targets) == 0 {
		return values, nil, map[string]any{"error": "targets required"}, http.StatusBadRequest, errors.New("targets required")
	}
	if values.JobKind == scheduledJobKindOnboarding {
		if errText := validateScheduledOnboardingConfig(values.Components, values.Targets, values.CredentialID); errText != "" {
			return values, nil, map[string]any{"error": errText}, http.StatusBadRequest, errors.New(errText)
		}
	} else if values.JobKind == scheduledJobKindPatchInstall {
		if errText := validateScheduledPatchInstallConfig(values.Components, values.Targets); errText != "" {
			return values, nil, map[string]any{"error": errText}, http.StatusBadRequest, errors.New(errText)
		}
		if errText, err := validateScheduledTargetsForSave(ctx, conn, values.Targets); err != nil {
			return values, nil, nil, http.StatusInternalServerError, err
		} else if errText != "" {
			return values, nil, map[string]any{"error": errText}, http.StatusBadRequest, errors.New(errText)
		}
		values.ExecutionContext = "system"
		values.UseServiceAccount = false
		values.CredentialID = sql.NullInt64{}
	} else {
		if workflowErr != "" {
			return values, nil, map[string]any{"error": workflowErr}, http.StatusBadRequest, errors.New(workflowErr)
		}
		if !isWorkflowJob {
			if errText, err := validateScheduledTargetsForSave(ctx, conn, values.Targets); err != nil {
				return values, nil, nil, http.StatusInternalServerError, err
			} else if errText != "" {
				return values, nil, map[string]any{"error": errText}, http.StatusBadRequest, errors.New(errText)
			}
		}
		if errText := validateScheduledComponentsForContext(values.Components, values.ExecutionContext); errText != "" {
			return values, nil, map[string]any{"error": errText}, http.StatusBadRequest, errors.New(errText)
		}
		if errText := validateScheduledRemoteCredentialForContext(values.Components, values.ExecutionContext, values.CredentialID, values.UseServiceAccount); errText != "" {
			return values, nil, map[string]any{"error": errText}, http.StatusBadRequest, errors.New(errText)
		}
	}
	warning := map[string]string(nil)
	if !isWorkflowJob && values.CredentialID.Valid {
		var err error
		warning, err = scheduledCredentialResetWarning(ctx, conn, values.CredentialID)
		if err != nil {
			return values, nil, nil, http.StatusInternalServerError, err
		}
		if warning != nil && values.Enabled {
			values.Enabled = false
		}
	}
	return values, warning, nil, http.StatusOK, nil
}

func normalizeAndScopeScheduledTargets(ctx context.Context, conn *sql.Conn, profile operatorProfile, allowedSiteIDs []int64, raw any) ([]any, string, error) {
	targets := normalizeScheduledTargetsForSave(raw)
	if allowedSiteIDs == nil {
		return targets, "", nil
	}
	if len(allowedSiteIDs) == 0 {
		return nil, "You do not have any sites assigned. Ask an administrator to assign at least one site.", nil
	}
	allowed := int64Set(allowedSiteIDs)
	scoped := make([]any, 0, len(targets))
	for _, target := range targets {
		entry, ok := target.(map[string]any)
		if !ok {
			hostname := cleanText(target)
			if hostname == "" {
				continue
			}
			siteID, siteName, resolvedHost, guid, err := lookupScheduledTargetDevice(ctx, conn, hostname, "")
			if err != nil {
				return nil, "", err
			}
			if siteID == nil {
				return nil, fmt.Sprintf("Target device '%s' is outside your assigned sites.", hostname), nil
			}
			if _, ok := allowed[*siteID]; !ok {
				return nil, fmt.Sprintf("Target device '%s' is outside your assigned sites.", hostname), nil
			}
			scoped = append(scoped, map[string]any{"kind": "device", "hostname": firstText(resolvedHost, hostname), "site_id": *siteID, "site_name": siteName, "device_guid": guid})
			continue
		}
		kind := strings.ToLower(cleanText(firstPresentAny(entry["kind"], entry["type"])))
		switch {
		case kind == "onboarding_scope":
			siteID, ok := int64Value(firstPresentAny(entry["site_id"], entry["siteId"]))
			if _, allowedSite := allowed[siteID]; !ok || !allowedSite {
				return nil, "Onboarding scope is outside your assigned sites.", nil
			}
			next := copyMap(entry)
			next["kind"] = "onboarding_scope"
			next["site_id"] = siteID
			next["allowed_site_ids"] = []int64{siteID}
			scoped = append(scoped, next)
		case scheduledIsFilterTarget(entry):
			next := copyMap(entry)
			next["kind"] = "filter"
			next["allowed_site_ids"] = append([]int64(nil), allowedSiteIDs...)
			scoped = append(scoped, next)
		default:
			hostname := cleanText(entry["hostname"])
			guid := normalizeCanonicalGUID(firstPresentAny(entry["device_guid"], entry["guid"]))
			siteID, hasSite := int64Value(entry["site_id"])
			siteName := cleanText(firstPresentAny(entry["site_name"], entry["site"]))
			resolvedHost := hostname
			resolvedGUID := guid
			if !hasSite {
				lookedUpSiteID, lookedUpSiteName, lookedUpHost, lookedUpGUID, err := lookupScheduledTargetDevice(ctx, conn, hostname, guid)
				if err != nil {
					return nil, "", err
				}
				if lookedUpSiteID != nil {
					siteID = *lookedUpSiteID
					hasSite = true
				}
				siteName = firstText(lookedUpSiteName, siteName)
				resolvedHost = firstText(lookedUpHost, resolvedHost)
				resolvedGUID = firstText(lookedUpGUID, resolvedGUID)
			}
			if _, allowedSite := allowed[siteID]; !hasSite || !allowedSite {
				label := firstText(resolvedHost, hostname, guid, "unknown")
				return nil, fmt.Sprintf("Target device '%s' is outside your assigned sites.", label), nil
			}
			scoped = append(scoped, map[string]any{"kind": "device", "device_guid": strings.ToLower(resolvedGUID), "hostname": resolvedHost, "site_id": siteID, "site_name": siteName})
		}
	}
	return scoped, "", nil
}

func normalizeScheduledTargetsForSave(raw any) []any {
	entries := scheduledAnySlice(raw)
	normalized := []any{}
	seenFilters := map[int64]struct{}{}
	seenDevices := map[string]struct{}{}
	seenOnboarding := map[string]struct{}{}
	includeAll := false
	for _, entry := range entries {
		if text, ok := entry.(string); ok {
			host := strings.TrimSpace(text)
			if host == "" {
				continue
			}
			key := strings.ToLower(host)
			if _, seen := seenDevices[key]; seen {
				continue
			}
			seenDevices[key] = struct{}{}
			normalized = append(normalized, host)
			continue
		}
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		kind := strings.ToLower(cleanText(firstPresentAny(item["kind"], item["type"])))
		if kind == "onboarding_scope" {
			siteID, ok := int64Value(firstPresentAny(item["site_id"], item["siteId"]))
			if !ok {
				continue
			}
			scopeEntries := scheduledStringEntries(firstPresentAny(item["entries"], item["scope"], item["targets"], item["discovery_scope"]))
			if len(scopeEntries) == 0 {
				continue
			}
			exclusions := scheduledStringEntries(firstPresentAny(item["exclusions"], item["exclude_entries"], item["exclusion_scope"], item["exclusionScope"]))
			key := fmt.Sprintf("onboarding:%d:%s:%s", siteID, strings.Join(scheduledLowerStringList(scopeEntries), "|"), strings.Join(scheduledLowerStringList(exclusions), "|"))
			if _, seen := seenOnboarding[key]; seen {
				continue
			}
			seenOnboarding[key] = struct{}{}
			normalized = append(normalized, map[string]any{"kind": "onboarding_scope", "site_id": siteID, "site_name": firstPresentAny(item["site_name"], item["site"], ""), "entries": scopeEntries, "exclusions": exclusions})
			continue
		}
		if kind == "all_devices" || boolFromAny(item["all_devices"]) {
			if includeAll {
				continue
			}
			includeAll = true
			normalized = append(normalized, map[string]any{"kind": "all_devices", "name": firstPresentAny(item["name"], "All Devices in Scope")})
			continue
		}
		if scheduledIsFilterTarget(item) {
			filterID, ok := int64Value(firstPresentAny(item["filter_id"], item["id"]))
			if !ok || filterID <= 0 {
				continue
			}
			if _, seen := seenFilters[filterID]; seen {
				continue
			}
			seenFilters[filterID] = struct{}{}
			normalized = append(normalized, map[string]any{"kind": "filter", "filter_id": filterID, "name": item["name"]})
			continue
		}
		host := cleanText(item["hostname"])
		if host == "" {
			continue
		}
		guid := strings.ToLower(normalizeCanonicalGUID(firstPresentAny(item["device_guid"], item["guid"])))
		siteID, hasSite := int64Value(item["site_id"])
		key := strings.ToLower(host)
		if guid != "" {
			key = "guid:" + guid
		} else if hasSite {
			key = fmt.Sprintf("site:%d:%s", siteID, strings.ToLower(host))
		}
		if _, seen := seenDevices[key]; seen {
			continue
		}
		seenDevices[key] = struct{}{}
		target := map[string]any{"kind": "device", "device_guid": guid, "hostname": host, "site_name": firstPresentAny(item["site_name"], item["site"], "")}
		if hasSite {
			target["site_id"] = siteID
		} else {
			target["site_id"] = nil
		}
		normalized = append(normalized, target)
	}
	return normalized
}

func validateScheduledTargetsForSave(ctx context.Context, conn *sql.Conn, targets []any) (string, error) {
	filterIDs := []int64{}
	for _, target := range targets {
		entry, ok := target.(map[string]any)
		if !ok {
			continue
		}
		if !scheduledIsFilterTarget(entry) {
			continue
		}
		filterID, ok := int64Value(firstPresentAny(entry["filter_id"], entry["id"]))
		if !ok || filterID <= 0 {
			return "One or more selected filters is invalid.", nil
		}
		filterIDs = append(filterIDs, filterID)
	}
	filterIDs = uniquePositiveInt64s(filterIDs)
	if len(filterIDs) == 0 {
		return "", nil
	}
	query, args := inClauseQuery("SELECT id, name, archived FROM engine.device_filters WHERE id IN (%s)", filterIDs)
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	found := map[int64]map[string]any{}
	for rows.Next() {
		var id sql.NullInt64
		var name sql.NullString
		var archived sql.NullInt64
		if err := rows.Scan(&id, &name, &archived); err != nil {
			return "", err
		}
		if id.Valid {
			found[id.Int64] = map[string]any{"name": nullString(name), "archived": archived.Valid && archived.Int64 != 0}
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	for _, id := range filterIDs {
		record, ok := found[id]
		if !ok {
			return fmt.Sprintf("Filter #%d does not exist.", id), nil
		}
		if boolFromAny(record["archived"]) {
			return fmt.Sprintf("Filter %q is archived and cannot be scheduled.", firstText(cleanText(record["name"]), fmt.Sprint(id))), nil
		}
	}
	return "", nil
}

func validateScheduledComponentsForContext(components []any, executionContext string) string {
	contextValue := strings.ToLower(strings.TrimSpace(executionContext))
	if len(scheduledWorkflowComponents(components)) > 0 {
		return ""
	}
	domains := map[string]struct{}{}
	for _, component := range components {
		if domain := scheduledComponentExecutionDomain(component); domain != "" {
			domains[domain] = struct{}{}
		}
	}
	if _, hasScript := domains["script"]; hasScript {
		if _, hasAnsible := domains["ansible"]; hasAnsible {
			return "Scheduled jobs cannot mix script assemblies with Ansible playbook assemblies. Remove the cross-domain assemblies or split them into separate jobs."
		}
	}
	if !stringInSet(contextValue, "local", "ssh", "winrm", "ssh_individual", "winrm_individual", "system", "current_user") {
		return ""
	}
	if stringInSet(contextValue, "local", "ssh", "winrm", "ssh_individual", "winrm_individual") && len(domains) > 0 && !onlyDomain(domains, "ansible") {
		return "Jobs using local, ssh, winrm, ssh_individual, or winrm_individual execution contexts must contain only Ansible components."
	}
	if stringInSet(contextValue, "system", "current_user") && len(domains) > 0 && !onlyDomain(domains, "script") {
		return "Jobs using agent execution contexts must contain only script assemblies."
	}
	return ""
}

func validateScheduledRemoteCredentialForContext(components []any, executionContext string, credentialID sql.NullInt64, useServiceAccount bool) string {
	domains := map[string]struct{}{}
	for _, component := range components {
		if domain := scheduledComponentExecutionDomain(component); domain != "" {
			domains[domain] = struct{}{}
		}
	}
	if !onlyDomain(domains, "ansible") {
		return ""
	}
	switch normalizeScheduledAnsibleTransport(executionContext) {
	case "ssh":
		if !credentialID.Valid || credentialID.Int64 <= 0 {
			return "SSH Ansible jobs require a stored credential."
		}
	case "winrm":
		if !useServiceAccount && (!credentialID.Valid || credentialID.Int64 <= 0) {
			return "WinRM Ansible jobs require a stored credential or service-account execution."
		}
	}
	return ""
}

func validateScheduledPatchInstallConfig(components []any, targets []any) string {
	component := scheduledPatchInstallComponent(components)
	if component == nil {
		return "Patch install jobs require a patch install component."
	}
	if len(targets) == 0 {
		return "Patch install jobs require at least one target device."
	}
	patch := scheduledPatchInstallSpec(component)
	if cleanText(patch["patch_key"]) == "" && normalizePatchKB(firstNonEmpty(patch["kb"], "")) == "" && cleanText(patch["title"]) == "" {
		return "Patch install component requires patch_key, KB, or title."
	}
	if state := normalizePatchState(cleanText(patch["state"])); state != "" && state != "pending" {
		return "Patch install jobs can only target pending updates."
	}
	return ""
}

func validateScheduledWorkflowJobConfiguration(components []any, targets []any, executionContext string, credentialID sql.NullInt64, useServiceAccount bool) string {
	workflowComponents := scheduledWorkflowComponents(components)
	if len(workflowComponents) == 0 {
		return ""
	}
	if len(workflowComponents) != 1 {
		return "Workflow-backed scheduled jobs must contain exactly one workflow component."
	}
	if len(workflowComponents) != len(components) {
		return "Workflow-backed scheduled jobs cannot mix workflow, script, or Ansible components."
	}
	if len(targets) > 0 {
		return "Workflow-backed scheduled jobs cannot define scheduler-level targets. Configure targets inside workflow nodes instead."
	}
	contextValue := strings.ToLower(firstText(strings.TrimSpace(executionContext), "system"))
	if contextValue != "" && contextValue != "system" {
		return "Workflow-backed scheduled jobs do not support scheduler-level execution contexts."
	}
	if credentialID.Valid {
		return "Workflow-backed scheduled jobs do not support scheduler-level credentials."
	}
	if useServiceAccount {
		return "Workflow-backed scheduled jobs do not support scheduler-level service account targeting."
	}
	if scheduledWorkflowGUID(workflowComponents[0]) == "" {
		return "Workflow-backed scheduled jobs require a saved workflow assembly selection."
	}
	return ""
}

func validateScheduledOnboardingConfig(components []any, targets []any, credentialID sql.NullInt64) string {
	component := scheduledOnboardingComponent(components)
	var siteID int64
	hasSite := false
	scopeEntries := []string{}
	for _, target := range targets {
		entry, ok := target.(map[string]any)
		if !ok {
			continue
		}
		if strings.ToLower(cleanText(firstPresentAny(entry["kind"], entry["type"]))) != "onboarding_scope" {
			continue
		}
		if parsed, ok := int64Value(firstPresentAny(entry["site_id"], entry["siteId"])); ok {
			siteID = parsed
			hasSite = true
		}
		scopeEntries = append(scopeEntries, scheduledStringEntries(firstPresentAny(entry["entries"], entry["scope"], entry["targets"], entry["discovery_scope"]))...)
	}
	_ = siteID
	if !hasSite {
		return "Onboarding jobs require a site."
	}
	if len(scopeEntries) == 0 {
		return "Onboarding jobs require at least one IP address, CIDR, range, or FQDN."
	}
	platform := strings.ToLower(firstText(cleanText(firstPresentAny(component["agent_platform"], component["target_os"], component["platform"], component["os"])), "linux"))
	switch platform {
	case "auto", "detect", "automatic", "autodetect", "auto_detect":
		platform = "auto"
	case "linux_ssh", "ssh":
		platform = "linux"
	case "windows_remote", "windows_smb", "smb", "winrm":
		platform = "windows"
	}
	if !stringInSet(platform, "auto", "linux", "windows") {
		return "Agent platform must be Auto, Linux, or Windows."
	}
	if !credentialID.Valid && len(scheduledCredentialIDList(component, "windows_credential_ids", "stored_windows_credential_ids", "windows_credentials")) == 0 && len(scheduledCredentialIDList(component, "linux_credential_ids", "stored_linux_credential_ids", "linux_credentials")) == 0 {
		return "Onboarding jobs require at least one stored credential."
	}
	return ""
}

func scheduledComponentExecutionDomain(component any) string {
	entry, ok := component.(map[string]any)
	if !ok {
		return ""
	}
	if scheduledIsPatchInstallComponent(entry) {
		return "patch"
	}
	if scheduledIsWorkflowComponent(entry) {
		return "workflow"
	}
	if scheduledIsAnsibleComponent(entry) {
		return "ansible"
	}
	return "script"
}

func scheduledIsFilterTarget(entry map[string]any) bool {
	kind := strings.ToLower(cleanText(firstPresentAny(entry["kind"], entry["type"])))
	return kind == "filter" || firstPresentAny(entry["filter_id"]) != nil
}

func scheduledWorkflowComponents(components []any) []map[string]any {
	result := []map[string]any{}
	for _, component := range components {
		entry, ok := component.(map[string]any)
		if ok && scheduledIsWorkflowComponent(entry) {
			result = append(result, copyMap(entry))
		}
	}
	return result
}

func scheduledIsWorkflowComponent(entry map[string]any) bool {
	return scheduledComponentHasValue(entry, "workflow")
}

func scheduledIsAnsibleComponent(entry map[string]any) bool {
	return scheduledComponentHasValue(entry, "ansible") || scheduledComponentHasValue(entry, "playbook")
}

func scheduledIsPatchInstallComponent(entry map[string]any) bool {
	return scheduledComponentHasValue(entry, patchInstallComponentKind) || scheduledComponentHasValue(entry, "patch_management")
}

func scheduledComponentHasValue(entry map[string]any, wanted string) bool {
	for _, key := range []string{"kind", "type", "component_type", "assembly_type", "assemblyType", "assembly_subtype", "assemblySubtype", "script_type"} {
		if strings.EqualFold(cleanText(entry[key]), wanted) {
			return true
		}
	}
	return false
}

func scheduledWorkflowGUID(component map[string]any) string {
	return cleanText(firstPresentAny(component["assembly_guid"], component["assemblyGuid"], component["workflow_guid"], component["workflowGuid"]))
}

func scheduledOnboardingComponent(components []any) map[string]any {
	for _, component := range components {
		entry, ok := component.(map[string]any)
		if !ok {
			continue
		}
		kind := strings.ToLower(cleanText(firstPresentAny(entry["kind"], entry["type"], entry["component_type"], entry["assembly_type"])))
		if stringInSet(kind, onboardingComponentKind, scheduledJobKindOnboarding, "automatic_device_onboarding") {
			return copyMap(entry)
		}
	}
	if len(components) > 0 {
		if entry, ok := components[0].(map[string]any); ok {
			return copyMap(entry)
		}
	}
	return map[string]any{}
}

func lookupScheduledTargetDevice(ctx context.Context, conn *sql.Conn, hostname string, guid string) (*int64, string, string, string, error) {
	hostname = cleanText(hostname)
	guid = normalizeCanonicalGUID(guid)
	if hostname == "" && guid == "" {
		return nil, "", "", "", nil
	}
	clauses := []string{}
	args := []any{}
	if hostname != "" {
		args = append(args, hostname)
		clauses = append(clauses, "LOWER(d.hostname)=LOWER($"+fmt.Sprint(len(args))+")")
	}
	if guid != "" {
		args = append(args, guid)
		clauses = append(clauses, "LOWER(d.guid)=LOWER($"+fmt.Sprint(len(args))+")")
	}
	var rowGUID, rowHost, siteName sql.NullString
	var siteID sql.NullInt64
	err := conn.QueryRowContext(ctx, `
		SELECT d.guid, d.hostname, ds.site_id, s.name
		  FROM engine.devices AS d
	 LEFT JOIN engine.device_sites AS ds ON ds.device_hostname=d.hostname
	 LEFT JOIN engine.sites AS s ON s.id=ds.site_id
		 WHERE `+strings.Join(clauses, " OR ")+`
	  ORDER BY COALESCE(d.last_seen, 0) DESC
		 LIMIT 1
	`, args...).Scan(&rowGUID, &rowHost, &siteID, &siteName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", "", "", nil
	}
	if err != nil {
		return nil, "", "", "", err
	}
	var sitePtr *int64
	if siteID.Valid {
		value := siteID.Int64
		sitePtr = &value
	}
	return sitePtr, nullString(siteName), nullString(rowHost), strings.ToLower(normalizeCanonicalGUID(rowGUID.String)), nil
}

func scheduledJobMutationJSON(components []any, targets []any) (string, string, error) {
	componentBytes, err := json.Marshal(components)
	if err != nil {
		return "", "", err
	}
	targetBytes, err := json.Marshal(targets)
	if err != nil {
		return "", "", err
	}
	return string(componentBytes), string(targetBytes), nil
}

func scheduledAnySlice(value any) []any {
	switch typed := value.(type) {
	case nil:
		return []any{}
	case []any:
		return typed
	case []map[string]any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, item)
		}
		return out
	default:
		return []any{typed}
	}
}

func scheduledMapAny(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return map[string]any{}
}

func scheduledParseTS(value any) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	switch typed := value.(type) {
	case int64:
		return sql.NullInt64{Int64: typed, Valid: true}
	case int:
		return sql.NullInt64{Int64: int64(typed), Valid: true}
	case float64:
		return sql.NullInt64{Int64: int64(typed), Valid: true}
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return sql.NullInt64{Int64: parsed, Valid: true}
		}
	}
	text := cleanText(value)
	if text == "" {
		return sql.NullInt64{}
	}
	if parsed, err := time.Parse(time.RFC3339, strings.ReplaceAll(text, "Z", "+00:00")); err == nil {
		return sql.NullInt64{Int64: parsed.Unix(), Valid: true}
	}
	if parsed, err := time.ParseInLocation("2006-01-02T15:04", text, engineScheduleLocation()); err == nil {
		return sql.NullInt64{Int64: parsed.Unix(), Valid: true}
	}
	if parsed, err := time.ParseInLocation("2006-01-02 15:04:05", text, engineScheduleLocation()); err == nil {
		return sql.NullInt64{Int64: parsed.Unix(), Valid: true}
	}
	if value, ok := int64Value(text); ok {
		return sql.NullInt64{Int64: value, Valid: true}
	}
	return sql.NullInt64{}
}

func scheduledOptionalPositiveInt64(value any) sql.NullInt64 {
	if value == nil || cleanText(value) == "" || strings.EqualFold(cleanText(value), "null") {
		return sql.NullInt64{}
	}
	parsed, ok := int64Value(value)
	if !ok || parsed <= 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: parsed, Valid: true}
}

func normalizeScheduledAnsibleTransport(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "ssh", "ssh_individual":
		return "ssh"
	case "winrm", "winrm_individual":
		return "winrm"
	case "local":
		return "local"
	default:
		return normalized
	}
}

func scheduledStringEntries(value any) []string {
	result := []string{}
	add := func(item any) {
		text := strings.TrimSpace(cleanText(item))
		if text != "" {
			result = append(result, text)
		}
	}
	switch typed := value.(type) {
	case string:
		for _, item := range strings.FieldsFunc(typed, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' || r == ';' }) {
			add(item)
		}
	case []any:
		for _, item := range typed {
			add(item)
		}
	case []string:
		for _, item := range typed {
			add(item)
		}
	default:
		add(typed)
	}
	return result
}

func scheduledCredentialIDList(component map[string]any, keys ...string) []int64 {
	values := []int64{}
	seen := map[int64]struct{}{}
	for _, key := range keys {
		for _, raw := range scheduledStringEntries(component[key]) {
			id, ok := int64Value(raw)
			if !ok || id <= 0 {
				continue
			}
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			values = append(values, id)
		}
	}
	return values
}

func scheduledLowerStringList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, strings.ToLower(strings.TrimSpace(value)))
	}
	return out
}

func stringInSet(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}
	return false
}

func onlyDomain(domains map[string]struct{}, expected string) bool {
	if len(domains) != 1 {
		return false
	}
	_, ok := domains[expected]
	return ok
}

func boolIntArg(value bool) int {
	if value {
		return 1
	}
	return 0
}

func statusOrDefault(status int, fallback int) int {
	if status == 0 {
		return fallback
	}
	return status
}

func scheduledJobUpdateHasRecognizedFields(payload map[string]any) bool {
	for _, key := range []string{"name", "components", "targets", "schedule", "schedule_type", "duration", "duration_stop_enabled", "expiration", "execution_context", "credential_id", "use_service_account", "enabled", "job_kind", "kind"} {
		if _, ok := payload[key]; ok {
			return true
		}
	}
	return false
}
