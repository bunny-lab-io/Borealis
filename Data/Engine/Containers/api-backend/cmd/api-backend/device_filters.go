package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	filterSiteModeGlobal     = "global"
	filterSiteModeSpecific   = "specific_sites"
	filterSiteModeExclusions = "global_exclusions"
)

var (
	filterTextOperators = []string{"contains", "does_not_contain", "equals", "begins_with", "ends_with"}
	filterNumOperators  = []string{"equals", "greater_than", "greater_than_or_equal", "less_than", "less_than_or_equal"}
	filterFields        = []map[string]any{
		{"value": "hostname", "label": "Hostname", "kind": "text", "operators": filterTextOperators, "supports_regex": true},
		{"value": "description", "label": "Description", "kind": "text", "operators": filterTextOperators, "supports_regex": true},
		{"value": "site_name", "label": "Site", "kind": "text", "operators": filterTextOperators, "supports_regex": true},
		{"value": "operating_system", "label": "Operating System", "kind": "text", "operators": filterTextOperators, "supports_regex": true},
		{"value": "device_type", "label": "Device Type", "kind": "text", "operators": filterTextOperators, "supports_regex": true},
		{"value": "status", "label": "Status", "kind": "enum", "operators": []string{"equals"}, "supports_regex": false},
		{"value": "last_seen_age_minutes", "label": "Last Seen Age (Minutes)", "kind": "number", "operators": filterNumOperators, "supports_regex": false},
		{"value": "last_user", "label": "Last User", "kind": "text", "operators": filterTextOperators, "supports_regex": true},
		{"value": "internal_ip", "label": "Internal IP", "kind": "text", "operators": filterTextOperators, "supports_regex": true},
		{"value": "external_ip", "label": "External IP", "kind": "text", "operators": filterTextOperators, "supports_regex": true},
		{"value": "domain", "label": "Domain", "kind": "text", "operators": filterTextOperators, "supports_regex": true},
		{"value": "total_ram_gb", "label": "Total RAM (GB)", "kind": "number", "operators": filterNumOperators, "supports_regex": false},
		{"value": "storage_free_percent", "label": "Storage Free %", "kind": "number", "operators": filterNumOperators, "supports_regex": false},
		{"value": "cpu_model", "label": "CPU Model", "kind": "text", "operators": filterTextOperators, "supports_regex": true},
		{"value": "agent_version", "label": "Agent Version", "kind": "text", "operators": filterTextOperators, "supports_regex": true},
		{"value": "installed_software", "label": "Installed Software", "kind": "software", "operators": filterTextOperators, "supports_regex": true, "supports_source": true, "supports_version": true},
		{"value": "metadata_field", "label": "Metadata Field", "kind": "metadata_field", "operators": filterTextOperators, "supports_regex": true, "supports_field_picker": true},
	}
	filterFieldByID = buildFilterFieldLookup()
)

type deviceFilterStore interface {
	listDeviceFilters(ctx context.Context, profile operatorProfile, archived bool, selectedSiteID *int64) ([]map[string]any, error)
	deviceFilterMetadata(ctx context.Context) (map[string]any, error)
	searchDeviceFilters(ctx context.Context, profile operatorProfile, query string) ([]map[string]any, error)
	previewDeviceFilter(ctx context.Context, profile operatorProfile, body map[string]any) (map[string]any, int, error)
	getDeviceFilter(ctx context.Context, profile operatorProfile, filterID int64) (map[string]any, bool, error)
	getDeviceFilterUsage(ctx context.Context, profile operatorProfile, filterID int64) (map[string]any, bool, error)
	createDeviceFilter(ctx context.Context, profile operatorProfile, body map[string]any) (map[string]any, int, error)
	updateDeviceFilter(ctx context.Context, profile operatorProfile, filterID int64, body map[string]any) (map[string]any, int, error)
	cloneDeviceFilter(ctx context.Context, profile operatorProfile, filterID int64) (map[string]any, int, error)
	archiveDeviceFilter(ctx context.Context, profile operatorProfile, filterID int64, archived bool) (map[string]any, int, error)
	deleteDeviceFilter(ctx context.Context, profile operatorProfile, filterID int64) (map[string]any, int, error)
}

type deviceFilterRow struct {
	ID                   sql.NullInt64
	Name                 sql.NullString
	Description          sql.NullString
	Archived             sql.NullInt64
	CriteriaMode         sql.NullString
	SiteMode             sql.NullString
	BasicCriteriaJSON    sql.NullString
	AdvancedCriteriaJSON sql.NullString
	LastEditedBy         sql.NullString
	CreatedAt            sql.NullInt64
	UpdatedAt            sql.NullInt64
}

type filterDeviceRow struct {
	GUID               sql.NullString
	Hostname           sql.NullString
	Description        sql.NullString
	CreatedAt          sql.NullInt64
	AgentHash          sql.NullString
	MemoryJSON         sql.NullString
	NetworkJSON        sql.NullString
	SoftwareJSON       sql.NullString
	StorageJSON        sql.NullString
	CPUJSON            sql.NullString
	DeviceType         sql.NullString
	Domain             sql.NullString
	ExternalIP         sql.NullString
	InternalIP         sql.NullString
	LastReboot         sql.NullString
	LastSeen           sql.NullInt64
	LastUser           sql.NullString
	OperatingSystem    sql.NullString
	Uptime             sql.NullInt64
	AgentID            sql.NullString
	SecurityStatus     sql.NullString
	ConnectionType     sql.NullString
	ConnectionEndpoint sql.NullString
	SiteID             sql.NullInt64
	SiteName           sql.NullString
	SiteDescription    sql.NullString
}

func registerDeviceFilterRoutes(mux *http.ServeMux, auth *authService) {
	mux.HandleFunc("/api/device_filters", deviceFiltersRootHandler(auth))
	mux.HandleFunc("GET /api/device_filters/metadata", deviceFilterMetadataHandler(auth))
	mux.HandleFunc("GET /api/device_filters/search", deviceFilterSearchHandler(auth))
	mux.HandleFunc("POST /api/device_filters/preview", deviceFilterPreviewHandler(auth))
	mux.HandleFunc("/api/device_filters/", deviceFilterIDHandler(auth))
}

func deviceFiltersRootHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleDeviceFilterList(w, r, auth)
		case http.MethodPost:
			handleDeviceFilterCreate(w, r, auth)
		default:
			writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
		}
	}
}

func deviceFilterMetadataHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		profile, failure := requireDeviceFilterProfile(w, r, auth)
		if failure != nil {
			failure.write(w)
			return
		}
		_ = profile
		store, ok := auth.store.(deviceFilterStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "device_filters_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		payload, err := store.deviceFilterMetadata(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, payload)
	}
}

func deviceFilterSearchHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		profile, failure := requireDeviceFilterProfile(w, r, auth)
		if failure != nil {
			failure.write(w)
			return
		}
		query := strings.TrimSpace(r.URL.Query().Get("query"))
		if query == "" {
			query = strings.TrimSpace(r.URL.Query().Get("name"))
		}
		if len(query) < 3 {
			writeJSON(w, http.StatusOK, map[string]any{"filters": []map[string]any{}, "query": query, "count": 0})
			return
		}
		store, ok := auth.store.(deviceFilterStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "device_filters_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		filters, err := store.searchDeviceFilters(ctx, profile, query)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"filters": filters, "query": query, "count": len(filters)})
	}
}

func deviceFilterPreviewHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		profile, failure := requireDeviceFilterProfile(w, r, auth)
		if failure != nil {
			failure.write(w)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, http.ErrBodyReadAfterClose) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
			return
		}
		store, ok := auth.store.(deviceFilterStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "device_filters_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		payload, status, err := store.previewDeviceFilter(ctx, profile, body)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, status, payload)
	}
}

func deviceFilterIDHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		profile, failure := requireDeviceFilterProfile(w, r, auth)
		if failure != nil {
			failure.write(w)
			return
		}
		filterID, action, ok := parseDeviceFilterAction(r.URL.Path)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}
		store, ok := auth.store.(deviceFilterStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "device_filters_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()

		switch {
		case action == "" && r.Method == http.MethodGet:
			filter, found, err := store.getDeviceFilter(ctx, profile, filterID)
			writeFilterFoundResponse(w, filter, found, err, "filter")
		case action == "" && r.Method == http.MethodPut:
			body, err := readJSONMap(r)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
				return
			}
			payload, status, err := store.updateDeviceFilter(ctx, profile, filterID, body)
			writeFilterMutationResponse(w, payload, status, err)
		case action == "" && r.Method == http.MethodDelete:
			payload, status, err := store.deleteDeviceFilter(ctx, profile, filterID)
			writeFilterMutationResponse(w, payload, status, err)
		case action == "usage" && r.Method == http.MethodGet:
			usage, found, err := store.getDeviceFilterUsage(ctx, profile, filterID)
			writeFilterFoundResponse(w, usage, found, err, "usage")
		case action == "clone" && r.Method == http.MethodPost:
			payload, status, err := store.cloneDeviceFilter(ctx, profile, filterID)
			writeFilterMutationResponse(w, payload, status, err)
		case action == "archive" && r.Method == http.MethodPost:
			payload, status, err := store.archiveDeviceFilter(ctx, profile, filterID, true)
			writeFilterMutationResponse(w, payload, status, err)
		case action == "unarchive" && r.Method == http.MethodPost:
			payload, status, err := store.archiveDeviceFilter(ctx, profile, filterID, false)
			writeFilterMutationResponse(w, payload, status, err)
		default:
			writeMethodNotAllowed(w, "GET, PUT, DELETE, POST")
		}
	}
}

func handleDeviceFilterList(w http.ResponseWriter, r *http.Request, auth *authService) {
	profile, failure := requireDeviceFilterProfile(w, r, auth)
	if failure != nil {
		failure.write(w)
		return
	}
	archived := parseTruthy(r.URL.Query().Get("archived"))
	var selectedSiteID *int64
	siteRaw := strings.TrimSpace(firstText(r.URL.Query().Get("site"), r.URL.Query().Get("site_id")))
	if siteRaw != "" {
		if parsed, err := strconv.ParseInt(siteRaw, 10, 64); err == nil && parsed > 0 {
			selectedSiteID = &parsed
		}
	}
	store, ok := auth.store.(deviceFilterStore)
	if !ok {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "device_filters_unavailable"})
		return
	}
	ctx, cancel := requestTimeout(r.Context(), auth)
	defer cancel()
	filters, err := store.listDeviceFilters(ctx, profile, archived, selectedSiteID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"filters": filters, "archived": archived})
}

func handleDeviceFilterCreate(w http.ResponseWriter, r *http.Request, auth *authService) {
	profile, failure := requireDeviceFilterProfile(w, r, auth)
	if failure != nil {
		failure.write(w)
		return
	}
	body, err := readJSONMap(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
		return
	}
	store, ok := auth.store.(deviceFilterStore)
	if !ok {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "device_filters_unavailable"})
		return
	}
	ctx, cancel := requestTimeout(r.Context(), auth)
	defer cancel()
	payload, status, err := store.createDeviceFilter(ctx, profile, body)
	writeFilterMutationResponse(w, payload, status, err)
}

func requireDeviceFilterProfile(_ http.ResponseWriter, r *http.Request, auth *authService) (operatorProfile, *authFailure) {
	profile, err := auth.currentProfile(r.Context(), r)
	if err == nil {
		return profile, nil
	}
	if isUnauthorizedAuthError(err) {
		return operatorProfile{}, unauthorizedAuthFailure()
	}
	return operatorProfile{}, &authFailure{status: http.StatusBadGateway, body: map[string]any{"error": "auth_unavailable", "detail": err.Error()}}
}

func parseDeviceFilterAction(path string) (int64, string, bool) {
	rest := strings.Trim(strings.TrimPrefix(path, "/api/device_filters/"), "/")
	if rest == "" {
		return 0, "", false
	}
	parts := strings.Split(rest, "/")
	filterID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || filterID <= 0 {
		return 0, "", false
	}
	action := ""
	if len(parts) > 1 {
		action = strings.TrimSpace(parts[1])
	}
	if len(parts) > 2 {
		return 0, "", false
	}
	return filterID, action, true
}

func readJSONMap(r *http.Request) (map[string]any, error) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, http.ErrBodyReadAfterClose) {
		return nil, err
	}
	if body == nil {
		body = map[string]any{}
	}
	return body, nil
}

func writeFilterFoundResponse(w http.ResponseWriter, payload map[string]any, found bool, err error, key string) {
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Filter not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{key: payload})
}

func writeFilterMutationResponse(w http.ResponseWriter, payload map[string]any, status int, err error) {
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if status <= 0 {
		status = http.StatusOK
	}
	writeJSON(w, status, payload)
}

func (s *postgresOperatorStore) deviceFilterMetadata(ctx context.Context) (map[string]any, error) {
	metadataFields, err := s.listMetadataDefinitions(ctx)
	if err != nil {
		metadataFields = buildMetadataDefinitions(map[int]metadataDefinitionRow{})
	}
	return map[string]any{
		"site_modes": []map[string]any{
			{"value": filterSiteModeGlobal, "label": "Global"},
			{"value": filterSiteModeSpecific, "label": "Specific Sites"},
			{"value": filterSiteModeExclusions, "label": "Global w/ Exclusions"},
		},
		"fields": filterFields,
		"operators": map[string]any{
			"text": []map[string]any{
				{"value": "contains", "label": "Contains"},
				{"value": "does_not_contain", "label": "Does Not Contain"},
				{"value": "equals", "label": "Equals"},
				{"value": "begins_with", "label": "Begins With"},
				{"value": "ends_with", "label": "Ends With"},
			},
			"number": []map[string]any{
				{"value": "equals", "label": "Equals"},
				{"value": "greater_than", "label": "Greater Than"},
				{"value": "greater_than_or_equal", "label": "Greater Than or Equal"},
				{"value": "less_than", "label": "Less Than"},
				{"value": "less_than_or_equal", "label": "Less Than or Equal"},
			},
			"enum": []map[string]any{
				{"value": "equals", "label": "Equals"},
			},
			"software_version": []map[string]any{
				{"value": "matches", "label": "Matches"},
				{"value": "older_than", "label": "Older Than"},
				{"value": "newer_than", "label": "Newer Than"},
			},
		},
		"software_sources": []map[string]any{
			{"value": "local_installed", "label": "Locally Installed"},
			{"value": "windows_store", "label": "Windows Store"},
			{"value": "dpkg", "label": "DPKG"},
			{"value": "rpm", "label": "RPM"},
		},
		"metadata_fields": metadataFields,
	}, nil
}

func (s *postgresOperatorStore) listDeviceFilters(ctx context.Context, profile operatorProfile, archived bool, selectedSiteID *int64) ([]map[string]any, error) {
	if selectedSiteID != nil {
		if ok, err := s.profileCanAccessSite(ctx, profile, *selectedSiteID); err != nil || !ok {
			return []map[string]any{}, err
		}
	}
	ids, err := s.loadOrderedDeviceFilterIDs(ctx, &archived)
	if err != nil {
		return nil, err
	}
	records, err := s.loadDeviceFilters(ctx, ids, true)
	if err != nil {
		return nil, err
	}
	allSiteIDs, err := s.allSiteIDs(ctx)
	if err != nil {
		return nil, err
	}
	visible, err := s.filterVisibleRecords(ctx, profile, ids, records, allSiteIDs)
	if err != nil {
		return nil, err
	}
	if selectedSiteID != nil && len(visible) > 0 {
		devices, _ := s.fetchFilterDevices(ctx, profile)
		scoped := make([]map[string]any, 0, len(visible))
		for _, record := range visible {
			matches := matchFilterDevices(record, devices)
			for _, device := range matches {
				if int64FromAny(device["site_id"]) != nil && *int64FromAny(device["site_id"]) == *selectedSiteID {
					scoped = append(scoped, record)
					break
				}
			}
		}
		visible = scoped
	}
	if err := s.attachDeviceFilterCountsAndUsage(ctx, profile, visible); err != nil {
		return nil, err
	}
	return visible, nil
}

func (s *postgresOperatorStore) searchDeviceFilters(ctx context.Context, profile operatorProfile, query string) ([]map[string]any, error) {
	archived := false
	ids, err := s.loadOrderedDeviceFilterIDs(ctx, &archived)
	if err != nil {
		return nil, err
	}
	records, err := s.loadDeviceFilters(ctx, ids, true)
	if err != nil {
		return nil, err
	}
	allSiteIDs, err := s.allSiteIDs(ctx)
	if err != nil {
		return nil, err
	}
	visible, err := s.filterVisibleRecords(ctx, profile, ids, records, allSiteIDs)
	if err != nil {
		return nil, err
	}
	devices, _ := s.fetchFilterDevices(ctx, profile)
	queryLC := strings.ToLower(strings.TrimSpace(query))
	matches := make([]map[string]any, 0)
	for _, record := range visible {
		name := cleanText(record["name"])
		if name == "" || !strings.Contains(strings.ToLower(name), queryLC) {
			continue
		}
		item := map[string]any{
			"id":                    record["id"],
			"name":                  name,
			"description":           cleanText(record["description"]),
			"site_mode":             cleanText(record["site_mode"]),
			"site_ids":              record["site_ids"],
			"site_names":            record["site_names"],
			"scope_summary":         filterScopeSummary(record),
			"matching_device_count": len(matchFilterDevices(record, devices)),
		}
		matches = append(matches, item)
	}
	sort.SliceStable(matches, func(i, j int) bool {
		left := strings.ToLower(cleanText(matches[i]["name"]))
		right := strings.ToLower(cleanText(matches[j]["name"]))
		leftRank := filterSearchRank(left, queryLC)
		rightRank := filterSearchRank(right, queryLC)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return left < right
	})
	if len(matches) > 25 {
		matches = matches[:25]
	}
	return matches, nil
}

func (s *postgresOperatorStore) previewDeviceFilter(ctx context.Context, profile operatorProfile, body map[string]any) (map[string]any, int, error) {
	var draft map[string]any
	if rawID := firstPresentAny(body["filter_id"], body["id"]); rawID != nil && !filterBodyHasDraftCriteria(body) {
		filterID, ok := int64Value(rawID)
		if !ok {
			return map[string]any{"error": "Filter not found"}, http.StatusNotFound, nil
		}
		record, found, err := s.getDeviceFilter(ctx, profile, filterID)
		if err != nil || !found {
			return map[string]any{"error": "Filter not found"}, http.StatusNotFound, err
		}
		draft = record
	} else {
		draft = normalizeFilterRecord(body, nil, profile.Username)
		if ok, err := s.filterRecordVisibleToProfile(ctx, profile, draft); err != nil || !ok {
			if err != nil {
				return nil, 0, err
			}
			return map[string]any{"error": "out_of_scope_sites", "message": "One or more selected sites is outside your assigned site scope."}, http.StatusForbidden, nil
		}
	}
	if validation := validateFilterRecord(draft); len(validation) > 0 {
		return map[string]any{"error": "validation_failed", "validation_errors": validation}, http.StatusBadRequest, nil
	}
	devices, err := s.fetchFilterDevices(ctx, profile)
	if err != nil {
		return nil, 0, err
	}
	matches := matchFilterDevices(draft, devices)
	return map[string]any{"matched_device_count": len(matches), "devices": matches, "site_mode": draft["site_mode"]}, http.StatusOK, nil
}

func (s *postgresOperatorStore) getDeviceFilter(ctx context.Context, profile operatorProfile, filterID int64) (map[string]any, bool, error) {
	records, err := s.loadDeviceFilters(ctx, []int64{filterID}, true)
	if err != nil {
		return nil, false, err
	}
	record, ok := records[filterID]
	if !ok {
		return nil, false, nil
	}
	if ok, err := s.filterRecordVisibleToProfile(ctx, profile, record); err != nil || !ok {
		return nil, false, err
	}
	if err := s.attachDeviceFilterCountsAndUsage(ctx, profile, []map[string]any{record}); err != nil {
		return nil, false, err
	}
	return record, true, nil
}

func (s *postgresOperatorStore) getDeviceFilterUsage(ctx context.Context, profile operatorProfile, filterID int64) (map[string]any, bool, error) {
	record, found, err := s.getDeviceFilter(ctx, profile, filterID)
	if err != nil || !found {
		return nil, found, err
	}
	usage, _ := record["usage"].(map[string]any)
	if usage == nil {
		usage = map[string]any{"job_count": 0, "jobs": []map[string]any{}}
	}
	return usage, true, nil
}

func (s *postgresOperatorStore) createDeviceFilter(ctx context.Context, profile operatorProfile, body map[string]any) (map[string]any, int, error) {
	record := normalizeFilterRecord(body, nil, profile.Username)
	if validation := validateFilterRecord(record); len(validation) > 0 {
		return map[string]any{"error": "validation_failed", "validation_errors": validation}, http.StatusBadRequest, nil
	}
	if ok, err := s.filterRecordVisibleToProfile(ctx, profile, record); err != nil || !ok {
		if err != nil {
			return nil, 0, err
		}
		return map[string]any{"error": "out_of_scope_sites", "message": "One or more selected sites is outside your assigned site scope."}, http.StatusForbidden, nil
	}
	if msg, err := s.deviceFilterNameConflict(ctx, cleanText(record["name"]), nil); err != nil || msg != "" {
		return map[string]any{"error": "duplicate_name", "message": firstText(msg, "A filter with this name already exists.")}, http.StatusConflict, err
	}
	saved, status, err := s.writeDeviceFilter(ctx, profile, record, nil)
	if err != nil {
		return nil, 0, err
	}
	return map[string]any{"filter": saved}, status, nil
}

func (s *postgresOperatorStore) updateDeviceFilter(ctx context.Context, profile operatorProfile, filterID int64, body map[string]any) (map[string]any, int, error) {
	existing, found, err := s.getDeviceFilter(ctx, profile, filterID)
	if err != nil || !found {
		return map[string]any{"error": "Filter not found"}, http.StatusNotFound, err
	}
	record := normalizeFilterRecord(body, existing, profile.Username)
	if validation := validateFilterRecord(record); len(validation) > 0 {
		return map[string]any{"error": "validation_failed", "validation_errors": validation}, http.StatusBadRequest, nil
	}
	if ok, err := s.filterRecordVisibleToProfile(ctx, profile, record); err != nil || !ok {
		if err != nil {
			return nil, 0, err
		}
		return map[string]any{"error": "out_of_scope_sites", "message": "One or more selected sites is outside your assigned site scope."}, http.StatusForbidden, nil
	}
	if msg, err := s.deviceFilterNameConflict(ctx, cleanText(record["name"]), &filterID); err != nil || msg != "" {
		return map[string]any{"error": "duplicate_name", "message": firstText(msg, "A filter with this name already exists.")}, http.StatusConflict, err
	}
	saved, status, err := s.writeDeviceFilter(ctx, profile, record, &filterID)
	if err != nil {
		return nil, 0, err
	}
	return map[string]any{"filter": saved}, status, nil
}

func (s *postgresOperatorStore) cloneDeviceFilter(ctx context.Context, profile operatorProfile, filterID int64) (map[string]any, int, error) {
	existing, found, err := s.getDeviceFilter(ctx, profile, filterID)
	if err != nil || !found {
		return map[string]any{"error": "Filter not found"}, http.StatusNotFound, err
	}
	now := time.Now().Unix()
	clone := copyMap(existing)
	clone["id"] = nil
	clone["name"] = s.resolveDeviceFilterCloneName(ctx, cleanText(existing["name"]))
	clone["archived"] = false
	clone["last_edited_by"] = firstText(profile.Username, cleanText(existing["last_edited_by"]), "Unknown")
	clone["created_at"] = now
	clone["updated_at"] = now
	saved, status, err := s.writeDeviceFilter(ctx, profile, clone, nil)
	if err != nil {
		return nil, 0, err
	}
	return map[string]any{"filter": saved}, status, nil
}

func (s *postgresOperatorStore) archiveDeviceFilter(ctx context.Context, profile operatorProfile, filterID int64, archived bool) (map[string]any, int, error) {
	existing, found, err := s.getDeviceFilter(ctx, profile, filterID)
	if err != nil || !found {
		return map[string]any{"error": "Filter not found"}, http.StatusNotFound, err
	}
	if archived {
		if conflict, err := s.deviceFilterUsageConflict(ctx, profile, filterID); err != nil || conflict != nil {
			if err != nil {
				return nil, 0, err
			}
			return conflict, http.StatusConflict, nil
		}
	}
	existing["archived"] = archived
	existing["updated_at"] = time.Now().Unix()
	existing["last_edited_by"] = firstText(profile.Username, cleanText(existing["last_edited_by"]), "Unknown")
	saved, status, err := s.writeDeviceFilter(ctx, profile, existing, &filterID)
	if err != nil {
		return nil, 0, err
	}
	return map[string]any{"filter": saved}, status, nil
}

func (s *postgresOperatorStore) deleteDeviceFilter(ctx context.Context, profile operatorProfile, filterID int64) (map[string]any, int, error) {
	existing, found, err := s.getDeviceFilter(ctx, profile, filterID)
	if err != nil || !found {
		return map[string]any{"error": "Filter not found"}, http.StatusNotFound, err
	}
	_ = existing
	if conflict, err := s.deviceFilterUsageConflict(ctx, profile, filterID); err != nil || conflict != nil {
		if err != nil {
			return nil, 0, err
		}
		return conflict, http.StatusConflict, nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, 0, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM engine.device_filter_sites WHERE filter_id=$1", filterID); err != nil {
		_ = tx.Rollback()
		return nil, 0, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM engine.device_filters WHERE id=$1", filterID); err != nil {
		_ = tx.Rollback()
		return nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return map[string]any{"status": "ok"}, http.StatusOK, nil
}

func (s *postgresOperatorStore) loadOrderedDeviceFilterIDs(ctx context.Context, archived *bool) ([]int64, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	sqlText := "SELECT id FROM engine.device_filters"
	params := []any{}
	if archived != nil {
		sqlText += " WHERE COALESCE(archived,0)=$1"
		if *archived {
			params = append(params, 1)
		} else {
			params = append(params, 0)
		}
	}
	sqlText += " ORDER BY COALESCE(updated_at, created_at, 0) DESC, id DESC"
	rows, err := conn.QueryContext(ctx, sqlText, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id sql.NullInt64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if id.Valid {
			ids = append(ids, id.Int64)
		}
	}
	return ids, rows.Err()
}

func (s *postgresOperatorStore) loadDeviceFilters(ctx context.Context, filterIDs []int64, includeArchived bool) (map[int64]map[string]any, error) {
	if filterIDs != nil && len(filterIDs) == 0 {
		return map[int64]map[string]any{}, nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	sqlText := `
		SELECT id, name, description, archived, criteria_mode, site_mode,
		       basic_criteria_json, advanced_criteria_json, last_edited_by,
		       created_at, updated_at
		  FROM engine.device_filters
	`
	params := []any{}
	clauses := []string{}
	if filterIDs != nil {
		placeholders := make([]string, 0, len(filterIDs))
		for _, id := range filterIDs {
			params = append(params, id)
			placeholders = append(placeholders, "$"+strconv.Itoa(len(params)))
		}
		clauses = append(clauses, "id IN ("+strings.Join(placeholders, ",")+")")
	}
	if !includeArchived {
		clauses = append(clauses, "COALESCE(archived,0)=0")
	}
	if len(clauses) > 0 {
		sqlText += " WHERE " + strings.Join(clauses, " AND ")
	}
	rows, err := conn.QueryContext(ctx, sqlText, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := map[int64]map[string]any{}
	for rows.Next() {
		var row deviceFilterRow
		if err := rows.Scan(
			&row.ID, &row.Name, &row.Description, &row.Archived, &row.CriteriaMode, &row.SiteMode,
			&row.BasicCriteriaJSON, &row.AdvancedCriteriaJSON, &row.LastEditedBy, &row.CreatedAt, &row.UpdatedAt,
		); err != nil {
			return nil, err
		}
		id := nullInt(row.ID)
		record := normalizeFilterRecord(map[string]any{
			"id":                id,
			"name":              nullString(row.Name),
			"description":       nullString(row.Description),
			"archived":          truthyInt(row.Archived) == 1,
			"criteria_mode":     firstText(nullString(row.CriteriaMode), "advanced"),
			"site_mode":         firstText(nullString(row.SiteMode), filterSiteModeGlobal),
			"basic_criteria":    parseJSONObject(row.BasicCriteriaJSON),
			"advanced_criteria": parseJSONObject(row.AdvancedCriteriaJSON),
			"last_edited_by":    nullString(row.LastEditedBy),
			"created_at":        nullInt(row.CreatedAt),
			"updated_at":        nullInt(row.UpdatedAt),
			"site_ids":          []any{},
		}, nil, "")
		records[id] = record
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return records, nil
	}
	siteLookup, err := s.loadDeviceFilterSites(ctx, records)
	if err != nil {
		return nil, err
	}
	for id, record := range records {
		sites := siteLookup[id]
		siteIDs := make([]int64, 0, len(sites))
		siteNames := make([]string, 0, len(sites))
		for _, site := range sites {
			if siteID, ok := int64Value(site["id"]); ok {
				siteIDs = append(siteIDs, siteID)
			}
			name := cleanText(site["name"])
			if name != "" {
				siteNames = append(siteNames, name)
			}
		}
		record["site_ids"] = int64SliceToAny(siteIDs)
		record["site_names"] = stringSliceToAnyStrings(siteNames)
		record["sites"] = sites
	}
	return records, nil
}

func (s *postgresOperatorStore) loadDeviceFilterSites(ctx context.Context, records map[int64]map[string]any) (map[int64][]map[string]any, error) {
	ids := make([]int64, 0, len(records))
	for id := range records {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	params := make([]any, 0, len(ids))
	placeholders := make([]string, 0, len(ids))
	for _, id := range ids {
		params = append(params, id)
		placeholders = append(placeholders, "$"+strconv.Itoa(len(params)))
	}
	rows, err := conn.QueryContext(ctx, `
		SELECT dfs.filter_id, s.id, s.name
		  FROM engine.device_filter_sites AS dfs
		  JOIN engine.sites AS s ON s.id=dfs.site_id
		 WHERE dfs.filter_id IN (`+strings.Join(placeholders, ",")+`)
		 ORDER BY LOWER(s.name) ASC
	`, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	mapping := map[int64][]map[string]any{}
	for rows.Next() {
		var filterID, siteID sql.NullInt64
		var siteName sql.NullString
		if err := rows.Scan(&filterID, &siteID, &siteName); err != nil {
			return nil, err
		}
		if filterID.Valid {
			mapping[filterID.Int64] = append(mapping[filterID.Int64], map[string]any{"id": nullInt(siteID), "name": nullString(siteName)})
		}
	}
	return mapping, rows.Err()
}

func (s *postgresOperatorStore) filterVisibleRecords(ctx context.Context, profile operatorProfile, ids []int64, records map[int64]map[string]any, allSiteIDs []int64) ([]map[string]any, error) {
	visible := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		record, ok := records[id]
		if !ok {
			continue
		}
		ok, err := s.filterRecordVisibleToProfileWithAll(ctx, profile, record, allSiteIDs)
		if err != nil {
			return nil, err
		}
		if ok {
			visible = append(visible, record)
		}
	}
	return visible, nil
}

func (s *postgresOperatorStore) filterRecordVisibleToProfile(ctx context.Context, profile operatorProfile, record map[string]any) (bool, error) {
	allSiteIDs, err := s.allSiteIDs(ctx)
	if err != nil {
		return false, err
	}
	return s.filterRecordVisibleToProfileWithAll(ctx, profile, record, allSiteIDs)
}

func (s *postgresOperatorStore) filterRecordVisibleToProfileWithAll(ctx context.Context, profile operatorProfile, record map[string]any, allSiteIDs []int64) (bool, error) {
	allowed, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return false, err
	}
	if allowed == nil {
		return true, nil
	}
	effective := effectiveFilterSiteIDs(record, allSiteIDs)
	if len(effective) == 0 {
		return len(allowed) == 0, nil
	}
	allowedSet := int64Set(allowed)
	for _, siteID := range effective {
		if _, ok := allowedSet[siteID]; !ok {
			return false, nil
		}
	}
	return true, nil
}

func (s *postgresOperatorStore) allSiteIDs(ctx context.Context) ([]int64, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, "SELECT id FROM engine.sites ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id sql.NullInt64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if id.Valid {
			ids = append(ids, id.Int64)
		}
	}
	return ids, rows.Err()
}

func (s *postgresOperatorStore) profileCanAccessSite(ctx context.Context, profile operatorProfile, siteID int64) (bool, error) {
	allowed, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return false, err
	}
	if allowed == nil {
		return true, nil
	}
	for _, allowedID := range allowed {
		if allowedID == siteID {
			return true, nil
		}
	}
	return false, nil
}

func (s *postgresOperatorStore) attachDeviceFilterCountsAndUsage(ctx context.Context, profile operatorProfile, records []map[string]any) error {
	if len(records) == 0 {
		return nil
	}
	devices, _ := s.fetchFilterDevices(ctx, profile)
	filterIDs := make([]int64, 0, len(records))
	for _, record := range records {
		if id, ok := int64Value(record["id"]); ok {
			filterIDs = append(filterIDs, id)
		}
	}
	usageLookup, err := s.deviceFilterUsageLookup(ctx, profile, filterIDs)
	if err != nil {
		return err
	}
	for _, record := range records {
		record["matching_device_count"] = len(matchFilterDevices(record, devices))
		if id, ok := int64Value(record["id"]); ok {
			if usage, ok := usageLookup[id]; ok {
				record["usage"] = usage
				continue
			}
		}
		record["usage"] = map[string]any{"job_count": 0, "jobs": []map[string]any{}}
	}
	return nil
}

func (s *postgresOperatorStore) deviceFilterUsageLookup(ctx context.Context, profile operatorProfile, filterIDs []int64) (map[int64]map[string]any, error) {
	usage := map[int64]map[string]any{}
	for _, id := range filterIDs {
		usage[id] = map[string]any{"job_count": 0, "jobs": []map[string]any{}}
	}
	if len(filterIDs) == 0 {
		return usage, nil
	}
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, err
	}
	filterSet := int64Set(filterIDs)
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, "SELECT id, name, targets_json FROM engine.scheduled_jobs ORDER BY LOWER(name) ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var jobID sql.NullInt64
		var jobName, targetsJSON sql.NullString
		if err := rows.Scan(&jobID, &jobName, &targetsJSON); err != nil {
			return nil, err
		}
		targets := parseJSONArray(targetsJSON)
		fitsScope, err := filterUsageTargetsFitScope(ctx, conn, targets, allowedSiteIDs)
		if err != nil {
			return nil, err
		}
		if !fitsScope {
			continue
		}
		referenced := referencedFilterIDs(targets, filterSet)
		for id := range referenced {
			item := map[string]any{"id": nullInt(jobID), "name": firstText(nullString(jobName), fmt.Sprintf("Job %d", nullInt(jobID))), "path": fmt.Sprintf("/scheduling/job/%d", nullInt(jobID))}
			jobs, _ := usage[id]["jobs"].([]map[string]any)
			jobs = append(jobs, item)
			usage[id]["jobs"] = jobs
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for id, payload := range usage {
		jobs, _ := payload["jobs"].([]map[string]any)
		sort.SliceStable(jobs, func(i, j int) bool {
			return strings.ToLower(cleanText(jobs[i]["name"])) < strings.ToLower(cleanText(jobs[j]["name"]))
		})
		payload["jobs"] = jobs
		payload["job_count"] = len(jobs)
		usage[id] = payload
	}
	return usage, nil
}

func (s *postgresOperatorStore) deviceFilterUsageConflict(ctx context.Context, profile operatorProfile, filterID int64) (map[string]any, error) {
	lookup, err := s.deviceFilterUsageLookup(ctx, profile, []int64{filterID})
	if err != nil {
		return nil, err
	}
	usage := lookup[filterID]
	if int64FromAny(usage["job_count"]) != nil && *int64FromAny(usage["job_count"]) > 0 {
		return map[string]any{"error": "filter_in_use", "message": "This filter is referenced by scheduled jobs.", "jobs": usage["jobs"]}, nil
	}
	return nil, nil
}

func filterUsageTargetsFitScope(ctx context.Context, conn *sql.Conn, targets []any, allowedSiteIDs []int64) (bool, error) {
	if allowedSiteIDs == nil {
		return true, nil
	}
	if len(allowedSiteIDs) == 0 {
		return false, nil
	}
	allowedSet := int64Set(allowedSiteIDs)
	for _, target := range targets {
		targetScope, err := filterUsageTargetSiteScope(ctx, conn, target)
		if err != nil {
			return false, err
		}
		if targetScope == nil || len(targetScope) == 0 {
			return false, nil
		}
		for _, siteID := range targetScope {
			if _, ok := allowedSet[siteID]; !ok {
				return false, nil
			}
		}
	}
	return true, nil
}

func filterUsageTargetSiteScope(ctx context.Context, conn *sql.Conn, target any) ([]int64, error) {
	if entry, ok := target.(map[string]any); ok {
		kind := strings.ToLower(cleanText(firstPresentAny(entry["kind"], entry["type"])))
		if kind == "onboarding_scope" {
			siteID, ok := int64Value(firstPresentAny(entry["site_id"], entry["siteId"]))
			if !ok {
				return []int64{}, nil
			}
			return []int64{siteID}, nil
		}
		if kind == "filter" || firstPresentAny(entry["filter_id"], entry["id"]) != nil {
			if allowed := siteIDsFromAny(firstPresentAny(entry["allowed_site_ids"], entry["scope_site_ids"])); len(allowed) > 0 {
				return allowed, nil
			}
			filterID, ok := int64Value(firstPresentAny(entry["filter_id"], entry["id"]))
			if !ok {
				return []int64{}, nil
			}
			return filterUsageFilterSiteScope(ctx, conn, filterID)
		}
		if siteID, ok := int64Value(entry["site_id"]); ok {
			return []int64{siteID}, nil
		}
		return filterUsageLookupDeviceSite(ctx, conn, cleanText(entry["hostname"]), cleanText(firstPresentAny(entry["device_guid"], entry["guid"])), cleanText(entry["agent_id"]))
	}
	hostname := cleanText(target)
	if hostname == "" {
		return []int64{}, nil
	}
	return filterUsageLookupDeviceSite(ctx, conn, hostname, "", "")
}

func filterUsageFilterSiteScope(ctx context.Context, conn *sql.Conn, filterID int64) ([]int64, error) {
	var siteMode sql.NullString
	if err := conn.QueryRowContext(ctx, "SELECT site_mode FROM engine.device_filters WHERE id=$1", filterID).Scan(&siteMode); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return []int64{}, nil
		}
		return nil, err
	}
	rows, err := conn.QueryContext(ctx, "SELECT site_id FROM engine.device_filter_sites WHERE filter_id=$1 ORDER BY site_id", filterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	siteIDs := []int64{}
	for rows.Next() {
		var siteID sql.NullInt64
		if err := rows.Scan(&siteID); err != nil {
			return nil, err
		}
		if siteID.Valid {
			siteIDs = append(siteIDs, siteID.Int64)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if strings.EqualFold(nullString(siteMode), filterSiteModeGlobal) && len(siteIDs) == 0 {
		return nil, nil
	}
	return siteIDs, nil
}

func filterUsageLookupDeviceSite(ctx context.Context, conn *sql.Conn, hostname, guid, agentID string) ([]int64, error) {
	if hostname == "" && guid == "" && agentID == "" {
		return []int64{}, nil
	}
	where := ""
	param := ""
	if hostname != "" {
		where = "LOWER(d.hostname)=LOWER($1)"
		param = hostname
	} else if guid != "" {
		where = "LOWER(d.guid)=LOWER($1)"
		param = guid
	} else {
		where = "LOWER(d.agent_id)=LOWER($1)"
		param = agentID
	}
	var siteID sql.NullInt64
	err := conn.QueryRowContext(ctx, `
		SELECT ds.site_id
		  FROM engine.devices AS d
	 LEFT JOIN engine.device_sites AS ds ON ds.device_hostname=d.hostname
		 WHERE `+where+`
		 LIMIT 1
	`, param).Scan(&siteID)
	if errors.Is(err, sql.ErrNoRows) {
		return []int64{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !siteID.Valid {
		return []int64{}, nil
	}
	return []int64{siteID.Int64}, nil
}

func (s *postgresOperatorStore) deviceFilterNameConflict(ctx context.Context, name string, existingID *int64) (string, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return "", errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	var rows *sql.Rows
	if existingID == nil {
		rows, err = conn.QueryContext(ctx, "SELECT id FROM engine.device_filters WHERE LOWER(name)=LOWER($1)", name)
	} else {
		rows, err = conn.QueryContext(ctx, "SELECT id FROM engine.device_filters WHERE LOWER(name)=LOWER($1) AND id<>$2", name, *existingID)
	}
	if err != nil {
		return "", err
	}
	defer rows.Close()
	if rows.Next() {
		return "A filter with this name already exists.", nil
	}
	return "", rows.Err()
}

func (s *postgresOperatorStore) resolveDeviceFilterCloneName(ctx context.Context, sourceName string) string {
	prefix := "(Clone) " + firstText(strings.TrimSpace(sourceName), "Filter")
	candidate := prefix
	for suffix := 2; ; suffix++ {
		msg, err := s.deviceFilterNameConflict(ctx, candidate, nil)
		if err != nil || msg == "" {
			return candidate
		}
		candidate = fmt.Sprintf("%s %d", prefix, suffix)
	}
}

func (s *postgresOperatorStore) writeDeviceFilter(ctx context.Context, profile operatorProfile, record map[string]any, existingID *int64) (map[string]any, int, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, 0, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	now := time.Now().Unix()
	if int64FromAny(record["created_at"]) == nil || *int64FromAny(record["created_at"]) == 0 {
		record["created_at"] = now
	}
	record["updated_at"] = now
	record["last_edited_by"] = firstText(profile.Username, cleanText(record["last_edited_by"]), "Unknown")
	advancedJSON, _ := json.Marshal(firstPresentAny(record["advanced_criteria"], map[string]any{"groups": []map[string]any{}}))
	filterID := int64(0)
	status := http.StatusOK
	if existingID == nil {
		status = http.StatusCreated
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO engine.device_filters(
				name, description, archived, criteria_mode, site_mode,
				basic_criteria_json, advanced_criteria_json, last_edited_by, created_at, updated_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			RETURNING id
		`,
			cleanText(record["name"]),
			cleanText(record["description"]),
			boolInt(boolFromAny(record["archived"])),
			"advanced",
			cleanText(record["site_mode"]),
			`{"criteria":[]}`,
			string(advancedJSON),
			cleanText(record["last_edited_by"]),
			*int64FromAny(record["created_at"]),
			*int64FromAny(record["updated_at"]),
		).Scan(&filterID); err != nil {
			_ = tx.Rollback()
			return nil, 0, err
		}
	} else {
		filterID = *existingID
		if _, err := tx.ExecContext(ctx, `
			UPDATE engine.device_filters
			   SET name=$1, description=$2, archived=$3, criteria_mode=$4, site_mode=$5,
			       basic_criteria_json=$6, advanced_criteria_json=$7, last_edited_by=$8, updated_at=$9
			 WHERE id=$10
		`,
			cleanText(record["name"]),
			cleanText(record["description"]),
			boolInt(boolFromAny(record["archived"])),
			"advanced",
			cleanText(record["site_mode"]),
			`{"criteria":[]}`,
			string(advancedJSON),
			cleanText(record["last_edited_by"]),
			*int64FromAny(record["updated_at"]),
			filterID,
		); err != nil {
			_ = tx.Rollback()
			return nil, 0, err
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM engine.device_filter_sites WHERE filter_id=$1", filterID); err != nil {
		_ = tx.Rollback()
		return nil, 0, err
	}
	for _, siteID := range siteIDsFromAny(record["site_ids"]) {
		if _, err := tx.ExecContext(ctx, "INSERT INTO engine.device_filter_sites(filter_id, site_id) VALUES ($1,$2)", filterID, siteID); err != nil {
			_ = tx.Rollback()
			return nil, 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	saved, found, err := s.getDeviceFilter(ctx, profile, filterID)
	if err != nil {
		return nil, 0, err
	}
	if !found {
		return nil, 0, errors.New("saved filter could not be reloaded")
	}
	return saved, status, nil
}

func (s *postgresOperatorStore) fetchFilterDevices(ctx context.Context, profile operatorProfile) ([]map[string]any, error) {
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
	sqlText := `
		SELECT d.guid, d.hostname, d.description, d.created_at, d.agent_hash, d.memory, d.network, d.software,
		       d.storage, d.cpu, d.device_type, d.domain, d.external_ip, d.internal_ip, d.last_reboot,
		       d.last_seen, d.last_user, d.operating_system, d.uptime, d.agent_id, d.connection_type,
		       d.connection_endpoint, COALESCE(d.status, 'active'), s.id AS site_id, s.name AS site_name, s.description AS site_description
		  FROM engine.devices AS d
	 LEFT JOIN engine.device_sites AS ds ON ds.device_hostname=d.hostname
	 LEFT JOIN engine.sites AS s ON s.id=ds.site_id
	`
	params := []any{}
	if allowedSiteIDs != nil {
		placeholders := make([]string, 0, len(allowedSiteIDs))
		for _, siteID := range allowedSiteIDs {
			params = append(params, siteID)
			placeholders = append(placeholders, "$"+strconv.Itoa(len(params)))
		}
		sqlText += " WHERE ds.site_id IN (" + strings.Join(placeholders, ",") + ")"
	}
	rows, err := conn.QueryContext(ctx, sqlText, params...)
	if err != nil {
		return nil, err
	}
	rawRows := make([]filterDeviceRow, 0)
	guids := make([]string, 0)
	for rows.Next() {
		var row filterDeviceRow
		if err := rows.Scan(
			&row.GUID, &row.Hostname, &row.Description, &row.CreatedAt, &row.AgentHash, &row.MemoryJSON, &row.NetworkJSON, &row.SoftwareJSON,
			&row.StorageJSON, &row.CPUJSON, &row.DeviceType, &row.Domain, &row.ExternalIP, &row.InternalIP, &row.LastReboot,
			&row.LastSeen, &row.LastUser, &row.OperatingSystem, &row.Uptime, &row.AgentID, &row.ConnectionType,
			&row.ConnectionEndpoint, &row.SecurityStatus, &row.SiteID, &row.SiteName, &row.SiteDescription,
		); err != nil {
			_ = rows.Close()
			return nil, err
		}
		rawRows = append(rawRows, row)
		if guid := cleanText(row.GUID.String); guid != "" {
			guids = append(guids, guid)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	softwareMap, _ := loadFilterSoftwareByGUID(ctx, conn, guids)
	metadataMap, _ := loadFilterMetadataByGUID(ctx, conn, guids)
	devices := make([]map[string]any, 0, len(rawRows))
	for _, row := range rawRows {
		devices = append(devices, filterDevicePayload(row, softwareMap, metadataMap))
	}
	return devices, nil
}

func loadFilterSoftwareByGUID(ctx context.Context, conn *sql.Conn, guids []string) (map[string][]map[string]any, error) {
	unique := uniqueStrings(guids)
	if len(unique) == 0 {
		return map[string][]map[string]any{}, nil
	}
	params := make([]any, 0, len(unique))
	placeholders := make([]string, 0, len(unique))
	for _, guid := range unique {
		params = append(params, guid)
		placeholders = append(placeholders, "$"+strconv.Itoa(len(params)))
	}
	rows, err := conn.QueryContext(ctx, `
		SELECT device_guid, name, version, source, metadata_json
		  FROM engine.device_software_inventory
		 WHERE device_guid IN (`+strings.Join(placeholders, ",")+`)
		 ORDER BY LOWER(name) ASC, LOWER(source) ASC
	`, params...)
	if err != nil {
		return map[string][]map[string]any{}, nil
	}
	defer rows.Close()
	lookup := map[string][]map[string]any{}
	for rows.Next() {
		var guid, name, version, source, metadata sql.NullString
		if err := rows.Scan(&guid, &name, &version, &source, &metadata); err != nil {
			return nil, err
		}
		key := cleanText(guid.String)
		if key == "" {
			continue
		}
		lookup[key] = append(lookup[key], map[string]any{"name": nullString(name), "version": nullString(version), "source": nullString(source), "metadata": parseJSONObject(metadata)})
	}
	return lookup, rows.Err()
}

func loadFilterMetadataByGUID(ctx context.Context, conn *sql.Conn, guids []string) (map[string]map[string]string, error) {
	unique := uniqueStrings(guids)
	if len(unique) == 0 {
		return map[string]map[string]string{}, nil
	}
	params := make([]any, 0, len(unique))
	placeholders := make([]string, 0, len(unique))
	for _, guid := range unique {
		params = append(params, guid)
		placeholders = append(placeholders, "$"+strconv.Itoa(len(params)))
	}
	rows, err := conn.QueryContext(ctx, `
		SELECT device_guid, field_number, value
		  FROM engine.device_metadata_fields
		 WHERE device_guid IN (`+strings.Join(placeholders, ",")+`)
	`, params...)
	if err != nil {
		return map[string]map[string]string{}, nil
	}
	defer rows.Close()
	lookup := map[string]map[string]string{}
	for rows.Next() {
		var guid sql.NullString
		var fieldNumber sql.NullInt64
		var value sql.NullString
		if err := rows.Scan(&guid, &fieldNumber, &value); err != nil {
			return nil, err
		}
		key := cleanText(guid.String)
		if key == "" || !fieldNumber.Valid {
			continue
		}
		if _, ok := lookup[key]; !ok {
			lookup[key] = map[string]string{}
		}
		lookup[key][metadataFieldKey(int(fieldNumber.Int64))] = decodeMetadataValue(nullString(value))
	}
	return lookup, rows.Err()
}

func filterDevicePayload(row filterDeviceRow, softwareMap map[string][]map[string]any, metadataMap map[string]map[string]string) map[string]any {
	guid := nullString(row.GUID)
	lastSeen := nullInt(row.LastSeen)
	createdAt := nullInt(row.CreatedAt)
	memory := parseJSONArray(row.MemoryJSON)
	network := parseJSONArray(row.NetworkJSON)
	software := parseJSONArray(row.SoftwareJSON)
	storage := parseJSONArray(row.StorageJSON)
	cpu := parseJSONObject(row.CPUJSON)
	softwareRecords := append([]map[string]any(nil), softwareMap[guid]...)
	if len(softwareRecords) == 0 {
		seen := map[string]struct{}{}
		for _, item := range software {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			name := cleanText(entry["name"])
			if name == "" {
				continue
			}
			version := cleanText(entry["version"])
			source := firstText(cleanText(entry["source"]), "local_installed")
			key := strings.ToLower(name + "\x00" + version + "\x00" + source)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			softwareRecords = append(softwareRecords, map[string]any{"name": name, "version": version, "source": source, "metadata": map[string]any{}})
		}
	}
	metadataFields := map[string]any{}
	for key, value := range metadataMap[guid] {
		metadataFields[key] = value
	}
	summary := map[string]any{
		"hostname":            nullString(row.Hostname),
		"description":         nullString(row.Description),
		"agent_hash":          nullString(row.AgentHash),
		"agent_guid":          guid,
		"agent_id":            nullString(row.AgentID),
		"device_type":         nullString(row.DeviceType),
		"domain":              nullString(row.Domain),
		"external_ip":         nullString(row.ExternalIP),
		"internal_ip":         nullString(row.InternalIP),
		"last_reboot":         nullString(row.LastReboot),
		"last_seen":           lastSeen,
		"last_user":           nullString(row.LastUser),
		"operating_system":    nullString(row.OperatingSystem),
		"uptime":              nullInt(row.Uptime),
		"created_at":          createdAt,
		"connection_type":     nullString(row.ConnectionType),
		"connection_endpoint": nullString(row.ConnectionEndpoint),
	}
	payload := map[string]any{
		"hostname":              summary["hostname"],
		"description":           summary["description"],
		"details":               map[string]any{"summary": summary, "memory": memory, "network": network, "software": software, "storage": storage, "cpu": cpu},
		"summary":               summary,
		"created_at":            createdAt,
		"created_at_iso":        unixISO(createdAt),
		"device_guid":           guid,
		"agent_hash":            summary["agent_hash"],
		"agent_guid":            guid,
		"guid":                  guid,
		"memory":                memory,
		"network":               network,
		"software":              software,
		"software_records":      softwareRecords,
		"metadata_fields":       metadataFields,
		"storage":               storage,
		"cpu":                   cpu,
		"device_type":           summary["device_type"],
		"domain":                summary["domain"],
		"external_ip":           summary["external_ip"],
		"internal_ip":           summary["internal_ip"],
		"last_reboot":           summary["last_reboot"],
		"last_seen":             lastSeen,
		"last_seen_iso":         unixISO(lastSeen),
		"last_seen_age_minutes": lastSeenAgeMinutes(lastSeen),
		"last_user":             summary["last_user"],
		"operating_system":      summary["operating_system"],
		"uptime":                summary["uptime"],
		"agent_id":              summary["agent_id"],
		"agent_version":         summary["agent_hash"],
		"connection_type":       summary["connection_type"],
		"connection_endpoint":   summary["connection_endpoint"],
		"site_id":               nullableInt(row.SiteID),
		"site_name":             nullString(row.SiteName),
		"site_description":      nullString(row.SiteDescription),
		"security_status":       firstText(strings.ToLower(strings.TrimSpace(nullString(row.SecurityStatus))), "active"),
		"status":                statusFromLastSeen(lastSeen, time.Now().Unix()),
		"total_ram_gb":          totalRAMGB(memory),
		"storage_free_percent":  storageFreePercent(storage),
		"cpu_model":             cpuModel(cpu, summary),
	}
	return payload
}

func normalizeFilterRecord(input map[string]any, existing map[string]any, username string) map[string]any {
	base := existing
	if base == nil {
		base = map[string]any{}
	}
	now := time.Now().Unix()
	siteMode := strings.ToLower(firstText(cleanText(firstPresentAny(input["site_mode"], input["siteMode"], base["site_mode"])), filterSiteModeGlobal))
	if siteMode != filterSiteModeGlobal && siteMode != filterSiteModeSpecific && siteMode != filterSiteModeExclusions {
		siteMode = filterSiteModeGlobal
	}
	advanced := firstPresentAny(input["criteria"], input["advanced_criteria"], input["advancedCriteria"], groupsPayload(input), base["advanced_criteria"], map[string]any{"groups": []map[string]any{}})
	basic := firstPresentAny(input["basic_criteria"], input["basicCriteria"], base["basic_criteria"], map[string]any{"criteria": []map[string]any{}})
	normalizedBasic := normalizeBasicCriteria(asMap(basic))
	normalizedAdvanced := normalizeAdvancedCriteria(asMap(advanced))
	merged := mergeCriteria(normalizedBasic, normalizedAdvanced)
	name := trimSingleLine(firstText(cleanText(input["name"]), cleanText(base["name"])))
	if name == "" {
		name = "Unnamed Filter"
	}
	createdAt := int64FromAny(base["created_at"])
	if createdAt == nil || *createdAt == 0 {
		createdAt = &now
	}
	return map[string]any{
		"id":                firstPresentAny(input["id"], base["id"]),
		"name":              name,
		"description":       trimSingleLine(firstText(cleanText(input["description"]), cleanText(base["description"]))),
		"archived":          boolFromAny(firstPresentAny(input["archived"], base["archived"], false)),
		"criteria_mode":     "advanced",
		"site_mode":         siteMode,
		"site_ids":          int64SliceToAny(siteIDsFromAny(firstPresentAny(input["site_ids"], input["sites"], input["siteIds"], input["site_scope_values"], base["site_ids"], []any{}))),
		"basic_criteria":    normalizedBasic,
		"advanced_criteria": merged,
		"criteria":          merged,
		"last_edited_by":    firstText(username, cleanText(base["last_edited_by"]), "Unknown"),
		"created_at":        *createdAt,
		"updated_at":        now,
	}
}

func validateFilterRecord(record map[string]any) []string {
	errors := []string{}
	if mode := cleanText(record["site_mode"]); (mode == filterSiteModeSpecific || mode == filterSiteModeExclusions) && len(siteIDsFromAny(record["site_ids"])) == 0 {
		errors = append(errors, "Select at least one site for the chosen site mode.")
	}
	groups := groupsFromCriteria(record["advanced_criteria"])
	for gi, group := range groups {
		if gi > 0 {
			join := strings.ToUpper(cleanText(group["join_with"]))
			if join != "AND" && join != "OR" {
				errors = append(errors, fmt.Sprintf("Advanced group %d must specify AND or OR.", gi+1))
			}
		}
		conditions, _ := group["conditions"].([]map[string]any)
		for ci, condition := range conditions {
			if ci > 0 {
				join := strings.ToUpper(cleanText(condition["join_with"]))
				if join != "AND" && join != "OR" {
					errors = append(errors, fmt.Sprintf("Advanced group %d condition %d must specify AND or OR.", gi+1, ci+1))
				}
			}
			errors = append(errors, validateFilterCriterion(condition, fmt.Sprintf("Advanced group %d condition %d", gi+1, ci+1))...)
		}
	}
	return errors
}

func validateFilterCriterion(condition map[string]any, path string) []string {
	errors := []string{}
	fieldID := cleanText(condition["field"])
	spec := filterFieldByID[fieldID]
	if spec == nil {
		return []string{path + ": field is invalid."}
	}
	operator := strings.ToLower(firstText(cleanText(condition["operator"]), "contains"))
	if !stringInSlice(operator, filterStringSliceFromAny(spec["operators"])) {
		errors = append(errors, fmt.Sprintf("%s: operator is invalid for %s.", path, cleanText(spec["label"])))
	}
	useRegex := boolFromAny(condition["use_regex"])
	if useRegex && !boolFromAny(spec["supports_regex"]) {
		errors = append(errors, fmt.Sprintf("%s: regex is not supported for %s.", path, cleanText(spec["label"])))
	}
	value := cleanText(condition["value"])
	switch cleanText(spec["kind"]) {
	case "text", "enum", "software":
		if value == "" {
			errors = append(errors, path+": value is required.")
		}
	case "number":
		if _, ok := float64Value(condition["value"]); !ok {
			errors = append(errors, path+": numeric value is required.")
		}
	case "metadata_field":
		if metadataFieldNumber(condition) == 0 {
			errors = append(errors, path+": metadata field is required.")
		}
		if value == "" && operator != "equals" {
			errors = append(errors, path+": value is required.")
		}
	}
	if useRegex {
		if _, err := regexp.Compile(value); err != nil {
			errors = append(errors, fmt.Sprintf("%s: regex is invalid (%v).", path, err))
		}
	}
	if fieldID == "installed_software" {
		source := strings.ToLower(cleanText(condition["software_source"]))
		if source != "" && !stringInSlice(source, []string{"local_installed", "windows_store", "dpkg", "rpm"}) {
			errors = append(errors, path+": software source is invalid.")
		}
		versionOperator := strings.ToLower(cleanText(condition["version_operator"]))
		versionValue := cleanText(condition["version_value"])
		if versionValue != "" && versionOperator == "" {
			versionOperator = "matches"
		}
		if versionOperator != "" && !stringInSlice(versionOperator, []string{"matches", "older_than", "newer_than"}) {
			errors = append(errors, path+": version operator is invalid.")
		}
		if versionOperator != "" && versionValue == "" {
			errors = append(errors, path+": version value is required when a version operator is set.")
		}
	}
	return errors
}

func matchFilterDevices(record map[string]any, devices []map[string]any) []map[string]any {
	normalized := normalizeFilterRecord(record, nil, "")
	if len(validateFilterRecord(normalized)) > 0 {
		return []map[string]any{}
	}
	matches := []map[string]any{}
	seen := map[string]struct{}{}
	for _, device := range devices {
		hostname := cleanText(device["hostname"])
		if hostname == "" {
			continue
		}
		if !deviceMatchesSiteMode(normalized, device) || !evaluateFilterCriteria(normalized, device) {
			continue
		}
		key := strings.ToLower(hostname)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		matches = append(matches, device)
	}
	return matches
}

func deviceMatchesSiteMode(record map[string]any, device map[string]any) bool {
	mode := cleanText(record["site_mode"])
	siteIDs := int64Set(siteIDsFromAny(record["site_ids"]))
	deviceSiteID := int64FromAny(device["site_id"])
	switch mode {
	case filterSiteModeSpecific:
		if deviceSiteID == nil {
			return false
		}
		_, ok := siteIDs[*deviceSiteID]
		return ok
	case filterSiteModeExclusions:
		if deviceSiteID == nil {
			return true
		}
		_, excluded := siteIDs[*deviceSiteID]
		return !excluded
	default:
		return true
	}
}

func evaluateFilterCriteria(record map[string]any, device map[string]any) bool {
	groups := groupsFromCriteria(record["advanced_criteria"])
	if len(groups) == 0 {
		return true
	}
	var result *bool
	for _, group := range groups {
		groupResult := evaluateFilterGroup(group, device)
		if result == nil {
			result = &groupResult
			continue
		}
		join := strings.ToUpper(firstText(cleanText(group["join_with"]), "OR"))
		next := *result || groupResult
		if join == "AND" {
			next = *result && groupResult
		}
		result = &next
	}
	return result != nil && *result
}

func evaluateFilterGroup(group map[string]any, device map[string]any) bool {
	conditions, _ := group["conditions"].([]map[string]any)
	if len(conditions) == 0 {
		return true
	}
	var result *bool
	for _, condition := range conditions {
		conditionResult := evaluateFilterCriterion(condition, device)
		if result == nil {
			result = &conditionResult
			continue
		}
		join := strings.ToUpper(firstText(cleanText(condition["join_with"]), "AND"))
		next := *result && conditionResult
		if join == "OR" {
			next = *result || conditionResult
		}
		result = &next
	}
	return result != nil && *result
}

func evaluateFilterCriterion(condition map[string]any, device map[string]any) bool {
	fieldID := cleanText(condition["field"])
	spec := filterFieldByID[fieldID]
	if spec == nil {
		return false
	}
	if cleanText(spec["kind"]) == "software" {
		return evaluateSoftwareCriterion(condition, device)
	}
	if cleanText(spec["kind"]) == "metadata_field" {
		return evaluateMetadataCriterion(condition, device)
	}
	operator := strings.ToLower(firstText(cleanText(condition["operator"]), "contains"))
	value := cleanText(condition["value"])
	useRegex := boolFromAny(condition["use_regex"])
	fieldValue := filterFieldValue(device, fieldID)
	switch cleanText(spec["kind"]) {
	case "text", "enum":
		return textMatch(operator, cleanText(fieldValue), value, useRegex)
	case "number":
		return numericMatch(operator, fieldValue, condition["value"])
	default:
		return false
	}
}

func evaluateMetadataCriterion(condition map[string]any, device map[string]any) bool {
	fieldNumber := metadataFieldNumber(condition)
	if fieldNumber == 0 {
		return false
	}
	metadata, _ := device["metadata_fields"].(map[string]any)
	fieldValue := ""
	if metadata != nil {
		fieldValue = cleanText(metadata[metadataFieldKey(fieldNumber)])
	}
	return textMatch(strings.ToLower(firstText(cleanText(condition["operator"]), "contains")), fieldValue, cleanText(condition["value"]), boolFromAny(condition["use_regex"]))
}

func evaluateSoftwareCriterion(condition map[string]any, device map[string]any) bool {
	operator := strings.ToLower(firstText(cleanText(condition["operator"]), "contains"))
	value := cleanText(condition["value"])
	if value == "" {
		return false
	}
	positiveOperator := operator
	if positiveOperator == "does_not_contain" {
		positiveOperator = "contains"
	}
	source := strings.ToLower(cleanText(condition["software_source"]))
	versionOperator := strings.ToLower(cleanText(condition["version_operator"]))
	versionValue := cleanText(condition["version_value"])
	if versionValue != "" && versionOperator == "" {
		versionOperator = "matches"
	}
	records, _ := device["software_records"].([]map[string]any)
	matched := false
	for _, record := range records {
		if source != "" && strings.ToLower(cleanText(record["source"])) != source {
			continue
		}
		if !textMatch(positiveOperator, cleanText(record["name"]), value, boolFromAny(condition["use_regex"])) {
			continue
		}
		if versionOperator != "" && !softwareVersionMatch(cleanText(record["version"]), versionOperator, versionValue) {
			continue
		}
		matched = true
		break
	}
	if operator == "does_not_contain" {
		return !matched
	}
	return matched
}

func textMatch(operator, fieldValue, value string, useRegex bool) bool {
	if useRegex {
		matched := false
		if re, err := regexp.Compile(value); err == nil {
			matched = re.MatchString(fieldValue)
		}
		if operator == "does_not_contain" {
			return !matched
		}
		return matched
	}
	haystack := strings.ToLower(strings.TrimSpace(fieldValue))
	needle := strings.ToLower(strings.TrimSpace(value))
	switch operator {
	case "contains":
		return strings.Contains(haystack, needle)
	case "does_not_contain":
		return !strings.Contains(haystack, needle)
	case "equals":
		return haystack == needle
	case "begins_with":
		return strings.HasPrefix(haystack, needle)
	case "ends_with":
		return strings.HasSuffix(haystack, needle)
	default:
		return false
	}
}

func numericMatch(operator string, fieldValue any, value any) bool {
	lhs, okLeft := float64Value(fieldValue)
	rhs, okRight := float64Value(value)
	if !okLeft || !okRight {
		return false
	}
	switch operator {
	case "equals":
		return lhs == rhs
	case "greater_than":
		return lhs > rhs
	case "greater_than_or_equal":
		return lhs >= rhs
	case "less_than":
		return lhs < rhs
	case "less_than_or_equal":
		return lhs <= rhs
	default:
		return false
	}
}

func softwareVersionMatch(deviceVersion, operator, value string) bool {
	switch operator {
	case "matches":
		return compareVersion(deviceVersion, value) == 0 || strings.EqualFold(strings.TrimSpace(deviceVersion), strings.TrimSpace(value))
	case "older_than":
		return compareVersion(deviceVersion, value) < 0
	case "newer_than":
		return compareVersion(deviceVersion, value) > 0
	default:
		return false
	}
}

func compareVersion(left, right string) int {
	leftParts := versionParts(left)
	rightParts := versionParts(right)
	maxLen := len(leftParts)
	if len(rightParts) > maxLen {
		maxLen = len(rightParts)
	}
	for i := 0; i < maxLen; i++ {
		lv, rv := 0, 0
		if i < len(leftParts) {
			lv = leftParts[i]
		}
		if i < len(rightParts) {
			rv = rightParts[i]
		}
		if lv < rv {
			return -1
		}
		if lv > rv {
			return 1
		}
	}
	return 0
}

func versionParts(value string) []int {
	parts := regexp.MustCompile(`\D+`).Split(strings.TrimSpace(value), -1)
	result := []int{}
	for _, part := range parts {
		if part == "" {
			continue
		}
		parsed, _ := strconv.Atoi(part)
		result = append(result, parsed)
	}
	return result
}

func filterFieldValue(device map[string]any, fieldID string) any {
	summary, _ := device["summary"].(map[string]any)
	switch fieldID {
	case "hostname":
		return firstPresentAny(device["hostname"], summary["hostname"])
	case "description":
		return firstPresentAny(device["description"], summary["description"])
	case "site_name":
		return firstPresentAny(device["site_name"], summary["site_name"], summary["site"])
	case "operating_system":
		return firstPresentAny(device["operating_system"], summary["operating_system"])
	case "device_type":
		return firstPresentAny(device["device_type"], summary["device_type"])
	case "status":
		return firstPresentAny(device["status"], summary["status"])
	case "last_seen_age_minutes":
		return device["last_seen_age_minutes"]
	case "last_user":
		return firstPresentAny(device["last_user"], summary["last_user"])
	case "internal_ip":
		return firstPresentAny(device["internal_ip"], summary["internal_ip"])
	case "external_ip":
		return firstPresentAny(device["external_ip"], summary["external_ip"])
	case "domain":
		return firstPresentAny(device["domain"], summary["domain"])
	case "total_ram_gb":
		return device["total_ram_gb"]
	case "storage_free_percent":
		return device["storage_free_percent"]
	case "cpu_model":
		return device["cpu_model"]
	case "agent_version":
		return firstPresentAny(device["agent_version"], summary["agent_hash"])
	default:
		return firstPresentAny(device[fieldID], summary[fieldID])
	}
}

func normalizeBasicCriteria(payload map[string]any) map[string]any {
	items, ok := payload["criteria"].([]any)
	if !ok {
		return map[string]any{"criteria": []map[string]any{}}
	}
	criteria := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if entry, ok := item.(map[string]any); ok {
			criteria = append(criteria, normalizeCriterion(entry, 0, false))
		}
	}
	return map[string]any{"criteria": criteria}
}

func normalizeAdvancedCriteria(payload map[string]any) map[string]any {
	items, ok := payload["groups"].([]any)
	if !ok {
		if typed, ok := payload["groups"].([]map[string]any); ok {
			groups := make([]any, 0, len(typed))
			for _, group := range typed {
				groups = append(groups, group)
			}
			items = groups
		}
	}
	groups := []map[string]any{}
	for index, item := range items {
		group, ok := item.(map[string]any)
		if !ok {
			continue
		}
		join := strings.ToUpper(firstText(cleanText(firstPresentAny(group["join_with"], group["joinWith"])), "OR"))
		if index == 0 {
			join = ""
		} else if join != "AND" && join != "OR" {
			join = "OR"
		}
		conditionsRaw := group["conditions"]
		conditionsAny, _ := conditionsRaw.([]any)
		if conditionsAny == nil {
			if typed, ok := conditionsRaw.([]map[string]any); ok {
				for _, condition := range typed {
					conditionsAny = append(conditionsAny, condition)
				}
			}
		}
		conditions := []map[string]any{}
		for conditionIndex, conditionAny := range conditionsAny {
			condition, ok := conditionAny.(map[string]any)
			if !ok {
				continue
			}
			conditions = append(conditions, normalizeCriterion(condition, conditionIndex, true))
		}
		groups = append(groups, map[string]any{"join_with": stringOrNil(join), "conditions": conditions})
	}
	return map[string]any{"groups": groups}
}

func normalizeCriterion(input map[string]any, index int, advanced bool) map[string]any {
	condition := map[string]any{
		"field":     cleanText(input["field"]),
		"operator":  strings.ToLower(firstText(cleanText(input["operator"]), "contains")),
		"value":     firstPresentAny(input["value"], ""),
		"use_regex": boolFromAny(firstPresentAny(input["use_regex"], input["useRegex"])),
	}
	if advanced {
		join := strings.ToUpper(firstText(cleanText(firstPresentAny(input["join_with"], input["joinWith"])), "AND"))
		if index == 0 {
			join = ""
		} else if join != "AND" && join != "OR" {
			join = "AND"
		}
		condition["join_with"] = stringOrNil(join)
	}
	if condition["field"] == "installed_software" {
		condition["software_source"] = strings.ToLower(cleanText(firstPresentAny(input["software_source"], input["softwareSource"], input["source"])))
		condition["version_operator"] = strings.ToLower(cleanText(firstPresentAny(input["version_operator"], input["versionOperator"])))
		condition["version_value"] = cleanText(firstPresentAny(input["version_value"], input["versionValue"]))
	}
	if condition["field"] == "metadata_field" {
		condition["metadata_field_number"] = metadataFieldNumber(input)
	}
	return condition
}

func mergeCriteria(basic map[string]any, advanced map[string]any) map[string]any {
	groups := groupsFromCriteria(advanced)
	if len(groups) > 0 {
		return map[string]any{"groups": groups}
	}
	rawCriteria, _ := basic["criteria"].([]map[string]any)
	if len(rawCriteria) == 0 {
		return map[string]any{"groups": []map[string]any{}}
	}
	conditions := []map[string]any{}
	for index, criterion := range rawCriteria {
		item := copyMap(criterion)
		if index == 0 {
			item["join_with"] = nil
		} else {
			item["join_with"] = "AND"
		}
		conditions = append(conditions, item)
	}
	return map[string]any{"groups": []map[string]any{{"join_with": nil, "conditions": conditions}}}
}

func groupsFromCriteria(criteria any) []map[string]any {
	payload := asMap(criteria)
	if groups, ok := payload["groups"].([]map[string]any); ok {
		return groups
	}
	items, _ := payload["groups"].([]any)
	groups := []map[string]any{}
	for _, item := range items {
		if group, ok := item.(map[string]any); ok {
			conditions, _ := group["conditions"].([]map[string]any)
			if conditions == nil {
				raw, _ := group["conditions"].([]any)
				for _, entry := range raw {
					if condition, ok := entry.(map[string]any); ok {
						conditions = append(conditions, condition)
					}
				}
			}
			groups = append(groups, map[string]any{"join_with": group["join_with"], "conditions": conditions})
		}
	}
	return groups
}

func buildFilterFieldLookup() map[string]map[string]any {
	lookup := map[string]map[string]any{}
	for _, field := range filterFields {
		lookup[cleanText(field["value"])] = field
	}
	return lookup
}

func effectiveFilterSiteIDs(record map[string]any, allSiteIDs []int64) []int64 {
	configured := siteIDsFromAny(record["site_ids"])
	mode := cleanText(record["site_mode"])
	if mode == filterSiteModeSpecific {
		return configured
	}
	if mode == filterSiteModeExclusions {
		excluded := int64Set(configured)
		result := []int64{}
		for _, siteID := range allSiteIDs {
			if _, ok := excluded[siteID]; !ok {
				result = append(result, siteID)
			}
		}
		return result
	}
	return append([]int64(nil), allSiteIDs...)
}

func referencedFilterIDs(targets []any, allowed map[int64]struct{}) map[int64]struct{} {
	referenced := map[int64]struct{}{}
	for _, target := range targets {
		entry, ok := target.(map[string]any)
		if !ok {
			continue
		}
		kind := strings.ToLower(cleanText(firstPresentAny(entry["kind"], entry["type"])))
		filterID, hasID := int64Value(firstPresentAny(entry["filter_id"], entry["id"]))
		if kind != "filter" && !hasID {
			continue
		}
		if hasID {
			if _, ok := allowed[filterID]; ok {
				referenced[filterID] = struct{}{}
			}
		}
	}
	return referenced
}

func filterScopeSummary(record map[string]any) string {
	names := []string{}
	for _, name := range filterStringSliceFromAny(record["site_names"]) {
		if strings.TrimSpace(name) != "" {
			names = append(names, strings.TrimSpace(name))
		}
	}
	switch cleanText(record["site_mode"]) {
	case filterSiteModeSpecific:
		if len(names) > 0 {
			return "Specific Sites: " + strings.Join(names, ", ")
		}
		return "Specific Sites"
	case filterSiteModeExclusions:
		if len(names) > 0 {
			return "Global w/ Exclusions: " + strings.Join(names, ", ")
		}
		return "Global w/ Exclusions"
	default:
		return "Global"
	}
}

func filterSearchRank(name, query string) int {
	if name == query {
		return 0
	}
	if strings.HasPrefix(name, query) {
		return 1
	}
	return 2
}

func filterBodyHasDraftCriteria(body map[string]any) bool {
	for _, key := range []string{"name", "criteria_mode", "criteriaMode", "site_mode", "siteMode", "criteria", "basic_criteria", "advanced_criteria", "groups"} {
		if _, ok := body[key]; ok {
			return true
		}
	}
	return false
}

func groupsPayload(input map[string]any) any {
	if groups, ok := input["groups"].([]any); ok {
		return map[string]any{"groups": groups}
	}
	if groups, ok := input["groups"].([]map[string]any); ok {
		return map[string]any{"groups": groups}
	}
	return nil
}

func metadataFieldNumber(input map[string]any) int {
	for _, key := range []string{"metadata_field_number", "metadataFieldNumber", "field_number", "fieldNumber"} {
		if value, ok := int64Value(input[key]); ok && value >= 1 && value <= metadataFieldCount {
			return int(value)
		}
	}
	return 0
}

func trimSingleLine(value string) string {
	parts := []string{}
	for _, line := range strings.Split(strings.TrimSpace(value), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			parts = append(parts, line)
		}
	}
	return strings.Join(parts, " ")
}

func lastSeenAgeMinutes(lastSeen int64) any {
	if lastSeen <= 0 {
		return nil
	}
	age := time.Since(time.Unix(lastSeen, 0)).Minutes()
	if age < 0 {
		age = 0
	}
	return age
}

func storageFreePercent(entries []any) any {
	total := 0.0
	free := 0.0
	for _, item := range entries {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		t, ok := float64Value(entry["total"])
		if !ok || t <= 0 {
			continue
		}
		f, _ := float64Value(entry["free"])
		total += t
		free += f
	}
	if total <= 0 {
		return nil
	}
	return (free / total) * 100
}

func totalRAMGB(entries []any) any {
	total := 0.0
	for _, item := range entries {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		capacity, _ := float64Value(entry["capacity"])
		total += capacity
	}
	if total <= 0 {
		return nil
	}
	return total / (1024 * 1024 * 1024)
}

func cpuModel(cpu map[string]any, summary map[string]any) string {
	return firstText(cleanText(cpu["name"]), cleanText(summary["processor"]))
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func boolFromAny(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case int:
		return typed != 0
	case int64:
		return typed != 0
	case float64:
		return typed != 0
	case string:
		return parseTruthy(typed)
	default:
		return false
	}
}

func int64Value(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		return int64(typed), true
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func int64FromAny(value any) *int64 {
	if parsed, ok := int64Value(value); ok {
		return &parsed
	}
	return nil
}

func float64Value(value any) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case float64:
		return typed, true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func siteIDsFromAny(value any) []int64 {
	items := []any{}
	switch typed := value.(type) {
	case []any:
		items = typed
	case []int64:
		result := append([]int64(nil), typed...)
		return result
	case []map[string]any:
		for _, item := range typed {
			items = append(items, item)
		}
	case nil:
		return []int64{}
	default:
		items = []any{value}
	}
	result := []int64{}
	seen := map[int64]struct{}{}
	for _, item := range items {
		value := item
		if entry, ok := item.(map[string]any); ok {
			value = firstPresentAny(entry["site_id"], entry["id"], entry["value"])
		}
		if parsed, ok := int64Value(value); ok {
			if _, exists := seen[parsed]; !exists {
				seen[parsed] = struct{}{}
				result = append(result, parsed)
			}
		}
	}
	return result
}

func int64Set(values []int64) map[int64]struct{} {
	result := map[int64]struct{}{}
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func int64SliceToAny(values []int64) []any {
	result := make([]any, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

func stringSliceToAnyStrings(values []string) []any {
	result := make([]any, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

func filterStringSliceFromAny(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		result := []string{}
		for _, item := range typed {
			result = append(result, cleanText(item))
		}
		return result
	default:
		return []string{}
	}
}

func stringInSlice(value string, items []string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func stringOrNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func firstPresentAny(values ...any) any {
	for _, value := range values {
		if value == nil {
			continue
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
			continue
		}
		return value
	}
	return nil
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
