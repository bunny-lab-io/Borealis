# Software Uninstall Blocklist
[Back to Docs Index](../index.md) | [Index (HTML)](../index.html)

## Purpose
Explain how to block installed software whose registry-provided `QuietUninstallString` still prompts, hangs, or otherwise cannot be trusted for unattended Borealis uninstall.

## Blocklist File
- Path: `Data/Engine/services/API/devices/software_uninstall_blocklist.json`
- Runtime consumer: `Data/Engine/services/API/devices/software_uninstall.py`

## JSON Shape
```json
{
  "windows_quiet_uninstall_blocklist": [
    {
      "rule_id": "fedora_media_writer_quiet_string_interactive",
      "source": "local_installed",
      "publisher_contains_any": ["fedora project"],
      "name_contains_any": ["fedora media writer"],
      "exe_names": ["uninstall.exe"],
      "quiet_args_any": ["/s"],
      "reason": "Fedora Media Writer's registered QuietUninstallString still prompts for confirmation."
    }
  ]
}
```

## Supported Match Fields
- `source`
  Exact normalized Borealis source such as `local_installed`.
- `publisher_contains_any`
  Optional substring list matched against the publisher.
- `name_contains_any`
  Optional substring list matched against the software name.
- `exe_names`
  Optional executable-name list matched against the parsed `QuietUninstallString`.
- `quiet_args_any`
  Optional switch list that must appear in the parsed quiet command arguments.

## Required Outcome Field
- `reason`
  Operator-facing explanation returned by Borealis when uninstall is blocked.

## When To Use The Blocklist
- The software claims to have a `QuietUninstallString`, but manual testing still shows prompts.
- The vendor installer ignores or misinterprets its quiet switches.
- Borealis should refuse automation until a verified override command is known.

## Fast Workflow
1. In the Installed Software tab, right-click the software name.
2. Click `Display software debug information`.
3. Use the copy icon in the top-right of the dialog, or manually select and copy the JSON payload.
4. Inspect `software.metadata.quiet_uninstall_string` and `suggested_entries.uninstall_blocklist`.
5. Paste the suggested blocklist entry into `software_uninstall_blocklist.json`.
6. Replace the placeholder `reason` text with the real observed failure mode.

## Example
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

## Related Documentation
- [Device Management](../device-management.md)
- [API Reference](../api-reference.md)
- [Adding Software to Uninstall Overrides](adding-software-to-uninstall-overrides.md)
- [Adding Software to Icon Overrides](adding-software-to-icon-overrides.md)

## Codex Agent (Detailed)
### How to decide between blocklist vs override
- Use the blocklist when the copied debug payload proves the existing quiet metadata is unsafe and no verified unattended replacement command is available yet.
- Use `software_uninstall_overrides.json` instead when a tested custom command is already known.
- Uninstall overrides are checked before the blocklist, so a verified override can intentionally replace an otherwise blocked registry quiet string.

### Preferred fields from copied debug payload
- `software.name`
- `software.version`
- `software.source`
- `software.metadata.publisher`
- `software.metadata.quiet_uninstall_string`
- `codex_notes.parsed_commands.quiet_uninstall`
- `suggested_entries.uninstall_blocklist`

### Implementation reference
- Engine resolver: `Data/Engine/services/API/devices/software_uninstall.py`
- Blocklist data file: `Data/Engine/services/API/devices/software_uninstall_blocklist.json`
