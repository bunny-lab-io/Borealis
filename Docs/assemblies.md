# Assemblies Runtime Reference

## Database Layout
- Three SQLite databases live under `Data/Engine/Assemblies` (`official.db`, `community.db`, `user_created.db`) and mirror to `Engine/Assemblies` at runtime.
- Automatic JSON → SQLite imports for the official domain have been retired; the staged `official.db` now serves as the authoritative store unless you invoke a manual sync.
- Payload binaries/json store under `Payloads/<payload-guid>` in both staging and runtime directories; the AssemblyCache references payload GUIDs instead of embedding large blobs.
- WAL mode with shared-cache is enabled on every connection; queue flushes copy the refreshed `.db`, `-wal`, and `-shm` files into the runtime mirror.
- `AssemblyCache.describe()` reveals dirty/clean state per assembly, helping operators spot pending writes before shutdown or sync operations.

## Dev Mode Controls
- User-created domain mutations remain open to authenticated operators; community/official writes require an administrator with Dev Mode enabled.
- Toggle Dev Mode via `POST /api/assemblies/dev-mode/switch` or the Assemblies admin controls; state expires automatically based on the server-side TTL.
- Privileged actions (create/update/delete, cross-domain clone, queue flush, official sync, import into protected domains) emit audit entries under `Engine/Logs/assemblies.log`.
- When Dev Mode is disabled, API responses return `dev_mode_required` to prompt admins to enable overrides before retrying protected mutations.

## Backup Guidance
- Regularly snapshot `Data/Engine/Assemblies` and `Data/Engine/Assemblies/Payloads` alongside the mirrored runtime copies to preserve both metadata and payload artifacts.
- Include the queue inspection endpoint (`GET /api/assemblies`) in maintenance scripts to verify no dirty entries remain before capturing backups.
- Maintain the staged databases directly; to publish new official assemblies copy the curated `official.db` into `Data/Engine/Assemblies` before restarting the Engine.
- Future automation will extend to scheduled backups and staged restore helpers; until then, ensure filesystem backups capture both SQLite databases and payload directories atomically.
