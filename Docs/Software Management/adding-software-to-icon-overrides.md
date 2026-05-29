# Software Icon Overrides

## Purpose
Explain how to add file-backed icon-location overrides for installed software when registry `DisplayIcon` metadata is missing or points at the wrong asset.

## Override File
- Path: `Data/Engine/Containers/api-backend/data/services/API/devices/software_icons_overrides.json`
- Runtime consumer:
  - Engine serves the override payload from `GET /api/agent/software-management/overrides`
- Agent applies matching icon overrides during `software_management` inventory refresh before it extracts icon payloads

## Operator Hotload Workflow
1. Open the device `Installed Software` tab.
2. Right-click the software name.
3. Click `Create Global Icon Override`.
4. Choose a suggested icon candidate, enter a verified `.ico`, `.exe`, `.dll`, or resource path manually, or check `Remove the icon entirely` if you intentionally want Borealis to blank the icon.
5. Save the override.
6. Borealis writes the rule into `software_icons_overrides.json`, hotloads it immediately, and requests a software inventory refresh for that device.
7. Use `Query Software Changes` when you want to force a fresh software snapshot instead of waiting for the normal poll cadence.
8. After the override behaves correctly, commit `software_icons_overrides.json` to Git so the rule survives Engine image rebuilds and becomes official for all Borealis users.

## JSON Shape
```json
{
  "windows_icon_overrides": [
    {
      "rule_id": "icon_override_contoso_agent",
      "name": "Contoso Agent",
      "display_icon": "C:\\Program Files\\Contoso Agent\\branding\\agent.ico"
    }
  ]
}
```

Clear-icon rules are also supported:

```json
{
  "windows_icon_overrides": [
    {
      "rule_id": "icon_override_contoso_agent_blank",
      "name": "Contoso Agent",
      "clear_icon": true
    }
  ]
}
```

## Supported Match Fields
- `name`
  Exact software-name match, case-insensitive.
  Borealis icon overrides now match by exact software name only. Version, publisher, source, and install-location fields are intentionally ignored for icon overrides.

## Required Action Field
- `display_icon`
  Verified icon resource string to hand back to the agent. This can be an `.ico`, `.exe`, `.dll`, or a Windows resource string such as `C:\Program Files\Vendor\App\app.exe,0`.
- `clear_icon`
  Optional boolean flag. When `true`, Borealis intentionally blanks the icon for matching rows instead of extracting one.

## Behavior Notes
- The first matching rule wins.
- Borealis replaces the row's `metadata.display_icon` before icon extraction runs.
- The Engine also reapplies matching icon override metadata when it serves device details, so older or stale software rows still see the same global override behavior even if they were inventoried by an older agent.
- Once any refreshed device publishes the override-derived icon asset for a given software name, the Engine reuses that known icon hash across other same-name rows fleet-wide.
- On Windows, the agent can extract icons from `.ico` files directly and from embedded resources in `.exe`, `.dll`, `.icl`, `.cpl`, `.ocx`, and `.scr` files.
- Resource-string overrides such as `C:\Program Files\Vendor\App\app.exe,0` are supported, and Borealis honors the icon index during extraction.
- Borealis uses built-in Windows APIs for EXE and DLL icon extraction; no third-party icon-extraction library is required on the agent.
- When an override is applied, the agent also preserves:
  - `metadata.original_display_icon`
  - `metadata.display_icon_override`
  - `metadata.display_icon_override_rule_id`
- Clear-icon overrides also set:
  - `metadata.display_icon_override_cleared`
- The new icon appears after the next `software_management` inventory refresh from the agent.

## Manual File Workflow
1. Open `software_icons_overrides.json`.
2. Add or update a rule under `windows_icon_overrides`.
3. Save the file.
4. Wait for the next agent software-management override fetch or trigger `Query Software Changes` from the Installed Software tab.
5. Commit `software_icons_overrides.json` to Git when the rule must survive Engine image rebuilds.

## Example
```json
{
  "windows_icon_overrides": [
    {
      "rule_id": "icon_override_fedora_media_writer",
      "name": "Fedora Media Writer",
      "display_icon": "C:\\Program Files\\Fedora Media Writer\\mediawriter.exe,0"
    }
  ]
}
```

## Related Documentation
- [Device Management](../Using%20the%20Platform/device-management.md)
- [API Reference](../Reference/Data%20and%20Schema/api-reference.md)
- [Adding Software to Uninstall Overrides](adding-software-to-uninstall-overrides.md)
- [Adding Software to Uninstall Blocklist](adding-software-to-uninstall-blocklist.md)

??? example "Detailed Codex Breakdown"

    ### Codex workflow
    - Prefer the operator-created hotloaded rule in `software_icons_overrides.json` as the source of truth when asked to make an icon override official.
    - If the operator has not saved a rule yet, gather the needed values from the current software row in `GET /api/device/details/<hostname>` or from the Installed Software UI:
      - `name`
      - any verified icon resource path the operator tested manually
    - Candidate icon paths shown in the UI are heuristics, not proof. Verify the file/resource path before keeping it in the official JSON file.
    - If the rule already works in production, prefer updating the existing same-name rule instead of inventing a brand-new duplicate rule.

    ### Recommended authoring rules
    - Start with the exact `name` from the software row.
    - Do not add `version`, `publisher_contains_any`, `source`, or install-location match fields for icon overrides. Borealis intentionally keeps icon matching name-only so overrides survive normal version churn.
    - Reuse the existing rule ID shape `icon_override_<software_name_slug>`.

    ### Implementation references
    - Engine override loader: `Data/Engine/Containers/api-backend/data/services/API/devices/software_icons.py`
    - Agent application path: `Data/Agent/Roles/system_software_management.py`
    - Agent publishing role: `Data/Agent/Roles/role_system_software_management.py`
    - Installed Software UI operator action: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Installed_Software.jsx`
