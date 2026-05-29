# Scheduled Jobs

Scheduled Jobs run saved automation now, once, or on a recurring cadence. Use them for routine script runs, workflow launches, Engine-side Ansible playbooks, agent maintenance, and local-network onboarding.

<figure class="bo-screenshot">
  <img src="../Reference/images/repo_screenshots/Scheduled_Job_List.png" alt="Borealis Scheduled Job List" loading="lazy">
  <figcaption>Scheduled Job List shows recurring automation, onboarding jobs, status, cadence, and recent results.</figcaption>
</figure>

## Create Job

1. Open `Automation > Scheduled Jobs`.
2. Select `New Job`.
3. Name the job.
4. Add one or more compatible assemblies.
5. Choose targets: devices or filters.
6. Choose schedule.
7. Choose execution context when needed.
8. Save.

Script jobs can target SYSTEM or current user. Ansible jobs target SSH or WinRM. Workflow jobs use the workflow's own runtime model and ignore scheduler-level targets/context.

## Choose Schedule

Common schedule types include immediate, once, every 5/10/15/30 minutes, hourly, daily, weekly, monthly, and yearly.

## Read Results

- Current Run shows live or latest run state.
- Historical Runs groups past occurrences.
- Device rows show target status, output, errors, and skipped reasons.
- Ansible playbooks store per-target or shared recap output depending on execution mode.

## Onboarding Jobs

Sites can launch local-network onboarding jobs that appear in Scheduled Jobs. They use discovery scope, exclusions, stored credentials, platform selection, install branch, remote ports, and concurrency limits. Successful install still requires Device Approval.

!!! tip

    Use filters for recurring jobs when membership should change as inventory changes. Use explicit devices when target list must stay obvious and small.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - `GET /api/scheduled_jobs` - list visible jobs.
    - `POST /api/scheduled_jobs` - create job.
    - `GET /api/scheduled_jobs/<job_id>` - get job.
    - `PUT /api/scheduled_jobs/<job_id>` - update job.
    - `POST /api/scheduled_jobs/<job_id>/toggle` - enable or disable.
    - `POST /api/scheduled_jobs/<job_id>/rerun` - queue immediate occurrence from saved config.
    - `DELETE /api/scheduled_jobs/<job_id>` - delete job.
    - `GET /api/scheduled_jobs/<job_id>/runs` - run history.
    - `GET /api/scheduled_jobs/<job_id>/devices` - device results.
    - `DELETE /api/scheduled_jobs/<job_id>/runs` - clear run history.
    - `POST /api/onboarding/jobs/<job_id>/redeploy` - rerun onboarding from saved config.
    - `GET /api/onboarding/jobs/<job_id>/targets` - onboarding target details and approval context.

    ### Related documentation

    - [Scripts](Assemblies/scripts.md)
    - [Workflows](Assemblies/workflows.md)
    - [Ansible Playbooks](Assemblies/ansible-playbooks.md)
    - [Device Filters](device-filters.md)
    - [Sites](sites.md)
    - [SSH Connection Logic](../Reference/ssh-connection-logic.md)

    ### Source map

    - API: `Data/Engine/Containers/api-backend/data/services/API/scheduled_jobs/management.py`
    - Scheduler core: `Data/Engine/Containers/api-backend/data/services/API/scheduled_jobs/job_scheduler.py`
    - Job scheduler manager: `Data/Engine/Containers/api-backend/data/services/job_scheduler/manager.py`
    - Site worker runtime: `Data/Engine/Containers/api-backend/data/services/job_scheduler/worker.py`
    - UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Scheduling/`

    ### Runtime behavior

    - `job-scheduler` owns scheduled ticks, queue leases, service actions, and site-worker lifecycle.
    - Site workers execute site-scoped pressure work outside `api-backend`.
    - Each due occurrence resolves targets once and freezes membership in run target rows.
    - Filter targets preserve allowed site scope from creation/edit time.
    - Remote SSH/WinRM Ansible requires active WireGuard peer IP and selected credential or service account path.
