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

Engine Go SSH network-observer fixtures require Ubuntu system Python with `python3-netplan` and `python3-yaml`. CI installs these parser dependencies on its disposable runner. Local prerequisite installation is `sudo apt-get install python3-netplan python3-yaml`; tests fail clearly when bindings are missing. Fixtures read synthetic configuration under private temporary directories and never change installed networking or start services.

Engine Go CI runs uncached tests with the race detector, including concurrent Aegis TLS reload. Reproduce on hosts with a C compiler using `CGO_ENABLED=1 BOREALIS_ENGINE_GO_RACE=1 ./Tests/run-engine-go.sh`. Runtime binary builds retain their documented `CGO_ENABLED=0` configuration. A normal local run without this flag is not race-detector evidence.

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

Legacy admission preparation recovery is covered by `PYTHONDONTWRITEBYTECODE=1 python3 -m unittest Tests.Unit_Tests.test_legacy_admission_recovery Tests.Unit_Tests.test_engine_cluster_recovery`. These repository tests use temporary files and mock host service/cluster observations; they never operate on deployed PostgreSQL or K3s. Runtime recovery still requires the immutable-release check and original controller admission gates.

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

    - S01 `cluster_ssh_onboarding_test.go` validates Admin submission/progress routes before source/encryption/store work, complete request shape and byte limits, encrypted-key syntax, exact operation/target binding, lost receipt recovery and private error boundaries. `Cluster_SSH_Onboarding.test.jsx` validates frontend cohort requests, nested key/passphrase/sudo preservation, paired submission, lost-response recovery, obsolete/failed status handling, safe cancellation and credential cleanup. `Cluster_Management.test.jsx` covers pair/replacement entry gates, public UUID URL recovery and active-operation access. WebUI runner executes tests and production build in a temporary workspace; these checks do not qualify preparation, joining or live deployment.

    ### Target SSH management-link fixtures

    - `internal/clusterremote/management_link_test.go` compares actual Go source and Python target parsers over the same adversarial Ethernet/IP/MAC corpus, including interface/address inventory limits and ambiguous JSON. Full observations use real Netplan parsing and synthetic busctl/ip child processes to check repeated MAC/interface/namespace/host/backend/route identity, private errors and killed/joined deadlines. Pinned SSH tests bind endpoint/key and complete peer addresses; actual nested shells verify password, NOPASSWD and root stdin framing. Run `go -C Data/Engine/Containers/api-backend test ./internal/clusterremote -run '^TestTargetManagementLink' -count=1`, then full Engine Go. These fixtures require no live host commands, privileged namespaces or ARP packets. Fresh cohort/VIP-owner binding, ARP and remaining preparation gates are separate qualification work.

    ### PostgreSQL inventory contract

    U02 Engine Version coverage lives in `Cluster_Management.test.jsx` and Go `TestClusterSnapshotPreservesNodeVersionRecordsAndPendingTarget`. It checks three-node mixed/unknown records, exact SHA tooltips, pending operation scope, recorded-versus-runtime identity, stale/future report times, failed/hanging polling and recovery. WebUI runner uses temporary workspace; live three-node browser verification remains Q01 qualification.

    - Every database-backed Go integration test in the API package reads `BOREALIS_TEST_DATABASE_URL` directly or through a package-level test helper function. The Go syntax walker follows helper declarations within each test package, preserving local shadowing and excluding same-named receiver methods and imported selectors. It compares discovered test names with the maintained inventory; register new tests in the same PR.
    - `Tests/run-repository-policy.sh` checks inventory drift. `Tests/run-database-postgres.sh` derives its exact anchored test selection from that inventory and uses uncached `go test -json -count=1` execution.
    - Result audit requires every inventoried top-level test to start and pass plus package completion. Any skipped or failed test/subtest, unexpected package/test, malformed output, or missing result fails. Unit-only Engine Go runs may still skip tests without database configuration; that is not database validation evidence.
    - Database CI selection covers API package Go source/tests, internal packages and module metadata as well as database fixtures, runner, inventory and auditing tools. This intentionally covers shared store/lease helpers beyond cluster filenames.
    - Results include `postgres-go-results.jsonl`, `postgres-go-stderr.log`, `postgres-integration.log` and PostgreSQL container diagnostics. CI uploads logs only, excluding temporary credentials, runtime files and virtual environments.
    - Hosts requiring documented permission-sensitive validation may use existing `BOREALIS_DOCKER_USE_SUDO=1` runner option. It applies only to the runner's uniquely named disposable PostgreSQL container.

    - Admission regressions cover 750 unrelated events, authenticated idempotent replay, bounded expiry and renewal, safe pending cancellation, retained failed second joiner, controller restart/lease fencing, and replacement admission. Node-manager HTTP tests reject plaintext/redirect disclosure and require authoritative K3s settings.
    - Aegis integration uses real PostgreSQL verification-token checks and a real TLS listener/fanout to prove a renewed surviving key holder unlocks a joining replica, incorrect keys remain rejected, database connections return to the pool, and all-cold replicas remain locked. Unit TLS tests cover expiry, partial/mixed projections, concurrent reload, CA overlap/retirement, and required client identity. K3s workload tests cover independent required trust projection and create-only initialization preserving operator policy.
    - S01 PostgreSQL tests cover encrypted SSH/sudo credential binding and exact syntax, ciphertext swapping, locked Aegis, fixed expiry, concurrent target ownership, stale holder/generation/step rejection, expired/replaced controller authority, changed operation/attempt/step, duplicate host identity, Aegis generation change before commit, reset cascade and terminal/startup cleanup. A one-connection pool checks crypto starts after connection release; concurrent claims temporarily use two connections. These tests exercise actual isolated PostgreSQL and Aegis envelopes, not deployed credentials, complete API rotation/reset workflows or remote provisioning. All four `TestClusterSSHCredentialsPostgres*` cases are required in the inventory.
    - Three required preparation lifecycle tests (`TestClusterSSHPreparationScopePostgresRenewsAndFences`, `TestClusterSSHPreparationRenewalPostgresBindsCiphertextAtCommit`, `TestClusterSSHPreparationRenewalPostgresRechecksAfterLockWait`) prove45-second lease renewal during blocked acquisition, one-connection database availability, original-ciphertext binding and controller-first stale/expiry checks after lock waits. Unit scope tests prove canceled work/cleanup joins, no detached export checks and unchanged authority across renewals. `TestBootstrapAuthorityTimeoutJoinsCallback` proves native SSH refresh joins canceled authority callbacks and rejects late success. Preparation claiming remains disabled in production; fixtures reserve that phase only inside disposable PostgreSQL.
    - Required `TestClusterSSHSourceBrokerPostgresAuthorityAndPrivateTransfer` exercises actual encrypted controller/API source transfer, expansion/replacement, fresh Job/Pod receipts, one-connection database release, controller/target/credential fencing, locked Aegis after network work and Secret UID/revision/excluded-data drift. Focused `TestClusterSSHSourceBroker*` tests cover authenticated wire shape, replay/capacity, follower responses, wrong nonce/key/direction, redirects, lost POSTs and cancellation. Staging remains reserved to isolated fixtures; no target mutation or live qualification follows from these tests.
    - Four required `TestClusterSSHWorkerPostgres*` tests compose actual Aegis encryption, database claims, the inspection executor and typed outcome commits. They cover one-connection crypto/network boundaries, lock during blocked SSH, changed controller/operation/target authority, replacement of both Aegis and credential generation, expired credentials, replay, retained evidence after cleanup and two competing API consumers claiming each target once. Authority-lock tests use three connections and actual `pg_stat_activity` lock waits to prove claim/renew/read/completion serialize with parent transitions, reject changed state after waiting and preserve controller-first lock order. Native SSH has separate transport tests; worker fixtures use controlled clients and never contact lab nodes. Two required `TestClusterSSHCohortPostgres*` tests cover retained report attempt/generation, incomplete/expired credential cohorts, changed source topology/controller during Kubernetes observation, actual source identity, and connection release before network calls. Three required `TestClusterSSHParentPostgres*` tests cover controller start/partial progress/resumption, durable qualification wait, transport failure retention, proof changes before commit, and read-only cancellation with stale-worker and generic-retry rejection. Two required `TestClusterSSHQueuePostgres*` tests cover atomic pair/replacement queueing, concurrent callers, changed source/Aegis, duplicate or retained target reservations, and public progress without credential or raw-error disclosure. The current inventory contains37required PostgreSQL tests; required-lane audit still rejects any skip.
    - `Tests/Unit_Tests/test_node_bootstrap_assets.py` belongs to repository policy discovery. It creates real tagged Git fixtures and compiles/runs their node-manager fixture with Go1.25.12, checking reproducible archive hashes, exact shallow Git/tag identity, executable mode, manifest/binary binding, exclusion of untracked/modified/operator Git state, unsafe source-object rejection, moved/missing tags and incorrect compiler failure. Run with `BOREALIS_GO_BIN` when Go is not in PATH. Together with `test_engine_release_bootstrap.py`, these checks cover packaging and existing standalone installer compatibility; remote receiver/execution and immutable publication require separate qualification.
    - Release identity integration rejects a K3s source change between API snapshot and queue transaction, mismatched manifests, and missing immutable proof; it verifies typed-manifest persistence and retry without GitHub reselection. HTTP tests cover cached-picker changes, immutable/channel checks, SHA-addressed manifests, tag movement, and K3s cache invalidation. Real Git tests fence moved stable/qualification tags; stubbed Engine installer coverage proves an existing newer K3s version is preserved. Live exact-release/K3s qualification remains Tier 3 under #493.

    - Clustered HMR entry regressions cover authenticated API denial, direct store/runner denial, legacy queued entry becoming recoverable without runtime calls, rejected entry retry, allowed failed-exit recovery/retry, and real PostgreSQL persistence. CLI tests cover confirmed standalone versus unknown/transitional membership and dev dispatch before runtime preparation. WebUI tests retain disabled-entry messaging, recorded target and active/failed exit controls; #492 production-candidate tests remain required. Exact-release live restoration must pass before U01 deployment and again during Q01.

    ### Python ownership audit

    - Privileged SSH coverage in `internal/clusterremote/privileged_inspection_test.go` uses real in-process SSH, isolated shell/sudo fixtures and real filesystem directory probes. It checks password/NOPASSWD/root stdin safety, untouched caller secrets, output limits, cancellation, failed/unknown versus absent installation state, existing Kubernetes identities, current permanent direct-network requirements and strict JSON. `TestClusterSSHCredentialSudoFramingPreservesSyntaxAndRejectsLines` keeps durable encryption aligned with sudo input framing without changing SSH login syntax. Live inspection must still verify approved target host key before authenticating.

    - Cluster recovery Bash-library tests also cover inherited sizing across all four profiles, stronger joining hosts, actual target telemetry, capped source tuning, malformed/partial/insufficient contracts and retained settings across hydration/redeploy. Source delivery and full aggregate storage/capacity qualification remain separate live gates.
    - Cohort unit tests cover paired expansion, recorded replacement, cloned host/member identities, unknown installations, inconsistent Kubernetes roster/identity/readiness, expired reports, usable subnet endpoints and wholly private IPv4 prefixes. These read-only checks do not qualify preparation, sizing/storage, persistent network configuration, Layer2 reachability or admission.
    - SSH unit and PostgreSQL fixtures use the production controller's hostname-plus-UUID holder shape, separately from target-worker UUIDs. Cohort readers accept the exact current lease identity and reject a matching UUID suffix under a missing or changed hostname prefix. Keep this runtime-shaped fixture so UUID-only validation cannot return unnoticed.
    - Source management-link tests in `internal/clusterbootstrap/management_link_test.go` and `cmd/borealis-node-manager/source_management_link_test.go` cover exact Ethernet/IP/MAC ownership, duplicate and unsupported links, strict projection/receipt fields, namespace and full source Node/link drift, bounded real command output, failed exits and killed/joined timeout/cancellation. Fresh Job/Pod and preparation-reader fixtures require recorded management-IP binding; existing PostgreSQL source/broker fixtures carry the same required identity. Run `go -C Data/Engine/Containers/api-backend test ./internal/clusterbootstrap ./cmd/borealis-node-manager ./cmd/api-backend -run 'Test(ManagementLink|Source|ClusterSSH(SourceAction|PreparationSource))' -count=1`; full Engine Go includes all source transport coverage. Tests use synthetic iproute2 data and private executable fixtures, never ARP packets or installed network changes. Source-link evidence does not qualify target networking or joining.
    - `internal/clusterremote/routed_network_test.go` checks effective direct routes under standard IPv4 RPDB, local and competing-route exclusions for every peer, repeated kernel lookups including cached redirects, strict public request/result bindings and real command/snapshot drift. `TestRoutedNetworkTargetDataBoundaries` requires independent Go/Python validation of public address data before commands. Run `go -C Data/Engine/Containers/api-backend test ./internal/clusterremote -run '^TestRoutedNetwork' -count=1`; full Engine Go also reruns active/persistent observer coverage. Tests use private synthetic network/Netplan roots, require no network namespace privilege and perform no installed route/rule changes. Custom policy support, ARP and remaining qualification gates stay separate.
    - `internal/clusterremote/active_network_test.go` combines actual Netplan fixtures with synthetic networkd/kernel command processes. It requires exact no-activation/no-interactive flags, fixed environment and unique owner/namespace binding; covers changed source/runtime files, daemon/kernel identity, ambiguous JSON, finite/foreign/duplicate addresses, failed exits and killed/joined deadline cases. `TestActiveNetworkRequiresExactCurrentInterface` checks retained iproute2 interface index through the real privileged parser. Run `go -C Data/Engine/Containers/api-backend test ./internal/clusterremote -run '^TestActiveNetwork|^TestPersistentNetwork' -count=1`. Full Engine Go runs both suites; no live D-Bus/networkd service or installed network files are required by these fixtures. Effective routing, ARP, boot/render correspondence and cohort integration remain separate qualification.
    - `internal/clusterremote/persistent_network_test.go` runs actual Netplan parsing against isolated roots, tests file/ownership/override/identity drift and public projection limits, and exercises pinned SSH timeout/output/secret boundaries. Run narrowly with `go -C Data/Engine/Containers/api-backend test ./internal/clusterremote -run '^TestPersistentNetwork' -count=1`; full Engine Go includes these tests with no optional skip. Matching/composite/backend activation, ARP and controller qualification integration remain separate S01 work.
    - S01 receiver tests: `internal/clusterbootstrap/archive_test.go` builds real Git/tag objects and a Go executable, verifies successful isolated extraction and rejects mismatched identities, content, modes, unsafe paths/Git metadata, compressed corruption and cancellation. `cluster_ssh_bootstrap_test.go` checks fresh publication/asset metadata, manifest rejection before archive download, redirect destination pinning and absence of GitHub credentials/cookies/Referer on CDN requests. These are local receiver proofs; immutable publication, SSH execution and admission require separate live qualification. `test_affected_services.py` preserves image selection for shared Go receiver, SSH and identity packages.
    - Fixed bootstrap execution tests use real in-process SSH in `internal/clusterremote/bootstrap_test.go`, real local shell/copy/hash fixtures and pipe-based session expiry tests in `internal/clusterbootstrap/session_test.go`. Coverage includes byte-exact executable/password framing, private scratch cleanup, changed/expired authority at every boundary, replayed challenges, bounded output, unsuccessful remote cleanup and exact completion identity. `cmd/borealis-node-manager/bootstrap_session_test.go` rejects changed host/executable metadata, duplicate address observations and unsafe public-file types/modes. Portable fixtures never deploy an unpublished executable to lab nodes; live immutable delivery and complete joining remain separate qualification.
    - Executor supervision tests in `internal/clusterbootstrap/executor_linux_test.go` validate fixed service arguments, complete process/invocation/cgroup/property identity, watchdog expiry/failure and private error boundaries. Supervised session tests reject observer failures before work and preserve deadlines measured from challenge issue. `TestExecutorServiceFixture` is an explicit Tier3 helper; its normal portable invocation does not start systemd or prove live containment.
    - On an operator-approved Ubuntu development host only, run `BOREALIS_GO_BIN=/opt/Borealis/Dependencies/Go/go1.25.12/bin/go python3 Tests/run-ssh-executor-containment.py`. It builds a test binary outside staged source and uses sudo only to manage unique disposable services running as the invoking development user. Cases cover normal exit, TERM-resistant orphan after successful main exit, main crash, frozen main/watchdog and authority expiry. Descendants call `setsid`, close inherited lock descriptors and write only private result-directory heartbeats. Proof requires stopped descendants, empty/removed cgroup, released lock and no late writes before guarded cleanup. Existing K3s service state/restart count/start time must remain unchanged. Each fixture also opens the actual supervised journal and retains its executor-bound intent, then releases the journal lock. The production recovery observer must reject that still-live executor and accept it only after whole-unit termination, before guarded cleanup. This runner is not part of normal PR validation and never invokes an unpublished production bootstrap helper or changes installed Borealis/K3s workloads.
    - `internal/clusterbootstrap/journal_linux_test.go` uses private temporary files and actual subprocesses to prove intent precedes execution, process death retains uncertainty, inherited child locks survive parent closure, stale owners cannot replay, definitive outcomes cannot change, and proven absence needs a fresh claim with retained bounded history. Real closed-directory errors test failed intent/completion persistence; a separate subprocess tests restrictive umask without changing the test runner. Unsafe paths/JSON and cancellation fail closed. Portable journal/executor-recovery tests now cover persisted producing executor identity, free-lock/live-executor rejection, before/after domain-proof quiescence, current supervision changes, retained history and legacy-format rejection. Unit tests inject observations; actual manager/kernel evidence comes only from the opt-in runner above. Mutation dispatch remains unfinished and CLI stays read-only.

    - Preparation input tests (`internal/clusterbootstrap/preparation_config_test.go`, `preparation_test.go` and `cluster_ssh_preparation_test.go`) cover literal private syntax, strict canonical imports, whole-cohort/claim binding, inherited sizing/CA/CNPG constraints, runtime override rejection and ownership/configuration drift across source download. Real tagged Git/Go bundle fixtures reject modified source/helper bytes, added Git metadata, unsafe paths and mismatched K3s compatibility. These are central input-assembly tests; source-action transport, preparation transition, root journal writer and live remote preparation remain unqualified and disabled. Existing executor host qualification need not repeat for this component because supervision/recovery behavior did not change.

    - Source observation tests (`source_network_test.go`, `source_runtime_test.go`, node-manager `source_network_test.go` and `cluster_ssh_preparation_source_test.go`) cover actual supervisor global ranges versus Node allocations, running-version/Node/boot agreement, unsupported critical settings, ambiguous JSON, private Secret identity/receipt drift, pair/replacement source agreement, complete authority rereads and cancellation while another observation runs. TLS listeners exercise trust rejection, redirect refusal, bounds and static private errors. Fixtures contain synthetic identities/secrets. No deployed source helper, K3s mutation or database migration is required; existing executor qualification remains unchanged. Fresh controller source-action transport and scoped Secret reads have separate coverage below; controller/API broker and preparation phase activation remain gated.
    - `TestSourceReceiptRejectsPrivateAmbiguousAndReplayedResults` and `TestSourceNetworkClientFixedRequestAndStaticFailures` verify the bounded public termination receipt and fixed Unix manager request. `TestClusterSSHSourceActionFreshJobReceiptAndAuthority` uses TLS Kubernetes fixtures for fresh observations, complete private source-reader composition, controller loss, POST ambiguity/collision, Job/Pod replacement, exact immutable image/security/mount/token settings, public source identity, malformed receipts, retry/multiple-Pod rejection and cancellation/deadline release. `TestClusterSSHSourceActionHTTPBoundary` rejects unscoped reads, Secret writes, plaintext/redirects, wrong POST status and oversized responses before accepting data. `Tests/run-k3s-policy.sh` enforces the controller-only named Secret/namespace reads and absence of Pod-log/cluster-wide Secret grants. No database behavior changes; PostgreSQL33 inventory remains unchanged. Live execution requires published immutable helper/image and guarded controller integration.

    - Three required `TestClusterSSHPreparationAuthorityPostgres*` tests cover the concrete database/Aegis adapter through paired-expansion and recorded-replacement source readers with a one-connection pool, unchanged original report generation, current ciphertext binding, partial/expired/changed cohorts, source baseline/VIP/application drift and locked memory key. Actual PostgreSQL lock waits prove controller-first order, changed payload rejection after waiting, late credential/lease expiry and cancelled-wait connection release. Pure proof tests permit map reordering while rejecting ambiguous structure and preserving exact large integers. Reserved preparation states exist only in disposable fixtures; production claiming remains inspection-only.

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
