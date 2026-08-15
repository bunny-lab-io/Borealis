package localui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	CommandAgentRestart = "agent.restart"
	CommandAgentUpdate  = "agent.update_check"

	StatusFile = "tray-status.json"
	CommandDir = "Commands"
)

type CommandRequest struct {
	Command   string         `json:"command"`
	Params    map[string]any `json:"params,omitempty"`
	ID        string         `json:"id,omitempty"`
	CreatedAt int64          `json:"created_at,omitempty"`
}

type StatusSnapshot struct {
	Hostname          string       `json:"hostname"`
	ServerURL         string       `json:"server_url"`
	AgentID           string       `json:"agent_id,omitempty"`
	BuildID           string       `json:"build_id,omitempty"`
	InstalledBuildID  string       `json:"installed_build_id,omitempty"`
	EngineState       string       `json:"engine_state"`
	LastStatusPhase   string       `json:"last_status_phase,omitempty"`
	LastStatus        string       `json:"last_status,omitempty"`
	LastStatusMessage string       `json:"last_status_message,omitempty"`
	LastStatusAt      int64        `json:"last_status_at,omitempty"`
	LastHeartbeatAt   int64        `json:"last_heartbeat_at,omitempty"`
	Roles             []RoleHealth `json:"roles"`
	Logs              []LogPath    `json:"logs"`
}

type RoleHealth struct {
	RoleID        string `json:"role_id"`
	RoleName      string `json:"role_name"`
	RoleLabel     string `json:"role_label"`
	Context       string `json:"context"`
	Status        string `json:"status"`
	StatusCode    string `json:"status_code"`
	Detail        string `json:"detail"`
	LastCheckedAt int64  `json:"last_checked_at,omitempty"`
}

type LogPath struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

func StateDir(override string) string {
	if strings.TrimSpace(override) != "" {
		return filepath.Clean(override)
	}
	publicDir := strings.TrimSpace(os.Getenv("PUBLIC"))
	if publicDir == "" {
		publicDir = filepath.Join(os.Getenv("SystemDrive")+`\`, "Users", "Public")
	}
	if publicDir == "" || strings.HasPrefix(publicDir, `\`) {
		publicDir = `C:\Users\Public`
	}
	return filepath.Join(publicDir, "Borealis", "CurrentUserHelpers")
}

func StatusPath(stateDir string) string {
	return filepath.Join(StateDir(stateDir), StatusFile)
}

func CommandPath(stateDir string, id string) string {
	return filepath.Join(StateDir(stateDir), CommandDir, strings.TrimSpace(id)+".json")
}

func WriteStatusSnapshot(stateDir string, snapshot StatusSnapshot) error {
	dir := StateDir(stateDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	path := StatusPath(dir)
	tmp := path + "." + randomID() + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func ReadStatusSnapshot(stateDir string) (StatusSnapshot, error) {
	data, err := os.ReadFile(StatusPath(stateDir))
	if err != nil {
		return StatusSnapshot{}, err
	}
	var snapshot StatusSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return StatusSnapshot{}, err
	}
	return snapshot, nil
}

func WriteCommandRequest(stateDir string, request CommandRequest) (string, error) {
	request.Command = strings.TrimSpace(request.Command)
	if request.Command == "" {
		return "", errors.New("command is required")
	}
	switch request.Command {
	case CommandAgentRestart, CommandAgentUpdate:
	default:
		return "", fmt.Errorf("unsupported command %q", request.Command)
	}
	if strings.TrimSpace(request.ID) == "" {
		request.ID = randomID()
	}
	if request.CreatedAt <= 0 {
		request.CreatedAt = time.Now().Unix()
	}
	dir := filepath.Join(StateDir(stateDir), CommandDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	payload, err := json.MarshalIndent(request, "", "  ")
	if err != nil {
		return "", err
	}
	payload = append(payload, '\n')
	path := CommandPath(stateDir, request.ID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return request.ID, nil
}

func ReadCommandRequests(ctx context.Context, stateDir string, maxAge time.Duration) ([]CommandRequest, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	dir := filepath.Join(StateDir(stateDir), CommandDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	now := time.Now()
	requests := []CommandRequest{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err == nil && maxAge > 0 && now.Sub(info.ModTime()) > maxAge {
			_ = os.Remove(path)
			continue
		}
		data, err := os.ReadFile(path)
		_ = os.Remove(path)
		if err != nil {
			continue
		}
		var request CommandRequest
		if err := json.Unmarshal(data, &request); err != nil {
			continue
		}
		request.Command = strings.TrimSpace(request.Command)
		if request.Command == "" {
			continue
		}
		requests = append(requests, request)
	}
	return requests, nil
}

func randomID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
