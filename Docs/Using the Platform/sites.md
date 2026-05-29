# Sites

Sites group devices for enrollment, operator visibility, targeting, onboarding, and organization. Create sites before enrolling agents so every new device lands in the right operational boundary.

<figure class="bo-screenshot">
  <img src="../Reference/images/repo_screenshots/Site_List.png" alt="Borealis Site List" loading="lazy">
  <figcaption>Sites group devices for enrollment, operator visibility, targeting, and organization.</figcaption>
</figure>

## Create Site

1. Open `Sites > Sites`.
2. Select `Create Site`.
3. Enter name and optional description.
4. Save.
5. Copy the site install command when deploying agents for that site.

Each site has its own enrollment code. Agent install commands include the selected Engine URL and site enrollment code.

## Assign Devices

Use site assignment actions when a device was approved without the expected site, moved between customers, or needs admin review. Devices without site assignment are admin-only.

## Onboard Devices

Use `Onboard Devices` from the Sites page when the Engine should attempt local-network agent installation against Linux or Windows targets.

Onboarding jobs still send agents through Device Approvals. Successful remote install means the agent reached Borealis, not that it is trusted yet.

!!! tip

    Keep one site per customer, lab, or security boundary. Filters and scheduled jobs become easier to reason about when site scope matches real ownership.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - `GET /api/sites` - list visible sites and install-command metadata.
    - `POST /api/sites` - create site.
    - `POST /api/sites/delete` - delete sites.
    - `GET /api/sites/device_map` - hostname-to-site map.
    - `POST /api/sites/assign` - assign devices to site.
    - `POST /api/sites/rename` - rename site.
    - `POST /api/sites/<site_id>/auto-approval` - set or clear temporary site auto-approval.
    - `GET /api/server/workers?history_seconds=60` - active/recent worker state used by Sites and Engine Status.

    ### Related documentation

    - [Device Approvals](device-approvals.md)
    - [Site Assignments](site-assignments.md)
    - [Scheduled Jobs](scheduled-jobs.md)
    - [Database Reference](../Reference/Data%20and%20Schema/db-reference.md)

    ### Source map

    - Site API: `Data/Engine/Containers/api-backend/data/services/API/sites/management.py`
    - Sites UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Sites/Site_List.jsx`
    - Site assignment UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Sites/Site_Assignment.jsx`

    ### Runtime behavior

    - Sites live in `sites`.
    - Device membership lives in `device_sites`.
    - Enrollment codes live on `sites.enrollment_code`.
    - Operators with no assigned sites see no normal device/site inventory unless they are admins.
