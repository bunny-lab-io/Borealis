# Device Alerts

## Purpose
Describe the runtime incident side of Watchdogs: the Alerts queue, incident lifecycle, and per-device alert handling workflow.

## Alerts Queue
- Alerts lives under `Alerting & Reporting` in the sidebar.
- The queue is the operational surface for incidents opened by Watchdogs.
- v1 queue behavior includes:
  - the page loads with all alerts visible by default, ordered with the newest alert instances at the top
  - pill-style `Open`, `Suppressed`, and `Resolved` queue filters with live counts
  - clicking a status pill filters the table to that queue, and clicking the same pill again clears the filter back to the full all-alerts view
  - AG Grid column filters for severity, site, device, watchdog, message, timestamps, and acknowledgement metadata
  - a `Status` column that mirrors the current incident state in the grid
  - device hostname links directly into the affected device page
  - the watchdog name links directly into the source watchdog policy
  - acknowledge incidents
  - suppress an active incident into the dedicated suppressed queue
  - `RE-OPEN` for suppressed incidents that should return to the open queue
  - the suppressed queue surfaces the operator-provided suppression reason directly in the table

## Incident Lifecycle
- `open`: a watchdog met its match conditions and opened or refreshed an incident.
- `acknowledged`: an operator reviewed the incident and recorded ownership through the acknowledge action.
- `suppressed`: an operator intentionally muted the current incident without marking the underlying condition as fixed. Suppressed incidents stay historical until manually reopened or until the condition truly clears.
- `resolved`: the condition cleared, the watchdog was disabled or archived, the target disappeared, or telemetry became stale long enough for auto-resolution.
- Offline-only watchdog incidents are the exception: once the device checks back in and the offline condition auto-clears, Borealis purges that incident instead of keeping a resolved historical row.
- Engine startup also purges any lingering resolved offline-only incidents so stale transient history from prior bugs or restart timing does not remain in Alerts.
- If a resolved condition later comes back, Borealis opens a new incident record so operators keep a clean historical dataset per device instead of overloading the old resolved entry.

## Device-Level Workflow
- Device Summary now includes a `Watchdogs` tab.
- The device tab is incident-first and shows:
  - active incidents
  - effective watchdog assignments
  - active device-specific overrides
- Operators can:
  - launch `New Watchdog for This Device`
  - suppress a shared watchdog only for the current device
  - clear a device-specific suppression
  - acknowledge the current device's incidents
  - open the source watchdog policy

## Real-time Refresh
- The Engine emits:
  - `watchdog_incidents_changed`
  - `device_watchdogs_changed`
- Borealis uses those socket events to refresh the Watchdog list, Alerts queue, and the device-level Watchdogs tab without waiting for a full poll cycle.

## API Endpoints
- `GET /api/watchdogs/incidents` (Token Authenticated) - list incidents by runtime state and return queue counts for `open`, `suppressed`, and `resolved`.
- `POST /api/watchdogs/incidents/<int:incident_id>/acknowledge` (Token Authenticated) - acknowledge an open incident.
- `POST /api/watchdogs/incidents/<int:incident_id>/state` (Token Authenticated) - move an incident between the `open` and `suppressed` queues.
- `GET /api/devices/<device_id>/watchdogs` (Token Authenticated) - load the current device watchdog view, including incidents, assignments, and overrides.
- `POST /api/devices/<device_id>/watchdogs/overrides` (Token Authenticated) - create, update, or clear a per-device watchdog override.

## Related Documentation
- [Watchdogs](../Automation%20and%20Execution/watchdogs.md)
- [Device Management](device-management.md)
- [UI and Notifications](../Engine%20Deployment/ui-and-notifications.md)
- [Logging and Operations](logging-and-operations.md)
- [API Reference](../Reference/Data%20and%20Schema/api-reference.md)

??? example "Detailed Codex Breakdown"

    ### Main implementation files
    - Alerts page: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Alerting/Active_Alerts.jsx`
    - Device tab: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Device_Watchdogs.jsx`
    - Device Summary integration: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Device_Summary.jsx`
    - Incident runtime: `Data/Engine/Containers/api-backend/data/services/API/watchdogs/runtime.py`

    ### Incident payload shape
    - Incident rows include:
      - watchdog id and name
      - hostname and device GUID
      - site id and site name
      - severity and state
      - title and message
      - sampled rule data
      - acknowledgement metadata
      - opened, updated, and resolved timestamps
    - Offline-only incidents that auto-clear are deleted, so they disappear from the queue instead of moving into the resolved dataset.

    ### Suppression behavior
    - Queue-level suppression is an incident-state transition, not just a visual filter.
    - Suppressed incidents remain distinct from resolved history so operators can re-open them later without manufacturing a brand-new record.
    - Device-level suppressions still exist separately in `watchdog_device_overrides` for the per-device Watchdogs tab.
