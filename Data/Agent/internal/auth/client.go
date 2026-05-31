package auth

import (
	"bytes"
	"context"
	cryptoRand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
)

const AgentContextHeader = "X-Borealis-Agent-Context"

type Client struct {
	mu          sync.Mutex
	configPath  string
	cfg         *agentconfig.AgentConfig
	identity    Identity
	httpClient  *http.Client
	serviceMode string
	hostname    string
}

type Option func(*Client)

func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) {
		if httpClient != nil {
			c.httpClient = httpClient
		}
	}
}

func WithHostname(hostname string) Option {
	return func(c *Client) {
		if strings.TrimSpace(hostname) != "" {
			c.hostname = strings.TrimSpace(hostname)
		}
	}
}

func NewClient(configPath string, cfg *agentconfig.AgentConfig, serviceMode string, opts ...Option) (*Client, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil config")
	}
	identity, changed, err := LoadOrCreateIdentity(cfg)
	if err != nil {
		return nil, err
	}
	if changed {
		if err := agentconfig.Save(configPath, cfg); err != nil {
			return nil, err
		}
	}
	hostname, _ := os.Hostname()
	client := &Client{
		configPath:  configPath,
		cfg:         cfg,
		identity:    identity,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		serviceMode: NormalizeServiceMode(serviceMode),
		hostname:    hostname,
	}
	for _, opt := range opts {
		opt(client)
	}
	return client, nil
}

func NormalizeServiceMode(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "system", "svc", "service", "system_service":
		return "system"
	case "helper", "currentuser", "current_user", "interactive", "user":
		return "currentuser"
	default:
		if normalized == "" {
			return "system"
		}
		return normalized
	}
}

func ContextLabel(serviceMode string) string {
	if NormalizeServiceMode(serviceMode) == "system" {
		return "SYSTEM"
	}
	return "CURRENTUSER"
}

func ComposeAgentID(hostname string, guid string, serviceMode string) string {
	host := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(hostname), " ", "-"))
	if host == "" {
		host = "UNKNOWN-HOST"
	}
	normalizedGUID := strings.ToUpper(strings.Trim(strings.TrimSpace(guid), "{}"))
	if normalizedGUID == "" {
		normalizedGUID = "UNKNOWN-GUID"
	}
	return fmt.Sprintf("%s_%s_%s", host, normalizedGUID, ContextLabel(serviceMode))
}

func (c *Client) Config() agentconfig.AgentConfig {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg.Clone()
}

func (c *Client) Identity() Identity {
	return c.identity
}

func (c *Client) BaseURL() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.TrimRight(c.cfg.ServerURL, "/")
}

func (c *Client) RemoteOpsBaseURL() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if remoteOpsRouteUsable(c.cfg.RemoteOps) {
		return strings.TrimRight(strings.TrimSpace(c.cfg.RemoteOps.BaseURL), "/")
	}
	return ""
}

func (c *Client) RemoteOpsRouteNeedsRefresh(maxAge time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !remoteOpsRouteUsable(c.cfg.RemoteOps) {
		return true
	}
	if c.cfg.RemoteOps.Available && strings.TrimSpace(c.cfg.RemoteOps.BaseURL) != "" {
		return false
	}
	if c.cfg.RemoteOps.UpdatedAt <= 0 {
		return true
	}
	if maxAge <= 0 {
		return true
	}
	return time.Since(time.Unix(c.cfg.RemoteOps.UpdatedAt, 0)) >= maxAge
}

func (c *Client) RefreshRemoteOpsRoute(ctx context.Context) error {
	return c.refreshLocked(ctx)
}

func (c *Client) AgentID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if strings.TrimSpace(c.cfg.Agent.AgentID) == "" && strings.TrimSpace(c.cfg.Agent.GUID) != "" {
		c.cfg.Agent.AgentID = ComposeAgentID(c.hostname, c.cfg.Agent.GUID, c.serviceMode)
		_ = agentconfig.Save(c.configPath, c.cfg)
	}
	return strings.TrimSpace(c.cfg.Agent.AgentID)
}

func (c *Client) GUID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.TrimSpace(c.cfg.Agent.GUID)
}

func (c *Client) StoreServerSigningKey(value string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	c.cfg.Trust.ServerSigningKeySPKIB64 = value
	return agentconfig.Save(c.configPath, c.cfg)
}

func (c *Client) StoreAgentReleaseTarget(releaseChannel string, branch string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	channel := agentconfig.NormalizeReleaseChannel(releaseChannel)
	targetBranch := agentconfig.NormalizeBranch(branch)
	if channel == agentconfig.ReleaseChannelStable {
		targetBranch = agentconfig.DefaultBranch
	}
	c.cfg.Agent.ReleaseChannel = channel
	c.cfg.Agent.Branch = targetBranch
	return agentconfig.Save(c.configPath, c.cfg)
}

func (c *Client) StoreAgentUpdateOperation(operationID string, kind string, releaseChannel string, branch string) (agentconfig.AgentUpdateSection, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now().Unix()
	channel := agentconfig.NormalizeReleaseChannel(releaseChannel)
	targetBranch := agentconfig.NormalizeBranch(branch)
	if channel == agentconfig.ReleaseChannelStable {
		targetBranch = agentconfig.DefaultBranch
	}
	previousChannel := agentconfig.NormalizeReleaseChannel(c.cfg.Agent.ReleaseChannel)
	previousBranch := agentconfig.NormalizeBranch(c.cfg.Agent.Branch)
	if previousChannel == agentconfig.ReleaseChannelStable {
		previousBranch = agentconfig.DefaultBranch
	}
	if strings.TrimSpace(operationID) == "" {
		operationID = fmt.Sprintf("%d", now)
	}
	operation := agentconfig.AgentUpdateSection{
		OperationID:     strings.TrimSpace(operationID),
		Kind:            strings.TrimSpace(kind),
		Status:          "config_written",
		StartedAt:       now,
		UpdatedAt:       now,
		DeadlineAt:      now + int64(15*time.Minute/time.Second),
		PreviousChannel: previousChannel,
		PreviousBranch:  previousBranch,
		TargetChannel:   channel,
		TargetBranch:    targetBranch,
	}
	c.cfg.Agent.ReleaseChannel = channel
	c.cfg.Agent.Branch = targetBranch
	c.cfg.Agent.Update = operation
	if err := agentconfig.Save(c.configPath, c.cfg); err != nil {
		return agentconfig.AgentUpdateSection{}, err
	}
	return operation, nil
}

func (c *Client) LoadServerSigningKey() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.TrimSpace(c.cfg.Trust.ServerSigningKeySPKIB64)
}

func (c *Client) AuthHeaders() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	headers := map[string]string{
		"User-Agent":       "Borealis-Agent-Go/1",
		AgentContextHeader: ContextLabel(c.serviceMode),
	}
	if token := strings.TrimSpace(c.cfg.Tokens.AccessToken); token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	return headers
}

func (c *Client) EnsureAuthenticated(ctx context.Context) error {
	c.mu.Lock()
	if strings.TrimSpace(c.cfg.ServerURL) == "" {
		c.mu.Unlock()
		return fmt.Errorf("server_url missing")
	}
	needsEnrollment := strings.TrimSpace(c.cfg.Agent.GUID) == "" || strings.TrimSpace(c.cfg.Tokens.RefreshToken) == ""
	needsRefresh := strings.TrimSpace(c.cfg.Tokens.AccessToken) == "" || time.Until(time.Unix(c.cfg.Tokens.AccessExpiresAt, 0)) < time.Minute
	c.mu.Unlock()
	if needsEnrollment {
		return c.performEnrollmentLocked(ctx)
	}
	if needsRefresh {
		return c.refreshLocked(ctx)
	}
	return nil
}

func (c *Client) PostJSON(ctx context.Context, path string, requestPayload any, responsePayload any) (*http.Response, error) {
	if err := c.EnsureAuthenticated(ctx); err != nil {
		return nil, err
	}
	return c.doJSON(ctx, http.MethodPost, path, requestPayload, responsePayload, true)
}

func (c *Client) GetJSON(ctx context.Context, path string, responsePayload any) (*http.Response, error) {
	if err := c.EnsureAuthenticated(ctx); err != nil {
		return nil, err
	}
	return c.doJSON(ctx, http.MethodGet, path, nil, responsePayload, true)
}

func (c *Client) doJSON(ctx context.Context, method string, path string, requestPayload any, responsePayload any, authenticated bool) (*http.Response, error) {
	var requestBody io.Reader
	if requestPayload != nil {
		payloadBytes, err := json.Marshal(requestPayload)
		if err != nil {
			return nil, err
		}
		requestBody = bytes.NewReader(payloadBytes)
	}
	baseURL := c.BaseURL()
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, requestBody)
	if err != nil {
		return nil, err
	}
	if requestPayload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Borealis-Agent-Go/1")
	req.Header.Set(AgentContextHeader, ContextLabel(c.serviceMode))
	if authenticated {
		if token := c.currentAccessToken(); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if readErr != nil {
		return resp, readErr
	}
	if resp.StatusCode >= 400 {
		return resp, fmt.Errorf("%s %s failed: HTTP %d: %s", method, path, resp.StatusCode, string(body))
	}
	if responsePayload != nil && len(body) > 0 {
		if err := json.Unmarshal(body, responsePayload); err != nil {
			return resp, err
		}
	}
	return resp, nil
}

func (c *Client) currentAccessToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.TrimSpace(c.cfg.Tokens.AccessToken)
}

func (c *Client) performEnrollmentLocked(ctx context.Context) error {
	c.mu.Lock()
	code := strings.TrimSpace(c.cfg.EnrollmentCode)
	if code == "" {
		c.mu.Unlock()
		return fmt.Errorf("enrollment_code missing")
	}
	hostname := c.hostname
	publicKey := c.identity.PublicB64()
	c.mu.Unlock()
	clientNonce := make([]byte, 32)
	if _, err := cryptoRand.Read(clientNonce); err != nil {
		return err
	}
	requestPayload := map[string]any{
		"hostname":        hostname,
		"enrollment_code": code,
		"agent_pubkey":    publicKey,
		"client_nonce":    base64.StdEncoding.EncodeToString(clientNonce),
	}
	var requestResponse struct {
		Status            string `json:"status"`
		ApprovalReference string `json:"approval_reference"`
		ServerNonce       string `json:"server_nonce"`
		PollAfterMS       int    `json:"poll_after_ms"`
		SigningKey        string `json:"signing_key"`
	}
	if _, err := c.doJSON(ctx, http.MethodPost, "/api/agent/enroll/request", requestPayload, &requestResponse, false); err != nil {
		return err
	}
	if strings.TrimSpace(requestResponse.SigningKey) != "" {
		c.mu.Lock()
		c.cfg.Trust.ServerSigningKeySPKIB64 = strings.TrimSpace(requestResponse.SigningKey)
		_ = agentconfig.Save(c.configPath, c.cfg)
		c.mu.Unlock()
	}
	if requestResponse.Status != "pending" {
		return fmt.Errorf("unexpected enrollment status %q", requestResponse.Status)
	}
	serverNonce, err := base64.StdEncoding.DecodeString(strings.TrimSpace(requestResponse.ServerNonce))
	if err != nil {
		return fmt.Errorf("decode server nonce: %w", err)
	}
	if requestResponse.ApprovalReference == "" {
		return fmt.Errorf("approval_reference missing")
	}
	pollDelay := time.Duration(requestResponse.PollAfterMS) * time.Millisecond
	if pollDelay <= 0 {
		pollDelay = time.Second
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollDelay):
		}
		message := append([]byte{}, serverNonce...)
		message = append(message, []byte(requestResponse.ApprovalReference)...)
		message = append(message, clientNonce...)
		pollPayload := map[string]any{
			"approval_reference": requestResponse.ApprovalReference,
			"client_nonce":       base64.StdEncoding.EncodeToString(clientNonce),
			"proof_sig":          base64.StdEncoding.EncodeToString(c.identity.Sign(message)),
		}
		var pollResponse enrollFinalResponse
		if _, err := c.doJSON(ctx, http.MethodPost, "/api/agent/enroll/poll", pollPayload, &pollResponse, false); err != nil {
			return err
		}
		switch pollResponse.Status {
		case "pending":
			pollDelay = time.Duration(pollResponse.PollAfterMS) * time.Millisecond
			if pollDelay <= 0 {
				pollDelay = 5 * time.Second
			}
		case "approved", "completed":
			return c.applyTokensLocked(pollResponse)
		case "denied", "expired", "unknown":
			return fmt.Errorf("enrollment %s", pollResponse.Status)
		default:
			return fmt.Errorf("unexpected enrollment poll status %q", pollResponse.Status)
		}
	}
}

type enrollFinalResponse struct {
	Status         string                  `json:"status"`
	GUID           string                  `json:"guid"`
	AccessToken    string                  `json:"access_token"`
	RefreshToken   string                  `json:"refresh_token"`
	ExpiresIn      int64                   `json:"expires_in"`
	PollAfterMS    int                     `json:"poll_after_ms"`
	SigningKey     string                  `json:"signing_key"`
	RemoteOpsRoute *remoteOpsRouteResponse `json:"remote_ops_route"`
}

type remoteOpsRouteResponse struct {
	Available       bool   `json:"available"`
	SiteID          int    `json:"site_id"`
	WorkerGUID      string `json:"worker_guid"`
	RouteGeneration int64  `json:"route_generation"`
	RoutePathPrefix string `json:"route_path_prefix"`
	BaseURL         string `json:"base_url"`
	SocketURL       string `json:"socket_url"`
	Reason          string `json:"reason"`
}

func (c *Client) applyTokensLocked(response enrollFinalResponse) error {
	if strings.TrimSpace(response.GUID) == "" || strings.TrimSpace(response.AccessToken) == "" || strings.TrimSpace(response.RefreshToken) == "" {
		return fmt.Errorf("token response missing guid/access/refresh")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	expiresIn := response.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 900
	}
	c.cfg.Agent.GUID = strings.ToUpper(strings.Trim(strings.TrimSpace(response.GUID), "{}"))
	c.cfg.Agent.AgentID = ComposeAgentID(c.hostname, c.cfg.Agent.GUID, c.serviceMode)
	c.cfg.Tokens.AccessToken = strings.TrimSpace(response.AccessToken)
	c.cfg.Tokens.RefreshToken = strings.TrimSpace(response.RefreshToken)
	c.cfg.Tokens.AccessExpiresAt = time.Now().Unix() + expiresIn - 5
	if strings.TrimSpace(response.SigningKey) != "" {
		c.cfg.Trust.ServerSigningKeySPKIB64 = strings.TrimSpace(response.SigningKey)
	}
	applyRemoteOpsRoute(c.cfg, response.RemoteOpsRoute)
	c.cfg.EnrollmentCode = ""
	return agentconfig.Save(c.configPath, c.cfg)
}

func (c *Client) refreshLocked(ctx context.Context) error {
	c.mu.Lock()
	guid := c.cfg.Agent.GUID
	refreshToken := c.cfg.Tokens.RefreshToken
	enrollmentCode := c.cfg.EnrollmentCode
	c.mu.Unlock()
	payload := map[string]any{
		"guid":          guid,
		"refresh_token": refreshToken,
	}
	var response struct {
		AccessToken    string                  `json:"access_token"`
		ExpiresIn      int64                   `json:"expires_in"`
		RemoteOpsRoute *remoteOpsRouteResponse `json:"remote_ops_route"`
	}
	if _, err := c.doJSON(ctx, http.MethodPost, "/api/agent/token/refresh", payload, &response, true); err != nil {
		if permanentRefreshFailure(err) {
			c.mu.Lock()
			c.cfg.Tokens.AccessToken = ""
			c.cfg.Tokens.RefreshToken = ""
			c.cfg.Tokens.AccessExpiresAt = 0
			_ = agentconfig.Save(c.configPath, c.cfg)
			c.mu.Unlock()
		}
		if strings.TrimSpace(enrollmentCode) != "" {
			return c.performEnrollmentLocked(ctx)
		}
		return err
	}
	if strings.TrimSpace(response.AccessToken) == "" {
		return fmt.Errorf("refresh response missing access_token")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	expiresIn := response.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 900
	}
	c.cfg.Tokens.AccessToken = strings.TrimSpace(response.AccessToken)
	c.cfg.Tokens.AccessExpiresAt = time.Now().Unix() + expiresIn - 5
	applyRemoteOpsRoute(c.cfg, response.RemoteOpsRoute)
	return agentconfig.Save(c.configPath, c.cfg)
}

func applyRemoteOpsRoute(cfg *agentconfig.AgentConfig, route *remoteOpsRouteResponse) {
	if cfg == nil || route == nil {
		return
	}
	now := time.Now().Unix()
	if route.Available && strings.TrimSpace(route.BaseURL) != "" {
		cfg.RemoteOps = agentconfig.RemoteOpsSection{
			Available:       true,
			SiteID:          route.SiteID,
			WorkerGUID:      strings.TrimSpace(route.WorkerGUID),
			RouteGeneration: route.RouteGeneration,
			RoutePathPrefix: strings.TrimSpace(route.RoutePathPrefix),
			BaseURL:         agentconfig.NormalizeRemoteOpsURL(route.BaseURL),
			SocketURL:       agentconfig.NormalizeRemoteOpsURL(route.SocketURL),
			UpdatedAt:       now,
		}
		return
	}
	cfg.RemoteOps = agentconfig.RemoteOpsSection{
		Available: false,
		SiteID:    route.SiteID,
		Reason:    strings.TrimSpace(route.Reason),
		UpdatedAt: now,
	}
}

func remoteOpsRouteUsable(route agentconfig.RemoteOpsSection) bool {
	if !route.Available {
		return false
	}
	baseURL := strings.TrimSpace(route.BaseURL)
	if baseURL == "" {
		return false
	}
	workerGUID := strings.TrimSpace(route.WorkerGUID)
	pathPrefix := strings.TrimSpace(route.RoutePathPrefix)
	if workerGUID == "" || pathPrefix == "" {
		return false
	}
	workerPrefix := "/_borealis/site-workers/"
	if !strings.Contains(baseURL, workerPrefix) || !strings.Contains(pathPrefix, workerPrefix) {
		return false
	}
	if !strings.Contains(baseURL, workerGUID) || !strings.Contains(pathPrefix, workerGUID) {
		return false
	}
	return true
}

func permanentRefreshFailure(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "http 401") || strings.Contains(text, "http 403") {
		return true
	}
	for _, marker := range []string{
		"invalid_refresh",
		"refresh_token_expired",
		"device_purged",
		"fingerprint_mismatch",
		"token_version",
		"revoked",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}
