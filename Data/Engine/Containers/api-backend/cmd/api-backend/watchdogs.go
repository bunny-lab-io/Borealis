package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	watchdogDefaultEvalSeconds       = 60
	watchdogDefaultCooldownSeconds   = 900
	watchdogDefaultAutoResolve       = 300
	watchdogDefaultMinMatches        = 1
	watchdogDefaultBootGrace         = 0
	watchdogDefaultOfflineSeconds    = 300
	watchdogDefaultResourceThreshold = 90
	watchdogDefaultResourceDuration  = 300
	watchdogDefaultUptimeSeconds     = 2592000
	watchdogDefaultServicePending    = 600
)

type watchdogStore interface {
	listWatchdogs(ctx context.Context, profile operatorProfile, filter watchdogListFilter) ([]map[string]any, error)
	getWatchdog(ctx context.Context, profile operatorProfile, watchdogID int64) (map[string]any, bool, error)
	listWatchdogIncidents(ctx context.Context, profile operatorProfile, filter watchdogIncidentFilter) (map[string]any, error)
	acknowledgeWatchdogIncident(ctx context.Context, profile operatorProfile, incidentID int64) (map[string]any, bool, error)
	updateWatchdogIncidentState(ctx context.Context, profile operatorProfile, incidentID int64, state string, reason string) (map[string]any, []string, error)
	deleteWatchdog(ctx context.Context, profile operatorProfile, watchdogID int64) ([]string, bool, error)
}

type watchdogIncidentBroadcaster interface {
	broadcastWatchdogIncidents(ctx context.Context, payload map[string]any) error
}

type watchdogDeviceBroadcaster interface {
	broadcastDeviceWatchdogs(ctx context.Context, payload map[string]any) error
}

type watchdogListFilter struct {
	ArchivedSet bool
	Archived    bool
	SiteID      *int64
}

type watchdogIncidentFilter struct {
	State      string
	SiteID     *int64
	IncidentID *int64
}

type watchdogRow struct {
	ID                        sql.NullInt64
	Name                      sql.NullString
	Description               sql.NullString
	Archived                  sql.NullInt64
	Enabled                   sql.NullInt64
	Severity                  sql.NullString
	MatchMode                 sql.NullString
	SiteMode                  sql.NullString
	CriteriaJSON              sql.NullString
	ActionsJSON               sql.NullString
	EvaluationIntervalSeconds sql.NullInt64
	CooldownSeconds           sql.NullInt64
	AutoResolveAfterSeconds   sql.NullInt64
	MinConsecutiveMatches     sql.NullInt64
	BootGraceSeconds          sql.NullInt64
	LastEditedBy              sql.NullString
	CreatedAt                 sql.NullInt64
	UpdatedAt                 sql.NullInt64
	LastEvaluatedAt           sql.NullInt64
	TargetDeviceCount         sql.NullInt64
}

type watchdogIncidentRow struct {
	ID                  sql.NullInt64
	WatchdogID          sql.NullInt64
	DeviceGUID          sql.NullString
	Hostname            sql.NullString
	SiteID              sql.NullInt64
	Severity            sql.NullString
	State               sql.NullString
	Title               sql.NullString
	Message             sql.NullString
	SampleJSON          sql.NullString
	RuleSummaryJSON     sql.NullString
	ActionSummaryJSON   sql.NullString
	OpenedAt            sql.NullInt64
	UpdatedAt           sql.NullInt64
	ResolvedAt          sql.NullInt64
	ResolutionReason    sql.NullString
	AcknowledgedAt      sql.NullInt64
	AcknowledgedBy      sql.NullString
	WatchdogName        sql.NullString
	WatchdogDescription sql.NullString
}

func registerWatchdogRoutes(mux *http.ServeMux, auth *authService, fallback http.Handler, broadcaster watchdogIncidentBroadcaster) {
	mux.HandleFunc("/api/watchdogs", watchdogsRootHandler(auth, fallback))
	mux.HandleFunc("/api/watchdogs/", watchdogsSubtreeHandler(auth, fallback, broadcaster))
}

func watchdogsRootHandler(auth *authService, fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.TrimRight(r.URL.Path, "/") == "/api/watchdogs" {
			watchdogList(w, r, auth)
			return
		}
		fallback.ServeHTTP(w, r)
	}
}

func watchdogsSubtreeHandler(auth *authService, fallback http.Handler, broadcaster watchdogIncidentBroadcaster) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/watchdogs/"), "/"), "/")
		if len(parts) == 1 && parts[0] == "metadata" && r.Method == http.MethodGet {
			if _, failure := requireUser(r.Context(), auth, r); failure != nil {
				failure.write(w)
				return
			}
			writeJSON(w, http.StatusOK, watchdogMetadataPayload())
			return
		}
		if len(parts) == 1 && parts[0] == "incidents" && r.Method == http.MethodGet {
			watchdogIncidentsList(w, r, auth)
			return
		}
		if len(parts) == 3 && parts[0] == "incidents" && r.Method == http.MethodPost {
			switch parts[2] {
			case "acknowledge":
				watchdogIncidentAcknowledge(w, r, auth, broadcaster, parts[1])
				return
			case "state":
				watchdogIncidentStateUpdate(w, r, auth, broadcaster, parts[1])
				return
			}
		}
		if len(parts) == 1 && parts[0] != "" && r.Method == http.MethodGet {
			watchdogDetail(w, r, auth, parts[0])
			return
		}
		if len(parts) == 1 && parts[0] != "" && r.Method == http.MethodDelete {
			watchdogDelete(w, r, auth, broadcaster, parts[0])
			return
		}
		fallback.ServeHTTP(w, r)
	}
}

func watchdogList(w http.ResponseWriter, r *http.Request, auth *authService) {
	profile, store, ok := watchdogRequestContext(w, r, auth)
	if !ok {
		return
	}
	filter := watchdogListFilter{}
	if archivedRaw := strings.TrimSpace(r.URL.Query().Get("archived")); archivedRaw != "" {
		filter.ArchivedSet = true
		filter.Archived = parseBoolish(archivedRaw)
	}
	filter.SiteID = parseOptionalPositiveInt64(cleanText(firstNonEmpty(r.URL.Query().Get("site"), r.URL.Query().Get("site_id"))))
	ctx, cancel := watchdogTimeoutContext(r.Context(), auth)
	defer cancel()
	items, err := store.listWatchdogs(ctx, profile, filter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func watchdogDetail(w http.ResponseWriter, r *http.Request, auth *authService, watchdogIDText string) {
	profile, store, ok := watchdogRequestContext(w, r, auth)
	if !ok {
		return
	}
	watchdogID, err := parsePositivePathInt(watchdogIDText)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
		return
	}
	ctx, cancel := watchdogTimeoutContext(r.Context(), auth)
	defer cancel()
	item, found, err := store.getWatchdog(ctx, profile, watchdogID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func watchdogDelete(w http.ResponseWriter, r *http.Request, auth *authService, broadcaster watchdogIncidentBroadcaster, watchdogIDText string) {
	profile, store, ok := watchdogRequestContext(w, r, auth)
	if !ok {
		return
	}
	watchdogID, err := parsePositivePathInt(watchdogIDText)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
		return
	}
	ctx, cancel := watchdogTimeoutContext(r.Context(), auth)
	defer cancel()
	affectedHosts, found, err := store.deleteWatchdog(ctx, profile, watchdogID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
		return
	}
	for _, hostname := range affectedHosts {
		broadcastWatchdogRefresh(r.Context(), broadcaster, hostname, watchdogID)
	}
	broadcastWatchdogRefresh(r.Context(), broadcaster, "", watchdogID)
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted"})
}

func watchdogIncidentsList(w http.ResponseWriter, r *http.Request, auth *authService) {
	profile, store, ok := watchdogRequestContext(w, r, auth)
	if !ok {
		return
	}
	filter := watchdogIncidentFilter{
		State: strings.TrimSpace(r.URL.Query().Get("state")),
	}
	filter.SiteID = parseOptionalPositiveInt64(cleanText(firstNonEmpty(r.URL.Query().Get("site"), r.URL.Query().Get("site_id"))))
	ctx, cancel := watchdogTimeoutContext(r.Context(), auth)
	defer cancel()
	payload, err := store.listWatchdogIncidents(ctx, profile, filter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func watchdogIncidentAcknowledge(w http.ResponseWriter, r *http.Request, auth *authService, broadcaster watchdogIncidentBroadcaster, incidentIDText string) {
	profile, store, ok := watchdogRequestContext(w, r, auth)
	if !ok {
		return
	}
	incidentID, err := parsePositivePathInt(incidentIDText)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
		return
	}
	ctx, cancel := watchdogTimeoutContext(r.Context(), auth)
	defer cancel()
	incident, found, err := store.acknowledgeWatchdogIncident(ctx, profile, incidentID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
		return
	}
	broadcastWatchdogIncidentRefresh(r.Context(), broadcaster, incident)
	writeJSON(w, http.StatusOK, incident)
}

func watchdogIncidentStateUpdate(w http.ResponseWriter, r *http.Request, auth *authService, broadcaster watchdogIncidentBroadcaster, incidentIDText string) {
	profile, store, ok := watchdogRequestContext(w, r, auth)
	if !ok {
		return
	}
	incidentID, err := parsePositivePathInt(incidentIDText)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
		return
	}
	body := map[string]any{}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body)
	ctx, cancel := watchdogTimeoutContext(r.Context(), auth)
	defer cancel()
	incident, validationErrors, err := store.updateWatchdogIncidentState(
		ctx,
		profile,
		incidentID,
		firstText(cleanText(body["state"]), "open"),
		cleanText(body["reason"]),
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if len(validationErrors) > 0 {
		if len(validationErrors) == 1 && validationErrors[0] == "Incident not found." {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"errors": validationErrors})
		return
	}
	if incident == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
		return
	}
	broadcastWatchdogIncidentRefresh(r.Context(), broadcaster, incident)
	writeJSON(w, http.StatusOK, incident)
}

func broadcastWatchdogIncidentRefresh(ctx context.Context, broadcaster watchdogIncidentBroadcaster, incident map[string]any) {
	if broadcaster == nil || incident == nil {
		return
	}
	broadcastWatchdogRefresh(ctx, broadcaster, cleanText(incident["hostname"]), coerceInt64(incident["watchdog_id"]))
}

func broadcastWatchdogRefresh(ctx context.Context, broadcaster watchdogIncidentBroadcaster, hostname string, watchdogID int64) {
	if broadcaster == nil {
		return
	}
	payload := map[string]any{
		"hostname":    cleanText(hostname),
		"watchdog_id": watchdogID,
		"changed_at":  time.Now().Unix(),
	}
	if err := broadcaster.broadcastWatchdogIncidents(ctx, payload); err != nil {
		logDebug("watchdogs", "watchdog_incidents_changed broadcast failed: "+err.Error())
	}
	if payload["hostname"] == "" {
		return
	}
	deviceBroadcaster, ok := broadcaster.(watchdogDeviceBroadcaster)
	if !ok {
		return
	}
	if err := deviceBroadcaster.broadcastDeviceWatchdogs(ctx, payload); err != nil {
		logDebug("watchdogs", "device_watchdogs_changed broadcast failed: "+err.Error())
	}
}

func watchdogRequestContext(w http.ResponseWriter, r *http.Request, auth *authService) (operatorProfile, watchdogStore, bool) {
	profile, err := auth.currentProfile(r.Context(), r)
	if err != nil {
		if isUnauthorizedAuthError(err) {
			unauthorizedAuthFailure().write(w)
			return operatorProfile{}, nil, false
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
		return operatorProfile{}, nil, false
	}
	store, ok := auth.store.(watchdogStore)
	if !ok {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "watchdogs_unavailable"})
		return operatorProfile{}, nil, false
	}
	return profile, store, true
}

func watchdogTimeoutContext(ctx context.Context, auth *authService) (context.Context, context.CancelFunc) {
	timeout := auth.timeout
	if timeout <= 0 {
		timeout = defaultAuthTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

func watchdogMetadataPayload() map[string]any {
	return map[string]any{
		"site_modes": []map[string]any{
			{"value": "global", "label": "Global"},
			{"value": "specific_sites", "label": "Specific Sites"},
			{"value": "global_exclusions", "label": "Global w/ Exclusions"},
		},
		"match_modes": []map[string]any{
			{"value": "all", "label": "All Rules"},
			{"value": "any", "label": "Any Rule"},
		},
		"severities": []map[string]any{
			{"value": "info", "label": "Info"},
			{"value": "warning", "label": "Warning"},
			{"value": "error", "label": "Error"},
		},
		"rule_types": []map[string]any{
			{"value": "device_offline", "label": "Device Offline"},
			{"value": "storage_usage_percent", "label": "Storage Usage"},
			{"value": "service_state", "label": "Service State"},
			{"value": "agent_role_health", "label": "Agent Role Health"},
			{"value": "cpu_usage_percent", "label": "CPU Usage"},
			{"value": "memory_usage_percent", "label": "Memory Usage"},
			{"value": "uptime_above_seconds", "label": "Uptime"},
			{"value": "reboot_detected", "label": "Reboot Detected"},
			{"value": "service_pending_timeout", "label": "Service Pending Timeout"},
			{"value": "user_session_match", "label": "Logged-In User Match"},
			{"value": "process_presence", "label": "Process Presence"},
			{"value": "session_state", "label": "Session State"},
			{"value": "network_interface_change", "label": "Network Interface Change"},
			{"value": "drive_presence_change", "label": "Drive Presence Change"},
			{"value": "software_presence_or_version", "label": "Software Presence / Version"},
			{"value": "agent_version_status", "label": "Agent Version Status"},
		},
		"action_types": []map[string]any{
			{"value": "do_nothing", "label": "Do Nothing"},
			{"value": "notification", "label": "Engine Toast Notification"},
			{"value": "service_control", "label": "Control Service"},
			{"value": "assembly", "label": "Run Assembly"},
		},
	}
}

func (s *postgresOperatorStore) listWatchdogs(ctx context.Context, profile operatorProfile, filter watchdogListFilter) ([]map[string]any, error) {
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, err
	}
	if filter.SiteID != nil && !siteIDVisible(*filter.SiteID, allowedSiteIDs) {
		return []map[string]any{}, nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	rows, err := queryWatchdogRows(ctx, conn, filter)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, int64OrZero(row.ID))
	}
	allSiteIDs, siteLookup, targetLookup, openCounts, stateCounts, siteNames, err := loadWatchdogHydration(ctx, conn, ids)
	_ = conn.Close()
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		item := watchdogPayload(row)
		hydrateWatchdogPayload(item, allSiteIDs, siteLookup, targetLookup, openCounts, stateCounts, siteNames)
		if !watchdogVisibleToProfile(item, allSiteIDs, allowedSiteIDs) {
			continue
		}
		if filter.SiteID != nil && !watchdogAppliesToSite(item, allSiteIDs, *filter.SiteID) {
			continue
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *postgresOperatorStore) getWatchdog(ctx context.Context, profile operatorProfile, watchdogID int64) (map[string]any, bool, error) {
	items, err := s.listWatchdogs(ctx, profile, watchdogListFilter{})
	if err != nil {
		return nil, false, err
	}
	for _, item := range items {
		if coerceInt64(item["id"]) == watchdogID {
			return item, true, nil
		}
	}
	return nil, false, nil
}

func (s *postgresOperatorStore) deleteWatchdog(ctx context.Context, profile operatorProfile, watchdogID int64) ([]string, bool, error) {
	existing, found, err := s.getWatchdog(ctx, profile, watchdogID)
	if err != nil || !found {
		return nil, found, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, false, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	affectedHosts, err := loadWatchdogAffectedHosts(ctx, conn, coerceInt64(existing["id"]))
	if err != nil {
		return nil, false, err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer rollbackQuietly(tx)
	for _, statement := range []string{
		"DELETE FROM engine.watchdog_targets WHERE watchdog_id=$1",
		"DELETE FROM engine.watchdog_sites WHERE watchdog_id=$1",
		"DELETE FROM engine.watchdog_device_overrides WHERE watchdog_id=$1",
		"DELETE FROM engine.watchdog_device_state WHERE watchdog_id=$1",
		"DELETE FROM engine.watchdog_incidents WHERE watchdog_id=$1",
	} {
		if _, err := tx.ExecContext(ctx, statement, watchdogID); err != nil {
			return nil, false, err
		}
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM engine.watchdogs WHERE id=$1", watchdogID)
	if err != nil {
		return nil, false, err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return nil, false, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return affectedHosts, true, nil
}

func (s *postgresOperatorStore) listWatchdogIncidents(ctx context.Context, profile operatorProfile, filter watchdogIncidentFilter) (map[string]any, error) {
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, err
	}
	if filter.SiteID != nil && !siteIDVisible(*filter.SiteID, allowedSiteIDs) {
		return map[string]any{"items": []map[string]any{}, "counts": watchdogEmptyIncidentCounts()}, nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	visibleWatchdogIDs, err := visibleWatchdogIDsForProfile(ctx, conn, allowedSiteIDs)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if visibleWatchdogIDs != nil && len(visibleWatchdogIDs) == 0 {
		_ = conn.Close()
		return map[string]any{"items": []map[string]any{}, "counts": watchdogEmptyIncidentCounts()}, nil
	}
	items, err := queryWatchdogIncidents(ctx, conn, filter, visibleWatchdogIDs)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	counts, err := queryWatchdogIncidentCounts(ctx, conn, filter.SiteID, visibleWatchdogIDs)
	_ = conn.Close()
	if err != nil {
		return nil, err
	}
	return map[string]any{"items": items, "counts": counts}, nil
}

func (s *postgresOperatorStore) acknowledgeWatchdogIncident(ctx context.Context, profile operatorProfile, incidentID int64) (map[string]any, bool, error) {
	_, found, err := s.findVisibleWatchdogIncident(ctx, profile, incidentID, "open")
	if err != nil || !found {
		return nil, found, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, false, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	now := time.Now().Unix()
	username := firstText(cleanText(profile.Username), "Unknown")
	result, err := conn.ExecContext(ctx, `
		UPDATE engine.watchdog_incidents
		   SET acknowledged_at=$1,
		       acknowledged_by=$2,
		       updated_at=$1
		 WHERE id=$3
	`, now, username, incidentID)
	if err != nil {
		return nil, false, err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return nil, false, nil
	}
	refreshed, refreshedFound, err := s.findVisibleWatchdogIncident(ctx, profile, incidentID, "open")
	if err != nil {
		return nil, false, err
	}
	if refreshedFound {
		return refreshed, true, nil
	}
	return nil, false, nil
}

func (s *postgresOperatorStore) updateWatchdogIncidentState(ctx context.Context, profile operatorProfile, incidentID int64, state string, reason string) (map[string]any, []string, error) {
	desiredState := normalizeIncidentMutationState(state)
	if desiredState == "" {
		return nil, []string{"Unsupported incident state transition."}, nil
	}
	target, found, err := s.findVisibleWatchdogIncident(ctx, profile, incidentID, "all")
	if err != nil {
		return nil, nil, err
	}
	if !found {
		return nil, []string{"Incident not found."}, nil
	}
	currentState := strings.ToLower(cleanText(target["state"]))
	if currentState == "resolved" {
		return nil, []string{"Resolved incidents are historical records and cannot be reopened."}, nil
	}
	if currentState == desiredState {
		return target, nil, nil
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer rollbackQuietly(tx)

	watchdogID := coerceInt64(target["watchdog_id"])
	hostname := cleanText(target["hostname"])
	now := time.Now().Unix()
	if desiredState == "open" {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM engine.watchdog_device_overrides
			 WHERE watchdog_id=$1 AND LOWER(hostname)=LOWER($2)
		`, watchdogID, hostname); err != nil {
			return nil, nil, err
		}
	}
	resolutionReason := ""
	if desiredState == "suppressed" {
		resolutionReason = cleanSingleLine(reason)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE engine.watchdog_incidents
		   SET state=$1,
		       resolved_at=NULL,
		       resolution_reason=$2,
		       updated_at=$3
		 WHERE id=$4
	`, desiredState, resolutionReason, now, incidentID); err != nil {
		return nil, nil, err
	}

	nextDeviceState := "triggered"
	if desiredState == "suppressed" {
		nextDeviceState = "suppressed"
	}
	if desiredState == "suppressed" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE engine.watchdog_device_state
			   SET state=$1,
			       last_evaluated_at=GREATEST(COALESCE(last_evaluated_at, 0), $2),
			       current_incident_id=$3,
			       updated_at=$2
			 WHERE watchdog_id=$4 AND LOWER(hostname)=LOWER($5)
		`, nextDeviceState, now, incidentID, watchdogID, hostname); err != nil {
			return nil, nil, err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `
			UPDATE engine.watchdog_device_state
			   SET state=$1,
			       clear_started_at=$2,
			       last_evaluated_at=GREATEST(COALESCE(last_evaluated_at, 0), $3),
			       current_incident_id=$4,
			       updated_at=$3
			 WHERE watchdog_id=$5 AND LOWER(hostname)=LOWER($6)
		`, nextDeviceState, nil, now, incidentID, watchdogID, hostname); err != nil {
			return nil, nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}

	refreshed, refreshedFound, err := s.findVisibleWatchdogIncident(ctx, profile, incidentID, desiredState)
	if err != nil {
		return nil, nil, err
	}
	if !refreshedFound {
		return nil, nil, nil
	}
	return refreshed, nil, nil
}

func (s *postgresOperatorStore) findVisibleWatchdogIncident(ctx context.Context, profile operatorProfile, incidentID int64, state string) (map[string]any, bool, error) {
	payload, err := s.listWatchdogIncidents(ctx, profile, watchdogIncidentFilter{
		State:      state,
		IncidentID: &incidentID,
	})
	if err != nil {
		return nil, false, err
	}
	items, _ := payload["items"].([]map[string]any)
	if len(items) > 0 {
		return items[0], true, nil
	}
	rawItems, _ := payload["items"].([]any)
	for _, item := range rawItems {
		if typed, ok := item.(map[string]any); ok {
			return typed, true, nil
		}
	}
	return nil, false, nil
}

func queryWatchdogRows(ctx context.Context, conn *sql.Conn, filter watchdogListFilter) ([]watchdogRow, error) {
	clauses := []string{}
	params := []any{}
	if filter.ArchivedSet {
		params = append(params, boolInt(filter.Archived))
		clauses = append(clauses, fmt.Sprintf("COALESCE(archived, 0) = $%d", len(params)))
	}
	whereSQL := ""
	if len(clauses) > 0 {
		whereSQL = "WHERE " + strings.Join(clauses, " AND ")
	}
	rows, err := conn.QueryContext(ctx, `
		SELECT
			id, name, description, archived, enabled, severity, match_mode, site_mode,
			criteria_json, actions_json, evaluation_interval_seconds, cooldown_seconds,
			auto_resolve_after_seconds, min_consecutive_matches, boot_grace_seconds,
			last_edited_by, created_at, updated_at, last_evaluated_at, target_device_count
		  FROM engine.watchdogs
		  `+whereSQL+`
	  ORDER BY COALESCE(updated_at, created_at, 0) DESC, id DESC
	`, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := []watchdogRow{}
	for rows.Next() {
		var row watchdogRow
		if err := rows.Scan(
			&row.ID,
			&row.Name,
			&row.Description,
			&row.Archived,
			&row.Enabled,
			&row.Severity,
			&row.MatchMode,
			&row.SiteMode,
			&row.CriteriaJSON,
			&row.ActionsJSON,
			&row.EvaluationIntervalSeconds,
			&row.CooldownSeconds,
			&row.AutoResolveAfterSeconds,
			&row.MinConsecutiveMatches,
			&row.BootGraceSeconds,
			&row.LastEditedBy,
			&row.CreatedAt,
			&row.UpdatedAt,
			&row.LastEvaluatedAt,
			&row.TargetDeviceCount,
		); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	return results, rows.Err()
}

func loadWatchdogHydration(ctx context.Context, conn *sql.Conn, ids []int64) (map[int64]struct{}, map[int64][]int64, map[int64][]any, map[int64]int64, map[int64]map[string]int64, map[int64]string, error) {
	allSiteIDs, err := loadAllSiteIDs(ctx, conn)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	if len(ids) == 0 {
		return allSiteIDs, map[int64][]int64{}, map[int64][]any{}, map[int64]int64{}, map[int64]map[string]int64{}, map[int64]string{}, nil
	}
	siteLookup, err := loadWatchdogSites(ctx, conn, ids)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	targetLookup, err := loadWatchdogTargets(ctx, conn, ids)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	openCounts, err := loadWatchdogOpenIncidentCounts(ctx, conn, ids)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	stateCounts, err := loadWatchdogStateCounts(ctx, conn, ids)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	siteIDs := []int64{}
	for _, values := range siteLookup {
		siteIDs = append(siteIDs, values...)
	}
	siteNames, err := loadSiteNamesByID(ctx, conn, siteIDs)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	return allSiteIDs, siteLookup, targetLookup, openCounts, stateCounts, siteNames, nil
}

func loadWatchdogAffectedHosts(ctx context.Context, conn *sql.Conn, watchdogID int64) ([]string, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT hostname
		  FROM engine.watchdog_device_state
		 WHERE watchdog_id=$1
		UNION
		SELECT hostname
		  FROM engine.watchdog_incidents
		 WHERE watchdog_id=$1
		   AND state IN ('open', 'suppressed')
	  ORDER BY hostname ASC
	`, watchdogID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hosts := []string{}
	seen := map[string]struct{}{}
	for rows.Next() {
		var hostname sql.NullString
		if err := rows.Scan(&hostname); err != nil {
			return nil, err
		}
		cleaned := strings.TrimSpace(nullString(hostname))
		if cleaned == "" {
			continue
		}
		key := strings.ToLower(cleaned)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		hosts = append(hosts, cleaned)
	}
	return hosts, rows.Err()
}

func loadAllSiteIDs(ctx context.Context, conn *sql.Conn) (map[int64]struct{}, error) {
	rows, err := conn.QueryContext(ctx, "SELECT id FROM engine.sites")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[int64]struct{}{}
	for rows.Next() {
		var siteID sql.NullInt64
		if err := rows.Scan(&siteID); err != nil {
			return nil, err
		}
		if siteID.Valid {
			result[siteID.Int64] = struct{}{}
		}
	}
	return result, rows.Err()
}

func loadWatchdogSites(ctx context.Context, conn *sql.Conn, ids []int64) (map[int64][]int64, error) {
	if len(ids) == 0 {
		return map[int64][]int64{}, nil
	}
	query, params := inClauseQuery("SELECT watchdog_id, site_id FROM engine.watchdog_sites WHERE watchdog_id IN (%s) ORDER BY site_id ASC", ids)
	rows, err := conn.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[int64][]int64{}
	for rows.Next() {
		var watchdogID, siteID sql.NullInt64
		if err := rows.Scan(&watchdogID, &siteID); err != nil {
			return nil, err
		}
		if watchdogID.Valid && siteID.Valid {
			result[watchdogID.Int64] = append(result[watchdogID.Int64], siteID.Int64)
		}
	}
	return result, rows.Err()
}

func loadWatchdogTargets(ctx context.Context, conn *sql.Conn, ids []int64) (map[int64][]any, error) {
	if len(ids) == 0 {
		return map[int64][]any{}, nil
	}
	query, params := inClauseQuery("SELECT watchdog_id, kind, target_json FROM engine.watchdog_targets WHERE watchdog_id IN (%s) ORDER BY id ASC", ids)
	rows, err := conn.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[int64][]any{}
	for rows.Next() {
		var watchdogID sql.NullInt64
		var kind sql.NullString
		var targetJSON sql.NullString
		if err := rows.Scan(&watchdogID, &kind, &targetJSON); err != nil {
			return nil, err
		}
		if !watchdogID.Valid {
			continue
		}
		target := parseJSONObject(targetJSON)
		targetKind := strings.ToLower(strings.TrimSpace(nullString(kind)))
		if targetKind == "" {
			targetKind = strings.ToLower(strings.TrimSpace(cleanText(target["kind"])))
		}
		target["kind"] = targetKind
		result[watchdogID.Int64] = append(result[watchdogID.Int64], target)
	}
	return result, rows.Err()
}

func loadWatchdogOpenIncidentCounts(ctx context.Context, conn *sql.Conn, ids []int64) (map[int64]int64, error) {
	if len(ids) == 0 {
		return map[int64]int64{}, nil
	}
	query, params := inClauseQuery("SELECT watchdog_id, COUNT(*) FROM engine.watchdog_incidents WHERE watchdog_id IN (%s) AND state = 'open' GROUP BY watchdog_id", ids)
	rows, err := conn.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[int64]int64{}
	for rows.Next() {
		var watchdogID, count sql.NullInt64
		if err := rows.Scan(&watchdogID, &count); err != nil {
			return nil, err
		}
		if watchdogID.Valid {
			result[watchdogID.Int64] = int64OrZero(count)
		}
	}
	return result, rows.Err()
}

func loadWatchdogStateCounts(ctx context.Context, conn *sql.Conn, ids []int64) (map[int64]map[string]int64, error) {
	if len(ids) == 0 {
		return map[int64]map[string]int64{}, nil
	}
	query, params := inClauseQuery("SELECT watchdog_id, state, COUNT(*) FROM engine.watchdog_device_state WHERE watchdog_id IN (%s) GROUP BY watchdog_id, state", ids)
	rows, err := conn.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[int64]map[string]int64{}
	for rows.Next() {
		var watchdogID, count sql.NullInt64
		var state sql.NullString
		if err := rows.Scan(&watchdogID, &state, &count); err != nil {
			return nil, err
		}
		if !watchdogID.Valid {
			continue
		}
		result[watchdogID.Int64] = ensureStringInt64Map(result[watchdogID.Int64])
		result[watchdogID.Int64][strings.ToLower(nullString(state))] = int64OrZero(count)
	}
	return result, rows.Err()
}

func loadSiteNamesByID(ctx context.Context, conn *sql.Conn, ids []int64) (map[int64]string, error) {
	if len(ids) == 0 {
		return map[int64]string{}, nil
	}
	query, params := inClauseQuery("SELECT id, name FROM engine.sites WHERE id IN (%s) ORDER BY id ASC", ids)
	rows, err := conn.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[int64]string{}
	for rows.Next() {
		var siteID sql.NullInt64
		var name sql.NullString
		if err := rows.Scan(&siteID, &name); err != nil {
			return nil, err
		}
		if siteID.Valid {
			result[siteID.Int64] = nullString(name)
		}
	}
	return result, rows.Err()
}

func queryWatchdogIncidents(ctx context.Context, conn *sql.Conn, filter watchdogIncidentFilter, visibleWatchdogIDs []int64) ([]map[string]any, error) {
	state := normalizeIncidentQueryState(filter.State)
	clauses := []string{}
	params := []any{}
	if state != "all" {
		params = append(params, state)
		clauses = append(clauses, fmt.Sprintf("i.state = $%d", len(params)))
	}
	if filter.SiteID != nil {
		params = append(params, *filter.SiteID)
		clauses = append(clauses, fmt.Sprintf("i.site_id = $%d", len(params)))
	}
	if filter.IncidentID != nil {
		params = append(params, *filter.IncidentID)
		clauses = append(clauses, fmt.Sprintf("i.id = $%d", len(params)))
	}
	if visibleWatchdogIDs != nil {
		placeholders := []string{}
		for _, id := range visibleWatchdogIDs {
			params = append(params, id)
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(params)))
		}
		clauses = append(clauses, "i.watchdog_id IN ("+strings.Join(placeholders, ",")+")")
	}
	whereSQL := ""
	if len(clauses) > 0 {
		whereSQL = " WHERE " + strings.Join(clauses, " AND ")
	}
	rows, err := conn.QueryContext(ctx, `
		SELECT
			i.id, i.watchdog_id, i.device_guid, i.hostname, i.site_id, i.severity, i.state,
			i.title, i.message, i.sample_json, i.rule_summary_json, i.action_summary_json,
			i.opened_at, i.updated_at, i.resolved_at, i.resolution_reason,
			i.acknowledged_at, i.acknowledged_by, w.name, w.description
		  FROM engine.watchdog_incidents AS i
		  JOIN engine.watchdogs AS w ON w.id = i.watchdog_id
		  `+whereSQL+`
	  ORDER BY i.updated_at DESC, i.id DESC
	`, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rawRows := []watchdogIncidentRow{}
	siteIDs := []int64{}
	for rows.Next() {
		var row watchdogIncidentRow
		if err := rows.Scan(
			&row.ID,
			&row.WatchdogID,
			&row.DeviceGUID,
			&row.Hostname,
			&row.SiteID,
			&row.Severity,
			&row.State,
			&row.Title,
			&row.Message,
			&row.SampleJSON,
			&row.RuleSummaryJSON,
			&row.ActionSummaryJSON,
			&row.OpenedAt,
			&row.UpdatedAt,
			&row.ResolvedAt,
			&row.ResolutionReason,
			&row.AcknowledgedAt,
			&row.AcknowledgedBy,
			&row.WatchdogName,
			&row.WatchdogDescription,
		); err != nil {
			return nil, err
		}
		rawRows = append(rawRows, row)
		if row.SiteID.Valid {
			siteIDs = append(siteIDs, row.SiteID.Int64)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	siteNames, err := loadSiteNamesByID(ctx, conn, siteIDs)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(rawRows))
	for _, row := range rawRows {
		items = append(items, watchdogIncidentPayload(row, siteNames))
	}
	return items, nil
}

func queryWatchdogIncidentCounts(ctx context.Context, conn *sql.Conn, siteID *int64, visibleWatchdogIDs []int64) (map[string]int64, error) {
	counts := watchdogEmptyIncidentCounts()
	clauses := []string{}
	params := []any{}
	if siteID != nil {
		params = append(params, *siteID)
		clauses = append(clauses, fmt.Sprintf("site_id = $%d", len(params)))
	}
	if visibleWatchdogIDs != nil {
		placeholders := []string{}
		for _, id := range visibleWatchdogIDs {
			params = append(params, id)
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(params)))
		}
		clauses = append(clauses, "watchdog_id IN ("+strings.Join(placeholders, ",")+")")
	}
	whereSQL := ""
	if len(clauses) > 0 {
		whereSQL = " WHERE " + strings.Join(clauses, " AND ")
	}
	rows, err := conn.QueryContext(ctx, "SELECT state, COUNT(*) FROM engine.watchdog_incidents "+whereSQL+" GROUP BY state", params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var state sql.NullString
		var count sql.NullInt64
		if err := rows.Scan(&state, &count); err != nil {
			return nil, err
		}
		stateText := strings.ToLower(nullString(state))
		if _, ok := counts[stateText]; ok {
			counts[stateText] = int64OrZero(count)
		}
	}
	return counts, rows.Err()
}

func visibleWatchdogIDsForProfile(ctx context.Context, conn *sql.Conn, allowedSiteIDs []int64) ([]int64, error) {
	if allowedSiteIDs == nil {
		return nil, nil
	}
	rows, err := conn.QueryContext(ctx, "SELECT id, site_mode FROM engine.watchdogs")
	if err != nil {
		return nil, err
	}
	rawRows := []struct {
		id       int64
		siteMode string
	}{}
	ids := []int64{}
	for rows.Next() {
		var id sql.NullInt64
		var siteMode sql.NullString
		if err := rows.Scan(&id, &siteMode); err != nil {
			rows.Close()
			return nil, err
		}
		if id.Valid {
			rawRows = append(rawRows, struct {
				id       int64
				siteMode string
			}{id: id.Int64, siteMode: normalizeWatchdogSiteMode(nullString(siteMode))})
			ids = append(ids, id.Int64)
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []int64{}, nil
	}
	allSiteIDs, err := loadAllSiteIDs(ctx, conn)
	if err != nil {
		return nil, err
	}
	siteLookup, err := loadWatchdogSites(ctx, conn, ids)
	if err != nil {
		return nil, err
	}
	visible := []int64{}
	for _, row := range rawRows {
		item := map[string]any{
			"id":        row.id,
			"site_mode": row.siteMode,
			"site_ids":  siteLookup[row.id],
		}
		if watchdogVisibleToProfile(item, allSiteIDs, allowedSiteIDs) {
			visible = append(visible, row.id)
		}
	}
	return visible, nil
}

func watchdogPayload(row watchdogRow) map[string]any {
	criteria := parseJSONObject(row.CriteriaJSON)
	if len(criteria) == 0 {
		criteria = map[string]any{"rules": []any{}, "match_mode": "all"}
	}
	actions := parseJSONObject(row.ActionsJSON)
	if len(actions) == 0 {
		actions = map[string]any{"actions": []any{}}
	}
	return map[string]any{
		"id":                          int64OrZero(row.ID),
		"name":                        cleanSingleLine(nullString(row.Name)),
		"description":                 cleanSingleLine(nullString(row.Description)),
		"archived":                    int64OrZero(row.Archived) != 0,
		"enabled":                     int64OrZero(row.Enabled) != 0,
		"severity":                    normalizeWatchdogSeverity(nullString(row.Severity)),
		"match_mode":                  normalizeWatchdogMatchMode(nullString(row.MatchMode)),
		"site_mode":                   normalizeWatchdogSiteMode(nullString(row.SiteMode)),
		"criteria":                    criteria,
		"actions":                     actions,
		"evaluation_interval_seconds": int64WithDefault(row.EvaluationIntervalSeconds, watchdogDefaultEvalSeconds),
		"cooldown_seconds":            int64WithDefault(row.CooldownSeconds, watchdogDefaultCooldownSeconds),
		"auto_resolve_after_seconds":  int64WithDefault(row.AutoResolveAfterSeconds, watchdogDefaultAutoResolve),
		"min_consecutive_matches":     int64WithDefault(row.MinConsecutiveMatches, watchdogDefaultMinMatches),
		"boot_grace_seconds":          int64WithDefault(row.BootGraceSeconds, watchdogDefaultBootGrace),
		"last_edited_by":              strings.TrimSpace(nullString(row.LastEditedBy)),
		"created_at":                  int64OrZero(row.CreatedAt),
		"updated_at":                  int64OrZero(row.UpdatedAt),
		"last_evaluated_at":           nullableInt(row.LastEvaluatedAt),
		"target_device_count":         int64OrZero(row.TargetDeviceCount),
		"site_ids":                    []int64{},
		"site_names":                  []string{},
		"targets":                     []any{},
	}
}

func hydrateWatchdogPayload(item map[string]any, allSiteIDs map[int64]struct{}, siteLookup map[int64][]int64, targetLookup map[int64][]any, openCounts map[int64]int64, stateCounts map[int64]map[string]int64, siteNames map[int64]string) {
	id := coerceInt64(item["id"])
	siteIDs := append([]int64(nil), siteLookup[id]...)
	item["site_ids"] = siteIDs
	names := []string{}
	for _, siteID := range siteIDs {
		name := siteNames[siteID]
		if name == "" {
			name = fmt.Sprintf("Site %d", siteID)
		}
		names = append(names, name)
	}
	item["site_names"] = names
	item["targets"] = append([]any(nil), targetLookup[id]...)
	item["open_incident_count"] = openCounts[id]
	stateMap := map[string]int64{}
	for key, value := range stateCounts[id] {
		stateMap[key] = value
	}
	item["state_counts"] = stateMap
	item["rule_summaries"] = watchdogRuleSummaries(asStringAnyMap(item["criteria"]))
	item["action_summaries"] = watchdogActionSummaries(asStringAnyMap(item["actions"]))
	_ = allSiteIDs
}

func watchdogIncidentPayload(row watchdogIncidentRow, siteNames map[int64]string) map[string]any {
	siteID := nullableInt(row.SiteID)
	payload := map[string]any{
		"id":                   int64OrZero(row.ID),
		"watchdog_id":          int64OrZero(row.WatchdogID),
		"device_guid":          normalizeCanonicalGUID(nullString(row.DeviceGUID)),
		"hostname":             strings.TrimSpace(nullString(row.Hostname)),
		"site_id":              siteID,
		"severity":             normalizeWatchdogSeverity(nullString(row.Severity)),
		"state":                strings.ToLower(strings.TrimSpace(nullString(row.State))),
		"title":                strings.TrimSpace(nullString(row.Title)),
		"message":              strings.TrimSpace(nullString(row.Message)),
		"sample":               parseJSONObject(row.SampleJSON),
		"rule_summary":         parseJSONArray(row.RuleSummaryJSON),
		"action_summary":       parseJSONArray(row.ActionSummaryJSON),
		"opened_at":            int64OrZero(row.OpenedAt),
		"updated_at":           int64OrZero(row.UpdatedAt),
		"resolved_at":          nullableInt(row.ResolvedAt),
		"resolution_reason":    strings.TrimSpace(nullString(row.ResolutionReason)),
		"acknowledged_at":      nullableInt(row.AcknowledgedAt),
		"acknowledged_by":      strings.TrimSpace(nullString(row.AcknowledgedBy)),
		"watchdog_name":        strings.TrimSpace(nullString(row.WatchdogName)),
		"watchdog_description": strings.TrimSpace(nullString(row.WatchdogDescription)),
		"site_name":            "",
	}
	if row.SiteID.Valid {
		payload["site_name"] = siteNames[row.SiteID.Int64]
	}
	return payload
}

func watchdogVisibleToProfile(item map[string]any, allSiteIDs map[int64]struct{}, allowedSiteIDs []int64) bool {
	if allowedSiteIDs == nil {
		return true
	}
	effective := effectiveWatchdogSiteIDs(item, allSiteIDs)
	allowed := int64Set(allowedSiteIDs)
	for siteID := range effective {
		if _, ok := allowed[siteID]; !ok {
			return false
		}
	}
	return true
}

func watchdogAppliesToSite(item map[string]any, allSiteIDs map[int64]struct{}, siteID int64) bool {
	_, ok := effectiveWatchdogSiteIDs(item, allSiteIDs)[siteID]
	return ok
}

func effectiveWatchdogSiteIDs(item map[string]any, allSiteIDs map[int64]struct{}) map[int64]struct{} {
	mode := normalizeWatchdogSiteMode(cleanText(item["site_mode"]))
	configured := map[int64]struct{}{}
	for _, siteID := range coerceInt64Slice(item["site_ids"]) {
		configured[siteID] = struct{}{}
	}
	if mode == "specific_sites" {
		return configured
	}
	if mode == "global_exclusions" {
		result := map[int64]struct{}{}
		for siteID := range allSiteIDs {
			if _, excluded := configured[siteID]; !excluded {
				result[siteID] = struct{}{}
			}
		}
		return result
	}
	result := map[int64]struct{}{}
	for siteID := range allSiteIDs {
		result[siteID] = struct{}{}
	}
	return result
}

func siteIDVisible(siteID int64, allowedSiteIDs []int64) bool {
	if allowedSiteIDs == nil {
		return true
	}
	for _, allowed := range allowedSiteIDs {
		if allowed == siteID {
			return true
		}
	}
	return false
}

func normalizeWatchdogSeverity(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "info", "warning", "error":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "warning"
	}
}

func normalizeWatchdogMatchMode(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), "any") {
		return "any"
	}
	return "all"
}

func normalizeWatchdogSiteMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "specific_sites", "global_exclusions":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "global"
	}
}

func normalizeIncidentQueryState(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "open", "suppressed", "resolved", "all":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "open"
	}
}

func normalizeIncidentMutationState(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "open", "suppressed":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func watchdogEmptyIncidentCounts() map[string]int64 {
	return map[string]int64{"open": 0, "suppressed": 0, "resolved": 0}
}

func parseBoolish(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func parseOptionalPositiveInt64(value string) *int64 {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed <= 0 {
		return nil
	}
	return &parsed
}

func int64WithDefault(value sql.NullInt64, fallback int64) int64 {
	if value.Valid {
		return value.Int64
	}
	return fallback
}

func ensureStringInt64Map(value map[string]int64) map[string]int64 {
	if value != nil {
		return value
	}
	return map[string]int64{}
}

func asStringAnyMap(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return map[string]any{}
}

func coerceInt64Slice(value any) []int64 {
	result := []int64{}
	switch typed := value.(type) {
	case []int64:
		return append(result, typed...)
	case []any:
		for _, item := range typed {
			if parsed := coerceInt64(item); parsed != 0 {
				result = append(result, parsed)
			}
		}
	case []map[string]any:
		for _, item := range typed {
			if parsed := coerceInt64(item["id"]); parsed != 0 {
				result = append(result, parsed)
			}
		}
	}
	return result
}

func cleanSingleLine(value string) string {
	parts := strings.Fields(strings.ReplaceAll(strings.ReplaceAll(value, "\r", " "), "\n", " "))
	return strings.TrimSpace(strings.Join(parts, " "))
}

func watchdogRuleSummaries(criteria map[string]any) []string {
	rules, _ := criteria["rules"].([]any)
	summaries := []string{}
	for _, entry := range rules {
		rule, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		summaries = append(summaries, summarizeWatchdogRule(rule))
	}
	return summaries
}

func watchdogActionSummaries(actions map[string]any) []string {
	entries, _ := actions["actions"].([]any)
	summaries := []string{}
	for _, entry := range entries {
		action, ok := entry.(map[string]any)
		if !ok || !boolDefault(action["enabled"], true) {
			continue
		}
		summaries = append(summaries, summarizeWatchdogAction(action))
	}
	return summaries
}

func summarizeWatchdogRule(rule map[string]any) string {
	ruleType := strings.ToLower(strings.TrimSpace(cleanText(rule["type"])))
	switch ruleType {
	case "device_offline":
		return fmt.Sprintf("Device offline for at least %d minute(s)", maxInt(fallbackInt(rule["offline_after_seconds"], watchdogDefaultOfflineSeconds)/60, 1))
	case "storage_usage_percent":
		drive := strings.TrimSpace(cleanText(rule["drive"]))
		prefix := "Any drive "
		if drive != "" {
			prefix = drive + " "
		}
		threshold := coerceInt64(rule["threshold"])
		if threshold <= 0 {
			threshold = watchdogDefaultResourceThreshold
		}
		return fmt.Sprintf("%susage at or above %d%%", prefix, threshold)
	case "service_state":
		return fmt.Sprintf("Service %s not %s", fallbackText(cleanText(rule["service_name"]), "service"), fallbackText(cleanText(rule["expected_status"]), "running"))
	case "agent_role_health":
		return fmt.Sprintf("%s health enters unhealthy", fallbackText(cleanText(rule["role_name"]), "Any role"))
	case "cpu_usage_percent":
		return fmt.Sprintf("CPU usage at or above %d%% for %d second(s)", fallbackInt(rule["threshold"], watchdogDefaultResourceThreshold), fallbackInt(rule["duration_seconds"], watchdogDefaultResourceDuration))
	case "memory_usage_percent":
		return fmt.Sprintf("Memory usage at or above %d%% for %d second(s)", fallbackInt(rule["threshold"], watchdogDefaultResourceThreshold), fallbackInt(rule["duration_seconds"], watchdogDefaultResourceDuration))
	case "uptime_above_seconds":
		return fmt.Sprintf("Device uptime above %d second(s)", maxInt(fallbackInt(rule["threshold_seconds"], watchdogDefaultUptimeSeconds), 60))
	case "reboot_detected":
		return "Device reboot detected"
	case "service_pending_timeout":
		return fmt.Sprintf("%s pending action for at least %d second(s)", fallbackText(cleanText(rule["service_name"]), "Any service"), fallbackInt(rule["timeout_seconds"], watchdogDefaultServicePending))
	case "user_session_match":
		return "Logged-in user blocklist match (normalized)"
	case "process_presence":
		expectation := strings.ToLower(cleanText(rule["expectation"]))
		if expectation == "missing" {
			return fmt.Sprintf("Process %s missing", cleanText(rule["process_name"]))
		}
		return fmt.Sprintf("Process %s present", cleanText(rule["process_name"]))
	case "session_state":
		return "Session state detected"
	case "network_interface_change":
		return "Network interface topology changed"
	case "drive_presence_change":
		return "All storage topology changed"
	case "software_presence_or_version":
		name := fallbackText(cleanText(rule["software_name"]), "Software")
		versionOperator := cleanText(rule["version_operator"])
		versionValue := cleanText(rule["version_value"])
		if versionOperator != "" && versionValue != "" {
			return fmt.Sprintf("%s version %s %s", name, strings.ReplaceAll(versionOperator, "_", " "), versionValue)
		}
		return name + " missing"
	case "agent_version_status":
		return fmt.Sprintf("Agent version is not %s", fallbackText(cleanText(rule["expected_status"]), "Up-to-Date"))
	default:
		return "Watchdog rule"
	}
}

func summarizeWatchdogAction(action map[string]any) string {
	actionType := strings.ToLower(cleanText(action["type"]))
	switch actionType {
	case "notification":
		return "Engine toast notification"
	case "do_nothing":
		return "Incident only (no notification or remediation)"
	case "service_control":
		return fmt.Sprintf("%s service %s", titleWord(cleanText(action["action"])), cleanText(action["service_name"]))
	case "assembly":
		return "Run assembly remediation"
	default:
		return "Action"
	}
}

func fallbackText(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	return fallback
}

func fallbackInt(value any, fallback int) int {
	parsed := int(coerceInt64(value))
	if parsed > 0 {
		return parsed
	}
	return fallback
}

func boolDefault(value any, fallback bool) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case int:
		return typed != 0
	case int64:
		return typed != 0
	case float64:
		return typed != 0
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return fallback
}

func titleWord(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + strings.ToLower(value[1:])
}
