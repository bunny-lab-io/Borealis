# Software Uninstall Overrides
[Back to Docs Index](../index.md) | [Index (HTML)](../index.html)

## Purpose
Explain how to provide file-backed custom uninstall plans when Windows registry uninstall metadata cannot be trusted or is incomplete.

## Override File
- Path: `Data/Engine/services/API/devices/software_uninstall_overrides.json`
- Runtime consumer: `Data/Engine/services/API/devices/software_uninstall.py`

## Precedence
- Borealis checks uninstall overrides before it trusts registry `QuietUninstallString` metadata and before it applies the interactive-quiet-string blocklist.
- Use overrides when you already have a verified unattended command or other uninstall plan.

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

## Common Match Fields
- `source`
  Exact normalized Borealis source such as `local_installed`.
- `name`
  Exact case-insensitive software-name match.
- `version`
  Exact case-insensitive version match.
- `product_code`
  Exact MSI product code match when that is the most stable identifier.
- `publisher_contains_any`
  Optional substring list matched against the publisher.
- `name_contains_any`
  Optional substring list matched against the software name.
- `install_location_contains_any`
  Optional substring list matched against `metadata.install_location`.
- `exe_names`
  Optional executable-name list matched against the parsed uninstall command.
- `uninstall_contains_any`
  Optional substring list matched against the uninstall command text.
- `quiet_args_any`
  Optional switch list matched against parsed quiet-command arguments.

## Supported Plan Fields
- `strategy = direct_command`
  Requires `quiet_uninstall_string`.
- `strategy = msi_product_code`
  Requires `product_code`.
- `strategy = windows_store`
  Requires `package_family_name`.
- `summary`
  Operator-facing explanation shown in Borealis UI payloads.
- `rule_id`
  Stable identifier for logs, tests, and future debugging.
- `uninstall_string`
  Optional original uninstall string to preserve alongside the override.

## Fast Workflow
1. In the Installed Software tab, right-click the software name.
2. Click `Display software debug information`.
3. Use the copy icon in the top-right of the dialog, or manually select and copy the JSON payload.
4. Review `software.uninstall_capability`, `software.uninstall_command_preview`, and `suggested_entries.uninstall_override`.
5. Paste the suggested entry into `software_uninstall_overrides.json`.
6. Replace the command with the verified unattended uninstall command you tested manually.

## Example
```json
{
  "windows_uninstall_overrides": [
    {
      "rule_id": "uninstall_override_fedora_media_writer",
      "source": "local_installed",
      "name": "Fedora Media Writer",
      "version": "5.2.8",
      "publisher_contains_any": ["Fedora Project"],
      "strategy": "direct_command",
      "quiet_uninstall_string": "\"C:\\Program Files\\Fedora Media Writer\\uninstall.exe\" remove --confirm-command",
      "summary": "Uses a verified Fedora Media Writer unattended uninstall command override."
    }
  ]
}
```

## Related Documentation
- [Device Management](../device-management.md)
- [API Reference](../api-reference.md)
- [Adding Software to Uninstall Blocklist](adding-software-to-uninstall-blocklist.md)
- [Adding Software to Icon Overrides](adding-software-to-icon-overrides.md)

## Codex Agent (Detailed)
### How to read copied software debug payloads
- Prefer the `suggested_entries.uninstall_override` object as the starting point.
- Cross-check these fields before saving:
  - `software.metadata.quiet_uninstall_string`
  - `software.metadata.uninstall_string`
  - `software.metadata.product_code`
  - `software.metadata.package_family_name`
  - `software.uninstall_command_preview`
  - `codex_notes.parsed_commands`
- Keep the override narrow enough to avoid accidentally matching unrelated versions or similarly named products.

### Strategy guidance
- `direct_command`
  Best when you have a tested vendor command line.
- `msi_product_code`
  Best when MSI metadata is the stable source of truth.
- `windows_store`
  Best when Borealis needs to remove an AppX/Store package by family name.

### Implementation reference
- Engine resolver: `Data/Engine/services/API/devices/software_uninstall.py`
- Override data file: `Data/Engine/services/API/devices/software_uninstall_overrides.json`
