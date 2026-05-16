package softwaremanagement

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	iconExtractionBatchSize = 40
)

var (
	displayIconQuotedRe   = regexp.MustCompile(`^\s*"([^"]+)"\s*(?:,\s*(-?\d+))?\s*$`)
	displayIconResourceRe = regexp.MustCompile(`(?i)^\s*(.+?\.(?:exe|dll|ico|icl|cpl|ocx|scr))\s*(?:,\s*(-?\d+))?\s*$`)
)

type Manager struct {
	authClient        *auth.Client
	hostname          string
	serviceMode       string
	runner            commandRunner
	publisher         func(context.Context, Snapshot) error
	httpClient        *http.Client
	wakeup            chan struct{}
	mu                sync.Mutex
	started           bool
	loopRunning       bool
	supported         bool
	unsupportedReason string
	lastError         string
	lastRefreshAt     int64
	lastSoftwareCount int
	lastIconPayloads  int
	lastIconOverrides int
	lastIconSignature string
	lastIconHashByKey map[string]string
	iconOverrides     []map[string]any
	fastPollUntil     time.Time
}

type Software struct {
	Name     string         `json:"name"`
	Version  string         `json:"version"`
	Source   string         `json:"source"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type IconPayload struct {
	IconHash   string `json:"icon_hash"`
	MimeType   string `json:"mime_type"`
	DataBase64 string `json:"data_base64"`
}

type Snapshot struct {
	Software           []Software
	IconPayloads       []IconPayload
	IconHashByKey      map[string]string
	IconSignature      string
	IconCandidateCount int
	IconOverrideCount  int
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
		httpClient:        &http.Client{Timeout: 30 * time.Second},
		wakeup:            make(chan struct{}, 1),
		supported:         supported,
		unsupportedReason: reason,
		lastIconHashByKey: map[string]string{},
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
	lastIconPayloads := m.lastIconPayloads
	lastIconOverrides := m.lastIconOverrides
	m.mu.Unlock()
	details := map[string]any{
		"running_status":      "Running",
		"software_count":      strconv.Itoa(lastSoftwareCount),
		"icon_payload_count":  strconv.Itoa(lastIconPayloads),
		"icon_override_count": strconv.Itoa(lastIconOverrides),
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
	snapshot, err := m.buildSnapshot(ctx)
	if err != nil {
		m.recordError("Software inventory refresh failed: " + err.Error())
		return err
	}
	if err := m.publisher(ctx, snapshot); err != nil {
		m.recordError("Software inventory publish failed: " + err.Error())
		return err
	}
	m.mu.Lock()
	m.lastError = ""
	m.lastRefreshAt = time.Now().Unix()
	m.lastSoftwareCount = len(snapshot.Software)
	m.lastIconPayloads = len(snapshot.IconPayloads)
	m.lastIconOverrides = snapshot.IconOverrideCount
	m.lastIconSignature = snapshot.IconSignature
	m.lastIconHashByKey = cloneStringMap(snapshot.IconHashByKey)
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

func (m *Manager) buildSnapshot(ctx context.Context) (Snapshot, error) {
	rows, err := m.collectSoftware(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	overrides := m.fetchIconOverrides(ctx)
	if len(overrides) > 0 {
		rows = applySoftwareIconOverrides(rows, overrides)
	}
	signature := softwareIconSignature(rows)
	previousHashByKey := map[string]string{}
	m.mu.Lock()
	if signature == m.lastIconSignature {
		previousHashByKey = cloneStringMap(m.lastIconHashByKey)
	}
	m.mu.Unlock()
	iconPayloads, iconHashByKey := m.attachWindowsSoftwareIcons(ctx, rows, previousHashByKey)
	return Snapshot{
		Software:           rows,
		IconPayloads:       iconPayloads,
		IconHashByKey:      iconHashByKey,
		IconSignature:      signature,
		IconCandidateCount: iconCandidateCount(rows),
		IconOverrideCount:  len(overrides),
	}, nil
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

func (m *Manager) publishSoftware(ctx context.Context, snapshot Snapshot) error {
	if m.authClient == nil {
		return fmt.Errorf("auth client unavailable")
	}
	details := map[string]any{
		"summary": map[string]any{
			"hostname":     m.hostname,
			"agent_id":     m.authClient.AgentID(),
			"service_mode": m.serviceMode,
		},
		"software": snapshot.Software,
	}
	if len(snapshot.IconPayloads) > 0 {
		details["software_icon_payloads"] = snapshot.IconPayloads
	}
	payload := map[string]any{
		"agent_id":     m.authClient.AgentID(),
		"hostname":     m.hostname,
		"service_mode": m.serviceMode,
		"details":      details,
	}
	_, err := m.authClient.PostJSON(ctx, "/api/agent/details", payload, nil)
	return err
}

func (m *Manager) fetchIconOverrides(ctx context.Context) []map[string]any {
	if runtime.GOOS != "windows" || m.authClient == nil {
		return nil
	}
	if err := m.authClient.EnsureAuthenticated(ctx); err != nil {
		m.mu.Lock()
		cached := cloneMapSlice(m.iconOverrides)
		m.mu.Unlock()
		return cached
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.authClient.BaseURL()+"/api/agent/software-management/overrides", nil)
	if err != nil {
		return nil
	}
	for key, value := range m.authClient.AuthHeaders() {
		if strings.TrimSpace(value) != "" {
			req.Header.Set(key, value)
		}
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		m.mu.Lock()
		cached := cloneMapSlice(m.iconOverrides)
		m.mu.Unlock()
		return cached
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		m.mu.Lock()
		cached := cloneMapSlice(m.iconOverrides)
		m.mu.Unlock()
		return cached
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	raw, _ := payload["windows_icon_overrides"].([]any)
	overrides := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if mapped, ok := item.(map[string]any); ok {
			overrides = append(overrides, mapped)
		}
	}
	m.mu.Lock()
	m.iconOverrides = cloneMapSlice(overrides)
	m.mu.Unlock()
	return overrides
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

func softwareRowKey(row Software) string {
	return strings.ToLower(cleanText(row.Name)) + "::" + strings.ToLower(cleanText(row.Version)) + "::" + strings.ToLower(normalizeSoftwareSource(row.Source))
}

func softwareIconSignature(rows []Software) string {
	entries := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		metadata := row.Metadata
		entries = append(entries, map[string]any{
			"key":                           softwareRowKey(row),
			"display_icon":                  cleanText(metadata["display_icon"]),
			"display_icon_override_cleared": asBool(metadata["display_icon_override_cleared"]),
		})
	}
	data, err := json.Marshal(entries)
	if err != nil {
		data = []byte(fmt.Sprint(entries))
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
}

func applySoftwareIconOverrides(rows []Software, overrides []map[string]any) []Software {
	for index := range rows {
		for _, rule := range overrides {
			expectedName := cleanText(rule["name"])
			if expectedName == "" || !strings.EqualFold(expectedName, rows[index].Name) {
				continue
			}
			clearIcon := asBool(firstValue(rule, "clear_icon", "remove_icon"))
			displayIcon := cleanText(firstValue(rule, "display_icon", "icon_location"))
			if !clearIcon && displayIcon == "" {
				continue
			}
			if rows[index].Metadata == nil {
				rows[index].Metadata = map[string]any{}
			}
			metadata := rows[index].Metadata
			original := cleanText(metadata["display_icon"])
			if original != "" && (clearIcon || original != displayIcon) {
				if cleanText(metadata["original_display_icon"]) == "" {
					metadata["original_display_icon"] = original
				}
			}
			if clearIcon {
				metadata["display_icon"] = ""
				metadata["display_icon_override"] = ""
				metadata["display_icon_override_cleared"] = true
				delete(metadata, "icon_hash")
			} else {
				metadata["display_icon"] = displayIcon
				metadata["display_icon_override"] = displayIcon
				metadata["display_icon_override_cleared"] = false
			}
			if ruleID := cleanText(rule["rule_id"]); ruleID != "" {
				metadata["display_icon_override_rule_id"] = ruleID
			}
			break
		}
	}
	return rows
}

func (m *Manager) attachWindowsSoftwareIcons(ctx context.Context, rows []Software, previous map[string]string) ([]IconPayload, map[string]string) {
	iconHashByKey := cloneStringMap(previous)
	if runtime.GOOS != "windows" {
		return nil, iconHashByKey
	}
	pendingByHint := map[string][]int{}
	for index := range rows {
		if rows[index].Source != "local_installed" {
			continue
		}
		if rows[index].Metadata == nil {
			rows[index].Metadata = map[string]any{}
		}
		rowKey := softwareRowKey(rows[index])
		if rowKey == "" {
			continue
		}
		if asBool(rows[index].Metadata["display_icon_override_cleared"]) {
			delete(rows[index].Metadata, "icon_hash")
			delete(iconHashByKey, rowKey)
			continue
		}
		if cachedHash := cleanText(iconHashByKey[rowKey]); cachedHash != "" {
			rows[index].Metadata["icon_hash"] = strings.ToLower(cachedHash)
			continue
		}
		hint := cleanText(rows[index].Metadata["display_icon"])
		if hint == "" {
			continue
		}
		if _, ok := parseDisplayIconResource(hint); !ok {
			continue
		}
		pendingByHint[hint] = append(pendingByHint[hint], index)
	}
	if len(pendingByHint) == 0 {
		return nil, iconHashByKey
	}
	hints := make([]string, 0, len(pendingByHint))
	for hint := range pendingByHint {
		hints = append(hints, hint)
	}
	extracted := m.extractWindowsIconPayloads(ctx, hints)
	payloadByHash := map[string]IconPayload{}
	for hint, indexes := range pendingByHint {
		payload, ok := extracted[hint]
		if !ok || payload.DataBase64 == "" {
			continue
		}
		iconBytes, err := base64.StdEncoding.DecodeString(payload.DataBase64)
		if err != nil || len(iconBytes) == 0 {
			continue
		}
		sum := sha256.Sum256(iconBytes)
		iconHash := fmt.Sprintf("%x", sum[:])
		normalizedPayload := IconPayload{
			IconHash:   iconHash,
			MimeType:   fallbackText(payload.MimeType, "image/png"),
			DataBase64: base64.StdEncoding.EncodeToString(iconBytes),
		}
		payloadByHash[iconHash] = normalizedPayload
		for _, index := range indexes {
			if rows[index].Metadata == nil {
				rows[index].Metadata = map[string]any{}
			}
			rows[index].Metadata["icon_hash"] = iconHash
			iconHashByKey[softwareRowKey(rows[index])] = iconHash
		}
	}
	payloads := make([]IconPayload, 0, len(payloadByHash))
	for _, payload := range payloadByHash {
		payloads = append(payloads, payload)
	}
	sort.SliceStable(payloads, func(i, j int) bool {
		return payloads[i].IconHash < payloads[j].IconHash
	})
	return payloads, iconHashByKey
}

func (m *Manager) extractWindowsIconPayloads(ctx context.Context, hints []string) map[string]IconPayload {
	specs := []map[string]any{}
	seen := map[string]bool{}
	for _, hint := range hints {
		parsed, ok := parseDisplayIconResource(hint)
		if !ok || seen[parsed.Hint] {
			continue
		}
		seen[parsed.Hint] = true
		specs = append(specs, map[string]any{
			"hint":  parsed.Hint,
			"path":  parsed.FilePath,
			"index": parsed.IconIndex,
		})
	}
	out := map[string]IconPayload{}
	for start := 0; start < len(specs); start += iconExtractionBatchSize {
		end := start + iconExtractionBatchSize
		if end > len(specs) {
			end = len(specs)
		}
		for hint, payload := range m.extractWindowsIconPayloadBatch(ctx, specs[start:end]) {
			out[hint] = payload
		}
	}
	return out
}

type displayIconResource struct {
	Hint      string
	FilePath  string
	IconIndex int
}

func parseDisplayIconResource(value any) (displayIconResource, bool) {
	text := cleanText(value)
	if text == "" {
		return displayIconResource{}, false
	}
	matches := displayIconQuotedRe.FindStringSubmatch(text)
	if len(matches) > 0 {
		index := 0
		if len(matches) > 2 && cleanText(matches[2]) != "" {
			parsed, _ := strconv.Atoi(cleanText(matches[2]))
			index = parsed
		}
		return displayIconResource{Hint: text, FilePath: cleanText(matches[1]), IconIndex: index}, cleanText(matches[1]) != ""
	}
	matches = displayIconResourceRe.FindStringSubmatch(text)
	if len(matches) > 0 {
		index := 0
		if len(matches) > 2 && cleanText(matches[2]) != "" {
			parsed, _ := strconv.Atoi(cleanText(matches[2]))
			index = parsed
		}
		filePath := strings.TrimRight(cleanText(matches[1]), ",")
		return displayIconResource{Hint: text, FilePath: filePath, IconIndex: index}, filePath != ""
	}
	return displayIconResource{}, false
}

func (m *Manager) extractWindowsIconPayloadBatch(ctx context.Context, specs []map[string]any) map[string]IconPayload {
	if len(specs) == 0 {
		return nil
	}
	specBytes, err := json.Marshal(specs)
	if err != nil {
		return nil
	}
	script := windowsIconExtractionScript(base64.StdEncoding.EncodeToString(specBytes))
	result, err := m.runPowerShellScriptFile(ctx, script, time.Duration(30+len(specs)*2)*time.Second)
	if err != nil || result.ExitCode != 0 {
		return nil
	}
	var raw any
	if err := json.Unmarshal([]byte(result.Stdout), &raw); err != nil {
		return nil
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
	out := map[string]IconPayload{}
	for _, item := range items {
		hint := cleanText(item["hint"])
		data := cleanText(item["data_base64"])
		if hint == "" || data == "" {
			continue
		}
		out[hint] = IconPayload{
			MimeType:   strings.ToLower(fallbackText(cleanText(item["mime_type"]), "image/png")),
			DataBase64: data,
		}
	}
	return out
}

func (m *Manager) runPowerShellScriptFile(ctx context.Context, script string, timeout time.Duration) (commandResult, error) {
	root := filepath.Join(os.TempDir(), "Borealis", "software_management")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return commandResult{}, err
	}
	file, err := os.CreateTemp(root, "icon_extract_*.ps1")
	if err != nil {
		return commandResult{}, err
	}
	path := file.Name()
	if _, err := file.WriteString(script); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return commandResult{}, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return commandResult{}, err
	}
	defer func() {
		_ = os.Remove(path)
	}()
	return m.runner(ctx, timeout, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path)
}

func iconCandidateCount(rows []Software) int {
	count := 0
	for _, row := range rows {
		if row.Source != "local_installed" {
			continue
		}
		if _, ok := parseDisplayIconResource(row.Metadata["display_icon"]); ok {
			count++
		}
	}
	return count
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

func windowsIconExtractionScript(specsJSONB64 string) string {
	return `
$specsJson = [System.Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('` + specsJSONB64 + `'))
$specs = $specsJson | ConvertFrom-Json
Add-Type -AssemblyName System.Drawing
Add-Type @"
using System;
using System.Runtime.InteropServices;

public static class BorealisIconInterop {
  [DllImport("shell32.dll", CharSet = CharSet.Unicode)]
  public static extern uint ExtractIconEx(
    string szFileName,
    int nIconIndex,
    IntPtr[] phiconLarge,
    IntPtr[] phiconSmall,
    uint nIcons
  );

  [DllImport("user32.dll", SetLastError = true)]
  public static extern bool DestroyIcon(IntPtr hIcon);
}
"@
function Get-BorealisIconPayload {
  param(
    [string]$FileName,
    [int]$IconIndex
  )
  try {
    $expanded = [Environment]::ExpandEnvironmentVariables([string]$FileName)
    if (-not $expanded) { return $null }
    $expanded = $expanded.Trim().Trim('"')
    if (-not $expanded -or -not (Test-Path -LiteralPath $expanded)) { return $null }
    $extension = [System.IO.Path]::GetExtension($expanded).ToLowerInvariant()
    if ($extension -eq '.ico') {
      $rawBytes = [System.IO.File]::ReadAllBytes($expanded)
      if ($rawBytes -and $rawBytes.Length -gt 0) {
        return [PSCustomObject]@{
          mime_type = 'image/vnd.microsoft.icon'
          data_base64 = [Convert]::ToBase64String($rawBytes)
        }
      }
    }

    $icon = $null
    $ownedIcon = $null
    $largeIcons = $null
    $smallIcons = $null
    $iconHandle = [IntPtr]::Zero
    try {
      $largeIcons = New-Object IntPtr[] 1
      $smallIcons = New-Object IntPtr[] 1
      $extractedCount = [BorealisIconInterop]::ExtractIconEx($expanded, [int]$IconIndex, $largeIcons, $smallIcons, 1)
      if ($extractedCount -gt 0) {
        if ($largeIcons[0] -ne [IntPtr]::Zero) {
          $iconHandle = $largeIcons[0]
        } elseif ($smallIcons[0] -ne [IntPtr]::Zero) {
          $iconHandle = $smallIcons[0]
        }
      }
      if ($iconHandle -ne [IntPtr]::Zero) {
        $icon = [System.Drawing.Icon]::FromHandle($iconHandle)
        if ($icon -ne $null) {
          $ownedIcon = $icon.Clone()
          $icon.Dispose()
          $icon = $null
        }
      }
    } catch {
      $icon = $null
      $ownedIcon = $null
    }
    if ($null -eq $ownedIcon) {
      try {
        $ownedIcon = [System.Drawing.Icon]::ExtractAssociatedIcon($expanded)
      } catch {
        $ownedIcon = $null
      }
      if ($null -eq $ownedIcon) { return $null }
    }
    $bitmap = $null
    $stream = $null
    try {
      $bitmap = $ownedIcon.ToBitmap()
      if ($null -eq $bitmap) { return $null }
      $stream = New-Object System.IO.MemoryStream
      $bitmap.Save($stream, [System.Drawing.Imaging.ImageFormat]::Png)
      $pngBytes = $stream.ToArray()
      if (-not $pngBytes -or $pngBytes.Length -le 0) { return $null }
      return [PSCustomObject]@{
        mime_type = 'image/png'
        data_base64 = [Convert]::ToBase64String($pngBytes)
      }
    } finally {
      if ($stream) { $stream.Dispose() }
      if ($bitmap) { $bitmap.Dispose() }
      if ($ownedIcon) { $ownedIcon.Dispose() }
      if ($largeIcons -and $largeIcons.Length -gt 0 -and $largeIcons[0] -ne [IntPtr]::Zero) {
        [BorealisIconInterop]::DestroyIcon($largeIcons[0]) | Out-Null
      }
      if ($smallIcons -and $smallIcons.Length -gt 0 -and $smallIcons[0] -ne [IntPtr]::Zero) {
        [BorealisIconInterop]::DestroyIcon($smallIcons[0]) | Out-Null
      }
    }
  } catch {
    return $null
  }
}
$results = @()
foreach ($spec in ($specs | Where-Object { $_ -and $_.hint -and $_.path })) {
  try {
    $payload = Get-BorealisIconPayload -FileName ([string]$spec.path) -IconIndex ([int]$spec.index)
    if ($payload -and $payload.data_base64) {
      $results += [PSCustomObject]@{
        hint = [string]$spec.hint
        mime_type = [string]$payload.mime_type
        data_base64 = [string]$payload.data_base64
      }
    }
  } catch {}
}
$results | ConvertTo-Json -Depth 4 -Compress
`
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

func asBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes", "on":
			return true
		default:
			return false
		}
	case float64:
		return typed != 0
	case int:
		return typed != 0
	default:
		return false
	}
}

func cloneStringMap(value map[string]string) map[string]string {
	out := map[string]string{}
	for key, item := range value {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(item) != "" {
			out[key] = strings.TrimSpace(item)
		}
	}
	return out
}

func cloneMapSlice(value []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(value))
	for _, item := range value {
		if item == nil {
			continue
		}
		copied := map[string]any{}
		for key, raw := range item {
			copied[key] = raw
		}
		out = append(out, copied)
	}
	return out
}

func fallbackText(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}
