package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

func (m *goSchedulerManager) processPatchInstallWork(ctx context.Context) error {
	leaseSeconds := patchInstallWorkLeaseSeconds()
	for i := 0; i < envInt("BOREALIS_PATCH_INSTALL_MANAGER_BATCH", 2, 1, 16); i++ {
		item, err := m.claimNextKindWorkItem(ctx, []string{schedulerKindPatchInstallRun}, "job-scheduler", leaseSeconds)
		if err != nil || item == nil {
			return err
		}
		claimed := *item
		go func() {
			status := workStatusSucceeded
			errorText := ""
			if err := m.runPatchInstallWorkItem(context.Background(), claimed); err != nil {
				status = workStatusFailed
				errorText = err.Error()
			}
			if err := m.completeWorkItem(context.Background(), claimed.ID, status, errorText); err != nil {
				logSchedulerPatchInstall("failed to complete patch install work item id=%d: %v", claimed.ID, err)
			}
		}()
	}
	return nil
}

func (m *goSchedulerManager) runPatchInstallWorkItem(ctx context.Context, item schedulerWorkItem) error {
	payload := item.Payload
	runID := firstPositiveInt64(coerceInt64(payload["run_id"]), nullInt(item.RunID))
	jobID := firstPositiveInt64(coerceInt64(payload["job_id"]), nullInt(item.JobID))
	if runID <= 0 || jobID <= 0 {
		return errors.New("patch install work item payload incomplete")
	}
	hostname := cleanText(payload["hostname"])
	if hostname == "" {
		var err error
		hostname, err = m.scheduledRunHostname(ctx, runID)
		if err != nil {
			return err
		}
	}
	component := schedulerAnyMap(payload["patch_component"])
	if hostname == "" || component == nil {
		err := errors.New("patch install work item target or component missing")
		_ = m.failScheduledRun(ctx, runID, err.Error())
		return err
	}
	patch := scheduledPatchInstallSpec(component)
	if cleanText(patch["patch_key"]) == "" && cleanText(patch["kb"]) == "" && cleanText(patch["title"]) == "" {
		err := errors.New("patch install work item patch identity missing")
		_ = m.failScheduledRun(ctx, runID, err.Error())
		return err
	}
	if err := m.markScheduledRunRunning(ctx, runID); err != nil {
		return err
	}
	componentName := scheduledPatchInstallDisplayName(component)
	metadata := scheduledActivityMetadata(jobID, runID, coerceInt64(payload["scheduled_ts"]), patchInstallComponentKind, componentName, "")
	metadata["patch"] = patch
	metadata["patch_trigger"] = firstText(cleanText(component["trigger"]), "ad_hoc")
	activityID, err := m.insertScheduledActivity(ctx, scheduledActivityInsert{
		RunID:         runID,
		Hostname:      hostname,
		ScriptPath:    "Internal/Patch_Management",
		ScriptName:    componentName,
		ScriptType:    patchInstallComponentKind,
		Status:        scheduledStatusRunning,
		ComponentKind: patchInstallComponentKind,
		Metadata:      metadata,
	})
	if err != nil {
		_ = m.failScheduledRun(ctx, runID, err.Error())
		return err
	}
	snapshot, status, err := m.store.loadDeviceProcessContext(ctx, operatorProfile{Username: "job-scheduler", Role: "Admin"}, hostname)
	if err != nil || snapshot.Route == nil {
		message := "No active site-worker route is available for host " + hostname + "."
		if err != nil && status != http.StatusNotFound {
			message = err.Error()
		}
		_ = m.updateScheduledPatchRunStatus(ctx, runID, activityID, scheduledStatusFailed, "", message, message)
		return errors.New(message)
	}
	requestedAt := time.Now().Unix()
	requestID := fmt.Sprintf("patch-job-%d-run-%d", jobID, runID)
	callTimeout := patchInstallAgentCallTimeout()
	eventPayload := map[string]any{
		"hostname":             firstText(snapshot.Hostname, hostname),
		"agent_id":             snapshot.AgentID,
		"requested_at":         requestedAt,
		"requested_by":         "job-scheduler",
		"request_id":           requestID,
		"scope":                "scheduled_job",
		"scheduled_job_id":     jobID,
		"scheduled_job_run_id": runID,
		"wait_for_completion":  true,
		"patch":                patch,
	}
	waitText := fmt.Sprintf("Job scheduler requesting patch install request_id=%s host=%s patch=%s\n", requestID, hostname, componentName)
	if err := m.updateScheduledPatchRunStatus(ctx, runID, activityID, scheduledStatusRunning, waitText, "", ""); err != nil {
		return err
	}
	response, workerState, workerErr := m.callSiteWorkerHostService(ctx, snapshot.Route, map[string]any{
		"hostname":        firstText(snapshot.Hostname, hostname),
		"service_mode":    "system",
		"event_name":      "patch_install_request",
		"timeout_seconds": int64(callTimeout / time.Second),
		"payload":         eventPayload,
	}, callTimeout+10*time.Second)
	if workerErr != nil {
		errorText := schedulerPatchInstallWorkerError(hostname, workerState, workerErr)
		_ = m.updateScheduledPatchRunStatus(ctx, runID, activityID, scheduledStatusFailed, "", errorText, errorText)
		return errors.New(errorText)
	}
	stdout, stderr, errorText := schedulerPatchInstallOutput(requestID, hostname, componentName, patch, response)
	if !schedulerPatchInstallResponseOK(response) {
		if errorText == "" {
			errorText = "Patch install failed."
		}
		_ = m.updateScheduledPatchRunStatus(ctx, runID, activityID, scheduledStatusFailed, stdout, stderr, errorText)
		return errors.New(errorText)
	}
	return m.updateScheduledPatchRunStatus(ctx, runID, activityID, scheduledStatusSuccess, stdout, stderr, "")
}

func patchInstallWorkLeaseSeconds() int64 {
	return int64(envInt("BOREALIS_PATCH_INSTALL_MANAGER_LEASE_SECONDS", 7200, 300, 86400))
}

func patchInstallAgentCallTimeout() time.Duration {
	return time.Duration(envInt("BOREALIS_PATCH_INSTALL_AGENT_TIMEOUT_SECONDS", 5700, 60, 86400)) * time.Second
}

func schedulerPatchInstallResponseOK(response map[string]any) bool {
	if _, exists := response["ok"]; exists {
		return boolFromAny(response["ok"])
	}
	status := strings.ToLower(cleanText(response["status"]))
	return stringInSet(status, "completed", "success", "installed", "succeeded")
}

func schedulerPatchInstallOutput(requestID, hostname, componentName string, patch map[string]any, response map[string]any) (string, string, string) {
	result := schedulerAnyMap(response["result"])
	lines := []string{
		fmt.Sprintf("Patch install request_id=%s host=%s patch=%s", requestID, hostname, componentName),
	}
	if status := cleanText(response["status"]); status != "" {
		lines = append(lines, "Agent response status="+status)
	}
	if code := firstText(cleanText(response["result_code"]), cleanText(result["result_code"])); code != "" {
		lines = append(lines, "WUA result_code="+code)
	}
	if reboot := firstText(cleanText(response["reboot_required"]), cleanText(result["reboot_required"])); reboot != "" {
		lines = append(lines, "Reboot required="+reboot)
	}
	if count := firstText(cleanText(response["installed_count"]), cleanText(result["installed_count"])); count != "" {
		lines = append(lines, "Installed update count="+count)
	}
	if raw, err := json.MarshalIndent(map[string]any{"patch": patch, "agent_response": response}, "", "  "); err == nil {
		lines = append(lines, string(raw))
	}
	if stdout := cleanText(response["stdout"]); stdout != "" {
		lines = append(lines, "PowerShell stdout:\n"+stdout)
	}
	stderr := cleanText(response["stderr"])
	errorText := firstText(
		cleanText(response["message"]),
		cleanText(response["error"]),
		cleanText(result["message"]),
		cleanText(result["error"]),
		stderr,
	)
	return strings.Join(lines, "\n") + "\n", stderr, errorText
}

func schedulerPatchInstallWorkerError(hostname string, state string, err error) string {
	detail := ""
	if err != nil {
		detail = strings.TrimSpace(err.Error())
	}
	switch state {
	case "agent_error":
		return fmt.Sprintf("Agent rejected patch install for host %s: %s", hostname, firstText(detail, "Agent rejected the patch install request."))
	case "no_response":
		return fmt.Sprintf("Agent did not complete patch install for host %s before timeout.", hostname)
	case "site_worker_unavailable":
		return fmt.Sprintf("No active site-worker route is available for host %s.", hostname)
	default:
		return fmt.Sprintf("Job scheduler could not dispatch patch install for host %s: %s", hostname, firstText(detail, state))
	}
}

func (m *goSchedulerManager) updateScheduledPatchRunStatus(ctx context.Context, runID int64, activityID int64, status string, stdout string, stderr string, errorText string) error {
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
	if scheduledTerminalStatus(status) {
		finished = now
	}
	targetStatus := schedulerResolutionEligible
	targetReason := ""
	if status == scheduledStatusFailed || status == scheduledStatusTimedOut || status == scheduledStatusExpired {
		targetStatus = schedulerResolutionUnresolved
		targetReason = truncateString(firstText(errorText, stderr), 512)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackQuietly(tx)
	if _, err := tx.ExecContext(ctx, `
		UPDATE engine.scheduled_job_runs
		   SET status=$1,
		       started_ts=COALESCE(started_ts, $2),
		       finished_ts=COALESCE($3, finished_ts),
		       updated_at=$4,
		       error=$5
		 WHERE id=$6
	`, status, now, finished, now, truncateString(errorText, 512), runID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE engine.scheduled_job_run_targets
		   SET resolution_status=$1,
		       resolution_reason=$2,
		       resolved_connection=COALESCE(NULLIF(resolved_connection, ''), $3)
		 WHERE run_id=$4
	`, targetStatus, targetReason, "agent_patch_management", runID); err != nil {
		return err
	}
	if activityID > 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE engine.activity_history
			   SET status=$1,
			       stdout=CASE WHEN $2 = '' THEN stdout ELSE COALESCE(NULLIF(stdout, ''), '') || $2 END,
			       stderr=CASE WHEN $3 = '' THEN stderr ELSE COALESCE(NULLIF(stderr, ''), '') || $3 END,
			       updated_at=$4,
			       finished_at=COALESCE($5, finished_at)
			 WHERE id=$6
		`, status, stdout, stderr, now, finished, activityID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func logSchedulerPatchInstall(format string, args ...any) {
	log.Printf(format, args...)
}
