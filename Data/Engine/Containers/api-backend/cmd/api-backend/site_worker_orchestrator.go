package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const defaultSiteWorkerOrchestratorSocket = "/opt/Borealis/Engine/Services/site-worker-orchestrator/run/orchestrator.sock"

type siteWorkerOrchestrator struct {
	secret      []byte
	projectRoot string
	socketPath  string
	dockerBin   string
	httpClient  *http.Client
}

type siteWorkerOrchestratorClient struct {
	socketPath string
	token      string
	baseURL    string
	httpClient *http.Client
}

type orchestratorLaunchRequest struct {
	WorkerGUID        string `json:"worker_guid"`
	SiteID            int64  `json:"site_id"`
	ContainerName     string `json:"container_name"`
	Image             string `json:"image"`
	RemoteOpsPort     int64  `json:"remote_ops_port"`
	RemoteDesktopPort int64  `json:"remote_desktop_port"`
}

type orchestratorContainerRequest struct {
	WorkerGUID    string `json:"worker_guid"`
	ContainerName string `json:"container_name"`
}

type orchestratorServiceActionRequest struct {
	ServiceKey string `json:"service_key"`
	Action     string `json:"action"`
	Mode       string `json:"mode"`
}

func siteWorkerOrchestratorMode() bool {
	if explicitHealthcheckArgMode() {
		return false
	}
	role := processRoleValue()
	if role != "" {
		return textInSet(role, "site-worker-orchestrator", "worker-orchestrator")
	}
	return processArgMatches("site-worker-orchestrator", "worker-orchestrator")
}

func siteWorkerOrchestratorHealthcheckMode() bool {
	if processArgMatches("site-worker-orchestrator-healthcheck", "worker-orchestrator-healthcheck") {
		return true
	}
	return processRoleMatches("site-worker-orchestrator-healthcheck", "worker-orchestrator-healthcheck")
}

func runSiteWorkerOrchestrator(ctx context.Context, cfg gatewayConfig) error {
	secret, err := loadOrCreateEngineSecret(cfg.EngineSecretPath)
	if err != nil {
		return err
	}
	orchestrator := &siteWorkerOrchestrator{
		secret:      []byte(secret),
		projectRoot: envDefault("BOREALIS_PROJECT_ROOT", "/opt/Borealis"),
		socketPath:  siteWorkerOrchestratorSocketPath(),
		dockerBin:   schedulerDockerBin(),
		httpClient:  &http.Client{Timeout: 30 * time.Second},
	}
	return orchestrator.serve(ctx)
}

func runSiteWorkerOrchestratorHealthcheck(ctx context.Context, cfg gatewayConfig) error {
	secret, err := loadOrCreateEngineSecret(cfg.EngineSecretPath)
	if err != nil {
		return err
	}
	client := newSiteWorkerOrchestratorClient([]byte(secret))
	return client.health(ctx)
}

func siteWorkerOrchestratorSocketPath() string {
	return envDefault("BOREALIS_SITE_WORKER_ORCHESTRATOR_SOCKET", defaultSiteWorkerOrchestratorSocket)
}

func (o *siteWorkerOrchestrator) serve(ctx context.Context) error {
	if o.dockerBin == "" {
		return errors.New("docker CLI unavailable")
	}
	if strings.TrimSpace(o.socketPath) == "" {
		return errors.New("site-worker orchestrator socket path unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(o.socketPath), 0o700); err != nil {
		return err
	}
	if err := os.Remove(o.socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	listener, err := net.Listen("unix", o.socketPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = listener.Close()
		_ = os.Remove(o.socketPath)
	}()
	if err := os.Chmod(o.socketPath, 0o600); err != nil {
		return err
	}
	server := &http.Server{
		Handler:           o.handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	exited := make(chan error, 1)
	go func() {
		log.Printf("site-worker orchestrator listening on unix://%s", o.socketPath)
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		exited <- err
	}()
	select {
	case <-ctx.Done():
	case err := <-exited:
		return err
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
		return err
	}
	return <-exited
}

func (o *siteWorkerOrchestrator) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", o.authenticated(o.handleHealth))
	mux.HandleFunc("/site-workers/launch", o.authenticated(o.handleLaunchSiteWorker))
	mux.HandleFunc("/site-workers/list", o.authenticated(o.handleListSiteWorkers))
	mux.HandleFunc("/site-workers/stop", o.authenticated(o.handleStopSiteWorker))
	mux.HandleFunc("/site-workers/remove", o.authenticated(o.handleRemoveSiteWorker))
	mux.HandleFunc("/services/action", o.authenticated(o.handleServiceAction))
	mux.HandleFunc("/services/snapshots", o.authenticated(o.handleServiceSnapshots))
	return mux
}

func (o *siteWorkerOrchestrator) authenticated(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if len(o.secret) == 0 {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "orchestrator_secret_unavailable"})
			return
		}
		expected := goInternalToken(o.secret)
		presented := strings.TrimSpace(r.Header.Get(internalTokenHeader))
		if expected == "" || presented == "" || !hmac.Equal([]byte(expected), []byte(presented)) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

func (o *siteWorkerOrchestrator) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (o *siteWorkerOrchestrator) handleLaunchSiteWorker(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var req orchestratorLaunchRequest
	if err := decodeOrchestratorJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json", "message": err.Error()})
		return
	}
	payload, status, err := o.launchSiteWorker(r.Context(), req)
	if err != nil {
		writeJSON(w, status, map[string]any{"error": "launch_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, payload)
}

func (o *siteWorkerOrchestrator) handleListSiteWorkers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	snapshots, err := schedulerDockerSiteWorkerSnapshots(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "docker_unavailable", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workers": snapshots})
}

func (o *siteWorkerOrchestrator) handleStopSiteWorker(w http.ResponseWriter, r *http.Request) {
	o.handleSiteWorkerContainerAction(w, r, "stop")
}

func (o *siteWorkerOrchestrator) handleRemoveSiteWorker(w http.ResponseWriter, r *http.Request) {
	o.handleSiteWorkerContainerAction(w, r, "remove")
}

func (o *siteWorkerOrchestrator) handleSiteWorkerContainerAction(w http.ResponseWriter, r *http.Request, action string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var req orchestratorContainerRequest
	if err := decodeOrchestratorJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json", "message": err.Error()})
		return
	}
	payload, status, err := o.siteWorkerContainerAction(r.Context(), req, action)
	if err != nil {
		writeJSON(w, status, map[string]any{"error": action + "_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (o *siteWorkerOrchestrator) handleServiceAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var req orchestratorServiceActionRequest
	if err := decodeOrchestratorJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json", "message": err.Error()})
		return
	}
	if err := o.runServiceAction(r.Context(), req); err != nil {
		status := http.StatusBadRequest
		if !strings.Contains(err.Error(), "unsupported") && !strings.Contains(err.Error(), "required") {
			status = http.StatusBadGateway
		}
		writeJSON(w, status, map[string]any{"error": "service_action_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": true})
}

func (o *siteWorkerOrchestrator) handleServiceSnapshots(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	snapshots, err := o.serviceSnapshots(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "service_snapshots_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": snapshots})
}

func decodeOrchestratorJSON(r *http.Request, out any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("request body must contain one JSON object")
		}
		return err
	}
	return nil
}

func (o *siteWorkerOrchestrator) launchSiteWorker(ctx context.Context, req orchestratorLaunchRequest) (map[string]any, int, error) {
	workerGUID := cleanText(req.WorkerGUID)
	siteID := req.SiteID
	if workerGUID == "" || siteID <= 0 {
		return nil, http.StatusBadRequest, errors.New("worker_guid and site_id are required")
	}
	containerName := cleanText(req.ContainerName)
	if containerName == "" {
		containerName = "site-worker-" + workerGUID
	}
	if !strings.HasPrefix(containerName, "site-worker-") {
		return nil, http.StatusBadRequest, errors.New("site-worker container name must use site-worker prefix")
	}
	image := firstText(cleanText(req.Image), schedulerDesiredSiteWorkerImage())
	if !orchestratorSiteWorkerImageAllowed(image) {
		return nil, http.StatusForbidden, fmt.Errorf("site-worker image %s is not allowed", image)
	}
	remoteOpsPort := req.RemoteOpsPort
	if remoteOpsPort <= 0 {
		remoteOpsPort = schedulerWorkerPort(workerGUID, siteID, "BOREALIS_SITE_WORKER_REMOTE_OPS_PORT_BASE", "BOREALIS_SITE_WORKER_REMOTE_OPS_PORT_RANGE", schedulerDefaultRemoteOpsPortBase, schedulerDefaultRemoteOpsPortRange)
	}
	remoteDesktopPort := req.RemoteDesktopPort
	if remoteDesktopPort <= 0 {
		remoteDesktopPort = schedulerWorkerPort(workerGUID, siteID, "BOREALIS_SITE_WORKER_REMOTE_DESKTOP_PORT_BASE", "BOREALIS_SITE_WORKER_REMOTE_DESKTOP_PORT_RANGE", schedulerDefaultRemoteDeskPortBase, schedulerDefaultRemoteDeskPortRange)
	}
	apiRoot := filepath.Join(o.projectRoot, "Engine", "Services", "api-backend")
	for _, path := range []string{
		filepath.Join(apiRoot, "cache"),
		filepath.Join(apiRoot, "logs", "site-workers"),
		filepath.Join(o.projectRoot, "Engine", "Services", "traefik-edge", "config"),
	} {
		_ = os.MkdirAll(path, 0o755)
	}
	args := []string{
		"run", "--rm", "-d", "--name", containerName, "--network", "host",
		"--label", "borealis.role=site-worker",
		"--label", fmt.Sprintf("borealis.site_id=%d", siteID),
		"--label", "borealis.worker_guid=" + workerGUID,
		"--label", fmt.Sprintf("borealis.remote_ops_port=%d", remoteOpsPort),
		"--label", fmt.Sprintf("borealis.remote_desktop_port=%d", remoteDesktopPort),
		"--label", "borealis.site_worker_image=" + image,
		"--label", "borealis.created_by=site-worker-orchestrator",
	}
	envFile := schedulerComposeEnvFile(o.projectRoot)
	if fileExists(envFile) {
		args = append(args, "--env-file", envFile)
	}
	args = append(args,
		"-e", "BOREALIS_SITE_WORKER_GUID="+workerGUID,
		"-e", fmt.Sprintf("BOREALIS_SITE_WORKER_SITE_ID=%d", siteID),
		"-e", "BOREALIS_SITE_WORKER_CONTAINER_NAME="+containerName,
		"-e", "BOREALIS_SITE_WORKER_REMOTE_OPS_HOST=127.0.0.1",
		"-e", fmt.Sprintf("BOREALIS_SITE_WORKER_REMOTE_OPS_PORT=%d", remoteOpsPort),
		"-e", fmt.Sprintf("BOREALIS_SITE_WORKER_REMOTE_DESKTOP_PORT=%d", remoteDesktopPort),
		"-e", "BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE="+schedulerSiteWorkerSocketIOAsyncMode(),
		"-e", "BOREALIS_SITE_WORKER_IDLE_TTL_SECONDS=300",
		"-e", "BOREALIS_INTERNAL_API_BASE_URL="+envDefault("BOREALIS_INTERNAL_API_BASE_URL", "http://127.0.0.1:5000"),
		"-e", fmt.Sprintf("BOREALIS_LOG_FILE=/opt/Borealis/Engine/Services/api-backend/logs/site-workers/%s.log", workerGUID),
		"-e", fmt.Sprintf("BOREALIS_ERROR_LOG_FILE=/opt/Borealis/Engine/Services/api-backend/logs/site-workers/%s-error.log", workerGUID),
		"-e", fmt.Sprintf("BOREALIS_API_LOG_FILE=/opt/Borealis/Engine/Services/api-backend/logs/site-workers/%s-api.log", workerGUID),
		"-e", fmt.Sprintf("BOREALIS_VPN_TUNNEL_LOG_FILE=/opt/Borealis/Engine/Services/api-backend/logs/site-workers/%s-vpn.log", workerGUID),
	)
	if explicit := strings.TrimSpace(os.Getenv("BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY")); explicit != "" {
		args = append(args, "-e", "BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY="+explicit)
	}
	args = append(args,
		"-v", fmt.Sprintf("%s:/opt/Borealis/Engine/Services/api-backend/logs/site-workers", filepath.Join(apiRoot, "logs", "site-workers")),
		"-v", fmt.Sprintf("%s:/opt/Borealis/Engine/Services/traefik-edge/config", filepath.Join(o.projectRoot, "Engine", "Services", "traefik-edge", "config")),
		"-v", fmt.Sprintf("%s:/opt/Borealis/Engine/Services/api-backend/secrets:ro", filepath.Join(apiRoot, "secrets")),
		"-v", fmt.Sprintf("%s:/opt/Borealis/Engine/Services/api-backend/config:ro", filepath.Join(apiRoot, "config")),
		"-v", fmt.Sprintf("%s:/opt/Borealis/Engine/Services/api-backend/cache", filepath.Join(apiRoot, "cache")),
		image,
	)
	out, err := exec.CommandContext(ctx, o.dockerBin, args...).CombinedOutput()
	if err != nil {
		return nil, http.StatusBadGateway, fmt.Errorf("failed to launch %s: %s", containerName, strings.TrimSpace(string(out)))
	}
	return map[string]any{
		"launched":            true,
		"container_name":      containerName,
		"worker_guid":         workerGUID,
		"site_id":             siteID,
		"remote_ops_port":     remoteOpsPort,
		"remote_desktop_port": remoteDesktopPort,
		"container_id":        strings.TrimSpace(string(out)),
	}, http.StatusAccepted, nil
}

func orchestratorSiteWorkerImageAllowed(image string) bool {
	image = strings.TrimSpace(image)
	if image == "" {
		return false
	}
	allowed := map[string]bool{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			allowed[value] = true
		}
	}
	add(schedulerDesiredSiteWorkerImage())
	for _, raw := range strings.FieldsFunc(os.Getenv("BOREALIS_SITE_WORKER_IMAGE_ALLOWLIST"), func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	}) {
		add(raw)
	}
	return allowed[image]
}

func (o *siteWorkerOrchestrator) siteWorkerContainerAction(ctx context.Context, req orchestratorContainerRequest, action string) (map[string]any, int, error) {
	workerGUID := cleanText(req.WorkerGUID)
	containerName := cleanText(req.ContainerName)
	if containerName == "" && workerGUID != "" {
		containerName = "site-worker-" + workerGUID
	}
	if containerName == "" {
		return nil, http.StatusBadRequest, errors.New("container_name or worker_guid is required")
	}
	inspection, found, err := o.inspectSiteWorkerContainer(ctx, containerName)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	if !found {
		return map[string]any{"ok": true, "missing": true, "container_name": containerName}, http.StatusOK, nil
	}
	labels := mapStringAny(nestedMap(inspection, "Config")["Labels"])
	role := strings.ToLower(cleanText(labels["borealis.role"]))
	labelGUID := cleanText(labels["borealis.worker_guid"])
	if role != "site-worker" {
		return nil, http.StatusForbidden, fmt.Errorf("refusing to %s non-site-worker container %s", action, containerName)
	}
	if workerGUID != "" && labelGUID != "" && workerGUID != labelGUID {
		return nil, http.StatusConflict, fmt.Errorf("site-worker guid mismatch for %s", containerName)
	}
	args := []string{}
	switch action {
	case "stop":
		args = []string{"stop", containerName}
	case "remove":
		args = []string{"rm", "-f", containerName}
	default:
		return nil, http.StatusBadRequest, fmt.Errorf("unsupported container action %s", action)
	}
	out, err := exec.CommandContext(ctx, o.dockerBin, args...).CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if strings.Contains(text, "No such") {
			return map[string]any{"ok": true, "missing": true, "container_name": containerName}, http.StatusOK, nil
		}
		return nil, http.StatusBadGateway, fmt.Errorf("%s", text)
	}
	return map[string]any{"ok": true, "container_name": containerName, "worker_guid": labelGUID}, http.StatusOK, nil
}

func (o *siteWorkerOrchestrator) inspectSiteWorkerContainer(ctx context.Context, containerName string) (map[string]any, bool, error) {
	out, err := exec.CommandContext(ctx, o.dockerBin, "inspect", containerName).CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if strings.Contains(text, "No such") {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("%s", text)
	}
	var inspected []map[string]any
	if err := json.Unmarshal(out, &inspected); err != nil {
		return nil, false, err
	}
	if len(inspected) == 0 {
		return nil, false, nil
	}
	return inspected[0], true, nil
}

func (o *siteWorkerOrchestrator) runServiceAction(ctx context.Context, req orchestratorServiceActionRequest) error {
	serviceKey := strings.ToLower(cleanText(req.ServiceKey))
	actionName := strings.ToLower(cleanText(req.Action))
	actionMode := strings.ToLower(cleanText(req.Mode))
	if serviceKey == "" || actionName == "" {
		return errors.New("service_key and action are required")
	}
	resolved := resolveOverviewServiceAction(serviceKey, map[string]any{"action": actionName, "mode": actionMode})
	if resolved == nil {
		return fmt.Errorf("unsupported service action service=%s action=%s mode=%s", serviceKey, actionName, actionMode)
	}
	actionName = strings.ToLower(cleanText(resolved["action"]))
	actionMode = strings.ToLower(cleanText(resolved["mode"]))
	image := schedulerServiceActionHelperImage()
	helperName := "borealis-engine-action-" + serviceKey + "-" + randomShortID()
	commandParts := []string{"bash", "Engine.sh", "--network-mode", overviewEngineNetworkMode(), "--service", serviceKey, actionName}
	if actionMode != "" {
		commandParts = append(commandParts, actionMode)
	}
	shellCommand := "sleep 2; " + shellJoin(commandParts)
	args := []string{
		"run", "--rm", "-d", "--name", helperName, "--network", "host",
		"-v", "/var/run/docker.sock:/var/run/docker.sock",
		"-v", fmt.Sprintf("%s:%s", o.projectRoot, o.projectRoot),
		"-w", o.projectRoot,
		"--entrypoint", "/bin/bash",
		image, "-lc", shellCommand,
	}
	out, err := exec.CommandContext(ctx, o.dockerBin, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	log.Printf("site-worker orchestrator queued service action helper=%s service=%s action=%s", strings.TrimSpace(string(out)), serviceKey, actionName)
	return nil
}

func (o *siteWorkerOrchestrator) serviceSnapshots(ctx context.Context) ([]map[string]any, error) {
	composeFile := filepath.Join(o.projectRoot, "Data", "Engine", "Containers", "compose.yaml")
	envFile := schedulerComposeEnvFile(o.projectRoot)
	if !fileExists(composeFile) || !fileExists(envFile) {
		return []map[string]any{}, nil
	}
	projectName := envDefault("BOREALIS_COMPOSE_PROJECT_NAME", "borealis-engine")
	args := []string{"compose", "--project-name", projectName, "--env-file", envFile, "-f", composeFile, "ps", "--format", "json"}
	out, err := exec.CommandContext(ctx, o.dockerBin, args...).Output()
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return []map[string]any{}, err
	}
	return parseDockerComposePS(out), nil
}

func newSiteWorkerOrchestratorClient(secret []byte) *siteWorkerOrchestratorClient {
	socketPath := siteWorkerOrchestratorSocketPath()
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socketPath)
		},
	}
	return &siteWorkerOrchestratorClient{
		socketPath: socketPath,
		token:      goInternalToken(secret),
		baseURL:    "http://site-worker-orchestrator",
		httpClient: &http.Client{Transport: transport, Timeout: 30 * time.Second},
	}
}

func (c *siteWorkerOrchestratorClient) health(ctx context.Context) error {
	var payload map[string]any
	return c.do(ctx, http.MethodGet, "/health", nil, &payload, 5*time.Second)
}

func (c *siteWorkerOrchestratorClient) launchSiteWorker(ctx context.Context, req orchestratorLaunchRequest) error {
	var payload map[string]any
	return c.do(ctx, http.MethodPost, "/site-workers/launch", req, &payload, 30*time.Second)
}

func (c *siteWorkerOrchestratorClient) listSiteWorkers(ctx context.Context) ([]map[string]any, error) {
	var payload struct {
		Workers []map[string]any `json:"workers"`
	}
	if err := c.do(ctx, http.MethodGet, "/site-workers/list", nil, &payload, 30*time.Second); err != nil {
		return nil, err
	}
	return payload.Workers, nil
}

func (c *siteWorkerOrchestratorClient) stopSiteWorker(ctx context.Context, workerGUID, containerName string) error {
	var payload map[string]any
	return c.do(ctx, http.MethodPost, "/site-workers/stop", orchestratorContainerRequest{WorkerGUID: workerGUID, ContainerName: containerName}, &payload, 30*time.Second)
}

func (c *siteWorkerOrchestratorClient) removeSiteWorker(ctx context.Context, workerGUID, containerName string) error {
	var payload map[string]any
	return c.do(ctx, http.MethodPost, "/site-workers/remove", orchestratorContainerRequest{WorkerGUID: workerGUID, ContainerName: containerName}, &payload, 30*time.Second)
}

func (c *siteWorkerOrchestratorClient) runServiceAction(ctx context.Context, serviceKey string, action map[string]any) error {
	var payload map[string]any
	return c.do(ctx, http.MethodPost, "/services/action", orchestratorServiceActionRequest{
		ServiceKey: serviceKey,
		Action:     strings.ToLower(cleanText(action["action"])),
		Mode:       strings.ToLower(cleanText(action["mode"])),
	}, &payload, 30*time.Second)
}

func (c *siteWorkerOrchestratorClient) serviceSnapshots(ctx context.Context) ([]map[string]any, error) {
	var payload struct {
		Snapshots []map[string]any `json:"snapshots"`
	}
	if err := c.do(ctx, http.MethodGet, "/services/snapshots", nil, &payload, 30*time.Second); err != nil {
		return nil, err
	}
	return payload.Snapshots, nil
}

func (c *siteWorkerOrchestratorClient) do(ctx context.Context, method string, path string, body any, out any, timeout time.Duration) error {
	if c == nil || c.httpClient == nil {
		return errors.New("site-worker orchestrator client unavailable")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	baseURL := strings.TrimRight(firstText(c.baseURL, "http://site-worker-orchestrator"), "/")
	req, err := http.NewRequestWithContext(requestCtx, method, baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set(internalTokenHeader, c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out)
	} else {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 2<<20))
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("site-worker orchestrator %s returned HTTP %d", path, resp.StatusCode)
	}
	return nil
}
