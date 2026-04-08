# Database Reference
[Back to Docs Index](index.md) | [Index (HTML)](index.html)

## Purpose
Describe the Borealis PostgreSQL schema, table ownership, runtime interactions, and legacy migration structures so operators and Codex agents can troubleshoot and change schema safely.

## Scope
- Primary runtime database: PostgreSQL (set via `BOREALIS_DATABASE_URL`).
- Source-of-truth schema code:
- `Data/Engine/database.py`
- `Data/Engine/database_migrations.py`
- `Data/Engine/services/API/scheduled_jobs/job_scheduler.py`
- Assembly catalog tables live in PostgreSQL `assemblies.*`.
- Bundled official assembly snapshot lives in `Data/Engine/Official_Assemblies/`.
- Assembly schema source: `Data/Engine/assembly_management/databases.py`.

## API Endpoints
None. This page documents persistence structures used by many endpoints.

## Quick Relationship Map
```text
sites (id) --------------------< device_sites (site_id)
  |                                 |
  |                                 +---- device hostname mapping (logical to devices.hostname)
  |
  +------------------------< device_approvals (site_id, soft relation)
  |
  +------------------------< credentials (site_id, ON DELETE SET NULL)

devices (guid) ----------------< refresh_tokens (guid)
devices (guid) ----------------< device_keys (guid)
devices (guid) ----------------< device_approvals (guid, optional)

scheduled_jobs (id) ----------< scheduled_job_runs (job_id)
scheduled_job_runs (id) ------< scheduled_job_run_activity (run_id)
scheduled_job_runs (id) ------< scheduled_job_run_targets (run_id)
activity_history (id) --------< scheduled_job_run_activity (activity_id, unique)

users (id/username) ----------< device_approvals.approved_by_user_id (soft relation)
users (id) -------------------< user_site_assignments (user_id)
sites (id) -------------------< user_site_assignments (site_id)
```

## Important PostgreSQL Behavior
- Borealis uses PostgreSQL as the live Engine database, so engine troubleshooting should focus on server-side constraints, indexes, sequences, and transaction boundaries.
- Constraint enforcement, indexes, and transactions are handled server-side by PostgreSQL.
- Some Borealis relations remain intentionally soft in schema/API logic, so application code still performs explicit cleanup and validation for tables such as `device_sites` and approval mappings.

## Engine Runtime Database Tables (`engine.*`)
### Enrollment, Identity, and Site Mapping
#### `sites`
- Status: Active.
- Purpose: Site records and static enrollment codes.
- Columns: `id`, `name`, `description`, `created_at`, `enrollment_code`.
- Constraints and indexes:
- `id` primary key.
- `name` unique.
- `uq_sites_enrollment_code` unique index on `enrollment_code`.
- Used by:
- Enrollment request lookup (`/api/agent/enroll/request`) via `enrollment_code`.
- Site CRUD APIs (`/api/sites*`).
- Admin code listing (`GET /api/admin/enrollment-codes`).
- Device, filter, and scheduled-job joins through `device_sites`.
- Notes:
- Rebuild migration removes legacy `enrollment_code_id` if present.
- Current design stores site-code association directly in this table.

#### `device_approvals`
- Status: Active.
- Purpose: Enrollment approval queue and history.
- Columns: `id`, `approval_reference`, `guid`, `hostname_claimed`, `ssl_key_fingerprint_claimed`, `enrollment_code`, `site_id`, `status`, `client_nonce`, `server_nonce`, `agent_pubkey_der`, `created_at`, `updated_at`, `approved_by_user_id`.
- Constraints and indexes:
- `id` primary key.
- `approval_reference` unique.
- `idx_da_status` on `status`.
- `idx_da_fp_status` on `(ssl_key_fingerprint_claimed, status)`.
- `idx_da_site` on `site_id`.
- Used by:
- Enrollment request and poll endpoints.
- Admin approval APIs (`/api/admin/device-approvals*`).
- Notes:
- `status` lifecycle typically `pending -> approved|denied|expired -> completed`.
- `site_id` and `approved_by_user_id` are soft relations (not enforced FK in schema).
- Rebuild migration removes legacy `enrollment_code_id` if present.

#### `devices`
- Status: Active (core inventory and identity table).
- Purpose: Canonical device identity and inventory snapshot.
- Columns: `guid`, `hostname`, `description`, `created_at`, `last_enrollment_at`, `agent_hash`, `agent_role_health`, `memory`, `network`, `software`, `storage`, `cpu`, `device_type`, `domain`, `external_ip`, `internal_ip`, `last_reboot`, `last_seen`, `last_user`, `operating_system`, `uptime`, `agent_id`, `connection_type`, `connection_endpoint`, `agent_vnc_password`, `ssl_key_fingerprint`, `token_version`, `status`, `key_added_at`.
- Constraints and indexes:
- `guid` primary key.
- `uq_devices_hostname` unique on `hostname`.
- `idx_devices_ssl_key` on `ssl_key_fingerprint`.
- `idx_devices_status` on `status`.
- Used by:
- Device auth (`guid + fingerprint + token_version` validation).
- Enrollment finalize/upsert.
- Heartbeats and inventory detail updates.
- Per-role health snapshots for the Device Details `Agent Roles Health` panel.
- VNC/VPN agent routing (`agent_id`, `agent_vnc_password`).
- Scheduled-jobs online host snapshot (`last_seen`).
- Notes:
- Fingerprint change increments `token_version` and revokes active refresh tokens.
- `status` supports revocation states used by auth and token refresh checks.

#### `device_keys`
- Status: Active.
- Purpose: Device key-fingerprint history and retirement tracking.
- Columns: `id`, `guid`, `ssl_key_fingerprint`, `added_at`, `retired_at`.
- Constraints and indexes:
- `id` primary key.
- `uq_device_keys_guid_fingerprint` unique on `(guid, ssl_key_fingerprint)`.
- `idx_device_keys_guid` on `guid`.
- Used by:
- Enrollment finalize.
- Agent details updates.
- Notes:
- Old active key rows are marked with `retired_at` when key material changes.

#### `refresh_tokens`
- Status: Active.
- Purpose: Long-lived refresh token state.
- Columns: `id`, `guid`, `token_hash`, `dpop_jkt`, `created_at`, `expires_at`, `revoked_at`, `last_used_at`.
- Constraints and indexes:
- `id` primary key.
- `idx_refresh_tokens_guid` on `guid`.
- `idx_refresh_tokens_expires_at` on `expires_at`.
- Used by:
- Enrollment poll finalization (`INSERT`).
- Token refresh endpoint (`/api/agent/token/refresh`).
- Enrollment key-rotation logic (`revoked_at` update).

#### `device_sites`
- Status: Active.
- Purpose: Site assignment map for hostnames.
- Columns: `device_hostname`, `site_id`, `assigned_at`.
- Constraints and indexes:
- `device_hostname` unique.
- FK declared: `site_id -> sites(id) ON DELETE CASCADE`.
- Used by:
- Enrollment finalize site assignment.
- Site assignment APIs (`/api/sites/assign`, `/api/sites/device_map`).
- Device listing/filter joins for site name.
- Scheduled-job device summaries.
- Notes:
- Mapping key is hostname, not GUID.
- There is no FK to `devices.hostname`; this is a logical relationship.

#### `user_site_assignments`
- Status: Active.
- Purpose: Operator-to-site RBAC scope assignments.
- Columns: `user_id`, `site_id`, `assigned_at`.
- Constraints and indexes:
- Unique index on `(user_id, site_id)`.
- `idx_user_site_assignments_user_id` on `user_id`.
- `idx_user_site_assignments_site_id` on `site_id`.
- Used by:
- `/api/user_site_assignments/selection` and `/api/user_site_assignments/assign`.
- Inventory, approvals, filters, quick jobs, scheduled jobs, tunnel/shell/VNC site-scope checks.
- Notes:
- Admins do not need rows in this table; they implicitly see all sites.
- Operators with no rows here have no site visibility until an admin assigns at least one site.

#### `device_vpn_config`
- Status: Dormant (schema present, no active read/write paths in current Engine source).
- Purpose: Reserved per-agent VPN configuration metadata.
- Columns: `agent_id`, `allowed_ports`, `updated_at`, `updated_by`.
- Constraints and indexes:
- `agent_id` primary key.
- Notes:
- Created by migrations; currently unused by active APIs/services.

### Operations and UI State
#### `activity_history`
- Status: Active.
- Purpose: Execution/activity ledger for quick jobs and job-like actions.
- Columns: `id`, `hostname`, `script_path`, `script_name`, `script_type`, `ran_at`, `status`, `stdout`, `stderr`.
- Constraints and indexes:
- `id` autoincrement primary key.
- Used by:
- Quick-run execution (`/api/scripts/quick_run`).
- Scheduled-jobs execution parity records.
- WebSocket `quick_job_result` updates.
- Activity APIs (`/api/device/activity/*`).
- Linked from `scheduled_job_run_activity.activity_id`.

#### `device_list_views`
- Status: Active.
- Purpose: Saved table views for the device list UI.
- Columns: `id`, `name`, `columns_json`, `filters_json`, `created_at`, `updated_at`.
- Constraints and indexes:
- `id` autoincrement primary key.
- `name` unique.
- Used by:
- `/api/device_list_views*` CRUD endpoints.
- Notes:
- Current schema has no user ownership column; views are globally stored.

#### `device_filters`
- Status: Active.
- Purpose: Saved filter definitions for target selection and device segmentation.
- Columns: `id`, `name`, `description`, `archived`, `criteria_mode`, `site_mode`, `basic_criteria_json`, `advanced_criteria_json`, `last_edited_by`, `created_at`, `updated_at`.
- Constraints and indexes:
- `id` autoincrement primary key.
- `name` unique.
- Used by:
- `/api/device_filters*` endpoints.
- `DeviceFilterMatcher` for match counts and scheduled-job target expansion.
- Notes:
- Active site membership is stored in `device_filter_sites`.
- Archived filters are excluded from scheduler pickers and runtime resolution.

#### `device_filter_sites`
- Status: Active.
- Purpose: Normalized site membership for saved device filters.
- Columns: `filter_id`, `site_id`.
- Constraints and indexes:
- No dedicated primary key; uniqueness is maintained by filter-write paths.
- Used by:
- `/api/device_filters*` write paths.
- `DeviceFilterMatcher.load_filters()` for site-mode hydration.

#### `device_software_inventory`
- Status: Active.
- Purpose: Normalized installed-software inventory for reliable filtering.
- Columns: `id`, `device_guid`, `name`, `name_normalized`, `version`, `source`, `captured_at`, `metadata_json`.
- Constraints and indexes:
- `id` autoincrement primary key.
- `idx_device_software_inventory_guid` on `device_guid`.
- `idx_device_software_inventory_name` on `name_normalized`.
- `idx_device_software_inventory_source` on `source`.
- `idx_device_software_inventory_guid_name_source` on `(device_guid, name_normalized, source)`.
- Used by:
- `/api/agent/details` ingestion refresh.
- `DeviceFilterMatcher` software-aware matching.
- Notes:
- Raw software blobs still live on `devices.software` for UI detail display, but matching uses this normalized table first.

### Scheduling and Automation
#### `scheduled_jobs`
- Status: Active.
- Purpose: Scheduled-job definitions.
- Columns: `id`, `name`, `components_json`, `targets_json`, `schedule_type`, `start_ts`, `duration_stop_enabled`, `expiration`, `execution_context`, `credential_id`, `use_service_account`, `enabled`, `created_at`, `updated_at`.
- Constraints and indexes:
- `id` autoincrement primary key.
- Used by:
- `/api/scheduled_jobs*` CRUD endpoints.
- Scheduler background loop.
- Notes:
- `credential_id` is logical linkage to `credentials.id`; no FK constraint in schema.

#### `scheduled_job_runs`
- Status: Active.
- Purpose: Execution state for scheduled occurrences. Legacy script jobs still use per-target rows; Engine-side Ansible jobs can now use shared rows per playbook component.
- Columns: `id`, `job_id`, `scheduled_ts`, `started_ts`, `finished_ts`, `status`, `error`, `created_at`, `updated_at`, `target_hostname`, `skip_reason`, `shared_execution`, `component_index`, `component_kind`, `component_name`.
- Constraints and indexes:
- `id` autoincrement primary key.
- FK declared: `job_id -> scheduled_jobs(id) ON DELETE CASCADE`.
- `idx_runs_job_sched_target` on `(job_id, scheduled_ts, target_hostname)`.
- Used by:
- Scheduler dispatch and status updates.
- WebSocket quick result handler for run state transitions.
- Scheduled-jobs run history and device status endpoints.
- Notes:
- Zero-target occurrences are stored as `status = Skipped` with `skip_reason = no_devices_targeted`.
- Shared Ansible rows leave `target_hostname` empty and use `shared_execution = 1`.

#### `scheduled_job_run_activity`
- Status: Active.
- Purpose: Link table from scheduled runs to `activity_history` rows.
- Columns: `id`, `run_id`, `activity_id`, `component_kind`, `script_type`, `component_path`, `component_name`, `created_at`.
- Constraints and indexes:
- `id` autoincrement primary key.
- FK declared: `run_id -> scheduled_job_runs(id) ON DELETE CASCADE`.
- FK declared: `activity_id -> activity_history(id) ON DELETE CASCADE`.
- `idx_run_activity_run` on `run_id`.
- `idx_run_activity_activity` unique on `activity_id`.
- Used by:
- Scheduler component dispatch bookkeeping.
- WebSocket run status propagation and run activity lookups.

#### `scheduled_job_run_targets`
- Status: Active.
- Purpose: Frozen point-in-time target membership for each scheduled occurrence or shared Ansible playbook run.
- Columns: `id`, `run_id`, `device_guid`, `hostname`, `site_id`, `resolved_from_filter_id`, `inventory_hostname`, `wireguard_peer_ip`, `resolved_connection`, `resolution_status`, `resolution_reason`, `resolved_from_filter_ids_json`, `created_at`.
- Constraints and indexes:
- `id` autoincrement primary key.
- FK declared: `run_id -> scheduled_job_runs(id) ON DELETE CASCADE`.
- `idx_scheduled_job_run_targets_run` on `run_id`.
- `idx_scheduled_job_run_targets_filter` on `resolved_from_filter_id`.
- `idx_scheduled_job_run_targets_host` on `hostname`.
- Used by:
- Scheduler snapshot creation.
- Scheduled-job device history endpoint.
- Notes:
- Legacy rows may still repeat a host when more than one saved filter contributed to the same occurrence target.
- Shared Ansible rows store the generated inventory alias and target-resolution outcome per device.

#### `credentials`
- Status: Active for scheduler and WebUI credential selection; protected at rest after Aegis Cipher setup.
- Purpose: Stored credential materials for remote execution contexts.
- Columns: `id`, `name`, `description`, `site_id`, `credential_type`, `connection_type`, `username`, `password_encrypted`, `private_key_encrypted`, `private_key_passphrase_encrypted`, `become_method`, `become_username`, `become_password_encrypted`, `metadata_json`, `created_at`, `updated_at`.
- Constraints and indexes:
- `id` autoincrement primary key.
- `name` unique.
- FK declared: `site_id -> sites(id) ON DELETE SET NULL`.
- Used by:
- `/api/credentials` CRUD endpoints.
- Scheduled Ansible job resolution for `ssh` and `winrm` execution contexts.
- Notes:
- Before Aegis setup, legacy plaintext values may still exist in the `*_encrypted` columns as migration input.
- After Aegis setup, secret columns store ASCII `aegis:v1:` envelopes even though the schema type remains `BLOB`.
- The runtime decrypts credential secrets on demand through `Data/Engine/services/aegis_cipher.py`.
- Operator-facing credential APIs now wait for bootstrap phase `login_required`; stale operator sessions no longer bypass the lock state after restart.
- If `metadata_json` contains `aegis_secret_state = "reset_required"`, the record survived an Aegis force reset but one or more stored secret fields were intentionally destroyed and must be re-entered.

#### `aegis_cipher_state`
- Status: Active after the first Aegis Cipher setup.
- Purpose: Singleton state row for the Engine-global Aegis KDF parameters and verification token.
- Columns: `id`, `kdf_name`, `kdf_params_json`, `verification_token`, `created_at`, `updated_at`.
- Constraints and indexes:
- `id` primary key, with Borealis using `id = 1`.
- Used by:
- `Data/Engine/services/aegis_cipher.py` setup, unlock, rotation, migration, and force-reset flows.
- `/api/bootstrap/state`, `/api/bootstrap/aegis/setup`, `/api/bootstrap/aegis/unlock`, `/api/bootstrap/admin/*`, `/api/aegis/status`, `/api/aegis/rotate`, and `/api/aegis/force_reset`.
- Notes:
- `kdf_params_json` stores the per-install `scrypt` parameters and Base64 salt.
- `verification_token` stores an Aegis-encrypted constant plaintext that validates the entered cipher without persisting the derived key.
- The derived key is never stored in the database; it lives only in Engine memory for the process lifetime.
- A force reset deletes the singleton row after protected secret material is destroyed, allowing a fresh Aegis setup to start from a clean state.

#### `ansible_play_recaps`
- Status: Active for Engine-side scheduled Ansible execution; broader API/UI surfacing is still incomplete.
- Purpose: Intended run recap storage for Ansible executions.
- Columns: `id`, `run_id`, `hostname`, `agent_id`, `playbook_path`, `playbook_name`, `scheduled_job_id`, `scheduled_run_id`, `activity_job_id`, `status`, `recap_text`, `recap_json`, `started_ts`, `finished_ts`, `created_at`, `updated_at`.
- Constraints and indexes:
- `id` autoincrement primary key.
- `run_id` unique.
- `idx_ansible_recaps_host_created` on `(hostname, created_at)`.
- `idx_ansible_recaps_status` on `status`.
- Used by:
- Engine-side scheduled Ansible runs executed from the Linux Engine.
- Future recap/report APIs and UI views.

#### `agent_service_account`
- Status: Dormant (schema present, no active read/write paths in current Engine source).
- Purpose: Reserved per-agent service account credential storage.
- Columns: `agent_id`, `username`, `password_hash`, `password_encrypted`, `last_rotated_utc`, `version`.
- Constraints and indexes:
- `agent_id` primary key.
- Notes:
- `password_encrypted` remains outside Aegis Cipher v1 scope because the table has no active runtime read or write paths today.

### Access Management
#### `users`
- Status: Active.
- Purpose: Operator identity, login state, and recovery state.
- Columns: `id`, `username`, `display_name`, `password_sha512`, `role`, `last_login`, `created_at`, `updated_at`, `mfa_enabled`, `mfa_disabled`, `mfa_secret`, `auth_reset_required`, `auth_reset_at`.
- Constraints and indexes:
- `id` autoincrement primary key.
- `username` unique.
- Used by:
- Login and MFA flows.
- Bootstrap setup and admin recovery flows.
- User administration APIs.
- Device approval auditing (`approved_by_user_id` lookup by id or username).
- Notes:
- Usernames, display names, and roles remain plaintext so bootstrap recovery can identify existing administrators before operator login is available.
- After Aegis setup, `password_sha512` stores an Aegis envelope containing the SHA-512 password hash, and `mfa_secret` stores an Aegis envelope containing the operator's TOTP seed.
- `auth_reset_required=1` means a force reset destroyed that operator's auth secrets; Borealis blocks normal login until an admin recovery or admin password reset clears the flag.
- Initial deployment no longer seeds a default admin automatically; bootstrap creates the first administrator after Aegis is configured.
- `mfa_disabled=0` means MFA is required by default, even before the operator has completed first-time setup.
- `mfa_secret` remaining empty with `mfa_disabled=0` causes the next successful password login to enter MFA setup immediately.

#### `user_passkeys`
- Status: Active.
- Purpose: Stored WebAuthn passkeys for operator sign-in.
- Columns: `id`, `user_id`, `credential_id`, `public_key`, `sign_count`, `label`, `transports_json`, `aaguid`, `created_at`, `last_used_at`, `credential_lookup_hmac`, `secret_encrypted`.
- Constraints and indexes:
- `id` autoincrement primary key.
- Unique index on `credential_lookup_hmac`.
- Used by:
- Passkey registration, authentication, listing, rename, and delete endpoints.
- Notes:
- `label`, `transports_json`, `created_at`, and `last_used_at` stay plaintext so the UI can render passkey inventory quickly after operator login.
- `credential_lookup_hmac` stores `HMAC-SHA256(app_secret, normalized_credential_id)` so Borealis can locate a passkey without keeping the raw credential id in a unique plaintext index.
- `secret_encrypted` stores an Aegis envelope containing JSON for `credential_id`, `public_key`, `sign_count`, and `aaguid`.
- Legacy `credential_id`, `public_key`, `sign_count`, and `aaguid` columns remain for migration compatibility and may be blanked after the passkey has been migrated into `secret_encrypted`.

#### `github_token`
- Status: Active.
- Purpose: Persisted GitHub API token for repo hash checks and integration.
- Columns: `token`, `reset_required`, `reset_at`.
- Constraints and indexes:
- No explicit primary key.
- Used by:
- GitHub integration store/load.
- `/api/github/token` admin endpoint.
- Notes:
- Service writes token by deleting all rows then inserting one row.
- Reads use `SELECT token FROM github_token LIMIT 1` or `SELECT token, reset_required, reset_at FROM github_token LIMIT 1` depending on whether reset-state metadata is needed.
- Before Aegis setup, a legacy plaintext token may exist and is migrated during setup.
- After Aegis setup, `token` stores an ASCII `aegis:v1:` envelope, and a locked Engine treats it as unavailable until unlock.
- After an Aegis force reset, Borealis can preserve a row with `token = NULL`, `reset_required = 1`, and `reset_at` set so the WebUI can warn that the GitHub token must be re-entered.


## Assembly Catalog Tables (`assemblies.*`)
### Domains and Tables
- `assemblies.official_assemblies` (`AssemblyDomain.OFFICIAL`)
- `assemblies.community_assemblies` (`AssemblyDomain.COMMUNITY`)
- `assemblies.user_created_assemblies` (`AssemblyDomain.USER`)

Each table has the same schema:

#### `assemblies.<domain>`
- Status: Active.
- Purpose: Assembly summary fields and inline payload JSON.
- Columns: `assembly_guid`, `display_name`, `summary`, `assembly_type`, `assembly_subtype`, `payload_json`, `source_repo`, `source_path`, `source_version`, `content_hash`, `payload_size_bytes`, `created_at`, `updated_at`.
- Constraints and indexes:
- `assembly_guid` primary key.
- `idx_assemblies_type` on `assembly_type`.
- Notes:
- Payload JSON is stored inline in `payload_json`.
- `assembly_type` is the authoritative routing discriminator for editors and execution pipelines.
- `metadata_json` and `payload_type` are removed from the active schema.
- Engine startup validates this schema strictly and fails fast if legacy columns are still present.
- `source_repo`, `source_path`, and `source_version` track Aurora provenance for official assemblies.
- `content_hash` stores the Engine-computed canonical SHA-256 used for update detection.
- The bundled official snapshot is versioned under `Data/Engine/Official_Assemblies/` as a seed snapshot and synced into `assemblies.official_assemblies` on startup.

### `assemblies.official_catalog_state`
- Status: Active.
- Purpose: Tracks bundled seed state plus the latest Aurora catalog metadata applied to each official assembly GUID.
- Columns: `assembly_guid`, `bundled_hash`, `remote_hash`, `catalog_hash`, `applied_hash`, `last_applied_source`, `repo_url`, `source_url`, `source_repo`, `source_path`, `source_version`, `last_catalog_sync_at`, `updated_at`.
- Constraints and indexes:
- `assembly_guid` primary key.
- Notes:
- `catalog_hash` reflects the latest hash seen in the active official catalog source.
- `applied_hash` reflects the version currently written into `assemblies.official_assemblies`.
- `last_catalog_sync_at` captures when Borealis last synced catalog metadata for that GUID.

## Deprecated and Removed Schema
### Removed Tables
- `enrollment_install_codes` (removed; superseded by `sites.enrollment_code`).
- `enrollment_install_codes_persistent` (removed; superseded by `sites.enrollment_code`).
- `payloads` in assembly DBs (removed by migration to consolidated `assemblies` schema).

### Auto-Migrated Legacy Columns
- `sites.enrollment_code_id` is removed by startup rebuild migration.
- `device_approvals.enrollment_code_id` is removed by startup rebuild migration.
- `device_filters.scope` and `device_filters.apply_to_all_sites` are removed by startup rebuild migration.

### Deprecated API Surface (still present intentionally)
- `POST /api/admin/enrollment-codes` returns `410` (`legacy_endpoint_removed_use_sites_api`).
- `DELETE /api/admin/enrollment-codes/<code_id>` returns `410` (`legacy_endpoint_removed_use_sites_api`).

## Codex Agent (Detailed)
### Troubleshooting queries
```sql
-- 1) List user-managed Borealis tables in PostgreSQL
SELECT schemaname, tablename
FROM pg_tables
WHERE schemaname NOT IN ('pg_catalog', 'information_schema')
ORDER BY schemaname, tablename;

-- 2) Site-to-enrollment code map (current source of truth)
SELECT id, name, enrollment_code
FROM sites
ORDER BY LOWER(name);

-- 3) Pending approvals with site context
SELECT da.id, da.approval_reference, da.hostname_claimed, da.enrollment_code, da.status, s.name AS site_name
FROM device_approvals da
LEFT JOIN sites s ON s.id = da.site_id
WHERE LOWER(da.status) = 'pending'
ORDER BY da.created_at ASC;

-- 4) Device-to-site assignments
SELECT d.guid, d.hostname, s.id AS site_id, s.name AS site_name
FROM devices d
LEFT JOIN device_sites ds ON ds.device_hostname = d.hostname
LEFT JOIN sites s ON s.id = ds.site_id
ORDER BY LOWER(d.hostname);

-- 5) Check for orphaned hostname mappings (no matching device row)
SELECT ds.device_hostname, ds.site_id
FROM device_sites ds
LEFT JOIN devices d ON d.hostname = ds.device_hostname
WHERE d.hostname IS NULL;

-- 6) Check for orphaned site mappings (no matching site row)
SELECT ds.device_hostname, ds.site_id
FROM device_sites ds
LEFT JOIN sites s ON s.id = ds.site_id
WHERE s.id IS NULL;

-- 7) Scheduled run/activity linkage health
SELECT r.id AS run_id, r.job_id, r.status, a.id AS link_id, a.activity_id
FROM scheduled_job_runs r
LEFT JOIN scheduled_job_run_activity a ON a.run_id = r.id
ORDER BY r.id DESC
LIMIT 200;

-- 8) Confirm legacy columns are gone (expect 0 rows)
SELECT COUNT(*) AS has_legacy_sites_col
FROM pragma_table_info('sites')
WHERE name = 'enrollment_code_id';
```

### Change-management checklist for schema edits
- Update creation/migration code first (`database.py`, `database_migrations.py`, scheduler table init, or assembly DB manager).
- Update this document and any affected domain docs (`device-management.md`, `scheduled-jobs.md`, `security-and-trust.md`).
- Update unit tests that rely on local schema fixtures (`Data/Engine/Unit_Tests/conftest.py`).
- Verify runtime startup applies schema without errors by checking `Engine/Logs/engine.log`.

### Data-model guidance for the current enrollment design
- Keep site/code association in `sites.enrollment_code` unless the enrollment model changes fundamentally.
- Keep `device_sites` as hostname-to-site map for UI and filter joins.
- Treat `device_approvals.enrollment_code` as immutable audit snapshot of the code used at request time.

## Related Documentation
- [Engine Runtime](engine-runtime.md)
- [Security and Trust](security-and-trust.md)
- [Device Management](device-management.md)
- [Scheduled Jobs](scheduled-jobs.md)
- [Assemblies and Quick Jobs](assemblies.md)
- [Aegis Cipher](features_to_implement/aegis_cipher.md)
