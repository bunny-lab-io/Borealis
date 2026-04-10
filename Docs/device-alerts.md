# Device Alerts
[Back to Docs Index](index.md) | [Index (HTML)](index.html)

## Purpose
Describe the runtime incident side of Watchdogs: the Alerts queue, incident lifecycle, and per-device alert handling workflow.

## Alerts Queue
- Alerts lives under `Alerting & Reporting` in the sidebar.
- The queue is the operational surface for incidents opened by Watchdogs.
- v1 queue behavior includes:
  - open and resolved tabs
  - filter by severity, site, device, watchdog, and acknowledgement state
  - jump directly to the affected device
  - jump directly to the source watchdog policy
  - acknowledge incidents
  - suppress a watchdog for the impacted device

## Incident Lifecycle
- `open`: a watchdog met its match conditions and opened or refreshed an incident.
- `acknowledged`: an operator reviewed the incident and recorded ownership through the acknowledge action.
- `resolved`: the condition cleared, the watchdog was disabled or archived, the target disappeared, or telemetry became stale long enough for auto-resolution.
- `suppressed` or `disabled`: these are device override states applied at the watchdog/device relationship, not separate incident states.

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
- `GET /api/watchdogs/incidents` (Token Authenticated) - list incidents by runtime state.
- `POST /api/watchdogs/incidents/<int:incident_id>/acknowledge` (Token Authenticated) - acknowledge an open incident.
- `GET /api/devices/<device_id>/watchdogs` (Token Authenticated) - load the current device watchdog view, including incidents, assignments, and overrides.
- `POST /api/devices/<device_id>/watchdogs/overrides` (Token Authenticated) - create, update, or clear a per-device watchdog override.

## Related Documentation
- [Watchdogs](watchdogs.md)
- [Device Management](device-management.md)
- [UI and Notifications](ui-and-notifications.md)
- [Logging and Operations](logging-and-operations.md)
- [API Reference](api-reference.md)

## Codex Agent (Detailed)
### Main implementation files
- Alerts page: `Data/Engine/web-interface/src/Alerting/Active_Alerts.jsx`
- Device tab: `Data/Engine/web-interface/src/Devices/Tabs/Device_Watchdogs.jsx`
- Device Summary integration: `Data/Engine/web-interface/src/Devices/Tabs/Device_Summary.jsx`
- Incident runtime: `Data/Engine/services/API/watchdogs/runtime.py`

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

### Suppression behavior
- Device suppressions are stored in `watchdog_device_overrides`.
- An active override prevents further incident creation for that watchdog/device pair.
- Applying an override immediately re-evaluates the watchdog, resolves any open incident for that device, and emits the refresh socket events.
