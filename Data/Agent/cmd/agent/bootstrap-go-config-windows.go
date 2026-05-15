//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
)

func agentConfigPath(installDir string) string {
	return filepath.Join(installDir, agentconfig.FileName)
}

func writeGoAgentConfig(cfg BootstrapConfig, logger *BootstrapLogger) error {
	path := agentConfigPath(cfg.InstallDir)
	current, err := agentconfig.Load(path)
	if err != nil {
		return err
	}
	current.ServerURL = agentconfig.NormalizeServerURL(cfg.ServerURL)
	current.EnrollmentCode = strings.TrimSpace(cfg.SiteEnrollmentCode)
	current.ApplyDefaults()
	if err := agentconfig.Save(path, &current); err != nil {
		return err
	}
	if logger != nil {
		logger.Tracef("Go Agent config written: path=%s server_url_present=%t enrollment_present=%t", path, current.ServerURL != "", current.EnrollmentCode != "")
	}
	return nil
}

func mergeConfigJSONBootstrapInputs(cfg *BootstrapConfig) {
	if cfg == nil {
		return
	}
	data, err := os.ReadFile(agentConfigPath(cfg.InstallDir))
	if err != nil {
		return
	}
	var parsed struct {
		ServerURL      string `json:"server_url"`
		EnrollmentCode string `json:"enrollment_code"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return
	}
	if strings.TrimSpace(cfg.ServerURL) == "" {
		cfg.ServerURL = strings.TrimSpace(parsed.ServerURL)
	}
	if strings.TrimSpace(cfg.SiteEnrollmentCode) == "" {
		cfg.SiteEnrollmentCode = strings.TrimSpace(parsed.EnrollmentCode)
	}
}

func readConfigAccessToken(cfg BootstrapConfig) string {
	data, err := os.ReadFile(agentConfigPath(cfg.InstallDir))
	if err != nil {
		return ""
	}
	var parsed struct {
		Tokens struct {
			AccessToken string `json:"access_token"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return ""
	}
	return strings.TrimSpace(parsed.Tokens.AccessToken)
}
