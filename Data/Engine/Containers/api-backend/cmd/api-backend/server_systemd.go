package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	systemdCommandTimeout       = 8 * time.Second
	systemdServiceRestartDelay  = 2 * time.Second
	systemdPendingActionTTL     = 60
	systemdRestartJobUnitPrefix = "borealis-admin-restart"
)

var (
	systemdPostgresqlInstancePattern = regexp.MustCompile(`(?i)^postgresql@(.+)\.service$`)
	systemdLookPath                  = exec.LookPath
	systemdRunCommand                = runSystemdCommand
	systemdPendingMu                 sync.Mutex
	systemdPendingActions            = map[string]pendingSystemdAction{}
)

var systemdShowProperties = []string{
	"LoadState",
	"ActiveState",
	"SubState",
	"UnitFileState",
	"MainPID",
	"ExecMainStartTimestampUSec",
	"ActiveEnterTimestampUSec",
	"ExecMainStartTimestamp",
	"ActiveEnterTimestamp",
	"FragmentPath",
}

type systemCommandResult struct {
	Code   int
	Stdout string
	Stderr string
}

type pendingSystemdAction struct {
	Action    string
	CreatedAt int64
	ExpiresAt int64
	UnitName  string
	Instance  string
}

func collectSystemdServiceRows() []map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), systemdCommandTimeout)
	defer cancel()
	systemctlBin, _ := systemdLookPath("systemctl")
	systemdRunBin, _ := systemdLookPath("systemd-run")
	rows := []map[string]any{}
	for _, spec := range []struct {
		key  string
		name string
		unit string
	}{
		{"borealis_engine", "Borealis Engine", "borealis-engine.service"},
		{"borealis_traefik", "Traefik", "borealis-traefik.service"},
	} {
		show := systemdShow(ctx, systemctlBin, spec.unit)
		rows = append(rows, systemdServiceRow(spec.key, spec.name, spec.unit, "", show, systemdServiceRestartSupported(systemctlBin, systemdRunBin, show)))
	}
	for _, unitName := range discoverPostgresqlClusterUnits(ctx, systemctlBin) {
		instance := postgresqlInstanceFromUnit(unitName)
		show := systemdShow(ctx, systemctlBin, unitName)
		label := "PostgreSQL"
		if instance != "" {
			label = "PostgreSQL " + instance
		}
		rows = append(rows, systemdServiceRow("postgresql_cluster", label, unitName, instance, show, systemdServiceRestartSupported(systemctlBin, systemdRunBin, show)))
	}
	return rows
}

func handleSystemdServiceRestart(w http.ResponseWriter, r *http.Request, serviceKey string) {
	body, err := readJSONMap(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json", "message": "Request body must be valid JSON."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), systemdCommandTimeout)
	defer cancel()
	unitName, instance := resolveSystemdRestartUnit(ctx, serviceKey, body)
	if unitName == "" {
		if serviceKey == "postgresql_cluster" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_postgresql_instance", "message": "A valid PostgreSQL cluster instance is required."})
			return
		}
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "service_unavailable", "message": "The requested service unit could not be resolved."})
		return
	}
	systemctlBin, _ := systemdLookPath("systemctl")
	systemdRunBin, _ := systemdLookPath("systemd-run")
	show := systemdShow(ctx, systemctlBin, unitName)
	if !systemdServiceRestartSupported(systemctlBin, systemdRunBin, show) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "restart_unsupported", "message": "This service cannot be restarted safely on the current engine host."})
		return
	}
	jobUnit, err := queueDetachedSystemdRestart(ctx, serviceKey, unitName, systemdRunBin, systemctlBin)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "restart_failed", "message": err.Error()})
		return
	}
	markSystemdActionPending(serviceKey, unitName, "restart", instance)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"queued":        true,
		"service_key":   serviceKey,
		"unit_name":     unitName,
		"job_unit":      jobUnit,
		"scheduled_for": time.Now().UTC().Add(systemdServiceRestartDelay).Format(time.RFC3339Nano),
	})
}

func resolveSystemdRestartUnit(ctx context.Context, serviceKey string, body map[string]any) (string, string) {
	switch strings.ToLower(cleanText(serviceKey)) {
	case "borealis_engine":
		return "borealis-engine.service", ""
	case "borealis_traefik":
		return "borealis-traefik.service", ""
	case "postgresql_cluster":
		instance := cleanText(body["instance"])
		if instance == "" {
			return "", ""
		}
		systemctlBin, _ := systemdLookPath("systemctl")
		for _, unitName := range discoverPostgresqlClusterUnits(ctx, systemctlBin) {
			if strings.EqualFold(postgresqlInstanceFromUnit(unitName), instance) {
				return unitName, instance
			}
		}
		return "", instance
	default:
		return "", ""
	}
}

func knownSystemdRestartService(serviceKey string) bool {
	switch strings.ToLower(cleanText(serviceKey)) {
	case "borealis_engine", "borealis_traefik", "postgresql_cluster":
		return true
	default:
		return false
	}
}

func queueDetachedSystemdRestart(ctx context.Context, serviceKey string, unitName string, systemdRunBin string, systemctlBin string) (string, error) {
	if strings.TrimSpace(systemdRunBin) == "" || strings.TrimSpace(systemctlBin) == "" {
		return "", fmt.Errorf("systemd-run or systemctl is unavailable on this engine host")
	}
	jobUnit := fmt.Sprintf("%s-%s-%s", systemdRestartJobUnitPrefix, strings.ToLower(cleanText(serviceKey)), strconv.FormatInt(time.Now().UnixNano(), 36))
	shellCommand := fmt.Sprintf("sleep %d; %s restart %s", int(systemdServiceRestartDelay/time.Second), shellQuote(systemctlBin), shellQuote(unitName))
	result := systemdRunCommand(ctx, []string{
		systemdRunBin,
		"--unit=" + jobUnit,
		"--collect",
		"--service-type=oneshot",
		"/bin/bash",
		"-lc",
		shellCommand,
	})
	if result.Code != 0 {
		return "", fmt.Errorf("%s", firstText(cleanText(result.Stderr), cleanText(result.Stdout), "unable to queue restart"))
	}
	return jobUnit, nil
}

func systemdShow(ctx context.Context, systemctlBin string, unitName string) map[string]string {
	if strings.TrimSpace(systemctlBin) == "" || strings.TrimSpace(unitName) == "" {
		return map[string]string{}
	}
	result := systemdRunCommand(ctx, []string{
		systemctlBin,
		"show",
		unitName,
		"--no-pager",
		"--property",
		strings.Join(systemdShowProperties, ","),
	})
	if result.Code != 0 && strings.TrimSpace(result.Stdout) == "" {
		return map[string]string{}
	}
	payload := map[string]string{}
	for _, raw := range strings.Split(result.Stdout, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || !strings.Contains(line, "=") {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		payload[key] = value
	}
	return payload
}

func discoverPostgresqlClusterUnits(ctx context.Context, systemctlBin string) []string {
	if strings.TrimSpace(systemctlBin) == "" {
		return []string{}
	}
	seen := map[string]struct{}{}
	discovered := []string{}
	for _, args := range [][]string{
		{systemctlBin, "list-unit-files", "postgresql@*.service", "--no-legend", "--no-pager"},
		{systemctlBin, "list-units", "postgresql@*.service", "--all", "--no-legend", "--no-pager"},
	} {
		result := systemdRunCommand(ctx, args)
		for _, unitName := range parseSystemdListUnits(result.Stdout) {
			if postgresqlInstanceFromUnit(unitName) == "" {
				continue
			}
			if _, ok := seen[unitName]; ok {
				continue
			}
			seen[unitName] = struct{}{}
			discovered = append(discovered, unitName)
		}
	}
	return discovered
}

func parseSystemdListUnits(output string) []string {
	units := []string{}
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] != "" {
			units = append(units, fields[0])
		}
	}
	return units
}

func postgresqlInstanceFromUnit(unitName string) string {
	match := systemdPostgresqlInstancePattern.FindStringSubmatch(strings.TrimSpace(unitName))
	if len(match) != 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func systemdServiceRestartSupported(systemctlBin string, systemdRunBin string, showPayload map[string]string) bool {
	if strings.TrimSpace(systemctlBin) == "" || strings.TrimSpace(systemdRunBin) == "" {
		return false
	}
	loadState := strings.ToLower(strings.TrimSpace(showPayload["LoadState"]))
	return loadState != "" && loadState != "not-found" && loadState != "error" && loadState != "bad-setting"
}

func systemdServiceRow(serviceKey string, label string, unitName string, instance string, show map[string]string, restartSupported bool) map[string]any {
	activeState := strings.ToLower(firstText(cleanText(show["ActiveState"]), "unknown"))
	subState := strings.ToLower(firstText(cleanText(show["SubState"]), "unknown"))
	return map[string]any{
		"key":               serviceKey,
		"label":             label,
		"instance":          nullableStringValue(instance),
		"unit_name":         unitName,
		"runtime":           "systemd",
		"active_state":      activeState,
		"sub_state":         subState,
		"enabled_state":     strings.ToLower(firstText(cleanText(show["UnitFileState"]), "unknown")),
		"main_pid":          parseIntDefault(show["MainPID"], 0),
		"started_at":        nullableStringValue(firstText(systemdTimestampUSec(show["ExecMainStartTimestampUSec"]), systemdTimestampUSec(show["ActiveEnterTimestampUSec"]), systemdTimestampValue(show["ExecMainStartTimestamp"]), systemdTimestampValue(show["ActiveEnterTimestamp"]))),
		"fragment_path":     nullableStringValue(show["FragmentPath"]),
		"restart_supported": restartSupported,
		"pending_action":    systemdPendingAction(serviceKey, instance),
		"actions":           []map[string]string{{"id": "restart", "label": "Restart", "action": "restart"}},
		"display_status":    titleCaseAPI(firstText(subState, activeState)),
		"status":            normalizeSystemdServiceStatus(show),
	}
}

func normalizeSystemdServiceStatus(show map[string]string) string {
	loadState := strings.ToLower(strings.TrimSpace(show["LoadState"]))
	activeState := strings.ToLower(strings.TrimSpace(show["ActiveState"]))
	if loadState == "not-found" || loadState == "error" || loadState == "bad-setting" {
		return "critical"
	}
	switch activeState {
	case "active":
		return "healthy"
	case "activating", "reloading", "deactivating":
		return "warning"
	case "failed", "inactive":
		return "critical"
	default:
		return "unknown"
	}
}

func systemdTimestampUSec(value string) string {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed <= 0 {
		return ""
	}
	return time.Unix(0, parsed*1000).UTC().Format(time.RFC3339Nano)
}

func systemdTimestampValue(value string) string {
	text := cleanText(value)
	if text == "" || strings.EqualFold(text, "n/a") || text == "0" {
		return ""
	}
	return text
}

func markSystemdActionPending(serviceKey string, unitName string, action string, instance string) {
	key := systemdPendingKey(serviceKey, instance)
	if key == "" || cleanText(unitName) == "" || cleanText(action) == "" {
		return
	}
	now := time.Now().Unix()
	systemdPendingMu.Lock()
	defer systemdPendingMu.Unlock()
	systemdPendingActions[key] = pendingSystemdAction{
		Action:    strings.ToLower(cleanText(action)),
		CreatedAt: now,
		ExpiresAt: now + systemdPendingActionTTL,
		UnitName:  unitName,
		Instance:  strings.ToLower(cleanText(instance)),
	}
}

func systemdPendingAction(serviceKey string, instance string) any {
	key := systemdPendingKey(serviceKey, instance)
	if key == "" {
		return nil
	}
	now := time.Now().Unix()
	systemdPendingMu.Lock()
	defer systemdPendingMu.Unlock()
	action, ok := systemdPendingActions[key]
	if !ok {
		return nil
	}
	if action.ExpiresAt <= now {
		delete(systemdPendingActions, key)
		return nil
	}
	return map[string]any{
		"action":     action.Action,
		"created_at": action.CreatedAt,
		"expires_at": action.ExpiresAt,
		"unit_name":  action.UnitName,
		"instance":   nullableStringValue(action.Instance),
	}
}

func systemdPendingKey(serviceKey string, instance string) string {
	key := strings.ToLower(cleanText(serviceKey))
	if key == "" {
		return ""
	}
	return key + "\x00" + strings.ToLower(cleanText(instance))
}

func runSystemdCommand(ctx context.Context, args []string) systemCommandResult {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return systemCommandResult{Code: 1, Stderr: "empty command"}
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		code = 1
		var exitErr *exec.ExitError
		if ok := errors.As(err, &exitErr); ok {
			code = exitErr.ExitCode()
		}
		if stderr.Len() == 0 {
			stderr.WriteString(err.Error())
		}
	}
	return systemCommandResult{Code: code, Stdout: stdout.String(), Stderr: stderr.String()}
}

func shellQuote(value string) string {
	text := strings.ReplaceAll(value, `'`, `'\''`)
	return "'" + text + "'"
}
