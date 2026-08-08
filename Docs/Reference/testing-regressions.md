# Borealis Testing Regressions

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
| REG-TEST-002 | Legacy Python Agent role and wrapper tests | `Data/Engine/Unit_Tests/test_agent_role_manager.py` and `Data/Engine/Unit_Tests/assemblies/test_agent_powershell_wrapper.py` | stale-test | Legacy Python RoleManager and Agent role modules were removed after Go Agent parity. PowerShell wrapper preamble coverage moved to `Data/Agent/internal/scripts/scripts_test.go`. | Do not reintroduce Python Agent role coverage; add Go runtime role-registry or script-wrapper tests under `Data/Agent/internal` when role wiring changes. |
| REG-TEST-003 | Engine WebUI unit lane | `Engine_Unit_Tests.sh` WebUI Vitest step | fixed | Runtime WebUI lane passes under bounded timeout. Stale Agent Health, route guard, and AppShell assertions were updated for current UI behavior. | Keep WebUI tests under `Unit_Tests/**/*.test.jsx` and preserve bounded Vitest execution. |
| REG-TEST-004 | Agent WireGuard role tests | `Data/Agent/internal/roles/wireguard_tunnel/wireguard_tunnel_test.go` | fixed | Go WireGuard tests cover session normalization, Windows service naming, firewall port arrays, Linux `wg-quick` apply, readiness payloads, and signed token trust persistence. | Keep WireGuard service/config/readiness tests aligned with real Windows and Linux tunnel behavior. |
| REG-TEST-005 | Engine software inventory tests | `Data/Engine/Containers/api-backend/cmd/api-backend/software_test.go`, `agent_details_test.go`, and `agent_reads_test.go` | fixed | Validation exposed tests depending on live icon override, uninstall override, and uninstall blocklist JSON before Go API cutover. Go tests now cover software inventory metadata and rule-file isolation. | Keep software metadata, icon override, uninstall override, and uninstall blocklist coverage isolated from operator runtime JSON unless a test explicitly covers hotload behavior. |
| REG-TEST-006 | Engine RBAC and filters | `Data/Engine/Containers/api-backend/cmd/api-backend/device_filters_test.go`, `user_site_assignments_test.go`, and `users_test.go` | fixed | Full Engine validation exposed filter reload and usage-conflict checks running without the current actor plus stale site-scope tuple unpacking before Go API cutover. | Keep filter visibility and site-scoped user access tests aligned with current actor and allowed-site behavior. |
| REG-TEST-007 | Engine runner isolation | `Engine_Unit_Tests.sh` Engine Python lane | fixed | Single-process pytest run exceeded the lane timeout after slow API tests and process-global state leaked between files. Runner now executes Engine Python test files in isolated pytest processes with per-file timeouts. | Preserve all-test execution while keeping file-level isolation and useful per-file logs. |
| REG-TEST-008 | Engine K3s/Go unit domains | `Engine_Unit_Tests.sh` Engine Go API lane | fixed | K3s and Go API migrations left active domain names pointing at deleted Python tests. Runner domains now execute current Go API coverage and retained Python tests, while deleted `agent-role` and `runtime-overrides` domains are no longer public runner options. | Keep `Engine_Unit_Tests.sh --list-domains` aligned with current test files and update `Docs/Reference/Unit_Testing.md` when domain membership changes. |

??? example "Detailed Codex Breakdown"

    ### Related documentation

    - [Unit Testing](Unit_Testing.md)
    - [Engine Runtime](../Reference/Core%20Runtimes/engine-runtime.md)
    - [Agent Runtime](../Reference/Core%20Runtimes/agent-runtime.md)
    - [Security Whitepaper](security-whitepaper.md)

    - Add a row when introducing a regression test for a bug found in production, operator validation, or a PR review.
    - If a formalization run exposes a failure that predates the cleanup, record it here before changing or quarantining the test.
    - Prefer fixing stale expectations over marking tests skipped.
    - If a skip or xfail is necessary, include the regression ID in the marker reason.
