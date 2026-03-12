# API Reference
[Back to Docs Index](index.md) | [Index (HTML)](index.html)

## Purpose
Provide a consolidated, human-readable list of Borealis Engine API endpoints grouped by domain.

## API Endpoints

### Core
- `GET /health` (No Authentication) - liveness probe.

### Authentication and Access Management
- `POST /api/auth/login` (No Authentication) - operator login.
- `POST /api/auth/logout` (Token Authenticated) - operator logout.
- `POST /api/auth/mfa/verify` (Token Authenticated, MFA pending) - verify MFA.
- `GET /api/auth/me` (Token Authenticated) - current operator profile.
- `GET /api/users` (Admin) - list operator accounts.
- `POST /api/users` (Admin) - create operator account.
- `DELETE /api/users/<username>` (Admin) - delete operator account.
- `POST /api/users/<username>/reset_password` (Admin) - reset operator password.
- `POST /api/users/<username>/role` (Admin) - update operator role.
- `POST /api/users/<username>/mfa` (Admin) - toggle MFA/reset secrets.
- `GET /api/github/token` (Admin) - GitHub API token status.
- `POST /api/github/token` (Admin) - update GitHub API token.

### Enrollment and Tokens
- `POST /api/agent/enroll/request` (No Authentication) - submit enrollment request.
- `POST /api/agent/enroll/poll` (No Authentication) - finalize approved enrollment.
- `POST /api/agent/token/refresh` (Refresh Token) - mint new access token.

### Devices and Inventory
- `POST /api/agent/heartbeat` (Device Authenticated) - heartbeat + metrics.
- `POST /api/agent/details` (Device Authenticated) - full hardware/inventory payload.
- `POST /api/agent/script/request` (Device Authenticated) - request work or idle signal.
- `POST /api/agent/vpn/ensure` (Device Authenticated) - persistent WireGuard tunnel bootstrap.
- `GET /api/agents` (Token Authenticated) - list online collectors by hostname/context.
- `GET /api/devices` (Token Authenticated) - device summary list.
- `GET /api/devices/<guid>` (Token Authenticated) - device summary by GUID.
- `GET /api/device/details/<hostname>` (Token Authenticated) - full device details.
- `POST /api/device/description/<hostname>` (Token Authenticated) - update description.
- `GET /api/device_list_views` (Token Authenticated) - list saved device views.
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
- `GET /api/repo/current_hash` (Device or Token Authenticated) - current agent repo hash.
- `GET /api/agent/hash` (Device Authenticated) - get agent hash.
- `POST /api/agent/hash` (Device Authenticated) - update agent hash.
- `GET /api/agent/hash_list` (Admin + Loopback) - list agent hashes (local diagnostics).

### Admin Approvals and Install Codes
- `GET /api/admin/enrollment-codes` (Admin) - list static site enrollment codes.
- `POST /api/admin/enrollment-codes` (Admin) - deprecated (returns 410; use site APIs).
- `DELETE /api/admin/enrollment-codes/<code_id>` (Admin) - deprecated (returns 410; use site APIs).
- `GET /api/admin/device-approvals` (Admin) - approval queue.
- `POST /api/admin/device-approvals/<approval_id>/approve` (Admin) - approve device.
- `POST /api/admin/device-approvals/<approval_id>/deny` (Admin) - deny device.

### Device Filters
- `GET /api/device_filters` (Token Authenticated) - list filters.
- `GET /api/device_filters/metadata` (Token Authenticated) - filter field/operator metadata.
- `POST /api/device_filters/preview` (Token Authenticated) - manual filter preview against current inventory.
- `GET /api/device_filters/<filter_id>` (Token Authenticated) - get filter.
- `GET /api/device_filters/<filter_id>/usage` (Token Authenticated) - scheduled-job usage summary.
- `POST /api/device_filters` (Token Authenticated) - create filter.
- `PUT /api/device_filters/<filter_id>` (Token Authenticated) - update filter.
- `POST /api/device_filters/<filter_id>/clone` (Token Authenticated) - clone filter.
- `POST /api/device_filters/<filter_id>/archive` (Token Authenticated) - archive filter.
- `POST /api/device_filters/<filter_id>/unarchive` (Token Authenticated) - unarchive filter.
- `DELETE /api/device_filters/<filter_id>` (Token Authenticated) - delete filter.

### Assemblies and Execution
- `GET /api/assemblies` (Token Authenticated) - list assemblies.
- `GET /api/assemblies/<assembly_guid>` (Token Authenticated) - assembly details.
- `POST /api/assemblies` (Token Authenticated) - create assembly.
- `PUT /api/assemblies/<assembly_guid>` (Token Authenticated) - update assembly.
- `DELETE /api/assemblies/<assembly_guid>` (Token Authenticated) - delete assembly.
- `POST /api/assemblies/<assembly_guid>/clone` (Admin + Dev Mode for protected domains) - clone assembly.
- `POST /api/assemblies/dev-mode/switch` (Admin) - toggle dev mode.
- `POST /api/assemblies/dev-mode/write` (Admin + Dev Mode) - flush queued writes.
- `POST /api/assemblies/import` (Domain write permission) - import legacy JSON assembly.
- `GET /api/assemblies/<assembly_guid>/export` (Token Authenticated) - export legacy JSON.
- `POST /api/scripts/quick_run` (Token Authenticated) - quick job (PowerShell).
- `POST /api/ansible/quick_run` (Token Authenticated) - placeholder (not implemented).
- `GET /api/device/activity/<hostname>` (Token Authenticated) - device activity history.
- `DELETE /api/device/activity/<hostname>` (Token Authenticated) - clear activity history.
- `GET /api/device/activity/job/<int:job_id>` (Token Authenticated) - activity record details.

### Scheduled Jobs
- `GET /api/scheduled_jobs` (Token Authenticated) - list scheduled jobs.
- `POST /api/scheduled_jobs` (Token Authenticated) - create scheduled job.
- `GET /api/scheduled_jobs/<int:job_id>` (Token Authenticated) - get scheduled job.
- `PUT /api/scheduled_jobs/<int:job_id>` (Token Authenticated) - update scheduled job.
- `POST /api/scheduled_jobs/<int:job_id>/toggle` (Token Authenticated) - enable/disable.
- `DELETE /api/scheduled_jobs/<int:job_id>` (Token Authenticated) - delete scheduled job.
- `GET /api/scheduled_jobs/<int:job_id>/runs` (Token Authenticated) - run history.
- `GET /api/scheduled_jobs/<int:job_id>/devices` (Token Authenticated) - device results.
- `DELETE /api/scheduled_jobs/<int:job_id>/runs` (Token Authenticated) - clear run history.

### Notifications
- `POST /api/notifications/notify` (Token Authenticated) - broadcast toast notification.

### VPN and Remote Access
- `POST /api/tunnel/connect` (Token Authenticated) - ensure WireGuard tunnel material for an agent.
- `GET /api/tunnel/status` (Token Authenticated) - tunnel status by agent.
- `GET /api/tunnel/active` (Token Authenticated) - list active tunnels.

### VNC
- `POST /api/agent/vnc/ensure` (Device Authenticated) - ensure always-on VNC credentials for the agent.
- `POST /api/vnc/establish` (Token Authenticated) - establish VNC session.
- `POST /api/vnc/disconnect` (Token Authenticated) - disconnect VNC session.
- `POST /api/vnc/session` (Token Authenticated) - legacy alias for establish.

### Remote Shell
- `POST /api/shell/establish` (Token Authenticated) - establish remote shell session.
- `POST /api/shell/disconnect` (Token Authenticated) - disconnect remote shell session.

### Server Info and Logs
- `GET /api/server/time` (Operator Session) - server clock.
- `GET /api/server/certificates/root` (Operator Session) - download Borealis root CA certificate.
- `GET /api/server/logs` (Admin) - list logs and retention.
- `GET /api/server/logs/<log_name>/entries` (Admin) - tail log lines.
- `PUT /api/server/logs/retention` (Admin) - update retention policies.
- `DELETE /api/server/logs/<log_name>` (Admin) - delete log file(s).

## Related Documentation
- [Engine Runtime](engine-runtime.md)
- [Database Reference](db-reference.md)
- [Device Management](device-management.md)
- [Assemblies and Quick Jobs](assemblies.md)
- [Scheduled Jobs](scheduled-jobs.md)
- [VPN and Remote Access](vpn-and-remote-access.md)

## Codex Agent (Detailed)
### Where endpoints are defined
- Each API module begins with a header listing endpoints.
- Search under `Data/Engine/services/API/` to find the authoritative source.
- The registry lives in `Data/Engine/services/API/__init__.py`.

### How to keep this doc accurate
- When you add or remove a route, update:
  1) The module header comment in the source file.
  2) This `api-reference.md` page.
  3) The domain page (example: `device-management.md`).

### Quick discovery workflow
- Use `rg "# - (GET|POST|PUT|DELETE)" Data/Engine/services/API` to list endpoints.
- Cross-check auth requirements in each module (RequestAuthContext, session checks, or device auth decorators).
- If a route is Socket.IO only, document it in the relevant domain page instead of this REST list.

### Auth labels used in this doc
- No Authentication: open endpoints (rare).
- Token Authenticated: operator session or bearer token.
- Device Authenticated: agent JWT access token.
- Admin: operator must have Admin role.

### Example update scenario
- You add `POST /api/devices/retire`:
  - Update `Data/Engine/services/API/devices/management.py` header.
  - Add the endpoint under the Devices and Inventory section here.
  - Update `device-management.md` with behavior and UI impact.
