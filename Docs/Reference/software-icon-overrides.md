# Software Icon Overrides

Software icon overrides are file-backed rules that fix missing or wrong installed-software icons.

## Override File

`Engine/Services/api-backend/config/software_icons_overrides.json`

## Operator Path

Use `Installed Software` row context menu first:

1. Open device `Installed Software`.
2. Right-click software name.
3. Select `Create Global Icon Override`.
4. Choose verified `.ico`, `.exe`, `.dll`, or resource path, or intentionally clear icon.
5. Save.
6. Use `Query Software Changes`.
7. Commit JSON rule later when it should become official.

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

Clear icon:

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

## Match Rule

Icon overrides match exact software `name`, case-insensitive. Version, publisher, source, and install location are intentionally ignored.

??? example "Detailed Codex Breakdown"

    ### Related documentation

    - [Software Management](../Using%20the%20Platform/software-management.md)
    - [API Reference](Data%20and%20Schema/api-reference.md)
    - [Software Uninstall Overrides](software-uninstall-overrides.md)
    - [Software Uninstall Blocklist](software-uninstall-blocklist.md)

    ### Implementation references

    - Engine override loader: `Data/Engine/Containers/api-backend/cmd/api-backend/agent_reads.go`
    - Runtime data file: `Engine/Services/api-backend/config/software_icons_overrides.json`
    - Installed Software UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Installed_Software.jsx`
    - Agent software role: `Data/Agent/internal/roles/software_management/`

    ### Behavior

    - First matching rule wins.
    - Agent applies matching metadata before icon extraction.
    - Engine reapplies matching icon metadata when serving device details.
    - Once a refreshed device publishes an override-derived icon asset, Engine can reuse that known icon hash for same-name rows fleet-wide.
