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
| REG-TEST-001 | Engine auth and Aegis | `Data/Engine/Unit_Tests/test_access_management_api.py` Aegis, GitHub token, credential, MFA, passkey, and password reset cases | open | Bounded pytest run found stale plaintext/envelope expectations, unauthorized responses after Aegis setup, and passkey lookup behavior mismatches. | Decide whether implementation regressed or tests must assert Aegis envelopes and lookup HMAC rows. Do not delete coverage. |
| REG-TEST-002 | Agent role loading | `Data/Engine/Unit_Tests/test_agent_role_manager.py::test_role_manager_adds_project_data_agent_to_import_path` | open | RoleManager did not load the synthetic runtime-path probe role in the sampled run. | Confirm runtime import-path contract, then fix loader or fixture. |
| REG-TEST-003 | Engine WebUI unit lane | `Engine_Unit_Tests.sh` WebUI Vitest step | environment-gap | Current runtime tree does not contain `Engine/web-interface/Unit_Tests` until Engine is redeployed from this branch. Prior source Vitest run also hung without a summary. | Redeploy runtime, rerun with bounded timeout, then fix any open handle if Vitest still hangs. |
| REG-TEST-004 | Agent WireGuard role tests | `Data/Agent/Unit_Tests/test_role_wireguard_tunnel.py` ensure-cycle cases | fixed | Formalization run exposed stale `Role.__new__` fixtures missing newer Agent status hooks. | Fixtures now install the hooks they exercise; keep hook setup in sync with role constructor changes. |
| REG-TEST-005 | Engine software inventory tests | `Data/Engine/Unit_Tests/test_devices_api.py` software metadata and uninstall override cases | fixed | Validation exposed tests depending on live icon override, uninstall override, and uninstall blocklist JSON. | Tests now use shared config isolation helpers; only load operator JSON when a test explicitly covers hotload behavior. |

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
