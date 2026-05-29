# Software Uninstall Overrides

Software uninstall overrides are file-backed rules that provide verified unattended uninstall plans when registry uninstall metadata is incomplete or unsafe.

## Override File

`Data/Engine/Containers/api-backend/data/services/API/devices/software_uninstall_overrides.json`

## Operator Path

Use `Installed Software` row context menu first:

1. Open device `Installed Software`.
2. Right-click software name.
3. Select `Create Global Uninstall Override`.
4. Enter verified application path and arguments.
5. Save.
6. Test uninstall from same row.
7. Commit JSON rule later when it should become official.

## JSON Shape

```json
{
  "windows_uninstall_overrides": [
    {
      "rule_id": "uninstall_override_contoso_agent",
      "source": "local_installed",
      "name": "Contoso Agent",
      "version": "2.4.1",
      "publisher_contains_any": ["Contoso Ltd"],
      "strategy": "direct_command",
      "quiet_uninstall_string": "\"C:\\Program Files\\Contoso Agent\\uninstall.exe\" /S",
      "summary": "Uses a verified Contoso unattended uninstall command."
    }
  ]
}
```

## Strategies

- `direct_command` uses a verified quiet command.
- `msi_product_code` uses MSI product code.
- `windows_store` removes AppX/Store package by family name.

## Precedence

Overrides win before Borealis trusts registry `QuietUninstallString` metadata and before the blocklist applies.

??? example "Detailed Codex Breakdown"

    ### Related documentation

    - [Software Management](../Using%20the%20Platform/software-management.md)
    - [Software Uninstall Blocklist](software-uninstall-blocklist.md)
    - [Software Icon Overrides](software-icon-overrides.md)
    - [API Reference](Data%20and%20Schema/api-reference.md)

    ### Implementation references

    - Engine resolver: `Data/Engine/Containers/api-backend/data/services/API/devices/software_uninstall.py`
    - Override data file: `Data/Engine/Containers/api-backend/data/services/API/devices/software_uninstall_overrides.json`
    - Installed Software UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Installed_Software.jsx`

    ### Authoring notes

    - Keep overrides narrow enough to avoid matching unrelated products.
    - Prefer product code when MSI metadata is stable.
    - Prefer direct command when vendor quiet command is manually verified.
    - Preserve operator-facing `summary` so future operators understand why rule exists.
