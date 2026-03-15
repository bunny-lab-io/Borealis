# Assemblies and Quick Jobs
[Back to Docs Index](index.md) | [Index (HTML)](index.html)

## Purpose
Explain Borealis assemblies (script definitions), how they are stored, and how quick jobs execute them.

## Assemblies at a Glance
- Assemblies are script definitions stored in PostgreSQL assembly-domain tables.
- Domains include: `official`, `community`, and `user`.
- Payload JSON is stored inline in each `assemblies` row (`payload_json`).
- Assemblies are cached at runtime by the Engine and served via API.
- `assembly_type` is the canonical classifier used to route assemblies to the Script Editor, Workflow Editor, and execution paths.
- `assembly_subtype` captures implementation/runtime subtype (for example: `powershell`, `workflow`, `ansible`).
- The assembly table does not use `metadata_json` or `payload_type`.
- Engine startup enforces the compact `assemblies` schema exactly; legacy extra columns are rejected instead of auto-migrated.

## Quick Jobs
- Quick jobs are immediate executions of a script assembly.
- The Engine resolves the script, signs it, and emits a Socket.IO `quick_job_run` event.
- Agents execute the payload and return `quick_job_result` for status and output.

## Activity History
- Quick job executions are tracked in `activity_history`.
- Operators can view or delete device activity history via API.

## Payload Size Limits
- Operator target: up to `500 MB` per assembly payload (`payload_json`) under normal resource conditions.
- Borealis import guardrail: `950,000,000` bytes per document.
- SQLite per-value hard ceiling in the current runtime build: `1,000,000,000` bytes for a single TEXT/BLOB value.
- Payloads near the hard ceiling are best-effort and depend on available Engine memory/CPU during JSON parse and DB write.

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
- `POST /api/assemblies/<assembly_guid>/official-update` (Admin) - update one official assembly from the active catalog.
- `POST /api/assemblies/official/update-all` (Admin) - update all official assemblies with available catalog changes.
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
- Bundled official assemblies live under `Data/Engine/Official_Assemblies/` as `manifest.json` plus one JSON file per assembly.
- Runtime assembly data lives in PostgreSQL `assemblies.official_assemblies`, `assemblies.community_assemblies`, and `assemblies.user_created_assemblies`.
- The Engine loads and caches assemblies via `Data/Engine/assembly_management` and `AssemblyRuntimeService`.

### Payload sizing guidance
- Treat `500 MB` as the practical operator-facing target for a single assembly payload.
- Runtime import limit remains `950,000,000` bytes as an application guardrail.

### Dev Mode behavior
- User-created domain writes are allowed for authenticated operators.
- Official/community domains require Admin + Dev Mode enabled.
- Dev Mode state is tracked per session and expires after a TTL.
- Use `/api/assemblies/dev-mode/switch` to toggle and `/api/assemblies/dev-mode/write` to flush.

### Quick job execution path
1) Operator calls `/api/scripts/quick_run` with `script_path` or `assembly_guid`, plus `hostnames`.
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
- Back up PostgreSQL `assemblies.*` tables.
- Back up `Data/Engine/Official_Assemblies/` if you want the bundled official catalog snapshot tracked with releases.

### Known limitations
- Ansible quick-run is not implemented in the Engine runtime.
- Linux agent support is incomplete; PowerShell scripts are Windows-first.

### Touch points to remember
- API routes: `Data/Engine/services/API/assemblies/`.
- Assembly runtime: `Data/Engine/services/assemblies/service.py` and `Data/Engine/assembly_management/`.
- UI editors: `Data/Engine/web-interface/src/Assemblies/`.
