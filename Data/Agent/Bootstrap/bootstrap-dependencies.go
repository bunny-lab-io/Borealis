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
	"strings"
	"time"
)

const (
	ultraVNCServiceName                  = "BorealisAgentUltraVNC"
	ultraVNCServiceDisplayName           = "Borealis Agent - UltraVNC"
	ultraVNCVersion                      = "1.8.2.1"
	ultraVNCMSIName                      = "UltraVNC_1821_x64_Setup.msi"
	ultraVNCMSIURL                       = "https://uvnc.eu/download/1800/UltraVNC_1821_x64_Setup.msi"
	ultraVNCMSISHA256                    = "cc7a41d546523dc5e33324b12a23d2fbb2d0a9b0b9f7c08b0e242ebe5da3c2b9"
	wireGuardManagerServiceName          = "WireGuardManager"
	wireGuardManagerDisplayName          = "Borealis Agent - WireGuard Manager"
	wireGuardInstallManagerServiceEnvVar = "BOREALIS_WIREGUARD_INSTALL_MANAGER_SERVICE"
	wireGuardDownloadBaseURL             = "https://download.wireguard.com/windows-client"
	wireGuardMSIVersion                  = "1.1"
	wireGuardMSISHA256AMD64              = "6daa5d37a9e2950dfb8c48b95ab8e562cb2bad1c785d020f38f97bea4c6a5566"
	wireGuardMSISHA256ARM64              = "a2a67fbb2db199525c35ce79ea6dd9031b116ba46561f2b993fb858668440131"
	wireGuardMSISHA256X86                = "71811698d544607e6bd94bbfff14e936b186da53b2934ff74d736daa74105481"
)

func ensureAgentDependencies(cfg BootstrapConfig, logger *BootstrapLogger) error {
	startedAt := time.Now()
	logger.Tracef("Dependency coordinator start.")
	writeTimeline(cfg, "running", dependencyTaskName("Python"), "Installing Python runtime.", 1)
	if err := ensurePythonRuntime(cfg, logger); err != nil {
		return err
	}
	writeTimeline(cfg, "completed", dependencyTaskName("Python"), "Python dependency ready.", 0)
	optionalSteps := []struct {
		name string
		fn   func(BootstrapConfig, *BootstrapLogger) error
	}{
		{"Git CLI", ensureGitCLI},
		{"UltraVNC Server", ensureUltraVNCServer},
		{"WireGuard VPN Adapter", ensureWireGuardInstaller},
	}
	for _, step := range optionalSteps {
		stepStartedAt := time.Now()
		task := dependencyTaskName(step.name)
		logger.Tracef("Dependency step start: %s", step.name)
		writeTimeline(cfg, "running", task, "Installing "+step.name+".", 1)
		if err := step.fn(cfg, logger); err != nil {
			logger.Warnf("%s dependency deferred: %v", step.name, err)
			writeTimeline(cfg, "failed", task, step.name+" dependency deferred: "+err.Error(), 1)
			logger.Tracef("Dependency step deferred: %s duration=%s error=%v", step.name, time.Since(stepStartedAt).Round(time.Millisecond), err)
			continue
		}
		writeTimeline(cfg, "completed", task, step.name+" dependency ready.", 0)
		logger.Tracef("Dependency step complete: %s duration=%s", step.name, time.Since(stepStartedAt).Round(time.Millisecond))
	}
	logger.Tracef("Dependency coordinator complete duration=%s.", time.Since(startedAt).Round(time.Millisecond))
	return nil
}

func dependencyTaskName(name string) string {
	return "Installing Agent Dependencies: " + name
}

func ensureGitCLI(cfg BootstrapConfig, logger *BootstrapLogger) error {
	gitExe := filepath.Join(cfg.InstallDir, "Dependencies", "git", "cmd", "git.exe")
	if fileExists(gitExe) {
		logger.Tracef("Git CLI already installed at %s", gitExe)
		return nil
	}
	zipPath := filepath.Join(cfg.InstallDir, "Dependencies", "MinGit-2.47.1-64-bit.zip")
	logger.Tracef("Git CLI download/stage start: zip=%s", zipPath)
	if err := downloadFileLogged(context.Background(), "https://github.com/git-for-windows/git/releases/download/v2.47.1.windows.1/MinGit-2.47.1-64-bit.zip", zipPath, 240*time.Second, logger); err != nil {
		return err
	}
	installDir := filepath.Join(cfg.InstallDir, "Dependencies", "git")
	_ = os.RemoveAll(installDir)
	if err := unzipFileLogged(zipPath, installDir, logger); err != nil {
		return err
	}
	_ = os.Remove(zipPath)
	if !fileExists(gitExe) {
		return fmt.Errorf("git.exe not found after extraction")
	}
	logger.Infof("Git CLI installed.")
	return nil
}

func ultraVNCProgramDataConfigPath() string {
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
	versionPath := filepath.Join(root, "installed_version.txt")
	exePath := resolveUltraVNCInstalledExe()
	installedVersion := strings.TrimSpace(readFirstLine(versionPath))
	if exePath != "" && installedVersion == ultraVNCVersion {
		logger.Tracef("UltraVNC %s already installed at %s", ultraVNCVersion, exePath)
		return nil
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return err
	}
	msiPath := filepath.Join(root, ultraVNCMSIName)
	if !fileExists(msiPath) {
		logger.Tracef("UltraVNC MSI missing; downloading to %s", msiPath)
		if err := downloadFileLogged(context.Background(), ultraVNCMSIURL, msiPath, 240*time.Second, logger); err != nil {
			return err
		}
	}
	actual, err := sha256File(msiPath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(actual, ultraVNCMSISHA256) {
		_ = os.Remove(msiPath)
		logger.Warnf("UltraVNC MSI checksum mismatch expected=%s actual=%s; redownloading.", ultraVNCMSISHA256, actual)
		if err := downloadFileLogged(context.Background(), ultraVNCMSIURL, msiPath, 240*time.Second, logger); err != nil {
			return err
		}
		actual, err = sha256File(msiPath)
		if err != nil {
			return err
		}
		if !strings.EqualFold(actual, ultraVNCMSISHA256) {
			return fmt.Errorf("UltraVNC MSI checksum mismatch expected=%s actual=%s", ultraVNCMSISHA256, actual)
		}
	}
	if _, err := ensureUltraVNCBootstrapConfig(cfg, logger); err != nil {
		return err
	}
	logPath := filepath.Join(cfg.InstallDir, "Agent", "Logs", "ultravnc-msi-install.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return err
	}
	logger.Tracef("UltraVNC MSI install command starting: version=%s msi=%s", ultraVNCVersion, msiPath)
	if _, err := runCommandTimeout(
		logger,
		4*time.Minute,
		"msiexec.exe",
		"/i",
		msiPath,
		"/qn",
		"/norestart",
		"DO_NOT_LAUNCH=1",
		"/l*v",
		logPath,
	); err != nil {
		return fmt.Errorf("UltraVNC MSI install failed: %w", err)
	}
	exePath = resolveUltraVNCInstalledExe()
	if exePath == "" {
		return fmt.Errorf("UltraVNC winvnc.exe not found after MSI install")
	}
	if err := os.WriteFile(versionPath, []byte(ultraVNCVersion+"\n"), 0644); err != nil {
		logger.Warnf("UltraVNC version marker write failed: %v", err)
	}
	logger.Infof("UltraVNC %s installed at %s.", ultraVNCVersion, exePath)
	return nil
}

func ensureUltraVNCPayload(cfg BootstrapConfig, logger *BootstrapLogger) error {
	root := filepath.Join(cfg.InstallDir, "Dependencies", "UltraVNC_Server")
	if fileExists(filepath.Join(root, "payload", "x64", "winvnc.exe")) || fileExists(filepath.Join(root, "payload", "x86", "winvnc.exe")) {
		logger.Tracef("UltraVNC payload already present under %s", root)
		return nil
	}
	zipPath := filepath.Join(root, "UltraVNC_1640.zip")
	logger.Tracef("UltraVNC payload download/stage start: zip=%s", zipPath)
	if err := downloadFileLogged(context.Background(), "https://uvnc.eu/download/1640/UltraVNC_1640.zip", zipPath, 240*time.Second, logger); err != nil {
		return err
	}
	payloadRoot := filepath.Join(root, "payload")
	_ = os.RemoveAll(payloadRoot)
	if err := unzipFileLogged(zipPath, payloadRoot, logger); err != nil {
		return err
	}
	logger.Infof("UltraVNC payload staged.")
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
	return ensureUltraVNCServiceRegistration(exePath, configPath, logger)
}

func resolveUltraVNCBootstrapExe(cfg BootstrapConfig) (string, error) {
	if exePath := resolveUltraVNCInstalledExe(); exePath != "" {
		return exePath, nil
	}
	candidates := []string{
		filepath.Join(cfg.InstallDir, "Agent", "Borealis", "Tools", "UltraVNC", "Server", "winvnc.exe"),
		filepath.Join(cfg.InstallDir, "Agent", "Borealis", "Tools", "UltraVNC", "Server", "winvnc64.exe"),
		filepath.Join(cfg.InstallDir, "Dependencies", "UltraVNC_Server", "payload", "x64", "winvnc.exe"),
		filepath.Join(cfg.InstallDir, "Dependencies", "UltraVNC_Server", "payload", "x64", "winvnc64.exe"),
		filepath.Join(cfg.InstallDir, "Dependencies", "UltraVNC_Server", "payload", "x86", "winvnc.exe"),
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
	configPath := filepath.Join(configDir, "ultravnc.ini")
	placeholderHash, err := generateUltraVNCStoredPasswordHash()
	if err != nil {
		return "", err
	}
	content := "[UltraVNC]\n" +
		"UseRegistry=0\n" +
		"AuthRequired=1\n" +
		"MSLogonRequired=0\n" +
		"NewMSLogon=0\n" +
		"PortNumber=5900\n" +
		"AutoPortSelect=0\n" +
		"SocketConnect=1\n" +
		"AllowLoopback=1\n" +
		"LoopbackOnly=0\n" +
		"HTTPConnect=0\n" +
		"AllowShutdown=1\n" +
		"DisableTrayIcon=1\n" +
		"EnableFileTransfer=0\n" +
		"RemoveWallpaper=1\n" +
		"passwd=" + placeholderHash + "\n" +
		"passwd2=\n"
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		return "", err
	}
	logger.Tracef("UltraVNC bootstrap config written at %s", configPath)
	return configPath, nil
}

func generateUltraVNCStoredPasswordHash() (string, error) {
	password := make([]byte, 8)
	if _, err := rand.Read(password); err != nil {
		return "", err
	}
	block, err := des.NewCipher([]byte{23, 82, 107, 6, 35, 78, 88, 7})
	if err != nil {
		return "", err
	}
	encrypted := make([]byte, 8)
	block.Encrypt(encrypted, password)
	return strings.ToUpper(hex.EncodeToString(encrypted)) + "00", nil
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
	if err != nil {
		return err
	}
	configureUltraVNCServiceRecovery(logger)
	logger.Tracef("UltraVNC service registration verified: service=%s bin_path=%s", ultraVNCServiceName, desiredBinPath)
	return nil
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
		logger.Infof("WireGuard client ready at %s; manager service disabled to keep bootstrap headless. Tunnel service will install on demand.", clientExe)
		return nil
	}
	logger.Tracef("WireGuard manager service install command starting: %s", clientExe)
	if _, err := runCommandTimeout(logger, 90*time.Second, clientExe, "/installmanagerservice"); err != nil {
		logger.Tracef("WireGuard manager service install skipped: %v", err)
	}
	state, exists := queryServiceState(wireGuardManagerServiceName)
	if !exists {
		logger.Infof("WireGuard client ready at %s; manager service not present, tunnel service will install on demand.", clientExe)
		return nil
	}
	ensureWireGuardManagerServiceDisplayName(logger)
	logger.Infof("WireGuard manager service installed.")
	logger.Tracef("WireGuard manager service verified: name=%s state=%s client=%s", wireGuardManagerServiceName, state, clientExe)
	return nil
}

func ensureWireGuardSystemInstall(cfg BootstrapConfig, logger *BootstrapLogger) (string, error) {
	root := filepath.Join(cfg.InstallDir, "Dependencies", "VPN_Tunnel_Adapter")
	versionPath := filepath.Join(root, "installed_version.txt")
	clientExe := resolveWireGuardClientExe()
	installedVersion := strings.TrimSpace(readFirstLine(versionPath))
	if clientExe != "" && installedVersion == wireGuardMSIVersion {
		logger.Tracef("WireGuard Windows client MSI %s already installed at %s", wireGuardMSIVersion, clientExe)
		return clientExe, nil
	}
	if err := installWireGuardMSI(cfg, logger); err != nil {
		return "", err
	}
	clientExe = waitForWireGuardClientExe(60*time.Second, logger)
	if clientExe == "" {
		return "", fmt.Errorf("WireGuard client executable missing after installer completed")
	}
	if err := os.WriteFile(versionPath, []byte(wireGuardMSIVersion+"\n"), 0644); err != nil {
		logger.Warnf("WireGuard version marker write failed: %v", err)
	}
	logger.Infof("WireGuard Windows client MSI %s installed at %s.", wireGuardMSIVersion, clientExe)
	return clientExe, nil
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
	logger.Tracef("WireGuard MSI install command starting.")
	prepareWireGuardMSIInstall(logger)
	logPath := wireGuardMSILogPath(cfg)
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return fmt.Errorf("prepare WireGuard MSI log directory: %w", err)
	}
	_ = os.Remove(logPath)
	_, err = runCommandTimeout(
		logger,
		300*time.Second,
		"msiexec.exe",
		"/i",
		msiPath,
		"/qn",
		"/norestart",
		"DO_NOT_LAUNCH=1",
		"/l*v",
		logPath,
	)
	if isWindowsInstallerSoftSuccess(err) {
		logger.Warnf("WireGuard MSI install returned reboot-needed success; continuing: %v. MSI log: %s", err, logPath)
		return nil
	}
	if err != nil {
		if clientExe := resolveWireGuardClientExe(); clientExe != "" {
			logger.Warnf("WireGuard MSI install returned error, but system client executable exists at %s. Continuing without extraction fallback. MSI log: %s error=%v", clientExe, logPath, err)
			return nil
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

func prepareWireGuardMSIInstall(logger *BootstrapLogger) {
	for _, service := range []string{"WireGuardTunnel$Borealis", "WireGuardTunnel$borealis-wg"} {
		stopServiceAndWait(service, 20*time.Second, logger)
		deleteServiceAndWait(service, 20*time.Second, logger)
	}
	stopServiceAndWait(wireGuardManagerServiceName, 20*time.Second, logger)
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
	return filepath.Join(cfg.InstallDir, "Agent", "Logs", "wireguard-msi-install.log")
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
