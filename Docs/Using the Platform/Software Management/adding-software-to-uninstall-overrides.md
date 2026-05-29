# Software Uninstall Overrides

Explain how to provide file-backed custom uninstall plans when Windows registry uninstall metadata cannot be trusted or is incomplete.

## Override File
- Path: `Data/Engine/Containers/api-backend/data/services/API/devices/software_uninstall_overrides.json`
- Runtime consumer: `Data/Engine/Containers/api-backend/data/services/API/devices/software_uninstall.py`

## Precedence
- Borealis checks uninstall overrides before it trusts registry `QuietUninstallString` metadata and before it applies the interactive-quiet-string blocklist.
- Use overrides when you already have a verified unattended command or other uninstall plan.

## Operator Hotload Workflow
1. Open the device `Installed Software` tab.
2. Right-click the software name.
3. Click `Create Global Uninstall Override`.
4. Enter the application path and any arguments Borealis should run for unattended uninstall.
5. Save the override.
6. Borealis writes the rule into `software_uninstall_overrides.json` and hotloads it immediately.
7. Test the uninstall from the same software row. If you also changed icon behavior, you can use `Query Software Changes` to force a fresh software snapshot.
8. Commit `software_uninstall_overrides.json` to Git later when the override should survive Engine image rebuilds and become an official shipped rule.

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

## Manual File Workflow
1. Open `software_uninstall_overrides.json`.
2. Add or update a rule under `windows_uninstall_overrides`.
3. Save the file.
4. Re-open the device details or retry the uninstall path; Borealis hotloads the file on the next uninstall-capability resolution.
5. Commit `software_uninstall_overrides.json` to Git when the override must survive Engine image rebuilds.

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

??? example "Detailed Codex Breakdown"

    ### Related documentation

    - [Device Management](../device-management.md)
    - [API Reference](../../Reference/Data%20and%20Schema/api-reference.md)
    - [Adding Software to Uninstall Blocklist](adding-software-to-uninstall-blocklist.md)
    - [Adding Software to Icon Overrides](adding-software-to-icon-overrides.md)

    ### Codex workflow
    - Prefer the operator-created hotloaded rule in `software_uninstall_overrides.json` as the source of truth when asked to make an uninstall override official.
    - If the operator has not created the rule yet, gather the current software row data from `GET /api/device/details/<hostname>` and any manually verified unattended uninstall command the operator tested.
    - Cross-check these values before finalizing a permanent rule:
      - `metadata.quiet_uninstall_string`
      - `metadata.uninstall_string`
      - `metadata.product_code`
      - `metadata.package_family_name`
      - the operator-provided verified executable path and arguments
    - Keep the override narrow enough to avoid accidentally matching unrelated versions or similarly named products.

    ### Strategy guidance
    - `direct_command`
      Best when you have a tested vendor command line.
    - `msi_product_code`
      Best when MSI metadata is the stable source of truth.
    - `windows_store`
      Best when Borealis needs to remove an AppX/Store package by family name.

    ### Implementation reference
    - Engine resolver: `Data/Engine/Containers/api-backend/data/services/API/devices/software_uninstall.py`
    - Override data file: `Data/Engine/Containers/api-backend/data/services/API/devices/software_uninstall_overrides.json`
