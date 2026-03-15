[Back to Docs Index](../index.md) | [Index (HTML)](../index.html)

# Assembly Repository Redesign

## Summary
This document defines the planned redesign for Borealis official assemblies so they no longer live inside the main Borealis platform repository as the primary source of truth.

The new design moves official assembly authoring and publication to a dedicated GitHub repository:
- Repository URL: `https://github.com/bunny-lab-io/Aurora`
- Git URL: `https://github.com/bunny-lab-io/Aurora.git`

Borealis Engine will ingest assemblies from that repository into PostgreSQL, using `assembly_guid` as the canonical identity key. The repository folder structure will exist for human organization only. Borealis itself will flatten assemblies into PostgreSQL tables and will not preserve repository folders as a runtime storage model.

This plan is intended to be decision-complete so a future Codex agent can implement it directly.

## Direct Answer
Yes, a separate repository is the right long-term design.

Reasons:
- It separates assembly lifecycle from platform lifecycle.
- It allows assembly changes to move faster than Borealis Engine releases.
- It makes official assemblies easier to review, version, and maintain.
- It avoids treating the main Borealis codebase as a content dump.

The Borealis repository should still be allowed to carry a bundled snapshot for fresh installs and release packaging, but that snapshot should be generated from the dedicated assembly repository, not manually edited in-place.

## Goals
- Move official assembly source-of-truth out of the main Borealis repository.
- Use human-readable folders and filenames in the assembly repository.
- Keep `assembly_guid` as the canonical identity key for upserts and updates.
- Allow Borealis Engine to recursively crawl the repository contents and ingest assemblies into PostgreSQL.
- Detect changed assemblies efficiently and update only what changed.
- Support single-assembly updates and bulk `Update All` behavior from the Borealis UI.
- Keep PostgreSQL storage flat and normalized by domain rather than mirroring repository folders.
- Ensure export/save/import flows always preserve or generate `assembly_guid`.
- Keep large base64 payloads supported, but recommend staying below `500 MB`.

## Non-Goals
- Do not preserve repository folder hierarchy inside PostgreSQL.
- Do not use `pg_dump` as the primary distribution format for official assemblies.
- Do not make GitHub the storage backend for user-created or community assemblies.
- Do not treat filenames as canonical identity.
- Do not support duplicate official assemblies with different GUIDs but identical functional content.

## Locked Product Decisions

### Source of truth
- Official assemblies are authored in `Aurora`.
- Borealis Engine ingests official assemblies from that repository.
- The main Borealis repository may contain a generated bundled snapshot for release/install convenience, but that snapshot is downstream from `Aurora`.

### Identity
- `assembly_guid` is the canonical assembly identity.
- `assembly_guid` must be present in every official assembly JSON document.
- If an assembly is created or exported from Borealis without a GUID, Borealis must generate one and include it in:
  - the PostgreSQL record
  - the exported JSON payload

### Folder handling
- Repository folders are for human organization only.
- Borealis must crawl folders recursively.
- Borealis must ignore folder hierarchy when storing assemblies in PostgreSQL.
- Borealis may keep a display path or source path for audit/debug, but that path is not the identity key.

### Update behavior
- Official assembly updates overwrite the existing PostgreSQL record when `assembly_guid` matches.
- If `assembly_guid` is new, Borealis inserts a new official assembly row.
- Borealis must never create duplicates for the same `assembly_guid`.

### Hashing
- Change detection should use a canonical content hash computed by Borealis.
- The preferred hash is SHA-256 over a canonicalized JSON document.
- If a hash is stored in the JSON metadata, Borealis should treat it as advisory, not authoritative.
- Borealis should compute its own hash during crawl/import and compare it against stored catalog state.

### Payload size
- Base64-encoded binaries should still be kept below `500 MB`.
- This is both a Borealis practical limit and a GitHub ecosystem safety limit.
- Large payload handling beyond that threshold is out of scope for this redesign.

## Recommended Repository Layout

### Top-level structure
Recommended layout for `Aurora`:

```text
Aurora/
  scripts/
    windows/
    linux/
    general/
  ansible/
    linux/
    windows/
    network/
  workflows/
    automation/
    reporting/
    utility/
```

### Naming
- Filenames should be human-readable.
- Filenames should not be GUID-only.
- Filenames may include a short GUID suffix if desired for collision resistance.

Recommended examples:
- `scripts/windows/unlock-ad-account--7f2b1f2a.json`
- `scripts/general/google-chrome-install--2d3c9b61.json`
- `ansible/linux/package-upgrade--18b9f4c2.json`
- `workflows/utility/value-parser--c2b8d317.json`

### Important rule
- The filename is a locator, not an identity.
- `assembly_guid` is the identity.

## Official Assembly JSON Contract

### Minimum required metadata
Every official assembly JSON document must contain:
- `assembly_guid`
- `display_name` or `name`
- `assembly_type`
- `assembly_subtype`
- `payload`

### Recommended metadata block
Official repository documents should also include:
- `source_repo`
- `source_path`
- `source_version`
- `updated_at`
- optional `content_hash`

Example shape:

```json
{
  "assembly_guid": "A1B2C3D4-E5F6-4789-ABCD-0123456789AB",
  "display_name": "Unlock AD Account [WIN]",
  "summary": "Unlocks a specified Active Directory user account.",
  "assembly_type": "script",
  "assembly_subtype": "powershell",
  "source_repo": "https://github.com/bunny-lab-io/Aurora",
  "source_path": "scripts/windows/unlock-ad-account--7f2b1f2a.json",
  "source_version": "git:<commit-sha>",
  "payload": {
    "assembly_guid": "A1B2C3D4-E5F6-4789-ABCD-0123456789AB",
    "name": "Unlock AD Account [WIN]",
    "type": "powershell",
    "category": "script",
    "script": "<base64-or-plain-content>",
    "script_encoding": "base64",
    "timeout_seconds": 3600,
    "variables": [],
    "files": []
  }
}
```

### Payload rule
- The nested `payload` should also include the same `assembly_guid`.
- Borealis should enforce this during import/export/save.

## Borealis Engine Ingest Model

### High-level flow
1. Borealis Engine clones or updates a local working copy of `Aurora`.
2. Borealis recursively crawls the repository for assembly JSON documents.
3. Borealis parses each JSON document and extracts `assembly_guid`.
4. Borealis canonicalizes the assembly document and computes a SHA-256 content hash.
5. Borealis compares that hash against stored official catalog state in PostgreSQL.
6. Borealis imports only new or changed assemblies.
7. Borealis upserts records into `assemblies.official_assemblies`.
8. Borealis records the latest applied hash, repo URL, source path, and source version.

### Recursive crawl rules
- Crawl every subfolder recursively.
- Ignore non-JSON files.
- Ignore hidden Git metadata.
- Ignore folder structure for PostgreSQL storage purposes.
- Preserve source path only as metadata.

### Local checkout strategy
Recommended runtime strategy:
- Clone to a dedicated Engine-managed cache path such as:
  - `Engine/Cache/Official_Assemblies/`
- Use shallow fetch where practical.
- Record the active commit SHA after each sync.

### Git update strategy
Recommended sync sequence:
1. If checkout does not exist, clone the repo.
2. If checkout exists, fetch latest refs.
3. Reset the working tree to the target branch or ref.
4. Crawl repository contents.
5. Ingest changed assemblies.

This keeps the process deterministic and avoids partial file drift.

## PostgreSQL Storage Model

### Database storage
Borealis continues storing official assemblies in PostgreSQL:
- `assemblies.official_assemblies`

### Flattening rule
- Repository folders do not create nested database structures.
- Each assembly is one row, keyed by `assembly_guid`.
- Optional metadata fields should include:
  - `source_repo`
  - `source_path`
  - `source_version`
  - `content_hash`

### Upsert rule
When ingesting official assemblies:
- If `assembly_guid` exists, overwrite the existing official assembly row.
- If `assembly_guid` does not exist, insert a new row.
- If the content hash is unchanged, skip the write.

## Hashing and Change Detection

### Recommended hash strategy
Use SHA-256 over canonical JSON produced by:
- sorted keys
- normalized line endings
- consistent whitespace removal for hash computation
- exclusion of transient metadata if needed

### Recommended hashed fields
Hash should cover the functional assembly content:
- `assembly_guid`
- `display_name`
- `summary`
- `assembly_type`
- `assembly_subtype`
- `payload`

### Why Borealis should compute the hash itself
- prevents trusting stale metadata in the repo
- avoids self-referential `content_hash` problems
- guarantees consistent update detection regardless of author workflow

## UI Update Behavior

### Per-assembly update
In the Assembly List:
- official assemblies that differ from the current repo snapshot should show `Update`
- clicking `Update` should open a confirmation dialog
- the dialog should link to:
  - `https://github.com/bunny-lab-io/Aurora`

Suggested confirmation text:
- `This will pull down the most recent version of this assembly from Aurora on GitHub and overwrite the current official version in Borealis. Proceed?`

### Bulk update
The Assembly List button rail should include `Update All` as a secondary action when updates are available.

`Update All` should:
- refresh the checkout
- crawl recursively
- detect changed assemblies
- upsert all changed official assemblies
- skip unchanged assemblies

## Save / Export / Import Behavior

### Save
When an assembly is saved in Borealis:
- if `assembly_guid` exists, keep it
- if `assembly_guid` does not exist, generate one
- write the GUID into PostgreSQL
- write the GUID into the nested payload document

### Export JSON
When exporting assembly JSON:
- always include top-level `assembly_guid`
- always include nested payload `assembly_guid`

### Import JSON
When importing JSON:
- if `assembly_guid` exists, use it
- if it does not exist, generate one before save

## GitHub Integration Notes

### Authentication
- Public repository mode should work without a GitHub token.
- If rate limiting or private access becomes relevant later, Borealis can reuse its GitHub token integration.

### Recommended branch model
- Default official sync target should be the main branch.
- Future enhancement: allow pinning to tag, release, or commit SHA for controlled rollouts.

## Operational Guidance

### Release/install model
Recommended long-term model:
- `Aurora` is the authoring repository.
- Borealis release packaging may vendor a generated snapshot of official assemblies for first boot.
- Runtime `Update` and `Update All` actions pull from GitHub on demand.

### Failure handling
If a sync fails:
- keep the current PostgreSQL official assemblies unchanged
- show a clear error in the UI
- log the failing repo URL, ref, and exception

### Logging
Log:
- repo fetch start/end
- commit SHA used
- number of files scanned
- number of assemblies changed
- number skipped
- number failed

## Migration Away from Main Borealis Repo Assemblies

### Required direction
- The main Borealis codebase should stop being the place where official assemblies are manually maintained.
- Official assembly content should live in `Aurora`.
- Borealis should ingest that content into PostgreSQL rather than treating source-controlled local assembly files as the active runtime database.

### Transitional allowance
- A generated bundled snapshot inside Borealis is acceptable for release/install convenience.
- Manual authoring of official assemblies inside the Borealis repo is not the desired end state.

## Implementation Plan

### Phase 1: Repository contract
- Create `Aurora` folder conventions.
- Require `assembly_guid` in every official JSON.
- Add metadata expectations for `source_repo`, `source_path`, and `source_version`.

### Phase 2: Engine checkout and crawl
- Add Engine-managed checkout/fetch logic for `https://github.com/bunny-lab-io/Aurora.git`.
- Recursively crawl `.json` files.
- Parse and validate assembly documents.
- Compute canonical SHA-256 content hashes.

### Phase 3: PostgreSQL catalog state
- Extend official catalog state tracking with:
  - `assembly_guid`
  - `content_hash`
  - `source_repo`
  - `source_path`
  - `source_version`
  - last sync timestamps

### Phase 4: UI update controls
- Show `Update` for changed official assemblies.
- Show `Update All` for bulk official updates.
- Add confirmation dialogs with links to `https://github.com/bunny-lab-io/Aurora`.

### Phase 5: Export/import hardening
- Ensure save/export/import always preserve or generate `assembly_guid`.
- Ensure nested payloads also contain that GUID.

### Phase 6: Optional bundled snapshot
- Allow Borealis releases to ship a generated snapshot copied from `Aurora`.
- Treat that snapshot as a seed, not as the long-term authoring source.

## Open Risks
- Large embedded base64 binaries will make Git diffs and repository storage heavier.
- GitHub API or network failures can block live update checks if not cached properly.
- Authors may accidentally duplicate assemblies by copying JSON and forgetting to regenerate GUIDs.
- A bad official update can overwrite a working assembly unless versioning and review practices are disciplined.

## Recommendation
Implement the redesign with:
- a separate `Aurora` repository
- human-readable folders and filenames
- `assembly_guid` as the canonical identity
- Borealis-computed SHA-256 hashes for change detection
- recursive Git checkout crawl
- PostgreSQL upsert by GUID
- optional bundled snapshot only for install/release convenience

This is the cleanest path away from the main Borealis repo acting as the assembly source of truth while keeping official assemblies easy to review, update, and ingest.
