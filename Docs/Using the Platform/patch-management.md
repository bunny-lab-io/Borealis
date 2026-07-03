# Patch Management

Patch Management shows Windows patch inventory collected by Borealis agents. Operators can schedule one pending update on one device, or schedule one pending update across all visible devices that currently report it. Policies, approvals, maintenance windows, and reboot orchestration are not implemented yet.

## Open Fleet Patch Audit

1. Open `Alerting & Reporting > Patch Management`.
2. Use `State` to switch between pending and installed inventory.
3. Use `Severity` to narrow Windows Update Agent rows when severity is available.
4. Select device counts when you need to jump back to Device Inventory for affected endpoints.
5. Use `Install` on a pending row to open a prefilled Scheduled Job draft for every visible device with that update pending.
6. Select two or more pending rows and use `Bulk Install` to schedule separate one-KB jobs that share the same immediate or one-time schedule.

Site-scoped navigation keeps the selected site in the URL as `?site=<site_id>` so operators with assigned sites only see patch inventory they can access.

## Read Device Patch Inventory

1. Open `Inventory > Devices`.
2. Select a device hostname.
3. Select `Patch Management` from the Device Summary sidebar.
4. Use `Query Patch Inventory` when you need a fresh Windows Update Agent and installed KB snapshot.
5. Use `Install` on a pending row to open a prefilled Scheduled Job draft for that device and selected update.
6. Select two or more pending rows and use `Bulk Install` to schedule separate one-KB jobs for that device with shared timing.

Pending rows come from Windows Update Agent search results that are not installed and not hidden. Installed rows come from `Get-HotFix` and Windows Update Agent history, then Borealis de-duplicates them by KB or update identity.

!!! info

    Windows Update Agent often leaves severity empty even when classification is populated. Borealis displays those rows as `Unspecified` instead of guessing severity from classification.

!!! warning

    Patch Management ad-hoc install jobs do not approve updates, force reboots, or replace existing patching policy tooling. Offline agents at execution time remain visible in Scheduled Job results so operators can retry or reschedule after the device returns.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - `GET /api/patches/audit` - fleet patch inventory, scoped to operator site access. Rows include `active_install_job` when an enabled scheduled patch install already owns that patch.
    - `GET /api/device/patches/<hostname>` - device patch inventory, scoped to operator site access.
    - `POST /api/device/patches/<hostname>/refresh` - queue `patch_inventory_refresh_request` over the device SYSTEM socket.
    - `POST /api/scheduled_jobs` with `job_kind=patch_install` - create an ad-hoc patch install job from the Patch Management install flow. Bulk flows call this once per selected patch.
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
    - Engine schema bootstrap: `Data/Engine/Containers/api-backend/data/database.py`
    - Job creation UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Scheduling/Create_Job.jsx`
    - Fleet UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Patches/Patch_Management.jsx`
    - Device tab UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Patch_Management.jsx`

    ### Runtime behavior

    - Windows agents collect pending updates through native Windows Update Agent COM and installed KB rows through `Get-HotFix` plus WUA history.
    - Pending rows come from WUA `IsInstalled=0 and IsHidden=0`, so they can be available but not downloaded yet. Download status stays in `is_downloaded`.
    - Install buttons do not trigger WUA directly. They open `Create_Job.jsx` with a `patch_install` component, selected patch metadata, and frozen targets prefilled.
    - Bulk Install sends multiple selected patch items into `Create_Job.jsx`. Create Job keeps schedule settings shared, then creates one `job_kind=patch_install` scheduled job per selected patch.
    - Scheduled patch jobs use names like `[Ad-Hoc Install] KB5050533 - SQL Server 2017 RTM Azure Connect Pack KB5050533 - 5 Devices`.
    - Bulk scheduled patch jobs use names like `[Bulk Ad-Hoc Install] - KB5050533 - SQL Server 2017 RTM Azure Connect Pack KB5050533 - 5 Devices`.
    - Scheduler snapshots target membership into `scheduled_job_runs` and `scheduled_job_run_targets`, then queues `patch_install_run` work items on the scheduled-job lane.
    - Patch install workers call the site worker host-service bridge, which emits Agent `patch_install_request` with `wait_for_completion=true` over the device SYSTEM socket.
    - The Agent matches WUA updates by update identity/revision first, KB second, then exact title fallback.
    - The Agent downloads the matched update when needed, calls WUA install for the matched update collection, never reboots the device, captures available stdout/stderr/result data, and refreshes patch inventory after the attempt.
    - Scheduler writes patch install stdout/stderr/result detail to normal Scheduled Job activity and target history surfaces.
    - Fleet and device patch APIs attach `active_install_job` while an enabled patch install job for the same KB, patch key, or title is still active. The UI replaces `Install` with a Scheduled Job link until that job completes, times out, or is deleted.
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
