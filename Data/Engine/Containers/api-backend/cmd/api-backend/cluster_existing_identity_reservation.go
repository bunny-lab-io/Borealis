package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const clusterExistingIdentityKind = "ssh_identity_recovery"
const clusterExistingIdentityReserved = "identity_recovery_reserved"

// Existing members are deliberately not represented as clean onboarding hosts.
// This reservation grants neither a target-worker lease nor mutation authority.
type clusterExistingIdentityMember struct {
	ID, Name, Address, Architecture, ApplicationState string
	Release, SHA, DrainReason                         string
}

type clusterExistingIdentitySnapshot struct {
	ClusterID, Release, SHA, Config, VIP, Controller string
	MaintenanceID, MaintenancePayload, TargetID      string
	MaintenanceAttempt, StartedAt, FinishedAt        int64
	Members                                          []clusterExistingIdentityMember
}

type clusterExistingIdentityPlan struct {
	Version                                        int
	OperationID, SourceID, TargetID, MaintenanceID string
	MaintenanceAttempt, ObservedAt                 int64
	Controller, ClusterID, KubeSystemUID           string
	Baseline                                       clusterbootstrap.Expected
	ConfigSHA256, SnapshotSHA256                   string
	Members                                        []clusterSSHSourceMember
}

type clusterExistingIdentityReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// One SQL snapshot, fully released before parsing configuration or Kubernetes IO.
func readClusterExistingIdentitySnapshot(ctx context.Context, reader clusterExistingIdentityReader, holder, maintenance string) (clusterExistingIdentitySnapshot, int64, error) {
	fail := func() (clusterExistingIdentitySnapshot, int64, error) {
		return clusterExistingIdentitySnapshot{}, 0, errClusterConflict
	}
	if reader == nil || holder == "" || len(holder) > 1024 || !clusterUUIDRE.MatchString(maintenance) || ctx.Err() != nil {
		return fail()
	}
	rows, err := reader.QueryContext(ctx, `SELECT c.cluster_id,c.baseline_release,c.baseline_sha,c.config_json,c.control_plane_vip,l.holder,
 m.id,m.payload_json,m.target_node_id,m.attempt,m.started_at,m.finished_at,
 n.id,n.node_name,n.management_ip,n.architecture,n.application_state,COALESCE(n.release_tag,''),COALESCE(n.release_sha,''),COALESCE(n.drain_reason,''),
 floor(extract(epoch FROM statement_timestamp()))::bigint
 FROM engine.cluster_state c
 JOIN engine.cluster_application_leases l ON l.name=$1 AND l.holder=$2 AND l.expires_at>extract(epoch FROM statement_timestamp())
 JOIN engine.cluster_operations m ON m.id=$3 AND m.kind='node_maintenance' AND m.state='succeeded' AND m.current_step='complete'
 JOIN engine.cluster_nodes n ON n.membership_state='Active'
 WHERE c.id=1 AND c.enabled=1 AND c.active_size=3 AND c.desired_size=3 AND c.status='Healthy' AND c.hmr_state='inactive'
 AND COALESCE(c.active_operation_id,'')='' AND c.control_plane_vip=c.edge_vip AND octet_length(c.config_json)<=65536
 AND octet_length(m.payload_json)<=65536 AND m.attempt>0 AND m.started_at>0 AND m.finished_at>=m.started_at
 AND m.finished_at<=extract(epoch FROM statement_timestamp())
 AND NOT EXISTS(SELECT 1 FROM engine.cluster_operations o WHERE o.state IN ('queued','running','waiting'))
 AND NOT EXISTS(SELECT 1 FROM engine.cluster_operations o WHERE o.id<>m.id AND o.kind='node_maintenance' AND o.target_node_id=m.target_node_id
 AND (o.created_at>=m.started_at OR o.finished_at>=m.finished_at))
 AND NOT EXISTS(SELECT 1 FROM engine.cluster_admissions a WHERE a.state IN ('Pending Quorum','Approved','Recovery Required'))
 ORDER BY n.id LIMIT 4`, clusterControllerLeaseName, holder, maintenance)
	if err != nil {
		return fail()
	}
	var snapshot clusterExistingIdentitySnapshot
	var now int64
	for rows.Next() {
		var member clusterExistingIdentityMember
		if rows.Scan(&snapshot.ClusterID, &snapshot.Release, &snapshot.SHA, &snapshot.Config, &snapshot.VIP, &snapshot.Controller,
			&snapshot.MaintenanceID, &snapshot.MaintenancePayload, &snapshot.TargetID, &snapshot.MaintenanceAttempt, &snapshot.StartedAt, &snapshot.FinishedAt,
			&member.ID, &member.Name, &member.Address, &member.Architecture, &member.ApplicationState, &member.Release, &member.SHA, &member.DrainReason, &now) != nil {
			rows.Close()
			return fail()
		}
		snapshot.Members = append(snapshot.Members, member)
	}
	err, closeErr := rows.Err(), rows.Close()
	if err != nil || closeErr != nil || len(snapshot.Members) != 3 || ctx.Err() != nil {
		return fail()
	}
	return snapshot, now, nil
}

func (s clusterExistingIdentitySnapshot) valid(source string) bool {
	if !clusterUUIDRE.MatchString(s.ClusterID) || !clusterUUIDRE.MatchString(source) || !clusterUUIDRE.MatchString(s.TargetID) || source == s.TargetID ||
		len(s.Members) != 3 || (clusterremote.Target{Address: s.VIP, Port: 22}).Validate() != nil {
		return false
	}
	baseline := clusterbootstrap.Expected{Repository: clusterGitHubRepo(), Release: s.Release, SourceSHA: s.SHA, AllowQualification: strings.Contains(s.Release, "-rc.")}
	if baseline.Validate() != nil || clusterDatabaseRuntimeRequiresRecovery(s.Config) {
		return false
	}
	var config, maintenance map[string]any
	if json.Unmarshal([]byte(s.Config), &config) != nil || !clusterK3sRE.MatchString(cleanText(config["k3s_version"])) ||
		json.Unmarshal([]byte(s.MaintenancePayload), &maintenance) != nil || cleanText(maintenance["action"]) != "enter" {
		return false
	}
	seenID, seenName, seenAddress := map[string]bool{}, map[string]bool{}, map[string]bool{}
	foundSource, foundTarget := false, false
	for _, m := range s.Members {
		if !clusterUUIDRE.MatchString(m.ID) || len(validateClusterNodeName("node", m.Name)) != 0 ||
			(clusterremote.Target{Address: m.Address, Port: 22}).Validate() != nil || m.Address == s.VIP || m.Architecture != "amd64" ||
			m.Release != s.Release || m.SHA != s.SHA || seenID[m.ID] || seenName[m.Name] || seenAddress[m.Address] {
			return false
		}
		seenID[m.ID], seenName[m.Name], seenAddress[m.Address] = true, true, true
		if m.ID == s.TargetID {
			if m.ApplicationState != "drained" || m.DrainReason != cleanText(maintenance["reason"]) {
				return false
			}
			foundTarget = true
		} else if m.ApplicationState != "active" {
			return false
		}
		if m.ID == source {
			foundSource = true
		}
	}
	return foundSource && foundTarget
}

// Explicit source ID is pinned by caller; never choose a source by sort order.
// Complete native Node evidence is repeated before the short SQL reservation.
func observeClusterExistingIdentityNodes(ctx context.Context, getJSON func(context.Context, string, any) error, s clusterExistingIdentitySnapshot) (string, []clusterSSHSourceMember, error) {
	fail := func() (string, []clusterSSHSourceMember, error) { return "", nil, errClusterConflict }
	if getJSON == nil || ctx.Err() != nil {
		return fail()
	}
	readNamespace := func() (string, error) {
		var ns struct {
			Metadata struct {
				UID string `json:"uid"`
			} `json:"metadata"`
		}
		if getJSON(ctx, "/api/v1/namespaces/kube-system", &ns) != nil || !clusterUUIDRE.MatchString(ns.Metadata.UID) || ctx.Err() != nil {
			return "", errClusterConflict
		}
		return ns.Metadata.UID, nil
	}
	before, err := readNamespace()
	if err != nil {
		return fail()
	}
	var nodes struct {
		Metadata struct {
			Continue string `json:"continue"`
		} `json:"metadata"`
		Items []clusterSSHIdentityRecoveryKubernetesNode `json:"items"`
	}
	if getJSON(ctx, "/api/v1/nodes", &nodes) != nil || nodes.Metadata.Continue != "" || len(nodes.Items) != 3 || ctx.Err() != nil {
		return fail()
	}
	byName := map[string]clusterExistingIdentityMember{}
	for _, m := range s.Members {
		byName[m.Name] = m
	}
	seenUID, seenMachine, seenBoot := map[string]bool{}, map[string]bool{}, map[string]bool{}
	var observed []clusterSSHSourceMember
	for _, node := range nodes.Items {
		m, ok := byName[node.Metadata.Name]
		if !ok || node.Metadata.DeletionTimestamp != nil {
			return fail()
		}
		delete(byName, m.Name)
		uid, machine, boot := node.Metadata.UID, node.Status.NodeInfo.MachineID, node.Status.NodeInfo.BootID
		if !clusterUUIDRE.MatchString(uid) || !clusterUUIDRE.MatchString(boot) || !clusterSSHMachineID.MatchString(machine) ||
			uid == "00000000-0000-0000-0000-000000000000" || boot == "00000000-0000-0000-0000-000000000000" || machine == strings.Repeat("0", 32) ||
			seenUID[uid] || seenMachine[machine] || seenBoot[boot] {
			return fail()
		}
		seenUID[uid], seenMachine[machine], seenBoot[boot] = true, true, true
		address, ready := "", ""
		for _, a := range node.Status.Addresses {
			if a.Type == "InternalIP" {
				if address != "" {
					return fail()
				}
				address = a.Address
			}
		}
		for _, c := range node.Status.Conditions {
			if c.Type == "Ready" {
				if ready != "" {
					return fail()
				}
				ready = c.Status
			}
		}
		if address != m.Address || (ready != "True" && ready != "False" && ready != "Unknown") || (m.ID != s.TargetID && ready != "True") ||
			node.Metadata.Labels["borealis.io/engine-node"] != "true" || node.Metadata.Labels["borealis.io/application-state"] != m.ApplicationState {
			return fail()
		}
		observed = append(observed, clusterSSHSourceMember{NodeID: m.ID, NodeUID: uid, Name: m.Name, Address: m.Address, MachineID: machine, BootID: boot})
	}
	after, err := readNamespace()
	if err != nil || before != after || len(byName) != 0 || ctx.Err() != nil {
		return fail()
	}
	slices.SortFunc(observed, func(a, b clusterSSHSourceMember) int { return strings.Compare(a.NodeID, b.NodeID) })
	return before, observed, nil
}

// Reservation remains inert. No credentials, target rows, host writes, restart,
// admission or takeover permission is created. Future dispatch must consume a
// separately reviewed exact confirmation, SSH binding and live proof capability.
func (c *clusterController) reserveClusterExistingIdentity(parent context.Context, id, maintenance, source, target, actor string, getJSON func(context.Context, string, any) error) (clusterExistingIdentityPlan, error) {
	fail := func() (clusterExistingIdentityPlan, error) { return clusterExistingIdentityPlan{}, errClusterConflict }
	if c == nil || c.store == nil || c.store.db == nil || !clusterUUIDRE.MatchString(id) || !clusterUUIDRE.MatchString(target) || !utf8.ValidString(actor) ||
		len(actor) == 0 || len(actor) > 128 || strings.TrimSpace(actor) != actor || strings.IndexFunc(actor, unicode.IsControl) >= 0 {
		return fail()
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	snapshot, now, err := readClusterExistingIdentitySnapshot(ctx, c.store.db, c.holder, maintenance)
	if err != nil || snapshot.TargetID != target || !snapshot.valid(source) {
		return fail()
	}
	namespace, members, err := observeClusterExistingIdentityNodes(ctx, getJSON, snapshot)
	if err != nil {
		return fail()
	}
	again, repeated, err := observeClusterExistingIdentityNodes(ctx, getJSON, snapshot)
	if err != nil || namespace != again || !reflect.DeepEqual(members, repeated) {
		return fail()
	}
	configDigest, err := clusterSSHPreparationDigest(snapshot.Config)
	if err != nil {
		return fail()
	}
	snapshotDigest, err := clusterSSHPreparationDigest(snapshot)
	if err != nil {
		return fail()
	}
	plan := clusterExistingIdentityPlan{Version: 1, OperationID: id, SourceID: source, TargetID: snapshot.TargetID, MaintenanceID: maintenance,
		MaintenanceAttempt: snapshot.MaintenanceAttempt, ObservedAt: now, Controller: c.holder, ClusterID: snapshot.ClusterID, KubeSystemUID: namespace,
		Baseline: clusterbootstrap.Expected{Repository: clusterGitHubRepo(), Release: snapshot.Release, SourceSHA: snapshot.SHA, AllowQualification: strings.Contains(snapshot.Release, "-rc.")}, ConfigSHA256: configDigest, SnapshotSHA256: snapshotDigest, Members: members}
	payload, err := json.Marshal(plan)
	if err != nil || !plan.valid() || len(payload) > 64<<10 {
		return fail()
	}
	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return fail()
	}
	defer tx.Rollback()
	var present int
	if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_application_leases WHERE name=$1 FOR UPDATE`, clusterControllerLeaseName).Scan(&present) != nil ||
		tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_state WHERE id=1 FOR UPDATE`).Scan(&present) != nil ||
		tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_operations WHERE id=$1 FOR UPDATE`, maintenance).Scan(&present) != nil {
		return fail()
	}
	for _, m := range snapshot.Members {
		if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_nodes WHERE id=$1 FOR SHARE`, m.ID).Scan(&present) != nil {
			return fail()
		}
	}
	current, _, err := readClusterExistingIdentitySnapshot(ctx, tx, c.holder, maintenance)
	if err != nil || !reflect.DeepEqual(snapshot, current) || ctx.Err() != nil {
		return fail()
	}
	// Fresh DB clock follows every lock wait; claim no historical lease authority.
	if tx.QueryRowContext(ctx, `SELECT floor(extract(epoch FROM clock_timestamp()))::bigint FROM engine.cluster_application_leases
 WHERE name=$1 AND holder=$2 AND expires_at>extract(epoch FROM clock_timestamp())`, clusterControllerLeaseName, c.holder).Scan(&now) != nil {
		return fail()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO engine.cluster_operations(id,kind,state,current_step,target_node_id,target_release,target_sha,requested_by,payload_json,created_at,updated_at)
 VALUES($1,$2,'waiting',$3,$4,$5,$6,$7,$8,$9,$9)`, id, clusterExistingIdentityKind, clusterExistingIdentityReserved, snapshot.TargetID, snapshot.Release, snapshot.SHA, actor, string(payload), now); err != nil {
		return fail()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_state SET active_operation_id=$1,updated_at=$2 WHERE id=1`, id, now); err != nil {
		return fail()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO engine.cluster_operation_events(operation_id,cluster_id,event_type,state,message,details_json,created_at)
 VALUES($1,$2,'existing_identity_reserved','waiting','Existing member recovery reserved; execution remains disabled.','{}',$3)`, id, snapshot.ClusterID, now); err != nil {
		return fail()
	}
	if ctx.Err() != nil || tx.Commit() != nil {
		return fail()
	}
	return plan, nil
}
