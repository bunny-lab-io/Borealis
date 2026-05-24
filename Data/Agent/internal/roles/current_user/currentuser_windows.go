//go:build windows

package currentuser

import (
	"context"
	cryptoRand "crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/bunny-lab-io/borealis/go-agent/internal/localui"
	"github.com/bunny-lab-io/borealis/go-agent/internal/scripts"
	"golang.org/x/sys/windows"
)

const (
	infiniteWait        = 0xffffffff
	maxCurrentUserWait  = 6 * time.Hour
	waitPollMillisecond = 250
	helperPollInterval  = 20 * time.Second
	helperReadyWindow   = 45 * time.Second
	createNewProcessGrp = 0x00000200
)

type helperStateFile struct {
	SessionID int    `json:"session_id"`
	PID       int    `json:"pid"`
	Status    string `json:"status"`
	BuildID   string `json:"build_id"`
	UpdatedAt int64  `json:"updated_at"`
}

func (d *Dispatcher) Start(ctx context.Context, configPath string) {
	if d == nil {
		return
	}
	d.mu.Lock()
	if d.started {
		d.mu.Unlock()
		return
	}
	d.started = true
	d.configPath = strings.TrimSpace(configPath)
	d.stateDir = helperStateDir("")
	if d.helperPIDs == nil {
		d.helperPIDs = map[uint32]int{}
	}
	d.mu.Unlock()
	go d.helperBrokerLoop(ctx)
}

func RunHelper(ctx context.Context, options HelperOptions) error {
	stateDir := helperStateDir(options.StateDir)
	sessionID := options.SessionID
	if sessionID <= 0 {
		sessionID = int(windows.WTSGetActiveConsoleSessionId())
	}
	if sessionID <= 0 || uint32(sessionID) == 0xffffffff {
		sessionID = 0
	}
	releaseSingleton, acquired, err := acquireHelperSingleton(sessionID)
	if err != nil {
		appendHelperEvent(stateDir, sessionID, "helper singleton acquisition failed: "+err.Error())
		return err
	}
	if !acquired {
		appendHelperEvent(stateDir, sessionID, "duplicate helper launch prevented")
		return nil
	}
	defer releaseSingleton()
	pid := os.Getpid()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	writeHelperState(stateDir, helperStateFile{
		SessionID: sessionID,
		PID:       pid,
		Status:    "ready",
		BuildID:   strings.TrimSpace(options.BuildID),
		UpdatedAt: time.Now().Unix(),
	})
	go runTray(ctx, trayOptions{
		StateDir:  stateDir,
		SessionID: sessionID,
		BuildID:   strings.TrimSpace(options.BuildID),
	})
	for {
		select {
		case <-ctx.Done():
			writeHelperState(stateDir, helperStateFile{
				SessionID: sessionID,
				PID:       pid,
				Status:    "stopped",
				BuildID:   strings.TrimSpace(options.BuildID),
				UpdatedAt: time.Now().Unix(),
			})
			return ctx.Err()
		case <-ticker.C:
			writeHelperState(stateDir, helperStateFile{
				SessionID: sessionID,
				PID:       pid,
				Status:    "ready",
				BuildID:   strings.TrimSpace(options.BuildID),
				UpdatedAt: time.Now().Unix(),
			})
		}
	}
}

func (d *Dispatcher) SupportsCurrentUserDispatch() bool {
	return true
}

func (d *Dispatcher) RoleHealth() RoleHealth {
	sessions, err := activeSessionIDs(0)
	if err != nil {
		return RoleHealth{
			Status:     "recovering",
			StatusCode: "recovering",
			Detail:     "Unable to inspect active Windows user sessions: " + err.Error(),
			Details: map[string]any{
				"running_status": "Recovering",
				"runtime":        "go",
				"broker_mode":    "helper_process_broker",
			},
		}
	}
	ready, pending := d.helperSessionLines(sessions)
	if len(sessions) == 0 {
		return RoleHealth{
			Status:     "not_applicable",
			StatusCode: "not_applicable",
			Detail:     "No active interactive Windows user session.",
			Details: map[string]any{
				"running_status": "No Active Session",
				"runtime":        "go",
				"broker_mode":    "helper_process_broker",
				"ready_helpers":  "0",
			},
		}
	}
	if len(ready) == 0 {
		return RoleHealth{
			Status:     "recovering",
			StatusCode: "recovering",
			Detail:     "Interactive Windows user sessions are present, but helpers are still warming up.",
			Details: map[string]any{
				"running_status":          "Warming Up",
				"runtime":                 "go",
				"broker_mode":             "helper_process_broker",
				"execution_context":       "CURRENTUSER",
				"ready_helpers":           "0",
				"loaded_helper_sessions":  strings.Join(ready, "\n"),
				"pending_helper_sessions": strings.Join(pending, "\n"),
			},
		}
	}
	return RoleHealth{
		Status:     "healthy",
		StatusCode: "healthy",
		Detail:     fmt.Sprintf("%d current-user helper session(s) ready.", len(ready)),
		Details: map[string]any{
			"running_status":          "Ready",
			"runtime":                 "go",
			"broker_mode":             "helper_process_broker",
			"execution_context":       "CURRENTUSER",
			"ready_helpers":           strconv.Itoa(len(ready)),
			"loaded_helper_sessions":  strings.Join(ready, "\n"),
			"pending_helper_sessions": strings.Join(pending, "\n"),
		},
	}
}

func (d *Dispatcher) DispatchCurrentUserQuickJob(ctx context.Context, payload map[string]any) (scripts.Result, bool, string) {
	scriptBytes, ok := scripts.DecodeScriptBytes(payload["script_content"], asString(payload["script_encoding"]))
	if !ok {
		return scripts.Result{}, false, "Invalid script payload (unable to decode)"
	}
	scriptType := strings.ToLower(strings.TrimSpace(asString(payload["script_type"])))
	envMap := scripts.BuildEnvMap(mapStringAny(payload["environment"]), listMapStringAny(payload["variables"]))
	timeoutSeconds := asInt(payload["timeout_seconds"])
	commandLine, appPath, err := commandForScript(scriptType, string(scriptBytes), envMap)
	if err != nil {
		return scripts.Result{}, false, err.Error()
	}
	sessionID, err := selectSessionID(payload)
	if err != nil {
		return scripts.Result{}, false, err.Error()
	}
	result, err := runInSession(ctx, sessionID, appPath, commandLine, timeoutSeconds)
	if err != nil {
		return result, false, err.Error()
	}
	return result, true, ""
}

func commandForScript(scriptType string, content string, envMap map[string]string) (string, string, error) {
	powershell := powershellPath()
	switch scriptType {
	case "powershell":
		encoded := encodePowerShellCommand(scripts.BuildPowerShellScript(content, envMap))
		args := []string{powershell, "-NoProfile", "-ExecutionPolicy", "Bypass", "-EncodedCommand", encoded}
		return windowsCommandLine(args), powershell, nil
	case "batch":
		wrapper := batchPowerShellWrapper(content, envMap)
		encoded := encodePowerShellCommand(wrapper)
		args := []string{powershell, "-NoProfile", "-ExecutionPolicy", "Bypass", "-EncodedCommand", encoded}
		return windowsCommandLine(args), powershell, nil
	default:
		return "", "", fmt.Errorf("CURRENTUSER script type %q is not supported on Windows", scriptType)
	}
}

func batchPowerShellWrapper(content string, envMap map[string]string) string {
	lines := scripts.PowerShellPreludeLines()
	encodedBatch := base64.StdEncoding.EncodeToString([]byte(normalizeBatchNewlines(content)))
	lines = append(lines, "$ErrorActionPreference = 'Stop'")
	for key, value := range envMap {
		if key == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("try { [System.Environment]::SetEnvironmentVariable(%s, %s, 'Process') } catch {}", scripts.PowerShellLiteral(key), scripts.PowerShellLiteral(value)))
		lines = append(lines, fmt.Sprintf("try { Set-Item -LiteralPath ([string]::Format('Env:{0}', %s)) -Value %s -ErrorAction Stop } catch {}", scripts.PowerShellLiteral(key), scripts.PowerShellLiteral(value)))
	}
	lines = append(lines, "$__BorealisBatch = Join-Path $env:TEMP ('borealis-' + [guid]::NewGuid().ToString('N') + '.bat')")
	lines = append(lines, "try {")
	lines = append(lines, "  [System.IO.File]::WriteAllBytes($__BorealisBatch, [Convert]::FromBase64String('"+encodedBatch+"'))")
	lines = append(lines, "  & $env:ComSpec /D /C $__BorealisBatch")
	lines = append(lines, "  exit $LASTEXITCODE")
	lines = append(lines, "} finally {")
	lines = append(lines, "  Remove-Item -LiteralPath $__BorealisBatch -Force -ErrorAction SilentlyContinue")
	lines = append(lines, "}")
	return strings.Join(lines, "\r\n") + "\r\n"
}

func normalizeBatchNewlines(content string) string {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	return strings.ReplaceAll(normalized, "\n", "\r\n")
}

func encodePowerShellCommand(script string) string {
	encoded := utf16.Encode([]rune(script))
	bytes := make([]byte, 0, len(encoded)*2)
	for _, value := range encoded {
		bytes = append(bytes, byte(value), byte(value>>8))
	}
	return base64.StdEncoding.EncodeToString(bytes)
}

func powershellPath() string {
	systemRoot := strings.TrimSpace(os.Getenv("SystemRoot"))
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	candidate := filepath.Join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate
	}
	return "powershell.exe"
}

func selectSessionID(payload map[string]any) (uint32, error) {
	target := asInt(payload["target_session_id"])
	if target <= 0 {
		if contextPayload, ok := payload["context"].(map[string]any); ok {
			target = asInt(contextPayload["target_session_id"])
		}
	}
	sessions, err := activeSessionIDs(uint32(target))
	if err != nil {
		return 0, err
	}
	if len(sessions) == 0 {
		return 0, fmt.Errorf("no_interactive_user_session")
	}
	return sessions[0], nil
}

func activeSessionIDs(target uint32) ([]uint32, error) {
	var sessions *windows.WTS_SESSION_INFO
	var count uint32
	if err := windows.WTSEnumerateSessions(0, 0, 1, &sessions, &count); err != nil {
		if target > 0 {
			return []uint32{target}, nil
		}
		console := windows.WTSGetActiveConsoleSessionId()
		if console != 0xffffffff {
			return []uint32{console}, nil
		}
		return nil, err
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(sessions)))
	if count == 0 || sessions == nil {
		return nil, nil
	}
	raw := unsafe.Slice(sessions, count)
	out := []uint32{}
	for _, session := range raw {
		if session.State != windows.WTSActive {
			continue
		}
		if target > 0 && session.SessionID != target {
			continue
		}
		out = append(out, session.SessionID)
	}
	if len(out) == 0 && target > 0 {
		return nil, fmt.Errorf("target_session_id %d is not active", target)
	}
	return out, nil
}

func runInSession(ctx context.Context, sessionID uint32, appPath string, commandLine string, timeoutSeconds int) (scripts.Result, error) {
	var userToken windows.Token
	if err := windows.WTSQueryUserToken(sessionID, &userToken); err != nil {
		return scripts.Result{}, fmt.Errorf("query user token for session %d: %w", sessionID, err)
	}
	defer userToken.Close()

	var primaryToken windows.Token
	desiredAccess := uint32(windows.TOKEN_ASSIGN_PRIMARY | windows.TOKEN_DUPLICATE | windows.TOKEN_QUERY | windows.TOKEN_ADJUST_DEFAULT | windows.TOKEN_ADJUST_SESSIONID)
	if err := windows.DuplicateTokenEx(userToken, desiredAccess, nil, windows.SecurityImpersonation, windows.TokenPrimary, &primaryToken); err != nil {
		return scripts.Result{}, fmt.Errorf("duplicate user token: %w", err)
	}
	defer primaryToken.Close()

	var env *uint16
	if err := windows.CreateEnvironmentBlock(&env, primaryToken, false); err != nil {
		return scripts.Result{}, fmt.Errorf("create user environment: %w", err)
	}
	defer windows.DestroyEnvironmentBlock(env)

	stdoutFile, stderrFile, stdinHandle, cleanup, err := prepareProcessHandles()
	if err != nil {
		return scripts.Result{}, err
	}
	defer cleanup()

	desktop, _ := windows.UTF16PtrFromString(`winsta0\default`)
	cwd := currentUserWorkingDirectory()
	cwdPtr, _ := windows.UTF16PtrFromString(cwd)
	appPtr, _ := windows.UTF16PtrFromString(appPath)
	cmdPtr, _ := windows.UTF16PtrFromString(commandLine)
	startupInfo := &windows.StartupInfo{
		Cb:        uint32(unsafe.Sizeof(windows.StartupInfo{})),
		Desktop:   desktop,
		Flags:     windows.STARTF_USESTDHANDLES,
		StdInput:  stdinHandle,
		StdOutput: windows.Handle(stdoutFile.Fd()),
		StdErr:    windows.Handle(stderrFile.Fd()),
	}
	var processInfo windows.ProcessInformation
	creationFlags := uint32(windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_NO_WINDOW)
	if err := windows.CreateProcessAsUser(primaryToken, appPtr, cmdPtr, nil, nil, true, creationFlags, env, cwdPtr, startupInfo, &processInfo); err != nil {
		return scripts.Result{}, fmt.Errorf("create current-user process: %w", err)
	}
	defer windows.CloseHandle(processInfo.Thread)
	defer windows.CloseHandle(processInfo.Process)

	waitErr := waitForProcess(ctx, processInfo.Process, timeoutSeconds)
	if waitErr != nil {
		_ = windows.TerminateProcess(processInfo.Process, 1)
	}
	var exitCode uint32
	if err := windows.GetExitCodeProcess(processInfo.Process, &exitCode); err != nil {
		exitCode = 1
	}
	_ = stdoutFile.Close()
	_ = stderrFile.Close()
	stdoutText, stderrText := readCapturedOutput(stdoutFile.Name(), stderrFile.Name())
	cleaned := scripts.CleanPowerShellResult(scripts.Result{ReturnCode: int(exitCode), Stdout: stdoutText, Stderr: stderrText})
	if waitErr != nil {
		return scripts.Result{ReturnCode: -1, Stdout: cleaned.Stdout, Stderr: joinStderr(cleaned.Stderr, waitErr.Error())}, nil
	}
	return cleaned, nil
}

func prepareProcessHandles() (*os.File, *os.File, windows.Handle, func(), error) {
	dir := filepath.Join(os.TempDir(), "Borealis", "currentuser")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, 0, func() {}, err
	}
	stdoutFile, err := os.CreateTemp(dir, "stdout-*.log")
	if err != nil {
		return nil, nil, 0, func() {}, err
	}
	stderrFile, err := os.CreateTemp(dir, "stderr-*.log")
	if err != nil {
		_ = stdoutFile.Close()
		_ = os.Remove(stdoutFile.Name())
		return nil, nil, 0, func() {}, err
	}
	stdinHandle, err := openInheritableNUL()
	if err != nil {
		_ = stdoutFile.Close()
		_ = stderrFile.Close()
		_ = os.Remove(stdoutFile.Name())
		_ = os.Remove(stderrFile.Name())
		return nil, nil, 0, func() {}, err
	}
	for _, handle := range []windows.Handle{windows.Handle(stdoutFile.Fd()), windows.Handle(stderrFile.Fd()), stdinHandle} {
		_ = windows.SetHandleInformation(handle, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT)
	}
	cleanup := func() {
		_ = windows.CloseHandle(stdinHandle)
		_ = stdoutFile.Close()
		_ = stderrFile.Close()
		_ = os.Remove(stdoutFile.Name())
		_ = os.Remove(stderrFile.Name())
	}
	return stdoutFile, stderrFile, stdinHandle, cleanup, nil
}

func openInheritableNUL() (windows.Handle, error) {
	name, _ := windows.UTF16PtrFromString("NUL")
	security := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}
	return windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, security, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
}

func waitForProcess(ctx context.Context, handle windows.Handle, timeoutSeconds int) error {
	deadline := time.Time{}
	if timeoutSeconds > 0 {
		if timeoutSeconds > int(maxCurrentUserWait.Seconds()) {
			timeoutSeconds = int(maxCurrentUserWait.Seconds())
		}
		deadline = time.Now().Add(time.Duration(timeoutSeconds) * time.Second)
	}
	for {
		waitMS := uint32(waitPollMillisecond)
		if !deadline.IsZero() {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return fmt.Errorf("Script timed out after %d seconds", timeoutSeconds)
			}
			if remaining < time.Duration(waitPollMillisecond)*time.Millisecond {
				waitMS = uint32(remaining / time.Millisecond)
				if waitMS == 0 {
					waitMS = 1
				}
			}
		} else if ctx == nil {
			waitMS = infiniteWait
		}
		event, err := windows.WaitForSingleObject(handle, waitMS)
		if err != nil {
			return err
		}
		if event == windows.WAIT_OBJECT_0 {
			return nil
		}
		if event != uint32(windows.WAIT_TIMEOUT) {
			return fmt.Errorf("unexpected process wait result %d", event)
		}
		if ctx != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		}
	}
}

func readCapturedOutput(stdoutPath string, stderrPath string) (string, string) {
	stdoutBytes, _ := os.ReadFile(stdoutPath)
	stderrBytes, _ := os.ReadFile(stderrPath)
	return string(stdoutBytes), string(stderrBytes)
}

func currentUserWorkingDirectory() string {
	for _, candidate := range []string{
		filepath.Join(os.Getenv("PUBLIC")),
		filepath.Join(os.Getenv("SystemDrive")+`\`, "Users", "Public"),
		os.Getenv("TEMP"),
		filepath.Join(os.Getenv("SystemRoot"), "Temp"),
	} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return `C:\`
}

func windowsCommandLine(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, quoteWindowsArg(arg))
	}
	return strings.Join(quoted, " ")
}

func quoteWindowsArg(arg string) string {
	if arg == "" {
		return `""`
	}
	if !strings.ContainsAny(arg, " \t\n\v\"") {
		return arg
	}
	var b strings.Builder
	b.WriteByte('"')
	backslashes := 0
	for _, r := range arg {
		if r == '\\' {
			backslashes++
			continue
		}
		if r == '"' {
			b.WriteString(strings.Repeat(`\`, backslashes*2+1))
			b.WriteRune(r)
			backslashes = 0
			continue
		}
		if backslashes > 0 {
			b.WriteString(strings.Repeat(`\`, backslashes))
			backslashes = 0
		}
		b.WriteRune(r)
	}
	if backslashes > 0 {
		b.WriteString(strings.Repeat(`\`, backslashes*2))
	}
	b.WriteByte('"')
	return b.String()
}

func asString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprint(v)
	}
}

func asInt(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(v))
		return parsed
	default:
		return 0
	}
}

func mapStringAny(value any) map[string]any {
	if value == nil {
		return nil
	}
	if mapped, ok := value.(map[string]any); ok {
		return mapped
	}
	return nil
}

func listMapStringAny(value any) []map[string]any {
	raw, ok := value.([]any)
	if !ok {
		if typed, ok := value.([]map[string]any); ok {
			return typed
		}
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if mapped, ok := item.(map[string]any); ok {
			out = append(out, mapped)
		}
	}
	return out
}

func joinStderr(existing string, extra string) string {
	existing = strings.TrimRight(existing, "\r\n")
	extra = strings.TrimSpace(extra)
	if existing == "" {
		return extra
	}
	if extra == "" {
		return existing
	}
	return existing + "\n" + extra
}

func (d *Dispatcher) helperBrokerLoop(ctx context.Context) {
	d.ensureHelpers(ctx)
	ticker := time.NewTicker(helperPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.ensureHelpers(ctx)
		}
	}
}

func (d *Dispatcher) ensureHelpers(ctx context.Context) {
	sessions, err := activeSessionIDs(0)
	if err != nil {
		return
	}
	active := map[uint32]bool{}
	for _, sessionID := range sessions {
		active[sessionID] = true
		if d.helperReady(sessionID) {
			continue
		}
		pid, err := launchHelperProcess(ctx, sessionID, d.helperStateDir())
		if err != nil {
			continue
		}
		d.mu.Lock()
		if d.helperPIDs == nil {
			d.helperPIDs = map[uint32]int{}
		}
		d.helperPIDs[sessionID] = pid
		d.mu.Unlock()
	}
	d.mu.Lock()
	for sessionID := range d.helperPIDs {
		if !active[sessionID] {
			delete(d.helperPIDs, sessionID)
		}
	}
	d.mu.Unlock()
}

func (d *Dispatcher) helperSessionLines(sessions []uint32) ([]string, []string) {
	ready := []string{}
	pending := []string{}
	for _, sessionID := range sessions {
		state, ok := readHelperState(d.helperStateDir(), int(sessionID))
		label := fmt.Sprintf("Session %d", sessionID)
		if ok && state.PID > 0 {
			label = fmt.Sprintf("Session %d (pid %d)", sessionID, state.PID)
		}
		if ok && strings.EqualFold(state.Status, "ready") && time.Since(time.Unix(state.UpdatedAt, 0)) <= helperReadyWindow {
			ready = append(ready, label+" Loaded Successfully")
		} else {
			pending = append(pending, label+" Pending")
		}
	}
	return ready, pending
}

func (d *Dispatcher) helperReady(sessionID uint32) bool {
	state, ok := readHelperState(d.helperStateDir(), int(sessionID))
	return ok && strings.EqualFold(state.Status, "ready") && time.Since(time.Unix(state.UpdatedAt, 0)) <= helperReadyWindow
}

func (d *Dispatcher) helperStateDir() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if strings.TrimSpace(d.stateDir) == "" {
		d.stateDir = helperStateDir("")
	}
	return d.stateDir
}

func launchHelperProcess(ctx context.Context, sessionID uint32, stateDir string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	token, err := tokenForSession(sessionID)
	if err != nil {
		return 0, err
	}
	defer token.Close()

	var env *uint16
	if err := windows.CreateEnvironmentBlock(&env, token, false); err != nil {
		return 0, err
	}
	defer windows.DestroyEnvironmentBlock(env)

	args := []string{
		exe,
		"--helper",
		"--helper-session-id", strconv.Itoa(int(sessionID)),
		"--helper-state-dir", stateDir,
	}
	desktop, _ := windows.UTF16PtrFromString(`winsta0\default`)
	cwd := currentUserWorkingDirectory()
	cwdPtr, _ := windows.UTF16PtrFromString(cwd)
	appPtr, _ := windows.UTF16PtrFromString(exe)
	cmdPtr, _ := windows.UTF16PtrFromString(windowsCommandLine(args))
	startupInfo := &windows.StartupInfo{
		Cb:      uint32(unsafe.Sizeof(windows.StartupInfo{})),
		Desktop: desktop,
	}
	var processInfo windows.ProcessInformation
	creationFlags := uint32(windows.CREATE_UNICODE_ENVIRONMENT | createNewProcessGrp | windows.CREATE_NO_WINDOW)
	if err := windows.CreateProcessAsUser(token, appPtr, cmdPtr, nil, nil, false, creationFlags, env, cwdPtr, startupInfo, &processInfo); err != nil {
		return 0, err
	}
	defer windows.CloseHandle(processInfo.Thread)
	defer windows.CloseHandle(processInfo.Process)
	return int(processInfo.ProcessId), nil
}

func tokenForSession(sessionID uint32) (windows.Token, error) {
	var userToken windows.Token
	if err := windows.WTSQueryUserToken(sessionID, &userToken); err != nil {
		return 0, fmt.Errorf("query user token for session %d: %w", sessionID, err)
	}
	var primaryToken windows.Token
	desiredAccess := uint32(windows.TOKEN_ASSIGN_PRIMARY | windows.TOKEN_DUPLICATE | windows.TOKEN_QUERY | windows.TOKEN_ADJUST_DEFAULT | windows.TOKEN_ADJUST_SESSIONID)
	if err := windows.DuplicateTokenEx(userToken, desiredAccess, nil, windows.SecurityImpersonation, windows.TokenPrimary, &primaryToken); err != nil {
		_ = userToken.Close()
		return 0, fmt.Errorf("duplicate user token: %w", err)
	}
	_ = userToken.Close()
	return primaryToken, nil
}

func helperStateDir(override string) string {
	return localui.StateDir(override)
}

func helperStatePath(stateDir string, sessionID int) string {
	return filepath.Join(helperStateDir(stateDir), fmt.Sprintf("session-%d.json", sessionID))
}

func writeHelperState(stateDir string, state helperStateFile) {
	if state.SessionID <= 0 {
		return
	}
	dir := helperStateDir(stateDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	path := helperStatePath(dir, state.SessionID)
	temp := path + "." + randomHex(4) + ".tmp"
	if err := os.WriteFile(temp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(temp, path)
}

func readHelperState(stateDir string, sessionID int) (helperStateFile, bool) {
	data, err := os.ReadFile(helperStatePath(stateDir, sessionID))
	if err != nil {
		return helperStateFile{}, false
	}
	var state helperStateFile
	if err := json.Unmarshal(data, &state); err != nil {
		return helperStateFile{}, false
	}
	if state.SessionID != sessionID {
		return helperStateFile{}, false
	}
	return state, true
}

func randomHex(size int) string {
	if size <= 0 {
		size = 4
	}
	buffer := make([]byte, size)
	if _, err := cryptoRand.Read(buffer); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(buffer)
}
