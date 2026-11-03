package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const schedulerKindPatchPolicyEnforcement = "patch_policy_enforcement"
const patchPolicyEnforcementKey = "policy_enforcement"

var errPatchPolicyScopeChanged = errors.New("patch policy enforcement scope changed")

// This is original run data, not an Agent grant. Admission still owns the token,
// nonce and terminal receipt. A successor cannot replace an active predecessor.
type patchPolicyEnforcementIntent struct {
	Version     int    `json:"version"`
	PolicyRunID int64  `json:"policy_run_id"`
	PolicyID    int64  `json:"policy_id"`
	ScheduledTS int64  `json:"scheduled_ts"`
	DeviceGUID  string `json:"device_guid"`
	Hostname    string `json:"hostname"`
	AgentID     string `json:"agent_id"`
	SiteID      int64  `json:"site_id"`
	Mode        string `json:"mode"`
	RequestID   string `json:"request_id"`
	ScopeSHA256 string `json:"scope_sha256"`
}

type patchPolicyEnforcementState struct {
	Version       int    `json:"version"`
	DesiredWorkID int64  `json:"desired_work_id"`
	ActiveWorkID  int64  `json:"active_work_id"`
	ReceiptSHA256 string `json:"receipt_sha256,omitempty"`
}

type patchPolicyEnforcementDispatch struct {
	WorkerGUID      string `json:"worker_guid"`
	RouteGeneration int64  `json:"route_generation"`
	PodUID          string `json:"pod_uid"`
	NodeName        string `json:"node_name"`
}

func (v patchPolicyEnforcementIntent) valid() bool {
	return v.Version == 1 && v.PolicyRunID > 0 && v.PolicyID > 0 && v.ScheduledTS > 0 &&
		v.PolicyRunID <= (1<<53)-1 && v.PolicyID <= (1<<53)-1 && v.SiteID <= (1<<53)-1 &&
		normalizeCanonicalGUID(v.DeviceGUID) == v.DeviceGUID && v.DeviceGUID != "" &&
		v.Hostname != "" && v.Hostname == strings.TrimSpace(v.Hostname) && v.AgentID != "" && v.SiteID > 0 &&
		stringInSet(v.Mode, "managed", "unmanaged", "frozen") &&
		v.RequestID == fmt.Sprintf("patch-policy-run-%d-device-%s-site-%d", v.PolicyRunID, v.DeviceGUID, v.SiteID) &&
		schedulerWorkerOperationDigest.MatchString(v.ScopeSHA256)
}

// Python worker receipts use ensure_ascii=False and sorted keys. Go's JSON
// encoder escapes U+2028/U+2029 even with EscapeHTML disabled, so encode strings
// explicitly. This bounded fixed-shape encoder is not a general JSON protocol.
func patchPolicyReceiptString(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", errors.New("invalid receipt UTF-8")
	}
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"', '\\':
			out.WriteByte('\\')
			out.WriteRune(r)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&out, `\u%04x`, r)
			} else {
				out.WriteRune(r)
			}
		}
	}
	out.WriteByte('"')
	return out.String(), nil
}

func patchPolicyReceiptDigest(activityID int64, status, stdout, stderr, token string) (string, error) {
	if activityID <= 0 || activityID > (1<<53)-1 || !schedulerWorkerOperationToken.MatchString(token) ||
		len(stdout) > 256*1024 || len(stderr) > 256*1024 {
		return "", errors.New("invalid policy receipt")
	}
	switch status {
	case "Success":
		status = "Completed"
	case "Failed", "Skipped":
	default:
		return "", errors.New("policy receipt is not terminal")
	}
	out, err := patchPolicyReceiptString(stdout)
	if err != nil {
		return "", err
	}
	errText, err := patchPolicyReceiptString(stderr)
	if err != nil {
		return "", err
	}
	raw := fmt.Sprintf(`{"context":{"worker_operation":{"token":"%s","version":1}},"job_id":%d,"status":"%s","stderr":%s,"stdout":%s}`, token, activityID, status, errText, out)
	if len(raw) > 1<<20 {
		return "", errors.New("policy receipt canonical envelope exceeds worker bound")
	}
	digest := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(digest[:]), nil
}

// A committed tombstone is necessary but its digest must still match retained
// status/output. Ordinary progress writers cannot manufacture this proof.
func patchPolicyCommittedReceipt(intent patchPolicyEnforcementIntent, dispatch patchPolicyEnforcementDispatch, activityID int64, status, stdout, stderr, metadata string) (string, error) {
	if !intent.valid() || !workerOperationActivityDeletionAllowed(activityID, metadata) {
		return "", errors.New("policy receipt binding invalid")
	}
	fields, err := schedulerAdmissionMetadata(metadata)
	if err != nil {
		return "", err
	}
	op, err := schedulerAdmissionMetadata(string(fields[schedulerWorkerOperationKey]))
	if err != nil {
		return "", err
	}
	str := func(key string) string { var value string; _ = json.Unmarshal(op[key], &value); return value }
	num := func(key string) int64 { n, _ := strconv.ParseInt(string(op[key]), 10, 64); return n }
	if str("kind") != "patch_policy_enforcement_request" || str("agent_guid") != intent.DeviceGUID || num("site_id") != intent.SiteID ||
		str("worker_guid") != dispatch.WorkerGUID || num("route_generation") != dispatch.RouteGeneration ||
		str("pod_uid") != dispatch.PodUID || str("node_name") != dispatch.NodeName || str("grant_nonce") == "" {
		return "", errors.New("policy receipt does not match original dispatch")
	}
	digest, err := patchPolicyReceiptDigest(activityID, status, stdout, stderr, str("token"))
	if err != nil || digest != str("receipt_sha256") {
		return "", errors.New("policy receipt digest mismatch")
	}
	return digest, nil
}

type patchPolicySQL interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type patchPolicyScopeStamp struct{ Rows []string }

// Version rows fence catalogue membership as well as existing rows. Device
// columns are selected from consumed filter fields, so unrelated telemetry does
// not invalidate a static global/site policy. No source value is stored on work.
func readPatchPolicyScopeStamp(ctx context.Context, q patchPolicySQL, fields map[string]bool) (patchPolicyScopeStamp, error) {
	stamp := patchPolicyScopeStamp{}
	queries := []string{
		`SELECT id::text, xmin::text FROM engine.patch_policies ORDER BY id`,
		`SELECT id::text, xmin::text FROM engine.patch_policy_sites ORDER BY id`,
		`SELECT id::text, xmin::text FROM engine.patch_policy_targets ORDER BY id`,
		`SELECT id::text, xmin::text FROM engine.patch_policy_exclusions ORDER BY id`,
		`SELECT id::text, xmin::text FROM engine.patch_policy_rules ORDER BY id`,
		`SELECT id::text, xmin::text FROM engine.device_filters ORDER BY id`,
		`SELECT filter_id::text, site_id::text FROM engine.device_filter_sites ORDER BY filter_id,site_id`,
	}
	columns := []string{"d.guid", "d.hostname", "d.agent_id", "d.device_type", "d.operating_system", "COALESCE(d.status,'active')", "ds.site_id::text", "s.name"}
	columnForField := map[string]string{
		"description": "d.description", "site_name": "s.name", "status": "d.last_seen::text", "last_seen_age_minutes": "d.last_seen::text",
		"last_user": "d.last_user", "internal_ip": "d.internal_ip", "external_ip": "d.external_ip", "domain": "d.domain",
		"total_ram_gb": "d.memory", "storage_free_percent": "d.storage", "cpu_model": "d.cpu", "agent_version": "d.agent_hash", "installed_software": "d.software",
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if column := columnForField[key]; column != "" {
			columns = append(columns, column)
		}
	}
	queries = append(queries, "SELECT "+strings.Join(columns, ",")+` FROM engine.devices d LEFT JOIN engine.device_sites ds ON ds.device_hostname=d.hostname LEFT JOIN engine.sites s ON s.id=ds.site_id ORDER BY d.guid`)
	if fields["installed_software"] {
		queries = append(queries, `SELECT device_guid,name,version,source,metadata_json FROM engine.device_software_inventory ORDER BY device_guid,name,version,source,metadata_json`)
	}
	if fields["metadata_field"] {
		queries = append(queries, `SELECT device_guid,field_number::text,value FROM engine.device_metadata_fields ORDER BY device_guid,field_number,value`)
	}
	for queryIndex, query := range queries {
		rows, err := q.QueryContext(ctx, query)
		if err != nil {
			return stamp, err
		}
		columns, err := rows.Columns()
		if err != nil {
			_ = rows.Close()
			return stamp, err
		}
		for rows.Next() {
			values := make([]sql.NullString, len(columns))
			args := make([]any, len(columns))
			for i := range values {
				args[i] = &values[i]
			}
			if err = rows.Scan(args...); err != nil {
				_ = rows.Close()
				return stamp, err
			}
			var row strings.Builder
			fmt.Fprintf(&row, "%d:", queryIndex)
			for _, value := range values {
				if !value.Valid {
					row.WriteString("null;")
				} else {
					fmt.Fprintf(&row, "%d:%s;", len(value.String), value.String)
				}
			}
			stamp.Rows = append(stamp.Rows, row.String())
		}
		err = errors.Join(rows.Err(), rows.Close())
		if err != nil {
			return stamp, err
		}
		stamp.Rows = append(stamp.Rows, fmt.Sprintf("end:%d", queryIndex))
	}
	return stamp, nil
}

type patchPolicyScopeProof struct {
	Stamp       patchPolicyScopeStamp
	Fields      map[string]bool
	Policies    []patchPolicyRow
	Scopes      map[int64]*patchPolicyEffectiveScope
	Devices     map[string]map[string]any
	Bindings    map[string]string
	ClockChecks []patchPolicyClockCheck
}

type patchPolicyClockCheck struct {
	LastSeen  int64
	Condition map[string]any
	Matched   bool
}

func (p *patchPolicyScopeProof) clockMatches(now time.Time) bool {
	for _, check := range p.ClockChecks {
		var age any
		if check.LastSeen > 0 {
			value := now.Sub(time.Unix(check.LastSeen, 0)).Minutes()
			if value < 0 {
				value = 0
			}
			age = value
		}
		device := map[string]any{"status": statusFromLastSeen(check.LastSeen, now.Unix()), "last_seen_age_minutes": age}
		if evaluateFilterCriterion(check.Condition, device) != check.Matched {
			return false
		}
	}
	return true
}

func patchPolicyConsumedJSON(field string, raw sql.NullString) error {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return nil
	}
	var value any
	if json.Unmarshal([]byte(raw.String), &value) != nil {
		return errors.New("malformed policy filter device input")
	}
	if value == nil {
		return nil
	}
	if field == "cpu_model" {
		if _, ok := value.(map[string]any); !ok {
			return errors.New("policy CPU input is not an object")
		}
	} else if _, ok := value.([]any); !ok {
		return errors.New("policy device input is not an array")
	}
	return nil
}

func patchPolicyScopeDeviceKey(policyID int64, guid string) string {
	return fmt.Sprintf("%d:%s", policyID, normalizeCanonicalGUID(guid))
}

func patchPolicyScopeBinding(row patchPolicyRow, device patchPolicyDevice, all []patchPolicyRow) (string, error) {
	hierarchy := []patchPolicyRow{}
	for _, candidate := range all {
		for _, id := range device.HierarchyPolicyIDs {
			if nullInt(candidate.ID) == id {
				hierarchy = append(hierarchy, candidate)
			}
		}
	}
	raw, err := json.Marshal(struct {
		Policy    patchPolicyRow
		Device    patchPolicyDevice
		Hierarchy []patchPolicyRow
	}{row, device, hierarchy})
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), nil
}

func (p *patchPolicyScopeProof) matches(intent patchPolicyEnforcementIntent) bool {
	if p == nil || !intent.valid() || p.Bindings[patchPolicyScopeDeviceKey(intent.PolicyID, intent.DeviceGUID)] != intent.ScopeSHA256 {
		return false
	}
	d := p.Devices[intent.DeviceGUID]
	mode := ""
	if scope := p.Scopes[intent.PolicyID]; scope != nil {
		for _, target := range scope.Devices {
			if normalizeCanonicalGUID(target.DeviceGUID) == intent.DeviceGUID && !target.Conflict {
				mode = patchPolicyEnforcementMode(scope.Policy, target)
			}
		}
	}
	return mode == intent.Mode && cleanText(d["hostname"]) == intent.Hostname && cleanText(d["agent_id"]) == intent.AgentID && coerceInt64(d["site_id"]) == intent.SiteID
}

func patchPolicyEnforcementMode(row patchPolicyRow, device patchPolicyDevice) string {
	if stringInSet(device.ExclusionMode, "unmanaged", "frozen") {
		return device.ExclusionMode
	}
	if boolInt64(row.ManagedUpdateMode) {
		return "managed"
	}
	return "unmanaged"
}

func (p *patchPolicyScopeProof) check(ctx context.Context, q patchPolicySQL) error {
	current, err := readPatchPolicyScopeStamp(ctx, q, p.Fields)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, p.Stamp) || !p.clockMatches(time.Now()) {
		return errPatchPolicyScopeChanged
	}
	return nil
}

// Reading/parsing/shaping is split so no resolver, JSON encoder or HTTP call
// runs while this reader owns a connection. The preloaded cache cannot fall back
// to the permissive inventory helpers which discard software/metadata errors.
func (s *postgresOperatorStore) readPatchPolicyScopeProof(ctx context.Context, profile operatorProfile) (*patchPolicyScopeProof, error) {
	allowed, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	initialPolicies, err := loadPatchPolicyRows(ctx, conn, "", nil)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	filterRows, filterSites, err := readPatchPolicyFilterInputs(ctx, conn)
	err = errors.Join(err, conn.Close())
	if err != nil {
		return nil, err
	}
	selectedFilters := map[int64]bool{}
	for _, policy := range initialPolicies {
		if !boolInt64(policy.Enabled) {
			continue
		}
		for _, targets := range [][]map[string]any{policy.Targets, policy.Exclusions} {
			for _, target := range targets {
				if id := coerceInt64(target["filter_id"]); id > 0 {
					selectedFilters[id] = true
				}
			}
		}
	}
	selectedRows := []deviceFilterRow{}
	for _, row := range filterRows {
		if selectedFilters[row.ID.Int64] {
			selectedRows = append(selectedRows, row)
		}
	}
	filters, fields, err := patchPolicyScopeFilters(selectedRows, filterSites)
	if err != nil {
		return nil, err
	}
	conn, err = s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	p := &patchPolicyScopeProof{Fields: fields, Devices: map[string]map[string]any{}, Bindings: map[string]string{}}
	var rawDevices []filterDeviceRow
	var software []patchPolicySoftwareInput
	var metadata []patchPolicyMetadataInput
	var rawTargets []sql.NullString
	readErr := func() error {
		before, err := readPatchPolicyScopeStamp(ctx, conn, fields)
		if err != nil {
			return err
		}
		currentFilters, currentSites, err := readPatchPolicyFilterInputs(ctx, conn)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(filterRows, currentFilters) || !reflect.DeepEqual(filterSites, currentSites) {
			return errPatchPolicyScopeChanged
		}
		p.Policies, err = loadPatchPolicyRows(ctx, conn, "", nil)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(initialPolicies, p.Policies) {
			return errPatchPolicyScopeChanged
		}
		rawDevices, software, metadata, err = readPatchPolicyDeviceInputs(ctx, conn, fields)
		if err != nil {
			return err
		}
		targetRows, err := conn.QueryContext(ctx, `SELECT t.target_json FROM engine.patch_policy_targets t JOIN engine.patch_policies p ON p.id=t.policy_id WHERE p.enabled=1 ORDER BY t.id`)
		if err != nil {
			return err
		}
		for targetRows.Next() {
			var raw sql.NullString
			if err = targetRows.Scan(&raw); err != nil {
				break
			}
			rawTargets = append(rawTargets, raw)
		}
		if err = errors.Join(err, targetRows.Err(), targetRows.Close()); err != nil {
			return err
		}
		p.Stamp, err = readPatchPolicyScopeStamp(ctx, conn, fields)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(before, p.Stamp) {
			return errPatchPolicyScopeChanged
		}
		return nil
	}()
	err = errors.Join(readErr, conn.Close())
	if err != nil {
		return nil, err
	}
	for _, raw := range rawTargets {
		if raw.Valid && strings.TrimSpace(raw.String) != "" {
			if _, err := schedulerAdmissionMetadata(raw.String); err != nil {
				return nil, errors.New("malformed policy target object")
			}
		}
	}
	if err := patchPolicyAuthorityRows(p.Policies); err != nil {
		return nil, err
	}
	softwareMap := map[string][]map[string]any{}
	metadataMap := map[string]map[string]string{}
	for _, v := range software {
		guid := normalizeCanonicalGUID(v.GUID)
		softwareMap[guid] = append(softwareMap[guid], map[string]any{"name": v.Name, "version": v.Version, "source": v.Source, "metadata": parseJSONObject(v.Metadata)})
	}
	for _, v := range metadata {
		guid := normalizeCanonicalGUID(v.GUID)
		if metadataMap[guid] == nil {
			metadataMap[guid] = map[string]string{}
		}
		key := metadataFieldKey(int(v.Field))
		if _, exists := metadataMap[guid][key]; exists {
			return nil, errors.New("ambiguous policy metadata identity")
		}
		metadataMap[guid][key] = decodeMetadataValue(v.Value)
	}
	cache := newPatchPolicyResolutionCache(s, profile)
	cache.strictInputs = true
	cache.devices = []map[string]any{}
	cache.filters = filters
	for _, row := range p.Policies {
		if !boolInt64(row.Enabled) {
			continue
		}
		for _, collection := range [][]map[string]any{row.Targets, row.Exclusions} {
			for _, target := range collection {
				if id := coerceInt64(target["filter_id"]); id > 0 {
					if filters[id] == nil {
						return nil, errors.New("policy filter input unavailable")
					}
					cache.loadedFilterIDs[id] = true
				}
			}
		}
	}
	seenHosts := map[string]string{}
	for _, row := range rawDevices {
		for field, raw := range map[string]sql.NullString{"total_ram_gb": row.MemoryJSON, "storage_free_percent": row.StorageJSON, "cpu_model": row.CPUJSON, "installed_software": row.SoftwareJSON} {
			if fields[field] {
				if err := patchPolicyConsumedJSON(field, raw); err != nil {
					return nil, err
				}
			}
		}
		guid := normalizeCanonicalGUID(row.GUID.String)
		if guid == "" || !strings.EqualFold(guid, row.GUID.String) {
			return nil, errors.New("policy device GUID is not canonical")
		}
		if _, exists := p.Devices[guid]; exists {
			return nil, errors.New("ambiguous policy device/site rows")
		}
		host := strings.ToLower(strings.TrimSpace(row.Hostname.String))
		if host == "" {
			return nil, errors.New("policy device hostname unavailable")
		}
		if previous := seenHosts[host]; previous != "" && previous != guid {
			return nil, errors.New("ambiguous policy hostname identity")
		}
		seenHosts[host] = guid
		row.GUID.String = guid
		device := filterDevicePayload(row, softwareMap, metadataMap)
		p.Devices[guid] = device
		if allowed != nil && !int64SliceContains(allowed, row.SiteID.Int64) {
			continue
		}
		cache.devices = append(cache.devices, device)
		for _, filter := range filters {
			if !deviceMatchesSiteMode(filter, device) {
				continue
			}
			for _, group := range groupsFromCriteria(filter["advanced_criteria"]) {
				conditions, _ := group["conditions"].([]map[string]any)
				for _, condition := range conditions {
					if field := cleanText(condition["field"]); field == "status" || field == "last_seen_age_minutes" {
						p.ClockChecks = append(p.ClockChecks, patchPolicyClockCheck{LastSeen: row.LastSeen.Int64, Condition: copyMap(condition), Matched: evaluateFilterCriterion(condition, device)})
					}
				}
			}
		}
	}
	cache.executableDevices = scheduledExecutableDevices(cache.devices)
	p.Scopes, err = cache.patchPolicyEffectiveScopes(ctx, p.Policies)
	if err != nil {
		return nil, err
	}
	for id, scope := range p.Scopes {
		for _, device := range scope.Devices {
			if device.Conflict {
				continue
			}
			binding, err := patchPolicyScopeBinding(scope.Policy, device, p.Policies)
			if err != nil {
				return nil, err
			}
			p.Bindings[patchPolicyScopeDeviceKey(id, device.DeviceGUID)] = binding
		}
	}
	return p, nil
}

type patchPolicyFilterSiteInput struct {
	FilterID, SiteID int64
	Name             string
}
type patchPolicySoftwareInput struct {
	GUID, Name, Version, Source string
	Metadata                    sql.NullString
}
type patchPolicyMetadataInput struct {
	GUID  string
	Field int64
	Value string
}

func readPatchPolicyFilterInputs(ctx context.Context, q patchPolicySQL) ([]deviceFilterRow, []patchPolicyFilterSiteInput, error) {
	filters := []deviceFilterRow{}
	sites := []patchPolicyFilterSiteInput{}
	rows, err := q.QueryContext(ctx, `SELECT id,name,description,archived,criteria_mode,site_mode,basic_criteria_json,advanced_criteria_json,last_edited_by,created_at,updated_at FROM engine.device_filters ORDER BY id`)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var v deviceFilterRow
		if err = rows.Scan(&v.ID, &v.Name, &v.Description, &v.Archived, &v.CriteriaMode, &v.SiteMode, &v.BasicCriteriaJSON, &v.AdvancedCriteriaJSON, &v.LastEditedBy, &v.CreatedAt, &v.UpdatedAt); err != nil {
			_ = rows.Close()
			return nil, nil, err
		}
		filters = append(filters, v)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, nil, err
	}
	rows, err = q.QueryContext(ctx, `SELECT fs.filter_id,fs.site_id,s.name FROM engine.device_filter_sites fs JOIN engine.sites s ON s.id=fs.site_id ORDER BY fs.filter_id,fs.site_id`)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var v patchPolicyFilterSiteInput
		if err = rows.Scan(&v.FilterID, &v.SiteID, &v.Name); err != nil {
			_ = rows.Close()
			return nil, nil, err
		}
		sites = append(sites, v)
	}
	err = errors.Join(rows.Err(), rows.Close())
	return filters, sites, err
}

func patchPolicyScopeFilters(rows []deviceFilterRow, sites []patchPolicyFilterSiteInput) (map[int64]map[string]any, map[string]bool, error) {
	filters := map[int64]map[string]any{}
	fields := map[string]bool{}
	for _, v := range rows {
		if truthyInt(v.Archived) == 1 {
			continue
		}
		if mode := strings.ToLower(strings.TrimSpace(v.SiteMode.String)); mode != "" && !stringInSet(mode, filterSiteModeGlobal, filterSiteModeSpecific, filterSiteModeExclusions) {
			return nil, nil, errors.New("invalid policy filter site mode")
		}
		if err := patchPolicyCriteriaShape(v.BasicCriteriaJSON, false); err != nil {
			return nil, nil, err
		}
		if err := patchPolicyCriteriaShape(v.AdvancedCriteriaJSON, true); err != nil {
			return nil, nil, err
		}
		record := normalizeFilterRecord(map[string]any{"id": nullInt(v.ID), "name": nullString(v.Name), "description": nullString(v.Description), "archived": false, "criteria_mode": firstText(nullString(v.CriteriaMode), "advanced"), "site_mode": firstText(nullString(v.SiteMode), filterSiteModeGlobal), "basic_criteria": parseJSONObject(v.BasicCriteriaJSON), "advanced_criteria": parseJSONObject(v.AdvancedCriteriaJSON), "site_ids": []any{}}, nil, "")
		for _, site := range sites {
			if site.FilterID == v.ID.Int64 {
				record["site_ids"] = append(record["site_ids"].([]any), site.SiteID)
			}
		}
		if problems := validateFilterRecord(record); len(problems) > 0 {
			return nil, nil, errors.New("invalid policy filter criteria")
		}
		for _, group := range groupsFromCriteria(record["advanced_criteria"]) {
			conditions, _ := group["conditions"].([]map[string]any)
			for _, condition := range conditions {
				field := cleanText(condition["field"])
				if filterFieldByID[field] == nil {
					return nil, nil, errors.New("unknown policy filter field")
				}
				fields[field] = true
			}
		}
		filters[v.ID.Int64] = record
	}
	return filters, fields, nil
}

// The legacy normalizer drops malformed objects and defaults unknown joins or
// site modes. That convenience is not an authority parser: a malformed selected
// filter must not turn into an intentional empty (match-all) criterion set.
func patchPolicyCriteriaShape(raw sql.NullString, advanced bool) error {
	invalid := errors.New("malformed policy filter criteria structure")
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return invalid
	}
	root, err := schedulerAdmissionMetadata(raw.String)
	if err != nil {
		return invalid
	}
	key := "criteria"
	if advanced {
		key = "groups"
	}
	for field := range root {
		if field != key {
			return invalid
		}
	}
	array, exists := root[key]
	if !exists {
		return nil
	}
	var items []json.RawMessage
	if len(array) == 0 || array[0] != '[' || json.Unmarshal(array, &items) != nil {
		return invalid
	}
	validJoin := func(value json.RawMessage, index int) bool {
		if len(value) == 0 {
			return true
		}
		if string(value) == "null" {
			return index == 0
		}
		var join string
		if json.Unmarshal(value, &join) != nil {
			return false
		}
		join = strings.ToUpper(strings.TrimSpace(join))
		return stringInSet(join, "AND", "OR") || (index == 0 && join == "")
	}
	checkConditions := func(conditions []json.RawMessage) error {
		for index, rawCondition := range conditions {
			condition, err := schedulerAdmissionMetadata(string(rawCondition))
			if err != nil {
				return invalid
			}
			for field := range condition {
				if !stringInSet(field, "field", "operator", "value", "use_regex", "join_with", "software_source", "version_operator", "version_value", "metadata_field_number") {
					return invalid
				}
			}
			for _, field := range []string{"field", "operator"} {
				var value string
				if json.Unmarshal(condition[field], &value) != nil || strings.TrimSpace(value) == "" {
					return invalid
				}
			}
			var value any
			if json.Unmarshal(condition["value"], &value) != nil {
				return invalid
			}
			switch value.(type) {
			case string, float64:
			default:
				return invalid
			}
			if option, ok := condition["use_regex"]; ok {
				var value bool
				if json.Unmarshal(option, &value) != nil || string(option) == "null" {
					return invalid
				}
			}
			if !validJoin(condition["join_with"], index) {
				return invalid
			}
			for _, field := range []string{"software_source", "version_operator", "version_value"} {
				if option, ok := condition[field]; ok {
					var value string
					if json.Unmarshal(option, &value) != nil || string(option) == "null" {
						return invalid
					}
				}
			}
			if option, ok := condition["metadata_field_number"]; ok {
				var value int64
				if json.Unmarshal(option, &value) != nil || value <= 0 {
					return invalid
				}
			}
		}
		return nil
	}
	if !advanced {
		return checkConditions(items)
	}
	for index, rawGroup := range items {
		group, err := schedulerAdmissionMetadata(string(rawGroup))
		if err != nil {
			return invalid
		}
		for field := range group {
			if field != "conditions" && field != "join_with" {
				return invalid
			}
		}
		if !validJoin(group["join_with"], index) {
			return invalid
		}
		conditions, ok := group["conditions"]
		if !ok || len(conditions) == 0 || conditions[0] != '[' {
			return invalid
		}
		var entries []json.RawMessage
		if json.Unmarshal(conditions, &entries) != nil {
			return invalid
		}
		if err = checkConditions(entries); err != nil {
			return err
		}
	}
	return nil
}

func patchPolicyAuthorityRows(rows []patchPolicyRow) error {
	invalid := errors.New("invalid retained policy authority input")
	for _, row := range rows {
		if !row.Enabled.Valid || !int64SliceContains([]int64{0, 1}, row.Enabled.Int64) {
			return invalid
		}
		if row.Enabled.Int64 == 0 {
			continue
		}
		if normalizePatchPolicyType(row.PolicyType.String) == "" || !row.ManagedUpdateMode.Valid || !int64SliceContains([]int64{0, 1}, row.ManagedUpdateMode.Int64) {
			return invalid
		}
		if !stringInSet(strings.ToLower(strings.TrimSpace(row.RoleScope.String)), "server", "servers", "workstation", "workstations", "desktop", "desktops", "both") {
			return invalid
		}
		for _, collection := range []struct {
			Values    []map[string]any
			Exclusion bool
		}{{row.Targets, false}, {row.Exclusions, true}} {
			for _, entry := range collection.Values {
				if collection.Exclusion && normalizePatchPolicyExclusionType(entry["exclusion_type"]) == "" {
					return invalid
				}
				targetType := normalizePatchPolicyTargetType(entry["target_type"])
				if targetType == "" {
					return invalid
				}
				target := patchPolicyScheduledTarget(entry)
				if cleanText(target["kind"]) != targetType {
					return invalid
				}
				if targetType == "filter" {
					if coerceInt64(target["filter_id"]) <= 0 {
						return invalid
					}
					continue
				}
				guid := cleanText(target["device_guid"])
				if guid != "" && normalizeCanonicalGUID(guid) == "" {
					return invalid
				}
				if guid == "" && (cleanText(target["hostname"]) == "" || (collection.Exclusion && coerceInt64(target["site_id"]) <= 0)) {
					return invalid
				}
			}
		}
		for _, rule := range row.Rules {
			if rule.RuleType == "" || rule.MatchType == "" || rule.MatchValue == "" {
				return invalid
			}
		}
	}
	return nil
}

func readPatchPolicyDeviceInputs(ctx context.Context, q patchPolicySQL, fields map[string]bool) ([]filterDeviceRow, []patchPolicySoftwareInput, []patchPolicyMetadataInput, error) {
	devices := []filterDeviceRow{}
	software := []patchPolicySoftwareInput{}
	metadata := []patchPolicyMetadataInput{}
	rows, err := q.QueryContext(ctx, `SELECT d.guid,d.hostname,d.description,d.created_at,d.agent_hash,d.memory,d.network,d.software,d.storage,d.cpu,d.device_type,d.domain,d.external_ip,d.internal_ip,d.last_reboot,d.last_seen,d.last_user,d.operating_system,d.uptime,d.agent_id,d.connection_type,d.connection_endpoint,COALESCE(d.status,'active'),s.id,s.name,s.description FROM engine.devices d LEFT JOIN engine.device_sites ds ON ds.device_hostname=d.hostname LEFT JOIN engine.sites s ON s.id=ds.site_id ORDER BY d.guid`)
	if err != nil {
		return nil, nil, nil, err
	}
	for rows.Next() {
		var v filterDeviceRow
		if err = rows.Scan(&v.GUID, &v.Hostname, &v.Description, &v.CreatedAt, &v.AgentHash, &v.MemoryJSON, &v.NetworkJSON, &v.SoftwareJSON, &v.StorageJSON, &v.CPUJSON, &v.DeviceType, &v.Domain, &v.ExternalIP, &v.InternalIP, &v.LastReboot, &v.LastSeen, &v.LastUser, &v.OperatingSystem, &v.Uptime, &v.AgentID, &v.ConnectionType, &v.ConnectionEndpoint, &v.SecurityStatus, &v.SiteID, &v.SiteName, &v.SiteDescription); err != nil {
			_ = rows.Close()
			return nil, nil, nil, err
		}
		devices = append(devices, v)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, nil, nil, err
	}
	if fields["installed_software"] {
		rows, err = q.QueryContext(ctx, `SELECT device_guid,COALESCE(name,''),COALESCE(version,''),COALESCE(source,''),metadata_json FROM engine.device_software_inventory ORDER BY device_guid,name,version,source,metadata_json`)
		if err != nil {
			return nil, nil, nil, err
		}
		for rows.Next() {
			var v patchPolicySoftwareInput
			if err = rows.Scan(&v.GUID, &v.Name, &v.Version, &v.Source, &v.Metadata); err != nil {
				_ = rows.Close()
				return nil, nil, nil, err
			}
			software = append(software, v)
		}
		if err = errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, nil, nil, err
		}
	}
	if fields["metadata_field"] {
		rows, err = q.QueryContext(ctx, `SELECT device_guid,field_number,COALESCE(value,'') FROM engine.device_metadata_fields ORDER BY device_guid,field_number,value`)
		if err != nil {
			return nil, nil, nil, err
		}
		for rows.Next() {
			var v patchPolicyMetadataInput
			if err = rows.Scan(&v.GUID, &v.Field, &v.Value); err != nil {
				_ = rows.Close()
				return nil, nil, nil, err
			}
			metadata = append(metadata, v)
		}
		if err = errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, nil, nil, err
		}
	}
	return devices, software, metadata, nil
}

type patchPolicyPreparedInstall struct {
	ID                        int64
	Name, Components, Targets string
}
type patchPolicyEnforcementDraft struct {
	StateID          int64
	PreviousID       int64
	PreviousMetadata string
	Metadata         string
	WorkID           int64
	Payload          string
	Intent           patchPolicyEnforcementIntent
	Device           patchPolicyDevice
}

func (s *postgresOperatorStore) reservePatchPolicyID(ctx context.Context, table string) (int64, error) {
	if !stringInSet(table, "patch_policy_runs", "patch_policy_device_state", "job_scheduler_work_items", "scheduled_jobs") {
		return 0, errors.New("invalid policy sequence")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return 0, err
	}
	var id int64
	err = conn.QueryRowContext(ctx, `SELECT nextval(pg_get_serial_sequence($1,'id'))`, "engine."+table).Scan(&id)
	return id, errors.Join(err, conn.Close())
}

func (s *postgresOperatorStore) reservePatchPolicyRun(ctx context.Context, policyID, scheduledTS int64) (int64, bool, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return 0, false, err
	}
	var id int64
	err = conn.QueryRowContext(ctx, `SELECT COALESCE(MIN(id),0) FROM engine.patch_policy_runs WHERE policy_id=$1 AND scheduled_ts=$2`, policyID, scheduledTS).Scan(&id)
	err = errors.Join(err, conn.Close())
	if err != nil || id > 0 {
		return id, false, err
	}
	id, err = s.reservePatchPolicyID(ctx, "patch_policy_runs")
	return id, true, err
}

func (s *postgresOperatorStore) preparePatchPolicyEnforcementTargets(ctx context.Context, proof *patchPolicyScopeProof, row patchPolicyRow, devices []patchPolicyDevice, runID, scheduledTS, now int64) ([]patchPolicyEnforcementDraft, error) {
	drafts := []patchPolicyEnforcementDraft{}
	for _, device := range devices {
		guid := normalizeCanonicalGUID(device.DeviceGUID)
		if guid == "" || device.SiteID <= 0 {
			return nil, errors.New("policy enforcement target lacks exact GUID/site")
		}
		mode := "unmanaged"
		if boolInt64(row.ManagedUpdateMode) {
			mode = "managed"
		}
		if stringInSet(device.ExclusionMode, "unmanaged", "frozen") {
			mode = device.ExclusionMode
		}
		intent := patchPolicyEnforcementIntent{Version: 1, PolicyRunID: runID, PolicyID: nullInt(row.ID), ScheduledTS: scheduledTS, DeviceGUID: guid, Hostname: device.Hostname, AgentID: cleanText(proof.Devices[guid]["agent_id"]), SiteID: device.SiteID, Mode: mode, RequestID: fmt.Sprintf("patch-policy-run-%d-device-%s-site-%d", runID, guid, device.SiteID), ScopeSHA256: proof.Bindings[patchPolicyScopeDeviceKey(nullInt(row.ID), guid)]}
		if !intent.valid() && !device.Conflict {
			return nil, errors.New("policy enforcement intent incomplete")
		}
		draft := patchPolicyEnforcementDraft{Intent: intent, Device: device}
		conn, err := s.db.Conn(ctx)
		if err != nil {
			return nil, err
		}
		rows, err := conn.QueryContext(ctx, `SELECT id,COALESCE(metadata_json,'{}') FROM engine.patch_policy_device_state WHERE LOWER(device_guid)=LOWER($1) ORDER BY id`, guid)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		count := 0
		for rows.Next() {
			count++
			if err = rows.Scan(&draft.PreviousID, &draft.PreviousMetadata); err != nil {
				break
			}
		}
		err = errors.Join(err, rows.Err(), rows.Close(), conn.Close())
		if err != nil {
			return nil, err
		}
		if count > 1 {
			return nil, errors.New("ambiguous policy state identity")
		}
		previous := map[string]any{}
		state := patchPolicyEnforcementState{Version: 1}
		if count > 0 {
			fields, err := schedulerAdmissionMetadata(draft.PreviousMetadata)
			if err != nil {
				return nil, err
			}
			if raw, ok := fields[patchPolicyEnforcementKey]; ok {
				if json.Unmarshal(raw, &state) != nil || state.Version != 1 {
					return nil, errors.New("invalid retained policy predecessor")
				}
			}
			if json.Unmarshal([]byte(draft.PreviousMetadata), &previous) != nil {
				return nil, errors.New("invalid retained policy state")
			}
			draft.StateID = draft.PreviousID
		} else {
			draft.StateID, err = s.reservePatchPolicyID(ctx, "patch_policy_device_state")
			if err != nil {
				return nil, err
			}
		}
		if !device.Conflict {
			draft.WorkID, err = s.reservePatchPolicyID(ctx, "job_scheduler_work_items")
			if err != nil {
				return nil, err
			}
		}
		state.DesiredWorkID = draft.WorkID
		state.ReceiptSHA256 = ""
		// Preserve the original active pointer even across site/policy changes.
		// The retained work scan remains authoritative if this state was deleted.
		previous[patchPolicyEnforcementKey] = state
		previous["policy_type"] = normalizePatchPolicyType(row.PolicyType.String)
		previous["policy_run_id"] = runID
		previous["site_id"] = device.SiteID
		previous["site_name"] = device.SiteName
		previous["conflicted"] = device.Conflict
		previous["hierarchy_policy_ids"] = device.HierarchyPolicyIDs
		previous["exclusion_policy_id"] = nullablePositiveInt64Any(device.ExclusionPolicyID)
		previous["exclusion_override_policy_id"] = nullablePositiveInt64Any(device.ExclusionOverridePolicyID)
		raw, err := json.Marshal(previous)
		if err != nil {
			return nil, err
		}
		draft.Metadata = string(raw)
		if !device.Conflict {
			raw, err = json.Marshal(map[string]any{patchPolicyEnforcementKey: intent})
			if err != nil {
				return nil, err
			}
			draft.Payload = string(raw)
		}
		drafts = append(drafts, draft)
	}
	sort.Slice(drafts, func(i, j int) bool { return drafts[i].Intent.DeviceGUID < drafts[j].Intent.DeviceGUID })
	return drafts, nil
}

func (s *postgresOperatorStore) commitPatchPolicyRun(ctx context.Context, proof *patchPolicyScopeProof, row patchPolicyRow, runID, scheduledTS, now int64, drafts []patchPolicyEnforcementDraft, jobs []patchPolicyPreparedInstall, summary string) (int64, bool, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return 0, false, err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	var policyID int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM engine.patch_policies WHERE id=$1 FOR UPDATE`, nullInt(row.ID)).Scan(&policyID); err != nil {
		return 0, false, err
	}
	var existing int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MIN(id),0) FROM engine.patch_policy_runs WHERE policy_id=$1 AND scheduled_ts=$2`, policyID, scheduledTS).Scan(&existing); err != nil {
		return 0, false, err
	}
	if existing > 0 {
		return existing, false, tx.Commit()
	}
	for _, draft := range drafts {
		if err = lockPatchPolicyDevice(ctx, tx, draft.Intent.DeviceGUID); err != nil {
			return 0, false, err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,COALESCE(metadata_json,'{}') FROM engine.patch_policy_device_state WHERE LOWER(device_guid)=LOWER($1) ORDER BY id FOR UPDATE`, draft.Intent.DeviceGUID)
		if err != nil {
			return 0, false, err
		}
		count := 0
		var id int64
		var raw string
		for rows.Next() {
			count++
			if err = rows.Scan(&id, &raw); err != nil {
				break
			}
		}
		err = errors.Join(err, rows.Err(), rows.Close())
		if err != nil {
			return 0, false, err
		}
		if count > 1 || id != draft.PreviousID || raw != draft.PreviousMetadata {
			return 0, false, errPatchPolicyScopeChanged
		}
	}
	if err = proof.check(ctx, tx); err != nil {
		return 0, false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO engine.patch_policy_runs(id,policy_id,scheduled_ts,started_at,status,finished_at,summary_json,created_at) VALUES($1,$2,$3,$4,'scheduled',$4,$5,$4)`, runID, policyID, scheduledTS, now, summary); err != nil {
		return 0, false, err
	}
	for _, draft := range drafts {
		if _, err = tx.ExecContext(ctx, `INSERT INTO engine.patch_policy_device_state(id,device_guid,hostname,effective_policy_id,exclusion_mode,enforcement_mode,enforcement_status,drift_detected,last_evaluated_at,metadata_json) VALUES($1,$2,$3,$4,$5,$6,'pending',0,$7,$8) ON CONFLICT(id) DO UPDATE SET device_guid=EXCLUDED.device_guid,hostname=EXCLUDED.hostname,effective_policy_id=EXCLUDED.effective_policy_id,exclusion_mode=EXCLUDED.exclusion_mode,enforcement_mode=EXCLUDED.enforcement_mode,enforcement_status=EXCLUDED.enforcement_status,drift_detected=0,last_evaluated_at=EXCLUDED.last_evaluated_at,metadata_json=EXCLUDED.metadata_json`, draft.StateID, draft.Intent.DeviceGUID, draft.Intent.Hostname, policyID, nullEmpty(draft.Device.ExclusionMode), draft.Intent.Mode, now, draft.Metadata); err != nil {
			return 0, false, err
		}
		if draft.WorkID > 0 {
			if _, err = tx.ExecContext(ctx, `INSERT INTO engine.job_scheduler_work_items(id,dedupe_key,kind,site_id,lane,payload_json,status,attempt_count,priority,available_at,created_at,updated_at) VALUES($1,$2,$3,$4,'scheduled_job',$5,'queued',0,45,$6,$6,$6)`, draft.WorkID, fmt.Sprintf("patch-policy:%d:%s:%d", runID, draft.Intent.DeviceGUID, draft.Intent.SiteID), schedulerKindPatchPolicyEnforcement, draft.Intent.SiteID, draft.Payload, now); err != nil {
				return 0, false, err
			}
		}
	}
	for _, job := range jobs {
		if _, err = tx.ExecContext(ctx, `INSERT INTO engine.scheduled_jobs(id,name,components_json,targets_json,schedule_type,start_ts,duration_stop_enabled,expiration,execution_context,credential_id,use_service_account,job_kind,enabled,created_at,updated_at) VALUES($1,$2,$3,$4,'immediately',$5,0,'no_expire','system',NULL,0,$6,1,$7,$7)`, job.ID, job.Name, job.Components, job.Targets, scheduledTS, scheduledJobKindPatchInstall, now); err != nil {
			return 0, false, err
		}
	}
	return runID, true, tx.Commit()
}

func patchPolicyIntentFromPayload(payload map[string]any) (patchPolicyEnforcementIntent, error) {
	var intent patchPolicyEnforcementIntent
	raw, err := json.Marshal(payload[patchPolicyEnforcementKey])
	if err != nil {
		return intent, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&intent); err != nil || !intent.valid() {
		return intent, errors.New("invalid policy enforcement work")
	}
	return intent, nil
}

func (m *goSchedulerManager) processPatchPolicyEnforcementWork(ctx context.Context) error {
	item, err := m.claimNextKindWorkItem(ctx, []string{schedulerKindPatchPolicyEnforcement})
	if err != nil || item == nil {
		return err
	}
	m.startClaimedWork(ctx, *item, m.runPatchPolicyEnforcementWorkItem)
	return nil
}

type patchPolicyRouteProof struct {
	Route     agentWorkerRoute
	Dispatch  patchPolicyEnforcementDispatch
	Placement string
}

func (m *goSchedulerManager) readPatchPolicyRoute(ctx context.Context, intent patchPolicyEnforcementIntent) (patchPolicyRouteProof, error) {
	var proof patchPolicyRouteProof
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return proof, err
	}
	rows, err := conn.QueryContext(ctx, `SELECT worker_guid,site_id,route_path_prefix,upstream_scheme,upstream_host,upstream_port,generation,COALESCE(metadata_json::jsonb->'worker_placement','null'::jsonb)::text FROM engine.job_scheduler_worker_routes WHERE site_id=$1 AND status='active' ORDER BY worker_guid`, intent.SiteID)
	if err != nil {
		_ = conn.Close()
		return proof, err
	}
	count := 0
	for rows.Next() {
		count++
		r := &proof.Route
		if err = rows.Scan(&r.WorkerGUID, &r.SiteID, &r.RoutePathPrefix, &r.UpstreamScheme, &r.UpstreamHost, &r.UpstreamPort, &r.Generation, &proof.Placement); err != nil {
			break
		}
	}
	err = errors.Join(err, rows.Err(), rows.Close(), conn.Close())
	if err != nil {
		return proof, err
	}
	if count != 1 {
		return proof, errors.New("policy worker route is ambiguous or unavailable")
	}
	placement, err := schedulerAdmissionMetadata(proof.Placement)
	if err != nil {
		return proof, err
	}
	str := func(key string) string { var v string; _ = json.Unmarshal(placement[key], &v); return v }
	num := func(key string) int64 { v, _ := strconv.ParseInt(string(placement[key]), 10, 64); return v }
	if string(placement["version"]) != "1" || str("worker_guid") != proof.Route.WorkerGUID || num("site_id") != intent.SiteID || num("route_generation") != proof.Route.Generation ||
		!clusterUUIDRE.MatchString(str("pod_uid")) || !schedulerPlacementName(str("node_name")) {
		return proof, errors.New("policy worker placement invalid")
	}
	proof.Dispatch = patchPolicyEnforcementDispatch{WorkerGUID: proof.Route.WorkerGUID, RouteGeneration: proof.Route.Generation, PodUID: str("pod_uid"), NodeName: str("node_name")}
	return proof, nil
}

func (p patchPolicyRouteProof) check(ctx context.Context, q patchPolicySQL) error {
	var count int
	r := p.Route
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM engine.job_scheduler_worker_routes WHERE site_id=$1 AND status='active'`, r.SiteID).Scan(&count)
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("policy route changed")
	}
	err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM engine.job_scheduler_worker_routes WHERE worker_guid=$1 AND site_id=$2 AND status='active' AND generation=$3 AND route_path_prefix=$4 AND upstream_scheme=$5 AND upstream_host=$6 AND upstream_port=$7 AND COALESCE(metadata_json::jsonb->'worker_placement','null'::jsonb)::text=$8`, r.WorkerGUID, r.SiteID, r.Generation, r.RoutePathPrefix, r.UpstreamScheme, r.UpstreamHost, r.UpstreamPort, p.Placement).Scan(&count)
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("policy route changed")
	}
	return nil
}

// Existing work remains durable after policy/state deletion. Only this private
// projector's verified original receipt closes a launched predecessor. Delivery
// acknowledgements, failed queue status and newer desired state cannot do so.
func patchPolicyPredecessorPending(ctx context.Context, q patchPolicySQL, guid string, workID int64) (bool, error) {
	var pending bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM engine.job_scheduler_work_items w WHERE w.id<>$1 AND w.kind=$2 AND w.payload_json::jsonb->'policy_enforcement'->>'device_guid'=$3 AND COALESCE(w.payload_json::jsonb->>'policy_terminal','')='' AND EXISTS(SELECT 1 FROM jsonb_each(COALESCE(w.payload_json::jsonb->'executions','{}'::jsonb)) e WHERE e.value->>'state' IN ('dispatching','acknowledged')))`, workID, schedulerKindPatchPolicyEnforcement, guid).Scan(&pending)
	return pending, err
}

func patchPolicyCurrentState(ctx context.Context, q patchPolicySQL, intent patchPolicyEnforcementIntent, workID int64) (int64, error) {
	if err := lockPatchPolicyDevice(ctx, q, intent.DeviceGUID); err != nil {
		return 0, err
	}
	var count int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM engine.devices d JOIN engine.device_sites ds ON ds.device_hostname=d.hostname WHERE d.hostname=$1 AND UPPER(d.guid)=$2 AND ds.site_id=$3 AND d.agent_id=$4 AND COALESCE(d.status,'active')='active'`, intent.Hostname, intent.DeviceGUID, intent.SiteID, intent.AgentID).Scan(&count); err != nil {
		return 0, err
	}
	if count != 1 {
		return 0, errPatchPolicyScopeChanged
	}
	rows, err := q.QueryContext(ctx, `SELECT id FROM engine.patch_policy_device_state WHERE LOWER(device_guid)=LOWER($1) FOR UPDATE`, intent.DeviceGUID)
	if err != nil {
		return 0, err
	}
	count = 0
	var id int64
	for rows.Next() {
		count++
		if err = rows.Scan(&id); err != nil {
			break
		}
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return 0, err
	}
	if count != 1 {
		return 0, errPatchPolicyScopeChanged
	}
	if err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM engine.patch_policy_device_state WHERE id=$1 AND device_guid=$2 AND hostname=$3 AND effective_policy_id=$4 AND enforcement_mode=$5 AND metadata_json::jsonb->>'site_id'=$6 AND metadata_json::jsonb->'policy_enforcement'->>'desired_work_id'=$7`, id, intent.DeviceGUID, intent.Hostname, intent.PolicyID, intent.Mode, strconv.FormatInt(intent.SiteID, 10), strconv.FormatInt(workID, 10)).Scan(&count); err != nil {
		return 0, err
	}
	if count != 1 {
		return 0, errPatchPolicyScopeChanged
	}
	return id, nil
}

func lockPatchPolicyDevice(ctx context.Context, q patchPolicySQL, guid string) error {
	rows, err := q.QueryContext(ctx, `SELECT guid FROM engine.devices WHERE UPPER(guid)=$1 ORDER BY guid FOR UPDATE`, guid)
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		var stored string
		if err = rows.Scan(&stored); err != nil {
			break
		}
		count++
		if normalizeCanonicalGUID(stored) != guid || !strings.EqualFold(stored, guid) {
			err = errPatchPolicyScopeChanged
			break
		}
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	if count != 1 {
		return errPatchPolicyScopeChanged
	}
	return nil
}

func patchPolicyHostServiceBody(intent patchPolicyEnforcementIntent, activityID int64) map[string]any {
	return map[string]any{"hostname": intent.Hostname, "service_mode": "system", "event_name": "patch_policy_enforcement_request", "allow_pending": false, "timeout_seconds": int64(30), "payload": map[string]any{"job_id": activityID, "hostname": intent.Hostname, "agent_id": intent.AgentID, "mode": intent.Mode, "request_id": intent.RequestID, "requested_by": "job-scheduler", "context": map[string]any{"policy_run_id": intent.PolicyRunID, "policy_id": intent.PolicyID}}}
}

func (m *goSchedulerManager) callPatchPolicyEnforcement(ctx context.Context, route *agentWorkerRoute, intent patchPolicyEnforcementIntent, activityID int64) (map[string]any, string, error) {
	// A redirect can replay an already accepted POST. Copy client policy without
	// mutating the shared client, transport, credentials or connection ownership.
	sender := *m
	client := m.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	sender.httpClient = &copyClient
	return sender.callSiteWorkerHostService(ctx, route, patchPolicyHostServiceBody(intent, activityID), 30*time.Second)
}

func (m *goSchedulerManager) runPatchPolicyEnforcementWorkItem(ctx context.Context, item schedulerWorkItem) error {
	intent, err := patchPolicyIntentFromPayload(item.Payload)
	if err != nil {
		return err
	}
	if item.Kind != schedulerKindPatchPolicyEnforcement || !item.SiteID.Valid || item.SiteID.Int64 != intent.SiteID {
		return errors.New("policy work routing mismatch")
	}
	proof, err := m.store.readPatchPolicyScopeProof(ctx, operatorProfile{Username: "job-scheduler", Role: "Admin"})
	if err != nil {
		return err
	}
	if !proof.matches(intent) {
		return errPatchPolicyScopeChanged
	}
	route, err := m.readPatchPolicyRoute(ctx, intent)
	if err != nil {
		return err
	}
	metadata, err := json.Marshal(map[string]any{patchPolicyEnforcementKey: intent, "scheduler_work_id": item.ID, "scheduler_holder": item.LeaseOwner, "scheduler_generation": item.AttemptCount, "scheduler_execution_id": fmt.Sprintf("work:%d:%s", item.ID, schedulerExecutionKey(ctx))})
	if err != nil {
		return err
	}
	dispatchRaw, err := json.Marshal(route.Dispatch)
	if err != nil {
		return err
	}
	intentRaw := stringIntent(intent)
	activityID, err := func() (int64, error) {
		tx, owned, cleanup, err := m.beginOwnedWorkTx(ctx)
		if err != nil {
			return 0, err
		}
		defer cleanup()
		record, err := loadSchedulerExecution(ctx, tx, owned)
		if err != nil {
			return 0, err
		}
		if record.State != "prepared" {
			return 0, errSchedulerOutcomeUnknown
		}
		if record.ActivityID != 0 {
			return 0, errors.New("prepared policy work already has activity custody")
		}
		var exact int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM engine.job_scheduler_work_items WHERE id=$1 AND kind=$2 AND site_id=$3 AND payload_json::jsonb->'policy_enforcement'=$4::jsonb`, item.ID, item.Kind, intent.SiteID, intentRaw).Scan(&exact); err != nil {
			return 0, err
		}
		if exact != 1 {
			return 0, errors.New("policy work intent changed")
		}
		stateID, err := patchPolicyCurrentState(ctx, tx, intent, item.ID)
		if err != nil {
			return 0, err
		}
		pending, err := patchPolicyPredecessorPending(ctx, tx, intent.DeviceGUID, item.ID)
		if err != nil {
			return 0, err
		}
		if pending {
			return 0, errSchedulerOutcomeUnknown
		}
		if record.ActivityID == 0 {
			if err = tx.QueryRowContext(ctx, `INSERT INTO engine.activity_history(hostname,script_path,script_name,script_type,ran_at,status,stdout,stderr,queue_lane,activity_kind,metadata_json,started_at,updated_at) VALUES($1,'Internal/Patch_Management','Patch policy enforcement','patch_policy_enforcement',$2,'Running','','','scheduled_job_system','patch_policy_enforcement',$3,$2,$2) RETURNING id`, intent.Hostname, time.Now().Unix(), string(metadata)).Scan(&record.ActivityID); err != nil {
				return 0, err
			}
		}
		record.State = "dispatching"
		record.Holder = owned.LeaseOwner
		record.Generation = owned.AttemptCount
		record.ResultID = intent.RequestID
		if err = storeSchedulerExecution(ctx, tx, owned, record); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE engine.job_scheduler_work_items SET payload_json=jsonb_set(payload_json::jsonb,'{policy_dispatch}',$2::jsonb,true)::text WHERE id=$1`, item.ID, string(dispatchRaw)); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE engine.patch_policy_device_state SET metadata_json=jsonb_set(metadata_json::jsonb,'{policy_enforcement,active_work_id}',to_jsonb($2::bigint),true)::text WHERE id=$1`, stateID, item.ID); err != nil {
			return 0, err
		}
		// Device/activity writes can wait while the original lease ages. Admission
		// at transaction entry is not authority to send after that lease expires.
		if err = proof.check(ctx, tx); err != nil {
			return 0, err
		}
		if err = route.check(ctx, tx); err != nil {
			return 0, err
		}
		if err = requireSchedulerWork(ctx, tx, owned); err != nil {
			return 0, err
		}
		return record.ActivityID, tx.Commit()
	}()
	if err != nil {
		return err
	}
	// Scope transaction and its connection have ended before request shaping and
	// transport. Even an immediate successful ACK leaves original custody pending.
	_, _, callErr := m.callPatchPolicyEnforcement(ctx, &route.Route, intent, activityID)
	return errors.Join(errSchedulerOutcomeUnknown, callErr)
}

type patchPolicyReceiptCandidate struct {
	ID      int64
	Payload string
}
type patchPolicyActivityReceipt struct {
	Hostname, Status, Stdout, Stderr, Metadata string
	StdoutPresent, StderrPresent               bool
}

func (m *goSchedulerManager) reconcilePatchPolicyEnforcementReceipts(ctx context.Context, now int64) error {
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return err
	}
	rows, err := conn.QueryContext(ctx, `SELECT id,payload_json FROM engine.job_scheduler_work_items WHERE kind=$1 AND payload_json::jsonb ? 'policy_dispatch' AND NOT(payload_json::jsonb ? 'policy_terminal') ORDER BY id`, schedulerKindPatchPolicyEnforcement)
	if err != nil {
		_ = conn.Close()
		return err
	}
	candidates := []patchPolicyReceiptCandidate{}
	for rows.Next() {
		var v patchPolicyReceiptCandidate
		if err = rows.Scan(&v.ID, &v.Payload); err != nil {
			break
		}
		candidates = append(candidates, v)
	}
	err = errors.Join(err, rows.Err(), rows.Close(), conn.Close())
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		if err = m.reconcilePatchPolicyReceipt(ctx, candidate, now); err != nil {
			return err
		}
	}
	return nil
}

func (m *goSchedulerManager) reconcilePatchPolicyReceipt(ctx context.Context, candidate patchPolicyReceiptCandidate, now int64) error {
	var payload map[string]any
	if json.Unmarshal([]byte(candidate.Payload), &payload) != nil {
		return errors.New("invalid retained policy work")
	}
	intent, err := patchPolicyIntentFromPayload(payload)
	if err != nil {
		return err
	}
	var dispatch patchPolicyEnforcementDispatch
	raw, err := json.Marshal(payload["policy_dispatch"])
	if err != nil {
		return err
	}
	if json.Unmarshal(raw, &dispatch) != nil {
		return errors.New("invalid retained policy dispatch")
	}
	var record schedulerExecutionRecord
	raw, err = json.Marshal(schedulerAnyMap(payload["executions"])["work"])
	if err != nil {
		return err
	}
	if json.Unmarshal(raw, &record) != nil || record.ID != fmt.Sprintf("work:%d:work", candidate.ID) || record.ResultID != intent.RequestID || record.ActivityID <= 0 || !stringInSet(record.State, "dispatching", "acknowledged") {
		return errors.New("invalid retained policy execution")
	}
	var receipt patchPolicyActivityReceipt
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return err
	}
	err = conn.QueryRowContext(ctx, `SELECT COALESCE(hostname,''),COALESCE(status,''),COALESCE(stdout,''),COALESCE(stderr,''),COALESCE(metadata_json,'{}'),stdout IS NOT NULL,stderr IS NOT NULL FROM engine.activity_history WHERE id=$1`, record.ActivityID).Scan(&receipt.Hostname, &receipt.Status, &receipt.Stdout, &receipt.Stderr, &receipt.Metadata, &receipt.StdoutPresent, &receipt.StderrPresent)
	err = errors.Join(err, conn.Close())
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if receipt.Hostname != intent.Hostname || !receipt.StdoutPresent || !receipt.StderrPresent {
		return nil
	}
	if !patchPolicyActivityBound(receipt.Metadata, intent, candidate.ID, record) {
		return nil
	}
	digest, err := patchPolicyCommittedReceipt(intent, dispatch, record.ActivityID, receipt.Status, receipt.Stdout, receipt.Stderr, receipt.Metadata)
	if err != nil {
		return nil
	}
	proof, scopeErr := m.store.readPatchPolicyScopeProof(ctx, operatorProfile{Username: "job-scheduler", Role: "Admin"})
	current := scopeErr == nil && proof.matches(intent)
	var route patchPolicyRouteProof
	if current {
		route, err = m.readPatchPolicyRoute(ctx, intent)
		current = err == nil && route.Dispatch == dispatch
	}
	// This marker records only original command closure. A stale/deleted policy
	// cannot project applied, but its verified predecessor must remain discoverable.
	payload["policy_terminal"] = digest
	record.State = "acknowledged"
	executions := schedulerAnyMap(payload["executions"])
	executions["work"] = record
	payload["executions"] = executions
	nextPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	conn, err = m.ownershipPool().Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = requireSchedulerLeadership(ctx, tx); err != nil {
		return err
	}
	var retained string
	if err = tx.QueryRowContext(ctx, `SELECT payload_json FROM engine.job_scheduler_work_items WHERE id=$1 AND kind=$2 FOR UPDATE`, candidate.ID, schedulerKindPatchPolicyEnforcement).Scan(&retained); err != nil {
		return err
	}
	if retained != candidate.Payload {
		return nil
	}
	var stateID int64
	if current {
		stateID, err = patchPolicyCurrentState(ctx, tx, intent, candidate.ID)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, errPatchPolicyScopeChanged) {
				return err
			}
			current = false
		}
	}
	var locked patchPolicyActivityReceipt
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(hostname,''),COALESCE(status,''),COALESCE(stdout,''),COALESCE(stderr,''),COALESCE(metadata_json,'{}'),stdout IS NOT NULL,stderr IS NOT NULL FROM engine.activity_history WHERE id=$1 FOR UPDATE`, record.ActivityID).Scan(&locked.Hostname, &locked.Status, &locked.Stdout, &locked.Stderr, &locked.Metadata, &locked.StdoutPresent, &locked.StderrPresent); err != nil {
		return err
	}
	if locked != receipt {
		return nil
	}
	if _, err = tx.ExecContext(ctx, `UPDATE engine.job_scheduler_work_items SET payload_json=$2,updated_at=$3 WHERE id=$1`, candidate.ID, string(nextPayload), now); err != nil {
		return err
	}
	if current {
		status := "failed"
		if receipt.Status == "Success" {
			status = "applied"
		}
		if _, err = tx.ExecContext(ctx, `UPDATE engine.patch_policy_device_state SET enforcement_status=$2,last_enforced_at=CASE WHEN $2='applied' THEN $3 ELSE last_enforced_at END,metadata_json=jsonb_set(jsonb_set(metadata_json::jsonb,'{policy_enforcement,receipt_sha256}',to_jsonb($4::text),true),'{policy_enforcement,active_work_id}','0'::jsonb,true)::text WHERE id=$1`, stateID, status, now, digest); err != nil {
			return err
		}
		if err = proof.check(ctx, tx); err != nil {
			return err
		}
		if err = route.check(ctx, tx); err != nil {
			return err
		}
	}
	if _, err = requireSchedulerLeadership(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func stringIntent(intent patchPolicyEnforcementIntent) string {
	raw, _ := json.Marshal(intent)
	return string(raw)
}

func patchPolicyActivityBound(metadata string, intent patchPolicyEnforcementIntent, workID int64, record schedulerExecutionRecord) bool {
	fields, err := schedulerAdmissionMetadata(metadata)
	if err != nil {
		return false
	}
	var retained patchPolicyEnforcementIntent
	if json.Unmarshal(fields[patchPolicyEnforcementKey], &retained) != nil || retained != intent {
		return false
	}
	var executionID, holder string
	if json.Unmarshal(fields["scheduler_execution_id"], &executionID) != nil || json.Unmarshal(fields["scheduler_holder"], &holder) != nil {
		return false
	}
	return executionID == record.ID && holder == record.Holder && string(fields["scheduler_work_id"]) == strconv.FormatInt(workID, 10) && string(fields["scheduler_generation"]) == strconv.FormatInt(record.Generation, 10)
}
