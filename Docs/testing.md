# Borealis Testing
[Back to Docs Index](index.md) | [Index (HTML)](index.html)

## Purpose
Explain how operators and Codex run Borealis unit tests after the test layout formalization.

## What Unit Tests Mean Here
- Unit tests check focused pieces of Borealis code with fake inputs, temporary files, mocks, and in-memory or temporary databases.
- Unit tests do not replace a live Engine deployment test, Agent enrollment test, browser smoke test, or remote-access test against real devices.
- Borealis now keeps authored unit tests under `Unit_Tests` folders so operators do not need to hunt through mixed `tests` directories.

## Test Locations
- Engine Python unit tests: `Data/Engine/Unit_Tests/`.
- Engine assembly unit tests: `Data/Engine/Unit_Tests/assemblies/`.
- Agent Python unit tests: `Data/Agent/Unit_Tests/`.
- WebUI unit tests: `Data/Engine/web-interface/Unit_Tests/`.
- Runtime WebUI unit tests after Engine redeploy: `Engine/web-interface/Unit_Tests/`.

## Operator Commands
Run from the repository root.

```bash
./Engine_Unit_Tests.sh
```

Runs all Engine Python unit tests and the staged Engine WebUI unit tests. The WebUI lane intentionally runs from `Engine/web-interface`, because Borealis serves and validates runtime-staged UI assets there.

```bash
./Agent_Unit_Tests.sh
```

Runs all Agent Python unit tests from Linux or another POSIX shell.

```powershell
.\Agent_Unit_Tests.ps1
```

Runs all Agent Python unit tests from Windows PowerShell.

## Results
- Results write to `Unit_Test_Results/<runtime>-<timestamp>/`.
- Python lanes write `*-pytest.log` and `*-pytest.xml`.
- WebUI lanes write `engine-webui-vitest.log` and, when Vitest reaches report generation, `engine-webui-vitest.xml`.
- `summary.txt` records each lane exit status.

## Expected Setup
- Run scripts from the repo root.
- Engine tests prefer `Engine/bin/python` when available because the Engine venv includes pytest.
- Agent tests use the first Python interpreter with pytest available, checking Agent runtime, Engine runtime, and system Python in that order.
- WebUI tests require `Engine/web-interface/node_modules` and staged `Engine/web-interface/Unit_Tests`.
- If the WebUI runtime is stale, run the normal Engine redeploy path first so `Data/Engine/web-interface/Unit_Tests` is copied into the runtime tree.

## Shared Test Helpers
- Shared Engine helpers live under `Data/Engine/Unit_Tests/support/`.
- Shared Agent helpers live under `Data/Agent/Unit_Tests/support/`.
- Engine helper focus: authenticated Flask clients, temporary Engine database access, and fake devices with valid defaults.
- Software inventory helpers isolate icon override, uninstall override, and uninstall blocklist JSON so unit tests do not depend on live operator config.
- Agent helper focus: Role objects built with `__new__`, fake status hooks, and future fake Agent/device runtime pieces.
- Prefer small helpers such as `admin_client(engine_harness)`, `make_device(...)`, and `set_device_services(...)`.
- Avoid one giant fake Borealis environment helper. Build reusable pieces instead: one helper for Engine/client state, one helper for devices, one helper for Role runtime hooks, and so on.

## Current Known Failures
- The formalization keeps current failing tests visible instead of deleting or hiding them.
- See [Testing Regressions](testing-regressions.md) for known failure status and cleanup guidance.

## Codex Agent
- Do not add domain-specific runner flags. The supported operator model is full Engine or full Agent.
- Use `PYTHONDONTWRITEBYTECODE=1` when running tests so `__pycache__` does not appear under source folders.
- Keep result artifacts under `Unit_Test_Results/`; do not write reports under `Data/Engine`, `Data/Agent`, or `Data/Engine/web-interface`.
- When adding tests, place them under the appropriate `Unit_Tests` folder and update this page if the operator command changes.
- When tests need fake Engine, fake device, or fake Role state, add the helper under the nearest `support/` package first, then call it from individual tests.
- Keep helper defaults realistic enough for existing Borealis code paths, and make test-specific differences explicit through keyword overrides.
- When a test captures a known regression, add or update a row in `testing-regressions.md` so operators know whether the failure is current, fixed, stale, environment-specific, or flaky.

## Related Documentation
- [Architecture Overview](architecture-overview.md)
- [Engine Runtime](engine-runtime.md)
- [Agent Runtime](agent-runtime.md)
- [Testing Regressions](testing-regressions.md)
