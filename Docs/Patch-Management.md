# Centralized Windows Patch Management
[Back to Docs Index](index.md) | [Index (HTML)](website/index.html)

## Purpose
Document Borealis Windows patch management across Agent, Engine API, database, and WebUI.

## Scope
- V1 targets Windows 10, Windows 11, and Windows Server.
- Borealis manages Microsoft Windows updates through Windows Update Agent (WUA).
- Third-party application patching is out of scope.
- One compiled Go Agent executable owns scan, download, install, policy refresh, reboot handling, and interactive user prompting. No PSWindowsUpdate module, sidecar updater, or second service is introduced.

## Agent Design
- Source role: `Data/Agent/internal/roles/patch_management`.
- Runtime context: SYSTEM only for WUA COM access, download, install, and reboot.
- Windows adapter uses WUA COM through `github.com/go-ole/go-ole`.
- Non-Windows adapter reports unsupported state without failing rest of Agent.
- Socket.IO event: `patch_management_request`.
- Supported actions:
  - `scan` - run immediate WUA scan and upload report.
  - `install` - refresh policy, scan, install approved and unheld updates, then upload report.
  - `policy_refresh` - fetch current effective policy and holds.
  - `reboot` - request local reboot through Windows shutdown tooling.
- Role health row: `system:patch_management`, with last scan, missing count, failed count, pending reboot, last error/HRESULT, and policy version.

## WUA Behavior
- Scan criteria: missing and non-hidden updates through `IUpdateSearcher::Search`.
- Inventory fields include update ID, revision, KBs, title, classification, categories, severity, update type, size, support URL, installed/downloaded/hidden state, HRESULT, result code, and reboot flags.
- Install flow batches approved updates first. If batch install fails, Agent retries each selected update individually so Catalog and device state show exact failed update.
- Pending reboot uses WUA install result plus local Windows pending-reboot indicators.
- Agent sends reports to Engine after scans and installs.

## Policy Model
- Effective policy is single-source, no merging:
  - device binding wins first
  - site binding wins second
  - global binding wins last
- API payload includes `effective_reason` so UI can show why policy won.
- Policy class toggles cover security, critical, cumulative, definition, driver, feature, optional, service pack, update rollup, and general updates.
- Manual install actions still honor policy class toggles and active holds. Manual actions only bypass schedule timing.
- Reboot policy includes maintenance window, user prompt enablement, and deferral deadline hours.

## Hold Model
- Global hold blocks update everywhere.
- Policy hold blocks only devices using that policy.
- Release marks matching holds released.
- There is no hidden allow override that can bypass a hold.

## Engine API
- Device-auth endpoints:
  - `POST /api/agent/patch-management/policy`
  - `POST /api/agent/patch-management/report`
- Operator endpoints:
  - `GET /api/patch-management/catalog`
  - `GET /api/patch-management/policies`
  - `POST /api/patch-management/policies`
  - `POST /api/patch-management/catalog/hold`
  - `POST /api/patch-management/catalog/release`
  - `GET /api/patch-management/devices`
  - `GET /api/patch-management/history`
  - `GET /api/device/patches/<hostname>`
  - `POST /api/device/patches/<hostname>/action`
- Operator endpoints follow existing site RBAC. Admin sees all sites; scoped operators see assigned sites only.

## Database Tables
- `patch_policies` stores policy definitions.
- `patch_policy_bindings` stores global, site, and device policy assignment.
- `patch_catalog` stores fleet-wide update metadata by update ID and revision.
- `device_patch_state` stores per-device update status.
- `patch_holds` stores global or policy scoped holds and release metadata.
- `patch_action_history` stores scan, install, reboot, and dispatch history.
- `patch_reboot_deferrals` stores operator/user deferrals and deadlines.

## WebUI
- Main route: `/patch-management`.
- Sidebar placement: Automation > Patch Management > Patch Management, below Scheduled Jobs and above Watchdogs.
- Main page tabs:
  - Patch Catalog
  - Device Compliance
  - Policies
  - Run History
- Patch Catalog prioritizes fleet-wide KB/update rows with affected device count, class, hold state, installed/missing/failed/pending reboot counts, and last-seen age.
- Catalog v1 actions: hold or release globally. Policy-scoped hold support is API-backed and can be exposed by UI iteration.
- Device Summary adds Patch Management tab with effective policy, missing updates, install history, pending reboot state, last scan, and manual scan/install/reboot actions.

## Reboot UX
- Servers and devices without interactive sessions reboot only inside policy window or at deadline.
- Interactive Windows users are handled through same Agent helper/tray path used by current-user support.
- Helper/tray presents reboot prompt, countdown, defer choices, and deadline state.

## Test Coverage
- Agent unit tests cover policy filtering, hold filtering, batch failure fallback, reboot window math, non-Windows unsupported behavior, and action parsing.
- Engine unit tests cover default policy bootstrap, effective-policy precedence, report ingestion, Catalog aggregation, hold/release, and socket dispatch history.
- Run Agent tests with `Data/Agent/Unit_Tests/Agent_Unit_Tests.sh` or `.ps1`.
- Run Engine affected domain with `./Engine_Unit_Tests.sh --domain devices`.

## Related Documentation
- [Agent Runtime](Core%20Runtimes/agent-runtime.md)
- [Device Management](Operations%20and%20Remote%20Access/device-management.md)
- [API Reference](Data%20and%20Schema/api-reference.md)
- [Database Reference](Data%20and%20Schema/db-reference.md)
- [UI and Notifications](Start%20Here/ui-and-notifications.md)
- [SBOM](SBOM.md)

## Codex Agent
- Keep Agent changes inside `Data/Agent`; runtime `Agent/` is generated.
- Keep Engine API and WebUI changes inside `Data/Engine`; runtime `Engine/` is generated.
- Preserve one `Agent.exe` architecture. New patch behavior must compile into the existing Go Agent binary.
- Update `Docs/SBOM.md` whenever WUA wrapper dependencies change.
- Avoid policy merge logic. If precedence becomes ambiguous, fix API/UI explanation instead of merging policy fragments.
