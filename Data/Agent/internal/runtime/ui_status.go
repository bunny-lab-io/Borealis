package agentruntime

import (
	"context"
	"fmt"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
	"github.com/bunny-lab-io/borealis/go-agent/internal/localui"
)

func (a *Agent) startTrayBridgeIfSupported(ctx context.Context) {
	if a == nil || goruntime.GOOS != "windows" || a.options.Once {
		return
	}
	a.writeUIStatusSnapshot()
	go a.trayStatusLoop(ctx)
	go a.trayCommandLoop(ctx)
	a.logger.Printf("local tray status bridge started state_dir=%s", localui.StateDir(""))
}

func (a *Agent) trayStatusLoop(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.writeUIStatusSnapshot()
		}
	}
}

func (a *Agent) trayCommandLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			requests, err := localui.ReadCommandRequests(ctx, "", 2*time.Minute)
			if err != nil {
				a.logger.Printf("tray command read failed: %v", err)
				continue
			}
			for _, request := range requests {
				switch strings.TrimSpace(request.Command) {
				case localui.CommandAgentUpdate:
					if err := startLocalUpdater(a.configPath); err != nil {
						a.logger.Printf("tray update request failed: %v", err)
					}
				case localui.CommandAgentRestart:
					if err := restartLocalAgent(); err != nil {
						a.logger.Printf("tray restart request failed: %v", err)
					}
				}
			}
		}
	}
}

func (a *Agent) UISnapshot() localui.StatusSnapshot {
	a.uiMu.RLock()
	defer a.uiMu.RUnlock()
	snapshot := a.uiSnapshot
	snapshot.Roles = append([]localui.RoleHealth(nil), snapshot.Roles...)
	snapshot.Logs = append([]localui.LogPath(nil), snapshot.Logs...)
	return snapshot
}

func (a *Agent) updateUIStatus(phase string, status string, message string) {
	if a == nil {
		return
	}
	now := time.Now().Unix()
	cfg := a.authClient.Config()
	a.uiMu.Lock()
	a.applyUIConfigLocked(cfg)
	a.uiSnapshot.LastStatusPhase = strings.TrimSpace(phase)
	a.uiSnapshot.LastStatus = strings.TrimSpace(status)
	a.uiSnapshot.LastStatusMessage = strings.TrimSpace(message)
	a.uiSnapshot.LastStatusAt = now
	a.uiSnapshot.EngineState = engineStateFromStatus(status, phase)
	a.uiMu.Unlock()
	a.writeUIStatusSnapshot()
	return
}

func (a *Agent) updateUIHeartbeat(payload map[string]any) {
	if a == nil {
		return
	}
	cfg := a.authClient.Config()
	a.uiMu.Lock()
	a.applyUIConfigLocked(cfg)
	a.uiSnapshot.LastHeartbeatAt = time.Now().Unix()
	a.uiSnapshot.EngineState = "Online"
	a.uiSnapshot.Roles = rolesFromHeartbeat(payload)
	a.uiMu.Unlock()
	a.writeUIStatusSnapshot()
	return
}

func (a *Agent) writeUIStatusSnapshot() {
	if a == nil || goruntime.GOOS != "windows" {
		return
	}
	if err := localui.WriteStatusSnapshot("", a.UISnapshot()); err != nil && a.logger != nil {
		a.logger.Printf("tray status write failed: %v", err)
	}
}

func (a *Agent) applyUIConfigLocked(cfg agentconfig.AgentConfig) {
	a.uiSnapshot.Hostname = strings.TrimSpace(a.hostname)
	a.uiSnapshot.ServerURL = agentconfig.NormalizeServerURL(cfg.ServerURL)
	a.uiSnapshot.AgentID = strings.TrimSpace(cfg.Agent.AgentID)
	a.uiSnapshot.BuildID = strings.TrimSpace(a.options.BuildID)
	a.uiSnapshot.InstalledBuildID = agentconfig.NormalizeBuildID(cfg.Agent.InstalledBuildID)
	if a.uiSnapshot.InstalledBuildID == "" {
		a.uiSnapshot.InstalledBuildID = agentconfig.NormalizeBuildID(a.options.BuildID)
	}
	a.uiSnapshot.ReleaseChannel = agentconfig.NormalizeReleaseChannel(cfg.Agent.ReleaseChannel)
	a.uiSnapshot.Branch = agentconfig.NormalizeBranch(cfg.Agent.Branch)
	a.uiSnapshot.Logs = safeLogPaths(a.configPath)
	if a.uiSnapshot.EngineState == "" {
		a.uiSnapshot.EngineState = "Starting"
	}
}

func rolesFromHeartbeat(payload map[string]any) []localui.RoleHealth {
	rawHealth, ok := payload["agent_role_health"].(map[string]any)
	if !ok {
		return nil
	}
	rawRoles, ok := rawHealth["roles"].([]map[string]any)
	if !ok {
		if anyRoles, anyOK := rawHealth["roles"].([]any); anyOK {
			roles := make([]localui.RoleHealth, 0, len(anyRoles))
			for _, item := range anyRoles {
				if mapped, mappedOK := item.(map[string]any); mappedOK {
					roles = append(roles, roleFromMap(mapped))
				}
			}
			return roles
		}
		return nil
	}
	roles := make([]localui.RoleHealth, 0, len(rawRoles))
	for _, item := range rawRoles {
		roles = append(roles, roleFromMap(item))
	}
	return roles
}

func roleFromMap(item map[string]any) localui.RoleHealth {
	return localui.RoleHealth{
		RoleID:        textValue(item["role_id"]),
		RoleName:      textValue(item["role_name"]),
		RoleLabel:     textValue(item["role_label"]),
		Context:       textValue(item["context"]),
		Status:        textValue(item["status"]),
		StatusCode:    textValue(item["status_code"]),
		Detail:        textValue(item["detail"]),
		LastCheckedAt: int64Value(item["last_checked_at"]),
	}
}

func safeLogPaths(configPath string) []localui.LogPath {
	root := filepath.Dir(strings.TrimSpace(configPath))
	if root == "." || root == "" {
		return nil
	}
	return []localui.LogPath{
		{Label: "Agent", Path: filepath.Join(root, "Logs", "Agent", "agent.log")},
		{Label: "Bootstrap", Path: filepath.Join(root, "Logs", "Agent", "bootstrap.log")},
		{Label: "Updater", Path: filepath.Join(root, "Logs", "Agent", "updater.log")},
		{Label: "Remote Shell", Path: filepath.Join(root, "Logs", "Agent", "remote_shell.log")},
		{Label: "WireGuard", Path: filepath.Join(root, "Logs", "WireGuard", "wireguard.log")},
		{Label: "UltraVNC", Path: filepath.Join(root, "Logs", "UltraVNC", "vnc.log")},
	}
}

func engineStateFromStatus(status string, phase string) string {
	normalized := strings.ToLower(strings.TrimSpace(status))
	switch normalized {
	case "healthy", "ok", "complete":
		return "Online"
	case "unhealthy", "failed":
		return "Unhealthy"
	case "degraded", "recovering":
		return "Recovering"
	default:
		if strings.Contains(strings.ToLower(phase), "socket") {
			return "Connecting"
		}
		return "Starting"
	}
}

func textValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	default:
		if value == nil {
			return ""
		}
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func int64Value(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case jsonNumber:
		out, _ := typed.Int64()
		return out
	default:
		return 0
	}
}

type jsonNumber interface {
	Int64() (int64, error)
}
