package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *postgresOperatorStore) ensurePatchPolicySchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errOperatorStoreDown
	}
	s.patchPolicySchemaMu.Lock()
	defer s.patchPolicySchemaMu.Unlock()
	if s.patchPolicySchemaOK {
		return nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	if err := ensurePatchPolicySchemaOnConn(ctx, conn); err != nil {
		return err
	}
	s.patchPolicySchemaOK = true
	return nil
}

func ensurePatchPolicySchemaOnConn(ctx context.Context, conn *sql.Conn) error {
	now := time.Now()
	nowTS := now.Unix()
	workstationInstallStart := patchPolicyDefaultStartTS(now, time.Tuesday, 2)
	serverInstallStart := patchPolicyDefaultStartTS(now, time.Wednesday, 2)
	rebootStart := patchPolicyDefaultStartTS(now, time.Saturday, 1)
	statements := []string{
		`CREATE TABLE IF NOT EXISTS engine.patch_catalog_entries (
			id BIGSERIAL PRIMARY KEY,
			patch_key TEXT,
			kb TEXT,
			update_id TEXT,
			revision_number BIGINT,
			title TEXT NOT NULL,
			classification TEXT,
			category TEXT,
			severity TEXT,
			published_at BIGINT,
			first_seen_at BIGINT NOT NULL,
			last_seen_at BIGINT NOT NULL,
			metadata_json TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_patch_catalog_identity ON engine.patch_catalog_entries(patch_key, kb, update_id)`,
		`CREATE INDEX IF NOT EXISTS idx_patch_catalog_kb ON engine.patch_catalog_entries(kb)`,
		`CREATE TABLE IF NOT EXISTS engine.patch_policies (
			id BIGSERIAL PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			description TEXT,
			policy_type TEXT NOT NULL DEFAULT 'site',
			enabled BIGINT NOT NULL DEFAULT 1,
			locked BIGINT NOT NULL DEFAULT 0,
			role_scope TEXT NOT NULL DEFAULT 'Both',
			approval_mode TEXT NOT NULL DEFAULT 'conservative_msp',
			deferral_days BIGINT NOT NULL DEFAULT 14,
			managed_update_mode BIGINT NOT NULL DEFAULT 1,
			class_toggles_json TEXT NOT NULL DEFAULT '{}',
			install_schedule_type TEXT NOT NULL DEFAULT 'weekly',
			install_start_ts BIGINT,
			reboot_after_install BIGINT NOT NULL DEFAULT 0,
			reboot_policy_json TEXT NOT NULL DEFAULT '{}',
			reboot_schedule_enabled BIGINT NOT NULL DEFAULT 0,
			reboot_schedule_type TEXT NOT NULL DEFAULT 'weekly',
			reboot_start_ts BIGINT,
			force_reboot_logged_in BIGINT NOT NULL DEFAULT 0,
			created_by TEXT,
			updated_by TEXT,
			created_at BIGINT NOT NULL DEFAULT 0,
			updated_at BIGINT NOT NULL DEFAULT 0
		)`,
		`ALTER TABLE engine.patch_policies ADD COLUMN IF NOT EXISTS name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE engine.patch_policies ADD COLUMN IF NOT EXISTS description TEXT`,
		`ALTER TABLE engine.patch_policies ADD COLUMN IF NOT EXISTS policy_type TEXT NOT NULL DEFAULT 'site'`,
		`ALTER TABLE engine.patch_policies ADD COLUMN IF NOT EXISTS enabled BIGINT NOT NULL DEFAULT 1`,
		`ALTER TABLE engine.patch_policies ADD COLUMN IF NOT EXISTS locked BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE engine.patch_policies ADD COLUMN IF NOT EXISTS role_scope TEXT NOT NULL DEFAULT 'Both'`,
		`ALTER TABLE engine.patch_policies ADD COLUMN IF NOT EXISTS approval_mode TEXT NOT NULL DEFAULT 'conservative_msp'`,
		`ALTER TABLE engine.patch_policies ADD COLUMN IF NOT EXISTS deferral_days BIGINT NOT NULL DEFAULT 14`,
		`ALTER TABLE engine.patch_policies ADD COLUMN IF NOT EXISTS managed_update_mode BIGINT NOT NULL DEFAULT 1`,
		`ALTER TABLE engine.patch_policies ADD COLUMN IF NOT EXISTS class_toggles_json TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE engine.patch_policies ADD COLUMN IF NOT EXISTS install_schedule_type TEXT NOT NULL DEFAULT 'weekly'`,
		`ALTER TABLE engine.patch_policies ADD COLUMN IF NOT EXISTS install_start_ts BIGINT`,
		`ALTER TABLE engine.patch_policies ADD COLUMN IF NOT EXISTS reboot_after_install BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE engine.patch_policies ADD COLUMN IF NOT EXISTS reboot_policy_json TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE engine.patch_policies ADD COLUMN IF NOT EXISTS reboot_schedule_enabled BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE engine.patch_policies ADD COLUMN IF NOT EXISTS reboot_schedule_type TEXT NOT NULL DEFAULT 'weekly'`,
		`ALTER TABLE engine.patch_policies ADD COLUMN IF NOT EXISTS reboot_start_ts BIGINT`,
		`ALTER TABLE engine.patch_policies ADD COLUMN IF NOT EXISTS force_reboot_logged_in BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE engine.patch_policies ADD COLUMN IF NOT EXISTS created_by TEXT`,
		`ALTER TABLE engine.patch_policies ADD COLUMN IF NOT EXISTS updated_by TEXT`,
		`ALTER TABLE engine.patch_policies ADD COLUMN IF NOT EXISTS created_at BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE engine.patch_policies ADD COLUMN IF NOT EXISTS updated_at BIGINT NOT NULL DEFAULT 0`,
		`UPDATE engine.patch_policies
		    SET policy_type='global', locked=1, role_scope='Both'
		  WHERE LOWER(TRIM(name))='global patch policy'
		    AND policy_type<>'global'`,
		`CREATE INDEX IF NOT EXISTS idx_patch_policies_type_enabled ON engine.patch_policies(policy_type, enabled)`,
		`CREATE TABLE IF NOT EXISTS engine.patch_policy_sites (
			id BIGSERIAL PRIMARY KEY,
			policy_id BIGINT NOT NULL REFERENCES engine.patch_policies(id) ON DELETE CASCADE,
			site_id BIGINT NOT NULL REFERENCES engine.sites(id) ON DELETE CASCADE,
			created_at BIGINT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_patch_policy_sites_site ON engine.patch_policy_sites(site_id, policy_id)`,
		`CREATE TABLE IF NOT EXISTS engine.patch_policy_targets (
			id BIGSERIAL PRIMARY KEY,
			policy_id BIGINT NOT NULL REFERENCES engine.patch_policies(id) ON DELETE CASCADE,
			target_type TEXT NOT NULL,
			device_guid TEXT,
			hostname TEXT,
			filter_id BIGINT REFERENCES engine.device_filters(id) ON DELETE SET NULL,
			target_json TEXT,
			created_at BIGINT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_patch_policy_targets_policy ON engine.patch_policy_targets(policy_id, target_type)`,
		`CREATE TABLE IF NOT EXISTS engine.patch_policy_exclusions (
			id BIGSERIAL PRIMARY KEY,
			policy_id BIGINT NOT NULL REFERENCES engine.patch_policies(id) ON DELETE CASCADE,
			exclusion_type TEXT NOT NULL,
			target_type TEXT NOT NULL,
			device_guid TEXT,
			hostname TEXT,
			site_id BIGINT REFERENCES engine.sites(id) ON DELETE SET NULL,
			filter_id BIGINT REFERENCES engine.device_filters(id) ON DELETE SET NULL,
			reason TEXT,
			created_by TEXT,
			created_at BIGINT NOT NULL
		)`,
		`ALTER TABLE engine.patch_policy_exclusions ADD COLUMN IF NOT EXISTS site_id BIGINT REFERENCES engine.sites(id) ON DELETE SET NULL`,
		`CREATE INDEX IF NOT EXISTS idx_patch_policy_exclusions_policy ON engine.patch_policy_exclusions(policy_id, exclusion_type)`,
		`CREATE INDEX IF NOT EXISTS idx_patch_policy_exclusions_site_host ON engine.patch_policy_exclusions(site_id, hostname)`,
		`CREATE TABLE IF NOT EXISTS engine.patch_policy_rules (
			id BIGSERIAL PRIMARY KEY,
			policy_id BIGINT NOT NULL REFERENCES engine.patch_policies(id) ON DELETE CASCADE,
			rule_type TEXT NOT NULL,
			match_type TEXT NOT NULL,
			match_value TEXT NOT NULL,
			override_parent_block BIGINT NOT NULL DEFAULT 0,
			notes TEXT,
			created_by TEXT,
			created_at BIGINT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_patch_policy_rules_policy ON engine.patch_policy_rules(policy_id, rule_type, match_type)`,
		`CREATE TABLE IF NOT EXISTS engine.patch_policy_runs (
			id BIGSERIAL PRIMARY KEY,
			policy_id BIGINT NOT NULL REFERENCES engine.patch_policies(id) ON DELETE CASCADE,
			scheduled_ts BIGINT NOT NULL,
			started_at BIGINT NOT NULL,
			finished_at BIGINT,
			status TEXT NOT NULL,
			summary_json TEXT,
			created_at BIGINT NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_patch_policy_runs_policy_scheduled ON engine.patch_policy_runs(policy_id, scheduled_ts)`,
		`CREATE TABLE IF NOT EXISTS engine.patch_policy_device_state (
			id BIGSERIAL PRIMARY KEY,
			device_guid TEXT,
			hostname TEXT NOT NULL,
			effective_policy_id BIGINT REFERENCES engine.patch_policies(id) ON DELETE SET NULL,
			exclusion_mode TEXT,
			enforcement_mode TEXT,
			enforcement_status TEXT,
			drift_detected BIGINT NOT NULL DEFAULT 0,
			last_evaluated_at BIGINT NOT NULL,
			last_enforced_at BIGINT,
			metadata_json TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_patch_policy_device_state_host ON engine.patch_policy_device_state(hostname)`,
		`CREATE TABLE IF NOT EXISTS engine.patch_policy_audit (
			id BIGSERIAL PRIMARY KEY,
			policy_id BIGINT REFERENCES engine.patch_policies(id) ON DELETE SET NULL,
			action TEXT NOT NULL,
			actor TEXT,
			detail_json TEXT,
			created_at BIGINT NOT NULL
		)`,
	}
	for _, statement := range statements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if err := ensurePatchPolicyLegacyColumnDefaults(ctx, conn); err != nil {
		return err
	}
	if err := ensureSplitGlobalPatchPolicies(ctx, conn, workstationInstallStart, serverInstallStart, rebootStart, nowTS); err != nil {
		return err
	}
	_, err := conn.ExecContext(ctx, `
		INSERT INTO engine.patch_policy_rules(policy_id, rule_type, match_type, match_value, override_parent_block, notes, created_by, created_at)
		SELECT p.id, v.rule_type, v.match_type, v.match_value, 0, NULL, 'system', $1
		  FROM engine.patch_policies AS p
		 CROSS JOIN (
			VALUES
				('approve', 'severity', 'Critical'),
				('approve', 'severity', 'Important'),
				('approve', 'classification', 'Security Updates'),
				('approve', 'classification', 'Critical Updates'),
				('approve', 'title_contains', 'Security Intelligence Update'),
				('block', 'classification', 'Drivers'),
				('block', 'classification', 'Feature Packs'),
				('block', 'title_contains', 'Preview')
		 ) AS v(rule_type, match_type, match_value)
		 WHERE p.policy_type='global'
		   AND NOT EXISTS (SELECT 1 FROM engine.patch_policy_rules AS r WHERE r.policy_id=p.id)
	`, nowTS)
	if err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, `
		INSERT INTO engine.patch_policy_rules(policy_id, rule_type, match_type, match_value, override_parent_block, notes, created_by, created_at)
		SELECT p.id, v.rule_type, v.match_type, v.match_value, 0, NULL, 'system', $1
		  FROM engine.patch_policies AS p
		 CROSS JOIN (
			VALUES
				('approve', 'title_contains', 'Security Intelligence Update'),
				('block', 'title_contains', 'Preview')
		 ) AS v(rule_type, match_type, match_value)
		 WHERE p.policy_type='global'
		   AND NOT EXISTS (
				SELECT 1
				  FROM engine.patch_policy_rules AS r
				 WHERE r.policy_id=p.id
				   AND r.rule_type=v.rule_type
				   AND r.match_type=v.match_type
				   AND LOWER(TRIM(r.match_value))=LOWER(TRIM(v.match_value))
		   )
	`, nowTS)
	return err
}

func ensureSplitGlobalPatchPolicies(ctx context.Context, conn *sql.Conn, workstationInstallStart int64, serverInstallStart int64, rebootStart int64, nowTS int64) error {
	var splitGlobals int64
	if err := conn.QueryRowContext(ctx, `
		SELECT COUNT(*)
		  FROM engine.patch_policies
		 WHERE policy_type='global'
		   AND role_scope IN ('Server', 'Workstation')
	`).Scan(&splitGlobals); err != nil {
		return err
	}
	if splitGlobals == 0 {
		for _, statement := range []string{
			`DELETE FROM engine.patch_policy_device_state`,
			`DELETE FROM engine.patch_policy_audit`,
			`DELETE FROM engine.patch_policy_sites`,
			`DELETE FROM engine.patch_policy_targets`,
			`DELETE FROM engine.patch_policy_exclusions`,
			`DELETE FROM engine.patch_policy_rules`,
			`DELETE FROM engine.patch_policy_runs`,
			`DELETE FROM engine.patch_policies`,
		} {
			if _, err := conn.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
	} else {
		if _, err := conn.ExecContext(ctx, `
			DELETE FROM engine.patch_policies
			 WHERE policy_type='global'
			   AND role_scope NOT IN ('Server', 'Workstation')
		`); err != nil {
			return err
		}
	}
	for _, seed := range []struct {
		Name        string
		Description string
		Role        string
		StartTS     int64
	}{
		{
			Name:        "Global Workstation Policy",
			Description: "Default Borealis workstation patch policy baseline. Locked from deletion and preserved across redeploys.",
			Role:        "Workstation",
			StartTS:     workstationInstallStart,
		},
		{
			Name:        "Global Server Policy",
			Description: "Default Borealis server patch policy baseline. Locked from deletion and preserved across redeploys.",
			Role:        "Server",
			StartTS:     serverInstallStart,
		},
	} {
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO engine.patch_policies(
				name, description, policy_type, enabled, locked, role_scope, approval_mode,
				deferral_days, managed_update_mode, install_schedule_type, install_start_ts,
				reboot_after_install, reboot_schedule_enabled, reboot_schedule_type, reboot_start_ts,
				force_reboot_logged_in, created_by, updated_by, created_at, updated_at
			)
			SELECT $1, $2, 'global', 1, 1, $3, 'conservative_msp', 14, 1, 'weekly', $4,
			       0, 0, 'weekly', $5, 0, 'system', 'system', $6, $6
			 WHERE NOT EXISTS (
				SELECT 1
				  FROM engine.patch_policies
				 WHERE policy_type='global'
				   AND role_scope=$3
			 )
		`, seed.Name, seed.Description, seed.Role, seed.StartTS, rebootStart, nowTS); err != nil {
			return err
		}
	}
	_, err := conn.ExecContext(ctx, `
		UPDATE engine.patch_policies
		   SET locked=1
		 WHERE policy_type='global'
		   AND role_scope IN ('Server', 'Workstation')
	`)
	return err
}

func patchPolicyDefaultStartTS(now time.Time, weekday time.Weekday, hour int) int64 {
	localNow := now.Local()
	daysUntil := (int(weekday) - int(localNow.Weekday()) + 7) % 7
	candidate := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, 0, 0, 0, localNow.Location()).AddDate(0, 0, daysUntil)
	if !candidate.After(localNow) {
		candidate = candidate.AddDate(0, 0, 7)
	}
	return candidate.Unix()
}

func ensurePatchPolicyLegacyColumnDefaults(ctx context.Context, conn *sql.Conn) error {
	rows, err := conn.QueryContext(ctx, `
		SELECT column_name, data_type
		  FROM information_schema.columns
		 WHERE table_schema='engine'
		   AND table_name='patch_policies'
		   AND is_nullable='NO'
		   AND column_default IS NULL
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type legacyColumn struct {
		Name     string
		DataType string
	}
	columns := []legacyColumn{}
	for rows.Next() {
		var column legacyColumn
		if err := rows.Scan(&column.Name, &column.DataType); err != nil {
			return err
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, column := range columns {
		defaultExpr := patchPolicyLegacyDefaultExpression(column.Name, column.DataType)
		if defaultExpr == "" {
			continue
		}
		if _, err := conn.ExecContext(ctx, fmt.Sprintf(
			"ALTER TABLE engine.patch_policies ALTER COLUMN %s SET DEFAULT %s",
			quotePostgresIdentifier(column.Name),
			defaultExpr,
		)); err != nil {
			return err
		}
	}
	return nil
}

func patchPolicyLegacyDefaultExpression(columnName string, dataType string) string {
	name := strings.ToLower(strings.TrimSpace(columnName))
	dataType = strings.ToLower(strings.TrimSpace(dataType))
	switch {
	case name == "id":
		return ""
	case strings.Contains(dataType, "jsonb"):
		if strings.Contains(name, "list") || strings.Contains(name, "array") || strings.Contains(name, "rules") {
			return "'[]'::jsonb"
		}
		return "'{}'::jsonb"
	case dataType == "json":
		if strings.Contains(name, "list") || strings.Contains(name, "array") || strings.Contains(name, "rules") {
			return "'[]'::json"
		}
		return "'{}'::json"
	case strings.HasSuffix(name, "_json"):
		if strings.Contains(name, "list") || strings.Contains(name, "array") || strings.Contains(name, "rules") {
			return "'[]'"
		}
		return "'{}'"
	case strings.Contains(dataType, "int") || dataType == "numeric" || dataType == "double precision" || dataType == "real":
		return "0"
	case dataType == "boolean":
		return "false"
	case strings.Contains(dataType, "timestamp"):
		return "NOW()"
	default:
		return "''"
	}
}

func quotePostgresIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}
