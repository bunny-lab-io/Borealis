package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	defaultSocketPath  = "/run/borealis/node-manager.sock"
	defaultSecretPath  = "/etc/borealis/node-manager.token"
	defaultRepoRoot    = "/opt/Borealis"
	managerTokenHeader = "X-Borealis-Node-Manager-Token"
	maxRequestBytes    = 64 << 10
	actionRuntimeID    = 64646
	defaultK3sVersion  = "v1.36.3+k3s1"
	memberFencePath    = "/etc/borealis/k3s-member-removal-fence.json"
	k3sRegistriesPath  = "/etc/rancher/k3s/registries.yaml"
)

var (
	releasePattern     = regexp.MustCompile(`^[0-9]{4}\.[0-9]{1,2}\.[0-9]+(?:\.[0-9]+)?$`)
	shaPattern         = regexp.MustCompile(`^[0-9a-f]{40}$`)
	nodePattern        = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?$`)
	k3sPattern         = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+\+k3s[0-9]+$`)
	clusterUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

type actionRequest struct {
	Verb   string         `json:"verb"`
	Params map[string]any `json:"params"`
}

type manager struct {
	repoRoot string
	nodeName string
	secret   []byte
}

func main() {
	if len(os.Args) < 2 {
		fatalf("usage: borealis-node-manager <serve|status|join|client>")
	}
	switch os.Args[1] {
	case "serve":
		serve()
	case "status":
		m := loadManager(false)
		result, err := m.execute(context.Background(), actionRequest{Verb: "Status"})
		writeResult(result, err)
	case "join":
		join(os.Args[2:])
	case "client":
		client(os.Args[2:])
	default:
		fatalf("unsupported command %q", os.Args[1])
	}
}

func client(args []string) {
	flags := flag.NewFlagSet("client", flag.ExitOnError)
	verb := flags.String("verb", "", "Fixed node-manager action")
	release := flags.String("release-tag", "", "Dotted numeric release tag")
	targetSHA := flags.String("target-sha", "", "Pinned lowercase commit SHA")
	schemaPhase := flags.String("schema-phase", "", "Fixed cluster schema phase")
	k3sVersion := flags.String("k3s-version", "", "Stable K3s version")
	reason := flags.String("reason", "", "Single-line drain reason")
	controlVIP := flags.String("control-plane-vip", "", "K3s control-plane VIP")
	edgeVIP := flags.String("edge-vip", "", "Borealis edge VIP")
	_ = flags.Parse(args)
	allowed := map[string]bool{
		"Status": true, "EnterApplicationDrain": true, "ExitApplicationDrain": true,
		"PrepareApplicationRestore": true,
		"FenceEdge":                 true, "RestoreEdgeEligibility": true, "FetchRelease": true,
		"PreflightRelease": true, "StagePinnedRelease": true, "RedeployRevision": true, "RedeployStagedRevision": true,
		"StageRevisionImages": true,
		"InspectHealth":       true, "InspectCandidateHealth": true, "PromoteCandidate": true, "EnrollCluster": true,
		"RunSchemaPhase":         true,
		"PrepareMemberRemoval":   true,
		"RunK3sProbeConformance": true,
		"CreateEtcdSnapshot":     true,
	}
	if !allowed[strings.TrimSpace(*verb)] {
		fatalf("unsupported fixed verb %q", *verb)
	}
	params := map[string]any{}
	if strings.TrimSpace(*release) != "" {
		params["release_tag"] = strings.TrimSpace(*release)
	}
	if strings.TrimSpace(*targetSHA) != "" {
		params["target_sha"] = strings.ToLower(strings.TrimSpace(*targetSHA))
	}
	if strings.TrimSpace(*schemaPhase) != "" {
		params["schema_phase"] = strings.ToLower(strings.TrimSpace(*schemaPhase))
	}
	if strings.TrimSpace(*k3sVersion) != "" {
		params["k3s_version"] = strings.TrimSpace(*k3sVersion)
	}
	if strings.TrimSpace(*reason) != "" {
		params["reason"] = strings.TrimSpace(*reason)
	}
	if strings.TrimSpace(*controlVIP) != "" {
		params["control_plane_vip"] = strings.TrimSpace(*controlVIP)
	}
	if strings.TrimSpace(*edgeVIP) != "" {
		params["edge_vip"] = strings.TrimSpace(*edgeVIP)
	}
	raw, _ := json.Marshal(actionRequest{Verb: strings.TrimSpace(*verb), Params: params})
	socketPath := envDefault("BOREALIS_NODE_MANAGER_SOCKET", defaultSocketPath)
	secretPath := envDefault("BOREALIS_NODE_MANAGER_SECRET_PATH", defaultSecretPath)
	secret, err := os.ReadFile(secretPath)
	if err != nil || len(strings.TrimSpace(string(secret))) < 32 {
		fatalf("node-manager client secret missing or too short: %s", secretPath)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "unix", socketPath)
	}}
	httpClient := &http.Client{Transport: transport, Timeout: nodeManagerActionTimeout(strings.TrimSpace(*verb)) + time.Minute}
	request, err := http.NewRequest(http.MethodPost, "http://node-manager/v1/action", bytes.NewReader(raw))
	if err != nil {
		fatalf("create node-manager request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(managerTokenHeader, strings.TrimSpace(string(secret)))
	response, err := httpClient.Do(request)
	if err != nil {
		fatalf("node-manager request: %v", err)
	}
	defer response.Body.Close()
	responseRaw, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode != http.StatusOK {
		fatalf("node-manager action rejected HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(responseRaw)))
	}
	fmt.Println(string(responseRaw))
}

func serve() {
	if os.Geteuid() != 0 {
		fatalf("serve requires root")
	}
	m := loadManager(true)
	socketPath := envDefault("BOREALIS_NODE_MANAGER_SOCKET", defaultSocketPath)
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o750); err != nil {
		fatalf("create socket directory: %v", err)
	}
	if info, err := os.Lstat(socketPath); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			fatalf("refusing to replace non-socket path %s", socketPath)
		}
		if err := os.Remove(socketPath); err != nil {
			fatalf("remove stale socket: %v", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		fatalf("inspect socket: %v", err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		fatalf("listen: %v", err)
	}
	defer listener.Close()
	if err := os.Chmod(socketPath, 0o660); err != nil {
		fatalf("secure socket: %v", err)
	}
	if err := os.Chown(socketPath, 0, actionRuntimeID); err != nil {
		fatalf("assign node-action socket group: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "node_name": m.nodeName})
	})
	mux.HandleFunc("POST /v1/action", m.authorized(m.handleAction))
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go m.reportEtcdLeadership(rootCtx)
	exited := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		exited <- err
	}()
	select {
	case <-rootCtx.Done():
	case err := <-exited:
		if err != nil {
			fatalf("server: %v", err)
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
	_ = os.Remove(socketPath)
}

func (m *manager) reportEtcdLeadership(ctx context.Context) {
	client := &http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	lastReported := ""
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:2381/metrics", nil)
		if err == nil {
			response, requestErr := client.Do(request)
			if requestErr == nil {
				raw, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
				response.Body.Close()
				leader, observed := parseEtcdLeadership(raw)
				value := strconv.FormatBool(leader)
				if readErr == nil && response.StatusCode == http.StatusOK && observed && value != lastReported {
					reportCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
					_, labelErr := run(reportCtx, "", "k3s", "kubectl", "label", "node", m.nodeName, "borealis.io/etcd-leader="+value, "--overwrite")
					cancel()
					if labelErr == nil {
						lastReported = value
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func parseEtcdLeadership(raw []byte) (bool, bool) {
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 2 && fields[0] == "etcd_server_is_leader" {
			return fields[1] == "1", fields[1] == "0" || fields[1] == "1"
		}
	}
	return false, false
}

func loadManager(requireSecret bool) *manager {
	repoRoot := envDefault("BOREALIS_PROJECT_ROOT", defaultRepoRoot)
	nodeName := strings.ToLower(strings.TrimSpace(os.Getenv("BOREALIS_CLUSTER_NODE_NAME")))
	if nodeName == "" {
		hostname, _ := os.Hostname()
		nodeName = strings.ToLower(strings.TrimSpace(hostname))
	}
	if !nodePattern.MatchString(nodeName) {
		fatalf("invalid BOREALIS_CLUSTER_NODE_NAME")
	}
	secretPath := envDefault("BOREALIS_NODE_MANAGER_SECRET_PATH", defaultSecretPath)
	secret, err := os.ReadFile(secretPath)
	if requireSecret && (err != nil || len(strings.TrimSpace(string(secret))) < 32) {
		fatalf("node-manager secret missing or too short: %s", secretPath)
	}
	return &manager{repoRoot: repoRoot, nodeName: nodeName, secret: []byte(strings.TrimSpace(string(secret)))}
}

func (m *manager) authorized(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		presented := []byte(strings.TrimSpace(r.Header.Get(managerTokenHeader)))
		if len(presented) != len(m.secret) || subtle.ConstantTimeCompare(presented, m.secret) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

func (m *manager) handleAction(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
	if err != nil || len(raw) > maxRequestBytes {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
		return
	}
	var request actionRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json", "message": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), nodeManagerActionTimeout(request.Verb))
	defer cancel()
	result, err := m.execute(ctx, request)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "node_action_failed", "message": err.Error(), "verb": request.Verb})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "verb": request.Verb, "result": result})
}

func nodeManagerActionTimeout(verb string) time.Duration {
	switch strings.TrimSpace(verb) {
	case "EnrollCluster":
		return 90 * time.Minute
	case "StageRevisionImages", "RedeployRevision", "RedeployStagedRevision", "PromoteCandidate":
		return 60 * time.Minute
	case "RunSchemaPhase":
		return 20 * time.Minute
	default:
		return 30 * time.Minute
	}
}

func (m *manager) execute(ctx context.Context, request actionRequest) (map[string]any, error) {
	if request.Params == nil {
		request.Params = map[string]any{}
	}
	switch strings.TrimSpace(request.Verb) {
	case "Status":
		return m.status(ctx)
	case "EnterApplicationDrain":
		return m.setApplicationState(ctx, "drained", requiredReason(request.Params))
	case "ExitApplicationDrain":
		return m.setApplicationState(ctx, "active", "")
	case "PrepareApplicationRestore":
		return m.prepareApplicationRestore(ctx)
	case "FenceEdge":
		return m.setNodeLabel(ctx, "borealis.io/edge-eligible", "false")
	case "RestoreEdgeEligibility":
		return m.setNodeLabel(ctx, "borealis.io/edge-eligible", "true")
	case "FetchRelease":
		return m.fetchRelease(ctx, requiredRelease(request.Params), requiredSHA(request.Params))
	case "PreflightRelease":
		return m.preflightRelease(ctx, requiredRelease(request.Params), requiredSHA(request.Params))
	case "StagePinnedRelease":
		return m.stagePinnedRelease(ctx, requiredRelease(request.Params), requiredSHA(request.Params))
	case "RedeployRevision":
		return m.redeployRevision(ctx, requiredSHA(request.Params))
	case "StageRevisionImages":
		return m.stageRevisionImages(ctx, requiredSHA(request.Params))
	case "RedeployStagedRevision":
		return m.redeployStagedRevision(ctx, requiredSHA(request.Params))
	case "InspectHealth":
		return m.inspectHealth(ctx)
	case "InspectCandidateHealth":
		return m.inspectCandidateHealth(ctx)
	case "PromoteCandidate":
		return m.promoteCandidate(ctx, requiredSHA(request.Params))
	case "RunSchemaPhase":
		return m.runSchemaPhase(ctx, requiredSchemaPhase(request.Params), requiredSHA(request.Params))
	case "PrepareMemberRemoval":
		return m.prepareMemberRemoval(ctx, requiredReason(request.Params))
	case "RunK3sProbeConformance":
		return m.runK3sProbeConformance(ctx, requiredK3sVersion(request.Params))
	case "CreateEtcdSnapshot":
		return m.createEtcdSnapshot(ctx)
	case "EnrollCluster":
		return m.enrollCluster(ctx, request.Params, requiredSHA(request.Params))
	default:
		return nil, fmt.Errorf("unsupported fixed verb %q", request.Verb)
	}
}

func (m *manager) createEtcdSnapshot(ctx context.Context) (map[string]any, error) {
	name := "borealis-pre-k3s-" + time.Now().UTC().Format("20060102t150405z")
	output, err := run(ctx, "", "k3s", "etcd-snapshot", "save", "--name", name)
	if err != nil {
		return nil, err
	}
	return map[string]any{"node_name": m.nodeName, "snapshot_name": name, "output": truncate(output, 8192)}, nil
}

func (m *manager) runK3sProbeConformance(ctx context.Context, expectedVersion string) (map[string]any, error) {
	if !k3sPattern.MatchString(expectedVersion) {
		return nil, errors.New("stable k3s_version is required")
	}
	versionOutput, err := run(ctx, "", "k3s", "--version")
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(versionOutput)
	if len(fields) < 3 || fields[2] != expectedVersion {
		return nil, fmt.Errorf("running K3s version does not match target %s", expectedVersion)
	}
	scriptPath := filepath.Join(m.repoRoot, "Data", "Engine", "K3s", "cluster", "run-probe-conformance.sh")
	if info, err := os.Stat(scriptPath); err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("K3s probe conformance script is unavailable")
	}
	output, err := run(ctx, m.repoRoot, "/usr/bin/bash", scriptPath)
	if err != nil {
		return nil, err
	}
	resultPath := envDefault("BOREALIS_K3S_PROBE_CONFORMANCE_FILE", "/etc/rancher/k3s/borealis-probe-conformance.json")
	raw, err := os.ReadFile(resultPath)
	if err != nil {
		return nil, err
	}
	var result struct {
		ID         string `json:"id"`
		Status     string `json:"status"`
		K3sVersion string `json:"k3s_version"`
		Trials     int    `json:"trials"`
	}
	if json.Unmarshal(raw, &result) != nil || result.ID != "pod-restart-policy-startup-probe-v2" || result.Status != "passed" || result.K3sVersion != expectedVersion || result.Trials != 10 {
		return nil, errors.New("K3s probe conformance result does not match upgraded version")
	}
	return map[string]any{"node_name": m.nodeName, "k3s_version": expectedVersion, "probe_conformance": "passed", "output": truncate(output, 8192)}, nil
}

func (m *manager) prepareMemberRemoval(ctx context.Context, reason string) (map[string]any, error) {
	if reason == "" {
		reason = "cluster membership removal"
	}
	loadState, err := run(ctx, "", "systemctl", "show", "--property=LoadState", "--value", "k3s.service")
	if err != nil || strings.TrimSpace(loadState) != "loaded" {
		return nil, errors.New("k3s.service is unavailable for controlled membership fence")
	}
	if err := os.MkdirAll(filepath.Dir(memberFencePath), 0o750); err != nil {
		return nil, err
	}
	scheduledAt := time.Now().UTC()
	marker, err := json.Marshal(map[string]any{
		"node_name":    m.nodeName,
		"reason":       reason,
		"scheduled_at": scheduledAt.Format(time.RFC3339Nano),
		"recovery":     "Remove this file and explicitly enable k3s.service only after cluster membership recovery approval.",
	})
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(memberFencePath, append(marker, '\n'), 0o600); err != nil {
		return nil, err
	}
	unitName := fmt.Sprintf("borealis-k3s-member-removal-fence-%d", scheduledAt.Unix())
	if _, err := run(ctx, "", "systemd-run", "--unit", unitName, "--on-active=15s", "--property=Type=oneshot", "--collect", "/usr/bin/systemctl", "disable", "--now", "k3s.service"); err != nil {
		return nil, err
	}
	return map[string]any{
		"node_name":     m.nodeName,
		"fence_marker":  memberFencePath,
		"fence_unit":    unitName,
		"scheduled_at":  scheduledAt.Format(time.RFC3339Nano),
		"delay_seconds": 15,
	}, nil
}

func (m *manager) verifyReleaseRef(ctx context.Context, release, targetSHA string) error {
	if !releasePattern.MatchString(release) || !shaPattern.MatchString(targetSHA) {
		return errors.New("release_tag and target_sha are required and must use fixed formats")
	}
	if err := validateRepositoryRoot(m.repoRoot); err != nil {
		return err
	}
	expectedOrigin := normalizeRemote(envDefault("BOREALIS_ENGINE_REPOSITORY_URL", "https://github.com/bunny-lab-io/Borealis.git"))
	origin, err := runGit(ctx, m.repoRoot, "remote", "get-url", "origin")
	if err != nil || normalizeRemote(origin) != expectedOrigin {
		return errors.New("origin does not match configured Borealis repository")
	}
	if _, err := runGit(ctx, m.repoRoot, "fetch", "--no-tags", "origin", "refs/tags/"+release+":refs/tags/"+release); err != nil {
		return err
	}
	resolved, err := runGit(ctx, m.repoRoot, "rev-parse", "refs/tags/"+release+"^{commit}")
	if err != nil || strings.ToLower(strings.TrimSpace(resolved)) != targetSHA {
		return errors.New("release tag does not resolve to pinned target SHA")
	}
	return nil
}

func (m *manager) stagePinnedRelease(ctx context.Context, release, targetSHA string) (map[string]any, error) {
	if err := m.verifyReleaseRef(ctx, release, targetSHA); err != nil {
		return nil, err
	}
	stageRoot := filepath.Join(m.repoRoot, "Engine", "Releases", targetSHA)
	if info, err := os.Stat(stageRoot); err == nil && info.IsDir() {
		resolved, resolveErr := runGit(ctx, stageRoot, "rev-parse", "HEAD")
		if resolveErr != nil || strings.TrimSpace(resolved) != targetSHA {
			return nil, errors.New("existing staged release does not match pinned SHA")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect staged release: %w", err)
	} else {
		if err := os.MkdirAll(filepath.Dir(stageRoot), 0o750); err != nil {
			return nil, err
		}
		if _, err := runGit(ctx, m.repoRoot, "worktree", "add", "--detach", stageRoot, targetSHA); err != nil {
			return nil, err
		}
	}
	hmrSource := filepath.Join(m.repoRoot, "Engine", "Services", "webui-frontend", "data", "web-interface")
	if info, err := os.Stat(hmrSource); err == nil && info.IsDir() {
		preserveRoot := filepath.Join(m.repoRoot, "Engine", "HMR", time.Now().UTC().Format("20060102T150405Z")+"-"+targetSHA[:12])
		if err := os.MkdirAll(preserveRoot, 0o750); err != nil {
			return nil, err
		}
		if _, err := run(ctx, "", "/bin/cp", "-a", hmrSource, filepath.Join(preserveRoot, "web-interface")); err != nil {
			return nil, err
		}
	}
	return map[string]any{"release_tag": release, "revision": targetSHA, "staged_path": stageRoot}, nil
}

func (m *manager) redeployStagedRevision(ctx context.Context, targetSHA string) (map[string]any, error) {
	if !shaPattern.MatchString(targetSHA) {
		return nil, errors.New("target_sha is required and must be a lowercase commit SHA")
	}
	stageRoot := filepath.Join(m.repoRoot, "Engine", "Releases", targetSHA)
	resolved, err := runGit(ctx, stageRoot, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(resolved) != targetSHA {
		return nil, errors.New("pinned staged worktree is unavailable")
	}
	enginePath := filepath.Join(stageRoot, "Engine.sh")
	engineEnv, err := m.engineChildEnvironment(stageRoot)
	if err != nil {
		return nil, err
	}
	engineEnv = append(stagedRedeployEnvironment(engineEnv, m.repoRoot),
		"/usr/bin/bash", enginePath, "--cluster-node-redeploy", "--revision", targetSHA,
	)
	output, err := run(ctx, stageRoot, "/usr/bin/env", engineEnv...)
	if err != nil {
		return nil, err
	}
	return map[string]any{"revision": targetSHA, "redeployed": true, "staged_path": stageRoot, "output": truncate(output, 8192)}, nil
}

func stagedRedeployEnvironment(base []string, repoRoot string) []string {
	return append(base,
		"BOREALIS_ENGINE_HOST_ROOT="+repoRoot,
		"BOREALIS_ENGINE_RUNTIME_ROOT="+filepath.Join(repoRoot, "Engine"),
		"BOREALIS_CLUSTER_DEPLOYMENT_MODE=candidate",
	)
}

func (m *manager) enrollCluster(ctx context.Context, params map[string]any, baselineSHA string) (map[string]any, error) {
	controlVIP := strings.TrimSpace(fmt.Sprint(params["control_plane_vip"]))
	edgeVIP := strings.TrimSpace(fmt.Sprint(params["edge_vip"]))
	controlIP := net.ParseIP(controlVIP)
	edgeIP := net.ParseIP(edgeVIP)
	if controlIP == nil || controlIP.To4() == nil || edgeIP == nil || edgeIP.To4() == nil || controlVIP == edgeVIP {
		return nil, errors.New("distinct control_plane_vip and edge_vip IPv4 values are required")
	}
	if !shaPattern.MatchString(baselineSHA) {
		return nil, errors.New("target_sha is required and must be a lowercase commit SHA")
	}
	if err := validateRepositoryRoot(m.repoRoot); err != nil {
		return nil, err
	}
	enginePath := filepath.Join(m.repoRoot, "Engine.sh")
	output, err := run(ctx, m.repoRoot, "/usr/bin/env", "BOREALIS_CLUSTER_ENROLL_OPERATION=1", "BOREALIS_CLUSTER_BASELINE_SHA="+baselineSHA, "/usr/bin/bash", enginePath, "--cluster-enable", "--control-plane-vip", controlVIP, "--edge-vip", edgeVIP)
	if err != nil {
		return nil, err
	}
	return map[string]any{"node_name": m.nodeName, "control_plane_vip": controlVIP, "edge_vip": edgeVIP, "enrolled": true, "output": truncate(output, 8192)}, nil
}

func (m *manager) status(ctx context.Context) (map[string]any, error) {
	revision, revisionErr := runGit(ctx, m.repoRoot, "rev-parse", "HEAD")
	branch, _ := runGit(ctx, m.repoRoot, "branch", "--show-current")
	dirty, dirtyErr := runGit(ctx, m.repoRoot, "status", "--porcelain", "--untracked-files=normal")
	nodeJSON, nodeErr := run(ctx, "", "k3s", "kubectl", "get", "node", m.nodeName, "-o", "json")
	return map[string]any{
		"node_name":          m.nodeName,
		"architecture":       runtime.GOARCH,
		"revision":           strings.TrimSpace(revision),
		"branch":             strings.TrimSpace(branch),
		"worktree_clean":     dirtyErr == nil && strings.TrimSpace(dirty) == "",
		"git_available":      revisionErr == nil,
		"k3s_node_available": nodeErr == nil && strings.TrimSpace(nodeJSON) != "",
	}, nil
}

func (m *manager) setApplicationState(ctx context.Context, state, reason string) (map[string]any, error) {
	if state != "active" && state != "drained" {
		return nil, errors.New("invalid application state")
	}
	if state == "drained" {
		if _, err := m.setNodeLabel(ctx, "borealis.io/application-state", state); err != nil {
			return nil, err
		}
	}
	if err := m.scaleNodeApplications(ctx, state); err != nil {
		return nil, err
	}
	if state == "active" {
		if _, err := m.setNodeLabel(ctx, "borealis.io/application-state", state); err != nil {
			return nil, err
		}
	}
	annotation := "borealis.io/drain-reason-"
	if reason != "" {
		annotation = "borealis.io/drain-reason=" + reason
	}
	if _, err := run(ctx, "", "k3s", "kubectl", "annotate", "node", m.nodeName, annotation, "--overwrite"); err != nil {
		return nil, err
	}
	return map[string]any{"node_name": m.nodeName, "application_state": state, "reason": reason}, nil
}

func (m *manager) prepareApplicationRestore(ctx context.Context) (map[string]any, error) {
	node, err := run(ctx, "", "k3s", "kubectl", "get", "node", m.nodeName, "-o", "json")
	if err != nil {
		return nil, err
	}
	if nodeLabelValue([]byte(node), "borealis.io/application-state") != "drained" {
		return nil, errors.New("application restore requires node to remain drained")
	}
	if err := m.scaleNodeApplications(ctx, "active"); err != nil {
		return nil, err
	}
	return map[string]any{"node_name": m.nodeName, "application_state": "drained", "workloads_prepared": true}, nil
}

func (m *manager) scaleNodeApplications(ctx context.Context, state string) error {
	deploymentsJSON, err := run(ctx, "", "k3s", "kubectl", "-n", "borealis", "get", "deployments", "-l", "borealis.io/engine-node="+m.nodeName+",borealis.io/node-workload=true", "-o", "json")
	if err != nil {
		return err
	}
	var deployments struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(deploymentsJSON), &deployments); err != nil {
		return fmt.Errorf("decode node deployments: %w", err)
	}
	replicas := "1"
	if state == "drained" {
		replicas = "0"
	}
	scalable := map[string]bool{
		"api-backend": true, "job-scheduler": true, "remote-desktop-guacd": true,
		"traefik-edge": true, "webui-frontend": true,
	}
	scaled := make([]string, 0, len(deployments.Items))
	for _, deployment := range deployments.Items {
		if !scalable[deployment.Metadata.Labels["app.kubernetes.io/name"]] {
			continue
		}
		if !nodePattern.MatchString(deployment.Metadata.Name) {
			return errors.New("node workload deployment name is invalid")
		}
		if _, err := run(ctx, "", "k3s", "kubectl", "-n", "borealis", "scale", "deployment/"+deployment.Metadata.Name, "--replicas="+replicas); err != nil {
			return err
		}
		scaled = append(scaled, deployment.Metadata.Name)
	}
	if state == "active" {
		for _, name := range scaled {
			if _, err := run(ctx, "", "k3s", "kubectl", "-n", "borealis", "rollout", "status", "deployment/"+name, "--timeout=10m"); err != nil {
				return err
			}
		}
	}
	if state == "drained" {
		_, err = run(ctx, "", "k3s", "kubectl", "-n", "borealis", "delete", "pods", "-l", "app.kubernetes.io/name=site-worker", "--field-selector", "spec.nodeName="+m.nodeName, "--grace-period=60", "--ignore-not-found=true")
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *manager) setNodeLabel(ctx context.Context, key, value string) (map[string]any, error) {
	if _, err := run(ctx, "", "k3s", "kubectl", "label", "node", m.nodeName, key+"="+value, "--overwrite"); err != nil {
		return nil, err
	}
	if key == "borealis.io/edge-eligible" {
		// WireGuard control stays present on every active node. Edge VIP
		// ownership gates shared interface activation inside controller.
		if err := m.scaleNamedNodeWorkload(ctx, "wireguard-tunnel", "1"); err != nil {
			return nil, err
		}
	}
	return map[string]any{"node_name": m.nodeName, "label": key, "value": value}, nil
}

func (m *manager) scaleNamedNodeWorkload(ctx context.Context, appName, replicas string) error {
	payload, err := run(ctx, "", "k3s", "kubectl", "-n", "borealis", "get", "deployments", "-l", "borealis.io/engine-node="+m.nodeName+",borealis.io/node-workload=true,app.kubernetes.io/name="+appName, "-o", "json")
	if err != nil {
		return err
	}
	var deployments struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(payload), &deployments); err != nil {
		return fmt.Errorf("decode %s node workload: %w", appName, err)
	}
	if len(deployments.Items) != 1 || !nodePattern.MatchString(deployments.Items[0].Metadata.Name) {
		return fmt.Errorf("expected one valid %s workload on node", appName)
	}
	_, err = run(ctx, "", "k3s", "kubectl", "-n", "borealis", "scale", "deployment/"+deployments.Items[0].Metadata.Name, "--replicas="+replicas)
	return err
}

func (m *manager) fetchRelease(ctx context.Context, release, targetSHA string) (map[string]any, error) {
	result, err := m.preflightRelease(ctx, release, targetSHA)
	if err != nil {
		return nil, err
	}
	if _, err := runGit(ctx, m.repoRoot, "merge", "--ff-only", targetSHA); err != nil {
		return nil, err
	}
	result["fast_forwarded"] = true
	return result, nil
}

func (m *manager) preflightRelease(ctx context.Context, release, targetSHA string) (map[string]any, error) {
	if err := m.verifyReleaseRef(ctx, release, targetSHA); err != nil {
		return nil, err
	}
	dirty, err := runGit(ctx, m.repoRoot, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(dirty) != "" {
		return nil, errors.New("worktree is not clean; refusing stash, reset, or checkout")
	}
	if _, err := runGit(ctx, m.repoRoot, "merge-base", "--is-ancestor", "HEAD", targetSHA); err != nil {
		return nil, errors.New("target is not a fast-forward descendant")
	}
	return map[string]any{"release_tag": release, "revision": targetSHA, "worktree_clean": true, "fast_forward": true}, nil
}

func (m *manager) redeployRevision(ctx context.Context, targetSHA string) (map[string]any, error) {
	if !shaPattern.MatchString(targetSHA) {
		return nil, errors.New("target_sha is required and must be a lowercase commit SHA")
	}
	current, err := runGit(ctx, m.repoRoot, "rev-parse", "HEAD")
	if err != nil || strings.ToLower(strings.TrimSpace(current)) != targetSHA {
		return nil, errors.New("worktree HEAD does not match requested revision")
	}
	enginePath := filepath.Join(m.repoRoot, "Engine.sh")
	if info, err := os.Stat(enginePath); err != nil || info.Mode().IsRegular() == false {
		return nil, errors.New("Engine.sh missing from repository root")
	}
	engineEnv, err := m.engineChildEnvironment(m.repoRoot)
	if err != nil {
		return nil, err
	}
	engineEnv = append(engineEnv,
		"BOREALIS_CLUSTER_DEPLOYMENT_MODE=candidate",
		"/usr/bin/bash", enginePath, "--cluster-node-redeploy", "--revision", targetSHA,
	)
	output, err := run(ctx, m.repoRoot, "/usr/bin/env", engineEnv...)
	if err != nil {
		return nil, err
	}
	return map[string]any{"revision": targetSHA, "redeployed": true, "output": truncate(output, 8192)}, nil
}

func (m *manager) stageRevisionImages(ctx context.Context, targetSHA string) (map[string]any, error) {
	if !shaPattern.MatchString(targetSHA) {
		return nil, errors.New("target_sha is required and must be a lowercase commit SHA")
	}
	current, err := runGit(ctx, m.repoRoot, "rev-parse", "HEAD")
	if err != nil || strings.ToLower(strings.TrimSpace(current)) != targetSHA {
		return nil, errors.New("worktree HEAD does not match requested revision")
	}
	enginePath := filepath.Join(m.repoRoot, "Engine.sh")
	if info, statErr := os.Stat(enginePath); statErr != nil || !info.Mode().IsRegular() {
		return nil, errors.New("Engine.sh missing from repository root")
	}
	engineEnv, err := m.engineChildEnvironment(m.repoRoot)
	if err != nil {
		return nil, err
	}
	engineEnv = append(engineEnv,
		"/usr/bin/bash", enginePath, "--cluster-stage-revision", "--revision", targetSHA,
	)
	output, err := run(ctx, m.repoRoot, "/usr/bin/env", engineEnv...)
	if err != nil {
		return nil, err
	}
	return map[string]any{"revision": targetSHA, "images_staged": true, "output": truncate(output, 8192)}, nil
}

func (m *manager) runSchemaPhase(ctx context.Context, phase, targetSHA string) (map[string]any, error) {
	if phase != "expand" && phase != "finalize" {
		return nil, errors.New("schema_phase must be expand or finalize")
	}
	if !shaPattern.MatchString(targetSHA) {
		return nil, errors.New("target_sha is required and must be a lowercase commit SHA")
	}
	current, err := runGit(ctx, m.repoRoot, "rev-parse", "HEAD")
	if err != nil || strings.ToLower(strings.TrimSpace(current)) != targetSHA {
		return nil, errors.New("worktree HEAD does not match schema phase revision")
	}
	enginePath := filepath.Join(m.repoRoot, "Engine.sh")
	if info, statErr := os.Stat(enginePath); statErr != nil || !info.Mode().IsRegular() {
		return nil, errors.New("Engine.sh missing from repository root")
	}
	engineEnv, err := m.engineChildEnvironment(m.repoRoot)
	if err != nil {
		return nil, err
	}
	engineEnv = append(engineEnv,
		"/usr/bin/bash", enginePath,
		"--cluster-schema-phase", "--schema-phase", phase, "--revision", targetSHA,
	)
	output, err := run(ctx, m.repoRoot, "/usr/bin/env", engineEnv...)
	if err != nil {
		return nil, err
	}
	return map[string]any{"revision": targetSHA, "schema_phase": phase, "completed": true, "output": truncate(output, 8192)}, nil
}

func gitSafeDirectoryEnvironment(repoRoot string) []string {
	return []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=safe.directory",
		"GIT_CONFIG_VALUE_0=" + repoRoot,
	}
}

func (m *manager) engineChildEnvironment(gitRoot string) ([]string, error) {
	home := filepath.Join(m.repoRoot, "Engine", "Deploy", "node-manager-home")
	if err := ensureSecureDirectory(home); err != nil {
		return nil, fmt.Errorf("prepare Engine child home: %w", err)
	}
	return append(gitSafeDirectoryEnvironment(gitRoot),
		"HOME="+home,
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
	), nil
}

func (m *manager) promoteCandidate(ctx context.Context, targetSHA string) (map[string]any, error) {
	if !shaPattern.MatchString(targetSHA) {
		return nil, errors.New("target_sha is required and must be a lowercase commit SHA")
	}
	reconciler := filepath.Join(m.repoRoot, "Data", "Engine", "K3s", "cluster", "reconcile-node-workloads.py")
	imageManifest := filepath.Join(m.repoRoot, "Engine", "Deploy", "image-manifest.json")
	if info, err := os.Stat(reconciler); err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("cluster workload reconciler is unavailable")
	}
	output, err := run(ctx, m.repoRoot, "/usr/bin/python3", reconciler,
		"--node", m.nodeName,
		"--revision", targetSHA,
		"--image-manifest", imageManifest,
		"--promote-candidate",
	)
	if err != nil {
		return nil, err
	}
	return map[string]any{"revision": targetSHA, "promoted": true, "output": truncate(output, 8192)}, nil
}

func (m *manager) inspectCandidateHealth(ctx context.Context) (map[string]any, error) {
	selector := "borealis.io/engine-node=" + m.nodeName + "," + "borealis.io/update-candidate=true"
	deployments, deploymentErr := run(ctx, "", "k3s", "kubectl", "-n", "borealis", "get", "deployments", "-l", selector, "-o", "json")
	pods, podErr := run(ctx, "", "k3s", "kubectl", "-n", "borealis", "get", "pods", "-l", selector, "-o", "json")
	endpoints, endpointErr := run(ctx, "", "k3s", "kubectl", "-n", "borealis", "get", "endpointslices", "-l", "kubernetes.io/service-name=api-backend", "-o", "json")
	if deploymentErr != nil || podErr != nil || endpointErr != nil {
		return nil, errors.New("candidate workload or endpoint health inspection failed")
	}
	workloads, allReady, err := readyCandidateWorkloads([]byte(deployments))
	if err != nil {
		return nil, err
	}
	if !allReady || !workloads["api-backend-candidate"] || !workloads["job-scheduler-candidate"] {
		return nil, errors.New("required candidate workloads are not ready")
	}
	address, port, err := readyCandidateAPIEndpoint([]byte(pods))
	if err != nil {
		return nil, err
	}
	if endpointSliceContainsAddress([]byte(endpoints), address) {
		return nil, errors.New("candidate API endpoint entered shared Service before promotion")
	}
	for _, path := range []string{"/startup", "/ready", "/live"} {
		if err := probeHTTP(ctx, address, port, path); err != nil {
			return nil, fmt.Errorf("candidate API probe %s failed: %w", path, err)
		}
	}
	if err := probeHTTPExpectedStatus(ctx, address, port, http.MethodPost, "/api/agent/heartbeat", http.StatusUnauthorized); err != nil {
		return nil, fmt.Errorf("candidate Agent API path probe failed: %w", err)
	}
	return map[string]any{"node_name": m.nodeName, "candidate_isolated": true, "candidate_address": address, "agent_path": "passed", "workloads": workloads}, nil
}

func (m *manager) inspectHealth(ctx context.Context) (map[string]any, error) {
	node, nodeErr := run(ctx, "", "k3s", "kubectl", "get", "node", m.nodeName, "-o", "json")
	deployments, deploymentErr := run(ctx, "", "k3s", "kubectl", "-n", "borealis", "get", "deployments", "-l", "borealis.io/engine-node="+m.nodeName+",borealis.io/node-workload=true", "-o", "json")
	endpoints, endpointErr := run(ctx, "", "k3s", "kubectl", "-n", "borealis", "get", "endpointslices", "-l", "kubernetes.io/service-name=api-backend", "-o", "json")
	postgres, postgresErr := run(ctx, "", "k3s", "kubectl", "-n", "borealis", "get", "pods", "-l", "cnpg.io/cluster=borealis-postgres", "-o", "json")
	service, serviceErr := run(ctx, "", "k3s", "kubectl", "-n", "borealis", "get", "service", "api-backend", "-o", "json")
	cluster, clusterErr := run(ctx, "", "k3s", "kubectl", "-n", "borealis", "get", "borealiscluster.engine.borealis.io/borealis", "-o", "json")
	if nodeErr != nil || deploymentErr != nil || endpointErr != nil || postgresErr != nil || serviceErr != nil || clusterErr != nil {
		return nil, errors.New("K3s node, workload, endpoint, PostgreSQL, or Service health inspection failed")
	}
	if !nodeReady([]byte(node)) {
		return nil, errors.New("K3s node Ready condition is not true")
	}
	workloads, err := readyNodeWorkloads([]byte(deployments))
	if err != nil {
		return nil, err
	}
	directAddress, directPort, err := readyAPIEndpoint([]byte(endpoints), m.nodeName)
	if err != nil {
		return nil, err
	}
	serviceAddress, servicePort, err := apiServiceAddress([]byte(service))
	if err != nil {
		return nil, err
	}
	if !podListHasReadyPod([]byte(postgres)) {
		return nil, errors.New("CloudNativePG has no ready instance")
	}
	for _, path := range []string{"/startup", "/live"} {
		if err := probeHTTP(ctx, directAddress, directPort, path); err != nil {
			return nil, fmt.Errorf("direct API probe %s failed: %w", path, err)
		}
	}
	if err := probeHTTP(ctx, serviceAddress, servicePort, "/ready"); err != nil {
		return nil, fmt.Errorf("API Service readiness probe failed: %w", err)
	}
	if err := probeHTTPExpectedStatus(ctx, serviceAddress, servicePort, http.MethodPost, "/api/agent/heartbeat", http.StatusUnauthorized); err != nil {
		return nil, fmt.Errorf("Agent API Service path probe failed: %w", err)
	}
	if nodeLabelTrue([]byte(node), "borealis.io/edge-eligible") {
		edgeVIP, err := clusterEdgeVIP([]byte(cluster))
		if err != nil {
			return nil, err
		}
		if err := probeTCP(ctx, edgeVIP, 443); err != nil {
			return nil, fmt.Errorf("edge VIP probe failed: %w", err)
		}
	}
	requiredWorkloads := []string{"api-backend", "job-scheduler"}
	if nodeLabelTrue([]byte(node), "borealis.io/edge-eligible") {
		requiredWorkloads = append(requiredWorkloads, "wireguard-tunnel")
	}
	for _, required := range requiredWorkloads {
		if !workloads[required] {
			return nil, fmt.Errorf("required node workload %s is not available", required)
		}
	}
	probes := map[string]string{
		"startup": "passed", "readiness": "passed", "liveness": "passed",
		"direct_endpoint": "passed", "service": "passed", "database": "passed",
		"scheduler": "passed", "agent_path": "passed", "wireguard": "passed",
	}
	return map[string]any{"node_name": m.nodeName, "probes": probes, "workloads": workloads}, nil
}

func clusterEdgeVIP(raw []byte) (string, error) {
	var cluster struct {
		Spec struct {
			EdgeVIP string `json:"edgeVIP"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(raw, &cluster); err != nil {
		return "", fmt.Errorf("decode Borealis cluster: %w", err)
	}
	address := net.ParseIP(strings.TrimSpace(cluster.Spec.EdgeVIP))
	if address == nil || address.To4() == nil || !address.IsPrivate() {
		return "", errors.New("Borealis cluster edge VIP is invalid")
	}
	return address.String(), nil
}

func probeTCP(ctx context.Context, host string, port int) error {
	dialer := net.Dialer{Timeout: 3 * time.Second}
	connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	return connection.Close()
}

func nodeReady(raw []byte) bool {
	var node struct {
		Status struct {
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
		} `json:"status"`
	}
	if json.Unmarshal(raw, &node) != nil {
		return false
	}
	for _, condition := range node.Status.Conditions {
		if condition.Type == "Ready" {
			return condition.Status == "True"
		}
	}
	return false
}

func nodeLabelTrue(raw []byte, name string) bool {
	return strings.EqualFold(nodeLabelValue(raw, name), "true")
}

func nodeLabelValue(raw []byte, name string) string {
	var node struct {
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	if json.Unmarshal(raw, &node) != nil {
		return ""
	}
	return strings.TrimSpace(node.Metadata.Labels[name])
}

func readyNodeWorkloads(raw []byte) (map[string]bool, error) {
	var payload struct {
		Items []struct {
			Metadata struct {
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				Replicas int64 `json:"replicas"`
			} `json:"spec"`
			Status struct {
				Available int64 `json:"availableReplicas"`
				Ready     int64 `json:"readyReplicas"`
				Updated   int64 `json:"updatedReplicas"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode node workloads: %w", err)
	}
	ready := map[string]bool{}
	for _, item := range payload.Items {
		name := item.Metadata.Labels["app.kubernetes.io/name"]
		if name == "" || item.Spec.Replicas < 1 {
			continue
		}
		ready[name] = item.Status.Available >= item.Spec.Replicas && item.Status.Ready >= item.Spec.Replicas && item.Status.Updated >= item.Spec.Replicas
	}
	return ready, nil
}

func readyCandidateWorkloads(raw []byte) (map[string]bool, bool, error) {
	var payload struct {
		Items []struct {
			Metadata struct {
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				Replicas int64 `json:"replicas"`
			} `json:"spec"`
			Status struct {
				Available int64 `json:"availableReplicas"`
				Ready     int64 `json:"readyReplicas"`
				Updated   int64 `json:"updatedReplicas"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, false, fmt.Errorf("decode candidate workloads: %w", err)
	}
	ready := map[string]bool{}
	runnable := 0
	allReady := true
	for _, item := range payload.Items {
		if !strings.EqualFold(item.Metadata.Labels["borealis.io/update-candidate"], "true") || item.Spec.Replicas < 1 {
			continue
		}
		runnable++
		name := item.Metadata.Labels["app.kubernetes.io/name"]
		isReady := name != "" && item.Status.Available >= item.Spec.Replicas && item.Status.Ready >= item.Spec.Replicas && item.Status.Updated >= item.Spec.Replicas
		ready[name] = isReady
		allReady = allReady && isReady
	}
	return ready, runnable > 0 && allReady, nil
}

func readyCandidateAPIEndpoint(raw []byte) (string, int, error) {
	var payload struct {
		Items []struct {
			Metadata struct {
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				Containers []struct {
					Ports []struct {
						Name          string `json:"name"`
						ContainerPort int    `json:"containerPort"`
					} `json:"ports"`
				} `json:"containers"`
			} `json:"spec"`
			Status struct {
				PodIP      string `json:"podIP"`
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", 0, fmt.Errorf("decode candidate pods: %w", err)
	}
	for _, pod := range payload.Items {
		if pod.Metadata.Labels["app.kubernetes.io/name"] != "api-backend-candidate" || !strings.EqualFold(pod.Metadata.Labels["borealis.io/update-candidate"], "true") || net.ParseIP(pod.Status.PodIP) == nil {
			continue
		}
		ready := false
		for _, condition := range pod.Status.Conditions {
			if condition.Type == "Ready" && condition.Status == "True" {
				ready = true
			}
		}
		if !ready {
			continue
		}
		for _, container := range pod.Spec.Containers {
			for _, port := range container.Ports {
				if port.Name == "http" && port.ContainerPort > 0 {
					return pod.Status.PodIP, port.ContainerPort, nil
				}
			}
		}
	}
	return "", 0, errors.New("ready isolated candidate API pod is unavailable")
}

func endpointSliceContainsAddress(raw []byte, expected string) bool {
	if net.ParseIP(expected) == nil {
		return true
	}
	var payload struct {
		Items []struct {
			Endpoints []struct {
				Addresses []string `json:"addresses"`
			} `json:"endpoints"`
		} `json:"items"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return true
	}
	for _, item := range payload.Items {
		for _, endpoint := range item.Endpoints {
			for _, address := range endpoint.Addresses {
				if address == expected {
					return true
				}
			}
		}
	}
	return false
}

func readyAPIEndpoint(raw []byte, nodeName string) (string, int, error) {
	var payload struct {
		Items []struct {
			Ports []struct {
				Port int `json:"port"`
			} `json:"ports"`
			Endpoints []struct {
				Addresses  []string `json:"addresses"`
				NodeName   string   `json:"nodeName"`
				Conditions struct {
					Ready *bool `json:"ready"`
				} `json:"conditions"`
			} `json:"endpoints"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", 0, fmt.Errorf("decode API endpoints: %w", err)
	}
	for _, item := range payload.Items {
		if len(item.Ports) == 0 || item.Ports[0].Port < 1 {
			continue
		}
		for _, endpoint := range item.Endpoints {
			if endpoint.NodeName == nodeName && len(endpoint.Addresses) > 0 && (endpoint.Conditions.Ready == nil || *endpoint.Conditions.Ready) {
				return endpoint.Addresses[0], item.Ports[0].Port, nil
			}
		}
	}
	return "", 0, errors.New("node has no ready direct API endpoint")
}

func apiServiceAddress(raw []byte) (string, int, error) {
	var service struct {
		Spec struct {
			ClusterIP string `json:"clusterIP"`
			Ports     []struct {
				Port int `json:"port"`
			} `json:"ports"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(raw, &service); err != nil || net.ParseIP(service.Spec.ClusterIP) == nil || len(service.Spec.Ports) == 0 || service.Spec.Ports[0].Port < 1 {
		return "", 0, errors.New("API Service has no valid ClusterIP and port")
	}
	return service.Spec.ClusterIP, service.Spec.Ports[0].Port, nil
}

func podListHasReadyPod(raw []byte) bool {
	var payload struct {
		Items []struct {
			Status struct {
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return false
	}
	for _, pod := range payload.Items {
		for _, condition := range pod.Status.Conditions {
			if condition.Type == "Ready" && condition.Status == "True" {
				return true
			}
		}
	}
	return false
}

func probeHTTP(ctx context.Context, host string, port int, path string) error {
	return probeHTTPExpectedStatus(ctx, host, port, http.MethodGet, path, http.StatusOK)
}

func probeHTTPExpectedStatus(ctx context.Context, host string, port int, method, path string, expectedStatus int) error {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(probeCtx, method, "http://"+net.JoinHostPort(host, fmt.Sprint(port))+path, nil)
	if err != nil {
		return err
	}
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != expectedStatus {
		return fmt.Errorf("HTTP %d, expected %d", response.StatusCode, expectedStatus)
	}
	return nil
}

func join(args []string) {
	flags := flag.NewFlagSet("join", flag.ExitOnError)
	endpoint := flags.String("endpoint", "", "Existing Borealis cluster base URL")
	bundle := flags.String("invite-bundle", "", "One-use invitation bundle")
	nodeName := flags.String("node-name", "", "Cluster node name")
	managementIP := flags.String("management-ip", "", "Static management IP")
	osVersion := flags.String("os-version", "Ubuntu 24.04", "Operating system version")
	k3sServer := flags.String("k3s-server", "", "Existing K3s control-plane HTTPS URL")
	k3sTokenFile := flags.String("k3s-token-file", "", "Root-readable K3s server token file")
	k3sVersion := flags.String("k3s-version", defaultK3sVersion, "Pinned K3s version")
	peerCIDRs := flags.String("peer-cidrs", "", "Comma-separated private Engine node IPv4 CIDRs")
	caFile := flags.String("ca-file", "", "Optional Borealis API CA certificate")
	_ = flags.Parse(args)
	if os.Geteuid() != 0 {
		fatalf("join requires root")
	}
	managementAddress := net.ParseIP(strings.TrimSpace(*managementIP))
	if strings.TrimSpace(*endpoint) == "" || strings.TrimSpace(*bundle) == "" || !nodePattern.MatchString(strings.ToLower(strings.TrimSpace(*nodeName))) || managementAddress == nil || managementAddress.To4() == nil {
		fatalf("join requires endpoint, invite-bundle, valid node-name, and management IPv4")
	}
	serverURL, err := url.Parse(strings.TrimSpace(*k3sServer))
	if err != nil || serverURL.Scheme != "https" || serverURL.Hostname() == "" || serverURL.Port() != "6443" || serverURL.Path != "" {
		fatalf("join requires --k3s-server https://<control-plane-vip>:6443")
	}
	if !k3sPattern.MatchString(strings.TrimSpace(*k3sVersion)) {
		fatalf("join requires pinned K3s version")
	}
	normalizedPeers, err := normalizePeerCIDRs(*peerCIDRs)
	if err != nil {
		fatalf("join requires --peer-cidrs covering current and planned Engine nodes: %v", err)
	}
	tokenPath := filepath.Clean(strings.TrimSpace(*k3sTokenFile))
	if !filepath.IsAbs(tokenPath) || tokenPath == "/" {
		fatalf("join requires absolute --k3s-token-file")
	}
	tokenInfo, err := os.Stat(tokenPath)
	if err != nil || !tokenInfo.Mode().IsRegular() || tokenInfo.Mode().Perm()&0o077 != 0 {
		fatalf("K3s token file must be regular and inaccessible to group/other")
	}
	repoRoot := envDefault("BOREALIS_PROJECT_ROOT", defaultRepoRoot)
	if err := prepareJoinedNode(repoRoot, normalizedPeers); err != nil {
		fatalf("prepare cluster node host: %v", err)
	}
	hostname, _ := os.Hostname()
	payload := map[string]any{"invite_bundle": strings.TrimSpace(*bundle), "node_name": strings.ToLower(strings.TrimSpace(*nodeName)), "hostname": hostname, "management_ip": strings.TrimSpace(*managementIP), "architecture": runtime.GOARCH, "os_version": strings.TrimSpace(*osVersion)}
	raw, _ := json.Marshal(payload)
	request, err := http.NewRequest(http.MethodPost, strings.TrimRight(strings.TrimSpace(*endpoint), "/")+"/api/bootstrap/cluster/join", bytes.NewReader(raw))
	if err != nil {
		fatalf("create join request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	if strings.TrimSpace(*caFile) != "" {
		client, err = clusterJoinHTTPClient(strings.TrimSpace(*caFile))
		if err != nil {
			fatalf("load Borealis API CA: %v", err)
		}
	}
	response, err := client.Do(request)
	if err != nil {
		fatalf("join request: %v", err)
	}
	defer response.Body.Close()
	responseRaw, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode != http.StatusAccepted {
		fatalf("join rejected HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(responseRaw)))
	}
	var admission map[string]any
	if err := json.Unmarshal(responseRaw, &admission); err != nil || !clusterUUIDPattern.MatchString(strings.ToLower(strings.TrimSpace(fmt.Sprint(admission["admission_id"])))) {
		fatalf("join response lacks canonical admission_id")
	}
	admissionID := strings.ToLower(strings.TrimSpace(fmt.Sprint(admission["admission_id"])))
	if err := waitForClusterAdmission(client, strings.TrimRight(strings.TrimSpace(*endpoint), "/"), strings.TrimSpace(*bundle), admissionID); err != nil {
		fatalf("wait for paired admission approval: %v", err)
	}
	if err := installJoinedK3sServer(strings.ToLower(strings.TrimSpace(*nodeName)), strings.TrimSpace(*managementIP), serverURL.String(), tokenPath, strings.TrimSpace(*k3sVersion)); err != nil {
		fatalf("install joined K3s server: %v", err)
	}
	if err := installLocalNodeManagerService(repoRoot); err != nil {
		fatalf("install local node-manager service: %v", err)
	}
	fmt.Printf("{\"admission_id\":%q,\"state\":\"joined\",\"node_name\":%q}\n", admissionID, strings.ToLower(strings.TrimSpace(*nodeName)))
}

func normalizePeerCIDRs(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 1024 {
		return "", errors.New("value must contain no more than 1024 characters")
	}
	parts := strings.Split(value, ",")
	if len(parts) > 16 {
		return "", errors.New("value must contain no more than 16 CIDRs")
	}
	normalized := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		ip, network, err := net.ParseCIDR(strings.TrimSpace(part))
		if err != nil || ip.To4() == nil || !privateIPv4Network(network) {
			return "", fmt.Errorf("%q is not a private IPv4 CIDR", strings.TrimSpace(part))
		}
		canonical := network.String()
		if !seen[canonical] {
			normalized = append(normalized, canonical)
			seen[canonical] = true
		}
	}
	return strings.Join(normalized, ","), nil
}

func privateIPv4Network(network *net.IPNet) bool {
	if network == nil || network.IP.To4() == nil || len(network.Mask) != net.IPv4len {
		return false
	}
	first := network.IP.To4()
	last := make(net.IP, net.IPv4len)
	for index := range first {
		last[index] = first[index] | ^network.Mask[index]
	}
	return first.IsPrivate() && last.IsPrivate()
}

func prepareJoinedNode(repoRoot, peerCIDRs string) error {
	if err := validateRepositoryRoot(repoRoot); err != nil {
		return err
	}
	enginePath := filepath.Join(repoRoot, "Engine.sh")
	info, err := os.Stat(enginePath)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("repository lacks regular Engine.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/bash", enginePath, "--cluster-prepare-node")
	command.Dir = repoRoot
	command.Env = environmentWithValue(os.Environ(), "BOREALIS_K3S_PEER_CIDRS", peerCIDRs)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("Engine node preparation failed: %w: %s", err, truncateDiagnostic(strings.TrimSpace(string(output)), 2048))
	}
	return nil
}

func environmentWithValue(environment []string, key, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(environment)+1)
	for _, item := range environment {
		if !strings.HasPrefix(item, prefix) {
			result = append(result, item)
		}
	}
	return append(result, prefix+value)
}

func clusterJoinHTTPClient(caFile string) (*http.Client, error) {
	pem, err := os.ReadFile(filepath.Clean(caFile))
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return nil, errors.New("CA file has no valid certificate")
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}, nil
}

func waitForClusterAdmission(client *http.Client, endpoint, bundle, admissionID string) error {
	deadline := time.Now().Add(14 * time.Minute)
	for time.Now().Before(deadline) {
		request, err := http.NewRequest(http.MethodGet, endpoint+"/api/bootstrap/cluster/join/"+admissionID+"/events", nil)
		if err != nil {
			return err
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("X-Borealis-Cluster-Invite", bundle)
		response, err := client.Do(request)
		if err == nil {
			raw, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				var payload struct {
					Events []map[string]any `json:"events"`
				}
				if json.Unmarshal(raw, &payload) == nil {
					for _, event := range payload.Events {
						if strings.TrimSpace(fmt.Sprint(event["event_type"])) == "admission_pair_approved" {
							return nil
						}
					}
				}
			} else if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
				return fmt.Errorf("admission authorization expired or was rejected: HTTP %d", response.StatusCode)
			}
		}
		time.Sleep(2 * time.Second)
	}
	return errors.New("admission approval did not arrive before invitation expiry")
}

func installJoinedK3sServer(nodeName, managementIP, serverURL, tokenFile, version string) error {
	if !hostHasIPv4(managementIP) {
		return errors.New("management IPv4 is not assigned to this host")
	}
	osRelease, err := os.ReadFile("/etc/os-release")
	if err != nil || !supportedUbuntuRelease(osRelease) {
		return errors.New("cluster nodes require Ubuntu 24.04 or newer")
	}
	configDirectory := "/etc/rancher/k3s/config.yaml.d"
	if err := os.MkdirAll(configDirectory, 0o700); err != nil {
		return err
	}
	controlVIP, _, err := net.SplitHostPort(strings.TrimPrefix(serverURL, "https://"))
	if err != nil || net.ParseIP(controlVIP) == nil {
		return errors.New("K3s server URL does not contain control-plane IPv4")
	}
	config := fmt.Sprintf("server: %q\ntoken-file: %q\nnode-name: %q\nnode-ip: %q\ntls-san:\n  - %q\ndisable:\n  - traefik\n  - servicelb\nsecrets-encryption: true\nembedded-registry: true\nnode-label:\n  - borealis.io/engine-node=true\n  - borealis.io/application-state=drained\n  - borealis.io/edge-eligible=false\n  - borealis.io/scheduler-eligible=false\n  - borealis.io/postgres-primary-eligible=false\n", serverURL, tokenFile, nodeName, managementIP, controlVIP)
	if err := os.WriteFile(filepath.Join(configDirectory, "20-borealis-cluster-join.yaml"), []byte(config), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(k3sRegistriesPath, k3sRegistryMirrorsConfig(), 0o644); err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodGet, "https://get.k3s.io", nil)
	if err != nil {
		return err
	}
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("K3s installer returned HTTP %d", response.StatusCode)
	}
	installer, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil || len(installer) == 0 {
		return errors.New("K3s installer download was empty or unreadable")
	}
	command := exec.Command("/usr/bin/bash", "-s", "--")
	command.Stdin = bytes.NewReader(installer)
	command.Env = append(os.Environ(), "INSTALL_K3S_VERSION="+version, "INSTALL_K3S_EXEC=server")
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("K3s install failed: %w: %s", err, truncateDiagnostic(strings.TrimSpace(string(output)), 2048))
	}
	for attempt := 0; attempt < 90; attempt++ {
		if _, err := run(context.Background(), "", "systemctl", "is-active", "--quiet", "k3s.service"); err == nil {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return errors.New("joined k3s.service did not become active")
}

func k3sRegistryMirrorsConfig() []byte {
	return []byte("# Borealis-managed Spegel registry sources. Engine.sh owns this file.\nmirrors:\n  docker.io:\n  ghcr.io:\n  quay.io:\n  registry.k8s.io:\n")
}

func supportedUbuntuRelease(osRelease []byte) bool {
	values := make(map[string]string)
	for _, line := range strings.Split(string(osRelease), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			continue
		}
		values[key] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	if values["ID"] != "ubuntu" {
		return false
	}
	majorText, _, _ := strings.Cut(values["VERSION_ID"], ".")
	major, err := strconv.Atoi(majorText)
	return err == nil && major >= 24
}

func hostHasIPv4(expected string) bool {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, address := range addresses {
		ip, _, err := net.ParseCIDR(address.String())
		if err == nil && ip.String() == expected {
			return true
		}
	}
	return false
}

func installLocalNodeManagerService(repoRoot string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		return err
	}
	if _, err := run(context.Background(), "", "groupadd", "--force", "--gid", fmt.Sprint(actionRuntimeID), "borealis-engine"); err != nil {
		return err
	}
	if err := replaceExecutable("/usr/local/sbin/borealis-node-manager", binary); err != nil {
		return err
	}
	if err := ensureSecureDirectory(filepath.Dir(defaultSecretPath)); err != nil {
		return err
	}
	if err := os.Chown(filepath.Dir(defaultSecretPath), 0, 0); err != nil {
		return err
	}
	if _, err := os.Stat(defaultSecretPath); errors.Is(err, os.ErrNotExist) {
		token := make([]byte, 32)
		if _, err := rand.Read(token); err != nil {
			return err
		}
		if err := os.WriteFile(defaultSecretPath, []byte(hex.EncodeToString(token)+"\n"), 0o640); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := os.Chown(defaultSecretPath, 0, actionRuntimeID); err != nil {
		return err
	}
	unitSource := filepath.Join(repoRoot, "Data", "Engine", "K3s", "cluster", "node-manager.service")
	unit, err := os.ReadFile(unitSource)
	if err != nil {
		return err
	}
	if err := os.WriteFile("/etc/systemd/system/borealis-node-manager.service", unit, 0o644); err != nil {
		return err
	}
	if _, err := run(context.Background(), "", "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if _, err := run(context.Background(), "", "systemctl", "enable", "borealis-node-manager.service"); err != nil {
		return err
	}
	_, err = run(context.Background(), "", "systemctl", "restart", "borealis-node-manager.service")
	return err
}

func replaceExecutable(destination string, content []byte) error {
	directory := filepath.Dir(destination)
	temporary, err := os.CreateTemp(directory, ".borealis-node-manager-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o750); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, destination)
}

func ensureSecureDirectory(path string) error {
	if err := os.MkdirAll(path, 0o750); err != nil {
		return err
	}
	return os.Chmod(path, 0o750)
}

func requiredRelease(params map[string]any) string {
	value := strings.TrimSpace(fmt.Sprint(params["release_tag"]))
	if !releasePattern.MatchString(value) {
		return ""
	}
	return value
}

func requiredSHA(params map[string]any) string {
	value := strings.ToLower(strings.TrimSpace(fmt.Sprint(params["target_sha"])))
	if !shaPattern.MatchString(value) {
		return ""
	}
	if _, err := hex.DecodeString(value); err != nil {
		return ""
	}
	return value
}

func requiredSchemaPhase(params map[string]any) string {
	value := strings.ToLower(strings.TrimSpace(fmt.Sprint(params["schema_phase"])))
	if value != "expand" && value != "finalize" {
		return ""
	}
	return value
}

func requiredK3sVersion(params map[string]any) string {
	value := strings.TrimSpace(fmt.Sprint(params["k3s_version"]))
	if !k3sPattern.MatchString(value) {
		return ""
	}
	return value
}

func requiredReason(params map[string]any) string {
	value := strings.TrimSpace(fmt.Sprint(params["reason"]))
	if value == "<nil>" {
		return ""
	}
	if len(value) > 256 || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	return value
}

func validateRepositoryRoot(root string) error {
	abs, err := filepath.Abs(root)
	if err != nil || abs == "/" || abs == "/opt" || abs == "/home" {
		return errors.New("unsafe repository root")
	}
	if _, err := os.Stat(filepath.Join(abs, ".git")); err != nil {
		return errors.New("repository root does not contain .git")
	}
	return nil
}

func normalizeRemote(value string) string {
	return strings.TrimSuffix(strings.TrimSpace(value), "/")
}

func run(ctx context.Context, workdir, binary string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, binary, args...)
	if workdir != "" {
		command.Dir = workdir
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%s failed: %w: %s", filepath.Base(binary), err, truncateDiagnostic(strings.TrimSpace(string(output)), 2048))
	}
	return string(output), nil
}

func runGit(ctx context.Context, workdir string, args ...string) (string, error) {
	abs, err := filepath.Abs(workdir)
	if err != nil || abs == "/" || strings.TrimSpace(workdir) == "" {
		return "", errors.New("unsafe Git worktree path")
	}
	gitArgs := append([]string{"-c", "safe.directory=" + abs}, args...)
	return run(ctx, abs, "git", gitArgs...)
}

func truncate(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	return value[:maximum]
}

func truncateDiagnostic(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	const marker = "\n... output truncated ...\n"
	if maximum <= len(marker) {
		return value[len(value)-maximum:]
	}
	contentLength := maximum - len(marker)
	headLength := contentLength / 2
	tailLength := contentLength - headLength
	return value[:headLength] + marker + value[len(value)-tailLength:]
}

func envDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func writeJSON(w http.ResponseWriter, status int, payload map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeResult(result map[string]any, err error) {
	if err != nil {
		fatalf("%v", err)
	}
	raw, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(raw))
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
