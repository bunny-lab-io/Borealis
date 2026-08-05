package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	remoteOpSessionIssuer        = "borealis-api-backend"
	remoteOpSessionAudience      = "borealis-site-worker"
	remoteOpSessionTokenType     = "remote-op-session"
	defaultRemoteOpSessionTTL    = 300 * time.Second
	maxRemoteOpSessionTTL        = 900 * time.Second
	minRemoteOpSessionTTLSeconds = 30
)

var remoteOpCapabilityAliases = map[string]string{
	"shell":               "remote_shell",
	"remote-shell":        "remote_shell",
	"remote_shell":        "remote_shell",
	"powershell":          "remote_shell",
	"desktop":             "remote_desktop",
	"remote-desktop":      "remote_desktop",
	"remote_desktop":      "remote_desktop",
	"vnc":                 "remote_desktop",
	"guacamole":           "remote_desktop",
	"files":               "remote_files",
	"file":                "remote_files",
	"remote-files":        "remote_files",
	"remote_files":        "remote_files",
	"file_management":     "remote_files",
	"process":             "process_management",
	"processes":           "process_management",
	"process-management":  "process_management",
	"process_management":  "process_management",
	"service":             "service_management",
	"services":            "service_management",
	"service-management":  "service_management",
	"service_management":  "service_management",
	"software":            "software_management",
	"software-management": "software_management",
	"software_management": "software_management",
	"agent-maintenance":   "agent_maintenance",
	"agent_maintenance":   "agent_maintenance",
	"quick-job":           "quick_job",
	"quick_job":           "quick_job",
}

type remoteOpsSessionStore interface {
	createRemoteOpsSession(ctx context.Context, profile operatorProfile, request remoteOpsSessionRequest) (remoteOpsSessionResult, int, error)
}

type remoteOpsSessionRequest struct {
	DeviceGUID   string
	Hostname     string
	AgentID      string
	Capabilities []string
	TTLSeconds   any
	Now          time.Time
}

type remoteOpsSessionDevice struct {
	GUID     string
	Hostname string
	AgentID  string
	Status   string
	SiteID   *int64
	SiteName string
}

type remoteOpsSessionResult struct {
	Device remoteOpsSessionDevice
	Route  *agentWorkerRoute
}

func registerRemoteOpsSessionRoutes(mux *http.ServeMux, auth *authService, signer *agentJWTSigner) {
	mux.HandleFunc("POST /api/remote-ops/session", remoteOpsSessionHandler(auth, signer))
}

func remoteOpsSessionHandler(auth *authService, signer *agentJWTSigner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		profile, err := auth.currentProfile(r.Context(), r)
		if err != nil {
			if isUnauthorizedAuthError(err) {
				unauthorizedAuthFailure().write(w)
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
			return
		}
		store, ok := auth.store.(remoteOpsSessionStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "remote_ops_session_unavailable"})
			return
		}
		body, err := readJSONMap(r)
		if err != nil {
			invalidJSONOrValidation(w, err)
			return
		}
		capabilities := normalizeRemoteOpCapabilities(firstPresent(body["capabilities"], body["capability"]))
		if len(capabilities) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error":   "invalid_capability",
				"message": "Valid remote-operation capability is required.",
			})
			return
		}

		timeout := auth.timeout
		if timeout <= 0 {
			timeout = defaultAuthTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		now := time.Now().UTC()
		ttl := configuredRemoteOpSessionTTL(body["ttl_seconds"])
		result, status, err := store.createRemoteOpsSession(ctx, profile, remoteOpsSessionRequest{
			DeviceGUID:   normalizeCanonicalGUID(firstText(cleanText(body["device_guid"]), cleanText(body["guid"]))),
			Hostname:     cleanText(firstPresent(body["hostname"], body["device_hostname"])),
			AgentID:      cleanText(body["agent_id"]),
			Capabilities: capabilities,
			TTLSeconds:   body["ttl_seconds"],
			Now:          now,
		})
		if err != nil {
			writeJSON(w, status, remoteOpsSessionErrorPayload(err))
			return
		}
		if result.Route == nil {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":   "site_worker_unavailable",
				"message": "No active site-worker route is available for this device site.",
			})
			return
		}

		issued, err := signer.issueRemoteOpsSession(profile, result.Device, *result.Route, capabilities, now, ttl)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "token_issue_failed"})
			return
		}
		urls := remoteOpsWorkerURLs(r, result.Route)
		session := map[string]any{
			"session_id":           issued.SessionID,
			"token_type":           "Bearer",
			"token":                issued.Token,
			"issuer":               remoteOpSessionIssuer,
			"audience":             remoteOpSessionAudience,
			"operation_token_type": remoteOpSessionTokenType,
			"issued_at":            issued.IssuedAt,
			"expires_at":           issued.ExpiresAt,
			"expires_in":           issued.ExpiresIn,
			"capabilities":         capabilities,
			"user": map[string]any{
				"username": profile.Username,
				"role":     firstText(profile.Role, defaultUserRole),
			},
			"device": map[string]any{
				"guid":      result.Device.GUID,
				"hostname":  result.Device.Hostname,
				"agent_id":  result.Device.AgentID,
				"site_id":   nullableInt64(result.Device.SiteID),
				"site_name": result.Device.SiteName,
			},
			"worker": map[string]any{
				"worker_guid":       result.Route.WorkerGUID,
				"route_generation":  result.Route.Generation,
				"route_path_prefix": result.Route.RoutePathPrefix,
				"base_url":          urls["base"],
				"urls":              urls,
			},
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "session": session})
	}
}

type issuedRemoteOpsSession struct {
	Token     string
	SessionID string
	IssuedAt  int64
	ExpiresAt int64
	ExpiresIn int64
}

func (s *agentJWTSigner) issueRemoteOpsSession(profile operatorProfile, device remoteOpsSessionDevice, route agentWorkerRoute, capabilities []string, now time.Time, ttl time.Duration) (issuedRemoteOpsSession, error) {
	if ttl <= 0 {
		ttl = defaultRemoteOpSessionTTL
	}
	sessionID, err := randomHex(16)
	if err != nil {
		return issuedRemoteOpsSession{}, err
	}
	nowUnix := now.UTC().Unix()
	siteID := nullableInt64(device.SiteID)
	claims := map[string]any{
		"iss":              remoteOpSessionIssuer,
		"aud":              remoteOpSessionAudience,
		"typ":              remoteOpSessionTokenType,
		"sub":              "remote-op:" + sessionID,
		"jti":              sessionID,
		"iat":              nowUnix,
		"nbf":              nowUnix,
		"exp":              now.Add(ttl).Unix(),
		"user":             profile.Username,
		"role":             firstText(profile.Role, defaultUserRole),
		"site_id":          siteID,
		"device_guid":      device.GUID,
		"hostname":         device.Hostname,
		"agent_id":         device.AgentID,
		"worker_guid":      route.WorkerGUID,
		"route_generation": route.Generation,
		"capabilities":     capabilities,
	}
	header := map[string]any{"alg": "EdDSA", "kid": s.keyID, "typ": "JWT"}
	token, err := signJWT(header, claims, s.privateKey)
	if err != nil {
		return issuedRemoteOpsSession{}, err
	}
	return issuedRemoteOpsSession{
		Token:     token,
		SessionID: sessionID,
		IssuedAt:  nowUnix,
		ExpiresAt: now.Add(ttl).Unix(),
		ExpiresIn: int64(ttl / time.Second),
	}, nil
}

func (s *postgresOperatorStore) createRemoteOpsSession(ctx context.Context, profile operatorProfile, request remoteOpsSessionRequest) (remoteOpsSessionResult, int, error) {
	if request.DeviceGUID == "" && request.Hostname == "" && request.AgentID == "" {
		return remoteOpsSessionResult{}, http.StatusNotFound, errors.New("device_not_found")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return remoteOpsSessionResult{}, http.StatusInternalServerError, err
	}
	defer conn.Close()

	device, err := lookupRemoteOpsDevice(ctx, conn, request)
	if err != nil {
		return remoteOpsSessionResult{}, http.StatusInternalServerError, err
	}
	if device.GUID == "" {
		return remoteOpsSessionResult{}, http.StatusNotFound, errors.New("device_not_found")
	}
	if device.SiteID == nil {
		return remoteOpsSessionResult{}, http.StatusConflict, errors.New("device_site_unassigned")
	}
	if !deviceAllowsRemoteAccess(device.Status) {
		return remoteOpsSessionResult{}, http.StatusForbidden, errors.New("device_remote_access_blocked")
	}
	allowed, err := profileCanAccessSite(ctx, conn, profile, *device.SiteID)
	if err != nil {
		return remoteOpsSessionResult{}, http.StatusInternalServerError, err
	}
	if !allowed {
		return remoteOpsSessionResult{}, http.StatusNotFound, errors.New("device_not_found")
	}
	route, err := fetchAgentWorkerRoute(ctx, conn, *device.SiteID)
	if err != nil {
		return remoteOpsSessionResult{}, http.StatusInternalServerError, err
	}
	return remoteOpsSessionResult{Device: device, Route: route}, http.StatusOK, nil
}

func lookupRemoteOpsDevice(ctx context.Context, conn *sql.Conn, request remoteOpsSessionRequest) (remoteOpsSessionDevice, error) {
	clauses := []string{}
	params := []any{}
	if request.DeviceGUID != "" {
		params = append(params, request.DeviceGUID)
		clauses = append(clauses, "UPPER(d.guid)=UPPER($"+strconv.Itoa(len(params))+")")
	}
	if request.Hostname != "" {
		params = append(params, request.Hostname)
		clauses = append(clauses, "LOWER(d.hostname)=LOWER($"+strconv.Itoa(len(params))+")")
	}
	if request.AgentID != "" {
		params = append(params, request.AgentID)
		clauses = append(clauses, "LOWER(d.agent_id)=LOWER($"+strconv.Itoa(len(params))+")")
	}
	if len(clauses) == 0 {
		return remoteOpsSessionDevice{}, nil
	}
	query := `
		SELECT d.guid, d.hostname, d.agent_id, COALESCE(d.status, 'active'), ds.site_id, s.name
		  FROM engine.devices AS d
	 LEFT JOIN engine.device_sites AS ds ON ds.device_hostname=d.hostname
	 LEFT JOIN engine.sites AS s ON s.id=ds.site_id
		 WHERE ` + strings.Join(clauses, " OR ") + `
	  ORDER BY COALESCE(d.last_seen, 0) DESC, d.hostname ASC
		 LIMIT 1`
	var guid, hostname, agentID, status, siteName sql.NullString
	var siteID sql.NullInt64
	err := conn.QueryRowContext(ctx, query, params...).Scan(&guid, &hostname, &agentID, &status, &siteID, &siteName)
	if errors.Is(err, sql.ErrNoRows) {
		return remoteOpsSessionDevice{}, nil
	}
	if err != nil {
		return remoteOpsSessionDevice{}, err
	}
	var siteIDPtr *int64
	if siteID.Valid {
		value := siteID.Int64
		siteIDPtr = &value
	}
	return remoteOpsSessionDevice{
		GUID:     normalizeCanonicalGUID(guid.String),
		Hostname: cleanText(hostname.String),
		AgentID:  cleanText(agentID.String),
		Status:   firstText(cleanText(status.String), "active"),
		SiteID:   siteIDPtr,
		SiteName: cleanText(siteName.String),
	}, nil
}

func deviceAllowsRemoteAccess(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "active", "online":
		return true
	default:
		return false
	}
}

func profileCanAccessSite(ctx context.Context, conn *sql.Conn, profile operatorProfile, siteID int64) (bool, error) {
	if strings.EqualFold(strings.TrimSpace(profile.Role), "admin") {
		return true, nil
	}
	if profile.ID <= 0 {
		return false, nil
	}
	var exists bool
	err := conn.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM engine.user_site_assignments WHERE user_id=$1 AND site_id=$2)`, profile.ID, siteID).Scan(&exists)
	return exists, err
}

func normalizeRemoteOpCapabilities(value any) []string {
	var candidates []any
	switch typed := value.(type) {
	case string:
		candidates = []any{typed}
	case []any:
		candidates = typed
	case []string:
		for _, item := range typed {
			candidates = append(candidates, item)
		}
	default:
		candidates = []any{}
	}
	seen := map[string]struct{}{}
	result := []string{}
	for _, item := range candidates {
		key := strings.ToLower(strings.ReplaceAll(cleanText(item), " ", "_"))
		if key == "" {
			continue
		}
		capability := remoteOpCapabilityAliases[key]
		if capability == "" {
			capability = remoteOpCapabilityAliases[strings.ReplaceAll(key, "_", "-")]
		}
		if capability == "" {
			continue
		}
		if _, ok := seen[capability]; ok {
			continue
		}
		seen[capability] = struct{}{}
		result = append(result, capability)
	}
	return result
}

func configuredRemoteOpSessionTTL(value any) time.Duration {
	defaultTTL := time.Duration(remoteOpsEnvInt("BOREALIS_REMOTE_OP_SESSION_TTL_SECONDS", int(defaultRemoteOpSessionTTL/time.Second), minRemoteOpSessionTTLSeconds)) * time.Second
	maxTTL := time.Duration(remoteOpsEnvInt("BOREALIS_REMOTE_OP_SESSION_MAX_TTL_SECONDS", int(maxRemoteOpSessionTTL/time.Second), minRemoteOpSessionTTLSeconds)) * time.Second
	ttl := time.Duration(coerceInt64(value)) * time.Second
	if ttl <= 0 {
		ttl = defaultTTL
	}
	if ttl < minRemoteOpSessionTTLSeconds*time.Second {
		ttl = minRemoteOpSessionTTLSeconds * time.Second
	}
	if ttl > maxTTL {
		ttl = maxTTL
	}
	return ttl
}

func remoteOpsEnvInt(name string, fallback int, minimum int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	if parsed < minimum {
		return minimum
	}
	return parsed
}

func remoteOpsWorkerURLs(r *http.Request, route *agentWorkerRoute) map[string]any {
	workerBase := joinURL(publicBaseURLForRequest(r), route.RoutePathPrefix)
	return map[string]any{
		"base":      workerBase,
		"socket_io": joinURL(workerBase, "/socket.io/"),
	}
}

func remoteOpsSessionErrorPayload(err error) map[string]any {
	switch err.Error() {
	case "device_not_found":
		return map[string]any{"error": "not_found", "message": "Device was not found."}
	case "device_site_unassigned":
		return map[string]any{"error": "device_site_unassigned", "message": "Device is not assigned to a site."}
	case "device_remote_access_blocked":
		return map[string]any{"error": "device_quarantined", "message": "Device is not active for remote operations."}
	default:
		return map[string]any{"error": err.Error()}
	}
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func firstPresent(values ...any) any {
	for _, value := range values {
		if cleanText(value) != "" {
			return value
		}
	}
	return nil
}

func randomHex(length int) (string, error) {
	buffer := make([]byte, length)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}
