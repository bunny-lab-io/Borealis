# Technical Debt
[Back to Docs Index](index.md) | [Index (HTML)](index.html)

## Purpose
Track technical debt items that affect developer experience, runtime behavior, or maintainability. This document is the staging area for items that will later become GitHub Issues.

Last reviewed: 2026-04-27 by codebase and `Docs/` analysis.

## How To Use This Doc
- Keep ongoing entries in priority order, most urgent first.
- Add new entries to the Ongoing Issues section at the priority that matches current blast radius.
- Move fixed entries to Resolved Issues after code and documentation satisfy removal criteria.
- Use the template below so future agents can understand impact, mitigation, and removal criteria.
- Keep entries concise but action oriented. Link to files and logs instead of pasting large outputs.
- When you open a GitHub Issue, note the issue URL and mark the entry as migrated.

## Logging Rules
- Log any patchy or non-standard workaround, even if it is small.
- Log any behavior that works in production but fails in dev, or vice versa.
- Log any changes that are likely to be removed once upstream or platform fixes land.
- If a change touches dependencies, include the exact version and why it matters.
- Use ASCII only unless the file already contains Unicode.

## Priority Legend
- P0: Security, auth, or deployment-correctness risk that can leave Borealis behavior materially wrong.
- P1: Remote access, update, automation, or database risk that can break operator workflows or hide production-only failures.
- P2: Product capability or schema debt with bounded impact and known mitigation.
- P3: Narrow product-specific workaround, dormant path, or transitional packaging behavior.
- P4: Developer-experience workaround with limited runtime impact.

## Entry Template
```
ID: TD-YYYYMMDD-##
Status: active | mitigated | migrated | resolved
Priority: P0 | P1 | P2 | P3 | P4 | n/a (resolved only)
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

## Current Triage Snapshot
- Resolved after 2026-04-27 review: `TD-20260331-01`, `TD-20260319-03`.
- Still ongoing after 2026-04-27 review: `TD-20260427-01`, `TD-20260416-01`, `TD-20260326-01`, `TD-20260319-04`, `TD-20260419-01`, `TD-20260315-01`, `TD-20260325-01`, `TD-20260321-01`, `TD-20260218-01`, `TD-20260421-01`, `TD-20260416-02`, `TD-20260422-01`, `TD-20260319-02`, `TD-20260319-05`, `TD-20260405-01`, `TD-20260405-02`, `TD-20260210-01`.

## Ongoing Issues
ID: TD-20260427-01
Status: active
Priority: P0
Owner: Engine
Date Added: 2026-04-27
Summary: Directory sync does not yet explicitly evaluate Active Directory disabled or locked account state for cached LDAP users.
Impact: LDAP/LDAPS password login still fails when AD rejects the bind, and Borealis passkeys remain blocked for directory users, but an already-active Borealis session may continue until the cached directory user is disabled by sync or operator action.
Root Cause: The LDAP user lookup path currently resolves identity and groups, but does not request or interpret AD account-control attributes such as `userAccountControl` and `lockoutTime`.
Current Mitigation: Directory users cannot register or use Borealis passkeys, cached directory users can be disabled from Access Management, active requests revalidate `users.directory_disabled`, and provider sync disables users that disappear from LDAP search or lose allowed group membership.
Removal Criteria: Borealis LDAP/LDAPS login and sync request AD account-state attributes, detect disabled/locked accounts, mark the cached directory user disabled, and invalidate active sessions through existing request-time revalidation.
Files: `Data/Engine/services/API/access_management/directory_services.py`, `Data/Engine/services/API/access_management/login.py`, `Data/Engine/services/auth/context.py`, `Docs/security-and-trust.md`
Evidence: `search_user()` only requests identity and group attributes, `sync_provider()` only disables cached users when search fails or role mapping no longer matches, and docs only state directory cache disable / passkey blocking behavior.
Next Step: Add AD account-state attribute parsing, unit tests for disabled and locked users, and operator-visible sync messages for disabled-by-directory-state cases.
GitHub Issue: not yet

ID: TD-20260416-01
Status: active
Priority: P1
Owner: Engine
Date Added: 2026-04-16
Summary: Direct Linux Engine service restarts can relaunch a stale `/Engine` runtime tree instead of freshly staged `Data/Engine` code.
Impact: Operators and developers can pull a new branch tip, restart `borealis-engine.service`, and still observe old backend or WebUI behavior because the live runtime copy under `/Engine` did not refresh from `Data/Engine`.
Root Cause: `borealis-engine.service` executes `Engine/run-engine-service.sh developer`, which launches the Engine from `/opt/Borealis/Engine/...`; runtime staging normally happens through `Borealis.sh`, but a plain systemd restart bypasses that staging flow.
Current Mitigation: Borealis documentation tells operators to use `Borealis.sh --EngineDev` or `Borealis.sh --EngineProduction` after source updates so the runtime tree is restaged before the service restarts.
Removal Criteria: The Linux Engine launch path restages `Data/Engine` into `/Engine` automatically on every service start, or the service runs directly from `Data/Engine` without a shadow runtime tree.
Files: `Docs/engine-runtime.md`, `Engine/run-engine-service.sh`, `Borealis.sh`
Evidence: `Engine/run-engine-service.sh` still points to `${PROJECT_ROOT}/Engine` and does not restage source. `Docs/engine-runtime.md` still warns that direct `systemctl restart borealis-engine` does not restage `Data/Engine` automatically.
Next Step: Update the Linux Engine startup path so runtime staging happens automatically before every service launch.
GitHub Issue: not yet

ID: TD-20260326-01
Status: active
Priority: P1
Owner: Engine
Date Added: 2026-03-26
Summary: Reverse VPN routes still depend on repair/fallback logic for stale `devices.agent_id` values.
Impact: Shell, VNC, and persistent WireGuard recovery continue working when a device row drifts to an old transport identity, but the Engine still depends on route-time repair and socket-registry fallback instead of authoritative device persistence.
Root Cause: Device records can keep a prior `agent_id` even after the same hostname and guid rebind to a newer agent identity, so GUID-based VPN lookups can resolve to dead sockets and unreachable tunnel peers.
Current Mitigation: `Data/Engine/services/API/devices/routes.py` repairs `devices.agent_id` during authenticated `/api/agent/vpn/ensure` and `/api/agent/vnc/ensure`, while `Data/Engine/services/API/devices/tunnel.py` prefers the live system socket for a hostname when the stored binding is stale.
Removal Criteria: Device persistence always stores the authoritative live `agent_id` for each guid/hostname without needing route-time repair or socket-registry fallbacks.
Files: `Data/Engine/services/API/devices/routes.py`, `Data/Engine/services/API/devices/tunnel.py`, `Data/Engine/services/API/devices/management.py`
Evidence: `_repair_agent_id_binding()` and `_resolve_requested_agent_id()` remain active fallback paths, so the removal criteria is not met.
Next Step: Audit hostname-based device upserts and merge paths so `guid`, `hostname`, and `agent_id` cannot drift out of sync in the `devices` table.
GitHub Issue: not yet

ID: TD-20260319-04
Status: active
Priority: P1
Owner: Engine
Date Added: 2026-03-19
Summary: Engine-side Ansible SSH credentials do not yet support passphrase-protected private keys.
Impact: Operators can save SSH key passphrases in Access Management, but shared Ansible SSH runs cannot use those encrypted key files directly and must fall back to password authentication or an unencrypted test key.
Root Cause: Borealis currently stages SSH credentials as `ansible_ssh_private_key_file`, and the Ansible SSH connection plugin does not apply `private_key_passphrase` to file-based keys.
Current Mitigation: `Data/Engine/services/API/scheduled_jobs/job_scheduler.py` normalizes staged SSH key files to LF line endings, rejects passphrase-protected key-only credentials with a clear run error, and falls back to password auth when both password and passphrase-protected key material are present.
Removal Criteria: Borealis supports agentless SSH key passphrases end to end, either through an ssh-agent-backed Ansible launch path or a supported inline-key transport.
Files: `Data/Engine/services/API/scheduled_jobs/job_scheduler.py`, `Data/Engine/services/API/access_management/credentials.py`, `Data/Engine/Unit_Tests/test_scheduled_jobs_api.py`
Evidence: The scheduler still emits `credential_private_key_passphrase_unsupported` and tests assert that passphrase-only keys fail with "Passphrase-protected SSH private keys are not yet supported".
Next Step: Decide whether Borealis should add ssh-agent orchestration for shared Ansible runs or continue requiring password / unencrypted test keys for v1 SSH automation.
GitHub Issue: not yet

ID: TD-20260419-01
Status: mitigated
Priority: P1
Owner: Agent + Engine
Date Added: 2026-04-19
Summary: Engine-managed zip apply intentionally preserves the currently running updater wrapper scripts, so `Update.sh` and `Update.ps1` can take one extra update cycle to land on agents.
Impact: The release-channel system can stage and apply most source changes immediately, but top-level updater wrapper-script fixes may still lag because the currently running wrapper is preserved until the next update pass.
Root Cause: The agent applies full-repo zip artifacts in place while the PowerShell/shell wrapper scripts are still executing, so overwriting those files mid-run risks corrupting the active update process.
Current Mitigation: `Data/Agent/update_helper.py` excludes only `Update.sh` and `Update.ps1` from staged archive replacement, while the Engine-managed manifest flow updates the Python helper/runtime files together and records channel-aware update state.
Removal Criteria: Borealis gains a two-phase updater bootstrap path, atomic updater self-replacement flow, or a dedicated external updater binary/service that can safely swap updater-tooling files during the same release.
Files: `Data/Agent/update_helper.py`, `Update.sh`, `Update.ps1`
Evidence: `_SYNC_EXCLUDED_RELATIVE = frozenset({"Update.ps1", "Update.sh"})` remains in `Data/Agent/update_helper.py`; `Data/Agent/update_helper.py` itself is not excluded.
Next Step: Split the updater bootstrap/runtime apply responsibilities so the bootstrap layer can safely replace wrapper scripts before handing off to the new version.
GitHub Issue: not yet

ID: TD-20260315-01
Status: active
Priority: P1
Owner: Engine
Date Added: 2026-03-15
Summary: The Engine runtime is PostgreSQL-only, but tests and legacy call sites still depend on a DB-API compatibility surface that can hide PostgreSQL-specific behavior.
Impact: Production and deployment paths use PostgreSQL, but local/unit-test flows can still succeed against adapter paths and SQLite fixtures that may not catch PostgreSQL-only behavior differences.
Root Cause: The codebase still has broad legacy cursor-style usage, and the local test harness has not been fully converted to real PostgreSQL integration coverage yet.
Current Mitigation: `Data/Engine/db/dbapi.py` routes production runtime connections through SQLAlchemy/psycopg while preserving the compatibility surface expected by existing tests and services.
Removal Criteria: Test harnesses run against real PostgreSQL instances end to end and the compatibility branch is no longer needed.
Files: `Data/Engine/db/core.py`, `Data/Engine/db/dbapi.py`, `Data/Engine/Unit_Tests/conftest.py`, `Data/Engine/database.py`
Evidence: `Data/Engine/Unit_Tests/conftest.py` still imports `Data.Engine.db.dbapi as sqlite3` and builds SQLite schema fixtures, while `Data/Engine/db/dbapi.py` still translates SQLite-style SQL and exposes a SQLite fallback branch.
Next Step: Add a real PostgreSQL-backed test harness in CI/local dev and then delete the non-production compatibility path.
GitHub Issue: not yet

ID: TD-20260325-01
Status: active
Priority: P2
Owner: Engine + WebUI
Date Added: 2026-03-25
Summary: Workflow Runtime v1 and workflow-backed Scheduled Jobs ship without retry orchestration.
Impact: Operators can run workflows manually, on schedules, or via webhooks, but Borealis cannot yet retry failed or warning workflow runs at either the per-node level or the scheduled-job level.
Root Cause: Workflow Runtime v1 prioritized deterministic left-to-right execution, frozen target snapshots, and final status rollup before retry semantics were designed across child assemblies, subworkflow calls, and scheduled job history.
Current Mitigation: `Data/Engine/services/workflows/runtime.py` and `Data/Engine/services/API/scheduled_jobs/job_scheduler.py` treat each workflow launch as a single attempt, while the WebUI avoids presenting retry configuration for workflow nodes or workflow-backed scheduled jobs.
Removal Criteria: Borealis supports explicit retry policy design for workflow nodes and scheduled jobs, including retry counts, retry delay/backoff, and final-status accounting across child executions.
Files: `Data/Engine/services/workflows/runtime.py`, `Data/Engine/services/API/scheduled_jobs/job_scheduler.py`, `Data/Engine/web-interface/src/Scheduling/Create_Job.jsx`, `Data/Engine/web-interface/src/Flow_Editor/`
Evidence: Workflow Runtime persists terminal statuses, timeout data, and linked child execution summaries, but there is no retry policy in workflow node configuration, workflow launch APIs, or scheduled job authoring.
Next Step: Design retry semantics for node timeouts/failures and scheduled workflow reruns, then add schema/API/UI support in one coordinated pass.
GitHub Issue: not yet

ID: TD-20260321-01
Status: active
Priority: P2
Owner: Engine + Agent
Date Added: 2026-03-21
Summary: Device update-status v1 reuses `devices.agent_hash` to store the installed agent build id.
Impact: Automatic agent updates and the Device Details `Agent Version` status work without a schema migration, but the database column name no longer reflects its broader meaning and could confuse future API/UI work.
Root Cause: Borealis needed a low-friction way to persist the installed agent build id for heartbeat/details reporting and server-side update-status checks while keeping the rollout simple and backward-compatible.
Current Mitigation: Agent payloads now prefer `agent_build_id` / `installed_build_id` externally, while `Data/Engine/services/API/devices/routes.py` and `Data/Engine/services/API/devices/management.py` continue storing that value in `devices.agent_hash` internally and mirror it back out as both `agent_hash` and `agent_build_id`.
Removal Criteria: Borealis introduces a dedicated persisted build/version field or migration and removes the dual-purpose use of `devices.agent_hash`.
Files: `Data/Engine/services/API/devices/routes.py`, `Data/Engine/services/API/devices/management.py`, `Data/Agent/agent.py`, `Data/Agent/Roles/role_system_device_auditor.py`, `Docs/db-reference.md`
Evidence: `Docs/db-reference.md` still lists `agent_hash` as the persisted `devices` column, and Engine routes still update `agent_hash` from `agent_build_id` / `installed_build_id`.
Next Step: Add a first-class schema field for installed agent build metadata and retire the aliasing layer once existing agents can report the new field reliably.
GitHub Issue: not yet

ID: TD-20260218-01
Status: active
Priority: P2
Owner: Agent + Engine + WebUI
Date Added: 2026-02-18
Summary: Device model and serial are temporarily mirrored inside the persisted `cpu` JSON payload.
Impact: System identity fields are available in the UI, but CPU payload semantics now include non-CPU keys and Engine persistence still does not expose dedicated identity columns.
Root Cause: The Engine `devices` schema/API persistence path only keeps a subset of summary fields and drops manufacturer/model/serial values from first-class columns.
Current Mitigation: `role_system_device_auditor.py` writes system identity fields to summary and mirrors them into `cpu` (`system_model`, `system_serial_number`, etc.); `Device_Summary.jsx` reads summary values first and CPU identity keys as fallback.
Removal Criteria: Engine persistence and APIs store/return dedicated system identity fields or full summary JSON without relying on CPU metadata.
Files: `Data/Agent/Roles/role_system_device_auditor.py`, `Data/Engine/services/API/devices/management.py`, `Data/Engine/web-interface/src/Devices/Tabs/Device_Summary.jsx`, `Docs/db-reference.md`
Evidence: `_extract_device_columns()` still only persists selected summary columns and special-cases `cpu` from `summary.get("cpu")`; `Docs/db-reference.md` still lists no dedicated manufacturer/model/serial columns on `devices`.
Next Step: Add first-class model/serial fields to the Engine device schema and API payload builders, then remove CPU fallback keys from agent/UI.
GitHub Issue: not yet

ID: TD-20260421-01
Status: mitigated
Priority: P2
Owner: Engine + Agent + WebUI
Date Added: 2026-04-21
Summary: Windows software uninstall v1 falls back to pattern-based silent-command derivation when inventory lacks a true quiet uninstall string.
Impact: Operators can queue many common Windows uninstalls directly from Device Details, but some products still cannot be removed silently unless the registry exposes `QuietUninstallString`, matches a bounded Borealis rule, or has an operator-approved override.
Root Cause: Windows uninstall inventory is vendor-defined, and many entries expose only interactive `UninstallString` values without a standardized silent equivalent.
Current Mitigation: `role_system_software_management.py` publishes persisted software inventory metadata plus AppX `non_removable`, the Engine resolves a per-row uninstall capability object when device details are loaded, the Installed Software tab only trusts that backend capability, and `Data/Engine/services/API/devices/software_uninstall.py` limits fallback silent derivation to MSI product codes, built-in installer patterns, a small explicit rule catalog, hotloaded uninstall overrides, and an unsafe-quiet-string blocklist before failing with a clear operator-visible reason.
Removal Criteria: Borealis gains broader installer-family detection or a richer uninstall metadata pipeline that can derive silent commands reliably without pattern heuristics.
Files: `Data/Agent/Roles/role_system_software_management.py`, `Data/Engine/services/API/devices/software_uninstall.py`, `Data/Engine/services/API/devices/services.py`, `Data/Engine/web-interface/src/Devices/Tabs/Installed_Software.jsx`, `Docs/Software Management/adding-software-to-uninstall-overrides.md`, `Docs/Software Management/adding-software-to-uninstall-blocklist.md`
Evidence: The uninstall resolver still prefers `QuietUninstallString`, MSI product codes, existing quiet flags, and recognized installer families/rules, with override/blocklist JSON stores used for operator-approved exceptions.
Next Step: Expand supported installer-family detection with test coverage from real fleet inventory, or persist installer classification from the agent so the Engine does not need to infer quiet flags from raw registry strings.
GitHub Issue: not yet

ID: TD-20260416-02
Status: mitigated
Priority: P3
Owner: Engine + Agent
Date Added: 2026-04-16
Summary: Linux OS package installation now rides through `bootstrap.sh`, while direct `Borealis.sh` redeploys only verify dependencies until `Update.sh` grows a proper update workflow.
Impact: Local Engine and Agent redeploys stop hammering apt/yum/dnf on every run, but package installation and package upgrade behavior are now split across scripts instead of one cohesive dependency-management workflow.
Root Cause: `Borealis.sh` previously mixed runtime staging with OS package-manager update/install logic, so every local redeploy could re-check distro repositories even when the host was already provisioned.
Current Mitigation: `bootstrap.sh` exports `BOREALIS_ALLOW_SYSTEM_PACKAGE_INSTALL=1` so first-run Linux installs can still add missing system packages, while direct `Borealis.sh` runs skip package-manager checks for shared Python/PostgreSQL/WireGuard dependencies and fall back to warnings or explicit errors when prerequisites are absent.
Removal Criteria: Borealis has a first-class dependency/update workflow in `Update.sh` or equivalent so bootstrap, redeploy, and package update responsibilities are fully separated without transitional gating flags.
Files: `Borealis.sh`, `bootstrap.sh`, `Docs/getting-started.md`, `Docs/engine-runtime.md`, `Update.sh`
Evidence: `bootstrap.sh` still exports `BOREALIS_ALLOW_SYSTEM_PACKAGE_INSTALL=1`, `Borealis.sh` still gates system package install behind that environment flag, and `Docs/getting-started.md` documents that direct redeploys avoid package-manager checks.
Next Step: Move package refresh / upgrade semantics into `Update.sh` so bootstrap only covers first-run provisioning and `Borealis.sh` stays focused on runtime staging.
GitHub Issue: not yet

ID: TD-20260422-01
Status: mitigated
Priority: P3
Owner: Engine
Date Added: 2026-04-22
Summary: Borealis blocks Fedora Media Writer's registry `QuietUninstallString` because the vendor-provided `/S` command still shows an interactive confirmation prompt.
Impact: Operators no longer get a misleading queued/running uninstall for Fedora Media Writer, but Borealis carries a product-specific uninstall blocklist entry until a verified unattended command is found.
Root Cause: Windows uninstall metadata is vendor-defined, and Fedora Media Writer 5.2.8 registers `"C:\Program Files\Fedora Media Writer\uninstall.exe" /S` as `QuietUninstallString` even though manual validation still opens a confirmation prompt instead of uninstalling silently.
Current Mitigation: `Data/Engine/services/API/devices/software_uninstall.py` loads `Data/Engine/services/API/devices/software_uninstall_blocklist.json`, matches Fedora Media Writer's known-bad quiet metadata, and returns an unsupported uninstall capability with a clear operator-visible reason rather than dispatching the command to the agent. A verified operator override can replace the block through `software_uninstall_overrides.json`.
Removal Criteria: Borealis verifies a truly unattended Fedora Media Writer uninstall command or upstream fixes the registered quiet uninstall metadata so the existing resolver can trust it again.
Files: `Data/Engine/services/API/devices/software_uninstall.py`, `Data/Engine/services/API/devices/software_uninstall_blocklist.json`, `Data/Engine/services/API/devices/software_uninstall_overrides.json`, `Data/Engine/Unit_Tests/test_devices_api.py`, `Docs/Software Management/adding-software-to-uninstall-blocklist.md`
Evidence: `software_uninstall_blocklist.json` still ships `fedora_media_writer_quiet_string_interactive`, while `software_uninstall_overrides.json` has no shipped Fedora override.
Next Step: Validate whether the bundled Qt/maintenance tool supports a documented headless uninstall mode for Fedora Media Writer, and replace the blocklist entry with a verified command if so.
GitHub Issue: not yet

ID: TD-20260319-02
Status: mitigated
Priority: P3
Owner: Engine
Date Added: 2026-03-19
Summary: Device rows do not consistently persist authoritative remote transport metadata.
Impact: Shared Ansible targeting follows the operator-selected execution context, so missing device transport metadata no longer blocks runs, but Borealis still lacks reliable per-device transport facts for preflight warnings and richer validation.
Root Cause: Agent/device detail payloads do not consistently populate `summary.connection_type`, so the Engine inventory can contain active WireGuard-managed devices with empty `devices.connection_type`.
Current Mitigation: `Data/Engine/services/API/scheduled_jobs/job_scheduler.py` treats the scheduled job execution context as authoritative for shared Ansible transport selection and logs per-target skip reasons when a run resolves no eligible devices.
Removal Criteria: Borealis persists authoritative per-device transport metadata and can surface advisory mismatch warnings without relying on missing or stale device summary fields.
Files: `Data/Engine/services/API/scheduled_jobs/job_scheduler.py`, `Data/Engine/services/API/devices/management.py`, `Data/Agent/agent.py`, `Docs/scheduled-jobs.md`, `Docs/engine-runtime.md`
Evidence: `Data/Agent/agent.py` reports `service_mode`, but not durable SSH/WinRM transport metadata in device details. Docs still state scheduled jobs use execution context instead of stored `connection_type`.
Next Step: Update the agent/device detail pipeline to send and persist explicit remote transport metadata, then use it for UI/advisory validation instead of hard scheduler gating.
GitHub Issue: not yet

ID: TD-20260319-05
Status: mitigated
Priority: P3
Owner: Engine
Date Added: 2026-03-19
Summary: `agent_service_account.password_encrypted` remains outside Aegis Cipher v1 scope.
Impact: The dormant `agent_service_account` table still uses a legacy encrypted-password column name and would not get Aegis protection automatically if future runtime code started using it without additional migration work.
Root Cause: Aegis Cipher v1 intentionally scopes protected-secret storage to shared credentials and the GitHub token, while `agent_service_account` currently has no active read/write paths.
Current Mitigation: Borealis documents the table as dormant, keeps Aegis-protected storage limited to active secret paths, and records the out-of-scope column in `Docs/db-reference.md`.
Removal Criteria: Either the `agent_service_account` table is removed as dead schema or any future runtime use migrates `password_encrypted` to Aegis or another supported secret-management path.
Files: `Data/Engine/database.py`, `Data/Engine/services/API/devices/management.py`, `Docs/db-reference.md`
Evidence: `Data/Engine/database.py` still defines `agent_service_account.password_encrypted`, and `Docs/db-reference.md` still says the column remains outside Aegis Cipher v1 scope because the table has no active runtime paths today.
Next Step: If Borealis activates agent service-account storage, add an explicit secret-management design instead of reusing the dormant column implicitly.
GitHub Issue: not yet

ID: TD-20260405-01
Status: mitigated
Priority: P4
Owner: WebUI
Date Added: 2026-04-05
Summary: Vite dev/HMR disables `React.StrictMode` to suppress React Flow type-object false positives in the workflow editor.
Impact: Flow Editor browser-console noise is reduced during dev/HMR, but Borealis no longer gets Strict Mode's extra development-only lifecycle checks while the Vite dev shell is active.
Root Cause: React Flow's development warning `error#002` is emitted under the current Strict Mode render pattern even after the workflow node registry and edge defaults are stabilized, so Vite dev sessions still show misleading `nodeTypes or edgeTypes object` warnings.
Current Mitigation: `Data/Engine/web-interface/src/index.jsx` renders `<App />` directly when `import.meta.env.DEV` is true and keeps the existing `React.StrictMode` wrapper outside Vite dev.
Removal Criteria: React Flow no longer emits the false-positive type-object warning under Strict Mode for Borealis's workflow editor, or Borealis adopts a narrower editor-local workaround that preserves global Strict Mode in dev.
Files: `Data/Engine/web-interface/src/index.jsx`, `Data/Engine/web-interface/src/Flow_Editor/Flow_Editor.jsx`, `Data/Engine/web-interface/src/Flow_Editor/Flow_Editor_Canvas.jsx`, `Data/Engine/web-interface/src/Flow_Editor/nodeRegistry.js`
Evidence: `src/index.jsx` still conditionally omits `<React.StrictMode>` in Vite dev, while the Flow Editor still passes stable `nodeTypes` and `edgeTypes` refs.
Next Step: Re-test the Flow Editor in Vite dev/HMR and revisit once a newer React Flow version or a narrower editor-scoped workaround is available.
GitHub Issue: not yet

ID: TD-20260405-02
Status: mitigated
Priority: P4
Owner: WebUI
Date Added: 2026-04-05
Summary: Vite dev/HMR disables optimized-dependency sourcemaps to suppress Firefox console spam from vendor bundles.
Impact: Borealis app code still debugs normally in Vite dev, but stepping into prebundled `node_modules/.vite/deps/*` vendor code loses source-map support while the mitigation is active.
Root Cause: Firefox reports repeated `No sources are declared in this source map` errors for Vite/esbuild-generated dependency maps, which floods the browser console on pages that load many shared WebUI dependencies even when Borealis app code is healthy.
Current Mitigation: `Data/Engine/web-interface/vite.config.mts` sets `optimizeDeps.esbuildOptions.sourcemap = false` so Vite dev prebundles no longer emit dependency `.map` files that trigger the Firefox console noise.
Removal Criteria: Vite/esbuild/Firefox no longer emit invalid dependency sourcemaps for Borealis dev sessions, or Borealis adopts a narrower upstream-supported ignore mechanism that keeps clean consoles without dropping vendor sourcemaps.
Files: `Data/Engine/web-interface/vite.config.mts`
Evidence: `vite.config.mts` still contains `optimizeDeps.esbuildOptions.sourcemap = false`.
Next Step: Re-test Firefox against a fresh Vite dev restart and revisit once a newer Vite/esbuild/Firefox combination stops generating invalid dependency-map warnings.
GitHub Issue: not yet

ID: TD-20260210-01
Status: mitigated
Priority: P4
Owner: WebUI
Date Added: 2026-02-10
Summary: Vite dev server fails to serve the WebUI when `@novnc/novnc` 1.6.0 is installed unless Borealis patches the package after install.
Impact: Dev UI loads blank or fails to render without the patch; production UI continues to work.
Root Cause: noVNC 1.6.0 ships a CommonJS build with a top-level `await` in `lib/util/browser.js`; esbuild refuses this during Vite dev prebundle.
Current Mitigation: `Data/Engine/web-interface/package.json` pins `@novnc/novnc` 1.6.0 and runs `node scripts/patch-novnc.js` from `postinstall` to replace the top-level `await` with async initialization so esbuild can prebundle.
Removal Criteria: noVNC ships a CJS build without top-level await or provides a stable ESM entry that Vite can use without patching.
Files: `Data/Engine/web-interface/package.json`, `Data/Engine/web-interface/scripts/patch-novnc.js`
Evidence: `package.json` still pins `@novnc/novnc` to `1.6.0` and still runs `patch-novnc.js` in `postinstall`.
Next Step: Track upstream noVNC packaging changes and remove the patch when safe.
GitHub Issue: not yet

## Resolved Issues
ID: TD-20260319-03
Status: resolved
Priority: n/a
Owner: Engine + Agent
Date Added: 2026-03-19
Resolved: 2026-04-27
Summary: Shared Ansible remote targeting previously relied on a bounded WireGuard readiness window before SSH/WinRM execution.
Impact: If Borealis preflighted too soon after re-priming an agent tunnel, healthy remote devices could be skipped or delayed even though the WireGuard path became reachable a few seconds later.
Root Cause: Tunnel session reuse and agent-side tunnel application were asynchronous, while scheduler-side readiness treated session/listener presence as enough proof for dispatch.
Current Mitigation: No active mitigation required beyond the readiness callback path.
Removal Criteria: Met. Agents call `/api/agent/vpn/ready` after applying the active WireGuard config and local firewall allowlist, `VpnTunnelService.wait_for_sessions_ready()` waits for that signal for the current tunnel and required ports, and scheduled SSH/WinRM targets with `dispatch_ready=false` are skipped as `wireguard_not_ready` instead of entering preflight or inventory early.
Files: `Data/Agent/Roles/role_system_wireguard.py`, `Data/Engine/services/API/devices/routes.py`, `Data/Engine/services/VPN/vpn_tunnel_service.py`, `Data/Engine/services/API/scheduled_jobs/management.py`, `Data/Engine/services/API/scheduled_jobs/job_scheduler.py`, `Docs/scheduled-jobs.md`, `Docs/vpn-and-remote-access.md`
Evidence: `Data/Agent/Roles/role_system_wireguard.py` posts `/api/agent/vpn/ready`; `Data/Engine/services/VPN/vpn_tunnel_service.py` records `agent_ready` / `dispatch_ready`; `Data/Engine/services/API/scheduled_jobs/management.py` waits through `wait_for_sessions_ready()`; `Data/Engine/services/API/scheduled_jobs/job_scheduler.py` handles `wireguard_not_ready`.
Next Step: Reopen with a new debt entry only if fresh scheduled Ansible logs show `wireguard_not_ready` for agents that subsequently report readiness in the same run window.
GitHub Issue: not yet

ID: TD-20260331-01
Status: resolved
Priority: n/a
Owner: Agent + Engine
Date Added: 2026-03-31
Resolved: 2026-04-27
Summary: VNC on weaker WireGuard agents previously succeeded while still triggering repeated `vnc_connect_retry` transport recovery.
Impact: Operators could get a usable VNC session, but the Engine could force repeated listener recovery for the same agent, adding noisy logs and disrupting adjacent remote-access activity.
Root Cause: The VNC bootstrap path retried backend TCP connects aggressively and escalated to forced transport recovery before the shared WireGuard listener had clearly proven healthy for VNC traffic on degraded hosts.
Current Mitigation: No active mitigation required beyond the current VNC proxy and tunnel-service behavior.
Removal Criteria: Met. VNC recovery is delayed, bounded to one forced retry per browser session, cooled down per agent, and successful backend connects confirm transport health.
Files: `Data/Engine/services/API/devices/vnc.py`, `Data/Engine/services/RemoteDesktop/vnc_proxy.py`, `Data/Engine/services/VPN/vpn_tunnel_service.py`, `Data/Agent/Roles/role_system_vnc.py`, `Docs/vpn-and-remote-access.md`, `Data/Engine/Unit_Tests/test_vnc_proxy.py`, `Data/Engine/Unit_Tests/test_vnc_api.py`
Evidence: `VncProxyServer` uses `_CONNECT_RECOVERY_MAX_ATTEMPTS = 1`, `_CONNECT_RECOVERY_COOLDOWN_SECONDS = 20.0`, confirms success with `vnc_backend_connect`, and unit tests assert bounded retry behavior. `Docs/vpn-and-remote-access.md` says repeated `vnc_connect_retry` after updated Engine redeploy should be treated as a fresh regression.
Next Step: Reopen with a new debt entry only if fresh logs after redeploy show repeated `vnc_connect_retry` recovery during otherwise successful VNC sessions.
GitHub Issue: not yet

## Related Documentation
- [Engine Runtime](engine-runtime.md)
- [UI and Notifications](ui-and-notifications.md)
- [Scheduled Jobs](scheduled-jobs.md)
- [VPN and Remote Access](vpn-and-remote-access.md)
- [Database Reference](db-reference.md)
