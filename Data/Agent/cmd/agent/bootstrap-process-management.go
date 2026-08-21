//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	agentruntime "github.com/bunny-lab-io/borealis/go-agent/internal/runtime"
	"golang.org/x/sys/windows"
)

const waitAbandoned = 0x00000080

type windowsProcessInfo struct {
	ProcessID      uint32 `json:"ProcessId"`
	ExecutablePath string `json:"ExecutablePath"`
	CommandLine    string `json:"CommandLine"`
}

type windowsServiceProcessInfo struct {
	Name      string `json:"Name"`
	State     string `json:"State"`
	ProcessID uint32 `json:"ProcessId"`
	PathName  string `json:"PathName"`
}

type deferredAgentReplacement struct {
	Pending        string
	Destination    string
	ExpectedSHA256 string
}

func acquireBootstrapMutex() (func(), bool, error) {
	name, err := windows.UTF16PtrFromString(`Global\BorealisAgentBootstrapper`)
	if err != nil {
		return func() {}, false, err
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		return func() {}, false, err
	}
	waitResult, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		_ = windows.CloseHandle(handle)
		return func() {}, false, err
	}
	if waitResult != windows.WAIT_OBJECT_0 && waitResult != waitAbandoned {
		_ = windows.CloseHandle(handle)
		return func() {}, false, nil
	}
	release := func() {
		_ = windows.ReleaseMutex(handle)
		_ = windows.CloseHandle(handle)
	}
	return release, true, nil
}

func runCommand(ctx context.Context, logger *BootstrapLogger, name string, args ...string) (string, error) {
	startedAt := time.Now()
	if logger != nil {
		logger.Tracef("Command start: %s %s timeout_set=%t", name, strings.Join(args, " "), ctx != nil)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if text != "" {
		logger.Infof("%s", text)
	}
	if ctx.Err() != nil {
		if logger != nil {
			logger.Tracef("Command timeout: %s duration=%s error=%v", name, time.Since(startedAt).Round(time.Millisecond), ctx.Err())
		}
		return text, ctx.Err()
	}
	if err != nil {
		if logger != nil {
			logger.Tracef("Command failed: %s duration=%s error=%v", name, time.Since(startedAt).Round(time.Millisecond), err)
		}
		return text, fmt.Errorf("%s %s failed: %w", name, strings.Join(args, " "), err)
	}
	if logger != nil {
		logger.Tracef("Command complete: %s duration=%s output_bytes=%d", name, time.Since(startedAt).Round(time.Millisecond), len(output))
	}
	return text, nil
}

func runCommandTimeout(logger *BootstrapLogger, timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return runCommand(ctx, logger, name, args...)
}

func stopBorealisProcesses(cfg BootstrapConfig, logger *BootstrapLogger) {
	installRoot := strings.ToLower(filepath.Clean(cfg.InstallDir))
	logger.Tracef("Scanning for stale Borealis processes under %s.", cfg.InstallDir)
	killed := 0
	processNames := []string{
		"powershell.exe",
		"Agent.exe",
		"winvnc.exe",
		"winvnc64.exe",
		"wireguard.exe",
	}
	for _, name := range processNames {
		logger.Tracef("Process scan start: image=%s", name)
		err := eachProcess(func(pid uint32, exe string, commandLine string) {
			lowerCmd := strings.ToLower(commandLine + " " + exe)
			if !strings.Contains(lowerCmd, strings.ToLower(installRoot)) {
				return
			}
			if int(pid) == os.Getpid() {
				return
			}
			logger.Tracef("Stale process matched: pid=%d exe=%s command_line=%s", pid, exe, commandLine)
			logger.Marker("__BOREALIS_ONBOARDING_STALE_PROCESS_KILLED__=" + strconv.Itoa(int(pid)))
			killProcessTree(int(pid))
			killed++
		}, name)
		if err != nil {
			logger.Tracef("Process scan failed: image=%s error=%v", name, err)
		}
	}
	logger.Tracef("Stale process scan complete: killed=%d", killed)
}

func stopBorealisOwnedServiceForUpdate(name string, cfg BootstrapConfig, timeout time.Duration, logger *BootstrapLogger) error {
	before, _ := queryWindowsServiceProcessInfo(name)
	stopServiceAndWait(name, timeout, logger)
	state, exists := queryServiceState(name)
	if !exists || strings.EqualFold(state, "STOPPED") {
		return nil
	}
	if before.ProcessID == 0 || !isBorealisOwnedServiceProcess(name, before.PathName, cfg.InstallDir) {
		return fmt.Errorf("managed service %s stayed %s and process ownership could not be verified", name, state)
	}
	logger.Tracef("Managed service graceful stop timed out; terminating verified service process: name=%s pid=%d path=%s", name, before.ProcessID, before.PathName)
	killProcessTree(int(before.ProcessID))
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		state, exists = queryServiceState(name)
		if !exists || strings.EqualFold(state, "STOPPED") {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("managed service %s did not stop after scoped process termination", name)
}

func queryWindowsServiceProcessInfo(name string) (windowsServiceProcessInfo, error) {
	escapedName := strings.ReplaceAll(strings.TrimSpace(name), "'", "''")
	script := fmt.Sprintf(
		`$ErrorActionPreference='Stop'; $item = Get-CimInstance Win32_Service -Filter "Name='%s'" | Select-Object Name,State,ProcessId,PathName; if ($item) { ConvertTo-Json -InputObject $item -Compress -Depth 3 }`,
		escapedName,
	)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, powershellPath(), "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if ctx.Err() != nil {
		return windowsServiceProcessInfo{}, ctx.Err()
	}
	if err != nil {
		return windowsServiceProcessInfo{}, fmt.Errorf("query service process %s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	if strings.TrimSpace(string(output)) == "" {
		return windowsServiceProcessInfo{}, nil
	}
	var info windowsServiceProcessInfo
	if err := json.Unmarshal(output, &info); err != nil {
		return windowsServiceProcessInfo{}, err
	}
	return info, nil
}

func isBorealisOwnedServiceProcess(serviceName string, pathName string, installDir string) bool {
	exePath := windowsServiceExecutablePath(pathName)
	if exePath == "" {
		return false
	}
	base := strings.ToLower(filepath.Base(exePath))
	switch strings.ToLower(strings.TrimSpace(serviceName)) {
	case strings.ToLower(agentruntime.WindowsServiceName):
		return samePath(exePath, filepath.Join(installDir, "Agent.exe"))
	case strings.ToLower(ultraVNCServiceName):
		return base == "winvnc.exe" || base == "winvnc64.exe"
	case strings.ToLower(wireGuardManagerServiceName),
		"borealiswireguardtunnel",
		"wireguardtunnel$wireguard",
		"wireguardtunnel$borealis",
		"wireguardtunnel$borealis-wg":
		return base == "wireguard.exe"
	default:
		return false
	}
}

func windowsServiceExecutablePath(pathName string) string {
	value := strings.TrimSpace(pathName)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, `"`) {
		if end := strings.Index(value[1:], `"`); end >= 0 {
			return strings.TrimSpace(value[1 : end+1])
		}
	}
	if index := strings.IndexAny(value, " \t"); index >= 0 {
		value = value[:index]
	}
	return strings.Trim(value, `"`)
}

func killProcessTree(pid int) {
	if pid <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "taskkill.exe", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
}

func eachProcess(callback func(pid uint32, exe string, commandLine string), imageName string) error {
	if err := eachProcessPowerShell(callback, imageName); err == nil {
		return nil
	} else {
		wmicErr := eachProcessWMIC(callback, imageName)
		if wmicErr == nil {
			return nil
		}
		return fmt.Errorf("powershell process query failed: %v; wmic process query failed: %w", err, wmicErr)
	}
}

func eachProcessPowerShell(callback func(pid uint32, exe string, commandLine string), imageName string) error {
	escapedName := strings.ReplaceAll(imageName, "'", "''")
	script := fmt.Sprintf(
		`$ErrorActionPreference='Stop'; $items = @(Get-CimInstance Win32_Process -Filter "Name='%s'" | Select-Object ProcessId,ExecutablePath,CommandLine); if ($items.Count -gt 0) { ConvertTo-Json -InputObject $items -Compress -Depth 3 }`,
		escapedName,
	)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, powershellPath(), "-NoProfile", "-NonInteractive", "-Command", script)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	raw := strings.TrimSpace(string(output))
	if raw == "" || raw == "null" {
		return nil
	}
	var processes []windowsProcessInfo
	if err := json.Unmarshal([]byte(raw), &processes); err != nil {
		var process windowsProcessInfo
		if singleErr := json.Unmarshal([]byte(raw), &process); singleErr != nil {
			return err
		}
		processes = []windowsProcessInfo{process}
	}
	for _, process := range processes {
		if process.ProcessID == 0 {
			continue
		}
		callback(process.ProcessID, process.ExecutablePath, process.CommandLine)
	}
	return nil
}

func eachProcessWMIC(callback func(pid uint32, exe string, commandLine string), imageName string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "wmic.exe", "process", "where", fmt.Sprintf("name='%s'", imageName), "get", "ProcessId,ExecutablePath,CommandLine", "/FORMAT:CSV")
	output, err := cmd.Output()
	if err != nil {
		return err
	}
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(strings.ToLower(line), "node,") {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) < 4 {
			continue
		}
		pidRaw := strings.TrimSpace(parts[len(parts)-1])
		pid64, err := strconv.ParseUint(pidRaw, 10, 32)
		if err != nil {
			continue
		}
		commandLine := strings.Join(parts[1:len(parts)-2], ",")
		exe := parts[len(parts)-2]
		callback(uint32(pid64), exe, commandLine)
	}
	return nil
}

func currentHostname() string {
	candidates := []string{}
	if hostname, err := os.Hostname(); err == nil {
		candidates = append(candidates, hostname)
	}
	candidates = append(candidates, os.Getenv("COMPUTERNAME"))
	for _, candidate := range candidates {
		value := strings.TrimSpace(candidate)
		value = strings.Trim(value, "[]")
		if value == "" {
			continue
		}
		if index := strings.IndexAny(value, "\r\n\t "); index >= 0 {
			value = strings.TrimSpace(value[:index])
		}
		if value != "" {
			return value
		}
	}
	return ""
}

func isInteractiveConsole() bool {
	var mode uint32
	handle := windows.Handle(os.Stdin.Fd())
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return false
	}
	return true
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func ensureBootstrapDirs(cfg BootstrapConfig) error {
	paths := []string{
		cfg.InstallDir,
		filepath.Join(cfg.InstallDir, "Logs"),
		filepath.Join(cfg.InstallDir, "Logs", "Agent"),
		filepath.Join(cfg.InstallDir, "Logs", "UltraVNC"),
		filepath.Join(cfg.InstallDir, "Logs", "WireGuard"),
		filepath.Join(cfg.InstallDir, "Temp", "Onboarding"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(path, 0755); err != nil {
			return err
		}
	}
	return nil
}

func copySelfToInstallRoot(cfg BootstrapConfig, logger *BootstrapLogger) (*deferredAgentReplacement, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	destination := filepath.Join(cfg.InstallDir, "Agent.exe")
	logger.Tracef("Agent.exe self-stage check: source=%s destination=%s same_path=%t", exe, destination, samePath(exe, destination))
	if samePath(exe, destination) {
		logger.Tracef("Agent.exe already running from install root.")
		return nil, nil
	}
	expectedSHA256, err := sha256File(exe)
	if err != nil {
		return nil, err
	}
	pending := destination + ".redeploy"
	if err := copyFile(exe, pending); err != nil {
		return nil, err
	}
	if err := verifyFileSHA256(pending, expectedSHA256); err != nil {
		_ = os.Remove(pending)
		return nil, err
	}
	if fileExists(agentConfigPath(cfg.InstallDir)) {
		if err := validateAgentUpdateCandidate(cfg, pending, "redeploy", logger); err != nil {
			_ = os.Remove(pending)
			return nil, err
		}
	}
	if err := copyFile(pending, destination); err != nil {
		logger.Warnf("Agent.exe direct replacement deferred: %v", err)
		logger.Marker("__BOREALIS_ONBOARDING_DEFERRED_REPLACEMENT__=1")
		return &deferredAgentReplacement{
			Pending:        pending,
			Destination:    destination,
			ExpectedSHA256: expectedSHA256,
		}, nil
	}
	if err := verifyFileSHA256(destination, expectedSHA256); err != nil {
		return nil, err
	}
	_ = os.Remove(pending)
	logger.Infof("Agent.exe staged at %s sha256=%s", destination, expectedSHA256)
	return nil, nil
}

func (r *deferredAgentReplacement) Schedule(cfg BootstrapConfig, logger *BootstrapLogger) error {
	if r == nil {
		return nil
	}
	if logger != nil {
		logger.Tracef("Scheduling deferred Agent redeploy replacement: pending=%s destination=%s expected_sha256=%s", r.Pending, r.Destination, r.ExpectedSHA256)
	}
	script := deferredRedeployReplacementScript(cfg, r.Pending, r.Destination, r.ExpectedSHA256)
	cmd := exec.Command(
		"powershell.exe",
		"-NoProfile",
		"-ExecutionPolicy",
		"Bypass",
		"-EncodedCommand",
		encodePowerShellCommand(script),
	)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func deferredRedeployReplacementScript(cfg BootstrapConfig, pending string, destination string, expectedSHA256 string) string {
	logPath := filepath.Join(cfg.InstallDir, "Logs", "Agent", "bootstrap.log")
	configPath := agentConfigPath(cfg.InstallDir)
	return fmt.Sprintf(`
$ErrorActionPreference = 'Continue'
$logPath = %s
$pending = %s
$destination = %s
$configPath = %s
$installDir = %s
$expectedSha256 = %s
$agentServiceName = %s
$validateExe = $pending + ".validate.exe"
$tasks = @(%s, %s, %s)
$services = @(%s, %s, %s, %s, %s, %s, %s)
New-Item -ItemType Directory -Force -Path (Split-Path -Parent $logPath) | Out-Null
function Write-BootstrapLog([string]$message) {
  Add-Content -LiteralPath $logPath -Value ("[{0}] {1}" -f (Get-Date).ToString("s"), $message)
}
function Stop-BorealisComponents {
  foreach ($task in $tasks) {
    schtasks.exe /End /TN $task *> $null
  }
  foreach ($service in $services) {
    sc.exe stop $service *> $null
  }
  Start-Sleep -Milliseconds 750
  try {
    $installPrefix = $installDir.ToLowerInvariant()
    while ($installPrefix.EndsWith('\') -or $installPrefix.EndsWith('/')) {
      $installPrefix = $installPrefix.Substring(0, $installPrefix.Length - 1)
    }
    $installPrefix = $installPrefix + '\'
    Get-CimInstance Win32_Process -ErrorAction SilentlyContinue |
      Where-Object {
        $_.ProcessId -ne $PID -and
        $_.ExecutablePath -and
        $_.ExecutablePath.ToLowerInvariant().StartsWith($installPrefix)
      } |
      ForEach-Object {
        Write-BootstrapLog ("Stopping Borealis process pid={0} path={1}" -f $_.ProcessId, $_.ExecutablePath)
        Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue
      }
  } catch {
    Write-BootstrapLog ("Process cleanup skipped: " + $_.Exception.Message)
  }
}
function Invoke-PendingAgentValidation {
  Remove-Item -LiteralPath $validateExe -Force -ErrorAction SilentlyContinue
  Copy-Item -LiteralPath $pending -Destination $validateExe -Force -ErrorAction Stop
  try {
    $validateOutput = & $validateExe --validate-config --config-path $configPath 2>&1
    foreach ($line in $validateOutput) { Write-BootstrapLog ("validate-pending: " + $line) }
    if ($LASTEXITCODE -ne 0) {
      throw "pending Agent.exe rejected current agent.json with exit $LASTEXITCODE"
    }
  } finally {
    Remove-Item -LiteralPath $validateExe -Force -ErrorAction SilentlyContinue
  }
}
function Ensure-AgentServiceRunning {
  $installOutput = & $destination --install-service --config-path $configPath 2>&1
  foreach ($line in $installOutput) { Write-BootstrapLog ("install-service: " + $line) }
  if ($LASTEXITCODE -ne 0) {
    Write-BootstrapLog "install-service exited $LASTEXITCODE."
  }
  for ($attempt = 1; $attempt -le 15; $attempt++) {
    try { Start-Service -Name $agentServiceName -ErrorAction SilentlyContinue } catch {}
    $service = Get-CimInstance Win32_Service -Filter ("Name='{0}'" -f $agentServiceName) -ErrorAction SilentlyContinue
    $state = if ($service) { [string]$service.State } else { "" }
    Write-BootstrapLog "Service verification attempt $attempt state=$state."
    if ($state -eq "Running") { return $true }
    Start-Sleep -Seconds 2
  }
  return $false
}
Write-BootstrapLog "Deferred redeploy replacement starting. pending=$pending destination=$destination expected_sha256=$expectedSha256"
Start-Sleep -Seconds 3
for ($attempt = 1; $attempt -le 30; $attempt++) {
  try {
    if (!(Test-Path -LiteralPath $pending)) {
      if ((Test-Path -LiteralPath $destination) -and (((Get-FileHash -LiteralPath $destination -Algorithm SHA256).Hash.ToLowerInvariant()) -eq $expectedSha256)) {
        Write-BootstrapLog "Pending binary already applied."
        if (!(Ensure-AgentServiceRunning)) { throw "service did not reach Running after existing replacement" }
        exit 0
      }
      throw "pending binary missing"
    }
    Invoke-PendingAgentValidation
    Stop-BorealisComponents
    Move-Item -LiteralPath $pending -Destination $destination -Force -ErrorAction Stop
    $actualSha256 = (Get-FileHash -LiteralPath $destination -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actualSha256 -ne $expectedSha256) {
      throw "hash mismatch actual=$actualSha256 expected=$expectedSha256"
    }
    if (!(Ensure-AgentServiceRunning)) {
      throw "service did not reach Running after replacement"
    }
    Write-BootstrapLog "Deferred redeploy replacement complete."
    exit 0
  } catch {
    Write-BootstrapLog ("Attempt $attempt failed: " + $_.Exception.Message)
    Start-Sleep -Seconds 2
  }
}
Remove-Item -LiteralPath $pending -Force -ErrorAction SilentlyContinue
Remove-Item -LiteralPath $validateExe -Force -ErrorAction SilentlyContinue
Write-BootstrapLog "Deferred redeploy replacement failed after all attempts; staged replacement removed."
exit 1
`,
		powershellSingleQuoted(logPath),
		powershellSingleQuoted(pending),
		powershellSingleQuoted(destination),
		powershellSingleQuoted(configPath),
		powershellSingleQuoted(cfg.InstallDir),
		powershellSingleQuoted(strings.ToLower(strings.TrimSpace(expectedSHA256))),
		powershellSingleQuoted("BorealisAgent"),
		powershellSingleQuoted(legacyAgentTaskName),
		powershellSingleQuoted(agentUpdaterTaskName),
		powershellSingleQuoted(agentWatchdogTaskName),
		powershellSingleQuoted("BorealisAgent"),
		powershellSingleQuoted(ultraVNCServiceName),
		powershellSingleQuoted(wireGuardManagerServiceName),
		powershellSingleQuoted("BorealisWireGuardTunnel"),
		powershellSingleQuoted("WireGuardTunnel$wireguard"),
		powershellSingleQuoted("WireGuardTunnel$Borealis"),
		powershellSingleQuoted("WireGuardTunnel$borealis-wg"),
	)
}

func removePathWithRetries(path string, attempts int, delay time.Duration, logger *BootstrapLogger) error {
	var lastErr error
	if attempts < 1 {
		attempts = 1
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		if !dirExists(path) && !fileExists(path) {
			return nil
		}
		if err := os.RemoveAll(path); err != nil {
			lastErr = err
			if logger != nil {
				logger.Warnf("Remove attempt %d/%d failed for %s: %v", attempt, attempts, path, err)
			}
		} else if !dirExists(path) && !fileExists(path) {
			return nil
		} else {
			lastErr = fmt.Errorf("path still exists after remove attempt %d", attempt)
			if logger != nil {
				logger.Warnf("Remove attempt %d/%d left path behind: %s", attempt, attempts, path)
			}
		}
		if attempt < attempts {
			time.Sleep(delay)
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("path still exists: %s", path)
	}
	return lastErr
}

func powershellPath() string {
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	candidate := filepath.Join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if fileExists(candidate) {
		return candidate
	}
	return "powershell.exe"
}
