# Technical Debt
[Back to Docs Index](index.md) | [Index (HTML)](index.html)

## Purpose
Track technical debt items that affect developer experience, runtime behavior, or maintainability. This document is the staging area for items that will later become GitHub Issues.

## How To Use This Doc
- Add new entries at the top of the Issues section.
- Use the template below so future agents can understand impact, mitigation, and removal criteria.
- Keep entries concise but action oriented. Link to files and logs instead of pasting large outputs.
- When you open a GitHub Issue, note the issue URL and mark the entry as migrated.

## Logging Rules
- Log any patchy or non-standard workaround, even if it is small.
- Log any behavior that works in production but fails in dev (or vice versa).
- Log any changes that are likely to be removed once upstream or platform fixes land.
- If a change touches dependencies, include the exact version and why it matters.
- Use ASCII only unless the file already contains Unicode.

## Entry Template
```
ID: TD-YYYYMMDD-##
Status: active | mitigated | migrated | resolved
Owner: <name or team>
Date Added: YYYY-MM-DD
Summary: <one sentence>
Impact: <what breaks and who is affected>
Root Cause: <why it happens>
Current Mitigation: <what we did>
Removal Criteria: <what must be true to remove the workaround>
Files: <paths>
Evidence: <logs, errors, or reproduction hints>
Next Step: <smallest useful follow-up>
GitHub Issue: <link or "not yet">
```

## Issues
ID: TD-20260319-04
Status: active
Owner: Engine
Date Added: 2026-03-19
Summary: Engine-side Ansible SSH credentials do not yet support passphrase-protected private keys.
Impact: Operators can save SSH key passphrases in Access Management, but shared Ansible SSH runs cannot use those encrypted key files directly and must fall back to password authentication or an unencrypted test key.
Root Cause: Borealis currently stages SSH credentials as `ansible_ssh_private_key_file`, and the Ansible SSH connection plugin does not apply `private_key_passphrase` to file-based keys.
Current Mitigation: `Data/Engine/services/API/scheduled_jobs/job_scheduler.py` normalizes staged SSH key files to LF line endings, rejects passphrase-protected key-only credentials with a clear run error, and falls back to password auth when both password and passphrase-protected key material are present.
Removal Criteria: Borealis supports agentless SSH key passphrases end to end, either through an ssh-agent-backed Ansible launch path or a supported inline-key transport.
Files: `Data/Engine/services/API/scheduled_jobs/job_scheduler.py`, `Data/Engine/services/API/access_management/credentials.py`
Evidence: Job `23` failed with `Load key ".../id_borealis_ssh": error in libcrypto`, and local repro confirmed that CRLF-staged OpenSSH private keys trigger the same libcrypto parse failure on Linux.
Next Step: Decide whether Borealis should add ssh-agent orchestration for shared Ansible runs or continue requiring password / unencrypted test keys for v1 SSH automation.
GitHub Issue: not yet

ID: TD-20260319-03
Status: active
Owner: Engine
Date Added: 2026-03-19
Summary: Shared Ansible remote targeting still relies on a bounded WireGuard readiness window before SSH/WinRM execution.
Impact: If Borealis preflights too soon after re-priming an agent tunnel, healthy remote devices can be skipped as `No Devices Targeted` even though the WireGuard path becomes reachable a few seconds later.
Root Cause: Tunnel session reuse and agent-side `vpn_agent_ensure_response` completion are asynchronous, but the scheduler previously waited only one second before deciding whether remote targets were reachable.
Current Mitigation: `Data/Engine/services/API/scheduled_jobs/management.py` now bootstraps missing shared-run WireGuard sessions using `BOREALIS_AGENT_PUBLIC_BASE_URL` or `BOREALIS_PUBLIC_BASE_URL` when available, otherwise it reuses the endpoint host from any active session, then spends up to `10.0` seconds by default polling for requested-session presence plus healthy listener state, configurable through `BOREALIS_SHARED_ANSIBLE_VPN_PREP_WAIT_SECONDS` and `BOREALIS_SHARED_ANSIBLE_VPN_PREP_POLL_INTERVAL_SECONDS`, before `Data/Engine/services/API/scheduled_jobs/job_scheduler.py` gives freshly prepared SSH/WinRM targets `5` bounded port-preflight attempts with a default `1.0` second retry delay, remote SSH runs `30` Ansible SSH retries by default, and a per-attempt `ansible_ssh_timeout` of `5` seconds by default, configurable through `BOREALIS_SHARED_ANSIBLE_SSH_RETRIES` and `BOREALIS_SHARED_ANSIBLE_SSH_TIMEOUT_SECONDS`. `Data/Engine/services/ansible/runner.py` also enforces a shared-run subprocess timeout of `900` seconds by default through `BOREALIS_SHARED_ANSIBLE_RUN_TIMEOUT_SECONDS` so unreachable targets cannot leave a playbook stuck in `Running` indefinitely, and it isolates SSH trust state to a per-run `ssh_known_hosts` file so stale `/root/.ssh/known_hosts` entries tied to reused WireGuard IPs do not block otherwise healthy targets.
Removal Criteria: Borealis has an explicit tunnel-readiness signal or a scheduler-side readiness poll that removes the need for a fixed sleep-based warm-up window.
Files: `Data/Engine/services/API/scheduled_jobs/management.py`, `Data/Engine/services/API/scheduled_jobs/job_scheduler.py`
Evidence: `Engine/Logs/scheduled_jobs.log` recorded `ssh_unreachable_preflight` for `lab-docs-01` at `2026-03-19 08:44:03 UTC`, while `Engine/Logs/engine.log` did not record `vpn_agent_ensure_response` for that same agent until `2026-03-19 08:44:08 UTC`.
Next Step: Replace the bounded readiness poll with an explicit per-target tunnel-ready signal or peer-handshake visibility from the VPN service.
GitHub Issue: not yet

ID: TD-20260319-02
Status: active
Owner: Engine
Date Added: 2026-03-19
Summary: Device rows do not consistently persist authoritative remote transport metadata.
Impact: Shared Ansible targeting now follows the operator-selected execution context, so missing device transport metadata no longer blocks runs, but Borealis still lacks reliable per-device transport facts for preflight warnings and richer validation.
Root Cause: Agent/device detail payloads do not consistently populate `summary.connection_type`, so the Engine inventory can contain active WireGuard-managed devices with empty `devices.connection_type`.
Current Mitigation: `Data/Engine/services/API/scheduled_jobs/job_scheduler.py` treats the scheduled job execution context as authoritative for shared Ansible transport selection and logs per-target skip reasons when a run resolves no eligible devices.
Removal Criteria: Borealis persists authoritative per-device transport metadata and can surface advisory mismatch warnings without relying on missing or stale device summary fields.
Files: `Data/Engine/services/API/scheduled_jobs/job_scheduler.py`, `Data/Engine/services/API/devices/management.py`, `Data/Agent/agent.py`
Evidence: Remote SSH playbooks against Ubuntu and Rocky Linux devices were skipped as `No Devices Targeted` even while those agents had active WireGuard sessions, because the eligibility gate previously depended on `devices.connection_type`.
Next Step: Update the agent/device detail pipeline to send and persist explicit remote transport metadata, then use it for UI/advisory validation instead of hard scheduler gating.
GitHub Issue: not yet

ID: TD-20260319-01
Status: active
Owner: Engine
Date Added: 2026-03-19
Summary: Credential secrets are currently stored as raw UTF-8 blobs inside `credentials.*_encrypted` columns.
Impact: Scheduler and Access Management can now create and use SSH/WinRM credentials, but those secret fields are not actually encrypted at rest despite the schema naming.
Root Cause: Borealis does not yet have a dedicated Engine secret-management layer or envelope-encryption utility for credential storage.
Current Mitigation: `Data/Engine/services/API/access_management/credentials.py` writes secrets as UTF-8 blobs and `Data/Engine/services/API/scheduled_jobs/job_scheduler.py` decodes them directly for Engine-side Ansible execution.
Removal Criteria: Credential CRUD and scheduler execution both use a real secret-encryption service with key management, rotation, and explicit decrypt-on-use behavior.
Files: `Data/Engine/services/API/access_management/credentials.py`, `Data/Engine/services/API/scheduled_jobs/job_scheduler.py`, `Docs/db-reference.md`
Evidence: The `credentials` schema uses `*_encrypted` BLOB columns, but no encryption helper exists elsewhere in `Data/Engine` for these fields.
Next Step: Introduce a Borealis Engine secret-storage abstraction, migrate existing credential rows, and remove direct UTF-8 blob decoding from the scheduler.
GitHub Issue: not yet

ID: TD-20260315-01
Status: active
Owner: Engine
Date Added: 2026-03-15
Summary: The new Engine DB layer still wraps SQLite for tests and migration-source reads while production is being moved to PostgreSQL.
Impact: Production and deployment paths are PostgreSQL-only, but some local/unit-test flows can still succeed against SQLite-shaped wrappers and may not catch PostgreSQL-only behavior differences.
Root Cause: The codebase still has broad sqlite3-shaped cursor usage, and the local test harness has not been fully converted to real PostgreSQL integration coverage yet.
Current Mitigation: `Data/Engine/db/dbapi.py` routes PostgreSQL runtime connections through SQLAlchemy/psycopg while still allowing SQLite-backed wrappers for unit tests.
Removal Criteria: Test harnesses run against real PostgreSQL instances end to end and the SQLite compatibility branch is no longer needed.
Files: `Data/Engine/db/core.py`, `Data/Engine/db/dbapi.py`, `Data/Engine/Unit_Tests/conftest.py`
Evidence: The workspace currently lacks `sqlalchemy`, `psycopg`, `flask`, and `pytest`, and no local `psql`/`postgres` binaries are present, so PostgreSQL runtime verification cannot happen here without extra bootstrap.
Next Step: Add a real PostgreSQL-backed test harness in CI/local dev and then delete the SQLite runtime/test compatibility path.
GitHub Issue: not yet

ID: TD-20260218-01
Status: active
Owner: Agent + WebUI
Date Added: 2026-02-18
Summary: Device model and serial are temporarily stored inside the persisted `cpu` JSON payload.
Impact: System identity fields are available in the UI, but CPU payload semantics now include non-CPU keys.
Root Cause: The Engine `devices` schema/API persistence path only keeps a subset of summary fields and drops manufacturer/model/serial values from agent details.
Current Mitigation: `role_DeviceAudit.py` writes system identity fields to summary and mirrors them into `cpu` (`system_model`, `system_serial_number`, etc.); `Device_Details.jsx` reads those keys as fallback.
Removal Criteria: Engine persistence and APIs store/return dedicated system identity fields (or full summary JSON) without relying on CPU metadata.
Files: `Data/Agent/Roles/role_DeviceAudit.py`, `Data/Engine/web-interface/src/Devices/Device_Details.jsx`
Evidence: `Data/Engine/services/API/devices/management.py` `_extract_device_columns` and `get_device_details` only expose selected summary columns.
Next Step: Add first-class model/serial fields to the Engine device schema and API payload builders, then remove CPU fallback keys from agent/UI.
GitHub Issue: not yet

ID: TD-20260210-01
Status: mitigated
Owner: WebUI
Date Added: 2026-02-10
Summary: Vite dev server fails to serve the WebUI when `@novnc/novnc` 1.6.0 is installed.
Impact: Dev UI loads blank or fails to render; production UI continues to work.
Root Cause: noVNC 1.6.0 ships a CommonJS build with a top level `await` in `lib/util/browser.js`; esbuild refuses this during Vite dev prebundle.
Current Mitigation: Postinstall patch replaces the top level `await` with async initialization so esbuild can prebundle.
Removal Criteria: noVNC ships a CJS build without top level await or provides a stable ESM entry that Vite can use without patching.
Files: `Data/Engine/web-interface/package.json`, `Data/Engine/web-interface/scripts/patch-novnc.js`
Evidence: `Engine/Logs/vite-dev.stderr.log` errors referencing top level await in `@novnc/novnc/lib/util/browser.js`.
Next Step: Track upstream noVNC packaging changes and remove patch when safe.
GitHub Issue: not yet

## Related Documentation
- [Engine Runtime](engine-runtime.md)
- [UI and Notifications](ui-and-notifications.md)

## Related Documentation
- [Engine Runtime](engine-runtime.md)
- [UI and Notifications](ui-and-notifications.md)
