# Flow Editor and Nodes
[Back to Docs Index](index.md) | [Index (HTML)](index.html)

## Purpose
Document the Borealis visual flow editor (React Flow) and how nodes are defined, grouped, and rendered.

## Core UI Components
- `Data/Engine/web-interface/src/Flow_Editor/Flow_Editor.jsx` - canvas, drag/drop, edge editing, context menus.
- `Data/Engine/web-interface/src/Flow_Editor/Node_Sidebar.jsx` - node catalog and drag source.
- `Data/Engine/web-interface/src/Flow_Editor/Node_Configuration_Sidebar.jsx` - per-node configuration UI.
- `Data/Engine/web-interface/src/Flow_Editor/Context_Menu_Sidebar.jsx` - right-click actions.

## Node Registration Pipeline
- Node modules are auto-loaded in `Data/Engine/web-interface/src/App.jsx` via:
  `import.meta.glob('./Nodes/**/*.jsx', { eager: true })`.
- Each module default-exports a descriptor object that includes:
  - `type` (unique node type string)
  - `component` (React component)
  - metadata like `name`, `category`, `description`, `config`, `usage_documentation`
- `App.jsx` builds:
  - `nodeTypes` (type -> component)
  - `categorizedNodes` (category -> list of descriptors)

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
- `Data/Engine/web-interface/src/Scheduling/Create_Job.jsx` uses React Flow for job status and dependency visualization.

## API Endpoints
None. This is a UI-only domain.

## Related Documentation
- [Assemblies and Quick Jobs](assemblies.md)
- [Scheduled Jobs](scheduled-jobs.md)
- [UI and Notifications](ui-and-notifications.md)

## Codex Agent (Detailed)
### How node modules are structured
- A node file exports a descriptor object, for example:
  - `type: "agent"`
  - `component: BorealisAgentNode`
  - `config: [{ key, label, type, defaultValue, optionsKey, ... }]`
- The `component` is the React Flow node UI.
- The descriptor is used by the sidebar for display and configuration forms.

### Adding a new node (step-by-step)
1) Create a new file under `Data/Engine/web-interface/src/nodes/<Category>/Node_<Name>.jsx`.
2) Export a descriptor object as the default export with `type` and `component` fields.
3) Include `config` entries if you want the configuration sidebar to render fields.
4) Rebuild the WebUI (or run Vite dev mode) so `import.meta.glob` picks it up.
5) Validate drag/drop in the Node Sidebar and ensure the node renders correctly.

### Sidebar behavior
- `Node_Sidebar.jsx` renders `categorizedNodes` and sets `dataTransfer` payloads with `application/reactflow`.
- `Flow_Editor.jsx` listens for drop events and creates nodes from the descriptor catalog.

### Node configuration sidebar
- `Node_Configuration_Sidebar.jsx` uses `useReactFlow().setNodes` to update node data.
- `config` metadata drives form rendering; data is stored in `node.data`.

### Canvas interactions
- Right-click context menus allow node delete, edge unlink, and property edit.
- Snap guides are computed in `Flow_Editor.jsx` for alignment.

### Job flow editor
- `Scheduling/Create_Job.jsx` uses a custom React Flow setup for status and dependency visualization.
- Keep job flow nodes separate from the general node catalog to avoid accidental crossover.

### Common gotchas
- Folder path casing is `src/nodes/` in the repo, but `App.jsx` imports `./Nodes/` (Windows is case-insensitive).
- Ensure each node descriptor has a unique `type` or React Flow will mis-render.
- If the sidebar does not show the new node, verify the export default object has `type` and `component`.
