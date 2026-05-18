package patchmanagement

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bunny-lab-io/borealis/go-agent/internal/auth"
)

const (
	patchScanInterval   = 6 * time.Hour
	patchBoostInterval  = 15 * time.Second
	patchBoostWindow    = 90 * time.Second
	patchRequestTimeout = 90 * time.Minute
)

type Manager struct {
	authClient        *auth.Client
	hostname          string
	serviceMode       string
	logPath           string
	adapter           WUAAdapter
	httpClient        *http.Client
	wakeup            chan struct{}
	mu                sync.Mutex
	started           bool
	loopRunning       bool
	supported         bool
	unsupportedReason string
	lastError         string
	lastScanAt        int64
	lastReportAt      int64
	lastMissingCount  int
	lastFailedCount   int
	lastInstalledAt   int64
	lastHRESULT       string
	pendingReboot     bool
	policyVersion     string
	fastPollUntil     time.Time
}

type RoleHealth struct {
	Status     string
	StatusCode string
	Detail     string
	Details    map[string]any
}

type WUAAdapter interface {
	Scan(ctx context.Context) ([]Update, error)
	Install(ctx context.Context, updates []Update) (InstallSummary, error)
	PendingReboot(ctx context.Context) (bool, error)
	Reboot(ctx context.Context, delaySeconds int, comment string) error
}

type Update struct {
	UpdateID        string   `json:"update_id"`
	Revision        int      `json:"revision_number"`
	KBArticleIDs    []string `json:"kb_article_ids"`
	Title           string   `json:"title"`
	Description     string   `json:"description,omitempty"`
	UpdateType      string   `json:"update_type"`
	Classifications []string `json:"classifications"`
	Categories      []string `json:"categories"`
	CategoryIDs     []string `json:"category_ids,omitempty"`
	MsrcSeverity    string   `json:"msrc_severity,omitempty"`
	SupportURL      string   `json:"support_url,omitempty"`
	SizeBytes       int64    `json:"size_bytes"`
	IsInstalled     bool     `json:"is_installed"`
	IsDownloaded    bool     `json:"is_downloaded"`
	IsHidden        bool     `json:"is_hidden"`
	RequiresReboot  bool     `json:"requires_reboot"`
	ResultCode      string   `json:"result_code,omitempty"`
	HResult         string   `json:"hresult,omitempty"`
	Source          string   `json:"source"`
	Approved        bool     `json:"approved"`
	Held            bool     `json:"held"`
	HoldReason      string   `json:"hold_reason,omitempty"`
	PolicyClass     string   `json:"policy_class,omitempty"`
}

type InstallSummary struct {
	StartedAt      int64    `json:"started_at"`
	FinishedAt     int64    `json:"finished_at"`
	RebootRequired bool     `json:"reboot_required"`
	ResultCode     string   `json:"result_code"`
	HResult        string   `json:"hresult,omitempty"`
	Results        []Update `json:"results"`
}

type Policy struct {
	PolicyID     string          `json:"policy_id"`
	PolicyName   string          `json:"policy_name"`
	Version      string          `json:"version"`
	Enabled      bool            `json:"enabled"`
	ClassToggles map[string]bool `json:"class_toggles"`
	Holds        []Hold          `json:"holds"`
	Reboot       RebootPolicy    `json:"reboot"`
}

type Hold struct {
	Scope    string `json:"scope"`
	PolicyID string `json:"policy_id,omitempty"`
	UpdateID string `json:"update_id,omitempty"`
	KB       string `json:"kb,omitempty"`
	Title    string `json:"title,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type RebootPolicy struct {
	Mode                   string `json:"mode"`
	MaintenanceWindowStart string `json:"maintenance_window_start"`
	MaintenanceWindowEnd   string `json:"maintenance_window_end"`
	DeferralDeadlineHours  int    `json:"deferral_deadline_hours"`
	UserPrompt             bool   `json:"user_prompt"`
}

func New(authClient *auth.Client, hostname string, serviceMode string, configPath string) *Manager {
	supported, reason := detectSupport()
	baseDir := strings.TrimSpace(filepath.Dir(strings.TrimSpace(configPath)))
	if baseDir == "." || baseDir == "" {
		if exe, err := os.Executable(); err == nil {
			baseDir = filepath.Dir(exe)
		}
	}
	manager := &Manager{
		authClient:        authClient,
		hostname:          strings.TrimSpace(hostname),
		serviceMode:       auth.NormalizeServiceMode(serviceMode),
		logPath:           filepath.Join(baseDir, "Logs", "Agent", "patch_management.log"),
		httpClient:        &http.Client{Timeout: 45 * time.Second},
		wakeup:            make(chan struct{}, 1),
		supported:         supported,
		unsupportedReason: reason,
	}
	manager.adapter = defaultWUAAdapterWithLogger(manager.logf)
	return manager
}

func detectSupport() (bool, string) {
	if runtime.GOOS != "windows" {
		return false, "Windows patch management is supported only on Windows agents."
	}
	return true, ""
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
		m.logf("role unsupported service_mode=%s reason=%s", m.serviceMode, m.unsupportedReason)
		m.mu.Unlock()
		return
	}
	m.loopRunning = true
	m.logf("role starting service_mode=%s hostname=%s", m.serviceMode, m.hostname)
	m.mu.Unlock()
	go m.pollLoop(ctx)
}

func (m *Manager) Health() RoleHealth {
	m.mu.Lock()
	supported := m.supported
	unsupportedReason := m.unsupportedReason
	loopRunning := m.loopRunning
	lastError := m.lastError
	lastScanAt := m.lastScanAt
	lastReportAt := m.lastReportAt
	lastMissingCount := m.lastMissingCount
	lastFailedCount := m.lastFailedCount
	lastInstalledAt := m.lastInstalledAt
	lastHRESULT := m.lastHRESULT
	pendingReboot := m.pendingReboot
	policyVersion := m.policyVersion
	m.mu.Unlock()
	details := map[string]any{
		"running_status":    "Running",
		"last_scan_at":      strconv.FormatInt(lastScanAt, 10),
		"last_report_at":    strconv.FormatInt(lastReportAt, 10),
		"missing_count":     strconv.Itoa(lastMissingCount),
		"failed_count":      strconv.Itoa(lastFailedCount),
		"last_installed_at": strconv.FormatInt(lastInstalledAt, 10),
		"pending_reboot":    strconv.FormatBool(pendingReboot),
		"last_hresult":      lastHRESULT,
		"policy_version":    policyVersion,
		"scan_interval_ms":  strconv.Itoa(int(patchScanInterval / time.Millisecond)),
		"runtime":           "go",
	}
	if !supported {
		details["running_status"] = "Unsupported"
		return RoleHealth{
			Status:     "unsupported",
			StatusCode: "unsupported",
			Detail:     fallbackText(unsupportedReason, "Patch management is unsupported on this platform."),
			Details:    details,
		}
	}
	if !loopRunning {
		details["running_status"] = "Stopped"
		return RoleHealth{
			Status:     "pending",
			StatusCode: "pending",
			Detail:     "Waiting for patch-management scan loop.",
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
	if pendingReboot {
		return RoleHealth{
			Status:     "degraded",
			StatusCode: "degraded",
			Detail:     "Windows update reboot is pending.",
			Details:    details,
		}
	}
	if lastScanAt <= 0 {
		return RoleHealth{
			Status:     "pending",
			StatusCode: "pending",
			Detail:     "Waiting for initial Windows patch scan.",
			Details:    details,
		}
	}
	return RoleHealth{
		Status:     "healthy",
		StatusCode: "healthy",
		Detail:     "Windows patch-management scan loop active.",
		Details:    details,
	}
}

func (m *Manager) HandleRequest(ctx context.Context, payload any) (any, error) {
	body, ok := payload.(map[string]any)
	if !ok {
		return errorResponse("invalid_request", "Patch-management payload must be an object."), nil
	}
	if !m.matchesTarget(body) {
		m.logf("request ignored target mismatch payload=%s", summarizePayload(body))
		return errorResponse("not_for_host", "Patch-management request targeted another device."), nil
	}
	if !m.supported {
		m.logf("request rejected unsupported payload=%s", summarizePayload(body))
		return errorResponse("unsupported_platform", fallbackText(m.unsupportedReason, "Patch management is unsupported on this platform.")), nil
	}
	action := normalizeAction(body["action"])
	if action == "" {
		m.logf("request rejected invalid_action payload=%s", summarizePayload(body))
		return errorResponse("invalid_action", "Patch-management action must be scan, install, policy_refresh, or reboot."), nil
	}
	requestedBy := cleanText(body["requested_by"])
	requestID := cleanText(body["request_id"])
	m.logf("request accepted action=%s request_id=%s requested_by=%s payload=%s", action, requestID, requestedBy, summarizePayload(body))
	switch action {
	case "scan", "policy_refresh":
		m.RequestScan("operator:" + fallbackText(requestedBy, "unknown"))
	case "install":
		updateIDs := cleanStringSlice(firstValue(body, "update_ids", "updates"))
		kbs := cleanStringSlice(firstValue(body, "kb_article_ids", "kbs"))
		go m.runInstall(context.Background(), requestID, requestedBy, updateIDs, kbs)
	case "reboot":
		delaySeconds := asInt(firstValue(body, "delay_seconds", "delay"))
		if delaySeconds < 0 {
			delaySeconds = 0
		}
		go m.runReboot(context.Background(), requestID, requestedBy, delaySeconds)
	}
	return map[string]any{
		"ok":         true,
		"status":     "accepted",
		"action":     action,
		"request_id": requestID,
	}, nil
}

func (m *Manager) RequestScan(reason string) {
	if m == nil {
		return
	}
	m.logf("scan requested reason=%s", cleanText(reason))
	m.mu.Lock()
	m.fastPollUntil = time.Now().Add(patchBoostWindow)
	loopRunning := m.loopRunning
	m.mu.Unlock()
	select {
	case m.wakeup <- struct{}{}:
	default:
	}
	if !loopRunning {
		go func() {
			_ = m.ScanAndReport(context.Background(), reason)
		}()
	}
}

func (m *Manager) ScanAndReport(ctx context.Context, reason string) error {
	if m == nil {
		return nil
	}
	if !m.supported {
		m.logf("scan skipped unsupported reason=%s", fallbackText(m.unsupportedReason, "patch management unsupported"))
		return fmt.Errorf("%s", fallbackText(m.unsupportedReason, "patch management unsupported"))
	}
	m.logf("scan start reason=%s", cleanText(reason))
	policy, err := m.fetchPolicy(ctx)
	if err != nil {
		m.logf("scan policy_fetch_failed error=%v", err)
		m.recordError("Patch policy fetch failed: " + err.Error())
		return err
	}
	m.logf("scan policy policy_id=%s policy_name=%q version=%s enabled=%t holds=%d", policy.PolicyID, policy.PolicyName, policy.Version, policy.Enabled, len(policy.Holds))
	startedAt := time.Now().Unix()
	m.logf("scan wua_search_start")
	updates, err := m.adapter.Scan(ctx)
	if err != nil {
		m.logf("scan wua_search_failed error=%v", err)
		m.recordError("Patch scan failed: " + err.Error())
		_ = m.publishReport(ctx, policy, startedAt, time.Now().Unix(), reason, nil, nil, err)
		return err
	}
	m.logf("scan wua_search_complete updates=%d", len(updates))
	pendingReboot, rebootErr := m.adapter.PendingReboot(ctx)
	if rebootErr != nil {
		m.logf("scan pending_reboot_check_failed error=%v", rebootErr)
		pendingReboot = false
	}
	annotated := applyPolicy(policy, updates)
	m.logf("scan policy_applied total=%d approved=%d held=%d missing=%d failed=%d pending_reboot=%t", len(annotated), countApproved(annotated), countHeld(annotated), countMissing(annotated), countFailed(annotated), pendingReboot)
	reportErr := m.publishReport(ctx, policy, startedAt, time.Now().Unix(), reason, annotated, nil, nil)
	if reportErr != nil {
		m.logf("scan report_publish_failed error=%v", reportErr)
		m.recordError("Patch report publish failed: " + reportErr.Error())
		return reportErr
	}
	m.mu.Lock()
	m.lastError = ""
	completedScanAt := time.Now().Unix()
	m.lastScanAt = completedScanAt
	m.lastReportAt = completedScanAt
	m.lastMissingCount = countMissing(annotated)
	m.lastFailedCount = countFailed(annotated)
	m.pendingReboot = pendingReboot
	m.policyVersion = policy.Version
	m.mu.Unlock()
	m.logf("scan complete report_uploaded=true last_scan_at=%d", completedScanAt)
	return nil
}

func (m *Manager) runInstall(ctx context.Context, requestID string, requestedBy string, updateIDs []string, kbs []string) {
	runCtx, cancel := context.WithTimeout(ctx, patchRequestTimeout)
	defer cancel()
	if err := m.installAndReport(runCtx, requestID, requestedBy, updateIDs, kbs); err != nil {
		m.recordError("Patch install failed: " + err.Error())
	}
}

func (m *Manager) installAndReport(ctx context.Context, requestID string, requestedBy string, updateIDs []string, kbs []string) error {
	policy, err := m.fetchPolicy(ctx)
	if err != nil {
		m.logf("install policy_fetch_failed request_id=%s error=%v", requestID, err)
		return err
	}
	startedAt := time.Now().Unix()
	m.logf("install scan_start request_id=%s requested_by=%s update_ids=%d kbs=%d", requestID, requestedBy, len(updateIDs), len(kbs))
	updates, err := m.adapter.Scan(ctx)
	if err != nil {
		m.logf("install scan_failed request_id=%s error=%v", requestID, err)
		_ = m.publishReport(ctx, policy, startedAt, time.Now().Unix(), "install_scan_failed", nil, nil, err)
		return err
	}
	annotated := applyPolicy(policy, updates)
	selected := selectInstallTargets(annotated, updateIDs, kbs)
	m.logf("install targets_selected request_id=%s scanned=%d selected=%d approved=%d held=%d", requestID, len(updates), len(selected), countApproved(annotated), countHeld(annotated))
	if len(selected) == 0 {
		m.logf("install no_updates request_id=%s", requestID)
		_ = m.publishReport(ctx, policy, startedAt, time.Now().Unix(), "install_no_updates", annotated, &InstallSummary{
			StartedAt:  startedAt,
			FinishedAt: time.Now().Unix(),
			ResultCode: "no_approved_updates",
			Results:    []Update{},
		}, nil)
		return nil
	}
	summary, installErr := m.adapter.Install(ctx, selected)
	pendingReboot, _ := m.adapter.PendingReboot(ctx)
	if summary.StartedAt <= 0 {
		summary.StartedAt = startedAt
	}
	if summary.FinishedAt <= 0 {
		summary.FinishedAt = time.Now().Unix()
	}
	if summary.RebootRequired {
		pendingReboot = true
	}
	reportErr := m.publishReport(ctx, policy, startedAt, time.Now().Unix(), "install", annotated, &summary, installErr)
	m.logf("install complete request_id=%s result=%s hresult=%s results=%d reboot_required=%t pending_reboot=%t install_error=%v report_error=%v", requestID, summary.ResultCode, summary.HResult, len(summary.Results), summary.RebootRequired, pendingReboot, installErr, reportErr)
	m.mu.Lock()
	m.lastInstalledAt = time.Now().Unix()
	m.pendingReboot = pendingReboot
	m.policyVersion = policy.Version
	m.lastHRESULT = summary.HResult
	m.mu.Unlock()
	if installErr != nil {
		return installErr
	}
	return reportErr
}

func (m *Manager) runReboot(ctx context.Context, requestID string, requestedBy string, delaySeconds int) {
	comment := "Borealis Windows patch management reboot"
	if requestedBy != "" {
		comment += " requested by " + requestedBy
	}
	m.logf("reboot requested request_id=%s requested_by=%s delay_seconds=%d", requestID, requestedBy, delaySeconds)
	if err := m.adapter.Reboot(ctx, delaySeconds, comment); err != nil {
		m.logf("reboot failed request_id=%s error=%v", requestID, err)
		m.recordError("Patch reboot request failed: " + err.Error())
		return
	}
	m.logf("reboot command_queued request_id=%s", requestID)
}

func (m *Manager) pollLoop(ctx context.Context) {
	defer func() {
		m.mu.Lock()
		m.loopRunning = false
		m.mu.Unlock()
	}()
	for {
		if err := m.ScanAndReport(ctx, "scheduled_scan"); err != nil {
			m.recordError("Patch scan failed: " + err.Error())
		}
		waitFor := patchScanInterval
		m.mu.Lock()
		if time.Now().Before(m.fastPollUntil) {
			waitFor = patchBoostInterval
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

func (m *Manager) fetchPolicy(ctx context.Context) (Policy, error) {
	if m.authClient == nil {
		m.logf("policy default reason=no_auth_client")
		return defaultPolicy(), nil
	}
	payload := map[string]any{
		"hostname":     m.hostname,
		"agent_id":     m.authClient.AgentID(),
		"service_mode": m.serviceMode,
	}
	var response Policy
	if _, err := m.authClient.PostJSON(ctx, "/api/agent/patch-management/policy", payload, &response); err != nil {
		m.logf("policy fetch_failed error=%v", err)
		return defaultPolicy(), err
	}
	if strings.TrimSpace(response.PolicyID) == "" {
		m.logf("policy empty_response using_default")
		response = defaultPolicy()
	}
	response.normalize()
	m.logf("policy fetched policy_id=%s name=%q version=%s enabled=%t holds=%d", response.PolicyID, response.PolicyName, response.Version, response.Enabled, len(response.Holds))
	return response, nil
}

func (m *Manager) publishReport(ctx context.Context, policy Policy, startedAt int64, completedAt int64, reason string, updates []Update, installSummary *InstallSummary, reportErr error) error {
	if m.authClient == nil {
		return nil
	}
	payload := map[string]any{
		"hostname":          m.hostname,
		"agent_id":          m.authClient.AgentID(),
		"service_mode":      m.serviceMode,
		"policy_id":         policy.PolicyID,
		"policy_name":       policy.PolicyName,
		"policy_version":    policy.Version,
		"scan_started_at":   startedAt,
		"scan_completed_at": completedAt,
		"reason":            cleanText(reason),
		"updates":           updates,
	}
	if installSummary != nil {
		payload["install"] = installSummary
	}
	if reportErr != nil {
		payload["error"] = reportErr.Error()
	}
	var response map[string]any
	_, err := m.authClient.PostJSON(ctx, "/api/agent/patch-management/report", payload, &response)
	if err != nil {
		m.logf("report post_failed reason=%s updates=%d install=%t error=%v", cleanText(reason), len(updates), installSummary != nil, err)
	} else {
		m.logf("report posted reason=%s updates=%d install=%t", cleanText(reason), len(updates), installSummary != nil)
	}
	return err
}

func (m *Manager) matchesTarget(payload map[string]any) bool {
	targetHost := strings.ToLower(cleanText(firstValue(payload, "target_hostname", "hostname")))
	if targetHost != "" && targetHost != strings.ToLower(strings.TrimSpace(m.hostname)) {
		return false
	}
	targetAgent := cleanText(payload["agent_id"])
	if targetAgent != "" && m.authClient != nil && targetAgent != m.authClient.AgentID() {
		return false
	}
	return true
}

func (m *Manager) recordError(message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastError = strings.TrimSpace(message)
	m.logf("error recorded message=%s", m.lastError)
}

func (m *Manager) logf(format string, args ...any) {
	if m == nil || strings.TrimSpace(m.logPath) == "" {
		return
	}
	message := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format("2006-01-02T15:04:05")
	line := fmt.Sprintf("[%s] [patch-management] %s\n", timestamp, message)
	if err := os.MkdirAll(filepath.Dir(m.logPath), 0o755); err != nil {
		return
	}
	file, err := os.OpenFile(m.logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.WriteString(line)
}

func defaultPolicy() Policy {
	policy := Policy{
		PolicyID:   "default",
		PolicyName: "Borealis Default",
		Version:    "1",
		Enabled:    true,
		ClassToggles: map[string]bool{
			"security":      true,
			"critical":      true,
			"cumulative":    true,
			"definition":    true,
			"driver":        true,
			"feature":       true,
			"optional":      true,
			"service_pack":  true,
			"update_rollup": true,
			"updates":       true,
		},
		Reboot: RebootPolicy{
			Mode:                   "maintenance_window",
			MaintenanceWindowStart: "22:00",
			MaintenanceWindowEnd:   "05:00",
			DeferralDeadlineHours:  72,
			UserPrompt:             true,
		},
	}
	return policy
}

func (p *Policy) normalize() {
	if strings.TrimSpace(p.PolicyID) == "" {
		p.PolicyID = "default"
	}
	if strings.TrimSpace(p.PolicyName) == "" {
		p.PolicyName = "Borealis Default"
	}
	if strings.TrimSpace(p.Version) == "" {
		p.Version = "1"
	}
	if p.ClassToggles == nil {
		p.ClassToggles = defaultPolicy().ClassToggles
	}
	for key, value := range defaultPolicy().ClassToggles {
		if _, found := p.ClassToggles[key]; !found {
			p.ClassToggles[key] = value
		}
	}
	if strings.TrimSpace(p.Reboot.Mode) == "" {
		p.Reboot = defaultPolicy().Reboot
	}
}

func applyPolicy(policy Policy, updates []Update) []Update {
	policy.normalize()
	out := make([]Update, 0, len(updates))
	for _, update := range updates {
		next := update
		next.PolicyClass = classifyUpdate(update)
		next.Approved = policy.Enabled && policy.ClassToggles[next.PolicyClass]
		if !next.Approved && next.PolicyClass == "updates" {
			next.Approved = policy.Enabled && policy.ClassToggles["updates"]
		}
		if held, reason := updateHeld(policy, next); held {
			next.Held = true
			next.Approved = false
			next.HoldReason = reason
		}
		out = append(out, next)
	}
	return out
}

func updateHeld(policy Policy, update Update) (bool, string) {
	updateID := strings.ToLower(strings.TrimSpace(update.UpdateID))
	title := strings.ToLower(strings.TrimSpace(update.Title))
	kbs := map[string]bool{}
	for _, kb := range update.KBArticleIDs {
		kbs[strings.ToLower(strings.TrimSpace(kb))] = true
		kbs[strings.ToLower(strings.TrimPrefix(strings.TrimSpace(kb), "KB"))] = true
	}
	for _, hold := range policy.Holds {
		if hold.PolicyID != "" && policy.PolicyID != "" && !strings.EqualFold(hold.PolicyID, policy.PolicyID) {
			continue
		}
		if hold.UpdateID != "" && strings.EqualFold(hold.UpdateID, updateID) {
			return true, fallbackText(hold.Reason, "Update held.")
		}
		if hold.KB != "" {
			cleanKB := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(hold.KB), "KB"))
			if kbs[cleanKB] {
				return true, fallbackText(hold.Reason, "KB held.")
			}
		}
		if hold.Title != "" && title != "" && strings.Contains(title, strings.ToLower(strings.TrimSpace(hold.Title))) {
			return true, fallbackText(hold.Reason, "Title held.")
		}
	}
	return false, ""
}

func classifyUpdate(update Update) string {
	candidates := append([]string{}, update.Classifications...)
	candidates = append(candidates, update.Categories...)
	candidates = append(candidates, update.UpdateType, update.Title)
	joined := strings.ToLower(strings.Join(candidates, " "))
	switch {
	case strings.Contains(joined, "driver"):
		return "driver"
	case strings.Contains(joined, "feature"):
		return "feature"
	case strings.Contains(joined, "definition"):
		return "definition"
	case strings.Contains(joined, "security"):
		return "security"
	case strings.Contains(joined, "critical"):
		return "critical"
	case strings.Contains(joined, "cumulative"):
		return "cumulative"
	case strings.Contains(joined, "service pack"):
		return "service_pack"
	case strings.Contains(joined, "rollup"):
		return "update_rollup"
	case strings.Contains(joined, "optional") || strings.Contains(joined, "preview"):
		return "optional"
	default:
		return "updates"
	}
}

func selectInstallTargets(updates []Update, updateIDs []string, kbs []string) []Update {
	idSet := make(map[string]bool)
	for _, id := range updateIDs {
		idSet[strings.ToLower(strings.TrimSpace(id))] = true
	}
	kbSet := make(map[string]bool)
	for _, kb := range kbs {
		clean := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(kb), "KB"))
		if clean != "" {
			kbSet[clean] = true
		}
	}
	selected := make([]Update, 0, len(updates))
	for _, update := range updates {
		if update.IsInstalled || update.IsHidden || !update.Approved || update.Held {
			continue
		}
		if len(idSet) == 0 && len(kbSet) == 0 {
			selected = append(selected, update)
			continue
		}
		if idSet[strings.ToLower(update.UpdateID)] {
			selected = append(selected, update)
			continue
		}
		for _, kb := range update.KBArticleIDs {
			if kbSet[strings.ToLower(strings.TrimPrefix(strings.TrimSpace(kb), "KB"))] {
				selected = append(selected, update)
				break
			}
		}
	}
	return selected
}

func countMissing(updates []Update) int {
	count := 0
	for _, update := range updates {
		if !update.IsInstalled && !update.IsHidden {
			count++
		}
	}
	return count
}

func countApproved(updates []Update) int {
	count := 0
	for _, update := range updates {
		if update.Approved {
			count++
		}
	}
	return count
}

func countHeld(updates []Update) int {
	count := 0
	for _, update := range updates {
		if update.Held {
			count++
		}
	}
	return count
}

func countFailed(updates []Update) int {
	count := 0
	for _, update := range updates {
		if strings.EqualFold(update.ResultCode, "failed") || strings.TrimSpace(update.HResult) != "" {
			count++
		}
	}
	return count
}

func normalizeAction(value any) string {
	switch strings.ToLower(strings.TrimSpace(fmt.Sprint(value))) {
	case "scan", "scan_now":
		return "scan"
	case "install", "install_now":
		return "install"
	case "policy", "policy_refresh", "refresh_policy":
		return "policy_refresh"
	case "reboot", "reboot_now":
		return "reboot"
	default:
		return ""
	}
}

func cleanStringSlice(value any) []string {
	out := []string{}
	switch typed := value.(type) {
	case []string:
		out = append(out, typed...)
	case []any:
		for _, item := range typed {
			out = append(out, fmt.Sprint(item))
		}
	case string:
		for _, part := range strings.Split(typed, ",") {
			out = append(out, part)
		}
	}
	cleaned := make([]string, 0, len(out))
	seen := map[string]bool{}
	for _, item := range out {
		text := strings.TrimSpace(item)
		key := strings.ToLower(text)
		if text == "" || seen[key] {
			continue
		}
		seen[key] = true
		cleaned = append(cleaned, text)
	}
	sort.Strings(cleaned)
	return cleaned
}

func firstValue(payload map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := payload[key]; ok {
			return value
		}
	}
	return nil
}

func cleanText(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func fallbackText(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func summarizePayload(payload map[string]any) string {
	if len(payload) == 0 {
		return "{}"
	}
	summary := map[string]any{}
	for _, key := range []string{"request_id", "action", "hostname", "target_hostname", "agent_id", "requested_by", "delay_seconds"} {
		if value, ok := payload[key]; ok {
			summary[key] = value
		}
	}
	if value := cleanStringSlice(firstValue(payload, "update_ids", "updates")); len(value) > 0 {
		summary["update_ids_count"] = len(value)
	}
	if value := cleanStringSlice(firstValue(payload, "kb_article_ids", "kbs")); len(value) > 0 {
		summary["kb_count"] = len(value)
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func asInt(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		parsed, _ := v.Int64()
		return int(parsed)
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(v))
		return parsed
	default:
		return 0
	}
}

func errorResponse(code string, message string) map[string]any {
	return map[string]any{
		"ok":      false,
		"error":   code,
		"message": message,
	}
}

func runShutdown(ctx context.Context, delaySeconds int, comment string) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("reboot is supported only on Windows agents")
	}
	args := []string{"/r", "/t", strconv.Itoa(delaySeconds)}
	if strings.TrimSpace(comment) != "" {
		args = append(args, "/c", comment)
	}
	cmd := exec.CommandContext(ctx, "shutdown.exe", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
