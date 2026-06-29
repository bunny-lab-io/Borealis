# UI and Notifications

Describe the Borealis WebUI architecture, styling conventions, and the toast notification system.
Treat this document as the single source of truth for Borealis WebUI design rules. Product pages may be referenced as example implementations, but when a page and this document disagree, this document wins and the page should be updated.

## WebUI Architecture (High Level)
- Entry point: `Data/Engine/Containers/webui-frontend/data/web-interface/src/App.jsx` bootstraps the app shell and router in `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/`.
- Router: `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/routes/router.jsx`.
- Shared shell: `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/shell/AppShell.jsx`.
- Login/bootstrap gate: `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/routes/LoginRoute.jsx` and `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/routes/BootstrapEntry.jsx`.
- Providers: `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/providers/` for bootstrap/auth session state and page chrome metadata.
- Global realtime bridge: `window.BorealisSocket` dispatches Go realtime events from `/api/realtime/events` through Server-Sent Events. Legacy Socket.IO transport is lazy and should not connect to `/socket.io` during normal authenticated page loads.
- Remote Desktop: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Remote_Desktop.jsx` uses Apache Guacamole VNC as the sole browser viewer and reports Guacamole service health before launch.
- Shared operator presence: authenticated browsers connect to `/api/realtime/events`; the Engine emits `server_operator_presence_changed` so informational admin pages can refresh live operator session data without waiting for their poll interval.
- Navigation: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Navigation_Sidebar.jsx`.
- Watchdog authoring: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Automation/Watchdogs/`.
- Alerts queue: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Alerting/Active_Alerts.jsx`.
- Page style template reference: `Data/Engine/Containers/webui-frontend/data/web-interface/src/DevTools/Page_Style_Template.jsx` (layout only).

## Operator Bootstrap Gate
- Borealis now resolves `/api/bootstrap/state` before it attempts `/api/auth/me` or renders the normal login form.
- Bootstrap phases are:
- `aegis_setup_required`
- `aegis_unlock_required`
- `admin_setup_required`
- `admin_recovery_required`
- `login_required`
- `BootstrapEntry.jsx` owns the public pre-auth journey:
- fresh deployment: set Aegis, create the first administrator, complete MFA
- restart: unlock Aegis, then proceed to the normal login or passkey view
- post-force-reset: set a new Aegis cipher, recover an existing administrator, complete MFA, then resume normal operation
- `AuthContext.jsx` now hydrates `bootstrapState` first. If bootstrap is not yet `login_required`, it clears any cached operator session and suppresses Aegis status polling until the Engine is ready for normal auth.
- The normal login surface in `Data/Engine/Containers/webui-frontend/data/web-interface/src/Login.jsx` now stays hidden until bootstrap reaches `login_required`.

## Access Management UX
- Credentials:
- The Aegis Cipher row still lives on `Access_Management/Credential_List.jsx`, but normal runtime actions are now `Rotate Aegis Cipher` and `Force Reset Aegis Cipher`.
- `Setup Aegis Cipher` and `Enter Aegis Cipher` moved out of the authenticated Credentials page and into the public bootstrap gate.
- The Aegis row now advertises that it protects operator auth, reusable credentials, and the GitHub API token.
- User Management:
- `Access_Management/Users.jsx` now surfaces `Source` and `Recovery` state columns. Directory users show provider/domain origin and expose cache-disable actions instead of local password/role/passkey management.
- `Access_Management/Directory_Services.jsx` owns LDAP, LDAPS, and Active Directory provider configuration under Access Management > Directory Services.
- Operators flagged by Aegis force reset show `Recovery Required`, and the row action relabels the password-reset flow to `Recover Account`.
- Recovering or resetting a flagged account also clears stale MFA/passkey auth material so the operator re-enrolls cleanly.

## Styling and Layout
- Borealis uses a MagicUI styling language with glass panels, gradients, and Quartz-themed AG Grid tables.
- The full MagicUI and AG Grid specification is embedded in the Codex Agent section below.
- Workflow Runtime v1 node-shell and port-row styling rules live in [Workflows](../Using%20the%20Platform/Assemblies/workflows.md). Use that document as the source of truth for workflow node title sizing, status badges, named port rows, and Action-vs-data edge behavior.
- Workflow Runtime v1 naming and edge-label behavior also live there. In particular, use `Device Filter` and `List of Devices` for the targeting nodes, keep workflow data edges dashed Borealis blue, and let target edges auto-label with device counts when available.
- Workflow node inspection also follows that document: the node sidebar `Debug Info` tab is available in both authoring mode and run-snapshot mode, with authoring previews clearly treated as pre-runtime estimates rather than executed results.

## Toast Notifications
- Backend: `POST /api/notifications/notify`.
- Transport: Socket.IO event `borealis_notification`.
- Frontend: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Notifications.jsx`.
- Scope: include `username` in the payload to target a specific signed-in operator; the frontend filters user-scoped notifications to that operator while unscoped notifications still broadcast to all connected operators.
- Installed Software now uses that same toast path for operator-managed software actions. Right-click actions such as `Create Global Icon Override`, `Create Global Uninstall Override`, `Block Uninstallation`, `Unblock Uninstallation`, and the page-level `Query Software Changes` button all post success or failure notifications through the shared notification system.

## Watchdogs and Alerts UX
- Sidebar placement:
  - `Automation -> Watchdogs`
  - `Alerting & Reporting -> Alerts`
- Watchdog authoring uses a tabbed editor with `Name`, `Scope`, `Targets`, `Rules`, `Actions`, and `Preview`.
- The `Preview` tab is part of the primary authoring flow and resolves current targets before save.
- The `Targets` tab uses 3-character minimum typeahead search panels for devices and saved filters instead of large autocomplete dropdowns.
- Alerts is a separate incident queue so operators can work alerts without opening the underlying policy editor.
- Alerts, Device Filters, and other grid-first pages use the shared Filter Slider pattern for queue/state/source filters instead of the older Material tab rail.
- Alerts starts in an unfiltered all-alerts view; clicking a status pill applies that queue filter, and clicking the same pill again clears it back to all alerts.
- Alerts relies on AG Grid's built-in column filters rather than a page-level custom filter bar.
- Device Summary includes a `Watchdogs` tab so operators can acknowledge incidents, suppress a watchdog for one device, or launch a prefilled device-scoped watchdog draft.
- Device Summary includes a right-anchored `Agent Health` tab. Keep agent-health tab composition in `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Agent_Health.jsx`, with focused helper components beside it under `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/`; the Summary tab should not contain an agent-health island or summary-section nav item.
- The Agent Health tab should show a visual startup flow like Remote Desktop readiness flows, plus clickable role/service runtime-health nodes inside the flow graph. Flow states are `complete`, `active`, `failed`, `pending`, and `skipped`; active nodes use the same spinner language as the linear readiness view.
- Agent Health refreshes silently from `agent_status_changed` on `window.BorealisSocket`; avoid toasts for routine startup progression.
- Filesystem browser surfaces should follow the explorer pattern now established in the Device Summary `File Management` tab:
  - use a single AG Grid with a custom tree-style `Name` column when operators benefit from seeing the broader hierarchy while working
  - keep the current working directory URL-synced through a stable query key such as `working_directory` so browser refreshes, post-action refreshes, and shared links reopen the same folder depth instead of collapsing back to the roots view
  - default hidden files and folders to off behind an explicit `Show Hidden Items` checkbox instead of showing them by default
  - use an address bar with two modes:
    - default mode renders clickable chevron-delimited path segments for fast parent navigation
    - clicking into the empty address-bar space to the right of the segment trail switches into raw-path edit mode for direct copy/paste and manual path entry
  - keep a clickable root affordance on the far-left of that address bar so operators can jump back to the filesystem root quickly
  - give both the root affordance and the chevron path segments an obvious soft Borealis-blue hover state so they read clearly as navigation controls rather than static labels
  - keep a copy affordance on the far-right edge of that address bar so operators can copy the current path without switching modes
  - lightweight inline text editing should open in a large glass dialog that mirrors the StdOut / StdErr viewer shell: path subtitle on the left, `Save` and `Close` actions on the top-right, and a monospace syntax-highlighted editor surface sized for quick inline edits rather than full IDE workflows
  - duplicate upload conflicts should use an explorer-style `Replace or Skip Files` decision dialog rather than a generic confirmation modal
- Real-time refresh uses `watchdog_incidents_changed` and `device_watchdogs_changed` on the shared `window.BorealisSocket`.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - `POST /api/notifications/notify` (Token Authenticated) - broadcast a toast to all connected operators.
    - `GET /api/devices/search?hostname=<query>` (Token Authenticated) - shared header device search, scoped to the current operator's visible sites unless the operator is an admin.
    - `GET /api/server/overview` (Operator Admin Session) - returns the Server Info dashboard snapshot including service state, host runtime details, WireGuard runtime status, public-edge certificate health, and live operator presence.
    - `GET /api/server/workers` (Operator Admin Session) - returns active and recent site-worker container state, all sites with total and online device counts, connected Agent counts, and worker-assigned work for Engine Status and Sites. Sites does not call this endpoint from the route loader; it immediately shows `Polling Site Worker Metrics in 10s`, starts polling after the first 5-second cadence, and displays mini-trends after the second successful sample. Sites forces AG Grid to refresh the Site Worker Container renderer because the visible metric value is a nested Docker stats sample, not the stable container id.
    - `GET /api/server/site-worker-settings` (Operator Admin Session) - returns profile-managed site-worker scheduled-lane work-item capacity shown in Server Info.
    - `GET /api/server/agent-release-channels` (Operator Admin Session) - returns Agent update channel targets shown in Server Info.
    - `PUT /api/server/agent-release-channels` (Operator Admin Session) - updates default Agent update channel or repo and refreshes cached artifacts.
    - `POST /api/server/agent-release-channels/refresh` (Operator Admin Session) - refreshes Agent update channel metadata and cached artifacts.
    - `POST /api/server/services/<service_key>/action` (Operator Admin Session) - queues the corresponding container service command shown on Server Info rows: restart, rebuild, reload, or reconcile.
    - `POST /api/server/services/<service_key>/restart` (Operator Admin Session) - queues a safe detached restart for `borealis_engine`, `borealis_traefik`, or a specific `postgresql_cluster` instance.
    - `POST /api/server/wireguard/recover` (Operator Admin Session) - queues WireGuard tunnel reconcile when active tunnels exist.
    - `GET /api/watchdogs` (Token Authenticated) - list watchdog policies for the Watchdogs page.
    - `POST /api/watchdogs/preview` (Token Authenticated) - preview the current watchdog outcome from the editor.
    - `GET /api/watchdogs/incidents` (Token Authenticated) - list queue incidents for Alerts and return queue counts.
    - `POST /api/watchdogs/incidents/<int:incident_id>/acknowledge` (Token Authenticated) - acknowledge an incident from Alerts or a device page.
    - `POST /api/watchdogs/incidents/<int:incident_id>/state` (Token Authenticated) - move an incident between the Alerts `Open` and `Suppressed` queues.
    - `GET /api/devices/<device_id>/watchdogs` (Token Authenticated) - hydrate the device-level Watchdogs tab.
    - `POST /api/devices/<device_id>/watchdogs/overrides` (Token Authenticated) - apply or clear a device-specific watchdog suppression.
    - `GET /api/device/files/<hostname>/roots` (Token Authenticated) - hydrate the Device Summary `File Management` roots view.
    - `GET /api/device/files/<hostname>/children?path=<absolute-path>` (Token Authenticated) - lazy-load one File Management directory.
    - `POST /api/device/files/<hostname>/upload` (Token Authenticated) - start a File Management upload transfer.
    - `POST /api/device/files/<hostname>/download` (Token Authenticated) - start a File Management download transfer.
    - `GET /api/device/processes/<hostname>?max_age_seconds=<seconds>` (Token Authenticated) - hydrate the Device Summary `Processes` tab with a live process snapshot.
    - `POST /api/device/processes/<hostname>/terminate` (Token Authenticated) - end one process from the Device Summary `Processes` context menu.
    - `POST /api/agent/status` (Device Authenticated) - agent startup status source for the Device Summary Agent Health timeline.

    ### Related documentation

    - [Engine Runtime](../Reference/Core%20Runtimes/engine-runtime.md)
    - [API Reference](../Reference/Data%20and%20Schema/api-reference.md)
    - [Technical Debt issues](https://github.com/bunny-lab-io/Borealis/issues?q=is%3Aissue%20label%3A%22Technical%20Debt%22)
    - [Engine Log Management](../Using%20the%20Platform/engine-log-management.md)
    - [Remote Shell](../Using%20the%20Platform/remote-shell.md)
    - [Workflows](../Using%20the%20Platform/Assemblies/workflows.md)
    - [Migrating Pages to React Router](../Reference/Migration%20Paths/migrating-pages-to-react-router.md)
    - [Watchdogs](../Using%20the%20Platform/watchdogs.md)
    - [Alerts](../Using%20the%20Platform/alerts.md)
    - [Software Icon Overrides](software-icon-overrides.md)
    - [Software Uninstall Overrides](software-uninstall-overrides.md)
    - [Software Uninstall Blocklist](software-uninstall-blocklist.md)

    ### Shared Conventions (Full)
    - Cross-cutting guidance that applies to both Agent and Engine work.
    - Domain-specific rules live in the Agent and Engine runtime docs.
    - UI and AG Grid rules are defined in this document under the MagicUI and AG Grid sections.
    - Add further shared topics here (for example, triage process, security posture deltas) instead of growing `AGENTS.md`.

    ### Shared UI (MagicUI + AG Grid) (Full)
    Applies to all Borealis frontends. Use `Data/Engine/Containers/webui-frontend/data/web-interface/src/DevTools/Page_Style_Template.jsx` as the canonical visual reference (no API/business logic). Keep this doc as the single source of truth for styling rules and AG Grid behavior.

    - Toast notifications: see the Toast Notifications section below for endpoint, payload, severity variants, and quick test commands.

    #### Page Style Template Reference
    - Purpose: visual-only baseline for new pages; copy structure but wire your data in real pages.
    - Header: small Material icon left of the title, subtitle beneath, utility buttons on the top-right.
    - Body: primary pages should render their main content through `Data/Engine/Containers/webui-frontend/data/web-interface/src/PageBodyFrame.jsx` so the shared body shell, outer inset, subtitle-to-body spacing, and edge-bleed behavior stay consistent.
    - Shell: avoid gutters on the Paper.
    - Selection column (for bulk actions): pinned left, header checkbox enabled, about 52px fixed width, no menu/sort/resize; rely on AG Grid and Quartz built-ins for checkbox visuals.
    - Typography/buttons: IBM Plex Sans, gradient primary buttons, rounded corners (about 8px), themed Quartz grid wrapper.

    #### Standardized Page Bodies
    - Primary pages rendered beneath the shared App header use `Data/Engine/Containers/webui-frontend/data/web-interface/src/PageBodyFrame.jsx`.
    - `AppShell.jsx` plus `PageChromeProvider` own the title, subtitle, icon, and header action rail. `PageBodyFrame` owns the body inset, rounded outer shell, shell chrome, and variant-specific structure.
    - Supported variants: `grid`, `grid_with_stack`, `split_tool`, `content_panel`.
    - Informational admin dashboards such as Server Info should prefer `content_panel`, start with a hero strip of high-signal stat cards, and then stack glass sections beneath it rather than recreating toolbar chrome inside the body.
    - Server Info includes a runtime row for Site Worker Scheduled Tasks so operators can see profile-managed per-worker scheduled-lane work-item capacity. Operators change it by changing host sizing/profile inputs and redeploying, not by editing a UI field.
    - Sites merges site-worker state into the main AG Grid rows. It loads site records first, immediately counts down from `Polling Site Worker Metrics in 10s`, starts `/api/server/workers?history_seconds=300` polling after the first 5-second cadence, then shows CPU/RAM/NET/DISK mini-trends after the second successful worker sample. Connected Devices renders `Analyzing Agent Connections` until that second worker sample makes metrics visible. Connected/disconnected/offline Agent bars and Scheduled Jobs-style task bars follow the same worker payload cadence. Keep the Site Worker Container column refresh value tied to the latest Docker stats/history sample so AG Grid does not skip rerendering when the container id stays unchanged. Keep Connected Devices stable across brief zero-connected worker polls by reusing the last non-zero connected breakdown for the browser grace window; sustained zero-connected payloads must still turn the bar red.
    - When a site has no active site-worker container stats, the Site Worker Container column uses the same muted empty-state treatment as task rows and shows `Site Worker Not Running`.
    - Sites resource mini-trends keep browser-only history for the last 60 seconds. CPU shows percent, RAM shows bytes, NET shows calculated throughput from Docker byte counters, and DISK shows Docker `SizeRootFs` container footprint with Docker-native storage limits when available.
    - Engine Status task cards label device count separately from scheduler slot count. `Task (8 Devices)` means eight targets are represented by the card; it can be one shared Ansible work item or a grouped set of individual one-target work items.
    - Engine Status terminal task cards show `Success`, `Failed`, or `Skipped` with a 30-second countdown. Matching terminal events update the existing bucket and reset the countdown; orphaned work awaiting a replacement site worker shows `Reassigning to New Worker`, while the worker lane shows `Re-Deploying`.
    - Default body inset is `px: 2`, `pt: 2.5`, and `pb: 2`. The `pt: 2.5` token adds 20px of shared spacing between the page subtitle and the body frame.
    - The body shell keeps that outer inset, but the main page content should bleed to the inside edge of the shell rather than sitting inside a second shared padding layer.
    - Do not add manual `mt: "10px"` drops or top-level `p: 3` wrappers to compensate for the header or to recreate internal shell padding.
    - Pre-grid banners, filter pills, selector rows, and page-local toolbars belong inside the same shell on `grid_with_stack` pages.
    - Implementation guide: `Docs/features_to_implement/standardized_page_bodies.md`.

    #### Navigation Sidebar Active Page Mapping
    - File: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Navigation_Sidebar.jsx`.
    - Goal: keep the parent nav item highlighted when operators navigate to nested detail or editor pages.
    - Source of truth: route `handle.navKey` values in `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/routes/router.jsx`.
    - The sidebar now owns static nav targets from `APP_PATHS`; it does not maintain a page-key alias table.
    - Examples:
    - device details use `pageKey: "device"` and `navKey: "devices"`
    - filters use `pageKey: "filter"` and `navKey: "filters"`
    - jobs use `pageKey: "job"` and `navKey: "jobs"`
    - assemblies use `pageKey: "script-assembly"`, `pageKey: "ansible-playbook"`, or `pageKey: "workflow"` with `navKey: "assemblies"`

    #### Global Device Search
    - Shared header ownership: `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/shell/AppShell.jsx` places the global device search in the top app bar; the search UI itself lives in `Data/Engine/Containers/webui-frontend/data/web-interface/src/GlobalDeviceSearch.jsx`.
    - Scope: search is hostname-only and should use `GET /api/devices/search?hostname=<query>` so operators only see devices inside their assigned sites while admins can see any device, including unassigned inventory.
    - Minimum activation: do not open the search overlay or call the API until the operator has entered at least 3 characters.
    - Presentation: style the field as a compact dark glass control with cyan hover/focus treatment so it feels like part of the shared header band rather than a legacy toolbar widget.
    - Results overlay: use a compact AG Grid rendered as a dropdown surface with `Hostname` and `Site` columns, Quartz styling, muted matte headers, and rounded overlay chrome.
    - Cell treatment: hostnames use the Borealis blue accent; site names use the shared muted gray copy used elsewhere in Borealis. Unassigned devices may display `Not Configured`.
    - Interaction: clicking a row should navigate directly to the target device's canonical `/devices/:deviceId` route.

    #### MagicUI Styling Language (Visual System)
    - Full-bleed canvas: hero shells run edge-to-edge; inset padding lives inside cards so gradients feel immersive.
    - Glass panels: glassmorphic layers (`rgba(15,23,42,0.7)`), rounded 16-24px corners, blurred backdrops, micro borders, optional radial flares for motion.
    - Hero storytelling: start views with stat-forward heroes, gradient StatTiles (min 160px) and uppercase pills (HERO_BADGE_SX) summarizing live signals/filters.
    - Summary data grids: use AG Grid inside a glass wrapper (two columns Field/Value), matte navy background, no row striping.
    - Tile palettes: online cyan to green; stale orange to red; needs update violet to cyan; secondary metrics fade from cyan into desaturated steel for consistent hue families.
    - Hardware islands: storage/memory/network blocks reuse Quartz theme in rounded glass shells with flat fills; present numeric columns (Capacity/Used/Free/%) to match Device Inventory.
    - Action surfaces: control bars live in translucent glass bands; filled dark inputs with cyan hover borders; primary actions are pill-shaped gradients; secondary controls are soft-outline icon buttons.
    - Anchored controls: align selectors/utility buttons with grid edges in a single row; reserve glass backdrops for hero sections so content stays flush.
    - Buttons and chips: page-header primary CTAs use a consistent cyan-to-violet gradient (`#7dd3fc -> #c084fc`); neutral actions use rounded outlines with `rgba(148,163,184,0.4)` borders and mixed-case labels.
    - Do not use rainbow-border CTAs in the shared page header rail.
    - AG Grid treatment: Quartz theme with matte navy headers, subtle alternating row opacity, cyan/magenta interaction glows, rounded wrappers, soft borders, inset selection glows.
    - Explorer-style file browsers and similar nested navigation surfaces that need broad hierarchy visibility should use community AG Grid plus a React-managed flattened tree. Do not switch those surfaces to AG Grid Tree Data or grouping APIs for this workflow. Reference implementation: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Remote_File_Management.jsx`.
    - Default grid cell padding: keep roughly 18px on the left edge and 12px on the right for standard cells (12px/9px for `auto-col-tight`) so text never hugs a column edge. Target the center + pinned containers so both regions stay aligned.
    - Overlays/menus: `rgba(8,12,24,0.96)` canvas, blurred backdrops, thin steel borders; bright typography; deep blue glass inputs; cyan confirm, mauve destructive accents.

    #### Right-Click Context Menus
    - Use a right-click context menu for row-targeted grid actions when operators benefit from working directly in-table rather than moving back to a page-level toolbar.
    - Match the shared Borealis context-menu model.
    - Reference implementations currently live in:
      - `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Installed_Software.jsx`
      - `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Remote_File_Management.jsx`
    - Core interaction model:
      - open the menu at the pointer using MUI `Menu` with `anchorReference="anchorPosition"`
      - use a dark glass paper surface with `rgba(8,12,24,0.96)` background, shared panel border, backdrop blur, about `8px` radius, and a little inner padding so grouped sections have room to breathe
      - keep menu copy compact, with bright foreground text, left-aligned icons, and enough height for a short helper line when a page needs one
    - Right-click behavior:
      - if the operator right-clicks an unselected row, select that row first and then open the menu
      - if the operator right-clicks an already selected row, preserve the existing selection
      - right-clicking empty grid space may still open a context menu when the page has useful non-row actions such as `New Folder` or `Collapse All`
    - Native browser menu suppression:
      - when Borealis intentionally exposes its own right-click context menu, suppress the browser's native context menu for that interaction target so the operator receives the Borealis menu consistently
      - wire suppression at every menu entrypoint, including DOM `onContextMenu` handlers and component-library context-menu hooks
      - on AG Grid and similar surfaces, pair the Borealis menu trigger with available grid-level context-menu suppression settings so the browser menu does not appear underneath or instead of the Borealis menu
      - do not suppress the browser menu on unrelated surfaces that do not provide a Borealis context menu
    - Context-menu anatomy:
      - a context header row belongs at the top of object-aware menus; use it to show the current object icon plus one short title and one concise subtitle
      - header title and subtitle should both stay single-line; if either value can ellipsize, expose the full value on hover with a tooltip instead of leaving the operator with raw trailing `...`
      - below the header, group actions into semantic sections instead of presenting one uninterrupted list
      - use small uppercase section labels for those groups when the menu contains more than one action family
      - separate groups with subtle dividers rather than large visual blocks
    - Standard group order:
      - `Primary`
      - `Organize`
      - `Danger Zone`
      - `View`
    - Action schema:
      - define context-menu items from a structured action object instead of hand-authoring one-off `MenuItem` lists whenever practical
      - the recommended fields are:
        - `id`
        - `label`
        - `icon`
        - `group`
        - `intent`
        - `shortcut`
        - `description`
        - `disabled`
        - `disabledReason`
        - `hidden`
        - `onClick`
    - Hidden vs disabled:
      - hide actions that are not relevant to the current object type
      - disable actions that are relevant but temporarily unavailable
      - when an action is disabled and the reason is not obvious, provide a short inline `disabledReason` instead of leaving the operator to guess
      - keep section labels visible even when every action inside that section is disabled; do not auto-collapse whole sections away
    - Menu composition:
      - put the most common direct actions first
      - keep destructive actions visually in the same family as the rest of the menu unless the product explicitly needs a destructive accent, but give the destructive section its own placement and a subtle danger tint on icon/hover so it reads as intentional
      - preserve useful action icons on the left side of each item rather than hiding them in trailing affordances
      - only show trailing shortcut hints when the shortcut actually exists in the product; do not imply unimplemented keyboard bindings
      - keep primary action labels single-line; prefer shorter copy or ellipsis over wrapping normal action labels across multiple lines
      - helper copy such as `description` and `disabledReason` may wrap when needed
      - single-line action rows should vertically center the icon and label content; two-line rows that include helper copy should remain top-aligned so the label-to-helper spacing stays readable
      - hovered actionable rows should show a subtle Borealis-blue left-edge accent bar in addition to the row hover fill so the active target feels anchored
    - Menu sizing and overflow:
      - use one of the approved Borealis menu width variants instead of inventing page-specific widths:
        - `compact`: short utility menus with a small number of terse actions and no object header
        - `standard`: the default for row-level and object-level action menus across Borealis
        - `extended`: rare metadata-heavy, compare-heavy, or decision-heavy menus that need extra width to stay readable
      - default to `standard` unless there is a clear reason to use `compact` or `extended`
      - `standard` menus should keep the same width across normal implementations so the product feels consistent; content should adapt inside that width rather than forcing each page to size menus unpredictably
      - do not introduce one-off page-specific widths unless the design language in this document is updated to approve a new variant
      - header title and subtitle should ellipsize within the chosen width rather than expanding the menu
      - helper copy may wrap within the chosen width; normal action labels should not
    - Selection-aware behavior:
      - one selected item may expose object-specific actions such as `Edit`, `Rename`, or `Properties`
      - multi-selection should automatically collapse to bulk-safe actions
      - empty-space context menus may expose location-scoped or view-scoped actions and should use the current location in the header row
    - Reference implementation:
      - `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Remote_File_Management.jsx` demonstrates the preferred Borealis anatomy:
        - context header
        - ellipsis-safe header tooltips
        - grouped sections
        - `standard` width behavior
        - destructive section placement
        - inline disabled reasons
        - mixed single-line/two-line row alignment
        - Borealis-blue left-edge hover accent
    - Filesystem browser reference ordering:
      - `Upload`
      - `Download`
      - `Edit`
      - `Rename`
      - `Delete`
      - `Move`
      - `New Folder`
      - `Collapse All`

    #### Filter Slider
    - Official name: `Filter Slider`.
    - Purpose: a compact segmented control for switching between a small number of mutually exclusive grid views such as queue states, lifecycle states, or source categories.
    - Placement: render it at the top-left of the `stack` area on `grid_with_stack` pages or at the top-left of the local control row above a tabbed grid. Pair it with a short summary on the right when space allows.
    - Label placement: every Filter Slider must have a short label directly above it. When a page has multiple sliders, render them side by side in individual column blocks with `8px` label-to-slider spacing and about `8px` horizontal spacing between slider blocks.
    - Label styling: match the Assemblies page filter labels: Borealis blue `#58a6ff`, `11px` font size, `600` font weight, `1.1` line height, and `8px` left padding so the label aligns with the slider shell.
    - Visual treatment: use the rounded glass shell with `4px` inner padding, inactive transparent pills, active cyan-to-violet gradient pill, and inline count chips shown inside each segment when counts are useful.
    - Labels: keep labels short and mixed case, for example `Active`, `Archived`, `Windows Store`, or `Locally Installed`.
    - Counts: show live counts when the page has them; `0` is acceptable for placeholder categories that are not fully implemented yet.
    - Selection behavior: pages may default to the highest-signal view when one is clearly preferred, but clicking the currently active segment should clear the filter and return the page to an unfiltered all-items view.
    - Summary copy: use a short sentence to the right of the control, for example `Showing 14 active filters` or `Showing 32 locally installed entries`.
    - Current implementation note: many pages still use a helper named `CountSliderGroup`. Treat that helper as the current code implementation of the Filter Slider pattern until Borealis consolidates it under the new name.

    #### Dialog Boxes and Confirmation Modals
    - Treat dialogs as compact glass overlays, not miniature pages. Use the same dark glass canvas as other overlays: blurred backdrop, thin steel border, rounded corners, and bright typography on a navy-violet surface.
    - Keep dialog chrome quiet. Do not add horizontal separator lines between title, body, and actions unless a specific workflow truly needs one.
    - Default header pattern: plain title first, optional single concise subtitle beneath it. Do not place a decorative glyph to the left of the title by default.
    - Keep the body direct and task-focused. Form dialogs should move immediately into the fields instead of leading with large explainer cards or stacked warning blocks.
    - Field order should follow operator intent. For example, create/edit dialogs should place the primary name field first, then secondary fields such as description.
    - Dialog text fields use the same deep blue glass treatment as other MagicUI inputs. Ensure the idle label sits visually centered within the field, and ensure the floating/shrunken label has enough top clearance so it is never clipped when focused.
    - Leave a little extra top spacing above the first dialog field so floating labels do not collide with the title area.
    - Dialog actions use the same pill sizing and typography as the shared page action rail: about `38px` tall, radius `999`, IBM Plex Sans, `fontWeight: 600`, mixed case labels.
    - Dialog primary actions keep the Borealis cyan-to-violet gradient, but do not add a glow behind the button. Confirm buttons should feel crisp rather than luminous.
    - Dialog secondary actions should be soft outline buttons on the dark surface. Dialog destructive confirms should use a red/mauve outline treatment instead of a filled bright red block.
    - Use explicit action labels for destructive dialogs when helpful. `Delete Site(s)` is preferred over a vague `Confirm` when the object type is known.
    - Confirmation dialogs should be simplified to the minimum information needed to make a safe decision. Prefer a short subtitle plus the selected-object preview instead of repeating multiple warning paragraphs.

    #### Page-Level Tabs
    - Header ownership: `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/shell/AppShell.jsx` owns the page title, subtitle, icon, and the page action rail. Routed pages should publish header metadata through `useRoutePageChrome()` or `usePageChrome()`.
    - Header action contract:
    ```jsx
    useRoutePageChrome({
      title: pageTitle,
      subtitle: pageSubtitle,
      Icon: PageIcon,
      controls: [statusSelectControl],
      actions: [
        { id: "refresh", label: "Refresh", icon: <RefreshIcon />, tone: "secondary", onClick: loadData },
        { id: "new-item", label: "New Item", icon: <AddIcon />, tone: "primary", onClick: handleCreate },
      ],
    });
    ```
    - Shared implementation: use `Data/Engine/Containers/webui-frontend/data/web-interface/src/Page_Header_Actions.jsx` for the action-rail renderer and the shared button/control tokens. Standalone pages that render without App should use the same `PageHeaderActionRail` component locally instead of re-creating header buttons.
    - Action rail placement: the rail lives inside the shared header band in normal document flow, aligned to the top-right of the page title block. Do not use `position: "fixed"` for page-level actions.
    - Rail ordering: pages declare actions in their final left-to-right display order. Supporting actions should be listed first and primary actions should be listed last so the main CTA stays on the far right.
    - Button tones:
    - `primary`: cyan-to-violet gradient, dark text. Use for page-defining create/save/launch actions such as `Create Site`, `New Filter`, `Quick Job`, `Add Device`, `About Borealis`, and `Save Assembly`.
    - `secondary`: muted outline, steel border, bright text. Use for passive or supporting actions such as `Refresh`, `Columns`, `Rename`, `GitHub Project`, `Cancel`, and `Settings`.
    - `warning`: amber outline. Use sparingly for high-risk admin toggles such as `Enable Dev Mode` and `Flush Queue`.
    - `danger`: red outline. Use sparingly for destructive actions such as `Delete`.
    - Default authoring rule: think in `primary` and `secondary` first. Only introduce `warning` or `danger` when the semantics are clear and the action truly needs that emphasis.
    - Multiple primary actions are allowed when a page has more than one legitimate top-level outcome, but use them sparingly to avoid clutter. Pages with no obvious CTA may use only secondary actions.
    - Strict action sizing: height `38px`, pill radius `999`, IBM Plex Sans, `fontWeight: 600`, `textTransform: "none"`, icon plus label by default. Do not introduce page-specific button heights, widths, or alternate fonts in the rail.
    - Creation CTA rule: page-header creation buttons use the shared `primary` gradient. `Quick Job` uses the same primary treatment; there is no separate accent tone in the header rail.
    - Controls in the rail: simple controls such as the Device Approval Queue `Status` select are allowed. Style them with the shared control token from `Page_Header_Actions.jsx` so they match the rail height and border treatment.
    - Floating control labels: rail dropdowns that use MUI `InputLabel`/`Select` need a small amount of top clearance so the shrunken label is never clipped by the header band.
    - Rail alignment for controls: when a rail contains floating-label controls, keep that internal top clearance but offset the rail upward slightly so the overall action row still aligns with adjacent header buttons instead of appearing dropped lower in the header.
    - Label treatment: the floating label text should blend directly into the page/header background. Do not add a chip, pill, or opaque backdrop behind the label text.
    - Badges/metadata: do not standardize badges as part of the shared header rail. Page-specific metadata belongs in the page body, usually directly under the subtitle or near the first relevant control group.
    - Secondary action overflow: on narrow widths, if the full rail no longer fits on a single row, the shared rail automatically collapses all secondary actions into a single `Actions` secondary button while keeping primary/warning/danger actions visible. Overflow menu ordering should place the secondary action closest to the primary buttons at the top of the menu.
    - Some tabbed surfaces may still expose page-local action menus when the actions are truly tab-scoped rather than row-scoped. Use `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Device_Summary.jsx` as an example implementation, but keep the shared action-rail rules in this document as the authority.
    - `Update Agent` is an operator-triggered AutoUpdater start. It tells the device to run its existing local updater task immediately rather than using a separate direct-launch updater path.
    - Workflow and targeting UIs that consume `/api/agents` should treat helper-backed current-user capability as metadata on the host's SYSTEM record (`helper_contexts`), not as a second live Borealis socket.
    - Responsive behavior: tabs remain in normal flow beneath the header band. On narrow widths, the title block and action rail stack vertically. The rail should prefer collapsing secondary actions before wrapping and must never cover the tabs or the first content section.
    - Placement: tabs sit directly below the shared header band (8-16px gap). Tabs span the full width of the content column.
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
    - Representative action rail mappings:
    - `Delete | Rename | Create Site`
    - `Columns | Refresh | Quick Job | Add Device`
    - `Refresh | New Filter`
    - `Refresh | GitHub Project | About Borealis`
    - `GitHub Project | About Borealis`
    - `Cancel | Save Filter`
    - Canonical examples:
    - Visual reference page: `Data/Engine/Containers/webui-frontend/data/web-interface/src/DevTools/Page_Style_Template.jsx`
    - Shared header owner: `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/shell/AppShell.jsx`
    - Shared button rail/tokens: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Page_Header_Actions.jsx`

    #### URL-Synced Tabs and Deep Links
    - Use URL paths for resource identity and `?tab=` for active tab state.
    - Route ownership lives in `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/routes/router.jsx` and `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/routes/paths.js`.
    - Use canonical ID-backed routes without `/edit` suffixes. If a route contains the resource identifier, that route is the edit/view surface for that resource.
    - Create route examples: `/jobs/new`, `/filters/new`, `/assemblies/new/script`
    - Existing-resource route examples: `/jobs/<job_id>`, `/filters/<filter_id>`, `/assemblies/workflows/<workflow_guid>`
    - Prefer `useUrlTabState(...)` for tab synchronization. Use `useSearchParams()` directly only when the page needs more custom query behavior.
    - In each tabbed page component:
    - Read the active tab from `useUrlTabState(...)` or `useSearchParams()`.
    - Write the active tab back through the same helper with `replace: true` so browser history does not spam each tab click.
    - Keep stable URL keys and map them to internal keys; internal keys can stay implementation-specific.
    - Maintain backward compatibility with alias mapping (old tab keys -> new tab keys) when renaming tab params.
    - Recommended URL tab key style: lowercase snake_case (for example, `execution_context`, `remote_desktop`).
    - Scheduled Job editor keys in production:
    - `job_name`
    - `assemblies`
    - `targets`
    - `schedule`
    - `execution_context`
    - `job_history` (editing mode only)
    - Device Details keys in production:
    - `device_summary`
    - `installed_software`
    - `services`
    - `process_management`
    - `activity_history`
    - `remote_shell`
    - `remote_desktop`
    - New pages adopting this pattern should document their canonical tab key list in their domain doc and keep aliases local to the component.

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
    - Row height baseline: use `rowHeight={44}` and `headerHeight={44}` for standard Quartz data grids. This is the rounded average of the current Device Approvals and Device List Quartz default row height plus the Scheduled Jobs List `46px` row height, and keeps dense operational grids visually aligned. Only deviate for explicitly compact detail grids, oversized card-like rows, or embedded controls that need more vertical space.
    - Pagination baseline: every Quartz grid ships with `pagination`, `paginationPageSize={20}`, and `paginationPageSizeSelector={[20, 50, 100]}`. This matches Device List behavior and prevents infinitely tall tables (Targets, assembly pickers, job histories, etc.).
    - Full-height grid baseline: when no content is expected below a grid, let the grid wrapper fill remaining page body height with `flexGrow: 1`, `minHeight: 0`, and `height: "100%"` instead of fixed pixel heights. Keep normal page/body padding around the wrapper so the grid visually reaches the bottom of the page while preserving the same bottom inset as Device List, Filters, and Assemblies.
    - Focus highlight baseline: set `suppressCellFocus` on AG Grid tables that do not rely on keyboard cell navigation so click/focus does not draw the magenta single-cell outline. Keep row hover/row selected styling for context.
    - Clickable name cells baseline: when a grid cell is just a row navigation trigger (for example Device hostname or Scheduled Job name), render plain text links (`<a>` with `textDecoration: "none"`) instead of MUI `Button`/`IconButton` so hover only shows a pointer cursor with no button box/highlight.
    - Borealis blue accent baseline: use `themeQuartz.withParams({ accentColor: "#7dd3fc" })`. Do not add custom checkbox styling overrides; let Quartz render checkbox visuals by default.
    - Row hover baseline: use a darker Borealis blue hover state (`--ag-row-hover-color: "rgba(73,156,196,0.2)"`) so hover stays in the same color family as selected rows without matching the selected intensity.
    - Selected row highlight baseline: use `backgroundColor: "rgba(125,211,252,0.2) !important"` with `boxShadow: "inset 0 0 0 1px rgba(125,211,252,0.45)"` so checkbox selection state reads in Borealis blue.
    - Inline grid text inputs: when a page embeds a MUI `TextField` inside AG Grid (for example, Device Description), use `MAGIC_UI.accentA` (`#7dd3fc`) for hover/focus border color.
    - Example: follow the scaffolding in `Engine/Services/webui-frontend/cache/web-interface/src/Scheduling/Scheduled_Jobs_List.jsx` and the structure in `Data/Engine/Containers/webui-frontend/data/web-interface/src/DevTools/Page_Style_Template.jsx`.
    - Server Info layout pattern: use Quartz AG Grid sections for service state, public-edge certificates, and live operator sessions, with concise glass summary cards above the grids for the most important runtime signals.

    ### Toast Notifications (Full)
    Use this guide to add, configure, and test transient toast notifications across Borealis. It documents the backend endpoint, frontend listener, payload contract, and quick Firefox console commands you can hand to operators for validation.

    #### Components and paths
    - Backend endpoint: `Data/Engine/Containers/api-backend/data/services/API/notifications/management.py` (registered as `/api/notifications/notify`).
    - Frontend listener and renderer: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Notifications.jsx` (mounted in `src/app/shell/AppShell.jsx`).
    - Transport: Socket.IO event `borealis_notification` broadcast to connected WebUI clients.

    #### Backend behavior
    - Auth: Uses `RequestAuthContext.require_user()`; session or bearer must be present. Returns `401/403` otherwise.
    - Route: `POST /api/notifications/notify`
      - Emits `borealis_notification` over Socket.IO (no persistence).
      - Logs via `service_log("notifications", ...)`.
    - Validation: Requires `message` in payload. `title` defaults to `"Notification"` if omitted.
    - Registration: API group `notifications` is enabled by default via `DEFAULT_API_GROUPS` and `_GROUP_REGISTRARS` in `Data/Engine/Containers/api-backend/data/services/API/__init__.py`.

    #### Payload schema
    Send JSON body (session-authenticated):
    - `title` (string, optional): heading line. Default `"Notification"`.
    - `message` (string, required): body copy.
    - `icon` (string, optional): Material icon name hint (for example, `info`, `filter`, `schedule`, `warning`, `error`). Falls back to `NotificationsActive`.
    - `variant` (string, optional): visual theme. Accepted: `info` | `warning` | `error` (case-insensitive). Aliases: `type` or `severity`. Defaults to `info`.
    - `username` (string, optional): when present, scope the toast to a single signed-in operator. This is the preferred pattern for admin actions that should only notify the initiator.
    - `ttl_ms` (number, optional): client-side lifetime in milliseconds; defaults to about 5200ms before fade-out.

    Notes:
    - Payload is fanned out verbatim to the WebUI (plus server-added fields: `id`, `username`, `role`, `created_at`).
    - The client caps the visible stack to the 5 most recent items (newest on top).
    - If `username` is present, the WebUI only renders that toast for the matching signed-in operator. Leave it unset for broadcast informational toasts.
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
    3) Frontend: `AppShell.jsx` mounts `Notifications` globally; page-local actions can either post to `/api/notifications/notify` directly or reuse the shared `postAppNotification()` helper in `src/app/utils/notifications.js`.
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
    - UI file: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/ReverseTunnel/Powershell.jsx`.
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
