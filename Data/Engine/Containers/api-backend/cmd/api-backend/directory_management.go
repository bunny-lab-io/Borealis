package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	directoryPasswordPlaceholder = "__directory_auth__"
	directoryAuthTimeout         = 15 * time.Second
)

type directoryProviderConfig struct {
	Row           directoryProviderRow
	GroupMappings []map[string]any
	SiteMappings  []map[string]any
}

type directoryAuthError struct {
	Code       string
	Message    string
	StatusCode int
}

type directoryLoginResult struct {
	Username     string
	DisplayName  string
	Role         string
	ProviderID   int64
	ProviderName string
	Domain       string
	DN           string
	Subject      string
	Groups       []string
}

type directoryUserInfo struct {
	DN          string
	Account     string
	DisplayName string
	Subject     string
	Groups      []string
	Attrs       map[string][]string
}

func (e directoryAuthError) Error() string {
	return e.Message
}

func newDirectoryError(code string, message string, status int) error {
	if status == 0 {
		status = http.StatusInternalServerError
	}
	return directoryAuthError{Code: code, Message: message, StatusCode: status}
}

func writeDirectoryError(w http.ResponseWriter, err error) {
	var dirErr directoryAuthError
	if errors.As(err, &dirErr) {
		writeJSON(w, dirErr.StatusCode, map[string]any{"error": dirErr.Code, "message": dirErr.Message})
		return
	}
	if errors.Is(err, errAegisNotConfigured) || errors.Is(err, errAegisLocked) || errors.Is(err, errAegisDataCorruption) {
		writeJSON(w, protectedSecretErrorStatus(err), protectedSecretErrorBody(err))
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]any{"error": firstText(err.Error(), "directory_failed")})
}

func directoryProviderSubtreeHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		providerID, action, ok := parseDirectoryProviderSubtree(r.URL.Path)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
			return
		}
		switch {
		case action == "" && r.Method == http.MethodPatch:
			directoryProviderSave(w, r, auth, providerID)
		case action == "" && r.Method == http.MethodDelete:
			directoryProviderDelete(w, r, auth, providerID)
		case action == "test" && r.Method == http.MethodPost:
			directoryProviderTest(w, r, auth, providerID)
		case action == "sync" && r.Method == http.MethodPost:
			directoryProviderSync(w, r, auth, providerID)
		case action == "lookup-user" && r.Method == http.MethodPost:
			directoryProviderLookupUser(w, r, auth, providerID)
		case action == "effective-access" && r.Method == http.MethodPost:
			directoryProviderEffectiveAccess(w, r, auth, providerID)
		default:
			writeMethodNotAllowed(w, http.MethodPatch+", "+http.MethodDelete+", "+http.MethodPost)
		}
	}
}

func parseDirectoryProviderSubtree(path string) (int64, string, bool) {
	rest := strings.Trim(strings.TrimPrefix(path, "/api/directory/providers/"), "/")
	if rest == "" {
		return 0, "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) > 2 {
		return 0, "", false
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		return 0, "", false
	}
	action := ""
	if len(parts) == 2 {
		action = strings.TrimSpace(parts[1])
	}
	return id, action, true
}

func directoryProviderSave(w http.ResponseWriter, r *http.Request, auth *authService, providerID int64) {
	if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
		failure.write(w)
		return
	}
	payload, ok := readAuthJSON(w, r)
	if !ok {
		return
	}
	store, ok := auth.store.(*postgresOperatorStore)
	if !ok {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "directory_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), directoryAuthTimeout)
	defer cancel()
	provider, err := store.saveDirectoryProvider(ctx, auth.aegis, providerID, payload)
	if err != nil {
		writeDirectoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "provider": provider})
}

func directoryProviderDelete(w http.ResponseWriter, r *http.Request, auth *authService, providerID int64) {
	if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
		failure.write(w)
		return
	}
	store, ok := auth.store.(*postgresOperatorStore)
	if !ok {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "directory_unavailable"})
		return
	}
	ctx, cancel := requestTimeout(r.Context(), auth)
	defer cancel()
	if err := store.deleteDirectoryProvider(ctx, providerID); err != nil {
		writeDirectoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func directoryProviderTest(w http.ResponseWriter, r *http.Request, auth *authService, providerID int64) {
	if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
		failure.write(w)
		return
	}
	store, ok := auth.store.(*postgresOperatorStore)
	if !ok || auth.aegis == nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "directory_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), directoryAuthTimeout)
	defer cancel()
	provider, found, err := store.loadDirectoryProvider(ctx, providerID)
	if err != nil {
		writeDirectoryError(w, err)
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "provider_not_found"})
		return
	}
	okResult, message := directoryTestProvider(ctx, auth.aegis, provider)
	statusText := "failed"
	statusCode := http.StatusBadGateway
	if okResult {
		statusText = "ok"
		statusCode = http.StatusOK
	}
	if err := store.updateDirectoryProviderTest(ctx, providerID, statusText, message); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, statusCode, map[string]any{"status": statusText, "ok": okResult, "message": message})
}

func directoryProviderSync(w http.ResponseWriter, r *http.Request, auth *authService, providerID int64) {
	if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
		failure.write(w)
		return
	}
	store, ok := auth.store.(*postgresOperatorStore)
	if !ok || auth.aegis == nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "directory_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), directoryAuthTimeout)
	defer cancel()
	disabled, message, err := store.syncDirectoryProvider(ctx, auth.aegis, providerID)
	if err != nil {
		_ = store.updateDirectoryProviderSync(ctx, providerID, "failed", firstText(err.Error(), "Directory sync failed."))
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "sync_failed", "message": firstText(err.Error(), "Directory sync failed.")})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "disabled_users": disabled, "message": message})
}

func directoryProviderLookupUser(w http.ResponseWriter, r *http.Request, auth *authService, providerID int64) {
	if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
		failure.write(w)
		return
	}
	payload, ok := readAuthJSON(w, r)
	if !ok {
		return
	}
	username := cleanText(payload["username"])
	password := cleanText(payload["password"])
	if username == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "username_required", "message": "Username is required."})
		return
	}
	store, ok := auth.store.(*postgresOperatorStore)
	if !ok || auth.aegis == nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "directory_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), directoryAuthTimeout)
	defer cancel()
	provider, found, err := store.loadDirectoryProvider(ctx, providerID)
	if err != nil {
		writeDirectoryError(w, err)
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "provider_not_found"})
		return
	}
	userInfo, okFound, err := directorySearchUser(ctx, auth.aegis, provider, username)
	if err != nil {
		writeDirectoryError(w, err)
		return
	}
	if !okFound {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "directory_user_not_found", "message": "Directory user was not found."})
		return
	}
	role := directoryMappedRole(provider, userInfo.Groups)
	result := map[string]any{
		"status":           "ok",
		"found":            true,
		"provider_id":      providerID,
		"provider_type":    provider.providerType(),
		"login":            username,
		"account":          userInfo.Account,
		"display_name":     userInfo.DisplayName,
		"dn":               userInfo.DN,
		"subject":          userInfo.Subject,
		"groups":           userInfo.Groups,
		"group_count":      len(userInfo.Groups),
		"mapped_role":      firstText(role, ""),
		"allowed":          role != "",
		"password_checked": password != "",
		"password_ok":      nil,
	}
	if password != "" {
		if err := directoryVerifyPassword(ctx, auth.aegis, provider, userInfo, username, password); err != nil {
			result["password_ok"] = false
			var dirErr directoryAuthError
			if errors.As(err, &dirErr) {
				result["password_error"] = dirErr.Code
				result["password_message"] = dirErr.Message
			} else {
				result["password_error"] = "password_check_failed"
				result["password_message"] = err.Error()
			}
		} else {
			result["password_ok"] = true
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func directoryProviderEffectiveAccess(w http.ResponseWriter, r *http.Request, auth *authService, providerID int64) {
	if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
		failure.write(w)
		return
	}
	payload, ok := readAuthJSON(w, r)
	if !ok {
		return
	}
	groupDNS := directoryStringSliceFromAny(payload["group_dns"])
	if len(groupDNS) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "group_dns_required", "message": "At least one group DN is required."})
		return
	}
	store, ok := auth.store.(*postgresOperatorStore)
	if !ok || auth.aegis == nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "directory_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), directoryAuthTimeout)
	defer cancel()
	provider, found, err := store.loadDirectoryProvider(ctx, providerID)
	if err != nil {
		writeDirectoryError(w, err)
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "provider_not_found"})
		return
	}
	users, err := directoryPreviewGroupAccess(ctx, auth.aegis, provider, groupDNS)
	if err != nil {
		writeDirectoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"provider_id": providerID,
		"role":        canonicalDirectoryRole(cleanText(payload["role"])),
		"group_dns":   groupDNS,
		"users":       users,
		"user_count":  len(users),
	})
}

func directoryCertificateHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		payload, ok := readAuthJSON(w, r)
		if !ok {
			return
		}
		rawURLs := payload["server_url"]
		if _, exists := payload["server_urls"]; exists {
			rawURLs = payload["server_urls"]
		}
		urls := splitDirectoryServerURLs(rawURLs, truthyPayloadDefault(payload["use_ldaps"], true))
		hostOverrides := splitDirectoryHostOverrides(payload["host_overrides"])
		if len(urls) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing_server", "message": "LDAP server URL is required."})
			return
		}
		var lastErr error
		for _, serverURL := range urls {
			certificate, err := fetchLDAPSDirectoryCertificate(serverURL, hostOverrides)
			if err == nil {
				writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "certificate": certificate})
				return
			}
			lastErr = err
			var dirErr directoryAuthError
			if errors.As(err, &dirErr) && dirErr.StatusCode == http.StatusBadRequest {
				break
			}
		}
		writeDirectoryError(w, firstErr(lastErr, newDirectoryError("certificate_download_failed", "Certificate download failed.", http.StatusBadGateway)))
	}
}

func directoryUserCacheHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		profile, failure := requireAdmin(r.Context(), auth, r)
		if failure != nil {
			failure.write(w)
			return
		}
		username := cleanText(r.PathValue("username"))
		if strings.EqualFold(profile.Username, username) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "cannot_disable_self"})
			return
		}
		payload, ok := readAuthJSON(w, r)
		if !ok {
			return
		}
		disabled := truthyPayloadDefault(payload["disabled"], true)
		store, ok := auth.store.(*postgresOperatorStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "directory_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		if err := store.setDirectoryCacheDisabled(ctx, username, disabled); err != nil {
			writeDirectoryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "username": username, "directory_disabled": disabled})
	}
}

func firstErr(err error, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}

func (s *postgresOperatorStore) saveDirectoryProvider(ctx context.Context, secret authSecretService, providerID int64, payload map[string]any) (map[string]any, error) {
	var existing directoryProviderConfig
	var found bool
	var err error
	if providerID > 0 {
		existing, found, err = s.loadDirectoryProvider(ctx, providerID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, newDirectoryError("provider_not_found", "Provider was not found.", http.StatusNotFound)
		}
	}
	values, siteMappings, err := directoryProviderMutationValues(ctx, secret, payload, existing)
	if err != nil {
		return nil, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
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
	if providerID > 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE engine.directory_providers
			   SET name=$1,
			       provider_type=$2,
			       enabled=$3,
			       priority=$4,
			       domain_suffix=$5,
			       server_urls_json=$6,
			       host_overrides_json=$7,
			       use_ldaps=$8,
			       tls_required=$9,
			       tls_ca_pem=$10,
			       base_dn=$11,
			       bind_dn=$12,
			       bind_password_encrypted=$13,
			       user_search_filter=$14,
			       username_attribute=$15,
			       display_name_attribute=$16,
			       email_attribute=$17,
			       member_of_attribute=$18,
			       group_search_base_dn=$19,
			       nested_groups=$20,
			       kerberos_realm=$21,
			       kerberos_kdc=$22,
			       kerberos_keytab_encrypted=$23,
			       sync_interval_seconds=$24,
			       updated_at=$25
			 WHERE id=$26
		`, values["name"], values["provider_type"], values["enabled"], values["priority"], values["domain_suffix"], values["server_urls_json"], values["host_overrides_json"], values["use_ldaps"], values["tls_required"], values["tls_ca_pem"], values["base_dn"], values["bind_dn"], values["bind_password_encrypted"], values["user_search_filter"], values["username_attribute"], values["display_name_attribute"], values["email_attribute"], values["member_of_attribute"], values["group_search_base_dn"], values["nested_groups"], values["kerberos_realm"], values["kerberos_kdc"], values["kerberos_keytab_encrypted"], values["sync_interval_seconds"], now, providerID); err != nil {
			return nil, directoryPersistError(err)
		}
	} else {
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO engine.directory_providers(
				name, provider_type, enabled, priority, domain_suffix,
				server_urls_json, host_overrides_json, use_ldaps, tls_required,
				tls_ca_pem, base_dn, bind_dn, bind_password_encrypted,
				user_search_filter, username_attribute, display_name_attribute,
				email_attribute, member_of_attribute, group_search_base_dn,
				nested_groups, kerberos_realm, kerberos_kdc,
				kerberos_keytab_encrypted, sync_interval_seconds, created_at, updated_at
			) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26)
			RETURNING id
		`, values["name"], values["provider_type"], values["enabled"], values["priority"], values["domain_suffix"], values["server_urls_json"], values["host_overrides_json"], values["use_ldaps"], values["tls_required"], values["tls_ca_pem"], values["base_dn"], values["bind_dn"], values["bind_password_encrypted"], values["user_search_filter"], values["username_attribute"], values["display_name_attribute"], values["email_attribute"], values["member_of_attribute"], values["group_search_base_dn"], values["nested_groups"], values["kerberos_realm"], values["kerberos_kdc"], values["kerberos_keytab_encrypted"], values["sync_interval_seconds"], now, now).Scan(&providerID); err != nil {
			return nil, directoryPersistError(err)
		}
	}
	if _, ok := payload["group_mappings"]; ok {
		if _, err := tx.ExecContext(ctx, `DELETE FROM engine.directory_provider_group_mappings WHERE provider_id=$1`, providerID); err != nil {
			return nil, err
		}
		for _, item := range mappingSlice(payload["group_mappings"]) {
			groupDN := cleanText(item["group_dn"])
			if groupDN == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO engine.directory_provider_group_mappings(provider_id, group_dn, role, created_at, updated_at)
				VALUES($1,$2,$3,$4,$5)
			`, providerID, groupDN, canonicalDirectoryRole(cleanText(item["role"])), now, now); err != nil {
				return nil, directoryPersistError(err)
			}
		}
	}
	if _, ok := payload["site_mappings"]; ok {
		if err := validateDirectorySiteIDs(ctx, tx, siteMappings); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM engine.directory_provider_site_mappings WHERE provider_id=$1`, providerID); err != nil {
			return nil, err
		}
		for position, item := range siteMappings {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO engine.directory_provider_site_mappings(provider_id, label, group_dns_json, site_ids_json, position, created_at, updated_at)
				VALUES($1,$2,$3,$4,$5,$6,$7)
			`, providerID, firstText(cleanText(item["label"]), fmt.Sprintf("User Site Group %d", position+1)), jsonTextList(item["group_dns"]), jsonIntListText(item["site_ids"]), position, now, now); err != nil {
				return nil, directoryPersistError(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	if _, ok := payload["site_mappings"]; ok {
		provider, found, err := s.loadDirectoryProvider(ctx, providerID)
		if err == nil && found {
			_, _ = s.refreshCachedDirectorySiteAssignments(ctx, provider)
		}
	}
	provider, found, err := s.loadDirectoryProvider(ctx, providerID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, newDirectoryError("provider_not_found", "Provider was not found after save.", http.StatusNotFound)
	}
	return provider.publicPayload(), nil
}

func directoryPersistError(err error) error {
	if isUniqueViolation(err) {
		return newDirectoryError("provider_name_exists", "Provider name already exists.", http.StatusConflict)
	}
	return err
}

func (s *postgresOperatorStore) deleteDirectoryProvider(ctx context.Context, providerID int64) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	var cachedUsers int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		  FROM engine.users
		 WHERE COALESCE(auth_source, 'local')=$1
		   AND COALESCE(directory_provider_id, 0)=$2
	`, directoryAuth, providerID).Scan(&cachedUsers); err != nil {
		return err
	}
	if cachedUsers > 0 {
		return newDirectoryError("provider_has_cached_users", "Cached directory users must be removed or disabled before deleting this provider.", http.StatusConflict)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM engine.directory_provider_site_mappings WHERE provider_id=$1`, providerID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM engine.directory_provider_group_mappings WHERE provider_id=$1`, providerID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM engine.directory_providers WHERE id=$1`, providerID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected <= 0 {
		return newDirectoryError("provider_not_found", "Provider was not found.", http.StatusNotFound)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *postgresOperatorStore) updateDirectoryProviderTest(ctx context.Context, providerID int64, status string, message string) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	now := time.Now().Unix()
	_, err = conn.ExecContext(ctx, `
		UPDATE engine.directory_providers
		   SET last_test_at=$1,
		       last_test_status=$2,
		       last_test_message=$3,
		       updated_at=$4
		 WHERE id=$5
	`, now, strings.TrimSpace(status), truncateText(message, 500), now, providerID)
	return err
}

func (s *postgresOperatorStore) updateDirectoryProviderSync(ctx context.Context, providerID int64, status string, message string) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	now := time.Now().Unix()
	_, err = conn.ExecContext(ctx, `
		UPDATE engine.directory_providers
		   SET last_sync_at=$1,
		       last_sync_status=$2,
		       last_sync_message=$3,
		       updated_at=$4
		 WHERE id=$5
	`, now, strings.TrimSpace(status), truncateText(message, 500), now, providerID)
	return err
}

func (s *postgresOperatorStore) setDirectoryCacheDisabled(ctx context.Context, username string, disabled bool) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return newDirectoryError("invalid_username", "Invalid username.", http.StatusBadRequest)
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	now := time.Now().Unix()
	var disabledAt any
	if disabled {
		disabledAt = now
	}
	result, err := conn.ExecContext(ctx, `
		UPDATE engine.users
		   SET directory_disabled=$1,
		       directory_disabled_at=$2,
		       updated_at=$3
		 WHERE LOWER(username)=LOWER($4)
		   AND COALESCE(auth_source, 'local')=$5
	`, boolInt64Value(disabled), disabledAt, now, username, directoryAuth)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected <= 0 {
		return newDirectoryError("directory_user_not_found", "Directory user not found.", http.StatusNotFound)
	}
	return nil
}

func (s *postgresOperatorStore) syncDirectoryProvider(ctx context.Context, secret authSecretService, providerID int64) (int, string, error) {
	provider, found, err := s.loadDirectoryProvider(ctx, providerID)
	if err != nil {
		return 0, "", err
	}
	if !found {
		return 0, "", newDirectoryError("provider_not_found", "Provider was not found.", http.StatusNotFound)
	}
	ok, message := directoryTestProvider(ctx, secret, provider)
	if !ok {
		return 0, "", newDirectoryError("provider_unreachable", message, http.StatusBadGateway)
	}
	cached, err := s.cachedDirectoryUsers(ctx, providerID)
	if err != nil {
		return 0, "", err
	}
	disabled := 0
	for _, user := range cached {
		username := cleanText(user["username"])
		foundUser, ok, err := directorySearchUser(ctx, secret, provider, username)
		if err != nil || !ok {
			_ = s.setDirectoryCacheDisabled(ctx, username, true)
			disabled++
			continue
		}
		role := directoryMappedRole(provider, foundUser.Groups)
		if role == "" {
			_ = s.setDirectoryCacheDisabled(ctx, username, true)
			disabled++
			continue
		}
		_, _ = s.upsertDirectoryUser(ctx, provider, foundUser, role)
	}
	message = fmt.Sprintf("Directory sync completed. Disabled %d cached user%s.", disabled, pluralSuffix(disabled))
	if err := s.updateDirectoryProviderSync(ctx, providerID, "ok", message); err != nil {
		return disabled, message, err
	}
	return disabled, message, nil
}

func (s *postgresOperatorStore) cachedDirectoryUsers(ctx context.Context, providerID int64) ([]map[string]any, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, `
		SELECT username, COALESCE(directory_dn, '')
		  FROM engine.users
		 WHERE COALESCE(auth_source, 'local')=$1
		   AND COALESCE(directory_provider_id, 0)=$2
		   AND COALESCE(directory_disabled, 0)=0
	`, directoryAuth, providerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []map[string]any{}
	for rows.Next() {
		var username, dn string
		if err := rows.Scan(&username, &dn); err != nil {
			return nil, err
		}
		users = append(users, map[string]any{"username": username, "directory_dn": dn})
	}
	return users, rows.Err()
}

func (s *postgresOperatorStore) loadDirectoryProvider(ctx context.Context, providerID int64) (directoryProviderConfig, bool, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return directoryProviderConfig{}, false, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	row, found, err := queryDirectoryProviderRow(ctx, conn, `WHERE id=$1`, providerID)
	if err != nil || !found {
		return directoryProviderConfig{}, found, err
	}
	groupMappings, err := loadDirectoryGroupMappings(ctx, conn, []int64{providerID})
	if err != nil {
		return directoryProviderConfig{}, false, err
	}
	siteMappings, err := loadDirectorySiteMappings(ctx, conn, []int64{providerID})
	if err != nil {
		return directoryProviderConfig{}, false, err
	}
	return directoryProviderConfig{
		Row:           row,
		GroupMappings: groupMappings[providerID],
		SiteMappings:  siteMappings[providerID],
	}, true, nil
}

func (s *postgresOperatorStore) loadEnabledDirectoryProviders(ctx context.Context) ([]directoryProviderConfig, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, `
		SELECT
			id, name, provider_type, enabled, priority, domain_suffix,
			server_urls_json, host_overrides_json, use_ldaps, tls_required,
			tls_ca_pem, base_dn, bind_dn, bind_password_encrypted,
			user_search_filter, username_attribute, display_name_attribute,
			email_attribute, member_of_attribute, group_search_base_dn,
			nested_groups, kerberos_realm, kerberos_kdc,
			kerberos_keytab_encrypted, sync_interval_seconds, last_sync_at,
			last_sync_status, last_sync_message, last_test_at, last_test_status,
			last_test_message, created_at, updated_at
		  FROM engine.directory_providers
		 WHERE COALESCE(enabled, 0)=1
		 ORDER BY priority ASC, LOWER(name) ASC
	`)
	if err != nil {
		return nil, err
	}
	providers := []directoryProviderRow{}
	ids := []int64{}
	for rows.Next() {
		row, err := scanDirectoryProviderRow(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		providers = append(providers, row)
		ids = append(ids, nullInt(row.ID))
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	groups, err := loadDirectoryGroupMappings(ctx, conn, ids)
	if err != nil {
		return nil, err
	}
	sites, err := loadDirectorySiteMappings(ctx, conn, ids)
	if err != nil {
		return nil, err
	}
	result := make([]directoryProviderConfig, 0, len(providers))
	for _, row := range providers {
		id := nullInt(row.ID)
		result = append(result, directoryProviderConfig{Row: row, GroupMappings: groups[id], SiteMappings: sites[id]})
	}
	return result, nil
}

func queryDirectoryProviderRow(ctx context.Context, conn *sql.Conn, where string, args ...any) (directoryProviderRow, bool, error) {
	query := `
		SELECT
			id, name, provider_type, enabled, priority, domain_suffix,
			server_urls_json, host_overrides_json, use_ldaps, tls_required,
			tls_ca_pem, base_dn, bind_dn, bind_password_encrypted,
			user_search_filter, username_attribute, display_name_attribute,
			email_attribute, member_of_attribute, group_search_base_dn,
			nested_groups, kerberos_realm, kerberos_kdc,
			kerberos_keytab_encrypted, sync_interval_seconds, last_sync_at,
			last_sync_status, last_sync_message, last_test_at, last_test_status,
			last_test_message, created_at, updated_at
		  FROM engine.directory_providers
		` + where + `
		 LIMIT 1`
	row, err := scanDirectoryProviderRow(conn.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return directoryProviderRow{}, false, nil
	}
	if err != nil {
		return directoryProviderRow{}, false, err
	}
	return row, true, nil
}

type directoryRowScanner interface {
	Scan(dest ...any) error
}

func scanDirectoryProviderRow(scanner directoryRowScanner) (directoryProviderRow, error) {
	var row directoryProviderRow
	err := scanner.Scan(
		&row.ID,
		&row.Name,
		&row.ProviderType,
		&row.Enabled,
		&row.Priority,
		&row.DomainSuffix,
		&row.ServerURLsJSON,
		&row.HostOverridesJSON,
		&row.UseLDAPS,
		&row.TLSRequired,
		&row.TLSCAPEM,
		&row.BaseDN,
		&row.BindDN,
		&row.BindPasswordEncrypted,
		&row.UserSearchFilter,
		&row.UsernameAttribute,
		&row.DisplayNameAttribute,
		&row.EmailAttribute,
		&row.MemberOfAttribute,
		&row.GroupSearchBaseDN,
		&row.NestedGroups,
		&row.KerberosRealm,
		&row.KerberosKDC,
		&row.KerberosKeytabEncrypted,
		&row.SyncIntervalSeconds,
		&row.LastSyncAt,
		&row.LastSyncStatus,
		&row.LastSyncMessage,
		&row.LastTestAt,
		&row.LastTestStatus,
		&row.LastTestMessage,
		&row.CreatedAt,
		&row.UpdatedAt,
	)
	return row, err
}

func directoryProviderMutationValues(ctx context.Context, secret authSecretService, payload map[string]any, existing directoryProviderConfig) (map[string]any, []map[string]any, error) {
	nowEnabled := existing.enabled()
	if value, ok := payload["enabled"]; ok {
		nowEnabled = truthyPayload(value)
	}
	if nowEnabled && strings.ToLower(existing.lastTestStatus()) != "ok" {
		return nil, nil, newDirectoryError("test_required", "Provider must pass connectivity test before it can be enabled.", http.StatusConflict)
	}
	name := fieldText(payload, existing.name(), "name")
	if name == "" {
		return nil, nil, newDirectoryError("name_required", "Provider name is required.", http.StatusBadRequest)
	}
	providerType := normalizeDirectoryProviderType(fieldText(payload, existing.providerType(), "provider_type"))
	priority := fieldInt(payload, directoryInt64Default(existing.Row.Priority, 100), "priority")
	serverURLs := stringSliceField(payload, jsonStringList(existing.Row.ServerURLsJSON), "server_urls")
	hostOverrides := stringMapField(payload, jsonStringObject(existing.Row.HostOverridesJSON), "host_overrides")
	useLDAPS := fieldBool(payload, sqlIntBool(existing.Row.UseLDAPS), "use_ldaps")
	tlsRequired := fieldBool(payload, sqlIntBool(existing.Row.TLSRequired), "tls_required")
	nestedGroups := fieldBool(payload, sqlIntBool(existing.Row.NestedGroups), "nested_groups")
	syncInterval := fieldInt(payload, directoryInt64Default(existing.Row.SyncIntervalSeconds, defaultDirectorySyncIntervalSeconds), "sync_interval_seconds")
	if syncInterval < defaultDirectorySyncIntervalSeconds {
		syncInterval = defaultDirectorySyncIntervalSeconds
	}
	bindPasswordEncrypted := nullString(existing.Row.BindPasswordEncrypted)
	if _, ok := payload["bind_password"]; ok {
		bindPassword := cleanText(payload["bind_password"])
		if bindPassword == "" {
			bindPasswordEncrypted = ""
		} else {
			if secret == nil {
				return nil, nil, errAegisLocked
			}
			encrypted, err := secret.encryptSecretText(ctx, bindPassword)
			if err != nil {
				return nil, nil, err
			}
			bindPasswordEncrypted = encrypted
		}
	}
	siteMappings := normalizeDirectorySiteMappings(anyField(payload, existing.siteMappingsAny(), "site_mappings"))
	values := map[string]any{
		"name":                      name,
		"provider_type":             providerType,
		"enabled":                   boolInt64Value(nowEnabled),
		"priority":                  priority,
		"domain_suffix":             fieldText(payload, nullString(existing.Row.DomainSuffix), "domain_suffix"),
		"server_urls_json":          jsonTextList(serverURLs),
		"host_overrides_json":       jsonStringMap(hostOverrides),
		"use_ldaps":                 boolInt64Value(useLDAPS),
		"tls_required":              boolInt64Value(tlsRequired),
		"tls_ca_pem":                fieldText(payload, nullString(existing.Row.TLSCAPEM), "tls_ca_pem"),
		"base_dn":                   fieldText(payload, nullString(existing.Row.BaseDN), "base_dn"),
		"bind_dn":                   fieldText(payload, nullString(existing.Row.BindDN), "bind_dn"),
		"bind_password_encrypted":   bindPasswordEncrypted,
		"user_search_filter":        fieldText(payload, nullString(existing.Row.UserSearchFilter), "user_search_filter"),
		"username_attribute":        fieldText(payload, nullString(existing.Row.UsernameAttribute), "username_attribute"),
		"display_name_attribute":    fieldText(payload, stringWithDefault(existing.Row.DisplayNameAttribute, "displayName"), "display_name_attribute"),
		"email_attribute":           fieldText(payload, stringWithDefault(existing.Row.EmailAttribute, "mail"), "email_attribute"),
		"member_of_attribute":       fieldText(payload, stringWithDefault(existing.Row.MemberOfAttribute, "memberOf"), "member_of_attribute"),
		"group_search_base_dn":      fieldText(payload, nullString(existing.Row.GroupSearchBaseDN), "group_search_base_dn"),
		"nested_groups":             boolInt64Value(nestedGroups),
		"kerberos_realm":            nullString(existing.Row.KerberosRealm),
		"kerberos_kdc":              nullString(existing.Row.KerberosKDC),
		"kerberos_keytab_encrypted": nullString(existing.Row.KerberosKeytabEncrypted),
		"sync_interval_seconds":     syncInterval,
	}
	return values, siteMappings, nil
}

func validateDirectorySiteIDs(ctx context.Context, tx *sql.Tx, mappings []map[string]any) error {
	wanted := map[int64]bool{}
	for _, item := range mappings {
		for _, id := range intSliceFromAny(item["site_ids"]) {
			if id > 0 {
				wanted[id] = true
			}
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(wanted))
	for id := range wanted {
		ids = append(ids, id)
	}
	query, args := inClauseQuery(`SELECT id FROM engine.sites WHERE id IN (%s)`, ids)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	found := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		found[id] = true
	}
	missing := []int64{}
	for _, id := range ids {
		if !found[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return newDirectoryError("site_not_found", fmt.Sprintf("One or more assigned sites no longer exists: %v", missing), http.StatusNotFound)
	}
	return rows.Err()
}

func (s *postgresOperatorStore) authenticateDirectoryLogin(ctx context.Context, secret authSecretService, loginName string, password string) (directoryLoginResult, error) {
	loginName = strings.TrimSpace(loginName)
	if loginName == "" || password == "" {
		return directoryLoginResult{}, newDirectoryError("missing_credentials", "Directory username and password are required.", http.StatusBadRequest)
	}
	accountName, domain := directoryDomainHint(loginName)
	providers, err := s.loadEnabledDirectoryProviders(ctx)
	if err != nil {
		return directoryLoginResult{}, err
	}
	filtered := make([]directoryProviderConfig, 0, len(providers))
	for _, provider := range providers {
		if directoryProviderMatchesDomain(provider, domain) {
			filtered = append(filtered, provider)
		}
	}
	if len(filtered) == 0 {
		return directoryLoginResult{}, newDirectoryError("directory_provider_not_found", "No enabled directory provider matches this login.", http.StatusUnauthorized)
	}
	type candidate struct {
		provider directoryProviderConfig
		user     directoryUserInfo
	}
	candidates := []candidate{}
	for _, provider := range filtered {
		found, ok, err := directorySearchUser(ctx, secret, provider, loginName)
		if err == nil && ok {
			candidates = append(candidates, candidate{provider: provider, user: found})
			continue
		}
		if provider.providerType() == "active_directory" && domain != "" {
			account := loginName
			if !strings.Contains(account, "@") {
				account = accountName + "@" + strings.TrimLeft(provider.domainSuffix(), "@")
			}
			candidates = append(candidates, candidate{provider: provider, user: directoryUserInfo{
				Account:     account,
				DisplayName: accountName,
				Subject:     loginName,
			}})
		}
	}
	if len(candidates) == 0 {
		return directoryLoginResult{}, newDirectoryError("invalid_username_or_password", "Invalid username or password.", http.StatusUnauthorized)
	}
	if domain == "" && len(candidates) > 1 {
		return directoryLoginResult{}, newDirectoryError("ambiguous_directory_username", "Multiple directory providers contain this username. Use a domain-qualified username.", http.StatusConflict)
	}
	selected := candidates[0]
	if err := directoryVerifyPassword(ctx, secret, selected.provider, selected.user, loginName, password); err != nil {
		return directoryLoginResult{}, err
	}
	role := directoryMappedRole(selected.provider, selected.user.Groups)
	if role == "" {
		return directoryLoginResult{}, newDirectoryError("directory_group_not_allowed", "Directory user is not in an allowed group.", http.StatusForbidden)
	}
	return s.upsertDirectoryUser(ctx, selected.provider, selected.user, role)
}

func (s *postgresOperatorStore) upsertDirectoryUser(ctx context.Context, provider directoryProviderConfig, userInfo directoryUserInfo, role string) (directoryLoginResult, error) {
	providerID := provider.id()
	username := firstText(strings.TrimSpace(userInfo.Account), strings.TrimSpace(userInfo.Subject), directoryDefaultUsername(provider))
	displayName := firstText(userInfo.DisplayName, username)
	domain := strings.TrimLeft(provider.domainSuffix(), "@")
	dn := strings.TrimSpace(userInfo.DN)
	subject := firstText(userInfo.Subject, dn, username)
	groups := cleanStringSlice(userInfo.Groups)
	now := time.Now().Unix()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return directoryLoginResult{}, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return directoryLoginResult{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	var userID int64
	var authSource string
	var existingProviderID int64
	err = tx.QueryRowContext(ctx, `
		SELECT id, COALESCE(auth_source, 'local'), COALESCE(directory_provider_id, 0)
		  FROM engine.users
		 WHERE LOWER(username)=LOWER($1)
		 LIMIT 1
	`, username).Scan(&userID, &authSource, &existingProviderID)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return directoryLoginResult{}, err
	}
	if userID > 0 && strings.ToLower(authSource) != directoryAuth {
		return directoryLoginResult{}, newDirectoryError("directory_username_conflict", "A local Borealis user already owns this username.", http.StatusConflict)
	}
	if userID > 0 && existingProviderID != 0 && existingProviderID != providerID {
		return directoryLoginResult{}, newDirectoryError("directory_username_conflict", "Another directory provider already owns this username.", http.StatusConflict)
	}
	groupsJSON := jsonTextList(groups)
	if userID > 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE engine.users
			   SET display_name=$1,
			       role=$2,
			       auth_source=$3,
			       directory_provider_id=$4,
			       directory_subject=$5,
			       directory_domain=$6,
			       directory_dn=$7,
			       directory_groups_json=$8,
			       directory_last_sync_at=$9,
			       directory_disabled=0,
			       directory_disabled_at=NULL,
			       mfa_disabled=0,
			       updated_at=$10
			 WHERE id=$11
		`, displayName, canonicalDirectoryRole(role), directoryAuth, providerID, subject, domain, dn, groupsJSON, now, now, userID); err != nil {
			return directoryLoginResult{}, err
		}
	} else {
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO engine.users(
				username, display_name, password_sha512, role, last_login,
				created_at, updated_at, mfa_enabled, mfa_disabled,
				auth_reset_required, auth_source, directory_provider_id,
				directory_subject, directory_domain, directory_dn,
				directory_groups_json, directory_last_sync_at, directory_disabled
			) VALUES($1,$2,$3,$4,0,$5,$6,0,0,0,$7,$8,$9,$10,$11,$12,$13,0)
			RETURNING id
		`, username, displayName, directoryPasswordPlaceholder, canonicalDirectoryRole(role), now, now, directoryAuth, providerID, subject, domain, dn, groupsJSON, now).Scan(&userID); err != nil {
			return directoryLoginResult{}, err
		}
	}
	if err := replaceDirectorySiteAssignments(ctx, tx, userID, role, provider, groups, now); err != nil {
		return directoryLoginResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return directoryLoginResult{}, err
	}
	committed = true
	return directoryLoginResult{Username: username, DisplayName: displayName, Role: canonicalDirectoryRole(role), ProviderID: providerID, ProviderName: provider.name(), Domain: domain, DN: dn, Subject: subject, Groups: groups}, nil
}

func replaceDirectorySiteAssignments(ctx context.Context, tx *sql.Tx, userID int64, role string, provider directoryProviderConfig, groups []string, assignedAt int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM engine.user_site_assignments WHERE user_id=$1`, userID); err != nil {
		return err
	}
	if canonicalDirectoryRole(role) == "Admin" {
		return nil
	}
	groupSet := map[string]bool{}
	for _, group := range groups {
		groupSet[strings.ToLower(strings.TrimSpace(group))] = true
	}
	seen := map[int64]bool{}
	for _, mapping := range provider.SiteMappings {
		mappingGroups := directoryStringSliceFromAny(mapping["group_dns"])
		matched := false
		for _, group := range mappingGroups {
			if groupSet[strings.ToLower(strings.TrimSpace(group))] {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		for _, siteID := range intSliceFromAny(mapping["site_ids"]) {
			if siteID <= 0 || seen[siteID] {
				continue
			}
			seen[siteID] = true
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO engine.user_site_assignments(user_id, site_id, assigned_at)
				VALUES($1,$2,$3)
				ON CONFLICT DO NOTHING
			`, userID, siteID, assignedAt); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *postgresOperatorStore) refreshCachedDirectorySiteAssignments(ctx context.Context, provider directoryProviderConfig) (int, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return 0, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, `
		SELECT id, COALESCE(role, $1), COALESCE(directory_groups_json, '[]')
		  FROM engine.users
		 WHERE COALESCE(auth_source, 'local')=$2
		   AND COALESCE(directory_provider_id, 0)=$3
		   AND COALESCE(directory_disabled, 0)=0
	`, defaultUserRole, directoryAuth, provider.id())
	if err != nil {
		return 0, err
	}
	type cached struct {
		id     int64
		role   string
		groups []string
	}
	cachedRows := []cached{}
	for rows.Next() {
		var id int64
		var role string
		var groupsRaw string
		if err := rows.Scan(&id, &role, &groupsRaw); err != nil {
			_ = rows.Close()
			return 0, err
		}
		cachedRows = append(cachedRows, cached{id: id, role: role, groups: stringSliceFromJSON(groupsRaw)})
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	now := time.Now().Unix()
	for _, row := range cachedRows {
		if err := replaceDirectorySiteAssignments(ctx, tx, row.id, row.role, provider, row.groups, now); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	committed = true
	return len(cachedRows), nil
}

func directoryLoginAndRespond(w http.ResponseWriter, r *http.Request, auth *authService, loginStore authLoginStore, username string, password string) {
	if strings.TrimSpace(password) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "directory_password_required"})
		return
	}
	dirStore, ok := auth.store.(interface {
		authenticateDirectoryLogin(context.Context, authSecretService, string, string) (directoryLoginResult, error)
	})
	if !ok || auth.aegis == nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "directory_auth_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), directoryAuthTimeout)
	defer cancel()
	result, err := dirStore.authenticateDirectoryLogin(ctx, auth.aegis, username, password)
	if err != nil {
		writeDirectoryError(w, err)
		return
	}
	row, found, err := loginStore.loadLoginRow(ctx, result.Username)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_store_unavailable", "message": err.Error()})
		return
	}
	if !found {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "directory_cache_failed"})
		return
	}
	existingSecret := ""
	if strings.TrimSpace(row.MFASecret) != "" {
		existingSecret, _ = auth.aegis.decryptSecretText(ctx, row.MFASecret)
	}
	beginMFAOrFinalize(w, r, auth, loginStore, row.Username, firstText(row.Role, result.Role, defaultUserRole), strings.TrimSpace(existingSecret), false, "")
}

func (p directoryProviderConfig) id() int64 {
	return nullInt(p.Row.ID)
}

func (p directoryProviderConfig) name() string {
	return nullString(p.Row.Name)
}

func (p directoryProviderConfig) providerType() string {
	return normalizeDirectoryProviderType(nullString(p.Row.ProviderType))
}

func (p directoryProviderConfig) enabled() bool {
	return sqlIntBool(p.Row.Enabled)
}

func (p directoryProviderConfig) domainSuffix() string {
	return nullString(p.Row.DomainSuffix)
}

func (p directoryProviderConfig) lastTestStatus() string {
	return nullString(p.Row.LastTestStatus)
}

func (p directoryProviderConfig) publicPayload() map[string]any {
	return directoryProviderPayload(p.Row, p.GroupMappings, p.SiteMappings)
}

func (p directoryProviderConfig) serverURLs() []string {
	scheme := "ldap"
	if sqlIntBool(p.Row.UseLDAPS) {
		scheme = "ldaps"
	}
	raw := jsonStringList(p.Row.ServerURLsJSON)
	urls := make([]string, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if strings.Contains(item, "://") {
			urls = append(urls, item)
		} else {
			urls = append(urls, scheme+"://"+item)
		}
	}
	return urls
}

func (p directoryProviderConfig) hostOverrides() map[string]string {
	return jsonStringObject(p.Row.HostOverridesJSON)
}

func (p directoryProviderConfig) defaultUsernameAttribute() string {
	if p.providerType() == "active_directory" {
		return "sAMAccountName"
	}
	return "uid"
}

func (p directoryProviderConfig) siteMappingsAny() any {
	if len(p.SiteMappings) == 0 {
		return []any{}
	}
	return p.SiteMappings
}

func directoryMappedRole(provider directoryProviderConfig, groups []string) string {
	if len(provider.GroupMappings) == 0 {
		return "User"
	}
	groupSet := map[string]bool{}
	for _, group := range groups {
		groupSet[strings.ToLower(strings.TrimSpace(group))] = true
	}
	matched := ""
	for _, mapping := range provider.GroupMappings {
		groupDN := strings.ToLower(cleanText(mapping["group_dn"]))
		if groupDN == "" || !groupSet[groupDN] {
			continue
		}
		role := canonicalDirectoryRole(cleanText(mapping["role"]))
		if role == "Admin" {
			return "Admin"
		}
		matched = "User"
	}
	return matched
}

func directoryDefaultUsername(provider directoryProviderConfig) string {
	domain := strings.TrimLeft(provider.domainSuffix(), "@")
	if domain != "" {
		return "user@" + domain
	}
	return "directory-user"
}

func directoryDomainHint(rawUsername string) (string, string) {
	text := strings.TrimSpace(rawUsername)
	if strings.Contains(text, "\\") {
		parts := strings.SplitN(text, "\\", 2)
		return strings.TrimSpace(parts[1]), strings.ToLower(strings.TrimSpace(parts[0]))
	}
	if strings.Contains(text, "@") {
		parts := strings.Split(text, "@")
		account := strings.Join(parts[:len(parts)-1], "@")
		return strings.TrimSpace(account), strings.ToLower(strings.TrimSpace(parts[len(parts)-1]))
	}
	return text, ""
}

func directoryProviderMatchesDomain(provider directoryProviderConfig, domain string) bool {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return true
	}
	suffix := strings.ToLower(strings.TrimLeft(provider.domainSuffix(), "@"))
	realm := strings.ToLower(nullString(provider.Row.KerberosRealm))
	if suffix != "" && (domain == suffix || domain == strings.SplitN(suffix, ".", 2)[0]) {
		return true
	}
	if realm != "" && (domain == realm || domain == strings.SplitN(realm, ".", 2)[0]) {
		return true
	}
	return false
}

func fieldText(payload map[string]any, fallback string, key string) string {
	if value, ok := payload[key]; ok {
		return cleanText(value)
	}
	return strings.TrimSpace(fallback)
}

func fieldInt(payload map[string]any, fallback int64, key string) int64 {
	if value, ok := payload[key]; ok {
		if parsed := parseInt64Any(value); parsed != 0 {
			return parsed
		}
		return 0
	}
	return fallback
}

func fieldBool(payload map[string]any, fallback bool, key string) bool {
	if value, ok := payload[key]; ok {
		return truthyPayload(value)
	}
	return fallback
}

func anyField(payload map[string]any, fallback any, key string) any {
	if value, ok := payload[key]; ok {
		return value
	}
	return fallback
}

func stringSliceField(payload map[string]any, fallback []string, key string) []string {
	if value, ok := payload[key]; ok {
		return directoryStringSliceFromAny(value)
	}
	return fallback
}

func stringMapField(payload map[string]any, fallback map[string]string, key string) map[string]string {
	if value, ok := payload[key]; ok {
		return splitDirectoryHostOverrides(value)
	}
	return fallback
}

func directoryStringSliceFromAny(value any) []string {
	switch typed := value.(type) {
	case nil:
		return []string{}
	case []string:
		return cleanStringSlice(typed)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := cleanText(item); text != "" {
				out = append(out, text)
			}
		}
		return out
	case string:
		if strings.HasPrefix(strings.TrimSpace(typed), "[") {
			return stringSliceFromJSON(typed)
		}
		parts := strings.FieldsFunc(typed, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' })
		return cleanStringSlice(parts)
	default:
		if text := cleanText(value); text != "" {
			return []string{text}
		}
		return []string{}
	}
}

func cleanStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func stringSliceFromJSON(raw string) []string {
	var values []any
	if err := json.Unmarshal([]byte(firstText(strings.TrimSpace(raw), "[]")), &values); err != nil {
		return []string{}
	}
	return directoryStringSliceFromAny(values)
}

func intSliceFromAny(value any) []int64 {
	switch typed := value.(type) {
	case []int64:
		return uniquePositiveInts(typed)
	case []int:
		out := make([]int64, 0, len(typed))
		for _, item := range typed {
			out = append(out, int64(item))
		}
		return uniquePositiveInts(out)
	case []any:
		out := make([]int64, 0, len(typed))
		for _, item := range typed {
			out = append(out, parseInt64Any(item))
		}
		return uniquePositiveInts(out)
	case string:
		if strings.HasPrefix(strings.TrimSpace(typed), "[") {
			var values []any
			if err := json.Unmarshal([]byte(typed), &values); err == nil {
				return intSliceFromAny(values)
			}
		}
		out := []int64{}
		for _, part := range strings.FieldsFunc(typed, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
			out = append(out, parseInt64Any(part))
		}
		return uniquePositiveInts(out)
	default:
		return uniquePositiveInts([]int64{parseInt64Any(value)})
	}
}

func uniquePositiveInts(values []int64) []int64 {
	seen := map[int64]bool{}
	out := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func jsonTextList(value any) string {
	raw, _ := json.Marshal(directoryStringSliceFromAny(value))
	return string(raw)
}

func jsonIntListText(value any) string {
	raw, _ := json.Marshal(intSliceFromAny(value))
	return string(raw)
}

func jsonStringMap(values map[string]string) string {
	cleaned := map[string]string{}
	for key, value := range values {
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			cleaned[key] = value
		}
	}
	raw, _ := json.Marshal(cleaned)
	return string(raw)
}

func mappingSlice(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		return typed
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if mapped, ok := item.(map[string]any); ok {
				out = append(out, mapped)
			}
		}
		return out
	default:
		return []map[string]any{}
	}
}

func normalizeDirectorySiteMappings(value any) []map[string]any {
	raw := mappingSlice(value)
	out := make([]map[string]any, 0, len(raw))
	for idx, item := range raw {
		groupDNS := directoryStringSliceFromAny(firstTextAny(item["group_dns"], item["group_dn"]))
		siteIDs := intSliceFromAny(firstTextAny(item["site_ids"], item["sites"]))
		if len(groupDNS) == 0 && len(siteIDs) == 0 {
			continue
		}
		label := firstText(cleanText(item["label"]), fmt.Sprintf("User Site Group %d", idx+1))
		out = append(out, map[string]any{
			"label":     label,
			"group_dns": groupDNS,
			"site_ids":  siteIDs,
			"position":  parseInt64Any(item["position"]),
		})
	}
	return out
}

func firstTextAny(values ...any) any {
	for _, value := range values {
		switch typed := value.(type) {
		case nil:
			continue
		case string:
			if strings.TrimSpace(typed) != "" {
				return typed
			}
		default:
			return value
		}
	}
	return nil
}

func truthyPayloadDefault(value any, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return truthyPayload(value)
}

func boolInt64Value(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func directoryInt64Default(value sql.NullInt64, fallback int64) int64 {
	if value.Valid {
		return value.Int64
	}
	return fallback
}

func truncateText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit > 0 && len(value) > limit {
		return value[:limit]
	}
	return value
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}
