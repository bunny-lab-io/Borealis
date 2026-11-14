package clusterremote

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

func sshPrejoinReceiptFixture(expected clusterbootstrap.PreparationExpected, source engineidentity.Binding) clusterbootstrap.PrejoinPreparationReceipt {
	r := clusterbootstrap.PrejoinPreparationReceipt{Version: 1, ExpectedSHA256: preparationReceiptDigest(expected), Source: source,
		Node: clusterbootstrap.K3sJoinedNode{NodeUID: "99999999-9999-4999-8999-999999999999", Hostname: expected.Target.Hostname, MachineID: expected.Target.MachineID, BootID: expected.TargetBootID, Address: expected.Target.Address, K3sVersion: expected.K3sVersion, Ready: true}, MemberID: 42, MemberName: expected.Target.Hostname + "-12345678", ImageTargetsSHA256: strings.Repeat("e", 64)}
	for i, name := range []string{"stage_source", "prepare_runtime_identity", "prepare_identity_root", "install_identity", "prepare_host", "stage_images", "install_k3s", "configure_k3s", "start_k3s", "verify_images", "activate_identity"} {
		r.Steps[i] = clusterbootstrap.PreparationStepReceipt{Step: name, InputSHA256: strings.Repeat("a", 64), ResultSHA256: strings.Repeat("a", 64)}
	}
	r.Steps[8].InputSHA256 = preparationReceiptHash("prepared-k3s-start-v1\x00" + r.Steps[7].ResultSHA256)
	r.Steps[8].ResultSHA256 = startedPreparationReceipt(r.Steps[8].InputSHA256, r.Node, r.MemberID, r.MemberName)
	r.Steps[9].InputSHA256 = preparationReceiptHash("prepared-image-verification-v1\x00" + r.Steps[8].InputSHA256 + "\x00" + r.Steps[8].ResultSHA256 + "\x00" + r.ImageTargetsSHA256)
	r.Steps[9].ResultSHA256 = preparationReceiptDigest(struct{ Domain, Input, Startup, Targets string }{"prepared-image-verification-receipt-v1", r.Steps[9].InputSHA256, r.Steps[8].ResultSHA256, r.ImageTargetsSHA256})
	r.Steps[10].InputSHA256 = preparationReceiptHash("prejoin-identity-activation-v1\x00" + r.Steps[9].InputSHA256 + "\x00" + r.Steps[9].ResultSHA256)
	r.Steps[10].ResultSHA256 = r.Steps[10].InputSHA256
	return r
}

func preparationReceiptDigest(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func preparationReceiptHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
func startedPreparationReceipt(input string, node clusterbootstrap.K3sJoinedNode, id uint64, name string) string {
	return preparationReceiptDigest(struct {
		Domain, Input string
		Node          clusterbootstrap.K3sJoinedNode
		MemberID      uint64
		MemberName    string
	}{"prepared-k3s-started-v1", input, node, id, name})
}
