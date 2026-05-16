# Borealis Unit Testing
[Back to Docs Index](../index.md) | [Index (HTML)](../website/index.html)

## Purpose
This page is the testing entrypoint for humans and Codex agents. Use the documented lane scripts first. They set the expected environment, write reports to one location, and keep Borealis-authored test entrypoints inside `Unit_Tests` folders.

## What Unit Tests Mean Here
- Unit tests check focused Borealis behavior with fake inputs, temporary files, mocks, and isolated helper objects.
- Unit tests do not replace live Engine deployment, Agent enrollment, browser smoke testing, or remote-access validation against real devices.
- Borealis tests lean on shared helpers so each test can say what behavior it checks instead of rebuilding fake Engine, fake device, or fake Role state every time.

## Main Commands
Run from the repository root.

```bash
./Engine_Unit_Tests.sh
```

Runs every Engine Python unit test plus staged Engine WebUI unit tests.

```bash
./Data/Agent/Unit_Tests/Agent_Unit_Tests.sh
```

Runs Go Agent unit tests and Windows/Linux build checks from Linux or another POSIX shell.

```powershell
.\Data\Agent\Unit_Tests\Agent_Unit_Tests.ps1
```

Runs Go Agent unit tests and Windows/Linux build checks from Windows PowerShell.

## Domain Commands
Use domain runs while iterating. Run full Engine or full Agent lane before handoff when change is broad, risky, or PR-ready.

```bash
./Engine_Unit_Tests.sh --list-domains
./Engine_Unit_Tests.sh --domain devices
./Engine_Unit_Tests.sh --domain webui
```

```bash
./Data/Agent/Unit_Tests/Agent_Unit_Tests.sh --list-domains
./Data/Agent/Unit_Tests/Agent_Unit_Tests.sh --domain go-agent
```

```powershell
.\Data\Agent\Unit_Tests\Agent_Unit_Tests.ps1 -ListDomains
.\Data\Agent\Unit_Tests\Agent_Unit_Tests.ps1 -Domain go-agent
```

Environment variables can also select domains:

```bash
BOREALIS_ENGINE_UNIT_TEST_DOMAIN=scheduler ./Engine_Unit_Tests.sh
BOREALIS_AGENT_UNIT_TEST_DOMAIN=go-agent ./Data/Agent/Unit_Tests/Agent_Unit_Tests.sh
```

## Engine Domains
| Domain | Purpose |
| --- | --- |
| `all` | All Engine Python tests and WebUI unit tests. |
| `access` | Auth, Aegis, credentials, passkeys, MFA, password reset, and GitHub token API behavior. |
| `agent-role` | Engine-side Agent RoleManager import and role health behavior. |
| `ansible` | Engine Ansible runner behavior. |
| `assemblies` | Assembly cache, import/export, payload, permission, execution type, and official catalog behavior. |
| `core` | Core API, edge runtime, Engine secret config, and web UI API checks. |
| `devices` | Device APIs, purge flow, filters, and session inventory. |
| `enrollment` | Agent enrollment and token API behavior. |
| `files` | Engine file management API behavior. |
| `rbac` | Role-based access control API behavior. |
| `remote-access` | Guacamole, VNC, VPN shell/tunnel, WireGuard, and websocket registry behavior. |
| `runtime-overrides` | Runtime override merge behavior. |
| `scheduler` | Scheduled jobs API, scheduler timing behavior, target parsing, and automatic onboarding job creation. |
| `server` | Server information API behavior. |
| `watchdogs` | Watchdog API behavior. |
| `webui` | Engine WebUI Vitest lane only. |
| `workflows` | Workflow runtime behavior. |

## Agent Domains
| Domain | Purpose |
| --- | --- |
| `all` | Go Agent tests and Windows/Linux build checks. |
| `go-agent` | Go Agent tests and Windows/Linux build checks. |

## Test Locations
- Engine Python unit tests: `Data/Engine/Unit_Tests/`.
- Engine assembly unit tests: `Data/Engine/Unit_Tests/assemblies/`.
- Agent test lane entrypoints: `Data/Agent/Unit_Tests/`.
- Agent Go unit tests: package-local `*_test.go` files under `Data/Agent/cmd` and `Data/Agent/internal`.
- WebUI unit tests: `Data/Engine/Containers/webui-frontend/data/web-interface/Unit_Tests/`.
- Runtime WebUI unit test cache, when prepared for tests: `Engine/Services/webui-frontend/cache/web-interface/Unit_Tests/`.

## Results
- Results write to `Unit_Test_Results/<runtime>-<timestamp>/`.
- Go Agent lane writes `agent-go.log`.
- Python lanes write `*-pytest.log` and `*-pytest.xml`.
- Engine Python writes per-file JUnit XML under `engine-pytest-junit/`.
- WebUI writes `engine-webui-vitest.log` and, when Vitest reaches report generation, `engine-webui-vitest.xml`.
- `summary.txt` records domain and lane exit status.
- Scripts set `PYTHONDONTWRITEBYTECODE=1` so source folders do not collect `__pycache__`.

## Expected Setup
- Run scripts from repo root.
- Engine tests prefer `Engine/bin/python` when available because Engine venv includes pytest.
- Agent tests use Go 1.22+; `Data/Agent/build-agent.sh` installs a native Go toolchain under `Dependencies/Go` on Linux when missing.
- WebUI tests require a prepared runtime cache at `Engine/Services/webui-frontend/cache/web-interface` with `node_modules` and `Unit_Tests`. Container deploys also seed editable dev/HMR source under `Engine/Services/webui-frontend/data/web-interface/`, but that source folder is not the test dependency cache.
- Do not run npm or Vite from `Data/Engine/Containers/webui-frontend/data/web-interface`; use a prepared runtime cache, the dev/HMR runtime source, or defer UI runtime validation to the operator redeploying the WebUI container.
- Container launcher/static validation should start with shell and Compose checks before broader unit lanes:
```bash
bash -n Engine.sh
docker compose -f Data/Engine/Containers/compose.yaml config
```

## Shared Helpers
- Engine helpers live under `Data/Engine/Unit_Tests/support/`.
- `support/engine.py` builds isolated Engine test harnesses and authenticated clients.
- `support/devices.py` creates fake devices, services, and inventory defaults.
- `support/software_config.py` isolates icon override, uninstall override, and uninstall blocklist JSON.
- Go tests should prefer package-local fakes and `httptest`/fake Socket.IO harnesses.

Prefer small helpers with clear names: fake Engine, fake devices, fake Role hooks, fake config files. Avoid one giant fake Borealis environment because it hides behavior and makes failures harder to read.

## Regression Tracking
- No regression test should be deleted silently.
- Fix stale expectation, document current failure, or quarantine with reason.
- Add or update `Docs/testing-regressions.md` when a test protects known production, operator, or PR-review regression.
- Use status labels from `Docs/testing-regressions.md`: `open`, `fixed`, `stale-test`, `environment-gap`, or `flaky`.

## Codex Agent
- Read this page before choosing validation for codebase changes.
- Use documented lane scripts as testing entrypoint. Do not start with raw `pytest`, `npm`, or `vitest` unless diagnosing runner failure.
- For container deployment changes, run shell syntax checks for `Engine.sh`, then validate `Data/Engine/Containers/compose.yaml` through `docker compose -f Data/Engine/Containers/compose.yaml config`.
- Pick narrow domain runs while iterating, then run full affected lane when practical: Engine change gets `./Engine_Unit_Tests.sh`; Agent change gets `./Data/Agent/Unit_Tests/Agent_Unit_Tests.sh` or `.\Data\Agent\Unit_Tests\Agent_Unit_Tests.ps1`; cross-runtime change gets both.
- For WebUI unit tests, use `./Engine_Unit_Tests.sh --domain webui`. Do not run npm or vite from `Data/Engine/Containers/webui-frontend/data/web-interface`; staging source is not the runtime test location.
- Keep reports under `Unit_Test_Results/`. Do not write `.pytest_cache`, `__pycache__`, JUnit XML, or Vitest output under `Data/Engine`, `Data/Agent`, or `Data/Engine/Containers/webui-frontend/data/web-interface`.
- When adding Python or WebUI tests, place them under the nearest `Unit_Tests` folder and reuse helpers before inventing new setup code. When adding Go Agent tests, keep them package-local as `*_test.go` files and run them through the Agent lane scripts under `Data/Agent/Unit_Tests`.
- When a test needs fake Engine, fake device, fake config, or fake Role state, add helper capability under nearest `support/` package first, then call it from individual tests.
- Keep helper defaults realistic for Borealis code paths, and expose test-specific changes through keyword overrides.
- If domain membership changes, update this page and the matching lane script in same commit.

## Related Documentation
- [Testing Regressions](testing-regressions.md)
- [Architecture Overview](architecture-overview.md)
- [Engine Runtime](../Core%20Runtimes/engine-runtime.md)
- [Agent Runtime](../Core%20Runtimes/agent-runtime.md)
