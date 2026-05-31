# API Backend Rewrite

Track the worker-first migration that moves remote-operation ownership out of `api-backend` before the Go rewrite. Use this page as the handoff record between Codex sessions: update the active milestone, work log, validation result, and next safe resume point before stopping.

## Current Status

| Field | Value |
| --- | --- |
| Branch | `feature/rewrite-api-backend-in-golang` |
| PR | [#232](https://github.com/bunny-lab-io/Borealis/pull/232) |
| Active milestone | `M3: Site-Worker Route Registry` |
| Last updated | 2026-05-31 |
| Latest implementation commit | `b2c8e488` (`Implement site-worker route registry`) |
| Current state | `M3` route registry implementation is committed and locally validated. Scheduler creates, updates, queries, and retires `job_scheduler_worker_routes` rows; post-redeploy smoke is still required before marking `M3` done. |
| Next safe step | Redeploy latest branch head, run M3 route-registry smoke, then mark `M3` done and set `M4` as next safe step if smoke passes. |

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
| `M1: Runtime Dependency Split` | `Done` | Move Ansible/runtime-heavy dependencies out of `api-backend`. |
| `M2: Traefik Dynamic Worker Routing` | `Done` | Hotload per-site-worker routes without Traefik recreate. |
| `M3: Site-Worker Route Registry` | `In Progress` | Track active worker route metadata in runtime registry. |
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
| Status | `Done` |
| Goal | Stop `api-backend` from carrying Ansible and execution-heavy runtime dependencies. |
| Migrates | Ansible-only packages, execution helpers, and related container install burden from `api-backend` to worker runtime ownership. |
| Out Of Scope | WebUI behavior, Agent socket routing, remote shell, remote desktop, file management, Go rewrite. |
| Done When | `api-backend` no longer installs Ansible-only dependencies, `site-worker` still has required execution dependencies, and scheduled Ansible work still runs from worker lane. |
| Validation | Focused dependency/container config review, `docker compose -f Data/Engine/Containers/compose.yaml config`, affected Engine tests when practical. |
| Handoff Note | `M1` completed after post-redeploy checks. API, scheduler, site-worker, and WebUI image hashes match current source; served WebUI `adminRoutes` bundle includes Engine Status countdown and handoff code; Jobs 102/103 reached terminal results through site-workers. `M2` is now active. |

### M1 Current Handoff State

- Source state: branch `feature/rewrite-api-backend-in-golang`, PR [#232](https://github.com/bunny-lab-io/Borealis/pull/232), branch head `71c3dfb8`, latest implementation commit `65ba85c6`.
- Implementation state: API, scheduler, site-worker, and WebUI image hashes match current source after redeploy. The served WebUI `adminRoutes` bundle contains `Re-Deploying`, `Reassigning to New Worker`, terminal bucket text, and close countdown text. Jobs 102/103 terminal results are confirmed from PostgreSQL.
- Expected current-host profile: `MSP / Production` from `16` vCPU and `32.3 GiB` RAM.
- Expected tuned scheduled-lane capacity: `12` active scheduled work items per site-worker.
- Slot semantics: site-worker capacity limits active scheduled work items, not raw devices. Shared Ansible batches consume one slot per site/component batch and may include several devices inside one Ansible process. Individual Ansible consumes one slot per target while active, but Engine Status may group same-job, same-status runs into one `Task (n Devices)` card.
- Server Info expected behavior: Site Worker Scheduled Tasks row is read-only, shows deployment-profile managed value, and no longer provides edit/save controls.
- Removed API behavior: `PUT /api/server/site-worker-settings` is removed. `GET /api/server/site-worker-settings` remains and returns read-only profile-managed payload.
- Scheduler expectation: legacy Ansible per-job/global runner limits remain API-compatible but no longer gate dispatch. Site-worker scheduled lane owns task throughput.
- M1 close condition: post-redeploy smoke confirms Ansible still runs through site-worker, Engine Status nodegraph is readable/accurate, and profile-tuned concurrency behaves as expected.

### M2: Traefik Dynamic Worker Routing

| Field | Definition |
| --- | --- |
| Status | `Done` |
| Goal | Let Traefik hotload per-site-worker route files without recreating Traefik. |
| Migrates | Direct routing setup for worker-owned remote-operation endpoints. |
| Out Of Scope | Worker registry schema, operation-token authorization, Agent route cutover, remote-op feature migration. |
| Done When | Traefik uses a watched dynamic configuration directory, core routes stay intact, and per-worker route files can be added/removed atomically. |
| Validation | `docker compose -f Data/Engine/Containers/compose.yaml config`, generated Traefik YAML parse check if available, manual hotload smoke when runtime is available. |
| Handoff Note | Route directory is `Engine/Services/traefik-edge/config/dynamic/`. Core routes render to `core.yml`. Future site-worker routes must use `site-worker-<worker_guid>.yml` in that directory; write `.site-worker-<worker_guid>.yml.tmp` in the same directory, then rename to the final filename so Traefik sees an atomic add/update. Roll back worker routes with `rm -f Engine/Services/traefik-edge/config/dynamic/site-worker-*.yml`; reload Traefik with `bash Engine.sh --service traefik-edge reload prod` only if runtime file watching is unhealthy. Post-redeploy smoke passed on 2026-05-31: temporary route returned API health, removal stopped that route, and Traefik PID/start time stayed unchanged. |

### M3: Site-Worker Route Registry

| Field | Definition |
| --- | --- |
| Status | `In Progress` |
| Goal | Track active site-worker route, port, generation, and status in a reliable runtime registry. |
| Migrates | Worker discovery from implicit Docker/container state to explicit scheduler-managed metadata. |
| Out Of Scope | Browser session tokens, Agent config changes, remote-op feature behavior. |
| Done When | Scheduler can create, update, query, and retire route records for active site-workers. |
| Validation | Focused scheduler tests, DB lifecycle review for short-lived connections, route-registry failure/restart scenario. |
| Handoff Note | Implementation commit `b2c8e488` adds `job_scheduler_worker_routes`. Lifecycle owner is `job-scheduler`: register/spawn/reconcile create active rows, route upserts increment `generation` only when metadata or lifecycle status changes, stopped workers retire routes, lost workers mark routes `lost`, and worker-history pruning removes old terminal routes. Route files are metadata-only in M3 and default to `Engine/Services/traefik-edge/config/dynamic/site-worker-<worker_guid>.yml`; M4 must not begin until post-redeploy smoke confirms this table is created and populated in runtime. |

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
| 2026-05-31 | `M3` | Implemented scheduler-owned site-worker route registry. Added `job_scheduler_worker_routes` with active/retired/lost lifecycle states, route file/path/upstream metadata, `generation`, and metadata JSON. `register_worker`, Docker reconcile, `stop_worker`, lost-worker detection, and worker-history pruning now create, refresh, retire, or prune route records. Public queue helpers can upsert, query by worker, query active route by site, list routes, and retire route records. | `python3 -m py_compile` passed for scheduler queue/manager and focused queue tests. Focused `test_job_scheduler_queue.py` passed (`12 passed`). `git diff --check` passed. `./Engine_Unit_Tests.sh --domain scheduler` with system Python failed before tests because `pytest` is missing; rerun with the repo test venv entered unrelated long-running `test_scheduled_jobs_api.py` failures and was stopped, so it is not counted as an M3 gate. Post-redeploy runtime smoke remains pending. | `b2c8e488`; `Data/Engine/Containers/api-backend/data/services/job_scheduler/queue.py`; `Data/Engine/Unit_Tests/test_job_scheduler_queue.py`; `Docs/Reference/Data and Schema/db-reference.md`; `Unit_Test_Results/engine-20260531T063347Z`; `Unit_Test_Results/engine-20260531T063404Z`. |
| 2026-05-31 | `M2` | Closed M2 after redeploy of the route-directory permission fix. Runtime `config/dynamic/` is operator-writable as `nicole:nicole 0775`; generated static config watches that directory; `core.yml` remains loaded; temporary `site-worker-m2-hotload-validation.yml` was added atomically, served `/__borealis_m2_hotload` through Traefik to API `/health`, then removed cleanly. Traefik process stayed on the same PID/start time through add/remove. | Runtime config parse passed. API `/health` and WebUI loopback smoke passed. Hotload add returned `{"status":"ok"}`. Hotload removal returned non-200 (`301` through the normal HTTP redirect route). Traefik process stayed `2240940 Sun May 31 00:12:24 2026`. Temporary route file cleanup verified. | Runtime `Settings.json`, `traefik.yml`, `dynamic/core.yml`; Traefik process table; hotload validation route output. |
| 2026-05-31 | `M2` | Ran post-redeploy M2 read-only smoke. Generated Traefik static config points `providers.file.directory` at `Engine/Services/traefik-edge/config/dynamic`, `watch` is enabled, `core.yml` exists, and live API/WebUI routes answer through current core routes. Hotload add/remove smoke was blocked because `config/dynamic/` was `root:root 0755` from deploy. Staged fix: `Engine.sh` now records runtime owner UID/GID, `ensure_service_tree` and `traefik-edge` entrypoint keep the watched dynamic directory owner-writable as `0775`, and `compose.env.example` carries safe root defaults. | `bash -n Engine.sh`, `bash -n Data/Engine/Containers/traefik-edge/entrypoint.sh`, `python3 -m py_compile`, `docker compose --env-file Data/Engine/Containers/compose.env.example -f Data/Engine/Containers/compose.yaml config`, focused `test_edge_runtime.py`, and `git diff --check` passed. Runtime hotload remains pending redeploy of this permission fix. | Runtime `Settings.json`, `traefik.yml`, and `dynamic/core.yml`; current Traefik process started 2026-05-31 00:02:03 America/Denver; route write attempt returned permission denied before fix. |
| 2026-05-30 | `M2` | Implemented Traefik watched dynamic directory support. `traefik-edge` now renders static config with `providers.file.directory`, keeps core routes in `config/dynamic/core.yml`, preserves current API/WebUI/VNC routers, and publishes the site-worker route filename pattern in `Settings.json`. `Engine.sh`, `compose.env.example`, and Python edge-runtime artifact generation now use the same dynamic directory layout and migrate legacy `dynamic.yml` settings to `dynamic/core.yml`. | `bash -n Engine.sh`, `bash -n Data/Engine/Containers/traefik-edge/entrypoint.sh`, `python3 -m py_compile` for touched Python files, `docker compose --env-file Data/Engine/Containers/compose.env.example -f Data/Engine/Containers/compose.yaml config`, generated Traefik YAML parse checks for Python and shell renderers, focused `test_edge_runtime.py`, and `git diff --check` passed. Full `core` lane remains blocked by runtime secret permission errors outside the touched edge tests; `test_edge_runtime.py` passes inside that lane. | `Data/Engine/Containers/traefik-edge/entrypoint.sh`; `Data/Engine/Containers/api-backend/data/edge_runtime.py`; `Data/Engine/Unit_Tests/test_edge_runtime.py`; `Unit_Test_Results/engine-20260530T233846Z`. |
| 2026-05-30 | `M1` | Closed M1 after redeploy. Runtime image manifest now matches current source for `api-backend`, `job-scheduler`, `site-worker`, and `webui-frontend`. Served WebUI `adminRoutes` bundle contains Engine Status terminal countdown and orphaned worker handoff strings. Jobs 102/103 remain terminal in PostgreSQL: Job 102 `Success`; Job 103 seven `Success` runs plus one endpoint-specific SSH auth `Failed` run on `lab-mail-02`. | `git diff --check` passed. Live API `/health` returned `ok`. `./Engine_Unit_Tests.sh --domain webui` remains blocked because runtime WebUI unit tests are missing at `Engine/Services/webui-frontend/cache/web-interface/Unit_Tests`, but prod WebUI image hash and served bundle were validated. | `Engine/Deploy/image-manifest.json`; served `/assets/adminRoutes-C4ohTIET.js`; `Unit_Test_Results/engine-20260530T232746Z`; runtime DB rows for Jobs 102/103. |
| 2026-05-30 | `M1` | Pulled branch head `71c3dfb8` and checked runtime state. API, job-scheduler, and site-worker service hashes match current source, but deployed WebUI hash/source does not include latest Engine Status countdown/handoff changes. PostgreSQL confirms Job 102 finished `Success`; Job 103 finished with 7 `Success` runs and 1 endpoint-specific SSH auth `Failed` run on `lab-mail-02`. No Jobs 102/103 work items remain queued, claimed, or running. | `git diff --check` passed before edit. `./Engine_Unit_Tests.sh --domain webui` remained blocked because runtime WebUI unit tests are missing at `Engine/Services/webui-frontend/cache/web-interface/Unit_Tests`. Live API `/health` returned `ok`; DB reads were short-lived. Local redeploy attempt failed because Docker daemon access is denied to this shell and sudo requires a password. | `Unit_Test_Results/engine-20260530T223324Z`; runtime DB rows for Jobs 102/103; `./Engine.sh deploy prod` blocked locally. |
| 2026-05-30 | `M1` | Fixed Engine Status terminal task visibility. Success, failed, and skipped task buckets now show a 30-second countdown in the status pill, aggregate matching terminal work into the same bucket, and reset the timer when newer terminal work arrives. Orphaned queued/running work with no active same-site worker now marks the worker lane `Re-Deploying` while task groups show `Reassigning to New Worker`. | `git diff --check` passed. `./Engine_Unit_Tests.sh --domain webui` could not run because runtime WebUI unit tests are missing at `Engine/Services/webui-frontend/cache/web-interface/Unit_Tests`. | `Unit_Test_Results/engine-20260530T221946Z` |
| 2026-05-30 | `M1` | Recorded post-redeploy smoke: Server Info shows read-only Site Worker Scheduled Tasks, Engine appears healthy, and Jobs 102/103 produced two running `Task (8 Devices)` cards on the same site-worker. Code review confirms this is not automatically a `12`-slot violation because Engine Status labels target count separately from scheduled work-item count. Shared Ansible uses one slot for a multi-device batch; individual Ansible one-target runs can be grouped visually. | Documentation update and source review. Direct DB verification was blocked locally because runtime env files are root-owned and Docker socket access is not available to this shell. | Operator screenshot for Jobs 102/103. |
| 2026-05-30 | `M1` | Refreshed tracker for fresh Codex handoff. Added branch head, M1 handoff state, explicit post-redeploy smoke gates, and fresh-session prompt so next agent can resume without replaying the full conversation. | Documentation update; `git diff --check` passed. | This branch. |
| 2026-05-30 | `M1` | Restored profile-based Engine tuning. `Engine.sh deploy` now detects host vCPU/RAM, writes deployment profile metadata, DB pool values, PostgreSQL startup settings, and profile-managed site-worker scheduled task concurrency. Server Info now shows scheduled-lane capacity as read-only profile data; site-worker settings `PUT` route was removed. | `bash -n Engine.sh`, `python3 -m py_compile` for Server Info API, Compose config with example env, and `git diff --check` passed. Official `server` and `ansible` test lanes could not run on this host because local Python is missing `pytest`; runtime compose-env config could not run because `Engine/Deploy/compose.env` is root-owned. | `Unit_Test_Results/engine-server-profile-tuning`, `Unit_Test_Results/engine-ansible-profile-tuning` |
| 2026-05-30 | `M1` | Updated Engine Status task group presentation to match scheduler status buckets: `Task (n Devices)` headers with `Success`, `Running`, `Pending`, `Failed`, or `Skipped` pills. Terminal task groups now remain visible for the 60-second worker history window. | `git diff --check` passed. `./Engine_Unit_Tests.sh --domain webui` could not run because runtime WebUI unit-test cache is missing on this host. | `Unit_Test_Results/engine-20260530T195346Z` |
| 2026-05-30 | `M1` | Fixed Engine Status visual inconsistency from issue #233. Running/queued work no longer anchors to stopped/lost site-worker nodes; stale active work rows are dropped during held-inactive payload merges while terminal rows remain available for close-out context. Orphaned active task groups now show `Reassigning to New Worker` while anchored to another same-site worker or queued placeholder. | `git diff --check` passed. `./Engine_Unit_Tests.sh --domain webui` could not run because runtime WebUI unit-test cache is missing on this host. | `Unit_Test_Results/engine-20260530T193850Z` |
| 2026-05-30 | `M1` | Corrected mixed SSH credential probing to match `ssh-connection-logic.md`: key-only probe runs first, password-only probe runs only after key probe failure/timeout, and failed password fallback keeps key-only final auth. | `python3 -m py_compile` passed; focused mixed-auth scheduler pytest passed; `./Engine_Unit_Tests.sh --domain ansible` passed with test token root. | `Unit_Test_Results/engine-20260530T123048Z` |
| 2026-05-30 | `M1` | Implemented worker-owned scheduled task concurrency. Added persisted `site_worker_settings.json` with `BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY` override, replaced Server Info UI with Site Worker Scheduled Tasks, removed Ansible per-job/global limits from scheduler dispatch gates, and made site-workers reload scheduled-lane capacity each loop. Legacy Ansible runner endpoints remain API-compatible but are no longer operator-visible or used for dispatch. | `python3 -m py_compile` passed; focused server-info/queue/scheduler pytest passed; `bash -n` entrypoints passed; `./Engine_Unit_Tests.sh --domain ansible` passed with test token root. | `Unit_Test_Results/engine-20260530T121325Z` |
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
- `M1` smoke after Job 98/99: shared Ansible and individual Ansible both completed successfully against stable target sets, except the same known skipped device.
- `M1` concurrency ownership simplified: site-worker scheduled-lane capacity is the single operator-facing scheduler throttle, profile-tuned to `5`, `8`, `12`, or `16` per site worker. Legacy Ansible per-job/global settings remain API-compatible but no longer gate scheduler dispatch.
- `M1` profile tuning restored: `Engine.sh deploy` now writes profile metadata, DB pool values, PostgreSQL settings, and `BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY`; Server Info displays tuned scheduled work-item slots read-only.
- `M1` post-redeploy smoke: operator confirmed Engine redeploy, Server Info read-only value, and general expected behavior. Jobs 102/103 screenshot shows two `Task (8 Devices)` cards; current code interpretation is expected because card labels show target counts, not slot counts.
- `M1` SSH auth behavior corrected: mixed credentials now follow documented key-first probe order.
- Engine Status graph corrected for issue #233: active task groups no longer render as downstream work owned by stopped/lost site-workers, and orphaned active groups now show `Reassigning to New Worker`.
- Engine Status task groups now use scheduler-style count/status buckets: `Task (n Devices)` plus `Success`, `Running`, `Pending`, `Failed`, or `Skipped`.
- Engine Status terminal task groups now show a 30-second countdown, aggregate matching terminal work, and reset the timer when newer matching terminal work arrives. Worker lanes now show `Re-Deploying` while orphaned same-site work waits for replacement worker claim.
- Jobs 102/103 terminal results were confirmed from PostgreSQL: Job 102 succeeded; Job 103 succeeded on seven targets and failed on `lab-mail-02` due SSH authentication, not scheduler/runtime ownership.
- M1 post-redeploy close check passed: current WebUI image hash is deployed and served `adminRoutes` bundle includes Engine Status countdown/handoff code.
- M2 complete: Traefik file provider now watches `Engine/Services/traefik-edge/config/dynamic/`, core routes render to `core.yml`, per-site-worker route files use `site-worker-<worker_guid>.yml`, and post-redeploy add/remove hotload smoke passed without recreating Traefik.
- M3 implementation committed: `job_scheduler_worker_routes` records scheduler-owned site-worker route metadata, lifecycle status, and generation; local focused queue tests cover create, query, update, retire, lost-worker, prune, and missing-registry recovery behavior.

## Remaining Work

- If operator wants device-level concurrency instead of work-item concurrency, create a new follow-up design item outside this migration path: shared Ansible would need an explicit forks/host-fan-out policy because current site-worker slots intentionally gate work items, not hosts inside a shared Ansible process.
- Redeploy latest branch head and complete `M3` post-redeploy route-registry smoke before marking `M3` done.
- Complete `M4` through `M6` to establish direct worker authorization and Agent socket ownership.
- Complete `M7` through `M11` to migrate each live remote-operation feature.
- Complete `M12` and `M13` to finalize Ansible ownership and clean `api-backend`.
- Complete `M14` before any Go rewrite implementation starts.

## Validation Matrix

| Check | Milestone | Status | Notes |
| --- | --- | --- | --- |
| Markdown and index link inspection | `M0` | `Done` | Doc-only validation. |
| `git diff --check` | `M0` | `Done` | Passed before first commit. |
| `docker compose -f Data/Engine/Containers/compose.yaml config` | `M1`, `M2` | `Done` for `M1` and local `M2` implementation | M2 validation used `docker compose --env-file Data/Engine/Containers/compose.env.example -f Data/Engine/Containers/compose.yaml config`. |
| Generated Traefik YAML parse | `M2` | `Done` for local implementation | PyYAML parsed both Python-rendered and shell entrypoint-rendered static/core dynamic configs. |
| Focused Engine tests | `M1`-`M14` | `Done` for current `M1` fixes, `M2` edge runtime, and local `M3` route registry | Queue retry tests, server-info settings tests, scheduled job credential validation tests, affected scheduler validation cases, focused `Data/Engine/Unit_Tests/test_edge_runtime.py`, and focused `Data/Engine/Unit_Tests/test_job_scheduler_queue.py` passed. |
| WebUI unit tests | `M1` support fixes | `Blocked` locally | `./Engine_Unit_Tests.sh --domain webui` cannot run until runtime cache exists at `Engine/Services/webui-frontend/cache/web-interface`. M1 close used prod WebUI image hash plus served bundle inspection instead. |
| Full affected Engine lane | `M1`-`M14` | `Blocked` for scheduler/core domains | `ansible` domain passed. For M3, `./Engine_Unit_Tests.sh --domain scheduler` failed under system Python because `pytest` is missing; with the repo test venv it entered unrelated long-running `test_scheduled_jobs_api.py` failures and was stopped. Earlier scheduler-domain blocker was the existing onboarding helper mismatch: `scheduled_job_module._onboarding_raw_input_map` missing. `core` domain currently fails on root-owned runtime secret paths outside touched edge tests. |
| Manual Traefik hotload smoke | `M2` | `Done` | Temporary route file add returned API health, removal stopped the route, and Traefik process stayed unchanged. |
| Runtime route-registry smoke | `M3` | `Pending redeploy` | After rebuild, verify `job_scheduler_worker_routes` exists, active site-workers have active route rows, missing route rows are recreated by reconcile, and stopped/lost workers mark routes terminal. |
| Agent unit tests | `M5`, `M6` | `Not Started` | Required when Agent config or socket behavior changes. |
| Manual remote-op smoke | `M7`-`M11` | `Not Started` | Shell, desktop, files, process/service/software. |

## Fresh Codex Prompt

Use this prompt when starting a new Codex conversation:

```text
Read /opt/Borealis/AGENTS.md first, then read Docs/index.md and Docs/Reference/Migration Paths/api-backend-rewrite.md.

We are on branch feature/rewrite-api-backend-in-golang for PR #232, "Rewrite api-backend in Golang". Branch head should include `b2c8e488`, "Implement site-worker route registry", and the later tracker update that records M3 as pending post-redeploy smoke.

M1 and M2 are Done. M3 implementation is committed, but M3 is still In Progress until post-redeploy route-registry smoke passes. Do not start M4 yet.

Completed M1 state:
- api-backend Ansible/runtime-heavy dependency split has been implemented.
- site-worker owns scheduled Ansible execution dependencies and shared Ansible cache.
- stopped/lost site-workers requeue claimed work instead of leaving jobs in limbo.
- Engine Status task groups now aggregate as Task (n Devices) with Success/Running/Pending/Failed/Skipped pills.
- orphaned active work shows Reassigning to New Worker instead of appearing owned by stopped/lost workers.
- terminal task buckets show a 30-second countdown and reset when matching terminal work arrives.
- orphaned worker lanes show Re-Deploying until replacement worker claim is visible.
- SSH mixed credentials follow ssh-connection-logic.md: key-only probe first, password-only fallback only after key failure/timeout, failed password fallback keeps key-only final auth.
- scheduler ignores legacy Ansible per-job/global runner limits for dispatch.
- site-worker scheduled lane is the visible throughput gate for scheduled work items, not raw device count.
- Engine.sh profile tuning has been restored. Current host detected as 16 vCPU and 32.3 GiB RAM, so expected profile is MSP / Production and expected site-worker scheduled work-item slot count is 12.
- Server Info should show Site Worker Scheduled Tasks as read-only profile-managed value. PUT /api/server/site-worker-settings was removed; GET remains.
- Engine Status `Task (n Devices)` cards show represented target count. A shared Ansible batch can be one scheduled work item with multiple devices; individual Ansible can group several one-target work items into one card when job/status/worker match.

Completed M2 state:
- Traefik watches `Engine/Services/traefik-edge/config/dynamic`.
- Core routes render to `Engine/Services/traefik-edge/config/dynamic/core.yml`.
- Future site-worker route files use `site-worker-<worker_guid>.yml`.
- Route files must be written as `.site-worker-<worker_guid>.yml.tmp` in the same directory, then renamed to final filename for atomic hotload.
- Route rollback command is `rm -f Engine/Services/traefik-edge/config/dynamic/site-worker-*.yml`.
- Post-redeploy hotload smoke passed: temporary route add returned API health, removal stopped the route, and Traefik process stayed unchanged.

Next work:
1. Have operator rebuild latest branch head if not already rebuilt.
2. Run M3 route-registry smoke:
   - Confirm `job_scheduler_worker_routes` exists in runtime DB.
   - Confirm active site-workers have `active` route rows with `generation >= 1`, route file path `Engine/Services/traefik-edge/config/dynamic/site-worker-<worker_guid>.yml`, and metadata owner `job-scheduler`.
   - Confirm a missing route row for an active Docker-discovered site-worker is recreated by job-scheduler reconcile.
   - Confirm stopped/lost site-workers retire route rows as `retired` or `lost`.
3. If smoke passes, mark M3 Done, add work-log validation row, and set next safe step to M4.
4. If smoke fails, fix only the M3 regression, validate, commit, and keep M3 In Progress.
5. Do not change browser session tokens, Agent config, or remote-op feature routing in M3.

Validation constraints from prior session:
- Static checks passed before handoff: bash -n Engine.sh, py_compile for server/info.py, docker compose config using Data/Engine/Containers/compose.env.example, git diff --check.
- Official `core` lane with the repo test venv still fails on root-owned runtime secret paths outside touched edge-runtime tests.
- Runtime Engine/Deploy/compose.env compose config could not run locally because the file was root-owned.
- Current M3 local validation passed: py_compile for scheduler queue/manager and focused queue tests, focused `Data/Engine/Unit_Tests/test_job_scheduler_queue.py` (`12 passed`), and `git diff --check`.
- `./Engine_Unit_Tests.sh --domain scheduler` under system Python fails because `pytest` is missing; under the repo test venv it entered unrelated long-running `test_scheduled_jobs_api.py` failures and was stopped.
- Do not run npm/vite from staging source under Data/Engine/Containers/*/data.
```

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
