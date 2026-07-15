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

## Reserved Fields

Borealis reserves some fields for standard audit data collected by bundled assemblies. These field descriptions cannot be renamed in the global metadata field editor. The field number links to the assembly that can collect the value.

| Field Number | Field Description | Linked Assembly |
| :--- | :--- | :--- |
| Field 001 | Server Roles | `Detect Server Roles [WIN]` |
| Field 002 | Bitlocker Drive Encryption | `Audit Bitlocker / TPM Status [WIN]` |

Create recurring scheduled jobs for the reserved assemblies when you want Borealis to refresh this data on a regular cadence. Borealis does not force collection. Operators still choose which assemblies to run, which targets receive them, and how often values refresh.

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

    - Metadata API: `Data/Engine/Containers/api-backend/cmd/api-backend/metadata_fields.go`
    - Shared metadata helpers: `Data/Engine/Containers/api-backend/data/services/metadata_fields.py`
    - Admin UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Admin/Metadata_Field_List.jsx`
    - Device tab: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Device_Metadata.jsx`

    ### Runtime behavior

    - Field definitions live in `metadata_field_definitions`.
    - Sparse per-device values live in `device_metadata_fields`.
    - Values are base64-encoded at rest and capped at 1024 decoded characters.
    - Newest `modified_at` wins. Future agent timestamps are clamped before conflict comparison.
    - Reserved metadata field definitions are API-level constants. Existing database descriptions for reserved fields are ignored during list rendering, and definition updates for reserved fields return `reserved_metadata_field`.
    - `Field 001` maps to script assembly `628f6686-c7c4-477d-bf9a-13c73d8246ba`; `Field 002` maps to script assembly `c4f97974-1d9c-4e89-8257-8a139637e51f`.
