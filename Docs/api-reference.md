# API Reference
[Back to Docs Index](index.md) | [Index (HTML)](index.html)

## Purpose
Provide a consolidated, human-readable list of Borealis Engine API endpoints grouped by domain.

## API Endpoints

### Core
- `GET /health` (No Authentication) - liveness probe.

### Authentication and Access Management
- `POST /api/auth/login` (No Authentication) - operator login. Borealis requires MFA setup or verification by default unless an administrator has explicitly disabled MFA for that operator.
- `POST /api/auth/logout` (Token Authenticated) - operator logout.
- `POST /api/auth/password/reset` (Token Authenticated) - verify the current operator password and replace it with a new password hash.
- `POST /api/auth/mfa/verify` (Token Authenticated, MFA pending) - verify MFA.
- `POST /api/auth/mfa/reset` (Token Authenticated) - clear the current operator's authenticator-app secret so Borealis prompts for MFA setup on the next password login. Passkeys are managed separately.
- `POST /api/auth/passkeys/register/options` (Token Authenticated) - start a WebAuthn passkey registration ceremony for the current operator.
- `POST /api/auth/passkeys/register/verify` (Token Authenticated) - verify and store a new WebAuthn passkey credential.
- `POST /api/auth/passkeys/authenticate/options` (No Authentication) - start a WebAuthn passkey sign-in ceremony for passwordless operator login.
- `POST /api/auth/passkeys/authenticate/verify` (No Authentication) - verify a WebAuthn passkey sign-in response and complete operator login.
- `GET /api/auth/passkeys` (Token Authenticated) - list the current operator's enrolled passkeys.
- `PATCH /api/auth/passkeys/<int:passkey_id>` (Token Authenticated) - rename one of the current operator's passkeys.
- `DELETE /api/auth/passkeys/<int:passkey_id>` (Token Authenticated) - remove one of the current operator's passkeys.
- `GET /api/auth/me` (Token Authenticated) - current operator profile, including MFA-enabled state and passkey count for account menu actions.
- `GET /api/credentials` (Token Authenticated) - list stored remote-execution credentials.
- `GET /api/credentials/<int:credential_id>` (Token Authenticated) - get one stored credential without secret material.
- `POST /api/credentials` (Admin) - create a stored credential.
- `PUT /api/credentials/<int:credential_id>` (Admin) - update a stored credential.
- `DELETE /api/credentials/<int:credential_id>` (Admin) - delete a stored credential.
- `GET /api/users` (Admin) - list operator accounts.
- `POST /api/users` (Admin) - create operator account.
- `DELETE /api/users/<username>` (Admin) - delete operator account.
- `POST /api/users/<username>/reset_password` (Admin) - reset operator password.
- `POST /api/users/<username>/role` (Admin) - update operator role.
- `POST /api/users/<username>/mfa` (Admin) - enable, disable, or reset MFA for an operator. Disabling MFA is admin-only.
- `POST /api/user_site_assignments/selection` (Admin) - load current site assignments for selected operators.
- `POST /api/user_site_assignments/assign` (Admin) - replace site assignments for selected operators.
- `GET /api/github/token` (Admin) - GitHub API token status.
- `POST /api/github/token` (Admin) - update GitHub API token.

### Enrollment and Tokens
- `POST /api/agent/enroll/request` (No Authentication) - submit enrollment request.
- `POST /api/agent/enroll/poll` (No Authentication) - finalize approved enrollment, including recreating a previously purged GUID with a bumped token version after fresh approval.
- `POST /api/agent/token/refresh` (Refresh Token) - mint new access token; returns `401 device_purged` when a GUID is blocked by a purge barrier.

### Devices and Inventory
- `POST /api/agent/heartbeat` (Device Authenticated) - heartbeat + metrics.
- `POST /api/agent/details` (Device Authenticated) - full hardware, inventory, and cached service payload.
- `POST /api/agent/script/request` (Device Authenticated) - request work or idle signal.
- `POST /api/agent/vpn/ensure` (Device Authenticated) - persistent WireGuard tunnel bootstrap.
- `GET /api/agents` (Token Authenticated) - list online collectors by hostname/context.
- `GET /api/devices` (Token Authenticated) - device summary list, scoped to the operator's assigned sites unless the operator is an admin.
- `GET /api/devices/search?hostname=<query>` (Token Authenticated) - hostname search matches for the shared header search, scoped to the operator's assigned sites unless the operator is an admin.
- `GET /api/devices/<guid>` (Token Authenticated) - device summary by GUID, site-scoped for operators.
- `POST /api/devices/<guid>/purge` (Admin) - purge a device, revoke stale trust state, remove current-known references, and rewrite scheduled-job targets that referenced the device.
- `GET /api/device/details/<hostname>` (Token Authenticated) - full device details, site-scoped for operators.
- `GET /api/device/services/<hostname>` (Token Authenticated) - cached service inventory for an in-scope device.
- `POST /api/device/services/<hostname>/action` (Token Authenticated) - start, stop, or restart a named service on an in-scope device.
- `POST /api/device/update-agent/<hostname>` (Token Authenticated) - queue an immediate agent updater run for an in-scope device.
- `POST /api/device/description/<hostname>` (Token Authenticated) - update description for an in-scope device.
- `GET /api/device_list_views` (Token Authenticated) - list saved device views.
- `GET /api/device_list_views/<int:view_id>` (Token Authenticated) - get saved view.
- `POST /api/device_list_views` (Token Authenticated) - create saved view.
- `PUT /api/device_list_views/<int:view_id>` (Token Authenticated) - update saved view.
- `DELETE /api/device_list_views/<int:view_id>` (Token Authenticated) - delete saved view.
- `GET /api/sites` (Token Authenticated) - list sites visible to the current operator, plus `public_base_url` / `public_hostname` metadata for install-command UIs.
- `POST /api/sites` (Admin) - create site.
- `POST /api/sites/delete` (Admin) - delete sites.
- `GET /api/sites/device_map` (Token Authenticated) - hostname to site map for devices in the current operator's site scope.
- `POST /api/sites/assign` (Admin) - assign devices to site.
- `POST /api/sites/rename` (Admin) - rename site.
- `GET /api/repo/current_hash` (Device or Token Authenticated) - current agent repo hash.
- `GET /api/agent/hash` (Device Authenticated) - get agent hash.
- `POST /api/agent/hash` (Device Authenticated) - update agent hash.
- `GET /api/agent/hash_list` (Admin + Loopback) - list agent hashes (local diagnostics).

### Approvals and Install Codes
- `GET /api/admin/enrollment-codes` (Admin) - list static site enrollment codes.
- `POST /api/admin/enrollment-codes` (Admin) - deprecated (returns 410; use site APIs).
- `DELETE /api/admin/enrollment-codes/<code_id>` (Admin) - deprecated (returns 410; use site APIs).
- `GET /api/admin/device-approvals` (Token Authenticated) - approval queue, scoped to the current operator's assigned sites unless the operator is an admin.
- `POST /api/admin/device-approvals/<approval_id>/approve` (Token Authenticated) - approve an in-scope device enrollment.
- `POST /api/admin/device-approvals/<approval_id>/deny` (Token Authenticated) - deny an in-scope device enrollment.

### Device Filters
- `GET /api/device_filters` (Token Authenticated) - list filters.
- `GET /api/device_filters/metadata` (Token Authenticated) - filter field/operator metadata.
- `POST /api/device_filters/preview` (Token Authenticated) - manual filter preview against current inventory, restricted to the current operator's site scope.
- `GET /api/device_filters/<filter_id>` (Token Authenticated) - get filter.
- `GET /api/device_filters/<filter_id>/usage` (Token Authenticated) - scheduled-job usage summary.
- `POST /api/device_filters` (Token Authenticated) - create filter within the current operator's site scope.
- `PUT /api/device_filters/<filter_id>` (Token Authenticated) - update filter within the current operator's site scope.
- `POST /api/device_filters/<filter_id>/clone` (Token Authenticated) - clone filter.
- `POST /api/device_filters/<filter_id>/archive` (Token Authenticated) - archive filter.
- `POST /api/device_filters/<filter_id>/unarchive` (Token Authenticated) - unarchive filter.
- `DELETE /api/device_filters/<filter_id>` (Token Authenticated) - delete filter.

### Watchdogs and Device Alerts
- `GET /api/watchdogs` (Token Authenticated) - list watchdog policies within the current operator's site scope.
- `GET /api/watchdogs/metadata` (Token Authenticated) - watchdog editor metadata for rule types, action types, severities, and scope modes.
- `POST /api/watchdogs/preview` (Token Authenticated) - resolve targets and preview current watchdog evaluation results.
- `GET /api/watchdogs/<int:watchdog_id>` (Token Authenticated) - get one watchdog policy.
- `POST /api/watchdogs` (Token Authenticated) - create a watchdog policy.
- `PUT /api/watchdogs/<int:watchdog_id>` (Token Authenticated) - update a watchdog policy.
- `DELETE /api/watchdogs/<int:watchdog_id>` (Token Authenticated) - delete a watchdog policy and its runtime state.
- `GET /api/watchdogs/incidents` (Token Authenticated) - list watchdog incidents in `open`, `suppressed`, `resolved`, or `all` state within the current operator's visible scope, including queue counts.
- `POST /api/watchdogs/incidents/<int:incident_id>/acknowledge` (Token Authenticated) - acknowledge an open watchdog incident.
- `POST /api/watchdogs/incidents/<int:incident_id>/state` (Token Authenticated) - move a watchdog incident between the `open` and `suppressed` queues.
- `GET /api/devices/<device_id>/watchdogs` (Token Authenticated) - load the device Watchdogs tab payload, including incidents, assignments, and overrides.
- `POST /api/devices/<device_id>/watchdogs/overrides` (Token Authenticated) - create, update, or clear a per-device watchdog override.

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
- `POST /api/assemblies/<assembly_guid>/official-update` (Admin) - update one official Aurora assembly from the active catalog.
- `POST /api/assemblies/official/update-all` (Admin) - sync all official Aurora assemblies, including newly added catalog entries.
- `POST /api/scripts/quick_run` (Token Authenticated) - quick agent-side script job (`powershell`, `batch`, or `bash`, depending on the target agent platform/runtime) for in-scope devices only.
- `GET /api/device/activity/<hostname>` (Token Authenticated) - device activity history for an in-scope device.
- `DELETE /api/device/activity/<hostname>` (Token Authenticated) - clear activity history.
- `GET /api/device/activity/job/<int:job_id>` (Token Authenticated) - activity record details for an in-scope device activity.

Playbook execution currently happens through scheduled jobs with `execution_context` set to `local`, `ssh`, `ssh_individual`, `winrm`, or `winrm_individual`.

### Scheduled Jobs
- `GET /api/scheduled_jobs` (Token Authenticated) - list scheduled jobs visible within the current operator's site scope.
- `POST /api/scheduled_jobs` (Token Authenticated) - create scheduled job with targets constrained to the current operator's site scope.
- `GET /api/scheduled_jobs/<int:job_id>` (Token Authenticated) - get a scheduled job if it is visible within the current operator's site scope.
- `PUT /api/scheduled_jobs/<int:job_id>` (Token Authenticated) - update a scheduled job within the current operator's site scope.
- `POST /api/scheduled_jobs/<int:job_id>/toggle` (Token Authenticated) - enable/disable.
- `DELETE /api/scheduled_jobs/<int:job_id>` (Token Authenticated) - delete scheduled job.
- `GET /api/scheduled_jobs/<int:job_id>/runs` (Token Authenticated) - run history.
- `GET /api/scheduled_jobs/<int:job_id>/devices` (Token Authenticated) - device results.
- `DELETE /api/scheduled_jobs/<int:job_id>/runs` (Token Authenticated) - clear run history.

### Notifications
- `POST /api/notifications/notify` (Token Authenticated) - broadcast toast notification.

### VPN and Remote Access
- `POST /api/tunnel/connect` (Token Authenticated) - ensure WireGuard tunnel material for an in-scope agent.
- `GET /api/tunnel/status` (Token Authenticated) - tunnel status by in-scope agent.
- `GET /api/tunnel/active` (Token Authenticated) - list active tunnels visible in the current operator's site scope.

### VNC
- `POST /api/agent/vnc/ensure` (Device Authenticated) - ensure always-on VNC tunnel/readiness state, refresh the Engine's cached agent VNC credential, and return active session metadata for the agent.
- `POST /api/vnc/establish` (Token Authenticated) - establish or join a VNC collaboration session for an in-scope device.
- `POST /api/vnc/disconnect` (Token Authenticated) - leave or close a VNC collaboration session for an in-scope device.
- `POST /api/vnc/handoff` (Token Authenticated) - reassign session-owner metadata inside an active shared VNC collaboration session.
- `GET /api/vnc/sessions` (Token Authenticated) - list active VNC collaboration sessions visible within the current operator's site scope.
- `POST /api/vnc/session` (Token Authenticated) - legacy alias for establish.

### Remote Shell
- `POST /api/shell/establish` (Token Authenticated) - establish remote shell session for an in-scope device.
- `POST /api/shell/disconnect` (Token Authenticated) - disconnect remote shell session for an in-scope device.

### Server Info and Logs
- `GET /api/server/time` (Operator Session) - server clock.
- `GET /api/server/timezones` (Admin) - list the current engine host timezone and the selectable timezone inventory for WebUI timezone management.
- `POST /api/server/timezone` (Admin) - change the timezone used by the entire engine host.
- `GET /api/server/overview` (Admin) - consolidated Engine host overview used by the Server Info dashboard, including service state, public cert status, live operator sessions, WireGuard runtime state, Aegis state, and host resource basics.
- `GET /api/server/ansible-runner-settings` (Admin) - read the persisted per-job and global scheduled-Ansible runner limits used by the Engine scheduler.
- `PUT /api/server/ansible-runner-settings` (Admin) - update the persisted per-job and global scheduled-Ansible runner limits used by the Engine scheduler.
- `POST /api/server/services/<service_key>/restart` (Admin) - queue a detached `systemd-run` restart for `borealis_engine`, `borealis_traefik`, or a `postgresql_cluster` instance.
- `POST /api/server/wireguard/recover` (Admin) - force a Borealis WireGuard listener recovery attempt when active VPN sessions exist.
- `GET /api/server/logs` (Admin) - list logs and retention.
- `GET /api/server/logs/<log_name>/entries` (Admin) - tail log lines.
- `PUT /api/server/logs/retention` (Admin) - update retention policies.
- `DELETE /api/server/logs/<log_name>` (Admin) - delete log file(s).

## Related Documentation
- [Engine Runtime](engine-runtime.md)
- [Database Reference](db-reference.md)
- [Device Management](device-management.md)
- [Watchdogs](watchdogs.md)
- [Device Alerts](device-alerts.md)
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
