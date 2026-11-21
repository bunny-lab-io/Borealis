package clusterbootstrap

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
)

const PreparedAdmissionVerb = "PreparedAdmissionAction"
const PreparedAdmissionRequestLimit = 24 << 10

// PreparedAdmissionRequest binds one existing admission action to settled
// preparation. It is not permission: the original controller still owns the
// operation, independent quorum evidence, Job lifetime and admission ordering.
// A distinct verb makes older managers reject instead of ignoring new params.
type PreparedAdmissionRequest struct {
	Version     int                       `json:"version"`
	Action      string                    `json:"action"`
	StartBefore int64                     `json:"start_before,omitempty"`
	Expected    PreparationExpected       `json:"expected"`
	Receipt     PrejoinPreparationReceipt `json:"receipt"`
}

func (PreparedAdmissionRequest) String() string   { return "prepared admission request [private]" }
func (PreparedAdmissionRequest) GoString() string { return "prepared admission request [private]" }

func (r PreparedAdmissionRequest) Validate() error {
	if r.Version != 1 || r.Receipt.Validate(r.Expected, r.Receipt.Source) != nil {
		return ErrPreparationConfig
	}
	switch r.Action {
	case "RunK3sProbeConformance", "RedeployRevision", "InspectCandidateHealth", "PromoteCandidate", "InspectHealth", "EnterApplicationDrain", "ExitApplicationDrain", "ReconcilePreparedAdmissionStopped":
		return nil
	}
	return ErrPreparationConfig
}

func EncodePreparedAdmissionRequest(r PreparedAdmissionRequest) (string, error) {
	if r.Validate() != nil {
		return "", ErrPreparationConfig
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return "", ErrPreparationConfig
	}
	encoded := base64.RawStdEncoding.EncodeToString(raw)
	if len(encoded) > PreparedAdmissionRequestLimit {
		return "", ErrPreparationConfig
	}
	return encoded, nil
}

func DecodePreparedAdmissionRequest(encoded string) (PreparedAdmissionRequest, error) {
	var r PreparedAdmissionRequest
	if len(encoded) == 0 || len(encoded) > PreparedAdmissionRequestLimit {
		return r, ErrPreparationConfig
	}
	raw, err := base64.RawStdEncoding.Strict().DecodeString(encoded)
	if err != nil || base64.RawStdEncoding.EncodeToString(raw) != encoded || membershipCanonical(raw, PreparedAdmissionRequestLimit, &r) != nil || r.Validate() != nil {
		return PreparedAdmissionRequest{}, ErrPreparationConfig
	}
	return r, nil
}

func ParsePreparedAdmissionActionRequest(raw []byte) (PreparedAdmissionRequest, error) {
	object, err := sourceJSONObject(raw, 64<<10)
	if err != nil || len(object) != 2 || string(object["verb"]) != `"PreparedAdmissionAction"` {
		return PreparedAdmissionRequest{}, ErrPreparationConfig
	}
	params, err := sourceJSONObject(object["params"], PreparedAdmissionRequestLimit+64)
	var encoded string
	if err != nil || len(params) != 1 || json.Unmarshal(params["payload"], &encoded) != nil {
		return PreparedAdmissionRequest{}, ErrPreparationConfig
	}
	return DecodePreparedAdmissionRequest(encoded)
}

func (r PreparedAdmissionRequest) SHA256() string {
	if r.Validate() != nil {
		return ""
	}
	raw, err := json.Marshal(struct {
		Domain  string
		Request PreparedAdmissionRequest
	}{"prepared-admission-action-v1", r})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Acknowledgement is request-bound completion, never reusable readiness proof.
func ValidatePreparedAdmissionAcknowledgement(raw []byte, request PreparedAdmissionRequest) error {
	object, err := sourceJSONObject(raw, 4096)
	if request.Validate() != nil || err != nil || len(object) != 3 || string(object["ok"]) != "true" || string(object["verb"]) != `"PreparedAdmissionAction"` {
		return ErrPreparationConfig
	}
	result, err := sourceJSONObject(object["result"], 512)
	var digest string
	if err != nil || len(result) != 2 || string(result["version"]) != "1" || json.Unmarshal(result["request_sha256"], &digest) != nil || digest != request.SHA256() {
		return ErrPreparationConfig
	}
	return nil
}

// Admission intentionally changes application/role labels. All strict Node
// identity checks remain shared with preparation; only its drained-label policy
// is omitted here. Readiness and exact UID are still required for each action.
func ParsePreparedAdmissionNode(raw []byte, request PreparedAdmissionRequest) (K3sJoinedNode, error) {
	if request.Validate() != nil {
		return K3sJoinedNode{}, ErrPreparationConfig
	}
	e := request.Expected
	node, err := parseK3sJoinedNodeIdentity(raw, K3sJoinedNodeIdentity{e.Target.Hostname, e.Target.MachineID, e.TargetBootID, e.Target.Address, e.K3sVersion}, false)
	if err != nil || node != request.Receipt.Node {
		return K3sJoinedNode{}, ErrPreparationConfig
	}
	return node, nil
}
