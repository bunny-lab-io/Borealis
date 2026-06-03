# Server Info

Server Info is the admin dashboard for Engine runtime health. Use it to inspect service state, resources, public edge certificates, live operators, WireGuard, Aegis, release channels, worker state, timezone, and site-worker scheduled task slots.

<figure class="bo-screenshot">
  <img src="../Reference/images/repo_screenshots/Server_Overview.png" alt="Borealis Server Overview" loading="lazy">
  <figcaption>Server Overview summarizes Engine runtime state, services, access, resources, WireGuard, and Aegis status.</figcaption>
</figure>

## Read Overview

1. Open `Admin Settings > Server Info`.
2. Start with summary cards.
3. Review runtime, service, resource, access, and security rows.
4. Check public certificate health before blaming browser or agent trust.
5. Check WireGuard state before troubleshooting shell, desktop, SSH, or WinRM.

## Run Service Actions

Server Info can queue supported Engine service actions through the job scheduler. These are the same targeted operations as `Engine.sh --service ...`.

Use service actions for focused restart, rebuild, reload, or WireGuard reconcile work. Use full Engine deploy when more than one component changed.

## Tune Runtime Settings

Admins can adjust:

- Agent release channel targets.

Server Info also shows Site Worker Scheduled Tasks as read-only profile-managed data. `Engine.sh deploy` tunes that value from the detected Engine deployment profile, and redeploys overwrite stale manual values.

This value is active scheduled-lane work-item capacity per site worker, not raw device concurrency. Shared Ansible batches, scheduled workflows, scheduled jobs, and agent-maintenance items each consume scheduled slots while active according to their work-item shape.

!!! warning

    Service actions affect live Engine components. Prefer targeted actions only after identifying affected service.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - `GET /api/server/overview` - dashboard snapshot.
    - `GET /api/server/time` - server clock.
    - `GET /api/server/workers` - active/recent worker state.
    - `POST /api/server/workers/<worker_guid>/recreate` - queue a site-worker container re-create.
    - `GET /api/server/site-worker-settings` - read profile-managed site-worker scheduled-lane work-item capacity.
    - `GET /api/server/agent-release-channels` - read Agent update channel targets.
    - `PUT /api/server/agent-release-channels` - update default channel or repo and refresh cached artifacts.
    - `POST /api/server/agent-release-channels/refresh` - refresh cached Agent update artifacts.
    - `POST /api/server/services/<service_key>/action` - queue container service action.
    - `POST /api/server/services/<service_key>/restart` - queue systemd restart path.
    - `POST /api/server/wireguard/recover` - queue WireGuard tunnel reconcile.

    ### Related documentation

    - [Engine Status](engine-status.md)
    - [Engine Log Management](engine-log-management.md)
    - [Engine Runtime](../Reference/Core%20Runtimes/engine-runtime.md)
    - [Docker Stack Breakdown](../Reference/Core%20Runtimes/Stack_Breakdown.md)

    ### Source map

    - Server API: `Data/Engine/Containers/api-backend/cmd/api-backend/server_overview.go`
    - Server actions: `Data/Engine/Containers/api-backend/cmd/api-backend/server_actions.go`
    - Agent release channels: `Data/Engine/Containers/api-backend/cmd/api-backend/server_agent_release_channels.go`
    - WireGuard recovery: `Data/Engine/Containers/api-backend/cmd/api-backend/server_wireguard.go`
    - Log API: `Data/Engine/Containers/api-backend/cmd/api-backend/server_logs.go`
    - Server Info UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Admin/Server_Info.jsx`
    - Service actions: `Data/Engine/Containers/api-backend/data/services/job_scheduler/`

    ### Runtime behavior

    - Container mode reads Docker state through `docker-proxy` and job-scheduler snapshots.
    - Service actions queue work items so API request can return before service changes interrupt runtime.
    - The Site Worker Scheduled Tasks value controls active scheduled-lane work items for scheduled jobs, scheduled workflows, scheduled Ansible work, and agent-maintenance work. Onboarding keeps its separate lane behavior.
    - Shared Ansible batches consume one scheduled slot for a site batch even when the batch targets several devices. Individual Ansible runs consume one scheduled slot per one-target run while active.
    - Server Info is informational first; raw log inspection belongs in Engine Log Management.
