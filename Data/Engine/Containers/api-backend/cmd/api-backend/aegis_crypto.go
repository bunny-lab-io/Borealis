package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

const (
	aegisEnvelopePrefix        = "aegis:v1:"
	aegisVerificationPlaintext = "7f5c2a1d-6e8b-4f3b-a0d1-9c3f77b34d52"
	aegisKDFName               = "scrypt"
	aegisKeyLength             = 32
	aegisNonceLength           = 12
)

type goAegisService struct {
	db         *sql.DB
	hmacSecret []byte
	mu         sync.RWMutex
	key        []byte
}

type aegisState struct {
	Configured        bool
	KDFName           string
	KDFParamsJSON     string
	VerificationToken string
	UpdatedAt         int64
}

func newGoAegisService(db *sql.DB, hmacSecret []byte) *goAegisService {
	return &goAegisService{db: db, hmacSecret: append([]byte(nil), hmacSecret...)}
}

func (s *goAegisService) status(ctx context.Context) (map[string]any, error) {
	state, err := s.state(ctx)
	if err != nil {
		return nil, err
	}
	locked := false
	if state.Configured {
		s.mu.RLock()
		locked = len(s.key) == 0
		s.mu.RUnlock()
	}
	return map[string]any{
		"configured":   state.Configured,
		"locked":       locked,
		"unlock_scope": "engine_global",
		"secret_scope": []string{"credentials", "github_token", "operator_auth"},
		"updated_at":   state.UpdatedAt,
	}, nil
}

func (s *goAegisService) unlockWithCipher(ctx context.Context, cipherText string) (map[string]any, error) {
	key, err := s.deriveAndVerify(ctx, cipherText)
	if err != nil {
		return nil, err
	}
	if s != nil && s.db != nil {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		committed := false
		defer func() {
			if !committed {
				_ = tx.Rollback()
			}
		}()
		if err := s.migrateLegacyOperatorAuth(ctx, tx, key); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		committed = true
	}
	s.mu.Lock()
	s.key = append([]byte(nil), key...)
	s.mu.Unlock()
	return s.status(ctx)
}

func (s *goAegisService) decryptSecretText(ctx context.Context, value any) (string, error) {
	text, err := aegisDBText(value)
	if err != nil || text == "" {
		return "", err
	}
	state, err := s.state(ctx)
	if err != nil {
		return "", err
	}
	if !state.Configured {
		if strings.HasPrefix(text, aegisEnvelopePrefix) {
			return "", errors.New("Aegis-encrypted secret exists before Aegis setup")
		}
		return text, nil
	}
	if !strings.HasPrefix(text, aegisEnvelopePrefix) {
		return "", errors.New("protected secret is not stored as an Aegis envelope")
	}
	key, err := s.activeKey()
	if err != nil {
		return "", err
	}
	return aegisDecryptText(text, key)
}

func (s *goAegisService) encryptSecretText(ctx context.Context, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	state, err := s.state(ctx)
	if err != nil {
		return "", err
	}
	if !state.Configured {
		return "", errors.New("Aegis Cipher must be set up before protected secrets can be stored")
	}
	key, err := s.activeKey()
	if err != nil {
		return "", err
	}
	return aegisEncryptText(value, key)
}

func (s *goAegisService) activeKey() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.key) == 0 {
		return nil, errAegisLocked
	}
	return append([]byte(nil), s.key...), nil
}

func (s *goAegisService) deriveAndVerify(ctx context.Context, cipherText string) ([]byte, error) {
	if strings.TrimSpace(cipherText) == "" {
		return nil, fmt.Errorf("%w: Aegis Cipher is required", errAegisInvalidRequest)
	}
	state, err := s.state(ctx)
	if err != nil {
		return nil, err
	}
	if !state.Configured {
		return nil, errAegisNotConfigured
	}
	key, err := s.deriveKeyFromState(cipherText, state)
	if err != nil {
		return nil, err
	}
	plain, err := aegisDecryptText(state.VerificationToken, key)
	if err != nil || plain != aegisVerificationPlaintext {
		return nil, errAegisInvalidCipher
	}
	return key, nil
}

func (s *goAegisService) state(ctx context.Context) (aegisState, error) {
	if s == nil || s.db == nil {
		return aegisState{}, errors.New("aegis store unavailable")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return aegisState{}, err
	}
	defer conn.Close()
	var state aegisState
	err = conn.QueryRowContext(ctx, `
		SELECT COALESCE(kdf_name, ''), COALESCE(kdf_params_json, '{}'), COALESCE(verification_token, ''), COALESCE(updated_at, 0)
		  FROM engine.aegis_cipher_state
		 WHERE id=1
	`).Scan(&state.KDFName, &state.KDFParamsJSON, &state.VerificationToken, &state.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return aegisState{}, nil
	}
	if err != nil {
		return aegisState{}, err
	}
	state.Configured = true
	return state, nil
}

type aegisKDFParams struct {
	salt   []byte
	n      int
	r      int
	p      int
	length int
}

func parseAegisKDFParams(state aegisState) (aegisKDFParams, error) {
	if strings.ToLower(strings.TrimSpace(state.KDFName)) != aegisKDFName {
		return aegisKDFParams{}, errors.New("stored Aegis KDF is not supported")
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(firstText(state.KDFParamsJSON, "{}")), &raw); err != nil {
		return aegisKDFParams{}, errors.New("stored Aegis KDF parameters are invalid JSON")
	}
	saltB64 := strings.TrimSpace(fmt.Sprint(raw["salt_b64"]))
	salt, err := base64.StdEncoding.DecodeString(saltB64)
	if err != nil || len(salt) == 0 {
		return aegisKDFParams{}, errors.New("stored Aegis salt is invalid")
	}
	return aegisKDFParams{
		salt:   salt,
		n:      intFromAnyDefault(raw["n"], 32768),
		r:      intFromAnyDefault(raw["r"], 8),
		p:      intFromAnyDefault(raw["p"], 1),
		length: intFromAnyDefault(raw["length"], aegisKeyLength),
	}, nil
}

func aegisEncryptText(plaintext string, key []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aegisNonceLength)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	raw := append(nonce, sealed...)
	return aegisEnvelopePrefix + base64.StdEncoding.EncodeToString(raw), nil
}

func aegisDecryptText(value string, key []byte) (string, error) {
	if !strings.HasPrefix(value, aegisEnvelopePrefix) {
		return "", errors.New("stored value is not an Aegis envelope")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(strings.TrimPrefix(value, aegisEnvelopePrefix)))
	if err != nil {
		return "", errors.New("stored Aegis envelope is not valid Base64")
	}
	if len(raw) <= aegisNonceLength {
		return "", errors.New("stored Aegis envelope is truncated")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, raw[:aegisNonceLength], raw[aegisNonceLength:], nil)
	if err != nil {
		return "", errors.New("stored Aegis envelope could not be decrypted")
	}
	return string(plain), nil
}

func aegisDBText(value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "", nil
	case string:
		return typed, nil
	case []byte:
		return string(typed), nil
	default:
		return fmt.Sprint(value), nil
	}
}

func intFromAnyDefault(value any, fallback int) int {
	parsed := parseInt64Any(value)
	if parsed > 0 && int64FitsInt(parsed) {
		return int(parsed)
	}
	return fallback
}

func randomBase32Secret() (string, error) {
	raw := make([]byte, 20)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw), nil
}
