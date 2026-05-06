# Scheduled Jobs
[Back to Docs Index](../index.md) | [Index (HTML)](../website/index.html)

## Purpose
Explain how Borealis schedules recurring jobs, targets devices, and records run history.

## Scheduler Overview
- Scheduler implementation lives in `Data/Engine/Containers/api-backend/data/services/API/scheduled_jobs/job_scheduler.py`.
- `job-scheduler` owns the scheduled tick loop in container deployments. `api-backend` keeps CRUD/status APIs and the live Socket.IO bridge.
- Site-scoped pressure work is queued in PostgreSQL and executed by ephemeral `site-worker-<uuid>` containers.
- Existing agent-side quick job dispatch still crosses `api-backend` because active agent sockets live there.
- Run history is stored in `scheduled_job_runs`, `scheduled_job_run_targets`, `scheduled_job_run_activity`, and, for automatic onboarding, `scheduled_job_onboarding_targets` plus `scheduled_job_onboarding_target_events`.
- Scheduled job definitions carry `job_kind`; normal jobs use `automation`, while local-network device enrollment uses `onboarding`.

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
- Onboarding jobs use `kind = onboarding_scope` targets. Scope entries accept single IPv4 addresses, IPv4 ranges, CIDR blocks, and FQDNs. Optional `exclusions` entries use the same formats and are expanded as a blacklist before SSH attempts start. Borealis deduplicates expanded targets and enforces `BOREALIS_ONBOARDING_TARGET_CAP` (default `512`).

## Execution Flow
1) `job-scheduler` tick loads enabled jobs.
2) Each due occurrence resolves its targets once and creates `scheduled_job_runs` plus `scheduled_job_run_targets` rows.
3) Pending agent targets dispatch through the `api-backend` internal Socket.IO bridge when their host is online.
4) Device-local script runs emit quick job payloads with `scheduled_job_id`, `target_context`, and helper-routing metadata when the job targets current-user execution.
5) Engine-side Ansible jobs using `local`, `ssh`, or `winrm` create one shared run row per playbook component, synthesize an ephemeral inventory, and execute in the Linux `job-scheduler` runtime.
6) Engine-side Ansible jobs using `ssh_individual` or `winrm_individual` create one `scheduled_job_runs` row per target host per playbook component, synthesize a one-host inventory for each row, and execute those rows with bounded concurrency in the Linux `job-scheduler` runtime.
7) Remote Ansible runs map Borealis inventory aliases to active WireGuard peer IPs and exclude devices that are not currently eligible.
8) Remote SSH/WinRM targets require an active WireGuard peer IP and an agent-side WireGuard readiness callback before they can enter the generated inventory. Borealis emits or refreshes the tunnel, waits for `/api/agent/vpn/ready` for the current tunnel and selected transport port, then dispatches. Standard SSH `22` is part of the default shell/VNC/SSH allowlist, while non-default SSH or WinRM ports are widened in addition to that baseline.
9) For `execution_context = ssh` and `ssh_individual`, Borealis defers host reachability, authentication, and task connectivity to Ansible itself instead of running scheduler-side SSH banner/session probes first. The Engine passes through the WireGuard peer IP plus the selected credential, and Ansible records unreachable/auth/task failures in the normal recap and per-device status surfaces.
10) Engine-side Ansible SSH runs isolate their SSH control sockets to a short per-run directory under `/tmp/ansible_controlplane` so one stuck or timed-out run cannot contaminate later runs and Linux Unix socket path limits stay well below the kernel cap.
11) Engine-side scheduled SSH runs also default `ansible_ssh_transfer_method` to `scp` so Borealis avoids both the earlier SFTP hangs and the later `piped`/`dd` upload stalls seen on some WireGuard-backed peers. Borealis also defaults `ansible_scp_extra_args` to `-O` for OpenSSH 9 compatibility. Override with `BOREALIS_SHARED_ANSIBLE_SSH_TRANSFER_METHOD` if an environment needs `smart`, `sftp`, `scp`, or `piped`, and use `BOREALIS_SHARED_ANSIBLE_SCP_EXTRA_ARGS` for SCP arg overrides.
12) SSH credentials may carry both an account password and an SSH private key. When both are present, Borealis probes key-only login per target first. If the key probe fails or is inconclusive, Borealis does one controlled password probe and writes password-only inventory only after that probe succeeds; otherwise it keeps key-only inventory for that host. This avoids sending the shared password through Ansible's normal retry path on key-capable hosts and prevents `sshpass` from stopping the whole run as an account-lockout guard.
13) For `execution_context = winrm` and `winrm_individual`, Borealis still does a lightweight Engine-side TCP preflight and excludes targets that fail it with `resolution_reason = remote_preflight_failed`. If no targets remain eligible, the affected run is recorded as `Skipped` with `skip_reason = no_eligible_targets`.
14) Individual scheduled Ansible dispatch is bounded by persisted runner settings exposed in Server Info:
  - per-job limit: `job_concurrency_limit` (default `20`)
  - global limit: `global_concurrency_limit` (default `50`)
15) The Engine updates run status, activity links, and Ansible recap rows as results arrive.
16) If zero devices are resolved, the occurrence is recorded as `Skipped` with `skip_reason = no_devices_targeted`.

## Job Scheduler And Site Workers
- `job-scheduler` is a long-lived Engine container with Docker socket access. It owns scheduled ticking, queue lease reconciliation, service-action execution, and site-worker lifecycle.
- Site workers are dynamic containers named `site-worker-<uuid>`. They receive a site id, claim work for that site only, and exit after 2 minutes idle.
- Worker lanes are site-local. Onboarding work claims the `onboarding` lane and honors the job's device onboarding concurrency. Scheduled ticking and Engine-side Ansible execution run in `job-scheduler` for v1 so they are still outside `api-backend`.
- Work items use Postgres leases. A worker heartbeat extends the lease; stale leases return to `queued` so another worker can reclaim them.
- Worker records are visible in Server Info under the Workers domain and through `GET /api/server/workers`.
- VNC, interactive shell, live file browsing/transfers, live process actions, live service actions, and software refresh remain in `api-backend` for v1 because those paths are browser/session-affine.

## Automatic Device Onboarding
- Local-network device onboarding is scheduled through the same job service with `job_kind = onboarding`.
- Onboarding execution runs in a site worker, not in `api-backend`.
- The WebUI flow lives at `/jobs/onboarding/new`, is launched from the Sites page action rail, and creates jobs named like `Automatic Device Onboarding <SiteName>`.
- Operators select one site, target OS, one stored machine or domain credential, discovery scope entries, exclusion scope entries, agent install branch, remote ports, per-job device onboarding concurrency, and normal schedule options.
- Linux targets use SSH. At run time the Engine probes TCP/SSH, authenticates with the selected credential, confirms the target is Linux, downloads `Agent.sh` from the selected branch, and runs `Agent.sh --repo-branch <branch> deploy --serverurl <engine_url> --enrollmentcode <site_code> --newEngine`.
- Windows targets use the same discovery/exclusion model. The Engine first tries SMB `ADMIN$` plus a temporary Remote Service Control Manager service, then a remote scheduled task, then WMI/DCOM process creation, then WinRM. The remote script syncs the selected branch into `C:\Borealis`, preserves the installed `C:\Borealis\Agent` runtime and onboarding temp/state, uses a host-wide onboarding mutex, stops `python.exe` / `pythonw.exe` processes with the same `Stop-Process` then `taskkill.exe` fallback used by `Update.ps1`, cleans stale per-run onboarding source folders/processes plus `BorealisOnboarding*` service/task artifacts, and skips duplicate attempts when a matching pending approval was already created in the last 24 hours. If all paths fail, the target records a manual-install-required failure because local security policy blocked remote enrollment.
- Remote machine credentials stay only in existing Aegis-protected credential records. Onboarding target output is stored as sanitized snippets in `scheduled_job_onboarding_targets`; per-target task timeline snippets are stored in `scheduled_job_onboarding_target_events`.
- Successful remote installs do not auto-approve devices. Agents submit normal enrollment requests and remain in the existing approval queue. When possible, Borealis records `onboarding_job_id`, `onboarding_run_id`, and `onboarding_target` on the approval. The onboarding target endpoint hydrates pending target rows with current approval status so approved or completed devices stop showing as `waiting_approval`.
- Windows SCM-based deployment may return a service-control timeout after the non-service PowerShell payload launches. Borealis treats that launch-state timeout as recoverable, polls the staged output file, and also accepts a matching approval record as proof that the installer reached the Engine.
- Re-deploying an onboarding job saves the current job definition, deletes prior run history for that job, creates a fresh immediate onboarding occurrence, and repopulates target status from the new run.
- Fan-out is bounded by each job's `onboarding_concurrency` component field (default `5`). Jobs without that field fall back to `BOREALIS_ONBOARDING_CONCURRENCY` (default `5`). Install command timeout is controlled by `BOREALIS_ONBOARDING_INSTALL_TIMEOUT_SECONDS` (default `900`).

## Execution Contexts
- `system` - runs on the agent as SYSTEM.
- `current_user` - runs through the host's SYSTEM socket and is forwarded by the agent broker into helper processes for active or locked user sessions.
- `ssh` - runs on the Linux Engine, synthesizes one shared inventory, and targets SSH devices over the managed WireGuard network as one grouped playbook run.
- `ssh_individual` - runs on the Linux Engine, synthesizes one-host inventories, and targets SSH devices individually with bounded concurrency.
- `winrm` - runs on the Linux Engine, synthesizes one shared inventory, and targets WinRM devices over the managed WireGuard network as one grouped playbook run.
- `winrm_individual` - runs on the Linux Engine, synthesizes one-host inventories, and targets WinRM devices individually with bounded concurrency.
- The scheduled-job editor now filters the dropdown by assembly domain:
  - script assemblies expose only `system` and `current_user`
  - Ansible playbook assemblies expose `ssh_individual`, `ssh`, `winrm_individual`, and `winrm`
  - the editor defaults new Ansible jobs to `ssh_individual`
  - workflow assemblies ignore scheduler-level execution context entirely
- The editor and API both reject jobs that mix script assemblies with Ansible playbook assemblies. Use separate jobs instead of mixing execution domains in one scheduled occurrence.
- `local` remains an internal Engine-side Ansible context for legacy/internal flows such as watchdog Ansible remediations, but it is no longer exposed in the scheduled-job editor.
- Scheduled current-user jobs default to `session_target = all_active_sessions`; scheduled jobs do not pin themselves to a raw Windows `session_id`.
- If no eligible interactive session exists for a current-user run, Borealis records the dispatch failure instead of silently falling back to SYSTEM.
- For shared Ansible runs, the selected execution context is the transport source of truth. Borealis uses `ssh` or `winrm` based on that operator choice and gates targets on WireGuard reachability plus credential/service-account readiness, not on the device row's stored `connection_type`.
- For individual Ansible runs, Borealis persists one run row per target host per playbook component so each device gets its own stdout/stderr, timeout, and terminal status instead of inheriting one shared batch result.
- Server Info exposes the live scheduled-Ansible runner budget through `GET/PUT /api/server/ansible-runner-settings`, and the scheduler applies those limits whenever it dispatches Engine-side scheduled Ansible runners.

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
- `POST /api/onboarding/jobs/<int:job_id>/redeploy` (Token Authenticated) - clear onboarding job history and start a fresh immediate onboarding run.
- `GET /api/onboarding/jobs/<int:job_id>/targets` (Token Authenticated) - onboarding target attempts for an occurrence, including approval context and persistent per-target timeline events with sanitized output snippets when available.
- `GET /api/server/workers` (Admin) - active and recent job-scheduler site worker state.

## Related Documentation
- [Assemblies and Quick Jobs](assemblies.md)
- [Ansible SSH Connection Logic](SSH_Connection_Logic.md)
- [Device Management](../Operations%20and%20Remote%20Access/device-management.md)
- [API Reference](../Data%20and%20Schema/api-reference.md)
- [SSH Connection Logic](SSH_Connection_Logic.md)

## Codex Agent (Detailed)
### Scheduler entry points
- API registration: `Data/Engine/Containers/api-backend/data/services/API/scheduled_jobs/management.py`.
- Scheduler core: `Data/Engine/Containers/api-backend/data/services/API/scheduled_jobs/job_scheduler.py`.
- Task scheduler runtime: `Data/Engine/Containers/api-backend/data/services/task_scheduler/manager.py`.
- Site worker runtime: `Data/Engine/Containers/api-backend/data/services/task_scheduler/worker.py`.

### Core tables (Engine DB)
- `scheduled_jobs` - job definition, schedule, targets, execution context.
- `scheduled_job_runs` - per-run status, timestamps, error fields, skip reason.
- `scheduled_job_run_targets` - frozen occurrence target snapshot and originating filter links.
- `scheduled_job_run_activity` - links activity_history to scheduled runs.
- `scheduled_job_onboarding_targets` - per-target local-network onboarding attempt state and sanitized output.
- `scheduled_job_onboarding_target_events` - persistent per-target onboarding task timeline for Detailed Breakdown.
- `task_scheduler_work_items` - durable queued/running/completed work with Postgres leases.
- `task_scheduler_workers` - active/recent site-worker lifecycle snapshots.
- `task_scheduler_service_snapshots` - last known Compose service state from `job-scheduler` for Server Info when `api-backend` has no Docker socket.

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
- Agent-side script payloads also carry:
  - `target_context` (`system` or `currentuser`)
  - `session_target` (`all_active_sessions` for scheduled current-user jobs)
  - `target_session_id` only when a specific session is explicitly chosen by an ad hoc caller
- `quick_job_result` updates `scheduled_job_runs` and `activity_history`.
- The scheduler does not speak to helpers directly; it targets the host's SYSTEM socket and lets the agent broker fan current-user runs out to helper-ready sessions.
- `execution_context = local` is Engine-side only and runs the playbook directly on the Linux Engine against the localhost-style Engine target.
- `execution_context = ssh` and `execution_context = winrm` run from the Linux Engine, synthesize shared per-run inventories, and target remote devices over the managed WireGuard network.
- `execution_context = ssh_individual` and `execution_context = winrm_individual` run from the Linux Engine, synthesize one-host inventories, and dispatch one run row per target host per playbook component with bounded concurrency.
- Scheduled-job authoring only exposes `system` / `current_user` for script assemblies and `ssh_individual` / `ssh` / `winrm_individual` / `winrm` for Ansible playbook assemblies. Mixing script and Ansible components is rejected in both the editor and API, while workflow-backed jobs continue to own their runtime inside the workflow document.
- Shared Ansible occurrences write one `scheduled_job_runs` row per playbook component and freeze one deduplicated target snapshot per resolved device in `scheduled_job_run_targets`.
- Individual Ansible occurrences write one `scheduled_job_runs` row per resolved target host per playbook component and freeze one `scheduled_job_run_targets` row per child run, which keeps device-level status/output isolated.
- Remote SSH targets are admitted when Borealis has a WireGuard peer IP, resolved credential material, and agent-side readiness for the active tunnel. Ansible itself owns SSH reachability and authentication outcomes after that point.
- Engine-side Ansible SSH runs isolate their SSH control sockets to a short per-run directory under `/tmp/ansible_controlplane` so stale multiplexed sessions do not bleed across retries or later jobs.
- Engine-side scheduled SSH runs also write `ansible_ssh_transfer_method = scp` by default because some peers first hung forever in the SFTP subsystem and then also stalled in the `piped`/`dd` module upload path. `BOREALIS_SHARED_ANSIBLE_SSH_TRANSFER_METHOD` can override that default, and `BOREALIS_SHARED_ANSIBLE_SCP_EXTRA_ARGS` defaults to `-O` when SCP is selected.
- Remote SSH/WinRM targets that do not report agent-side WireGuard readiness are marked `resolution_status = skipped` with `resolution_reason = wireguard_not_ready`.
- Remote WinRM targets that report WireGuard readiness but fail Engine-side TCP preflight are marked `resolution_status = skipped` with `resolution_reason = remote_preflight_failed` and are not forwarded into Ansible.
- Individual scheduled Ansible runner fan-out is bounded by persisted settings from `Data/Engine/Containers/api-backend/data/services/ansible/runtime_settings.py`, surfaced through Server Info, and enforced inside `_tick_once()` before dispatch.

### Retention and cleanup
- Retention defaults to 30 days and is configured by `BOREALIS_JOB_HISTORY_DAYS`.
- Purging is done inside the scheduler tick loop.

### Failure and retry notes
- The scheduler is designed to be resilient; it logs and continues on errors.
- Expired runs are marked `Timed Out` when they exceed the expiration window.
- Offline pending targets can age into `Expired`.
- Zero-target occurrences are stored as skipped instead of success or failure.

### UI touch points
- Scheduled job UI lives under `Data/Engine/Containers/webui-frontend/data/web-interface/src/Scheduling/`.
- The list page expects pagination and run history endpoints to respond quickly.
- Session-target selection is currently an ad hoc quick-run concept; scheduled current-user jobs intentionally default to all active sessions until a dedicated scheduler UI is introduced.
- WebUI deep links:
- Create route: `/jobs/new`
- Edit route: `/jobs/<job_id>`
- Scheduled automation tab query keys: `job_name`, `assemblies`, `targets`, `schedule`, `execution_context`, `job_history` (edit mode only).
- Automatic onboarding tab query keys: `job_name`, `scope`, `connection_method`, `schedule`, `discovered_devices` (edit mode only). Legacy `ssh_context` and `target_status` links still map to the current tabs.
- Route registration and URL preservation are implemented in `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/routes/router.jsx` plus `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/routes/paths.js`; component-level tab URL sync is implemented in `Data/Engine/Containers/webui-frontend/data/web-interface/src/Scheduling/Create_Job.jsx`.

### Debug checklist
- Jobs not running: check `Engine/Services/api-backend/logs/engine.log` and `Engine/Services/api-backend/logs/scheduled_jobs.log`.
- Run history empty: verify `scheduled_job_runs` table and quick job events.
- Filter target mismatch: inspect the saved filter payload, `scheduled_job_run_targets`, and matcher logic.
