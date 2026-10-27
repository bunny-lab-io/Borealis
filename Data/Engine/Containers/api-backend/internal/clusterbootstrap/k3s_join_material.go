package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"reflect"
	"slices"
	"sync"
	"unicode/utf8"
)

const MaxK3sJoinTokenBytes = 4096
const MaxK3sJoinCABytes = 64 << 10

var ErrK3sJoinMaterial = errors.New("K3s join material unavailable or changed; private data withheld")

// K3sJoinMaterial owns a source-proved secure server token for exactly one
// current target claim. Construction verifies format and CA binding; caller
// must independently prove original controller/source custody before supplying
// bytes and throughout export. No short token, agent token or invented CA hash.
// This object grants no admission, filesystem or runtime authority.
type K3sJoinMaterial struct{ owned *k3sJoinMaterial }

type k3sJoinMaterial struct {
	mu        sync.Mutex
	expected  PreparationExpected
	source    engineidentity.Binding
	token, ca []byte
	digest    string
	closed    bool
}

func (K3sJoinMaterial) String() string               { return "K3s join material [redacted]" }
func (K3sJoinMaterial) GoString() string             { return "K3s join material [redacted]" }
func (K3sJoinMaterial) MarshalJSON() ([]byte, error) { return nil, ErrK3sJoinMaterial }

func ValidateK3sJoinBinding(expected PreparationExpected, source engineidentity.Binding) error {
	if expected.Validate() != nil || validatePrejoinSource(expected.Target, source) != nil || expected.K3sVersion != K3sPins().Version {
		return ErrK3sJoinMaterial
	}
	return nil
}

func NewK3sJoinMaterial(expected PreparationExpected, source engineidentity.Binding, token, ca []byte) (*K3sJoinMaterial, error) {
	if ValidateK3sJoinBinding(expected, source) != nil ||
		len(token) == 0 || len(token) > MaxK3sJoinTokenBytes || !utf8.Valid(token) || bytes.IndexAny(token, "\x00\r\n") >= 0 {
		return nil, ErrK3sJoinMaterial
	}
	// Native token-file reader removes its one trailing newline before calling.
	// Preserve password syntax; delimiters are parsed only at their fixed prefix.
	const credentialOffset = 3 + 64 + len("::server:")
	if len(token) <= credentialOffset || !bytes.Equal(token[:3], []byte("K10")) || !digestPattern.Match(token[3:67]) ||
		!bytes.Equal(token[67:credentialOffset], []byte("::server:")) {
		return nil, ErrK3sJoinMaterial
	}
	caHash, err := k3sJoinCAHash(ca)
	if err != nil || subtle.ConstantTimeCompare(token[3:67], []byte(caHash)) != 1 {
		return nil, ErrK3sJoinMaterial
	}
	expected.PeerAddresses = slices.Clone(expected.PeerAddresses)
	// Transfer/export remains bound to the exact live claim. Content identity
	// must survive a stopped-producer takeover so a later executor can prove
	// the original effect without pretending it was a new installation.
	stable := expected
	stable.Target.HolderID, stable.Target.Generation, stable.Target.OperationAttempt = "", 0, 0
	metadata, err := json.Marshal(struct {
		Domain   string
		Expected PreparationExpected
		Source   engineidentity.Binding
	}{"prejoin-k3s-join-material-v1", stable, source})
	if err != nil {
		return nil, ErrK3sJoinMaterial
	}
	h := sha256.New()
	h.Write(metadata)
	h.Write([]byte{0})
	h.Write(token)
	h.Write([]byte{0})
	h.Write(ca)
	return &K3sJoinMaterial{owned: &k3sJoinMaterial{expected: expected, source: source, token: bytes.Clone(token), ca: bytes.Clone(ca), digest: hex.EncodeToString(h.Sum(nil))}}, nil
}

// Match pinned K3s clientaccess hashCA: one certificate hashes literal PEM;
// a chain hashes its verified root DER. Reject malformed or unverified chains
// instead of falling back to a different hash when chain validation fails.
// Reference: k3s-io/k3s v1.36.3+k3s1 pkg/clientaccess/token.go.
func k3sJoinCAHash(raw []byte) (string, error) {
	if len(raw) == 0 || len(raw) > MaxK3sJoinCABytes {
		return "", ErrK3sJoinMaterial
	}
	remaining := raw
	certs := []*x509.Certificate{}
	for len(bytes.TrimSpace(remaining)) != 0 {
		if len(certs) >= 16 || !bytes.HasPrefix(bytes.TrimSpace(remaining), []byte("-----BEGIN CERTIFICATE-----")) {
			return "", ErrK3sJoinMaterial
		}
		block, rest := pem.Decode(remaining)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return "", ErrK3sJoinMaterial
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !cert.IsCA || !cert.BasicConstraintsValid || cert.KeyUsage&x509.KeyUsageCertSign == 0 {
			return "", ErrK3sJoinMaterial
		}
		certs = append(certs, cert)
		remaining = rest
	}
	if len(certs) == 0 {
		return "", ErrK3sJoinMaterial
	}
	toHash := raw
	if len(certs) == 1 {
		if certs[0].CheckSignatureFrom(certs[0]) != nil {
			return "", ErrK3sJoinMaterial
		}
	} else {
		roots, intermediates := x509.NewCertPool(), x509.NewCertPool()
		for _, cert := range certs[1:] {
			if len(cert.AuthorityKeyId) == 0 || bytes.Equal(cert.AuthorityKeyId, cert.SubjectKeyId) {
				if cert.CheckSignatureFrom(cert) != nil {
					return "", ErrK3sJoinMaterial
				}
				roots.AddCert(cert)
			} else {
				intermediates.AddCert(cert)
			}
		}
		chains, err := certs[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates})
		if err != nil || len(chains) != 1 || len(chains[0]) < 2 {
			return "", ErrK3sJoinMaterial
		}
		chain := chains[0]
		toHash = chain[len(chain)-1].Raw
	}
	h := sha256.Sum256(toHash)
	return hex.EncodeToString(h[:]), nil
}

// WriteToken is for a fixed private receiver, never public JSON/log/command
// arguments. Caller supplies current exact claim and source on every export.
func (m *K3sJoinMaterial) WriteToken(ctx context.Context, expected PreparationExpected, source engineidentity.Binding, check func(context.Context) error, out io.Writer) error {
	if m == nil || m.owned == nil || check == nil || out == nil {
		return ErrK3sJoinMaterial
	}
	d := m.owned
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || expected.Validate() != nil || source != d.source || !reflect.DeepEqual(expected, d.expected) || ctx.Err() != nil || check(ctx) != nil {
		return ErrK3sJoinMaterial
	}
	raw := bytes.Clone(d.token)
	defer clear(raw)
	n, err := out.Write(raw)
	if err != nil || n != len(d.token) || ctx.Err() != nil || check(ctx) != nil {
		return ErrK3sJoinMaterial
	}
	return nil
}

func (m *K3sJoinMaterial) Verify(ctx context.Context, expected PreparationExpected, source engineidentity.Binding, check func(context.Context) error) error {
	if m == nil || m.owned == nil || imageBoundary(ctx, check) != nil {
		return ErrK3sJoinMaterial
	}
	d := m.owned
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || !reflect.DeepEqual(expected, d.expected) || source != d.source || imageBoundary(ctx, check) != nil {
		return ErrK3sJoinMaterial
	}
	return nil
}

func (m *K3sJoinMaterial) Close() error {
	if m == nil || m.owned == nil {
		return nil
	}
	d := m.owned
	d.mu.Lock()
	defer d.mu.Unlock()
	clear(d.token)
	clear(d.ca)
	d.token, d.ca, d.digest, d.closed = nil, nil, "", true
	return nil
}
