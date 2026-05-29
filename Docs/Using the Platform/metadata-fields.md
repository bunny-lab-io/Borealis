# Metadata Fields

Metadata Fields give operators 500 simple custom text fields per device. Use them for data Borealis does not already collect, such as asset tags, warranty notes, customer codes, rack positions, or automation hints.

<figure class="bo-screenshot">
  <img src="../Reference/images/repo_screenshots/Device_Filter_Editor.png" alt="Borealis metadata field filter picker" loading="lazy">
  <figcaption>Metadata fields can be labeled globally and used in filter criteria.</figcaption>
</figure>

## Label Fields

1. Open `Admin Settings > Metadata Fields`.
2. Find `Field 001` through `Field 500`.
3. Add a clear description for fields your team uses.
4. Save.

Descriptions are global labels. They do not store device values.

## Fill Device Values

1. Open a device.
2. Open `Metadata Fields`.
3. Enter or clear values for the relevant fields.
4. Save field changes.

## Use Fields In Filters

Device Filters include a grouped `Metadata Field` selector. Use it when you need dynamic groups based on custom device values.

## Agent CLI Usage

Scripts can read or queue values locally through the Agent CLI:

```powershell
Agent.exe --metadata get 1
Agent.exe --metadata set 1 "Asset-1234"
```

Blank values queue a clear.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - `GET /api/metadata_fields` - list all field labels and limits.
    - `PUT /api/metadata_fields/<field_number>` - update global label.
    - `GET /api/devices/<device_id>/metadata_fields` - list device values.
    - `PUT /api/devices/<device_id>/metadata_fields/<field_number>` - update or clear device value.
    - `GET /api/agent/metadata/<field_number>` - device-authenticated Agent CLI read.
    - `POST /api/agent/heartbeat` - metadata queue sync and ack.

    ### Related documentation

    - [Device Filters](device-filters.md)
    - [Device Auditing](device-auditing.md)
    - [Agent Runtime](../Reference/Core%20Runtimes/agent-runtime.md)
    - [Database Reference](../Reference/Data%20and%20Schema/db-reference.md)

    ### Source map

    - Metadata API: `Data/Engine/Containers/api-backend/data/services/API/metadata_fields.py`
    - Admin UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Admin/Metadata_Field_List.jsx`
    - Device tab: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Device_Metadata.jsx`

    ### Runtime behavior

    - Field definitions live in `metadata_field_definitions`.
    - Sparse per-device values live in `device_metadata_fields`.
    - Values are base64-encoded at rest and capped at 1024 decoded characters.
    - Newest `modified_at` wins. Future agent timestamps are clamped before conflict comparison.
