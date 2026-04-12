![Borealis Logo](Data/Engine/web-interface/public/Borealis_Logo_Full.png)

Borealis is a cross-platform remote management and automation platform with a visual workflow layer, enabling operators to execute scripts, orchestrate infrastructure tasks, and manage distributed systems through a unified interface.

The project was originally created to consolidate the functionality of multiple standalone tools used in my homelab and real-world environments (TacticalRMM, Ansible AWX, SemaphoreUI, etc.) into a single, cohesive platform.

## A Note on Development Pace
I'm the sole maintainer and still learning as I go while working a full-time IT job. Progress is iterative, and parts of the system are occasionally reworked as better architectural approaches emerge.

---

## Documentation
- Human-friendly docs live in `Docs/` with a top-level index at `Docs/index.md`
- The same files also include **Codex Agent** sections with deeper implementation details
- Start with:
  - `Docs/getting-started.md`
  - `Docs/architecture-overview.md`

---

# System Requirements

Borealis currently runs as a single-node Engine deployment that bundles the Python API/runtime, PostgreSQL, Traefik, WebSockets, scheduling, and automation services onto one host. Because of that, the Engine will use the resources you give it in a few predictable ways:

- **CPU**:
  - operator-driven activity such as Web UI requests, AG Grid-backed searches, filtering, and live socket updates
  - scheduler, watchdog evaluation, workflow execution, and assembly dispatch
  - PostgreSQL query execution, indexing, and autovacuum work
- **RAM**:
  - PostgreSQL shared buffers and filesystem cache for device inventory, job history, and alerting data
  - Engine process memory for operator sessions, active WebSocket clients, background workers, and runtime caches
  - temporary headroom during large queries, scheduled job bursts, or watchdog preview/evaluation work
- **Storage**:
  - PostgreSQL tables, indexes, and WAL files
  - Engine logs, job history, and other retained operational artifacts
  - staged runtime assets such as the built Web UI, certificates, and Aurora-managed assembly content

Storage requirements are driven more by retention policy than by the Borealis binaries themselves. Shorter log and job-history retention keeps storage needs much lower, while longer retention and heavier automation output will grow the requirement over time.

## Recommended Deployment Profiles

| Profile | Typical Use | Endpoints | Active Operators | vCPU | RAM | NVMe Storage |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Homelab | Personal labs, testing, feature development, and very small sites | Up to 250 | 1-3 | 4-8 | 8-16 GiB | 80-150 GiB |
| Small Business | Smaller production environments with light-to-moderate operator activity | Up to 1,000 | 2-4 | 8-12 | 16-24 GiB | 150-250 GiB |
| MSP / Production | The main Borealis target for real-world SMB and managed-service usage | Up to 2,000 | 4-8 | 16 | 32 GiB | 500 GiB |
| Enterprise | Larger single-node environments on the current Borealis architecture | Up to 10,000 | 10-20 | 16-24 | 32-64 GiB | 500 GiB-1 TiB |
| Enterprise Clustered `Not Implemented Yet` | Much larger multi-node clustered environments | 10,000+ | 20+ per node | 16-24 per node | 32-64 GiB per node | 500 GiB-1 TiB per node |

## Practical Guidance

- The `Homelab` profile is intended for personal labs, development work, and very small environments where operator concurrency and retained history stay light.
- The `Small Business` profile is intended for smaller real-world deployments and provides a practical production floor before Borealis starts benefiting heavily from larger PostgreSQL cache and job-history headroom.
- The `MSP / Production` profile is the primary Borealis target today and should feel very strong at `2,000` endpoints or less with `4-8` active operators.
- The `Enterprise` profile represents the upper single-node range of the current architecture. It should remain comfortable around `5,000` endpoints with disciplined retention policies and can function decently up to `10,000` endpoints on a well-tuned host.
- All production-oriented profiles benefit most from additional RAM and fast NVMe storage because PostgreSQL cache, WAL activity, alerting history, job history, and device inventory all scale with real usage.
- The clustered enterprise profile is intentionally marked as roadmap-only guidance. It describes the kind of per-node sizing that would likely make sense once Borealis gains orchestrated horizontal scaling, but that deployment model is not implemented today.

## Current Architecture and Future Scale

- The sizing guidance above assumes Borealis is running in its current single-node architecture.
- Borealis is intentionally being sized today so it can soar for homelab users and smaller businesses, remain strong for typical MSP-style environments, and still operate reasonably well at higher endpoint counts.
- Horizontal scaling through orchestrated clustering is a future roadmap item, but it is not required for the common `2,000`-endpoint-or-less usage profile that Borealis is primarily targeting today.
- Until clustering exists, the `Enterprise Clustered Not Implemented Yet` row should be read as a forward-looking planning placeholder rather than a currently supported deployment topology.

---

# Core Architecture

## Secure Connectivity (WireGuard)
Borealis uses **WireGuard-based tunnels** as its primary transport layer between the Engine (*Server*) and Agents (*Clients*), and serves as the foundation for all remote operations.

- Outbound-only agent connections (no inbound exposure)
- Persistent, low-overhead tunnels with keepalive
- Shared tunnel sessions per agent
- Strict isolation (`/32` addressing, no lateral movement)
- Port-level allowlists enforced by the Engine
- Ed25519-signed, short-lived tunnel tokens
- Pinned TLS for orchestration channel security

---

## Data Layer (PostgreSQL)
Borealis now runs on **PostgreSQL**, replacing SQLite that was used in older versions of Borealis:

- Improved scalability and concurrency
- Stronger data integrity guarantees
- Foundation for larger environments and higher workloads

---

## Assembly & Automation Model
- Assemblies are stored directly in the database
- Jobs resolve assemblies by GUID
- Supports:
  - Quick Jobs
  - Scheduled Jobs
  - Workflow-based Automation (Experimental)

---

## Aurora Repository Integration
Borealis integrates with the **[Aurora Repository](https://github.com/bunny-lab-io/Aurora)**:

- Aurora serves as the external source of truth for official assemblies, scripts, and playbooks
- Decouples automation content from engine releases
- Supports update ingestion into PostgreSQL
- User-created assemblies remain local to the Engine

---

# Features

## Device Management
- Device inventory (OS, hardware, status)
- Site-based organization and filtering
- Approval workflows
- Global hostname search (RBAC-aware)

## Remote Execution
- PowerShell (Windows)
- Batch (Windows)
- Bash (Linux)
- SYSTEM-level execution support
- CURRENTUSER-level execution support

## Remote Access
- WireGuard-backed secure connectivity
- Remote Shell (cross-platform)
- Web-based VNC remote desktop (UltraVNC for Windows)
- Future support for WinRM and other protocols

## Automation & Workflows
- Quick Jobs for immediate execution
- Scheduled Jobs with improved reliability and real failure reporting
- Visual node-based workflow editor (Experimental)
- Assembly-driven execution model

## Ansible Integration
- Engine-side Ansible playbook execution
- Supports:
  - `ssh`
  - `winrm`
- Routed over Borealis WireGuard tunnels
- Integrated credential selection
- Recap/output surfaced in job history StdOut / StdErr

## Credential Management (Aegis Cipher)
- Encrypted secret storage (AES-256-GCM)
- Engine-wide unlock mechanism
- Supports:
  - Passwords
  - Private keys
  - Tokens (including GitHub API)
- Features:
  - Setup
  - Rotation
  - Reset (destructive recovery)
- Secrets decrypted only in-memory when needed

## Role-Based Access Control (RBAC)
- Site-scoped access restrictions
- Operators only see assigned devices/sites
- Enforcement across:
  - Filters
  - Jobs
  - Remote access APIs

## Authentication & Security
- MFA required by default
- Persistent Engine session secret
- Strict session validation and invalidation

## Agent Capabilities
- Cross-platform (Windows + Linux)
- Automatic self-updating:
  - Windows Scheduled Task
  - Linux systemd service/timer
- Agent Health telemetry:
  - Roles/services status
  - Recovery state visibility
  - 60-second refresh intervals

---

# Current Status

Borealis is actively evolving into a unified automation and remote management platform.

### Stable / Functional Areas
- WireGuard-based connectivity
- PostgreSQL-backed data model
- Cross-platform script execution
- Ansible Playbook execution (via WireGuard tunnels)
- Credential management with encryption
- RBAC and MFA enforcement
- VNC remote desktop (Windows)

### In Progress / Expanding Areas
- Secure-at-rest credential hardening (continued improvements)
- Additional remote protocols (RDP, SSH, WinRM expansion)
- Aurora repository workflows (export/import tooling)
- UX improvements and reporting enhancements

---

# Device Management

Device List:
![Device List](Docs/images/repo_screenshots/Device_List.png)

Device Details:
![Device Details](Docs/images/repo_screenshots/Device_Details.png)

Agent Health Telemetry & Global Device Search:
![Agent Health](Docs/images/repo_screenshots/Device_Details_Agent_Health.png)

Device Remote Desktop:
![Device List](Docs/images/repo_screenshots/Device_VNC.png)

Device Approval Queue:
![Device Approval Queue](Docs/images/repo_screenshots/Device_Approval_Queue.png)

Device Filters:
![Device Filters](Docs/images/repo_screenshots/Device_Filter_List.png)

Device Filter Editor:
![Device Filter Editor](Docs/images/repo_screenshots/Device_Filter_Editor.png)

Device Remote Shell:
![Device Remote Shell](Docs/images/repo_screenshots/Device_Remote_Shell.png)

Device Service List:
![Device Service List](Docs/images/repo_screenshots/Device_Service_List.png)

---

# Assembly Management

Assembly List:
![Assembly List](Docs/images/repo_screenshots/Assembly_List.png)

Assembly Editor:
![Assembly Editor](Docs/images/repo_screenshots/Assembly_Editor.png)

Workflow Editor:
[![Workflow Editor Demonstration](Docs/images/repo_screenshots/Workflow_Editor.png)](https://www.youtube.com/watch?v=6GLolR70CTo)

---

# Log Management

Log Management:
![Log Management](Docs/images/repo_screenshots/Log_Management.png)

Log Management (Raw):
![Log Management (Raw)](Docs/images/repo_screenshots/Log_Management_Raw.png)

---

# Job Scheduling

Scheduled Job List:
![Scheduled Job List](Docs/images/repo_screenshots/Scheduled_Job_List.png)

Scheduled Job Editor:
![Scheduled Job List](Docs/images/repo_screenshots/Scheduled_Job_Editor.png)

Scheduled Job History:
![Scheduled Job History](Docs/images/repo_screenshots/Scheduled_Job_History.png)

Ansible Playbook Recap:
![Ansible Playbook Recap](Docs/images/repo_screenshots/Ansible_Playbook_Recap.png)

---

# Misc Management

Site List:
![Site List](Docs/images/repo_screenshots/Site_List.png)

Credential Management List:
![Credential Management](Docs/images/repo_screenshots/Credential_Management.png)

Credential Management Editor:
![Credential Management Editor](Docs/images/repo_screenshots/Credential_Management_Editor.png)

---

# Getting Started

## Engine Installation

```sh
# Production
curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/bootstrap.sh | sudo bash -s -- --engine-production

# Development (Vite Dev File Hot-loading)
curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/bootstrap.sh | sudo bash -s -- --engine-dev
````

## Agent Installation

### Windows

```powershell
$env:BOREALIS_SERVER_URL="https://borealis.bunny-lab.io"; $env:BOREALIS_ENROLLMENT_CODE="044C-30BA-A742-8D8E-20FB-771A-A94F-E6E4"; irm https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/bootstrap.ps1 | iex
```

### Linux

```sh
curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/bootstrap.sh | sudo bash -s -- --agent --serverurl "https://borealis.bunny-lab.io" --enrollmentcode "044C-30BA-A742-8D8E-20FB-771A-A94F-E6E4"
```
