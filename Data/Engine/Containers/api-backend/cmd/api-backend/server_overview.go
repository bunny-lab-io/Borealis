package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
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
}{}

var k3sWorkloadServiceSpecs = []struct {
	key   string
	label string
}{
	{"api-backend", "API Backend"},
	{"job-scheduler", "Job Scheduler"},
	{"postgres-db", "PostgreSQL"},
	{"remote-desktop-guacd", "Guacamole"},
	{"traefik-edge", "Traefik Edge"},
	{"webui-frontend", "WebUI Frontend"},
	{"wireguard-tunnel", "WireGuard Tunnel"},
}

type overviewServiceSnapshotStore interface {
	overviewServiceSnapshots(ctx context.Context) (map[string]overviewServiceSnapshot, error)
}

type overviewServiceSnapshot struct {
	payload   map[string]any
	updatedAt int64
}

func registerServerOverviewRoutes(mux *http.ServeMux, auth *authService, realtime *operatorRealtimeHub, _ http.Handler) {
	mux.HandleFunc("/api/server/overview", serverOverviewHandler(auth, realtime))
}

func serverOverviewHandler(auth *authService, realtime *operatorRealtimeHub) http.HandlerFunc {
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
		payload, err := collectServerOverviewPayload(ctx, auth.store, realtime)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, payload)
	}
}

func collectServerOverviewPayload(ctx context.Context, store operatorStore, realtime *operatorRealtimeHub) (map[string]any, error) {
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
		"services":               collectOverviewServiceRows(ctx, store),
		"wireguard":              collectOverviewWireGuardPayload(ctx, store),
		"public_edge":            collectOverviewPublicEdgePayload(),
		"security":               collectOverviewSecurityPayload(),
		"ansible_runner":         collectAnsibleRunnerSettingsPayload(),
		"site_worker_settings":   collectSiteWorkerSettingsPayload(),
		"agent_release_channels": releasePayload,
		"remote_desktop":         collectOverviewRemoteDesktopPayload(),
		"operator_session_count": overviewOperatorSessionCount(realtime),
	}, nil
}

func collectOverviewHostPayload() map[string]any {
	nowUTC := time.Now().UTC()
	nowLocal := nowUTC.Local()
	timezoneID := currentTimezoneID()
	hostname, _ := os.Hostname()
	webuiUpstreamHost := firstText(strings.TrimSpace(os.Getenv("BOREALIS_WEBUI_UPSTREAM_HOST")), "127.0.0.1")
	webuiUpstreamPort := parseIntDefault(os.Getenv("BOREALIS_WEBUI_UPSTREAM_PORT"), 8000)
	return map[string]any{
		"hostname":            hostname,
		"kernel":              runtime.GOOS,
		"architecture":        runtime.GOARCH,
		"engine_mode":         firstText(strings.TrimSpace(os.Getenv("BOREALIS_ENGINE_MODE")), "unknown"),
		"webui_mode":          normalizeOverviewWebUIMode(os.Getenv("BOREALIS_WEBUI_MODE")),
		"webui_traffic_owner": normalizeOverviewWebUITrafficOwner(os.Getenv("BOREALIS_WEBUI_TRAFFIC_OWNER")),
		"webui_upstream":      map[string]any{"host": webuiUpstreamHost, "port": webuiUpstreamPort, "display": endpointDisplay(webuiUpstreamHost, webuiUpstreamPort)},
		"server_time":         serializeServerTime(nowLocal, nowUTC, timezoneID),
		"timezone":            nowLocal.Format("MST"),
		"timezone_id":         timezoneID,
		"uptime_seconds":      readProcUptimeSeconds(),
		"public_base_url":     strings.TrimSpace(os.Getenv("BOREALIS_PUBLIC_BASE_URL")),
		"public_hostname":     strings.TrimSpace(os.Getenv("BOREALIS_PUBLIC_HOSTNAME")),
		"public_https_port":   parseIntDefault(os.Getenv("BOREALIS_PUBLIC_HTTPS_PORT"), 443),
		"deployment_profile":  deploymentProfilePayload(),
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

func collectOverviewServiceRows(ctx context.Context, store operatorStore) []map[string]any {
	if !containerizedEngineEnabled() {
		return collectSystemdServiceRows()
	}
	snapshots := collectOverviewComposeServiceSnapshots(ctx, store)
	rows := make([]map[string]any, 0, len(composeServiceSpecs))
	for _, spec := range composeServiceSpecs {
		if snapshot, ok := snapshots[spec.key]; ok && overviewServiceSnapshotFresh(snapshot.updatedAt) {
			rows = append(rows, overviewComposeServiceRowFromSnapshot(spec.key, spec.label, snapshot))
			continue
		}
		rows = append(rows, overviewComposeServiceRowFromDocker(spec.key, spec.label))
	}
	rows = append(rows, collectOverviewK3sWorkloadRows(ctx)...)
	return rows
}

func collectOverviewComposeServiceSnapshots(ctx context.Context, store operatorStore) map[string]overviewServiceSnapshot {
	if store == nil {
		return map[string]overviewServiceSnapshot{}
	}
	snapshotStore, ok := store.(overviewServiceSnapshotStore)
	if !ok {
		return map[string]overviewServiceSnapshot{}
	}
	requestCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	snapshots, err := snapshotStore.overviewServiceSnapshots(requestCtx)
	if err != nil {
		return map[string]overviewServiceSnapshot{}
	}
	return snapshots
}

func (s *postgresOperatorStore) overviewServiceSnapshots(ctx context.Context) (map[string]overviewServiceSnapshot, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, `
		SELECT service_key, payload_json, updated_at
		  FROM engine.job_scheduler_service_snapshots
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	snapshots := map[string]overviewServiceSnapshot{}
	for rows.Next() {
		var serviceKey string
		var rawPayload string
		var updatedAt int64
		if err := rows.Scan(&serviceKey, &rawPayload, &updatedAt); err != nil {
			return nil, err
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(rawPayload), &payload); err != nil || payload == nil {
			continue
		}
		key := normalizeOverviewSnapshotServiceKey(serviceKey, payload)
		if key == "" {
			continue
		}
		snapshots[key] = overviewServiceSnapshot{payload: payload, updatedAt: updatedAt}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return snapshots, nil
}

func normalizeOverviewSnapshotServiceKey(serviceKey string, payload map[string]any) string {
	candidates := []string{
		serviceKey,
		cleanText(payload["Service"]),
		cleanText(payload["service"]),
		cleanText(payload["Name"]),
		cleanText(payload["name"]),
	}
	for _, candidate := range candidates {
		normalized := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(candidate), "borealis-engine-"))
		for _, spec := range composeServiceSpecs {
			if normalized == spec.key {
				return spec.key
			}
		}
	}
	return ""
}

func overviewServiceSnapshotFresh(updatedAt int64) bool {
	return updatedAt > 0 && time.Now().Unix()-updatedAt <= 120
}

func overviewComposeServiceRowFromDocker(serviceKey string, label string) map[string]any {
	containerName := "borealis-engine-" + serviceKey
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
	return map[string]any{
		"key":                serviceKey,
		"label":              label,
		"instance":           nil,
		"unit_name":          containerName,
		"compose_service":    serviceKey,
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
		"restart_supported":  overviewServiceRestartSupported(serviceKey),
		"actions":            overviewServiceActions(serviceKey),
		"pending_action":     nil,
		"status":             overviewComposeStatus(state, health),
		"container_image":    cleanText(configPayload["Image"]),
	}
}

func overviewComposeServiceRowFromSnapshot(serviceKey string, label string, snapshot overviewServiceSnapshot) map[string]any {
	payload := snapshot.payload
	containerName := firstText(cleanText(payload["Name"]), cleanText(payload["Names"]), "borealis-engine-"+serviceKey)
	state := strings.ToLower(firstText(cleanText(payload["State"]), cleanText(payload["state"])))
	if state == "" {
		state = "unknown"
	}
	health := strings.ToLower(firstText(cleanText(payload["Health"]), cleanText(payload["health"])))
	statusText := firstText(cleanText(payload["Status"]), cleanText(payload["status"]), state)
	displayStatus := overviewDisplayStatus(state, health)
	startedAt := firstText(cleanText(payload["StartedAt"]), cleanText(payload["started_at"]))
	if startedAt == "" {
		startedAt = firstText(cleanText(payload["CreatedAt"]), cleanText(payload["created_at"]))
	}
	return map[string]any{
		"key":                 serviceKey,
		"label":               label,
		"instance":            nil,
		"unit_name":           containerName,
		"compose_service":     serviceKey,
		"runtime":             "compose",
		"docker_state":        state,
		"docker_health":       nullableStringValue(health),
		"docker_status":       displayStatus,
		"docker_status_text":  statusText,
		"display_status":      displayStatus,
		"active_state":        state,
		"sub_state":           firstText(health, state),
		"enabled_state":       "compose",
		"main_pid":            coerceInt64(payload["Pid"]),
		"started_at":          nullableStringValue(normalizeStartedAt(startedAt)),
		"fragment_path":       nil,
		"restart_supported":   overviewServiceRestartSupported(serviceKey),
		"actions":             overviewServiceActions(serviceKey),
		"pending_action":      nil,
		"status":              overviewComposeStatus(state, health),
		"container_image":     firstText(cleanText(payload["Image"]), cleanText(payload["image"])),
		"snapshot_source":     "job-scheduler",
		"snapshot_updated_at": snapshot.updatedAt,
	}
}

func collectOverviewK3sWorkloadRows(ctx context.Context) []map[string]any {
	rows := []map[string]any{}
	client, configured := newBorealisOperatorClientFromEnv()
	for _, spec := range k3sWorkloadServiceSpecs {
		if !overviewK3sWorkloadEnabled(spec.key) {
			continue
		}
		row := overviewK3sWorkloadServiceRow(spec.key, spec.label, nil, false, configured)
		if configured {
			requestCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			payload, err := client.command(requestCtx, "GetWorkloadStatus", map[string]any{"service_key": spec.key})
			cancel()
			if err == nil {
				row = overviewK3sWorkloadServiceRow(spec.key, spec.label, schedulerAnyMap(payload["result"]), true, true)
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func overviewK3sWorkloadEnabled(serviceKey string) bool {
	switch strings.ToLower(strings.TrimSpace(serviceKey)) {
	case "api-backend":
		return schedulerEnvOwnerIsK3s("BOREALIS_API_BACKEND_RUNTIME_OWNER") || schedulerEnvOwnerIsK3s("BOREALIS_API_BACKEND_TRAFFIC_OWNER")
	case "job-scheduler":
		return schedulerEnvOwnerIsK3s("BOREALIS_JOB_SCHEDULER_RUNTIME_OWNER")
	case "postgres-db":
		return schedulerEnvOwnerIsK3s("BOREALIS_POSTGRES_RUNTIME_OWNER") || schedulerEnvOwnerIsK3s("BOREALIS_POSTGRES_TRAFFIC_OWNER")
	case "remote-desktop-guacd":
		return schedulerEnvOwnerIsK3s("BOREALIS_REMOTE_DESKTOP_GUACD_RUNTIME_OWNER")
	case "traefik-edge":
		return schedulerEnvOwnerIsK3s("BOREALIS_TRAEFIK_EDGE_RUNTIME_OWNER")
	case "webui-frontend":
		return schedulerEnvOwnerIsK3s("BOREALIS_WEBUI_RUNTIME_OWNER") || schedulerEnvOwnerIsK3s("BOREALIS_WEBUI_TRAFFIC_OWNER")
	case "wireguard-tunnel":
		return schedulerEnvOwnerIsK3s("BOREALIS_WIREGUARD_TUNNEL_RUNTIME_OWNER")
	default:
		return false
	}
}

func overviewK3sWorkloadKind(serviceKey string) string {
	switch strings.ToLower(strings.TrimSpace(serviceKey)) {
	case "postgres-db":
		return "StatefulSet"
	default:
		return "Deployment"
	}
}

func overviewK3sWorkloadServiceRow(serviceKey string, label string, workload map[string]any, available bool, operatorConfigured bool) map[string]any {
	desiredReady := false
	if available {
		if value, ok := workload["desired_ready"].(bool); ok {
			desiredReady = value
		}
	}
	status := "unknown"
	state := "unknown"
	if desiredReady {
		status = "healthy"
		state = "running"
	} else if operatorConfigured {
		status = "warning"
		state = "pending"
	}
	kind := firstText(cleanText(workload["kind"]), overviewK3sWorkloadKind(serviceKey))
	name := firstText(cleanText(workload["name"]), serviceKey)
	actions := overviewServiceActions(serviceKey)
	return map[string]any{
		"key":                serviceKey,
		"label":              label,
		"instance":           nil,
		"unit_name":          strings.ToLower(kind) + "/" + name,
		"compose_service":    nil,
		"kubernetes_kind":    kind,
		"kubernetes_name":    name,
		"kubernetes_ready":   desiredReady,
		"replicas":           coerceInt64(workload["replicas"]),
		"ready_replicas":     coerceInt64(workload["ready_replicas"]),
		"available_replicas": coerceInt64(workload["available_replicas"]),
		"runtime":            "k3s",
		"docker_state":       "",
		"docker_health":      nullableStringValue(status),
		"docker_status":      status,
		"docker_status_text": state,
		"display_status":     status,
		"active_state":       state,
		"sub_state":          state,
		"enabled_state":      "k3s",
		"main_pid":           int64(0),
		"started_at":         nil,
		"fragment_path":      nil,
		"restart_supported":  overviewServiceRestartSupported(serviceKey),
		"actions":            actions,
		"pending_action":     nil,
		"status":             status,
		"container_image":    nil,
	}
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
	fqdn := strings.TrimSpace(os.Getenv("BOREALIS_PUBLIC_HOSTNAME"))
	deploymentProfile := overviewEngineDeploymentProfile()
	localCAPayload := collectOverviewLocalCAPayload()
	serverIPFallback := ""
	certificateMode := "traefik_default"
	acmePath := overviewACMEStoragePath()
	certificates, certificateReadError := collectOverviewACMECertificates(acmePath, fqdn)
	if deploymentProfile == "internal-only" {
		serverIPFallback = overviewEngineIPFallback()
		certificateMode = "local_ca"
		certificates = []any{}
		certificateReadError = ""
		if row, errText := collectOverviewPEMCertificate(overviewLocalTLSCertPath(), "traefik_local_ca", "local-ca", fqdn); row != nil {
			certificates = append(certificates, row)
		} else if errText != "" {
			certificateReadError = errText
		}
	} else if len(certificates) > 0 {
		certificateMode = "acme"
	}
	return map[string]any{
		"enabled":                  parseTruthy(os.Getenv("BOREALIS_PUBLIC_EDGE_ENABLED")),
		"fqdn":                     fqdn,
		"fqdn_aliases":             overviewFQDNAliases(fqdn),
		"network_mode":             overviewEngineNetworkMode(),
		"network_mode_label":       overviewEngineNetworkModeLabel(),
		"deployment_profile":       deploymentProfile,
		"deployment_profile_label": overviewEngineDeploymentProfileLabel(),
		"server_ip_fallback":       serverIPFallback,
		"certificate_mode":         certificateMode,
		"acme_email":               overviewACMEEmail(),
		"public_base_url":          strings.TrimSpace(os.Getenv("BOREALIS_PUBLIC_BASE_URL")),
		"public_vnc_path":          firstText(strings.TrimSpace(os.Getenv("BOREALIS_PUBLIC_VNC_PATH")), "/remote-desktop/vnc"),
		"wireguard_endpoint":       endpointDisplay(endpointHost, endpointPort),
		"certificates":             certificates,
		"certificate_count":        len(certificates),
		"acme_storage_path":        acmePath,
		"acme_read_error":          certificateReadError,
		"local_ca":                 localCAPayload,
	}
}

func overviewEngineDeploymentProfile() string {
	if strings.TrimSpace(os.Getenv("BOREALIS_ENGINE_NETWORK_MODE")) != "" {
		if overviewEngineNetworkMode() == "local" {
			return "internal-only"
		}
		return "externally-accessible"
	}
	value := strings.ToLower(strings.TrimSpace(os.Getenv("BOREALIS_ENGINE_DEPLOYMENT_PROFILE")))
	value = strings.ReplaceAll(value, "_", "-")
	switch value {
	case "internal-only", "internal", "local", "local-only":
		return "internal-only"
	default:
		return "externally-accessible"
	}
}

func overviewEngineNetworkMode() string {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("BOREALIS_ENGINE_NETWORK_MODE")))
	value = strings.ReplaceAll(value, "_", "-")
	switch value {
	case "local", "local-edge", "site-local", "internal-only", "internal", "local-only", "private", "private-edge", "private-network":
		return "local"
	case "public", "public-edge", "internet", "internet-edge", "externally-accessible", "external", "public-facing":
		return "public"
	}
	profile := strings.ToLower(strings.TrimSpace(os.Getenv("BOREALIS_ENGINE_DEPLOYMENT_PROFILE")))
	profile = strings.ReplaceAll(profile, "_", "-")
	switch profile {
	case "internal-only", "internal", "local", "local-only":
		return "local"
	}
	return "public"
}

func overviewEngineNetworkModeLabel() string {
	if value := strings.TrimSpace(os.Getenv("BOREALIS_ENGINE_NETWORK_MODE_LABEL")); value != "" {
		return value
	}
	if overviewEngineNetworkMode() == "local" {
		return "Local"
	}
	return "Public"
}

func overviewEngineDeploymentProfileLabel() string {
	if value := strings.TrimSpace(os.Getenv("BOREALIS_ENGINE_DEPLOYMENT_PROFILE_LABEL")); value != "" {
		return value
	}
	if overviewEngineDeploymentProfile() == "internal-only" {
		return "Internal-Only"
	}
	return "Externally Accessible"
}

func overviewFQDNAliases(primary string) []any {
	seen := map[string]bool{}
	values := []any{}
	add := func(value string) {
		value = strings.TrimSpace(strings.ToLower(strings.TrimSuffix(value, ".")))
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		values = append(values, value)
	}
	add(primary)
	for _, value := range strings.Split(os.Getenv("BOREALIS_PUBLIC_HOSTNAME_ALIASES"), ",") {
		add(value)
	}
	return values
}

func overviewLocalCAEnabled() bool {
	if parseTruthy(os.Getenv("BOREALIS_LOCAL_CA_ENABLED")) {
		return true
	}
	return overviewEngineDeploymentProfile() == "internal-only"
}

func overviewLocalCACertPath() string {
	if value := strings.TrimSpace(os.Getenv("BOREALIS_LOCAL_CA_CERT_PATH")); value != "" {
		return value
	}
	if value := cleanText(overviewTraefikSettings()["local_ca_cert_path"]); value != "" {
		return value
	}
	return filepath.Join(projectRoot(), "Engine", "Services", "traefik-edge", "state", "local-ca", "borealis-local-ca.pem")
}

func overviewLocalCAKeyPath() string {
	if value := strings.TrimSpace(os.Getenv("BOREALIS_LOCAL_CA_KEY_PATH")); value != "" {
		return value
	}
	return filepath.Join(projectRoot(), "Engine", "Services", "traefik-edge", "state", "local-ca", "borealis-local-ca.key")
}

func overviewLocalTLSCertPath() string {
	if value := strings.TrimSpace(os.Getenv("BOREALIS_LOCAL_TLS_CERT_PATH")); value != "" {
		return value
	}
	if value := cleanText(overviewTraefikSettings()["local_tls_cert_path"]); value != "" {
		return value
	}
	return filepath.Join(projectRoot(), "Engine", "Services", "traefik-edge", "state", "local-certs", "traefik-local-leaf.pem")
}

func overviewLocalTLSKeyPath() string {
	if value := strings.TrimSpace(os.Getenv("BOREALIS_LOCAL_TLS_KEY_PATH")); value != "" {
		return value
	}
	if value := cleanText(overviewTraefikSettings()["local_tls_key_path"]); value != "" {
		return value
	}
	return filepath.Join(projectRoot(), "Engine", "Services", "traefik-edge", "state", "local-certs", "traefik-local-leaf.key")
}

func collectOverviewLocalCAPayload() map[string]any {
	enabled := overviewLocalCAEnabled()
	path := overviewLocalCACertPath()
	payload := map[string]any{
		"enabled":     enabled,
		"cert_path":   path,
		"installable": false,
		"pem_b64":     "",
		"status":      "disabled",
		"severity":    "healthy",
		"read_error":  "",
	}
	if !enabled {
		return payload
	}
	row, errText := collectOverviewPEMCertificate(path, "borealis_local_ca", "local-ca", "Borealis Local Engine CA")
	if row == nil {
		payload["status"] = "missing"
		payload["severity"] = "critical"
		payload["read_error"] = errText
		return payload
	}
	for key, value := range row {
		payload[key] = value
	}
	if content, err := os.ReadFile(path); err == nil && len(content) > 0 {
		payload["pem_b64"] = base64.StdEncoding.EncodeToString(content)
		payload["installable"] = true
	} else if err != nil {
		payload["read_error"] = err.Error()
	}
	return payload
}

func collectOverviewPEMCertificate(path string, source string, resolver string, fallbackName string) (map[string]any, string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, "certificate_path_unavailable"
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err.Error()
	}
	remaining := content
	for {
		block, rest := pem.Decode(remaining)
		if block == nil {
			return nil, "certificate_pem_unavailable"
		}
		remaining = rest
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err.Error()
		}
		return overviewPEMCertificateRow(cert, path, source, resolver, fallbackName), ""
	}
}

func overviewPEMCertificateRow(cert *x509.Certificate, path string, source string, resolver string, fallbackName string) map[string]any {
	fingerprint := sha256.Sum256(cert.Raw)
	now := time.Now().UTC()
	daysRemaining := int64(cert.NotAfter.UTC().Sub(now).Hours() / 24)
	severity := "healthy"
	status := "valid"
	if !cert.NotAfter.After(now) {
		severity = "critical"
		status = "expired"
	} else if daysRemaining <= 30 {
		severity = "warning"
		status = "expiring"
	}
	domains := []any{}
	seen := map[string]bool{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		domains = append(domains, value)
	}
	add(cert.Subject.CommonName)
	for _, name := range cert.DNSNames {
		add(name)
	}
	for _, ip := range cert.IPAddresses {
		add(ip.String())
	}
	return map[string]any{
		"name":               firstText(cert.Subject.CommonName, fallbackName),
		"domains":            domains,
		"resolver":           resolver,
		"source":             source,
		"status":             status,
		"severity":           severity,
		"not_before":         cert.NotBefore.UTC().Format(time.RFC3339),
		"expires_at":         cert.NotAfter.UTC().Format(time.RFC3339),
		"days_remaining":     daysRemaining,
		"issuer":             cert.Issuer.String(),
		"subject":            cert.Subject.String(),
		"serial_number":      cert.SerialNumber.String(),
		"sha256_fingerprint": strings.ToUpper(hex.EncodeToString(fingerprint[:])),
		"path":               path,
	}
}

func overviewOperatorSessionCount(realtime *operatorRealtimeHub) int64 {
	if realtime == nil {
		return 0
	}
	return realtime.subscriberCount()
}

func overviewACMEEmail() string {
	if value := strings.TrimSpace(os.Getenv("BOREALIS_ACME_EMAIL")); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("BOREALIS_TRAEFIK_ACME_EMAIL")); value != "" {
		return value
	}
	if value := cleanText(overviewTraefikSettings()["acme_email"]); value != "" {
		return value
	}
	return ""
}

func overviewACMEStoragePath() string {
	if value := strings.TrimSpace(os.Getenv("BOREALIS_TRAEFIK_ACME_STORAGE_PATH")); value != "" {
		return value
	}
	if value := cleanText(overviewTraefikSettings()["acme_storage_path"]); value != "" {
		return value
	}
	return filepath.Join(projectRoot(), "Engine", "Services", "traefik-edge", "state", "acme.json")
}

func overviewTraefikSettings() map[string]any {
	settingsPath := strings.TrimSpace(os.Getenv("BOREALIS_TRAEFIK_SETTINGS_PATH"))
	if settingsPath == "" {
		settingsPath = filepath.Join(projectRoot(), "Engine", "Services", "traefik-edge", "state", "Settings.json")
	}
	content, err := os.ReadFile(settingsPath)
	if err != nil {
		return map[string]any{}
	}
	var payload map[string]any
	if err := json.Unmarshal(content, &payload); err != nil {
		return map[string]any{}
	}
	return payload
}

func collectOverviewACMECertificates(acmePath string, fqdn string) ([]any, string) {
	acmePath = strings.TrimSpace(acmePath)
	if acmePath == "" {
		return []any{}, "acme_path_unavailable"
	}
	content, err := os.ReadFile(acmePath)
	if err != nil {
		return []any{}, err.Error()
	}
	var payload map[string]any
	if err := json.Unmarshal(content, &payload); err != nil {
		return []any{}, err.Error()
	}
	rows := []any{}
	for resolverName, resolverRaw := range payload {
		resolver, ok := resolverRaw.(map[string]any)
		if !ok {
			continue
		}
		certificates, _ := resolver["Certificates"].([]any)
		if len(certificates) == 0 {
			certificates, _ = resolver["certificates"].([]any)
		}
		for _, certRaw := range certificates {
			certEntry, ok := certRaw.(map[string]any)
			if !ok {
				continue
			}
			if row := overviewACMECertificateRow(resolverName, certEntry, fqdn); row != nil {
				rows = append(rows, row)
			}
		}
	}
	return rows, ""
}

func overviewACMECertificateRow(resolverName string, certEntry map[string]any, fqdn string) map[string]any {
	certificatePEM := cleanText(certEntry["certificate"])
	if certificatePEM == "" {
		certificatePEM = cleanText(certEntry["Certificate"])
	}
	if certificatePEM == "" {
		return nil
	}
	decoded, err := base64.StdEncoding.DecodeString(certificatePEM)
	if err == nil && len(decoded) > 0 {
		certificatePEM = string(decoded)
	}
	block, _ := pem.Decode([]byte(certificatePEM))
	if block == nil {
		return map[string]any{
			"name":     overviewACMEDomainName(certEntry, fqdn),
			"resolver": cleanText(resolverName),
			"status":   "parse_failed",
			"severity": "critical",
			"source":   "traefik_acme",
		}
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return map[string]any{
			"name":     overviewACMEDomainName(certEntry, fqdn),
			"resolver": cleanText(resolverName),
			"status":   "parse_failed",
			"severity": "critical",
			"source":   "traefik_acme",
		}
	}
	fingerprint := sha256.Sum256(cert.Raw)
	domains := overviewACMEDomains(certEntry, cert)
	name := firstText(overviewACMEDomainName(certEntry, fqdn), cert.Subject.CommonName, fqdn)
	now := time.Now().UTC()
	daysRemaining := int64(cert.NotAfter.UTC().Sub(now).Hours() / 24)
	severity := "healthy"
	status := "valid"
	if !cert.NotAfter.After(now) {
		severity = "critical"
		status = "expired"
	} else if daysRemaining <= 30 {
		severity = "warning"
		status = "expiring"
	}
	return map[string]any{
		"name":               name,
		"domains":            domains,
		"resolver":           cleanText(resolverName),
		"source":             "traefik_acme",
		"status":             status,
		"severity":           severity,
		"not_before":         cert.NotBefore.UTC().Format(time.RFC3339),
		"expires_at":         cert.NotAfter.UTC().Format(time.RFC3339),
		"days_remaining":     daysRemaining,
		"issuer":             cert.Issuer.String(),
		"subject":            cert.Subject.String(),
		"serial_number":      cert.SerialNumber.String(),
		"sha256_fingerprint": strings.ToUpper(hex.EncodeToString(fingerprint[:])),
		"store":              firstText(cleanText(certEntry["Store"]), cleanText(certEntry["store"])),
	}
}

func overviewACMEDomainName(certEntry map[string]any, fqdn string) string {
	if domain, ok := certEntry["domain"].(map[string]any); ok {
		if value := cleanText(domain["main"]); value != "" {
			return value
		}
	}
	if domain, ok := certEntry["Domain"].(map[string]any); ok {
		if value := cleanText(domain["main"]); value != "" {
			return value
		}
	}
	return strings.TrimSpace(fqdn)
}

func overviewACMEDomains(certEntry map[string]any, cert *x509.Certificate) []any {
	seen := map[string]bool{}
	values := []any{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		values = append(values, value)
	}
	if domain, ok := certEntry["domain"].(map[string]any); ok {
		add(cleanText(domain["main"]))
		if sans, ok := domain["sans"].([]any); ok {
			for _, san := range sans {
				add(cleanText(san))
			}
		}
	}
	if domain, ok := certEntry["Domain"].(map[string]any); ok {
		add(cleanText(domain["main"]))
		if sans, ok := domain["sans"].([]any); ok {
			for _, san := range sans {
				add(cleanText(san))
			}
		}
	}
	if cert != nil {
		add(cert.Subject.CommonName)
		for _, name := range cert.DNSNames {
			add(name)
		}
	}
	return values
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

type wireGuardLeaseCountStore interface {
	activeWireGuardLeaseCount(ctx context.Context) int64
}

func activeWireGuardLeaseCount(ctx context.Context, store operatorStore) int64 {
	counter, ok := store.(wireGuardLeaseCountStore)
	if !ok {
		return 0
	}
	return counter.activeWireGuardLeaseCount(ctx)
}

func (s *postgresOperatorStore) activeWireGuardLeaseCount(ctx context.Context) int64 {
	conn, err := s.db.Conn(ctx)
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
		return []map[string]string{{"id": "restart", "label": "Restart", "action": "restart"}}
	case "traefik-edge":
		return []map[string]string{{"id": "reload", "label": "Reload", "action": "reload"}}
	case "wireguard-tunnel":
		return []map[string]string{{"id": "reconcile", "label": "Reconcile", "action": "reconcile"}}
	case "api-backend", "postgres-db", "remote-desktop-guacd":
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

func normalizeOverviewWebUITrafficOwner(value string) string {
	switch strings.ToLower(strings.TrimSpace(strings.ReplaceAll(value, "_", "-"))) {
	case "", "auto", "k3s", "kubernetes", "compose", "docker", "docker-compose":
		return "k3s"
	default:
		return "unknown"
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
