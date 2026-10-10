package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"io"
	"time"
	"unicode/utf8"
)

const (
	clusterSSHVIPStartPath  = "/internal/cluster/ssh-vip-scope"
	clusterSSHVIPCheckPath  = "/internal/cluster/ssh-vip-check"
	clusterSSHVIPFrameLimit = 16 << 10
)

type clusterSSHVIPStart struct {
	Request clusterSSHSourceBrokerRequest    `json:"request"`
	Sources []clusterbootstrap.SourceNetwork `json:"sources"`
}

func (clusterSSHVIPStart) String() string   { return "VIP scope start [redacted]" }
func (clusterSSHVIPStart) GoString() string { return "VIP scope start [redacted]" }

type clusterSSHVIPMessage struct {
	Version int                 `json:"version"`
	ID      string              `json:"id"`
	Scope   string              `json:"scope"`
	Status  string              `json:"status"`
	Owner   *clusterSSHVIPOwner `json:"owner"`
}
type clusterSSHVIPControl struct {
	Version int    `json:"version"`
	ID      string `json:"id"`
	Start   string `json:"start"`
	Scope   string `json:"scope"`
	Step    string `json:"step"`
}

func vipScopeUUID(v string) bool { return clusterbootstrap.ValidSourceReceiptIdentity(v, v, v) }
func (v clusterSSHVIPControl) valid() bool {
	return v.Version == 1 && vipScopeUUID(v.ID) && vipScopeUUID(v.Start) && vipScopeUUID(v.Scope) && (v.Step == "authority" || v.Step == "inputs" || v.Step == "finish")
}

// Separate fixed direction/path contexts keep snapshot and scope protocols
// cryptographically distinct under the existing internal operator secret.
func clusterSSHVIPCipher(secret, direction string) (cipher.AEAD, error) {
	if len(secret) < 32 || len(secret) > 4096 || (direction != "start-request" && direction != "start-response" && direction != "check-request" && direction != "check-response") {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("borealis/cluster/ssh-vip/v1/" + direction))
	key := mac.Sum(nil)
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	return cipher.NewGCMWithRandomNonce(block)
}
func sealClusterSSHVIP(aead cipher.AEAD, path string, value any, limit int) ([]byte, error) {
	if aead == nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	raw, err := json.Marshal(value)
	defer clear(raw)
	if err != nil || len(raw)+aead.Overhead() > limit {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	return aead.Seal(nil, nil, raw, []byte(path)), nil
}
func openClusterSSHVIP(aead cipher.AEAD, path string, raw []byte, value any, limit int) error {
	if aead == nil || len(raw) > limit {
		return clusterbootstrap.ErrPreparationConfig
	}
	plain, err := aead.Open(nil, nil, raw, []byte(path))
	defer clear(plain)
	if err != nil || !utf8.Valid(plain) || json.Unmarshal(plain, value) != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	canonical, err := json.Marshal(value)
	defer clear(canonical)
	if err != nil || !sameClusterSSHStoredJSON(plain, canonical, 0) {
		return clusterbootstrap.ErrPreparationConfig
	}
	return nil
}
func writeClusterSSHVIPFrame(w io.Writer, aead cipher.AEAD, v clusterSSHVIPMessage) error {
	raw, err := sealClusterSSHVIP(aead, clusterSSHVIPStartPath, v, clusterSSHVIPFrameLimit)
	if err != nil {
		return err
	}
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(raw)))
	frame := append(size[:], raw...)
	if n, err := w.Write(frame); err != nil || n != len(frame) {
		return clusterbootstrap.ErrPreparationConfig
	}
	return nil
}
func readClusterSSHVIPFrame(r io.Reader, aead cipher.AEAD) (clusterSSHVIPMessage, error) {
	var size [4]byte
	var v clusterSSHVIPMessage
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return v, clusterbootstrap.ErrPreparationConfig
	}
	n := binary.BigEndian.Uint32(size[:])
	if n == 0 || n > clusterSSHVIPFrameLimit {
		return v, clusterbootstrap.ErrPreparationConfig
	}
	raw := make([]byte, n)
	if _, err := io.ReadFull(r, raw); err != nil || openClusterSSHVIP(aead, clusterSSHVIPStartPath, raw, &v, clusterSSHVIPFrameLimit) != nil {
		return clusterSSHVIPMessage{}, clusterbootstrap.ErrPreparationConfig
	}
	return v, nil
}
func validClusterSSHVIPOwner(owner clusterSSHVIPOwner, a clusterSSHPreparationAuthority, sources []clusterbootstrap.SourceNetwork) bool {
	if validateClusterSSHSourceNetworks(a, sources) != nil || owner.Address != a.Source.ControlPlaneVIP || owner.Address != a.Source.EdgeVIP {
		return false
	}
	epoch := clusterbootstrap.VIPLease{UID: owner.LeaseUID, ResourceVersion: "projection", Holder: owner.Owner.Hostname, AcquireTime: owner.AcquireTime, RenewTime: owner.AcquireTime, Transitions: owner.Transitions, DurationSeconds: 10}
	if epoch.Validate() != nil {
		return false
	}
	for i, m := range a.Source.Members {
		expected := clusterSSHManagementPeer{ID: m.NodeID, NodeUID: m.NodeUID, Hostname: m.Name, MachineID: m.MachineID, BootID: m.BootID, SSHFingerprint: m.SSHFingerprint, Link: sources[i].ManagementLink}
		if owner.Owner == expected {
			return true
		}
	}
	return false
}
func validClusterSSHVIPStart(v clusterSSHVIPStart) bool {
	if !v.Request.valid(time.Now()) || len(v.Sources) < 1 || len(v.Sources) > 2 {
		return false
	}
	for _, source := range v.Sources {
		if source.Validate() != nil {
			return false
		}
	}
	return true
}
