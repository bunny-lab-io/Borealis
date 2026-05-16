# Borealis Knowledgebase Index
[Index (HTML)](website/index.html)

## Purpose
This page is the navigation hub for the Borealis documentation set. The knowledgebase is organized by domain folders under `Docs/`.

## Table Of Contents
### Start Here
- [Getting Started](Start%20Here/getting-started.md)
- [Architecture Overview](Start%20Here/architecture-overview.md)
- [Security and Trust](Start%20Here/security-and-trust.md)
- [UI and Notifications](Start%20Here/ui-and-notifications.md)
- [Unit Testing](Start%20Here/Unit_Testing.md)
- [Testing](Start%20Here/testing.md)
- [Testing Regressions](Start%20Here/testing-regressions.md)

### Core Runtimes
- [Engine Runtime](Core%20Runtimes/engine-runtime.md)
- [Agent Runtime](Core%20Runtimes/agent-runtime.md)
- [Docker Stack Breakdown](Core%20Runtimes/Stack_Breakdown.md)

### Data And Schema
- [Database Reference](Data%20and%20Schema/db-reference.md)
- [API Reference](Data%20and%20Schema/api-reference.md)
- [Integrations](Data%20and%20Schema/integrations.md)

### Automation And Execution
- [Assemblies and Quick Jobs](Automation%20and%20Execution/assemblies.md)
- [Flow Editor and Nodes](Automation%20and%20Execution/flow-editor-and-nodes.md)
- [Scheduled Jobs](Automation%20and%20Execution/scheduled-jobs.md)
- [SSH Connection Logic](Automation%20and%20Execution/SSH_Connection_Logic.md)
- [Watchdogs](Automation%20and%20Execution/watchdogs.md)

### Operations And Remote Access
- [Device Management](Operations%20and%20Remote%20Access/device-management.md)
- [Device Alerts](Operations%20and%20Remote%20Access/device-alerts.md)
- [VPN and Remote Access](Operations%20and%20Remote%20Access/vpn-and-remote-access.md)
- [Logging and Operations](Operations%20and%20Remote%20Access/logging-and-operations.md)

### Software Management
- [Software Icon Overrides](Software%20Management/adding-software-to-icon-overrides.md)
- [Software Uninstall Blocklist](Software%20Management/adding-software-to-uninstall-blocklist.md)
- [Software Uninstall Overrides](Software%20Management/adding-software-to-uninstall-overrides.md)

### Migration Paths
- [Migrating Pages to React Router](Migration%20Paths/migrating-pages-to-react-router.md)

### Future Roadmaps
- [Competitive Feature Gap Analysis](Future_Roadmaps/competitive-feature-gap-analysis.md)

### Assets
- [Branding Assets](Branding/)
- [Repository Screenshots](images/repo_screenshots/)
- [Static Website Index](website/index.html)

### External References
- [Technical Debt issues](https://github.com/bunny-lab-io/Borealis/issues?q=is%3Aissue%20label%3A%22Technical%20Debt%22)

### Key Repo References
- [README](../README.md)
- [AGENTS.md](../AGENTS.md)
- [Engine Unit Test Script](../Engine_Unit_Tests.sh)
- [Linux Agent Unit Test Script](../Data/Agent/Unit_Tests/Agent_Unit_Tests.sh)
- [Windows Agent Unit Test Script](../Data/Agent/Unit_Tests/Agent_Unit_Tests.ps1)

## API Endpoints
None. This index only links to other pages.

## Related Documentation
- See the Table of Contents above for the primary knowledgebase pages.

## Codex Agent
### How To Use This Knowledgebase
- Start with `AGENTS.md` at the repo root.
- Read `Docs/Start Here/getting-started.md` and `Docs/Start Here/architecture-overview.md` to build the global model.
- Use `Docs/Core Runtimes/engine-runtime.md`, `Docs/Core Runtimes/agent-runtime.md`, and `Docs/Core Runtimes/Stack_Breakdown.md` for implementation-level runtime details.
- Use `Docs/Data and Schema/db-reference.md` for PostgreSQL table ownership, relationships, and DB lifecycle guardrails.
- Use `Docs/Automation and Execution/assemblies.md`, `Docs/Automation and Execution/flow-editor-and-nodes.md`, `Docs/Automation and Execution/scheduled-jobs.md`, `Docs/Automation and Execution/SSH_Connection_Logic.md`, and `Docs/Automation and Execution/watchdogs.md` for automation authoring and execution behavior.
- Use `Docs/Operations and Remote Access/device-management.md`, `Docs/Operations and Remote Access/device-alerts.md`, `Docs/Operations and Remote Access/logging-and-operations.md`, and `Docs/Operations and Remote Access/vpn-and-remote-access.md` for operational workflows, incident handling, and remote access behavior.
- Use `Docs/Start Here/ui-and-notifications.md` for MagicUI, AG Grid, toast notification, and shared UI rules.
- Use `Docs/Migration Paths/migrating-pages-to-react-router.md` when moving legacy WebUI pages onto the shared router and app shell.
- Use `Docs/Data and Schema/api-reference.md` and `Docs/Data and Schema/integrations.md` for API surfaces and external service behavior.
- Use `Docs/Start Here/security-and-trust.md` for enrollment, tokens, Aegis, and code-signing behavior.
- Use `Docs/Start Here/Unit_Testing.md` and `Docs/Start Here/testing-regressions.md` for unit test commands, domain selection, helper rules, result locations, and known regression status.
- Use GitHub issues labeled `Technical Debt` when documenting workarounds, non-standard build steps, or dev/prod drift.

### Where Truth Lives In Code
- Engine package shim and tests: `Data/Engine/`.
- Engine API source code: `Data/Engine/Containers/api-backend/data/` (edit here).
- Agent source code: `Data/Agent/` (edit here).
- Web UI source: `Data/Engine/Containers/webui-frontend/data/web-interface/src/`.
- Runtime copies: `Engine/` and `Agent/` (do not edit directly; they are regenerated).
- Logs: `Engine/Services/api-backend/logs/` and `Agent/Logs/` (runtime artifacts).
- Official assembly snapshot: `Data/Engine/Containers/api-backend/data/Official_Assemblies/` (generated bundled seed snapshot).
- Runtime assembly data: PostgreSQL `assemblies.*` tables.

### Documentation Authoring Rules
- Keep new documentation inside the closest domain folder.
- Add a top-of-page link back to the index. For docs in subfolders, link `Back to Docs Index` to `../index.md` and `Index (HTML)` to `../website/index.html`.
- Use ASCII characters only unless the file already uses Unicode.
- Avoid duplicating long source code; paraphrase and point to files instead.
- When a feature has UI and backend components, document both and link the relevant files.
- Codex Agent sections should stay detailed enough for a future agent to act without rediscovery.

### Cross-Linking And Maintenance
- Link outward to adjacent domains. Example: device management should link to filters, scheduled jobs, VPN, and API reference.
- When adding a new doc, add it to this Table of Contents and add at least two Related Documentation links from other pages.
- When moving docs between folders, update this index and any Related Documentation links that still point at old root-level paths.

### Update Workflow Example
- Change: add a new endpoint in `Data/Engine/Containers/api-backend/data/services/API/devices/management.py`.
- Update steps:
  1. Add the endpoint to the file header in that module.
  2. Update `Docs/Data and Schema/api-reference.md` under the Devices and Inventory section.
  3. Update `Docs/Operations and Remote Access/device-management.md` with the new endpoint and behavior.
  4. If UI changes are involved, update `Docs/Start Here/ui-and-notifications.md`.

### Editing Safety Reminders
- Do not edit runtime directories `Engine/` or `Agent/`.
- Prefer reading with `rg` or `find` for quick discovery and update docs after code changes.
- If you notice unexpected changes in git, identify them before editing and do not revert user work.
