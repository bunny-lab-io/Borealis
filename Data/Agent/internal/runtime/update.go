package agentruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
)

func (a *Agent) handleUpdateRequest(ctx context.Context, payload any) (any, error) {
	if a == nil {
		return map[string]any{"status": "error", "detail": "agent unavailable"}, nil
	}
	if err := startLocalUpdater(a.configPath); err != nil {
		a.logger.Printf("local updater start failed: %v", err)
		return map[string]any{
			"status": "error",
			"detail": err.Error(),
		}, nil
	}
	return map[string]any{"status": "ok"}, nil
}

func (a *Agent) handleReleaseChannelChanged(ctx context.Context, payload any) (any, error) {
	if a == nil {
		return map[string]any{"status": "error", "detail": "agent unavailable"}, nil
	}
	body, _ := payload.(map[string]any)
	releaseChannel := releaseChannelFromPayload(body)
	branch := stringFromPayload(body, "branch", "repo_ref", "repo_branch")
	if releaseChannel == "" {
		return map[string]any{"status": "error", "detail": "release_channel missing"}, nil
	}
	if err := a.authClient.StoreAgentReleaseTarget(releaseChannel, branch); err != nil {
		a.logger.Printf("release channel update failed: %v", err)
		return map[string]any{"status": "error", "detail": err.Error()}, nil
	}
	a.logger.Printf("release channel updated release_channel=%s branch=%s", releaseChannel, branch)
	return map[string]any{
		"status":          "ok",
		"release_channel": releaseChannel,
		"branch":          branch,
	}, nil
}

func releaseChannelFromPayload(payload map[string]any) string {
	releaseChannel := stringFromPayload(payload, "release_channel")
	if releaseChannel != "" {
		return agentconfig.NormalizeReleaseChannel(releaseChannel)
	}
	effective := strings.ToLower(stringFromPayload(payload, "effective_channel", "target_channel", "channel"))
	switch effective {
	case "unstable", "source", "branch":
		return agentconfig.ReleaseChannelSource
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
	if agentconfig.UsesSourceReleaseChannel(cfg.Agent.ReleaseChannel) {
		currentBuildID := agentconfig.NormalizeBuildID(cfg.Agent.InstalledBuildID)
		if currentBuildID != "" && !strings.EqualFold(currentBuildID, buildID) {
			return nil
		}
	}
	cfg.Agent.InstalledBuildID = buildID
	if err := agentconfig.Save(configPath, &cfg); err != nil {
		return err
	}
	return nil
}
