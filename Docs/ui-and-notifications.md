# UI and Notifications
[Back to Docs Index](index.md) | [Index (HTML)](index.html)

## Purpose
Describe the Borealis WebUI architecture, styling conventions, and the toast notification system.

## WebUI Architecture (High Level)
- Entry point: `Data/Engine/web-interface/src/App.jsx`.
- Global socket: `window.BorealisSocket` (Socket.IO client).
- Navigation: `Data/Engine/web-interface/src/Navigation_Sidebar.jsx`.
- Page template reference: `Data/Engine/web-interface/src/Admin/Page_Template.jsx` (layout only).

## Styling and Layout
- Borealis uses a MagicUI styling language with glass panels, gradients, and Quartz-themed AG Grid tables.
- The full MagicUI and AG Grid specification is embedded in the Codex Agent section below.

## Toast Notifications
- Backend: `POST /api/notifications/notify`.
- Transport: Socket.IO event `borealis_notification`.
- Frontend: `Data/Engine/web-interface/src/Notifications.jsx`.

## API Endpoints
- `POST /api/notifications/notify` (Token Authenticated) - broadcast a toast to all connected operators.

## Related Documentation
- [Engine Runtime](engine-runtime.md)
- [API Reference](api-reference.md)
- [Technical Debt](technical-debt.md)
- [Logging and Operations](logging-and-operations.md)
- [VPN and Remote Access](vpn-and-remote-access.md)

## Codex Agent (Detailed)
### Shared Conventions (Full)
- Cross-cutting guidance that applies to both Agent and Engine work.
- Domain-specific rules live in the Agent and Engine runtime docs.
- UI and AG Grid rules are defined in this document under the MagicUI and AG Grid sections.
- Add further shared topics here (for example, triage process, security posture deltas) instead of growing `AGENTS.md`.

### Shared UI (MagicUI + AG Grid) (Full)
Applies to all Borealis frontends. Use `Data/Engine/web-interface/src/Admin/Page_Template.jsx` as the canonical visual reference (no API/business logic). Keep this doc as the single source of truth for styling rules and AG Grid behavior.

- Toast notifications: see the Toast Notifications section below for endpoint, payload, severity variants, and quick test commands.

#### Page Template Reference
- Purpose: visual-only baseline for new pages; copy structure but wire your data in real pages.
- Header: small Material icon left of the title, subtitle beneath, utility buttons on the top-right.
- Shell: avoid gutters on the Paper.
- Selection column (for bulk actions): pinned left, square checkboxes, header checkbox enabled, about 52px fixed width, no menu/sort/resize; rely on AG Grid built-ins.
- Typography/buttons: IBM Plex Sans, gradient primary buttons, rounded corners (about 8px), themed Quartz grid wrapper.

#### Navigation Sidebar Active Page Mapping
- File: `Data/Engine/web-interface/src/Navigation_Sidebar.jsx`.
- Goal: keep the parent nav item highlighted when operators navigate to nested detail or editor pages.
- Source of truth: update the `NAV_ITEM_ALIASES` mapping so nested `currentPage` values resolve to the parent nav item.
- Add new nested pages explicitly; do not rely on URL prefixes or path parsing in the sidebar.
- Examples (keep these in the mapping unless the UI changes):
- `device_details` -> `devices`
- `filter_editor` -> `filters`
- `create_job` -> `jobs`
- `scripts`, `ansible_editor`, `workflow-editor` -> `assemblies`

#### MagicUI Styling Language (Visual System)
- Full-bleed canvas: hero shells run edge-to-edge; inset padding lives inside cards so gradients feel immersive.
- Glass panels: glassmorphic layers (`rgba(15,23,42,0.7)`), rounded 16-24px corners, blurred backdrops, micro borders, optional radial flares for motion.
- Hero storytelling: start views with stat-forward heroes, gradient StatTiles (min 160px) and uppercase pills (HERO_BADGE_SX) summarizing live signals/filters.
- Summary data grids: use AG Grid inside a glass wrapper (two columns Field/Value), matte navy background, no row striping.
- Tile palettes: online cyan to green; stale orange to red; needs update violet to cyan; secondary metrics fade from cyan into desaturated steel for consistent hue families.
- Hardware islands: storage/memory/network blocks reuse Quartz theme in rounded glass shells with flat fills; present numeric columns (Capacity/Used/Free/%) to match Device Inventory.
- Action surfaces: control bars live in translucent glass bands; filled dark inputs with cyan hover borders; primary actions are pill-shaped gradients; secondary controls are soft-outline icon buttons.
- Anchored controls: align selectors/utility buttons with grid edges in a single row; reserve glass backdrops for hero sections so content stays flush.
- Buttons and chips: gradient pills for primary CTAs (`linear-gradient(135deg,#34d399,#22d3ee)` success; `#7dd3fc->#c084fc` creation); neutral actions use rounded outlines with `rgba(148,163,184,0.4)` borders and uppercase microcopy.
- Rainbow accents: for creation CTAs, use dark-fill pills with rainbow border gradients and teal halo (shared with Quick Job).
- AG Grid treatment: Quartz theme with matte navy headers, subtle alternating row opacity, cyan/magenta interaction glows, rounded wrappers, soft borders, inset selection glows.
- Default grid cell padding: keep roughly 18px on the left edge and 12px on the right for standard cells (12px/9px for `auto-col-tight`) so text never hugs a column edge. Target the center + pinned containers so both regions stay aligned.
- Overlays/menus: `rgba(8,12,24,0.96)` canvas, blurred backdrops, thin steel borders; bright typography; deep blue glass inputs; cyan confirm, mauve destructive accents.

#### Page-Level Tabs
- Placement: sit directly below the hero title/subtitle band (8-16px gap). Tabs span the full width of the content column.
- Typography: match Navigation Sidebar typography. Inherit the font family (IBM Plex Sans via theme), use `fontSize: "0.8rem"`, mixed case labels (`textTransform: "none"`). Default `fontWeight: 400`; active tabs are `fontWeight: 600`. Standard rail height is `32px` (compact stacks use `28px`).
- Indicator: 3px tall bar with rounded corners that uses the Navigation Sidebar cyan (`#7db7ff`). Keep it flush with the bottom border so it reads as a light strip under the active tab.
- Hover/active treatment: hover background uses the Navigation Sidebar hover fill `rgba(255,255,255,0.05)`. Selected tabs use the same active highlight gradient as the sidebar: `linear-gradient(90deg, rgba(125,183,255,0.14) 0%, rgba(125,183,255,0.06) 55%, rgba(125,183,255,0.00) 100%)`. Keep the highlight on hover for selected tabs.
- Colors: base text `#cbd5e1` (Navigation Sidebar unselected). Selected text `#e6f2ff`. Always force `opacity: 1` to avoid MUI's default faded text on unfocused tabs.
- Icons: when tabs include glyphs, color the icon wrapper `#8fbfff` when idle and `#7db7ff` when selected (match Navigation Sidebar glyphs). Do not hardcode icon colors on the icon component; rely on the wrapper style so selected states update automatically.
- Shape/spacing: tabs are pill-like with `borderRadius: 4` (MUI unit `1`). Maintain `minHeight: 44px` so targets are touchable. Provide `borderBottom: 1px solid MAGIC_UI.panelBorder` (or the local shell border) to anchor the rail.
- CSS/SX snippet to copy into new tab stacks:
```jsx
const NAV_TAB_HEIGHT = 32;
const NAV_TAB_HEIGHT_COMPACT = 28;
const NAV_TAB_COLORS = {
  text: "#cbd5e1",
  textActive: "#e6f2ff",
  icon: "#8fbfff",
  iconActive: "#7db7ff",
  hover: "rgba(255,255,255,0.05)",
  activeBg:
    "linear-gradient(to top, rgba(125,183,255,0.14) 0%, rgba(125,183,255,0.06) 55%, rgba(125,183,255,0.00) 100%)",
};

<Tabs
  value={tab}
  onChange={(_, v) => setTab(v)}
  variant="scrollable"
  scrollButtons="auto"
  TabIndicatorProps={{
    style: {
      height: 3,
      borderRadius: 3,
      background: NAV_TAB_COLORS.iconActive,
    },
  }}
  sx={{
    borderBottom: `1px solid ${MAGIC_UI.panelBorder}`,
    minHeight: NAV_TAB_HEIGHT,
    height: NAV_TAB_HEIGHT,
    "& .MuiTabs-flexContainer": {
      minHeight: NAV_TAB_HEIGHT,
      height: NAV_TAB_HEIGHT,
      alignItems: "stretch",
    },
    "& .MuiTab-root": {
      color: NAV_TAB_COLORS.text,
      fontFamily: "inherit",
      fontSize: "0.8rem",
      textTransform: "none",
      fontWeight: 400,
      minHeight: NAV_TAB_HEIGHT,
      height: NAV_TAB_HEIGHT,
      opacity: 1,
      borderRadius: 1,
      py: 0.35,
      transition: "background 160ms ease, box-shadow 160ms ease, color 160ms ease, transform 120ms ease",
      "& .MuiTab-iconWrapper": {
        color: NAV_TAB_COLORS.icon,
      },
      "&:hover": {
        background: NAV_TAB_COLORS.hover,
      },
      "&:active": {
        transform: "translateY(0.5px)",
      },
    },
    "& .MuiTab-root.Mui-selected": {
      color: NAV_TAB_COLORS.textActive,
      fontWeight: 600,
      background: NAV_TAB_COLORS.activeBg,
      "& .MuiTab-iconWrapper": {
        color: NAV_TAB_COLORS.iconActive,
      },
      "&:hover": {
        background: NAV_TAB_COLORS.activeBg,
      },
    },
  }}
>
  {TABS.map((t) => (
    <Tab key={t} label={t} icon={t.icon} iconPosition="start" />
  ))}
</Tabs>
```
- Interaction rules: tabs should never scroll vertically; rely on horizontal scroll for overflow. Always align the tab rail with the first section header on the page so the aurora indicator lines up with hero metrics.
- Accessibility: keep `aria-label` and `aria-controls` pairs when the panes hold complex content, and ensure the gradient backgrounds preserve 4.5:1 contrast for the text (the current cyan on dark meets this).

#### Page-Level Action Buttons
- Place page-level actions/buttons/hero-badges in a fixed overlay at the top-right, just below the global menu bar. Match the Filter Editor placement if an example is needed `Data/Engine/web-interface/src/Devices/Filters/Filter_Editor.jsx`: wrapper `position: "fixed"`, `top: { xs: 72, md: 88 }`, `right: { xs: 12, md: 20 }`, `zIndex: 1400`, with `pointerEvents: "none"` on the wrapper and `pointerEvents: "auto"` on the inner `Stack` so underlying content remains clickable.
- Use gradient primary pills and outlined secondary pills (rounded 999 radius, MagicUI colors). Keep horizontal spacing via a `Stack` (for example, `spacing={1.25}`); do not nest these buttons inside the title grid or tab rail.
- Tabs stay in normal document flow beneath the title/subtitle; the floating action bar should not shift layout. When operators request moving page actions (or when building new pages), apply this fixed overlay pattern instead of absolute positioning tied to tab rails.
- Keep the responsive offsets (xs/md) unless a specific page has a different header height/padding; only adjust the numeric values when explicitly needed to align with a nonstandard shell.

#### AG Grid Column Behavior (All Tables)
- Auto-size value columns and let the last column absorb remaining width so views span available space.
- Declare `AUTO_SIZE_COLUMNS` near the grid component (exclude the fill column).
- Helper: store the grid API in a ref and call `api.autoSizeColumns(AUTO_SIZE_COLUMNS, true)` inside `requestAnimationFrame` (or `setTimeout(...,0)` fallback); swallow errors because it can run before rows render.
- Hook the helper into both `onGridReady` and a `useEffect` watching the dataset (for example, `[filteredRows, loading]`); skip while `loading` or when there are zero rows.
- Column defs: apply shared `cellClass: "auto-col-tight"` (or equivalent) to every auto-sized column for consistent padding. Last column keeps the class for styling consistency.
- CSS override: ensure the wrapper targets both center and pinned containers so every cell shares the same flex alignment. Then apply the tighter inset to `auto-col-tight`:
```jsx
"& .ag-center-cols-container .ag-cell, & .ag-pinned-left-cols-container .ag-cell, & .ag-pinned-right-cols-container .ag-cell": {
  display: "flex",
  alignItems: "center",
  justifyContent: "flex-start",
  textAlign: "left",
  padding: "8px 12px 8px 18px",
},
"& .ag-center-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-left-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-right-cols-container .ag-cell .ag-cell-wrapper": {
  width: "100%",
  display: "flex",
  alignItems: "center",
  justifyContent: "flex-start",
  padding: 0,
},
"& .ag-center-cols-container .ag-cell.auto-col-tight, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight": {
  paddingLeft: "12px",
  paddingRight: "9px",
},
```
- Style helper: reuse a `GRID_STYLE_BASE` (or similar) to set fonts/icons and `--ag-cell-horizontal-padding: "18px"` on every grid, then merge it with per-grid dimensions.
- Fill column: last column `{ flex: 1, minWidth: X }` (no width/maxWidth) to stretch when horizontal space remains.
- Pagination baseline: every Quartz grid ships with `pagination`, `paginationPageSize={20}`, and `paginationPageSizeSelector={[20, 50, 100]}`. This matches Device List behavior and prevents infinitely tall tables (Targets, assembly pickers, job histories, etc.).
- Focus highlight baseline: set `suppressCellFocus` on AG Grid tables that do not rely on keyboard cell navigation so click/focus does not draw the magenta single-cell outline. Keep row hover/row selected styling for context.
- Borealis blue accent baseline: use `themeQuartz.withParams({ accentColor: "#7dd3fc" })` and keep `--ag-checkbox-checked-color: "#7dd3fc"` where checkbox columns are present.
- Row hover baseline: use a darker Borealis blue hover state (`--ag-row-hover-color: "rgba(73,156,196,0.2)"`) so hover stays in the same color family as selected rows without matching the selected intensity.
- Selected row highlight baseline: use `backgroundColor: "rgba(125,211,252,0.2) !important"` with `boxShadow: "inset 0 0 0 1px rgba(125,211,252,0.45)"` so checkbox selection state reads in Borealis blue.
- Inline grid text inputs: when a page embeds a MUI `TextField` inside AG Grid (for example, Device Description), use `MAGIC_UI.accentA` (`#7dd3fc`) for hover/focus border color.
- Example: follow the scaffolding in `Engine/web-interface/src/Scheduling/Scheduled_Jobs_List.jsx` and the structure in `Data/Engine/web-interface/src/Admin/Page_Template.jsx`.

### Toast Notifications (Full)
Use this guide to add, configure, and test transient toast notifications across Borealis. It documents the backend endpoint, frontend listener, payload contract, and quick Firefox console commands you can hand to operators for validation.

#### Components and paths
- Backend endpoint: `Data/Engine/services/API/notifications/management.py` (registered as `/api/notifications/notify`).
- Frontend listener and renderer: `Data/Engine/web-interface/src/Notifications.jsx` (mounted in `App.jsx`).
- Transport: Socket.IO event `borealis_notification` broadcast to connected WebUI clients.

#### Backend behavior
- Auth: Uses `RequestAuthContext.require_user()`; session or bearer must be present. Returns `401/403` otherwise.
- Route: `POST /api/notifications/notify`
  - Emits `borealis_notification` over Socket.IO (no persistence).
  - Logs via `service_log("notifications", ...)`.
- Validation: Requires `message` in payload. `title` defaults to `"Notification"` if omitted.
- Registration: API group `notifications` is enabled by default via `DEFAULT_API_GROUPS` and `_GROUP_REGISTRARS` in `Data/Engine/services/API/__init__.py`.

#### Payload schema
Send JSON body (session-authenticated):
- `title` (string, optional): heading line. Default `"Notification"`.
- `message` (string, required): body copy.
- `icon` (string, optional): Material icon name hint (for example, `info`, `filter`, `schedule`, `warning`, `error`). Falls back to `NotificationsActive`.
- `variant` (string, optional): visual theme. Accepted: `info` | `warning` | `error` (case-insensitive). Aliases: `type` or `severity`. Defaults to `info`.
- `ttl_ms` (number, optional): client-side lifetime in milliseconds; defaults to about 5200ms before fade-out.

Notes:
- Payload is fanned out verbatim to the WebUI (plus server-added fields: `id`, `username`, `role`, `created_at`).
- The client caps the visible stack to the 5 most recent items (newest on top).
- Non-empty `message` is mandatory; otherwise HTTP 400.

#### Frontend rendering rules
- Component: `Notifications.jsx` listens to `borealis_notification` on `window.BorealisSocket`.
- Stack position: fixed top-right, high z-index, pointer events enabled on toasts only.
- Auto-dismiss: about 5s default; each item fades out and is removed.
- Theme by `variant`:
  - `info` (default): Borealis blue aurora gradient.
  - `warning`: muted amber gradient.
  - `error`: deep red gradient.
- Icon: no container; uses the provided Material icon hint. Small drop shadow for legibility.

#### Implementation steps (recap)
1) Backend: ensure `/api/notifications/notify` is registered (already in repo). New services should import `register_notifications` if API groups are customized.
2) Emit: from any authenticated server flow, POST to `/api/notifications/notify` with the payload above.
3) Frontend: `App.jsx` mounts `Notifications` globally; no per-page wiring needed.
4) Test: use the Firefox console examples below while logged in to confirm toast rendering.

#### Firefox console examples (run while signed in)
Info (default blue):
```js
fetch("/api/notifications/notify", {
  method: "POST",
  credentials: "include",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({
    title: "Test Notification",
    message: "Hello from the console!",
    icon: "info",
    variant: "info"
  })
}).then(r => r.json()).then(console.log).catch(console.error);
```

Warning (amber):
```js
fetch("/api/notifications/notify", {
  method: "POST",
  credentials: "include",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({
    title: "Heads up",
    message: "This is a warning example.",
    icon: "warning",
    variant: "warning"
  })
}).then(r => r.json()).then(console.log).catch(console.error);
```

Error (red):
```js
fetch("/api/notifications/notify", {
  method: "POST",
  credentials: "include",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({
    title: "Error encountered",
    message: "Something failed during processing.",
    icon: "error",
    variant: "error"
  })
}).then(r => r.json()).then(console.log).catch(console.error);
```

#### Usage notes and tips
- Keep `message` concise; multiline is supported via `\n`.
- Use `icon` to match the source feature (for example, `filter`, `schedule`, `device`, `error`).
- The server adds `username` and `role` to payloads; the client currently shows all variants regardless of role (filtering is per-username match when present).
- If sockets are unavailable, the endpoint still returns 200; toasts simply will not render until Socket.IO is connected.

### Remote Shell UI Changes Handoff (Full)
This section captures the UI behavior requirements and troubleshooting context for the Remote Shell onboarding path.

#### Current situation
- The WireGuard tunnel and Remote Shell work once the agent SYSTEM socket is online.
- If the operator clicks Connect too early, the UI shows `agent_socket_missing` and no toast appears.
- Goal: prevent the Remote Shell connect attempt until the agent is actually ready, and show a toast notification if the operator clicks too early.

#### Required behavior
- When the agent SYSTEM socket is not registered, the UI must block the connection attempt, show a toast via `/api/notifications/notify`, and keep the UI idle (no tunnel/session attempt).
- Toast title: `Agent Onboarding Underway`.
- Toast message: `Please wait for the agent to finish onboarding into Borealis. It takes about 1 minute to finish the process.`

#### Important references
- Toast API and payload rules are documented above.
- UI file: `Data/Engine/web-interface/src/Devices/ReverseTunnel/Powershell.jsx`.
- API establish endpoint: `/api/shell/establish` returns `agent_socket` when available.
- Socket error path: `agent_socket_missing`.

#### Troubleshooting context
- Engine logs show `vpn_shell_open_failed ... reason=agent_socket_missing` when the SYSTEM socket is not connected.
- Toasts do not appear; likely causes: WebUI build is reused (`Existing WebUI build found`) or the UI error path does not trigger the toast.
- Ensure the toast is sent via `/api/notifications/notify` with `credentials: "include"` and the payload schema above.

#### Deliverables
- Update UI logic to call the notification API and block the connection attempt until readiness is confirmed.
- Cover both preflight status checks and the `agent_socket_missing` shell open response.
- Provide explicit rebuild/restart steps if the WebUI build must be refreshed.
