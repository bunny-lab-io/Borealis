package agentruntime

import (
	"context"
	"strings"
)

func (a *Agent) handleRemoteShellRestart(ctx context.Context, payload any) (any, error) {
	return a.handleRemoteShellRestartWithCompletion(ctx, payload, nil)
}

func (a *Agent) handleRemoteShellRestartWithCompletion(ctx context.Context, payload any, complete func(error)) (any, error) {
	reject := func(code string) (any, error) {
		if complete == nil {
			return map[string]any{"status": "error", "error": code}, nil
		}
		return map[string]any{"ok": false, "error": code}, nil
	}
	if a == nil || a.remoteShell == nil {
		return reject("remote_shell_unavailable")
	}
	body, _ := payload.(map[string]any)
	requestedAgentID := strings.TrimSpace(stringFromPayload(body, "agent_id"))
	if requestedAgentID != "" && a.authClient != nil && !strings.EqualFold(requestedAgentID, a.authClient.AgentID()) {
		return reject("not_for_agent")
	}
	reason := strings.TrimSpace(stringFromPayload(body, "reason", "restart_reason"))
	if reason == "" {
		reason = "remote_shell_backend_unreachable"
	}
	a.remoteShell.RestartWithCompletion(ctx, reason, complete)
	if complete == nil {
		return map[string]any{"status": "ok", "reason": reason}, nil
	}
	return map[string]any{"ok": true, "status": "accepted", "reason": reason}, nil
}
