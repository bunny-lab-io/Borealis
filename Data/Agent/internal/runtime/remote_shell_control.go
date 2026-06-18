package agentruntime

import (
	"context"
	"strings"
)

func (a *Agent) handleRemoteShellRestart(ctx context.Context, payload any) (any, error) {
	if a == nil || a.remoteShell == nil {
		return map[string]any{"status": "error", "error": "remote_shell_unavailable"}, nil
	}
	body, _ := payload.(map[string]any)
	requestedAgentID := strings.TrimSpace(stringFromPayload(body, "agent_id"))
	if requestedAgentID != "" && a.authClient != nil && !strings.EqualFold(requestedAgentID, a.authClient.AgentID()) {
		return map[string]any{"status": "error", "error": "not_for_agent"}, nil
	}
	reason := strings.TrimSpace(stringFromPayload(body, "reason", "restart_reason"))
	if reason == "" {
		reason = "remote_shell_backend_unreachable"
	}
	a.remoteShell.Restart(context.Background(), reason)
	return map[string]any{"status": "ok", "reason": reason}, nil
}
