package main

import (
	"context"
	"database/sql"
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
	"sync/atomic"
	"time"
)

const (
	clusterControllerLeaseName = "cluster-operation-controller"
	clusterControllerLeaseTTL  = 20 * time.Second
	clusterNodeManagerSocket   = "/run/borealis/node-manager.sock"
	clusterNodeManagerToken    = "/etc/borealis/node-manager.token"
	clusterUpgradeNamespace    = "system-upgrade"
)

var (
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
	Roles            map[string]any
}

type clusterControllerStep struct {
	Name   string
	NodeID string
}

type clusterAdmissionNodeAction struct {
	stepName      string
	verb          string
	targetRelease string
}

func clusterAdmissionConformanceAction(nodeID, k3sVersion string) clusterAdmissionNodeAction {
	return clusterAdmissionNodeAction{stepName: "admit:" + nodeID + ":probe_conformance", verb: "RunK3sProbeConformance", targetRelease: k3sVersion}
}

func clusterAdmissionWorkloadActions(nodeID string) []clusterAdmissionNodeAction {
	return []clusterAdmissionNodeAction{
		{stepName: "admit:" + nodeID + ":redeploy", verb: "RedeployRevision"},
		{stepName: "admit:" + nodeID + ":health", verb: "InspectHealth"},
	}
}

type clusterControllerStepRunner interface {
	Run(context.Context, clusterControllerOperation, clusterControllerStep, []clusterControllerNode) error
}

type clusterController struct {
	store     *postgresOperatorStore
	runner    clusterControllerStepRunner
	holder    string
	now       func() time.Time
	lastPrune atomic.Int64
}

type kubernetesClusterStepRunner struct {
	kube                          *kubernetesAPIClient
	namespace                     string
	actionImage                   string
	soak                          time.Duration
	jobPollInterval               time.Duration
	clusterInitAuthorizationGrace time.Duration
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
	if now := c.now().UTC().Unix(); now-c.lastPrune.Load() >= int64(time.Hour/time.Second) {
		if runner, ok := c.runner.(*kubernetesClusterStepRunner); ok {
			if pruneErr := runner.pruneDailyCNPGBackups(ctx, 14); pruneErr != nil {
				log.Printf("cluster snapshot retention reconcile failed: %v", pruneErr)
			} else {
				c.lastPrune.Store(now)
			}
		}
	}
	if runner, ok := c.runner.(*kubernetesClusterStepRunner); ok {
		if recovered, recoveryErr := c.reconcileLostHMRNode(ctx, runner); recoveryErr != nil || recovered {
			return recoveryErr
		}
	}
	operation, ok, err := c.claimOperation(ctx)
	if err != nil || !ok {
		return err
	}
	nodes, err := c.activeNodes(ctx)
	if err != nil {
		return c.failOperation(ctx, operation, err)
	}
	steps, err := clusterOperationSteps(operation, nodes)
	if err != nil {
		return c.failOperation(ctx, operation, err)
	}
	stepIndex := 0
	for i := range steps {
		if steps[i].Name == operation.CurrentStep {
			stepIndex = i
			break
		}
	}
	step := steps[stepIndex]
	stepCtx, cancel := context.WithTimeout(ctx, clusterControllerStepTimeout(step.Name))
	err = c.runner.Run(stepCtx, operation, step, nodes)
	cancel()
	if err != nil {
		return c.failOperation(ctx, operation, fmt.Errorf("%s: %w", step.Name, err))
	}
	if stepIndex+1 == len(steps) {
		return c.completeOperation(ctx, operation, nodes)
	}
	return c.advanceOperation(ctx, operation, steps[stepIndex+1].Name)
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
	for index, node := range standby {
		roleOwner := index == 0
		if err := runner.patchNodeLabels(ctx, node.Name, map[string]string{
			"borealis.io/application-state": "active",
			"borealis.io/hmr-target":        "false",
		}); err != nil {
			return true, err
		}
		if err := runner.setNodeRoleEligibility(ctx, node.Name, roleOwner); err != nil {
			return true, err
		}
		step := clusterControllerStep{Name: "hmr-recovery:" + node.ID, NodeID: node.ID}
		if err := runner.nodeActionJob(ctx, recovery, step, node, "ExitApplicationDrain"); err != nil {
			return true, err
		}
		if err := runner.nodeActionJob(ctx, recovery, clusterControllerStep{Name: step.Name + ":health", NodeID: node.ID}, node, "InspectHealth"); err != nil {
			return true, err
		}
		if err := runner.minimumReadySoak(ctx, node.Name); err != nil {
			return true, err
		}
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
	case strings.HasSuffix(step, ":redeploy_revision"), strings.HasSuffix(step, ":promote_candidate"):
		return 65 * time.Minute
	case strings.HasSuffix(step, ":fetch_release"), strings.HasSuffix(step, ":apply_k3s_upgrade"):
		return 35 * time.Minute
	case step == "apply_cluster_foundation":
		return 95 * time.Minute
	case step == "apply_membership":
		return 45 * time.Minute
	case step == "pre_change_snapshot", step == "migrate_postgres", step == "finalize_schema", step == "prepare_postgres_removal", step == "scale_postgres_membership":
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
		base = append(base, clusterControllerStep{Name: "pre_change_snapshot"}, clusterControllerStep{Name: "expand_schema"})
		for _, node := range ordered {
			for _, action := range []string{"transfer_roles", "enter_drain", "wait_endpoint_withdrawal", "fetch_release", "redeploy_revision", "inspect_candidate_health", "minimum_candidate_soak", "promote_candidate", "inspect_health", "minimum_ready_soak", "exit_drain"} {
				base = append(base, clusterControllerStep{Name: "node:" + node.ID + ":" + action, NodeID: node.ID})
			}
		}
		return append(base, clusterControllerStep{Name: "finalize_schema"}, clusterControllerStep{Name: "verify_cluster"}), nil
	case "k3s_update":
		ordered, err := clusterUpdateNodes(operation, nodes)
		if err != nil {
			return nil, err
		}
		base = append(base, clusterControllerStep{Name: "pre_change_snapshot"})
		for _, node := range ordered {
			for _, action := range []string{"transfer_roles", "enter_drain", "wait_endpoint_withdrawal", "apply_k3s_upgrade", "wait_k3s_ready", "post_k3s_conformance", "inspect_health", "minimum_ready_soak", "exit_drain"} {
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
		steps := append(base, clusterControllerStep{Name: "hmr_restore_pinned_release", NodeID: targetID}, clusterControllerStep{Name: "hmr_verify_production", NodeID: targetID})
		for _, node := range nodes {
			if node.ID != targetID {
				steps = append(steps,
					clusterControllerStep{Name: "node:" + node.ID + ":exit_drain", NodeID: node.ID},
					clusterControllerStep{Name: "node:" + node.ID + ":inspect_health", NodeID: node.ID},
					clusterControllerStep{Name: "node:" + node.ID + ":minimum_ready_soak", NodeID: node.ID},
				)
			}
		}
		return append(steps, clusterControllerStep{Name: "verify_cluster"}), nil
	case "node_maintenance":
		action := cleanText(operation.Payload["action"])
		if action == "enter" {
			return append(base, clusterControllerStep{Name: "node:" + operation.TargetNodeID + ":transfer_roles", NodeID: operation.TargetNodeID}, clusterControllerStep{Name: "node:" + operation.TargetNodeID + ":enter_drain", NodeID: operation.TargetNodeID}, clusterControllerStep{Name: "node:" + operation.TargetNodeID + ":wait_endpoint_withdrawal", NodeID: operation.TargetNodeID}), nil
		}
		return append(base, clusterControllerStep{Name: "node:" + operation.TargetNodeID + ":inspect_health", NodeID: operation.TargetNodeID}, clusterControllerStep{Name: "node:" + operation.TargetNodeID + ":exit_drain", NodeID: operation.TargetNodeID}, clusterControllerStep{Name: "node:" + operation.TargetNodeID + ":minimum_ready_soak", NodeID: operation.TargetNodeID}), nil
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
			for _, action := range []string{"transfer_roles", "enter_drain", "wait_endpoint_withdrawal", "prepare_member_removal", "wait_member_fenced", "delete_member_node", "verify_member_removed"} {
				base = append(base, clusterControllerStep{Name: "node:" + node.ID + ":" + action, NodeID: node.ID})
			}
		}
		return append(base, clusterControllerStep{Name: "verify_quorum"}), nil
	case "membership_admit", "membership_scale":
		return append(base, clusterControllerStep{Name: "apply_membership"}, clusterControllerStep{Name: "verify_quorum"}), nil
	case "postgres_switchover", "postgres_emergency_failover":
		return append(base, clusterControllerStep{Name: "pre_change_snapshot"}, clusterControllerStep{Name: "postgres_role_change", NodeID: operation.TargetNodeID}, clusterControllerStep{Name: "verify_postgres"}), nil
	case "cluster_enable":
		return append(base, clusterControllerStep{Name: "apply_cluster_foundation"}, clusterControllerStep{Name: "migrate_postgres"}, clusterControllerStep{Name: "verify_cluster"}), nil
	default:
		return nil, fmt.Errorf("unsupported cluster operation kind %q", operation.Kind)
	}
}

func clusterUpdateNodes(operation clusterControllerOperation, nodes []clusterControllerNode) ([]clusterControllerNode, error) {
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

func (c *clusterController) acquireLease(ctx context.Context) (bool, error) {
	now := c.now().UTC().Unix()
	expires := now + int64(clusterControllerLeaseTTL/time.Second)
	conn, err := c.store.db.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	result, err := conn.ExecContext(ctx, `
		INSERT INTO engine.cluster_application_leases(name, holder, expires_at, updated_at)
		VALUES($1,$2,$3,$4)
		ON CONFLICT(name) DO UPDATE
		SET holder=EXCLUDED.holder, expires_at=EXCLUDED.expires_at, updated_at=EXCLUDED.updated_at
		WHERE engine.cluster_application_leases.holder=EXCLUDED.holder
		   OR engine.cluster_application_leases.expires_at < EXCLUDED.updated_at
	`, clusterControllerLeaseName, c.holder, expires, now)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
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
		SELECT id, node_name, application_state, COALESCE(release_tag,''), COALESCE(release_sha,''), roles_json
		  FROM engine.cluster_nodes WHERE membership_state='Active' ORDER BY node_name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := make([]clusterControllerNode, 0, 5)
	for rows.Next() {
		var node clusterControllerNode
		var rolesJSON string
		if err := rows.Scan(&node.ID, &node.Name, &node.ApplicationState, &node.ReleaseTag, &node.ReleaseSHA, &rolesJSON); err != nil {
			return nil, err
		}
		node.Roles = parseClusterJSON(rolesJSON)
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
	now := c.now().UTC().Unix()
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_operations SET current_step=$1, updated_at=$2 WHERE id=$3 AND state='running'`, nextStep, now, operation.ID); err != nil {
		return err
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
	now := c.now().UTC().Unix()
	message := truncateClusterError(cause.Error(), 2048)
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_operations SET state='failed', error_text=$1, finished_at=$2, updated_at=$2 WHERE id=$3`, message, now, operation.ID); err != nil {
		return errors.Join(cause, err)
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
	now := c.now().UTC().Unix()
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
		if _, err = tx.ExecContext(ctx, `UPDATE engine.cluster_state SET hmr_state='inactive',hmr_node_id=NULL,status='Healthy',updated_at=$1 WHERE id=1`, now); err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE engine.cluster_nodes SET application_state='active',drain_reason=NULL,updated_at=$1 WHERE membership_state='Active'`, now)
		}
	case "engine_update":
		if cleanText(operation.Payload["scope"]) == "all" {
			_, err = tx.ExecContext(ctx, `UPDATE engine.cluster_state SET baseline_release=$1,baseline_sha=$2,status='Healthy',updated_at=$3 WHERE id=1`, operation.TargetRelease, operation.TargetSHA, now)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE engine.cluster_state SET status='Mixed Version',updated_at=$1 WHERE id=1`, now)
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
	case "node_remove":
		for _, rawID := range anySlice(operation.Payload["removal_node_ids"]) {
			_, err = tx.ExecContext(ctx, `UPDATE engine.cluster_nodes SET membership_state='Removed',application_state='drained',drain_reason=$1,roles_json='{}',updated_at=$2 WHERE id=$3`, firstText(cleanText(operation.Payload["reason"]), "removed"), now, cleanText(rawID))
			if err != nil {
				break
			}
		}
		if err == nil {
			status := "Healthy"
			if emergency, _ := operation.Payload["emergency"].(bool); emergency {
				status = "Degraded Quorum"
			}
			_, err = tx.ExecContext(ctx, `UPDATE engine.cluster_state SET active_size=$1,desired_size=$1,status=$2,updated_at=$3 WHERE id=1`, coerceInt64(operation.Payload["target_size"]), status, now)
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
			_, err = tx.ExecContext(ctx, `INSERT INTO engine.cluster_nodes(id,node_name,hostname,management_ip,architecture,os_version,membership_state,application_state,release_tag,release_sha,roles_json,probe_health_json,last_seen_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,'Active','active',$7,$8,$9,$10,$11,$11,$11) ON CONFLICT(id) DO UPDATE SET membership_state='Active',application_state='active',release_tag=EXCLUDED.release_tag,release_sha=EXCLUDED.release_sha,roles_json=EXCLUDED.roles_json,probe_health_json=EXCLUDED.probe_health_json,last_seen_at=EXCLUDED.last_seen_at,updated_at=EXCLUDED.updated_at`, id, nodeName, hostname, managementIP, architecture, osVersion, baselineRelease, baselineSHA, rolesJSON, probeHealth, now)
			if err != nil {
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
		if operation.Kind != "cluster_enable" && len(nodes) != 1 && len(nodes) != 3 && len(nodes) != 5 {
			return fmt.Errorf("active node count %d is not supported", len(nodes))
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
	if step.Name == "expand_schema" || step.Name == "finalize_schema" {
		// Migration phases are target-owned jobs. Manifest declares expand/contract safety;
		// node-scoped redeploy applies phase-specific jobs idempotently.
		return nil
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
		for _, candidate := range nodes {
			target := candidate.ID == node.ID
			if err := r.setNodeRoleEligibility(ctx, candidate.Name, target); err != nil {
				return err
			}
		}
		return nil
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
		sha := firstText(operation.TargetSHA, cleanText(operation.Payload["baseline_sha"]), node.ReleaseSHA)
		release := firstText(operation.TargetRelease, cleanText(operation.Payload["baseline_release"]), node.ReleaseTag)
		if !clusterControllerSHARegex.MatchString(sha) || !clusterReleaseRE.MatchString(release) {
			return errors.New("saved pinned production release is unavailable")
		}
		restore := operation
		restore.TargetSHA = sha
		restore.TargetRelease = release
		if err := r.nodeActionJob(ctx, restore, clusterControllerStep{Name: step.Name + ":stage", NodeID: node.ID}, node, "StagePinnedRelease"); err != nil {
			return err
		}
		return r.nodeActionJob(ctx, restore, clusterControllerStep{Name: step.Name + ":deploy", NodeID: node.ID}, node, "RedeployStagedRevision")
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
		if operation.Kind == "node_remove" && textInSet(action, "transfer_roles", "enter_drain", "prepare_member_removal") {
			exists, err := r.kubernetesNodeExists(ctx, node.Name)
			if err != nil {
				return err
			}
			if !exists {
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
		case "wait_member_fenced":
			return r.waitNodeNotReady(ctx, node.Name)
		case "delete_member_node":
			return r.deleteNodeResource(ctx, node.Name)
		case "verify_member_removed":
			return r.verifyMemberRemoved(ctx, operation, node.Name, nodes)
		case "fetch_release":
			return r.nodeActionJob(ctx, operation, step, node, "FetchRelease")
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
		case "inspect_health":
			return r.nodeActionJob(ctx, operation, step, node, "InspectHealth")
		case "minimum_ready_soak":
			return r.minimumReadySoak(ctx, node.Name)
		case "exit_drain":
			if err := r.nodeActionJob(ctx, operation, step, node, "ExitApplicationDrain"); err != nil {
				return err
			}
			if len(nodes) == 1 {
				return r.setNodeRoleEligibility(ctx, node.Name, true)
			}
			return r.patchNodeLabels(ctx, node.Name, map[string]string{"borealis.io/hmr-target": "false"})
		}
	}
	return fmt.Errorf("unsupported cluster controller step %q", step.Name)
}

func (r *kubernetesClusterStepRunner) admitPendingMembers(ctx context.Context, operation clusterControllerOperation, activeNodes []clusterControllerNode) error {
	ids := anySlice(operation.Payload["admission_ids"])
	names := anySlice(operation.Payload["node_names"])
	baselineRelease := cleanText(operation.Payload["baseline_release"])
	baselineSHA := cleanText(operation.Payload["baseline_sha"])
	if len(ids) != 2 || len(names) != 2 || !clusterReleaseRE.MatchString(baselineRelease) || !clusterControllerSHARegex.MatchString(baselineSHA) {
		return errors.New("paired admission lacks pinned cluster baseline")
	}
	pending := make([]clusterControllerNode, 0, 2)
	for index := range ids {
		node := clusterControllerNode{ID: cleanText(ids[index]), Name: cleanText(names[index]), ReleaseTag: baselineRelease, ReleaseSHA: baselineSHA}
		if !clusterUUIDRE.MatchString(node.ID) || !clusterControllerNodeRegex.MatchString(node.Name) {
			return errors.New("paired admission contains invalid node identity")
		}
		pending = append(pending, node)
	}
	for _, node := range pending {
		if err := r.waitNodeReady(ctx, node.Name); err != nil {
			return err
		}
		if err := r.patchNodeLabels(ctx, node.Name, map[string]string{
			"borealis.io/application-state":         "active",
			"borealis.io/edge-eligible":             "false",
			"borealis.io/scheduler-eligible":        "false",
			"borealis.io/postgres-primary-eligible": "false",
			"borealis.io/hmr-target":                "false",
		}); err != nil {
			return err
		}
	}
	newSize := len(activeNodes) + len(pending)
	if newSize != 3 && newSize != 5 {
		return fmt.Errorf("paired admission would produce unsupported active size %d", newSize)
	}
	k3sVersion := cleanText(operation.Payload["k3s_version"])
	if !clusterK3sRE.MatchString(k3sVersion) {
		return errors.New("paired admission lacks stable K3s version")
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
		}
		if err := r.minimumReadySoak(ctx, node.Name); err != nil {
			return err
		}
	}
	return r.recordClusterIntent(ctx, operation, clusterControllerStep{Name: "apply_membership"})
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

func (r *kubernetesClusterStepRunner) scaleCNPG(ctx context.Context, size int) error {
	if size < 1 || size > 5 {
		return fmt.Errorf("unsupported CloudNativePG membership size %d", size)
	}
	path := fmt.Sprintf("/apis/postgresql.cnpg.io/v1/namespaces/%s/clusters/borealis-postgres", r.namespace)
	var synchronous any
	if size > 1 {
		acknowledgements := int64(1)
		if size >= 4 {
			acknowledgements = 2
		}
		synchronous = map[string]any{"method": "any", "number": acknowledgements, "dataDurability": "required"}
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
	return r.setNodeRoleEligibility(ctx, target.Name, false)
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
	return r.setNodeRoleEligibility(ctx, target.Name, false)
}

func (r *kubernetesClusterStepRunner) setNodeRoleEligibility(ctx context.Context, nodeName string, eligible bool) error {
	value := strconv.FormatBool(eligible)
	if err := r.patchNodeLabels(ctx, nodeName, map[string]string{
		"borealis.io/edge-eligible":             value,
		"borealis.io/scheduler-eligible":        value,
		"borealis.io/postgres-primary-eligible": value,
	}); err != nil {
		return err
	}
	return r.scaleNodeWorkload(ctx, nodeName, "wireguard-tunnel", eligible)
}

func (r *kubernetesClusterStepRunner) scaleNodeWorkload(ctx context.Context, nodeName, appName string, active bool) error {
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
	return r.kube.doJSON(ctx, http.MethodPatch, fmt.Sprintf("/apis/apps/v1/namespaces/%s/deployments/%s", r.namespace, name), map[string]any{"spec": map[string]any{"replicas": replicas}}, "application/merge-patch+json", &result, 30*time.Second)
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
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if err := r.kube.getJSON(ctx, clusterPath, &cluster); err != nil {
			return err
		}
		status = nestedMap(cluster, "status")
		if cleanText(status["currentPrimary"]) == target && cleanText(status["targetPrimary"]) == target {
			return nil
		}
		if strings.EqualFold(cleanText(status["phase"]), "Cluster in healthy state") && cleanText(status["currentPrimary"]) != target {
			return errors.New("CloudNativePG completed without promoting requested target")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
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
	var existing map[string]any
	if err := r.kube.getJSON(ctx, path, &existing); err != nil {
		if !strings.Contains(err.Error(), "returned HTTP 404") {
			return err
		}
		args := []string{"client", "--verb", verb}
		if verb == "PreflightRelease" || verb == "FetchRelease" || verb == "StagePinnedRelease" {
			args = append(args, "--release-tag", operation.TargetRelease, "--target-sha", operation.TargetSHA)
		}
		if verb == "RedeployRevision" || verb == "RedeployStagedRevision" || verb == "PromoteCandidate" {
			args = append(args, "--target-sha", operation.TargetSHA)
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
		manifest := clusterActionJobManifest(jobName, r.namespace, node.Name, r.actionImage, args, operation.ID, step.Name)
		if err := r.kube.doJSON(ctx, http.MethodPost, fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs", r.namespace), manifest, "application/json", &existing, 30*time.Second); err != nil {
			return err
		}
	}
	if verb == "EnrollCluster" {
		return r.waitClusterInitJob(ctx, jobName)
	}
	return r.waitJob(ctx, jobName)
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
		"metadata": map[string]any{"name": name, "namespace": namespace, "labels": map[string]string{"app.kubernetes.io/name": "borealis-node-action", "borealis.io/operation-id": operationID}},
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
			for _, endpointRaw := range anySlice(slice["endpoints"]) {
				endpoint, _ := endpointRaw.(map[string]any)
				conditions := mapStringAny(endpoint["conditions"])
				if cleanText(endpoint["nodeName"]) == nodeName && conditions["ready"] != false {
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
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		ready, err := r.nodeReady(ctx, nodeName)
		if err != nil {
			return err
		}
		if !ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("node %s did not become NotReady after K3s fence: %w", nodeName, ctx.Err())
		case <-ticker.C:
		}
	}
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
	name := "borealis"
	path := fmt.Sprintf("/apis/engine.borealis.io/v1alpha1/namespaces/%s/borealisclusteroperations/%s", r.namespace, operation.ID)
	patch := map[string]any{"spec": map[string]any{"operationID": operation.ID, "kind": operation.Kind, "step": step.Name, "targetNodeID": step.NodeID, "targetRelease": operation.TargetRelease, "targetSHA": operation.TargetSHA}}
	var output map[string]any
	if err := r.kube.doJSON(ctx, http.MethodPatch, path, patch, "application/merge-patch+json", &output, 30*time.Second); err == nil {
		return nil
	}
	manifest := map[string]any{"apiVersion": "engine.borealis.io/v1alpha1", "kind": "BorealisClusterOperation", "metadata": map[string]any{"name": operation.ID, "namespace": r.namespace, "labels": map[string]string{"app.kubernetes.io/name": name}}, "spec": patch["spec"]}
	return r.kube.doJSON(ctx, http.MethodPost, fmt.Sprintf("/apis/engine.borealis.io/v1alpha1/namespaces/%s/borealisclusteroperations", r.namespace), manifest, "application/json", &output, 30*time.Second)
}
