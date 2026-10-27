package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

const K3sJoinSourcePath = "/internal/cluster/k3s-join-result"
const K3sJoinSourceSocketPath = "/v1/cluster-join-material"
const K3sJoinSourceRequestHeader = "X-Borealis-Join-Request"
const MaxK3sJoinSourceRequestBytes = 32 << 10
const MaxK3sJoinSourceDeliveryBytes = 128 << 10

// Public request carried by one immutable source Job. It grants no authority
// independently: original controller validates current claim/selected source,
// and native manager validates its actual local source before reading secrets.
type K3sJoinSourceRequest struct {
	Version   int                    `json:"version"`
	ID        string                 `json:"id"`
	ExpiresAt int64                  `json:"expires_at"`
	Expected  PreparationExpected    `json:"expected"`
	Source    engineidentity.Binding `json:"source"`
	Network   SourceNetwork          `json:"network"`
}

func (r K3sJoinSourceRequest) Validate(now time.Time) error {
	if r.Version != 1 || !nonzeroPreparationUUID(r.ID) || r.ExpiresAt <= now.Unix() || r.ExpiresAt > now.Unix()+90 ||
		ValidateK3sJoinBinding(r.Expected, r.Source) != nil || r.Network.Validate() != nil || r.Network.NodeUID != r.Source.SourceUID ||
		r.Network.K3sVersion != r.Expected.K3sVersion || r.Network.PodCIDR != r.Expected.PodCIDR || r.Network.ServiceCIDR != r.Expected.ServiceCIDR ||
		r.Network.ManagementLink.MatchesAddress(r.Expected.Target.Address) {
		return ErrK3sJoinMaterial
	}
	for _, peer := range r.Expected.PeerAddresses {
		if r.Network.ManagementLink.MatchesAddress(peer) {
			return nil
		}
	}
	return ErrK3sJoinMaterial
}

func ParseK3sJoinSourceRequest(raw []byte, now time.Time) (K3sJoinSourceRequest, error) {
	var r K3sJoinSourceRequest
	if len(raw) == 0 || len(raw) > MaxK3sJoinSourceRequestBytes || json.Unmarshal(raw, &r) != nil || r.Validate(now) != nil {
		return K3sJoinSourceRequest{}, ErrK3sJoinMaterial
	}
	canonical, err := json.Marshal(r)
	if err != nil || !bytes.Equal(raw, canonical) {
		return K3sJoinSourceRequest{}, ErrK3sJoinMaterial
	}
	return r, nil
}

func (r K3sJoinSourceRequest) Digest() (string, error) {
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > MaxK3sJoinSourceRequestBytes {
		return "", ErrK3sJoinMaterial
	}
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:]), nil
}

type K3sJoinSourceReceipt struct {
	Version          int    `json:"version"`
	ID               string `json:"id"`
	JobUID           string `json:"job_uid"`
	PodUID           string `json:"pod_uid"`
	RequestSHA256    string `json:"request_sha256"`
	CiphertextSHA256 string `json:"ciphertext_sha256"`
}

// Receipt is public evidence only after original controller reads it from the
// actual successful immutable Job/Pod. HTTP authentication alone cannot prove
// origin: API workers also possess the existing operator transport secret.
func (r K3sJoinSourceReceipt) Validate(request K3sJoinSourceRequest, jobUID, podUID string) error {
	digest, err := request.Digest()
	if err != nil || r.Version != 1 || r.ID != request.ID || !ValidSourceReceiptIdentity(r.ID, jobUID, podUID) ||
		r.JobUID != jobUID || r.PodUID != podUID || r.RequestSHA256 != digest || !digestPattern.MatchString(r.CiphertextSHA256) {
		return ErrK3sJoinMaterial
	}
	return nil
}

type k3sJoinSourceDelivery struct {
	Version       int    `json:"version"`
	ID            string `json:"id"`
	JobUID        string `json:"job_uid"`
	PodUID        string `json:"pod_uid"`
	RequestSHA256 string `json:"request_sha256"`
	Frame         []byte `json:"frame"`
}

func (k3sJoinSourceDelivery) String() string   { return "K3s source delivery [redacted]" }
func (k3sJoinSourceDelivery) GoString() string { return "K3s source delivery [redacted]" }

func k3sJoinSourceCipher(secret []byte) (cipher.AEAD, error) {
	if len(secret) < 32 || len(secret) > 4096 {
		return nil, ErrK3sJoinMaterial
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("borealis/cluster/k3s-join-source/v1/delivery"))
	key := mac.Sum(nil)
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrK3sJoinMaterial
	}
	return cipher.NewGCMWithRandomNonce(block)
}

// Seal uses a separate AEAD domain and random nonce. Only ciphertext crosses
// the internal callback; neither plaintext frame nor encoding goes into argv,
// logs, Kubernetes Job status or generic Engine API output.
func SealK3sJoinSourceDelivery(secret []byte, request K3sJoinSourceRequest, jobUID, podUID string, frame []byte, now time.Time) ([]byte, K3sJoinSourceReceipt, error) {
	if request.Validate(now) != nil || !ValidSourceReceiptIdentity(request.ID, jobUID, podUID) || len(frame) < 1 || len(frame) > MaxK3sJoinTokenBytes+MaxK3sJoinCABytes+1024 {
		return nil, K3sJoinSourceReceipt{}, ErrK3sJoinMaterial
	}
	aead, err := k3sJoinSourceCipher(secret)
	if err != nil {
		return nil, K3sJoinSourceReceipt{}, err
	}
	digest, err := request.Digest()
	if err != nil {
		return nil, K3sJoinSourceReceipt{}, err
	}
	raw, err := json.Marshal(k3sJoinSourceDelivery{1, request.ID, jobUID, podUID, digest, frame})
	defer clear(raw)
	if err != nil || len(raw)+aead.Overhead() > MaxK3sJoinSourceDeliveryBytes {
		return nil, K3sJoinSourceReceipt{}, ErrK3sJoinMaterial
	}
	wire := aead.Seal(nil, nil, raw, []byte(K3sJoinSourcePath))
	h := sha256.Sum256(wire)
	receipt := K3sJoinSourceReceipt{1, request.ID, jobUID, podUID, digest, hex.EncodeToString(h[:])}
	return wire, receipt, nil
}

// Open returns scope-owned private frame for a pending current request. Caller
// must clear it and must not consume it before separately observed Job proof.
func OpenK3sJoinSourceDelivery(secret []byte, request K3sJoinSourceRequest, wire []byte, now time.Time) ([]byte, K3sJoinSourceReceipt, error) {
	if request.Validate(now) != nil || len(wire) == 0 || len(wire) > MaxK3sJoinSourceDeliveryBytes {
		return nil, K3sJoinSourceReceipt{}, ErrK3sJoinMaterial
	}
	aead, err := k3sJoinSourceCipher(secret)
	if err != nil {
		return nil, K3sJoinSourceReceipt{}, err
	}
	raw, err := aead.Open(nil, nil, wire, []byte(K3sJoinSourcePath))
	defer clear(raw)
	if err != nil {
		return nil, K3sJoinSourceReceipt{}, ErrK3sJoinMaterial
	}
	var delivery k3sJoinSourceDelivery
	if json.Unmarshal(raw, &delivery) != nil {
		clear(delivery.Frame)
		return nil, K3sJoinSourceReceipt{}, ErrK3sJoinMaterial
	}
	canonical, err := json.Marshal(delivery)
	defer clear(canonical)
	h := sha256.Sum256(wire)
	receipt := K3sJoinSourceReceipt{delivery.Version, delivery.ID, delivery.JobUID, delivery.PodUID, delivery.RequestSHA256, hex.EncodeToString(h[:])}
	if err != nil || !bytes.Equal(raw, canonical) || receipt.Validate(request, delivery.JobUID, delivery.PodUID) != nil || len(delivery.Frame) < 1 || len(delivery.Frame) > MaxK3sJoinTokenBytes+MaxK3sJoinCABytes+1024 {
		clear(delivery.Frame)
		return nil, K3sJoinSourceReceipt{}, ErrK3sJoinMaterial
	}
	return delivery.Frame, receipt, nil
}

func ParseK3sJoinSourceReceipt(raw []byte, request K3sJoinSourceRequest, jobUID, podUID string) (K3sJoinSourceReceipt, error) {
	var receipt K3sJoinSourceReceipt
	if len(raw) == 0 || len(raw) > 4096 || json.Unmarshal(raw, &receipt) != nil || receipt.Validate(request, jobUID, podUID) != nil {
		return K3sJoinSourceReceipt{}, ErrK3sJoinMaterial
	}
	canonical, err := json.Marshal(receipt)
	if err != nil || !bytes.Equal(raw, canonical) {
		return K3sJoinSourceReceipt{}, ErrK3sJoinMaterial
	}
	return receipt, nil
}
