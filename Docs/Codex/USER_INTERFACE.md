# Codex Guide: Shared UI (MagicUI + AG Grid)

Applies to all Borealis frontends. Use `Data/Engine/web-interface/src/Admin/Page_Template.jsx` as the canonical visual reference (no API/business logic). Keep this doc as the single source of truth for styling rules and AG Grid behavior.

- Toast notifications: see `Docs/Codex/TOAST_NOTIFICATIONS.md` for endpoint, payload, severity variants, and quick test commands.

## Page Template Reference
- Purpose: visual-only baseline for new pages; copy structure but wire your data in real pages.
- Header: small Material icon left of the title, subtitle beneath, utility buttons on the top-right.
- Shell: avoid gutters on the Paper.
- Selection column (for bulk actions): pinned left, square checkboxes, header checkbox enabled, ~52px fixed width, no menu/sort/resize; rely on AG Grid built-ins.
- Typography/buttons: IBM Plex Sans, gradient primary buttons, rounded corners (~8px), themed Quartz grid wrapper.

## MagicUI Styling Language (Visual System)
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
- Default grid cell padding: keep roughly 18px on the left edge and 12px on the right for standard cells (12px/9px for `auto-col-tight`) so text never hugs a column edge. Target the center + pinned containers so both regions stay aligned.
- Overlays/menus: `rgba(8,12,24,0.96)` canvas, blurred backdrops, thin steel borders; bright typography; deep blue glass inputs; cyan confirm, mauve destructive accents.

## Aurora Tabs (MagicUI Tabbed Interfaces)
- Placement: sit directly below the hero title/subtitle band (8–16px gap). Tabs span the full width of the content column.
- Typography: IBM Plex Sans, `fontSize: 15`, mixed case labels (`textTransform: "none"`). Use `fontWeight: 600` for emphasis, but avoid uppercase that crowds the aurora glow.
- Indicator: 3px tall bar with rounded corners that uses the cyan→violet aurora gradient `linear-gradient(90deg,#7dd3fc,#c084fc)`. Keep it flush with the bottom border so it looks like a light strip under the active tab.
- Hover/active treatment: tabs float on a translucent aurora panel `linear-gradient(120deg, rgba(125,211,252,0.18), rgba(192,132,252,0.22))` with a 1px inset steel outline. This gradient applies on hover for both selected and non-selected tabs to keep parity.
- Colors: base text `MAGIC_UI.textMuted` (`#94a3b8`). Hovering switches to `MAGIC_UI.textBright` (`#e2e8f0`). Always force `opacity: 1` to avoid MUI’s default faded text on unfocused tabs.
- Shape/spacing: tabs are pill-like with `borderRadius: 4` (MUI unit `1`). Maintain `minHeight: 44px` so targets are touchable. Provide `borderBottom: 1px solid MAGIC_UI.panelBorder` to anchor the rail.
- CSS/SX snippet to copy into new tab stacks:
  ```jsx
  const TAB_HOVER_GRADIENT = "linear-gradient(120deg, rgba(125,211,252,0.18), rgba(192,132,252,0.22))";

  <Tabs
    value={tab}
    onChange={(_, v) => setTab(v)}
    variant="scrollable"
    scrollButtons="auto"
    TabIndicatorProps={{
      style: {
        height: 3,
        borderRadius: 3,
        background: "linear-gradient(90deg,#7dd3fc,#c084fc)",
      },
    }}
    sx={{
      borderBottom: `1px solid ${MAGIC_UI.panelBorder}`,
      "& .MuiTab-root": {
        color: MAGIC_UI.textMuted,
        fontFamily: "\"IBM Plex Sans\", \"Helvetica Neue\", Arial, sans-serif",
        fontSize: 15,
        textTransform: "none",
        fontWeight: 600,
        minHeight: 44,
        opacity: 1,
        borderRadius: 1,
        transition: "background 0.2s ease, color 0.2s ease, box-shadow 0.2s ease",
        "&:hover": {
          color: MAGIC_UI.textBright,
          backgroundImage: TAB_HOVER_GRADIENT,
          boxShadow: "0 0 0 1px rgba(148,163,184,0.25) inset",
        },
      },
      "& .Mui-selected": {
        color: MAGIC_UI.textBright,
        "&:hover": {
          backgroundImage: TAB_HOVER_GRADIENT,
        },
      },
    }}
  >
    {TABS.map((t) => (
      <Tab key={t} label={t} />
    ))}
  </Tabs>
  ```
- Interaction rules: tabs should never scroll vertically; rely on horizontal scroll for overflow. Always align the tab rail with the first section header on the page so the aurora indicator lines up with hero metrics.
- Accessibility: keep `aria-label`/`aria-controls` pairs when the panes hold complex content, and ensure the gradient backgrounds preserve 4.5:1 contrast for the text (the current cyan on dark meets this).

## Page-Level Actions with Tab Rails
- When a page uses Aurora Tabs, place primary/secondary page actions inline with the tab rail, floating on the top-right of the content layer (under the global nav).
- Wrap the tab stack and actions in a `position: relative` container so actions can be absolutely positioned without leaving the shell flow.
- Position the action bar as `position: "absolute", top: 12, right: 24, zIndex: 3` (adjust top/right to match your page padding) and keep it `display: "flex"` with a `Stack` for spacing.
- Use the same gradient primary pill and outlined secondary styles from the template; preserve the rounded 999 radius and MagicUI colors.
- Keep the bar above content but separate from the title/subtitle block—do not nest buttons inside the title grid. This matches the Filter Editor pattern and keeps tabs and actions visually aligned.

## AG Grid Column Behavior (All Tables)
- Auto-size value columns and let the last column absorb remaining width so views span available space.
- Declare `AUTO_SIZE_COLUMNS` near the grid component (exclude the fill column).
- Helper: store the grid API in a ref and call `api.autoSizeColumns(AUTO_SIZE_COLUMNS, true)` inside `requestAnimationFrame` (or `setTimeout(...,0)` fallback); swallow errors because it can run before rows render.
- Hook the helper into both `onGridReady` and a `useEffect` watching the dataset (e.g., `[filteredRows, loading]`); skip while `loading` or when there are zero rows.
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
- Example: follow the scaffolding in `Engine/web-interface/src/Scheduling/Scheduled_Jobs_List.jsx` and the structure in `Data/Engine/web-interface/src/Admin/Page_Template.jsx`.
