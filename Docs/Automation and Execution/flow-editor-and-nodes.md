# Flow Editor and Nodes
[Back to Docs Index](../index.md) | [Index (HTML)](../website/index.html)

## Purpose
Document the Borealis visual flow editor (React Flow) and how nodes are defined, grouped, and rendered.

## Core UI Components
- `Data/Engine/Containers/webui-frontend/data/web-interface/src/Flow_Editor/Flow_Editor.jsx` - workflow editor page controller/compositor. Owns route-driven workflow load/save/run behavior, access checks, run snapshot hydration, and the access-warning dialog.
- `Data/Engine/Containers/webui-frontend/data/web-interface/src/Flow_Editor/Flow_Editor_Canvas.jsx` - React Flow canvas, drag/drop, node/edge selection, snap guides, and canvas interaction wiring.
- `Data/Engine/Containers/webui-frontend/data/web-interface/src/Flow_Editor/Flow_Editor_Sidebar.jsx` - workflow actions, node catalog, import/export actions, webhook entry point, and drag sources.
- `Data/Engine/Containers/webui-frontend/data/web-interface/src/Flow_Editor/Node_Configuration_Sidebar.jsx` - per-node configuration UI for the selected workflow node.
- `Data/Engine/Containers/webui-frontend/data/web-interface/src/Flow_Editor/Flow_Editor_Node_Config.jsx` - edge and route configuration sidebar for the selected workflow edge.
- `Data/Engine/Containers/webui-frontend/data/web-interface/src/Flow_Editor/Flow_Editor_Status_Bar.jsx` - footer status bar for authoring mode and immutable run snapshots.
- `Data/Engine/Containers/webui-frontend/data/web-interface/src/Flow_Editor/nodeRegistry.js` - shared node registry that auto-loads workflow node descriptors and exports the stable `workflowNodeTypes` and `workflowCategorizedNodes` objects used by the editor and sidebar.

## Node Registration Pipeline
- Node modules are auto-loaded in `Data/Engine/Containers/webui-frontend/data/web-interface/src/Flow_Editor/nodeRegistry.js` via:
  `import.meta.glob('../nodes/**/*.jsx', { eager: true })`.
- Each module default-exports a descriptor object that includes:
  - `type` (unique node type string)
  - `component` (React component)
  - metadata like `name`, `category`, `description`, `config`, `usage_documentation`
- `nodeRegistry.js` builds:
  - `workflowNodeTypes` (type -> component)
  - `workflowCategorizedNodes` (category -> list of descriptors)
- `Flow_Editor.jsx` and `Flow_Editor_Sidebar.jsx` import the shared registry directly so the Flow Editor domain owns node discovery and the React Flow `nodeTypes` object keeps a stable identity across renders.
- `Data/Engine/Containers/webui-frontend/data/web-interface/src/App.jsx` no longer builds or owns the workflow node registry. It only bootstraps the React Router app layer in `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/`, and `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/route-modules/assemblyRoutes.jsx` routes workflow pages into `Flow_Editor.jsx`.

## Node Categories (Current Folder Layout)
- `Agent`
- `Alerting`
- `Data Analysis & Manipulation`
- `Data Collection`
- `Flow Control`
- `General Purpose`
- `Image Processing`
- `Organization`
- `Reporting`
- `Templates`

## Scheduling Flow Usage
- `Data/Engine/Containers/webui-frontend/data/web-interface/src/Scheduling/Create_Job.jsx` uses React Flow for job status and dependency visualization.

## API Endpoints
- `GET /api/assemblies/<assembly_guid>/export` - load a saved workflow assembly into the editor.
- `POST /api/assemblies/import` - save the current workflow document.
- `DELETE /api/assemblies/<assembly_guid>` - delete a saved User-domain workflow.
- `GET /api/workflows/<workflow_guid>/editor-access` - verify site-scope access before opening a saved workflow.
- `POST /api/workflows/run` - trigger a saved workflow run.
- `GET /api/workflows/runs/<run_id>` - load an immutable workflow run snapshot.
- `GET /api/workflows/runs/<run_id>/nodes/<node_id>` - hydrate selected node run details in run mode.
- `GET /api/workflows/<workflow_guid>/webhooks` - list workflow webhooks.
- `POST /api/workflows/<workflow_guid>/webhooks` - create a workflow webhook.
- `DELETE /api/workflows/<workflow_guid>/webhooks/<webhook_id>` - delete a workflow webhook.
- `GET /api/device_filters/<filter_id>` - enrich Device Filter nodes with matching-device counts when the count is not already embedded in the saved workflow document.

## Related Documentation
- [Assemblies and Quick Jobs](assemblies.md)
- [Scheduled Jobs](scheduled-jobs.md)
- [UI and Notifications](../Start%20Here/ui-and-notifications.md)

## Codex Agent (Detailed)
### How node modules are structured
- A node file exports a descriptor object, for example:
  - `type: "agent"`
  - `component: BorealisAgentNode`
  - `config: [{ key, label, type, defaultValue, optionsKey, ... }]`
- The `component` is the React Flow node UI.
- The descriptor is used by the sidebar for display and configuration forms.

### Adding a new node (step-by-step)
1) Create a new file under `Data/Engine/Containers/webui-frontend/data/web-interface/src/nodes/<Category>/Node_<Name>.jsx`.
2) Export a descriptor object as the default export with `type` and `component` fields.
3) Include `config` entries if you want the configuration sidebar to render fields.
4) Rebuild the WebUI (or run Vite dev mode) so `import.meta.glob` picks it up.
5) Validate drag/drop in the Node Sidebar and ensure the node renders correctly.

### Sidebar behavior
- `Flow_Editor_Sidebar.jsx` renders `workflowCategorizedNodes` and sets `dataTransfer` payloads with `application/reactflow`.
- `Flow_Editor_Canvas.jsx` listens for drop events and creates nodes from the shared descriptor catalog exported by `nodeRegistry.js`.

### Node configuration sidebar
- `Node_Configuration_Sidebar.jsx` uses `useReactFlow().setNodes` to update node data.
- `config` metadata drives form rendering; data is stored in `node.data`.

### Edge configuration sidebar
- `Flow_Editor_Node_Config.jsx` is the renamed edge-configuration sidebar that used to live under `Context_Menu_Sidebar.jsx`.
- It owns edge label, stroke, route selection, copy/paste, and reset behavior for the selected workflow edge.

### Canvas interactions
- Right-click context menus allow node delete, edge unlink, and property edit.
- Snap guides are computed in `Flow_Editor_Canvas.jsx` for alignment.

### Controller boundaries
- `Flow_Editor.jsx` is the workflow editor controller and compositor.
- `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/route-modules/assemblyRoutes.jsx` should only pass route state and navigation into the editor, not workflow save/load/run logic.
- Keep workflow-specific persistence, access warnings, run snapshot hydration, and webhook affordances inside the `Flow_Editor/` folder.

### Job flow editor
- `Scheduling/Create_Job.jsx` uses a custom React Flow setup for status and dependency visualization.
- Keep job flow nodes separate from the general node catalog to avoid accidental crossover.

### Common gotchas
- Folder path casing matters. The source tree is `src/nodes/`, and `Flow_Editor/nodeRegistry.js` must import `../nodes/**/*.jsx` to work on Linux as well as Windows.
- Ensure each node descriptor has a unique `type` or React Flow will mis-render.
- If the sidebar does not show the new node, verify the export default object has `type` and `component`.

### Workflow Runtime Node Port Standard V1
- Applies to Workflow Runtime v1 nodes only. Legacy loop/value-bus nodes may continue using their older visuals until they are migrated.
- Shared shell: workflow-runtime nodes use a compact title bar with icon, a small non-bold title, and a compact status badge. Inline descriptive paragraphs do not belong inside the node body.
- Workflow-runtime node titles use mixed-case IBM Plex Sans styling and should stay visually lighter than the legacy loop/value-bus nodes. The title bar remains the primary drag surface.
- Port rows: visible input/output rows beneath the title bar are the canonical interaction surface. Handles must align to those labeled rows.
- Port handles: workflow-runtime handles should render as compact circular Borealis-blue connection points that sit on top of the node edge, not clipped behind the node border. Do not use mismatched white-outlined dots for these nodes.
- Port contract fields:
  - `id`
  - `label`
  - `direction` (`input` or `output`)
  - `kind` (`action` or `data`)
  - `cardinality` (`single` or `multi`)
  - `required` (`true` or `false`)
- React Flow handles are authoritative for workflow-runtime execution. Use `sourceHandle` and `targetHandle` values that match the declared port ids.
- Control flow:
  - `Action` is the only control-flow port family in V1.
  - `edge.data.route_on` is meaningful on `Action` edges and on `Job Output` data edges.
  - `Action` edges default to `Always` and use Borealis route styling: blue `Always`, green `On Success`, orange `On Warning`, red `On Failed`.
- Data flow:
  - `Targets` and `Job Output` edges are data links.
  - `Job Output` edges from result-producing nodes may use `Always`, `On Success`, `On Warning`, and `On Failed` to pass only the matching per-device records and target list downstream.
  - Other data edges do not expose route controls in the UI.
  - Default workflow-runtime data edges use the Borealis dashed blue style (`#58a6ff`), not solid white lines.
  - `Targets` edges from **List of Devices** and **Device Filter** nodes should auto-label themselves with the current targeted-device count when that count is known.
- Initial standardized workflow-runtime ports:
  - `Trigger - Manual`: `Action` out
  - `Trigger - Scheduled Job`: `Action` out
  - `Trigger - Webhook`: `Action` out
  - `Device Filter`: `Targets` out
  - `List of Devices`: `Targets` out
  - `Execute Assembly`: `Trigger` in, `Targets` in, `Action` out, `Job Output` out
  - `Execute Subworkflow`: `Trigger` in, `Action` out, `Job Output` out
- Trigger fan-in:
  - Runtime nodes may accept multiple incoming `Trigger` action paths when the workflow uses several branches that can converge on the same node.
  - A node should execute after its predecessors are terminal and at least one routed `Trigger` input matched into the node.
- `Job Output` standard:
  - Nodes that expose execution results should emit a per-device payload with device identity plus per-device status at minimum.
  - Routed `Job Output` edges emit only the matching per-device records and the derived `Targets` set for the chosen route.
- Mixed execution results:
  - `Execute Assembly` keeps a single overall workflow status for routing and outline color.
  - When child-device results are mixed, the node badge should summarize the split, for example `7 Success | 1 Failed`, while the node outline and badge tone still follow the overall status.
- Validation and compatibility:
  - Do not auto-migrate older saved workflows.
  - If a workflow uses legacy generic wiring instead of named ports, Borealis should allow it to open for repair but must block manual run, webhook run, and Scheduled Job selection until the operator reconnects the edges to the named ports.
  - The editor should show a prominent warning banner when that older wiring is detected.
- Debug Info tab:
  - Workflow nodes expose the `Debug Info` tab both while authoring and while inspecting immutable run snapshots.
  - In authoring mode, `Debug Info` shows a preview of the selected node's expected runtime-style input and output envelopes based on the current graph wiring and node configuration.
  - In run mode, `Debug Info` shows the actual persisted runtime envelopes, ignored inputs, and linked child summaries for that node run.
