package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"borealis/api-backend/internal/clusterremote"
)

var errClusterSSHCredentials = errors.New("cluster SSH credentials unavailable, expired or changed")

// These private types never enter ordinary operation/event payloads. Their
// explicit JSON encoding is solely for authenticated Aegis encryption.
type clusterSSHCredentialBinding struct {
	ClusterID   string `json:"cluster_id"`
	OperationID string `json:"operation_id"`
	TargetID    string `json:"target_id"`
	Address     string `json:"address"`
	Port        int    `json:"port"`
	Fingerprint string `json:"host_key_fingerprint"`
}

func (binding clusterSSHCredentialBinding) valid() bool {
	for _, id := range []string{binding.ClusterID, binding.OperationID, binding.TargetID} {
		if !clusterUUIDRE.MatchString(id) {
			return false
		}
	}
	if (clusterremote.Target{Address: binding.Address, Port: binding.Port}).Validate() != nil {
		return false
	}
	encoded, ok := strings.CutPrefix(binding.Fingerprint, "SHA256:")
	raw, err := base64.RawStdEncoding.Strict().DecodeString(encoded)
	return ok && err == nil && len(raw) == 32 && len(encoded) == 43 && base64.RawStdEncoding.EncodeToString(raw) == encoded
}

type clusterSSHCredentialEnvelope struct {
	Version      int                         `json:"version"`
	Binding      clusterSSHCredentialBinding `json:"binding"`
	Username     string                      `json:"username"`
	Method       string                      `json:"auth_method"`
	Password     string                      `json:"password,omitempty"`
	PrivateKey   string                      `json:"private_key,omitempty"`
	Passphrase   string                      `json:"passphrase,omitempty"`
	SudoPassword string                      `json:"sudo_password,omitempty"`
}

func (clusterSSHCredentialEnvelope) String() string   { return "cluster SSH credentials [redacted]" }
func (clusterSSHCredentialEnvelope) GoString() string { return "cluster SSH credentials [redacted]" }

func (envelope clusterSSHCredentialEnvelope) validate() error {
	if envelope.Version != 1 || !envelope.Binding.valid() || clusterremote.ValidateSudoPassword([]byte(envelope.SudoPassword)) != nil {
		return errClusterSSHCredentials
	}
	var credential *clusterremote.Credential
	var err error
	switch envelope.Method {
	case "password":
		if envelope.PrivateKey != "" || envelope.Passphrase != "" {
			return errClusterSSHCredentials
		}
		credential, err = clusterremote.PasswordCredential(envelope.Username, []byte(envelope.Password))
	case "private_key":
		if envelope.Password != "" {
			return errClusterSSHCredentials
		}
		credential, err = clusterremote.KeyCredential(envelope.Username, []byte(envelope.PrivateKey), []byte(envelope.Passphrase))
	default:
		return errClusterSSHCredentials
	}
	if err != nil {
		return errClusterSSHCredentials
	}
	credential.Destroy()
	return nil
}

// sealedClusterSSHCredentials retains the Aegis generation read before crypto.
// Store must compare it under the Aegis row lock before committing ciphertext;
// workers compare it again before decrypting or renewing execution authority.
type sealedClusterSSHCredentials struct {
	binding    clusterSSHCredentialBinding
	generation string
	ciphertext string
}

func (sealedClusterSSHCredentials) String() string {
	return "sealed cluster SSH credentials [redacted]"
}
func (sealedClusterSSHCredentials) GoString() string {
	return "sealed cluster SSH credentials [redacted]"
}

func (s *goAegisService) sealClusterSSHCredentials(ctx context.Context, envelope clusterSSHCredentialEnvelope) (sealedClusterSSHCredentials, error) {
	if envelope.validate() != nil {
		return sealedClusterSSHCredentials{}, errClusterSSHCredentials
	}
	state, err := s.state(ctx)
	if err != nil || !state.Configured {
		return sealedClusterSSHCredentials{}, errClusterSSHCredentials
	}
	// state has returned its connection before key verification and encryption.
	key, err := s.activeKey()
	if err != nil {
		return sealedClusterSSHCredentials{}, errClusterSSHCredentials
	}
	defer clear(key)
	verified, err := aegisDecryptText(state.VerificationToken, key)
	if err != nil || verified != aegisVerificationPlaintext {
		return sealedClusterSSHCredentials{}, errClusterSSHCredentials
	}
	raw, err := json.Marshal(envelope)
	if err != nil || len(raw) > clusterSSHBodyMaxBytes {
		return sealedClusterSSHCredentials{}, errClusterSSHCredentials
	}
	defer clear(raw)
	ciphertext, err := aegisEncryptText(string(raw), key)
	if err != nil {
		return sealedClusterSSHCredentials{}, errClusterSSHCredentials
	}
	return sealedClusterSSHCredentials{binding: envelope.Binding, generation: state.VerificationToken, ciphertext: ciphertext}, nil
}

func (s *goAegisService) openClusterSSHCredentials(ctx context.Context, sealed sealedClusterSSHCredentials, expected clusterSSHCredentialBinding) (clusterSSHCredentialEnvelope, error) {
	if !expected.valid() || sealed.binding != expected || len(sealed.ciphertext) > 2*clusterSSHBodyMaxBytes ||
		!strings.HasPrefix(sealed.ciphertext, aegisEnvelopePrefix) {
		return clusterSSHCredentialEnvelope{}, errClusterSSHCredentials
	}
	state, err := s.state(ctx)
	if err != nil || !state.Configured || sealed.generation != state.VerificationToken {
		return clusterSSHCredentialEnvelope{}, errClusterSSHCredentials
	}
	key, err := s.activeKey()
	if err != nil {
		return clusterSSHCredentialEnvelope{}, errClusterSSHCredentials
	}
	defer clear(key)
	verified, err := aegisDecryptText(state.VerificationToken, key)
	if err != nil || verified != aegisVerificationPlaintext {
		return clusterSSHCredentialEnvelope{}, errClusterSSHCredentials
	}
	plaintext, err := aegisDecryptText(sealed.ciphertext, key)
	if err != nil || len(plaintext) > clusterSSHBodyMaxBytes || !utf8.ValidString(plaintext) {
		return clusterSSHCredentialEnvelope{}, errClusterSSHCredentials
	}
	raw := []byte(plaintext)
	defer clear(raw)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var envelope clusterSSHCredentialEnvelope
	if decoder.Decode(&envelope) != nil {
		return clusterSSHCredentialEnvelope{}, errClusterSSHCredentials
	}
	if _, err := decoder.Token(); err != io.EOF || envelope.Binding != expected || envelope.validate() != nil {
		return clusterSSHCredentialEnvelope{}, errClusterSSHCredentials
	}
	return envelope, nil
}
