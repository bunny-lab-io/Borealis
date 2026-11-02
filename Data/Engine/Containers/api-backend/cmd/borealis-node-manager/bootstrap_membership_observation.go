package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/netip"
	"os"
	"reflect"
	"slices"
	"strings"
	"time"
)

// Historical completion inputs remain evidence, never live worker claims.
// Caller must derive these from the original controller's prepared-v2 snapshot.
type bootstrapMembershipPreparedTarget struct {
	Expected clusterbootstrap.PreparationExpected
	Receipt  clusterbootstrap.PrejoinPreparationReceipt
}

// These identities and member annotations are controller-provided expectations.
// Only the selected local source's Node is independently read by this adapter.
type bootstrapMembershipNodeExpectation struct {
	Node       clusterbootstrap.K3sJoinedNode
	MemberName string
}
type bootstrapMembershipObservationRequest struct {
	Source        engineidentity.Binding
	Network       clusterbootstrap.SourceNetwork
	KubeSystemUID string
	Nodes         []bootstrapMembershipNodeExpectation
	Prepared      []bootstrapMembershipPreparedTarget
}

// One original source's current complete view. This is not independent agreement
// between endpoints, quorum availability, partition proof or admission authority.
type bootstrapMembershipObservation struct {
	Source        engineidentity.Binding
	LocalNode     clusterbootstrap.K3sJoinedNode
	Membership    clusterbootstrap.K3sMembership
	RequestSHA256 string
}

func (bootstrapMembershipObservationRequest) String() string {
	return "membership observation request [internal]"
}
func (bootstrapMembershipObservationRequest) GoString() string {
	return "membership observation request [internal]"
}

func (r bootstrapMembershipObservationRequest) valid() bool {
	uuid := func(value string) bool {
		return clusterUUIDPattern.MatchString(value) && value != "00000000-0000-0000-0000-000000000000"
	}
	if !uuid(r.Source.ClusterID) || !uuid(r.Source.SourceID) || !uuid(r.Source.SourceUID) || !uuid(r.KubeSystemUID) ||
		r.Network.Validate() != nil || r.Source.SourceUID != r.Network.NodeUID || r.Network.K3sVersion != clusterbootstrap.K3sPins().Version || len(r.Nodes) != 3 || len(r.Prepared) < 1 || len(r.Prepared) > 2 {
		return false
	}
	names, uids, addresses, machines, boots := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	peers := []string{}
	local := false
	for _, expected := range r.Nodes {
		node := expected.Node
		if !clusterbootstrap.ValidK3sMemberName(expected.MemberName, node.Hostname) {
			return false
		}
		ip, err := netip.ParseAddr(node.Address)
		if err != nil || !ip.Is4() || !ip.IsPrivate() || ip.String() != node.Address || !nodePattern.MatchString(node.Hostname) || !uuid(node.NodeUID) || !uuid(node.BootID) || len(node.MachineID) != 32 || node.MachineID == strings.Repeat("0", 32) || !node.Ready || node.K3sVersion != r.Network.K3sVersion || names[node.Hostname] || uids[node.NodeUID] || addresses[node.Address] || machines[node.MachineID] || boots[node.BootID] {
			return false
		}
		for _, c := range node.MachineID {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return false
			}
		}
		names[node.Hostname], uids[node.NodeUID], addresses[node.Address], machines[node.MachineID], boots[node.BootID] = true, true, true, true, true
		peers = append(peers, node.Address)
		if node.NodeUID == r.Source.SourceUID {
			local = node.Hostname == r.Network.Hostname && node.MachineID == r.Network.MachineID && node.BootID == r.Network.BootID && r.Network.ManagementLink.MatchesAddress(node.Address)
		}
	}
	if !local {
		return false
	}
	slices.Sort(peers)
	prepared, targets, ids := map[string]bool{}, map[string]bool{}, map[uint64]bool{}
	for i, p := range r.Prepared {
		e := p.Expected
		if e.Validate() != nil || p.Receipt.Validate(e, r.Source) != nil || e.Target.ClusterID != r.Source.ClusterID || e.KubeSystemUID != r.KubeSystemUID || e.K3sVersion != r.Network.K3sVersion || e.PodCIDR != r.Network.PodCIDR || e.ServiceCIDR != r.Network.ServiceCIDR || !slices.Equal(e.PeerAddresses, peers) || p.Receipt.Node.NodeUID == r.Source.SourceUID || prepared[p.Receipt.Node.NodeUID] || targets[e.Target.TargetID] || ids[p.Receipt.MemberID] {
			return false
		}
		found := false
		for _, node := range r.Nodes {
			found = found || node.Node == p.Receipt.Node && node.MemberName == p.Receipt.MemberName
		}
		if !found {
			return false
		}
		if i > 0 {
			first := r.Prepared[0].Expected
			if e.Target.OperationID != first.Target.OperationID || e.Target.OperationAttempt != first.Target.OperationAttempt || e.Source != first.Source || e.CohortSHA256 != first.CohortSHA256 || e.ManagementCIDR != first.ManagementCIDR || e.ControlPlaneVIP != first.ControlPlaneVIP || e.EdgeVIP != first.EdgeVIP {
				return false
			}
		}
		prepared[p.Receipt.Node.NodeUID], targets[e.Target.TargetID], ids[p.Receipt.MemberID] = true, true, true
	}
	return true
}

// Callable only. No registered action, result transport, service start or SQL
// authority exists here; the future caller must bracket this with its own exact
// prepared parent/controller/payload checks. Local reads independently bind the
// selected installed source's current host and runtime identity.
// Namespace/remote-Node identity is provided expectation only. The future
// controller must independently bracket this with fresh whole-Node/namespace
// reads using its existing authority; kubelet credentials cannot provide those.
func (m *manager) observeBootstrapMembership(parent context.Context, request bootstrapMembershipObservationRequest, authority func(context.Context) error) (bootstrapMembershipObservation, error) {
	if m == nil || os.Geteuid() != 0 || os.Getegid() != 0 || m.nodeName != request.Network.Hostname || authority == nil {
		return bootstrapMembershipObservation{}, errBootstrapSource
	}
	root, err := os.OpenRoot("/")
	if err != nil {
		return bootstrapMembershipObservation{}, errBootstrapSource
	}
	defer root.Close()
	check := func(ctx context.Context) error {
		if ctx.Err() != nil || authority(ctx) != nil {
			return errBootstrapSource
		}
		hostname, err := os.Hostname()
		if err != nil || hostname != request.Network.Hostname {
			return errBootstrapSource
		}
		for path, want := range map[string]string{"/etc/machine-id": request.Network.MachineID, "/proc/sys/kernel/random/boot_id": request.Network.BootID} {
			raw, err := readBootstrapPublicFile(path, 128)
			if err != nil || strings.TrimSpace(string(raw)) != want {
				return errBootstrapSource
			}
		}
		return nil
	}
	source := func(ctx context.Context) (clusterbootstrap.SourceNetwork, error) {
		result, err := m.inspectSourceNetwork(ctx)
		value, ok := result["source_network"].(clusterbootstrap.SourceNetwork)
		if err != nil || !ok {
			return clusterbootstrap.SourceNetwork{}, errBootstrapSource
		}
		return value, nil
	}
	return observeBootstrapMembershipAt(parent, root, request, check, source, newBootstrapJoinedNodeClient)
}

func observeBootstrapMembershipAt(parent context.Context, root *os.Root, request bootstrapMembershipObservationRequest,
	check func(context.Context) error, sourceRead func(context.Context) (clusterbootstrap.SourceNetwork, error), clientFor func(*tls.Config) *http.Client) (bootstrapMembershipObservation, error) {
	fail := func() (bootstrapMembershipObservation, error) {
		return bootstrapMembershipObservation{}, errBootstrapSource
	}
	if root == nil || check == nil || sourceRead == nil || clientFor == nil || !request.valid() || parent.Err() != nil {
		return fail()
	}
	request.Nodes = slices.Clone(request.Nodes)
	request.Prepared = slices.Clone(request.Prepared)
	for i := range request.Prepared {
		request.Prepared[i].Expected.PeerAddresses = slices.Clone(request.Prepared[i].Expected.PeerAddresses)
	}
	wire, err := json.Marshal(struct {
		Domain  string
		Request bootstrapMembershipObservationRequest
	}{"original-source-membership-observation-v1", request})
	if err != nil {
		return fail()
	}
	digest := sha256.Sum256(wire)
	clear(wire)
	requestSHA256 := hex.EncodeToString(digest[:])
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	prove := func() bool {
		if check(ctx) != nil {
			return false
		}
		network, err := sourceRead(ctx)
		return err == nil && network == request.Network && check(ctx) == nil
	}
	if !prove() {
		return fail()
	}
	var result bootstrapMembershipObservation
	err = withBootstrapK3sObservationFiles(ctx, root, request.Network.Hostname, true, check, func(files []bootstrapHostFile, refresh func(context.Context) error, localName string) error {
		pair, err := tls.X509KeyPair(files[1].data, files[2].data)
		if err != nil || len(pair.Certificate) == 0 {
			return errBootstrapSource
		}
		leaf, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil || leaf.Subject.CommonName != "system:node:"+request.Network.Hostname || len(leaf.Subject.Organization) != 1 || leaf.Subject.Organization[0] != "system:nodes" {
			return errBootstrapSource
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(files[0].data) {
			return errBootstrapSource
		}
		config := &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
		defer func() { pair.PrivateKey = nil; config.Certificates = nil; config.RootCAs = nil }()
		client := clientFor(config)
		if client == nil {
			return errBootstrapSource
		}
		defer client.CloseIdleConnections()
		get := func(path string) ([]byte, error) {
			if path != "/version" && path != "/db/info" && path != "/api/v1/nodes/"+request.Network.Hostname || ctx.Err() != nil || refresh(ctx) != nil {
				return nil, errBootstrapSource
			}
			raw, err := sourceNetworkHTTPGet(ctx, client, "https://127.0.0.1:6443", path)
			if err != nil || refresh(ctx) != nil {
				clear(raw)
				return nil, errBootstrapSource
			}
			return raw, nil
		}
		version := func() error {
			raw, err := get("/version")
			defer clear(raw)
			if err != nil {
				return errBootstrapSource
			}
			return clusterbootstrap.ValidateSourceVersion(raw, request.Network.K3sVersion)
		}
		readMembership := func() (clusterbootstrap.K3sMembership, error) {
			raw, err := get("/db/info")
			defer clear(raw)
			if err != nil {
				return clusterbootstrap.K3sMembership{}, errBootstrapSource
			}
			local := clusterbootstrap.K3sJoinedNodeIdentity{Hostname: request.Network.Hostname, MachineID: request.Network.MachineID, BootID: request.Network.BootID, K3sVersion: request.Network.K3sVersion}
			for _, node := range request.Nodes {
				if node.Node.NodeUID == request.Source.SourceUID {
					local.Address = node.Node.Address
				}
			}
			value, err := clusterbootstrap.ParseK3sMembershipIdentity(raw, local, request.Prepared[0].Expected.PeerAddresses, localName)
			if err != nil || len(value.Members) != 3 || !value.Voting {
				return clusterbootstrap.K3sMembership{}, errBootstrapSource
			}
			for _, member := range value.Members {
				if member.Learner {
					return clusterbootstrap.K3sMembership{}, errBootstrapSource
				}
				matched := false
				for _, expected := range request.Nodes {
					matched = matched || member.Address == expected.Node.Address && member.Name == expected.MemberName
				}
				if !matched {
					return clusterbootstrap.K3sMembership{}, errBootstrapSource
				}
				for _, target := range request.Prepared {
					if member.Address == target.Receipt.Node.Address && (member.ID != target.Receipt.MemberID || member.Name != target.Receipt.MemberName) {
						return clusterbootstrap.K3sMembership{}, errBootstrapSource
					}
				}
			}
			return value, nil
		}
		localNode := func(membership clusterbootstrap.K3sMembership) (clusterbootstrap.K3sJoinedNode, error) {
			raw, err := get("/api/v1/nodes/" + request.Network.Hostname)
			defer clear(raw)
			if err != nil {
				return clusterbootstrap.K3sJoinedNode{}, errBootstrapSource
			}
			return observeBootstrapMembershipLocalNode(raw, request, membership, localName)
		}
		if version() != nil {
			return errBootstrapSource
		}
		before, err := readMembership()
		if err != nil {
			return errBootstrapSource
		}
		first, err := localNode(before)
		if err != nil {
			return errBootstrapSource
		}
		second, err := localNode(before)
		if err != nil || first != second {
			return errBootstrapSource
		}
		after, err := readMembership()
		if err != nil || !reflect.DeepEqual(before, after) || version() != nil || !prove() {
			return errBootstrapSource
		}
		result = bootstrapMembershipObservation{request.Source, second, after, requestSHA256}
		return nil
	})
	if err != nil || ctx.Err() != nil {
		return fail()
	}
	return result, nil
}

func bootstrapMembershipObject(raw []byte, names ...string) (map[string]json.RawMessage, bool) {
	object, err := clusterbootstrap.ParseK3sObservationObject(raw)
	if err != nil {
		return nil, false
	}
	for key := range object {
		for _, name := range names {
			if key != name && strings.EqualFold(key, name) {
				return nil, false
			}
		}
	}
	return object, true
}
func bootstrapMembershipText(object map[string]json.RawMessage, key string) string {
	var text string
	raw := object[key]
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &text) != nil {
		return ""
	}
	return text
}
func bootstrapMembershipMetadata(object map[string]json.RawMessage, kind string) (map[string]json.RawMessage, bool) {
	if bootstrapMembershipText(object, "apiVersion") != "v1" || bootstrapMembershipText(object, "kind") != kind {
		return nil, false
	}
	m, ok := bootstrapMembershipObject(object["metadata"], "name", "uid", "namespace", "deletionTimestamp", "resourceVersion", "annotations", "labels")
	revision := bootstrapMembershipText(m, "resourceVersion")
	if !ok || !nodePattern.MatchString(bootstrapMembershipText(m, "name")) || !clusterUUIDPattern.MatchString(bootstrapMembershipText(m, "uid")) || bootstrapMembershipText(m, "uid") == "00000000-0000-0000-0000-000000000000" || len(revision) < 1 || len(revision) > 128 {
		return nil, false
	}
	if scope, present := m["namespace"]; present && string(scope) != `""` {
		return nil, false
	}
	if deletion, present := m["deletionTimestamp"]; present && string(deletion) != "null" {
		return nil, false
	}
	for _, c := range revision {
		if c < 33 || c > 126 {
			return nil, false
		}
	}
	return m, true
}
func observeBootstrapMembershipLocalNode(raw []byte, r bootstrapMembershipObservationRequest, membership clusterbootstrap.K3sMembership, localName string) (clusterbootstrap.K3sJoinedNode, error) {
	fail := func() (clusterbootstrap.K3sJoinedNode, error) {
		return clusterbootstrap.K3sJoinedNode{}, errBootstrapSource
	}
	object, ok := bootstrapMembershipObject(raw, "apiVersion", "kind", "metadata", "status")
	if !ok {
		return fail()
	}
	m, ok := bootstrapMembershipMetadata(object, "Node")
	if !ok {
		return fail()
	}
	uid, name := bootstrapMembershipText(m, "uid"), bootstrapMembershipText(m, "name")
	var want clusterbootstrap.K3sJoinedNode
	for _, node := range r.Nodes {
		if node.Node.NodeUID == uid && uid == r.Source.SourceUID && node.MemberName == localName {
			want = node.Node
		}
	}
	if want.Hostname != name || want.NodeUID == "" {
		return fail()
	}
	annotations, ok := bootstrapMembershipObject(m["annotations"], "etcd.k3s.cattle.io/node-name", "etcd.k3s.cattle.io/removed-node-name")
	if !ok {
		return fail()
	}
	memberName := bootstrapMembershipText(annotations, "etcd.k3s.cattle.io/node-name")
	if !clusterbootstrap.ValidK3sMemberName(memberName, name) {
		return fail()
	}
	if value, exists := annotations["etcd.k3s.cattle.io/removed-node-name"]; exists && string(value) != `""` {
		return fail()
	}
	var member clusterbootstrap.K3sEtcdMember
	for _, value := range membership.Members {
		if value.Address == want.Address {
			member = value
		}
	}
	if member.ID == 0 || member.Name != memberName || uid == r.Source.SourceUID && memberName != localName {
		return fail()
	}
	labels, ok := bootstrapMembershipObject(m["labels"])
	if !ok || bootstrapMembershipText(labels, "borealis.io/engine-node") != "true" {
		return fail()
	}
	if bootstrapMembershipText(labels, "borealis.io/application-state") != "active" {
		return fail()
	}
	status, ok := bootstrapMembershipObject(object["status"], "nodeInfo", "addresses", "conditions")
	if !ok {
		return fail()
	}
	info, ok := bootstrapMembershipObject(status["nodeInfo"], "machineID", "bootID", "kubeletVersion", "architecture", "operatingSystem")
	if !ok || bootstrapMembershipText(info, "machineID") != want.MachineID || bootstrapMembershipText(info, "bootID") != want.BootID || bootstrapMembershipText(info, "kubeletVersion") != want.K3sVersion || bootstrapMembershipText(info, "architecture") != "amd64" || bootstrapMembershipText(info, "operatingSystem") != "linux" {
		return fail()
	}
	var addresses, conditions []json.RawMessage
	if json.Unmarshal(status["addresses"], &addresses) != nil || json.Unmarshal(status["conditions"], &conditions) != nil {
		return fail()
	}
	internal, ready := 0, 0
	conditionNames := map[string]bool{}
	for _, raw := range addresses {
		value, ok := bootstrapMembershipObject(raw, "type", "address")
		if !ok || bootstrapMembershipText(value, "type") == "" || bootstrapMembershipText(value, "address") == "" {
			return fail()
		}
		if bootstrapMembershipText(value, "type") == "InternalIP" {
			internal++
			if bootstrapMembershipText(value, "address") != want.Address {
				return fail()
			}
		}
	}
	for _, raw := range conditions {
		value, ok := bootstrapMembershipObject(raw, "type", "status")
		kind, state := bootstrapMembershipText(value, "type"), bootstrapMembershipText(value, "status")
		if !ok || kind == "" || conditionNames[kind] || state != "True" && state != "False" && state != "Unknown" {
			return fail()
		}
		conditionNames[kind] = true
		if kind == "Ready" {
			ready++
			if state != "True" {
				return fail()
			}
		}
	}
	if internal != 1 || ready != 1 {
		return fail()
	}
	return want, nil
}
