# Patch Management

Patch Management shows Windows patch inventory collected by Borealis agents. Operators can request one pending update on one device, or request one pending update across all visible devices that currently report it. Policies, approvals, maintenance windows, and reboot orchestration are not implemented yet.

## Open Fleet Patch Audit

1. Open `Alerting & Reporting > Patch Management`.
2. Use `State` to switch between pending and installed inventory.
3. Use `Severity` to narrow Windows Update Agent rows when severity is available.
4. Select device counts when you need to jump back to Device Inventory for affected endpoints.
5. Use `Install` on a pending row to ask every visible device with that update pending to install it.

Site-scoped navigation keeps the selected site in the URL as `?site=<site_id>` so operators with assigned sites only see patch inventory they can access.

## Read Device Patch Inventory

1. Open `Inventory > Devices`.
2. Select a device hostname.
3. Select `Patch Management` from the Device Summary sidebar.
4. Use `Query Patch Inventory` when you need a fresh Windows Update Agent and installed KB snapshot.
5. Use `Install` on a pending row to ask that device to install the selected update.

Pending rows come from Windows Update Agent search results that are not installed and not hidden. Installed rows come from `Get-HotFix` and Windows Update Agent history, then Borealis de-duplicates them by KB or update identity.

!!! info

    Windows Update Agent often leaves severity empty even when classification is populated. Borealis displays those rows as `Unspecified` instead of guessing severity from classification.

!!! warning

    Patch Management ad-hoc install requests do not approve updates, schedule maintenance windows, force reboots, or replace existing patching policy tooling. Offline agents are not queued for later install; the request must reach the live SYSTEM socket.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - `GET /api/patches/audit` - fleet patch inventory, scoped to operator site access.
    - `POST /api/patches/install` - request one pending patch on every visible device that has the selected patch key pending.
    - `GET /api/device/patches/<hostname>` - device patch inventory, scoped to operator site access.
    - `POST /api/device/patches/<hostname>/refresh` - queue `patch_inventory_refresh_request` over the device SYSTEM socket.
    - `POST /api/device/patches/<hostname>/install` - request one pending patch over the device SYSTEM socket.
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
    - Engine schema bootstrap: `Data/Engine/Containers/api-backend/data/database.py`
    - Fleet UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Patches/Patch_Management.jsx`
    - Device tab UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Patch_Management.jsx`

    ### Runtime behavior

    - Windows agents collect pending updates through native Windows Update Agent COM and installed KB rows through `Get-HotFix` plus WUA history.
    - Pending rows come from WUA `IsInstalled=0 and IsHidden=0`, so they can be available but not downloaded yet. Download status stays in `is_downloaded`.
    - Install requests use live Socket.IO `patch_install_request` calls. The Agent matches WUA updates by update identity/revision first, KB second, then exact title fallback.
    - The Agent downloads the matched update when needed, calls WUA install for the matched update collection, never reboots the device, and refreshes patch inventory after the attempt.
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
