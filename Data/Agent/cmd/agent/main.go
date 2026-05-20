package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
	"github.com/bunny-lab-io/borealis/go-agent/internal/roles/current_user"
	agentruntime "github.com/bunny-lab-io/borealis/go-agent/internal/runtime"
)

var version = "dev"
var resolveInstallRepoRefBuildIDFunc = resolveInstallRepoRefBuildID

func main() {
	os.Exit(run())
}

func run() int {
	if code, handled := maybeRunBootstrap(os.Args[1:]); handled {
		return code
	}

	var options agentruntime.Options
	var installService bool
	var uninstallService bool
	var printVersion bool
	var updateCheck bool
	var finalizeUpdate bool
	var service bool
	var watchdogCheck bool
	var validateConfig bool
	var finalizeBuildID string
	var finalizeExpectedSHA256 string
	var repoRef string
	var helperSessionID int
	var helperStateDir string
	flag.StringVar(&options.ConfigPath, "config-path", "", "Path to agent.json. Defaults beside Agent.exe.")
	flag.StringVar(&options.ServerURL, "server-url", "", "Borealis Engine public URL.")
	flag.StringVar(&options.EnrollmentCode, "site-enrollment-code", "", "Site enrollment code.")
	flag.StringVar(&options.EnrollmentCode, "enrollment-code", "", "Enrollment code.")
	flag.StringVar(&repoRef, "repo-ref", "", "Borealis repository branch/ref used by bootstrap installers.")
	flag.StringVar(&options.ReleaseChannel, "release-channel", "", "Agent release channel: stable or unstable.")
	flag.StringVar(&helperStateDir, "helper-state-dir", "", "Current-user helper state directory.")
	flag.BoolVar(&options.Verbose, "verbose", false, "Mirror logs to stdout.")
	flag.BoolVar(&options.Once, "once", false, "Run auth and heartbeat once, then exit.")
	flag.BoolVar(&installService, "install-service", false, "Install and start Borealis Agent service.")
	flag.BoolVar(&uninstallService, "uninstall-service", false, "Uninstall Borealis Agent service.")
	flag.BoolVar(&updateCheck, "update-check", false, "Run one Agent release-channel update check.")
	flag.BoolVar(&watchdogCheck, "watchdog-check", false, "Run one local Agent watchdog check.")
	flag.BoolVar(&finalizeUpdate, "finalize-update", false, "Finalize a deferred Agent binary replacement.")
	flag.BoolVar(&validateConfig, "validate-config", false, "Validate agent.json compatibility and exit.")
	flag.StringVar(&finalizeBuildID, "build-id", "", "Installed build ID for deferred update finalization.")
	flag.StringVar(&finalizeExpectedSHA256, "expected-sha256", "", "Expected Agent binary SHA-256 for deferred update finalization.")
	flag.BoolVar(&printVersion, "version", false, "Print version.")
	flag.BoolVar(&service, "service", false, "Run as managed Agent service.")
	helperMode := flag.Bool("helper", false, "Run as current-user helper.")
	flag.IntVar(&helperSessionID, "helper-session-id", 0, "Current-user helper session ID.")
	flag.Parse()
	options.RepoRef = repoRef
	options.BuildID = version
	installService = shouldRunInstallService(installService, options)

	if printVersion {
		fmt.Println(version)
		return 0
	}
	if validateConfig {
		configPath := options.ConfigPath
		if configPath == "" {
			var err error
			configPath, err = agentconfig.PathFromBinary()
			if err != nil {
				fmt.Fprintf(os.Stderr, "resolve config path: %v\n", err)
				return 1
			}
		}
		if err := validateAgentConfig(configPath); err != nil {
			fmt.Fprintf(os.Stderr, "validate config: %v\n", err)
			return 1
		}
		fmt.Println("agent config ok")
		return 0
	}

	exePath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve executable: %v\n", err)
		return 1
	}
	if finalizeUpdate {
		configPath := options.ConfigPath
		if configPath == "" {
			configPath, err = agentconfig.PathFromBinary()
			if err != nil {
				fmt.Fprintf(os.Stderr, "resolve config path: %v\n", err)
				return 1
			}
		}
		if err := finalizeDeferredUpdate(configPath, finalizeBuildID, finalizeExpectedSHA256); err != nil {
			fmt.Fprintf(os.Stderr, "finalize update: %v\n", err)
			return 1
		}
		return 0
	}
	if updateCheck {
		updateConfigPath := options.ConfigPath
		if updateConfigPath == "" {
			updateConfigPath, _ = agentconfig.PathFromBinary()
		}
		markConfigUpdateOperation(updateConfigPath, "running", "")
		if err := runStandaloneUpdateCheck(options); err != nil {
			markConfigUpdateOperation(updateConfigPath, "failed", err.Error())
			fmt.Fprintf(os.Stderr, "update check: %v\n", err)
			return 1
		}
		markConfigUpdateOperationSuccess(updateConfigPath)
		return 0
	}
	if watchdogCheck {
		configPath := options.ConfigPath
		if configPath == "" {
			configPath, err = agentconfig.PathFromBinary()
			if err != nil {
				fmt.Fprintf(os.Stderr, "resolve agent path: %v\n", err)
				return 1
			}
		}
		if err := agentruntime.RunWatchdogCheck(configPath); err != nil {
			fmt.Fprintf(os.Stderr, "watchdog check: %v\n", err)
			return 1
		}
		return 0
	}
	if installService {
		if isFreshDeployInstall(options) {
			if err := validateFreshDeployInstall(options); err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				return 1
			}
			if err := agentruntime.ResetInstallForFreshDeploy(exePath); err != nil {
				fmt.Fprintf(os.Stderr, "reset install root: %v\n", err)
				return 1
			}
		}
		serviceExePath, err := agentruntime.PrepareServiceExecutable(exePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "prepare service executable: %v\n", err)
			return 1
		}
		if options.ConfigPath == "" {
			options.ConfigPath = agentruntime.ConfigPathForExecutable(serviceExePath)
		}
		if err := persistInstallConfig(options); err != nil {
			fmt.Fprintf(os.Stderr, "persist install config: %v\n", err)
			return 1
		}
		if err := agentruntime.InstallService(serviceExePath); err != nil {
			fmt.Fprintf(os.Stderr, "install service: %v\n", err)
			return 1
		}
		return 0
	}
	if uninstallService {
		if err := agentruntime.UninstallService(); err != nil {
			fmt.Fprintf(os.Stderr, "uninstall service: %v\n", err)
			return 1
		}
		return 0
	}

	if *helperMode {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := currentuser.RunHelper(ctx, currentuser.HelperOptions{
			SessionID: helperSessionID,
			StateDir:  helperStateDir,
			BuildID:   version,
		}); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "helper stopped: %v\n", err)
			return 1
		}
		return 0
	} else if service {
		options.ServiceMode = "system"
	} else {
		options.ServiceMode = "system"
	}

	configPath := options.ConfigPath
	if configPath == "" {
		configPath, err = agentconfig.PathFromBinary()
		if err != nil {
			fmt.Fprintf(os.Stderr, "resolve config path: %v\n", err)
			return 1
		}
	}
	logger, closeLogger := agentruntime.OpenLogger(configPath, options.Verbose)
	defer closeLogger()

	runAgent := func(ctx context.Context) error {
		agent, err := agentruntime.New(options, logger)
		if err != nil {
			logger.Printf("agent init failed: %v", err)
			return err
		}
		return agent.Run(ctx)
	}
	if service {
		if code, handled := agentruntime.RunServiceIfNeeded(logger, runAgent); handled {
			return code
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runAgent(ctx); err != nil && ctx.Err() == nil {
		logger.Printf("agent stopped: %v", err)
		fmt.Fprintf(os.Stderr, "agent stopped: %v\n", err)
		return 1
	}
	return 0
}

func isFreshDeployInstall(options agentruntime.Options) bool {
	return strings.TrimSpace(options.ServerURL) != "" || strings.TrimSpace(options.EnrollmentCode) != "" || strings.TrimSpace(options.RepoRef) != ""
}

func shouldRunInstallService(explicit bool, options agentruntime.Options) bool {
	return explicit || strings.TrimSpace(options.ServerURL) != "" || strings.TrimSpace(options.EnrollmentCode) != ""
}

func validateFreshDeployInstall(options agentruntime.Options) error {
	if strings.TrimSpace(options.ServerURL) == "" || strings.TrimSpace(options.EnrollmentCode) == "" {
		return fmt.Errorf("unsafe fresh install: --server-url and --site-enrollment-code are required to re-enroll the device")
	}
	return nil
}

func persistInstallConfig(options agentruntime.Options) error {
	configPath := options.ConfigPath
	if configPath == "" {
		resolved, err := agentconfig.PathFromBinary()
		if err != nil {
			return err
		}
		configPath = resolved
	}
	cfg, err := loadInstallConfig(configPath)
	if err != nil {
		return err
	}
	if options.ServerURL != "" {
		cfg.ServerURL = agentconfig.NormalizeServerURL(options.ServerURL)
	}
	if options.EnrollmentCode != "" {
		cfg.EnrollmentCode = options.EnrollmentCode
	}
	if options.RepoRef != "" {
		cfg.Agent.Branch = agentconfig.NormalizeBranch(options.RepoRef)
		cfg.Agent.ReleaseChannel = agentconfig.ReleaseChannelForBranch(cfg.Agent.Branch)
	}
	if options.ReleaseChannel != "" {
		cfg.Agent.ReleaseChannel = agentconfig.NormalizeReleaseChannel(options.ReleaseChannel)
	}
	if buildID := resolveInstallBuildID(options, cfg); buildID != "" {
		cfg.Agent.InstalledBuildID = buildID
	}
	return agentconfig.Save(configPath, &cfg)
}

func loadInstallConfig(configPath string) (agentconfig.AgentConfig, error) {
	return agentconfig.LoadOrCreate(configPath)
}

func resolveInstallBuildID(options agentruntime.Options, cfg agentconfig.AgentConfig) string {
	if agentconfig.NormalizeBuildID(cfg.Agent.InstalledBuildID) != "" {
		return agentconfig.NormalizeBuildID(cfg.Agent.InstalledBuildID)
	}
	repoRef := strings.TrimSpace(options.RepoRef)
	if repoRef == "" {
		repoRef = strings.TrimSpace(cfg.Agent.Branch)
	}
	channel := cfg.Agent.ReleaseChannel
	if strings.TrimSpace(options.ReleaseChannel) != "" {
		channel = options.ReleaseChannel
	}
	if repoRef != "" && agentconfig.UsesUnstableReleaseChannel(channel) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if buildID, err := resolveInstallRepoRefBuildIDFunc(ctx, repoRef); err == nil {
			return buildID
		}
	}
	return agentconfig.NormalizeBuildID(options.BuildID)
}

func resolveInstallRepoRefBuildID(ctx context.Context, repoRef string) (string, error) {
	apiURL := "https://api.github.com/repos/bunny-lab-io/Borealis/commits/" + url.PathEscape(strings.TrimSpace(repoRef))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("GitHub commit API HTTP %d", resp.StatusCode)
	}
	var payload struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	sha := agentconfig.NormalizeBuildID(payload.SHA)
	if sha == "" {
		return "", fmt.Errorf("GitHub commit API returned empty sha")
	}
	return sha, nil
}
