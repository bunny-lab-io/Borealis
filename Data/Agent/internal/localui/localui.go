package localui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	BrokerStateFile = "ui-broker.json"
	TokenHeader     = "X-Borealis-UI-Token"

	CommandStatusGet       = "status.get"
	CommandAgentRestart    = "agent.restart"
	CommandAgentUpdate     = "agent.update_check"
	CommandDiagnosticsCopy = "diagnostics.copy_summary"
)

type BrokerState struct {
	URL       string `json:"url"`
	Token     string `json:"token"`
	UpdatedAt int64  `json:"updated_at"`
}

type CommandRequest struct {
	Command string         `json:"command"`
	Params  map[string]any `json:"params,omitempty"`
}

type CommandResponse struct {
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	Data   any    `json:"data,omitempty"`
}

type StatusSnapshot struct {
	Hostname          string       `json:"hostname"`
	ServerURL         string       `json:"server_url"`
	AgentID           string       `json:"agent_id,omitempty"`
	BuildID           string       `json:"build_id,omitempty"`
	InstalledBuildID  string       `json:"installed_build_id,omitempty"`
	ReleaseChannel    string       `json:"release_channel,omitempty"`
	Branch            string       `json:"branch,omitempty"`
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

func BrokerStatePath(stateDir string) string {
	return filepath.Join(StateDir(stateDir), BrokerStateFile)
}

func WriteBrokerState(stateDir string, state BrokerState) error {
	state.URL = strings.TrimSpace(state.URL)
	state.Token = strings.TrimSpace(state.Token)
	if state.URL == "" || state.Token == "" {
		return errors.New("broker URL and token are required")
	}
	if state.UpdatedAt <= 0 {
		state.UpdatedAt = time.Now().Unix()
	}
	dir := StateDir(stateDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	path := BrokerStatePath(dir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func ReadBrokerState(stateDir string) (BrokerState, error) {
	data, err := os.ReadFile(BrokerStatePath(stateDir))
	if err != nil {
		return BrokerState{}, err
	}
	var state BrokerState
	if err := json.Unmarshal(data, &state); err != nil {
		return BrokerState{}, err
	}
	state.URL = strings.TrimRight(strings.TrimSpace(state.URL), "/")
	state.Token = strings.TrimSpace(state.Token)
	if state.URL == "" || state.Token == "" {
		return BrokerState{}, errors.New("broker state is incomplete")
	}
	return state, nil
}

func DoCommand(ctx context.Context, httpClient *http.Client, stateDir string, request CommandRequest) (CommandResponse, error) {
	state, err := ReadBrokerState(stateDir)
	if err != nil {
		return CommandResponse{}, err
	}
	return DoCommandWithState(ctx, httpClient, state, request)
}

func DoCommandWithState(ctx context.Context, httpClient *http.Client, state BrokerState, request CommandRequest) (CommandResponse, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	request.Command = strings.TrimSpace(request.Command)
	if request.Command == "" {
		return CommandResponse{}, errors.New("command is required")
	}
	body, err := json.Marshal(request)
	if err != nil {
		return CommandResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(state.URL, "/")+"/command", bytes.NewReader(body))
	if err != nil {
		return CommandResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set(TokenHeader, strings.TrimSpace(state.Token))
	resp, err := httpClient.Do(req)
	if err != nil {
		return CommandResponse{}, err
	}
	defer resp.Body.Close()
	var response CommandResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return CommandResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if response.Detail == "" {
			response.Detail = fmt.Sprintf("broker HTTP %d", resp.StatusCode)
		}
		return response, errors.New(response.Detail)
	}
	return response, nil
}

func DiagnosticsText(snapshot StatusSnapshot) string {
	var out strings.Builder
	write := func(format string, args ...any) {
		out.WriteString(fmt.Sprintf(format, args...))
		out.WriteByte('\n')
	}
	write("Borealis Agent Diagnostics")
	write("Hostname: %s", emptyAs(snapshot.Hostname, "Unknown"))
	write("Engine: %s", emptyAs(snapshot.ServerURL, "Unknown"))
	write("Agent ID: %s", emptyAs(snapshot.AgentID, "Pending"))
	write("Build: %s", emptyAs(firstNonEmpty(snapshot.InstalledBuildID, snapshot.BuildID), "Unknown"))
	write("Release: %s / %s", emptyAs(snapshot.ReleaseChannel, "stable"), emptyAs(snapshot.Branch, "main"))
	write("Engine State: %s", emptyAs(snapshot.EngineState, "Unknown"))
	if snapshot.LastHeartbeatAt > 0 {
		write("Last Heartbeat: %s", time.Unix(snapshot.LastHeartbeatAt, 0).Format(time.RFC3339))
	}
	if snapshot.LastStatusAt > 0 {
		write("Last Status: %s %s %s", snapshot.LastStatusPhase, snapshot.LastStatus, time.Unix(snapshot.LastStatusAt, 0).Format(time.RFC3339))
	}
	write("")
	write("Role Health:")
	for _, role := range snapshot.Roles {
		write("- %s [%s]: %s", emptyAs(role.RoleLabel, role.RoleName), emptyAs(role.Context, "system"), emptyAs(firstNonEmpty(role.StatusCode, role.Status), "unknown"))
		if strings.TrimSpace(role.Detail) != "" {
			write("  %s", role.Detail)
		}
	}
	write("")
	write("Logs:")
	for _, logPath := range snapshot.Logs {
		if strings.TrimSpace(logPath.Path) != "" {
			write("- %s: %s", emptyAs(logPath.Label, "Log"), logPath.Path)
		}
	}
	return out.String()
}

func emptyAs(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
