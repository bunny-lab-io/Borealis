package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

const onboardingGoCutoverRetiredMessage = "Automatic remote onboarding execution was retired from the legacy Python site-worker runtime during the Go api-backend cutover. Use generated Agent install commands until Go-native remote onboarding execution is implemented."

func (m *goSchedulerManager) processOnboardingWork(ctx context.Context) error {
	for i := 0; i < envInt("BOREALIS_ONBOARDING_MANAGER_BATCH", 4, 1, 32); i++ {
		item, err := m.claimNextKindWorkItem(ctx, []string{schedulerKindOnboardingRun})
		if err != nil || item == nil {
			return err
		}
		if err := m.runClaimedWork(ctx, *item, m.runOnboardingWorkItem); err != nil {
			return err
		}
	}
	return nil
}

func (m *goSchedulerManager) runOnboardingWorkItem(ctx context.Context, item schedulerWorkItem) error {
	payload := item.Payload
	runID := firstPositiveInt64(coerceInt64(payload["run_id"]), nullInt(item.RunID))
	jobID := firstPositiveInt64(coerceInt64(payload["job_id"]), nullInt(item.JobID))
	if runID <= 0 || jobID <= 0 {
		return errors.New("onboarding work item payload incomplete")
	}
	if err := m.markScheduledRunRunning(ctx, runID); err != nil {
		return err
	}
	targets := schedulerAnyList(payload["targets"])
	siteID := firstPositiveInt64(coerceInt64(payload["site_id"]), schedulerNullableInt64(item.SiteID), schedulerOnboardingSiteID(targets))
	scheduledTS := coerceInt64(payload["scheduled_ts"])
	return m.retireOnboardingRun(ctx, jobID, runID, scheduledTS, siteID, targets)
}

func (m *goSchedulerManager) retireOnboardingRun(ctx context.Context, jobID int64, runID int64, scheduledTS int64, siteID int64, targets []any) error {
	tx, _, cleanup, err := m.beginOwnedWorkTx(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	now := time.Now().Unix()

	var rowJobID sql.NullInt64
	var rowScheduledTS sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT job_id, scheduled_ts FROM engine.scheduled_job_runs WHERE id=$1`, runID).Scan(&rowJobID, &rowScheduledTS); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if rowJobID.Valid && rowJobID.Int64 > 0 {
		jobID = rowJobID.Int64
	}
	if rowScheduledTS.Valid && rowScheduledTS.Int64 > 0 {
		scheduledTS = rowScheduledTS.Int64
	}
	if scheduledTS <= 0 {
		scheduledTS = now
	}

	targetIDs, err := onboardingTargetIDsForRun(ctx, tx, runID)
	if err != nil {
		return err
	}
	if len(targetIDs) == 0 {
		entries := schedulerOnboardingScopeEntries(targets)
		if len(entries) == 0 {
			entries = []string{"configured onboarding scope"}
		}
		for _, entry := range entries {
			host, port := schedulerOnboardingEntryEndpoint(entry, defaultOnboardingSSHPort)
			var targetID int64
			if err := tx.QueryRowContext(ctx, `
				INSERT INTO engine.scheduled_job_onboarding_targets(
					run_id, job_id, scheduled_ts, site_id, target_input, target_address, target_hostname,
					ssh_port, status, detail, stdout_snippet, stderr_snippet, approval_reference,
					created_at, updated_at, finished_at
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'',$11,'',$12,$13,$14)
				RETURNING id
			`, runID, jobID, scheduledTS, nullablePositiveIntArg(siteID), entry, nullIfEmpty(host), nullIfEmpty(host), port, "failed", onboardingGoCutoverRetiredMessage, onboardingGoCutoverRetiredMessage, now, now, now).Scan(&targetID); err != nil {
				return err
			}
			if targetID > 0 {
				targetIDs = append(targetIDs, targetID)
			}
		}
	} else {
		if _, err := tx.ExecContext(ctx, `
			UPDATE engine.scheduled_job_onboarding_targets
			   SET status='failed',
			       detail=$1,
			       stderr_snippet=$2,
			       updated_at=$3,
			       finished_at=COALESCE(finished_at, $4)
			 WHERE run_id=$5
		`, onboardingGoCutoverRetiredMessage, onboardingGoCutoverRetiredMessage, now, now, runID); err != nil {
			return err
		}
	}
	for _, targetID := range targetIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO engine.scheduled_job_onboarding_target_events(
				target_row_id, run_id, job_id, status, task, detail, stdout_snippet, stderr_snippet,
				started_at, finished_at, created_at, updated_at
			) VALUES ($1,$2,$3,$4,$5,$6,'',$7,$8,$9,$10,$11)
		`, targetID, runID, jobID, "failed", "Go api-backend onboarding cutover", onboardingGoCutoverRetiredMessage, onboardingGoCutoverRetiredMessage, now, now, now, now); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE engine.scheduled_job_runs
		   SET status=$1,
		       finished_ts=$2,
		       updated_at=$3,
		       error=$4
		 WHERE id=$5
	`, scheduledStatusFailed, now, now, onboardingGoCutoverRetiredMessage, runID); err != nil {
		return err
	}
	return tx.Commit()
}

func onboardingTargetIDsForRun(ctx context.Context, tx *sql.Tx, runID int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM engine.scheduled_job_onboarding_targets WHERE run_id=$1 ORDER BY id ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id sql.NullInt64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if id.Valid && id.Int64 > 0 {
			out = append(out, id.Int64)
		}
	}
	return out, rows.Err()
}

func schedulerOnboardingScopeEntries(targets []any) []string {
	seen := map[string]bool{}
	out := []string{}
	addEntry := func(value any) {
		for _, part := range strings.FieldsFunc(cleanText(value), func(r rune) bool {
			return r == '\n' || r == '\r' || r == ',' || r == ';'
		}) {
			entry := strings.TrimSpace(part)
			key := strings.ToLower(entry)
			if entry == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, entry)
		}
	}
	for _, raw := range targets {
		entry := schedulerAnyMap(raw)
		if strings.ToLower(cleanText(firstNonEmptyAny(entry["kind"], entry["type"]))) != "onboarding_scope" {
			continue
		}
		for _, key := range []string{"entries", "scope", "targets", "discovery_scope"} {
			value, ok := entry[key]
			if !ok {
				continue
			}
			switch list := value.(type) {
			case []any:
				for _, item := range list {
					addEntry(item)
				}
			case []string:
				for _, item := range list {
					addEntry(item)
				}
			default:
				addEntry(value)
			}
		}
	}
	return out
}

func schedulerOnboardingEntryEndpoint(entry string, defaultPort int64) (string, int64) {
	text := strings.TrimSpace(entry)
	if defaultPort <= 0 {
		defaultPort = defaultOnboardingSSHPort
	}
	if strings.HasPrefix(text, "[") {
		if host, portText, err := net.SplitHostPort(text); err == nil {
			if port, parseErr := strconv.ParseInt(portText, 10, 64); parseErr == nil && port > 0 && port <= 65535 {
				return strings.Trim(host, "[]"), port
			}
		}
	}
	host := text
	port := defaultPort
	if strings.Count(text, ":") == 1 {
		left, right, _ := strings.Cut(text, ":")
		if parsed, err := strconv.ParseInt(right, 10, 64); err == nil && parsed > 0 && parsed <= 65535 {
			host = left
			port = parsed
		}
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" {
		host = fmt.Sprintf("onboarding-target-%d", time.Now().Unix())
	}
	return host, port
}

func schedulerNullableInt64(value sql.NullInt64) int64 {
	if !value.Valid {
		return 0
	}
	return value.Int64
}
