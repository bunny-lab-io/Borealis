![Borealis Logo](Data/Engine/web-interface/public/Borealis_Logo_Full.png)

Borealis is a remote management platform with a simple, visual automation layer, enabling you to leverage scripts and advanced nodegraph-based automation workflows. I originally created Borealis to work towards consolidating the core functionality of several standalone automation platforms in my homelab, such as TacticalRMM, Ansible AWX, SemaphoreUI, and a few others. 

### A Note on Development Pace
I'm the sole maintainer and still learning as I go, while working a full-time IT job. Progress is sporadic, and parts of the codebase get rebuilt when I discover better or more optimized approaches. Thank you for your patience with the slower cadence.  Ko-Fi donations are always welcome and help keep me motivated to actively continue development of Borealis.

## Documentation
- Human-friendly docs live in `Docs/` with a top-level index at `Docs/index.md`.
- The same files also contain **Codex Agent** sections with deep, agent-focused implementation details.
- Start with `Docs/getting-started.md` and `Docs/architecture-overview.md`, then jump to the domain pages.

## Features
- **Device Inventory**: OS, hardware, and status posted on connect and periodically.
- **Remote Script Execution**: Run PowerShell in `CURRENT USER` context or as `NT AUTHORITY\SYSTEM`.
- **Jobs and Scheduling**: Launch "*Quick Jobs*" instantly or create more advanced schedules.
- **Visual Workflows**: Drag-and-drop node canvas for combining steps, analysis, and logic.
- **Ansible Playbooks**: Ansible playbook support is unfinished/broken in both the Engine and agent runtimes. The goal is to ship server-driven Ansible (SSH/WinRM) alongside agent-driven playbooks.
- **Linux Engine + Cross-Platform Agents**: Engine deployment is Linux-only (`Borealis.sh`), and agents run on both Windows (`Borealis.ps1`/`bootstrap.ps1` and Linux (`Borealis.sh`/`bootstrap.sh`).

## Current Status & Limitations
- Ansible is disabled/unstable: Engine quick-run returns not implemented, scheduled-job and agent paths are incomplete, and server-side SSH/WinRM playbook dispatch is still on the roadmap. Expect failures until the Ansible pipeline is rebuilt.
- Linux agents are functional, including enrollment and remote shell workflows.
- Remote bash script execution is not fully implemented or validated yet; treat Linux remote script execution as in-progress.

## Device Management
Device List:
![Device List](Docs/images/repo_screenshots/Device_List.png)

Device Details:
![Device Details](Docs/images/repo_screenshots/Device_Details.png)

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

## Assembly Management
Assembly List:
![Assembly List](Docs/images/repo_screenshots/Assembly_List.png)

Assembly Editor:
![Assembly Editor](Docs/images/repo_screenshots/Assembly_Editor.png)

Workflow Editor:
[![Workflow Editor Demonstration](Docs/images/repo_screenshots/Workflow_Editor.png)](https://www.youtube.com/watch?v=6GLolR70CTo)

## Log Management
Log Management:
![Log Management](Docs/images/repo_screenshots/Log_Management.png)

Log Management (Raw):
![Log Management (Raw)](Docs/images/repo_screenshots/Log_Management_Raw.png)

## Job Scheduling
Scheduled Job List:
![Scheduled Job List](Docs/images/repo_screenshots/Scheduled_Job_List.png)

Scheduled Job Editor:
![Scheduled Job List](Docs/images/repo_screenshots/Scheduled_Job_Editor.png)

Scheduled Job History:
![Scheduled Job History](Docs/images/repo_screenshots/Scheduled_Job_History.png)

Ansible Playbook Recap:
![Ansible Playbook Recap](Docs/images/repo_screenshots/Ansible_Playbook_Recap.png)

## Misc Management

Site List:
![Site List](Docs/images/repo_screenshots/Site_List.png)

Credential Management List:
![Credential Management](Docs/images/repo_screenshots/Credential_Management.png)

Credential Management Editor:
![Credential Management Editor](Docs/images/repo_screenshots/Credential_Management_Editor.png)

## Getting Started
### Engine Installation:
```sh
# Production
curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/bootstrap.sh | sudo bash -s -- --engine-production

# Development (Vite Dev File Hot-loading)
curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/bootstrap.sh | sudo bash -s -- --engine-dev
```
### Agent Installation:
#### Windows
```powershell
$env:BOREALIS_SERVER_URL="https://192.168.3.252:5000"; $env:BOREALIS_ENROLLMENT_CODE="044C-30BA-A742-8D8E-20FB-771A-A94F-E6E4"; irm https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/bootstrap.ps1 | iex
```
#### Linux
```sh
curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/bootstrap.sh | sudo bash -s -- --agent --serverurl "https://192.168.3.252:5000" --enrollmentcode "044C-30BA-A742-8D8E-20FB-771A-A94F-E6E4"
```

