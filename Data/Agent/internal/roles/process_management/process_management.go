package processmanagement

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	refreshIntervalSeconds = 5.0
	defaultCommandTimeout  = 8 * time.Second
	terminateTimeout       = 15 * time.Second
)

type Manager struct {
	hostname string
	mu       sync.Mutex
	snapshot Snapshot
	lastErr  string
}

type Snapshot struct {
	ReportedAt        int64
	RefreshIntervalMS int
	Processes         []Process
}

type Process struct {
	ID                    string   `json:"id"`
	PID                   int      `json:"pid"`
	ParentPID             int      `json:"parent_pid"`
	Name                  string   `json:"name"`
	CPUPercent            float64  `json:"cpu_percent"`
	RawCPUPercent         float64  `json:"raw_cpu_percent"`
	MemoryPercent         float64  `json:"memory_percent"`
	MemoryBytes           int64    `json:"memory_bytes"`
	DiskBytesPerSecond    float64  `json:"disk_bytes_per_second"`
	NetworkBytesPerSecond *float64 `json:"network_bytes_per_second"`
	CommandLine           string   `json:"command_line"`
	ExecutablePath        string   `json:"executable_path"`
	Username              string   `json:"username"`
	Status                string   `json:"status"`
	CreatedAt             float64  `json:"created_at"`
	CapturedAt            int64    `json:"captured_at"`
	ChildCount            int      `json:"child_count"`
	HasChildren           bool     `json:"has_children"`
}

type RoleHealth struct {
	Status     string
	StatusCode string
	Detail     string
	Details    map[string]any
}

type pmError struct {
	Code    string
	Message string
}

func (e pmError) Error() string {
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return e.Code
}

func New(hostname string) *Manager {
	return &Manager{
		hostname: strings.TrimSpace(hostname),
		snapshot: Snapshot{
			ReportedAt:        0,
			RefreshIntervalMS: int(refreshIntervalSeconds * 1000),
			Processes:         []Process{},
		},
	}
}

func (m *Manager) HandleRequest(ctx context.Context, payload any) (any, error) {
	body, ok := payload.(map[string]any)
	if !ok {
		return errorResponse("invalid_request", "Process request payload must be an object."), nil
	}
	if !m.matchesTarget(body) {
		return errorResponse("not_for_host", "The process-management request targeted another device."), nil
	}
	action := strings.ToLower(cleanText(body["action"]))
	if action == "" {
		action = "list"
	}
	switch action {
	case "list", "snapshot":
		response, err := m.list(ctx, maxFloat(0.25, asFloat(body["max_age_seconds"], refreshIntervalSeconds)))
		if err != nil {
			return m.handleError(err), nil
		}
		return response, nil
	case "terminate", "kill", "end_task":
		response, err := m.terminate(ctx, asInt(body["pid"]), asBool(body["include_children"]))
		if err != nil {
			return m.handleError(err), nil
		}
		return response, nil
	default:
		return errorResponse("invalid_action", fmt.Sprintf("Unsupported process action '%s'.", action)), nil
	}
}

func (m *Manager) Health() RoleHealth {
	m.mu.Lock()
	lastErr := m.lastErr
	lastRefresh := m.snapshot.ReportedAt
	processCount := len(m.snapshot.Processes)
	m.mu.Unlock()
	details := map[string]any{
		"running_status":      "Ready",
		"process_count":       strconv.Itoa(processCount),
		"last_refresh_at":     strconv.FormatInt(lastRefresh, 10),
		"refresh_interval_ms": strconv.Itoa(int(refreshIntervalSeconds * 1000)),
		"runtime":             "go",
	}
	if lastErr != "" {
		details["last_error"] = lastErr
		return RoleHealth{
			Status:     "recovering",
			StatusCode: "recovering",
			Detail:     lastErr,
			Details:    details,
		}
	}
	return RoleHealth{
		Status:     "healthy",
		StatusCode: "healthy",
		Detail:     "Process management request handler is ready.",
		Details:    details,
	}
}

func (m *Manager) list(ctx context.Context, maxAgeSeconds float64) (map[string]any, error) {
	m.mu.Lock()
	current := m.snapshot
	m.mu.Unlock()
	if current.ReportedAt > 0 && time.Since(time.Unix(current.ReportedAt, 0)).Seconds() <= maxAgeSeconds {
		return snapshotResponse(current), nil
	}
	snapshot, err := collectSnapshot(ctx)
	if err != nil {
		m.setLastError(err.Error())
		return nil, err
	}
	m.mu.Lock()
	m.snapshot = snapshot
	m.lastErr = ""
	m.mu.Unlock()
	return snapshotResponse(snapshot), nil
}

func (m *Manager) terminate(ctx context.Context, pid int, includeChildren bool) (map[string]any, error) {
	if pid <= 0 {
		return nil, newError("pid_required", "A process id is required.")
	}
	if pid == os.Getpid() {
		return nil, newError("protected_process", "Borealis will not terminate its own agent process.")
	}
	targets := []int{pid}
	if includeChildren {
		if snapshot, err := collectSnapshot(ctx); err == nil {
			targets = descendantPIDs(snapshot.Processes, pid)
			targets = append(targets, pid)
		}
	}
	terminated, err := terminateProcesses(ctx, uniqueInts(targets))
	if err != nil {
		m.setLastError(err.Error())
		return nil, err
	}
	time.Sleep(250 * time.Millisecond)
	snapshot, err := collectSnapshot(ctx)
	if err != nil {
		m.setLastError(err.Error())
		return nil, err
	}
	m.mu.Lock()
	m.snapshot = snapshot
	m.lastErr = ""
	m.mu.Unlock()
	response := snapshotResponse(snapshot)
	response["terminated_pids"] = terminated
	return response, nil
}

func (m *Manager) matchesTarget(payload map[string]any) bool {
	targetHostname := strings.ToLower(cleanText(firstValue(payload, "hostname", "target_hostname")))
	if targetHostname != "" && targetHostname != strings.ToLower(strings.TrimSpace(m.hostname)) {
		return false
	}
	return true
}

func (m *Manager) handleError(err error) map[string]any {
	pmErr := normalizeError(err)
	m.setLastError(pmErr.Message)
	return errorResponse(pmErr.Code, pmErr.Message)
}

func (m *Manager) setLastError(value string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastErr = strings.TrimSpace(value)
}

func collectSnapshot(ctx context.Context) (Snapshot, error) {
	if runtime.GOOS == "windows" {
		return collectWindowsSnapshot(ctx)
	}
	return collectPOSIXSnapshot(ctx)
}

func collectWindowsSnapshot(ctx context.Context) (Snapshot, error) {
	command := `$os = Get-CimInstance Win32_OperatingSystem; $total = [double]$os.TotalVisibleMemorySize * 1024; ` +
		`$items = Get-CimInstance Win32_Process | Select-Object ProcessId,ParentProcessId,Name,CommandLine,ExecutablePath,WorkingSetSize,CreationDate; ` +
		`$items | ForEach-Object { [pscustomobject]@{ ProcessId=$_.ProcessId; ParentProcessId=$_.ParentProcessId; Name=$_.Name; CommandLine=$_.CommandLine; ExecutablePath=$_.ExecutablePath; WorkingSetSize=$_.WorkingSetSize; CreationDate=$_.CreationDate; TotalMemory=$total } } | ConvertTo-Json -Depth 3 -Compress`
	output, err := runCommand(ctx, defaultCommandTimeout, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", command)
	if err != nil {
		output, err = runCommand(ctx, defaultCommandTimeout, "powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", command)
	}
	if err != nil {
		return Snapshot{}, err
	}
	var raw any
	if err := json.Unmarshal([]byte(output), &raw); err != nil {
		return Snapshot{}, newError("agent_error", err.Error())
	}
	rows := mapSlice(raw)
	now := time.Now().Unix()
	processes := make([]Process, 0, len(rows))
	for _, row := range rows {
		pid := asInt(row["ProcessId"])
		if pid <= 0 {
			continue
		}
		memoryBytes := asInt64(row["WorkingSetSize"])
		totalMemory := asFloat(row["TotalMemory"], 0)
		name := cleanText(row["Name"])
		if name == "" {
			name = fmt.Sprintf("PID %d", pid)
		}
		executablePath := cleanText(row["ExecutablePath"])
		commandLine := cleanText(row["CommandLine"])
		if commandLine == "" {
			commandLine = executablePath
		}
		if commandLine == "" {
			commandLine = name
		}
		createdAt := windowsCIMTime(row["CreationDate"])
		processes = append(processes, Process{
			ID:                 fmt.Sprintf("%d:%d", pid, int64(createdAt)),
			PID:                pid,
			ParentPID:          asInt(row["ParentProcessId"]),
			Name:               name,
			CPUPercent:         0,
			RawCPUPercent:      0,
			MemoryPercent:      memoryPercent(memoryBytes, totalMemory),
			MemoryBytes:        memoryBytes,
			DiskBytesPerSecond: 0,
			CommandLine:        commandLine,
			ExecutablePath:     executablePath,
			Username:           "",
			Status:             "",
			CreatedAt:          createdAt,
			CapturedAt:         now,
		})
	}
	finalizeProcessRows(processes)
	return Snapshot{ReportedAt: now, RefreshIntervalMS: int(refreshIntervalSeconds * 1000), Processes: sortProcesses(processes)}, nil
}

func collectPOSIXSnapshot(ctx context.Context) (Snapshot, error) {
	output, err := runCommand(ctx, defaultCommandTimeout, "ps", "-eo", "pid=,ppid=,pcpu=,rss=,user=,stat=,comm=,args=")
	if err != nil {
		return Snapshot{}, err
	}
	now := time.Now().Unix()
	totalMemory := totalMemoryBytes()
	processes := []Process{}
	for _, line := range strings.Split(output, "\n") {
		parts := strings.Fields(line)
		if len(parts) < 7 {
			continue
		}
		pid := asInt(parts[0])
		if pid <= 0 {
			continue
		}
		ppid := asInt(parts[1])
		rawCPU := maxFloat(0, asFloat(parts[2], 0))
		memoryBytes := asInt64(parts[3]) * 1024
		username := cleanText(parts[4])
		status := cleanText(parts[5])
		name := cleanText(parts[6])
		commandLine := ""
		if len(parts) > 7 {
			commandLine = strings.Join(parts[7:], " ")
		}
		if commandLine == "" {
			commandLine = name
		}
		executablePath := resolveExecutablePath(pid)
		createdAt := processStartTime(pid)
		processes = append(processes, Process{
			ID:                 fmt.Sprintf("%d:%d", pid, int64(createdAt)),
			PID:                pid,
			ParentPID:          ppid,
			Name:               nameOrPID(name, pid),
			CPUPercent:         normalizeCPU(rawCPU),
			RawCPUPercent:      roundFloat(rawCPU, 2),
			MemoryPercent:      memoryPercent(memoryBytes, float64(totalMemory)),
			MemoryBytes:        memoryBytes,
			DiskBytesPerSecond: 0,
			CommandLine:        commandLine,
			ExecutablePath:     executablePath,
			Username:           username,
			Status:             status,
			CreatedAt:          createdAt,
			CapturedAt:         now,
		})
	}
	finalizeProcessRows(processes)
	return Snapshot{ReportedAt: now, RefreshIntervalMS: int(refreshIntervalSeconds * 1000), Processes: sortProcesses(processes)}, nil
}

func terminateProcesses(ctx context.Context, pids []int) ([]int, error) {
	if len(pids) == 0 {
		return nil, newError("process_not_found", "The process is no longer running.")
	}
	if runtime.GOOS == "windows" {
		return terminateWindows(ctx, pids)
	}
	return terminatePOSIX(pids)
}

func terminateWindows(ctx context.Context, pids []int) ([]int, error) {
	terminated := []int{}
	for _, pid := range pids {
		args := []string{"/PID", strconv.Itoa(pid), "/F"}
		if len(pids) > 1 {
			args = append(args, "/T")
		}
		output, err := runCommand(ctx, terminateTimeout, "taskkill", args...)
		if err != nil {
			detail := strings.ToLower(err.Error() + " " + output)
			switch {
			case strings.Contains(detail, "not found"), strings.Contains(detail, "not running"):
				return nil, newError("process_not_found", "The process is no longer running.")
			case strings.Contains(detail, "access is denied"), strings.Contains(detail, "permission"):
				return nil, newError("access_denied", "Access denied while terminating process.")
			default:
				return nil, newError("termination_failed", strings.TrimSpace(err.Error()+" "+output))
			}
		}
		terminated = append(terminated, pid)
	}
	return uniqueInts(terminated), nil
}

func terminatePOSIX(pids []int) ([]int, error) {
	terminated := []int{}
	for _, pid := range pids {
		if pid <= 0 || pid == os.Getpid() {
			continue
		}
		if !processExists(pid) {
			continue
		}
		output, err := runCommand(context.Background(), 3*time.Second, "kill", "-TERM", strconv.Itoa(pid))
		if err != nil {
			detail := strings.ToLower(err.Error() + " " + output)
			if strings.Contains(detail, "permission") || strings.Contains(detail, "operation not permitted") {
				return nil, newError("access_denied", "Access denied while terminating process.")
			}
			if strings.Contains(detail, "no such process") {
				continue
			}
			return nil, newError("termination_failed", strings.TrimSpace(err.Error()+" "+output))
		}
		terminated = append(terminated, pid)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		allGone := true
		for _, pid := range terminated {
			if processExists(pid) {
				allGone = false
				break
			}
		}
		if allGone {
			return uniqueInts(terminated), nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	for _, pid := range terminated {
		if processExists(pid) {
			_, _ = runCommand(context.Background(), 3*time.Second, "kill", "-KILL", strconv.Itoa(pid))
		}
	}
	if len(terminated) == 0 {
		return nil, newError("process_not_found", "The process is no longer running.")
	}
	return uniqueInts(terminated), nil
}

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	_, err := runCommand(context.Background(), 3*time.Second, "kill", "-0", strconv.Itoa(pid))
	return err == nil
}

func runCommand(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, name, args...)
	output, err := cmd.CombinedOutput()
	if runCtx.Err() == context.DeadlineExceeded {
		return string(output), newError("timeout", fmt.Sprintf("%s timed out.", name))
	}
	if err != nil {
		return string(output), err
	}
	return string(output), nil
}

func finalizeProcessRows(processes []Process) {
	parentCounts := map[int]int{}
	for _, item := range processes {
		if item.ParentPID > 0 {
			parentCounts[item.ParentPID]++
		}
	}
	for index := range processes {
		processes[index].ChildCount = parentCounts[processes[index].PID]
		processes[index].HasChildren = processes[index].ChildCount > 0
	}
}

func sortProcesses(processes []Process) []Process {
	out := append([]Process(nil), processes...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CPUPercent != out[j].CPUPercent {
			return out[i].CPUPercent > out[j].CPUPercent
		}
		if out[i].MemoryPercent != out[j].MemoryPercent {
			return out[i].MemoryPercent > out[j].MemoryPercent
		}
		left := strings.ToLower(out[i].Name)
		right := strings.ToLower(out[j].Name)
		if left != right {
			return left < right
		}
		return out[i].PID < out[j].PID
	})
	return out
}

func snapshotResponse(snapshot Snapshot) map[string]any {
	return map[string]any{
		"ok":                  true,
		"reported_at":         snapshot.ReportedAt,
		"refresh_interval_ms": snapshot.RefreshIntervalMS,
		"processes":           processesToMaps(snapshot.Processes),
	}
}

func processesToMaps(processes []Process) []map[string]any {
	out := make([]map[string]any, 0, len(processes))
	for _, item := range processes {
		out = append(out, map[string]any{
			"id":                       item.ID,
			"pid":                      item.PID,
			"parent_pid":               item.ParentPID,
			"name":                     item.Name,
			"cpu_percent":              item.CPUPercent,
			"raw_cpu_percent":          item.RawCPUPercent,
			"memory_percent":           item.MemoryPercent,
			"memory_bytes":             item.MemoryBytes,
			"disk_bytes_per_second":    item.DiskBytesPerSecond,
			"network_bytes_per_second": item.NetworkBytesPerSecond,
			"command_line":             item.CommandLine,
			"executable_path":          item.ExecutablePath,
			"username":                 item.Username,
			"status":                   item.Status,
			"created_at":               item.CreatedAt,
			"captured_at":              item.CapturedAt,
			"child_count":              item.ChildCount,
			"has_children":             item.HasChildren,
		})
	}
	return out
}

func descendantPIDs(processes []Process, rootPID int) []int {
	children := map[int][]int{}
	for _, item := range processes {
		if item.ParentPID > 0 {
			children[item.ParentPID] = append(children[item.ParentPID], item.PID)
		}
	}
	out := []int{}
	var visit func(int)
	visit = func(pid int) {
		for _, child := range children[pid] {
			visit(child)
			out = append(out, child)
		}
	}
	visit(rootPID)
	return out
}

func uniqueInts(values []int) []int {
	seen := map[int]bool{}
	out := []int{}
	for _, value := range values {
		if value <= 0 || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func resolveExecutablePath(pid int) string {
	path, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	if err == nil {
		return path
	}
	return ""
}

func processStartTime(pid int) float64 {
	info, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid)))
	if err != nil {
		return 0
	}
	return float64(info.ModTime().Unix())
}

func totalMemoryBytes() int64 {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return asInt64(fields[1]) * 1024
			}
		}
	}
	return 0
}

func windowsCIMTime(value any) float64 {
	raw := cleanText(value)
	if len(raw) < 14 {
		return 0
	}
	parsed, err := time.Parse("20060102150405", raw[:14])
	if err != nil {
		return 0
	}
	return float64(parsed.Unix())
}

func normalizeCPU(raw float64) float64 {
	return roundFloat(raw/float64(maxInt(1, runtime.NumCPU())), 2)
}

func memoryPercent(memoryBytes int64, totalMemory float64) float64 {
	if memoryBytes <= 0 || totalMemory <= 0 {
		return 0
	}
	return roundFloat((float64(memoryBytes)/totalMemory)*100, 2)
}

func nameOrPID(name string, pid int) string {
	if strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	return fmt.Sprintf("PID %d", pid)
}

func errorResponse(code string, message string) map[string]any {
	normalized := strings.TrimSpace(code)
	if normalized == "" {
		normalized = "agent_error"
	}
	return map[string]any{"ok": false, "error": normalized, "message": strings.TrimSpace(message)}
}

func normalizeError(err error) pmError {
	if err == nil {
		return pmError{Code: "agent_error", Message: "Unknown process-management error."}
	}
	if typed, ok := err.(pmError); ok {
		return typed
	}
	return pmError{Code: "agent_error", Message: err.Error()}
}

func newError(code string, message string) pmError {
	normalized := strings.TrimSpace(code)
	if normalized == "" {
		normalized = "agent_error"
	}
	return pmError{Code: normalized, Message: strings.TrimSpace(message)}
}

func cleanText(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func firstValue(row map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := row[key]; ok {
			return value
		}
	}
	return nil
}

func mapSlice(value any) []map[string]any {
	switch typed := value.(type) {
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if row, ok := item.(map[string]any); ok {
				out = append(out, row)
			}
		}
		return out
	case map[string]any:
		return []map[string]any{typed}
	default:
		return nil
	}
}

func asInt(value any) int {
	return int(asInt64(value))
}

func asInt64(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	default:
		parsed, _ := strconv.ParseInt(cleanText(value), 10, 64)
		return parsed
	}
}

func asFloat(value any, fallback float64) float64 {
	switch typed := value.(type) {
	case float32:
		return float64(typed)
	case float64:
		return typed
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		parsed, err := typed.Float64()
		if err == nil {
			return parsed
		}
		return fallback
	default:
		parsed, err := strconv.ParseFloat(cleanText(value), 64)
		if err != nil {
			return fallback
		}
		return parsed
	}
}

func asBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes", "y", "on":
			return true
		default:
			return false
		}
	default:
		return asInt64(value) != 0
	}
}

func roundFloat(value float64, places int) float64 {
	scale := 1.0
	for i := 0; i < places; i++ {
		scale *= 10
	}
	return float64(int(value*scale+0.5)) / scale
}

func maxFloat(left float64, right float64) float64 {
	if left > right {
		return left
	}
	return right
}

func maxInt(left int, right int) int {
	if left > right {
		return left
	}
	return right
}
