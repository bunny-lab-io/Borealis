# Assemblies and Quick Jobs

## Purpose
Explain Borealis assemblies (script definitions), how they are stored, and how quick jobs execute them.

## Assemblies at a Glance
- Assemblies are script definitions stored in PostgreSQL assembly-domain tables.
- Domains include: `official`, `community`, and `user`.
- Payload JSON is stored inline in each `assemblies` row (`payload_json`).
- Assemblies are cached at runtime by the Engine and served via API.
- `assembly_type` is the canonical classifier used to route assemblies to the Script Editor, Workflow Editor, and execution paths.
- `assembly_subtype` captures implementation/runtime subtype (for example: `powershell`, `batch`, `bash`, `workflow`, `ansible`).
- The assembly table does not use `metadata_json` or `payload_type`.
- Engine startup enforces the compact `assemblies` schema exactly; legacy extra columns are rejected instead of auto-migrated.

## Authoring JSON
- Exported assembly JSON is now a clean authoring document, not a runtime metadata envelope.
- Borealis import accepts both wrapped legacy documents and direct authoring documents, but export now prefers the direct shape.
- For script assemblies, the intended authoring shape is:
  - `assembly_guid`
  - `name`
  - `description`
  - `type`
  - `script`
  - optional: `timeout_seconds`, `variables`, `files`
- For workflow assemblies, the intended authoring shape is:
  - `assembly_guid`
  - `name`
  - `description`
  - `type` = `workflow`
  - `workflow`
- The decoded `workflow` JSON payload should contain the flow-canvas body such as:
  - `tab_name`
  - `nodes`
  - `edges`
- Borealis still accepts legacy flat workflow exports where `tab_name`, `nodes`, and `edges` live at the top level, but canonical export now normalizes workflows into the encoded `workflow` field so assembly metadata and flow payload stay standardized.
- Borealis no longer needs authoring exports to carry `display_name`, `summary`, `category`, `sites`, `script_encoding`, `version`, `payload_guid`, `source_repo`, `source_version`, `content_hash`, or dirty/persisted queue metadata.
- When an operator refreshes the Aurora catalog, Borealis now treats a clean Aurora manifest as authoritative for the official domain and removes local official assemblies whose GUIDs were revoked upstream. That cleanup does not run against bundled or fallback manifests.

## Quick Jobs
- Quick jobs are immediate executions of a script assembly.
- The Engine resolves the script, signs it, and emits a Socket.IO `quick_job_run` event to the host's SYSTEM socket.
- The agent SYSTEM broker executes SYSTEM work locally and forwards current-user work into helper processes when `run_mode` targets the interactive user context.
- Agents return `quick_job_result` for status and output through the SYSTEM socket.
- Device Summary and Device List expose inline Quick Job pickers backed by `src/Assemblies/Assembly_Picker.jsx` and `src/Assemblies/Quick_Job_Dialog.jsx`. They search script assemblies in place, collect assembly variables when present, create saved immediate scheduled jobs for selected devices, and let the scheduler execute them without forcing the operator through the full Scheduled Job editor.

## Watchdog-Triggered Remediation
- Watchdogs can use assemblies as remediation actions.
- Borealis supports:
  - script assemblies through the same quick-job dispatch path used by operator-launched script runs
  - workflow assemblies through the workflow runtime's async run-start API
  - Ansible assemblies through the Engine-side Ansible runner in `local` execution context
- Watchdog `Run Assembly` actions expose assembly-defined runtime variables in the editor, similar to Scheduled Job assembly inputs.
- Script and Ansible watchdog actions apply those `variable_values` directly during execution.
- Workflow watchdog actions currently carry `variable_values` inside the trigger metadata so downstream workflow logic can inspect the launch context, but they do not yet have a dedicated workflow-input contract like scripts and playbooks.
- Watchdog-owned script remediations still create `activity_history` rows so the device activity UI and historical troubleshooting stay consistent.
- Workflow remediations carry source metadata that links the workflow run back to the triggering watchdog and incident.

## Activity History
- Quick job executions are tracked in `activity_history`.
- Operators can view or delete device activity history via API.

## Payload Size Limits
- Operator target: up to `500 MB` per assembly payload (`payload_json`) under normal resource conditions.
- Borealis import guardrail: `950,000,000` bytes per document.
- PostgreSQL stores the payload inline, but very large documents are still best-effort and depend on available Engine memory/CPU during JSON parse and DB write.

## Ansible Status (Current)
- The Linux Engine packages an Ansible control-node runtime inside the Engine venv.
- Engine startup stages `Data/Engine/Containers/api-backend/data/Ansible/collections.yml` into the runtime Ansible cache and installs any missing collections into `Engine/Services/api-backend/cache/Ansible/collections`, which is the same path used by scheduled playbook runs.
- Scheduled jobs support agent-side script execution for `execution_context = system` and `current_user`, plus Engine-side playbook execution for `execution_context = ssh`, `ssh_individual`, `winrm`, and `winrm_individual`.
- The scheduled-job editor now filters execution contexts by assembly domain so script assemblies cannot be mixed with Ansible playbooks in one job. Workflow-backed scheduled jobs continue to ignore scheduler-level execution context.
- The scheduled-job editor defaults new Ansible jobs to `ssh_individual` so normal fleet automation runs one target at a time with bounded concurrency, while `ssh` / `winrm` remain available for grouped multi-host playbooks that need a shared inventory context.
- `execution_context = local` still exists for internal Engine-side Ansible flows such as watchdog remediations, but it is no longer exposed in the scheduled-job editor.
- Remote SSH/WinRM runs synthesize Engine-side inventories from Borealis device state, credentials, and active WireGuard sessions. Grouped contexts build one shared inventory per playbook component, while individual contexts build one-host inventories so each target gets its own stdout/stderr, timeout window, and terminal status.
- Remote SSH/WinRM runs ensure the active WireGuard session allows the requested transport port before inventory execution. Standard SSH `22` is already part of the default shell/VNC/SSH allowlist, while non-default SSH or WinRM ports are widened in addition to that baseline. For scheduled jobs in `execution_context = ssh` and `ssh_individual`, Borealis now lets Ansible itself own SSH reachability and authentication outcomes instead of running scheduler-side SSH banner/session probes first. Engine-side SSH runs also isolate their SSH control sockets to a short per-run directory under `/tmp/ansible_controlplane`, and now default the file transfer method to `scp` with `-O` because some peers first hung in the SFTP subsystem and then also stalled in the `piped`/`dd` upload path. Override those transfer behaviors with `BOREALIS_SHARED_ANSIBLE_SSH_TRANSFER_METHOD` and `BOREALIS_SHARED_ANSIBLE_SCP_EXTRA_ARGS` if an environment needs a different transport mix. WinRM still keeps a lightweight Engine-side TCP preflight and excludes targets that fail it before launch. If none remain eligible, Borealis skips the affected run instead of waiting on Ansible transport retries.
- Individual scheduled Ansible fan-out is bounded by persisted runner settings stored under `Engine/Services/api-backend/config/ansible_runner_settings.json` by default and surfaced through Server Info. Borealis enforces both a per-job limit and a shared global limit whenever scheduled jobs dispatch Engine-side Ansible runners.

## API Endpoints
- `GET /api/assemblies` (Token Authenticated) - list assemblies.
- `GET /api/assemblies/<assembly_guid>` (Token Authenticated) - assembly details.
- `POST /api/assemblies` (Token Authenticated) - create assembly.
- `PUT /api/assemblies/<assembly_guid>` (Token Authenticated) - update assembly.
- `DELETE /api/assemblies/<assembly_guid>` (Token Authenticated) - delete assembly.
- `POST /api/assemblies/<assembly_guid>/clone` (Admin + Dev Mode for protected domains) - clone assembly.
- `POST /api/assemblies/dev-mode/switch` (Admin) - toggle dev mode.
- `POST /api/assemblies/dev-mode/write` (Admin + Dev Mode) - flush queued writes.
- `POST /api/assemblies/import` (Domain write permissions) - import legacy JSON.
- `GET /api/assemblies/<assembly_guid>/export` (Token Authenticated) - export legacy JSON.
- `POST /api/assemblies/<assembly_guid>/official-update` (Admin) - update one official assembly from the active catalog.
- `POST /api/assemblies/official/update-all` (Admin) - sync official assemblies from the active catalog, including brand-new Aurora entries that are not yet installed locally.
- `POST /api/scripts/quick_run` (Token Authenticated) - quick agent-side script job (`powershell`, `batch`, or `bash`, depending on the target agent platform/runtime), with optional current-user `session_target` and `target_session_id`.
- `GET /api/device/activity/<hostname>` (Token Authenticated) - device activity history.
- `DELETE /api/device/activity/<hostname>` (Token Authenticated) - clear history.
- `GET /api/device/activity/job/<int:job_id>` (Token Authenticated) - activity record.

## Related Documentation
- [Flow Editor and Nodes](flow-editor-and-nodes.md)
- [Scheduled Jobs](scheduled-jobs.md)
- [Security and Trust](../Engine%20Deployment/security-and-trust.md)
- [API Reference](../Data%20and%20Schema/api-reference.md)
- [SSH Connection Logic](SSH_Connection_Logic.md)
- [Watchdogs](watchdogs.md)
- [Device Alerts](../Operations%20and%20Remote%20Access/device-alerts.md)

??? example "Detailed Codex Breakdown"

    ### Storage layout and caching
    - Aurora (`https://github.com/bunny-lab-io/Aurora`) is the official assembly authoring source of truth.
    - Engine keeps a managed Aurora checkout under `Engine/Services/api-backend/cache/Aurora/` for update checks and imports.
    - Bundled official assemblies live under `Data/Engine/Containers/api-backend/data/Official_Assemblies/` as a generated seed snapshot for fresh installs and release packaging.
    - Startup seeds from the bundled snapshot and then attempts an Aurora git sync so official assemblies can be refreshed without relying on the bundled files alone.
    - Runtime assembly data lives in PostgreSQL `assemblies.official_assemblies`, `assemblies.community_assemblies`, and `assemblies.user_created_assemblies`.
    - The Engine loads and caches assemblies via `Data/Engine/Containers/api-backend/data/assembly_management` and `AssemblyRuntimeService`.
    - Official rows persist `source_repo`, `source_path`, `source_version`, and `content_hash` so GUID-based Aurora updates can be tracked without mirroring repo folders into PostgreSQL.

    ### Payload sizing guidance
    - Treat `500 MB` as the practical operator-facing target for a single assembly payload.
    - Runtime import limit remains `950,000,000` bytes as an application guardrail.

    ### Dev Mode behavior
    - User-created domain writes are allowed for authenticated operators.
    - Official/community domains require Admin + Dev Mode enabled.
    - Dev Mode state is tracked per session and expires after a TTL.
    - Use `/api/assemblies/dev-mode/switch` to toggle and `/api/assemblies/dev-mode/write` to flush.

    ### Quick job execution path
    1) Operator calls `/api/scripts/quick_run` with `script_path` or `assembly_guid`, plus `hostnames`.
    2) Engine resolves the assembly document (DB-backed or filesystem).
    3) Engine rewrites variable placeholders and signs the script with Ed25519.
    4) Engine creates `activity_history` rows, sets `target_context`, and emits `quick_job_run` over Socket.IO to the host's SYSTEM socket.
    5) The agent either executes the script in SYSTEM mode or forwards current-user work into one or more helper sessions, depending on `run_mode`, `session_target`, and `target_session_id`.
    6) The agent returns `quick_job_result` through the SYSTEM socket.
    7) Engine updates `activity_history` and emits `device_activity_changed`.

    ### Script variables and environment injection
    - Assembly variables are stored with name, type, default, and description.
    - Engine always builds an environment map for agent-side scripts.
    - PowerShell scripts also use literal rewrite for `$env:VAR` references before dispatch.
    - Batch and Bash scripts rely on the injected process environment on the agent side (`%VAR%` / `$VAR`).
    - Variables are included in the payload so agents can log context.

    ### Code signing
    - Script bytes are signed in `Data/Engine/Containers/api-backend/data/services/API/assemblies/execution.py`.
    - Agents verify signatures using `signature_utils` before execution.

    ### Activity history
    - `activity_history` stores script metadata, timestamps, status, stdout, stderr.
    - Use `/api/device/activity/<hostname>` to query or clear entries.

    ### Backup guidance
    - Back up PostgreSQL `assemblies.*` tables.
    - Back up `Data/Engine/Containers/api-backend/data/Official_Assemblies/` if you want the bundled official seed snapshot tracked with releases.

    ### PostgreSQL dump commands
    - Use these commands when you want a raw database export of all assemblies before reorganizing them into Aurora-friendly folders.

    ```bash
    . /opt/Borealis/Engine/database.env
    mkdir -p /opt/Borealis/Aurora/_db_exports
    psql "$BOREALIS_DATABASE_URL" -c "\copy (
      SELECT jsonb_build_object(
        'domain', domain,
        'assembly_guid', assembly_guid,
        'display_name', display_name,
        'summary', summary,
        'assembly_type', assembly_type,
        'assembly_subtype', assembly_subtype,
        'source_repo', source_repo,
        'source_path', source_path,
        'source_version', source_version,
        'content_hash', content_hash,
        'payload_json', payload_json::jsonb,
        'created_at', created_at,
        'updated_at', updated_at
      )::text
      FROM (
        SELECT 'official' AS domain, assembly_guid, display_name, summary, assembly_type, assembly_subtype, source_repo, source_path, source_version, content_hash, payload_json, created_at, updated_at
        FROM assemblies.official_assemblies
        UNION ALL
        SELECT 'community' AS domain, assembly_guid, display_name, summary, assembly_type, assembly_subtype, source_repo, source_path, source_version, content_hash, payload_json, created_at, updated_at
        FROM assemblies.community_assemblies
        UNION ALL
        SELECT 'user' AS domain, assembly_guid, display_name, summary, assembly_type, assembly_subtype, source_repo, source_path, source_version, content_hash, payload_json, created_at, updated_at
        FROM assemblies.user_created_assemblies
      ) AS assemblies_export
      ORDER BY domain, assembly_type, lower(coalesce(display_name, '')), assembly_guid
    ) TO '/opt/Borealis/Aurora/_db_exports/all_assemblies.jsonl'"
    ```

    ```bash
    . /opt/Borealis/Engine/database.env
    mkdir -p /opt/Borealis/Aurora/_db_exports
    psql "$BOREALIS_DATABASE_URL" -c "\copy (
      SELECT *
      FROM assemblies.official_assemblies
      ORDER BY assembly_type, lower(coalesce(display_name, '')), assembly_guid
    ) TO '/opt/Borealis/Aurora/_db_exports/official_assemblies.csv' CSV HEADER"
    psql "$BOREALIS_DATABASE_URL" -c "\copy (
      SELECT *
      FROM assemblies.community_assemblies
      ORDER BY assembly_type, lower(coalesce(display_name, '')), assembly_guid
    ) TO '/opt/Borealis/Aurora/_db_exports/community_assemblies.csv' CSV HEADER"
    psql "$BOREALIS_DATABASE_URL" -c "\copy (
      SELECT *
      FROM assemblies.user_created_assemblies
      ORDER BY assembly_type, lower(coalesce(display_name, '')), assembly_guid
    ) TO '/opt/Borealis/Aurora/_db_exports/user_created_assemblies.csv' CSV HEADER"
    ```

    ### Known limitations
    - Scheduled jobs are the supported playbook execution path in the Engine runtime.
    - Recap/report APIs and some recap-focused UI surfaces are still being fleshed out around the working Engine-side runner.
    - Linux agent support is incomplete; PowerShell scripts are Windows-first.

    ### Touch points to remember
    - API routes: `Data/Engine/Containers/api-backend/data/services/API/assemblies/`.
    - Assembly runtime: `Data/Engine/Containers/api-backend/data/services/assemblies/service.py` and `Data/Engine/Containers/api-backend/data/assembly_management/`.
    - UI editors: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Assemblies/`.
