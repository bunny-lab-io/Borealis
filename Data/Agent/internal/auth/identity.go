package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
)

type Identity struct {
	PrivateKey  ed25519.PrivateKey
	PublicKey   ed25519.PublicKey
	PublicDER   []byte
	Fingerprint string
}

func LoadOrCreateIdentity(cfg *agentconfig.AgentConfig) (Identity, bool, error) {
	if cfg == nil {
		return Identity{}, false, fmt.Errorf("nil config")
	}
	if strings.TrimSpace(cfg.Identity.PrivateKeyPKCS8B64) != "" && strings.TrimSpace(cfg.Identity.PublicKeySPKIB64) != "" {
		identity, err := LoadIdentity(cfg.Identity.PrivateKeyPKCS8B64, cfg.Identity.PublicKeySPKIB64)
		if err == nil {
			return identity, false, nil
		}
	}
	identity, err := NewIdentity()
	if err != nil {
		return Identity{}, false, err
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(identity.PrivateKey)
	if err != nil {
		return Identity{}, false, err
	}
	cfg.Identity.PrivateKeyPKCS8B64 = base64.StdEncoding.EncodeToString(privateDER)
	cfg.Identity.PublicKeySPKIB64 = base64.StdEncoding.EncodeToString(identity.PublicDER)
	return identity, true, nil
}

func NewIdentity() (Identity, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Identity{}, err
	}
	publicDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return Identity{}, err
	}
	return Identity{
		PrivateKey:  priv,
		PublicKey:   pub,
		PublicDER:   publicDER,
		Fingerprint: Fingerprint(publicDER),
	}, nil
}

func LoadIdentity(privateB64 string, publicB64 string) (Identity, error) {
	privateDER, err := base64.StdEncoding.DecodeString(strings.TrimSpace(privateB64))
	if err != nil {
		return Identity{}, err
	}
	key, err := x509.ParsePKCS8PrivateKey(privateDER)
	if err != nil {
		return Identity{}, err
	}
	privateKey, ok := key.(ed25519.PrivateKey)
	if !ok {
		return Identity{}, fmt.Errorf("private key is %T, not ed25519", key)
	}
	publicDER, err := base64.StdEncoding.DecodeString(strings.TrimSpace(publicB64))
	if err != nil {
		return Identity{}, err
	}
	parsedPub, err := x509.ParsePKIXPublicKey(publicDER)
	if err != nil {
		return Identity{}, err
	}
	publicKey, ok := parsedPub.(ed25519.PublicKey)
	if !ok {
		return Identity{}, fmt.Errorf("public key is %T, not ed25519", parsedPub)
	}
	return Identity{
		PrivateKey:  privateKey,
		PublicKey:   publicKey,
		PublicDER:   publicDER,
		Fingerprint: Fingerprint(publicDER),
	}, nil
}

func (i Identity) PublicB64() string {
	return base64.StdEncoding.EncodeToString(i.PublicDER)
}

func (i Identity) Sign(payload []byte) []byte {
	return ed25519.Sign(i.PrivateKey, payload)
}

func Fingerprint(publicDER []byte) string {
	sum := sha256.Sum256(publicDER)
	return hex.EncodeToString(sum[:])
}
