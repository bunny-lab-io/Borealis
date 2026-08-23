# Overview
<figure class="bo-screenshot">
  <img src="Reference/Branding/Borealis_Logo_Full.png" alt="Borealis Automation Platform" loading="lazy">
</figure>
Borealis is a self-hosted remote management, monitoring, and visual automation platform built around a Linux-hosted management Engine and a cross-platform Agent runtime. It replaces separate homelab and real-world operations tools with one cohesive operator interface.

Borealis combines useful parts of RMM platforms, Ansible/AWX-style automation, scheduled jobs, watchdog remediation, remote desktop and interactive shell access, file/software/process/service management, and credential-backed infrastructure execution.

## Development Pace
Borealis is maintained by one person while working a full-time IT job. Progress is iterative, and some internals get reworked as better architecture emerges. Current focus is turning the automation and remote-operations core into a broader MSP-ready platform.

## High-Level Architecture Overview
Borealis has two main runtime sides: a Linux-hosted Engine server and cross-platform Agent clients. See [Architecture Overview](Reference/architecture-overview.md) for component roles, runtime boundaries, data flow, and debugging entrypoints.

## Feature Support Matrix
Status means productized support in current Borealis codebase and docs, not long-term intent. `Full` means supported on that endpoint path today. `Partial` means useful implementation exists but gaps or validation remain. `-` means no productized endpoint support or OS scope does not apply.

=== "Agent (Client)"

    | Feature | What it Does | Windows | Linux | macOS |
    | --- | --- | --- | --- | --- |
    | Agent Runtime | Script-staged Python Agent with role loading, enrollment, telemetry, and remote-operation roles. | Full | Full | - |
    | Inventory Collection | Collect hardware, OS, software, services, sessions, status, and health payloads from endpoint. | Full | Full | - |
    | WireGuard Tunnel | Maintain outbound WireGuard transport for remote operations and Engine-side automation reachability. | Full | Full | - |
    | Remote Shell Host | Expose interactive shell over managed WireGuard tunnel. | Full | Full | - |
    | Remote Desktop | Run endpoint-side remote desktop service used by Apache Guacamole browser sessions. | Full | - | - |
    | File Operations | Browse, upload, folder-upload, download, cancel transfers, copy, cut, paste, rename, move, delete, create folders, create text files, and edit text files remotely. | Full | Full | - |
    | Registry Operations | Browse and edit Windows registry keys and values from Device Summary. | Full | - | - |
    | Process Operations | Report live process data and accept process-control actions such as End Task. | Full | Full | - |
    | Service Operations | Report service inventory and accept start, stop, and restart actions. | Full | Full | - |
    | Software Operations | Report installed software, refresh inventory, and support software-management actions. | Full | Full | - |
    | Signed Script Execution | Validate signed payloads and run scripts in supported contexts. | Full | Full | - |
    | Watchdog Inputs and Remediation | Provide endpoint telemetry used by watchdogs and execute remediation assemblies when dispatched. | Full | Full | - |
    | Device Identity and Tunnel Trust | Use Ed25519 device identity, short-lived tunnel tokens, and public CA/hostname validation. | Full | Full | - |

=== "Engine (Server)"

    | Feature | What it Does |
    | --- | --- |
    | Device Inventory Store | Store device inventory, status, health, software, services, sessions, and activity history in PostgreSQL. |
    | Sites, Agent Approvals, and RBAC | Scope devices by site, approve agent enrollments, and restrict operators by site. |
    | Device Filters | Build typed filters, preview matches, scope automations by site, and save per-operator device-list views. |
    | Remote Operations API and UI | Provide operator-facing APIs and UI for shell, desktop, files, processes, services, and software actions. |
    | Scheduled and Quick Jobs | Dispatch signed scripts, workflows, and Engine-side Ansible playbook runs with target history. |
    | Workflow Editor | Build and run graph-based automation from web UI. |
    | Watchdogs and Auto-Remediation | Preview watchdog matches, track incidents, suppress noise, and dispatch remediation automations. |
    | Aurora Content Repository | Ingest official assemblies, scripts, and playbooks while keeping local user assemblies on Engine. |
    | Engine-side Ansible | Run SSH or WinRM automation from Linux Engine over Borealis-managed WireGuard sessions. |
    | Aegis Cipher | Protect reusable machine credentials, operator password hashes, TOTP secrets, passkey data, and GitHub token storage with `scrypt` plus `AES-256-GCM`. |
    | MFA, Passkeys, and Sessions | Require Aegis unlock, enforce MFA by default, support WebAuthn passkeys, and invalidate sessions strictly. |
    | Code Signing | Sign script delivery and enforce trusted execution payloads. |
    | REST/API Surface | Expose authenticated APIs for devices, jobs, files, registry, processes, services, software, filters, sites, logs, and runtime operations. |
    | Reporting | Track device activity history, scheduled job run history, alerts, and ansible recap data. |

## Getting Started
Deploy the Borealis Engine to a Linux host via the [Engine Deployment](Engine/deploying-the-engine.md) documentation.

??? example "Detailed Codex Breakdown"

    Start with `AGENTS.md` at the repo root, then use this documentation site as the knowledgebase entrypoint.

    Runtime source locations:

    - Engine package shim and tests: `Data/Engine/`.
    - Engine public API source code: `Data/Engine/Containers/api-backend/cmd/api-backend/`; retained Python worker support: `Data/Engine/Containers/site-worker/data/`.
    - Agent source code: `Data/Agent/`.
    - Web UI source: `Data/Engine/Containers/webui-frontend/data/web-interface/src/`.
    - Runtime copies: `Engine/` and `Agent/`; do not edit directly.
    - Logs: `Engine/Services/api-backend/logs/` and `Agent/Logs/`.

    Authoring rules:

    - Keep new documentation inside closest domain folder.
    - Do not manually add pages to `Docs/zensical.toml`; Zensical discovers Markdown files from `Docs/`.
    - Use ASCII unless existing file already uses Unicode.
    - Avoid duplicating long source code; link to files and summarize behavior.
    - Document UI and backend components together when both change.
    - Follow `Docs/Engine/deploying-the-engine.md` for page shape: short opening explanation, clear requirements, normal path first, optional paths collapsed, and Codex detail hidden.
    - Do not add visible `Purpose`, `API Endpoints`, `Related Documentation`, source map, or implementation-note sections. Put that material inside final `??? example "Detailed Codex Breakdown"` sections.
    - Keep screenshots on [Screenshots](screenshots.md) by default. Use one high-signal screenshot per topic page when it helps orient operators.
    - Put Codex-only guidance at the end of each page in `??? example "Detailed Codex Breakdown"`.
    - Use GitHub issues labeled `Technical Debt` for workarounds, non-standard build steps, or dev/prod drift.

    Documentation Map:

    - [Engine Deployment](Engine/deploying-the-engine.md) - install path, architecture, security, UI rules, and testing entrypoints.
    - [Screenshots](screenshots.md) - visual tour of Borealis operator surfaces.
    - [Using the Platform](Using%20the%20Platform/index.md) - operator workflows for devices, sites, remote operations, automation, access, logs, and software.
    - [Assemblies](Using%20the%20Platform/Assemblies/assemblies.md) - scripts, workflows, Ansible playbooks, quick jobs, and Aurora content.
    - [Reference](Reference/index.md) - runtime, Docker stack, API, database, integration, and SBOM references.
    - [Development](Reference/Unit_Testing.md) - testing and migration guidance.
    - [Roadmap](Reference/Future_Roadmaps/index.md) - competitive gaps and roadmap pressure.

    Repository References:

    - [README](https://github.com/bunny-lab-io/Borealis/blob/main/README.md)
    - [AGENTS.md](https://github.com/bunny-lab-io/Borealis/blob/main/AGENTS.md)
    - [Engine Unit Test Script](https://github.com/bunny-lab-io/Borealis/blob/main/Engine_Unit_Tests.sh)
    - [Linux Agent Unit Test Script](https://github.com/bunny-lab-io/Borealis/blob/main/Data/Agent/Unit_Tests/Agent_Unit_Tests.sh)
    - [Windows Agent Unit Test Script](https://github.com/bunny-lab-io/Borealis/blob/main/Data/Agent/Unit_Tests/Agent_Unit_Tests.ps1)
    - [Technical Debt issues](https://github.com/bunny-lab-io/Borealis/issues?q=is%3Aissue%20label%3A%22Technical%20Debt%22)
