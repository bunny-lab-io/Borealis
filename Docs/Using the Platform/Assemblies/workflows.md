# Workflows

Workflows are visual automation assemblies built with React Flow. Use them when automation needs routing, reusable graph logic, typed connections, subworkflows, webhook triggers, or readable operator flow.

<figure class="bo-screenshot">
  <img src="../../Reference/images/repo_screenshots/Workflow_Editor.png" alt="Borealis Workflow Editor" loading="lazy">
  <figcaption>Workflow Editor composes automation through visual nodes, typed ports, and routed connections.</figcaption>
</figure>

## Create Workflow

1. Open `Automation > Assemblies`.
2. Select `New Workflow`.
3. Drag nodes from sidebar onto canvas.
4. Configure selected node in the side panel.
5. Connect named ports.
6. Save workflow assembly.

## Read Workflow Canvas

- Trigger nodes start flow.
- Target nodes provide device sets.
- Action edges control execution route.
- Data edges pass target or result payloads.
- Debug Info previews expected runtime envelopes while authoring and shows persisted runtime details in run snapshots.

## Run Workflow

Workflows can run manually, from scheduled jobs, from webhooks, or from watchdog remediation. Scheduled workflow jobs use one saved workflow and configure execution inside the workflow itself.

## Repair Legacy Wiring

Older saved workflows may open for repair but be blocked from manual run, webhook run, or Scheduled Job selection until edges reconnect to current named ports.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - `GET /api/assemblies/<assembly_guid>/export` - load saved workflow.
    - `POST /api/assemblies/import` - save workflow assembly.
    - `DELETE /api/assemblies/<assembly_guid>` - delete user-domain workflow.
    - `GET /api/workflows/<workflow_guid>/editor-access` - verify editor access.
    - `POST /api/workflows/run` - start workflow run.
    - `GET /api/workflows/runs/<run_id>` - load run snapshot.
    - `GET /api/workflows/runs/<run_id>/nodes/<node_id>` - node run detail.
    - `GET /api/workflows/<workflow_guid>/webhooks` - list webhooks.
    - `POST /api/workflows/<workflow_guid>/webhooks` - create webhook.
    - `DELETE /api/workflows/<workflow_guid>/webhooks/<webhook_id>` - delete webhook.

    ### Related documentation

    - [Assemblies](assemblies.md)
    - [Scripts](scripts.md)
    - [Scheduled Jobs](../scheduled-jobs.md)
    - [Watchdogs](../watchdogs.md)
    - [UI and Notifications](../../Reference/ui-and-notifications.md)

    ### Source map

    - Workflow editor controller: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Flow_Editor/Flow_Editor.jsx`
    - Canvas: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Flow_Editor/Flow_Editor_Canvas.jsx`
    - Sidebar: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Flow_Editor/Flow_Editor_Sidebar.jsx`
    - Node registry: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Flow_Editor/nodeRegistry.js`
    - Node source: `Data/Engine/Containers/webui-frontend/data/web-interface/src/nodes/`
    - Workflow API and runtime: `Data/Engine/Containers/api-backend/cmd/api-backend/workflows.go` and `workflows_runtime.go`

    ### Runtime behavior

    - Node modules are auto-loaded with `import.meta.glob('../nodes/**/*.jsx', { eager: true })`.
    - Workflow documents store `nodes` and `edges` inside encoded workflow payloads.
    - Runtime v1 uses named ports and route-aware Action edges.
    - Workflow run snapshots are immutable inspection records.
