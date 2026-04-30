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

## Current Known Failures
- The formalization keeps current failing tests visible instead of deleting or hiding them.
- See [Testing Regressions](testing-regressions.md) for known failure status and cleanup guidance.

## Codex Agent
- Do not add domain-specific runner flags. The supported operator model is full Engine or full Agent.
- Use `PYTHONDONTWRITEBYTECODE=1` when running tests so `__pycache__` does not appear under source folders.
- Keep result artifacts under `Unit_Test_Results/`; do not write reports under `Data/Engine`, `Data/Agent`, or `Data/Engine/web-interface`.
- When adding tests, place them under the appropriate `Unit_Tests` folder and update this page if the operator command changes.
- When a test captures a known regression, add or update a row in `testing-regressions.md` so operators know whether the failure is current, fixed, stale, environment-specific, or flaky.

## Related Documentation
- [Architecture Overview](architecture-overview.md)
- [Engine Runtime](engine-runtime.md)
- [Agent Runtime](agent-runtime.md)
- [Testing Regressions](testing-regressions.md)
