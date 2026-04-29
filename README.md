![Borealis Logo](Data/Engine/web-interface/public/Borealis_Logo_Full.png)

# Why Borealis Exists
Borealis was created to replace a pile of separate homelab and real-world operations tools with one cohesive platform.  Borealis is a remote management, monitoring, and automation platform built around a Linux-hosted management Engine and a cross-platform Agent runtime

It combines the useful parts of various RMM platforms, Ansible/AWX-style automation, scheduled jobs, watchdog remediation, remote desktop and interactive shell access, file/software/process/service management, and credential-backed infrastructure execution into one single operator interface.

## Project Development Pace
Borealis is maintained by one person while working a full-time IT job. Progress is iterative, and some internals get reworked as better architecture emerges. Current focus is turning the strong automation and remote-operations core into a broader MSP-ready platform.

## Feature Support Matrix
Status means productized support in the current Borealis codebase and docs, not long-term intent. `Full` means supported on that endpoint path today. `Partial` means useful implementation exists but gaps or validation remain. `-` means no productized endpoint support or OS scope does not apply. Engine features use `-` for OS columns because they are Engine-side capabilities.

| Scope | Feature | What it Does | Windows | Linux | macOS |
| --- | --- | --- | --- | --- | --- |
| Agent | Agent Runtime | Script-staged Python Agent with role loading, enrollment, telemetry, and remote-operation roles. | Full | Partial | - |
| Agent | Inventory Collection | Collect hardware, OS, software, services, sessions, status, and health payloads from the endpoint. | Full | Partial | - |
| Agent | WireGuard Tunnel | Maintain outbound WireGuard transport for remote operations and Engine-side automation reachability. | Full | Full | - |
| Agent | Remote Shell Host | Expose an interactive shell over the managed WireGuard tunnel. | Full | Full | - |
| Agent | Remote Desktop Host | Run the endpoint-side remote desktop service used by browser-based noVNC sessions. | Full | - | - |
| Agent | File Operations | Browse, upload, folder-upload, download, cancel transfers, copy, cut, paste, rename, move, delete, create folders, and edit text files remotely. | Full | Full | - |
| Agent | Process Operations | Report live process data and accept process-control actions such as End Task. | Full | Partial | - |
| Agent | Service Operations | Report service inventory and accept start, stop, and restart actions. | Full | Partial | - |
| Agent | Software Operations | Report installed software, refresh inventory, and support software-management actions. | Full | Partial | - |
| Agent | Signed Script Execution | Validate signed payloads and run scripts in supported contexts. | Full | Full | - |
| Agent | Watchdog Inputs and Remediation | Provide endpoint telemetry used by watchdogs and execute remediation assemblies when dispatched. | Full | Partial | - |
| Agent | Device Identity and Tunnel Trust | Use Ed25519 device identity, short-lived tunnel tokens, and public CA/hostname validation. | Full | Full | - |
| Engine | Device Inventory Store | Store device inventory, status, health, software, services, sessions, and activity history in PostgreSQL. | - | - | - |
| Engine | Sites, Agent Approvals, and RBAC | Scope devices by site, approve agent enrollments, and restrict operators by site. | - | - | - |
| Engine | Device Filters | Build typed filters, preview matches, scope automations by site, and save per-operator device-list views. | - | - | - |
| Engine | Remote Operations API and UI | Provide operator-facing APIs and UI for shell, desktop, files, processes, services, and software actions. | - | - | - |
| Engine | Scheduled and Quick Jobs | Dispatch signed scripts, workflows, and Engine-side Ansible playbook runs with target history. | - | - | - |
| Engine | Workflow Editor | Build and run graph-based automation from the web UI. | - | - | - |
| Engine | Watchdogs and Auto-Remediation | Preview watchdog matches, track incidents, suppress noise, and dispatch remediation automations. | - | - | - |
| Engine | Aurora Content Repository | Ingest official assemblies, scripts, and playbooks while keeping local user assemblies on the Engine. | - | - | - |
| Engine | Engine-side Ansible | Run SSH or WinRM automation from the Linux Engine over Borealis-managed WireGuard sessions. | - | - | - |
| Engine | Aegis Cipher | Protect reusable machine credentials, operator password hashes, TOTP secrets, passkey data, and GitHub token storage with `scrypt` plus `AES-256-GCM`. | - | - | - |
| Engine | MFA, Passkeys, and Sessions | Require Aegis unlock, enforce MFA by default, support WebAuthn passkeys, and invalidate sessions strictly. | - | - | - |
| Engine | Code Signing | Sign script delivery and enforce trusted execution payloads. | - | - | - |
| Engine | REST/API Surface | Expose authenticated APIs for devices, jobs, files, processes, services, software, filters, sites, logs, and runtime operations. | - | - | - |
| Engine | Reporting | Track device activity history, scheduled job run history, alerts, and ansible recap data. | - | - | - |

## Architecture
- **Engine**: Linux-hosted single-node control plane with Python services, PostgreSQL, Traefik, WebSockets, scheduling, automation, and the web UI.
- **Agent**: Script-staged cross-platform runtime with Windows as the reference path, Linux as a partial-but-working path, role-based capabilities, signed work execution, inventory, WireGuard, remote shell, file management, and remote operation roles.
- **Transport**: Agents connect outbound. Remote operations use WireGuard sessions with strict `/32` isolation and Engine-controlled port allowlists.
- **Data Layer**: PostgreSQL stores devices, inventory, jobs, activity history, alerts, assemblies, credentials metadata, and operational state.
- **Automation Model**: Assemblies can run through quick jobs, scheduled jobs, workflows, watchdog remediation, and Engine-side Ansible playbooks.
- **Security Model**: Aegis Cipher system protects secrets, MFA is required by default, passkeys are supported, scripts are signed, and WireGuard tunnel access uses short-lived tokens.

## Engine Deployment Profiles
`Borealis.sh` profiles CPU and RAM during Engine deployment, prints the detected profile, and auto-configures PostgreSQL plus Engine DB tuning. Storage guidance is advisory and mostly depends on retention policy, job output, logs, and inventory volume.

| Profile | Typical use | Endpoints | Active operators | vCPU | RAM | NVMe storage |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Homelab | Personal labs, testing, feature development, very small sites | Up to 250 | 1-3 | < 8 | < 16 GiB | 80-150 GiB |
| Small Business | Smaller production environments | Up to 1,000 | 2-4 | 8-15 | 16-31 GiB | 150-250 GiB |
| MSP / Production | Main Borealis target for SMB and managed-service usage | Up to 2,000 | 4-8 | 16-23 | 32-63 GiB | 500 GiB |
| Enterprise | Larger single-node environments on current architecture | Up to 10,000 | 10-20 | 24+ | 64 GiB+ | 500 GiB-1 TiB |
| Enterprise Clustered | Roadmap-only multi-node planning placeholder | 10,000+ | 20+ per node | 24+ per node | 64 GiB+ per node | 500 GiB-1 TiB per node |

## Getting Started
Deploy the Borealis Engine to a Linux host:

```sh
curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/bootstrap.sh | sudo bash -s --
```