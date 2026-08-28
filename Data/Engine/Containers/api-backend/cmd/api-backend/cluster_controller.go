package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	clusterControllerLeaseName           = "cluster-operation-controller"
	clusterControllerLeaseTTL            = 20 * time.Second
	clusterControllerLeaseRenewInterval  = 5 * time.Second
	clusterControllerLeaseRenewalGrace   = clusterControllerLeaseTTL - clusterControllerLeaseRenewInterval
	clusterControllerLeaseAcquireTimeout = 5 * time.Second
	clusterNodeManagerSocket             = "/run/borealis/node-manager.sock"
	clusterNodeManagerToken              = "/etc/borealis/node-manager.token"
	clusterUpgradeNamespace              = "system-upgrade"
	clusterSharedArtifactPVCName         = "borealis-agent-artifacts"
	clusterLonghornNamespace             = "longhorn-system"
	clusterLonghornCSIDriver             = "driver.longhorn.io"
	clusterSharedArtifactReplicaCount    = int64(3)
	clusterK3sEtcdRemoveAnnotation       = "etcd.k3s.cattle.io/remove"
	clusterK3sEtcdRemovedNameAnnotation  = "etcd.k3s.cattle.io/removed-node-name"
	clusterK3sEtcdNodeNameAnnotation     = "etcd.k3s.cattle.io/node-name"
)

var (
	errClusterControllerLeaseLost    = errors.New("cluster controller operation lease lost")
	clusterControllerSHARegex        = regexp.MustCompile(`^[0-9a-f]{40}$`)
	clusterControllerNodeRegex       = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?$`)
	clusterControllerLabelValueRegex = regexp.MustCompile(`^(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?$`)
)

type clusterControllerOperation struct {
	ID            string
	Kind          string
	State         string
	CurrentStep   string
	TargetNodeID  string
	TargetRelease string
	TargetSHA     string
	Payload       map[string]any
	Attempt       int64
}

type clusterControllerNode struct {
	ID               string
	Name             string
	ApplicationState string
	ReleaseTag       string
	ReleaseSHA       string
	DrainReason      string
	Roles            map[string]any
	ProbeHealth      map[string]any
}

type clusterControllerState struct {
	ClusterID       string
	Enabled         bool
	Status          string
	ActiveSize      int64
	DesiredSize     int64
	ControlPlaneVIP string
	EdgeVIP         string
	BaselineRelease string
	BaselineSHA     string
	HMRState        string
	HMRNodeID       string
	ActiveOperation string
	Config          map[string]any
}

type clusterControllerAdmission struct {
	ID           string
	NodeName     string
	Hostname     string
	ManagementIP string
	Architecture string
	OSVersion    string
	State        string
	ApprovedBy   string
	ApprovedAt   int64
}

type clusterControllerOperationResource struct {
	Operation clusterControllerOperation
	ErrorText string
}

type clusterControllerStep struct {
	Name   string
	NodeID string
}

type clusterAdmissionNodeAction struct {
	stepName      string
	verb          string
	targetRelease string
	soakAfter     bool
}

func clusterAdmissionConformanceAction(nodeID, k3sVersion string) clusterAdmissionNodeAction {
	return clusterAdmissionNodeAction{stepName: "admit:" + nodeID + ":probe_conformance", verb: "RunK3sProbeConformance", targetRelease: k3sVersion}
}

func clusterAdmissionWorkloadActions(nodeID string) []clusterAdmissionNodeAction {
	return []clusterAdmissionNodeAction{
		{stepName: "admit:" + nodeID + ":redeploy", verb: "RedeployRevision"},
		{stepName: "admit:" + nodeID + ":candidate_health", verb: "InspectCandidateHealth", soakAfter: true},
		{stepName: "admit:" + nodeID + ":promote", verb: "PromoteCandidate"},
	}
}

type clusterControllerStepRunner interface {
	Run(context.Context, clusterControllerOperation, clusterControllerStep, []clusterControllerNode) error
}

type clusterController struct {
	store                      *postgresOperatorStore
	runner                     clusterControllerStepRunner
	holder                     string
	now                        func() time.Time
	leaseRenewInterval         time.Duration
	leaseAcquireTimeout        time.Duration
	lastPrune                  atomic.Int64
	lastCustomResourceSync     atomic.Int64
	lastRuntimeRoleObservation string
	lastCustomResourceError    string
	lastSharedArtifactError    string
}

type clusterControllerLeaseGuard struct {
	ctx             context.Context
	cancel          context.CancelCauseFunc
	heartbeatCancel context.CancelFunc
	done            chan struct{}
	mu              sync.Mutex
	err             error
}

func startClusterControllerLeaseGuard(parent context.Context, interval time.Duration, renew func(context.Context) (bool, error)) *clusterControllerLeaseGuard {
	return startClusterControllerLeaseGuardWithGrace(parent, interval, clusterControllerLeaseRenewalGrace, renew)
}

func startClusterControllerLeaseGuardWithGrace(parent context.Context, interval, renewalGrace time.Duration, renew func(context.Context) (bool, error)) *clusterControllerLeaseGuard {
	if interval <= 0 {
		interval = clusterControllerLeaseRenewInterval
	}
	if renewalGrace <= 0 {
		renewalGrace = clusterControllerLeaseRenewalGrace
	}
	guardCtx, cancel := context.WithCancelCause(parent)
	heartbeatCtx, heartbeatCancel := context.WithCancel(parent)
	guard := &clusterControllerLeaseGuard{
		ctx:             guardCtx,
		cancel:          cancel,
		heartbeatCancel: heartbeatCancel,
		done:            make(chan struct{}),
	}
	go func() {
		defer close(guard.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		lastRenewed := time.Now()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				owned, err := renew(heartbeatCtx)
				if heartbeatCtx.Err() != nil {
					return
				}
				if err != nil {
					if time.Since(lastRenewed) >= renewalGrace {
						guard.fail(fmt.Errorf("%w: renewal failed throughout grace period: %v", errClusterControllerLeaseLost, err))
						return
					}
					continue
				}
				if !owned {
					guard.fail(errClusterControllerLeaseLost)
					return
				}
				lastRenewed = time.Now()
			}
		}
	}()
	return guard
}

func (g *clusterControllerLeaseGuard) fail(err error) {
	g.mu.Lock()
	if g.err == nil {
		g.err = err
	}
	g.mu.Unlock()
	g.cancel(err)
}

func (g *clusterControllerLeaseGuard) Context() context.Context {
	return g.ctx
}

func (g *clusterControllerLeaseGuard) Err() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.err
}

func (g *clusterControllerLeaseGuard) Close() {
	g.heartbeatCancel()
	<-g.done
	g.cancel(context.Canceled)
}

type kubernetesClusterStepRunner struct {
	kube                          *kubernetesAPIClient
	db                            *sql.DB
	namespace                     string
	actionImage                   string
	soak                          time.Duration
	jobPollInterval               time.Duration
	clusterInitAuthorizationGrace time.Duration
	postgresReplicationProbe      func(context.Context, string, int64) (bool, error)
}

func clusterControllerMode() bool {
	if explicitHealthcheckArgMode() {
		return false
	}
	return processArgMatches("borealis-cluster-controller", "cluster-controller") || processRoleMatches("borealis-cluster-controller", "cluster-controller")
}

func runClusterController(ctx context.Context, cfg gatewayConfig) error {
	operatorStore, closeStore, err := openOperatorStore(cfg)
	if err != nil {
		return err
	}
	defer closeStore()
	store, ok := operatorStore.(*postgresOperatorStore)
	if !ok {
		return errors.New("cluster controller requires PostgreSQL operation store")
	}
	kube, err := newInClusterKubernetesAPIClient()
	if err != nil {
		return err
	}
	hostname, _ := os.Hostname()
	holder := firstText(strings.TrimSpace(os.Getenv("BOREALIS_CLUSTER_CONTROLLER_ID")), strings.TrimSpace(hostname)+"-"+newClusterUUID())
	runner := &kubernetesClusterStepRunner{
		kube:        kube,
		db:          store.db,
		namespace:   borealisOperatorNamespace(),
		actionImage: strings.TrimSpace(os.Getenv("BOREALIS_CLUSTER_ACTION_IMAGE")),
		soak:        envDurationSeconds("BOREALIS_CLUSTER_MIN_READY_SOAK_SECONDS", 30*time.Second),
	}
	controller := &clusterController{store: store, runner: runner, holder: holder, now: time.Now}
	healthServer := controller.healthServer()
	healthExited := make(chan error, 1)
	go func() {
		err := healthServer.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		healthExited <- err
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = healthServer.Shutdown(shutdownCtx)
	}()
	if !clusterControllerEligible() {
		log.Printf("borealis-cluster-controller running as isolated update candidate")
		select {
		case <-ctx.Done():
			return nil
		case err := <-healthExited:
			return err
		}
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	log.Printf("borealis-cluster-controller started holder=%s", holder)
	for {
		if err := controller.runOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("cluster controller reconcile failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case err := <-healthExited:
			return err
		case <-ticker.C:
		}
	}
}

func clusterControllerEligible() bool {
	return parseBoolEnvDefault("BOREALIS_CLUSTER_CONTROLLER_ELIGIBLE", true)
}

func (c *clusterController) healthServer() *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /startup", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /live", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, request *http.Request) {
		ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
		defer cancel()
		if err := c.store.db.PingContext(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "reason": "operation_store_unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	return &http.Server{Addr: net.JoinHostPort("0.0.0.0", envDefault("BOREALIS_CLUSTER_CONTROLLER_PORT", "8090")), Handler: mux, ReadHeaderTimeout: 5 * time.Second}
}

func (c *clusterController) runOnce(ctx context.Context) error {
	owned, err := c.acquireLease(ctx)
	if err != nil || !owned {
		return err
	}
	leaseGuard := startClusterControllerLeaseGuard(ctx, c.leaseRenewInterval, c.acquireLease)
	defer leaseGuard.Close()
	runCtx := leaseGuard.Context()
	if now := c.now().UTC().Unix(); now-c.lastPrune.Load() >= int64(time.Hour/time.Second) {
		if runner, ok := c.runner.(*kubernetesClusterStepRunner); ok {
			if pruneErr := runner.pruneDailyCNPGBackups(runCtx, 14); pruneErr != nil {
				log.Printf("cluster snapshot retention reconcile failed: %v", pruneErr)
			} else {
				c.lastPrune.Store(now)
			}
		}
	}
	if runner, ok := c.runner.(*kubernetesClusterStepRunner); ok {
		c.observeRuntimeRoles(runCtx, runner)
		c.observeCustomResources(runCtx, runner)
		c.observeSharedArtifactStorage(runCtx, runner)
		if recovered, recoveryErr := c.reconcileLostHMRNode(runCtx, runner); recoveryErr != nil || recovered {
			return recoveryErr
		}
	}
	operation, ok, err := c.claimOperation(runCtx)
	if err != nil || !ok {
		return err
	}
	if runner, ok := c.runner.(*kubernetesClusterStepRunner); ok {
		if err := runner.recordClusterIntent(runCtx, operation, clusterControllerStep{Name: operation.CurrentStep, NodeID: operation.TargetNodeID}); err != nil {
			// Cluster enablement installs CRDs during foundation step. Until then,
			// PostgreSQL remains authoritative and later reconciliation creates CR.
			if operation.Kind != "cluster_enable" {
				return err
			}
		}
	}
	nodes, err := c.activeNodes(runCtx)
	if err != nil {
		if cause := context.Cause(runCtx); cause != nil {
			return cause
		}
		return c.failOperation(runCtx, operation, err)
	}
	steps, err := clusterOperationSteps(operation, nodes)
	if err != nil {
		return c.failOperation(runCtx, operation, err)
	}
	stepIndex := -1
	for i := range steps {
		if steps[i].Name == operation.CurrentStep {
			stepIndex = i
			break
		}
	}
	if stepIndex < 0 {
		return c.failOperation(runCtx, operation, fmt.Errorf("recorded operation step %q is not valid for current operation state", operation.CurrentStep))
	}
	step := steps[stepIndex]
	stepCtx, cancel := context.WithTimeout(runCtx, clusterControllerStepTimeout(step.Name))
	err = c.runner.Run(stepCtx, operation, step, nodes)
	cancel()
	if leaseErr := leaseGuard.Err(); leaseErr != nil {
		return leaseErr
	}
	if cause := context.Cause(runCtx); cause != nil {
		return cause
	}
	if err != nil {
		return c.failOperation(runCtx, operation, fmt.Errorf("%s: %w", step.Name, err))
	}
	if stepIndex+1 == len(steps) {
		return c.completeOperation(runCtx, operation, nodes)
	}
	return c.advanceOperation(runCtx, operation, steps[stepIndex+1].Name)
}

func (c *clusterController) observeRuntimeRoles(ctx context.Context, runner *kubernetesClusterStepRunner) {
	err := c.reconcileRuntimeRoles(ctx, runner)
	if err == nil {
		if c.lastRuntimeRoleObservation != "" {
			log.Printf("cluster runtime role observation recovered")
			c.lastRuntimeRoleObservation = ""
		}
		return
	}
	message := err.Error()
	if message != c.lastRuntimeRoleObservation {
		log.Printf("cluster runtime role observation pending: %v", err)
		c.lastRuntimeRoleObservation = message
	}
}

func (c *clusterController) observeCustomResources(ctx context.Context, runner *kubernetesClusterStepRunner) {
	now := c.now().UTC().Unix()
	if now-c.lastCustomResourceSync.Load() < 10 {
		return
	}
	err := c.reconcileCustomResources(ctx, runner, now)
	if err == nil {
		c.lastCustomResourceSync.Store(now)
		if c.lastCustomResourceError != "" {
			log.Printf("cluster custom-resource reconciliation recovered")
			c.lastCustomResourceError = ""
		}
		return
	}
	message := err.Error()
	if message != c.lastCustomResourceError {
		log.Printf("cluster custom-resource reconciliation pending: %v", err)
		c.lastCustomResourceError = message
	}
}

func (c *clusterController) observeSharedArtifactStorage(ctx context.Context, runner *kubernetesClusterStepRunner) {
	nodes, err := c.activeNodes(ctx)
	if err == nil && len(nodes) == int(clusterSharedArtifactReplicaCount) {
		for _, node := range nodes {
			ready, readyErr := runner.nodeReady(ctx, node.Name)
			if readyErr != nil {
				err = readyErr
				break
			}
			if !ready {
				err = fmt.Errorf("full shared artifact replica reconciliation paused while %s is unavailable", node.Name)
				break
			}
		}
		if err == nil {
			err = runner.reconcileSharedArtifactStorage(ctx, nodes)
		}
	}
	if err == nil {
		if c.lastSharedArtifactError != "" {
			log.Printf("cluster shared artifact storage reconciliation recovered")
			c.lastSharedArtifactError = ""
		}
		return
	}
	message := err.Error()
	if message != c.lastSharedArtifactError {
		log.Printf("cluster shared artifact storage reconciliation pending: %v", err)
		c.lastSharedArtifactError = message
	}
}

func (c *clusterController) reconcileRuntimeRoles(ctx context.Context, runner *kubernetesClusterStepRunner) error {
	conn, err := c.store.db.Conn(ctx)
	if err != nil {
		return err
	}
	var enabled int
	var clusterStatus, configJSON, activeOperationID, hmrState string
	if err := conn.QueryRowContext(ctx, `SELECT enabled,status,config_json,COALESCE(active_operation_id,''),hmr_state FROM engine.cluster_state WHERE id=1`).Scan(&enabled, &clusterStatus, &configJSON, &activeOperationID, &hmrState); err != nil {
		conn.Close()
		return err
	}
	conn.Close()
	if enabled != 1 {
		return nil
	}
	owners, err := runner.runtimeRoleOwners(ctx, c.store.db, c.now().UTC().Unix())
	if err != nil {
		return err
	}
	nodes, err := c.activeNodes(ctx)
	if err != nil {
		return err
	}
	kubernetesNodes := make(map[string]map[string]any, len(nodes))
	for _, node := range nodes {
		var payload map[string]any
		if err := runner.kube.getJSON(ctx, "/api/v1/nodes/"+node.Name, &payload); err != nil {
			return err
		}
		kubernetesNodes[node.Name] = payload
	}
	databaseRuntime, err := runner.runtimeDatabaseState(ctx, int64(len(nodes)))
	if err != nil {
		return err
	}
	conn, err = c.store.db.Conn(ctx)
	if err != nil {
		return err
	}
	runtimeDrainObserved := false
	for index, node := range nodes {
		if node.ApplicationState == "drained" && node.DrainReason == "k3s_restart_label_drift" {
			runtimeDrainObserved = true
		}
		if updated, changed := clusterObservedDrainTransition(node, kubernetesNodes[node.Name], activeOperationID == "" && hmrState == "inactive"); changed {
			if _, err := conn.ExecContext(ctx, `UPDATE engine.cluster_nodes SET application_state='drained',drain_reason=$1,updated_at=$2 WHERE id=$3 AND membership_state='Active' AND application_state='active'`, updated.DrainReason, c.now().UTC().Unix(), node.ID); err != nil {
				conn.Close()
				return err
			}
			node = updated
			runtimeDrainObserved = true
		}
		if node.Roles == nil {
			node.Roles = map[string]any{}
		}
		before := marshalClusterJSON(node.Roles)
		for role, owner := range owners {
			node.Roles[role] = owner != "" && owner == node.Name
		}
		after := marshalClusterJSON(node.Roles)
		if before != after {
			if _, err := conn.ExecContext(ctx, `UPDATE engine.cluster_nodes SET roles_json=$1,updated_at=$2 WHERE id=$3`, after, c.now().UTC().Unix(), node.ID); err != nil {
				conn.Close()
				return err
			}
		}
		nodes[index] = node
	}
	config := parseClusterJSON(configJSON)
	beforeDatabaseRuntime := marshalClusterJSON(mapStringAny(config["database_runtime"]))
	nextStatus := clusterRuntimeDatabaseStatus(clusterStatus, databaseRuntime)
	if runtimeDrainObserved {
		nextStatus = "Degraded Quorum"
	}
	if beforeDatabaseRuntime != marshalClusterJSON(databaseRuntime) || nextStatus != clusterStatus {
		if _, err := conn.ExecContext(ctx, `UPDATE engine.cluster_state SET status=$1,config_json=jsonb_set(COALESCE(NULLIF(config_json,''),'{}')::jsonb,'{database_runtime}',$2::jsonb,true)::text,updated_at=$3 WHERE id=1`, nextStatus, marshalClusterJSON(databaseRuntime), c.now().UTC().Unix()); err != nil {
			conn.Close()
			return err
		}
	}
	if err := conn.Close(); err != nil {
		return err
	}
	for _, node := range nodes {
		if err := runner.reconcileNodeRuntime(ctx, node, kubernetesNodes[node.Name], c.now().UTC().Unix()); err != nil {
			return err
		}
	}
	return nil
}

func clusterObservedDrainTransition(node clusterControllerNode, kubernetesNode map[string]any, steadyState bool) (clusterControllerNode, bool) {
	observed := cleanText(nestedMap(nestedMap(kubernetesNode, "metadata"), "labels")["borealis.io/application-state"])
	if !steadyState || node.ApplicationState != "active" || observed != "drained" {
		return node, false
	}
	node.ApplicationState = "drained"
	node.DrainReason = "k3s_restart_label_drift"
	return node, true
}

func (r *kubernetesClusterStepRunner) runtimeDatabaseState(ctx context.Context, activeSize int64) (map[string]any, error) {
	path := fmt.Sprintf("/apis/postgresql.cnpg.io/v1/namespaces/%s/clusters/borealis-postgres", r.namespace)
	var cluster map[string]any
	if err := r.kube.getJSON(ctx, path, &cluster); err != nil {
		return nil, err
	}
	return clusterDatabaseRuntimeState(cluster, activeSize), nil
}

func clusterDatabaseRuntimeState(cluster map[string]any, activeSize int64) map[string]any {
	spec := nestedMap(cluster, "spec")
	status := nestedMap(cluster, "status")
	configured := coerceInt64(spec["instances"])
	if configured == 0 {
		configured = coerceInt64(status["instances"])
	}
	if configured == 0 {
		configured = activeSize
	}
	ready := coerceInt64(status["readyInstances"])
	requiredReady := int64(1)
	acknowledgements := int64(0)
	if configured > 1 {
		requiredReady = 2
		acknowledgements = 1
	}
	return map[string]any{
		"configured_instances":          configured,
		"ready_instances":               ready,
		"required_ready_for_durability": requiredReady,
		"synchronous_acknowledgements":  acknowledgements,
		"durability_quorum":             ready >= requiredReady,
		"fully_ready":                   configured > 0 && ready == configured,
		"phase":                         cleanText(status["phase"]),
		"primary_pod":                   cleanText(status["currentPrimary"]),
	}
}

func clusterRuntimeDatabaseStatus(current string, database map[string]any) string {
	if current != "Healthy" && current != "Degraded Database" {
		return current
	}
	fullyReady, _ := database["fully_ready"].(bool)
	durabilityQuorum, _ := database["durability_quorum"].(bool)
	if !fullyReady || !durabilityQuorum {
		return "Degraded Database"
	}
	return "Healthy"
}

func (c *clusterController) reconcileCustomResources(ctx context.Context, runner *kubernetesClusterStepRunner, observedAt int64) error {
	state, admissions, operations, err := c.customResourceSnapshot(ctx)
	if err != nil || !state.Enabled {
		return err
	}
	if err := runner.reconcileClusterResource(ctx, state, observedAt); err != nil {
		return err
	}
	for _, admission := range admissions {
		if err := runner.reconcileAdmissionResource(ctx, admission, observedAt); err != nil {
			return err
		}
	}
	for _, operation := range operations {
		if err := runner.reconcileOperationResource(ctx, operation, observedAt); err != nil {
			return err
		}
	}
	return nil
}

func (c *clusterController) customResourceSnapshot(ctx context.Context) (clusterControllerState, []clusterControllerAdmission, []clusterControllerOperationResource, error) {
	conn, err := c.store.db.Conn(ctx)
	if err != nil {
		return clusterControllerState{}, nil, nil, err
	}
	var state clusterControllerState
	var enabled int64
	var configJSON string
	err = conn.QueryRowContext(ctx, `
		SELECT cluster_id,enabled,status,active_size,desired_size,
		       COALESCE(control_plane_vip,''),COALESCE(edge_vip,''),
		       COALESCE(baseline_release,''),COALESCE(baseline_sha,''),
		       hmr_state,COALESCE(hmr_node_id,''),COALESCE(active_operation_id,''),config_json
		  FROM engine.cluster_state WHERE id=1
	`).Scan(
		&state.ClusterID,
		&enabled,
		&state.Status,
		&state.ActiveSize,
		&state.DesiredSize,
		&state.ControlPlaneVIP,
		&state.EdgeVIP,
		&state.BaselineRelease,
		&state.BaselineSHA,
		&state.HMRState,
		&state.HMRNodeID,
		&state.ActiveOperation,
		&configJSON,
	)
	if errors.Is(err, sql.ErrNoRows) {
		conn.Close()
		return clusterControllerState{}, nil, nil, nil
	}
	if err != nil {
		conn.Close()
		return clusterControllerState{}, nil, nil, err
	}
	state.Enabled = enabled == 1
	if !state.Enabled {
		conn.Close()
		return state, nil, nil, nil
	}

	admissionRows, err := conn.QueryContext(ctx, `
		SELECT id,node_name,hostname,management_ip,architecture,os_version,state,
		       COALESCE(approved_by,''),COALESCE(approved_at,0)
		  FROM engine.cluster_admissions ORDER BY updated_at DESC LIMIT 25
	`)
	if err != nil {
		conn.Close()
		return clusterControllerState{}, nil, nil, err
	}
	admissions := make([]clusterControllerAdmission, 0, 3)
	for admissionRows.Next() {
		var admission clusterControllerAdmission
		if err := admissionRows.Scan(
			&admission.ID,
			&admission.NodeName,
			&admission.Hostname,
			&admission.ManagementIP,
			&admission.Architecture,
			&admission.OSVersion,
			&admission.State,
			&admission.ApprovedBy,
			&admission.ApprovedAt,
		); err != nil {
			admissionRows.Close()
			conn.Close()
			return clusterControllerState{}, nil, nil, err
		}
		admissions = append(admissions, admission)
	}
	if err := admissionRows.Err(); err != nil {
		admissionRows.Close()
		conn.Close()
		return clusterControllerState{}, nil, nil, err
	}
	admissionRows.Close()

	operationRows, err := conn.QueryContext(ctx, `
		SELECT id,kind,state,current_step,COALESCE(target_node_id,''),
		       COALESCE(target_release,''),COALESCE(target_sha,''),COALESCE(error_text,''),attempt
		  FROM engine.cluster_operations ORDER BY updated_at DESC LIMIT 25
	`)
	if err != nil {
		conn.Close()
		return clusterControllerState{}, nil, nil, err
	}
	operations := make([]clusterControllerOperationResource, 0, 25)
	for operationRows.Next() {
		var resource clusterControllerOperationResource
		if err := operationRows.Scan(
			&resource.Operation.ID,
			&resource.Operation.Kind,
			&resource.Operation.State,
			&resource.Operation.CurrentStep,
			&resource.Operation.TargetNodeID,
			&resource.Operation.TargetRelease,
			&resource.Operation.TargetSHA,
			&resource.ErrorText,
			&resource.Operation.Attempt,
		); err != nil {
			operationRows.Close()
			conn.Close()
			return clusterControllerState{}, nil, nil, err
		}
		operations = append(operations, resource)
	}
	if err := operationRows.Err(); err != nil {
		operationRows.Close()
		conn.Close()
		return clusterControllerState{}, nil, nil, err
	}
	operationRows.Close()
	if err := conn.Close(); err != nil {
		return clusterControllerState{}, nil, nil, err
	}
	state.Config = parseClusterJSON(configJSON)
	return state, admissions, operations, nil
}

func (c *clusterController) reconcileLostHMRNode(ctx context.Context, runner *kubernetesClusterStepRunner) (bool, error) {
	conn, err := c.store.db.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	var state, targetID string
	if err := conn.QueryRowContext(ctx, `SELECT hmr_state,COALESCE(hmr_node_id,'') FROM engine.cluster_state WHERE id=1`).Scan(&state, &targetID); errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if state != "active" && state != "recovering" {
		return false, nil
	}
	var targetName string
	if err := conn.QueryRowContext(ctx, `SELECT node_name FROM engine.cluster_nodes WHERE id=$1 AND membership_state='Active'`, targetID).Scan(&targetName); err != nil {
		return false, err
	}
	if state == "active" {
		ready, err := runner.nodeReady(ctx, targetName)
		if err != nil {
			return false, err
		}
		if ready && runner.verifyReadyEndpointsForNode(ctx, targetName) != nil {
			ready = false
		}
		if ready {
			return false, nil
		}
		if _, err := conn.ExecContext(ctx, `UPDATE engine.cluster_state SET hmr_state='recovering',status='HMR Recovery',updated_at=$1 WHERE id=1 AND hmr_state='active'`, c.now().UTC().Unix()); err != nil {
			return false, err
		}
	}
	nodes, err := c.activeNodes(ctx)
	if err != nil {
		return true, err
	}
	standby := make([]clusterControllerNode, 0, len(nodes)-1)
	for _, node := range nodes {
		if node.ID != targetID {
			standby = append(standby, node)
		}
	}
	if len(standby) == 0 {
		return true, errors.New("HMR target is unavailable and no standby node can restore production")
	}
	if err := runner.patchNodeLabels(ctx, targetName, map[string]string{"borealis.io/application-state": "drained", "borealis.io/hmr-target": "false"}); err != nil {
		return true, err
	}
	if err := runner.setNodeRoleEligibility(ctx, targetName, false); err != nil {
		return true, err
	}
	roleNode := standby[0]
	if err := runner.ensurePostgresPrimaryOnNode(ctx, roleNode.Name); err != nil {
		return true, err
	}
	recovery := clusterControllerOperation{ID: newClusterUUID(), Kind: "hmr_recover", TargetNodeID: roleNode.ID}
	for _, node := range standby {
		if err := runner.patchNodeLabels(ctx, node.Name, map[string]string{
			"borealis.io/application-state": "drained",
			"borealis.io/hmr-target":        "false",
		}); err != nil {
			return true, err
		}
		step := clusterControllerStep{Name: "hmr-recovery:" + node.ID, NodeID: node.ID}
		if err := runner.nodeActionJob(ctx, recovery, clusterControllerStep{Name: step.Name + ":prepare", NodeID: node.ID}, node, "PrepareApplicationRestore"); err != nil {
			return true, err
		}
		if err := runner.nodeActionJob(ctx, recovery, clusterControllerStep{Name: step.Name + ":health", NodeID: node.ID}, node, "InspectHealth"); err != nil {
			return true, err
		}
		if err := runner.minimumReadySoak(ctx, node.Name); err != nil {
			return true, err
		}
		if err := runner.setNodeRoleEligibility(ctx, node.Name, true); err != nil {
			return true, err
		}
		if err := runner.nodeActionJob(ctx, recovery, clusterControllerStep{Name: step.Name + ":role-health", NodeID: node.ID}, node, "InspectHealth"); err != nil {
			return true, err
		}
		if err := runner.minimumReadySoak(ctx, node.Name); err != nil {
			return true, err
		}
		if err := runner.nodeActionJob(ctx, recovery, clusterControllerStep{Name: step.Name + ":activate", NodeID: node.ID}, node, "ExitApplicationDrain"); err != nil {
			return true, err
		}
	}
	target := clusterNodeByID(nodes, targetID)
	if target.ID == "" {
		return true, errors.New("lost HMR target disappeared from active membership")
	}
	targetReady, err := runner.nodeReady(ctx, targetName)
	if err != nil {
		return true, err
	}
	if targetReady {
		targetRecovery := recovery
		targetRecovery.TargetNodeID = targetID
		targetRecovery.Payload = map[string]any{"reason": "hmr_target_lost"}
		step := clusterControllerStep{Name: "hmr-recovery:" + targetID + ":fence-rejoined-target", NodeID: targetID}
		if err := runner.nodeActionJob(ctx, targetRecovery, step, target, "EnterApplicationDrain"); err != nil {
			return true, err
		}
	}
	if err := runner.setNodeRoleEligibility(ctx, targetName, false); err != nil {
		return true, err
	}
	if targetReady {
		if err := runner.waitNodeEndpointsWithdrawn(ctx, targetName); err != nil {
			return true, err
		}
	}
	if err := runner.waitEdgeAndWireGuardOwnerAwayFrom(ctx, targetName); err != nil {
		return true, err
	}
	now := c.now().UTC().Unix()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return true, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_nodes SET application_state=CASE WHEN id=$1 THEN 'drained' ELSE 'active' END,drain_reason=CASE WHEN id=$1 THEN 'hmr_target_lost' ELSE NULL END,updated_at=$2 WHERE membership_state='Active'`, targetID, now); err != nil {
		return true, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_state SET hmr_state='inactive',hmr_node_id=NULL,status='Degraded Quorum',updated_at=$1 WHERE id=1`, now); err != nil {
		return true, err
	}
	if err := insertClusterEvent(ctx, tx, "", "", "", "hmr_target_lost_recovered", "succeeded", "Pinned production workloads restored on standby nodes after HMR target loss.", map[string]any{"lost_node_id": targetID, "role_node_id": roleNode.ID}, now); err != nil {
		return true, err
	}
	if err := insertClusterAudit(ctx, tx, "borealis-cluster-controller", "hmr_target_lost_recovery", targetID, "succeeded", map[string]any{"role_node_id": roleNode.ID}, now); err != nil {
		return true, err
	}
	return true, tx.Commit()
}

func clusterControllerStepTimeout(step string) time.Duration {
	switch {
	case strings.HasSuffix(step, ":stage_revision_images"), strings.HasSuffix(step, ":redeploy_revision"), strings.HasSuffix(step, ":promote_candidate"):
		return 65 * time.Minute
	case strings.HasSuffix(step, ":prepare_restore"):
		return 15 * time.Minute
	case step == "hmr_restore_pinned_release", step == "hmr_promote_candidate":
		return 65 * time.Minute
	case strings.HasSuffix(step, ":fetch_release"), strings.HasSuffix(step, ":apply_k3s_upgrade"):
		return 35 * time.Minute
	case step == "apply_cluster_foundation":
		return 95 * time.Minute
	case step == "apply_membership":
		// Pair admission runs conformance, cold image builds, candidate probes,
		// promotion, and two ready soaks on each joining node in sequence.
		return 3 * time.Hour
	case step == "pre_change_snapshot", step == "migrate_postgres", step == "expand_schema", step == "finalize_schema", step == "prepare_postgres_removal", step == "scale_postgres_membership":
		return 20 * time.Minute
	default:
		return 5 * time.Minute
	}
}

func clusterOperationSteps(operation clusterControllerOperation, nodes []clusterControllerNode) ([]clusterControllerStep, error) {
	base := []clusterControllerStep{{Name: "preflight"}}
	switch operation.Kind {
	case "engine_update":
		ordered, err := clusterUpdateNodes(operation, nodes)
		if err != nil {
			return nil, err
		}
		migration := cleanText(clusterCompatibilityMap(operation.Payload["compatibility"])["database_migration"])
		if !textInSet(migration, "none", "expand-contract") {
			return nil, errors.New("Engine update lacks supported database migration contract")
		}
		base = append(base, clusterControllerStep{Name: "pre_change_snapshot"})
		for index, node := range ordered {
			for _, action := range []string{"transfer_roles", "enter_drain", "wait_endpoint_withdrawal", "fetch_release", "stage_revision_images", "redeploy_revision", "inspect_candidate_health", "minimum_candidate_soak", "promote_candidate", "inspect_health", "minimum_ready_soak", "restore_roles", "inspect_role_health", "minimum_role_soak", "exit_drain"} {
				base = append(base, clusterControllerStep{Name: "node:" + node.ID + ":" + action, NodeID: node.ID})
				if migration == "expand-contract" && index == 0 && action == "stage_revision_images" {
					base = append(base, clusterControllerStep{Name: "expand_schema", NodeID: node.ID})
				}
			}
		}
		if migration == "expand-contract" {
			base = append(base, clusterControllerStep{Name: "finalize_schema", NodeID: ordered[0].ID})
		}
		return append(base, clusterControllerStep{Name: "verify_cluster"}), nil
	case "k3s_update":
		ordered, err := clusterUpdateNodes(operation, nodes)
		if err != nil {
			return nil, err
		}
		base = append(base, clusterControllerStep{Name: "pre_change_snapshot"})
		for _, node := range ordered {
			for _, action := range []string{"transfer_roles", "enter_drain", "wait_endpoint_withdrawal", "apply_k3s_upgrade", "wait_k3s_ready", "post_k3s_conformance", "prepare_restore", "inspect_health", "minimum_ready_soak", "restore_roles", "inspect_role_health", "minimum_role_soak", "exit_drain"} {
				base = append(base, clusterControllerStep{Name: "node:" + node.ID + ":" + action, NodeID: node.ID})
			}
		}
		return append(base, clusterControllerStep{Name: "verify_cluster"}), nil
	case "hmr_start":
		if clusterNodeByID(nodes, operation.TargetNodeID).ID == "" {
			return nil, errors.New("HMR target is not an active cluster node")
		}
		return append(base,
			clusterControllerStep{Name: "pre_change_snapshot"},
			clusterControllerStep{Name: "hmr_stage_pinned_release", NodeID: operation.TargetNodeID},
			clusterControllerStep{Name: "hmr_move_roles", NodeID: operation.TargetNodeID},
			clusterControllerStep{Name: "hmr_drain_standby", NodeID: operation.TargetNodeID},
			clusterControllerStep{Name: "hmr_activate_target", NodeID: operation.TargetNodeID},
			clusterControllerStep{Name: "hmr_verify_target", NodeID: operation.TargetNodeID},
		), nil
	case "hmr_exit":
		targetID := operation.TargetNodeID
		if targetID == "" {
			targetID = cleanText(operation.Payload["hmr_node_id"])
		}
		if clusterNodeByID(nodes, targetID).ID == "" {
			return nil, errors.New("saved HMR target is not an active cluster node")
		}
		steps := append([]clusterControllerStep{}, base...)
		standbyID := ""
		for _, node := range nodes {
			if node.ID != targetID {
				if standbyID == "" {
					standbyID = node.ID
				}
				steps = append(steps,
					clusterControllerStep{Name: "node:" + node.ID + ":prepare_restore", NodeID: node.ID},
					clusterControllerStep{Name: "node:" + node.ID + ":inspect_health", NodeID: node.ID},
					clusterControllerStep{Name: "node:" + node.ID + ":minimum_ready_soak", NodeID: node.ID},
					clusterControllerStep{Name: "node:" + node.ID + ":restore_roles", NodeID: node.ID},
					clusterControllerStep{Name: "node:" + node.ID + ":inspect_role_health", NodeID: node.ID},
					clusterControllerStep{Name: "node:" + node.ID + ":minimum_role_soak", NodeID: node.ID},
					clusterControllerStep{Name: "node:" + node.ID + ":exit_drain", NodeID: node.ID},
				)
			}
		}
		if standbyID != "" {
			steps = append(steps, clusterControllerStep{Name: "hmr_move_roles_to_standby", NodeID: standbyID})
		} else {
			steps = append(steps, clusterControllerStep{Name: "hmr_fence_target_roles", NodeID: targetID})
		}
		for _, action := range []string{"enter_drain", "wait_endpoint_withdrawal"} {
			steps = append(steps, clusterControllerStep{Name: "node:" + targetID + ":" + action, NodeID: targetID})
		}
		steps = append(steps,
			clusterControllerStep{Name: "hmr_restore_pinned_release", NodeID: targetID},
			clusterControllerStep{Name: "hmr_inspect_candidate", NodeID: targetID},
			clusterControllerStep{Name: "hmr_candidate_soak", NodeID: targetID},
			clusterControllerStep{Name: "hmr_promote_candidate", NodeID: targetID},
			clusterControllerStep{Name: "hmr_verify_production", NodeID: targetID},
			clusterControllerStep{Name: "node:" + targetID + ":restore_roles", NodeID: targetID},
			clusterControllerStep{Name: "node:" + targetID + ":inspect_role_health", NodeID: targetID},
			clusterControllerStep{Name: "node:" + targetID + ":minimum_role_soak", NodeID: targetID},
			clusterControllerStep{Name: "node:" + targetID + ":exit_drain", NodeID: targetID},
			clusterControllerStep{Name: "verify_cluster"},
		)
		return steps, nil
	case "node_maintenance":
		action := cleanText(operation.Payload["action"])
		if action == "enter" {
			return append(base, clusterControllerStep{Name: "node:" + operation.TargetNodeID + ":transfer_roles", NodeID: operation.TargetNodeID}, clusterControllerStep{Name: "node:" + operation.TargetNodeID + ":enter_drain", NodeID: operation.TargetNodeID}, clusterControllerStep{Name: "node:" + operation.TargetNodeID + ":wait_endpoint_withdrawal", NodeID: operation.TargetNodeID}), nil
		}
		return append(base,
			clusterControllerStep{Name: "node:" + operation.TargetNodeID + ":prepare_restore", NodeID: operation.TargetNodeID},
			clusterControllerStep{Name: "node:" + operation.TargetNodeID + ":inspect_health", NodeID: operation.TargetNodeID},
			clusterControllerStep{Name: "node:" + operation.TargetNodeID + ":minimum_ready_soak", NodeID: operation.TargetNodeID},
			clusterControllerStep{Name: "node:" + operation.TargetNodeID + ":restore_roles", NodeID: operation.TargetNodeID},
			clusterControllerStep{Name: "node:" + operation.TargetNodeID + ":inspect_role_health", NodeID: operation.TargetNodeID},
			clusterControllerStep{Name: "node:" + operation.TargetNodeID + ":minimum_role_soak", NodeID: operation.TargetNodeID},
			clusterControllerStep{Name: "node:" + operation.TargetNodeID + ":exit_drain", NodeID: operation.TargetNodeID},
		), nil
	case "node_remove":
		ordered, err := clusterRemovalNodes(operation, nodes)
		if err != nil {
			return nil, err
		}
		if emergency, _ := operation.Payload["emergency"].(bool); emergency {
			for _, node := range ordered {
				base = append(base,
					clusterControllerStep{Name: "node:" + node.ID + ":delete_member_node", NodeID: node.ID},
					clusterControllerStep{Name: "node:" + node.ID + ":verify_member_removed", NodeID: node.ID},
				)
			}
			return append(base, clusterControllerStep{Name: "scale_postgres_membership"}, clusterControllerStep{Name: "verify_quorum"}), nil
		}
		base = append(base, clusterControllerStep{Name: "pre_change_snapshot"}, clusterControllerStep{Name: "prepare_postgres_removal"})
		for _, node := range ordered {
			for _, action := range []string{"transfer_roles", "enter_drain", "wait_endpoint_withdrawal", "prepare_member_removal", "remove_etcd_membership", "wait_member_fenced", "delete_member_node", "verify_member_removed"} {
				base = append(base, clusterControllerStep{Name: "node:" + node.ID + ":" + action, NodeID: node.ID})
			}
		}
		return append(base, clusterControllerStep{Name: "scale_shared_artifact_membership"}, clusterControllerStep{Name: "verify_quorum"}), nil
	case "membership_admit", "membership_scale":
		return append(base, clusterControllerStep{Name: "apply_membership"}, clusterControllerStep{Name: "verify_quorum"}), nil
	case "postgres_switchover":
		return append(base, clusterControllerStep{Name: "pre_change_snapshot"}, clusterControllerStep{Name: "postgres_role_change", NodeID: operation.TargetNodeID}, clusterControllerStep{Name: "verify_postgres"}), nil
	case "postgres_emergency_failover":
		// Emergency failover must remain usable when failed primary cannot
		// complete new backup. Existing scheduled/pre-change backups remain
		// recovery source; promotion starts after normal preflight checks.
		return append(base, clusterControllerStep{Name: "postgres_role_change", NodeID: operation.TargetNodeID}, clusterControllerStep{Name: "verify_postgres"}), nil
	case "cluster_enable":
		return append(base, clusterControllerStep{Name: "apply_cluster_foundation"}, clusterControllerStep{Name: "migrate_postgres"}, clusterControllerStep{Name: "verify_cluster"}), nil
	default:
		return nil, fmt.Errorf("unsupported cluster operation kind %q", operation.Kind)
	}
}

func clusterUpdateNodes(operation clusterControllerOperation, nodes []clusterControllerNode) ([]clusterControllerNode, error) {
	if pinned := anySlice(operation.Payload["update_node_ids"]); len(pinned) > 0 {
		expected := len(nodes)
		if cleanText(operation.Payload["scope"]) == "node" {
			expected = 1
		}
		if len(pinned) != expected {
			return nil, errors.New("pinned update order does not match operation scope")
		}
		selected := make([]clusterControllerNode, 0, len(pinned))
		seen := make(map[string]bool, len(pinned))
		for _, rawID := range pinned {
			id := cleanText(rawID)
			if seen[id] {
				return nil, errors.New("pinned update order contains duplicate node")
			}
			seen[id] = true
			node := clusterNodeByID(nodes, id)
			if node.ID == "" {
				return nil, fmt.Errorf("pinned update node %s is not active", id)
			}
			selected = append(selected, node)
		}
		return selected, nil
	}
	selected := append([]clusterControllerNode(nil), nodes...)
	if cleanText(operation.Payload["scope"]) == "node" {
		selected = nil
		for _, raw := range anySlice(operation.Payload["node_ids"]) {
			node := clusterNodeByID(nodes, cleanText(raw))
			if node.ID == "" {
				return nil, fmt.Errorf("selected update node %s is not active", cleanText(raw))
			}
			selected = append(selected, node)
		}
	}
	if len(selected) == 0 {
		return nil, errors.New("update has no active target nodes")
	}
	sort.SliceStable(selected, func(i, j int) bool {
		left := clusterNodeLeadershipWeight(selected[i])
		right := clusterNodeLeadershipWeight(selected[j])
		if left == right {
			return selected[i].Name < selected[j].Name
		}
		return left < right
	})
	return selected, nil
}

func clusterRemovalNodes(operation clusterControllerOperation, nodes []clusterControllerNode) ([]clusterControllerNode, error) {
	ids := anySlice(operation.Payload["removal_node_ids"])
	if len(ids) == 0 {
		for _, id := range clusterRemovalNodeIDs(operation.TargetNodeID, operation.Payload) {
			ids = append(ids, id)
		}
	}
	emergency, _ := operation.Payload["emergency"].(bool)
	expected := 2
	if emergency {
		expected = 1
	}
	if len(ids) != expected {
		return nil, fmt.Errorf("node removal requires %d target(s)", expected)
	}
	selected := make([]clusterControllerNode, 0, expected)
	seen := map[string]bool{}
	for _, rawID := range ids {
		id := cleanText(rawID)
		if seen[id] {
			return nil, errors.New("node removal targets must be distinct")
		}
		seen[id] = true
		node := clusterNodeByID(nodes, id)
		if node.ID == "" {
			return nil, fmt.Errorf("removal target %s is not active", id)
		}
		selected = append(selected, node)
	}
	sort.SliceStable(selected, func(i, j int) bool {
		left := clusterNodeLeadershipWeight(selected[i])
		right := clusterNodeLeadershipWeight(selected[j])
		if left == right {
			return selected[i].Name < selected[j].Name
		}
		return left < right
	})
	return selected, nil
}

func clusterNodeLeadershipWeight(node clusterControllerNode) int {
	weight := 0
	for _, key := range []string{"etcd_leader", "control_vip_owner", "edge_vip_owner", "postgres_primary", "scheduler_leader", "wireguard_owner"} {
		if value, _ := node.Roles[key].(bool); value {
			weight++
		}
	}
	return weight
}

func clusterNodeByID(nodes []clusterControllerNode, id string) clusterControllerNode {
	for _, node := range nodes {
		if node.ID == id {
			return node
		}
	}
	return clusterControllerNode{}
}

func clusterAllNodesAtRevision(nodes []clusterControllerNode, revision string) bool {
	if len(nodes) == 0 || !clusterControllerSHARegex.MatchString(revision) {
		return false
	}
	for _, node := range nodes {
		if node.ReleaseSHA != revision {
			return false
		}
	}
	return true
}

func (c *clusterController) acquireLease(ctx context.Context) (bool, error) {
	timeout := c.leaseAcquireTimeout
	if timeout <= 0 {
		timeout = clusterControllerLeaseAcquireTimeout
	}
	acquireCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	now := c.now().UTC().Unix()
	expires := now + int64(clusterControllerLeaseTTL/time.Second)
	conn, err := c.store.db.Conn(acquireCtx)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	result, err := conn.ExecContext(acquireCtx, `
		INSERT INTO engine.cluster_application_leases(name, holder, expires_at, updated_at)
		VALUES($1,$2,$3,$4)
		ON CONFLICT(name) DO UPDATE
		SET holder=EXCLUDED.holder, expires_at=EXCLUDED.expires_at, updated_at=EXCLUDED.updated_at
		WHERE engine.cluster_application_leases.holder=EXCLUDED.holder
		   OR engine.cluster_application_leases.expires_at < EXCLUDED.updated_at
	`, clusterControllerLeaseName, c.holder, expires, now)
	if err != nil {
		if acquireCtx.Err() != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (c *clusterController) requireLeaseOwnership(ctx context.Context, tx *sql.Tx) error {
	var holder string
	var expiresAt int64
	err := tx.QueryRowContext(ctx, `
		SELECT holder, expires_at
		  FROM engine.cluster_application_leases
		 WHERE name=$1
		 FOR UPDATE
	`, clusterControllerLeaseName).Scan(&holder, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return errClusterControllerLeaseLost
	}
	if err != nil {
		return err
	}
	now := c.now().UTC().Unix()
	if holder != c.holder || expiresAt < now {
		return fmt.Errorf("%w: holder=%q expires_at=%d", errClusterControllerLeaseLost, holder, expiresAt)
	}
	return nil
}

func (c *clusterController) claimOperation(ctx context.Context) (clusterControllerOperation, bool, error) {
	conn, err := c.store.db.Conn(ctx)
	if err != nil {
		return clusterControllerOperation{}, false, err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return clusterControllerOperation{}, false, err
	}
	defer tx.Rollback()
	if err := c.requireLeaseOwnership(ctx, tx); err != nil {
		return clusterControllerOperation{}, false, err
	}
	var operation clusterControllerOperation
	var targetNodeID, targetRelease, targetSHA, payloadJSON string
	err = tx.QueryRowContext(ctx, `
		SELECT o.id, o.kind, o.state, o.current_step,
		       COALESCE(o.target_node_id,''), COALESCE(o.target_release,''), COALESCE(o.target_sha,''), o.payload_json, o.attempt
		  FROM engine.cluster_operations o
		  JOIN engine.cluster_state c ON c.active_operation_id=o.id
		 WHERE o.state IN ('queued','running','waiting')
		 FOR UPDATE OF o SKIP LOCKED
	`).Scan(&operation.ID, &operation.Kind, &operation.State, &operation.CurrentStep, &targetNodeID, &targetRelease, &targetSHA, &payloadJSON, &operation.Attempt)
	if errors.Is(err, sql.ErrNoRows) {
		return clusterControllerOperation{}, false, nil
	}
	if err != nil {
		return clusterControllerOperation{}, false, err
	}
	operation.TargetNodeID = targetNodeID
	operation.TargetRelease = targetRelease
	operation.TargetSHA = targetSHA
	operation.Payload = parseClusterJSON(payloadJSON)
	if operation.Kind == "hmr_exit" && operation.TargetNodeID == "" {
		_ = tx.QueryRowContext(ctx, `SELECT COALESCE(hmr_node_id,'') FROM engine.cluster_state WHERE id=1`).Scan(&operation.TargetNodeID)
		operation.Payload["hmr_node_id"] = operation.TargetNodeID
	}
	if operation.State == "queued" || operation.State == "waiting" {
		now := c.now().UTC().Unix()
		if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_operations SET state='running', started_at=COALESCE(started_at,$1), updated_at=$1 WHERE id=$2`, now, operation.ID); err != nil {
			return clusterControllerOperation{}, false, err
		}
		if err := insertClusterEvent(ctx, tx, operation.ID, "", "", "operation_started", "running", "Cluster controller acquired operation.", map[string]any{"holder": c.holder}, now); err != nil {
			return clusterControllerOperation{}, false, err
		}
		operation.State = "running"
	}
	if err := tx.Commit(); err != nil {
		return clusterControllerOperation{}, false, err
	}
	return operation, true, nil
}

func (c *clusterController) activeNodes(ctx context.Context) ([]clusterControllerNode, error) {
	conn, err := c.store.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, `
		SELECT id, node_name, application_state, COALESCE(release_tag,''), COALESCE(release_sha,''), COALESCE(drain_reason,''), roles_json, probe_health_json
		  FROM engine.cluster_nodes WHERE membership_state='Active' ORDER BY node_name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := make([]clusterControllerNode, 0, 3)
	for rows.Next() {
		var node clusterControllerNode
		var rolesJSON string
		var probesJSON string
		if err := rows.Scan(&node.ID, &node.Name, &node.ApplicationState, &node.ReleaseTag, &node.ReleaseSHA, &node.DrainReason, &rolesJSON, &probesJSON); err != nil {
			return nil, err
		}
		node.Roles = parseClusterJSON(rolesJSON)
		node.ProbeHealth = parseClusterJSON(probesJSON)
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

func (c *clusterController) advanceOperation(ctx context.Context, operation clusterControllerOperation, nextStep string) error {
	conn, err := c.store.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := c.requireLeaseOwnership(ctx, tx); err != nil {
		return err
	}
	now := c.now().UTC().Unix()
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_operations SET current_step=$1, updated_at=$2 WHERE id=$3 AND state='running'`, nextStep, now, operation.ID); err != nil {
		return err
	}
	if nodeID, state, reason, ok := clusterOperationNodeStateTransition(operation); ok {
		if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_nodes SET application_state=$1,drain_reason=$2,updated_at=$3 WHERE id=$4 AND membership_state='Active'`, state, nullClusterString(reason), now, nodeID); err != nil {
			return err
		}
	}
	if operation.Kind == "engine_update" && strings.HasSuffix(operation.CurrentStep, ":inspect_health") && operation.TargetRelease != "" {
		nodeID := clusterStepNodeID(operation.CurrentStep)
		if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_nodes SET release_tag=$1, release_sha=$2, probe_health_json=$3, last_seen_at=$4, updated_at=$4 WHERE id=$5`, operation.TargetRelease, operation.TargetSHA, `{"startup":"passed","readiness":"passed","liveness":"passed","direct_endpoint":"passed","service":"passed","database":"passed","scheduler":"passed","agent_path":"passed","wireguard":"passed"}`, now, nodeID); err != nil {
			return err
		}
	}
	if operation.Kind == "k3s_update" && strings.HasSuffix(operation.CurrentStep, ":post_k3s_conformance") {
		nodeID := clusterStepNodeID(operation.CurrentStep)
		if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_nodes SET roles_json=jsonb_set(COALESCE(NULLIF(roles_json,''),'{}')::jsonb,'{k3s_version}',to_jsonb($1::text),true)::text,last_seen_at=$2,updated_at=$2 WHERE id=$3`, operation.TargetRelease, now, nodeID); err != nil {
			return err
		}
	}
	if err := insertClusterEvent(ctx, tx, operation.ID, "", "", "operation_step_passed", "running", "Cluster operation step passed.", map[string]any{"step": operation.CurrentStep, "next_step": nextStep}, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (c *clusterController) failOperation(ctx context.Context, operation clusterControllerOperation, cause error) error {
	conn, err := c.store.db.Conn(ctx)
	if err != nil {
		return errors.Join(cause, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return errors.Join(cause, err)
	}
	defer tx.Rollback()
	if err := c.requireLeaseOwnership(ctx, tx); err != nil {
		return errors.Join(cause, err)
	}
	now := c.now().UTC().Unix()
	message := truncateClusterError(cause.Error(), 2048)
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_operations SET state='failed', error_text=$1, finished_at=$2, updated_at=$2 WHERE id=$3`, message, now, operation.ID); err != nil {
		return errors.Join(cause, err)
	}
	// EnterApplicationDrain labels node before scaling workloads down. Persist
	// conservative drained state even when action fails after label change.
	if nodeID, state, reason, ok := clusterOperationNodeStateTransition(operation); ok && state == "drained" {
		if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_nodes SET application_state=$1,drain_reason=$2,updated_at=$3 WHERE id=$4 AND membership_state='Active'`, state, nullClusterString(reason), now, nodeID); err != nil {
			return errors.Join(cause, err)
		}
	}
	isHMR := operation.Kind == "hmr_start" || operation.Kind == "hmr_exit"
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_state SET status='Degraded Quorum',hmr_state=CASE WHEN $3 THEN 'restore_failed' ELSE hmr_state END,active_operation_id=NULL,updated_at=$1 WHERE id=1 AND active_operation_id=$2`, now, operation.ID, isHMR); err != nil {
		return errors.Join(cause, err)
	}
	if err := insertClusterEvent(ctx, tx, operation.ID, "", "", "operation_failed", "failed", "Operation halted; affected node remains drained. Retry or recover explicitly.", map[string]any{"step": operation.CurrentStep, "error": message}, now); err != nil {
		return errors.Join(cause, err)
	}
	if err := tx.Commit(); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func (c *clusterController) completeOperation(ctx context.Context, operation clusterControllerOperation, nodes []clusterControllerNode) error {
	conn, err := c.store.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := c.requireLeaseOwnership(ctx, tx); err != nil {
		return err
	}
	now := c.now().UTC().Unix()
	var clusterStatus, configJSON string
	var activeSize, desiredSize int64
	if err := tx.QueryRowContext(ctx, `SELECT status,active_size,desired_size,config_json FROM engine.cluster_state WHERE id=1 FOR UPDATE`).Scan(&clusterStatus, &activeSize, &desiredSize, &configJSON); err != nil {
		return err
	}
	switch operation.Kind {
	case "cluster_enable":
		nodeName := cleanText(operation.Payload["node_name"])
		managementIP := cleanText(operation.Payload["management_ip"])
		architecture := cleanText(operation.Payload["architecture"])
		nodeID := newClusterUUID()
		rolesJSON := marshalClusterJSON(map[string]any{"etcd_leader": true, "control_vip_owner": true, "edge_vip_owner": true, "postgres_primary": true, "scheduler_leader": true, "wireguard_owner": true, "k3s_version": cleanText(operation.Payload["k3s_version"])})
		_, err = tx.ExecContext(ctx, `
			INSERT INTO engine.cluster_nodes(id,node_name,hostname,management_ip,architecture,os_version,membership_state,application_state,release_tag,release_sha,roles_json,probe_health_json,last_seen_at,created_at,updated_at)
			VALUES($1,$2,$2,$3,$4,'Ubuntu 24.04','Active','active',$5,$6,$7,$8,$9,$9,$9)
			ON CONFLICT(node_name) DO UPDATE SET membership_state='Active', application_state='active',management_ip=EXCLUDED.management_ip,architecture=EXCLUDED.architecture,release_tag=EXCLUDED.release_tag,release_sha=EXCLUDED.release_sha,roles_json=EXCLUDED.roles_json,probe_health_json=EXCLUDED.probe_health_json,last_seen_at=EXCLUDED.last_seen_at,updated_at=EXCLUDED.updated_at
		`, nodeID, nodeName, managementIP, architecture, cleanText(operation.Payload["baseline_release"]), cleanText(operation.Payload["baseline_sha"]), rolesJSON, `{"startup":"passed","readiness":"passed","liveness":"passed","direct_endpoint":"passed","service":"passed","database":"passed","scheduler":"passed","agent_path":"passed","wireguard":"passed"}`, now)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE engine.cluster_state SET enabled=1,status='Healthy',active_size=1,desired_size=1,baseline_release=$1,baseline_sha=$2,hmr_state='inactive',config_json=jsonb_set(COALESCE(NULLIF(config_json,''),'{}')::jsonb,'{k3s_version}',to_jsonb($3::text),true)::text,updated_at=$4 WHERE id=1`, cleanText(operation.Payload["baseline_release"]), cleanText(operation.Payload["baseline_sha"]), cleanText(operation.Payload["k3s_version"]), now)
		}
	case "hmr_start":
		if _, err = tx.ExecContext(ctx, `UPDATE engine.cluster_state SET hmr_state='active',hmr_node_id=$1,status='HMR Non-HA',updated_at=$2 WHERE id=1`, operation.TargetNodeID, now); err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE engine.cluster_nodes SET application_state=CASE WHEN id=$1 THEN 'active' ELSE 'drained' END,drain_reason=CASE WHEN id=$1 THEN NULL ELSE 'cluster_hmr' END,updated_at=$2 WHERE membership_state='Active'`, operation.TargetNodeID, now)
		}
	case "hmr_exit":
		status := completedClusterRecoveryStatus(operation, clusterStatus, activeSize, desiredSize, true, parseClusterJSON(configJSON))
		if _, err = tx.ExecContext(ctx, `UPDATE engine.cluster_state SET hmr_state='inactive',hmr_node_id=NULL,status=$1,updated_at=$2 WHERE id=1`, status, now); err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE engine.cluster_nodes SET application_state='active',drain_reason=NULL,updated_at=$1 WHERE membership_state='Active'`, now)
		}
	case "engine_update":
		var remaining int64
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM engine.cluster_nodes WHERE membership_state='Active' AND COALESCE(release_sha,'')<>$1`, operation.TargetSHA).Scan(&remaining); err == nil {
			if remaining == 0 {
				_, err = tx.ExecContext(ctx, `UPDATE engine.cluster_state SET baseline_release=$1,baseline_sha=$2,status='Healthy',updated_at=$3 WHERE id=1`, operation.TargetRelease, operation.TargetSHA, now)
			} else {
				_, err = tx.ExecContext(ctx, `UPDATE engine.cluster_state SET status='Mixed Version',updated_at=$1 WHERE id=1`, now)
			}
		}
	case "k3s_update":
		_, err = tx.ExecContext(ctx, `UPDATE engine.cluster_state SET status='Healthy',config_json=jsonb_set(COALESCE(NULLIF(config_json,''),'{}')::jsonb,'{k3s_version}',to_jsonb($1::text),true)::text,updated_at=$2 WHERE id=1`, operation.TargetRelease, now)
	case "node_maintenance":
		state := "active"
		reason := ""
		if cleanText(operation.Payload["action"]) == "enter" {
			state = "drained"
			reason = cleanText(operation.Payload["reason"])
		}
		_, err = tx.ExecContext(ctx, `UPDATE engine.cluster_nodes SET application_state=$1,drain_reason=$2,updated_at=$3 WHERE id=$4`, state, nullClusterString(reason), now, operation.TargetNodeID)
		if err == nil && state == "active" {
			var drained int64
			if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM engine.cluster_nodes WHERE membership_state='Active' AND application_state<>'active'`).Scan(&drained); err == nil {
				status := completedClusterRecoveryStatus(operation, clusterStatus, activeSize, desiredSize, drained == 0, parseClusterJSON(configJSON))
				_, err = tx.ExecContext(ctx, `UPDATE engine.cluster_state SET status=$1,updated_at=$2 WHERE id=1`, status, now)
			}
		}
	case "postgres_switchover", "postgres_emergency_failover":
		status := completedClusterRecoveryStatus(operation, clusterStatus, activeSize, desiredSize, true, parseClusterJSON(configJSON))
		_, err = tx.ExecContext(ctx, `UPDATE engine.cluster_state SET status=$1,updated_at=$2 WHERE id=1`, status, now)
	case "node_remove":
		for _, rawID := range anySlice(operation.Payload["removal_node_ids"]) {
			_, err = tx.ExecContext(ctx, `UPDATE engine.cluster_nodes SET membership_state='Removed',application_state='drained',drain_reason=$1,roles_json='{}',updated_at=$2 WHERE id=$3`, firstText(cleanText(operation.Payload["reason"]), "removed"), now, cleanText(rawID))
			if err != nil {
				break
			}
		}
		if err == nil {
			targetSize := coerceInt64(operation.Payload["target_size"])
			emergency, _ := operation.Payload["emergency"].(bool)
			desiredSize, status := completedRemovalClusterState(targetSize, emergency)
			_, err = tx.ExecContext(ctx, `UPDATE engine.cluster_state SET active_size=$1,desired_size=$2,status=$3,updated_at=$4 WHERE id=1`, targetSize, desiredSize, status, now)
		}
	case "membership_scale":
		_, err = tx.ExecContext(ctx, `UPDATE engine.cluster_state SET desired_size=$1,status='Pending Quorum',updated_at=$2 WHERE id=1`, coerceInt64(operation.Payload["desired_size"]), now)
	case "membership_admit":
		baselineRelease := cleanText(operation.Payload["baseline_release"])
		baselineSHA := cleanText(operation.Payload["baseline_sha"])
		rolesJSON := marshalClusterJSON(map[string]any{"k3s_version": cleanText(operation.Payload["k3s_version"])})
		probeHealth := `{"startup":"passed","readiness":"passed","liveness":"passed","direct_endpoint":"passed","service":"passed","database":"passed","scheduler":"passed","agent_path":"passed","wireguard":"passed"}`
		for _, rawID := range anySlice(operation.Payload["admission_ids"]) {
			var id, nodeName, hostname, managementIP, architecture, osVersion string
			scanErr := tx.QueryRowContext(ctx, `SELECT id,node_name,hostname,management_ip,architecture,os_version FROM engine.cluster_admissions WHERE id=$1 AND state='Approved'`, cleanText(rawID)).Scan(&id, &nodeName, &hostname, &managementIP, &architecture, &osVersion)
			if scanErr != nil {
				err = scanErr
				break
			}
			var durableNodeID string
			scanErr = tx.QueryRowContext(ctx, `
				INSERT INTO engine.cluster_nodes(id,node_name,hostname,management_ip,architecture,os_version,membership_state,application_state,release_tag,release_sha,drain_reason,roles_json,probe_health_json,last_seen_at,created_at,updated_at)
				VALUES($1,$2,$3,$4,$5,$6,'Active','active',$7,$8,NULL,$9,$10,$11,$11,$11)
				ON CONFLICT(node_name) DO UPDATE SET
					hostname=EXCLUDED.hostname,
					management_ip=EXCLUDED.management_ip,
					architecture=EXCLUDED.architecture,
					os_version=EXCLUDED.os_version,
					membership_state='Active',
					application_state='active',
					release_tag=EXCLUDED.release_tag,
					release_sha=EXCLUDED.release_sha,
					drain_reason=NULL,
					roles_json=EXCLUDED.roles_json,
					probe_health_json=EXCLUDED.probe_health_json,
					last_seen_at=EXCLUDED.last_seen_at,
					updated_at=EXCLUDED.updated_at
				WHERE engine.cluster_nodes.membership_state='Removed' OR engine.cluster_nodes.id=EXCLUDED.id
				RETURNING id
			`, id, nodeName, hostname, managementIP, architecture, osVersion, baselineRelease, baselineSHA, rolesJSON, probeHealth, now).Scan(&durableNodeID)
			if errors.Is(scanErr, sql.ErrNoRows) {
				err = fmt.Errorf("membership admission node %q conflicts with non-removed durable identity", nodeName)
				break
			}
			if scanErr != nil {
				err = scanErr
				break
			}
			_, err = tx.ExecContext(ctx, `UPDATE engine.cluster_admissions SET state='Admitted',updated_at=$1 WHERE id=$2`, now, id)
			if err != nil {
				break
			}
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE engine.cluster_state SET active_size=(SELECT COUNT(*) FROM engine.cluster_nodes WHERE membership_state='Active'),desired_size=(SELECT COUNT(*) FROM engine.cluster_nodes WHERE membership_state='Active'),status='Healthy',updated_at=$1 WHERE id=1`, now)
		}
	default:
		_, err = tx.ExecContext(ctx, `UPDATE engine.cluster_state SET status='Healthy',updated_at=$1 WHERE id=1`, now)
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_operations SET state='succeeded',current_step='complete',finished_at=$1,updated_at=$1 WHERE id=$2`, now, operation.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_state SET active_operation_id=NULL,updated_at=$1 WHERE id=1 AND active_operation_id=$2`, now, operation.ID); err != nil {
		return err
	}
	if err := insertClusterEvent(ctx, tx, operation.ID, "", "", "operation_succeeded", "succeeded", "Cluster operation completed.", map[string]any{"kind": operation.Kind}, now); err != nil {
		return err
	}
	return tx.Commit()
}

func clusterStepNodeID(step string) string {
	parts := strings.Split(step, ":")
	if len(parts) == 3 && parts[0] == "node" {
		return parts[1]
	}
	return ""
}

func clusterOperationNodeStateTransition(operation clusterControllerOperation) (string, string, string, bool) {
	nodeID := clusterStepNodeID(operation.CurrentStep)
	if nodeID == "" {
		return "", "", "", false
	}
	switch strings.TrimPrefix(operation.CurrentStep, "node:"+nodeID+":") {
	case "enter_drain":
		return nodeID, "drained", firstText(cleanText(operation.Payload["reason"]), operation.Kind), true
	case "exit_drain":
		return nodeID, "active", "", true
	default:
		return "", "", "", false
	}
}

func completedClusterRecoveryStatus(operation clusterControllerOperation, current string, activeSize, desiredSize int64, allApplicationsActive bool, config map[string]any) string {
	if activeSize == 2 && desiredSize == 3 {
		return "Degraded Quorum"
	}
	if operation.Kind == "postgres_emergency_failover" && current == "Degraded Quorum" {
		return current
	}
	if operation.Kind == "node_maintenance" && cleanText(operation.Payload["action"]) == "exit" && (!allApplicationsActive || activeSize != desiredSize) {
		return current
	}
	database := mapStringAny(config["database_runtime"])
	if len(database) == 0 {
		if current == "Degraded Database" {
			return current
		}
		return "Healthy"
	}
	return clusterRuntimeDatabaseStatus("Healthy", database)
}

func truncateClusterError(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	return value[:maximum]
}

func (r *kubernetesClusterStepRunner) Run(ctx context.Context, operation clusterControllerOperation, step clusterControllerStep, nodes []clusterControllerNode) error {
	if r == nil || r.kube == nil {
		return errors.New("Kubernetes cluster runner is unavailable")
	}
	if step.Name == "preflight" {
		if operation.Kind == "engine_update" && (!clusterReleaseRE.MatchString(operation.TargetRelease) || !clusterControllerSHARegex.MatchString(operation.TargetSHA)) {
			return errors.New("update release tag or pinned SHA is invalid")
		}
		if operation.Kind == "k3s_update" {
			if err := validateK3sUpgradePath(cleanText(operation.Payload["source_k3s_version"]), operation.TargetRelease); err != nil {
				return err
			}
			if !borealisOperatorImmutableImageRefPattern.MatchString(cleanText(operation.Payload["upgrade_image"])) {
				return errors.New("K3s upgrade image must be content-addressed")
			}
		}
		if (operation.Kind == "engine_update" || operation.Kind == "k3s_update") && len(nodes) == 1 && cleanText(operation.Payload["maintenance_outage_acknowledgement"]) != "ACCEPT OUTAGE" {
			return errors.New("one-node update requires ACCEPT OUTAGE acknowledgement")
		}
		if operation.Kind != "cluster_enable" && len(nodes) != 1 && len(nodes) != 3 && !clusterOperationSupportsTwoNodeRecovery(operation, len(nodes)) {
			return fmt.Errorf("active node count %d is not supported", len(nodes))
		}
		if len(nodes) == int(clusterSharedArtifactReplicaCount) && clusterOperationRequiresSharedArtifactHA(operation) {
			storageNodes := nodes
			requireFullHA := true
			if operation.Kind == "node_remove" && operation.Attempt > 1 {
				fenced, err := r.removalRetryHasFencedTarget(ctx, operation, nodes)
				if err != nil {
					return err
				}
				if fenced {
					storageNodes, err = clusterRemovalSurvivorNodes(operation, nodes)
					if err != nil {
						return err
					}
					requireFullHA = false
				}
			}
			var err error
			if requireFullHA {
				err = r.waitForSharedArtifactStorageHA(ctx, storageNodes)
			} else {
				err = r.waitForSharedArtifactStorageState(ctx, storageNodes, int64(len(storageNodes)), false, false)
			}
			if err != nil {
				return fmt.Errorf("shared Agent artifact storage is not failure-safe: %w", err)
			}
		}
		if operation.Kind == "engine_update" {
			selected, err := clusterUpdateNodes(operation, nodes)
			if err != nil {
				return err
			}
			for _, node := range selected {
				if err := r.nodeActionJob(ctx, operation, clusterControllerStep{Name: "preflight:" + node.ID, NodeID: node.ID}, node, "PreflightRelease"); err != nil {
					return fmt.Errorf("node %s release preflight failed: %w", node.Name, err)
				}
			}
		}
		return nil
	}
	if step.Name == "pre_change_snapshot" {
		if operation.Kind == "k3s_update" {
			ordered, err := clusterUpdateNodes(operation, nodes)
			if err != nil {
				return err
			}
			if len(ordered) == 0 {
				return errors.New("K3s snapshot has no healthy target server")
			}
			if err := r.nodeActionJob(ctx, operation, clusterControllerStep{Name: "pre_change_snapshot:etcd", NodeID: ordered[0].ID}, ordered[0], "CreateEtcdSnapshot"); err != nil {
				return err
			}
		}
		return r.createCNPGBackup(ctx, operation)
	}
	if step.Name == "prepare_postgres_removal" {
		return r.preparePostgresRemoval(ctx, operation, nodes)
	}
	if step.Name == "scale_postgres_membership" {
		return r.scaleCNPG(ctx, int(coerceInt64(operation.Payload["target_size"])))
	}
	if step.Name == "scale_shared_artifact_membership" {
		survivors, err := clusterRemovalSurvivorNodes(operation, nodes)
		if err != nil {
			return err
		}
		return r.waitForSharedArtifactStorageState(ctx, survivors, coerceInt64(operation.Payload["target_size"]), true, true)
	}
	if step.Name == "expand_schema" || step.Name == "finalize_schema" {
		node := clusterNodeByID(nodes, step.NodeID)
		if node.ID == "" {
			return errors.New("schema phase target is not an active node")
		}
		if step.Name == "finalize_schema" {
			if !clusterAllNodesAtRevision(nodes, operation.TargetSHA) {
				// Node-scoped updates keep contract work pending until last active
				// node reaches target revision through later explicit update.
				return nil
			}
		}
		phase := strings.TrimSuffix(step.Name, "_schema")
		phaseOperation := operation
		phaseOperation.Payload = copyMap(operation.Payload)
		phaseOperation.Payload["schema_phase"] = phase
		return r.nodeActionJob(ctx, phaseOperation, step, node, "RunSchemaPhase")
	}
	if step.Name == "verify_cluster" || step.Name == "verify_quorum" || step.Name == "verify_postgres" {
		return r.verifyReadyEndpoints(ctx)
	}
	if step.Name == "apply_cluster_foundation" {
		nodeName := cleanText(operation.Payload["node_name"])
		node := clusterControllerNode{ID: operation.ID, Name: nodeName}
		return r.nodeActionJob(ctx, operation, step, node, "EnrollCluster")
	}
	if step.Name == "migrate_postgres" {
		return r.verifyCNPGMigration(ctx)
	}
	if step.Name == "postgres_role_change" {
		node := clusterNodeByID(nodes, step.NodeID)
		if node.ID == "" {
			return errors.New("PostgreSQL target is not active")
		}
		return r.ensurePostgresPrimaryOnNode(ctx, node.Name)
	}
	if step.Name == "apply_membership" && operation.Kind == "membership_admit" {
		return r.admitPendingMembers(ctx, operation, nodes)
	}
	if step.Name == "apply_membership" {
		return r.recordClusterIntent(ctx, operation, step)
	}
	if step.Name == "hmr_move_roles" {
		node := clusterNodeByID(nodes, step.NodeID)
		if err := r.ensurePostgresPrimaryOnNode(ctx, node.Name); err != nil {
			return err
		}
		if err := r.waitPostgresClusterSynchronizedOnNode(ctx, node.Name); err != nil {
			return err
		}
		for _, candidate := range nodes {
			target := candidate.ID == node.ID
			if err := r.setNodeRoleEligibility(ctx, candidate.Name, target); err != nil {
				return err
			}
		}
		return r.waitEdgeAndWireGuardOwner(ctx, node.Name)
	}
	if step.Name == "hmr_move_roles_to_standby" {
		targetID := firstText(operation.TargetNodeID, cleanText(operation.Payload["hmr_node_id"]))
		target := clusterNodeByID(nodes, targetID)
		standby := clusterNodeByID(nodes, step.NodeID)
		if target.ID == "" || standby.ID == "" || target.ID == standby.ID {
			return errors.New("HMR production restore lacks distinct active role targets")
		}
		if err := r.ensurePostgresPrimaryOnNode(ctx, standby.Name); err != nil {
			return err
		}
		if err := r.waitPostgresClusterSynchronizedOnNode(ctx, standby.Name); err != nil {
			return err
		}
		if err := r.setNodeRoleEligibility(ctx, standby.Name, true); err != nil {
			return err
		}
		if err := r.setNodeRoleEligibility(ctx, target.Name, false); err != nil {
			return err
		}
		// PostgreSQL is deliberately pinned to the first restored standby, but
		// kube-vip may elect either eligible non-target node for edge traffic.
		// HMR exit only needs production roles to leave the target safely.
		return r.waitEdgeAndWireGuardOwnerAwayFrom(ctx, target.Name)
	}
	if step.Name == "hmr_fence_target_roles" {
		target := clusterNodeByID(nodes, step.NodeID)
		if target.ID == "" {
			return errors.New("saved HMR target unavailable")
		}
		return r.setNodeRoleEligibility(ctx, target.Name, false)
	}
	if step.Name == "hmr_drain_standby" {
		for _, node := range nodes {
			state := "drained"
			edge := "false"
			if node.ID == step.NodeID {
				state = "active"
				edge = "true"
			}
			if err := r.patchNodeLabels(ctx, node.Name, map[string]string{"borealis.io/application-state": state, "borealis.io/edge-eligible": edge, "borealis.io/hmr-target": strconv.FormatBool(node.ID == step.NodeID)}); err != nil {
				return err
			}
			if node.ID != step.NodeID {
				drainStep := clusterControllerStep{Name: "hmr:" + node.ID + ":enter_drain", NodeID: node.ID}
				if err := r.nodeActionJob(ctx, operation, drainStep, node, "EnterApplicationDrain"); err != nil {
					return err
				}
				if err := r.waitNodeEndpointsWithdrawn(ctx, node.Name); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if step.Name == "hmr_stage_pinned_release" {
		node := clusterNodeByID(nodes, step.NodeID)
		stage := operation
		stage.TargetRelease = cleanText(operation.Payload["baseline_release"])
		stage.TargetSHA = cleanText(operation.Payload["baseline_sha"])
		return r.nodeActionJob(ctx, stage, step, node, "StagePinnedRelease")
	}
	if step.Name == "hmr_activate_target" || step.Name == "hmr_verify_target" {
		node := clusterNodeByID(nodes, step.NodeID)
		if node.ID == "" {
			return errors.New("HMR target disappeared")
		}
		if step.Name == "hmr_verify_target" {
			if err := r.nodeActionJob(ctx, operation, step, node, "InspectHealth"); err != nil {
				return err
			}
			return r.minimumReadySoak(ctx, node.Name)
		}
		if err := r.patchNodeLabels(ctx, node.Name, map[string]string{"borealis.io/application-state": "active", "borealis.io/edge-eligible": "true", "borealis.io/scheduler-eligible": "true", "borealis.io/postgres-primary-eligible": "true", "borealis.io/hmr-target": "true"}); err != nil {
			return err
		}
		return r.nodeActionJob(ctx, operation, step, node, "ExitApplicationDrain")
	}
	if step.Name == "hmr_restore_pinned_release" {
		node := clusterNodeByID(nodes, step.NodeID)
		if node.ID == "" {
			return errors.New("saved HMR target unavailable")
		}
		restore, err := hmrPinnedRestoreOperation(operation, node)
		if err != nil {
			return err
		}
		if err := r.nodeActionJob(ctx, restore, clusterControllerStep{Name: step.Name + ":stage", NodeID: node.ID}, node, "StagePinnedRelease"); err != nil {
			return err
		}
		return r.nodeActionJob(ctx, restore, clusterControllerStep{Name: step.Name + ":deploy", NodeID: node.ID}, node, "RedeployStagedRevision")
	}
	if step.Name == "hmr_inspect_candidate" || step.Name == "hmr_candidate_soak" || step.Name == "hmr_promote_candidate" {
		node := clusterNodeByID(nodes, step.NodeID)
		if node.ID == "" {
			return errors.New("saved HMR target unavailable")
		}
		restore, err := hmrPinnedRestoreOperation(operation, node)
		if err != nil {
			return err
		}
		switch step.Name {
		case "hmr_inspect_candidate":
			return r.nodeActionJob(ctx, restore, step, node, "InspectCandidateHealth")
		case "hmr_promote_candidate":
			return r.nodeActionJob(ctx, restore, step, node, "PromoteCandidate")
		default:
			if err := r.nodeActionJob(ctx, restore, clusterControllerStep{Name: step.Name + ":start", NodeID: node.ID}, node, "InspectCandidateHealth"); err != nil {
				return err
			}
			timer := time.NewTimer(r.soak)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
			}
			return r.nodeActionJob(ctx, restore, clusterControllerStep{Name: step.Name + ":finish", NodeID: node.ID}, node, "InspectCandidateHealth")
		}
	}
	if step.Name == "hmr_verify_production" {
		node := clusterNodeByID(nodes, step.NodeID)
		if err := r.nodeActionJob(ctx, operation, step, node, "InspectHealth"); err != nil {
			return err
		}
		return r.minimumReadySoak(ctx, node.Name)
	}
	if strings.HasPrefix(step.Name, "node:") {
		node := clusterNodeByID(nodes, step.NodeID)
		if node.ID == "" {
			return errors.New("operation target is not an active node")
		}
		action := strings.TrimPrefix(step.Name, "node:"+step.NodeID+":")
		if operation.Kind == "node_remove" && action == "transfer_roles" {
			exists, err := r.kubernetesNodeExists(ctx, node.Name)
			if err != nil {
				return err
			}
			if !exists {
				return nil
			}
		}
		if operation.Kind == "node_remove" && textInSet(action, "enter_drain", "prepare_member_removal") {
			ready, err := r.nodeReady(ctx, node.Name)
			if err != nil {
				return err
			}
			if !ready {
				return nil
			}
		}
		switch action {
		case "transfer_roles":
			if operation.Kind == "node_remove" {
				return r.transferRemovalRolesAway(ctx, operation, node, nodes)
			}
			return r.transferRolesAway(ctx, node, nodes)
		case "enter_drain":
			return r.nodeActionJob(ctx, operation, step, node, "EnterApplicationDrain")
		case "wait_endpoint_withdrawal":
			return r.waitNodeEndpointsWithdrawn(ctx, node.Name)
		case "prepare_member_removal":
			if err := r.recordClusterIntent(ctx, operation, step); err != nil {
				return err
			}
			return r.nodeActionJob(ctx, operation, step, node, "PrepareMemberRemoval")
		case "remove_etcd_membership":
			return r.removeEtcdMembership(ctx, node.Name)
		case "wait_member_fenced":
			return r.waitNodeNotReady(ctx, node.Name)
		case "delete_member_node":
			return r.deleteNodeResource(ctx, node.Name)
		case "verify_member_removed":
			return r.verifyMemberRemoved(ctx, operation, node.Name, nodes)
		case "fetch_release":
			return r.nodeActionJob(ctx, operation, step, node, "FetchRelease")
		case "stage_revision_images":
			return r.nodeActionJob(ctx, operation, step, node, "StageRevisionImages")
		case "redeploy_revision":
			return r.nodeActionJob(ctx, operation, step, node, "RedeployRevision")
		case "apply_k3s_upgrade":
			return r.applyK3sUpgrade(ctx, operation, node)
		case "wait_k3s_ready":
			return r.verifyK3sNodeVersion(ctx, node.Name, operation.TargetRelease)
		case "post_k3s_conformance":
			if err := r.nodeActionJob(ctx, operation, step, node, "RunK3sProbeConformance"); err != nil {
				return err
			}
			return r.cleanupK3sUpgrade(ctx, operation, node.Name)
		case "inspect_candidate_health":
			return r.nodeActionJob(ctx, operation, step, node, "InspectCandidateHealth")
		case "minimum_candidate_soak":
			if err := r.nodeActionJob(ctx, operation, clusterControllerStep{Name: step.Name + ":start", NodeID: node.ID}, node, "InspectCandidateHealth"); err != nil {
				return err
			}
			timer := time.NewTimer(r.soak)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
			}
			return r.nodeActionJob(ctx, operation, clusterControllerStep{Name: step.Name + ":finish", NodeID: node.ID}, node, "InspectCandidateHealth")
		case "promote_candidate":
			return r.nodeActionJob(ctx, operation, step, node, "PromoteCandidate")
		case "prepare_restore":
			return r.nodeActionJob(ctx, operation, step, node, "PrepareApplicationRestore")
		case "inspect_health":
			return r.nodeActionJob(ctx, operation, step, node, "InspectHealth")
		case "minimum_ready_soak":
			return r.minimumReadySoak(ctx, node.Name)
		case "restore_roles":
			if err := r.setNodeRoleEligibility(ctx, node.Name, true); err != nil {
				return err
			}
			if len(nodes) == 1 {
				return r.waitEdgeAndWireGuardOwner(ctx, node.Name)
			}
			return nil
		case "inspect_role_health":
			return r.nodeActionJob(ctx, operation, step, node, "InspectHealth")
		case "minimum_role_soak":
			return r.minimumReadySoak(ctx, node.Name)
		case "exit_drain":
			if err := r.nodeActionJob(ctx, operation, step, node, "ExitApplicationDrain"); err != nil {
				return err
			}
			return r.patchNodeLabels(ctx, node.Name, map[string]string{"borealis.io/hmr-target": "false"})
		}
	}
	return fmt.Errorf("unsupported cluster controller step %q", step.Name)
}

func clusterOperationSupportsTwoNodeRecovery(operation clusterControllerOperation, activeNodes int) bool {
	if activeNodes != 2 {
		return false
	}
	switch operation.Kind {
	case "membership_admit", "postgres_emergency_failover":
		return true
	case "node_maintenance":
		return cleanText(operation.Payload["action"]) == "exit"
	default:
		return false
	}
}

func clusterOperationRequiresSharedArtifactHA(operation clusterControllerOperation) bool {
	switch operation.Kind {
	case "engine_update", "k3s_update", "hmr_start":
		return true
	case "node_maintenance":
		return cleanText(operation.Payload["action"]) == "enter"
	case "node_remove":
		emergency, _ := operation.Payload["emergency"].(bool)
		return !emergency
	default:
		return false
	}
}

func completedRemovalClusterState(targetSize int64, emergency bool) (int64, string) {
	if emergency {
		return 3, "Degraded Quorum"
	}
	return targetSize, "Healthy"
}

func hmrPinnedRestoreOperation(operation clusterControllerOperation, node clusterControllerNode) (clusterControllerOperation, error) {
	sha := firstText(operation.TargetSHA, cleanText(operation.Payload["baseline_sha"]), node.ReleaseSHA)
	release := firstText(operation.TargetRelease, cleanText(operation.Payload["baseline_release"]), node.ReleaseTag)
	if !clusterControllerSHARegex.MatchString(sha) || !clusterReleaseRE.MatchString(release) {
		return clusterControllerOperation{}, errors.New("saved pinned production release is unavailable")
	}
	restore := operation
	restore.TargetSHA = sha
	restore.TargetRelease = release
	return restore, nil
}

func validateCurrentReleaseAdmission(activeNodes, pendingAdmissions int) error {
	if (activeNodes == 1 && pendingAdmissions == 2) || (activeNodes == 2 && pendingAdmissions == 1) {
		return nil
	}
	return fmt.Errorf("membership admission from %d active node(s) with %d pending node(s) is unsupported; current release restores exactly three active nodes and odd membership changes beyond three nodes are future roadmap work", activeNodes, pendingAdmissions)
}

func (r *kubernetesClusterStepRunner) admitPendingMembers(ctx context.Context, operation clusterControllerOperation, activeNodes []clusterControllerNode) error {
	ids := anySlice(operation.Payload["admission_ids"])
	names := anySlice(operation.Payload["node_names"])
	baselineRelease := cleanText(operation.Payload["baseline_release"])
	baselineSHA := cleanText(operation.Payload["baseline_sha"])
	if err := validateCurrentReleaseAdmission(len(activeNodes), len(ids)); err != nil {
		return err
	}
	if len(names) != len(ids) || !clusterReleaseRE.MatchString(baselineRelease) || !clusterControllerSHARegex.MatchString(baselineSHA) {
		return errors.New("membership admission lacks pinned cluster baseline")
	}
	pending := make([]clusterControllerNode, 0, len(ids))
	for index := range ids {
		node := clusterControllerNode{ID: cleanText(ids[index]), Name: cleanText(names[index]), ReleaseTag: baselineRelease, ReleaseSHA: baselineSHA}
		if !clusterUUIDRE.MatchString(node.ID) || !clusterControllerNodeRegex.MatchString(node.Name) {
			return errors.New("membership admission contains invalid node identity")
		}
		pending = append(pending, node)
	}
	newSize := len(activeNodes) + len(pending)
	for _, node := range pending {
		if err := r.waitNodeReady(ctx, node.Name); err != nil {
			return err
		}
		if err := r.patchNodeLabels(ctx, node.Name, map[string]string{
			"borealis.io/application-state":         "drained",
			"borealis.io/edge-eligible":             "false",
			"borealis.io/scheduler-eligible":        "false",
			"borealis.io/postgres-primary-eligible": "false",
			"borealis.io/hmr-target":                "false",
		}); err != nil {
			return err
		}
	}
	k3sVersion := cleanText(operation.Payload["k3s_version"])
	if !clusterK3sRE.MatchString(k3sVersion) {
		return errors.New("membership admission lacks stable K3s version")
	}
	for _, node := range pending {
		action := clusterAdmissionConformanceAction(node.ID, k3sVersion)
		conformance := operation
		conformance.TargetNodeID = node.ID
		conformance.TargetRelease = action.targetRelease
		if err := r.nodeActionJob(ctx, conformance, clusterControllerStep{Name: action.stepName, NodeID: node.ID}, node, action.verb); err != nil {
			return err
		}
	}
	if err := r.scaleCNPG(ctx, newSize); err != nil {
		return err
	}
	storageNodes := append(append(make([]clusterControllerNode, 0, newSize), activeNodes...), pending...)
	if err := r.waitForSharedArtifactStorageHA(ctx, storageNodes); err != nil {
		return fmt.Errorf("shared Agent artifact storage did not reach one healthy replica per Engine: %w", err)
	}
	for _, node := range pending {
		redeploy := operation
		redeploy.TargetNodeID = node.ID
		redeploy.TargetRelease = baselineRelease
		redeploy.TargetSHA = baselineSHA
		for _, action := range clusterAdmissionWorkloadActions(node.ID) {
			actionOperation := redeploy
			if action.targetRelease != "" {
				actionOperation.TargetRelease = action.targetRelease
			}
			if err := r.nodeActionJob(ctx, actionOperation, clusterControllerStep{Name: action.stepName, NodeID: node.ID}, node, action.verb); err != nil {
				return err
			}
			if action.soakAfter {
				timer := time.NewTimer(r.soak)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
				if err := r.nodeActionJob(ctx, actionOperation, clusterControllerStep{Name: action.stepName + ":soak", NodeID: node.ID}, node, action.verb); err != nil {
					return err
				}
			}
		}
		// Start and prove local WireGuard while all cluster roles remain fenced.
		// Eligibility is enabled only after active workload soak completes.
		if err := r.scaleNodeWorkload(ctx, node.Name, "wireguard-tunnel", true); err != nil {
			return err
		}
		healthStep := clusterControllerStep{Name: "admit:" + node.ID + ":active_health", NodeID: node.ID}
		if err := r.nodeActionJob(ctx, redeploy, healthStep, node, "InspectHealth"); err != nil {
			return err
		}
		timer := time.NewTimer(r.soak)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if err := r.nodeActionJob(ctx, redeploy, clusterControllerStep{Name: healthStep.Name + ":soak", NodeID: node.ID}, node, "InspectHealth"); err != nil {
			return err
		}
		if err := r.patchNodeLabels(ctx, node.Name, map[string]string{
			"borealis.io/edge-eligible":             "true",
			"borealis.io/scheduler-eligible":        "true",
			"borealis.io/postgres-primary-eligible": "true",
		}); err != nil {
			return err
		}
		activationErr := r.nodeActionJob(ctx, redeploy, clusterControllerStep{Name: "admit:" + node.ID + ":role_health", NodeID: node.ID}, node, "InspectHealth")
		if activationErr == nil {
			timer = time.NewTimer(r.soak)
			select {
			case <-ctx.Done():
				timer.Stop()
				activationErr = ctx.Err()
			case <-timer.C:
			}
		}
		if activationErr == nil {
			activationErr = r.nodeActionJob(ctx, redeploy, clusterControllerStep{Name: "admit:" + node.ID + ":role_health:soak", NodeID: node.ID}, node, "InspectHealth")
		}
		if activationErr == nil {
			activationErr = r.nodeActionJob(ctx, redeploy, clusterControllerStep{Name: "admit:" + node.ID + ":exit_drain", NodeID: node.ID}, node, "ExitApplicationDrain")
		}
		if activationErr != nil {
			_ = r.patchNodeLabels(ctx, node.Name, map[string]string{
				"borealis.io/edge-eligible":             "false",
				"borealis.io/scheduler-eligible":        "false",
				"borealis.io/postgres-primary-eligible": "false",
			})
			_ = r.scaleNodeWorkload(ctx, node.Name, "wireguard-tunnel", false)
			return activationErr
		}
	}
	return r.recordClusterIntent(ctx, operation, clusterControllerStep{Name: "apply_membership"})
}

func (r *kubernetesClusterStepRunner) waitForSharedArtifactStorageHA(ctx context.Context, nodes []clusterControllerNode) error {
	return r.waitForSharedArtifactStorageState(ctx, nodes, clusterSharedArtifactReplicaCount, true, false)
}

func (r *kubernetesClusterStepRunner) waitForSharedArtifactStorageState(ctx context.Context, nodes []clusterControllerNode, replicaCount int64, requireHealthy, exactReplicaCount bool) error {
	pollInterval := r.jobPollInterval
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	var lastErr error
	for {
		if err := r.reconcileSharedArtifactStorageState(ctx, nodes, replicaCount, requireHealthy, exactReplicaCount); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return errors.Join(lastErr, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (r *kubernetesClusterStepRunner) reconcileSharedArtifactStorage(ctx context.Context, nodes []clusterControllerNode) error {
	return r.reconcileSharedArtifactStorageState(ctx, nodes, clusterSharedArtifactReplicaCount, true, false)
}

func (r *kubernetesClusterStepRunner) reconcileSharedArtifactStorageState(ctx context.Context, nodes []clusterControllerNode, replicaCount int64, requireHealthy, exactReplicaCount bool) error {
	if r == nil || r.kube == nil {
		return errors.New("Kubernetes cluster runner is unavailable")
	}
	if len(nodes) == 0 || replicaCount < 1 || replicaCount > clusterSharedArtifactReplicaCount || int64(len(nodes)) < replicaCount {
		return errors.New("shared Agent artifact storage received invalid replica safety target")
	}
	wantedNodes := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		if !clusterControllerNodeRegex.MatchString(node.Name) || wantedNodes[node.Name] {
			return errors.New("shared Agent artifact storage received invalid or duplicate Engine node identity")
		}
		wantedNodes[node.Name] = true
	}

	pvcPath := fmt.Sprintf("/api/v1/namespaces/%s/persistentvolumeclaims/%s", r.namespace, clusterSharedArtifactPVCName)
	var pvc map[string]any
	if err := r.kube.getJSON(ctx, pvcPath, &pvc); err != nil {
		return fmt.Errorf("read shared Agent artifact PVC: %w", err)
	}
	pvcSpec := nestedMap(pvc, "spec")
	if cleanText(nestedMap(pvc, "status")["phase"]) != "Bound" {
		return errors.New("shared Agent artifact PVC is not Bound")
	}
	if !anyTextSliceContains(pvcSpec["accessModes"], "ReadWriteMany") {
		return errors.New("shared Agent artifact PVC is not ReadWriteMany")
	}
	volumeName := cleanText(pvcSpec["volumeName"])
	if !clusterControllerNodeRegex.MatchString(volumeName) {
		return errors.New("shared Agent artifact PVC has invalid bound volume identity")
	}

	var pv map[string]any
	if err := r.kube.getJSON(ctx, "/api/v1/persistentvolumes/"+volumeName, &pv); err != nil {
		return fmt.Errorf("read shared Agent artifact PV: %w", err)
	}
	pvSpec := nestedMap(pv, "spec")
	claimRef := nestedMap(pvSpec, "claimRef")
	if cleanText(claimRef["namespace"]) != r.namespace || cleanText(claimRef["name"]) != clusterSharedArtifactPVCName {
		return errors.New("shared Agent artifact PV claim ownership does not match fixed Borealis PVC")
	}
	if !anyTextSliceContains(pvSpec["accessModes"], "ReadWriteMany") {
		return errors.New("shared Agent artifact PV is not ReadWriteMany")
	}
	csi := nestedMap(pvSpec, "csi")
	if cleanText(csi["driver"]) != clusterLonghornCSIDriver || cleanText(csi["volumeHandle"]) != volumeName {
		return errors.New("shared Agent artifact PV is not exact Longhorn volume")
	}

	volumePath := fmt.Sprintf("/apis/longhorn.io/v1beta2/namespaces/%s/volumes/%s", clusterLonghornNamespace, volumeName)
	var volume map[string]any
	if err := r.kube.getJSON(ctx, volumePath, &volume); err != nil {
		return fmt.Errorf("read shared Agent artifact Longhorn volume: %w", err)
	}
	volumeSpec := nestedMap(volume, "spec")
	volumeStatus := nestedMap(volume, "status")
	kubernetesStatus := nestedMap(volumeStatus, "kubernetesStatus")
	if cleanText(volumeSpec["accessMode"]) != "rwx" ||
		cleanText(kubernetesStatus["namespace"]) != r.namespace ||
		cleanText(kubernetesStatus["pvcName"]) != clusterSharedArtifactPVCName ||
		cleanText(kubernetesStatus["pvName"]) != volumeName {
		return errors.New("Longhorn volume ownership or RWX mode does not match fixed shared Agent artifact PVC")
	}
	if current := coerceInt64(volumeSpec["numberOfReplicas"]); current < replicaCount || (exactReplicaCount && current != replicaCount) {
		patch := map[string]any{"spec": map[string]any{"numberOfReplicas": replicaCount}}
		var patched map[string]any
		if err := r.kube.doJSON(ctx, http.MethodPatch, volumePath, patch, "application/merge-patch+json", &patched, 30*time.Second); err != nil {
			return fmt.Errorf("reconcile shared Agent artifact Longhorn replicas: %w", err)
		}
		return fmt.Errorf("Longhorn replica reconciliation started: %d of %d configured", current, replicaCount)
	}
	robustness := cleanText(volumeStatus["robustness"])
	if requireHealthy && robustness != "healthy" {
		return fmt.Errorf("Longhorn volume robustness is %q", robustness)
	}
	if !requireHealthy && !textInSet(robustness, "healthy", "degraded") {
		return fmt.Errorf("Longhorn volume robustness is %q", robustness)
	}

	replicaPath := fmt.Sprintf("/apis/longhorn.io/v1beta2/namespaces/%s/replicas?labelSelector=%s", clusterLonghornNamespace, url.QueryEscape("longhornvolume="+volumeName))
	var replicas map[string]any
	if err := r.kube.getJSON(ctx, replicaPath, &replicas); err != nil {
		return fmt.Errorf("read shared Agent artifact Longhorn replicas: %w", err)
	}
	healthyNodes := make(map[string]bool, len(wantedNodes))
	for _, raw := range anySlice(replicas["items"]) {
		replica, _ := raw.(map[string]any)
		spec := nestedMap(replica, "spec")
		status := nestedMap(replica, "status")
		nodeName := cleanText(spec["nodeID"])
		evictionRequested, _ := spec["evictionRequested"].(bool)
		if wantedNodes[nodeName] && cleanText(spec["failedAt"]) == "" && cleanText(spec["healthyAt"]) != "" &&
			cleanText(spec["desireState"]) == "running" && cleanText(status["currentState"]) == "running" && !evictionRequested {
			healthyNodes[nodeName] = true
		}
	}
	missing := make([]string, 0, len(wantedNodes))
	for nodeName := range wantedNodes {
		if !healthyNodes[nodeName] {
			missing = append(missing, nodeName)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("Longhorn lacks healthy running replica on Engine node(s): %s", strings.Join(missing, ", "))
	}
	return nil
}

func anyTextSliceContains(value any, wanted string) bool {
	for _, raw := range anySlice(value) {
		if cleanText(raw) == wanted {
			return true
		}
	}
	return false
}

func (r *kubernetesClusterStepRunner) waitNodeReady(ctx context.Context, nodeName string) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		ready, err := r.nodeReady(ctx, nodeName)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("node %s did not join Ready: %w", nodeName, ctx.Err())
		case <-ticker.C:
		}
	}
}

func validateCNPGMembershipSize(size int) error {
	if size < 1 || size > 3 {
		return fmt.Errorf("unsupported CloudNativePG membership size %d", size)
	}
	return nil
}

func (r *kubernetesClusterStepRunner) scaleCNPG(ctx context.Context, size int) error {
	if err := validateCNPGMembershipSize(size); err != nil {
		return err
	}
	path := fmt.Sprintf("/apis/postgresql.cnpg.io/v1/namespaces/%s/clusters/borealis-postgres", r.namespace)
	var synchronous any
	if size > 1 {
		synchronous = map[string]any{"method": "any", "number": int64(1), "dataDurability": "required"}
	}
	patch := map[string]any{"spec": map[string]any{"instances": size, "postgresql": map[string]any{"synchronous": synchronous}}}
	var result map[string]any
	if err := r.kube.doJSON(ctx, http.MethodPatch, path, patch, "application/merge-patch+json", &result, 30*time.Second); err != nil {
		return err
	}
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		var cluster map[string]any
		if err := r.kube.getJSON(ctx, path, &cluster); err != nil {
			return err
		}
		status := nestedMap(cluster, "status")
		if coerceInt64(status["instances"]) == int64(size) && coerceInt64(status["readyInstances"]) == int64(size) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("CloudNativePG did not reach %d ready instances: %w", size, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (r *kubernetesClusterStepRunner) preparePostgresRemoval(ctx context.Context, operation clusterControllerOperation, nodes []clusterControllerNode) error {
	selected, err := clusterRemovalNodes(operation, nodes)
	if err != nil {
		return err
	}
	targetSize := int(coerceInt64(operation.Payload["target_size"]))
	if targetSize != len(nodes)-len(selected) || (targetSize != 1 && targetSize != 3) {
		return errors.New("safe paired removal has invalid PostgreSQL target size")
	}
	selectedIDs := make(map[string]bool, len(selected))
	for _, node := range selected {
		selectedIDs[node.ID] = true
	}
	var survivor clusterControllerNode
	for _, node := range nodes {
		if !selectedIDs[node.ID] && node.ApplicationState == "active" {
			survivor = node
			break
		}
	}
	if survivor.ID == "" {
		return errors.New("safe paired removal has no active PostgreSQL survivor")
	}
	if err := r.ensurePostgresPrimaryOnNode(ctx, survivor.Name); err != nil {
		return fmt.Errorf("move CloudNativePG primary to surviving node %s: %w", survivor.Name, err)
	}
	if err := r.scaleCNPG(ctx, targetSize); err != nil {
		return err
	}
	targetNames := map[string]bool{}
	for _, node := range selected {
		targetNames[node.Name] = true
	}
	occupied, err := r.cnpgPodsOnNodes(ctx, targetNames)
	if err != nil {
		return err
	}
	if len(occupied) == 0 {
		return nil
	}
	restoreErr := r.scaleCNPG(ctx, len(nodes))
	message := fmt.Errorf("CloudNativePG retained instances on removal targets %s; select nodes hosting scale-down instances", strings.Join(occupied, ", "))
	if restoreErr != nil {
		return errors.Join(message, fmt.Errorf("restore PostgreSQL membership: %w", restoreErr))
	}
	return message
}

func (r *kubernetesClusterStepRunner) cnpgPodsOnNodes(ctx context.Context, nodeNames map[string]bool) ([]string, error) {
	path := fmt.Sprintf("/api/v1/namespaces/%s/pods?labelSelector=cnpg.io%%2Fcluster%%3Dborealis-postgres", r.namespace)
	var payload map[string]any
	if err := r.kube.getJSON(ctx, path, &payload); err != nil {
		return nil, err
	}
	occupied := make([]string, 0, len(nodeNames))
	seen := map[string]bool{}
	for _, raw := range anySlice(payload["items"]) {
		pod, _ := raw.(map[string]any)
		nodeName := cleanText(nestedMap(pod, "spec")["nodeName"])
		if nodeNames[nodeName] && !seen[nodeName] {
			seen[nodeName] = true
			occupied = append(occupied, nodeName)
		}
	}
	sort.Strings(occupied)
	return occupied, nil
}

func (r *kubernetesClusterStepRunner) transferRolesAway(ctx context.Context, target clusterControllerNode, nodes []clusterControllerNode) error {
	var replacement clusterControllerNode
	for _, node := range nodes {
		if node.ID != target.ID && node.ApplicationState == "active" {
			replacement = node
			break
		}
	}
	if replacement.ID == "" {
		// One-node maintenance/update has an acknowledged outage and no transfer target.
		return r.setNodeRoleEligibility(ctx, target.Name, false)
	}
	if err := r.ensurePostgresPrimaryOnNode(ctx, replacement.Name); err != nil {
		return err
	}
	if err := r.setNodeRoleEligibility(ctx, replacement.Name, true); err != nil {
		return err
	}
	if err := r.setNodeRoleEligibility(ctx, target.Name, false); err != nil {
		return err
	}
	return r.waitEdgeAndWireGuardOwnerAwayFrom(ctx, target.Name)
}

func (r *kubernetesClusterStepRunner) transferRemovalRolesAway(ctx context.Context, operation clusterControllerOperation, target clusterControllerNode, nodes []clusterControllerNode) error {
	removing := map[string]bool{}
	for _, rawID := range anySlice(operation.Payload["removal_node_ids"]) {
		removing[cleanText(rawID)] = true
	}
	var replacement clusterControllerNode
	for _, node := range nodes {
		if !removing[node.ID] && node.ApplicationState == "active" {
			replacement = node
			break
		}
	}
	if replacement.ID == "" {
		return errors.New("paired removal has no active surviving role target")
	}
	if err := r.ensurePostgresPrimaryOnNode(ctx, replacement.Name); err != nil {
		return err
	}
	if err := r.setNodeRoleEligibility(ctx, replacement.Name, true); err != nil {
		return err
	}
	if err := r.setNodeRoleEligibility(ctx, target.Name, false); err != nil {
		return err
	}
	return r.waitEdgeAndWireGuardOwnerAwayFrom(ctx, target.Name)
}

func (r *kubernetesClusterStepRunner) setNodeRoleEligibility(ctx context.Context, nodeName string, eligible bool) error {
	value := strconv.FormatBool(eligible)
	labels := map[string]string{
		"borealis.io/edge-eligible":             value,
		"borealis.io/scheduler-eligible":        value,
		"borealis.io/postgres-primary-eligible": value,
	}
	if err := r.patchNodeLabels(ctx, nodeName, labels); err != nil {
		return err
	}
	// Controller remains available on every active node. Its owner-aware
	// readiness and interface gate follow actual edge VIP lease ownership.
	// Eligibility changes happen before edge ownership moves. Keep the
	// controller present without requiring standby readiness from the prior
	// release; transfer callers prove the elected owner's readiness afterward.
	if err := r.scaleNodeWorkloadWithReadiness(ctx, nodeName, "wireguard-tunnel", true, false); err != nil {
		if !eligible {
			return err
		}
		rollbackLabels := map[string]string{
			"borealis.io/edge-eligible":             "false",
			"borealis.io/scheduler-eligible":        "false",
			"borealis.io/postgres-primary-eligible": "false",
		}
		labelErr := r.patchNodeLabels(ctx, nodeName, rollbackLabels)
		return errors.Join(err, labelErr)
	}
	return nil
}

func (r *kubernetesClusterStepRunner) waitEdgeAndWireGuardOwner(ctx context.Context, nodeName string) error {
	if !clusterControllerNodeRegex.MatchString(nodeName) {
		return errors.New("invalid edge owner node")
	}
	path := "/apis/coordination.k8s.io/v1/namespaces/kube-system/leases/borealis-edge-vip"
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		var lease map[string]any
		if err := r.kube.getJSON(ctx, path, &lease); err == nil && cleanText(nestedMap(lease, "spec")["holderIdentity"]) == nodeName {
			return r.scaleNodeWorkload(ctx, nodeName, "wireguard-tunnel", true)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("edge VIP and WireGuard ownership did not move to %s: %w", nodeName, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (r *kubernetesClusterStepRunner) waitEdgeAndWireGuardOwnerAwayFrom(ctx context.Context, targetNodeName string) error {
	if !clusterControllerNodeRegex.MatchString(targetNodeName) {
		return errors.New("invalid former edge owner node")
	}
	path := "/apis/coordination.k8s.io/v1/namespaces/kube-system/leases/borealis-edge-vip"
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		var lease map[string]any
		if err := r.kube.getJSON(ctx, path, &lease); err == nil {
			owner := cleanText(nestedMap(lease, "spec")["holderIdentity"])
			if owner != targetNodeName && clusterControllerNodeRegex.MatchString(owner) {
				ready, readyErr := r.nodeWorkloadReady(ctx, owner, "wireguard-tunnel")
				if readyErr != nil {
					return readyErr
				}
				if ready {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("edge VIP and WireGuard ownership did not move away from %s: %w", targetNodeName, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (r *kubernetesClusterStepRunner) runtimeRoleOwners(ctx context.Context, db *sql.DB, now int64) (map[string]string, error) {
	etcdOwner, err := r.etcdLeaderOwner(ctx)
	if err != nil {
		return nil, err
	}
	controlOwner, err := r.kubernetesLeaseOwner(ctx, "borealis-control-vip")
	if err != nil {
		return nil, err
	}
	edgeOwner, err := r.kubernetesLeaseOwner(ctx, "borealis-edge-vip")
	if err != nil {
		return nil, err
	}
	clusterPath := fmt.Sprintf("/apis/postgresql.cnpg.io/v1/namespaces/%s/clusters/borealis-postgres", r.namespace)
	var cluster map[string]any
	if err := r.kube.getJSON(ctx, clusterPath, &cluster); err != nil {
		return nil, err
	}
	primaryPod := cleanText(nestedMap(cluster, "status")["currentPrimary"])
	postgresOwner, err := r.kubernetesPodNode(ctx, r.namespace, primaryPod)
	if err != nil {
		return nil, err
	}
	schedulerOwner := ""
	if db != nil {
		conn, connErr := db.Conn(ctx)
		if connErr != nil {
			return nil, connErr
		}
		var holder string
		queryErr := conn.QueryRowContext(ctx, `SELECT holder FROM engine.cluster_application_leases WHERE name='scheduler-leader' AND expires_at >= $1`, now).Scan(&holder)
		conn.Close()
		if queryErr != nil && !errors.Is(queryErr, sql.ErrNoRows) {
			return nil, queryErr
		}
		if queryErr == nil {
			schedulerOwner, err = r.kubernetesPodNode(ctx, r.namespace, holder)
			if err != nil {
				return nil, err
			}
		}
	}
	wireGuardOwner := ""
	if ready, readyErr := r.nodeWorkloadReady(ctx, edgeOwner, "wireguard-tunnel"); readyErr != nil {
		return nil, readyErr
	} else if ready {
		wireGuardOwner = edgeOwner
	}
	return map[string]string{
		"etcd_leader":       etcdOwner,
		"control_vip_owner": controlOwner,
		"edge_vip_owner":    edgeOwner,
		"postgres_primary":  postgresOwner,
		"scheduler_leader":  schedulerOwner,
		"wireguard_owner":   wireGuardOwner,
	}, nil
}

func (r *kubernetesClusterStepRunner) etcdLeaderOwner(ctx context.Context) (string, error) {
	var payload map[string]any
	path := "/api/v1/nodes?labelSelector=borealis.io%2Fetcd-leader%3Dtrue"
	if err := r.kube.getJSON(ctx, path, &payload); err != nil {
		return "", err
	}
	readyLeaders := make([]map[string]any, 0, 1)
	for _, raw := range anySlice(payload["items"]) {
		node, _ := raw.(map[string]any)
		if kubernetesNodeReady(node) {
			readyLeaders = append(readyLeaders, node)
		}
	}
	if len(readyLeaders) != 1 {
		return "", fmt.Errorf("expected one ready observed etcd leader, found %d", len(readyLeaders))
	}
	node := readyLeaders[0]
	name := cleanText(nestedMap(node, "metadata")["name"])
	if !clusterControllerNodeRegex.MatchString(name) {
		return "", errors.New("observed etcd leader has invalid node name")
	}
	return name, nil
}

func kubernetesNodeReady(node map[string]any) bool {
	for _, raw := range anySlice(nestedMap(node, "status")["conditions"]) {
		condition, _ := raw.(map[string]any)
		if cleanText(condition["type"]) == "Ready" {
			return cleanText(condition["status"]) == "True"
		}
	}
	return false
}

func clusterNodeRuntimeState(node clusterControllerNode, kubernetesNode map[string]any) (map[string]any, map[string]any, error) {
	if !clusterUUIDRE.MatchString(node.ID) || !clusterControllerNodeRegex.MatchString(node.Name) {
		return nil, nil, errors.New("cluster node runtime identity is invalid")
	}
	desiredApplicationState := cleanText(node.ApplicationState)
	if !textInSet(desiredApplicationState, "active", "drained", "standby") {
		desiredApplicationState = "standby"
	}
	spec := map[string]any{
		"nodeID":                  node.ID,
		"nodeName":                node.Name,
		"desiredApplicationState": desiredApplicationState,
	}
	if clusterReleaseRE.MatchString(node.ReleaseTag) {
		spec["desiredRelease"] = node.ReleaseTag
	}
	if clusterControllerSHARegex.MatchString(node.ReleaseSHA) {
		spec["desiredSHA"] = node.ReleaseSHA
	}
	observedApplicationState := cleanText(nestedMap(nestedMap(kubernetesNode, "metadata"), "labels")["borealis.io/application-state"])
	if !textInSet(observedApplicationState, "active", "drained", "standby") {
		observedApplicationState = desiredApplicationState
	}
	conditionStatus := "False"
	conditionReason := "KubernetesNodeNotReady"
	if kubernetesNodeReady(kubernetesNode) {
		conditionStatus = "True"
		conditionReason = "KubernetesNodeReady"
	}
	status := map[string]any{
		"observedApplicationState": observedApplicationState,
		"nodeReady":                conditionStatus == "True",
		"releaseTag":               node.ReleaseTag,
		"releaseSHA":               node.ReleaseSHA,
		"drainReason":              node.DrainReason,
		"roles":                    copyMap(node.Roles),
		"probeHealth":              copyMap(node.ProbeHealth),
		"conditions": []any{map[string]any{
			"type":   "Ready",
			"status": conditionStatus,
			"reason": conditionReason,
		}},
	}
	return spec, status, nil
}

func clusterRuntimeMapEqual(current, desired map[string]any, ignoredKeys ...string) bool {
	left := copyMap(current)
	right := copyMap(desired)
	for _, key := range ignoredKeys {
		delete(left, key)
		delete(right, key)
	}
	return marshalClusterJSON(left) == marshalClusterJSON(right)
}

func clusterMergePatchMap(current, desired map[string]any) map[string]any {
	patch := copyMap(desired)
	for key := range current {
		if _, present := desired[key]; !present {
			patch[key] = nil
		}
	}
	return patch
}

func (r *kubernetesClusterStepRunner) reconcileNodeRuntime(ctx context.Context, node clusterControllerNode, kubernetesNode map[string]any, observedAt int64) error {
	spec, status, err := clusterNodeRuntimeState(node, kubernetesNode)
	if err != nil {
		return err
	}
	return r.reconcileNamespacedClusterResource(
		ctx,
		"borealisnoderuntimes",
		node.Name,
		"BorealisNodeRuntime",
		map[string]string{
			"app.kubernetes.io/name":    "borealis-node-runtime",
			"app.kubernetes.io/part-of": "borealis",
			"borealis.io/node-id":       node.ID,
		},
		spec,
		status,
		observedAt,
	)
}

func clusterResourceState(state clusterControllerState) (map[string]any, map[string]any, error) {
	if !clusterUUIDRE.MatchString(state.ClusterID) || !state.Enabled {
		return nil, nil, errors.New("cluster custom-resource identity is invalid")
	}
	supportedMembership := (state.ActiveSize == 1 && (state.DesiredSize == 1 || state.DesiredSize == 3)) || (state.ActiveSize == 3 && state.DesiredSize == 3)
	replacementRecovery := state.ActiveSize == 2 && state.DesiredSize == 3 && state.Status == "Degraded Quorum"
	if !supportedMembership && !replacementRecovery {
		return nil, nil, errors.New("cluster custom-resource size is invalid")
	}
	controlVIP := net.ParseIP(state.ControlPlaneVIP)
	edgeVIP := net.ParseIP(state.EdgeVIP)
	if len(validateClusterIP("control_plane_vip", state.ControlPlaneVIP)) != 0 || len(validateClusterIP("edge_vip", state.EdgeVIP)) != 0 || controlVIP.Equal(edgeVIP) {
		return nil, nil, errors.New("cluster custom-resource VIPs are invalid")
	}
	spec := map[string]any{
		"activeSize":      state.ActiveSize,
		"desiredSize":     state.DesiredSize,
		"controlPlaneVIP": state.ControlPlaneVIP,
		"edgeVIP":         state.EdgeVIP,
	}
	if clusterReleaseRE.MatchString(state.BaselineRelease) {
		spec["baselineRelease"] = state.BaselineRelease
	}
	if clusterControllerSHARegex.MatchString(state.BaselineSHA) {
		spec["baselineSHA"] = state.BaselineSHA
	}
	if clusterUUIDRE.MatchString(state.HMRNodeID) {
		spec["hmrNodeID"] = state.HMRNodeID
	}
	ready := textInSet(state.Status, "Healthy", "Mixed Version", "HMR Non-HA")
	conditionStatus := "False"
	conditionReason := "ClusterNotReady"
	if ready {
		conditionStatus = "True"
		conditionReason = "ClusterServing"
	}
	status := map[string]any{
		"clusterID":     state.ClusterID,
		"phase":         state.Status,
		"activeSize":    state.ActiveSize,
		"desiredSize":   state.DesiredSize,
		"hmrState":      state.HMRState,
		"configuration": copyMap(state.Config),
		"conditions": []any{map[string]any{
			"type":   "Ready",
			"status": conditionStatus,
			"reason": conditionReason,
		}},
	}
	if clusterUUIDRE.MatchString(state.HMRNodeID) {
		status["hmrNodeID"] = state.HMRNodeID
	}
	if clusterUUIDRE.MatchString(state.ActiveOperation) {
		status["activeOperationID"] = state.ActiveOperation
	}
	return spec, status, nil
}

func (r *kubernetesClusterStepRunner) reconcileClusterResource(ctx context.Context, state clusterControllerState, observedAt int64) error {
	spec, status, err := clusterResourceState(state)
	if err != nil {
		return err
	}
	return r.reconcileNamespacedClusterResource(
		ctx,
		"borealisclusters",
		"borealis",
		"BorealisCluster",
		map[string]string{
			"app.kubernetes.io/name":    "borealis-cluster",
			"app.kubernetes.io/part-of": "borealis",
		},
		spec,
		status,
		observedAt,
	)
}

func clusterAdmissionResourceState(admission clusterControllerAdmission) (map[string]any, map[string]any, error) {
	if !clusterUUIDRE.MatchString(admission.ID) || len(validateClusterNodeName("node_name", admission.NodeName)) != 0 {
		return nil, nil, errors.New("cluster admission custom-resource identity is invalid")
	}
	if err := validateHostInput("hostname", admission.Hostname); err != nil || len(admission.Hostname) > clusterHostnameMaxLength {
		return nil, nil, errors.New("cluster admission hostname is invalid")
	}
	if len(validateClusterIP("management_ip", admission.ManagementIP)) != 0 {
		return nil, nil, errors.New("cluster admission management IP is invalid")
	}
	if !textInSet(admission.Architecture, "amd64", "arm64") || admission.OSVersion == "" || len(admission.OSVersion) > 64 {
		return nil, nil, errors.New("cluster admission platform is invalid")
	}
	spec := map[string]any{
		"admissionID":  admission.ID,
		"nodeName":     admission.NodeName,
		"hostname":     admission.Hostname,
		"managementIP": admission.ManagementIP,
		"architecture": admission.Architecture,
		"osVersion":    admission.OSVersion,
	}
	status := map[string]any{
		"state": admission.State,
	}
	if admission.ApprovedBy != "" {
		status["approvedBy"] = admission.ApprovedBy
	}
	if admission.ApprovedAt > 0 {
		status["approvedAt"] = admission.ApprovedAt
	}
	return spec, status, nil
}

func (r *kubernetesClusterStepRunner) reconcileAdmissionResource(ctx context.Context, admission clusterControllerAdmission, observedAt int64) error {
	spec, status, err := clusterAdmissionResourceState(admission)
	if err != nil {
		return err
	}
	return r.reconcileNamespacedClusterResource(
		ctx,
		"borealisnodeadmissions",
		admission.ID,
		"BorealisNodeAdmission",
		map[string]string{
			"app.kubernetes.io/name":    "borealis-node-admission",
			"app.kubernetes.io/part-of": "borealis",
			"borealis.io/node-name":     admission.NodeName,
		},
		spec,
		status,
		observedAt,
	)
}

func clusterOperationResourceState(resource clusterControllerOperationResource) (map[string]any, map[string]any, error) {
	operation := resource.Operation
	if !clusterUUIDRE.MatchString(operation.ID) || operation.Kind == "" || len(operation.Kind) > 64 || operation.CurrentStep == "" || len(operation.CurrentStep) > 128 {
		return nil, nil, errors.New("cluster operation custom-resource identity is invalid")
	}
	spec := map[string]any{
		"operationID": operation.ID,
		"kind":        operation.Kind,
		"step":        operation.CurrentStep,
	}
	if clusterUUIDRE.MatchString(operation.TargetNodeID) {
		spec["targetNodeID"] = operation.TargetNodeID
	}
	if clusterReleaseRE.MatchString(operation.TargetRelease) {
		spec["targetRelease"] = operation.TargetRelease
	}
	if clusterControllerSHARegex.MatchString(operation.TargetSHA) {
		spec["targetSHA"] = operation.TargetSHA
	}
	status := map[string]any{
		"state":   operation.State,
		"attempt": operation.Attempt,
	}
	if resource.ErrorText != "" {
		status["error"] = resource.ErrorText
	}
	return spec, status, nil
}

func (r *kubernetesClusterStepRunner) reconcileOperationResource(ctx context.Context, resource clusterControllerOperationResource, observedAt int64) error {
	spec, status, err := clusterOperationResourceState(resource)
	if err != nil {
		return err
	}
	return r.reconcileNamespacedClusterResource(
		ctx,
		"borealisclusteroperations",
		resource.Operation.ID,
		"BorealisClusterOperation",
		map[string]string{
			"app.kubernetes.io/name":     "borealis-cluster-operation",
			"app.kubernetes.io/part-of":  "borealis",
			"borealis.io/operation-kind": clusterActionStepLabel(resource.Operation.Kind),
		},
		spec,
		status,
		observedAt,
	)
}

func (r *kubernetesClusterStepRunner) reconcileNamespacedClusterResource(
	ctx context.Context,
	resource string,
	name string,
	kind string,
	labels map[string]string,
	spec map[string]any,
	status map[string]any,
	observedAt int64,
) error {
	path := fmt.Sprintf("/apis/engine.borealis.io/v1alpha1/namespaces/%s/%s/%s", r.namespace, resource, name)
	collectionPath := fmt.Sprintf("/apis/engine.borealis.io/v1alpha1/namespaces/%s/%s", r.namespace, resource)
	var existing map[string]any
	err := r.kube.getJSON(ctx, path, &existing)
	if err != nil && !strings.Contains(err.Error(), "returned HTTP 404") {
		return err
	}
	if err != nil {
		manifest := map[string]any{
			"apiVersion": "engine.borealis.io/v1alpha1",
			"kind":       kind,
			"metadata": map[string]any{
				"name":      name,
				"namespace": r.namespace,
				"labels":    labels,
			},
			"spec": spec,
		}
		if err := r.kube.doJSON(ctx, http.MethodPost, collectionPath, manifest, "application/json", &existing, 30*time.Second); err != nil {
			return err
		}
	} else if !clusterRuntimeMapEqual(nestedMap(existing, "spec"), spec) {
		specPatch := clusterMergePatchMap(nestedMap(existing, "spec"), spec)
		if err := r.kube.doJSON(ctx, http.MethodPatch, path, map[string]any{"spec": specPatch}, "application/merge-patch+json", &existing, 30*time.Second); err != nil {
			return err
		}
	}
	currentStatus := nestedMap(existing, "status")
	if clusterRuntimeMapEqual(currentStatus, status, "observedAt") {
		return nil
	}
	status = copyMap(status)
	status["observedAt"] = observedAt
	status = clusterMergePatchMap(currentStatus, status)
	var updated map[string]any
	return r.kube.doJSON(ctx, http.MethodPatch, path+"/status", map[string]any{"status": status}, "application/merge-patch+json", &updated, 30*time.Second)
}

func (r *kubernetesClusterStepRunner) kubernetesLeaseOwner(ctx context.Context, name string) (string, error) {
	if name != "borealis-control-vip" && name != "borealis-edge-vip" {
		return "", errors.New("unsupported cluster lease")
	}
	var lease map[string]any
	path := "/apis/coordination.k8s.io/v1/namespaces/kube-system/leases/" + name
	if err := r.kube.getJSON(ctx, path, &lease); err != nil {
		return "", err
	}
	owner := cleanText(nestedMap(lease, "spec")["holderIdentity"])
	if !clusterControllerNodeRegex.MatchString(owner) {
		return "", fmt.Errorf("%s has no valid holder", name)
	}
	return owner, nil
}

func (r *kubernetesClusterStepRunner) kubernetesPodNode(ctx context.Context, namespace, podName string) (string, error) {
	if !clusterControllerNodeRegex.MatchString(podName) || !clusterControllerNodeRegex.MatchString(namespace) {
		return "", errors.New("runtime role pod identity is invalid")
	}
	var pod map[string]any
	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s", namespace, podName)
	if err := r.kube.getJSON(ctx, path, &pod); err != nil {
		return "", err
	}
	nodeName := cleanText(nestedMap(pod, "spec")["nodeName"])
	if !clusterControllerNodeRegex.MatchString(nodeName) {
		return "", errors.New("runtime role pod has no valid node")
	}
	return nodeName, nil
}

func (r *kubernetesClusterStepRunner) nodeWorkloadReady(ctx context.Context, nodeName, appName string) (bool, error) {
	if !clusterControllerNodeRegex.MatchString(nodeName) || !clusterControllerNodeRegex.MatchString(appName) {
		return false, errors.New("node workload identity is invalid")
	}
	selector := "borealis.io/engine-node=" + nodeName + ",borealis.io/node-workload=true,app.kubernetes.io/name=" + appName
	path := fmt.Sprintf("/apis/apps/v1/namespaces/%s/deployments?labelSelector=%s", r.namespace, url.QueryEscape(selector))
	var deployments map[string]any
	if err := r.kube.getJSON(ctx, path, &deployments); err != nil {
		return false, err
	}
	items := anySlice(deployments["items"])
	if len(items) != 1 {
		return false, nil
	}
	deployment, _ := items[0].(map[string]any)
	metadata := nestedMap(deployment, "metadata")
	spec := nestedMap(deployment, "spec")
	status := nestedMap(deployment, "status")
	replicas := coerceInt64(spec["replicas"])
	return replicas == 1 &&
		coerceInt64(status["observedGeneration"]) >= coerceInt64(metadata["generation"]) &&
		coerceInt64(status["availableReplicas"]) >= replicas &&
		coerceInt64(status["readyReplicas"]) >= replicas &&
		coerceInt64(status["updatedReplicas"]) >= replicas, nil
}

func (r *kubernetesClusterStepRunner) scaleNodeWorkload(ctx context.Context, nodeName, appName string, active bool) error {
	return r.scaleNodeWorkloadWithReadiness(ctx, nodeName, appName, active, active)
}

func (r *kubernetesClusterStepRunner) scaleNodeWorkloadWithReadiness(ctx context.Context, nodeName, appName string, active, waitForReady bool) error {
	selector := "borealis.io/engine-node=" + nodeName + ",borealis.io/node-workload=true,app.kubernetes.io/name=" + appName
	path := fmt.Sprintf("/apis/apps/v1/namespaces/%s/deployments?labelSelector=%s", r.namespace, url.QueryEscape(selector))
	var deployments map[string]any
	if err := r.kube.getJSON(ctx, path, &deployments); err != nil {
		return err
	}
	items := anySlice(deployments["items"])
	if len(items) != 1 {
		return fmt.Errorf("expected one %s workload on node %s, found %d", appName, nodeName, len(items))
	}
	metadata := nestedMap(items[0].(map[string]any), "metadata")
	name := cleanText(metadata["name"])
	if !clusterControllerNodeRegex.MatchString(name) {
		return errors.New("invalid node workload deployment name")
	}
	replicas := int64(0)
	if active {
		replicas = 1
	}
	var result map[string]any
	path = fmt.Sprintf("/apis/apps/v1/namespaces/%s/deployments/%s", r.namespace, name)
	if err := r.kube.doJSON(ctx, http.MethodPatch, path, map[string]any{"spec": map[string]any{"replicas": replicas}}, "application/merge-patch+json", &result, 30*time.Second); err != nil {
		return err
	}
	if !active || !waitForReady {
		return nil
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if err := r.kube.getJSON(ctx, path, &result); err != nil {
			return err
		}
		spec := nestedMap(result, "spec")
		status := nestedMap(result, "status")
		metadata := nestedMap(result, "metadata")
		if coerceInt64(status["observedGeneration"]) >= coerceInt64(metadata["generation"]) &&
			coerceInt64(status["availableReplicas"]) >= coerceInt64(spec["replicas"]) &&
			coerceInt64(status["readyReplicas"]) >= coerceInt64(spec["replicas"]) &&
			coerceInt64(status["updatedReplicas"]) >= coerceInt64(spec["replicas"]) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s workload on node %s did not become available: %w", appName, nodeName, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (r *kubernetesClusterStepRunner) ensurePostgresPrimaryOnNode(ctx context.Context, nodeName string) error {
	if !clusterControllerNodeRegex.MatchString(nodeName) {
		return errors.New("invalid PostgreSQL target node")
	}
	clusterPath := fmt.Sprintf("/apis/postgresql.cnpg.io/v1/namespaces/%s/clusters/borealis-postgres", r.namespace)
	var cluster map[string]any
	if err := r.kube.getJSON(ctx, clusterPath, &cluster); err != nil {
		return err
	}
	status := nestedMap(cluster, "status")
	current := cleanText(status["currentPrimary"])
	podsPath := fmt.Sprintf("/api/v1/namespaces/%s/pods?labelSelector=cnpg.io%%2Fcluster%%3Dborealis-postgres&fieldSelector=spec.nodeName%%3D%s", r.namespace, nodeName)
	var pods map[string]any
	if err := r.kube.getJSON(ctx, podsPath, &pods); err != nil {
		return err
	}
	target := ""
	for _, raw := range anySlice(pods["items"]) {
		pod, _ := raw.(map[string]any)
		metadata := nestedMap(pod, "metadata")
		candidate := cleanText(metadata["name"])
		if candidate == "" || !podReady(pod) {
			continue
		}
		if candidate == current {
			return nil
		}
		target = candidate
		break
	}
	if target == "" {
		return fmt.Errorf("healthy CloudNativePG replica is unavailable on node %s", nodeName)
	}
	if err := r.waitPostgresReplicaSynchronized(ctx, target); err != nil {
		return err
	}
	patch := map[string]any{"status": map[string]any{
		"targetPrimary":          target,
		"targetPrimaryTimestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"phase":                  "Switchover",
		"phaseReason":            "Borealis cluster-aware role transfer to " + target,
	}}
	var output map[string]any
	if err := r.kube.doJSON(ctx, http.MethodPatch, clusterPath+"/status", patch, "application/merge-patch+json", &output, 30*time.Second); err != nil {
		return err
	}
	return r.waitPostgresClusterSynchronized(ctx, target)
}

func (r *kubernetesClusterStepRunner) waitPostgresReplicaSynchronized(ctx context.Context, target string) error {
	if !clusterControllerNodeRegex.MatchString(target) {
		return errors.New("invalid CloudNativePG replica identity")
	}
	pollInterval := r.jobPollInterval
	if pollInterval <= 0 {
		pollInterval = time.Second
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	var lastErr error
	for {
		ready, err := r.postgresReplicationReady(ctx, target, 0)
		if err == nil && ready {
			return nil
		}
		if err != nil {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			message := fmt.Errorf("CloudNativePG replica %s did not become a synchronized streaming quorum candidate: %w", target, ctx.Err())
			if lastErr != nil {
				return errors.Join(message, lastErr)
			}
			return message
		case <-ticker.C:
		}
	}
}

func (r *kubernetesClusterStepRunner) waitPostgresClusterSynchronizedOnNode(ctx context.Context, nodeName string) error {
	if !clusterControllerNodeRegex.MatchString(nodeName) {
		return errors.New("invalid CloudNativePG primary node")
	}
	clusterPath := fmt.Sprintf("/apis/postgresql.cnpg.io/v1/namespaces/%s/clusters/borealis-postgres", r.namespace)
	var cluster map[string]any
	if err := r.kube.getJSON(ctx, clusterPath, &cluster); err != nil {
		return err
	}
	primaryPod := cleanText(nestedMap(cluster, "status")["currentPrimary"])
	primaryNode, err := r.kubernetesPodNode(ctx, r.namespace, primaryPod)
	if err != nil {
		return err
	}
	if primaryNode != nodeName {
		return fmt.Errorf("CloudNativePG primary %s is on %s instead of %s", primaryPod, primaryNode, nodeName)
	}
	return r.waitPostgresClusterSynchronized(ctx, primaryPod)
}

func (r *kubernetesClusterStepRunner) waitPostgresClusterSynchronized(ctx context.Context, target string) error {
	if !clusterControllerNodeRegex.MatchString(target) {
		return errors.New("invalid CloudNativePG primary identity")
	}
	clusterPath := fmt.Sprintf("/apis/postgresql.cnpg.io/v1/namespaces/%s/clusters/borealis-postgres", r.namespace)
	pollInterval := r.jobPollInterval
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	lastState := "unknown"
	var lastErr error
	for {
		var cluster map[string]any
		if err := r.kube.getJSON(ctx, clusterPath, &cluster); err != nil {
			lastErr = err
		} else {
			spec := nestedMap(cluster, "spec")
			status := nestedMap(cluster, "status")
			instances := coerceInt64(spec["instances"])
			if instances == 0 {
				instances = coerceInt64(status["instances"])
			}
			readyInstances := coerceInt64(status["readyInstances"])
			current := cleanText(status["currentPrimary"])
			requested := cleanText(status["targetPrimary"])
			phase := cleanText(status["phase"])
			lastState = fmt.Sprintf("phase=%s current=%s target=%s ready=%d/%d", phase, current, requested, readyInstances, instances)
			if instances > 0 && readyInstances == instances && current == target && requested == target && strings.EqualFold(phase, "Cluster in healthy state") {
				replicationReady, probeErr := r.postgresReplicationReady(ctx, "", instances-1)
				if probeErr != nil {
					lastErr = probeErr
				} else if replicationReady {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			message := fmt.Errorf("CloudNativePG did not reach synchronized healthy state on %s (%s): %w", target, lastState, ctx.Err())
			if lastErr != nil {
				return errors.Join(message, lastErr)
			}
			return message
		case <-ticker.C:
		}
	}
}

func (r *kubernetesClusterStepRunner) postgresReplicationReady(ctx context.Context, target string, expectedReplicas int64) (bool, error) {
	if r.postgresReplicationProbe != nil {
		return r.postgresReplicationProbe(ctx, target, expectedReplicas)
	}
	if r.db == nil {
		return false, errors.New("PostgreSQL replication probe database is unavailable")
	}
	if target == "" && expectedReplicas <= 0 {
		return true, nil
	}
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return false, err
	}
	if target != "" {
		var state, syncState string
		var flushCaughtUp bool
		queryErr := conn.QueryRowContext(ctx, `
			SELECT state,
			       sync_state,
			       COALESCE(flush_lsn >= pg_current_wal_flush_lsn(), FALSE)
			FROM pg_stat_replication
			WHERE application_name=$1
		`, target).Scan(&state, &syncState, &flushCaughtUp)
		closeErr := conn.Close()
		if errors.Is(queryErr, sql.ErrNoRows) {
			return false, closeErr
		}
		if queryErr != nil {
			return false, queryErr
		}
		if closeErr != nil {
			return false, closeErr
		}
		return state == "streaming" && (syncState == "sync" || syncState == "quorum") && flushCaughtUp, nil
	}
	var caughtUp, synchronous int64
	queryErr := conn.QueryRowContext(ctx, `
		SELECT COUNT(*) FILTER (
		           WHERE state='streaming'
		             AND flush_lsn IS NOT NULL
		             AND flush_lsn >= pg_current_wal_flush_lsn()
		       ),
		       COUNT(*) FILTER (
		           WHERE state='streaming'
		             AND sync_state IN ('sync','quorum')
		             AND flush_lsn IS NOT NULL
		             AND flush_lsn >= pg_current_wal_flush_lsn()
		       )
		FROM pg_stat_replication
	`).Scan(&caughtUp, &synchronous)
	closeErr := conn.Close()
	if queryErr != nil {
		return false, queryErr
	}
	if closeErr != nil {
		return false, closeErr
	}
	return caughtUp >= expectedReplicas && synchronous >= 1, nil
}

func podReady(pod map[string]any) bool {
	for _, raw := range anySlice(nestedMap(pod, "status")["conditions"]) {
		condition, _ := raw.(map[string]any)
		if cleanText(condition["type"]) == "Ready" {
			return strings.EqualFold(cleanText(condition["status"]), "True")
		}
	}
	return false
}

func (r *kubernetesClusterStepRunner) patchNodeLabels(ctx context.Context, nodeName string, labels map[string]string) error {
	values := make(map[string]any, len(labels))
	for key, value := range labels {
		values[key] = value
	}
	return r.patchNodeLabelValues(ctx, nodeName, values)
}

func (r *kubernetesClusterStepRunner) patchNodeLabelValues(ctx context.Context, nodeName string, labels map[string]any) error {
	if !clusterControllerNodeRegex.MatchString(nodeName) {
		return errors.New("invalid Kubernetes node name")
	}
	var output map[string]any
	return r.kube.doJSON(ctx, http.MethodPatch, "/api/v1/nodes/"+nodeName, map[string]any{"metadata": map[string]any{"labels": labels}}, "application/strategic-merge-patch+json", &output, 30*time.Second)
}

func (r *kubernetesClusterStepRunner) nodeActionJob(ctx context.Context, operation clusterControllerOperation, step clusterControllerStep, node clusterControllerNode, verb string) error {
	if !borealisOperatorImmutableImageRefPattern.MatchString(r.actionImage) {
		return errors.New("BOREALIS_CLUSTER_ACTION_IMAGE must be immutable")
	}
	if node.ID == "" || !clusterControllerNodeRegex.MatchString(node.Name) {
		return errors.New("node action target is invalid")
	}
	jobName := clusterActionJobName(operation.ID, fmt.Sprintf("attempt:%d:%s", operation.Attempt, step.Name))
	path := fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs/%s", r.namespace, jobName)
	args := clusterNodeActionArgs(operation, verb)
	manifest := clusterActionJobManifest(jobName, r.namespace, node.Name, r.actionImage, args, operation.ID, step.Name)
	var existing map[string]any
	if err := r.kube.getJSON(ctx, path, &existing); err != nil {
		if !kubernetesAPIErrorHasStatus(err, http.StatusNotFound) {
			return err
		}
		if err := r.kube.doJSON(ctx, http.MethodPost, fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs", r.namespace), manifest, "application/json", &existing, 30*time.Second); err != nil {
			if !kubernetesAPIErrorHasStatus(err, http.StatusConflict) {
				return err
			}
			if err := r.kube.getJSON(ctx, path, &existing); err != nil {
				return fmt.Errorf("node action Job %s collision could not be inspected: %w", jobName, err)
			}
		}
	}
	if err := validateClusterActionJobIdentity(existing, manifest); err != nil {
		return fmt.Errorf("node action Job %s does not match requested immutable action: %w", jobName, err)
	}
	if verb == "EnrollCluster" {
		return r.waitClusterInitJob(ctx, jobName)
	}
	return r.waitJob(ctx, jobName)
}

func clusterNodeActionArgs(operation clusterControllerOperation, verb string) []string {
	args := []string{"client", "--verb", verb}
	if verb == "PreflightRelease" || verb == "FetchRelease" || verb == "StagePinnedRelease" {
		args = append(args, "--release-tag", operation.TargetRelease, "--target-sha", operation.TargetSHA)
	}
	if verb == "StageRevisionImages" || verb == "RedeployRevision" || verb == "RedeployStagedRevision" || verb == "PromoteCandidate" {
		args = append(args, "--target-sha", operation.TargetSHA)
	}
	if verb == "RunSchemaPhase" {
		args = append(args, "--schema-phase", cleanText(operation.Payload["schema_phase"]), "--target-sha", operation.TargetSHA)
	}
	if verb == "RunK3sProbeConformance" {
		args = append(args, "--k3s-version", operation.TargetRelease)
	}
	if verb == "EnterApplicationDrain" || verb == "PrepareMemberRemoval" {
		args = append(args, "--reason", firstText(cleanText(operation.Payload["reason"]), operation.Kind))
	}
	if verb == "EnrollCluster" {
		args = append(args,
			"--control-plane-vip", cleanText(operation.Payload["control_plane_vip"]),
			"--edge-vip", cleanText(operation.Payload["edge_vip"]),
			"--target-sha", cleanText(operation.Payload["baseline_sha"]),
		)
	}
	return args
}

func (r *kubernetesClusterStepRunner) applyK3sUpgrade(ctx context.Context, operation clusterControllerOperation, node clusterControllerNode) error {
	version := operation.TargetRelease
	imageRef := cleanText(operation.Payload["upgrade_image"])
	if !clusterK3sRE.MatchString(version) || !borealisOperatorImmutableImageRefPattern.MatchString(imageRef) {
		return errors.New("K3s Plan requires stable version and immutable upgrade image")
	}
	attemptKey := clusterOperationAttemptKey(operation)
	planName := clusterK3sPlanName(attemptKey, node.Name)
	planPath := fmt.Sprintf("/apis/upgrade.cattle.io/v1/namespaces/%s/plans/%s", clusterUpgradeNamespace, planName)
	manifest := map[string]any{
		"apiVersion": "upgrade.cattle.io/v1",
		"kind":       "Plan",
		"metadata": map[string]any{
			"name":      planName,
			"namespace": clusterUpgradeNamespace,
			"labels": map[string]string{
				"app.kubernetes.io/name":   "borealis-k3s-upgrade",
				"borealis.io/operation-id": operation.ID,
			},
		},
		"spec": map[string]any{
			"concurrency":           1,
			"exclusive":             true,
			"cordon":                true,
			"jobActiveDeadlineSecs": 1800,
			"version":               version,
			"serviceAccountName":    "system-upgrade",
			"nodeSelector": map[string]any{"matchExpressions": []any{map[string]any{
				"key": "borealis.io/k3s-upgrade-operation", "operator": "In", "values": []string{attemptKey},
			}}},
			"postCompleteLabels": map[string]string{"borealis.io/k3s-version": version},
			"upgrade":            map[string]any{"image": imageRef},
		},
	}
	var existing map[string]any
	if err := r.kube.getJSON(ctx, planPath, &existing); err != nil {
		if !strings.Contains(err.Error(), "returned HTTP 404") {
			return err
		}
		if err := r.kube.doJSON(ctx, http.MethodPost, fmt.Sprintf("/apis/upgrade.cattle.io/v1/namespaces/%s/plans", clusterUpgradeNamespace), manifest, "application/json", &existing, 30*time.Second); err != nil {
			return err
		}
	} else if cleanText(nestedMap(nestedMap(existing, "metadata"), "labels")["borealis.io/operation-id"]) != operation.ID {
		return errors.New("K3s upgrade Plan name collision")
	}
	if err := r.patchNodeLabelValues(ctx, node.Name, map[string]any{"borealis.io/k3s-upgrade-operation": attemptKey}); err != nil {
		return err
	}
	return r.waitK3sUpgradePlan(ctx, planName, node.Name, version)
}

func clusterK3sPlanName(operationID, nodeName string) string {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(operationID + ":" + nodeName))
	prefix := strings.ReplaceAll(operationID, "-", "")
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	return fmt.Sprintf("borealis-k3s-%s-%08x", prefix, hash.Sum32())
}

func clusterOperationAttemptKey(operation clusterControllerOperation) string {
	attempt := operation.Attempt
	if attempt < 1 {
		attempt = 1
	}
	return fmt.Sprintf("%s-attempt-%d", operation.ID, attempt)
}

func (r *kubernetesClusterStepRunner) waitK3sUpgradePlan(ctx context.Context, planName, nodeName, version string) error {
	planPath := fmt.Sprintf("/apis/upgrade.cattle.io/v1/namespaces/%s/plans/%s", clusterUpgradeNamespace, planName)
	jobsPath := fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs?labelSelector=%s", clusterUpgradeNamespace, url.QueryEscape("upgrade.cattle.io/plan="+planName))
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		var plan map[string]any
		if err := r.kube.getJSON(ctx, planPath, &plan); err != nil {
			return err
		}
		status := nestedMap(plan, "status")
		if cleanText(status["latestVersion"]) == version {
			for _, raw := range anySlice(status["conditions"]) {
				condition, _ := raw.(map[string]any)
				conditionType := cleanText(condition["type"])
				conditionStatus := cleanText(condition["status"])
				if conditionType == "Validated" && conditionStatus == "False" {
					return fmt.Errorf("K3s upgrade Plan validation failed: %s", firstText(cleanText(condition["message"]), cleanText(condition["reason"])))
				}
				if conditionType == "Complete" && conditionStatus == "True" {
					return r.verifyK3sNodeVersion(ctx, nodeName, version)
				}
			}
		}
		var jobs map[string]any
		if err := r.kube.getJSON(ctx, jobsPath, &jobs); err != nil {
			return err
		}
		for _, raw := range anySlice(jobs["items"]) {
			job, _ := raw.(map[string]any)
			if coerceInt64(nestedMap(job, "status")["failed"]) > 0 {
				return fmt.Errorf("K3s upgrade Plan %s has failed Job", planName)
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("K3s upgrade Plan %s did not complete: %w", planName, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (r *kubernetesClusterStepRunner) verifyK3sNodeVersion(ctx context.Context, nodeName, version string) error {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		var node map[string]any
		if err := r.kube.getJSON(ctx, "/api/v1/nodes/"+nodeName, &node); err == nil {
			nodeInfo := nestedMap(nestedMap(node, "status"), "nodeInfo")
			if nodeConditionTrue(node, "Ready") && nodeConditionTrue(node, "EtcdIsVoter") && cleanText(nodeInfo["kubeletVersion"]) == version && nestedMap(node, "spec")["unschedulable"] != true {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("node %s did not return Ready as K3s %s: %w", nodeName, version, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (r *kubernetesClusterStepRunner) cleanupK3sUpgrade(ctx context.Context, operation clusterControllerOperation, nodeName string) error {
	planName := clusterK3sPlanName(clusterOperationAttemptKey(operation), nodeName)
	if err := r.patchNodeLabelValues(ctx, nodeName, map[string]any{"borealis.io/k3s-upgrade-operation": nil}); err != nil {
		return err
	}
	var output map[string]any
	path := fmt.Sprintf("/apis/upgrade.cattle.io/v1/namespaces/%s/plans/%s", clusterUpgradeNamespace, planName)
	if err := r.kube.doJSON(ctx, http.MethodDelete, path, map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "propagationPolicy": "Background"}, "application/json", &output, 30*time.Second); err != nil && !strings.Contains(err.Error(), "returned HTTP 404") {
		return err
	}
	return nil
}

func (r *kubernetesClusterStepRunner) verifyCNPGMigration(ctx context.Context) error {
	var cluster map[string]any
	path := fmt.Sprintf("/apis/postgresql.cnpg.io/v1/namespaces/%s/clusters/borealis-postgres", r.namespace)
	if err := r.kube.getJSON(ctx, path, &cluster); err != nil {
		return err
	}
	status := nestedMap(cluster, "status")
	instances := coerceInt64(status["instances"])
	ready := coerceInt64(status["readyInstances"])
	if instances < 1 || ready != instances {
		return fmt.Errorf("CloudNativePG migration not ready: instances=%d ready=%d", instances, ready)
	}
	var oldStatefulSet map[string]any
	oldPath := fmt.Sprintf("/apis/apps/v1/namespaces/%s/statefulsets/postgres-db", r.namespace)
	if err := r.kube.getJSON(ctx, oldPath, &oldStatefulSet); err == nil && coerceInt64(nestedMap(oldStatefulSet, "spec")["replicas"]) != 0 {
		return errors.New("standalone PostgreSQL StatefulSet remains active after cluster cutover")
	}
	return nil
}

func clusterActionJobName(operationID, step string) string {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(step))
	prefix := strings.ReplaceAll(operationID, "-", "")
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	return fmt.Sprintf("cluster-%s-%08x", prefix, hash.Sum32())
}

func clusterActionStepLabel(step string) string {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(step))
	value := strings.Map(func(char rune) rune {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9', char == '-', char == '_', char == '.':
			return char
		default:
			return '-'
		}
	}, step)
	value = strings.Trim(value, "-_.")
	if value == "" {
		value = "step"
	}
	if len(value) <= 63 {
		return value
	}
	prefix := strings.TrimRight(value[:54], "-_.")
	if prefix == "" {
		prefix = "step"
	}
	return fmt.Sprintf("%s-%08x", prefix, hash.Sum32())
}

func clusterActionJobManifest(name, namespace, nodeName, imageRef string, args []string, operationID, step string) map[string]any {
	return map[string]any{
		"apiVersion": "batch/v1", "kind": "Job",
		"metadata": map[string]any{
			"name": name, "namespace": namespace,
			"labels":      map[string]string{"app.kubernetes.io/name": "borealis-node-action", "borealis.io/operation-id": operationID},
			"annotations": map[string]string{"borealis.io/operation-step": step},
		},
		"spec": map[string]any{"backoffLimit": 0, "ttlSecondsAfterFinished": 86400, "template": map[string]any{
			"metadata": map[string]any{"labels": map[string]string{"app.kubernetes.io/name": "borealis-node-action", "borealis.io/operation-step": clusterActionStepLabel(step)}},
			"spec": map[string]any{
				"restartPolicy": "Never", "nodeName": nodeName, "serviceAccountName": "borealis-cluster-controller", "automountServiceAccountToken": false,
				"containers": []any{map[string]any{
					"name": "action", "image": imageRef, "imagePullPolicy": "IfNotPresent", "command": []string{"/usr/local/bin/borealis-node-manager"}, "args": args,
					"securityContext": map[string]any{"allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true, "runAsNonRoot": true, "runAsUser": 64646, "runAsGroup": 64646, "capabilities": map[string]any{"drop": []string{"ALL"}}},
					"volumeMounts": []any{
						map[string]any{"name": "manager-socket", "mountPath": clusterNodeManagerSocket},
						map[string]any{"name": "manager-token", "mountPath": clusterNodeManagerToken, "readOnly": true},
					},
				}},
				"volumes": []any{
					map[string]any{"name": "manager-socket", "hostPath": map[string]any{"path": clusterNodeManagerSocket, "type": "Socket"}},
					map[string]any{"name": "manager-token", "hostPath": map[string]any{"path": clusterNodeManagerToken, "type": "File"}},
				},
			},
		}},
	}
}

func validateClusterActionJobIdentity(actual, expected map[string]any) error {
	actualMetadata := nestedMap(actual, "metadata")
	expectedMetadata := nestedMap(expected, "metadata")
	for _, key := range []string{"name", "namespace"} {
		if cleanText(actualMetadata[key]) != cleanText(expectedMetadata[key]) {
			return fmt.Errorf("metadata %s mismatch", key)
		}
	}
	actualLabels := clusterStringMap(actualMetadata["labels"])
	expectedLabels := clusterStringMap(expectedMetadata["labels"])
	if actualLabels["borealis.io/operation-id"] != expectedLabels["borealis.io/operation-id"] {
		return errors.New("operation label mismatch")
	}
	actualAnnotations := clusterStringMap(actualMetadata["annotations"])
	expectedAnnotations := clusterStringMap(expectedMetadata["annotations"])
	if actualAnnotations["borealis.io/operation-step"] != expectedAnnotations["borealis.io/operation-step"] {
		return errors.New("operation step annotation mismatch")
	}

	actualTemplate := nestedMap(nestedMap(actual, "spec"), "template")
	expectedTemplate := nestedMap(nestedMap(expected, "spec"), "template")
	actualStepLabels := clusterStringMap(nestedMap(actualTemplate, "metadata")["labels"])
	expectedStepLabels := clusterStringMap(nestedMap(expectedTemplate, "metadata")["labels"])
	if actualStepLabels["borealis.io/operation-step"] != expectedStepLabels["borealis.io/operation-step"] {
		return errors.New("operation step label mismatch")
	}
	actualPod := nestedMap(actualTemplate, "spec")
	expectedPod := nestedMap(expectedTemplate, "spec")
	if cleanText(actualPod["nodeName"]) != cleanText(expectedPod["nodeName"]) {
		return errors.New("target node mismatch")
	}
	if cleanText(actualPod["serviceAccountName"]) != cleanText(expectedPod["serviceAccountName"]) || actualPod["automountServiceAccountToken"] != expectedPod["automountServiceAccountToken"] {
		return errors.New("pod identity boundary mismatch")
	}
	actualContainers := anySlice(actualPod["containers"])
	expectedContainers := anySlice(expectedPod["containers"])
	if len(actualContainers) != 1 || len(expectedContainers) != 1 {
		return errors.New("action container count mismatch")
	}
	actualContainer, actualOK := actualContainers[0].(map[string]any)
	expectedContainer, expectedOK := expectedContainers[0].(map[string]any)
	if !actualOK || !expectedOK || cleanText(actualContainer["name"]) != cleanText(expectedContainer["name"]) {
		return errors.New("action container identity mismatch")
	}
	if cleanText(actualContainer["image"]) != cleanText(expectedContainer["image"]) {
		return errors.New("action image mismatch")
	}
	if !clusterStringSlicesEqual(clusterStringSlice(actualContainer["command"]), clusterStringSlice(expectedContainer["command"])) {
		return errors.New("action command mismatch")
	}
	if !clusterStringSlicesEqual(clusterStringSlice(actualContainer["args"]), clusterStringSlice(expectedContainer["args"])) {
		return errors.New("action arguments mismatch")
	}
	return nil
}

func clusterStringMap(value any) map[string]string {
	result := map[string]string{}
	switch typed := value.(type) {
	case map[string]string:
		for key, item := range typed {
			result[key] = item
		}
	case map[string]any:
		for key, item := range typed {
			result[key] = cleanText(item)
		}
	}
	return result
}

func clusterStringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			result = append(result, cleanText(item))
		}
		return result
	default:
		return nil
	}
}

func clusterStringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (r *kubernetesClusterStepRunner) waitJob(ctx context.Context, name string) error {
	return r.waitJobWithAuthorizationTransition(ctx, name, false)
}

func (r *kubernetesClusterStepRunner) waitClusterInitJob(ctx context.Context, name string) error {
	return r.waitJobWithAuthorizationTransition(ctx, name, true)
}

func (r *kubernetesClusterStepRunner) waitJobWithAuthorizationTransition(ctx context.Context, name string, allowClusterInitAuthorizationTransition bool) error {
	path := fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs/%s", r.namespace, name)
	pollInterval := r.jobPollInterval
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	authorizationGrace := r.clusterInitAuthorizationGrace
	if authorizationGrace <= 0 {
		authorizationGrace = 2 * time.Minute
	}
	var authorizationFailureStarted time.Time
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		var job map[string]any
		if err := r.kube.getJSON(ctx, path, &job); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if allowClusterInitAuthorizationTransition && clusterInitAuthorizationTransitionError(err) {
				if authorizationFailureStarted.IsZero() {
					authorizationFailureStarted = time.Now()
				}
				if time.Since(authorizationFailureStarted) >= authorizationGrace {
					return fmt.Errorf("cluster-init Kubernetes authorization did not recover within %s: %w", authorizationGrace, err)
				}
			} else if !transientKubernetesAPIError(err) {
				return err
			}
		} else {
			authorizationFailureStarted = time.Time{}
			status := nestedMap(job, "status")
			if coerceInt64(status["succeeded"]) > 0 {
				return nil
			}
			if coerceInt64(status["failed"]) > 0 {
				return fmt.Errorf("node action Job %s failed", name)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func clusterInitAuthorizationTransitionError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "returned http 401") || strings.Contains(message, "returned http 403")
}

func transientKubernetesAPIError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, status := range []string{
		"returned http 429",
		"returned http 500",
		"returned http 502",
		"returned http 503",
		"returned http 504",
	} {
		if strings.Contains(message, status) {
			return true
		}
	}
	var networkError net.Error
	if errors.As(err, &networkError) && (networkError.Timeout() || networkError.Temporary()) {
		return true
	}
	for _, fragment := range []string{
		"connection refused",
		"connection reset",
		"connection aborted",
		"server closed idle connection",
		"tls handshake timeout",
		"unexpected eof",
	} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}

func (r *kubernetesClusterStepRunner) waitNodeEndpointsWithdrawn(ctx context.Context, nodeName string) error {
	path := fmt.Sprintf("/apis/discovery.k8s.io/v1/namespaces/%s/endpointslices?labelSelector=app.kubernetes.io%%2Fpart-of%%3Dborealis", r.namespace)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		var payload map[string]any
		if err := r.kube.getJSON(ctx, path, &payload); err != nil {
			return err
		}
		foundReady := false
		for _, item := range anySlice(payload["items"]) {
			slice, _ := item.(map[string]any)
			serviceName := cleanText(nestedMap(nestedMap(slice, "metadata"), "labels")["kubernetes.io/service-name"])
			if !clusterDrainedTrafficService(serviceName) {
				continue
			}
			for _, endpointRaw := range anySlice(slice["endpoints"]) {
				endpoint, _ := endpointRaw.(map[string]any)
				conditions := mapStringAny(endpoint["conditions"])
				if cleanText(endpoint["nodeName"]) == nodeName && conditions["ready"] != false && !clusterCandidateEndpoint(endpoint) {
					foundReady = true
				}
			}
		}
		if !foundReady {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func clusterCandidateEndpoint(endpoint map[string]any) bool {
	targetRef := mapStringAny(endpoint["targetRef"])
	return cleanText(targetRef["kind"]) == "Pod" && strings.Contains(cleanText(targetRef["name"]), "-candidate-")
}

func clusterDrainedTrafficService(name string) bool {
	return textInSet(name,
		"api-backend",
		"api-backend-aegis",
		"job-scheduler",
		"remote-desktop-guacd",
		"traefik-edge",
		"webui-frontend",
	) || strings.HasPrefix(name, "site-worker-")
}

func (r *kubernetesClusterStepRunner) nodeReady(ctx context.Context, nodeName string) (bool, error) {
	if !clusterControllerNodeRegex.MatchString(nodeName) {
		return false, errors.New("invalid cluster node name")
	}
	var node map[string]any
	if err := r.kube.getJSON(ctx, "/api/v1/nodes/"+nodeName, &node); err != nil {
		if strings.Contains(err.Error(), "returned HTTP 404") {
			return false, nil
		}
		return false, err
	}
	for _, conditionRaw := range anySlice(nestedMap(node, "status")["conditions"]) {
		condition, _ := conditionRaw.(map[string]any)
		if cleanText(condition["type"]) == "Ready" {
			return cleanText(condition["status"]) == "True", nil
		}
	}
	return false, nil
}

func (r *kubernetesClusterStepRunner) kubernetesNodeExists(ctx context.Context, nodeName string) (bool, error) {
	if !clusterControllerNodeRegex.MatchString(nodeName) {
		return false, errors.New("invalid cluster node name")
	}
	var node map[string]any
	if err := r.kube.getJSON(ctx, "/api/v1/nodes/"+nodeName, &node); err != nil {
		if strings.Contains(err.Error(), "returned HTTP 404") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (r *kubernetesClusterStepRunner) waitNodeNotReady(ctx context.Context, nodeName string) error {
	pollInterval := r.jobPollInterval
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	var lastErr error
	for {
		ready, err := r.nodeReady(ctx, nodeName)
		if err == nil && !ready {
			return nil
		}
		if err != nil {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			message := fmt.Errorf("node %s did not become NotReady after K3s fence: %w", nodeName, ctx.Err())
			if lastErr != nil {
				return errors.Join(message, lastErr)
			}
			return message
		case <-ticker.C:
		}
	}
}

func (r *kubernetesClusterStepRunner) removeEtcdMembership(ctx context.Context, nodeName string) error {
	if !clusterControllerNodeRegex.MatchString(nodeName) {
		return errors.New("invalid Kubernetes member node name")
	}
	path := "/api/v1/nodes/" + nodeName
	pollInterval := r.jobPollInterval
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	etcdName := ""
	var lastErr error
	for {
		var observed map[string]any
		err := r.kube.getJSON(ctx, path, &observed)
		if err != nil && kubernetesAPIErrorHasStatus(err, http.StatusNotFound) {
			return nil
		}
		if err != nil {
			lastErr = err
		} else {
			observedAnnotations, _ := nestedMap(observed, "metadata")["annotations"].(map[string]any)
			removedName := cleanText(observedAnnotations[clusterK3sEtcdRemovedNameAnnotation])
			if removedName != "" {
				if etcdName == "" || removedName == etcdName {
					return nil
				}
				return errors.New("K3s confirmed removal for unexpected etcd member identity")
			}
			observedName := cleanText(observedAnnotations[clusterK3sEtcdNodeNameAnnotation])
			if etcdName == "" {
				if observedName == "" || len(observedName) > 128 {
					lastErr = errors.New("K3s etcd member identity is unavailable")
				} else {
					etcdName = observedName
				}
			} else if observedName != "" && observedName != etcdName {
				return errors.New("K3s etcd member identity changed during removal")
			}
			if etcdName != "" && cleanText(observedAnnotations[clusterK3sEtcdRemoveAnnotation]) != "true" {
				patch := map[string]any{"metadata": map[string]any{"annotations": map[string]any{clusterK3sEtcdRemoveAnnotation: "true"}}}
				var output map[string]any
				if patchErr := r.kube.doJSON(ctx, http.MethodPatch, path, patch, "application/strategic-merge-patch+json", &output, 30*time.Second); patchErr != nil {
					lastErr = patchErr
				} else {
					lastErr = nil
				}
			}
		}
		select {
		case <-ctx.Done():
			message := fmt.Errorf("K3s etcd membership was not removed for %s: %w", nodeName, ctx.Err())
			if lastErr != nil {
				return errors.Join(message, lastErr)
			}
			return message
		case <-ticker.C:
		}
	}
}

func (r *kubernetesClusterStepRunner) removalRetryHasFencedTarget(ctx context.Context, operation clusterControllerOperation, nodes []clusterControllerNode) (bool, error) {
	targets := make(map[string]bool, len(anySlice(operation.Payload["removal_node_ids"])))
	for _, rawID := range anySlice(operation.Payload["removal_node_ids"]) {
		targets[cleanText(rawID)] = true
	}
	for _, node := range nodes {
		if !targets[node.ID] {
			continue
		}
		ready, err := r.nodeReady(ctx, node.Name)
		if err != nil {
			return false, err
		}
		if !ready {
			return true, nil
		}
	}
	return false, nil
}

func clusterRemovalSurvivorNodes(operation clusterControllerOperation, nodes []clusterControllerNode) ([]clusterControllerNode, error) {
	removed := make(map[string]bool, len(anySlice(operation.Payload["removal_node_ids"])))
	for _, rawID := range anySlice(operation.Payload["removal_node_ids"]) {
		removed[cleanText(rawID)] = true
	}
	survivors := make([]clusterControllerNode, 0, len(nodes))
	for _, node := range nodes {
		if !removed[node.ID] {
			survivors = append(survivors, node)
		}
	}
	if int64(len(survivors)) != coerceInt64(operation.Payload["target_size"]) || len(survivors) == 0 {
		return nil, errors.New("node removal survivor set does not match target size")
	}
	return survivors, nil
}

func (r *kubernetesClusterStepRunner) deleteNodeResource(ctx context.Context, nodeName string) error {
	if !clusterControllerNodeRegex.MatchString(nodeName) {
		return errors.New("invalid Kubernetes member node name")
	}
	path := "/api/v1/nodes/" + nodeName
	var node map[string]any
	if err := r.kube.getJSON(ctx, path, &node); err != nil {
		if strings.Contains(err.Error(), "returned HTTP 404") {
			return nil
		}
		return err
	}
	uid := cleanText(nestedMap(node, "metadata")["uid"])
	if uid == "" || len(uid) > 128 {
		return errors.New("Kubernetes member node UID is unavailable")
	}
	deleteOptions := map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "propagationPolicy": "Foreground", "preconditions": map[string]any{"uid": uid}}
	var output map[string]any
	return r.kube.doJSON(ctx, http.MethodDelete, path, deleteOptions, "application/json", &output, 30*time.Second)
}

func (r *kubernetesClusterStepRunner) verifyMemberRemoved(ctx context.Context, operation clusterControllerOperation, targetName string, nodes []clusterControllerNode) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		healthy, err := r.memberRemovalStateHealthy(ctx, operation, targetName, nodes)
		if err != nil {
			return err
		}
		if healthy {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("K3s membership did not converge after removing %s: %w", targetName, ctx.Err())
		case <-ticker.C:
		}
	}
	if err := r.retireMemberWorkloads(ctx, targetName); err != nil {
		return err
	}
	timer := time.NewTimer(r.soak)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	healthy, err := r.memberRemovalStateHealthy(ctx, operation, targetName, nodes)
	if err != nil {
		return err
	}
	if !healthy {
		return errors.New("K3s membership lost voter health during removal soak")
	}
	return nil
}

func (r *kubernetesClusterStepRunner) memberRemovalStateHealthy(ctx context.Context, operation clusterControllerOperation, targetName string, nodes []clusterControllerNode) (bool, error) {
	removedIDs := map[string]bool{}
	for _, rawID := range anySlice(operation.Payload["removal_node_ids"]) {
		removedIDs[cleanText(rawID)] = true
	}
	for _, node := range nodes {
		var payload map[string]any
		err := r.kube.getJSON(ctx, "/api/v1/nodes/"+node.Name, &payload)
		missing := err != nil && strings.Contains(err.Error(), "returned HTTP 404")
		if node.Name == targetName {
			if missing {
				continue
			}
			if err != nil {
				return false, err
			}
			return false, nil
		}
		if missing && removedIDs[node.ID] {
			continue
		}
		if err != nil {
			return false, err
		}
		if !nodeConditionTrue(payload, "Ready") || !nodeConditionTrue(payload, "EtcdIsVoter") {
			return false, nil
		}
	}
	return true, nil
}

func (r *kubernetesClusterStepRunner) retireMemberWorkloads(ctx context.Context, nodeName string) error {
	for _, appName := range []string{"borealis-operator", "wireguard-tunnel"} {
		if err := r.scaleNodeWorkload(ctx, nodeName, appName, false); err != nil {
			return fmt.Errorf("retire %s workload on removed node %s: %w", appName, nodeName, err)
		}
	}
	return nil
}

func nodeConditionTrue(node map[string]any, conditionType string) bool {
	for _, raw := range anySlice(nestedMap(node, "status")["conditions"]) {
		condition, _ := raw.(map[string]any)
		if cleanText(condition["type"]) == conditionType {
			return cleanText(condition["status"]) == "True"
		}
	}
	return false
}

func (r *kubernetesClusterStepRunner) minimumReadySoak(ctx context.Context, nodeName string) error {
	if err := r.verifyReadyEndpointsForNode(ctx, nodeName); err != nil {
		return err
	}
	timer := time.NewTimer(r.soak)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	return r.verifyReadyEndpointsForNode(ctx, nodeName)
}

func (r *kubernetesClusterStepRunner) verifyReadyEndpoints(ctx context.Context) error {
	return r.verifyReadyEndpointsForNode(ctx, "")
}

func (r *kubernetesClusterStepRunner) verifyReadyEndpointsForNode(ctx context.Context, nodeName string) error {
	var payload map[string]any
	path := fmt.Sprintf("/apis/discovery.k8s.io/v1/namespaces/%s/endpointslices?labelSelector=app.kubernetes.io%%2Fpart-of%%3Dborealis", r.namespace)
	if err := r.kube.getJSON(ctx, path, &payload); err != nil {
		return err
	}
	ready := 0
	for _, item := range anySlice(payload["items"]) {
		slice, _ := item.(map[string]any)
		for _, endpointRaw := range anySlice(slice["endpoints"]) {
			endpoint, _ := endpointRaw.(map[string]any)
			conditions := mapStringAny(endpoint["conditions"])
			if conditions["ready"] != false && (nodeName == "" || cleanText(endpoint["nodeName"]) == nodeName) {
				ready++
			}
		}
	}
	if ready == 0 {
		if nodeName != "" {
			return fmt.Errorf("node %s has no ready Borealis endpoints", nodeName)
		}
		return errors.New("cluster has no ready Borealis endpoints")
	}
	return nil
}

func (r *kubernetesClusterStepRunner) createCNPGBackup(ctx context.Context, operation clusterControllerOperation) error {
	name := clusterActionJobName(operation.ID, fmt.Sprintf("attempt:%d:snapshot", operation.Attempt))
	path := fmt.Sprintf("/apis/postgresql.cnpg.io/v1/namespaces/%s/backups", r.namespace)
	manifest := map[string]any{
		"apiVersion": "postgresql.cnpg.io/v1", "kind": "Backup",
		"metadata": map[string]any{"name": name, "namespace": r.namespace, "labels": map[string]string{"borealis.io/operation-id": operation.ID}},
		"spec":     map[string]any{"method": "volumeSnapshot", "cluster": map[string]any{"name": "borealis-postgres"}},
	}
	var output map[string]any
	if err := r.kube.doJSON(ctx, http.MethodPost, path, manifest, "application/json", &output, 30*time.Second); err != nil {
		// Idempotent retry: existing named backup is accepted and inspected below.
		if err := r.kube.getJSON(ctx, path+"/"+name, &output); err != nil {
			return err
		}
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if err := r.kube.getJSON(ctx, path+"/"+name, &output); err != nil {
			return err
		}
		phase := strings.ToLower(cleanText(nestedMap(output, "status")["phase"]))
		switch phase {
		case "completed":
			return nil
		case "failed":
			return errors.New("pre-change CloudNativePG backup failed")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (r *kubernetesClusterStepRunner) pruneDailyCNPGBackups(ctx context.Context, retain int) error {
	if retain < 1 {
		return errors.New("snapshot retention must be positive")
	}
	path := fmt.Sprintf("/apis/postgresql.cnpg.io/v1/namespaces/%s/backups", r.namespace)
	var payload map[string]any
	if err := r.kube.getJSON(ctx, path, &payload); err != nil {
		// Standalone mode has no CNPG CRD. Nothing to retain yet.
		if strings.Contains(err.Error(), "HTTP 404") {
			return nil
		}
		return err
	}
	type retainedBackup struct {
		name      string
		createdAt string
	}
	backups := make([]retainedBackup, 0)
	for _, raw := range anySlice(payload["items"]) {
		item, _ := raw.(map[string]any)
		metadata := nestedMap(item, "metadata")
		isDaily := false
		for _, ownerRaw := range anySlice(metadata["ownerReferences"]) {
			owner, _ := ownerRaw.(map[string]any)
			if cleanText(owner["kind"]) == "ScheduledBackup" && cleanText(owner["name"]) == "borealis-postgres-daily" {
				isDaily = true
				break
			}
		}
		if !isDaily || strings.ToLower(cleanText(nestedMap(item, "status")["phase"])) != "completed" {
			continue
		}
		name := cleanText(metadata["name"])
		if name != "" {
			backups = append(backups, retainedBackup{name: name, createdAt: cleanText(metadata["creationTimestamp"])})
		}
	}
	sort.Slice(backups, func(i, j int) bool { return backups[i].createdAt > backups[j].createdAt })
	if len(backups) <= retain {
		return nil
	}
	for _, backup := range backups[retain:] {
		var deleted map[string]any
		if err := r.kube.doJSON(ctx, http.MethodDelete, path+"/"+backup.name, map[string]any{"propagationPolicy": "Foreground"}, "application/json", &deleted, 30*time.Second); err != nil {
			return err
		}
	}
	return nil
}

func (r *kubernetesClusterStepRunner) recordClusterIntent(ctx context.Context, operation clusterControllerOperation, step clusterControllerStep) error {
	operation.CurrentStep = step.Name
	if operation.State == "" {
		operation.State = "running"
	}
	return r.reconcileOperationResource(ctx, clusterControllerOperationResource{Operation: operation}, time.Now().UTC().Unix())
}
