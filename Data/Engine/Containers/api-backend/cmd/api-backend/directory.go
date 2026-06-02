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

const defaultDirectorySyncIntervalSeconds = 60

type directoryReadStore interface {
	listDirectoryProviders(ctx context.Context) ([]map[string]any, error)
	listDirectorySites(ctx context.Context) ([]map[string]any, error)
}

type directoryProviderRow struct {
	ID                      sql.NullInt64
	Name                    sql.NullString
	ProviderType            sql.NullString
	Enabled                 sql.NullInt64
	Priority                sql.NullInt64
	DomainSuffix            sql.NullString
	ServerURLsJSON          sql.NullString
	HostOverridesJSON       sql.NullString
	UseLDAPS                sql.NullInt64
	TLSRequired             sql.NullInt64
	TLSCAPEM                sql.NullString
	BaseDN                  sql.NullString
	BindDN                  sql.NullString
	BindPasswordEncrypted   sql.NullString
	UserSearchFilter        sql.NullString
	UsernameAttribute       sql.NullString
	DisplayNameAttribute    sql.NullString
	EmailAttribute          sql.NullString
	MemberOfAttribute       sql.NullString
	GroupSearchBaseDN       sql.NullString
	NestedGroups            sql.NullInt64
	KerberosRealm           sql.NullString
	KerberosKDC             sql.NullString
	KerberosKeytabEncrypted sql.NullString
	SyncIntervalSeconds     sql.NullInt64
	LastSyncAt              sql.NullInt64
	LastSyncStatus          sql.NullString
	LastSyncMessage         sql.NullString
	LastTestAt              sql.NullInt64
	LastTestStatus          sql.NullString
	LastTestMessage         sql.NullString
	CreatedAt               sql.NullInt64
	UpdatedAt               sql.NullInt64
}

type directoryGroupMappingRow struct {
	ProviderID sql.NullInt64
	GroupDN    sql.NullString
	Role       sql.NullString
}

type directorySiteMappingRow struct {
	ID           sql.NullInt64
	ProviderID   sql.NullInt64
	Label        sql.NullString
	GroupDNSJSON sql.NullString
	SiteIDsJSON  sql.NullString
	Position     sql.NullInt64
}

type directorySiteRow struct {
	ID             sql.NullInt64
	Name           sql.NullString
	Description    sql.NullString
	CreatedAt      sql.NullInt64
	DeviceCount    sql.NullInt64
	EnrollmentCode sql.NullString
}

func registerDirectoryRoutes(mux *http.ServeMux, auth *authService, fallback http.Handler) {
	mux.HandleFunc("/api/directory/providers", directoryProvidersHandler(auth, fallback))
	mux.HandleFunc("/api/directory/sites", directorySitesHandler(auth, fallback))
}

func directoryProvidersHandler(auth *authService, fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			proxyFallbackOrMethodNotAllowed(w, r, fallback, http.MethodGet)
			return
		}
		if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		store, ok := auth.store.(directoryReadStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "directory_unavailable"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), authTimeout(auth))
		defer cancel()
		providers, err := store.listDirectoryProviders(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"providers": providers})
	}
}

func directorySitesHandler(auth *authService, fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			proxyFallbackOrMethodNotAllowed(w, r, fallback, http.MethodGet)
			return
		}
		if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		store, ok := auth.store.(directoryReadStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "directory_unavailable"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), authTimeout(auth))
		defer cancel()
		sites, err := store.listDirectorySites(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"sites": sites})
	}
}

func authTimeout(auth *authService) time.Duration {
	timeout := defaultAuthTimeout
	if auth != nil && auth.timeout > 0 {
		timeout = auth.timeout
	}
	return timeout
}

func (s *postgresOperatorStore) listDirectoryProviders(ctx context.Context) ([]map[string]any, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		ctx,
		`
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
		ORDER BY priority ASC, LOWER(name) ASC
		`,
	)
	if err != nil {
		return nil, err
	}

	providers := make([]directoryProviderRow, 0)
	providerIDs := make([]int64, 0)
	for rows.Next() {
		var row directoryProviderRow
		if err := rows.Scan(
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
		); err != nil {
			_ = rows.Close()
			return nil, err
		}
		providers = append(providers, row)
		if row.ID.Valid {
			providerIDs = append(providerIDs, row.ID.Int64)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	groupMappings, err := loadDirectoryGroupMappings(ctx, conn, providerIDs)
	if err != nil {
		return nil, err
	}
	siteMappings, err := loadDirectorySiteMappings(ctx, conn, providerIDs)
	if err != nil {
		return nil, err
	}

	result := make([]map[string]any, 0, len(providers))
	for _, provider := range providers {
		result = append(result, directoryProviderPayload(provider, groupMappings[nullInt(provider.ID)], siteMappings[nullInt(provider.ID)]))
	}
	return result, nil
}

func loadDirectoryGroupMappings(ctx context.Context, conn *sql.Conn, providerIDs []int64) (map[int64][]map[string]any, error) {
	result := make(map[int64][]map[string]any, len(providerIDs))
	if len(providerIDs) == 0 {
		return result, nil
	}
	query, args := inClauseQuery(
		`
		SELECT provider_id, group_dn, role
		FROM engine.directory_provider_group_mappings
		WHERE provider_id IN (%s)
		ORDER BY LOWER(group_dn) ASC, role ASC
		`,
		providerIDs,
	)
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var row directoryGroupMappingRow
		if err := rows.Scan(&row.ProviderID, &row.GroupDN, &row.Role); err != nil {
			return nil, err
		}
		providerID := nullInt(row.ProviderID)
		groupDN := nullString(row.GroupDN)
		if groupDN == "" {
			continue
		}
		result[providerID] = append(result[providerID], map[string]any{
			"group_dn": groupDN,
			"role":     canonicalDirectoryRole(nullString(row.Role)),
		})
	}
	return result, rows.Err()
}

func loadDirectorySiteMappings(ctx context.Context, conn *sql.Conn, providerIDs []int64) (map[int64][]map[string]any, error) {
	result := make(map[int64][]map[string]any, len(providerIDs))
	if len(providerIDs) == 0 {
		return result, nil
	}
	query, args := inClauseQuery(
		`
		SELECT id, provider_id, label, group_dns_json, site_ids_json, position
		FROM engine.directory_provider_site_mappings
		WHERE provider_id IN (%s)
		ORDER BY provider_id ASC, position ASC, id ASC
		`,
		providerIDs,
	)
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var row directorySiteMappingRow
		if err := rows.Scan(&row.ID, &row.ProviderID, &row.Label, &row.GroupDNSJSON, &row.SiteIDsJSON, &row.Position); err != nil {
			return nil, err
		}
		groupDNS := jsonStringList(row.GroupDNSJSON)
		siteIDs := jsonIntList(row.SiteIDsJSON)
		if len(groupDNS) == 0 && len(siteIDs) == 0 {
			continue
		}
		providerID := nullInt(row.ProviderID)
		result[providerID] = append(result[providerID], map[string]any{
			"id":        nullInt(row.ID),
			"label":     nullString(row.Label),
			"group_dns": groupDNS,
			"site_ids":  siteIDs,
			"position":  nullInt(row.Position),
		})
	}
	return result, rows.Err()
}

func (s *postgresOperatorStore) listDirectorySites(ctx context.Context) ([]map[string]any, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		ctx,
		`
		SELECT s.id,
		       s.name,
		       s.description,
		       s.created_at,
		       COALESCE(ds.cnt, 0) AS device_count,
		       s.enrollment_code
		  FROM engine.sites AS s
	 LEFT JOIN (
		       SELECT site_id, COUNT(*) AS cnt
		         FROM engine.device_sites
		     GROUP BY site_id
		   ) AS ds ON ds.site_id = s.id
	  ORDER BY LOWER(s.name) ASC
		`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sites := make([]map[string]any, 0)
	for rows.Next() {
		var row directorySiteRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Description, &row.CreatedAt, &row.DeviceCount, &row.EnrollmentCode); err != nil {
			return nil, err
		}
		sites = append(sites, map[string]any{
			"id":              nullInt(row.ID),
			"name":            nullString(row.Name),
			"description":     nullString(row.Description),
			"created_at":      nullInt(row.CreatedAt),
			"device_count":    nullInt(row.DeviceCount),
			"enrollment_code": nullString(row.EnrollmentCode),
		})
	}
	return sites, rows.Err()
}

func directoryProviderPayload(provider directoryProviderRow, groupMappings []map[string]any, siteMappings []map[string]any) map[string]any {
	providerType := normalizeDirectoryProviderType(nullString(provider.ProviderType))
	usernameAttribute := nullString(provider.UsernameAttribute)
	if usernameAttribute == "" {
		if providerType == "active_directory" {
			usernameAttribute = "sAMAccountName"
		} else {
			usernameAttribute = "uid"
		}
	}
	return map[string]any{
		"id":                      nullInt(provider.ID),
		"name":                    nullString(provider.Name),
		"provider_type":           providerType,
		"enabled":                 sqlIntBool(provider.Enabled),
		"priority":                intWithDefault(provider.Priority, 100),
		"domain_suffix":           nullString(provider.DomainSuffix),
		"server_urls":             jsonStringList(provider.ServerURLsJSON),
		"host_overrides":          jsonStringObject(provider.HostOverridesJSON),
		"use_ldaps":               sqlIntBool(provider.UseLDAPS),
		"tls_required":            sqlIntBool(provider.TLSRequired),
		"tls_ca_pem_present":      nullString(provider.TLSCAPEM) != "",
		"base_dn":                 nullString(provider.BaseDN),
		"bind_dn":                 nullString(provider.BindDN),
		"bind_password_present":   nullString(provider.BindPasswordEncrypted) != "",
		"user_search_filter":      nullString(provider.UserSearchFilter),
		"username_attribute":      usernameAttribute,
		"display_name_attribute":  stringWithDefault(provider.DisplayNameAttribute, "displayName"),
		"email_attribute":         stringWithDefault(provider.EmailAttribute, "mail"),
		"member_of_attribute":     stringWithDefault(provider.MemberOfAttribute, "memberOf"),
		"group_search_base_dn":    nullString(provider.GroupSearchBaseDN),
		"nested_groups":           sqlIntBool(provider.NestedGroups),
		"kerberos_realm":          nullString(provider.KerberosRealm),
		"kerberos_kdc":            nullString(provider.KerberosKDC),
		"kerberos_keytab_present": nullString(provider.KerberosKeytabEncrypted) != "",
		"sync_interval_seconds":   intWithDefault(provider.SyncIntervalSeconds, defaultDirectorySyncIntervalSeconds),
		"last_sync_at":            nullInt(provider.LastSyncAt),
		"last_sync_status":        nullString(provider.LastSyncStatus),
		"last_sync_message":       nullString(provider.LastSyncMessage),
		"last_test_at":            nullInt(provider.LastTestAt),
		"last_test_status":        nullString(provider.LastTestStatus),
		"last_test_message":       nullString(provider.LastTestMessage),
		"created_at":              nullInt(provider.CreatedAt),
		"updated_at":              nullInt(provider.UpdatedAt),
		"group_mappings":          groupMappings,
		"site_mappings":           siteMappings,
	}
}

func inClauseQuery(template string, ids []int64) (string, []any) {
	placeholders := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))
	for idx, id := range ids {
		placeholders = append(placeholders, "$"+fmt.Sprint(idx+1))
		args = append(args, id)
	}
	return fmt.Sprintf(template, strings.Join(placeholders, ",")), args
}

func normalizeDirectoryProviderType(value string) string {
	text := strings.NewReplacer("-", "_", " ", "_").Replace(strings.ToLower(strings.TrimSpace(value)))
	switch text {
	case "ad", "active_directory", "activedirectory":
		return "active_directory"
	default:
		return "ldap"
	}
}

func canonicalDirectoryRole(value string) string {
	role := strings.Title(strings.ToLower(strings.TrimSpace(value)))
	if role == "Admin" || role == "User" {
		return role
	}
	return "User"
}

func sqlIntBool(value sql.NullInt64) bool {
	return value.Valid && value.Int64 != 0
}

func intWithDefault(value sql.NullInt64, fallback int64) int64 {
	if value.Valid {
		return value.Int64
	}
	return fallback
}

func stringWithDefault(value sql.NullString, fallback string) string {
	text := nullString(value)
	if text != "" {
		return text
	}
	return fallback
}

func jsonStringList(raw sql.NullString) []string {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return []string{}
	}
	var items []any
	if err := json.Unmarshal([]byte(raw.String), &items); err != nil {
		return []string{}
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		text := strings.TrimSpace(fmt.Sprint(item))
		if text != "" && text != "<nil>" {
			out = append(out, text)
		}
	}
	return out
}

func jsonIntList(raw sql.NullString) []int64 {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return []int64{}
	}
	var items []any
	if err := json.Unmarshal([]byte(raw.String), &items); err != nil {
		return []int64{}
	}
	out := make([]int64, 0, len(items))
	seen := map[int64]bool{}
	for _, item := range items {
		var parsed int64
		switch value := item.(type) {
		case float64:
			parsed = int64(value)
		case string:
			n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			if err != nil {
				continue
			}
			parsed = n
		default:
			continue
		}
		if seen[parsed] {
			continue
		}
		seen[parsed] = true
		out = append(out, parsed)
	}
	return out
}

func jsonStringObject(raw sql.NullString) map[string]string {
	result := map[string]string{}
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return result
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw.String), &parsed); err != nil {
		return result
	}
	for key, value := range parsed {
		cleanKey := strings.ToLower(strings.TrimSpace(key))
		cleanValue := strings.TrimSpace(fmt.Sprint(value))
		if cleanKey != "" && cleanValue != "" && cleanValue != "<nil>" {
			result[cleanKey] = cleanValue
		}
	}
	return result
}
