package main

import (
	"context"
	"database/sql"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Original-controller provenance must supply this binding before observation.
// No current-name discovery constructor exists. Promotion, Pod replacement or
// container restart requires separately authorized provenance, never rebinding.
type clusterAutomaticRecoveryDatabaseBinding struct {
	Node                                 clusterAutomaticRecoveryNodeBinding
	NamespaceUID, ClusterUID, PrimaryPod string
	Members                              [3]clusterAutomaticRecoveryDatabaseMember
}

type clusterAutomaticRecoveryDatabaseMember struct {
	NodeID, NodeName, NodeUID, Pod, PodUID, PodIP string
	Image, ImageID, ContainerID                   string
}

func (b clusterAutomaticRecoveryDatabaseBinding) valid() bool {
	if !b.Node.valid() || !clusterSSHStorageInstanceRE.MatchString(b.PrimaryPod) {
		return false
	}
	uids := []string{b.Node.ClusterID, b.Node.KubeSystemUID, b.NamespaceUID, b.ClusterUID}
	names, pods, addresses, containers := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	target, primary := false, false
	for _, m := range b.Members {
		if !clusterControllerNodeRegex.MatchString(m.NodeName) || !clusterSSHStorageInstanceRE.MatchString(m.Pod) || !recoveryApplicationIP(m.PodIP) || names[m.NodeName] || pods[m.Pod] || addresses[m.PodIP] || containers[m.ContainerID] {
			return false
		}
		for _, image := range []string{m.Image, m.ImageID} {
			prefix, digest, ok := strings.Cut(image, "@")
			if !ok || prefix == "" || len(prefix) > 255 || strings.ContainsAny(prefix, " \t\r\n") || !clusterBootstrapDigestPattern.MatchString(digest) {
				return false
			}
		}
		if !strings.HasPrefix(m.ContainerID, "containerd://") || !validClusterSSHPreparationDigest(strings.TrimPrefix(m.ContainerID, "containerd://")) {
			return false
		}
		names[m.NodeName], pods[m.Pod], addresses[m.PodIP], containers[m.ContainerID] = true, true, true, true
		uids = append(uids, m.NodeID, m.NodeUID, m.PodUID)
		if m.NodeID == b.Node.NodeID {
			target = m.NodeName == b.Node.NodeName && m.NodeUID == b.Node.NodeUID
		}
		if m.Pod == b.PrimaryPod {
			primary = true
		}
	}
	seen := map[string]bool{}
	for _, uid := range uids {
		if !vipScopeUUID(uid) || seen[uid] {
			return false
		}
		seen[uid] = true
	}
	return target && primary
}

type clusterAutomaticRecoveryDatabaseRow struct {
	Server, Commit, Standbys, Point, Application, Client, State, Sync, Flush sql.NullString
	Recovery                                                                 bool
	Reply                                                                    sql.NullTime
}

type clusterAutomaticRecoveryDatabaseRead func(context.Context) ([]clusterAutomaticRecoveryDatabaseRow, error)

// Capture one primary WAL point, then fetch a bounded complete replica census.
// Three rows already exceed this fixed three-member H04 contract and reject.
// Existing borealis pg_read_all_stats grant supplies these fields; no new role,
// connection credentials, target execution or replication mutation is needed.
const clusterAutomaticRecoveryDatabaseQuery = `WITH primary_observation AS MATERIALIZED (
 SELECT host(inet_server_addr()) AS server, pg_is_in_recovery() AS recovery,
 current_setting('synchronous_commit') AS commit,
 CASE WHEN length(current_setting('synchronous_standby_names'))<=512 THEN current_setting('synchronous_standby_names') END AS standbys,
 pg_current_wal_flush_lsn()::text AS point
), replicas AS MATERIALIZED (
 SELECT application_name,host(client_addr) AS client,state,sync_state,flush_lsn::text AS flush,reply_time
 FROM pg_stat_replication ORDER BY application_name LIMIT 3
)
SELECT p.server,p.recovery,p.commit,p.standbys,p.point,r.application_name,r.client,r.state,r.sync_state,r.flush,r.reply_time
FROM primary_observation p LEFT JOIN replicas r ON true`

func readClusterAutomaticRecoveryDatabaseSQL(ctx context.Context, db *sql.DB) ([]clusterAutomaticRecoveryDatabaseRow, error) {
	if db == nil || ctx.Err() != nil {
		return nil, errClusterConflict
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, errClusterConflict
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, clusterAutomaticRecoveryDatabaseQuery)
	if err != nil {
		return nil, errClusterConflict
	}
	defer rows.Close()
	values := make([]clusterAutomaticRecoveryDatabaseRow, 0, 3)
	for rows.Next() {
		var row clusterAutomaticRecoveryDatabaseRow
		if len(values) == 3 || rows.Scan(&row.Server, &row.Recovery, &row.Commit, &row.Standbys, &row.Point, &row.Application, &row.Client, &row.State, &row.Sync, &row.Flush, &row.Reply) != nil {
			return nil, errClusterConflict
		}
		values = append(values, row)
	}
	rowErr := rows.Err()
	closeRowsErr := rows.Close()
	closeConnErr := conn.Close()
	if rowErr != nil || closeRowsErr != nil || closeConnErr != nil || ctx.Err() != nil {
		return nil, errClusterConflict
	}
	// Validation and all subsequent native IO happen after the pool return.
	return values, nil
}

var recoveryDatabaseLSN = regexp.MustCompile(`^[0-9A-F]{1,8}/[0-9A-F]{1,8}$`)
var recoveryDatabaseStandbys = regexp.MustCompile(`^ANY[ \t]+1[ \t]*\([ \t]*("?borealis-postgres-[1-9][0-9]{0,8}"?)[ \t]*,[ \t]*("?borealis-postgres-[1-9][0-9]{0,8}"?)[ \t]*\)$`)

func recoveryDatabaseWAL(raw sql.NullString) (uint64, bool) {
	if !raw.Valid || !recoveryDatabaseLSN.MatchString(raw.String) {
		return 0, false
	}
	upper, lower, _ := strings.Cut(raw.String, "/")
	hi, _ := strconv.ParseUint(upper, 16, 32)
	lo, _ := strconv.ParseUint(lower, 16, 32)
	value := hi<<32 | lo
	return value, value > 0
}

func recoveryDatabaseReplication(b clusterAutomaticRecoveryDatabaseBinding, rows []clusterAutomaticRecoveryDatabaseRow) bool {
	if !b.valid() || len(rows) != 2 {
		return false
	}
	primary := ""
	replicas := map[string]string{}
	for _, m := range b.Members {
		if m.Pod == b.PrimaryPod {
			primary = m.PodIP
		} else {
			replicas[m.Pod] = m.PodIP
		}
	}
	first := rows[0]
	point, valid := recoveryDatabaseWAL(first.Point)
	if !valid || !first.Server.Valid || first.Server.String != primary || first.Recovery || !first.Commit.Valid || first.Commit.String != "on" || !first.Standbys.Valid || len(first.Standbys.String) > 512 {
		return false
	}
	policy := recoveryDatabaseStandbys.FindStringSubmatch(first.Standbys.String)
	if len(policy) != 3 {
		return false
	}
	policyNames := map[string]bool{}
	for _, raw := range policy[1:] {
		if strings.HasPrefix(raw, `"`) != strings.HasSuffix(raw, `"`) {
			return false
		}
		name := strings.Trim(raw, `"`)
		if replicas[name] == "" || policyNames[name] {
			return false
		}
		policyNames[name] = true
	}
	seen := map[string]bool{}
	synchronous := false
	for _, row := range rows {
		address, expected := replicas[row.Application.String]
		if !expected || row.Server != first.Server || row.Recovery || row.Commit != first.Commit || row.Standbys != first.Standbys || row.Point != first.Point || !row.Application.Valid || !row.Client.Valid || !row.State.Valid || !row.Sync.Valid || row.State.String != "streaming" || !textInSet(row.Sync.String, "quorum", "async") || address != row.Client.String || seen[row.Application.String] {
			return false
		}
		if !row.Reply.Valid || row.Reply.Time.IsZero() {
			return false
		}
		flush, valid := recoveryDatabaseWAL(row.Flush)
		if !valid || flush < point {
			return false
		}
		seen[row.Application.String] = true
		synchronous = synchronous || row.Sync.String == "quorum"
	}
	return len(seen) == 2 && synchronous
}

func recoveryDatabaseExactFields(value any) bool {
	if !recoveryApplicationExactFields(value) {
		return false
	}
	switch object := value.(type) {
	case clusterNativeObservationObject:
		return recoveryDatabaseExactFields(map[string]any(object))
	case map[string]any:
		if !clusterNativeObservationFieldNames(object, "items", "continue", "remainingItemCount", "instances", "readyInstances", "currentPrimary", "targetPrimary", "postgresql", "parameters", "synchronous", "method", "number", "dataDurability", "synchronous_commit", "initContainerStatuses", "restartPolicy", "terminated", "exitCode", "reason", "podIPs", "ip") {
			return false
		}
		for _, child := range object {
			if !recoveryDatabaseExactFields(child) {
				return false
			}
		}
	case []any:
		for _, child := range object {
			if !recoveryDatabaseExactFields(child) {
				return false
			}
		}
	}
	return true
}

func recoveryDatabaseCluster(object map[string]any, b clusterAutomaticRecoveryDatabaseBinding) bool {
	spec, status := clusterSSHStorageMap(object, "spec"), clusterSSHStorageMap(object, "status")
	instances, ok := clusterSSHStorageInteger(spec["instances"])
	ready, readyOK := clusterSSHStorageInteger(status["readyInstances"])
	postgres := clusterSSHStorageMap(spec, "postgresql")
	policy := clusterSSHStorageMap(postgres, "synchronous")
	number, numberOK := clusterSSHStorageInteger(policy["number"])
	// Exact required three-member policy from render-postgres.py/scaleCNPG.
	return ok && readyOK && instances == 3 && ready == 3 && status["currentPrimary"] == b.PrimaryPod && status["targetPrimary"] == b.PrimaryPod && status["phase"] == "Cluster in healthy state" && len(policy) == 3 && policy["method"] == "any" && numberOK && number == 1 && policy["dataDurability"] == "required" && clusterSSHStorageMap(postgres, "parameters")["synchronous_commit"] == "on"
}

func recoveryDatabasePod(pod map[string]any, m clusterAutomaticRecoveryDatabaseMember, owner clusterSSHStorageIdentity) bool {
	id, ok := clusterSSHStorageMetadata(pod, "v1", "Pod", "borealis")
	spec, status := clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
	labels := clusterSSHStorageMap(clusterSSHStorageMap(pod, "metadata"), "labels")
	if !ok || id.Name != m.Pod || id.UID != m.PodUID || !clusterSSHStorageOwner(pod, owner) || labels["cnpg.io/cluster"] != "borealis-postgres" || spec["nodeName"] != m.NodeName || status["podIP"] != m.PodIP || status["phase"] != "Running" || !validSourceConditions(status) || !sourceConditionTrue(status, "Ready") || !clusterSSHStorageEmptyList(spec["ephemeralContainers"]) || spec["hostNetwork"] != nil && spec["hostNetwork"] != false {
		return false
	}
	containers, ok := spec["containers"].([]any)
	states, statesOK := status["containerStatuses"].([]any)
	if !ok || len(containers) != 1 || !statesOK || len(states) != 1 {
		return false
	}
	container, ok := containers[0].(map[string]any)
	runtime, runtimeOK := states[0].(map[string]any)
	state := clusterSSHStorageMap(runtime, "state")
	_, startedErr := time.Parse(time.RFC3339Nano, clusterSSHStorageText(clusterSSHStorageMap(state, "running"), "startedAt"))
	if !ok || !runtimeOK || container["name"] != "postgres" || container["image"] != m.Image || runtime["name"] != "postgres" || runtime["image"] != m.Image || runtime["imageID"] != m.ImageID || runtime["containerID"] != m.ContainerID || runtime["ready"] != true || runtime["started"] != true || len(state) != 1 || startedErr != nil {
		return false
	}
	// CNPG's sole sequential bootstrap must be complete, never an active sidecar.
	init, initOK := spec["initContainers"].([]any)
	completed, completedOK := status["initContainerStatuses"].([]any)
	if !initOK || len(init) != 1 || !completedOK || len(completed) != 1 {
		return false
	}
	bootstrap, ok := init[0].(map[string]any)
	done, doneOK := completed[0].(map[string]any)
	doneState := clusterSSHStorageMap(done, "state")
	terminated := clusterSSHStorageMap(doneState, "terminated")
	exit, exitOK := clusterSSHStorageInteger(terminated["exitCode"])
	return ok && doneOK && bootstrap["name"] == "bootstrap-controller" && bootstrap["restartPolicy"] == nil && done["name"] == "bootstrap-controller" && len(doneState) == 1 && exitOK && exit == 0 && terminated["reason"] == "Completed"
}

func readClusterAutomaticRecoveryDatabase(ctx context.Context, get func(context.Context, string, any) error, readSQL clusterAutomaticRecoveryDatabaseRead, b clusterAutomaticRecoveryDatabaseBinding) (clusterAutomaticRecoveryHealth, error) {
	fail := func() (clusterAutomaticRecoveryHealth, error) {
		return clusterAutomaticRecoveryHealth{}, errClusterConflict
	}
	if !b.valid() || get == nil || readSQL == nil || ctx.Err() != nil {
		return fail()
	}
	read := func() (map[string]map[string]any, error) {
		if _, err := readClusterAutomaticRecoveryNodeHealth(ctx, get, b.Node); err != nil {
			return nil, err
		}
		objects := map[string]map[string]any{}
		refs := []struct{ path, version, kind, name, uid, namespace string }{
			{"/api/v1/namespaces/borealis", "v1", "Namespace", "borealis", b.NamespaceUID, ""},
			{clusterSSHStoragePostgresPath, "postgresql.cnpg.io/v1", "Cluster", "borealis-postgres", b.ClusterUID, "borealis"},
		}
		for _, m := range b.Members {
			refs = append(refs, struct{ path, version, kind, name, uid, namespace string }{"/api/v1/nodes/" + m.NodeName, "v1", "Node", m.NodeName, m.NodeUID, ""})
		}
		for _, ref := range refs {
			var object clusterNativeObservationObject
			if get(ctx, ref.path, &object) != nil || ctx.Err() != nil || !recoveryDatabaseExactFields(object) {
				return nil, errClusterConflict
			}
			id, ok := clusterSSHStorageMetadata(object, ref.version, ref.kind, ref.namespace)
			if !ok || id.Name != ref.name || id.UID != ref.uid {
				return nil, errClusterConflict
			}
			if ref.kind == "Node" && (!validSourceConditions(clusterSSHStorageMap(object, "status")) || !sourceConditionTrue(clusterSSHStorageMap(object, "status"), "Ready")) {
				return nil, errClusterConflict
			}
			objects[ref.path] = object
		}
		cluster := objects[clusterSSHStoragePostgresPath]
		if !recoveryDatabaseCluster(cluster, b) {
			return nil, errClusterConflict
		}
		var list clusterNativeObservationObject
		if get(ctx, clusterSSHStoragePodsPath+"&limit=4", &list) != nil || ctx.Err() != nil || !recoveryDatabaseExactFields(list) {
			return nil, errClusterConflict
		}
		pods, ok := clusterSSHStorageList(list, "Pod", "borealis", 3)
		if !ok || len(pods) != 3 {
			return nil, errClusterConflict
		}
		owner, _ := clusterSSHStorageMetadata(cluster, "postgresql.cnpg.io/v1", "Cluster", "borealis")
		seen := map[string]bool{}
		for _, pod := range pods {
			name := clusterSSHStorageText(clusterSSHStorageMap(pod, "metadata"), "name")
			matched := false
			for _, m := range b.Members {
				if m.Pod == name {
					matched = recoveryDatabasePod(pod, m, owner)
				}
			}
			if !matched || seen[name] {
				return nil, errClusterConflict
			}
			seen[name] = true
			objects["pod/"+name] = pod
		}
		return objects, nil
	}
	before, err := read()
	if err != nil {
		return fail()
	}
	rows, err := readSQL(ctx)
	if err != nil || ctx.Err() != nil || !recoveryDatabaseReplication(b, rows) {
		return fail()
	}
	after, err := read()
	if err != nil || ctx.Err() != nil || !reflect.DeepEqual(before, after) {
		return fail()
	}
	return clusterAutomaticRecoveryHealth{NodeName: b.Node.NodeName, NodeUID: b.Node.NodeUID, Node: true}, nil
}

func (c *clusterAutomaticRecoveryCoordinator) databaseHealthObserver(b clusterAutomaticRecoveryDatabaseBinding) clusterAutomaticRecoveryObserve {
	return c.databaseHealthObserverWithRead(b, nil)
}

// Only received-feedback progress across successful observations can turn a
// snapshot into Database health. reply_time is the standby SEND timestamp;
// comparing it with either local or primary wall time is not freshness proof.
type clusterAutomaticRecoveryDatabaseHistory struct {
	previousStart time.Time
	replies       [2]time.Time
	progress      [2]time.Time
}

func (h clusterAutomaticRecoveryDatabaseHistory) advance(b clusterAutomaticRecoveryDatabaseBinding, rows []clusterAutomaticRecoveryDatabaseRow, start, complete time.Time) (clusterAutomaticRecoveryDatabaseHistory, bool) {
	var baseline clusterAutomaticRecoveryDatabaseHistory
	if !recoveryDatabaseReplication(b, rows) || start.IsZero() || complete.Before(start) || complete.Sub(start) > clusterControllerLeaseAcquireTimeout {
		return baseline, false
	}
	baseline.previousStart = start
	i := 0
	for _, member := range b.Members {
		if member.Pod == b.PrimaryPod {
			continue
		}
		for _, row := range rows {
			if row.Application.String == member.Pod {
				baseline.replies[i] = row.Reply.Time
			}
		}
		i++
	}
	if h.previousStart.IsZero() || start.Before(h.previousStart) || complete.Sub(h.previousStart) > clusterControllerLeaseRenewalGrace {
		return baseline, false
	}
	next := baseline
	for i, reply := range baseline.replies {
		if reply.Before(h.replies[i]) {
			return baseline, false
		}
		next.progress[i] = h.progress[i]
		if reply.After(h.replies[i]) {
			// The primary received newer feedback after the previous SQL observation
			// began. Never date this receipt at the current sample/standby clock.
			next.progress[i] = h.previousStart
		}
		if !next.progress[i].IsZero() && (complete.Before(next.progress[i]) || complete.Sub(next.progress[i]) > clusterControllerLeaseRenewalGrace) {
			return baseline, false
		}
	}
	return next, !next.progress[0].IsZero() && !next.progress[1].IsZero()
}

type clusterAutomaticRecoveryDatabaseContinuity struct {
	mu                sync.Mutex
	busy, invalidated bool
	session           string
	history           clusterAutomaticRecoveryDatabaseHistory
}

func (s *clusterAutomaticRecoveryDatabaseContinuity) begin(session string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		s.invalidated = true
		s.history = clusterAutomaticRecoveryDatabaseHistory{}
		return false
	}
	if session == "" || session != s.session {
		s.history = clusterAutomaticRecoveryDatabaseHistory{}
		s.session = session
	}
	if session == "" {
		return false
	}
	s.busy = true
	return true
}

func (s *clusterAutomaticRecoveryDatabaseContinuity) finish(b clusterAutomaticRecoveryDatabaseBinding, rows []clusterAutomaticRecoveryDatabaseRow, start, complete time.Time, valid bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.busy = false
	if !valid || s.invalidated {
		s.invalidated = false
		s.history = clusterAutomaticRecoveryDatabaseHistory{}
		return false
	}
	var healthy bool
	s.history, healthy = s.history.advance(b, rows, start, complete)
	return healthy
}

func (c *clusterAutomaticRecoveryCoordinator) databaseHealthObserverWithRead(b clusterAutomaticRecoveryDatabaseBinding, readSQL clusterAutomaticRecoveryDatabaseRead) clusterAutomaticRecoveryObserve {
	return c.databaseHealthObserverWithClock(b, readSQL, time.Now)
}

func (c *clusterAutomaticRecoveryCoordinator) databaseHealthObserverWithClock(b clusterAutomaticRecoveryDatabaseBinding, readSQL clusterAutomaticRecoveryDatabaseRead, clock func() time.Time) clusterAutomaticRecoveryObserve {
	continuity := &clusterAutomaticRecoveryDatabaseContinuity{}
	return func(parent context.Context, node clusterControllerNode) (clusterAutomaticRecoveryHealth, error) {
		fail := func() (clusterAutomaticRecoveryHealth, error) {
			return clusterAutomaticRecoveryHealth{}, errClusterConflict
		}
		if !continuity.begin(clusterAutomaticRecoveryObserverSession(parent)) {
			return fail()
		}
		var rows []clusterAutomaticRecoveryDatabaseRow
		var start time.Time
		if clock == nil {
			continuity.finish(b, nil, start, start, false)
			return fail()
		}
		// The wrapper's final SQL authority check must succeed BEFORE committing
		// history. Neither this mutex nor a SQL connection spans native IO.
		observer := c.nativeHealthObserver(b.Node, func(ctx context.Context, kube *kubernetesAPIClient) (clusterAutomaticRecoveryHealth, error) {
			if !b.valid() {
				return fail()
			}
			runner, ok := c.controller.runner.(*kubernetesClusterStepRunner)
			if !ok || runner.namespace != "borealis" || runner.db != c.controller.store.db {
				return fail()
			}
			snapshot, _, err := readClusterAutomaticRecoverySnapshot(ctx, c.controller.store.db, b.Node.Controller)
			if err != nil || snapshot.ClusterID != b.Node.ClusterID {
				return fail()
			}
			for _, m := range b.Members {
				if node := clusterNodeByID(snapshot.Nodes, m.NodeID); node.ID != m.NodeID || node.Name != m.NodeName {
					return fail()
				}
			}
			probe := readSQL
			if probe == nil {
				probe = func(ctx context.Context) ([]clusterAutomaticRecoveryDatabaseRow, error) {
					return readClusterAutomaticRecoveryDatabaseSQL(ctx, runner.db)
				}
			}
			capture := func(ctx context.Context) ([]clusterAutomaticRecoveryDatabaseRow, error) {
				start = clock()
				sample, err := probe(ctx)
				rows = append([]clusterAutomaticRecoveryDatabaseRow(nil), sample...)
				return sample, err
			}
			get := func(ctx context.Context, path string, out any) error {
				return kube.clusterSSHPrivateJSON(ctx, http.MethodGet, path, nil, out)
			}
			return readClusterAutomaticRecoveryDatabase(ctx, get, capture, b)
		})
		health, err := observer(parent, node)
		if !continuity.finish(b, rows, start, clock(), err == nil && parent.Err() == nil) {
			return fail()
		}
		health.Database = true
		return health, nil
	}
}
