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
		"install_date":                  {},
		"install_date_confidence":       {},
		"install_date_source":           {},
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
	steamUninstallProtocol    = regexp.MustCompile(`(?i)\bsteam://uninstall/(\d+)\b`)
	steamLibraryPathPattern   = regexp.MustCompile(`(?i)(^|[\\/])steamapps[\\/]+common([\\/]|$)`)

	windowsSoftwareUninstallRules = []map[string]any{
		{
			"rule_id":                "7zip_uninstall_silent",
			"source":                 "local_installed",
			"publisher_contains_any": []any{"igor pavlov"},
			"name_contains_any":      []any{"7-zip"},
			"exe_names":              []any{"uninstall.exe"},
			"append_args":            []any{"/S"},
			"summary":                "7-Zip uninstall supports /S.",
		},
		{
			"rule_id":                "mozilla_helper_silent",
			"source":                 "local_installed",
			"publisher_contains_any": []any{"mozilla", "betterbird project"},
			"exe_names":              []any{"helper.exe"},
			"append_args":            []any{"/S"},
			"summary":                "Mozilla helper.exe supports /S silent uninstall.",
		},
		{
			"rule_id":                "irfanview_uninstall_silent",
			"source":                 "local_installed",
			"publisher_contains_any": []any{"irfan skiljan"},
			"name_contains_any":      []any{"irfanview"},
			"exe_names":              []any{"iv_uninstall.exe"},
			"append_args":            []any{"/silent"},
			"summary":                "IrfanView uninstall supports /silent.",
		},
		{
			"rule_id":                "edge_setup_force_uninstall",
			"source":                 "local_installed",
			"publisher_contains_any": []any{"microsoft corporation"},
			"name_contains_any":      []any{"microsoft edge", "webview2 runtime"},
			"exe_names":              []any{"setup.exe"},
			"uninstall_contains_any": []any{"--uninstall", "--msedge", "--msedgewebview"},
			"append_args":            []any{"--force-uninstall"},
			"summary":                "Edge setup.exe uninstall can be forced silent.",
		},
		{
			"rule_id":                "chrome_setup_force_uninstall",
			"source":                 "local_installed",
			"publisher_contains_any": []any{"google llc"},
			"name_contains_any":      []any{"google chrome"},
			"exe_names":              []any{"setup.exe"},
			"uninstall_contains_any": []any{"--uninstall"},
			"append_args":            []any{"--force-uninstall"},
			"summary":                "Chrome setup.exe uninstall can be forced silent.",
		},
	}
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
	entry := softwareUninstallEntry(name, version, source, metadata)
	if normalizeSoftwareSource(source) == "windows_store" {
		packageFamily := cleanText(metadata["package_family_name"])
		if parseSoftwareBoolish(metadata["non_removable"]) {
			return unsupportedSoftwareUninstall("Windows marks this Store package as non-removable.")
		}
		if packageFamily == "" {
			return unsupportedSoftwareUninstall("This Windows Store entry does not include enough package metadata yet.")
		}
		return supportedSoftwareUninstall(map[string]any{
			"strategy":            "windows_store",
			"summary":             "Windows Store package uninstall.",
			"rule_id":             "metadata_windows_store_family_name",
			"package_family_name": packageFamily,
		})
	}
	if normalizeSoftwareSource(source) != "local_installed" {
		return unsupportedSoftwareUninstall("This software source is not part of the first Windows uninstall release.")
	}
	if softwareDistributionIsSteam(metadata) {
		return unsupportedSoftwareUninstall("Steam manages this title, and Borealis does not yet have a verified unattended uninstall path.")
	}

	quiet := cleanText(metadata["quiet_uninstall_string"])
	uninstall := cleanText(metadata["uninstall_string"])
	productCode := strings.ToUpper(cleanText(metadata["product_code"]))
	if blocked := matchBlockedQuietUninstall(entry); blocked != nil {
		return unsupportedSoftwareUninstall(firstText(cleanText(blocked["reason"]), "This uninstall command is blocked by operator policy."))
	}
	if override := matchWindowsUninstallOverride(entry); override != nil {
		if plan := planFromUninstallOverride(override, uninstall); plan != nil {
			return plan
		}
	}
	if quiet != "" {
		return supportedSoftwareUninstall(map[string]any{
			"strategy":               "direct_command",
			"summary":                "Uses the registry quiet uninstall string.",
			"rule_id":                "metadata_quiet_uninstall_string",
			"quiet_uninstall_string": canonicalizeWindowsCommand(quiet, nil),
			"uninstall_string":       uninstall,
			"product_code":           productCodeIfValid(productCode),
		})
	}
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
	parsed := splitWindowsCommandLine(uninstall)
	executableName := strings.ToLower(cleanText(parsed["executable_name"]))
	existingArgs := cleanText(parsed["arguments"])
	if uninstall != "" && windowsQuietSwitchPattern.MatchString(uninstall) {
		return supportedSoftwareUninstall(map[string]any{
			"strategy":               "direct_command",
			"summary":                "The registry uninstall string already includes quiet flags.",
			"rule_id":                "metadata_quiet_flags",
			"quiet_uninstall_string": canonicalizeWindowsCommand(uninstall, nil),
			"uninstall_string":       uninstall,
		})
	}
	if len(parsed) > 0 && strings.HasPrefix(executableName, "unins") {
		return supportedSoftwareUninstall(map[string]any{
			"strategy":               "direct_command",
			"summary":                "Derived Inno Setup silent uninstall.",
			"rule_id":                "builtin_inno_uninstall",
			"quiet_uninstall_string": buildWindowsCommand(parsed["file_path"], existingArgs, []string{"/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART"}),
			"uninstall_string":       uninstall,
		})
	}
	if len(parsed) > 0 && executableName == "update.exe" {
		extra := []string{}
		if !optionPresent(existingArgs, "--uninstall") {
			extra = append(extra, "--uninstall")
		}
		if !optionPresent(existingArgs, "--silent") && !optionPresent(existingArgs, "--quiet") {
			extra = append(extra, "--silent")
		}
		return supportedSoftwareUninstall(map[string]any{
			"strategy":               "direct_command",
			"summary":                "Derived Squirrel-style silent uninstall.",
			"rule_id":                "builtin_squirrel_update",
			"quiet_uninstall_string": buildWindowsCommand(parsed["file_path"], existingArgs, extra),
			"uninstall_string":       uninstall,
		})
	}
	if len(parsed) > 0 {
		for _, rule := range windowsSoftwareUninstallRules {
			if !softwareMatchesRule(entry, rule, executableName, uninstall, existingArgs) {
				continue
			}
			extra := cleanStringList(rule["append_args"])
			missing := []string{}
			for _, arg := range extra {
				argText := cleanText(arg)
				if !optionPresent(existingArgs, argText) {
					missing = append(missing, argText)
				}
			}
			return supportedSoftwareUninstall(map[string]any{
				"strategy":               "direct_command",
				"summary":                firstText(cleanText(rule["summary"]), "Derived uninstall command from the Borealis rule catalog."),
				"rule_id":                cleanText(rule["rule_id"]),
				"quiet_uninstall_string": buildWindowsCommand(parsed["file_path"], existingArgs, missing),
				"uninstall_string":       uninstall,
			})
		}
	}
	if plan := resolveWindowsInstallLocationRule(entry, metadata); plan != nil {
		return plan
	}
	if strings.TrimSpace(uninstall) != "" {
		return unsupportedSoftwareUninstall("Borealis could not derive a silent uninstall command for this software yet.")
	}
	return unsupportedSoftwareUninstall("This software row does not expose a usable uninstall command yet.")
}

func softwareUninstallEntry(name string, version string, source string, metadata map[string]any) map[string]any {
	return map[string]any{
		"name":     cleanText(name),
		"version":  cleanText(version),
		"source":   normalizeSoftwareSource(source),
		"metadata": metadata,
	}
}

func softwareDistributionIsSteam(metadata map[string]any) bool {
	uninstall := cleanText(metadata["uninstall_string"])
	installLocation := trimWindowsPath(metadata["install_location"])
	return steamUninstallProtocol.MatchString(uninstall) || steamLibraryPathPattern.MatchString(installLocation)
}

func matchBlockedQuietUninstall(entry map[string]any) map[string]any {
	matches := findMatchingUninstallBlocklistRules(entry)
	if len(matches) == 0 {
		return nil
	}
	return matches[0]
}

func matchWindowsUninstallOverride(entry map[string]any) map[string]any {
	metadata := softwareMetadata(entry)
	quiet := cleanText(metadata["quiet_uninstall_string"])
	uninstall := cleanText(metadata["uninstall_string"])
	parsed := splitWindowsCommandLine(firstText(quiet, uninstall))
	executableName := cleanText(parsed["executable_name"])
	arguments := cleanText(parsed["arguments"])
	payload := loadSoftwareRuleFile(softwareUninstallOverridesPath(), "windows_uninstall_overrides")
	for _, rule := range ruleRows(payload["windows_uninstall_overrides"]) {
		if softwareMatchesRule(entry, rule, executableName, firstText(quiet, uninstall), arguments) {
			return rule
		}
	}
	return nil
}

func planFromUninstallOverride(rule map[string]any, fallbackUninstall string) map[string]any {
	strategy := strings.ToLower(firstText(cleanText(rule["strategy"]), "direct_command"))
	ruleID := cleanText(rule["rule_id"])
	summary := firstText(cleanText(rule["summary"]), "Uses a custom uninstall override.")
	uninstall := firstText(cleanText(rule["uninstall_string"]), fallbackUninstall)
	switch strategy {
	case "direct_command":
		quiet := cleanText(rule["quiet_uninstall_string"])
		if quiet == "" {
			return nil
		}
		return supportedSoftwareUninstall(map[string]any{
			"strategy":               "direct_command",
			"summary":                summary,
			"rule_id":                ruleID,
			"quiet_uninstall_string": canonicalizeWindowsCommand(quiet, nil),
			"uninstall_string":       uninstall,
			"product_code":           productCodeIfValid(rule["product_code"]),
		})
	case "msi_product_code":
		productCode := productCodeIfValid(rule["product_code"])
		if productCode == "" {
			return nil
		}
		return supportedSoftwareUninstall(map[string]any{
			"strategy":         "msi_product_code",
			"summary":          summary,
			"rule_id":          ruleID,
			"uninstall_string": uninstall,
			"product_code":     productCode,
		})
	case "windows_store", "windows_store_package":
		packageFamily := cleanText(rule["package_family_name"])
		if packageFamily == "" {
			return nil
		}
		return supportedSoftwareUninstall(map[string]any{
			"strategy":            "windows_store",
			"summary":             summary,
			"rule_id":             ruleID,
			"package_family_name": packageFamily,
		})
	default:
		return nil
	}
}

func productCodeIfValid(value any) string {
	productCode := strings.ToUpper(cleanText(value))
	if productCode != "" && windowsProductCodePattern.MatchString(productCode) {
		return productCode
	}
	return ""
}

func resolveWindowsInstallLocationRule(entry map[string]any, metadata map[string]any) map[string]any {
	name := cleanText(entry["name"])
	nameLower := strings.ToLower(name)
	publisher := strings.ToLower(cleanText(metadata["publisher"]))
	installLocation := trimWindowsPath(metadata["install_location"])
	version := cleanText(entry["version"])
	if installLocation != "" && strings.Contains(publisher, "igor pavlov") && strings.Contains(nameLower, "7-zip") {
		return supportedSoftwareUninstall(map[string]any{
			"strategy":               "direct_command",
			"summary":                "Derived 7-Zip uninstall from install location.",
			"rule_id":                "install_location_7zip",
			"quiet_uninstall_string": buildWindowsCommand(joinWindowsPath(installLocation, "Uninstall.exe"), "", []string{"/S"}),
		})
	}
	if installLocation != "" && strings.Contains(publisher, "betterbird project") && strings.Contains(nameLower, "betterbird") {
		return supportedSoftwareUninstall(map[string]any{
			"strategy":               "direct_command",
			"summary":                "Derived Betterbird uninstall from install location.",
			"rule_id":                "install_location_betterbird_helper",
			"quiet_uninstall_string": buildWindowsCommand(joinWindowsPath(installLocation, "uninstall", "helper.exe"), "", []string{"/S"}),
		})
	}
	if installLocation != "" && strings.Contains(publisher, "mozilla") && strings.Contains(nameLower, "firefox") {
		return supportedSoftwareUninstall(map[string]any{
			"strategy":               "direct_command",
			"summary":                "Derived Firefox uninstall from install location.",
			"rule_id":                "install_location_firefox_helper",
			"quiet_uninstall_string": buildWindowsCommand(joinWindowsPath(installLocation, "uninstall", "helper.exe"), "", []string{"/S"}),
		})
	}
	if installLocation != "" && strings.Contains(publisher, "irfan skiljan") && strings.Contains(nameLower, "irfanview") {
		return supportedSoftwareUninstall(map[string]any{
			"strategy":               "direct_command",
			"summary":                "Derived IrfanView uninstall from install location.",
			"rule_id":                "install_location_irfanview",
			"quiet_uninstall_string": buildWindowsCommand(joinWindowsPath(installLocation, "iv_uninstall.exe"), "", []string{"/silent"}),
		})
	}
	if installLocation != "" && strings.Contains(publisher, "microsoft corporation") && version != "" {
		if strings.Contains(nameLower, "microsoft edge webview2 runtime") {
			return supportedSoftwareUninstall(map[string]any{
				"strategy":               "direct_command",
				"summary":                "Derived WebView2 uninstall from install location and version.",
				"rule_id":                "install_location_edge_webview_setup",
				"quiet_uninstall_string": buildWindowsCommand(joinWindowsPath(installLocation, version, "Installer", "setup.exe"), "", []string{"--uninstall", "--msedgewebview", "--system-level", "--force-uninstall"}),
			})
		}
		if nameLower == "microsoft edge" {
			return supportedSoftwareUninstall(map[string]any{
				"strategy":               "direct_command",
				"summary":                "Derived Edge uninstall from install location and version.",
				"rule_id":                "install_location_edge_setup",
				"quiet_uninstall_string": buildWindowsCommand(joinWindowsPath(installLocation, version, "Installer", "setup.exe"), "", []string{"--uninstall", "--msedge", "--system-level", "--force-uninstall"}),
			})
		}
	}
	return nil
}

func joinWindowsPath(base any, parts ...string) string {
	normalizedBase := trimWindowsPath(base)
	cleanParts := []string{}
	for _, part := range parts {
		text := strings.Trim(cleanText(part), `\/`)
		if text != "" {
			cleanParts = append(cleanParts, text)
		}
	}
	if normalizedBase == "" {
		return strings.Join(cleanParts, `\`)
	}
	if len(cleanParts) == 0 {
		return normalizedBase
	}
	return normalizedBase + `\` + strings.Join(cleanParts, `\`)
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
