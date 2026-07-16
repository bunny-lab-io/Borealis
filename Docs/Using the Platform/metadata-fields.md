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

Borealis reserves some fields for standard audit data collected by bundled assemblies and future Borealis-managed uses. These field descriptions cannot be renamed in the global metadata field editor. When a reserved field has a linked assembly, the field number opens the assembly that can collect the value.

| Field Number | Field Description | Linked Assembly |
| :--- | :--- | :--- |
| Field 001 | Server Roles | `Detect Server Roles [WIN]` |
| Field 002 | Bitlocker Drive Encryption | `Audit Bitlocker / TPM Status [WIN]` |
| Field 003 | Reserved | - |
| Field 004 | Reserved | - |
| Field 005 | Reserved | - |
| Field 006 | Reserved | - |
| Field 007 | Reserved | - |
| Field 008 | Reserved | - |
| Field 009 | Reserved | - |
| Field 010 | Reserved | - |

Create recurring scheduled jobs for the reserved assemblies when you want Borealis to refresh this data on a regular cadence. Borealis does not force collection. Operators still choose which assemblies to run, which targets receive them, and how often values refresh.

## Fill Device Values

1. Open a device.
2. Open `Metadata Fields`.
3. Enter or clear values for the relevant fields.
4. Save field changes.

## Use Fields In Filters

Device Filters include a grouped `Metadata Field` selector. Use it when you need dynamic groups based on custom device values.

## Display Fields In Device Inventory

1. Open `Inventory > Devices`.
2. Open `Columns`.
3. Select the metadata field description you want to show.

Only metadata fields with non-empty descriptions appear in the Device Inventory column chooser. Fields labeled `Reserved` stay hidden.

The column chooser groups fields by Device Specs, Location, Networking, Heartbeat, and Metadata. Each group is sorted A-Z by display name.

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
    - Device list API payload: `Data/Engine/Containers/api-backend/cmd/api-backend/devices.go`
    - Device list UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Device_List.jsx`
    - Admin UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Admin/Metadata_Field_List.jsx`
    - Device tab: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Device_Metadata.jsx`
    - Shared field cell renderers: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Metadata_Field_Cells.jsx`

    ### Runtime behavior

    - Field definitions live in `metadata_field_definitions`.
    - Sparse per-device values live in `device_metadata_fields`.
    - Values are base64-encoded at rest and capped at 1024 decoded characters.
    - Newest `modified_at` wins. Future agent timestamps are clamped before conflict comparison.
    - Reserved metadata field definitions are API-level constants. Existing database descriptions for reserved fields are ignored during list rendering, and definition updates for reserved fields return `reserved_metadata_field`.
    - `Field 001` maps to script assembly `628f6686-c7c4-477d-bf9a-13c73d8246ba`; `Field 002` maps to script assembly `c4f97974-1d9c-4e89-8257-8a139637e51f`.
    - `Field 003` through `Field 010` are reserved placeholders named `Reserved` with no linked assemblies.
    - `Metadata_Field_Cells.jsx` renders linked reserved field numbers in both the global Metadata Fields page and device metadata tab. Linked field numbers have no underline, hover with the assembly name, and navigate to the assembly editor. Reserved placeholders without an assembly GUID render as plain field numbers.
    - `GET /api/devices` includes a sparse decoded `metadata_fields` map for each visible device so Device List can render metadata columns without per-device requests.
    - `Device_List.jsx` flattens sparse `metadata_fields` values into `metadataField###` row properties. The column chooser appends metadata definitions whose `description` is non-empty and not `Reserved`; saved views persist those dynamic column ids like any other Device List column.
    - Device List column chooser groups are codified in `buildDeviceListColumnGroups`: `Device Specs`, `Location`, `Networking`, `Heartbeat`, then `Metadata`. Every group sorts options A-Z by rendered label before display.
    - The global Metadata Fields grid shows definition audit data from `metadata_field_definitions`: `updated_at` renders as `Modified`, and `updated_by` renders as `Source`. These audit columns track administrator label changes, not per-device metadata value changes.
    - The device metadata grid keeps `Field Number` fixed at 150px with no resize handle, autosizes `Field Description` against visible descriptions, sets fixed widths for `Modified` and `Source`, disables the `Source` resize handle, and leaves `Value` as the only flex column so it consumes remaining grid width.

    ### Adding reserved fields

    When adding another static reserved metadata field, keep the backend constants, WebUI behavior, operator table, and tests synchronized in the same change.

    1. Choose an unused field number from `1` through `500`.
    2. If the field should link to an assembly, confirm the assembly is bundled or otherwise stable, then record its display name, GUID, and assembly type.
    3. Add the field to `reservedMetadataFields` in `Data/Engine/Containers/api-backend/cmd/api-backend/metadata_fields.go`.
    4. Add the same field to `RESERVED_METADATA_FIELDS` in `Data/Engine/Containers/api-backend/data/services/metadata_fields.py`.
    5. Update the visible Reserved Fields table on this page with the field number, description, and linked assembly name, or `-` when the field is an unlinked placeholder.
    6. Update tests that assert reserved labels, linked assembly metadata, and rename rejection:
        - `Data/Engine/Containers/api-backend/cmd/api-backend/main_test.go`
        - `Data/Engine/Unit_Tests/test_metadata_fields.py`
        - `Data/Engine/Containers/webui-frontend/data/web-interface/Unit_Tests/Admin/Metadata_Field_List.reservedFields.test.jsx`
    7. Run focused validation before handoff:
        - `/opt/Borealis/Dependencies/Go/go1.23.12/bin/gofmt -w Data/Engine/Containers/api-backend/cmd/api-backend/metadata_fields.go Data/Engine/Containers/api-backend/cmd/api-backend/main_test.go`
        - `cd Data/Engine/Containers/api-backend && /opt/Borealis/Dependencies/Go/go1.23.12/bin/go test ./cmd/api-backend -run 'TestMetadata'`
        - `python3 -m py_compile Data/Engine/Containers/api-backend/data/services/metadata_fields.py Data/Engine/Unit_Tests/test_metadata_fields.py`
        - `./Engine_Unit_Tests.sh --domain webui` when the WebUI runtime test cache exists.

    Reserved field records require these values:

    - Field number: integer key in both backend maps.
    - Description: immutable metadata field label shown in the admin editor and device metadata grid.
    - Assembly name: optional; tooltip shown when hovering a linked field number.
    - Assembly GUID: optional; route target for the linked field number.
    - Assembly type: optional unless assembly GUID exists; use `script`, `ansible_playbook`, or `workflow` to control the generated `/assemblies/.../<guid>` route.

    Reserved placeholder fields should set only `Description: "Reserved"` in Go and `"description": "Reserved"` in Python. Do not set assembly name, GUID, type, or path for placeholders. The WebUI treats reserved fields without an assembly GUID as immutable non-links.

    Do not add a database migration for reserved labels. Reserved labels intentionally override persisted `metadata_field_definitions` descriptions at render time so existing Engines become consistent without data rewrite. Device values remain editable; only global reserved field descriptions are immutable.
