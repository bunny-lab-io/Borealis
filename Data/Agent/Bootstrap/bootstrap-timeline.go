//go:build windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type StatePayload struct {
	JobID                int    `json:"job_id,omitempty"`
	RunID                int    `json:"run_id,omitempty"`
	Target               string `json:"target,omitempty"`
	Hostname             string `json:"hostname,omitempty"`
	RepoRef              string `json:"repo_ref,omitempty"`
	ServerURL            string `json:"server_url,omitempty"`
	EnrollmentCodeSHA256 string `json:"enrollment_code_sha256,omitempty"`
	Status               string `json:"status"`
	Phase                string `json:"phase,omitempty"`
	ExitCode             int    `json:"exit_code"`
	Detail               string `json:"detail"`
	UpdatedAt            string `json:"updated_at"`
}

type EventPayload struct {
	Status    string `json:"status"`
	Phase     string `json:"phase,omitempty"`
	Task      string `json:"task"`
	Detail    string `json:"detail,omitempty"`
	ExitCode  int    `json:"exit_code,omitempty"`
	Hostname  string `json:"hostname,omitempty"`
	CreatedAt string `json:"created_at"`
}

func writeState(cfg BootstrapConfig, status string, exitCode int, detail string) {
	if strings.TrimSpace(cfg.StatePath) == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(cfg.StatePath), 0755)
	payload := StatePayload{
		JobID:                cfg.JobID,
		RunID:                cfg.RunID,
		Target:               cfg.Target,
		Hostname:             currentHostname(),
		RepoRef:              cfg.RepoRef,
		ServerURL:            cfg.ServerURL,
		EnrollmentCodeSHA256: hashText(cfg.SiteEnrollmentCode),
		Status:               status,
		Phase:                timelinePhaseForTask(detail, status),
		ExitCode:             exitCode,
		Detail:               detail,
		UpdatedAt:            time.Now().UTC().Format(time.RFC3339Nano),
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return
	}
	tmp := cfg.StatePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return
	}
	_ = os.Remove(cfg.StatePath)
	_ = os.Rename(tmp, cfg.StatePath)
}

func writeTimeline(cfg BootstrapConfig, status string, task string, detail string, exitCode int) {
	if strings.TrimSpace(cfg.EventsPath) == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(cfg.EventsPath), 0755)
	payload := EventPayload{
		Status:    status,
		Phase:     timelinePhaseForTask(task+" "+detail, status),
		Task:      task,
		Detail:    detail,
		ExitCode:  exitCode,
		Hostname:  currentHostname(),
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	file, err := os.OpenFile(cfg.EventsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.Write(append(data, '\n'))
}

func timelinePhaseForTask(value string, status string) string {
	normalized := strings.ToLower(strings.TrimSpace(value + " " + status))
	switch {
	case strings.Contains(normalized, "auto-detecting remote os"):
		return "os_detection"
	case strings.Contains(normalized, "repair"):
		return "repair"
	case strings.Contains(normalized, "existing agent"):
		return "existing_agent_preflight"
	case strings.Contains(normalized, "dependency") || strings.Contains(normalized, "python") || strings.Contains(normalized, "git") || strings.Contains(normalized, "ultravnc") || strings.Contains(normalized, "wireguard") || strings.Contains(normalized, "autohotkey"):
		return "dependencies"
	case strings.Contains(normalized, "configuring agent runtime") || strings.Contains(normalized, "settings") || strings.Contains(normalized, "scheduled task"):
		return "runtime_configuration"
	case strings.Contains(normalized, "awaiting approval") || strings.Contains(normalized, "pending_approval"):
		return "approval_wait"
	case strings.Contains(normalized, "already enrolled") || strings.Contains(normalized, "active"):
		return "already_enrolled"
	case strings.Contains(normalized, "uninstall"):
		return "uninstall"
	case strings.Contains(normalized, "failed"):
		return "failed"
	default:
		return "bootstrap"
	}
}

func hashText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
