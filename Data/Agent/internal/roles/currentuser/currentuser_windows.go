//go:build windows

package currentuser

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/bunny-lab-io/borealis/go-agent/internal/scripts"
	"golang.org/x/sys/windows"
)

const (
	infiniteWait        = 0xffffffff
	maxCurrentUserWait  = 6 * time.Hour
	waitPollMillisecond = 250
)

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
				"broker_mode":    "direct_session_launch",
			},
		}
	}
	if len(sessions) == 0 {
		return RoleHealth{
			Status:     "not_applicable",
			StatusCode: "not_applicable",
			Detail:     "No active interactive Windows user session.",
			Details: map[string]any{
				"running_status": "No Active Session",
				"runtime":        "go",
				"broker_mode":    "direct_session_launch",
				"ready_helpers":  "0",
			},
		}
	}
	return RoleHealth{
		Status:     "healthy",
		StatusCode: "healthy",
		Detail:     fmt.Sprintf("%d active Windows user session(s) available.", len(sessions)),
		Details: map[string]any{
			"running_status":    "Ready",
			"runtime":           "go",
			"broker_mode":       "direct_session_launch",
			"execution_context": "CURRENTUSER",
			"ready_helpers":     strconv.Itoa(len(sessions)),
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
	var lines []string
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
	if waitErr != nil {
		return scripts.Result{ReturnCode: -1, Stdout: stdoutText, Stderr: joinStderr(stderrText, waitErr.Error())}, nil
	}
	return scripts.Result{ReturnCode: int(exitCode), Stdout: stdoutText, Stderr: stderrText}, nil
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
