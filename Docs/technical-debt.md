# Technical Debt
[Back to Docs Index](index.md) | [Index (HTML)](index.html)

## Purpose
Track technical debt items that affect developer experience, runtime behavior, or maintainability. This document is the staging area for items that will later become GitHub Issues.

## How To Use This Doc
- Add new entries at the top of the Issues section.
- Use the template below so future agents can understand impact, mitigation, and removal criteria.
- Keep entries concise but action oriented. Link to files and logs instead of pasting large outputs.
- When you open a GitHub Issue, note the issue URL and mark the entry as migrated.

## Logging Rules
- Log any patchy or non-standard workaround, even if it is small.
- Log any behavior that works in production but fails in dev (or vice versa).
- Log any changes that are likely to be removed once upstream or platform fixes land.
- If a change touches dependencies, include the exact version and why it matters.
- Use ASCII only unless the file already contains Unicode.

## Entry Template
```
ID: TD-YYYYMMDD-##
Status: active | mitigated | migrated | resolved
Owner: <name or team>
Date Added: YYYY-MM-DD
Summary: <one sentence>
Impact: <what breaks and who is affected>
Root Cause: <why it happens>
Current Mitigation: <what we did>
Removal Criteria: <what must be true to remove the workaround>
Files: <paths>
Evidence: <logs, errors, or reproduction hints>
Next Step: <smallest useful follow-up>
GitHub Issue: <link or "not yet">
```

## Issues
ID: TD-20260419-01
Status: active
Owner: Agent + Engine
Date Added: 2026-04-19
Summary: Engine-managed zip apply intentionally preserves the currently running updater tooling, so changes to `Update.sh`, `Update.ps1`, and `Data/Agent/update_helper.py` can take one extra update cycle to land on agents.
Impact: The new release-channel system can stage and apply most source changes immediately, but updater-tooling fixes themselves may not take effect until the next successful update pass, which can surprise operators during rollout/debugging.
Root Cause: The agent now applies full-repo zip artifacts in place while the updater helper and wrapper scripts are still executing, so overwriting those files mid-run risks corrupting the active update process.
Current Mitigation: `Data/Agent/update_helper.py` excludes `Update.sh`, `Update.ps1`, and `Data/Agent/update_helper.py` from staged archive replacement, while the Engine-managed manifest flow still updates the rest of the agent/runtime tree and records channel-aware update state.
Removal Criteria: Borealis gains a two-phase updater bootstrap path, atomic updater self-replacement flow, or a dedicated external updater binary/service that can safely swap updater-tooling files during the same release.
Files: `Data/Agent/update_helper.py`, `Update.sh`, `Update.ps1`
Evidence: The staged zip-apply path now uses `_SYNC_EXCLUDED_RELATIVE = {"Update.ps1", "Update.sh", "Data/Agent/update_helper.py"}` to avoid overwriting the actively executing updater files during `prepare-update`.
Next Step: Split the updater bootstrap/runtime apply responsibilities so the bootstrap layer can safely replace the helper and wrapper scripts before handing off to the new version.
GitHub Issue: not yet

ID: TD-20260416-02
Status: active
Owner: Engine + Agent
Date Added: 2026-04-16
Summary: Linux OS package installation now rides through `bootstrap.sh`, while direct `Borealis.sh` redeploys only verify dependencies until `Update.sh` grows a proper update workflow.
Impact: Local Engine and Agent redeploys stop hammering apt/yum/dnf on every run, but package installation and package upgrade behavior are now split across two scripts instead of one cohesive dependency-management workflow.
Root Cause: `Borealis.sh` previously mixed runtime staging with OS package-manager update/install logic, so every local redeploy could re-check distro repositories even when the host was already provisioned.
Current Mitigation: `bootstrap.sh` now exports `BOREALIS_ALLOW_SYSTEM_PACKAGE_INSTALL=1` so first-run Linux installs can still add missing system packages, while direct `Borealis.sh` runs skip package-manager checks for shared Python/PostgreSQL/WireGuard dependencies and fall back to warnings or explicit errors when prerequisites are absent.
Removal Criteria: Borealis has a first-class dependency/update workflow in `Update.sh` (or equivalent) so bootstrap, redeploy, and package update responsibilities are fully separated without transitional gating flags.
Files: `Borealis.sh`, `bootstrap.sh`, `Docs/getting-started.md`, `Docs/engine-runtime.md`, `Update.sh`
Evidence: Repeated local Engine and Agent redeploys were still running distro package-manager checks (`apt update`, `yum install`, etc.) even when the host was already provisioned, which created unnecessary package-manager load and slowed down routine redeploys.
Next Step: Move package refresh / upgrade semantics into `Update.sh` so bootstrap only covers first-run provisioning and `Borealis.sh` stays focused on runtime staging.
GitHub Issue: not yet

ID: TD-20260416-01
Status: active
Owner: Engine
Date Added: 2026-04-16
Summary: Direct Linux Engine service restarts can relaunch a stale `/Engine` runtime tree instead of freshly staged `Data/Engine` code.
Impact: Operators and developers can pull a new branch tip, restart `borealis-engine.service`, and still observe old backend or WebUI behavior because the live runtime copy under `/Engine` did not refresh from `Data/Engine`.
Root Cause: `borealis-engine.service` executes `Engine/run-engine-service.sh developer`, which launches the Engine from `/opt/Borealis/Engine/...`; runtime staging normally happens through `Borealis.sh`, but a plain systemd restart bypasses that staging flow.
Current Mitigation: Borealis documentation now explicitly tells operators to use `Borealis.sh --EngineDev` or `Borealis.sh --EngineProduction` after source updates so the runtime tree is restaged before the service restarts.
Removal Criteria: The Linux Engine launch path restages `Data/Engine` into `/Engine` automatically on every service start, or the service runs directly from `Data/Engine` without a shadow runtime tree.
Files: `Docs/engine-runtime.md`, `Engine/run-engine-service.sh`
Evidence: On April 16, 2026, `/opt/Borealis/Data/Engine/services/API/devices/vnc.py` and `/opt/Borealis/Data/Engine/services/WebSocket/__init__.py` contained newer VNC latency logic (`ready_profile`, `vnc_socket_prewarm`), but `/opt/Borealis/Engine/Logs/VNC.log` still showed the older `E12 wait_seconds=12.0` / `E15 wait_seconds=8.0` path after `systemctl restart borealis-engine`, and the live runtime files under `/opt/Borealis/Engine/Data/Engine/...` still lacked the new code.
Next Step: Update the Linux Engine startup path so runtime staging happens automatically before every service launch.
GitHub Issue: not yet

ID: TD-20260405-02
Status: active
Owner: WebUI
Date Added: 2026-04-05
Summary: Vite dev/HMR now disables optimized-dependency sourcemaps to suppress Firefox console spam from vendor bundles.
Impact: Borealis app code still debugs normally in Vite dev, but stepping into prebundled `node_modules/.vite/deps/*` vendor code loses source-map support while the mitigation is active.
Root Cause: Firefox reports repeated `No sources are declared in this source map` errors for Vite/esbuild-generated dependency maps, which floods the browser console on pages that load many shared WebUI dependencies even when Borealis app code is healthy.
Current Mitigation: `Data/Engine/web-interface/vite.config.mts` sets `optimizeDeps.esbuildOptions.sourcemap = false` so Vite dev prebundles no longer emit dependency `.map` files that trigger the Firefox console noise.
Removal Criteria: Vite/esbuild/Firefox no longer emit invalid dependency sourcemaps for Borealis dev sessions, or Borealis adopts a narrower upstream-supported ignore mechanism that keeps clean consoles without dropping vendor sourcemaps.
Files: `Data/Engine/web-interface/vite.config.mts`
Evidence: Fresh 2026-04-05 Vite dev/HMR refreshes on `Filter_Editor.jsx` produced dozens of Firefox source-map errors for `/node_modules/.vite/deps/*.js.map` resources such as `react.js.map`, `ag-grid-community.js.map`, and multiple MUI icon bundles, while Borealis page code itself showed no corresponding runtime error.
Next Step: Re-test Firefox against a fresh Vite dev restart and revisit once a newer Vite/esbuild/Firefox combination stops generating the invalid dependency-map warnings.
GitHub Issue: not yet

ID: TD-20260405-01
Status: active
Owner: WebUI
Date Added: 2026-04-05
Summary: Vite dev/HMR now disables `React.StrictMode` to suppress React Flow type-object false positives in the workflow editor.
Impact: Flow Editor browser-console noise is reduced during dev/HMR, but Borealis no longer gets Strict Mode's extra development-only lifecycle checks while the Vite dev shell is active.
Root Cause: React Flow's development warning `error#002` is emitted under the current Strict Mode render pattern even after the workflow node registry and edge defaults are stabilized, so Vite dev sessions still show misleading `nodeTypes or edgeTypes object` warnings.
Current Mitigation: `Data/Engine/web-interface/src/index.jsx` renders `<App />` directly when `import.meta.env.DEV` is true and keeps the existing `React.StrictMode` wrapper outside Vite dev.
Removal Criteria: React Flow no longer emits the false-positive type-object warning under Strict Mode for Borealis's workflow editor, or Borealis adopts a narrower editor-local workaround that preserves global Strict Mode in dev.
Files: `Data/Engine/web-interface/src/index.jsx`, `Data/Engine/web-interface/src/Flow_Editor/Flow_Editor.jsx`, `Data/Engine/web-interface/src/Flow_Editor/nodeRegistry.js`
Evidence: Fresh 2026-04-05 Vite dev/HMR browser console output still showed duplicate React Flow `error#002` warnings from `Flow_Editor.jsx` after the shared node registry refactor, stable default edge options, and stable type refs were already in place.
Next Step: Re-test the Flow Editor in Vite dev/HMR after the conditional Strict Mode removal and revisit once a newer React Flow version or a narrower editor-scoped workaround is available.
GitHub Issue: not yet

ID: TD-20260331-01
Status: active
Owner: Engine + Agent
Date Added: 2026-03-31
Summary: VNC on weaker WireGuard agents can succeed while still triggering repeated `vnc_connect_retry` transport recovery.
Impact: Operators can get a usable VNC session, but the Engine may still force repeated listener recovery for the same agent, which adds noisy logs and leaves weaker hosts more likely to disrupt adjacent remote-access activity.
Root Cause: The VNC bootstrap path retries backend TCP connects aggressively and escalates to forced transport recovery before the shared WireGuard listener has clearly proven healthy for VNC traffic on degraded hosts.
Current Mitigation: `Data/Engine/services/API/devices/vnc.py` re-emits `vpn_tunnel_start` with `vnc_bootstrap` and now confirms successful backend connects with `vnc_backend_connect`, `Data/Engine/services/RemoteDesktop/vnc_proxy.py` delays `vnc_connect_retry` until the backend connect has genuinely stalled, bounds forced recovery to one request per browser session, and suppresses duplicate recovery across closely spaced retries for the same agent, `Data/Engine/services/VPN/vpn_tunnel_service.py` throttles repeated listener recovery, the shell bridge keeps quiet shell sessions off the same stale-transport path with idle keepalives, and `Data/Agent/Roles/role_VNC.py` suppresses the periodic firewall-success log spam so the agent-side `vnc.log` stays readable while the transport issue is still open.
Removal Criteria: VNC connects on degraded agents no longer require repeated `vnc_connect_retry` recovery attempts and do not trigger shared-listener watchdog or recovery churn during otherwise successful sessions.
Files: `Data/Engine/services/API/devices/vnc.py`, `Data/Engine/services/RemoteDesktop/vnc_proxy.py`, `Data/Engine/services/VPN/vpn_tunnel_service.py`, `Data/Agent/Roles/role_VNC.py`
Evidence: Fresh 2026-03-31 engine logs for `LAB-CAMERA-01` showed successful shell and VNC sessions alongside repeated `vpn_transport_recovery_request ... reason=vnc_connect_retry`, `vpn_listener_recovery_attempt`, and one `vpn_transport_watchdog_recovery ... reason=stale_handshake` for the same tunnel. Fresh 2026-04-03 engine logs for `LAB-OPERATOR-01` still showed repeated `vnc_connect_retry` recovery at `11:06:31`, `11:06:41`, `11:06:43`, and `11:06:46` UTC during otherwise successful VNC activity, while the agent `vnc.log` accumulated thousands of periodic firewall-success entries before that no-op logging was removed.
Next Step: Re-test the refreshed Engine + Agent build on a weaker host and confirm the delayed, single-shot, cooldown-limited `vnc_connect_retry` plus `vnc_backend_connect` confirmation fully eliminate repeated shared-listener churn during successful sessions.
GitHub Issue: not yet

ID: TD-20260326-01
Status: active
Owner: Engine
Date Added: 2026-03-26
Summary: Reverse VPN routes now self-heal stale `devices.agent_id` values for guid-bound devices.
Impact: Shell, VNC, and persistent WireGuard recovery continue working when a device row drifts to an old transport identity, but the Engine still depends on route-time repair and socket-registry fallback instead of authoritative device persistence.
Root Cause: Device records can keep a prior `agent_id` even after the same hostname and guid rebind to a newer agent identity, so GUID-based VPN lookups can resolve to dead sockets and unreachable tunnel peers.
Current Mitigation: `Data/Engine/services/API/devices/routes.py` repairs `devices.agent_id` during authenticated `/api/agent/vpn/ensure` and `/api/agent/vnc/ensure`, while `Data/Engine/services/API/devices/tunnel.py` prefers the live system socket for a hostname when the stored binding is stale.
Removal Criteria: Device persistence always stores the authoritative live `agent_id` for each guid/hostname without needing route-time repair or socket-registry fallbacks.
Files: `Data/Engine/services/API/devices/routes.py`, `Data/Engine/services/API/devices/tunnel.py`
Evidence: Engine logs on 2026-03-26 showed `LAB-AIO-01_1B60AFE7-4AA7-4AD8-B18B-23F5F98209EE_SYSTEM` socket registrations while shell, VNC, and VPN lookups still resolved `LAB-AIO-01` to stale `LAB-AIO-01_08FB4B0D-FE6B-4D41-B09B-7947851BFD7A_SYSTEM`.
Next Step: Audit hostname-based device upserts and merge paths so `guid`, `hostname`, and `agent_id` cannot drift out of sync in the `devices` table.
GitHub Issue: not yet

ID: TD-20260325-01
Status: active
Owner: Engine + WebUI
Date Added: 2026-03-25
Summary: Workflow Runtime v1 and workflow-backed Scheduled Jobs ship without retry orchestration.
Impact: Operators can run workflows manually, on schedules, or via webhooks, but Borealis cannot yet retry failed or warning workflow runs at either the per-node level or the scheduled-job level.
Root Cause: Workflow Runtime v1 prioritized deterministic left-to-right execution, frozen target snapshots, and final status rollup before retry semantics were designed across child assemblies, subworkflow calls, and scheduled job history.
Current Mitigation: `Data/Engine/services/workflows/runtime.py` and `Data/Engine/services/API/scheduled_jobs/job_scheduler.py` treat each workflow launch as a single attempt, while the WebUI avoids presenting retry configuration for workflow nodes or workflow-backed scheduled jobs.
Removal Criteria: Borealis supports explicit retry policy design for workflow nodes and scheduled jobs, including retry counts, retry delay/backoff, and final-status accounting across child executions.
Files: `Data/Engine/services/workflows/runtime.py`, `Data/Engine/services/API/scheduled_jobs/job_scheduler.py`, `Data/Engine/web-interface/src/Scheduling/Create_Job.jsx`
Evidence: Workflow Runtime v1 persists terminal statuses and linked child execution summaries, but there is no retry configuration in workflow node configuration, workflow launch APIs, or scheduled job authoring.
Next Step: Design retry semantics for node timeouts/failures and scheduled workflow reruns, then add schema/API/UI support in one coordinated pass.
GitHub Issue: not yet

ID: TD-20260321-01
Status: active
Owner: Agent + Engine
Date Added: 2026-03-21
Summary: Device update-status v1 reuses `devices.agent_hash` to store the installed agent build id.
Impact: Automatic agent updates and the Device Details `Agent Version` status work without a schema migration, but the database column name no longer reflects its broader meaning and could confuse future API/UI work.
Root Cause: Borealis needed a low-friction way to persist the installed agent build id for heartbeat/details reporting and server-side update-status checks while keeping the rollout simple and backward-compatible.
Current Mitigation: Agent payloads now prefer `agent_build_id` / `installed_build_id` externally, while `Data/Engine/services/API/devices/routes.py` and `Data/Engine/services/API/devices/management.py` continue storing that value in `devices.agent_hash` internally and mirror it back out as both `agent_hash` and `agent_build_id`.
Removal Criteria: Borealis introduces a dedicated persisted build/version field (or migration) and removes the dual-purpose use of `devices.agent_hash`.
Files: `Data/Engine/services/API/devices/routes.py`, `Data/Engine/services/API/devices/management.py`, `Data/Agent/agent.py`, `Data/Agent/Roles/role_DeviceAudit.py`
Evidence: The auto-update rollout compares the installed build id from normal heartbeat/details traffic against `/api/repo/current_hash`, but the Engine still persists that value via the legacy `agent_hash` device column.
Next Step: Add a first-class schema field for installed agent build metadata and retire the aliasing layer once existing agents can report the new field reliably.
GitHub Issue: not yet

ID: TD-20260319-05
Status: active
Owner: Engine
Date Added: 2026-03-19
Summary: `agent_service_account.password_encrypted` remains outside Aegis Cipher v1 scope.
Impact: The dormant `agent_service_account` table still uses a legacy encrypted-password column name and would not get Aegis protection automatically if future runtime code started using it without additional migration work.
Root Cause: Aegis Cipher v1 intentionally scopes protected-secret storage to shared credentials and the GitHub token, while `agent_service_account` currently has no active read/write paths.
Current Mitigation: Borealis documents the table as dormant, keeps Aegis-protected storage limited to active secret paths, and records the out-of-scope column in `Docs/db-reference.md`.
Removal Criteria: Either the `agent_service_account` table is removed as dead schema or any future runtime use migrates `password_encrypted` to Aegis or another supported secret-management path.
Files: `Data/Engine/database.py`, `Docs/db-reference.md`
Evidence: `Data/Engine/database.py` still defines `agent_service_account.password_encrypted`, while the active Aegis service only migrates `credentials.*_encrypted` and `github_token.token`.
Next Step: If Borealis activates agent service-account storage, add an explicit secret-management design instead of reusing the dormant column implicitly.
GitHub Issue: not yet

ID: TD-20260319-04
Status: active
Owner: Engine
Date Added: 2026-03-19
Summary: Engine-side Ansible SSH credentials do not yet support passphrase-protected private keys.
Impact: Operators can save SSH key passphrases in Access Management, but shared Ansible SSH runs cannot use those encrypted key files directly and must fall back to password authentication or an unencrypted test key.
Root Cause: Borealis currently stages SSH credentials as `ansible_ssh_private_key_file`, and the Ansible SSH connection plugin does not apply `private_key_passphrase` to file-based keys.
Current Mitigation: `Data/Engine/services/API/scheduled_jobs/job_scheduler.py` normalizes staged SSH key files to LF line endings, rejects passphrase-protected key-only credentials with a clear run error, and falls back to password auth when both password and passphrase-protected key material are present.
Removal Criteria: Borealis supports agentless SSH key passphrases end to end, either through an ssh-agent-backed Ansible launch path or a supported inline-key transport.
Files: `Data/Engine/services/API/scheduled_jobs/job_scheduler.py`, `Data/Engine/services/API/access_management/credentials.py`
Evidence: Job `23` failed with `Load key ".../id_borealis_ssh": error in libcrypto`, and local repro confirmed that CRLF-staged OpenSSH private keys trigger the same libcrypto parse failure on Linux.
Next Step: Decide whether Borealis should add ssh-agent orchestration for shared Ansible runs or continue requiring password / unencrypted test keys for v1 SSH automation.
GitHub Issue: not yet

ID: TD-20260319-03
Status: active
Owner: Engine
Date Added: 2026-03-19
Summary: Shared Ansible remote targeting still relies on a bounded WireGuard readiness window before SSH/WinRM execution.
Impact: If Borealis preflights too soon after re-priming an agent tunnel, healthy remote devices can be skipped as `No Devices Targeted` even though the WireGuard path becomes reachable a few seconds later.
Root Cause: Tunnel session reuse and agent-side `vpn_agent_ensure_response` completion are asynchronous, but the scheduler previously waited only one second before deciding whether remote targets were reachable.
Current Mitigation: `Data/Engine/services/API/scheduled_jobs/management.py` now bootstraps missing shared-run WireGuard sessions using `BOREALIS_AGENT_PUBLIC_BASE_URL` or `BOREALIS_PUBLIC_BASE_URL` when available, otherwise it reuses the endpoint host from any active session, then spends up to `10.0` seconds by default polling for requested-session presence plus healthy listener state, configurable through `BOREALIS_SHARED_ANSIBLE_VPN_PREP_WAIT_SECONDS` and `BOREALIS_SHARED_ANSIBLE_VPN_PREP_POLL_INTERVAL_SECONDS`, before `Data/Engine/services/API/scheduled_jobs/job_scheduler.py` gives freshly prepared SSH/WinRM targets `5` bounded port-preflight attempts with a default `1.0` second retry delay, remote SSH runs `30` Ansible SSH retries by default, and a per-attempt `ansible_ssh_timeout` of `5` seconds by default, configurable through `BOREALIS_SHARED_ANSIBLE_SSH_RETRIES` and `BOREALIS_SHARED_ANSIBLE_SSH_TIMEOUT_SECONDS`. `Data/Engine/services/ansible/runner.py` also enforces a shared-run subprocess timeout of `900` seconds by default through `BOREALIS_SHARED_ANSIBLE_RUN_TIMEOUT_SECONDS` so unreachable targets cannot leave a playbook stuck in `Running` indefinitely, and it isolates SSH trust state to a per-run `ssh_known_hosts` file so stale `/root/.ssh/known_hosts` entries tied to reused WireGuard IPs do not block otherwise healthy targets.
Removal Criteria: Borealis has an explicit tunnel-readiness signal or a scheduler-side readiness poll that removes the need for a fixed sleep-based warm-up window.
Files: `Data/Engine/services/API/scheduled_jobs/management.py`, `Data/Engine/services/API/scheduled_jobs/job_scheduler.py`
Evidence: `Engine/Logs/scheduled_jobs.log` recorded `ssh_unreachable_preflight` for `lab-docs-01` at `2026-03-19 08:44:03 UTC`, while `Engine/Logs/engine.log` did not record `vpn_agent_ensure_response` for that same agent until `2026-03-19 08:44:08 UTC`.
Next Step: Replace the bounded readiness poll with an explicit per-target tunnel-ready signal or peer-handshake visibility from the VPN service.
GitHub Issue: not yet

ID: TD-20260319-02
Status: active
Owner: Engine
Date Added: 2026-03-19
Summary: Device rows do not consistently persist authoritative remote transport metadata.
Impact: Shared Ansible targeting now follows the operator-selected execution context, so missing device transport metadata no longer blocks runs, but Borealis still lacks reliable per-device transport facts for preflight warnings and richer validation.
Root Cause: Agent/device detail payloads do not consistently populate `summary.connection_type`, so the Engine inventory can contain active WireGuard-managed devices with empty `devices.connection_type`.
Current Mitigation: `Data/Engine/services/API/scheduled_jobs/job_scheduler.py` treats the scheduled job execution context as authoritative for shared Ansible transport selection and logs per-target skip reasons when a run resolves no eligible devices.
Removal Criteria: Borealis persists authoritative per-device transport metadata and can surface advisory mismatch warnings without relying on missing or stale device summary fields.
Files: `Data/Engine/services/API/scheduled_jobs/job_scheduler.py`, `Data/Engine/services/API/devices/management.py`, `Data/Agent/agent.py`
Evidence: Remote SSH playbooks against Ubuntu and Rocky Linux devices were skipped as `No Devices Targeted` even while those agents had active WireGuard sessions, because the eligibility gate previously depended on `devices.connection_type`.
Next Step: Update the agent/device detail pipeline to send and persist explicit remote transport metadata, then use it for UI/advisory validation instead of hard scheduler gating.
GitHub Issue: not yet

ID: TD-20260315-01
Status: active
Owner: Engine
Date Added: 2026-03-15
Summary: The Engine runtime is PostgreSQL-only, but some tests and legacy call sites still depend on a DB-API compatibility surface that can hide PostgreSQL-specific behavior.
Impact: Production and deployment paths use PostgreSQL, but some local/unit-test flows can still succeed against non-production adapter paths and may not catch PostgreSQL-only behavior differences.
Root Cause: The codebase still has broad legacy cursor-style usage, and the local test harness has not been fully converted to real PostgreSQL integration coverage yet.
Current Mitigation: `Data/Engine/db/dbapi.py` routes production runtime connections through SQLAlchemy/psycopg while preserving the compatibility surface expected by existing tests and services.
Removal Criteria: Test harnesses run against real PostgreSQL instances end to end and the compatibility branch is no longer needed.
Files: `Data/Engine/db/core.py`, `Data/Engine/db/dbapi.py`, `Data/Engine/Unit_Tests/conftest.py`
Evidence: The workspace currently lacks `sqlalchemy`, `psycopg`, `flask`, and `pytest`, and no local `psql`/`postgres` binaries are present, so PostgreSQL runtime verification cannot happen here without extra bootstrap.
Next Step: Add a real PostgreSQL-backed test harness in CI/local dev and then delete the non-production compatibility path.
GitHub Issue: not yet

ID: TD-20260218-01
Status: active
Owner: Agent + WebUI
Date Added: 2026-02-18
Summary: Device model and serial are temporarily stored inside the persisted `cpu` JSON payload.
Impact: System identity fields are available in the UI, but CPU payload semantics now include non-CPU keys.
Root Cause: The Engine `devices` schema/API persistence path only keeps a subset of summary fields and drops manufacturer/model/serial values from agent details.
Current Mitigation: `role_DeviceAudit.py` writes system identity fields to summary and mirrors them into `cpu` (`system_model`, `system_serial_number`, etc.); `Device_Summary.jsx` reads those keys as fallback.
Removal Criteria: Engine persistence and APIs store/return dedicated system identity fields (or full summary JSON) without relying on CPU metadata.
Files: `Data/Agent/Roles/role_DeviceAudit.py`, `Data/Engine/web-interface/src/Devices/Tabs/Device_Summary.jsx`
Evidence: `Data/Engine/services/API/devices/management.py` `_extract_device_columns` and `get_device_details` only expose selected summary columns.
Next Step: Add first-class model/serial fields to the Engine device schema and API payload builders, then remove CPU fallback keys from agent/UI.
GitHub Issue: not yet

ID: TD-20260210-01
Status: mitigated
Owner: WebUI
Date Added: 2026-02-10
Summary: Vite dev server fails to serve the WebUI when `@novnc/novnc` 1.6.0 is installed.
Impact: Dev UI loads blank or fails to render; production UI continues to work.
Root Cause: noVNC 1.6.0 ships a CommonJS build with a top level `await` in `lib/util/browser.js`; esbuild refuses this during Vite dev prebundle.
Current Mitigation: Postinstall patch replaces the top level `await` with async initialization so esbuild can prebundle.
Removal Criteria: noVNC ships a CJS build without top level await or provides a stable ESM entry that Vite can use without patching.
Files: `Data/Engine/web-interface/package.json`, `Data/Engine/web-interface/scripts/patch-novnc.js`
Evidence: `Engine/Logs/vite-dev.stderr.log` errors referencing top level await in `@novnc/novnc/lib/util/browser.js`.
Next Step: Track upstream noVNC packaging changes and remove patch when safe.
GitHub Issue: not yet

## Related Documentation
- [Engine Runtime](engine-runtime.md)
- [UI and Notifications](ui-and-notifications.md)
