# Software Uninstall Blocklist

Software uninstall blocklist rules stop Borealis from trusting uninstall metadata that claims to be quiet but still prompts, hangs, or behaves unsafely.

## Blocklist File

`Engine/Services/api-backend/config/software_uninstall_blocklist.json`

## Operator Path

Use `Installed Software` row context menu first:

1. Open device `Installed Software`.
2. Right-click software name.
3. Select `Block Uninstallation`.
4. Enter reason other operators should see.
5. Save.
6. Create an uninstall override later when a verified unattended command exists.
7. Commit JSON rule later when it should become official.

## JSON Shape

```json
{
  "windows_quiet_uninstall_blocklist": [
    {
      "rule_id": "vendor_app_quiet_string_prompts",
      "source": "local_installed",
      "name_contains_any": ["Vendor App"],
      "publisher_contains_any": ["Vendor Inc"],
      "exe_names": ["uninstall.exe"],
      "quiet_args_any": ["/s"],
      "reason": "Vendor App's registered QuietUninstallString still opens an interactive confirmation prompt."
    }
  ]
}
```

## When To Block

- Registered quiet uninstall still prompts.
- Vendor installer ignores quiet flags.
- Uninstall hangs in unattended execution.
- Borealis should refuse automation until a verified override exists.

??? example "Detailed Codex Breakdown"

    ### Related documentation

    - [Software Management](../Using%20the%20Platform/software-management.md)
    - [Software Uninstall Overrides](software-uninstall-overrides.md)
    - [Software Icon Overrides](software-icon-overrides.md)
    - [API Reference](Data%20and%20Schema/api-reference.md)

    ### Implementation references

    - Engine resolver: `Data/Engine/Containers/api-backend/cmd/api-backend/software_overrides.go`
    - Runtime data file: `Engine/Services/api-backend/config/software_uninstall_blocklist.json`
    - Installed Software UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Installed_Software.jsx`

    ### Behavior

    - Use blocklist when current quiet metadata is unsafe and no verified replacement exists.
    - Use uninstall override when replacement command is known.
    - Overrides are checked before blocklist, so a verified override can intentionally replace a blocked quiet string.
