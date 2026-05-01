# Borealis Knowledgebase Index
[Index (HTML)](index.html)

## Purpose
This page is the navigation hub for the Borealis documentation set. The knowledgebase now includes the full content that previously lived under `Docs/Codex` and `Docs/Agent`, compiled into the relevant pages below.

## Table of Contents
### Start Here
- [Getting Started](getting-started.md)
- [Architecture Overview](architecture-overview.md)
- [Unit Testing](Unit_Testing.md)
- [Testing Regressions](testing-regressions.md)

### Core Runtimes
- [Engine Runtime](engine-runtime.md)
- [Agent Runtime](agent-runtime.md)

### Data and Schema
- [Database Reference](db-reference.md)

### Security and Trust
- [Security and Trust](security-and-trust.md)

### Automation and Execution
- [Assemblies and Quick Jobs](assemblies.md)
- [Flow Editor and Nodes](flow-editor-and-nodes.md)
- [Scheduled Jobs](scheduled-jobs.md)
- [Ansible SSH Connection Logic](Ansible/SSH_Connection_Logic.md)
- [Watchdogs](watchdogs.md)

### Operations and Remote Access
- [Device Management](device-management.md)
- [Device Alerts](device-alerts.md)
- [VPN and Remote Access](vpn-and-remote-access.md)
- [Logging and Operations](logging-and-operations.md)

### Software Management
- [Software Icon Overrides](Software%20Management/adding-software-to-icon-overrides.md)
- [Software Uninstall Blocklist](Software%20Management/adding-software-to-uninstall-blocklist.md)
- [Software Uninstall Overrides](Software%20Management/adding-software-to-uninstall-overrides.md)

### UI and API
- [UI and Notifications](ui-and-notifications.md)
- [Migrating Pages to React Router](migrating-pages-to-react-router.md)
- [API Reference](api-reference.md)
- [Technical Debt issues](https://github.com/bunny-lab-io/Borealis/issues?q=is%3Aissue%20label%3A%22Technical%20Debt%22)

### Integrations
- [Integrations](integrations.md)

### Key Repo References
- [README](../README.md)
- [AGENTS.md](../AGENTS.md)
- [Unit test scripts](../Engine_Unit_Tests.sh)

## API Endpoints
None. This index only links to other pages.

## Related Documentation
- See the Table of Contents above for the primary knowledgebase pages.

## Codex Agent (Detailed)
### How to use this knowledgebase
- Start with `AGENTS.md` at the repo root.
- Read `getting-started.md` and `architecture-overview.md` to build the global model.
- Use `engine-runtime.md` and `agent-runtime.md` for implementation-level details.
- Use `db-reference.md` for PostgreSQL table ownership, relationships, and legacy migration notes.
- Use `assemblies.md`, `flow-editor-and-nodes.md`, `scheduled-jobs.md`, and `watchdogs.md` for automation authoring and execution behavior.
- Use `device-management.md`, `device-alerts.md`, `logging-and-operations.md`, and `vpn-and-remote-access.md` for operational workflows, incident handling, and remote access behavior.
- Use `ui-and-notifications.md` for MagicUI, AG Grid, and toast notification rules.
- Use `migrating-pages-to-react-router.md` when moving legacy WebUI pages onto the shared router and app shell.
- Use `api-reference.md` and `integrations.md` for public API surfaces and external service behavior.
- Use `security-and-trust.md` for enrollment, tokens, and code-signing behavior.
- Use `Unit_Testing.md` and `testing-regressions.md` for unit test commands, domain selection, helper rules, result locations, and known regression status.
- Use GitHub issues labeled `Technical Debt` when documenting workarounds, non-standard build steps, or dev/prod drift.

### Where the truth lives in code
- Engine package shim and tests: `Data/Engine/`.
- Engine API source code: `Data/Engine/Containers/api-backend/data/` (edit here).
- Agent source code: `Data/Agent/` (edit here).
- Web UI source: `Data/Engine/Containers/webui-frontend/data/web-interface/src/`.
- Runtime copies: `Engine/` and `Agent/` (do not edit directly; they are regenerated).
- Logs: `Engine/Services/api-backend/logs/` and `Agent/Logs/` (runtime artifacts).
- Official assembly snapshot: `Data/Engine/Containers/api-backend/data/Official_Assemblies/` (generated bundled seed snapshot).
- Runtime assembly data: PostgreSQL `assemblies.*` tables.

### Documentation authoring rules
- Keep filenames lowercase with hyphens (example: `device-management.md`) except established operator entrypoints such as `Unit_Testing.md`.
- Add a top-of-page link back to the index: `[Back to Docs Index](index.md) | [Index (HTML)](index.html)`.
- For docs in subfolders, use relative paths (example: `../index.md`).
- Use ASCII characters only unless the file already uses Unicode.
- Avoid duplicating long source code; paraphrase and point to files instead.
- When a feature has UI and backend components, document both and link the relevant files.
- Codex Agent sections must remain verbose and example-driven; they now hold the full former Codex content.

### Cross-linking and maintenance
- Link outward to adjacent domains (example: device management should link to filters, scheduled jobs, VPN).
- When adding a new doc, add it to the Table of Contents and add at least two Related Documentation links from other pages.
- Keep Codex Agent sections detailed so a new agent can act without extra discovery.

### Update workflow example
- Change: add a new endpoint in `Data/Engine/Containers/api-backend/data/services/API/devices/management.py`.
- Update steps:
  1) Add the endpoint to the file header in that module.
  2) Update `api-reference.md` under the Devices and Inventory section.
  3) Update `device-management.md` with the new endpoint and behavior.
  4) If UI changes are involved, update `ui-and-notifications.md`.

### Editing safety reminders
- Do not edit runtime directories `Engine/` or `Agent/`.
- Prefer reading with `rg` for quick discovery and update docs after code changes.
- If you notice unexpected changes in git, pause and clarify before proceeding.
