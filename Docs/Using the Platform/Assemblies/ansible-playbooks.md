# Ansible Playbooks

Ansible playbook assemblies run from the Linux Engine against remote devices over Borealis-managed WireGuard paths. Use them for infrastructure-style automation when Ansible modules fit better than endpoint scripts.

<figure class="bo-screenshot">
  <img src="../../Reference/images/repo_screenshots/Ansible_Playbook_Recap.png" alt="Borealis Ansible Playbook Recap" loading="lazy">
  <figcaption>Ansible recaps expose Engine-side playbook execution output and per-target status.</figcaption>
</figure>

## Create Playbook Assembly

1. Open `Automation > Assemblies`.
2. Select `New Ansible Playbook`.
3. Add playbook content and metadata.
4. Save.

## Schedule Playbook

1. Create Scheduled Job.
2. Add Ansible playbook assembly.
3. Choose device or filter targets.
4. Choose execution context:
   - `ssh_individual`
   - `ssh`
   - `winrm_individual`
   - `winrm`
5. Select credential or service account path where applicable.
6. Save.

New Ansible jobs default to individual execution so each target gets separate status, output, and timeout handling.

## Read Recap

Run history shows target status and StdOut/StdErr. Playbook recap data captures Ansible results per host or run component.

## Credential Notes

SSH credentials may include password, private key, become method, and become password. Borealis chooses final SSH auth mode per target instead of blindly passing key and password together.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - Playbook execution is scheduled through [Scheduled Jobs](../scheduled-jobs.md).
    - Assembly CRUD endpoints are listed in [Assemblies](assemblies.md).
    - `GET /api/server/ansible-runner-settings` - read scheduled-Ansible concurrency limits.
    - `PUT /api/server/ansible-runner-settings` - update scheduled-Ansible concurrency limits.

    ### Related documentation

    - [Scheduled Jobs](../scheduled-jobs.md)
    - [Credential Management](../credential-management.md)
    - [SSH Connection Logic](../../Reference/ssh-connection-logic.md)
    - [Engine Runtime](../../Reference/Core%20Runtimes/engine-runtime.md)
    - [API Reference](../../Reference/Data%20and%20Schema/api-reference.md)

    ### Source map

    - Ansible runner: `Data/Engine/Containers/api-backend/data/services/ansible/runner.py`
    - SSH credential rendering: `Data/Engine/Containers/api-backend/data/services/ansible/ssh_auth.py`
    - Scheduler dispatch: `Data/Engine/Containers/api-backend/data/services/API/scheduled_jobs/job_scheduler.py`
    - Scheduled job UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Scheduling/Create_Job.jsx`

    ### Runtime behavior

    - Engine stages required Ansible collections into `Engine/Services/api-backend/cache/Ansible/collections`.
    - Shared contexts run one inventory per playbook component.
    - Individual contexts create one-host inventories and one run row per target.
    - SSH/WinRM target admission depends on WireGuard readiness and credential/service-account resolution.
    - Individual runner fan-out is bounded by persisted per-job and global limits exposed in Server Info.
