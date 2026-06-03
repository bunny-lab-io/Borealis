package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

type remoteShellTunnelConnector interface {
	connectTunnel(ctx context.Context, source *http.Request, agentID string, operatorID string) (map[string]any, int, map[string]any)
}

type legacyRemoteShellTunnelConnector struct {
	baseURL *url.URL
	auth    *authService
	client  *http.Client
}

type goRemoteShellTunnelConnector struct {
	service *vpnTunnelService
}

func registerRemoteShellRoutes(mux *http.ServeMux, auth *authService, legacyURL *url.URL, vpnRuntime *vpnTunnelService) error {
	signer, err := loadOrCreateAgentJWTSigner()
	if err != nil {
		return err
	}
	var connector remoteShellTunnelConnector
	if vpnRuntime != nil {
		connector = &goRemoteShellTunnelConnector{service: vpnRuntime}
	} else {
		connector = &legacyRemoteShellTunnelConnector{
			baseURL: legacyURL,
			auth:    auth,
			client:  &http.Client{Timeout: 30 * time.Second},
		}
	}
	mux.HandleFunc("POST /api/shell/establish", remoteShellEstablishHandler(auth, signer, connector))
	mux.HandleFunc("POST /api/shell/disconnect", remoteShellDisconnectHandler(auth))
	return nil
}

func remoteShellEstablishHandler(auth *authService, signer *agentJWTSigner, connector remoteShellTunnelConnector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		profile, err := auth.currentProfile(r.Context(), r)
		if err != nil {
			if isUnauthorizedAuthError(err) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
			return
		}
		body, ok := readRemoteShellJSON(w, r)
		if !ok {
			return
		}
		requestedAgentID := cleanText(body["agent_id"])
		if requestedAgentID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "agent_id_required"})
			return
		}
		store, ok := auth.store.(remoteOpsSessionStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "remote_shell_unavailable"})
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
			DeviceGUID:   normalizeCanonicalGUID(requestedAgentID),
			AgentID:      requestedAgentID,
			Capabilities: []string{"remote_shell"},
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
		if connector == nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "tunnel_unavailable"})
			return
		}
		tunnelPayload, tunnelStatus, tunnelErr := connector.connectTunnel(r.Context(), r, result.Device.AgentID, profile.Username)
		if tunnelErr != nil {
			writeJSON(w, tunnelStatus, tunnelErr)
			return
		}
		issued, err := signer.issueRemoteOpsSession(profile, result.Device, *result.Route, []string{"remote_shell"}, now, ttl)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "token_issue_failed"})
			return
		}
		response := copyMap(tunnelPayload)
		response["status"] = "ok"
		response["agent_socket"] = true
		response["shell_port"] = parseIntDefault(os.Getenv("BOREALIS_WIREGUARD_SHELL_PORT"), 47002)
		if cleanText(response["agent_id"]) == "" {
			response["agent_id"] = result.Device.AgentID
		}
		response["remote_ops_session"] = remoteShellSessionPayload(r, profile, result, issued)
		writeJSON(w, http.StatusOK, response)
	}
}

func remoteShellDisconnectHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		profile, err := auth.currentProfile(r.Context(), r)
		if err != nil {
			if isUnauthorizedAuthError(err) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
			return
		}
		body, ok := readRemoteShellJSON(w, r)
		if !ok {
			return
		}
		requestedAgentID := cleanText(body["agent_id"])
		if requestedAgentID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "agent_id_required"})
			return
		}
		store, ok := auth.store.(remoteOpsSessionStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "remote_shell_unavailable"})
			return
		}
		timeout := auth.timeout
		if timeout <= 0 {
			timeout = defaultAuthTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		_, status, err := store.createRemoteOpsSession(ctx, profile, remoteOpsSessionRequest{
			DeviceGUID:   normalizeCanonicalGUID(requestedAgentID),
			AgentID:      requestedAgentID,
			Capabilities: []string{"remote_shell"},
			Now:          time.Now().UTC(),
		})
		if err != nil {
			writeJSON(w, status, remoteOpsSessionErrorPayload(err))
			return
		}
		reason := firstText(cleanText(body["reason"]), "operator_disconnect")
		writeJSON(w, http.StatusOK, map[string]any{"status": "disconnected", "reason": reason})
	}
}

func readRemoteShellJSON(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
		return nil, false
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, true
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
		return nil, false
	}
	if body == nil {
		body = map[string]any{}
	}
	return body, true
}

func remoteShellSessionPayload(r *http.Request, profile operatorProfile, result remoteOpsSessionResult, issued issuedRemoteOpsSession) map[string]any {
	urls := remoteOpsWorkerURLs(r, result.Route)
	return map[string]any{
		"session_id":   issued.SessionID,
		"token_type":   "Bearer",
		"token":        issued.Token,
		"issued_at":    issued.IssuedAt,
		"expires_at":   issued.ExpiresAt,
		"expires_in":   issued.ExpiresIn,
		"capabilities": []string{"remote_shell"},
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
}

func (c *goRemoteShellTunnelConnector) connectTunnel(ctx context.Context, source *http.Request, agentID string, operatorID string) (map[string]any, int, map[string]any) {
	if c == nil || c.service == nil {
		return nil, http.StatusBadGateway, map[string]any{"error": "tunnel_unavailable"}
	}
	payload, err := c.service.connect(ctx, vpnConnectRequest{
		AgentID:       agentID,
		OperatorID:    operatorID,
		EndpointHost:  inferWireGuardEndpointHost(source),
		MarkActivity:  true,
		RequiredPorts: []int{parseIntDefault(os.Getenv("BOREALIS_WIREGUARD_SHELL_PORT"), 47002)},
	})
	if err != nil {
		return nil, http.StatusInternalServerError, map[string]any{"error": "connect_failed", "detail": err.Error()}
	}
	return payload, http.StatusOK, nil
}

func (c *legacyRemoteShellTunnelConnector) connectTunnel(ctx context.Context, source *http.Request, agentID string, operatorID string) (map[string]any, int, map[string]any) {
	if c == nil || c.baseURL == nil {
		return nil, http.StatusBadGateway, map[string]any{"error": "tunnel_unavailable"}
	}
	_ = operatorID
	payload, err := json.Marshal(map[string]any{"agent_id": agentID})
	if err != nil {
		return nil, http.StatusBadRequest, map[string]any{"error": "invalid_request"}
	}
	target := c.baseURL.ResolveReference(&url.URL{Path: "/api/tunnel/connect"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, http.StatusBadGateway, map[string]any{"error": "tunnel_request_failed", "message": err.Error()}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	for _, header := range []string{"Authorization", "Cookie", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-IP"} {
		for _, value := range source.Header.Values(header) {
			req.Header.Add(header, value)
		}
	}
	client := c.client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, http.StatusBadGateway, map[string]any{"error": "tunnel_unavailable", "message": err.Error()}
	}
	defer resp.Body.Close()
	var response map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&response); err != nil {
		if errors.Is(err, io.EOF) {
			response = map[string]any{}
		} else {
			return nil, http.StatusBadGateway, map[string]any{"error": "invalid_tunnel_response"}
		}
	}
	if response == nil {
		response = map[string]any{}
	}
	if resp.StatusCode >= 400 {
		if cleanText(response["error"]) == "" {
			response["error"] = "tunnel_unavailable"
		}
		return nil, resp.StatusCode, response
	}
	return response, resp.StatusCode, nil
}
