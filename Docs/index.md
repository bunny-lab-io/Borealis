# Borealis Documentation

Borealis is a self-hosted remote management and visual automation platform. These docs cover deployment, agent behavior, device operations, automation authoring, remote access, database ownership, and contributor guardrails.

Start with the operator guides when deploying or running Borealis. Use runtime, schema, and API references when changing code or troubleshooting production behavior.

<figure class="bo-screenshot">
  <img src="images/repo_screenshots/Device_List.png" alt="Borealis device list page" loading="lazy">
  <figcaption>Device List is the normal operator entrypoint for managed fleet work.</figcaption>
</figure>

## Choose Starting Point

<div class="bo-card-grid" markdown>

<div class="bo-card" markdown>
**New Operators**

[Getting Started](Start%20Here/getting-started.md) covers Engine bootstrap, optional Agent install, and first-run checks.
</div>

<div class="bo-card" markdown>
**Runtime Maintainers**

[Architecture Overview](Start%20Here/architecture-overview.md), [Engine Runtime](Core%20Runtimes/engine-runtime.md), and [Agent Runtime](Core%20Runtimes/agent-runtime.md) explain system shape.
</div>

<div class="bo-card" markdown>
**Fleet Operations**

[Device Management](Operations%20and%20Remote%20Access/device-management.md), [Device Alerts](Operations%20and%20Remote%20Access/device-alerts.md), and [Logging and Operations](Operations%20and%20Remote%20Access/logging-and-operations.md) cover daily operator workflows.
</div>

<div class="bo-card" markdown>
**Automation Authors**

[Assemblies and Quick Jobs](Automation%20and%20Execution/assemblies.md), [Flow Editor and Nodes](Automation%20and%20Execution/flow-editor-and-nodes.md), and [Scheduled Jobs](Automation%20and%20Execution/scheduled-jobs.md) cover automation design and execution.
</div>

<div class="bo-card" markdown>
**Remote Support**

[VPN and Remote Access](Operations%20and%20Remote%20Access/vpn-and-remote-access.md) covers WireGuard tunnels, Remote PowerShell, and browser VNC.
</div>

<div class="bo-card" markdown>
**Contributors**

[Unit Testing](Start%20Here/Unit_Testing.md), [API Reference](Data%20and%20Schema/api-reference.md), and [Database Reference](Data%20and%20Schema/db-reference.md) define validation and shared contracts.
</div>

</div>

## Documentation Map

- [Start Here](Start%20Here/index.md) - install path, architecture, security, UI rules, and testing entrypoints.
- [Operations](Operations%20and%20Remote%20Access/index.md) - device inventory, alerts, remote access, logs, and software management.
- [Automation](Automation%20and%20Execution/index.md) - assemblies, flows, scheduled jobs, SSH logic, and watchdogs.
- [Reference](Core%20Runtimes/index.md) - runtime, Docker stack, API, database, integration, and SBOM references.
- [Development](Start%20Here/Unit_Testing.md) - testing and migration guidance.
- [Roadmap](Future_Roadmaps/index.md) - competitive gaps and roadmap pressure.

## Repository References

- [README](https://github.com/bunny-lab-io/Borealis/blob/main/README.md)
- [AGENTS.md](https://github.com/bunny-lab-io/Borealis/blob/main/AGENTS.md)
- [Engine Unit Test Script](https://github.com/bunny-lab-io/Borealis/blob/main/Engine_Unit_Tests.sh)
- [Linux Agent Unit Test Script](https://github.com/bunny-lab-io/Borealis/blob/main/Data/Agent/Unit_Tests/Agent_Unit_Tests.sh)
- [Windows Agent Unit Test Script](https://github.com/bunny-lab-io/Borealis/blob/main/Data/Agent/Unit_Tests/Agent_Unit_Tests.ps1)
- [Technical Debt issues](https://github.com/bunny-lab-io/Borealis/issues?q=is%3Aissue%20label%3A%22Technical%20Debt%22)

??? example "Detailed Codex Breakdown"

    Start with `AGENTS.md` at the repo root, then use this documentation site as the knowledgebase entrypoint.

    Runtime source locations:

    - Engine package shim and tests: `Data/Engine/`.
    - Engine API source code: `Data/Engine/Containers/api-backend/data/`.
    - Agent source code: `Data/Agent/`.
    - Web UI source: `Data/Engine/Containers/webui-frontend/data/web-interface/src/`.
    - Runtime copies: `Engine/` and `Agent/`; do not edit directly.
    - Logs: `Engine/Services/api-backend/logs/` and `Agent/Logs/`.

    Authoring rules:

    - Keep new documentation inside closest domain folder.
    - Add public pages to `../zensical.toml` navigation.
    - Use ASCII unless existing file already uses Unicode.
    - Avoid duplicating long source code; link to files and summarize behavior.
    - Document UI and backend components together when both change.
    - Put Codex-only guidance at the end of each page in `??? example "Detailed Codex Breakdown"`.
    - Use GitHub issues labeled `Technical Debt` for workarounds, non-standard build steps, or dev/prod drift.
