package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultRDPBackendPort              = 3389
	defaultRDPEstablishDeadlineSeconds = 30
	defaultRDPStartReadyWaitSeconds    = 20
	maxRDPUsernameLength               = 256
	maxRDPDomainLength                 = 256
	maxRDPPasswordLength               = 4096
)

type rdpRuntime struct {
	auth   *authService
	vpn    *vpnTunnelService
	signer *agentJWTSigner
}

type rdpConnectionCredential struct {
	Username     string
	Password     string
	Domain       string
	CredentialID int64
	Name         string
}

func newRDPRuntime(auth *authService, vpn *vpnTunnelService) *rdpRuntime {
	signer, _ := loadOrCreateAgentJWTSigner()
	return &rdpRuntime{auth: auth, vpn: vpn, signer: signer}
}

func (r *rdpRuntime) issueSession(ctx context.Context, request *http.Request, profile operatorProfile, result remoteOpsSessionResult, body map[string]any) (map[string]any, int) {
	if r == nil || r.vpn == nil {
		return map[string]any{"error": "tunnel_unavailable"}, http.StatusServiceUnavailable
	}
	credential, status, credentialErr := r.resolveCredential(ctx, result.Device, body)
	if credentialErr != nil {
		return credentialErr, status
	}
	ctx, cancel := context.WithTimeout(ctx, defaultRDPEstablishDeadlineSeconds*time.Second)
	defer cancel()
	agentID := result.Device.AgentID
	rdpPort := parseIntDefault(os.Getenv("BOREALIS_RDP_PORT"), defaultRDPBackendPort)
	tunnelPayload := r.vpn.sessionPayload(agentID, false)
	if tunnelPayload == nil {
		var err error
		tunnelPayload, err = r.vpn.connect(ctx, vpnConnectRequest{
			AgentID:       agentID,
			OperatorID:    profile.Username,
			EndpointHost:  inferWireGuardEndpointHost(request),
			MarkActivity:  true,
			RequiredPorts: []int{rdpPort},
		})
		if err != nil {
			return map[string]any{"error": "tunnel_down", "detail": err.Error()}, http.StatusConflict
		}
	} else {
		r.vpn.requestAgentStart(ctx, agentID, false, "rdp_establish", []int{rdpPort})
	}
	virtualIP := cleanText(tunnelPayload["virtual_ip"])
	host := strings.Split(virtualIP, "/")[0]
	if host == "" {
		return map[string]any{"error": "virtual_ip_missing"}, http.StatusInternalServerError
	}
	allowedIPs := cleanText(firstNonEmpty(tunnelPayload["allowed_ips"], tunnelPayload["engine_virtual_ip"]))
	startResponse, startStatus, startErr := requestRDPStartReady(ctx, r.auth, result.Route, result.Device.Hostname, serviceModeFromAgentID(agentID), map[string]any{
		"agent_id":          agentID,
		"port":              rdpPort,
		"rdp_port":          rdpPort,
		"allowed_ips":       allowedIPs,
		"engine_virtual_ip": tunnelPayload["engine_virtual_ip"],
		"virtual_ip":        tunnelPayload["virtual_ip"],
		"reason":            "rdp_establish",
	}, defaultRDPStartReadyWaitSeconds)
	log.Printf("rdp_start_ready ready=%t status=%d error=%s", startErr == nil && boolFromAny(startResponse["ready"]), startStatus, cleanText(startErr["error"]))
	if startErr != nil {
		return startErr, startStatus
	}
	if !waitForTCP(host, rdpPort, 5, 0.25) {
		return map[string]any{
			"error":  "rdp_backend_not_ready",
			"detail": "RDP listener did not accept a WireGuard connection.",
			"host":   host,
			"port":   rdpPort,
		}, http.StatusServiceUnavailable
	}
	health := guacdHealth(ctx, 350*time.Millisecond)
	if !boolFromAny(health["enabled"]) || !boolFromAny(health["available"]) {
		return map[string]any{"error": "guacamole_unavailable", "detail": firstText(cleanText(health["reason"]), "unavailable")}, http.StatusServiceUnavailable
	}
	issued, issueErr := r.issueRemoteDesktopToken(profile, result)
	if issueErr != nil {
		return map[string]any{"error": "token_issue_failed"}, http.StatusInternalServerError
	}
	sessionID, sessionErr := randomHex(16)
	participantID, participantErr := randomHex(16)
	if sessionErr != nil || participantErr != nil {
		return map[string]any{"error": "session_issue_failed"}, http.StatusInternalServerError
	}
	workerResponse, workerStatus, workerErr := remoteFilePostWorkerJSON(ctx, r.auth, result.Route, "/remote-desktop/vnc/session", map[string]any{
		"operation_token":        issued.Token,
		"protocol":               "rdp",
		"agent_id":               agentID,
		"host":                   host,
		"port":                   rdpPort,
		"username":               credential.Username,
		"password":               credential.Password,
		"domain":                 credential.Domain,
		"security":               "nla",
		"ignore_cert":            true,
		"operator_id":            profile.Username,
		"session_id":             sessionID,
		"participant_id":         participantID,
		"role":                   "controller",
		"width":                  rdpDisplayDimension(body["width"], 1440, 320, 8192),
		"height":                 rdpDisplayDimension(body["height"], 900, 200, 8192),
		"dpi":                    rdpDisplayDimension(body["dpi"], 96, 48, 384),
		"performance_preference": normalizePerformancePreference(body["performance_preference"]),
		"image_codec":            normalizeVNCImageCodec(body["image_codec"]),
	}, 10*time.Second)
	if workerErr != nil {
		if workerStatus == 0 {
			workerStatus = http.StatusServiceUnavailable
		}
		return workerErr, workerStatus
	}
	guacToken := cleanText(workerResponse["token"])
	if guacToken == "" {
		return map[string]any{"error": "guacamole_proxy_unavailable", "detail": "worker_token_missing"}, http.StatusServiceUnavailable
	}
	_ = r.vpn.confirmTransportSuccess(agentID)
	wsPath := joinURL(result.Route.RoutePathPrefix, "/remote-desktop/vnc/guacamole")
	urls := remoteOpsWorkerURLs(request, result.Route)
	return map[string]any{
		"viewer":            "guacamole",
		"protocol":          "rdp",
		"session_id":        sessionID,
		"participant_id":    participantID,
		"participant_role":  "controller",
		"view_only":         false,
		"session_state":     "ready",
		"credential_id":     nullablePositiveInt64Value(credential.CredentialID),
		"credential_name":   credential.Name,
		"virtual_ip":        host,
		"tunnel_id":         tunnelPayload["tunnel_id"],
		"engine_virtual_ip": tunnelPayload["engine_virtual_ip"],
		"rdp_port":          rdpPort,
		"guacamole_ws_url":  websocketURLForRequest(request, wsPath),
		"guacamole_ws_path": wsPath,
		"token":             guacToken,
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

func (r *rdpRuntime) resolveCredential(ctx context.Context, device remoteOpsSessionDevice, body map[string]any) (rdpConnectionCredential, int, map[string]any) {
	credentialID, credentialIDProvided, credentialIDValid := rdpCredentialID(body)
	if credentialIDProvided {
		if !credentialIDValid {
			return rdpConnectionCredential{}, http.StatusBadRequest, map[string]any{"error": "invalid_credential_id"}
		}
		if r == nil || r.auth == nil || r.auth.aegis == nil {
			return rdpConnectionCredential{}, http.StatusLocked, map[string]any{"error": "aegis_locked", "message": errAegisLocked.Error()}
		}
		store, ok := r.auth.store.(internalSchedulerCredentialStore)
		if !ok {
			return rdpConnectionCredential{}, http.StatusServiceUnavailable, map[string]any{"error": "credential_store_unavailable"}
		}
		credential, found, err := store.loadDecryptedSchedulerCredential(ctx, r.auth.aegis, credentialID)
		if err != nil {
			switch {
			case errors.Is(err, errSchedulerCredentialResetRequired):
				return rdpConnectionCredential{}, http.StatusLocked, map[string]any{"error": "credential_reset_required", "message": errSchedulerCredentialResetRequired.Error()}
			case errors.Is(err, errAegisNotConfigured), errors.Is(err, errAegisLocked), errors.Is(err, errAegisDataCorruption):
				return rdpConnectionCredential{}, protectedSecretErrorStatus(err), protectedSecretErrorBody(err)
			default:
				return rdpConnectionCredential{}, http.StatusInternalServerError, map[string]any{"error": "credential_unavailable"}
			}
		}
		if !found {
			return rdpConnectionCredential{}, http.StatusNotFound, map[string]any{"error": "credential_not_found"}
		}
		credentialSiteID := coerceInt64(credential["site_id"])
		deviceSiteID := int64(0)
		if device.SiteID != nil {
			deviceSiteID = *device.SiteID
		}
		if credentialSiteID > 0 && credentialSiteID != deviceSiteID {
			return rdpConnectionCredential{}, http.StatusForbidden, map[string]any{"error": "credential_site_mismatch"}
		}
		credentialType := strings.ToLower(cleanText(credential["credential_type"]))
		connectionType := strings.ToLower(cleanText(credential["connection_type"]))
		if credentialType != credentialTypeMachine && credentialType != credentialTypeDomain {
			return rdpConnectionCredential{}, http.StatusBadRequest, map[string]any{"error": "credential_type_mismatch"}
		}
		if connectionType != connectionTypeWindows && connectionType != connectionTypeWinRM {
			return rdpConnectionCredential{}, http.StatusBadRequest, map[string]any{"error": "credential_connection_mismatch"}
		}
		resolved, validationErr := validateRDPCredential(cleanText(credential["username"]), rawStringValue(credential["password"]), "")
		if validationErr != nil {
			return rdpConnectionCredential{}, http.StatusBadRequest, map[string]any{"error": "credential_incomplete", "detail": validationErr.Error()}
		}
		resolved.CredentialID = credentialID
		resolved.Name = cleanText(credential["name"])
		return resolved, http.StatusOK, nil
	}
	username, password, domain, err := rdpManualCredentialValues(body)
	if err != nil {
		return rdpConnectionCredential{}, http.StatusBadRequest, map[string]any{"error": "invalid_rdp_credentials", "detail": err.Error()}
	}
	resolved, err := validateRDPCredential(username, password, domain)
	if err != nil {
		return rdpConnectionCredential{}, http.StatusBadRequest, map[string]any{"error": "invalid_rdp_credentials", "detail": err.Error()}
	}
	return resolved, http.StatusOK, nil
}

func validateRDPRequestCredentialInput(body map[string]any) error {
	_, provided, valid := rdpCredentialID(body)
	if provided {
		if !valid {
			return errors.New("credential_id must be positive integer")
		}
		return nil
	}
	username, password, domain, err := rdpManualCredentialValues(body)
	if err != nil {
		return err
	}
	_, err = validateRDPCredential(username, password, domain)
	return err
}

func rdpCredentialID(body map[string]any) (int64, bool, bool) {
	value, provided := body["credential_id"]
	if !provided {
		return 0, false, false
	}
	credentialID, valid := parseInt64Value(value)
	return credentialID, true, valid && credentialID > 0
}

func rdpManualCredentialValues(body map[string]any) (string, string, string, error) {
	username, usernameOK := body["rdp_username"].(string)
	password, passwordOK := body["rdp_password"].(string)
	domain := ""
	if value, provided := body["rdp_domain"]; provided {
		var domainOK bool
		domain, domainOK = value.(string)
		if !domainOK {
			return "", "", "", errors.New("rdp_domain must be text")
		}
	}
	if !usernameOK {
		return "", "", "", errors.New("rdp_username must be text")
	}
	if !passwordOK {
		return "", "", "", errors.New("rdp_password must be text")
	}
	return username, password, domain, nil
}

func validateRDPCredential(username string, password string, domain string) (rdpConnectionCredential, error) {
	username = strings.TrimSpace(username)
	domain = strings.TrimSpace(domain)
	if err := validateInputUTF8AndControls("rdp_username", username, maxRDPUsernameLength, false); err != nil {
		return rdpConnectionCredential{}, err
	}
	if err := validateNoUnsafeMarkup("rdp_username", username); err != nil {
		return rdpConnectionCredential{}, err
	}
	if err := validateInputUTF8AndControls("rdp_domain", domain, maxRDPDomainLength, false); err != nil {
		return rdpConnectionCredential{}, err
	}
	if err := validateNoUnsafeMarkup("rdp_domain", domain); err != nil {
		return rdpConnectionCredential{}, err
	}
	if err := validateInputUTF8AndControls("rdp_password", password, maxRDPPasswordLength, false); err != nil {
		return rdpConnectionCredential{}, err
	}
	if username == "" {
		return rdpConnectionCredential{}, errors.New("RDP username is required")
	}
	if password == "" {
		return rdpConnectionCredential{}, errors.New("RDP password is required")
	}
	if parts := strings.SplitN(username, "\\", 2); len(parts) == 2 && strings.TrimSpace(parts[0]) != "" && strings.TrimSpace(parts[1]) != "" {
		usernameDomain := strings.TrimSpace(parts[0])
		if domain != "" && !strings.EqualFold(domain, usernameDomain) {
			return rdpConnectionCredential{}, errors.New("RDP domain conflicts with username domain")
		}
		domain = usernameDomain
		username = strings.TrimSpace(parts[1])
	}
	return rdpConnectionCredential{Username: username, Password: password, Domain: domain}, nil
}

func (r *rdpRuntime) issueRemoteDesktopToken(profile operatorProfile, result remoteOpsSessionResult) (issuedRemoteOpsSession, error) {
	if r.signer == nil {
		signer, err := loadOrCreateAgentJWTSigner()
		if err != nil {
			return issuedRemoteOpsSession{}, err
		}
		r.signer = signer
	}
	if result.Route == nil {
		return issuedRemoteOpsSession{}, errors.New("site_worker_unavailable")
	}
	return r.signer.issueRemoteOpsSession(profile, result.Device, *result.Route, []string{"remote_desktop"}, time.Now().UTC(), defaultRemoteOpSessionTTL)
}

func (r *rdpRuntime) disconnect(ctx context.Context, profile operatorProfile, agentID string, sessionID string, participantID string, reason string) (map[string]any, int) {
	if cleanText(agentID) == "" || cleanText(sessionID) == "" {
		return map[string]any{"error": "session_identity_required"}, http.StatusBadRequest
	}
	result, status, payloadErr := vpnAuthorizedDevice(ctx, r.auth, profile, agentID)
	if payloadErr != nil {
		return payloadErr, status
	}
	if result.Route == nil {
		return map[string]any{"error": "site_worker_unavailable"}, http.StatusConflict
	}
	_, workerStatus, workerErr := remoteFilePostWorkerJSON(ctx, r.auth, result.Route, "/remote-desktop/vnc/disconnect", map[string]any{
		"session_id":     sessionID,
		"participant_id": participantID,
		"reason":         firstText(cleanText(reason), "operator_disconnect"),
		"close_session":  true,
	}, 5*time.Second)
	if workerErr != nil {
		if workerStatus == 0 {
			workerStatus = http.StatusServiceUnavailable
		}
		return workerErr, workerStatus
	}
	return map[string]any{"status": "closed", "reason": reason, "session_id": sessionID, "protocol": "rdp"}, http.StatusOK
}

func requestRDPStartReady(ctx context.Context, auth *authService, route *agentWorkerRoute, hostname string, serviceMode string, payload map[string]any, timeoutSeconds float64) (map[string]any, int, map[string]any) {
	if timeoutSeconds <= 0 {
		timeoutSeconds = defaultRDPStartReadyWaitSeconds
	}
	response, status, workerErr := callWorkerHostServiceEvent(ctx, auth, route, map[string]any{
		"hostname":        hostname,
		"service_mode":    serviceMode,
		"event_name":      "rdp_start",
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
		return nil, http.StatusBadGateway, map[string]any{"error": "rdp_start_response_missing"}
	}
	if statusText := cleanText(response["status"]); statusText != "" && !strings.EqualFold(statusText, "ok") {
		return nil, http.StatusServiceUnavailable, map[string]any{
			"error":  firstText(cleanText(response["error"]), "rdp_start_failed"),
			"detail": firstText(cleanText(response["detail"]), cleanText(response["message"]), statusText),
		}
	}
	if _, ok := response["ready"]; ok && !boolFromAny(response["ready"]) {
		return nil, http.StatusServiceUnavailable, map[string]any{
			"error":  "rdp_agent_not_ready",
			"detail": firstText(cleanText(response["detail"]), cleanText(response["service_state"]), cleanText(response["listener_state"]), "rdp_agent_not_ready"),
		}
	}
	return response, http.StatusOK, nil
}

func rdpDisplayDimension(value any, fallback int, minimum int, maximum int) int {
	normalized := int(coerceInt64(value))
	if normalized < minimum || normalized > maximum {
		return fallback
	}
	return normalized
}

func rawStringValue(value any) string {
	if typed, ok := value.(string); ok {
		return typed
	}
	return ""
}

func nullablePositiveInt64Value(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}
