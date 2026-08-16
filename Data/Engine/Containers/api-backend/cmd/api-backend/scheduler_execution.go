package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
)

const (
	schedulerResolutionEligible                 = "eligible"
	schedulerResolutionSkipped                  = "skipped"
	schedulerResolutionUnresolved               = "unresolved"
	schedulerResolutionEstablishingConnection   = "establishing_connection"
	scheduledConnectionProbeTimeoutSeconds      = int64(60)
	scheduledConnectionProbeRequestGraceSeconds = int64(15)
	scheduledSSHPrivateKeyPath                  = "{{BOREALIS_RUNTIME_DIR}}/auth/id_borealis_ssh"
)

type scheduledExecutionTarget struct {
	ID                 int64
	Hostname           string
	DeviceGUID         string
	SiteID             int64
	SiteName           string
	AgentID            string
	ConnectionEndpoint string
	InventoryHostname  string
}

type scheduledComponentDocument struct {
	Doc          map[string]any
	Record       map[string]any
	RelativePath string
	GUID         string
}

func (m *goSchedulerManager) processScheduledRunWork(ctx context.Context) error {
	leaseSeconds := int64(envInt("BOREALIS_SCHEDULED_RUN_MANAGER_LEASE_SECONDS", 900, 60, 86400))
	for i := 0; i < envInt("BOREALIS_SCHEDULED_RUN_MANAGER_BATCH", 4, 1, 32); i++ {
		item, err := m.claimNextKindWorkItem(ctx, []string{schedulerKindScheduledRun}, "job-scheduler", leaseSeconds)
		if err != nil || item == nil {
			return err
		}
		claimed := *item
		go func() {
			status := workStatusSucceeded
			errorText := ""
			if err := m.runScheduledRunWorkItem(context.Background(), claimed); err != nil {
				status = workStatusFailed
				errorText = err.Error()
			}
			if err := m.completeWorkItem(context.Background(), claimed.ID, status, errorText); err != nil {
				log.Printf("failed to complete scheduled run work item id=%d: %v", claimed.ID, err)
			}
		}()
	}
	return nil
}

func (m *goSchedulerManager) runScheduledRunWorkItem(ctx context.Context, item schedulerWorkItem) error {
	payload := item.Payload
	runID := firstPositiveInt64(coerceInt64(payload["run_id"]), nullInt(item.RunID))
	jobID := firstPositiveInt64(coerceInt64(payload["job_id"]), nullInt(item.JobID))
	if runID <= 0 || jobID <= 0 {
		return errors.New("scheduled run work item payload incomplete")
	}
	if err := m.markScheduledRunRunning(ctx, runID); err != nil {
		return err
	}
	if boolFromAny(payload["shared_execution"]) {
		ansibleComponents := schedulerAnyList(payload["ansible_components"])
		componentIndex := int(coerceInt64(payload["component_index"]))
		if componentIndex < 0 || componentIndex >= len(ansibleComponents) {
			return m.failScheduledRun(ctx, runID, "No runnable Ansible component was found for this scheduled run.")
		}
		component := schedulerAnyMap(ansibleComponents[componentIndex])
		if len(component) == 0 {
			return m.failScheduledRun(ctx, runID, "No runnable Ansible component was found for this scheduled run.")
		}
		return m.dispatchScheduledAnsible(ctx, scheduledAnsibleDispatch{
			JobID:             jobID,
			RunID:             runID,
			ScheduledTS:       coerceInt64(payload["scheduled_ts"]),
			RunMode:           cleanText(payload["run_mode"]),
			Component:         component,
			CredentialID:      nullablePositiveInt64(payload["credential_id"]),
			UseServiceAccount: boolFromAny(payload["use_service_account"]),
			TargetRowIDs:      positiveInt64sFromAny(payload["target_row_ids"]),
		})
	}
	hostname, err := m.scheduledRunHostname(ctx, runID)
	if err != nil {
		return err
	}
	if hostname == "" {
		return m.failScheduledRun(ctx, runID, "Scheduled run has no target hostname.")
	}
	scriptComponents := schedulerAnyList(payload["script_components"])
	ansibleComponents := schedulerAnyList(payload["ansible_components"])
	selectedAnsible := ansibleComponents
	if _, ok := payload["component_index"]; ok {
		componentIndex := int(coerceInt64(payload["component_index"]))
		if componentIndex >= 0 && componentIndex < len(ansibleComponents) {
			selectedAnsible = []any{ansibleComponents[componentIndex]}
		} else {
			selectedAnsible = []any{}
		}
	}
	dispatched := 0
	var dispatchErrors []string
	for _, raw := range scriptComponents {
		component := schedulerAnyMap(raw)
		if len(component) == 0 {
			continue
		}
		if err := m.dispatchScheduledScript(ctx, scheduledScriptDispatch{
			JobID:       jobID,
			RunID:       runID,
			ScheduledTS: coerceInt64(payload["scheduled_ts"]),
			Hostname:    hostname,
			RunMode:     cleanText(payload["run_mode"]),
			Component:   component,
		}); err != nil {
			dispatchErrors = append(dispatchErrors, err.Error())
			continue
		}
		dispatched++
	}
	for _, raw := range selectedAnsible {
		component := schedulerAnyMap(raw)
		if len(component) == 0 {
			continue
		}
		if err := m.dispatchScheduledAnsible(ctx, scheduledAnsibleDispatch{
			JobID:             jobID,
			RunID:             runID,
			ScheduledTS:       coerceInt64(payload["scheduled_ts"]),
			RunMode:           cleanText(payload["run_mode"]),
			Component:         component,
			CredentialID:      nullablePositiveInt64(payload["credential_id"]),
			UseServiceAccount: boolFromAny(payload["use_service_account"]),
		}); err != nil {
			dispatchErrors = append(dispatchErrors, err.Error())
			continue
		}
		dispatched++
	}
	if dispatched == 0 {
		message := "No runnable activities were dispatched."
		if len(dispatchErrors) > 0 {
			message = dispatchErrors[0]
		}
		return m.failScheduledRun(ctx, runID, message)
	}
	return nil
}

type scheduledScriptDispatch struct {
	JobID       int64
	RunID       int64
	ScheduledTS int64
	Hostname    string
	RunMode     string
	Component   map[string]any
}

func (m *goSchedulerManager) dispatchScheduledScript(ctx context.Context, req scheduledScriptDispatch) error {
	resolved, err := m.resolveScheduledComponentDocument(ctx, req.Component, "script")
	if err != nil {
		return err
	}
	scriptType := quickRunNormalizeScriptType(resolved.Doc["type"])
	if !quickRunSupportedScriptType(scriptType) {
		return fmt.Errorf("unsupported scheduled script type %s", scriptType)
	}
	content := cleanText(resolved.Doc["script"])
	overrides := scheduledComponentVariableOverrides(req.Component)
	envMap, variables, literalLookup := quickRunPrepareVariableContext(quickRunVariables(resolved.Doc["variables"]), overrides)
	content = quickRunRewriteScriptForDispatch(content, scriptType, literalLookup)
	scriptBytes := []byte(content)
	signer, err := loadOrCreateScriptSigner()
	if err != nil || signer == nil || len(signer.privateKey) == 0 {
		return errors.New("script signer unavailable")
	}
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(signer.privateKey, scriptBytes))
	signingKey := scriptSigningKeyB64(signer)
	friendlyName := firstText(cleanText(resolved.Doc["name"]), cleanText(req.Component["name"]), filepath.Base(resolved.RelativePath), "Scheduled Script")
	runMode := quickRunNormalizeRunMode(req.RunMode)
	metadata := scheduledActivityMetadata(req.JobID, req.RunID, req.ScheduledTS, "script", friendlyName, resolved.GUID)
	activityID, err := m.insertScheduledActivity(ctx, scheduledActivityInsert{
		RunID:         req.RunID,
		Hostname:      req.Hostname,
		ScriptPath:    resolved.RelativePath,
		ScriptName:    friendlyName,
		ScriptType:    scriptType,
		Status:        "Queued",
		ComponentKind: "script",
		Metadata:      metadata,
	})
	if err != nil {
		return err
	}
	snapshot, status, err := m.store.loadDeviceProcessContext(ctx, operatorProfile{Username: "job-scheduler", Role: "Admin"}, req.Hostname)
	if err != nil || snapshot.Route == nil {
		failure := "No active site-worker route is available for host " + req.Hostname + "."
		if err != nil && status != http.StatusNotFound {
			failure = err.Error()
		}
		_ = m.markScheduledActivityFailed(ctx, activityID, failure)
		_ = m.failScheduledRun(ctx, req.RunID, failure)
		return errors.New(failure)
	}
	payload := map[string]any{
		"job_id":          activityID,
		"target_hostname": req.Hostname,
		"script_type":     scriptType,
		"script_name":     friendlyName,
		"script_path":     resolved.RelativePath,
		"script_content":  base64.StdEncoding.EncodeToString(scriptBytes),
		"script_encoding": "base64",
		"environment":     envMap,
		"variables":       variables,
		"timeout_seconds": maxInt64(0, coerceInt64(firstNonEmpty(resolved.Doc["timeout_seconds"], 0))),
		"files":           quickRunFiles(resolved.Doc["files"]),
		"run_mode":        runMode,
		"admin_user":      "",
		"admin_pass":      "",
		"signature":       signature,
		"sig_alg":         "ed25519",
		"signing_key":     signingKey,
		"context": map[string]any{
			"scheduled_job_id":     req.JobID,
			"scheduled_job_run_id": req.RunID,
			"scheduled_ts":         req.ScheduledTS,
			"queue_lane":           "scheduled_job_system",
			"activity_kind":        "scheduled_job",
			"activity_metadata":    metadata,
		},
	}
	for key, value := range quickRunCurrentUserDispatchFields(runMode, "all_active_sessions", nil) {
		payload[key] = value
	}
	if resolved.GUID != "" {
		payload["context"].(map[string]any)["assembly_guid"] = resolved.GUID
	}
	result, state, err := m.emitSiteWorkerHostServiceEvent(ctx, snapshot.Route, map[string]any{
		"hostname":            req.Hostname,
		"service_mode":        runMode,
		"event_name":          "quick_job_run",
		"payload":             payload,
		"allow_pending":       true,
		"pending_ttl_seconds": 240,
	}, 6*time.Second)
	if err != nil || (!boolFromAny(result["emitted"]) && !boolFromAny(result["queued"])) {
		failure := fmt.Sprintf("No %s agent socket is registered for host %s; unable to dispatch scheduled job.", runMode, req.Hostname)
		if err != nil {
			failure = firstText(err.Error(), state, failure)
		}
		_ = m.markScheduledActivityFailed(ctx, activityID, failure)
		_ = m.failScheduledRun(ctx, req.RunID, failure)
		return errors.New(failure)
	}
	return nil
}

type scheduledAnsibleDispatch struct {
	JobID             int64
	RunID             int64
	ScheduledTS       int64
	RunMode           string
	Component         map[string]any
	CredentialID      *int64
	UseServiceAccount bool
	TargetRowIDs      []int64
}

func (m *goSchedulerManager) dispatchScheduledAnsible(ctx context.Context, req scheduledAnsibleDispatch) error {
	transport := normalizeScheduledAnsibleTransport(req.RunMode)
	if !stringInSet(transport, "local", "ssh", "winrm") {
		return m.failScheduledRun(ctx, req.RunID, "Unsupported Ansible execution context "+req.RunMode+".")
	}
	resolved, err := m.resolveScheduledComponentDocument(ctx, req.Component, "ansible")
	if err != nil {
		return err
	}
	targets, err := m.loadScheduledExecutionTargets(ctx, req.RunID, req.TargetRowIDs)
	if err != nil {
		return err
	}
	targetSpecs, runtimeFiles, err := m.scheduledAnsibleTargetSpecs(ctx, req, transport, targets)
	if err != nil {
		return err
	}
	if len(targetSpecs) == 0 && transport != "local" {
		return nil
	}
	friendlyName := firstText(cleanText(resolved.Doc["name"]), cleanText(req.Component["name"]), filepath.Base(resolved.RelativePath), "Scheduled Playbook")
	metadata := scheduledActivityMetadata(req.JobID, req.RunID, req.ScheduledTS, "ansible", friendlyName, resolved.GUID)
	activityID, err := m.insertScheduledActivity(ctx, scheduledActivityInsert{
		RunID:         req.RunID,
		Hostname:      "borealis-engine-01",
		ScriptPath:    resolved.RelativePath,
		ScriptName:    friendlyName,
		ScriptType:    "ansible",
		Status:        "Running",
		ComponentKind: "ansible",
		Metadata:      metadata,
	})
	if err != nil {
		return err
	}
	routeSiteID := int64(0)
	for _, target := range targets {
		if target.SiteID > 0 {
			routeSiteID = target.SiteID
			break
		}
	}
	route, err := m.store.watchdogAnsibleRoute(ctx, int64PtrOrNil(routeSiteID))
	if err != nil {
		_ = m.markScheduledActivityFailed(ctx, activityID, err.Error())
		_ = m.failScheduledRun(ctx, req.RunID, err.Error())
		return err
	}
	queueRun := map[string]any{
		"hostname":                 "borealis-engine-01",
		"playbook_rel_path":        resolved.RelativePath,
		"playbook_name":            friendlyName,
		"playbook_content":         strings.ReplaceAll(cleanText(resolved.Doc["script"]), "\r\n", "\n"),
		"credential_id":            nullableScheduledCredentialID(req.CredentialID),
		"variable_values":          scheduledComponentVariableOverrides(req.Component),
		"payload_files":            quickRunFiles(resolved.Doc["files"]),
		"target_specifications":    targetSpecs,
		"runtime_files":            runtimeFiles,
		"source":                   "scheduled_job",
		"activity_id":              activityID,
		"scheduled_job_id":         req.JobID,
		"scheduled_run_id":         req.RunID,
		"scheduled_job_run_row_id": req.RunID,
		"connection":               transport,
	}
	response, errPayload := m.postSiteWorkerJSON(ctx, route, "/automation/ansible/run", map[string]any{"queue_run": queueRun}, 10*time.Second)
	if errPayload != nil {
		message := firstText(cleanText(errPayload["message"]), cleanText(errPayload["error"]), "Ansible playbook dispatch failed.")
		_ = m.markScheduledActivityFailed(ctx, activityID, message)
		_ = m.failScheduledRun(ctx, req.RunID, message)
		return errors.New(message)
	}
	if cleanText(response["run_id"]) == "" {
		message := "Site-worker did not return an Ansible run id."
		_ = m.markScheduledActivityFailed(ctx, activityID, message)
		_ = m.failScheduledRun(ctx, req.RunID, message)
		return errors.New(message)
	}
	return nil
}

func (m *goSchedulerManager) scheduledAnsibleTargetSpecs(ctx context.Context, req scheduledAnsibleDispatch, transport string, targets []scheduledExecutionTarget) ([]any, []any, error) {
	if transport == "local" {
		_ = m.updateScheduledRunTargets(ctx, req.RunID, req.TargetRowIDs, schedulerResolutionEligible, "", "local", "", "")
		return []any{}, []any{}, nil
	}
	if len(targets) == 0 {
		_ = m.markScheduledRunSkipped(ctx, req.RunID, "No devices were targeted for this Ansible run.")
		return nil, nil, nil
	}
	var credential map[string]any
	var runtimeFiles []any
	privateKeyPath := ""
	if transport == "ssh" || (transport == "winrm" && !req.UseServiceAccount) {
		if req.CredentialID == nil {
			_ = m.updateScheduledRunTargets(ctx, req.RunID, req.TargetRowIDs, schedulerResolutionUnresolved, "credential_missing", transport, "", "")
			return nil, nil, m.failScheduledRun(ctx, req.RunID, "Credential required for remote execution")
		}
		payload, err := m.internalJSON(ctx, http.MethodGet, fmt.Sprintf("/api/internal/job-scheduler/credential/%d", *req.CredentialID), nil, 30*time.Second)
		if err != nil {
			_ = m.updateScheduledRunTargets(ctx, req.RunID, req.TargetRowIDs, schedulerResolutionUnresolved, "credential_unavailable", transport, "", "")
			return nil, nil, m.failScheduledRun(ctx, req.RunID, firstText(cleanText(payload["message"]), cleanText(payload["error"]), err.Error()))
		}
		credential = schedulerAnyMap(payload["credential"])
		if strings.ToLower(cleanText(credential["connection_type"])) != "" && strings.ToLower(cleanText(credential["connection_type"])) != transport {
			_ = m.updateScheduledRunTargets(ctx, req.RunID, req.TargetRowIDs, schedulerResolutionUnresolved, "credential_connection_mismatch", transport, "", "")
			return nil, nil, m.failScheduledRun(ctx, req.RunID, "Selected credential does not match the execution context.")
		}
		if transport == "ssh" {
			privateKey, err := scheduledSSHPrivateKeyContent(credential)
			if err != nil {
				_ = m.updateScheduledRunTargets(ctx, req.RunID, req.TargetRowIDs, schedulerResolutionUnresolved, "credential_private_key_invalid", transport, "", "")
				return nil, nil, m.failScheduledRun(ctx, req.RunID, err.Error())
			}
			if privateKey != "" {
				privateKeyPath = scheduledSSHPrivateKeyPath
				runtimeFiles = append(runtimeFiles, map[string]any{"relative_path": "auth/id_borealis_ssh", "content": privateKey, "mode": 384})
			}
		}
	}
	requiredPorts := []any{}
	targetPorts := map[int64]int{}
	agentIDs := []any{}
	for _, target := range targets {
		if target.AgentID == "" {
			continue
		}
		port := scheduledEndpointPort(target.ConnectionEndpoint)
		if port <= 0 {
			if transport == "winrm" {
				port = 5985
			} else {
				port = 22
			}
		}
		targetPorts[target.ID] = port
		agentIDs = append(agentIDs, target.AgentID)
		requiredPorts = append(requiredPorts, port)
	}
	if err := m.markScheduledConnectionProbe(ctx, req.RunID, req.TargetRowIDs, transport); err != nil {
		return nil, nil, err
	}
	vpnPayload, err := m.internalJSON(ctx, http.MethodPost, "/api/internal/job-scheduler/vpn-prepare", map[string]any{
		"agent_ids":             agentIDs,
		"required_ports":        requiredPorts,
		"reason":                "scheduled_ansible",
		"timeout_seconds":       scheduledConnectionProbeTimeoutSeconds,
		"poll_interval_seconds": 0.5,
	}, time.Duration(scheduledConnectionProbeTimeoutSeconds+scheduledConnectionProbeRequestGraceSeconds)*time.Second)
	if err != nil {
		_ = m.updateScheduledRunTargets(ctx, req.RunID, req.TargetRowIDs, schedulerResolutionSkipped, "wireguard_unavailable", transport, "", "")
		return nil, nil, m.markScheduledRunSkipped(ctx, req.RunID, "Managed WireGuard session is unavailable for this Ansible run.")
	}
	sessions := schedulerAnyMap(vpnPayload["sessions"])
	specs := []any{}
	skipped := 0
	for _, target := range targets {
		session := schedulerAnyMap(sessions[target.AgentID])
		peerIP := strings.Split(cleanText(session["virtual_ip"]), "/")[0]
		port := targetPorts[target.ID]
		if peerIP == "" || !boolFromAny(session["dispatch_ready"]) {
			skipped++
			_ = m.updateScheduledTargetRow(ctx, target.ID, schedulerResolutionSkipped, firstText(cleanText(session["dispatch_ready_reason"]), "wireguard_unavailable"), transport, peerIP, target.InventoryHostname)
			continue
		}
		hostVars := map[string]any{
			"ansible_host":       peerIP,
			"ansible_connection": transport,
		}
		if port > 0 {
			hostVars["ansible_port"] = port
		}
		if transport == "ssh" {
			applyScheduledSSHCredentialHostVars(hostVars, credential, privateKeyPath)
			hostVars["ansible_ssh_retries"] = envInt("BOREALIS_SHARED_ANSIBLE_SSH_RETRIES", 3, 1, 20)
			hostVars["ansible_ssh_timeout"] = envInt("BOREALIS_SHARED_ANSIBLE_SSH_TIMEOUT_SECONDS", 10, 1, 120)
			hostVars["ansible_ssh_transfer_method"] = firstText(cleanText(os.Getenv("BOREALIS_SHARED_ANSIBLE_SSH_TRANSFER_METHOD")), "sftp")
		} else {
			username := ""
			password := ""
			transportName := "ntlm"
			if req.UseServiceAccount {
				payload, err := m.internalJSON(ctx, http.MethodGet, "/api/internal/job-scheduler/service-account/"+target.AgentID, nil, 30*time.Second)
				if err != nil {
					skipped++
					_ = m.updateScheduledTargetRow(ctx, target.ID, schedulerResolutionSkipped, "service_account_unavailable", transport, peerIP, target.InventoryHostname)
					continue
				}
				account := schedulerAnyMap(payload["service_account"])
				username = cleanText(account["username"])
				password = cleanText(account["password"])
			} else {
				username = cleanText(credential["username"])
				password = cleanText(credential["password"])
				metadata := schedulerAnyMap(credential["metadata"])
				transportName = firstText(cleanText(metadata["winrm_transport"]), "ntlm")
			}
			if username == "" || password == "" {
				skipped++
				_ = m.updateScheduledTargetRow(ctx, target.ID, schedulerResolutionSkipped, "credential_incomplete", transport, peerIP, target.InventoryHostname)
				continue
			}
			hostVars["ansible_user"] = username
			hostVars["ansible_password"] = password
			hostVars["ansible_winrm_transport"] = transportName
			hostVars["ansible_winrm_server_cert_validation"] = "ignore"
		}
		inventory := firstText(target.InventoryHostname, scheduledSafeInventoryLabel(target.Hostname, "host"))
		_ = m.updateScheduledTargetRow(ctx, target.ID, schedulerResolutionEligible, "", transport, peerIP, inventory)
		specs = append(specs, map[string]any{
			"hostname":           target.Hostname,
			"inventory_hostname": inventory,
			"site_group":         scheduledSiteGroupName(target.SiteName, target.SiteID),
			"site_id":            target.SiteID,
			"host_vars":          hostVars,
		})
	}
	if len(specs) == 0 {
		_ = m.markScheduledRunSkipped(ctx, req.RunID, fmt.Sprintf("No eligible devices were available for this Ansible run (%d skipped).", skipped))
	} else {
		_ = m.markScheduledRunRunning(ctx, req.RunID)
	}
	return specs, runtimeFiles, nil
}

func scheduledSSHPrivateKeyContent(credential map[string]any) (string, error) {
	return ansibleSSHPrivateKeyContent(credential, "scheduled Ansible runs")
}

func ansibleSSHPrivateKeyContent(credential map[string]any, contextLabel string) (string, error) {
	privateKey := normalizeSSHPrivateKeyMaterial(cleanText(credential["private_key"]))
	if privateKey == "" {
		return "", nil
	}
	password := cleanText(credential["password"])
	if cleanText(credential["private_key_passphrase"]) != "" {
		if password == "" {
			return "", fmt.Errorf("Passphrase-protected SSH private keys require a credential password for %s.", contextLabel)
		}
		return "", nil
	}
	if err := parseSSHPrivateKeyMaterial(privateKey); err != nil {
		if password != "" {
			return "", nil
		}
		return "", errors.New("Selected SSH private key could not be parsed by OpenSSH. Save a valid unencrypted SSH private key or add a password fallback.")
	}
	return privateKey, nil
}

func applyScheduledSSHCredentialHostVars(hostVars map[string]any, credential map[string]any, privateKeyPath string) {
	if username := cleanText(credential["username"]); username != "" {
		hostVars["ansible_user"] = username
	}
	password := cleanText(credential["password"])
	if password != "" {
		hostVars["ansible_password"] = password
		hostVars["ansible_ssh_password_mechanism"] = "sshpass"
	}
	if privateKeyPath != "" {
		hostVars["ansible_ssh_private_key_file"] = privateKeyPath
		existing := cleanText(hostVars["ansible_ssh_extra_args"])
		addition := "-o IdentitiesOnly=yes -o PreferredAuthentications=publickey,password,keyboard-interactive -o PubkeyAuthentication=yes -o PasswordAuthentication=yes -o KbdInteractiveAuthentication=yes"
		if password == "" {
			addition = "-o IdentitiesOnly=yes -o BatchMode=yes -o PreferredAuthentications=publickey -o PubkeyAuthentication=yes -o PasswordAuthentication=no -o KbdInteractiveAuthentication=no"
		}
		if existing == "" {
			hostVars["ansible_ssh_extra_args"] = addition
		} else if !strings.Contains(existing, addition) {
			hostVars["ansible_ssh_extra_args"] = existing + " " + addition
		}
	}
	if become := cleanText(credential["become_method"]); become != "" {
		hostVars["ansible_become"] = true
		hostVars["ansible_become_method"] = become
		if username := cleanText(credential["become_username"]); username != "" {
			hostVars["ansible_become_user"] = username
		}
		if password := cleanText(credential["become_password"]); password != "" {
			hostVars["ansible_become_password"] = password
		}
	}
}

type scheduledActivityInsert struct {
	RunID         int64
	Hostname      string
	ScriptPath    string
	ScriptName    string
	ScriptType    string
	Status        string
	ComponentKind string
	Metadata      map[string]any
}

func (m *goSchedulerManager) insertScheduledActivity(ctx context.Context, req scheduledActivityInsert) (int64, error) {
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return 0, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	metadataJSON, err := json.Marshal(req.Metadata)
	if err != nil {
		return 0, err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer rollbackQuietly(tx)
	now := time.Now().Unix()
	var startedAt any
	if strings.EqualFold(req.Status, "running") {
		startedAt = now
	}
	var activityID int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO engine.activity_history(
			hostname, script_path, script_name, script_type, ran_at, status,
			stdout, stderr, queue_lane, activity_kind, metadata_json,
			started_at, updated_at, finished_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING id
	`, cleanText(req.Hostname), cleanText(req.ScriptPath), cleanText(req.ScriptName), cleanText(req.ScriptType), now, firstText(cleanText(req.Status), "Queued"), "", "", "scheduled_job_system", "scheduled_job", string(metadataJSON), startedAt, now, nil).Scan(&activityID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO engine.scheduled_job_run_activity(
			run_id, activity_id, component_kind, script_type, component_path, component_name, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7)
	`, req.RunID, activityID, cleanText(req.ComponentKind), cleanText(req.ScriptType), cleanText(req.ScriptPath), cleanText(req.ScriptName), now); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return activityID, nil
}

func (m *goSchedulerManager) resolveScheduledComponentDocument(ctx context.Context, component map[string]any, domain string) (scheduledComponentDocument, error) {
	guid := assemblyCoerceGUID(firstNonEmptyAny(component["assembly_guid"], component["assemblyGuid"], component["assembly_id"], component["assemblyId"]))
	relPath := ""
	defaultType := "powershell"
	if domain == "ansible" {
		defaultType = "ansible"
		relPath = scheduledAnsibleRelPath(firstNonEmptyAny(component["path"], component["playbook_path"], component["script_path"]))
	} else {
		relPath = quickRunNormalizeScriptRelPath(firstNonEmptyAny(component["path"], component["script_path"]))
	}
	if guid != "" {
		item, found, err := m.store.getAssembly(ctx, guid, true)
		if err != nil {
			return scheduledComponentDocument{}, err
		}
		if !found {
			return scheduledComponentDocument{}, errors.New("assembly not found")
		}
		if relPath == "" {
			if domain == "ansible" {
				relPath = scheduledAnsibleRelPath(quickRunItemPath(item))
			} else {
				relPath = quickRunNormalizeScriptRelPath(quickRunItemPath(item))
			}
		}
		payload := quickRunPayloadMap(item)
		if payload == nil {
			return scheduledComponentDocument{}, errors.New("assembly payload unavailable")
		}
		doc := quickRunLoadAssemblyDocument(relPath, defaultType, payload)
		return scheduledComponentDocument{Doc: doc, Record: item, RelativePath: relPath, GUID: guid}, nil
	}
	if relPath == "" {
		return scheduledComponentDocument{}, errors.New("component path or assembly GUID required")
	}
	items, _, err := m.store.listAssemblies(ctx, assemblyListFilter{})
	if err != nil {
		return scheduledComponentDocument{}, err
	}
	for _, item := range items {
		itemPath := quickRunItemPath(item)
		normalized := quickRunNormalizeScriptRelPath(itemPath)
		if domain == "ansible" {
			normalized = scheduledAnsibleRelPath(itemPath)
		}
		if !strings.EqualFold(normalized, relPath) {
			continue
		}
		itemGUID := assemblyCoerceGUID(firstNonEmptyAny(item["assembly_guid"], item["assembly_id"]))
		fullItem := item
		if itemGUID != "" {
			if loaded, found, loadErr := m.store.getAssembly(ctx, itemGUID, true); loadErr == nil && found {
				fullItem = loaded
			}
		}
		payload := quickRunPayloadMap(fullItem)
		if payload == nil {
			return scheduledComponentDocument{}, errors.New("assembly payload unavailable")
		}
		doc := quickRunLoadAssemblyDocument(relPath, defaultType, payload)
		return scheduledComponentDocument{Doc: doc, Record: fullItem, RelativePath: relPath, GUID: itemGUID}, nil
	}
	return scheduledComponentDocument{}, errors.New("assembly not found")
}

func scheduledComponentVariableOverrides(component map[string]any) map[string]any {
	out := map[string]any{}
	if raw := schedulerAnyMap(component["variable_values"]); len(raw) > 0 {
		for key, value := range raw {
			if name := cleanText(key); name != "" {
				out[name] = value
			}
		}
	}
	for _, item := range schedulerAnyList(component["variables"]) {
		variable := schedulerAnyMap(item)
		name := cleanText(variable["name"])
		if name == "" {
			continue
		}
		if _, exists := out[name]; !exists {
			if value, ok := variable["value"]; ok {
				out[name] = value
			}
		}
	}
	return out
}

func scheduledActivityMetadata(jobID, runID, scheduledTS int64, componentKind, componentName, assemblyGUID string) map[string]any {
	out := map[string]any{
		"assembly_source":      "runtime",
		"scheduled_job_id":     jobID,
		"scheduled_job_run_id": runID,
		"scheduled_ts":         scheduledTS,
		"component_kind":       componentKind,
		"component_name":       componentName,
	}
	if assemblyGUID != "" {
		out["assembly_guid"] = assemblyGUID
	}
	return out
}

func (m *goSchedulerManager) scheduledRunHostname(ctx context.Context, runID int64) (string, error) {
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return "", errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	var hostname sql.NullString
	err = conn.QueryRowContext(ctx, `SELECT target_hostname FROM engine.scheduled_job_runs WHERE id=$1`, runID).Scan(&hostname)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return cleanText(hostname.String), nil
}

func (m *goSchedulerManager) markScheduledRunRunning(ctx context.Context, runID int64) error {
	now := time.Now().Unix()
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	_, err = conn.ExecContext(ctx, `
		UPDATE engine.scheduled_job_runs
		   SET status=$1, started_ts=COALESCE(started_ts, $2), finished_ts=NULL, error='', updated_at=$3
		 WHERE id=$4
	`, scheduledStatusRunning, now, now, runID)
	return err
}

func (m *goSchedulerManager) markScheduledConnectionProbe(ctx context.Context, runID int64, targetRowIDs []int64, connection string) error {
	now := time.Now().Unix()
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackQuietly(tx)
	if _, err := tx.ExecContext(ctx, `
		UPDATE engine.scheduled_job_runs
		   SET status=$1, updated_at=$2
		 WHERE id=$3
	`, scheduledStatusEstablishingConnection, now, runID); err != nil {
		return err
	}
	filter := ""
	args := []any{connection, schedulerResolutionEstablishingConnection, runID}
	if len(targetRowIDs) > 0 {
		filter = " AND id = ANY($4)"
		args = append(args, pq.Array(targetRowIDs))
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE engine.scheduled_job_run_targets
		   SET wireguard_peer_ip='',
		       resolved_connection=$1,
		       resolution_status=$2,
		       resolution_reason=''
		 WHERE run_id=$3`+filter, args...); err != nil {
		return err
	}
	return tx.Commit()
}

func (m *goSchedulerManager) failScheduledRun(ctx context.Context, runID int64, message string) error {
	now := time.Now().Unix()
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	_, err = conn.ExecContext(ctx, `
		UPDATE engine.scheduled_job_runs
		   SET status=$1, finished_ts=$2, updated_at=$3, error=$4
		 WHERE id=$5
	`, scheduledStatusFailed, now, now, truncateString(message, 512), runID)
	return err
}

func (m *goSchedulerManager) markScheduledRunSkipped(ctx context.Context, runID int64, message string) error {
	now := time.Now().Unix()
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	_, err = conn.ExecContext(ctx, `
		UPDATE engine.scheduled_job_runs
		   SET status=$1, finished_ts=$2, updated_at=$3, skip_reason=$4, error=$5
		 WHERE id=$6
	`, scheduledStatusSkipped, now, now, scheduledSkipNoTargets, truncateString(message, 512), runID)
	return err
}

func (m *goSchedulerManager) markScheduledActivityFailed(ctx context.Context, activityID int64, failureText string) error {
	return m.store.markQuickRunActivityFailed(ctx, activityID, failureText)
}

func (m *goSchedulerManager) loadScheduledExecutionTargets(ctx context.Context, runID int64, targetRowIDs []int64) ([]scheduledExecutionTarget, error) {
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	filter := ""
	args := []any{runID}
	if len(targetRowIDs) > 0 {
		filter = " AND t.id = ANY($2)"
		args = append(args, pq.Array(targetRowIDs))
	}
	rows, err := conn.QueryContext(ctx, `
		SELECT t.id, t.hostname, COALESCE(t.device_guid, ''), COALESCE(t.site_id, 0),
		       COALESCE(s.name, ''), COALESCE(d.agent_id, ''), COALESCE(d.connection_endpoint, ''),
		       COALESCE(t.inventory_hostname, '')
		  FROM engine.scheduled_job_run_targets t
	 LEFT JOIN engine.devices d ON LOWER(d.hostname)=LOWER(t.hostname)
	 LEFT JOIN engine.sites s ON s.id=t.site_id
		 WHERE t.run_id=$1`+filter+`
	  ORDER BY t.id ASC
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []scheduledExecutionTarget
	for rows.Next() {
		var target scheduledExecutionTarget
		if err := rows.Scan(&target.ID, &target.Hostname, &target.DeviceGUID, &target.SiteID, &target.SiteName, &target.AgentID, &target.ConnectionEndpoint, &target.InventoryHostname); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func (m *goSchedulerManager) updateScheduledRunTargets(ctx context.Context, runID int64, targetRowIDs []int64, status string, reason string, connection string, peerIP string, inventory string) error {
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	filter := ""
	args := []any{inventory, peerIP, connection, status, reason, runID}
	if len(targetRowIDs) > 0 {
		filter = " AND id = ANY($7)"
		args = append(args, pq.Array(targetRowIDs))
	}
	_, err = conn.ExecContext(ctx, `
		UPDATE engine.scheduled_job_run_targets
		   SET inventory_hostname=COALESCE(NULLIF($1, ''), inventory_hostname),
		       wireguard_peer_ip=$2,
		       resolved_connection=$3,
		       resolution_status=$4,
		       resolution_reason=$5
		 WHERE run_id=$6`+filter, args...)
	return err
}

func (m *goSchedulerManager) updateScheduledTargetRow(ctx context.Context, targetID int64, status string, reason string, connection string, peerIP string, inventory string) error {
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	_, err = conn.ExecContext(ctx, `
		UPDATE engine.scheduled_job_run_targets
		   SET inventory_hostname=COALESCE(NULLIF($1, ''), inventory_hostname),
		       wireguard_peer_ip=$2,
		       resolved_connection=$3,
		       resolution_status=$4,
		       resolution_reason=$5
		 WHERE id=$6
	`, inventory, peerIP, connection, status, reason, targetID)
	return err
}

func (m *goSchedulerManager) emitSiteWorkerHostServiceEvent(ctx context.Context, route *agentWorkerRoute, body map[string]any, timeout time.Duration) (map[string]any, string, error) {
	response, errPayload := m.postSiteWorkerJSON(ctx, route, "/remote-ops/host-service/event", body, timeout)
	if errPayload != nil {
		return response, firstText(cleanText(errPayload["error"]), "site_worker_error"), errors.New(firstText(cleanText(errPayload["message"]), cleanText(errPayload["error"]), "site worker error"))
	}
	return response, "ok", nil
}

func (m *goSchedulerManager) postSiteWorkerJSON(ctx context.Context, route *agentWorkerRoute, path string, body map[string]any, timeout time.Duration) (map[string]any, map[string]any) {
	if route == nil {
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
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, map[string]any{"error": "invalid_request", "message": err.Error()}
	}
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, target, bytes.NewReader(raw))
	if err != nil {
		return nil, map[string]any{"error": "worker_request_failed", "message": err.Error()}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(internalTokenHeader, goInternalToken(m.secret))
	client := m.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, map[string]any{"error": "site_worker_unavailable", "message": err.Error()}
	}
	defer resp.Body.Close()
	var payload map[string]any
	_ = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&payload)
	if payload == nil {
		payload = map[string]any{}
	}
	if resp.StatusCode >= 400 {
		if cleanText(payload["error"]) == "" {
			payload["error"] = "site_worker_error"
		}
		return payload, payload
	}
	return payload, nil
}

func scheduledAnsibleRelPath(value any) string {
	text := strings.ReplaceAll(cleanText(value), "\\", "/")
	text = strings.TrimLeft(strings.TrimSpace(text), "/")
	if text == "" {
		return ""
	}
	parts := []string{}
	for _, part := range strings.Split(text, "/") {
		candidate := strings.TrimSpace(part)
		if candidate == "" || candidate == "." {
			continue
		}
		if candidate == ".." {
			return ""
		}
		parts = append(parts, candidate)
	}
	if len(parts) == 0 {
		return ""
	}
	if !strings.EqualFold(parts[0], "Ansible_Playbooks") {
		parts = append([]string{"Ansible_Playbooks"}, parts...)
	} else {
		parts[0] = "Ansible_Playbooks"
	}
	return strings.Join(parts, "/")
}

func nullablePositiveInt64(value any) *int64 {
	parsed := coerceInt64(value)
	if parsed <= 0 {
		return nil
	}
	return &parsed
}

func nullableScheduledCredentialID(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func int64PtrOrNil(value int64) *int64 {
	if value <= 0 {
		return nil
	}
	return &value
}

func positiveInt64sFromAny(value any) []int64 {
	out := []int64{}
	for _, item := range schedulerAnyList(value) {
		if parsed := coerceInt64(item); parsed > 0 {
			out = append(out, parsed)
		}
	}
	return out
}

func scheduledEndpointPort(endpoint string) int {
	text := strings.TrimSpace(endpoint)
	if text == "" {
		return 0
	}
	if _, portText, err := net.SplitHostPort(text); err == nil {
		if parsed, parseErr := strconv.Atoi(portText); parseErr == nil && parsed > 0 {
			return parsed
		}
	}
	if strings.Contains(text, ":") {
		parts := strings.Split(text, ":")
		if parsed, err := strconv.Atoi(parts[len(parts)-1]); err == nil && parsed > 0 {
			return parsed
		}
	}
	return 0
}

func scheduledSiteGroupName(siteName string, siteID int64) string {
	base := strings.ToLower(cleanText(siteName))
	if base == "" {
		base = fmt.Sprintf("site_%d", siteID)
	}
	var builder strings.Builder
	for _, ch := range base {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
			builder.WriteRune(ch)
		} else {
			builder.WriteByte('_')
		}
	}
	result := strings.Trim(builder.String(), "_")
	if result == "" {
		result = "site_unassigned"
	}
	return result
}
