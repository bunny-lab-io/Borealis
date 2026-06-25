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
		"",
		"",
	)
	response := map[string]any{
		"status":          "ok",
		"operation_id":    operation.OperationID,
		"release_channel": operation.TargetChannel,
		"branch":          operation.TargetBranch,
	}
	return a.responseWithUpdateAfterAck(response, operation, "update request"), nil
}

func (a *Agent) handleReleaseChannelChanged(ctx context.Context, payload any) (any, error) {
	return a.handleAgentMaintenanceRequest(ctx, payload)
}

func (a *Agent) handleAgentMaintenanceRequest(ctx context.Context, payload any) (any, error) {
	if a == nil {
		return map[string]any{"status": "error", "detail": "agent unavailable"}, nil
	}
	body, _ := payload.(map[string]any)
	releaseChannel := releaseChannelFromPayload(body)
	branch := stringFromPayload(body, "branch", "repo_ref", "repo_branch")
	if releaseChannel == "" {
		return map[string]any{"status": "error", "detail": "release_channel missing"}, nil
	}
	kind := stringFromPayload(body, "kind", "action")
	if kind == "" {
		kind = "switch_branch_channel"
	}
	operation := a.previewUpdateOperation(
		stringFromPayload(body, "operation_id", "request_id"),
		kind,
		releaseChannel,
		branch,
	)
	response := map[string]any{
		"status":          "ok",
		"operation_id":    operation.OperationID,
		"release_channel": operation.TargetChannel,
		"branch":          operation.TargetBranch,
	}
	return a.responseWithUpdateAfterAck(response, operation, "release channel change"), nil
}

func (a *Agent) responseWithUpdateAfterAck(response map[string]any, operation agentconfig.AgentUpdateSection, reason string) any {
	if a == nil {
		return response
	}
	return agenttransport.NewAfterAckResponse(response, func() {
		a.storeUpdateAndStartLocalUpdaterAsync(operation, reason)
	})
}

func (a *Agent) previewUpdateOperation(operationID string, kind string, releaseChannel string, branch string) agentconfig.AgentUpdateSection {
	now := time.Now().Unix()
	if strings.TrimSpace(operationID) == "" {
		operationID = fmt.Sprintf("%d", now)
	}
	channel := ""
	if strings.TrimSpace(releaseChannel) != "" {
		channel = agentconfig.NormalizeReleaseChannel(releaseChannel)
	}
	targetBranch := ""
	if strings.TrimSpace(branch) != "" {
		targetBranch = agentconfig.NormalizeBranch(branch)
	}
	if channel == agentconfig.ReleaseChannelStable {
		targetBranch = agentconfig.DefaultBranch
	}
	if channel != "" && targetBranch == "" {
		targetBranch = agentconfig.DefaultBranch
	}
	return agentconfig.AgentUpdateSection{
		OperationID:   strings.TrimSpace(operationID),
		Kind:          strings.TrimSpace(kind),
		Status:        "ack_pending_config",
		StartedAt:     now,
		UpdatedAt:     now,
		DeadlineAt:    now + int64(15*time.Minute/time.Second),
		TargetChannel: channel,
		TargetBranch:  targetBranch,
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
		if strings.TrimSpace(operation.TargetChannel) == "" || strings.TrimSpace(operation.TargetBranch) == "" {
			cfg := authClient.Config()
			if strings.TrimSpace(operation.TargetChannel) == "" {
				operation.TargetChannel = cfg.Agent.ReleaseChannel
			}
			if strings.TrimSpace(operation.TargetBranch) == "" {
				operation.TargetBranch = cfg.Agent.Branch
			}
		}
		stored, err := authClient.StoreAgentUpdateOperation(
			operation.OperationID,
			operation.Kind,
			operation.TargetChannel,
			operation.TargetBranch,
		)
		if err != nil {
			if logger != nil {
				logger.Printf("update operation state failed after %s ack: %v", reason, err)
			}
			return
		}
		if logger != nil {
			logger.Printf("update operation stored after %s ack release_channel=%s branch=%s operation_id=%s", reason, stored.TargetChannel, stored.TargetBranch, stored.OperationID)
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

func releaseChannelFromPayload(payload map[string]any) string {
	releaseChannel := stringFromPayload(payload, "release_channel")
	if releaseChannel != "" {
		return agentconfig.NormalizeReleaseChannel(releaseChannel)
	}
	effective := strings.ToLower(stringFromPayload(payload, "effective_channel", "target_channel", "channel"))
	switch effective {
	case "unstable", "source", "branch":
		return agentconfig.ReleaseChannelUnstable
	case "stable", "release", "releases":
		return agentconfig.ReleaseChannelStable
	case "":
		return ""
	default:
		return agentconfig.NormalizeReleaseChannel(effective)
	}
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
