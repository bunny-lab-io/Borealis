package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultVNCEstablishDeadlineSeconds       = 30
	defaultVNCLiveCredentialWaitSeconds      = 30
	defaultVNCStartReadyWaitSeconds          = 20
	defaultVNCStopDebounceSeconds            = 12
	defaultVNCRecoveryReadyWaitSeconds       = 25
	defaultVNCRestartReadyWaitSeconds        = 25
	defaultVNCAuthRetryCredentialWaitSeconds = 20
	defaultVNCAuthRetryReadyWaitSeconds      = 20
	defaultVNCAuthRetryCooldownSeconds       = 30
	defaultVNCAuthLockoutCooldownSeconds     = 30
)

type vncRuntime struct {
	auth    *authService
	vpn     *vpnTunnelService
	signer  *agentJWTSigner
	mu      sync.Mutex
	byID    map[string]*vncCollaborationSession
	byAgent map[string]string
	stops   map[string]*vncPendingStop
}

type vncPendingStop struct {
	token  string
	cancel context.CancelFunc
}

type vncCollaborationSession struct {
	SessionID             string
	AgentID               string
	CreatedAt             time.Time
	UpdatedAt             time.Time
	State                 string
	ControllerOperatorID  string
	ControllerParticipant string
	ControllerPassword    string
	CredentialRevision    int
	RemoveWallpaper       bool
	LastError             string
	TunnelID              string
	AllowedIPs            string
	EngineVirtualIP       string
	AuthRetryStartedAt    time.Time
	AuthRetryCompletedAt  time.Time
	AuthRetryInProgress   bool
	AuthRetrySettleProbe  bool
	LastBackendReadyAt    time.Time
	FirstFrameAt          time.Time
	DisplayTopology       []map[string]any
	DisplayVirtualBounds  map[string]any
	Participants          map[string]*vncParticipant
}

type vncParticipant struct {
	ParticipantID      string
	OperatorID         string
	Role               string
	JoinedAt           time.Time
	LastActivityAt     time.Time
	ActiveConnections  int
	LastConnectedAt    time.Time
	LastDisconnectedAt time.Time
}

type vncCredential struct {
	ControllerPassword   string
	CredentialRevision   int
	DisplayTopology      []map[string]any
	DisplayVirtualBounds map[string]any
}

func newVNCRuntime(auth *authService, vpn *vpnTunnelService) *vncRuntime {
	signer, _ := loadOrCreateAgentJWTSigner()
	return &vncRuntime{
		auth:    auth,
		vpn:     vpn,
		signer:  signer,
		byID:    map[string]*vncCollaborationSession{},
		byAgent: map[string]string{},
		stops:   map[string]*vncPendingStop{},
	}
}

func registerVNCRoutes(mux *http.ServeMux, auth *authService, vpn *vpnTunnelService, runtime *vncRuntime) {
	if runtime == nil {
		runtime = newVNCRuntime(auth, vpn)
	}
	mux.HandleFunc("GET /api/vnc/viewers", vncViewersHandler(auth))
	mux.HandleFunc("POST /api/vnc/establish", vncEstablishHandler(auth, runtime))
	mux.HandleFunc("POST /api/vnc/session", vncEstablishHandler(auth, runtime))
	mux.HandleFunc("POST /api/vnc/disconnect", vncDisconnectHandler(auth, runtime))
	mux.HandleFunc("POST /api/vnc/handoff", vncHandoffHandler(auth, runtime))
	mux.HandleFunc("GET /api/vnc/sessions", vncSessionsHandler(auth, runtime))
	mux.HandleFunc("POST /api/internal/vnc/session-event", vncInternalSessionEventHandler(auth, runtime))
}

func vncEstablishHandler(auth *authService, runtime *vncRuntime) http.HandlerFunc {
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
		viewer := strings.ToLower(firstText(cleanText(body["viewer"]), "guacamole"))
		if viewer != "guacamole" && viewer != "apache-guacamole" && viewer != "apache_guacamole" && viewer != "guac" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_viewer"})
			return
		}
		result, status, payloadErr := vpnAuthorizedDevice(r.Context(), auth, profile, requestedAgentID)
		if payloadErr != nil {
			writeJSON(w, status, payloadErr)
			return
		}
		if result.Route == nil {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "site_worker_unavailable", "message": "No active site-worker route is available for this device site."})
			return
		}
		log.Printf("vnc_establish_request agent_id=%s hostname=%s worker_guid=%s route_prefix=%s", result.Device.AgentID, result.Device.Hostname, result.Route.WorkerGUID, result.Route.RoutePathPrefix)
		payload, statusCode := runtime.issueSession(r.Context(), r, profile, result, body)
		log.Printf("vnc_establish_response agent_id=%s hostname=%s status=%d error=%s session_id=%s", result.Device.AgentID, result.Device.Hostname, statusCode, cleanText(payload["error"]), cleanText(payload["session_id"]))
		writeJSON(w, statusCode, payload)
	}
}

func (v *vncRuntime) issueSession(ctx context.Context, r *http.Request, profile operatorProfile, result remoteOpsSessionResult, body map[string]any) (map[string]any, int) {
	if v == nil || v.vpn == nil {
		return map[string]any{"error": "tunnel_unavailable"}, http.StatusServiceUnavailable
	}
	var establishDeadline time.Time
	ctx, cancel, establishDeadline := vncEstablishContext(ctx)
	defer cancel()
	agentID := result.Device.AgentID
	hostname := result.Device.Hostname
	serviceMode := serviceModeFromAgentID(agentID)
	v.cancelPendingStop(agentID, "vnc_establish")
	log.Printf("vnc_issue_start agent_id=%s hostname=%s service_mode=%s worker_guid=%s", agentID, hostname, serviceMode, result.Route.WorkerGUID)
	credentialReason := "vnc_establish"
	credentialWait := vncBoundedWaitSeconds("BOREALIS_VNC_LIVE_CREDENTIAL_WAIT_SECONDS", defaultVNCLiveCredentialWaitSeconds, establishDeadline)
	authRetryReserved := false
	authRetryReservation := v.reserveAgentAuthRetry(agentID, time.Now().UTC(), "")
	if authRetryReservation.Needed && !authRetryReservation.Reserved {
		log.Printf("vnc_auth_retry_wait agent_id=%s hostname=%s retry_after_seconds=%d reason=%s", agentID, hostname, authRetryReservation.RetryAfterSeconds, authRetryReservation.Reason)
		return vncAuthRetryInProgressPayload(authRetryReservation.RetryAfterSeconds), http.StatusTooManyRequests
	}
	if authRetryReservation.Reserved {
		authRetryReserved = true
		credentialReason = "vnc_auth_retry"
		credentialWait = vncBoundedWaitSeconds("BOREALIS_VNC_AUTH_RETRY_CREDENTIAL_WAIT_SECONDS", defaultVNCAuthRetryCredentialWaitSeconds, establishDeadline)
		log.Printf("vnc_auth_retry_preflight agent_id=%s hostname=%s reason=previous_vnc_auth_failed", agentID, hostname)
	}
	credential, credErr := requestVNCServerCredential(ctx, v.auth, result.Route, hostname, serviceMode, agentID, credentialReason, credentialWait)
	if credErr != nil || credential.ControllerPassword == "" {
		log.Printf("vnc_credential_failed agent_id=%s hostname=%s error=%v", agentID, hostname, credErr)
		if authRetryReserved {
			v.finishAgentAuthRetry(agentID, false, "vnc_agent_live_credentials_unavailable")
			payload := vncAuthRetryInProgressPayload(v.agentAuthRetryAfterSeconds(agentID, time.Now().UTC()))
			payload["error"] = "vnc_auth_retry_settling"
			payload["detail"] = "Agent VNC credential rotation started but service readiness is still settling."
			return payload, http.StatusServiceUnavailable
		}
		return map[string]any{"error": "vnc_agent_live_credentials_unavailable"}, http.StatusServiceUnavailable
	}
	log.Printf("vnc_credential_received agent_id=%s hostname=%s revision=%d displays=%d", agentID, hostname, credential.CredentialRevision, len(credential.DisplayTopology))
	removeWallpaper := true
	if _, ok := body["remove_wallpaper"]; ok {
		removeWallpaper = boolFromAny(body["remove_wallpaper"])
	}
	session, participant, created := v.ensureSession(agentID, profile.Username, credential, removeWallpaper)
	_ = created
	vncPort := parseIntDefault(os.Getenv("BOREALIS_VNC_PORT"), defaultVNCBackendPort)
	tunnelPayload := v.vpn.sessionPayload(agentID, false)
	if tunnelPayload == nil {
		var err error
		log.Printf("vnc_tunnel_connect agent_id=%s hostname=%s port=%d", agentID, hostname, vncPort)
		tunnelPayload, err = v.vpn.connect(ctx, vpnConnectRequest{
			AgentID:       agentID,
			OperatorID:    profile.Username,
			EndpointHost:  inferWireGuardEndpointHost(r),
			MarkActivity:  true,
			RequiredPorts: []int{vncPort},
		})
		if err != nil {
			log.Printf("vnc_tunnel_connect_failed agent_id=%s hostname=%s error=%v", agentID, hostname, err)
			return map[string]any{"error": "tunnel_down", "detail": err.Error()}, http.StatusConflict
		}
	} else {
		log.Printf("vnc_tunnel_existing agent_id=%s hostname=%s tunnel_id=%s port=%d", agentID, hostname, cleanText(tunnelPayload["tunnel_id"]), vncPort)
		v.vpn.requestAgentStart(ctx, agentID, false, "vnc_establish", []int{vncPort})
	}
	virtualIP := cleanText(tunnelPayload["virtual_ip"])
	host := strings.Split(virtualIP, "/")[0]
	if host == "" {
		log.Printf("vnc_tunnel_virtual_ip_missing agent_id=%s hostname=%s tunnel_id=%s", agentID, hostname, cleanText(tunnelPayload["tunnel_id"]))
		return map[string]any{"error": "virtual_ip_missing"}, http.StatusInternalServerError
	}
	allowedIPs := cleanText(firstNonEmpty(tunnelPayload["allowed_ips"], tunnelPayload["engine_virtual_ip"]))
	if allowedIPs == "" {
		allowedIPs = cleanText(tunnelPayload["engine_virtual_ip"])
	}
	log.Printf("vnc_tunnel_ready agent_id=%s hostname=%s tunnel_id=%s virtual_ip=%s allowed_ips=%s", agentID, hostname, cleanText(tunnelPayload["tunnel_id"]), host, allowedIPs)
	v.recordBackendReady(session.SessionID, cleanText(tunnelPayload["tunnel_id"]), allowedIPs, cleanText(tunnelPayload["engine_virtual_ip"]))
	startResponse, startStatus, startErr := requestVNCStartReady(ctx, v.auth, result.Route, hostname, serviceMode, map[string]any{
		"agent_id":            agentID,
		"session_id":          session.SessionID,
		"controller_password": "",
		"view_only_password":  "",
		"port":                vncPort,
		"allowed_ips":         allowedIPs,
		"remove_wallpaper":    removeWallpaper,
		"credential_revision": session.CredentialRevision,
		"reason":              "vnc_establish",
	}, vncBoundedWaitSeconds("BOREALIS_VNC_START_READY_WAIT_SECONDS", defaultVNCStartReadyWaitSeconds, establishDeadline))
	log.Printf("vnc_start_ready agent_id=%s hostname=%s ready=%t status=%d error=%s detail=%s session_id=%s", agentID, hostname, startErr == nil && boolFromAny(startResponse["ready"]), startStatus, cleanText(startErr["error"]), cleanText(startErr["detail"]), session.SessionID)
	if startErr != nil {
		return startErr, startStatus
	}
	fastReady := waitForTCP(host, vncPort, vncBoundedWaitSeconds("BOREALIS_VNC_FAST_READY_WAIT_SECONDS", 0.75, establishDeadline), vncEnvFloat("BOREALIS_VNC_FAST_READY_POLL_INTERVAL_SECONDS", 0.15))
	log.Printf("vnc_tcp_probe_fast agent_id=%s hostname=%s host=%s port=%d ready=%t", agentID, hostname, host, vncPort, fastReady)
	recoveryReady := fastReady
	if !fastReady {
		recoveryReady = waitForTCP(host, vncPort, vncBoundedWaitSeconds("BOREALIS_VNC_RECOVERY_READY_WAIT_SECONDS", defaultVNCRecoveryReadyWaitSeconds, establishDeadline), vncEnvFloat("BOREALIS_VNC_RECOVERY_READY_POLL_INTERVAL_SECONDS", 0.5))
	}
	log.Printf("vnc_tcp_probe_recovery agent_id=%s hostname=%s host=%s port=%d ready=%t", agentID, hostname, host, vncPort, recoveryReady)
	if !recoveryReady {
		log.Printf("vnc_tunnel_force_restart agent_id=%s hostname=%s host=%s port=%d reason=vnc_backend_unreachable", agentID, hostname, host, vncPort)
		v.vpn.requestAgentStart(ctx, agentID, true, "vnc_backend_unreachable", []int{vncPort})
		restartReady := waitForTCP(host, vncPort, vncBoundedWaitSeconds("BOREALIS_VNC_RESTART_READY_WAIT_SECONDS", defaultVNCRestartReadyWaitSeconds, establishDeadline), vncEnvFloat("BOREALIS_VNC_RESTART_READY_POLL_INTERVAL_SECONDS", 0.5))
		log.Printf("vnc_tcp_probe_restart agent_id=%s hostname=%s host=%s port=%d ready=%t", agentID, hostname, host, vncPort, restartReady)
		if !restartReady {
			v.recordError(session.SessionID, "vnc_backend_unreachable")
			return map[string]any{
				"error": "vnc_backend_unreachable",
				"host":  host,
				"port":  vncPort,
			}, http.StatusServiceUnavailable
		}
	}
	health := guacdHealth(ctx, 350*time.Millisecond)
	if !boolFromAny(health["enabled"]) || !boolFromAny(health["available"]) {
		log.Printf("vnc_guacd_unavailable agent_id=%s hostname=%s reason=%s", agentID, hostname, firstText(cleanText(health["reason"]), "unavailable"))
		return map[string]any{"error": "guacamole_unavailable", "detail": firstText(cleanText(health["reason"]), "unavailable")}, http.StatusServiceUnavailable
	}
	issued, issueErr := v.issueRemoteDesktopToken(profile, result)
	if issueErr != nil {
		log.Printf("vnc_token_issue_failed agent_id=%s hostname=%s error=%v", agentID, hostname, issueErr)
		return map[string]any{"error": "token_issue_failed"}, http.StatusInternalServerError
	}
	authProbeRequired := true
	workerResponse, workerStatus, workerErr := v.postWorkerGuacamoleSession(ctx, profile, result, issued, session, participant, credential, host, vncPort, body, authProbeRequired)
	log.Printf("vnc_worker_session_response agent_id=%s hostname=%s status=%d error=%s", agentID, hostname, workerStatus, cleanText(workerErr["error"]))
	if authRetryReserved {
		if workerErr == nil {
			v.finishAgentAuthRetry(agentID, true, "")
		} else {
			v.finishAgentAuthRetry(agentID, false, vncWorkerSessionAuthRetryReason(workerErr))
			if vncWorkerSessionNeedsAuthRetry(workerErr) {
				payload := vncAuthRetrySettlingPayload(v.agentAuthRetryAfterSeconds(agentID, time.Now().UTC()))
				workerErr = payload
				workerStatus = http.StatusServiceUnavailable
			}
		}
	} else if workerErr == nil && authProbeRequired {
		v.clearAgentAuthRetryAfterWorkerReady(agentID)
	}
	if !authRetryReserved && vncWorkerSessionNeedsAuthRetry(workerErr) {
		retryReason := vncWorkerSessionAuthRetryReason(workerErr)
		if vncWorkerSessionIsAuthLockout(workerErr) {
			v.markAgentAuthRetrySettling(agentID, retryReason)
			workerErr = vncAuthRetrySettlingPayload(v.agentAuthRetryAfterSeconds(agentID, time.Now().UTC()))
			workerStatus = http.StatusServiceUnavailable
		} else {
			authRetryReservation := v.reserveAgentAuthRetry(agentID, time.Now().UTC(), retryReason)
			if !authRetryReservation.Reserved {
				if authRetryReservation.RetryAfterSeconds <= 0 {
					authRetryReservation.RetryAfterSeconds = v.agentAuthRetryAfterSeconds(agentID, time.Now().UTC())
				}
				log.Printf("vnc_auth_retry_deferred agent_id=%s hostname=%s retry_after_seconds=%d reason=%s", agentID, hostname, authRetryReservation.RetryAfterSeconds, authRetryReservation.Reason)
				workerStatus = http.StatusTooManyRequests
				workerErr = vncAuthRetryInProgressPayload(authRetryReservation.RetryAfterSeconds)
			} else {
				log.Printf("vnc_auth_retry_start agent_id=%s hostname=%s session_id=%s reason=%s", agentID, hostname, session.SessionID, cleanText(workerErr["detail"]))
				retryCredential, retryErr := requestVNCServerCredential(ctx, v.auth, result.Route, hostname, serviceMode, agentID, "vnc_auth_retry", vncBoundedWaitSeconds("BOREALIS_VNC_AUTH_RETRY_CREDENTIAL_WAIT_SECONDS", defaultVNCAuthRetryCredentialWaitSeconds, establishDeadline))
				retrySucceeded := false
				if retryErr == nil && retryCredential.ControllerPassword != "" {
					credential = retryCredential
					session, participant, _ = v.ensureSession(agentID, profile.Username, credential, removeWallpaper)
					retryStartResponse, retryStartStatus, retryStartErr := requestVNCStartReady(ctx, v.auth, result.Route, hostname, serviceMode, map[string]any{
						"agent_id":            agentID,
						"session_id":          session.SessionID,
						"controller_password": "",
						"view_only_password":  "",
						"port":                vncPort,
						"allowed_ips":         allowedIPs,
						"remove_wallpaper":    removeWallpaper,
						"credential_revision": session.CredentialRevision,
						"reason":              "vnc_auth_retry",
					}, vncBoundedWaitSeconds("BOREALIS_VNC_AUTH_RETRY_START_READY_WAIT_SECONDS", defaultVNCStartReadyWaitSeconds, establishDeadline))
					log.Printf("vnc_auth_retry_start_ready agent_id=%s hostname=%s ready=%t status=%d error=%s detail=%s session_id=%s revision=%d", agentID, hostname, retryStartErr == nil && boolFromAny(retryStartResponse["ready"]), retryStartStatus, cleanText(retryStartErr["error"]), cleanText(retryStartErr["detail"]), session.SessionID, session.CredentialRevision)
					if retryStartErr == nil {
						retryReady := waitForTCP(host, vncPort, vncBoundedWaitSeconds("BOREALIS_VNC_AUTH_RETRY_READY_WAIT_SECONDS", defaultVNCAuthRetryReadyWaitSeconds, establishDeadline), vncEnvFloat("BOREALIS_VNC_AUTH_RETRY_READY_POLL_INTERVAL_SECONDS", 0.5))
						log.Printf("vnc_auth_retry_tcp_probe agent_id=%s hostname=%s host=%s port=%d ready=%t", agentID, hostname, host, vncPort, retryReady)
						if retryReady {
							workerResponse, workerStatus, workerErr = v.postWorkerGuacamoleSession(ctx, profile, result, issued, session, participant, credential, host, vncPort, body, true)
							log.Printf("vnc_auth_retry_worker_session_response agent_id=%s hostname=%s status=%d error=%s", agentID, hostname, workerStatus, cleanText(workerErr["error"]))
							retrySucceeded = workerErr == nil
						} else {
							workerStatus = http.StatusServiceUnavailable
							workerErr = map[string]any{"error": "vnc_backend_unreachable", "detail": "vnc_auth_retry_tcp_probe_failed"}
						}
					} else {
						workerStatus = retryStartStatus
						workerErr = retryStartErr
					}
				} else {
					log.Printf("vnc_auth_retry_credential_failed agent_id=%s hostname=%s error=%v", agentID, hostname, retryErr)
				}
				v.finishAgentAuthRetry(agentID, retrySucceeded, vncWorkerSessionAuthRetryReason(workerErr))
				if !retrySucceeded && vncWorkerSessionNeedsAuthRetry(workerErr) {
					workerErr = vncAuthRetrySettlingPayload(v.agentAuthRetryAfterSeconds(agentID, time.Now().UTC()))
					workerStatus = http.StatusServiceUnavailable
				}
			}
		}
	}
	if workerErr != nil {
		if !vncWorkerSessionIsAuthRecoveryPayload(workerErr) {
			v.recordError(session.SessionID, "worker_guacamole_unavailable")
		}
		if workerStatus == 0 {
			workerStatus = http.StatusServiceUnavailable
		}
		return workerErr, workerStatus
	}
	guacToken := cleanText(workerResponse["token"])
	if guacToken == "" {
		v.recordError(session.SessionID, "worker_guacamole_token_missing")
		log.Printf("vnc_worker_token_missing agent_id=%s hostname=%s session_id=%s", agentID, hostname, session.SessionID)
		return map[string]any{"error": "guacamole_proxy_unavailable", "detail": "worker_token_missing"}, http.StatusServiceUnavailable
	}
	_ = v.vpn.confirmTransportSuccess(agentID)
	wsPath := joinURL(result.Route.RoutePathPrefix, "/remote-desktop/vnc/guacamole")
	tokenHint := guacToken
	if len(tokenHint) > 8 {
		tokenHint = tokenHint[:8]
	}
	log.Printf("vnc_establish_success agent_id=%s hostname=%s session_id=%s participant_id=%s host=%s port=%d ws_path=%s token_hint=%s", agentID, hostname, session.SessionID, participant.ParticipantID, host, vncPort, wsPath, tokenHint)
	urls := remoteOpsWorkerURLs(r, result.Route)
	snapshot := v.sessionSnapshot(session, profile.Username)
	snapshot["display_topology"] = credential.DisplayTopology
	snapshot["display_virtual_bounds"] = credential.DisplayVirtualBounds
	return map[string]any{
		"viewer":                 "guacamole",
		"session_id":             session.SessionID,
		"participant_id":         participant.ParticipantID,
		"participant_role":       participant.Role,
		"view_only":              false,
		"session_state":          session.State,
		"controller_operator_id": session.ControllerOperatorID,
		"credential_revision":    session.CredentialRevision,
		"session":                snapshot,
		"display_topology":       credential.DisplayTopology,
		"display_virtual_bounds": credential.DisplayVirtualBounds,
		"virtual_ip":             host,
		"tunnel_id":              tunnelPayload["tunnel_id"],
		"engine_virtual_ip":      tunnelPayload["engine_virtual_ip"],
		"vnc_port":               vncPort,
		"performance_preference": normalizePerformancePreference(body["performance_preference"]),
		"guacamole_ws_url":       websocketURLForRequest(r, wsPath),
		"guacamole_ws_path":      wsPath,
		"token":                  guacToken,
		"remote_ops_session": map[string]any{
			"session_id":   issued.SessionID,
			"token_type":   "Bearer",
			"issued_at":    issued.IssuedAt,
			"expires_at":   issued.ExpiresAt,
			"expires_in":   issued.ExpiresIn,
			"capabilities": []string{"remote_desktop"},
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
		},
	}, http.StatusOK
}

func (v *vncRuntime) postWorkerGuacamoleSession(ctx context.Context, profile operatorProfile, result remoteOpsSessionResult, issued issuedRemoteOpsSession, session *vncCollaborationSession, participant *vncParticipant, credential vncCredential, host string, vncPort int, body map[string]any, authProbe bool) (map[string]any, int, map[string]any) {
	width, height := initialDisplaySize(credential.DisplayVirtualBounds, credential.DisplayTopology)
	return remoteFilePostWorkerJSON(ctx, v.auth, result.Route, "/remote-desktop/vnc/session", map[string]any{
		"operation_token":        issued.Token,
		"agent_id":               session.AgentID,
		"host":                   host,
		"port":                   vncPort,
		"password":               session.ControllerPassword,
		"operator_id":            profile.Username,
		"session_id":             session.SessionID,
		"participant_id":         participant.ParticipantID,
		"role":                   participant.Role,
		"width":                  width,
		"height":                 height,
		"dpi":                    96,
		"performance_preference": normalizePerformancePreference(body["performance_preference"]),
		"auth_probe":             authProbe,
	}, 10*time.Second)
}

func vncWorkerSessionNeedsAuthRetry(workerErr map[string]any) bool {
	return vncErrorNeedsAuthRetry(cleanText(workerErr["error"]))
}

func vncWorkerSessionAuthRetryReason(workerErr map[string]any) string {
	if workerErr == nil {
		return ""
	}
	if vncWorkerSessionIsAuthLockout(workerErr) {
		return firstText(cleanText(workerErr["detail"]), cleanText(workerErr["error"]), "too_many_auth_failures")
	}
	return firstText(cleanText(workerErr["error"]), cleanText(workerErr["detail"]), "vnc_auth_failed")
}

func vncWorkerSessionIsAuthRecoveryPayload(workerErr map[string]any) bool {
	if workerErr == nil {
		return false
	}
	errorCode := cleanText(workerErr["error"])
	return errorCode == "vnc_auth_retry_in_progress" ||
		errorCode == "vnc_auth_retry_settling" ||
		vncWorkerSessionNeedsAuthRetry(workerErr)
}

func vncWorkerSessionIsAuthLockout(workerErr map[string]any) bool {
	if workerErr == nil {
		return false
	}
	return vncReasonIsAuthLockout(firstText(cleanText(workerErr["detail"]), cleanText(workerErr["error"])))
}

func vncReasonIsAuthLockout(reason string) bool {
	text := strings.ToLower(cleanText(reason))
	return strings.Contains(text, "too_many_auth_failures") ||
		strings.Contains(text, "too many") ||
		strings.Contains(text, "to many")
}

func vncErrorNeedsAuthRetry(reason string) bool {
	normalized := strings.ToLower(cleanText(reason))
	if normalized == "" {
		return false
	}
	return normalized == "vnc_auth_failed" ||
		normalized == "guacd_backend_retryable_519" ||
		strings.Contains(normalized, "too_many_auth_failures") ||
		strings.Contains(normalized, "auth_failed") ||
		strings.Contains(normalized, "auth_rejected")
}

type vncAuthRetryReservation struct {
	Needed            bool
	Reserved          bool
	RetryAfterSeconds int
	Reason            string
}

func (v *vncRuntime) reserveAgentAuthRetry(agentID string, now time.Time, reason string) vncAuthRetryReservation {
	if v == nil {
		return vncAuthRetryReservation{}
	}
	agentID = cleanText(agentID)
	if agentID == "" {
		return vncAuthRetryReservation{}
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	reason = cleanText(reason)
	v.mu.Lock()
	defer v.mu.Unlock()
	sessionID := v.byAgent[agentID]
	if sessionID == "" {
		return vncAuthRetryReservation{}
	}
	session := v.byID[sessionID]
	if session == nil {
		return vncAuthRetryReservation{}
	}
	reasonNeedsRetry := vncErrorNeedsAuthRetry(reason)
	sessionNeedsRetry := vncErrorNeedsAuthRetry(session.LastError)
	cooldown := vncAuthRetryCooldownForReason(firstText(reason, session.LastError))
	if !session.AuthRetryStartedAt.IsZero() {
		elapsed := now.Sub(session.AuthRetryStartedAt)
		if elapsed < cooldown && (session.AuthRetryInProgress || session.AuthRetrySettleProbe || sessionNeedsRetry || reasonNeedsRetry) {
			if reasonNeedsRetry {
				session.LastError = reason
				session.AuthRetrySettleProbe = true
			}
			session.UpdatedAt = now
			return vncAuthRetryReservation{
				Needed:            true,
				Reserved:          false,
				RetryAfterSeconds: retryAfterSeconds(cooldown - elapsed),
				Reason:            firstText(reason, session.LastError, "vnc_auth_retry_in_progress"),
			}
		}
	}
	if session.AuthRetrySettleProbe && !reasonNeedsRetry {
		return vncAuthRetryReservation{}
	}
	if !session.AuthRetryInProgress && !sessionNeedsRetry && !reasonNeedsRetry {
		return vncAuthRetryReservation{}
	}
	if reasonNeedsRetry {
		session.LastError = reason
	}
	session.AuthRetryStartedAt = now
	session.AuthRetryCompletedAt = time.Time{}
	session.AuthRetryInProgress = true
	session.AuthRetrySettleProbe = false
	session.UpdatedAt = now
	return vncAuthRetryReservation{
		Needed:            true,
		Reserved:          true,
		RetryAfterSeconds: 0,
		Reason:            firstText(reason, session.LastError, "vnc_auth_failed"),
	}
}

func (v *vncRuntime) finishAgentAuthRetry(agentID string, success bool, reason string) {
	if v == nil {
		return
	}
	agentID = cleanText(agentID)
	if agentID == "" {
		return
	}
	now := time.Now().UTC()
	v.mu.Lock()
	defer v.mu.Unlock()
	session := v.byID[v.byAgent[agentID]]
	if session == nil {
		return
	}
	session.AuthRetryInProgress = false
	session.AuthRetryCompletedAt = now
	session.AuthRetrySettleProbe = !success
	session.UpdatedAt = now
	if success {
		session.LastError = ""
		return
	}
	session.LastError = firstText(cleanText(reason), "vnc_auth_retry_settling")
}

func (v *vncRuntime) markAgentAuthRetrySettling(agentID string, reason string) {
	if v == nil {
		return
	}
	agentID = cleanText(agentID)
	if agentID == "" {
		return
	}
	now := time.Now().UTC()
	v.mu.Lock()
	defer v.mu.Unlock()
	session := v.byID[v.byAgent[agentID]]
	if session == nil {
		return
	}
	session.AuthRetryStartedAt = now
	session.AuthRetryCompletedAt = now
	session.AuthRetryInProgress = false
	session.AuthRetrySettleProbe = true
	session.LastError = firstText(cleanText(reason), "vnc_auth_retry_settling")
	session.UpdatedAt = now
}

func (v *vncRuntime) agentAuthRetryAfterSeconds(agentID string, now time.Time) int {
	if v == nil {
		return retryAfterSeconds(vncAuthRetryCooldown())
	}
	agentID = cleanText(agentID)
	if agentID == "" {
		return retryAfterSeconds(vncAuthRetryCooldown())
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	session := v.byID[v.byAgent[agentID]]
	cooldown := vncAuthRetryCooldown()
	if session != nil {
		cooldown = vncAuthRetryCooldownForReason(session.LastError)
	}
	if session == nil || session.AuthRetryStartedAt.IsZero() {
		return retryAfterSeconds(cooldown)
	}
	remaining := cooldown - now.Sub(session.AuthRetryStartedAt)
	return retryAfterSeconds(remaining)
}

func vncAuthRetryInProgressPayload(retryAfterSeconds int) map[string]any {
	return map[string]any{
		"error":               "vnc_auth_retry_in_progress",
		"detail":              "Agent VNC credential recovery is already in progress.",
		"retry_after_seconds": retryAfterSeconds,
	}
}

func vncAuthRetrySettlingPayload(retryAfterSeconds int) map[string]any {
	payload := vncAuthRetryInProgressPayload(retryAfterSeconds)
	payload["error"] = "vnc_auth_retry_settling"
	payload["detail"] = "Agent VNC credential recovery started but UltraVNC auth is still settling."
	return payload
}

func clearAgentAuthRetryLocked(session *vncCollaborationSession) {
	if session == nil {
		return
	}
	session.AuthRetryStartedAt = time.Time{}
	session.AuthRetryCompletedAt = time.Time{}
	session.AuthRetryInProgress = false
	session.AuthRetrySettleProbe = false
}

func (v *vncRuntime) agentAuthProbeRequired(agentID string) bool {
	if v == nil {
		return false
	}
	agentID = cleanText(agentID)
	if agentID == "" {
		return false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	session := v.byID[v.byAgent[agentID]]
	if session == nil {
		return false
	}
	return session.AuthRetryInProgress || session.AuthRetrySettleProbe || vncErrorNeedsAuthRetry(session.LastError)
}

func (v *vncRuntime) clearAgentAuthRetryAfterWorkerReady(agentID string) {
	if v == nil {
		return
	}
	agentID = cleanText(agentID)
	if agentID == "" {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	session := v.byID[v.byAgent[agentID]]
	if session == nil {
		return
	}
	clearAgentAuthRetryLocked(session)
	session.LastError = ""
	session.UpdatedAt = time.Now().UTC()
}

func vncAuthRetryCooldown() time.Duration {
	seconds := vncEnvFloat("BOREALIS_VNC_AUTH_RETRY_COOLDOWN_SECONDS", defaultVNCAuthRetryCooldownSeconds)
	if seconds < 1 {
		seconds = 1
	}
	if seconds > defaultVNCEstablishDeadlineSeconds {
		seconds = defaultVNCEstablishDeadlineSeconds
	}
	return time.Duration(seconds * float64(time.Second))
}

func vncAuthLockoutCooldown() time.Duration {
	seconds := vncEnvFloat("BOREALIS_VNC_AUTH_LOCKOUT_COOLDOWN_SECONDS", defaultVNCAuthLockoutCooldownSeconds)
	if seconds < 1 {
		seconds = 1
	}
	if seconds > defaultVNCEstablishDeadlineSeconds {
		seconds = defaultVNCEstablishDeadlineSeconds
	}
	return time.Duration(seconds * float64(time.Second))
}

func vncAuthRetryCooldownForReason(reason string) time.Duration {
	cooldown := vncAuthRetryCooldown()
	if vncReasonIsAuthLockout(reason) {
		if lockoutCooldown := vncAuthLockoutCooldown(); lockoutCooldown > cooldown {
			return lockoutCooldown
		}
	}
	return cooldown
}

func retryAfterSeconds(remaining time.Duration) int {
	if remaining <= 0 {
		return 1
	}
	seconds := int(remaining / time.Second)
	if remaining%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		return 1
	}
	return seconds
}

func vncEstablishContext(parent context.Context) (context.Context, context.CancelFunc, time.Time) {
	if parent == nil {
		parent = context.Background()
	}
	deadline := time.Now().Add(vncEstablishTimeout())
	if parentDeadline, ok := parent.Deadline(); ok && parentDeadline.Before(deadline) {
		deadline = parentDeadline
	}
	ctx, cancel := context.WithDeadline(parent, deadline)
	return ctx, cancel, deadline
}

func vncEstablishTimeout() time.Duration {
	seconds := vncEnvFloat("BOREALIS_VNC_ESTABLISH_DEADLINE_SECONDS", defaultVNCEstablishDeadlineSeconds)
	if seconds < 1 {
		seconds = 1
	}
	if seconds > defaultVNCEstablishDeadlineSeconds {
		seconds = defaultVNCEstablishDeadlineSeconds
	}
	return time.Duration(seconds * float64(time.Second))
}

func vncBoundedWaitSeconds(name string, fallback float64, deadline time.Time) float64 {
	seconds := vncEnvFloat(name, fallback)
	if seconds < 0.1 {
		seconds = 0.1
	}
	if seconds > defaultVNCEstablishDeadlineSeconds {
		seconds = defaultVNCEstablishDeadlineSeconds
	}
	if !deadline.IsZero() {
		remaining := time.Until(deadline).Seconds()
		if remaining < 0.1 {
			return 0.1
		}
		if seconds > remaining {
			seconds = remaining
		}
	}
	return seconds
}

func (v *vncRuntime) issueRemoteDesktopToken(profile operatorProfile, result remoteOpsSessionResult) (issuedRemoteOpsSession, error) {
	if v.signer == nil {
		signer, err := loadOrCreateAgentJWTSigner()
		if err != nil {
			return issuedRemoteOpsSession{}, err
		}
		v.signer = signer
	}
	if result.Route == nil {
		return issuedRemoteOpsSession{}, errors.New("site_worker_unavailable")
	}
	return v.signer.issueRemoteOpsSession(profile, result.Device, *result.Route, []string{"remote_desktop"}, time.Now().UTC(), defaultRemoteOpSessionTTL)
}

func vncDisconnectHandler(auth *authService, runtime *vncRuntime) http.HandlerFunc {
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
		sessionID := cleanText(body["session_id"])
		agentID := cleanText(body["agent_id"])
		reason := firstText(cleanText(body["reason"]), "operator_disconnect")
		closeSession := boolFromAny(body["close_session"])
		session := runtime.sessionByID(sessionID)
		if session == nil && agentID != "" {
			result, status, payloadErr := vpnAuthorizedDevice(r.Context(), auth, profile, agentID)
			if payloadErr != nil {
				writeJSON(w, status, payloadErr)
				return
			}
			session = runtime.sessionByAgent(result.Device.AgentID)
		}
		if session == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "session_not_found"})
			return
		}
		if _, status, payloadErr := vpnAuthorizedDevice(r.Context(), auth, profile, session.AgentID); payloadErr != nil {
			writeJSON(w, status, payloadErr)
			return
		}
		result, err := runtime.leaveOrClose(session.SessionID, profile.Username, closeSession, strings.EqualFold(profile.Role, "admin"), reason)
		if err != nil {
			switch err.Error() {
			case "participant_required":
				writeJSON(w, http.StatusForbidden, map[string]any{"error": "participant_required"})
			default:
				writeJSON(w, http.StatusNotFound, map[string]any{"error": "session_not_found"})
			}
			return
		}
		device, route := runtime.vpn.lookupDeviceRouteByAgentID(r.Context(), session.AgentID)
		if route != nil {
			if boolFromAny(result["closed"]) {
				_, _, _ = remoteFilePostWorkerJSON(r.Context(), auth, route, "/remote-desktop/vnc/disconnect", map[string]any{
					"session_id":    session.SessionID,
					"reason":        reason,
					"close_session": true,
				}, 5*time.Second)
				runtime.scheduleVNCStop(auth, route, device.Hostname, serviceModeFromAgentID(session.AgentID), session.AgentID, reason)
			} else if participantID := cleanText(result["participant_id"]); participantID != "" {
				_, _, _ = remoteFilePostWorkerJSON(r.Context(), auth, route, "/remote-desktop/vnc/disconnect", map[string]any{
					"session_id":     session.SessionID,
					"participant_id": participantID,
					"reason":         reason,
					"close_session":  false,
				}, 5*time.Second)
			}
		}
		response := map[string]any{
			"status":            map[bool]string{true: "closed", false: "left"}[boolFromAny(result["closed"])],
			"reason":            reason,
			"session_id":        session.SessionID,
			"controller_vacant": false,
			"reconnect_pending": boolFromAny(result["reconnect_pending"]),
		}
		if refreshed := runtime.sessionByID(session.SessionID); refreshed != nil {
			response["session"] = runtime.sessionSnapshot(refreshed, profile.Username)
		}
		writeJSON(w, http.StatusOK, response)
	}
}

func vncHandoffHandler(auth *authService, runtime *vncRuntime) http.HandlerFunc {
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
		sessionID := cleanText(body["session_id"])
		if sessionID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "session_id_required"})
			return
		}
		session := runtime.sessionByID(sessionID)
		if session == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "session_not_found"})
			return
		}
		if _, status, payloadErr := vpnAuthorizedDevice(r.Context(), auth, profile, session.AgentID); payloadErr != nil {
			writeJSON(w, status, payloadErr)
			return
		}
		refreshed, err := runtime.handoff(sessionID, profile.Username, cleanText(body["target_operator_id"]))
		if err != nil {
			switch err.Error() {
			case "controller_required":
				writeJSON(w, http.StatusForbidden, map[string]any{"error": "controller_required"})
			case "target_already_controller":
				writeJSON(w, http.StatusConflict, map[string]any{"error": "target_already_controller"})
			default:
				writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			}
			return
		}
		snapshot := runtime.sessionSnapshot(refreshed, profile.Username)
		writeJSON(w, http.StatusOK, map[string]any{
			"status":                 "ok",
			"participant_role":       snapshot["current_operator_role"],
			"session":                snapshot,
			"reconnect_required":     false,
			"allowed_ips":            refreshed.AllowedIPs,
			"display_topology":       refreshed.DisplayTopology,
			"display_virtual_bounds": refreshed.DisplayVirtualBounds,
		})
	}
}

func vncSessionsHandler(auth *authService, runtime *vncRuntime) http.HandlerFunc {
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
		sessionID := cleanText(r.URL.Query().Get("session_id"))
		agentID := cleanText(r.URL.Query().Get("agent_id"))
		sessions := runtime.listSessions()
		visible := []map[string]any{}
		for _, session := range sessions {
			if sessionID != "" && session.SessionID != sessionID {
				continue
			}
			if agentID != "" && !strings.EqualFold(session.AgentID, agentID) {
				continue
			}
			if _, _, payloadErr := vpnAuthorizedDevice(r.Context(), auth, profile, session.AgentID); payloadErr != nil {
				continue
			}
			visible = append(visible, runtime.sessionSnapshot(session, profile.Username))
		}
		writeJSON(w, http.StatusOK, map[string]any{"sessions": visible, "count": len(visible)})
	}
}

func vncInternalSessionEventHandler(auth *authService, runtime *vncRuntime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		if !validInternalSchedulerRequest(auth, r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		var body map[string]any
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
			return
		}
		event := strings.ToLower(cleanText(body["event"]))
		sessionID := cleanText(body["session_id"])
		participantID := cleanText(body["participant_id"])
		if event == "" || sessionID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "event_and_session_id_required"})
			return
		}
		session := runtime.sessionByID(sessionID)
		if session == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "session_not_found"})
			return
		}
		switch event {
		case "open":
			runtime.recordProxyOpen(sessionID, participantID)
		case "close":
			runtime.recordProxyClose(sessionID, participantID, cleanText(body["reason"]))
		case "first_frame":
			runtime.recordProxyFirstFrame(sessionID, participantID, cleanText(body["reason"]))
		case "transport_confirm":
			if runtime.vpn != nil {
				runtime.vpn.confirmTransportSuccess(session.AgentID)
			}
		default:
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_event"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	}
}

func (v *vncRuntime) ensureSession(agentID string, operatorID string, credential vncCredential, removeWallpaper bool) (*vncCollaborationSession, *vncParticipant, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.cleanupStaleLocked()
	now := time.Now().UTC()
	sessionID := v.byAgent[agentID]
	session := v.byID[sessionID]
	created := false
	if session == nil {
		sessionID, _ = randomHex(16)
		participantID, _ := randomHex(16)
		participant := &vncParticipant{ParticipantID: participantID, OperatorID: operatorID, Role: "controller", JoinedAt: now, LastActivityAt: now}
		session = &vncCollaborationSession{
			SessionID:             sessionID,
			AgentID:               agentID,
			CreatedAt:             now,
			UpdatedAt:             now,
			State:                 "active",
			ControllerOperatorID:  operatorID,
			ControllerParticipant: participantID,
			ControllerPassword:    credential.ControllerPassword,
			CredentialRevision:    maxInt(1, credential.CredentialRevision),
			RemoveWallpaper:       removeWallpaper,
			DisplayTopology:       cloneMapSlice(credential.DisplayTopology),
			DisplayVirtualBounds:  copyMap(credential.DisplayVirtualBounds),
			Participants:          map[string]*vncParticipant{participantID: participant},
		}
		v.byID[sessionID] = session
		v.byAgent[agentID] = sessionID
		return session, participant, true
	}
	created = false
	session.ControllerPassword = credential.ControllerPassword
	session.CredentialRevision = maxInt(1, credential.CredentialRevision)
	session.RemoveWallpaper = removeWallpaper
	session.DisplayTopology = cloneMapSlice(credential.DisplayTopology)
	session.DisplayVirtualBounds = copyMap(credential.DisplayVirtualBounds)
	session.UpdatedAt = now
	for _, participant := range session.Participants {
		if participant.OperatorID == operatorID {
			participant.Role = "controller"
			participant.LastActivityAt = now
			session.State = "active"
			if session.ControllerOperatorID == "" {
				session.ControllerOperatorID = operatorID
				session.ControllerParticipant = participant.ParticipantID
			}
			return session, participant, created
		}
	}
	participantID, _ := randomHex(16)
	participant := &vncParticipant{ParticipantID: participantID, OperatorID: operatorID, Role: "controller", JoinedAt: now, LastActivityAt: now}
	session.Participants[participantID] = participant
	if session.ControllerOperatorID == "" {
		session.ControllerOperatorID = operatorID
		session.ControllerParticipant = participantID
	}
	return session, participant, created
}

func (v *vncRuntime) recordBackendReady(sessionID string, tunnelID string, allowedIPs string, engineVirtualIP string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	session := v.byID[sessionID]
	if session == nil {
		return
	}
	session.TunnelID = tunnelID
	session.AllowedIPs = allowedIPs
	session.EngineVirtualIP = engineVirtualIP
	session.LastBackendReadyAt = time.Now().UTC()
	session.LastError = ""
	session.UpdatedAt = time.Now().UTC()
}

func (v *vncRuntime) recordError(sessionID string, reason string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if session := v.byID[sessionID]; session != nil {
		session.LastError = cleanText(reason)
		session.UpdatedAt = time.Now().UTC()
	}
}

func (v *vncRuntime) cancelPendingStop(agentID string, reason string) {
	agentID = cleanText(agentID)
	if agentID == "" {
		return
	}
	var pending *vncPendingStop
	v.mu.Lock()
	if v.stops != nil {
		pending = v.stops[agentID]
		delete(v.stops, agentID)
	}
	v.mu.Unlock()
	if pending != nil {
		pending.cancel()
		log.Printf("vnc_stop_debounce_cancelled agent_id=%s reason=%s", agentID, cleanText(reason))
	}
}

func (v *vncRuntime) scheduleVNCStop(auth *authService, route *agentWorkerRoute, hostname string, serviceMode string, agentID string, reason string) {
	agentID = cleanText(agentID)
	if v == nil || agentID == "" {
		return
	}
	delaySeconds := vncEnvFloat("BOREALIS_VNC_STOP_DEBOUNCE_SECONDS", defaultVNCStopDebounceSeconds)
	if delaySeconds <= 0 {
		emitVNCStop(context.Background(), auth, route, hostname, serviceMode, agentID, reason)
		return
	}
	token, _ := randomHex(8)
	stopCtx, cancel := context.WithCancel(context.Background())
	pending := &vncPendingStop{token: token, cancel: cancel}
	v.mu.Lock()
	if v.stops == nil {
		v.stops = map[string]*vncPendingStop{}
	}
	if existing := v.stops[agentID]; existing != nil {
		existing.cancel()
	}
	v.stops[agentID] = pending
	v.mu.Unlock()
	delay := time.Duration(delaySeconds * float64(time.Second))
	log.Printf("vnc_stop_debounce_scheduled agent_id=%s hostname=%s delay=%s reason=%s", agentID, hostname, delay.Round(time.Millisecond), cleanText(reason))
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-stopCtx.Done():
			return
		case <-timer.C:
		}
		v.mu.Lock()
		current := v.stops[agentID]
		if current != pending {
			v.mu.Unlock()
			return
		}
		delete(v.stops, agentID)
		activeSessionID := v.byAgent[agentID]
		v.mu.Unlock()
		if activeSessionID != "" {
			log.Printf("vnc_stop_debounce_skipped agent_id=%s hostname=%s active_session_id=%s reason=%s", agentID, hostname, activeSessionID, cleanText(reason))
			return
		}
		ctx, cancelEmit := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancelEmit()
		emitted := emitVNCStop(ctx, auth, route, hostname, serviceMode, agentID, reason)
		log.Printf("vnc_stop_debounce_emitted agent_id=%s hostname=%s emitted=%t reason=%s", agentID, hostname, emitted, cleanText(reason))
	}()
}

func (v *vncRuntime) sessionByID(sessionID string) *vncCollaborationSession {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.cleanupStaleLocked()
	return v.byID[cleanText(sessionID)]
}

func (v *vncRuntime) sessionByAgent(agentID string) *vncCollaborationSession {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.cleanupStaleLocked()
	return v.byID[v.byAgent[cleanText(agentID)]]
}

func (v *vncRuntime) listSessions() []*vncCollaborationSession {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.cleanupStaleLocked()
	sessions := make([]*vncCollaborationSession, 0, len(v.byID))
	for _, session := range v.byID {
		sessions = append(sessions, session)
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return sessions
}

func (v *vncRuntime) leaveOrClose(sessionID string, operatorID string, closeSession bool, admin bool, reason string) (map[string]any, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	session := v.byID[sessionID]
	if session == nil {
		return nil, errors.New("session_not_found")
	}
	var participant *vncParticipant
	for _, candidate := range session.Participants {
		if candidate.OperatorID == operatorID {
			participant = candidate
			break
		}
	}
	if participant == nil && !admin {
		return nil, errors.New("participant_required")
	}
	if closeSession || admin {
		delete(v.byID, session.SessionID)
		delete(v.byAgent, session.AgentID)
		return map[string]any{"closed": true, "participant_id": ""}, nil
	}
	delete(session.Participants, participant.ParticipantID)
	if len(session.Participants) == 0 {
		delete(v.byID, session.SessionID)
		delete(v.byAgent, session.AgentID)
		return map[string]any{"closed": true, "participant_id": participant.ParticipantID}, nil
	}
	if session.ControllerOperatorID == operatorID {
		v.assignControllerLocked(session, "")
	}
	session.UpdatedAt = time.Now().UTC()
	_ = reason
	return map[string]any{"closed": false, "participant_id": participant.ParticipantID}, nil
}

func (v *vncRuntime) handoff(sessionID string, actor string, target string) (*vncCollaborationSession, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	session := v.byID[sessionID]
	if session == nil {
		return nil, errors.New("session_not_found")
	}
	if session.ControllerOperatorID != "" && session.ControllerOperatorID != actor {
		return nil, errors.New("controller_required")
	}
	targetParticipant := (*vncParticipant)(nil)
	if target == "" {
		target = actor
	}
	for _, participant := range session.Participants {
		if participant.OperatorID == target {
			targetParticipant = participant
			break
		}
	}
	if targetParticipant == nil {
		return nil, errors.New("target_not_found")
	}
	if targetParticipant.OperatorID == actor && session.ControllerOperatorID == actor {
		return nil, errors.New("target_already_controller")
	}
	v.assignControllerLocked(session, targetParticipant.ParticipantID)
	return session, nil
}

func (v *vncRuntime) assignControllerLocked(session *vncCollaborationSession, preferredParticipantID string) {
	if session == nil || len(session.Participants) == 0 {
		return
	}
	var selected *vncParticipant
	if preferredParticipantID != "" {
		selected = session.Participants[preferredParticipantID]
	}
	if selected == nil {
		for _, participant := range session.Participants {
			selected = participant
			break
		}
	}
	if selected == nil {
		return
	}
	session.ControllerOperatorID = selected.OperatorID
	session.ControllerParticipant = selected.ParticipantID
	session.State = "active"
	session.UpdatedAt = time.Now().UTC()
}

func (v *vncRuntime) recordProxyOpen(sessionID string, participantID string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	session := v.byID[sessionID]
	if session == nil {
		return
	}
	if participant := session.Participants[participantID]; participant != nil {
		now := time.Now().UTC()
		participant.ActiveConnections++
		participant.LastConnectedAt = now
		participant.LastActivityAt = now
		session.UpdatedAt = now
	}
}

func (v *vncRuntime) recordProxyFirstFrame(sessionID string, participantID string, opcode string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	session := v.byID[sessionID]
	if session == nil {
		return
	}
	now := time.Now().UTC()
	if session.FirstFrameAt.IsZero() {
		session.FirstFrameAt = now
	}
	session.LastError = ""
	clearAgentAuthRetryLocked(session)
	session.UpdatedAt = now
	if participant := session.Participants[participantID]; participant != nil {
		participant.LastActivityAt = now
	}
	log.Printf("vnc_first_frame_recorded agent_id=%s session_id=%s participant_id=%s opcode=%s", session.AgentID, session.SessionID, participantID, cleanText(opcode))
}

func (v *vncRuntime) recordProxyClose(sessionID string, participantID string, reason string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	session := v.byID[sessionID]
	if session == nil {
		return
	}
	if participant := session.Participants[participantID]; participant != nil {
		now := time.Now().UTC()
		if participant.ActiveConnections > 0 {
			participant.ActiveConnections--
		}
		participant.LastDisconnectedAt = now
		participant.LastActivityAt = now
		session.UpdatedAt = now
	}
	if cleanText(reason) != "" {
		session.LastError = cleanText(reason)
		if vncErrorNeedsAuthRetry(session.LastError) {
			session.AuthRetrySettleProbe = false
		}
	}
}

func (v *vncRuntime) cleanupStaleLocked() {
	now := time.Now().UTC()
	for sessionID, session := range v.byID {
		for participantID, participant := range session.Participants {
			if participant.ActiveConnections <= 0 && now.Sub(participant.LastActivityAt) >= 90*time.Second {
				delete(session.Participants, participantID)
			}
		}
		if len(session.Participants) == 0 {
			if vncSessionRetainForRecovery(session, now) {
				session.UpdatedAt = now
				continue
			}
			delete(v.byID, sessionID)
			delete(v.byAgent, session.AgentID)
		}
	}
}

func vncSessionRetainForRecovery(session *vncCollaborationSession, now time.Time) bool {
	if session == nil {
		return false
	}
	if session.AuthRetryInProgress || session.AuthRetrySettleProbe || vncErrorNeedsAuthRetry(session.LastError) {
		if session.AuthRetryStartedAt.IsZero() {
			return true
		}
		return now.Sub(session.AuthRetryStartedAt) <= vncAuthRetryCooldownForReason(session.LastError)+90*time.Second
	}
	return false
}

func (v *vncRuntime) sessionSnapshot(session *vncCollaborationSession, currentOperator string) map[string]any {
	participants := []map[string]any{}
	currentRole := ""
	currentParticipantID := ""
	for _, participant := range session.Participants {
		if participant.OperatorID == currentOperator {
			currentRole = participant.Role
			currentParticipantID = participant.ParticipantID
		}
		participants = append(participants, map[string]any{
			"participant_id":       participant.ParticipantID,
			"operator_id":          participant.OperatorID,
			"role":                 participant.Role,
			"connected":            participant.ActiveConnections > 0,
			"joined_at":            float64(participant.JoinedAt.Unix()),
			"last_activity_at":     float64(participant.LastActivityAt.Unix()),
			"last_connected_at":    unixOrNil(participant.LastConnectedAt),
			"last_disconnected_at": unixOrNil(participant.LastDisconnectedAt),
		})
	}
	sort.Slice(participants, func(i, j int) bool {
		return cleanText(participants[i]["operator_id"]) < cleanText(participants[j]["operator_id"])
	})
	return map[string]any{
		"session_id":                  session.SessionID,
		"agent_id":                    session.AgentID,
		"state":                       session.State,
		"controller_operator_id":      session.ControllerOperatorID,
		"controller_participant_id":   session.ControllerParticipant,
		"participant_count":           len(session.Participants),
		"connected_participant_count": connectedVNCParticipantCount(session),
		"credential_revision":         session.CredentialRevision,
		"created_at":                  float64(session.CreatedAt.Unix()),
		"updated_at":                  float64(session.UpdatedAt.Unix()),
		"remove_wallpaper":            session.RemoveWallpaper,
		"last_error":                  session.LastError,
		"tunnel_id":                   session.TunnelID,
		"allowed_ips":                 session.AllowedIPs,
		"engine_virtual_ip":           session.EngineVirtualIP,
		"auth_retry_in_progress":      session.AuthRetryInProgress,
		"auth_retry_settle_probe":     session.AuthRetrySettleProbe,
		"auth_retry_started_at":       unixOrNil(session.AuthRetryStartedAt),
		"auth_retry_completed_at":     unixOrNil(session.AuthRetryCompletedAt),
		"last_backend_ready_at":       float64(session.LastBackendReadyAt.Unix()),
		"first_frame_at":              unixOrNil(session.FirstFrameAt),
		"participants":                participants,
		"current_operator_role":       currentRole,
		"current_participant_id":      currentParticipantID,
		"controller_vacant":           session.State == "controller_vacant",
		"reconnect_pending":           session.State == "reconnect_pending",
		"can_handoff":                 false,
		"can_claim_control":           false,
		"display_topology":            session.DisplayTopology,
		"display_virtual_bounds":      session.DisplayVirtualBounds,
	}
}

func connectedVNCParticipantCount(session *vncCollaborationSession) int {
	count := 0
	for _, participant := range session.Participants {
		if participant.ActiveConnections > 0 {
			count++
		}
	}
	return count
}

func requestVNCServerCredential(ctx context.Context, auth *authService, route *agentWorkerRoute, hostname string, serviceMode string, agentID string, reason string, timeoutSeconds float64) (vncCredential, error) {
	requestID, _ := randomHex(16)
	response, status, workerErr := callWorkerHostServiceEvent(ctx, auth, route, map[string]any{
		"hostname":        hostname,
		"service_mode":    serviceMode,
		"event_name":      "vnc_credential_request",
		"timeout_seconds": timeoutSeconds,
		"payload": map[string]any{
			"agent_id":   agentID,
			"request_id": requestID,
			"reason":     reason,
		},
	}, time.Duration((timeoutSeconds+1)*float64(time.Second)))
	if workerErr != nil {
		return vncCredential{}, errors.New(firstText(cleanText(workerErr["error"]), strconv.Itoa(status)))
	}
	if response == nil {
		return vncCredential{}, errors.New("credential_missing")
	}
	raw := response
	if request := cleanText(raw["request_id"]); request != "" && request != requestID {
		return vncCredential{}, errors.New("credential_request_mismatch")
	}
	if status := cleanText(raw["status"]); status != "" && !strings.EqualFold(status, "ok") {
		return vncCredential{}, errors.New(firstText(cleanText(raw["detail"]), cleanText(raw["error"]), status))
	}
	if _, ok := raw["ready"]; ok && !boolFromAny(raw["ready"]) {
		detail := firstText(cleanText(raw["detail"]), cleanText(raw["service_state"]), "vnc_agent_not_ready")
		return vncCredential{}, errors.New(detail)
	}
	password := cleanText(firstNonEmpty(raw["controller_password"], raw["vnc_password"], raw["password"]))
	if len(password) > 8 {
		password = password[:8]
	}
	if password == "" {
		return vncCredential{}, errors.New("credential_password_missing")
	}
	revision := int(coerceInt64(raw["credential_revision"]))
	if revision <= 0 {
		revision = int(time.Now().UnixMilli())
	}
	return vncCredential{
		ControllerPassword:   password,
		CredentialRevision:   revision,
		DisplayTopology:      cloneAnyMapSlice(raw["display_topology"]),
		DisplayVirtualBounds: copyAnyMap(raw["display_virtual_bounds"]),
	}, nil
}

func requestVNCStartReady(ctx context.Context, auth *authService, route *agentWorkerRoute, hostname string, serviceMode string, payload map[string]any, timeoutSeconds float64) (map[string]any, int, map[string]any) {
	if timeoutSeconds <= 0 {
		timeoutSeconds = defaultVNCStartReadyWaitSeconds
	}
	response, status, workerErr := callWorkerHostServiceEvent(ctx, auth, route, map[string]any{
		"hostname":        hostname,
		"service_mode":    serviceMode,
		"event_name":      "vnc_start",
		"timeout_seconds": timeoutSeconds,
		"payload":         payload,
	}, time.Duration((timeoutSeconds+2)*float64(time.Second)))
	if workerErr != nil {
		if status == 0 {
			status = http.StatusServiceUnavailable
		}
		return nil, status, workerErr
	}
	if response == nil {
		return nil, http.StatusBadGateway, map[string]any{"error": "vnc_start_response_missing"}
	}
	if statusText := cleanText(response["status"]); statusText != "" && !strings.EqualFold(statusText, "ok") {
		errorCode := firstText(cleanText(response["error"]), "vnc_start_failed")
		detail := firstText(cleanText(response["detail"]), cleanText(response["message"]), statusText)
		return nil, http.StatusServiceUnavailable, map[string]any{"error": errorCode, "detail": detail}
	}
	if _, ok := response["ready"]; ok && !boolFromAny(response["ready"]) {
		detail := firstText(cleanText(response["detail"]), cleanText(response["service_state"]), cleanText(response["listener_state"]), "vnc_agent_not_ready")
		return nil, http.StatusServiceUnavailable, map[string]any{"error": "vnc_agent_not_ready", "detail": detail}
	}
	return response, http.StatusOK, nil
}

func emitVNCStart(ctx context.Context, auth *authService, route *agentWorkerRoute, hostname string, serviceMode string, payload map[string]any) bool {
	result, _, workerErr := emitWorkerHostServiceEvent(ctx, auth, route, map[string]any{
		"hostname":     hostname,
		"service_mode": serviceMode,
		"event_name":   "vnc_start",
		"payload":      payload,
	}, 6*time.Second)
	return workerErr == nil && boolFromAny(result["emitted"])
}

func emitVNCStop(ctx context.Context, auth *authService, route *agentWorkerRoute, hostname string, serviceMode string, agentID string, reason string) bool {
	result, _, workerErr := emitWorkerHostServiceEvent(ctx, auth, route, map[string]any{
		"hostname":     hostname,
		"service_mode": serviceMode,
		"event_name":   "vnc_stop",
		"payload": map[string]any{
			"agent_id": agentID,
			"reason":   reason,
		},
	}, 6*time.Second)
	return workerErr == nil && boolFromAny(result["emitted"])
}

func waitForTCP(host string, port int, timeoutSeconds float64, pollSeconds float64) bool {
	if host == "" || port <= 0 || timeoutSeconds <= 0 {
		return false
	}
	deadline := time.Now().Add(time.Duration(timeoutSeconds * float64(time.Second)))
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 750*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return true
		}
		sleep := time.Duration(pollSeconds * float64(time.Second))
		if sleep <= 0 {
			sleep = 150 * time.Millisecond
		}
		if remaining := time.Until(deadline); sleep > remaining {
			sleep = remaining
		}
		time.Sleep(sleep)
	}
	return false
}

func initialDisplaySize(bounds map[string]any, topology []map[string]any) (int, int) {
	width := int(coerceInt64(bounds["width"]))
	height := int(coerceInt64(bounds["height"]))
	if width > 0 && height > 0 {
		return width, height
	}
	for _, item := range topology {
		width = int(coerceInt64(item["width"]))
		height = int(coerceInt64(item["height"]))
		if width > 0 && height > 0 {
			return width, height
		}
	}
	return 1024, 768
}

func normalizePerformancePreference(value any) int {
	parsed := int(coerceInt64(value))
	if parsed < -2 {
		return -2
	}
	if parsed > 2 {
		return 2
	}
	return parsed
}

func vncEnvFloat(name string, fallback float64) float64 {
	value := cleanText(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func websocketURLForRequest(r *http.Request, path string) string {
	base := publicBaseURLForRequest(r)
	if strings.HasPrefix(base, "https://") {
		base = "wss://" + strings.TrimPrefix(base, "https://")
	} else if strings.HasPrefix(base, "http://") {
		base = "ws://" + strings.TrimPrefix(base, "http://")
	}
	return joinURL(base, path)
}

func cloneAnyMapSlice(value any) []map[string]any {
	if raw, ok := value.(string); ok {
		var parsed []any
		if json.Unmarshal([]byte(raw), &parsed) == nil {
			value = parsed
		}
	}
	items, ok := value.([]any)
	if !ok {
		if typed, ok := value.([]map[string]any); ok {
			return cloneMapSlice(typed)
		}
		return []map[string]any{}
	}
	out := []map[string]any{}
	for _, item := range items {
		if row, ok := item.(map[string]any); ok {
			out = append(out, copyMap(row))
		}
	}
	return out
}

func cloneMapSlice(value []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(value))
	for _, item := range value {
		out = append(out, copyMap(item))
	}
	return out
}

func copyAnyMap(value any) map[string]any {
	if raw, ok := value.(string); ok {
		var parsed map[string]any
		if json.Unmarshal([]byte(raw), &parsed) == nil {
			value = parsed
		}
	}
	if row, ok := value.(map[string]any); ok {
		return copyMap(row)
	}
	return map[string]any{}
}
