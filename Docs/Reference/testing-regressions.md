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
| REG-TEST-001 | Engine auth and Aegis | Go `auth_*_test.go`, `aegis_*_test.go`, `credentials_test.go`, and `TestWebUILeavesAuthCookieManagementToGoBackend` | fixed | Go tests assert secure HttpOnly cookies, Aegis envelopes, credential storage, MFA, passkey, and password-reset behavior. Retired Python source scans were removed. | Keep tests aligned with encrypted-at-rest auth storage. Do not reintroduce plaintext passkey, MFA, or browser-written auth cookies. |
| REG-TEST-002 | Legacy Python Agent role loading | `Data/Engine/Unit_Tests/test_agent_role_manager.py` | stale-test | Legacy Python RoleManager source and tests were removed after Go Agent parity. | Do not reintroduce Python RoleManager coverage; add Go runtime role-registry tests under `Data/Agent/internal` when role wiring changes. |
| REG-TEST-003 | Engine WebUI unit lane | `Engine_Unit_Tests.sh` WebUI Vitest step | fixed | Runtime WebUI lane passes under bounded timeout. Stale Agent Health, route guard, and AppShell assertions were updated for current UI behavior. | Keep WebUI tests under `Unit_Tests/**/*.test.jsx` and preserve bounded Vitest execution. |
| REG-TEST-004 | Agent WireGuard role tests | `Data/Agent/internal/roles/wireguard_tunnel/wireguard_tunnel_test.go` | fixed | Go WireGuard tests cover session normalization, Windows service naming, firewall port arrays, Linux `wg-quick` apply, missing `wireguard-tools` repair through Rocky/RHEL `dnf`, dependency retry cooldown, authenticated endpoint fallback, handshake-aware health/readiness, and signed token trust persistence. | Keep WireGuard service/config/dependency/handshake/readiness tests aligned with real Windows and Linux tunnel behavior. |
| REG-TEST-005 | Engine software inventory tests | `Data/Engine/Containers/api-backend/cmd/api-backend/software_test.go` | fixed | Validation exposed tests depending on live icon override, uninstall override, and uninstall blocklist JSON. | Keep override paths isolated with test-owned temporary files; load operator JSON only when a test explicitly covers hotload behavior. |
| REG-TEST-006 | Engine RBAC and filters | `Data/Engine/Containers/api-backend/cmd/api-backend/device_filters_test.go` | fixed | Full Engine validation exposed filter reload and usage-conflict checks running without current actor plus stale site-scope handling. | Keep filter create/update/archive/delete visibility and usage contracts in Go API tests. |
| REG-TEST-007 | Site-worker runner isolation | `Engine_Unit_Tests.sh` site-worker Python lane | fixed | Single-process pytest run exceeded lane timeout and process-global worker state leaked between files. Runner executes current site-worker Python files in isolated pytest processes with per-file timeouts. | Preserve all site-worker test execution while keeping file-level isolation and useful per-file logs. Go-owned behavior must stay in Go suites. |
| REG-TEST-008 | Scheduled Ansible on K3s | `Data/Engine/Unit_Tests/test_ansible_runner.py::test_runner_bounds_concurrent_controller_processes` and `TestSchedulerInternalJSONHonorsRequestTimeoutBeyondSharedClientTimeout` | fixed | Job 372 exposed 30-second HTTP cutoff inside 45-second WireGuard readiness window plus eight simultaneous Ansible controllers exhausting 512 MiB site-worker cgroup. | Keep request-specific readiness deadlines authoritative and bound controller processes independently from scheduled work-item slots. |
| REG-TEST-009 | Agent binary hot-publish and worker cutover | `TestEngineAgentBinaryRedeployKeepsOldWorkersUntilHealthCutover`, `test_stale_worker_exit_does_not_retire_replacement_registration`, and `TestBorealisOperatorLaunchSiteWorkerBuildsSafePod` | fixed | LAB-TRAEFIK-01 redeploy reused stale Engine Agent cache. Go contract tests now protect readiness-first stable-Service cutover and pre-commit rollback ordering. | Keep candidate readiness and Service health ahead of old-pod deletion. Preserve pre-commit selector rollback and incarnation-aware worker stop. |
| REG-TEST-010 | Scheduled Ansible connection admission | `TestScheduledJobAggregationShowsConnectionProbeDeadline`, `TestScheduledConnectionProbeUsesSixtySecondWindow`, `Create_Job.connectionProbeStatus.test.jsx`, `TestVPNSessionPayloadIncludesEngineWireGuardFallback`, `TestEngineIPFallbackIsResolvedForEveryNetworkMode`, and Agent WireGuard fallback/health tests | environment-gap | Jobs 380/381 and first two Job 382 runs skipped LAB-TRAEFIK-01 with `no_handshake`. Public-mode `write_compose_env` resolved Engine host fallback only inside Internal-Only CA branch, leaving deployed API fallback blank. Assignment now runs before profile-specific CA work. After API rollout, Agent selected `192.168.3.252:30000`, reported fresh handshakes, and Job 382 run 47960 completed `Success` with Ansible `pong`, `unreachable=0`, and `failed=0`. | Preserve persisted connection deadline, one-second UI derivation, non-wrapping status pills, lowercase seconds suffix, mutually exclusive live Job History status Filter Sliders, full-height target grid, 60-second SSH/WinRM window, active-run handling, public/local fallback emission, and handshake-proven VPN readiness. Restore deployed WebUI `Unit_Tests`, rerun WebUI lane, then mark fixed. |
| REG-TEST-011 | Privileged WireGuard control boundary | `Data/Engine/Containers/api-backend/internal/wireguardcontrol/server_test.go` | fixed | Python control proxy and its pytest were ported together. Go tests preserve exact listener, peer, route, and firewall command shapes and reject broad networks, Engine-address reuse, arbitrary commands, and symlink escapes. | Keep privileged command allowlist narrow. Any new command shape needs direct allow and reject tests plus security-document update. |
| REG-TEST-012 | Go route test evidence | `APIRouteEvidenceTests` and `Tests/policy/check_api_routes.py` | fixed | PR review found route generator auto-assigned companion test files without proof author reviewed route coverage. Generator now preserves committed route-specific evidence and leaves new routes empty so policy fails until focused test or reviewed exemption is recorded. | Never infer new-route coverage from companion filename alone. |
| REG-TEST-013 | K3s hostPath boundary | `K3sHostPathPolicyTests` | fixed | PR review found string-prefix comparison allowed sibling paths such as `secrets-copy` through `secrets` allowlist. Policy now accepts only exact directory or slash-delimited descendant. | Preserve path-component boundary for every fixed hostPath prefix. |
| REG-TEST-014 | Full portable container lane | `PortableRunnerContractTests` | fixed | PR review found clean-worktree `run-all.sh` selected zero container images. Full runner now calls explicit `run-containers.sh --all` mode and builds every production manifest image. | Keep worktree-aware default for narrow iteration; keep `run-all.sh` explicitly exhaustive. |
| REG-TEST-015 | Empty affected-container selection | `AffectedServicesTests.test_cli_emits_no_empty_service_for_unknown_path` | fixed | PR #445 exposed blank-line CLI output being parsed as an empty Docker service when workflow-only changes selected no images. | Keep line-oriented service output empty when no image matches; preserve JSON and GitHub output contracts. |

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
