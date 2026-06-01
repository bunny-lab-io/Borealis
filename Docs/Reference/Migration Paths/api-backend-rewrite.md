# API Backend Rewrite

Track the worker-first migration that moves remote-operation ownership out of `api-backend` before the Go rewrite. Use this page as the handoff record between Codex sessions: update the active milestone, work log, validation result, and next safe resume point before stopping.

## Current Status

| Field | Value |
| --- | --- |
| Branch | `feature/rewrite-api-backend-in-golang` |
| PR | [#232](https://github.com/bunny-lab-io/Borealis/pull/232) |
| Active milestone | `M10: Process, Service, Software Ops` |
| Last updated | 2026-06-01 |
| Latest implementation commit | `c76073f7` (`Route process service software ops through site workers`). |
| Current state | `M10` implementation is committed and awaiting post-redeploy smoke. api-backend remains the authenticated REST/RBAC/database broker for process, service, and software pages, while live process RPCs, service control events, and software inventory refresh events route through the active same-site site-worker host-service bridge. Site Workers UI follow-up commit `5e198907` fixes status pill vertical alignment and hides AG Grid multi-sort numbering. |
| Next safe step | Have the operator rebuild/redeploy the Engine from branch head, then smoke M10 in the WebUI: process list and End Task, service list and start/stop/restart, software refresh plus one override-triggered refresh. Do not start `M11` until post-redeploy M10 smoke passes and this tracker marks `M10` Done. |

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
| `M3: Site-Worker Route Registry` | `Done` | Track active worker route metadata in runtime registry. |
| `M4: Signed Remote-Op Sessions` | `Done` | Mint scoped tokens for direct browser-to-worker access. |
| `M5: Agent Ops Route Cutover` | `Done` | Move Agent remote-op socket target to site-worker. |
| `M6: Site-Worker Agent Socket.IO` | `Done` | Move Agent remote-op event ownership to site-worker. |
| `M7: Remote Shell` | `Done` | Move interactive shell broker path to site-worker. |
| `M8: Remote Desktop + Guacamole` | `Done` | Move VNC and Guacamole path to site-worker. |
| `M9: Remote File Management` | `Done` | Move remote file operations and transfer state to site-worker. |
| `M10: Process, Service, Software Ops` | `In Progress` | Move remaining live Agent task handlers to site-worker. |
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
| Status | `Done` |
| Goal | Track active site-worker route, port, generation, and status in a reliable runtime registry. |
| Migrates | Worker discovery from implicit Docker/container state to explicit scheduler-managed metadata. |
| Out Of Scope | Browser session tokens, Agent config changes, remote-op feature behavior. |
| Done When | Scheduler can create, update, query, and retire route records for active site-workers. |
| Validation | Focused scheduler tests, DB lifecycle review for short-lived connections, route-registry failure/restart scenario. |
| Handoff Note | Implementation commit `b2c8e488` adds `job_scheduler_worker_routes`. Lifecycle owner is `job-scheduler`: register/spawn/reconcile create active rows, route upserts increment `generation` only when metadata or lifecycle status changes, stopped workers retire routes, lost workers mark routes `lost`, and worker-history pruning removes old terminal routes. Route files are metadata-only in M3 and default to `Engine/Services/traefik-edge/config/dynamic/site-worker-<worker_guid>.yml`. Runtime smoke after rebuild confirmed table creation and route create/query/update/recover/retire/lost behavior against PostgreSQL. `M4` may now begin. |

### M4: Signed Remote-Op Sessions

| Field | Definition |
| --- | --- |
| Status | `Done` |
| Goal | Authorize direct browser-to-site-worker remote operations with short-lived scoped tokens. |
| Migrates | Remote-op authorization away from api-backend proxying and toward api-backend session brokering only. |
| Out Of Scope | Moving individual remote-op traffic paths, Agent socket target changes, Guacamole data path. |
| Done When | WebUI can request worker URLs plus signed operation token scoped to user, site, device, capability, and expiry. |
| Validation | Endpoint tests for scope/expiry/RBAC, token verification tests in worker code, unauthorized/expired-token manual checks. |
| Handoff Note | Implementation commit `53388af0` adds `POST /api/remote-ops/session` and shared `Data.Engine.services.remote_ops` token helpers. Token issuer is `borealis-api-backend`; audience is `borealis-site-worker`; token type is `remote-op-session`; default TTL is `300` seconds and max TTL is `900` seconds, overrideable with `BOREALIS_REMOTE_OP_SESSION_TTL_SECONDS` and `BOREALIS_REMOTE_OP_SESSION_MAX_TTL_SECONDS`. Claims are `iss`, `aud`, `typ`, `sub`, `jti`, `iat`, `nbf`, `exp`, `user`, `role`, `site_id`, `device_guid`, `hostname`, `agent_id`, `worker_guid`, `route_generation`, and `capabilities`. Signing uses the existing Engine Ed25519 JWT key from `Engine/Services/api-backend/secrets/Auth_Tokens/borealis-jwt-ed25519.key`, with `BOREALIS_ENGINE_AUTH_TOKEN_ROOT` honored at key-load time. Post-redeploy smoke passed on 2026-05-31 using runtime PostgreSQL and synthetic worker route `m4-runtime-smoke-worker`; synthetic rows were cleaned up. `M5` is now active. |

### M5: Agent Ops Route Cutover

| Field | Definition |
| --- | --- |
| Status | `Done` |
| Goal | Make enrolled Agents connect to the site-worker ops route instead of api-backend `/socket.io/`. |
| Migrates | Agent remote-operation socket target selection. |
| Out Of Scope | Individual remote-op handler moves, shell/VNC/file behavior, Go rewrite. |
| Done When | Agent enrollment or refresh can deliver worker ops URL, Agent connects there, and legacy api-backend remote-op fallback is removed. |
| Validation | Agent unit tests for config/enrollment route data, Engine tests for route response, manual reconnect smoke with one enrolled Agent. |
| Handoff Note | Implementation commit `5227098c` delivers `remote_ops_route` through enrollment poll and token refresh, persists Agent route metadata in `agent.json`, and makes Agent Socket.IO reconnect use the site-worker route when available. Post-redeploy token-refresh smoke passed against runtime PostgreSQL using a synthetic same-site active worker route; synthetic rows were cleaned up. Real Agent connection validation moves to `M6` because site-worker did not own a Socket.IO listener until that milestone. |

### M6: Site-Worker Agent Socket.IO

| Field | Definition |
| --- | --- |
| Status | `Done` |
| Goal | Move Agent remote-operation socket registry and event dispatch from `api-backend` to `site-worker`. |
| Migrates | Agent connect/disconnect tracking, capability state, task event routing, remote-op response correlation. |
| Out Of Scope | Feature-specific shell/VNC/file/process behavior unless needed for socket ownership smoke. |
| Done When | Site-worker owns Agent session registry and can dispatch/receive generic remote-operation events without api-backend as middle-man. |
| Validation | Worker socket tests, Agent connect/disconnect smoke, event timeout/error-path tests. |
| Handoff Note | Implementation commit `494f1f3d` adds a site-worker Socket.IO runtime on `/socket.io/` behind the existing `/_borealis/site-workers/<worker_guid>` Traefik prefix. Site-workers authenticate Agent bearer tokens against device GUID, fingerprint, token version, status, and site assignment; registered sockets are stored in a worker-local registry keyed by Agent ID and hostname/service mode. Generic event dispatch supports `emit` and `call` semantics for later feature migrations. Follow-up commit `f32f6c4d` mounts Traefik config into `job-scheduler` and spawned site-workers so route-file writes reach the host watched directory. Follow-up commit `d831f92a` prevents Agents from treating stale api-backend roots as valid remote-op routes and forces route refresh. Follow-up commit `9a7dea29` prevents idle site-workers from retiring while Agent sockets are registered and makes usable site-worker routes refresh by age so Agents recover from worker route churn. Post-rebuild M6 smoke passed with LAB-OPERATOR-01 on 2026-05-31: worker route health, prefixed Socket.IO, Agent registry, idle hold-open, harmless dispatch, route-churn recovery, and synthetic disconnect cleanup all passed. `M7` is now active. |

### M7: Remote Shell

| Field | Definition |
| --- | --- |
| Status | `Done` |
| Goal | Move interactive shell traffic and WireGuard shell TCP handling to site-worker. |
| Migrates | Shell open/send/resize/close, shell session state, and Engine-side TCP connection to Agent shell service. |
| Out Of Scope | Remote desktop, file management, process/service/software actions, Ansible execution. |
| Done When | WebUI shell uses direct site-worker path and `api-backend` no longer brokers shell traffic. |
| Validation | Manual shell open/send/close smoke, disconnect/reconnect scenario, authorization failure check, focused worker tests where available. |
| Handoff Note | `M7` completed after post-redeploy WebUI smoke on LAB-OPERATOR-01. Browser shell traffic reached `/_borealis/site-workers/38da1dbc-a6ee-43e9-b16d-cdb6be6da4c5/socket.io/`, site-worker opened the shell over WireGuard, command output returned, and close/disconnect cleaned up the worker-local shell session. `M8` is now active. |

### M8: Remote Desktop + Guacamole

| Field | Definition |
| --- | --- |
| Status | `Done` |
| Goal | Move VNC orchestration and Guacamole connection path to site-worker. |
| Migrates | VNC start/stop/probe/credential request flow, VNC proxy ownership, and browser/Guacamole routing away from `api-backend`. |
| Out Of Scope | Remote shell, remote files, process/service/software actions, Go rewrite. |
| Done When | Browser remote desktop session connects through site-worker-managed route, and Guacamole no longer depends on api-backend VNC proxy code. |
| Validation | Manual remote desktop open/close smoke, credential-request flow, unavailable-host failure, Traefik route hotload check. |
| Handoff Note | `M8` completed after post-redeploy browser smoke on LAB-OPERATOR-01. Remote Desktop connected successfully three times in a row through the worker-managed Guacamole path. `M9` is now active. |

### M9: Remote File Management

| Field | Definition |
| --- | --- |
| Status | `Done` |
| Goal | Move remote file operations and transfer state to site-worker. |
| Migrates | Browse, upload, folder upload, download, cancel, copy, cut, paste, rename, move, delete, create-folder, edit-text, transfer progress, and Agent file events. |
| Out Of Scope | Process/service/software operations, remote shell, remote desktop, Ansible execution. |
| Done When | File operations use direct site-worker routes and worker-local transfer state; api-backend no longer stores live transfer state. |
| Validation | Manual browse/upload/download/cancel smoke, large transfer progress check, expired-token check, focused file-operation tests where available. |
| Handoff Note | `M9` completed after post-redeploy File Management smoke. Operator confirmed File Management behavior is working as expected. Worker-local transfer state lives under temp path `Borealis/site_worker_file_management/<worker_guid>` with `FILE_TRANSFER_SESSION_TTL_SECONDS` cleanup; completed download artifacts are removed after the operator downloads content. `M10` is now active. |

### M10: Process, Service, Software Ops

| Field | Definition |
| --- | --- |
| Status | `In Progress` |
| Goal | Move remaining live Agent management handlers to site-worker. |
| Migrates | Process list/control, service inventory/actions, software inventory/actions, and related live response events. |
| Out Of Scope | Scheduled inventory storage, long-term reporting, Ansible execution, Go rewrite. |
| Done When | Process, service, and software live operations run through site-worker and no longer depend on api-backend Agent socket registry. |
| Validation | Manual process/service/software smoke, RBAC/token denial checks, focused route/worker tests. |
| Handoff Note | Implementation commit `c76073f7` adds shared api-backend worker-bridge helpers and routes live process RPCs, service control events, and software inventory refresh events through the active same-site site-worker host-service internal routes. api-backend still owns login/RBAC, cached service/software inventory reads, service pending-state persistence, software override persistence, and quick-job queueing. Post-redeploy WebUI smoke remains pending; do not start `M11` until that smoke passes and this milestone is marked Done. |

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
| 2026-06-01 | `M10` | Implemented worker-routed process, service, and software live operations. Added shared api-backend worker bridge for internal site-worker host-service `status`, `event`, and `call`; process list/terminate now call worker-owned SYSTEM Agent sockets; service start/stop/restart and software inventory refresh requests now emit through the active same-site worker. Site Workers UI status pills were vertically centered and AG Grid sort-order numbers hidden. | Local validation passed: Python compile for touched backend/test files; focused device API pytest for process/service/software paths passed (`7 passed`); `test_site_worker_socket.py` passed (`10 passed`); `./Engine_Unit_Tests.sh --domain devices` passed when run with isolated `BOREALIS_ENGINE_AUTH_TOKEN_ROOT`; `git diff --check` passed. WebUI unit lane remains blocked until runtime cache exists at `Engine/Services/webui-frontend/cache/web-interface/Unit_Tests`. Post-redeploy M10 smoke remains pending. | Implementation commit `c76073f7`; UI commit `5e198907`; `Unit_Test_Results/engine-20260601T074913Z`; blocked WebUI run `Unit_Test_Results/engine-20260601T075442Z`. |
| 2026-06-01 | `M9` | Closed Remote File Management worker migration after operator smoke. After Engine/Agent redeploy, operator confirmed File Management behavior worked as expected through the WebUI. `M10` is now active. | Post-redeploy File Management smoke passed by operator report. | Operator report on 2026-06-01; implementation commit `a62dae99`; UI follow-up commit `ecd2c41f`. |
| 2026-06-01 | `M9` | Implemented worker-owned File Management transfer state. Operator `/api/device/files/<hostname>/*` endpoints still perform api-backend login/RBAC/device-scope checks, then call the active same-site site-worker over internal routes for live file RPCs, upload/download transfer creation, status, cancel, and content streaming. Site-worker now owns FileTransferStore, staged upload bytes, completed download artifacts, Agent transfer helper endpoints, and worker-local Agent file events. Agent upload/download code now honors `transfer_base_url` for status, progress, upload-item fetches, cancellation checks, and artifact upload. | Local validation passed: Python compile for touched Engine files, direct focused pytest for File Management and site-worker socket tests passed (`17 passed`), `./Engine_Unit_Tests.sh --domain files` passed, `./Engine_Unit_Tests.sh --domain remote-access` passed, `./Data/Agent/Unit_Tests/Agent_Unit_Tests.sh --domain go-agent` passed, and `git diff --check` passed. Post-redeploy browser File Management smoke remains pending. | Implementation commit `a62dae99`; `Unit_Test_Results/engine-20260601T061939Z`; `Unit_Test_Results/agent-20260601T062212Z`. |
| 2026-06-01 | `M8` | Closed Remote Desktop worker migration after operator browser smoke. LAB-OPERATOR-01 Remote Desktop connected successfully three times in a row after Engine rebuild/redeploy, confirming the worker-managed Guacamole route and VNC credential bridge were usable in the WebUI. | Post-redeploy browser Remote Desktop smoke passed. | Operator report on 2026-06-01; M8 implementation commits through `699dbcd8` plus follow-up site-worker status/UI commits. |
| 2026-06-01 | `M8` | Fixed Remote Desktop establish after Agent sockets moved to site-workers. Runtime VNC logs showed repeated LAB-OPERATOR-01 establish attempts failing at `vnc_agent_live_credentials_unavailable` while Agent role health reported VNC ready. api-backend now falls back from legacy `call_agent_event` to the active same-site worker route for live `vnc_credential_request`, socket-registration checks, and VNC start/stop emits. Site-worker exposes internal authenticated host-service `status`, `event`, and `call` endpoints backed by its worker-local Agent socket registry. | Local validation passed: Python compile for touched VNC/site-worker/test files, focused worker credential bridge regressions passed (`2 passed`), full `test_vnc_api.py` plus `test_site_worker_socket.py` passed (`42 passed`), `./Engine_Unit_Tests.sh --domain remote-access` passed, and `git diff --check` passed. Post-redeploy browser Remote Desktop smoke remains pending. | Implementation commit `699dbcd8`; VNC log evidence `E04F result=vnc_agent_live_credentials_unavailable` for LAB-OPERATOR-01 at `2026-06-01 00:30:48-00:30:59`; `Unit_Test_Results/engine-20260601T004042Z`. |
| 2026-06-01 | `M8` | Simplified connected site-worker status presentation after operator UI review. Connected site-worker node header now shows only `Running`; connected device count stays in the node body as `n Devices Connected`. | `git diff --check` passed. `./Engine_Unit_Tests.sh --domain webui` is blocked because runtime WebUI unit tests are missing at `Engine/Services/webui-frontend/cache/web-interface/Unit_Tests`. | Implementation commit `5ca5e94a`; blocked WebUI run `Unit_Test_Results/engine-20260601T001545Z`. |
| 2026-06-01 | `M8` | Stabilized persistent site-workers after Remote Desktop route work. Site-workers with registered Agent sockets now heartbeat as running with an `agent_sockets` link so Engine Status shows connected workers as running and suppresses idle teardown. Job-scheduler detects live site-worker containers whose image tag differs from `BOREALIS_SITE_WORKER_IMAGE`, stops stale containers during reconcile, and spawns replacements from the current image. Online same-site devices also keep a replacement site-worker route alive while Agent sockets reconnect. | Runtime smoke after manually stopping stale worker confirmed replacement site-worker image `borealis-engine/site-worker:sha-2c480331ff3a`; LAB-OPERATOR-01 reconnected and Engine Status showed a running site-worker with `1 Device Connected`. Focused pytest passed for `test_job_scheduler_queue.py` and `test_site_worker_socket.py` (`25 passed`); `test_job_scheduler_queue.py` passed inside scheduler lane. Full scheduler lane remains blocked by existing runtime secret permission errors in `test_scheduled_jobs_api.py` against root-owned `Engine/Services/api-backend/secrets/Auth_Tokens/borealis-jwt-ed25519.key`. `git diff --check` passed. | Follow-up commits `5c805338` and `d43786e3`; active runtime worker during smoke `5c9bc193-78a5-42a6-8d42-976af5666246`; Remote Desktop browser smoke remains pending before M8 Done. |
| 2026-05-31 | `M8` | Implemented worker-owned Remote Desktop/Guacamole path. Site-worker route metadata now includes a deterministic Remote Desktop Guacamole port and Traefik writes a higher-priority `/_borealis/site-workers/<worker_guid>/remote-desktop/vnc` router to that port. Site-worker exposes internal VNC session registration/disconnect endpoints, validates scoped `remote_desktop` tokens, owns the Guacamole one-time-token registry and VNC proxy, and sends open/close/transport-confirm callbacks back to api-backend. `/api/vnc/establish` now registers browser sessions inside the target worker and returns the worker-prefixed Guacamole WebSocket path; WebUI prefers that path on the current page origin. api-backend no longer pre-starts its own Guacamole VNC proxy. | Local validation passed: `python3 -m py_compile` for touched Python files, focused pytest for VNC API/site-worker socket/route registry/VNC proxy/Guacamole proxy passed (`67 passed`), focused pytest after fixture cleanup passed (`55 passed`), `./Engine_Unit_Tests.sh --domain remote-access` passed, and `git diff --check` passed before tracker update. Post-redeploy Remote Desktop browser smoke remains pending. | Implementation commit `46cc9833`; `Unit_Test_Results/engine-20260531T223334Z`; expected smoke path `/_borealis/site-workers/<worker_guid>/remote-desktop/vnc/guacamole`. |
| 2026-05-31 | `M7` | Closed Remote Shell worker migration after Engine rebuild from `8edd5d2c`. Served WebUI bundle included route-prefix shell Socket.IO logic and polling-first transport. Synthetic future Bunny Lab work item spawned a site-worker route so LAB-OPERATOR-01 could register on the worker. Operator opened WebUI Remote Shell, ran `whoami`, ran `echo M7_SHELL_SMOKE`, received output, and closed the session. Synthetic `m7-runtime-smoke-route-*` work row was deleted after smoke. | Post-redeploy M7 smoke passed. Worker-local logs show `vpn_shell_open_success`, `vpn_shell_ready_pong`, two `vpn_shell_output_timing` entries, `vpn_shell_closed reason=close_request`, and `vpn_shell_output_summary lines=4 inputs=2`. Traefik access log shows browser polling through the site-worker route. `git diff --check` passed before the code-fix handoff commit. | Branch head `8edd5d2c`; served asset `inventoryRoutes-CEOXYMjk.js`; worker route `38da1dbc-a6ee-43e9-b16d-cdb6be6da4c5` on port `60600`; Traefik route `/_borealis/site-workers/38da1dbc-a6ee-43e9-b16d-cdb6be6da4c5/socket.io/`; Agent `LAB-OPERATOR-01_2540DA38-E2B1-45B9-9113-BF7CF0E1778A_SYSTEM`; smoke row `705` deleted. |
| 2026-05-31 | `M7` | Investigated repeated post-redeploy `worker_socket_connect_timeout` during LAB-OPERATOR-01 WebUI Remote Shell attempts. `/api/shell/establish` returned active worker `0deeff72-fda8-4b0a-9c94-ea12f5f39f19` three times, the worker was healthy, LAB-OPERATOR-01 was registered on the worker, Traefik-prefixed worker health and Socket.IO polling passed, and direct missing-token `vpn_shell_open` was rejected. Browser attempts did not reach the worker route in Traefik or site-worker logs, so WebUI now derives the shell Socket.IO path from `route_path_prefix` on `window.location.origin`, keeps absolute worker URLs only as path fallback, and starts with polling before WebSocket upgrade. | `git diff --check` passed. WebUI unit lane was not run because runtime cache is missing at `Engine/Services/webui-frontend/cache/web-interface/Unit_Tests` and `node_modules`. Post-rebuild WebUI shell smoke remains pending. | Runtime worker `0deeff72-fda8-4b0a-9c94-ea12f5f39f19`, port `56706`; Agent registry contained `LAB-OPERATOR-01_2540DA38-E2B1-45B9-9113-BF7CF0E1778A_SYSTEM`; Traefik-prefixed `/health` and `/socket.io/?transport=polling&EIO=4` returned `200`; direct `vpn_shell_open` without token returned `missing_token`; no browser `/_borealis/site-workers/...` access log appeared for the failed UI attempts. |
| 2026-05-31 | `M7` | Investigated failed LAB-OPERATOR-01 WebUI Remote Shell attempts after Engine redeploy. `/api/shell/establish` returned the active site-worker route and scoped token, served WebUI bundle contained M7 code, Traefik prefix polling reached the worker, and HTTP/1.1 WebSocket upgrade worked. Hardened the WebUI shell path so it waits for the dedicated worker Socket.IO connection before emitting `vpn_shell_open`, allows polling fallback, and reports worker socket connect failures directly instead of surfacing only a shell-open timeout. | `git diff --check` passed. `./Engine_Unit_Tests.sh --domain remote-access` passed with `BOREALIS_ENGINE_TEST_PYTHON=/opt/Borealis/.cache/codex-engine-tests/bin/python`; the first system-Python attempt failed before tests because `/usr/bin/python3` has no `pytest`. WebUI unit lane remains blocked because runtime cache is missing at `Engine/Services/webui-frontend/cache/web-interface/Unit_Tests` and `node_modules`. Post-rebuild WebUI smoke remains pending. | Runtime logs: shell establishes at `13:22:21`, `13:23:08`, and `13:23:44`; worker route `dbcee686-3e5a-4ba3-970b-b843507ab316`; Traefik-prefixed worker polling `HTTP/2 200`; HTTP/1.1 worker WebSocket `101 Switching Protocols`; `Unit_Test_Results/engine-20260531T133040Z`; source `Remote_Shell.jsx`. |
| 2026-05-31 | `M7` | Implemented Remote Shell worker path. Shell establish now requires an active same-site worker route and returns a scoped `remote_shell` token plus worker Socket.IO URL. WebUI creates a dedicated site-worker Socket.IO client for shell events and no longer binds shell handlers to the main api-backend socket. Site-worker validates the token, looks up the Agent WireGuard lease, owns shell sessions, enforces one active shell per Agent, retries TCP connect to the Agent shell listener, and cleans sessions on close/disconnect. | Local validation passed: `python3 -m py_compile` for touched Python files, focused `test_site_worker_socket.py` passed (`4 passed`), `Engine_Unit_Tests.sh --domain remote-access` passed with isolated JWT token root, and `git diff --check` passed. WebUI unit lane was not run because runtime cache is missing at `Engine/Services/webui-frontend/cache/web-interface/Unit_Tests` and `node_modules`. Post-rebuild WebUI/Agent smoke remains pending. | `83136e22`; `Unit_Test_Results/engine-20260531T125049Z`; focused pytest output. |
| 2026-05-31 | `M6` | Closed M6 after Engine rebuild and LAB-OPERATOR-01 Agent redeploy/reapproval from branch head. Synthetic future work item spawned Bunny Lab worker route `6f4e5d9d-4bdf-42f3-a853-2ec98ba1327d`; direct `/health`, Traefik-prefixed `/health`, and prefixed Engine.IO handshake passed. LAB-OPERATOR-01 registered in worker `/agents`, remained registered past the 60-second idle TTL, and harmless generic dispatch work item `702` succeeded through the worker-local emit path. Agent recovered to replacement worker route `1a4d67db-b4be-4b95-875e-8ddbdadf5970` after route churn. A synthetic same-site device socket registered and unregistered through the prefixed route without disturbing LAB-OPERATOR-01. Smoke work rows and synthetic device/token rows were deleted. | Runtime M6 smoke passed. LAB-OPERATOR-01 remained registered as `LAB-OPERATOR-01_2540DA38-E2B1-45B9-9113-BF7CF0E1778A_SYSTEM`; worker route health and prefixed Socket.IO passed; idle hold-open passed from `12:31:03Z` through `12:32:18Z`; dispatch item `702` finished `succeeded`; synthetic socket disconnect removed only `M6-SMOKE-CLIENT_*` while LAB-OPERATOR-01 stayed registered. Cleanup verified zero `m6-*` work items and zero synthetic device rows. | Branch head `cad6d728`; implementation commit `9a7dea29`; worker routes `6f4e5d9d-4bdf-42f3-a853-2ec98ba1327d` and `1a4d67db-b4be-4b95-875e-8ddbdadf5970`; work items `701` and `702` deleted. |
| 2026-05-31 | `M6` | Reran LAB-OPERATOR-01 live Agent smoke after Agent redeploy. Synthetic same-site future work item spawned worker `f24d3b66-ef2f-44d5-8ca5-980ccede57d5`; direct `/health`, Traefik-prefixed `/health`, prefixed Socket.IO handshake, and worker `/agents` registration all passed for Agent GUID `2540DA38-E2B1-45B9-9113-BF7CF0E1778A`. Harmless synthetic dispatch item then failed because the first worker retired while the Agent socket was registered, replacement worker `9348e7cc-07bc-45ed-a1e8-8dad820bb7fe` claimed the dispatch item, and the Agent still held the retired worker route. Fixed site-worker idle handling so registered Agent sockets suppress idle TTL, and fixed Agent route refresh so usable site-worker routes refresh by age and refresh again after socket disconnect. Synthetic smoke rows were deleted. | Runtime route and initial Agent registration smoke passed. Dispatch smoke failed pre-fix with `No system agent socket is registered for host LAB-OPERATOR-01`; this is now covered by code changes in `9a7dea29`. Local validation after fix: `go test ./internal/auth ./internal/runtime` passed from `Data/Agent`; focused pytest for `test_site_worker_socket.py` and `test_job_scheduler_queue.py` passed (`17 passed`); `./Data/Agent/Unit_Tests/Agent_Unit_Tests.sh --domain go-agent` passed; `git diff --check` passed. Post-rebuild Engine plus Agent smoke remains pending. | `9a7dea29`; worker `f24d3b66-ef2f-44d5-8ca5-980ccede57d5`; replacement `9348e7cc-07bc-45ed-a1e8-8dad820bb7fe`; synthetic work items `699` and `700` deleted; `Unit_Test_Results/agent-20260531T121031Z`. |
| 2026-05-31 | `M6` | Ran LAB-OPERATOR-01 live Agent smoke against Bunny Lab site `1`. Created synthetic future work item to spawn a same-site worker route, verified token refresh returns a site-worker `remote_ops_route`, but LAB-OPERATOR-01 still registered on the api-backend Agent socket while route was active. Fixed Agent route validation so stale `remote_ops.available=true` configs that point at the api-backend root are not usable, force token refresh, and cannot fall back to api-backend `/socket.io/`. Synthetic work item and temporary refresh token were deleted. | Runtime route and token-refresh API returned correct site-worker URL. Live Agent pre-fix failed by registering on api-backend at `11:43:08` instead of worker `/agents`. Local validation after fix: focused `go test ./internal/auth` passed from `Data/Agent`; `./Data/Agent/Unit_Tests/Agent_Unit_Tests.sh --domain go-agent` passed; `git diff --check` passed. Post-redeploy LAB-OPERATOR-01 smoke remains pending. | `d831f92a`; device `LAB-OPERATOR-01`; site `Bunny Lab`; synthetic work item `698` deleted; temporary refresh token `m6-smoke-refresh-lab-operator` deleted; `Unit_Test_Results/agent-20260531T114654Z`. |
| 2026-05-31 | `M6` | Ran post-rebuild route-file smoke after `f32f6c4d`. Synthetic future work items for site `1` and site `5` spawned site-workers and wrote host route files under `Engine/Services/traefik-edge/config/dynamic/`. Direct worker `/health` and `/agents` passed, Traefik-prefixed `/health` passed, and Traefik-prefixed `/socket.io/?transport=polling&EIO=4` returned Engine.IO open packets. Manager reconciliation retired idle/dead workers and spawned replacements when synthetic queued work still existed. Existing live Agents continued registering through api-backend socket logs, not site-worker logs. Synthetic queue rows were deleted after smoke. | Runtime DB checks passed for active worker routes and cleanup. Route files appeared for current workers. Direct and prefixed health checks passed for site `1` and site `5` workers. Prefixed Socket.IO polling handshakes returned `HTTP/2 200` plus Engine.IO open packets. Site-worker `/agents` remained empty with existing Agents, while api-backend `engine.log` showed recent `Agent socket registered` events, so fresh/redeployed Agent validation is still required. | Runtime workers included `b4b4e38b-a1bd-4655-ad3e-c4d5e9305040`, `18d95876-5509-4f53-9264-3694155b9c8c`, and replacements; synthetic work items `696` and `697` deleted. |
| 2026-05-31 | `M6` | Ran first post-rebuild runtime smoke and fixed route-file mount gap. Synthetic future work item spawned site-worker `092c0316-7e0c-46e6-b38e-899bfa2156cf`; direct listener `127.0.0.1:59902` returned `/health` and `/agents`, but host Traefik route file was missing and prefixed Traefik route returned `404`. Added Traefik config volume to `job-scheduler` and spawned site-worker Docker args so route file writes land in the watched host directory. Synthetic queue row was deleted. | Runtime direct worker health passed before fix; Traefik prefixed route failed as expected from missing route file. Local validation after fix: `python3 -m py_compile` passed for scheduler manager/test file, compose config rendered and includes `traefik-edge/config` volume under `job-scheduler`, focused pytest passed for `test_job_scheduler_queue.py` and `test_site_worker_socket.py` (`17 passed`), and `git diff --check` passed. Post-redeploy route-file/Agent socket smoke remains pending. | `f32f6c4d`; runtime worker `092c0316-7e0c-46e6-b38e-899bfa2156cf`; direct port `59902`; synthetic work item `695` deleted. |
| 2026-05-31 | `M6` | Implemented site-worker Agent Socket.IO ownership. Dynamic site-workers now get deterministic remote-op listener ports, publish Traefik route files with `stripPrefix`, start a worker-local Socket.IO server, authenticate same-site Agent bearer tokens, maintain a worker-local Agent registry, and dispatch Agent maintenance/generic scheduler events without api-backend as the socket middle-man. | `python3 -m py_compile` passed for touched scheduler/socket/registry files and focused tests. Focused pytest passed for `test_job_scheduler_queue.py` and `test_site_worker_socket.py` (`16 passed`). `git diff --check` passed. Broad `scheduler` and `remote-access` lanes entered unrelated long-running `test_scheduled_jobs_api.py`/`test_vnc_api.py` paths and were stopped, so they are not counted as M6 gates. Post-redeploy Agent socket smoke remains pending. | `494f1f3d`; focused pytest output. |
| 2026-05-31 | `M5` | Implemented Agent ops route cutover scaffolding. Added shared Agent route payload helper, returned `remote_ops_route` from enrollment poll and token refresh, stored route metadata in `agent.json`, preserved site-worker route prefixes for Socket.IO URLs, refreshed missing route metadata before Agent socket reconnect, and removed legacy api-backend socket fallback when no route is available. | `python3 -m py_compile` passed for touched Engine API files. Targeted Go package tests passed. `./Data/Agent/Unit_Tests/Agent_Unit_Tests.sh --domain go-agent` passed. `./Engine_Unit_Tests.sh --domain enrollment` passed with `BOREALIS_ENGINE_TEST_PYTHON=/opt/Borealis/.cache/codex-engine-tests/bin/python`. Focused `test_remote_ops_sessions.py` passed. `git diff --check` passed. Post-redeploy runtime token-refresh smoke passed using a synthetic same-site active worker route; real Agent socket connection validation is tracked under `M6`. | `5227098c`; `Unit_Test_Results/agent-20260531T080921Z`; `Unit_Test_Results/engine-20260531T081102Z`; focused pytest output. |
| 2026-05-31 | `M4` | Closed M4 after operator rebuild. Runtime image manifest updated at `2026-05-31T07:50:20Z`; live backend `POST /api/remote-ops/session` returned `401` without auth; runtime DB initially had no active site-worker route rows. Created synthetic worker route `m4-runtime-smoke-worker` for site `1`, requested session for `LAB-CA-01`, verified returned worker URL and scoped token claims, checked missing-route `409`, invalid capability `400`, out-of-scope user `404`, invalid-token rejection, and expired-token rejection, then deleted synthetic worker and route rows. | Runtime API `/health` returned `{"status":"ok"}`. Synthetic M4 smoke returned `m4_runtime_smoke=pass`, `session_status=200`, `unauthorized_status=401`, `missing_route_status=409`, `invalid_capability_status=400`, and `out_of_scope_status=404`. Cleanup verified zero synthetic worker/route rows and zero active route rows. `git diff --check` passed before tracker update. | Runtime PostgreSQL tables; `Engine/Deploy/image-manifest.json`; synthetic worker GUID `m4-runtime-smoke-worker`; `92acf473`. |
| 2026-05-31 | `M4` | Implemented signed remote-op session broker. Added `POST /api/remote-ops/session`, shared token issue/verify helpers for api-backend/site-worker use, scoped capability aliases, active site-worker route lookup, site RBAC checks, and direct worker URL response. JWT service now supports signing arbitrary claim sets and resolves the Engine auth token root at key-load time so runtime/test env overrides are honored. | `python3 -m py_compile` passed for touched API/auth/token files. Focused `test_remote_ops_sessions.py` passed (`8 passed`). Adjacent token/enrollment tests passed. `./Engine_Unit_Tests.sh --domain remote-access` passed with isolated test token root. `git diff --check` passed. Post-redeploy runtime smoke remains pending. | `53388af0`; `Unit_Test_Results/engine-20260531T073124Z`; focused pytest output. |
| 2026-05-31 | `M3` | Closed M3 after operator rebuild. Runtime DB table `engine.job_scheduler_worker_routes` exists. Runtime DB initially had only the job-scheduler manager worker, so no active site-worker route rows were expected. Ran isolated synthetic registry smoke against runtime PostgreSQL: created active route row through `register_worker`, queried by worker and site, confirmed no-op upsert keeps `generation=1`, metadata/upstream update increments to `generation=2`, deleted route row and recovered it with `upsert_worker_route`, retired it with `stop_worker`, marked it `lost` with `mark_missing_workers_lost`, listed terminal route rows, then deleted synthetic worker/route rows. | Runtime API `/health` returned `{"status":"ok"}`; Traefik ping returned `OK`; runtime image manifest updated at `2026-05-31T07:11:04Z`; branch head is `faf6de8d`. Runtime registry smoke returned `route-registry-smoke=pass`, `created_generation=1 changed_generation=2 final_status=lost`. Cleanup verified zero synthetic rows remained. `git diff --check` passed before tracker update. | Runtime PostgreSQL table inventory; `Engine/Deploy/image-manifest.json`; synthetic worker GUID `m3-runtime-smoke-worker`; `faf6de8d`. |
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
- M3 post-redeploy smoke complete: runtime PostgreSQL has `engine.job_scheduler_worker_routes`, synthetic route registry lifecycle smoke passed, and synthetic rows were cleaned up.
- M4 complete: api-backend now brokers signed remote-op sessions for active site-worker routes, shared token verification covers scope, expiry, worker, site, device, hostname, and capability checks, and post-redeploy runtime smoke passed with synthetic route cleanup.
- M5 complete: Agent enrollment/token refresh delivers site-worker remote-op route metadata, Agent config stores it, and Agent Socket.IO targets the site-worker route when available. Runtime synthetic token-refresh smoke passed; real Agent connection validation is part of M6 because site-worker listener ownership starts there.
- M6 implementation committed: site-worker starts a local Socket.IO listener, scheduler publishes matching Traefik route files, same-site Agent bearer-token authentication is enforced, and worker-local Agent registry/dispatch helpers are available for feature migrations. Follow-up mount fix is committed after first runtime smoke found host route file missing.
- M6 post-rebuild route-file smoke passed: direct worker health, Traefik-prefixed health, and Traefik-prefixed Socket.IO polling handshake all work.
- M6 stale-Agent-route fix committed: Agents no longer treat an api-backend root remote-op base URL as usable and will refresh route metadata before socket connect. LAB-OPERATOR-01 needs redeploy from branch head before final M6 Agent smoke.
- M6 post-redeploy Agent socket smoke passed with LAB-OPERATOR-01 registered on a site-worker route.
- M7 complete: Remote Shell opens through the site-worker route and worker-owned WireGuard shell bridge.
- M8 complete: Remote Desktop/Guacamole opens through the site-worker route; LAB-OPERATOR-01 connected successfully three times after redeploy.
- M9 complete: live File Management RPCs and transfer state route through the active same-site site-worker, and post-redeploy File Management smoke passed.
- M10 implementation committed: live process RPCs, service control events, and software inventory refresh events now route through the active same-site site-worker host-service bridge. Post-redeploy M10 smoke remains pending before marking M10 Done.

## Remaining Work

- If operator wants device-level concurrency instead of work-item concurrency, create a new follow-up design item outside this migration path: shared Ansible would need an explicit forks/host-fan-out policy because current site-worker slots intentionally gate work items, not hosts inside a shared Ansible process.
- Complete `M10` post-redeploy smoke, then mark `M10` Done before starting `M11`.
- Complete `M11` to migrate remaining live quick-job and maintenance dispatch.
- Complete `M12` and `M13` to finalize Ansible ownership and clean `api-backend`.
- Complete `M14` before any Go rewrite implementation starts.

## Validation Matrix

| Check | Milestone | Status | Notes |
| --- | --- | --- | --- |
| Markdown and index link inspection | `M0` | `Done` | Doc-only validation. |
| `git diff --check` | `M0` | `Done` | Passed before first commit. |
| `docker compose -f Data/Engine/Containers/compose.yaml config` | `M1`, `M2` | `Done` for `M1` and local `M2` implementation | M2 validation used `docker compose --env-file Data/Engine/Containers/compose.env.example -f Data/Engine/Containers/compose.yaml config`. |
| Generated Traefik YAML parse | `M2` | `Done` for local implementation | PyYAML parsed both Python-rendered and shell entrypoint-rendered static/core dynamic configs. |
| Focused Engine tests | `M1`-`M14` | `Done` for current `M1` fixes, `M2` edge runtime, local `M3` route registry, local `M4` session broker, local `M6` worker socket, local `M9` File Management worker routes, and local `M10` process/service/software worker routing | Queue retry tests, server-info settings tests, scheduled job credential validation tests, affected scheduler validation cases, focused `Data/Engine/Unit_Tests/test_edge_runtime.py`, focused `Data/Engine/Unit_Tests/test_job_scheduler_queue.py`, focused `Data/Engine/Unit_Tests/test_remote_ops_sessions.py`, focused `Data/Engine/Unit_Tests/test_file_management_api.py`, focused M10 `test_devices_api.py` cases (`7 passed`), and focused `Data/Engine/Unit_Tests/test_site_worker_socket.py` passed. |
| WebUI unit tests | `M1` support fixes and `M10` Site Workers UI follow-up | `Blocked` locally | `./Engine_Unit_Tests.sh --domain webui` cannot run until runtime cache exists at `Engine/Services/webui-frontend/cache/web-interface`. Latest blocked run: `Unit_Test_Results/engine-20260601T075442Z`. |
| Full affected Engine lane | `M1`-`M14` | `Blocked` for scheduler/core domains | `ansible` domain passed. For M3, `./Engine_Unit_Tests.sh --domain scheduler` failed under system Python because `pytest` is missing; with the repo test venv it entered unrelated long-running `test_scheduled_jobs_api.py` failures and was stopped. Earlier scheduler-domain blocker was the existing onboarding helper mismatch: `scheduled_job_module._onboarding_raw_input_map` missing. `core` domain currently fails on root-owned runtime secret paths outside touched edge tests. |
| Manual Traefik hotload smoke | `M2` | `Done` | Temporary route file add returned API health, removal stopped the route, and Traefik process stayed unchanged. |
| Runtime route-registry smoke | `M3` | `Done` | Runtime DB table exists; synthetic smoke verified create/query/update/recover/retire/lost lifecycle and cleanup. No active site-worker existed at smoke time, so no live route row was expected. |
| Remote-op session smoke | `M4` | `Done` | Runtime smoke confirmed success, unauthorized access, invalid capability, out-of-scope access, expired/invalid token rejection, missing worker-route behavior, and synthetic route cleanup. |
| Agent socket smoke | `M6` | `Done` | LAB-OPERATOR-01 registered through site-worker route after redeploy/reapproval, and later remote shell/desktop smokes confirmed worker-owned Agent socket dispatch. |
| Agent unit tests | `M5`, `M6`, `M9` | `Done` | `./Data/Agent/Unit_Tests/Agent_Unit_Tests.sh --domain go-agent` passed for M5 route cutover, M6 stale-route refresh, and M9 transfer URL routing (`Unit_Test_Results/agent-20260601T062212Z`). |
| Engine devices lane | `M10` | `Done` for local implementation | `./Engine_Unit_Tests.sh --domain devices` passed with isolated `BOREALIS_ENGINE_AUTH_TOKEN_ROOT=/tmp/borealis-codex-test-auth-tokens-m10` to avoid root-owned runtime JWT secret path. Results: `Unit_Test_Results/engine-20260601T074913Z`. |
| Manual remote-op smoke | `M7`-`M11` | `Done` for `M7`, `M8`, and `M9`; `Pending` for `M10` | Shell, desktop, and File Management passed against LAB-OPERATOR-01. Process/service/software M10 smoke is next. |

## Fresh Codex Prompt

Use this prompt when starting a new Codex conversation:

```text
Read /opt/Borealis/AGENTS.md first, then read Docs/index.md and Docs/Reference/Migration Paths/api-backend-rewrite.md.

We are on branch feature/rewrite-api-backend-in-golang for PR #232, "Rewrite api-backend in Golang". Branch head should include M10 implementation commit `c76073f7` and tracker updates showing M10 smoke is pending.

M1 through M9 are Done. M10 is active.

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

Completed M3 state:
- `job_scheduler_worker_routes` records scheduler-owned site-worker route metadata, lifecycle status, and generation.
- Runtime PostgreSQL has `engine.job_scheduler_worker_routes`.
- Synthetic post-redeploy route-registry smoke passed create/query/update/recover/retire/lost lifecycle and cleanup.

Completed M4 state:
- `POST /api/remote-ops/session` authorizes an operator/device/capability request and returns direct active site-worker URLs plus a signed operation token.
- Shared `Data.Engine.services.remote_ops` helpers issue and verify tokens for api-backend/site-worker code.
- Token issuer is `borealis-api-backend`; audience is `borealis-site-worker`; token type is `remote-op-session`; default TTL is 300 seconds; max TTL is 900 seconds.
- Claims include user, role, site_id, device_guid, hostname, agent_id, worker_guid, route_generation, capabilities, and standard JWT timing/id fields.
- Signing uses the existing Engine Ed25519 JWT key under `Engine/Services/api-backend/secrets/Auth_Tokens/borealis-jwt-ed25519.key`.
- Post-redeploy smoke passed after operator rebuild: runtime DB synthetic route `m4-runtime-smoke-worker` produced session success, missing-route, invalid-capability, out-of-scope, unauthorized, invalid-token, and expired-token checks; synthetic rows were cleaned up.

Completed M5 state:
- Enrollment poll and token refresh return `remote_ops_route` when an active same-site site-worker route exists.
- Agent config stores the route and reconnects Socket.IO to the site-worker route when available.
- Runtime synthetic token-refresh smoke passed after redeploy; synthetic rows were cleaned up.

Completed M6 state:
- Site-worker owns Agent Socket.IO listener on `/socket.io/` behind `/_borealis/site-workers/<worker_guid>`.
- Site-worker authenticates Agent bearer tokens against device GUID, fingerprint, token version, status, and site assignment.
- Worker-local Agent registry supports hostname/service-mode lookup, `emit`, and `call`.
- LAB-OPERATOR-01 registered through site-worker route after redeploy/reapproval, and later Remote Shell/Remote Desktop smokes confirmed worker-owned Agent socket dispatch.

Completed M7 state:
- Remote Shell uses direct site-worker Socket.IO path and worker-owned WireGuard shell bridge.
- LAB-OPERATOR-01 shell smoke passed with command output returned and close cleanup confirmed.

Completed M8 state:
- `/api/vnc/establish` brokers auth/RBAC and registers Remote Desktop sessions inside the active same-site worker.
- Browser Guacamole WebSocket path uses `/_borealis/site-workers/<worker_guid>/remote-desktop/vnc/guacamole`.
- LAB-OPERATOR-01 Remote Desktop connected successfully three times in a row after redeploy.

Completed M9 state:
- Implementation commit `a62dae99` routes live File Management RPCs and transfer state through active same-site site-workers.
- api-backend remains operator REST broker for login/RBAC/device scope, then calls worker internal routes.
- Site-worker owns FileTransferStore, upload staging, download artifacts, Agent transfer helper endpoints, transfer progress/cancel snapshots, and Agent file-management event dispatch.
- Agent File Management honors `transfer_base_url` for upload item fetches, transfer status checks, progress updates, cancellation checks, and download artifact upload.
- Worker-local transfer state lives under temp path `Borealis/site_worker_file_management/<worker_guid>` with `FILE_TRANSFER_SESSION_TTL_SECONDS` cleanup.
- Local validation passed: Python compile, focused pytest (`17 passed`), Engine `files`, Engine `remote-access`, Agent `go-agent`, and `git diff --check`.
- Post-redeploy File Management smoke passed by operator report.

Current M10 state:
- Implementation commit `c76073f7` routes live process, service, and software operations through active same-site site-workers.
- Shared helper `Data/Engine/Containers/api-backend/data/services/remote_ops/worker_bridge.py` wraps internal site-worker host-service `status`, `event`, and `call`.
- Process list and End Task now call `process_management_request` through the worker-owned SYSTEM Agent socket.
- Service start/stop/restart now emits `service_control_action` through the worker-owned SYSTEM Agent socket, while api-backend still persists pending service state.
- Software inventory refresh requests now emit `software_inventory_refresh_request` through the worker-owned SYSTEM Agent socket, while api-backend still owns icon/uninstall override persistence and quick-job queueing.
- Site Workers UI follow-up commit `5e198907` vertically centers status pill text and hides AG Grid multi-sort numbering.
- Local validation passed: py_compile for touched backend/test files, focused M10 pytest (`7 passed`), `test_site_worker_socket.py` (`10 passed`), `./Engine_Unit_Tests.sh --domain devices` with isolated `BOREALIS_ENGINE_AUTH_TOKEN_ROOT`, and `git diff --check`.
- WebUI unit lane remains blocked until runtime cache exists at `Engine/Services/webui-frontend/cache/web-interface/Unit_Tests`.
- Post-redeploy M10 smoke remains pending. Do not mark M10 Done yet.

Next work:
1. Have operator rebuild/redeploy Engine from branch head.
2. Smoke M10 in WebUI against LAB-OPERATOR-01 or another online device:
   - Processes tab loads live process list.
   - End Task returns expected success/failure for a safe test process.
   - Services tab loads cached service inventory and shows `agent_socket` as available when worker socket is connected.
   - Service action start/stop/restart dispatches and records pending state.
   - Installed Software `Query Software Changes` queues a refresh.
   - Software icon override or clear-icon action persists rule and requests refresh.
3. If smoke passes, update this tracker: mark M10 Done, add validation row, set next safe step to M11.
4. If smoke fails, fix only M10 regression, validate, commit, push, and retry smoke.
5. Do not start `M11` until M10 smoke passes and this tracker marks M10 `Done`.

Validation constraints from prior session:
- Static checks passed before handoff: bash -n Engine.sh, py_compile for server/info.py, docker compose config using Data/Engine/Containers/compose.env.example, git diff --check.
- Official `core` lane with the repo test venv still fails on root-owned runtime secret paths outside touched edge-runtime tests.
- Runtime Engine/Deploy/compose.env compose config could not run locally because the file was root-owned.
- Current M6 local validation passed: py_compile for touched scheduler/socket/registry files, compose config with `Data/Engine/Containers/compose.env.example`, focused `Data/Engine/Unit_Tests/test_job_scheduler_queue.py` plus `Data/Engine/Unit_Tests/test_site_worker_socket.py` (`17 passed`), and `git diff --check`.
- Current M6 runtime route validation passed after rebuild: synthetic site `1` and site `5` workers wrote host route files, direct `/health` passed, Traefik-prefixed `/health` passed, and Traefik-prefixed `/socket.io/?transport=polling&EIO=4` returned Engine.IO open packets.
- Current M6 stale-route fix validation passed: `go test ./internal/auth` from `Data/Agent`, `./Data/Agent/Unit_Tests/Agent_Unit_Tests.sh --domain go-agent`, and `git diff --check`.
- Current M6 live Agent validation passed after LAB-OPERATOR-01 redeploy/reapproval.
- Current M9 local validation passed: py_compile for touched Engine files, focused File Management/site-worker pytest (`17 passed`), Engine `files`, Engine `remote-access`, Agent `go-agent`, and `git diff --check`.
- Current M10 local validation passed: py_compile for touched backend/test files, focused M10 device API pytest (`7 passed`), `test_site_worker_socket.py` (`10 passed`), Engine `devices` with isolated auth-token root (`Unit_Test_Results/engine-20260601T074913Z`), and `git diff --check`.
- Current M4 runtime validation passed after rebuild: live backend rejected unauthenticated session requests with `401`; synthetic runtime route smoke passed success and denial paths; cleanup verified zero synthetic rows.
- `./Engine_Unit_Tests.sh --domain scheduler` under system Python fails because `pytest` is missing; under the repo test venv it entered unrelated long-running `test_scheduled_jobs_api.py` failures and was stopped.
- `./Engine_Unit_Tests.sh --domain scheduler` was not counted for current M6 because it entered unrelated long-running `test_scheduled_jobs_api.py` paths.
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
