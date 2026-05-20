//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
)

func agentConfigPath(installDir string) string {
	return filepath.Join(installDir, agentconfig.FileName)
}

func writeGoAgentConfig(cfg BootstrapConfig, logger *BootstrapLogger) error {
	removeLegacyConfigJSON(cfg.InstallDir, logger)
	path := agentConfigPath(cfg.InstallDir)
	current, err := agentconfig.Load(path)
	if err != nil {
		return err
	}
	current.ServerURL = agentconfig.NormalizeServerURL(cfg.ServerURL)
	current.EnrollmentCode = strings.TrimSpace(cfg.SiteEnrollmentCode)
	current.Agent.ReleaseChannel = agentconfig.NormalizeReleaseChannel(cfg.ReleaseChannel)
	current.Agent.Branch = agentconfig.NormalizeBranch(cfg.RepoRef)
	current.ApplyDefaults()
	if err := agentconfig.SaveWithWriter(path, "bootstrap:config", &current); err != nil {
		return err
	}
	if logger != nil {
		logger.Tracef("Go Agent config written: path=%s server_url_present=%t enrollment_present=%t release_channel=%s branch=%s", path, current.ServerURL != "", current.EnrollmentCode != "", current.Agent.ReleaseChannel, current.Agent.Branch)
	}
	return nil
}

func removeLegacyConfigJSON(installDir string, logger *BootstrapLogger) {
	legacyPath := filepath.Join(installDir, "config.json")
	if _, err := os.Stat(legacyPath); err != nil {
		return
	}
	if err := os.Remove(legacyPath); err != nil {
		if logger != nil {
			logger.Warnf("Failed to remove legacy config.json: path=%s error=%v", legacyPath, err)
		}
		return
	}
	if logger != nil {
		logger.Tracef("Removed legacy config.json: path=%s", legacyPath)
	}
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
			ReleaseChannel string `json:"release_channel"`
			Branch         string `json:"branch"`
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
	if strings.TrimSpace(cfg.ReleaseChannel) == "" {
		if releaseChannel := strings.TrimSpace(parsed.Agent.ReleaseChannel); releaseChannel != "" {
			cfg.ReleaseChannel = releaseChannel
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
	if strings.TrimSpace(current.Agent.ReleaseChannel) == "" {
		current.Agent.ReleaseChannel = agentconfig.NormalizeReleaseChannel(cfg.ReleaseChannel)
	}
	if strings.TrimSpace(current.Agent.Branch) == "" {
		current.Agent.Branch = agentconfig.NormalizeBranch(cfg.RepoRef)
	}
	if err := agentconfig.SaveWithWriter(path, "bootstrap:installed_build_id", &current); err != nil {
		return err
	}
	return nil
}

func writeConfigReleaseTarget(cfg BootstrapConfig, releaseChannel string, branch string) {
	path := agentConfigPath(cfg.InstallDir)
	current, err := agentconfig.LoadOrCreate(path)
	if err != nil {
		return
	}
	current.Agent.ReleaseChannel = agentconfig.NormalizeReleaseChannel(releaseChannel)
	if strings.TrimSpace(branch) != "" {
		current.Agent.Branch = agentconfig.NormalizeBranch(branch)
	}
	_ = agentconfig.SaveWithWriter(path, "bootstrap:release_target", &current)
}

func readConfigDependencyVersion(cfg BootstrapConfig, name string) string {
	current, err := agentconfig.Load(agentConfigPath(cfg.InstallDir))
	if err != nil || len(current.Agent.DependencyState) == 0 {
		return ""
	}
	state, ok := current.Agent.DependencyState[agentconfig.NormalizeDependencyName(name)]
	if !ok {
		return ""
	}
	return agentconfig.NormalizeDependencyVersion(state.InstalledVersion)
}

func writeConfigDependencyVersion(cfg BootstrapConfig, name string, version string) error {
	normalizedVersion := agentconfig.NormalizeDependencyVersion(version)
	if normalizedVersion == "" {
		return nil
	}
	path := agentConfigPath(cfg.InstallDir)
	current, err := agentconfig.LoadOrCreate(path)
	if err != nil {
		return err
	}
	if agentconfig.NormalizeDependencyName(name) == "" {
		return nil
	}
	current.UpdateDependencyState(name, func(state *agentconfig.DependencyStateSection) {
		state.InstalledVersion = normalizedVersion
	})
	if err := agentconfig.SaveWithWriter(path, "bootstrap:dependency_version:"+agentconfig.NormalizeDependencyName(name), &current); err != nil {
		return err
	}
	return nil
}

func writeConfigDependencyState(cfg BootstrapConfig, name string, phase string, status string, desiredVersion string, installedVersion string, detail string, lastError string) {
	path := agentConfigPath(cfg.InstallDir)
	now := time.Now().Unix()
	_ = agentconfig.UpdateWithWriter(path, "bootstrap:dependency_state:"+strings.ToLower(strings.TrimSpace(name)), func(current *agentconfig.AgentConfig) {
		current.UpdateDependencyState(name, func(state *agentconfig.DependencyStateSection) {
			state.Phase = phase
			state.Status = status
			state.DesiredVersion = desiredVersion
			if strings.TrimSpace(installedVersion) != "" {
				state.InstalledVersion = installedVersion
			}
			state.Detail = detail
			if status == "recovering" || phase == "installing" || phase == "install_needed" {
				state.LastAttemptAt = now
			}
			if status == "healthy" {
				state.LastSuccessAt = now
				state.LastError = ""
			}
			if strings.TrimSpace(lastError) != "" {
				state.LastError = strings.TrimSpace(lastError)
			}
		})
	})
}
