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
- **Windows-first**. Linux Engine support ships via `Borealis.sh` (Engine is currently the focus); the Linux agent is not yet available; only settings can be staged - and the current Linux agent build would not execute scripts, audits, or likely even enroll reliably.

## Current Status & Limitations
- Ansible is disabled/unstable: Engine quick-run returns not implemented, scheduled-job and agent paths are incomplete, and server-side SSH/WinRM playbook dispatch is still on the roadmap. Expect failures until the Ansible pipeline is rebuilt.
- Linux agent is non-functional: script execution, auditing, and enrollment flows are Windows-only right now. Avoid Linux agent deployments until a proper port is delivered.  The core of Borealis is Python and Java, so it's already inherantly compatible with Linux, and you will find that the Engine runs fine in Linux, but the Agent needs a huge amount of work to account for various Linux distributions.

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

## Misc Management Sections
Scheduled Job List:
![Scheduled Job List](Docs/images/repo_screenshots/Scheduled_Job_List.png)

Scheduled Job Editor:
![Scheduled Job List](Docs/images/repo_screenshots/Scheduled_Job_Editor.png)

Scheduled Job History:
![Scheduled Job History](Docs/images/repo_screenshots/Scheduled_Job_History.png)

Site List:
![Site List](Docs/images/repo_screenshots/Site_List.png)

## Getting Started

### Installation
1) Start the Engine:
   - Windows: `./Borealis.ps1 -EngineProduction` *Production Engine @ https://localhost:5000*
   - Windows: `./Borealis.ps1 -EngineDev` *Dev (Vite + Flask) @ https://localhost:5173*
   - Linux (Engine only): `./Borealis.sh --EngineProduction` *Production Engine @ https://localhost:5000* (use `--EngineDev` for Vite)
      - Default Username: `admin`
      - Default Password: `Password`
2) Install the Agent:
   - Windows: `./Borealis.ps1 -Agent`
   - Linux: `curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/Borealis.sh | sudo bash -s --`
   - Linux (ServerURL & Enrollment Code Included): `curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/Borealis.sh | sudo bash -s -- --agent --serverurl https://10.0.0.54:5000 --enrollmentcode E56F-FD6A-7D68-DEE9-899D-68AC-127D-84FE`

