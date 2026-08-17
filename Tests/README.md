# Borealis Portable Validation

Repository commands below own deterministic correctness rules. GitHub Actions selects affected commands and reports one stable `PR Validation / required` result.

## Local Commands

| Command | Purpose | Main prerequisites |
| --- | --- | --- |
| `./Tests/run-repository-policy.sh` | Syntax, workflow, inventory, architecture, source-map, build-manifest, and SBOM policy | Python, PyYAML, Node 22, ShellCheck, PowerShell, actionlint, Docker Compose |
| `./Tests/run-agent.sh` | Agent format, tidy drift, vet, tests, Windows build, Linux build | Go 1.22.12 |
| `./Tests/run-engine-go.sh` | Go API/scheduler/operator/WireGuard format, tidy drift, vet, tests, binaries, route/cutover policy | Go 1.25.12 |
| `./Tests/run-engine-python.sh [--domain NAME]` | Inventory-backed site-worker Python suites in isolated files | Python 3.12 and venv |
| `./Tests/run-webui.sh` | Runtime-script contracts, clean `npm ci`, Vitest, production build | Node 22 and npm |
| `./Tests/run-database-postgres.sh` | Fresh, repeat, legacy, partial-schema, SQL translation, transaction, and constraint behavior | Docker, Python 3.12 |
| `./Tests/run-k3s-policy.sh` | Side-effect-free production manifest render, semantic security policy, operator contract tests | Python/PyYAML, Go 1.25.12 |
| `./Tests/run-containers.sh` | Manifest-derived affected image builds and metadata checks | Docker, Go 1.25.12 when shared binary needed |
| `./Tests/run-migration-helpers.sh` | Command-mocked destructive-helper ordering and failure contracts | Python 3 |
| `./Tests/run-docs.sh` | Pinned clean Zensical strict build | Python 3 and venv |
| `./Tests/run-all.sh` | All normal portable lanes | Combined prerequisites above |

Set `BOREALIS_DOCKER_USE_SUDO=1` where local Docker socket requires sudo. Results, virtual environments, caches, compiled output, and reports stay under ignored `Unit_Test_Results/`, temporary workspaces, or ignored `site/`.

## Path-to-Gate Behavior

`Tests/manifests/ci-paths.json` selects subsystem lanes. `Tests/helpers/affected_services.py` separately resolves images from production `build-manifest.json`. Workflow, shared-runner, and path-manifest changes trigger all subsystem lanes. Each required job has explicit timeout; superseded PR runs cancel; failed lane logs upload only ignored test results.

| Workflow/check | Trigger paths | Repository command | Validation performed | Regression prevented | Expected warm runtime |
| --- | --- | --- | --- | --- | --- |
| Repository Policy / static-and-inventory | Every PR | `./Tests/run-repository-policy.sh` | Syntax, data parsing, workflow, inventories, cutovers, source maps, SBOM | Silent stale references and invalid configuration | under 2 min |
| Agent / linux | `Data/Agent/**`, shared runners | `./Tests/run-agent.sh` | Go format/tidy/vet/test plus Windows/Linux compile | OS build or module drift | under 10 min |
| Agent / windows | `Data/Agent/**`, shared runners | `.\Tests\run-agent-windows.ps1` | Native Windows Go format/tidy/vet/test/build | Windows-tagged test disconnection | under 10 min |
| Engine / go-runtimes | Go runtime and inspected cross-runtime inputs | `./Tests/run-engine-go.sh` | Go format/tidy/vet/test/build, routes, cutover | Public API, scheduler, operator, WireGuard, launcher, Traefik, and auth-boundary regressions | under 10 min |
| Engine / site-worker-python | Python worker/tests | `./Tests/run-engine-python.sh` | Every inventoried site-worker test file in isolation | Missing, hidden, or Go-owned Python suite | under 20 min |
| WebUI / test-and-build | WebUI source/package/runtime scripts | `./Tests/run-webui.sh` | Runtime scripts, clean install, Vitest, production build | Dirty-checkout dependency and static hosting regressions | under 15 min |
| Database / postgres-17 | schema/database/PostgreSQL image | `./Tests/run-database-postgres.sh` | Real PostgreSQL 17 schema lifecycle | SQLite-only compatibility gaps | under 15 min |
| Architecture / k3s-and-cutover | Engine/K3s/security paths | `./Tests/run-k3s-policy.sh` | Semantic manifests, privileges, mounts, ownership, safe operator tests | Security boundary and completed cutover regressions | under 10 min |
| Containers / affected-image-builds | Build-manifest service inputs | `./Tests/run-containers.sh` | Affected builds, both WebUI targets, image metadata | Broken COPY/base/entrypoint paths | varies by affected images |
| Migration / helper-contracts | Migration helpers/tests | `./Tests/run-migration-helpers.sh` | Tool absence, ordering, failure preservation, targeting | Destructive helper sequencing regressions | under 2 min |
| Documentation / strict-build | Docs/config/docs runner | `./Tests/run-docs.sh` | Pinned strict docs build and artifact pruning | Post-merge documentation failure | under 5 min |

## Existing Tests Now Enforced

- Every package-local Agent Go test, including Windows build-tagged tests on native Windows.
- Every package-local Engine Go API, scheduler, and operator test.
- All 10 site-worker Python files recorded across five explicit domains.
- All WebUI Vitest suites under staged `Unit_Tests/` through clean temporary dependency workspace.
- Existing operator safe-pod and restricted lifecycle tests through focused K3s lane.
- Retired Compose policy through repository policy.

## New Tests and Executable Policies

- WebUI runtime-script tests reject unsafe methods, malformed URLs, NUL/backslash/traversal requests, bad health results, and unsupported entrypoint modes while protecting SPA, MIME, cache, HEAD, and bind behavior.
- PostgreSQL integration catches non-idempotent initialization, legacy/partial repair loss, incorrect SQLite translation, transaction loss, and missing constraints/indexes.
- Migration helper mocks catch early truncation, missing prerequisites, wrong K3s target, unsafe backup replacement, failed-dump preservation, and unignored backup paths.
- Build-manifest policy catches unmatched inputs, missing Dockerfiles, duplicate coverage, role drift, and hash omissions.
- Affected-service unit tests catch shared-input and service-selection drift.
- K3s semantic policy enforces namespace ownership, namespace-scoped operator RBAC, ServiceAccount token boundary, exact capability/host-network/hostPath exceptions, seccomp, read-only roots, dropped capabilities, probes/resources, ClusterIP services, scheduler `Recreate`, and Longhorn PVC retention.
- Cutover policy prevents return of retired Compose workloads, Python public API/WebUI ownership, Python in API/scheduler/WireGuard images, public Python listeners, legacy orchestrator role, and direct kubectl lifecycle bypass.
- Go WireGuard control tests reject arbitrary privileged commands, broad routes, Engine-address peer reuse, and service-root symlink escapes while preserving expected listener, peer, interface, and firewall operations.
- Go AST route policy inventories literal routes, auth class, docs row, focused test or reviewed exemption.
- Source-map/test docs policy catches removed ownership paths, nonexistent source references, stale regression tests, domain mismatch, and missing commands.
- SBOM policy compares base images, Go modules, Python requirements, WebUI package/lock roots, bootstrap tool versions, and Go archive checksum pins.

## Baseline Defects Repaired

- Replaced stale Engine mappings with machine-readable ownership, then reduced Python inventory from 19 files to 10 site-worker/schema files. Go suites now own Agent wrapper, auth, Assembly, metadata, workflow, launcher, Traefik-entrypoint, and WireGuard checks.
- Connected production Go API/scheduler/operator tests to Engine compatibility entrypoint.
- Added committed WebUI lockfile and changed Docker/CI installation to `npm ci`.
- Corrected stale build-manifest inputs and verified every `BUILD_ROLES` Dockerfile exactly once.
- Corrected removed Python API/WebUI source maps and generated reviewed 172-route Go inventory.
- Moved software override defaults from removed source directory into mounted runtime config.
- Corrected Traefik and direct WebUI dependency versions in SBOM; added pinned validation dependencies and Go toolchains.
- Added SHA256 verification for downloaded official Go archives.

## GitHub and Deployment Boundaries

Workflow YAML intentionally owns checkout, event metadata, tool setup/cache, path-conditioned job scheduling, timeouts, failure artifact upload, and stable aggregate result. Repository scripts own behavior assertions.

Normal CI does not deploy Engine or require production secrets, root K3s access, DNS, TLS issuance, real Agents, persistent storage, or real remote endpoints. `Engine.sh` retains host package, K3s, Longhorn, storage, network, certificate, workload rollout, and live health validation.

## Deferred Tier 3 Validation

- Clean install/upgrade and rollback on disposable supported Linux hosts.
- Real K3s/Longhorn restart, PVC persistence, backup/restore, and migration qualification.
- Public and internal-only DNS/TLS edge behavior.
- Browser interaction, HMR, accessibility, and visual regression.
- Real Windows/Linux Agent enrollment, update, service recovery, WireGuard, shell, VNC, file, process, and software operations.
- Multi-device/site pressure, unreliable networks, and long-duration scheduler behavior.

Run those manually, on release candidates, scheduled disposable self-hosted infrastructure, or dedicated qualification lanes. Normal PR validation cannot prove actual host or network behavior.

## Cost and Remaining Risk

Path selection avoids Windows, PostgreSQL, WebUI, docs, and image runners on unrelated changes. Go, npm, and pip caches reduce repeated dependency cost. Container lane remains slowest and builds only manifest-affected images; WebUI changes build dev and prod. Monthly cost depends on PR volume and changed-path mix; repository has no reliable usage baseline yet.

Portable tests still depend on mocks for external systems and cannot prove deployment health, browser rendering, physical endpoint behavior, network reachability, or persistent cluster recovery.
