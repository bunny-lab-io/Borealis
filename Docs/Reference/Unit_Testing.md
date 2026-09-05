# Borealis Validation and Unit Testing

Repository-owned commands run same correctness rules locally and in pull requests. Start with narrow affected lane, then run full affected lane before review.

## Main Commands

Run commands from repository root.

```bash
# Fast syntax, inventory, source-map, architecture, and dependency policies.
./Tests/run-repository-policy.sh

# Runtime test lanes.
./Tests/run-agent.sh
./Tests/run-engine-go.sh
./Tests/run-engine-python.sh
./Tests/run-webui.sh

# Integration and build lanes.
./Tests/run-database-postgres.sh
./Tests/run-k3s-policy.sh
./Tests/run-containers.sh        # Images affected by current worktree.
./Tests/run-containers.sh --all  # Every production image.
./Tests/run-migration-helpers.sh
./Tests/run-docs.sh

# Full portable suite. Builds every production image; container and PostgreSQL lanes need Docker.
./Tests/run-all.sh
```

Compatibility entrypoints remain supported:

```bash
./Engine_Unit_Tests.sh
./Data/Agent/Unit_Tests/Agent_Unit_Tests.sh
```

```powershell
.\Data\Agent\Unit_Tests\Agent_Unit_Tests.ps1
```

`Engine_Unit_Tests.sh` aggregates Engine Go, site-worker Python, and WebUI lanes. Agent wrappers run Go format, module, vet, test, and cross-platform build checks.

## Engine Domains

Use inventory-backed site-worker Python domains while iterating:

```bash
./Engine_Unit_Tests.sh --list-domains
./Engine_Unit_Tests.sh --domain go
./Engine_Unit_Tests.sh --domain core
./Engine_Unit_Tests.sh --domain webui
```

| Domain | Coverage |
| --- | --- |
| `all` | Engine Go, all site-worker Python, and WebUI lanes through compatibility entrypoint. |
| `go` | Production Go API, scheduler, operator, WireGuard control, and cross-runtime contracts. |
| `webui` | WebUI runtime-script tests, Vitest, and production build. |
| `ansible` | Site-worker Ansible runner. |
| `core` | Site-worker database bootstrap, edge settings, and secret configuration. |
| `files` | Site-worker file-transfer behavior. |
| `remote-access` | Site-worker Guacamole, worker socket, VNC, and VPN shell behavior. |
| `scheduler` | Site-worker queue and work-claim behavior. |

`Tests/manifests/engine-test-domains.json` owns Python domain membership. Inventory policy fails for undocumented tests, missing files, duplicate ownership, or zero-test domains.

## Clean Workspace Contract

Runners keep dependencies, compiled output, caches, virtual environments, and reports outside staged source.

- Results default to `Unit_Test_Results/<lane>-<timestamp>/`.
- `BOREALIS_UNIT_TEST_RESULTS_DIR` overrides result location.
- Site-worker Python creates clean virtual environment unless `BOREALIS_ENGINE_TEST_PYTHON` names prepared interpreter.
- WebUI copies committed source to temporary workspace before `npm ci`, Vitest, and Vite build.
- Go module tidy checks work against temporary module copy and fail on drift.
- No lane writes `node_modules`, `__pycache__`, binaries, or generated configuration into staged source.

## Prerequisites

- Agent: Go 1.22.12.
- Engine API: Go 1.25.12.
- WebUI: Node.js 22 and npm.
- Site-worker Python and docs: Python 3 with `venv`.
- Repository policy: ShellCheck, PowerShell, Node.js, `actionlint`, and dependencies in `Tests/requirements-policy.txt`.
- Container and PostgreSQL lanes: Docker with local image-build permission.

Missing tools fail clearly. No required lane silently skips.

## PostgreSQL Validation

Run `./Tests/run-database-postgres.sh` when changing cluster membership, controller recovery, operation storage, or scheduler behavior. The runner creates an isolated PostgreSQL 17 container and removes it when validation exits. It never uses the deployed Engine database.

Every required database test must run and pass. Missing tests, skipped tests or subtests, and incomplete results fail the lane. Keep the reported result directory when investigating a failure; CI retains database logs for successful and failed runs.

## CI Boundary

Normal pull requests validate deterministic repository behavior. Full Engine deploy, live K3s readiness, Longhorn persistence, public DNS, TLS issuance, real Agent enrollment, browser interaction, and remote-device networking remain deployment or Tier 3 qualification responsibilities.

## Regression Tracking

Do not delete regression coverage silently. Update [Testing Regressions](testing-regressions.md) when test protects known production, operator, or review regression.

??? example "Detailed Codex Breakdown"

    ### Related documentation

    - [Testing Regressions](testing-regressions.md)
    - [Architecture Overview](architecture-overview.md)
    - [Engine Runtime](Core%20Runtimes/engine-runtime.md)
    - [Agent Runtime](Core%20Runtimes/agent-runtime.md)

    ### Source map

    - Portable runners: `Tests/run-*.sh`.
    - Windows Agent runner: `Tests/run-agent-windows.ps1`.
    - Test-domain inventory: `Tests/manifests/engine-test-domains.json`.
    - Path-to-gate inventory: `Tests/manifests/ci-paths.json`.
    - Engine Go tests: package-local `*_test.go` under `Data/Engine/Containers/api-backend/cmd/api-backend/`.
    - Site-worker Python tests: `Data/Engine/Unit_Tests/`.
    - Agent Go tests: package-local `*_test.go` under `Data/Agent/`.
    - WebUI Vitest tests: `Data/Engine/Containers/webui-frontend/data/web-interface/Unit_Tests/`.
    - WebUI runtime contracts: `Tests/webui/runtime-scripts.test.js`.
    - PostgreSQL integration: `Tests/integration/database/`.
    - Required PostgreSQL Go test inventory: `Tests/manifests/postgres-tests.json`.
    - PostgreSQL inventory and result audit: `Tests/policy/check_postgres_inventory.py`; Go syntax discovery: `Tests/tools/postgresinventory/main.go`.
    - Migration helper tests: `Tests/integration/migration_helpers/`.

    ### Validation selection

    - Use `Tests/helpers/changed_paths.py` for stable CI group selection.
    - Use `Tests/helpers/affected_services.py` for container image selection.
    - Keep workflow YAML thin: checkout, tool setup/cache, repository command invocation, aggregate status, diagnostic artifact upload.
    - Add public API routes to Go source, API docs, and generated route inventory in same change. Generator preserves reviewed route-specific test/exemption choices; new routes remain without evidence and fail policy until author records focused test or reviewed exemption.
    - Add direct dependencies to lockfiles/manifests and `Docs/Reference/SBOM.md` in same change.

    ### PostgreSQL inventory contract

    - Every database-backed Go integration test in the API package reads `BOREALIS_TEST_DATABASE_URL` directly or through a package-level test helper function. The Go syntax walker follows helper declarations within each test package, preserving local shadowing and excluding same-named receiver methods and imported selectors. It compares discovered test names with the maintained inventory; register new tests in the same PR.
    - `Tests/run-repository-policy.sh` checks inventory drift. `Tests/run-database-postgres.sh` derives its exact anchored test selection from that inventory and uses uncached `go test -json -count=1` execution.
    - Result audit requires every inventoried top-level test to start and pass plus package completion. Any skipped or failed test/subtest, unexpected package/test, malformed output, or missing result fails. Unit-only Engine Go runs may still skip tests without database configuration; that is not database validation evidence.
    - Database CI selection covers API package Go source/tests, internal packages and module metadata as well as database fixtures, runner, inventory and auditing tools. This intentionally covers shared store/lease helpers beyond cluster filenames.
    - Results include `postgres-go-results.jsonl`, `postgres-go-stderr.log`, `postgres-integration.log` and PostgreSQL container diagnostics. CI uploads logs only, excluding temporary credentials, runtime files and virtual environments.
    - `TestPostgresLeaseTransportBlackholeAndRecovery` forwards real PostgreSQL traffic through a local TCP proxy, drops both directions after confirming an active query, verifies direct socket cancellation and bounded startup, and proves reconnection after healing. `TestClusterControllerLeaseGuardBoundsBlockedRenewalAndClose` deliberately ignores cancellation inside renewal to verify the independent watchdog.
    - Hosts requiring documented permission-sensitive validation may use existing `BOREALIS_DOCKER_USE_SUDO=1` runner option. It applies only to the runner's uniquely named disposable PostgreSQL container.

    ### Python ownership audit

    - Engine Python inventory contains 10 files across five domains. Every file exercises current site-worker execution, worker transport, remote access, or schema bootstrap reused by site-worker image.
    - Go Agent wrapper coverage lives in `Data/Agent/internal/scripts/scripts_test.go`.
    - Go auth, Assembly, metadata, workflow, Engine launcher, Traefik-entrypoint, and WebUI cookie-boundary coverage lives under `Data/Engine/Containers/api-backend/cmd/api-backend/`.
    - Go WireGuard control validation lives under `Data/Engine/Containers/api-backend/internal/wireguardcontrol/`.
    - Removed Python suites must not return under new names. Behavior owned by Go belongs in package-local Go tests.

    - `test_access_management_api.py` -> Go auth, Aegis, credential, passkey, password, cookie, and WebUI cookie-boundary tests.
    - `assemblies/test_agent_powershell_wrapper.py` -> Agent `TestBuildPowerShellScriptPreservesAdvancedScriptPreamble`.
    - `assemblies/test_cache.py` -> Go Assembly store/catalog tests plus retained site-worker schema-bootstrap test. Python cache-only timing checks retired with cache runtime.
    - `assemblies/test_official_catalog.py` -> Go catalog refresh/import/cleanup, summary precedence, and canonical workflow document tests.
    - `assemblies/test_payloads.py` -> Go Assembly store and handler tests. Retired filesystem payload mirror removed.
    - `test_engine_launcher.py` -> Go repository-contract tests for command exposure, cutover order, rollback, and network-mode fallback.
    - `test_metadata_fields.py` -> existing Go metadata definition, reserved-field, and device-value handler tests.
    - `test_wireguard_control_server.py` -> Go WireGuard control runtime tests, including live Unix socket and privileged command allowlist.
    - `test_workflow_runtime.py` -> Go `TestWorkflowUpdateSQLUsesExplicitColumnAllowlist`.

    Traefik shell-entrypoint assertions moved from `test_edge_runtime.py` into Go repository-contract tests. Python file now tests only Python edge-settings loader still consumed by site workers.
