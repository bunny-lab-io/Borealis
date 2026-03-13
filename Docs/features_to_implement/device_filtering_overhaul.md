[Back to Docs Index](../index.md) | [Index (HTML)](../index.html)

# Device Filtering Overhaul

## Summary
This document defines the full planned overhaul of Borealis device filtering, including:
- filter data model redesign
- backend matcher redesign
- site scope redesign
- installed software filtering
- filter list and filter editor UI changes
- scheduler integration and run-time target snapshots
- archive/delete safety rules
- device-list handoff from saved filters

Implementation status:
- Backend schema, matcher, filter API, software inventory normalization, scheduler snapshots, filter UI rebuild, device-list handoff, scheduler picker updates, targeted unit coverage, and core docs propagation have been implemented.
- The live filter system now uses one grouped criteria model. Legacy `basic_criteria` payloads are auto-converted into grouped criteria during normalization, and the separate Basic/Advanced mode distinction is deprecated in the UI and runtime.
- Text and software filters now support `Does Not Contain` for absence-based matching.
- Local execution verification is partially blocked in this environment because `pytest` and `npm` are unavailable on-path; Python syntax compilation for the touched backend and test files succeeded.

This plan is intended to be decision-complete so a future Codex agent can implement it directly without re-opening product questions.

## Direct Answer
The current filter system is functional enough to prototype with, but it is not robust enough to support the requested feature set safely.

The current implementation has four major structural problems:
- The filter editor evaluates criteria in the browser while scheduled jobs and match counts use a separate backend matcher.
- Site scoping is inconsistent. The UI allows multiple sites, but the backend persists only a single site value.
- Complex fields such as installed software are treated as raw JSON blobs or stringified objects instead of typed searchable records.
- Filter payloads are loosely shaped and normalized in multiple places, which makes the system fragile and hard to extend.

The required solution is a full-stack rework, not a patch.

## Goals
- Make the backend matcher the single source of truth for all filter evaluation.
- Replace the loose filter payload with a strict typed filter schema.
- Support three explicit site modes:
  - `Global`
  - `Specific Sites`
  - `Global w/ Exclusions`
- Add first-class installed software filtering for Windows and Linux.
- Add a manual preview/apply workflow rather than live matching while typing.
- Use one grouped criteria system for all saved filters and editor interactions.
- Add archive, unarchive, clone, and delete workflows with scheduled-job safety checks.
- Prevent archive and delete when scheduled jobs still reference a filter.
- Add a `View Devices` action that opens the device list with ephemeral filters derived from the selected saved filter.
- Keep device list views separate from automation filters.
- Make recurring jobs resolve filters into a point-in-time target snapshot each time they run.
- Mark runs with zero matched devices as gracefully skipped, with a user-facing label of `No Devices Targeted`.

## Non-Goals
- Do not implement user/group/RBAC filter ownership in this effort.
- Do not implement macOS software collection in this effort.
- Do not implement filter version history.
- Do not merge device list saved views and device filters into one system.
- Do not migrate site assignment from hostname to device GUID in this effort.
- Do not add software publisher/vendor as a first-class filter in the first pass.

## Locked Product Decisions

### Filter visibility and ownership
- Filters remain globally visible to operators for now.
- Future roles and ACL work is expected later under an eventual `Roles` admin section, but that work is deferred.

### Site modes
- Site mode labels must be exactly:
  - `Global`
  - `Specific Sites`
  - `Global w/ Exclusions`
- Site mode choices are mutually exclusive.
- Filter site matching must use robust site identifiers, not site names.
- Site references should be stored by `site_id`.

### Archive and delete behavior
- Archived filters must never be exposed in the scheduled-job target picker.
- Archive and delete must both be blocked when any scheduled job references the filter.
- The operator must be shown which job or jobs reference the filter.
- Those job names must be clickable deep links that open the relevant job editor.

### Clone behavior
- Cloning creates an active, unarchived copy.
- The clone copies scope, description, criteria payloads, and site selection.
- The clone name must be prefixed as `(Clone) <Original Name>`.

### Description
- Filters have a plain-text, single-line description.

### Criteria model
- The separate `Basic` and `Advanced` criteria modes are deprecated.
- The editor exposes one grouped criteria builder.
- Runtime evaluation uses one grouped criteria payload.
- Legacy `basic_criteria` payloads are converted into one grouped `AND` block automatically when filters are normalized or loaded.

### Preview behavior
- The filter editor must use a manual apply model.
- No live matching while the operator types.
- Matching should only occur on explicit preview/apply and at runtime when scheduled jobs or quick jobs resolve targets.

### Regex
- Regex support is available per criterion in the grouped criteria builder.
- Regex is off by default per criterion.
- Default non-regex matching is case-insensitive.
- Regex matching does not inherit the default case-insensitive behavior.
- Backend regex evaluation should use a dependency that supports PCRE-style behavior as closely as practical.

### Negative matching
- Text criteria support `Does Not Contain`.
- Software criteria support `Does Not Contain`.
- A filter can combine positive and negative text matches inside the same condition group.

### Software filtering
- Windows and Linux are in scope.
- macOS is deferred.
- Windows software sources:
  - uninstall registry software
  - AppX / Windows Store apps
- Linux software sources:
  - `dpkg`
  - `rpm`
- Software data should appear in one merged list with a `source` field.
- Software filters should support:
  - software name matching
  - optional source matching
  - optional version matching
  - regex when enabled
- Version comparisons must support:
  - `Matches`
  - `Older Than`
  - `Newer Than`
- For difficult version formats, regex is the operator escape hatch rather than trying to auto-solve every edge case.

### Scheduler behavior
- Each job run resolves filter targets into a point-in-time snapshot at run start.
- Recurring jobs resolve again on the next run.
- If zero devices are matched at runtime:
  - the run must not dispatch
  - the run should be recorded as skipped
  - the UI should display `No Devices Targeted`

### Device list integration
- `View Devices` should open the device list with ephemeral filters only.
- No one-click saved device view should be created.

## Current State Problems

### 1. UI matcher drift
The filter editor currently evaluates criteria in the browser while the scheduler and list counts use the backend matcher.

Relevant files:
- `Data/Engine/web-interface/src/Devices/Filters/Filter_Editor.jsx`
- `Data/Engine/services/filters/matcher.py`
- `Data/Engine/services/API/scheduled_jobs/job_scheduler.py`
- `Data/Engine/services/API/filters/management.py`

Impact:
- preview results can disagree with saved filter behavior
- scheduled jobs may target different devices than the editor preview showed
- software matching is especially unreliable

### 2. Site scope mismatch
The UI currently allows multiple sites, but the backend only persists a single site field.

Relevant files:
- `Data/Engine/web-interface/src/Devices/Filters/Filter_Editor.jsx`
- `Data/Engine/services/API/filters/management.py`
- `Data/Engine/services/filters/matcher.py`

Impact:
- saved filters cannot represent the UI state accurately
- site scope behavior is not trustworthy

### 3. Software is stored for display, not for searching
Installed software is currently collected as JSON blobs and passed through to the UI, but not normalized for matching.

Relevant files:
- `Data/Agent/Roles/role_DeviceAudit.py`
- `Data/Engine/services/API/devices/management.py`
- `Data/Engine/services/filters/matcher.py`
- `Data/Engine/web-interface/src/Devices/Device_Details.jsx`

Impact:
- exact and version-aware software filtering is not robust
- complex matching is reduced to stringifying arrays and objects
- source-aware filtering is not possible yet

### 4. Payload shape drift and duplicated normalization
There are multiple frontend normalizers and alias fallbacks for the same filter data.

Relevant files:
- `Data/Engine/web-interface/src/Devices/Filters/Filter_List.jsx`
- `Data/Engine/web-interface/src/Devices/Filters/Filter_Editor.jsx`
- `Data/Engine/web-interface/src/Scheduling/Create_Job.jsx`

Impact:
- extending the filter schema gets harder over time
- the contract is unstable
- bugs are likely whenever one consumer is updated and another is not

### 5. Filter list error handling is misleading
The current filter list falls back to sample data when the API is unavailable.

Relevant file:
- `Data/Engine/web-interface/src/Devices/Filters/Filter_List.jsx`

Impact:
- operators can be shown fake rows while the system is actually broken
- real API failures are obscured

### 6. Filter endpoints need consistent backend ownership
The devices API uses explicit login enforcement patterns. The filter API should follow the same standard explicitly rather than relying on loose assumptions.

Relevant files:
- `Data/Engine/services/API/devices/management.py`
- `Data/Engine/services/API/filters/management.py`

Impact:
- auth behavior may be inconsistent
- audit fields such as `last_edited_by` should be server-owned, not client-owned

### 7. Dead or partial UI wiring exists
The navigation and breadcrumb system still references a `Groups` page that does not exist as a real implemented feature.

Relevant files:
- `Data/Engine/web-interface/src/App.jsx`
- `Data/Engine/web-interface/src/Navigation_Sidebar.jsx`

Impact:
- unnecessary confusion
- implementation surface looks larger than the real feature set

## Desired End State

### Filter storage
- Filters are saved as strictly typed records.
- Site references are stored by `site_id`.
- Active and archived filters are clearly separated.
- Filters can be cloned, archived, unarchived, and deleted safely.

### Filter evaluation
- The backend matcher is the only evaluator.
- The UI only edits filter payloads and requests preview results.
- Preview, list counts, scheduler resolution, and device-list handoff use the same evaluation logic.

### Software filtering
- Software can be filtered by name.
- Operators can optionally constrain source.
- Operators can optionally constrain version.
- Windows Store vs locally installed software is visible to operators and usable in filters.

### UI
- Filter List has `Active` and `Archived` tabs.
- Filter List has row actions for edit, view devices, clone, archive or unarchive, and delete.
- Filter List surfaces `Jobs Referencing this Filter`.
- Filter Editor supports `Basic` and `Advanced` criteria sub-tabs under `Criteria`.
- Preview only runs on demand.

### Scheduler
- Jobs using filters resolve their targets at the moment the run starts.
- Each run freezes its own target list.
- Empty target resolutions are recorded as skipped with the display label `No Devices Targeted`.

## Data Model

### Table: `device_filters`
Recommended columns:
- `id INTEGER PRIMARY KEY AUTOINCREMENT`
- `name TEXT NOT NULL UNIQUE`
- `description TEXT`
- `archived INTEGER NOT NULL DEFAULT 0`
- `criteria_mode TEXT NOT NULL DEFAULT 'basic'`
- `site_mode TEXT NOT NULL DEFAULT 'global'`
- `basic_criteria_json TEXT`
- `advanced_criteria_json TEXT`
- `last_edited_by TEXT`
- `created_at INTEGER NOT NULL`
- `updated_at INTEGER NOT NULL`

Implementation notes:
- `description` should be trimmed to a single line.
- `criteria_mode` valid values:
  - `basic`
  - `advanced`
- `site_mode` valid values:
  - `global`
  - `specific_sites`
  - `global_exclusions`

### Table: `device_filter_sites`
Recommended columns:
- `filter_id INTEGER NOT NULL`
- `site_id INTEGER NOT NULL`

Recommended constraints:
- unique composite index on `(filter_id, site_id)`
- foreign key on `filter_id -> device_filters(id) ON DELETE CASCADE`
- foreign key on `site_id -> sites(id) ON DELETE CASCADE`

Purpose:
- stores included or excluded site references depending on `device_filters.site_mode`

### Table: `device_software_inventory`
Recommended columns:
- `id INTEGER PRIMARY KEY AUTOINCREMENT`
- `device_guid TEXT NOT NULL`
- `name TEXT NOT NULL`
- `name_normalized TEXT NOT NULL`
- `version TEXT`
- `source TEXT NOT NULL`
- `captured_at INTEGER NOT NULL`
- `metadata_json TEXT`

Recommended indexes:
- `(device_guid)`
- `(name_normalized)`
- `(source)`
- `(device_guid, name_normalized, source)`

Expected `source` values:
- `local_installed`
- `windows_store`
- `dpkg`
- `rpm`

### Table: `scheduled_job_run_targets`
Recommended columns:
- `id INTEGER PRIMARY KEY AUTOINCREMENT`
- `run_id INTEGER NOT NULL`
- `device_guid TEXT`
- `hostname TEXT NOT NULL`
- `site_id INTEGER`
- `resolved_from_filter_id INTEGER`
- `created_at INTEGER NOT NULL`

Recommended indexes:
- `(run_id)`
- `(resolved_from_filter_id)`
- `(hostname)`

Purpose:
- stores the frozen target list used for a specific scheduled run

### Status handling for zero-target runs
Recommended scheduler storage:
- `status = 'skipped'`
- `skip_reason = 'no_devices_targeted'`

UI display:
- render as `No Devices Targeted`

If the existing scheduled run schema does not have a good place for `skip_reason`, add one.

## Site Matching Semantics

### Global
- Matches all devices regardless of site assignment.

### Specific Sites
- Matches devices whose assigned `site_id` is in the filter's selected site set.
- Devices with no site assignment do not match.

### Global w/ Exclusions
- Matches all devices except devices whose assigned `site_id` is in the filter's selected exclusion set.
- Devices with no site assignment still match.

### Important current-system note
The current system still maps one site per device by hostname in `device_sites`.
That underlying site-assignment model is not being redesigned in this effort.
This overhaul should consume the existing assignment model while storing filter site selection by `site_id`.

## Criteria Model

### Core principle
Criteria must be typed.
Do not store a loose list of arbitrary field names and string operators without metadata.

### Metadata endpoint
Add a backend metadata endpoint:
- `GET /api/device_filters/metadata`

Suggested payload sections:
- `criteria_modes`
- `site_modes`
- `fields`
- `operators`
- `software_sources`
- `version_operators`

Purpose:
- the UI should render its form from backend metadata rather than a duplicated hardcoded field catalog

### Basic criteria payload
Recommended shape:

```json
{
  "criteria": [
    {
      "field": "hostname",
      "operator": "contains",
      "value": "server",
      "use_regex": false
    },
    {
      "field": "installed_software",
      "operator": "matches",
      "value": "Google Chrome",
      "software_source": "local_installed",
      "version_operator": "newer_than",
      "version_value": "120.0",
      "use_regex": false
    }
  ]
}
```

Rules:
- criteria are joined by `AND` only
- no grouping
- no negative operators in first pass
- regex is optional per row
- software rows support inline optional sub-controls

### Advanced criteria payload
Recommended shape:

```json
{
  "groups": [
    {
      "join_with": null,
      "conditions": [
        {
          "field": "hostname",
          "operator": "contains",
          "value": "server",
          "use_regex": false
        },
        {
          "field": "operating_system",
          "operator": "equals",
          "value": "Windows 11",
          "use_regex": false
        }
      ]
    },
    {
      "join_with": "OR",
      "conditions": [
        {
          "field": "installed_software",
          "operator": "matches",
          "value": "Google Chrome",
          "software_source": "windows_store",
          "version_operator": "matches",
          "version_value": "120.0.6099.110",
          "use_regex": false
        }
      ]
    }
  ]
}
```

Rules:
- groups can be combined with `AND` or `OR`
- conditions inside a group can be combined with `AND` or `OR`
- negative operators remain deferred in the first pass even in Advanced mode
- regex is available per condition

### Initial first-pass field catalog
Recommended fields:
- `hostname`
- `description`
- `site`
- `operating_system`
- `device_type`
- `status`
- `last_seen`
- `last_user`
- `internal_ip`
- `external_ip`
- `domain`
- `total_ram`
- `storage_free_percent`
- `cpu_model`
- `agent_version`
- `installed_software`

### Initial first-pass operator catalog
Text-like fields:
- `contains`
- `equals`
- `begins_with`
- `ends_with`
- `matches` when regex is enabled

Status and enum-like fields:
- `equals`

Numeric and time-like fields:
- `equals`
- `greater_than`
- `greater_than_or_equal`
- `less_than`
- `less_than_or_equal`

Software version operators:
- `matches`
- `older_than`
- `newer_than`

Deferred:
- all negative operators

## Version Comparison Rules

### Recommended implementation
- Use a dedicated Python version parsing dependency such as `packaging`.
- For standard software versions, use parsed version comparison for:
  - `older_than`
  - `newer_than`
- For exact version equality, `matches` should compare normalized parsed versions when parsing succeeds.

### Complex or odd versions
If a version value cannot be parsed reliably:
- `matches` may still compare the raw string
- `older_than` and `newer_than` should fail validation or return an explicit unsupported-comparison error for that criterion
- the operator should be guided toward regex when they need custom logic for complex version formats

This is intentional. Borealis should not silently invent ordering rules for arbitrary version strings.

## Regex Rules

### Backend dependency
Add a backend dependency that supports PCRE-style behavior as closely as practical.

Recommended candidate:
- Python `regex`

### Matching semantics
- Regex is opt-in per criterion.
- Non-regex matching remains case-insensitive by default.
- Regex matching follows the backend regex engine behavior and should not silently force case insensitivity.
- Validation errors for invalid regex patterns must be surfaced cleanly in preview and save flows.

## Software Inventory Collection Plan

### Windows
Collect two categories:
- uninstall registry software
- AppX / Windows Store apps

Required output fields:
- `name`
- `version`
- `source`

Suggested source values:
- `local_installed`
- `windows_store`

Recommended collection changes:
- extend `role_DeviceAudit.py`
- retain the merged operator-facing software list
- also emit source-aware structured records for Engine normalization

### Linux
Collect:
- `dpkg`
- `rpm`

Required output fields:
- `name`
- `version`
- `source`

Suggested source values:
- `dpkg`
- `rpm`

### Engine ingestion behavior
At `/api/agent/details` ingest time:
- continue storing summary/detail JSON for Device Details rendering
- additionally refresh normalized `device_software_inventory` rows for the reporting and filter path

Recommended strategy:
- delete existing normalized software rows for the device
- insert the fresh current snapshot
- index normalized names for case-insensitive matching

## Backend API Plan

### Endpoints to add or rebuild
- `GET /api/device_filters`
- `GET /api/device_filters/<filter_id>`
- `POST /api/device_filters`
- `PUT /api/device_filters/<filter_id>`
- `DELETE /api/device_filters/<filter_id>`
- `POST /api/device_filters/<filter_id>/clone`
- `POST /api/device_filters/<filter_id>/archive`
- `POST /api/device_filters/<filter_id>/unarchive`
- `POST /api/device_filters/preview`
- `GET /api/device_filters/<filter_id>/usage`
- `GET /api/device_filters/metadata`

### Auth requirements
Mirror the device-management auth pattern explicitly:
- list, detail, preview, metadata require authenticated operator access
- create, update, clone, archive, unarchive, delete require authenticated operator access
- `last_edited_by` must be derived server-side from the current user
- do not trust client-provided editor identity

### Archive and delete conflict responses
Recommended HTTP behavior:
- return `409 Conflict` when archive or delete is blocked because jobs reference the filter

Recommended response shape:

```json
{
  "error": "filter_in_use",
  "message": "This filter is referenced by scheduled jobs.",
  "jobs": [
    {
      "id": 14,
      "name": "Weekly Patch Ring",
      "path": "/scheduling/job/14"
    }
  ]
}
```

### Clone response behavior
Recommended behavior:
- clone copies description, site mode, site selection, criteria payloads
- clone resets `archived` to `0`
- clone updates `name` to `(Clone) <Original Name>`
- clone updates `created_at`, `updated_at`, and `last_edited_by`

### Preview endpoint behavior
Recommended request:
- accepts either a saved filter ID or an unsaved draft payload
- accepts optional pagination or result limit

Recommended response:
- matched count
- site summary if useful
- device preview rows
- validation errors if payload is invalid

Important:
- preview must use the same backend matcher used by the scheduler
- preview should only run when requested manually by the operator

## Matcher Redesign Plan

### Core requirements
Rewrite `Data/Engine/services/filters/matcher.py` to:
- understand typed fields
- evaluate only one active criteria mode at a time
- evaluate site mode using selected `site_id` sets
- evaluate software using normalized software inventory
- support preview, count, and scheduled target resolution through the same codepath

### Performance expectations
The matcher should avoid loading more data than necessary:
- if no software criteria are present, do not join or hydrate normalized software data unnecessarily
- use one device inventory snapshot per request where practical
- compute list-page device counts server-side, but do not continuously recompute while typing in the UI

### Device record normalization
The matcher should expose a stable internal device shape rather than reusing ad hoc frontend field names.

Suggested normalized internal fields:
- `device_guid`
- `hostname`
- `description`
- `site_id`
- `site_name`
- `operating_system`
- `device_type`
- `status`
- `last_seen`
- `last_user`
- `internal_ip`
- `external_ip`
- `domain`
- `total_ram`
- `storage_free_percent`
- `cpu_model`
- `agent_version`
- `software_records`

### Software matching semantics
Name matching:
- case-insensitive by default
- regex only when enabled

Source matching:
- optional
- exact enum compare

Version matching:
- optional
- only evaluated when a version criterion is present

## Filter Usage Tracking

### Purpose
The system must be able to answer:
- which scheduled jobs reference this filter
- whether archive is allowed
- whether delete is allowed

### Implementation
Reuse scheduled job target definitions as the source of truth.

Required backend ability:
- query scheduled jobs where `targets_json` references a given `filter_id`
- return those jobs in list/detail/usage payloads

Recommended UI surface:
- row-level action or link labeled `Jobs Referencing this Filter`
- opens a dialog or panel listing job names
- each job name is clickable and opens the job editor directly

## Filter List UI Plan

### File
- `Data/Engine/web-interface/src/Devices/Filters/Filter_List.jsx`

### Required changes
- remove sample fallback rows entirely
- show real loading states
- show real error states
- add top-level tabs:
  - `Active`
  - `Archived`
- add columns for:
  - filter name
  - description
  - scope
  - matched device count
  - last edited by
  - last edited
  - usage summary
- add row actions:
  - `Edit`
  - `View Devices`
  - `Clone`
  - `Archive` or `Unarchive`
  - `Delete`
  - `Jobs Referencing this Filter`

### Scope display rules
- `Global`
- `Specific Sites`
- `Global w/ Exclusions`

If site mode references selected sites:
- display human-readable site names in the grid or row details

### Jobs usage action
If the filter has referencing jobs:
- show a clickable affordance for `Jobs Referencing this Filter`
- open a dialog or panel with clickable job names

If no jobs reference the filter:
- the affordance may be hidden or disabled

## Filter Editor UI Plan

### File
- `Data/Engine/web-interface/src/Devices/Filters/Filter_Editor.jsx`

### Top-level tabs
- `Name`
- `Scope`
- `Criteria`
- `Results`

### Name tab
Fields:
- filter name
- single-line description

### Scope tab
Controls:
- `Global`
- `Specific Sites`
- `Global w/ Exclusions`

If `Specific Sites`:
- show selectable sites table or list

If `Global w/ Exclusions`:
- show selectable sites table or list labeled as exclusions

### Criteria tab
Nested tabs:
- `Basic`
- `Advanced`

#### Basic mode
- flat list of `AND` criteria only
- no groups
- no negative operators
- regex toggle per row

When `Installed Software` is selected:
- keep the main criterion on one line
- optional inline controls appear to the right of the main value field
- inline optional controls:
  - source
  - version comparator
  - version value
  - regex toggle

#### Advanced mode
- grouped logic
- group-level `AND` / `OR`
- condition-level `AND` / `OR`
- regex toggle per condition
- no negative operators in first pass

### Results tab
- manual apply only
- no auto-recompute while editing
- preview must call the backend preview endpoint
- preview grid should show matched devices and count

## Scheduled Job Integration Plan

### File
- `Data/Engine/services/API/scheduled_jobs/job_scheduler.py`

### Required behavior
- when a run starts, resolve filter targets into a frozen target snapshot
- execute only against that resolved snapshot for the duration of the run
- on the next run, resolve again

### Empty target behavior
If the filter resolves to zero devices:
- do not dispatch to any device
- mark the run as `skipped`
- set `skip_reason = no_devices_targeted`
- surface `No Devices Targeted` in UI and history

### Filter picker behavior
Archived filters must be excluded from the scheduler target picker entirely.

### Deletion and archive safety
When the scheduler references a filter:
- that filter cannot be archived
- that filter cannot be deleted

## Device List Integration Plan

### Goal
Allow operators to inspect filter results visually without merging device views and automation filters into one feature.

### UI behavior
Add `View Devices` to the filter list.

Clicking `View Devices` should:
- navigate to the device list page
- apply ephemeral device-list filtering based on the selected saved filter
- not create a saved device view

### Translation strategy
Preferred implementation:
- if the saved filter can be faithfully represented in device-list UI filters, hydrate those filters visibly
- if a saved filter cannot be faithfully represented in native device-list filter controls, preserve an obvious "viewing saved filter" state and use the backend-filtered dataset rather than losing accuracy

Important:
- do not silently broaden or weaken advanced criteria just to fit AG Grid text filters

## Cleanup and Redundancy Removal

### Remove duplicated filter normalization
Consolidate filter payload handling across:
- `Data/Engine/web-interface/src/Devices/Filters/Filter_List.jsx`
- `Data/Engine/web-interface/src/Devices/Filters/Filter_Editor.jsx`
- `Data/Engine/web-interface/src/Scheduling/Create_Job.jsx`

### Remove client-side criteria evaluation
Delete the browser-side matcher from:
- `Data/Engine/web-interface/src/Devices/Filters/Filter_Editor.jsx`

### Remove sample filter fallback data
Delete fake sample rows from:
- `Data/Engine/web-interface/src/Devices/Filters/Filter_List.jsx`

### Remove or defer partial `Groups` wiring
Do not leave the UI half-pointing at a feature that is not part of this implementation.

Relevant files:
- `Data/Engine/web-interface/src/App.jsx`
- `Data/Engine/web-interface/src/Navigation_Sidebar.jsx`

## Dependencies

### Engine dependencies
Add dependencies as needed for:
- PCRE-style regex support
- robust software version parsing

Recommended candidates:
- `regex`
- `packaging`

Implementation note:
- document exact dependency choice and rationale in `Docs/technical-debt.md` only if the solution is temporary, patchy, or expected to be removed later

## Primary Files Expected to Change

### Engine backend
- `Data/Engine/database.py`
- `Data/Engine/database_migrations.py`
- `Data/Engine/services/API/filters/management.py`
- `Data/Engine/services/filters/matcher.py`
- `Data/Engine/services/API/scheduled_jobs/job_scheduler.py`
- `Data/Engine/services/API/devices/management.py`

### Agent
- `Data/Agent/Roles/role_DeviceAudit.py`

### WebUI
- `Data/Engine/web-interface/src/Devices/Filters/Filter_List.jsx`
- `Data/Engine/web-interface/src/Devices/Filters/Filter_Editor.jsx`
- `Data/Engine/web-interface/src/Scheduling/Create_Job.jsx`
- `Data/Engine/web-interface/src/Devices/Device_List.jsx`
- `Data/Engine/web-interface/src/Devices/Device_Details.jsx`
- `Data/Engine/web-interface/src/App.jsx`
- `Data/Engine/web-interface/src/Navigation_Sidebar.jsx`

### Documentation
- `Docs/db-reference.md`
- `Docs/device-management.md`
- `Docs/api-reference.md`
- `Docs/ui-and-notifications.md`
- `Docs/technical-debt.md` if needed

## Ordered Execution Plan

### [Complete] Phase 0: Preparation
1. Read the current filter UI, backend filter API, matcher, scheduler, and device inventory paths.
2. Confirm there are no real saved filters that require preserving compatibility.
3. Confirm scheduler target payload shape and where filter references are persisted.
4. Confirm how device list initial filters and navigation handoff currently work.
5. Add or update test fixtures for:
   - sites
   - devices
   - software inventory
   - scheduled jobs referencing filters

### [Complete] Phase 1: Database Redesign
1. Rebuild `device_filters` around the new strict schema.
2. Add `device_filter_sites`.
3. Add `device_software_inventory`.
4. Add `scheduled_job_run_targets`.
5. Add any needed indexes.
6. If run metadata needs `skip_reason`, add it.
7. Remove obsolete compatibility columns and assumptions from the filter table.
8. Update the database reference documentation after implementation.

### [Complete] Phase 2: Backend Filter Service Redesign
1. Rebuild `Data/Engine/services/API/filters/management.py` around the new schema.
2. Add explicit auth enforcement consistent with the device-management API.
3. Add strict payload validation.
4. Add metadata, preview, clone, archive, unarchive, delete, and usage endpoints.
5. Make `last_edited_by` server-owned.
6. Ensure archive and delete return `409` with referencing job data when blocked.

### [Complete] Phase 3: Matcher Rewrite
1. Replace ad hoc field-name handling in `matcher.py` with a typed field catalog.
2. Add criteria-mode-aware evaluation.
3. Add site-mode-aware evaluation using `site_id` sets.
4. Add software-aware evaluation using normalized software inventory.
5. Add regex handling through the chosen backend dependency.
6. Add version comparison helpers for supported parsed versions.
7. Add validation errors for unsupported version ordering on non-parseable versions.
8. Ensure preview, count, and scheduler target resolution all reuse the same evaluator.

### [Complete] Phase 4: Agent Software Collection and Engine Ingestion
1. Extend `role_DeviceAudit.py` to collect Windows AppX/MS Store apps in addition to current registry software.
2. Tag every software record with a source value.
3. Ensure Linux package records include source values for `dpkg` and `rpm`.
4. Update Engine ingestion so normalized software rows are refreshed on device details updates.
5. Preserve the merged software view for Device Details.

### [Complete] Phase 5: Filter List UI Rebuild
1. Remove sample fallback rows and fake sample error behavior.
2. Add `Active` and `Archived` tabs.
3. Add archive or unarchive actions.
4. Add clone and delete actions.
5. Add `Jobs Referencing this Filter`.
6. Add `View Devices`.
7. Surface API errors cleanly.
8. Show archive and delete conflicts with clickable job links.

### [Complete] Phase 6: Filter Editor UI Rebuild
1. Remove client-side matcher logic.
2. Load backend metadata for field and operator configuration.
3. Add description support.
4. Rebuild scope handling around:
   - `Global`
   - `Specific Sites`
   - `Global w/ Exclusions`
5. Add nested `Basic` and `Advanced` sub-tabs under `Criteria`.
6. Keep Basic and Advanced payloads isolated from each other.
7. Add software-specific inline sub-controls in Basic mode.
8. Add regex toggles in both modes.
9. Make preview manual-only through the backend preview endpoint.
10. Reopen filters into the last saved active criteria mode automatically.

### [Complete] Phase 7: Scheduler Integration
1. Exclude archived filters from scheduler target selection.
2. At run start, resolve filter targets into `scheduled_job_run_targets`.
3. Use the frozen snapshot for that run.
4. Mark empty target results as skipped with `skip_reason = no_devices_targeted`.
5. Return filter usage data so archive and delete safety checks can block correctly.

### [Complete] Phase 8: Device List Handoff
1. Add the `View Devices` action from the filter list.
2. Implement ephemeral device-list handoff.
3. Where possible, hydrate visible device-list filters to reflect the saved filter.
4. Where exact hydration is not possible, preserve exact backend-filtered results and clearly communicate that the operator is viewing a saved filter result set.

### [Complete] Phase 9: Cleanup
1. Remove duplicate filter normalization code in the UI.
2. Remove dead `Groups` wiring if not being implemented for real.
3. Remove old field aliases and compatibility shims that no longer apply.
4. Simplify the filter payload contract everywhere it is consumed.

### [Complete] Phase 10: Tests
1. Add matcher unit tests for:
   - text fields
   - numeric comparisons
   - site modes
   - regex matching
   - software name matching
   - software source matching
   - software version matching
2. Add API tests for:
   - create
   - update
   - clone
   - archive
   - unarchive
   - delete
   - preview
   - usage
   - archive/delete conflict responses
3. Add scheduler tests for:
   - point-in-time target snapshots
   - zero-target skipped runs
4. Add ingestion tests for normalized software inventory updates.
5. Add UI verification for:
   - archived tab behavior
   - blocked archive/delete flows
   - manual preview
   - Basic vs Advanced criteria isolation

### [Complete] Phase 11: Documentation
1. Update `Docs/db-reference.md`.
2. Update `Docs/device-management.md`.
3. Update `Docs/api-reference.md`.
4. Update `Docs/ui-and-notifications.md` if shared page-body or action-rail behavior changes.
5. Log technical debt only if temporary workarounds or intentionally incomplete pieces are introduced.

## Acceptance Criteria
- Filters are persisted with a strict schema and selected sites by `site_id`.
- The backend matcher is the only source of truth for evaluation.
- The filter editor does not perform local matching.
- Operators can filter by installed software name, source, and optional version.
- Operators can use regex in both Basic and Advanced modes.
- Basic and Advanced criteria remain separate and do not overwrite each other.
- Archived filters do not appear in the scheduler picker.
- Filters referenced by scheduled jobs cannot be archived or deleted.
- Operators can see which jobs reference a filter and open them directly.
- `View Devices` opens the device list with ephemeral filter behavior.
- Each scheduled run uses a point-in-time target snapshot.
- Zero-target runs are stored as skipped and displayed as `No Devices Targeted`.

## Deferred Follow-Up Work
- roles and ACLs for filter visibility
- filter ownership and sharing rules
- macOS software collection
- negative operators
- software publisher/vendor filtering
- filter version history
- redesign of site assignment storage from hostname to device GUID
