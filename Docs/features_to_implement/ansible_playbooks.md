[Back to Docs Index](../index.md) | [Index (HTML)](../index.html)

# Ansible Playbooks

## Summary
This document defines how Borealis should incorporate Ansible playbooks into the Linux Engine and the assembly architecture.

The goal is to treat playbooks as first-class assemblies stored in the same PostgreSQL assembly tables as scripts and workflows, while the Linux Engine acts as the first Ansible control node.

This plan is intentionally decision-complete for the current phase:
- package Ansible inside the Engine virtual environment
- install the baseline Galaxy collections Borealis needs
- support localhost testing through the scheduler first
- make WireGuard the remote transport model for Engine-targeted playbooks
- keep scheduler targeting operator-friendly by synthesizing ephemeral inventories from device/filter state instead of relying on manual inventory files

## Implementation Status
The following pieces are implemented in this pass:
- `Borealis.sh` now installs the Engine Ansible control-node runtime into the Engine virtual environment and installs Borealis-managed Galaxy collections into `Engine/Ansible/collections`.
- The old agent-side Ansible execution scaffolding has been removed so Borealis has one clear Ansible architecture.
- The Engine Python dependency manifest now includes:
  - `ansible-core`
  - `ansible-runner`
  - `jmespath`
  - `pywinrm[credssp]>=0.4.0`
  - `pypsrp[credssp]>=0.4.0,<1.0.0`
- Borealis-managed collections now include:
  - `ansible.windows`
  - `ansible.posix`
  - `community.general`
- Scheduled jobs now support an Engine-local `execution_context` value of `local` for localhost testing.
- The Engine now has a server-side Ansible runner for shared scheduled Ansible execution with per-run ephemeral inventories.
- Ansible play recap rows are now written to `ansible_play_recaps` for Engine-local scheduled runs.
- Scheduled Ansible jobs now synthesize site-qualified inventory aliases such as `bunny_lab__host01`.
- Scheduled Ansible jobs now resolve direct targets plus device filters into one shared target set and map remote devices to active WireGuard peer IPs at run time.
- The Engine now exposes `/api/credentials` so stored SSH and WinRM credentials can be selected from the scheduler UI and Access Management UI.
- Generated runtime artifacts stay centralized under `Engine/Ansible`, including per-run workspaces and playbook staging.

The following pieces are still intentionally deferred:
- `/api/ansible/quick_run`
- playbook cancel and live streamed output
- dedicated play recap API/UI
- PSRP support and deeper credential-management ergonomics

## Direct Answer
Yes, Borealis should store playbooks in the same database domain as assemblies.

For the first usable slice, the Linux Engine should behave as an Ansible control node and support Engine-local localhost execution before Borealis attempts general remote inventory management.

That gives us a safe path:
1. store playbooks as assemblies now
2. package the controller runtime now
3. validate playbook execution safely against `borealis-engine-01` / `127.0.0.1`
4. add remote inventory and credential-backed targeting later

## Goals
- Keep Ansible playbooks in the same assembly architecture and PostgreSQL storage model as scripts and workflows.
- Install all Engine-side Ansible dependencies inside the Engine Python virtual environment.
- Install Borealis-managed Galaxy collections as part of Engine deployment.
- Support localhost scheduled testing without waiting for the remote inventory design.
- Use Engine-managed WireGuard connectivity as the remote transport path for future playbook execution.
- Construct target inventory on the fly from Borealis device records, filters, credentials, and WireGuard reachability.
- Keep the future remote model compatible with SSH for Linux targets and WinRM/PSRP for Windows targets.
- Preserve Borealis activity history and scheduled-job history for playbook runs.

## Non-Goals
- Do not introduce standalone Ansible inventory files as a user-authored artifact yet.
- Do not redesign the credential system in this phase.
- Do not add AWX / Automation Controller as a required dependency.
- Do not route playbook execution through the unfinished agent Ansible path for Linux Engine testing.
- Do not implement full playbook cancellation or live line-by-line output streaming in this phase.

## Locked Decisions

### Storage
- Playbooks stay in the assembly tables.
- The payload stays inside `payload_json`.
- The assembly type remains `ansible`.
- The assembly subtype remains `ansible`.

### Initial execution mode
- The first supported Engine execution mode is `execution_context = local`.
- `local` means:
  - the Linux Engine runs `ansible-playbook`
  - Borealis synthesizes an in-memory/on-disk localhost inventory for the run
  - the scheduler target must be the Engine host alias for local testing

### Remote transport model
- Remote Ansible runs originate from the Engine, not from the agent.
- The Engine should reach devices over the Borealis-managed WireGuard tunnel network.
- Device filters and scheduled-job target resolution are responsible for building the point-in-time target set.
- Borealis should synthesize inventory records from:
  - the selected target hostnames produced by filters or direct targeting
  - device metadata persisted in the Engine
  - the WireGuard-reachable endpoint or hostname for that device
  - the selected credential and connection mode
- Manual inventory-file authoring is out of scope for normal operator workflows.

### Initial inventory behavior
- User-authored inventory files are out of scope for now.
- Borealis synthesizes the localhost target automatically for local testing.
- The initial logical target is:
  - inventory hostname: `borealis-engine-01`
  - network endpoint: `127.0.0.1`
  - connection plugin: `local`

### Windows support packaging
- The Engine control node installs the Python libraries needed for Windows remoting compatibility:
  - `pywinrm[credssp]`
  - `pypsrp[credssp]`
- Borealis-managed collections include `ansible.windows` so Windows task authoring is available even before the remote execution model is complete.

### AWX
- `awx.awx` is not installed by default.
- It is optional and only becomes necessary if Borealis playbooks need to manage an external AWX / Automation Controller deployment.

## Runtime Packaging Design

### Engine Python environment
Install Ansible inside the Engine venv:
- venv root: `Engine/`
- Python deps manifest: `Data/Engine/engine-requirements.txt`

### Galaxy collections
Install Borealis-managed collections from:
- source manifest: `Data/Engine/Ansible/collections.yml`
- staged runtime manifest: `Engine/Ansible/collections.yml`

Install destination:
- `Engine/Ansible/collections`

Generated execution workspaces live under:
- `Engine/Ansible/Generated/Runtime`

The Engine service environment exports:
- `ANSIBLE_COLLECTIONS_PATH`
- `ANSIBLE_COLLECTIONS_PATHS`

This keeps Engine-side Ansible artifacts centralized under `Engine/Ansible` even when Borealis stages runtime code into `Engine/Data/Engine`.

## Assembly Authoring Contract
For playbooks, Borealis should continue using the same clean authoring document shape used by other assemblies.

Recommended minimum fields:
- `assembly_guid`
- `name`
- `description`
- `type`
- `script`
- optional: `variables`
- optional: `files`
- optional: `timeout_seconds`

Rules:
- `type` should be `ansible`
- `script` should contain the YAML playbook text
- attached files should live in the assembly `files` array, base64 encoded like other assembly attachments

## Localhost Execution Model
For `execution_context = local`, Borealis currently performs these steps:
1. the scheduler resolves the Ansible assembly from the runtime cache
2. Borealis records an `activity_history` row
3. Borealis stages the playbook into `Engine/Ansible/Generated/Runtime/<run_id>/project/`
4. Borealis stages any attached assembly files alongside the playbook
5. Borealis writes a generated inventory with `ansible_connection=local`
6. the Engine runs `ansible-playbook` directly from the Engine venv
7. Borealis writes output back to `activity_history`, `scheduled_job_runs`, and `ansible_play_recaps`

## Remote Inventory Model Over WireGuard
For non-local execution, the Borealis model is:
1. scheduled jobs resolve a point-in-time target set from hostnames and device filters
2. the Engine looks up each target device's current WireGuard-reachable identity
3. Borealis synthesizes inventory entries for the run instead of requiring hand-authored inventory files
4. the Engine selects the Ansible connection plugin based on job context:
   - `ssh` for Linux and other SSH-managed devices
   - `winrm` for Windows devices
5. the Engine executes the playbook across those WireGuard-reachable targets
6. the generated inventory aliases are site-qualified for remote runs so duplicate hostnames remain human-readable

This keeps simple playbooks and workflows operator-friendly because the inventory is derived from Borealis state rather than manually maintained.

Generated localhost inventory shape:

```ini
[borealis_local]
borealis-engine-01 ansible_host=127.0.0.1 ansible_connection=local ansible_python_interpreter=/opt/Borealis/Engine/bin/python
```

## Basic Assembly Example
This is the simplest assembly document shape to import or author for localhost testing.

```json
{
  "assembly_guid": "11111111-2222-3333-4444-555555555555",
  "name": "Borealis Localhost Smoke Test",
  "description": "Verifies that the Linux Engine can execute an Ansible assembly against itself.",
  "type": "ansible",
  "timeout_seconds": 600,
  "variables": [
    {
      "name": "marker_path",
      "type": "string",
      "default": "/tmp/borealis-ansible-smoke.txt",
      "description": "File written by the playbook during localhost testing."
    }
  ],
  "script": "---\n- name: Borealis localhost smoke test\n  hosts: all\n  gather_facts: true\n  tasks:\n    - name: Show the current target\n      ansible.builtin.debug:\n        msg: \"Running on {{ inventory_hostname }} via {{ ansible_connection }}\"\n\n    - name: Write a localhost marker file\n      ansible.builtin.copy:\n        dest: \"{{ marker_path }}\"\n        mode: \"0644\"\n        content: |\n          Borealis Ansible smoke test succeeded.\n          Host: {{ inventory_hostname }}\n          Time: {{ ansible_facts['date_time']['iso8601'] }}\n\n    - name: Confirm the file exists\n      ansible.builtin.stat:\n        path: \"{{ marker_path }}\"\n      register: marker_file\n\n    - name: Assert that the file was created\n      ansible.builtin.assert:\n        that:\n          - marker_file.stat.exists\n"
}
```

## Readable YAML Version
If you want the playbook body by itself, this is the YAML the assembly should carry in `script`.

```yaml
---
- name: Borealis localhost smoke test
  hosts: all
  gather_facts: true
  tasks:
    - name: Show the current target
      ansible.builtin.debug:
        msg: "Running on {{ inventory_hostname }} via {{ ansible_connection }}"

    - name: Write a localhost marker file
      ansible.builtin.copy:
        dest: "{{ marker_path }}"
        mode: "0644"
        content: |
          Borealis Ansible smoke test succeeded.
          Host: {{ inventory_hostname }}
          Time: {{ ansible_facts['date_time']['iso8601'] }}

    - name: Confirm the file exists
      ansible.builtin.stat:
        path: "{{ marker_path }}"
      register: marker_file

    - name: Assert that the file was created
      ansible.builtin.assert:
        that:
          - marker_file.stat.exists
```

## How To Schedule The First Test
Use these job values for the first proof-of-life run:
- component type: `ansible`
- component assembly: the localhost smoke-test assembly
- target hostname: `borealis-engine-01`
- execution context: `local`
- credentials: none

Expected result:
- the Engine runs the playbook locally
- the target row in scheduled-job history transitions to `Success`
- `activity_history` captures stdout/stderr
- `ansible_play_recaps` receives a recap row
- `/tmp/borealis-ansible-smoke.txt` is created on the Engine host

## Roadmap

### Phase 1: Engine control-node packaging
Status: implemented

Deliverables:
- Engine venv contains Ansible runtime libraries
- Borealis-managed collections are installed during Engine deployment
- Engine service exports collection paths

### Phase 2: Localhost scheduled execution
Status: implemented

Deliverables:
- new scheduled-job execution context: `local`
- Engine-local runner for Ansible assemblies
- localhost inventory synthesis
- activity history and recap persistence

### Phase 3: Quick run for playbooks
Status: pending

Deliverables:
- implement `POST /api/ansible/quick_run`
- allow operator-triggered playbook execution without creating a scheduled job
- reuse the same Engine-local runner for localhost tests

### Phase 4: Remote Linux targeting
Status: pending

Deliverables:
- SSH inventory synthesis from Borealis job targets and WireGuard-reachable device records
- credential lookup/decryption for SSH username/password and key-based auth
- optional `sshpass` or key-agent strategy as a product decision
- host-key handling policy
- route remote execution over the Engine-managed WireGuard network rather than a separate standalone targeting model

### Phase 5: Remote Windows targeting
Status: pending

Deliverables:
- WinRM / PSRP inventory synthesis from Borealis job targets and WireGuard-reachable device records
- map Borealis credential records into Ansible connection variables
- connection choice policy:
  - `winrm`
  - `psrp`
  - transport selection such as `ntlm`, `credssp`, or `kerberos`
- clearer Windows preflight and connectivity diagnostics
- route Windows remoting over the Engine-managed WireGuard network

### Phase 6: Operator experience
Status: pending

Deliverables:
- recap API endpoints and UI panels
- live output streaming
- cancel support
- explicit per-run Ansible logs surfaced in the UI
- clearer validation when a non-local hostname is used with `execution_context = local`

## Open Questions To Confirm
- Should the Engine-local scheduler mode stay named `local`, or do you want a more explicit label such as `engine_local` before we extend the API further?
- When we add remote Linux execution, do you want Borealis to support password-based SSH, key-based SSH, or both in the first release?
- When we add remote Windows execution, should Borealis prefer `winrm` first, `psrp` first, or let the credential determine that automatically?

## Related Documentation
- [Assemblies and Quick Jobs](../assemblies.md)
- [Scheduled Jobs](../scheduled-jobs.md)
- [Engine Runtime](../engine-runtime.md)
- [Database Reference](../db-reference.md)
