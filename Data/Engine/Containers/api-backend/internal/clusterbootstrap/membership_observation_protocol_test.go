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
