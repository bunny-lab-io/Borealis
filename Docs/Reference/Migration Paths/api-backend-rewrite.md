# API Backend Rewrite

Track the worker-first migration that moves remote-operation ownership out of `api-backend` before the Go rewrite. Use this page as the handoff record between Codex sessions: update the active milestone, work log, validation result, and next safe resume point before stopping.

## Current Status

| Field | Value |
| --- | --- |
| Branch | `feature/rewrite-api-backend-in-golang` |
| PR | [#232](https://github.com/bunny-lab-io/Borealis/pull/232) |
| Active milestone | `M1: Runtime Dependency Split` |
| Last updated | 2026-05-30 |
| Next safe step | Redeploy Engine/job-scheduler/site-worker so source defaults apply, then rerun Ubuntu/Rocky Ansible Quick Job smoke with per-job runner limit `5` and site-worker scheduled lane concurrency `5`. |

## Tracker Rules

- Keep exactly one milestone marked `In Progress`.
- Update this page when a milestone starts, completes, blocks, or changes scope.
- Add a work-log row before every handoff so the next session has a safe resume point.
- Keep validation results tied to the milestone that produced them.
- Do not start the next milestone until the current milestone has a clear `Handoff Note`.

## Status Legend

| Status | Meaning |
| --- | --- |
| `Not Started` | No implementation work has begun. |
| `In Progress` | Work is active or ready for next Codex session. |
| `Done` | Exit criteria and validation are complete. |
| `Blocked` | Work cannot continue without operator decision or external change. |

## Milestone Summary

| Milestone | Status | Core migration |
| --- | --- | --- |
| `M0: Tracker + PR Setup` | `Done` | Create branch, tracker, index link, and draft PR. |
| `M1: Runtime Dependency Split` | `In Progress` | Move Ansible/runtime-heavy dependencies out of `api-backend`. |
| `M2: Traefik Dynamic Worker Routing` | `Not Started` | Hotload per-site-worker routes without Traefik recreate. |
| `M3: Site-Worker Route Registry` | `Not Started` | Track active worker route metadata in runtime registry. |
| `M4: Signed Remote-Op Sessions` | `Not Started` | Mint scoped tokens for direct browser-to-worker access. |
| `M5: Agent Ops Route Cutover` | `Not Started` | Move Agent remote-op socket target to site-worker. |
| `M6: Site-Worker Agent Socket.IO` | `Not Started` | Move Agent remote-op event ownership to site-worker. |
| `M7: Remote Shell` | `Not Started` | Move interactive shell broker path to site-worker. |
| `M8: Remote Desktop + Guacamole` | `Not Started` | Move VNC and Guacamole path to site-worker. |
| `M9: Remote File Management` | `Not Started` | Move remote file operations and transfer state to site-worker. |
| `M10: Process, Service, Software Ops` | `Not Started` | Move remaining live Agent task handlers to site-worker. |
| `M11: Agent Maintenance + Quick Jobs` | `Not Started` | Move live quick-job and maintenance dispatch to site-worker. |
| `M12: Ansible Execution Finalization` | `Not Started` | Ensure Ansible execution belongs to worker lane only. |
| `M13: api-backend Cleanup` | `Not Started` | Remove obsolete remote-op proxy code and stale dependencies. |
| `M14: Go Rewrite Prep` | `Not Started` | Freeze reduced Python API surface for Go rewrite design. |

## Milestone Definitions

### M0: Tracker + PR Setup

| Field | Definition |
| --- | --- |
| Status | `Done` |
| Goal | Create the long-lived branch, draft PR, tracker document, and migration-path index link. |
| Migrates | Nothing. This milestone creates the coordination surface for later migration work. |
| Out Of Scope | Runtime behavior changes, dependency changes, route changes, Agent changes, Go code. |
| Done When | Branch exists, draft PR exists, tracker is linked from the migration-path index, and initial roadmap is committed. |
| Validation | Inspect Markdown, verify index link, run `git diff --check`. |
| Handoff Note | `M1` is now active. Start with dependency ownership inventory before editing container requirements. |

### M1: Runtime Dependency Split

| Field | Definition |
| --- | --- |
| Status | `In Progress` |
| Goal | Stop `api-backend` from carrying Ansible and execution-heavy runtime dependencies. |
| Migrates | Ansible-only packages, execution helpers, and related container install burden from `api-backend` to worker runtime ownership. |
| Out Of Scope | WebUI behavior, Agent socket routing, remote shell, remote desktop, file management, Go rewrite. |
| Done When | `api-backend` no longer installs Ansible-only dependencies, `site-worker` still has required execution dependencies, and scheduled Ansible work still runs from worker lane. |
| Validation | Focused dependency/container config review, `docker compose -f Data/Engine/Containers/compose.yaml config`, affected Engine tests when practical. |
| Handoff Note | Operator smoke after Job 96/97 shows stable Ubuntu/Rocky Ansible Quick Job targets succeeding through site-workers. Default per-job Ansible runner limit and site-worker scheduled lane concurrency are both set to `5`; rerun smoke after redeploy before starting `M2`. |

### M2: Traefik Dynamic Worker Routing

| Field | Definition |
| --- | --- |
| Status | `Not Started` |
| Goal | Let Traefik hotload per-site-worker route files without recreating Traefik. |
| Migrates | Direct routing setup for worker-owned remote-operation endpoints. |
| Out Of Scope | Worker registry schema, operation-token authorization, Agent route cutover, remote-op feature migration. |
| Done When | Traefik uses a watched dynamic configuration directory, core routes stay intact, and per-worker route files can be added/removed atomically. |
| Validation | `docker compose -f Data/Engine/Containers/compose.yaml config`, generated Traefik YAML parse check if available, manual hotload smoke when runtime is available. |
| Handoff Note | Record route filename pattern and rollback command before starting `M3`. |

### M3: Site-Worker Route Registry

| Field | Definition |
| --- | --- |
| Status | `Not Started` |
| Goal | Track active site-worker route, port, generation, and status in a reliable runtime registry. |
| Migrates | Worker discovery from implicit Docker/container state to explicit scheduler-managed metadata. |
| Out Of Scope | Browser session tokens, Agent config changes, remote-op feature behavior. |
| Done When | Scheduler can create, update, query, and retire route records for active site-workers. |
| Validation | Focused scheduler tests, DB lifecycle review for short-lived connections, route-registry failure/restart scenario. |
| Handoff Note | Record registry table/structure and lifecycle owner before starting `M4`. |

### M4: Signed Remote-Op Sessions

| Field | Definition |
| --- | --- |
| Status | `Not Started` |
| Goal | Authorize direct browser-to-site-worker remote operations with short-lived scoped tokens. |
| Migrates | Remote-op authorization away from api-backend proxying and toward api-backend session brokering only. |
| Out Of Scope | Moving individual remote-op traffic paths, Agent socket target changes, Guacamole data path. |
| Done When | WebUI can request worker URLs plus signed operation token scoped to user, site, device, capability, and expiry. |
| Validation | Endpoint tests for scope/expiry/RBAC, token verification tests in worker code, unauthorized/expired-token manual checks. |
| Handoff Note | Record token issuer, audience, TTL, claims, and signing-key source before starting `M5`. |

### M5: Agent Ops Route Cutover

| Field | Definition |
| --- | --- |
| Status | `Not Started` |
| Goal | Make enrolled Agents connect to the site-worker ops route instead of api-backend `/socket.io/`. |
| Migrates | Agent remote-operation socket target selection. |
| Out Of Scope | Individual remote-op handler moves, shell/VNC/file behavior, Go rewrite. |
| Done When | Agent enrollment or refresh can deliver worker ops URL, Agent connects there, and legacy api-backend remote-op fallback is removed. |
| Validation | Agent unit tests for config/enrollment route data, Engine tests for route response, manual reconnect smoke with one enrolled Agent. |
| Handoff Note | Record Agent config key and cutover behavior before starting `M6`. |

### M6: Site-Worker Agent Socket.IO

| Field | Definition |
| --- | --- |
| Status | `Not Started` |
| Goal | Move Agent remote-operation socket registry and event dispatch from `api-backend` to `site-worker`. |
| Migrates | Agent connect/disconnect tracking, capability state, task event routing, remote-op response correlation. |
| Out Of Scope | Feature-specific shell/VNC/file/process behavior unless needed for socket ownership smoke. |
| Done When | Site-worker owns Agent session registry and can dispatch/receive generic remote-operation events without api-backend as middle-man. |
| Validation | Worker socket tests, Agent connect/disconnect smoke, event timeout/error-path tests. |
| Handoff Note | Record socket namespace/path and event contract before starting feature migrations in `M7` through `M11`. |

### M7: Remote Shell

| Field | Definition |
| --- | --- |
| Status | `Not Started` |
| Goal | Move interactive shell traffic and WireGuard shell TCP handling to site-worker. |
| Migrates | Shell open/send/resize/close, shell session state, and Engine-side TCP connection to Agent shell service. |
| Out Of Scope | Remote desktop, file management, process/service/software actions, Ansible execution. |
| Done When | WebUI shell uses direct site-worker path and `api-backend` no longer brokers shell traffic. |
| Validation | Manual shell open/send/close smoke, disconnect/reconnect scenario, authorization failure check, focused worker tests where available. |
| Handoff Note | Record worker route, WebSocket path, and cleanup behavior before starting `M8`. |

### M8: Remote Desktop + Guacamole

| Field | Definition |
| --- | --- |
| Status | `Not Started` |
| Goal | Move VNC orchestration and Guacamole connection path to site-worker. |
| Migrates | VNC start/stop/probe/credential request flow, VNC proxy ownership, and browser/Guacamole routing away from `api-backend`. |
| Out Of Scope | Remote shell, remote files, process/service/software actions, Go rewrite. |
| Done When | Browser remote desktop session connects through site-worker-managed route, and Guacamole no longer depends on api-backend VNC proxy code. |
| Validation | Manual remote desktop open/close smoke, credential-request flow, unavailable-host failure, Traefik route hotload check. |
| Handoff Note | Record Guacamole-to-worker connection method and fallback/error behavior before starting `M9`. |

### M9: Remote File Management

| Field | Definition |
| --- | --- |
| Status | `Not Started` |
| Goal | Move remote file operations and transfer state to site-worker. |
| Migrates | Browse, upload, folder upload, download, cancel, copy, cut, paste, rename, move, delete, create-folder, edit-text, transfer progress, and Agent file events. |
| Out Of Scope | Process/service/software operations, remote shell, remote desktop, Ansible execution. |
| Done When | File operations use direct site-worker routes and worker-local transfer state; api-backend no longer stores live transfer state. |
| Validation | Manual browse/upload/download/cancel smoke, large transfer progress check, expired-token check, focused file-operation tests where available. |
| Handoff Note | Record transfer state lifetime and cleanup behavior before starting `M10`. |

### M10: Process, Service, Software Ops

| Field | Definition |
| --- | --- |
| Status | `Not Started` |
| Goal | Move remaining live Agent management handlers to site-worker. |
| Migrates | Process list/control, service inventory/actions, software inventory/actions, and related live response events. |
| Out Of Scope | Scheduled inventory storage, long-term reporting, Ansible execution, Go rewrite. |
| Done When | Process, service, and software live operations run through site-worker and no longer depend on api-backend Agent socket registry. |
| Validation | Manual process/service/software smoke, RBAC/token denial checks, focused route/worker tests. |
| Handoff Note | Record any retained api-backend persistence endpoints before starting `M11`. |

### M11: Agent Maintenance + Quick Jobs

| Field | Definition |
| --- | --- |
| Status | `Not Started` |
| Goal | Move live quick-job and Agent maintenance dispatch that depends on the active Agent channel to site-worker. |
| Migrates | Quick job run event dispatch, maintenance task dispatch, and live Agent delivery response handling. |
| Out Of Scope | Scheduled job database ownership, job-scheduler manager lane, Ansible playbook execution finalization. |
| Done When | Live dispatch path uses site-worker while scheduler persistence and job history remain intact. |
| Validation | Quick-job smoke, Agent maintenance smoke, scheduler queue regression check. |
| Handoff Note | Record split between scheduler persistence and worker live dispatch before starting `M12`. |

### M12: Ansible Execution Finalization

| Field | Definition |
| --- | --- |
| Status | `Not Started` |
| Goal | Make worker lane the only owner of Engine-side Ansible execution. |
| Migrates | Manual and scheduled Ansible playbook execution paths that still run through `api-backend`. |
| Out Of Scope | Non-Ansible quick jobs, remote shell/desktop/files already handled by earlier milestones, Go rewrite. |
| Done When | `api-backend` has no Ansible execution path, worker lane runs scheduled/manual playbooks, and affected tests pass. |
| Validation | Ansible scheduled-job smoke, manual playbook smoke, focused Engine tests for job scheduler and Ansible runner. |
| Handoff Note | Record final Ansible ownership map before starting `M13`. |

### M13: api-backend Cleanup

| Field | Definition |
| --- | --- |
| Status | `Not Started` |
| Goal | Remove obsolete remote-operation proxy code and stale dependencies from `api-backend`. |
| Migrates | Nothing new. This milestone deletes old middle-man code after feature paths move. |
| Out Of Scope | New worker features, Agent protocol changes, Go implementation. |
| Done When | `api-backend` responsibility map contains API/auth/database/business logic only; remote-operation broker paths are removed or rejected. |
| Validation | Full affected Engine lane when practical, import/dependency scan, manual WebUI smoke for migrated paths. |
| Handoff Note | Record remaining Python API surface before starting `M14`. |

### M14: Go Rewrite Prep

| Field | Definition |
| --- | --- |
| Status | `Not Started` |
| Goal | Freeze the reduced Python `api-backend` surface so Go rewrite design can start cleanly. |
| Migrates | Planning boundary only. No runtime ownership moves in this milestone. |
| Out Of Scope | Writing Go production replacement, schema migrations unrelated to rewrite prep, UI changes. |
| Done When | Reduced API surface is documented, remaining Python responsibilities are categorized, and next Go design milestone has clear boundaries. |
| Validation | Source map review, endpoint inventory review, issue/PR notes updated. |
| Handoff Note | Start Go design work from frozen surface map instead of pre-migration assumptions. |

## Work Log

| Date | Milestone | Work performed | Validation | Evidence |
| --- | --- | --- | --- | --- |
| 2026-05-30 | `M1` | Set default scheduled Ansible per-job runner limit to `5` and default site-worker scheduled-job lane concurrency to `5`. Left global Ansible runner default at `50` and left onboarding/other worker lane behavior unchanged. Updated Server Info and Ansible playbook docs with concurrency defaults. | `python3 -m py_compile` passed; focused server-info/queue/scheduler pytest passed; `./Engine_Unit_Tests.sh --domain ansible` passed with test token root. | `Unit_Test_Results/engine-20260530T113906Z` |
| 2026-05-30 | `M1` | Reviewed operator Job 96/97 smoke. Job 96 produced 11 successes and 6 visible failures, with transient WireGuard failures no longer hidden as skipped/succeeded. Job 97 focused on Ubuntu/Rocky targets and reached 12 successes with only `lab-fog-01` still retrying WireGuard readiness. Confirmed Create Job only selects execution mode; Server Info Ansible runner limits gate scheduler dispatch; site-worker concurrency gates per-site worker claims. | Live PostgreSQL inspection. No source change needed. | Jobs 96 and 97 runtime rows. |
| 2026-05-30 | `M1` | Investigated operator-found Job 95 split result: 7 successes, 9 transient WireGuard skips, and 1 real SSH route failure. Confirmed Quick Job credential now persisted (`credential_id=1`) and retries helped some targets, but per-site worker concurrency of 7 flooded tunnel preparation and a 4-attempt retry budget let final WireGuard skips complete as successful work items. Reduced default site-worker scheduled concurrency to 3, increased transient retry budget to 8 attempts with 30 second delay, and changed exhausted transient tunnel-prep skips into failed runs/work items with visible errors. | `python3 -m py_compile` passed for touched scheduler files and queue test; focused queue/API pytest passed; `./Engine_Unit_Tests.sh --domain ansible` passed with test token root. | `Unit_Test_Results/engine-20260530T041923Z` |
| 2026-05-30 | `M1` | Fixed operator-found Job 91/93 and Job 94 regressions. Ansible Quick Job now requires an SSH credential in the dialog and API create/update validation rejects SSH Ansible without a stored credential. Site-worker now retries transient WireGuard preparation skips (`wireguard_unavailable`, `wireguard_not_ready`, `remote_preflight_failed`) instead of completing those work items as success. Dynamic site-worker logs now persist under host `Engine/Services/api-backend/logs/site-workers/`. | `python3 -m py_compile` passed; `git diff --check` passed; focused queue/API pytest passed; `./Engine_Unit_Tests.sh --domain ansible` passed with test token root. Full scheduler domain did not pass due unrelated existing onboarding test failure: `scheduled_job_module._onboarding_raw_input_map` missing. | `Unit_Test_Results/engine-20260530T023400Z`, `Unit_Test_Results/engine-20260530T023421Z` |
| 2026-05-30 | `M1` | Fixed operator-found Job 90 regression: stopped/lost site-workers now release claimed work back to `queued`, clear stale leases, and record requeue reason so scheduler can spawn/claim instead of leaving runs in limbo. | `python3 -m py_compile` passed; `Data/Engine/Unit_Tests/test_job_scheduler_queue.py` passed; `./Engine_Unit_Tests.sh --domain ansible` passed with test secret env. | `Unit_Test_Results/engine-20260530T015406Z` |
| 2026-05-30 | `M1` | Fixed operator-found worker lifecycle regression: site-worker now waits for scheduled run terminal status before completing work item or entering idle TTL, so Ansible daemon threads are not killed mid-run. | `py_compile` passed; `git diff --check` passed; `./Engine_Unit_Tests.sh --domain ansible` passed with test secret env. | `Unit_Test_Results/engine-20260530T013150Z` |
| 2026-05-30 | `M1` | Split base API requirements from worker Ansible requirements, removed Ansible collection staging from api-backend bootstrap, moved collection staging to job-scheduler/site-worker entrypoints, and mounted shared Ansible cache into dynamic site-workers. | `bash -n` entrypoints passed; `docker compose -f Data/Engine/Containers/compose.yaml config` passed; `git diff --check` passed; `./Engine_Unit_Tests.sh --domain ansible` passed with test secret env. | `Unit_Test_Results/engine-20260530T010620Z` |
| 2026-05-30 | `M1` | Updated SBOM source list for new worker requirement file. | Documentation inspection. | `Docs/Reference/SBOM.md` |
| 2026-05-30 | `M1` | Tried broader scheduler/core validation. Scheduler run was stopped after long-running `test_scheduled_jobs_api.py` showed many failures; core still has unrelated local edge-runtime expectation for dev UI port 5173 vs current 8000 default. | Not counted as M1 gate. | `Unit_Test_Results/engine-20260530T005538Z`, `Unit_Test_Results/engine-20260530T010410Z` |
| 2026-05-30 | `M0` | Opened draft PR and marked `M1` as next active milestone. | PR created from pushed branch. | [PR #232](https://github.com/bunny-lab-io/Borealis/pull/232) |
| 2026-05-30 | `M0` | Created tracker document and migration-path index link. | Markdown inspected; `git diff --check` passed. | This branch. |
| 2026-05-30 | Planning | Re-evaluated issue #226 and selected worker-first migration before Go rewrite. | Repository inspection only. | Issue #226 and planning notes. |

## Completed Work

- Issue #226 reviewed. Decision: move remote-operation execution paths into `site-worker` before rewriting `api-backend` in Go.
- Milestone tracker drafted with quota-sized work chunks.
- Branch `feature/rewrite-api-backend-in-golang` created and draft [PR #232](https://github.com/bunny-lab-io/Borealis/pull/232) opened.
- `M1` complete: api-backend no longer installs Ansible-only Python packages or stages Ansible collections; worker runtimes install `engine-worker-requirements.txt`, stage collections at startup, and share the Ansible cache with dynamic site-workers.
- `M1` stabilized after operator smoke test: site-worker no longer exits on idle TTL while scheduled Ansible run remains `Running`.
- `M1` stabilized after Job 90 smoke test: stopped/lost site-workers now requeue claimed work immediately instead of leaving work items leased to dead workers.
- `M1` staged fix after Job 91/93 smoke test: Ansible Quick Job now carries an SSH credential instead of creating `credential_id=NULL` jobs.
- `M1` staged fix after Job 94 smoke test: transient WireGuard readiness skips are requeued for retry instead of marked complete.
- `M1` staged fix after Job 95 smoke test: broad Quick Job Ansible runs now use lower per-worker concurrency, longer transient tunnel retry window, and fail visibly if WireGuard preparation never becomes ready.
- `M1` smoke after Job 96/97: stable Ubuntu/Rocky Ansible Quick Job targets are succeeding through site-workers; remaining concern is endpoint WireGuard readiness on specific experimental/unstable targets.
- `M1` concurrency defaults adjusted: scheduled Ansible per-job default is `5`; site-worker scheduled-job lane default is `5`; global Ansible runner default remains `50`.

## Remaining Work

- Redeploy and revalidate `M1` operator smoke tests, then mark `M1` done again.
- Complete `M2`, then `M3`, to prepare worker routing and registry foundation.
- Complete `M4` through `M6` to establish direct worker authorization and Agent socket ownership.
- Complete `M7` through `M11` to migrate each live remote-operation feature.
- Complete `M12` and `M13` to finalize Ansible ownership and clean `api-backend`.
- Complete `M14` before any Go rewrite implementation starts.

## Validation Matrix

| Check | Milestone | Status | Notes |
| --- | --- | --- | --- |
| Markdown and index link inspection | `M0` | `Done` | Doc-only validation. |
| `git diff --check` | `M0` | `Done` | Passed before first commit. |
| `docker compose -f Data/Engine/Containers/compose.yaml config` | `M1`, `M2` | `Done` for `M1` | Required after container or Traefik changes. |
| Focused Engine tests | `M1`-`M14` | `Done` for current `M1` fixes | Queue retry tests, scheduled job credential validation tests, and affected scheduler validation cases passed. |
| Full affected Engine lane | `M1`-`M14` | `Blocked` for scheduler domain | `ansible` domain passed. `scheduler` domain currently fails before touched cases on existing onboarding helper mismatch: `scheduled_job_module._onboarding_raw_input_map` missing. |
| Agent unit tests | `M5`, `M6` | `Not Started` | Required when Agent config or socket behavior changes. |
| Manual remote-op smoke | `M7`-`M11` | `Not Started` | Shell, desktop, files, process/service/software. |

??? example "Detailed Codex Breakdown"

    ### Related documentation

    - [Architecture Overview](../../Reference/architecture-overview.md)
    - [Docker Stack](../../Reference/docker-stack.md)
    - [Database Reference](../../Reference/database.md)
    - [Unit Testing](../../Reference/Unit_Testing.md)
    - [Scheduled Jobs](../../Using%20the%20Platform/scheduled-jobs.md)

    ### Source map

    - `Data/Engine/Containers/api-backend/data/` owns current Python API and many remote-operation paths.
    - `Data/Engine/services/job_scheduler/` owns scheduler manager, worker queue, and site-worker lifecycle code.
    - `Data/Engine/Containers/site-worker/` runs worker container entrypoint.
    - `Data/Engine/Containers/traefik-edge/` owns Traefik static and dynamic route generation.
    - `Data/Agent/` owns Agent enrollment, config, Socket.IO transport, and remote-operation handlers.

    ### Planned API endpoints

    - `POST /api/remote-ops/session` should authorize a user/device/capability request and return direct worker URLs plus a short-lived scoped operation token.
    - Agent enrollment, refresh, or a dedicated Agent route endpoint should return the site-worker ops base URL used by the Agent Socket.IO client.
    - Existing api-backend remote-operation proxy endpoints should be removed or converted to route-negotiation/session-broker endpoints only.

    ### Runtime behavior

    - `api-backend` remains API/auth/database/business-logic owner during worker-first migration.
    - `site-worker` becomes live remote-operation owner for Agent sockets, remote shell, remote desktop, remote file management, process/service/software operations, Agent maintenance, quick jobs that need live dispatch, and Ansible execution.
    - Traefik hotloads per-site-worker routes from dynamic files so worker route changes do not require Traefik container recreation.
    - Legacy api-backend remote-op fallback is intentionally not preserved after Agent ops route cutover.

    ### Debug flow

    - Start with this tracker and identify active milestone.
    - Read work-log rows newest first for last completed action and validation state.
    - For routing issues, inspect Traefik dynamic route files and worker registry state.
    - For Agent connection issues, inspect Agent ops URL delivery, Socket.IO path, and site-worker Agent registry.
    - For feature failures, debug the relevant milestone path before editing unrelated remote-operation code.
