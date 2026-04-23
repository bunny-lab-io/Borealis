# Software Icon Overrides
[Back to Docs Index](../index.md) | [Index (HTML)](../index.html)

## Purpose
Explain how to add file-backed icon-location overrides for installed software when registry `DisplayIcon` metadata is missing or points at the wrong asset.

## Override File
- Path: `Data/Engine/services/API/devices/software_icons_overrides.json`
- Runtime consumer:
  - Engine serves the override payload from `GET /api/agent/software-management/overrides`
  - Agent applies matching icon overrides during `software_management` inventory refresh before it extracts icon payloads

## JSON Shape
```json
{
  "windows_icon_overrides": [
    {
      "rule_id": "icon_override_contoso_agent",
      "source": "local_installed",
      "name": "Contoso Agent",
      "version": "2.4.1",
      "publisher_contains_any": ["Contoso Ltd"],
      "display_icon": "C:\\Program Files\\Contoso Agent\\branding\\agent.ico"
    }
  ]
}
```

## Supported Match Fields
- `source`
  Exact source match after Borealis normalization. Use `local_installed` for normal Windows registry software rows.
- `name`
  Exact software-name match, case-insensitive.
- `version`
  Exact software-version match, case-insensitive.
- `publisher_contains_any`
  Optional substring list matched against the software row publisher.
- `name_contains_any`
  Optional substring list matched against the software row name.
- `install_location_contains_any`
  Optional substring list matched against `metadata.install_location`.

## Required Action Field
- `display_icon`
  Verified icon resource string to hand back to the agent. This can be an `.ico`, `.exe`, `.dll`, or a Windows resource string such as `C:\Program Files\Vendor\App\app.exe,0`.

## Behavior Notes
- The first matching rule wins.
- Borealis replaces the row's `metadata.display_icon` before icon extraction runs.
- On Windows, the agent can extract icons from `.ico` files directly and from embedded resources in `.exe`, `.dll`, `.icl`, `.cpl`, `.ocx`, and `.scr` files.
- Resource-string overrides such as `C:\Program Files\Vendor\App\app.exe,0` are supported, and Borealis honors the icon index during extraction.
- Borealis uses built-in Windows APIs for EXE and DLL icon extraction; no third-party icon-extraction library is required on the agent.
- When an override is applied, the agent also preserves:
  - `metadata.original_display_icon`
  - `metadata.display_icon_override`
  - `metadata.display_icon_override_rule_id`
- The new icon appears after the next `software_management` inventory refresh from the agent.

## Fast Workflow
1. In the Installed Software tab, right-click the software name.
2. Click `Copy software debug information`.
3. Paste the JSON payload into your notes or into a Codex request.
4. Copy the suggested `icon_override` entry from that payload into `software_icons_overrides.json`.
5. Replace `display_icon` with a verified local icon path/resource if needed.
6. Wait for the next software refresh or trigger a software-management refresh path.

## Example
```json
{
  "windows_icon_overrides": [
    {
      "rule_id": "icon_override_fedora_media_writer",
      "source": "local_installed",
      "name": "Fedora Media Writer",
      "version": "5.2.8",
      "publisher_contains_any": ["Fedora Project"],
      "display_icon": "C:\\Program Files\\Fedora Media Writer\\mediawriter.exe,0"
    }
  ]
}
```

## Related Documentation
- [Device Management](../device-management.md)
- [API Reference](../api-reference.md)
- [Adding Software to Uninstall Overrides](adding-software-to-uninstall-overrides.md)
- [Adding Software to Uninstall Blocklist](adding-software-to-uninstall-blocklist.md)

## Codex Agent (Detailed)
### How to read copied software debug payloads
- The Installed Software context-menu copy action places a JSON blob with `schema = borealis_software_debug_v1` on the clipboard.
- Prefer these fields when creating an icon override:
  - `software.name`
  - `software.version`
  - `software.source`
  - `software.metadata.publisher`
  - `software.metadata.install_location`
  - `software.metadata.display_icon`
  - `software.metadata.original_display_icon`
  - `suggested_entries.icon_override`
- If the copied payload already contains `display_icon_override`, treat that as the current active override value.

### Recommended authoring rules
- Start with the exact `name` from the copied payload.
- Keep `version` only when the icon path is version-specific.
- Keep `publisher_contains_any` when the title name is generic or when there are likely multiple rows with similar names.
- Use `install_location_contains_any` when the path is the most stable discriminator.
- Prefer the narrowest rule that still survives normal version churn.

### Implementation references
- Engine override loader: `Data/Engine/services/API/devices/software_icons.py`
- Agent application path: `Data/Agent/Roles/system_software_management.py`
- Agent publishing role: `Data/Agent/Roles/role_system_software_management.py`
- Installed Software UI copy action: `Data/Engine/web-interface/src/Devices/Tabs/Installed_Software.jsx`
