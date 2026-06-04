package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/scrypt"
)

var (
	errAegisAlreadyConfigured = errors.New("Aegis Cipher is already configured")
	errAegisNotConfigured     = errors.New("Aegis Cipher is not configured")
	errAegisInvalidCipher     = errors.New("Incorrect Aegis Cipher")
	errAegisLocked            = errors.New("Aegis Cipher has not been entered; protected secrets remain locked")
	errAegisDataCorruption    = errors.New("Aegis protected storage is corrupt")
	errAegisInvalidRequest    = errors.New("invalid Aegis request")
)

func (s *goAegisService) setupWithCipher(ctx context.Context, cipherText string) (map[string]any, error) {
	if strings.TrimSpace(cipherText) == "" {
		return nil, fmt.Errorf("%w: Aegis Cipher is required", errAegisInvalidRequest)
	}
	if s == nil || s.db == nil {
		return nil, errors.New("aegis store unavailable")
	}
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

	state, err := s.stateFromTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	if state.Configured {
		return nil, errAegisAlreadyConfigured
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	params := map[string]any{
		"salt_b64": base64.StdEncoding.EncodeToString(salt),
		"n":        32768,
		"r":        8,
		"p":        1,
		"length":   aegisKeyLength,
	}
	key, err := scrypt.Key([]byte(cipherText), salt, 32768, 8, 1, aegisKeyLength)
	if err != nil {
		return nil, err
	}
	verification, err := aegisEncryptText(aegisVerificationPlaintext, key)
	if err != nil {
		return nil, err
	}
	if err := s.migrateLegacyCredentials(ctx, tx, key); err != nil {
		return nil, err
	}
	if err := s.migrateLegacyGithubToken(ctx, tx, key); err != nil {
		return nil, err
	}
	if err := s.migrateLegacyOperatorAuth(ctx, tx, key); err != nil {
		return nil, err
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO engine.aegis_cipher_state(id, kdf_name, kdf_params_json, verification_token, created_at, updated_at)
		VALUES (1, $1, $2, $3, $4, $4)
	`, aegisKDFName, string(paramsJSON), verification, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	s.setActiveKey(key)
	return s.status(ctx)
}

func (s *goAegisService) rotateWithCipher(ctx context.Context, currentCipher string, newCipher string) (map[string]any, error) {
	if strings.TrimSpace(currentCipher) == "" || strings.TrimSpace(newCipher) == "" {
		return nil, fmt.Errorf("%w: current_cipher and new_cipher are required", errAegisInvalidRequest)
	}
	if s == nil || s.db == nil {
		return nil, errors.New("aegis store unavailable")
	}
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
	state, err := s.stateFromTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	if !state.Configured {
		return nil, errAegisNotConfigured
	}
	oldKey, err := s.deriveKeyFromState(currentCipher, state)
	if err != nil {
		return nil, err
	}
	plain, err := aegisDecryptText(state.VerificationToken, oldKey)
	if err != nil || plain != aegisVerificationPlaintext {
		return nil, errAegisInvalidCipher
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	params := map[string]any{
		"salt_b64": base64.StdEncoding.EncodeToString(salt),
		"n":        32768,
		"r":        8,
		"p":        1,
		"length":   aegisKeyLength,
	}
	newKey, err := scrypt.Key([]byte(newCipher), salt, 32768, 8, 1, aegisKeyLength)
	if err != nil {
		return nil, err
	}
	if err := s.reencryptCredentials(ctx, tx, oldKey, newKey); err != nil {
		return nil, err
	}
	if err := s.reencryptGithubToken(ctx, tx, oldKey, newKey); err != nil {
		return nil, err
	}
	if err := s.reencryptOperatorAuth(ctx, tx, oldKey, newKey); err != nil {
		return nil, err
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	verification, err := aegisEncryptText(aegisVerificationPlaintext, newKey)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	if _, err := tx.ExecContext(ctx, `
		UPDATE engine.aegis_cipher_state
		   SET kdf_name=$1,
		       kdf_params_json=$2,
		       verification_token=$3,
		       updated_at=$4
		 WHERE id=1
	`, aegisKDFName, string(paramsJSON), verification, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	s.setActiveKey(newKey)
	return s.status(ctx)
}

func (s *goAegisService) forceReset(ctx context.Context) (map[string]any, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("aegis store unavailable")
	}
	state, err := s.state(ctx)
	if err != nil {
		return nil, err
	}
	if !state.Configured {
		return nil, errAegisNotConfigured
	}
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
	now := time.Now().Unix()
	affectedCredentialIDs, err := s.resetCredentialSecrets(ctx, tx, now)
	if err != nil {
		return nil, err
	}
	githubReset, err := s.resetGithubToken(ctx, tx, now)
	if err != nil {
		return nil, err
	}
	var affectedUsers int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM engine.users`).Scan(&affectedUsers); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE engine.users
		   SET password_sha512='',
		       mfa_secret=NULL,
		       mfa_enabled=0,
		       mfa_disabled=0,
		       auth_reset_required=1,
		       auth_reset_at=$1,
		       updated_at=$1
	`, now); err != nil {
		return nil, err
	}
	var removedPasskeys int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM engine.user_passkeys`).Scan(&removedPasskeys); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM engine.user_passkeys`); err != nil {
		return nil, err
	}
	disabledJobs, err := disableJobsForCredentials(ctx, tx, now, affectedCredentialIDs)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM engine.aegis_cipher_state WHERE id=1`); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	s.clearActiveKey()
	payload, err := s.status(ctx)
	if err != nil {
		return nil, err
	}
	payload["force_reset"] = true
	payload["affected_credentials"] = int64(len(affectedCredentialIDs))
	payload["disabled_jobs"] = disabledJobs
	payload["github_token_reset"] = githubReset
	payload["affected_users"] = affectedUsers
	payload["removed_passkeys"] = removedPasskeys
	return payload, nil
}

func (s *goAegisService) stateFromTx(ctx context.Context, tx *sql.Tx) (aegisState, error) {
	var state aegisState
	err := tx.QueryRowContext(ctx, `
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

func (s *goAegisService) deriveKeyFromState(cipherText string, state aegisState) ([]byte, error) {
	params, err := parseAegisKDFParams(state)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errAegisDataCorruption, err)
	}
	key, err := scrypt.Key([]byte(cipherText), params.salt, params.n, params.r, params.p, params.length)
	if err != nil {
		return nil, err
	}
	return key, nil
}

func (s *goAegisService) setActiveKey(key []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.key = append([]byte(nil), key...)
}

func (s *goAegisService) clearActiveKey() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.key = nil
}

func (s *goAegisService) migrateLegacyCredentials(ctx context.Context, tx *sql.Tx, key []byte) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, password_encrypted, private_key_encrypted, private_key_passphrase_encrypted, become_password_encrypted
		  FROM engine.credentials
		 ORDER BY id ASC
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		values := make([][]byte, 4)
		if err := rows.Scan(&id, &values[0], &values[1], &values[2], &values[3]); err != nil {
			return err
		}
		plaintexts := make([]*string, 4)
		for i, value := range values {
			plain, err := legacySecretOrNil(value)
			if err != nil {
				return err
			}
			plaintexts[i] = plain
		}
		encrypted := make([]any, 4)
		for i, plain := range plaintexts {
			value, err := encryptOptionalBlob(plain, key)
			if err != nil {
				return err
			}
			encrypted[i] = value
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE engine.credentials
			   SET password_encrypted=$1,
			       private_key_encrypted=$2,
			       private_key_passphrase_encrypted=$3,
			       become_password_encrypted=$4
			 WHERE id=$5
		`, encrypted[0], encrypted[1], encrypted[2], encrypted[3], id); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *goAegisService) reencryptCredentials(ctx context.Context, tx *sql.Tx, oldKey []byte, newKey []byte) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, password_encrypted, private_key_encrypted, private_key_passphrase_encrypted, become_password_encrypted
		  FROM engine.credentials
		 ORDER BY id ASC
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		values := make([][]byte, 4)
		if err := rows.Scan(&id, &values[0], &values[1], &values[2], &values[3]); err != nil {
			return err
		}
		encrypted := make([]any, 4)
		for i, value := range values {
			plain, err := decryptOptional(value, oldKey)
			if err != nil {
				return err
			}
			next, err := encryptOptionalBlob(plain, newKey)
			if err != nil {
				return err
			}
			encrypted[i] = next
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE engine.credentials
			   SET password_encrypted=$1,
			       private_key_encrypted=$2,
			       private_key_passphrase_encrypted=$3,
			       become_password_encrypted=$4
			 WHERE id=$5
		`, encrypted[0], encrypted[1], encrypted[2], encrypted[3], id); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *goAegisService) migrateLegacyGithubToken(ctx context.Context, tx *sql.Tx, key []byte) error {
	var token []byte
	var resetRequired sql.NullInt64
	var resetAt sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT token, reset_required, reset_at FROM engine.github_token LIMIT 1`).Scan(&token, &resetRequired, &resetAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	plain, err := legacySecretOrNil(token)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM engine.github_token`); err != nil {
		return err
	}
	if plain != nil {
		encrypted, err := aegisEncryptText(*plain, key)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO engine.github_token(token, reset_required, reset_at) VALUES ($1, 0, NULL)`, encrypted)
		return err
	}
	if resetRequired.Valid && resetRequired.Int64 != 0 {
		var at any
		if resetAt.Valid {
			at = resetAt.Int64
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO engine.github_token(token, reset_required, reset_at) VALUES (NULL, 1, $1)`, at)
		return err
	}
	return nil
}

func (s *goAegisService) reencryptGithubToken(ctx context.Context, tx *sql.Tx, oldKey []byte, newKey []byte) error {
	var token []byte
	var resetRequired sql.NullInt64
	var resetAt sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT token, reset_required, reset_at FROM engine.github_token LIMIT 1`).Scan(&token, &resetRequired, &resetAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	plain, err := decryptOptional(token, oldKey)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM engine.github_token`); err != nil {
		return err
	}
	if plain != nil {
		encrypted, err := aegisEncryptText(*plain, newKey)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO engine.github_token(token, reset_required, reset_at) VALUES ($1, 0, NULL)`, encrypted)
		return err
	}
	if resetRequired.Valid && resetRequired.Int64 != 0 {
		var at any
		if resetAt.Valid {
			at = resetAt.Int64
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO engine.github_token(token, reset_required, reset_at) VALUES (NULL, 1, $1)`, at)
		return err
	}
	return nil
}

func (s *goAegisService) migrateLegacyOperatorAuth(ctx context.Context, tx *sql.Tx, key []byte) error {
	if err := s.migrateLegacyUserSecrets(ctx, tx, key); err != nil {
		return err
	}
	return s.migrateLegacyPasskeys(ctx, tx, key)
}

func (s *goAegisService) migrateLegacyUserSecrets(ctx context.Context, tx *sql.Tx, key []byte) error {
	rows, err := tx.QueryContext(ctx, `SELECT id, password_sha512, mfa_secret FROM engine.users ORDER BY id ASC`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var password sql.NullString
		var mfa sql.NullString
		if err := rows.Scan(&id, &password, &mfa); err != nil {
			return err
		}
		nextPassword := password.String
		nextMFA := mfa
		changed := false
		if password.Valid && strings.TrimSpace(password.String) != "" && !strings.HasPrefix(password.String, aegisEnvelopePrefix) {
			encrypted, err := aegisEncryptText(password.String, key)
			if err != nil {
				return err
			}
			nextPassword = encrypted
			changed = true
		}
		if mfa.Valid && strings.TrimSpace(mfa.String) != "" && !strings.HasPrefix(mfa.String, aegisEnvelopePrefix) {
			encrypted, err := aegisEncryptText(mfa.String, key)
			if err != nil {
				return err
			}
			nextMFA = sql.NullString{String: encrypted, Valid: true}
			changed = true
		}
		if changed {
			var mfaValue any
			if nextMFA.Valid {
				mfaValue = nextMFA.String
			}
			if _, err := tx.ExecContext(ctx, `UPDATE engine.users SET password_sha512=$1, mfa_secret=$2 WHERE id=$3`, nextPassword, mfaValue, id); err != nil {
				return err
			}
		}
	}
	return rows.Err()
}

func (s *goAegisService) migrateLegacyPasskeys(ctx context.Context, tx *sql.Tx, key []byte) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, credential_id, public_key, sign_count, aaguid, secret_encrypted
		  FROM engine.user_passkeys
		 ORDER BY id ASC
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var credentialID any
		var publicKey any
		var signCount sql.NullInt64
		var aaguid sql.NullString
		var encryptedSecret sql.NullString
		if err := rows.Scan(&id, &credentialID, &publicKey, &signCount, &aaguid, &encryptedSecret); err != nil {
			return err
		}
		if encryptedSecret.Valid && strings.TrimSpace(encryptedSecret.String) != "" {
			if !strings.HasPrefix(encryptedSecret.String, aegisEnvelopePrefix) {
				return fmt.Errorf("%w: protected secret is not stored as an Aegis envelope", errAegisDataCorruption)
			}
			continue
		}
		credential := normalizeWebAuthnStorageValue(credentialID)
		pub := normalizeWebAuthnStorageValue(publicKey)
		if credential == "" || pub == "" {
			return fmt.Errorf("%w: stored passkey record is incomplete", errAegisDataCorruption)
		}
		bundle, err := passkeySecretBundle(credential, pub, signCount.Int64, aaguid.String)
		if err != nil {
			return err
		}
		secret, err := aegisEncryptText(bundle, key)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE engine.user_passkeys
			   SET credential_id='',
			       public_key='',
			       sign_count=0,
			       aaguid='',
			       credential_lookup_hmac=$1,
			       secret_encrypted=$2
			 WHERE id=$3
		`, s.passkeyLookupHMAC(credential), secret, id); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *goAegisService) reencryptOperatorAuth(ctx context.Context, tx *sql.Tx, oldKey []byte, newKey []byte) error {
	rows, err := tx.QueryContext(ctx, `SELECT id, password_sha512, mfa_secret FROM engine.users ORDER BY id ASC`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var password []byte
		var mfa []byte
		if err := rows.Scan(&id, &password, &mfa); err != nil {
			return err
		}
		passwordPlain, err := decryptOptional(password, oldKey)
		if err != nil {
			return err
		}
		mfaPlain, err := decryptOptional(mfa, oldKey)
		if err != nil {
			return err
		}
		nextPassword, err := encryptOptionalText(passwordPlain, newKey, true)
		if err != nil {
			return err
		}
		nextMFA, err := encryptOptionalText(mfaPlain, newKey, false)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE engine.users
			   SET password_sha512=$1,
			       mfa_secret=$2
			 WHERE id=$3
		`, nextPassword, nextMFA, id); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return s.reencryptPasskeys(ctx, tx, oldKey, newKey)
}

func (s *goAegisService) reencryptPasskeys(ctx context.Context, tx *sql.Tx, oldKey []byte, newKey []byte) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, credential_id, public_key, sign_count, aaguid, secret_encrypted
		  FROM engine.user_passkeys
		 ORDER BY id ASC
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var credentialID any
		var publicKey any
		var signCount sql.NullInt64
		var aaguid sql.NullString
		var encryptedSecret []byte
		if err := rows.Scan(&id, &credentialID, &publicKey, &signCount, &aaguid, &encryptedSecret); err != nil {
			return err
		}
		var bundle string
		if len(encryptedSecret) > 0 {
			plain, err := decryptOptional(encryptedSecret, oldKey)
			if err != nil {
				return err
			}
			if plain == nil {
				return fmt.Errorf("%w: stored passkey record is incomplete", errAegisDataCorruption)
			}
			bundle = *plain
		} else {
			credential := normalizeWebAuthnStorageValue(credentialID)
			pub := normalizeWebAuthnStorageValue(publicKey)
			if credential == "" || pub == "" {
				return fmt.Errorf("%w: stored passkey record is incomplete", errAegisDataCorruption)
			}
			var err error
			bundle, err = passkeySecretBundle(credential, pub, signCount.Int64, aaguid.String)
			if err != nil {
				return err
			}
		}
		parsed := parsePasskeySecretBundle(bundle)
		credential := cleanText(parsed["credential_id"])
		if credential == "" {
			return fmt.Errorf("%w: stored passkey record is incomplete", errAegisDataCorruption)
		}
		secret, err := aegisEncryptText(bundle, newKey)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE engine.user_passkeys
			   SET credential_id='',
			       public_key='',
			       sign_count=0,
			       aaguid='',
			       credential_lookup_hmac=$1,
			       secret_encrypted=$2
			 WHERE id=$3
		`, s.passkeyLookupHMAC(credential), secret, id); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *goAegisService) resetCredentialSecrets(ctx context.Context, tx *sql.Tx, now int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, password_encrypted, private_key_encrypted, private_key_passphrase_encrypted, become_password_encrypted, metadata_json
		  FROM engine.credentials
		 ORDER BY id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		values := make([][]byte, 4)
		var metadataRaw sql.NullString
		if err := rows.Scan(&id, &values[0], &values[1], &values[2], &values[3], &metadataRaw); err != nil {
			return nil, err
		}
		fields := make([]string, 0, 4)
		for index, value := range values {
			if len(value) == 0 {
				continue
			}
			if !strings.HasPrefix(string(value), aegisEnvelopePrefix) {
				return nil, fmt.Errorf("%w: protected secret is not stored as an Aegis envelope", errAegisDataCorruption)
			}
			fields = append(fields, []string{"password", "private_key", "private_key_passphrase", "become_password"}[index])
		}
		if len(fields) == 0 {
			continue
		}
		metadata := parseJSONObject(metadataRaw)
		metadata["aegis_secret_state"] = "reset_required"
		metadata["aegis_lost_secret_fields"] = aegisStringSliceToAny(fields)
		metadata["aegis_reset_at"] = now
		metadataJSON, err := json.Marshal(metadata)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE engine.credentials
			   SET password_encrypted=NULL,
			       private_key_encrypted=NULL,
			       private_key_passphrase_encrypted=NULL,
			       become_password_encrypted=NULL,
			       metadata_json=$1,
			       updated_at=$2
			 WHERE id=$3
		`, string(metadataJSON), now, id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *goAegisService) resetGithubToken(ctx context.Context, tx *sql.Tx, now int64) (bool, error) {
	var token []byte
	err := tx.QueryRowContext(ctx, `SELECT token FROM engine.github_token LIMIT 1`).Scan(&token)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	reset := len(token) > 0
	if reset && !strings.HasPrefix(string(token), aegisEnvelopePrefix) {
		return false, fmt.Errorf("%w: protected secret is not stored as an Aegis envelope", errAegisDataCorruption)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM engine.github_token`); err != nil {
		return false, err
	}
	if reset {
		if _, err := tx.ExecContext(ctx, `INSERT INTO engine.github_token(token, reset_required, reset_at) VALUES (NULL, 1, $1)`, now); err != nil {
			return false, err
		}
	}
	return reset, nil
}

func disableJobsForCredentials(ctx context.Context, tx *sql.Tx, now int64, ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	args := make([]any, 0, len(ids)+1)
	args = append(args, now)
	placeholders := make([]string, 0, len(ids))
	for i, id := range ids {
		args = append(args, id)
		placeholders = append(placeholders, "$"+strconv.Itoa(i+2))
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE engine.scheduled_jobs
		   SET enabled=0,
		       updated_at=$1
		 WHERE credential_id IN (`+strings.Join(placeholders, ",")+`)
		   AND COALESCE(enabled, 0)<>0
	`, args...)
	if err != nil {
		return 0, err
	}
	count, _ := result.RowsAffected()
	return count, nil
}

func legacySecretOrNil(value any) (*string, error) {
	text, err := aegisDBText(value)
	if err != nil {
		return nil, err
	}
	if text == "" {
		return nil, nil
	}
	if strings.HasPrefix(text, aegisEnvelopePrefix) {
		return nil, fmt.Errorf("%w: protected secret is already Aegis-encrypted before setup", errAegisDataCorruption)
	}
	return &text, nil
}

func decryptOptional(value any, key []byte) (*string, error) {
	text, err := aegisDBText(value)
	if err != nil {
		return nil, err
	}
	if text == "" {
		return nil, nil
	}
	if !strings.HasPrefix(text, aegisEnvelopePrefix) {
		return nil, fmt.Errorf("%w: protected secret is not stored as an Aegis envelope", errAegisDataCorruption)
	}
	plain, err := aegisDecryptText(text, key)
	if err != nil {
		return nil, fmt.Errorf("%w: protected secret could not be decrypted", errAegisDataCorruption)
	}
	return &plain, nil
}

func encryptOptionalBlob(value *string, key []byte) (any, error) {
	if value == nil {
		return nil, nil
	}
	encrypted, err := aegisEncryptText(*value, key)
	if err != nil {
		return nil, err
	}
	return []byte(encrypted), nil
}

func encryptOptionalText(value *string, key []byte, emptyFallback bool) (any, error) {
	if value == nil {
		if emptyFallback {
			return "", nil
		}
		return nil, nil
	}
	encrypted, err := aegisEncryptText(*value, key)
	if err != nil {
		return nil, err
	}
	return encrypted, nil
}

func (s *goAegisService) passkeyLookupHMAC(credentialID string) string {
	if strings.TrimSpace(credentialID) == "" || len(s.hmacSecret) == 0 {
		return ""
	}
	mac := hmac.New(sha256.New, s.hmacSecret)
	_, _ = mac.Write([]byte(credentialID))
	return fmt.Sprintf("%x", mac.Sum(nil))
}

func normalizeWebAuthnStorageValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case []byte:
		if len(typed) == 0 {
			return ""
		}
		return base64.RawURLEncoding.EncodeToString(typed)
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return ""
		}
		if strings.HasPrefix(text, "b'") || strings.HasPrefix(text, "b\"") {
			if unquoted, err := strconv.Unquote(text[1:]); err == nil {
				return base64.RawURLEncoding.EncodeToString([]byte(unquoted))
			}
		}
		return text
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func passkeySecretBundle(credentialID string, publicKey string, signCount int64, aaguid string) (string, error) {
	payload := map[string]any{
		"aaguid":        strings.TrimSpace(aaguid),
		"credential_id": normalizeWebAuthnStorageValue(credentialID),
		"public_key":    normalizeWebAuthnStorageValue(publicKey),
		"sign_count":    signCount,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func parsePasskeySecretBundle(raw string) map[string]any {
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil || payload == nil {
		return map[string]any{}
	}
	payload["credential_id"] = normalizeWebAuthnStorageValue(payload["credential_id"])
	payload["public_key"] = normalizeWebAuthnStorageValue(payload["public_key"])
	payload["aaguid"] = cleanText(payload["aaguid"])
	payload["sign_count"] = parseInt64Any(payload["sign_count"])
	return payload
}

func aegisStringSliceToAny(values []string) []any {
	result := make([]any, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

func aegisErrorPayload(err error) (map[string]any, int, error) {
	payload, status := aegisErrorBody(err)
	return payload, status, nil
}

func aegisErrorBody(err error) (map[string]any, int) {
	message := ""
	if err != nil {
		message = err.Error()
	}
	switch {
	case errors.Is(err, errAegisInvalidCipher):
		return map[string]any{"error": "invalid_cipher", "message": message}, http.StatusUnauthorized
	case errors.Is(err, errAegisAlreadyConfigured):
		return map[string]any{"error": "already_configured", "message": message}, http.StatusConflict
	case errors.Is(err, errAegisNotConfigured):
		return map[string]any{"error": "not_configured", "message": message}, http.StatusConflict
	case errors.Is(err, errAegisLocked):
		return map[string]any{"error": "locked", "message": message}, http.StatusLocked
	case errors.Is(err, errAegisDataCorruption):
		return map[string]any{"error": "corrupt_secret_store", "message": message}, http.StatusInternalServerError
	default:
		return map[string]any{"error": "invalid_request", "message": message}, http.StatusBadRequest
	}
}
