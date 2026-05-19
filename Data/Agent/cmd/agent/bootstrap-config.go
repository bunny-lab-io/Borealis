//go:build windows

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
)

const (
	defaultInstallDir            = `C:\Borealis`
	defaultRepoURL               = "https://github.com/bunny-lab-io/Borealis.git"
	defaultTimeoutSeconds        = 1800
	agentTaskName                = "Borealis Agent"
	agentUpdaterTaskName         = "Borealis Agent (AutoUpdater)"
	agentWatchdogTaskName        = "Borealis Agent (Watchdog)"
	bootstrapConfigFileName      = "bootstrapper-config.json"
	agentPayloadFileName         = "agent-payload.zip"
	agentPayloadManifestFileName = "agent-payload-manifest.json"
	bootstrapLogRelativePath     = `Logs\Agent\bootstrap.log`
	bootstrapStateRelativePath   = `Temp\Onboarding\state.json`
	bootstrapEventsRelativePath  = `Temp\Onboarding\events.jsonl`
	bootstrapOutputRelativePath  = `Temp\Onboarding\stdout.log`
)

var defaultRepoRef = "main"

type cliOptions struct {
	ServerURL          string
	SiteEnrollmentCode string
	RepoRef            string
	ReleaseChannel     string
	Uninstall          bool
	Verbose            bool
}

type BootstrapConfig struct {
	InstallDir          string `json:"install_dir"`
	ServerURL           string `json:"server_url"`
	SiteEnrollmentCode  string `json:"site_enrollment_code"`
	LegacyEnrollment    string `json:"enrollment_code"`
	RepoURL             string `json:"repo_url"`
	RepoRef             string `json:"repo_ref"`
	ReleaseChannel      string `json:"release_channel"`
	PayloadPath         string `json:"agent_bundle_path"`
	PayloadSHA256       string `json:"agent_bundle_sha256"`
	ManifestPath        string `json:"manifest_path"`
	StatePath           string `json:"state_path"`
	EventsPath          string `json:"events_path"`
	StdoutPath          string `json:"stdout_path"`
	StderrPath          string `json:"stderr_path"`
	TimeoutSeconds      int    `json:"timeout_seconds"`
	JobID               int    `json:"job_id"`
	RunID               int    `json:"run_id"`
	Target              string `json:"target"`
	ServiceName         string `json:"service_name"`
	NonInteractive      bool   `json:"noninteractive"`
	Uninstall           bool   `json:"-"`
	Verbose             bool   `json:"verbose"`
	Interactive         bool   `json:"-"`
	ConfigPath          string `json:"-"`
	ResolvedPayloadRoot string `json:"-"`
	CLIRepoRefExplicit  bool   `json:"-"`
	CLIChannelExplicit  bool   `json:"-"`
}

func parseCLI(args []string) (cliOptions, error) {
	var opts cliOptions
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}
		switch arg {
		case "--server-url":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return opts, errors.New("--server-url requires value")
			}
			i++
			opts.ServerURL = strings.TrimSpace(args[i])
		case "--site-enrollment-code":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return opts, errors.New("--site-enrollment-code requires value")
			}
			i++
			opts.SiteEnrollmentCode = strings.TrimSpace(args[i])
		case "--repo-ref", "--repo-branch":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return opts, errors.New(arg + " requires value")
			}
			i++
			opts.RepoRef = strings.TrimSpace(args[i])
		case "--release-channel":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return opts, errors.New("--release-channel requires value")
			}
			i++
			opts.ReleaseChannel = strings.TrimSpace(args[i])
		case "-uninstall":
			opts.Uninstall = true
		case "-verbose", "--verbose":
			opts.Verbose = true
		default:
			return opts, fmt.Errorf("unsupported Agent.exe argument %q", arg)
		}
	}
	return opts, nil
}

func loadBootstrapConfig(cli cliOptions, serviceMode bool) (BootstrapConfig, error) {
	cfg := defaultBootstrapConfig()
	cfg.Uninstall = cli.Uninstall
	cfg.Verbose = cli.Verbose
	cfg.Interactive = !serviceMode && isInteractiveConsole()
	if serviceMode {
		cfg.NonInteractive = true
	}

	configPath := discoverBootstrapConfigPath(cfg.InstallDir)
	if configPath != "" {
		fileCfg, err := readBootstrapConfigFile(configPath)
		if err != nil {
			return cfg, err
		}
		mergeBootstrapConfig(&cfg, fileCfg)
		cfg.ConfigPath = configPath
	}

	if cli.ServerURL != "" {
		cfg.ServerURL = cli.ServerURL
	}
	if cli.SiteEnrollmentCode != "" {
		cfg.SiteEnrollmentCode = cli.SiteEnrollmentCode
	}
	if cli.RepoRef != "" {
		cfg.RepoRef = cli.RepoRef
		cfg.CLIRepoRefExplicit = true
	}
	if cli.ReleaseChannel != "" {
		cfg.ReleaseChannel = cli.ReleaseChannel
		cfg.CLIChannelExplicit = true
	}
	if cli.Verbose {
		cfg.Verbose = true
	}
	if cfg.SiteEnrollmentCode == "" && cfg.LegacyEnrollment != "" {
		cfg.SiteEnrollmentCode = cfg.LegacyEnrollment
	}

	mergeStoredBootstrapInputs(&cfg)
	normalizeBootstrapConfig(&cfg)
	return cfg, nil
}

func defaultBootstrapConfig() BootstrapConfig {
	return BootstrapConfig{
		InstallDir:     defaultInstallDir,
		RepoURL:        defaultRepoURL,
		RepoRef:        defaultRepoRef,
		TimeoutSeconds: defaultTimeoutSeconds,
	}
}

func discoverBootstrapConfigPath(installDir string) string {
	candidates := []string{}
	if envPath := strings.TrimSpace(os.Getenv("BOREALIS_AGENT_BOOTSTRAP_CONFIG")); envPath != "" {
		candidates = append(candidates, envPath)
	}
	candidates = append(candidates, filepath.Join(installDir, "Temp", "Onboarding", bootstrapConfigFileName))
	if exePath, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exePath), bootstrapConfigFileName))
	}
	for _, candidate := range candidates {
		if fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

func readBootstrapConfigFile(path string) (BootstrapConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return BootstrapConfig{}, err
	}
	var cfg BootstrapConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return BootstrapConfig{}, fmt.Errorf("read %s: %w", path, err)
	}
	return cfg, nil
}

func mergeBootstrapConfig(base *BootstrapConfig, incoming BootstrapConfig) {
	if strings.TrimSpace(incoming.InstallDir) != "" {
		base.InstallDir = incoming.InstallDir
	}
	if strings.TrimSpace(incoming.ServerURL) != "" {
		base.ServerURL = incoming.ServerURL
	}
	if strings.TrimSpace(incoming.SiteEnrollmentCode) != "" {
		base.SiteEnrollmentCode = incoming.SiteEnrollmentCode
	}
	if strings.TrimSpace(incoming.LegacyEnrollment) != "" {
		base.LegacyEnrollment = incoming.LegacyEnrollment
	}
	if strings.TrimSpace(incoming.RepoURL) != "" {
		base.RepoURL = incoming.RepoURL
	}
	if strings.TrimSpace(incoming.RepoRef) != "" {
		base.RepoRef = incoming.RepoRef
	}
	if strings.TrimSpace(incoming.ReleaseChannel) != "" {
		base.ReleaseChannel = incoming.ReleaseChannel
	}
	if strings.TrimSpace(incoming.PayloadPath) != "" {
		base.PayloadPath = incoming.PayloadPath
	}
	if strings.TrimSpace(incoming.PayloadSHA256) != "" {
		base.PayloadSHA256 = incoming.PayloadSHA256
	}
	if strings.TrimSpace(incoming.ManifestPath) != "" {
		base.ManifestPath = incoming.ManifestPath
	}
	if strings.TrimSpace(incoming.StatePath) != "" {
		base.StatePath = incoming.StatePath
	}
	if strings.TrimSpace(incoming.EventsPath) != "" {
		base.EventsPath = incoming.EventsPath
	}
	if strings.TrimSpace(incoming.StdoutPath) != "" {
		base.StdoutPath = incoming.StdoutPath
	}
	if strings.TrimSpace(incoming.StderrPath) != "" {
		base.StderrPath = incoming.StderrPath
	}
	if incoming.TimeoutSeconds > 0 {
		base.TimeoutSeconds = incoming.TimeoutSeconds
	}
	if incoming.JobID > 0 {
		base.JobID = incoming.JobID
	}
	if incoming.RunID > 0 {
		base.RunID = incoming.RunID
	}
	if strings.TrimSpace(incoming.Target) != "" {
		base.Target = incoming.Target
	}
	if strings.TrimSpace(incoming.ServiceName) != "" {
		base.ServiceName = incoming.ServiceName
	}
	if incoming.NonInteractive {
		base.NonInteractive = true
		base.Interactive = false
	}
	if incoming.Verbose {
		base.Verbose = true
	}
}

func normalizeBootstrapConfig(cfg *BootstrapConfig) {
	cfg.InstallDir = cleanPathOrDefault(cfg.InstallDir, defaultInstallDir)
	cfg.RepoURL = strings.TrimSpace(cfg.RepoURL)
	if cfg.RepoURL == "" {
		cfg.RepoURL = defaultRepoURL
	}
	cfg.RepoRef = strings.TrimSpace(cfg.RepoRef)
	if cfg.RepoRef == "" {
		cfg.RepoRef = defaultRepoRef
	}
	if strings.TrimSpace(cfg.ReleaseChannel) == "" {
		cfg.ReleaseChannel = agentconfig.ReleaseChannelForBranch(cfg.RepoRef)
	} else {
		cfg.ReleaseChannel = agentconfig.NormalizeReleaseChannel(cfg.ReleaseChannel)
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = defaultTimeoutSeconds
	}
	if cfg.PayloadPath == "" {
		cfg.PayloadPath = filepath.Join(cfg.InstallDir, "Temp", "Onboarding", agentPayloadFileName)
	}
	if cfg.ManifestPath == "" {
		cfg.ManifestPath = filepath.Join(cfg.InstallDir, "Temp", "Onboarding", agentPayloadManifestFileName)
	}
	if cfg.StatePath == "" {
		cfg.StatePath = filepath.Join(cfg.InstallDir, bootstrapStateRelativePath)
	}
	if cfg.EventsPath == "" {
		cfg.EventsPath = filepath.Join(cfg.InstallDir, bootstrapEventsRelativePath)
	}
	if cfg.StdoutPath == "" {
		cfg.StdoutPath = filepath.Join(cfg.InstallDir, bootstrapOutputRelativePath)
	}
	if cfg.StderrPath == "" {
		cfg.StderrPath = cfg.StdoutPath
	}
	cfg.ServiceName = strings.TrimSpace(cfg.ServiceName)
	if cfg.ServiceName == "" {
		cfg.ServiceName = "BorealisAgentBootstrapper"
	}
	if cfg.NonInteractive {
		cfg.Interactive = false
	}
}

func mergeStoredBootstrapInputs(cfg *BootstrapConfig) {
	mergeConfigJSONBootstrapInputs(cfg)
	if cfg.ServerURL == "" {
		cfg.ServerURL = readFirstLine(filepath.Join(agentSettingsDir(cfg.InstallDir), "server_url.txt"))
	}
	if cfg.SiteEnrollmentCode == "" {
		cfg.SiteEnrollmentCode = readEnrollmentCode(filepath.Join(agentSettingsDir(cfg.InstallDir), "agent_settings.json"))
	}
	if cfg.SiteEnrollmentCode == "" {
		cfg.SiteEnrollmentCode = readEnrollmentCode(filepath.Join(agentSettingsDir(cfg.InstallDir), "agent_settings_SYSTEM.json"))
	}
}

func missingBootstrapInputs(cfg BootstrapConfig) []string {
	missing := []string{}
	if strings.TrimSpace(cfg.ServerURL) == "" {
		missing = append(missing, "--server-url")
	}
	if strings.TrimSpace(cfg.SiteEnrollmentCode) == "" {
		missing = append(missing, "--site-enrollment-code")
	}
	return missing
}

func promptForMissingInputs(cfg *BootstrapConfig, logger *BootstrapLogger) {
	reader := bufio.NewReader(os.Stdin)
	if strings.TrimSpace(cfg.ServerURL) == "" {
		logger.Println("Server URL required.")
		fmt.Print("Server URL: ")
		value, _ := reader.ReadString('\n')
		cfg.ServerURL = strings.TrimSpace(value)
	}
	if strings.TrimSpace(cfg.SiteEnrollmentCode) == "" {
		logger.Println("Site enrollment code required.")
		fmt.Print("Site enrollment code: ")
		value, _ := reader.ReadString('\n')
		cfg.SiteEnrollmentCode = strings.TrimSpace(value)
	}
}

func agentSettingsDir(installDir string) string {
	return filepath.Join(installDir, "Agent", "Borealis", "Settings")
}

func cleanPathOrDefault(value string, fallback string) string {
	text := strings.TrimSpace(value)
	if text == "" {
		return fallback
	}
	cleaned := filepath.Clean(text)
	volume := filepath.VolumeName(cleaned)
	if strings.TrimRight(cleaned, `\`) == strings.TrimRight(volume+`\`, `\`) {
		return fallback
	}
	return cleaned
}

func readFirstLine(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[0])
}

func readEnrollmentCode(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return ""
	}
	for _, key := range []string{"enrollment_code", "installer_code", "site_enrollment_code"} {
		if value := strings.TrimSpace(fmt.Sprint(payload[key])); value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}
