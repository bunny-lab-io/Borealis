package softwaremanagement

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bunny-lab-io/borealis/go-agent/internal/auth"
)

const (
	softwareRefreshInterval = 5 * time.Minute
	softwareBoostInterval   = 5 * time.Second
	softwareBoostWindow     = 45 * time.Second
	softwareCommandTimeout  = 2 * time.Minute
)

type Manager struct {
	authClient        *auth.Client
	hostname          string
	serviceMode       string
	runner            commandRunner
	publisher         func(context.Context, []Software) error
	wakeup            chan struct{}
	mu                sync.Mutex
	started           bool
	loopRunning       bool
	supported         bool
	unsupportedReason string
	lastError         string
	lastRefreshAt     int64
	lastSoftwareCount int
	fastPollUntil     time.Time
}

type Software struct {
	Name     string         `json:"name"`
	Version  string         `json:"version"`
	Source   string         `json:"source"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type RoleHealth struct {
	Status     string
	StatusCode string
	Detail     string
	Details    map[string]any
}

type commandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type commandRunner func(ctx context.Context, timeout time.Duration, name string, args ...string) (commandResult, error)

func New(authClient *auth.Client, hostname string, serviceMode string) *Manager {
	supported, reason := detectSupport()
	manager := &Manager{
		authClient:        authClient,
		hostname:          strings.TrimSpace(hostname),
		serviceMode:       auth.NormalizeServiceMode(serviceMode),
		runner:            runCommand,
		wakeup:            make(chan struct{}, 1),
		supported:         supported,
		unsupportedReason: reason,
	}
	manager.publisher = manager.publishSoftware
	return manager
}

func detectSupport() (bool, string) {
	switch runtime.GOOS {
	case "windows":
		return true, ""
	case "linux":
		if _, err := exec.LookPath("dpkg-query"); err == nil {
			return true, ""
		}
		if _, err := exec.LookPath("rpm"); err == nil {
			return true, ""
		}
		return false, "No supported package inventory tools are available on this Linux agent."
	default:
		return false, fmt.Sprintf("Unsupported software-management platform '%s'.", runtime.GOOS)
	}
}

func (m *Manager) Start(ctx context.Context) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	m.started = true
	if !m.supported {
		m.lastError = m.unsupportedReason
		m.mu.Unlock()
		return
	}
	m.loopRunning = true
	m.mu.Unlock()
	go m.pollLoop(ctx)
}

func (m *Manager) Health() RoleHealth {
	m.mu.Lock()
	supported := m.supported
	unsupportedReason := m.unsupportedReason
	loopRunning := m.loopRunning
	lastError := m.lastError
	lastRefreshAt := m.lastRefreshAt
	lastSoftwareCount := m.lastSoftwareCount
	m.mu.Unlock()
	details := map[string]any{
		"running_status":      "Running",
		"software_count":      strconv.Itoa(lastSoftwareCount),
		"last_refresh_at":     strconv.FormatInt(lastRefreshAt, 10),
		"refresh_interval_ms": strconv.Itoa(int(softwareRefreshInterval / time.Millisecond)),
		"runtime":             "go",
	}
	if !supported {
		details["running_status"] = "Unsupported"
		return RoleHealth{
			Status:     "unsupported",
			StatusCode: "unsupported",
			Detail:     fallbackText(unsupportedReason, "Software management is unsupported on this platform."),
			Details:    details,
		}
	}
	if !loopRunning {
		details["running_status"] = "Stopped"
		return RoleHealth{
			Status:     "pending",
			StatusCode: "pending",
			Detail:     "Waiting for software inventory refresh loop.",
			Details:    details,
		}
	}
	if strings.TrimSpace(lastError) != "" {
		details["last_error"] = lastError
		return RoleHealth{
			Status:     "recovering",
			StatusCode: "recovering",
			Detail:     lastError,
			Details:    details,
		}
	}
	if lastRefreshAt <= 0 {
		return RoleHealth{
			Status:     "pending",
			StatusCode: "pending",
			Detail:     "Waiting for initial software inventory snapshot.",
			Details:    details,
		}
	}
	return RoleHealth{
		Status:     "healthy",
		StatusCode: "healthy",
		Detail:     "Software inventory refresh loop active.",
		Details:    details,
	}
}

func (m *Manager) HandleRefreshRequest(ctx context.Context, payload any) (any, error) {
	body, ok := payload.(map[string]any)
	if !ok {
		return errorResponse("invalid_request", "Software refresh payload must be an object."), nil
	}
	if !m.matchesTarget(body) {
		return errorResponse("not_for_host", "The software refresh request targeted another device."), nil
	}
	if !m.supported {
		return errorResponse("unsupported_platform", fallbackText(m.unsupportedReason, "Software management is unsupported on this platform.")), nil
	}
	reason := cleanText(body["reason"])
	if reason == "" {
		reason = "operator:" + fallbackText(cleanText(body["requested_by"]), "unknown")
	}
	m.RequestRefresh(reason)
	return map[string]any{
		"ok":     true,
		"status": "accepted",
		"reason": reason,
	}, nil
}

func (m *Manager) RequestRefresh(reason string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.fastPollUntil = time.Now().Add(softwareBoostWindow)
	loopRunning := m.loopRunning
	m.mu.Unlock()
	select {
	case m.wakeup <- struct{}{}:
	default:
	}
	if !loopRunning {
		go func() {
			_ = m.Refresh(context.Background())
		}()
	}
}

func (m *Manager) Refresh(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if !m.supported {
		return fmt.Errorf("%s", fallbackText(m.unsupportedReason, "software management unsupported"))
	}
	rows, err := m.collectSoftware(ctx)
	if err != nil {
		m.recordError("Software inventory refresh failed: " + err.Error())
		return err
	}
	if err := m.publisher(ctx, rows); err != nil {
		m.recordError("Software inventory publish failed: " + err.Error())
		return err
	}
	m.mu.Lock()
	m.lastError = ""
	m.lastRefreshAt = time.Now().Unix()
	m.lastSoftwareCount = len(rows)
	m.mu.Unlock()
	return nil
}

func (m *Manager) pollLoop(ctx context.Context) {
	defer func() {
		m.mu.Lock()
		m.loopRunning = false
		m.mu.Unlock()
	}()
	for {
		if err := m.Refresh(ctx); err != nil {
			m.recordError("Software inventory refresh failed: " + err.Error())
		}
		waitFor := softwareRefreshInterval
		m.mu.Lock()
		if time.Now().Before(m.fastPollUntil) {
			waitFor = softwareBoostInterval
		}
		m.mu.Unlock()
		timer := time.NewTimer(waitFor)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-m.wakeup:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (m *Manager) collectSoftware(ctx context.Context) ([]Software, error) {
	switch runtime.GOOS {
	case "windows":
		return m.collectWindowsSoftware(ctx)
	case "linux":
		return m.collectLinuxSoftware(ctx)
	default:
		return nil, fmt.Errorf("%s", fallbackText(m.unsupportedReason, "unsupported platform"))
	}
}

func (m *Manager) collectWindowsSoftware(ctx context.Context) ([]Software, error) {
	result, err := m.runner(ctx, softwareCommandTimeout, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", windowsSoftwareInventoryScript())
	if err != nil || result.ExitCode != 0 {
		return nil, commandError(result, err)
	}
	return parseWindowsSoftware(result.Stdout)
}

func (m *Manager) collectLinuxSoftware(ctx context.Context) ([]Software, error) {
	if _, err := exec.LookPath("dpkg-query"); err == nil {
		result, runErr := m.runner(ctx, softwareCommandTimeout, "dpkg-query", "-W", "-f=${Package}\t${Version}\n")
		if runErr != nil || result.ExitCode != 0 {
			return nil, commandError(result, runErr)
		}
		return parseLinuxPackages(result.Stdout, "dpkg"), nil
	}
	result, runErr := m.runner(ctx, softwareCommandTimeout, "rpm", "-qa", "--qf", "%{NAME}\t%{VERSION}-%{RELEASE}\n")
	if runErr != nil || result.ExitCode != 0 {
		return nil, commandError(result, runErr)
	}
	return parseLinuxPackages(result.Stdout, "rpm"), nil
}

func (m *Manager) publishSoftware(ctx context.Context, rows []Software) error {
	if m.authClient == nil {
		return fmt.Errorf("auth client unavailable")
	}
	payload := map[string]any{
		"agent_id":     m.authClient.AgentID(),
		"hostname":     m.hostname,
		"service_mode": m.serviceMode,
		"details": map[string]any{
			"summary": map[string]any{
				"hostname":     m.hostname,
				"agent_id":     m.authClient.AgentID(),
				"service_mode": m.serviceMode,
			},
			"software": rows,
		},
	}
	_, err := m.authClient.PostJSON(ctx, "/api/agent/details", payload, nil)
	return err
}

func (m *Manager) recordError(message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastError = strings.TrimSpace(message)
}

func (m *Manager) matchesTarget(payload map[string]any) bool {
	targetHostname := strings.ToLower(cleanText(firstValue(payload, "hostname", "target_hostname")))
	if targetHostname != "" && targetHostname != strings.ToLower(strings.TrimSpace(m.hostname)) {
		return false
	}
	targetAgent := cleanText(payload["agent_id"])
	if targetAgent != "" && m.authClient != nil && targetAgent != m.authClient.AgentID() {
		return false
	}
	return true
}

func parseLinuxPackages(output string, source string) []Software {
	rows := []Software{}
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		name := cleanText(parts[0])
		if name == "" {
			continue
		}
		version := ""
		if len(parts) > 1 {
			version = cleanText(parts[1])
		}
		rows = append(rows, Software{Name: name, Version: version, Source: source})
	}
	return dedupeAndSort(rows)
}

func parseWindowsSoftware(output string) ([]Software, error) {
	if strings.TrimSpace(output) == "" {
		return []Software{}, nil
	}
	var raw any
	if err := json.Unmarshal([]byte(output), &raw); err != nil {
		return nil, err
	}
	items := []map[string]any{}
	switch value := raw.(type) {
	case []any:
		for _, item := range value {
			if mapped, ok := item.(map[string]any); ok {
				items = append(items, mapped)
			}
		}
	case map[string]any:
		items = append(items, value)
	}
	rows := make([]Software, 0, len(items))
	for _, item := range items {
		name := cleanText(item["name"])
		if name == "" {
			continue
		}
		source := normalizeSoftwareSource(item["source"])
		if source == "" {
			source = "local_installed"
		}
		row := Software{
			Name:     name,
			Version:  cleanText(item["version"]),
			Source:   source,
			Metadata: normalizeMetadata(item["metadata"]),
		}
		if len(row.Metadata) == 0 {
			row.Metadata = nil
		}
		rows = append(rows, row)
	}
	return dedupeAndSort(rows), nil
}

func dedupeAndSort(rows []Software) []Software {
	deduped := map[string]Software{}
	for _, row := range rows {
		row.Name = cleanText(row.Name)
		if row.Name == "" {
			continue
		}
		row.Version = cleanText(row.Version)
		row.Source = normalizeSoftwareSource(row.Source)
		if row.Source == "" {
			row.Source = "local_installed"
		}
		if len(row.Metadata) == 0 {
			row.Metadata = nil
		}
		key := strings.ToLower(row.Name) + "\x00" + strings.ToLower(row.Version) + "\x00" + strings.ToLower(row.Source)
		deduped[key] = row
	}
	out := make([]Software, 0, len(deduped))
	for _, row := range deduped {
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := strings.ToLower(out[i].Name) + "\x00" + strings.ToLower(out[i].Source) + "\x00" + strings.ToLower(out[i].Version)
		right := strings.ToLower(out[j].Name) + "\x00" + strings.ToLower(out[j].Source) + "\x00" + strings.ToLower(out[j].Version)
		return left < right
	})
	return out
}

func normalizeSoftwareSource(value any) string {
	text := strings.ToLower(cleanText(value))
	aliases := map[string]string{
		"":                         "local_installed",
		"local":                    "local_installed",
		"installed":                "local_installed",
		"registry":                 "local_installed",
		"registry_uninstall":       "local_installed",
		"local_installed":          "local_installed",
		"windows":                  "local_installed",
		"win32":                    "local_installed",
		"windows_store":            "windows_store",
		"store":                    "windows_store",
		"appx":                     "windows_store",
		"uwp":                      "windows_store",
		"microsoft_store":          "windows_store",
		"dpkg":                     "dpkg",
		"rpm":                      "rpm",
		"debian":                   "dpkg",
		"redhat":                   "rpm",
		"red_hat":                  "rpm",
		"linux_dpkg":               "dpkg",
		"linux_rpm":                "rpm",
		"windows_package_manager":  "local_installed",
		"windows_package_registry": "local_installed",
	}
	if normalized, ok := aliases[text]; ok {
		return normalized
	}
	return text
}

func normalizeMetadata(value any) map[string]any {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]any{}
	for key, rawValue := range raw {
		cleanKey := cleanText(key)
		if cleanKey == "" || rawValue == nil {
			continue
		}
		switch typed := rawValue.(type) {
		case string:
			if cleanText(typed) != "" {
				out[cleanKey] = cleanText(typed)
			}
		case bool:
			out[cleanKey] = typed
		case float64:
			if typed != 0 {
				out[cleanKey] = typed
			}
		default:
			if cleanText(typed) != "" {
				out[cleanKey] = typed
			}
		}
	}
	return out
}

func windowsSoftwareInventoryScript() string {
	return `$ErrorActionPreference='SilentlyContinue'; ` +
		`$rows=@(); ` +
		`$paths=@('HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*','HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*','HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*'); ` +
		`foreach($p in $paths){ try { Get-ItemProperty -Path $p -ErrorAction SilentlyContinue | Where-Object { $_.DisplayName -and "$($_.DisplayName)".Trim().Length -gt 0 } | ForEach-Object { $m=[ordered]@{}; if($_.Publisher){$m.publisher="$($_.Publisher)".Trim()}; if($_.InstallLocation){$m.install_location="$($_.InstallLocation)".Trim()}; if($_.InstallDate){$m.install_date="$($_.InstallDate)".Trim()}; if($_.EstimatedSize){$m.estimated_size_kb=[int64]$_.EstimatedSize}; if($_.DisplayIcon){$m.display_icon="$($_.DisplayIcon)".Trim()}; if($_.UninstallString){$m.uninstall_string="$($_.UninstallString)".Trim()}; if($_.QuietUninstallString){$m.quiet_uninstall_string="$($_.QuietUninstallString)".Trim()}; if($_.PSChildName){$m.product_code="$($_.PSChildName)".Trim()}; if($null -ne $_.WindowsInstaller -and "$($_.WindowsInstaller)" -ne ''){$m.windows_installer=[bool]$_.WindowsInstaller}; $rows += [pscustomobject]@{name="$($_.DisplayName)".Trim(); version="$($_.DisplayVersion)".Trim(); source='local_installed'; metadata=$m} } } catch {} }; ` +
		`try { Get-AppxPackage -AllUsers -ErrorAction Stop | Where-Object { -not $_.IsFramework -and -not $_.IsResourcePackage } | ForEach-Object { $m=[ordered]@{}; if($_.Publisher){$m.publisher="$($_.Publisher)".Trim()}; if($_.InstallLocation){$m.install_location="$($_.InstallLocation)".Trim()}; if($_.PackageFamilyName){$m.package_family_name="$($_.PackageFamilyName)".Trim()}; if($null -ne $_.NonRemovable){$m.non_removable=[bool]$_.NonRemovable}; $rows += [pscustomobject]@{name="$($_.Name)".Trim(); version="$($_.Version)".Trim(); source='windows_store'; metadata=$m} } } catch { try { Get-AppxPackage -ErrorAction SilentlyContinue | Where-Object { -not $_.IsFramework -and -not $_.IsResourcePackage } | ForEach-Object { $m=[ordered]@{}; if($_.Publisher){$m.publisher="$($_.Publisher)".Trim()}; if($_.InstallLocation){$m.install_location="$($_.InstallLocation)".Trim()}; if($_.PackageFamilyName){$m.package_family_name="$($_.PackageFamilyName)".Trim()}; if($null -ne $_.NonRemovable){$m.non_removable=[bool]$_.NonRemovable}; $rows += [pscustomobject]@{name="$($_.Name)".Trim(); version="$($_.Version)".Trim(); source='windows_store'; metadata=$m} } } catch {} }; ` +
		`$rows | Sort-Object name,source,version -Unique | ConvertTo-Json -Depth 6 -Compress`
}

func runCommand(ctx context.Context, timeout time.Duration, name string, args ...string) (commandResult, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, name, args...)
	stdout, stderr := strings.Builder{}, strings.Builder{}
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		exitCode = -1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}
	if runCtx.Err() == context.DeadlineExceeded {
		return commandResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode}, runCtx.Err()
	}
	return commandResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode}, err
}

func commandError(result commandResult, err error) error {
	detail := cleanText(result.Stderr)
	if detail == "" {
		detail = cleanText(result.Stdout)
	}
	if detail == "" && err != nil {
		detail = err.Error()
	}
	if detail == "" {
		detail = fmt.Sprintf("exit %d", result.ExitCode)
	}
	return fmt.Errorf("%s", detail)
}

func errorResponse(code string, message string) map[string]any {
	return map[string]any{"ok": false, "error": code, "message": message}
}

func cleanText(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func firstValue(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok && cleanText(value) != "" {
			return value
		}
	}
	return nil
}

func fallbackText(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}
