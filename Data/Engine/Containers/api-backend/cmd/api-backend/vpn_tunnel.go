package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/curve25519"
)

const (
	defaultWireGuardPort       = 30000
	defaultWireGuardEngineIP   = "10.255.0.1/32"
	defaultWireGuardPeerCIDR   = "10.255.0.0/16"
	defaultWireGuardInterface  = "borealis-wg"
	defaultWireGuardConfigName = "borealis-wg"
	wireGuardInputChain        = "BOREALIS-WG-INPUT"
	wireGuardForwardChain      = "BOREALIS-WG-FWD"
	defaultVPNTokenTTL         = 300 * time.Second
	defaultVPNIdleSeconds      = 900
	defaultVNCBackendPort      = 5900
)

type vpnWireGuardBackend interface {
	serverPublicKey() string
	buildPeerProfile(agentID string, virtualIP string, allowedPorts []int) map[string]any
	upsertPeer(peer map[string]any) error
	removePeer(agentID string, publicKey string) error
	reconcilePeers(peers []map[string]any) error
	checkListenerHealth(expectedPeerCount int) map[string]any
	checkPeerHealth(publicKey string) map[string]any
}

type vpnTunnelService struct {
	auth         *authService
	scriptSigner *agentScriptSigner
	wg           vpnWireGuardBackend
	enginePrefix netip.Prefix
	peerPrefix   netip.Prefix
	port         int
	publicPort   int
	allowPorts   []int
	persistent   bool
	idleSeconds  int

	mu              sync.Mutex
	ready           *sync.Cond
	leasesLoaded    bool
	ipLeases        map[string]string
	keyLeases       map[string]vpnClientKeys
	sessionsByAgent map[string]*vpnSession
	sessionsByID    map[string]*vpnSession
}

type vpnClientKeys struct {
	Private string
	Public  string
}

type vpnSession struct {
	TunnelID                   string
	AgentID                    string
	VirtualIP                  string
	ClientPublicKey            string
	ClientPrivateKey           string
	AllowedPorts               []int
	Token                      map[string]any
	CreatedAt                  time.Time
	ExpiresAt                  time.Time
	LastActivity               time.Time
	LastTransportProbeAt       time.Time
	LastTransportConfirmedAt   time.Time
	LastAgentReadyAt           time.Time
	LastAgentReadyTunnelID     string
	LastAgentReadyAllowedPorts []int
	LastAgentReadyReason       string
	LastAgentReadyServiceState string
	Operators                  map[string]struct{}
	Hostname                   string
	EndpointHost               string
}

func newVPNTunnelService(auth *authService) *vpnTunnelService {
	enginePrefix, peerPrefix := parseWireGuardRuntimePrefixes()
	port := parseIntDefault(os.Getenv("BOREALIS_WIREGUARD_PORT"), defaultWireGuardPort)
	publicPort := parseIntDefault(os.Getenv("BOREALIS_PUBLIC_WIREGUARD_PORT"), port)
	if publicPort <= 0 {
		publicPort = port
	}
	signer, _ := loadOrCreateScriptSigner()
	service := &vpnTunnelService{
		auth:            auth,
		scriptSigner:    signer,
		enginePrefix:    enginePrefix,
		peerPrefix:      peerPrefix,
		port:            port,
		publicPort:      publicPort,
		allowPorts:      parsePortListEnv("BOREALIS_WIREGUARD_PORT_ALLOWLIST", []int{parseIntDefault(os.Getenv("BOREALIS_WIREGUARD_SHELL_PORT"), 47002), parseIntDefault(os.Getenv("BOREALIS_VNC_PORT"), defaultVNCBackendPort), 22}),
		persistent:      parseBoolEnvDefault("BOREALIS_WIREGUARD_PERSISTENT", true),
		idleSeconds:     parseIntDefault(os.Getenv("BOREALIS_WIREGUARD_IDLE_SECONDS"), defaultVPNIdleSeconds),
		ipLeases:        map[string]string{},
		keyLeases:       map[string]vpnClientKeys{},
		sessionsByAgent: map[string]*vpnSession{},
		sessionsByID:    map[string]*vpnSession{},
	}
	service.ready = sync.NewCond(&service.mu)
	service.wg = newWireGuardRuntime(service.port, service.enginePrefix, service.peerPrefix, service.allowPorts)
	if service.idleSeconds < 60 {
		service.idleSeconds = 60
	}
	if !service.persistent {
		go service.idleLoop()
	}
	return service
}

func parsePrefixEnv(name string, fallback string) netip.Prefix {
	value := cleanText(os.Getenv(name))
	if value == "" {
		value = fallback
	}
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		prefix, _ = netip.ParsePrefix(fallback)
	}
	return prefix.Masked()
}

func parseWireGuardRuntimePrefixes() (netip.Prefix, netip.Prefix) {
	engineFallback := netip.MustParsePrefix(defaultWireGuardEngineIP)
	peerFallback := netip.MustParsePrefix(defaultWireGuardPeerCIDR)
	enginePrefix := parsePrefixEnv("BOREALIS_WIREGUARD_ENGINE_VIRTUAL_IP", defaultWireGuardEngineIP)
	peerPrefix := parsePrefixEnv("BOREALIS_WIREGUARD_PEER_NETWORK", defaultWireGuardPeerCIDR)
	if !validWireGuardEnginePrefix(enginePrefix) || !validWireGuardPeerPrefix(peerPrefix, enginePrefix) {
		return engineFallback, peerFallback
	}
	return enginePrefix, peerPrefix
}

func validWireGuardEnginePrefix(prefix netip.Prefix) bool {
	return prefix.IsValid() && prefix.Addr().Is4() && prefix.Addr().IsPrivate() && prefix.Bits() == 32
}

func validWireGuardPeerPrefix(prefix netip.Prefix, enginePrefix netip.Prefix) bool {
	return prefix.IsValid() &&
		prefix.Addr().Is4() &&
		prefix.Addr().IsPrivate() &&
		prefix.Bits() >= 16 &&
		prefix.Bits() <= 30 &&
		validWireGuardEnginePrefix(enginePrefix) &&
		prefix.Contains(enginePrefix.Addr())
}

func parsePortListEnv(name string, fallback []int) []int {
	raw := cleanText(os.Getenv(name))
	if raw == "" {
		return uniquePorts(fallback)
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n'
	})
	ports := make([]int, 0, len(parts))
	for _, part := range parts {
		port, err := strconv.Atoi(strings.TrimSpace(part))
		if err == nil && port >= 1 && port <= 65535 {
			ports = append(ports, port)
		}
	}
	if len(ports) == 0 {
		ports = fallback
	}
	return uniquePorts(ports)
}

func uniquePorts(values []int) []int {
	seen := map[int]struct{}{}
	out := []int{}
	for _, value := range values {
		if value < 1 || value > 65535 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func registerTunnelRoutes(mux *http.ServeMux, auth *authService, service *vpnTunnelService) {
	if service == nil {
		service = newVPNTunnelService(auth)
	}
	mux.HandleFunc("POST /api/tunnel/connect", tunnelConnectHandler(auth, service))
	mux.HandleFunc("GET /api/tunnel/status", tunnelStatusHandler(auth, service))
	mux.HandleFunc("GET /api/tunnel/connect/status", tunnelStatusHandler(auth, service))
	mux.HandleFunc("GET /api/tunnel/active", tunnelActiveHandler(auth, service))
}

func tunnelConnectHandler(auth *authService, service *vpnTunnelService) http.HandlerFunc {
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
		var body map[string]any
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
			return
		}
		requestedAgentID := cleanText(body["agent_id"])
		if requestedAgentID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "agent_id_required"})
			return
		}
		result, status, payloadErr := vpnAuthorizedDevice(r.Context(), auth, profile, requestedAgentID)
		if payloadErr != nil {
			writeJSON(w, status, payloadErr)
			return
		}
		payload, err := service.connect(r.Context(), vpnConnectRequest{
			AgentID:      result.Device.AgentID,
			OperatorID:   profile.Username,
			EndpointHost: inferWireGuardEndpointHost(r),
			MarkActivity: true,
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "connect_failed", "detail": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, payload)
	}
}

func tunnelStatusHandler(auth *authService, service *vpnTunnelService) http.HandlerFunc {
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
		requestedAgentID := cleanText(r.URL.Query().Get("agent_id"))
		if requestedAgentID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "agent_id_required"})
			return
		}
		result, status, payloadErr := vpnAuthorizedDevice(r.Context(), auth, profile, requestedAgentID)
		if payloadErr != nil {
			writeJSON(w, status, payloadErr)
			return
		}
		payload := service.status(result.Device.AgentID)
		agentSocket := false
		if result.Route != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			agentSocket = workerHostServiceRegistered(ctx, auth, result.Route, result.Device.Hostname, serviceModeFromAgentID(result.Device.AgentID))
			cancel()
		}
		if payload == nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"status":                       "down",
				"agent_id":                     result.Device.AgentID,
				"agent_socket":                 agentSocket,
				"listener_healthy":             false,
				"recovery_in_progress":         false,
				"last_recovery_attempt_at":     nil,
				"last_recovery_attempt_at_iso": "",
			})
			return
		}
		payload["agent_socket"] = agentSocket
		if cleanText(r.URL.Query().Get("bump")) != "" {
			service.bumpActivity(result.Device.AgentID)
		}
		writeJSON(w, http.StatusOK, payload)
	}
}

func tunnelActiveHandler(auth *authService, service *vpnTunnelService) http.HandlerFunc {
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
		sessions := service.listSessions()
		visible := []map[string]any{}
		for _, session := range sessions {
			agentID := cleanText(session["agent_id"])
			result, _, payloadErr := vpnAuthorizedDevice(r.Context(), auth, profile, agentID)
			if payloadErr != nil {
				continue
			}
			if result.Route != nil {
				ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
				session["agent_socket"] = workerHostServiceRegistered(ctx, auth, result.Route, result.Device.Hostname, serviceModeFromAgentID(result.Device.AgentID))
				cancel()
			} else {
				session["agent_socket"] = false
			}
			visible = append(visible, session)
		}
		writeJSON(w, http.StatusOK, map[string]any{"count": len(visible), "tunnels": visible})
	}
}

type vpnConnectRequest struct {
	AgentID       string
	OperatorID    string
	EndpointHost  string
	MarkActivity  bool
	RequiredPorts []int
}

func (s *vpnTunnelService) connect(ctx context.Context, request vpnConnectRequest) (map[string]any, error) {
	if s == nil {
		return nil, errors.New("tunnel_unavailable")
	}
	agentID := cleanText(request.AgentID)
	if agentID == "" {
		return nil, errors.New("agent_id_required")
	}
	s.loadLeases(ctx)
	now := time.Now().UTC()
	requiredPorts := uniquePorts(request.RequiredPorts)
	if len(requiredPorts) == 0 {
		requiredPorts = nil
	}

	s.mu.Lock()
	session := s.sessionsByAgent[agentID]
	if session != nil {
		if cleanText(request.OperatorID) != "" {
			session.Operators[cleanText(request.OperatorID)] = struct{}{}
		}
		if cleanText(request.EndpointHost) != "" && session.EndpointHost == "" {
			session.EndpointHost = cleanText(request.EndpointHost)
		}
		if request.MarkActivity {
			session.LastActivity = now
			session.LastTransportProbeAt = now
		}
		session.AllowedPorts = mergePorts(s.allowPorts, session.AllowedPorts, requiredPorts)
		if session.ExpiresAt.Before(now.Add(30 * time.Second)) {
			session.ExpiresAt = now.Add(defaultVPNTokenTTL)
			session.Token = s.issueToken(session.AgentID, session.TunnelID, session.ExpiresAt)
		}
		s.mu.Unlock()
		if err := s.upsertListenerPeer(session); err != nil {
			return nil, err
		}
		s.emitStart(ctx, session.payload(true), true)
		return session.payload(true), nil
	}

	keys, err := s.loadOrCreateClientKeys(ctx, agentID)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	virtualIP, err := s.allocateVirtualIPLocked(agentID)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	tunnelID, err := randomHex(16)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	expiresAt := now.Add(defaultVPNTokenTTL)
	session = &vpnSession{
		TunnelID:             tunnelID,
		AgentID:              agentID,
		VirtualIP:            virtualIP,
		ClientPrivateKey:     keys.Private,
		ClientPublicKey:      keys.Public,
		AllowedPorts:         mergePorts(s.allowPorts, requiredPorts),
		CreatedAt:            now,
		ExpiresAt:            expiresAt,
		LastActivity:         now,
		EndpointHost:         cleanText(request.EndpointHost),
		Operators:            map[string]struct{}{},
		LastTransportProbeAt: time.Time{},
	}
	if request.MarkActivity {
		session.LastTransportProbeAt = now
	}
	if cleanText(request.OperatorID) != "" {
		session.Operators[cleanText(request.OperatorID)] = struct{}{}
	}
	session.Token = s.issueToken(agentID, tunnelID, expiresAt)
	s.sessionsByAgent[agentID] = session
	s.sessionsByID[tunnelID] = session
	s.ipLeases[agentID] = virtualIP
	s.keyLeases[agentID] = keys
	s.mu.Unlock()

	if err := s.persistClientKeyLease(ctx, agentID, keys); err != nil {
		log.Printf("vpn key lease persist failed agent_id=%s err=%v", agentID, err)
	}
	if err := s.persistVirtualIPLease(ctx, agentID, virtualIP); err != nil {
		log.Printf("vpn ip lease persist failed agent_id=%s err=%v", agentID, err)
	}
	if err := s.upsertListenerPeer(session); err != nil {
		s.mu.Lock()
		delete(s.sessionsByAgent, agentID)
		delete(s.sessionsByID, tunnelID)
		s.mu.Unlock()
		return nil, err
	}
	payload := session.payload(true)
	s.emitStart(ctx, payload, true)
	return payload, nil
}

func (s *vpnTunnelService) status(agentID string) map[string]any {
	s.mu.Lock()
	session := s.sessionsByAgent[cleanText(agentID)]
	s.mu.Unlock()
	if session == nil {
		return nil
	}
	payload := session.payload(false)
	payload = mergeMaps(payload, s.sessionRuntimePayload(session, true))
	return payload
}

func (s *vpnTunnelService) listSessions() []map[string]any {
	s.mu.Lock()
	sessions := make([]*vpnSession, 0, len(s.sessionsByAgent))
	for _, session := range s.sessionsByAgent {
		sessions = append(sessions, session)
	}
	s.mu.Unlock()
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].AgentID < sessions[j].AgentID })
	out := make([]map[string]any, 0, len(sessions))
	for _, session := range sessions {
		payload := session.summary()
		payload = mergeMaps(payload, s.sessionRuntimePayload(session, false))
		out = append(out, payload)
	}
	return out
}

func (s *vpnTunnelService) sessionPayload(agentID string, includeToken bool) map[string]any {
	s.mu.Lock()
	session := s.sessionsByAgent[cleanText(agentID)]
	if session == nil {
		s.mu.Unlock()
		return nil
	}
	if includeToken && session.ExpiresAt.Before(time.Now().UTC().Add(30*time.Second)) {
		session.ExpiresAt = time.Now().UTC().Add(defaultVPNTokenTTL)
		session.Token = s.issueToken(session.AgentID, session.TunnelID, session.ExpiresAt)
	}
	payload := session.payload(includeToken)
	s.mu.Unlock()
	return payload
}

func (s *vpnTunnelService) requestAgentStart(ctx context.Context, agentID string, forceRestart bool, reason string, requiredPorts []int) map[string]any {
	s.mu.Lock()
	session := s.sessionsByAgent[cleanText(agentID)]
	if session == nil {
		s.mu.Unlock()
		return nil
	}
	session.AllowedPorts = mergePorts(s.allowPorts, session.AllowedPorts, requiredPorts)
	if session.ExpiresAt.Before(time.Now().UTC().Add(30 * time.Second)) {
		session.ExpiresAt = time.Now().UTC().Add(defaultVPNTokenTTL)
		session.Token = s.issueToken(session.AgentID, session.TunnelID, session.ExpiresAt)
	}
	payload := session.payload(true)
	if forceRestart {
		payload["force_restart"] = true
	}
	if cleanText(reason) != "" {
		payload["restart_reason"] = cleanText(reason)
	}
	s.mu.Unlock()
	_ = s.upsertListenerPeer(session)
	s.emitStart(ctx, payload, true)
	return payload
}

func (s *vpnTunnelService) requestRemoteShellRestart(ctx context.Context, agentID string, reason string) bool {
	agentID = cleanText(agentID)
	if agentID == "" || s == nil || s.auth == nil {
		return false
	}
	device, route := s.lookupDeviceRouteByAgentID(ctx, agentID)
	if device.AgentID == "" || route == nil {
		return false
	}
	result, _, workerErr := emitWorkerHostServiceEvent(ctx, s.auth, route, remoteShellRestartEventBody(device, agentID, reason), 6*time.Second)
	return workerErr == nil && boolFromAny(result["emitted"])
}

func remoteShellRestartEventBody(device remoteOpsSessionDevice, agentID string, reason string) map[string]any {
	agentID = cleanText(agentID)
	restartReason := firstText(cleanText(reason), "remote_shell_backend_unreachable")
	return map[string]any{
		"hostname":            device.Hostname,
		"service_mode":        serviceModeFromAgentID(agentID),
		"event_name":          "remote_shell_restart",
		"payload":             map[string]any{"agent_id": agentID, "reason": restartReason},
		"allow_pending":       true,
		"pending_ttl_seconds": 60,
	}
}

func (s *vpnTunnelService) recordAgentReady(agentID string, tunnelID string, allowedPorts []int, reason string, serviceState string, virtualIP string) map[string]any {
	agentID = cleanText(agentID)
	tunnelID = cleanText(tunnelID)
	if agentID == "" || tunnelID == "" {
		return nil
	}
	s.mu.Lock()
	session := s.sessionsByAgent[agentID]
	if session == nil || session.TunnelID != tunnelID {
		s.mu.Unlock()
		return nil
	}
	now := time.Now().UTC()
	session.LastAgentReadyAt = now
	session.LastAgentReadyTunnelID = tunnelID
	session.LastAgentReadyAllowedPorts = uniquePorts(firstPorts(allowedPorts, session.AllowedPorts))
	session.LastAgentReadyReason = cleanText(reason)
	session.LastAgentReadyServiceState = cleanText(serviceState)
	session.LastActivity = now
	s.ready.Broadcast()
	payload := s.dispatchReadyPayloadLocked(session, session.LastAgentReadyAllowedPorts)
	s.mu.Unlock()
	_ = virtualIP
	return payload
}

func (s *vpnTunnelService) waitForSessionsReady(agentIDs []string, requiredPorts []int, timeoutSeconds float64, pollIntervalSeconds float64) map[string]map[string]any {
	requested := []string{}
	seen := map[string]struct{}{}
	for _, agentID := range agentIDs {
		agentID = cleanText(agentID)
		if agentID == "" {
			continue
		}
		if _, ok := seen[agentID]; ok {
			continue
		}
		seen[agentID] = struct{}{}
		requested = append(requested, agentID)
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = 45
	}
	if pollIntervalSeconds <= 0 {
		pollIntervalSeconds = 0.5
	}
	deadline := time.Now().Add(time.Duration(timeoutSeconds * float64(time.Second)))
	out := map[string]map[string]any{}
	for {
		s.mu.Lock()
		out = map[string]map[string]any{}
		allReady := len(requested) > 0
		for _, agentID := range requested {
			session := s.sessionsByAgent[agentID]
			if session == nil {
				allReady = false
				continue
			}
			payload := s.dispatchReadyPayloadLocked(session, requiredPorts)
			out[agentID] = payload
			if !boolFromAny(payload["dispatch_ready"]) {
				allReady = false
			}
		}
		if allReady || time.Now().After(deadline) {
			s.mu.Unlock()
			return out
		}
		wait := time.Duration(pollIntervalSeconds * float64(time.Second))
		if remaining := time.Until(deadline); wait > remaining {
			wait = remaining
		}
		timer := time.NewTimer(wait)
		s.mu.Unlock()
		<-timer.C
	}
}

func (s *vpnTunnelService) markTransportRequired(agentID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessionsByAgent[cleanText(agentID)]
	if session == nil {
		return false
	}
	now := time.Now().UTC()
	session.LastActivity = now
	session.LastTransportProbeAt = now
	return true
}

func (s *vpnTunnelService) confirmTransportSuccess(agentID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessionsByAgent[cleanText(agentID)]
	if session == nil {
		return false
	}
	now := time.Now().UTC()
	session.LastActivity = now
	session.LastTransportConfirmedAt = now
	return true
}

func (s *vpnTunnelService) bumpActivity(agentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessionsByAgent[cleanText(agentID)]
	if session == nil {
		return
	}
	now := time.Now().UTC()
	session.LastActivity = now
	session.LastTransportProbeAt = now
}

func (s *vpnTunnelService) disconnect(ctx context.Context, agentID string, reason string, force bool) bool {
	s.mu.Lock()
	session := s.sessionsByAgent[cleanText(agentID)]
	if session == nil {
		s.mu.Unlock()
		return false
	}
	if s.persistent && !force {
		session.LastActivity = time.Now().UTC()
		s.mu.Unlock()
		return true
	}
	delete(s.sessionsByAgent, session.AgentID)
	delete(s.sessionsByID, session.TunnelID)
	s.mu.Unlock()
	_ = s.wg.removePeer(session.AgentID, session.ClientPublicKey)
	s.emitStop(ctx, session, reason)
	return true
}

func (s *vpnTunnelService) sessionRuntimePayload(session *vpnSession, refresh bool) map[string]any {
	health := s.wg.checkListenerHealth(0)
	peerHealth := s.wg.checkPeerHealth(session.ClientPublicKey)
	listenerHealthy := boolFromAny(health["healthy"])
	peerPresent := boolFromAny(peerHealth["peer_present"])
	transportReady := listenerHealthy && boolFromAny(peerHealth["healthy"])
	status := "up"
	if !transportReady {
		status = "recovering"
	}
	agentReady := s.agentReadyPayload(session, nil)
	return mergeMaps(map[string]any{
		"status":                          status,
		"listener_healthy":                transportReady,
		"recovery_in_progress":            !transportReady,
		"last_recovery_attempt_at":        nil,
		"last_recovery_attempt_at_iso":    "",
		"transport_ready":                 transportReady,
		"peer_present":                    peerPresent,
		"peer_healthy":                    boolFromAny(peerHealth["healthy"]),
		"peer_health_reason":              cleanText(firstNonEmpty(peerHealth["reason"], "unknown")),
		"last_handshake_at":               firstNonEmpty(peerHealth["last_handshake_at"], nil),
		"last_handshake_at_iso":           firstNonEmpty(peerHealth["last_handshake_at_iso"], ""),
		"handshake_age_seconds":           firstNonEmpty(peerHealth["handshake_age_seconds"], nil),
		"last_transport_probe_at":         unixOrNil(session.LastTransportProbeAt),
		"last_transport_probe_at_iso":     isoOrEmpty(session.LastTransportProbeAt),
		"last_transport_confirmed_at":     unixOrNil(session.LastTransportConfirmedAt),
		"last_transport_confirmed_at_iso": isoOrEmpty(session.LastTransportConfirmedAt),
	}, agentReady)
}

func (s *vpnTunnelService) dispatchReadyPayloadLocked(session *vpnSession, requiredPorts []int) map[string]any {
	payload := session.payload(false)
	runtimePayload := s.sessionRuntimePayload(session, false)
	agentReady := s.agentReadyPayload(session, requiredPorts)
	dispatchReady := boolFromAny(runtimePayload["listener_healthy"]) && !boolFromAny(runtimePayload["recovery_in_progress"]) && boolFromAny(agentReady["agent_ready"])
	payload = mergeMaps(payload, runtimePayload)
	payload = mergeMaps(payload, agentReady)
	payload["dispatch_ready"] = dispatchReady
	if dispatchReady {
		payload["dispatch_ready_reason"] = "ready"
	} else if !boolFromAny(agentReady["agent_ready"]) {
		payload["dispatch_ready_reason"] = "agent_ready_missing"
	} else {
		payload["dispatch_ready_reason"] = firstText(cleanText(runtimePayload["peer_health_reason"]), "listener_unhealthy")
	}
	return payload
}

func (s *vpnTunnelService) agentReadyPayload(session *vpnSession, requiredPorts []int) map[string]any {
	required := uniquePorts(requiredPorts)
	readyPorts := uniquePorts(session.LastAgentReadyAllowedPorts)
	portsReady := true
	for _, port := range required {
		if !portIn(port, readyPorts) {
			portsReady = false
			break
		}
	}
	ready := !session.LastAgentReadyAt.IsZero() && session.LastAgentReadyTunnelID == session.TunnelID && portsReady
	return map[string]any{
		"agent_ready":                ready,
		"agent_ready_reason":         session.LastAgentReadyReason,
		"agent_ready_service_state":  session.LastAgentReadyServiceState,
		"agent_ready_required_ports": required,
		"agent_ready_allowed_ports":  readyPorts,
		"agent_ready_tunnel_id":      session.LastAgentReadyTunnelID,
		"last_agent_ready_at":        unixOrNil(session.LastAgentReadyAt),
		"last_agent_ready_at_iso":    isoOrEmpty(session.LastAgentReadyAt),
	}
}

func (s *vpnTunnelService) upsertListenerPeer(session *vpnSession) error {
	if s == nil || s.wg == nil || session == nil {
		return errors.New("wireguard_unavailable")
	}
	peer := s.wg.buildPeerProfile(session.AgentID, session.VirtualIP, session.AllowedPorts)
	peer["public_key"] = session.ClientPublicKey
	return s.wg.upsertPeer(peer)
}

func (s *vpnTunnelService) emitStart(ctx context.Context, payload map[string]any, allowPending bool) {
	agentID := cleanText(payload["agent_id"])
	if agentID == "" || s == nil || s.auth == nil {
		return
	}
	device, route := s.lookupDeviceRouteByAgentID(ctx, agentID)
	if device.AgentID == "" || route == nil {
		return
	}
	body := map[string]any{
		"hostname":            device.Hostname,
		"service_mode":        serviceModeFromAgentID(agentID),
		"event_name":          "vpn_tunnel_start",
		"payload":             payload,
		"allow_pending":       allowPending,
		"pending_ttl_seconds": 180,
	}
	_, _, _ = emitWorkerHostServiceEvent(ctx, s.auth, route, body, 6*time.Second)
}

func (s *vpnTunnelService) emitStop(ctx context.Context, session *vpnSession, reason string) {
	if session == nil || s == nil || s.auth == nil {
		return
	}
	device, route := s.lookupDeviceRouteByAgentID(ctx, session.AgentID)
	if device.AgentID == "" || route == nil {
		return
	}
	_, _, _ = emitWorkerHostServiceEvent(ctx, s.auth, route, map[string]any{
		"hostname":     device.Hostname,
		"service_mode": serviceModeFromAgentID(session.AgentID),
		"event_name":   "vpn_tunnel_stop",
		"payload": map[string]any{
			"agent_id":  session.AgentID,
			"tunnel_id": session.TunnelID,
			"reason":    firstText(cleanText(reason), "operator_stop"),
		},
	}, 6*time.Second)
}

func (s *vpnTunnelService) lookupDeviceRouteByAgentID(ctx context.Context, agentID string) (remoteOpsSessionDevice, *agentWorkerRoute) {
	store, ok := s.auth.store.(*postgresOperatorStore)
	if !ok || store == nil {
		return remoteOpsSessionDevice{}, nil
	}
	conn, err := store.db.Conn(ctx)
	if err != nil {
		return remoteOpsSessionDevice{}, nil
	}
	defer conn.Close()
	device, err := lookupRemoteOpsDevice(ctx, conn, remoteOpsSessionRequest{AgentID: agentID})
	if err != nil || device.AgentID == "" || device.SiteID == nil {
		return device, nil
	}
	route, _ := fetchAgentWorkerRoute(ctx, conn, *device.SiteID)
	return device, route
}

func (s *vpnTunnelService) loadOrCreateClientKeys(ctx context.Context, agentID string) (vpnClientKeys, error) {
	if keys := s.keyLeases[agentID]; keys.Private != "" && keys.Public != "" {
		return keys, nil
	}
	keys, err := generateWireGuardKeyPair()
	if err != nil {
		return vpnClientKeys{}, err
	}
	return keys, nil
}

func generateWireGuardKeyPair() (vpnClientKeys, error) {
	private := make([]byte, 32)
	if _, err := rand.Read(private); err != nil {
		return vpnClientKeys{}, err
	}
	private[0] &= 248
	private[31] &= 127
	private[31] |= 64
	public, err := curve25519.X25519(private, curve25519.Basepoint)
	if err != nil {
		return vpnClientKeys{}, err
	}
	return vpnClientKeys{
		Private: base64.StdEncoding.EncodeToString(private),
		Public:  base64.StdEncoding.EncodeToString(public),
	}, nil
}

func (s *vpnTunnelService) issueToken(agentID string, tunnelID string, expiresAt time.Time) map[string]any {
	now := time.Now().UTC()
	token := map[string]any{
		"agent_id":   agentID,
		"tunnel_id":  tunnelID,
		"port":       s.port,
		"expires_at": float64(expiresAt.Unix()),
		"issued_at":  float64(now.Unix()),
	}
	signer := s.scriptSigner
	if signer == nil {
		signer, _ = loadOrCreateScriptSigner()
		s.scriptSigner = signer
	}
	if signer != nil && len(signer.privateKey) > 0 {
		payload, err := json.Marshal(token)
		if err == nil {
			token["signature"] = base64.StdEncoding.EncodeToString(ed25519.Sign(signer.privateKey, payload))
			token["signing_key"] = scriptSigningKeyB64(signer)
			token["sig_alg"] = "ed25519"
		}
	}
	return token
}

func (session *vpnSession) payload(includeToken bool) map[string]any {
	endpointHost := cleanText(session.EndpointHost)
	if endpointHost == "" {
		endpointHost = cleanText(os.Getenv("BOREALIS_PUBLIC_WIREGUARD_HOST"))
	}
	if endpointHost == "" {
		endpointHost = "10.255.0.1"
	}
	if strings.Contains(endpointHost, ":") && !strings.HasPrefix(endpointHost, "[") {
		endpointHost = "[" + endpointHost + "]"
	}
	engineIP := cleanText(os.Getenv("BOREALIS_WIREGUARD_ENGINE_VIRTUAL_IP"))
	if engineIP == "" {
		engineIP = defaultWireGuardEngineIP
	}
	engineAddr := strings.Split(engineIP, "/")[0]
	port := parseIntDefault(os.Getenv("BOREALIS_PUBLIC_WIREGUARD_PORT"), parseIntDefault(os.Getenv("BOREALIS_WIREGUARD_PORT"), defaultWireGuardPort))
	payload := map[string]any{
		"tunnel_id":           session.TunnelID,
		"agent_id":            session.AgentID,
		"virtual_ip":          session.VirtualIP,
		"engine_virtual_ip":   engineAddr,
		"allowed_ips":         engineAddr + "/32",
		"endpoint":            endpointHost + ":" + strconv.Itoa(port),
		"server_public_key":   cleanText(os.Getenv("BOREALIS_WIREGUARD_SERVER_PUBLIC_KEY")),
		"client_public_key":   session.ClientPublicKey,
		"client_private_key":  session.ClientPrivateKey,
		"idle_seconds":        0,
		"allowed_ports":       session.AllowedPorts,
		"connected_operators": len(session.Operators),
	}
	if includeToken {
		payload["token"] = session.Token
	}
	return payload
}

func (session *vpnSession) summary() map[string]any {
	payload := session.payload(false)
	payload["created_at"] = session.CreatedAt.Unix()
	payload["created_at_iso"] = session.CreatedAt.Format(time.RFC3339)
	payload["last_activity"] = session.LastActivity.Unix()
	payload["last_activity_iso"] = session.LastActivity.Format(time.RFC3339)
	payload["expires_at"] = session.ExpiresAt.Unix()
	payload["expires_at_iso"] = session.ExpiresAt.Format(time.RFC3339)
	payload["status"] = "up"
	return payload
}

func (s *vpnTunnelService) allocateVirtualIPLocked(agentID string) (string, error) {
	if existing := s.ipLeases[agentID]; existing != "" {
		if s.usablePeerVirtualIP(existing) {
			return existing, nil
		}
		delete(s.ipLeases, agentID)
	}
	reserved := map[string]struct{}{}
	for owner, ip := range s.ipLeases {
		if owner != agentID && s.usablePeerVirtualIP(ip) {
			reserved[ip] = struct{}{}
		}
	}
	for owner, session := range s.sessionsByAgent {
		if owner != agentID && s.usablePeerVirtualIP(session.VirtualIP) {
			reserved[session.VirtualIP] = struct{}{}
		}
	}
	for ip := s.peerPrefix.Addr(); s.peerPrefix.Contains(ip); ip = nextAddr(ip) {
		if !usablePeerVirtualAddr(ip, s.peerPrefix, s.enginePrefix) {
			continue
		}
		candidate := ip.String() + "/32"
		if _, ok := reserved[candidate]; ok {
			continue
		}
		return candidate, nil
	}
	return "", errors.New("vpn_ip_pool_exhausted")
}

func (s *vpnTunnelService) usablePeerVirtualIP(value string) bool {
	addr, ok := parseWireGuardHostRouteAddr(value)
	return ok && usablePeerVirtualAddr(addr, s.peerPrefix, s.enginePrefix)
}

func parseWireGuardHostRouteAddr(value string) (netip.Addr, bool) {
	prefix, err := netip.ParsePrefix(cleanText(value))
	if err != nil {
		return netip.Addr{}, false
	}
	prefix = prefix.Masked()
	if !prefix.Addr().Is4() || prefix.Bits() != 32 {
		return netip.Addr{}, false
	}
	return prefix.Addr(), true
}

func peerPrefixBoundaryAddrs(prefix netip.Prefix) (netip.Addr, netip.Addr, bool) {
	prefix = prefix.Masked()
	if !prefix.IsValid() || !prefix.Addr().Is4() {
		return netip.Addr{}, netip.Addr{}, false
	}
	network := prefix.Addr()
	raw := network.As4()
	hostBits := 32 - prefix.Bits()
	for bit := 0; bit < hostBits; bit++ {
		index := len(raw) - 1 - bit/8
		raw[index] |= byte(1 << uint(bit%8))
	}
	return network, netip.AddrFrom4(raw), true
}

func usablePeerVirtualAddr(addr netip.Addr, peerPrefix netip.Prefix, enginePrefix netip.Prefix) bool {
	if !addr.Is4() || !peerPrefix.Contains(addr) || addr == enginePrefix.Addr() {
		return false
	}
	network, broadcast, ok := peerPrefixBoundaryAddrs(peerPrefix)
	if !ok {
		return false
	}
	return addr != network && addr != broadcast
}

func nextAddr(ip netip.Addr) netip.Addr {
	if !ip.Is4() {
		return ip.Next()
	}
	raw := ip.As4()
	for i := len(raw) - 1; i >= 0; i-- {
		raw[i]++
		if raw[i] != 0 {
			break
		}
	}
	return netip.AddrFrom4(raw)
}

func (s *vpnTunnelService) loadLeases(ctx context.Context) {
	s.mu.Lock()
	if s.leasesLoaded {
		s.mu.Unlock()
		return
	}
	s.leasesLoaded = true
	s.mu.Unlock()
	db := vpnDB(s.auth)
	if db == nil {
		return
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return
	}
	defer conn.Close()
	_, _ = conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS engine.device_vpn_ip_leases (agent_id TEXT PRIMARY KEY, virtual_ip TEXT NOT NULL, updated_at TEXT NOT NULL)`)
	_, _ = conn.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS uq_device_vpn_ip_leases_virtual_ip ON engine.device_vpn_ip_leases(virtual_ip)`)
	_, _ = conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS engine.device_vpn_key_leases (agent_id TEXT PRIMARY KEY, client_private_key TEXT NOT NULL, client_public_key TEXT NOT NULL, updated_at TEXT NOT NULL)`)
	rows, err := conn.QueryContext(ctx, `SELECT agent_id, virtual_ip FROM engine.device_vpn_ip_leases`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var agentID, virtualIP string
			if rows.Scan(&agentID, &virtualIP) == nil && cleanText(agentID) != "" && cleanText(virtualIP) != "" {
				if !s.usablePeerVirtualIP(virtualIP) {
					log.Printf("vpn ip lease ignored agent_id=%s virtual_ip=%s reason=reserved_or_invalid", cleanText(agentID), cleanText(virtualIP))
					continue
				}
				s.mu.Lock()
				s.ipLeases[cleanText(agentID)] = cleanText(virtualIP)
				s.mu.Unlock()
			}
		}
	}
	keyRows, err := conn.QueryContext(ctx, `SELECT agent_id, client_private_key, client_public_key FROM engine.device_vpn_key_leases`)
	if err == nil {
		defer keyRows.Close()
		for keyRows.Next() {
			var agentID, privateKey, publicKey string
			if keyRows.Scan(&agentID, &privateKey, &publicKey) == nil && cleanText(agentID) != "" && cleanText(privateKey) != "" && cleanText(publicKey) != "" {
				s.mu.Lock()
				s.keyLeases[cleanText(agentID)] = vpnClientKeys{Private: cleanText(privateKey), Public: cleanText(publicKey)}
				s.mu.Unlock()
			}
		}
	}
}

func (s *vpnTunnelService) persistVirtualIPLease(ctx context.Context, agentID string, virtualIP string) error {
	db := vpnDB(s.auth)
	if db == nil {
		return nil
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, _ = conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS engine.device_vpn_ip_leases (agent_id TEXT PRIMARY KEY, virtual_ip TEXT NOT NULL, updated_at TEXT NOT NULL)`)
	_, err = conn.ExecContext(ctx, `INSERT INTO engine.device_vpn_ip_leases(agent_id, virtual_ip, updated_at) VALUES($1,$2,$3) ON CONFLICT(agent_id) DO UPDATE SET virtual_ip=EXCLUDED.virtual_ip, updated_at=EXCLUDED.updated_at`, agentID, virtualIP, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *vpnTunnelService) persistClientKeyLease(ctx context.Context, agentID string, keys vpnClientKeys) error {
	db := vpnDB(s.auth)
	if db == nil {
		return nil
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, _ = conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS engine.device_vpn_key_leases (agent_id TEXT PRIMARY KEY, client_private_key TEXT NOT NULL, client_public_key TEXT NOT NULL, updated_at TEXT NOT NULL)`)
	_, err = conn.ExecContext(ctx, `INSERT INTO engine.device_vpn_key_leases(agent_id, client_private_key, client_public_key, updated_at) VALUES($1,$2,$3,$4) ON CONFLICT(agent_id) DO UPDATE SET client_private_key=EXCLUDED.client_private_key, client_public_key=EXCLUDED.client_public_key, updated_at=EXCLUDED.updated_at`, agentID, keys.Private, keys.Public, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func vpnDB(auth *authService) *sql.DB {
	if auth == nil {
		return nil
	}
	store, ok := auth.store.(*postgresOperatorStore)
	if !ok || store == nil {
		return nil
	}
	return store.db
}

func vpnAuthorizedDevice(ctx context.Context, auth *authService, profile operatorProfile, requestedAgentID string) (remoteOpsSessionResult, int, map[string]any) {
	store, ok := auth.store.(remoteOpsSessionStore)
	if !ok {
		return remoteOpsSessionResult{}, http.StatusBadGateway, map[string]any{"error": "remote_ops_session_unavailable"}
	}
	result, status, err := store.createRemoteOpsSession(ctx, profile, remoteOpsSessionRequest{
		DeviceGUID:   normalizeCanonicalGUID(requestedAgentID),
		AgentID:      requestedAgentID,
		Capabilities: []string{"remote_shell"},
		Now:          time.Now().UTC(),
	})
	if err != nil {
		return remoteOpsSessionResult{}, status, remoteOpsSessionErrorPayload(err)
	}
	return result, http.StatusOK, nil
}

func inferWireGuardEndpointHost(r *http.Request) string {
	if value := cleanText(os.Getenv("BOREALIS_PUBLIC_WIREGUARD_HOST")); value != "" {
		return value
	}
	for _, name := range []string{"BOREALIS_AGENT_PUBLIC_BASE_URL", "BOREALIS_PUBLIC_BASE_URL", "PUBLIC_BASE_URL"} {
		raw := cleanText(os.Getenv(name))
		if raw == "" {
			continue
		}
		parsed := raw
		if !strings.Contains(parsed, "://") {
			parsed = "//" + parsed
		}
		if u, err := urlParseHost(parsed); err == nil && u != "" {
			return u
		}
	}
	host := firstHeaderValue(r, "X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	if strings.Contains(host, ":") {
		if parsedHost, _, err := net.SplitHostPort(host); err == nil {
			host = parsedHost
		}
	}
	return strings.Trim(host, "[]")
}

func urlParseHost(raw string) (string, error) {
	u, err := urlParse(raw)
	if err != nil {
		return "", err
	}
	return u, nil
}

func urlParse(raw string) (string, error) {
	type hostParser struct {
		Scheme string
		Host   string
	}
	_ = hostParser{}
	// Avoid importing net/url only for one small helper in this file.
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	parts := strings.SplitN(raw, "://", 2)
	if len(parts) != 2 {
		return "", errors.New("invalid_url")
	}
	host := parts[1]
	if slash := strings.Index(host, "/"); slash >= 0 {
		host = host[:slash]
	}
	if strings.Contains(host, "@") {
		host = host[strings.LastIndex(host, "@")+1:]
	}
	if strings.HasPrefix(host, "[") {
		if end := strings.Index(host, "]"); end >= 0 {
			return strings.Trim(host[:end+1], "[]"), nil
		}
	}
	if strings.Contains(host, ":") {
		host = strings.Split(host, ":")[0]
	}
	if host == "" {
		return "", errors.New("missing_host")
	}
	return host, nil
}

func serviceModeFromAgentID(agentID string) string {
	suffix := strings.ToLower(cleanText(agentID))
	if idx := strings.LastIndex(suffix, "_"); idx >= 0 && idx+1 < len(suffix) {
		suffix = suffix[idx+1:]
	}
	switch suffix {
	case "user", "interactive":
		return "user"
	default:
		return "system"
	}
}

func mergePorts(groups ...[]int) []int {
	out := []int{}
	for _, group := range groups {
		out = append(out, group...)
	}
	return uniquePorts(out)
}

func firstPorts(primary []int, fallback []int) []int {
	if len(primary) > 0 {
		return primary
	}
	return fallback
}

func portIn(port int, ports []int) bool {
	for _, candidate := range ports {
		if candidate == port {
			return true
		}
	}
	return false
}

func mergeMaps(base map[string]any, extra map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range base {
		out[key] = value
	}
	for key, value := range extra {
		out[key] = value
	}
	return out
}

func unixOrNil(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.Unix()
}

func isoOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func (s *vpnTunnelService) idleLoop() {
	for {
		time.Sleep(10 * time.Second)
		now := time.Now().UTC()
		expired := []string{}
		s.mu.Lock()
		for agentID, session := range s.sessionsByAgent {
			if session.LastActivity.Add(time.Duration(s.idleSeconds) * time.Second).Before(now) {
				expired = append(expired, agentID)
			}
		}
		s.mu.Unlock()
		for _, agentID := range expired {
			s.disconnect(context.Background(), agentID, "idle_timeout", true)
		}
	}
}

type wireGuardRuntime struct {
	port           int
	enginePrefix   netip.Prefix
	peerPrefix     netip.Prefix
	allowPorts     []int
	privateKeyPath string
	publicKeyPath  string
	configRoot     string
	interfaceName  string
	serverPrivate  string
	serverPublic   string
	mu             sync.Mutex
	managedPeers   map[string]map[string]any
	commandRunner  func([]string) (int, string, string)
}

func newWireGuardRuntime(port int, enginePrefix netip.Prefix, peerPrefix netip.Prefix, allowPorts []int) *wireGuardRuntime {
	root := cleanText(os.Getenv("BOREALIS_WIREGUARD_KEY_ROOT"))
	if root == "" {
		engineRoot := firstText(cleanText(os.Getenv("BOREALIS_ENGINE_ROOT")), "/opt/Borealis/Engine")
		root = filepath.Join(engineRoot, "Services", "wireguard-tunnel", "secrets")
	}
	configRoot := cleanText(os.Getenv("BOREALIS_WIREGUARD_CONFIG_ROOT"))
	if configRoot == "" {
		engineRoot := firstText(cleanText(os.Getenv("BOREALIS_ENGINE_ROOT")), "/opt/Borealis/Engine")
		configRoot = filepath.Join(engineRoot, "Services", "wireguard-tunnel", "config")
	}
	runtime := &wireGuardRuntime{
		port:           port,
		enginePrefix:   enginePrefix,
		peerPrefix:     peerPrefix,
		allowPorts:     uniquePorts(allowPorts),
		privateKeyPath: filepath.Join(root, "server_private.key"),
		publicKeyPath:  filepath.Join(root, "server_public.key"),
		configRoot:     configRoot,
		interfaceName:  sanitizeInterfaceName(firstText(cleanText(os.Getenv("BOREALIS_WIREGUARD_INTERFACE")), defaultWireGuardInterface)),
		managedPeers:   map[string]map[string]any{},
	}
	runtime.serverPrivate, runtime.serverPublic = runtime.ensureServerKeys()
	if runtime.serverPublic != "" {
		os.Setenv("BOREALIS_WIREGUARD_SERVER_PUBLIC_KEY", runtime.serverPublic)
	}
	return runtime
}

func sanitizeInterfaceName(value string) string {
	value = strings.ToLower(cleanText(value))
	out := strings.Builder{}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			out.WriteRune(r)
		}
	}
	cleaned := out.String()
	if cleaned == "" {
		cleaned = defaultWireGuardInterface
	}
	if len(cleaned) > 15 {
		cleaned = cleaned[:15]
	}
	return cleaned
}

func (w *wireGuardRuntime) serverPublicKey() string {
	return w.serverPublic
}

func (w *wireGuardRuntime) ensureServerKeys() (string, string) {
	if existingPrivate, err := os.ReadFile(w.privateKeyPath); err == nil {
		if existingPublic, pubErr := os.ReadFile(w.publicKeyPath); pubErr == nil {
			priv := cleanText(string(existingPrivate))
			pub := cleanText(string(existingPublic))
			if priv != "" && pub != "" {
				return priv, pub
			}
		}
	}
	keys, err := generateWireGuardKeyPair()
	if err != nil {
		return "", ""
	}
	_ = os.MkdirAll(filepath.Dir(w.privateKeyPath), 0o750)
	_ = os.Chmod(filepath.Dir(w.privateKeyPath), 0o750)
	_ = os.WriteFile(w.privateKeyPath, []byte(keys.Private+"\n"), 0o640)
	_ = os.WriteFile(w.publicKeyPath, []byte(keys.Public+"\n"), 0o640)
	return keys.Private, keys.Public
}

func (w *wireGuardRuntime) buildPeerProfile(agentID string, virtualIP string, allowedPorts []int) map[string]any {
	ports := uniquePorts(allowedPorts)
	if len(ports) == 0 {
		ports = w.allowPorts
	}
	return map[string]any{
		"agent_id":          cleanText(agentID),
		"virtual_ip":        cleanText(virtualIP),
		"allowed_ips":       []string{cleanText(virtualIP)},
		"endpoint":          w.enginePrefix.Addr().String() + ":" + strconv.Itoa(w.port),
		"client_to_client":  false,
		"engine_virtual_ip": w.enginePrefix.Addr().String(),
		"engine_interface":  w.enginePrefix.String(),
		"allowed_ports":     ports,
	}
}

func (w *wireGuardRuntime) upsertPeer(peer map[string]any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	agentID := cleanText(peer["agent_id"])
	publicKey := cleanText(peer["public_key"])
	allowedIPs := normalizeStringSlice(peer["allowed_ips"])
	if agentID == "" || publicKey == "" || len(allowedIPs) == 0 {
		return errors.New("invalid_wireguard_peer")
	}
	normalizedIPs, err := w.validatePeerPolicyLocked(agentID, publicKey, allowedIPs, w.occupiedPeerPolicyLocked(agentID))
	if err != nil {
		return err
	}
	peer = copyMap(peer)
	peer["allowed_ips"] = normalizedIPs
	if err := w.ensureListenerLocked(); err != nil {
		return err
	}
	if previous := w.managedPeers[agentID]; previous != nil {
		previousKey := cleanText(previous["public_key"])
		if previousKey != "" && previousKey != publicKey {
			_ = w.removePeerLocked(agentID, previousKey)
		}
	}
	args := []string{w.bin("wg"), "set", w.interfaceName, "peer", publicKey, "allowed-ips", strings.Join(normalizedIPs, ",")}
	code, out, errOut := w.runCommand(args)
	if code != 0 {
		return fmt.Errorf("wireguard_peer_upsert_failed: %s", firstText(errOut, out, "unknown"))
	}
	w.managedPeers[agentID] = copyMap(peer)
	return nil
}

func (w *wireGuardRuntime) removePeer(agentID string, publicKey string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.removePeerLocked(agentID, publicKey)
}

func (w *wireGuardRuntime) removePeerLocked(agentID string, publicKey string) error {
	publicKey = cleanText(publicKey)
	if publicKey == "" {
		if peer := w.managedPeers[agentID]; peer != nil {
			publicKey = cleanText(peer["public_key"])
		}
	}
	delete(w.managedPeers, agentID)
	if publicKey == "" {
		return nil
	}
	code, out, errOut := w.runCommand([]string{w.bin("wg"), "set", w.interfaceName, "peer", publicKey, "remove"})
	if code != 0 {
		return fmt.Errorf("wireguard_peer_remove_failed: %s", firstText(errOut, out, "unknown"))
	}
	return nil
}

func (w *wireGuardRuntime) reconcilePeers(peers []map[string]any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	desired := map[string]map[string]any{}
	desiredKeys := map[string]struct{}{}
	occupied := peerPolicyOwners{AllowedIPs: map[string]string{}, PublicKeys: map[string]string{}}
	for _, peer := range peers {
		agentID := cleanText(peer["agent_id"])
		publicKey := cleanText(peer["public_key"])
		if agentID == "" || publicKey == "" {
			continue
		}
		allowedIPs, err := w.validatePeerPolicyLocked(agentID, publicKey, normalizeStringSlice(peer["allowed_ips"]), occupied)
		if err != nil {
			return err
		}
		normalized := copyMap(peer)
		normalized["allowed_ips"] = allowedIPs
		desired[agentID] = normalized
		desiredKeys[publicKey] = struct{}{}
	}
	if err := w.ensureListenerLocked(); err != nil {
		return err
	}
	for _, current := range w.currentPeersLocked() {
		if _, ok := desiredKeys[current]; !ok {
			_ = w.removePeerLocked("", current)
		}
	}
	for agentID, peer := range desired {
		publicKey := cleanText(peer["public_key"])
		allowedIPs := normalizeStringSlice(peer["allowed_ips"])
		code, out, errOut := w.runCommand([]string{w.bin("wg"), "set", w.interfaceName, "peer", publicKey, "allowed-ips", strings.Join(allowedIPs, ",")})
		if code != 0 {
			return fmt.Errorf("wireguard_peer_reconcile_failed: %s", firstText(errOut, out, "unknown"))
		}
		w.managedPeers[agentID] = copyMap(peer)
	}
	return nil
}

func (w *wireGuardRuntime) checkListenerHealth(expectedPeerCount int) map[string]any {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.interfaceExistsLocked() {
		return map[string]any{"healthy": false, "reason": "interface_down", "peer_count": 0}
	}
	peers := w.currentPeersLocked()
	healthy := len(peers) > 0 || expectedPeerCount == 0
	reason := "listener_running"
	if !healthy {
		reason = "no_peers_configured"
	}
	if expectedPeerCount > 0 && len(peers) != expectedPeerCount {
		healthy = false
		reason = "peer_count_mismatch"
	}
	return map[string]any{"healthy": healthy, "reason": reason, "peer_count": len(peers), "service_state": "RUNNING"}
}

func (w *wireGuardRuntime) checkPeerHealth(publicKey string) map[string]any {
	w.mu.Lock()
	defer w.mu.Unlock()
	publicKey = cleanText(publicKey)
	if publicKey == "" || !w.interfaceExistsLocked() {
		return map[string]any{"healthy": false, "reason": "peer_missing", "peer_present": false}
	}
	peers := w.currentPeersLocked()
	present := false
	for _, peer := range peers {
		if peer == publicKey {
			present = true
			break
		}
	}
	if !present {
		return map[string]any{"healthy": false, "reason": "peer_missing", "peer_present": false}
	}
	handshakes := w.latestHandshakesLocked()
	last := handshakes[publicKey]
	if last <= 0 {
		return map[string]any{"healthy": false, "reason": "no_handshake", "peer_present": true, "last_handshake_at": nil, "last_handshake_at_iso": "", "handshake_age_seconds": nil}
	}
	age := int(time.Now().Unix()) - last
	return map[string]any{"healthy": true, "reason": "peer_ready", "peer_present": true, "last_handshake_at": last, "last_handshake_at_iso": time.Unix(int64(last), 0).UTC().Format(time.RFC3339), "handshake_age_seconds": age}
}

func (w *wireGuardRuntime) ensureListenerLocked() error {
	if w.interfaceExistsLocked() {
		code, out, errOut := w.runCommand([]string{w.bin("wg"), "set", w.interfaceName, "listen-port", strconv.Itoa(w.port), "private-key", w.privateKeyPath})
		if code != 0 {
			return fmt.Errorf("wireguard_listener_config_failed: %s", firstText(errOut, out, "unknown"))
		}
		return w.ensureLinuxRuntimeLocked()
	}
	if err := os.MkdirAll(w.configRoot, 0o750); err != nil {
		return err
	}
	_ = os.Chmod(w.configRoot, 0o750)
	configPath := filepath.Join(w.configRoot, defaultWireGuardConfigName+".conf")
	if err := os.WriteFile(configPath, []byte(w.renderBaseConfig()), 0o640); err != nil {
		return err
	}
	code, out, errOut := w.runCommand([]string{w.bin("wg-quick"), "up", configPath})
	if code != 0 {
		return fmt.Errorf("wireguard_listener_up_failed: %s", firstText(errOut, out, "unknown"))
	}
	return w.ensureLinuxRuntimeLocked()
}

func (w *wireGuardRuntime) ensureLinuxRuntimeLocked() error {
	code, out, errOut := w.runCommand([]string{w.bin("ip"), "address", "replace", w.enginePrefix.String(), "dev", w.interfaceName})
	if code != 0 {
		return fmt.Errorf("wireguard_address_failed: %s", firstText(errOut, out, "unknown"))
	}
	code, out, errOut = w.runCommand([]string{w.bin("ip"), "route", "replace", w.peerPrefix.String(), "dev", w.interfaceName})
	if code != 0 {
		return fmt.Errorf("wireguard_route_failed: %s", firstText(errOut, out, "unknown"))
	}
	code, out, errOut = w.runCommand([]string{w.bin("ip"), "link", "set", "up", "dev", w.interfaceName})
	if code != 0 {
		return fmt.Errorf("wireguard_link_up_failed: %s", firstText(errOut, out, "unknown"))
	}
	return w.ensureLinuxFirewallLocked()
}

type peerPolicyOwners struct {
	AllowedIPs map[string]string
	PublicKeys map[string]string
}

func (w *wireGuardRuntime) occupiedPeerPolicyLocked(exceptAgentID string) peerPolicyOwners {
	owners := peerPolicyOwners{AllowedIPs: map[string]string{}, PublicKeys: map[string]string{}}
	exceptAgentID = cleanText(exceptAgentID)
	for agentID, peer := range w.managedPeers {
		agentID = cleanText(agentID)
		if agentID == "" || agentID == exceptAgentID {
			continue
		}
		for _, allowedIP := range normalizeStringSlice(peer["allowed_ips"]) {
			if normalized := normalizeWireGuardHostRoute(allowedIP); normalized != "" {
				owners.AllowedIPs[normalized] = agentID
			}
		}
		if publicKey := cleanText(peer["public_key"]); publicKey != "" {
			owners.PublicKeys[publicKey] = agentID
		}
	}
	return owners
}

func (w *wireGuardRuntime) validatePeerPolicyLocked(agentID string, publicKey string, allowedIPs []string, owners peerPolicyOwners) ([]string, error) {
	agentID = cleanText(agentID)
	publicKey = cleanText(publicKey)
	if owners.AllowedIPs == nil {
		owners.AllowedIPs = map[string]string{}
	}
	if owners.PublicKeys == nil {
		owners.PublicKeys = map[string]string{}
	}
	if agentID == "" || publicKey == "" {
		return nil, errors.New("invalid_wireguard_peer")
	}
	if owner := owners.PublicKeys[publicKey]; owner != "" && owner != agentID {
		return nil, fmt.Errorf("wireguard_public_key_already_assigned: %s", owner)
	}
	if len(allowedIPs) != 1 {
		return nil, errors.New("wireguard_peer_allowed_ips_must_be_single_host")
	}
	prefix, err := netip.ParsePrefix(cleanText(allowedIPs[0]))
	if err != nil || !prefix.Addr().Is4() || prefix.Bits() != 32 {
		return nil, errors.New("wireguard_peer_allowed_ip_must_be_ipv4_32")
	}
	prefix = prefix.Masked()
	if !w.peerPrefix.Contains(prefix.Addr()) {
		return nil, errors.New("wireguard_peer_allowed_ip_outside_peer_network")
	}
	if !usablePeerVirtualAddr(prefix.Addr(), w.peerPrefix, w.enginePrefix) {
		if prefix.Addr() == w.enginePrefix.Addr() {
			return nil, errors.New("wireguard_peer_allowed_ip_reserved_for_engine")
		}
		return nil, errors.New("wireguard_peer_allowed_ip_reserved")
	}
	normalized := prefix.String()
	if owner := owners.AllowedIPs[normalized]; owner != "" && owner != agentID {
		return nil, fmt.Errorf("wireguard_allowed_ip_already_assigned: %s", normalized)
	}
	owners.AllowedIPs[normalized] = agentID
	owners.PublicKeys[publicKey] = agentID
	return []string{normalized}, nil
}

func normalizeWireGuardHostRoute(value string) string {
	prefix, err := netip.ParsePrefix(cleanText(value))
	if err != nil || !prefix.Addr().Is4() || prefix.Bits() != 32 {
		return ""
	}
	return prefix.Masked().String()
}

func (w *wireGuardRuntime) ensureLinuxFirewallLocked() error {
	if !w.peerPrefix.Addr().Is4() || !w.enginePrefix.Addr().Is4() {
		return errors.New("wireguard_firewall_ipv4_required")
	}
	peerCIDR := w.peerPrefix.String()
	required := [][]string{
		{w.bin("iptables"), "-F", wireGuardInputChain},
		{w.bin("iptables"), "-A", wireGuardInputChain, "-m", "conntrack", "--ctstate", "INVALID", "-m", "comment", "--comment", "borealis wg drop invalid ingress", "-j", "DROP"},
		{w.bin("iptables"), "-A", wireGuardInputChain, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-m", "comment", "--comment", "borealis wg established return", "-j", "ACCEPT"},
		{w.bin("iptables"), "-A", wireGuardInputChain, "-s", peerCIDR, "-m", "comment", "--comment", "borealis deny agent host ingress", "-j", "DROP"},
		{w.bin("iptables"), "-F", wireGuardForwardChain},
		{w.bin("iptables"), "-A", wireGuardForwardChain, "-m", "conntrack", "--ctstate", "INVALID", "-m", "comment", "--comment", "borealis wg drop invalid forward", "-j", "DROP"},
		{w.bin("iptables"), "-A", wireGuardForwardChain, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-m", "comment", "--comment", "borealis wg established forward", "-j", "ACCEPT"},
		{w.bin("iptables"), "-A", wireGuardForwardChain, "-i", w.interfaceName, "-o", w.interfaceName, "-m", "comment", "--comment", "borealis deny agent lateral wg", "-j", "DROP"},
		{w.bin("iptables"), "-A", wireGuardForwardChain, "-s", peerCIDR, "-m", "comment", "--comment", "borealis deny agent forwarding", "-j", "DROP"},
	}
	for _, chain := range []string{wireGuardInputChain, wireGuardForwardChain} {
		code, _, _ := w.runCommand([]string{w.bin("iptables"), "-N", chain})
		if code != 0 {
			// Existing chains are expected after restart/reconcile.
		}
	}
	for _, args := range required {
		if err := w.runRequiredCommand(args, "wireguard_firewall_rule_failed"); err != nil {
			return err
		}
	}
	hooks := []struct {
		chain string
		args  []string
	}{
		{chain: "INPUT", args: []string{"-i", w.interfaceName, "-j", wireGuardInputChain}},
		{chain: "FORWARD", args: []string{"-i", w.interfaceName, "-j", wireGuardForwardChain}},
	}
	for _, hook := range hooks {
		check := append([]string{w.bin("iptables"), "-C", hook.chain}, hook.args...)
		code, _, _ := w.runCommand(check)
		if code == 0 {
			continue
		}
		insert := append([]string{w.bin("iptables"), "-I", hook.chain, "1"}, hook.args...)
		if err := w.runRequiredCommand(insert, "wireguard_firewall_hook_failed"); err != nil {
			return err
		}
	}
	return nil
}

func (w *wireGuardRuntime) runRequiredCommand(args []string, reason string) error {
	code, out, errOut := w.runCommand(args)
	if code != 0 {
		return fmt.Errorf("%s: %s", reason, firstText(errOut, out, "unknown"))
	}
	return nil
}

func (w *wireGuardRuntime) renderBaseConfig() string {
	return strings.Join([]string{
		"[Interface]",
		"PrivateKey = " + w.serverPrivate,
		"ListenPort = " + strconv.Itoa(w.port),
		"Address = " + w.enginePrefix.String(),
		"MTU = " + strconv.Itoa(parseIntDefault(os.Getenv("BOREALIS_WIREGUARD_MTU"), 1420)),
		"",
	}, "\n")
}

func (w *wireGuardRuntime) interfaceExistsLocked() bool {
	code, _, _ := w.runCommand([]string{w.bin("wg"), "show", w.interfaceName})
	if code == 0 {
		return true
	}
	code, _, _ = w.runCommand([]string{w.bin("ip"), "link", "show", "dev", w.interfaceName})
	return code == 0
}

func (w *wireGuardRuntime) currentPeersLocked() []string {
	code, out, _ := w.runCommand([]string{w.bin("wg"), "show", w.interfaceName, "peers"})
	if code != 0 {
		return []string{}
	}
	peers := []string{}
	for _, line := range strings.Split(out, "\n") {
		if cleanText(line) != "" {
			peers = append(peers, cleanText(line))
		}
	}
	return peers
}

func (w *wireGuardRuntime) latestHandshakesLocked() map[string]int {
	code, out, _ := w.runCommand([]string{w.bin("wg"), "show", w.interfaceName, "latest-handshakes"})
	if code != 0 {
		return map[string]int{}
	}
	result := map[string]int{}
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		value, err := strconv.Atoi(parts[1])
		if err == nil {
			result[parts[0]] = value
		}
	}
	return result
}

func (w *wireGuardRuntime) bin(name string) string {
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	return name
}

func (w *wireGuardRuntime) runCommand(args []string) (int, string, string) {
	if w != nil && w.commandRunner != nil {
		return w.commandRunner(args)
	}
	socketPath := cleanText(os.Getenv("BOREALIS_WIREGUARD_CONTROL_SOCKET"))
	if socketPath != "" && len(args) > 0 {
		code, out, errOut, err := runWireGuardControlCommand(socketPath, args)
		if err == nil {
			return code, out, errOut
		}
		return 1, "", err.Error()
	}
	if len(args) == 0 {
		return 1, "", "missing command"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	proc := exec.CommandContext(ctx, args[0], args[1:]...)
	stdout, err := proc.Output()
	if err == nil {
		return 0, strings.TrimSpace(string(stdout)), ""
	}
	stderr := ""
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		stderr = strings.TrimSpace(string(exitErr.Stderr))
		return exitErr.ExitCode(), strings.TrimSpace(string(stdout)), stderr
	}
	return 1, strings.TrimSpace(string(stdout)), err.Error()
}

func runWireGuardControlCommand(socketPath string, args []string) (int, string, string, error) {
	return runWireGuardControlSocketPayload(context.Background(), socketPath, map[string]any{"command": "run", "args": args, "timeout": 30}, 30*time.Second)
}

func runWireGuardControlSocketCommand(ctx context.Context, socketPath string, command string, timeout time.Duration) (int, string, string, error) {
	command = cleanText(command)
	if command == "" {
		return 1, "", "", errors.New("missing wireguard control command")
	}
	return runWireGuardControlSocketPayload(ctx, socketPath, map[string]any{"command": command}, timeout)
}

func runWireGuardControlSocketPayload(ctx context.Context, socketPath string, payload map[string]any, timeout time.Duration) (int, string, string, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return 1, "", "", err
	}
	defer conn.Close()
	rawPayload, _ := json.Marshal(payload)
	if _, err := conn.Write(append(rawPayload, '\n')); err != nil {
		return 1, "", "", err
	}
	_ = conn.SetReadDeadline(deadline)
	raw, err := io.ReadAll(io.LimitReader(conn, 1024*1024))
	if err != nil {
		return 1, "", "", err
	}
	var response map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(raw), &response); err != nil {
		return 1, "", "", err
	}
	return int(coerceInt64(response["returncode"])), cleanText(response["stdout"]), cleanText(response["stderr"]), nil
}

func normalizeStringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		out := []string{}
		for _, item := range typed {
			if cleanText(item) != "" {
				out = append(out, cleanText(item))
			}
		}
		return out
	case []any:
		out := []string{}
		for _, item := range typed {
			if cleanText(item) != "" {
				out = append(out, cleanText(item))
			}
		}
		return out
	default:
		if cleanText(value) == "" {
			return []string{}
		}
		return []string{cleanText(value)}
	}
}
