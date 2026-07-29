# Patch Management

Patch Management lives under `Automation` and groups OS-specific patch surfaces behind page tabs. Windows patch inventory and policies are active today. Linux and MacOS tabs are present for layout consistency and show `Coming Soon` until those patch backends exist.

The Patch Management page has top tabs for `Windows`, `Linux`, and `MacOS`. The Windows tab has two inner tabs:

- `Patch List` shows available and installed Windows updates.
- `Patch Management Policies` shows global baselines, site-level overrides, and device filter policies in one hierarchy table.

## Open Fleet Patch Audit

1. Open `Automation > Patch Management`.
2. Select the `Windows` tab.
3. Use `State` to switch between pending and installed inventory.
4. Use `Severity` to narrow Windows Update Agent rows when severity is available.
5. Select a blue KB number to open Microsoft Update Catalog search results in a new browser tab.
6. Select device counts when you need to jump back to Device Inventory for affected endpoints.
7. Use `Install` on a pending row to open a Schedule-only Scheduled Job draft for every visible device with that update pending. After the job is created, Borealis returns to Patch Management.
8. Select two or more pending rows and use `Bulk Install` to open a Schedule-only draft that creates separate one-KB jobs sharing the same immediate or one-time schedule. After the jobs are created, Borealis returns to Patch Management.

Scheduled patch-install jobs appear under the Scheduled Jobs `Patch Management` filter instead of the default `Normal` job list.

Site-scoped navigation keeps the selected site in the URL as `?site=<site_id>` so operators with assigned sites only see patch inventory they can access.

## Configure Patch Policies

1. Open `Automation > Patch Management`.
2. Select the `Windows` tab.
3. Select `Patch Management Policies`.
4. Use `New Policy` and choose `New Site Policy` or `New Device Filter Policy`.
5. Create a policy and choose its policy type: `Server` or `Workstation`.
6. Choose the install schedule. Times use the Engine host timezone.
7. Set deferral days. Borealis waits from the update `published_at` timestamp, or from first-seen catalog time when Microsoft does not provide `published_at`.
8. Keep `Managed Windows Update` enabled when Borealis should prevent devices from installing updates on their own.
9. Add allow or block rules for severity, classification, category, KB, update ID, patch key, or title text.
10. Save the policy. Borealis blocks same-layer coverage conflicts and asks for confirmation before a child policy overrides a parent block rule.

Two Global Patch Policies are created automatically during Engine database initialization. `Global Workstation Policy` is locked from deletion, enabled by default, uses Tuesday 2:00 AM Engine-local time, defers updates for 14 days, applies conservative MSP approvals, enables managed Windows Update mode, and leaves reboot-after-install off. `Global Server Policy` uses the same defaults except its install window is Wednesday 2:00 AM Engine-local time. Global policies also approve update titles containing `Security Intelligence Update` and block update titles containing `Preview`.

Use the circular play icon beside a policy name only when you want Borealis to create patch install jobs immediately for that policy hierarchy. The confirmation dialog warns that matching devices will download and install approved updates now, and that configured reboot behavior can run after installation finishes. Select the policy name itself to edit the policy. Use the trash icon in the Actions column to delete unlocked policies; global policies remain locked.

The policy table nests site-level overrides under the matching `Server` or `Workstation` global policy. Device Filter policies appear under matching site-level overrides when their eligible devices belong to those targeted sites. If a Device Filter policy targets devices in multiple sites, Borealis can show linked references to the same policy in multiple hierarchy positions. If no matching site-level override exists for a targeted site, the Device Filter policy appears directly under the matching global policy. The `Targeted Sites` column shows searchable site bubbles so operators can inspect policy reach without opening the editor.

Windows patch policies are single-domain. Site policies target one or more sites plus exactly one policy type: `Server` or `Workstation`. One site and policy type combination can only be covered by one enabled site policy.

The policy `Schedule` column shows cadence plus timing, such as `Weekly: Wednesdays @ 2:00AM`, so operators do not need a separate first-run column for normal review.

Device Filter policies also choose exactly one policy type. Borealis resolves direct devices and filters first, then strips devices that do not match the policy type. Devices with no declared `device_type` are ignored by Windows patch automation until typed. A Windows Server `operating_system` or `device_type` containing `server` is treated as `Server`; any other non-empty `device_type` is treated as `Workstation`.

Policy editor target rows can show `eligible / raw Devices Match Policy Type` so operators can see how many raw Windows device matches were stripped by server/workstation filtering.

Device Filter policies override site and global policy behavior. Direct same-layer overlaps are blocked at save time. Dynamic filter overlaps mark affected devices as conflicted during evaluation so automation skips them instead of guessing which policy should win. Parent policies skip devices already owned by a deeper same-type policy, so policy-created patch jobs do not duplicate deployments.

The `Pending Updates` policy column shows approved, deferral-ready unique update identities that would create policy-driven install jobs, plus how many unique devices those updates span. Borealis counts the same KB/update only once per policy layer even when many devices need it. Zero-count layers stay hidden so empty rows remain visually quiet. Global rows show pending updates approved by the global policy even when a site or device filter policy owns execution for those devices. Site rows can show `Global` and `Site`; Device Filter rows can show `Global`, `Site`, and `Filter` in compact text such as `Global: 3 Updates (9 Devices) / Filter: 1 Update (2 Devices)`. Select a blue count to open `Patch List` filtered to that row scope and source layer. The Patch List `Linked Policy(s)` column shows unique policy names whose direct allow or block rules match the grouped update. The split global server and workstation policies both appear when their matching direct rules apply to the same update title. Inherited child policies are not repeated unless they add their own matching rule, and hover text shows the layer and rule action.

## Exclusions and Reboots

Use `Unmanaged` when Borealis should do no patch installs and no Windows Update enforcement for a device. Use `Frozen` when Borealis should not install updates, but the Agent should still enforce Windows Update lock/freeze settings. Child policies can use `Managed Override` to explicitly clear inherited `Unmanaged` or `Frozen` coverage for matching devices or filters. Hostname exclusions require a site selection so duplicate hostnames in different sites stay distinct. Excluded devices still count as covered during conflict checks.

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
    - `GET /api/patches/policies` - list patch policies, optionally filtered by `type=global`, `type=site`, or `type=device_filter`. Unfiltered Windows UI calls return all policy types with target counts and targeted-site summaries for the hierarchy table.
    - `POST /api/patches/policies` - create a patch policy.
    - `GET /api/patches/policies/metadata` - load policy editor metadata for sites, filters, rule types, Windows role scopes, exclusion modes, and defaults.
    - `GET /api/patches/policies/<policy_id>` - load one policy.
    - `PUT /api/patches/policies/<policy_id>` - update one policy.
    - `DELETE /api/patches/policies/<policy_id>` - delete one unlocked policy. Global policy returns a locked-policy error.
    - `POST /api/patches/policies/<policy_id>/preview` - resolve target coverage, role-filtered counts, conflicts, and parent-block override warnings.
    - `GET /api/patches/policies/effective?hostname=<hostname>` - show effective same-role hierarchy, inherited exclusion source, and override source for one device.
    - `POST /api/patches/policies/evaluate` - backend policy runner used by `Run Updates Now` and scheduler-driven policy execution. Manual calls create immediate patch-install jobs for matching approved updates.
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
    - OS placeholder pane: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Patches/Patch_Management_Platform_Page.jsx`
    - Shared patch UI constants: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Patches/Patch_Management_Shared.jsx`
    - Policy editor UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Patches/Patch_Policy_Editor.jsx`
    - Device tab UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Patch_Management.jsx`

    ### Runtime behavior

    - Windows agents collect pending updates through native Windows Update Agent COM and installed KB rows through `Get-HotFix` plus WUA history.
    - Pending rows come from WUA `IsInstalled=0 and IsHidden=0`, so they can be available but not downloaded yet. Download status stays in `is_downloaded`.
    - Install buttons do not trigger WUA directly. They open `Create_Job.jsx` with a `patch_install` component, selected patch metadata, and frozen targets prefilled.
    - Patch install drafts show only the `Schedule` tab during creation. Job name, target, assembly, and execution-context tabs stay hidden because Patch Management owns those values.
    - Patch install drafts carry an internal `return_to` path so successful creation returns to the originating fleet or device Patch Management route.
    - KB cells link to `https://www.catalog.update.microsoft.com/Search.aspx?q=<KB>` in a new tab when the row has a normalized KB value.
    - Bulk Install sends multiple selected patch items into `Create_Job.jsx`. Create Job keeps schedule settings shared, then creates one `job_kind=patch_install` scheduled job per selected patch.
    - Global Patch Policy seed happens in `_ensure_patch_policy_tables()` and `ensureSplitGlobalPatchPolicies()`. When no split global policy exists, Borealis clears legacy patch policy definitions/history/state, preserves `patch_catalog_entries` and `device_patch_inventory`, then seeds `Global Workstation Policy` and `Global Server Policy`. Redeploy does not overwrite existing split global policies.
    - Policy hierarchy is Global -> Site -> Device Filter within the same policy type. Device Filter policy is deepest. Same site+role overlaps block site policy save. Direct device filter target overlap blocks device filter policy save. Dynamic filter overlap is evaluated at runtime and conflicting devices are skipped.
    - Fleet Patch Management renders one route at `/patch-management`. The top OS tab uses `platform=windows|linux|macos`; Windows policy sub-tabs keep using `tab=patch_list|policies`.
    - Legacy `/patch-management/windows`, `/patch-management/linux`, and `/patch-management/macos` routes redirect to the matching `platform` query tab.
    - Windows fleet UI renders one converged `Patch Management Policies` table. The API includes `target_sites`, `target_site_ids`, and `target_site_names`; device filter target sites come from eligible resolved devices, so linked policy references disappear from a site branch when that policy no longer targets eligible devices in that site.
    - Windows patch policy role scopes are `Server` and `Workstation`. `Both` is retained only as a legacy overlap helper and is rejected by policy save validation. Empty `device_type` devices do not match either role unless `operating_system` identifies Windows Server. Windows Server `operating_system` or `device_type` strings containing `server` match `Server`; any other non-empty `device_type` matches `Workstation`.
    - Effective policy resolution groups policies by device identity, selects the deepest same-role match, and assigns each device to only that effective policy. Parent policy runs therefore do not create duplicate KB jobs for devices covered by child policies.
    - Policy rules support `approve` and `block` against `severity`, `classification`, `category`, `kb`, `update_id`, `patch_key`, and `title_contains`. `title_contains` performs a case-insensitive substring match against the update title. Parent approve and block rules inherit downward as the baseline. Child approval of a parent block requires confirmation and stores `override_parent_block`.
    - Device exclusions inherit downward. `managed_override` in `patch_policy_exclusions.exclusion_type` clears inherited `unmanaged` or `frozen` for matching devices or filters. Effective device-state metadata records `hierarchy_policy_ids`, `exclusion_policy_id`, and `exclusion_override_policy_id`.
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
