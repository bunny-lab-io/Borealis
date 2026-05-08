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
	ExitCode             int    `json:"exit_code"`
	Detail               string `json:"detail"`
	UpdatedAt            string `json:"updated_at"`
}

type EventPayload struct {
	Status    string `json:"status"`
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

func hashText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
