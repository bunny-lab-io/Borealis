# Patch Management

Patch Management shows Windows patch inventory collected by Borealis agents and controls policy-driven Windows update installs. Operators can still schedule ad-hoc installs, but recurring automation should use Patch Policies so Global, site, and device/filter overrides stay auditable.

Patch Management has three tabs:

- `Patch List` shows available and installed Windows updates.
- `Site Policies` controls site-scoped maintenance windows and role scope.
- `Device / Filter Policies` applies deepest overrides for explicit devices or dynamic device filters.

## Open Fleet Patch Audit

1. Open `Alerting & Reporting > Patch Management`.
2. Use `State` to switch between pending and installed inventory.
3. Use `Severity` to narrow Windows Update Agent rows when severity is available.
4. Select a blue KB number to open Microsoft Update Catalog search results in a new browser tab.
5. Select device counts when you need to jump back to Device Inventory for affected endpoints.
6. Use `Install` on a pending row to open a Schedule-only Scheduled Job draft for every visible device with that update pending. After the job is created, Borealis returns to Patch Management.
7. Select two or more pending rows and use `Bulk Install` to open a Schedule-only draft that creates separate one-KB jobs sharing the same immediate or one-time schedule. After the jobs are created, Borealis returns to Patch Management.

Scheduled patch-install jobs appear under the Scheduled Jobs `Patch Management` filter instead of the default `Normal` job list.

Site-scoped navigation keeps the selected site in the URL as `?site=<site_id>` so operators with assigned sites only see patch inventory they can access.

## Configure Patch Policies

1. Open `Alerting & Reporting > Patch Management`.
2. Select `Site Policies` or `Device / Filter Policies`.
3. Create a policy and choose its scope.
4. Choose the install schedule. Times use the Engine host timezone.
5. Set deferral days. Borealis waits from the update `published_at` timestamp, or from first-seen catalog time when Microsoft does not provide `published_at`.
6. Keep `Managed Windows Update` enabled when Borealis should prevent devices from installing updates on their own.
7. Add allow or block rules for severity, classification, category, KB, update ID, or patch key.
8. Save the policy. Borealis blocks same-layer coverage conflicts and asks for confirmation before a child policy overrides a parent block rule.

The Global Patch Policy is created automatically during Engine database initialization. It is locked from deletion, enabled by default, uses Wednesday 2:00 AM Engine-local time, defers updates for 14 days, applies conservative MSP approvals, enables managed Windows Update mode, and leaves reboot-after-install off.

Site policies target one or more sites plus a role scope: `Server`, `Workstation`, or `Both`. One site and role combination can only be covered by one enabled site policy.

Device / Filter policies override site and global policy behavior. Direct same-layer overlaps are blocked at save time. Dynamic filter overlaps mark affected devices as conflicted during evaluation so automation skips them instead of guessing which policy should win.

## Exclusions and Reboots

Use `Unmanaged` when Borealis should do no patch installs and no Windows Update enforcement for a device. Use `Frozen` when Borealis should not install updates, but the Agent should still enforce Windows Update lock/freeze settings. Excluded devices still count as covered during conflict checks.

Reboots are off by default. If enabled, reboot policy can use a separate schedule. Agents skip rebooting devices with a logged-in user unless the policy explicitly allows forced logged-in-user reboots.

## Read Device Patch Inventory

1. Open `Inventory > Devices`.
2. Select a device hostname.
3. Select `Patch Management` from the Device Summary sidebar.
4. Use `Query Patch Inventory` when you need a fresh Windows Update Agent and installed KB snapshot.
5. Select a blue KB number to open Microsoft Update Catalog search results in a new browser tab.
6. Use `Install` on a pending row to open a Schedule-only Scheduled Job draft for that device and selected update. After the job is created, Borealis returns to the device Patch Management tab.
7. Select two or more pending rows and use `Bulk Install` to open a Schedule-only draft that creates separate one-KB jobs for that device with shared timing. After the jobs are created, Borealis returns to the device Patch Management tab.

Pending rows come from Windows Update Agent search results that are not installed and not hidden. Installed rows come from `Get-HotFix` and Windows Update Agent history, then Borealis de-duplicates them by KB or update identity.

!!! info

    Windows Update Agent often leaves severity empty even when classification is populated. Borealis displays those rows as `Unspecified` instead of guessing severity from classification.

!!! warning

    Patch Management ad-hoc install jobs do not approve updates, force reboots, or replace existing patching policy tooling. Offline agents at execution time remain visible in Scheduled Job results so operators can retry or reschedule after the device returns.

!!! info

    Scheduled patch install jobs show live Windows Update Agent progress in the Scheduled Jobs current-run device rows. Running rows display labels such as `Downloading 42%` and `Installing 18%`. WUA progress is Microsoft-reported and can pause, jump, or stay on one percentage for large updates.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - `GET /api/patches/audit` - fleet patch inventory, scoped to operator site access. Rows include `active_install_job` when an enabled scheduled patch install already owns that patch.
    - `GET /api/device/patches/<hostname>` - device patch inventory, scoped to operator site access.
    - `POST /api/device/patches/<hostname>/refresh` - queue `patch_inventory_refresh_request` over the device SYSTEM socket.
    - `GET /api/patches/policies` - list patch policies, optionally filtered by `type=site` or `type=device_filter`.
    - `POST /api/patches/policies` - create a patch policy.
    - `GET /api/patches/policies/metadata` - load policy editor metadata for sites, filters, rule types, role scopes, and defaults.
    - `GET /api/patches/policies/<policy_id>` - load one policy.
    - `PUT /api/patches/policies/<policy_id>` - update one policy.
    - `DELETE /api/patches/policies/<policy_id>` - delete one unlocked policy. Global policy returns a locked-policy error.
    - `POST /api/patches/policies/<policy_id>/preview` - resolve target coverage, conflicts, and parent-block override warnings.
    - `GET /api/patches/policies/effective?hostname=<hostname>` - show effective hierarchy for one device.
    - `POST /api/patches/policies/evaluate` - manually evaluate due patches for one policy or all enabled policies.
    - `POST /api/scheduled_jobs` with `job_kind=patch_install` - create an ad-hoc patch install job from the Patch Management install flow. Bulk and policy flows call this once per selected patch identity.
    - `POST /api/agent/patches/install-progress` - device-authenticated Agent progress update for scheduled patch installs. The Engine stores latest progress in scheduled activity metadata and emits `scheduled_job_patch_progress`.
    - `POST /api/agent/details` - accepts `details.patches` from the Agent `patch_management` role.

    ### Related documentation

    - [Device Auditing](device-auditing.md)
    - [Agent Runtime](../Reference/Core%20Runtimes/agent-runtime.md)
    - [API Reference](../Reference/Data%20and%20Schema/api-reference.md)
    - [Database Reference](../Reference/Data%20and%20Schema/db-reference.md)

    ### Source map

    - Agent role: `Data/Agent/internal/roles/patch_management/`
    - Agent runtime wiring: `Data/Agent/internal/runtime/runtime.go`
    - Engine ingestion: `Data/Engine/Containers/api-backend/cmd/api-backend/agent_details.go`
    - Engine API routes: `Data/Engine/Containers/api-backend/cmd/api-backend/patches.go`
    - Engine scheduler routes: `Data/Engine/Containers/api-backend/cmd/api-backend/scheduled_jobs*.go`
    - Engine patch scheduler worker: `Data/Engine/Containers/api-backend/cmd/api-backend/scheduler_patch_install.go`
    - Engine patch policy API and evaluator: `Data/Engine/Containers/api-backend/cmd/api-backend/patch_policies.go`
    - Engine patch policy scheduler hook: `Data/Engine/Containers/api-backend/cmd/api-backend/scheduler_patch_policies.go`
    - Engine schema bootstrap: `Data/Engine/Containers/api-backend/data/database.py`
    - Job creation UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Scheduling/Create_Job.jsx`
    - Fleet UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Patches/Patch_Management.jsx`
    - Device tab UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Patch_Management.jsx`

    ### Runtime behavior

    - Windows agents collect pending updates through native Windows Update Agent COM and installed KB rows through `Get-HotFix` plus WUA history.
    - Pending rows come from WUA `IsInstalled=0 and IsHidden=0`, so they can be available but not downloaded yet. Download status stays in `is_downloaded`.
    - Install buttons do not trigger WUA directly. They open `Create_Job.jsx` with a `patch_install` component, selected patch metadata, and frozen targets prefilled.
    - Patch install drafts show only the `Schedule` tab during creation. Job name, target, assembly, and execution-context tabs stay hidden because Patch Management owns those values.
    - Patch install drafts carry an internal `return_to` path so successful creation returns to the originating fleet or device Patch Management route.
    - KB cells link to `https://www.catalog.update.microsoft.com/Search.aspx?q=<KB>` in a new tab when the row has a normalized KB value.
    - Bulk Install sends multiple selected patch items into `Create_Job.jsx`. Create Job keeps schedule settings shared, then creates one `job_kind=patch_install` scheduled job per selected patch.
    - Global Patch Policy seed happens in `_ensure_patch_policy_tables()` and only runs when no `policy_type='global'` row exists. Redeploy does not overwrite operator changes.
    - Policy hierarchy is Global -> Site -> Device/Filter. Device/filter policy is deepest. Same site+role overlaps block site policy save. Direct device/filter target overlap blocks device/filter policy save. Dynamic filter overlap is evaluated at runtime and conflicting devices are skipped.
    - Policy rules support `approve` and `block` against `severity`, `classification`, `category`, `kb`, `update_id`, and `patch_key`. Child approval of a parent block requires confirmation and stores `override_parent_block`.
    - Policy evaluation syncs `patch_catalog_entries` from current `device_patch_inventory`, resolves covered Windows devices, applies `Unmanaged`/`Frozen` exclusions, checks deferral, and groups approved pending inventory by KB/update identity.
    - Each policy run writes `patch_policy_runs`, then creates one enabled `job_kind=patch_install` scheduled job per KB/update identity. The scheduled job component carries `trigger=policy`, `policy_id`, and `policy_run_id`.
    - Active install lockout reuses the existing patch active-job identity map and skips policy-created duplicate KB/update deployments while another enabled patch install job owns the same identity.
    - Agent `patch_policy_enforcement_request` applies Borealis-owned reversible Windows Update policy registry settings. Managed/frozen mode sets Windows Update policy values that prevent automatic installs while preserving the Borealis SYSTEM WUA scan/install path. Unmanaged mode restores Borealis-backed-up values.
    - Agent `patch_reboot_request` schedules Windows reboot only when policy asks for it. It skips logged-in users unless `force_logged_in_user` is true.
    - Scheduled patch jobs use names like `[Ad-Hoc Install] KB5050533 - SQL Server 2017 RTM Azure Connect Pack KB5050533 - 5 Devices`.
    - Bulk scheduled patch jobs use names like `[Bulk Ad-Hoc Install] - KB5050533 - SQL Server 2017 RTM Azure Connect Pack KB5050533 - 5 Devices`.
    - Scheduler snapshots target membership into `scheduled_job_runs` and `scheduled_job_run_targets`, then queues `patch_install_run` work items on the scheduled-job lane.
    - Patch install workers call the site worker host-service bridge, which emits Agent `patch_install_request` with `wait_for_completion=true` over the device SYSTEM socket.
    - The Agent serializes patch installs per device. If another patch install is already active, later patch install requests wait for the current install to finish instead of failing immediately.
    - The Agent matches WUA updates by update identity/revision first, KB second, then exact title fallback. If a pending update identity changes between inventory and install, common with Defender definitions, the Agent can still match the current available update by KB.
    - If the selected update is no longer pending because it was installed by an earlier job or by Windows Update, the Agent records the request as completed with `already_installed=true` instead of failing with `update_not_found`.
    - The Agent waits for Windows Update Agent to become idle before download/install, downloads the matched update when needed, uses WUA async `BeginDownload`/`EndDownload` and `BeginInstall`/`EndInstall` when available, falls back to synchronous WUA `Download()` or `Install()` if async callback startup fails, posts live progress to `/api/agent/patches/install-progress`, never reboots the device, captures available stdout/stderr/result data, and refreshes patch inventory after the attempt.
    - Final patch install results include WUA result code names, pre-install reboot-required state when WUA reports it, and per-update result/HRESULT details when WUA exposes them.
    - Scheduler writes patch install stdout/stderr/result detail to normal Scheduled Job activity and target history surfaces.
    - Scheduled Job device rows derive `patch_progress` from `activity_history.metadata_json.patch_progress` and keep canonical `job_status=Running` while displaying `Downloading n%` or `Installing n%`.
    - Fleet and device patch APIs attach `active_install_job` while an enabled patch install job for the same KB, patch key, WUA update identity, KB discovered in title, or title is still active. The UI replaces `Install` with `Immediate - Job ID: <id>` or `Scheduled - Job ID: <id>` until that job completes, times out, or is deleted.
    - Non-Windows agents report the role as unsupported in Agent Health.
    - Agent rows use `state=pending` or `state=installed`.
    - Agent rows use `source=wua_pending`, `source=wua_history`, or `source=quick_fix_engineering`.
    - Pending metadata includes `is_downloaded`, `is_mandatory`, `requires_reboot`, `update_id`, and `revision_number` when WUA provides those fields.
    - Engine stores normalized rows in `engine.device_patch_inventory`.
    - Non-patch `/api/agent/details` payloads do not clear existing patch inventory.
    - Patch inventory changes emit `device_inventory_changed` with `change=patches_updated`.

    ### De-duplication

    - Borealis groups by normalized KB first when one exists.
    - Rows without KB fall back to WUA update ID plus revision number.
    - Rows without KB or update identity fall back to a title hash.
    - Installed WUA history and `Get-HotFix` duplicates merge into one installed row.
