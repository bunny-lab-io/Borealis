package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var etcdMemberNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,127}$`)

type memberRemovalIdentity struct {
	OperationID    string `json:"operation_id"`
	NodeID         string `json:"node_id"`
	NodeName       string `json:"node_name"`
	NodeUID        string `json:"node_uid"`
	EtcdMemberName string `json:"etcd_member_name"`
}

func removalIdentity(params map[string]any, nodeName string) (memberRemovalIdentity, error) {
	identity := memberRemovalIdentity{NodeName: nodeName}
	for field, target := range map[string]*string{
		"operation_id": &identity.OperationID, "node_id": &identity.NodeID, "node_uid": &identity.NodeUID,
	} {
		value, ok := params[field].(string)
		if !ok || !clusterUUIDPattern.MatchString(value) {
			return identity, fmt.Errorf("member removal requires canonical %s UUID", field)
		}
		*target = value
	}
	member, ok := params["etcd_member_name"].(string)
	if !ok || !etcdMemberNamePattern.MatchString(member) || !nodePattern.MatchString(nodeName) || params["node_name"] != nodeName {
		return identity, errors.New("member removal requires bounded K3s etcd member identity")
	}
	identity.EtcdMemberName = member
	return identity, nil
}

// The new action client must reject old managers' unbound success responses.
// Otherwise a successful Job could incorrectly certify an identity check that
// the installed host helper never performed.
func validateRemovalAcknowledgement(raw []byte, identity memberRemovalIdentity) error {
	var response struct {
		OK     bool   `json:"ok"`
		Verb   string `json:"verb"`
		Result struct {
			memberRemovalIdentity
			ServiceDisabled bool   `json:"service_disabled"`
			K3sState        string `json:"k3s_state"`
			FenceMarker     string `json:"fence_marker"`
			FenceDropIn     string `json:"fence_drop_in"`
			ArmedAt         string `json:"armed_at"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &response) != nil || !response.OK || response.Verb != "PrepareMemberRemoval" ||
		response.Result.memberRemovalIdentity != identity || !response.Result.ServiceDisabled || response.Result.K3sState != "active" ||
		response.Result.FenceMarker != memberFencePath || response.Result.FenceDropIn != memberFenceDropIn {
		return errors.New("successful identity-bound persistent fence response required; upgrade the installed node manager before planned removal")
	}
	if _, err := time.Parse(time.RFC3339Nano, response.Result.ArmedAt); err != nil {
		return errors.New("removal fence acknowledgement lacks valid armed timestamp")
	}
	return nil
}

func verifyRemovalIdentity(raw []byte, identity memberRemovalIdentity) error {
	var node struct {
		Metadata struct {
			Name        string            `json:"name"`
			UID         string            `json:"uid"`
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	}
	if json.Unmarshal(raw, &node) != nil || node.Metadata.Name != identity.NodeName ||
		node.Metadata.UID != identity.NodeUID || node.Metadata.Annotations["etcd.k3s.cattle.io/node-name"] != identity.EtcdMemberName ||
		node.Metadata.Annotations["etcd.k3s.cattle.io/removed-node-name"] != "" {
		return errors.New("local Kubernetes UID or etcd member identity does not match removal request")
	}
	return nil
}

type memberRemovalRun func(context.Context, string, string, ...string) (string, error)

func (m *manager) prepareMemberRemoval(ctx context.Context, params map[string]any) (map[string]any, error) {
	identity, err := removalIdentity(params, m.nodeName)
	if err != nil {
		return nil, err
	}
	return prepareMemberRemovalFence(ctx, identity, requiredReason(params), run, memberFencePath, memberFenceDropIn)
}

func prepareMemberRemovalFence(ctx context.Context, identity memberRemovalIdentity, reason string, command memberRemovalRun, markerPath, dropInPath string) (map[string]any, error) {
	raw, err := command(ctx, "", "k3s", "kubectl", "get", "node", identity.NodeName, "-o", "json")
	if err != nil {
		return nil, err
	}
	if err := verifyRemovalIdentity([]byte(raw), identity); err != nil {
		return nil, err
	}
	if previous, err := os.ReadFile(markerPath); err == nil {
		var recorded memberRemovalIdentity
		if json.Unmarshal(previous, &recorded) != nil || recorded != identity {
			return nil, errors.New("existing removal fence belongs to another operation or target; explicit membership recovery required")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	loadState, err := command(ctx, "", "systemctl", "show", "--property=LoadState", "--value", "k3s.service")
	if err != nil || strings.TrimSpace(loadState) != "loaded" {
		return nil, errors.New("k3s.service is unavailable for controlled membership fence")
	}
	if err := os.MkdirAll(filepath.Dir(markerPath), 0o750); err != nil {
		return nil, err
	}
	if dropInPath == memberFenceDropIn {
		if err := ensureMemberFenceDropInDirectory(); err != nil {
			return nil, err
		}
	} else if err := os.MkdirAll(filepath.Dir(dropInPath), 0o755); err != nil {
		return nil, err
	}
	marker := map[string]any{
		"operation_id": identity.OperationID, "node_id": identity.NodeID, "node_name": identity.NodeName,
		"node_uid": identity.NodeUID, "etcd_member_name": identity.EtcdMemberName,
		"reason": reason, "armed_at": time.Now().UTC().Format(time.RFC3339Nano),
		"recovery": "Remove this marker and member-removal systemd drop-in, run systemctl daemon-reload, then explicitly enable and start k3s.service only after cluster membership recovery approval.",
	}
	encoded, err := json.Marshal(marker)
	if err != nil {
		return nil, err
	}
	if err := writeRemovalFenceFile(markerPath, append(encoded, '\n'), 0o600); err != nil {
		return nil, err
	}
	if err := writeRemovalFenceFile(dropInPath, []byte("[Service]\nRestart=no\n"), 0o644); err != nil {
		return nil, err
	}
	if _, err := command(ctx, "", "systemctl", memberRemovalFenceSystemctlArgs()...); err != nil {
		return nil, err
	}
	if _, err := command(ctx, "", "systemctl", "daemon-reload"); err != nil {
		return nil, err
	}
	restartPolicy, err := command(ctx, "", "systemctl", "show", "--property=Restart", "--value", "k3s.service")
	if err != nil || strings.TrimSpace(restartPolicy) != "no" {
		return nil, errors.New("k3s.service restart policy was not fenced")
	}
	activeState, err := command(ctx, "", "systemctl", "is-active", "k3s.service")
	if err != nil || strings.TrimSpace(activeState) != "active" {
		return nil, errors.New("k3s.service stopped before managed etcd membership removal")
	}
	marker["fence_marker"], marker["fence_drop_in"] = markerPath, dropInPath
	marker["service_disabled"], marker["k3s_state"] = true, "active"
	return marker, nil
}

// Commit each persistent fence file atomically, including directory durability,
// before acknowledging service restart suppression to the controller.
func writeRemovalFenceFile(path string, content []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".removal-fence-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if _, err := file.Write(content); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	parent, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.Sync()
}
