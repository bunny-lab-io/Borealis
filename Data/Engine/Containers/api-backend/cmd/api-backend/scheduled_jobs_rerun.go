package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	scheduledResolutionPending    = "pending"
	scheduledResolutionUnresolved = "unresolved"
	scheduledOnboardingName       = "Device Onboarding"
)

var (
	scheduledInventorySlugPattern = regexp.MustCompile(`[^a-z0-9]+`)
	scheduledInventoryDashPattern = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)
	scheduledRepeatedSlugPattern  = regexp.MustCompile(`_+`)
)

type scheduledResolvedTarget struct {
	Hostname           string
	DeviceGUID         string
	SiteID             *int64
	SiteName           string
	AgentID            string
	ConnectionType     string
	ConnectionEndpoint string
	OperatingSystem    string
	FilterIDs          []int64
}

type scheduledTargetResolution struct {
	Hosts         []string
	Targets       []*scheduledResolvedTarget
	FilterMatches map[int64][]string
}

type scheduledComponentBuckets struct {
	Workflow []map[string]any
	Script   []map[string]any
	Ansible  []map[string]any
}

func (s *postgresOperatorStore) rerunScheduledJob(ctx context.Context, profile operatorProfile, jobID int64) (map[string]any, int, error) {
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	job, found, err := loadScheduledJobVisibility(ctx, conn, jobID, allowedSiteIDs)
	if err != nil || !found {
		_ = conn.Close()
		return map[string]any{"error": "not found"}, http.StatusNotFound, err
	}
	if !boolInt64(job.Enabled) {
		_ = conn.Close()
		return map[string]any{"error": "job_disabled"}, http.StatusConflict, nil
	}
	latest, err := latestScheduledOccurrence(ctx, conn, jobID)
	if err != nil {
		_ = conn.Close()
		return nil, http.StatusInternalServerError, err
	}
	if err := conn.Close(); err != nil {
		return nil, http.StatusInternalServerError, err
	}

	components := parseJSONArray(job.ComponentsJSON)
	if len(components) == 0 {
		return map[string]any{"error": "job has no components"}, http.StatusBadRequest, nil
	}
	rawTargets := parseJSONArray(job.TargetsJSON)
	now := time.Now().Unix()
	occurrence := now
	if latest != nil && *latest >= occurrence {
		occurrence = *latest + 1
	}

	jobKind := normalizeScheduledJobKind(nullString(job.JobKind))
	if jobKind == scheduledJobKindOnboarding {
		if err := s.recordScheduledOnboardingSnapshot(ctx, jobID, occurrence, now); err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return s.scheduledRerunResponse(ctx, job, jobID, occurrence)
	}

	buckets := scheduledClassifyComponents(components)
	if len(buckets.Workflow) == 1 && len(buckets.Script) == 0 && len(buckets.Ansible) == 0 {
		if err := s.recordScheduledWorkflowSnapshot(ctx, jobID, occurrence, buckets.Workflow[0], now); err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return s.scheduledRerunResponse(ctx, job, jobID, occurrence)
	}

	resolution, err := s.resolveScheduledRerunTargets(ctx, profile, rawTargets)
	if err != nil {
		resolution = scheduledTargetResolution{Hosts: []string{}, Targets: []*scheduledResolvedTarget{}, FilterMatches: map[int64][]string{}}
	}
	runMode := strings.ToLower(firstText(nullString(job.ExecutionContext), "system"))
	sharedAnsible := len(buckets.Ansible) > 0 && len(buckets.Script) == 0 && stringInSet(runMode, "local", "ssh", "winrm")
	individualAnsible := len(buckets.Ansible) > 0 && len(buckets.Script) == 0 && stringInSet(runMode, "ssh_individual", "winrm_individual")
	switch {
	case sharedAnsible:
		err = s.recordScheduledSharedAnsibleSnapshot(ctx, jobID, occurrence, runMode, buckets.Ansible, resolution.Targets, now)
	case individualAnsible:
		err = s.recordScheduledIndividualAnsibleSnapshot(ctx, jobID, occurrence, runMode, buckets.Ansible, resolution.Targets, now)
	default:
		err = s.recordScheduledGeneralSnapshot(ctx, jobID, occurrence, resolution.Targets, now)
	}
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return s.scheduledRerunResponse(ctx, job, jobID, occurrence)
}

func (s *postgresOperatorStore) scheduledRerunResponse(ctx context.Context, job scheduledJobRow, jobID int64, occurrence int64) (map[string]any, int, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	runs, err := loadScheduledRunsForOccurrence(ctx, conn, jobID, occurrence)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	runPayloads := make([]map[string]any, 0, len(runs))
	for _, row := range runs {
		runPayloads = append(runPayloads, scheduledRunPayload(row))
	}
	jobPayload, err := scheduledJobPayload(ctx, conn, job)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return map[string]any{"status": "queued", "occurrence": occurrence, "runs": runPayloads, "job": jobPayload}, http.StatusOK, nil
}

func scheduledClassifyComponents(components []any) scheduledComponentBuckets {
	buckets := scheduledComponentBuckets{Workflow: []map[string]any{}, Script: []map[string]any{}, Ansible: []map[string]any{}}
	for _, component := range components {
		entry, ok := component.(map[string]any)
		if !ok {
			continue
		}
		copy := copyMap(entry)
		switch {
		case scheduledIsWorkflowComponent(entry):
			buckets.Workflow = append(buckets.Workflow, copy)
		case scheduledIsAnsibleComponent(entry):
			buckets.Ansible = append(buckets.Ansible, copy)
		default:
			buckets.Script = append(buckets.Script, copy)
		}
	}
	return buckets
}

func (s *postgresOperatorStore) resolveScheduledRerunTargets(ctx context.Context, profile operatorProfile, rawTargets []any) (scheduledTargetResolution, error) {
	devices, err := s.fetchFilterDevices(ctx, profile)
	if err != nil {
		return scheduledTargetResolution{}, err
	}
	filterIDs := scheduledFilterIDsFromTargets(rawTargets)
	filters, err := s.loadDeviceFilters(ctx, filterIDs, false)
	if err != nil {
		return scheduledTargetResolution{}, err
	}

	byGUID := map[string]map[string]any{}
	byHost := map[string][]map[string]any{}
	bySiteHost := map[string][]map[string]any{}
	for _, device := range devices {
		guid := strings.ToLower(normalizeCanonicalGUID(firstPresentAny(device["device_guid"], device["guid"])))
		if guid != "" {
			byGUID[guid] = device
		}
		hostname := strings.ToLower(cleanText(device["hostname"]))
		if hostname == "" {
			continue
		}
		byHost[hostname] = append(byHost[hostname], device)
		if siteID, ok := int64Value(device["site_id"]); ok {
			bySiteHost[scheduledSiteHostKey(siteID, hostname)] = append(bySiteHost[scheduledSiteHostKey(siteID, hostname)], device)
		}
	}

	result := scheduledTargetResolution{Hosts: []string{}, Targets: []*scheduledResolvedTarget{}, FilterMatches: map[int64][]string{}}
	identityMap := map[string]*scheduledResolvedTarget{}
	appendTarget := func(record scheduledResolvedTarget) *scheduledResolvedTarget {
		record.Hostname = strings.TrimSpace(record.Hostname)
		if record.Hostname == "" {
			return nil
		}
		record.FilterIDs = uniquePositiveInt64s(record.FilterIDs)
		identity := scheduledTargetIdentity(record)
		if identity != "" {
			if existing := identityMap[identity]; existing != nil {
				scheduledMergeTarget(existing, record)
				return existing
			}
		}
		next := record
		if identity != "" {
			identityMap[identity] = &next
		}
		result.Targets = append(result.Targets, &next)
		result.Hosts = append(result.Hosts, next.Hostname)
		return &next
	}

	for _, raw := range rawTargets {
		entry := scheduledNormalizeRerunTarget(raw)
		switch cleanText(entry["kind"]) {
		case "all_devices":
			for _, device := range devices {
				appendTarget(scheduledTargetFromDevice(device, "", nil, ""))
			}
		case "device":
			hostname := cleanText(entry["hostname"])
			guid := strings.ToLower(normalizeCanonicalGUID(entry["device_guid"]))
			siteID := int64FromAny(entry["site_id"])
			siteName := cleanText(entry["site_name"])
			matches := []map[string]any{}
			if guid != "" {
				if match := byGUID[guid]; match != nil {
					matches = append(matches, match)
				}
			} else if hostname != "" && siteID != nil {
				matches = append(matches, bySiteHost[scheduledSiteHostKey(*siteID, strings.ToLower(hostname))]...)
			} else if hostname != "" {
				matches = append(matches, byHost[strings.ToLower(hostname)]...)
			}
			if len(matches) == 0 {
				appendTarget(scheduledResolvedTarget{Hostname: hostname, SiteID: siteID, SiteName: siteName})
				continue
			}
			for _, match := range matches {
				appendTarget(scheduledTargetFromDevice(match, hostname, siteID, siteName))
			}
		case "filter":
			filterID, ok := int64Value(entry["filter_id"])
			if !ok || filterID <= 0 {
				continue
			}
			record := filters[filterID]
			if record == nil || boolFromAny(record["archived"]) {
				continue
			}
			scopedDevices := devices
			if allowed := siteIDsFromAny(entry["allowed_site_ids"]); len(allowed) > 0 {
				allowedSet := int64Set(allowed)
				scopedDevices = []map[string]any{}
				for _, device := range devices {
					if siteID, ok := int64Value(device["site_id"]); ok {
						if _, allowedOK := allowedSet[siteID]; allowedOK {
							scopedDevices = append(scopedDevices, device)
						}
					}
				}
			}
			matches := matchFilterDevices(record, scopedDevices)
			hosts := []string{}
			for _, device := range matches {
				target := scheduledTargetFromDevice(device, "", nil, "")
				target.FilterIDs = append(target.FilterIDs, filterID)
				merged := appendTarget(target)
				if merged == nil {
					continue
				}
				if !int64SliceContains(merged.FilterIDs, filterID) {
					merged.FilterIDs = uniquePositiveInt64s(append(merged.FilterIDs, filterID))
				}
				hosts = append(hosts, merged.Hostname)
			}
			result.FilterMatches[filterID] = uniqueStrings(hosts)
		}
	}
	return result, nil
}

func scheduledFilterIDsFromTargets(rawTargets []any) []int64 {
	ids := []int64{}
	for _, raw := range rawTargets {
		entry := scheduledNormalizeRerunTarget(raw)
		if cleanText(entry["kind"]) != "filter" {
			continue
		}
		if id, ok := int64Value(entry["filter_id"]); ok && id > 0 {
			ids = append(ids, id)
		}
	}
	return uniquePositiveInt64s(ids)
}

func scheduledNormalizeRerunTarget(raw any) map[string]any {
	switch typed := raw.(type) {
	case string:
		return map[string]any{"kind": "device", "hostname": strings.TrimSpace(typed)}
	case int, int64, float64:
		return map[string]any{"kind": "device", "hostname": cleanText(typed)}
	case map[string]any:
		kind := strings.ToLower(cleanText(firstPresentAny(typed["kind"], typed["type"])))
		if kind == "all_devices" || boolFromAny(typed["all_devices"]) {
			return map[string]any{"kind": "all_devices"}
		}
		if kind == "filter" || firstPresentAny(typed["filter_id"]) != nil {
			return map[string]any{
				"kind":             "filter",
				"filter_id":        firstPresentAny(typed["filter_id"], typed["id"]),
				"name":             typed["name"],
				"allowed_site_ids": siteIDsFromAny(firstPresentAny(typed["allowed_site_ids"], typed["scope_site_ids"])),
			}
		}
		hostname := cleanText(typed["hostname"])
		if hostname == "" {
			return map[string]any{"kind": "unknown"}
		}
		return map[string]any{
			"kind":        "device",
			"hostname":    hostname,
			"device_guid": firstPresentAny(typed["device_guid"], typed["guid"]),
			"site_id":     typed["site_id"],
			"site_name":   firstPresentAny(typed["site_name"], typed["site"]),
		}
	default:
		return map[string]any{"kind": "unknown"}
	}
}

func scheduledTargetFromDevice(device map[string]any, hostnameOverride string, siteIDOverride *int64, siteNameOverride string) scheduledResolvedTarget {
	hostname := firstText(hostnameOverride, cleanText(device["hostname"]))
	siteID := siteIDOverride
	if siteID == nil {
		siteID = int64FromAny(device["site_id"])
	}
	return scheduledResolvedTarget{
		Hostname:           hostname,
		DeviceGUID:         strings.ToLower(normalizeCanonicalGUID(firstPresentAny(device["device_guid"], device["guid"]))),
		SiteID:             siteID,
		SiteName:           firstText(siteNameOverride, cleanText(device["site_name"])),
		AgentID:            cleanText(device["agent_id"]),
		ConnectionType:     cleanText(device["connection_type"]),
		ConnectionEndpoint: cleanText(device["connection_endpoint"]),
		OperatingSystem:    cleanText(device["operating_system"]),
		FilterIDs:          []int64{},
	}
}

func scheduledTargetIdentity(target scheduledResolvedTarget) string {
	if target.DeviceGUID != "" {
		return "guid:" + strings.ToLower(target.DeviceGUID)
	}
	host := strings.ToLower(strings.TrimSpace(target.Hostname))
	if host == "" {
		return ""
	}
	if target.SiteID != nil {
		return scheduledSiteHostKey(*target.SiteID, host)
	}
	return "host:" + host
}

func scheduledSiteHostKey(siteID int64, host string) string {
	return "site:" + strconv.FormatInt(siteID, 10) + ":" + strings.ToLower(strings.TrimSpace(host))
}

func scheduledMergeTarget(existing *scheduledResolvedTarget, incoming scheduledResolvedTarget) {
	if existing.Hostname == "" {
		existing.Hostname = incoming.Hostname
	}
	if existing.DeviceGUID == "" {
		existing.DeviceGUID = incoming.DeviceGUID
	}
	if existing.SiteID == nil && incoming.SiteID != nil {
		existing.SiteID = incoming.SiteID
	}
	if existing.SiteName == "" {
		existing.SiteName = incoming.SiteName
	}
	if existing.AgentID == "" {
		existing.AgentID = incoming.AgentID
	}
	if existing.ConnectionType == "" {
		existing.ConnectionType = incoming.ConnectionType
	}
	if existing.ConnectionEndpoint == "" {
		existing.ConnectionEndpoint = incoming.ConnectionEndpoint
	}
	if existing.OperatingSystem == "" {
		existing.OperatingSystem = incoming.OperatingSystem
	}
	existing.FilterIDs = uniquePositiveInt64s(append(existing.FilterIDs, incoming.FilterIDs...))
}

func (s *postgresOperatorStore) recordScheduledOnboardingSnapshot(ctx context.Context, jobID, occurrence, createdAt int64) error {
	return s.withScheduledSnapshotTx(ctx, jobID, occurrence, func(tx *sql.Tx) error {
		_, err := insertScheduledRun(ctx, tx, scheduledRunInsert{
			JobID:          jobID,
			ScheduledTS:    occurrence,
			Status:         scheduledStatusPending,
			CreatedAt:      createdAt,
			Shared:         true,
			ComponentIndex: intPtr(0),
			ComponentKind:  onboardingComponentKind,
			ComponentName:  scheduledOnboardingName,
		})
		return err
	})
}

func (s *postgresOperatorStore) recordScheduledWorkflowSnapshot(ctx context.Context, jobID, occurrence int64, component map[string]any, createdAt int64) error {
	return s.withScheduledSnapshotTx(ctx, jobID, occurrence, func(tx *sql.Tx) error {
		_, err := insertScheduledRun(ctx, tx, scheduledRunInsert{
			JobID:          jobID,
			ScheduledTS:    occurrence,
			Status:         scheduledStatusPending,
			CreatedAt:      createdAt,
			ComponentIndex: intPtr(0),
			ComponentKind:  "workflow",
			ComponentName:  scheduledComponentDisplayName(component, "Workflow"),
		})
		return err
	})
}

func (s *postgresOperatorStore) recordScheduledGeneralSnapshot(ctx context.Context, jobID, occurrence int64, targets []*scheduledResolvedTarget, createdAt int64) error {
	return s.withScheduledSnapshotTx(ctx, jobID, occurrence, func(tx *sql.Tx) error {
		if len(targets) == 0 {
			_, err := insertScheduledRun(ctx, tx, scheduledRunInsert{
				JobID:       jobID,
				ScheduledTS: occurrence,
				Status:      scheduledStatusSkipped,
				SkipReason:  scheduledSkipNoTargets,
				CreatedAt:   createdAt,
			})
			return err
		}
		for _, target := range targets {
			runID, err := insertScheduledRun(ctx, tx, scheduledRunInsert{
				JobID:          jobID,
				TargetHostname: target.Hostname,
				ScheduledTS:    occurrence,
				Status:         scheduledStatusPending,
				CreatedAt:      createdAt,
			})
			if err != nil {
				return err
			}
			filterIDs := uniquePositiveInt64s(target.FilterIDs)
			if len(filterIDs) == 0 {
				if err := insertScheduledRunTarget(ctx, tx, runID, *target, nil, "", "", "", createdAt); err != nil {
					return err
				}
				continue
			}
			for _, filterID := range filterIDs {
				id := filterID
				if err := insertScheduledRunTarget(ctx, tx, runID, *target, &id, "", "", "", createdAt); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s *postgresOperatorStore) recordScheduledSharedAnsibleSnapshot(ctx context.Context, jobID, occurrence int64, runMode string, components []map[string]any, targets []*scheduledResolvedTarget, createdAt int64) error {
	return s.withScheduledSnapshotTx(ctx, jobID, occurrence, func(tx *sql.Tx) error {
		for index, component := range components {
			status := scheduledStatusPending
			skipReason := ""
			if len(targets) == 0 {
				status = scheduledStatusSkipped
				skipReason = scheduledSkipNoTargets
			}
			runID, err := insertScheduledRun(ctx, tx, scheduledRunInsert{
				JobID:          jobID,
				ScheduledTS:    occurrence,
				Status:         status,
				SkipReason:     skipReason,
				CreatedAt:      createdAt,
				Shared:         true,
				ComponentIndex: intPtr(index),
				ComponentKind:  "ansible",
				ComponentName:  scheduledComponentDisplayName(component, fmt.Sprintf("Ansible Playbook %d", index+1)),
			})
			if err != nil {
				return err
			}
			for _, target := range targets {
				if err := insertScheduledRunTarget(ctx, tx, runID, *target, firstInt64Ptr(target.FilterIDs), scheduledInventoryHostname(target, runMode), runMode, scheduledResolutionPending, createdAt); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s *postgresOperatorStore) recordScheduledIndividualAnsibleSnapshot(ctx context.Context, jobID, occurrence int64, runMode string, components []map[string]any, targets []*scheduledResolvedTarget, createdAt int64) error {
	transport := normalizeScheduledAnsibleTransport(runMode)
	return s.withScheduledSnapshotTx(ctx, jobID, occurrence, func(tx *sql.Tx) error {
		if len(targets) == 0 {
			for index, component := range components {
				if _, err := insertScheduledRun(ctx, tx, scheduledRunInsert{
					JobID:          jobID,
					ScheduledTS:    occurrence,
					Status:         scheduledStatusSkipped,
					SkipReason:     scheduledSkipNoTargets,
					CreatedAt:      createdAt,
					ComponentIndex: intPtr(index),
					ComponentKind:  "ansible",
					ComponentName:  scheduledComponentDisplayName(component, fmt.Sprintf("Ansible Playbook %d", index+1)),
				}); err != nil {
					return err
				}
			}
			return nil
		}
		for _, target := range targets {
			for index, component := range components {
				runID, err := insertScheduledRun(ctx, tx, scheduledRunInsert{
					JobID:          jobID,
					TargetHostname: target.Hostname,
					ScheduledTS:    occurrence,
					Status:         scheduledStatusPending,
					CreatedAt:      createdAt,
					ComponentIndex: intPtr(index),
					ComponentKind:  "ansible",
					ComponentName:  scheduledComponentDisplayName(component, fmt.Sprintf("Ansible Playbook %d", index+1)),
				})
				if err != nil {
					return err
				}
				if err := insertScheduledRunTarget(ctx, tx, runID, *target, firstInt64Ptr(target.FilterIDs), scheduledInventoryHostname(target, transport), transport, scheduledResolutionPending, createdAt); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s *postgresOperatorStore) withScheduledSnapshotTx(ctx context.Context, jobID, occurrence int64, fn func(*sql.Tx) error) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	var existing int64
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM engine.scheduled_job_runs WHERE job_id=$1 AND scheduled_ts=$2", jobID, occurrence).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackQuietly(tx)
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

type scheduledRunInsert struct {
	JobID          int64
	TargetHostname string
	ScheduledTS    int64
	Status         string
	SkipReason     string
	CreatedAt      int64
	Shared         bool
	ComponentIndex *int
	ComponentKind  string
	ComponentName  string
}

func insertScheduledRun(ctx context.Context, tx *sql.Tx, run scheduledRunInsert) (int64, error) {
	shared := 0
	if run.Shared {
		shared = 1
	}
	var id int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO engine.scheduled_job_runs(
			job_id, target_hostname, scheduled_ts, status, skip_reason, created_at, updated_at,
			shared_execution, component_index, component_kind, component_name
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING id
	`, run.JobID, nullIfEmpty(run.TargetHostname), run.ScheduledTS, run.Status, run.SkipReason, run.CreatedAt, run.CreatedAt, shared, run.ComponentIndex, nullIfEmpty(run.ComponentKind), nullIfEmpty(run.ComponentName)).Scan(&id)
	return id, err
}

func insertScheduledRunTarget(ctx context.Context, tx *sql.Tx, runID int64, target scheduledResolvedTarget, filterID *int64, inventoryHostname, resolvedConnection, resolutionStatus string, createdAt int64) error {
	filterIDsJSON := ""
	if resolvedConnection != "" {
		filterIDsJSON = scheduledInt64JSON(uniquePositiveInt64s(target.FilterIDs))
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO engine.scheduled_job_run_targets(
			run_id, device_guid, hostname, site_id, resolved_from_filter_id,
			inventory_hostname, wireguard_peer_ip, resolved_connection, resolution_status,
			resolution_reason, resolved_from_filter_ids_json, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
	`, runID, nullIfEmpty(target.DeviceGUID), target.Hostname, target.SiteID, filterID, nullIfEmpty(inventoryHostname), "", nullIfEmpty(resolvedConnection), nullIfEmpty(resolutionStatus), "", nullIfEmpty(filterIDsJSON), createdAt)
	return err
}

func scheduledComponentDisplayName(component map[string]any, fallback string) string {
	for _, key := range []string{"name", "component_name", "displayName", "script_name", "script_path", "path"} {
		if value := cleanText(component[key]); value != "" {
			return value
		}
	}
	return fallback
}

func scheduledInventoryHostname(target *scheduledResolvedTarget, connection string) string {
	hostSlug := scheduledSafeInventorySlug(target.Hostname, "host")
	if strings.EqualFold(connection, "local") {
		return scheduledSafeInventoryLabel(firstText(target.Hostname, "borealis-engine-01"), "borealis-engine-01")
	}
	siteSlug := scheduledSafeInventorySlug(target.SiteName, "")
	if siteSlug != "" {
		return siteSlug + "__" + hostSlug
	}
	if target.SiteID != nil {
		return "site_" + strconv.FormatInt(*target.SiteID, 10) + "__" + hostSlug
	}
	return "unassigned__" + hostSlug
}

func scheduledSafeInventorySlug(value, fallback string) string {
	raw := strings.ToLower(strings.TrimSpace(value))
	if raw == "" {
		return fallback
	}
	cleaned := scheduledInventorySlugPattern.ReplaceAllString(raw, "_")
	cleaned = scheduledRepeatedSlugPattern.ReplaceAllString(cleaned, "_")
	cleaned = strings.Trim(cleaned, "_")
	if cleaned == "" {
		return fallback
	}
	return cleaned
}

func scheduledSafeInventoryLabel(value, fallback string) string {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return fallback
	}
	cleaned := scheduledInventoryDashPattern.ReplaceAllString(raw, "-")
	cleaned = strings.Trim(cleaned, ".-")
	if cleaned == "" {
		return fallback
	}
	return cleaned
}

func firstInt64Ptr(values []int64) *int64 {
	values = uniquePositiveInt64s(values)
	if len(values) == 0 {
		return nil
	}
	return &values[0]
}

func int64SliceContains(values []int64, wanted int64) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func scheduledInt64JSON(values []int64) string {
	values = uniquePositiveInt64s(values)
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	raw, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

func intPtr(value int) *int {
	return &value
}
