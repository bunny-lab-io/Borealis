![Borealis Logo](Data/Engine/Containers/webui-frontend/data/web-interface/public/Borealis_Logo_Full.png)

# Borealis

Borealis is a self-hosted remote management, monitoring, and visual automation platform built around a Linux-hosted Engine and a cross-platform Agent runtime.

It combines RMM-style endpoint operations, Ansible/AWX-style automation, scheduled jobs, watchdog remediation, remote desktop, remote shell, file/software/process/service management, and credential-backed infrastructure execution in one operator interface.

**Documentation:** https://bunny-lab-io.github.io/Borealis

**Deploy Borealis:** https://bunny-lab-io.github.io/Borealis/Engine/deploying-the-engine/

## Standout Capabilities

- Linux-hosted Engine with Dockerized API, WebUI, PostgreSQL, Traefik, WireGuard, and Guacamole services.
- Cross-platform Agent runtime with Windows and Linux support for inventory, telemetry, remote shell, file operations, process/service/software management, signed script execution, and watchdog inputs.
- Managed WireGuard tunnel for remote operations without inbound endpoint exposure.
- Browser-based remote desktop for Windows endpoints through Apache Guacamole.
- Scheduled jobs, quick jobs, reusable assemblies, workflow automation, and Engine-side Ansible over SSH or WinRM.
- Aegis Cipher secret protection, MFA by default, WebAuthn passkeys, signed script delivery, RBAC, and site scoping.
- Operator documentation for deployment, runtime architecture, API behavior, testing, troubleshooting, and platform workflows.

## Feature Snapshot

Status reflects productized support in current Borealis code and docs. See the [documentation feature matrix](https://bunny-lab-io.github.io/Borealis/) for detailed breakdowns and caveats.

### Agent Runtime

| Capability | Windows | Linux | macOS |
| --- | --- | --- | --- |
| Agent runtime, enrollment, telemetry, and role loading | Full | Full | - |
| Hardware, OS, software, service, session, status, and health inventory | Full | Full | - |
| Managed WireGuard tunnel | Full | Full | - |
| Remote shell | Full | Full | - |
| Remote desktop | Full | - | - |
| File, process, service, and software operations | Full | Full | - |
| Signed script execution | Full | Full | - |
| Watchdog inputs and remediation execution | Full | Full | - |
| Device identity and tunnel trust | Full | Full | - |

### Engine Platform

| Area | Support |
| --- | --- |
| Device inventory, status, health, software, services, sessions, and activity history | Full |
| Sites, agent approvals, and RBAC scoping | Full |
| Remote operations API and UI for shell, desktop, files, processes, services, and software | Full |
| Scheduled jobs, quick jobs, workflows, and target history | Full |
| Watchdogs, incident tracking, suppression, and auto-remediation dispatch | Full |
| Aurora content repository for official assemblies, scripts, and playbooks | Full |
| Engine-side Ansible over SSH or WinRM through managed WireGuard reachability | Full |
| Aegis Cipher, MFA, passkeys, sessions, code signing, and REST/API surface | Full |
| Reporting for activity history, job history, alerts, and Ansible recap data | Full |

## Architecture At A Glance

- **Engine:** Linux-hosted control plane for APIs, WebUI, scheduling, automation, remote access, credentials, security controls, and PostgreSQL-backed state.
- **Agent:** Go endpoint runtime that enrolls, reports inventory and health, maintains remote-operation roles, and executes approved work.
- **Transport:** Agents connect outbound to the Engine. Remote operations use Borealis-managed WireGuard sessions with Engine-controlled access.
- **Automation:** Assemblies run through quick jobs, scheduled jobs, workflows, watchdog remediation, and Engine-side Ansible.

Full deployment, operation, automation, architecture, API, testing, and contributor documentation lives in the [documentation site](https://bunny-lab-io.github.io/Borealis/).
