# Watchdogs

## Purpose
Describe Borealis Watchdogs: reusable monitoring policies that evaluate existing device data, explain why they matched, and optionally launch native remediation.

## Visual Tour

<figure class="bo-screenshot">
  <img src="../images/repo_screenshots/Watchdog_List.png" alt="Borealis watchdog list" loading="lazy">
  <figcaption>Watchdog List is the policy surface for monitoring rules, assignment scope, and remediation.</figcaption>
</figure>

## Watchdogs at a Glance
- Watchdogs live under `Automation` in the sidebar because they are policy-authoring and remediation tools.
- Each watchdog has:
  - name and description
  - severity
  - site scope
  - device targets
  - rule logic
  - remediation actions
  - evaluation cadence and debounce controls
- Watchdog changes become live immediately on save. Borealis does not use a separate deploy-later step for watchdog policies.

## Authoring Model
### Scope
- Scope uses the same site model Borealis already uses for filters:
  - `global`
  - `specific_sites`
  - `global_exclusions`
- Operator visibility follows effective site scope. Non-admin operators can only see and save watchdogs whose included sites are fully inside their assigned site set.

### Targets
- Watchdogs target devices by:
  - all devices in the current watchdog scope
  - explicit device targets
  - saved device filters
- Filter targets stay dynamic. When inventory changes, the watchdog's resolved device set changes with it.
- `All Devices in Scope` resolves every device that currently falls inside the watchdog's site scope, without requiring a separate saved filter.
- The Watchdog editor uses 3-character minimum search inputs for explicit device targeting and saved filter targeting so operators can type to find matches instead of working through long dropdown menus.

### Rules
- Watchdog v1 now spans both existing device inventory and a small set of Engine-persisted snapshot telemetry from the Agent:
  - `device_offline`
  - `storage_usage_percent` with either a specific drive or all drives on the device
  - `service_state`
  - `agent_role_health`
  - `cpu_usage_percent`
  - `memory_usage_percent`
  - `uptime_above_seconds`
  - `reboot_detected`
  - `service_pending_timeout`
  - `user_session_match`
  - `process_presence`
  - `session_state`
  - `network_interface_change`
  - `drive_presence_change`
  - `software_presence_or_version`
  - `agent_version_status`
- Rule combination is `all` or `any`. Nested boolean trees are not part of v1.
- Rule evaluation treats stale telemetry as its own outcome. Borealis surfaces `stale_data` instead of converting stale inventory into a false incident.
- Change-detection rules such as reboot, drive topology, interface topology, and session transitions establish a baseline snapshot first instead of alerting on the first evaluation.

### Anti-noise Controls
- Watchdogs include:
  - evaluation interval
  - cooldown between remediation runs
  - auto-resolve delay after the condition clears
  - minimum consecutive matches before an incident opens
  - boot grace period
- Offline-only watchdogs treat auto-clear differently: when the device checks back in, the transient offline incident is purged instead of retained as a resolved record.

### Actions
- v1 actions are:
  - `Do Nothing` for incident-only tracking with no notification or remediation
  - Engine Toast Notification
  - control a Windows service
  - run an assembly remediation
- `Run Assembly` surfaces the selected assembly's runtime variables directly in the Watchdog editor so operators can pass ad-hoc values at save time.
- Assembly remediation supports:
  - script assemblies by queueing a watchdog-owned quick job and applying `variable_values` at runtime
  - workflow assemblies by starting an async workflow run and carrying `variable_values` in the workflow trigger metadata
  - Ansible assemblies by queueing an Engine-side Ansible run in `local` execution context with the configured `variable_values`

## Preview Behavior
- The Watchdog editor includes a dedicated `Preview` tab.
- Preview resolves current targets and shows per-device results before save.
- Each preview row includes:
  - hostname
  - site
  - current device status
  - watchdog evaluation state
  - matched or not matched
  - the rule explanation sampled from current inventory

## UI Surfaces
- Global routes:
  - `/automation/watchdogs`
  - `/automation/watchdogs/new`
  - `/automation/watchdogs/:watchdogId`
- Main files:
  - `Data/Engine/Containers/webui-frontend/data/web-interface/src/Automation/Watchdogs/Watchdog_List.jsx`
  - `Data/Engine/Containers/webui-frontend/data/web-interface/src/Automation/Watchdogs/Watchdog_Editor.jsx`
  - `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/route-modules/watchdogRoutes.jsx`
- Device pages can launch a prefilled watchdog draft through the Device Summary `Actions` menu and the device-level `Watchdogs` tab.

## API Endpoints
- `GET /api/watchdogs` (Token Authenticated) - list watchdogs within the current operator scope.
- `GET /api/watchdogs/metadata` (Token Authenticated) - watchdog editor metadata for site modes, match modes, rule types, action types, and severities.
- `POST /api/watchdogs/preview` (Token Authenticated) - resolve targets and preview the current watchdog outcome.
- `GET /api/watchdogs/<int:watchdog_id>` (Token Authenticated) - get one watchdog.
- `POST /api/watchdogs` (Token Authenticated) - create one watchdog.
- `PUT /api/watchdogs/<int:watchdog_id>` (Token Authenticated) - update one watchdog.
- `DELETE /api/watchdogs/<int:watchdog_id>` (Token Authenticated) - delete one watchdog and its runtime state.

## Related Documentation
- [Device Alerts](../Operations%20and%20Remote%20Access/device-alerts.md)
- [Device Management](../Operations%20and%20Remote%20Access/device-management.md)
- [Assemblies and Quick Jobs](assemblies.md)
- [UI and Notifications](../Start%20Here/ui-and-notifications.md)
- [API Reference](../Data%20and%20Schema/api-reference.md)

??? example "Detailed Codex Breakdown"

    ### Main implementation files
    - API registration: `Data/Engine/Containers/api-backend/data/services/API/watchdogs/management.py`
    - Runtime service: `Data/Engine/Containers/api-backend/data/services/API/watchdogs/runtime.py`
    - Schema migration: `Data/Engine/Containers/api-backend/data/database_migrations.py`
    - Navigation: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Navigation_Sidebar.jsx`
    - Routes: `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/routes/router.jsx` and `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/routes/paths.js`

    ### Runtime ownership
    - `WatchdogRuntimeService` is attached to `EngineContext.watchdog_runtime`.
    - The Engine app factory wires the watchdog API registration after the normal API/WebUI/Socket.IO registrars.
    - The runtime keeps a background evaluator loop that periodically re-evaluates enabled watchdogs whose `evaluation_interval_seconds` has elapsed.

    ### Device data sources used by v1
    - Device heartbeat age from `devices.last_seen`
    - Heartbeat performance metrics from `devices.cpu_percent` and `devices.memory_percent`
    - Storage JSON from `devices.storage`
    - Cached session snapshots from `devices.sessions`
    - Cached process snapshots from `devices.processes`
    - Cached service inventory from `devices.services`
    - Agent role telemetry from `devices.agent_role_health`
    - Normalized software inventory from `device_software_inventory`
    - Agent repo/hash comparison from `devices.agent_hash` plus the Engine-side current-repo-hash lookup

    ### State and incident tables
    - `watchdogs`
    - `watchdog_sites`
    - `watchdog_targets`
    - `watchdog_device_overrides`
    - `watchdog_device_state`
    - `watchdog_incidents`

    ### Runtime notes
    - Saving a watchdog immediately re-evaluates it so operators see current state without waiting for the next scheduler tick.
    - Disabling or archiving a watchdog resolves open incidents and marks saved device state as disabled instead of leaving stale incidents behind.
    - Deleting a watchdog removes related targets, site scope rows, overrides, runtime state rows, and incidents.
    - On Engine startup, Borealis purges any lingering resolved incidents belonging to offline-only watchdogs so transient offline history does not accumulate after restarts or earlier bugs.
