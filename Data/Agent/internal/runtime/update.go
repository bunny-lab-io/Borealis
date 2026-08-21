package agentruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
	agenttransport "github.com/bunny-lab-io/borealis/go-agent/internal/transport"
)

var startLocalUpdaterForRequest = startLocalUpdater

func (a *Agent) handleUpdateRequest(ctx context.Context, payload any) (any, error) {
	if a == nil {
		return map[string]any{"status": "error", "detail": "agent unavailable"}, nil
	}
	body, _ := payload.(map[string]any)
	if current, err := agentconfig.Load(a.configPath); err == nil && updateOperationActive(current.Agent.Update) {
		return map[string]any{
			"status":       "active",
			"operation_id": current.Agent.Update.OperationID,
			"detail":       "Agent update already underway.",
		}, nil
	}
	operation := a.previewUpdateOperation(
		stringFromPayload(body, "operation_id", "request_id"),
		"update_now",
	)
	operation.Source = stringFromPayload(body, "source")
	if operation.Source == "" {
		operation.Source = "operator_initiated"
	}
	operation.RequestedBy = stringFromPayload(body, "requested_by")
	operation.ScheduledJobID = int64FromPayload(body, "scheduled_job_id", "job_id")
	operation.ScheduledJobRunID = int64FromPayload(body, "scheduled_job_run_id", "run_id")
	stored, err := a.authClient.StoreAgentUpdateOperationDetails(operation)
	if err != nil {
		return map[string]any{"status": "error", "detail": "could not persist update operation"}, nil
	}
	response := map[string]any{
		"status":       "ok",
		"operation_id": stored.OperationID,
	}
	return a.responseWithUpdateAfterAck(response, stored, "update request"), nil
}

func (a *Agent) handleAgentMaintenanceRequest(ctx context.Context, payload any) (any, error) {
	return a.handleUpdateRequest(ctx, payload)
}

func (a *Agent) responseWithUpdateAfterAck(response map[string]any, operation agentconfig.AgentUpdateSection, reason string) any {
	if a == nil {
		return response
	}
	return agenttransport.NewAfterAckResponse(response, func() {
		a.startLocalUpdaterAsync(operation, reason)
	})
}

func (a *Agent) previewUpdateOperation(operationID string, kind string) agentconfig.AgentUpdateSection {
	now := time.Now().Unix()
	if strings.TrimSpace(operationID) == "" {
		operationID = fmt.Sprintf("%d", now)
	}
	return agentconfig.AgentUpdateSection{
		OperationID: strings.TrimSpace(operationID),
		Kind:        strings.TrimSpace(kind),
		Status:      "ack_pending_config",
		StartedAt:   now,
		UpdatedAt:   now,
		DeadlineAt:  now + int64(15*time.Minute/time.Second),
	}
}

func (a *Agent) startLocalUpdaterAsync(operation agentconfig.AgentUpdateSection, reason string) {
	if a == nil {
		return
	}
	configPath := a.configPath
	logger := a.logger
	go func() {
		if logger != nil {
			logger.Printf("update operation starting after %s ack operation_id=%s", reason, operation.OperationID)
		}
		if err := startLocalUpdaterForRequest(configPath); err != nil {
			markUpdateOperationStatus(configPath, "failed", err.Error())
			if logger != nil {
				logger.Printf("local updater start failed after %s: %v", reason, err)
			}
			return
		}
		markUpdateOperationStatus(configPath, "updater_started", "")
	}()
}

func int64FromPayload(payload map[string]any, keys ...string) int64 {
	for _, key := range keys {
		value, exists := payload[key]
		if !exists {
			continue
		}
		switch typed := value.(type) {
		case int64:
			return typed
		case int:
			return int64(typed)
		case float64:
			return int64(typed)
		}
	}
	return 0
}

func stringFromPayload(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if payload == nil {
			return ""
		}
		value, exists := payload[key]
		if !exists {
			continue
		}
		switch typed := value.(type) {
		case string:
			if text := strings.TrimSpace(typed); text != "" {
				return text
			}
		case fmt.Stringer:
			if text := strings.TrimSpace(typed.String()); text != "" {
				return text
			}
		default:
			if text := strings.TrimSpace(fmt.Sprint(value)); text != "" && text != "<nil>" {
				return text
			}
		}
	}
	return ""
}

func removeUpdateStatusFile(configPath string) {
	base := filepath.Dir(strings.TrimSpace(configPath))
	if base == "." || base == "" {
		base, _ = os.Getwd()
	}
	_ = os.Remove(filepath.Join(base, "Updater", "update_status.json"))
}

func writeInstalledBuildID(configPath string, buildID string) error {
	removeUpdateStatusFile(configPath)
	buildID = agentconfig.NormalizeBuildID(buildID)
	if buildID == "" || strings.EqualFold(buildID, "dev") {
		return nil
	}
	cfg, err := agentconfig.LoadOrCreate(configPath)
	if err != nil {
		return err
	}
	cfg.Agent.InstalledBuildID = buildID
	if updateOperationCanEnterHealthGate(cfg.Agent.Update) {
		now := time.Now().Unix()
		cfg.Agent.Update.Status = "awaiting_health"
		cfg.Agent.Update.UpdatedAt = now
		cfg.Agent.Update.CompletedAt = 0
		cfg.Agent.Update.LastError = ""
		cfg.Agent.Update.InstalledBuildAfter = buildID
	}
	if err := agentconfig.Save(configPath, &cfg); err != nil {
		return err
	}
	return nil
}

func updateOperationCanEnterHealthGate(update agentconfig.AgentUpdateSection) bool {
	if strings.TrimSpace(update.OperationID) == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(update.Status)) {
	case "staging", "restarting", "awaiting_reconnect", "awaiting_health", "verifying", "recovering":
		return true
	default:
		return false
	}
}

func markUpdateOperationStatus(configPath string, status string, detail string) {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		return
	}
	_ = agentconfig.UpdateWithWriter(configPath, "runtime:update_operation", func(cfg *agentconfig.AgentConfig) {
		if strings.TrimSpace(cfg.Agent.Update.OperationID) == "" {
			return
		}
		now := time.Now().Unix()
		cfg.Agent.Update.Status = status
		cfg.Agent.Update.UpdatedAt = now
		cfg.Agent.Update.LastError = strings.TrimSpace(detail)
		if status == "success" || status == "failed" {
			cfg.Agent.Update.CompletedAt = now
		}
	})
}
