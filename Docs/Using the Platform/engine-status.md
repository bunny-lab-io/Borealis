# Engine Status

Engine Status shows live topology for Engine containers, job-scheduler workers, site workers, and recent task groups. Use it when scheduled work, onboarding, or service actions need visual execution context.

<figure class="bo-screenshot">
  <img src="../Reference/images/repo_screenshots/Server_Overview.png" alt="Borealis Server Overview runtime summary" loading="lazy">
  <figcaption>Server Overview and Engine Status share the same runtime health source data.</figcaption>
</figure>

## Read The Canvas

Open `Admin Settings > Engine Status`.

The React Flow canvas uses left-to-right topology:

- Engine service nodes show core container health.
- Scheduler node represents long-lived job-scheduler manager.
- Site-worker nodes represent active or recent `site-worker-*` containers.
- Task group nodes represent recent work claimed by workers.
- Edges show ownership or assignment, not network traffic.

## Understand Worker States

- Active workers are currently heartbeating.
- Recent workers may show stopped, lost, completed, or failed state briefly for handoff visibility.
- Task groups show work kind, site, claimed counts, and recent status.
- Running or queued task groups attach only to active site workers or queued site placeholders. If their previous worker stopped or was lost, the task group shows `Reassigning to New Worker` while the scheduler hands it to another same-site worker.

## Use Actions

- `Dismiss Inactive Workers` hides stale terminal rows from the current view.
- Service action controls queue supported Engine service actions.
- Canvas is read-only. Nodes are not draggable or connectable.

!!! tip

    Use Engine Status for "what is running where" and Scheduled Job history for "what happened to this job."

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - `GET /api/server/workers?history_seconds=60` - worker and recent work payload.
    - `GET /api/server/overview` - service state and runtime context.
    - `POST /api/server/services/<service_key>/action` - queue service action from status surface.

    ### Related documentation

    - [Server Info](server-info.md)
    - [Scheduled Jobs](scheduled-jobs.md)
    - [Sites](sites.md)
    - [Docker Stack Breakdown](../Reference/Core%20Runtimes/Stack_Breakdown.md)

    ### Source map

    - Engine Status UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Admin/Engine_Status.jsx`
    - Worker queue: `Data/Engine/Containers/api-backend/data/services/job_scheduler/queue.py`
    - Scheduler manager: `Data/Engine/Containers/api-backend/data/services/job_scheduler/manager.py`
    - Site worker: `Data/Engine/Containers/api-backend/data/services/job_scheduler/worker.py`

    ### Runtime behavior

    - UI builds React Flow nodes and edges from `/api/server/workers` plus `/api/server/overview`.
    - Worker rows live in `job_scheduler_workers`.
    - Work rows live in `job_scheduler_work_items`.
    - Terminal worker records are short-lived lifecycle hints, not durable job history.
