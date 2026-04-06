# Scheduled Jobs
[Back to Docs Index](index.md) | [Index (HTML)](index.html)

## Purpose
Explain how Borealis schedules recurring jobs, targets devices, and records run history.

## Scheduler Overview
- Scheduler implementation lives in `Data/Engine/services/API/scheduled_jobs/job_scheduler.py`.
- It reads job definitions from PostgreSQL and emits quick job payloads over Socket.IO.
- Run history is stored in `scheduled_job_runs`, `scheduled_job_run_targets`, and `scheduled_job_run_activity` tables.

## Schedule Types
Supported schedule types (from the scheduler core):
- `immediately`
- `once`
- `every_5_minutes`
- `every_10_minutes`
- `every_15_minutes`
- `every_30_minutes`
- `every_hour`
- `daily`
- `weekly`
- `monthly`
- `yearly`

## Target Resolution
- Targets can be explicit hostnames or device filter definitions.
- The scheduler uses `DeviceFilterMatcher` to resolve filters to a point-in-time snapshot at occurrence start.
- Each occurrence freezes the resolved host list in `scheduled_job_run_targets`.
- Online snapshot logic is used only to decide when a pending target can dispatch, not to recalculate the occurrence target list.
- Engine-side Ansible jobs persist structured device targets with `device_guid`, `hostname`, and site context so duplicate hostnames across sites can be targeted safely.
- When an operator creates or edits a job, Borealis constrains targets to the operator's assigned sites before persistence.
- Filter targets created by operators persist `allowed_site_ids` alongside the filter reference so future scheduler runs stay inside the operator's approved site scope.

## Execution Flow
1) Scheduler tick loads enabled jobs.
2) Each due occurrence resolves its targets once and creates `scheduled_job_runs` plus `scheduled_job_run_targets` rows.
3) Pending targets dispatch when their host is online.
4) Device-local script runs emit quick job payloads with `scheduled_job_id` context.
5) Engine-side Ansible jobs using `local`, `ssh`, or `winrm` create one shared run row per playbook component, synthesize an ephemeral inventory, and execute directly on the Linux Engine.
6) Remote Ansible runs map Borealis inventory aliases to active WireGuard peer IPs and exclude devices that are not currently eligible.
7) The Engine updates run status, activity links, and Ansible recap rows as results arrive.
8) If zero devices are resolved, the occurrence is recorded as `Skipped` with `skip_reason = no_devices_targeted`.

## Execution Contexts
- `system` - runs on the agent as SYSTEM.
- `current_user` - runs on the agent in the logged-in user context.
- `local` - runs on the Linux Engine through the Engine-side Ansible runner and targets the Engine host alias directly.
- `ssh` - runs on the Linux Engine, synthesizes an ephemeral inventory, and targets SSH devices over the managed WireGuard network.
- `winrm` - runs on the Linux Engine, synthesizes an ephemeral inventory, and targets WinRM devices over the managed WireGuard network.
- For shared Ansible runs, the selected execution context is the transport source of truth. Borealis uses `ssh` or `winrm` based on that operator choice and gates targets on WireGuard reachability plus credential/service-account readiness, not on the device row's stored `connection_type`.

## Run History and Retention
- Run history is retained for `BOREALIS_JOB_HISTORY_DAYS` (default 30).
- Old runs are purged during scheduler ticks.

## API Endpoints
- `GET /api/scheduled_jobs` (Token Authenticated) - list scheduled jobs.
- `POST /api/scheduled_jobs` (Token Authenticated) - create scheduled job.
- `GET /api/scheduled_jobs/<int:job_id>` (Token Authenticated) - get scheduled job.
- `PUT /api/scheduled_jobs/<int:job_id>` (Token Authenticated) - update scheduled job.
- `POST /api/scheduled_jobs/<int:job_id>/toggle` (Token Authenticated) - enable/disable.
- `DELETE /api/scheduled_jobs/<int:job_id>` (Token Authenticated) - delete scheduled job.
- `GET /api/scheduled_jobs/<int:job_id>/runs` (Token Authenticated) - run history.
- `GET /api/scheduled_jobs/<int:job_id>/devices` (Token Authenticated) - device results.
- `DELETE /api/scheduled_jobs/<int:job_id>/runs` (Token Authenticated) - clear run history.

## Related Documentation
- [Assemblies and Quick Jobs](assemblies.md)
- [Device Management](device-management.md)
- [API Reference](api-reference.md)
- [Ansible Playbooks](features_to_implement/ansible_playbooks.md)

## Codex Agent (Detailed)
### Scheduler entry points
- API registration: `Data/Engine/services/API/scheduled_jobs/management.py`.
- Scheduler core: `Data/Engine/services/API/scheduled_jobs/job_scheduler.py`.
- Scheduler runner: `Data/Engine/services/API/scheduled_jobs/runner.py`.

### Core tables (Engine DB)
- `scheduled_jobs` - job definition, schedule, targets, execution context.
- `scheduled_job_runs` - per-run status, timestamps, error fields, skip reason.
- `scheduled_job_run_targets` - frozen occurrence target snapshot and originating filter links.
- `scheduled_job_run_activity` - links activity_history to scheduled runs.

### Schedule computation
- `_compute_next_run` normalizes timestamps to minutes and applies schedule type logic.
- `immediately` schedules once if the job never ran.
- `once` schedules at `start_ts` only once.

### Targeting logic
- Targets can be hostnames or device filters.
- `DeviceFilterMatcher` loads device snapshots and resolves filter matches.
- Operator-created filter targets carry persisted `allowed_site_ids`; matcher resolution must honor that saved site scope instead of treating the filter as globally visible.
- A due occurrence is resolved once, then reused for the rest of that occurrence.
- The scheduler can also request an online-only hostname snapshot when deciding whether a pending target can dispatch.

### Execution context
- Payloads are emitted as quick jobs with extra context:
  - `scheduled_job_id`
  - `scheduled_job_run_id`
  - `scheduled_ts`
- `quick_job_result` updates `scheduled_job_runs` and `activity_history`.
- `execution_context = local` is Engine-side only and runs the playbook directly on the Linux Engine against the localhost-style Engine target.
- `execution_context = ssh` and `execution_context = winrm` now run from the Linux Engine, synthesize per-run inventories, and target remote devices over the managed WireGuard network.
- Shared Ansible occurrences write one `scheduled_job_runs` row per playbook component and freeze one deduplicated target snapshot per resolved device in `scheduled_job_run_targets`.

### Retention and cleanup
- Retention defaults to 30 days and is configured by `BOREALIS_JOB_HISTORY_DAYS`.
- Purging is done inside the scheduler tick loop.

### Failure and retry notes
- The scheduler is designed to be resilient; it logs and continues on errors.
- Expired runs are marked `Timed Out` when they exceed the expiration window.
- Offline pending targets can age into `Expired`.
- Zero-target occurrences are stored as skipped instead of success or failure.

### UI touch points
- Scheduled job UI lives under `Data/Engine/web-interface/src/Scheduling/`.
- The list page expects pagination and run history endpoints to respond quickly.
- WebUI deep links:
- Create route: `/jobs/new`
- Edit route: `/jobs/<job_id>`
- Tab query keys: `job_name`, `assemblies`, `targets`, `schedule`, `execution_context`, `job_history` (edit mode only).
- Route registration and URL preservation are implemented in `Data/Engine/web-interface/src/app/routes/router.jsx` plus `Data/Engine/web-interface/src/app/routes/paths.js`; component-level tab URL sync is implemented in `Data/Engine/web-interface/src/Scheduling/Create_Job.jsx`.

### Debug checklist
- Jobs not running: check `Engine/Logs/engine.log` and `Engine/Logs/scheduled_jobs.log`.
- Run history empty: verify `scheduled_job_runs` table and quick job events.
- Filter target mismatch: inspect the saved filter payload, `scheduled_job_run_targets`, and matcher logic.
