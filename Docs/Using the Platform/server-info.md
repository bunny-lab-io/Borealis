# Server Info

Server Info is the admin dashboard for Engine runtime health. Use it to inspect service state, resources, public edge certificates, live operators, WireGuard, Aegis, release channels, worker state, timezone, and site-worker scheduled task capacity.

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

- Engine timezone.
- Agent release channel targets.

Server Info also shows Site Worker Scheduled Task Concurrency as read-only profile-managed data. `Engine.sh deploy` tunes that value from the detected Engine deployment profile, and redeploys overwrite stale manual values.

!!! warning

    Service actions affect live Engine components. Prefer targeted actions only after identifying affected service.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - `GET /api/server/overview` - dashboard snapshot.
    - `GET /api/server/time` - server clock.
    - `GET /api/server/timezones` - timezone options.
    - `POST /api/server/timezone` - change host timezone.
    - `GET /api/server/workers` - active/recent worker state.
    - `GET /api/server/site-worker-settings` - read profile-managed site-worker scheduled-lane capacity.
    - `POST /api/server/services/<service_key>/action` - queue container service action.
    - `POST /api/server/services/<service_key>/restart` - queue systemd restart path.
    - `POST /api/server/wireguard/recover` - recover WireGuard listener.

    ### Related documentation

    - [Engine Status](engine-status.md)
    - [Engine Log Management](engine-log-management.md)
    - [Engine Runtime](../Reference/Core%20Runtimes/engine-runtime.md)
    - [Docker Stack Breakdown](../Reference/Core%20Runtimes/Stack_Breakdown.md)

    ### Source map

    - Server API: `Data/Engine/Containers/api-backend/data/services/API/server/info.py`
    - Log API: `Data/Engine/Containers/api-backend/data/services/API/server/log_management.py`
    - Server Info UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Admin/Server_Info.jsx`
    - Service actions: `Data/Engine/Containers/api-backend/data/services/job_scheduler/`

    ### Runtime behavior

    - Container mode reads Docker state through `docker-proxy` and job-scheduler snapshots.
    - Service actions queue work items so API request can return before service changes interrupt runtime.
    - Site Worker Scheduled Task Concurrency controls scheduled jobs, scheduled workflows, scheduled Ansible work, and agent-maintenance work that uses the scheduled lane. Onboarding keeps its separate lane behavior.
    - Server Info is informational first; raw log inspection belongs in Engine Log Management.
