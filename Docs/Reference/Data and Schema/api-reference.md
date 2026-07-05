# API Reference

Provide a consolidated, human-readable list of Borealis Engine API endpoints grouped by domain.

??? example "Detailed Codex Breakdown"

    ### API endpoints

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
    - `GET /api/auth/me` (Token Authenticated) - current operator profile, including MFA-enabled state, auth source, and passkey count for account menu actions.
    - `GET /api/directory/providers` (Admin) - list LDAP, LDAPS, and Active Directory providers without secret material.
    - `POST /api/directory/providers` (Admin) - create a directory provider. Providers are disabled until connectivity test succeeds. Supports optional provider-scoped `host_overrides` for FQDN-to-IP LDAP connection routing.
    - `PATCH /api/directory/providers/<int:provider_id>` (Admin) - update a directory provider or toggle enablement after a passing test.
    - `DELETE /api/directory/providers/<int:provider_id>` (Admin) - delete a provider with no cached directory users.
    - `POST /api/directory/providers/certificate` (Admin) - download LDAPS peer-certificate metadata and PEM for operator review before pinning trust.
    - `POST /api/directory/providers/<int:provider_id>/test` (Admin) - verify provider connectivity and mark provider test state.
    - `POST /api/directory/providers/<int:provider_id>/lookup-user` (Admin) - run provider-scoped user lookup diagnostics, including group-role mapping and optional password verification.
    - `POST /api/directory/providers/<int:provider_id>/sync` (Admin) - re-check cached users and disable cache entries no longer found in the provider.
    - `POST /api/users/<username>/directory-cache` (Admin) - enable or disable a cached directory user.
    - `GET /api/credentials` (Token Authenticated) - list stored remote-execution credentials.
    - `GET /api/credentials/<int:credential_id>` (Token Authenticated) - get one stored credential without secret material.
    - `POST /api/credentials` (Admin) - create a stored credential.
    - `PUT /api/credentials/<int:credential_id>` (Admin) - update a stored credential.
    - `DELETE /api/credentials/<int:credential_id>` (Admin) - delete a stored credential.
    - `GET /api/users` (Admin) - list operator accounts, including local/directory source metadata.
    - `POST /api/users` (Admin) - create operator account.
    - `DELETE /api/users/<username>` (Admin) - delete operator account.
    - `POST /api/users/<username>/reset_password` (Admin) - reset operator password.
    - `POST /api/users/<username>/role` (Admin) - update operator role.
    - `POST /api/users/<username>/mfa` (Admin) - enable, disable, or reset MFA for an operator. Disabling MFA is admin-only.
    - `POST /api/user_site_assignments/selection` (Admin) - load current site assignments for selected operators.
    - `POST /api/user_site_assignments/assign` (Admin) - replace site assignments for selected operators.
    - `GET /api/github/token` (Admin) - GitHub API token status.
    - `POST /api/github/token` (Admin) - update GitHub API token.
    - `GET /api/server/backup/export` (Admin) - export encrypted Engine configuration backup JSON. Requires Aegis configured and unlocked.
    - `POST /api/server/backup/analyze` (Admin) - decrypt and validate encrypted backup JSON with the supplied Aegis Cipher, then return high-level import counts without modifying Engine state.
    - `POST /api/server/backup/restore` (Admin) - sterilize current Engine configuration/trust state, import encrypted backup JSON, clear operator cookies, and return `restart_required: true`.
    - `POST /api/bootstrap/backup/analyze` (No Authentication, bootstrap only) - decrypt and validate encrypted backup JSON before normal login is enabled, then return high-level import counts without modifying Engine state.
    - `POST /api/bootstrap/backup/restore` (No Authentication, bootstrap only) - import encrypted backup JSON before normal login is enabled. Requires the source Aegis Cipher and typed confirmation `RESTORE ENGINE CONFIG BACKUP`.

    ### Enrollment and Tokens
    - `POST /api/agent/enroll/request` (No Authentication) - submit enrollment request.
    - `POST /api/agent/enroll/poll` (No Authentication) - finalize approved enrollment, including recreating a previously purged GUID with a bumped token version after fresh approval.
    - `POST /api/agent/token/refresh` (Refresh Token) - mint new access token; returns `401 device_purged` when a GUID is blocked by a purge barrier.

    ### Devices and Inventory
    - `POST /api/agent/heartbeat` (Device Authenticated) - heartbeat, metrics, and Agent Metadata Field sync.
    - `POST /api/agent/status` (Device Authenticated) - update `devices.last_seen`, upsert the `system:system_heartbeat` startup timeline row in `agent_role_health`, and emit `agent_status_changed` for Device Summary Agent Health refresh.
    - `POST /api/agent/details` (Device Authenticated) - full hardware, inventory, and cached service payload.
    - `POST /api/agent/script/request` (Device Authenticated) - request work or idle signal.
    - `GET /api/agent/software-management/overrides` (Device Authenticated) - file-backed software icon override rules used by the agent `software_management` role during inventory refresh.
    - `GET /api/agent/files/transfers/<transfer_id>/upload-item/<item_id>` (Device Authenticated) - fetch one staged File Management upload item from the Engine.
    - `GET /api/agent/files/transfers/<transfer_id>/status` (Device Authenticated) - fetch one File Management transfer control snapshot so the agent can honor cancellation while streaming or archiving.
    - `POST /api/agent/files/transfers/<transfer_id>/progress` (Device Authenticated) - update Engine-side File Management transfer progress.
    - `POST /api/agent/files/transfers/<transfer_id>/content` (Device Authenticated) - upload a completed File Management download artifact back to the Engine.
    - `POST /api/agent/patches/install-progress` (Device Authenticated) - update latest scheduled Windows patch install progress for the authenticated device/run. Payloads include scheduled job/run IDs, KB/update identity, phase, percent, message, and captured timestamp.
    - `POST /api/agent/vpn/ensure` (Device Authenticated) - persistent WireGuard tunnel bootstrap.
    - `POST /api/agent/vpn/ready` (Device Authenticated) - report active WireGuard tunnel, local service, and firewall readiness for scheduled SSH/WinRM dispatch.
    - `GET /api/agent/metadata/<field_number>` (Device Authenticated) - read one decoded metadata field for local Agent CLI.
    - `GET /api/agents` (Token Authenticated) - list online collectors, with upgraded hosts advertising helper-backed current-user capability on their SYSTEM record via `helper_contexts`.
    - `GET /api/devices` (Token Authenticated) - device summary list, scoped to the operator's assigned sites unless the operator is an admin.
    - `GET /api/devices/search?hostname=<query>` (Token Authenticated) - hostname search matches for the shared header search, scoped to the operator's assigned sites unless the operator is an admin.
    - `GET /api/devices/<guid>` (Token Authenticated) - device summary by GUID, site-scoped for operators.
    - `GET /api/metadata_fields` (Token Authenticated) - list 500 global Agent Metadata Field definitions, default labels, descriptions, and value limits.
    - `PUT /api/metadata_fields/<field_number>` (Admin) - update one global Agent Metadata Field description.
    - `GET /api/devices/<device_id>/metadata_fields` (Token Authenticated) - list all 500 metadata field rows for an in-scope device, including decoded sparse values and modification metadata.
    - `PUT /api/devices/<device_id>/metadata_fields/<field_number>` (Token Authenticated) - update or clear one in-scope device metadata field. Blank value clears the field.
    - `POST /api/devices/<guid>/quarantine` (Admin) - mark a device quarantined, bump its token version, disconnect active WireGuard/VNC runtime state, and block new jobs, VPN, VNC, shell, remote-op, and scheduled transport paths.
    - `POST /api/devices/<guid>/unquarantine` (Admin) - return a quarantined device to active state and bump its token version so stale access tokens cannot continue.
    - `POST /api/devices/<guid>/revoke` (Admin) - mark a device revoked, bump its token version, revoke refresh tokens, and disconnect active WireGuard/VNC runtime state.
    - `POST /api/devices/<guid>/purge` (Admin) - purge a device, revoke stale trust state, remove current-known references, and rewrite scheduled-job targets that referenced the device.
    - `PUT /api/devices/<guid>/agent-release-channel` (Admin) - update the device agent release channel override and optional source branch, persist the target on the device row, and notify the online SYSTEM agent over Socket.IO.
    - `POST /api/devices/agent-maintenance` (Token Authenticated) - queue on-demand Agent updates or Agent branch/channel switches for selected devices. Requests create `agent_maintenance` scheduled-job history and site-worker `agent_maintenance_run` work items; site workers fan out to agents through the internal socket bridge.
    - `GET /api/device/details/<hostname>` (Token Authenticated) - full device details, site-scoped for operators, including normalized session inventory with helper readiness fields.
    - `GET /api/device/services/<hostname>` (Token Authenticated) - cached service inventory for an in-scope device.
    - `POST /api/device/services/<hostname>/action` (Token Authenticated) - start, stop, or restart a named service on an in-scope device.
    - `GET /api/device/processes/<hostname>?max_age_seconds=<seconds>` (Token Authenticated) - return a live process snapshot for an in-scope device, optionally forcing a fresher agent snapshot for live polling.
    - `POST /api/device/processes/<hostname>/terminate` (Token Authenticated) - request process termination on an in-scope device.
    - `POST /api/device/software/<hostname>/refresh` (Token Authenticated) - request an immediate software inventory refresh over the device SYSTEM socket.
    - `POST /api/device/software/<hostname>/icon-override` (Token Authenticated) - persist a hotloaded global software icon override for the selected software row and request a software refresh.
    - `POST /api/device/software/<hostname>/uninstall-override` (Token Authenticated) - persist a hotloaded global software uninstall override for the selected software row.
    - `POST /api/device/software/<hostname>/uninstall-block` (Token Authenticated) - persist a hotloaded global uninstall blocklist rule for the selected software row.
    - `POST /api/device/software/<hostname>/uninstall-unblock` (Token Authenticated) - remove matching hotloaded global uninstall blocklist rules for the selected software row.
    - `POST /api/device/software/<hostname>/uninstall` (Token Authenticated) - queue a silent uninstall quick job for a supported installed-software row on an in-scope Windows device.
    - `GET /api/patches/audit` (Token Authenticated) - list normalized patch inventory for all devices visible to the operator, including `active_install_job` when an enabled scheduled patch install already owns a matching KB, patch key, or title. Pending rows can include `patch_policy_*` source fields so Patch List can show the effective policy hierarchy and the specific policy layer whose rule approves each install candidate.
    - `GET /api/patches/policies` (Token Authenticated) - list patch policies, optionally filtered by `type=global`, `type=site`, or `type=device_filter`. Rows include `eligible / raw Devices Match Policy Type` count labels, `target_sites` resolved from eligible devices for Device Filter policies, plus `pending_update_count`, compatibility `pending_update_device_count`, and `pending_update_breakdown` entries with per-source `count` and `device_count` values for approved, deferral-ready install candidates scoped to that effective policy row and grouped by approving rule source layer.
    - `POST /api/patches/policies` (Admin) - create a site or device filter patch policy. Windows policy `role_scope` must be `Server` or `Workstation`; global policies are seeded by Borealis and cannot be created manually. Hostname-based device exclusions require `site_id` unless `device_guid` is present.
    - `GET /api/patches/policies/metadata` (Token Authenticated) - policy editor metadata for sites, device filters, Windows role scopes, rule types, match types, exclusions, and defaults. Match types include `title_contains` for case-insensitive substring checks against update titles.
    - `POST /api/patches/policies/evaluate` (Admin) - run policy evaluation for all due policies or a single `policy_id`. Manual UI use is labeled `Run Updates Now` because matching approved updates create immediate patch-install jobs.
    - `GET /api/patches/policies/effective?hostname=<hostname>` (Token Authenticated) - return effective same-role policy hierarchy for one typed Windows device, including inherited exclusion source and override source metadata.
    - `POST /api/patches/policies/conflicts` (Token Authenticated) - preview policy coverage, role-filtered target counts, and conflict state for a draft policy payload.
    - `GET /api/patches/policies/<policy_id>` (Token Authenticated) - get one visible patch policy.
    - `PUT /api/patches/policies/<policy_id>` (Admin) - update a patch policy. Same-layer conflicts return `policy_conflict`; parent block overrides require confirmation. Hostname-based device exclusions require `site_id` unless `device_guid` is present. Global policy role cannot be changed.
    - `DELETE /api/patches/policies/<policy_id>` (Admin) - delete one unlocked patch policy. Global Patch Policies are locked.
    - `POST /api/patches/policies/<policy_id>/preview` (Token Authenticated) - preview targets, role-filtered target/exclusion row counts, dynamic filter conflicts, and parent override warnings for a saved policy.
    - `GET /api/device/patches/<hostname>` (Token Authenticated) - list normalized patch inventory for one in-scope device.
    - `POST /api/device/patches/<hostname>/refresh` (Token Authenticated) - queue an immediate patch inventory refresh over the device SYSTEM socket.
    - `POST /api/device/update-agent/<hostname>` (Token Authenticated) - ask an in-scope device to start its local AutoUpdater task immediately.
    - `GET /api/device/files/<hostname>/roots` (Token Authenticated) - load the Device Summary `File Management` roots view for an in-scope device.
    - `GET /api/device/files/<hostname>/children?path=<absolute-path>` (Token Authenticated) - list one remote directory for an in-scope device.
    - `POST /api/device/files/<hostname>/upload/conflicts` (Token Authenticated) - preflight upload name conflicts in one remote directory for an in-scope device.
    - `GET /api/device/files/<hostname>/text?path=<absolute-path>` (Token Authenticated) - read one lightweight-editable remote text file for the File Management editor.
    - `POST /api/device/files/<hostname>/text` (Token Authenticated) - save one lightweight-editable remote text file back in place on an in-scope device.
    - `POST /api/device/files/<hostname>/mkdir` (Token Authenticated) - create a remote directory on an in-scope device.
    - `POST /api/device/files/<hostname>/rename` (Token Authenticated) - rename one remote file-system item on an in-scope device.
    - `POST /api/device/files/<hostname>/move` (Token Authenticated) - move remote file-system items on an in-scope device.
    - `POST /api/device/files/<hostname>/paste` (Token Authenticated) - paste copied or cut remote file-system items into a destination directory on an in-scope device.
    - `POST /api/device/files/<hostname>/delete` (Token Authenticated) - delete remote file-system items on an in-scope device.
    - `POST /api/device/files/<hostname>/upload` (Token Authenticated) - stage browser-uploaded files or folder manifests for transfer to an in-scope device.
    - `POST /api/device/files/<hostname>/download` (Token Authenticated) - start a remote file download transfer from an in-scope device.
    - `GET /api/device/files/<hostname>/transfer/<transfer_id>/status` (Token Authenticated) - poll a File Management transfer snapshot.
    - `POST /api/device/files/<hostname>/transfer/<transfer_id>/cancel` (Token Authenticated) - request cancellation for an in-progress File Management transfer.
    - `GET /api/device/files/<hostname>/transfer/<transfer_id>/content` (Token Authenticated) - download a completed File Management transfer artifact through the Engine API from site-worker transfer storage.
    - `GET /api/device/registry/<hostname>/roots` (Token Authenticated) - load the Device Summary `Registry` roots view for an in-scope Windows device.
    - `GET /api/device/registry/<hostname>/children?path=<registry-path>` (Token Authenticated) - list subkeys and values for one registry key on an in-scope Windows device.
    - `POST /api/device/registry/<hostname>/key/create` (Token Authenticated) - create a registry subkey on an in-scope Windows device.
    - `POST /api/device/registry/<hostname>/key/rename` (Token Authenticated) - rename a registry key on an in-scope Windows device.
    - `POST /api/device/registry/<hostname>/key/delete` (Token Authenticated) - delete a registry key on an in-scope Windows device.
    - `POST /api/device/registry/<hostname>/value/create` (Token Authenticated) - create a registry value on an in-scope Windows device.
    - `POST /api/device/registry/<hostname>/value/update` (Token Authenticated) - update a registry value on an in-scope Windows device.
    - `POST /api/device/registry/<hostname>/value/delete` (Token Authenticated) - delete a registry value on an in-scope Windows device.
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
    - `POST /api/sites/<site_id>/auto-approval` (Admin) - set or clear temporary site-level enrollment auto-approval.
    - `GET /api/repo/current_hash` (Device or Token Authenticated) - current agent repo hash for optional `repo`, `branch`, and `ttl` query parameters; feature branch refs with slashes are supported.
    - `GET /api/agent/hash` (Device Authenticated) - get agent hash.
    - `POST /api/agent/hash` (Device Authenticated) - update agent hash.
    - `GET /api/agent/hash_list` (Admin + Loopback) - list agent hashes (local diagnostics).

    ### Approvals and Install Codes
    - `GET /api/admin/enrollment-codes` (Admin) - list static site enrollment codes.
    - `POST /api/admin/enrollment-codes` (Admin) - deprecated (returns 410; use site APIs).
    - `DELETE /api/admin/enrollment-codes/<code_id>` (Admin) - deprecated (returns 410; use site APIs).
    - `GET /api/admin/device-approvals` (Token Authenticated) - approval queue, scoped to the current operator's assigned sites unless the operator is an admin. Admins can use `status=wrong_code` to list recent agents submitting invalid enrollment codes.
    - `POST /api/admin/device-approvals/<approval_id>/approve` (Token Authenticated) - approve an in-scope device enrollment.
    - `POST /api/admin/device-approvals/<approval_id>/deny` (Token Authenticated) - deny an in-scope device enrollment.

    ### Device Filters
    - `GET /api/device_filters` (Token Authenticated) - list filters.
    - `GET /api/device_filters/metadata` (Token Authenticated) - filter field/operator metadata, including the searchable `Metadata Field` picker definitions.
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
    - `POST /api/scripts/quick_run` (Token Authenticated) - quick agent-side script job (`powershell`, `batch`, or `bash`, depending on the target agent platform/runtime) for in-scope devices only; current-user runs may also specify `session_target` (`all_active_sessions` or `specific_session`) plus `target_session_id`.
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
    - `POST /api/scheduled_jobs/<int:job_id>/rerun` (Token Authenticated) - queue a fresh immediate occurrence for an enabled scheduled job.
    - `DELETE /api/scheduled_jobs/<int:job_id>` (Token Authenticated) - delete scheduled job.
    - `GET /api/scheduled_jobs/<int:job_id>/runs` (Token Authenticated) - run history.
    - `GET /api/scheduled_jobs/<int:job_id>/devices` (Token Authenticated) - device results. Scheduled patch install rows include `patch_progress` and may include `display_status_label` while keeping canonical `job_status` unchanged.
    - `DELETE /api/scheduled_jobs/<int:job_id>/runs` (Token Authenticated) - clear run history.
    - `job_kind = onboarding` on scheduled-job create/update creates an automatic local-network onboarding job. Payloads use a `device_onboarding` component and an `onboarding_scope` target. The component accepts `agent_platform` (`linux` or `windows`), `install_branch`, `ssh_port`, `windows_port`, `winrm_port`, optional `onboarding_methods` (`smb_scm`, `scheduled_task`, `wmi_dcom`, `winrm`), and optional `onboarding_concurrency` (default `5`). The target accepts `entries` for discovery scope and optional `exclusions` for IP/FQDN/CIDR/range blacklist entries.
    - `job_kind = patch_install` on scheduled-job create/update creates a Windows patch install job. Payloads use one `patch_install` component with `patch_key`, optional `kb`, `title`, `source`, `classification`, `severity`, and `metadata`; targets are normal scheduled-job device/filter targets scoped to the operator. Execution uses system context and stores per-device result/output in Scheduled Job history.
    - `POST /api/onboarding/jobs/<int:job_id>/redeploy` (Token Authenticated) - delete prior run history for one onboarding job and dispatch a fresh immediate onboarding occurrence.
    - `GET /api/onboarding/jobs/<int:job_id>/targets` (Token Authenticated) - per-target onboarding status, SSH port, approval reference, approval id, current approval status when available, and a persistent `timeline`/`events` array of sanitized task events with status, task, start/finish timestamps, and stdout/stderr snippets.
    - Internal job-scheduler endpoints under `/api/internal/job-scheduler/*` are HMAC-authenticated with the Engine secret and are not public operator APIs. They let workers fetch decrypted credentials at execution time, fetch the Engine public base URL, ask `api-backend` to emit host-service events over existing agent sockets, start workflow runs, and bridge WireGuard session lookup/preparation for scheduled Ansible dispatch.

    ### Notifications
    - `POST /api/notifications/notify` (Token Authenticated) - broadcast toast notification.

    ### VPN and Remote Access
    - `POST /api/tunnel/connect` (Token Authenticated) - ensure WireGuard tunnel material for an in-scope agent.
    - `GET /api/tunnel/status` (Token Authenticated) - tunnel status by in-scope agent.
    - `GET /api/tunnel/active` (Token Authenticated) - list active tunnels visible in the current operator's site scope.

    ### VNC
    - `POST /api/agent/vnc/ensure` (Device Authenticated) - ensure always-on VNC tunnel/readiness state and return listener/session metadata for the agent without caching or echoing the VNC password.
    - `GET /api/vnc/viewers` (Token Authenticated) - report Apache Guacamole VNC availability.
    - `POST /api/vnc/establish` (Token Authenticated) - establish or join an Apache Guacamole VNC collaboration session for an in-scope device. Optional `viewer` accepts `guacamole` and defaults to `guacamole`.
    - `POST /api/vnc/disconnect` (Token Authenticated) - leave or close a VNC collaboration session for an in-scope device.
    - `POST /api/vnc/handoff` (Token Authenticated) - reassign session-owner metadata inside an active shared VNC collaboration session.
    - `GET /api/vnc/sessions` (Token Authenticated) - list active VNC collaboration sessions visible within the current operator's site scope.
    - `POST /api/vnc/session` (Token Authenticated) - legacy alias for establish.

    ### Remote Shell
    - `POST /api/shell/establish` (Token Authenticated) - establish remote shell session for an in-scope device.
    - `POST /api/shell/disconnect` (Token Authenticated) - disconnect remote shell session for an in-scope device.

    ### Server Info and Logs
    - `GET /api/server/time` (Operator Session) - server clock.
    - `GET /api/server/overview` (Admin) - consolidated Engine host overview used by the Server Info dashboard, including Compose-backed service state in container mode, public cert status, live operator sessions, WireGuard runtime state, Aegis state, and host resource basics.
    - `GET /api/server/workers` (Admin) - active and recent `job-scheduler` site-worker state, all site names plus total/online device counts, recent assigned work, short Docker container IDs, normalized Docker stats, and optional Docker inspect size metadata when Docker metadata is available.
    - `GET /api/server/site-worker-settings` (Admin) - read the profile-managed site-worker scheduled-lane task concurrency limit.
    - `GET /api/server/agent-release-channels` (Admin) - read Agent update channel targets.
    - `PUT /api/server/agent-release-channels` (Admin) - update default Agent channel or GitHub repo, then refresh cached update artifacts.
    - `POST /api/server/agent-release-channels/refresh` (Admin) - refresh Agent update channel metadata and cached artifacts.
    - `POST /api/server/services/<service_key>/action` (Admin) - queue a detached container service action through `job-scheduler` and `Engine.sh --service`. Supported container actions are `docker-proxy restart`, `api-backend restart`, `job-scheduler restart`, `webui-frontend rebuild prod|dev`, `traefik-edge reload`, `postgres-db restart`, `remote-desktop-guacd restart`, and `wireguard-tunnel reconcile`.
    - `POST /api/server/services/<service_key>/restart` (Admin) - queue a detached `systemd-run` restart for `borealis_engine`, `borealis_traefik`, or a `postgresql_cluster` instance on non-container/systemd installs. Container service operations use `Engine.sh --service ...`.
    - `POST /api/server/wireguard/recover` (Admin) - queue a WireGuard tunnel reconcile when active VPN sessions exist.
    - `GET /api/server/logs` (Admin) - list logs and retention.
    - `GET /api/server/logs/<log_name>/entries` (Admin) - tail log lines.
    - `PUT /api/server/logs/retention` (Admin) - update retention policies.
    - `DELETE /api/server/logs/<log_name>` (Admin) - delete log file(s).

    ### Related documentation

    - [Engine Runtime](../Core%20Runtimes/engine-runtime.md)
    - [Database Reference](db-reference.md)
    - [Device Auditing](../../Using%20the%20Platform/device-auditing.md)
    - [Watchdogs](../../Using%20the%20Platform/watchdogs.md)
    - [Alerts](../../Using%20the%20Platform/alerts.md)
    - [Assemblies](../../Using%20the%20Platform/Assemblies/assemblies.md)
    - [Scheduled Jobs](../../Using%20the%20Platform/scheduled-jobs.md)
    - [Remote Shell](../../Using%20the%20Platform/remote-shell.md)
    - [Software Icon Overrides](../software-icon-overrides.md)
    - [Software Uninstall Overrides](../software-uninstall-overrides.md)
    - [Software Uninstall Blocklist](../software-uninstall-blocklist.md)

    ### Where endpoints are defined
    - Each API module begins with a header listing endpoints.
    - Search under `Data/Engine/Containers/api-backend/data/services/API/` to find the authoritative source.
    - The registry lives in `Data/Engine/Containers/api-backend/data/services/API/__init__.py`.

    ### How to keep this doc accurate
    - When you add or remove a route, update:
      1) The module header comment in the source file.
      2) This `api-reference.md` page.
      3) The domain page (example: `device-auditing.md`).

    ### Quick discovery workflow
    - Use `rg "# - (GET|POST|PUT|DELETE)" Data/Engine/Containers/api-backend/data/services/API` to list endpoints.
    - Cross-check auth requirements in each module (RequestAuthContext, session checks, or device auth decorators).
    - If a route is Socket.IO only, document it in the relevant domain page instead of this REST list.

    ### Auth labels used in this doc
    - No Authentication: open endpoints (rare).
    - Token Authenticated: operator session or bearer token.
    - Device Authenticated: agent JWT access token.
    - Admin: operator must have Admin role.

    ### Example update scenario
    - You add `POST /api/devices/retire`:
      - Update `Data/Engine/Containers/api-backend/data/services/API/devices/management.py` header.
      - Add the endpoint under the Devices and Inventory section here.
      - Update `device-auditing.md` with behavior and UI impact.
