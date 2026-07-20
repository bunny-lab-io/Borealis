# Database Reference

Describe the Borealis PostgreSQL schema, table ownership, runtime interactions, and legacy migration structures so operators and Codex agents can troubleshoot and change schema safely.

## Scope
- Primary runtime database: PostgreSQL (set via `BOREALIS_DATABASE_URL`). Linux Engine container deployments use the `postgres-db` service on `127.0.0.1:5432`.
- Assembly catalog tables live in PostgreSQL `assemblies.*`.
- K3s migration Stage 9 adds Longhorn as the storage baseline and creates a shadow `postgres-db` StatefulSet before PostgreSQL moves. PostgreSQL remains Compose-owned until the explicit traffic cutover with backup, restore, and import validation.

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
agent enrollment fingerprint ---< enrollment_code_failures (logical identity)

scheduled_jobs (id) ----------< scheduled_job_runs (job_id)
scheduled_job_runs (id) ------< scheduled_job_run_activity (run_id)
scheduled_job_runs (id) ------< scheduled_job_run_targets (run_id)
activity_history (id) --------< scheduled_job_run_activity (activity_id, unique)
activity_history.metadata_json.patch_progress stores latest scheduled Windows patch install progress.

patch_policies (id) ----------< patch_policy_sites (policy_id)
patch_policies (id) ----------< patch_policy_targets (policy_id)
patch_policies (id) ----------< patch_policy_exclusions (policy_id)
patch_policies (id) ----------< patch_policy_rules (policy_id)
patch_policies (id) ----------< patch_policy_runs (policy_id, history)
patch_policies (id) ----------< patch_policy_device_state (effective_policy_id, transient)
patch_catalog_entries caches KB/update identity metadata for deferral fallback.

watchdogs (id) ---------------< watchdog_sites (watchdog_id)
watchdogs (id) ---------------< watchdog_targets (watchdog_id)
watchdogs (id) ---------------< watchdog_device_overrides (watchdog_id)
watchdogs (id) ---------------< watchdog_device_state (watchdog_id)
watchdogs (id) ---------------< watchdog_incidents (watchdog_id)

users (id/username) ----------< device_approvals.approved_by_user_id (soft relation)
users (id) -------------------< user_site_assignments (user_id)
sites (id) -------------------< user_site_assignments (site_id)
```

## Important PostgreSQL Behavior
- Borealis uses PostgreSQL as the live Engine database, so engine troubleshooting should focus on server-side constraints, indexes, sequences, and transaction boundaries.
- Current Engine PostgreSQL state remains under `Engine/Services/postgres-db/state`. Longhorn installation and the Stage 9 shadow StatefulSet do not migrate, copy, delete, or re-own that state.
- Constraint enforcement, indexes, and transactions are handled server-side by PostgreSQL.
- Some Borealis relations remain intentionally soft in schema/API logic, so application code still performs explicit cleanup and validation for tables such as `device_sites` and approval mappings.
- Borealis now treats database connections as short-lived pooled resources. Request handlers and background services should fetch rows, release the connection, and then perform Python-side enrichment, JSON shaping, crypto, GitHub lookups, or target expansion outside the transaction boundary.
- Healthy pooled sessions often appear in `pg_stat_activity` as `state = idle`, `wait_event = ClientRead`, and a recent `ROLLBACK` statement. That pattern means the session is clean and ready to be reused.
- Unhealthy sessions usually appear as `state = idle in transaction` with an older `SELECT`, `UPDATE`, or `INSERT` still attached. That pattern means Borealis is holding a transaction open after the SQL work is already done.

## Connection Lifecycle Guidelines
### Healthy patterns
- Open the connection immediately before the SQL work starts.
- Execute the minimum required statements.
- Fetch the rows or commit the write.
- Close or return the connection to the pool immediately.
- Perform non-DB work after the connection has been released:
- JSON parsing and payload shaping.
- Device/filter/watchdog target expansion.
- Aegis encryption or decryption.
- GitHub or other network lookups.
- AG Grid summary aggregation and UI formatting.

### Unhealthy patterns
- Holding a connection open while calling `resolve_target_entries()`, `fetch_devices()`, or other target-resolution helpers.
- Holding a connection open while decrypting credentials or encrypting secret blobs.
- Holding a connection open while fetching repo hashes, waiting on Socket.IO, or calling other integrations.
- Returning large payloads after `cur.fetchall()` without first closing the connection.
- Reusing a write transaction for post-commit readback when the response can be assembled in a fresh short-lived read.

### Practical symptoms
- `QueuePool` timeout errors in `Engine/Services/api-backend/logs/error.log`.
- Operators seeing random `500` responses on otherwise unrelated routes such as `/api/auth/me`.
- Socket reconnect churn or delayed page refreshes when the Engine is under load.
- `pg_stat_activity` showing many `idle in transaction` rows older than a few seconds.
- Pages that should be read-only becoming slower as more operators are online.

### Operator CLI check for live PostgreSQL sessions
- Operators with root access on the Engine host can inspect Borealis PostgreSQL sessions directly during troubleshooting.
- Run this command from the Engine host shell:
```sh
sudo -u postgres psql -d borealis -c "select pid, state, wait_event, query_start, now()-query_start as age, left(query,200) as query from pg_stat_activity where datname='borealis' order by query_start;"
```
- Healthy pooled connections usually appear as `state = idle`, `wait_event = ClientRead`, and a recent `ROLLBACK` statement.
- Problematic sessions usually appear as `state = idle in transaction` with an older `SELECT`, `UPDATE`, or `INSERT` still attached.
- If Borealis feels slow, this command is the fastest way to distinguish normal pooled connections from sessions that are holding transactions open too long.

## Engine Tuning Profiles
- Container deployments auto-detect the Engine host sizing profile during every `Engine.sh --network-mode public|local deploy` and redeploy.
- Profile selection is based on detected CPU and RAM only.
- Storage is displayed in the CLI as deployment guidance, but it does not change the selected profile or the applied DB tuning.
- The launcher writes selected profile metadata, database pool values, PostgreSQL settings, and site-worker scheduled work-item slots into `Engine/Deploy/compose.env`.
- Docker Compose applies PostgreSQL tuning through the `postgres-db` container startup command, so operators do not run manual `ALTER SYSTEM` steps for normal profile tuning.
- The current auto-selected single-node profiles are:
  - `Homelab`
  - `Small Business`
  - `MSP / Production`
  - `Enterprise`
- The roadmap-only `Enterprise Clustered` profile in `Docs/Engine/deploying-the-engine.md` is not auto-selected today because Borealis does not yet support clustered orchestration.
- Site-worker scheduled work-item slots are profile-managed and exposed as read-only in Server Info.

### Profile selection thresholds
- Borealis scores CPU and RAM separately, then uses the lower of the two ranks as the effective profile so an unbalanced host does not get over-tuned.
- CPU thresholds:
  - `Homelab`: fewer than `8` vCPU
  - `Small Business`: `8-15` vCPU
  - `MSP / Production`: `16-23` vCPU
  - `Enterprise`: `24+` vCPU
- RAM thresholds:
  - `Homelab`: fewer than `16 GiB`
  - `Small Business`: `16-31 GiB`
  - `MSP / Production`: `32-63 GiB`
  - `Enterprise`: `64 GiB+`

### Shared values across all auto-configured profiles
- Engine DB connect timeout:
  - `BOREALIS_DB_CONNECT_TIMEOUT = 15`
- Engine idle-in-transaction safety timeout:
  - `BOREALIS_DB_IDLE_IN_TXN_TIMEOUT_MS = 60000`
- PostgreSQL autovacuum scale factors:
  - `autovacuum_vacuum_scale_factor = 0.02`
  - `autovacuum_analyze_scale_factor = 0.01`
- PostgreSQL WAL and planner defaults:
  - `wal_compression = on`
  - `checkpoint_timeout = 15min`
  - `checkpoint_completion_target = 0.9`
  - `random_page_cost = 1.1`

### `Homelab`
- Intended host shape:
  - fewer than `8` vCPU or fewer than `16 GiB` RAM
- Engine DB pool:
  - `BOREALIS_DB_POOL_SIZE = 10`
  - `BOREALIS_DB_MAX_OVERFLOW = 10`
  - effective pooled Engine connection burst capacity: `20`
- Site-worker scheduled work-item slots:
  - `BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY = 5`
- PostgreSQL connection ceiling:
  - `max_connections = 80`
- PostgreSQL memory and cache:
  - `shared_buffers = max(1 GiB, min(25% of RAM, 4 GiB))`
  - `effective_cache_size = max(4 GiB, min(62.5% of RAM, 12 GiB))`
  - `work_mem = 4 MB`
  - `maintenance_work_mem = 256 MB`
- PostgreSQL worker and planner parallelism:
  - `max_worker_processes = 8`
  - `max_parallel_workers = 8`
  - `max_parallel_workers_per_gather = 2`
- PostgreSQL autovacuum:
  - `autovacuum_max_workers = 3`
  - `autovacuum_vacuum_cost_limit = 1000`
  - `autovacuum_naptime = 30s`
- PostgreSQL WAL / IO:
  - `max_wal_size = 4GB`
  - `min_wal_size = 512MB`
  - `effective_io_concurrency = 16`

### `Small Business`
- Intended host shape:
  - at least `8` vCPU and at least `16 GiB` RAM, but below `16` vCPU or below `32 GiB` RAM
- Engine DB pool:
  - `BOREALIS_DB_POOL_SIZE = 12`
  - `BOREALIS_DB_MAX_OVERFLOW = 16`
  - effective pooled Engine connection burst capacity: `28`
- Site-worker scheduled work-item slots:
  - `BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY = 8`
- PostgreSQL connection ceiling:
  - `max_connections = 120`
- PostgreSQL memory and cache:
  - `shared_buffers = max(4 GiB, min(25% of RAM, 8 GiB))`
  - `effective_cache_size = max(8 GiB, min(62.5% of RAM, 16 GiB))`
  - `work_mem = 8 MB`
  - `maintenance_work_mem = 512 MB`
- PostgreSQL worker and planner parallelism:
  - `max_worker_processes = 8`
  - `max_parallel_workers = 8`
  - `max_parallel_workers_per_gather = 2`
- PostgreSQL autovacuum:
  - `autovacuum_max_workers = 4`
  - `autovacuum_vacuum_cost_limit = 1500`
  - `autovacuum_naptime = 20s`
- PostgreSQL WAL / IO:
  - `max_wal_size = 6GB`
  - `min_wal_size = 1GB`
  - `effective_io_concurrency = 32`

### `MSP / Production`
- Intended host shape:
  - at least `16` vCPU and at least `32 GiB` RAM, but below `24` vCPU or below `64 GiB` RAM
- Engine DB pool:
  - `BOREALIS_DB_POOL_SIZE = 20`
  - `BOREALIS_DB_MAX_OVERFLOW = 20`
  - effective pooled Engine connection burst capacity: `40`
- Site-worker scheduled work-item slots:
  - `BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY = 12`
- PostgreSQL connection ceiling:
  - `max_connections = 150`
- PostgreSQL memory and cache:
  - `shared_buffers = max(8 GiB, min(25% of RAM, 16 GiB))`
  - `effective_cache_size = max(20 GiB, min(62.5% of RAM, 32 GiB))`
  - `work_mem = 8 MB`
  - `maintenance_work_mem = 512 MB`
- PostgreSQL worker and planner parallelism:
  - `max_worker_processes = 12`
  - `max_parallel_workers = 12`
  - `max_parallel_workers_per_gather = 4`
- PostgreSQL autovacuum:
  - `autovacuum_max_workers = 5`
  - `autovacuum_vacuum_cost_limit = 2000`
  - `autovacuum_naptime = 15s`
- PostgreSQL WAL / IO:
  - `max_wal_size = 8GB`
  - `min_wal_size = 1GB`
  - `effective_io_concurrency = 64`

### `Enterprise`
- Intended host shape:
  - at least `24` vCPU and at least `64 GiB` RAM
- Engine DB pool:
  - `BOREALIS_DB_POOL_SIZE = 24`
  - `BOREALIS_DB_MAX_OVERFLOW = 24`
  - effective pooled Engine connection burst capacity: `48`
- Site-worker scheduled work-item slots:
  - `BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY = 16`
- PostgreSQL connection ceiling:
  - `max_connections = 180`
- PostgreSQL memory and cache:
  - `shared_buffers = max(12 GiB, min(25% of RAM, 24 GiB))`
  - `effective_cache_size = max(32 GiB, min(62.5% of RAM, 64 GiB))`
  - `work_mem = 16 MB`
  - `maintenance_work_mem = 1 GiB`
- PostgreSQL worker and planner parallelism:
  - `max_worker_processes = 16`
  - `max_parallel_workers = 16`
  - `max_parallel_workers_per_gather = 4`
- PostgreSQL autovacuum:
  - `autovacuum_max_workers = 6`
  - `autovacuum_vacuum_cost_limit = 2500`
  - `autovacuum_naptime = 15s`
- PostgreSQL WAL / IO:
  - `max_wal_size = 12GB`
  - `min_wal_size = 2GB`
  - `effective_io_concurrency = 64`

### Maintenance rule
- If you change the profile thresholds or any profile-tuned values in `Engine.sh` or container PostgreSQL configuration, update this section in `Docs/Reference/Data and Schema/db-reference.md` in the same change so the operator and Codex guidance stays accurate.

## Container PostgreSQL Operations
- Runtime state: `Engine/Services/postgres-db/state`.
- Compose environment: `Engine/Deploy/compose.env`.
- Default database URL shape: `postgresql://borealis:<generated-password>@127.0.0.1:5432/borealis`.
- Engine deploy starts `postgres-db` and runs schema setup before API and scheduler containers reconcile. Schema setup covers both `engine.*` runtime tables and `assemblies.*` catalog tables, so operators do not need a separate first-run schema command.
- Stage 9 K3s preparation creates a separate `postgres-db` StatefulSet and Longhorn PVC inside K3s for storage/runtime validation. That pod is ClusterIP-only and is not the live Engine database while `BOREALIS_POSTGRES_RUNTIME_OWNER=compose`.
- `Engine.sh deploy` does not delete the K3s PostgreSQL StatefulSet or PVC when Stage 9 is disabled or changed. PVC cleanup is intentionally manual.
- `Data/Engine/Containers/sterilize-systemd-runtime.sh` attempts a logical dump of the legacy `borealis` database before disabling host PostgreSQL and renaming `Engine/` to `Engine.old/`.
- Preserved dumps land under the legacy runtime after rename, usually `Engine.old/Deploy/legacy-postgres-borealis-<timestamp>.sql`.
- Import after first container deployment with `./Data/Engine/Containers/import-legacy-postgres-dump.sh Engine.old/Deploy/<dump>.sql`.
- Do not run host PostgreSQL on `127.0.0.1:5432` after migration; it conflicts with `postgres-db`.

### Preferred remediation pattern
```python
conn = db_conn_factory()
try:
    cur = conn.cursor()
    cur.execute("SELECT ...")
    rows = cur.fetchall()
finally:
    conn.close()

# Safe to do slower work now.
payload = [shape_row(row) for row in rows]
payload = enrich_payload(payload)
return payload
```

### Patterns to avoid
```python
conn = db_conn_factory()
try:
    cur = conn.cursor()
    cur.execute("SELECT ...")
    rows = cur.fetchall()
    payload = enrich_payload(rows)  # bad: could do crypto/network/target expansion here
    return payload
finally:
    conn.close()
```

??? example "Detailed Codex Breakdown"

    ### API endpoints

    None. This page documents persistence structures used by many endpoints.

    ### Related documentation

    - [Engine Runtime](../Core%20Runtimes/engine-runtime.md)
    - [Security Whitepaper](../../Reference/security-whitepaper.md)
    - [Device Auditing](../../Using%20the%20Platform/device-auditing.md)
    - [Scheduled Jobs](../../Using%20the%20Platform/scheduled-jobs.md)
    - [Assemblies](../../Using%20the%20Platform/Assemblies/assemblies.md)
    - [Security Whitepaper](../../Reference/security-whitepaper.md)

    ### Source map

    - Deploy-time schema caller: `Engine.sh`
    - Runtime schema setup: `Data/Engine/Containers/api-backend/data/database.py`
    - Startup migrations: `Data/Engine/Containers/api-backend/data/database_migrations.py`
    - Scheduler database behavior: `Data/Engine/Containers/api-backend/cmd/api-backend/scheduler_manager.go` and `Data/Engine/Containers/api-backend/cmd/api-backend/scheduler_execution.go`
    - Scheduler queue leasing: `Data/Engine/Containers/api-backend/data/services/job_scheduler/queue.py`
    - Assembly schema source: `Data/Engine/Containers/api-backend/data/assembly_management/databases.py`
    - Bundled official assembly snapshot: `Data/Engine/Containers/api-backend/data/Official_Assemblies/`

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

    -- 4) Recent wrong-code enrollment attempts
    SELECT hostname_claimed, enrollment_code_mask, remote_addr, attempt_count, last_seen_at
    FROM enrollment_code_failures
    ORDER BY last_seen_at DESC;

    -- 5) Device-to-site assignments
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

    -- 9) Identify sessions that are cleanly pooled and waiting for reuse
    SELECT pid, state, wait_event, query_start, now() - query_start AS age, left(query, 200) AS query
    FROM pg_stat_activity
    WHERE datname = 'borealis'
    ORDER BY query_start;

    -- 10) Focus only on bad pooled sessions (these should be rare and short-lived)
    SELECT pid, usename, state, wait_event, xact_start, now() - xact_start AS txn_age, left(query, 200) AS query
    FROM pg_stat_activity
    WHERE datname = 'borealis'
      AND state = 'idle in transaction'
    ORDER BY xact_start NULLS LAST;
    ```

    ### Change-management checklist for schema edits
    - Update creation/migration code first (`database.py`, `database_migrations.py`, scheduler table init, or assembly DB manager).
    - Update this document and any affected domain docs (`device-auditing.md`, `scheduled-jobs.md`, `security-whitepaper.md`).
    - Update unit tests that rely on local schema fixtures (`Data/Engine/Unit_Tests/conftest.py`).
    - Verify runtime startup applies schema without errors by checking `Engine/Services/api-backend/logs/engine.log`.

    ### Data-model guidance for the current enrollment design
    - Keep site/code association in `sites.enrollment_code` unless the enrollment model changes fundamentally.
    - Keep `device_sites` as hostname-to-site map for UI and filter joins.
    - Treat `device_approvals.enrollment_code` as immutable audit snapshot of the code used at request time.
    - Treat `enrollment_code_failures.enrollment_code_mask` as operator visibility only; do not persist full wrong codes.

    ### Codex checklist for DB connection hygiene
    - Start with the hottest request or loop, not the easiest file. Prioritize watchdogs, device inventory, scheduled jobs, workflows, and auth-sensitive routes.
    - Search for `conn =`, `_db_conn()`, `_conn()`, `cur.fetchall()`, `commit()`, and `conn.close()` in the target module before editing.
    - Look for any work between the last SQL statement and `conn.close()`:
    - payload shaping loops
    - `json.loads()` or other parsing
    - `resolve_target_entries()` or `fetch_devices()`
    - Aegis crypto helpers
    - GitHub or other integration lookups
    - If that work does not require the live cursor or transaction, move it after `conn.close()`.
    - For write paths, keep the transaction open only for validation reads that must be consistent with the write. Do not leave the connection open for response shaping.
    - If a route resolves filters or device targets more than once, fetch one inventory snapshot and reuse it for the whole request.
    - If a helper repeatedly asks for repo or catalog metadata, add a short TTL cache so repeated reads do not create unnecessary external latency while DB connections are checked out.
    - After each fix, test the route and inspect `pg_stat_activity` to confirm pooled sessions return to `idle` rather than `idle in transaction`.

    ### Engine runtime database tables (`engine.*`)

    ### Enrollment, Identity, and Site Mapping
    #### `sites`
    - Status: Active.
    - Purpose: Site records and static enrollment codes.
    - Columns: `id`, `name`, `description`, `created_at`, `enrollment_code`, `auto_approve_until`.
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
    - `auto_approve_until` stores a UTC epoch timestamp for temporary site-level auto-approval. Enrollment only auto-approves while the timestamp is in the future and hostname conflict checks are safe.

    #### `device_approvals`
    - Status: Active.
    - Purpose: Enrollment approval queue and history.
    - Columns: `id`, `approval_reference`, `guid`, `hostname_claimed`, `ssl_key_fingerprint_claimed`, `enrollment_code`, `site_id`, `status`, `client_nonce`, `server_nonce`, `agent_pubkey_der`, `created_at`, `updated_at`, `approved_by_user_id`, `onboarding_job_id`, `onboarding_run_id`, `onboarding_target`.
    - Constraints and indexes:
    - `id` primary key.
    - `approval_reference` unique.
    - `idx_da_status` on `status`.
    - `idx_da_fp_status` on `(ssl_key_fingerprint_claimed, status)`.
    - `idx_da_site` on `site_id`.
    - `idx_da_onboarding_job` on `onboarding_job_id`.
    - Used by:
    - Enrollment request and poll endpoints.
    - Admin approval APIs (`/api/admin/device-approvals*`).
    - Notes:
    - `status` lifecycle typically `pending -> approved|denied|expired -> completed`.
    - `site_id` and `approved_by_user_id` are soft relations (not enforced FK in schema).
    - Automatic local-network onboarding stores source job/run/target context when the agent submits it. Approval trust flow remains unchanged.
    - Rebuild migration removes legacy `enrollment_code_id` if present.

    #### `enrollment_code_failures`
    - Status: Active.
    - Purpose: Rolling visibility for agents actively submitting wrong enrollment codes.
    - Columns: `id`, `hostname_claimed`, `ssl_key_fingerprint_claimed`, `enrollment_code_mask`, `remote_addr`, `first_seen_at`, `last_seen_at`, `attempt_count`, `last_error`.
    - Constraints and indexes:
    - `id` primary key.
    - `uq_enrollment_code_failures_fp` unique on `ssl_key_fingerprint_claimed`.
    - `idx_enrollment_code_failures_last_seen` on `last_seen_at`.
    - Used by:
    - Enrollment request endpoint records `invalid_enrollment_code` failures after payload and key validation.
    - `GET /api/admin/device-approvals?status=wrong_code` returns failures seen in the recent active window.
    - Notes:
    - Stores masked codes only. Full submitted enrollment codes stay out of the table.
    - A later valid enrollment request from the same fingerprint clears the failure row.
    - Old rows are opportunistically pruned by the enrollment request path.

    #### `devices`
    - Status: Active (core inventory and identity table).
    - Purpose: Canonical device identity and inventory snapshot.
    - Columns: `guid`, `hostname`, `description`, `created_at`, `last_enrollment_at`, `agent_hash`, `agent_role_health`, `memory`, `network`, `software`, `services`, `storage`, `cpu`, `sessions`, `processes`, `device_type`, `domain`, `external_ip`, `internal_ip`, `last_reboot`, `last_seen`, `cpu_percent`, `memory_percent`, `last_user`, `operating_system`, `uptime`, `agent_id`, `connection_type`, `connection_endpoint`, `agent_release_channel_override`, `agent_release_channel`, `agent_branch`, `agent_update_channel`, `agent_update_target_build_id`, `agent_update_state`, `agent_update_error`, `agent_update_source`, `agent_vnc_password`, `ssl_key_fingerprint`, `token_version`, `status`, `key_added_at`.
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

    #### `metadata_field_definitions`
    - Status: Active.
    - Purpose: Global Agent Metadata Field descriptions for fixed fields `Field 001` through `Field 500`.
    - Columns: `field_number`, `description`, `updated_at`, `updated_by`.
    - Constraints and indexes:
    - `field_number` primary key.
    - `idx_metadata_field_definitions_updated` on `updated_at`.
    - Used by:
    - `/api/metadata_fields` list/update endpoints.
    - Device Summary Metadata Fields tab labels.
    - Device Filter metadata-field picker.
    - Notes:
    - Empty descriptions fall back to default labels such as `Field 001`.

    #### `device_metadata_fields`
    - Status: Active.
    - Purpose: Sparse per-device Agent Metadata Field values with timestamp conflict metadata.
    - Columns: `device_guid`, `field_number`, `field_key`, `value`, `modified_at`, `source`, `actor`, `created_at`, `updated_at`.
    - Constraints and indexes:
    - `(device_guid, field_number)` primary key.
    - `idx_device_metadata_fields_guid` on `device_guid`.
    - `idx_device_metadata_fields_number_value` on `(field_number, value)`.
    - Used by:
    - `/api/devices/<device_id>/metadata_fields*` endpoints.
    - `/api/agent/heartbeat` metadata sync.
    - `DeviceFilterMatcher` metadata-field criteria.
    - Notes:
    - Values are base64-encoded at rest, capped at 1024 decoded characters, and omitted from agent payloads after blank clears are acknowledged.

    #### `device_filter_sites`
    - Status: Active.
    - Purpose: Normalized site membership for saved device filters.
    - Columns: `filter_id`, `site_id`.
    - Constraints and indexes:
    - No dedicated primary key; uniqueness is maintained by filter-write paths.
    - Used by:
    - `/api/device_filters*` write paths.

    #### `watchdogs`
    - Status: Active.
    - Purpose: Saved watchdog policy definitions.
    - Columns: `id`, `name`, `description`, `archived`, `enabled`, `severity`, `match_mode`, `site_mode`, `criteria_json`, `actions_json`, `evaluation_interval_seconds`, `cooldown_seconds`, `auto_resolve_after_seconds`, `min_consecutive_matches`, `boot_grace_seconds`, `last_edited_by`, `created_at`, `updated_at`, `last_evaluated_at`.
    - Used by:
    - `/api/watchdogs*`.
    - Go `watchdogRuntime`.
    - Notes:
    - Watchdog definitions are saved independently from runtime state and incident history.
    - Saving a watchdog immediately triggers a fresh evaluation pass.

    #### `watchdog_sites`
    - Status: Active.
    - Purpose: Normalized site-scope rows for watchdogs with explicit site membership.
    - Columns: `watchdog_id`, `site_id`.
    - Used by:
    - Watchdog save paths.
    - RBAC visibility checks.

    #### `watchdog_targets`
    - Status: Active.
    - Purpose: Saved dynamic or explicit watchdog targets.
    - Columns: `id`, `watchdog_id`, `kind`, `target_json`, `created_at`.
    - Used by:
    - Watchdog target resolution.
    - Filter-backed device expansion.
    - Notes:
    - `kind` is `filter` or `device`.
    - `target_json` stores the normalized target payload used by runtime expansion.

    #### `watchdog_device_overrides`
    - Status: Active.
    - Purpose: Device-specific suppressions and disables for one watchdog/device pair.
    - Columns: `id`, `watchdog_id`, `device_guid`, `hostname`, `site_id`, `state`, `reason`, `created_by`, `created_at`, `expires_at`, `updated_at`.
    - Used by:
    - Device Watchdogs tab.
    - Alerts suppression flow.

    #### `watchdog_device_state`
    - Status: Active.
    - Purpose: Last-known per-device watchdog evaluation state.
    - Columns: `id`, `watchdog_id`, `device_guid`, `hostname`, `site_id`, `state`, `consecutive_matches`, `first_matched_at`, `clear_started_at`, `last_evaluated_at`, `last_matched_at`, `last_sample_json`, `current_incident_id`, `last_action_at`, `updated_at`.
    - Used by:
    - Watchdog list state summaries.
    - Device Watchdogs tab.
    - Incident reconciliation and cooldown logic.
    - Notes:
    - `state` can include `normal`, `pending`, `triggered`, `suppressed`, `disabled`, and `stale_data`.

    #### `watchdog_incidents`
    - Status: Active.
    - Purpose: Watchdog incident history and operator-facing alert queue rows.
    - Columns: `id`, `watchdog_id`, `device_guid`, `hostname`, `site_id`, `severity`, `state`, `title`, `message`, `sample_json`, `rule_summary_json`, `action_summary_json`, `opened_at`, `updated_at`, `resolved_at`, `resolution_reason`, `acknowledged_at`, `acknowledged_by`, `trigger_count`.
    - Used by:
    - `GET /api/watchdogs/incidents`.
    - Device Watchdogs tab.
    - Acknowledgement and auto-resolve flows.
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

    #### `device_patch_inventory`
    - Status: Active.
    - Purpose: Normalized Windows patch inventory for fleet audit and Device Summary patch views.
    - Columns: `id`, `device_guid`, `patch_key`, `kb`, `title`, `state`, `source`, `classification`, `severity`, `installed_on`, `published_at`, `captured_at`, `metadata_json`.
    - Constraints and indexes:
    - `id` autoincrement primary key.
    - `idx_device_patch_inventory_guid` on `device_guid`.
    - `idx_device_patch_inventory_patch_key` on `patch_key`.
    - `idx_device_patch_inventory_kb` on `kb`.
    - `idx_device_patch_inventory_state` on `state`.
    - `idx_device_patch_inventory_guid_state` on `(device_guid, state)`.
    - Used by:
    - `/api/agent/details` ingestion refresh when `details.patches` is present.
    - `GET /api/patches/audit`.
    - `GET /api/device/patches/<hostname>`.
    - `job_kind=patch_install` scheduled-job target resolution.
    - Notes:
    - Non-patch `/api/agent/details` payloads preserve existing patch rows.
    - Pending rows use `source=wua_pending`; installed rows use `source=wua_history` or `source=quick_fix_engineering`.
    - Pending metadata can include `is_downloaded`, `is_mandatory`, `requires_reboot`, `update_id`, and `revision_number`.
    - Ad-hoc install actions create scheduled jobs. Active enabled patch jobs are surfaced back onto patch rows as `active_install_job` to prevent duplicate deployment jobs for the same KB/patch identity.

    #### `patch_catalog_entries`
    - Status: Active.
    - Purpose: KB/update identity cache used by patch policy deferral fallback and policy evaluation.
    - Columns: `id`, `patch_key`, `kb`, `update_id`, `revision_number`, `title`, `classification`, `category`, `severity`, `published_at`, `first_seen_at`, `last_seen_at`, `metadata_json`.
    - Used by:
    - Patch policy evaluation before creating policy-driven patch install jobs.
    - Backup/restore durable patch state.

    #### `patch_policies`
    - Status: Active.
    - Purpose: Global, site, and device filter patch policy definitions.
    - Columns include: `id`, `name`, `policy_type`, `enabled`, `locked`, `role_scope`, `approval_mode`, `deferral_days`, `managed_update_mode`, install/reboot schedule fields, reboot force flag, and audit actor/timestamps.
    - Used by:
    - `/api/patches/policies*`.
    - Scheduler patch policy evaluation.
    - Backup/restore durable patch state.
    - Notes:
    - Engine DB init seeds two locked `policy_type='global'` policies, one `Workstation` and one `Server`. When no split global exists, Borealis clears legacy patch policy definitions/history/state before seeding the two baselines, but preserves `patch_catalog_entries` and `device_patch_inventory`.
    - Windows patch policies use `role_scope` as policy type. `Server` and `Workstation` are valid for save flows. `Both` may exist only as legacy helper semantics and is rejected by current policy validation. Windows Server `operating_system` or `device_type` containing `server` resolves to `Server`; other non-empty `device_type` values resolve to `Workstation`.
    - Site policy scope is stored in `patch_policy_sites`; device filter scope is stored in `patch_policy_targets`.
    - `patch_policy_exclusions` stores `unmanaged`, `frozen`, and `managed_override` coverage. Device hostname exclusions include `site_id` when no device GUID is present so duplicate hostnames across sites remain distinct. Exclusions still count as covered for conflict detection.
    - `patch_policy_rules` stores approve/block rules and `override_parent_block` confirmation. Approve and block rules inherit downward as baseline policy behavior; child approval of a parent block requires an explicit override flag.

    #### `patch_policy_runs`
    - Status: Active history.
    - Purpose: One row per policy evaluation occurrence. Excluded from backup/restore.
    - Notes:
    - Unique index on `(policy_id, scheduled_ts)` prevents duplicate policy runs per scheduler tick/occurrence.
    - Policy runs create normal `scheduled_jobs` rows with `job_kind=patch_install` so history remains in scheduled-job tables.

    #### `patch_policy_device_state`
    - Status: Active transient state.
    - Purpose: Latest effective policy/enforcement state by device. Excluded from backup/restore.
    - Notes:
    - Effective state metadata records the same-role policy hierarchy, inherited exclusion source policy, and exclusion override source policy for audit/UI display.

    ### Scheduling and Automation
    #### `scheduled_jobs`
    - Status: Active.
    - Purpose: Scheduled-job definitions.
    - Columns: `id`, `name`, `components_json`, `targets_json`, `schedule_type`, `start_ts`, `duration_stop_enabled`, `expiration`, `execution_context`, `credential_id`, `use_service_account`, `job_kind`, `enabled`, `created_at`, `updated_at`.
    - Constraints and indexes:
    - `id` autoincrement primary key.
    - Used by:
    - `/api/scheduled_jobs*` CRUD endpoints.
    - Scheduler background loop.
    - Notes:
    - `credential_id` is logical linkage to `credentials.id`; no FK constraint in schema.
    - `job_kind = automation` is normal scheduled automation. `job_kind = onboarding` is automatic local-network device enrollment. `job_kind = agent_maintenance` records on-demand Agent update and branch/channel switch requests. `job_kind = patch_install` records ad-hoc Windows patch install jobs created from Patch Management.
    - Onboarding jobs store discovery entries and exclusion entries inside the JSON `targets_json` `onboarding_scope` record. Inline `#` comments remain in these saved JSON entries and are stripped only when runtime parsing expands the target list. New onboarding target rows preserve the raw matching scope entry in `target_input` so comments such as `10.0.0.56 # LAB-AIO-01` can help correlate pending approvals back to the summary row. Agent branch, target platform, remote ports, Windows fallback methods, and per-job onboarding concurrency live in the JSON `components_json` `device_onboarding` record. No remote machine credential material is copied into either JSON payload.

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
    - Patch install rows use `component_kind = patch_install`, store the KB/title display name in `component_name`, and keep per-device state in normal run target/activity tables.

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

    #### `scheduled_job_onboarding_targets`
    - Status: Active.
    - Purpose: Per-target status for automatic local-network onboarding jobs.
    - Columns: `id`, `run_id`, `job_id`, `scheduled_ts`, `site_id`, `target_input`, `target_address`, `target_hostname`, `ssh_port`, `status`, `detail`, `stdout_snippet`, `stderr_snippet`, `approval_reference`, `created_at`, `updated_at`, `finished_at`.
    - Constraints and indexes:
    - `id` autoincrement primary key.
    - FK declared: `run_id -> scheduled_job_runs(id) ON DELETE CASCADE`.
    - FK declared: `job_id -> scheduled_jobs(id) ON DELETE CASCADE`.
    - `idx_onboarding_targets_run` on `run_id`.
    - `idx_onboarding_targets_job` on `(job_id, scheduled_ts)`.
    - `idx_onboarding_targets_status` on `status`.
    - Used by:
    - Automatic local-network onboarding runner.
    - Scheduled Jobs result summaries.
    - `/api/onboarding/jobs/<job_id>/targets`.
    - Notes:
    - stdout/stderr are sanitized snippets only. Stored machine and domain credentials remain in `credentials` and are not copied into onboarding attempts.
    - Approval status is not duplicated here. `/api/onboarding/jobs/<job_id>/targets` joins back to `device_approvals` by saved approval reference or onboarding context, then falls back through hostname/IP matching for legacy rows, and hydrates approval id/status for UI display and inline approval actions.

    #### `scheduled_job_onboarding_target_events`
    - Status: Active.
    - Purpose: Persistent per-target onboarding timeline used by the WebUI Detailed Breakdown table.
    - Columns: `id`, `target_row_id`, `run_id`, `job_id`, `status`, `task`, `detail`, `stdout_snippet`, `stderr_snippet`, `started_at`, `finished_at`, `created_at`, `updated_at`.
    - Constraints and indexes:
    - `id` autoincrement primary key.
    - FK declared: `target_row_id -> scheduled_job_onboarding_targets(id) ON DELETE CASCADE`.
    - FK declared: `run_id -> scheduled_job_runs(id) ON DELETE CASCADE`.
    - FK declared: `job_id -> scheduled_jobs(id) ON DELETE CASCADE`.
    - `idx_onboarding_target_events_target` on `(target_row_id, started_at)`.
    - `idx_onboarding_target_events_run` on `run_id`.
    - Used by:
    - Automatic local-network onboarding runner.
    - `/api/onboarding/jobs/<job_id>/targets`, nested under each target as `timeline` and `events`.
    - Notes:
    - Rows are appended when target task/status changes. Active prior rows are closed with `finished_at`; terminal rows are written as completed/failed/skipped snapshots.
    - stdout/stderr are sanitized snippets captured for the specific task event, not raw remote logs.

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
    - Patch install rows store one frozen device target per endpoint so operators can see install success, failure, timeout, and skipped/offline state in Scheduled Job history.

    #### `job_scheduler_work_items`
    - Status: Active.
    - Purpose: Durable work queue for `job-scheduler` and dynamic site-worker containers or pods.
    - Columns: `id`, `dedupe_key`, `kind`, `site_id`, `lane`, `job_id`, `run_id`, `target_id`, `payload_json`, `status`, `attempt_count`, `priority`, `available_at`, `lease_owner`, `lease_expires_at`, `heartbeat_at`, `worker_guid`, `container_name`, `error`, `created_at`, `updated_at`, `started_at`, `finished_at`.
    - Used by:
    - `job-scheduler` scheduled ticking and service-action dispatch.
    - Site-worker onboarding execution.
    - Notes:
    - Work kinds include `onboarding_run`, `scheduled_run`, `scheduled_workflow_run`, `agent_maintenance_run`, `patch_install_run`, and `service_action`.
    - `scheduled_run`, `scheduled_workflow_run`, `agent_maintenance_run`, and `patch_install_run` payloads include task-link metadata for Server Info and the Sites Active Site Workers canvas; secrets stay out of `payload_json`.
    - Credentials are not stored in `payload_json`; workers retrieve decrypted credential material from the internal API only while executing.
    - `lease_owner` plus `lease_expires_at` protect work from duplicate claims and allow stale work to be reclaimed.

    #### `job_scheduler_workers`
    - Status: Active.
    - Purpose: Worker lifecycle/status visibility for ephemeral site workers.
    - Columns: `worker_guid`, `container_name`, `site_id`, `status`, `started_at`, `last_seen_at`, `idle_since`, `stopped_at`, `current_lanes_json`, `claimed_count`, `task_links_json`, `docker_state`, `exit_code`, `created_at`, `updated_at`.
    - Used by:
    - Server Info Workers view.
    - Sites Active Site Workers canvas.
    - Task-scheduler worker reconciliation.
    - Notes:
    - Docker-backed worker container names use random UUIDs (`site-worker-<uuid>`). K3s bridge worker pod names use deterministic site slugs (`site-worker-<sanitized-site-name>`), while `worker_guid` remains the stable worker identity for route records, labels, and lifecycle reconciliation.
    - Terminal site-worker rows are lifecycle records, not job history. `job-scheduler` prunes stopped/lost site workers after `BOREALIS_WORKER_HISTORY_SECONDS` (default 60 seconds), and `/api/server/workers?history_seconds=60` hides old terminal rows even if legacy rows lack `stopped_at`.

    #### `job_scheduler_worker_routes`
    - Status: Active.
    - Purpose: Scheduler-owned route registry for active and recently terminal site-worker routes.
    - Columns: `worker_guid`, `site_id`, `container_name`, `route_name`, `route_path_prefix`, `route_file_path`, `upstream_scheme`, `upstream_host`, `upstream_port`, `status`, `generation`, `metadata_json`, `created_at`, `updated_at`, `retired_at`.
    - Used by:
    - `job-scheduler` site-worker route reconciliation, orchestrator-backed Docker reconcile, lost-worker detection, and worker-history pruning.
    - Future remote-operation session brokering that needs a stable worker route lookup without Docker inspection.
    - Notes:
    - `worker_guid` is the primary key and matches `job_scheduler_workers.worker_guid`.
    - Active route rows use status `active`; terminal route rows use `retired` or `lost`.
    - `generation` starts at `1` and increments only when route metadata or lifecycle status changes, not on every scheduler reconcile tick.
    - Route files are recorded as `Engine/Services/traefik-edge/config/dynamic/site-worker-<worker_guid>.yml` by default. M3 records route metadata only; later milestones write and authorize feature-specific route traffic.

    #### `job_scheduler_service_snapshots`
    - Status: Active.
    - Purpose: Last known Compose service visibility snapshot written by `job-scheduler` for Server Info when `api-backend` has no Docker socket.
    - Columns: `service_key`, `payload_json`, `updated_at`.
    - Used by:
    - `/api/server/overview` and `/api/server/services` fallback service rows.
    - Task-scheduler Compose reconciliation.
    - Notes:
    - `api-backend` reads this table only for display. Docker-backed service actions are queued into `job_scheduler_work_items`; `job-scheduler` claims them and asks `site-worker-orchestrator` to execute the allowlisted Docker work.

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
    - The runtime decrypts credential secrets on demand through `Data/Engine/Containers/api-backend/data/services/aegis_cipher.py`.
    - Operator-facing credential APIs now wait for bootstrap phase `login_required`; stale operator sessions no longer bypass the lock state after restart.
    - If `metadata_json` contains `aegis_secret_state = "reset_required"`, the record survived an Aegis force reset but one or more stored secret fields were intentionally destroyed and must be re-entered.

    #### `aegis_cipher_state`
    - Status: Active after the first Aegis Cipher setup.
    - Purpose: Singleton state row for the Engine-global Aegis KDF parameters and verification token.
    - Columns: `id`, `kdf_name`, `kdf_params_json`, `verification_token`, `created_at`, `updated_at`.
    - Constraints and indexes:
    - `id` primary key, with Borealis using `id = 1`.
    - Used by:
    - `Data/Engine/Containers/api-backend/data/services/aegis_cipher.py` setup, unlock, rotation, migration, and force-reset flows.
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
    - Columns: `id`, `username`, `display_name`, `password_sha512`, `role`, `last_login`, `created_at`, `updated_at`, `mfa_enabled`, `mfa_disabled`, `mfa_secret`, `auth_reset_required`, `auth_reset_at`, `auth_source`, `directory_provider_id`, `directory_subject`, `directory_domain`, `directory_dn`, `directory_groups_json`, `directory_last_sync_at`, `directory_disabled`, `directory_disabled_at`.
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
    - `auth_source='local'` uses Borealis password/passkey auth. `auth_source='directory'` uses an LDAP/AD provider, keeps Borealis TOTP MFA, and blocks local password/passkey management.
    - `directory_disabled=1` disables the JIT cache row and invalidates active sessions through request-time user revalidation.

    #### `directory_providers`
    - Status: Active.
    - Purpose: LDAP, LDAPS, and Active Directory provider configuration.
    - Columns: `id`, `name`, `provider_type`, `enabled`, `priority`, `domain_suffix`, `server_urls_json`, `host_overrides_json`, `use_ldaps`, `tls_required`, `tls_ca_pem`, `base_dn`, `bind_dn`, `bind_password_encrypted`, `user_search_filter`, `username_attribute`, `display_name_attribute`, `email_attribute`, `member_of_attribute`, `group_search_base_dn`, `nested_groups`, `kerberos_realm`, `kerberos_kdc`, `kerberos_keytab_encrypted`, `sync_interval_seconds`, `last_sync_at`, `last_sync_status`, `last_sync_message`, `last_test_at`, `last_test_status`, `last_test_message`, `created_at`, `updated_at`.
    - Constraints and indexes:
    - `id` autoincrement primary key.
    - `name` unique.
    - Index on `enabled, priority`.
    - Used by:
    - Directory Services admin APIs.
    - `/api/auth/login` directory credential-provider routing.
    - Notes:
    - Aegis protects `bind_password_encrypted`; `kerberos_*` columns are retained for legacy schema compatibility.
    - Providers must pass `/api/directory/providers/<id>/test` before enablement.
    - LDAPS is strict by default; optional PEM trust anchors supplement system trust.

    #### `directory_provider_group_mappings`
    - Status: Active.
    - Purpose: Allowed/admin group role mapping for directory-authenticated operators.
    - Columns: `id`, `provider_id`, `group_dn`, `role`, `created_at`, `updated_at`.
    - Constraints and indexes:
    - `id` autoincrement primary key.
    - Unique index on `provider_id, group_dn, role`.
    - Index on `provider_id`.
    - Used by:
    - Directory authentication role assignment and admission checks.
    - Notes:
    - `role='Admin'` grants Borealis Admin. `role='User'` grants Borealis User and acts as allowed-group membership.

    #### `directory_provider_site_mappings`
    - Status: Active.
    - Purpose: Directory user-group to Borealis site-scope mapping.
    - Columns: `id`, `provider_id`, `label`, `group_dns_json`, `site_ids_json`, `position`, `created_at`, `updated_at`.
    - Constraints and indexes:
    - `id` autoincrement primary key.
    - Index on `provider_id, position`.
    - Used by:
    - Directory Services provider editor Site Assignment tab.
    - Directory authentication JIT cache updates that replace `user_site_assignments` for non-admin directory users.
    - Notes:
    - Admin directory users remain globally scoped through role semantics.
    - User directory groups are unioned across matching mapping rows; each matching mapping contributes all configured `site_ids_json` entries.
    - Saving provider site mappings immediately reapplies site scope for cached directory users using their stored `directory_groups_json`; directory login and sync also refresh the assignments.

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
    - The bundled official snapshot is versioned under `Data/Engine/Containers/api-backend/data/Official_Assemblies/` as a seed snapshot and synced into `assemblies.official_assemblies` on startup.
    - Deploy-time schema setup and Go API startup both ensure the active `assemblies.*` tables exist before `/api/assemblies`, quick jobs, scheduled jobs, workflows, or watchdog remediation read them.

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

    ### Codex remediation workflow
    1. Reproduce the route or background loop that is suspected of exhausting the pool.
    2. Inspect `pg_stat_activity` and note whether the bad rows are `idle in transaction`, long-lived `active`, or simply a large number of healthy `idle` sessions.
    3. Read the corresponding service code and mark every line between the final SQL statement and `conn.close()`.
    4. Move all non-DB work out of the connection scope unless the logic requires transactional consistency.
    5. For repeated target resolution, build one device snapshot and pass it to downstream helpers instead of refetching inventory.
    6. For repeated integration lookups, add a small in-memory TTL cache rather than doing the lookup on every request.
    7. Re-run the route, verify the behavior, and check `pg_stat_activity` again.
    8. If the route is still heavy, then consider query-count reduction, bulk loading, or schema/index tuning. Do not jump to pool-size changes before the connection lifecycle is clean.
