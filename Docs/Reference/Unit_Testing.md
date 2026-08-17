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
./Tests/run-containers.sh
./Tests/run-migration-helpers.sh
./Tests/run-docs.sh

# Normal portable suite. Container and PostgreSQL lanes need Docker.
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

`Engine_Unit_Tests.sh` aggregates Engine Go, retained Python, and WebUI lanes. Agent wrappers run Go format, module, vet, test, and cross-platform build checks.

## Engine Domains

Use inventory-backed retained Python domains while iterating:

```bash
./Engine_Unit_Tests.sh --list-domains
./Engine_Unit_Tests.sh --domain go
./Engine_Unit_Tests.sh --domain metadata
./Engine_Unit_Tests.sh --domain webui
```

| Domain | Coverage |
| --- | --- |
| `all` | Engine Go, all retained Python, and WebUI lanes through compatibility entrypoint. |
| `go` | Production Go API, scheduler, and operator packages. |
| `webui` | WebUI runtime-script tests, Vitest, and production build. |
| `access` | Retained Aegis and access-management behavior. |
| `ansible` | Retained Engine-side Ansible runner. |
| `assemblies` | Retained assembly cache, catalog, payload, and Agent wrapper behavior. |
| `core` | Database bootstrap, edge runtime, launcher, and secret configuration. |
| `files` | Retained file-management worker behavior. |
| `metadata` | Reserved and operator-defined metadata fields. |
| `remote-access` | Retained Guacamole, site-worker socket, VNC, VPN shell, and WireGuard worker behavior. |
| `scheduler` | Retained queue and site-worker work-claim behavior. |
| `workflows` | Retained workflow runtime behavior. |

`Tests/manifests/engine-test-domains.json` owns Python domain membership. Inventory policy fails for undocumented tests, missing files, duplicate ownership, or zero-test domains.

## Clean Workspace Contract

Runners keep dependencies, compiled output, caches, virtual environments, and reports outside staged source.

- Results default to `Unit_Test_Results/<lane>-<timestamp>/`.
- `BOREALIS_UNIT_TEST_RESULTS_DIR` overrides result location.
- Engine Python creates clean virtual environment unless `BOREALIS_ENGINE_TEST_PYTHON` names prepared interpreter.
- WebUI copies committed source to temporary workspace before `npm ci`, Vitest, and Vite build.
- Go module tidy checks work against temporary module copy and fail on drift.
- No lane writes `node_modules`, `__pycache__`, binaries, or generated configuration into staged source.

## Prerequisites

- Agent: Go 1.22.12.
- Engine API: Go 1.25.12.
- WebUI: Node.js 22 and npm.
- Retained Python and docs: Python 3 with `venv`.
- Repository policy: ShellCheck, PowerShell, Node.js, `actionlint`, and dependencies in `Tests/requirements-policy.txt`.
- Container and PostgreSQL lanes: Docker with local image-build permission.

Missing tools fail clearly. No required lane silently skips.

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
    - Retained Engine Python tests: `Data/Engine/Unit_Tests/`.
    - Agent Go tests: package-local `*_test.go` under `Data/Agent/`.
    - WebUI Vitest tests: `Data/Engine/Containers/webui-frontend/data/web-interface/Unit_Tests/`.
    - WebUI runtime contracts: `Tests/webui/runtime-scripts.test.js`.
    - PostgreSQL integration: `Tests/integration/database/`.
    - Migration helper tests: `Tests/integration/migration_helpers/`.

    ### Validation selection

    - Use `Tests/helpers/changed_paths.py` for stable CI group selection.
    - Use `Tests/helpers/affected_services.py` for container image selection.
    - Keep workflow YAML thin: checkout, tool setup/cache, repository command invocation, aggregate status, diagnostic artifact upload.
    - Add public API routes to Go source, API docs, and generated route inventory in same change.
    - Add direct dependencies to lockfiles/manifests and `Docs/Reference/SBOM.md` in same change.
