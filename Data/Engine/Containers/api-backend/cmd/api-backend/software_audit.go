package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var (
	softwareAuditMetadataKeys = map[string]struct{}{
		"display_icon":                  {},
		"display_icon_override":         {},
		"display_icon_override_cleared": {},
		"display_icon_override_rule_id": {},
		"distribution_app_id":           {},
		"distribution_platform":         {},
		"icon_hash":                     {},
		"icon_location":                 {},
		"install_location":              {},
		"non_removable":                 {},
		"original_display_icon":         {},
		"package_family_name":           {},
		"product_code":                  {},
		"publisher":                     {},
		"quiet_uninstall_string":        {},
		"uninstall_string":              {},
	}
	windowsProductCodePattern = regexp.MustCompile(`(?i)^\{[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\}$`)
	windowsProductCodeInText  = regexp.MustCompile(`(?i)\{[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\}`)
	windowsQuietSwitchPattern = regexp.MustCompile(`(?i)(^|\s)(/quiet|/qn|/qb!?|/passive|/s(\s|$)|/silent|/verysilent|--silent|--quiet|/suppressmsgboxes)(\s|$)`)
)

type softwareAuditStore interface {
	listSoftwareAudit(ctx context.Context, profile operatorProfile) ([]map[string]any, error)
}

type softwareAuditRow struct {
	ID              sql.NullInt64
	DeviceGUID      sql.NullString
	Name            sql.NullString
	Version         sql.NullString
	Source          sql.NullString
	CapturedAt      sql.NullInt64
	MetadataJSON    sql.NullString
	GUID            sql.NullString
	Hostname        sql.NullString
	AgentID         sql.NullString
	OperatingSystem sql.NullString
	SiteID          sql.NullInt64
	SiteName        sql.NullString
}

func softwareAuditHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, failure := requireUser(r.Context(), auth, r)
		if failure != nil {
			failure.write(w)
			return
		}
		store, ok := auth.store.(softwareAuditStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "software_audit_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		profile, err := auth.store.lookupOperator(ctx, identity.Username, identity.Role)
		if err != nil {
			if errors.Is(err, errOperatorNotFound) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "operator_lookup_failed", "message": err.Error()})
			return
		}
		rows, err := store.listSoftwareAudit(ctx, profile)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "software_audit_failed", "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"software": rows, "count": len(rows)})
	}
}

func (s *postgresOperatorStore) listSoftwareAudit(ctx context.Context, profile operatorProfile) ([]map[string]any, error) {
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, err
	}
	if allowedSiteIDs != nil && len(allowedSiteIDs) == 0 {
		return []map[string]any{}, nil
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	query := `
		SELECT
			dsi.id,
			dsi.device_guid,
			dsi.name,
			dsi.version,
			dsi.source,
			dsi.captured_at,
			CASE
				WHEN LOWER(COALESCE(d.operating_system, '')) LIKE '%windows%'
				  OR LOWER(COALESCE(d.operating_system, '')) LIKE 'win%'
				THEN dsi.metadata_json
				ELSE NULL
			END AS metadata_json,
			d.guid,
			d.hostname,
			d.agent_id,
			d.operating_system,
			ds.site_id,
			s.name
		  FROM engine.device_software_inventory AS dsi
		  JOIN engine.devices AS d
		    ON d.guid = dsi.device_guid
	 LEFT JOIN engine.device_sites AS ds
		    ON ds.device_hostname = d.hostname
	 LEFT JOIN engine.sites AS s
		    ON s.id = ds.site_id
		 WHERE TRIM(COALESCE(dsi.name, '')) <> ''
	`
	args := []any{}
	if allowedSiteIDs != nil {
		placeholders := make([]string, 0, len(allowedSiteIDs))
		for idx, siteID := range allowedSiteIDs {
			placeholders = append(placeholders, "$"+strconv.Itoa(idx+1))
			args = append(args, siteID)
		}
		query += " AND ds.site_id IN (" + strings.Join(placeholders, ",") + ")"
	}

	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	rawRows := make([]softwareAuditRow, 0)
	for rows.Next() {
		var row softwareAuditRow
		if err := rows.Scan(
			&row.ID,
			&row.DeviceGUID,
			&row.Name,
			&row.Version,
			&row.Source,
			&row.CapturedAt,
			&row.MetadataJSON,
			&row.GUID,
			&row.Hostname,
			&row.AgentID,
			&row.OperatingSystem,
			&row.SiteID,
			&row.SiteName,
		); err != nil {
			_ = rows.Close()
			return nil, err
		}
		rawRows = append(rawRows, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	result := make([]map[string]any, 0, len(rawRows))
	for _, row := range rawRows {
		result = append(result, softwareAuditPayload(row))
	}
	return result, nil
}

func softwareAuditPayload(row softwareAuditRow) map[string]any {
	platform := normalizeSoftwarePlatform(nullString(row.OperatingSystem))
	metadata := map[string]any{}
	if platform == "windows" {
		metadata = softwareAuditMetadata(parseJSONObject(row.MetadataJSON))
	}
	name := cleanText(nullString(row.Name))
	version := cleanText(nullString(row.Version))
	source := normalizeSoftwareSource(nullString(row.Source))
	uninstall := softwareUninstallCapability(name, version, source, metadata, nullString(row.OperatingSystem))
	payload := map[string]any{
		"id":               nullInt(row.ID),
		"inventory_id":     nullInt(row.ID),
		"name":             name,
		"version":          version,
		"source":           source,
		"metadata":         metadata,
		"captured_at":      nullInt(row.CapturedAt),
		"device_guid":      cleanText(nullString(row.DeviceGUID)),
		"hostname":         cleanText(nullString(row.Hostname)),
		"agent_id":         cleanText(nullString(row.AgentID)),
		"operating_system": cleanText(nullString(row.OperatingSystem)),
		"platform":         platform,
		"site_id":          nil,
		"site_name":        cleanText(nullString(row.SiteName)),
		"uninstall":        uninstall,
	}
	if row.SiteID.Valid {
		payload["site_id"] = row.SiteID.Int64
	}
	if value := cleanText(metadata["distribution_platform"]); value != "" {
		payload["distribution_platform"] = value
	}
	if value := cleanText(metadata["distribution_app_id"]); value != "" {
		payload["distribution_app_id"] = value
	}
	return payload
}

func normalizeSoftwarePlatform(value any) string {
	text := strings.ToLower(cleanText(value))
	switch {
	case strings.Contains(text, "windows") || strings.HasPrefix(text, "win"):
		return "windows"
	case strings.Contains(text, "mac") || strings.Contains(text, "darwin") || strings.Contains(text, "os x"):
		return "macos"
	case text != "":
		return "linux"
	default:
		return "unknown"
	}
}

func normalizeSoftwareSource(value any) string {
	text := strings.ToLower(cleanText(value))
	switch text {
	case "appx", "ms_store", "store", "windows_store":
		return "windows_store"
	case "installed", "local", "local_installed", "registry", "uninstall_registry":
		return "local_installed"
	case "":
		return "local_installed"
	default:
		return text
	}
}

func softwareAuditMetadata(metadata map[string]any) map[string]any {
	filtered := map[string]any{}
	for key, value := range metadata {
		key = cleanText(key)
		if _, ok := softwareAuditMetadataKeys[key]; !ok || !metadataValuePresent(value) {
			continue
		}
		filtered[key] = value
	}
	return filtered
}

func metadataValuePresent(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(typed) != ""
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	default:
		return true
	}
}

func softwareUninstallCapability(name string, version string, source string, metadata map[string]any, operatingSystem string) map[string]any {
	osText := strings.ToLower(cleanText(operatingSystem))
	if osText == "" {
		return unsupportedSoftwareUninstall("Borealis has not received the device platform yet.")
	}
	if !strings.Contains(osText, "windows") {
		return unsupportedSoftwareUninstall("Windows uninstall support ships first. Linux support comes next.")
	}
	if parseSoftwareBoolish(metadata["non_removable"]) {
		return unsupportedSoftwareUninstall("This software package is marked non-removable by inventory.")
	}
	if normalizeSoftwareSource(source) == "windows_store" {
		packageFamily := cleanText(metadata["package_family_name"])
		if packageFamily == "" {
			return unsupportedSoftwareUninstall("Windows Store package metadata is missing the package family name.")
		}
		return supportedSoftwareUninstall(map[string]any{
			"strategy":            "windows_store_package",
			"summary":             "Uses Remove-AppxPackage for the Windows Store package.",
			"rule_id":             "metadata_windows_store_package",
			"package_family_name": packageFamily,
		})
	}

	quiet := cleanText(metadata["quiet_uninstall_string"])
	uninstall := cleanText(metadata["uninstall_string"])
	productCode := strings.ToUpper(cleanText(metadata["product_code"]))
	if productCode != "" && windowsProductCodePattern.MatchString(productCode) {
		return supportedSoftwareUninstall(map[string]any{
			"strategy":         "msi_product_code",
			"summary":          "Uses the MSI product code from inventory.",
			"rule_id":          "metadata_product_code",
			"uninstall_string": uninstall,
			"product_code":     productCode,
		})
	}
	if match := windowsProductCodeInText.FindString(uninstall); match != "" {
		return supportedSoftwareUninstall(map[string]any{
			"strategy":         "msi_product_code",
			"summary":          "Derived MSI uninstall from the registry uninstall string.",
			"rule_id":          "metadata_msi_guid",
			"uninstall_string": uninstall,
			"product_code":     strings.ToUpper(match),
		})
	}
	if quiet != "" {
		return supportedSoftwareUninstall(map[string]any{
			"strategy":               "direct_command",
			"summary":                "Uses the quiet uninstall string from inventory.",
			"rule_id":                "metadata_quiet_uninstall_string",
			"quiet_uninstall_string": quiet,
			"uninstall_string":       uninstall,
		})
	}
	if uninstall != "" && windowsQuietSwitchPattern.MatchString(uninstall) {
		return supportedSoftwareUninstall(map[string]any{
			"strategy":               "direct_command",
			"summary":                "The registry uninstall string already includes quiet flags.",
			"rule_id":                "metadata_quiet_flags",
			"quiet_uninstall_string": uninstall,
			"uninstall_string":       uninstall,
		})
	}
	if uninstall != "" && strings.HasPrefix(strings.ToLower(filepathBase(firstCommandToken(uninstall))), "unins") {
		return supportedSoftwareUninstall(map[string]any{
			"strategy":               "direct_command",
			"summary":                "Derived Inno Setup silent uninstall.",
			"rule_id":                "builtin_inno_uninstall",
			"quiet_uninstall_string": strings.TrimSpace(uninstall + " /VERYSILENT /SUPPRESSMSGBOXES /NORESTART"),
			"uninstall_string":       uninstall,
		})
	}
	if strings.TrimSpace(uninstall) != "" {
		_ = name
		_ = version
		return unsupportedSoftwareUninstall("Borealis could not derive a silent uninstall command for this software yet.")
	}
	return unsupportedSoftwareUninstall("This software row does not expose a usable uninstall command yet.")
}

func supportedSoftwareUninstall(fields map[string]any) map[string]any {
	result := map[string]any{
		"supported": true,
		"reason":    "",
	}
	for key, value := range fields {
		result[key] = value
	}
	return result
}

func unsupportedSoftwareUninstall(reason string) map[string]any {
	return map[string]any{
		"supported": false,
		"reason":    reason,
		"summary":   "",
	}
}

func parseSoftwareBoolish(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes", "on":
			return true
		}
	case float64:
		return typed != 0
	case int:
		return typed != 0
	case int64:
		return typed != 0
	}
	return false
}

func firstCommandToken(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	if strings.HasPrefix(command, `"`) {
		rest := strings.TrimPrefix(command, `"`)
		idx := strings.Index(rest, `"`)
		if idx >= 0 {
			return rest[:idx]
		}
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func filepathBase(path string) string {
	path = strings.TrimSpace(strings.ReplaceAll(path, `\`, "/"))
	if path == "" {
		return ""
	}
	parts := strings.Split(path, "/")
	return parts[len(parts)-1]
}
