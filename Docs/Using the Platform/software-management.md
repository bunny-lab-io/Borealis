# Software Management

Software Management helps operators audit installed applications, refresh software inventory, run supported uninstall actions, and govern global software metadata fixes.

<figure class="bo-screenshot">
  <img src="../Reference/images/repo_screenshots/Installed_Software.png" alt="Borealis Installed Software inventory" loading="lazy">
  <figcaption>Installed Software shows per-device package state and software actions.</figcaption>
</figure>

## Audit Software

- Open `Alerting & Reporting > Software Audit` for fleet-level software review.
- Open a device and select `Installed Software` for device-level software inventory.
- Use filters and sorting to find package name, version, source, publisher, or installation metadata.

## Refresh Inventory

Use `Query Software Changes` when you need a fresh software snapshot after install, uninstall, override, or remediation work.

## Uninstall Software

1. Open a device.
2. Open `Installed Software`.
3. Right-click supported software row.
4. Select `Uninstall`.
5. Track output in Activity History and refresh software inventory after completion.

Borealis only exposes uninstall when it can build a supported unattended uninstall plan.

## Manage Global Overrides

Right-click software rows to create:

- Global icon overrides.
- Global uninstall overrides.
- Global uninstall blocks.
- Uninstall unblock rules.

These hotload immediately in the Engine. Commit file-backed rules later when they should ship with Borealis.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - `POST /api/device/software/<hostname>/refresh` - request fresh software inventory.
    - `POST /api/device/software/<hostname>/icon-override` - save hotloaded icon override.
    - `POST /api/device/software/<hostname>/uninstall-override` - save hotloaded uninstall override.
    - `POST /api/device/software/<hostname>/uninstall-block` - save hotloaded block rule.
    - `POST /api/device/software/<hostname>/uninstall-unblock` - remove matching block rules.
    - `POST /api/device/software/<hostname>/uninstall` - queue supported uninstall quick job.
    - `GET /api/agent/software-management/overrides` - agent override payload fetch.

    ### Related documentation

    - [Software Icon Overrides](../Reference/software-icon-overrides.md)
    - [Software Uninstall Overrides](../Reference/software-uninstall-overrides.md)
    - [Software Uninstall Blocklist](../Reference/software-uninstall-blocklist.md)
    - [Device Filters](device-filters.md)
    - [API Reference](../Reference/Data%20and%20Schema/api-reference.md)

    ### Source map

    - Installed Software UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Installed_Software.jsx`
    - Software APIs: `Data/Engine/Containers/api-backend/data/services/API/devices/software_uninstall.py`
    - Icon override loader: `Data/Engine/Containers/api-backend/data/services/API/devices/software_icons.py`
    - Agent software role: `Data/Agent/internal/roles/software_management/`

    ### Runtime behavior

    - Agent publishes raw software blobs for device detail display.
    - Engine normalizes rows into `device_software_inventory` for reliable filtering.
    - Uninstall actions run through signed quick-job execution and write Activity History.
    - Override and blocklist JSON files live under `Data/Engine/Containers/api-backend/data/services/API/devices/`.
