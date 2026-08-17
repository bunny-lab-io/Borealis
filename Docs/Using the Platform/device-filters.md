# Device Filters

Device Filters create reusable dynamic groups. Use them for device views, scheduled-job targets, watchdog targets, and site-scoped fleet segmentation.

<figure class="bo-screenshot">
  <img src="../Reference/images/repo_screenshots/Device_Filter_Editor.png" alt="Borealis Device Filter Editor" loading="lazy">
  <figcaption>Device Filter Editor defines criteria and site scope for dynamic endpoint matching.</figcaption>
</figure>

## Create Filter

1. Open `Filters & Groups > Filters`.
2. Select `New Filter`.
3. Name the filter and choose site scope.
4. Add criteria such as OS, status, site, software, user, or metadata field.
5. Use Preview to confirm matched devices.
6. Save the filter.

## Choose Site Scope

- `Global` includes all sites visible to the authoring operator.
- `Specific Sites` includes only selected sites.
- `Global w/ Exclusions` includes broad scope except excluded sites.

Non-admin operators can only save filters inside their assigned site scope.

## Use Filters

- Device list: narrow fleet views.
- Scheduled Jobs: target current devices matching the filter when a run starts.
- Watchdogs: apply monitoring policy to dynamic device sets.
- Metadata Fields: match custom per-device values when normal inventory fields are not enough.

## Avoid Surprises

- Preview before save when criteria include software or metadata fields.
- Archived filters stay out of scheduler pickers.
- Filter targets in scheduled jobs preserve allowed site scope from the creator.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - `GET /api/device_filters` - list filters.
    - `GET /api/device_filters/metadata` - filter field/operator metadata.
    - `POST /api/device_filters/preview` - preview matches.
    - `GET /api/device_filters/<filter_id>` - get filter.
    - `GET /api/device_filters/<filter_id>/usage` - scheduled-job usage summary.
    - `POST /api/device_filters` - create filter.
    - `PUT /api/device_filters/<filter_id>` - update filter.
    - `POST /api/device_filters/<filter_id>/clone` - clone filter.
    - `POST /api/device_filters/<filter_id>/archive` - archive filter.
    - `POST /api/device_filters/<filter_id>/unarchive` - restore filter.
    - `DELETE /api/device_filters/<filter_id>` - delete filter.

    ### Related documentation

    - [Scheduled Jobs](scheduled-jobs.md)
    - [Watchdogs](watchdogs.md)
    - [Metadata Fields](metadata-fields.md)
    - [Database Reference](../Reference/Data%20and%20Schema/db-reference.md)

    ### Source map

    - Filter API and matching: `Data/Engine/Containers/api-backend/cmd/api-backend/device_filters.go`
    - Filter contract tests: `Data/Engine/Containers/api-backend/cmd/api-backend/device_filters_test.go`
    - UI routes: `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/routes/router.jsx`

    ### Runtime behavior

    - Filters store typed `basic_criteria_json` and `advanced_criteria_json`.
    - Site scope is normalized through `site_mode` and `device_filter_sites`.
    - Matching uses inventory snapshots plus normalized software rows and sparse metadata field rows.
