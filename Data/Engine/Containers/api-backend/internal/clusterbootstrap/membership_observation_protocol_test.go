package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

func protocolMembershipReceiptFixture(e PreparationExpected, source engineidentity.Binding, uid string, id uint64) PrejoinPreparationReceipt {
	digest := func(value any) string {
		raw, _ := json.Marshal(value)
		sum := sha256.Sum256(raw)
		return hex.EncodeToString(sum[:])
	}
	hash := func(value string) string { sum := sha256.Sum256([]byte(value)); return hex.EncodeToString(sum[:]) }
	r := PrejoinPreparationReceipt{Version: 1, ExpectedSHA256: digest(e), Source: source,
		Node: K3sJoinedNode{NodeUID: uid, Hostname: e.Target.Hostname, MachineID: e.Target.MachineID, BootID: e.TargetBootID, Address: e.Target.Address, K3sVersion: e.K3sVersion, Ready: true}, MemberID: id, MemberName: e.Target.Hostname + "-12345678", ImageTargetsSHA256: strings.Repeat("e", 64)}
	for i, name := range []string{"stage_source", "prepare_runtime_identity", "prepare_identity_root", "install_identity", "prepare_host", "stage_images", "install_k3s", "configure_k3s", "start_k3s", "verify_images", "activate_identity"} {
		r.Steps[i] = PreparationStepReceipt{Step: name, InputSHA256: strings.Repeat("a", 64), ResultSHA256: strings.Repeat("a", 64)}
	}
	r.Steps[8].InputSHA256 = hash("prepared-k3s-start-v1\x00" + r.Steps[7].ResultSHA256)
	r.Steps[8].ResultSHA256 = digest(struct {
		Domain, Input string
		Node          K3sJoinedNode
		MemberID      uint64
		MemberName    string
	}{"prepared-k3s-started-v1", r.Steps[8].InputSHA256, r.Node, r.MemberID, r.MemberName})
	r.Steps[9].InputSHA256 = hash("prepared-image-verification-v1\x00" + r.Steps[8].InputSHA256 + "\x00" + r.Steps[8].ResultSHA256 + "\x00" + r.ImageTargetsSHA256)
	r.Steps[9].ResultSHA256 = digest(struct{ Domain, Input, Startup, Targets string }{"prepared-image-verification-receipt-v1", r.Steps[9].InputSHA256, r.Steps[8].ResultSHA256, r.ImageTargetsSHA256})
	r.Steps[10].InputSHA256 = hash("prejoin-identity-activation-v1\x00" + r.Steps[9].InputSHA256 + "\x00" + r.Steps[9].ResultSHA256)
	r.Steps[10].ResultSHA256 = r.Steps[10].InputSHA256
	return r
}

func membershipProtocolFixture(t *testing.T, longest bool) (MembershipObservationRequest, MembershipObservation) {
	t.Helper()
	e, _ := preparationFixture()
	source := engineidentity.Binding{ClusterID: e.Target.ClusterID, SourceID: "77777777-7777-4777-8777-777777777777", SourceUID: "88888888-8888-4888-8888-888888888888"}
	_, n := sourceNetworkFixture(t)
	n.NodeUID = source.SourceUID
	n.PodCIDR, n.ServiceCIDR = e.PodCIDR, e.ServiceCIDR
	n.ManagementLink.Address = "192.168.3.250/24"
	if longest {
		n.Hostname = strings.Repeat("s", 63)
		e.Target.Hostname = strings.Repeat("t", 63)
	}
	receipt := protocolMembershipReceiptFixture(e, source, "99999999-9999-4999-8999-999999999999", math.MaxUint64)
	local := K3sJoinedNode{n.NodeUID, n.Hostname, n.MachineID, n.BootID, "192.168.3.250", n.K3sVersion, true}
	other := K3sJoinedNode{"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "engine-03", strings.Repeat("c", 32), "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "192.168.3.252", n.K3sVersion, true}
	if longest {
		other.Hostname = strings.Repeat("r", 63)
	}
	r := MembershipObservationRequest{Source: source, Network: n, KubeSystemUID: e.KubeSystemUID, Nodes: []MembershipNodeExpectation{{local, n.Hostname + "-12345678"}, {receipt.Node, receipt.MemberName}, {other, other.Hostname + "-12345678"}}, Prepared: []MembershipPreparedTarget{{e, receipt}}}
	o := MembershipObservation{Source: source, LocalNode: local, RequestSHA256: r.SHA256(), Membership: K3sMembership{LocalID: math.MaxUint64 - 1, Voting: true, Members: []K3sEtcdMember{{math.MaxUint64 - 1, r.Nodes[0].MemberName, local.Address, false}, {math.MaxUint64, receipt.MemberName, receipt.Node.Address, false}, {9007199254740993, r.Nodes[2].MemberName, other.Address, false}}}}
	if !r.Valid() || !o.Valid(r) {
		t.Fatal("invalid fixture")
	}
	return r, o
}
func TestMembershipObservationProtocolPrecisionBoundsAndBinding(t *testing.T) {
	for _, longest := range []bool{false, true} {
		r, o := membershipProtocolFixture(t, longest)
		encoded, err := EncodeMembershipObservationRequest(r)
		decoded, err2 := DecodeMembershipObservationRequest(encoded)
		if err != nil || err2 != nil || !reflect.DeepEqual(r, decoded) {
			t.Fatal("request identity", err, err2)
		}
		action, _ := json.Marshal(map[string]any{"verb": "InspectSourceMembership", "params": map[string]any{"payload": encoded}})
		var generic map[string]any
		if json.Unmarshal(action, &generic) != nil {
			t.Fatal("generic request")
		}
		wire, _ := json.Marshal(generic)
		parsed, err := ParseMembershipActionRequest(wire)
		if err != nil || parsed.Prepared[0].Receipt.MemberID != math.MaxUint64 || len(wire) > 64<<10 {
			t.Fatal("generic request rounded ID", err)
		}
		result, err := EncodeMembershipObservationResult(o, r)
		if err != nil {
			t.Fatal(err)
		}
		response, _ := json.Marshal(map[string]any{"ok": true, "verb": "InspectSourceMembership", "result": map[string]any{"membership_observation": result}})
		generic = nil
		if json.Unmarshal(response, &generic) != nil {
			t.Fatal("generic result")
		}
		response, _ = json.Marshal(generic)
		nonce, job, pod := r.Source.ClusterID, r.Source.SourceID, r.Source.SourceUID
		receipt, err := NewMembershipObservationReceipt(response, r, nonce, job, pod)
		got, err2 := ParseMembershipObservationReceipt(receipt, r, nonce, job, pod)
		if err != nil || err2 != nil || !reflect.DeepEqual(o, got) || len(receipt) > SourceNetworkReceiptLimit {
			t.Fatal("receipt identity/size", err, err2, len(receipt))
		}
		t.Logf("longest=%v request=%d response=%d receipt=%d", longest, len(wire), len(response), len(receipt))
		for _, ids := range [][3]string{{pod, job, pod}, {nonce, pod, pod}, {nonce, job, job}} {
			if _, err := ParseMembershipObservationReceipt(receipt, r, ids[0], ids[1], ids[2]); err == nil {
				t.Fatal("receipt replay accepted")
			}
		}
		changed := r
		changed.KubeSystemUID = job
		if _, err := ParseMembershipObservationReceipt(receipt, changed, nonce, job, pod); err == nil {
			t.Fatal("changed expectation accepted")
		}
	}
}
func TestMembershipObservationProtocolRejectsMalformed(t *testing.T) {
	r, o := membershipProtocolFixture(t, false)
	encoded, _ := EncodeMembershipObservationRequest(r)
	raw, _ := base64.RawStdEncoding.DecodeString(encoded)
	for _, bad := range [][]byte{append(bytes.Clone(raw), ' '), bytes.Replace(raw, []byte(`"Source":`), []byte(`"source":`), 1), bytes.Replace(raw, []byte(`"MemberName":`), []byte(`"MemberName":"bad","MemberName":`), 1), bytes.Replace(raw, []byte("18446744073709551615"), []byte("18446744073709551616"), 1), bytes.Replace(raw, []byte("18446744073709551615"), []byte("1.5"), 1), append(bytes.Clone(raw), []byte(`{}`)...)} {
		if bytes.Equal(raw, bad) {
			t.Fatal("mutation missing")
		}
		if _, err := DecodeMembershipObservationRequest(base64.RawStdEncoding.EncodeToString(bad)); err == nil {
			t.Fatal("malformed accepted")
		}
	}
	for _, bad := range []string{encoded + "=", strings.Repeat("A", MembershipObservationRequestLimit+1), ""} {
		if _, err := DecodeMembershipObservationRequest(bad); err == nil {
			t.Fatal("encoded bounds")
		}
	}
	result, _ := EncodeMembershipObservationResult(o, r)
	response, _ := json.Marshal(map[string]any{"ok": true, "verb": "InspectSourceMembership", "result": map[string]any{"membership_observation": result}})
	nonce, job, pod := r.Source.ClusterID, r.Source.SourceID, r.Source.SourceUID
	receipt, _ := NewMembershipObservationReceipt(response, r, nonce, job, pod)
	for _, bad := range [][]byte{append(bytes.Clone(receipt), ' '), bytes.Replace(receipt, []byte(`"version":1`), []byte(`"version":1,"extra":0`), 1), bytes.Replace(receipt, []byte(`"learner":false`), []byte(`"learner":true`), 1), bytes.Replace(receipt, []byte(`"LocalNode":`), []byte(`"localnode":`), 1), bytes.Replace(receipt, []byte("18446744073709551615"), []byte("18446744073709551614"), 1)} {
		if bytes.Equal(receipt, bad) {
			t.Fatal("mutation missing")
		}
		if _, err := ParseMembershipObservationReceipt(bad, r, nonce, job, pod); err == nil {
			t.Fatal("malformed receipt accepted")
		}
	}
}

func membershipObserverProtocolFixture(t *testing.T, pair bool, index int) (MembershipObservationRequest, MembershipObservation) {
	t.Helper()
	r, o := membershipProtocolFixture(t, true)
	if pair {
		e := r.Prepared[0].Expected
		node := r.Nodes[2].Node
		e.Target.TargetID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
		e.Target.Hostname, e.Target.Address, e.Target.MachineID, e.TargetBootID = node.Hostname, node.Address, node.MachineID, node.BootID
		receipt := protocolMembershipReceiptFixture(e, r.Source, node.NodeUID, o.Membership.Members[2].ID)
		r.Prepared = append(r.Prepared, MembershipPreparedTarget{e, receipt})
	}
	node := r.Nodes[index].Node
	r.Observer = &MembershipObserver{NodeUID: node.NodeUID}
	r.OriginalSourceNodeUIDs = []string{r.Source.SourceUID}
	if !pair {
		r.OriginalSourceNodeUIDs = append(r.OriginalSourceNodeUIDs, r.Nodes[2].Node.NodeUID)
	}
	r.Network.NodeUID, r.Network.Hostname, r.Network.MachineID, r.Network.BootID = node.NodeUID, node.Hostname, node.MachineID, node.BootID
	r.Network.ManagementLink.Address = node.Address + "/24"
	observer := *r.Observer
	o.Observer, o.LocalNode = &observer, node
	for _, member := range o.Membership.Members {
		if member.Address == node.Address {
			o.Membership.LocalID = member.ID
		}
	}
	o.RequestSHA256 = r.SHA256()
	if !r.Valid() || !o.Valid(r) {
		t.Fatal("invalid explicit observer fixture")
	}
	return r, o
}

func TestMembershipObservationObserverPreservesLegacyAndProvenance(t *testing.T) {
	legacy, oldResult := membershipProtocolFixture(t, false)
	type legacyRequest struct {
		Source        engineidentity.Binding
		Network       SourceNetwork
		KubeSystemUID string
		Nodes         []MembershipNodeExpectation
		Prepared      []MembershipPreparedTarget
	}
	type legacyResult struct {
		Source        engineidentity.Binding
		LocalNode     K3sJoinedNode
		Membership    K3sMembership
		RequestSHA256 string
	}
	old := legacyRequest{legacy.Source, legacy.Network, legacy.KubeSystemUID, legacy.Nodes, legacy.Prepared}
	want, _ := json.Marshal(old)
	got, _ := json.Marshal(legacy)
	if !bytes.Equal(want, got) {
		t.Fatal("legacy request bytes changed")
	}
	want, _ = json.Marshal(legacyResult{oldResult.Source, oldResult.LocalNode, oldResult.Membership, oldResult.RequestSHA256})
	got, _ = json.Marshal(oldResult)
	if !bytes.Equal(want, got) {
		t.Fatal("legacy result bytes changed")
	}
	raw, _ := json.Marshal(struct {
		Domain  string
		Request legacyRequest
	}{"original-source-membership-observation-v1", old})
	sum := sha256.Sum256(raw)
	if legacy.SHA256() != hex.EncodeToString(sum[:]) {
		t.Fatal("legacy digest changed")
	}
	for _, pair := range []bool{false, true} {
		seen := map[string]bool{}
		for index := range 3 {
			r, o := membershipObserverProtocolFixture(t, pair, index)
			if seen[o.LocalNode.NodeUID] || o.Source != legacy.Source {
				t.Fatal("observer reused UID or replaced original source")
			}
			seen[o.LocalNode.NodeUID] = true
			for _, p := range r.Prepared {
				if p.Receipt.Source != legacy.Source || p.Receipt.Validate(p.Expected, legacy.Source) != nil {
					t.Fatal("observer replaced historical receipt provenance")
				}
			}
			encoded, err := EncodeMembershipObservationRequest(r)
			decoded, decodeErr := DecodeMembershipObservationRequest(encoded)
			if err != nil || decodeErr != nil || !reflect.DeepEqual(r, decoded) {
				t.Fatal("observer request round trip", err, decodeErr)
			}
			raw, _ := base64.RawStdEncoding.DecodeString(encoded)
			if membershipCanonical(raw, MembershipObservationRequestLimit, &legacyRequest{}) == nil {
				t.Fatal("old decoder accepted explicit observer")
			}
			result, err := EncodeMembershipObservationResult(o, r)
			if err != nil {
				t.Fatal(err)
			}
			response, _ := json.Marshal(map[string]any{"ok": true, "verb": "InspectSourceMembership", "result": map[string]any{"membership_observation": result}})
			nonce, job, pod := r.Source.ClusterID, r.Source.SourceID, r.Source.SourceUID
			receipt, err := NewMembershipObservationReceipt(response, r, nonce, job, pod)
			parsed, parseErr := ParseMembershipObservationReceipt(receipt, r, nonce, job, pod)
			if err != nil || parseErr != nil || !reflect.DeepEqual(parsed, o) || len(receipt) > SourceNetworkReceiptLimit {
				t.Fatal("observer receipt", err, parseErr, len(receipt))
			}
			for _, ids := range [][3]string{{pod, job, pod}, {nonce, pod, pod}, {nonce, job, job}} {
				if _, err := ParseMembershipObservationReceipt(receipt, r, ids[0], ids[1], ids[2]); err == nil {
					t.Fatal("observer receipt replay accepted")
				}
			}
			other, _ := membershipObserverProtocolFixture(t, pair, (index+1)%3)
			if _, err := ParseMembershipObservationReceipt(receipt, other, nonce, job, pod); err == nil || o.Valid(legacy) || oldResult.Valid(r) {
				t.Fatal("observer or legacy result substituted")
			}
			t.Logf("pair=%v observer=%d request=%d receipt=%d", pair, index, len(encoded), len(receipt))
		}
	}
}

func TestMembershipObservationObserverRejectsForeignEvidence(t *testing.T) {
	for _, mode := range []string{"unknown observer", "zero observer", "wrong network", "missing original", "duplicate UID", "short census", "missing receipt", "dropped target receipt", "missing original roster", "unknown original UID", "duplicate original UID", "zero original UID", "overlapping partition", "omitted survivor", "original outside source roster", "legacy with roster", "original source", "receipt source", "target UID", "target member ID", "learner", "duplicate member", "wrong local UID", "missing result observer", "different result observer"} {
		t.Run(mode, func(t *testing.T) {
			r, o := membershipObserverProtocolFixture(t, true, 1)
			if mode == "duplicate original UID" || mode == "overlapping partition" || mode == "omitted survivor" || mode == "original outside source roster" {
				r, o = membershipObserverProtocolFixture(t, false, 1)
			}
			switch mode {
			case "unknown observer":
				r.Observer.NodeUID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
			case "zero observer":
				r.Observer.NodeUID = "00000000-0000-0000-0000-000000000000"
			case "wrong network":
				r.Network.NodeUID = r.Source.SourceUID
			case "missing original":
				r.Nodes[0].Node.NodeUID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
			case "duplicate UID":
				r.Nodes[2].Node.NodeUID = r.Nodes[1].Node.NodeUID
			case "short census":
				r.Nodes = r.Nodes[:2]
			case "missing receipt":
				r.Prepared = nil
			case "dropped target receipt":
				r.Prepared = r.Prepared[:1]
			case "missing original roster":
				r.OriginalSourceNodeUIDs = nil
			case "unknown original UID":
				r.OriginalSourceNodeUIDs[0] = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
			case "duplicate original UID":
				r.OriginalSourceNodeUIDs[1] = r.OriginalSourceNodeUIDs[0]
			case "zero original UID":
				r.OriginalSourceNodeUIDs[0] = "00000000-0000-0000-0000-000000000000"
			case "overlapping partition":
				r.OriginalSourceNodeUIDs[1] = r.Prepared[0].Receipt.Node.NodeUID
			case "omitted survivor":
				r.OriginalSourceNodeUIDs = r.OriginalSourceNodeUIDs[:1]
			case "original outside source roster":
				r.OriginalSourceNodeUIDs[0] = r.Prepared[0].Receipt.Node.NodeUID
			case "legacy with roster":
				r, o = membershipProtocolFixture(t, false)
				r.OriginalSourceNodeUIDs = []string{r.Source.SourceUID}
			case "original source":
				r.Source.SourceUID = r.Observer.NodeUID
			case "receipt source":
				r.Prepared[0].Receipt.Source.SourceUID = r.Observer.NodeUID
			case "target UID":
				r.Prepared[0].Receipt.Node.NodeUID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
			case "target member ID":
				o.Membership.Members[1].ID--
			case "learner":
				o.Membership.Members[1].Learner = true
			case "duplicate member":
				o.Membership.Members[2] = o.Membership.Members[1]
			case "wrong local UID":
				o.LocalNode.NodeUID = r.Source.SourceUID
			case "missing result observer":
				o.Observer = nil
			case "different result observer":
				o.Observer.NodeUID = r.Source.SourceUID
			}
			o.RequestSHA256 = r.SHA256()
			if o.Valid(r) {
				t.Fatal("foreign observer evidence accepted")
			}
		})
	}
}

func TestMembershipObservationObserverRejectsAliases(t *testing.T) {
	r, o := membershipObserverProtocolFixture(t, true, 1)
	raw, _ := json.Marshal(r)
	for _, bad := range [][]byte{bytes.Replace(raw, []byte(`"Observer":`), []byte(`"observer":`), 1), bytes.Replace(raw, []byte(`"Observer":{`), []byte(`"Observer":null,"Observer":{`), 1), bytes.Replace(raw, []byte(`"Observer":{"NodeUID":`), []byte(`"Observer":{"nodeuid":`), 1), bytes.Replace(raw, []byte(`"Observer":{"NodeUID":`), []byte(`"Observer":{"extra":true,"NodeUID":`), 1), bytes.Replace(raw, []byte(`"OriginalSourceNodeUIDs":`), []byte(`"originalSourceNodeUIDs":`), 1), bytes.Replace(raw, []byte(`"OriginalSourceNodeUIDs":`), []byte(`"OriginalSourceNodeUIDs":[],"OriginalSourceNodeUIDs":`), 1)} {
		if bytes.Equal(raw, bad) {
			t.Fatal("missing observer mutation")
		}
		if _, err := DecodeMembershipObservationRequest(base64.RawStdEncoding.EncodeToString(bad)); err == nil {
			t.Fatal("malformed observer accepted")
		}
	}
	raw, _ = json.Marshal(o)
	bad := bytes.Replace(raw, []byte(`"Observer":`), []byte(`"observer":`), 1)
	if _, err := decodeMembershipObservationResult(base64.RawStdEncoding.EncodeToString(bad), r); err == nil {
		t.Fatal("aliased result observer accepted")
	}
}
