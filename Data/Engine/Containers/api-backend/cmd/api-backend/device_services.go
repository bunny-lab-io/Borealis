package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	internalTokenHeader  = "X-Borealis-Internal-Token"
	serviceRefreshPeriod = 60
)

var internalTokenContext = []byte("borealis-job-scheduler-internal-v1")

type deviceServicesStore interface {
	loadDeviceServices(ctx context.Context, profile operatorProfile, hostname string) (deviceServicesSnapshot, int, error)
}

type deviceServicesSnapshot struct {
	Hostname string
	AgentID  string
	SiteID   *int64
	Services []map[string]any
	Reported int64
	Route    *agentWorkerRoute
}

func deviceServicesHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		profile, err := auth.currentProfile(r.Context(), r)
		if err != nil {
			if isUnauthorizedAuthError(err) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
			return
		}
		store, ok := auth.store.(deviceServicesStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "device_services_unavailable"})
			return
		}
		timeout := auth.timeout
		if timeout <= 0 {
			timeout = defaultAuthTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		snapshot, status, err := store.loadDeviceServices(ctx, profile, r.PathValue("hostname"))
		if err != nil {
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		agentSocket := false
		if snapshot.Route != nil {
			workerCtx, workerCancel := context.WithTimeout(r.Context(), 5*time.Second)
			agentSocket = workerHostServiceRegistered(workerCtx, auth, snapshot.Route, snapshot.Hostname, "system")
			workerCancel()
		}
		writeJSON(w, status, map[string]any{
			"hostname":                 firstText(snapshot.Hostname, r.PathValue("hostname")),
			"agent_id":                 snapshot.AgentID,
			"agent_socket":             agentSocket,
			"reported_at":              snapshot.Reported,
			"refresh_interval_seconds": serviceRefreshPeriod,
			"count":                    len(snapshot.Services),
			"services":                 snapshot.Services,
		})
	}
}

func (s *postgresOperatorStore) loadDeviceServices(ctx context.Context, profile operatorProfile, hostname string) (deviceServicesSnapshot, int, error) {
	hostname = cleanText(hostname)
	if hostname == "" {
		return deviceServicesSnapshot{}, http.StatusNotFound, errors.New("not found")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return deviceServicesSnapshot{}, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	var row struct {
		Hostname sql.NullString
		AgentID  sql.NullString
		Services sql.NullString
		SiteID   sql.NullInt64
	}
	err = conn.QueryRowContext(ctx, `
		SELECT d.hostname, d.agent_id, d.services, ds.site_id
		  FROM engine.devices AS d
	 LEFT JOIN engine.device_sites AS ds ON ds.device_hostname = d.hostname
		 WHERE LOWER(d.hostname) = LOWER($1)
	  ORDER BY COALESCE(d.last_seen, 0) DESC
		 LIMIT 1
	`, hostname).Scan(&row.Hostname, &row.AgentID, &row.Services, &row.SiteID)
	if errors.Is(err, sql.ErrNoRows) {
		return deviceServicesSnapshot{}, http.StatusNotFound, errors.New("not found")
	}
	if err != nil {
		return deviceServicesSnapshot{}, http.StatusInternalServerError, err
	}
	if row.SiteID.Valid {
		allowed, err := profileCanAccessSite(ctx, conn, profile, row.SiteID.Int64)
		if err != nil {
			return deviceServicesSnapshot{}, http.StatusInternalServerError, err
		}
		if !allowed {
			return deviceServicesSnapshot{}, http.StatusNotFound, errors.New("not found")
		}
	} else if !strings.EqualFold(strings.TrimSpace(profile.Role), "admin") {
		return deviceServicesSnapshot{}, http.StatusNotFound, errors.New("not found")
	}

	services, reportedAt := normalizeDeviceServicePayload(row.Services)
	snapshot := deviceServicesSnapshot{
		Hostname: nullString(row.Hostname),
		AgentID:  row.AgentID.String,
		Services: services,
		Reported: reportedAt,
	}
	if row.SiteID.Valid && row.SiteID.Int64 > 0 {
		siteID := row.SiteID.Int64
		snapshot.SiteID = &siteID
		route, err := fetchAgentWorkerRoute(ctx, conn, siteID)
		if err != nil {
			return deviceServicesSnapshot{}, http.StatusInternalServerError, err
		}
		snapshot.Route = route
	}
	return snapshot, http.StatusOK, nil
}

func normalizeDeviceServicePayload(raw sql.NullString) ([]map[string]any, int64) {
	payload := normalizeServicePayloadShape(raw)
	reportedAt := coerceInt64(payload["reported_at"])
	items, _ := payload["services"].([]any)
	byID := map[string]map[string]any{}
	for _, item := range items {
		entry := normalizeServiceEntry(item, reportedAt)
		if entry == nil {
			continue
		}
		serviceID := cleanText(entry["service_id"])
		byID[serviceID] = entry
		capturedAt := coerceInt64(entry["captured_at"])
		if capturedAt > reportedAt {
			reportedAt = capturedAt
		}
	}
	services := make([]map[string]any, 0, len(byID))
	for _, entry := range byID {
		services = append(services, entry)
	}
	sort.SliceStable(services, func(left, right int) bool {
		leftName := strings.ToLower(firstText(cleanText(services[left]["display_name"]), cleanText(services[left]["name"])))
		rightName := strings.ToLower(firstText(cleanText(services[right]["display_name"]), cleanText(services[right]["name"])))
		if leftName != rightName {
			return leftName < rightName
		}
		leftService := strings.ToLower(cleanText(services[left]["name"]))
		rightService := strings.ToLower(cleanText(services[right]["name"]))
		if leftService != rightService {
			return leftService < rightService
		}
		return strings.ToLower(cleanText(services[left]["description"])) < strings.ToLower(cleanText(services[right]["description"]))
	})
	return services, reportedAt
}

func normalizeServicePayloadShape(raw sql.NullString) map[string]any {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return map[string]any{"services": []any{}, "reported_at": int64(0)}
	}
	var value any
	if err := json.Unmarshal([]byte(raw.String), &value); err != nil {
		return map[string]any{"services": []any{}, "reported_at": int64(0)}
	}
	switch typed := value.(type) {
	case []any:
		return map[string]any{"services": typed, "reported_at": int64(0)}
	case map[string]any:
		services, _ := typed["services"].([]any)
		return map[string]any{"services": services, "reported_at": coerceInt64(typed["reported_at"])}
	default:
		return map[string]any{"services": []any{}, "reported_at": int64(0)}
	}
}

func normalizeServiceEntry(item any, defaultCapturedAt int64) map[string]any {
	entry, ok := item.(map[string]any)
	if !ok {
		return nil
	}
	name := firstText(cleanText(entry["name"]), cleanText(entry["service_name"]), cleanText(entry["id"]))
	if name == "" {
		return nil
	}
	statusCode := normalizeServiceStatusCode(firstNonEmpty(entry["status_code"], entry["status"], entry["state"]))
	pendingAction := normalizeServiceActionValue(firstNonEmpty(entry["pending_action"], entry["action"]))
	desiredStatus := normalizeServiceStatusCode(firstNonEmpty(entry["desired_status"], desiredServiceStatus(pendingAction)))
	if desiredStatus != "running" && desiredStatus != "stopped" {
		desiredStatus = desiredServiceStatus(pendingAction)
	}
	capturedAt := coerceInt64(firstNonEmpty(entry["captured_at"], entry["reported_at"]))
	if capturedAt == 0 {
		capturedAt = defaultCapturedAt
	}
	return map[string]any{
		"service_id":           firstText(cleanText(entry["service_id"]), serviceIDForName(name)),
		"name":                 name,
		"display_name":         firstText(cleanText(entry["display_name"]), cleanText(entry["displayName"]), cleanText(entry["label"])),
		"description":          firstText(cleanText(entry["description"]), cleanText(entry["detail"])),
		"status_code":          statusCode,
		"status":               serviceStatusLabel(statusCode),
		"captured_at":          capturedAt,
		"pending_action":       pendingAction,
		"desired_status":       desiredStatus,
		"pending_requested_at": coerceInt64(entry["pending_requested_at"]),
		"pending_requested_by": cleanText(entry["pending_requested_by"]),
	}
}

func normalizeServiceStatusCode(value any) string {
	text := strings.ToLower(strings.TrimSpace(cleanText(value)))
	text = strings.ReplaceAll(strings.ReplaceAll(text, " ", "_"), "-", "_")
	aliases := map[string]string{
		"active":           "running",
		"running":          "running",
		"up":               "running",
		"online":           "running",
		"inactive":         "stopped",
		"stopped":          "stopped",
		"dead":             "stopped",
		"down":             "stopped",
		"disabled":         "stopped",
		"activating":       "starting",
		"start_pending":    "starting",
		"starting":         "starting",
		"reloading":        "starting",
		"deactivating":     "stopping",
		"stop_pending":     "stopping",
		"stopping":         "stopping",
		"paused":           "paused",
		"pause_pending":    "paused",
		"continue_pending": "starting",
		"failed":           "failed",
		"error":            "failed",
	}
	if normalized := aliases[text]; normalized != "" {
		return normalized
	}
	switch text {
	case "running", "stopped", "starting", "stopping", "paused", "failed", "unknown":
		return text
	default:
		return "unknown"
	}
}

func normalizeServiceActionValue(value any) string {
	switch strings.ToLower(strings.TrimSpace(cleanText(value))) {
	case "start", "stop", "restart":
		return strings.ToLower(strings.TrimSpace(cleanText(value)))
	default:
		return ""
	}
}

func desiredServiceStatus(action string) string {
	switch normalizeServiceActionValue(action) {
	case "start", "restart":
		return "running"
	case "stop":
		return "stopped"
	default:
		return ""
	}
}

func serviceStatusLabel(statusCode string) string {
	switch normalizeServiceStatusCode(statusCode) {
	case "running":
		return "Running"
	case "stopped":
		return "Stopped"
	case "starting":
		return "Starting"
	case "stopping":
		return "Stopping"
	case "paused":
		return "Paused"
	case "failed":
		return "Failed"
	default:
		return "Unknown"
	}
}

func serviceIDForName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func workerHostServiceRegistered(ctx context.Context, auth *authService, route *agentWorkerRoute, hostname string, serviceMode string) bool {
	if auth == nil || route == nil || cleanText(hostname) == "" {
		return false
	}
	target := workerInternalURL(route, "/remote-ops/host-service/status")
	if target == "" {
		return false
	}
	body, err := json.Marshal(map[string]any{
		"hostname":     cleanText(hostname),
		"service_mode": firstText(cleanText(serviceMode), "system"),
	})
	if err != nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(internalTokenHeader, goInternalToken(auth.verifier.secret))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return false
	}
	var payload map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return false
	}
	return boolFromAny(payload["registered"])
}

func workerInternalURL(route *agentWorkerRoute, path string) string {
	port := route.UpstreamPort
	if port <= 0 {
		return ""
	}
	scheme := firstText(cleanText(route.UpstreamScheme), "http")
	host := firstText(cleanText(route.UpstreamHost), "127.0.0.1")
	cleanPath := cleanText(path)
	if !strings.HasPrefix(cleanPath, "/") {
		cleanPath = "/" + cleanPath
	}
	return scheme + "://" + host + ":" + strconv.FormatInt(port, 10) + cleanPath
}

func goInternalToken(secret []byte) string {
	if len(secret) == 0 {
		return ""
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(internalTokenContext)
	return hex.EncodeToString(mac.Sum(nil))
}
