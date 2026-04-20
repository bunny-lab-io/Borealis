![Borealis Logo](Data/Engine/web-interface/public/Borealis_Logo_Full.png)

Borealis is a remote management and automation platform built around a Linux-hosted Engine (*server*), Windows and Linux agent (*client*) runtimes, and a visual workflow layer. Operators can execute scripts, schedule jobs, orchestrate infrastructure tasks, and manage distributed systems through a unified interface.

The project was originally created to consolidate the functionality of multiple standalone tools used in and outside my homelab and real-world environments (Various RMM platforms, Ansible/AWX, SemaphoreUI, etc.) into a single, cohesive platform.

## A Note on Development Pace
I'm the sole maintainer of this project and still learning as I go while working a full-time IT job. Progress is iterative, and parts of the system are occasionally reworked as better architectural approaches emerge.

## Documentation
- Human-friendly docs live in `Docs/` with a top-level index at `Docs/index.md`
- The same files also include **Codex Agent** sections with deeper implementation details
- Start with:
  - `Docs/index.md`
  - `Docs/getting-started.md`
  - `Docs/architecture-overview.md`

# System Requirements
Borealis currently runs as a single-node Linux Engine deployment that bundles the Python API/runtime, PostgreSQL, Traefik, WebSockets, scheduling, and automation services onto one host. Because of that, the Engine will use the resources you give it in a few predictable ways:

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
During Engine deployment and re-deployment, `Borealis.sh` profiles the host CPU and RAM, prints the detected Engine profile in the CLI, and auto-configures the PostgreSQL and Engine DB tuning for that host. Storage is displayed as guidance only and does not change the selected profile.

## Engine Deployment Profiles

| Profile | Typical Use | Endpoints | Active Operators | vCPU | RAM | NVMe Storage |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Homelab | Personal labs, testing, feature development, and very small sites | Up to 250 | 1-3 | < 8 | < 16 GiB | 80-150 GiB |
| Small Business | Smaller production environments with light-to-moderate operator activity | Up to 1,000 | 2-4 | 8-15 | 16-31 GiB | 150-250 GiB |
| MSP / Production | The main Borealis target for real-world SMB and managed-service usage | Up to 2,000 | 4-8 | 16-23 | 32-63 GiB | 500 GiB |
| Enterprise | Larger single-node environments on the current Borealis architecture | Up to 10,000 | 10-20 | 24+ | 64 GiB+ | 500 GiB-1 TiB |
| Enterprise Clustered (Not Implemented Yet) | Much larger multi-node clustered environments | 10,000+ | 20+ per node | 24+ per node | 64 GiB+ per node | 500 GiB-1 TiB per node |

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
- Until clustering exists, the `Enterprise Clustered (Not Implemented Yet)` row should be read as a forward-looking planning placeholder rather than a currently supported deployment topology.

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
- Public CA + hostname validation on the HTTPS control plane

---

## Data Layer (PostgreSQL)
Borealis now runs on **PostgreSQL**, replacing SQLite that was used in older versions of Borealis:

- Improved scalability and concurrency
- Stronger data integrity guarantees
- Foundation for larger environments and higher workloads

---

## Assembly & Automation Model
- Assemblies are stored in PostgreSQL `assemblies.*` tables
- Jobs resolve assemblies by GUID
- Supports:
  - Quick Jobs
  - Scheduled Jobs
  - Watchdog-triggered remediation
  - Workflow authoring and execution

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
- Device inventory (OS, hardware, software, services, and status)
- Site-based organization, approvals, and RBAC-aware targeting
- Saved filters and device list views
- Global hostname search scoped by operator access
- Device-level watchdog and alerts surfaces

## Remote Execution
- PowerShell (Windows)
- Batch (Windows)
- Bash (when the target agent runtime provides it)
- SYSTEM-level execution support
- CURRENTUSER-level execution support

## Remote Access
- WireGuard-backed secure connectivity
- Remote Shell over WireGuard
- Same-origin VNC remote desktop for Windows (UltraVNC + noVNC)
- Engine-side automation can reach SSH and WinRM targets over managed WireGuard sessions

## Automation & Workflows
- Quick Jobs for immediate agent-side script execution
- Scheduled Jobs for scripts, workflows, and Engine-side Ansible runs
- Visual workflow editor and runtime
- Watchdogs with preview, incident tracking, and assembly-based remediation
- Assembly-driven execution model

## Ansible Integration
- Engine-side Ansible playbook execution on the Linux Engine
- Scheduled-job execution contexts:
  - `ssh`
  - `ssh_individual`
  - `winrm`
  - `winrm_individual`
- Routed over Borealis-managed WireGuard sessions
- Integrated credential selection and runner-budget controls
- Per-run output and recap data are persisted, with richer recap/reporting UX still expanding

## Credential Management (Aegis Cipher)
- Engine-global Aegis bootstrap and unlock gate
- `scrypt` + `AES-256-GCM` protection for reusable credentials, operator password hashes, TOTP secrets, passkey material, and the GitHub API token
- Rotation and destructive force-reset flows
- Secrets decrypted only in memory after Aegis unlock

## Role-Based Access Control (RBAC)
- Site-scoped access restrictions
- Operators only see assigned devices/sites
- Enforcement across:
  - Filters
  - Jobs
  - Remote access APIs

## Authentication & Security
- Aegis bootstrap is required before the normal login UI is available
- MFA required by default
- WebAuthn passkeys for direct browser sign-in
- Persistent Engine session secret
- Strict session validation and invalidation
- Code-signed script delivery to agents

## Agent Capabilities
- Windows and Linux agent support
- Automatic self-updating:
  - Windows Scheduled Task
  - Linux systemd service/timer
- Inventory collection, service telemetry, and health reporting
- WireGuard tunnel management and remote shell access
- Agent Health telemetry:
  - Roles/services status
  - Recovery state visibility
  - 60-second refresh intervals

# Getting Started
## Deploy the Borealis Engine
```sh
curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/bootstrap.sh | sudo bash -s --
```