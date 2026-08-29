package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func registerAgentVPNRuntimeRoutes(mux *http.ServeMux, auth *authService, vpnRuntime *vpnTunnelService, vncRuntime *vncRuntime) error {
	signer, err := loadOrCreateAgentJWTSigner()
	if err != nil {
		return err
	}
	dpop := &dpopVerifier{seenJTI: map[string]time.Time{}}
	mux.HandleFunc("POST /api/agent/vpn/ensure", agentVPNTunnelEnsureHandler(auth, signer, dpop, vpnRuntime, vncRuntime))
	mux.HandleFunc("POST /api/agent/vpn/ready", agentVPNTunnelReadyHandler(auth, signer, dpop, vpnRuntime))
	mux.HandleFunc("POST /api/agent/vnc/ensure", agentVNCEnsureHandler(auth, signer, dpop, vpnRuntime, vncRuntime))
	mux.HandleFunc("POST /api/agent/rdp/ensure", agentRDPEnsureHandler(auth, signer, dpop, vpnRuntime))
	return nil
}

func agentRDPEnsureHandler(auth *authService, signer *agentJWTSigner, dpop *dpopVerifier, vpnRuntime *vpnTunnelService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deviceCtx, failure := authenticateDeviceBearer(r.Context(), r, auth, signer, dpop)
		if failure != nil {
			failure.write(w)
			return
		}
		if !deviceAllowsRemoteAccess(deviceCtx.Status) {
			writeJSON(w, http.StatusForbidden, deviceRemoteAccessBlockedPayload(deviceCtx.Status))
			return
		}
		body := readOptionalJSONMap(w, r)
		if body == nil {
			return
		}
		device, status, payloadErr := resolveAgentDeviceForGUID(r.Context(), auth, deviceCtx.GUID, cleanText(body["agent_id"]))
		if payloadErr != nil {
			writeJSON(w, status, payloadErr)
			return
		}
		if vpnRuntime == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "tunnel_unavailable"})
			return
		}
		rdpPort := parseIntDefault(os.Getenv("BOREALIS_RDP_PORT"), defaultRDPBackendPort)
		sessionPayload := vpnRuntime.sessionPayload(r.Context(), device.AgentID, false)
		if sessionPayload == nil {
			var err error
			sessionPayload, err = vpnRuntime.connect(r.Context(), vpnConnectRequest{
				AgentID:       device.AgentID,
				EndpointHost:  inferWireGuardEndpointHost(r),
				MarkActivity:  false,
				RequiredPorts: []int{rdpPort},
			})
			if err != nil {
				writeJSON(w, http.StatusConflict, map[string]any{"error": "tunnel_down", "detail": err.Error()})
				return
			}
		}
		roleHealth := loadAgentRoleHealth(r.Context(), auth, device.AgentID, "rdp")
		writeJSON(w, http.StatusOK, map[string]any{
			"status":            "ok",
			"agent_id":          device.AgentID,
			"rdp_port":          rdpPort,
			"allowed_ips":       firstNonEmpty(sessionPayload["allowed_ips"], sessionPayload["engine_virtual_ip"]),
			"engine_virtual_ip": sessionPayload["engine_virtual_ip"],
			"virtual_ip":        sessionPayload["virtual_ip"],
			"tunnel_id":         sessionPayload["tunnel_id"],
			"ready":             boolFromAny(roleHealth["ready"]),
			"service_state":     cleanText(roleHealth["service_state"]),
			"listener_state":    cleanText(roleHealth["listener_state"]),
			"last_ready_at":     coerceInt64(roleHealth["last_ready_at"]),
			"health_status":     cleanText(roleHealth["status"]),
			"detail":            cleanText(roleHealth["detail"]),
		})
	}
}

func agentVPNTunnelEnsureHandler(auth *authService, signer *agentJWTSigner, dpop *dpopVerifier, vpnRuntime *vpnTunnelService, vncRuntime *vncRuntime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deviceCtx, failure := authenticateDeviceBearer(r.Context(), r, auth, signer, dpop)
		if failure != nil {
			failure.write(w)
			return
		}
		if !deviceAllowsRemoteAccess(deviceCtx.Status) {
			writeJSON(w, http.StatusForbidden, deviceRemoteAccessBlockedPayload(deviceCtx.Status))
			return
		}
		body := readOptionalJSONMap(w, r)
		if body == nil {
			return
		}
		device, status, payloadErr := resolveAgentDeviceForGUID(r.Context(), auth, deviceCtx.GUID, cleanText(body["agent_id"]))
		if payloadErr != nil {
			writeJSON(w, status, payloadErr)
			return
		}
		if vpnRuntime == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "tunnel_unavailable"})
			return
		}
		payload, err := vpnRuntime.connect(r.Context(), vpnConnectRequest{
			AgentID:      device.AgentID,
			EndpointHost: inferWireGuardEndpointHost(r),
			MarkActivity: false,
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "tunnel_start_failed", "detail": err.Error()})
			return
		}
		response := copyMap(payload)
		response["vnc_port"] = parseIntDefault(os.Getenv("BOREALIS_VNC_PORT"), defaultVNCBackendPort)
		response["vnc_password"] = ""
		response["view_only_password"] = ""
		if vncRuntime != nil {
			if session := vncRuntime.sessionByAgent(device.AgentID); session != nil {
				response["vnc_session_id"] = session.SessionID
				response["vnc_credential_revision"] = session.CredentialRevision
			} else {
				response["vnc_session_id"] = ""
				response["vnc_credential_revision"] = 0
			}
		}
		writeJSON(w, http.StatusOK, response)
	}
}

func agentVPNTunnelReadyHandler(auth *authService, signer *agentJWTSigner, dpop *dpopVerifier, vpnRuntime *vpnTunnelService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deviceCtx, failure := authenticateDeviceBearer(r.Context(), r, auth, signer, dpop)
		if failure != nil {
			failure.write(w)
			return
		}
		if !deviceAllowsRemoteAccess(deviceCtx.Status) {
			writeJSON(w, http.StatusForbidden, deviceRemoteAccessBlockedPayload(deviceCtx.Status))
			return
		}
		body := readOptionalJSONMap(w, r)
		if body == nil {
			return
		}
		device, status, payloadErr := resolveAgentDeviceForGUID(r.Context(), auth, deviceCtx.GUID, cleanText(body["agent_id"]))
		if payloadErr != nil {
			writeJSON(w, status, payloadErr)
			return
		}
		tunnelID := cleanText(body["tunnel_id"])
		if tunnelID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "tunnel_id_required"})
			return
		}
		if vpnRuntime == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "tunnel_unavailable"})
			return
		}
		payload := vpnRuntime.recordAgentReady(
			r.Context(),
			device.AgentID,
			tunnelID,
			coercePortList(body["allowed_ports"]),
			cleanText(body["reason"]),
			cleanText(body["service_state"]),
			cleanText(body["virtual_ip"]),
		)
		if payload == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "tunnel_not_found"})
			return
		}
		writeJSON(w, http.StatusOK, payload)
	}
}

func agentVNCEnsureHandler(auth *authService, signer *agentJWTSigner, dpop *dpopVerifier, vpnRuntime *vpnTunnelService, vncRuntime *vncRuntime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deviceCtx, failure := authenticateDeviceBearer(r.Context(), r, auth, signer, dpop)
		if failure != nil {
			failure.write(w)
			return
		}
		if !deviceAllowsRemoteAccess(deviceCtx.Status) {
			writeJSON(w, http.StatusForbidden, deviceRemoteAccessBlockedPayload(deviceCtx.Status))
			return
		}
		body := readOptionalJSONMap(w, r)
		if body == nil {
			return
		}
		device, status, payloadErr := resolveAgentDeviceForGUID(r.Context(), auth, deviceCtx.GUID, cleanText(body["agent_id"]))
		if payloadErr != nil {
			writeJSON(w, status, payloadErr)
			return
		}
		if vpnRuntime == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "tunnel_unavailable"})
			return
		}
		vncPort := parseIntDefault(os.Getenv("BOREALIS_VNC_PORT"), defaultVNCBackendPort)
		sessionPayload := vpnRuntime.sessionPayload(r.Context(), device.AgentID, false)
		if sessionPayload == nil {
			var err error
			sessionPayload, err = vpnRuntime.connect(r.Context(), vpnConnectRequest{
				AgentID:       device.AgentID,
				EndpointHost:  inferWireGuardEndpointHost(r),
				MarkActivity:  false,
				RequiredPorts: []int{vncPort},
			})
			if err != nil {
				writeJSON(w, http.StatusConflict, map[string]any{"error": "tunnel_down", "detail": err.Error()})
				return
			}
		}
		roleHealth := loadAgentVNCRoleHealth(r.Context(), auth, device.AgentID)
		displayTopology := cloneAnyMapSlice(body["display_topology"])
		if len(displayTopology) == 0 {
			displayTopology = cloneAnyMapSlice(roleHealth["display_topology"])
		}
		displayBounds := copyAnyMap(body["display_virtual_bounds"])
		if len(displayBounds) == 0 {
			displayBounds = copyAnyMap(roleHealth["display_virtual_bounds"])
		}
		if len(displayBounds) == 0 {
			displayBounds = displayBoundsFromTopology(displayTopology)
		}
		activeSession := map[string]any{}
		if vncRuntime != nil {
			if session := vncRuntime.sessionByAgent(device.AgentID); session != nil {
				activeSession = map[string]any{
					"session_id":             session.SessionID,
					"session_state":          session.State,
					"controller_operator_id": session.ControllerOperatorID,
					"credential_revision":    session.CredentialRevision,
					"remove_wallpaper":       session.RemoveWallpaper,
					"session":                vncRuntime.sessionSnapshot(session, ""),
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":                 "ok",
			"agent_id":               device.AgentID,
			"vnc_port":               vncPort,
			"allowed_ips":            firstNonEmpty(sessionPayload["allowed_ips"], sessionPayload["engine_virtual_ip"]),
			"tunnel_id":              sessionPayload["tunnel_id"],
			"engine_virtual_ip":      sessionPayload["engine_virtual_ip"],
			"ready":                  boolFromAny(roleHealth["ready"]),
			"service_state":          cleanText(roleHealth["service_state"]),
			"listener_state":         cleanText(roleHealth["listener_state"]),
			"last_ready_at":          coerceInt64(roleHealth["last_ready_at"]),
			"health_status":          cleanText(roleHealth["status"]),
			"detail":                 cleanText(roleHealth["detail"]),
			"session_id":             cleanText(activeSession["session_id"]),
			"session_state":          cleanText(activeSession["session_state"]),
			"controller_operator_id": cleanText(activeSession["controller_operator_id"]),
			"controller_password":    "",
			"view_only_password":     "",
			"vnc_password":           "",
			"credential_revision":    coerceInt64(activeSession["credential_revision"]),
			"remove_wallpaper":       boolFromAny(activeSession["remove_wallpaper"]),
			"display_topology":       displayTopology,
			"display_virtual_bounds": displayBounds,
			"session":                firstNonEmpty(activeSession["session"], nil),
		})
	}
}

func deviceRemoteAccessBlockedPayload(status string) map[string]any {
	return map[string]any{
		"error":   "device_quarantined",
		"message": "Device is not active for remote operations.",
		"status":  firstText(strings.ToLower(strings.TrimSpace(status)), "unknown"),
	}
}

func readOptionalJSONMap(w http.ResponseWriter, r *http.Request) map[string]any {
	body, err := readJSONMap(r)
	if err != nil {
		invalidJSONOrValidation(w, err)
		return nil
	}
	return body
}

func resolveAgentDeviceForGUID(ctx context.Context, auth *authService, guid string, requestedAgentID string) (remoteOpsSessionDevice, int, map[string]any) {
	guid = normalizeCanonicalGUID(guid)
	if guid == "" {
		return remoteOpsSessionDevice{}, http.StatusNotFound, map[string]any{"error": "agent_id_missing"}
	}
	db := vpnDB(auth)
	if db == nil {
		return remoteOpsSessionDevice{}, http.StatusBadGateway, map[string]any{"error": "device_lookup_unavailable"}
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return remoteOpsSessionDevice{}, http.StatusInternalServerError, map[string]any{"error": err.Error()}
	}
	defer conn.Close()
	var storedGUID, hostname, agentID sql.NullString
	var siteID sql.NullInt64
	err = conn.QueryRowContext(ctx, `
		SELECT d.guid, d.hostname, d.agent_id, ds.site_id
		  FROM engine.devices AS d
	 LEFT JOIN engine.device_sites AS ds ON ds.device_hostname=d.hostname
		 WHERE UPPER(d.guid)=UPPER($1)
	  ORDER BY COALESCE(d.last_seen, 0) DESC, d.hostname ASC
		 LIMIT 1`, guid).Scan(&storedGUID, &hostname, &agentID, &siteID)
	if err == sql.ErrNoRows {
		return remoteOpsSessionDevice{}, http.StatusNotFound, map[string]any{"error": "agent_id_missing"}
	}
	if err != nil {
		return remoteOpsSessionDevice{}, http.StatusInternalServerError, map[string]any{"error": err.Error()}
	}
	resolved := cleanText(agentID.String)
	if requestedAgentID = cleanText(requestedAgentID); requestedAgentID != "" && strings.EqualFold(guidFromAgentID(requestedAgentID), guid) {
		resolved = requestedAgentID
		if !strings.EqualFold(cleanText(agentID.String), requestedAgentID) {
			_, _ = conn.ExecContext(ctx, `UPDATE engine.devices SET agent_id=$1 WHERE UPPER(guid)=UPPER($2)`, requestedAgentID, guid)
		}
	}
	if resolved == "" {
		return remoteOpsSessionDevice{}, http.StatusNotFound, map[string]any{"error": "agent_id_missing"}
	}
	var siteIDPtr *int64
	if siteID.Valid {
		value := siteID.Int64
		siteIDPtr = &value
	}
	return remoteOpsSessionDevice{
		GUID:     normalizeCanonicalGUID(storedGUID.String),
		Hostname: cleanText(hostname.String),
		AgentID:  resolved,
		SiteID:   siteIDPtr,
	}, http.StatusOK, nil
}

func guidFromAgentID(value string) string {
	text := cleanText(value)
	if text == "" {
		return ""
	}
	parts := strings.Split(text, "_")
	if len(parts) < 3 {
		return ""
	}
	return normalizeCanonicalGUID(parts[len(parts)-2])
}

func coercePortList(value any) []int {
	switch typed := value.(type) {
	case []any:
		out := make([]int, 0, len(typed))
		for _, item := range typed {
			port := int(coerceInt64(item))
			if port >= 1 && port <= 65535 {
				out = append(out, port)
			}
		}
		return uniquePorts(out)
	case []int:
		return uniquePorts(typed)
	case []string:
		out := make([]int, 0, len(typed))
		for _, item := range typed {
			port, err := strconv.Atoi(strings.TrimSpace(item))
			if err == nil && port >= 1 && port <= 65535 {
				out = append(out, port)
			}
		}
		return uniquePorts(out)
	case string:
		return parsePortListText(typed)
	default:
		return nil
	}
}

func parsePortListText(raw string) []int {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n'
	})
	ports := []int{}
	for _, part := range parts {
		port, err := strconv.Atoi(strings.TrimSpace(part))
		if err == nil && port >= 1 && port <= 65535 {
			ports = append(ports, port)
		}
	}
	return uniquePorts(ports)
}

func loadAgentVNCRoleHealth(ctx context.Context, auth *authService, agentID string) map[string]any {
	defaultPayload := map[string]any{
		"status":                 "unknown",
		"detail":                 "",
		"ready":                  false,
		"service_state":          "",
		"listener_state":         "",
		"last_ready_at":          0,
		"display_topology":       []map[string]any{},
		"display_virtual_bounds": map[string]any{},
	}
	db := vpnDB(auth)
	if db == nil || cleanText(agentID) == "" {
		return defaultPayload
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return defaultPayload
	}
	defer conn.Close()
	var raw sql.NullString
	err = conn.QueryRowContext(ctx, `SELECT agent_role_health FROM engine.devices WHERE LOWER(agent_id)=LOWER($1) ORDER BY last_seen DESC LIMIT 1`, agentID).Scan(&raw)
	if err != nil || !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return defaultPayload
	}
	var parsed map[string]any
	if json.Unmarshal([]byte(raw.String), &parsed) != nil {
		return defaultPayload
	}
	roles, _ := parsed["roles"].([]any)
	for _, item := range roles {
		role, ok := item.(map[string]any)
		if !ok || (!strings.EqualFold(cleanText(role["role_id"]), "vnc") && !strings.EqualFold(cleanText(role["role_name"]), "vnc")) {
			continue
		}
		details, _ := role["details"].(map[string]any)
		serviceState := cleanText(firstNonEmpty(details["service_state"], details["state"]))
		listenerState := cleanText(firstNonEmpty(details["listener_state"], details["listener_ready"]))
		ready := boolFromAny(details["ready"]) || (strings.EqualFold(cleanText(firstNonEmpty(role["status_code"], role["status"])), "healthy") && boolFromAny(firstNonEmpty(details["listener_ready"], listenerState)))
		return map[string]any{
			"status":                 firstText(strings.ToLower(cleanText(firstNonEmpty(role["status_code"], role["status"]))), "unknown"),
			"detail":                 cleanText(role["detail"]),
			"ready":                  ready,
			"service_state":          serviceState,
			"listener_state":         listenerState,
			"last_ready_at":          coerceInt64(details["last_ready_at"]),
			"display_topology":       cloneAnyMapSlice(firstNonEmpty(details["display_topology"], details["display_topology_json"])),
			"display_virtual_bounds": copyAnyMap(firstNonEmpty(details["display_virtual_bounds"], details["display_virtual_bounds_json"])),
			"details":                details,
		}
	}
	return defaultPayload
}

func loadAgentRoleHealth(ctx context.Context, auth *authService, agentID string, roleName string) map[string]any {
	defaultPayload := map[string]any{
		"status":         "unknown",
		"detail":         "",
		"ready":          false,
		"service_state":  "",
		"listener_state": "",
		"last_ready_at":  0,
	}
	db := vpnDB(auth)
	roleName = strings.ToLower(strings.TrimSpace(roleName))
	if db == nil || cleanText(agentID) == "" || roleName == "" {
		return defaultPayload
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return defaultPayload
	}
	var raw sql.NullString
	err = conn.QueryRowContext(ctx, `SELECT agent_role_health FROM engine.devices WHERE LOWER(agent_id)=LOWER($1) ORDER BY last_seen DESC LIMIT 1`, agentID).Scan(&raw)
	closeErr := conn.Close()
	if err != nil || closeErr != nil || !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return defaultPayload
	}
	var parsed map[string]any
	if json.Unmarshal([]byte(raw.String), &parsed) != nil {
		return defaultPayload
	}
	roles, _ := parsed["roles"].([]any)
	for _, item := range roles {
		role, ok := item.(map[string]any)
		if !ok {
			continue
		}
		roleID := strings.ToLower(cleanText(role["role_id"]))
		storedName := strings.ToLower(cleanText(role["role_name"]))
		if storedName != roleName && roleID != roleName && roleID != "system:"+roleName {
			continue
		}
		details, _ := role["details"].(map[string]any)
		serviceState := cleanText(firstNonEmpty(details["service_state"], details["state"]))
		listenerState := cleanText(firstNonEmpty(details["listener_state"], details["listener_ready"]))
		status := firstText(strings.ToLower(cleanText(firstNonEmpty(role["status_code"], role["status"]))), "unknown")
		ready := boolFromAny(details["ready"]) || (status == "healthy" && boolFromAny(firstNonEmpty(details["listener_ready"], listenerState)))
		return map[string]any{
			"status":         status,
			"detail":         cleanText(role["detail"]),
			"ready":          ready,
			"service_state":  serviceState,
			"listener_state": listenerState,
			"last_ready_at":  coerceInt64(details["last_ready_at"]),
			"details":        details,
		}
	}
	return defaultPayload
}

func displayBoundsFromTopology(topology []map[string]any) map[string]any {
	if len(topology) == 0 {
		return map[string]any{}
	}
	minX := int(coerceInt64(topology[0]["x"]))
	minY := int(coerceInt64(topology[0]["y"]))
	maxX := minX + int(coerceInt64(topology[0]["width"]))
	maxY := minY + int(coerceInt64(topology[0]["height"]))
	for _, item := range topology[1:] {
		x := int(coerceInt64(item["x"]))
		y := int(coerceInt64(item["y"]))
		width := int(coerceInt64(item["width"]))
		height := int(coerceInt64(item["height"]))
		if x < minX {
			minX = x
		}
		if y < minY {
			minY = y
		}
		if x+width > maxX {
			maxX = x + width
		}
		if y+height > maxY {
			maxY = y + height
		}
	}
	return map[string]any{"x": minX, "y": minY, "width": maxX - minX, "height": maxY - minY}
}
