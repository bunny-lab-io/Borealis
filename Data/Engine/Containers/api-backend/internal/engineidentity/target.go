package engineidentity

import (
	"encoding/base64"
	"strings"
)

// TargetBinding identifies the approved SSH host before Kubernetes creates a
// Node UID. ProvisioningID is a separately generated durable operation-target
// UUID, never a fabricated Kubernetes UID. NodeUID is required by callers doing
// joined-node recovery and is empty for fresh pre-staging.
//
// The SSH transport must independently verify the approved full wire key. This
// public fingerprint binds the private transaction to that same target; it does
// not authorize a host or replace operation ownership and consumer fencing.
type TargetBinding struct {
	ProvisioningID string `json:"provisioning_id"`
	SSHFingerprint string `json:"ssh_host_key_fingerprint"`
	NodeUID        string `json:"node_uid,omitempty"`
}

func nonzeroUUID(value string) bool {
	return canonicalUUID.MatchString(value) && value != "00000000-0000-0000-0000-000000000000"
}

func (target TargetBinding) valid(source Binding) bool {
	if !nonzeroUUID(target.ProvisioningID) || (target.NodeUID != "" && (!nonzeroUUID(target.NodeUID) || target.NodeUID == source.SourceUID)) {
		return false
	}
	encoded, prefixed := strings.CutPrefix(target.SSHFingerprint, "SHA256:")
	if !prefixed || len(encoded) != 43 {
		return false
	}
	digest, err := base64.RawStdEncoding.Strict().DecodeString(encoded)
	return err == nil && len(digest) == 32 && base64.RawStdEncoding.EncodeToString(digest) == encoded
}
