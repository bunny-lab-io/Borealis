# Device Management

## Purpose
Explain how Borealis tracks devices, ingests inventory, manages sites and filters, and handles enrollment approvals.

## Inventory and Status
- Agents send heartbeats and inventory payloads to the Engine.
- The Engine stores device summaries and detailed hardware, software, and cached service data in PostgreSQL.
- Online status is derived from `last_seen` (online if the heartbeat is within ~5 minutes).
- Startup health is reported separately through `POST /api/agent/status`, which updates `last_seen`, preserves existing role-health rows, and upserts only `system:system_heartbeat` with boot timeline milestones.
- Session inventory now also carries helper-specific readiness data such as `eligible_for_interactive`, `helper_ready`, `helper_pid`, and `helper_last_seen_at` so Borealis can validate current-user execution targets.

## Sites and Enrollment Codes
- Sites group devices for organizational and targeting purposes.
- Each site can have an enrollment code that agents can use during install.
- Site mapping is stored separately from device records and exposed via API.
- Automatic local-network onboarding uses the selected site's enrollment code after a successful SSH install. The resulting device still lands in the normal approval queue.

## Operator Site Scope (RBAC)
- Admins implicitly see all sites, devices, approvals, and remote-access surfaces.
- Operators are scoped by `user_site_assignments`; if an operator has no assigned sites, Borealis hides all sites and devices from that operator.
- Devices without a site assignment remain admin-only until an admin places them into a site.
- The shared header hostname search follows the same scope rules and only returns devices in the signed-in operator's visible sites unless the operator is an admin.
- The WebUI site-assignment workflow lives on the Users page and writes assignments through `/api/user_site_assignments/*`.

## Device Filters
- Filters are stored as typed records with separate `basic_criteria_json` and `advanced_criteria_json` payloads.
- Site scope is explicit via `site_mode` plus `device_filter_sites` rows keyed by `site_id`.
- The Engine computes match counts using `DeviceFilterMatcher` against the inventory snapshot.
- Filters can be `Global`, `Specific Sites`, or `Global w/ Exclusions`.
- Operator visibility is based on the filter's effective included site scope, not the subset of matching devices the operator can see.
- Filter criteria include a grouped `Metadata Field` selector. Each criterion stores `metadata_field_number`, so global field-description changes do not break saved filters.

## Agent Metadata Fields
- Borealis exposes 500 fixed text metadata fields per device, keyed as `field_001` through `field_500` and displayed as `Field 001` through `Field 500` unless an admin sets a global description.
- Global descriptions live in Admin Settings > Metadata Fields. Values live per device and are editable from Device Summary > Metadata Fields.
- Values have a 1024-character decoded limit and are base64-encoded at rest in Engine storage and in the Agent's transient `metadata-queue.json`.
- Device values are sparse. The Agent does not store metadata fields in `agent.json`; local CLI changes live only in `metadata-queue.json` until Engine acknowledges them.
- The Engine stores encoded values in `device_metadata_fields` and decodes them only for device-field API/UI reads, filter matching, and device-auth CLI reads.
- Newest `modified_at` wins. Agent-provided timestamps more than five minutes in the future are clamped to Engine time before conflict resolution.
- Scripts and automations should write through the Agent CLI: `Agent.exe --metadata set 1 "text"` or `Agent --metadata set 1 "text"`. `Agent.exe --metadata get 1` returns a pending local queue value first, otherwise Engine value. Passing a blank value queues a clear until the next heartbeat acknowledgement.

## Device Watchdogs and Alerts
- Device Summary now exposes a `Watchdogs` tab for incident-first per-device monitoring.
- That tab shows:
  - active incidents currently affecting the device
  - effective watchdog assignments resolved onto the device
  - active per-device suppressions and overrides
- Operators can create a new prefilled watchdog for the current device without leaving the device page.
- Per-device suppressions mute one watchdog/device relationship without cloning the shared watchdog policy.

## Device Agent Health
- Device Summary includes a right-anchored `Agent Health` tab separate from the normal left-side tab sequence.
- The Agent Health tab shows a visual startup flow with complete, active, failed, pending, and skipped states plus clickable role/service runtime-health nodes inside the flow graph.
- Startup flow data comes from the agent SYSTEM heartbeat role and appears as `system:system_heartbeat`; normal role/service health rows remain separate.
- The tab listens for `agent_status_changed` on `window.BorealisSocket` and silently refreshes current-device telemetry when the Engine commits a new status update.

## Device File Management
- Device Summary now also exposes a `File Management` tab for remote browse, upload, download, lightweight text editing, copy, cut, paste, create-folder, rename, move, and delete actions against the device file system.
- Windows devices browse in the Borealis SYSTEM context and Linux devices browse in the root context.
- The tab uses a single AG Grid with a custom tree-style `Name` column, React-managed flattened rows, lazy directory expansion, a page-level `Refresh` control, and a right-click context menu for file actions.
- Uploads stage browser files on the Engine first and then let the agent pull them into place. Folder uploads preserve the browser-provided relative path manifest so Borealis can recreate the nested destination structure on the remote agent while serializing the file transfers into their matching subfolders.
- Downloads let the agent stage the requested file or archive back to the Engine before the operator fetches it, and the in-tab transfer banner now also exposes `Cancel` so operators can interrupt in-progress uploads or downloads.
- Before File Management uploads start, Borealis now preflights duplicate names in the destination directory and, when needed, shows a Windows-style replace-or-skip dialog so the operator can replace, skip, or compare each duplicate before the transfer begins, including duplicate files discovered inside folder uploads.
- Lightweight text editing reads one remote text file at a time through the file-management socket lane, applies syntax highlighting based on the filename extension, and saves back to the same path in-place so the file keeps its existing permissions.
- Copy and cut actions stay operator-local until the operator pastes them into a destination folder or drive, at which point the Engine asks the agent to perform the remote filesystem copy or move in place.
- Operator access stays site-scoped per hostname, and agent-side transfer routes stay bound to the authenticated device GUID.

## Device Process Management
- Device Summary exposes a `Processes` tab for live task-manager style process inspection and control.
- The tab uses a single AG Grid with `Name`, `Owner`, `CPU Usage`, `Memory Usage`, `Disk`, `Network`, and `Command Line` columns, defaults to the hottest individual or app-family CPU usage first, trims Windows service scaffolding, `explorer.exe`, and terminal shell hosts out of the visible parent chain, and keeps parent/child expansion opt-in so the default view reads like Task Manager rather than a fully expanded Process Explorer tree.
- Parent process rows display aggregate CPU and memory totals for their visible descendant processes so collapsed app families line up with Windows Task Manager group rows.
- `Show System Processes` is disabled by default. When disabled, Borealis removes low-CPU, low-memory OS scaffolding such as service hosts, session brokers, shell helpers, and kernel worker rows while keeping those same processes visible when they become meaningful resource consumers. It always hides `MemCompression` and `wslservice.exe` while system processes are suppressed.
- The refresh Filter Slider supports `Live` (1s), `Normal` (5s), and `Quiet` (15s) polling. Faster polling passes a lower process snapshot max age to the agent so the device refreshes more often while the tab is active.
- Terminated processes remain visible by default with a gentle red row highlight through `Show Terminated Processes` so operators can see task death after an End Task action or when a process disappears from later live snapshots.
- Process names use the same Borealis-blue treatment as the File Management `Name` column.
- CPU, memory, disk, and network cells include resource heat-map fills so high-consumption processes stand out before an operator sorts or filters.
- Windows disk usage uses psutil process I/O deltas with a `Win32_PerfFormattedData_PerfProc_Process` fallback so short cached samples still receive an OS-formatted per-process I/O rate.
- Windows network usage uses TCP extended statistics when available, aggregated by owning process ID. Rows report `N/A` only when the agent cannot access those per-connection counters.
- The WebUI polls the live process endpoint according to the selected refresh rate while the tab is open. The agent-side `process_management` role keeps a short active polling window and cached snapshots so multiple UI refreshes reuse recent process data instead of forcing duplicate process walks.
- Right-click row actions use the Borealis context-menu model and currently include `Copy Location to Clipboard`, `Copy Command to Clipboard`, and `End Task`.

## Device List Views
- Operators can save custom table views for the device list UI.
- Views are stored per operator and exposed via `/api/device_list_views`.

## Enrollment Approvals
- Enrollment requests are queued for approval within the request's site.
- Admins can approve any site; operators can approve only requests for sites they are assigned to.
- Approvals enforce hostname conflict checks and device identity tracking.
- Approvals created by automatic local-network onboarding can include `onboarding_job_id`, `onboarding_run_id`, and `onboarding_target` so operators can trace a pending approval back to the scheduled onboarding run and target.
- Admins can bulk-select pending approval rows and approve them in one action. Rows that need hostname conflict resolution stay pending for explicit review.
- Sites can carry a temporary `auto_approve_until` timestamp. While active, new enrollments for that site are marked approved automatically unless the requested hostname conflicts with a different existing fingerprint.

## Automatic Local-Network Onboarding
- Jobs are created from Sites > Onboard Devices and appear in Scheduled Jobs alongside automation jobs.
- Targets can be supplied as IPv4 addresses, IPv4 ranges, CIDR blocks, or FQDNs. Exclusion scope entries use the same formats and remove targets before onboarding attempts start.
- Windows targets use the same IP/FQDN scope model. Borealis tries SMB `ADMIN$` plus Remote Service Control Manager, then remote scheduled task, then WMI/DCOM process creation, then WinRM. Windows onboarding stages Go `Agent.exe`, which owns install, repair, update checks, support dependency setup, `agent.json`, `BorealisAgent` service registration, AutoUpdater/Watchdog task registration, and runtime. When `Agent.exe` validates an already-running enrolled agent service, the onboarding target is marked skipped with `Already Enrolled and Active` instead of completed. If policy blocks all methods, Borealis records that manual agent installation is required.
- The Engine reaches targets directly over the local network; no WireGuard tunnel or existing Borealis agent is required.
- Borealis uses one stored machine or domain credential per onboarding job and does not copy credentials into the job definition.
- The remote installer uses the selected agent install branch, the Engine public URL, and the selected site's enrollment code. Device approval remains manual, but pending approvals can be approved directly from the onboarding job target status table when no hostname conflict prompt is required.
- Re-deploy clears prior onboarding run history for that job and starts a new immediate local-network deployment. Device onboarding concurrency defaults to `5` so subnet scans do not flood the Engine, and operators can tune it per onboarding job.

## Device Purge
- The Device List `Delete` action is now an admin-only purge flow backed by `POST /api/devices/<guid>/purge`.
- Purge is authoritative: Borealis deletes the device row plus current-known identity, trust, and inventory references including `device_keys`, `refresh_tokens`, `device_sites`, `device_software_inventory`, approval records, activity history, scheduled-job target history, Ansible recaps, and dormant per-agent VPN/service-account rows.
- Purge also rewrites future scheduled-job definitions. Direct device targets for the purged GUID are removed, hostname and site fallback targets are stripped when no GUID is present, filter targets remain intact, and any job left with no targets is deleted entirely.
- A purge creates a temporary auth barrier keyed by GUID and the required next token version so stale access tokens and stale refresh tokens fail with `device_purged` instead of recreating the device row.
- If the same machine is approved again later, enrollment recreates the device row at or above the barrier token version and then clears the barrier so the device can return through normal approval.
- After the purge transaction commits, the Engine best-effort disconnects active VPN transport and revokes cached VNC sessions for the purged agent ID.

## API Endpoints
- `POST /api/agent/heartbeat` (Device Authenticated) - heartbeat, metrics, and Agent Metadata Field sync.
- `POST /api/agent/status` (Device Authenticated) - startup phase, boot ID, milestone timeline, and last-error telemetry for the Agent Health tab.
- `POST /api/agent/details` (Device Authenticated) - inventory and cached service payloads.
- `GET /api/agent/software-management/overrides` (Device Authenticated) - file-backed icon override rules used by the agent software-management inventory path.
- `GET /api/agents` (Token Authenticated) - online collectors keyed by agent identity, with upgraded hosts advertising helper-backed current-user capability on their SYSTEM record instead of registering a second Borealis socket.
- `GET /api/devices` (Token Authenticated) - device summary list.
- `GET /api/devices/search?hostname=<query>` (Token Authenticated) - site-scoped hostname search for the shared header search UI.
- `GET /api/devices/<guid>` (Token Authenticated) - device summary by GUID.
- `GET /api/metadata_fields` (Token Authenticated) - list global metadata field definitions.
- `PUT /api/metadata_fields/<field_number>` (Admin) - update a global metadata field description.
- `GET /api/devices/<device_id>/metadata_fields` (Token Authenticated) - list all metadata fields for an in-scope device.
- `PUT /api/devices/<device_id>/metadata_fields/<field_number>` (Token Authenticated) - update or clear one metadata field value for an in-scope device.
- `GET /api/agent/metadata/<field_number>` (Device Authenticated) - read one decoded metadata field for local Agent CLI.
- `POST /api/devices/<guid>/purge` (Admin) - holistically purge a device, its trust records, and scheduled-job references.
- `GET /api/device/details/<hostname>` (Token Authenticated) - full device details.
- `GET /api/device/services/<hostname>` (Token Authenticated) - cached service inventory.
- `POST /api/device/services/<hostname>/action` (Token Authenticated) - start, stop, or restart a named service.
- `GET /api/device/processes/<hostname>?max_age_seconds=<seconds>` (Token Authenticated) - return a live process snapshot for an in-scope device, optionally forcing a fresher agent snapshot for live polling.
- `POST /api/device/processes/<hostname>/terminate` (Token Authenticated) - request process termination on an in-scope device.
- `POST /api/device/software/<hostname>/refresh` (Token Authenticated) - request an immediate software inventory refresh on the device.
- `POST /api/device/software/<hostname>/icon-override` (Token Authenticated) - create or replace a hotloaded global icon override for a software row.
- `POST /api/device/software/<hostname>/uninstall-override` (Token Authenticated) - create or replace a hotloaded global uninstall override for a software row.
- `POST /api/device/software/<hostname>/uninstall-block` (Token Authenticated) - create or replace a hotloaded global uninstall block rule for a software row.
- `POST /api/device/software/<hostname>/uninstall-unblock` (Token Authenticated) - remove matching hotloaded uninstall block rules for a software row.
- `POST /api/device/software/<hostname>/uninstall` (Token Authenticated) - queue a silent uninstall quick job for a supported installed-software row on an in-scope Windows device.
- `POST /api/device/update-agent/<hostname>` (Token Authenticated) - ask a device to start its local AutoUpdater task immediately.
- `POST /api/devices/agent-maintenance` (Token Authenticated) - queue Agent updates or Agent branch/channel switches for one or more devices through scheduled-job history and site-worker fan-out.
- `GET /api/device/files/<hostname>/roots` (Token Authenticated) - load the File Management tab roots view for an in-scope device.
- `GET /api/device/files/<hostname>/children?path=<absolute-path>` (Token Authenticated) - list one remote directory for an in-scope device.
- `POST /api/device/files/<hostname>/upload/conflicts` (Token Authenticated) - preflight upload name conflicts in one remote directory for an in-scope device.
- `GET /api/device/files/<hostname>/text?path=<absolute-path>` (Token Authenticated) - read one lightweight-editable remote text file for the File Management editor.
- `POST /api/device/files/<hostname>/text` (Token Authenticated) - save one lightweight-editable remote text file back in place on an in-scope device.
- `POST /api/device/files/<hostname>/mkdir` (Token Authenticated) - create a remote directory on an in-scope device.
- `POST /api/device/files/<hostname>/rename` (Token Authenticated) - rename one remote file-system item on an in-scope device.
- `POST /api/device/files/<hostname>/move` (Token Authenticated) - move remote file-system items on an in-scope device.
- `POST /api/device/files/<hostname>/paste` (Token Authenticated) - paste copied or cut remote file-system items into a destination directory on an in-scope device.
- `POST /api/device/files/<hostname>/delete` (Token Authenticated) - delete remote file-system items on an in-scope device.
- `POST /api/device/files/<hostname>/upload` (Token Authenticated) - stage browser-uploaded files for transfer to an in-scope device.
- `POST /api/device/files/<hostname>/download` (Token Authenticated) - start a remote file download transfer from an in-scope device.
- `GET /api/device/files/<hostname>/transfer/<transfer_id>/status` (Token Authenticated) - poll a File Management transfer snapshot.
- `POST /api/device/files/<hostname>/transfer/<transfer_id>/cancel` (Token Authenticated) - request cancellation for an in-progress File Management transfer.
- `GET /api/device/files/<hostname>/transfer/<transfer_id>/content` (Token Authenticated) - download a completed File Management transfer artifact from Engine temp storage.
- `POST /api/device/description/<hostname>` (Token Authenticated) - update description.
- `GET /api/device_list_views` (Token Authenticated) - list saved views.
- `GET /api/device_list_views/<int:view_id>` (Token Authenticated) - get saved view.
- `POST /api/device_list_views` (Token Authenticated) - create saved view.
- `PUT /api/device_list_views/<int:view_id>` (Token Authenticated) - update saved view.
- `DELETE /api/device_list_views/<int:view_id>` (Token Authenticated) - delete saved view.
- `GET /api/sites` (Token Authenticated) - list sites plus public engine URL metadata used for copy-ready agent install commands.
- `POST /api/sites` (Admin) - create site.
- `POST /api/sites/delete` (Admin) - delete sites.
- `GET /api/sites/device_map` (Token Authenticated) - hostname to site map.
- `POST /api/sites/assign` (Admin) - assign devices to site.
- `POST /api/sites/rename` (Admin) - rename site.
- `POST /api/sites/<site_id>/auto-approval` (Admin) - set or clear temporary site-level device auto-approval.
- `POST /api/user_site_assignments/selection` (Admin) - load selected-operator site assignments.
- `POST /api/user_site_assignments/assign` (Admin) - replace selected-operator site assignments.
- `GET /api/device_filters` (Token Authenticated) - list filters.
- `GET /api/device_filters/metadata` (Token Authenticated) - filter metadata, including metadata-field definitions for the field picker.
- `POST /api/device_filters/preview` (Token Authenticated) - preview filter matches.
- `GET /api/device_filters/<filter_id>` (Token Authenticated) - get filter.
- `GET /api/device_filters/<filter_id>/usage` (Token Authenticated) - list scheduled jobs referencing a filter.
- `POST /api/device_filters` (Token Authenticated) - create filter.
- `PUT /api/device_filters/<filter_id>` (Token Authenticated) - update filter.
- `POST /api/device_filters/<filter_id>/clone` (Token Authenticated) - clone filter.
- `POST /api/device_filters/<filter_id>/archive` (Token Authenticated) - archive filter.
- `POST /api/device_filters/<filter_id>/unarchive` (Token Authenticated) - unarchive filter.
- `DELETE /api/device_filters/<filter_id>` (Token Authenticated) - delete filter.
- `GET /api/devices/<device_id>/watchdogs` (Token Authenticated) - load the device Watchdogs tab payload.
- `POST /api/devices/<device_id>/watchdogs/overrides` (Token Authenticated) - create, update, or clear a device-specific watchdog override.
- `GET /api/watchdogs/incidents` (Token Authenticated) - list current watchdog incidents.
- `POST /api/watchdogs/incidents/<int:incident_id>/acknowledge` (Token Authenticated) - acknowledge an open incident.
- `GET /api/admin/enrollment-codes` (Admin) - list static site enrollment codes.
- `POST /api/admin/enrollment-codes` (Admin) - deprecated (returns 410; use site APIs).
- `DELETE /api/admin/enrollment-codes/<code_id>` (Admin) - deprecated (returns 410; use site APIs).
- `GET /api/admin/device-approvals` (Token Authenticated) - approval queue scoped to the current operator's assigned sites unless the operator is an admin. Admins can use `status=wrong_code` for recent invalid enrollment-code attempts.
- `POST /api/admin/device-approvals/<approval_id>/approve` (Token Authenticated) - approve an in-scope device.
- `POST /api/admin/device-approvals/<approval_id>/deny` (Token Authenticated) - deny an in-scope device.
- `POST /api/onboarding/jobs/<job_id>/redeploy` (Token Authenticated) - clear onboarding history for a job and start a fresh run.
- `GET /api/onboarding/jobs/<job_id>/targets` (Token Authenticated) - per-target automatic onboarding attempts for a scheduled job occurrence, including current approval context when available.

## Related Documentation
- [Agent Runtime](../Core%20Runtimes/agent-runtime.md)
- [Database Reference](../Data%20and%20Schema/db-reference.md)
- [Security and Trust](../Start%20Here/security-and-trust.md)
- [Scheduled Jobs](../Automation%20and%20Execution/scheduled-jobs.md)
- [Watchdogs](../Automation%20and%20Execution/watchdogs.md)
- [Device Alerts](device-alerts.md)
- [VPN and Remote Access](vpn-and-remote-access.md)
- [API Reference](../Data%20and%20Schema/api-reference.md)
- [Software Icon Overrides](../Software%20Management/adding-software-to-icon-overrides.md)
- [Software Uninstall Overrides](../Software%20Management/adding-software-to-uninstall-overrides.md)
- [Software Uninstall Blocklist](../Software%20Management/adding-software-to-uninstall-blocklist.md)

## Codex Agent (Detailed)
### Key files and services
- Device APIs: `Data/Engine/Containers/api-backend/data/services/API/devices/` (management, approval, services, tunnel, vnc, routes).
- Filters: `Data/Engine/Containers/api-backend/data/services/filters/matcher.py` and `Data/Engine/Containers/api-backend/data/services/API/filters/management.py`.
- Enrollment approvals: `Data/Engine/Containers/api-backend/data/services/API/devices/approval.py`.

### Inventory ingestion behavior
- `/api/agent/heartbeat` updates `last_seen`, key metrics (last_user, OS/build, last reboot, uptime), and sparse Agent Metadata Field changes.
- `/api/agent/status` updates `last_seen`, upserts only the `system:system_heartbeat` role-health row, and emits `agent_status_changed` after commit.
- `/api/agent/details` stores full inventory payloads for memory, network, storage, software, cpu, and services.
- JSON blobs are serialized into PostgreSQL text columns and rehydrated for UI.
- Installed software is also normalized into `device_software_inventory` so filters can match name, source, and version reliably.
- Agent Metadata Field heartbeat sync is timestamp-based. Queued Agent updates are accepted when newer, superseded when Engine has the same/newer timestamp, and returned in `metadata_field_acks` so the Agent can remove them from `metadata-queue.json`.
- The Installed Software tab now also exposes a row-level `Uninstall` action for supported Windows software entries. Borealis queues that work through the signed quick-job path in SYSTEM context, so uninstall output lands in `activity_history` and the row disappears after the next successful device software inventory refresh.
- Operators can right-click a software name in the Installed Software tab to create global icon overrides, global uninstall overrides, uninstall blocks, or uninstall unblocks directly from the WebUI. Borealis writes those operator-approved changes into the file-backed JSON override/blocklist stores under `Data/Engine/Containers/api-backend/data/services/API/devices/`, hotloads them without an Engine restart, and lets the developer commit those files to Git later when the pilot rules should become official.
- The Installed Software tab also exposes a `Query Software Changes` button that emits `software_inventory_refresh_request` over the device SYSTEM socket so operators can observe override and inventory changes faster than the normal software-management poll cadence.
- Session inventory enrichment from the Go agent broker flows through `Data/Agent/internal/roles/current_user` and heartbeat role health, so Device Details can distinguish a merely logged-in session from a helper-ready interactive session.
- Service inventory is cached in the `devices.services` JSON blob and merged with pending operator actions until a fresh agent snapshot confirms the desired state.
- Process Management uses the device SYSTEM Socket.IO channel with ACK responses under `process_management_request` for live process snapshots and process termination. It does not replace the slower cached `devices.processes` watchdog inventory, which remains name/count oriented.
- Manual agent update requests from Device Summary or Device List call `POST /api/devices/agent-maintenance` with `action=update_now`. The Engine records `Update Borealis Agent` scheduled-job history, enqueues site-scoped `agent_maintenance_run` work, and site workers emit `agent_maintenance_request` to the device SYSTEM socket through the API internal bridge.
- The agent starts the local AutoUpdater scheduler path for that request. Windows uses the `Borealis Agent (AutoUpdater)` scheduled task; Linux uses `borealis-agent-updater.service` plus the hourly `borealis-agent-updater.timer`. Both paths consume the Engine cached Go binary bundle.
- Device Summary and Device List admins can change an agent's release channel and branch through the same `POST /api/devices/agent-maintenance` path with `action=switch_branch_channel`. The Engine stores `agent_release_channel` and `agent_branch` on the device row, records a `Switch Agent Branch/Channel` scheduled job, and lets heartbeat `agent_update_status` close each target as success or failure.
- File Management browse and mutation requests use the device SYSTEM Socket.IO channel with Socket.IO ACK responses under the `file_management_request` event instead of piggybacking on quick-job stdout/stderr.
- File transfers use Engine temp-file staging plus device-authenticated agent pull/push endpoints so large uploads and downloads do not have to fit inside one socket payload.
- Folder uploads rely on an Engine-staged upload manifest that preserves each browser file's relative path so the agent can rebuild the destination tree incrementally without requiring the whole folder to ride in one payload.
- Transfer progress updates now double as cancellation checkpoints: the operator requests cancellation through the Engine, the Engine marks the transfer `cancel_requested`, and the agent polls that control snapshot while streaming upload chunks or building archives.

### Status computation
- Online/offline is computed from `last_seen` (online if within ~300 seconds).
- UI tables use the derived `status` field from the API payload.
- Agent Health startup flow status is not a replacement for online/offline. It explains startup progression, especially auth, Socket.IO, role loading, WireGuard, inventory, and steady-state milestones.

### Device identity and keys
- Device identity is tied to GUID + SSL fingerprint + token version.
- `DeviceAuthManager` enforces fingerprint matches and token version checks.
- Purge inserts a `device_purge_barriers` row so deleted devices cannot be silently recreated by stale access tokens or stale refresh tokens.
- Re-enrollment clears the barrier only after the recreated device row has been seeded with a token version at or above the purge barrier.

### Sites and enrollment codes
- Sites live in `sites` and `device_sites` tables (see `Data/Engine/Containers/api-backend/data/database.py`).
- Operator RBAC scope lives in `user_site_assignments`.
- Enrollment codes are stored directly on `sites.enrollment_code`.
- Rotating a site code updates the `sites` record only.
- `GET /api/sites` also returns `public_base_url` and `public_hostname` so the Sites WebUI can generate per-site agent install commands without a second server-info fetch.

### Device filters (matching)
- Filters are stored in typed basic/advanced payloads and normalized by `Data/Engine/Containers/api-backend/data/services/filters/matcher.py`.
- `DeviceFilterMatcher.fetch_devices()` loads a snapshot from `devices` and joins `sites`.
- It also joins normalized software rows from `device_software_inventory`.
- It also joins sparse metadata rows from `device_metadata_fields` so criteria can match text values or empty fields by `metadata_field_number`.
- `count_filter_devices` computes match counts for UI summaries and scheduler previews.

### Approval flow detail
- Enrollment requests create approval records (pending).
- Approval access is site-scoped for operators and unrestricted for admins.
- Approval handles hostname conflicts (merge or rename).
- Denials are logged and remove pending requests.
- Invalid-code enrollment attempts do not create approval records. Admins can surface them through the Device Approval Queue `Invalid Code` filter for recent active failures.

### WebUI deep links
- Device Details route: `/devices/<agent_guid_or_hostname>`.
- Tab query keys: `device_summary`, `metadata_fields`, `installed_software`, `services`, `process_management`, `activity_history`, `remote_shell`, `remote_desktop`.
- Device Details also exposes the `file_management` tab query key for the File Management view.
- File Management also exposes a `working_directory` query param for shareable folder deep links, for example `?tab=file_management&working_directory=C%3A%5CUsers%5Cnicole.rappe%5CDesktop`.
- Route registration and URL preservation are implemented in `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/routes/router.jsx` plus `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/routes/paths.js`; component-level tab URL sync is implemented in `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Device_Summary.jsx`.
- Shared header hostname search is implemented in `Data/Engine/Containers/webui-frontend/data/web-interface/src/GlobalDeviceSearch.jsx` and queries `GET /api/devices/search`.

### Debug checklist
- Device missing from list: check PostgreSQL `engine.devices` and `engine.device_keys`.
- Online status wrong: check `last_seen` timestamps in `devices` table.
- Filter counts zero: validate the active criteria payload plus `device_software_inventory` or `device_metadata_fields` rows when software or metadata criteria are involved.
