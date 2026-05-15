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
- Onboarding jobs use `kind = onboarding_scope` targets. Scope entries accept single IPv4 addresses, IPv4 ranges, CIDR blocks, and FQDNs. Inline `#` comments and blank separator lines are preserved in the saved job definition so operators can organize scopes, but comments and empty lines are ignored while expanding targets. Optional `exclusions` entries use the same formats and are expanded as a blacklist before SSH attempts start. Borealis deduplicates expanded targets and enforces `BOREALIS_ONBOARDING_TARGET_CAP` (default `512`).

## Execution Flow
1) `job-scheduler` tick loads enabled jobs.
2) Each due occurrence resolves its targets once and creates `scheduled_job_runs` plus `scheduled_job_run_targets` rows.
3) Pending agent targets dispatch through the `api-backend` internal Socket.IO bridge when their host is online.
4) Device-local script runs emit quick job payloads with `scheduled_job_id`, `target_context`, and helper-routing metadata when the job targets current-user execution.
5) Scheduled script and Engine-side Ansible jobs create existing run/target history rows, then enqueue site-scoped `scheduled_run` work items for site workers to execute. Site workers still use the API internal bridge for live agent sockets, credentials, VPN prep, and workflow launch callbacks.
6) Engine-side Ansible jobs using `local`, `ssh`, or `winrm` keep one shared run row per playbook component, but worker items process site-scoped target subsets so large multi-site jobs do not execute inside `api-backend`.
7) Engine-side Ansible jobs using `ssh_individual` or `winrm_individual` create one `scheduled_job_runs` row per target host per playbook component, synthesize a one-host inventory for each row, and execute those rows through bounded site-worker dispatch.
7) Remote Ansible runs map Borealis inventory aliases to active WireGuard peer IPs and exclude devices that are not currently eligible.
8) Remote SSH/WinRM targets require an active WireGuard peer IP and an agent-side WireGuard readiness callback before they can enter the generated inventory. Borealis emits or refreshes the tunnel, waits for `/api/agent/vpn/ready` for the current tunnel and selected transport port, then dispatches. Standard SSH `22` is part of the default shell/VNC/SSH allowlist, while non-default SSH or WinRM ports are widened in addition to that baseline.
9) For `execution_context = ssh` and `ssh_individual`, Borealis defers host reachability, authentication, and task connectivity to Ansible itself instead of running scheduler-side SSH banner/session probes first. The Engine passes through the WireGuard peer IP plus the selected credential, and Ansible records unreachable/auth/task failures in the normal recap and per-device status surfaces.
10) Engine-side Ansible SSH runs isolate their SSH control sockets to a short per-run directory under `/tmp/ansible_controlplane` so one stuck or timed-out run cannot contaminate later runs and Linux Unix socket path limits stay well below the kernel cap.
11) Engine-side scheduled SSH runs also default `ansible_ssh_transfer_method` to `scp` so Borealis avoids both the earlier SFTP hangs and the later `piped`/`dd` upload stalls seen on some WireGuard-backed peers. Borealis also defaults `ansible_scp_extra_args` to `-O` for OpenSSH 9 compatibility. Override with `BOREALIS_SHARED_ANSIBLE_SSH_TRANSFER_METHOD` if an environment needs `smart`, `sftp`, `scp`, or `piped`, and use `BOREALIS_SHARED_ANSIBLE_SCP_EXTRA_ARGS` for SCP arg overrides.
12) SSH credentials may carry both an account password and an SSH private key. When both are present, Borealis probes key-only login per target first. If the key probe fails or is inconclusive, Borealis does one controlled password probe and writes password-only inventory only after that probe succeeds; otherwise it keeps key-only inventory for that host. This avoids sending the shared password through Ansible's normal retry path on key-capable hosts and prevents `sshpass` from stopping the whole run as an account-lockout guard.
13) For `execution_context = winrm` and `winrm_individual`, Borealis still does a lightweight Engine-side TCP preflight and excludes targets that fail it with `resolution_reason = remote_preflight_failed`. If no targets remain eligible, the affected run is recorded as `Skipped` with `skip_reason = no_eligible_targets`.
14) Workflow-backed scheduled jobs derive worker site scope from workflow target nodes. Device-list nodes use embedded `site_id` or device lookup; filter nodes use filter site scope; workflows without site-bearing target nodes use a manager/global work item.
15) Individual scheduled Ansible dispatch is bounded by persisted runner settings exposed in Server Info:
  - per-job limit: `job_concurrency_limit` (default `20`)
  - global limit: `global_concurrency_limit` (default `50`)
16) The Engine updates run status, activity links, and Ansible recap rows as results arrive.
17) If zero devices are resolved, the occurrence is recorded as `Skipped` with `skip_reason = no_devices_targeted`.

## Job Scheduler And Site Workers
- `job-scheduler` is a long-lived Engine container with Docker socket access. It owns scheduled ticking, queue lease reconciliation, service-action execution, and site-worker lifecycle.
- Site workers are dynamic containers named `site-worker-<uuid>`. They receive a site id, claim work for that site only, and exit after 60 seconds idle by default (`BOREALIS_SITE_WORKER_IDLE_TTL_SECONDS`).
- Worker lanes are site-local. Onboarding work claims the `onboarding` lane and honors the job's device onboarding concurrency. Scheduled automation claims the `scheduled_job` lane with up to 7 concurrent scheduled work items per site worker by default (`BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY`). Global workflow work with no site scope is claimed by the manager.
- Work items use Postgres leases. A worker heartbeat extends the lease; stale leases return to `queued` so another worker can reclaim them.
- Worker records are visible in Server Info under the Workers domain, the Sites page Active Site Workers tab, and through `GET /api/server/workers`. The long-lived manager heartbeats as `job-scheduler`; active/recent site workers expose site id, lanes, claimed counts, task links, and recent work-item status.
- VNC, interactive shell, live file browsing/transfers, live process actions, live service actions, and software refresh remain in `api-backend` for v1 because those paths are browser/session-affine.

## Automatic Device Onboarding
- Local-network device onboarding is scheduled through the same job service with `job_kind = onboarding`.
- Onboarding execution runs in a site worker, not in `api-backend`.
- The WebUI flow lives at `/jobs/onboarding/new`, is launched from the Sites page action rail, and creates jobs named like `Automatic Device Onboarding <SiteName>`.
- Operators select one site, target OS, one stored machine or domain credential, discovery scope entries, exclusion scope entries, agent install branch, remote ports, per-job device onboarding concurrency, and normal schedule options.
- Linux targets use SSH. At run time the Engine probes TCP/SSH, authenticates with the selected credential, confirms the target is Linux, downloads `Data/Agent/dist/linux-amd64/Agent` from the selected branch, stages it at `/opt/Borealis/Agent/Agent`, and runs it with `--server-url <engine_url> --site-enrollment-code <site_code> --install-service`.
- Windows targets use the same discovery/exclusion model. The Engine first tries SMB `ADMIN$` plus a temporary Remote Service Control Manager service, then a remote scheduled task, then WMI/DCOM process creation, then WinRM. The Engine stages Go `Agent.exe`, a payload bundle, and `bootstrapper-config.json` under `C:\Borealis`; `Agent.exe` handles native service/process semantics, existing-agent repair checks, support dependency setup, `config.json` creation, task registration, state/events, timeout logging, and process cleanup before running the Go runtime. If an existing agent has a valid Engine token and the `Borealis Agent` scheduled task is running, or the task accepts a start request and the Engine accepts the token even while Task Scheduler reports `Ready`, the EXE reports `Already Enrolled and Active`; the scheduler records the target as skipped because no new onboarding was performed. If the task is missing, cannot be started, or the Engine rejects the local token, the EXE reports `Unable to Repair Agent > Re-Deploying` and runs fresh onboarding. If all paths fail, the target records a manual-install-required failure because local security policy blocked remote enrollment.
- `Agent.exe` reports the remote hostname through onboarding state and events. The scheduler stores that value in `scheduled_job_onboarding_targets.target_hostname`, while preserving `target_address`, so the Onboarding Summary can render `address:port (hostname)` when the hostname is known. Machine markers remain in `Logs/Agent/bootstrap.log` for diagnostics but are not shown in operator-facing onboarding output.
- The Detailed Breakdown table intentionally collapses low-level logs into task-oriented rows in chronological order: `Spinning-Up Site-Worker Container`, `Establishing Connection to Remote Device`, `Connection Established using <protocol>`, `Uploading Agent.exe to Remote Device`, `Creating Windows Service to Run Agent.exe using <protocol/service>`, `Ensuring Windows Service is Running`, `Existing Agent Detected`, `Already Enrolled and Active`, `Successfully Repaired Agent`, `Unable to Repair Agent > Re-Deploying`, `Running Agent Bootstrap`, `Installing Agent Dependencies: <dependency>`, `Configuring Agent Runtime`, `Agent Ready and Awaiting Approval`, and `Device Enrollment Approved`. Agent.exe timeline JSONL events are replayed in order with their original timestamps so elapsed time reflects remote bootstrap work instead of Engine polling cadence. Repeated singleton rows such as `Running Agent Bootstrap` and duplicate `Connection Established using <protocol>` updates are collapsed while preserving StdOut/StdErr snippets for drilldown, and the table does not expose sorting or filtering because task order is meaningful.
- Wrong credential failures remain failed targets and render with the red key status icon in the onboarding tables so operators can distinguish authentication failures from generic runtime failures at a glance.
- Remote machine credentials stay only in existing Aegis-protected credential records. Onboarding target output is stored as sanitized snippets in `scheduled_job_onboarding_targets`; per-target task timeline snippets are stored in `scheduled_job_onboarding_target_events`.
- Successful remote installs do not auto-approve devices. Agents submit normal enrollment requests and remain in the existing approval queue. Remote onboarding writes `onboarding_context.json` into the Agent settings directory so the approval records `onboarding_job_id`, `onboarding_run_id`, and `onboarding_target`. The onboarding target endpoint hydrates pending target rows with current approval status, with hostname/IP fallback for older rows that predate context persistence, so approved or completed devices stop showing as `waiting_approval`.
- Windows SCM-based deployment may still report a service-control timeout if the remote Service Control Manager is slow. Borealis treats that timeout as recoverable, polls the staged EXE output/state files, and also accepts a matching approval record as proof that the installer reached the Engine.
- Re-deploying an onboarding job saves the current job definition, deletes prior run history for that job, creates a fresh immediate onboarding occurrence, and repopulates target status from the new run.
- Fan-out is bounded by each job's `onboarding_concurrency` component field (default `5`). Jobs without that field fall back to `BOREALIS_ONBOARDING_CONCURRENCY` (default `5`). Install command timeout is controlled by `BOREALIS_ONBOARDING_INSTALL_TIMEOUT_SECONDS` (default `900`), and Windows SMB/scheduled-task observation follows that value unless `BOREALIS_WINDOWS_ONBOARDING_OBSERVATION_TIMEOUT_SECONDS` is set.

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
- The job editor's Historical Runs tab groups `scheduled_job_runs` rows by occurrence timestamp, lists `Ran On` plus the overall summary status, and hydrates the standard device/output grid by calling `GET /api/scheduled_jobs/<job_id>/devices?occurrence=<timestamp>`.
- Old runs are purged during scheduler ticks.

## API Endpoints
- `GET /api/scheduled_jobs` (Token Authenticated) - list scheduled jobs.
- `POST /api/scheduled_jobs` (Token Authenticated) - create scheduled job.
- `GET /api/scheduled_jobs/<int:job_id>` (Token Authenticated) - get scheduled job.
- `PUT /api/scheduled_jobs/<int:job_id>` (Token Authenticated) - update scheduled job.
- `POST /api/scheduled_jobs/<int:job_id>/toggle` (Token Authenticated) - enable/disable.
- `POST /api/scheduled_jobs/<int:job_id>/rerun` (Token Authenticated) - queue a fresh immediate occurrence for an enabled job using the saved configuration.
- `DELETE /api/scheduled_jobs/<int:job_id>` (Token Authenticated) - delete scheduled job.
- `GET /api/scheduled_jobs/<int:job_id>/runs` (Token Authenticated) - run history.
- `GET /api/scheduled_jobs/<int:job_id>/devices` (Token Authenticated) - device results.
- `DELETE /api/scheduled_jobs/<int:job_id>/runs` (Token Authenticated) - clear run history.
- `POST /api/onboarding/jobs/<int:job_id>/redeploy` (Token Authenticated) - clear onboarding job history and start a fresh immediate onboarding run.
- `GET /api/onboarding/jobs/<int:job_id>/targets` (Token Authenticated) - onboarding target attempts for an occurrence, including approval context and persistent per-target timeline events with sanitized output snippets when available.
- `GET /api/server/workers?history_seconds=60` (Admin) - active and recent job-scheduler site worker state plus recent work items for visualizers.

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
- Job scheduler runtime: `Data/Engine/Containers/api-backend/data/services/job_scheduler/manager.py`.
- Site worker runtime: `Data/Engine/Containers/api-backend/data/services/job_scheduler/worker.py`.

### Core tables (Engine DB)
- `scheduled_jobs` - job definition, schedule, targets, execution context.
- `scheduled_job_runs` - per-run status, timestamps, error fields, skip reason.
- `scheduled_job_run_targets` - frozen occurrence target snapshot and originating filter links.
- `scheduled_job_run_activity` - links activity_history to scheduled runs.
- `scheduled_job_onboarding_targets` - per-target local-network onboarding attempt state and sanitized output.
- `scheduled_job_onboarding_target_events` - persistent per-target onboarding task timeline for Detailed Breakdown.
- `job_scheduler_work_items` - durable queued/running/completed work with Postgres leases. Current kinds include `onboarding_run`, `scheduled_run`, `scheduled_workflow_run`, and `service_action`.
- `job_scheduler_workers` - active/recent site-worker lifecycle snapshots.
- `job_scheduler_service_snapshots` - last known Compose service state from `job-scheduler` for Server Info when `api-backend` has no Docker socket.

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
- Site worker lifecycle rows are separate from scheduled job history. Terminal `site-worker-*` rows are kept for the worker canvas history window, then pruned by `job-scheduler`; default is 60 seconds via `BOREALIS_WORKER_HISTORY_SECONDS`.
- `GET /api/server/workers?history_seconds=60` uses the same history window for stopped/lost worker visibility, so old terminal rows with missing `stopped_at` no longer remain visible forever.

### Failure and retry notes
- The scheduler is designed to be resilient; it logs and continues on errors.
- Expired runs are marked `Timed Out` when they exceed the expiration window.
- Offline pending targets can age into `Expired`.
- Zero-target occurrences are stored as skipped instead of success or failure.

### UI touch points
- Scheduled job UI lives under `Data/Engine/Containers/webui-frontend/data/web-interface/src/Scheduling/`.
- The list page expects pagination and run history endpoints to respond quickly.
- The list page exposes `Re-Run Job` for one selected enabled job. It calls `/api/scheduled_jobs/<id>/rerun`, which records a new pending occurrence and lets the scheduler/site-worker lane execute the saved configuration.
- Session-target selection is currently an ad hoc quick-run concept; scheduled current-user jobs intentionally default to all active sessions until a dedicated scheduler UI is introduced.
- WebUI deep links:
- Create route: `/jobs/new`
- Edit route: `/jobs/<job_id>`
- Scheduled automation tab query keys: `job_name`, `assemblies`, `targets`, `schedule`, `execution_context`, `job_history` (edit mode only).
- Scheduled automation history subtab query keys: `current_run`, `historical_runs`, and `historical_run`.
- Automatic onboarding tab query keys: `job_name`, `scope`, `connection_method`, `schedule`, `discovered_devices` (edit mode only). Legacy `ssh_context` and `target_status` links still map to the current tabs.
- Route registration and URL preservation are implemented in `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/routes/router.jsx` plus `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/routes/paths.js`; component-level tab URL sync is implemented in `Data/Engine/Containers/webui-frontend/data/web-interface/src/Scheduling/Create_Job.jsx`.

### Debug checklist
- Jobs not running: check `Engine/Services/api-backend/logs/engine.log` and `Engine/Services/api-backend/logs/scheduled_jobs.log`.
- Run history empty: verify `scheduled_job_runs` table and quick job events.
- Filter target mismatch: inspect the saved filter payload, `scheduled_job_run_targets`, and matcher logic.
