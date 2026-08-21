package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bunny-lab-io/borealis/go-agent/internal/auth"
	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
)

const (
	updateSourceOperator = "operator_initiated"
	updateSourceHourly   = "hourly_update_checker"
)

type updateProgressReporter struct {
	configPath string
	logger     updateProgressLogger
}

type updateProgressLogger interface {
	Tracef(format string, args ...any)
}

func newUpdateProgressReporter(configPath string, logger updateProgressLogger) *updateProgressReporter {
	return &updateProgressReporter{configPath: strings.TrimSpace(configPath), logger: logger}
}

func (r *updateProgressReporter) operation() agentconfig.AgentUpdateSection {
	if r == nil || r.configPath == "" {
		return agentconfig.AgentUpdateSection{}
	}
	cfg, err := agentconfig.Load(r.configPath)
	if err != nil {
		return agentconfig.AgentUpdateSection{}
	}
	return cfg.Agent.Update
}

func (r *updateProgressReporter) ensureHourlyOperation(targetBuildID string, installedBuildID string) agentconfig.AgentUpdateSection {
	current := r.operation()
	if updateOperationIsActive(current) {
		return current
	}
	now := time.Now().Unix()
	operation := agentconfig.AgentUpdateSection{
		OperationID:          newAgentUpdateOperationID(),
		Kind:                 "update_now",
		Source:               updateSourceHourly,
		RequestedBy:          "Hourly Update Checker",
		Status:               "running",
		TargetBuildID:        agentconfig.NormalizeBuildID(targetBuildID),
		InstalledBuildBefore: agentconfig.NormalizeBuildID(installedBuildID),
		StartedAt:            now,
		UpdatedAt:            now,
		DeadlineAt:           now + int64(15*time.Minute/time.Second),
	}
	_ = agentconfig.UpdateWithWriter(r.configPath, "updater:hourly_operation", func(cfg *agentconfig.AgentConfig) {
		cfg.Agent.Update = operation
	})
	return operation
}

func (r *updateProgressReporter) setBuilds(targetBuildID string, installedBefore string, installedAfter string) {
	if r == nil || r.configPath == "" {
		return
	}
	_ = agentconfig.UpdateWithWriter(r.configPath, "updater:update_builds", func(cfg *agentconfig.AgentConfig) {
		cfg.Agent.Update.TargetBuildID = agentconfig.NormalizeBuildID(targetBuildID)
		if normalized := agentconfig.NormalizeBuildID(installedBefore); normalized != "" {
			cfg.Agent.Update.InstalledBuildBefore = normalized
		}
		if normalized := agentconfig.NormalizeBuildID(installedAfter); normalized != "" {
			cfg.Agent.Update.InstalledBuildAfter = normalized
		}
		cfg.Agent.Update.UpdatedAt = time.Now().Unix()
	})
}

func (r *updateProgressReporter) emit(phaseID string, parentPhaseID string, state string, summary string, detail string, terminalStatus string) {
	if r == nil || r.configPath == "" {
		return
	}
	now := time.Now()
	event := agentconfig.AgentUpdateEvent{
		PhaseID:        normalizeUpdateProgressID(phaseID),
		ParentPhaseID:  normalizeUpdateProgressID(parentPhaseID),
		State:          normalizeUpdateProgressState(state),
		AgentTimestamp: now.Unix(),
		Summary:        sanitizeUpdateProgressText(summary, 240),
		Detail:         sanitizeUpdateProgressText(detail, 1024),
		TerminalStatus: normalizeUpdateTerminalStatus(terminalStatus),
	}
	var operation agentconfig.AgentUpdateSection
	_ = agentconfig.UpdateWithWriter(r.configPath, "updater:progress", func(cfg *agentconfig.AgentConfig) {
		if strings.TrimSpace(cfg.Agent.Update.OperationID) == "" {
			return
		}
		if event.State != "running" {
			for index := len(cfg.Agent.Update.Events) - 1; index >= 0; index-- {
				previous := cfg.Agent.Update.Events[index]
				if previous.PhaseID == event.PhaseID && previous.State == "running" && previous.AgentTimestamp > 0 {
					event.DurationMS = maxAgentUpdateInt64(now.Sub(time.Unix(previous.AgentTimestamp, 0)).Milliseconds(), 0)
					break
				}
			}
		}
		event.EventID = fmt.Sprintf("%s:%s:%s:%d", cfg.Agent.Update.OperationID, event.PhaseID, event.State, now.UnixNano())
		cfg.Agent.Update.Events = append(cfg.Agent.Update.Events, event)
		if len(cfg.Agent.Update.Events) > 128 {
			cfg.Agent.Update.Events = append([]agentconfig.AgentUpdateEvent(nil), cfg.Agent.Update.Events[len(cfg.Agent.Update.Events)-128:]...)
		}
		cfg.Agent.Update.UpdatedAt = now.Unix()
		if event.TerminalStatus == "failed" || event.TerminalStatus == "timed_out" {
			cfg.Agent.Update.Status = event.TerminalStatus
			cfg.Agent.Update.CompletedAt = now.Unix()
			cfg.Agent.Update.LastError = event.Detail
		}
		operation = cfg.Agent.Update
	})
	if operation.OperationID == "" {
		return
	}
	r.post(operation, event)
}

func maxAgentUpdateInt64(value int64, minimum int64) int64 {
	if value < minimum {
		return minimum
	}
	return value
}

func (r *updateProgressReporter) post(operation agentconfig.AgentUpdateSection, event agentconfig.AgentUpdateEvent) {
	cfg, err := agentconfig.Load(r.configPath)
	if err != nil {
		r.logPostFailure(event, err)
		return
	}
	client, err := auth.NewClient(r.configPath, &cfg, "system")
	if err != nil {
		r.logPostFailure(event, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.EnsureAuthenticated(ctx); err != nil {
		r.logPostFailure(event, err)
		return
	}
	payload := map[string]any{
		"operation_id":           operation.OperationID,
		"scheduled_job_id":       operation.ScheduledJobID,
		"scheduled_job_run_id":   operation.ScheduledJobRunID,
		"source":                 operation.Source,
		"requested_by":           operation.RequestedBy,
		"target_build_id":        operation.TargetBuildID,
		"installed_build_before": operation.InstalledBuildBefore,
		"installed_build_after":  operation.InstalledBuildAfter,
		"event":                  event,
	}
	var response map[string]any
	if _, err := client.PostJSON(ctx, "/api/agent/update/progress", payload, &response); err != nil {
		r.logPostFailure(event, err)
		return
	}
	jobID := updateProgressInt64(response["scheduled_job_id"])
	runID := updateProgressInt64(response["scheduled_job_run_id"])
	if jobID > 0 || runID > 0 {
		_ = agentconfig.UpdateWithWriter(r.configPath, "updater:progress_correlation", func(cfg *agentconfig.AgentConfig) {
			if cfg.Agent.Update.OperationID != operation.OperationID {
				return
			}
			if jobID > 0 {
				cfg.Agent.Update.ScheduledJobID = jobID
			}
			if runID > 0 {
				cfg.Agent.Update.ScheduledJobRunID = runID
			}
		})
	}
}

func (r *updateProgressReporter) logPostFailure(event agentconfig.AgentUpdateEvent, err error) {
	if r != nil && r.logger != nil {
		r.logger.Tracef("Agent update progress post deferred: phase=%s state=%s error=%v", event.PhaseID, event.State, err)
	}
}

func updateOperationIsActive(update agentconfig.AgentUpdateSection) bool {
	if strings.TrimSpace(update.OperationID) == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(update.Status)) {
	case "requested", "config_written", "updater_started", "running", "staging", "restarting", "awaiting_reconnect", "awaiting_health", "verifying", "recovering":
		return true
	default:
		return false
	}
}

func normalizeUpdateProgressID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer(" ", "_", "-", "_").Replace(value)
	if len(value) > 96 {
		value = value[:96]
	}
	return value
}

func normalizeUpdateProgressState(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "pending", "running", "success", "failed", "timed_out", "skipped", "recovering":
		return value
	default:
		return "running"
	}
}

func normalizeUpdateTerminalStatus(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "success", "failed", "timed_out":
		return value
	default:
		return ""
	}
}

func sanitizeUpdateProgressText(value string, limit int) string {
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\x00", ""), "\r", ""))
	lower := strings.ToLower(value)
	for _, secretLabel := range []string{"access_token", "refresh_token", "private_key", "password"} {
		if strings.Contains(lower, secretLabel) {
			return "Sensitive diagnostic detail redacted."
		}
	}
	if limit > 0 && len(value) > limit {
		value = value[:limit]
	}
	return value
}

func newAgentUpdateOperationID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}

func agentUpdateIdentityFingerprint(configPath string) string {
	cfg, err := agentconfig.Load(configPath)
	if err != nil {
		return ""
	}
	snapshot := struct {
		GUID             string `json:"guid"`
		AgentID          string `json:"agent_id"`
		IdentityPublic   string `json:"identity_public"`
		ServerSigningKey string `json:"server_signing_key"`
		EngineCA         string `json:"engine_ca"`
	}{
		GUID:             cfg.Agent.GUID,
		AgentID:          cfg.Agent.AgentID,
		IdentityPublic:   cfg.Identity.PublicKeySPKIB64,
		ServerSigningKey: cfg.Trust.ServerSigningKeySPKIB64,
		EngineCA:         cfg.Trust.EngineCAPEM,
	}
	encoded, _ := json.Marshal(snapshot)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:8])
}

func updateProgressInt64(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	default:
		return 0
	}
}
