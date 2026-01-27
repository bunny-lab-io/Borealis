# Assemblies and Quick Jobs
[Back to Docs Index](index.md) | [Index (HTML)](index.html)

## Purpose
Explain Borealis assemblies (script definitions), how they are stored, and how quick jobs execute them.

## Assemblies at a Glance
- Assemblies are script definitions stored in SQLite domains.
- Domains include: `official`, `community`, and `user_created`.
- Payload artifacts live under `Payloads/<payload-guid>`.
- Assemblies are cached at runtime by the Engine and served via API.

## Quick Jobs
- Quick jobs are immediate executions of a script assembly.
- The Engine resolves the script, signs it, and emits a Socket.IO `quick_job_run` event.
- Agents execute the payload and return `quick_job_result` for status and output.

## Activity History
- Quick job executions are tracked in `activity_history`.
- Operators can view or delete device activity history via API.

## Ansible Status (Current)
- Ansible quick-run exists as an endpoint but is not implemented.
- Agent and Engine scaffolding exist but are unstable; treat as disabled.

## API Endpoints
- `GET /api/assemblies` (Token Authenticated) - list assemblies.
- `GET /api/assemblies/<assembly_guid>` (Token Authenticated) - assembly details.
- `POST /api/assemblies` (Token Authenticated) - create assembly.
- `PUT /api/assemblies/<assembly_guid>` (Token Authenticated) - update assembly.
- `DELETE /api/assemblies/<assembly_guid>` (Token Authenticated) - delete assembly.
- `POST /api/assemblies/<assembly_guid>/clone` (Admin + Dev Mode for protected domains) - clone assembly.
- `POST /api/assemblies/dev-mode/switch` (Admin) - toggle dev mode.
- `POST /api/assemblies/dev-mode/write` (Admin + Dev Mode) - flush queued writes.
- `POST /api/assemblies/import` (Domain write permissions) - import legacy JSON.
- `GET /api/assemblies/<assembly_guid>/export` (Token Authenticated) - export legacy JSON.
- `POST /api/scripts/quick_run` (Token Authenticated) - quick PowerShell job.
- `POST /api/ansible/quick_run` (Token Authenticated) - placeholder (not implemented).
- `GET /api/device/activity/<hostname>` (Token Authenticated) - device activity history.
- `DELETE /api/device/activity/<hostname>` (Token Authenticated) - clear history.
- `GET /api/device/activity/job/<int:job_id>` (Token Authenticated) - activity record.

## Related Documentation
- [Flow Editor and Nodes](flow-editor-and-nodes.md)
- [Scheduled Jobs](scheduled-jobs.md)
- [Security and Trust](security-and-trust.md)
- [API Reference](api-reference.md)

## Codex Agent (Detailed)
### Storage layout and caching
- Staging assemblies live under `Data/Engine/Assemblies/`:
  - `official.db`
  - `community.db`
  - `user_created.db`
  - `Payloads/` for large script assets
- Runtime mirror lives under `Engine/Assemblies/` and is refreshed at launch.
- The Engine loads and caches assemblies via `Data/Engine/assembly_management` and `AssemblyRuntimeService`.

### Dev Mode behavior
- User-created domain writes are allowed for authenticated operators.
- Official/community domains require Admin + Dev Mode enabled.
- Dev Mode state is tracked per session and expires after a TTL.
- Use `/api/assemblies/dev-mode/switch` to toggle and `/api/assemblies/dev-mode/write` to flush.

### Quick job execution path
1) Operator calls `/api/scripts/quick_run` with `script_path` and `hostnames`.
2) Engine resolves the assembly document (DB-backed or filesystem).
3) Engine rewrites variable placeholders and signs the script with Ed25519.
4) Engine creates `activity_history` rows and emits `quick_job_run` over Socket.IO.
5) Agent role executes the script (SYSTEM or CURRENTUSER) and returns `quick_job_result`.
6) Engine updates `activity_history` and emits `device_activity_changed`.

### Script variables and environment injection
- Assembly variables are stored with name, type, default, and description.
- Engine builds an environment map and also rewrites `$env:VAR` occurrences.
- Variables are included in the payload so agents can log context.

### Code signing
- Script bytes are signed in `Data/Engine/services/API/assemblies/execution.py`.
- Agents verify signatures using `signature_utils` before execution.

### Activity history
- `activity_history` stores script metadata, timestamps, status, stdout, stderr.
- Use `/api/device/activity/<hostname>` to query or clear entries.

### Backup guidance
- Back up `Data/Engine/Assemblies/` and `Data/Engine/Assemblies/Payloads/` together.
- Also back up `Engine/Assemblies/` if you need runtime snapshots.

### Known limitations
- Ansible quick-run is not implemented in the Engine runtime.
- Linux agent support is incomplete; PowerShell scripts are Windows-first.

### Touch points to remember
- API routes: `Data/Engine/services/API/assemblies/`.
- Assembly runtime: `Data/Engine/services/assemblies/service.py` and `Data/Engine/assembly_management/`.
- UI editors: `Data/Engine/web-interface/src/Assemblies/`.
