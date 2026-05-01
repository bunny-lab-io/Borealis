# Borealis Testing Regressions
[Back to Docs Index](index.md) | [Index (HTML)](index.html)

## Purpose
Track unit tests that protect prior regressions or currently fail during test formalization.

## Status Labels
- `open`: failure still reproduces.
- `fixed`: test passes and protects the repaired behavior.
- `stale-test`: product behavior changed and the test expectation needs updating.
- `environment-gap`: test needs a dependency, runtime copy, OS feature, or service not present in the current runner.
- `flaky`: test sometimes passes and sometimes fails without code changes.

## Current Formalization Baseline
Baseline sampled on April 30, 2026 from branch `feature/unit-test-formalization`.

| ID | Area | Test or Lane | Status | Notes | Cleanup Rule |
| --- | --- | --- | --- | --- | --- |
| REG-TEST-001 | Engine auth and Aegis | `Data/Engine/Unit_Tests/test_access_management_api.py` Aegis, GitHub token, credential, MFA, passkey, and password reset cases | fixed | Tests now assert Aegis envelopes and passkey lookup HMAC rows. Engine adapter now passes the lowercase `secret_key` config into Aegis HMAC setup. | Keep tests aligned with encrypted-at-rest auth storage. Do not reintroduce plaintext passkey or MFA expectations. |
| REG-TEST-002 | Agent role loading | `Data/Engine/Unit_Tests/test_agent_role_manager.py::test_role_manager_adds_project_data_agent_to_import_path` | fixed | Test now isolates cached `runtime_paths` imports so prior Agent role imports cannot hide the synthetic source path. | Keep RoleManager import-path tests isolated from process-global `sys.modules` and `sys.path` state. |
| REG-TEST-003 | Engine WebUI unit lane | `Engine_Unit_Tests.sh` WebUI Vitest step | fixed | Runtime WebUI lane passes under bounded timeout. Stale Agent Health, route guard, and AppShell assertions were updated for current UI behavior. | Keep WebUI tests under `Unit_Tests/**/*.test.jsx` and preserve bounded Vitest execution. |
| REG-TEST-004 | Agent WireGuard role tests | `Data/Agent/Unit_Tests/test_role_wireguard_tunnel.py` ensure-cycle cases | fixed | Formalization run exposed stale `Role.__new__` fixtures missing newer Agent status hooks. | Fixtures now install the hooks they exercise; keep hook setup in sync with role constructor changes. |
| REG-TEST-005 | Engine software inventory tests | `Data/Engine/Unit_Tests/test_devices_api.py` software metadata and uninstall override cases | fixed | Validation exposed tests depending on live icon override, uninstall override, and uninstall blocklist JSON. | Tests now use shared config isolation helpers; only load operator JSON when a test explicitly covers hotload behavior. |
| REG-TEST-006 | Engine RBAC and filters | `Data/Engine/Unit_Tests/test_device_filters_api.py` and `Data/Engine/Unit_Tests/test_rbac_api.py` | fixed | Full Engine validation exposed filter reload and usage-conflict checks running without the current actor plus stale site-scope tuple unpacking. | Keep create/update/archive/delete filter paths passing the current user into visibility and usage checks. |
| REG-TEST-007 | Engine runner isolation | `Engine_Unit_Tests.sh` Engine Python lane | fixed | Single-process pytest run exceeded the lane timeout after slow API tests and process-global state leaked between files. Runner now executes Engine Python test files in isolated pytest processes with per-file timeouts. | Preserve all-test execution while keeping file-level isolation and useful per-file logs. |

## Codex Agent
- Add a row when introducing a regression test for a bug found in production, operator validation, or a PR review.
- If a formalization run exposes a failure that predates the cleanup, record it here before changing or quarantining the test.
- Prefer fixing stale expectations over marking tests skipped.
- If a skip or xfail is necessary, include the regression ID in the marker reason.

## Related Documentation
- [Testing](testing.md)
- [Engine Runtime](engine-runtime.md)
- [Agent Runtime](agent-runtime.md)
- [Security and Trust](security-and-trust.md)
