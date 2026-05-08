//go:build windows

package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

const (
	defaultServiceName    = "BorealisOnboarding"
	defaultInstallDir     = `C:\Borealis`
	defaultTimeoutSeconds = 900
	agentTaskName         = "Borealis Agent"
	waitAbandoned         = 0x00000080
)

var agentStepMarkerPattern = regexp.MustCompile(`^__BOREALIS_AGENT_STEP_(STARTED|COMPLETED|FAILED|DEFERRED)__=(.+)$`)

type BootstrapConfig struct {
	AgentURL        string   `json:"agent_url"`
	AgentScriptPath string   `json:"agent_script_path"`
	InstallDir      string   `json:"install_dir"`
	RepoURL         string   `json:"repo_url"`
	RepoRef         string   `json:"repo_ref"`
	ServerURL       string   `json:"server_url"`
	EnrollmentCode  string   `json:"enrollment_code"`
	StatePath       string   `json:"state_path"`
	EventsPath      string   `json:"events_path"`
	StdoutPath      string   `json:"stdout_path"`
	StderrPath      string   `json:"stderr_path"`
	TimeoutSeconds  int      `json:"timeout_seconds"`
	JobID           int      `json:"job_id"`
	RunID           int      `json:"run_id"`
	Target          string   `json:"target"`
	ExtraArgs       []string `json:"extra_args"`
}

type StatePayload struct {
	JobID                int    `json:"job_id,omitempty"`
	RunID                int    `json:"run_id,omitempty"`
	Target               string `json:"target,omitempty"`
	Hostname             string `json:"hostname,omitempty"`
	RepoRef              string `json:"repo_ref,omitempty"`
	ServerURL            string `json:"server_url,omitempty"`
	EnrollmentCodeSHA256 string `json:"enrollment_code_sha256,omitempty"`
	Status               string `json:"status"`
	ExitCode             int    `json:"exit_code"`
	Detail               string `json:"detail"`
	UpdatedAt            string `json:"updated_at"`
}

type EventPayload struct {
	Status    string `json:"status"`
	Task      string `json:"task"`
	Detail    string `json:"detail"`
	ExitCode  int    `json:"exit_code"`
	Hostname  string `json:"hostname,omitempty"`
	CreatedAt string `json:"created_at"`
}

type ExistingAgentAssessment struct {
	Detected        bool
	TaskExists      bool
	TaskRunning     bool
	TaskState       string
	EngineValid     bool
	RepairSucceeded bool
	Detail          string
}

type ScheduledTaskInfo struct {
	Exists bool
	State  string
	Raw    string
	Error  string
}

type bootstrapService struct {
	configPath string
}

type logWriter struct {
	mu   sync.Mutex
	file *os.File
}

func main() {
	configPath := flag.String("config", "", "Path to Borealis Agent service bootstrapper config JSON.")
	serviceName := flag.String("service-name", defaultServiceName, "Windows service name.")
	flag.Parse()

	if strings.TrimSpace(*configPath) == "" {
		fmt.Fprintln(os.Stderr, "missing --config")
		os.Exit(2)
	}

	isService, err := svc.IsWindowsService()
	if err != nil {
		fmt.Fprintf(os.Stderr, "windows service detection failed: %v\n", err)
		os.Exit(1)
	}

	if isService {
		if err := svc.Run(*serviceName, &bootstrapService{configPath: *configPath}); err != nil {
			os.Exit(1)
		}
		return
	}

	code := runFromConfig(context.Background(), *configPath)
	os.Exit(code)
}

func (s *bootstrapService) Execute(_ []string, changes <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	status <- svc.Status{State: svc.StartPending}
	result := make(chan int, 1)
	go func() {
		result <- runFromConfig(ctx, s.configPath)
	}()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for {
		select {
		case code := <-result:
			status <- svc.Status{State: svc.StopPending}
			if code == 0 {
				return false, 0
			}
			return false, 1
		case change := <-changes:
			switch change.Cmd {
			case svc.Interrogate:
				status <- change.CurrentStatus
			case svc.Stop, svc.Shutdown:
				cancel()
				status <- svc.Status{State: svc.StopPending}
			}
		}
	}
}

func runFromConfig(ctx context.Context, configPath string) int {
	cfg, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config load failed: %v\n", err)
		return 2
	}
	normalizeConfig(&cfg)

	combinedLog, closeLogs, err := openCombinedLog(cfg.StdoutPath, cfg.StderrPath)
	if err != nil {
		writeState(cfg, "failed", 1, fmt.Sprintf("Unable to open onboarding logs: %v", err))
		return 1
	}
	defer closeLogs()

	logger := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		combinedLog.WriteLine(line)
	}

	logger("__BOREALIS_AGENT_SERVICE_BOOTSTRAPPER_STARTED__=1")
	if hostname := currentHostname(); hostname != "" {
		logger("__BOREALIS_ONBOARDING_HOSTNAME__=%s", hostname)
	}

	mutex, acquired, err := acquireOnboardingMutex()
	if err != nil {
		detail := fmt.Sprintf("Unable to acquire Borealis onboarding mutex: %v", err)
		logger(detail)
		writeState(cfg, "failed", 1, detail)
		return 1
	}
	if !acquired {
		logger("__BOREALIS_ONBOARDING_ALREADY_RUNNING__=1")
		logger("__BOREALIS_WINDOWS_ONBOARDING_EXIT_CODE__=73")
		writeState(cfg, "already_running", 73, "Another Borealis onboarding deployment is already running on this target.")
		writeEvent(cfg, "skipped", "Agent Service Bootstrapper Mutex", "Another Borealis onboarding deployment is already running on this target.", 73)
		return 73
	}
	defer releaseOnboardingMutex(mutex)

	writeState(cfg, "running", 1, "Agent service bootstrapper started.")
	writeEvent(cfg, "running", "Agent Service Bootstrapper Started", "Agent service bootstrapper started.", 1)

	writeState(cfg, "running", 1, "Cleaning onboarding temp folder.")
	writeEvent(cfg, "running", "Cleaning Onboarding Temp", "Cleaning onboarding temp folder.", 1)
	if err := cleanOnboardingTemp(cfg); err != nil {
		detail := fmt.Sprintf("Unable to clean onboarding temp folder: %v", err)
		logger(detail)
		writeState(cfg, "failed", 1, detail)
		writeEvent(cfg, "failed", "Cleaning Onboarding Temp", detail, 1)
		return 1
	}
	logger("__BOREALIS_ONBOARDING_TEMP_CLEANED__=1")
	writeState(cfg, "running", 1, "Onboarding temp folder cleaned.")

	assessment := assessExistingAgent(ctx, cfg, logger)
	if assessment.Detected {
		detail := assessment.Detail
		if detail == "" {
			detail = "Existing Borealis Agent runtime detected."
		}
		logger("__BOREALIS_ONBOARDING_EXISTING_AGENT_DETECTED__=1")
		writeState(cfg, "running", 1, detail)
		writeEvent(cfg, "running", "Existing Agent Detected", detail, 1)
		if assessment.RepairSucceeded {
			logger("__BOREALIS_ONBOARDING_AGENT_REPAIRED__=1")
			logger("__BOREALIS_ONBOARDING_ALREADY_ENROLLED__=1")
			logger("__BOREALIS_WINDOWS_ONBOARDING_EXIT_CODE__=73")
			writeState(cfg, "already_enrolled", 73, "Existing Borealis Agent is already enrolled and active.")
			writeEvent(cfg, "skipped", "Already Enrolled and Active", "Existing Borealis Agent is already enrolled and active.", 73)
			return 73
		}
		redeployDetail := assessment.Detail
		if redeployDetail == "" {
			redeployDetail = "Existing Borealis Agent could not be validated or repaired; re-deploying."
		}
		logger("__BOREALIS_ONBOARDING_REDEPLOY_REQUIRED__=1")
		writeState(cfg, "running", 1, redeployDetail)
		writeEvent(cfg, "running", "Unable to Repair Agent > Re-Deploying", redeployDetail, 1)
	}

	if cfg.AgentURL != "" {
		writeState(cfg, "running", 1, "Downloading Agent.ps1.")
		writeEvent(cfg, "running", "Downloading Agent.ps1", "Downloading Windows Agent bootstrap script.", 1)
		logger("Downloading Windows Agent bootstrap from %s.", cfg.AgentURL)
		if err := downloadFile(ctx, cfg.AgentURL, cfg.AgentScriptPath); err != nil {
			detail := fmt.Sprintf("Agent.ps1 download failed: %v", err)
			logger(detail)
			writeState(cfg, "failed", 1, detail)
			writeEvent(cfg, "failed", "Downloading Agent.ps1", detail, 1)
			return 1
		}
	}

	if _, err := os.Stat(cfg.AgentScriptPath); err != nil {
		detail := fmt.Sprintf("Agent.ps1 not found at %s.", cfg.AgentScriptPath)
		logger(detail)
		writeState(cfg, "failed", 1, detail)
		writeEvent(cfg, "failed", "Locating Agent.ps1", detail, 1)
		return 1
	}

	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := agentPowerShellArgs(cfg)
	logger("Launching %s %s.", powershellPath(), redactArgsForLog(args))
	writeState(cfg, "running", 1, "Running Agent.ps1.")
	writeEvent(cfg, "running", "Running Agent Bootstrap", "Running Agent.ps1.", 1)

	exitCode, err := runAgent(runCtx, cfg, combinedLog, args)
	if errors.Is(err, context.DeadlineExceeded) {
		detail := fmt.Sprintf("Agent.ps1 timed out after %d seconds.", cfg.TimeoutSeconds)
		logger(detail)
		writeState(cfg, "failed", 124, detail)
		writeEvent(cfg, "failed", "Running Agent Bootstrap", detail, 124)
		logger("__BOREALIS_WINDOWS_ONBOARDING_EXIT_CODE__=124")
		return 124
	}
	if err != nil {
		detail := fmt.Sprintf("Agent.ps1 launch failed: %v", err)
		logger(detail)
		writeState(cfg, "failed", 1, detail)
		writeEvent(cfg, "failed", "Running Agent Bootstrap", detail, 1)
		logger("__BOREALIS_WINDOWS_ONBOARDING_EXIT_CODE__=1")
		return 1
	}

	if exitCode == 0 {
		writeState(cfg, "pending_approval", 0, "Agent.ps1 completed; device approval pending operator action.")
		writeEvent(cfg, "completed", "Agent Ready and Awaiting Approval", "Agent.ps1 completed; device approval pending operator action.", 0)
	} else {
		writeState(cfg, "failed", exitCode, fmt.Sprintf("Agent.ps1 failed with exit code %d.", exitCode))
		writeEvent(cfg, "failed", "Running Agent Bootstrap", fmt.Sprintf("Agent.ps1 failed with exit code %d.", exitCode), exitCode)
	}
	logger("__BOREALIS_WINDOWS_ONBOARDING_EXIT_CODE__=%d", exitCode)
	return exitCode
}

func loadConfig(path string) (BootstrapConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return BootstrapConfig{}, err
	}
	var cfg BootstrapConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return BootstrapConfig{}, err
	}
	return cfg, nil
}

func normalizeConfig(cfg *BootstrapConfig) {
	if strings.TrimSpace(cfg.InstallDir) == "" {
		cfg.InstallDir = defaultInstallDir
	}
	if strings.TrimSpace(cfg.AgentScriptPath) == "" {
		cfg.AgentScriptPath = filepath.Join(cfg.InstallDir, "Temp", "Onboarding", "Agent.ps1")
	}
	if strings.TrimSpace(cfg.StatePath) == "" {
		cfg.StatePath = filepath.Join(cfg.InstallDir, "Temp", "Onboarding", "state.json")
	}
	if strings.TrimSpace(cfg.EventsPath) == "" {
		cfg.EventsPath = filepath.Join(cfg.InstallDir, "Temp", "Onboarding", "events.jsonl")
	}
	if strings.TrimSpace(cfg.StdoutPath) == "" {
		cfg.StdoutPath = filepath.Join(cfg.InstallDir, "Temp", "Onboarding", "bootstrapper.log")
	}
	if strings.TrimSpace(cfg.StderrPath) == "" {
		cfg.StderrPath = cfg.StdoutPath
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = defaultTimeoutSeconds
	}
}

func agentPowerShellArgs(cfg BootstrapConfig) []string {
	args := []string{
		"-NoProfile",
		"-NonInteractive",
		"-ExecutionPolicy",
		"Bypass",
		"-WindowStyle",
		"Hidden",
		"-File",
		cfg.AgentScriptPath,
		"--install-dir",
		cfg.InstallDir,
	}
	if cfg.RepoURL != "" {
		args = append(args, "--repo-url", cfg.RepoURL)
	}
	if cfg.RepoRef != "" {
		args = append(args, "--ref", cfg.RepoRef)
	}
	args = append(args, "deploy", "--agent")
	if cfg.ServerURL != "" {
		args = append(args, "--serverurl", cfg.ServerURL)
	}
	if cfg.EnrollmentCode != "" {
		args = append(args, "--enrollmentcode", cfg.EnrollmentCode)
	}
	args = append(args, "--reset-enrollment")
	args = append(args, cfg.ExtraArgs...)
	return args
}

func runAgent(ctx context.Context, cfg BootstrapConfig, logs *logWriter, args []string) (int, error) {
	cmd := exec.Command(powershellPath(), args...)
	cmd.Env = append(os.Environ(),
		"BOREALIS_ONBOARDING_JOB_ID="+strconv.Itoa(cfg.JobID),
		"BOREALIS_ONBOARDING_RUN_ID="+strconv.Itoa(cfg.RunID),
		"BOREALIS_ONBOARDING_TARGET="+cfg.Target,
		"BOREALIS_ONBOARDING_TIMEOUT_SECONDS="+strconv.Itoa(cfg.TimeoutSeconds),
		"BOREALIS_ONBOARDING_STATE_PATH="+cfg.StatePath,
		"BOREALIS_AGENT_NONINTERACTIVE=1",
		"BOREALIS_AGENT_NO_TTY=1",
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 1, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return 1, err
	}
	if err := cmd.Start(); err != nil {
		return 1, err
	}

	var copyWG sync.WaitGroup
	copyWG.Add(2)
	go copyStream(&copyWG, stdout, logs, "", func(line string) {
		writeAgentStepEvent(cfg, line)
	})
	go copyStream(&copyWG, stderr, logs, "", func(line string) {
		writeAgentStepEvent(cfg, line)
	})

	waitCh := make(chan error, 1)
	go func() {
		waitErr := cmd.Wait()
		copyWG.Wait()
		waitCh <- waitErr
	}()

	var waitErr error
	select {
	case waitErr = <-waitCh:
	case <-ctx.Done():
		killProcessTree(cmd.Process.Pid)
		waitErr = <-waitCh
		return 124, ctx.Err()
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		return 1, waitErr
	}
	return 0, nil
}

func copyStream(wg *sync.WaitGroup, reader io.Reader, logs *logWriter, prefix string, onLine func(string)) {
	defer wg.Done()
	scanner := bufio.NewScanner(reader)
	buffer := make([]byte, 0, 64*1024)
	scanner.Buffer(buffer, 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if prefix != "" {
			line = prefix + line
		}
		logs.WriteLine(line)
		if onLine != nil {
			onLine(line)
		}
	}
}

func writeAgentStepEvent(cfg BootstrapConfig, line string) {
	match := agentStepMarkerPattern.FindStringSubmatch(strings.TrimSpace(line))
	if len(match) != 3 {
		return
	}
	markerState := strings.ToUpper(strings.TrimSpace(match[1]))
	rawTask := strings.TrimSpace(match[2])
	task := normalizeAgentTask(rawTask)
	if task == "" {
		return
	}
	status := "running"
	exitCode := 1
	switch markerState {
	case "COMPLETED", "DEFERRED":
		status = "completed"
		exitCode = 0
	case "FAILED":
		status = "failed"
		exitCode = 1
	default:
		status = "running"
		exitCode = 1
	}
	writeEvent(cfg, status, task, rawTask, exitCode)
	if status == "running" {
		writeState(cfg, "running", exitCode, task)
	}
}

func normalizeAgentTask(task string) string {
	text := strings.TrimSpace(task)
	if text == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(text), "dependency:") {
		return "Installing Agent Dependencies"
	}
	return text
}

func powershellPath() string {
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	candidate := filepath.Join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return "powershell.exe"
}

func downloadFile(ctx context.Context, url string, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	tmp := destination + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, resp.Body)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	_ = os.Remove(destination)
	return os.Rename(tmp, destination)
}

func cleanOnboardingTemp(cfg BootstrapConfig) error {
	dir := filepath.Dir(cfg.AgentScriptPath)
	if strings.TrimSpace(dir) == "" || dir == "." {
		return nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return os.MkdirAll(dir, 0755)
}

func assessExistingAgent(ctx context.Context, cfg BootstrapConfig, logger func(string, ...any)) ExistingAgentAssessment {
	assessment := ExistingAgentAssessment{}
	if !existingAgentDetected(cfg) {
		return assessment
	}
	assessment.Detected = true

	taskCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	taskInfo := queryAgentScheduledTask(taskCtx)
	cancel()
	assessment.TaskExists = taskInfo.Exists
	assessment.TaskState = taskInfo.State
	assessment.TaskRunning = taskScheduledStateIsRunning(taskInfo.State)
	if taskInfo.Exists {
		logger("__BOREALIS_AGENT_TASK_STATE__=%s", taskInfo.State)
	}

	if !taskInfo.Exists {
		assessment.Detail = "Existing Borealis Agent files found, but Borealis Agent scheduled task is missing."
		return assessment
	}

	startAttempted := false
	if !assessment.TaskRunning {
		logger("__BOREALIS_AGENT_TASK_START_ATTEMPTED__=1")
		startAttempted = true
		if err := startAgentScheduledTask(ctx); err != nil {
			assessment.Detail = fmt.Sprintf("Existing Borealis Agent scheduled task could not be started: %v", err)
			return assessment
		}
		taskCtx, cancel = context.WithTimeout(ctx, 20*time.Second)
		taskInfo = waitForAgentScheduledTaskRunning(taskCtx)
		cancel()
		assessment.TaskState = taskInfo.State
		assessment.TaskRunning = taskScheduledStateIsRunning(taskInfo.State)
		if !assessment.TaskRunning {
			stateDetail := taskInfo.State
			if stateDetail == "" {
				stateDetail = taskInfo.Error
			}
			logger("__BOREALIS_AGENT_TASK_READY_AFTER_START__=%s", stateDetail)
		}
	}

	valid, detail := validateExistingAgentWithEngine(ctx, cfg)
	assessment.EngineValid = valid
	if !valid {
		if detail == "" {
			detail = "Engine did not accept existing Borealis Agent token."
		}
		assessment.Detail = detail
		return assessment
	}

	if assessment.TaskRunning || startAttempted {
		assessment.RepairSucceeded = true
		if assessment.TaskRunning && strings.EqualFold(taskInfo.State, "Running") {
			assessment.Detail = "Existing Borealis Agent scheduled task is running and Engine authentication is valid."
		} else if assessment.TaskRunning {
			assessment.Detail = "Existing Borealis Agent scheduled task was started and Engine authentication is valid."
		} else {
			stateDetail := assessment.TaskState
			if stateDetail == "" {
				stateDetail = "unknown"
			}
			assessment.Detail = fmt.Sprintf("Existing Borealis Agent scheduled task accepted start request and Engine authentication is valid. Current task state: %s.", stateDetail)
		}
	}
	return assessment
}

func existingAgentDetected(cfg BootstrapConfig) bool {
	candidates := []string{
		filepath.Join(agentSettingsDir(cfg), "access.jwt"),
		filepath.Join(agentSettingsDir(cfg), "refresh.token"),
		filepath.Join(agentSettingsDir(cfg), "Agent_GUID.txt"),
		filepath.Join(cfg.InstallDir, "Agent", "Borealis", "agent.py"),
		filepath.Join(cfg.InstallDir, "Agent", "Borealis", "launch_service.ps1"),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return true
		}
	}
	taskCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return queryAgentScheduledTask(taskCtx).Exists
}

func agentSettingsDir(cfg BootstrapConfig) string {
	return filepath.Join(cfg.InstallDir, "Agent", "Borealis", "Settings")
}

func readExistingAccessToken(cfg BootstrapConfig) string {
	data, err := os.ReadFile(filepath.Join(agentSettingsDir(cfg), "access.jwt"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func validateExistingAgentWithEngine(ctx context.Context, cfg BootstrapConfig) (bool, string) {
	serverURL := strings.TrimRight(strings.TrimSpace(cfg.ServerURL), "/")
	if serverURL == "" {
		return false, "Server URL is unavailable for existing Borealis Agent validation."
	}
	token := readExistingAccessToken(cfg)
	if token == "" {
		return false, "Existing Borealis Agent access token is missing."
	}

	probeCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, serverURL+"/api/repo/current_hash?ttl=300", nil)
	if err != nil {
		return false, fmt.Sprintf("Existing Borealis Agent validation request failed: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, fmt.Sprintf("Engine did not answer existing Borealis Agent validation: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, "Existing Borealis Agent token accepted by Engine."
	}
	return false, fmt.Sprintf("Engine rejected existing Borealis Agent token with HTTP %d.", resp.StatusCode)
}

func queryAgentScheduledTask(ctx context.Context) ScheduledTaskInfo {
	cmd := exec.CommandContext(ctx, "schtasks.exe", "/Query", "/TN", agentTaskName, "/FO", "LIST", "/V")
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		return ScheduledTaskInfo{Exists: false, Raw: text, Error: strings.TrimSpace(err.Error() + " " + text)}
	}
	return ScheduledTaskInfo{
		Exists: true,
		State:  parseScheduledTaskState(text),
		Raw:    text,
	}
}

func parseScheduledTaskState(output string) string {
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(strings.ToLower(parts[0]))
		if key == "status" || key == "scheduled task state" {
			return strings.TrimSpace(parts[1])
		}
	}
	return ""
}

func taskScheduledStateIsRunning(state string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(state)), "running")
}

func startAgentScheduledTask(ctx context.Context) error {
	startCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(startCtx, "schtasks.exe", "/Run", "/TN", agentTaskName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(output))
		if text != "" {
			return fmt.Errorf("%v: %s", err, text)
		}
		return err
	}
	return nil
}

func waitForAgentScheduledTaskRunning(ctx context.Context) ScheduledTaskInfo {
	var last ScheduledTaskInfo
	for {
		last = queryAgentScheduledTask(ctx)
		if taskScheduledStateIsRunning(last.State) || ctx.Err() != nil {
			return last
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return last
		case <-timer.C:
		}
	}
}

func acquireOnboardingMutex() (windows.Handle, bool, error) {
	name, err := windows.UTF16PtrFromString(`Global\BorealisAgentOnboarding`)
	if err != nil {
		return 0, false, err
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		return 0, false, err
	}
	waitResult, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		_ = windows.CloseHandle(handle)
		return 0, false, err
	}
	if waitResult == windows.WAIT_OBJECT_0 || waitResult == waitAbandoned {
		return handle, true, nil
	}
	_ = windows.CloseHandle(handle)
	return 0, false, nil
}

func releaseOnboardingMutex(handle windows.Handle) {
	if handle == 0 {
		return
	}
	_ = windows.ReleaseMutex(handle)
	_ = windows.CloseHandle(handle)
}

func writeState(cfg BootstrapConfig, status string, exitCode int, detail string) {
	if strings.TrimSpace(cfg.StatePath) == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(cfg.StatePath), 0755)
	payload := StatePayload{
		JobID:                cfg.JobID,
		RunID:                cfg.RunID,
		Target:               cfg.Target,
		Hostname:             currentHostname(),
		RepoRef:              cfg.RepoRef,
		ServerURL:            cfg.ServerURL,
		EnrollmentCodeSHA256: hashText(cfg.EnrollmentCode),
		Status:               status,
		ExitCode:             exitCode,
		Detail:               detail,
		UpdatedAt:            time.Now().UTC().Format(time.RFC3339Nano),
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return
	}
	tmpPath := cfg.StatePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return
	}
	_ = os.Remove(cfg.StatePath)
	_ = os.Rename(tmpPath, cfg.StatePath)
}

func writeEvent(cfg BootstrapConfig, status string, task string, detail string, exitCode int) {
	if strings.TrimSpace(cfg.EventsPath) == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(cfg.EventsPath), 0755)
	payload := EventPayload{
		Status:    status,
		Task:      task,
		Detail:    detail,
		ExitCode:  exitCode,
		Hostname:  currentHostname(),
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	file, err := os.OpenFile(cfg.EventsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.Write(append(data, '\n'))
}

func openCombinedLog(stdoutPath string, stderrPath string) (*logWriter, func(), error) {
	path := stdoutPath
	if strings.TrimSpace(path) == "" {
		path = stderrPath
	}
	if strings.TrimSpace(path) == "" {
		return &logWriter{}, func() {}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, nil, err
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, nil, err
	}
	writer := &logWriter{file: file}
	return writer, func() { _ = file.Close() }, nil
}

func (w *logWriter) WriteLine(line string) {
	if w == nil || w.file == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_, _ = w.file.WriteString(line + "\r\n")
}

func killProcessTree(pid int) {
	if pid <= 0 {
		return
	}
	cmd := exec.Command("taskkill.exe", "/PID", strconv.Itoa(pid), "/T", "/F")
	_ = cmd.Run()
}

func hashText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
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

func redactArgsForLog(args []string) string {
	redacted := make([]string, 0, len(args))
	skipValue := false
	for i, arg := range args {
		if skipValue {
			skipValue = false
			continue
		}
		lower := strings.ToLower(arg)
		if lower == "--serverurl" || lower == "--enrollmentcode" {
			redacted = append(redacted, arg, "[redacted]")
			if i+1 < len(args) {
				skipValue = true
			}
			continue
		}
		redacted = append(redacted, arg)
	}
	return strings.Join(redacted, " ")
}
