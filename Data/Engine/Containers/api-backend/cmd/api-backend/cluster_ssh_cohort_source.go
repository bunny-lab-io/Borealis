package main

import (
	"context"
	"reflect"
	"time"
)

// Load controller-owned topology before contacting Kubernetes. The source is
// read-only; any later approval must compare it again under controller authority.
func (s *postgresOperatorStore) loadClusterSSHSourceCohort(ctx context.Context, operationID, holder string, attempt int64) (clusterSSHSourceCohort, error) {
	if !clusterUUIDRE.MatchString(operationID) || holder == "" || attempt < 1 {
		return clusterSSHSourceCohort{}, errClusterSSHCohort
	}
	rows, err := s.db.QueryContext(ctx, `SELECT c.cluster_id,c.active_size,c.desired_size,c.status,c.hmr_state,
  COALESCE(c.control_plane_vip,''),COALESCE(c.edge_vip,''),n.id,n.node_name,n.management_ip
  FROM engine.cluster_state c
  JOIN engine.cluster_operations o ON o.id=c.active_operation_id
  JOIN engine.cluster_application_leases l ON l.name=$4 AND l.holder=$2
  JOIN engine.cluster_nodes n ON n.membership_state='Active'
  WHERE c.id=1 AND c.enabled=1 AND o.id=$1 AND o.kind='ssh_onboarding' AND o.state='running' AND o.attempt=$3
   AND o.current_step='inspect_ssh_targets' AND l.expires_at>extract(epoch FROM clock_timestamp())
  ORDER BY n.node_name LIMIT 4`, operationID, holder, attempt, clusterControllerLeaseName)
	if err != nil {
		return clusterSSHSourceCohort{}, errClusterUnavailable
	}
	var source clusterSSHSourceCohort
	for rows.Next() {
		var member clusterSSHSourceMember
		if err := rows.Scan(&source.ClusterID, &source.ActiveSize, &source.DesiredSize, &source.Status, &source.HMRState, &source.ControlPlaneVIP, &source.EdgeVIP, &member.NodeID, &member.Name, &member.Address); err != nil {
			rows.Close()
			return clusterSSHSourceCohort{}, errClusterUnavailable
		}
		source.Members = append(source.Members, member)
	}
	scanErr, closeErr := rows.Err(), rows.Close()
	if scanErr != nil || closeErr != nil {
		return clusterSSHSourceCohort{}, errClusterUnavailable
	}
	if len(source.Members) != int(source.ActiveSize) || len(source.Members) < 1 || len(source.Members) > 2 {
		return clusterSSHSourceCohort{}, errClusterSSHCohort
	}
	return source, nil
}

// Public Kubernetes evidence only. Read list once so all member identities come
// from one API snapshot; reject extra/unrecorded nodes before provisioning.
// A machine ID or Node UID is never inferred from a hostname or address.
func observeClusterSSHSourceCohort(ctx context.Context, getJSON func(context.Context, string, any) error, source clusterSSHSourceCohort) (clusterSSHSourceCohort, error) {
	if getJSON == nil {
		return clusterSSHSourceCohort{}, errClusterUnavailable
	}
	if len(source.Members) < 1 || len(source.Members) > 2 {
		return clusterSSHSourceCohort{}, errClusterSSHCohort
	}
	var namespace struct {
		Metadata struct {
			UID string `json:"uid"`
		} `json:"metadata"`
	}
	if err := getJSON(ctx, "/api/v1/namespaces/kube-system", &namespace); err != nil {
		return clusterSSHSourceCohort{}, errClusterUnavailable
	}
	if !clusterUUIDRE.MatchString(namespace.Metadata.UID) {
		return clusterSSHSourceCohort{}, errClusterSSHCohort
	}
	var nodes struct {
		Items []struct {
			Metadata struct {
				Name              string  `json:"name"`
				UID               string  `json:"uid"`
				DeletionTimestamp *string `json:"deletionTimestamp"`
			} `json:"metadata"`
			Status struct {
				NodeInfo struct {
					MachineID string `json:"machineID"`
					BootID    string `json:"bootID"`
				} `json:"nodeInfo"`
				Addresses []struct {
					Type    string `json:"type"`
					Address string `json:"address"`
				} `json:"addresses"`
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := getJSON(ctx, "/api/v1/nodes", &nodes); err != nil {
		return clusterSSHSourceCohort{}, errClusterUnavailable
	}
	if len(nodes.Items) != len(source.Members) {
		return clusterSSHSourceCohort{}, errClusterSSHCohort
	}
	source.Members = append([]clusterSSHSourceMember(nil), source.Members...)
	seen := map[string]bool{}
	for _, node := range nodes.Items {
		if seen[node.Metadata.Name] || node.Metadata.DeletionTimestamp != nil {
			return clusterSSHSourceCohort{}, errClusterSSHCohort
		}
		seen[node.Metadata.Name] = true
		found := false
		for i := range source.Members {
			member := &source.Members[i]
			if member.Name != node.Metadata.Name {
				continue
			}
			found = true
			addresses, ready := 0, 0
			for _, address := range node.Status.Addresses {
				if address.Type == "InternalIP" {
					addresses++
					if address.Address != member.Address {
						return clusterSSHSourceCohort{}, errClusterSSHCohort
					}
				}
			}
			for _, condition := range node.Status.Conditions {
				if condition.Type == "Ready" {
					if condition.Status != "True" {
						return clusterSSHSourceCohort{}, errClusterSSHCohort
					}
					ready++
				}
			}
			if addresses != 1 || ready != 1 || !clusterUUIDRE.MatchString(node.Metadata.UID) || !clusterUUIDRE.MatchString(node.Status.NodeInfo.BootID) ||
				!clusterSSHMachineID.MatchString(node.Status.NodeInfo.MachineID) || node.Status.NodeInfo.MachineID == "00000000000000000000000000000000" {
				return clusterSSHSourceCohort{}, errClusterSSHCohort
			}
			member.NodeUID, member.MachineID, member.BootID = node.Metadata.UID, node.Status.NodeInfo.MachineID, node.Status.NodeInfo.BootID
		}
		if !found {
			return clusterSSHSourceCohort{}, errClusterSSHCohort
		}
	}
	source.KubeSystemUID = namespace.Metadata.UID
	return source, nil
}

// Assessment composes current controller-owned DB state and live Kubernetes
// observations without holding a DB connection across the network. Rechecking
// source state afterwards detects a changed topology/holder during observation.
// It remains a read-only gate, not a durable preparation approval.
func (s *postgresOperatorStore) assessClusterSSHInspectionCohort(parent context.Context, operationID, holder string, attempt int64, getJSON func(context.Context, string, any) error) (clusterSSHInspectionCohort, clusterSSHSourceCohort, error) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	fail := func(err error) (clusterSSHInspectionCohort, clusterSSHSourceCohort, error) {
		return clusterSSHInspectionCohort{}, clusterSSHSourceCohort{}, err
	}
	source, err := s.loadClusterSSHSourceCohort(ctx, operationID, holder, attempt)
	if err != nil {
		return fail(err)
	}
	observed, err := observeClusterSSHSourceCohort(ctx, getJSON, source)
	if err != nil {
		return fail(err)
	}
	current, err := s.loadClusterSSHSourceCohort(ctx, operationID, holder, attempt)
	if err != nil {
		return fail(err)
	}
	if !reflect.DeepEqual(source, current) {
		return fail(errClusterSSHCohort)
	}
	cohort, err := s.loadClusterSSHInspectionCohort(ctx, operationID, holder, attempt)
	if err != nil {
		return fail(err)
	}
	if err := validateClusterSSHInspectionCohort(cohort, observed); err != nil {
		return fail(err)
	}
	if ctx.Err() != nil {
		return fail(errClusterSSHCohort)
	}
	return cohort, observed, nil
}
