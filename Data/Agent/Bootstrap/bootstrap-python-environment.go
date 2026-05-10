//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func ensurePythonRuntime(cfg BootstrapConfig, logger *BootstrapLogger) error {
	startedAt := time.Now()
	pythonExe := bootstrapPythonExe(cfg)
	if fileExists(pythonExe) {
		logger.Tracef("Python runtime already installed: %s", pythonExe)
		return nil
	}
	packagePath := filepath.Join(cfg.InstallDir, "Dependencies", defaultPythonNugetPackageName)
	logger.Tracef("Python runtime download start: url=%s package=%s", defaultPythonNugetURL, packagePath)
	if err := downloadFileLogged(context.Background(), defaultPythonNugetURL, packagePath, 240*time.Second, logger); err != nil {
		return err
	}
	extractRoot := filepath.Join(cfg.InstallDir, "Temp", "PythonNuget")
	_ = os.RemoveAll(extractRoot)
	if err := unzipFileLogged(packagePath, extractRoot, logger); err != nil {
		return err
	}
	toolsRoot := filepath.Join(extractRoot, "tools")
	if !fileExists(filepath.Join(toolsRoot, "python.exe")) {
		return fmt.Errorf("Python NuGet package missing tools\\python.exe")
	}
	pythonRoot := filepath.Join(cfg.InstallDir, "Dependencies", "Python")
	_ = os.RemoveAll(pythonRoot)
	logger.Tracef("Copying Python runtime: source=%s destination=%s", toolsRoot, pythonRoot)
	if err := copyTree(toolsRoot, pythonRoot, nil); err != nil {
		return err
	}
	if !fileExists(pythonExe) {
		return fmt.Errorf("python.exe not found after NuGet extraction")
	}
	logger.Infof("Python runtime installed.")
	logger.Tracef("Python runtime install complete: python=%s duration=%s", pythonExe, time.Since(startedAt).Round(time.Millisecond))
	return nil
}

func setupPythonEnvironment(cfg BootstrapConfig, sourceRoot string, logger *BootstrapLogger) error {
	startedAt := time.Now()
	pythonExe := bootstrapPythonExe(cfg)
	venvRoot := filepath.Join(cfg.InstallDir, "Agent")
	venvPython := filepath.Join(venvRoot, "Scripts", "python.exe")
	logger.Tracef("Python environment setup start: bootstrap_python=%s venv_root=%s venv_python_exists=%t", pythonExe, venvRoot, fileExists(venvPython))
	if !fileExists(filepath.Join(venvRoot, "Scripts", "python.exe")) {
		logger.Tracef("Creating Python virtual environment: %s", venvRoot)
		if _, err := runCommandTimeout(logger, 600*time.Second, pythonExe, "-m", "venv", venvRoot); err != nil {
			return err
		}
	} else {
		logger.Tracef("Python virtual environment already exists: %s", venvRoot)
	}
	requirements := filepath.Join(sourceRoot, "Data", "Agent", "agent-requirements.txt")
	if fileExists(requirements) {
		logger.Tracef("Installing Python requirements: requirements=%s", requirements)
		if _, err := runCommandTimeout(logger, time.Duration(cfg.TimeoutSeconds)*time.Second, venvPython, "-m", "pip", "install", "--disable-pip-version-check", "-q", "-r", requirements); err != nil {
			return err
		}
	} else {
		logger.Tracef("Python requirements file missing; skipping pip install: %s", requirements)
	}
	logger.Tracef("Python environment setup complete duration=%s", time.Since(startedAt).Round(time.Millisecond))
	return nil
}

func stageAgentRuntime(cfg BootstrapConfig, sourceRoot string, logger *BootstrapLogger) error {
	startedAt := time.Now()
	source := filepath.Join(sourceRoot, "Data", "Agent")
	destination := filepath.Join(cfg.InstallDir, "Agent", "Borealis")
	logger.Tracef("Agent runtime staging start: source=%s destination=%s", source, destination)
	if !fileExists(filepath.Join(source, "agent.py")) {
		return fmt.Errorf("agent source missing at %s", source)
	}
	preserve := map[string]bool{
		"Settings":     true,
		"Certificates": true,
		"Tools":        true,
	}
	if err := pruneDirectory(destination, preserve); err != nil {
		return err
	}
	filter := func(path string, info os.FileInfo) bool {
		name := info.Name()
		if info.IsDir() {
			switch name {
			case "Unit_Tests", "Bootstrap", "Logs", "__pycache__", ".pytest_cache":
				return false
			}
		}
		return true
	}
	if err := copyTree(source, destination, filter); err != nil {
		return err
	}
	stageUltraVNCTools(cfg, logger)
	logger.Infof("Agent runtime staged.")
	logger.Tracef("Agent runtime staging complete duration=%s", time.Since(startedAt).Round(time.Millisecond))
	return nil
}

func writeAgentSettings(cfg BootstrapConfig, logger *BootstrapLogger) error {
	settingsDir := agentSettingsDir(cfg.InstallDir)
	logger.Tracef("Writing Agent settings: settings_dir=%s", settingsDir)
	if err := os.MkdirAll(settingsDir, 0755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(settingsDir, "server_url.txt"), []byte(strings.TrimSpace(cfg.ServerURL)), 0644); err != nil {
		return err
	}
	defaults := map[string]any{
		"config_file_watcher_interval": 2,
		"agent_id":                     "",
		"regions":                      map[string]any{},
		"enrollment_code":              strings.TrimSpace(cfg.SiteEnrollmentCode),
		"installer_code":               strings.TrimSpace(cfg.SiteEnrollmentCode),
	}
	for _, name := range []string{"agent_settings.json", "agent_settings_SYSTEM.json"} {
		path := filepath.Join(settingsDir, name)
		merged := map[string]any{}
		if data, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(data, &merged)
			logger.Tracef("Merged existing Agent settings file: %s", path)
		} else {
			logger.Tracef("Creating new Agent settings file: %s", path)
		}
		for key, value := range defaults {
			merged[key] = value
		}
		data, err := json.MarshalIndent(merged, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0644); err != nil {
			return err
		}
	}
	contextPayload := map[string]any{}
	if cfg.JobID > 0 {
		contextPayload["job_id"] = cfg.JobID
	}
	if cfg.RunID > 0 {
		contextPayload["run_id"] = cfg.RunID
	}
	if cfg.Target != "" {
		contextPayload["target"] = cfg.Target
	}
	if cfg.StatePath != "" {
		contextPayload["state_path"] = cfg.StatePath
	}
	if cfg.EventsPath != "" {
		contextPayload["events_path"] = cfg.EventsPath
	}
	if len(contextPayload) > 0 {
		data, _ := json.MarshalIndent(contextPayload, "", "  ")
		_ = os.WriteFile(filepath.Join(settingsDir, "onboarding_context.json"), data, 0644)
		logger.Tracef("Onboarding context written: job_id=%d run_id=%d target=%s", cfg.JobID, cfg.RunID, cfg.Target)
	}
	logger.Infof("Agent settings written.")
	return nil
}

func bootstrapPythonExe(cfg BootstrapConfig) string {
	return filepath.Join(cfg.InstallDir, "Dependencies", "Python", "python.exe")
}

func copyTree(source string, destination string, include func(string, os.FileInfo) bool) error {
	return filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if include != nil && !include(path, info) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target)
	})
}

func pruneDirectory(path string, preserve map[string]bool) error {
	if err := os.MkdirAll(path, 0755); err != nil {
		return err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if preserve[entry.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(path, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func stageUltraVNCTools(cfg BootstrapConfig, logger *BootstrapLogger) {
	payloadRoot := filepath.Join(cfg.InstallDir, "Dependencies", "UltraVNC_Server", "payload", "x64")
	if !dirExists(payloadRoot) {
		logger.Tracef("UltraVNC x64 payload root missing; skipping tool staging: %s", payloadRoot)
		return
	}
	destination := filepath.Join(cfg.InstallDir, "Agent", "Borealis", "Tools", "UltraVNC", "Server")
	logger.Tracef("Staging UltraVNC tools: source=%s destination=%s", payloadRoot, destination)
	if err := copyTree(payloadRoot, destination, nil); err != nil {
		logger.Warnf("UltraVNC payload staging failed: %v", err)
	} else {
		logger.Tracef("UltraVNC tools staged.")
	}
}

func copyReaderToFile(reader io.Reader, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	out, err := os.Create(destination)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, reader)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
