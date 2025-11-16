# Codex Guide: Shared UI (MagicUI + AG Grid)

Applies to all Borealis frontends. Use `Data/Engine/web-interface/src/Admin/Page_Template.jsx` as the canonical visual reference (no API/business logic). Keep this doc as the single source of truth for styling rules and AG Grid behavior.

## Page Template Reference
- Purpose: visual-only baseline for new pages; copy structure but wire your data in real pages.
- Header: small Material icon left of the title, subtitle beneath, utility buttons on the top-right.
- Shell: full-bleed aurora gradient container; avoid gutters on the Paper.
- Selection column (for bulk actions): pinned left, square checkboxes, header checkbox enabled, ~52px fixed width, no menu/sort/resize; rely on AG Grid built-ins.
- Typography/buttons: IBM Plex Sans, gradient primary buttons, rounded corners (~8px), themed Quartz grid wrapper.

## MagicUI Styling Language (Visual System)
- Aurora shells: gradient backgrounds blending deep navy (#040711) with soft cyan/violet blooms, subtle borders (`rgba(148,163,184,0.35)`), and low, velvety shadows.
- Full-bleed canvas: hero shells run edge-to-edge; inset padding lives inside cards so gradients feel immersive.
- Glass panels: glassmorphic layers (`rgba(15,23,42,0.7)`), rounded 16–24px corners, blurred backdrops, micro borders, optional radial flares for motion.
- Hero storytelling: start views with stat-forward heroes—gradient StatTiles (min 160px) and uppercase pills (HERO_BADGE_SX) summarizing live signals/filters.
- Summary data grids: use AG Grid inside a glass wrapper (two columns Field/Value), matte navy background, no row striping.
- Tile palettes: online cyan→green; stale orange→red; “needs update” violet→cyan; secondary metrics fade from cyan into desaturated steel for consistent hue families.
- Hardware islands: storage/memory/network blocks reuse Quartz theme in rounded glass shells with flat fills; present numeric columns (Capacity/Used/Free/%) to match Device Inventory.
- Action surfaces: control bars live in translucent glass bands; filled dark inputs with cyan hover borders; primary actions are pill-shaped gradients; secondary controls are soft-outline icon buttons.
- Anchored controls: align selectors/utility buttons with grid edges in a single row; reserve glass backdrops for hero sections so content stays flush.
- Buttons & chips: gradient pills for primary CTAs (`linear-gradient(135deg,#34d399,#22d3ee)` success; `#7dd3fc→#c084fc` creation); neutral actions use rounded outlines with `rgba(148,163,184,0.4)` borders and uppercase microcopy.
- Rainbow accents: for creation CTAs, use dark-fill pills with rainbow border gradients + teal halo (shared with Quick Job).
- AG Grid treatment: Quartz theme with matte navy headers, subtle alternating row opacity, cyan/magenta interaction glows, rounded wrappers, soft borders, inset selection glows.
- Overlays/menus: `rgba(8,12,24,0.96)` canvas, blurred backdrops, thin steel borders; bright typography; deep blue glass inputs; cyan confirm, mauve destructive accents.

## AG Grid Column Behavior (All Tables)
- Auto-size value columns and let the last column absorb remaining width so views span available space.
- Declare `AUTO_SIZE_COLUMNS` near the grid component (exclude the fill column).
- Helper: store the grid API in a ref and call `api.autoSizeColumns(AUTO_SIZE_COLUMNS, true)` inside `requestAnimationFrame` (or `setTimeout(...,0)` fallback); swallow errors because it can run before rows render.
- Hook the helper into both `onGridReady` and a `useEffect` watching the dataset (e.g., `[filteredRows, loading]`); skip while `loading` or when there are zero rows.
- Column defs: apply shared `cellClass: "auto-col-tight"` (or equivalent) to every auto-sized column for consistent padding. Last column keeps the class for styling consistency.
- CSS override: add `& .ag-cell.auto-col-tight { padding-left: 0; padding-right: 0; }` in the theme scope.
- Fill column: last column `{ flex: 1, minWidth: X }` (no width/maxWidth) to stretch when horizontal space remains.
- Example: follow the scaffolding in `Engine/web-interface/src/Scheduling/Scheduled_Jobs_List.jsx` and the structure in `Data/Engine/web-interface/src/Admin/Page_Template.jsx`.
