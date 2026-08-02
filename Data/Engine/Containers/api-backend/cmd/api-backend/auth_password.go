package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/scrypt"
)

const (
	passwordVerifierVersion = "borealis-password-v1"
	passwordVerifierKDF     = "scrypt"
	passwordVerifierPlain   = "plain"
	passwordVerifierSHA512  = "sha512"
	passwordVerifierN       = 32768
	passwordVerifierR       = 8
	passwordVerifierP       = 1
	passwordVerifierSaltLen = 16
	passwordVerifierKeyLen  = 32
)

var errPasswordVerifier = errors.New("invalid password verifier")

type passwordCredential struct {
	Plain        string
	LegacySHA512 string
}

func passwordCredentialFromBody(body map[string]any, plainKey string, legacyKey string) (passwordCredential, bool) {
	credential := passwordCredential{
		Plain:        passwordPlainText(body[plainKey]),
		LegacySHA512: strings.ToLower(cleanText(body[legacyKey])),
	}
	if credential.LegacySHA512 != "" && !isLegacySHA512Digest(credential.LegacySHA512) {
		return passwordCredential{}, false
	}
	if credential.Plain == "" && credential.LegacySHA512 == "" {
		return passwordCredential{}, false
	}
	return credential, true
}

func passwordPlainText(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func (c passwordCredential) verifierSecret(kind string) string {
	switch kind {
	case passwordVerifierPlain:
		return c.Plain
	case passwordVerifierSHA512:
		return c.LegacySHA512
	default:
		return ""
	}
}

func (c passwordCredential) preferredVerifierSecret() (string, string, bool) {
	if c.Plain != "" {
		return passwordVerifierPlain, c.Plain, true
	}
	if c.LegacySHA512 != "" {
		return passwordVerifierSHA512, c.LegacySHA512, true
	}
	return "", "", false
}

func newPasswordVerifierFromCredential(credential passwordCredential) (string, error) {
	kind, secret, ok := credential.preferredVerifierSecret()
	if !ok {
		return "", errPasswordVerifier
	}
	salt := make([]byte, passwordVerifierSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := scrypt.Key([]byte(secret), salt, passwordVerifierN, passwordVerifierR, passwordVerifierP, passwordVerifierKeyLen)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{
		passwordVerifierVersion,
		passwordVerifierKDF,
		kind,
		strconv.Itoa(passwordVerifierN),
		strconv.Itoa(passwordVerifierR),
		strconv.Itoa(passwordVerifierP),
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	}, "$"), nil
}

func verifyPasswordSecret(stored string, credential passwordCredential) (bool, bool, error) {
	stored = strings.TrimSpace(stored)
	if stored == "" {
		return false, false, nil
	}
	if strings.HasPrefix(stored, passwordVerifierVersion+"$") {
		ok, err := verifyPasswordVerifier(stored, credential)
		return ok, false, err
	}
	if isLegacySHA512Digest(stored) && credential.LegacySHA512 != "" {
		ok := subtle.ConstantTimeCompare([]byte(strings.ToLower(stored)), []byte(credential.LegacySHA512)) == 1
		return ok, ok, nil
	}
	return false, false, nil
}

func verifyPasswordVerifier(verifier string, credential passwordCredential) (bool, error) {
	parts := strings.Split(verifier, "$")
	if len(parts) != 8 || parts[0] != passwordVerifierVersion || parts[1] != passwordVerifierKDF {
		return false, errPasswordVerifier
	}
	kind := parts[2]
	secret := credential.verifierSecret(kind)
	if secret == "" {
		return false, nil
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil || n <= 0 {
		return false, errPasswordVerifier
	}
	r, err := strconv.Atoi(parts[4])
	if err != nil || r <= 0 {
		return false, errPasswordVerifier
	}
	p, err := strconv.Atoi(parts[5])
	if err != nil || p <= 0 {
		return false, errPasswordVerifier
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[6])
	if err != nil || len(salt) == 0 {
		return false, errPasswordVerifier
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[7])
	if err != nil || len(expected) == 0 {
		return false, errPasswordVerifier
	}
	actual, err := scrypt.Key([]byte(secret), salt, n, r, p, len(expected))
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

func passwordVerifierLooksValid(verifier string) bool {
	return strings.HasPrefix(strings.TrimSpace(verifier), passwordVerifierVersion+"$")
}

func isLegacySHA512Digest(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 128 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func encryptUserPassword(ctx context.Context, secret authSecretService, credential passwordCredential) (string, map[string]any, int, error) {
	if secret == nil {
		return "", map[string]any{"error": "aegis_unavailable"}, http.StatusInternalServerError, nil
	}
	verifier, err := newPasswordVerifierFromCredential(credential)
	if err != nil {
		return "", nil, http.StatusInternalServerError, err
	}
	encrypted, err := secret.encryptSecretText(ctx, verifier)
	if err != nil {
		return "", nil, http.StatusInternalServerError, err
	}
	return encrypted, nil, 0, nil
}

func decryptUserPassword(ctx context.Context, secret authSecretService, value any) (string, map[string]any, int, error) {
	if secret == nil {
		return "", map[string]any{"error": "aegis_unavailable"}, http.StatusInternalServerError, nil
	}
	plain, err := secret.decryptSecretText(ctx, value)
	if err != nil {
		return "", nil, http.StatusInternalServerError, err
	}
	return strings.TrimSpace(plain), nil, 0, nil
}

func upgradeLoginPasswordIfNeeded(ctx context.Context, auth *authService, store authLoginStore, username string, verifier string) error {
	verifier = strings.TrimSpace(verifier)
	if verifier == "" {
		return nil
	}
	if !passwordVerifierLooksValid(verifier) {
		return errPasswordVerifier
	}
	if auth == nil || auth.aegis == nil {
		return errors.New("aegis_unavailable")
	}
	encrypted, err := auth.aegis.encryptSecretText(ctx, verifier)
	if err != nil {
		return err
	}
	return store.updateUserPasswordSecret(ctx, username, encrypted, time.Now().Unix())
}

func passwordCredentialError(name string) map[string]any {
	return map[string]any{"error": fmt.Sprintf("%s required", name)}
}
