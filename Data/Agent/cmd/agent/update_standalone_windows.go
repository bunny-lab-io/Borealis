//go:build windows

package main

import (
	"path/filepath"
	"strings"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
	agentruntime "github.com/bunny-lab-io/borealis/go-agent/internal/runtime"
)

func runStandaloneUpdateCheck(options agentruntime.Options) error {
	cfg := defaultBootstrapConfig()
	configPath := agentruntime.ConfigPathForExecutable(filepath.Join(cfg.InstallDir, "Agent.exe"))
	if strings.TrimSpace(options.ConfigPath) != "" {
		cfg.InstallDir = filepath.Dir(options.ConfigPath)
		configPath = options.ConfigPath
	}
	current, err := agentconfig.LoadOrCreate(configPath)
	configLoaded := err == nil
	if configLoaded {
		cfg.ServerURL = current.ServerURL
		cfg.ReleaseChannel = agentconfig.NormalizeReleaseChannel(current.Agent.ReleaseChannel)
		cfg.RepoRef = agentconfig.NormalizeBranch(current.Agent.Branch)
	}
	if strings.TrimSpace(options.ServerURL) != "" {
		if err := agentconfig.ValidateServerURLForEnrollment(options.ServerURL); err != nil {
			return err
		}
		cfg.ServerURL = options.ServerURL
	}
	if strings.TrimSpace(options.ServerIPFallback) != "" {
		if err := agentconfig.ValidateServerIPFallback(options.ServerIPFallback); err != nil {
			return err
		}
		if configLoaded {
			current.ServerIPFallback = agentconfig.NormalizeServerIPFallback(options.ServerIPFallback)
			_ = agentconfig.Save(configPath, &current)
		}
	}
	if strings.TrimSpace(options.TrustedEngineCAB64) != "" {
		pemText, err := agentconfig.DecodeEngineCAB64(options.TrustedEngineCAB64)
		if err != nil {
			return err
		}
		cfg.TrustedEngineCAPEM = pemText
		if configLoaded {
			current.Trust.EngineCAPEM = pemText
			_ = agentconfig.Save(configPath, &current)
		}
	}
	if strings.TrimSpace(options.TrustedEngineCAPEM) != "" {
		cfg.TrustedEngineCAPEM = agentconfig.NormalizeEngineCAPEM(options.TrustedEngineCAPEM)
		if configLoaded {
			current.Trust.EngineCAPEM = cfg.TrustedEngineCAPEM
			_ = agentconfig.Save(configPath, &current)
		}
	}
	if strings.TrimSpace(options.RepoRef) != "" {
		cfg.RepoRef = agentconfig.NormalizeBranch(options.RepoRef)
		cfg.ReleaseChannel = agentconfig.ReleaseChannelForBranch(cfg.RepoRef)
		if configLoaded {
			current.Agent.Branch = cfg.RepoRef
			current.Agent.ReleaseChannel = cfg.ReleaseChannel
			_ = agentconfig.Save(configPath, &current)
		}
	}
	if strings.TrimSpace(options.ReleaseChannel) != "" {
		cfg.ReleaseChannel = agentconfig.NormalizeReleaseChannel(options.ReleaseChannel)
		if configLoaded {
			current.Agent.ReleaseChannel = cfg.ReleaseChannel
			_ = agentconfig.Save(configPath, &current)
		}
	}
	cfg.Verbose = options.Verbose
	logger, closeLog, err := openBootstrapLogger(cfg, false)
	if err != nil {
		return err
	}
	defer closeLog()
	return runAgentUpdateCheck(cfg, logger)
}
