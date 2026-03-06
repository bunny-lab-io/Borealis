# Device Management
[Back to Docs Index](index.md) | [Index (HTML)](index.html)

## Purpose
Explain how Borealis tracks devices, ingests inventory, manages sites and filters, and handles enrollment approvals.

## Inventory and Status
- Agents send heartbeats and inventory payloads to the Engine.
- The Engine stores device summaries and detailed hardware/software data in SQLite.
- Online status is derived from `last_seen` (online if the heartbeat is within ~5 minutes).

## Sites and Enrollment Codes
- Sites group devices for organizational and targeting purposes.
- Each site can have an enrollment code that agents can use during install.
- Site mapping is stored separately from device records and exposed via API.

## Device Filters
- Filters are stored in the `device_filters` table as JSON criteria groups.
- The Engine computes match counts using `DeviceFilterMatcher` against the inventory snapshot.
- Filters can be global or scoped to a site.

## Device List Views
- Operators can save custom table views for the device list UI.
- Views are stored per operator and exposed via `/api/device_list_views`.

## Enrollment Approvals
- Enrollment requests are queued for admin approval.
- Approvals enforce hostname conflict checks and device identity tracking.

## API Endpoints
- `POST /api/agent/heartbeat` (Device Authenticated) - heartbeat + metrics.
- `POST /api/agent/details` (Device Authenticated) - inventory payloads.
- `GET /api/agents` (Token Authenticated) - online collectors grouped by context.
- `GET /api/devices` (Token Authenticated) - device summary list.
- `GET /api/devices/<guid>` (Token Authenticated) - device summary by GUID.
- `GET /api/device/details/<hostname>` (Token Authenticated) - full device details.
- `POST /api/device/description/<hostname>` (Token Authenticated) - update description.
- `GET /api/device_list_views` (Token Authenticated) - list saved views.
- `GET /api/device_list_views/<int:view_id>` (Token Authenticated) - get saved view.
- `POST /api/device_list_views` (Token Authenticated) - create saved view.
- `PUT /api/device_list_views/<int:view_id>` (Token Authenticated) - update saved view.
- `DELETE /api/device_list_views/<int:view_id>` (Token Authenticated) - delete saved view.
- `GET /api/sites` (Token Authenticated) - list sites.
- `POST /api/sites` (Admin) - create site.
- `POST /api/sites/delete` (Admin) - delete sites.
- `GET /api/sites/device_map` (Token Authenticated) - hostname to site map.
- `POST /api/sites/assign` (Admin) - assign devices to site.
- `POST /api/sites/rename` (Admin) - rename site.
- `GET /api/device_filters` (Token Authenticated) - list filters.
- `GET /api/device_filters/<filter_id>` (Token Authenticated) - get filter.
- `POST /api/device_filters` (Token Authenticated) - create filter.
- `PUT /api/device_filters/<filter_id>` (Token Authenticated) - update filter.
- `GET /api/admin/enrollment-codes` (Admin) - list static site enrollment codes.
- `POST /api/admin/enrollment-codes` (Admin) - deprecated (returns 410; use site APIs).
- `DELETE /api/admin/enrollment-codes/<code_id>` (Admin) - deprecated (returns 410; use site APIs).
- `GET /api/admin/device-approvals` (Admin) - approval queue.
- `POST /api/admin/device-approvals/<approval_id>/approve` (Admin) - approve device.
- `POST /api/admin/device-approvals/<approval_id>/deny` (Admin) - deny device.

## Related Documentation
- [Agent Runtime](agent-runtime.md)
- [Database Reference](db-reference.md)
- [Security and Trust](security-and-trust.md)
- [Scheduled Jobs](scheduled-jobs.md)
- [VPN and Remote Access](vpn-and-remote-access.md)
- [API Reference](api-reference.md)

## Codex Agent (Detailed)
### Key files and services
- Device APIs: `Data/Engine/services/API/devices/` (management, approval, tunnel, vnc, routes).
- Filters: `Data/Engine/services/filters/matcher.py` and `Data/Engine/services/API/filters/management.py`.
- Enrollment approvals: `Data/Engine/services/API/devices/approval.py`.

### Inventory ingestion behavior
- `/api/agent/heartbeat` updates `last_seen` and key metrics (last_user, OS, uptime).
- `/api/agent/details` stores full inventory payloads for memory, network, storage, software, cpu.
- JSON blobs are serialized into SQLite text columns and rehydrated for UI.

### Status computation
- Online/offline is computed from `last_seen` (online if within ~300 seconds).
- UI tables use the derived `status` field from the API payload.

### Device identity and keys
- Device identity is tied to GUID + SSL fingerprint + token version.
- `DeviceAuthManager` enforces fingerprint matches and token version checks.

### Sites and enrollment codes
- Sites live in `sites` and `device_sites` tables (see `Data/Engine/database.py`).
- Enrollment codes are stored directly on `sites.enrollment_code`.
- Rotating a site code updates the `sites` record only.

### Device filters (matching)
- Filters are stored as JSON criteria groups in `device_filters.criteria_json`.
- `DeviceFilterMatcher.fetch_devices()` loads a snapshot from `devices` and joins `sites`.
- `count_filter_devices` computes match counts for UI summaries.

### Approval flow detail
- Enrollment requests create approval records (pending).
- Admin approval handles hostname conflicts (merge or rename).
- Denials are logged and remove pending requests.

### WebUI deep links
- Device Details route: `/device/<agent_guid_or_hostname>`.
- Tab query keys: `device_summary`, `installed_software`, `activity_history`, `remote_shell`, `remote_desktop`.
- Route parsing and URL preservation are implemented in `Data/Engine/web-interface/src/App.jsx`; component-level tab URL sync is implemented in `Data/Engine/web-interface/src/Devices/Device_Details.jsx`.

### Debug checklist
- Device missing from list: check `Engine/database.db` tables `devices` and `device_keys`.
- Online status wrong: check `last_seen` timestamps in `devices` table.
- Filter counts zero: validate `device_filters.criteria_json` and matcher logic.
