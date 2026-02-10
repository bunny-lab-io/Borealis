# Borealis Knowledgebase Index
[Index (HTML)](index.html)

## Purpose
This page is the navigation hub for the Borealis documentation set. The knowledgebase now includes the full content that previously lived under `Docs/Codex` and `Docs/Agent`, compiled into the relevant pages below.

## Table of Contents
### Start Here
- [Getting Started](getting-started.md)
- [Architecture Overview](architecture-overview.md)

### Core Runtimes
- [Engine Runtime](engine-runtime.md)
- [Agent Runtime](agent-runtime.md)

### Security and Trust
- [Security and Trust](security-and-trust.md)

### Automation and Execution
- [Assemblies and Quick Jobs](assemblies.md)
- [Flow Editor and Nodes](flow-editor-and-nodes.md)
- [Scheduled Jobs](scheduled-jobs.md)

### Operations and Remote Access
- [Device Management](device-management.md)
- [VPN and Remote Access](vpn-and-remote-access.md)
- [Logging and Operations](logging-and-operations.md)

### UI and API
- [UI and Notifications](ui-and-notifications.md)
- [API Reference](api-reference.md)
- [Technical Debt](technical-debt.md)

### Integrations
- [Integrations](integrations.md)

### Key Repo References
- [README](../README.md)
- [AGENTS.md](../AGENTS.md)

## API Endpoints
None. This index only links to other pages.

## Related Documentation
- See the Table of Contents above for the primary knowledgebase pages.

## Codex Agent (Detailed)
### How to use this knowledgebase
- Start with `AGENTS.md` at the repo root.
- Read `getting-started.md` and `architecture-overview.md` to build the global model.
- Use `engine-runtime.md` and `agent-runtime.md` for implementation-level details.
- Use `ui-and-notifications.md` for MagicUI, AG Grid, and toast notification rules.
- Use `vpn-and-remote-access.md` for WireGuard and remote shell/VNC details.
- Use `security-and-trust.md` for enrollment, tokens, and code-signing behavior.

### Where the truth lives in code
- Engine source code: `Data/Engine/` (edit here).
- Agent source code: `Data/Agent/` (edit here).
- Web UI source: `Data/Engine/web-interface/src/`.
- Runtime copies: `Engine/` and `Agent/` (do not edit directly; they are regenerated).
- Logs: `Engine/Logs/` and `Agent/Logs/` (runtime artifacts).
- Assemblies data: `Data/Engine/Assemblies/` (staging) and `Engine/Assemblies/` (runtime mirror).

### Documentation authoring rules
- Keep filenames lowercase with hyphens (example: `device-management.md`).
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
- Change: add a new endpoint in `Data/Engine/services/API/devices/management.py`.
- Update steps:
  1) Add the endpoint to the file header in that module.
  2) Update `api-reference.md` under the Devices and Inventory section.
  3) Update `device-management.md` with the new endpoint and behavior.
  4) If UI changes are involved, update `ui-and-notifications.md`.

### Editing safety reminders
- Do not edit runtime directories `Engine/` or `Agent/`.
- Prefer reading with `rg` for quick discovery and update docs after code changes.
- If you notice unexpected changes in git, pause and clarify before proceeding.
