package agentruntime

import (
	"fmt"
	"strings"
	"time"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
)

func reconcileUpdateWithRoleHealth(configPath string, roleHealth map[string]any, buildID string) {
	if strings.TrimSpace(configPath) == "" {
		return
	}
	roles := updateRoleMaps(roleHealth["roles"])
	if len(roles) == 0 {
		return
	}
	_ = agentconfig.UpdateWithWriter(configPath, "runtime:update_health", func(cfg *agentconfig.AgentConfig) {
		if !updateOperationAwaitingHealth(cfg.Agent.Update) {
			return
		}
		now := time.Now().Unix()
		appendAgentUpdateEvent(&cfg.Agent.Update, agentconfig.AgentUpdateEvent{
			PhaseID:        "waiting_agent_reconnection",
			State:          "success",
			AgentTimestamp: now,
			Summary:        "Agent Reconnected",
			Detail:         "Matching Agent operation resumed after runtime restart.",
		})
		appendAgentUpdateEvent(&cfg.Agent.Update, agentconfig.AgentUpdateEvent{
			PhaseID:        "verifying_post_update_health",
			State:          "running",
			AgentTimestamp: now,
			Summary:        "Verifying Post-Update Health",
			Detail:         "Checking required Agent roles and managed services.",
		})

		allHealthy := true
		for _, role := range roles {
			roleID := strings.TrimSpace(fmt.Sprint(role["role_id"]))
			if roleID == "" {
				continue
			}
			statusCode := strings.ToLower(strings.TrimSpace(fmt.Sprint(role["status_code"])))
			state := "pending"
			switch statusCode {
			case "healthy", "loaded":
				state = "success"
			case "unsupported", "not_applicable", "no_desktop_environment_active":
				state = "skipped"
			default:
				allHealthy = false
				if statusCode == "recovering" || statusCode == "stale" {
					state = "recovering"
				} else if statusCode == "unhealthy" || statusCode == "failed" {
					state = "failed"
				}
			}
			label := strings.TrimSpace(fmt.Sprint(role["role_label"]))
			if label == "" {
				label = roleID
			}
			appendAgentUpdateEvent(&cfg.Agent.Update, agentconfig.AgentUpdateEvent{
				PhaseID:        "role:" + roleID,
				ParentPhaseID:  "verifying_post_update_health",
				State:          state,
				AgentTimestamp: now,
				Summary:        label,
				Detail:         strings.TrimSpace(fmt.Sprint(role["detail"])),
				RetryCount:     intFromAny(role["recovery_attempts"]),
			})
		}
		cfg.Agent.Update.UpdatedAt = now
		cfg.Agent.Update.InstalledBuildAfter = agentconfig.NormalizeBuildID(firstNonEmpty(buildID, cfg.Agent.InstalledBuildID))
		if allHealthy {
			cfg.Agent.Update.Status = "success"
			cfg.Agent.Update.CompletedAt = now
			cfg.Agent.Update.LastError = ""
			appendAgentUpdateEvent(&cfg.Agent.Update, agentconfig.AgentUpdateEvent{
				PhaseID:        "verifying_post_update_health",
				State:          "success",
				AgentTimestamp: now,
				Summary:        "Post-Update Health Verified",
				Detail:         "All required applicable Agent roles report healthy.",
			})
			appendAgentUpdateEvent(&cfg.Agent.Update, agentconfig.AgentUpdateEvent{
				PhaseID:        "update_completed",
				State:          "success",
				AgentTimestamp: now,
				Summary:        "Update Completed",
				Detail:         "Agent reconnected and required role health passed.",
				TerminalStatus: "success",
			})
			return
		}
		if cfg.Agent.Update.DeadlineAt > 0 && now > cfg.Agent.Update.DeadlineAt {
			cfg.Agent.Update.Status = "timed_out"
			cfg.Agent.Update.CompletedAt = now
			cfg.Agent.Update.LastError = "Required Agent roles did not become healthy before update deadline."
			appendAgentUpdateEvent(&cfg.Agent.Update, agentconfig.AgentUpdateEvent{
				PhaseID:        "update_completed",
				State:          "timed_out",
				AgentTimestamp: now,
				Summary:        "Update Timed Out",
				Detail:         cfg.Agent.Update.LastError,
				TerminalStatus: "timed_out",
			})
			return
		}
		cfg.Agent.Update.Status = "awaiting_health"
	})
}

func updateOperationAwaitingHealth(update agentconfig.AgentUpdateSection) bool {
	if strings.TrimSpace(update.OperationID) == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(update.Status)) {
	case "awaiting_reconnect", "awaiting_health", "verifying":
		return true
	default:
		return false
	}
}

func appendAgentUpdateEvent(update *agentconfig.AgentUpdateSection, event agentconfig.AgentUpdateEvent) {
	if update == nil || strings.TrimSpace(event.PhaseID) == "" {
		return
	}
	if event.AgentTimestamp <= 0 {
		event.AgentTimestamp = time.Now().Unix()
	}
	for index := len(update.Events) - 1; index >= 0; index-- {
		last := update.Events[index]
		if last.PhaseID != event.PhaseID {
			continue
		}
		if last.State == event.State && last.Summary == event.Summary && last.Detail == event.Detail {
			return
		}
		break
	}
	event.EventID = fmt.Sprintf("%s:%s:%s:%d", update.OperationID, event.PhaseID, event.State, event.AgentTimestamp)
	update.Events = append(update.Events, event)
	if len(update.Events) > 128 {
		update.Events = append([]agentconfig.AgentUpdateEvent(nil), update.Events[len(update.Events)-128:]...)
	}
}

func updateRoleMaps(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		return typed
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if mapped, ok := item.(map[string]any); ok {
				out = append(out, mapped)
			}
		}
		return out
	default:
		return nil
	}
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}
