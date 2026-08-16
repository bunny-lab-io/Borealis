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
| REG-TEST-002 | Legacy Python Agent role loading | `Data/Engine/Unit_Tests/test_agent_role_manager.py` | stale-test | Legacy Python RoleManager source and tests were removed after Go Agent parity. | Do not reintroduce Python RoleManager coverage; add Go runtime role-registry tests under `Data/Agent/internal` when role wiring changes. |
| REG-TEST-003 | Engine WebUI unit lane | `Engine_Unit_Tests.sh` WebUI Vitest step | fixed | Runtime WebUI lane passes under bounded timeout. Stale Agent Health, route guard, and AppShell assertions were updated for current UI behavior. | Keep WebUI tests under `Unit_Tests/**/*.test.jsx` and preserve bounded Vitest execution. |
| REG-TEST-004 | Agent WireGuard role tests | `Data/Agent/internal/roles/wireguard_tunnel/wireguard_tunnel_test.go` | fixed | Go WireGuard tests cover session normalization, Windows service naming, firewall port arrays, Linux `wg-quick` apply, missing `wireguard-tools` repair through Rocky/RHEL `dnf`, dependency retry cooldown, authenticated endpoint fallback, handshake-aware health/readiness, and signed token trust persistence. | Keep WireGuard service/config/dependency/handshake/readiness tests aligned with real Windows and Linux tunnel behavior. |
| REG-TEST-005 | Engine software inventory tests | `Data/Engine/Unit_Tests/test_devices_api.py` software metadata and uninstall override cases | fixed | Validation exposed tests depending on live icon override, uninstall override, and uninstall blocklist JSON. | Tests now use shared config isolation helpers; only load operator JSON when a test explicitly covers hotload behavior. |
| REG-TEST-006 | Engine RBAC and filters | `Data/Engine/Unit_Tests/test_device_filters_api.py` and `Data/Engine/Unit_Tests/test_rbac_api.py` | fixed | Full Engine validation exposed filter reload and usage-conflict checks running without the current actor plus stale site-scope tuple unpacking. | Keep create/update/archive/delete filter paths passing the current user into visibility and usage checks. |
| REG-TEST-007 | Engine runner isolation | `Engine_Unit_Tests.sh` Engine Python lane | fixed | Single-process pytest run exceeded the lane timeout after slow API tests and process-global state leaked between files. Runner now executes Engine Python test files in isolated pytest processes with per-file timeouts. | Preserve all-test execution while keeping file-level isolation and useful per-file logs. |
| REG-TEST-008 | Scheduled Ansible on K3s | `Data/Engine/Unit_Tests/test_ansible_runner.py::test_runner_bounds_concurrent_controller_processes` and `TestSchedulerInternalJSONHonorsRequestTimeoutBeyondSharedClientTimeout` | fixed | Job 372 exposed 30-second HTTP cutoff inside 45-second WireGuard readiness window plus eight simultaneous Ansible controllers exhausting 512 MiB site-worker cgroup. | Keep request-specific readiness deadlines authoritative and bound controller processes independently from scheduled work-item slots. |
| REG-TEST-009 | Agent binary hot-publish and worker cutover | `Data/Engine/Unit_Tests/test_engine_launcher.py`, `test_stale_worker_exit_does_not_retire_replacement_registration`, and `TestBorealisOperatorLaunchSiteWorkerBuildsSafePod` | fixed | LAB-TRAEFIK-01 redeploy reused stale Engine Agent cache. Scoped command now publishes checked-out Agent source and replaces outdated site workers through readiness-first stable-Service cutover. | Keep candidate readiness and Service health ahead of old-pod deletion. Preserve pre-commit selector rollback and incarnation-aware worker stop. |
| REG-TEST-010 | Scheduled Ansible connection admission | `TestScheduledJobAggregationShowsConnectionProbeDeadline`, `TestScheduledConnectionProbeUsesSixtySecondWindow`, `Create_Job.connectionProbeStatus.test.jsx`, `TestVPNSessionPayloadIncludesEngineWireGuardFallback`, and Agent WireGuard fallback/health tests | environment-gap | Jobs 380/381 skipped LAB-TRAEFIK-01 with `no_handshake` while Agent incorrectly reported Healthy from interface presence. Live Engine peer had no endpoint, handshake, or traffic because LAB-TRAEFIK-01 also owned public UDP forwarding path. Coverage now protects 60-second admission countdown plus authenticated private endpoint recovery and handshake-aware Agent health; deployed LAB rerun remains pending. | Preserve persisted connection deadline, one-second UI derivation, non-wrapping status pills, lowercase seconds suffix, 60-second SSH/WinRM window, active-run handling, and handshake-proven VPN readiness. Rerun WebUI lane and live LAB job after Engine/Agent rollout, then mark fixed. |

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
