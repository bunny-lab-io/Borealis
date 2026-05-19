package agentruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type heartbeatSuccessMarker struct {
	Hostname  string `json:"hostname"`
	Timestamp int64  `json:"timestamp"`
}

func heartbeatSuccessMarkerPath(configPath string) string {
	root := filepath.Dir(configPath)
	return filepath.Join(root, "Logs", "Agent", "heartbeat-success.json")
}

func (a *Agent) recordHeartbeatSuccess() {
	if a == nil {
		return
	}
	path := heartbeatSuccessMarkerPath(a.configPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		if a.logger != nil {
			a.logger.Printf("heartbeat marker directory failed: %v", err)
		}
		return
	}
	payload, err := json.MarshalIndent(heartbeatSuccessMarker{
		Hostname:  a.hostname,
		Timestamp: time.Now().Unix(),
	}, "", "  ")
	if err != nil {
		if a.logger != nil {
			a.logger.Printf("heartbeat marker encode failed: %v", err)
		}
		return
	}
	payload = append(payload, '\n')
	tmp := path + "." + time.Now().Format("20060102150405.000000000") + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		if a.logger != nil {
			a.logger.Printf("heartbeat marker write failed: %v", err)
		}
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		if a.logger != nil {
			a.logger.Printf("heartbeat marker rename failed: %v", err)
		}
	}
}
