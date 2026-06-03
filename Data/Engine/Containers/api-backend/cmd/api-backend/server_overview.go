package main

import (
	"bufio"
	"context"
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const wireguardInterfaceName = "borealis-wg"

var composeServiceSpecs = []struct {
	key   string
	label string
}{
	{"docker-proxy", "Docker Proxy"},
	{"api-backend", "API Backend"},
	{"job-scheduler", "Job Scheduler"},
	{"webui-frontend", "WebUI Frontend"},
	{"traefik-edge", "Traefik Edge"},
	{"postgres-db", "PostgreSQL"},
	{"remote-desktop-guacd", "Guacamole"},
	{"wireguard-tunnel", "WireGuard Tunnel"},
}

func registerServerOverviewRoutes(mux *http.ServeMux, auth *authService, _ http.Handler) {
	mux.HandleFunc("/api/server/overview", serverOverviewHandler(auth))
}

func serverOverviewHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		payload, err := collectServerOverviewPayload(ctx, auth.store)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, payload)
	}
}

func collectServerOverviewPayload(ctx context.Context, store operatorStore) (map[string]any, error) {
	workerPayload := map[string]any{
		"active_count":              int64(0),
		"manager_active_count":      int64(0),
		"workers":                   []any{},
		"active_work":               []any{},
		"recent_work":               []any{},
		"site_names":                map[string]string{},
		"site_device_counts":        map[string]int64{},
		"site_online_device_counts": map[string]int64{},
		"sites":                     []any{},
		"worker_idle_ttl_seconds":   parseEnvIntMin("BOREALIS_SITE_WORKER_IDLE_TTL_SECONDS", 300, 300),
	}
	if workerStore, ok := store.(serverWorkerStore); ok {
		if payload, err := workerStore.serverWorkerPayload(ctx, 60, true); err == nil && payload != nil {
			workerPayload = payload
		}
	}

	releasePayload := collectAgentReleaseChannelSettings()
	if githubStore, ok := store.(githubTokenStateStore); ok {
		releasePayload["github_token"] = githubStore.githubTokenState(ctx)
	} else {
		releasePayload["github_token"] = defaultGithubTokenState()
	}
	releasePayload["settings_path"] = agentReleaseChannelsPath()
	if _, ok := releasePayload["last_persist_error"]; !ok {
		releasePayload["last_persist_error"] = ""
	}

	return map[string]any{
		"collected_at":           time.Now().UTC().Format(time.RFC3339Nano),
		"host":                   collectOverviewHostPayload(),
		"resources":              collectOverviewResourcePayload(),
		"services":               collectOverviewServiceRows(),
		"wireguard":              collectOverviewWireGuardPayload(ctx, store),
		"public_edge":            collectOverviewPublicEdgePayload(),
		"security":               collectOverviewSecurityPayload(),
		"ansible_runner":         collectAnsibleRunnerSettingsPayload(),
		"site_worker_settings":   collectSiteWorkerSettingsPayload(),
		"agent_release_channels": releasePayload,
		"remote_desktop":         collectOverviewRemoteDesktopPayload(),
		"operator_session_count": int64(0),
		"workers":                workerPayload,
	}, nil
}

func collectOverviewHostPayload() map[string]any {
	nowUTC := time.Now().UTC()
	nowLocal := nowUTC.Local()
	timezoneID := currentTimezoneID()
	hostname, _ := os.Hostname()
	return map[string]any{
		"hostname":           hostname,
		"kernel":             runtime.GOOS,
		"architecture":       runtime.GOARCH,
		"engine_mode":        firstText(strings.TrimSpace(os.Getenv("BOREALIS_ENGINE_MODE")), "unknown"),
		"webui_mode":         normalizeOverviewWebUIMode(os.Getenv("BOREALIS_WEBUI_MODE")),
		"server_time":        serializeServerTime(nowLocal, nowUTC, timezoneID),
		"timezone":           nowLocal.Format("MST"),
		"timezone_id":        timezoneID,
		"uptime_seconds":     readProcUptimeSeconds(),
		"public_base_url":    strings.TrimSpace(os.Getenv("BOREALIS_PUBLIC_BASE_URL")),
		"public_hostname":    strings.TrimSpace(os.Getenv("BOREALIS_PUBLIC_HOSTNAME")),
		"public_https_port":  parseIntDefault(os.Getenv("BOREALIS_PUBLIC_HTTPS_PORT"), 443),
		"deployment_profile": deploymentProfilePayload(),
	}
}

func collectOverviewResourcePayload() map[string]any {
	meminfo := readMeminfoKiB()
	memoryTotal := int64(meminfo["MemTotal"]) * 1024
	memoryFree := int64(meminfo["MemAvailable"]) * 1024
	if memoryFree <= 0 {
		memoryFree = int64(meminfo["MemFree"]) * 1024
	}
	swapTotal := int64(meminfo["SwapTotal"]) * 1024
	swapFree := int64(meminfo["SwapFree"]) * 1024
	load := []float64{0, 0, 0}
	if values, err := os.ReadFile("/proc/loadavg"); err == nil {
		parts := strings.Fields(string(values))
		for idx := 0; idx < len(parts) && idx < 3; idx++ {
			load[idx] = round2(parseFloatDefault(parts[idx], 0))
		}
	}
	return map[string]any{
		"load_average": load,
		"cpu_count":    runtime.NumCPU(),
		"memory":       bytesSummary(memoryTotal, memoryFree, ""),
		"swap":         bytesSummary(swapTotal, swapFree, ""),
		"disk_root":    diskUsageSummary("/", "/"),
		"disk_project": diskUsageSummary(projectRoot(), projectRoot()),
	}
}

func collectOverviewServiceRows() []map[string]any {
	rows := make([]map[string]any, 0, len(composeServiceSpecs))
	for _, spec := range composeServiceSpecs {
		containerName := "borealis-engine-" + spec.key
		if spec.key == "docker-proxy" {
			containerName = "borealis-engine-docker-proxy"
		}
		inspected := dockerInspectContainer(containerName)
		statePayload, _ := inspected["State"].(map[string]any)
		configPayload, _ := inspected["Config"].(map[string]any)
		state := strings.ToLower(cleanText(statePayload["Status"]))
		if state == "" {
			state = "unknown"
		}
		health := ""
		if healthPayload, ok := statePayload["Health"].(map[string]any); ok {
			health = strings.ToLower(cleanText(healthPayload["Status"]))
		}
		displayStatus := overviewDisplayStatus(state, health)
		rows = append(rows, map[string]any{
			"key":                spec.key,
			"label":              spec.label,
			"instance":           nil,
			"unit_name":          containerName,
			"compose_service":    spec.key,
			"runtime":            "compose",
			"docker_state":       state,
			"docker_health":      nullableStringValue(health),
			"docker_status":      displayStatus,
			"docker_status_text": cleanText(statePayload["Status"]),
			"display_status":     displayStatus,
			"active_state":       state,
			"sub_state":          firstText(health, state),
			"enabled_state":      "compose",
			"main_pid":           coerceInt64(statePayload["Pid"]),
			"started_at":         nullableStringValue(normalizeStartedAt(cleanText(statePayload["StartedAt"]))),
			"fragment_path":      nil,
			"restart_supported":  overviewServiceRestartSupported(spec.key),
			"actions":            overviewServiceActions(spec.key),
			"pending_action":     nil,
			"status":             overviewComposeStatus(state, health),
			"container_image":    cleanText(configPayload["Image"]),
		})
	}
	return rows
}

func collectOverviewWireGuardPayload(ctx context.Context, store operatorStore) map[string]any {
	interfacePresent, interfaceUp := linuxInterfaceState(wireguardInterfaceName)
	activeCount := activeWireGuardLeaseCount(ctx, store)
	endpointHost := firstText(strings.TrimSpace(os.Getenv("BOREALIS_PUBLIC_WIREGUARD_HOST")), strings.TrimSpace(os.Getenv("BOREALIS_PUBLIC_HOSTNAME")))
	endpointPort := parseIntDefault(os.Getenv("BOREALIS_PUBLIC_WIREGUARD_PORT"), parseIntDefault(os.Getenv("BOREALIS_WIREGUARD_PORT"), 30000))
	reason := "idle"
	if activeCount > 0 && interfacePresent && interfaceUp {
		reason = "listener_healthy"
	} else if activeCount > 0 {
		reason = "listener_unhealthy"
	}
	return map[string]any{
		"interface_name":               wireguardInterfaceName,
		"interface_present":            interfacePresent,
		"interface_up":                 interfaceUp,
		"active_tunnel_count":          activeCount,
		"listener_healthy":             interfacePresent && interfaceUp,
		"listener_reason":              reason,
		"listener_service_state":       nil,
		"recovery_in_progress":         false,
		"last_recovery_attempt_at":     nil,
		"last_recovery_attempt_at_iso": "",
		"shell_port":                   parseIntDefault(os.Getenv("BOREALIS_WIREGUARD_SHELL_PORT"), 47002),
		"vnc_port":                     parseIntDefault(os.Getenv("BOREALIS_VNC_PORT"), 5900),
		"vnc_ws_port":                  parseIntDefault(os.Getenv("BOREALIS_VNC_WS_PORT"), 4823),
		"wireguard_endpoint": map[string]any{
			"host":    endpointHost,
			"port":    endpointPort,
			"display": endpointDisplay(endpointHost, endpointPort),
		},
		"recover_supported": activeCount > 0,
		"active_tunnels":    []any{},
	}
}

func collectOverviewPublicEdgePayload() map[string]any {
	endpointHost := firstText(strings.TrimSpace(os.Getenv("BOREALIS_PUBLIC_WIREGUARD_HOST")), strings.TrimSpace(os.Getenv("BOREALIS_PUBLIC_HOSTNAME")))
	endpointPort := parseIntDefault(os.Getenv("BOREALIS_PUBLIC_WIREGUARD_PORT"), parseIntDefault(os.Getenv("BOREALIS_WIREGUARD_PORT"), 30000))
	return map[string]any{
		"enabled":            parseTruthy(os.Getenv("BOREALIS_PUBLIC_EDGE_ENABLED")),
		"fqdn":               strings.TrimSpace(os.Getenv("BOREALIS_PUBLIC_HOSTNAME")),
		"acme_email":         "",
		"public_base_url":    strings.TrimSpace(os.Getenv("BOREALIS_PUBLIC_BASE_URL")),
		"public_vnc_path":    firstText(strings.TrimSpace(os.Getenv("BOREALIS_PUBLIC_VNC_PATH")), "/remote-desktop/vnc"),
		"wireguard_endpoint": endpointDisplay(endpointHost, endpointPort),
		"certificates":       []any{},
	}
}

func collectOverviewSecurityPayload() map[string]any {
	return map[string]any{
		"aegis": map[string]any{
			"configured":   false,
			"locked":       false,
			"unlock_scope": "engine_global",
			"secret_scope": []any{"credentials", "github_token"},
			"updated_at":   int64(0),
		},
	}
}

func collectOverviewRemoteDesktopPayload() map[string]any {
	return map[string]any{
		"active_session_count": int64(0),
		"active_sessions":      []any{},
		"viewers": map[string]any{
			"guacamole": map[string]any{
				"enabled":   true,
				"available": true,
				"reason":    "",
			},
		},
	}
}

func activeWireGuardLeaseCount(ctx context.Context, store operatorStore) int64 {
	postgresStore, ok := store.(*postgresOperatorStore)
	if !ok {
		return 0
	}
	conn, err := postgresStore.db.Conn(ctx)
	if err != nil {
		return 0
	}
	defer conn.Close()
	var count sql.NullInt64
	err = conn.QueryRowContext(ctx, `
		SELECT COUNT(*)
		  FROM engine.device_vpn_ip_leases l
		  JOIN engine.devices d ON d.agent_id = l.agent_id
		 WHERE LOWER(COALESCE(d.status, '')) IN ('online', 'active')
	`).Scan(&count)
	if err != nil {
		return 0
	}
	return nullInt(count)
}

func overviewServiceActions(serviceKey string) []map[string]string {
	switch serviceKey {
	case "webui-frontend":
		return []map[string]string{
			{"id": "rebuild_prod", "label": "Rebuild Prod", "action": "rebuild", "mode": "prod"},
			{"id": "rebuild_dev", "label": "Rebuild Dev", "action": "rebuild", "mode": "dev"},
		}
	case "traefik-edge":
		return []map[string]string{{"id": "reload", "label": "Reload", "action": "reload"}}
	case "wireguard-tunnel":
		return []map[string]string{{"id": "reconcile", "label": "Reconcile", "action": "reconcile"}}
	case "docker-proxy", "api-backend", "job-scheduler", "postgres-db", "remote-desktop-guacd":
		return []map[string]string{{"id": "restart", "label": "Restart", "action": "restart"}}
	default:
		return []map[string]string{}
	}
}

func overviewServiceRestartSupported(serviceKey string) bool {
	for _, action := range overviewServiceActions(serviceKey) {
		if action["action"] == "restart" {
			return true
		}
	}
	return false
}

func overviewComposeStatus(state string, health string) string {
	if health == "unhealthy" {
		return "critical"
	}
	if state == "running" && health != "starting" {
		return "healthy"
	}
	if state == "restarting" || state == "created" || health == "starting" {
		return "warning"
	}
	if state == "exited" || state == "dead" || state == "removing" || state == "paused" {
		return "critical"
	}
	return "unknown"
}

func overviewDisplayStatus(state string, health string) string {
	if health != "" {
		return titleCaseAPI(health)
	}
	if state != "" {
		return titleCaseAPI(state)
	}
	return "Unknown"
}

func normalizeOverviewWebUIMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "dev", "developer", "development":
		return "development"
	case "prod", "production":
		return "production"
	default:
		if strings.TrimSpace(value) == "" {
			return "unknown"
		}
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func normalizeStartedAt(value string) string {
	text := strings.TrimSpace(value)
	if text == "" || strings.HasPrefix(text, "0001-") {
		return ""
	}
	return text
}

func endpointDisplay(host string, port int) string {
	if strings.TrimSpace(host) == "" {
		return ""
	}
	return strings.TrimSpace(host) + ":" + strconv.Itoa(port)
}

func titleCaseAPI(value string) string {
	parts := strings.Fields(strings.NewReplacer("_", " ", "-", " ").Replace(value))
	for idx, part := range parts {
		if part == "" {
			continue
		}
		parts[idx] = strings.ToUpper(part[:1]) + strings.ToLower(part[1:])
	}
	if len(parts) == 0 {
		return "Unknown"
	}
	return strings.Join(parts, " ")
}

func readProcUptimeSeconds() int64 {
	content, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(content))
	if len(fields) == 0 {
		return 0
	}
	return int64(parseFloatDefault(fields[0], 0))
}

func readMeminfoKiB() map[string]int64 {
	content, err := os.Open("/proc/meminfo")
	if err != nil {
		return map[string]int64{}
	}
	defer content.Close()
	values := map[string]int64{}
	scanner := bufio.NewScanner(content)
	for scanner.Scan() {
		line := scanner.Text()
		key, raw, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		parts := strings.Fields(raw)
		if len(parts) == 0 {
			continue
		}
		values[strings.TrimSpace(key)] = int64(parseIntDefault(parts[0], 0))
	}
	return values
}

func bytesSummary(totalBytes int64, freeBytes int64, path string) map[string]any {
	total := maxInt64(totalBytes, 0)
	free := maxInt64(freeBytes, 0)
	used := maxInt64(total-free, 0)
	usedPercent := float64(0)
	if total > 0 {
		usedPercent = round2((float64(used) / float64(total)) * 100)
	}
	payload := map[string]any{
		"total_bytes":  total,
		"used_bytes":   used,
		"free_bytes":   free,
		"used_percent": usedPercent,
	}
	if strings.TrimSpace(path) != "" {
		payload["path"] = path
	}
	return payload
}

func diskUsageSummary(targetPath string, label string) map[string]any {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(targetPath, &stat); err != nil {
		return bytesSummary(0, 0, label)
	}
	total := int64(stat.Blocks) * int64(stat.Bsize)
	free := int64(stat.Bavail) * int64(stat.Bsize)
	return bytesSummary(total, free, label)
}

func linuxInterfaceState(interfaceName string) (bool, bool) {
	operstatePath := filepath.Join("/sys/class/net", interfaceName, "operstate")
	content, err := os.ReadFile(operstatePath)
	if err != nil {
		return false, false
	}
	state := strings.ToLower(strings.TrimSpace(string(content)))
	return true, state == "up" || state == "unknown"
}

func projectRoot() string {
	return firstText(strings.TrimSpace(os.Getenv("BOREALIS_PROJECT_ROOT")), "/opt/Borealis")
}

func parseFloatDefault(value string, fallback float64) float64 {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func round2(value float64) float64 {
	return float64(int(value*100+0.5)) / 100
}

func maxInt64(value int64, minimum int64) int64 {
	if value < minimum {
		return minimum
	}
	return value
}
