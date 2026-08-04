package main

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type deviceBearerAuthStore interface {
	requiredDeviceTokenVersion(ctx context.Context, guid string) (*int, error)
	deviceBearerAuthRecord(ctx context.Context, guid string) (deviceBearerAuthRecord, bool, error)
}

type agentHashStore interface {
	lookupAgentHash(ctx context.Context, authGUID string, agentGUID string, agentID string) (map[string]any, int, error)
	updateAgentHash(ctx context.Context, authGUID string, request agentHashUpdateRequest) (map[string]any, int, error)
	listAgentHashes(ctx context.Context) ([]map[string]any, error)
}

type deviceBearerAuthRecord struct {
	GUID         string
	Fingerprint  string
	TokenVersion int
	Status       string
}

type deviceBearerAuthContext struct {
	GUID         string
	Fingerprint  string
	TokenVersion int
	Status       string
	AccessToken  string
	Claims       map[string]any
	DPoPJKT      string
}

type agentHashUpdateRequest struct {
	AgentHash string
	AgentID   string
	AgentGUID string
}

func registerAgentHashRoutes(mux *http.ServeMux, auth *authService, signer *agentJWTSigner, dpop *dpopVerifier) {
	mux.HandleFunc("/api/agent/hash", agentHashHandler(auth, signer, dpop))
	mux.HandleFunc("GET /api/agent/hash_list", agentHashListHandler(auth))
}

func agentHashHandler(auth *authService, signer *agentJWTSigner, dpop *dpopVerifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			writeMethodNotAllowed(w, "GET, POST")
			return
		}
		store, ok := auth.store.(agentHashStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "agent_hash_unavailable"})
			return
		}
		deviceCtx, failure := authenticateDeviceBearer(r.Context(), r, auth, signer, dpop)
		if failure != nil {
			failure.write(w)
			return
		}

		timeout := auth.timeout
		if timeout <= 0 {
			timeout = defaultAuthTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		var payload map[string]any
		var status int
		var err error
		if r.Method == http.MethodGet {
			agentGUID, agentID, badRequest := parseAgentHashLookupRequest(r)
			if badRequest != nil {
				writeJSON(w, http.StatusBadRequest, badRequest)
				return
			}
			payload, status, err = store.lookupAgentHash(ctx, deviceCtx.GUID, agentGUID, agentID)
		} else {
			updateRequest, badRequest := parseAgentHashUpdateRequest(r)
			if badRequest != nil {
				writeJSON(w, http.StatusBadRequest, badRequest)
				return
			}
			payload, status, err = store.updateAgentHash(ctx, deviceCtx.GUID, updateRequest)
		}
		if err != nil {
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, status, payload)
	}
}

func agentHashListHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		if !isInternalRequest(r.RemoteAddr) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden"})
			return
		}
		if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		store, ok := auth.store.(agentHashStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "agent_hash_unavailable"})
			return
		}
		timeout := auth.timeout
		if timeout <= 0 {
			timeout = defaultAuthTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		agents, err := store.listAgentHashes(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"agents": agents})
	}
}

func authenticateDeviceBearer(ctx context.Context, r *http.Request, auth *authService, signer *agentJWTSigner, dpop *dpopVerifier) (deviceBearerAuthContext, *authFailure) {
	token, err := extractBearerToken(r)
	if err != nil {
		return deviceBearerAuthContext{}, &authFailure{status: http.StatusUnauthorized, body: map[string]any{"error": "missing_authorization"}}
	}
	claims, err := signer.verifyAccessToken(token)
	if err != nil {
		if errors.Is(err, errExpiredToken) {
			return deviceBearerAuthContext{}, &authFailure{status: http.StatusUnauthorized, body: map[string]any{"error": "token_expired"}}
		}
		return deviceBearerAuthContext{}, &authFailure{status: http.StatusUnauthorized, body: map[string]any{"error": "invalid_token"}}
	}
	guid := normalizeCanonicalGUID(claimString(claims, "guid"))
	fingerprint := strings.ToLower(strings.TrimSpace(claimString(claims, "ssl_key_fingerprint")))
	tokenVersion := claimInt(claims, "token_version")
	if guid == "" || fingerprint == "" || tokenVersion <= 0 {
		return deviceBearerAuthContext{}, &authFailure{status: http.StatusUnauthorized, body: map[string]any{"error": "invalid_claims"}}
	}
	store, ok := auth.store.(deviceBearerAuthStore)
	if !ok {
		return deviceBearerAuthContext{}, &authFailure{status: http.StatusBadGateway, body: map[string]any{"error": "device_auth_unavailable"}}
	}

	timeout := auth.timeout
	if timeout <= 0 {
		timeout = defaultAuthTimeout
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	requiredVersion, err := store.requiredDeviceTokenVersion(requestCtx, guid)
	if err != nil {
		return deviceBearerAuthContext{}, &authFailure{status: http.StatusInternalServerError, body: map[string]any{"error": err.Error()}}
	}
	if requiredVersion != nil && tokenVersion < *requiredVersion {
		return deviceBearerAuthContext{}, &authFailure{status: http.StatusUnauthorized, body: map[string]any{"error": "device_purged"}}
	}
	record, ok, err := store.deviceBearerAuthRecord(requestCtx, guid)
	if err != nil {
		return deviceBearerAuthContext{}, &authFailure{status: http.StatusInternalServerError, body: map[string]any{"error": err.Error()}}
	}
	if !ok {
		if requiredVersion != nil {
			return deviceBearerAuthContext{}, &authFailure{status: http.StatusUnauthorized, body: map[string]any{"error": "device_purged"}}
		}
		return deviceBearerAuthContext{}, &authFailure{status: http.StatusNotFound, body: map[string]any{"error": "device_not_found"}}
	}
	if normalizeCanonicalGUID(record.GUID) != guid {
		return deviceBearerAuthContext{}, &authFailure{status: http.StatusUnauthorized, body: map[string]any{"error": "device_mismatch"}}
	}
	if strings.ToLower(strings.TrimSpace(record.Fingerprint)) != fingerprint {
		return deviceBearerAuthContext{}, &authFailure{status: http.StatusForbidden, body: map[string]any{"error": "fingerprint_mismatch"}}
	}
	if record.TokenVersion != tokenVersion {
		return deviceBearerAuthContext{}, &authFailure{status: http.StatusForbidden, body: map[string]any{"error": "token_version_mismatch"}}
	}
	status := strings.ToLower(strings.TrimSpace(record.Status))
	if status == "" {
		status = "active"
	}
	if status == "revoked" || status == "decommissioned" {
		return deviceBearerAuthContext{}, &authFailure{status: http.StatusForbidden, body: map[string]any{"error": "device_revoked"}}
	}

	jkt := ""
	proof := strings.TrimSpace(r.Header.Get("DPoP"))
	if proof != "" {
		verifiedJKT, err := dpop.verify(r.Method, absoluteRequestURL(r), proof, time.Now().UTC(), token)
		if err != nil {
			if errors.Is(err, errDPoPReplay) {
				return deviceBearerAuthContext{}, &authFailure{status: http.StatusBadRequest, body: map[string]any{"error": "dpop_replayed"}}
			}
			return deviceBearerAuthContext{}, &authFailure{status: http.StatusBadRequest, body: map[string]any{"error": "dpop_invalid"}}
		}
		jkt = verifiedJKT
	}
	return deviceBearerAuthContext{
		GUID:         guid,
		Fingerprint:  fingerprint,
		TokenVersion: tokenVersion,
		Status:       status,
		AccessToken:  token,
		Claims:       claims,
		DPoPJKT:      jkt,
	}, nil
}

func extractBearerToken(r *http.Request) (string, error) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(header) >= 7 && strings.EqualFold(header[:6], "Bearer") && header[6] == ' ' {
		if token := strings.TrimSpace(header[7:]); token != "" {
			return token, nil
		}
	}
	return "", errMissingToken
}

func (s *agentJWTSigner) verifyAccessToken(token string) (map[string]any, error) {
	if s == nil || len(s.privateKey) == 0 {
		return nil, errInvalidToken
	}
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return nil, errInvalidToken
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, errInvalidToken
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errInvalidToken
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errInvalidToken
	}
	var header struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil || header.Alg != "EdDSA" {
		return nil, errInvalidToken
	}
	publicKey, ok := s.privateKey.Public().(ed25519.PublicKey)
	if !ok {
		return nil, errInvalidToken
	}
	if !ed25519.Verify(publicKey, []byte(parts[0]+"."+parts[1]), signature) {
		return nil, errInvalidToken
	}
	var claims map[string]any
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, errInvalidToken
	}
	now := time.Now().Unix()
	if s.now != nil {
		now = s.now().Unix()
	}
	if exp := claimInt64(claims, "exp"); exp > 0 && exp <= now {
		return nil, errExpiredToken
	}
	if nbf := claimInt64(claims, "nbf"); nbf > 0 && nbf > now {
		return nil, errExpiredToken
	}
	if strings.TrimSpace(claimString(claims, "sub")) == "" || claimInt64(claims, "iat") == 0 {
		return nil, errInvalidToken
	}
	return claims, nil
}

func parseAgentHashLookupRequest(r *http.Request) (string, string, map[string]any) {
	agentGUID := normalizeCanonicalGUID(r.URL.Query().Get("agent_guid"))
	agentID := cleanText(firstText(r.URL.Query().Get("agent_id"), r.URL.Query().Get("id")))
	if agentGUID != "" || agentID != "" {
		return agentGUID, agentID, nil
	}
	body, err := readJSONMapWithLimit(r, 1<<20)
	if err != nil {
		return "", "", publicValidationErrorPayload(err, "invalid_request")
	}
	agentGUID = normalizeCanonicalGUID(body["agent_guid"])
	agentID = cleanText(firstText(cleanText(body["agent_id"]), cleanText(body["id"])))
	return agentGUID, agentID, nil
}

func parseAgentHashUpdateRequest(r *http.Request) (agentHashUpdateRequest, map[string]any) {
	body, err := readJSONMapWithLimit(r, 1<<20)
	if err != nil {
		return agentHashUpdateRequest{}, publicValidationErrorPayload(err, "invalid_request")
	}
	request := agentHashUpdateRequest{
		AgentHash: cleanText(body["agent_hash"]),
		AgentID:   cleanText(body["agent_id"]),
		AgentGUID: normalizeCanonicalGUID(body["agent_guid"]),
	}
	if request.AgentHash == "" {
		return agentHashUpdateRequest{}, map[string]any{"error": "agent_hash required"}
	}
	return request, nil
}

func (s *postgresOperatorStore) requiredDeviceTokenVersion(ctx context.Context, guid string) (*int, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	exists, err := relationExists(ctx, conn, "engine.device_purge_barriers")
	if err != nil || !exists {
		return nil, err
	}
	var required int
	err = conn.QueryRowContext(
		ctx,
		`SELECT required_token_version FROM engine.device_purge_barriers WHERE UPPER(guid)=UPPER($1) LIMIT 1`,
		guid,
	).Scan(&required)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if required < 1 {
		required = 1
	}
	return &required, nil
}

func (s *postgresOperatorStore) deviceBearerAuthRecord(ctx context.Context, guid string) (deviceBearerAuthRecord, bool, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return deviceBearerAuthRecord{}, false, err
	}
	defer conn.Close()
	var record deviceBearerAuthRecord
	var version sql.NullInt64
	err = conn.QueryRowContext(
		ctx,
		`
		SELECT guid, COALESCE(ssl_key_fingerprint, ''), COALESCE(token_version, 1), COALESCE(status, 'active')
		  FROM engine.devices
		 WHERE UPPER(guid)=UPPER($1)
		 LIMIT 1
		`,
		guid,
	).Scan(&record.GUID, &record.Fingerprint, &version, &record.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return deviceBearerAuthRecord{}, false, nil
	}
	if err != nil {
		return deviceBearerAuthRecord{}, false, err
	}
	record.GUID = normalizeCanonicalGUID(record.GUID)
	record.Fingerprint = strings.ToLower(strings.TrimSpace(record.Fingerprint))
	record.TokenVersion = int(version.Int64)
	if record.TokenVersion <= 0 {
		record.TokenVersion = 1
	}
	return record, true, nil
}

func (s *postgresOperatorStore) lookupAgentHash(ctx context.Context, authGUID string, agentGUID string, agentID string) (map[string]any, int, error) {
	authGUID = normalizeCanonicalGUID(authGUID)
	agentGUID = normalizeCanonicalGUID(agentGUID)
	agentID = cleanText(agentID)
	if authGUID == "" {
		return nil, http.StatusForbidden, errors.New("guid_required")
	}
	if agentGUID != "" && agentGUID != authGUID {
		return nil, http.StatusForbidden, errors.New("guid_mismatch")
	}
	effectiveGUID := firstText(agentGUID, authGUID)

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	defer conn.Close()

	row, found, err := queryAgentHashRow(ctx, conn, effectiveGUID, agentID)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if !found {
		return nil, http.StatusNotFound, errors.New("agent hash not found")
	}
	normalizedGUID := normalizeCanonicalGUID(row.GUID)
	if normalizedGUID != "" && normalizedGUID != authGUID {
		return nil, http.StatusForbidden, errors.New("guid_mismatch")
	}
	return agentHashRowPayload(row, firstText(normalizedGUID, effectiveGUID), agentID), http.StatusOK, nil
}

func (s *postgresOperatorStore) updateAgentHash(ctx context.Context, authGUID string, request agentHashUpdateRequest) (map[string]any, int, error) {
	authGUID = normalizeCanonicalGUID(authGUID)
	request.AgentGUID = normalizeCanonicalGUID(request.AgentGUID)
	request.AgentID = cleanText(request.AgentID)
	request.AgentHash = cleanText(request.AgentHash)
	if authGUID == "" {
		return nil, http.StatusForbidden, errors.New("guid_required")
	}
	if request.AgentHash == "" {
		return nil, http.StatusBadRequest, errors.New("agent_hash required")
	}
	if request.AgentGUID != "" && request.AgentGUID != authGUID {
		return nil, http.StatusForbidden, errors.New("guid_mismatch")
	}
	effectiveGUID := firstText(request.AgentGUID, authGUID)
	if effectiveGUID == "" && request.AgentID == "" {
		return nil, http.StatusBadRequest, errors.New("agent_hash and agent_guid or agent_id required")
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	defer conn.Close()

	row, found, err := queryAgentHashRow(ctx, conn, effectiveGUID, request.AgentID)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if !found {
		payload := map[string]any{
			"status":     "ignored",
			"agent_hash": request.AgentHash,
		}
		if effectiveGUID != "" {
			payload["agent_guid"] = effectiveGUID
		}
		if request.AgentID != "" {
			payload["agent_id"] = request.AgentID
		}
		return payload, http.StatusOK, nil
	}
	targetGUID := row.GUID
	normalizedGUID := normalizeCanonicalGUID(targetGUID)
	if normalizedGUID != "" && normalizedGUID != authGUID {
		return nil, http.StatusForbidden, errors.New("guid_mismatch")
	}
	if normalizedGUID != "" {
		effectiveGUID = normalizedGUID
	}
	resolvedAgentID := firstText(request.AgentID, row.AgentID)
	if resolvedAgentID != "" {
		_, err = conn.ExecContext(
			ctx,
			`UPDATE engine.devices SET agent_hash=$1, agent_id=$2 WHERE guid=$3`,
			request.AgentHash,
			resolvedAgentID,
			targetGUID,
		)
	} else {
		_, err = conn.ExecContext(
			ctx,
			`UPDATE engine.devices SET agent_hash=$1 WHERE guid=$2`,
			request.AgentHash,
			targetGUID,
		)
	}
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	response := map[string]any{
		"status":     "ok",
		"agent_hash": request.AgentHash,
	}
	if resolvedAgentID != "" {
		response["agent_id"] = resolvedAgentID
	}
	if effectiveGUID != "" {
		response["agent_guid"] = effectiveGUID
	}
	if row.Hostname != "" {
		response["hostname"] = row.Hostname
	}
	return response, http.StatusOK, nil
}

func (s *postgresOperatorStore) listAgentHashes(ctx context.Context) ([]map[string]any, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, `SELECT guid, hostname, agent_hash, agent_id FROM engine.devices`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	agents := []map[string]any{}
	for rows.Next() {
		var row agentHashRow
		if err := rows.Scan(&row.GUID, &row.Hostname, &row.AgentHash, &row.AgentID); err != nil {
			return nil, err
		}
		agents = append(agents, map[string]any{
			"agent_guid": nullIfEmpty(normalizeCanonicalGUID(row.GUID)),
			"hostname":   nullIfEmpty(row.Hostname),
			"agent_hash": nullIfEmpty(row.AgentHash),
			"agent_id":   nullIfEmpty(row.AgentID),
			"source":     "database",
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortMapsByText(agents, "hostname", "agent_id")
	return agents, nil
}

type agentHashRow struct {
	GUID      string
	Hostname  string
	AgentHash string
	AgentID   string
}

func queryAgentHashRow(ctx context.Context, conn *sql.Conn, guid string, agentID string) (agentHashRow, bool, error) {
	guid = normalizeCanonicalGUID(guid)
	agentID = cleanText(agentID)
	var row agentHashRow
	var err error
	if guid != "" {
		err = conn.QueryRowContext(
			ctx,
			`
			SELECT guid, COALESCE(hostname, ''), COALESCE(agent_hash, ''), COALESCE(agent_id, '')
			  FROM engine.devices
			 WHERE UPPER(guid)=UPPER($1)
			 LIMIT 1
			`,
			guid,
		).Scan(&row.GUID, &row.Hostname, &row.AgentHash, &row.AgentID)
		if err == nil {
			return cleanAgentHashRow(row), true, nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return agentHashRow{}, false, err
		}
	}
	if agentID != "" {
		err = conn.QueryRowContext(
			ctx,
			`
			SELECT guid, COALESCE(hostname, ''), COALESCE(agent_hash, ''), COALESCE(agent_id, '')
			  FROM engine.devices
			 WHERE agent_id=$1
			 ORDER BY last_seen DESC, created_at DESC
			 LIMIT 1
			`,
			agentID,
		).Scan(&row.GUID, &row.Hostname, &row.AgentHash, &row.AgentID)
		if err == nil {
			return cleanAgentHashRow(row), true, nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return agentHashRow{}, false, err
		}
	}
	return agentHashRow{}, false, nil
}

func cleanAgentHashRow(row agentHashRow) agentHashRow {
	row.GUID = normalizeCanonicalGUID(row.GUID)
	row.Hostname = cleanText(row.Hostname)
	row.AgentHash = cleanText(row.AgentHash)
	row.AgentID = cleanText(row.AgentID)
	return row
}

func agentHashRowPayload(row agentHashRow, fallbackGUID string, fallbackAgentID string) map[string]any {
	agentGUID := firstText(normalizeCanonicalGUID(row.GUID), fallbackGUID)
	payload := map[string]any{
		"agent_hash": nullIfEmpty(row.AgentHash),
		"agent_guid": agentGUID,
	}
	if agentID := firstText(row.AgentID, fallbackAgentID); agentID != "" {
		payload["agent_id"] = agentID
	}
	if row.Hostname != "" {
		payload["hostname"] = row.Hostname
	}
	return payload
}

func claimString(claims map[string]any, key string) string {
	return cleanText(claims[key])
}

func claimInt(claims map[string]any, key string) int {
	return int64ToIntDefault(claimInt64(claims, key), 0)
}

func claimInt64(claims map[string]any, key string) int64 {
	switch value := claims[key].(type) {
	case float64:
		return int64(value)
	case int:
		return int64(value)
	case int64:
		return value
	case json.Number:
		parsed, _ := value.Int64()
		return parsed
	case string:
		if result, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
			return result
		}
	}
	return 0
}

func sortMapsByText(items []map[string]any, keys ...string) {
	sort.SliceStable(items, func(i, j int) bool {
		left := items[i]
		right := items[j]
		for _, key := range keys {
			leftValue := strings.ToLower(cleanText(left[key]))
			rightValue := strings.ToLower(cleanText(right[key]))
			if leftValue != rightValue {
				return leftValue < rightValue
			}
		}
		return false
	})
}

func nullIfEmpty(value string) any {
	value = cleanText(value)
	if value == "" {
		return nil
	}
	return value
}

func isInternalRequest(remoteAddr string) bool {
	host := strings.TrimSpace(remoteAddr)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return host == "localhost"
	}
	return ip.IsLoopback()
}
