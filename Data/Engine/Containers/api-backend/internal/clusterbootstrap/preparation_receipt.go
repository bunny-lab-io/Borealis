package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// PrejoinPreparationReceipt is private evidence, never an admission capability.
// Fixed-order results cover the complete native chain. The original controller
// must independently observe the same Node and retain its own current authority.
type PrejoinPreparationReceipt struct {
	Version            int                        `json:"version"`
	ExpectedSHA256     string                     `json:"expected_sha256"`
	Source             engineidentity.Binding     `json:"source"`
	Steps              [11]PreparationStepReceipt `json:"steps"`
	Node               K3sJoinedNode              `json:"node"`
	MemberID           uint64                     `json:"member_id"`
	MemberName         string                     `json:"member_name"`
	ImageTargetsSHA256 string                     `json:"image_targets_sha256"`
}

type PreparationStepReceipt struct {
	Step         string `json:"step"`
	InputSHA256  string `json:"input_sha256"`
	ResultSHA256 string `json:"result_sha256"`
}

func (PrejoinPreparationReceipt) String() string {
	return "prejoin preparation receipt [private evidence withheld]"
}
func (PrejoinPreparationReceipt) GoString() string {
	return "prejoin preparation receipt [private evidence withheld]"
}

func preparationReceiptDigest(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func preparationReceiptHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func startedPreparationReceipt(input string, node K3sJoinedNode, memberID uint64, memberName string) string {
	return preparationReceiptDigest(struct {
		Domain, Input string
		Node          K3sJoinedNode
		MemberID      uint64
		MemberName    string
	}{"prepared-k3s-started-v1", input, node, memberID, memberName})
}

// Validate binds an untrusted private frame to caller-owned expected inputs.
// Structural/hash consistency alone cannot establish actual host completion.
func (r PrejoinPreparationReceipt) Validate(expected PreparationExpected, source engineidentity.Binding) error {
	if expected.Validate() != nil || validatePrejoinSource(expected.Target, source) != nil || r.Version != 1 || r.Source != source || r.ExpectedSHA256 != preparationReceiptDigest(expected) ||
		!r.Node.Ready || !nonzeroPreparationUUID(r.Node.NodeUID) || r.Node.Hostname != expected.Target.Hostname || r.Node.MachineID != expected.Target.MachineID || r.Node.BootID != expected.TargetBootID || r.Node.Address != expected.Target.Address || r.Node.K3sVersion != expected.K3sVersion ||
		r.MemberID == 0 || !ValidK3sMemberName(r.MemberName, r.Node.Hostname) {
		return ErrSessionProtocol
	}
	names := [...]string{"stage_source", "prepare_runtime_identity", "prepare_identity_root", "install_identity", "prepare_host", "stage_images", "install_k3s", "configure_k3s", "start_k3s", "verify_images", "activate_identity"}
	for i, step := range r.Steps {
		if step.Step != names[i] {
			return ErrSessionProtocol
		}
		for _, digest := range []string{step.InputSHA256, step.ResultSHA256} {
			if !digestPattern.MatchString(digest) || digest == strings.Repeat("0", 64) {
				return ErrSessionProtocol
			}
		}
	}
	if !digestPattern.MatchString(r.ImageTargetsSHA256) || r.ImageTargetsSHA256 == strings.Repeat("0", 64) {
		return ErrSessionProtocol
	}
	start, images, activation := r.Steps[8], r.Steps[9], r.Steps[10]
	if start.InputSHA256 != preparationReceiptHash("prepared-k3s-start-v1\x00"+r.Steps[7].ResultSHA256) || start.ResultSHA256 != startedPreparationReceipt(start.InputSHA256, r.Node, r.MemberID, r.MemberName) ||
		images.InputSHA256 != preparationReceiptHash("prepared-image-verification-v1\x00"+start.InputSHA256+"\x00"+start.ResultSHA256+"\x00"+r.ImageTargetsSHA256) ||
		images.ResultSHA256 != preparationReceiptDigest(struct{ Domain, Input, Startup, Targets string }{"prepared-image-verification-receipt-v1", images.InputSHA256, start.ResultSHA256, r.ImageTargetsSHA256}) ||
		activation.InputSHA256 != preparationReceiptHash("prejoin-identity-activation-v1\x00"+images.InputSHA256+"\x00"+images.ResultSHA256) || activation.ResultSHA256 != activation.InputSHA256 {
		return ErrSessionProtocol
	}
	return nil
}

// Fixed canonical private envelope preserves the outer protocol's no-array
// grammar. Round-trip equality rejects unknown, duplicate, aliased and extra
// array entries instead of relying on encoding/json's fixed-array truncation.
func encodePreparationReceipt(r PrejoinPreparationReceipt) (string, error) {
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > 8<<10 {
		return "", ErrSessionProtocol
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}
func decodePreparationReceipt(encoded string, expected PreparationExpected, source engineidentity.Binding) (PrejoinPreparationReceipt, error) {
	fail := func() (PrejoinPreparationReceipt, error) { return PrejoinPreparationReceipt{}, ErrSessionProtocol }
	if len(encoded) == 0 || len(encoded) > 12<<10 {
		return fail()
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(raw) > 8<<10 || base64.StdEncoding.EncodeToString(raw) != encoded {
		return fail()
	}
	var result PrejoinPreparationReceipt
	if json.Unmarshal(raw, &result) != nil || result.Validate(expected, source) != nil {
		return fail()
	}
	canonical, err := json.Marshal(result)
	if err != nil || !bytes.Equal(raw, canonical) {
		return fail()
	}
	return result, nil
}
