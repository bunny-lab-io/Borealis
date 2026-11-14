package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/netip"
	"slices"
	"strings"
)

// Encoded input fits the existing 64KiB action envelope; receipt remains below
// the existing 2048-byte source Job termination-message policy.
const MembershipObservationRequestLimit = 48 << 10

const MembershipObservationResponseLimit = 4096

// Capability of the native observer, not a caller-supplied listener identity or
// proof of quorum/admission. Only the opt-in contract may carry this value.
const MembershipListenerProofVersion uint8 = 1

// Distinct native capability: both fixed loopback listeners belong to the
// measured K3s process. Pinned routing supplies local embedded read attribution,
// never a response Header.MemberID, continuous quorum or admission authority.
const MembershipLocalEtcdProofVersion uint8 = 2

// Explicit fixed local etcd Range completion, with response cluster/member IDs
// bound to the independently retained observer and complete membership view.
// This is an observation at bounded instants, never continuous health or an
// admission capability outside original controller custody.
const MembershipQuorumProofVersion uint8 = 3

type MembershipQuorumRead struct {
	ClusterID uint64
	MemberID  uint64
}

func membershipCanonical(raw []byte, maximum int, out any) error {
	if _, err := sourceJSONObject(raw, maximum); err != nil || json.Unmarshal(raw, out) != nil {
		return ErrPreparationConfig
	}
	canonical, err := json.Marshal(out)
	if err != nil || !bytes.Equal(raw, canonical) {
		return ErrPreparationConfig
	}
	return nil
}

func EncodeMembershipObservationRequest(request MembershipObservationRequest) (string, error) {
	if !request.Valid() {
		return "", ErrPreparationConfig
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return "", ErrPreparationConfig
	}
	encoded := base64.RawStdEncoding.EncodeToString(raw)
	if len(encoded) > MembershipObservationRequestLimit {
		return "", ErrPreparationConfig
	}
	return encoded, nil
}

func DecodeMembershipObservationRequest(encoded string) (MembershipObservationRequest, error) {
	var request MembershipObservationRequest
	if len(encoded) == 0 || len(encoded) > MembershipObservationRequestLimit {
		return request, ErrPreparationConfig
	}
	raw, err := base64.RawStdEncoding.Strict().DecodeString(encoded)
	if err != nil || base64.RawStdEncoding.EncodeToString(raw) != encoded || membershipCanonical(raw, MembershipObservationRequestLimit, &request) != nil || !request.Valid() {
		return MembershipObservationRequest{}, ErrPreparationConfig
	}
	return request, nil
}

func (r MembershipObservationRequest) SHA256() string {
	if !r.Valid() {
		return ""
	}
	domain := "original-source-membership-observation-v1"
	if r.Observer != nil {
		domain = "settled-cohort-membership-observation-v1"
	}
	if r.RequireListenerProof {
		domain = "original-source-membership-listener-observation-v1"
		if r.Observer != nil {
			domain = "settled-cohort-membership-listener-observation-v1"
		}
	}
	if r.RequiredListenerProofVersion == MembershipLocalEtcdProofVersion {
		domain = "original-source-membership-local-etcd-observation-v1"
		if r.Observer != nil {
			domain = "settled-cohort-membership-local-etcd-observation-v1"
		}
	}
	if r.RequiredListenerProofVersion == MembershipQuorumProofVersion {
		domain = "original-source-membership-quorum-observation-v1"
		if r.Observer != nil {
			domain = "settled-cohort-membership-quorum-observation-v1"
		}
	}
	raw, err := json.Marshal(struct {
		Domain  string
		Request MembershipObservationRequest
	}{domain, r})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func ParseMembershipActionRequest(raw []byte) (MembershipObservationRequest, error) {
	object, err := sourceJSONObject(raw, 64<<10)
	if err != nil || len(object) != 2 || string(object["verb"]) != `"InspectSourceMembership"` {
		return MembershipObservationRequest{}, ErrPreparationConfig
	}
	params, err := sourceJSONObject(object["params"], MembershipObservationRequestLimit+64)
	var encoded string
	if err != nil || len(params) != 1 || json.Unmarshal(params["payload"], &encoded) != nil {
		return MembershipObservationRequest{}, ErrPreparationConfig
	}
	return DecodeMembershipObservationRequest(encoded)
}

func (o MembershipObservation) Valid(request MembershipObservationRequest) bool {
	if !request.Valid() || o.Source != request.Source || o.RequestSHA256 != request.SHA256() || len(o.Membership.Members) != 3 || !o.Membership.Voting {
		return false
	}
	wantProof := uint8(0)
	if request.RequireListenerProof {
		wantProof = MembershipListenerProofVersion
	}
	if request.RequiredListenerProofVersion == MembershipLocalEtcdProofVersion {
		wantProof = MembershipLocalEtcdProofVersion
	}
	if request.RequiredListenerProofVersion == MembershipQuorumProofVersion {
		wantProof = MembershipQuorumProofVersion
		if o.Quorum == nil || o.Quorum.ClusterID == 0 || o.Quorum.MemberID == 0 || o.Quorum.MemberID != o.Membership.LocalID {
			return false
		}
	} else if o.Quorum != nil {
		return false
	}
	if o.ListenerProofVersion != wantProof {
		return false
	}
	if (o.Observer == nil) != (request.Observer == nil) || o.Observer != nil && *o.Observer != *request.Observer {
		return false
	}
	ids, names, addresses := map[uint64]bool{}, map[string]bool{}, map[string]bool{}
	local := false
	for i, member := range o.Membership.Members {
		if member.ID == 0 || member.Learner || ids[member.ID] || names[member.Name] || addresses[member.Address] || i > 0 && o.Membership.Members[i-1].Address >= member.Address {
			return false
		}
		ids[member.ID], names[member.Name], addresses[member.Address] = true, true, true
		matched := false
		for _, expected := range request.Nodes {
			if member.Address == expected.Node.Address && member.Name == expected.MemberName {
				matched = true
				if expected.Node.NodeUID == request.ObserverUID() {
					local = o.LocalNode == expected.Node && o.Membership.LocalID == member.ID
				}
			}
		}
		if !matched {
			return false
		}
		for _, target := range request.Prepared {
			if member.Address == target.Receipt.Node.Address && (member.ID != target.Receipt.MemberID || member.Name != target.Receipt.MemberName) {
				return false
			}
		}
	}
	return local
}

// Strings carry numeric IDs through generic action maps without float64 coercion.
func EncodeMembershipObservationResult(result MembershipObservation, request MembershipObservationRequest) (string, error) {
	if !result.Valid(request) {
		return "", ErrPreparationConfig
	}
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > SourceNetworkReceiptLimit {
		return "", ErrPreparationConfig
	}
	return base64.RawStdEncoding.EncodeToString(raw), nil
}

func decodeMembershipObservationResult(encoded string, request MembershipObservationRequest) (MembershipObservation, error) {
	var result MembershipObservation
	if len(encoded) == 0 || len(encoded) > MembershipObservationResponseLimit {
		return result, ErrPreparationConfig
	}
	raw, err := base64.RawStdEncoding.Strict().DecodeString(encoded)
	if err != nil || base64.RawStdEncoding.EncodeToString(raw) != encoded || membershipCanonical(raw, SourceNetworkReceiptLimit, &result) != nil || !result.Valid(request) {
		return MembershipObservation{}, ErrPreparationConfig
	}
	return result, nil
}

type membershipObservationReceipt struct {
	Version     int                   `json:"version"`
	Nonce       string                `json:"nonce"`
	JobUID      string                `json:"job_uid"`
	PodUID      string                `json:"pod_uid"`
	Observation MembershipObservation `json:"observation"`
}

func NewMembershipObservationReceipt(raw []byte, request MembershipObservationRequest, nonce, jobUID, podUID string) ([]byte, error) {
	if !ValidSourceReceiptIdentity(nonce, jobUID, podUID) {
		return nil, ErrPreparationConfig
	}
	object, err := sourceJSONObject(raw, MembershipObservationResponseLimit)
	if err != nil || len(object) != 3 || string(object["ok"]) != "true" || string(object["verb"]) != `"InspectSourceMembership"` {
		return nil, ErrPreparationConfig
	}
	result, err := sourceJSONObject(object["result"], MembershipObservationResponseLimit)
	var encoded string
	if err != nil || len(result) != 1 || json.Unmarshal(result["membership_observation"], &encoded) != nil {
		return nil, ErrPreparationConfig
	}
	value, err := decodeMembershipObservationResult(encoded, request)
	if err != nil {
		return nil, ErrPreparationConfig
	}
	receipt, err := json.Marshal(membershipObservationReceipt{request.receiptVersion(), nonce, jobUID, podUID, value})
	if err != nil || len(receipt) > SourceNetworkReceiptLimit {
		return nil, ErrPreparationConfig
	}
	return receipt, nil
}

func ParseMembershipObservationReceipt(raw []byte, request MembershipObservationRequest, nonce, jobUID, podUID string) (MembershipObservation, error) {
	var receipt membershipObservationReceipt
	if !ValidSourceReceiptIdentity(nonce, jobUID, podUID) || membershipCanonical(raw, SourceNetworkReceiptLimit, &receipt) != nil || receipt.Version != request.receiptVersion() || receipt.Nonce != nonce || receipt.JobUID != jobUID || receipt.PodUID != podUID || !receipt.Observation.Valid(request) {
		return MembershipObservation{}, ErrPreparationConfig
	}
	return receipt.Observation, nil
}

// Historical completion inputs remain evidence, never live worker claims.
// Caller must derive these from the original controller's prepared-v2 snapshot.
type MembershipPreparedTarget struct {
	Expected PreparationExpected
	Receipt  PrejoinPreparationReceipt
}

// These identities and member annotations are controller-provided expectations.
// Only the selected local observer's Node is independently read by this adapter.
type MembershipNodeExpectation struct {
	Node       K3sJoinedNode
	MemberName string
}

// Observer selects one existing UID in the complete controller-owned cohort.
// It is independent of Source, which remains immutable receipt provenance.
// Full observer host/network identity comes from that exact Node and Network.
type MembershipObserver struct {
	NodeUID string
}

type MembershipObservationRequest struct {
	Source        engineidentity.Binding
	Network       SourceNetwork
	KubeSystemUID string
	Nodes         []MembershipNodeExpectation
	Prepared      []MembershipPreparedTarget
	Observer      *MembershipObserver `json:",omitempty"`
	// Explicit observers require the retained original-source cohort. Its
	// disjoint union with Prepared must be exactly Nodes. Only the original
	// controller can authenticate this historical partition from its SQL proof.
	OriginalSourceNodeUIDs []string `json:",omitempty"`
	// Explicit opt-in preserves legacy wire bytes and digest domains. A new
	// request cannot promote a legacy producer's membership-only response.
	RequireListenerProof bool `json:",omitempty"`
	// Zero preserves both historical contracts exactly. Version2 explicitly
	// requests measured6443+2379 custody; an installed marker1 producer cannot
	// satisfy this request even when it echoes the current request digest.
	// Version3 additionally requests fixed gateway custody and direct Range proof.
	RequiredListenerProofVersion uint8 `json:",omitempty"`
	// Only actual admission/verification phases may opt into intentional label
	// transitions. Exact settled target identity and quorum capability remain.
	PreparedAdmission bool `json:"prepared_admission,omitempty"`
}

// One selected endpoint's current complete view. Optional Quorum binds direct
// linearizable completion at its observation instants. Neither form supplies
// continuous availability, partition tolerance or admission authority.
type MembershipObservation struct {
	Source               engineidentity.Binding
	LocalNode            K3sJoinedNode
	Membership           K3sMembership
	RequestSHA256        string
	Observer             *MembershipObserver   `json:",omitempty"`
	ListenerProofVersion uint8                 `json:",omitempty"`
	Quorum               *MembershipQuorumRead `json:",omitempty"`
}

func (r MembershipObservationRequest) receiptVersion() int {
	if r.RequiredListenerProofVersion == MembershipQuorumProofVersion {
		return 4
	}
	if r.RequiredListenerProofVersion == MembershipLocalEtcdProofVersion {
		return 3
	}
	if r.RequireListenerProof {
		return 2
	}
	return 1
}

func (r MembershipObservationRequest) ObserverUID() string {
	if r.Observer != nil {
		return r.Observer.NodeUID
	}
	return r.Source.SourceUID
}

func (MembershipObservationRequest) String() string {
	return "membership observation request [internal]"
}
func (MembershipObservationRequest) GoString() string {
	return "membership observation request [internal]"
}

func (r MembershipObservationRequest) Valid() bool {
	if r.PreparedAdmission && (r.Observer == nil || !r.RequireListenerProof || r.RequiredListenerProofVersion != MembershipQuorumProofVersion) {
		return false
	}
	if r.RequiredListenerProofVersion != 0 && ((r.RequiredListenerProofVersion != MembershipLocalEtcdProofVersion && r.RequiredListenerProofVersion != MembershipQuorumProofVersion) || !r.RequireListenerProof) {
		return false
	}
	uuid := func(value string) bool {
		return sessionUUID.MatchString(value) && value != "00000000-0000-0000-0000-000000000000"
	}
	if !uuid(r.Source.ClusterID) || !uuid(r.Source.SourceID) || !uuid(r.Source.SourceUID) || !uuid(r.KubeSystemUID) ||
		r.Network.Validate() != nil || !uuid(r.ObserverUID()) || r.ObserverUID() != r.Network.NodeUID || r.Network.K3sVersion != K3sPins().Version || len(r.Nodes) != 3 || len(r.Prepared) < 1 || len(r.Prepared) > 2 {
		return false
	}
	names, uids, addresses, machines, boots := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	peers := []string{}
	local := false
	for _, expected := range r.Nodes {
		node := expected.Node
		if !ValidK3sMemberName(expected.MemberName, node.Hostname) {
			return false
		}
		ip, err := netip.ParseAddr(node.Address)
		if err != nil || !ip.Is4() || !ip.IsPrivate() || ip.String() != node.Address || !sessionHostname.MatchString(node.Hostname) || !uuid(node.NodeUID) || !uuid(node.BootID) || len(node.MachineID) != 32 || node.MachineID == strings.Repeat("0", 32) || !node.Ready || node.K3sVersion != r.Network.K3sVersion || names[node.Hostname] || uids[node.NodeUID] || addresses[node.Address] || machines[node.MachineID] || boots[node.BootID] {
			return false
		}
		for _, c := range node.MachineID {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return false
			}
		}
		names[node.Hostname], uids[node.NodeUID], addresses[node.Address], machines[node.MachineID], boots[node.BootID] = true, true, true, true, true
		peers = append(peers, node.Address)
		if node.NodeUID == r.ObserverUID() {
			local = node.Hostname == r.Network.Hostname && node.MachineID == r.Network.MachineID && node.BootID == r.Network.BootID && r.Network.ManagementLink.MatchesAddress(node.Address)
		}
	}
	if !local || !uids[r.Source.SourceUID] {
		return false
	}
	originalSources := map[string]bool{}
	if r.Observer == nil {
		if r.OriginalSourceNodeUIDs != nil {
			return false
		}
	} else {
		if len(r.OriginalSourceNodeUIDs) < 1 || len(r.OriginalSourceNodeUIDs) > 2 || len(r.OriginalSourceNodeUIDs)+len(r.Prepared) != len(r.Nodes) {
			return false
		}
		for _, uid := range r.OriginalSourceNodeUIDs {
			if !uuid(uid) || !uids[uid] || originalSources[uid] {
				return false
			}
			originalSources[uid] = true
		}
		if !originalSources[r.Source.SourceUID] {
			return false
		}
	}
	slices.Sort(peers)
	prepared, targets, ids := map[string]bool{}, map[string]bool{}, map[uint64]bool{}
	for i, p := range r.Prepared {
		e := p.Expected
		if e.Validate() != nil || p.Receipt.Validate(e, r.Source) != nil || e.Target.ClusterID != r.Source.ClusterID || e.KubeSystemUID != r.KubeSystemUID || e.K3sVersion != r.Network.K3sVersion || e.PodCIDR != r.Network.PodCIDR || e.ServiceCIDR != r.Network.ServiceCIDR || !slices.Equal(e.PeerAddresses, peers) || p.Receipt.Node.NodeUID == r.Source.SourceUID || originalSources[p.Receipt.Node.NodeUID] || prepared[p.Receipt.Node.NodeUID] || targets[e.Target.TargetID] || ids[p.Receipt.MemberID] {
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
