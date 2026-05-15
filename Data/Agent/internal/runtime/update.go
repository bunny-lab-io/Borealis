package agentruntime

import (
	"context"
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
	if err := agentconfig.Save(configPath, &cfg); err != nil {
		return err
	}
	return nil
}
