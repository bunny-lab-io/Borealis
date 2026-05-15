package agentruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
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
	status := map[string]any{
		"state":           "requested",
		"last_checked_at": time.Now().Unix(),
		"last_source":     "socket_request",
	}
	_ = writeUpdateStatusFile(a.configPath, status)
	return map[string]any{"status": "ok"}, nil
}

func updaterDir(configPath string) string {
	base := filepath.Dir(strings.TrimSpace(configPath))
	if base == "." || base == "" {
		base, _ = os.Getwd()
	}
	return filepath.Join(base, "Updater")
}

func writeInstalledBuildID(configPath string, buildID string) error {
	buildID = strings.TrimSpace(strings.ToLower(buildID))
	if buildID == "" || strings.EqualFold(buildID, "dev") {
		return nil
	}
	path := filepath.Join(updaterDir(configPath), "installed_build_id.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(buildID), 0o644)
}

func readUpdateStatus(configPath string, buildID string) map[string]any {
	status := map[string]any{
		"state":              "idle",
		"installed_build_id": strings.TrimSpace(strings.ToLower(buildID)),
		"last_source":        "agent_runtime",
	}
	path := filepath.Join(updaterDir(configPath), "update_status.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return status
	}
	var stored map[string]any
	if err := json.Unmarshal(data, &stored); err != nil {
		status["last_error"] = err.Error()
		return status
	}
	for key, value := range stored {
		status[key] = value
	}
	if status["installed_build_id"] == "" && strings.TrimSpace(buildID) != "" {
		status["installed_build_id"] = strings.TrimSpace(strings.ToLower(buildID))
	}
	return status
}

func writeUpdateStatusFile(configPath string, values map[string]any) error {
	path := filepath.Join(updaterDir(configPath), "update_status.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	current := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &current)
	}
	for key, value := range values {
		current[key] = value
	}
	data, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
