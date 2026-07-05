package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	patchPolicyTypeGlobal       = "global"
	patchPolicyTypeSite         = "site"
	patchPolicyTypeDeviceFilter = "device_filter"

	patchPolicyEvaluateTimeout = 60 * time.Second

	patchPolicyRoleServer      = "Server"
	patchPolicyRoleWorkstation = "Workstation"
	patchPolicyRoleBoth        = "Both"

	patchPolicyRuleApprove = "approve"
	patchPolicyRuleBlock   = "block"

	patchPolicyExclusionUnmanaged = "unmanaged"
	patchPolicyExclusionFrozen    = "frozen"
	patchPolicyExclusionOverride  = "managed_override"
)

type patchPolicyStore interface {
	listPatchPolicies(ctx context.Context, profile operatorProfile, policyType string) ([]map[string]any, error)
	patchPolicyMetadata(ctx context.Context, profile operatorProfile) (map[string]any, error)
	getPatchPolicy(ctx context.Context, profile operatorProfile, policyID int64) (map[string]any, bool, error)
	savePatchPolicy(ctx context.Context, profile operatorProfile, policyID *int64, body map[string]any) (map[string]any, int, error)
	deletePatchPolicy(ctx context.Context, profile operatorProfile, policyID int64) (map[string]any, int, error)
	previewPatchPolicy(ctx context.Context, profile operatorProfile, policyID *int64, body map[string]any) (map[string]any, int, error)
	effectivePatchPolicy(ctx context.Context, profile operatorProfile, hostname string) (map[string]any, int, error)
	evaluatePatchPolicies(ctx context.Context, profile operatorProfile, body map[string]any) (map[string]any, int, error)
}

type patchPolicyRow struct {
	ID                    sql.NullInt64
	Name                  sql.NullString
	Description           sql.NullString
	PolicyType            sql.NullString
	Enabled               sql.NullInt64
	Locked                sql.NullInt64
	RoleScope             sql.NullString
	ApprovalMode          sql.NullString
	DeferralDays          sql.NullInt64
	ManagedUpdateMode     sql.NullInt64
	InstallScheduleType   sql.NullString
	InstallStartTS        sql.NullInt64
	RebootAfterInstall    sql.NullInt64
	RebootScheduleEnabled sql.NullInt64
	RebootScheduleType    sql.NullString
	RebootStartTS         sql.NullInt64
	ForceRebootLoggedIn   sql.NullInt64
	CreatedBy             sql.NullString
	UpdatedBy             sql.NullString
	CreatedAt             sql.NullInt64
	UpdatedAt             sql.NullInt64
	Sites                 []map[string]any
	Targets               []map[string]any
	Exclusions            []map[string]any
	Rules                 []patchPolicyRule
}

type patchPolicyRule struct {
	ID                  int64
	RuleType            string
	MatchType           string
	MatchValue          string
	OverrideParentBlock bool
	Notes               string
	CreatedBy           string
	CreatedAt           int64
}

type patchPolicyTargetRef struct {
	TargetType string
	DeviceGUID string
	Hostname   string
	FilterID   int64
	TargetJSON string
}

type patchPolicyExclusionRef struct {
	ExclusionType string
	TargetType    string
	DeviceGUID    string
	Hostname      string
	SiteID        int64
	SiteName      string
	FilterID      int64
	Reason        string
	CreatedBy     string
}

type patchPolicyDevice struct {
	DeviceGUID                string
	Hostname                  string
	SiteID                    int64
	SiteName                  string
	DeviceType                string
	OperatingSystem           string
	FilterIDs                 []int64
	ExclusionMode             string
	ExclusionPolicyID         int64
	ExclusionOverridePolicyID int64
	HierarchyPolicyIDs        []int64
	Conflict                  bool
}

type patchPolicyDeviceResolution struct {
	Raw      []patchPolicyDevice
	Eligible []patchPolicyDevice
}

type patchPolicyEffectiveScope struct {
	Policy        patchPolicyRow
	Devices       []patchPolicyDevice
	RulesByDevice map[string][]patchPolicyRule
}

func patchPoliciesRootHandler(auth *authService, fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			patchPolicyList(w, r, auth)
		case http.MethodPost:
			patchPolicySave(w, r, auth, nil)
		default:
			fallback.ServeHTTP(w, r)
		}
	}
}

func patchPoliciesSubtreeHandler(auth *authService, fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/patches/policies/"), "/"), "/")
		if len(parts) == 0 || parts[0] == "" {
			fallback.ServeHTTP(w, r)
			return
		}
		switch parts[0] {
		case "metadata":
			if len(parts) == 1 && r.Method == http.MethodGet {
				patchPolicyMetadataHandler(w, r, auth)
				return
			}
		case "evaluate":
			if len(parts) == 1 && r.Method == http.MethodPost {
				patchPolicyEvaluate(w, r, auth)
				return
			}
		case "effective":
			if len(parts) == 1 && r.Method == http.MethodGet {
				patchPolicyEffective(w, r, auth)
				return
			}
		case "conflicts":
			if len(parts) == 1 && r.Method == http.MethodPost {
				patchPolicyPreview(w, r, auth, nil)
				return
			}
		}
		policyID, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || policyID <= 0 {
			fallback.ServeHTTP(w, r)
			return
		}
		switch {
		case len(parts) == 1 && r.Method == http.MethodGet:
			patchPolicyDetail(w, r, auth, policyID)
		case len(parts) == 1 && r.Method == http.MethodPut:
			patchPolicySave(w, r, auth, &policyID)
		case len(parts) == 1 && r.Method == http.MethodDelete:
			patchPolicyDelete(w, r, auth, policyID)
		case len(parts) == 2 && parts[1] == "preview" && r.Method == http.MethodPost:
			patchPolicyPreview(w, r, auth, &policyID)
		default:
			fallback.ServeHTTP(w, r)
		}
	}
}

func patchPolicyRequestContext(w http.ResponseWriter, r *http.Request, auth *authService) (operatorProfile, patchPolicyStore, bool) {
	profile, err := auth.currentProfile(r.Context(), r)
	if err != nil {
		if isUnauthorizedAuthError(err) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return operatorProfile{}, nil, false
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
		return operatorProfile{}, nil, false
	}
	store, ok := auth.store.(patchPolicyStore)
	if !ok {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "patch_policy_unavailable"})
		return operatorProfile{}, nil, false
	}
	return profile, store, true
}

func patchPolicyList(w http.ResponseWriter, r *http.Request, auth *authService) {
	profile, store, ok := patchPolicyRequestContext(w, r, auth)
	if !ok {
		return
	}
	ctx, cancel := requestTimeout(r.Context(), auth)
	defer cancel()
	policies, err := store.listPatchPolicies(ctx, profile, normalizePatchPolicyType(r.URL.Query().Get("type")))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "patch_policy_list_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policies": policies, "count": len(policies)})
}

func patchPolicyMetadataHandler(w http.ResponseWriter, r *http.Request, auth *authService) {
	profile, store, ok := patchPolicyRequestContext(w, r, auth)
	if !ok {
		return
	}
	ctx, cancel := requestTimeout(r.Context(), auth)
	defer cancel()
	payload, err := store.patchPolicyMetadata(ctx, profile)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "patch_policy_metadata_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func patchPolicyDetail(w http.ResponseWriter, r *http.Request, auth *authService, policyID int64) {
	profile, store, ok := patchPolicyRequestContext(w, r, auth)
	if !ok {
		return
	}
	ctx, cancel := requestTimeout(r.Context(), auth)
	defer cancel()
	policy, found, err := store.getPatchPolicy(ctx, profile, policyID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "patch_policy_detail_failed", "message": err.Error()})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policy": policy})
}

func patchPolicySave(w http.ResponseWriter, r *http.Request, auth *authService, policyID *int64) {
	profile, store, ok := patchPolicyRequestContext(w, r, auth)
	if !ok {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(profile.Role), "admin") {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden", "message": "Administrator permissions are required for patch policy changes."})
		return
	}
	body, err := readJSONMap(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json", "message": "Request body must be valid JSON."})
		return
	}
	ctx, cancel := requestTimeout(r.Context(), auth)
	defer cancel()
	payload, status, err := store.savePatchPolicy(ctx, profile, policyID, body)
	if err != nil {
		writeJSON(w, status, payload)
		return
	}
	writeJSON(w, status, payload)
}

func patchPolicyDelete(w http.ResponseWriter, r *http.Request, auth *authService, policyID int64) {
	profile, store, ok := patchPolicyRequestContext(w, r, auth)
	if !ok {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(profile.Role), "admin") {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden", "message": "Administrator permissions are required for patch policy changes."})
		return
	}
	ctx, cancel := requestTimeout(r.Context(), auth)
	defer cancel()
	payload, status, err := store.deletePatchPolicy(ctx, profile, policyID)
	if err != nil {
		writeJSON(w, status, payload)
		return
	}
	writeJSON(w, status, payload)
}

func patchPolicyPreview(w http.ResponseWriter, r *http.Request, auth *authService, policyID *int64) {
	profile, store, ok := patchPolicyRequestContext(w, r, auth)
	if !ok {
		return
	}
	body, err := readJSONMap(r)
	if err != nil {
		body = map[string]any{}
	}
	ctx, cancel := requestTimeout(r.Context(), auth)
	defer cancel()
	payload, status, err := store.previewPatchPolicy(ctx, profile, policyID, body)
	if err != nil {
		writeJSON(w, status, payload)
		return
	}
	writeJSON(w, status, payload)
}

func patchPolicyEffective(w http.ResponseWriter, r *http.Request, auth *authService) {
	profile, store, ok := patchPolicyRequestContext(w, r, auth)
	if !ok {
		return
	}
	hostname := firstText(cleanText(r.URL.Query().Get("hostname")), cleanText(r.URL.Query().Get("device")))
	ctx, cancel := requestTimeout(r.Context(), auth)
	defer cancel()
	payload, status, err := store.effectivePatchPolicy(ctx, profile, hostname)
	if err != nil {
		writeJSON(w, status, payload)
		return
	}
	writeJSON(w, status, payload)
}

func patchPolicyEvaluate(w http.ResponseWriter, r *http.Request, auth *authService) {
	profile, store, ok := patchPolicyRequestContext(w, r, auth)
	if !ok {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(profile.Role), "admin") {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden", "message": "Administrator permissions are required for manual policy evaluation."})
		return
	}
	body, err := readJSONMap(r)
	if err != nil {
		body = map[string]any{}
	}
	ctx, cancel := patchPolicyEvaluateContext(r.Context(), auth)
	defer cancel()
	payload, status, err := store.evaluatePatchPolicies(ctx, profile, body)
	if err != nil {
		writeJSON(w, status, payload)
		return
	}
	writeJSON(w, status, payload)
}

func patchPolicyEvaluateContext(ctx context.Context, auth *authService) (context.Context, context.CancelFunc) {
	timeout := patchPolicyEvaluateTimeout
	if auth != nil && auth.timeout > timeout {
		timeout = auth.timeout
	}
	return context.WithTimeout(ctx, timeout)
}

func (s *postgresOperatorStore) listPatchPolicies(ctx context.Context, profile operatorProfile, policyType string) ([]map[string]any, error) {
	if err := s.ensurePatchPolicySchema(ctx); err != nil {
		return nil, err
	}
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	rows, err := loadPatchPolicyRows(ctx, conn, policyType, nil)
	closeErr := conn.Close()
	if err != nil {
		if closeErr != nil {
			return nil, errors.Join(err, closeErr)
		}
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	indexRows := rows
	if policyType != "" {
		conn, err = s.db.Conn(ctx)
		if err != nil {
			return nil, errors.Join(errOperatorStoreDown, err)
		}
		allRows, loadErr := loadPatchPolicyRows(ctx, conn, "", nil)
		closeErr = conn.Close()
		if loadErr != nil {
			if closeErr != nil {
				return nil, errors.Join(loadErr, closeErr)
			}
			return nil, loadErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		indexRows = allRows
	}
	pendingIndex, err := s.patchPolicyPendingInventoryIndex(ctx, profile, indexRows, nil)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, row := range rows {
		if !patchPolicyVisible(row, allowedSiteIDs) {
			continue
		}
		payload := patchPolicyPayload(row)
		pendingCounts := pendingIndex.BreakdownByPolicyID[nullInt(row.ID)]
		counts, targetSites := patchPolicyCoverageListSummary(ctx, s, profile, row)
		payload["target_count"] = counts["eligible"]
		payload["raw_target_count"] = counts["raw"]
		payload["ignored_role_count"] = counts["ignored_role"]
		payload["role_match_label"] = patchPolicyRoleMatchLabel(counts)
		payload["pending_update_count"] = patchPolicyPendingTotal(pendingCounts)
		payload["pending_update_breakdown"] = patchPolicyPendingBreakdownPayload(pendingCounts)
		payload["pending_update_device_count"] = pendingIndex.DeviceCountByPolicyID[nullInt(row.ID)]
		payload["target_sites"] = targetSites
		payload["target_site_ids"] = patchPolicyTargetSiteIDs(targetSites)
		payload["target_site_names"] = patchPolicyTargetSiteNames(targetSites)
		out = append(out, payload)
	}
	return out, nil
}

func (s *postgresOperatorStore) patchPolicyMetadata(ctx context.Context, profile operatorProfile) (map[string]any, error) {
	sites, err := s.listSites(ctx, profile)
	if err != nil {
		return nil, err
	}
	filters, err := s.listDeviceFilters(ctx, profile, false, nil)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"sites":        sites,
		"filters":      filters,
		"policy_types": []string{patchPolicyTypeGlobal, patchPolicyTypeSite, patchPolicyTypeDeviceFilter},
		"role_scopes":  []string{patchPolicyRoleServer, patchPolicyRoleWorkstation},
		"rule_types":   []string{patchPolicyRuleApprove, patchPolicyRuleBlock},
		"match_types":  []string{"severity", "classification", "category", "kb", "update_id", "patch_key"},
		"exclusions":   []string{patchPolicyExclusionUnmanaged, patchPolicyExclusionFrozen, patchPolicyExclusionOverride},
		"defaults": map[string]any{
			"approval_mode":           "conservative_msp",
			"deferral_days":           14,
			"managed_update_mode":     true,
			"install_schedule_type":   "weekly",
			"reboot_after_install":    false,
			"reboot_schedule_enabled": false,
		},
	}, nil
}

func (s *postgresOperatorStore) getPatchPolicy(ctx context.Context, profile operatorProfile, policyID int64) (map[string]any, bool, error) {
	if err := s.ensurePatchPolicySchema(ctx); err != nil {
		return nil, false, err
	}
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, false, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, false, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	rows, err := loadPatchPolicyRows(ctx, conn, "", &policyID)
	if err != nil {
		return nil, false, err
	}
	if len(rows) == 0 || !patchPolicyVisible(rows[0], allowedSiteIDs) {
		return nil, false, nil
	}
	return patchPolicyPayload(rows[0]), true, nil
}

func (s *postgresOperatorStore) savePatchPolicy(ctx context.Context, profile operatorProfile, policyID *int64, body map[string]any) (map[string]any, int, error) {
	if !strings.EqualFold(strings.TrimSpace(profile.Role), "admin") {
		return map[string]any{"error": "forbidden"}, http.StatusForbidden, errors.New("admin required")
	}
	if err := s.ensurePatchPolicySchema(ctx); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	now := time.Now().Unix()
	existing := patchPolicyRow{}
	if policyID != nil && *policyID > 0 {
		rows, err := loadPatchPolicyRows(ctx, conn, "", policyID)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		if len(rows) == 0 {
			return map[string]any{"error": "not_found"}, http.StatusNotFound, errors.New("not found")
		}
		existing = rows[0]
	}
	values, errText := normalizePatchPolicySaveBody(body, existing, now, profile.Username)
	if errText != "" {
		return map[string]any{"error": "invalid_policy", "message": errText}, http.StatusBadRequest, errors.New(errText)
	}
	if !policyIDValid(policyID) && values.PolicyType == patchPolicyTypeGlobal {
		return map[string]any{"error": "global_policy_locked", "message": "Global Patch Policies are seeded by Borealis and cannot be created manually."}, http.StatusConflict, errors.New("global policy locked")
	}
	if policyIDValid(policyID) && normalizePatchPolicyType(existing.PolicyType.String) == patchPolicyTypeGlobal && values.RoleScope != normalizePatchPolicyRoleScope(existing.RoleScope.String) {
		return map[string]any{"error": "global_role_locked", "message": "Global Patch Policy role cannot be changed."}, http.StatusConflict, errors.New("global role locked")
	}
	conflicts, err := patchPolicySaveConflicts(ctx, conn, nullablePolicyID(policyID), values)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if len(conflicts) > 0 {
		return map[string]any{"error": "policy_conflict", "conflicts": conflicts}, http.StatusConflict, errors.New("policy conflict")
	}
	overrideWarnings, err := patchPolicyParentOverrideWarnings(ctx, conn, values.PolicyType, values.RoleScope, values.Rules)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	confirmOverride := boolFromAny(firstPresentAny(body["confirm_parent_overrides"], body["confirm_parent_override"]))
	if len(overrideWarnings) > 0 && !confirmOverride {
		return map[string]any{"error": "parent_block_override_confirmation_required", "warnings": overrideWarnings}, http.StatusConflict, errors.New("parent override confirmation required")
	}
	if confirmOverride {
		values.Rules = patchPolicyMarkOverrideRules(values.Rules, overrideWarnings)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	defer rollbackQuietly(tx)
	var id int64
	if policyID != nil && *policyID > 0 {
		id = *policyID
		_, err = tx.ExecContext(ctx, `
			UPDATE engine.patch_policies
			   SET name=$1, description=$2, enabled=$3, role_scope=$4, approval_mode=$5,
			       deferral_days=$6, managed_update_mode=$7, install_schedule_type=$8,
			       install_start_ts=$9, reboot_after_install=$10, reboot_schedule_enabled=$11,
			       reboot_schedule_type=$12, reboot_start_ts=$13, force_reboot_logged_in=$14,
			       updated_by=$15, updated_at=$16
			 WHERE id=$17
		`, values.Name, values.Description, boolIntArg(values.Enabled), values.RoleScope, values.ApprovalMode,
			values.DeferralDays, boolIntArg(values.ManagedUpdateMode), values.InstallScheduleType,
			nullableInt64Arg(values.InstallStartTS), boolIntArg(values.RebootAfterInstall), boolIntArg(values.RebootScheduleEnabled),
			values.RebootScheduleType, nullableInt64Arg(values.RebootStartTS), boolIntArg(values.ForceRebootLoggedIn),
			profile.Username, now, id)
	} else {
		err = tx.QueryRowContext(ctx, `
			INSERT INTO engine.patch_policies(
				name, description, policy_type, enabled, locked, role_scope, approval_mode,
				deferral_days, managed_update_mode, install_schedule_type, install_start_ts,
				reboot_after_install, reboot_schedule_enabled, reboot_schedule_type, reboot_start_ts,
				force_reboot_logged_in, created_by, updated_by, created_at, updated_at
			) VALUES ($1,$2,$3,$4,0,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$16,$17,$17)
			RETURNING id
		`, values.Name, values.Description, values.PolicyType, boolIntArg(values.Enabled), values.RoleScope, values.ApprovalMode,
			values.DeferralDays, boolIntArg(values.ManagedUpdateMode), values.InstallScheduleType, nullableInt64Arg(values.InstallStartTS),
			boolIntArg(values.RebootAfterInstall), boolIntArg(values.RebootScheduleEnabled), values.RebootScheduleType,
			nullableInt64Arg(values.RebootStartTS), boolIntArg(values.ForceRebootLoggedIn), profile.Username, now).Scan(&id)
	}
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	for _, table := range []string{"engine.patch_policy_sites", "engine.patch_policy_targets", "engine.patch_policy_exclusions", "engine.patch_policy_rules"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE policy_id=$1", id); err != nil {
			return nil, http.StatusInternalServerError, err
		}
	}
	for _, siteID := range values.SiteIDs {
		if _, err := tx.ExecContext(ctx, "INSERT INTO engine.patch_policy_sites(policy_id, site_id, created_at) VALUES ($1,$2,$3)", id, siteID, now); err != nil {
			return nil, http.StatusInternalServerError, err
		}
	}
	for _, target := range values.Targets {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO engine.patch_policy_targets(policy_id, target_type, device_guid, hostname, filter_id, target_json, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
		`, id, target.TargetType, nullEmpty(target.DeviceGUID), nullEmpty(target.Hostname), nullablePositiveInt64Arg(target.FilterID), nullEmpty(target.TargetJSON), now); err != nil {
			return nil, http.StatusInternalServerError, err
		}
	}
	for _, exclusion := range values.Exclusions {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO engine.patch_policy_exclusions(policy_id, exclusion_type, target_type, device_guid, hostname, site_id, filter_id, reason, created_by, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		`, id, exclusion.ExclusionType, exclusion.TargetType, nullEmpty(exclusion.DeviceGUID), nullEmpty(exclusion.Hostname), nullablePositiveInt64Arg(exclusion.SiteID), nullablePositiveInt64Arg(exclusion.FilterID), nullEmpty(exclusion.Reason), profile.Username, now); err != nil {
			return nil, http.StatusInternalServerError, err
		}
	}
	for _, rule := range values.Rules {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO engine.patch_policy_rules(policy_id, rule_type, match_type, match_value, override_parent_block, notes, created_by, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		`, id, rule.RuleType, rule.MatchType, rule.MatchValue, boolIntArg(rule.OverrideParentBlock), nullEmpty(rule.Notes), profile.Username, now); err != nil {
			return nil, http.StatusInternalServerError, err
		}
	}
	auditAction := "created"
	if policyIDValid(policyID) {
		auditAction = "updated"
	}
	auditDetail := map[string]any{"confirmed_parent_overrides": confirmOverride, "warnings": overrideWarnings}
	if err := insertPatchPolicyAuditTx(ctx, tx, id, auditAction, profile.Username, auditDetail, now); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if err := tx.Commit(); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	rows, err := loadPatchPolicyRows(ctx, conn, "", &id)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if len(rows) == 0 {
		return map[string]any{"error": "not_found"}, http.StatusNotFound, errors.New("not found")
	}
	return map[string]any{"policy": patchPolicyPayload(rows[0]), "warnings": overrideWarnings}, http.StatusOK, nil
}

func (s *postgresOperatorStore) deletePatchPolicy(ctx context.Context, profile operatorProfile, policyID int64) (map[string]any, int, error) {
	if !strings.EqualFold(strings.TrimSpace(profile.Role), "admin") {
		return map[string]any{"error": "forbidden"}, http.StatusForbidden, errors.New("admin required")
	}
	if err := s.ensurePatchPolicySchema(ctx); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	rows, err := loadPatchPolicyRows(ctx, conn, "", &policyID)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if len(rows) == 0 {
		return map[string]any{"error": "not_found"}, http.StatusNotFound, errors.New("not found")
	}
	if boolInt64(rows[0].Locked) || normalizePatchPolicyType(rows[0].PolicyType.String) == patchPolicyTypeGlobal {
		return map[string]any{"error": "locked_policy", "message": "Global Patch Policy cannot be deleted."}, http.StatusConflict, errors.New("locked policy")
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	defer rollbackQuietly(tx)
	if _, err := tx.ExecContext(ctx, "DELETE FROM engine.patch_policies WHERE id=$1", policyID); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if err := insertPatchPolicyAuditTx(ctx, tx, policyID, "deleted", profile.Username, map[string]any{"name": nullString(rows[0].Name)}, time.Now().Unix()); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if err := tx.Commit(); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return map[string]any{"status": "deleted", "id": policyID}, http.StatusOK, nil
}

func (s *postgresOperatorStore) previewPatchPolicy(ctx context.Context, profile operatorProfile, policyID *int64, body map[string]any) (map[string]any, int, error) {
	if err := s.ensurePatchPolicySchema(ctx); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	var row patchPolicyRow
	if len(body) == 0 && policyIDValid(policyID) {
		rows, err := loadPatchPolicyRows(ctx, conn, "", policyID)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		if len(rows) == 0 {
			return map[string]any{"error": "not_found"}, http.StatusNotFound, errors.New("not found")
		}
		row = rows[0]
	} else {
		values, errText := normalizePatchPolicySaveBody(body, patchPolicyRow{}, time.Now().Unix(), profile.Username)
		if errText != "" {
			return map[string]any{"error": "invalid_policy", "message": errText}, http.StatusBadRequest, errors.New(errText)
		}
		row = values.toRow()
	}
	resolution, err := s.resolvePatchPolicyDeviceResolution(ctx, profile, row, true)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	devices := resolution.Eligible
	conflicts, err := patchPolicySaveConflicts(ctx, conn, nullablePolicyID(policyID), patchPolicySaveValuesFromRow(row))
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	warnings, err := patchPolicyParentOverrideWarnings(ctx, conn, normalizePatchPolicyType(row.PolicyType.String), normalizePatchPolicyRoleScope(row.RoleScope.String), row.Rules)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	dynamicConflicts, err := s.patchPolicyDynamicConflicts(ctx, profile, nullablePolicyID(policyID), row)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	targetRows, err := s.patchPolicyPreviewTargetRows(ctx, profile, row)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	exclusionRows, err := s.patchPolicyPreviewExclusionRows(ctx, profile, row)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return map[string]any{
		"policy":             patchPolicyPayload(row),
		"target_count":       len(devices),
		"raw_target_count":   len(resolution.Raw),
		"ignored_role_count": len(resolution.Raw) - len(resolution.Eligible),
		"role_match_label":   patchPolicyRoleMatchLabel(map[string]int{"eligible": len(resolution.Eligible), "raw": len(resolution.Raw), "ignored_role": len(resolution.Raw) - len(resolution.Eligible)}),
		"targets":            patchPolicyDevicePayloads(devices),
		"target_rows":        targetRows,
		"exclusion_rows":     exclusionRows,
		"conflicts":          conflicts,
		"dynamic_conflicts":  dynamicConflicts,
		"warnings":           warnings,
	}, http.StatusOK, nil
}

func (s *postgresOperatorStore) effectivePatchPolicy(ctx context.Context, profile operatorProfile, hostname string) (map[string]any, int, error) {
	hostname = cleanText(hostname)
	if hostname == "" {
		return map[string]any{"error": "hostname_required"}, http.StatusBadRequest, errors.New("hostname required")
	}
	if err := s.ensurePatchPolicySchema(ctx); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	rows, err := loadPatchPolicyRows(ctx, conn, "", nil)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	rowByID := map[int64]patchPolicyRow{}
	for _, row := range rows {
		if id := nullInt(row.ID); id > 0 {
			rowByID[id] = row
		}
	}
	scopes, err := s.patchPolicyEffectiveScopes(ctx, profile, rows)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	for policyID, scope := range scopes {
		for _, device := range scope.Devices {
			if strings.EqualFold(device.Hostname, hostname) {
				hierarchy := []patchPolicyRow{}
				for _, id := range device.HierarchyPolicyIDs {
					if row, ok := rowByID[id]; ok {
						hierarchy = append(hierarchy, row)
					}
				}
				policy := rowByID[policyID]
				return map[string]any{
					"hostname":                     hostname,
					"policy":                       patchPolicyPayload(policy),
					"hierarchy":                    patchPolicyPayloads(hierarchy),
					"conflicted":                   device.Conflict,
					"exclusion_mode":               device.ExclusionMode,
					"exclusion_policy_id":          nullablePositiveInt64Any(device.ExclusionPolicyID),
					"exclusion_override_policy_id": nullablePositiveInt64Any(device.ExclusionOverridePolicyID),
				}, http.StatusOK, nil
			}
		}
	}
	return map[string]any{"hostname": hostname, "policy": nil, "status": "uncovered"}, http.StatusOK, nil
}

func (s *postgresOperatorStore) patchPolicyPreviewTargetRows(ctx context.Context, profile operatorProfile, row patchPolicyRow) ([]map[string]any, error) {
	roleScope := normalizePatchPolicyRoleScope(row.RoleScope.String)
	switch normalizePatchPolicyType(row.PolicyType.String) {
	case patchPolicyTypeGlobal:
		return nil, nil
	case patchPolicyTypeSite:
		rows := []map[string]any{}
		for idx, site := range row.Sites {
			siteID := coerceInt64(firstPresentAny(site["site_id"], site["id"]))
			if siteID <= 0 {
				continue
			}
			counts, err := s.patchPolicySiteTargetCounts(ctx, profile, roleScope, siteID)
			if err != nil {
				return nil, err
			}
			next := copyMap(site)
			next["row_index"] = idx
			next["target_type"] = "site"
			next["site_id"] = siteID
			patchPolicyAttachCountPayload(next, counts)
			rows = append(rows, next)
		}
		return rows, nil
	case patchPolicyTypeDeviceFilter:
		rows := []map[string]any{}
		for idx, target := range row.Targets {
			counts, err := s.patchPolicyScheduledTargetCounts(ctx, profile, roleScope, patchPolicyScheduledTarget(target))
			if err != nil {
				return nil, err
			}
			next := copyMap(target)
			next["row_index"] = idx
			patchPolicyAttachCountPayload(next, counts)
			rows = append(rows, next)
		}
		return rows, nil
	default:
		return nil, nil
	}
}

func (s *postgresOperatorStore) patchPolicyPreviewExclusionRows(ctx context.Context, profile operatorProfile, row patchPolicyRow) ([]map[string]any, error) {
	roleScope := normalizePatchPolicyRoleScope(row.RoleScope.String)
	rows := []map[string]any{}
	for idx, exclusion := range row.Exclusions {
		counts, err := s.patchPolicyScheduledTargetCounts(ctx, profile, roleScope, patchPolicyScheduledTarget(exclusion))
		if err != nil {
			return nil, err
		}
		next := copyMap(exclusion)
		next["row_index"] = idx
		patchPolicyAttachCountPayload(next, counts)
		rows = append(rows, next)
	}
	return rows, nil
}

func (s *postgresOperatorStore) patchPolicySiteTargetCounts(ctx context.Context, profile operatorProfile, roleScope string, siteID int64) (map[string]int, error) {
	devices, err := s.fetchFilterDevices(ctx, profile)
	if err != nil {
		return nil, err
	}
	raw := 0
	eligible := 0
	for _, device := range devices {
		if coerceInt64(device["site_id"]) != siteID {
			continue
		}
		osText := cleanText(firstPresentAny(device["operating_system"], nestedAny(device, "summary", "operating_system")))
		if !patchPolicyWindowsOS(osText) {
			continue
		}
		raw++
		deviceType := cleanText(firstPresentAny(device["device_type"], nestedAny(device, "summary", "device_type")))
		if patchPolicyRoleMatches(roleScope, deviceType) {
			eligible++
		}
	}
	return patchPolicyCountMap(raw, eligible), nil
}

func (s *postgresOperatorStore) patchPolicyScheduledTargetCounts(ctx context.Context, profile operatorProfile, roleScope string, target map[string]any) (map[string]int, error) {
	resolution, err := s.resolveScheduledRerunTargets(ctx, profile, []any{target})
	if err != nil {
		return nil, err
	}
	raw := 0
	eligible := 0
	for _, target := range resolution.Targets {
		if target == nil || !patchPolicyWindowsOS(target.OperatingSystem) {
			continue
		}
		raw++
		if patchPolicyRoleMatches(roleScope, target.DeviceType) {
			eligible++
		}
	}
	return patchPolicyCountMap(raw, eligible), nil
}

func patchPolicyCountMap(raw int, eligible int) map[string]int {
	ignored := raw - eligible
	if ignored < 0 {
		ignored = 0
	}
	return map[string]int{"raw": raw, "eligible": eligible, "ignored_role": ignored}
}

func patchPolicyAttachCountPayload(payload map[string]any, counts map[string]int) {
	if payload == nil {
		return
	}
	payload["raw_target_count"] = counts["raw"]
	payload["eligible_target_count"] = counts["eligible"]
	payload["ignored_role_count"] = counts["ignored_role"]
	payload["role_match_label"] = patchPolicyRoleMatchLabel(counts)
}

func (s *postgresOperatorStore) evaluatePatchPolicies(ctx context.Context, profile operatorProfile, body map[string]any) (map[string]any, int, error) {
	policyID := coerceInt64(firstPresentAny(body["policy_id"], body["id"]))
	now := time.Now().Unix()
	result, err := s.evaluatePatchPoliciesAt(ctx, profile, policyID, schedulerFloorMinute(now), now, true)
	if err != nil {
		return map[string]any{"error": "policy_evaluate_failed", "message": err.Error()}, http.StatusInternalServerError, err
	}
	return result, http.StatusOK, nil
}

func loadPatchPolicyRows(ctx context.Context, conn *sql.Conn, policyType string, policyID *int64) ([]patchPolicyRow, error) {
	query := `
		SELECT id, name, description, policy_type, enabled, locked, role_scope, approval_mode,
		       deferral_days, managed_update_mode, install_schedule_type, install_start_ts,
		       reboot_after_install, reboot_schedule_enabled, reboot_schedule_type, reboot_start_ts,
		       force_reboot_logged_in, created_by, updated_by, created_at, updated_at
		  FROM engine.patch_policies
	`
	args := []any{}
	where := []string{}
	if policyType != "" {
		args = append(args, normalizePatchPolicyType(policyType))
		where = append(where, "policy_type=$"+strconv.Itoa(len(args)))
	}
	if policyID != nil && *policyID > 0 {
		args = append(args, *policyID)
		where = append(where, "id=$"+strconv.Itoa(len(args)))
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY CASE policy_type WHEN 'global' THEN 0 WHEN 'site' THEN 1 ELSE 2 END, LOWER(name), id"
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []patchPolicyRow{}
	ids := []int64{}
	for rows.Next() {
		var row patchPolicyRow
		err := rows.Scan(
			&row.ID, &row.Name, &row.Description, &row.PolicyType, &row.Enabled, &row.Locked, &row.RoleScope, &row.ApprovalMode,
			&row.DeferralDays, &row.ManagedUpdateMode, &row.InstallScheduleType, &row.InstallStartTS,
			&row.RebootAfterInstall, &row.RebootScheduleEnabled, &row.RebootScheduleType, &row.RebootStartTS,
			&row.ForceRebootLoggedIn, &row.CreatedBy, &row.UpdatedBy, &row.CreatedAt, &row.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
		if row.ID.Valid {
			ids = append(ids, row.ID.Int64)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return out, nil
	}
	sites, targets, exclusions, rules, err := loadPatchPolicyAssociations(ctx, conn, ids)
	if err != nil {
		return nil, err
	}
	for idx := range out {
		id := out[idx].ID.Int64
		out[idx].Sites = sites[id]
		out[idx].Targets = targets[id]
		out[idx].Exclusions = exclusions[id]
		out[idx].Rules = rules[id]
	}
	return out, nil
}

func loadPatchPolicyAssociations(ctx context.Context, conn *sql.Conn, ids []int64) (map[int64][]map[string]any, map[int64][]map[string]any, map[int64][]map[string]any, map[int64][]patchPolicyRule, error) {
	policyIDs := uniquePositiveInt64s(ids)
	placeholders := make([]string, 0, len(policyIDs))
	args := make([]any, 0, len(policyIDs))
	for _, id := range policyIDs {
		args = append(args, id)
		placeholders = append(placeholders, "$"+strconv.Itoa(len(args)))
	}
	inSQL := strings.Join(placeholders, ",")
	sites := map[int64][]map[string]any{}
	targets := map[int64][]map[string]any{}
	exclusions := map[int64][]map[string]any{}
	rules := map[int64][]patchPolicyRule{}
	siteRows, err := conn.QueryContext(ctx, `
		SELECT ps.policy_id, s.id, s.name
		  FROM engine.patch_policy_sites AS ps
		  JOIN engine.sites AS s ON s.id=ps.site_id
		 WHERE ps.policy_id IN (`+inSQL+`)
	  ORDER BY LOWER(s.name), s.id
	`, args...)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	for siteRows.Next() {
		var policyID, siteID sql.NullInt64
		var name sql.NullString
		if err := siteRows.Scan(&policyID, &siteID, &name); err != nil {
			_ = siteRows.Close()
			return nil, nil, nil, nil, err
		}
		if policyID.Valid && siteID.Valid {
			sites[policyID.Int64] = append(sites[policyID.Int64], map[string]any{"id": siteID.Int64, "site_id": siteID.Int64, "name": nullString(name)})
		}
	}
	if err := siteRows.Close(); err != nil {
		return nil, nil, nil, nil, err
	}
	targetRows, err := conn.QueryContext(ctx, `
		SELECT policy_id, target_type, device_guid, hostname, filter_id, target_json
		  FROM engine.patch_policy_targets
		 WHERE policy_id IN (`+inSQL+`)
	  ORDER BY id
	`, args...)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	for targetRows.Next() {
		var policyID, filterID sql.NullInt64
		var targetType, guid, hostname, targetJSON sql.NullString
		if err := targetRows.Scan(&policyID, &targetType, &guid, &hostname, &filterID, &targetJSON); err != nil {
			_ = targetRows.Close()
			return nil, nil, nil, nil, err
		}
		if !policyID.Valid {
			continue
		}
		targets[policyID.Int64] = append(targets[policyID.Int64], map[string]any{
			"target_type": cleanText(targetType.String),
			"device_guid": cleanText(guid.String),
			"hostname":    cleanText(hostname.String),
			"filter_id":   nullableInt(filterID),
			"target":      parseJSON(targetJSON),
		})
	}
	if err := targetRows.Close(); err != nil {
		return nil, nil, nil, nil, err
	}
	exclusionRows, err := conn.QueryContext(ctx, `
		SELECT e.policy_id, e.exclusion_type, e.target_type, e.device_guid, e.hostname, e.site_id, s.name, e.filter_id, e.reason, e.created_by, e.created_at
		  FROM engine.patch_policy_exclusions AS e
		  LEFT JOIN engine.sites AS s ON s.id=e.site_id
		 WHERE e.policy_id IN (`+inSQL+`)
	  ORDER BY e.id
	`, args...)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	for exclusionRows.Next() {
		var policyID, siteID, filterID, createdAt sql.NullInt64
		var exclusionType, targetType, guid, hostname, siteName, reason, createdBy sql.NullString
		if err := exclusionRows.Scan(&policyID, &exclusionType, &targetType, &guid, &hostname, &siteID, &siteName, &filterID, &reason, &createdBy, &createdAt); err != nil {
			_ = exclusionRows.Close()
			return nil, nil, nil, nil, err
		}
		if !policyID.Valid {
			continue
		}
		exclusions[policyID.Int64] = append(exclusions[policyID.Int64], map[string]any{
			"exclusion_type": cleanText(exclusionType.String),
			"target_type":    cleanText(targetType.String),
			"device_guid":    cleanText(guid.String),
			"hostname":       cleanText(hostname.String),
			"site_id":        nullableInt(siteID),
			"site_name":      cleanText(siteName.String),
			"filter_id":      nullableInt(filterID),
			"reason":         cleanText(reason.String),
			"created_by":     cleanText(createdBy.String),
			"created_at":     nullableInt(createdAt),
		})
	}
	if err := exclusionRows.Close(); err != nil {
		return nil, nil, nil, nil, err
	}
	ruleRows, err := conn.QueryContext(ctx, `
		SELECT policy_id, id, rule_type, match_type, match_value, override_parent_block, notes, created_by, created_at
		  FROM engine.patch_policy_rules
		 WHERE policy_id IN (`+inSQL+`)
	  ORDER BY CASE rule_type WHEN 'block' THEN 0 ELSE 1 END, match_type, LOWER(match_value), id
	`, args...)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	defer ruleRows.Close()
	for ruleRows.Next() {
		var policyID, id, override, createdAt sql.NullInt64
		var ruleType, matchType, matchValue, notes, createdBy sql.NullString
		if err := ruleRows.Scan(&policyID, &id, &ruleType, &matchType, &matchValue, &override, &notes, &createdBy, &createdAt); err != nil {
			return nil, nil, nil, nil, err
		}
		if !policyID.Valid {
			continue
		}
		rules[policyID.Int64] = append(rules[policyID.Int64], patchPolicyRule{
			ID:                  nullInt(id),
			RuleType:            normalizePatchPolicyRuleType(ruleType.String),
			MatchType:           normalizePatchPolicyMatchType(matchType.String),
			MatchValue:          cleanText(matchValue.String),
			OverrideParentBlock: boolInt64(override),
			Notes:               cleanText(notes.String),
			CreatedBy:           cleanText(createdBy.String),
			CreatedAt:           nullInt(createdAt),
		})
	}
	return sites, targets, exclusions, rules, ruleRows.Err()
}

func patchPolicyPayload(row patchPolicyRow) map[string]any {
	id := nullInt(row.ID)
	return map[string]any{
		"id":                      id,
		"name":                    nullString(row.Name),
		"description":             nullString(row.Description),
		"policy_type":             normalizePatchPolicyType(row.PolicyType.String),
		"enabled":                 boolInt64(row.Enabled),
		"locked":                  boolInt64(row.Locked),
		"role_scope":              normalizePatchPolicyRoleScope(row.RoleScope.String),
		"approval_mode":           firstText(cleanText(row.ApprovalMode.String), "conservative_msp"),
		"deferral_days":           firstPositiveInt64(nullInt(row.DeferralDays), 14),
		"managed_update_mode":     boolInt64(row.ManagedUpdateMode),
		"install_schedule_type":   normalizePatchPolicyScheduleType(row.InstallScheduleType.String),
		"install_start_ts":        nullableInt(row.InstallStartTS),
		"reboot_after_install":    boolInt64(row.RebootAfterInstall),
		"reboot_schedule_enabled": boolInt64(row.RebootScheduleEnabled),
		"reboot_schedule_type":    normalizePatchPolicyScheduleType(row.RebootScheduleType.String),
		"reboot_start_ts":         nullableInt(row.RebootStartTS),
		"force_reboot_logged_in":  boolInt64(row.ForceRebootLoggedIn),
		"sites":                   row.Sites,
		"site_ids":                patchPolicySiteIDs(row.Sites),
		"targets":                 row.Targets,
		"exclusions":              row.Exclusions,
		"rules":                   patchPolicyRulePayloads(row.Rules),
		"created_by":              nullString(row.CreatedBy),
		"updated_by":              nullString(row.UpdatedBy),
		"created_at":              nullableInt(row.CreatedAt),
		"updated_at":              nullableInt(row.UpdatedAt),
	}
}

func patchPolicyPayloads(rows []patchPolicyRow) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, patchPolicyPayload(row))
	}
	return out
}

func patchPolicyRulePayloads(rules []patchPolicyRule) []map[string]any {
	out := make([]map[string]any, 0, len(rules))
	for _, rule := range rules {
		out = append(out, map[string]any{
			"id":                    rule.ID,
			"rule_type":             rule.RuleType,
			"match_type":            rule.MatchType,
			"match_value":           rule.MatchValue,
			"override_parent_block": rule.OverrideParentBlock,
			"notes":                 rule.Notes,
			"created_by":            rule.CreatedBy,
			"created_at":            rule.CreatedAt,
		})
	}
	return out
}

type patchPolicySaveValues struct {
	Name                  string
	Description           string
	PolicyType            string
	Enabled               bool
	RoleScope             string
	ApprovalMode          string
	DeferralDays          int64
	ManagedUpdateMode     bool
	InstallScheduleType   string
	InstallStartTS        *int64
	RebootAfterInstall    bool
	RebootScheduleEnabled bool
	RebootScheduleType    string
	RebootStartTS         *int64
	ForceRebootLoggedIn   bool
	SiteIDs               []int64
	Targets               []patchPolicyTargetRef
	Exclusions            []patchPolicyExclusionRef
	Rules                 []patchPolicyRule
}

func normalizePatchPolicySaveBody(body map[string]any, existing patchPolicyRow, now int64, actor string) (patchPolicySaveValues, string) {
	policyType := normalizePatchPolicyType(firstPresentAny(body["policy_type"], body["type"]))
	if policyType == "" {
		policyType = normalizePatchPolicyType(existing.PolicyType.String)
	}
	if policyType == "" {
		policyType = patchPolicyTypeSite
	}
	name := cleanText(firstPresentAny(body["name"], existing.Name.String))
	if name == "" {
		return patchPolicySaveValues{}, "Policy name is required."
	}
	roleScopeRaw := firstPresentAny(body["role_scope"], existing.RoleScope.String)
	values := patchPolicySaveValues{
		Name:                  name,
		Description:           cleanText(firstPresentAny(body["description"], existing.Description.String)),
		PolicyType:            policyType,
		Enabled:               boolFromAny(firstPresentAny(body["enabled"], boolInt64(existing.Enabled), true)),
		RoleScope:             normalizePatchPolicyRoleScope(roleScopeRaw),
		ApprovalMode:          firstText(cleanText(firstPresentAny(body["approval_mode"], existing.ApprovalMode.String)), "conservative_msp"),
		DeferralDays:          firstPositiveInt64(coerceInt64(firstPresentAny(body["deferral_days"], existing.DeferralDays.Int64)), 14),
		ManagedUpdateMode:     boolFromAny(firstPresentAny(body["managed_update_mode"], boolInt64(existing.ManagedUpdateMode), true)),
		InstallScheduleType:   normalizePatchPolicyScheduleType(firstPresentAny(body["install_schedule_type"], existing.InstallScheduleType.String, "weekly")),
		InstallStartTS:        optionalPositiveInt64Ptr(firstPresentAny(body["install_start_ts"], nullableInt(existing.InstallStartTS))),
		RebootAfterInstall:    boolFromAny(firstPresentAny(body["reboot_after_install"], boolInt64(existing.RebootAfterInstall))),
		RebootScheduleEnabled: boolFromAny(firstPresentAny(body["reboot_schedule_enabled"], boolInt64(existing.RebootScheduleEnabled))),
		RebootScheduleType:    normalizePatchPolicyScheduleType(firstPresentAny(body["reboot_schedule_type"], existing.RebootScheduleType.String, "weekly")),
		RebootStartTS:         optionalPositiveInt64Ptr(firstPresentAny(body["reboot_start_ts"], nullableInt(existing.RebootStartTS))),
		ForceRebootLoggedIn:   boolFromAny(firstPresentAny(body["force_reboot_logged_in"], boolInt64(existing.ForceRebootLoggedIn))),
	}
	if roleScopeRaw == nil {
		return patchPolicySaveValues{}, "Patch policies require Server or Workstation role scope."
	}
	if !patchPolicyValidRoleDomain(values.RoleScope) {
		return patchPolicySaveValues{}, "Patch policies require Server or Workstation role scope."
	}
	if values.InstallScheduleType == "" {
		values.InstallScheduleType = "weekly"
	}
	if values.RebootScheduleType == "" {
		values.RebootScheduleType = "weekly"
	}
	values.SiteIDs = patchPolicySiteIDsFromAny(firstPresentAny(body["site_ids"], body["sites"], existing.Sites))
	values.Targets = patchPolicyTargetsFromAny(firstPresentAny(body["targets"], existing.Targets))
	values.Exclusions = patchPolicyExclusionsFromAny(firstPresentAny(body["exclusions"], existing.Exclusions), actor)
	values.Rules = patchPolicyRulesFromAny(firstPresentAny(body["rules"], patchPolicyRulePayloads(existing.Rules)), actor, now)
	if errText := patchPolicyExclusionValidationError(values.Exclusions); errText != "" {
		return patchPolicySaveValues{}, errText
	}
	if values.PolicyType == patchPolicyTypeSite && len(values.SiteIDs) == 0 {
		return patchPolicySaveValues{}, "Site policies require at least one site."
	}
	if values.PolicyType == patchPolicyTypeDeviceFilter && len(values.Targets) == 0 {
		return patchPolicySaveValues{}, "Device Filter policies require at least one target."
	}
	if values.PolicyType == patchPolicyTypeGlobal {
		values.SiteIDs = nil
		values.Targets = nil
	}
	return values, ""
}

func (v patchPolicySaveValues) toRow() patchPolicyRow {
	row := patchPolicyRow{
		Name:                  sql.NullString{String: v.Name, Valid: v.Name != ""},
		Description:           sql.NullString{String: v.Description, Valid: v.Description != ""},
		PolicyType:            sql.NullString{String: v.PolicyType, Valid: v.PolicyType != ""},
		Enabled:               sql.NullInt64{Int64: int64(boolIntArg(v.Enabled)), Valid: true},
		RoleScope:             sql.NullString{String: v.RoleScope, Valid: v.RoleScope != ""},
		ApprovalMode:          sql.NullString{String: v.ApprovalMode, Valid: v.ApprovalMode != ""},
		DeferralDays:          sql.NullInt64{Int64: v.DeferralDays, Valid: true},
		ManagedUpdateMode:     sql.NullInt64{Int64: int64(boolIntArg(v.ManagedUpdateMode)), Valid: true},
		InstallScheduleType:   sql.NullString{String: v.InstallScheduleType, Valid: v.InstallScheduleType != ""},
		RebootAfterInstall:    sql.NullInt64{Int64: int64(boolIntArg(v.RebootAfterInstall)), Valid: true},
		RebootScheduleEnabled: sql.NullInt64{Int64: int64(boolIntArg(v.RebootScheduleEnabled)), Valid: true},
		RebootScheduleType:    sql.NullString{String: v.RebootScheduleType, Valid: v.RebootScheduleType != ""},
		ForceRebootLoggedIn:   sql.NullInt64{Int64: int64(boolIntArg(v.ForceRebootLoggedIn)), Valid: true},
		Rules:                 v.Rules,
	}
	if v.InstallStartTS != nil {
		row.InstallStartTS = sql.NullInt64{Int64: *v.InstallStartTS, Valid: true}
	}
	if v.RebootStartTS != nil {
		row.RebootStartTS = sql.NullInt64{Int64: *v.RebootStartTS, Valid: true}
	}
	for _, siteID := range v.SiteIDs {
		row.Sites = append(row.Sites, map[string]any{"id": siteID, "site_id": siteID})
	}
	for _, target := range v.Targets {
		row.Targets = append(row.Targets, patchPolicyTargetPayload(target))
	}
	for _, exclusion := range v.Exclusions {
		row.Exclusions = append(row.Exclusions, patchPolicyExclusionPayload(exclusion))
	}
	return row
}

func patchPolicySaveValuesFromRow(row patchPolicyRow) patchPolicySaveValues {
	return patchPolicySaveValues{
		Name:                  nullString(row.Name),
		Description:           nullString(row.Description),
		PolicyType:            normalizePatchPolicyType(row.PolicyType.String),
		Enabled:               boolInt64(row.Enabled),
		RoleScope:             normalizePatchPolicyRoleScope(row.RoleScope.String),
		ApprovalMode:          firstText(cleanText(row.ApprovalMode.String), "conservative_msp"),
		DeferralDays:          firstPositiveInt64(nullInt(row.DeferralDays), 14),
		ManagedUpdateMode:     boolInt64(row.ManagedUpdateMode),
		InstallScheduleType:   normalizePatchPolicyScheduleType(row.InstallScheduleType.String),
		InstallStartTS:        optionalPositiveInt64Ptr(nullableInt(row.InstallStartTS)),
		RebootAfterInstall:    boolInt64(row.RebootAfterInstall),
		RebootScheduleEnabled: boolInt64(row.RebootScheduleEnabled),
		RebootScheduleType:    normalizePatchPolicyScheduleType(row.RebootScheduleType.String),
		RebootStartTS:         optionalPositiveInt64Ptr(nullableInt(row.RebootStartTS)),
		ForceRebootLoggedIn:   boolInt64(row.ForceRebootLoggedIn),
		SiteIDs:               patchPolicySiteIDs(row.Sites),
		Targets:               patchPolicyTargetsFromAny(row.Targets),
		Exclusions:            patchPolicyExclusionsFromAny(row.Exclusions, ""),
		Rules:                 row.Rules,
	}
}

func patchPolicySaveConflicts(ctx context.Context, conn *sql.Conn, policyID int64, values patchPolicySaveValues) ([]map[string]any, error) {
	conflicts := []map[string]any{}
	if values.PolicyType == patchPolicyTypeSite && len(values.SiteIDs) > 0 && values.Enabled {
		rows, err := conn.QueryContext(ctx, `
			SELECT p.id, p.name, p.role_scope, ps.site_id
			  FROM engine.patch_policies AS p
			  JOIN engine.patch_policy_sites AS ps ON ps.policy_id=p.id
			 WHERE p.policy_type='site'
			   AND p.enabled=1
			   AND p.id<>$1
		`, policyID)
		if err != nil {
			return nil, err
		}
		siteSet := int64Set(values.SiteIDs)
		for rows.Next() {
			var id, siteID sql.NullInt64
			var name, role sql.NullString
			if err := rows.Scan(&id, &name, &role, &siteID); err != nil {
				_ = rows.Close()
				return nil, err
			}
			_, siteCovered := siteSet[siteID.Int64]
			if !siteID.Valid || !siteCovered || !patchPolicyRoleScopesOverlap(values.RoleScope, role.String) {
				continue
			}
			conflicts = append(conflicts, map[string]any{"type": "site_role_overlap", "policy_id": nullInt(id), "policy_name": nullString(name), "site_id": siteID.Int64, "role_scope": role.String})
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	if values.PolicyType == patchPolicyTypeDeviceFilter && values.Enabled {
		conflicts = append(conflicts, patchPolicyDirectTargetConflicts(ctx, conn, policyID, values)...)
	}
	return conflicts, nil
}

func patchPolicyDirectTargetConflicts(ctx context.Context, conn *sql.Conn, policyID int64, values patchPolicySaveValues) []map[string]any {
	keys := patchPolicyCoverageKeys(values.Targets, values.Exclusions)
	if len(keys) == 0 {
		return nil
	}
	rows, err := conn.QueryContext(ctx, `
		SELECT p.id, p.name, p.role_scope, t.target_type, t.device_guid, t.hostname, NULL::BIGINT AS site_id, t.filter_id
		  FROM engine.patch_policies AS p
		  JOIN engine.patch_policy_targets AS t ON t.policy_id=p.id
		 WHERE p.policy_type='device_filter'
		   AND p.enabled=1
		   AND p.id<>$1
		UNION ALL
		SELECT p.id, p.name, p.role_scope, e.target_type, e.device_guid, e.hostname, e.site_id, e.filter_id
		  FROM engine.patch_policies AS p
		  JOIN engine.patch_policy_exclusions AS e ON e.policy_id=p.id
		 WHERE p.policy_type='device_filter'
		   AND p.enabled=1
		   AND p.id<>$1
	`, policyID)
	if err != nil {
		return []map[string]any{{"type": "target_conflict_check_failed", "message": err.Error()}}
	}
	defer rows.Close()
	conflicts := []map[string]any{}
	for rows.Next() {
		var id, siteID, filterID sql.NullInt64
		var name, roleScope, targetType, guid, hostname sql.NullString
		if err := rows.Scan(&id, &name, &roleScope, &targetType, &guid, &hostname, &siteID, &filterID); err != nil {
			return []map[string]any{{"type": "target_conflict_check_failed", "message": err.Error()}}
		}
		if normalizePatchPolicyRoleScope(roleScope.String) != values.RoleScope {
			continue
		}
		key, ok := patchPolicyTargetOverlapsKeys(keys, targetType.String, guid.String, hostname.String, nullInt(filterID), nullInt(siteID))
		if !ok {
			continue
		}
		conflicts = append(conflicts, map[string]any{"type": "same_layer_target_overlap", "policy_id": nullInt(id), "policy_name": nullString(name), "target_key": key})
	}
	return conflicts
}

func patchPolicyParentOverrideWarnings(ctx context.Context, conn *sql.Conn, policyType string, roleScope string, rules []patchPolicyRule) ([]map[string]any, error) {
	if policyType == patchPolicyTypeGlobal {
		return nil, nil
	}
	approveKeys := map[string]patchPolicyRule{}
	for _, rule := range rules {
		if rule.RuleType != patchPolicyRuleApprove {
			continue
		}
		approveKeys[patchPolicyRuleKey(rule.MatchType, rule.MatchValue)] = rule
	}
	if len(approveKeys) == 0 {
		return nil, nil
	}
	rows, err := conn.QueryContext(ctx, `
		SELECT p.id, p.name, r.match_type, r.match_value
		  FROM engine.patch_policies AS p
		  JOIN engine.patch_policy_rules AS r ON r.policy_id=p.id
		 WHERE p.enabled=1
		   AND p.role_scope=$1
		   AND p.policy_type IN ('global', 'site')
		   AND r.rule_type='block'
	`, normalizePatchPolicyRoleScope(roleScope))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	warnings := []map[string]any{}
	for rows.Next() {
		var id sql.NullInt64
		var name, matchType, matchValue sql.NullString
		if err := rows.Scan(&id, &name, &matchType, &matchValue); err != nil {
			return nil, err
		}
		key := patchPolicyRuleKey(normalizePatchPolicyMatchType(matchType.String), matchValue.String)
		rule, ok := approveKeys[key]
		if !ok || rule.OverrideParentBlock {
			continue
		}
		warnings = append(warnings, map[string]any{
			"type":             "parent_block_override",
			"parent_policy_id": nullInt(id),
			"parent_policy":    nullString(name),
			"match_type":       normalizePatchPolicyMatchType(matchType.String),
			"match_value":      cleanText(matchValue.String),
		})
	}
	return warnings, rows.Err()
}

func (s *postgresOperatorStore) resolvePatchPolicyDevices(ctx context.Context, profile operatorProfile, row patchPolicyRow) ([]patchPolicyDevice, error) {
	resolution, err := s.resolvePatchPolicyDeviceResolution(ctx, profile, row, true)
	if err != nil {
		return nil, err
	}
	return resolution.Eligible, nil
}

func (s *postgresOperatorStore) resolvePatchPolicyDeviceResolution(ctx context.Context, profile operatorProfile, row patchPolicyRow, applyExclusions bool) (patchPolicyDeviceResolution, error) {
	policyType := normalizePatchPolicyType(row.PolicyType.String)
	roleScope := normalizePatchPolicyRoleScope(row.RoleScope.String)
	switch policyType {
	case patchPolicyTypeDeviceFilter:
		targets := []any{}
		for _, target := range row.Targets {
			targets = append(targets, patchPolicyScheduledTarget(target))
		}
		resolution, err := s.resolveScheduledRerunTargets(ctx, profile, targets)
		if err != nil {
			return patchPolicyDeviceResolution{}, err
		}
		raw := []patchPolicyDevice{}
		eligible := []patchPolicyDevice{}
		for _, target := range resolution.Targets {
			if target == nil || !patchPolicyWindowsOS(target.OperatingSystem) {
				continue
			}
			device := patchPolicyDevice{
				DeviceGUID:      strings.ToLower(normalizeCanonicalGUID(target.DeviceGUID)),
				Hostname:        target.Hostname,
				SiteID:          derefInt64(target.SiteID),
				SiteName:        target.SiteName,
				DeviceType:      target.DeviceType,
				OperatingSystem: target.OperatingSystem,
				FilterIDs:       uniquePositiveInt64s(target.FilterIDs),
			}
			raw = append(raw, device)
			if patchPolicyRoleMatches(roleScope, device.DeviceType) {
				eligible = append(eligible, device)
			}
		}
		if applyExclusions {
			var err error
			eligible, err = applyPatchPolicyExclusions(ctx, s, profile, eligible, row.Exclusions)
			if err != nil {
				return patchPolicyDeviceResolution{}, err
			}
		}
		return patchPolicyDeviceResolution{Raw: raw, Eligible: eligible}, nil
	case patchPolicyTypeSite, patchPolicyTypeGlobal:
		devices, err := s.fetchFilterDevices(ctx, profile)
		if err != nil {
			return patchPolicyDeviceResolution{}, err
		}
		siteSet := int64Set(patchPolicySiteIDs(row.Sites))
		raw := []patchPolicyDevice{}
		eligible := []patchPolicyDevice{}
		for _, device := range devices {
			osText := cleanText(firstPresentAny(device["operating_system"], nestedAny(device, "summary", "operating_system")))
			if !patchPolicyWindowsOS(osText) {
				continue
			}
			siteID := coerceInt64(device["site_id"])
			_, siteCovered := siteSet[siteID]
			if policyType == patchPolicyTypeSite && !siteCovered {
				continue
			}
			deviceType := cleanText(firstPresentAny(device["device_type"], nestedAny(device, "summary", "device_type")))
			record := patchPolicyDevice{
				DeviceGUID:      strings.ToLower(normalizeCanonicalGUID(firstPresentAny(device["device_guid"], device["guid"]))),
				Hostname:        cleanText(device["hostname"]),
				SiteID:          siteID,
				SiteName:        cleanText(device["site_name"]),
				DeviceType:      deviceType,
				OperatingSystem: osText,
			}
			raw = append(raw, record)
			if patchPolicyRoleMatches(roleScope, deviceType) {
				eligible = append(eligible, record)
			}
		}
		if applyExclusions {
			var err error
			eligible, err = applyPatchPolicyExclusions(ctx, s, profile, eligible, row.Exclusions)
			if err != nil {
				return patchPolicyDeviceResolution{}, err
			}
		}
		return patchPolicyDeviceResolution{Raw: raw, Eligible: eligible}, nil
	default:
		return patchPolicyDeviceResolution{}, nil
	}
}

func applyPatchPolicyExclusions(ctx context.Context, s *postgresOperatorStore, profile operatorProfile, devices []patchPolicyDevice, exclusions []map[string]any) ([]patchPolicyDevice, error) {
	if len(exclusions) == 0 || len(devices) == 0 {
		return devices, nil
	}
	exclusionTargets := []any{}
	exclusionModeByTarget := map[string]string{}
	exclusionModeByFilterID := map[int64]string{}
	for _, exclusion := range exclusions {
		mode := normalizePatchPolicyExclusionType(exclusion["exclusion_type"])
		if mode == "" {
			continue
		}
		target := patchPolicyScheduledTarget(exclusion)
		exclusionTargets = append(exclusionTargets, target)
		if key := patchPolicyTargetIdentityFromAny(target); key != "" {
			exclusionModeByTarget[key] = mode
		}
		if normalizePatchPolicyTargetType(firstPresentAny(exclusion["target_type"], exclusion["kind"], exclusion["type"])) == "filter" || coerceInt64(exclusion["filter_id"]) > 0 {
			if filterID := coerceInt64(exclusion["filter_id"]); filterID > 0 {
				exclusionModeByFilterID[filterID] = mode
			}
		}
	}
	if len(exclusionTargets) == 0 {
		return devices, nil
	}
	resolution, err := s.resolveScheduledRerunTargets(ctx, profile, exclusionTargets)
	if err != nil {
		return nil, err
	}
	modeByDevice := map[string]string{}
	for _, target := range resolution.Targets {
		if target == nil {
			continue
		}
		mode := ""
		siteID := derefInt64(target.SiteID)
		for _, key := range []string{
			patchPolicyTargetKeyWithSite("device", target.DeviceGUID, target.Hostname, 0, siteID),
			patchPolicyTargetKey("device", target.DeviceGUID, "", 0),
			patchPolicyTargetKeyWithSite("device", "", target.Hostname, 0, siteID),
			patchPolicyTargetKey("device", "", target.Hostname, 0),
		} {
			if v := exclusionModeByTarget[key]; v != "" {
				mode = v
				break
			}
		}
		if mode == "" {
			for _, filterID := range uniquePositiveInt64s(target.FilterIDs) {
				if v := exclusionModeByFilterID[filterID]; v != "" {
					mode = v
					break
				}
			}
		}
		if mode == "" {
			continue
		}
		modeByDevice[patchPolicyDeviceIdentity(patchPolicyDevice{DeviceGUID: target.DeviceGUID, Hostname: target.Hostname, SiteID: siteID})] = mode
	}
	for idx := range devices {
		if mode := modeByDevice[patchPolicyDeviceIdentity(devices[idx])]; mode != "" {
			devices[idx].ExclusionMode = mode
		}
	}
	return devices, nil
}

type patchPolicyDeviceMatch struct {
	Row       patchPolicyRow
	Device    patchPolicyDevice
	LocalMode string
}

func (s *postgresOperatorStore) patchPolicyEffectiveScopes(ctx context.Context, profile operatorProfile, rows []patchPolicyRow) (map[int64]*patchPolicyEffectiveScope, error) {
	matchesByDevice := map[string][]patchPolicyDeviceMatch{}
	for _, row := range rows {
		if !boolInt64(row.Enabled) {
			continue
		}
		resolution, err := s.resolvePatchPolicyDeviceResolution(ctx, profile, row, false)
		if err != nil {
			return nil, err
		}
		withLocalExclusions := append([]patchPolicyDevice(nil), resolution.Eligible...)
		withLocalExclusions, err = applyPatchPolicyExclusions(ctx, s, profile, withLocalExclusions, row.Exclusions)
		if err != nil {
			return nil, err
		}
		localModeByDevice := map[string]string{}
		for _, device := range withLocalExclusions {
			if device.ExclusionMode == "" {
				continue
			}
			if key := patchPolicyDeviceIdentity(device); key != "" {
				localModeByDevice[key] = device.ExclusionMode
			}
		}
		for _, device := range resolution.Eligible {
			key := patchPolicyDeviceIdentity(device)
			if key == "" {
				continue
			}
			matchesByDevice[key] = append(matchesByDevice[key], patchPolicyDeviceMatch{Row: row, Device: device, LocalMode: localModeByDevice[key]})
		}
	}
	scopes := map[int64]*patchPolicyEffectiveScope{}
	for deviceKey, matches := range matchesByDevice {
		sort.SliceStable(matches, func(i, j int) bool {
			leftDepth := patchPolicyDepth(matches[i].Row)
			rightDepth := patchPolicyDepth(matches[j].Row)
			if leftDepth != rightDepth {
				return leftDepth < rightDepth
			}
			return nullInt(matches[i].Row.ID) < nullInt(matches[j].Row.ID)
		})
		if len(matches) == 0 {
			continue
		}
		deepestDepth := patchPolicyDepth(matches[len(matches)-1].Row)
		deepestMatches := []patchPolicyDeviceMatch{}
		for _, match := range matches {
			if patchPolicyDepth(match.Row) == deepestDepth {
				deepestMatches = append(deepestMatches, match)
			}
		}
		addScopeDevice := func(match patchPolicyDeviceMatch, device patchPolicyDevice, hierarchy []patchPolicyRow) {
			effectivePolicyID := nullInt(match.Row.ID)
			if effectivePolicyID <= 0 {
				return
			}
			scope := scopes[effectivePolicyID]
			if scope == nil {
				scope = &patchPolicyEffectiveScope{Policy: match.Row, RulesByDevice: map[string][]patchPolicyRule{}}
				scopes[effectivePolicyID] = scope
			}
			scope.Devices = append(scope.Devices, device)
			scope.RulesByDevice[deviceKey] = patchPolicyEffectiveRules(hierarchy)
		}
		if len(deepestMatches) > 1 {
			for _, match := range deepestMatches {
				device, hierarchy := patchPolicyDeviceWithHierarchy(matches, match)
				device.Conflict = true
				addScopeDevice(match, device, hierarchy)
			}
			continue
		}
		if len(deepestMatches) == 0 {
			continue
		}
		effectiveMatch := deepestMatches[0]
		device, hierarchy := patchPolicyDeviceWithHierarchy(matches, effectiveMatch)
		addScopeDevice(effectiveMatch, device, hierarchy)
	}
	return scopes, nil
}

func patchPolicyDeviceWithHierarchy(matches []patchPolicyDeviceMatch, effectiveMatch patchPolicyDeviceMatch) (patchPolicyDevice, []patchPolicyRow) {
	device := effectiveMatch.Device
	hierarchy := make([]patchPolicyRow, 0, len(matches))
	hierarchyIDs := make([]int64, 0, len(matches))
	exclusionMode := ""
	exclusionPolicyID := int64(0)
	exclusionOverridePolicyID := int64(0)
	for _, match := range matches {
		hierarchy = append(hierarchy, match.Row)
		if id := nullInt(match.Row.ID); id > 0 {
			hierarchyIDs = append(hierarchyIDs, id)
		}
		switch match.LocalMode {
		case patchPolicyExclusionOverride:
			exclusionMode = ""
			exclusionOverridePolicyID = nullInt(match.Row.ID)
		case patchPolicyExclusionUnmanaged, patchPolicyExclusionFrozen:
			exclusionMode = match.LocalMode
			exclusionPolicyID = nullInt(match.Row.ID)
		}
	}
	device.ExclusionMode = exclusionMode
	device.ExclusionPolicyID = exclusionPolicyID
	device.ExclusionOverridePolicyID = exclusionOverridePolicyID
	device.HierarchyPolicyIDs = hierarchyIDs
	return device, hierarchy
}

func patchPolicyEffectiveRules(hierarchy []patchPolicyRow) []patchPolicyRule {
	if len(hierarchy) == 0 {
		return nil
	}
	out := []patchPolicyRule{}
	for idx, row := range hierarchy {
		if idx == len(hierarchy)-1 {
			out = append(out, row.Rules...)
			continue
		}
		for _, rule := range row.Rules {
			if rule.RuleType == patchPolicyRuleBlock {
				out = append(out, rule)
			}
		}
	}
	return out
}

type patchPolicyPendingInventoryIndex struct {
	BreakdownByPolicyID   map[int64]map[string]int
	DeviceCountByPolicyID map[int64]int
	deviceGUIDsByPolicyID map[int64]map[string]struct{}
	RowsByInventoryID     map[int64]patchPolicyPendingInventoryRow
}

type patchPolicyInventoryAssignment struct {
	EffectivePolicyID    int64
	EffectivePolicyName  string
	EffectivePolicyType  string
	EffectiveRoleScope   string
	HierarchyPolicyIDs   []int64
	HierarchyPolicyNames []string
	Hierarchy            []map[string]any
	Rules                []patchPolicyRule
	DeferralDays         int64
}

type patchPolicyPendingInventoryRow struct {
	patchPolicyInventoryAssignment
	InstallCandidate bool
	SkipReason       string
}

func (s *postgresOperatorStore) patchPolicyPendingInventoryIndex(ctx context.Context, profile operatorProfile, rows []patchPolicyRow, activeJobs map[string]map[string]any) (patchPolicyPendingInventoryIndex, error) {
	index := patchPolicyPendingInventoryIndex{
		BreakdownByPolicyID:   map[int64]map[string]int{},
		DeviceCountByPolicyID: map[int64]int{},
		deviceGUIDsByPolicyID: map[int64]map[string]struct{}{},
		RowsByInventoryID:     map[int64]patchPolicyPendingInventoryRow{},
	}
	if rows == nil {
		if err := s.ensurePatchPolicySchema(ctx); err != nil {
			return index, err
		}
		conn, err := s.db.Conn(ctx)
		if err != nil {
			return index, errors.Join(errOperatorStoreDown, err)
		}
		loadedRows, loadErr := loadPatchPolicyRows(ctx, conn, "", nil)
		closeErr := conn.Close()
		if loadErr != nil {
			if closeErr != nil {
				return index, errors.Join(loadErr, closeErr)
			}
			return index, loadErr
		}
		if closeErr != nil {
			return index, closeErr
		}
		rows = loadedRows
	}
	if len(rows) == 0 {
		return index, nil
	}
	policyByID := map[int64]patchPolicyRow{}
	for _, row := range rows {
		if id := nullInt(row.ID); id > 0 {
			policyByID[id] = row
		}
	}
	effectiveScopes, err := s.patchPolicyEffectiveScopes(ctx, profile, rows)
	if err != nil {
		return index, err
	}
	assignmentsByGUID := map[string]patchPolicyInventoryAssignment{}
	pendingDevices := map[string]patchPolicyDevice{}
	for policyID, scope := range effectiveScopes {
		if scope == nil {
			continue
		}
		effectiveRow := firstPatchPolicyRow(policyByID[policyID], scope.Policy)
		effectivePolicyID := nullInt(effectiveRow.ID)
		if effectivePolicyID <= 0 {
			continue
		}
		for _, device := range scope.Devices {
			if device.ExclusionMode != "" || device.Conflict {
				continue
			}
			guid := strings.ToLower(normalizeCanonicalGUID(device.DeviceGUID))
			if guid == "" {
				continue
			}
			hierarchyRows := patchPolicyRowsForIDs(policyByID, device.HierarchyPolicyIDs)
			hierarchyNames := make([]string, 0, len(hierarchyRows))
			for _, hierarchyRow := range hierarchyRows {
				hierarchyNames = append(hierarchyNames, nullString(hierarchyRow.Name))
			}
			deviceKey := patchPolicyDeviceIdentity(device)
			rules := scope.RulesByDevice[deviceKey]
			if len(rules) == 0 {
				rules = effectiveRow.Rules
			}
			assignmentsByGUID[guid] = patchPolicyInventoryAssignment{
				EffectivePolicyID:    effectivePolicyID,
				EffectivePolicyName:  nullString(effectiveRow.Name),
				EffectivePolicyType:  normalizePatchPolicyType(effectiveRow.PolicyType.String),
				EffectiveRoleScope:   normalizePatchPolicyRoleScope(effectiveRow.RoleScope.String),
				HierarchyPolicyIDs:   append([]int64(nil), device.HierarchyPolicyIDs...),
				HierarchyPolicyNames: hierarchyNames,
				Hierarchy:            patchPolicyHierarchySummary(hierarchyRows),
				Rules:                append([]patchPolicyRule(nil), rules...),
				DeferralDays:         firstPositiveInt64(nullInt(effectiveRow.DeferralDays), 14),
			}
			pendingDevices[guid] = device
		}
	}
	if len(pendingDevices) == 0 {
		return index, nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return index, errors.Join(errOperatorStoreDown, err)
	}
	if activeJobs == nil {
		activeJobs, err = loadActivePatchInstallJobs(ctx, conn, profile)
		if err != nil {
			_ = conn.Close()
			return index, err
		}
	}
	pendingRows, err := loadPendingPolicyPatchRows(ctx, conn, pendingDevices)
	closeErr := conn.Close()
	if err != nil {
		if closeErr != nil {
			return index, errors.Join(err, closeErr)
		}
		return index, err
	}
	if closeErr != nil {
		return index, closeErr
	}
	for _, pendingRow := range pendingRows {
		inventoryID := nullInt(pendingRow.ID)
		guid := strings.ToLower(normalizeCanonicalGUID(pendingRow.DeviceGUID.String))
		assignment, ok := assignmentsByGUID[guid]
		if !ok || inventoryID <= 0 {
			continue
		}
		rowResult := patchPolicyPendingInventoryRow{patchPolicyInventoryAssignment: assignment, InstallCandidate: true}
		patch := patchInventoryPayload(pendingRow)
		if !patchPolicyDeferralSatisfied(pendingRow, assignment.DeferralDays, time.Now().Unix()) {
			rowResult.InstallCandidate = false
			rowResult.SkipReason = "deferred"
		} else if patchPolicyDecision(assignment.Rules, patch) != patchPolicyRuleApprove {
			rowResult.InstallCandidate = false
			rowResult.SkipReason = "not_approved"
		} else {
			for _, activeKey := range patchActiveIdentityKeys(patch) {
				if activeJobs[activeKey] != nil {
					rowResult.InstallCandidate = false
					rowResult.SkipReason = "active_lockout"
					break
				}
			}
		}
		index.RowsByInventoryID[inventoryID] = rowResult
		if !rowResult.InstallCandidate {
			continue
		}
		patchPolicyAddPendingBreakdownCount(&index, assignment, guid)
	}
	return index, nil
}

func patchPolicyAddPendingBreakdownCount(index *patchPolicyPendingInventoryIndex, assignment patchPolicyInventoryAssignment, deviceGUID string) {
	if index == nil || assignment.EffectivePolicyID <= 0 {
		return
	}
	policyType := normalizePatchPolicyType(assignment.EffectivePolicyType)
	if policyType == "" {
		return
	}
	if index.BreakdownByPolicyID == nil {
		index.BreakdownByPolicyID = map[int64]map[string]int{}
	}
	if index.BreakdownByPolicyID[assignment.EffectivePolicyID] == nil {
		index.BreakdownByPolicyID[assignment.EffectivePolicyID] = map[string]int{}
	}
	index.BreakdownByPolicyID[assignment.EffectivePolicyID][policyType]++
	deviceGUID = strings.ToLower(normalizeCanonicalGUID(deviceGUID))
	if deviceGUID == "" {
		return
	}
	if index.deviceGUIDsByPolicyID == nil {
		index.deviceGUIDsByPolicyID = map[int64]map[string]struct{}{}
	}
	if index.deviceGUIDsByPolicyID[assignment.EffectivePolicyID] == nil {
		index.deviceGUIDsByPolicyID[assignment.EffectivePolicyID] = map[string]struct{}{}
	}
	index.deviceGUIDsByPolicyID[assignment.EffectivePolicyID][deviceGUID] = struct{}{}
	if index.DeviceCountByPolicyID == nil {
		index.DeviceCountByPolicyID = map[int64]int{}
	}
	index.DeviceCountByPolicyID[assignment.EffectivePolicyID] = len(index.deviceGUIDsByPolicyID[assignment.EffectivePolicyID])
}

func firstPatchPolicyRow(left patchPolicyRow, right patchPolicyRow) patchPolicyRow {
	if nullInt(left.ID) > 0 {
		return left
	}
	return right
}

func patchPolicyRowsForIDs(rowsByID map[int64]patchPolicyRow, ids []int64) []patchPolicyRow {
	out := []patchPolicyRow{}
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if row, ok := rowsByID[id]; ok {
			out = append(out, row)
		}
	}
	return out
}

func patchPolicyHierarchySummary(rows []patchPolicyRow) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"id":          nullInt(row.ID),
			"name":        nullString(row.Name),
			"policy_type": normalizePatchPolicyType(row.PolicyType.String),
			"role_scope":  normalizePatchPolicyRoleScope(row.RoleScope.String),
		})
	}
	return out
}

func patchPolicyPendingTotal(counts map[string]int) int {
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}

func patchPolicyPendingBreakdownPayload(counts map[string]int) []map[string]any {
	out := []map[string]any{}
	for _, policyType := range []string{patchPolicyTypeGlobal, patchPolicyTypeSite, patchPolicyTypeDeviceFilter} {
		count := counts[policyType]
		if count <= 0 {
			continue
		}
		out = append(out, map[string]any{
			"policy_type": policyType,
			"label":       patchPolicyTypeDisplayLabel(policyType),
			"count":       count,
		})
	}
	return out
}

func patchPolicyTypeDisplayLabel(policyType string) string {
	switch normalizePatchPolicyType(policyType) {
	case patchPolicyTypeGlobal:
		return "Global"
	case patchPolicyTypeDeviceFilter:
		return "Device Filter"
	default:
		return "Site-Level Override"
	}
}

func attachPatchPolicyInventoryPayload(payload map[string]any, index patchPolicyPendingInventoryIndex) {
	if payload == nil {
		return
	}
	inventoryID := coerceInt64(firstPresentAny(payload["inventory_id"], payload["id"]))
	row, ok := index.RowsByInventoryID[inventoryID]
	if !ok {
		return
	}
	payload["patch_policy_effective_policy_id"] = row.EffectivePolicyID
	payload["patch_policy_effective_policy_name"] = row.EffectivePolicyName
	payload["patch_policy_effective_policy_type"] = row.EffectivePolicyType
	payload["patch_policy_effective_policy_type_label"] = patchPolicyTypeDisplayLabel(row.EffectivePolicyType)
	payload["patch_policy_effective_role_scope"] = row.EffectiveRoleScope
	payload["patch_policy_hierarchy_policy_ids"] = row.HierarchyPolicyIDs
	payload["patch_policy_hierarchy_policy_names"] = row.HierarchyPolicyNames
	payload["patch_policy_hierarchy"] = row.Hierarchy
	payload["patch_policy_install_candidate"] = row.InstallCandidate
	payload["patch_policy_skip_reason"] = row.SkipReason
}

func (s *postgresOperatorStore) patchPolicyDynamicConflicts(ctx context.Context, profile operatorProfile, currentPolicyID int64, current patchPolicyRow) ([]map[string]any, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	rows, err := loadPatchPolicyRows(ctx, conn, patchPolicyTypeDeviceFilter, nil)
	if err != nil {
		return nil, err
	}
	if currentPolicyID <= 0 && normalizePatchPolicyType(current.PolicyType.String) == patchPolicyTypeDeviceFilter {
		rows = append(rows, current)
	}
	devicePolicies := []patchPolicyRow{}
	for _, row := range rows {
		if !boolInt64(row.Enabled) {
			continue
		}
		if currentPolicyID > 0 && nullInt(row.ID) == currentPolicyID {
			row = current
		}
		devicePolicies = append(devicePolicies, row)
	}
	coverage := map[string][]patchPolicyRow{}
	for _, row := range devicePolicies {
		devices, err := s.resolvePatchPolicyDevices(ctx, profile, row)
		if err != nil {
			return nil, err
		}
		for _, device := range devices {
			key := patchPolicyDeviceIdentity(device)
			if key == "" {
				continue
			}
			coverage[key] = append(coverage[key], row)
		}
	}
	conflicts := []map[string]any{}
	for key, policies := range coverage {
		if len(policies) < 2 {
			continue
		}
		conflicts = append(conflicts, map[string]any{"type": "dynamic_device_overlap", "device": key, "policies": patchPolicyPayloads(policies)})
	}
	return conflicts, nil
}

func (s *postgresOperatorStore) evaluatePatchPoliciesAt(ctx context.Context, profile operatorProfile, onlyPolicyID int64, scheduledTS int64, now int64, manual bool) (map[string]any, error) {
	result, err := s.evaluatePatchPoliciesAtOnce(ctx, profile, onlyPolicyID, scheduledTS, now, manual)
	if err == nil || !errors.Is(err, driver.ErrBadConn) {
		return result, err
	}
	return s.evaluatePatchPoliciesAtOnce(ctx, profile, onlyPolicyID, scheduledTS, now, manual)
}

func (s *postgresOperatorStore) evaluatePatchPoliciesAtOnce(ctx context.Context, profile operatorProfile, onlyPolicyID int64, scheduledTS int64, now int64, manual bool) (map[string]any, error) {
	if err := s.ensurePatchPolicySchema(ctx); err != nil {
		return nil, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	if err := syncPatchCatalogFromInventory(ctx, conn, now); err != nil {
		_ = conn.Close()
		return nil, err
	}
	rows, err := loadPatchPolicyRows(ctx, conn, "", nil)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := conn.Close(); err != nil {
		return nil, err
	}
	effectiveScopes, err := s.patchPolicyEffectiveScopes(ctx, profile, rows)
	if err != nil {
		return nil, err
	}
	conn, err = s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	activeJobs, err := loadActivePatchInstallJobs(ctx, conn, profile)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := conn.Close(); err != nil {
		return nil, err
	}
	summaries := []map[string]any{}
	for _, row := range rows {
		if !boolInt64(row.Enabled) {
			continue
		}
		if onlyPolicyID > 0 && nullInt(row.ID) != onlyPolicyID {
			continue
		}
		if !manual {
			due, err := s.patchPolicyDueAt(ctx, row, scheduledTS)
			if err != nil {
				return nil, err
			}
			if !due {
				continue
			}
		}
		conflictedKeys, err := s.patchPolicyConflictedDeviceKeys(ctx, profile, row)
		if err != nil {
			return nil, err
		}
		conn, err := s.db.Conn(ctx)
		if err != nil {
			return nil, errors.Join(errOperatorStoreDown, err)
		}
		summary, err := s.createPatchPolicyRunJobs(ctx, profile, conn, row, effectiveScopes[nullInt(row.ID)], scheduledTS, now, activeJobs, conflictedKeys)
		closeErr := conn.Close()
		if err != nil {
			if closeErr != nil {
				return nil, errors.Join(err, closeErr)
			}
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		summaries = append(summaries, summary)
	}
	return map[string]any{"status": "evaluated", "scheduled_ts": scheduledTS, "runs": summaries, "count": len(summaries)}, nil
}

func (s *postgresOperatorStore) patchPolicyDueAt(ctx context.Context, row patchPolicyRow, scheduledTS int64) (bool, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return false, errors.Join(errOperatorStoreDown, err)
	}
	due := patchPolicyDue(ctx, conn, row, scheduledTS)
	closeErr := conn.Close()
	if closeErr != nil {
		return false, closeErr
	}
	return due, nil
}

func (s *postgresOperatorStore) createPatchPolicyRunJobs(ctx context.Context, profile operatorProfile, conn *sql.Conn, row patchPolicyRow, scope *patchPolicyEffectiveScope, scheduledTS int64, now int64, activeJobs map[string]map[string]any, conflictedKeys map[string]bool) (map[string]any, error) {
	devices := []patchPolicyDevice{}
	rulesByDevice := map[string][]patchPolicyRule{}
	if scope != nil {
		devices = append(devices, scope.Devices...)
		rulesByDevice = scope.RulesByDevice
	}
	deviceByGUID := map[string]patchPolicyDevice{}
	excludedDevices := 0
	conflictedDevices := 0
	for _, device := range devices {
		targetKey := patchPolicyDeviceIdentity(device)
		if conflictedKeys[targetKey] {
			device.Conflict = true
		}
		if device.ExclusionMode != "" {
			excludedDevices++
			continue
		}
		if device.Conflict {
			conflictedDevices++
			continue
		}
		guid := strings.ToLower(normalizeCanonicalGUID(device.DeviceGUID))
		if guid != "" {
			deviceByGUID[guid] = device
		}
	}
	runID, inserted, err := ensurePatchPolicyRun(ctx, conn, nullInt(row.ID), scheduledTS, now)
	if err != nil {
		return nil, err
	}
	if !inserted {
		return map[string]any{"policy_id": nullInt(row.ID), "policy_name": nullString(row.Name), "policy_run_id": runID, "status": "already_evaluated"}, nil
	}
	if err := upsertPatchPolicyDeviceStates(ctx, conn, row, devices, conflictedKeys, runID, now); err != nil {
		return nil, err
	}
	patchRows, err := loadPendingPolicyPatchRows(ctx, conn, deviceByGUID)
	if err != nil {
		return nil, err
	}
	groups := map[string][]patchInventoryRow{}
	groupPatch := map[string]map[string]any{}
	skipped := map[string]int{}
	for _, patchRow := range patchRows {
		if !patchPolicyDeferralSatisfied(patchRow, firstPositiveInt64(nullInt(row.DeferralDays), 14), now) {
			skipped["deferred"]++
			continue
		}
		device := deviceByGUID[strings.ToLower(normalizeCanonicalGUID(patchRow.DeviceGUID.String))]
		deviceKey := patchPolicyDeviceIdentity(device)
		rules := rulesByDevice[deviceKey]
		if len(rules) == 0 {
			rules = row.Rules
		}
		decision := patchPolicyDecision(rules, patchInventoryPayload(patchRow))
		if decision != patchPolicyRuleApprove {
			skipped["not_approved"]++
			continue
		}
		patch := patchInventoryPayload(patchRow)
		key := firstText(patchPolicyPrimaryIdentityKey(patch), "title:"+strings.ToLower(cleanText(patch["title"])))
		if key == "" {
			skipped["missing_identity"]++
			continue
		}
		active := false
		for _, activeKey := range patchActiveIdentityKeys(patch) {
			if activeJobs[activeKey] != nil {
				active = true
				break
			}
		}
		if active {
			skipped["active_lockout"]++
			continue
		}
		groups[key] = append(groups[key], patchRow)
		groupPatch[key] = patch
	}
	jobIDs := []int64{}
	for key, rows := range groups {
		targets := []any{}
		seenTargets := map[string]bool{}
		for _, patchRow := range rows {
			device := deviceByGUID[strings.ToLower(normalizeCanonicalGUID(patchRow.DeviceGUID.String))]
			targetKey := patchPolicyDeviceIdentity(device)
			if targetKey == "" || seenTargets[targetKey] {
				continue
			}
			seenTargets[targetKey] = true
			targets = append(targets, map[string]any{
				"kind":        "device",
				"hostname":    device.Hostname,
				"device_guid": device.DeviceGUID,
				"site_id":     device.SiteID,
				"site_name":   device.SiteName,
			})
		}
		if len(targets) == 0 {
			continue
		}
		patch := groupPatch[key]
		component := map[string]any{
			"kind":          patchInstallComponentKind,
			"trigger":       "policy",
			"policy_id":     nullInt(row.ID),
			"policy_run_id": runID,
			"patch":         patch,
		}
		jobID, err := insertPatchPolicyScheduledJob(ctx, conn, row, runID, scheduledTS, now, component, targets)
		if err != nil {
			return nil, err
		}
		jobIDs = append(jobIDs, jobID)
	}
	summary := map[string]any{
		"policy_id":     nullInt(row.ID),
		"policy_name":   nullString(row.Name),
		"policy_run_id": runID,
		"status":        "scheduled",
		"jobs_created":  len(jobIDs),
		"job_ids":       jobIDs,
		"skipped":       skipped,
		"target_count":  len(deviceByGUID),
		"excluded":      excludedDevices,
		"conflicted":    conflictedDevices,
	}
	if err := finishPatchPolicyRun(ctx, conn, runID, "scheduled", summary, now); err != nil {
		return nil, err
	}
	return summary, nil
}

func (s *postgresOperatorStore) patchPolicyConflictedDeviceKeys(ctx context.Context, profile operatorProfile, current patchPolicyRow) (map[string]bool, error) {
	if normalizePatchPolicyType(current.PolicyType.String) != patchPolicyTypeDeviceFilter {
		return map[string]bool{}, nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	rows, err := loadPatchPolicyRows(ctx, conn, patchPolicyTypeDeviceFilter, nil)
	closeErr := conn.Close()
	if err != nil {
		if closeErr != nil {
			return nil, errors.Join(err, closeErr)
		}
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	coverage := map[string]int{}
	for _, row := range rows {
		if !boolInt64(row.Enabled) {
			continue
		}
		devices, err := s.resolvePatchPolicyDevices(ctx, profile, row)
		if err != nil {
			return nil, err
		}
		seenForPolicy := map[string]bool{}
		for _, device := range devices {
			key := patchPolicyDeviceIdentity(device)
			if key == "" || seenForPolicy[key] {
				continue
			}
			seenForPolicy[key] = true
			coverage[key]++
		}
	}
	conflicted := map[string]bool{}
	for key, count := range coverage {
		if count > 1 {
			conflicted[key] = true
		}
	}
	return conflicted, nil
}

func upsertPatchPolicyDeviceStates(ctx context.Context, conn *sql.Conn, row patchPolicyRow, devices []patchPolicyDevice, conflictedKeys map[string]bool, runID int64, now int64) error {
	policyID := nullInt(row.ID)
	policyType := normalizePatchPolicyType(row.PolicyType.String)
	enforcementMode := "unmanaged"
	if boolInt64(row.ManagedUpdateMode) {
		enforcementMode = "managed"
	}
	for _, device := range devices {
		hostname := cleanText(device.Hostname)
		if hostname == "" {
			continue
		}
		deviceKey := patchPolicyDeviceIdentity(device)
		conflicted := device.Conflict || conflictedKeys[deviceKey]
		mode := enforcementMode
		if device.ExclusionMode == patchPolicyExclusionFrozen {
			mode = patchPolicyExclusionFrozen
		}
		if device.ExclusionMode == patchPolicyExclusionUnmanaged {
			mode = patchPolicyExclusionUnmanaged
		}
		metadata := map[string]any{
			"policy_type":                  policyType,
			"policy_run_id":                runID,
			"site_id":                      device.SiteID,
			"site_name":                    device.SiteName,
			"conflicted":                   conflicted,
			"hierarchy_policy_ids":         device.HierarchyPolicyIDs,
			"exclusion_policy_id":          nullablePositiveInt64Any(device.ExclusionPolicyID),
			"exclusion_override_policy_id": nullablePositiveInt64Any(device.ExclusionOverridePolicyID),
		}
		metadataJSON, _ := json.Marshal(metadata)
		if _, err := conn.ExecContext(ctx, `
			DELETE FROM engine.patch_policy_device_state
			 WHERE LOWER(hostname)=LOWER($1)
			    OR (COALESCE(device_guid, '')<>'' AND LOWER(device_guid)=LOWER($2))
		`, hostname, normalizeCanonicalGUID(device.DeviceGUID)); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO engine.patch_policy_device_state(
				device_guid, hostname, effective_policy_id, exclusion_mode, enforcement_mode,
				enforcement_status, drift_detected, last_evaluated_at, metadata_json
			) VALUES ($1,$2,$3,$4,$5,$6,0,$7,$8)
		`, nullEmpty(normalizeCanonicalGUID(device.DeviceGUID)), hostname, nullablePositiveInt64Arg(policyID), nullEmpty(device.ExclusionMode), mode, "pending", now, string(metadataJSON)); err != nil {
			return err
		}
	}
	return nil
}

func syncPatchCatalogFromInventory(ctx context.Context, conn *sql.Conn, now int64) error {
	rows, err := conn.QueryContext(ctx, `
		SELECT patch_key, kb, title, classification, severity, published_at, MIN(captured_at), MAX(captured_at), metadata_json
		  FROM engine.device_patch_inventory
		 WHERE TRIM(COALESCE(title, kb, patch_key, '')) <> ''
	  GROUP BY patch_key, kb, title, classification, severity, published_at, metadata_json
	`)
	if err != nil {
		return err
	}
	type catalogRow struct {
		PatchKey, KB, Title, Classification, Severity, Metadata sql.NullString
		PublishedAt, FirstSeen, LastSeen                        sql.NullInt64
	}
	catalogRows := []catalogRow{}
	for rows.Next() {
		var row catalogRow
		if err := rows.Scan(&row.PatchKey, &row.KB, &row.Title, &row.Classification, &row.Severity, &row.PublishedAt, &row.FirstSeen, &row.LastSeen, &row.Metadata); err != nil {
			_ = rows.Close()
			return err
		}
		catalogRows = append(catalogRows, row)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, row := range catalogRows {
		metadata := agentDetailsMapFromAny(parseJSON(row.Metadata))
		updateID := cleanText(firstPresentAny(metadata["update_id"], metadata["updateID"]))
		revision := coerceInt64(firstPresentAny(metadata["revision_number"], metadata["revision"]))
		firstSeen := firstPositiveInt64(nullInt(row.FirstSeen), now)
		lastSeen := firstPositiveInt64(nullInt(row.LastSeen), now)
		var id int64
		err := conn.QueryRowContext(ctx, `
			SELECT COALESCE(MIN(id), 0)
			  FROM engine.patch_catalog_entries
			 WHERE COALESCE(patch_key, '')=COALESCE($1, '')
			   AND COALESCE(kb, '')=COALESCE($2, '')
			   AND COALESCE(update_id, '')=COALESCE($3, '')
			   AND COALESCE(revision_number, 0)=COALESCE($4, 0)
		`, nullEmpty(row.PatchKey.String), nullEmpty(row.KB.String), nullEmpty(updateID), nullablePositiveInt64Arg(revision)).Scan(&id)
		if err != nil {
			return err
		}
		if id > 0 {
			_, err = conn.ExecContext(ctx, `
				UPDATE engine.patch_catalog_entries
				   SET title=$1, classification=$2, severity=$3, published_at=$4,
				       first_seen_at=LEAST(first_seen_at, $5), last_seen_at=GREATEST(last_seen_at, $6), metadata_json=$7
				 WHERE id=$8
			`, row.Title.String, nullEmpty(row.Classification.String), nullEmpty(row.Severity.String), nullableInt64Arg(&row.PublishedAt.Int64), firstSeen, lastSeen, row.Metadata.String, id)
		} else {
			_, err = conn.ExecContext(ctx, `
				INSERT INTO engine.patch_catalog_entries(
					patch_key, kb, update_id, revision_number, title, classification, severity,
					published_at, first_seen_at, last_seen_at, metadata_json
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			`, nullEmpty(row.PatchKey.String), nullEmpty(row.KB.String), nullEmpty(updateID), nullablePositiveInt64Arg(revision),
				row.Title.String, nullEmpty(row.Classification.String), nullEmpty(row.Severity.String), nullableInt64Arg(&row.PublishedAt.Int64),
				firstSeen, lastSeen, row.Metadata.String)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func loadPendingPolicyPatchRows(ctx context.Context, conn *sql.Conn, devices map[string]patchPolicyDevice) ([]patchInventoryRow, error) {
	guids := make([]string, 0, len(devices))
	for guid := range devices {
		guids = append(guids, guid)
	}
	guids = uniqueStrings(guids)
	if len(guids) == 0 {
		return []patchInventoryRow{}, nil
	}
	args := make([]any, 0, len(guids))
	placeholders := make([]string, 0, len(guids))
	for _, guid := range guids {
		args = append(args, guid)
		placeholders = append(placeholders, "$"+strconv.Itoa(len(args)))
	}
	rows, err := conn.QueryContext(ctx, `
		SELECT dpi.id, dpi.device_guid, dpi.patch_key, dpi.kb, dpi.title, dpi.state, dpi.source,
		       dpi.classification, dpi.severity, dpi.installed_on, dpi.published_at,
		       COALESCE(pce.first_seen_at, dpi.captured_at), dpi.metadata_json,
		       d.guid, d.hostname, d.agent_id, d.operating_system, ds.site_id, s.name
		  FROM engine.device_patch_inventory AS dpi
		  JOIN engine.devices AS d ON d.guid=dpi.device_guid
	 LEFT JOIN engine.device_sites AS ds ON ds.device_hostname=d.hostname
	 LEFT JOIN engine.sites AS s ON s.id=ds.site_id
	 LEFT JOIN engine.patch_catalog_entries AS pce
		    ON COALESCE(pce.patch_key, '')=COALESCE(dpi.patch_key, '')
		   AND COALESCE(pce.kb, '')=COALESCE(dpi.kb, '')
		 WHERE LOWER(TRIM(COALESCE(dpi.state, '')))='pending'
		   AND LOWER(dpi.device_guid) IN (`+strings.Join(placeholders, ",")+`)
	  ORDER BY LOWER(COALESCE(dpi.kb, '')), LOWER(dpi.title), LOWER(d.hostname)
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []patchInventoryRow{}
	for rows.Next() {
		row, err := scanPatchInventoryRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func ensurePatchPolicyRun(ctx context.Context, conn *sql.Conn, policyID int64, scheduledTS int64, now int64) (int64, bool, error) {
	var existing int64
	err := conn.QueryRowContext(ctx, `
		SELECT COALESCE(MIN(id), 0)
		  FROM engine.patch_policy_runs
		 WHERE policy_id=$1 AND scheduled_ts=$2
	`, policyID, scheduledTS).Scan(&existing)
	if err != nil {
		return 0, false, err
	}
	if existing > 0 {
		return existing, false, nil
	}
	var id int64
	err = conn.QueryRowContext(ctx, `
		INSERT INTO engine.patch_policy_runs(policy_id, scheduled_ts, started_at, status, created_at)
		VALUES ($1,$2,$3,'running',$3)
		RETURNING id
	`, policyID, scheduledTS, now).Scan(&id)
	return id, true, err
}

func finishPatchPolicyRun(ctx context.Context, conn *sql.Conn, runID int64, status string, summary map[string]any, now int64) error {
	payload, _ := json.Marshal(summary)
	_, err := conn.ExecContext(ctx, `
		UPDATE engine.patch_policy_runs
		   SET status=$1, finished_at=$2, summary_json=$3
		 WHERE id=$4
	`, status, now, string(payload), runID)
	return err
}

func insertPatchPolicyScheduledJob(ctx context.Context, conn *sql.Conn, row patchPolicyRow, runID int64, scheduledTS int64, now int64, component map[string]any, targets []any) (int64, error) {
	componentsJSON, targetsJSON, err := scheduledJobMutationJSON([]any{component}, targets)
	if err != nil {
		return 0, err
	}
	name := strings.TrimSpace("Policy: " + nullString(row.Name) + " - " + scheduledPatchInstallDisplayName(component))
	var jobID int64
	err = conn.QueryRowContext(ctx, `
		INSERT INTO engine.scheduled_jobs(
			name, components_json, targets_json, schedule_type, start_ts,
			duration_stop_enabled, expiration, execution_context, credential_id,
			use_service_account, job_kind, enabled, created_at, updated_at
		) VALUES ($1,$2,$3,'immediately',$4,0,'no_expire','system',NULL,0,$5,1,$6,$6)
		RETURNING id
	`, name, componentsJSON, targetsJSON, scheduledTS, scheduledJobKindPatchInstall, now).Scan(&jobID)
	return jobID, err
}

func patchPolicyDue(ctx context.Context, conn *sql.Conn, row patchPolicyRow, nowMinute int64) bool {
	policyID := nullInt(row.ID)
	if policyID <= 0 {
		return false
	}
	var latest sql.NullInt64
	_ = conn.QueryRowContext(ctx, "SELECT MAX(scheduled_ts) FROM engine.patch_policy_runs WHERE policy_id=$1", policyID).Scan(&latest)
	occurrence := schedulerComputeNextRun(
		normalizePatchPolicyScheduleType(row.InstallScheduleType.String),
		schedulerNullInt64Ptr(row.InstallStartTS),
		schedulerNullInt64Ptr(latest),
		nowMinute,
	)
	return occurrence != nil && *occurrence <= nowMinute
}

func patchPolicyDecision(rules []patchPolicyRule, patch map[string]any) string {
	blocked := false
	approved := false
	overrideApproved := false
	for _, rule := range rules {
		if !patchPolicyRuleMatches(rule, patch) {
			continue
		}
		if rule.RuleType == patchPolicyRuleBlock {
			blocked = true
		}
		if rule.RuleType == patchPolicyRuleApprove {
			approved = true
			if rule.OverrideParentBlock {
				overrideApproved = true
			}
		}
	}
	if overrideApproved {
		return patchPolicyRuleApprove
	}
	if blocked {
		return patchPolicyRuleBlock
	}
	if approved {
		return patchPolicyRuleApprove
	}
	return ""
}

func patchPolicyDeferralSatisfied(row patchInventoryRow, deferralDays int64, now int64) bool {
	if deferralDays <= 0 {
		return true
	}
	basis := nullInt(row.PublishedAt)
	if basis <= 0 {
		basis = nullInt(row.CapturedAt)
	}
	if basis <= 0 {
		return false
	}
	return basis+(deferralDays*86400) <= now
}

func patchPolicyRuleMatches(rule patchPolicyRule, patch map[string]any) bool {
	target := strings.ToLower(cleanText(rule.MatchValue))
	if target == "" {
		return false
	}
	switch rule.MatchType {
	case "severity":
		return strings.EqualFold(cleanText(patch["severity"]), rule.MatchValue)
	case "classification", "category":
		return strings.EqualFold(firstText(cleanText(patch["classification"]), cleanText(patch["category"])), rule.MatchValue)
	case "kb":
		return strings.EqualFold(normalizePatchKB(patch["kb"]), normalizePatchKB(rule.MatchValue)) || strings.Contains(strings.ToLower(cleanText(patch["title"])), strings.ToLower(normalizePatchKB(rule.MatchValue)))
	case "update_id":
		metadata := schedulerAnyMap(patch["metadata"])
		return strings.EqualFold(cleanText(firstPresentAny(patch["update_id"], metadata["update_id"], metadata["updateID"])), rule.MatchValue)
	case "patch_key":
		return strings.EqualFold(cleanText(patch["patch_key"]), rule.MatchValue)
	default:
		return false
	}
}

func patchPolicyRuleKey(matchType string, matchValue string) string {
	return normalizePatchPolicyMatchType(matchType) + ":" + strings.ToLower(strings.TrimSpace(matchValue))
}

func patchPolicyMarkOverrideRules(rules []patchPolicyRule, warnings []map[string]any) []patchPolicyRule {
	warnKeys := map[string]bool{}
	for _, warning := range warnings {
		warnKeys[patchPolicyRuleKey(cleanText(warning["match_type"]), cleanText(warning["match_value"]))] = true
	}
	out := append([]patchPolicyRule(nil), rules...)
	for idx := range out {
		if out[idx].RuleType == patchPolicyRuleApprove && warnKeys[patchPolicyRuleKey(out[idx].MatchType, out[idx].MatchValue)] {
			out[idx].OverrideParentBlock = true
		}
	}
	return out
}

func patchPolicyPrimaryIdentityKey(patch map[string]any) string {
	keys := patchActiveIdentityKeys(patch)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

func patchPolicyCoveredCounts(ctx context.Context, s *postgresOperatorStore, profile operatorProfile, row patchPolicyRow) map[string]int {
	resolution, err := s.resolvePatchPolicyDeviceResolution(ctx, profile, row, true)
	if err != nil {
		return map[string]int{"raw": 0, "eligible": 0, "ignored_role": 0}
	}
	return patchPolicyResolutionCounts(resolution)
}

func patchPolicyCoverageListSummary(ctx context.Context, s *postgresOperatorStore, profile operatorProfile, row patchPolicyRow) (map[string]int, []map[string]any) {
	resolution, err := s.resolvePatchPolicyDeviceResolution(ctx, profile, row, true)
	if err != nil {
		return map[string]int{"raw": 0, "eligible": 0, "ignored_role": 0}, patchPolicyTargetSitesForRow(row, patchPolicyDeviceResolution{})
	}
	return patchPolicyResolutionCounts(resolution), patchPolicyTargetSitesForRow(row, resolution)
}

func patchPolicyResolutionCounts(resolution patchPolicyDeviceResolution) map[string]int {
	ignored := len(resolution.Raw) - len(resolution.Eligible)
	if ignored < 0 {
		ignored = 0
	}
	return map[string]int{"raw": len(resolution.Raw), "eligible": len(resolution.Eligible), "ignored_role": ignored}
}

func patchPolicyTargetSitesForRow(row patchPolicyRow, resolution patchPolicyDeviceResolution) []map[string]any {
	switch normalizePatchPolicyType(row.PolicyType.String) {
	case patchPolicyTypeGlobal:
		return []map[string]any{{"id": int64(0), "site_id": int64(0), "name": "All Sites", "scope": "all"}}
	case patchPolicyTypeSite:
		return patchPolicyConfiguredTargetSites(row.Sites)
	case patchPolicyTypeDeviceFilter:
		return patchPolicyDeviceTargetSites(resolution.Eligible)
	default:
		return []map[string]any{}
	}
}

func patchPolicyConfiguredTargetSites(sites []map[string]any) []map[string]any {
	out := []map[string]any{}
	for _, site := range sites {
		siteID := coerceInt64(firstPresentAny(site["site_id"], site["id"]))
		name := cleanText(site["name"])
		if siteID <= 0 && name == "" {
			continue
		}
		out = append(out, map[string]any{"id": siteID, "site_id": siteID, "name": firstText(name, "Site "+strconv.FormatInt(siteID, 10))})
	}
	return patchPolicyNormalizeTargetSites(out)
}

func patchPolicyDeviceTargetSites(devices []patchPolicyDevice) []map[string]any {
	out := []map[string]any{}
	for _, device := range devices {
		siteID := device.SiteID
		name := cleanText(device.SiteName)
		if siteID <= 0 && name == "" {
			name = "Unassigned"
		}
		out = append(out, map[string]any{"id": siteID, "site_id": siteID, "name": firstText(name, "Site "+strconv.FormatInt(siteID, 10))})
	}
	return patchPolicyNormalizeTargetSites(out)
}

func patchPolicyNormalizeTargetSites(sites []map[string]any) []map[string]any {
	seen := map[string]map[string]any{}
	keys := []string{}
	for _, site := range sites {
		siteID := coerceInt64(firstPresentAny(site["site_id"], site["id"]))
		name := cleanText(site["name"])
		key := ""
		if siteID > 0 {
			key = "id:" + strconv.FormatInt(siteID, 10)
		} else if name != "" {
			key = "name:" + strings.ToLower(name)
		}
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = map[string]any{"id": siteID, "site_id": siteID, "name": name}
		keys = append(keys, key)
	}
	sort.SliceStable(keys, func(i, j int) bool {
		left := seen[keys[i]]
		right := seen[keys[j]]
		leftName := strings.ToLower(cleanText(left["name"]))
		rightName := strings.ToLower(cleanText(right["name"]))
		if leftName != rightName {
			return leftName < rightName
		}
		return coerceInt64(left["site_id"]) < coerceInt64(right["site_id"])
	})
	out := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		out = append(out, seen[key])
	}
	return out
}

func patchPolicyTargetSiteIDs(sites []map[string]any) []int64 {
	out := []int64{}
	for _, site := range sites {
		if id := coerceInt64(firstPresentAny(site["site_id"], site["id"])); id > 0 {
			out = append(out, id)
		}
	}
	return uniquePositiveInt64s(out)
}

func patchPolicyTargetSiteNames(sites []map[string]any) []string {
	names := []string{}
	seen := map[string]struct{}{}
	for _, site := range sites {
		name := cleanText(site["name"])
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		names = append(names, name)
	}
	sort.SliceStable(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})
	return names
}

func patchPolicyRoleMatchLabel(counts map[string]int) string {
	return strconv.Itoa(counts["eligible"]) + " / " + strconv.Itoa(counts["raw"]) + " Devices Match Policy Type"
}

func patchPolicyVisible(row patchPolicyRow, allowedSiteIDs []int64) bool {
	if allowedSiteIDs == nil {
		return true
	}
	if normalizePatchPolicyType(row.PolicyType.String) == patchPolicyTypeGlobal {
		return true
	}
	allowed := int64Set(allowedSiteIDs)
	for _, siteID := range patchPolicySiteIDs(row.Sites) {
		if _, ok := allowed[siteID]; ok {
			return true
		}
	}
	return normalizePatchPolicyType(row.PolicyType.String) == patchPolicyTypeDeviceFilter
}

func patchPolicyDepth(row patchPolicyRow) int {
	switch normalizePatchPolicyType(row.PolicyType.String) {
	case patchPolicyTypeDeviceFilter:
		return 3
	case patchPolicyTypeSite:
		return 2
	case patchPolicyTypeGlobal:
		return 1
	default:
		return 0
	}
}

func patchPolicySiteIDs(sites []map[string]any) []int64 {
	out := []int64{}
	for _, site := range sites {
		if id := coerceInt64(firstPresentAny(site["site_id"], site["id"])); id > 0 {
			out = append(out, id)
		}
	}
	return uniquePositiveInt64s(out)
}

func patchPolicySiteIDsFromAny(value any) []int64 {
	switch typed := value.(type) {
	case []int64:
		return uniquePositiveInt64s(typed)
	case []any:
		out := []int64{}
		for _, item := range typed {
			if id := coerceInt64(firstPresentAny(item, nestedAnyMap(item, "site_id"), nestedAnyMap(item, "id"))); id > 0 {
				out = append(out, id)
			}
		}
		return uniquePositiveInt64s(out)
	case []map[string]any:
		return patchPolicySiteIDs(typed)
	default:
		if id := coerceInt64(value); id > 0 {
			return []int64{id}
		}
	}
	return nil
}

func patchPolicyTargetsFromAny(value any) []patchPolicyTargetRef {
	items := anySliceFromAny(value)
	out := []patchPolicyTargetRef{}
	for _, item := range items {
		entry := schedulerAnyMap(item)
		targetType := normalizePatchPolicyTargetType(firstPresentAny(entry["target_type"], entry["kind"], entry["type"]))
		filterID := coerceInt64(firstPresentAny(entry["filter_id"], entry["id"]))
		hostname := cleanText(entry["hostname"])
		guid := strings.ToLower(normalizeCanonicalGUID(firstPresentAny(entry["device_guid"], entry["guid"])))
		if targetType == "" {
			if filterID > 0 {
				targetType = "filter"
			} else if hostname != "" || guid != "" {
				targetType = "device"
			}
		}
		if targetType == "" {
			continue
		}
		targetJSON := ""
		if raw, ok := entry["target"]; ok {
			if encoded, err := json.Marshal(raw); err == nil {
				targetJSON = string(encoded)
			}
		}
		out = append(out, patchPolicyTargetRef{TargetType: targetType, DeviceGUID: guid, Hostname: hostname, FilterID: filterID, TargetJSON: targetJSON})
	}
	return out
}

func patchPolicyExclusionsFromAny(value any, actor string) []patchPolicyExclusionRef {
	items := anySliceFromAny(value)
	out := []patchPolicyExclusionRef{}
	for _, item := range items {
		entry := schedulerAnyMap(item)
		exclusionType := normalizePatchPolicyExclusionType(firstPresentAny(entry["exclusion_type"], entry["mode"], entry["type"]))
		targetType := normalizePatchPolicyTargetType(firstPresentAny(entry["target_type"], entry["target_kind"], entry["kind"]))
		filterID := coerceInt64(firstPresentAny(entry["filter_id"], entry["id"]))
		hostname := cleanText(entry["hostname"])
		guid := strings.ToLower(normalizeCanonicalGUID(firstPresentAny(entry["device_guid"], entry["guid"])))
		siteID := coerceInt64(firstPresentAny(entry["site_id"], entry["siteId"]))
		siteName := cleanText(firstPresentAny(entry["site_name"], entry["siteName"], entry["site"]))
		if targetType == "" {
			if filterID > 0 {
				targetType = "filter"
			} else if hostname != "" || guid != "" {
				targetType = "device"
			}
		}
		if exclusionType == "" || targetType == "" {
			continue
		}
		if targetType == "filter" {
			siteID = 0
			siteName = ""
		}
		out = append(out, patchPolicyExclusionRef{ExclusionType: exclusionType, TargetType: targetType, DeviceGUID: guid, Hostname: hostname, SiteID: siteID, SiteName: siteName, FilterID: filterID, Reason: cleanText(entry["reason"]), CreatedBy: actor})
	}
	return out
}

func patchPolicyExclusionValidationError(exclusions []patchPolicyExclusionRef) string {
	for _, exclusion := range exclusions {
		if exclusion.TargetType == "device" && exclusion.DeviceGUID == "" && exclusion.Hostname != "" && exclusion.SiteID <= 0 {
			return "Device hostname exclusions require a site."
		}
	}
	return ""
}

func patchPolicyRulesFromAny(value any, actor string, now int64) []patchPolicyRule {
	items := anySliceFromAny(value)
	out := []patchPolicyRule{}
	for _, item := range items {
		entry := schedulerAnyMap(item)
		ruleType := normalizePatchPolicyRuleType(firstPresentAny(entry["rule_type"], entry["action"], entry["type"]))
		matchType := normalizePatchPolicyMatchType(firstPresentAny(entry["match_type"], entry["field"]))
		matchValue := cleanText(firstPresentAny(entry["match_value"], entry["value"]))
		if ruleType == "" || matchType == "" || matchValue == "" {
			continue
		}
		out = append(out, patchPolicyRule{
			ID:                  coerceInt64(entry["id"]),
			RuleType:            ruleType,
			MatchType:           matchType,
			MatchValue:          matchValue,
			OverrideParentBlock: boolFromAny(entry["override_parent_block"]),
			Notes:               cleanText(entry["notes"]),
			CreatedBy:           firstText(cleanText(entry["created_by"]), actor),
			CreatedAt:           firstPositiveInt64(coerceInt64(entry["created_at"]), now),
		})
	}
	return out
}

func patchPolicyTargetPayload(target patchPolicyTargetRef) map[string]any {
	return map[string]any{"target_type": target.TargetType, "device_guid": target.DeviceGUID, "hostname": target.Hostname, "filter_id": nullablePositiveInt64Any(target.FilterID), "target": parseJSON(sql.NullString{String: target.TargetJSON, Valid: target.TargetJSON != ""})}
}

func patchPolicyExclusionPayload(exclusion patchPolicyExclusionRef) map[string]any {
	return map[string]any{"exclusion_type": exclusion.ExclusionType, "target_type": exclusion.TargetType, "device_guid": exclusion.DeviceGUID, "hostname": exclusion.Hostname, "site_id": nullablePositiveInt64Any(exclusion.SiteID), "site_name": exclusion.SiteName, "filter_id": nullablePositiveInt64Any(exclusion.FilterID), "reason": exclusion.Reason, "created_by": exclusion.CreatedBy}
}

func patchPolicyCoverageKeys(targets []patchPolicyTargetRef, exclusions []patchPolicyExclusionRef) map[string]bool {
	keys := map[string]bool{}
	for _, target := range targets {
		if key := patchPolicyTargetKey(target.TargetType, target.DeviceGUID, target.Hostname, target.FilterID); key != "" {
			keys[key] = true
		}
	}
	for _, exclusion := range exclusions {
		if key := patchPolicyTargetKeyWithSite(exclusion.TargetType, exclusion.DeviceGUID, exclusion.Hostname, exclusion.FilterID, exclusion.SiteID); key != "" {
			keys[key] = true
		}
	}
	return keys
}

func patchPolicyScheduledTarget(entry map[string]any) map[string]any {
	target := schedulerAnyMap(entry["target"])
	targetType := normalizePatchPolicyTargetType(firstPresentAny(entry["target_type"], entry["kind"], entry["type"], target["target_type"], target["kind"], target["type"]))
	filterID := firstPresentAny(entry["filter_id"], entry["id"], target["filter_id"], target["id"])
	if targetType == "filter" || coerceInt64(filterID) > 0 {
		return map[string]any{"kind": "filter", "filter_id": filterID, "name": firstPresentAny(entry["name"], target["name"])}
	}
	return map[string]any{
		"kind":        "device",
		"hostname":    firstPresentAny(entry["hostname"], target["hostname"]),
		"device_guid": firstPresentAny(entry["device_guid"], entry["guid"], target["device_guid"], target["guid"]),
		"site_id":     firstPresentAny(entry["site_id"], entry["siteId"], target["site_id"], target["siteId"]),
		"site_name":   firstPresentAny(entry["site_name"], entry["siteName"], entry["site"], target["site_name"], target["siteName"], target["site"]),
	}
}

func patchPolicyTargetIdentityFromAny(value any) string {
	entry := schedulerAnyMap(value)
	return patchPolicyTargetKeyWithSite(cleanText(entry["kind"]), cleanText(firstPresentAny(entry["device_guid"], entry["guid"])), cleanText(entry["hostname"]), coerceInt64(firstPresentAny(entry["filter_id"], entry["id"])), coerceInt64(firstPresentAny(entry["site_id"], entry["siteId"])))
}

func patchPolicyTargetKey(targetType string, guid string, hostname string, filterID int64) string {
	return patchPolicyTargetKeyWithSite(targetType, guid, hostname, filterID, 0)
}

func patchPolicyTargetKeyWithSite(targetType string, guid string, hostname string, filterID int64, siteID int64) string {
	targetType = normalizePatchPolicyTargetType(targetType)
	switch targetType {
	case "filter":
		if filterID > 0 {
			return "filter:" + strconv.FormatInt(filterID, 10)
		}
	case "device":
		if guid = strings.ToLower(normalizeCanonicalGUID(guid)); guid != "" {
			return "device-guid:" + guid
		}
		if hostname = strings.ToLower(strings.TrimSpace(hostname)); hostname != "" {
			if siteID > 0 {
				return "device-host:" + strconv.FormatInt(siteID, 10) + ":" + hostname
			}
			return "device-host:*:" + hostname
		}
	}
	return ""
}

func patchPolicyTargetOverlapsKeys(keys map[string]bool, targetType string, guid string, hostname string, filterID int64, siteID int64) (string, bool) {
	key := patchPolicyTargetKeyWithSite(targetType, guid, hostname, filterID, siteID)
	if key != "" && keys[key] {
		return key, true
	}
	if normalizePatchPolicyTargetType(targetType) != "device" || strings.ToLower(normalizeCanonicalGUID(guid)) != "" {
		return "", false
	}
	host := strings.ToLower(strings.TrimSpace(hostname))
	if host == "" {
		return "", false
	}
	globalKey := patchPolicyTargetKeyWithSite("device", "", host, 0, 0)
	if siteID > 0 {
		if keys[globalKey] {
			return globalKey, true
		}
		return "", false
	}
	suffix := ":" + host
	for candidate := range keys {
		if candidate != globalKey && strings.HasPrefix(candidate, "device-host:") && strings.HasSuffix(candidate, suffix) {
			return candidate, true
		}
	}
	return "", false
}

func patchPolicyDeviceIdentity(device patchPolicyDevice) string {
	if guid := strings.ToLower(normalizeCanonicalGUID(device.DeviceGUID)); guid != "" {
		return "guid:" + guid
	}
	host := strings.ToLower(strings.TrimSpace(device.Hostname))
	if host == "" {
		return ""
	}
	if device.SiteID > 0 {
		return "site:" + strconv.FormatInt(device.SiteID, 10) + ":" + host
	}
	return "host:" + host
}

func patchPolicyDevicePayloads(devices []patchPolicyDevice) []map[string]any {
	out := []map[string]any{}
	for _, device := range devices {
		out = append(out, map[string]any{"device_guid": device.DeviceGUID, "hostname": device.Hostname, "site_id": device.SiteID, "site_name": device.SiteName, "device_type": device.DeviceType, "operating_system": device.OperatingSystem, "filter_ids": device.FilterIDs, "exclusion_mode": device.ExclusionMode, "conflicted": device.Conflict})
	}
	return out
}

func patchPolicyRoleScopesOverlap(a string, b string) bool {
	left := patchPolicyRoleSet(normalizePatchPolicyRoleScope(a))
	right := patchPolicyRoleSet(normalizePatchPolicyRoleScope(b))
	for role := range left {
		if right[role] {
			return true
		}
	}
	return false
}

func patchPolicyRoleSet(scope string) map[string]bool {
	switch normalizePatchPolicyRoleScope(scope) {
	case patchPolicyRoleServer:
		return map[string]bool{patchPolicyRoleServer: true}
	case patchPolicyRoleWorkstation:
		return map[string]bool{patchPolicyRoleWorkstation: true}
	default:
		return map[string]bool{patchPolicyRoleServer: true, patchPolicyRoleWorkstation: true}
	}
}

func patchPolicyRoleMatches(scope string, deviceType string) bool {
	scope = normalizePatchPolicyRoleScope(scope)
	return patchPolicyValidRoleDomain(scope) && scope == patchPolicyDeviceRole(deviceType)
}

func patchPolicyDeviceRole(deviceType string) string {
	deviceType = strings.ToLower(strings.TrimSpace(deviceType))
	if deviceType == "" {
		return ""
	}
	if strings.Contains(deviceType, "server") {
		return patchPolicyRoleServer
	}
	return patchPolicyRoleWorkstation
}

func patchPolicyValidRoleDomain(scope string) bool {
	scope = normalizePatchPolicyRoleScope(scope)
	return scope == patchPolicyRoleServer || scope == patchPolicyRoleWorkstation
}

func normalizePatchPolicyType(value any) string {
	switch strings.ToLower(strings.TrimSpace(cleanText(value))) {
	case "global":
		return patchPolicyTypeGlobal
	case "site", "site_policy":
		return patchPolicyTypeSite
	case "device_filter", "device", "filter", "device_filter_policy":
		return patchPolicyTypeDeviceFilter
	default:
		return ""
	}
}

func normalizePatchPolicyRoleScope(value any) string {
	switch strings.ToLower(strings.TrimSpace(cleanText(value))) {
	case "server", "servers":
		return patchPolicyRoleServer
	case "workstation", "workstations", "desktop", "desktops":
		return patchPolicyRoleWorkstation
	default:
		return patchPolicyRoleBoth
	}
}

func normalizePatchPolicyScheduleType(value any) string {
	switch strings.ToLower(strings.TrimSpace(cleanText(value))) {
	case "immediately", "once", "daily", "weekly", "monthly", "yearly":
		return strings.ToLower(strings.TrimSpace(cleanText(value)))
	default:
		return "weekly"
	}
}

func normalizePatchPolicyRuleType(value any) string {
	switch strings.ToLower(strings.TrimSpace(cleanText(value))) {
	case "approve", "allow", "approved":
		return patchPolicyRuleApprove
	case "block", "deny", "blacklist", "blocked":
		return patchPolicyRuleBlock
	default:
		return ""
	}
}

func normalizePatchPolicyMatchType(value any) string {
	switch strings.ToLower(strings.TrimSpace(cleanText(value))) {
	case "severity":
		return "severity"
	case "classification", "class":
		return "classification"
	case "category":
		return "category"
	case "kb", "kb_article", "kb_article_id":
		return "kb"
	case "update_id", "updateid":
		return "update_id"
	case "patch_key", "identity":
		return "patch_key"
	default:
		return ""
	}
}

func normalizePatchPolicyExclusionType(value any) string {
	switch strings.ToLower(strings.TrimSpace(cleanText(value))) {
	case "unmanaged":
		return patchPolicyExclusionUnmanaged
	case "frozen", "freeze":
		return patchPolicyExclusionFrozen
	case "managed_override", "override", "clear", "clear_inherited":
		return patchPolicyExclusionOverride
	default:
		return ""
	}
}

func normalizePatchPolicyTargetType(value any) string {
	switch strings.ToLower(strings.TrimSpace(cleanText(value))) {
	case "device", "host", "hostname":
		return "device"
	case "filter", "device_filter":
		return "filter"
	default:
		return ""
	}
}

func patchPolicyWindowsOS(value string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(value)), "windows")
}

func insertPatchPolicyAuditTx(ctx context.Context, tx *sql.Tx, policyID int64, action string, actor string, detail map[string]any, now int64) error {
	payload, _ := json.Marshal(detail)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO engine.patch_policy_audit(policy_id, action, actor, detail_json, created_at)
		VALUES ($1,$2,$3,$4,$5)
	`, policyID, action, actor, string(payload), now)
	return err
}

func policyIDValid(policyID *int64) bool {
	return policyID != nil && *policyID > 0
}

func nullablePolicyID(policyID *int64) int64 {
	if policyID == nil {
		return 0
	}
	return *policyID
}

func nullableInt64Arg(value *int64) any {
	if value == nil || *value <= 0 {
		return nil
	}
	return *value
}

func nullablePositiveInt64Arg(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}

func nullablePositiveInt64Any(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}

func optionalPositiveInt64Ptr(value any) *int64 {
	if value == nil {
		return nil
	}
	if v := coerceInt64(value); v > 0 {
		return &v
	}
	return nil
}

func nullEmpty(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func derefInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func nestedAny(value map[string]any, keys ...string) any {
	var current any = value
	for _, key := range keys {
		entry := schedulerAnyMap(current)
		if entry == nil {
			return nil
		}
		current = entry[key]
	}
	return current
}

func nestedAnyMap(value any, key string) any {
	entry := schedulerAnyMap(value)
	if entry == nil {
		return nil
	}
	return entry[key]
}

var _ patchPolicyStore = (*postgresOperatorStore)(nil)
