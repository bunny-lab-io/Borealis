# Prompt Instructions for Assembly Database Migration & Implementation
1. Open and read `Data/Engine/Assemblies/DB_MIGRATION_TRACKER.md`.
2. Work through the migration tracker one stage at a time. For the current stage:
   • Read the entire task list plus the associated “Details” subsection before changing any code.
   • Implement only the current stage’s requirements before asking for the operator to sync a commit to github before moving on.  Do not do this yourself.
3. As soon as you complete any checkbox item (even partway through your coding), immediately update the markdown file to mark that checkbox as checked (`[x]`). Do this incrementally while you code, not just at the end.
4. Repeat the same read/implement/check-off cycle for each subsequent stage until the file is fully completed.
5. Keep all stage notes synchronized with the implementation as you go.


## 1. Implement multi-database assembly persistence with payload indirection
[ ] Define three SQLite databases (`official.db`, `community.db`, `user_created.db`) stored under `Data/Engine/Assemblies/` and mirrored to `/Engine/Assemblies/` at runtime.
[ ] Standardize a shared schema (per your column list) and enable WAL + shared-cache on all connections.
[ ] Add payload GUID indirection so large scripts/workflows/binaries live under `Data/Engine/Assemblies/Payloads/<GUID>` and `/Engine/Assemblies/Payloads/<GUID>`; the DB stores GUID references, not raw base64.
[ ] Build a startup loader that opens each database, validates schema, and hydrates an in-memory cache keyed by `assembly_id` with metadata, payload handles, and source domain.
[ ] Implement a write queue service that stages mutations in memory, marks cache entries as “dirty,” persists them on a 60-second cadence (configurable), and handles graceful shutdown flushing.
[ ] Expose cache state so callers can detect queued writes vs. persisted rows.
### Details
```
1. Under `Data/Engine/engine/assemblies/`, add a new package (e.g., `persistence/`) containing:

   * `databases.py` for connection management (WAL, pragmas, attach logic).
   * `models.py` defining dataclasses for assemblies/payload metadata.
   * `payloads.py` handling GUID generation, filesystem read/write, and mirroring between staging/runtime paths.
2. Write a startup hook in `Data/Engine/server.py` (or appropriate service bootstrap) that:

   * Ensures the three `.db` files exist (creating `user_created.db`, `community.db` if missing).
   * Runs migrations (idempotent CREATE TABLE IF NOT EXISTS).
   * Loads all assemblies into an in-memory cache structure (`AssemblyCache` singleton) with domain tagging.
3. Implement `AssemblyCache` with:

   * Read APIs returning cached copies.
   * Mutation methods that enqueue write operations into an internal async queue.
   * Background worker that flushes dirty entries every 60s or on-demand; allow config overrides.
   * Graceful shutdown flush in existing shutdown sequence.
4. Add payload GUID support:

   * Modify save/update flows to write payload files (`.json` for workflows, raw text for scripts) under `Payloads/`.
   * Update cache entries to track payload GUID + type; adapt runner interfaces to resolve GUID back to content.
5. Include integrity checks/logging: detect missing payload files, log warnings, and surface errors to API callers.
```

## 2. Update Engine services and APIs for multi-domain assemblies
[ ] Refactor existing assembly REST endpoints to read from the cache instead of filesystem JSON.
[ ] Add source metadata (`official`, `community`, `user`) to API responses.
[ ] Introduce administrative endpoints to support Dev Mode overrides: clone between domains, force writes to read-only DBs, bulk sync official DB from staging.
[ ] Provide queue status (dirty vs. persisted) in list/detail responses for UI pill rendering.
### Details
```
1. Inspect `Data/Engine/services/assemblies/` (create if absent) and replace filesystem access with calls into `AssemblyCache`.
2. Extend response serializers to include:

   * `source` field.
   * `is_dirty` (true when queued for persistence).
   * `payload_guid` (for internal tooling; hide from non-admin if required).
3. Implement new endpoints:

   * `POST /assemblies/{id}/clone` with body specifying target domain.
   * `POST /assemblies/dev-mode/switch` to toggle Dev Mode (admin role only).
   * `POST /assemblies/dev-mode/write` to persist staged changes immediately to a chosen domain (bypasses read-only).
   * `POST /assemblies/official/sync` to purge and repopulate official DB from staging copy on engine boot.
4. Add role guards: normal operators can only mutate `user_created`; admin+Dev Mode can modify any domain.
5. Update background boot sequence to call `official_db_sync()` before cache hydration.
6. Adjust error handling to surface concurrency/locking issues with retries and user-friendly messages.
```

## 3. Implement Dev Mode authorization and UX toggles
[ ] Gate privileged writes behind Admin role + Dev Mode toggle.
[ ] Store Dev Mode state server-side (per-user session or short-lived token) to prevent unauthorized access.
[ ] Ensure Dev Mode audit logging for all privileged operations.
### Details
```
1. Extend authentication middleware in `Data/Engine/services/auth/` to include role checks and Dev Mode status.
2. Persist Dev Mode state per operator (e.g., in Redis/in-memory with TTL) and expose REST endpoints to enable/disable it.
3. Record Dev Mode actions in audit logs (`Engine/Logs/engine.log` or dedicated file) with user, time, target domain, and operation.
4. Update error responses for insufficient privileges to guide admins to enable Dev Mode.
```

## 4. Enhance Assembly Editor WebUI
[ ] Add “Source” column to AG Grid with domain filter badges.
[ ] Display yellow “Queued to Write to DB” pill for assemblies whose cache entry is dirty.
[ ] Implement Import/Export dropdown in `Assembly_Editor.jsx`:
    [ ] Export: bundle metadata + payload contents into legacy JSON format for download.
    [ ] Import: parse JSON, populate editor form, and default to saving into `user_created`.
[ ] Add Dev Mode banner/toggles and domain picker when Dev Mode is active; otherwise show read-only warnings.
[ ] Ensure admin-only controls are hidden for non-authorized users.
### Details
```
1. Modify `Data/Engine/web-interface/src/Assemblies/Assembly_List.jsx` (or equivalent) to:

   * Fetch new API fields (`source`, `is_dirty`).
   * Render Source column with badges (Official/Community/User-Created).
   * Show yellow pill when `is_dirty` true.
2. In `Assembly_Editor.jsx`:

   * Add Import/Export dropdown; hook export to new REST endpoint or client-side JSON assembly packaging.
   * On import, validate schema, generate new payload GUID, and stage in editor state.
   * Display Dev Mode status, domain selection dropdown, and warnings when editing read-only domains without Dev Mode.
3. Create reusable components for domain badges/pills to maintain consistent styling.
4. Wire UI actions to new API endpoints (clone, Dev Mode enable/disable, immediate persist).
5. Update i18n/strings files if the UI uses localization.
```

## 5. Support JSON import/export endpoints
[ ] Implement backend utilities to translate between DB model and legacy JSON structure.
[ ] Ensure exports include payload content (decoded) and metadata for compatibility.
[ ] On import, map JSON back into DB schema, creating payload GUIDs and queuing writes.
### Details
```
1. Add `serialization.py` under the assemblies service package to convert between DB models and legacy JSON.
2. Implement `POST /assemblies/import` and `GET /assemblies/{id}/export` endpoints leveraging the serializer.
3. Validate imported JSON (schema, payload size limits) before staging to cache.
4. Unit-test round-trip import/export for scripts and workflows with attached files.
```

## 6. Testing and tooling
[ ] Unit tests for cache, payload storage, Dev Mode permissions, and import/export.
[ ] Integration tests simulating concurrent edits across domains.
[ ] CLI or admin script to run official DB sync and verify counts.
[ ] Update developer documentation with new workflows.
### Details
```
1. Under `Data/Engine/tests/assemblies/`, add:

   * Cache tests covering load, dirty flag, flush scheduling.
   * Permission tests ensuring Dev Mode restrictions.
   * Import/export round-trip tests with payload files.
2. Create an integration test (pytest) mocking concurrent writer/reader scenarios using WAL and ensuring retries succeed.
3. Add a CLI helper (`python Data/Engine/tools/assemblies.py sync-official`) to trigger staging sync manually.
4. Update `Docs/` (e.g., `Docs/assemblies.md`) with:

   * Database layout.
   * Dev Mode usage instructions.
   * Backup guidance (even if future work, note current expectations).
```