package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
)

const (
	schedulerLaneServiceAction = "service_action"
	schedulerKindServiceAction = "service_action"

	schedulerWorkerStatusStarting = "starting"
	schedulerWorkerStatusRunning  = "running"
	schedulerWorkerStatusIdle     = "idle"
	schedulerWorkerStatusStopped  = "stopped"
	schedulerWorkerStatusLost     = "lost"

	schedulerRouteStatusActive  = "active"
	schedulerRouteStatusRetired = "retired"
	schedulerRouteStatusLost    = "lost"

	schedulerDefaultRouteRoot           = "/_borealis/site-workers"
	schedulerDefaultRemoteOpsPortBase   = 56000
	schedulerDefaultRemoteOpsPortRange  = 5000
	schedulerDefaultRemoteDeskPortBase  = 61000
	schedulerDefaultRemoteDeskPortRange = 3000
)

type goSchedulerManager struct {
	cfg         gatewayConfig
	store       *postgresOperatorStore
	secret      []byte
	apiBase     string
	projectRoot string
	httpClient  *http.Client
}

type schedulerWorkItem struct {
	ID           int64
	Kind         string
	SiteID       sql.NullInt64
	Lane         string
	JobID        sql.NullInt64
	RunID        sql.NullInt64
	Payload      map[string]any
	AttemptCount int64
}

type schedulerRoute struct {
	WorkerGUID     string
	SiteID         int64
	ContainerName  string
	RouteName      string
	RoutePath      string
	RouteFilePath  string
	UpstreamScheme string
	UpstreamHost   string
	UpstreamPort   int64
	Status         string
	Generation     int64
	Metadata       map[string]any
}

func runGoJobSchedulerManager(ctx context.Context, cfg gatewayConfig) error {
	store, closeStore, err := openOperatorStore(cfg)
	if err != nil {
		return err
	}
	defer closeStore()
	pgStore, ok := store.(*postgresOperatorStore)
	if !ok {
		return errors.New("postgres store required")
	}
	secret, err := loadOrCreateEngineSecret(cfg.EngineSecretPath)
	if err != nil {
		return err
	}
	manager := &goSchedulerManager{
		cfg:         cfg,
		store:       pgStore,
		secret:      []byte(secret),
		apiBase:     strings.TrimRight(envDefault("BOREALIS_INTERNAL_API_BASE_URL", "http://127.0.0.1:5000"), "/"),
		projectRoot: envDefault("BOREALIS_PROJECT_ROOT", "/opt/Borealis"),
		httpClient:  &http.Client{Timeout: 30 * time.Second},
	}
	return manager.run(ctx)
}

func runGoJobSchedulerHealthcheck(ctx context.Context, cfg gatewayConfig) error {
	store, closeStore, err := openOperatorStore(cfg)
	if err != nil {
		return err
	}
	defer closeStore()
	pgStore, ok := store.(*postgresOperatorStore)
	if !ok {
		return errors.New("postgres store required")
	}
	manager := &goSchedulerManager{
		cfg:   cfg,
		store: pgStore,
	}
	if err := manager.ensureTables(ctx); err != nil {
		return err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return pgStore.db.PingContext(pingCtx)
}

func (m *goSchedulerManager) run(ctx context.Context) error {
	if err := m.ensureTables(ctx); err != nil {
		return err
	}
	log.Printf("Go job-scheduler manager starting")
	if err := m.heartbeatManager(ctx); err != nil {
		log.Printf("job-scheduler manager heartbeat failed: %v", err)
	}
	if err := m.reconcileSiteWorkers(ctx); err != nil {
		log.Printf("site-worker startup reconcile failed: %v", err)
	}

	nextTick := int64(0)
	nextReconcile := int64(0)
	reconcileInterval := int64(envInt("BOREALIS_SITE_WORKER_RECONCILE_SECONDS", 30, 10, 3600))
	historySeconds := envInt("BOREALIS_WORKER_HISTORY_SECONDS", 60, 60, 86400)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("Go job-scheduler manager shutdown requested")
			return nil
		case <-ticker.C:
		}
		now := time.Now().Unix()
		if err := m.heartbeatManager(ctx); err != nil {
			log.Printf("job-scheduler manager heartbeat failed: %v", err)
		}
		if now >= nextTick {
			if err := m.tickOnce(ctx); err != nil {
				log.Printf("scheduled tick failed: %v", err)
			}
			nextTick = now + maxInt64(60-(now%60), 5)
		}
		if now >= nextReconcile {
			if err := m.reconcileSiteWorkers(ctx); err != nil {
				log.Printf("site-worker reconcile failed: %v", err)
			}
			nextReconcile = now + reconcileInterval
		}
		if err := m.expireStaleLeases(ctx); err != nil {
			log.Printf("stale lease expiration failed: %v", err)
		}
		if err := m.markLostWorkers(ctx); err != nil {
			log.Printf("worker lost-state update failed: %v", err)
		}
		if err := m.pruneWorkerHistory(ctx, historySeconds); err != nil {
			log.Printf("worker history prune failed: %v", err)
		}
		siteIDs, err := m.siteIDsNeedingWorkers(ctx)
		if err != nil {
			log.Printf("site worker demand lookup failed: %v", err)
		}
		for _, siteID := range siteIDs {
			if err := m.spawnSiteWorker(ctx, siteID); err != nil {
				log.Printf("failed to reconcile worker for site_id=%d: %v", siteID, err)
			}
		}
		if err := m.processServiceAction(ctx); err != nil {
			log.Printf("failed to process service action queue: %v", err)
		}
		if err := m.processGlobalScheduledWork(ctx); err != nil {
			log.Printf("failed to process global scheduled work: %v", err)
		}
		if err := m.processScheduledRunWork(ctx); err != nil {
			log.Printf("failed to process scheduled run work: %v", err)
		}
		if err := m.processPatchInstallWork(ctx); err != nil {
			log.Printf("failed to process patch install work: %v", err)
		}
		if err := m.processOnboardingWork(ctx); err != nil {
			log.Printf("failed to process onboarding work: %v", err)
		}
		if err := m.processAgentMaintenanceWork(ctx); err != nil {
			log.Printf("failed to process agent maintenance work: %v", err)
		}
		if err := m.refreshServiceSnapshots(ctx); err != nil {
			log.Printf("failed to refresh service snapshots: %v", err)
		}
	}
}

func (m *goSchedulerManager) ensureTables(ctx context.Context) error {
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	statements := []string{
		`CREATE SCHEMA IF NOT EXISTS engine`,
		`CREATE TABLE IF NOT EXISTS engine.job_scheduler_work_items (
			id BIGSERIAL PRIMARY KEY,
			dedupe_key TEXT UNIQUE,
			kind TEXT NOT NULL,
			site_id BIGINT,
			lane TEXT NOT NULL,
			job_id BIGINT,
			run_id BIGINT,
			target_id BIGINT,
			payload_json TEXT NOT NULL,
			status TEXT NOT NULL,
			attempt_count BIGINT NOT NULL DEFAULT 0,
			priority BIGINT NOT NULL DEFAULT 0,
			available_at BIGINT NOT NULL,
			lease_owner TEXT,
			lease_expires_at BIGINT,
			heartbeat_at BIGINT,
			worker_guid TEXT,
			container_name TEXT,
			error TEXT,
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL,
			started_at BIGINT,
			finished_at BIGINT
		)`,
		`CREATE TABLE IF NOT EXISTS engine.job_scheduler_workers (
			worker_guid TEXT PRIMARY KEY,
			container_name TEXT NOT NULL,
			site_id BIGINT,
			status TEXT NOT NULL,
			started_at BIGINT NOT NULL,
			last_seen_at BIGINT NOT NULL,
			idle_since BIGINT,
			stopped_at BIGINT,
			current_lanes_json TEXT,
			claimed_count BIGINT NOT NULL DEFAULT 0,
			task_links_json TEXT,
			docker_state TEXT,
			exit_code BIGINT,
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS engine.job_scheduler_worker_routes (
			worker_guid TEXT PRIMARY KEY,
			site_id BIGINT NOT NULL,
			container_name TEXT NOT NULL,
			route_name TEXT NOT NULL,
			route_path_prefix TEXT NOT NULL,
			route_file_path TEXT NOT NULL,
			upstream_scheme TEXT NOT NULL,
			upstream_host TEXT NOT NULL,
			upstream_port BIGINT NOT NULL DEFAULT 0,
			status TEXT NOT NULL,
			generation BIGINT NOT NULL DEFAULT 1,
			metadata_json TEXT NOT NULL,
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL,
			retired_at BIGINT
		)`,
		`CREATE TABLE IF NOT EXISTS engine.job_scheduler_service_snapshots (
			service_key TEXT PRIMARY KEY,
			payload_json TEXT NOT NULL,
			updated_at BIGINT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_job_scheduler_work_claim ON engine.job_scheduler_work_items(site_id, lane, status, available_at, priority)`,
		`CREATE INDEX IF NOT EXISTS idx_job_scheduler_work_lease ON engine.job_scheduler_work_items(status, lease_expires_at)`,
		`CREATE INDEX IF NOT EXISTS idx_job_scheduler_workers_site ON engine.job_scheduler_workers(site_id, status, last_seen_at)`,
		`CREATE INDEX IF NOT EXISTS idx_job_scheduler_worker_routes_site ON engine.job_scheduler_worker_routes(site_id, status, updated_at)`,
		`CREATE INDEX IF NOT EXISTS idx_job_scheduler_worker_routes_status ON engine.job_scheduler_worker_routes(status, retired_at)`,
	}
	for _, statement := range statements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func (m *goSchedulerManager) heartbeatManager(ctx context.Context) error {
	return m.upsertWorker(ctx, "job-scheduler", envDefault("BOREALIS_JOB_SCHEDULER_CONTAINER_NAME", "borealis-engine-job-scheduler"), 0, schedulerWorkerStatusRunning, []string{"scheduled_tick", "worker_reconcile", "service_action"}, []map[string]any{{
		"kind":  "manager",
		"label": "Job Scheduler Manager",
		"path":  "/server-info",
	}}, 0, nil)
}

func (m *goSchedulerManager) tickOnce(ctx context.Context) error {
	now := time.Now().Unix()
	if err := m.expireRunningScheduledRuns(ctx, now); err != nil {
		log.Printf("scheduled run expiration scan failed: %v", err)
	}
	if err := m.reconcileScheduledTerminalActivities(ctx, now); err != nil {
		log.Printf("scheduled activity reconciliation failed: %v", err)
	}
	if err := m.purgeOldScheduledRuns(ctx, now); err != nil {
		log.Printf("scheduled run history purge failed: %v", err)
	}
	nowMinute := schedulerFloorMinute(now)
	profile := operatorProfile{Username: "job-scheduler", Role: "Admin"}
	if err := m.processPatchPolicyTick(ctx, profile, now, nowMinute); err != nil {
		log.Printf("patch policy tick failed: %v", err)
	}
	jobs, err := m.enabledScheduledJobs(ctx)
	if err != nil {
		return err
	}
	onlineHosts, err := m.store.loadSchedulerOnlineHostnames(ctx, int64(envInt("BOREALIS_AGENT_ONLINE_WINDOW_SECONDS", 300, 60, 3600)))
	if err != nil {
		log.Printf("online host snapshot lookup failed: %v", err)
	}
	online := map[string]bool{}
	for _, host := range schedulerHostnameVariants(onlineHosts) {
		online[strings.ToLower(strings.TrimSpace(host))] = true
	}
	for _, job := range jobs {
		if err := m.processScheduledJobTick(ctx, profile, job, now, nowMinute, online); err != nil {
			log.Printf("scheduled job tick failed job_id=%d: %v", nullInt(job.ID), err)
		}
	}
	return nil
}

func (m *goSchedulerManager) enabledScheduledJobs(ctx context.Context) ([]scheduledJobRow, error) {
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, `
		SELECT id, name, components_json, targets_json, schedule_type, start_ts,
		       duration_stop_enabled, expiration, execution_context, credential_id,
		       use_service_account, enabled, created_at, updated_at, job_kind
		  FROM engine.scheduled_jobs
		 WHERE enabled = 1
		 ORDER BY id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []scheduledJobRow{}
	for rows.Next() {
		var row scheduledJobRow
		if err := rows.Scan(&row.ID, &row.Name, &row.ComponentsJSON, &row.TargetsJSON, &row.ScheduleType, &row.StartTS, &row.DurationStopEnabled, &row.Expiration, &row.ExecutionContext, &row.CredentialID, &row.UseServiceAccount, &row.Enabled, &row.CreatedAt, &row.UpdatedAt, &row.JobKind); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (m *goSchedulerManager) processScheduledJobTick(ctx context.Context, profile operatorProfile, job scheduledJobRow, now, nowMinute int64, online map[string]bool) error {
	jobID := nullInt(job.ID)
	if jobID <= 0 {
		return nil
	}
	occurrence, err := m.resolveOccurrenceForTick(ctx, jobID, nullString(job.ScheduleType), schedulerNullInt64Ptr(job.StartTS), schedulerNullInt64Ptr(job.CreatedAt), nowMinute)
	if err != nil || occurrence == nil {
		return err
	}
	components := parseJSONArray(job.ComponentsJSON)
	if len(components) == 0 {
		return nil
	}
	rawTargets := parseJSONArray(job.TargetsJSON)
	jobKind := normalizeScheduledJobKind(nullString(job.JobKind))
	if jobKind == scheduledJobKindAgentMaintenance {
		return nil
	}
	if jobKind == scheduledJobKindOnboarding {
		return m.processOnboardingTick(ctx, job, jobID, *occurrence, now, components, rawTargets)
	}
	if jobKind == scheduledJobKindPatchInstall {
		return m.processPatchInstallTick(ctx, profile, job, jobID, *occurrence, now, components, rawTargets, online)
	}
	buckets := scheduledClassifyComponents(components)
	if len(buckets.Workflow) > 0 && (len(buckets.Workflow) != 1 || len(buckets.Script) > 0 || len(buckets.Ansible) > 0) {
		log.Printf("skipping invalid workflow-backed scheduled job configuration job=%d", jobID)
		return nil
	}
	if len(buckets.Workflow) == 0 && len(buckets.Script) == 0 && len(buckets.Ansible) == 0 {
		return nil
	}
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	runs, err := loadScheduledRunsForOccurrence(ctx, conn, jobID, *occurrence)
	if closeErr := conn.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	runMode := strings.ToLower(firstText(nullString(job.ExecutionContext), "system"))
	sharedAnsible := len(buckets.Ansible) > 0 && len(buckets.Script) == 0 && stringInSet(runMode, "local", "ssh", "winrm")
	individualAnsible := len(buckets.Ansible) > 0 && len(buckets.Script) == 0 && stringInSet(runMode, "ssh_individual", "winrm_individual")
	if len(runs) == 0 {
		if len(buckets.Workflow) == 1 {
			if err := m.store.recordScheduledWorkflowSnapshot(ctx, jobID, *occurrence, buckets.Workflow[0], now); err != nil {
				return err
			}
		} else {
			resolution, err := m.store.resolveScheduledRerunTargets(ctx, profile, rawTargets)
			if err != nil {
				resolution = scheduledTargetResolution{Targets: []*scheduledResolvedTarget{}}
			}
			switch {
			case sharedAnsible:
				err = m.store.recordScheduledSharedAnsibleSnapshot(ctx, jobID, *occurrence, runMode, buckets.Ansible, resolution.Targets, now)
			case individualAnsible:
				err = m.store.recordScheduledIndividualAnsibleSnapshot(ctx, jobID, *occurrence, runMode, buckets.Ansible, resolution.Targets, now)
			default:
				err = m.store.recordScheduledGeneralSnapshot(ctx, jobID, *occurrence, resolution.Targets, now)
			}
			if err != nil {
				return err
			}
		}
		conn, err := m.store.db.Conn(ctx)
		if err != nil {
			return errors.Join(errOperatorStoreDown, err)
		}
		runs, err = loadScheduledRunsForOccurrence(ctx, conn, jobID, *occurrence)
		if closeErr := conn.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
	}
	expSeconds := schedulerParseExpiration(nullString(job.Expiration))
	for _, run := range runs {
		if scheduledTerminalStatus(nullString(run.Status)) || nullString(run.Status) == scheduledStatusRunning {
			continue
		}
		runID := nullInt(run.ID)
		if runID <= 0 {
			continue
		}
		if len(buckets.Workflow) == 1 {
			if _, err := m.enqueueWorkItem(ctx, schedulerKindScheduledWorkflowRun, map[string]any{
				"job_id":             jobID,
				"run_id":             runID,
				"scheduled_ts":       *occurrence,
				"site_id":            0,
				"workflow_component": buckets.Workflow[0],
				"task_link":          schedulerTaskLink(jobID, runID, "workflow"),
			}); err != nil {
				return err
			}
			continue
		}
		if sharedAnsible && boolInt64(run.SharedExecution) {
			componentIndex := int(nullInt(run.ComponentIndex))
			for siteID, targetRows := range m.siteTargetRowsForRun(ctx, runID) {
				if _, err := m.enqueueWorkItem(ctx, schedulerKindScheduledRun, map[string]any{
					"job_id":              jobID,
					"run_id":              runID,
					"scheduled_ts":        *occurrence,
					"site_id":             siteID,
					"run_mode":            runMode,
					"script_components":   []any{},
					"ansible_components":  mapsToAnyList(buckets.Ansible),
					"credential_id":       nullableInt64Any(job.CredentialID),
					"use_service_account": boolInt64(job.UseServiceAccount) && normalizeScheduledAnsibleTransport(runMode) == "winrm",
					"shared_execution":    true,
					"component_index":     componentIndex,
					"target_row_ids":      int64sToAnyList(targetRows),
					"task_link":           schedulerTaskLink(jobID, runID, "ansible"),
				}); err != nil {
					return err
				}
			}
			continue
		}
		host := strings.TrimSpace(nullString(run.TargetHostname))
		if host == "" {
			continue
		}
		if online[strings.ToLower(host)] {
			siteID := int64(0)
			for candidate := range m.siteTargetRowsForRun(ctx, runID) {
				siteID = candidate
				break
			}
			componentIndex := any(nil)
			if individualAnsible && run.ComponentIndex.Valid {
				componentIndex = run.ComponentIndex.Int64
			}
			if _, err := m.enqueueWorkItem(ctx, schedulerKindScheduledRun, map[string]any{
				"job_id":              jobID,
				"run_id":              runID,
				"scheduled_ts":        *occurrence,
				"site_id":             siteID,
				"run_mode":            runMode,
				"script_components":   mapsToAnyList(buckets.Script),
				"ansible_components":  mapsToAnyList(buckets.Ansible),
				"credential_id":       nullableInt64Any(job.CredentialID),
				"use_service_account": boolInt64(job.UseServiceAccount) && normalizeScheduledAnsibleTransport(runMode) == "winrm",
				"component_index":     componentIndex,
				"task_link":           schedulerTaskLink(jobID, runID, "scheduled_job"),
			}); err != nil {
				return err
			}
			continue
		}
		if expSeconds != nil && *occurrence+*expSeconds <= now {
			if err := m.markScheduledRunExpired(ctx, runID, now, "Device offline"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *goSchedulerManager) processPatchInstallTick(ctx context.Context, profile operatorProfile, job scheduledJobRow, jobID, occurrence, now int64, components, rawTargets []any, online map[string]bool) error {
	component := scheduledPatchInstallComponent(components)
	if component == nil {
		return nil
	}
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	runs, err := loadScheduledRunsForOccurrence(ctx, conn, jobID, occurrence)
	if closeErr := conn.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		resolution, err := m.store.resolveScheduledRerunTargets(ctx, profile, rawTargets)
		if err != nil {
			resolution = scheduledTargetResolution{Targets: []*scheduledResolvedTarget{}}
		}
		if err := m.store.recordScheduledPatchInstallSnapshot(ctx, jobID, occurrence, scheduledPatchInstallDisplayName(component), resolution.Targets, now); err != nil {
			return err
		}
		conn, err := m.store.db.Conn(ctx)
		if err != nil {
			return errors.Join(errOperatorStoreDown, err)
		}
		runs, err = loadScheduledRunsForOccurrence(ctx, conn, jobID, occurrence)
		if closeErr := conn.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
	}
	expSeconds := schedulerParseExpiration(nullString(job.Expiration))
	for _, run := range runs {
		if scheduledTerminalStatus(nullString(run.Status)) || nullString(run.Status) == scheduledStatusRunning {
			continue
		}
		runID := nullInt(run.ID)
		if runID <= 0 {
			continue
		}
		host := strings.TrimSpace(nullString(run.TargetHostname))
		if host == "" {
			continue
		}
		if online[strings.ToLower(host)] {
			siteID := int64(0)
			for candidate := range m.siteTargetRowsForRun(ctx, runID) {
				siteID = candidate
				break
			}
			if _, err := m.enqueueWorkItem(ctx, schedulerKindPatchInstallRun, map[string]any{
				"job_id":          jobID,
				"run_id":          runID,
				"scheduled_ts":    occurrence,
				"site_id":         siteID,
				"hostname":        host,
				"patch_component": component,
				"task_link":       schedulerTaskLink(jobID, runID, patchInstallComponentKind),
			}); err != nil {
				return err
			}
			continue
		}
		if expSeconds != nil && occurrence+*expSeconds <= now {
			if err := m.markScheduledRunExpired(ctx, runID, now, "Device offline"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *goSchedulerManager) processOnboardingTick(ctx context.Context, job scheduledJobRow, jobID, occurrence, now int64, components, targets []any) error {
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	runs, err := loadScheduledRunsForOccurrence(ctx, conn, jobID, occurrence)
	if closeErr := conn.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		if err := m.store.recordScheduledOnboardingSnapshot(ctx, jobID, occurrence, now); err != nil {
			return err
		}
		conn, err := m.store.db.Conn(ctx)
		if err != nil {
			return errors.Join(errOperatorStoreDown, err)
		}
		runs, err = loadScheduledRunsForOccurrence(ctx, conn, jobID, occurrence)
		if closeErr := conn.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
	}
	siteID := schedulerOnboardingSiteID(targets)
	if siteID <= 0 {
		for _, run := range runs {
			if nullInt(run.ID) > 0 && !scheduledTerminalStatus(nullString(run.Status)) {
				_ = m.markScheduledRunFailed(ctx, nullInt(run.ID), now, "Onboarding scope site is required.")
			}
		}
		return nil
	}
	for _, run := range runs {
		if scheduledTerminalStatus(nullString(run.Status)) || nullString(run.Status) == scheduledStatusRunning {
			continue
		}
		if _, err := m.enqueueWorkItem(ctx, schedulerKindOnboardingRun, map[string]any{
			"job_id":        jobID,
			"run_id":        nullInt(run.ID),
			"scheduled_ts":  occurrence,
			"site_id":       siteID,
			"components":    components,
			"targets":       targets,
			"credential_id": nullableInt64Any(job.CredentialID),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (m *goSchedulerManager) resolveOccurrenceForTick(ctx context.Context, jobID int64, scheduleType string, startTS *int64, createdAt *int64, nowMinute int64) (*int64, error) {
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	latest, err := latestScheduledOccurrence(ctx, conn, jobID)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if latest == nil {
		var occ *int64
		if strings.EqualFold(scheduleType, "immediately") {
			value := schedulerFloorMinute(nowMinute)
			if createdAt != nil {
				value = schedulerFloorMinute(*createdAt)
			}
			occ = &value
		} else if startTS != nil {
			value := schedulerFloorMinute(*startTS)
			occ = &value
		} else {
			occ = schedulerComputeNextRun(scheduleType, startTS, nil, nowMinute)
		}
		_ = conn.Close()
		if occ == nil || nowMinute < *occ {
			return nil, nil
		}
		return occ, nil
	}
	runs, err := loadScheduledRunsForOccurrence(ctx, conn, jobID, *latest)
	if closeErr := conn.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	for _, run := range runs {
		if !scheduledTerminalStatus(nullString(run.Status)) {
			value := *latest
			return &value, nil
		}
	}
	next := schedulerComputeNextRun(scheduleType, startTS, latest, nowMinute)
	if next == nil || nowMinute < *next {
		return nil, nil
	}
	return next, nil
}

func (m *goSchedulerManager) enqueueWorkItem(ctx context.Context, kind string, payload map[string]any) (int64, error) {
	item, updateRunRunning, err := schedulerWorkItemFromPayload(kind, payload)
	if err != nil {
		return 0, err
	}
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return 0, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	workID, err := insertSchedulerWorkItem(ctx, tx, item)
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	if updateRunRunning && item.RunID.Valid {
		now := time.Now().Unix()
		if _, err := tx.ExecContext(ctx, `UPDATE engine.scheduled_job_runs SET status=$1, started_ts=COALESCE(started_ts, $2), updated_at=$3 WHERE id=$4`, scheduledStatusRunning, now, now, item.RunID.Int64); err != nil {
			_ = tx.Rollback()
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return workID, nil
}

func (m *goSchedulerManager) processGlobalScheduledWork(ctx context.Context) error {
	item, err := m.claimNextKindWorkItem(ctx, []string{schedulerKindScheduledWorkflowRun}, "job-scheduler", 300)
	if err != nil || item == nil {
		return err
	}
	status := workStatusSucceeded
	errorText := ""
	if err := m.runGlobalWorkItem(ctx, *item); err != nil {
		status = workStatusFailed
		errorText = err.Error()
	}
	if err := m.completeWorkItem(ctx, item.ID, status, errorText); err != nil {
		return err
	}
	return nil
}

func (m *goSchedulerManager) runGlobalWorkItem(ctx context.Context, item schedulerWorkItem) error {
	switch item.Kind {
	case schedulerKindScheduledWorkflowRun:
		component := schedulerAnyMap(item.Payload["workflow_component"])
		workflowGUID := assemblyCoerceGUID(firstNonEmptyAny(component["assembly_guid"], component["assemblyGuid"], component["workflow_guid"], component["workflowGuid"]))
		if workflowGUID == "" {
			return m.markScheduledRunFailed(ctx, nullInt(item.RunID), time.Now().Unix(), "Workflow GUID is required.")
		}
		sourceMetadata := map[string]any{
			"scheduled_job_id":      nullInt(item.JobID),
			"scheduled_job_run_id":  nullInt(item.RunID),
			"scheduled_ts":          coerceInt64(item.Payload["scheduled_ts"]),
			"component_name":        scheduledComponentDisplayName(component, "Workflow"),
			"workflow_site_scope":   schedulerAnyMap(item.Payload["workflow_site_scope"]),
			"workflow_component_id": firstNonEmptyAny(component["id"], component["component_id"]),
		}
		body := map[string]any{
			"workflow_guid":    workflowGUID,
			"source_type":      "scheduled_job",
			"source_metadata":  sourceMetadata,
			"created_by":       "scheduler",
			"runner_username":  "job-scheduler",
			"runner_role":      "Admin",
			"scheduled_job_id": nullInt(item.JobID),
		}
		_, err := m.internalJSON(ctx, http.MethodPost, "/api/internal/job-scheduler/workflow/start", body, 30*time.Second)
		return err
	case schedulerKindScheduledRun:
		return m.markScheduledRunFailed(ctx, nullInt(item.RunID), time.Now().Unix(), "No site worker is available for unassigned scheduled run.")
	default:
		return fmt.Errorf("unsupported global work kind %s", item.Kind)
	}
}

func (m *goSchedulerManager) processAgentMaintenanceWork(ctx context.Context) error {
	leaseSeconds := int64(schedulerAgentMaintenanceSocketWaitSeconds() + 120)
	for i := 0; i < envInt("BOREALIS_AGENT_MAINTENANCE_MANAGER_BATCH", 4, 1, 32); i++ {
		item, err := m.claimNextKindWorkItem(ctx, []string{schedulerKindAgentMaintenanceRun}, "job-scheduler", leaseSeconds)
		if err != nil || item == nil {
			return err
		}
		claimed := *item
		go func() {
			status := workStatusSucceeded
			errorText := ""
			if err := m.runAgentMaintenanceWorkItem(ctx, claimed); err != nil {
				status = workStatusFailed
				errorText = err.Error()
			}
			if err := m.completeWorkItem(ctx, claimed.ID, status, errorText); err != nil {
				log.Printf("failed to complete agent maintenance work item id=%d: %v", claimed.ID, err)
			}
		}()
	}
	return nil
}

func (m *goSchedulerManager) runAgentMaintenanceWorkItem(ctx context.Context, item schedulerWorkItem) error {
	payload := item.Payload
	runID := firstPositiveInt64(coerceInt64(payload["run_id"]), nullInt(item.RunID))
	hostname := cleanText(payload["hostname"])
	operationID := cleanText(payload["operation_id"])
	eventPayload := schedulerAnyMap(payload["event_payload"])
	if runID <= 0 || hostname == "" || operationID == "" || len(eventPayload) == 0 {
		err := errors.New("agent maintenance work item payload incomplete")
		_ = m.updateAgentMaintenanceRunStatus(ctx, runID, "Failed", "", err.Error())
		return err
	}
	serviceMode := firstText(cleanText(payload["service_mode"]), "system")
	eventName := firstText(cleanText(payload["event_name"]), "agent_maintenance_request")
	action := cleanText(payload["action"])
	releaseChannel := cleanText(payload["release_channel"])
	branch := cleanText(payload["branch"])
	waitText := fmt.Sprintf(
		"Job scheduler waiting for Agent %s socket operation_id=%s action=%s release_channel=%s branch=%s\n",
		firstText(serviceMode, "system"),
		operationID,
		firstText(action, "-"),
		firstText(releaseChannel, "-"),
		firstText(branch, "-"),
	)
	if err := m.updateAgentMaintenanceRunStatus(ctx, runID, "Running", waitText, ""); err != nil {
		return err
	}

	deadline := time.Now().Add(time.Duration(schedulerAgentMaintenanceSocketWaitSeconds()) * time.Second)
	pollDelay := schedulerAgentMaintenanceSocketWaitPoll()
	var lastState string
	var lastErr error
	var response map[string]any
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		callTimeout := remaining
		if callTimeout > 30*time.Second {
			callTimeout = 30 * time.Second
		}
		snapshot, status, err := m.store.loadDeviceProcessContext(ctx, operatorProfile{Username: "job-scheduler", Role: "Admin"}, hostname)
		if err != nil || snapshot.Route == nil {
			lastState = "site_worker_unavailable"
			if err != nil {
				lastErr = err
			} else {
				lastErr = fmt.Errorf("site worker unavailable status=%d", status)
			}
		} else {
			body := map[string]any{
				"hostname":        firstText(snapshot.Hostname, hostname),
				"service_mode":    serviceMode,
				"event_name":      eventName,
				"timeout_seconds": callTimeout.Seconds(),
				"payload":         eventPayload,
			}
			response, lastState, lastErr = m.callSiteWorkerHostService(ctx, snapshot.Route, body, callTimeout+2*time.Second)
			if lastErr == nil {
				break
			}
			if lastState == "agent_error" || lastState == "invalid_agent_response" {
				break
			}
		}
		if time.Until(deadline) <= 0 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(minDuration(pollDelay, maxDuration(50*time.Millisecond, time.Until(deadline)))):
		}
	}
	if lastErr != nil {
		errorText := schedulerAgentMaintenanceError(hostname, lastState, lastErr)
		_ = m.updateAgentMaintenanceRunStatus(ctx, runID, "Failed", "", errorText)
		return errors.New(errorText)
	}
	responseStatus := strings.ToLower(cleanText(response["status"]))
	stdout := fmt.Sprintf(
		"Job scheduler delivered agent maintenance operation_id=%s action=%s release_channel=%s branch=%s\n",
		operationID,
		firstText(action, "-"),
		firstText(releaseChannel, "-"),
		firstText(branch, "-"),
	)
	if responseStatus != "" {
		stdout += "Agent response status=" + responseStatus + "\n"
	}
	return m.updateAgentMaintenanceRunStatus(ctx, runID, "Running", stdout, "")
}

func schedulerAgentMaintenanceSocketWaitSeconds() int {
	return envInt("BOREALIS_AGENT_MAINTENANCE_SOCKET_WAIT_SECONDS", 180, 1, 3600)
}

func schedulerAgentMaintenanceSocketWaitPoll() time.Duration {
	raw := strings.TrimSpace(os.Getenv("BOREALIS_AGENT_MAINTENANCE_SOCKET_WAIT_POLL_SECONDS"))
	if raw == "" {
		return 2 * time.Second
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0.05 {
		return 2 * time.Second
	}
	return time.Duration(value * float64(time.Second))
}

func schedulerAgentMaintenanceError(hostname string, state string, err error) string {
	detail := ""
	if err != nil {
		detail = strings.TrimSpace(err.Error())
	}
	switch state {
	case "agent_error":
		return fmt.Sprintf("Agent rejected maintenance request for host %s: %s", hostname, firstText(detail, "Agent rejected the maintenance request."))
	case "no_response":
		return fmt.Sprintf("Agent did not acknowledge maintenance request for host %s before timeout.", hostname)
	case "site_worker_unavailable":
		return fmt.Sprintf("No active site-worker route is available for host %s.", hostname)
	default:
		return fmt.Sprintf("Job scheduler could not dispatch agent maintenance for host %s: %s", hostname, firstText(detail, state))
	}
}

func (m *goSchedulerManager) callSiteWorkerHostService(ctx context.Context, route *agentWorkerRoute, body map[string]any, timeout time.Duration) (map[string]any, string, error) {
	target := workerInternalURL(route, "/remote-ops/host-service/call")
	if target == "" {
		return nil, "site_worker_unavailable", errors.New("site worker unavailable")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, "invalid_request", err
	}
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, target, bytes.NewReader(raw))
	if err != nil {
		return nil, "worker_request_failed", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(internalTokenHeader, goInternalToken(m.secret))
	client := m.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	if client.Timeout > 0 && timeout > client.Timeout {
		clone := *client
		clone.Timeout = timeout
		client = &clone
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "site_worker_unavailable", err
	}
	defer resp.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return nil, "invalid_worker_response", err
	}
	if resp.StatusCode >= 400 {
		state := firstText(cleanText(payload["error"]), "worker_error")
		return nil, state, errors.New(firstText(cleanText(payload["message"]), state))
	}
	if !boolFromAny(payload["called"]) {
		return nil, "no_response", errors.New("agent did not answer")
	}
	response := schedulerAnyMap(payload["response"])
	if len(response) == 0 {
		return nil, "invalid_agent_response", errors.New("invalid agent response")
	}
	if strings.EqualFold(cleanText(response["status"]), "error") {
		return response, "agent_error", errors.New(firstText(cleanText(response["detail"]), cleanText(response["message"]), cleanText(response["error"]), "agent_error"))
	}
	return response, "called", nil
}

func (m *goSchedulerManager) updateAgentMaintenanceRunStatus(ctx context.Context, runID int64, status string, stdout string, stderr string) error {
	if runID <= 0 {
		return nil
	}
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	now := time.Now().Unix()
	var finished any
	if stringInSet(status, "Success", "Failed", "Skipped") {
		finished = now
	}
	errorText := truncateString(stderr, 512)
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackQuietly(tx)
	if _, err := tx.ExecContext(ctx, `
		UPDATE engine.scheduled_job_runs
		   SET status=$1, updated_at=$2, finished_ts=COALESCE($3, finished_ts), error=$4
		 WHERE id=$5
	`, status, now, finished, errorText, runID); err != nil {
		return err
	}
	resolutionStatus := "eligible"
	if status == "Failed" {
		resolutionStatus = "unresolved"
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE engine.scheduled_job_run_targets
		   SET resolution_status=$1, resolution_reason=$2
		 WHERE run_id=$3
	`, resolutionStatus, errorText, runID); err != nil {
		return err
	}
	var activityID sql.NullInt64
	err = tx.QueryRowContext(ctx, `
		SELECT activity_id
		  FROM engine.scheduled_job_run_activity
		 WHERE run_id=$1
		 ORDER BY id ASC
		 LIMIT 1
	`, runID).Scan(&activityID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if activityID.Valid && activityID.Int64 > 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE engine.activity_history
			   SET status=$1,
			       stdout=COALESCE(stdout, '') || $2,
			       stderr=COALESCE(stderr, '') || $3,
			       updated_at=$4,
			       finished_at=COALESCE($5, finished_at)
			 WHERE id=$6
		`, status, stdout, stderr, now, finished, activityID.Int64); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (m *goSchedulerManager) processServiceAction(ctx context.Context) error {
	item, err := m.claimNextWorkItem(ctx, 0, []string{schedulerLaneServiceAction}, "job-scheduler", 300)
	if err != nil || item == nil {
		return err
	}
	status := workStatusSucceeded
	errorText := ""
	if err := m.runServiceAction(ctx, item.Payload); err != nil {
		status = workStatusFailed
		errorText = err.Error()
	}
	return m.completeWorkItem(ctx, item.ID, status, errorText)
}

func (m *goSchedulerManager) runServiceAction(ctx context.Context, payload map[string]any) error {
	serviceKey := strings.ToLower(cleanText(payload["service_key"]))
	action := schedulerAnyMap(payload["action"])
	actionName := strings.ToLower(cleanText(action["action"]))
	actionMode := strings.ToLower(cleanText(action["mode"]))
	if serviceKey == "" || actionName == "" {
		return errors.New("service action payload incomplete")
	}
	if serviceKey == "site-worker" && actionName == "recreate" {
		return m.runSiteWorkerRecreate(ctx, action)
	}
	dockerBin := schedulerDockerBin()
	if dockerBin == "" {
		return errors.New("docker CLI unavailable")
	}
	image := schedulerServiceActionHelperImage()
	helperName := "borealis-engine-action-" + serviceKey + "-" + randomShortID()
	commandParts := []string{"bash", "Engine.sh", "--service", serviceKey, actionName}
	if actionMode != "" {
		commandParts = append(commandParts, actionMode)
	}
	shellCommand := "sleep 2; " + shellJoin(commandParts)
	args := []string{
		"run", "--rm", "-d", "--name", helperName, "--network", "host",
		"-v", "/var/run/docker.sock:/var/run/docker.sock",
		"-v", fmt.Sprintf("%s:%s", m.projectRoot, m.projectRoot),
		"-w", m.projectRoot,
		"--entrypoint", "/bin/bash",
		image, "-lc", shellCommand,
	}
	out, err := exec.CommandContext(ctx, dockerBin, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	log.Printf("queued service action helper=%s service=%s action=%s", strings.TrimSpace(string(out)), serviceKey, actionName)
	return nil
}

func (m *goSchedulerManager) runSiteWorkerRecreate(ctx context.Context, action map[string]any) error {
	dockerBin := schedulerDockerBin()
	if dockerBin == "" {
		return errors.New("docker CLI unavailable")
	}
	workerGUID := cleanText(action["worker_guid"])
	containerName := cleanText(action["container_name"])
	if containerName == "" && workerGUID != "" {
		containerName = "site-worker-" + workerGUID
	}
	if containerName == "" {
		return errors.New("site-worker recreate payload missing container name")
	}
	out, err := exec.CommandContext(ctx, dockerBin, "inspect", containerName).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "No such") && strings.HasPrefix(containerName, "site-worker-") {
			return nil
		}
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	var inspected []map[string]any
	if err := json.Unmarshal(out, &inspected); err != nil {
		return err
	}
	record := map[string]any{}
	if len(inspected) > 0 {
		record = inspected[0]
	}
	labels := mapStringAny(nestedMap(record, "Config")["Labels"])
	role := strings.ToLower(cleanText(labels["borealis.role"]))
	labelGUID := cleanText(labels["borealis.worker_guid"])
	if role != "site-worker" && !strings.HasPrefix(containerName, "site-worker-") {
		return fmt.Errorf("refusing to stop non-site-worker container %s", containerName)
	}
	if workerGUID != "" && labelGUID != "" && workerGUID != labelGUID {
		return fmt.Errorf("site-worker guid mismatch for %s", containerName)
	}
	out, err = exec.CommandContext(ctx, dockerBin, "stop", containerName).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "No such") {
			return nil
		}
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	log.Printf("site-worker recreate stopped container=%s worker_guid=%s", containerName, workerGUID)
	return nil
}

func (m *goSchedulerManager) claimNextWorkItem(ctx context.Context, siteID int64, lanes []string, leaseOwner string, leaseSeconds int64) (*schedulerWorkItem, error) {
	if len(lanes) == 0 {
		return nil, nil
	}
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer rollbackQuietly(tx)
	now := time.Now().Unix()
	rows, err := tx.QueryContext(ctx, `
		SELECT id
		  FROM engine.job_scheduler_work_items
		 WHERE site_id=$1
		   AND lane = ANY($2)
		   AND status=$3
		   AND available_at <= $4
		 ORDER BY priority DESC, available_at ASC, id ASC
		 LIMIT 1
		 FOR UPDATE SKIP LOCKED
	`, siteID, pq.Array(lanes), workStatusQueued, now)
	if err != nil {
		return nil, err
	}
	var workID int64
	if rows.Next() {
		if err := rows.Scan(&workID); err != nil {
			_ = rows.Close()
			return nil, err
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if workID <= 0 {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	lease := now + maxInt64(leaseSeconds, 30)
	if _, err := tx.ExecContext(ctx, `
		UPDATE engine.job_scheduler_work_items
		   SET status=$1, lease_owner=$2, lease_expires_at=$3, heartbeat_at=$4,
		       worker_guid=$5, container_name=NULL, attempt_count=attempt_count+1,
		       started_at=COALESCE(started_at, $6), updated_at=$7
		 WHERE id=$8
	`, workStatusRunning, leaseOwner, lease, now, leaseOwner, now, now, workID); err != nil {
		return nil, err
	}
	var item schedulerWorkItem
	var payloadRaw sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT id, kind, site_id, lane, job_id, run_id, payload_json, attempt_count
		  FROM engine.job_scheduler_work_items
		 WHERE id=$1
	`, workID).Scan(&item.ID, &item.Kind, &item.SiteID, &item.Lane, &item.JobID, &item.RunID, &payloadRaw, &item.AttemptCount)
	if err != nil {
		return nil, err
	}
	item.Payload = schedulerJSONMap(payloadRaw)
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &item, nil
}

func (m *goSchedulerManager) claimNextKindWorkItem(ctx context.Context, kinds []string, leaseOwner string, leaseSeconds int64) (*schedulerWorkItem, error) {
	normalizedKinds := []string{}
	for _, kind := range kinds {
		if cleaned := strings.TrimSpace(kind); cleaned != "" {
			normalizedKinds = append(normalizedKinds, cleaned)
		}
	}
	if len(normalizedKinds) == 0 {
		return nil, nil
	}
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer rollbackQuietly(tx)
	now := time.Now().Unix()
	rows, err := tx.QueryContext(ctx, `
		SELECT id
		  FROM engine.job_scheduler_work_items
		 WHERE kind = ANY($1)
		   AND status=$2
		   AND available_at <= $3
		 ORDER BY priority DESC, available_at ASC, id ASC
		 LIMIT 1
		 FOR UPDATE SKIP LOCKED
	`, pq.Array(normalizedKinds), workStatusQueued, now)
	if err != nil {
		return nil, err
	}
	var workID int64
	if rows.Next() {
		if err := rows.Scan(&workID); err != nil {
			_ = rows.Close()
			return nil, err
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if workID <= 0 {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	lease := now + maxInt64(leaseSeconds, 30)
	if _, err := tx.ExecContext(ctx, `
		UPDATE engine.job_scheduler_work_items
		   SET status=$1, lease_owner=$2, lease_expires_at=$3, heartbeat_at=$4,
		       worker_guid=$5, container_name=NULL, attempt_count=attempt_count+1,
		       started_at=COALESCE(started_at, $6), updated_at=$7
		 WHERE id=$8
	`, workStatusRunning, leaseOwner, lease, now, leaseOwner, now, now, workID); err != nil {
		return nil, err
	}
	var item schedulerWorkItem
	var payloadRaw sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT id, kind, site_id, lane, job_id, run_id, payload_json, attempt_count
		  FROM engine.job_scheduler_work_items
		 WHERE id=$1
	`, workID).Scan(&item.ID, &item.Kind, &item.SiteID, &item.Lane, &item.JobID, &item.RunID, &payloadRaw, &item.AttemptCount)
	if err != nil {
		return nil, err
	}
	item.Payload = schedulerJSONMap(payloadRaw)
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &item, nil
}

func (m *goSchedulerManager) completeWorkItem(ctx context.Context, workID int64, status string, errorText string) error {
	normalized := status
	if !stringInSet(normalized, workStatusSucceeded, workStatusFailed, workStatusCancelled) {
		normalized = workStatusFailed
	}
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	now := time.Now().Unix()
	_, err = conn.ExecContext(ctx, `
		UPDATE engine.job_scheduler_work_items
		   SET status=$1, lease_expires_at=NULL, heartbeat_at=$2, error=$3,
		       finished_at=$4, updated_at=$5
		 WHERE id=$6
	`, normalized, now, truncateString(errorText, 2000), now, now, workID)
	return err
}

func (m *goSchedulerManager) expireStaleLeases(ctx context.Context) error {
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	now := time.Now().Unix()
	_, err = conn.ExecContext(ctx, `
		UPDATE engine.job_scheduler_work_items
		   SET status=$1, lease_owner=NULL, lease_expires_at=NULL, heartbeat_at=NULL,
		       worker_guid=NULL, container_name=NULL, error=$2, available_at=$3, updated_at=$4
		 WHERE status=$5
		   AND lease_expires_at IS NOT NULL
		   AND lease_expires_at < $6
	`, workStatusQueued, "requeued after work lease expired", now, now, workStatusRunning, now)
	return err
}

func (m *goSchedulerManager) markLostWorkers(ctx context.Context) error {
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	now := time.Now().Unix()
	staleBefore := now - int64(envInt("BOREALIS_SITE_WORKER_LOST_SECONDS", 180, 30, 3600))
	routeFiles, err := schedulerRouteFilesForStaleWorkers(ctx, conn, staleBefore)
	if err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, `
		UPDATE engine.job_scheduler_workers
		   SET status=$1, stopped_at=COALESCE(stopped_at, $2), updated_at=$3
		 WHERE site_id > 0
		   AND status IN ($4,$5,$6)
		   AND last_seen_at < $7
	`, schedulerWorkerStatusLost, now, now, schedulerWorkerStatusStarting, schedulerWorkerStatusRunning, schedulerWorkerStatusIdle, staleBefore)
	if err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, `
		UPDATE engine.job_scheduler_worker_routes
		   SET status=$1, retired_at=COALESCE(retired_at, $2), updated_at=$3
		 WHERE status=$4
		   AND worker_guid IN (
		       SELECT worker_guid FROM engine.job_scheduler_workers WHERE status=$5
		   )
	`, schedulerRouteStatusLost, now, now, schedulerRouteStatusActive, schedulerWorkerStatusLost)
	if err == nil {
		schedulerRemoveRouteFiles(routeFiles)
	}
	return err
}

func (m *goSchedulerManager) pruneWorkerHistory(ctx context.Context, retentionSeconds int) error {
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	cutoff := time.Now().Unix() - int64(retentionSeconds)
	_, err = conn.ExecContext(ctx, `
		DELETE FROM engine.job_scheduler_workers
		 WHERE status IN ($1,$2)
		   AND COALESCE(stopped_at, updated_at, last_seen_at) < $3
	`, schedulerWorkerStatusStopped, schedulerWorkerStatusLost, cutoff)
	return err
}

func (m *goSchedulerManager) siteIDsNeedingWorkers(ctx context.Context) ([]int64, error) {
	queued, err := m.queuedSiteIDs(ctx)
	if err != nil {
		return nil, err
	}
	counts, err := m.store.loadSchedulerOnlineSites(ctx, int64(envInt("BOREALIS_AGENT_ONLINE_WINDOW_SECONDS", 300, 60, 3600)), nil)
	if err != nil {
		return nil, err
	}
	seen := map[int64]struct{}{}
	for _, siteID := range queued {
		if siteID > 0 {
			seen[siteID] = struct{}{}
		}
	}
	for siteID := range counts {
		if siteID > 0 {
			seen[siteID] = struct{}{}
		}
	}
	out := make([]int64, 0, len(seen))
	for siteID := range seen {
		out = append(out, siteID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func (m *goSchedulerManager) queuedSiteIDs(ctx context.Context) ([]int64, error) {
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, `
		SELECT DISTINCT site_id
		  FROM engine.job_scheduler_work_items
		 WHERE status=$1
		   AND site_id IS NOT NULL
		   AND site_id > 0
		 ORDER BY site_id ASC
	`, workStatusQueued)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var siteID sql.NullInt64
		if err := rows.Scan(&siteID); err != nil {
			return nil, err
		}
		if siteID.Valid && siteID.Int64 > 0 {
			out = append(out, siteID.Int64)
		}
	}
	return out, rows.Err()
}

func (m *goSchedulerManager) spawnSiteWorker(ctx context.Context, siteID int64) error {
	if siteID <= 0 {
		return nil
	}
	active, err := m.activeWorkerForSite(ctx, siteID)
	if err != nil || active {
		return err
	}
	workerGUID := randomUUID()
	containerName := "site-worker-" + workerGUID
	remoteOpsPort := schedulerWorkerPort(workerGUID, siteID, "BOREALIS_SITE_WORKER_REMOTE_OPS_PORT_BASE", "BOREALIS_SITE_WORKER_REMOTE_OPS_PORT_RANGE", schedulerDefaultRemoteOpsPortBase, schedulerDefaultRemoteOpsPortRange)
	remoteDesktopPort := schedulerWorkerPort(workerGUID, siteID, "BOREALIS_SITE_WORKER_REMOTE_DESKTOP_PORT_BASE", "BOREALIS_SITE_WORKER_REMOTE_DESKTOP_PORT_RANGE", schedulerDefaultRemoteDeskPortBase, schedulerDefaultRemoteDeskPortRange)
	metadata := schedulerWorkerRouteMetadata(workerGUID, remoteOpsPort, remoteDesktopPort)
	if err := m.upsertWorker(ctx, workerGUID, containerName, siteID, schedulerWorkerStatusStarting, []string{}, nil, remoteOpsPort, metadata); err != nil {
		return err
	}
	dockerBin := schedulerDockerBin()
	if dockerBin == "" {
		_ = m.stopWorker(ctx, workerGUID, schedulerWorkerStatusLost)
		return errors.New("docker CLI unavailable")
	}
	image := schedulerDesiredSiteWorkerImage()
	apiRoot := filepath.Join(m.projectRoot, "Engine", "Services", "api-backend")
	for _, path := range []string{
		filepath.Join(apiRoot, "cache"),
		filepath.Join(apiRoot, "logs", "site-workers"),
		filepath.Join(m.projectRoot, "Engine", "Services", "traefik-edge", "config"),
	} {
		_ = os.MkdirAll(path, 0o755)
	}
	args := []string{
		"run", "--rm", "-d", "--name", containerName, "--network", "host",
		"--label", "borealis.role=site-worker",
		"--label", fmt.Sprintf("borealis.site_id=%d", siteID),
		"--label", "borealis.worker_guid=" + workerGUID,
		"--label", fmt.Sprintf("borealis.remote_ops_port=%d", remoteOpsPort),
		"--label", fmt.Sprintf("borealis.remote_desktop_port=%d", remoteDesktopPort),
		"--label", "borealis.site_worker_image=" + image,
		"--label", "borealis.created_by=job-scheduler",
	}
	envFile := schedulerComposeEnvFile(m.projectRoot)
	if fileExists(envFile) {
		args = append(args, "--env-file", envFile)
	}
	args = append(args,
		"-e", "BOREALIS_SITE_WORKER_GUID="+workerGUID,
		"-e", fmt.Sprintf("BOREALIS_SITE_WORKER_SITE_ID=%d", siteID),
		"-e", "BOREALIS_SITE_WORKER_CONTAINER_NAME="+containerName,
		"-e", "BOREALIS_SITE_WORKER_REMOTE_OPS_HOST=127.0.0.1",
		"-e", fmt.Sprintf("BOREALIS_SITE_WORKER_REMOTE_OPS_PORT=%d", remoteOpsPort),
		"-e", fmt.Sprintf("BOREALIS_SITE_WORKER_REMOTE_DESKTOP_PORT=%d", remoteDesktopPort),
		"-e", "BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE="+schedulerSiteWorkerSocketIOAsyncMode(),
		"-e", "BOREALIS_SITE_WORKER_IDLE_TTL_SECONDS=300",
		"-e", "BOREALIS_INTERNAL_API_BASE_URL="+m.apiBase,
		"-e", fmt.Sprintf("BOREALIS_LOG_FILE=/opt/Borealis/Engine/Services/api-backend/logs/site-workers/%s.log", workerGUID),
		"-e", fmt.Sprintf("BOREALIS_ERROR_LOG_FILE=/opt/Borealis/Engine/Services/api-backend/logs/site-workers/%s-error.log", workerGUID),
		"-e", fmt.Sprintf("BOREALIS_API_LOG_FILE=/opt/Borealis/Engine/Services/api-backend/logs/site-workers/%s-api.log", workerGUID),
		"-e", fmt.Sprintf("BOREALIS_VPN_TUNNEL_LOG_FILE=/opt/Borealis/Engine/Services/api-backend/logs/site-workers/%s-vpn.log", workerGUID),
	)
	if explicit := strings.TrimSpace(os.Getenv("BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY")); explicit != "" {
		args = append(args, "-e", "BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY="+explicit)
	}
	args = append(args,
		"-v", fmt.Sprintf("%s:/opt/Borealis/Engine/Services/api-backend/logs/site-workers", filepath.Join(apiRoot, "logs", "site-workers")),
		"-v", fmt.Sprintf("%s:/opt/Borealis/Engine/Services/traefik-edge/config", filepath.Join(m.projectRoot, "Engine", "Services", "traefik-edge", "config")),
		"-v", fmt.Sprintf("%s:/opt/Borealis/Engine/Services/api-backend/secrets:ro", filepath.Join(apiRoot, "secrets")),
		"-v", fmt.Sprintf("%s:/opt/Borealis/Engine/Services/api-backend/config:ro", filepath.Join(apiRoot, "config")),
		"-v", fmt.Sprintf("%s:/opt/Borealis/Engine/Services/api-backend/cache", filepath.Join(apiRoot, "cache")),
		image,
	)
	out, err := exec.CommandContext(ctx, dockerBin, args...).CombinedOutput()
	if err != nil {
		_ = m.stopWorker(ctx, workerGUID, schedulerWorkerStatusLost)
		return fmt.Errorf("failed to launch %s: %s", containerName, strings.TrimSpace(string(out)))
	}
	log.Printf("launched %s for site_id=%d", containerName, siteID)
	return nil
}

func (m *goSchedulerManager) activeWorkerForSite(ctx context.Context, siteID int64) (bool, error) {
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return false, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	var count int64
	err = conn.QueryRowContext(ctx, `
		SELECT COUNT(*)
		  FROM engine.job_scheduler_workers
		 WHERE site_id=$1
		   AND status IN ($2,$3,$4)
	`, siteID, schedulerWorkerStatusStarting, schedulerWorkerStatusRunning, schedulerWorkerStatusIdle).Scan(&count)
	return count > 0, err
}

func (m *goSchedulerManager) reconcileSiteWorkers(ctx context.Context) error {
	snapshots, err := schedulerDockerSiteWorkerSnapshots(ctx)
	if err != nil {
		log.Printf("site-worker docker reconcile skipped: %v", err)
		return nil
	}
	desiredImage := schedulerDesiredSiteWorkerImage()
	sort.SliceStable(snapshots, func(i, j int) bool {
		leftSite := coerceInt64(snapshots[i]["site_id"])
		rightSite := coerceInt64(snapshots[j]["site_id"])
		if leftSite != rightSite {
			return leftSite < rightSite
		}
		leftMatches := schedulerSiteWorkerImageMatches(snapshots[i], desiredImage)
		rightMatches := schedulerSiteWorkerImageMatches(snapshots[j], desiredImage)
		if leftMatches != rightMatches {
			return leftMatches
		}
		leftCreated := coerceInt64(snapshots[i]["created_at"])
		rightCreated := coerceInt64(snapshots[j]["created_at"])
		if leftCreated != rightCreated {
			return leftCreated > rightCreated
		}
		return cleanText(snapshots[i]["worker_guid"]) < cleanText(snapshots[j]["worker_guid"])
	})
	live := []string{}
	liveSites := map[int64]string{}
	for _, snapshot := range snapshots {
		workerGUID := cleanText(snapshot["worker_guid"])
		siteID := coerceInt64(snapshot["site_id"])
		if workerGUID == "" || siteID <= 0 {
			continue
		}
		containerName := firstText(cleanText(snapshot["container_name"]), "site-worker-"+workerGUID)
		if !schedulerSiteWorkerImageMatches(snapshot, desiredImage) {
			if err := schedulerStopContainer(ctx, containerName); err != nil {
				log.Printf("failed to stop stale site-worker container=%s worker_guid=%s: %v", containerName, workerGUID, err)
			} else {
				log.Printf("stopped stale site-worker container=%s worker_guid=%s image=%s desired=%s", containerName, workerGUID, cleanText(snapshot["configured_image"]), desiredImage)
			}
			_ = m.stopWorker(ctx, workerGUID, schedulerWorkerStatusLost)
			continue
		}
		if existing := liveSites[siteID]; existing != "" {
			if err := schedulerStopContainer(ctx, containerName); err != nil {
				log.Printf("failed to stop duplicate site-worker container=%s worker_guid=%s site_id=%d: %v", containerName, workerGUID, siteID, err)
			} else {
				log.Printf("stopped duplicate site-worker container=%s worker_guid=%s site_id=%d kept_worker_guid=%s", containerName, workerGUID, siteID, existing)
			}
			_ = m.stopWorker(ctx, workerGUID, schedulerWorkerStatusLost)
			continue
		}
		liveSites[siteID] = workerGUID
		live = append(live, workerGUID)
		remoteOpsPort := coerceInt64(snapshot["remote_ops_port"])
		if remoteOpsPort <= 0 {
			remoteOpsPort = schedulerWorkerPort(workerGUID, siteID, "BOREALIS_SITE_WORKER_REMOTE_OPS_PORT_BASE", "BOREALIS_SITE_WORKER_REMOTE_OPS_PORT_RANGE", schedulerDefaultRemoteOpsPortBase, schedulerDefaultRemoteOpsPortRange)
		}
		remoteDesktopPort := coerceInt64(snapshot["remote_desktop_port"])
		if remoteDesktopPort <= 0 {
			remoteDesktopPort = schedulerWorkerPort(workerGUID, siteID, "BOREALIS_SITE_WORKER_REMOTE_DESKTOP_PORT_BASE", "BOREALIS_SITE_WORKER_REMOTE_DESKTOP_PORT_RANGE", schedulerDefaultRemoteDeskPortBase, schedulerDefaultRemoteDeskPortRange)
		}
		if err := m.upsertWorker(ctx, workerGUID, containerName, siteID, schedulerWorkerStatusRunning, nil, nil, remoteOpsPort, schedulerWorkerRouteMetadata(workerGUID, remoteOpsPort, remoteDesktopPort)); err != nil {
			return err
		}
		if err := m.updateWorkerDockerState(ctx, workerGUID, cleanText(snapshot["docker_state"]), coerceInt64Ptr(snapshot["exit_code"])); err != nil {
			return err
		}
	}
	return m.markMissingWorkersLost(ctx, live)
}

func (m *goSchedulerManager) upsertWorker(ctx context.Context, workerGUID, containerName string, siteID int64, status string, lanes []string, taskLinks []map[string]any, upstreamPort int64, routeMetadata map[string]any) error {
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	now := time.Now().Unix()
	lanesJSON := mustJSON(lanes)
	linksJSON := mustJSON(taskLinks)
	_, err = conn.ExecContext(ctx, `
		INSERT INTO engine.job_scheduler_workers(
			worker_guid, container_name, site_id, status, started_at, last_seen_at,
			current_lanes_json, claimed_count, task_links_json, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,0,$8,$9,$10)
		ON CONFLICT (worker_guid) DO UPDATE SET
			container_name=EXCLUDED.container_name,
			site_id=EXCLUDED.site_id,
			status=EXCLUDED.status,
			last_seen_at=EXCLUDED.last_seen_at,
			current_lanes_json=EXCLUDED.current_lanes_json,
			task_links_json=EXCLUDED.task_links_json,
			updated_at=EXCLUDED.updated_at
	`, workerGUID, containerName, siteID, status, now, now, lanesJSON, linksJSON, now, now)
	if err != nil {
		return err
	}
	if siteID > 0 && upstreamPort > 0 {
		return m.upsertWorkerRoute(ctx, workerGUID, containerName, siteID, upstreamPort, routeMetadata)
	}
	return nil
}

func (m *goSchedulerManager) upsertWorkerRoute(ctx context.Context, workerGUID, containerName string, siteID int64, upstreamPort int64, metadata map[string]any) error {
	route := schedulerBuildRoute(workerGUID, containerName, siteID, upstreamPort, metadata)
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	now := time.Now().Unix()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackQuietly(tx)
	retiredFiles := []string{}
	rows, err := tx.QueryContext(ctx, `
		SELECT route_file_path
		  FROM engine.job_scheduler_worker_routes
		 WHERE site_id=$1
		   AND status=$2
		   AND worker_guid<>$3
	`, siteID, schedulerRouteStatusActive, workerGUID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var path sql.NullString
		if err := rows.Scan(&path); err != nil {
			_ = rows.Close()
			return err
		}
		if path.Valid {
			retiredFiles = append(retiredFiles, path.String)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE engine.job_scheduler_worker_routes
		   SET status=$1, generation=generation+1, updated_at=$2, retired_at=COALESCE(retired_at, $3)
		 WHERE site_id=$4
		   AND status=$5
		   AND worker_guid<>$6
	`, schedulerRouteStatusRetired, now, now, siteID, schedulerRouteStatusActive, workerGUID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO engine.job_scheduler_worker_routes(
			worker_guid, site_id, container_name, route_name, route_path_prefix,
			route_file_path, upstream_scheme, upstream_host, upstream_port,
			status, generation, metadata_json, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,1,$11,$12,$13)
		ON CONFLICT (worker_guid) DO UPDATE SET
			site_id=EXCLUDED.site_id,
			container_name=EXCLUDED.container_name,
			route_name=EXCLUDED.route_name,
			route_path_prefix=EXCLUDED.route_path_prefix,
			route_file_path=EXCLUDED.route_file_path,
			upstream_scheme=EXCLUDED.upstream_scheme,
			upstream_host=EXCLUDED.upstream_host,
			upstream_port=EXCLUDED.upstream_port,
			status=EXCLUDED.status,
			metadata_json=EXCLUDED.metadata_json,
			updated_at=EXCLUDED.updated_at
	`, route.WorkerGUID, route.SiteID, route.ContainerName, route.RouteName, route.RoutePath, route.RouteFilePath, route.UpstreamScheme, route.UpstreamHost, route.UpstreamPort, route.Status, mustJSON(route.Metadata), now, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	schedulerRemoveRouteFiles(retiredFiles)
	return schedulerWriteRouteFile(route)
}

func (m *goSchedulerManager) stopWorker(ctx context.Context, workerGUID, status string) error {
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	now := time.Now().Unix()
	routeFiles, err := schedulerRouteFilesForWorkers(ctx, conn, []string{workerGUID})
	if err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, `
		UPDATE engine.job_scheduler_workers
		   SET status=$1, stopped_at=COALESCE(stopped_at, $2), updated_at=$3
		 WHERE worker_guid=$4
	`, status, now, now, workerGUID)
	if err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, `
		UPDATE engine.job_scheduler_worker_routes
		   SET status=$1, retired_at=COALESCE(retired_at, $2), updated_at=$3
		 WHERE worker_guid=$4
		   AND status=$5
	`, schedulerRouteStatusLost, now, now, workerGUID, schedulerRouteStatusActive)
	if err != nil {
		return err
	}
	schedulerRemoveRouteFiles(routeFiles)
	return nil
}

func (m *goSchedulerManager) updateWorkerDockerState(ctx context.Context, workerGUID, dockerState string, exitCode *int64) error {
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	_, err = conn.ExecContext(ctx, `
		UPDATE engine.job_scheduler_workers
		   SET docker_state=$1, exit_code=$2, updated_at=$3
		 WHERE worker_guid=$4
	`, nullIfEmpty(dockerState), exitCode, time.Now().Unix(), workerGUID)
	return err
}

func (m *goSchedulerManager) markMissingWorkersLost(ctx context.Context, live []string) error {
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	now := time.Now().Unix()
	routeFiles := []string{}
	if len(live) == 0 {
		routeFiles, err = schedulerRouteFilesForMissingWorkers(ctx, conn, nil)
		if err != nil {
			return err
		}
		_, err = conn.ExecContext(ctx, `
			UPDATE engine.job_scheduler_workers
			   SET status=$1, stopped_at=COALESCE(stopped_at, $2), updated_at=$3
			 WHERE site_id > 0
			   AND status IN ($4,$5,$6)
		`, schedulerWorkerStatusLost, now, now, schedulerWorkerStatusStarting, schedulerWorkerStatusRunning, schedulerWorkerStatusIdle)
		if err != nil {
			return err
		}
		_, err = conn.ExecContext(ctx, `
			UPDATE engine.job_scheduler_worker_routes
			   SET status=$1, retired_at=COALESCE(retired_at, $2), updated_at=$3
			 WHERE status=$4
			   AND worker_guid IN (
			       SELECT worker_guid FROM engine.job_scheduler_workers WHERE site_id > 0 AND status=$5
			   )
		`, schedulerRouteStatusLost, now, now, schedulerRouteStatusActive, schedulerWorkerStatusLost)
		if err != nil {
			return err
		}
		schedulerRemoveRouteFiles(routeFiles)
		return nil
	}
	routeFiles, err = schedulerRouteFilesForMissingWorkers(ctx, conn, live)
	if err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, `
		UPDATE engine.job_scheduler_workers
		   SET status=$1, stopped_at=COALESCE(stopped_at, $2), updated_at=$3
		 WHERE site_id > 0
		   AND status IN ($4,$5,$6)
		   AND NOT (worker_guid = ANY($7))
	`, schedulerWorkerStatusLost, now, now, schedulerWorkerStatusStarting, schedulerWorkerStatusRunning, schedulerWorkerStatusIdle, pq.Array(live))
	if err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, `
		UPDATE engine.job_scheduler_worker_routes
		   SET status=$1, retired_at=COALESCE(retired_at, $2), updated_at=$3
		 WHERE status=$4
		   AND worker_guid IN (
		       SELECT worker_guid FROM engine.job_scheduler_workers
		        WHERE site_id > 0 AND status=$5 AND NOT (worker_guid = ANY($6))
		   )
	`, schedulerRouteStatusLost, now, now, schedulerRouteStatusActive, schedulerWorkerStatusLost, pq.Array(live))
	if err != nil {
		return err
	}
	schedulerRemoveRouteFiles(routeFiles)
	return nil
}

func (m *goSchedulerManager) refreshServiceSnapshots(ctx context.Context) error {
	dockerBin := schedulerDockerBin()
	if dockerBin == "" {
		return nil
	}
	composeFile := filepath.Join(m.projectRoot, "Data", "Engine", "Containers", "compose.yaml")
	envFile := schedulerComposeEnvFile(m.projectRoot)
	if !fileExists(composeFile) || !fileExists(envFile) {
		return nil
	}
	projectName := envDefault("BOREALIS_COMPOSE_PROJECT_NAME", "borealis-engine")
	args := []string{"compose", "--project-name", projectName, "--env-file", envFile, "-f", composeFile, "ps", "--format", "json"}
	out, err := exec.CommandContext(ctx, dockerBin, args...).Output()
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return nil
	}
	snapshots := parseDockerComposePS(out)
	if len(snapshots) == 0 {
		return nil
	}
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	now := time.Now().Unix()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackQuietly(tx)
	for _, snapshot := range snapshots {
		serviceKey := firstText(cleanText(snapshot["Service"]), cleanText(snapshot["Name"]))
		if serviceKey == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO engine.job_scheduler_service_snapshots(service_key, payload_json, updated_at)
			VALUES($1,$2,$3)
			ON CONFLICT(service_key) DO UPDATE SET payload_json=EXCLUDED.payload_json, updated_at=EXCLUDED.updated_at
		`, serviceKey, mustJSON(snapshot), now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (m *goSchedulerManager) expireRunningScheduledRuns(ctx context.Context, now int64) error {
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, `
		SELECT r.id, r.started_ts, j.expiration, r.component_kind
		  FROM engine.scheduled_job_runs r
		  JOIN engine.scheduled_jobs j ON j.id = r.job_id
		 WHERE r.status=$1
	`, scheduledStatusRunning)
	if err != nil {
		return err
	}
	type expiringRun struct {
		ID        int64
		StartedTS int64
		Seconds   int64
		Message   string
	}
	candidates := []expiringRun{}
	orphanTimeout := scheduledRunOrphanTimeoutSeconds()
	for rows.Next() {
		var id, started sql.NullInt64
		var expiration, componentKind sql.NullString
		if err := rows.Scan(&id, &started, &expiration, &componentKind); err != nil {
			_ = rows.Close()
			return err
		}
		if strings.EqualFold(nullString(componentKind), "workflow") || !id.Valid || !started.Valid {
			continue
		}
		seconds := scheduledRunTimeoutSeconds(nullString(expiration), nullString(componentKind), orphanTimeout)
		if seconds == nil {
			continue
		}
		if started.Int64+*seconds <= now {
			candidates = append(candidates, expiringRun{
				ID:        id.Int64,
				StartedTS: started.Int64,
				Seconds:   *seconds,
				Message:   fmt.Sprintf("Scheduled run timed out after %s without Agent completion.", durationForOperator(*seconds)),
			})
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, candidate := range candidates {
		if err := markScheduledRunTerminalTx(ctx, conn, candidate.ID, scheduledStatusTimedOut, now, candidate.Message); err != nil {
			return err
		}
	}
	return rows.Err()
}

func scheduledRunOrphanTimeoutSeconds() int64 {
	raw := strings.TrimSpace(os.Getenv("BOREALIS_SCHEDULED_RUN_ORPHAN_TIMEOUT_SECONDS"))
	if raw == "0" {
		return 0
	}
	return int64(envInt("BOREALIS_SCHEDULED_RUN_ORPHAN_TIMEOUT_SECONDS", 3600, 60, 604800))
}

func scheduledRunTimeoutSeconds(expiration string, componentKind string, orphanTimeout int64) *int64 {
	if strings.EqualFold(strings.TrimSpace(componentKind), "workflow") {
		return nil
	}
	if seconds := schedulerParseExpiration(expiration); seconds != nil {
		return seconds
	}
	if orphanTimeout <= 0 {
		return nil
	}
	value := orphanTimeout
	return &value
}

func (m *goSchedulerManager) reconcileScheduledTerminalActivities(ctx context.Context, now int64) error {
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	_, err = conn.ExecContext(ctx, `
		UPDATE engine.activity_history AS h
		   SET status=r.status,
		       stderr=CASE
		           WHEN COALESCE(NULLIF(h.stderr, ''), '') = '' THEN COALESCE(NULLIF(r.error, ''), h.stderr, '')
		           ELSE h.stderr
		       END,
		       updated_at=$1,
		       finished_at=COALESCE(h.finished_at, r.finished_ts, $2)
		  FROM engine.scheduled_job_run_activity AS s
		  JOIN engine.scheduled_job_runs AS r ON r.id=s.run_id
		 WHERE h.id=s.activity_id
		   AND LOWER(COALESCE(h.status, '')) IN ('queued','running','pending','created','started','in_progress')
		   AND LOWER(COALESCE(r.status, '')) IN ('success','warning','failed','expired','timed out','skipped')
	`, now, now)
	return err
}

func markScheduledRunTerminalTx(ctx context.Context, conn *sql.Conn, runID int64, status string, now int64, message string) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackQuietly(tx)
	if _, err := tx.ExecContext(ctx, `
		UPDATE engine.scheduled_job_runs
		   SET status=$1, finished_ts=$2, updated_at=$3, error=COALESCE(NULLIF(error, ''), $4)
		 WHERE id=$5
	`, status, now, now, truncateString(message, 512), runID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE engine.scheduled_job_run_targets
		   SET resolution_status=CASE
		           WHEN COALESCE(NULLIF(resolution_status, ''), '') = '' THEN $1
		           ELSE resolution_status
		       END,
		       resolution_reason=CASE
		           WHEN COALESCE(NULLIF(resolution_reason, ''), '') = '' THEN $2
		           ELSE resolution_reason
		       END
		 WHERE run_id=$3
	`, schedulerResolutionUnresolved, truncateString(message, 512), runID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE engine.activity_history AS h
		   SET status=$1,
		       stderr=CASE
		           WHEN COALESCE(NULLIF(h.stderr, ''), '') = '' THEN $2
		           ELSE h.stderr
		       END,
		       updated_at=$3,
		       finished_at=COALESCE(h.finished_at, $4)
		  FROM engine.scheduled_job_run_activity AS s
		 WHERE s.activity_id=h.id
		   AND s.run_id=$5
		   AND LOWER(COALESCE(h.status, '')) IN ('queued','running','pending','created','started','in_progress')
	`, status, truncateString(message, 2000), now, now, runID); err != nil {
		return err
	}
	return tx.Commit()
}

func (m *goSchedulerManager) purgeOldScheduledRuns(ctx context.Context, now int64) error {
	retentionDays := envInt("BOREALIS_JOB_HISTORY_DAYS", defaultScheduledRunHistoryDays, 1, 3650)
	cutoff := now - int64(retentionDays*86400)
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, `SELECT id FROM engine.scheduled_job_runs WHERE COALESCE(finished_ts, started_ts, scheduled_ts, 0) < $1`, cutoff)
	if err != nil {
		return err
	}
	ids := []int64{}
	for rows.Next() {
		var id sql.NullInt64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		if id.Valid {
			ids = append(ids, id.Int64)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackQuietly(tx)
	for _, statement := range []string{
		`DELETE FROM engine.scheduled_job_run_activity WHERE run_id = ANY($1)`,
		`DELETE FROM engine.scheduled_job_run_targets WHERE run_id = ANY($1)`,
		`DELETE FROM engine.scheduled_job_onboarding_target_events WHERE run_id = ANY($1)`,
		`DELETE FROM engine.scheduled_job_onboarding_targets WHERE run_id = ANY($1)`,
		`DELETE FROM engine.scheduled_job_runs WHERE id = ANY($1)`,
	} {
		if _, err := tx.ExecContext(ctx, statement, pq.Array(ids)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (m *goSchedulerManager) siteTargetRowsForRun(ctx context.Context, runID int64) map[int64][]int64 {
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return map[int64][]int64{0: {}}
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, `SELECT id, site_id FROM engine.scheduled_job_run_targets WHERE run_id=$1 ORDER BY id ASC`, runID)
	if err != nil {
		return map[int64][]int64{0: {}}
	}
	defer rows.Close()
	grouped := map[int64][]int64{}
	for rows.Next() {
		var id, siteID sql.NullInt64
		if err := rows.Scan(&id, &siteID); err != nil {
			continue
		}
		key := int64(0)
		if siteID.Valid && siteID.Int64 > 0 {
			key = siteID.Int64
		}
		if id.Valid && id.Int64 > 0 {
			grouped[key] = append(grouped[key], id.Int64)
		} else if _, ok := grouped[key]; !ok {
			grouped[key] = []int64{}
		}
	}
	if len(grouped) == 0 {
		grouped[0] = []int64{}
	}
	return grouped
}

func (m *goSchedulerManager) markScheduledRunExpired(ctx context.Context, runID, now int64, errorText string) error {
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	_, err = conn.ExecContext(ctx, `UPDATE engine.scheduled_job_runs SET status=$1, finished_ts=$2, updated_at=$3, error=$4 WHERE id=$5`, scheduledStatusExpired, now, now, truncateString(errorText, 512), runID)
	return err
}

func (m *goSchedulerManager) markScheduledRunFailed(ctx context.Context, runID, now int64, errorText string) error {
	if runID <= 0 {
		return nil
	}
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	_, err = conn.ExecContext(ctx, `UPDATE engine.scheduled_job_runs SET status=$1, finished_ts=$2, updated_at=$3, error=$4 WHERE id=$5`, scheduledStatusFailed, now, now, truncateString(errorText, 512), runID)
	return err
}

func (m *goSchedulerManager) internalJSON(ctx context.Context, method string, path string, body map[string]any, timeout time.Duration) (map[string]any, error) {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(requestCtx, method, m.apiBase+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set(internalTokenHeader, goInternalToken(m.secret))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var payload map[string]any
	_ = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&payload)
	if payload == nil {
		payload = map[string]any{}
	}
	if resp.StatusCode >= 400 {
		return payload, fmt.Errorf("internal API %s returned %d: %s", path, resp.StatusCode, firstText(cleanText(payload["error"]), cleanText(payload["message"])))
	}
	return payload, nil
}

func schedulerComputeNextRun(scheduleType string, startTS *int64, lastRunTS *int64, nowTS int64) *int64 {
	st := strings.ToLower(strings.TrimSpace(scheduleType))
	if st == "" {
		st = "immediately"
	}
	var start *int64
	if startTS != nil && *startTS > 0 {
		value := schedulerFloorMinute(*startTS)
		start = &value
	}
	var last *int64
	if lastRunTS != nil && *lastRunTS > 0 {
		value := schedulerFloorMinute(*lastRunTS)
		last = &value
	}
	nowTS = schedulerFloorMinute(nowTS)
	if st == "immediately" {
		if last != nil {
			return nil
		}
		return &nowTS
	}
	if st == "once" {
		if start == nil || last != nil {
			return nil
		}
		return start
	}
	if start == nil {
		return nil
	}
	periods := map[string]int64{
		"every_5_minutes":  5 * 60,
		"every_10_minutes": 10 * 60,
		"every_15_minutes": 15 * 60,
		"every_30_minutes": 30 * 60,
		"every_hour":       60 * 60,
	}
	if period, ok := periods[st]; ok {
		if last == nil {
			return start
		}
		value := *last + period
		return &value
	}
	if last == nil {
		return start
	}
	switch st {
	case "daily":
		value := schedulerAddDays(*last, 1)
		return &value
	case "weekly":
		value := schedulerAddDays(*last, 7)
		return &value
	case "monthly":
		value := schedulerAddMonths(*last, 1)
		return &value
	case "yearly":
		value := schedulerAddYears(*last, 1)
		return &value
	default:
		return nil
	}
}

func engineScheduleLocation() *time.Location {
	timezoneID := strings.TrimSpace(currentTimezoneID())
	if timezoneID != "" {
		if loc, err := time.LoadLocation(timezoneID); err == nil {
			return loc
		}
	}
	if time.Local != nil {
		return time.Local
	}
	return time.UTC
}

func schedulerAddDays(ts int64, days int) int64 {
	loc := engineScheduleLocation()
	base := time.Unix(ts, 0).In(loc)
	return base.AddDate(0, 0, days).Unix()
}

func schedulerAddMonths(ts int64, months int) int64 {
	loc := engineScheduleLocation()
	base := time.Unix(ts, 0).In(loc)
	year, month, day := base.Date()
	hour, min, sec := base.Clock()
	targetMonth := int(month) + months
	year += (targetMonth - 1) / 12
	targetMonth = ((targetMonth - 1) % 12) + 1
	lastDay := daysInMonth(year, time.Month(targetMonth), loc)
	if day > lastDay {
		day = lastDay
	}
	return time.Date(year, time.Month(targetMonth), day, hour, min, sec, 0, loc).Unix()
}

func schedulerAddYears(ts int64, years int) int64 {
	loc := engineScheduleLocation()
	base := time.Unix(ts, 0).In(loc)
	year, month, day := base.Date()
	hour, min, sec := base.Clock()
	year += years
	if month == time.February && day == 29 && daysInMonth(year, month, loc) < day {
		day = 28
	}
	return time.Date(year, month, day, hour, min, sec, 0, loc).Unix()
}

func daysInMonth(year int, month time.Month, loc *time.Location) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, loc).Day()
}

func schedulerFloorMinute(ts int64) int64 {
	if ts <= 0 {
		return 0
	}
	return ts - (ts % 60)
}

func schedulerParseExpiration(value string) *int64 {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" || value == "no_expire" {
		return nil
	}
	unit := value[len(value)-1:]
	numberText := strings.TrimSpace(value[:len(value)-1])
	multiplier := int64(60)
	switch unit {
	case "m":
		multiplier = 60
	case "h":
		multiplier = 3600
	case "d":
		multiplier = 86400
	default:
		numberText = value
		multiplier = 60
	}
	parsed, err := strconv.ParseInt(numberText, 10, 64)
	if err != nil || parsed <= 0 {
		return nil
	}
	result := parsed * multiplier
	return &result
}

func scheduledTerminalStatus(status string) bool {
	return stringInSet(status, scheduledStatusSuccess, scheduledStatusWarning, scheduledStatusFailed, scheduledStatusExpired, scheduledStatusTimedOut, scheduledStatusSkipped)
}

func durationForOperator(seconds int64) string {
	if seconds <= 0 {
		return "0s"
	}
	if seconds%86400 == 0 {
		return strconv.FormatInt(seconds/86400, 10) + "d"
	}
	if seconds%3600 == 0 {
		return strconv.FormatInt(seconds/3600, 10) + "h"
	}
	if seconds%60 == 0 {
		return strconv.FormatInt(seconds/60, 10) + "m"
	}
	return strconv.FormatInt(seconds, 10) + "s"
}

func schedulerOnboardingSiteID(targets []any) int64 {
	for _, raw := range targets {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		kind := strings.ToLower(cleanText(firstNonEmptyAny(entry["kind"], entry["type"])))
		if kind != "onboarding_scope" {
			continue
		}
		if siteID := coerceInt64(entry["site_id"]); siteID > 0 {
			return siteID
		}
	}
	return 0
}

func schedulerTaskLink(jobID, runID int64, kind string) map[string]any {
	label := "Scheduled Job " + strconv.FormatInt(jobID, 10)
	path := fmt.Sprintf("/jobs/%d?tab=job_history", jobID)
	if kind == "workflow" {
		label = "Workflow Job " + strconv.FormatInt(jobID, 10)
	}
	return map[string]any{"kind": kind, "label": label, "job_id": jobID, "run_id": runID, "path": path}
}

func mapsToAnyList(values []map[string]any) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func int64sToAnyList(values []int64) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func firstPositiveInt64(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func schedulerNullInt64Ptr(value sql.NullInt64) *int64 {
	if value.Valid {
		return &value.Int64
	}
	return nil
}

func nullableInt64Any(value sql.NullInt64) any {
	if value.Valid {
		return value.Int64
	}
	return nil
}

func schedulerJSONMap(value sql.NullString) map[string]any {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return map[string]any{}
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(value.String), &payload); err != nil || payload == nil {
		return map[string]any{}
	}
	return payload
}

func mustJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

func truncateString(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func schedulerDockerBin() string {
	if configured := strings.TrimSpace(os.Getenv("BOREALIS_DOCKER_BIN")); configured != "" {
		return configured
	}
	if path, err := exec.LookPath("docker"); err == nil {
		return path
	}
	return ""
}

func schedulerComposeEnvFile(projectRoot string) string {
	if configured := strings.TrimSpace(os.Getenv("BOREALIS_RUNTIME_ENV_FILE")); configured != "" {
		return configured
	}
	return filepath.Join(projectRoot, "Engine", "Deploy", "compose.env")
}

func schedulerDesiredSiteWorkerImage() string {
	return envDefault("BOREALIS_SITE_WORKER_IMAGE", "borealis-engine/site-worker:local")
}

func schedulerServiceActionHelperImage() string {
	return envDefault("BOREALIS_JOB_SCHEDULER_IMAGE", "borealis-engine/job-scheduler:local")
}

func schedulerSiteWorkerSocketIOAsyncMode() string {
	return envDefault("BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE", "eventlet")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func schedulerBuildRoute(workerGUID, containerName string, siteID int64, upstreamPort int64, metadata map[string]any) schedulerRoute {
	segment := schedulerSafeSegment(workerGUID)
	routeDir := strings.TrimSpace(os.Getenv("BOREALIS_TRAEFIK_DYNAMIC_CONFIG_DIR"))
	if routeDir == "" {
		routeDir = filepath.Join(envDefault("BOREALIS_PROJECT_ROOT", "/opt/Borealis"), "Engine", "Services", "traefik-edge", "config", "dynamic")
	}
	routeMetadata := copyMap(metadata)
	routeMetadata["lifecycle_owner"] = "job-scheduler"
	routeMetadata["route_kind"] = "site_worker"
	routeMetadata["worker_guid"] = workerGUID
	return schedulerRoute{
		WorkerGUID:     workerGUID,
		SiteID:         siteID,
		ContainerName:  firstText(containerName, "site-worker-"+workerGUID),
		RouteName:      "borealis-site-worker-" + segment,
		RoutePath:      schedulerDefaultRouteRoot + "/" + segment,
		RouteFilePath:  filepath.Join(routeDir, "site-worker-"+segment+".yml"),
		UpstreamScheme: "http",
		UpstreamHost:   "127.0.0.1",
		UpstreamPort:   upstreamPort,
		Status:         schedulerRouteStatusActive,
		Metadata:       routeMetadata,
	}
}

func schedulerWorkerRouteMetadata(workerGUID string, remoteOpsPort, remoteDesktopPort int64) map[string]any {
	return map[string]any{
		"remote_ops_socket": map[string]any{
			"host":        "127.0.0.1",
			"path":        "/socket.io/",
			"port":        remoteOpsPort,
			"worker_guid": workerGUID,
		},
		"remote_desktop_guacamole": map[string]any{
			"host":        "127.0.0.1",
			"scheme":      "http",
			"path":        "/remote-desktop/vnc/guacamole",
			"path_prefix": "/remote-desktop/vnc",
			"port":        remoteDesktopPort,
			"worker_guid": workerGUID,
		},
	}
}

func schedulerWriteRouteFile(route schedulerRoute) error {
	if route.UpstreamPort <= 0 || route.RouteFilePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(route.RouteFilePath), 0o755); err != nil {
		return err
	}
	content := schedulerRouteYAML(route)
	tmp := filepath.Join(filepath.Dir(route.RouteFilePath), "."+filepath.Base(route.RouteFilePath)+".tmp")
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, route.RouteFilePath)
}

func schedulerRouteYAML(route schedulerRoute) string {
	hostname := firstText(strings.TrimSpace(os.Getenv("BOREALIS_PUBLIC_HOSTNAME")), "localhost")
	hostRule := schedulerPublicHostRule(hostname)
	stripName := route.RouteName + "-strip"
	upstreamURL := fmt.Sprintf("%s://%s:%d", route.UpstreamScheme, route.UpstreamHost, route.UpstreamPort)
	tlsLines := schedulerRouteTLSLines(hostname)
	guacamole := schedulerAnyMap(route.Metadata["remote_desktop_guacamole"])
	guacamolePort := coerceInt64(guacamole["port"])
	guacamolePathPrefix := firstText(cleanText(guacamole["path_prefix"]), "/remote-desktop/vnc")
	guacamoleEnabled := guacamolePort > 0 && guacamolePathPrefix != ""
	guacamoleRouteName := route.RouteName + "-remote-desktop"
	guacamoleRoutePath := strings.TrimRight(route.RoutePath, "/") + guacamolePathPrefix
	guacamoleURL := ""
	if guacamoleEnabled {
		guacamoleScheme := firstText(cleanText(guacamole["scheme"]), route.UpstreamScheme)
		guacamoleHost := firstText(cleanText(guacamole["host"]), route.UpstreamHost)
		guacamoleURL = fmt.Sprintf("%s://%s:%d", guacamoleScheme, guacamoleHost, guacamolePort)
	}
	lines := []string{
		"http:",
		"  middlewares:",
		"    " + stripName + ":",
		"      stripPrefix:",
		"        prefixes:",
		"          - " + quoteYAML(route.RoutePath),
	}
	lines = append(lines,
		"  routers:",
		"    "+route.RouteName+":",
		"      entryPoints:",
		"        - websecure",
		fmt.Sprintf("      rule: \"%s && PathPrefix(`%s`)\"", hostRule, route.RoutePath),
		"      middlewares:",
		"        - "+stripName,
		"      service: "+route.RouteName,
		"      priority: 120",
	)
	lines = append(lines, tlsLines...)
	if guacamoleEnabled {
		lines = append(lines,
			"    "+guacamoleRouteName+":",
			"      entryPoints:",
			"        - websecure",
			fmt.Sprintf("      rule: \"%s && PathPrefix(`%s`)\"", hostRule, guacamoleRoutePath),
			"      middlewares:",
			"        - "+stripName,
			"      service: "+guacamoleRouteName,
			"      priority: 130",
		)
		lines = append(lines, tlsLines...)
	}
	lines = append(lines,
		"  services:",
		"    "+route.RouteName+":",
		"      loadBalancer:",
		"        servers:",
		"          - url: "+quoteYAML(upstreamURL),
	)
	if guacamoleEnabled {
		lines = append(lines,
			"    "+guacamoleRouteName+":",
			"      loadBalancer:",
			"        servers:",
			"          - url: "+quoteYAML(guacamoleURL),
		)
	}
	lines = append(lines, "")
	return strings.Join(lines, "\n")
}

func schedulerPublicHostRule(primary string) string {
	rawHosts := []string{primary}
	if aliases := strings.TrimSpace(os.Getenv("BOREALIS_PUBLIC_HOSTNAME_ALIASES")); aliases != "" {
		rawHosts = append(rawHosts, strings.Split(strings.ReplaceAll(aliases, "\n", ","), ",")...)
	}
	seen := map[string]bool{}
	hosts := []string{}
	for _, raw := range rawHosts {
		host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
		if host == "" || seen[host] || strings.ContainsAny(host, "`\"' \t\r\n") {
			continue
		}
		seen[host] = true
		hosts = append(hosts, host)
	}
	if len(hosts) == 0 {
		hosts = []string{"localhost"}
	}
	parts := make([]string, 0, len(hosts))
	for _, host := range hosts {
		parts = append(parts, "`"+host+"`")
	}
	return "Host(" + strings.Join(parts, ",") + ")"
}

func schedulerRouteTLSLines(hostname string) []string {
	acmeEmail := strings.TrimSpace(os.Getenv("BOREALIS_ACME_EMAIL"))
	if acmeEmail != "" && strings.TrimSpace(hostname) != "" && strings.TrimSpace(hostname) != "localhost" {
		return []string{"      tls:", "        certResolver: letsencrypt"}
	}
	return []string{"      tls: {}"}
}

func quoteYAML(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}

func schedulerSafeSegment(value string) string {
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-_")
	if out == "" {
		return "worker"
	}
	return out
}

func schedulerWorkerPort(workerGUID string, siteID int64, baseEnv, rangeEnv string, defaultBase, defaultRange int64) int64 {
	base := int64(envInt(baseEnv, int(defaultBase), 1024, 65000))
	portRange := int64(envInt(rangeEnv, int(defaultRange), 1, int(65535-base)))
	hash := fnv32(fmt.Sprintf("%s:%d", workerGUID, siteID))
	return base + int64(hash%uint32(portRange))
}

func fnv32(value string) uint32 {
	var hash uint32 = 2166136261
	for i := 0; i < len(value); i++ {
		hash ^= uint32(value[i])
		hash *= 16777619
	}
	return hash
}

func schedulerDockerSiteWorkerSnapshots(ctx context.Context) ([]map[string]any, error) {
	dockerBin := schedulerDockerBin()
	if dockerBin == "" {
		return nil, errors.New("docker CLI unavailable")
	}
	out, err := exec.CommandContext(ctx, dockerBin, "ps", "--filter", "label=borealis.role=site-worker", "--format", "{{.ID}}").Output()
	if err != nil {
		return nil, err
	}
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		return []map[string]any{}, nil
	}
	args := append([]string{"inspect"}, ids...)
	out, err = exec.CommandContext(ctx, dockerBin, args...).Output()
	if err != nil {
		return nil, err
	}
	var inspected []map[string]any
	if err := json.Unmarshal(out, &inspected); err != nil {
		return nil, err
	}
	snapshots := []map[string]any{}
	for _, item := range inspected {
		config := nestedMap(item, "Config")
		labels := mapStringAny(config["Labels"])
		envItems, _ := config["Env"].([]any)
		envLookup := envListLookup(envItems)
		workerGUID := firstText(cleanText(labels["borealis.worker_guid"]), envLookup["BOREALIS_SITE_WORKER_GUID"])
		siteID := coerceInt64(firstNonEmptyAny(labels["borealis.site_id"], envLookup["BOREALIS_SITE_WORKER_SITE_ID"]))
		if workerGUID == "" || siteID <= 0 {
			continue
		}
		state := nestedMap(item, "State")
		name := strings.TrimPrefix(cleanText(item["Name"]), "/")
		image := cleanText(config["Image"])
		snapshots = append(snapshots, map[string]any{
			"worker_guid":         workerGUID,
			"site_id":             siteID,
			"container_name":      firstText(name, "site-worker-"+workerGUID),
			"image":               image,
			"configured_image":    firstText(cleanText(labels["borealis.site_worker_image"]), envLookup["BOREALIS_SITE_WORKER_IMAGE"], image),
			"created_at":          schedulerDockerCreatedAt(cleanText(item["Created"])),
			"docker_state":        firstText(cleanText(state["Status"]), "running"),
			"exit_code":           coerceInt64(state["ExitCode"]),
			"remote_ops_port":     coerceInt64(firstNonEmptyAny(labels["borealis.remote_ops_port"], envLookup["BOREALIS_SITE_WORKER_REMOTE_OPS_PORT"])),
			"remote_desktop_port": coerceInt64(firstNonEmptyAny(labels["borealis.remote_desktop_port"], envLookup["BOREALIS_SITE_WORKER_REMOTE_DESKTOP_PORT"])),
		})
	}
	return snapshots, nil
}

func schedulerSiteWorkerImageMatches(snapshot map[string]any, desiredImage string) bool {
	configured := cleanText(snapshot["configured_image"])
	if configured != "" {
		return configured == desiredImage
	}
	image := cleanText(snapshot["image"])
	return image != "" && image == desiredImage
}

func schedulerDockerCreatedAt(value string) int64 {
	if value == "" {
		return 0
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.Unix()
	}
	return 0
}

func schedulerStopContainer(ctx context.Context, containerName string) error {
	containerName = strings.TrimSpace(containerName)
	if containerName == "" {
		return nil
	}
	dockerBin := schedulerDockerBin()
	if dockerBin == "" {
		return errors.New("docker CLI unavailable")
	}
	out, err := exec.CommandContext(ctx, dockerBin, "stop", containerName).CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if strings.Contains(text, "No such") {
			return nil
		}
		return fmt.Errorf("%s", text)
	}
	return nil
}

func schedulerRouteFilesForWorkers(ctx context.Context, conn *sql.Conn, workerGUIDs []string) ([]string, error) {
	if len(workerGUIDs) == 0 {
		return nil, nil
	}
	rows, err := conn.QueryContext(ctx, `
		SELECT route_file_path
		  FROM engine.job_scheduler_worker_routes
		 WHERE status=$1
		   AND worker_guid = ANY($2)
	`, schedulerRouteStatusActive, pq.Array(workerGUIDs))
	if err != nil {
		return nil, err
	}
	return schedulerCollectRouteFilePaths(rows)
}

func schedulerRouteFilesForMissingWorkers(ctx context.Context, conn *sql.Conn, live []string) ([]string, error) {
	var rows *sql.Rows
	var err error
	if len(live) == 0 {
		rows, err = conn.QueryContext(ctx, `
			SELECT route_file_path
			  FROM engine.job_scheduler_worker_routes
			 WHERE status=$1
			   AND worker_guid IN (
			       SELECT worker_guid
			         FROM engine.job_scheduler_workers
			        WHERE site_id > 0
			          AND status IN ($2,$3,$4)
			   )
		`, schedulerRouteStatusActive, schedulerWorkerStatusStarting, schedulerWorkerStatusRunning, schedulerWorkerStatusIdle)
	} else {
		rows, err = conn.QueryContext(ctx, `
			SELECT route_file_path
			  FROM engine.job_scheduler_worker_routes
			 WHERE status=$1
			   AND worker_guid IN (
			       SELECT worker_guid
			         FROM engine.job_scheduler_workers
			        WHERE site_id > 0
			          AND status IN ($2,$3,$4)
			          AND NOT (worker_guid = ANY($5))
			   )
		`, schedulerRouteStatusActive, schedulerWorkerStatusStarting, schedulerWorkerStatusRunning, schedulerWorkerStatusIdle, pq.Array(live))
	}
	if err != nil {
		return nil, err
	}
	return schedulerCollectRouteFilePaths(rows)
}

func schedulerRouteFilesForStaleWorkers(ctx context.Context, conn *sql.Conn, staleBefore int64) ([]string, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT route_file_path
		  FROM engine.job_scheduler_worker_routes
		 WHERE status=$1
		   AND worker_guid IN (
		       SELECT worker_guid
		         FROM engine.job_scheduler_workers
		        WHERE site_id > 0
		          AND status IN ($2,$3,$4)
		          AND last_seen_at < $5
		   )
	`, schedulerRouteStatusActive, schedulerWorkerStatusStarting, schedulerWorkerStatusRunning, schedulerWorkerStatusIdle, staleBefore)
	if err != nil {
		return nil, err
	}
	return schedulerCollectRouteFilePaths(rows)
}

func schedulerCollectRouteFilePaths(rows *sql.Rows) ([]string, error) {
	defer rows.Close()
	paths := []string{}
	for rows.Next() {
		var path sql.NullString
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		if path.Valid {
			paths = append(paths, path.String)
		}
	}
	return paths, rows.Err()
}

func schedulerRemoveRouteFiles(paths []string) {
	seen := map[string]struct{}{}
	for _, rawPath := range paths {
		path := strings.TrimSpace(rawPath)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		if !strings.HasPrefix(filepath.Base(path), "site-worker-") {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("failed to remove retired site-worker route file %s: %v", path, err)
		}
	}
}

func envListLookup(values []any) map[string]string {
	out := map[string]string{}
	for _, raw := range values {
		text := cleanText(raw)
		if key, value, ok := strings.Cut(text, "="); ok {
			out[key] = value
		}
	}
	return out
}

func parseDockerComposePS(raw []byte) []map[string]any {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return nil
	}
	var list []map[string]any
	if err := json.Unmarshal([]byte(text), &list); err == nil {
		return list
	}
	var single map[string]any
	if err := json.Unmarshal([]byte(text), &single); err == nil {
		return []map[string]any{single}
	}
	out := []map[string]any{}
	for _, line := range strings.Split(text, "\n") {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err == nil && row != nil {
			out = append(out, row)
		}
	}
	return out
}

func randomShortID() string {
	id := randomUUID()
	if len(id) >= 8 {
		return strings.ReplaceAll(id[:8], "-", "")
	}
	return strconv.FormatInt(time.Now().UnixNano(), 16)
}

func randomUUID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}

func shellJoin(values []string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, shellQuote(value))
	}
	return strings.Join(parts, " ")
}

func nestedMap(value map[string]any, key string) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return mapStringAny(value[key])
}

func coerceInt64Ptr(value any) *int64 {
	if value == nil {
		return nil
	}
	parsed := coerceInt64(value)
	return &parsed
}
