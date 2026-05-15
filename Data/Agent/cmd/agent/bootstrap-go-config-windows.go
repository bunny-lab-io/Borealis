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
	current.Agent.Branch = agentconfig.NormalizeBranch(cfg.RepoRef)
	current.ApplyDefaults()
	if err := agentconfig.Save(path, &current); err != nil {
		return err
	}
	if logger != nil {
		logger.Tracef("Go Agent config written: path=%s server_url_present=%t enrollment_present=%t branch=%s", path, current.ServerURL != "", current.EnrollmentCode != "", current.Agent.Branch)
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
		Agent          struct {
			Branch string `json:"branch"`
		} `json:"agent"`
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
	if strings.TrimSpace(cfg.RepoRef) == "" || strings.EqualFold(strings.TrimSpace(cfg.RepoRef), defaultRepoRef) {
		if branch := strings.TrimSpace(parsed.Agent.Branch); branch != "" {
			cfg.RepoRef = branch
		}
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

func readConfigInstalledBuildID(cfg BootstrapConfig) string {
	current, err := agentconfig.Load(agentConfigPath(cfg.InstallDir))
	if err != nil {
		return ""
	}
	return agentconfig.NormalizeBuildID(current.Agent.InstalledBuildID)
}

func writeConfigInstalledBuildID(cfg BootstrapConfig, value string) error {
	buildID := agentconfig.NormalizeBuildID(value)
	if buildID == "" || strings.EqualFold(buildID, "dev") {
		return nil
	}
	path := agentConfigPath(cfg.InstallDir)
	current, err := agentconfig.LoadOrCreate(path)
	if err != nil {
		return err
	}
	current.Agent.InstalledBuildID = buildID
	if strings.TrimSpace(current.Agent.Branch) == "" {
		current.Agent.Branch = agentconfig.NormalizeBranch(cfg.RepoRef)
	}
	if err := agentconfig.Save(path, &current); err != nil {
		return err
	}
	return nil
}
