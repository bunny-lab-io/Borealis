# Registry Editor

Registry Editor lets operators browse and change Windows registry keys and values from Device Summary without opening a remote shell.

## Browse Registry

1. Open a Windows device.
2. Open `Backend Tools > Registry`.
3. Select a root hive such as `HKLM`.
4. Double-click keys or enter a registry path such as `HKLM\SOFTWARE`.
5. Select a value to review its type and data.

Registry paths use short hive names: `HKCR`, `HKCU`, `HKLM`, `HKU`, and `HKCC`.

!!! info

    `HKCU` is the Agent service context. For interactive-user hives, browse loaded user hives under `HKU`.

## Edit Keys And Values

- `Key` creates a subkey under the current path.
- `Value` creates a value under the current path.
- `Edit` updates the selected value data and type.
- `Rename` changes the selected key name.
- `Delete` removes the selected key or value.

Supported editable value types are `REG_SZ`, `REG_EXPAND_SZ`, `REG_MULTI_SZ`, `REG_DWORD`, `REG_QWORD`, and `REG_BINARY`. Unsupported value types are visible but read-only.

!!! warning

    Registry actions run in the device service context: SYSTEM on Windows. Confirm path, hive, and selected item before destructive actions.

!!! warning

    Key deletion requires typing the full registry path. Use recursive deletion only when child keys should be removed too.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - `GET /api/device/registry/<hostname>/roots` - list registry hives.
    - `GET /api/device/registry/<hostname>/children?path=<registry-path>` - list subkeys and values for one registry key.
    - `POST /api/device/registry/<hostname>/key/create` - create a subkey.
    - `POST /api/device/registry/<hostname>/key/rename` - rename a key by copy/delete.
    - `POST /api/device/registry/<hostname>/key/delete` - delete a key, optionally recursively.
    - `POST /api/device/registry/<hostname>/value/create` - create a value.
    - `POST /api/device/registry/<hostname>/value/update` - update a value.
    - `POST /api/device/registry/<hostname>/value/delete` - delete a value.

    ### Related documentation

    - [Device Auditing](device-auditing.md)
    - [File Management](file-management.md)
    - [Agent Runtime](../Reference/Core%20Runtimes/agent-runtime.md)
    - [API Reference](../Reference/Data%20and%20Schema/api-reference.md)

    ### Source map

    - Registry API: `Data/Engine/Containers/api-backend/cmd/api-backend/remote_registry.go`
    - Registry tab UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Remote_Registry_Editor.jsx`
    - Agent registry role: `Data/Agent/internal/roles/registry_management/`

    ### Runtime behavior

    - Engine routes use the same operator device/site scope and site-worker host-service socket bridge as File Management.
    - Agent dispatch listens on `registry_management_request` from the SYSTEM socket.
    - Windows agents use `golang.org/x/sys/windows/registry`.
    - Linux agents report Registry Management as unsupported instead of unhealthy.
    - Key rename copies the source key tree to the new sibling name, preserves raw value types, then deletes the old key tree.
