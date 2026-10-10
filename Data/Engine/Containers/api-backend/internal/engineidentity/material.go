// Package engineidentity validates the fixed Engine identity shared by API
// replicas. It does not elect an authority, change files, or distribute keys.
package engineidentity

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"regexp"
	"strings"
)

const (
	MaxEnvelopeBytes = 16 * 1024
	EngineSecretPath = "engine_secret.txt"
	AgentJWTPath     = "Auth_Tokens/borealis-jwt-ed25519.key"
	ScriptKeyPath    = "Certificates/Code-Signing/borealis-script-ed25519.key"
	ScriptPublicPath = "Certificates/Code-Signing/borealis-script-ed25519.pub"
)

var canonicalUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Binding identifies the cluster and explicitly selected source. Callers must
// compare it with independently observed identities before accepting material.
type Binding struct {
	ClusterID string `json:"cluster_id"`
	SourceID  string `json:"source_node_id"`
	SourceUID string `json:"source_node_uid"`
}

// Material keeps secrets private to prevent accidental structured logging.
// Export serializes sensitive data only for an authenticated transport or a
// private rollback journal; its result must never be logged.
type Material struct {
	secret string
	jwt    ed25519.PrivateKey
	script ed25519.PrivateKey
}

func (Material) String() string   { return "Engine identity material [redacted]" }
func (Material) GoString() string { return "Engine identity material [redacted]" }

type envelope struct {
	Version       int    `json:"version"`
	ClusterID     string `json:"cluster_id"`
	SourceID      string `json:"source_node_id"`
	SourceUID     string `json:"source_node_uid"`
	EngineSecret  string `json:"engine_secret"`
	AgentJWT      string `json:"agent_jwt_private_pem"`
	ScriptPrivate string `json:"script_private_pem"`
	ScriptPublic  []byte `json:"script_public_der"`
}

// ParseFiles validates all four fixed identity files together. It never creates
// missing material or repairs mismatched keys. PEM formatting is normalized on
// export; private/public identity remains unchanged.
func ParseFiles(files map[string][]byte) (*Material, error) {
	if len(files) != 4 {
		return nil, errors.New("exactly four fixed Engine identity files required")
	}
	for _, name := range []string{EngineSecretPath, AgentJWTPath, ScriptKeyPath, ScriptPublicPath} {
		if len(files[name]) == 0 || len(files[name]) > MaxEnvelopeBytes/4 {
			return nil, errors.New("Engine identity file missing or oversized")
		}
	}
	secret := strings.TrimSpace(string(files[EngineSecretPath]))
	if len(secret) < 32 || len(secret) > 256 {
		return nil, errors.New("Engine session secret length invalid")
	}
	for _, value := range []byte(secret) {
		if value < 0x21 || value > 0x7e {
			return nil, errors.New("Engine session secret encoding invalid")
		}
	}
	jwt, err := parsePrivateKey(files[AgentJWTPath])
	if err != nil {
		return nil, err
	}
	script, err := parsePrivateKey(files[ScriptKeyPath])
	if err != nil {
		return nil, err
	}
	public, err := x509.ParsePKIXPublicKey(files[ScriptPublicPath])
	if err != nil {
		return nil, errors.New("Engine script public key invalid")
	}
	edPublic, ok := public.(ed25519.PublicKey)
	if !ok || subtle.ConstantTimeCompare(edPublic, script.Public().(ed25519.PublicKey)) != 1 {
		return nil, errors.New("Engine script keypair does not match")
	}
	return &Material{secret: secret, jwt: jwt, script: script}, nil
}

func parsePrivateKey(raw []byte) (ed25519.PrivateKey, error) {
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("-----BEGIN PRIVATE KEY-----")) {
		return nil, errors.New("Engine private key PEM invalid")
	}
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "PRIVATE KEY" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("Engine private key PEM invalid")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("Engine private key PKCS8 invalid")
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok || len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("Engine private key must be Ed25519")
	}
	return key, nil
}

func privatePEM(key ed25519.PrivateKey) []byte {
	raw, _ := x509.MarshalPKCS8PrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw})
}

// Files returns new sensitive byte slices at the fixed relative paths. Callers
// own private file modes, ownership, quiescence, atomic writes and rollback.
func (m *Material) Files() map[string][]byte {
	if !m.valid() {
		return nil
	}
	public, _ := x509.MarshalPKIXPublicKey(m.script.Public())
	return map[string][]byte{
		EngineSecretPath: []byte(m.secret + "\n"),
		AgentJWTPath:     privatePEM(m.jwt), ScriptKeyPath: privatePEM(m.script),
		ScriptPublicPath: public,
	}
}

// Digest commits to all private identity components in canonical form. It is
// an internal equality guard, not an authorization proof or a public API field.
func (m *Material) Digest() string {
	if !m.valid() {
		return ""
	}
	hash := sha256.New()
	hash.Write([]byte("borealis-engine-identity-v1\x00"))
	for _, raw := range [][]byte{[]byte(m.secret), m.jwt.Seed(), m.script.Seed()} {
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(raw)))
		hash.Write(size[:])
		hash.Write(raw)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (m *Material) Equal(other *Material) bool {
	return m.valid() && other.valid() && subtle.ConstantTimeCompare([]byte(m.Digest()), []byte(other.Digest())) == 1
}

func (m *Material) valid() bool {
	return m != nil && len(m.secret) >= 32 && len(m.jwt) == ed25519.PrivateKeySize && len(m.script) == ed25519.PrivateKeySize
}

func validBinding(binding Binding) bool {
	for _, value := range []string{binding.ClusterID, binding.SourceID, binding.SourceUID} {
		if !canonicalUUID.MatchString(value) || value == "00000000-0000-0000-0000-000000000000" {
			return false
		}
	}
	return true
}

func (m *Material) Export(binding Binding) ([]byte, error) {
	if !m.valid() || !validBinding(binding) {
		return nil, errors.New("Engine identity export binding invalid")
	}
	files := m.Files()
	return json.Marshal(envelope{Version: 1, ClusterID: binding.ClusterID, SourceID: binding.SourceID,
		SourceUID: binding.SourceUID, EngineSecret: m.secret, AgentJWT: string(files[AgentJWTPath]),
		ScriptPrivate: string(files[ScriptKeyPath]), ScriptPublic: files[ScriptPublicPath]})
}

// Import requires independently observed binding and rejects ambiguous JSON.
// Its errors never include request data, key parser details, or private paths.
func Import(raw []byte, expected Binding) (*Material, error) {
	if len(raw) == 0 || len(raw) > MaxEnvelopeBytes || !validBinding(expected) {
		return nil, errors.New("Engine identity envelope size or binding invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return nil, errors.New("Engine identity envelope invalid")
	}
	fields := map[string]json.RawMessage{}
	allowed := map[string]bool{"version": true, "cluster_id": true, "source_node_id": true,
		"source_node_uid": true, "engine_secret": true, "agent_jwt_private_pem": true,
		"script_private_pem": true, "script_public_der": true}
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok || !allowed[name] || fields[name] != nil {
			return nil, errors.New("Engine identity envelope fields invalid")
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(value, []byte("null")) {
			return nil, errors.New("Engine identity envelope value invalid")
		}
		fields[name] = value
	}
	if close, err := decoder.Token(); err != nil || close != json.Delim('}') || len(fields) != len(allowed) {
		return nil, errors.New("Engine identity envelope incomplete")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("Engine identity envelope trailing data")
	}
	var payload envelope
	if json.Unmarshal(raw, &payload) != nil || payload.Version != 1 ||
		payload.ClusterID != expected.ClusterID || payload.SourceID != expected.SourceID || payload.SourceUID != expected.SourceUID {
		return nil, errors.New("Engine identity envelope binding mismatch")
	}
	return ParseFiles(map[string][]byte{EngineSecretPath: []byte(payload.EngineSecret),
		AgentJWTPath: []byte(payload.AgentJWT), ScriptKeyPath: []byte(payload.ScriptPrivate), ScriptPublicPath: payload.ScriptPublic})
}
