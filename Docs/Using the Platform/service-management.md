# Service Management

Service Management shows cached service inventory for one device and lets operators send start, stop, or restart actions.

<figure class="bo-screenshot">
  <img src="../Reference/images/repo_screenshots/Device_Service_List.png" alt="Borealis Device Service List" loading="lazy">
  <figcaption>Service inventory shows cached service state and supports service management actions for devices.</figcaption>
</figure>

## Inspect Services

1. Open a device.
2. Open `Services`.
3. Search or filter by service status, name, or description.
4. Review pending action badges when a service change was recently requested.

## Control Service

1. Select target service.
2. Choose `Start`, `Stop`, or `Restart`.
3. Wait for refresh to confirm state.

Pending state is shown until fresh agent service inventory confirms the result.

## Good Use Cases

- Restart stuck vendor services.
- Stop noisy services before remediation.
- Confirm service state before running watchdog or script remediation.
- Validate automatic service-control watchdog actions.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - `GET /api/device/services/<hostname>` - cached service inventory.
    - `POST /api/device/services/<hostname>/action` - start, stop, or restart one service.

    ### Related documentation

    - [Watchdogs](watchdogs.md)
    - [Device Auditing](device-auditing.md)
    - [Agent Runtime](../Reference/Core%20Runtimes/agent-runtime.md)
    - [API Reference](../Reference/Data%20and%20Schema/api-reference.md)

    ### Source map

    - Service API: `Data/Engine/Containers/api-backend/data/services/API/devices/services.py`
    - Service inventory API: `Data/Engine/Containers/api-backend/data/services/API/devices/service_inventory.py`
    - Service tab UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Service_List.jsx`
    - Agent service role: `Data/Agent/internal/roles/service_management/`

    ### Runtime behavior

    - Service inventory is stored in the `devices.services` JSON payload.
    - Operator actions are merged into UI state as pending until a fresh agent inventory snapshot confirms final service state.
    - Watchdogs can use service state and service pending timeout rules.
