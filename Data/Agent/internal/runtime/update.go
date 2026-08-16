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
	operation := a.previewUpdateOperation(
		stringFromPayload(body, "operation_id", "request_id"),
		"update_now",
	)
	response := map[string]any{
		"status":       "ok",
		"operation_id": operation.OperationID,
	}
	return a.responseWithUpdateAfterAck(response, operation, "update request"), nil
}

func (a *Agent) handleAgentMaintenanceRequest(ctx context.Context, payload any) (any, error) {
	return a.handleUpdateRequest(ctx, payload)
}

func (a *Agent) responseWithUpdateAfterAck(response map[string]any, operation agentconfig.AgentUpdateSection, reason string) any {
	if a == nil {
		return response
	}
	return agenttransport.NewAfterAckResponse(response, func() {
		a.storeUpdateAndStartLocalUpdaterAsync(operation, reason)
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

func (a *Agent) storeUpdateAndStartLocalUpdaterAsync(operation agentconfig.AgentUpdateSection, reason string) {
	if a == nil {
		return
	}
	configPath := a.configPath
	logger := a.logger
	authClient := a.authClient
	go func() {
		stored, err := authClient.StoreAgentUpdateOperation(
			operation.OperationID,
			operation.Kind,
		)
		if err != nil {
			if logger != nil {
				logger.Printf("update operation state failed after %s ack: %v", reason, err)
			}
			return
		}
		if logger != nil {
			logger.Printf("update operation stored after %s ack operation_id=%s", reason, stored.OperationID)
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
	if cfg.Agent.Update.OperationID != "" {
		now := time.Now().Unix()
		cfg.Agent.Update.Status = "success"
		cfg.Agent.Update.UpdatedAt = now
		cfg.Agent.Update.CompletedAt = now
		cfg.Agent.Update.LastError = ""
	}
	if err := agentconfig.Save(configPath, &cfg); err != nil {
		return err
	}
	return nil
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
