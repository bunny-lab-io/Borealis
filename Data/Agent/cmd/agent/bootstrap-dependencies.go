//go:build windows

package main

import (
	"context"
	"crypto/des"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
)

const (
	dependencyNameUltraVNC               = "ultravnc"
	dependencyNameWireGuard              = "wireguard"
	ultraVNCServiceName                  = "BorealisAgentUltraVNC"
	ultraVNCServiceDisplayName           = "Borealis Agent - UltraVNC"
	ultraVNCVersion                      = "1.8.2.1"
	ultraVNCInstallerName                = "UltraVNC_1821_x64_Setup.exe"
	ultraVNCInstallerURL                 = "https://uvnc.eu/download/1800/UltraVNC_1821_x64_Setup.exe"
	ultraVNCInstallerSHA256              = "7c12518b05a25f5cd502fa21818c7271c766cacff312cbc47bc3942468b14919"
	wireGuardManagerServiceName          = "WireGuardManager"
	wireGuardManagerDisplayName          = "Borealis Agent - WireGuard Manager"
	wireGuardInstallManagerServiceEnvVar = "BOREALIS_WIREGUARD_INSTALL_MANAGER_SERVICE"
	wireGuardDownloadBaseURL             = "https://download.wireguard.com/windows-client"
	wireGuardMSIVersion                  = "1.1"
	wireGuardMSIProductCode              = "{99A54A94-4BE0-4374-B3A6-F504E826DDF8}"
	wireGuardMSISHA256AMD64              = "6daa5d37a9e2950dfb8c48b95ab8e562cb2bad1c785d020f38f97bea4c6a5566"
	wireGuardMSISHA256ARM64              = "a2a67fbb2db199525c35ce79ea6dd9031b116ba46561f2b993fb858668440131"
	wireGuardMSISHA256X86                = "71811698d544607e6bd94bbfff14e936b186da53b2934ff74d736daa74105481"
	bootstrapUltraVNCPasswordBytes       = 8
	bootstrapUltraVNCPasswordHashSuffix  = "00"
)

var bootstrapUltraVNCStoredPasswordDESKey = []byte{0xE8, 0x4A, 0xD6, 0x60, 0xC4, 0x72, 0x1A, 0xE0}

func ensureAgentDependencies(cfg BootstrapConfig, logger *BootstrapLogger) error {
	startedAt := time.Now()
	logger.Tracef("Dependency coordinator start.")
	optionalSteps := []struct {
		name       string
		dependency string
		fn         func(BootstrapConfig, *BootstrapLogger) error
	}{
		{"UltraVNC Server", dependencyNameUltraVNC, ensureUltraVNCServer},
		{"WireGuard VPN Adapter", dependencyNameWireGuard, ensureWireGuardInstaller},
	}
	for _, step := range optionalSteps {
		stepStartedAt := time.Now()
		task := dependencyTaskName(step.name)
		logger.Tracef("Dependency step start: %s", step.name)
		writeConfigDependencyState(cfg, step.dependency, "install_needed", "recovering", dependencyDesiredVersion(step.dependency), "", "Dependency reconciliation started.", "")
		writeTimeline(cfg, "running", task, "Installing "+step.name+".", 1)
		if err := step.fn(cfg, logger); err != nil {
			writeConfigDependencyState(cfg, step.dependency, "failed", "failed", dependencyDesiredVersion(step.dependency), readConfigDependencyVersion(cfg, step.dependency), step.name+" dependency deferred.", err.Error())
			logger.Warnf("%s dependency deferred: %v", step.name, err)
			writeTimeline(cfg, "failed", task, step.name+" dependency deferred: "+err.Error(), 1)
			logger.Tracef("Dependency step deferred: %s duration=%s error=%v", step.name, time.Since(stepStartedAt).Round(time.Millisecond), err)
			continue
		}
		writeConfigDependencyState(cfg, step.dependency, "healthy", "healthy", dependencyDesiredVersion(step.dependency), readConfigDependencyVersion(cfg, step.dependency), step.name+" dependency ready.", "")
		writeTimeline(cfg, "completed", task, step.name+" dependency ready.", 0)
		logger.Tracef("Dependency step complete: %s duration=%s", step.name, time.Since(stepStartedAt).Round(time.Millisecond))
	}
	cleanupDependencyWorkspace(cfg, logger)
	logger.Tracef("Dependency coordinator complete duration=%s.", time.Since(startedAt).Round(time.Millisecond))
	return nil
}

func dependencyDesiredVersion(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case dependencyNameUltraVNC:
		return ultraVNCVersion
	case dependencyNameWireGuard:
		return wireGuardMSIVersion
	default:
		return ""
	}
}

func dependencyTaskName(name string) string {
	return "Installing Agent Dependencies: " + name
}

func cleanupDependencyWorkspace(cfg BootstrapConfig, logger *BootstrapLogger) {
	root := filepath.Join(cfg.InstallDir, "Dependencies")
	if filepath.Base(root) != "Dependencies" {
		if logger != nil {
			logger.Tracef("Skipping unexpected dependency cleanup path: %s", root)
		}
		return
	}
	if err := removePathWithRetries(root, 3, time.Second, nil); err != nil {
		if logger != nil {
			logger.Tracef("Dependency workspace cleanup skipped or partial: %v", err)
		}
		return
	}
	if logger != nil {
		logger.Tracef("Dependency workspace removed: %s", root)
	}
}

func dependencyVersionAtLeast(installed string, desired string) bool {
	return compareDependencyVersions(installed, desired) >= 0
}

func compareDependencyVersions(left string, right string) int {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" && right == "" {
		return 0
	}
	if left == "" {
		return -1
	}
	if right == "" {
		return 1
	}
	leftParts := strings.Split(left, ".")
	rightParts := strings.Split(right, ".")
	maxParts := len(leftParts)
	if len(rightParts) > maxParts {
		maxParts = len(rightParts)
	}
	for index := 0; index < maxParts; index++ {
		leftPart := "0"
		if index < len(leftParts) {
			leftPart = strings.TrimSpace(leftParts[index])
		}
		rightPart := "0"
		if index < len(rightParts) {
			rightPart = strings.TrimSpace(rightParts[index])
		}
		leftNumber, leftErr := strconv.Atoi(leftPart)
		rightNumber, rightErr := strconv.Atoi(rightPart)
		if leftErr == nil && rightErr == nil {
			if leftNumber < rightNumber {
				return -1
			}
			if leftNumber > rightNumber {
				return 1
			}
			continue
		}
		if leftPart < rightPart {
			return -1
		}
		if leftPart > rightPart {
			return 1
		}
	}
	return 0
}

func ultraVNCProgramDataConfigPath() string {
	programData := strings.TrimSpace(os.Getenv("ProgramData"))
	if programData == "" {
		programData = `C:\ProgramData`
	}
	return filepath.Join(programData, "UltraVNC", ultraVNCServiceName+".ini")
}

func ultraVNCLegacyProgramDataConfigPath() string {
	programData := strings.TrimSpace(os.Getenv("ProgramData"))
	if programData == "" {
		programData = `C:\ProgramData`
	}
	return filepath.Join(programData, "UltraVNC", "ultravnc.ini")
}

func resolveUltraVNCInstalledExe() string {
	candidates := []string{
		filepath.Join(os.Getenv("ProgramFiles"), "uvnc bvba", "UltraVNC", "winvnc.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "UltraVNC", "winvnc.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "uvnc bvba", "UltraVNC", "winvnc64.exe"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "uvnc bvba", "UltraVNC", "winvnc.exe"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "UltraVNC", "winvnc.exe"),
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		if fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

func ensureUltraVNCSystemInstall(cfg BootstrapConfig, logger *BootstrapLogger) error {
	root := filepath.Join(cfg.InstallDir, "Dependencies", "UltraVNC_Server")
	exePath := resolveUltraVNCInstalledExe()
	installedVersion := readConfigDependencyVersion(cfg, dependencyNameUltraVNC)
	writeConfigDependencyState(cfg, dependencyNameUltraVNC, "detected", "recovering", ultraVNCVersion, installedVersion, dependencyFallbackText(exePath, "UltraVNC executable not found."), "")
	if exePath != "" && dependencyVersionAtLeast(installedVersion, ultraVNCVersion) {
		writeConfigDependencyState(cfg, dependencyNameUltraVNC, "installed", "healthy", ultraVNCVersion, installedVersion, exePath, "")
		logger.Tracef("UltraVNC %s already installed at %s; config version=%s", ultraVNCVersion, exePath, installedVersion)
		return nil
	}
	if exePath != "" {
		fileVersion := readWindowsFileVersion(exePath, logger)
		version := dependencyFallbackText(fileVersion, dependencyFallbackText(installedVersion, ultraVNCVersion))
		logger.Tracef("UltraVNC executable already present at %s; config version=%s file_version=%s. Skipping setup reinstall.", exePath, installedVersion, dependencyFallbackText(fileVersion, "unknown"))
		if err := writeConfigDependencyVersion(cfg, dependencyNameUltraVNC, version); err != nil {
			logger.Warnf("UltraVNC dependency version write failed: %v", err)
		}
		writeConfigDependencyState(cfg, dependencyNameUltraVNC, "installed", "healthy", ultraVNCVersion, version, exePath, "")
		return nil
	}
	writeConfigDependencyState(cfg, dependencyNameUltraVNC, "installing", "recovering", ultraVNCVersion, installedVersion, "UltraVNC setup starting.", "")
	if err := os.MkdirAll(root, 0755); err != nil {
		return err
	}
	installerPath := filepath.Join(root, ultraVNCInstallerName)
	if !fileExists(installerPath) {
		logger.Tracef("UltraVNC installer missing; downloading to %s", installerPath)
		if err := downloadFileLogged(context.Background(), ultraVNCInstallerURL, installerPath, 240*time.Second, logger); err != nil {
			return err
		}
	}
	actual, err := sha256File(installerPath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(actual, ultraVNCInstallerSHA256) {
		_ = os.Remove(installerPath)
		logger.Warnf("UltraVNC installer checksum mismatch expected=%s actual=%s; redownloading.", ultraVNCInstallerSHA256, actual)
		if err := downloadFileLogged(context.Background(), ultraVNCInstallerURL, installerPath, 240*time.Second, logger); err != nil {
			return err
		}
		actual, err = sha256File(installerPath)
		if err != nil {
			return err
		}
		if !strings.EqualFold(actual, ultraVNCInstallerSHA256) {
			return fmt.Errorf("UltraVNC installer checksum mismatch expected=%s actual=%s", ultraVNCInstallerSHA256, actual)
		}
	}
	if _, err := ensureUltraVNCBootstrapConfig(cfg, logger); err != nil {
		return err
	}
	prepareUltraVNCMSIInstall(logger)
	logPath := filepath.Join(cfg.InstallDir, "Logs", "UltraVNC", "ultravnc-setup.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return err
	}
	logger.Tracef("UltraVNC installer command starting: version=%s installer=%s", ultraVNCVersion, installerPath)
	if _, err := runCommandTimeout(
		logger,
		4*time.Minute,
		installerPath,
		ultraVNCInnoInstallArgs(logPath)...,
	); err != nil {
		return fmt.Errorf("UltraVNC installer failed: %w", err)
	}
	exePath = resolveUltraVNCInstalledExe()
	if exePath == "" {
		return fmt.Errorf("UltraVNC winvnc.exe not found after setup install")
	}
	if err := writeConfigDependencyVersion(cfg, dependencyNameUltraVNC, ultraVNCVersion); err != nil {
		logger.Warnf("UltraVNC dependency version write failed: %v", err)
	}
	writeConfigDependencyState(cfg, dependencyNameUltraVNC, "installed", "healthy", ultraVNCVersion, ultraVNCVersion, exePath, "")
	logger.Infof("UltraVNC %s installed at %s.", ultraVNCVersion, exePath)
	return nil
}

func ensureUltraVNCServer(cfg BootstrapConfig, logger *BootstrapLogger) error {
	if err := ensureUltraVNCSystemInstall(cfg, logger); err != nil {
		return err
	}
	exePath, err := resolveUltraVNCBootstrapExe(cfg)
	if err != nil {
		return err
	}
	configPath, err := ensureUltraVNCBootstrapConfig(cfg, logger)
	if err != nil {
		return err
	}
	if err := ensureUltraVNCServiceRegistration(exePath, configPath, logger); err != nil {
		return err
	}
	writeConfigDependencyState(cfg, dependencyNameUltraVNC, "service_registered", "healthy", ultraVNCVersion, readConfigDependencyVersion(cfg, dependencyNameUltraVNC), ultraVNCServiceName, "")
	return nil
}

func prepareUltraVNCMSIInstall(logger *BootstrapLogger) {
	for _, service := range []string{ultraVNCServiceName, "uvnc_service", "uvnc_service_64", "UltraVNC", "WinVNC"} {
		stopServiceAndWait(service, 20*time.Second, logger)
	}
	removeLegacyUltraVNCServices(logger)
	stopNamedDependencyProcesses(logger, []string{"winvnc.exe", "winvnc64.exe", "uvnc_service.exe"}, []string{"borealis", "ultravnc", "uvnc"})
}

func readWindowsFileVersion(path string, logger *BootstrapLogger) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	script := fmt.Sprintf(`$item = Get-Item -LiteralPath %s -ErrorAction SilentlyContinue; if ($item -and $item.VersionInfo) { $value = $item.VersionInfo.ProductVersion; if (-not $value) { $value = $item.VersionInfo.FileVersion }; if ($value) { (($value -replace ',', '.') -replace '\s+', '') } }`, powershellSingleQuoted(path))
	output, err := runCommandTimeout(logger, 15*time.Second, powershellPath(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	if err != nil {
		if logger != nil {
			logger.Tracef("File version probe failed: path=%s err=%v", path, err)
		}
		return ""
	}
	fields := strings.Fields(strings.TrimSpace(output))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func dependencyFallbackText(value string, fallback string) string {
	text := strings.TrimSpace(value)
	if text == "" {
		return fallback
	}
	return text
}

func resolveUltraVNCBootstrapExe(cfg BootstrapConfig) (string, error) {
	if exePath := resolveUltraVNCInstalledExe(); exePath != "" {
		return exePath, nil
	}
	candidates := []string{
		filepath.Join(cfg.InstallDir, "Agent", "Borealis", "Tools", "UltraVNC", "Server", "winvnc.exe"),
		filepath.Join(cfg.InstallDir, "Agent", "Borealis", "Tools", "UltraVNC", "Server", "winvnc64.exe"),
	}
	for _, candidate := range candidates {
		if fileExists(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("UltraVNC winvnc.exe not found after payload staging")
}

func ensureUltraVNCBootstrapConfig(cfg BootstrapConfig, logger *BootstrapLogger) (string, error) {
	configDir := filepath.Dir(ultraVNCProgramDataConfigPath())
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", err
	}
	configPath := ultraVNCProgramDataConfigPath()
	placeholderHash, err := generateUltraVNCStoredPasswordHash()
	if err != nil {
		return "", err
	}
	captureSettings := ultraVNCCaptureSettings()
	content := "[admin]\n" +
		"UseRegistry=0\n" +
		"AuthRequired=1\n" +
		"MSLogonRequired=0\n" +
		"NewMSLogon=0\n" +
		"primary=1\n" +
		"secondary=1\n" +
		"PortNumber=5900\n" +
		"AutoPortSelect=0\n" +
		"SocketConnect=1\n" +
		"AllowLoopback=1\n" +
		"LoopbackOnly=0\n" +
		"HTTPConnect=0\n" +
		"AllowShutdown=1\n" +
		"DisableTrayIcon=1\n" +
		"EnableFileTransfer=0\n" +
		"FileTransferEnabled=0\n" +
		"RemoveWallpaper=1\n" +
		"\n" +
		"[UltraVNC]\n" +
		"passwd=" + placeholderHash + "\n" +
		"passwd2=\n" +
		"\n" +
		"[poll]\n" +
		"TurboMode=1\n" +
		"PollUnderCursor=0\n" +
		"PollForeground=0\n" +
		"PollFullScreen=1\n" +
		"OnlyPollConsole=0\n" +
		"OnlyPollOnEvent=0\n" +
		"EnableDriver=" + captureSettings.enableDriver + "\n" +
		"EnableHook=" + captureSettings.enableHook + "\n" +
		"EnableVirtual=0\n" +
		"SingleWindow=0\n"
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		return "", err
	}
	legacyConfigPath := ultraVNCLegacyProgramDataConfigPath()
	if !strings.EqualFold(filepath.Clean(legacyConfigPath), filepath.Clean(configPath)) {
		if err := os.WriteFile(legacyConfigPath, []byte(content), 0644); err != nil {
			logger.Warnf("UltraVNC legacy config mirror write failed at %s: %v", legacyConfigPath, err)
		}
	}
	logger.Tracef("UltraVNC bootstrap config written at %s", configPath)
	return configPath, nil
}

type ultraVNCCaptureConfig struct {
	enableDriver string
	enableHook   string
}

func ultraVNCCaptureSettings() ultraVNCCaptureConfig {
	settings := ultraVNCCaptureConfig{enableDriver: "0", enableHook: "0"}
	exePath := resolveUltraVNCInstalledExe()
	if strings.TrimSpace(exePath) == "" {
		return settings
	}
	root := filepath.Dir(exePath)
	if fileExists(filepath.Join(root, "ddengine64.dll")) || fileExists(filepath.Join(root, "ddengine.dll")) {
		settings.enableDriver = "1"
	}
	if fileExists(filepath.Join(root, "vnchooks.dll")) {
		settings.enableHook = "1"
	}
	return settings
}

func generateUltraVNCStoredPasswordHash() (string, error) {
	password := make([]byte, bootstrapUltraVNCPasswordBytes)
	if _, err := rand.Read(password); err != nil {
		return "", err
	}
	// Protocol-required UltraVNC stored-password compatibility. This is not
	// Borealis secret storage, transport encryption, or operator auth crypto.
	block, err := des.NewCipher(bootstrapUltraVNCStoredPasswordDESKey)
	if err != nil {
		return "", err
	}
	encrypted := make([]byte, bootstrapUltraVNCPasswordBytes)
	block.Encrypt(encrypted, password)
	return strings.ToUpper(hex.EncodeToString(encrypted)) + bootstrapUltraVNCPasswordHashSuffix, nil
}

func mirrorUltraVNCBootstrapConfigToServiceDir(exePath string, configPath string, logger *BootstrapLogger) string {
	if strings.TrimSpace(exePath) == "" || strings.TrimSpace(configPath) == "" {
		return configPath
	}
	targetPath := filepath.Join(filepath.Dir(exePath), "ultravnc.ini")
	if samePath(configPath, targetPath) {
		return targetPath
	}
	content, err := os.ReadFile(configPath)
	if err != nil {
		if logger != nil {
			logger.Warnf("UltraVNC bootstrap config mirror skipped: read %s failed: %v", configPath, err)
		}
		return configPath
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		if logger != nil {
			logger.Warnf("UltraVNC bootstrap config mirror skipped: mkdir %s failed: %v", filepath.Dir(targetPath), err)
		}
		return configPath
	}
	if err := os.WriteFile(targetPath, content, 0644); err != nil {
		if logger != nil {
			logger.Warnf("UltraVNC bootstrap config mirror skipped: write %s failed: %v", targetPath, err)
		}
		return configPath
	}
	if logger != nil {
		logger.Tracef("UltraVNC bootstrap config mirrored to %s", targetPath)
	}
	return targetPath
}

func ensureUltraVNCServiceRegistration(exePath string, configPath string, logger *BootstrapLogger) error {
	removeLegacyUltraVNCServices(logger)
	desiredBinPath := fmt.Sprintf(`"%s" -service`, exePath)
	err := createOrUpdateUltraVNCService(desiredBinPath, logger)
	if isWindowsServiceMarkedForDeletionError(err) {
		if logger != nil {
			logger.Tracef("UltraVNC service marked for deletion; waiting before service recreation.")
		}
		waitForServiceDeletion(ultraVNCServiceName, 45*time.Second, logger)
		err = createOrUpdateUltraVNCService(desiredBinPath, logger)
	}
	if err != nil {
		return err
	}
	configureUltraVNCServiceRecovery(logger)
	logger.Tracef("UltraVNC service registration verified: service=%s bin_path=%s", ultraVNCServiceName, desiredBinPath)
	return nil
}

func createOrUpdateUltraVNCService(desiredBinPath string, logger *BootstrapLogger) error {
	if _, err := runCommandTimeout(logger, 20*time.Second, "sc.exe", "query", ultraVNCServiceName); err != nil {
		if _, createErr := runCommandTimeout(
			logger,
			30*time.Second,
			"sc.exe",
			"create",
			ultraVNCServiceName,
			"binPath=",
			desiredBinPath,
			"start=",
			"auto",
			"type=",
			"own",
			"DisplayName=",
			ultraVNCServiceDisplayName,
		); createErr != nil {
			return createErr
		}
		logger.Infof("UltraVNC service registered.")
	}
	_, err := runCommandTimeout(
		logger,
		30*time.Second,
		"sc.exe",
		"config",
		ultraVNCServiceName,
		"binPath=",
		desiredBinPath,
		"start=",
		"auto",
		"DisplayName=",
		ultraVNCServiceDisplayName,
	)
	return err
}

func waitForServiceDeletion(name string, timeout time.Duration, logger *BootstrapLogger) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, exists := queryServiceState(name)
		if !exists {
			if logger != nil {
				logger.Tracef("Service deletion observed: name=%s", name)
			}
			return true
		}
		time.Sleep(1 * time.Second)
	}
	if logger != nil {
		logger.Warnf("Service deletion wait timed out: name=%s", name)
	}
	return false
}

func isWindowsServiceMarkedForDeletionError(err error) bool {
	if err == nil {
		return false
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1072 {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "1072") || strings.Contains(lower, "marked for deletion")
}

func reconcileUltraVNCServiceAfterRuntimeStage(cfg BootstrapConfig, logger *BootstrapLogger) {
	exePath, err := resolveUltraVNCBootstrapExe(cfg)
	if err != nil {
		logger.Warnf("UltraVNC final service reconciliation skipped: %v", err)
		return
	}
	configPath, err := ensureUltraVNCBootstrapConfig(cfg, logger)
	if err != nil {
		logger.Warnf("UltraVNC final service config skipped: %v", err)
		return
	}
	if err := ensureUltraVNCServiceRegistration(exePath, configPath, logger); err != nil {
		logger.Warnf("UltraVNC final service reconciliation failed: %v", err)
		return
	}
	verifyUltraVNCServiceRegistered(logger)
}

func verifyUltraVNCServiceRegistered(logger *BootstrapLogger) {
	state, exists := queryServiceState(ultraVNCServiceName)
	if !exists {
		logger.Warnf("UltraVNC service verification failed: service missing after registration.")
		return
	}
	logger.Tracef("UltraVNC service registration ready: exists=%t state=%s startup=deferred_until_agent_credentials", exists, state)
}

func configureUltraVNCServiceRecovery(logger *BootstrapLogger) {
	_, err := runCommandTimeout(
		logger,
		20*time.Second,
		"sc.exe",
		"failure",
		ultraVNCServiceName,
		"reset=",
		"86400",
		"actions=",
		"restart/5000/restart/15000/restart/30000",
	)
	if err != nil && logger != nil {
		logger.Warnf("UltraVNC service recovery config failed: %v", err)
	}
}

func removeLegacyUltraVNCServices(logger *BootstrapLogger) {
	for _, service := range []string{"uvnc_service", "uvnc_service_64", "UltraVNC", "WinVNC"} {
		if service == ultraVNCServiceName {
			continue
		}
		binPath := queryServiceBinPath(service, logger)
		if binPath == "" || !isRemovableUltraVNCServiceBinPath(binPath) {
			continue
		}
		logger.Tracef("Removing conflicting UltraVNC service: name=%s bin_path=%s", service, binPath)
		stopServiceAndWait(service, 30*time.Second, logger)
		deleteServiceAndWait(service, 30*time.Second, logger)
	}
}

func isRemovableUltraVNCServiceBinPath(binPath string) bool {
	lower := strings.ToLower(strings.ReplaceAll(binPath, "/", `\`))
	if !strings.Contains(lower, "winvnc") {
		return false
	}
	if strings.Contains(lower, `\borealis\`) {
		return true
	}
	if strings.Contains(lower, `\uvnc bvba\ultravnc\`) {
		return true
	}
	if strings.Contains(lower, `\program files\ultravnc\`) {
		return true
	}
	return false
}

func queryServiceBinPath(service string, logger *BootstrapLogger) string {
	output, err := runCommandTimeout(logger, 20*time.Second, "sc.exe", "qc", service)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(strings.ToUpper(line), "BINARY_PATH_NAME") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(parts[1])
		}
	}
	return ""
}

func ensureWireGuardInstaller(cfg BootstrapConfig, logger *BootstrapLogger) error {
	clientExe, err := ensureWireGuardSystemInstall(cfg, logger)
	if err != nil {
		return fmt.Errorf("WireGuard MSI install failed: %w", err)
	}
	if !shouldInstallWireGuardManagerService() {
		removeWireGuardManagerService(clientExe, logger)
		writeConfigDependencyState(cfg, dependencyNameWireGuard, "healthy", "healthy", wireGuardMSIVersion, readConfigDependencyVersion(cfg, dependencyNameWireGuard), "WireGuard client ready; tunnel service installs on demand.", "")
		logger.Infof("WireGuard client ready at %s; manager service disabled to keep bootstrap headless. Tunnel service will install on demand.", clientExe)
		return nil
	}
	logger.Tracef("WireGuard manager service install command starting: %s", clientExe)
	if _, err := runCommandTimeout(logger, 90*time.Second, clientExe, "/installmanagerservice"); err != nil {
		logger.Tracef("WireGuard manager service install skipped: %v", err)
	}
	state, exists := queryServiceState(wireGuardManagerServiceName)
	if !exists {
		writeConfigDependencyState(cfg, dependencyNameWireGuard, "healthy", "healthy", wireGuardMSIVersion, readConfigDependencyVersion(cfg, dependencyNameWireGuard), "WireGuard client ready; manager service absent.", "")
		logger.Infof("WireGuard client ready at %s; manager service not present, tunnel service will install on demand.", clientExe)
		return nil
	}
	ensureWireGuardManagerServiceDisplayName(logger)
	writeConfigDependencyState(cfg, dependencyNameWireGuard, "service_registered", "healthy", wireGuardMSIVersion, readConfigDependencyVersion(cfg, dependencyNameWireGuard), wireGuardManagerServiceName, "")
	logger.Infof("WireGuard manager service installed.")
	logger.Tracef("WireGuard manager service verified: name=%s state=%s client=%s", wireGuardManagerServiceName, state, clientExe)
	return nil
}

func ensureWireGuardSystemInstall(cfg BootstrapConfig, logger *BootstrapLogger) (string, error) {
	clientExe := resolveWireGuardClientExe()
	installedVersion := wireGuardInstalledVersion(cfg, clientExe, logger)
	writeConfigDependencyState(cfg, dependencyNameWireGuard, "detected", "recovering", wireGuardMSIVersion, installedVersion, dependencyFallbackText(clientExe, "WireGuard client executable not found."), "")
	if clientExe != "" {
		if strings.TrimSpace(installedVersion) == "" {
			installedVersion = wireGuardMSIVersion
		}
		if err := writeConfigDependencyVersion(cfg, dependencyNameWireGuard, installedVersion); err != nil {
			logger.Tracef("WireGuard dependency version write skipped: %v", err)
		}
		writeConfigDependencyState(cfg, dependencyNameWireGuard, "installed", "healthy", wireGuardMSIVersion, installedVersion, clientExe, "")
		logger.Tracef("WireGuard Windows client already installed at %s; detected_version=%s desired_version=%s. Skipping MSI install.", clientExe, installedVersion, wireGuardMSIVersion)
		return clientExe, nil
	}
	writeConfigDependencyState(cfg, dependencyNameWireGuard, "installing", "recovering", wireGuardMSIVersion, installedVersion, "WireGuard MSI starting.", "")
	if err := installWireGuardMSI(cfg, logger); err != nil {
		return "", err
	}
	clientExe = waitForWireGuardClientExe(60*time.Second, logger)
	if clientExe == "" {
		return "", fmt.Errorf("WireGuard client executable missing after installer completed")
	}
	if err := writeConfigDependencyVersion(cfg, dependencyNameWireGuard, wireGuardMSIVersion); err != nil {
		logger.Warnf("WireGuard dependency version write failed: %v", err)
	}
	writeConfigDependencyState(cfg, dependencyNameWireGuard, "installed", "healthy", wireGuardMSIVersion, wireGuardMSIVersion, clientExe, "")
	logger.Infof("WireGuard Windows client MSI %s installed at %s.", wireGuardMSIVersion, clientExe)
	return clientExe, nil
}

func wireGuardInstalledVersion(cfg BootstrapConfig, clientExe string, logger *BootstrapLogger) string {
	if strings.TrimSpace(clientExe) != "" {
		if fileVersion := readWindowsFileVersion(clientExe, logger); strings.TrimSpace(fileVersion) != "" {
			return agentconfig.NormalizeDependencyVersion(fileVersion)
		}
		if registryVersion := readWireGuardRegistryVersion(logger); strings.TrimSpace(registryVersion) != "" {
			return agentconfig.NormalizeDependencyVersion(registryVersion)
		}
	}
	return readConfigDependencyVersion(cfg, dependencyNameWireGuard)
}

func readWireGuardRegistryVersion(logger *BootstrapLogger) string {
	for _, key := range []string{
		`HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\` + wireGuardMSIProductCode,
		`HKLM\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\` + wireGuardMSIProductCode,
	} {
		output, err := runCommandTimeout(logger, 15*time.Second, "reg.exe", "query", key, "/v", "DisplayVersion")
		if err != nil {
			continue
		}
		for _, line := range strings.Split(output, "\n") {
			if !strings.Contains(strings.ToLower(line), "displayversion") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) > 0 {
				return fields[len(fields)-1]
			}
		}
	}
	return ""
}

func shouldInstallWireGuardManagerService() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(wireGuardInstallManagerServiceEnvVar)))
	switch value {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func installWireGuardMSI(cfg BootstrapConfig, logger *BootstrapLogger) error {
	msiName, msiURL, msiSHA256, err := wireGuardMSIArtifact()
	if err != nil {
		return err
	}
	msiPath := filepath.Join(cfg.InstallDir, "Dependencies", "VPN_Tunnel_Adapter", msiName)
	if !fileExists(msiPath) {
		logger.Tracef("WireGuard MSI missing; downloading to %s", msiPath)
		if err := downloadFileLogged(context.Background(), msiURL, msiPath, 240*time.Second, logger); err != nil {
			return err
		}
	} else {
		logger.Tracef("WireGuard MSI already present at %s", msiPath)
	}
	actual, err := sha256File(msiPath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(actual, msiSHA256) {
		_ = os.Remove(msiPath)
		logger.Warnf("WireGuard MSI checksum mismatch expected=%s actual=%s; redownloading.", msiSHA256, actual)
		if err := downloadFileLogged(context.Background(), msiURL, msiPath, 240*time.Second, logger); err != nil {
			return err
		}
		actual, err = sha256File(msiPath)
		if err != nil {
			return err
		}
		if !strings.EqualFold(actual, msiSHA256) {
			return fmt.Errorf("WireGuard MSI checksum mismatch expected=%s actual=%s", msiSHA256, actual)
		}
	}
	if resolveWireGuardClientExe() == "" && wireGuardMSIProductRegistered(logger) {
		logger.Tracef("WireGuard MSI registration found but client executable is missing; cleaning stale registration before install.")
		if err := uninstallWireGuardMSIProduct(cfg, msiPath, logger); err != nil {
			logger.Tracef("WireGuard stale MSI registration cleanup returned error; continuing with normal install attempt: %v", err)
		}
	}
	logger.Tracef("WireGuard MSI install command starting.")
	prepareWireGuardMSIInstall(logger)
	logPath := wireGuardMSILogPath(cfg)
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return fmt.Errorf("prepare WireGuard MSI log directory: %w", err)
	}
	_ = os.Remove(logPath)
	err = runWireGuardMSIInstall(logger, msiPath, logPath)
	if isWindowsInstallerSoftSuccess(err) {
		logger.Warnf("WireGuard MSI install returned reboot-needed success; continuing: %v. MSI log: %s", err, logPath)
		return nil
	}
	if err != nil {
		if clientExe := resolveWireGuardClientExe(); clientExe != "" {
			logger.Warnf("WireGuard MSI install returned error, but system client executable exists at %s. Continuing without extraction fallback. MSI log: %s error=%v", clientExe, logPath, err)
			return nil
		}
		logger.Warnf("WireGuard MSI install failed and no system client executable was found; attempting clean MSI uninstall/reinstall. MSI log: %s error=%v", logPath, err)
		if repairErr := repairWireGuardMSIInstall(cfg, msiPath, logger); repairErr == nil {
			return nil
		} else {
			logger.Warnf("WireGuard MSI clean reinstall failed: %v", repairErr)
		}
		if tail := tailTextFile(logPath, 12000); tail != "" {
			logger.Warnf("WireGuard MSI verbose log tail (%s):\n%s", logPath, tail)
		} else {
			logger.Warnf("WireGuard MSI verbose log unavailable or empty: %s", logPath)
		}
		return fmt.Errorf("%w (verbose log: %s)", err, logPath)
	}
	logger.Tracef("WireGuard MSI install command complete. MSI log: %s", logPath)
	return err
}

func runWireGuardMSIInstall(logger *BootstrapLogger, msiPath string, logPath string) error {
	_, err := runCommandTimeout(
		logger,
		300*time.Second,
		"msiexec.exe",
		msiInstallArgs(msiPath, logPath, "DO_NOT_LAUNCH=1")...,
	)
	return err
}

func repairWireGuardMSIInstall(cfg BootstrapConfig, msiPath string, logger *BootstrapLogger) error {
	if err := uninstallWireGuardMSIProduct(cfg, msiPath, logger); err != nil {
		return err
	}
	retryLogPath := filepath.Join(cfg.InstallDir, "Logs", "WireGuard", "wireguard-msi-install-retry.log")
	if err := os.MkdirAll(filepath.Dir(retryLogPath), 0755); err != nil {
		return err
	}
	_ = os.Remove(retryLogPath)
	err := runWireGuardMSIInstall(logger, msiPath, retryLogPath)
	if isWindowsInstallerSoftSuccess(err) {
		logger.Warnf("WireGuard MSI reinstall returned reboot-needed success; continuing: %v. MSI log: %s", err, retryLogPath)
		return nil
	}
	if err != nil {
		if clientExe := resolveWireGuardClientExe(); clientExe != "" {
			logger.Warnf("WireGuard MSI reinstall returned error, but system client executable exists at %s. Continuing. MSI log: %s error=%v", clientExe, retryLogPath, err)
			return nil
		}
		if tail := tailTextFile(retryLogPath, 12000); tail != "" {
			logger.Warnf("WireGuard MSI reinstall log tail (%s):\n%s", retryLogPath, tail)
		}
		return fmt.Errorf("%w (verbose log: %s)", err, retryLogPath)
	}
	logger.Tracef("WireGuard MSI clean reinstall complete. MSI log: %s", retryLogPath)
	return nil
}

func wireGuardMSIProductRegistered(logger *BootstrapLogger) bool {
	for _, key := range []string{
		`HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\` + wireGuardMSIProductCode,
		`HKLM\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\` + wireGuardMSIProductCode,
	} {
		output, err := runCommandTimeout(logger, 15*time.Second, "reg.exe", "query", key, "/v", "DisplayName")
		if err != nil {
			logger.Tracef("WireGuard MSI registration probe missed: key=%s error=%v", key, err)
			continue
		}
		if strings.Contains(strings.ToLower(output), "wireguard") {
			logger.Tracef("WireGuard MSI registration present: key=%s", key)
			return true
		}
	}
	return false
}

func uninstallWireGuardMSIProduct(cfg BootstrapConfig, msiPath string, logger *BootstrapLogger) error {
	prepareWireGuardMSIInstall(logger)
	logPath := filepath.Join(cfg.InstallDir, "Logs", "WireGuard", "wireguard-msi-uninstall.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return err
	}
	_ = os.Remove(logPath)
	logger.Tracef("WireGuard MSI uninstall command starting: msi=%s", msiPath)
	_, err := runCommandTimeout(
		logger,
		180*time.Second,
		"msiexec.exe",
		msiUninstallArgs(msiPath, logPath)...,
	)
	if isWindowsInstallerSoftSuccess(err) || isWindowsInstallerBenignUninstall(err) {
		logger.Warnf("WireGuard MSI uninstall returned benign installer status; continuing: %v. MSI log: %s", err, logPath)
		return nil
	}
	if err != nil {
		if tail := tailTextFile(logPath, 12000); tail != "" {
			logger.Warnf("WireGuard MSI uninstall log tail (%s):\n%s", logPath, tail)
		}
		return fmt.Errorf("%w (verbose log: %s)", err, logPath)
	}
	logger.Tracef("WireGuard MSI uninstall command complete. MSI log: %s", logPath)
	return nil
}

func prepareWireGuardMSIInstall(logger *BootstrapLogger) {
	for _, service := range []string{"WireGuardTunnel$wireguard", "WireGuardTunnel$Borealis", "WireGuardTunnel$borealis-wg"} {
		stopServiceAndWait(service, 20*time.Second, logger)
		deleteServiceAndWait(service, 20*time.Second, logger)
	}
	stopServiceAndWait(wireGuardManagerServiceName, 20*time.Second, logger)
	stopNamedDependencyProcesses(logger, []string{"wireguard.exe"}, []string{"wireguard"})
}

func removeWireGuardManagerService(clientExe string, logger *BootstrapLogger) {
	state, exists := queryServiceState(wireGuardManagerServiceName)
	if !exists {
		logger.Tracef("WireGuard manager service absent; bootstrap remains headless.")
		return
	}
	logger.Tracef("WireGuard manager service removal requested: name=%s state=%s client=%s", wireGuardManagerServiceName, state, clientExe)
	stopServiceAndWait(wireGuardManagerServiceName, 20*time.Second, logger)
	if strings.TrimSpace(clientExe) != "" {
		if _, err := runCommandTimeout(logger, 30*time.Second, clientExe, "/uninstallmanagerservice"); err != nil {
			logger.Tracef("WireGuard manager uninstall command returned: %v", err)
		}
	}
	if _, stillExists := queryServiceState(wireGuardManagerServiceName); stillExists {
		deleteServiceAndWait(wireGuardManagerServiceName, 20*time.Second, logger)
	}
	if state, stillExists := queryServiceState(wireGuardManagerServiceName); stillExists {
		logger.Warnf("WireGuard manager service still present after removal attempt: name=%s state=%s", wireGuardManagerServiceName, state)
	} else {
		logger.Tracef("WireGuard manager service removed; tunnel services remain Agent-controlled.")
	}
}

func wireGuardMSILogPath(cfg BootstrapConfig) string {
	return filepath.Join(cfg.InstallDir, "Logs", "WireGuard", "wireguard-msi-install.log")
}

func tailTextFile(path string, maxBytes int64) string {
	if maxBytes <= 0 {
		return ""
	}
	handle, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer handle.Close()
	info, err := handle.Stat()
	if err != nil {
		return ""
	}
	start := int64(0)
	if info.Size() > maxBytes {
		start = info.Size() - maxBytes
	}
	if _, err := handle.Seek(start, io.SeekStart); err != nil {
		return ""
	}
	bytes, err := io.ReadAll(handle)
	if err != nil {
		return ""
	}
	text := strings.TrimSpace(string(bytes))
	if start > 0 && text != "" {
		text = "... " + text
	}
	return text
}

func isWindowsInstallerSoftSuccess(err error) bool {
	if err == nil {
		return false
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	switch exitErr.ExitCode() {
	case 1641, 3010:
		return true
	default:
		return false
	}
}

func isWindowsInstallerBenignUninstall(err error) bool {
	if err == nil {
		return false
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	switch exitErr.ExitCode() {
	case 1605, 1614:
		return true
	default:
		return false
	}
}

func wireGuardMSIArtifact() (string, string, string, error) {
	arch := runtime.GOARCH
	var suffix string
	var sha256 string
	switch arch {
	case "amd64":
		suffix = "amd64"
		sha256 = wireGuardMSISHA256AMD64
	case "arm64":
		suffix = "arm64"
		sha256 = wireGuardMSISHA256ARM64
	case "386":
		suffix = "x86"
		sha256 = wireGuardMSISHA256X86
	default:
		return "", "", "", fmt.Errorf("unsupported WireGuard Windows MSI architecture: %s", arch)
	}
	name := fmt.Sprintf("wireguard-%s-%s.msi", suffix, wireGuardMSIVersion)
	return name, wireGuardDownloadBaseURL + "/" + name, sha256, nil
}

func waitForWireGuardClientExe(timeout time.Duration, logger *BootstrapLogger) string {
	deadline := time.Now().Add(timeout)
	for {
		if clientExe := resolveWireGuardClientExe(); clientExe != "" {
			logger.Tracef("WireGuard client executable detected: %s", clientExe)
			return clientExe
		}
		if time.Now().After(deadline) {
			return ""
		}
		time.Sleep(1 * time.Second)
	}
}

func resolveWireGuardClientExe() string {
	programFiles := os.Getenv("ProgramFiles")
	if programFiles == "" {
		programFiles = `C:\Program Files`
	}
	programFilesX86 := os.Getenv("ProgramFiles(x86)")
	if programFilesX86 == "" {
		programFilesX86 = `C:\Program Files (x86)`
	}
	for _, candidate := range []string{
		filepath.Join(programFiles, "WireGuard", "wireguard.exe"),
		filepath.Join(programFilesX86, "WireGuard", "wireguard.exe"),
	} {
		if fileExists(candidate) {
			return candidate
		}
	}
	if path, err := exec.LookPath("wireguard.exe"); err == nil {
		return path
	}
	return ""
}

func ensureWireGuardManagerServiceDisplayName(logger *BootstrapLogger) {
	_, err := runCommandTimeout(
		logger,
		30*time.Second,
		"sc.exe",
		"config",
		wireGuardManagerServiceName,
		"DisplayName=",
		wireGuardManagerDisplayName,
	)
	if err != nil {
		logger.Tracef("WireGuard manager display-name update skipped: %v", err)
	}
}
